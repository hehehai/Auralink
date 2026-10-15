package main

// Home (/home, plan §3.1): what is still missing before Auralink can work
// (a speech service, a language model, the Microphone, Accessibility,
// the shortcut), each with the way to fix it; a Try area that runs a
// whole transcription on the page (Start, or the shortcut pressed while
// Auralink is in front); today's count of results and characters; the
// last five results, to copy or open in History.
//
// Today's numbers and the recent results are read from the history in the
// background: when Home shows, and again when a session keeps an entry
// (historyChanged).

import (
	"context"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/permission"
	"github.com/hehehai/Auralink/internal/providers"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

// homeRecent is how many recent results Home lists.
const homeRecent = 5

// homeState is Home's view state.
type homeState struct {
	hm struct {
		// read: the history was read once; loading a read is on its way
		// (seq numbers them: only the last lands); stale an entry was kept
		// since.
		read, loading, stale bool
		seq                  int
		today                dayStats
		recent               []store.Entry
		err                  string
		// block says why the Try area's Start did not start.
		block *sessionNotice
	}
}

// dayStats are a day's finished results: how many, and their characters
// (without spaces), by feature too.
type dayStats struct {
	count, chars int
	byKind       map[store.Kind]int
}

// countChars counts the characters of s that are not spaces.
func countChars(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// readHome reads the recent results and today's numbers (since midnight
// at now) from the history: entries come newest first, so it reads pages
// until one is older than today.
func readHome(ctx context.Context, h store.History, now time.Time) (dayStats, []store.Entry, error) {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	st := dayStats{byKind: map[store.Kind]int{}}
	var recent []store.Entry
	const page = 200
	for off := 0; ; off += page {
		es, err := h.List(ctx, store.HistoryQuery{Limit: page, Offset: off})
		if err != nil {
			return st, recent, err
		}
		older := false
		for _, e := range es {
			done := e.Status == store.StatusDone && strings.TrimSpace(e.Result) != ""
			if done && len(recent) < homeRecent {
				recent = append(recent, e)
			}
			if e.CreatedAt.Before(midnight) {
				older = true
				continue
			}
			if done {
				st.count++
				st.chars += countChars(e.Result)
				st.byKind[e.Kind]++
			}
		}
		if len(es) < page || (older && len(recent) >= homeRecent) {
			return st, recent, nil
		}
	}
}

// loadHome reads Home's numbers and recent results in the background.
func (a *app) loadHome() {
	hm := &a.hm
	hm.seq++
	hm.loading, hm.stale = true, false
	seq := hm.seq
	h := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, recent, err := readHome(ctx, h, time.Now())
		a.post(func() {
			if seq != a.hm.seq {
				return
			}
			hm := &a.hm
			hm.loading, hm.read = false, true
			if err != nil {
				hm.err = err.Error()
				return
			}
			hm.today, hm.recent, hm.err = st, recent, ""
		})
	})
}

// homeChanged runs when the history changed: Home reads it again now
// when it shows, else when it shows next.
func (a *app) homeChanged() {
	if a.currentPage() == "home" && !a.settingsOpen {
		a.loadHome()
		return
	}
	a.hm.stale = true
}

// ---- Try places ----

// tryPlace is where a shortcut pressed while Auralink is in front runs
// as a try.
type tryPlace int

const (
	placeNone       tryPlace = iota // not a try: a session as anywhere else
	placeIgnore                     // the setup guide waits for another feature's shortcut
	placeHome                       // Home's Try area
	placeFeature                    // the feature page's Try area
	placeOnboarding                 // the setup guide's step
)

// tryPlace decides where a shortcut of kind, pressed with target in
// front, runs: a try when target is Auralink and what shows has a Try
// area for it (the setup guide's step, Home, the feature's page).
func (a *app) tryPlace(kind store.Kind, target proto.App) tryPlace {
	if target.PID == 0 || target.PID != os.Getpid() {
		return placeNone
	}
	if a.ob.open {
		if k, ok := obStepKind(a.ob.step); ok && k == kind {
			return placeOnboarding
		}
		return placeIgnore
	}
	if a.settingsOpen {
		return placeNone
	}
	switch a.router.Path() {
	case "/home":
		return placeHome
	case "/" + string(kind):
		return placeFeature
	}
	return placeNone
}

// tryStarted keeps a try a shortcut started (id, "" when it did not:
// then n says why) for the place that shows it.
func (a *app) tryStarted(place tryPlace, kind store.Kind, id string, n *sessionNotice) {
	switch place {
	case placeHome:
		a.hm.block = n
	case placeFeature:
		a.fui(string(kind)).block = n
	case placeOnboarding:
		a.obTryStarted(id, n) // onboarding.go
	}
}

// ---- The page ----

// readiness is one thing Auralink needs, as Home lists it.
type readiness struct {
	key   string
	title string // what it is
	ok    bool
	warn  bool   // ok, but it waits for something another line asks for
	text  string // ok: what is used; else what is missing
	more  string // ⓘ
	kind  noticeKind
	act   func(c *ui.Context) // the way to fix it, nil for none
}

// readinessList says what is ready and what is missing (memory only).
func (a *app) readinessList() []readiness {
	var out []readiness
	openProv := func(key string) func(c *ui.Context) {
		return func(c *ui.Context) {
			if ui.Button(c.Key(key), i18n.T("Open Model services")).Clicked() {
				a.navigate("/providers")
			}
		}
	}
	openPerms := func(key string) func(c *ui.Context) {
		return func(c *ui.Context) {
			if ui.Button(c.Key(key), i18n.T("Open Permissions")).Clicked() {
				a.navigate("/settings/permissions")
			}
		}
	}
	f := a.prefs.Features.Transcribe.Feature
	pa, okA := providers.Choose(a.prefs, providers.ASR, f.ASR, a.hasKey)
	pl, okL := providers.Choose(a.prefs, providers.LLM, f.LLM, a.hasKey)
	switch {
	case !okA && !okL:
		// Nothing set up: one notice, one button.
		out = append(out, readiness{key: "home-services", title: i18n.T("Services"), kind: noticeWarn,
			text: i18n.T("No speech service or language model is ready: set up one of each to start."), act: openProv("home-services-open")})
	case !okA:
		out = append(out, readiness{key: "home-asr", title: i18n.T("Speech recognition"), kind: noticeWarn,
			text: i18n.T("No speech service is ready: set one up to transcribe, translate and rewrite by voice."), act: openProv("home-asr-open")})
	default:
		out = append(out, readiness{key: "home-asr", title: i18n.T("Speech recognition"), ok: true, text: providerName(providers.ASR, pa)})
	}
	switch {
	case !okA && !okL:
	case !okL:
		out = append(out, readiness{key: "home-llm", title: i18n.T("Language model"), kind: noticeWarn,
			text: i18n.T("No language model is ready: Translate and Rewrite need one, and Transcribe types the text as recognised."), act: openProv("home-llm-open")})
	default:
		out = append(out, readiness{key: "home-llm", title: i18n.T("Language model"), ok: true, text: providerName(providers.LLM, pl)})
	}
	// The Microphone.
	switch {
	case !a.perms.checked:
	case a.perms.mic == permission.Granted:
		out = append(out, readiness{key: "home-mic", title: i18n.T("Microphone"), ok: true, text: i18n.T("Allowed")})
	case a.perms.mic == permission.NotDetermined:
		out = append(out, readiness{key: "home-mic", title: i18n.T("Microphone"), kind: noticeInfo,
			text: i18n.T("macOS asks for the microphone the first time you record."), act: func(c *ui.Context) {
				if ui.Button(c.Key("home-mic-ask"), i18n.T("Ask now")).Clicked() {
					a.askPermission(proto.PermissionMicrophone)
				}
			}})
	default:
		out = append(out, readiness{key: "home-mic", title: i18n.T("Microphone"), kind: noticeError,
			text: i18n.T("Auralink may not use the microphone: allow it in System Settings."), act: openPerms("home-mic-open")})
	}
	// Accessibility.
	switch {
	case !a.perms.checked:
	case a.perms.access:
		out = append(out, readiness{key: "home-access", title: i18n.T("Accessibility"), ok: true, text: i18n.T("Allowed")})
	default:
		out = append(out, readiness{key: "home-access", title: i18n.T("Accessibility"), kind: noticeWarn,
			text: i18n.T("Without Accessibility results are copied for you to paste, and fn shortcuts do not work."), act: openPerms("home-access-open")})
	}
	// The shortcut.
	out = append(out, a.shortcutReadiness())
	// The helper.
	switch st := a.helperStatus(); {
	case st.State == helper.Ready:
	case st.State == helper.Starting:
		out = append(out, readiness{key: "home-helper", title: i18n.T("Helper"), kind: noticeInfo, text: i18n.T("Helper starting…")})
	default:
		out = append(out, readiness{key: "home-helper", title: i18n.T("Helper"), kind: noticeError,
			text: helperFailText(st), act: func(c *ui.Context) {
				if ui.Button(c.Key("home-helper-restart"), i18n.T("Restart the helper")).Clicked() {
					a.restartHelper()
				}
			}})
	}
	return out
}

// shortcutReadiness says whether Transcribe's shortcut works: there is
// one, nothing else takes its keys, and the event tap it may need runs.
func (a *app) shortcutReadiness() readiness {
	r := readiness{key: "home-keys", title: i18n.T("Shortcut")}
	openTr := func(c *ui.Context) {
		if ui.Button(c.Key("home-keys-open"), i18n.T("Open Transcribe")).Clicked() {
			a.navigate("/transcribe")
		}
	}
	hs := hotkey.HotkeysOf(a.prefs.Features, hotkey.Transcribe)
	if len(hs) == 0 {
		r.kind, r.text, r.act = noticeWarn, i18n.T("Transcribe has no shortcut: add one, or start from the menu bar."), openTr
		return r
	}
	// Its own problems only: a clash is reported on the later binding,
	// and the first one (Transcribe's) wins.
	for _, p := range hotkey.Check(a.prefs.Features) {
		if p.Feature == hotkey.Transcribe && p.Index == 0 {
			r.kind, r.text, r.act = noticeWarn, i18n.T("Transcribe's shortcut %s: %s", hotkeyText(hs[0].Keys), hotkeyProblemText(p)), openTr
			return r
		}
	}
	r.ok, r.text = true, hotkeyText(hs[0].Keys)
	if hotkey.NeedsTap(hs[0]) && !a.hotkeys.Installed {
		// The Accessibility notice says what to do; the line says it waits.
		r.warn = true
		r.text = i18n.T("%s (once Accessibility is allowed)", hotkeyText(hs[0].Keys))
	}
	return r
}

func (a *app) homePage(c *ui.Context) {
	t := c.Theme()
	hm := &a.hm
	if !hm.loading && (!hm.read || hm.stale) {
		a.loadHome()
	}
	if !a.perms.checked {
		a.recheckPermissions()
	}
	if a.sess.Try && a.sess.State == session.Recording {
		c.After(250 * time.Millisecond) // the Try area's clock
	}
	pageHeader(c, i18n.T("Home"), func() {
		if ghostIconButton(c.Key("home-guide"), icons.Compass, i18n.T("Setup guide")).Label(i18n.T("Open the setup guide")).Clicked() {
			a.openOnboarding()
		}
	})
	ui.Scroll(c.Key("home-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(0, px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 880)).Children(func() {
			caption(c, i18n.T("Press a shortcut in any app, speak, and the text is typed where your cursor is."))
			a.homeReadiness(c)
			sectionTitle(c, i18n.T("Try it"))
			a.tryCard(c, tryOpts{
				prefix: "home", feature: hotkey.Transcribe, mine: a.sess.Try && a.sess.ID != "" && !a.obOwns(a.sess.ID),
				intro: a.tryIntro(hotkey.Transcribe), block: &hm.block,
				start: func() *sessionNotice { _, n := a.startTry(featureKind(hotkey.Transcribe)); return n },
			})
			a.homeToday(c)
			a.homeRecentList(c)
		})
	})
}

// homeReadiness is the list of what is ready (a line each, in a card)
// and what is not (a notice each, with the way to fix it).
func (a *app) homeReadiness(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	list := a.readinessList()
	var ready, missing []readiness
	for _, r := range list {
		if r.ok {
			ready = append(ready, r)
		} else {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		sectionTitle(c, i18n.T("Ready to go"))
	} else {
		sectionTitle(c, i18n.T("Still to set up"))
	}
	for _, r := range missing {
		var act func()
		if r.act != nil {
			act = func() { r.act(c) }
		}
		notice(c, r.key, r.kind, r.title+" · "+r.text, r.more, act, nil)
	}
	if len(ready) == 0 {
		return
	}
	ui.Row(c.Key("home-ready")).Wrap().GapX(px(t, sp4)).GapY(px(t, sp1)).Padding(px(t, sp1), px(t, 2)).Children(func() {
		for _, r := range ready {
			ui.Row(c.Key(r.key)).Gap(px(t, 6)).AlignItems(ui.Center).MinWidth(0).Children(func() {
				if r.warn {
					ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Warning).Shrink(0)
				} else {
					ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Success).Shrink(0)
				}
				ui.Text(c, r.title).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine()
				ui.Text(c, r.text).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0).MaxWidth(px(t, 220)).Tooltip(r.text)
			})
		}
	})
}

// homeToday is today's numbers.
func (a *app) homeToday(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	hm := &a.hm
	sectionTitle(c, i18n.T("Today"))
	if !a.prefs.Privacy.KeepHistory {
		caption(c, i18n.T("History is off, so nothing is counted."))
		return
	}
	st := hm.today
	tile := func(key, value, label string) {
		fillCard(c.Key(key)).Gap(px(t, 2)).Grow(1).Basis(px(t, 140)).MinWidth(px(t, 120)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
			ui.Text(c, value).FontSize(px(t, fsTitle)).FontWeight(600).SingleLine()
			ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
		})
	}
	ui.Row(c.Key("home-today")).Wrap().Gap(px(t, sp3)).Children(func() {
		tile("home-count", itoa(st.count), i18n.N(st.count, "result", "results"))
		tile("home-chars", groupDigits(st.chars), i18n.N(st.chars, "character", "characters"))
		// By feature: each one's icon and count.
		fillCard(c.Key("home-kinds")).Gap(px(t, 2)).Grow(1).Basis(px(t, 140)).MinWidth(px(t, 120)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
			ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Height(px(t, fsTitle*1.3)).Children(func() {
				for _, kd := range store.Kinds {
					ui.Row(c.Key("kind-" + string(kd))).Gap(px(t, 4)).AlignItems(ui.Center).Label(kindTitle(kd) + " " + itoa(st.byKind[kd])).Tooltip(kindTitle(kd)).Children(func() {
						ui.Icon(c, kindIcon(kd).Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2)
						ui.Text(c, itoa(st.byKind[kd])).FontSize(px(t, fsSection)).FontWeight(600).SingleLine()
					})
				}
			})
			ui.Text(c, i18n.T("By feature")).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
		})
	})
}

// groupDigits writes n with thousands separators: 12,345.
func groupDigits(n int) string {
	s := itoa(n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// homeRecentList is the last results: each copies, or opens in History.
func (a *app) homeRecentList(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	hm := &a.hm
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Margin(px(t, sp2), 0, 0, 0).Children(func() {
		sectionTitleText(c, i18n.T("Recent results")).Grow(1)
		if ghostButton(c.Key("home-history"), i18n.T("Open History")).Clicked() {
			a.navigate("/history")
		}
	})
	if hm.err != "" {
		notice(c, "home-err", noticeError, i18n.T("Could not read the history."), hm.err, nil, nil)
		return
	}
	if len(hm.recent) == 0 {
		if hm.read {
			caption(c, i18n.T("Results show here once you use a shortcut."))
		}
		return
	}
	c.After(time.Minute) // the times are relative
	card(c.Key("home-recent")).Padding(px(t, sp1)).Gap(0).Children(func() {
		for _, e := range hm.recent {
			title, _ := entryTitle(e)
			row := ui.ButtonBase(c.Key("home-recent-"+e.ID)).Padding(px(t, sp2), px(t, sp3)).Radius(px(t, radiusControl)).
				Gap(px(t, sp3)).AlignItems(ui.Center).Label(title).Tooltip(i18n.T("Open in History"))
			if row.Hovered() {
				row.Background(k.Hover)
			}
			row.Children(func() {
				ui.Icon(c, kindIcon(e.Kind).Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0).Label(kindTitle(e.Kind))
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
					ui.Text(c, title).SingleLine().Ellipsis("…").MinWidth(0)
					ui.Text(c, entryMeta(e)).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
				})
				if iconButton(c.Key("copy"), icons.Copy, i18n.T("Copy")).Label(i18n.T("Copy: %s", title)).Clicked() {
					c.Services().WriteClipboard(e.Result)
					a.toast(i18n.T("Copied"))
				}
			})
			if row.Clicked() {
				a.openHistoryEntry(e)
			}
		}
	})
}

// openHistoryEntry shows an entry in History: its feature's tab, its
// details open.
func (a *app) openHistoryEntry(e store.Entry) {
	h := &a.hist
	for i, kd := range store.Kinds {
		if kd == e.Kind {
			if h.tab != i {
				h.tab, h.listFor = i, ""
			}
		}
	}
	h.query = ""
	h.sel = e.ID
	a.navigate("/history")
}
