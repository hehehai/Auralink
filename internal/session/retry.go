package session

// Retry (behavior.md 5.20a, from VoxtNew 4d5d5f62 and 78e24f3b): a
// history entry made again from its recording, beside any session.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hehehai/Auralink/internal/appctx"
	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/store"
)

// RetryRequest asks for a history entry to be made again from its
// recording.
type RetryRequest struct {
	Entry store.Entry
	// OnStage is told when recognition (Recognizing) and the language
	// model (Composing) start, on Retry's goroutine; nil: nobody.
	OnStage func(State)
}

var (
	// ErrNoRecording: the entry kept no recording to retry.
	ErrNoRecording = errors.New("session: the entry kept no recording")
	// ErrRetrying: another retry runs (one at a time).
	ErrRetrying = errors.New("session: another entry is being retried")
)

// DefaultStreamSpeed is how many times faster than it was said a
// streaming service gets a recording to retry (Config.StreamSpeed).
const DefaultStreamSpeed = 4

// streamChunk is how much of a recording a retry sends to a stream at a
// time: 200 ms, Doubao's packet.
const streamChunk = asr.SampleRate * 2 / 5

// retrying is the Manager's one retry at a time.
type retrying struct{ busy atomic.Bool }

// Retry makes a history entry again from its recording: recognition as a
// whole recording (the voice activity detection and the sound
// processing of an upload; a streaming service gets it through a
// stream, at Config.StreamSpeed), then the feature's language model step
// with the settings as they are now (Transcribe's clean-up, Translate
// into the entry's target, Rewrite of the entry's selection), with the
// app enhancement group of the entry's app and site. Nothing is inserted.
// The entry is updated in place: done with its new texts and times, or
// failed with the new error. A success drops a recording kept only for
// the retry (store.Entry.AudioUntil) when the settings keep no
// recordings. It runs beside any session (it never takes the Manager's
// current session, nor its snapshot), one retry at a time (ErrRetrying),
// blocking; it returns the entry as written.
func (m *Manager) Retry(ctx context.Context, req RetryRequest) (store.Entry, error) {
	cfg := m.cfg
	e := req.Entry
	if cfg.History == nil {
		return e, errors.New("session: no history")
	}
	if e.Audio == "" {
		return e, ErrNoRecording
	}
	if !m.retry.busy.CompareAndSwap(false, true) {
		return e, ErrRetrying
	}
	defer m.retry.busy.Store(false)
	raw, err := os.ReadFile(cfg.History.AudioPath(e.Audio))
	if err != nil {
		return e, fmt.Errorf("%w: %v", ErrNoRecording, err)
	}
	pcm, rate, ch, err := asr.ReadWAV(raw)
	if err != nil || rate != asr.SampleRate || ch != 1 {
		return e, fmt.Errorf("%w: not a 16 kHz mono recording (%v)", ErrNoRecording, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &run{m: m, id: e.ID, req: StartRequest{Kind: e.Kind}, settings: cfg.Settings(), ctx: ctx, cancel: cancel,
		stop: make(chan struct{}), pcm: newPCMBuffer(), target: e.TargetLang}
	r.pcm.append(pcm)
	r.retryScene(e)
	stage := func(st State) {
		if req.OnStage != nil {
			req.OnStage(st)
		}
	}
	r.logf("info", "retry of a %s entry: %v of audio", e.Kind, asr.Duration(pcm, asr.SampleRate).Round(time.Millisecond))
	start := r.now()
	out, err := r.retry(e, pcm, stage)
	out.TotalMS = r.now().Sub(start).Milliseconds()
	if err != nil {
		r.logf("warning", "retry failed: %v", err)
		out.Status, out.Error = store.StatusFailed, err.Error()
	} else {
		out.Status = store.StatusDone // Error: a clean-up's failure, the text going on as recognised
		if !out.AudioUntil.IsZero() {
			if !r.settings.Privacy.KeepAudio {
				_ = os.Remove(cfg.History.AudioPath(out.Audio))
				out.Audio = ""
			}
			out.AudioUntil = time.Time{}
		}
		r.logf("info", "retry done in %v", time.Duration(out.TotalMS)*time.Millisecond)
	}
	pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pcancel()
	if perr := cfg.History.Put(pctx, out); perr != nil {
		r.logf("error", "history: %v", perr)
		if err == nil {
			err = perr
		}
	}
	return out, err
}

// Retrying reports whether a retry runs.
func (m *Manager) Retrying() bool { return m.retry.busy.Load() }

// retryScene decides a retry's app enhancement group from what its entry
// recorded: the app, and the host of a browser's tab.
func (r *run) retryScene(e store.Entry) {
	r.sc.done = make(chan struct{})
	app := proto.App{BundleID: e.AppBundle, Name: e.AppName}
	r.req.Target = app
	global := appctx.Match{Source: appctx.SourceGlobal, App: app}
	cfg := r.m.cfg.Scene
	if cfg == nil || cfg.Rules == nil || e.AppBundle == "" {
		r.setScene(global, false)
		return
	}
	rules, on := cfg.Rules()
	if !on {
		r.setScene(global, false)
		return
	}
	in := appctx.Context{App: app}
	if e.URLHost != "" {
		in.Browser, in.URL = true, "https://"+e.URLHost
	}
	r.setScene(appctx.Resolve(rules, in, string(e.Kind)), false)
}

// retryASRLimit is how long a retry's recognition may take: the
// session's limit after Stop, plus the recording's length (a stream gets
// it at Config.StreamSpeed; an upload of a long one takes longer too).
func (r *run) retryASRLimit(pcm []byte) time.Duration {
	return r.m.cfg.ASRTimeout + asr.Duration(pcm, asr.SampleRate)
}

// retry is Retry's work: e with its new texts, models and times, or the
// first error (e as far as it got).
func (r *run) retry(e store.Entry, pcm []byte, stage func(State)) (store.Entry, error) {
	cfg := r.m.cfg
	svc, err := cfg.Resolve(r.ctx, e.Kind, r.settings)
	if err != nil {
		return e, err
	}
	r.svc = svc
	if r.settings.Dictionary.Enabled && cfg.Glossary != nil {
		r.terms, _ = cfg.Glossary(r.ctx)
	}
	if p := r.lacks(); p != nil {
		return e, p.Err
	}
	stage(Recognizing)
	e.Error = ""
	e.ASRProvider, e.ASRModel = svc.ASRInfo.ID, svc.ASRInfo.Model
	e.LLMProvider, e.LLMModel, e.LLMMS = "", "", 0
	asrStart := r.now()
	upload, speech := r.prepare(pcm)
	e.DSP, e.GainDB = r.dsp, r.gainDB
	if !speech {
		e.SentMS = 0
		return e, errors.New("no speech was heard in the recording")
	}
	e.SentMS = asr.Duration(upload, asr.SampleRate).Milliseconds()
	limit := r.retryASRLimit(pcm)
	actx, acancel := context.WithTimeout(r.ctx, limit)
	res, err := r.recognizeWhole(actx, upload)
	switch {
	case err != nil && errors.Is(actx.Err(), context.DeadlineExceeded):
		err = &asr.Error{Kind: asr.KindNetwork, Detail: fmt.Sprintf("no result within %v", limit.Round(time.Second)), Retryable: true}
	case err != nil && actx.Err() != nil:
		err = r.ctxErr(actx)
	}
	acancel()
	e.ASRMS = r.now().Sub(asrStart).Milliseconds()
	if err != nil {
		return e, err
	}
	raw := strings.TrimSpace(res.Text)
	r.logf("info", "retry: recognised %s in %v", r.logText(raw), time.Duration(e.ASRMS)*time.Millisecond)
	if raw == "" {
		return e, errors.New("nothing was recognised in the recording")
	}
	if e.Kind == store.KindRewrite {
		e.PromptText = raw
	} else {
		e.RawText = raw
	}
	e.Result = raw
	if e.Kind == store.KindTranscribe && (!r.settings.Features.Transcribe.Enhance || svc.LLM == nil) {
		return e, nil
	}
	stage(Composing)
	llmStart := r.now()
	var llmErr error
	if e.Kind == store.KindTranscribe {
		var text string
		text, _, llmErr = r.compose(raw)
		switch {
		case llmErr != nil:
			// As a session: the text as recognised, with the warning.
			e.Error = llmErr.Error()
			llmErr = nil
		case text != "":
			e.Result = text
		}
	} else {
		ans, _, err := r.answer(r.writeInput(raw, e.SourceText), e.SourceText)
		e.Result, llmErr = strings.TrimSpace(ans.Content), err
		if llmErr == nil && e.Result == "" {
			llmErr = errors.New("the language model answered nothing")
		}
	}
	e.LLMMS = r.now().Sub(llmStart).Milliseconds()
	e.LLMProvider, e.LLMModel = svc.LLMInfo.ID, svc.LLMInfo.Model
	return e, llmErr
}

// recognizeWhole recognises a whole recording: one upload to a file
// service; through a stream for a streaming one, sent in streamChunk
// pieces at Config.StreamSpeed times the speed it was said, then Finish.
func (r *run) recognizeWhole(ctx context.Context, pcm []byte) (asr.Result, error) {
	if st, ok := r.streamer(); ok {
		return r.streamWhole(ctx, st, pcm)
	}
	if ft, ok := r.svc.ASR.(asr.FileTranscriber); ok {
		return ft.Transcribe(ctx, asr.FileRequest{Audio: pcm, Format: "pcm", SampleRate: asr.SampleRate, Hints: r.hints()})
	}
	return asr.Result{}, &asr.Error{Kind: asr.KindConfig, Detail: "the speech service neither uploads nor streams"}
}

// streamWhole sends pcm through one stream of st and waits for its final
// result.
func (r *run) streamWhole(ctx context.Context, st asr.Streamer, pcm []byte) (asr.Result, error) {
	stream, err := st.Open(ctx, asr.StreamRequest{SampleRate: asr.SampleRate, Hints: r.hints()})
	if err != nil {
		return asr.Result{}, err
	}
	defer stream.Close()
	speed := r.m.cfg.StreamSpeed
	if speed <= 0 {
		speed = DefaultStreamSpeed
	}
	sendErr := make(chan error, 1)
	go func() {
		pace := pcmDuration(streamChunk) / time.Duration(speed)
		for off := 0; off < len(pcm); off += streamChunk {
			if off > 0 {
				select {
				case <-time.After(pace):
				case <-ctx.Done():
					return
				}
			}
			if err := stream.Send(pcm[off:min(off+streamChunk, len(pcm))]); err != nil {
				sendErr <- err
				return
			}
		}
		if err := stream.Finish(); err != nil {
			sendErr <- err
		}
	}()
	events := stream.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return asr.Result{}, &asr.Error{Kind: asr.KindProtocol, Detail: "the stream ended without a final result"}
			}
			switch ev.Kind {
			case asr.EventFinal:
				res := ev.Result
				res.Text = ev.Text
				return res, nil
			case asr.EventError:
				return asr.Result{}, ev.Err
			}
		case err := <-sendErr:
			return asr.Result{}, asr.NetError(ctx, "", err)
		case <-ctx.Done():
			return asr.Result{}, ctx.Err()
		}
	}
}
