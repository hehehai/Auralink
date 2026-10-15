package main

// The overlay check (scripts/mac-overlay-check.sh, docs/manual-test.md
// §10.5): the real overlay window on the Mac, walked through its states
// over a light and a dark backdrop, each state captured by the script
// (screencapture, from outside: the check takes no Screen Recording
// permission) and its window's frame recorded, so that a change of the
// overlay can be looked at without a session, a microphone or a key.
//
// It runs instead of the app when started with --overlay-check <dir>:
// accessory (no Dock icon, never active), no main window, no helper, no
// menu bar icon, nothing read or written but <dir>; non-activating
// windows at the overlay's own place, each state shown about a second.
// For each step it writes <dir>/step-N.ready ("window x y w h": the
// overlay's window number and the backdrop's rectangle) and waits (5 s at
// most) for <dir>/step-N.done, which the script touches once it captured
// step-N-window.png (the window alone) and step-N-screen.png (the
// backdrop with the overlay on it). frames.txt records each step: the
// frame the overlay asked for, the window's own, whether it shows, the app
// in front, and what the window's capture shows of the face (its ink,
// overlay_ink.go); a line starting FAIL when the anchor moved (the bottom
// edge or the centre, from the first pill), the window is not where it was
// asked to be, the check came to the front, or the face's ink is not
// where it should be: the pill not 148×48, its icon's side and its
// waveform's more than a point apart, their middles more than a point
// apart, short text not centred or the face not as wide as it, long text
// not at 300 and scrolled to its end. sheet.png puts the
// screens side by side, the anchor drawn over them. Then it quits.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/macx"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// overlayCheckDir is the directory --overlay-check names ("": not the
// check).
func overlayCheckDir() string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == "--overlay-check" {
			return os.Args[i+1]
		}
	}
	return ""
}

// ovCheckStep is one state of the check: what it does to the app, and
// whether the script captures it.
type ovCheckStep struct {
	name  string
	apply func(a *app)
	shot  bool
}

// ovCheckSteps are the states, in order: the pill, live text short and
// long (scrolled), working, gone after an insert, the next session's pill,
// a notice, gone.
func ovCheckSteps(lang string) []ovCheckStep {
	now := time.Now()
	levels := previewSession().Levels
	short, long := "Meet at three tomorrow", "So the plan for the release is to finish the onboarding first, then the history page, and after that the translation card"
	if lang != "en" {
		short, long = "明天三点开会", "那个我们明天，不对，后天下午三点开会，讨论一下 Auralink 的发布计划，然后把引导页和历史页先做完"
	}
	rec := func(id, partial string) func(*app) {
		return func(a *app) {
			a.notice = nil
			a.sess = session.Snapshot{ID: id, Kind: store.KindTranscribe, State: session.Recording, StartedAt: now, HeardAt: now, Levels: levels, Partial: partial}
		}
	}
	return append([]ovCheckStep{
		{"pill", rec(lang+"-1", ""), true},
		{"live-short", rec(lang+"-1", short), true},
		{"live-long", rec(lang+"-1", long), true},
		{"working", func(a *app) { a.sess.State = session.Composing }, true},
		{"inserted", func(a *app) { a.sess.State, a.sess.Inserted = session.Done, true }, false},
		{"pill-again", rec(lang+"-2", ""), true},
		{"notice", func(a *app) {
			a.sess.State = session.Failed
			text, more, action := sessionProblemText(&session.Problem{Code: session.ProblemASR, Err: &asr.Error{Kind: asr.KindAuth}})
			a.notice = &sessionNotice{text: text, more: more, action: action}
		}, true},
		{"gone", func(a *app) { a.notice = nil }, false},
	}, cardCheckSteps(lang)...)
}

// cardCheckSteps are the answer card's states (overlay_card.go): a
// translation streaming in, short, into another language, copied, Insert
// that did not work, long (it scrolls), a Rewrite answer, a follow-up
// recording in its conversation, the conversation, a failure; then gone.
func cardCheckSteps(lang string) []ovCheckStep {
	now := time.Now()
	arc := proto.App{PID: 1, Name: "Arc"}
	src, short, long := "我们把发布推迟到周四，先把引导页和历史页做完。", "We are moving the release to Thursday, to finish the onboarding and the history page first.", ""
	if lang != "en" {
		src, short = "We are moving the release to Thursday.", "我们把发布推迟到周四，先把引导页和历史页做完。"
	}
	for range 30 {
		long += short + " "
	}
	tr := func(id string, st session.State, result, draft, target string) session.Snapshot {
		return session.Snapshot{ID: id, Kind: store.KindTranslate, State: st, Card: true, Delivery: deliver.ReasonNotEditable, Target: arc,
			Selection: src, TargetLanguage: target, Result: result, Draft: draft}
	}
	end := func(s session.Snapshot) func(*app) {
		return func(a *app) { a.sess = s; a.openAnswer(s) }
	}
	return []ovCheckStep{
		{"card-streaming", func(a *app) {
			a.answer, a.followUp = answerCard{}, ""
			a.sess = tr(lang+"-t1", session.Composing, "", short[:len(short)/2], lang)
		}, true},
		{"card-short", end(tr(lang+"-t1", session.Done, short, short, lang)), true},
		{"card-redo", func(a *app) {
			a.answer.redo, a.answer.redoLang = lang+"-t2", "ja"
			a.sess = tr(lang+"-t2", session.Composing, "", "", "ja")
		}, true},
		{"card-copied", func(a *app) {
			a.answer.redo, a.answer.redoLang, a.answer.copied = "", "", true
			a.sess = tr(lang+"-t1", session.Done, short, short, lang)
		}, true},
		{"card-insert-failed", func(a *app) {
			a.answer.copied = false
			a.answer.msg = deliveryText(deliver.Result{Reason: deliver.ReasonNotFront}, arc)
		}, true},
		{"card-long", end(tr(lang+"-t3", session.Done, long, long, lang)), true},
		{"card-rewrite", func(a *app) {
			a.answer = answerCard{}
			end(session.Snapshot{ID: lang + "-r1", Kind: store.KindRewrite, State: session.Done, Card: true, Delivery: deliver.ReasonLong, Target: arc,
				Raw: "write that I am late", Title: "Running late", Result: short})(a)
		}, true},
		{"card-follow-up", func(a *app) {
			a.followUp, a.answer.conversation = a.answer.id, true
			a.sess = session.Snapshot{ID: lang + "-r2", Kind: store.KindRewrite, State: session.Recording, StartedAt: now, HeardAt: now,
				Levels: previewSession().Levels, Partial: "shorter"}
		}, true},
		{"card-conversation", end(session.Snapshot{ID: lang + "-r2", Kind: store.KindRewrite, State: session.Done, Card: true, Delivery: deliver.ReasonCard,
			Target: arc, Raw: "shorter", Title: "Late", Result: "Ten minutes late: start without me."}), true},
		{"card-failed", func(a *app) {
			a.answer, a.followUp = answerCard{}, ""
			end(session.Snapshot{ID: lang + "-t4", Kind: store.KindTranslate, State: session.Failed, Card: true, Target: arc, Selection: src,
				Problem: &session.Problem{Code: session.ProblemLLM, Err: errors.New("server error (503): overloaded")}})(a)
		}, true},
		{"card-gone", func(a *app) { a.answer = answerCard{}; a.sess.State = session.Cancelled }, false},
	}
}

// runOverlayCheck runs the check in place of the app, then quits.
func runOverlayCheck(dir string) {
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	a := &app{}
	a.prefs = settings.Defaults()
	a.ov.native = true
	a.postQueue = mygo.RunOnMain
	mygo.App.WhenReady(func() { go a.overlayCheck(dir) })
	// Whatever happens, it does not stay.
	time.AfterFunc(3*time.Minute, func() { mygo.RunOnMain(mygo.App.Quit) })
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ovCheckShot is a captured step for the sheet.
type ovCheckShot struct {
	name   string
	file   string
	region mygo.Rectangle // the backdrop's, which the capture shows
	anchor mygo.Rectangle // the first pill's frame
}

// overlayCheck walks the steps over each backdrop; on a goroutine.
func (a *app) overlayCheck(dir string) {
	onMain := func(fn func()) {
		done := make(chan struct{})
		mygo.RunOnMain(func() { fn(); close(done) })
		<-done
	}
	var log strings.Builder
	logf := func(format string, args ...any) { fmt.Fprintf(&log, format+"\n", args...) }
	self := os.Getpid()
	logf("# step mode asked=x,y,w,h window=x,y,w,h visible front")
	n := 0
	var columns [][]ovCheckShot
	for _, bd := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"zh-Hans", true}} {
		i18n.SetLocale(bd.lang)
		var back *mygo.Window
		var region mygo.Rectangle
		onMain(func() {
			area := a.overlayArea()
			// The backdrop: under the largest face (the answer card), 20
			// below the face's bottom edge (or above its top).
			wide := overlayFrame(area, a.prefs.Overlay, max(ovNoticeW2, ovAnswerW), ovCardMaxH)
			bw, bh := wide.Width+60, wide.Height+40
			region = mygo.Rectangle{X: wide.X - 30, Y: wide.Y + wide.Height - ovPad + 20 - bh, Width: bw, Height: bh}
			opts := floatingWindowOptions("Auralink overlay check", bw, bh, ui.View(func(c *ui.Context) { ovCheckBackdrop(c, bd.dark) }))
			opts.Transparent = false
			back = mygo.NewWindow(opts)
			showFloating(back, region.X, region.Y)
		})
		time.Sleep(300 * time.Millisecond)
		var anchor mygo.Rectangle
		var column []ovCheckShot
		for _, st := range ovCheckSteps(bd.lang) {
			onMain(func() { st.apply(a); a.overlaySync() })
			time.Sleep(600 * time.Millisecond) // the reveal, the fade, a few frames
			var got mygo.Rectangle
			var wid int
			var visible, shown bool
			var asked mygo.Rectangle
			var mode overlayMode
			var ink ovInkWant
			onMain(func() {
				shown, asked, mode = a.ov.shown, a.ov.frame, a.ov.mode
				ink = a.overlayInkWant()
				if a.ov.win != nil {
					got, visible = a.ov.win.Bounds(), a.ov.win.IsVisible()
					wid = macx.WindowNumber(a.ov.win.NativeHandle())
				}
			})
			var front macx.App
			onMain(func() { front = macx.Frontmost() })
			name := fmt.Sprintf("%s/%s", bd.lang, st.name)
			logf("%d %s %s asked=%d,%d,%d,%d window=%d,%d,%d,%d visible=%v front=%s(%d)", n, name, mode,
				asked.X, asked.Y, asked.Width, asked.Height, got.X, got.Y, got.Width, got.Height, visible, front.BundleID, front.PID)
			switch {
			case front.PID == self:
				logf("FAIL %s: the check is in front", name)
			case shown && got != asked:
				logf("FAIL %s: the window is not where it was asked to be", name)
			case shown && !visible, !shown && visible:
				logf("FAIL %s: shown %v, on screen %v", name, shown, visible)
			}
			if shown {
				if anchor.Width == 0 {
					anchor = got
				}
				if got.Y+got.Height != anchor.Y+anchor.Height || abs(2*got.X+got.Width-(2*anchor.X+anchor.Width)) > 1 {
					logf("FAIL %s: the anchor moved: bottom %d (was %d), centre %d (was %d)", name,
						got.Y+got.Height, anchor.Y+anchor.Height, got.X+got.Width/2, anchor.X+anchor.Width/2)
				}
			}
			if st.shot && visible {
				ready := filepath.Join(dir, fmt.Sprintf("step-%d.ready", n))
				os.WriteFile(ready, fmt.Appendf(nil, "%d %d %d %d %d\n", wid, region.X, region.Y, region.Width, region.Height), 0o644)
				done := filepath.Join(dir, fmt.Sprintf("step-%d.done", n))
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
					if _, err := os.Stat(done); err == nil {
						break
					}
				}
				// The face's ink, from the window's capture.
				if img, err := ovCheckImage(filepath.Join(dir, fmt.Sprintf("step-%d-window.png", n))); err != nil {
					logf("FAIL %s: the capture: %v", name, err)
				} else {
					k, bad := checkOverlayInk(img, float64(img.Bounds().Dx())/float64(got.Width), ink)
					logf("  ink %s", k)
					for _, b := range bad {
						logf("FAIL %s: %s", name, b)
					}
				}
				column = append(column, ovCheckShot{name: name, file: filepath.Join(dir, fmt.Sprintf("step-%d-screen.png", n)), region: region, anchor: anchor})
			}
			n++
		}
		onMain(func() { back.Destroy() })
		columns = append(columns, column)
	}
	if err := ovCheckSheet(filepath.Join(dir, "sheet.png"), columns); err != nil {
		logf("FAIL the sheet: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "frames.txt"), []byte(log.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "finished"), nil, 0o644)
	onMain(func() {
		a.closeOverlay()
		mygo.App.Quit()
	})
}

func abs(v int) int { return max(v, -v) }

// overlayInkWant is what the face shows now, for the ink check: the
// waveform's row even on both sides; the text under it, scrolled when it
// is wider than its line. A notice's icon is coloured, not ink: only its
// size is checked.
func (a *app) overlayInkWant() ovInkWant {
	w := ovInkWant{w: a.ov.w, h: a.ov.h}
	if a.ov.mode == ovAnswer {
		return cardInkWant(w, a.cardNow())
	}
	if a.ov.mode != ovRecording && a.ov.mode != ovWorking {
		return w
	}
	w.row = true
	w.even = a.ov.mode == ovRecording && !a.prefs.Overlay.ShowTime && sceneShort(a.sess) == ""
	if text := liveLineText(a.sess); text != "" {
		tw, _ := a.ovTextWidth(liveTextKey(text))
		w.text, w.scrolled = true, tw > ovLiveTextW
	}
	return w
}

// cardInkWant is what the answer card v should show: its ink from its
// left padding down to its text's last line (a text's line, 14 at most
// 22 high, and the small line under it when there is one, whose ink may
// be too dim to count), or to its bottom padding when it scrolls.
func cardInkWant(w ovInkWant, v cardView) ovInkWant {
	w.card, w.gap = true, 8
	if !v.conversation {
		w.gap = 22
	} else {
		w.gap = ovBubblePadY + 8 // a bubble's fill is not ink
	}
	if v.msg != "" {
		w.gap += ovCardMsgGap + ovCardMsgH
	}
	return w
}

// ovCheckImage decodes a capture.
func ovCheckImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// ovCheckBackdrop is the backdrop: a page of text lines, light or dark.
func ovCheckBackdrop(c *ui.Context, dark bool) {
	desk, line := ui.RGB(246, 246, 248), ui.RGB(150, 156, 166)
	if dark {
		desk, line = ui.RGB(30, 31, 36), ui.RGB(120, 128, 140)
	}
	ui.Column(c).Fill().Background(desk).Padding(12, 16).Gap(9).Children(func() {
		for j := range 14 {
			ui.Box(c.Key(itoa(j))).Height(6).WidthPercent(45 + 40*float32((j*37)%10)/10).Radius(3).Background(line)
		}
	})
}

// ovCheckSheet renders the captured screens side by side, a column a
// backdrop, each with its step's name and the anchor over it: the line
// of the first pill's face's bottom edge and its centre.
func ovCheckSheet(path string, columns [][]ovCheckShot) error {
	type pic struct {
		shot ovCheckShot
		bmp  *ui.Bitmap
	}
	var cols [][]pic
	cellW, rows := 0, 0
	for _, col := range columns {
		var ps []pic
		for _, s := range col {
			data, err := os.ReadFile(s.file)
			if err != nil {
				return err
			}
			b, err := ui.DecodeBitmap(data)
			if err != nil {
				return err
			}
			ps = append(ps, pic{s, b})
			cellW = max(cellW, s.region.Width)
		}
		rows = max(rows, len(ps))
		cols = append(cols, ps)
	}
	if cellW == 0 {
		return fmt.Errorf("nothing captured")
	}
	cellH := columns[0][0].region.Height
	const labelH, gap = 22, 14
	w := len(cols)*cellW + (len(cols)+1)*gap
	h := rows*(cellH+labelH+gap) + gap
	guide := ui.RGBA(255, 59, 48, 0.85)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Background(ui.RGB(255, 255, 255)).Padding(gap).Gap(gap).AlignItems(ui.Start).Children(func() {
			for i, col := range cols {
				ui.Column(c.Key(i)).Width(float32(cellW)).Gap(gap).Children(func() {
					for j, p := range col {
						r, an := p.shot.region, p.shot.anchor
						ui.Column(c.Key(j)).Children(func() {
							ui.Text(c, p.shot.name).FontSize(12).Height(labelH).TextColor(ui.RGB(60, 60, 67))
							ui.Box(c).Size(float32(r.Width), float32(r.Height)).Children(func() {
								ui.Image(c, p.bmp).Absolute().Fill().Fit(ui.FillBox)
								bottom := float32(an.Y + an.Height - ovPad - r.Y)
								centre := float32(an.X + an.Width/2 - r.X)
								ui.Box(c).Absolute().Left(0).Right(0).Top(bottom).Height(1).Background(guide)
								ui.Box(c).Absolute().Top(0).Bottom(0).Left(centre).Width(1).Background(guide)
							})
						})
					}
				})
			}
		})
	}, w, h)
	tt.SetScale(2)
	for range 3 {
		tt.Frame()
	}
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}
