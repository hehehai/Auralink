package main

// The output's mute in whole sessions on Linux: the app over the real
// supervisor and the fake helper process, whose pretend output devices
// live in a file (out=), so that what each helper process did can be read
// from the test, and they outlive a helper's crash.

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/helper/helpertest"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/session"
	"github.com/hehehai/Auralink/internal/settings"
)

// TestSessionMutesOutput: each feature records with the output muted,
// and it is back as the recording stops, before the services work on it
// (the language model's request finds it unmuted); a cancel gives it back
// too, and the follow-up of a Rewrite answer mutes again.
func TestSessionMutesOutput(t *testing.T) {
	for _, tc := range []struct {
		name, opts string
		setup      func(t *testing.T, chat *fakeChat) (func(*settings.Settings), map[string]string)
		events     []string
		end        session.State
	}{
		{name: "transcribe, upload", events: []string{hotkeyLine(0, "transcribe/0", proto.PhaseTrigger), hotkeyLine(500, "transcribe/0", proto.PhaseTrigger)}, end: session.Done},
		{name: "transcribe, stream", setup: func(t *testing.T, _ *fakeChat) (func(*settings.Settings), map[string]string) {
			url := aliyunServer(t, "你好，Auralink")
			return func(s *settings.Settings) {
				s.Providers.ASR = []settings.Provider{{ID: "aliyun", Enabled: true, Endpoint: url}}
				s.Features.Transcribe.Enhance = false
			}, map[string]string{"asr.aliyun": "sk-ali"}
		}, events: []string{hotkeyLine(0, "transcribe/0", proto.PhaseTrigger), hotkeyLine(600, "transcribe/0", proto.PhaseTrigger)}, end: session.Done},
		{name: "translate by voice", events: []string{hotkeyLine(0, "translate/0", proto.PhaseTrigger), hotkeyLine(500, "translate/0", proto.PhaseTrigger)}, end: session.Done},
		{name: "rewrite", opts: "readonly", events: []string{hotkeyLine(0, "rewrite/0", proto.PhaseTrigger), hotkeyLine(500, "rewrite/0", proto.PhaseTrigger)}, end: session.Done},
		{name: "cancelled", events: []string{hotkeyLine(0, "transcribe/0", proto.PhaseTrigger), hotkeyLine(400, proto.HotkeyEscape, proto.PhaseTrigger)}, end: session.Cancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "output.json")
			muted := func() bool { return helpertest.LoadOutputSystem(state).Muted(testSpeakers) }
			var mu sync.Mutex
			var mutedAtChat []bool
			chat := &fakeChat{text: "Hello there.", onRequest: func() {
				m := muted()
				mu.Lock()
				mutedAtChat = append(mutedAtChat, m)
				mu.Unlock()
			}}
			set, keys, _ := fileSetup(t, "hello there", chat)
			if tc.setup != nil {
				set, keys = tc.setup(t, chat)
			}
			opts := helpertest.Opt("out", state) + "," + script(t, tc.events...)
			if tc.opts != "" {
				opts = tc.opts + "," + opts
			}
			a := newSessionApp(t, opts, set, keys)
			seen := false
			a.pump("the output muted while recording", func() bool {
				seen = seen || a.sess.State == session.Recording && muted()
				return seen || a.sess.State.Ended()
			})
			if !seen {
				t.Fatalf("not muted while recording (%s)", a.sess.State)
			}
			a.pump("the end", func() bool { return a.sess.State.Ended() })
			if a.sess.State != tc.end {
				t.Fatalf("end %s %+v", a.sess.State, a.sess.Problem)
			}
			a.mute.Wait()
			if muted() {
				t.Fatal("still muted after the session")
			}
			mu.Lock()
			atChat := slices.Clone(mutedAtChat)
			mu.Unlock()
			if slices.Contains(atChat, true) || (tc.end == session.Done && tc.name != "transcribe, stream" && len(atChat) == 0) {
				t.Errorf("muted while the language model worked (or no request): %v", atChat)
			}
			if w := helpertest.LoadOutputSystem(state).WriteLog(); !slices.Equal(w, []string{testSpeakers + ":true", testSpeakers + ":false"}) {
				t.Errorf("writes %v", w)
			}
			if tc.name != "rewrite" {
				return
			}
			// The answer card's follow-up records with the output muted too.
			front := helpertest.Apps["com.tinyspeck.slackmacgap"]
			a.hotkeyFired(proto.HotkeyEventParams{Binding: "rewrite/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, Front: &front})
			a.pump("the follow-up muted", func() bool { return a.sess.State == session.Recording && muted() })
			time.Sleep(300 * time.Millisecond)
			a.hotkeyFired(proto.HotkeyEventParams{Binding: "rewrite/0", Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap, Front: &front})
			a.pump("the follow-up's end", func() bool { return a.sess.State.Ended() })
			a.mute.Wait()
			if muted() {
				t.Fatal("still muted after the follow-up")
			}
		})
	}
}

// TestSessionMuteHelperCrash: the helper dies right after muting; the
// session fails, and the next helper unmutes what the first one left.
func TestSessionMuteHelperCrash(t *testing.T) {
	state := filepath.Join(t.TempDir(), "output.json")
	muted := func() bool { return helpertest.LoadOutputSystem(state).Muted(testSpeakers) }
	set, keys, _ := fileSetup(t, "hello", &fakeChat{text: "x"})
	ev := script(t, hotkeyLine(0, "transcribe/0", proto.PhaseTrigger))
	a := newSessionApp(t, "crash-mute,"+helpertest.Opt("out", state)+","+ev, set, keys)
	a.pump("the session's end", func() bool { return a.sess.State.Ended() })
	if a.sess.State != session.Failed {
		t.Fatalf("end %s", a.sess.State)
	}
	deadline := time.Now().Add(10 * time.Second)
	for muted() && time.Now().Before(deadline) {
		select {
		case fn := <-a.queue:
			fn()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if muted() {
		t.Fatal("the next helper did not unmute")
	}
}
