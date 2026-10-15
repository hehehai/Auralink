package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/permission"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// homeTester is the test app on Home, in a window tall enough to show all
// of it.
func homeTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a, _, tt := newTestApp(t)
	tt.SetSize(windowWidth, 1600)
	a.navigate("/home")
	frames(tt)
	return a, tt
}

// allowAll makes the Microphone and Accessibility allowed, as the helper
// would say.
func allowAll(a *app) {
	a.perms.mic, a.perms.access, a.perms.checked = permission.Granted, true, true
	a.hotkeys.Installed = true
}

func TestHomeReadiness(t *testing.T) {
	a, tt := homeTester(t)
	for _, s := range []string{
		"Still to set up",
		"Services · No speech service or language model is ready: set up one of each to start.",
		"Accessibility · Without Accessibility results are copied for you to paste, and fn shortcuts do not work.",
		"fn (once Accessibility is allowed)",
		"Try it", "Today", "Recent results", "Results show here once you use a shortcut.",
	} {
		if !has(tt, s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	click(t, tt, "Open Model services")
	if a.shownPath() != "/providers" {
		t.Fatalf("went to %s", a.shownPath())
	}
	a.navigate("/home")

	// A speech service only: the language model is missing.
	a.updateSettings(func(s *settings.Settings) { s.Providers.ASR = []settings.Provider{{ID: "doubao", Enabled: true}} })
	a.keyMasks["asr.doubao"] = "tok…abcd"
	if !has(tt, "Language model · No language model is ready: Translate and Rewrite need one, and Transcribe types the text as recognised.") || !has(tt, "Doubao (Volcengine)") {
		t.Errorf("no language model: %q", tt.Texts())
	}

	readyServices(a)
	allowAll(a)
	for _, s := range []string{"Ready to go", "Doubao (Volcengine)", "DeepSeek", "fn"} {
		if !has(tt, s) {
			t.Errorf("ready: no %q in %q", s, tt.Texts())
		}
	}
	if has(tt, "Still to set up") || hasPrefix(tt, "Accessibility · ") {
		t.Errorf("ready, still notices: %q", tt.Texts())
	}

	// A shortcut macOS takes says so, and opens Transcribe.
	a.updateSettings(func(s *settings.Settings) {
		s.Features.Transcribe.Hotkeys = []settings.Hotkey{{Keys: "Command+Space", Trigger: settings.TriggerTap}}
	})
	if !has(tt, "Shortcut · Transcribe's shortcut ⌘ Space: macOS uses these keys for Spotlight.") {
		t.Errorf("no clash: %q", tt.Texts())
	}
	click(t, tt, "Open Transcribe")
	if a.router.Path() != "/transcribe" {
		t.Errorf("went to %s", a.router.Path())
	}
}

func TestReadHome(t *testing.T) {
	a, _, _ := newTestApp(t)
	h := a.lib.store.History()
	now := time.Date(2026, 10, 15, 15, 0, 0, 0, time.Local)
	put := func(id string, kind store.Kind, at time.Time, status store.Status, result string) {
		t.Helper()
		if err := h.Put(context.Background(), store.Entry{ID: id, Kind: kind, CreatedAt: at, Status: status, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	put("01A", store.KindTranscribe, now.Add(-time.Hour), store.StatusDone, "Hello world.")
	put("01B", store.KindTranslate, now.Add(-2*time.Hour), store.StatusDone, "你好，世界。")
	put("01C", store.KindRewrite, now.Add(-3*time.Hour), store.StatusFailed, "")
	put("01D", store.KindTranscribe, now.Add(-16*time.Hour), store.StatusDone, "Late yesterday.")
	put("01E", store.KindRewrite, now.Add(-30*time.Hour), store.StatusDone, "Older.")
	st, recent, err := readHome(context.Background(), h, now)
	if err != nil {
		t.Fatal(err)
	}
	if st.count != 2 || st.chars != len("Helloworld.")+6 || st.byKind[store.KindTranscribe] != 1 || st.byKind[store.KindTranslate] != 1 {
		t.Errorf("today %+v", st)
	}
	var ids []string
	for _, e := range recent {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "01A,01B,01D,01E" {
		t.Errorf("recent %v", ids)
	}
	if groupDigits(1234567) != "1,234,567" || groupDigits(999) != "999" {
		t.Errorf("groupDigits %s %s", groupDigits(1234567), groupDigits(999))
	}
}

func TestHomeRecent(t *testing.T) {
	a, tt := homeTester(t)
	seedHistory(t, a)
	seedWriteHistory(t, a)
	a.historyChanged()
	frames(tt)
	if !has(tt, "Let's move the design review to Thursday afternoon.") || !has(tt, "By feature") {
		t.Fatalf("recent: %q", tt.Texts())
	}
	if a.hm.today.count == 0 {
		t.Errorf("nothing today: %+v", a.hm.today)
	}
	if len(a.hm.recent) != homeRecent {
		t.Errorf("%d recent", len(a.hm.recent))
	}
	e := a.hm.recent[0]
	click(t, tt, "Copy: "+firstLine(e.Result))
	if tt.Clipboard() != e.Result {
		t.Errorf("clipboard %q", tt.Clipboard())
	}
	title, _ := entryTitle(e)
	click(t, tt, title)
	if a.router.Path() != "/history" || a.hist.sel != e.ID || a.hist.kind() != e.Kind {
		t.Errorf("opened %s sel %q tab %s", a.router.Path(), a.hist.sel, a.hist.kind())
	}
	// History off: nothing is counted.
	a.navigate("/home")
	a.updateSettings(func(s *settings.Settings) { s.Privacy.KeepHistory = false })
	if !has(tt, "History is off, so nothing is counted.") {
		t.Errorf("history off: %q", tt.Texts())
	}
}

// selfFront is Auralink as the app in front.
func selfFront() *proto.App {
	return &proto.App{PID: os.Getpid(), BundleID: "dev.hehehai.auralink", Name: "Auralink"}
}

func TestTryPlace(t *testing.T) {
	a, _, tt := newTestApp(t)
	other := proto.App{PID: 77, BundleID: "com.apple.Notes"}
	cases := []struct {
		path   string
		target proto.App
		kind   store.Kind
		want   tryPlace
	}{
		{"/home", *selfFront(), store.KindTranscribe, placeHome},
		{"/home", *selfFront(), store.KindRewrite, placeHome},
		{"/home", other, store.KindTranscribe, placeNone},
		{"/translate", *selfFront(), store.KindTranslate, placeFeature},
		{"/translate", *selfFront(), store.KindTranscribe, placeNone},
		{"/dictionary", *selfFront(), store.KindTranscribe, placeNone},
	}
	for _, tc := range cases {
		a.navigate(tc.path)
		if got := a.tryPlace(tc.kind, tc.target); got != tc.want {
			t.Errorf("%s %s in %s: %v, want %v", tc.path, tc.kind, tc.target.BundleID, got, tc.want)
		}
	}
	a.navigate("/settings/general")
	if a.tryPlace(store.KindTranscribe, *selfFront()) != placeNone {
		t.Error("a try under the settings")
	}
	a.openOnboarding()
	a.ob.step = obTranslate
	if a.tryPlace(store.KindTranslate, *selfFront()) != placeOnboarding || a.tryPlace(store.KindTranscribe, *selfFront()) != placeIgnore {
		t.Error("the guide's places")
	}
	a.ob.step = obServices
	if a.tryPlace(store.KindTranscribe, *selfFront()) != placeIgnore {
		t.Error("a shortcut on a step without a try")
	}
	_ = tt
}

// TestHomeTryByShortcut: the shortcut pressed while Home shows (Auralink
// in front) runs a try on the page; in another app, a session as usual.
func TestHomeTryByShortcut(t *testing.T) {
	a, tt := homeTester(t)
	drain := queuePosts(a)
	a.hotkeyFired(proto.HotkeyEventParams{Binding: "transcribe/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, Front: selfFront()})
	frames(tt)
	if a.sess.State.Live() || a.hm.block == nil || !has(tt, "No speech service is set up.") {
		t.Fatalf("blocked try: %+v %v %q", a.sess, a.hm.block, tt.Texts())
	}
	if a.notice != nil {
		t.Error("a try's problem went to the overlay")
	}
	readyServices(a)
	drain()
	a.hotkeyFired(proto.HotkeyEventParams{Binding: "transcribe/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, Front: selfFront()})
	drain()
	frames(tt)
	if a.sess.ID == "" || !a.sess.Try || a.sess.Kind != store.KindTranscribe {
		t.Fatalf("no try: %+v", a.sess)
	}
	a.cancelSession()
	drain()
	frames(tt)
	// Done: the result shows on Home.
	s := trySnapshot(a, session.Done)
	a.sessionChanged(s)
	if !has(tt, "The meeting moves to Thursday at four.") {
		t.Errorf("result: %q", tt.Texts())
	}
}

// homeShots saves Home with everything ready, the history seeded, and a
// try that is done (TestSnapshots), at the top and scrolled to its end.
func homeShots(a *app, tt *ui.Tester, save func(string)) {
	a.host.sup.Start(context.Background()) // ready again (the shots before stopped it)
	readyServices(a)
	allowAll(a)
	a.navigate("/home")
	a.historyChanged()
	a.sessionChanged(trySnapshot(a, session.Done))
	frames(tt)
	save("page-home-ready")
	w, h := snapshotSize()
	for range 5 {
		tt.Scroll(float32(w)-100, float32(h)/2, 0, 2000)
		frames(tt)
	}
	save("page-home-ready-end")
	a.sessionChanged(session.Snapshot{Rev: a.sess.Rev + 1})
}
