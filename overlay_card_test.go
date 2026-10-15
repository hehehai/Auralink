package main

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

// cardLong is a translation longer than the card's 220 of text.
var cardLong = strings.Repeat("goroutine 是由 Go 运行时管理的轻量级线程，启动成本很低，一个程序里同时运行成千上万个也很常见。", 12)

// cardStep is one state of the answer card, set on an app.
type cardStep struct {
	name  string
	apply func(a *app)
	// What it should be: the text's height (0: one line, -1: all of
	// it, ovCardBodyMax: scrolled), a line under it, the header's offer.
	body                                                   int
	msg                                                    bool
	insert, copy, followUp, stop, retry, lang, busy, convo bool
}

// cardSteps walk the answer card through its states, as one Translate
// card and one Rewrite conversation would.
func cardSteps() []cardStep {
	arc := proto.App{PID: 77, BundleID: "company.thebrowser.Browser", Name: "Arc"}
	sel := "A goroutine is a lightweight thread managed by the Go runtime."
	tr := func(st session.State, result, draft string) session.Snapshot {
		return session.Snapshot{ID: "t1", Kind: store.KindTranslate, State: st, Card: true, Delivery: deliver.ReasonNotEditable, Target: arc,
			Selection: sel, TargetLanguage: "zh-Hans", Result: result, Draft: draft}
	}
	return []cardStep{
		{name: "translating", apply: func(a *app) { snap(a, tr(session.Composing, "", "")) }, busy: true},
		{name: "streaming", apply: func(a *app) { snap(a, tr(session.Composing, "", "goroutine 是由 Go 运行时管理的")) }},
		{name: "streaming-long", apply: func(a *app) { snap(a, tr(session.Composing, "", cardLong)) }, body: ovCardBodyMax},
		{name: "short", apply: func(a *app) { snap(a, tr(session.Done, "goroutine 是由 Go 运行时管理的轻量级线程。", "goroutine 是由 Go 运行时管理的轻量级线程。")) },
			insert: true, copy: true, lang: true},
		{name: "redo", apply: func(a *app) {
			a.answer.redo, a.answer.redoLang = "t2", "ja"
			snap(a, session.Snapshot{ID: "t2", Kind: store.KindTranslate, State: session.Composing, Card: true, Target: arc, Selection: sel, TargetLanguage: "ja", Draft: "ゴルーチン"})
		}, copy: true, busy: true},
		{name: "redone-long", apply: func(a *app) {
			snap(a, session.Snapshot{ID: "t2", Kind: store.KindTranslate, State: session.Done, Card: true, Delivery: deliver.ReasonCard, Target: arc,
				Selection: sel, TargetLanguage: "ja", Result: cardLong})
		}, body: ovCardBodyMax, insert: true, copy: true, lang: true},
		{name: "copied", apply: func(a *app) { a.answer.copied = true }, body: ovCardBodyMax, insert: true, copy: true, lang: true},
		{name: "insert-failed", apply: func(a *app) {
			a.answer.copied, a.answer.text = false, "goroutine 是由 Go 运行时管理的轻量级线程。"
			a.answer.msg = deliveryText(deliver.Result{Reason: deliver.ReasonNotFront}, arc)
		}, msg: true, insert: true, copy: true, lang: true},
		{name: "failed", apply: func(a *app) {
			snap(a, session.Snapshot{ID: "t3", Kind: store.KindTranslate, State: session.Recording})
			snap(a, session.Snapshot{ID: "t3", Kind: store.KindTranslate, State: session.Failed, Card: true, Target: arc, Selection: sel,
				Problem: &session.Problem{Code: session.ProblemLLM, Err: errors.New("deepseek: server error (503): overloaded")}})
		}, body: -1, retry: true},
		{name: "rewrite", apply: func(a *app) {
			a.closeAnswer()
			snap(a, session.Snapshot{ID: "r1", Kind: store.KindRewrite, State: session.Recording})
			snap(a, session.Snapshot{ID: "r1", Kind: store.KindRewrite, State: session.Done, Card: true, Delivery: deliver.ReasonLong, Raw: "write that I am late",
				Title: "Running late", Result: "Sorry, I am running about ten minutes late: please start without me."})
		}, insert: true, copy: true, followUp: true},
		{name: "follow-up-recording", apply: func(a *app) {
			a.followUp, a.answer.conversation = "r1", true
			now := time.Now()
			snap(a, session.Snapshot{ID: "r2", Kind: store.KindRewrite, State: session.Recording, StartedAt: now, HeardAt: now, Levels: wave(), Partial: "shorter"})
		}, body: -1, copy: true, stop: true, busy: true, convo: true},
		{name: "follow-up-writing", apply: func(a *app) {
			snap(a, session.Snapshot{ID: "r2", Kind: store.KindRewrite, State: session.Composing, Card: true, Raw: "shorter", Title: "Late", Draft: "Ten minutes late"})
		}, body: -1, copy: true, busy: true, convo: true},
		{name: "conversation", apply: func(a *app) {
			snap(a, session.Snapshot{ID: "r2", Kind: store.KindRewrite, State: session.Done, Card: true, Delivery: deliver.ReasonCard, Raw: "shorter",
				Title: "Late", Result: "Ten minutes late: start without me."})
		}, body: -1, insert: true, copy: true, followUp: true, convo: true},
	}
}

// TestAnswerCardStates: the answer card through its states, the overlay
// at the bottom and at the top: 560 wide, as high as its text (Voxt's
// chrome and up to 220 of text, no empty space; a line under it when
// there is one), about the anchor; what its header offers.
func TestAnswerCardStates(t *testing.T) {
	for _, pos := range []string{"bottom", "top"} {
		t.Run(pos, func(t *testing.T) {
			a, _, _ := newTestApp(t)
			a.prefs.Overlay.Position = pos
			for _, st := range cardSteps() {
				st.apply(a)
				a.overlaySync()
				v := a.cardNow()
				if a.ov.mode != ovAnswer || !a.ov.shown || a.ov.passThrough || a.ov.w != ovAnswerW {
					t.Fatalf("%s: %s shown %v pass %v %d wide", st.name, a.ov.mode, a.ov.shown, a.ov.passThrough, a.ov.w)
				}
				body, _ := a.cardBodyHeight(v)
				_, line := guessText(ovSized{"x", ovCardTextSize, ovCardTextWeight, ovCardBodyW})
				switch st.body {
				case 0:
					if body != int(line) {
						t.Errorf("%s: the text is %d high, not a line (%v)", st.name, body, line)
					}
				case ovCardBodyMax:
					if body != ovCardBodyMax {
						t.Errorf("%s: the text is %d high, not %d", st.name, body, ovCardBodyMax)
					}
				default:
					if body <= int(line) || body > ovCardBodyMax {
						t.Errorf("%s: the text is %d high", st.name, body)
					}
				}
				want := ovCardChrome + body
				if st.msg {
					want += ovCardMsgGap + ovCardMsgH
				}
				if a.ov.h != want || (v.msg != "") != st.msg {
					t.Errorf("%s: %d high, want %d (line %q)", st.name, a.ov.h, want, v.msg)
				}
				anchored(t, st.name, a.prefs.Overlay, a.ov.frame)
				got := []bool{v.canInsert, v.canCopy, v.canFollowUp, v.canStop, v.canRetry, v.canLang, v.busy || v.streaming && v.text == "", v.conversation}
				exp := []bool{st.insert, st.copy, st.followUp, st.stop, st.retry, st.lang, st.busy, st.convo}
				for i := range got {
					if got[i] != exp[i] {
						t.Errorf("%s: insert, copy, follow up, stop, retry, language, busy, conversation = %v, want %v", st.name, got, exp)
						break
					}
				}
			}
		})
	}
}

// TestAnswerCardInk: each state as the view draws it, its texts measured
// (as on the Mac), checked from its pixels as the Mac's overlay check
// checks its captures: the face's size, its ink from the left padding
// down to just above the bottom padding (no empty space, nothing cut).
func TestAnswerCardInk(t *testing.T) {
	a, _, _ := newTestApp(t)
	defer func() { a.measureHook = guessText }()
	for _, st := range cardSteps() {
		st.apply(a)
		a.overlaySync()
		img := renderOverlay(t, a)
		v := a.cardNow()
		k, bad := checkOverlayInk(img, 2, cardInkWant(ovInkWant{w: a.ov.w, h: a.ov.h}, v))
		for _, b := range bad {
			t.Errorf("%s: %s", st.name, b)
		}
		t.Logf("%s: %v", st.name, k)
		if st.body == 0 && !st.msg && a.ov.h > ovCardChrome+24 {
			t.Errorf("%s: one line of text, the card %d high", st.name, a.ov.h)
		}
		if scrolls := a.ov.cardScroll.MaxY > 0; scrolls != (st.body == ovCardBodyMax) && !v.conversation {
			t.Errorf("%s: scrolls %v (%v)", st.name, scrolls, a.ov.cardScroll.MaxY)
		}
		if dir := os.Getenv("AURALINK_INK"); dir != "" {
			var b bytes.Buffer
			png.Encode(&b, img)
			os.WriteFile(filepath.Join(dir, "card-"+st.name+".png"), b.Bytes(), 0o644)
		}
	}
}

// TestAnswerCardInteraction: the header's buttons, the language menu,
// Continue and Done, the bubbles' Copy under the pointer, and Escape:
// it closes the card, and only cancels a follow-up (Voxt's).
func TestAnswerCardInteraction(t *testing.T) {
	a, _, _ := newTestApp(t)
	ft := new(fakeTimers).install(a)
	steps := cardSteps()
	for _, i := range []int{0, 3} { // translating, then the short answer
		steps[i].apply(a)
	}
	tt := cardTester(a)
	frames(tt)
	for _, s := range []string{"Translation", "goroutine 是由 Go 运行时管理的轻量级线程。", "Simplified Chinese", "Insert", "Copy", "Close"} {
		if !has(tt, s) {
			t.Errorf("no %q in %q", s, firstTexts(tt))
		}
	}
	// No line of orange: why it is in the card is Insert's tip.
	if has(tt, a.answer.why) || !strings.HasPrefix(a.answer.why, "The selection in Arc cannot be edited") {
		t.Errorf("why %q shows", a.answer.why)
	}
	// The language menu: the system's, the language checked; another
	// asks again (here nothing is set up: the line says so).
	if err := tt.Click("Translate into"); err != nil {
		t.Fatal(err)
	}
	if m := tt.Menu(); len(m) != len(compose.TargetLanguages) {
		t.Fatalf("menu %q", m)
	}
	if err := tt.ChooseMenuItem("Japanese"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.answer.msg != "No language model is set up." || a.answer.lang != "zh-Hans" || !a.answer.open {
		t.Errorf("another language: %+v", a.answer)
	}
	// Copy: the mark, then Copy again.
	click(t, tt, "Copy")
	if !has(tt, "Copied") || tt.Clipboard() != a.answer.text || !a.answer.open {
		t.Errorf("copied: %q", tt.Clipboard())
	}
	ft.advance(copyMark)
	if !has(tt, "Copy") || a.answer.copied {
		t.Error("the mark stays")
	}
	// Escape closes it.
	a.hotkeyFired(proto.HotkeyEventParams{Binding: proto.HotkeyEscape, Phase: proto.PhaseTrigger})
	if a.answer.open || a.ov.shown {
		t.Fatalf("escape: %+v", a.answer)
	}

	// A Rewrite answer: Continue follows up (here the model is missing:
	// a notice, the card stays as it was).
	steps[9].apply(a)
	frames(tt)
	click(t, tt, "Continue")
	if !a.answer.open || a.answer.conversation || a.notice == nil || a.notice.text != "No speech service is set up." {
		t.Errorf("continue: %+v %+v", a.answer, a.notice)
	}
	// A follow-up records in the card: Done stops it; Escape cancels it,
	// the card staying with its answer; a second Escape closes it.
	steps[10].apply(a)
	frames(tt)
	if !has(tt, "Done") || !has(tt, "shorter") || !has(tt, "You") || has(tt, "Continue") {
		t.Errorf("recording: %q", firstTexts(tt))
	}
	a.hotkeyFired(proto.HotkeyEventParams{Binding: proto.HotkeyEscape, Phase: proto.PhaseTrigger})
	snap(a, session.Snapshot{ID: "r2", Kind: store.KindRewrite, State: session.Cancelled}) // as the manager would
	if !a.answer.open || a.ov.mode != ovAnswer || a.followUp != "" || a.answer.text == "" {
		t.Errorf("escape on a follow-up: %s %+v", a.ov.mode, a.answer)
	}
	if v := a.cardNow(); !v.conversation || !v.canFollowUp || len(v.bubbles) != 2 {
		t.Errorf("after the cancel %+v", v)
	}
	// An answer's bubble shows Copy under the pointer.
	frames(tt)
	r, ok := tt.Find("Sorry, I am running about ten minutes late: please start without me.")
	if !ok {
		t.Fatalf("no answer bubble in %q", firstTexts(tt))
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	frames(tt)
	title, ok := tt.Find("Running late") // the bubble's heading, 12 inside it
	if !ok {
		t.Fatalf("no heading in %q", firstTexts(tt))
	}
	// Its Copy: 8 from the bubble's top right corner, 22 high.
	tt.ClickAt(title.X-ovBubblePadX+ovBubbleW-8-10, title.Y-ovBubblePadY+8+11)
	frames(tt)
	if tt.Clipboard() != "Running late\n\nSorry, I am running about ten minutes late: please start without me." || a.ov.bubbleCopied == "" {
		t.Errorf("bubble copied %q", tt.Clipboard())
	}
	ft.advance(bubbleMark)
	if a.ov.bubbleCopied != "" {
		t.Error("the bubble's mark stays")
	}
	a.hotkeyFired(proto.HotkeyEventParams{Binding: proto.HotkeyEscape, Phase: proto.PhaseTrigger})
	if a.answer.open {
		t.Error("the second Escape left the card")
	}
}
