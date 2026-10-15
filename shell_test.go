package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/helpertest"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/lang"
	"github.com/hehehai/Auralink/internal/permission"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

func TestSidebarAndShortcuts(t *testing.T) {
	a, _, tt := newTestApp(t)
	if got := a.router.Path(); got != "/home" {
		t.Fatalf("starts at %s", got)
	}
	// Every page of the sidebar shows, with its title and its empty state
	// (or its own view).
	for _, p := range pages {
		click(t, tt, pageTitle(p.path))
		if a.router.Path() != p.path {
			t.Fatalf("clicking %s went to %s", p.title, a.router.Path())
		}
		if title, _ := placeholderText(p.path); title != "" && !has(tt, title) {
			t.Errorf("%s: no empty state %q", p.path, title)
		}
	}
	// ⌘1…⌘6.
	for i, path := range shortcutPages {
		tt.Key(ui.Cmd, ui.Key1+ui.Key(i))
		frames(tt)
		if a.router.Path() != path {
			t.Errorf("⌘%d went to %s, want %s", i+1, a.router.Path(), path)
		}
	}
	if len(shortcutPages) != 6 {
		t.Errorf("%d shortcut pages", len(shortcutPages))
	}
	// Settings, at the foot of the sidebar, opens the dialog over the page.
	click(t, tt, "Settings")
	if !a.settingsOpen || a.shownPath() != "/settings/general" || a.router.Path() != "/app-enhancement" {
		t.Fatalf("settings: %v %s over %s", a.settingsOpen, a.shownPath(), a.router.Path())
	}
	tt.Key(0, ui.KeyEscape)
	frames(tt)
	if a.settingsOpen {
		t.Error("Escape left the settings open")
	}
	// The rail keeps the pages.
	click(t, tt, "Show icons only")
	if !a.prefs.Appearance.Rail {
		t.Fatal("rail not on")
	}
	click(t, tt, "History")
	if a.router.Path() != "/history" {
		t.Errorf("rail: %s", a.router.Path())
	}
	click(t, tt, "Show the sidebar")
	if a.prefs.Appearance.Rail {
		t.Error("rail still on")
	}
}

func TestPaletteAndDeepLinks(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Key(ui.Cmd, ui.KeyK)
	frames(tt)
	if !a.paletteOpen {
		t.Fatal("⌘K did not open the palette")
	}
	tt.Type("dictionary")
	frames(tt)
	items := a.paletteItems()
	if len(items) == 0 || items[0].id != "go/dictionary" {
		t.Fatalf("palette items %+v", items)
	}
	tt.Key(0, ui.KeyEnter)
	frames(tt)
	if a.paletteOpen || a.router.Path() != "/dictionary" {
		t.Errorf("palette ran to %s (open %v)", a.router.Path(), a.paletteOpen)
	}
	// Chinese words find commands too, through their translation.
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	a.paletteQuery = "翻译"
	if items := a.paletteItems(); len(items) == 0 || items[0].id != "go/translate" {
		t.Errorf("zh palette %+v", items)
	}
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "en" })

	for link, want := range map[string]string{
		"auralink://history":           "/history",
		"auralink:///translate":        "/translate",
		"auralink://settings/network":  "/settings/network",
		"/settings/privacy":            "/settings/privacy",
		"AURALINK://app-enhancement/x": "/app-enhancement",
	} {
		a.settingsOpen = false
		a.openURL(link)
		if got := a.shownPath(); got != want {
			t.Errorf("%s → %s, want %s", link, got, want)
		}
	}
	a.toasts = nil
	a.openURL("auralink://studio")
	a.openURL("https://example.com/history")
	if len(a.toasts) != 2 || !strings.Contains(a.toasts[0], "Unknown link") {
		t.Errorf("unknown links: %q", a.toasts)
	}
	// The shortcut sheet lists the global shortcuts from the settings.
	tt.Key(ui.Shift, ui.KeySlash)
	if !has(tt, "fn ⇧") || !has(tt, "⌃⌘V") || !has(tt, "Paste the last result") {
		t.Errorf("sheet: %q", firstTexts(tt))
	}
}

func TestHotkeyGlyphs(t *testing.T) {
	for keys, want := range map[string]string{
		"Fn": "fn", "Fn+Shift": "fn ⇧", "Fn+Control": "fn ⌃", "Control+Command+V": "⌃⌘V",
		"RightCommand": "R⌘", "Option+Space": "⌥Space", "": "—", "Command+Shift+k": "⌘⇧K",
	} {
		if got := hotkeyGlyphs(keys); got != want {
			t.Errorf("%q → %q, want %q", keys, got, want)
		}
	}
}

func TestSettingsPersistAndApply(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	a.navigate("/settings/general")
	frames(tt)
	for _, title := range []string{"App language", "Main language", "Show in the menu bar", "Keep running when the window closes", "Open at login"} {
		if !has(tt, title) {
			t.Errorf("general lacks %q", title)
		}
	}
	// The language applies at once, menu and all.
	a.settingsForm.language = i18n.Name("zh-Hans")
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	frames(tt)
	if i18n.Locale() != "zh-Hans" || !has(tt, "设置") {
		t.Errorf("locale %s; texts %q", i18n.Locale(), firstTexts(tt))
	}
	a.updateSettings(func(s *settings.Settings) {
		s.General.MainLanguage = "ja"
		s.Appearance.Theme = "dark"
		s.Network.Proxy = "socks5://127.0.0.1:1080"
	})
	// Each section shows.
	for _, s := range settingsSections() {
		a.navigate("/settings/" + s.id)
		frames(tt)
		if a.shownPath() != "/settings/"+s.id || !has(tt, s.title) {
			t.Errorf("section %s: %s", s.id, a.shownPath())
		}
	}
	// Saved in the library: a new app over it reads them.
	a.quit()
	a.lib.close()
	b, _, _ := newTestAppIn(t, dir)
	if p := b.prefs; p.General.Language != "zh-Hans" || p.General.MainLanguage != "ja" || p.Appearance.Theme != "dark" || p.Network.Proxy != "socks5://127.0.0.1:1080" {
		t.Errorf("reopened: %+v", p)
	}
	if b.settingsForm.proxyMode != proxyOptions()[2] || b.settingsForm.mainLanguage != langLabel("ja") {
		t.Errorf("form %q %q", b.settingsForm.proxyMode, b.settingsForm.mainLanguage)
	}
}

func TestMainLanguagesAreKnown(t *testing.T) {
	for _, code := range mainLanguages {
		if _, ok := lang.Lookup(code); !ok {
			t.Errorf("%s is not in the language table", code)
		}
	}
	if got := langLabel("ja"); got != "Japanese · 日本語" {
		t.Errorf("label %q", got)
	}
}

func TestStatusBarFollowsTheHelper(t *testing.T) {
	a, fs, tt := newTestApp(t)
	if !has(tt, "Helper ready") || !has(tt, "No provider set up") {
		t.Fatalf("status bar: %q", firstTexts(tt))
	}
	click(t, tt, "No provider set up")
	if a.router.Path() != "/providers" {
		t.Errorf("providers: %s", a.router.Path())
	}
	fs.set(helper.Status{State: helper.Failed, Reason: helper.FailMissing, Err: "auralink-helper was not found"})
	if !has(tt, "Helper missing") {
		t.Errorf("missing helper: %q", firstTexts(tt))
	}
	a.helperOpen = true
	if !has(tt, "Restart the helper") {
		t.Fatal("no popover")
	}
	click(t, tt, "Restart the helper")
	if a.helperStatus().State != helper.Ready {
		t.Errorf("after restart: %+v", a.helperStatus())
	}
}

func TestPermissionsPage(t *testing.T) {
	a, fs, tt := newTestApp(t)
	a.navigate("/permissions")
	frames(tt)
	if !slices.Contains(fs.methods(), proto.MethodAutomationProbe) {
		t.Fatalf("no probe: %v", fs.methods())
	}
	if a.automation["com.google.Chrome"] != proto.AutomationGranted || a.automation["com.apple.Safari"] != proto.AutomationNotDetermined {
		t.Fatalf("automation %v", a.automation)
	}
	for _, s := range []string{"Microphone", "Accessibility", "Safari", "Arc", "Not asked yet", "Open it to check"} {
		if !has(tt, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	// The helper's view of the permissions wins: the microphone is not
	// asked yet there.
	if !slices.Contains(fs.methods(), proto.MethodPermissionsStatus) || a.perms.mic != permission.NotDetermined || a.perms.access {
		t.Errorf("permissions %+v after %v", a.perms, fs.methods())
	}
	// Ask: the first is the microphone's, through the helper.
	click(t, tt, "Ask")
	if !slices.Contains(fs.methods(), proto.MethodPermissionsRequest) || a.perms.mic != permission.Granted {
		t.Errorf("microphone after asking: %+v", a.perms)
	}
	if f := a.permSys.(*permission.Fake); f.Prompted != 0 {
		t.Errorf("asked from the app: %d", f.Prompted)
	}
	// A browser not asked yet: Ask makes macOS ask (the helper opens it).
	a.requestAutomation("com.apple.Safari")
	if a.automation["com.apple.Safari"] != proto.AutomationGranted {
		t.Errorf("after asking: %v", a.automation)
	}
	// Without the helper, Accessibility is asked from the app.
	fs.set(helper.Status{State: helper.Failed, Reason: helper.FailCrashed})
	a.askPermission(proto.PermissionAccessibility)
	if f := a.permSys.(*permission.Fake); f.Prompted != 1 {
		t.Errorf("accessibility prompts: %d", f.Prompted)
	}
	// Without the helper, the page says so.
	if !has(tt, "Checking browsers needs the helper, which is not running.") {
		t.Errorf("no helper: %q", firstTexts(tt))
	}
}

func TestPrivacyClearsHistory(t *testing.T) {
	a, _, tt := newTestApp(t)
	h := a.lib.store.History()
	for i, id := range []string{"a", "b"} {
		h.Put(context.Background(), store.Entry{ID: id, Kind: store.KindTranscribe, CreatedAt: time.Now().Add(time.Duration(i) * time.Second), Result: "x"})
	}
	a.navigate("/settings/privacy")
	click(t, tt, "Clear…")
	if !a.clearHistoryAsk {
		t.Fatal("no confirmation")
	}
	click(t, tt, "Delete the history")
	if list, _ := h.List(context.Background(), store.HistoryQuery{}); len(list) != 0 {
		t.Errorf("%d entries left", len(list))
	}
	if !slices.ContainsFunc(a.toasts, func(s string) bool { return s == "Deleted 2 entries" }) && !has(tt, "Deleted 2 entries") {
		t.Errorf("toasts %q", a.toasts)
	}
	// Keeping no history turns the recordings off too.
	a.settingsForm.keepAudio = true
	a.updateSettings(func(s *settings.Settings) { s.Privacy.KeepAudio = true })
	click(t, tt, "Save each result")
	if a.prefs.Privacy.KeepHistory || a.prefs.Privacy.KeepAudio {
		t.Errorf("privacy %+v", a.prefs.Privacy)
	}
}

func TestNotificationsAndLogs(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.notify(core.SeverityWarning, "Helper out of date", "Rebuild it", "/settings/about")
	frames(tt)
	if len(a.notices) != 1 || !has(tt, "1") {
		t.Fatalf("notices %+v", a.notices)
	}
	a.noticesOpen = true
	click(t, tt, "Open")
	if a.shownPath() != "/settings/about" {
		t.Errorf("notice link: %s", a.shownPath())
	}
	a.settingsOpen = false
	a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.SeverityInfo, Source: helper.LogSource, Message: "fake helper ready"})
	a.logs.Append(core.LogEntry{Time: time.Now(), Level: core.SeverityInfo, Source: "app", Message: "Auralink started"})
	a.navigate("/logs")
	a.logsSource = helper.LogSource
	if !has(tt, "fake helper ready") || has(tt, "Auralink started") {
		t.Errorf("logs: %q", firstTexts(tt))
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyL)
	frames(tt)
	if !a.logsPanel {
		t.Error("⇧⌘L")
	}
}

func TestAboutAndBundle(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/settings/about")
	frames(tt)
	if !has(tt, "Helper protocol") || !has(tt, "0.1.0") {
		t.Errorf("about: %q", firstTexts(tt))
	}
	d := a.diagnosticsText()
	if !strings.Contains(d, "Helper: 0.1.0 (ready)") || !strings.Contains(d, "Helper protocol: 1") {
		t.Errorf("diagnostics:\n%s", d)
	}
	bundleDir = t.TempDir()
	t.Cleanup(func() { bundleDir = "" })
	a.saveBundle()
	if ents, _ := os.ReadDir(bundleDir); len(ents) != 1 || !strings.HasSuffix(ents[0].Name(), ".zip") {
		t.Errorf("bundle: %v", ents)
	}
	if u := a.bugReportURL(); !strings.HasPrefix(u, issuesURL+"?") || !strings.Contains(u, "Diagnostics") {
		t.Errorf("bug url %s", u)
	}
}

// TestHelperProcess runs the shell over a real supervisor and the fake
// helper process: the status bar follows the handshake, the Permissions
// page asks it, and its hotkey events reach the log.
func TestHelperProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	lib, err := openLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lib.close)
	queue := make(chan func(), 256)
	a := newApp(lib, &helperHost{bin: self, env: helpertest.Env("press=transcribe/0")})
	a.postQueue = func(fn func()) { queue <- fn }
	pump := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !cond() {
			select {
			case fn := <-queue:
				fn()
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	a.connectHelper()
	a.startHelper()
	t.Cleanup(a.quit)
	pump("the helper", func() bool { return a.helperStatus().State == helper.Ready })
	pump("the browsers", func() bool { return a.automation["com.google.Chrome"] == proto.AutomationGranted })
	// A hotkey.event of no request goes to helperNotified.
	raw, err := a.host.sup.Call(context.Background(), "", proto.MethodHotkeyConfigure, proto.HotkeyConfigureParams{Bindings: []proto.HotkeyBinding{
		{ID: "transcribe/0", Modifiers: map[string]string{proto.ModFn: proto.SideAny}, Behavior: proto.BehaviorTap}}}, nil)
	if err != nil || !strings.Contains(string(raw), `"installed":true`) {
		t.Fatalf("configure: %s %v", raw, err)
	}
	pump("the tap", func() bool {
		return slices.ContainsFunc(a.logs.Tail(50, helper.LogSource), func(e core.LogEntry) bool {
			return e.Message == "hotkey transcribe/0 trigger in com.tinyspeck.slackmacgap"
		})
	})
}
