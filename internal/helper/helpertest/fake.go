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
// cmd/fakehelper builds the same fake as a program of its own, to try the
// app without the Swift helper (AURALINK_HELPER=path/to/fakehelper).
//
// The fake serves every method of package proto, so whole sessions run on
// Linux: capture.start plays a 440 Hz tone, or a recording (pcm=), in real
// time as capture.pcm frames and capture.level; hotkey.configure installs
// a pretend tap and can play a script of notifications (events=, press=);
// textio.* read a scripted selection and log what they paste (paste-log=);
// browser.url answers from Browsers, app.list from Apps and Installed.
// (From AudioSmith's engine/enginetest, with the helper's methods.)
package helpertest

//
// AURALINK_FAKE_HELPER is a comma-separated list of options; values are
// URL-query escaped (a comma is %2C):
//
//	(empty)          speak the protocol (the default)
//	mismatch         answer helper.hello with protocol 9
//	refuse           answer helper.hello with protocol_mismatch
//	silent           never answer helper.hello
//	crash-start      exit 3 at once
//	hang-cancel      ignore job.cancel (the capture keeps running)
//	mic=STATE        the Microphone: granted (default), denied, notDetermined, restricted;
//	                 notDetermined becomes granted on permissions.request or capture.start
//	deny-mic         mic=denied
//	untrusted        no Accessibility: the tap is not installed, textio fails
//	trust-after=MS   with untrusted: Accessibility comes MS ms after the start
//	                 (then the tap installs and hotkey.state is sent)
//	frame-ms=N       a capture.pcm frame (and level) every N ms of wall time (default 100)
//	pcm=PATH         capture.pcm plays this WAV (16-bit mono, any rate) or raw
//	                 PCM16 at the requested rate, then silence
//	events=PATH      once the tap is installed, play a script: JSON lines
//	                 {"afterMs":N,"method":"hotkey.event","params":{…}}, any notification
//	press=ID         once the tap is installed, binding ID fires (a trigger, with Front)
//	front=BUNDLE     the app in front (default com.tinyspeck.slackmacgap, Slack, pid 812)
//	selection=TEXT   textio.selection reads TEXT through Accessibility
//	copy-selection=TEXT  textio.selection reads TEXT only with allowCopy (via copy)
//	readonly         textio.focus: the focused element is not editable
//	paste-log=PATH   append each textio.paste's params to PATH (JSON lines)
//	delay=METHOD:MS  answer METHOD MS ms late (slow calls)
//	no-url[=BUNDLE]  browser.url reads no URL (of BUNDLE, or of every browser)
//	replay=PATH      replay a fixture transcript (helper/Fixtures/*.jsonl)
//
// A capture.start whose first device is "crash" exits 3 after its first
// frame; one whose first device is "gone" fails with device_unavailable;
// "lost" ends the recording with deviceLost after its third frame.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
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

// Opt escapes a value for an option (texts with commas).
func Opt(key, value string) string { return key + "=" + url.QueryEscape(value) }

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

// Apps are the apps the fake knows, by bundle id (front=).
var Apps = map[string]proto.App{
	"com.tinyspeck.slackmacgap":  {PID: 812, BundleID: "com.tinyspeck.slackmacgap", Name: "Slack"},
	"com.google.Chrome":          {PID: 901, BundleID: "com.google.Chrome", Name: "Google Chrome"},
	"com.apple.Safari":           {PID: 902, BundleID: "com.apple.Safari", Name: "Safari"},
	"company.thebrowser.Browser": {PID: 903, BundleID: "company.thebrowser.Browser", Name: "Arc"},
	"com.apple.TextEdit":         {PID: 530, BundleID: "com.apple.TextEdit", Name: "TextEdit"},
}

// Browsers are the URLs browser.url answers, by bundle id; other apps
// have none (via "none").
var Browsers = map[string]string{
	"com.apple.Safari":           "https://developer.apple.com/documentation/",
	"com.google.Chrome":          "https://mail.google.com/mail/u/0/#inbox",
	"com.microsoft.edgemac":      "https://github.com/hehehai/Auralink/pulls",
	"com.brave.Browser":          "https://news.ycombinator.com/",
	"company.thebrowser.Browser": "https://linear.app/team/issue/AUR-12",
}

// Installed are apps the fake lists with app.list's installed, besides
// Apps (which run).
var Installed = []proto.AppInfo{
	{BundleID: "com.apple.Notes", Name: "Notes", Path: "/System/Applications/Notes.app"},
	{BundleID: "com.microsoft.edgemac", Name: "Microsoft Edge", Path: "/Applications/Microsoft Edge.app"},
	{BundleID: "com.apple.mail", Name: "Mail", Path: "/System/Applications/Mail.app"},
}

// Script is one line of an events= script.
type Script struct {
	AfterMS int             `json:"afterMs,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type fake struct {
	opts  map[string]string
	out   *bufio.Writer
	wmu   sync.Mutex
	start time.Time

	mu        sync.Mutex
	hello     bool
	mic       string
	trusted   bool
	bindings  int
	installed bool
	scripted  bool // the events= script and press= ran
	hkActive  bool
	running   map[string]bool          // capture jobs
	cancel    map[string]bool          // asked to stop by job.cancel
	stop      map[string]chan struct{} // closed by capture.stop
	finished  map[string]chan struct{}
}

// Run serves the protocol on stdin and stdout with opts (see the top of
// the file) until stdin closes or helper.shutdown.
func Run(optString string) {
	opts := map[string]string{}
	for _, o := range strings.Split(optString, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
		if k != "" {
			if u, err := url.QueryUnescape(v); err == nil {
				v = u
			}
			opts[k] = v
		}
	}
	f := &fake{opts: opts, out: bufio.NewWriter(os.Stdout), start: time.Now(), mic: proto.PermGranted, trusted: true,
		running: map[string]bool{}, cancel: map[string]bool{},
		stop: map[string]chan struct{}{}, finished: map[string]chan struct{}{}}
	if v, ok := opts["mic"]; ok {
		f.mic = v
	}
	if f.has("deny-mic") {
		f.mic = proto.PermDenied
	}
	if f.has("untrusted") {
		f.trusted = false
		if ms, err := strconv.Atoi(opts["trust-after"]); err == nil {
			time.AfterFunc(time.Duration(ms)*time.Millisecond, f.trust)
		}
	}
	fmt.Fprintln(os.Stderr, "fake helper starting")
	if f.has("crash-start") {
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

func (f *fake) has(opt string) bool { _, ok := f.opts[opt]; return ok }

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

// now is the fake's monotonic clock, in ms (hotkey.event's t).
func (f *fake) now() float64 { return float64(time.Since(f.start).Microseconds())/1000 + 1 }

func (f *fake) front() proto.App {
	if a, ok := Apps[f.opts["front"]]; ok {
		return a
	}
	if id := f.opts["front"]; id != "" {
		return proto.App{PID: 700, BundleID: id, Name: id}
	}
	return Apps["com.tinyspeck.slackmacgap"]
}

func (f *fake) isTrusted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trusted
}

func (f *fake) handle(m proto.Message) {
	if method, ms, ok := strings.Cut(f.opts["delay"], ":"); ok && method == m.Method {
		d, _ := strconv.Atoi(ms)
		go func() {
			time.Sleep(time.Duration(d) * time.Millisecond)
			f.serve(m)
		}()
		return
	}
	f.serve(m)
}

func (f *fake) serve(m proto.Message) {
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
		if running && !f.has("hang-cancel") {
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
	case proto.MethodPermissionsStatus:
		f.result(m.ID, f.permissions())
	case proto.MethodPermissionsRequest:
		var p proto.PermissionsRequestParams
		m.DecodeParams(&p)
		switch p.Permission {
		case proto.PermissionMicrophone:
			f.mu.Lock()
			if f.mic == proto.PermNotDetermined {
				f.mic = proto.PermGranted
			}
			f.mu.Unlock()
		case proto.PermissionAccessibility:
		default:
			f.fail(m.ID, proto.CodeBadRequest, "Unknown permission %s", p.Permission)
			return
		}
		f.result(m.ID, f.permissions())
	case proto.MethodCaptureDevices:
		f.result(m.ID, proto.DevicesResult{Devices: Devices()})
	case proto.MethodCaptureStart:
		f.startCapture(m)
	case proto.MethodCaptureStop:
		f.captureStop(m)
	case proto.MethodHotkeyConfigure:
		f.hotkeys(m)
	case proto.MethodHotkeyStatus:
		f.result(m.ID, f.hotkeyStatus())
	case proto.MethodAppFrontmost:
		f.result(m.ID, f.front())
	case proto.MethodTextFocus, proto.MethodTextSelection, proto.MethodTextPaste:
		f.textio(m)
	case proto.MethodBrowserURL:
		var p proto.BrowserURLParams
		m.DecodeParams(&p)
		if p.PID != 0 {
			for _, app := range Apps {
				if app.PID == p.PID {
					p.BundleID = app.BundleID
				}
			}
		}
		if p.BundleID == "" {
			p.BundleID = f.front().BundleID
		}
		res := proto.BrowserURLResult{BundleID: p.BundleID, Via: proto.URLViaNone, MS: 1}
		u, ok := Browsers[p.BundleID]
		switch {
		case !ok:
			res.Error = "no AXDocument or web area URL (visited 12)"
		case f.has("no-url") && (f.opts["no-url"] == "" || f.opts["no-url"] == p.BundleID):
			res.Error = "no AXDocument or web area URL (visited 27)"
		default:
			res.URL, res.Via = u, proto.URLViaAXDocument
		}
		f.result(m.ID, res)
	case proto.MethodAppList:
		var p proto.AppListParams
		m.DecodeParams(&p)
		var apps []proto.AppInfo
		for _, app := range Apps {
			apps = append(apps, proto.AppInfo{BundleID: app.BundleID, Name: app.Name, Running: true, PID: app.PID, Path: "/Applications/" + app.Name + ".app"})
		}
		if p.Installed {
			apps = append(apps, Installed...)
		}
		slices.SortFunc(apps, func(x, y proto.AppInfo) int { return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
		f.result(m.ID, proto.AppListResult{Apps: apps})
	default:
		f.fail(m.ID, proto.CodeMethodNotFound, "Unknown method %s", m.Method)
	}
}

func (f *fake) permissions() proto.PermissionsResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return proto.PermissionsResult{Microphone: f.mic, Accessibility: f.trusted}
}

func (f *fake) handleHello(m proto.Message) {
	var p proto.HelloParams
	m.DecodeParams(&p)
	if f.has("silent") {
		return
	}
	if f.has("refuse") || p.Protocol != proto.Version {
		f.send(proto.NewError(m.ID, &proto.Error{Code: proto.CodeProtocolMismatch, Message: "This helper speaks protocol 1", Data: json.RawMessage(`{"supported":[1]}`)}))
		return
	}
	res := proto.HelloResult{Protocol: proto.Version, Version: "fake", PID: os.Getpid(), Methods: Methods(),
		Device: proto.Device{Name: "Fake Mac", OS: "test"}}
	if f.has("mismatch") {
		res.Protocol = 9
	}
	f.mu.Lock()
	f.hello = true
	f.mu.Unlock()
	f.notify(proto.MethodLog, proto.LogParams{Level: "info", Message: "fake helper ready"})
	f.result(m.ID, res)
}

// ---- hotkeys ----

func (f *fake) hotkeyStatus() proto.HotkeyStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := proto.HotkeyStatus{Installed: f.installed, Accessibility: f.trusted, Bindings: f.bindings}
	if f.bindings > 0 && !f.trusted {
		st.Error = "CGEvent.tapCreate failed: Auralink may not use Accessibility; retrying every 2 s"
	}
	return st
}

func (f *fake) hotkeys(m proto.Message) {
	var p proto.HotkeyConfigureParams
	if err := m.DecodeParams(&p); err != nil {
		f.fail(m.ID, proto.CodeBadRequest, "Bad bindings: %v", err)
		return
	}
	f.mu.Lock()
	f.bindings = len(p.Bindings)
	f.installed = f.bindings > 0 && f.trusted
	f.hkActive = p.Active
	install := f.installed && !f.scripted
	if install {
		f.scripted = true
	}
	f.mu.Unlock()
	f.result(m.ID, f.hotkeyStatus())
	if install {
		go f.playScript(p.Bindings)
	}
}

// trust grants Accessibility (trust-after): a configured tap installs and
// says so.
func (f *fake) trust() {
	f.mu.Lock()
	f.trusted = true
	install := f.bindings > 0 && !f.installed
	f.installed = f.bindings > 0
	play := install && !f.scripted
	if play {
		f.scripted = true
	}
	f.mu.Unlock()
	if install {
		f.notify(proto.MethodHotkeyState, f.hotkeyStatus())
	}
	if play {
		go f.playScript(nil)
	}
}

// playScript plays press= and events= once the tap is installed.
func (f *fake) playScript(bindings []proto.HotkeyBinding) {
	if id := f.opts["press"]; id != "" {
		front := f.front()
		f.notify(proto.MethodHotkeyEvent, proto.HotkeyEventParams{Binding: id, Phase: proto.PhaseTrigger, Behavior: proto.BehaviorTap,
			HeldMS: 110, T: f.now(), Front: &front})
	}
	path := f.opts["events"]
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "events:", err)
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s Script
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			fmt.Fprintln(os.Stderr, "events:", err)
			continue
		}
		time.Sleep(time.Duration(s.AfterMS) * time.Millisecond)
		f.send(proto.Message{Method: s.Method, Params: s.Params})
	}
}

// ---- textio ----

func (f *fake) textio(m proto.Message) {
	if !f.isTrusted() {
		f.failData(m.ID, proto.CodePermissionDenied, map[string]string{"permission": proto.PermissionAccessibility}, "Auralink may not use Accessibility")
		return
	}
	front := f.front()
	switch m.Method {
	case proto.MethodTextFocus:
		res := proto.FocusResult{PID: front.PID, Role: "AXTextArea", Editable: true, ValueLength: 40}
		if f.has("readonly") {
			res.Role, res.Editable, res.ValueLength = "AXStaticText", false, 0
		}
		if sel := f.opts["selection"]; sel != "" {
			res.Selection = &proto.TextRange{Location: 0, Length: len([]rune(sel))}
		}
		f.result(m.ID, res)
	case proto.MethodTextSelection:
		var p proto.SelectionParams
		m.DecodeParams(&p)
		res := proto.SelectionResult{Via: proto.ViaNone, MS: 1, Steps: []string{"focus=AXTextArea"}}
		if sel := f.opts["selection"]; sel != "" {
			res.Text, res.Via = sel, proto.ViaAX
		} else if sel := f.opts["copy-selection"]; sel != "" && p.AllowCopy {
			res.Text, res.Via, res.Steps = sel, proto.ViaCopy, append(res.Steps, "copy: changed")
		}
		f.result(m.ID, res)
	case proto.MethodTextPaste:
		var p proto.PasteParams
		m.DecodeParams(&p)
		if p.TargetPID != 0 && p.TargetPID != front.PID {
			f.result(m.ID, proto.PasteResult{MS: 0.1, Front: &front})
			return
		}
		if path := f.opts["paste-log"]; path != "" {
			line, _ := json.Marshal(p)
			if fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				fh.Write(append(line, '\n'))
				fh.Close()
			}
		}
		f.result(m.ID, proto.PasteResult{Posted: true, Restored: !p.KeepClipboard, MS: 2})
	}
}

// ---- capture ----

func (f *fake) cancelled(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancel[id]
}

// startCapture runs a capture.start job on a goroutine of its own; one at
// a time.
func (f *fake) startCapture(m proto.Message) {
	var p proto.CaptureParams
	if err := m.DecodeParams(&p); err != nil {
		f.fail(m.ID, proto.CodeBadRequest, "Bad params: %v", err)
		return
	}
	f.mu.Lock()
	if f.mic == proto.PermNotDetermined {
		f.mic = proto.PermGranted
	}
	mic := f.mic
	f.mu.Unlock()
	if mic != proto.PermGranted {
		f.failData(m.ID, proto.CodePermissionDenied, map[string]string{"permission": proto.PermissionMicrophone}, "Auralink may not use the microphone")
		return
	}
	if len(p.Devices) > 0 && p.Devices[0] == "gone" {
		f.failData(m.ID, proto.CodeDeviceUnavailable, map[string]string{"device": "gone"}, "No input gave audio")
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

// source is the audio a capture plays: pcm= at the requested rate, then
// silence; or a tone.
type source struct {
	rate int
	pcm  []byte // PCM16 LE at rate; nil: the tone
	pos  int    // samples given
}

func (s *source) next(n int) []byte {
	out := make([]byte, 2*n)
	if s.pcm == nil {
		for i := range n {
			v := 0.25 * math.Sin(2*math.Pi*440*float64(s.pos+i)/float64(s.rate))
			binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v*32767)))
		}
	} else if 2*s.pos < len(s.pcm) {
		copy(out, s.pcm[2*s.pos:])
	}
	s.pos += n
	return out
}

// level is the frame's RMS and peak in dBFS.
func level(frame []byte) (rms, peak float64) {
	var sum, max float64
	n := len(frame) / 2
	for i := range n {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(frame[2*i:]))) / 32768)
		sum += v * v
		max = math.Max(max, v)
	}
	db := func(x float64) float64 {
		if x <= 1e-6 {
			return -120
		}
		return math.Max(-120, math.Round(200*math.Log10(x))/10)
	}
	if n == 0 {
		return -120, -120
	}
	return db(math.Sqrt(sum / float64(n))), db(max)
}

// capture records until capture.stop, job.cancel, maxSeconds or a fake
// device's end.
func (f *fake) capture(id string, p proto.CaptureParams, stop chan struct{}) {
	rate := p.SampleRate
	if rate <= 0 {
		rate = 16000
	}
	dev := Devices()[0]
	fellBack := false
	for _, d := range p.Devices {
		if i := slices.IndexFunc(Devices(), func(x proto.CaptureDevice) bool { return x.ID == d }); i >= 0 {
			dev = Devices()[i]
			break
		}
		if d != "crash" && d != "lost" {
			fellBack = true
		}
	}
	src := &source{rate: rate}
	if path := f.opts["pcm"]; path != "" {
		pcm, err := ReadPCM(path, rate)
		if err != nil {
			f.fail(id, proto.CodeInternal, "pcm: %v", err)
			return
		}
		src.pcm = pcm
	}
	started := proto.CaptureStartedParams{Job: id, Device: dev.ID, DeviceName: dev.Name, FellBack: fellBack,
		DeviceSampleRate: dev.SampleRate, DeviceChannels: dev.Channels, SampleRate: rate, StartMS: 5}
	if fellBack {
		started.FallbackReason = "input " + p.Devices[0] + " not found"
	}
	f.notify(proto.MethodCaptureStarted, started)
	frameMS := p.FrameMS
	if frameMS <= 0 {
		frameMS = 100
	}
	wallMS := frameMS
	if ms, err := strconv.Atoi(f.opts["frame-ms"]); err == nil && ms > 0 {
		wallMS = ms
	}
	samples := rate * frameMS / 1000
	tick := time.NewTicker(time.Duration(wallMS) * time.Millisecond)
	defer tick.Stop()
	seq, total, reason, peak := 0, 0, "", -120.0
	first := 0.0
	t0 := time.Now()
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
		frame := src.next(samples)
		rms, pk := level(frame)
		peak = math.Max(peak, pk)
		seconds := float64(total+samples) / float64(rate)
		f.notify(proto.MethodCaptureLevel, proto.LevelParams{Job: id, Seconds: seconds, RMS: rms, Peak: pk})
		f.notify(proto.MethodCapturePCM, proto.PCMParams{Job: id, Seq: seq, Offset: total, Data: frame})
		if seq == 0 {
			first = float64(time.Since(t0).Microseconds())/1000 + 1
		}
		seq++
		total += samples
		if len(p.Devices) > 0 && p.Devices[0] == "crash" {
			fmt.Fprintln(os.Stderr, "fatal: the audio engine stopped")
			os.Exit(3)
		}
		if len(p.Devices) > 0 && p.Devices[0] == "lost" && seq == 3 {
			reason = proto.CaptureDeviceLost
		}
		if p.MaxSeconds > 0 && seconds >= p.MaxSeconds {
			reason = proto.CaptureMaxSeconds
		}
	}
	f.result(id, proto.CaptureResult{Device: dev.ID, Reason: reason, Frames: seq, Samples: total,
		Seconds: float64(total) / float64(rate), Peak: peak, FirstFrameMS: first, CPUSeconds: 0.001})
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

// ReadPCM reads a WAV file (PCM16 mono) or raw PCM16 LE, resampled
// (linearly) from the WAV's rate to rate; raw PCM is taken to be at rate.
func ReadPCM(path string, rate int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return data[:len(data)&^1], nil
	}
	var from, channels, bits int
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id, size := string(data[off:off+4]), int(binary.LittleEndian.Uint32(data[off+4:]))
		body := data[off+8 : min(len(data), off+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 || binary.LittleEndian.Uint16(body) != 1 {
				return nil, fmt.Errorf("%s: not PCM", path)
			}
			channels, from, bits = int(binary.LittleEndian.Uint16(body[2:])), int(binary.LittleEndian.Uint32(body[4:])), int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			pcm = body
		}
		off += 8 + size + size%2
	}
	if channels != 1 || bits != 16 || from <= 0 {
		return nil, fmt.Errorf("%s: want 16-bit mono, have %d channels of %d bits", path, channels, bits)
	}
	if from == rate {
		return pcm[:len(pcm)&^1], nil
	}
	n := len(pcm) / 2
	m := int(int64(n) * int64(rate) / int64(from))
	out := make([]byte, 2*m)
	for i := range m {
		x := float64(i) * float64(from) / float64(rate)
		j := int(x)
		a := float64(int16(binary.LittleEndian.Uint16(pcm[2*j:])))
		b := a
		if j+1 < n {
			b = float64(int16(binary.LittleEndian.Uint16(pcm[2*j+2:])))
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(math.Round(a+(b-a)*(x-float64(j))))))
	}
	return out, nil
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
