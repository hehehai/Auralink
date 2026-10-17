package session

import (
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// TestRecordingRetention: with the settings keeping no recordings, an
// entry keeps its recording for a while (Config.AudioRetention) only when
// recognition failed, timed out or found nothing; with them keeping
// recordings, for as long as the entry (VoxtNew's TestRecordingRetention).
func TestRecordingRetention(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prov      asr.Provider
		keepAudio bool
		kept      bool
		until     bool
	}{
		{"done", &fakeFile{text: "x"}, false, false, false},
		{"done, keeping recordings", &fakeFile{text: "x"}, true, true, false},
		{"failed", &fakeFile{err: &asr.Error{Kind: asr.KindServer}}, false, true, true},
		{"failed, keeping recordings", &fakeFile{err: &asr.Error{Kind: asr.KindServer}}, true, true, false},
		{"timed out", &fakeFile{hang: true}, false, true, true},
		{"nothing recognised", &fakeFile{text: " "}, false, true, true},
		{"the stream fails at the end", &fakeStreamer{finalErr: &asr.Error{Kind: asr.KindServer}}, false, true, true},
		{"the stream fails while recording", &fakeStreamer{errAfter: 4}, false, true, true},
		{"the stream recognises nothing", &fakeStreamer{final: ""}, false, true, true},
		{"the stream is done", &fakeStreamer{final: "x"}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.prov, nil, func(c *Config, s *settings.Settings) {
				s.Privacy.KeepAudio = tc.keepAudio
				c.ASRTimeout = 300 * time.Millisecond
			})
			id := h.start()
			frames := 5
			if st, ok := tc.prov.(*fakeStreamer); ok && st.errAfter > 0 {
				frames = st.errAfter // the session ends there
			}
			h.speak(id, frames)
			end := h.rec.wait(t)
			es := h.hist.all()
			if len(es) != 1 || (es[0].Audio != "") != tc.kept || end.Recording != tc.kept || (!es[0].AudioUntil.IsZero()) != tc.until {
				t.Fatalf("end %s recording %v, entries %+v", end.State, end.Recording, es)
			}
			if tc.until {
				if d := time.Until(es[0].AudioUntil); d < 6*24*time.Hour || d > 7*24*time.Hour {
					t.Errorf("kept until %v", es[0].AudioUntil)
				}
			}
			if writeFileForTest(t, h.hist.dir, id+".wav") != tc.kept {
				t.Error("the file does not match the entry")
			}
		})
	}
}

// TestRecordingRetentionNotKept: no recording is kept for a retry without
// the history, nor for a try.
func TestRecordingRetentionNotKept(t *testing.T) {
	t.Run("history off", func(t *testing.T) {
		h := newHarness(t, &fakeFile{err: &asr.Error{Kind: asr.KindServer}}, nil, func(_ *Config, s *settings.Settings) { s.Privacy.KeepHistory = false })
		id := h.start()
		h.speak(id, 5)
		if end := h.rec.wait(t); end.Recording || len(h.hist.all()) != 0 || writeFileForTest(t, h.hist.dir, id+".wav") {
			t.Fatalf("end %+v, entries %+v", end, h.hist.all())
		}
	})
	t.Run("a try", func(t *testing.T) {
		h := newHarness(t, &fakeFile{err: &asr.Error{Kind: asr.KindServer}}, nil, nil)
		id, err := h.m.Start(StartRequest{Kind: store.KindTranscribe, Target: slack, Try: true})
		if err != nil {
			t.Fatal(err)
		}
		h.speak(id, 5)
		if end := h.rec.wait(t); end.Recording || len(h.hist.all()) != 0 || writeFileForTest(t, h.hist.dir, id+".wav") {
			t.Fatalf("end %+v, entries %+v", end, h.hist.all())
		}
	})
}
