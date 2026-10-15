package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/lang"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// run is one session. Its fields are the session goroutine's, but for
// ended (Manager.mu) and what is atomic or a channel.
type run struct {
	m        *Manager
	id       string
	req      StartRequest
	settings settings.Settings

	ctx       context.Context // the session's: cancelled by Cancel and at the end
	cancel    context.CancelFunc
	cancelled atomic.Bool
	stop      chan struct{}
	stopOnce  sync.Once
	pcm       *pcmBuffer
	ended     bool // guarded by m.mu

	svc   Services
	terms []store.Term
}

type capResult struct {
	res proto.CaptureResult
	err error
}

type svcResult struct {
	svc   Services
	terms []store.Term
	err   error
}

type openResult struct {
	st  asr.Stream
	err error
}

// streamEnd is a stream's last event: its final result or its error.
type streamEnd struct {
	res asr.Result
	err error
}

func (r *run) logf(level, format string, args ...any) {
	r.m.cfg.Log(level, "session "+r.id+": "+fmt.Sprintf(format, args...))
}

// logText is a text for the log: itself when the settings allow it, else
// its length.
func (r *run) logText(s string) string {
	if r.settings.Privacy.LogText {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%d chars", len([]rune(s)))
}

func (r *run) now() time.Time { return r.m.cfg.Now() }

// main is the session's goroutine: recording, recognition, composition,
// delivery, history.
func (r *run) main() {
	defer r.cancel()
	cfg := r.m.cfg
	if r.req.Kind != store.KindTranscribe {
		r.fail(&Problem{Code: ProblemUnsupported, Err: fmt.Errorf("%s is not available yet", r.req.Kind)})
		return
	}
	r.logf("info", "start %s in %s", r.req.Kind, r.req.Target.BundleID)

	// The recording, at once: the user is speaking already.
	capCtx, capCancel := context.WithCancel(r.ctx)
	defer capCancel()
	capDone := make(chan capResult, 1)
	go func() {
		params := proto.CaptureParams{Devices: r.settings.Audio.Devices, SampleRate: asr.SampleRate, MaxSeconds: cfg.MaxRecording.Seconds()}
		res, err := cfg.Audio.Record(capCtx, r.id, params, r.onAudio)
		r.pcm.close()
		capDone <- capResult{res, err}
	}()
	// The services and the dictionary, meanwhile.
	svcDone := make(chan svcResult, 1)
	go func() {
		svc, err := cfg.Resolve(r.ctx, r.req.Kind, r.settings)
		var terms []store.Term
		if err == nil && r.settings.Dictionary.Enabled && cfg.Glossary != nil {
			terms, _ = cfg.Glossary(r.ctx)
		}
		svcDone <- svcResult{svc, terms, err}
	}()

	var (
		resolved   bool
		openDone   chan openResult
		stream     asr.Stream
		finals     chan streamEnd
		early      *streamEnd
		stopCh     = r.stop
		stopTimer  <-chan time.Time
		captured   capResult
		recordDone bool
	)
	for !recordDone {
		select {
		case <-r.ctx.Done():
			r.endCancelled(stream)
			return
		case s := <-svcDone:
			if s.err != nil {
				r.fail(ProblemOf(s.err))
				return
			}
			resolved, r.svc, r.terms = true, s.svc, s.terms
			r.m.update(r, func(sn *Snapshot) { sn.ASR, sn.LLM = s.svc.ASRInfo, s.svc.LLMInfo })
			if st, ok := r.streamer(); ok {
				openDone = make(chan openResult, 1)
				go func() {
					s, err := st.Open(r.ctx, asr.StreamRequest{SampleRate: asr.SampleRate, Hints: r.hints()})
					openDone <- openResult{s, err}
				}()
			}
		case o := <-openDone:
			openDone = nil
			if o.err != nil {
				r.fail(r.asrProblem(o.err))
				return
			}
			stream = o.st
			finals = r.feed(stream)
		case e := <-finals:
			finals = nil
			if e.err != nil {
				r.endFailed(r.asrProblem(e.err), stream)
				return
			}
			early = &e // a final before the end: kept for the end
		case <-stopCh:
			stopCh = nil
			at := r.now()
			r.m.update(r, func(s *Snapshot) { s.State, s.StoppedAt = Recognizing, at })
			go func() {
				ctx, cancel := context.WithTimeout(r.ctx, cfg.StopGrace)
				defer cancel()
				if err := cfg.Audio.Stop(ctx, r.id); err != nil {
					r.logf("warning", "capture.stop: %v", err)
				}
			}()
			stopTimer = time.After(cfg.StopGrace)
		case <-stopTimer:
			r.logf("warning", "the recording did not end %v after Stop: discarding the capture job", cfg.StopGrace)
			capCancel()
		case c := <-capDone:
			recordDone, captured = true, c
		}
	}
	if r.cancelled.Load() {
		r.endCancelled(stream)
		return
	}
	stopped := stopCh == nil
	if captured.err != nil && !(stopped && errors.Is(captured.err, context.Canceled)) && !isCancelled(captured.err, stopped) {
		r.endFailed(ProblemOf(captured.err), stream)
		return
	}
	pcm := r.pcm.bytes()
	audioLen := asr.Duration(pcm, asr.SampleRate)
	r.m.update(r, func(s *Snapshot) {
		s.State = Recognizing
		if s.StoppedAt.IsZero() {
			s.StoppedAt = r.now()
		}
		s.Timings.Audio = audioLen
	})
	r.logf("info", "recorded %v (%s, %d frames, %d restarts)", audioLen.Round(time.Millisecond), captured.res.Reason, captured.res.Frames, captured.res.Restarts)
	if audioLen < cfg.MinRecording {
		if stream != nil {
			stream.Close()
		}
		r.m.end(r, Cancelled, func(s *Snapshot) { s.Problem = &Problem{Code: ProblemTooShort} }, nil)
		return
	}
	asrStart := r.now()

	// The services, if they are still on their way.
	if !resolved {
		select {
		case s := <-svcDone:
			if s.err != nil {
				r.fail(ProblemOf(s.err))
				return
			}
			r.svc, r.terms = s.svc, s.terms
			r.m.update(r, func(sn *Snapshot) { sn.ASR, sn.LLM = s.svc.ASRInfo, s.svc.LLMInfo })
			if st, ok := r.streamer(); ok {
				openDone = make(chan openResult, 1)
				go func() {
					s, err := st.Open(r.ctx, asr.StreamRequest{SampleRate: asr.SampleRate, Hints: r.hints()})
					openDone <- openResult{s, err}
				}()
			}
		case <-r.ctx.Done():
			r.endCancelled(nil)
			return
		}
	}

	// Recognition.
	actx, acancel := context.WithTimeout(r.ctx, cfg.ASRTimeout)
	defer acancel()
	var res asr.Result
	var err error
	if _, ok := r.streamer(); ok {
		if openDone != nil {
			select {
			case o := <-openDone:
				if o.err != nil {
					r.endFailed(r.asrProblem(o.err), nil)
					return
				}
				stream = o.st
				finals = r.feed(stream)
			case <-actx.Done():
				r.endFailed(r.timeoutProblem(actx), nil)
				return
			}
		}
		switch {
		case early != nil:
			res = early.res
		default:
			select {
			case e := <-finals:
				res, err = e.res, e.err
			case <-actx.Done():
				err = r.ctxErr(actx)
			}
		}
		stream.Close()
	} else if ft, ok := r.svc.ASR.(asr.FileTranscriber); ok {
		res, err = ft.Transcribe(actx, asr.FileRequest{Audio: pcm, Format: "pcm", SampleRate: asr.SampleRate, Hints: r.hints()})
		if err != nil && actx.Err() != nil {
			err = r.ctxErr(actx)
		}
	} else {
		err = &asr.Error{Kind: asr.KindConfig, Detail: "the speech service neither uploads nor streams"}
	}
	if r.cancelled.Load() {
		r.endCancelled(nil)
		return
	}
	asrTime := r.now().Sub(asrStart)
	if err != nil {
		r.endFailed(r.asrProblem(err), nil, func(s *Snapshot) { s.Timings.ASR = asrTime })
		return
	}
	raw := strings.TrimSpace(res.Text)
	r.logf("info", "recognised %s in %v", r.logText(raw), asrTime.Round(time.Millisecond))
	r.m.update(r, func(s *Snapshot) { s.Raw, s.Partial, s.Timings.ASR = raw, raw, asrTime })
	if raw == "" {
		r.finish(Done, &Problem{Code: ProblemNoSpeech}, "")
		return
	}

	// One language-model request.
	result := raw
	var problem *Problem
	if f := r.settings.Features.Transcribe; f.Enhance && r.svc.LLM != nil {
		r.m.update(r, func(s *Snapshot) { s.State = Composing })
		llmStart := r.now()
		text, profile, err := r.compose(raw)
		llmTime := r.now().Sub(llmStart)
		if r.cancelled.Load() {
			r.endCancelled(nil)
			return
		}
		r.m.update(r, func(s *Snapshot) { s.Profile, s.Timings.LLM = profile, llmTime })
		if err != nil {
			r.logf("warning", "language model failed after %v, the text goes on as recognised: %v", llmTime.Round(time.Millisecond), err)
			problem = &Problem{Code: ProblemLLM, Err: err, Degraded: true}
		} else {
			r.logf("info", "composed %s in %v", r.logText(text), llmTime.Round(time.Millisecond))
			result = text
			if result == "" {
				r.finish(Done, &Problem{Code: ProblemNoSpeech}, "")
				return
			}
		}
	}
	r.finish(Done, problem, result)
}

// isCancelled reports whether a capture error is the end of a job that
// Stop discarded (cancelled after Config.StopGrace).
func isCancelled(err error, stopped bool) bool {
	var pe *proto.Error
	return stopped && errors.As(err, &pe) && pe.Code == proto.CodeCancelled
}

// streamer is the session's speech service as a streamer, when it streams.
func (r *run) streamer() (asr.Streamer, bool) {
	st, ok := r.svc.ASR.(asr.Streamer)
	return st, ok && r.svc.ASR.Caps().Stream
}

// hints are the dictionary's terms for the speech service.
func (r *run) hints() []string {
	if !r.settings.Dictionary.Enabled || !r.settings.Dictionary.ASRHints {
		return nil
	}
	var out []string
	for _, t := range r.terms {
		if t.Enabled && strings.TrimSpace(t.Term) != "" {
			out = append(out, t.Term)
		}
	}
	return out
}

// onAudio handles the recording's events (on the helper's reader
// goroutine: it must not block).
func (r *run) onAudio(ev AudioEvent) {
	switch {
	case ev.PCM != nil:
		r.pcm.append(ev.PCM.Data)
	case ev.Started != nil:
		st, at := ev.Started, r.now()
		r.m.update(r, func(s *Snapshot) { s.HeardAt, s.Device, s.FellBack = at, st.DeviceName, st.FellBack })
		if st.FellBack {
			r.logf("warning", "recording from %s: %s", st.DeviceName, st.FallbackReason)
		}
	case ev.Level != nil:
		v := Level(ev.Level.RMS)
		r.m.update(r, func(s *Snapshot) {
			s.Levels = append(s.Levels, v)
			if len(s.Levels) > Bars {
				s.Levels = s.Levels[len(s.Levels)-Bars:]
			}
		})
	case ev.Restarted != nil:
		r.logf("warning", "the input restarted (%s, attempt %d)", ev.Restarted.Reason, ev.Restarted.Attempt)
	}
}

// feed sends the recording to st as it grows, then Finish; its partials
// go to the snapshot. The returned channel gets the stream's end: the
// final result or the error.
func (r *run) feed(st asr.Stream) chan streamEnd {
	end := make(chan streamEnd, 1)
	sendErr := make(chan error, 1)
	go func() {
		off := 0
		for {
			chunk, closed, err := r.pcm.next(r.ctx, off)
			if err != nil {
				return
			}
			if len(chunk) > 0 {
				if err := st.Send(chunk); err != nil {
					sendErr <- err
					return
				}
				off += len(chunk)
				continue
			}
			if closed {
				if err := st.Finish(); err != nil {
					sendErr <- err
				}
				return
			}
		}
	}()
	go func() {
		events := st.Events()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					end <- streamEnd{err: &asr.Error{Kind: asr.KindProtocol, Detail: "the stream ended without a final result"}}
					return
				}
				switch ev.Kind {
				case asr.EventPartial:
					text := ev.Text
					r.m.update(r, func(s *Snapshot) { s.Partial = text })
				case asr.EventFinal:
					res := ev.Result
					res.Text = ev.Text
					end <- streamEnd{res: res}
					return
				case asr.EventError:
					end <- streamEnd{err: ev.Err}
					return
				}
			case err := <-sendErr:
				// The reader usually has the real reason; give it a moment.
				select {
				case ev, ok := <-events:
					if ok && ev.Kind == asr.EventError {
						err = ev.Err
					}
				case <-time.After(500 * time.Millisecond):
				}
				end <- streamEnd{err: asr.NetError(r.ctx, "", err)}
				return
			}
		}
	}()
	return end
}

// compose runs the language model over raw.
func (r *run) compose(raw string) (string, string, error) {
	f := r.settings.Features.Transcribe
	g := r.settings.General
	main := g.MainLanguage
	if main == "" {
		main = lang.Canonical(r.m.cfg.Locale())
	}
	in := compose.Input{
		Task: compose.Transcribe, Cleanup: f.Cleanup, Structure: f.Structure, TaskPrompt: f.Prompt,
		MainLanguage: main, OtherLanguages: g.OtherLanguages, Text: raw,
	}
	if r.settings.Dictionary.Enabled && r.settings.Dictionary.Glossary {
		for _, t := range r.terms {
			if t.Enabled {
				in.Glossary = append(in.Glossary, compose.Term{Term: t.Term, Aliases: t.Aliases})
			}
		}
	}
	out, err := compose.Build(in)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.m.cfg.LLMTimeout)
	defer cancel()
	resp, err := r.svc.LLM.Chat(ctx, llm.Request{
		Messages: []llm.Message{{Role: llm.System, Content: out.System}, {Role: llm.User, Content: out.User}},
		Skill:    llm.SkillTranscribeEnhance, Timeout: r.m.cfg.LLMTimeout,
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("no answer within %v: %w", r.m.cfg.LLMTimeout, err)
		}
		return "", out.Profile(), err
	}
	return compose.Clean(resp.Text, raw), out.Profile(), nil
}

// finish delivers result (unless it is a try or empty), keeps the
// history entry and ends the session.
func (r *run) finish(st State, problem *Problem, result string) {
	cfg := r.m.cfg
	var dres deliver.Result
	if result != "" && !r.req.Try {
		r.m.update(r, func(s *Snapshot) { s.State, s.Result = Delivering, result })
		ctx, cancel := context.WithTimeout(context.Background(), cfg.DeliverTime)
		dres = deliver.Insert(ctx, cfg.Text, deliver.Request{Text: result, Target: r.req.Target, Self: cfg.Self, Activate: cfg.Activate})
		cancel()
		if dres.Err != nil {
			r.logf("warning", "insert: %v", dres.Err)
		}
		r.logf("info", "inserted %v (%s) in %v", dres.Inserted, dres.Reason, dres.Elapsed.Round(time.Millisecond))
	}
	snap := r.m.Current()
	total := r.now().Sub(snap.StartedAt)
	r.m.end(r, st, func(s *Snapshot) {
		s.Result, s.Problem = result, problem
		s.Inserted = dres.Inserted
		s.Card = result != "" && !r.req.Try && !dres.Inserted
		s.Delivery = dres.Reason
		s.Timings.Deliver, s.Timings.Total = dres.Elapsed, total
	}, func(s Snapshot) { r.keep(s, store.StatusDone) })
}

// fail ends the session failed before it recorded anything worth keeping.
func (r *run) fail(p *Problem) {
	r.cancel()
	r.logf("warning", "failed: %s: %v", p.Code, p.Err)
	r.m.end(r, Failed, func(s *Snapshot) { s.Problem = p }, nil)
}

// endFailed ends the session failed after it recorded: the entry keeps
// the recording when the settings say so.
func (r *run) endFailed(p *Problem, stream asr.Stream, more ...func(*Snapshot)) {
	if stream != nil {
		stream.Close()
	}
	r.cancel()
	r.logf("warning", "failed: %s: %v", p.Code, p.Err)
	total := r.now().Sub(r.m.Current().StartedAt)
	audio := asr.Duration(r.pcm.bytes(), asr.SampleRate)
	r.m.end(r, Failed, func(s *Snapshot) {
		s.Problem = p
		s.Timings.Total = total
		s.Timings.Audio = audio
		for _, fn := range more {
			fn(s)
		}
	}, func(s Snapshot) {
		if audio >= r.m.cfg.MinRecording {
			r.keep(s, store.StatusFailed)
		}
	})
}

func (r *run) endCancelled(stream asr.Stream) {
	if stream != nil {
		stream.Close()
	}
	r.cancel()
	r.logf("info", "cancelled")
	r.m.end(r, Cancelled, nil, nil)
}

// asrProblem classifies a speech service's error.
func (r *run) asrProblem(err error) *Problem {
	if r.cancelled.Load() {
		return &Problem{Code: ProblemInternal, Err: context.Canceled}
	}
	return &Problem{Code: ProblemASR, Err: err}
}

func (r *run) ctxErr(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &asr.Error{Kind: asr.KindNetwork, Detail: fmt.Sprintf("no result within %v", r.m.cfg.ASRTimeout), Retryable: true}
	}
	return &asr.Error{Kind: asr.KindCancelled, Detail: "cancelled"}
}

func (r *run) timeoutProblem(ctx context.Context) *Problem {
	return &Problem{Code: ProblemASR, Err: r.ctxErr(ctx)}
}

// keep writes the history entry of an ended session (and its recording),
// when the settings keep history. Not for tries.
func (r *run) keep(s Snapshot, status store.Status) {
	cfg := r.m.cfg
	if r.req.Try || cfg.History == nil || !r.settings.Privacy.KeepHistory {
		return
	}
	e := store.Entry{
		ID: r.id, Kind: r.req.Kind, CreatedAt: s.StartedAt, Status: status,
		AppBundle: s.Target.BundleID, AppName: s.Target.Name,
		RawText: s.Raw, Result: s.Result,
		ASRProvider: s.ASR.ID, ASRModel: s.ASR.Model, LLMProvider: s.LLM.ID, LLMModel: s.LLM.Model,
		AudioMS: s.Timings.Audio.Milliseconds(), ASRMS: s.Timings.ASR.Milliseconds(), LLMMS: s.Timings.LLM.Milliseconds(),
		TotalMS: s.Timings.Total.Milliseconds(),
	}
	if s.Timings.LLM == 0 {
		e.LLMProvider, e.LLMModel = "", ""
	}
	switch {
	case s.Inserted:
		e.Delivery = "insert"
	case s.Card:
		e.Delivery = "card"
	}
	if s.Problem != nil && s.Problem.Err != nil {
		e.Error = s.Problem.Err.Error()
	}
	if r.settings.Privacy.KeepAudio {
		if pcm := r.pcm.bytes(); len(pcm) > 0 {
			name := r.id + ".wav"
			dir := cfg.History.AudioDir()
			if err := os.MkdirAll(dir, 0o755); err == nil {
				if err := os.WriteFile(filepath.Join(dir, name), asr.WAV(pcm, asr.SampleRate), 0o644); err == nil {
					e.Audio = name
				} else {
					r.logf("warning", "keeping the recording: %v", err)
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cfg.History.Put(ctx, e); err != nil {
		r.logf("error", "history: %v", err)
	}
}

func isHelperErr(err error) bool { return errors.Is(err, core.ErrHelper) }
