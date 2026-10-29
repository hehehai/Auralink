package main

// The diagnostic lines a user copies to a bug report (docs/diagnostics.md):
// what the start found (the version, the data folder, the database, the
// settings, the menu bar icon, the files that move or erase the data), and
// what closing the window decided. Lines are English, and hold no text the
// user wrote, no key, and no path beyond ~ (diag.Tilde). The Keychain's
// lines are in diagnostics_keys.go, the sessions' in session_diag.go.

import (
	"fmt"
	"os"
	"time"

	"github.com/hehehai/Auralink/internal/actnow"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/datadir"
	"github.com/hehehai/Auralink/internal/diag"
	"github.com/hehehai/Auralink/internal/logs"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store/sqlitestore"
)

// diagSource is the logs' source of the start-up, database, window and
// menu bar lines.
const diagSource = "app"

// startFacts is what main found before the library opened: the data
// folder in use and the default one, whether the database was there
// already, and the folder's files that change a start.
type startFacts struct {
	dataDir, home string
	dbExisted     bool
	files         datadir.Files
}

// The log lines the copied diagnostics carry: the newest diagLogMax, and
// only those of the last diagLogWithin, whichever is fewer.
const (
	diagLogMax    = 200
	diagLogWithin = 30 * time.Minute
)

// recentLogLines formats the newest max lines of r that are at most within
// old, oldest first (r may be nil).
func recentLogLines(r *logs.Ring, max int, within time.Duration) []string {
	if r == nil {
		return nil
	}
	cut := time.Now().Add(-within)
	es := r.Tail(max, "") // newest first
	var out []string
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Time.Before(cut) {
			continue
		}
		out = append(out, logs.Format(es[i]))
	}
	return out
}

// appendLine adds a line to r (any goroutine; a nil ring takes nothing).
func appendLine(r *logs.Ring, sev core.Severity, source, msg string) {
	if r != nil {
		r.Append(core.LogEntry{Time: time.Now(), Level: sev, Source: source, Message: msg})
	}
}

// diagLog adds a line to the app's logs (main thread).
func (a *app) diagLog(sev core.Severity, source, msg string) {
	appendLine(a.logs, sev, source, msg)
}

// recordStartFailure writes why the app quits at start to its log file: the
// logs are in memory until connectShell, which a failed start never reaches.
func recordStartFailure(logDir, msg string) {
	if logDir == "" {
		return
	}
	r := logs.New(logs.Options{Dir: logDir})
	appendLine(r, core.SeverityError, diagSource, msg)
	r.Close()
}

// userHome is the user's home folder ("" when unknown), for diag.Tilde.
func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// trayText is the menu bar icon's state for the summary (main thread).
func (a *app) trayText() string {
	switch {
	case !a.prefs.General.MenuBar:
		return "menu bar icon off"
	case a.tray != nil:
		return "menu bar icon created"
	case a.trayErr != "":
		return "menu bar icon not created (" + a.trayErr + ")"
	}
	return "menu bar icon not shown"
}

// logStartup writes the start-up lines: a summary, the database, the
// settings and the data folder's files. It runs once the window and the
// menu bar exist (main's WhenReady); the bundle is read off the main thread.
func (a *app) logStartup(f startFacts) {
	ring, lib, g := a.logs, a.lib, a.prefs.General
	version := a.version
	if version == "" {
		version = "dev"
	}
	folder := diag.Tilde(f.dataDir, userHome())
	if f.dataDir == f.home {
		folder += " (default)"
	} else {
		folder += " (moved, Settings → Storage)"
	}
	db := "new database"
	if f.dbExisted {
		db = "database found"
	}
	summary := fmt.Sprintf("start: Auralink %s · data folder %s · %s · %s · close hides to the menu bar: %s · login item: %s",
		version, folder, db, a.trayText(), onOff(g.CloseToTray), onOff(g.LaunchAtLogin))
	files := fmt.Sprintf("data folder files: location.json %s, move.json %s, reset.json %s",
		yesNo(f.files.Location), yesNo(f.files.Move), yesNo(f.files.Reset))
	a.async(func() {
		bundle := "none (not an app bundle)"
		if s, ok := actnow.FindSelf(); ok {
			bundle = s.BundleID + ", variant " + string(s.Variant())
		}
		if demoStart != nil {
			bundle += ", demo build"
		}
		appendLine(ring, core.SeverityInfo, diagSource, summary+" · bundle "+bundle)
		appendLine(ring, core.SeverityInfo, diagSource, files)
		if f.files.Reset {
			appendLine(ring, core.SeverityWarning, diagSource, "reset.json is present: the data folder is erased at this start")
		}
		if f.files.Move {
			appendLine(ring, core.SeverityWarning, diagSource, "move.json is present: the data folder is moved at this start")
		}
		appendLine(ring, core.SeverityInfo, diagSource, databaseLine(lib.store.Opened()))
		appendLine(ring, core.SeverityInfo, diagSource, settingsLine(lib.settings.Stored(), lib.settingsErr))
	})
}

// databaseLine says what opening the database did.
func databaseLine(o sqlitestore.OpenInfo) string {
	switch {
	case o.Found == o.Now:
		return fmt.Sprintf("database: opened, schema version %d", o.Now)
	case o.Found == 0:
		return fmt.Sprintf("database: new, schema version %d", o.Now)
	}
	return fmt.Sprintf("database: migrated from schema version %d to %d (a copy of the old file is kept)", o.Found, o.Now)
}

// settingsLine says where the settings came from.
func settingsLine(stored bool, err error) string {
	switch {
	case err != nil:
		return "settings: could not be read (" + diag.ErrorClass(err) + "), defaults used"
	case stored:
		return "settings: saved settings read"
	}
	return "settings: no saved settings, defaults used"
}

// closeDecision is what closing the main window does: hide it in the menu
// bar (the window's OnClose) or let Auralink quit, and the line that says
// why. It hides only with the setting on, a menu bar icon, and no quit.
func closeDecision(g settings.General, quitting, hasTray bool) (hide bool, line string) {
	switch {
	case quitting:
		return false, "window closing: Auralink quits (quit was chosen)"
	case !g.CloseToTray:
		return false, "window closing: Auralink quits (keep running when the window closes is off)"
	case !hasTray:
		return false, "window closing: Auralink quits (no menu bar icon)"
	}
	return true, "window closed: hidden in the menu bar, Auralink keeps running"
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func yesNo(ok bool) string {
	if ok {
		return "present"
	}
	return "absent"
}
