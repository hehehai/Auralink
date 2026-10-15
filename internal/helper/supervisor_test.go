package helper

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper/helpertest"
	"github.com/hehehai/Auralink/internal/helper/proto"
)

// The test binary is also the fake helper (helpertest).
func TestMain(m *testing.M) {
	if helpertest.Enabled() {
		helpertest.Main()
	}
	os.Exit(m.Run())
}

// harness is a supervisor over the fake helper, recording what it
// publishes and the notifications that belong to no request.
type harness struct {
	sup      Supervisor
	mu       sync.Mutex
	statuses []Status
	logs     []core.LogEntry
	notes    []proto.Message
}

func start(t *testing.T, opts string, tweak ...func(*Config)) *harness {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{}
	bus := core.NewBus()
	core.On(bus, func(ev StatusChanged) { h.mu.Lock(); h.statuses = append(h.statuses, ev.Status); h.mu.Unlock() })
	cfg := Config{
		Bin: self, AppVersion: "test", LogDir: t.TempDir(), Bus: bus, Env: helpertest.Env(opts),
		Log:      func(e core.LogEntry) { h.mu.Lock(); h.logs = append(h.logs, e); h.mu.Unlock() },
		OnNotify: func(m proto.Message) { h.mu.Lock(); h.notes = append(h.notes, m); h.mu.Unlock() },
		Logf:     t.Logf, HelloTimeout: 2 * time.Second, CancelGrace: 500 * time.Millisecond,
	}
	for _, fn := range tweak {
		fn(&cfg)
	}
	h.sup = New(cfg)
	t.Cleanup(h.sup.Stop)
	h.sup.Start(context.Background())
	return h
}

// wait polls cond for up to 5 s.
func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) ready(t *testing.T) Status {
	t.Helper()
	wait(t, "the helper to be ready", func() bool { return h.sup.Status().State == Ready })
	return h.sup.Status()
}

func request[T any](t *testing.T, h *harness, method string, params any) (T, error) {
	t.Helper()
	var out T
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := h.sup.Call(ctx, "", method, params, nil)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func TestHandshakeAndControl(t *testing.T) {
	h := start(t, "")
	st := h.ready(t)
	if st.Hello == nil || st.Hello.Version != "fake" || st.PID == 0 || !st.Has(proto.MethodBrowserURL) || st.Has("tts.synthesize") {
		t.Fatalf("status %+v", st)
	}
	devs, err := request[proto.DevicesResult](t, h, proto.MethodCaptureDevices, nil)
	if err != nil || len(devs.Devices) != 2 || !devs.Devices[0].Default {
		t.Fatalf("devices %+v %v", devs, err)
	}
	u, err := request[proto.BrowserURLResult](t, h, proto.MethodBrowserURL, proto.BrowserURLParams{BundleID: "com.google.Chrome"})
	if err != nil || !strings.HasPrefix(u.URL, "https://mail.google.com/") {
		t.Errorf("url %+v %v", u, err)
	}
	_, err = request[proto.BrowserURLResult](t, h, proto.MethodBrowserURL, proto.BrowserURLParams{BundleID: "org.mozilla.firefox"})
	if !errors.Is(err, &proto.Error{Code: proto.CodeUnsupported}) {
		t.Errorf("firefox: %v", err)
	}
	probe, err := request[proto.AutomationProbeResult](t, h, proto.MethodAutomationProbe, proto.AutomationProbeParams{BundleIDs: []string{"com.apple.Safari", "com.brave.Browser"}, Prompt: true})
	if err != nil || len(probe.Browsers) != 2 || probe.Browsers[0].State != proto.AutomationGranted || probe.Browsers[1].State != proto.AutomationDenied {
		t.Errorf("probe %+v %v", probe, err)
	}
	// The fake's log line and stderr reach the log, as source "helper".
	wait(t, "log lines", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return slices.ContainsFunc(h.logs, func(e core.LogEntry) bool { return e.Message == "fake helper ready" && e.Source == LogSource }) &&
			slices.ContainsFunc(h.logs, func(e core.LogEntry) bool { return e.Message == "fake helper starting" })
	})
}

func TestCaptureStreamsPCM(t *testing.T) {
	h := start(t, "frame-ms=10")
	h.ready(t)
	out := filepath.Join(t.TempDir(), "rec.wav")
	var mu sync.Mutex
	var pcm []byte
	levels, seqs := 0, []int{}
	type res struct {
		r   proto.CaptureResult
		err error
	}
	done := make(chan res, 1)
	go func() {
		raw, err := h.sup.Call(context.Background(), "S1", proto.MethodCaptureStart,
			proto.CaptureParams{SampleRate: 16000, Channels: 1, Encoding: "pcm16le", Output: out}, func(m proto.Message) {
				mu.Lock()
				defer mu.Unlock()
				switch m.Method {
				case proto.MethodCaptureLevel:
					levels++
				case proto.MethodCapturePCM:
					var p proto.PCMParams
					m.DecodeParams(&p)
					seqs = append(seqs, p.Seq)
					pcm = append(pcm, p.Data...)
				}
			})
		var r proto.CaptureResult
		if err == nil {
			err = json.Unmarshal(raw, &r)
		}
		done <- res{r, err}
	}()
	wait(t, "five frames", func() bool { mu.Lock(); defer mu.Unlock(); return len(seqs) >= 5 })
	if st := h.sup.Status(); !slices.Equal(st.Running, []string{"S1"}) {
		t.Errorf("running %v", st.Running)
	}
	// A second recording is refused while one runs.
	if _, err := request[proto.CaptureResult](t, h, proto.MethodCaptureStart, proto.CaptureParams{SampleRate: 16000, Channels: 1, Encoding: "pcm16le"}); !errors.Is(err, &proto.Error{Code: proto.CodeBusy}) {
		t.Errorf("second capture: %v", err)
	}
	stop, err := request[proto.CaptureStopResult](t, h, proto.MethodCaptureStop, proto.CaptureStopParams{Job: "S1"})
	if err != nil || !stop.Stopped {
		t.Fatalf("stop %+v %v", stop, err)
	}
	r := <-done
	if r.err != nil || r.r.Reason != proto.CaptureStopped || r.r.Frames < 5 || r.r.Output != out {
		t.Fatalf("capture %+v %v", r.r, r.err)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, s := range seqs {
		if s != i {
			t.Fatalf("seq %v", seqs)
		}
	}
	if levels != len(seqs) || len(pcm) != r.r.Frames*160*2 {
		t.Errorf("%d levels, %d bytes for %d frames", levels, len(pcm), r.r.Frames)
	}
	wav, _ := os.ReadFile(out)
	if len(wav) != 44+len(pcm) || string(wav[:4]) != "RIFF" || binary.LittleEndian.Uint32(wav[24:]) != 16000 {
		t.Errorf("wav: %d bytes for %d", len(wav), len(pcm))
	}
	// Stopped already: nothing to stop.
	if stop, _ := request[proto.CaptureStopResult](t, h, proto.MethodCaptureStop, proto.CaptureStopParams{Job: "S1"}); stop.Stopped {
		t.Error("stopped twice")
	}
}

func TestCancelCapture(t *testing.T) {
	h := start(t, "frame-ms=10")
	h.ready(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	frames := make(chan struct{}, 100)
	go func() {
		_, err := h.sup.Call(ctx, "S2", proto.MethodCaptureStart, proto.CaptureParams{SampleRate: 16000, Channels: 1, Encoding: "pcm16le"},
			func(proto.Message) { frames <- struct{}{} })
		got <- err
	}()
	<-frames
	cancel()
	if err := <-got; !errors.Is(err, &proto.Error{Code: proto.CodeCancelled}) {
		t.Fatalf("cancelled capture: %v", err)
	}
	if h.sup.Status().State != Ready {
		t.Error("the helper did not stay")
	}
}

func TestStuckCancelRestarts(t *testing.T) {
	h := start(t, "hang-cancel,frame-ms=10")
	first := h.ready(t).PID
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, err := h.sup.Call(ctx, "S3", proto.MethodCaptureStart, proto.CaptureParams{SampleRate: 16000, Channels: 1, Encoding: "pcm16le"}, nil)
		got <- err
	}()
	wait(t, "the capture", func() bool { return len(h.sup.Status().Running) == 1 })
	cancel()
	if err := <-got; !errors.Is(err, ErrExited) {
		t.Fatalf("stuck capture: %v", err)
	}
	wait(t, "a new helper", func() bool { st := h.sup.Status(); return st.State == Ready && st.PID != first })
	if st := h.sup.Status(); st.Restarts != 1 {
		t.Errorf("restarts %d", st.Restarts)
	}
}

func TestHotkeyEventsGoToOnNotify(t *testing.T) {
	h := start(t, "press=transcribe/0")
	h.ready(t)
	res, err := request[proto.HotkeyConfigureResult](t, h, proto.MethodHotkeyConfigure, proto.HotkeyConfigureParams{Bindings: []proto.HotkeyBinding{
		{ID: "transcribe/0", Keys: "Fn", Trigger: "tap"}, {ID: "translate/0", Keys: "Fn+Shift", Trigger: "tap"}}})
	if err != nil || !res.Trusted || !slices.Equal(res.Active, []string{"transcribe/0", "translate/0"}) {
		t.Fatalf("configure %+v %v", res, err)
	}
	wait(t, "three events", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.notes) == 3 })
	var actions []string
	for _, m := range h.notes {
		var e proto.HotkeyEventParams
		m.DecodeParams(&e)
		actions = append(actions, e.Action)
		if e.Binding != "transcribe/0" {
			t.Errorf("binding %q", e.Binding)
		}
	}
	if !slices.Equal(actions, []string{"down", "up", "tap"}) {
		t.Errorf("actions %v", actions)
	}

	u := start(t, "untrusted")
	u.ready(t)
	res, err = request[proto.HotkeyConfigureResult](t, u, proto.MethodHotkeyConfigure, proto.HotkeyConfigureParams{Bindings: []proto.HotkeyBinding{{ID: "a", Keys: "Fn", Trigger: "tap"}}})
	if err != nil || res.Trusted || len(res.Active) != 0 {
		t.Errorf("untrusted %+v %v", res, err)
	}
}

func TestCrashRestartsAndFailsCalls(t *testing.T) {
	h := start(t, "frame-ms=10")
	first := h.ready(t).PID
	_, err := h.sup.Call(context.Background(), "S4", proto.MethodCaptureStart,
		proto.CaptureParams{Devices: []string{"crash"}, SampleRate: 16000, Channels: 1, Encoding: "pcm16le"}, nil)
	if !errors.Is(err, ErrExited) || !errors.Is(err, core.ErrHelper) {
		t.Fatalf("crash: %v", err)
	}
	wait(t, "the failure", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return slices.ContainsFunc(h.statuses, func(s Status) bool { return s.State == Failed })
	})
	h.mu.Lock()
	var failed *Status
	for i := range h.statuses {
		if h.statuses[i].State == Failed {
			failed = &h.statuses[i]
		}
	}
	h.mu.Unlock()
	if failed == nil || failed.Reason != FailCrashed || !strings.Contains(failed.StderrTail, "the audio engine stopped") || failed.RestartAt.IsZero() {
		t.Fatalf("failed status %+v", failed)
	}
	// Calls wait for the restart.
	if _, err := request[proto.DevicesResult](t, h, proto.MethodCaptureDevices, nil); err != nil {
		t.Fatalf("after the restart: %v", err)
	}
	if st := h.sup.Status(); st.PID == first || st.Restarts != 1 {
		t.Errorf("status %+v", st)
	}
	logs, _ := os.ReadFile(filepath.Join(filepathDir(t, h), "helper.log"))
	if !strings.Contains(string(logs), "the audio engine stopped") {
		t.Errorf("helper.log: %q", logs)
	}
}

// filepathDir is the LogDir the harness gave the supervisor.
func filepathDir(t *testing.T, h *harness) string {
	return h.sup.(*supervisor).cfg.LogDir
}

func TestFailures(t *testing.T) {
	for _, tc := range []struct {
		opts   string
		reason FailReason
		retry  bool
	}{
		{"mismatch", FailProtocol, false},
		{"refuse", FailProtocol, false},
		{"silent", FailHandshake, true},
		{"crash-start", FailCrashed, true},
	} {
		t.Run(tc.opts, func(t *testing.T) {
			h := start(t, tc.opts, func(c *Config) { c.HelloTimeout = 300 * time.Millisecond; c.MaxRestarts = 1 })
			wait(t, "the failure", func() bool { return h.sup.Status().State == Failed })
			st := h.sup.Status()
			if st.Reason != tc.reason || st.RestartAt.IsZero() != !tc.retry {
				t.Fatalf("status %+v", st)
			}
			if !tc.retry {
				if _, err := h.sup.Call(context.Background(), "", proto.MethodCaptureDevices, nil, nil); !errors.Is(err, core.ErrHelper) {
					t.Errorf("call to a failed helper: %v", err)
				}
			}
		})
	}
	// No executable.
	h := start(t, "", func(c *Config) { c.Bin = "" })
	if st := h.sup.Status(); st.State != Failed || st.Reason != FailMissing {
		t.Fatalf("missing: %+v", st)
	}
}

func TestRestartAndStop(t *testing.T) {
	h := start(t, "")
	first := h.ready(t).PID
	if err := h.sup.Restart(); err != nil {
		t.Fatal(err)
	}
	st := h.ready(t)
	if st.PID == first || st.Restarts != 0 {
		t.Errorf("restart %+v", st)
	}
	h.sup.Stop()
	if st := h.sup.Status(); st.State != Stopped || st.PID != 0 {
		t.Errorf("stop %+v", st)
	}
	if _, err := h.sup.Call(context.Background(), "", proto.MethodCaptureDevices, nil, nil); !errors.Is(err, core.ErrHelper) {
		t.Errorf("call after stop: %v", err)
	}
}

// TestReplayFixtures runs every fixture's requests through the
// supervisor against the fake replaying it, and checks the answers match.
func TestReplayFixtures(t *testing.T) {
	paths, _ := filepath.Glob("../../helper/Fixtures/*.jsonl")
	if len(paths) == 0 {
		t.Fatal("no fixtures")
	}
	for _, path := range []string{"../../helper/Fixtures/browser.jsonl", "../../helper/Fixtures/hotkey.jsonl"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			abs, _ := filepath.Abs(path)
			h := start(t, "replay="+abs)
			h.ready(t)
			u, err := request[proto.BrowserURLResult](t, h, proto.MethodBrowserURL, proto.BrowserURLParams{BundleID: "com.google.Chrome"})
			if strings.HasSuffix(path, "browser.jsonl") {
				if err != nil || u.URL != "https://mail.google.com/mail/u/0/#inbox" {
					t.Errorf("replayed url %+v %v", u, err)
				}
				return
			}
			if !errors.Is(err, &proto.Error{Code: proto.CodeMethodNotFound}) {
				t.Errorf("not in the fixture: %v", err)
			}
			res, err := request[proto.HotkeyConfigureResult](t, h, proto.MethodHotkeyConfigure, proto.HotkeyConfigureParams{Bindings: []proto.HotkeyBinding{{ID: "transcribe/0", Keys: "Fn", Trigger: "tap"}}})
			if err != nil || len(res.Active) != 4 {
				t.Errorf("replayed configure %+v %v", res, err)
			}
			wait(t, "the replayed events", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.notes) == 3 })
		})
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AURALINK_HELPER", "")
	os.Unsetenv("AURALINK_HELPER")
	if Find(dir) != "" {
		t.Error("found nothing as something")
	}
	bin := filepath.Join(dir, "helper", BinName)
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	if Find(dir) != bin {
		t.Errorf("Find = %q", Find(dir))
	}
	t.Setenv("AURALINK_HELPER", "/opt/x")
	if Find(dir) != "/opt/x" {
		t.Error("the environment does not win")
	}
}
