package main

// The sessions' diagnostic lines (docs/diagnostics.md): a start refused
// because a session runs, a start that does not happen and why, and a
// shortcut press that starts nothing. Session lines are written at the
// logs' session source; they hold no text the user wrote.

import (
	"fmt"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/hotkey"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/store"
)

// refusalLine is the warning for a start refused because a session runs:
// its state, id and how long it has run.
func refusalLine(kind store.Kind, cur session.Snapshot, now time.Time) string {
	age := "unknown"
	if !cur.StartedAt.IsZero() {
		age = now.Sub(cur.StartedAt).Round(time.Second).String()
	}
	return fmt.Sprintf("start %s refused: a session is %s (id %s, started %s ago)", kind, cur.State, cur.ID, age)
}

// logRefused writes the warning for a start refused because cur runs.
func (a *app) logRefused(kind store.Kind, cur session.Snapshot) {
	a.diagLog(core.SeverityWarning, sessionSource, refusalLine(kind, cur, time.Now()))
}

// shortcutLine says what a shortcut press did when it does not start a
// session: "" for presses that need no line (a key release, a start).
func shortcutLine(e proto.HotkeyEventParams, d hotkey.Decision, state session.State) string {
	if e.Phase != proto.PhaseTrigger && e.Phase != proto.PhaseDown {
		return ""
	}
	switch d.Action {
	case hotkey.None:
		return fmt.Sprintf("shortcut %s ignored: a session is %s", e.Binding, state)
	case hotkey.Stop:
		return fmt.Sprintf("shortcut %s: stop the recording", e.Binding)
	case hotkey.Cancel:
		return fmt.Sprintf("shortcut %s: cancel the session", e.Binding)
	case hotkey.Paste:
		return fmt.Sprintf("shortcut %s: paste the last result", e.Binding)
	}
	return ""
}

// logShortcut writes the line of a shortcut press that starts nothing.
func (a *app) logShortcut(e proto.HotkeyEventParams, d hotkey.Decision) {
	if line := shortcutLine(e, d, a.sess.State); line != "" {
		a.diagLog(core.SeverityInfo, sessionSource, line)
	}
}
