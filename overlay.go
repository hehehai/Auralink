package main

// The recording overlay (plan §3.2): Auralink's one floating window. It
// is non-activating (floating.go, mygo patch 0003): clicking it leaves
// the app the user dictates into in front, with its caret. It always
// uses the dark tokens, whatever the appearance.
//
//	recording   240×52, 380×120 with live text: the feature's icon, a
//	            16-bar waveform, the time, the last lines of the live
//	            text, Stop and × (cancel; Escape too, through the helper)
//	working     recognising / cleaning up / inserting, with a spinner and ×
//	done        a check for a moment after the text was inserted
//	notice      one line, an action ("Open Model services"), × (4 s)
//	answer      560×340: the result that was not inserted, Insert (ink),
//	            Copy, Close
//
// Its place and look come from settings.Overlay: the top or bottom of
// the display under the pointer, the margin from that edge, the opacity
// of its face and the radius of its corners.

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/icons"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
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
	ovNoticeW   = 380
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
		// done is the session whose "inserted" check shows (until its
		// timer clears it).
		done string
	}
}

// overlayModeNow decides what the overlay shows: the answer card, a live
// session (not a try: the page shows those), a notice, the check after an
// insert, or nothing.
func (a *app) overlayModeNow() overlayMode {
	s := a.sess
	switch {
	case a.answer.open:
		return ovAnswer
	case s.State == session.Recording && !s.Try:
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
		if strings.TrimSpace(a.sess.Partial) != "" {
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
		x, y := overlayPlace(a.prefs.Overlay, winW, winH)
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

// overlayPlace is where a window of w × h goes: centred on the display
// under the pointer, Overlay.Margin from its bottom (or top) edge.
func overlayPlace(o settings.Overlay, w, h int) (int, int) {
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
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
				ui.Icon(c, transcribeIcon.Bold).Size(18, 18).TextColor(tok(t).Record)
				a.waveform(c, s.Levels, !s.HeardAt.IsZero())
				ui.Text(c, clock(s.Elapsed(time.Now()))).FontSize(12).Font("monospace").TextColor(ovText2).Shrink(0)
				if ui.ButtonBase(c.Key("ov-stop")).Size(28, 28).Center().Radius(14).Background(tok(t).Record).Label(i18n.T("Stop")).Children(func() {
					ui.Box(c).Width(10).Height(10).Radius(2).Background(ui.RGB(255, 255, 255))
				}).Clicked() {
					a.stopSession()
				}
			case ovWorking:
				ui.Spinner(c)
				ui.Text(c, workingText(s.State)).FontSize(13).TextColor(ovText).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
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
		if live := lastLines(s.Partial, ovLiveLines); live != "" && a.ov.mode != ovDone {
			ui.Text(c, live).FontSize(13).TextColor(ovText).MaxLines(ovLiveLines).Ellipsis("…").Padding(0, 4, 12, 4)
		}
	})
}

// workingText says what a session does after recording.
func workingText(st session.State) string {
	switch st {
	case session.Composing:
		return i18n.T("Cleaning up…")
	case session.Delivering:
		return i18n.T("Inserting…")
	}
	return i18n.T("Recognizing…")
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

// overlayAnswer is the answer card.
func (a *app) overlayAnswer(c *ui.Context) {
	t := c.Theme()
	card := a.answer
	ui.Column(c).Fill().Padding(16, 18).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, transcribeIcon.Linear).Size(16, 16).TextColor(ovText2)
			ui.Text(c, i18n.T("Transcription")).FontSize(14).Bold().TextColor(ovText).Grow(1)
			if ui.ButtonBase(c.Key("ov-answer-close")).Size(28, 28).Center().Radius(14).Label(i18n.T("Close")).Children(func() {
				ui.Icon(c, icons.Close.Linear).Size(16, 16).TextColor(ovText2)
			}).Clicked() {
				a.closeAnswer()
			}
		})
		msg := card.msg
		if msg == "" {
			msg = notInsertedText(a.sess, card)
		}
		if msg != "" {
			ui.Text(c, msg).FontSize(12).TextColor(tok(t).Warning).MaxLines(2).Ellipsis("…")
		}
		ui.Scroll(c.Key("ov-answer-scroll")).Grow(1).MinHeight(0).Children(func() {
			ui.Text(c, card.text).FontSize(14).TextColor(ovText).Selectable()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if ghostButton(c.Key("ov-answer-copy"), i18n.T("Copy")).Clicked() {
				a.copyAnswer(c.Services().WriteClipboard)
			}
			label := i18n.T("Insert")
			if card.inserting {
				label = i18n.T("Inserting…")
			}
			if primaryButton(c.Key("ov-answer-insert"), label).Disabled(card.inserting).Clicked() {
				a.insertAnswer()
			}
		})
	})
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
		return i18n.T("Nothing in %s takes text: click where it should go, then insert.", name)
	}
	return ""
}
