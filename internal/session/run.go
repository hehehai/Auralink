package session

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	"github.com/hehehai/Auralink/internal/dict"
	"github.com/hehehai/Auralink/internal/dsp"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
	"github.com/hehehai/Auralink/internal/vad"
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
	// svcDone gets the services, resolved meanwhile; resolved once r.svc
	// holds them.
	svcDone  chan svcResult
	resolved bool
	sc       sceneState // app enhancement (scene.go)
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

// streamEnd is a stream's last event: its final result or its error;
// silent when the recording ended with no speech, so nothing was sent
// (the gate held it all) and no result will come.
type streamEnd struct {
	res    asr.Result
	err    error
	silent bool
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

// firstState is where a session starts: composing with its text,
// reading the selection (Translate), else recording.
func (r *run) firstState() State {
	switch {
	case r.req.Text != nil:
		return Composing
	case r.req.Kind == store.KindTranslate && r.canSelect():
		return Reading
	}
	return Recording
}

// canSelect says whether the session reads the selection: Translate and
// Rewrite with a selector, not a try, not a follow-up, in an app that is
// not Auralink.
func (r *run) canSelect() bool {
	cfg := r.m.cfg
	t := r.req.Target
	return cfg.Select != nil && !r.req.Try && len(r.req.Previous) == 0 && t.PID != 0 && (cfg.Self == 0 || t.PID != cfg.Self) &&
		(r.req.Kind == store.KindTranslate || r.req.Kind == store.KindRewrite)
}

// main is the session's goroutine: the selection, recording,
// recognition, composition, delivery, history.
func (r *run) main() {
	defer r.cancel()
	cfg := r.m.cfg
	kind := r.req.Kind
	switch kind {
	case store.KindTranscribe, store.KindTranslate, store.KindRewrite:
	default:
		r.fail(&Problem{Code: ProblemUnsupported, Err: fmt.Errorf("unknown feature %q", kind)})
		return
	}
	r.logf("info", "start %s in %s", kind, r.req.Target.BundleID)

	// The services and the dictionary, meanwhile.
	r.svcDone = make(chan svcResult, 1)
	go func() {
		svc, err := cfg.Resolve(r.ctx, kind, r.settings)
		var terms []store.Term
		if err == nil && r.settings.Dictionary.Enabled && cfg.Glossary != nil {
			terms, _ = cfg.Glossary(r.ctx)
		}
		r.svcDone <- svcResult{svc, terms, err}
	}()

	if t := r.req.Text; t != nil {
		if r.awaitServices() {
			r.write(strings.TrimSpace(t.Raw), t.Selection)
		}
		return
	}
	var selDone chan string
	switch {
	case kind == store.KindTranslate && r.canSelect():
		sel := r.readSelection()
		if r.ctx.Err() != nil {
			r.endCancelled(nil)
			return
		}
		if sel != "" {
			if r.awaitServices() {
				r.write("", sel)
			}
			return
		}
		r.m.update(r, func(s *Snapshot) { s.State = Recording })
	case kind == store.KindRewrite && r.canSelect():
		selDone = make(chan string, 1)
		go func() { selDone <- r.readSelection() }()
	}

	raw, ok := r.listen()
	if !ok {
		return
	}
	switch kind {
	case store.KindTranscribe:
		r.transcribe(raw)
	case store.KindTranslate:
		r.write(raw, "")
	case store.KindRewrite:
		sel := ""
		if selDone != nil {
			select {
			case sel = <-selDone:
			case <-r.ctx.Done():
				r.endCancelled(nil)
				return
			}
		}
		r.write(raw, sel)
	}
}

// awaitServices waits for the services; false when the session ended
// (cancelled, or the services failed).
func (r *run) awaitServices() bool {
	if r.resolved {
		return true
	}
	select {
	case s := <-r.svcDone:
		if s.err != nil {
			r.fail(ProblemOf(s.err))
			return false
		}
		r.useServices(s)
		return true
	case <-r.ctx.Done():
		r.endCancelled(nil)
		return false
	}
}

// useServices keeps the resolved services.
func (r *run) useServices(s svcResult) {
	r.resolved, r.svc, r.terms = true, s.svc, s.terms
	r.m.update(r, func(sn *Snapshot) { sn.ASR, sn.LLM = s.svc.ASRInfo, s.svc.LLMInfo })
}

// needs reports what the services lack for the session: a speech
// service, or the language model Translate and Rewrite need.
func (r *run) lacks() *Problem {
	if r.svc.ASR == nil {
		return ProblemOf(fmt.Errorf("%s: %w", r.req.Kind, ErrNoASR))
	}
	if r.svc.LLM == nil && r.req.Kind != store.KindTranscribe {
		return &Problem{Code: ProblemNoLLM, Err: errors.New("no language model is ready")}
	}
	return nil
}

// readSelection reads the selected text of the app in front (Translate,
// Rewrite) and puts it in the snapshot; "" for none, or when it could
// not be read (the session goes on without one).
func (r *run) readSelection() string {
	cfg := r.m.cfg
	ctx, cancel := context.WithTimeout(r.ctx, cfg.ReadTimeout)
	defer cancel()
	res, err := cfg.Select.Selection(ctx, proto.SelectionParams{AllowCopy: AllowCopy(r.req.Target.BundleID)})
	if err != nil {
		if r.ctx.Err() == nil {
			r.logf("warning", "reading the selection: %v", err)
		}
		return ""
	}
	text := res.Text
	if strings.TrimSpace(text) == "" {
		r.logf("info", "no selection (%s, %.0f ms)", res.Via, res.MS)
		return ""
	}
	r.logf("info", "selection: %s via %s in %.0f ms", r.logText(text), res.Via, res.MS)
	r.m.update(r, func(s *Snapshot) { s.Selection, s.SelectionVia = text, res.Via })
	return text
}

// listen records and recognises: the recognised text, or false when the
// session ended (cancelled, failed, too short, nothing heard).
func (r *run) listen() (string, bool) {
	cfg := r.m.cfg
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
	svcDone := r.svcDone
	if r.resolved {
		svcDone = nil
	}
	open := func() chan openResult {
		st, ok := r.streamer()
		if !ok {
			return nil
		}
		ch := make(chan openResult, 1)
		go func() {
			s, err := st.Open(r.ctx, asr.StreamRequest{SampleRate: asr.SampleRate, Hints: r.hints()})
			ch <- openResult{s, err}
		}()
		return ch
	}

	var (
		openDone   chan openResult
		stream     asr.Stream
		finals     chan streamEnd
		early      *streamEnd
		stopCh     = r.stop
		stopTimer  <-chan time.Time
		captured   capResult
		recordDone bool
	)
	if r.resolved {
		if p := r.lacks(); p != nil {
			r.fail(p)
			return "", false
		}
		openDone = open()
	}
	for !recordDone {
		select {
		case <-r.ctx.Done():
			r.endCancelled(stream)
			return "", false
		case s := <-svcDone:
			svcDone = nil
			if s.err != nil {
				r.fail(ProblemOf(s.err))
				return "", false
			}
			r.useServices(s)
			if p := r.lacks(); p != nil {
				r.fail(p)
				return "", false
			}
			openDone = open()
		case o := <-openDone:
			openDone = nil
			if o.err != nil {
				r.fail(r.asrProblem(o.err))
				return "", false
			}
			stream = o.st
			finals = r.feed(stream)
		case e := <-finals:
			finals = nil
			if e.err != nil {
				r.endFailed(r.asrProblem(e.err), stream)
				return "", false
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
		return "", false
	}
	stopped := stopCh == nil
	if captured.err != nil && !(stopped && errors.Is(captured.err, context.Canceled)) && !isCancelled(captured.err, stopped) {
		r.endFailed(ProblemOf(captured.err), stream)
		return "", false
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
		return "", false
	}
	asrStart := r.now()

	// The services, if they are still on their way.
	if !r.resolved {
		if !r.awaitServices() {
			return "", false
		}
		if p := r.lacks(); p != nil {
			r.fail(p)
			return "", false
		}
		openDone = open()
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
					return "", false
				}
				stream = o.st
				finals = r.feed(stream)
			case <-actx.Done():
				r.endFailed(r.timeoutProblem(actx), nil)
				return "", false
			}
		}
		silent := false
		switch {
		case early != nil:
			res, silent = early.res, early.silent
		default:
			select {
			case e := <-finals:
				res, err, silent = e.res, e.err, e.silent
			case <-actx.Done():
				err = r.ctxErr(actx)
			}
		}
		stream.Close()
		if silent {
			// The gate held the whole recording: no speech, nothing sent.
			r.logf("info", "no speech in %v: nothing sent to %s", audioLen.Round(time.Millisecond), r.svc.ASRInfo.ID)
			r.m.update(r, func(s *Snapshot) { s.Timings.Sent = 0 })
			r.finish(Done, &Problem{Code: ProblemNoSpeech}, "", finishOpts{})
			return "", false
		}
	} else if ft, ok := r.svc.ASR.(asr.FileTranscriber); ok {
		upload, speech := r.prepare(pcm)
		if !speech {
			r.logf("info", "no speech in %v: not sent to %s", audioLen.Round(time.Millisecond), r.svc.ASRInfo.ID)
			r.m.update(r, func(s *Snapshot) { s.Timings.Sent = 0 })
			r.finish(Done, &Problem{Code: ProblemNoSpeech}, "", finishOpts{})
			return "", false
		}
		sent := asr.Duration(upload, asr.SampleRate)
		r.m.update(r, func(s *Snapshot) { s.Timings.Sent = sent })
		res, err = ft.Transcribe(actx, asr.FileRequest{Audio: upload, Format: "pcm", SampleRate: asr.SampleRate, Hints: r.hints()})
		if err != nil && actx.Err() != nil {
			err = r.ctxErr(actx)
		}
	} else {
		err = &asr.Error{Kind: asr.KindConfig, Detail: "the speech service neither uploads nor streams"}
	}
	if r.cancelled.Load() {
		r.endCancelled(nil)
		return "", false
	}
	asrTime := r.now().Sub(asrStart)
	if err != nil {
		// The error itself follows in endFailed's line.
		r.logf("warning", "speech service %s (model %s) failed after %v", r.svc.ASRInfo.ID, r.svc.ASRInfo.Model, asrTime.Round(time.Millisecond))
		r.endFailed(r.asrProblem(err), nil, func(s *Snapshot) { s.Timings.ASR = asrTime })
		return "", false
	}
	raw := strings.TrimSpace(res.Text)
	r.logf("info", "recognised %s in %v", r.logText(raw), asrTime.Round(time.Millisecond))
	r.m.update(r, func(s *Snapshot) { s.Raw, s.Partial, s.Timings.ASR = raw, raw, asrTime })
	if raw == "" {
		r.finish(Done, &Problem{Code: ProblemNoSpeech}, "", finishOpts{})
		return "", false
	}
	return raw, true
}

// transcribe cleans up raw with one language-model request (unless the
// feature has it off or no model is ready), and delivers it; the raw
// text goes on when the model fails.
func (r *run) transcribe(raw string) {
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
			r.logf("warning", "language model %s (model %s) failed after %v, the text goes on as recognised: %v", r.svc.LLMInfo.ID, r.svc.LLMInfo.Model, llmTime.Round(time.Millisecond), err)
			problem = &Problem{Code: ProblemLLM, Err: err, Degraded: true}
		} else {
			r.logf("info", "composed %s in %v", r.logText(text), llmTime.Round(time.Millisecond))
			result = text
			if result == "" {
				r.finish(Done, &Problem{Code: ProblemNoSpeech}, "", finishOpts{})
				return
			}
		}
	}
	r.finish(Done, problem, result, finishOpts{})
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

// hints are the dictionary's terms for the speech service (dict.Hints:
// the enabled ones, the most recently changed first).
func (r *run) hints() []string {
	if !r.settings.Dictionary.Enabled || !r.settings.Dictionary.ASRHints {
		return nil
	}
	return dict.Hints(r.terms)
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
	// Two may come (the sender's silent end, the reader's): the first
	// counts, the other must not block.
	end := make(chan streamEnd, 2)
	sendErr := make(chan error, 1)
	gate := r.gate()
	hp, lev := r.streamDSP()
	go func() {
		off, sent := 0, 0
		for {
			chunk, closed, err := r.pcm.next(r.ctx, off)
			if err != nil {
				return
			}
			if len(chunk) > 0 {
				off += len(chunk)
				// The recording is kept as it came; the stages make copies.
				raw := chunk
				if hp != nil {
					chunk = hp.Process(chunk)
				}
				if gate != nil {
					if hp != nil {
						gate.Hear(raw) // the filter never holds back more
					}
					chunk = gate.Push(chunk)
				}
				if len(chunk) == 0 {
					continue
				}
				if lev != nil {
					chunk = lev.Process(chunk)
				}
				if err := st.Send(chunk); err != nil {
					sendErr <- err
					return
				}
				sent += len(chunk)
				continue
			}
			if closed {
				d := pcmDuration(sent)
				r.m.update(r, func(s *Snapshot) { s.Timings.Sent = d })
				if sent > 0 && (hp != nil || lev != nil) {
					rep := dsp.Report{SpeechDB: -120, FloorDB: -120}
					if lev != nil {
						rep = lev.Report()
					}
					if hp != nil {
						rep.HighPass, rep.Clipped = true, hp.Clipped
					}
					r.noteDSP(rep)
				}
				if gate != nil {
					if !gate.Open() {
						end <- streamEnd{silent: true}
						return
					}
					if gate.Dropped() > 0 {
						r.logf("info", "voice activity: held back %v of silence before the speech", pcmDuration(gate.Dropped()).Round(10*time.Millisecond))
					}
				}
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

// vadParams are the local voice activity detection's parameters, and
// false when the settings turn it off (settings.VAD).
func (r *run) vadParams() (vad.Params, bool) {
	v := r.settings.Audio.VAD
	if v.Off {
		return vad.Params{}, false
	}
	return vad.Params{
		OnsetDB: v.OnsetDB, OffsetDB: v.OffsetDB, MinLevelDB: v.MinLevelDB, HangoverMS: v.HangoverMS,
		LeadMS: v.LeadMS, TailMS: v.TailMS, LongGapMS: v.LongGapMS, GapKeepMS: v.GapKeepMS,
		PreRollMS: v.PreRollMS, MaxHoldMS: v.MaxHoldMS,
	}.Resolved(), true
}

// dspParams are the sound processing's parameters (settings.Clean), and
// false when the settings turn it off.
func (r *run) dspParams() (dsp.Params, bool) {
	c := r.settings.Audio.Clean
	if c.Off || c.HighPassOff && c.LevelOff {
		return dsp.Params{}, false
	}
	return dsp.Params{
		HighPassOff: c.HighPassOff, LevelOff: c.LevelOff, HighPassHz: c.HighPassHz,
		TargetDB: c.TargetDB, MaxGainDB: c.MaxGainDB, LoudDB: c.LoudDB, MaxCutDB: c.MaxCutDB,
		NoiseCeilDB: c.NoiseCeilDB, CeilingDB: c.CeilingDB,
	}.Resolved(), true
}

// prepare is what a file service gets of the recording pcm: processed
// (internal/dsp: the high-pass filter, one gain from the speech, the
// limiter) and without its leading and trailing silence, long pauses
// shortened (internal/vad), each when the settings have it on; false when
// the detection finds no speech at all (nothing is sent). The detection
// runs on the filtered recording and on the original, and keeps the
// speech either finds. pcm itself is not changed: the history keeps it.
func (r *run) prepare(pcm []byte) ([]byte, bool) {
	p, vadOn := r.vadParams()
	dp, dspOn := r.dspParams()
	upload := pcm
	var an vad.Analysis
	switch {
	case dspOn:
		f := dsp.ProcessFile(pcm, asr.SampleRate, dp, p)
		upload, an = f.PCM, f.Analysis
		r.noteDSP(f.Report)
	case vadOn:
		an = vad.Analyze(pcm, asr.SampleRate, p)
	}
	if !vadOn {
		return upload, true
	}
	if !an.Speech {
		r.logf("info", "voice activity: no speech (noise floor %.0f dBFS, loudest %.0f dBFS)", an.FloorDB, an.PeakDB)
		return nil, false
	}
	c := vad.Compact(upload, asr.SampleRate, an, p)
	if c.Changed {
		r.logf("info", "voice activity: %d segments, sending %v of %v", len(an.Segments), c.Kept.Round(10*time.Millisecond), c.Original.Round(10*time.Millisecond))
	}
	return c.PCM, true
}

// noteDSP logs what the sound processing did and puts it in the snapshot
// (and so in the history entry).
func (r *run) noteDSP(rep dsp.Report) {
	var stages []string
	if rep.HighPass {
		stages = append(stages, "highpass")
	}
	if rep.Level {
		stages = append(stages, "level")
	}
	desc := strings.Join(stages, "+")
	speech := "no speech"
	if rep.SpeechDB > -120 {
		speech = fmt.Sprintf("speech %.1f dBFS", rep.SpeechDB)
	}
	extra := ""
	if rep.MinGainDB != rep.MaxGainDB {
		extra = fmt.Sprintf(" (%+.1f…%+.1f)", rep.MinGainDB, rep.MaxGainDB)
	}
	r.logf("info", "sound: %s, %s over a floor of %.0f dBFS, gain %+.1f dB%s, %d samples limited, %d clipped by the filter",
		desc, speech, rep.FloorDB, rep.GainDB, extra, rep.Limited, rep.Clipped)
	gain := math.Round(rep.GainDB*10) / 10
	r.m.update(r, func(s *Snapshot) { s.DSP, s.GainDB = desc, gain })
}

// streamDSP are the stages a streaming service's audio goes through: the
// high-pass filter before the gate, the level stage after it; nil when
// the settings turn them off.
func (r *run) streamDSP() (*dsp.HighPass, *dsp.Leveler) {
	dp, ok := r.dspParams()
	if !ok {
		return nil, nil
	}
	var hp *dsp.HighPass
	var lev *dsp.Leveler
	if !dp.HighPassOff {
		hp = dsp.NewHighPass(asr.SampleRate, dp.HighPassHz)
	}
	if !dp.LevelOff {
		vp, _ := r.vadParams()
		lev = dsp.NewLeveler(asr.SampleRate, dp, vp)
	}
	return hp, lev
}

// pcmDuration is how long n bytes of the recording's PCM play.
func pcmDuration(n int) time.Duration {
	return time.Duration(n/2) * time.Second / asr.SampleRate
}

// gate is the gate a streaming service's audio goes through (the
// leading silence held back), nil when the detection is off.
func (r *run) gate() *vad.Gate {
	p, ok := r.vadParams()
	if !ok {
		return nil
	}
	return vad.NewGate(asr.SampleRate, p)
}

// compose runs the language model over raw.
func (r *run) compose(raw string) (string, string, error) {
	f := r.settings.Features.Transcribe
	in := compose.Input{
		Task: compose.Transcribe, Cleanup: f.Cleanup, Structure: f.Structure, TaskPrompt: f.Prompt,
		MainLanguage: MainLanguage(r.settings, r.m.cfg.Locale()), OtherLanguages: r.settings.General.OtherLanguages, Text: raw,
		Glossary: r.glossary(),
		Scene:    r.composeScene(),
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

// glossary is the dictionary's enabled terms for the prompt, when the
// settings send them.
func (r *run) glossary() []compose.Term {
	if !r.settings.Dictionary.Enabled || !r.settings.Dictionary.Glossary {
		return nil
	}
	var out []compose.Term
	for _, t := range r.terms {
		if t.Enabled {
			out = append(out, compose.Term{Term: t.Term, Aliases: t.Aliases})
		}
	}
	return out
}

// finishOpts say how a result is delivered: card, when set, sends it to
// the answer card without trying to insert it (the reason it shows
// there); needEditable inserts only into an element Accessibility calls
// editable (a selection).
type finishOpts struct {
	card         deliver.Reason
	needEditable bool
}

// finish delivers result (unless it is a try or empty), keeps the
// history entry and ends the session.
func (r *run) finish(st State, problem *Problem, result string, o finishOpts) {
	cfg := r.m.cfg
	var dres deliver.Result
	switch {
	case result == "" || r.req.Try:
	case o.card != "":
		dres.Reason = o.card
		r.m.update(r, func(s *Snapshot) { s.Result, s.Card = result, true })
	default:
		r.m.update(r, func(s *Snapshot) { s.State, s.Result = Delivering, result })
		press := r.press(o.needEditable) // app enhancement's auto key
		ctx, cancel := context.WithTimeout(context.Background(), cfg.DeliverTime)
		dres = deliver.Insert(ctx, cfg.Text, deliver.Request{Text: result, Target: r.req.Target, Press: press, Self: cfg.Self, Activate: cfg.Activate, NeedEditable: o.needEditable})
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
		RawText: s.Raw, Result: s.Result, SourceText: s.Selection, TargetLang: s.TargetLanguage,
		ASRProvider: s.ASR.ID, ASRModel: s.ASR.Model, LLMProvider: s.LLM.ID, LLMModel: s.LLM.Model,
		AudioMS: s.Timings.Audio.Milliseconds(), SentMS: s.Timings.Sent.Milliseconds(), ASRMS: s.Timings.ASR.Milliseconds(), LLMMS: s.Timings.LLM.Milliseconds(),
		TotalMS: s.Timings.Total.Milliseconds(), DSP: s.DSP, GainDB: s.GainDB,
	}
	if r.req.Kind == store.KindRewrite {
		e.PromptText, e.RawText = s.Raw, "" // the spoken instruction
	}
	if s.Timings.LLM == 0 {
		e.LLMProvider, e.LLMModel = "", ""
	}
	if r.req.Text != nil || s.Timings.Audio == 0 {
		e.ASRProvider, e.ASRModel = "", "" // nothing was recognised
	}
	if m, ok := r.knownScene(); ok {
		e.URLHost = m.Host
		if m.Matched() {
			e.RuleID, e.RuleName, e.RuleSource = m.Group.ID, m.Group.Name, string(m.Source)
		}
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
