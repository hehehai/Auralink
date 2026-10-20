package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/uitext"
	"github.com/hehehai/actnowkit/family"
	akup "github.com/hehehai/actnowkit/updates"
)

// fakeFeed is MyGo's updater in tests: the release a check finds, how its
// install goes, and what Auralink asked of it.
type fakeFeed struct {
	mu       sync.Mutex
	enabled  bool
	release  *akup.Release
	checkErr error
	checks   int
	installs int
	// during runs inside the install, at half the download (the test
	// looks at the window then); installErr fails it.
	during     func()
	installErr error
	// pre is each SetPrereleases.
	pre []bool
}

func (f *fakeFeed) funcs() akup.Funcs {
	return akup.Funcs{
		EnabledFunc: func() bool { return f.enabled },
		CheckFunc: func(context.Context) (*akup.Release, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.checks++
			return f.release, f.checkErr
		},
	}
}

func (f *fakeFeed) checked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

func (f *fakeFeed) prereleases() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pre) > 0 && f.pre[len(f.pre)-1]
}

// offer makes the feed offer version, with English notes from the
// manifest; its install reports progress and calls during.
func (f *fakeFeed) offer(version string) {
	f.release = &akup.Release{Version: version, Notes: "### Added\n\n- From the manifest", Date: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
		Install: func(ctx context.Context, progress func(done, total int64)) error {
			f.installs++
			progress(0, 24<<20)
			time.Sleep(120 * time.Millisecond) // the manager passes ~10 a second on
			progress(12<<20, 24<<20)
			if f.during != nil {
				f.during()
			}
			if f.installErr != nil {
				return f.installErr
			}
			progress(24<<20, 24<<20)
			return nil
		}}
}

// changelogTransport answers /api/changelog/auralink like actnow.dev,
// without a network: 0.1.0 in both languages, 0.1.0-beta.3 (beta) in
// English only, 0.1.0-beta.2; betas only when asked for.
type changelogTransport struct {
	mu    sync.Mutex
	asked []string
	down  bool
}

func (c *changelogTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.asked = append(c.asked, r.URL.RawQuery)
	down := c.down
	c.mu.Unlock()
	if down || r.URL.Path != "/api/changelog/auralink" {
		return nil, errors.New("dial tcp: no route to host")
	}
	zh := r.URL.Query().Get("locale") == "zh-Hans"
	beta := r.URL.Query().Get("channel") == "beta"
	entry := func(v, channel, date, en, cn string) map[string]any {
		notes, loc := en, "en"
		if zh && cn != "" {
			notes, loc = cn, "zh-Hans"
		}
		return map[string]any{"version": v, "channel": channel, "date": date, "notes": notes, "locale": loc, "requiresPaidLicence": false}
	}
	entries := []any{entry("0.1.0", "stable", "2026-10-30",
		"### Added\n\n- **Open source** under the AGPL-3.0\n- Rewrite answers in the language you spoke\n\n### Fixed\n\n- The overlay no longer covers the menu bar",
		"### 新增\n\n- 以 AGPL-3.0 **开源**\n- 改写按你说话的语言回答\n\n### 修复\n\n- 浮层不再遮住菜单栏")}
	if beta {
		entries = append(entries, entry("0.1.0-beta.3", "beta", "2026-10-25", "### Fixed\n\n- Translate keeps the line breaks", ""))
	}
	entries = append(entries, entry("0.1.0-beta.2", "beta", "2026-10-19", "### Added\n\n- The first beta on actnow.dev", "### 新增\n\n- 第一个在 actnow.dev 发布的测试版"))
	body, _ := json.Marshal(map[string]any{"app": "auralink", "locale": map[bool]string{true: "zh-Hans", false: "en"}[zh], "paidSince": nil, "entries": entries})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
}

// testUpdates are the fakes behind each test app's updater.
type testUpdates struct {
	feed      *fakeFeed
	site      *changelogTransport
	restarted int
}

var (
	testUpdatesMu sync.Mutex
	testUpdatesOf = map[*app]*testUpdates{}
)

// useTestUpdates gives a test app the in-app updater over a fake feed (a
// build that can update itself, up to date) and changelog
// (newTestAppIn).
func useTestUpdates(t *testing.T, a *app) {
	u := &testUpdates{feed: &fakeFeed{enabled: true}, site: &changelogTransport{}}
	testUpdatesMu.Lock()
	testUpdatesOf[a] = u
	testUpdatesMu.Unlock()
	t.Cleanup(func() {
		testUpdatesMu.Lock()
		delete(testUpdatesOf, a)
		testUpdatesMu.Unlock()
	})
	a.setPrereleases = func(on bool) {
		u.feed.mu.Lock()
		u.feed.pre = append(u.feed.pre, on)
		u.feed.mu.Unlock()
	}
	a.upd = akup.New(akup.Config{
		Updater: u.feed.funcs(), Current: "0.1.0-beta.2",
		History:  &akup.HistoryClient{App: "auralink", HTTPClient: &http.Client{Transport: u.site}},
		Language: i18n.Locale,
		Beta:     func() bool { return a.betaOn() },
		Restart:  func() { u.restarted++ },
	})
	a.connectUpdates()
}

func updatesOf(a *app) *testUpdates {
	testUpdatesMu.Lock()
	defer testUpdatesMu.Unlock()
	return testUpdatesOf[a]
}

// TestUpdateFlow: the daily check only shows "Update available" at the
// sidebar's foot (no window); the panel has the version, its notes from
// actnow.dev, the download's progress, and the restart.
func TestUpdateFlow(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.version = "0.1.0-beta.2"
	u := updatesOf(a)
	u.feed.offer("0.1.0")
	a.upd.Check(context.Background()) // as the daily check does
	if !has(tt, "Update available") || a.updPanel || has(tt, "Download and Install") {
		t.Fatalf("after the daily check: %q", firstTexts(tt))
	}
	click(t, tt, "Update available")
	for _, s := range []string{"Auralink 0.1.0 is available", "You have 0.1.0-beta.2 · Oct 30, 2026", "Open source", "Download and Install", "Later"} {
		if !has(tt, s) {
			t.Errorf("no %q in the panel: %q", s, tt.Texts())
		}
	}
	var during []string
	u.feed.during = func() {
		frames(tt)
		during = tt.Texts()
	}
	a.installUpdate() // as Download and Install does, outside a frame
	for _, s := range []string{"Downloading Auralink 0.1.0", "Downloading… 50% · " + uitext.HumanBytes(12<<20) + " / " + uitext.HumanBytes(24<<20), "Updating… 50%", "Hide"} {
		if !slices.Contains(during, s) {
			t.Errorf("no %q while downloading: %q", s, during)
		}
	}
	for _, s := range []string{"Auralink 0.1.0 is installed", "The new version runs once Auralink restarts.", "Restart to Finish the Update", "Restart to update"} {
		if !has(tt, s) {
			t.Errorf("no %q once installed: %q", s, tt.Texts())
		}
	}
	if slices.ContainsFunc(a.notices, func(n core.Notification) bool { return n.ID == problemUpdate }) {
		t.Error("a card while the panel asks for the restart")
	}
	click(t, tt, "Restart to Finish the Update")
	if u.restarted != 1 {
		t.Errorf("restarted %d", u.restarted)
	}
	if q := u.site.asked; len(q) == 0 || !strings.Contains(q[0], "channel=beta") {
		t.Errorf("changelog asked %q", q)
	}
}

// TestUpdateFailed: a failed download says Auralink is unchanged; Try
// again checks again. A check asked for that fails says so.
func TestUpdateFailed(t *testing.T) {
	a, _, tt := newTestApp(t)
	u := updatesOf(a)
	u.feed.offer("0.1.1")
	u.feed.installErr = errors.New("mygo: signature does not match")
	a.checkForUpdates()
	a.installUpdate()
	if !has(tt, "The update did not install: signature does not match. Auralink is unchanged.") || !has(tt, "Try again") {
		t.Fatalf("failed: %q", tt.Texts())
	}
	u.feed.installErr = nil
	click(t, tt, "Try again")
	if u.feed.checked() != 2 || !has(tt, "Download and Install") {
		t.Errorf("checks %d: %q", u.feed.checked(), tt.Texts())
	}

	b, _, tt2 := newTestApp(t)
	updatesOf(b).feed.checkErr = errors.New("GET https://actnow.dev/updates/auralink/update-darwin-arm64.json: 503")
	b.checkForUpdates()
	if !has(tt2, "Cannot check for updates: GET https://actnow.dev/updates/auralink/update-darwin-arm64.json: 503") {
		t.Errorf("check failed: %q", tt2.Texts())
	}
}

// TestUpdateNotesLanguage: in Chinese the notes are actnow.dev's Chinese
// ones; a version without them shows the English, marked.
func TestUpdateNotesLanguage(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.version = "0.1.0-beta.2"
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	u := updatesOf(a)
	u.feed.offer("0.1.0")
	a.checkForUpdates()
	if !has(tt, "Auralink 0.1.0 已发布") || !strings.Contains(strings.Join(tt.Texts(), " "), "开源") || has(tt, "这个版本暂无中文说明，以下为英文。") {
		t.Errorf("not the Chinese notes: %q", tt.Texts())
	}
	a.updPanel = false
	a.upd.Dismiss()
	u.feed.offer("0.1.0-beta.3")
	a.checkForUpdates()
	if !has(tt, "这个版本暂无中文说明，以下为英文。") || !has(tt, "Translate keeps the line breaks") {
		t.Errorf("English notes: %q", tt.Texts())
	}
}

// TestUpdatesSettings: Settings → About checks for updates, keeps the
// choices in the settings; a build that cannot update itself says why.
func TestUpdatesSettings(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.Updates.LastCheck = time.Now().Add(-2*time.Hour - time.Minute) })
	u := updatesOf(a)
	a.navigate("/settings/about")
	if !has(tt, "Check for updates") || !has(tt, "Last checked 2 hours ago") {
		t.Fatalf("about: %q", firstTexts(tt))
	}
	see(t, tt, "Check for Updates…")
	click(t, tt, "Check for Updates…")
	if u.feed.checked() != 1 || !has(tt, "Auralink is up to date (") {
		t.Errorf("checks %d: %q", u.feed.checked(), tt.Texts())
	}
	click(t, tt, "OK")
	see(t, tt, "Install updates without asking")
	click(t, tt, "Install updates without asking")
	if p := a.prefs.Updates; !p.AutoInstall || !p.AutoCheckOn() {
		t.Errorf("after turning automatic installs on: %+v", p)
	}
	click(t, tt, "Check for updates automatically")
	if p := a.prefs.Updates; p.AutoCheckOn() || a.updAutoOn.Load() {
		t.Errorf("after turning automatic checks off: %+v", p)
	}
	if s := a.lib.settings.Get().Updates; s.AutoCheckOn() || !s.AutoInstall {
		t.Errorf("saved %+v", s)
	}
	see(t, tt, "Show Changelog…")
	click(t, tt, "Show Changelog…")
	if !has(tt, "Auralink · English") || !has(tt, "0.1.0") {
		t.Errorf("changelog: %q", tt.Texts())
	}
	click(t, tt, "Close")

	// A development build, or one in a folder it cannot write to.
	b, _, tt2 := newTestApp(t)
	ub := updatesOf(b)
	ub.feed.enabled = false
	b.upd, b.updConnected = nil, false
	useTestUpdatesWith(t, b, ub)
	b.navigate("/settings/about")
	frames(tt2)
	if !hasPrefix(tt2, "This copy cannot update itself") || has(tt2, "Check automatically") {
		t.Errorf("disabled: %q", firstTexts(tt2))
	}
	see(t, tt2, "Check for Updates…")
	click(t, tt2, "Check for Updates…") // the panel says why
	if !has(tt2, "Download the latest version from actnow.dev") {
		t.Errorf("disabled panel: %q", tt2.Texts())
	}
}

// useTestUpdatesWith makes a's updater again over u (after a change of
// u.feed.enabled, which the Manager reads when it is made).
func useTestUpdatesWith(t *testing.T, a *app, u *testUpdates) {
	a.upd = akup.New(akup.Config{
		Updater: u.feed.funcs(), Current: a.version,
		History:  &akup.HistoryClient{App: "auralink", HTTPClient: &http.Client{Transport: u.site}},
		Language: i18n.Locale, Restart: func() { u.restarted++ },
	})
	a.connectUpdates()
}

// TestUpdateInstallWithoutAsking: with Install updates without asking on,
// a version the daily check found installs at once; the card asks for the
// restart; a failure is not retried in a loop.
func TestUpdateInstallWithoutAsking(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = true })
	u := updatesOf(a)
	u.feed.offer("0.1.1")
	a.upd.Check(context.Background())
	if u.feed.installs != 1 || a.updSt.Phase != akup.Ready {
		t.Fatalf("installs %d, phase %s", u.feed.installs, a.updSt.Phase)
	}
	a.navigate("/home")
	if !has(tt, "Update installed") || !has(tt, "Restart to update") {
		t.Errorf("no card: %q", firstTexts(tt))
	}
	b, _, _ := newTestApp(t)
	b.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = true })
	ub := updatesOf(b)
	ub.feed.offer("0.1.1")
	ub.feed.installErr = errors.New("disk full")
	b.upd.Check(context.Background())
	b.upd.Check(context.Background())
	if ub.feed.installs != 1 {
		t.Errorf("installs %d", ub.feed.installs)
	}
}

// TestKeepUpdaterChoices: the choices of MyGo's update window
// (updater.json) carry over once.
func TestKeepUpdaterChoices(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.dataHome = t.TempDir()
	if err := os.WriteFile(filepath.Join(a.dataHome, "updater.json"), []byte(`{"automaticChecks":false,"automaticDownloads":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.keepUpdaterChoices()
	if u := a.prefs.Updates; u.AutoCheckOn() || !u.AutoInstall {
		t.Fatalf("carried over %+v", u)
	}
	on := true
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoCheck = &on })
	a.keepUpdaterChoices()
	if !a.prefs.Updates.AutoCheckOn() {
		t.Error("carried over again")
	}
}

// TestChangelogHistory: every version in Auralink's language, betas
// marked; English ones marked in Chinese; an unreachable site says so.
func TestChangelogHistory(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.version = "0.1.0-beta.2"
	tt.SetSize(windowWidth, 1400)
	a.openHistory()
	for _, s := range []string{"Changelog", "0.1.0", "0.1.0-beta.3", "0.1.0-beta.2", "This version", "Translate keeps the line breaks", "On actnow.dev"} {
		if !has(tt, s) {
			t.Errorf("no %q: %q", s, tt.Texts())
		}
	}
	a.histClose()
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	a.openHistory()
	if !has(tt, "更新日志") || !has(tt, "这个版本暂无中文说明，以下为英文。") || !strings.Contains(strings.Join(tt.Texts(), " "), "第一个在 actnow.dev 发布的测试版") {
		t.Errorf("zh: %q", tt.Texts())
	}
	updatesOf(a).site.down = true
	a.clog = nil
	a.openHistory()
	if !strings.Contains(strings.Join(tt.Texts(), " "), "无法读取更新日志") && !has(tt, "无法读取更新日志：dial tcp: no route to host") {
		frames(tt)
		if !strings.Contains(strings.Join(tt.Texts(), " "), "无法读取更新日志") {
			t.Errorf("no error: %q", tt.Texts())
		}
	}
}

// histClose closes the changelog (its × in tests).
func (a *app) histClose() { a.clogOpen = false }

// TestUpdateTextsMatchKit: the updater's texts say, in English and in
// Chinese, what actnowkit's reference texts say.
func TestUpdateTextsMatchKit(t *testing.T) {
	defer i18n.SetLocale("en")
	msg := func(s string) string {
		return strings.NewReplacer("{app}", "Auralink", "{version}", "%s", "{percent}", "%s", "{error}", "%s").Replace(s)
	}
	for _, tx := range []family.Text{
		akup.TextUpdateAvailable, akup.TextNewVersion, akup.TextInstall, akup.TextDownloading, akup.TextRestart,
		akup.TextLater, akup.TextUpToDate, akup.TextChecking, akup.TextFailed, akup.TextCheckFailed,
		akup.TextChangelog, akup.TextAutoCheck, akup.TextNotesInEnglish,
	} {
		en := msg(tx.EN)
		if tx == akup.TextNotesInEnglish {
			en = "These notes are not translated yet; here they are in English."
		}
		i18n.SetLocale("zh-Hans")
		zh := i18n.T(en)
		if zh == en {
			t.Errorf("%q: Auralink has no such message (or no translation)", en)
			continue
		}
		if want := msg(tx.ZH); strings.NewReplacer("“", `"`, "”", `"`).Replace(zh) != want {
			t.Errorf("%q: zh %q, the kit says %q", en, zh, want)
		}
	}
}

// TestBetaUpdates: Settings → About → Beta updates is on by default in a
// beta and off in a release, tells the updater, and the user's choice
// lasts over versions.
func TestBetaUpdates(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	f := updatesOf(a).feed
	a.version = "0.1.0-beta.1"
	a.applyBeta()
	if !f.prereleases() {
		t.Error("a beta does not take betas by default")
	}
	a.navigate("/settings/about")
	see(t, tt, "Get beta versions")
	if !has(tt, "Beta updates") || !hasPrefix(tt, "Betas and releases, whichever is newer.") {
		t.Fatalf("about: %q", firstTexts(tt))
	}
	click(t, tt, "Get beta versions")
	frames(tt)
	if !hasPrefix(tt, "Releases only. This beta stays until a release newer than it comes out") {
		t.Errorf("off in a beta: %q", firstTexts(tt))
	}
	if f.prereleases() || a.prefs.Updates.Beta == nil || *a.prefs.Updates.Beta {
		t.Errorf("after turning betas off: updater %t, settings %v", f.prereleases(), a.prefs.Updates.Beta)
	}
	if b := a.lib.settings.Get().Updates.Beta; b == nil || *b {
		t.Errorf("saved %v", b)
	}
	// A release turns them off by default, and the choice lasts.
	a.quit()
	a.lib.close()
	b, _, _ := newTestAppIn(t, dir)
	b.version = "0.1.0-beta.2"
	b.applyBeta()
	if updatesOf(b).feed.prereleases() {
		t.Error("the choice to turn betas off was lost")
	}
	c, _, tt3 := newTestApp(t)
	c.version = "0.1.0"
	c.applyBeta()
	if updatesOf(c).feed.prereleases() {
		t.Error("a release takes betas by default")
	}
	c.navigate("/settings/about")
	see(t, tt3, "Get beta versions")
	if !hasPrefix(tt3, "Releases only. Turning it on offers betas too") {
		t.Errorf("off in a release: %q", firstTexts(tt3))
	}
	click(t, tt3, "Get beta versions")
	if !updatesOf(c).feed.prereleases() {
		t.Error("turning betas on did not reach the updater")
	}
}

// see scrolls the settings until s shows in the middle of the window.
func see(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	frames(tt)
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q: %q", s, firstTexts(tt))
	}
	tt.Scroll(windowWidth*0.6, windowHeight*0.5, 0, r.Y-windowHeight*0.4)
	frames(tt)
}

// TestUpdateMenus: Check for Updates… in the menu bar icon's menu and the
// palette, none without the updater.
func TestUpdateMenus(t *testing.T) {
	a, _, _ := newTestApp(t)
	f := updatesOf(a).feed
	act := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { fn() }
	}
	labels := func() []string {
		var out []string
		for _, it := range a.trayItems() {
			out = append(out, it.Label)
			if it.Label == "Check for Updates…" {
				it.Click(it, nil)
			}
		}
		return out
	}
	tray := labels()
	if i := slices.Index(tray, "Check for Updates…"); i < 0 || tray[i-1] != "Settings…" || f.checked() != 1 || !a.updPanel {
		t.Errorf("tray %q, checks %d, panel %v", tray, f.checked(), a.updPanel)
	}
	a.updPanel = false
	a.upd.Dismiss()
	cmd := slices.IndexFunc(a.commands(), func(c command) bool { return c.id == "check-updates" })
	if cmd < 0 || !a.commands()[cmd].enabled {
		t.Fatal("no palette command")
	}
	a.runCommand(a.commands()[cmd])
	if f.checked() != 2 {
		t.Errorf("palette: checks %d", f.checked())
	}
	if len(a.updateMenuItems(act)) != 1 {
		t.Error("app menu item")
	}
	a.upd = nil
	if slices.Contains(labels(), "Check for Updates…") || len(a.updateMenuItems(act)) != 0 {
		t.Error("an item without the updater")
	}
	a.checkForUpdates() // nothing to do, no panic
}

// TestAppUpdated: an update installed while Auralink runs (the helper on
// disk is the new version's) shows one card, not the helper's, that
// restarts Auralink, on Home and in Settings → About too.
func TestAppUpdated(t *testing.T) {
	a, fs, tt := newTestApp(t)
	relaunched := 0
	old := appRelaunch
	appRelaunch = func() { relaunched++ }
	t.Cleanup(func() { appRelaunch = old })

	fs.set(helper.Status{State: helper.Failed, Reason: helper.FailUpdated, Err: "Auralink was updated"})
	a.navigate("/home")
	if !has(tt, "Update installed") || !has(tt, "Restart Auralink to finish updating.") || has(tt, "Helper stopped unexpectedly") {
		t.Fatalf("card: %q", firstTexts(tt))
	}
	if !has(tt, "Auralink was updated: restart it to finish.") {
		t.Errorf("home: %q", firstTexts(tt))
	}
	n := slices.IndexFunc(a.notices, func(n core.Notification) bool { return n.ID == problemUpdate })
	if n < 0 || a.notices[n].Link != linkRelaunch || slices.ContainsFunc(a.notices, func(n core.Notification) bool { return n.ID == problemHelper }) {
		t.Fatalf("notices %+v", a.notices)
	}
	a.cardAction(a.notices[n])
	if relaunched != 1 {
		t.Errorf("relaunched %d", relaunched)
	}
	// Settings over a page without the action: its button is the first.
	a.clearNotices()
	a.navigate("/logs")
	a.navigate("/settings/about")
	see(t, tt, "Restart Auralink")
	click(t, tt, "Restart Auralink")
	if relaunched != 2 {
		t.Errorf("about: relaunched %d", relaunched)
	}
	if cardActionLabel(linkRelaunch) != "Restart Auralink" {
		t.Error("card action label")
	}
}

// TestBundleWatch: an update installed in the background shows the card
// once the app sees it (after a check); after the restart the kept card
// is gone.
func TestBundleWatch(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	stale := false
	a.bundleStale = func() bool { return stale }
	a.checkBundle()
	if a.bundleUpdated || has(tt, "Update installed") {
		t.Fatal("updated before the update")
	}
	stale = true
	a.upd.Check(context.Background()) // a check, then the install
	if !a.bundleUpdated || !has(tt, "Update installed") {
		t.Fatalf("not seen: %q", firstTexts(tt))
	}
	a.quit()
	a.lib.close()
	b, _, tt2 := newTestAppIn(t, dir)
	if slices.ContainsFunc(b.notices, func(n core.Notification) bool { return n.ID == problemUpdate }) || has(tt2, "Update installed") {
		t.Errorf("the card outlived the restart: %+v", b.notices)
	}
}
