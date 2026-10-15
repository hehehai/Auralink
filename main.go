// Auralink is a voice input app for macOS that uses remote models only:
// hold a shortcut to transcribe what you say into any text field, to
// translate speech or the selection, or to rewrite the selection (or write
// new text) from a spoken instruction, with per-app scene rules. The
// window is MyGo native UI in Go; a small Swift helper (auralink-helper)
// records the microphone, watches the keyboard and reads browser URLs.
//
// Package main is the UI (views and their state); the domain lives in
// internal/ (see docs/architecture.md). main only wires them together:
// each step is a function in the file of the area that owns it.
package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	// The overlay check (overlay_check.go) runs instead of the app: no
	// data, no lock, no helper.
	if dir := overlayCheckDir(); dir != "" {
		runOverlayCheck(dir)
		return
	}
	// Dev builds are "Auralink Dev"; share data with the installed app.
	mygo.App.SetName("Auralink")
	// One Auralink at a time (one library, one set of global shortcuts);
	// a second launch shows the first one's window.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	home, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	// The data folder may have moved (Settings → Storage); a move or an
	// erase asked for finishes here, before the library opens.
	dataDir, err := resolveDataDir(home) // storage_move.go
	if err != nil {
		showFatal(err)
		return
	}
	resources, _ := mygo.App.Path(mygo.PathResources)
	logDir, _ := mygo.App.Path(mygo.PathLogs)

	// Wiring: one call per area, each defined in the file of that area.
	lib, err := openLibrary(dataDir) // library.go
	if err != nil {
		showFatal(err) // shell.go: explains, then quits
		return
	}
	h := newHelperHost(resources, logDir) // helperlink.go
	a := newApp(lib, h)                   // app.go
	a.dataHome = home                     // settings_storage.go
	a.connectShell(logDir)                // shell.go
	a.connectHelper()                     // helperlink.go

	mygo.App.WhenReady(func() {
		a.openWindow()        // shell.go
		a.startHelper()       // helperlink.go
		a.openTray()          // tray.go: the menu bar icon, close to the menu bar
		a.connectWindowLife() // tray.go
		mygo.App.OnSecondInstance(func([]string, string) { a.post(a.showMainWindow) })
	})
	mygo.App.OnQuit(func() {
		a.quit()
		lib.close()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
