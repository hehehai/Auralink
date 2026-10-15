// Package session is the one owner of a voice session (plan §1.1, §4.2):
// a state machine that takes a shortcut's press to a delivered result.
//
//	idle ─Start─▶ recording ─Stop─▶ recognizing ─▶ composing ─▶ delivering ─▶ done
//	                  │                  │             │             │
//	                  └──────────────────┴── Cancel ───┴─────────────┴──▶ cancelled
//	                  └──────────── an error with no way on ────────────▶ failed
//
// Every session has an id; its callbacks (audio frames, the speech
// service's partials, timers) carry it, and anything that arrives for a
// session that is not the current one, or after it ended, is dropped. A
// session ends once: done, cancelled or failed, with one history entry
// (when the settings keep history).
//
// While it records, the PCM goes to a streaming speech service as it
// comes (partial text for the overlay), or is kept for a file service,
// which gets it on Stop; either way the recording is kept in memory as
// the WAV a history entry may keep. After recognition one language-model
// request (internal/compose) cleans the text up, unless the feature has
// it off; if it fails or times out the raw text goes on. Then
// internal/deliver inserts it, or leaves it for the answer card.
//
// Everything platform is behind an interface: the microphone and the
// text I/O (the helper; helperio.go adapts the supervisor), the speech and
// language services (Resolver), the history and the dictionary (store),
// the clock. Tests drive the whole machine on Linux with fakes.
//
// Imports: asr, compose, deliver, llm, settings, store, helper/proto, lang.
package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/helper/proto"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/Auralink/internal/store"
)

// Kind is the feature of a session.
type Kind = store.Kind

// State is where a session is.
type State string

const (
	Idle        State = "idle" // no session yet
	Recording   State = "recording"
	Recognizing State = "recognizing"
	Composing   State = "composing"
	Delivering  State = "delivering"
	Done        State = "done"
	Cancelled   State = "cancelled"
	Failed      State = "failed"
)

// Live reports whether a session in state s has not ended.
func (s State) Live() bool {
	switch s {
	case Recording, Recognizing, Composing, Delivering:
		return true
	}
	return false
}

// Ended reports whether s is an end.
func (s State) Ended() bool { return s == Done || s == Cancelled || s == Failed }

// Problem codes.
const (
	// ProblemNoASR: no speech service is ready (Model services).
	ProblemNoASR = "noASR"
	// ProblemMicrophone: macOS refused the microphone.
	ProblemMicrophone = "microphone"
	// ProblemDevice: no input gave audio.
	ProblemDevice = "device"
	// ProblemHelper: the helper is not running (or died).
	ProblemHelper = "helper"
	// ProblemASR: the speech service failed (Err is an *asr.Error).
	ProblemASR = "asr"
	// ProblemLLM: the language model failed or timed out; the raw text
	// went on (Degraded).
	ProblemLLM = "llm"
	// ProblemNoSpeech: nothing was recognised.
	ProblemNoSpeech = "noSpeech"
	// ProblemTooShort: the recording was too short to send (the session
	// is cancelled quietly).
	ProblemTooShort = "tooShort"
	// ProblemUnsupported: the feature does not work yet (M4).
	ProblemUnsupported = "unsupported"
	// ProblemInternal: anything else.
	ProblemInternal = "internal"
)

// Problem is what went wrong in a session.
type Problem struct {
	Code string
	Err  error
	// Degraded: the session went on without the failed part.
	Degraded bool
}

// Model names a service a session used.
type Model struct {
	ID, Name, Model string
}

// Timings are a session's stage times.
type Timings struct {
	Audio, ASR, LLM, Deliver, Total time.Duration
}

// Bars is the number of level bars a snapshot keeps (the overlay's
// waveform).
const Bars = 16

// Snapshot is a session as the UI sees it: a copy, published at each
// change (Config.OnChange) with an increasing Rev.
type Snapshot struct {
	Rev   uint64
	ID    string
	Kind  Kind
	State State
	// Try: started from a page's Try area, the result stays there.
	Try bool
	// HoldBinding is the binding whose long press started the session
	// (its release stops it); "" for none.
	HoldBinding string
	// Target is the app in front when the shortcut was pressed.
	Target proto.App
	// StartedAt: the shortcut; HeardAt: the first audio (zero until then:
	// the user's first words are not heard yet); StoppedAt: Stop.
	StartedAt, HeardAt, StoppedAt time.Time
	// Levels are the last Bars input levels, 0–1, oldest first.
	Levels []float64
	// Device is the input that records, FellBack whether it was not the
	// first choice.
	Device   string
	FellBack bool
	// Partial is a streaming service's text so far; Raw the final
	// recognised text; Result what is delivered.
	Partial, Raw, Result string
	// Inserted: the result was pasted; Card: it waits in the answer card
	// (Delivery says why it was not inserted).
	Inserted bool
	Card     bool
	Delivery deliver.Reason
	// Profile is the composition's ("transcribe:cleanup+structure").
	Profile  string
	ASR, LLM Model
	Timings  Timings
	Problem  *Problem
}

// Elapsed is how long the recording has run (from the first audio), at
// now.
func (s Snapshot) Elapsed(now time.Time) time.Duration {
	if s.HeardAt.IsZero() {
		return 0
	}
	end := now
	if !s.StoppedAt.IsZero() {
		end = s.StoppedAt
	}
	if end.Before(s.HeardAt) {
		return 0
	}
	return end.Sub(s.HeardAt)
}

// Audio is the microphone: the helper's capture.start and capture.stop.
type Audio interface {
	// Record records as job id until Stop, the input's end or ctx's end
	// (which discards it), handing its events to fn in order, before it
	// returns.
	Record(ctx context.Context, id string, p proto.CaptureParams, fn func(AudioEvent)) (proto.CaptureResult, error)
	// Stop ends the recording id; Record returns before it does.
	Stop(ctx context.Context, id string) error
}

// AudioEvent is one notification of a recording: exactly one field set.
type AudioEvent struct {
	Started   *proto.CaptureStartedParams
	Level     *proto.LevelParams
	PCM       *proto.PCMParams
	Restarted *proto.RestartedParams
}

// LLM is a language model (llm.Client, llm.Router's skill).
type LLM interface {
	Chat(ctx context.Context, req llm.Request) (llm.Response, error)
}

// Services are the speech and language services of one session.
type Services struct {
	ASR     asr.Provider
	ASRInfo Model
	// LLM is nil when the feature has no language model ready.
	LLM     LLM
	LLMInfo Model
}

// Resolver builds the services a session of kind uses with settings s,
// reading their keys; it may block. It fails with an error wrapping
// ErrNoASR when no speech service is ready.
type Resolver func(ctx context.Context, kind Kind, s settings.Settings) (Services, error)

// ErrNoASR: no speech service is ready.
var ErrNoASR = errors.New("session: no speech service is ready")

// ErrBusy: a session is live already.
var ErrBusy = errors.New("session: a session is running")

// Config is what a Manager needs. Audio, Text and Resolve are required.
type Config struct {
	Audio   Audio
	Text    deliver.TextIO
	Resolve Resolver
	// History keeps finished sessions (nil: none); Glossary lists the
	// dictionary's enabled terms (nil: none).
	History  store.History
	Glossary func(ctx context.Context) ([]store.Term, error)
	// Settings returns the current settings (memory only); Locale the UI
	// language, for a main language left to follow it.
	Settings func() settings.Settings
	Locale   func() string
	// Self is Auralink's pid and Activate brings an app back to the front
	// (deliver.Request).
	Self     int
	Activate func(pid int) bool
	// OnChange gets every snapshot, on the session's goroutines; it must
	// not block (post it to the UI). Snapshots may arrive out of order:
	// keep the highest Rev.
	OnChange func(Snapshot)
	// Log writes a line about a session: never its text unless the
	// settings allow it (Privacy.LogText).
	Log func(level, msg string)
	// Now is the clock (nil: time.Now); NewID makes session ids.
	Now   func() time.Time
	NewID func() string

	// Limits (0: the defaults).
	MaxRecording time.Duration // DefaultMaxRecording
	MinRecording time.Duration // DefaultMinRecording
	ASRTimeout   time.Duration // after Stop; DefaultASRTimeout
	LLMTimeout   time.Duration // DefaultLLMTimeout
	DeliverTime  time.Duration // DefaultDeliverTimeout
	// StopGrace is how long Stop waits for the recording to end before
	// discarding the capture job (a capture.stop that raced its start);
	// DefaultStopGrace.
	StopGrace time.Duration
}

// Defaults of Config's limits.
const (
	DefaultMaxRecording   = 10 * time.Minute
	DefaultMinRecording   = 300 * time.Millisecond
	DefaultASRTimeout     = 30 * time.Second
	DefaultLLMTimeout     = 8 * time.Second
	DefaultDeliverTimeout = 5 * time.Second
	DefaultStopGrace      = 3 * time.Second
)

// StartRequest starts a session.
type StartRequest struct {
	Kind Kind
	// Target is the app in front (hotkey.event's front, else the last
	// app.activated).
	Target proto.App
	// HoldBinding: a long press of this binding started it.
	HoldBinding string
	// Try: from a page's Try area; nothing is inserted and nothing kept.
	Try bool
	// At is when the shortcut fired (zero: now).
	At time.Time
}

// Manager runs one session at a time. Goroutine-safe.
type Manager struct {
	cfg Config

	mu   sync.Mutex
	cur  *run
	snap Snapshot // the current (or last) session's
	rev  uint64
	last string // the last result delivered or shown, for "paste the last result"
}

// New returns a Manager; nothing runs until Start.
func New(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Settings == nil {
		cfg.Settings = settings.Defaults
	}
	if cfg.Locale == nil {
		cfg.Locale = func() string { return "en" }
	}
	if cfg.Log == nil {
		cfg.Log = func(string, string) {}
	}
	if cfg.NewID == nil {
		var n int
		var mu sync.Mutex
		cfg.NewID = func() string { mu.Lock(); defer mu.Unlock(); n++; return "s" + itoa(n) }
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.MaxRecording, DefaultMaxRecording)
	def(&cfg.MinRecording, DefaultMinRecording)
	def(&cfg.ASRTimeout, DefaultASRTimeout)
	def(&cfg.LLMTimeout, DefaultLLMTimeout)
	def(&cfg.DeliverTime, DefaultDeliverTimeout)
	def(&cfg.StopGrace, DefaultStopGrace)
	return &Manager{cfg: cfg, snap: Snapshot{State: Idle}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Current is the current session's snapshot (or the last one's, ended;
// Idle before the first).
func (m *Manager) Current() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap.clone()
}

// Last is the last result a session delivered or showed ("" for none):
// what "paste the last result" pastes.
func (m *Manager) Last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// SetLast remembers a result as the last one (the app reads the newest
// history entry at launch).
func (m *Manager) SetLast(text string) {
	m.mu.Lock()
	if m.last == "" {
		m.last = text
	}
	m.mu.Unlock()
}

// Start begins a session: it records at once, and resolves the services
// meanwhile. It fails with ErrBusy while a session is live.
func (m *Manager) Start(req StartRequest) (string, error) {
	m.mu.Lock()
	if m.cur != nil && m.snap.State.Live() {
		m.mu.Unlock()
		return "", ErrBusy
	}
	at := req.At
	if at.IsZero() {
		at = m.cfg.Now()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		m: m, id: m.cfg.NewID(), req: req, settings: m.cfg.Settings(), ctx: ctx, cancel: cancel,
		stop: make(chan struct{}), pcm: newPCMBuffer(),
	}
	m.cur = r
	m.rev++
	m.snap = Snapshot{
		Rev: m.rev, ID: r.id, Kind: req.Kind, State: Recording, Try: req.Try, HoldBinding: req.HoldBinding,
		Target: req.Target, StartedAt: at,
	}
	s := m.snap.clone()
	m.mu.Unlock()
	m.publish(s)
	go r.main()
	return r.id, nil
}

// Stop ends the recording of session id; it goes on to recognition. A
// session that is not recording, or not the current one, is left alone.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	r := m.cur
	ok := r != nil && r.id == id && m.snap.State == Recording
	m.mu.Unlock()
	if ok {
		r.stopOnce.Do(func() { close(r.stop) })
	}
}

// Cancel ends session id where it is: nothing more is delivered or kept.
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	r := m.cur
	ok := r != nil && r.id == id && m.snap.State.Live()
	m.mu.Unlock()
	if ok {
		r.cancelled.Store(true)
		r.cancel()
	}
}

// update changes the snapshot of run r, if r is still the current,
// live session, and publishes it. It reports whether it did.
func (m *Manager) update(r *run, fn func(*Snapshot)) bool {
	m.mu.Lock()
	if m.cur != r || r.ended {
		m.mu.Unlock()
		return false
	}
	fn(&m.snap)
	m.rev++
	m.snap.Rev = m.rev
	s := m.snap.clone()
	m.mu.Unlock()
	m.publish(s)
	return true
}

// end makes r's session end in state st (once). Before the end is
// published, kept (when not nil) gets the final snapshot: the history
// entry exists by the time the UI sees the end.
func (m *Manager) end(r *run, st State, fn func(*Snapshot), kept func(Snapshot)) bool {
	m.mu.Lock()
	if m.cur != r || r.ended {
		m.mu.Unlock()
		return false
	}
	if fn != nil {
		fn(&m.snap)
	}
	m.snap.State = st
	r.ended = true
	if st == Done && m.snap.Result != "" && !m.snap.Try {
		m.last = m.snap.Result
	}
	m.rev++
	m.snap.Rev = m.rev
	s := m.snap.clone()
	m.mu.Unlock()
	if kept != nil {
		kept(s)
	}
	m.publish(s)
	return true
}

func (m *Manager) publish(s Snapshot) {
	if m.cfg.OnChange != nil {
		m.cfg.OnChange(s)
	}
}

func (s Snapshot) clone() Snapshot {
	if s.Levels != nil {
		s.Levels = append([]float64(nil), s.Levels...)
	}
	if s.Problem != nil {
		p := *s.Problem
		s.Problem = &p
	}
	return s
}

// Level is a dBFS level as a bar height, 0–1 (−60 dBFS and below is 0).
func Level(db float64) float64 {
	v := (db + 60) / 60
	return min(max(v, 0), 1)
}

// ProblemOf classifies an error of a session.
func ProblemOf(err error) *Problem {
	if err == nil {
		return nil
	}
	p := &Problem{Code: ProblemInternal, Err: err}
	var pe *proto.Error
	var ae *asr.Error
	switch {
	case errors.Is(err, ErrNoASR):
		p.Code = ProblemNoASR
	case errors.As(err, &pe):
		switch pe.Code {
		case proto.CodePermissionDenied:
			p.Code = ProblemMicrophone
		case proto.CodeDeviceUnavailable:
			p.Code = ProblemDevice
		case proto.CodeBusy:
			p.Code = ProblemDevice
		}
	case errors.As(err, &ae):
		p.Code = ProblemASR
	case isHelperErr(err):
		p.Code = ProblemHelper
	}
	return p
}
