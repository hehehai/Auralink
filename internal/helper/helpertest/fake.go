// Package helpertest is a scripted auralink-helper for tests: a test
// binary runs it instead of its tests when AURALINK_FAKE_HELPER is set,
// so the supervisor spawns a real process and talks to it over real
// pipes, without macOS. In the test package:
//
//	func TestMain(m *testing.M) {
//		if helpertest.Enabled() {
//			helpertest.Main()
//		}
//		os.Exit(m.Run())
//	}
//
// and point helper.Config at os.Executable() with Env: helpertest.Env(opts).
// cmd/fakehelper builds the same fake as a program of its own, to stand in
// for the Swift helper in a packaged app until that exists
// (scripts/stage-fake-helper.sh).
//
// The fake serves every method of package proto: capture.start records a
// 440 Hz tone in real time as capture.pcm frames (16 kHz mono PCM16) and
// capture.level, and writes Output as a WAV; hotkey.configure takes every
// binding; browser.url answers for the browsers in Browsers; and
// automation.probe answers from AutomationStates. (From AudioSmith's
// engine/enginetest, reduced to the helper's methods.)
package helpertest

//
// AURALINK_FAKE_HELPER is a comma-separated list of options:
//
//	(empty)        speak the protocol (the default)
//	mismatch       answer helper.hello with protocol 9
//	refuse         answer helper.hello with protocol_mismatch
//	silent         never answer helper.hello
//	crash-start    exit 3 at once
//	hang-cancel    ignore job.cancel (the capture keeps running)
//	deny-mic       capture.start fails with permission_denied
//	untrusted      hotkey.configure takes nothing (no Accessibility)
//	press=ID       after hotkey.configure, binding ID goes down, up and taps
//	frame-ms=N     a capture.pcm frame (and level) every N ms (default 100)
//	replay=PATH    replay a fixture transcript (helper/Fixtures/*.jsonl)
//
// A capture.start whose first device is "crash" exits 3 after its first
// frame; one whose device is "gone" fails with device_unavailable.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/Auralink/internal/helper/proto"
)

const fakeEnv = "AURALINK_FAKE_HELPER"

// Enabled reports whether this process should be the fake helper.
func Enabled() bool {
	_, ok := os.LookupEnv(fakeEnv)
	return ok
}

// Env returns the environment that makes a re-executed test binary the
// fake helper with opts (see the comment at the top of the file).
func Env(opts string) []string { return []string{fakeEnv + "=" + opts} }

// Main runs the fake helper with the options of the environment, and
// exits.
func Main() {
	Run(os.Getenv(fakeEnv))
	os.Exit(0)
}

// Methods is what the fake implements (hello.methods): every request.
func Methods() []string { return slices.Clone(proto.Requests) }

// Devices are the fake's audio inputs.
func Devices() []proto.CaptureDevice {
	return []proto.CaptureDevice{
		{ID: "BuiltInMicrophoneDevice", Name: "MacBook Air Microphone", Channels: 1, SampleRate: 48000, Default: true, Transport: "builtin"},
		{ID: "AirPodsPro:input", Name: "AirPods Pro", Channels: 1, SampleRate: 24000, Transport: "bluetooth"},
	}
}

// Browsers are the URLs browser.url answers, by bundle id; others are
// unsupported.
var Browsers = map[string]string{
	"com.apple.Safari":           "https://developer.apple.com/documentation/",
	"com.google.Chrome":          "https://mail.google.com/mail/u/0/#inbox",
	"com.microsoft.edgemac":      "https://github.com/hehehai/Auralink/pulls",
	"com.brave.Browser":          "https://news.ycombinator.com/",
	"company.thebrowser.Browser": "https://linear.app/team/issue/AUR-12",
}

// AutomationStates are what automation.probe answers without prompting;
// a prompt grants what is not determined. Others are not running.
var AutomationStates = map[string]string{
	"com.apple.Safari":           proto.AutomationNotDetermined,
	"com.google.Chrome":          proto.AutomationGranted,
	"com.microsoft.edgemac":      proto.AutomationNotRunning,
	"com.brave.Browser":          proto.AutomationDenied,
	"company.thebrowser.Browser": proto.AutomationGranted,
}

type fake struct {
	opts map[string]string
	out  *bufio.Writer
	wmu  sync.Mutex

	mu       sync.Mutex
	hello    bool
	running  map[string]bool          // capture jobs
	cancel   map[string]bool          // asked to stop by job.cancel
	stop     map[string]chan struct{} // closed by capture.stop
	finished map[string]chan struct{}
}

// Run serves the protocol on stdin and stdout with opts (see the top of
// the file) until stdin closes or helper.shutdown.
func Run(optString string) {
	opts := map[string]string{}
	for _, o := range strings.Split(optString, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
		if k != "" {
			opts[k] = v
		}
	}
	f := &fake{opts: opts, out: bufio.NewWriter(os.Stdout), running: map[string]bool{}, cancel: map[string]bool{},
		stop: map[string]chan struct{}{}, finished: map[string]chan struct{}{}}
	fmt.Fprintln(os.Stderr, "fake helper starting")
	if _, ok := opts["crash-start"]; ok {
		fmt.Fprintln(os.Stderr, "fatal: could not start")
		os.Exit(3)
	}
	if path, ok := opts["replay"]; ok {
		f.replay(path)
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), proto.MaxLine)
	for sc.Scan() {
		m, err := proto.Decode(sc.Bytes())
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad line:", err)
			continue
		}
		f.handle(m)
	}
	f.mu.Lock()
	for id := range f.running {
		f.cancel[id] = true
	}
	f.mu.Unlock()
}

func (f *fake) send(m proto.Message) {
	line, _ := proto.Encode(m)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.Write(line)
	f.out.Flush()
}

func (f *fake) writeRaw(line string) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.WriteString(line + "\n")
	f.out.Flush()
}

func (f *fake) result(id string, v any) {
	m, _ := proto.NewResult(id, v)
	f.send(m)
}

func (f *fake) fail(id, code, format string, args ...any) {
	f.send(proto.NewError(id, proto.Errorf(code, format, args...)))
}

func (f *fake) failData(id, code string, data any, format string, args ...any) {
	e := proto.Errorf(code, format, args...)
	e.Data, _ = json.Marshal(data)
	f.send(proto.NewError(id, e))
}

func (f *fake) notify(method string, params any) {
	m, _ := proto.NewNotification(method, params)
	f.send(m)
}

func (f *fake) handle(m proto.Message) {
	if m.Method == proto.MethodHello {
		f.handleHello(m)
		return
	}
	f.mu.Lock()
	ready := f.hello
	f.mu.Unlock()
	if !ready {
		f.fail(m.ID, proto.CodeNotInitialized, "helper.hello must come first")
		return
	}
	switch m.Method {
	case proto.MethodShutdown:
		f.result(m.ID, nil)
		os.Exit(0)
	case proto.MethodJobCancel:
		var p proto.CancelParams
		m.DecodeParams(&p)
		f.mu.Lock()
		running := f.running[p.Job]
		if _, hang := f.opts["hang-cancel"]; running && !hang {
			f.cancel[p.Job] = true
		}
		done := f.finished[p.Job]
		f.mu.Unlock()
		if !running {
			f.result(m.ID, proto.CancelResult{Cancelled: false})
			return
		}
		go func() {
			<-done
			f.result(m.ID, proto.CancelResult{Cancelled: true})
		}()
	case proto.MethodCaptureDevices:
		f.result(m.ID, proto.DevicesResult{Devices: Devices()})
	case proto.MethodCaptureStart:
		f.startCapture(m)
	case proto.MethodCaptureStop:
		f.captureStop(m)
	case proto.MethodHotkeyConfigure:
		f.hotkeys(m)
	case proto.MethodBrowserURL:
		var p proto.BrowserURLParams
		if err := m.DecodeParams(&p); err != nil || p.BundleID == "" {
			f.fail(m.ID, proto.CodeBadRequest, "Missing bundleId")
			return
		}
		u, ok := Browsers[p.BundleID]
		if !ok {
			f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": proto.MethodBrowserURL}, "%s cannot be asked for its URL", p.BundleID)
			return
		}
		f.result(m.ID, proto.BrowserURLResult{URL: u, Source: "applescript"})
	case proto.MethodAutomationProbe:
		var p proto.AutomationProbeParams
		m.DecodeParams(&p)
		res := proto.AutomationProbeResult{Browsers: []proto.AutomationState{}}
		for _, id := range p.BundleIDs {
			st, ok := AutomationStates[id]
			if !ok {
				st = proto.AutomationNotRunning
			}
			if p.Prompt && st == proto.AutomationNotDetermined {
				st = proto.AutomationGranted
			}
			res.Browsers = append(res.Browsers, proto.AutomationState{BundleID: id, State: st})
		}
		f.result(m.ID, res)
	default:
		f.fail(m.ID, proto.CodeMethodNotFound, "Unknown method %s", m.Method)
	}
}

func (f *fake) handleHello(m proto.Message) {
	var p proto.HelloParams
	m.DecodeParams(&p)
	if _, ok := f.opts["silent"]; ok {
		return
	}
	if _, ok := f.opts["refuse"]; ok || p.Protocol != proto.Version {
		f.send(proto.NewError(m.ID, &proto.Error{Code: proto.CodeProtocolMismatch, Message: "This helper speaks protocol 1", Data: json.RawMessage(`{"supported":[1]}`)}))
		return
	}
	res := proto.HelloResult{Protocol: proto.Version, Version: "fake", PID: os.Getpid(), Methods: Methods(),
		Device: proto.Device{Name: "Fake Mac", OS: "test"}}
	if _, ok := f.opts["mismatch"]; ok {
		res.Protocol = 9
	}
	f.mu.Lock()
	f.hello = true
	f.mu.Unlock()
	f.notify(proto.MethodLog, proto.LogParams{Level: "info", Message: "fake helper ready"})
	f.result(m.ID, res)
}

func (f *fake) hotkeys(m proto.Message) {
	var p proto.HotkeyConfigureParams
	if err := m.DecodeParams(&p); err != nil {
		f.fail(m.ID, proto.CodeBadRequest, "Bad bindings: %v", err)
		return
	}
	res := proto.HotkeyConfigureResult{Active: []string{}, Trusted: true}
	if _, ok := f.opts["untrusted"]; ok {
		res.Trusted = false
	} else {
		for _, b := range p.Bindings {
			res.Active = append(res.Active, b.ID)
		}
	}
	f.result(m.ID, res)
	if id := f.opts["press"]; id != "" && slices.Contains(res.Active, id) {
		app := &proto.App{BundleID: "com.tinyspeck.slackmacgap", Name: "Slack", PID: 812}
		f.notify(proto.MethodHotkeyEvent, proto.HotkeyEventParams{Binding: id, Action: proto.HotkeyDown, App: app})
		f.notify(proto.MethodHotkeyEvent, proto.HotkeyEventParams{Binding: id, Action: proto.HotkeyUp})
		f.notify(proto.MethodHotkeyEvent, proto.HotkeyEventParams{Binding: id, Action: proto.HotkeyTap, App: app})
	}
}

func (f *fake) cancelled(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancel[id]
}

// startCapture runs a capture.start job on a goroutine of its own; one at
// a time.
func (f *fake) startCapture(m proto.Message) {
	var p proto.CaptureParams
	if err := m.DecodeParams(&p); err != nil || p.Encoding != "pcm16le" || p.SampleRate <= 0 || p.Channels != 1 {
		f.fail(m.ID, proto.CodeBadRequest, "The fake records 16-bit mono PCM")
		return
	}
	if _, deny := f.opts["deny-mic"]; deny {
		f.failData(m.ID, proto.CodePermissionDenied, map[string]string{"permission": "microphone"}, "Auralink may not use the microphone")
		return
	}
	if len(p.Devices) > 0 && p.Devices[0] == "gone" {
		f.failData(m.ID, proto.CodeDeviceUnavailable, map[string]string{"device": "gone"}, "No input device gave audio")
		return
	}
	f.mu.Lock()
	if len(f.running) > 0 {
		f.mu.Unlock()
		f.fail(m.ID, proto.CodeBusy, "A recording is running already")
		return
	}
	f.running[m.ID] = true
	stop, done := make(chan struct{}), make(chan struct{})
	f.stop[m.ID], f.finished[m.ID] = stop, done
	f.mu.Unlock()
	go func() {
		defer func() {
			f.mu.Lock()
			delete(f.running, m.ID)
			delete(f.cancel, m.ID)
			delete(f.stop, m.ID)
			delete(f.finished, m.ID)
			f.mu.Unlock()
			close(done)
		}()
		f.capture(m.ID, p, stop)
	}()
}

// capture records until capture.stop, job.cancel or maxSeconds.
func (f *fake) capture(id string, p proto.CaptureParams, stop chan struct{}) {
	dev := Devices()[0].ID
	if len(p.Devices) > 0 && slices.ContainsFunc(Devices(), func(d proto.CaptureDevice) bool { return d.ID == p.Devices[0] }) {
		dev = p.Devices[0]
	}
	frameMS := p.FrameMS
	if ms, err := strconv.Atoi(f.opts["frame-ms"]); err == nil && ms > 0 {
		frameMS = ms
	}
	if frameMS <= 0 {
		frameMS = 100
	}
	samples := p.SampleRate * frameMS / 1000
	tick := time.NewTicker(time.Duration(frameMS) * time.Millisecond)
	defer tick.Stop()
	var pcm []byte
	seq, reason := 0, ""
	for reason == "" {
		select {
		case <-stop:
			reason = proto.CaptureStopped
			continue
		case <-tick.C:
		}
		if f.cancelled(id) {
			f.fail(id, proto.CodeCancelled, "The recording was cancelled")
			return
		}
		frame := tone(p.SampleRate, seq*samples, samples)
		pcm = append(pcm, frame...)
		seconds := float64((seq+1)*samples) / float64(p.SampleRate)
		f.notify(proto.MethodCaptureLevel, proto.LevelParams{Job: id, Seconds: seconds, RMS: -15, Peak: -12})
		f.notify(proto.MethodCapturePCM, proto.PCMParams{Job: id, Seq: seq, SampleRate: p.SampleRate, Channels: 1, Encoding: "pcm16le", Data: frame})
		seq++
		if len(p.Devices) > 0 && p.Devices[0] == "crash" {
			fmt.Fprintln(os.Stderr, "fatal: the audio engine stopped")
			os.Exit(3)
		}
		if p.MaxSeconds > 0 && seconds >= p.MaxSeconds {
			reason = proto.CaptureMaxSeconds
		}
	}
	res := proto.CaptureResult{Device: dev, AudioSeconds: float64(len(pcm)/2) / float64(p.SampleRate), Frames: seq, Peak: -12, Reason: reason}
	if seq == 0 {
		res.Peak = -120
	}
	if p.Output != "" {
		if err := os.WriteFile(p.Output, WAV(pcm, p.SampleRate), 0o644); err != nil {
			f.fail(id, proto.CodeInternal, "Could not write %s: %v", p.Output, err)
			return
		}
		res.Output = p.Output
	}
	f.result(id, res)
}

// captureStop ends a recording; the job answers first.
func (f *fake) captureStop(m proto.Message) {
	var p proto.CaptureStopParams
	m.DecodeParams(&p)
	if p.Job == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing job")
		return
	}
	f.mu.Lock()
	stop, ok := f.stop[p.Job]
	done := f.finished[p.Job]
	delete(f.stop, p.Job)
	f.mu.Unlock()
	if !ok {
		f.result(m.ID, proto.CaptureStopResult{Stopped: false})
		return
	}
	close(stop)
	go func() {
		<-done
		f.result(m.ID, proto.CaptureStopResult{Stopped: true})
	}()
}

// tone is n samples of a 440 Hz tone at -12 dBFS from sample start, as
// PCM16 little endian.
func tone(rate, start, n int) []byte {
	out := make([]byte, 2*n)
	for i := range n {
		v := 0.25 * math.Sin(2*math.Pi*440*float64(start+i)/float64(rate))
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v*32767)))
	}
	return out
}

// WAV wraps mono PCM16 in a WAV header.
func WAV(pcm []byte, rate int) []byte {
	b := make([]byte, 44, 44+len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	return append(b, pcm...)
}

// replay answers requests from a fixture transcript: each request takes
// the next unused fixture request of the same method, and the helper-bound
// lines that follow it are written with the fixture's ids replaced by the
// live ones. Hello is answered by the fake itself.
func (f *fake) replay(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	type line struct {
		To  string          `json:"to"`
		Msg json.RawMessage `json:"msg"`
	}
	var lines []line
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ln line
		if err := json.Unmarshal([]byte(l), &ln); err != nil || ln.Msg == nil {
			continue
		}
		lines = append(lines, ln)
	}
	used := make([]bool, len(lines))
	ids := map[string]string{} // fixture id → live id
	rewrite := func(raw json.RawMessage) string {
		var m proto.Message
		json.Unmarshal(raw, &m)
		if to, ok := ids[m.ID]; ok {
			m.ID = to
		}
		if len(m.Params) > 0 {
			var params map[string]any
			json.Unmarshal(m.Params, &params)
			if job, ok := params["job"].(string); ok {
				if to, ok := ids[job]; ok {
					params["job"] = to
				}
			}
			m.Params, _ = json.Marshal(params)
		}
		out, _ := json.Marshal(m)
		return string(out)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), proto.MaxLine)
	for sc.Scan() {
		m, err := proto.Decode(sc.Bytes())
		if err != nil {
			continue
		}
		if m.Method == proto.MethodHello {
			f.result(m.ID, proto.HelloResult{Protocol: proto.Version, Version: "replay", PID: os.Getpid(), Methods: Methods()})
			continue
		}
		found := -1
		for i, ln := range lines {
			if used[i] || ln.To != "helper" {
				continue
			}
			var fm proto.Message
			json.Unmarshal(ln.Msg, &fm)
			if fm.Method == m.Method {
				found = i
				ids[fm.ID] = m.ID
				break
			}
		}
		if found < 0 {
			f.fail(m.ID, proto.CodeMethodNotFound, "not in the fixture: %s", m.Method)
			continue
		}
		used[found] = true
		for i := found + 1; i < len(lines) && lines[i].To == "app"; i++ {
			used[i] = true
			f.writeRaw(rewrite(lines[i].Msg))
		}
	}
}
