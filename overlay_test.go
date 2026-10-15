package main

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
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
	snap(a, session.Snapshot{State: session.Done, Result: "Hello.", Inserted: true})
	if a.ov.mode != ovDone {
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

func TestClockAndLastLines(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", 9 * time.Second: "0:09", 75 * time.Second: "1:15", 10 * time.Minute: "10:00"} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q", d, got)
		}
	}
	long := ""
	for range 40 {
		long += "word "
	}
	if got := lastLines(long, 3); len([]rune(got)) > 48*3 || got[:3] != "…" {
		t.Errorf("lastLines %q", got)
	}
	if lastLines("  short ", 3) != "short" {
		t.Error("short text")
	}
}

// overlayShots renders every state of the overlay (snapshot_test.go):
// the face over a desktop grey, so its shadow and edge show.
func overlayShots(t *testing.T, a *app, out, suffix string) {
	t.Helper()
	now := time.Now()
	states := []struct {
		name   string
		s      session.Snapshot
		notice *sessionNotice
	}{
		{"overlay-starting", session.Snapshot{State: session.Recording, StartedAt: now}, nil},
		{"overlay-recording", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-12 * time.Second), Levels: wave()}, nil},
		{"overlay-live-text", session.Snapshot{State: session.Recording, StartedAt: now, HeardAt: now.Add(-47 * time.Second), Levels: wave(),
			Partial: "So the plan for the release is to finish the onboarding first, then the history page, and after that we look at the translation card"}, nil},
		{"overlay-recognizing", session.Snapshot{State: session.Recognizing}, nil},
		{"overlay-composing", session.Snapshot{State: session.Composing, Partial: "那个我们明天，不对，后天下午三点开会，讨论一下 Auralink 的发布计划"}, nil},
		{"overlay-done", session.Snapshot{State: session.Done, Result: "x", Inserted: true}, nil},
		{"overlay-notice", session.Snapshot{State: session.Failed, Problem: &session.Problem{Code: session.ProblemNoASR}}, nil},
		{"overlay-warning", session.Snapshot{State: session.Done, Result: "x", Inserted: true, Problem: &session.Problem{Code: session.ProblemLLM, Degraded: true,
			Err: errors.New("timeout")}}, nil},
		{"overlay-answer", session.Snapshot{State: session.Done, Card: true, Delivery: deliver.ReasonNotEditable,
			Result: "Hello team, the release moves to Thursday.\n\n1. Finish the onboarding\n2. Polish the history page\n3. Ship the translation card\n\nThanks!"}, nil},
	}
	for i, st := range states {
		id := "shot" + itoa(i)
		a.answer, a.notice, a.ov.done = answerCard{}, nil, ""
		final := st.s
		final.ID = id
		live := final
		live.State = session.Recording
		live.Problem, live.Result, live.Inserted, live.Card = nil, "", false, false
		snap(a, live)
		a.ov.shown = true
		snap(a, final)
		a.toasts = nil
		w, h := a.ov.w+2*ovPad, a.ov.h+2*ovPad
		tt := ui.NewTester(a.overlayView, w, h)
		tt.SetScale(2)
		frames(tt)
		img := tt.Image()
		bg := image.NewRGBA(img.Bounds())
		draw.Draw(bg, bg.Bounds(), &image.Uniform{color.RGBA{0x7c, 0x84, 0x90, 0xff}}, image.Point{}, draw.Src)
		draw.Draw(bg, bg.Bounds(), img, img.Bounds().Min, draw.Over)
		f, err := os.Create(filepath.Join(out, st.name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, bg)
		f.Close()
	}
	a.answer, a.notice = answerCard{}, nil
	snap(a, session.Snapshot{ID: "shot-end", State: session.Cancelled})
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
