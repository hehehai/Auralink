package main

// The system's output, muted while a session records (Settings → Audio →
// Mute system audio while recording, on by default; docs/behavior.md
// §9.3): internal/outmute decides, the helper's output.* do it. Here it is
// wired to the app: the session's snapshots say when (with the cues: the
// start cue plays first, to be heard, and the stop cue after the output is
// back), the helper's status and notifications reach the controller, and
// an output that cannot be muted is said for a moment in the pill, on the
// live text's line (Voxt's status line), while the recording goes on.

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/i18n"
	"github.com/hehehai/Auralink/internal/macx"
	"github.com/hehehai/Auralink/internal/outmute"
	"github.com/hehehai/Auralink/internal/session"
)

// muteSource is the logs' source of the output's lines.
const muteSource = "audio"

// muteState is the output's mute as the main thread knows it.
type muteState struct {
	mute *outmute.Controller
	// muteJournal is where the helper keeps its claims (main.go: the
	// cache directory's output-mute; "" in tests: none).
	muteJournal string
	// muting is the session whose output is muted, or will be once its
	// start cue ended ("" for none).
	muting string
	// muteNote is the line the pill shows about the output while its
	// session records; seq numbers them, so that a note's timer clears
	// only itself.
	muteNote struct {
		id, text string
		seq      int
	}
}

// Times around the cues: the start cue plays at most startCueMax before
// the output is muted anyway; the stop cue waits at most stopCueMax for
// the output to come back.
const (
	startCueMax  = time.Second
	stopCueMax   = 300 * time.Millisecond
	muteNoteTime = 3 * time.Second
)

// outputJournal is the helper's journal of claims in the cache directory.
func outputJournal(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, "output-mute")
}

// openMute makes the controller; connectHelper calls it once the
// supervisor exists.
func (a *app) openMute() {
	logs := a.logs
	a.mute = outmute.New(outmute.Config{
		Output:  outmute.HelperOutput{H: a.host.sup},
		Journal: a.muteJournal,
		Log: func(level, msg string) {
			logs.Append(core.LogEntry{Time: time.Now(), Level: core.Severity(level), Source: muteSource, Message: msg})
		},
		OnUnmutable: func(id string) { a.post(func() { a.showMuteNote(id) }) },
	})
}

// muteHelperChanged tells the controller of a helper that became ready
// (its claims before it are undone first) or went away.
func (a *app) muteHelperChanged(was helper.State, st helper.Status) {
	switch {
	case st.State == helper.Ready && was != helper.Ready:
		a.mute.HelperReady(st.Has(proto.MethodOutputMute))
	case st.State != helper.Ready && was == helper.Ready:
		a.mute.HelperGone()
	}
}

// muteNotified hands the helper's output notifications to the controller.
func (a *app) muteNotified(m proto.Message) {
	switch m.Method {
	case proto.MethodOutputChanged:
		var s proto.OutputState
		if m.DecodeParams(&s) == nil {
			a.mute.Changed(s)
		}
	case proto.MethodOutputReleased:
		var p proto.OutputReleasedParams
		if m.DecodeParams(&p) == nil {
			a.mute.Released(p)
		}
	}
}

// recordingAudio is what a change of the session does to the speakers
// (events.go: onSession): its cue (Settings → Audio → Play sounds) and
// the output's mute. Leaving the recording gives the output back first,
// and the stop cue plays once it is back; entering it mutes the output
// once the start cue played.
func (a *app) recordingAudio(was, s session.Snapshot) {
	cue, hasCue := recordingCue(was, s)
	sounds := hasCue && a.prefs.Audio.Sounds && a.playSound != nil
	play := a.playSound
	if id := a.muting; id != "" && (s.ID != id || s.State != session.Recording) {
		a.muting = ""
		a.clearMuteNote(id)
		if sounds && cue == macx.SoundStop {
			sounds = false
			once := sync.Once{}
			then := func() { once.Do(func() { a.post(func() { a.async(func() { play(cue) }) }) }) }
			time.AfterFunc(stopCueMax, then) // the output is slow to come back: the cue anyway
			a.mute.Release(id, then)
		} else {
			a.mute.Release(id, nil)
		}
	}
	if s.State == session.Recording && (was.State != session.Recording || was.ID != s.ID) && a.prefs.Audio.MuteOn() && a.mute != nil {
		id := s.ID
		a.muting = id
		engage := func() {
			if a.muting == id && a.sess.ID == id && a.sess.State == session.Recording {
				a.mute.Engage(id)
			}
		}
		if sounds && cue == macx.SoundStart {
			sounds = false
			a.async(func() {
				done := make(chan struct{})
				go func() { play(cue); close(done) }()
				select {
				case <-done:
				case <-time.After(startCueMax):
				}
				a.post(engage)
			})
		} else {
			engage()
		}
	}
	if sounds {
		a.async(func() { play(cue) })
	}
}

// muteSettingChanged follows Settings → Audio → Mute system audio while
// recording: turned off during a recording, its output comes back now.
func (a *app) muteSettingChanged(on bool) {
	if id := a.muting; !on && id != "" {
		a.muting = ""
		a.clearMuteNote(id)
		a.mute.Release(id, nil)
	}
}

// closeMute gives the output back as the app quits, before the helper
// stops (which would undo it too).
func (a *app) closeMute() {
	if a.mute != nil {
		a.muting = ""
		a.mute.Close(time.Second)
	}
}

// showMuteNote says in the pill that session id's output could not be
// muted, while it records.
func (a *app) showMuteNote(id string) {
	if a.sess.ID != id || a.sess.State != session.Recording || a.sess.Try {
		return // a try's page shows no pill; a recording that stopped needs no word
	}
	a.muteNote.seq++
	seq := a.muteNote.seq
	a.muteNote.id, a.muteNote.text = id, i18n.T("This output device cannot be muted. Recording continues.")
	a.overlaySync()
	a.later(muteNoteTime, func() {
		if a.muteNote.seq == seq {
			a.clearMuteNote(id)
		}
	})
}

// clearMuteNote takes session id's note away; the pill goes back to its
// size (the live text's rule, only growing, is for the speech).
func (a *app) clearMuteNote(id string) {
	if a.muteNote.id != id || a.muteNote.text == "" {
		return
	}
	a.muteNote.id, a.muteNote.text = "", ""
	if a.ov.live.id == id {
		a.ov.live.w = 0
	}
	a.overlaySync()
}

// pillSession is the session as the pill shows it: the output's note on
// the live text's line while it shows (Voxt's status line comes before
// the text).
func (a *app) pillSession() session.Snapshot {
	s := a.sess
	if a.muteNote.text != "" && a.muteNote.id == s.ID && s.State == session.Recording {
		s.Partial = a.muteNote.text
	}
	return s
}
