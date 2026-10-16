package main

// The menu bar icon (plan §2.3; AudioSmith's dictation tray, for
// Auralink): Start transcribing, Paste the last result, the last three
// results (a click copies one), Open Auralink, Settings, Quit. With
// Settings → General → "Keep running when the window closes", closing
// the window hides it, so the shortcuts keep working; the Dock icon or
// the menu brings it back. Start transcribing records for the app in
// front (as the shortcut does) and turns into Stop while a session runs;
// the icon (Voxt's microphone) shows a recording dot meanwhile.

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/store"
	"github.com/hehehai/Auralink/internal/trayicon"
)

// trayState is the menu bar icon.
type trayState struct {
	tray     *mygo.Tray
	trayLive bool     // the icon shows the recording one
	quitting bool     // Quit was chosen: closing the window must not hide it
	recent   []string // the last results, newest first (trayRecentN at most)
	// copyText puts text on the clipboard (mygo.Clipboard); tests record.
	copyText func(string) error
}

// trayRecentN is how many results the menu lists; trayRecentWidth how
// wide each shows, in display units (a CJK letter 2, any other 1), the
// "…" included: about as wide as "Paste the last result ⌃⌘V", so a long
// result does not widen the menu (10-15: whole results made it as wide
// as the screen); trayTipRunes how much of it the tooltip shows.
const (
	trayRecentN     = 3
	trayRecentWidth = 26
	trayTipRunes    = 400
)

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
	live := a.sess.State.Live()
	// Voxt's microphone, a 36 px template image MyGo shows 18 pt tall
	// (Voxt: 18×18 pt, template); while recording it has a dot.
	t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayicon.Icon(live), IconIsTemplate: true, ToolTip: "Auralink"})
	if err != nil {
		a.toast(i18n.T("The menu bar icon could not be made: %v", err))
		return
	}
	a.tray, a.trayLive = t, live
	a.buildTrayMenu()
	a.loadTrayRecent()
}

func (a *app) closeTray() {
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
}

// loadTrayRecent reads the last results from the history for the menu,
// then builds it again; at start and after the history changed.
func (a *app) loadTrayRecent() {
	if a.tray == nil && a.win != nil {
		return // no menu to show them in
	}
	h := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es, err := h.List(ctx, store.HistoryQuery{Limit: 20})
		if err != nil {
			return
		}
		a.post(func() {
			a.recent = recentResults(es, trayRecentN)
			a.buildTrayMenu()
		})
	})
}

// recentResults are the results of the newest n entries that delivered
// one (done, with a text).
func recentResults(es []store.Entry, n int) []string {
	var out []string
	for _, e := range es {
		if e.Status == store.StatusDone && strings.TrimSpace(e.Result) != "" {
			out = append(out, e.Result)
			if len(out) == n {
				break
			}
		}
	}
	return out
}

// trayLabel is a result as a menu item shows it: one line, its spaces
// and line breaks one space, at most trayRecentWidth display units wide,
// cut with "…" (Voxt cuts at a rendered width of 100 pt; a native menu's
// text cannot be measured here, so the units stand in for it).
func trayLabel(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if displayWidth(line) <= trayRecentWidth {
		return line
	}
	room, w := trayRecentWidth-1, 0 // the "…" takes one
	cut := len(line)
	for i, r := range line {
		if w+runeWidth(r) > room {
			cut = i
			break
		}
		w += runeWidth(r)
	}
	// Not in the middle of a (Latin) word: back to its space, if near.
	if latinAt(line, cut-1) && latinAt(line, cut) {
		if sp := strings.LastIndexByte(line[:cut], ' '); sp > 0 && displayWidth(line[sp:cut]) <= 10 {
			cut = sp
		}
	}
	return strings.TrimRight(line[:cut], " ,.;:，。；：、") + "…"
}

// latinAt says whether the rune at byte i of s (or the one ending there,
// for the last byte of one) is a Latin letter or digit.
func latinAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r < 0x2E80 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// trayTip is a result as its item's tooltip shows it: whole, unless very
// long.
func trayTip(text string) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > trayTipRunes {
		return strings.TrimSpace(string(r[:trayTipRunes])) + "…"
	}
	return text
}

// displayWidth is how wide s shows in display units: a wide (CJK) letter
// 2, a combining mark 0, any other 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case wideRune(r):
		return 2
	case unicode.Is(unicode.Mn, r) || r == 0x200B || r == 0xFE0F:
		return 0
	}
	return 1
}

// buildTrayMenu builds the icon's menu in the current language, and
// shows the recording icon while a session runs.
func (a *app) buildTrayMenu() {
	if a.tray == nil {
		return
	}
	if live := a.sess.State.Live(); live != a.trayLive {
		if a.tray.SetIcon(trayicon.Icon(live), true) == nil {
			a.trayLive = live
		}
	}
	a.tray.SetMenu(mygo.NewMenu(a.trayItems()))
}

// trayItems are the menu's items.
func (a *app) trayItems() []*mygo.MenuItem {
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
	items := []*mygo.MenuItem{
		{Label: start, Click: act(a.toggleTranscribe)},
		{Label: i18n.T("Paste the last result") + "    " + hotkeyGlyphs(f.PasteLast.Keys), Click: act(func() { a.pasteLast(a.front) })},
	}
	if len(a.recent) > 0 {
		items = append(items, mygo.Separator(), &mygo.MenuItem{Label: i18n.T("Recent results"), Disabled: true})
		for _, text := range a.recent {
			items = append(items, &mygo.MenuItem{Label: trayLabel(text), ToolTip: trayTip(text), Click: act(func() { a.copyRecent(text) })})
		}
	}
	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{Label: i18n.T("Open Auralink"), Click: act(a.showMainWindow)},
		&mygo.MenuItem{Label: i18n.T("Settings…"), Click: act(func() { a.showMainWindow(); a.navigate("/settings") })},
	)
	items = append(items, a.updateMenuItems(act)...) // updates.go
	return append(items,
		mygo.Separator(),
		&mygo.MenuItem{Label: i18n.T("Quit Auralink"), Click: act(func() { a.quitting = true; mygo.App.Quit() })},
	)
}

// copyRecent copies a recent result (the menu bar).
func (a *app) copyRecent(text string) {
	cp := a.copyText
	if cp == nil {
		cp = func(s string) error { return mygo.Clipboard.Write(transfer.TextData(s)) }
	}
	if err := cp(text); err != nil {
		a.toast(i18n.T("Could not copy: %v", err))
	}
}

// showMainWindow brings the main window back (it may be hidden in the
// menu bar).
func (a *app) showMainWindow() {
	if a.win != nil {
		a.win.Show()
	}
}

// connectWindowLife keeps Auralink in the menu bar when its window closes
// (the setting, while the menu bar icon shows), and quits otherwise; it
// reads the permissions again whenever Auralink comes to the front
// (System Settings may have changed them).
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
