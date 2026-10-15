package proto

// Methods. helper.hello, helper.shutdown and job.cancel are settled; the
// others are provisional: the M0 spikes (helper recording, the event tap,
// AppleScript on five browsers) decide their final shape on a Mac.
//
// Requests marked "job" are long-running: job.cancel stops them, and
// their notifications name them in "job". Other requests are control
// requests, answered promptly, even while a job runs.
const (
	MethodHello     = "helper.hello"    // request; must be first
	MethodShutdown  = "helper.shutdown" // request: cancel jobs, answer, exit 0
	MethodJobCancel = "job.cancel"      // request

	MethodCaptureDevices  = "capture.devices"  // request: the audio inputs
	MethodCaptureStart    = "capture.start"    // request, job: records until capture.stop
	MethodCaptureStop     = "capture.stop"     // request: ends a capture.start, which answers first
	MethodHotkeyConfigure = "hotkey.configure" // request: the bindings the event tap watches
	MethodBrowserURL      = "browser.url"      // request: the front tab's URL of a browser
	MethodAutomationProbe = "automation.probe" // request: may Auralink script these browsers?

	// Notifications, helper → app.
	MethodCaptureLevel = "capture.level" // input meter of a capture.start job, ~20 Hz
	MethodCapturePCM   = "capture.pcm"   // audio of a capture.start job, a frame every ~100 ms
	MethodHotkeyEvent  = "hotkey.event"  // a bound key went down, up, or was tapped, double-tapped or held
	MethodLog          = "log"           // a log line (stderr stays free-form text)
)

// HelloParams opens a session. The app sends it first, right after
// spawning the process; until it is answered every other request fails
// with not_initialized.
type HelloParams struct {
	Protocol int    `json:"protocol"`           // Version
	App      string `json:"app"`                // app version, for logs
	LogLevel string `json:"logLevel,omitempty"` // "debug" | "info" (default) | "warning" | "error"
}

// HelloResult describes the helper. The app refuses (and reports) a
// helper whose Protocol differs from Version, and hides what needs a
// method the helper does not list.
type HelloResult struct {
	Protocol int      `json:"protocol"`
	Version  string   `json:"version"` // helper build version
	PID      int      `json:"pid"`
	Methods  []string `json:"methods"` // requests it implements
	Device   Device   `json:"device"`
}

// Device is the machine the helper runs on.
type Device struct {
	Name string `json:"name"` // "MacBook Air (M2)"
	OS   string `json:"os"`   // "macOS 26.7"
}

// CancelParams names the job (the id of its request) to cancel.
type CancelParams struct {
	Job string `json:"job"`
}

// CancelResult says whether a running job was found. The job's own
// request then answers with a cancelled error.
type CancelResult struct {
	Cancelled bool `json:"cancelled"`
}

// Empty is the params/result of methods that take or return nothing ({}).
type Empty struct{}

// LogParams is a structured log line.
type LogParams struct {
	Level   string `json:"level"` // "debug" | "info" | "warning" | "error"
	Message string `json:"message"`
	Job     string `json:"job,omitempty"`
}

// ---- capture.* ----

// DevicesResult answers capture.devices.
type DevicesResult struct {
	Devices []CaptureDevice `json:"devices"`
}

// CaptureDevice is an audio input.
type CaptureDevice struct {
	ID         string `json:"id"` // Core Audio device UID
	Name       string `json:"name"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sampleRate"`
	Default    bool   `json:"default,omitempty"`   // the system's default input
	Transport  string `json:"transport,omitempty"` // "builtin" | "usb" | "bluetooth" | "virtual" | …
}

// CaptureParams starts recording. The capture.start job sends
// capture.level and capture.pcm while it runs and answers with the
// CaptureResult when capture.stop, MaxSeconds or the device going away
// ends it; job.cancel discards it.
type CaptureParams struct {
	// Devices are device ids in the user's order of preference. The helper
	// opens the first one present; when it yields no audio within its
	// watchdog (1.2 s) it moves to the next, then to the system's default
	// input. Empty: the default input.
	Devices []string `json:"devices,omitempty"`
	// SampleRate and Channels of capture.pcm: 16000 and 1. Encoding is
	// "pcm16le".
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Encoding   string `json:"encoding"`
	// FrameMS is the audio in each capture.pcm (default 100).
	FrameMS    int     `json:"frameMs,omitempty"`
	MaxSeconds float64 `json:"maxSeconds,omitempty"` // 0 = until stopped
	// Output, when set, is a WAV file the helper also writes the
	// recording to (absolute; the app owns its directory).
	Output string `json:"output,omitempty"`
}

// CaptureResult describes the recording.
type CaptureResult struct {
	Device       string  `json:"device"`             // the device that recorded
	Fallback     bool    `json:"fallback,omitempty"` // not the first choice: it failed
	AudioSeconds float64 `json:"audioSeconds"`
	Frames       int     `json:"frames"` // capture.pcm notifications sent
	Peak         float64 `json:"peak"`   // dBFS
	// Reason is why it ended: CaptureStopped, CaptureMaxSeconds,
	// CaptureDeviceLost.
	Reason string `json:"reason"`
	Output string `json:"output,omitempty"`
}

// Capture end reasons.
const (
	CaptureStopped    = "stopped"
	CaptureMaxSeconds = "maxSeconds"
	CaptureDeviceLost = "deviceLost"
)

// CaptureStopParams names the capture.start job to stop.
type CaptureStopParams struct {
	Job string `json:"job"`
}

// CaptureStopResult says whether the capture was running; its job then
// answers (before this response).
type CaptureStopResult struct {
	Stopped bool `json:"stopped"`
}

// LevelParams is the input meter, in dBFS.
type LevelParams struct {
	Job     string  `json:"job"`
	Seconds float64 `json:"seconds"` // since the start
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	Clipped bool    `json:"clipped,omitempty"`
}

// PCMParams carries recorded audio. Seq counts from 0 without gaps. Data
// is base64 in JSON (Go []byte, Swift Data).
type PCMParams struct {
	Job        string `json:"job"`
	Seq        int    `json:"seq"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Encoding   string `json:"encoding"` // "pcm16le"
	Data       []byte `json:"data"`
}

// ---- hotkey.* ----

// HotkeyBinding is one shortcut the event tap watches. Keys follow
// settings.Hotkey ("Fn", "Fn+Shift", "RightCommand", "Control+Command+V");
// Trigger is "tap", "double" or "long". The helper decides on its own
// thread whether the key event is swallowed, as macOS requires.
type HotkeyBinding struct {
	ID      string `json:"id"` // the app's name for it: "transcribe/0"
	Keys    string `json:"keys"`
	Trigger string `json:"trigger"`
}

// HotkeyConfigureParams replaces the bindings. An empty list removes the
// event tap.
type HotkeyConfigureParams struct {
	Bindings []HotkeyBinding `json:"bindings"`
	// DoubleMS and LongMS are the double-tap window and the hold that
	// counts as long (0 = the helper's defaults).
	DoubleMS int `json:"doubleMs,omitempty"`
	LongMS   int `json:"longMs,omitempty"`
}

// HotkeyConfigureResult says what the tap watches: Active are the ids it
// took; without Accessibility (Trusted false) it takes none, and the app
// falls back to MyGo's Carbon shortcuts for plain key combinations.
type HotkeyConfigureResult struct {
	Active  []string `json:"active"`
	Trusted bool     `json:"trusted"`
}

// HotkeyEventParams reports what a bound key did: HotkeyDown and
// HotkeyUp as it happens (a hold to talk), then the trigger it made.
type HotkeyEventParams struct {
	Binding string `json:"binding"` // HotkeyBinding.ID
	Action  string `json:"action"`
	// App is the app in front when the key went down, read on the tap's
	// thread (the context snapshot of plan §4.2).
	App *App `json:"app,omitempty"`
}

// Hotkey actions.
const (
	HotkeyDown   = "down"
	HotkeyUp     = "up"
	HotkeyTap    = "tap"
	HotkeyDouble = "double"
	HotkeyLong   = "long"
)

// App is a running app.
type App struct {
	BundleID string `json:"bundleId"`
	Name     string `json:"name"`
	PID      int    `json:"pid"`
}

// ---- browser.url, automation.probe ----

// BrowserURLParams asks a browser for its front tab's URL: AppleScript
// first, the Accessibility tree (AXDocument / AXURL) when that fails.
type BrowserURLParams struct {
	BundleID  string `json:"bundleId"`
	PID       int    `json:"pid,omitempty"`       // the running instance, when known
	TimeoutMS int    `json:"timeoutMs,omitempty"` // 0 = the helper's (1 s)
}

// BrowserURLResult is the URL ("" when the front window has none) and how
// it was read.
type BrowserURLResult struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source"` // "applescript" | "ax"
}

// AutomationProbeParams asks whether Auralink may send Apple Events to
// each browser; Prompt asks macOS to show its consent prompt for those
// not determined yet (the browser must be running).
type AutomationProbeParams struct {
	BundleIDs []string `json:"bundleIds"`
	Prompt    bool     `json:"prompt,omitempty"`
}

// AutomationProbeResult lists a state per browser, in the order asked.
type AutomationProbeResult struct {
	Browsers []AutomationState `json:"browsers"`
}

// AutomationState is one browser's Automation permission.
type AutomationState struct {
	BundleID string `json:"bundleId"`
	State    string `json:"state"`
}

// Automation states.
const (
	AutomationGranted       = "granted"
	AutomationDenied        = "denied"
	AutomationNotDetermined = "notDetermined"
	AutomationNotRunning    = "notRunning" // macOS answers only for a running app
	AutomationUnknown       = "unknown"
)

// ParamsFor returns a new value of the params type of method, or nil for
// an unknown method. ResultFor does the same for results. The supervisor's
// fake and the fixture tests use them.
func ParamsFor(method string) any {
	switch method {
	case MethodHello:
		return new(HelloParams)
	case MethodShutdown, MethodCaptureDevices:
		return new(Empty)
	case MethodJobCancel:
		return new(CancelParams)
	case MethodCaptureStart:
		return new(CaptureParams)
	case MethodCaptureStop:
		return new(CaptureStopParams)
	case MethodHotkeyConfigure:
		return new(HotkeyConfigureParams)
	case MethodBrowserURL:
		return new(BrowserURLParams)
	case MethodAutomationProbe:
		return new(AutomationProbeParams)
	case MethodCaptureLevel:
		return new(LevelParams)
	case MethodCapturePCM:
		return new(PCMParams)
	case MethodHotkeyEvent:
		return new(HotkeyEventParams)
	case MethodLog:
		return new(LogParams)
	}
	return nil
}

// ResultFor: see ParamsFor.
func ResultFor(method string) any {
	switch method {
	case MethodHello:
		return new(HelloResult)
	case MethodShutdown:
		return new(Empty)
	case MethodJobCancel:
		return new(CancelResult)
	case MethodCaptureDevices:
		return new(DevicesResult)
	case MethodCaptureStart:
		return new(CaptureResult)
	case MethodCaptureStop:
		return new(CaptureStopResult)
	case MethodHotkeyConfigure:
		return new(HotkeyConfigureResult)
	case MethodBrowserURL:
		return new(BrowserURLResult)
	case MethodAutomationProbe:
		return new(AutomationProbeResult)
	}
	return nil
}

// Requests lists the requests of the protocol, Notifications its
// notifications.
var (
	Requests = []string{MethodHello, MethodShutdown, MethodJobCancel, MethodCaptureDevices, MethodCaptureStart, MethodCaptureStop,
		MethodHotkeyConfigure, MethodBrowserURL, MethodAutomationProbe}
	Notifications = []string{MethodCaptureLevel, MethodCapturePCM, MethodHotkeyEvent, MethodLog}
)

// IsJob reports whether method is a long-running, cancellable job.
func IsJob(method string) bool { return method == MethodCaptureStart }
