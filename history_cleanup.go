package main

// The history's automatic clean-up (settings.History, the store's Prune)
// and the recordings kept only for a retry (store.Entry.AudioUntil, the
// store's ExpireAudio), off the main thread; from VoxtNew (ba6b8047,
// 9fa88583, 4d5d5f62).
//
// The clean-up runs at the start, after each change of the history (a
// new entry: historyChanged's observers, events.go) and when its setting
// changes. Each entry it deletes goes with its recording, kept or kept
// for a while (a failed recognition's); the history's observers hear of
// it. The recordings kept for a retry go when their time is up: at the
// start, then every hour.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// histCleanState is the clean-up's state: it runs, and runs again when
// it ends.
type histCleanState struct {
	pruning, pruneAgain bool
}

// retention is what the history keeps under h at now.
func retention(h settings.History, now time.Time) store.Retention {
	return store.Retention{Before: h.Cutoff(now), PerKind: h.PerKind()}
}

// openHistorySettings applies the saved recordings folder and runs the
// clean-up once, as the app starts (newApp).
func (a *app) openHistorySettings() {
	a.applyAudioDir() // history_settings_ops.go
	a.pruneHistory()
}

// historySettingsChanged applies a change of settings.History
// (settingsChanged).
func (a *app) historySettingsChanged(old, new settings.Settings) {
	if old.History.AudioDir != new.History.AudioDir {
		a.applyAudioDir()
	}
	if !old.History.SameCleanup(new.History) {
		a.pruneHistory()
	}
}

// pruneHistory runs the clean-up in the background; one at a time, and
// once more when asked while it runs.
func (a *app) pruneHistory() {
	hc := &a.histClean
	if hc.pruning {
		hc.pruneAgain = true
		return
	}
	rt := retention(a.prefs.History, time.Now())
	if rt.Zero() {
		return
	}
	hc.pruning = true
	h := a.lib.store.History()
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n, err := h.Prune(ctx, rt)
		a.post(func() {
			hc := &a.histClean
			hc.pruning = false
			if err != nil {
				log.Printf("history: clean-up: %v", err)
			}
			if n > 0 {
				log.Printf("history: the clean-up deleted %d entries", n)
				a.recordingsChanged() // history_settings_ops.go: measured again
				a.historyChanged()
			}
			if hc.pruneAgain {
				hc.pruneAgain = false
				a.pruneHistory()
			}
		})
	})
}

// askCleanup saves a new clean-up setting, after asking when it would
// delete entries now (the dialog's question shows their number).
func (a *app) askCleanup(next settings.History) {
	rt := retention(next, time.Now())
	h := a.lib.store.History()
	a.async(func() {
		n, err := h.CountPrune(context.Background(), rt)
		a.post(func() {
			hs := &a.histSet
			if err != nil {
				a.toast(i18n.T("Could not read the history: %v", err))
				hs.formFor = "" // the controls show the setting again
				return
			}
			if n > 0 {
				hs.pruneAsk, hs.pruneN, hs.pruneTo = true, n, next
				return
			}
			a.saveCleanup(next)
		})
	})
}

// saveCleanup saves the clean-up's part of h (the folder stays).
func (a *app) saveCleanup(h settings.History) {
	a.updateSettings(func(s *settings.Settings) {
		s.History.CleanupOff, s.History.KeepDays, s.History.KeepCount = h.CleanupOff, h.KeepDays, h.KeepCount
	})
	a.histSet.formFor = ""
}

// recordingSweep is how often the recordings kept for a retry are
// checked for their end.
const recordingSweep = time.Hour

// expireRecordings deletes the recordings kept for a retry once their
// time is up (store.Entry.AudioUntil): now, then every recordingSweep
// (main.go, when the app is ready).
func (a *app) expireRecordings() {
	hs := a.lib.store.History()
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			n, err := hs.ExpireAudio(ctx, time.Now())
			cancel()
			if n > 0 || err != nil {
				msg := fmt.Sprintf("history: deleted %d recordings kept for a retry", n)
				level := core.SeverityInfo
				if err != nil {
					msg += ": " + err.Error()
					level = core.SeverityWarning
				}
				a.logs.Append(core.LogEntry{Time: time.Now(), Level: level, Source: sessionSource, Message: msg})
				a.post(func() {
					a.recordingsChanged()
					a.historyChanged()
				})
			}
			time.Sleep(recordingSweep)
		}
	}()
}

// recordingKeptText says, in the details, why a recording is kept when
// the settings keep none, and until when.
func recordingKeptText(e store.Entry) string {
	if e.Audio == "" || e.AudioUntil.IsZero() {
		return ""
	}
	return i18n.T("Recording kept because recognition failed; deleted after %s.", i18n.DateTime(e.AudioUntil))
}
