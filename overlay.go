package main

// The recording overlay (plan §3.2): Auralink's one floating window. It
// is non-activating (floating.go, mygo patch 0003): clicking it leaves
// the app the user dictates into in front, with its caret. It always
// uses the dark tokens, whatever the appearance.
//
//	recording   240×52, 380×120 with live text: the feature's icon, a
//	            16-bar waveform, the time, the last lines of the live
//	            text, Stop and × (cancel; Escape too, through the helper)
//	working     recognising / cleaning up / translating / writing /
//	            inserting, with a spinner, the answer as it comes, and ×
//	done        a check for a moment after the text was inserted
//	notice      one line, an action ("Open Model services"), × (4 s)
//	answer      560×340: the result that was not inserted, Insert (ink),
//	            Copy, Close; it streams while the model writes, shows a
//	            Rewrite answer's title, Translate's target language (to
//	            translate again into another), and a failure with Try
//	            again
//
// Its place and look come from settings.Overlay: the top or bottom of
// the display under the pointer, the margin from that edge, the opacity
// of its face and the radius of its corners.

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// overlayMode is what the overlay shows.
type overlayMode int

const (
	ovHidden overlayMode = iota
	ovRecording
	ovWorking
	ovDone
	ovNotice
	ovAnswer
)

func (m overlayMode) String() string {
	return [...]string{"hidden", "recording", "working", "done", "notice", "answer"}[m]
}

// Sizes of the overlay's face, in DIPs; its window adds ovPad around it
// for the face's own shadow (the window has none: macOS would draw its
// rectangle's).
const (
	ovPad       = 16
	ovPillW     = 240
	ovPillH     = 52
	ovLiveW     = 380
	ovLiveH     = 120
	ovNoticeW   = 480
	ovAnswerW   = 560
	ovAnswerH   = 340
	ovDoneTime  = 700 * time.Millisecond
	ovLiveLines = 3
)

// overlayState is the overlay's window and what it shows.
type overlayState struct {
	ov struct {
		win   *mygo.Window
		shown bool
		mode  overlayMode
		w, h  int // the face's size
		// area is the work area of the display it shows on, chosen when
		// it appears (the display under the pointer); a resize stays there.
		area mygo.Rectangle
		// done is the session whose "inserted" check shows (until its
		// timer clears it).
		done string
	}
}

// overlayModeNow decides what the overlay shows: the answer card (an
// ended session's, or one the model writes into), a live session (not a
// try: the page shows those), a notice, the check after an insert, or
// nothing.
func (a *app) overlayModeNow() overlayMode {
	s := a.sess
	live := s.State.Live() && !s.Try
	switch {
	case a.answer.open && !live:
		return ovAnswer
	case live && s.Card && s.State == session.Composing:
		return ovAnswer
	case live && (s.State == session.Recording || s.State == session.Reading):
		return ovRecording
	case s.State.Live() && !s.Try:
		return ovWorking
	case a.notice != nil:
		return ovNotice
	case s.State == session.Done && s.Inserted && a.ov.done == s.ID:
		return ovDone
	}
	return ovHidden
}

// overlaySize is the face's size for mode.
func (a *app) overlaySize(mode overlayMode) (int, int) {
	switch mode {
	case ovAnswer:
		return ovAnswerW, ovAnswerH
	case ovNotice:
		return ovNoticeW, ovPillH
	case ovRecording, ovWorking:
		if liveText(a.sess) != "" {
			return ovLiveW, ovLiveH
		}
	}
	return ovPillW, ovPillH
}

// overlaySync shows, resizes or hides the overlay after a change of the
// session, the notice or the answer card. Main thread.
func (a *app) overlaySync() {
	s := a.sess
	if s.State == session.Done && s.Inserted && a.notice == nil && a.ov.done != s.ID && !a.answer.open && !s.Try && a.ov.shown {
		// A check for a moment, then hide.
		a.ov.done = s.ID
		if a.win != nil {
			id := s.ID
			time.AfterFunc(ovDoneTime, func() {
				a.post(func() {
					if a.ov.done == id {
						a.ov.done = ""
						a.overlaySync()
					}
				})
			})
		}
	}
	mode := a.overlayModeNow()
	w, h := a.overlaySize(mode)
	was := a.ov.shown
	resized := w != a.ov.w || h != a.ov.h
	a.ov.mode, a.ov.w, a.ov.h = mode, w, h
	if mode == ovHidden {
		a.ov.shown = false
		if a.ov.win != nil && was {
			a.ov.win.Hide()
		}
		return
	}
	a.ov.shown = true
	if a.win == nil {
		return // tests: the state is enough
	}
	winW, winH := w+2*ovPad, h+2*ovPad
	if a.ov.win == nil {
		a.ov.win = mygo.NewWindow(floatingWindowOptions("Auralink", winW, winH, ui.View(a.overlayView)))
	}
	if !was || resized {
		if !was {
			a.ov.area = mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
		}
		x, y := overlayPlace(a.ov.area, a.prefs.Overlay, winW, winH)
		if resized {
			a.ov.win.SetContentSize(winW, winH)
		}
		if was {
			a.ov.win.SetPosition(x, y)
		} else {
			showFloating(a.ov.win, x, y)
		}
	}
	a.ov.win.Invalidate()
}

// overlayPlace is where a window of w × h goes in the work area wa:
// centred, Overlay.Margin from its bottom (or top) edge.
func overlayPlace(wa mygo.Rectangle, o settings.Overlay, w, h int) (int, int) {
	margin := int(math.Round(o.Margin)) - ovPad
	x := wa.X + (wa.Width-w)/2
	y := wa.Y + wa.Height - h - margin
	if o.Position == "top" {
		y = wa.Y + margin
	}
	return x, y
}

// closeOverlay destroys the window (quit).
func (a *app) closeOverlay() {
	if a.ov.win != nil {
		a.ov.win.Destroy()
		a.ov.win = nil
	}
	a.ov.shown = false
}

// overlayTheme is the overlay's theme: the dark tokens, always.
func overlayTheme() *ui.Theme {
	th := *ui.DarkTheme()
	styleTheme(&th)
	return &th
}

// overlayView is the overlay window's content.
func (a *app) overlayView(c *ui.Context) {
	c.SetTheme(overlayTheme())
	// The root draws the theme's background unless told not to: in this
	// transparent window only the face may show. Last, once the view is
	// built: the kit's ink and record buttons swap the theme while they
	// build (AudioSmith's pill).
	defer c.Root().Background(ui.Transparent)
	if a.win == nil || a.prefs.Appearance.ReduceMotion {
		c.DisableAnimations()
	}
	if a.ov.mode == ovRecording {
		c.After(250 * time.Millisecond) // the time
	}
	ui.Column(c).Fill().Padding(ovPad).Children(func() { a.overlayFace(c) })
}

// overlayFace is the face: the pill, the notice or the card.
func (a *app) overlayFace(c *ui.Context) {
	o := a.prefs.Overlay
	radius := float32(o.Radius)
	if a.ov.mode != ovAnswer {
		radius = min(radius, float32(a.ov.h)/2)
	}
	alpha := float32(min(max(o.Opacity, 0.5), 1))
	face := ui.Column(c.Key("ov-face")).Fill().Radius(radius).Background(ui.RGBA(22, 22, 26, 0.96*alpha)).
		Border(1, ui.RGBA(255, 255, 255, 0.12)).Shadow(0, 6, 16, 0, ui.RGBA(0, 0, 0, 0.32)).DragWindow()
	face.Children(func() {
		switch a.ov.mode {
		case ovAnswer:
			a.overlayAnswer(c)
		case ovNotice:
			a.overlayNotice(c)
		default:
			a.overlayPill(c)
		}
	})
}

var (
	ovText  = ui.RGB(245, 245, 247)
	ovText2 = ui.RGBA(235, 235, 245, 0.62)
)

// overlayPill is the recording, working and done pill, with the live text
// below it when there is some.
func (a *app) overlayPill(c *ui.Context) {
	t := c.Theme()
	s := a.sess
	ui.Column(c).Fill().Padding(0, 10, 0, 14).Children(func() {
		ui.Row(c).Height(ovPillH).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
			switch a.ov.mode {
			case ovRecording:
				ui.Icon(c, kindIcon(s.Kind).Bold).Size(18, 18).TextColor(tok(t).Record).Label(kindTitle(s.Kind))
				a.waveform(c, s.Levels, !s.HeardAt.IsZero())
				ui.Text(c, clock(s.Elapsed(time.Now()))).FontSize(12).Font("monospace").TextColor(ovText2).Shrink(0)
				if ui.ButtonBase(c.Key("ov-stop")).Size(28, 28).Center().Radius(14).Background(tok(t).Record).Label(i18n.T("Stop")).Children(func() {
					ui.Box(c).Width(10).Height(10).Radius(2).Background(ui.RGB(255, 255, 255))
				}).Clicked() {
					a.stopSession()
				}
			case ovWorking:
				ui.Spinner(c)
				ui.Text(c, workingText(s)).FontSize(13).TextColor(ovText).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			case ovDone:
				ui.Icon(c, icons.CheckCircle.Bold).Size(18, 18).TextColor(tok(t).Success)
				ui.Text(c, i18n.T("Inserted")).FontSize(13).TextColor(ovText).SingleLine().Grow(1).MinWidth(0)
			}
			if a.ov.mode != ovDone {
				closeLabel := i18n.T("Cancel (Esc)")
				if ui.ButtonBase(c.Key("ov-cancel")).Size(28, 28).Center().Radius(14).Label(closeLabel).Children(func() {
					ui.Icon(c, icons.Close.Linear).Size(16, 16).TextColor(ovText2)
				}).Clicked() {
					a.cancelSession()
				}
			}
		})
		if live := lastLines(liveText(s), ovLiveLines); live != "" && a.ov.mode != ovDone {
			ui.Text(c, live).FontSize(13).TextColor(ovText).MaxLines(ovLiveLines).Ellipsis("…").Padding(0, 4, 12, 4)
		}
	})
}

// liveText is the text the pill shows under it: the answer as the model
// writes it, else what the speech service heard so far.
func liveText(s session.Snapshot) string {
	if d := strings.TrimSpace(s.Draft); d != "" && (s.State == session.Composing || s.State == session.Delivering) {
		return d
	}
	return strings.TrimSpace(s.Partial)
}

// workingText says what a session does after recording.
func workingText(s session.Snapshot) string {
	switch s.State {
	case session.Reading:
		return i18n.T("Reading the selection…")
	case session.Composing:
		switch {
		case s.Kind == store.KindTranslate && s.Selection != "":
			return i18n.T("Translating the selection…")
		case s.Kind == store.KindTranslate:
			return i18n.T("Translating…")
		case s.Kind == store.KindRewrite && s.Selection != "":
			return i18n.T("Rewriting the selection…")
		case s.Kind == store.KindRewrite:
			return i18n.T("Writing…")
		}
		return i18n.T("Cleaning up…")
	case session.Delivering:
		return i18n.T("Inserting…")
	}
	return i18n.T("Recognizing…")
}

// kindIcon is a feature's icon; kindTitle its name.
func kindIcon(k store.Kind) icons.Icon {
	switch k {
	case store.KindTranslate:
		return translateIcon
	case store.KindRewrite:
		return rewriteIcon
	}
	return transcribeIcon
}

func kindTitle(k store.Kind) string {
	switch k {
	case store.KindTranslate:
		return i18n.T("Translate")
	case store.KindRewrite:
		return i18n.T("Rewrite")
	}
	return i18n.T("Transcribe")
}

// waveform draws the input levels as 16 bars, newest on the right.
func (a *app) waveform(c *ui.Context, levels []float64, heard bool) {
	ui.Row(c.Key("ov-wave")).Grow(1).Height(24).Gap(3).AlignItems(ui.Center).Label(i18n.T("Input level")).Children(func() {
		pad := session.Bars - len(levels)
		for i := range session.Bars {
			v := 0.0
			if i >= pad {
				v = levels[i-pad]
			}
			h := float32(3 + v*21)
			col := ui.RGBA(255, 255, 255, 0.85)
			if !heard {
				col = ui.RGBA(255, 255, 255, 0.3)
			}
			ui.Box(c).Width(3).Height(h).Radius(1.5).Background(col)
		}
	})
}

// clock shows a duration as m:ss.
func clock(d time.Duration) string {
	sec := int(d.Seconds())
	return itoa(sec/60) + ":" + string(rune('0'+sec%60/10)) + string(rune('0'+sec%10))
}

// lastLines is the end of text: its last n lines of about 48 characters.
func lastLines(text string, n int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	const width = 48
	r := []rune(text)
	if len(r) <= width*n {
		return text
	}
	return "…" + strings.TrimSpace(string(r[len(r)-width*n+1:]))
}

// overlayNotice is the notice pill.
func (a *app) overlayNotice(c *ui.Context) {
	t := c.Theme()
	n := a.notice
	if n == nil {
		return
	}
	ui.Row(c).Fill().Padding(0, 10, 0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		ic, col := icons.DangerCircle, tok(t).Danger
		if n.warn {
			ic, col = icons.DangerTriangle, tok(t).Warning
		}
		ui.Icon(c, ic.Bold).Size(18, 18).TextColor(col)
		ui.Text(c, n.text).FontSize(13).TextColor(ovText).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if label := noticeActionLabel(n.action); label != "" {
			if ui.Button(c.Key("ov-notice-action"), label).Clicked() {
				a.noticeAction(n.action)
			}
		}
		if ui.ButtonBase(c.Key("ov-notice-close")).Size(28, 28).Center().Radius(14).Label(i18n.T("Close")).Children(func() {
			ui.Icon(c, icons.Close.Linear).Size(16, 16).TextColor(ovText2)
		}).Clicked() {
			a.closeNotice()
		}
	})
}

// noticeActionLabel is a notice's button.
func noticeActionLabel(action string) string {
	switch action {
	case actionProviders:
		return i18n.T("Open Model services")
	case actionPermissions:
		return i18n.T("Open Permissions")
	case actionHelper:
		return i18n.T("Restart the helper")
	}
	return ""
}

// cardView is what the answer card shows: an ended session's card, or
// the answer the model is writing into it.
type cardView struct {
	kind      store.Kind
	title     string
	text      string
	streaming bool
	failed    *session.Problem
	msg       string // a warning line: why it is in the card, why Insert failed
	lang      string // Translate's target
	selection bool   // it works on a selection
	followUp  bool   // Rewrite: the shortcut follows up
	inserting bool
}

func (a *app) cardNow() cardView {
	s := a.sess
	if s.State.Live() && s.Card && !s.Try {
		v := cardView{kind: s.Kind, title: s.Title, text: s.Draft, streaming: true, lang: s.TargetLanguage, selection: s.Selection != ""}
		if s.Retried {
			v.msg = i18n.T("The first answer was not translated: asking again.")
		}
		return v
	}
	card := a.answer
	v := cardView{kind: card.kind, title: card.title, text: card.text, failed: card.problem, msg: card.msg, lang: card.lang,
		selection: card.input.Selection != "", inserting: card.inserting}
	if v.msg == "" && card.problem == nil {
		v.msg = notInsertedText(a.sess, card)
	}
	v.followUp = card.kind == store.KindRewrite && card.problem == nil && len(card.turns) > 0
	return v
}

// cardTitle is the card's heading: a Rewrite answer's own title, else
// what the card holds.
func (v cardView) cardTitle() string {
	if t := strings.TrimSpace(v.title); t != "" {
		return t
	}
	switch v.kind {
	case store.KindTranslate:
		return i18n.T("Translation")
	case store.KindRewrite:
		if v.selection {
			return i18n.T("Rewritten text")
		}
		return i18n.T("Answer")
	}
	return i18n.T("Transcription")
}

// overlayAnswer is the answer card.
func (a *app) overlayAnswer(c *ui.Context) {
	t := c.Theme()
	v := a.cardNow()
	ui.Column(c).Fill().Padding(16, 18).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, kindIcon(v.kind).Linear).Size(16, 16).TextColor(ovText2)
			ui.Text(c, v.cardTitle()).FontSize(14).Bold().TextColor(ovText).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if v.streaming {
				ui.Spinner(c).Size(14, 14)
			}
			if v.kind == store.KindTranslate {
				a.cardLanguage(c, v)
			}
			if ui.ButtonBase(c.Key("ov-answer-close")).Size(28, 28).Center().Radius(14).Label(i18n.T("Close")).Children(func() {
				ui.Icon(c, icons.Close.Linear).Size(16, 16).TextColor(ovText2)
			}).Clicked() {
				if v.streaming {
					a.cancelSession()
				}
				a.closeAnswer()
			}
		})
		if p := v.failed; p != nil {
			text, more, _ := sessionProblemText(p)
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.DangerCircle.Bold).Size(16, 16).TextColor(tok(t).Danger).Shrink(0)
				ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, text).FontSize(13).TextColor(ovText).MaxLines(2).Ellipsis("…")
					if more != "" {
						ui.Text(c, more).FontSize(12).TextColor(ovText2).MaxLines(3).Ellipsis("…").Selectable()
					}
				})
			})
		} else if v.msg != "" {
			ui.Text(c, v.msg).FontSize(12).TextColor(tok(t).Warning).MaxLines(2).Ellipsis("…")
		}
		ui.Scroll(c.Key("ov-answer-scroll")).Grow(1).MinHeight(0).Children(func() {
			text := v.text
			if v.failed != nil {
				// What it worked on: the instruction, then the selection.
				in := a.answer.input
				if v.kind == store.KindRewrite && in.Raw != "" && in.Selection != "" {
					ui.Text(c, in.Raw).FontSize(13).TextColor(ovText).Selectable().Margin(0, 0, 6, 0)
				}
				text = in.Selection
				if text == "" {
					text = in.Raw
				}
			}
			tx := ui.Text(c, text).FontSize(14).TextColor(ovText).Selectable()
			if v.failed != nil {
				tx.TextColor(ovText2)
			}
			if v.streaming && strings.TrimSpace(text) == "" {
				ui.Text(c, workingText(a.sess)).FontSize(13).TextColor(ovText2)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if v.followUp {
				if keys := a.featureKeys(hotkey.Rewrite); keys != "" {
					ui.Text(c, i18n.T("%s to follow up", keys)).FontSize(12).TextColor(ovText2).SingleLine().Ellipsis("…").MinWidth(0)
				}
			}
			ui.Spacer(c)
			if v.failed != nil {
				if primaryButton(c.Key("ov-answer-retry"), i18n.T("Try again")).Disabled(a.sess.State.Live()).Clicked() {
					a.retryAnswer("")
				}
				return
			}
			if ghostButton(c.Key("ov-answer-copy"), i18n.T("Copy")).Disabled(v.streaming).Clicked() {
				a.copyAnswer(c.Services().WriteClipboard)
			}
			label := i18n.T("Insert")
			if v.inserting {
				label = i18n.T("Inserting…")
			}
			if primaryButton(c.Key("ov-answer-insert"), label).Disabled(v.inserting || v.streaming).Clicked() {
				a.insertAnswer()
			}
		})
	})
}

// cardLanguage is Translate's target in the card: choosing another
// translates the same text again.
func (a *app) cardLanguage(c *ui.Context, v cardView) {
	opts := make([]string, len(compose.TargetLanguages))
	for i, code := range compose.TargetLanguages {
		opts[i] = targetLanguageLabel(code)
	}
	cur := targetLanguageLabel(v.lang)
	if !slices.Contains(opts, cur) {
		opts = append(opts, cur)
	}
	if a.answer.langSel == "" || !slices.Contains(opts, a.answer.langSel) || v.streaming {
		a.answer.langSel = cur
	}
	sel := ui.Select(c.Key("ov-answer-lang"), &a.answer.langSel, opts).Label(i18n.T("Translate into")).Width(190).Shrink(1).MinWidth(120).Disabled(v.streaming || a.sess.State.Live())
	if sel.Changed() {
		if j := slices.Index(opts, a.answer.langSel); j >= 0 && j < len(compose.TargetLanguages) && compose.TargetLanguages[j] != v.lang {
			a.retryAnswer(compose.TargetLanguages[j])
		}
	}
}

// targetLanguageLabel names a target language in the UI's language.
func targetLanguageLabel(code string) string {
	return compose.LanguageName(code, i18n.Locale())
}

// featureKeys are the glyphs of a feature's first shortcut ("" for none).
func (a *app) featureKeys(feature string) string {
	hs := hotkey.HotkeysOf(a.prefs.Features, feature)
	if len(hs) == 0 {
		return ""
	}
	return hotkeyText(hs[0].Keys)
}

// notInsertedText says why the card shows.
func notInsertedText(s session.Snapshot, card answerCard) string {
	if s.ID != card.id {
		return ""
	}
	name := card.target.Name
	if name == "" {
		name = i18n.T("the app")
	}
	switch s.Delivery {
	case deliver.ReasonNotFront, deliver.ReasonNoTarget:
		return i18n.T("%s is not in front any more: insert it there, or copy it.", name)
	case deliver.ReasonNotEditable:
		if s.Selection != "" {
			return i18n.T("The selection in %s cannot be edited: copy the result, or click where it should go and insert.", name)
		}
		return i18n.T("Nothing in %s takes text: click where it should go, then insert.", name)
	case deliver.ReasonLong:
		return i18n.T("A long answer waits here: insert it, or copy it.")
	}
	return ""
}
