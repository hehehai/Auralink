package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/store"
)

// frame is 100 ms of a 16 kHz recording.
var frame = make([]byte, asr.BytesPerSecond/10)

// fakeAudio records frames every tick until Stop or ctx.
type fakeAudio struct {
	tick     time.Duration
	startErr error // Record fails before any audio
	maxFrame int   // the recording ends by itself after this many frames (0: never)
	failAt   int   // after this many frames Record fails with failErr
	failErr  error
	deaf     bool // ignore Stop (a capture.stop that raced its start)

	mu      sync.Mutex
	stops   map[string]chan struct{}
	late    map[string]func(AudioEvent) // the callback of an ended recording
	frames  int
	stopped int
}

func newFakeAudio() *fakeAudio {
	return &fakeAudio{tick: time.Millisecond, stops: map[string]chan struct{}{}, late: map[string]func(AudioEvent){}}
}

func (f *fakeAudio) Record(ctx context.Context, id string, p proto.CaptureParams, fn func(AudioEvent)) (proto.CaptureResult, error) {
	if f.startErr != nil {
		return proto.CaptureResult{}, f.startErr
	}
	stop := make(chan struct{})
	f.mu.Lock()
	f.stops[id] = stop
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.late[id] = fn
		f.mu.Unlock()
	}()
	fn(AudioEvent{Started: &proto.CaptureStartedParams{Job: id, DeviceName: "MacBook Air Microphone", SampleRate: 16000}})
	n := 0
	t := time.NewTicker(f.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return proto.CaptureResult{}, &proto.Error{Code: proto.CodeCancelled, Message: "cancelled"}
		case <-stop:
			return proto.CaptureResult{Reason: proto.CaptureStopped, Frames: n}, nil
		case <-t.C:
			fn(AudioEvent{Level: &proto.LevelParams{Job: id, RMS: -20}})
			fn(AudioEvent{PCM: &proto.PCMParams{Job: id, Seq: n, Data: frame}})
			n++
			f.mu.Lock()
			f.frames++
			f.mu.Unlock()
			if f.failAt > 0 && n == f.failAt {
				return proto.CaptureResult{}, f.failErr
			}
			if f.maxFrame > 0 && n == f.maxFrame {
				return proto.CaptureResult{Reason: proto.CaptureMaxSeconds, Frames: n}, nil
			}
		}
	}
}

func (f *fakeAudio) Stop(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped++
	if f.deaf {
		return nil
	}
	if ch, ok := f.stops[id]; ok {
		delete(f.stops, id)
		close(ch)
	}
	return nil
}

func (f *fakeAudio) recorded() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frames
}

// lateEvents plays events to an ended recording's callback.
func (f *fakeAudio) lateEvents(id string) {
	f.mu.Lock()
	fn := f.late[id]
	f.mu.Unlock()
	if fn == nil {
		return
	}
	fn(AudioEvent{Level: &proto.LevelParams{Job: id, RMS: 0}})
	fn(AudioEvent{PCM: &proto.PCMParams{Job: id, Data: frame}})
	fn(AudioEvent{Started: &proto.CaptureStartedParams{Job: id, DeviceName: "late"}})
}

// fakeStreamer streams: a partial per frame, the final on Finish.
type fakeStreamer struct {
	final    string
	openErr  error
	finalErr error         // the stream fails instead of answering
	errAfter int           // the stream fails after this many sends
	hang     bool          // no final after Finish
	openWait time.Duration // Open takes this long

	mu      sync.Mutex
	streams []*fakeStream
	hints   []string
}

func (f *fakeStreamer) Caps() asr.Caps { return asr.Caps{Stream: true, Hints: true} }

func (f *fakeStreamer) Open(ctx context.Context, req asr.StreamRequest) (asr.Stream, error) {
	if f.openWait > 0 {
		select {
		case <-time.After(f.openWait):
		case <-ctx.Done():
			return nil, &asr.Error{Kind: asr.KindCancelled}
		}
	}
	if f.openErr != nil {
		return nil, f.openErr
	}
	s := &fakeStream{f: f, ctx: ctx, events: make(chan asr.Event, 1024)}
	f.mu.Lock()
	f.streams = append(f.streams, s)
	f.hints = req.Hints
	f.mu.Unlock()
	return s, nil
}

type fakeStream struct {
	f      *fakeStreamer
	ctx    context.Context
	mu     sync.Mutex
	bytes  int
	sends  int
	closed bool
	done   bool
	events chan asr.Event
}

func (s *fakeStream) emit(ev asr.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.done {
		return
	}
	if ev.Kind != asr.EventPartial {
		s.done = true
	}
	s.events <- ev
	if s.done {
		close(s.events)
	}
}

func (s *fakeStream) Send(pcm []byte) error {
	s.mu.Lock()
	s.bytes += len(pcm)
	s.sends++
	n := s.sends
	s.mu.Unlock()
	if s.f.errAfter > 0 && n >= s.f.errAfter {
		s.emit(asr.Event{Kind: asr.EventError, Err: &asr.Error{Kind: asr.KindNetwork, Detail: "connection reset"}})
		return nil
	}
	s.emit(asr.Event{Kind: asr.EventPartial, Text: "partial"})
	return nil
}

func (s *fakeStream) Finish() error {
	switch {
	case s.f.hang:
	case s.f.finalErr != nil:
		s.emit(asr.Event{Kind: asr.EventError, Err: s.f.finalErr})
	default:
		s.emit(asr.Event{Kind: asr.EventFinal, Text: s.f.final, Result: asr.Result{Text: s.f.final, Model: "fake-stream"}})
	}
	return nil
}

func (s *fakeStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if !s.done {
			close(s.events)
		}
	}
}

func (s *fakeStream) Events() <-chan asr.Event { return s.events }

// fakeFile uploads.
type fakeFile struct {
	text string
	err  error
	hang bool

	mu    sync.Mutex
	calls int
	bytes int
}

func (f *fakeFile) Caps() asr.Caps { return asr.Caps{File: true, Prompt: true} }

func (f *fakeFile) Transcribe(ctx context.Context, req asr.FileRequest) (asr.Result, error) {
	f.mu.Lock()
	f.calls++
	f.bytes = len(req.Audio)
	f.mu.Unlock()
	if f.hang {
		<-ctx.Done()
		return asr.Result{}, &asr.Error{Kind: asr.KindCancelled, Detail: ctx.Err().Error()}
	}
	return asr.Result{Text: f.text, Model: "fake-file"}, f.err
}

// fakeLLM answers chats: text, or texts in turn (the last repeats),
// streamed in pieces of chunk runes to OnDelta (0: in one), each after
// pause.
type fakeLLM struct {
	text  string
	texts []string
	err   error
	hang  bool
	chunk int
	pause time.Duration

	mu   sync.Mutex
	reqs []llm.Request
}

func (f *fakeLLM) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	text := f.text
	if len(f.texts) > 0 {
		text = f.texts[min(len(f.reqs), len(f.texts))-1]
	}
	f.mu.Unlock()
	if f.hang {
		if req.OnDelta != nil {
			req.OnDelta("Half an ans")
		}
		<-ctx.Done()
		return llm.Response{}, ctx.Err()
	}
	if f.err != nil {
		return llm.Response{}, f.err
	}
	if req.OnDelta != nil {
		rs := []rune(text)
		n := f.chunk
		if n <= 0 {
			n = len(rs)
		}
		for i := 0; i < len(rs); i += n {
			if f.pause > 0 {
				time.Sleep(f.pause)
			}
			req.OnDelta(string(rs[i:min(i+n, len(rs))]))
		}
	}
	return llm.Response{Text: text}, nil
}

func (f *fakeLLM) requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.reqs...)
}

// fakeText is the app in front.
type fakeText struct {
	focus proto.FocusResult
	err   error

	mu     sync.Mutex
	pasted []string
}

func (f *fakeText) Focus(context.Context) (proto.FocusResult, error) { return f.focus, f.err }

func (f *fakeText) Paste(_ context.Context, p proto.PasteParams) (proto.PasteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasted = append(f.pasted, p.Text)
	return proto.PasteResult{Posted: true, Restored: true}, nil
}

func (f *fakeText) pastes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pasted...)
}

// memHistory keeps entries in memory.
type memHistory struct {
	dir     string
	mu      sync.Mutex
	entries []store.Entry
}

func (h *memHistory) Put(_ context.Context, e store.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, e)
	return nil
}
func (h *memHistory) Get(context.Context, string) (store.Entry, error) {
	return store.Entry{}, errors.New("no")
}
func (h *memHistory) List(context.Context, store.HistoryQuery) ([]store.Entry, error) {
	return nil, nil
}
func (h *memHistory) Delete(context.Context, string) error               { return nil }
func (h *memHistory) DeleteAll(context.Context, store.Kind) (int, error) { return 0, nil }
func (h *memHistory) AudioDir() string                                   { return h.dir }

func (h *memHistory) all() []store.Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Entry(nil), h.entries...)
}

// recorder keeps every snapshot.
type recorder struct {
	mu    sync.Mutex
	snaps []Snapshot
	ended chan Snapshot
}

func newRecorder() *recorder { return &recorder{ended: make(chan Snapshot, 16)} }

func (r *recorder) add(s Snapshot) {
	r.mu.Lock()
	r.snaps = append(r.snaps, s)
	r.mu.Unlock()
	if s.State.Ended() {
		r.ended <- s
	}
}

func (r *recorder) all() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Snapshot(nil), r.snaps...)
}

func (r *recorder) wait(t *testing.T) Snapshot {
	t.Helper()
	select {
	case s := <-r.ended:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("the session did not end; last snapshots %+v", tail(r.all()))
	}
	return Snapshot{}
}

// waitFor waits until a snapshot satisfies ok.
func (r *recorder) waitFor(t *testing.T, what string, ok func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range r.all() {
			if ok(s) {
				return s
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no snapshot %s; last %+v", what, tail(r.all()))
	return Snapshot{}
}

func tail(s []Snapshot) []Snapshot { return s[max(0, len(s)-3):] }

func writeFileForTest(t *testing.T, dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

var errHelperForTest = core.ErrHelper

// fakeSelector is the selection of the app in front: text read through
// Accessibility, or copyText only when ⌘C is allowed.
type fakeSelector struct {
	text     string
	copyText string
	err      error
	delay    time.Duration

	mu    sync.Mutex
	calls []proto.SelectionParams
}

func (f *fakeSelector) Selection(ctx context.Context, p proto.SelectionParams) (proto.SelectionResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, p)
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return proto.SelectionResult{}, ctx.Err()
		}
	}
	if f.err != nil {
		return proto.SelectionResult{}, f.err
	}
	switch {
	case f.text != "":
		return proto.SelectionResult{Text: f.text, Via: proto.ViaAX, MS: 2}, nil
	case f.copyText != "" && p.AllowCopy:
		return proto.SelectionResult{Text: f.copyText, Via: proto.ViaCopy, MS: 40}, nil
	}
	return proto.SelectionResult{Via: proto.ViaNone, MS: 1}, nil
}

func (f *fakeSelector) params() []proto.SelectionParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.SelectionParams(nil), f.calls...)
}
