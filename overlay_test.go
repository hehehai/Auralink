package main

import (
	"encoding/json"
	"errors"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/appctx"
	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

var slackApp = proto.App{PID: 812, BundleID: "com.tinyspeck.slackmacgap", Name: "Slack"}

// snap gives the app a session snapshot, as the manager would.
func snap(a *app, s session.Snapshot) {
	s.Rev = a.sess.Rev + 1
	if s.ID == "" {
		s.ID = "s1"
	}
	if s.Target.PID == 0 {
		s.Target = slackApp
	}
	a.sessionChanged(s)
}

func wave() []float64 {
	var out []float64
	for i := range session.Bars {
		out = append(out, 0.35+0.3*math.Sin(float64(i)/2))
	}
	return out
}

// TestOverlayModes: what the overlay shows through a session's states.
func TestOverlayModes(t *testing.T) {
	a, _, _ := newTestApp(t)
	if a.ov.mode != ovHidden || a.ov.shown {
		t.Fatalf("starts %s", a.ov.mode)
	}
	now := time.Now()
	snap(a, session.Snapshot{State: session.Recording, StartedAt: now})
	if a.ov.mode != ovRecording || a.ov.w != ovPillW || a.ov.h != ovPillH {
		t.Errorf("recording %s %d×%d", a.ov.mode, a.ov.w, a.ov.h)
	}
	snap(a, session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now, Partial: "hello"})
	if a.ov.w != ovLiveW || a.ov.h != ovLiveH {
		t.Errorf("live text %d×%d", a.ov.w, a.ov.h)
	}
	snap(a, session.Snapshot{State: session.Composing, Partial: "hello"})
	if a.ov.mode != ovWorking {
		t.Errorf("composing %s", a.ov.mode)
	}
	// Inserted: nothing more to show.
	snap(a, session.Snapshot{State: session.Done, Result: "Hello.", Inserted: true})
	if a.ov.mode != ovHidden || a.ov.shown {
		t.Errorf("done %s", a.ov.mode)
	}
	// A try shows on the page, not in the overlay.
	snap(a, session.Snapshot{ID: "s2", State: session.Recording, Try: true})
	if a.ov.mode != ovHidden {
		t.Errorf("try %s", a.ov.mode)
	}
	// Not inserted: the answer card, until closed (Escape closes it).
	snap(a, session.Snapshot{ID: "s3", State: session.Recording})
	snap(a, session.Snapshot{ID: "s3", State: session.Done, Result: "Card text", Card: true, Delivery: deliver.ReasonNotEditable})
	if a.ov.mode != ovAnswer || !a.answer.open || a.answer.text != "Card text" || a.ov.w != ovAnswerW {
		t.Fatalf("card %s %+v", a.ov.mode, a.answer)
	}
	a.hotkeyFired(proto.HotkeyEventParams{Binding: proto.HotkeyEscape, Phase: proto.PhaseTrigger})
	if a.answer.open || a.ov.mode != ovHidden {
		t.Errorf("escape: %s %+v", a.ov.mode, a.answer)
	}
	// A failure: a notice with a way forward.
	snap(a, session.Snapshot{ID: "s4", State: session.Recording})
	snap(a, session.Snapshot{ID: "s4", State: session.Failed, Problem: &session.Problem{Code: session.ProblemASR, Err: &asr.Error{Kind: asr.KindAuth}}})
	if a.ov.mode != ovNotice || a.notice == nil || a.notice.action != actionProviders || a.notice.text != "Speech recognition failed: The service refused the key. Check it and try again." {
		t.Fatalf("notice %s %+v", a.ov.mode, a.notice)
	}
	a.noticeAction(a.notice.action)
	if a.router.Path() != "/providers" || a.ov.mode != ovHidden {
		t.Errorf("action: %s %s", a.router.Path(), a.ov.mode)
	}
	// Cancelled and too short: nothing.
	snap(a, session.Snapshot{ID: "s5", State: session.Recording})
	snap(a, session.Snapshot{ID: "s5", State: session.Cancelled, Problem: &session.Problem{Code: session.ProblemTooShort}})
	if a.ov.mode != ovHidden || a.notice != nil {
		t.Errorf("cancelled: %s %+v", a.ov.mode, a.notice)
	}
	// An older snapshot changes nothing.
	old := a.sess
	a.sessionChanged(session.Snapshot{Rev: old.Rev - 1, ID: "s4", State: session.Recording})
	if a.sess.Rev != old.Rev || a.ov.mode != ovHidden {
		t.Errorf("an older snapshot won: %+v", a.sess)
	}
}

// TestAnswerCardButtons: Copy copies and closes; Insert pastes through
// the helper into the session's app, or says why it could not.
func TestAnswerCardButtons(t *testing.T) {
	a, fs, _ := newTestApp(t)
	var pasted []proto.PasteParams
	front := 812
	fs.handlers[proto.MethodTextFocus] = func(json.RawMessage) (any, error) {
		return proto.FocusResult{PID: front, Role: "AXStaticText"}, nil
	}
	fs.handlers[proto.MethodTextPaste] = func(raw json.RawMessage) (any, error) {
		var p proto.PasteParams
		json.Unmarshal(raw, &p)
		pasted = append(pasted, p)
		return proto.PasteResult{Posted: true}, nil
	}
	card := func(id string) {
		snap(a, session.Snapshot{ID: id, State: session.Recording})
		snap(a, session.Snapshot{ID: id, State: session.Done, Result: "Answer " + id, Card: true, Delivery: deliver.ReasonNotEditable})
	}
	card("c1")
	tt := ui.NewTester(a.overlayView, ovAnswerW+2*ovPad, ovAnswerH+2*ovPad)
	frames(tt)
	if !tt.HasText("Answer c1") || !tt.HasText("Nothing in Slack takes text: click where it should go, then insert.") {
		t.Errorf("card texts %q", firstTexts(tt))
	}
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.answer.open || len(a.toasts) == 0 || a.toasts[len(a.toasts)-1] != "Copied" {
		t.Errorf("copy: %+v %v", a.answer, a.toasts)
	}
	card("c2")
	frames(tt)
	if err := tt.Click("Insert"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.answer.open || len(pasted) != 1 || pasted[0].Text != "Answer c2" || pasted[0].TargetPID != 812 {
		t.Errorf("insert: %+v %+v", a.answer, pasted)
	}
	front = 530 // another app came to the front
	card("c3")
	a.insertAnswer()
	if !a.answer.open || a.answer.msg != "Slack is not in front any more." || len(pasted) != 1 {
		t.Errorf("elsewhere: %+v", a.answer)
	}
	fs.handlers[proto.MethodTextFocus] = func(json.RawMessage) (any, error) {
		return nil, &proto.Error{Code: proto.CodePermissionDenied}
	}
	a.insertAnswer()
	if a.answer.msg != "Allow Accessibility to insert results where you type." {
		t.Errorf("no access: %+v", a.answer)
	}
	frames(tt)
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.answer.open || a.ov.mode != ovHidden {
		t.Errorf("close: %+v", a.answer)
	}
}

func TestSessionProblemTexts(t *testing.T) {
	for _, tc := range []struct {
		p      session.Problem
		text   string
		action string
	}{
		{session.Problem{Code: session.ProblemNoASR}, "No speech service is set up.", actionProviders},
		{session.Problem{Code: session.ProblemMicrophone, Err: errors.New("x")}, "Auralink may not use the microphone.", actionPermissions},
		{session.Problem{Code: session.ProblemHelper}, "The helper stopped: try again in a moment.", actionHelper},
		{session.Problem{Code: session.ProblemNoSpeech}, "Nothing was heard.", actionNone},
		{session.Problem{Code: session.ProblemTooShort}, "", actionNone},
		{session.Problem{Code: session.ProblemInternal, Err: errors.New("boom")}, "Something went wrong: boom", actionNone},
	} {
		text, _, action := sessionProblemText(&tc.p)
		if text != tc.text || action != tc.action {
			t.Errorf("%s: %q %q", tc.p.Code, text, action)
		}
	}
}

func TestClockAndTail(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", 9 * time.Second: "0:09", 75 * time.Second: "1:15", 10 * time.Minute: "10:00"} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q", d, got)
		}
	}
	long := ""
	for i := range 40 {
		long += "word" + itoa(i) + " "
	}
	got := liveTail(long)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "word39") || textWidth(got, 13) > (ovLiveW-2*ovPillPadX)*ovLiveLines {
		t.Errorf("liveTail %q", got)
	}
	zh := strings.Repeat("今天下午三点开会讨论发布计划", 10)
	if got := liveTail(zh); !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "发布计划") || len([]rune(got)) > 2*(ovLiveW-2*ovPillPadX)/13 {
		t.Errorf("liveTail zh %q", got)
	}
	if liveTail("  short  text ") != "short text" {
		t.Error("short text")
	}
}

// TestWaveBars: the bars follow a word up quickly and down slowly, and
// a quiet room stays flat.
func TestWaveBars(t *testing.T) {
	quiet := make([]float64, session.Bars)
	for i := range quiet {
		quiet[i] = session.Level(-55)
	}
	for i, v := range waveBars(quiet) {
		if v != 0 {
			t.Fatalf("quiet bar %d = %v", i, v)
		}
	}
	word := slices.Clone(quiet)
	word[8] = session.Level(-15) // one loud level, then quiet again
	b := waveBars(word)
	if b[7] != 0 || b[8] < 0.7 || !(b[9] < b[8] && b[9] > 0.4*b[8]) || !(b[15] > 0 && b[15] < b[9]) {
		t.Errorf("word %v", b)
	}
	if got := waveBars(nil); got[0] != 0 || got[15] != 0 {
		t.Errorf("none %v", got)
	}
	long := append(slices.Clone(quiet), word...)
	if waveBars(long) != waveBars(word) {
		t.Error("more levels than bars: the newest count")
	}
	for _, p := range []float64{0, 0.3, 0.6, 0.9, 1.15} {
		sc, al := loaderDot(p)
		if sc < 0.6 || sc > 1.3 || al < 0.15 || al > 1 {
			t.Errorf("loader at %v: %v %v", p, sc, al)
		}
	}
}

// overlayShots renders every state of the overlay (snapshot_test.go)
// over a desktop, light or dark, that shows through the face blurred, as
// the window's material blurs it on the Mac.
func overlayShots(t *testing.T, a *app, out, suffix string, dark bool) {
	t.Helper()
	now := time.Now()
	a.prefs.Overlay.ShowTime = false
	states := []struct {
		name  string
		s     session.Snapshot
		setup func()
	}{
		{"overlay-starting", session.Snapshot{State: session.Recording, StartedAt: now}, nil},
		{"overlay-recording", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-12 * time.Second), Levels: wave()}, nil},
		{"overlay-recording-time", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-12 * time.Second), Levels: wave()},
			func() { a.prefs.Overlay.ShowTime = true }},
		{"overlay-live-text", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-47 * time.Second), Levels: wave(),
			Partial: "So the plan for the release is to finish the onboarding first, then the history page, and after that we look at the translation card"}, nil},
		{"overlay-live-text-zh", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-20 * time.Second), Levels: wave(),
			Partial: "那个我们明天，不对，后天下午三点开会，讨论一下 Auralink 的发布计划，然后把引导页和历史页先做完"}, nil},
		{"overlay-recording-scene", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-5 * time.Second), Levels: wave(),
			Target: proto.App{Name: "Slack"}, Scene: sceneMatch("Team chat", appctx.SourceApp, "")}, nil},
		{"overlay-working-scene", session.Snapshot{State: session.Composing, Target: proto.App{Name: "Google Chrome"},
			Scene: sceneMatch("Code review", appctx.SourceURL, "github.com")}, nil},
		{"overlay-recognizing", session.Snapshot{State: session.Recognizing}, nil},
		{"overlay-composing", session.Snapshot{State: session.Composing, Partial: "那个我们明天，不对，后天下午三点开会，讨论一下 Auralink 的发布计划"}, nil},
		{"overlay-error", session.Snapshot{State: session.Failed, Problem: &session.Problem{Code: session.ProblemASR, Err: &asr.Error{Kind: asr.KindAuth}}}, nil},
		{"overlay-no-speech", session.Snapshot{State: session.Done, Problem: &session.Problem{Code: session.ProblemNoSpeech}}, nil},
		{"overlay-warning", session.Snapshot{State: session.Done, Result: "x", Inserted: true, Problem: &session.Problem{Code: session.ProblemLLM, Degraded: true,
			Err: errors.New("timeout")}}, nil},
		{"overlay-answer", session.Snapshot{State: session.Done, Card: true, Delivery: deliver.ReasonNotEditable,
			Result: "Hello team, the release moves to Thursday.\n\n1. Finish the onboarding\n2. Polish the history page\n3. Ship the translation card\n\nThanks!"}, nil},
		// Translate and Rewrite.
		{"overlay-translate-recording", session.Snapshot{Kind: store.KindTranslate, State: session.Recording, StartedAt: now, HeardAt: now.Add(-5 * time.Second), Levels: wave(), TargetLanguage: "en"}, nil},
		{"overlay-translate-selection", session.Snapshot{Kind: store.KindTranslate, State: session.Composing, Selection: "A goroutine is a lightweight thread.", TargetLanguage: "zh-Hans",
			Draft: "goroutine 是一种轻量级"}, nil},
		{"overlay-rewrite-writing", session.Snapshot{Kind: store.KindRewrite, State: session.Composing, Raw: "write a short note saying I will be late",
			Draft: "Sorry, I'm running about ten minutes late"}, nil},
		{"overlay-translate-card-streaming", session.Snapshot{Kind: store.KindTranslate, State: session.Composing, Card: true, Delivery: deliver.ReasonNotEditable,
			Selection: "A goroutine is a lightweight thread managed by the Go runtime.", TargetLanguage: "zh-Hans", Draft: "goroutine 是由 Go 运行时管理的"}, nil},
		{"overlay-translate-card", session.Snapshot{Kind: store.KindTranslate, State: session.Done, Card: true, Delivery: deliver.ReasonCard,
			Selection: "A goroutine is a lightweight thread managed by the Go runtime.", TargetLanguage: "zh-Hans",
			Result: "goroutine 是由 Go 运行时管理的轻量级线程。"}, nil},
		{"overlay-rewrite-card", session.Snapshot{Kind: store.KindRewrite, State: session.Done, Card: true, Delivery: deliver.ReasonLong,
			Raw: "写一段两百字的自我介绍，用于技术分享会的开场", Title: "技术分享开场自我介绍",
			Result: "大家好，我是关伟，一名专注于桌面应用和语音交互的工程师。过去几年，我主要在做跨平台的原生界面框架，也参与了几个语音输入工具的设计和开发。\n\n今天想和大家聊聊：怎样让语音输入真正融入日常的写作流程——从按下快捷键的那一刻，到文字出现在光标处，中间发生了什么，又有哪些地方值得打磨。"}, nil},
		{"overlay-rewrite-failed", session.Snapshot{Kind: store.KindRewrite, State: session.Failed, Card: true,
			Raw: "make it more formal", Selection: "hey, the release slips to thursday, sorry!",
			Problem: &session.Problem{Code: session.ProblemLLM, Err: errors.New("deepseek: server error (503): overloaded")}}, nil},
	}
	desk, line := ui.RGB(246, 246, 248), ui.RGB(150, 156, 166)
	if dark {
		desk, line = ui.RGB(30, 31, 36), ui.RGB(120, 128, 140)
	}
	for i, st := range states {
		id := "shot" + itoa(i)
		a.answer, a.notice = answerCard{}, nil
		a.prefs.Overlay.ShowTime = false
		if st.setup != nil {
			st.setup()
		}
		final := st.s
		final.ID = id
		live := final
		live.State = session.Recording
		live.Problem, live.Result, live.Inserted, live.Card = nil, "", false, false
		snap(a, live)
		snap(a, final)
		a.toasts = nil
		if a.ov.mode == ovHidden {
			t.Fatalf("%s: nothing shows", st.name)
		}
		w, h := a.ov.w+2*ovPad, a.ov.h+2*ovPad
		radius := faceRadius(a.prefs.Overlay, a.ov.mode, a.ov.h)
		tt := ui.NewTester(func(c *ui.Context) {
			// The desktop: a page of text lines.
			ui.Column(c).Absolute().Fill().Background(desk).Padding(10, 14).Gap(8).Children(func() {
				for j := range 12 {
					ui.Box(c.Key(itoa(j))).Height(6).Width(float32(w) * (0.45 + 0.4*float32((j*37)%10)/10)).Radius(3).Background(line)
				}
			})
			// The window's material: what is under the face, blurred.
			ui.Box(c).Absolute().Left(ovPad).Top(ovPad).Right(ovPad).Bottom(ovPad).Radius(radius).Material(glass.Blur{Radius: 8})
			// Positioned too, to paint over the blur.
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Children(func() { a.overlayView(c) })
		}, w, h)
		tt.SetScale(2)
		frames(tt)
		f, err := os.Create(filepath.Join(out, st.name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tt.Image())
		f.Close()
	}
	a.answer, a.notice = answerCard{}, nil
	a.prefs.Overlay.ShowTime = false
	snap(a, session.Snapshot{ID: "shot-end", State: session.Cancelled})
}

// sceneMatch is a matched group for a snapshot.
func sceneMatch(name string, src appctx.Source, host string) *appctx.Match {
	return &appctx.Match{Source: src, Group: &appctx.Group{ID: "g", Name: name}, Host: host, Pattern: host}
}

func TestOverlaySceneLabel(t *testing.T) {
	for _, tc := range []struct {
		s    session.Snapshot
		want string
	}{
		{session.Snapshot{}, ""},
		{session.Snapshot{Target: proto.App{Name: "Slack"}, Scene: &appctx.Match{Source: appctx.SourceGlobal}}, ""},
		{session.Snapshot{Target: proto.App{Name: "Slack"}, Scene: sceneMatch("Team chat", appctx.SourceApp, "")}, "Slack · Team chat"},
		{session.Snapshot{Target: proto.App{Name: "Google Chrome"}, Scene: sceneMatch("Code review", appctx.SourceURL, "github.com")}, "github.com · Code review"},
		{session.Snapshot{Target: proto.App{Name: "Google Chrome"}, Scene: sceneMatch("Work", appctx.SourceBrowserApp, "news.ycombinator.com")}, "Google Chrome · Work"},
		{session.Snapshot{Scene: sceneMatch("Work", appctx.SourceApp, "")}, "Work"},
	} {
		if got := sceneLabel(tc.s); got != tc.want {
			t.Errorf("sceneLabel = %q, want %q", got, tc.want)
		}
	}
	a, _, _ := newTestApp(t)
	snap(a, session.Snapshot{ID: "s1", State: session.Recording, Target: proto.App{Name: "Slack"}, Scene: sceneMatch("Team chat", appctx.SourceApp, "")})
	if a.ov.w != ovPillW+ovSceneW || a.ov.h != ovPillH {
		t.Errorf("size %dx%d", a.ov.w, a.ov.h)
	}
	if sceneShort(a.sess) != "Team chat" {
		t.Errorf("short %q", sceneShort(a.sess))
	}
}

func TestOverlayPlace(t *testing.T) {
	wa := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 875}
	x, y := overlayPlace(wa, settings.Overlay{Position: "bottom", Margin: 96}, 272, 84)
	if x != (1440-272)/2 || y != 25+875-84-(96-ovPad) {
		t.Errorf("bottom %d,%d", x, y)
	}
	_, y = overlayPlace(wa, settings.Overlay{Position: "top", Margin: 40}, 272, 84)
	if y != 25+40-ovPad {
		t.Errorf("top %d", y)
	}
}

// ovGone is how soon the overlay must be off the screen once nothing is
// left to show (the fade's two steps, and room).
const ovGone = 150 * time.Millisecond

// TestOverlayGoesAway: whatever ends what the overlay shows (an insert,
// Escape, a notice's time, the answer card's Close), it leaves the screen
// within ovGone and sets no timer again (10-15: after an insert, 043324a
// re-armed its "Inserted" timer from the timer itself, for ever).
func TestOverlayGoesAway(t *testing.T) {
	a, _, _ := newTestApp(t)
	ft := new(fakeTimers).install(a)
	gone := func(what string) {
		t.Helper()
		if a.ov.shown {
			t.Errorf("%s: still shown (%s)", what, a.ov.mode)
		}
		ft.advance(ovGone)
		if a.ov.visible {
			t.Errorf("%s: still on screen after %v (%s)", what, ovGone, a.ov.mode)
		}
		before := ft.added
		ft.advance(time.Minute)
		if a.ov.visible || ft.added != before || len(ft.timers) != 0 {
			t.Errorf("%s: timers set again: %d more, %d pending", what, ft.added-before, len(ft.timers))
		}
	}
	// An insert.
	snap(a, session.Snapshot{ID: "s1", State: session.Recording})
	if !a.ov.visible {
		t.Fatal("recording: not on screen")
	}
	snap(a, session.Snapshot{ID: "s1", State: session.Delivering})
	snap(a, session.Snapshot{ID: "s1", State: session.Done, Result: "Hello.", Inserted: true})
	gone("inserted")
	// Escape.
	snap(a, session.Snapshot{ID: "s2", State: session.Recording})
	snap(a, session.Snapshot{ID: "s2", State: session.Cancelled})
	gone("cancelled")
	// A notice, after its time.
	snap(a, session.Snapshot{ID: "s3", State: session.Recording})
	snap(a, session.Snapshot{ID: "s3", State: session.Done, Problem: &session.Problem{Code: session.ProblemNoSpeech}})
	if a.ov.mode != ovNotice || a.notice == nil || a.notice.text != "Nothing was heard." || !a.notice.warn {
		t.Fatalf("no speech: %s %+v", a.ov.mode, a.notice)
	}
	ft.advance(noticeTime - time.Millisecond)
	if a.ov.mode != ovNotice || !a.ov.visible {
		t.Fatalf("the notice went early: %s", a.ov.mode)
	}
	ft.advance(time.Millisecond)
	if a.notice != nil {
		t.Fatal("the notice stays")
	}
	gone("notice")
	// The answer card, closed.
	snap(a, session.Snapshot{ID: "s4", State: session.Recording})
	snap(a, session.Snapshot{ID: "s4", State: session.Done, Result: "Card", Card: true, Delivery: deliver.ReasonNotEditable})
	ft.advance(time.Minute)
	if a.ov.mode != ovAnswer || !a.ov.visible {
		t.Fatalf("the card went by itself: %s", a.ov.mode)
	}
	a.closeAnswer()
	gone("card closed")
}

// TestOverlayQuickSessions: what the last session left on its way out
// (the fade, a notice's timer) never hides the next one.
func TestOverlayQuickSessions(t *testing.T) {
	a, _, _ := newTestApp(t)
	ft := new(fakeTimers).install(a)
	snap(a, session.Snapshot{ID: "s1", State: session.Recording})
	snap(a, session.Snapshot{ID: "s1", State: session.Done, Result: "One.", Inserted: true})
	ft.advance(20 * time.Millisecond) // mid-fade
	snap(a, session.Snapshot{ID: "s2", State: session.Recording})
	ft.advance(time.Second)
	if a.ov.mode != ovRecording || !a.ov.shown || !a.ov.visible {
		t.Fatalf("the second recording hidden: %s shown %v visible %v", a.ov.mode, a.ov.shown, a.ov.visible)
	}
	// A notice of the second, then a third session failing: the second's
	// timer leaves the third's notice alone.
	snap(a, session.Snapshot{ID: "s2", State: session.Done, Problem: &session.Problem{Code: session.ProblemNoSpeech}})
	ft.advance(3 * time.Second)
	snap(a, session.Snapshot{ID: "s3", State: session.Recording})
	snap(a, session.Snapshot{ID: "s3", State: session.Failed, Problem: &session.Problem{Code: session.ProblemNoASR}})
	ft.advance(3 * time.Second) // the second's timer is due
	if a.notice == nil || a.notice.action != actionProviders || !a.ov.visible {
		t.Fatalf("the third's notice went with the second's timer: %+v", a.notice)
	}
	ft.advance(noticeTime)
	if a.notice != nil || a.ov.visible {
		t.Errorf("the third's notice stays: %+v", a.notice)
	}
}
