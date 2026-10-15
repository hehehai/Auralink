package main

// The menu bar icon (plan §2.3; AudioSmith's dictation tray, for
// Auralink): Start transcribing, Paste the last result, Open Auralink,
// Settings, Quit. With Settings → General → "Keep running when the window
// closes", closing the window hides it, so the shortcuts keep working;
// the Dock icon or the menu brings it back. Start transcribing records
// for the app in front (as the shortcut does) and turns into Stop while a
// session runs.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/egoist/mygo"
	"github.com/hehehai/Auralink/internal/i18n"
)

// trayState is the menu bar icon.
type trayState struct {
	tray     *mygo.Tray
	quitting bool // Quit was chosen: closing the window must not hide it
}

// trayIcon is drawn once: five rounded bars of a sound wave, as a
// template image (macOS tints it).
var trayIcon = drawWave(color.NRGBA{0, 0, 0, 255})

// drawWave draws a 36×36 (18 pt @2x) sound wave: five bars, 3 px wide,
// rounded, of heights that rise and fall.
func drawWave(c color.NRGBA) []byte {
	const n = 36
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	heights := []float64{10, 20, 28, 20, 10}
	cover := func(x, y float64) float64 {
		inside := 0.0
		for i, h := range heights {
			cx := 8 + float64(i)*5
			top, bottom := 18-h/2, 18+h/2
			cy := min(max(y, top+1.5), bottom-1.5)
			d := math.Hypot(x-cx, y-cy) - 1.5
			inside = max(inside, clamp01(0.5-d))
		}
		return inside
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if a := cover(float64(x)+0.5, float64(y)+0.5); a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(a * float64(c.A))})
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

// openTray makes the menu bar icon when the settings want one; it runs in
// App.WhenReady.
func (a *app) openTray() { a.applyTray() }

// applyTray shows or removes the icon as Settings → General says.
func (a *app) applyTray() {
	if !a.hasMenu {
		return
	}
	if !a.prefs.General.MenuBar {
		a.closeTray()
		return
	}
	if a.tray != nil {
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, IconIsTemplate: true, ToolTip: "Auralink"})
	if err != nil {
		a.toast(i18n.T("The menu bar icon could not be made: %v", err))
		return
	}
	a.tray = t
	a.buildTrayMenu()
}

func (a *app) closeTray() {
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
}

// buildTrayMenu builds the icon's menu in the current language.
func (a *app) buildTrayMenu() {
	if a.tray == nil {
		return
	}
	act := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.post(fn) }
	}
	f := a.prefs.Features
	start := i18n.T("Start transcribing")
	if a.sess.State.Live() {
		start = i18n.T("Stop transcribing")
	}
	if len(f.Transcribe.Hotkeys) > 0 {
		start += "    " + hotkeyGlyphs(f.Transcribe.Hotkeys[0].Keys)
	}
	a.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: start, Click: act(a.toggleTranscribe)},
		{Label: i18n.T("Paste the last result") + "    " + hotkeyGlyphs(f.PasteLast.Keys), Click: act(func() { a.pasteLast(a.front) })},
		mygo.Separator(),
		{Label: i18n.T("Open Auralink"), Click: act(a.showMainWindow)},
		{Label: i18n.T("Settings…"), Click: act(func() { a.showMainWindow(); a.navigate("/settings") })},
		mygo.Separator(),
		{Label: i18n.T("Quit Auralink"), Click: act(func() { a.quitting = true; mygo.App.Quit() })},
	}))
}

// showMainWindow brings the main window back (it may be hidden in the
// menu bar).
func (a *app) showMainWindow() {
	if a.win != nil {
		a.win.Show()
	}
}

// connectWindowLife keeps Auralink in the menu bar when its window closes
// (the setting), and quits otherwise; it reads the permissions again
// whenever Auralink comes to the front (System Settings may have changed
// them).
func (a *app) connectWindowLife() {
	if a.win == nil {
		return
	}
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if a.prefs.General.CloseToTray && !a.quitting && a.tray != nil {
			e.PreventDefault()
			a.win.Hide()
		}
	})
	a.win.OnClosed(func() { mygo.App.Quit() })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !a.win.IsVisible() {
			a.win.Show()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnDidBecomeActive(func() { a.post(a.recheckPermissions) })
}
