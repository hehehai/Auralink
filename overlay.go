package main

// The recording overlay (plan §3.2): Auralink's one floating window,
// redone after Voxt's (10-15): a small pill with no buttons. It is
// non-activating (floating.go, mygo patch 0003): clicking it leaves the
// app the user dictates into in front, with its caret. It always uses the
// dark tokens, whatever the appearance.
//
//	recording  150×48: the feature's icon and a 16-bar waveform; the
//	           recording's time when Overlay.ShowTime says so; the matched
//	           app enhancement group, short ("Team chat"); with live text
//	           (a streaming service's, the answer as the model writes it)
//	           320×96, its last two lines under them
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

// Sizes of the overlay's face, in DIPs.
const (
	ovPillH     = 48
	ovPillPadX  = 16
	ovIconSize  = 16
	ovGap       = 10
	ovWaveW     = 92 // 16 bars of 3.2 with 2.5 between them, and air
	ovWaveH     = 28
	ovPillW     = 2*ovPillPadX + ovIconSize + ovGap + ovWaveW
	ovTimeW     = 8 + 34 // the time, when it shows
	ovSceneW    = 8 + 88 // the group's name, when one matched
	ovLiveW     = 320    // with live text…
	ovLiveH     = 96     // …two lines of it
	ovNoticeW   = 200    // a notice's least width…
	ovNoticeW2  = 420    // …and most
	ovAnswerW   = 560
	ovAnswerH   = 340
	ovLiveLines = 2
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
		// area is the work area of the display it shows on, chosen when
		// it appears (the display under the pointer); a resize stays there.
		area mygo.Rectangle
		// style is the material's shape last given to the window.
		style mygo.VibrancyStyle
		// fade numbers the fades out: showing again, or another fade,
		// makes the steps of an older one do nothing.
		fade int
		// epoch is when the overlay first showed: the waveform's and the
		// loader's motion count from it.
		epoch time.Time
	}
	// laterHook replaces the timers of later in tests (nil: none run
	// without a window).
	laterHook func(time.Duration, func())
}

// later runs fn on the main thread after d: the notice's and the fade's
// timers. Without a window (tests) only laterHook runs them.
func (a *app) later(d time.Duration, fn func()) {
	if a.laterHook != nil {
		a.laterHook(d, fn)
		return
	}
	if a.win == nil {
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

// overlaySize is the face's size for mode.
func (a *app) overlaySize(mode overlayMode) (int, int) {
	switch mode {
	case ovAnswer:
		return ovAnswerW, ovAnswerH
	case ovNotice:
		return noticeWidth(a.notice), ovPillH
	case ovRecording, ovWorking:
		w := ovPillW
		if mode == ovRecording && a.prefs.Overlay.ShowTime {
			w += ovTimeW
		}
		if sceneShort(a.sess) != "" {
			w += ovSceneW
		}
		if liveText(a.sess) != "" {
			return max(w, ovLiveW), ovLiveH
		}
		return w, ovPillH
	}
	return ovPillW, ovPillH
}

// noticeWidth fits a notice's line: its text (as wide as it may be
// guessed without laying it out), the icon, the arrow of an action.
func noticeWidth(n *sessionNotice) int {
	if n == nil {
		return ovNoticeW
	}
	w := 2*ovPillPadX + ovIconSize + ovGap + textWidth(n.text, 13)
	if n.action != actionNone {
		w += 8 + 14
	}
	return min(max(w, ovNoticeW), ovNoticeW2)
}

// textWidth guesses how wide text is at size DIPs: a wide (CJK) letter
// takes the size, any other about 0.56 of it; a little more than less.
func textWidth(text string, size float64) int {
	w := 0.0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) || r >= 0xFF00 && r <= 0xFFEF || r >= 0x3000 && r <= 0x303F:
			w += size
		case r == ' ' || r == 'i' || r == 'l' || r == '.' || r == ',' || r == ':':
			w += 0.32 * size
		default:
			w += 0.58 * size
		}
	}
	return int(math.Ceil(w))
}

// overlaySync shows, resizes or hides the overlay after a change of the
// session, the notice or the answer card. Main thread.
func (a *app) overlaySync() {
	mode := a.overlayModeNow()
	w, h := a.overlaySize(mode)
	was := a.ov.shown
	resized := w != a.ov.w || h != a.ov.h
	a.ov.mode, a.ov.w, a.ov.h = mode, w, h
	if mode == ovHidden {
		a.ov.shown = false
		if was {
			a.overlayFadeOut()
		}
		return
	}
	a.ov.shown = true
	a.ov.fade++ // a fade on its way stops where it is
	a.ov.visible = true
	if a.ov.epoch.IsZero() {
		a.ov.epoch = time.Now()
	}
	if a.win == nil {
		return // tests: the state is enough
	}
	winW, winH := w+2*ovPad, h+2*ovPad
	if a.ov.win == nil {
		opts := floatingWindowOptions("Auralink", winW, winH, ui.View(a.overlayView))
		opts.Vibrancy = mygo.VibrancyHUD
		a.ov.win = mygo.NewWindow(opts)
	}
	if !was || resized {
		if !was {
			a.ov.area = mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
		}
		x, y := overlayPlace(a.ov.area, a.prefs.Overlay, winW, winH)
		if resized {
			a.ov.win.SetContentSize(winW, winH)
		}
		a.syncOverlayMaterial()
		if was {
			a.ov.win.SetPosition(x, y)
		} else {
			a.ov.win.SetOpacity(1)
			showFloating(a.ov.win, x, y)
		}
	}
	a.ov.win.Invalidate()
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
			a.ov.win.SetOpacity(1)
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

// overlayRestyle applies a change of settings.Overlay to the overlay
// while it shows: its place, its size (the time) and its face.
func (a *app) overlayRestyle() {
	if !a.ov.shown || a.ov.win == nil {
		return
	}
	w, h := a.overlaySize(a.ov.mode)
	if w != a.ov.w || h != a.ov.h {
		a.ov.w, a.ov.h = w, h
		a.ov.win.SetContentSize(w+2*ovPad, h+2*ovPad)
	}
	x, y := overlayPlace(a.ov.area, a.prefs.Overlay, a.ov.w+2*ovPad, a.ov.h+2*ovPad)
	a.ov.win.SetPosition(x, y)
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
func (a *app) overlayMotion() bool { return a.win != nil && !a.prefs.Appearance.ReduceMotion }

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
	look := pillLook{time: a.prefs.Overlay.ShowTime, motion: motion, phase: a.overlayPhase()}
	ui.Column(c).Fill().Padding(ovPad).Children(func() {
		o := a.prefs.Overlay
		switch a.ov.mode {
		case ovAnswer:
			faceStyle(ui.Column(c.Key("ov-face")), o, ovAnswer, a.ov.h).DragWindow().Children(func() { a.overlayAnswer(c) })
		case ovNotice:
			a.overlayNotice(c)
		default:
			faceStyle(ui.Column(c.Key("ov-face")), o, a.ov.mode, a.ov.h).DragWindow().Children(func() {
				overlayPill(c, a.sess, a.ov.mode, look)
			})
		}
	})
}

// Colours of the face.
var (
	ovText  = ui.RGB(245, 245, 247)
	ovText2 = ui.RGBA(235, 235, 245, 0.62)
	ovIcon  = ui.RGBA(255, 255, 255, 0.92)
)

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
// seconds.
type pillLook struct {
	time   bool
	motion bool
	phase  float64
}

// overlayPill is the recording and working pill of s in mode: the
// feature's icon, the waveform or the loader, the time and the matched
// group when they show, and the live text under them.
func overlayPill(c *ui.Context, s session.Snapshot, mode overlayMode, look pillLook) {
	text := liveTail(liveText(s))
	ui.Column(c).Fill().Padding(0, ovPillPadX).Children(func() {
		ui.Row(c).Height(ovPillH).Shrink(0).Gap(ovGap).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, kindIcon(s.Kind).Bold).Size(ovIconSize, ovIconSize).TextColor(ovIcon).Shrink(0).Label(kindTitle(s.Kind))
			if mode == ovRecording {
				waveform(c, s.Levels, !s.HeardAt.IsZero(), look)
				if look.time {
					ui.Text(c, clock(s.Elapsed(time.Now()))).FontSize(12).Font("monospace").TextColor(ovText2).Shrink(0).Margin(0, 0, 0, -2)
				}
			} else {
				overlayLoader(c, workingText(s), look)
			}
			if name := sceneShort(s); name != "" {
				ui.Text(c, name).FontSize(11).TextColor(ovText2).SingleLine().Ellipsis("…").MaxWidth(88).MinWidth(0).Shrink(1).
					Label(i18n.T("App enhancement: %s", sceneLabel(s)))
			}
		})
		if text != "" {
			ui.Text(c, text).FontSize(13).LineHeight(1.35).TextColor(ovText).MaxLines(ovLiveLines).Ellipsis("…").Padding(0, 2, 12, 2)
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

// liveTail is the end of the live text that fits the pill's two lines
// (the newest words), after "…" when it is cut.
func liveTail(text string) string {
	return tailFit(text, float64(ovLiveW-2*ovPillPadX-4)*ovLiveLines*0.9, 13)
}

// tailFit is the end of text no wider than width DIPs at size (as
// textWidth guesses), after "…" when it is cut.
func tailFit(text string, width, size float64) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || float64(textWidth(text, size)) <= width {
		return text
	}
	r := []rune(text)
	room := width - size // the "…"
	i := len(r)
	for i > 0 && float64(textWidth(string(r[i-1:]), size)) <= room {
		i--
	}
	// Not in the middle of a (Latin) word: from the next space, if near.
	latin := func(c rune) bool { return c < 0x2E80 && (unicode.IsLetter(c) || unicode.IsDigit(c)) }
	if i > 0 && i < len(r) && latin(r[i-1]) && latin(r[i]) {
		for j := i; j < len(r) && j < i+16; j++ {
			if r[j] == ' ' {
				i = j
				break
			}
		}
	}
	return "…" + strings.TrimSpace(string(r[i:]))
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

// Bars of the waveform (Voxt's proportions).
const (
	waveBarW    = 3.2
	waveBarGap  = 2.5
	waveBarMinH = 3.2
	waveBarMaxH = 22
)

// waveform draws the input levels as 16 rounded bars, newest on the
// right: smoothed (waveBars), and, where it is quiet, breathing a little
// about their least height while look moves. Dim until the first audio.
func waveform(c *ui.Context, levels []float64, heard bool, look pillLook) {
	bars := waveBars(levels)
	ui.Row(c.Key("ov-wave")).Width(ovWaveW).Height(ovWaveH).Shrink(0).Gap(waveBarGap).AlignItems(ui.Center).Justify(ui.Center).
		Label(i18n.T("Input level")).Children(func() {
		for i, v := range bars {
			h := waveBarMinH + (waveBarMaxH-waveBarMinH)*float32(v)
			if look.motion {
				h += float32(waveIdle(i, v, look.phase))
			}
			alpha := float32(0.92)
			if !heard {
				alpha = 0.32
			}
			bar := ui.Box(c).Width(waveBarW).Height(h).Radius(waveBarW / 2).Background(ui.RGBA(255, 255, 255, alpha)).Shrink(0)
			if g := waveGlow(v); g > 0 && heard {
				bar.Shadow(0, 0, 3, 0, ui.RGBA(255, 255, 255, float32(g)))
			}
		}
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

// overlayLoader is the working pill's loader in the waveform's place:
// five dots that swell and brighten in turn (Voxt's: 1.2 s, 0.12 s
// apart); still, all half bright, without motion.
func overlayLoader(c *ui.Context, label string, look pillLook) {
	ui.Row(c.Key("ov-loader")).Width(ovWaveW).Height(ovWaveH).Shrink(0).Gap(4).AlignItems(ui.Center).Justify(ui.Center).
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
		ui.Row(c).Fill().Padding(0, ovPillPadX).Gap(ovGap).AlignItems(ui.Center).Children(func() {
			ic, col := icons.DangerCircle, tok(t).Danger
			if n.warn {
				ic, col = icons.DangerTriangle, tok(t).Warning
			}
			ui.Icon(c, ic.Bold).Size(ovIconSize, ovIconSize).TextColor(col).Shrink(0)
			ui.Text(c, n.text).FontSize(13).TextColor(ovText).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
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
