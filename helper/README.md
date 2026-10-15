# auralink-helper

The small Swift process Auralink runs beside its window, for what MyGo cannot do from Go:

- **capture**: record the microphone (AVAudioEngine) and stream 16 kHz mono PCM16 to the app (`capture.*`), with a watchdog that falls back to the default input when the chosen device gives no audio;
- **hotkeys**: watch the keyboard with a CGEventTap, so that `fn`, a single modifier, double taps and long presses can be shortcuts, deciding on its own thread whether to swallow the key (`hotkey.configure`, `hotkey.event`);
- **browser URLs**: read the front tab's URL with AppleScript, or the Accessibility tree when that fails, and report each browser's Automation permission (`browser.url`, `automation.probe`).

It speaks the helper protocol (`docs/helper-protocol.md`) over stdin and stdout; `internal/helper` supervises it (handshake, restarts, stderr to `~/Library/Logs/Auralink/helper.log`).

## Status

**There is no Swift code yet.** The M0 spikes (thread #1, `~/helper/auralink-spikes`) try capture, the event tap, AppleScript and the Accessibility attribution of a child process on a Mac; their results decide the Swift code and may rename the provisional methods. Until then:

- `Fixtures/*.jsonl` are the protocol's golden transcripts; the Go tests decode them, and the Swift tests will too.
- `Support/helper.entitlements` gives the helper `com.apple.security.device.audio-input` (mygo.json `macos.helperEntitlements`).
- `internal/helper/helpertest` is a fake helper in Go. `scripts/stage-fake-helper.sh` builds it into `resources/darwin-arm64/helper/auralink-helper`, where a packaged app looks for the real one, so `go tool mygo build` works and the app's supervisor can be tried on a Mac. It records a tone, not the microphone.

## Layout (planned)

```
helper/
  Package.swift            Swift 6, macOS 15, no third-party dependencies
  Sources/AuralinkHelper/  Server (routing, hello), Capture, Hotkeys, Browser
  Tests/                   fixture tests over Fixtures/*.jsonl
  Fixtures/                golden transcripts (shared with Go)
  Support/                 entitlements
```

`scripts/build-helper.sh` (to come) will build it on the Mac (`scripts/mac.sh scripts/build-helper.sh`) into `resources/darwin-arm64/helper/`.
