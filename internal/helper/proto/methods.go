package proto

// Methods. The shapes follow the M0 spikes on a Mac (the project's
// research/spikes.md): the helper records, watches the keyboard, reads and
// writes text in the app in front, and reads browser URLs; Go keeps every
// policy and passes it in the params.
//
// Requests marked "job" are long-running: job.cancel stops them, and
// their notifications name them in "job". Other requests are control
// requests, answered promptly, even while a job runs.
//
// Field conventions, which the fixtures hold both sides to: a Go field
// without omitempty is always on the wire (Swift: a non-optional); a Go
// field with omitempty is absent when zero (Swift: an optional left nil).
const (
	MethodHello     = "helper.hello"    // request; must be first
	MethodShutdown  = "helper.shutdown" // request: cancel jobs, answer, exit 0
	MethodJobCancel = "job.cancel"      // request

	MethodPermissionsStatus  = "permissions.status"  // request: the Microphone and Accessibility, as the helper sees them
	MethodPermissionsRequest = "permissions.request" // request: show macOS's prompt for one, then report

	MethodCaptureDevices = "capture.devices" // request: the audio inputs
	MethodCaptureStart   = "capture.start"   // request, job: records until capture.stop
	MethodCaptureStop    = "capture.stop"    // request: ends a capture.start, which answers first

	MethodHotkeyConfigure = "hotkey.configure" // request: the bindings the event tap watches (replaces all)
	MethodHotkeyStatus    = "hotkey.status"    // request: the tap's state and counters

	MethodAppFrontmost = "app.frontmost" // request: the app in front

	MethodTextFocus     = "textio.focus"     // request: the focused element of the app in front (can it take text?)
	MethodTextSelection = "textio.selection" // request: the selected text (Accessibility, else ⌘C when allowed)
	MethodTextPaste     = "textio.paste"     // request: insert text through the clipboard and ⌘V, then restore it

	MethodAppList      = "app.list"      // request: running (and installed) apps, to pick from
	MethodAppConfigure = "app.configure" // request: which apps get AXManualAccessibility as they come to the front

	MethodBrowserURL = "browser.url" // request: the front tab's URL of a browser (Accessibility only)

	// Notifications, helper → app.
	MethodCaptureStarted   = "capture.started"   // a capture.start job records: which device, how long it took
	MethodCaptureLevel     = "capture.level"     // input meter of a capture.start job, ~20 Hz
	MethodCapturePCM       = "capture.pcm"       // audio of a capture.start job, a frame every ~100 ms
	MethodCaptureRestarted = "capture.restarted" // the job's audio engine was rebuilt (watchdog, device change)
	MethodHotkeyEvent      = "hotkey.event"      // a binding fired (or Escape while active)
	MethodHotkeyState      = "hotkey.state"      // the event tap was installed or lost (e.g. Accessibility granted)
	MethodAppActivated     = "app.activated"     // another app came to the front
	MethodLog              = "log"               // a log line (stderr stays free-form text)
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

// ---- permissions.* ----

// PermissionsResult answers permissions.status and permissions.request.
// macOS checks the helper's permissions against Auralink.app, its
// responsible process: they are the app's.
type PermissionsResult struct {
	Microphone    string `json:"microphone"`    // a Perm* state
	Accessibility bool   `json:"accessibility"` // AXIsProcessTrusted: the event tap, AX reads, posting keys
}

// Microphone states.
const (
	PermGranted       = "granted"
	PermDenied        = "denied"
	PermNotDetermined = "notDetermined"
	PermRestricted    = "restricted"
)

// PermissionsRequestParams asks for one permission: the microphone shows
// macOS's prompt once (answered when the user chose); accessibility shows
// the prompt that adds Auralink to the list in System Settings (answered
// at once: the user grants it there).
type PermissionsRequestParams struct {
	Permission string `json:"permission"` // PermissionMicrophone | PermissionAccessibility
}

// Permissions, as in permissions.request and permission_denied's data.
const (
	PermissionMicrophone    = "microphone"
	PermissionAccessibility = "accessibility"
)

// ---- capture.* ----

// DevicesResult answers capture.devices.
type DevicesResult struct {
	Devices []CaptureDevice `json:"devices"`
}

// CaptureDevice is an audio input.
type CaptureDevice struct {
	ID         string `json:"id"` // Core Audio device UID, stable across restarts
	Name       string `json:"name"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sampleRate"`
	Default    bool   `json:"default"`             // the system's default input
	Transport  string `json:"transport,omitempty"` // "builtin" | "usb" | "bluetooth" | "continuity" | "aggregate" | "virtual"
}

// CaptureParams starts recording. The capture.start job sends
// capture.started once the audio engine runs, then capture.level and
// capture.pcm, and answers with a CaptureResult when capture.stop,
// MaxSeconds or the input ends it; job.cancel discards it. It fails (and
// sends no capture.started) when no input starts: permission_denied,
// device_unavailable, busy.
type CaptureParams struct {
	// Devices are device ids in the user's order of preference: the first
	// one present that starts records, else the system's default input
	// (CaptureStarted.FellBack). Empty: the default input.
	Devices []string `json:"devices,omitempty"`
	// SampleRate of capture.pcm (default 16000). The audio is always mono
	// PCM16 little endian.
	SampleRate int `json:"sampleRate,omitempty"`
	// FrameMS is the audio in each capture.pcm (default 100); LevelHz the
	// rate of capture.level (default 20).
	FrameMS int `json:"frameMs,omitempty"`
	LevelHz int `json:"levelHz,omitempty"`
	// MaxSeconds ends the recording on its own (0 = until stopped): a
	// guard against a capture.stop that never comes.
	MaxSeconds float64 `json:"maxSeconds,omitempty"`
}

// CaptureStartedParams: the job records. Until it comes the user's first
// words are not being heard (200–400 ms after capture.start).
type CaptureStartedParams struct {
	Job        string `json:"job"`
	Device     string `json:"device"` // the device that records
	DeviceName string `json:"deviceName"`
	// FellBack: not the first choice, which failed (FallbackReason).
	FellBack         bool    `json:"fellBack"`
	FallbackReason   string  `json:"fallbackReason,omitempty"`
	DeviceSampleRate int     `json:"deviceSampleRate"`
	DeviceChannels   int     `json:"deviceChannels"`
	SampleRate       int     `json:"sampleRate"` // of capture.pcm
	StartMS          float64 `json:"startMs"`    // capture.start to the engine running
}

// LevelParams is the input meter, in dBFS (-120 is silence).
type LevelParams struct {
	Job     string  `json:"job"`
	Seconds float64 `json:"seconds"` // of audio since the start
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
}

// PCMParams carries recorded audio: mono PCM16 little endian at the
// started sample rate. Seq counts from 0 without gaps; Offset is the
// sample offset of its first sample. Data is base64 in JSON (Go []byte,
// Swift Data). The last frame may be shorter.
type PCMParams struct {
	Job    string `json:"job"`
	Seq    int    `json:"seq"`
	Offset int    `json:"offset"`
	Data   []byte `json:"data"`
}

// RestartedParams: the job's audio engine was rebuilt (Reason
// CaptureNoCallback, CaptureConfigurationChange or CaptureDeviceLost for
// the default input, which follows the new default). Recording goes on.
type RestartedParams struct {
	Job     string `json:"job"`
	Reason  string `json:"reason"`
	Attempt int    `json:"attempt"`
	Device  string `json:"device"` // the device it records from now
}

// CaptureResult describes the recording. Every capture.pcm of the job
// was sent before it.
type CaptureResult struct {
	Device string `json:"device"` // the last device that recorded
	// Reason is why it ended: CaptureStopped, CaptureMaxSeconds,
	// CaptureDeviceLost (the chosen input went away) or CaptureNoCallback
	// (the input stopped delivering audio, restarts did not help).
	Reason   string  `json:"reason"`
	Frames   int     `json:"frames"`  // capture.pcm notifications sent
	Samples  int     `json:"samples"` // at the started sample rate
	Seconds  float64 `json:"seconds"`
	Restarts int     `json:"restarts"`
	Peak     float64 `json:"peak"` // dBFS
	// FirstFrameMS is capture.start to the first capture.pcm (0: none).
	FirstFrameMS float64 `json:"firstFrameMs,omitempty"`
	CPUSeconds   float64 `json:"cpuSeconds"` // the helper's CPU time while it recorded
}

// Capture end and restart reasons.
const (
	CaptureStopped             = "stopped"
	CaptureMaxSeconds          = "maxSeconds"
	CaptureDeviceLost          = "deviceLost"
	CaptureNoCallback          = "noCallback"
	CaptureConfigurationChange = "configurationChange"
)

// CaptureStopParams names the capture.start job to stop.
type CaptureStopParams struct {
	Job string `json:"job"`
}

// CaptureStopResult says whether the capture was running; its job then
// answered (before this response).
type CaptureStopResult struct {
	Stopped bool `json:"stopped"`
}

// ---- hotkey.* ----

// HotkeyBinding is one shortcut the event tap watches: a modifier-only
// chord (Key nil: fn, fn+⇧, right ⌘…) or a key with modifiers (⌃⌘V).
// The helper's recognizer decides on the tap's thread, synchronously,
// whether to swallow each key event, as macOS requires.
type HotkeyBinding struct {
	ID string `json:"id"` // the app's name for it: "transcribe/0"
	// Modifiers maps each modifier family the binding needs (Mod*) to its
	// side (Side*); families not listed must be up.
	Modifiers map[string]string `json:"modifiers"`
	// Key is the virtual key code (kVK_*, 0 is A) of a key binding.
	Key      *int   `json:"key,omitempty"`
	Behavior string `json:"behavior"` // BehaviorTap | BehaviorDoubleTap | BehaviorLongPress
	// Swallow, for a key binding, keeps its key events from the app in
	// front (default true). Modifier-only chords are never swallowed, but
	// fn with HotkeyConfigureParams.SwallowFn.
	Swallow *bool `json:"swallow,omitempty"`
}

// Modifier families and sides.
const (
	ModFn      = "fn"
	ModShift   = "shift"
	ModControl = "control"
	ModOption  = "option"
	ModCommand = "command"

	SideAny   = "any"
	SideLeft  = "left"
	SideRight = "right"
)

// Hotkey behaviours and phases.
const (
	BehaviorTap       = "tap"
	BehaviorDoubleTap = "doubleTap"
	BehaviorLongPress = "longPress"

	PhaseTrigger = "trigger" // tap or doubleTap fired; a key binding's tap fires on key down
	PhaseDown    = "down"    // longPress: held long enough
	PhaseUp      = "up"      // longPress: released after down
)

// HotkeyEscape is the binding of the hotkey.event sent for Escape while
// Active (a recording runs); Escape is then swallowed.
const HotkeyEscape = "escape"

// HotkeyConfigureParams replaces the bindings; an empty list removes the
// event tap. Changing only the timings or Active keeps a chord in progress:
// the app flips Active as a session starts and ends.
type HotkeyConfigureParams struct {
	Bindings []HotkeyBinding `json:"bindings"`
	// DoubleTapMS is the double-tap window: a second press starting
	// within it of the first release makes a double tap (default: the
	// system's double-click interval, at most 800, else 500). LongPressMS
	// is the hold that fires a long press (300), TapMaxMS the longest
	// press that is still a tap (1500: real ⇧+fn presses took up to 983 ms).
	DoubleTapMS float64 `json:"doubleTapMs,omitempty"`
	LongPressMS float64 `json:"longPressMs,omitempty"`
	TapMaxMS    float64 `json:"tapMaxMs,omitempty"`
	// SwallowFn swallows fn's own events (and the 🌐 key's that follow)
	// while an fn chord is bound, so the 🌐 action does not run.
	SwallowFn bool `json:"swallowFn,omitempty"`
	// Active: a session records. A tap fires at once (no wait for a
	// double tap), and Escape is reported and swallowed.
	Active bool `json:"active,omitempty"`
}

// HotkeyStatus answers hotkey.configure and hotkey.status, and is
// hotkey.state's params. Without Accessibility the tap cannot be
// installed: the helper retries every 2 s and sends hotkey.state when it
// gets in; meanwhile the app falls back to MyGo's Carbon shortcuts for
// plain key combinations.
type HotkeyStatus struct {
	Installed     bool `json:"installed"`
	Accessibility bool `json:"accessibility"`
	Bindings      int  `json:"bindings"`
	Events        int  `json:"events"` // keyboard events seen
	// DisabledByTimeout counts the times macOS disabled the tap (the
	// helper enables it again); MissedEvents the modifier events found
	// missing (a release macOS did not deliver), after which the chord in
	// progress was dropped.
	DisabledByTimeout int     `json:"disabledByTimeout"`
	MissedEvents      int     `json:"missedEvents"`
	MaxCallbackMicros float64 `json:"maxCallbackMicros"` // slowest tap callback
	Error             string  `json:"error,omitempty"`
}

// HotkeyEventParams reports a binding that fired.
type HotkeyEventParams struct {
	Binding  string  `json:"binding"` // HotkeyBinding.ID, or HotkeyEscape
	Phase    string  `json:"phase"`
	Behavior string  `json:"behavior"`
	HeldMS   float64 `json:"heldMs,omitempty"` // how long the chord was held, when known
	T        float64 `json:"t"`                // when it was decided, ms of the helper's monotonic clock
	// Front is the app in front as the key went down: the start of the
	// session's context snapshot (plan §4.2).
	Front *App `json:"front,omitempty"`
}

// ---- app.* ----

// App is a running app (app.frontmost, app.activated, a hotkey's front).
type App struct {
	PID      int    `json:"pid"`
	BundleID string `json:"bundleId,omitempty"`
	Name     string `json:"name,omitempty"`
}

// ---- textio.* ----

// FocusResult describes the focused element of the app in front, for
// deciding whether a result can be inserted there.
type FocusResult struct {
	PID     int    `json:"pid"`
	Role    string `json:"role,omitempty"` // "AXTextArea", "AXTextField", "AXWebArea"…
	Subrole string `json:"subrole,omitempty"`
	// Editable: its value can be set, or it is a text role. Selection is
	// the selected range (UTF-16), when it has one.
	Editable    bool       `json:"editable"`
	Selection   *TextRange `json:"selection,omitempty"`
	ValueLength int        `json:"valueLength"` // UTF-16 units of its value (0: none or unknown)
	Error       string     `json:"error,omitempty"`
}

// TextRange is a range of UTF-16 units.
type TextRange struct {
	Location int `json:"location"`
	Length   int `json:"length"`
}

// SelectionParams reads the selected text of the app in front:
// Accessibility first (the selected text, the text of the selected range,
// WebKit's and Chromium's text markers), then, with AllowCopy, ⌘C, waiting
// up to CopyTimeoutMS (default 300) for the clipboard to change, which is
// then put back. The app decides AllowCopy (not where ⌘C copies a whole
// line on an empty selection: VS Code, Cursor…).
type SelectionParams struct {
	AllowCopy     bool `json:"allowCopy,omitempty"`
	CopyTimeoutMS int  `json:"copyTimeoutMs,omitempty"`
}

// SelectionResult is the text ("" for none) and how it was read.
type SelectionResult struct {
	Text  string   `json:"text,omitempty"`
	Via   string   `json:"via"` // Via*
	MS    float64  `json:"ms"`
	Steps []string `json:"steps,omitempty"` // what was tried, for the log
}

// How a selection was read.
const (
	ViaAX       = "ax"
	ViaAXRange  = "ax-range"
	ViaAXMarker = "ax-marker"
	ViaCopy     = "copy"
	ViaNone     = "none"
)

// PasteParams inserts Text where the cursor is: the clipboard is
// snapshotted (every item and type), Text written (marked transient for
// clipboard managers), ⌘V posted, then the keys in Press, and after
// RestoreDelayMS the snapshot written back unless someone wrote to the
// clipboard meanwhile.
type PasteParams struct {
	Text string `json:"text"`
	// KeepClipboard leaves Text on the clipboard instead of restoring.
	KeepClipboard  bool `json:"keepClipboard,omitempty"`
	RestoreDelayMS int  `json:"restoreDelayMs,omitempty"` // default 500
	// TargetPID, when set, must be the app in front: otherwise nothing is
	// pasted (Posted false, Front says who is in front). The app activates
	// the target itself (macx.Activate) when it has to.
	TargetPID int `json:"targetPid,omitempty"`
	// Press are keys posted after ⌘V (app enhancement's auto key: Return,
	// ⌘Return…), PressDelayMS (default 120) after it.
	Press        []KeyPress `json:"press,omitempty"`
	PressDelayMS int        `json:"pressDelayMs,omitempty"`
}

// KeyPress is a key (kVK_*) with modifier families (Mod*, not fn).
type KeyPress struct {
	Key       int      `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
}

// PasteResult says what happened.
type PasteResult struct {
	Posted bool `json:"posted"`
	// Restored: the old clipboard is back. ClipboardTaken: someone wrote
	// to the clipboard before the restore, which was then skipped.
	Restored       bool    `json:"restored"`
	ClipboardTaken bool    `json:"clipboardTaken"`
	MS             float64 `json:"ms"`
	Front          *App    `json:"front,omitempty"` // when not posted: the app in front
}

// ---- browser.url ----

// BrowserURLParams asks for the front tab's URL of a browser, read
// through Accessibility only (no AppleScript, no Automation consent): the
// web area that holds the focus, the focused window's AXDocument, then the
// first web area's AXURL. PID picks the process (the hotkey's front);
// else BundleID's first running one; neither: the app in front.
type BrowserURLParams struct {
	BundleID string `json:"bundleId,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

// BrowserURLResult is the URL ("" when none was read) and how. A URL is
// used only to match app enhancement rules, never sent to a model.
type BrowserURLResult struct {
	BundleID string  `json:"bundleId"`
	URL      string  `json:"url,omitempty"`
	Via      string  `json:"via"` // URLVia*
	MS       float64 `json:"ms"`
	// Error says why none was read, for the log and the App enhancement
	// page ("no focused window", "no web area").
	Error string `json:"error,omitempty"`
	// Retried: the first read found no web area in a Chromium browser, so
	// the helper set AXManualAccessibility on it (once per process) and
	// read again after a short wait; URL is the second read's.
	Retried bool `json:"retried,omitempty"`
}

// How a URL was read.
const (
	URLViaAXFocus    = "ax-focus"    // the AXURL of the web area around the focused element
	URLViaAXDocument = "ax-document" // the focused window's AXDocument
	URLViaAXWebURL   = "ax-weburl"   // the AXURL of the window's first web area
	URLViaNone       = "none"
)

// ---- app.configure ----

// AppConfigureParams says which apps the helper gives Chromium's
// AXManualAccessibility as they come to the front (once per process),
// besides the Electron apps it finds itself: Chromium builds a page's
// accessibility tree only when asked, and only a moment later, so
// browser.url and textio find it when a shortcut comes. The app sends its
// Chromium browsers (internal/appctx) while app enhancement is on; it
// replaces the last list, and a new helper has none. The result is Empty.
type AppConfigureParams struct {
	ManualAccessibility []string `json:"manualAccessibility,omitempty"` // bundle ids
}

// ---- app.list ----

// AppListParams lists apps: the running ones (regular apps, with a Dock
// icon), and with Installed also those in /Applications,
// /System/Applications and ~/Applications (one level of folders down).
type AppListParams struct {
	Installed bool `json:"installed,omitempty"`
}

// AppListResult lists apps by name, each bundle id once.
type AppListResult struct {
	Apps []AppInfo `json:"apps"`
}

// AppInfo is an app the user may pick (App enhancement's members).
type AppInfo struct {
	BundleID string `json:"bundleId"`
	Name     string `json:"name"`
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`  // when running
	Path     string `json:"path,omitempty"` // its bundle
}

// ParamsFor returns a new value of the params type of method, or nil for
// an unknown method. ResultFor does the same for results. The fake helper
// and the fixture tests use them.
func ParamsFor(method string) any {
	switch method {
	case MethodHello:
		return new(HelloParams)
	case MethodShutdown, MethodCaptureDevices, MethodPermissionsStatus, MethodHotkeyStatus, MethodAppFrontmost, MethodTextFocus:
		return new(Empty)
	case MethodJobCancel:
		return new(CancelParams)
	case MethodPermissionsRequest:
		return new(PermissionsRequestParams)
	case MethodCaptureStart:
		return new(CaptureParams)
	case MethodCaptureStop:
		return new(CaptureStopParams)
	case MethodHotkeyConfigure:
		return new(HotkeyConfigureParams)
	case MethodTextSelection:
		return new(SelectionParams)
	case MethodTextPaste:
		return new(PasteParams)
	case MethodBrowserURL:
		return new(BrowserURLParams)
	case MethodAppList:
		return new(AppListParams)
	case MethodAppConfigure:
		return new(AppConfigureParams)
	case MethodCaptureStarted:
		return new(CaptureStartedParams)
	case MethodCaptureLevel:
		return new(LevelParams)
	case MethodCapturePCM:
		return new(PCMParams)
	case MethodCaptureRestarted:
		return new(RestartedParams)
	case MethodHotkeyEvent:
		return new(HotkeyEventParams)
	case MethodHotkeyState:
		return new(HotkeyStatus)
	case MethodAppActivated:
		return new(App)
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
	case MethodShutdown, MethodAppConfigure:
		return new(Empty)
	case MethodJobCancel:
		return new(CancelResult)
	case MethodPermissionsStatus, MethodPermissionsRequest:
		return new(PermissionsResult)
	case MethodCaptureDevices:
		return new(DevicesResult)
	case MethodCaptureStart:
		return new(CaptureResult)
	case MethodCaptureStop:
		return new(CaptureStopResult)
	case MethodHotkeyConfigure, MethodHotkeyStatus:
		return new(HotkeyStatus)
	case MethodAppFrontmost:
		return new(App)
	case MethodTextFocus:
		return new(FocusResult)
	case MethodTextSelection:
		return new(SelectionResult)
	case MethodTextPaste:
		return new(PasteResult)
	case MethodBrowserURL:
		return new(BrowserURLResult)
	case MethodAppList:
		return new(AppListResult)
	}
	return nil
}

// Requests lists the requests of the protocol, Notifications its
// notifications.
var (
	Requests = []string{
		MethodHello, MethodShutdown, MethodJobCancel,
		MethodPermissionsStatus, MethodPermissionsRequest,
		MethodCaptureDevices, MethodCaptureStart, MethodCaptureStop,
		MethodHotkeyConfigure, MethodHotkeyStatus,
		MethodAppFrontmost, MethodAppList, MethodAppConfigure,
		MethodTextFocus, MethodTextSelection, MethodTextPaste,
		MethodBrowserURL,
	}
	Notifications = []string{
		MethodCaptureStarted, MethodCaptureLevel, MethodCapturePCM, MethodCaptureRestarted,
		MethodHotkeyEvent, MethodHotkeyState, MethodAppActivated, MethodLog,
	}
)

// IsJob reports whether method is a long-running, cancellable job.
func IsJob(method string) bool { return method == MethodCaptureStart }
