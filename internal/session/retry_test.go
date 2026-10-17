package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// retryPCM is the recording of retryHarness's entry: two seconds of a
// voice.
var retryPCM = bytes.Repeat(frame, 20)

// retryHarness is a harness with a failed entry whose recording was kept
// (VoxtNew's).
func retryHarness(t *testing.T, prov asr.Provider, model LLM, keepAudio bool) (*harness, store.Entry) {
	h := newHarness(t, prov, model, func(c *Config, s *settings.Settings) {
		s.Privacy.KeepAudio = keepAudio
		s.Features.Transcribe.Enhance = model != nil
		c.StreamSpeed = 100
	})
	if err := os.WriteFile(filepath.Join(h.hist.dir, "e1.wav"), asr.WAV(retryPCM, asr.SampleRate), 0o644); err != nil {
		t.Fatal(err)
	}
	e := store.Entry{ID: "e1", Kind: store.KindTranscribe, Status: store.StatusFailed, Error: "doubao: network: no result within 30s",
		AppBundle: slack.BundleID, AppName: "Slack", Audio: "e1.wav", AudioUntil: time.Now().Add(time.Hour), AudioMS: 2000, CreatedAt: time.Now().Add(-time.Hour)}
	return h, e
}

// TestRetry: Retry makes a failed entry again from its recording, in
// place, without inserting anything; a success drops a recording kept
// only for it when the settings keep none.
func TestRetry(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		model := &fakeLLM{text: "Hello, Auralink."}
		file := &fakeFile{text: "hello aura link"}
		h, e := retryHarness(t, file, model, false)
		var mu sync.Mutex
		var stages []State
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e, OnStage: func(s State) { mu.Lock(); stages = append(stages, s); mu.Unlock() }})
		if err != nil {
			t.Fatal(err)
		}
		if out.ID != "e1" || out.Status != store.StatusDone || out.Error != "" || out.RawText != "hello aura link" || out.Result != "Hello, Auralink." ||
			out.ASRProvider != "doubao" || out.LLMProvider != "deepseek" || out.Audio != "" || !out.AudioUntil.IsZero() || !out.CreatedAt.Equal(e.CreatedAt) {
			t.Fatalf("entry %+v", out)
		}
		if !slices.Equal(stages, []State{Recognizing, Composing}) {
			t.Errorf("stages %v", stages)
		}
		if writeFileForTest(t, h.hist.dir, "e1.wav") {
			t.Error("the recording kept for the retry is still there")
		}
		if es := h.hist.all(); len(es) != 1 || es[0].Result != out.Result {
			t.Errorf("written %+v", es)
		}
		if len(h.text.pastes()) != 0 || h.m.Current().State != Idle || len(h.rec.all()) != 0 {
			t.Errorf("pasted %q, session %s, %d snapshots", h.text.pastes(), h.m.Current().State, len(h.rec.all()))
		}
		// The upload: the recording without its silence, the dictionary's
		// hints; the clean-up got the recognised text.
		if file.calls != 1 || len(file.audio) == 0 || out.SentMS == 0 || out.DSP == "" {
			t.Errorf("upload: %d calls, %d bytes, sent %d ms, dsp %q", file.calls, len(file.audio), out.SentMS, out.DSP)
		}
		if reqs := model.requests(); len(reqs) != 1 || !containsText(reqs[0], "hello aura link") {
			t.Errorf("requests %+v", reqs)
		}
	})
	t.Run("keeping recordings", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: "x"}, nil, true)
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err != nil || out.Audio != "e1.wav" || !out.AudioUntil.IsZero() || out.Result != "x" || !writeFileForTest(t, h.hist.dir, "e1.wav") {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
	t.Run("fails again", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{err: &asr.Error{Kind: asr.KindServer, Detail: "the service stopped answering"}}, nil, false)
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err == nil || out.Status != store.StatusFailed || out.Error == e.Error || out.Audio != "e1.wav" || !out.AudioUntil.Equal(e.AudioUntil) {
			t.Fatalf("entry %+v, %v", out, err)
		}
		if !writeFileForTest(t, h.hist.dir, "e1.wav") {
			t.Error("the recording went")
		}
	})
	t.Run("nothing recognised", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: " "}, nil, false)
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err == nil || out.Status != store.StatusFailed || out.Audio != "e1.wav" {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
	t.Run("the clean-up fails", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: "x"}, &fakeLLM{err: errors.New("boom")}, false)
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err != nil || out.Status != store.StatusDone || out.Result != "x" || out.Error == "" || out.Audio != "" {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
	t.Run("no recording", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: "x"}, nil, false)
		e.Audio = ""
		if _, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); !errors.Is(err, ErrNoRecording) {
			t.Fatalf("err %v", err)
		}
		e.Audio = "gone.wav"
		if _, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); !errors.Is(err, ErrNoRecording) {
			t.Fatalf("missing file: err %v", err)
		}
	})
	t.Run("one at a time", func(t *testing.T) {
		file := &fakeFile{hang: true}
		h, e := retryHarness(t, file, nil, false)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := h.m.Retry(ctx, RetryRequest{Entry: e}); done <- err }()
		deadline := time.Now().Add(5 * time.Second)
		for !h.m.Retrying() {
			if time.Now().After(deadline) {
				t.Fatal("the first retry did not start")
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); !errors.Is(err, ErrRetrying) {
			t.Errorf("second retry: %v", err)
		}
		cancel()
		if err := <-done; err == nil {
			t.Error("the cancelled retry succeeded")
		}
		if h.m.Retrying() {
			t.Error("still retrying")
		}
	})
	t.Run("beside a session", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: "again"}, nil, false)
		id := h.start()
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err != nil || out.Result != "again" {
			t.Fatalf("entry %+v, %v", out, err)
		}
		if cur := h.m.Current(); cur.ID != id || cur.State != Recording {
			t.Fatalf("the session changed: %+v", cur)
		}
		h.speak(id, 3)
		if end := h.rec.wait(t); end.State != Done || end.Result != "again" {
			t.Errorf("session %+v", end)
		}
	})
}

// TestRetryStream: a streaming service gets the whole recording through
// one stream, then Finish; its final is the text.
func TestRetryStream(t *testing.T) {
	st := &fakeStreamer{final: "streamed again"}
	h, e := retryHarness(t, st, nil, false)
	out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
	if err != nil || out.Status != store.StatusDone || out.Result != "streamed again" || out.Audio != "" {
		t.Fatalf("entry %+v, %v", out, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.streams) != 1 {
		t.Fatalf("%d streams", len(st.streams))
	}
	s := st.streams[0]
	if s.bytes == 0 || s.bytes > len(retryPCM) || s.sends != (s.bytes+streamChunk-1)/streamChunk || !slices.Equal(st.hints, []string{"Auralink"}) {
		t.Errorf("sent %d bytes in %d sends of %d, hints %q", s.bytes, s.sends, len(retryPCM), st.hints)
	}
	t.Run("fails", func(t *testing.T) {
		h, e := retryHarness(t, &fakeStreamer{finalErr: &asr.Error{Kind: asr.KindServer, Detail: "boom"}}, nil, false)
		if out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); err == nil || out.Status != store.StatusFailed || out.Audio == "" {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
	t.Run("hangs", func(t *testing.T) {
		h, e := retryHarness(t, &fakeStreamer{hang: true}, nil, false)
		h.m.cfg.ASRTimeout = 50 * time.Millisecond
		if out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); err == nil || out.Status != store.StatusFailed || !strings.Contains(out.Error, "no result") {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
}

// TestRetryWrite: Translate into the entry's target, Rewrite of the
// entry's selection, with the settings as they are now.
func TestRetryWrite(t *testing.T) {
	t.Run("translate", func(t *testing.T) {
		model := &fakeLLM{text: "Good morning."}
		h, e := retryHarness(t, &fakeFile{text: "早上好"}, model, false)
		e.Kind, e.TargetLang = store.KindTranslate, "en"
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err != nil || out.RawText != "早上好" || out.Result != "Good morning." || out.TargetLang != "en" {
			t.Fatalf("entry %+v, %v", out, err)
		}
		if reqs := model.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].Content, "English") {
			t.Errorf("requests %+v", reqs)
		}
	})
	t.Run("rewrite", func(t *testing.T) {
		model := &fakeLLM{text: "Dear team, the meeting moves to Friday."}
		h, e := retryHarness(t, &fakeFile{text: "make it formal"}, model, false)
		e.Kind, e.SourceText = store.KindRewrite, "meeting's on friday now"
		out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e})
		if err != nil || out.PromptText != "make it formal" || out.RawText != "" || out.Result != "Dear team, the meeting moves to Friday." {
			t.Fatalf("entry %+v, %v", out, err)
		}
		if reqs := model.requests(); len(reqs) != 1 || !containsText(reqs[0], "meeting's on friday now") || !containsText(reqs[0], "make it formal") {
			t.Errorf("requests %+v", reqs)
		}
	})
	t.Run("no language model", func(t *testing.T) {
		h, e := retryHarness(t, &fakeFile{text: "x"}, nil, false)
		e.Kind = store.KindTranslate
		if out, err := h.m.Retry(context.Background(), RetryRequest{Entry: e}); err == nil || out.Status != store.StatusFailed {
			t.Fatalf("entry %+v, %v", out, err)
		}
	})
}

// containsText reports whether a request's last message holds text.
func containsText(req llm.Request, text string) bool {
	return len(req.Messages) > 0 && strings.Contains(req.Messages[len(req.Messages)-1].Content, text)
}
