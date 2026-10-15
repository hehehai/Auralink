package main

// The recording overlay (plan §3.2): Auralink's one floating window,
// redone after Voxt's (10-15): a small pill with no buttons. It is
// non-activating (floating.go, mygo patch 0003): clicking it leaves the
// app the user dictates into in front, with its caret. It always uses the
// dark tokens, whatever the appearance.
//
//	recording  148×48 (Voxt's compact card): the feature's icon and a
//	           16-bar waveform; the recording's time when Overlay.ShowTime
//	           says so; the matched app enhancement group, short ("Team
//	           chat"); with live text (a streaming service's, the text
//	           being cleaned up, the answer as the model writes it) 76
//	           high, one line of it under them, the face as wide as the
//	           text (measured) up to 260 and its padding, then the text
//	           scrolled to its newest words, the start fading out at the
//	           left edge (Voxt's)
//	working    the same pill with a five-dot loader in the waveform's
//	           place: recognising, cleaning up, translating, writing,
//	           inserting
//	notice     an error (or a warning) in the waveform's place: an icon and
//	           one line, for 5 s; a click opens where to fix it
//	answer     560×340: the result that was not inserted, Insert (ink),
//	           Copy, Close; it streams while the model writes, shows a
//	           Rewrite answer's title, Translate's target language (to
//	           translate again into another), and a failure with Try
//	           again
//
// Nothing shows after an insert: the pill fades out (100 ms). Every
// action is a shortcut: the feature's shortcut stops, Escape cancels,
// releasing a long press stops.
//
// The face is a translucent dark fill (Overlay.Opacity) over the window's
// HUD material, which blurs what is behind it (mygo patch 0004), with a
// white hairline and a small shadow the window leaves room for (ovPad).
// Its place comes from settings.Overlay: the top or bottom of the display
// under the pointer, and the margin from that edge.

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/Auralink/internal/appctx"
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
	ovNotice
	ovAnswer
)

func (m overlayMode) String() string {
	return [...]string{"hidden", "recording", "working", "notice", "answer"}[m]
}

// The face's shadow: small, and whole. The window adds ovPad around the
// face for it (the window has none: macOS would draw its rectangle's):
// the blur's reach plus the offset, the side it falls on.
const (
	ovShadowY    = 4
	ovShadowBlur = 12
	ovShadowA    = 0.22
	ovPad        = ovShadowBlur + ovShadowY + 2
)

// Sizes of the overlay's face, in DIPs: Voxt's (WaveformView.swift).
const (
	// ovBorder is the face's hairline. MyGo's boxes are border boxes: it
	// takes this much from each side of the face's content, so the
	// paddings inside the face are Voxt's less it. (10-15: they were not,
	// and the pill's row overflowed its content box to the right and
	// down, a point each: the icon's side looked wider than the
	// waveform's.)
	ovBorder = 1
	// The row: the feature's icon (ovIconSize) centred in its slot, ovGap,
	// the waveform (its 16 bars, waveW wide) or the loader centred in its
	// slot; ovRowH high.
	ovIconSlot = 16
	ovIconSize = 14
	ovGap      = 10
	ovWaveW    = 94
	ovRowH     = 28
	ovRowW     = ovIconSlot + ovGap + ovWaveW
	// The pill, without text: the row, ovPillPadX and ovPillPadY around
	// it: 148 × 48.
	ovPillPadX = 14
	ovPillPadY = 10
	ovPillW    = ovRowW + 2*ovPillPadX
	ovPillH    = ovRowH + 2*ovPillPadY
	// With live text: the row, ovLiveGap, one line of the text (ovLiveSize,
	// medium, ovLineH high) as wide as it is up to ovLiveTextW, the two
	// centred, ovLivePadX and ovLivePadY around them: as wide as the wider
	// of the two, at most 300, and 76 high.
	ovLivePadX   = 20
	ovLivePadY   = 12
	ovLiveGap    = 8
	ovLineH      = 16
	ovLiveSize   = 12
	ovLiveWeight = 500
	ovLiveTextW  = 260
	ovLiveMaxW   = ovLiveTextW + 2*ovLivePadX
	ovLiveH      = ovRowH + ovLiveGap + ovLineH + 2*ovLivePadY
	ovFadeW      = 16 // the line's left edge fades out over this, scrolled
	// After the waveform in the row, ovGap before each, when they show:
	// the time, and the matched group's name (ovSceneSize, at most
	// ovSceneMaxW wide).
	ovTimeW     = 30
	ovSceneSize = 11
	ovSceneMaxW = 88
	// A notice: the icon's slot, ovGap, its line (ovNoticeSize), the
	// arrow of an action, in the pill's padding; ovNoticeW to ovNoticeW2
	// wide.
	ovNoticeSize = 13
	ovArrowW     = 8 + 14
	ovNoticeW    = 200
	ovNoticeW2   = 420
	ovAnswerW    = 560
	ovAnswerH    = 340
	// ovFrame paces the waveform and the loader (Voxt's 20 per second);
	// ovFadeStep the fade after an insert (two steps, then hidden).
	ovFrame    = 50 * time.Millisecond
	ovFadeStep = 50 * time.Millisecond
)

// overlayState is the overlay's window and what it shows.
type overlayState struct {
	ov struct {
		win   *mygo.Window
		shown bool // it shows (or should: the tests have no window)
		// visible: the window is on screen; it stays while it fades out.
		visible bool
		mode    overlayMode
		w, h    int // the face's size
		// frame is the window's, in screen DIPs, as last given to it (or
		// as it would be in tests): the face and the shadow's margin
		// around it, its anchor edge (Overlay.Position) Overlay.Margin
		// from the work area's, centred. Hidden, the window keeps it, so
		// the next showing compares with it (10-15: a pill that had grown
		// with live text came back grown and empty in the next session).
		frame mygo.Rectangle
		// live is the session whose live text widened the face, and to
		// what: within it the face only grows (a revised or restarted
		// text does not make it jump back), the next starts as a pill.
		live struct {
			id string
			w  int
		}
		// textW holds the widths of the texts the face's size depends on
		// (the live text, the group's name, a notice's line), as the
		// overlay's view measured them (overlayMeasure); a text not there
		// yet is guessed (textWidth) until it is.
		textW map[ovSized]float32
		// area is the work area of the display it shows on, chosen when
		// it appears (the display under the pointer); a resize stays there.
		area mygo.Rectangle
		// style is the material's shape last given to the window.
		style mygo.VibrancyStyle
		// passThrough: the window lets the pointer through (recording and
		// working: nothing in it takes a click).
		passThrough bool
		// reveal: the window was just shown transparent; the next build of
		// its view (the new face's) makes it opaque, so that no frame of
		// the last face shows.
		reveal bool
		// fade numbers the fades out: showing again, or another fade,
		// makes the steps of an older one do nothing.
		fade int
		// epoch is when the overlay first showed: the waveform's and the
		// loader's motion count from it.
		epoch time.Time
		// native: the overlay makes its window without the main one (the
		// overlay check, overlay_check.go).
		native bool
	}
	// laterHook replaces the timers of later in tests (nil: none run
	// without a window).
	laterHook func(time.Duration, func())
	// measureHook measures the face's texts in tests, which build no view
	// (nil: the view measures them, overlayMeasure).
	measureHook func(ovSized) float32
}

// overlayNative says whether the overlay has a real window: in the app,
// not in tests.
func (a *app) overlayNative() bool { return a.win != nil || a.ov.native }

// later runs fn on the main thread after d: the notice's and the fade's
// timers. Without a window (tests) only laterHook runs them.
func (a *app) later(d time.Duration, fn func()) {
	if a.laterHook != nil {
		a.laterHook(d, fn)
		return
	}
	if !a.overlayNative() {
		return
	}
	time.AfterFunc(d, func() { a.post(fn) })
}

// overlayModeNow decides what the overlay shows: the answer card (an
// ended session's, or one the model writes into), a live session (not a
// try: the page shows those), a notice, or nothing. A session that ended
// shows nothing of its own: not the insert, which the text in place says.
func (a *app) overlayModeNow() overlayMode {
	s := a.sess
	live := s.State.Live() && !s.Try
	switch {
	case a.answer.open && !live:
		return ovAnswer
	case live && s.Card && s.State == session.Composing:
		return ovAnswer
	case live && s.State == session.Recording:
		return ovRecording
	case live:
		return ovWorking
	case a.notice != nil:
		return ovNotice
	}
	return ovHidden
}

// overlaySize is the face's size for mode, and whether its width is the
// measured texts' (not guessed).
func (a *app) overlaySize(mode overlayMode) (w, h int, exact bool) {
	switch mode {
	case ovAnswer:
		return ovAnswerW, ovAnswerH, true
	case ovNotice:
		if a.notice == nil {
			return ovNoticeW, ovPillH, true
		}
		tw, exact := a.ovTextWidth(noticeText(a.notice.text))
		return noticeWidth(tw, a.notice.action != actionNone), ovPillH, exact
	case ovRecording, ovWorking:
		scene, exact := float32(0), true
		if name := sceneShort(a.sess); name != "" {
			scene, exact = a.ovTextWidth(sceneText(name))
		}
		row := rowWidth(mode == ovRecording && a.prefs.Overlay.ShowTime, scene)
		text := liveLineText(a.sess)
		if text == "" {
			return row + 2*ovPillPadX, ovPillH, exact
		}
		tw, known := a.ovTextWidth(liveTextKey(text))
		w := liveWidth(row, tw)
		if a.ov.live.id == a.sess.ID {
			// Within a session the face only grows: a revised text, or the
			// model's draft after the speech, does not make it jump back.
			w = max(w, a.ov.live.w)
		}
		return w, ovLiveH, exact && known
	}
	return ovPillW, ovPillH, true
}

// rowWidth is the width of the pill's row: the icon and the waveform, the
// time when it shows, the group's name when one matched (scene DIPs
// wide, at most ovSceneMaxW; 0 for none).
func rowWidth(showTime bool, scene float32) int {
	w := ovRowW
	if showTime {
		w += ovGap + ovTimeW
	}
	if scene > 0 {
		w += ovGap + int(math.Ceil(float64(min(scene, ovSceneMaxW))))
	}
	return w
}

// liveWidth is the face's width with live text text DIPs wide under a
// row row DIPs wide: the wider of the two, the text at most
// ovLiveTextW, and the padding (Voxt's).
func liveWidth(row int, text float32) int {
	line := int(math.Ceil(float64(min(text, ovLiveTextW))))
	return max(row, line) + 2*ovLivePadX
}

// noticeWidth fits a notice's line, text DIPs wide: the pill's padding,
// the icon, the text, the arrow of an action; at least ovNoticeW, at most
// ovNoticeW2.
func noticeWidth(text float32, action bool) int {
	w := 2*ovPillPadX + ovIconSlot + ovGap + int(math.Ceil(float64(text)))
	if action {
		w += ovArrowW
	}
	return min(max(w, ovNoticeW), ovNoticeW2)
}

// ovSized is a text the face's size depends on, as it draws.
type ovSized struct {
	text   string
	size   float32
	weight int
}

func (k ovSized) span() ui.Span {
	return ui.Span{Text: k.text, Size: k.size, Weight: k.weight}
}

func liveTextKey(text string) ovSized { return ovSized{text, ovLiveSize, ovLiveWeight} }
func sceneText(name string) ovSized   { return ovSized{name, ovSceneSize, 0} }
func noticeText(text string) ovSized  { return ovSized{text, ovNoticeSize, 0} }

// ovTextWidth is how wide k draws: as measured, or guessed (false)
// until the overlay's view measured it.
func (a *app) ovTextWidth(k ovSized) (float32, bool) {
	if a.measureHook != nil {
		return a.measureHook(k), true
	}
	if w, ok := a.ov.textW[k]; ok {
		return w, true
	}
	return float32(textWidth(k.text, float64(k.size))), false
}

// overlayTexts are the texts the face's size depends on in its mode.
func (a *app) overlayTexts() []ovSized {
	var out []ovSized
	switch a.ov.mode {
	case ovNotice:
		if a.notice != nil {
			out = append(out, noticeText(a.notice.text))
		}
	case ovRecording, ovWorking:
		if name := sceneShort(a.sess); name != "" {
			out = append(out, sceneText(name))
		}
		if text := liveLineText(a.sess); text != "" {
			out = append(out, liveTextKey(text))
		}
	}
	return out
}

// overlayMeasure measures, in the overlay's build, the texts the face's
// size was guessed from; the next turn of the main loop keeps their
// widths and fits the face to them (the build pass resizes nothing).
// A window that shows again stays transparent until then: the fitting
// is posted before the reveal.
func (a *app) overlayMeasure(c *ui.Context) {
	var keys []ovSized
	var widths []float32
	for _, k := range a.overlayTexts() {
		if _, ok := a.ov.textW[k]; ok {
			continue
		}
		w, _ := c.MeasureText(0, k.span())
		keys, widths = append(keys, k), append(widths, w)
	}
	if len(keys) == 0 {
		return
	}
	a.post(func() {
		if a.ov.textW == nil || len(a.ov.textW) > 256 {
			a.ov.textW = map[ovSized]float32{}
		}
		for i, k := range keys {
			a.ov.textW[k] = widths[i]
		}
		a.overlayRestyle()
	})
}

// textWidth guesses how wide text is at size DIPs: a wide (CJK) letter
// takes the size, any other about 0.56 of it; a little more than less.
func textWidth(text string, size float64) int {
	w := 0.0
	for _, r := range text {
		switch {
		case wideRune(r):
			w += size
		case r == ' ' || r == 'i' || r == 'l' || r == '.' || r == ',' || r == ':':
			w += 0.32 * size
		default:
			w += 0.58 * size
		}
	}
	return int(math.Ceil(w))
}

// wideRune says whether r is a wide (CJK) letter: as wide as the font
// is high.
func wideRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0xFF00 && r <= 0xFFEF || r >= 0x3000 && r <= 0x303F
}

// ovTestArea is the work area the overlay takes without a display
// (tests): a 1440 × 900 one under a menu bar.
var ovTestArea = mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 875}

// overlayArea is the work area of the display under the pointer.
func (a *app) overlayArea() mygo.Rectangle {
	if !a.overlayNative() {
		return ovTestArea
	}
	return mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
}

// overlaySync shows, resizes or hides the overlay after a change of the
// session, the notice or the answer card. Main thread.
//
// The window is as large as the face (and the shadow's margin), so that
// the desktop around it takes clicks; it grows and shrinks about its
// anchor (the bottom edge and the centre, or the top edge), the size and
// the place set in one move: the material (patch 0004) follows it in the
// same Core Animation transaction, and MyGo draws the new size's frame
// before it commits (darwin/surface.go). The view state is the new face's
// before the window changes, so that frame is the new face. A window that
// shows again is first transparent, until its view built the new face
// (reveal): no frame of the last face shows.
func (a *app) overlaySync() {
	mode := a.overlayModeNow()
	was := a.ov.shown
	if mode == ovHidden {
		// The face stays as it was while it fades; the next showing sizes
		// it again.
		a.ov.mode, a.ov.shown = mode, false
		a.ov.live.id, a.ov.live.w = "", 0
		if was {
			a.overlayFadeOut()
		}
		return
	}
	w, h := a.overlayFace(mode)
	if !was {
		a.ov.area = a.overlayArea()
	}
	frame := overlayFrame(a.ov.area, a.prefs.Overlay, w, h)
	moved := frame != a.ov.frame
	a.ov.mode, a.ov.w, a.ov.h, a.ov.frame = mode, w, h, frame
	a.ov.shown = true
	a.ov.fade++ // a fade on its way stops where it is
	a.ov.visible = true
	if a.ov.epoch.IsZero() {
		a.ov.epoch = time.Now()
	}
	pass := mode == ovRecording || mode == ovWorking
	if !a.overlayNative() {
		a.ov.passThrough = pass
		return // tests: the state is enough
	}
	if a.ov.win == nil {
		opts := floatingWindowOptions("Auralink", frame.Width, frame.Height, ui.View(a.overlayView))
		opts.Vibrancy = mygo.VibrancyHUD
		a.ov.win = mygo.NewWindow(opts)
		a.ov.passThrough = false
	}
	if pass != a.ov.passThrough {
		a.ov.passThrough = pass
		a.ov.win.SetIgnoreMouseEvents(pass)
	}
	if !was {
		a.ov.win.SetOpacity(0)
		a.ov.reveal = true
		seq := a.ov.fade
		a.later(ovRevealMax, func() { // whatever the view does
			if a.ov.fade == seq && a.ov.shown {
				a.revealOverlay()
			}
		})
	}
	if moved {
		a.ov.win.SetContentBounds(frame)
	}
	a.syncOverlayMaterial()
	a.ov.win.Invalidate()
	if !was {
		showFloating(a.ov.win, frame.X, frame.Y)
	}
}

// ovRevealMax is how long a window that shows again may wait for its view
// to build before it turns opaque anyway.
const ovRevealMax = 120 * time.Millisecond

// revealOverlay makes the window opaque once it shows the new face.
func (a *app) revealOverlay() {
	a.ov.reveal = false
	if a.ov.win != nil && a.ov.shown {
		a.ov.win.SetOpacity(1)
	}
}

// overlayFrame is the window's frame for a face of w × h in the work area
// wa: centred, its anchor edge (the bottom, or the top) Overlay.Margin
// from the work area's, the shadow's margin around the face.
func overlayFrame(wa mygo.Rectangle, o settings.Overlay, w, h int) mygo.Rectangle {
	winW, winH := w+2*ovPad, h+2*ovPad
	x, y := overlayPlace(wa, o, winW, winH)
	return mygo.Rectangle{X: x, Y: y, Width: winW, Height: winH}
}

// syncOverlayMaterial shapes the window's material to the face: inside
// the shadow's margin, with the face's corners, dark.
func (a *app) syncOverlayMaterial() {
	if a.ov.win == nil {
		return
	}
	style := mygo.VibrancyStyle{Top: ovPad, Right: ovPad, Bottom: ovPad, Left: ovPad,
		Radius: float64(faceRadius(a.prefs.Overlay, a.ov.mode, a.ov.h)), Dark: true}
	if style != a.ov.style {
		a.ov.style = style
		a.ov.win.SetVibrancyStyle(style)
	}
}

// overlayFadeOut hides the overlay: at once with reduced motion, else
// after a short fade. Showing it again meanwhile stops the fade.
func (a *app) overlayFadeOut() {
	a.ov.fade++
	seq := a.ov.fade
	current := func() bool { return a.ov.fade == seq && !a.ov.shown }
	hide := func() {
		if !current() {
			return
		}
		a.ov.visible = false
		if a.ov.win != nil {
			a.ov.win.Hide()
		}
	}
	if a.prefs.Appearance.ReduceMotion {
		hide()
		return
	}
	dim := func(v float64) func() {
		return func() {
			if current() && a.ov.win != nil {
				a.ov.win.SetOpacity(v)
			}
		}
	}
	dim(0.6)()
	a.later(ovFadeStep, dim(0.25))
	a.later(2*ovFadeStep, hide)
}

// overlayPlace is where a window of w × h goes in the work area wa:
// centred, Overlay.Margin from its bottom (or top) edge to the face.
func overlayPlace(wa mygo.Rectangle, o settings.Overlay, w, h int) (int, int) {
	margin := int(math.Round(o.Margin)) - ovPad
	x := wa.X + (wa.Width-w)/2
	y := wa.Y + wa.Height - h - margin
	if o.Position == "top" {
		y = wa.Y + margin
	}
	return x, y
}

// overlayFace is the face's size in mode, kept as the session's live
// width when it is a measured one with live text (a guess may be wider
// than the text: the face would never fit it).
func (a *app) overlayFace(mode overlayMode) (int, int) {
	w, h, exact := a.overlaySize(mode)
	switch {
	case h == ovLiveH && mode != ovAnswer:
		if a.ov.live.id != a.sess.ID {
			a.ov.live.id, a.ov.live.w = a.sess.ID, 0
		}
		if exact {
			a.ov.live.w = max(a.ov.live.w, w)
		}
	case a.ov.live.id != a.sess.ID:
		a.ov.live.id, a.ov.live.w = "", 0
	}
	return w, h
}

// overlayRestyle applies a change of settings.Overlay, or of the measured
// texts, to the overlay while it shows: its place, its size (the time,
// the texts) and its face.
func (a *app) overlayRestyle() {
	if !a.ov.shown {
		return
	}
	w, h := a.overlayFace(a.ov.mode)
	frame := overlayFrame(a.ov.area, a.prefs.Overlay, w, h)
	moved := frame != a.ov.frame
	a.ov.w, a.ov.h, a.ov.frame = w, h, frame
	if a.ov.win == nil {
		return
	}
	if moved {
		a.ov.win.SetContentBounds(frame)
	}
	a.syncOverlayMaterial()
	a.ov.win.Invalidate()
}

// closeOverlay destroys the window (quit).
func (a *app) closeOverlay() {
	if a.ov.win != nil {
		a.ov.win.Destroy()
		a.ov.win = nil
	}
	a.ov.shown, a.ov.visible = false, false
}

// overlayTheme is the overlay's theme: the dark tokens, always.
func overlayTheme() *ui.Theme {
	th := *ui.DarkTheme()
	styleTheme(&th)
	return &th
}

// overlayMotion says whether the waveform and the loader move.
func (a *app) overlayMotion() bool { return a.overlayNative() && !a.prefs.Appearance.ReduceMotion }

// overlayPhase is the time the motion is at, in seconds; 0 when nothing
// moves (and in tests).
func (a *app) overlayPhase() float64 {
	if !a.overlayMotion() || a.ov.epoch.IsZero() {
		return 0
	}
	return time.Since(a.ov.epoch).Seconds()
}

// overlayView is the overlay window's content.
func (a *app) overlayView(c *ui.Context) {
	c.SetTheme(overlayTheme())
	// The root draws the theme's background unless told not to: in this
	// transparent window only the face may show. Last, once the view is
	// built: the kit's ink buttons swap the theme while they build
	// (AudioSmith's pill).
	defer c.Root().Background(ui.Transparent)
	a.overlayMeasure(c)
	if a.ov.reveal {
		// This frame draws the new face: opaque from the next one.
		a.ov.reveal = false
		a.post(a.revealOverlay)
	}
	motion := a.overlayMotion()
	if !motion {
		c.DisableAnimations()
	}
	switch {
	case motion && (a.ov.mode == ovRecording || a.ov.mode == ovWorking):
		c.After(ovFrame)
	case a.ov.mode == ovRecording && a.prefs.Overlay.ShowTime:
		c.After(250 * time.Millisecond) // the time
	}
	look := pillLook{time: a.prefs.Overlay.ShowTime, motion: motion, phase: a.overlayPhase(), w: a.ov.w}
	ui.Column(c).Fill().Padding(ovPad).Children(func() {
		o := a.prefs.Overlay
		switch a.ov.mode {
		case ovAnswer:
			faceStyle(ui.Column(c.Key("ov-face")), o, ovAnswer, a.ov.h).DragWindow().Children(func() { a.overlayAnswer(c) })
		case ovNotice:
			a.overlayNotice(c)
		default:
			// The pointer goes through it (passThrough): nothing to drag.
			faceStyle(ui.Column(c.Key("ov-face")), o, a.ov.mode, a.ov.h).Children(func() {
				overlayPill(c, a.sess, a.ov.mode, look)
			})
		}
	})
}

// Colours of the face; the live text's is Voxt's: white, 85 %.
var (
	ovText      = ui.RGB(245, 245, 247)
	ovText2     = ui.RGBA(235, 235, 245, 0.62)
	ovIcon      = ui.RGBA(255, 255, 255, 0.92)
	ovLiveColor = ui.RGBA(255, 255, 255, ovLiveAlpha)
)

const ovLiveAlpha = 0.85

// faceRadius is the face's corner radius in mode, h DIPs high: the
// setting's, at most a capsule for the pill.
func faceRadius(o settings.Overlay, mode overlayMode, h int) float32 {
	radius := float32(o.Radius)
	if mode != ovAnswer {
		radius = min(radius, float32(h)/2)
	}
	return radius
}

// faceAlpha is how opaque the face's fill is (Overlay.Opacity).
func faceAlpha(o settings.Overlay) float32 {
	return float32(min(max(o.Opacity, 0.5), 1))
}

// faceStyle gives e, the face, its look in mode, h DIPs high: the radius,
// the translucent fill, the hairline and the shadow. Give it children.
func faceStyle(e ui.Element, o settings.Overlay, mode overlayMode, h int) ui.Element {
	return e.Fill().Radius(faceRadius(o, mode, h)).Background(ui.RGBA(22, 22, 26, faceAlpha(o))).
		Border(1, ui.RGBA(255, 255, 255, 0.12)).Shadow(0, ovShadowY, ovShadowBlur, 0, ui.RGBA(0, 0, 0, ovShadowA))
}

// pillLook is how the pill draws: with the time, moving, at phase
// seconds, w DIPs wide.
type pillLook struct {
	time   bool
	motion bool
	phase  float64
	w      int
}

// overlayPill is the recording and working pill of s in mode, look.w
// wide: the feature's icon, the waveform or the loader, the time and the
// matched group when they show, in a row centred in the pill (Voxt's
// compact card); with live text the row and the text's line under it,
// both centred, in the wider padding of Voxt's card with text.
func overlayPill(c *ui.Context, s session.Snapshot, mode overlayMode, look pillLook) {
	text := liveLineText(s)
	// The face's border takes ovBorder of its padding (MyGo's border box).
	col := ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Padding(ovPillPadY-ovBorder, ovPillPadX-ovBorder)
	if text != "" {
		col.Padding(ovLivePadY-ovBorder, ovLivePadX-ovBorder).Gap(ovLiveGap)
	}
	col.Children(func() {
		ui.Row(c).Height(ovRowH).Shrink(0).Gap(ovGap).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(ovIconSlot, ovRowH).Shrink(0).Center().Children(func() {
				ui.Icon(c, kindIcon(s.Kind).Bold).Size(ovIconSize, ovIconSize).TextColor(ovIcon).Shrink(0).Label(kindTitle(s.Kind))
			})
			if mode == ovRecording {
				waveform(c, s.Levels, !s.HeardAt.IsZero(), look)
				if look.time {
					ui.Box(c).Size(ovTimeW, ovRowH).Shrink(0).Center().Children(func() {
						ui.Text(c, clock(s.Elapsed(time.Now()))).FontSize(12).Font("monospace").TextColor(ovText2).SingleLine().Shrink(0)
					})
				}
			} else {
				overlayLoader(c, workingText(s), look)
			}
			if name := sceneShort(s); name != "" {
				ui.Text(c, name).FontSize(ovSceneSize).TextColor(ovText2).SingleLine().Ellipsis("…").MaxWidth(ovSceneMaxW).MinWidth(0).Shrink(1).
					Label(i18n.T("App enhancement: %s", sceneLabel(s)))
			}
		})
		if text != "" {
			liveLine(c.Key("ov-live-"+s.ID), text, float32(max(look.w, ovPillW)-2*ovLivePadX), look.motion)
		}
	})
}

// liveLineText is the live text as its line shows it: its spaces and
// line breaks one space each.
func liveLineText(s session.Snapshot) string {
	return strings.Join(strings.Fields(liveText(s)), " ")
}

// liveLine is the live text on one line, width DIPs wide: centred while
// it fits, else scrolled so that its newest words show at the right
// edge, sliding left as more come (180 ms, Voxt's 0.2 s ease-out), the
// letters at the left edge fading out to say that more came before. The
// key is the session's: a new one starts where its text is, not sliding.
func liveLine(c *ui.Context, text string, width float32, motion bool) {
	measure := func(t string) float32 { w, _ := c.MeasureText(0, liveTextKey(t).span()); return w }
	box := ui.Row(c).Width(width).Height(ovLineH).Shrink(0).AlignItems(ui.Center).Clip().Label(text)
	// Centred under the row while it fits; else its end at the right edge.
	target := width - measure(text)
	if target > 0 {
		target /= 2
	}
	x := target
	if motion {
		x = box.AnimateTo("x", target, ui.Animation{Duration: 180 * time.Millisecond, Ease: ui.EaseOut})
	}
	shown, at, spans := text, x, []ui.Span{{Text: text, Size: ovLiveSize, Weight: ovLiveWeight, Color: ovLiveColor}}
	if x < 0 {
		shown, at, spans = fadeSpans(text, -x, measure)
		at = x + at
	}
	box.Children(func() {
		ui.RichText(c, spans...).NoWrap().Shrink(0).FixedLineHeight(ovLineH).Translate(at, 0).Label(shown)
	})
}

// fadeSpans are the part of text that shows when it is scrolled off DIPs
// to the left, measure telling how wide a part of it is: the letters past
// the edge, where the shown part starts (from the text's start), and its
// spans: the letters in the ovFadeW after the edge fading in from it, the
// rest as ovText.
func fadeSpans(text string, off float32, measure func(string) float32) (string, float32, []ui.Span) {
	r := []rune(text)
	// The first letter that ends past the edge.
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi) / 2
		if measure(string(r[:mid+1])) <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	from := measure(string(r[:lo]))
	var spans []ui.Span
	i, start := lo, from-off
	for ; i < len(r) && start < ovFadeW; i++ {
		end := measure(string(r[:i+1])) - off
		mid := max(0, (start+end)/2)
		a := min(mid/ovFadeW, 1)
		spans = append(spans, ui.Span{Text: string(r[i]), Size: ovLiveSize, Weight: ovLiveWeight, Color: ui.RGBA(255, 255, 255, ovLiveAlpha*(0.1+0.9*a))})
		start = end
	}
	if i < len(r) {
		spans = append(spans, ui.Span{Text: string(r[i:]), Size: ovLiveSize, Weight: ovLiveWeight, Color: ovLiveColor})
	}
	return string(r[lo:]), from, spans
}

// liveText is the text the pill shows under it: the answer as the model
// writes it, else what the speech service heard so far (a file service's
// whole text, while it is cleaned up).
func liveText(s session.Snapshot) string {
	if d := strings.TrimSpace(s.Draft); d != "" && (s.State == session.Composing || s.State == session.Delivering) {
		return d
	}
	return strings.TrimSpace(s.Partial)
}

// sceneLabel is the app enhancement group a session falls in, as the
// history and the pill's description say it: "Slack · Team chat"; ""
// for none.
func sceneLabel(s session.Snapshot) string {
	if s.Scene == nil || !s.Scene.Matched() {
		return ""
	}
	app := s.Target.Name
	if s.Scene.Source == appctx.SourceURL && s.Scene.Host != "" {
		app = s.Scene.Host // a website rule: the site, not the browser
	}
	if app == "" {
		return s.Scene.GroupName()
	}
	return app + " · " + s.Scene.GroupName()
}

// sceneShort is the group's name alone, as the pill shows it: the user
// knows which app they are in.
func sceneShort(s session.Snapshot) string {
	if s.Scene == nil || !s.Scene.Matched() {
		return ""
	}
	return s.Scene.GroupName()
}

// workingText says what a session does after recording (the loader's
// description).
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

// Bars of the waveform (Voxt's): waveW wide in all, centred in the
// waveform's slot.
const (
	waveBarW    = 3.2
	waveBarGap  = 2.5
	waveBarR    = 1.5
	waveBarMinH = 3.2
	waveBarMaxH = 22
	waveW       = session.Bars*waveBarW + (session.Bars-1)*waveBarGap // 88.7
)

// waveform draws the input levels as 16 rounded bars, newest on the
// right, in the waveform's slot (ovWaveW, ovRowH), centred (Voxt's):
// smoothed (waveBars), and, where it is quiet, breathing a little about
// their least height while look moves. Dim until the first audio.
func waveform(c *ui.Context, levels []float64, heard bool, look pillLook) {
	bars := waveBars(levels)
	ui.Row(c.Key("ov-wave")).Width(ovWaveW).Height(ovRowH).Shrink(0).Center().Children(func() {
		ui.Row(c).Width(waveW).Height(ovRowH).Shrink(0).Gap(waveBarGap).AlignItems(ui.Center).Label(i18n.T("Input level")).Children(func() {
			for i, v := range bars {
				h := waveBarMinH + (waveBarMaxH-waveBarMinH)*float32(v)
				if look.motion {
					h += float32(waveIdle(i, v, look.phase))
				}
				// Voxt's fill: white, 98 % at the top to 80 % at the bottom.
				top, bottom := float32(0.98), float32(0.8)
				if !heard {
					top, bottom = 0.34, 0.28
				}
				bar := ui.Box(c).Width(waveBarW).Height(h).Radius(waveBarR).Gradient(ui.RGBA(255, 255, 255, top), ui.RGBA(255, 255, 255, bottom), 180).Shrink(0)
				if g := waveGlow(v); g > 0 && heard {
					bar.Shadow(0, 0, 3, 0, ui.RGBA(255, 255, 255, float32(g)))
				}
			}
		})
	})
}

// waveBars turns the last levels (session.Level, oldest first, about 20
// a second) into the bars' heights, 0–1: each level lifted so that quiet
// speech shows and room noise does not, then followed quickly as it rises
// and slowly as it falls (Voxt's waveform: rise 0.82, fall 0.24 a frame),
// so the bars swell with a word and ebb after it. Missing levels (the
// recording's first second) are 0.
func waveBars(levels []float64) [session.Bars]float64 {
	var out [session.Bars]float64
	pad := session.Bars - len(levels)
	if pad < 0 {
		levels, pad = levels[-pad:], 0
	}
	s := 0.0
	for i := range session.Bars {
		x := 0.0
		if i >= pad {
			x = waveLift(levels[i-pad])
		}
		if x >= s {
			s += (x - s) * 0.82
		} else {
			s += (x - s) * 0.24
		}
		out[i] = min(max(s, 0), 1)
	}
	return out
}

// waveLift maps a level (0 at −60 dBFS, 1 at 0 dBFS) to a bar: below
// −48 dBFS (a room) nothing, full at −12 dBFS, eased so that speech at
// −35 dBFS still stands up.
func waveLift(v float64) float64 {
	g := min(max((v-0.2)/0.6, 0), 1)
	return math.Pow(g, 0.8)
}

// waveIdle is a quiet bar's breath: up to 1.6 DIPs, a slow wave along
// the bars (1.6 s), gone as the bar rises.
func waveIdle(i int, v, phase float64) float64 {
	quiet := max(0, 1-v*8)
	return quiet * 1.6 * (0.5 + 0.5*math.Sin(2*math.Pi*phase/1.6+float64(i)*0.55))
}

// waveGlow is a loud bar's halo, as Voxt's: none when quiet.
func waveGlow(v float64) float64 {
	a := max(0, v-0.05)
	return min(0.24, 0.26*a)
}

// overlayLoader is the working pill's loader in the waveform's place,
// centred in its slot: five dots of 5, 4 apart, that swell and brighten
// in turn (Voxt's: 1.2 s, 0.12 s apart); still, all half bright, without
// motion.
func overlayLoader(c *ui.Context, label string, look pillLook) {
	ui.Row(c.Key("ov-loader")).Width(ovWaveW).Height(ovRowH).Shrink(0).Gap(4).AlignItems(ui.Center).Justify(ui.Center).
		Label(label).Children(func() {
		for i := range 5 {
			scale, alpha := 1.0, 0.7
			if look.motion {
				scale, alpha = loaderDot(look.phase - float64(i)*0.12)
			}
			d := float32(5 * scale)
			ui.Box(c).Size(d, d).Radius(d / 2).Background(ui.RGBA(255, 255, 255, float32(alpha))).Shrink(0)
		}
	})
}

// loaderDot is a loader dot's scale and opacity at t seconds: it swells
// and brightens over the first 30 % of a 1.2 s cycle, shrinks and fades
// over the next 30 %, and rests.
func loaderDot(t float64) (scale, alpha float64) {
	p := math.Mod(t, 1.2) / 1.2
	if p < 0 {
		p++
	}
	switch {
	case p < 0.3:
		u := p / 0.3
		return 0.6 + 0.7*u, 0.15 + 0.85*u
	case p < 0.6:
		u := (p - 0.3) / 0.3
		return 1.3 - 0.7*u, 1 - 0.85*u
	}
	return 0.6, 0.15
}

// clock shows a duration as m:ss.
func clock(d time.Duration) string {
	sec := int(d.Seconds())
	return itoa(sec/60) + ":" + string(rune('0'+sec%60/10)) + string(rune('0'+sec%10))
}

// overlayNotice is a notice: the pill with an icon and one line in place
// of the waveform; with an action, a click on it runs the action.
func (a *app) overlayNotice(c *ui.Context) {
	t := c.Theme()
	n := a.notice
	if n == nil {
		return
	}
	label := noticeActionLabel(n.action)
	content := func() {
		// Centred, as the pill's row: a notice narrower than ovNoticeW
		// keeps its sides even. The border takes ovBorder of the padding.
		ui.Row(c).Fill().Padding(0, ovPillPadX-ovBorder).Gap(ovGap).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ic, col := icons.DangerCircle, tok(t).Danger
			if n.warn {
				ic, col = icons.DangerTriangle, tok(t).Warning
			}
			ui.Box(c).Size(ovIconSlot, ovRowH).Shrink(0).Center().Children(func() {
				ui.Icon(c, ic.Bold).Size(ovIconSize, ovIconSize).TextColor(col).Shrink(0)
			})
			ui.Text(c, n.text).FontSize(ovNoticeSize).TextColor(ovText).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			if label != "" {
				ui.Icon(c, icons.AltArrowRight.Linear).Size(14, 14).TextColor(ovText2).Shrink(0).Margin(0, 0, 0, -2)
			}
		})
	}
	o := a.prefs.Overlay
	if label == "" {
		faceStyle(ui.Column(c.Key("ov-face")), o, ovNotice, a.ov.h).DragWindow().Label(n.text).Children(content)
		return
	}
	b := faceStyle(ui.ButtonBase(c.Key("ov-notice")), o, ovNotice, a.ov.h).Label(n.text + " · " + label)
	if b.Hovered() {
		b.Background(ui.RGBA(34, 34, 40, min(faceAlpha(o)+0.06, 1)))
	}
	if b.Children(content).Clicked() {
		a.noticeAction(n.action)
	}
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
