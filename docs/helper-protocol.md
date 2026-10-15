# Helper protocol 1

How Auralink (Go) talks to `auralink-helper` (Swift). Go types: `internal/helper/proto`. Golden transcripts: `helper/Fixtures/*.jsonl`. Supervisor: `internal/helper`.

It is a subset of AudioSmith's engine protocol 2 (`docs/protocol-v2.md` there): the same envelope, handshake and error shape, with one helper process instead of an engine per role, and a few methods of its own.

> **Provisional.** `helper.hello`, `helper.shutdown`, `job.cancel`, the envelope and the error codes are settled. The capture, hotkey and browser methods are what the plan expects; the M0 spikes on a Mac decide their final shape. Change the Go types, the fixtures and this page together.

## 1. Transport

- The app starts the helper with no arguments; its working directory is the executable's.
- One JSON message per line (UTF-8, `\n`), at most 4 MiB (`proto.MaxLine`); app → helper on stdin, helper → app on stdout. stderr is free-form text: the supervisor writes it to `~/Library/Logs/Auralink/helper.log` and to the logs panel (source `helper`).
- The helper exits when stdin closes or on `helper.shutdown`.

## 2. Messages

| Shape | Members | Direction |
|---|---|---|
| request | `id`, `method`, `params`? | app → helper |
| notification | `method`, `params`? (no `id`) | helper → app |
| response | `id` + `result` or `id` + `error` | helper → app |

No `jsonrpc` member; ids are strings (`c1`… for control requests, the session's ULID for a capture). Errors are `{"code": "…", "message": "…", "data"?: {…}}` with a string code:

| Code | When | data |
|---|---|---|
| `bad_request` | malformed params | |
| `method_not_found` | unknown method | |
| `not_initialized` | a request before `helper.hello` | |
| `protocol_mismatch` | hello with another protocol | `{"supported":[1]}` |
| `busy` | a recording runs already | |
| `cancelled` | the job was cancelled | |
| `permission_denied` | macOS refused | `{"permission":"microphone"\|"accessibility"\|"automation"}` |
| `device_unavailable` | no input device gave audio | `{"device":"…"}` |
| `unsupported` | e.g. a browser that cannot be scripted | `{"capability":"…"}` |
| `timeout` | the helper gave up waiting on macOS | |
| `internal` | anything else | |

## 3. Sequencing

- `helper.hello` comes first; until it is answered every other request fails with `not_initialized`. The app refuses a helper whose `protocol` is not 1 (it is not restarted: rebuild it).
- Requests are answered in any order. Control requests are answered promptly, even while a job runs.
- **Jobs** (`capture.start`) send notifications naming them in `job` (`capture.level`, `capture.pcm`, `log`) before their response; `job.cancel` makes them answer `cancelled`. `capture.stop` and `job.cancel` are answered after the job they end.
- Notifications without `job` belong to no request: `hotkey.event` (and `log`). The supervisor hands them to `Config.OnNotify`; a notification naming a job that already answered is dropped.
- If the helper exits, its calls fail (`core.ErrHelper`) and the supervisor restarts it after 1 s, 2 s, 4 s… (at most 3 times in 10 minutes).

## 4. Methods

| Method | Kind | Params → result |
|---|---|---|
| `helper.hello` | control | `{protocol, app, logLevel?}` → `{protocol, version, pid, methods, device{name, os}}` |
| `helper.shutdown` | control | → `{}`, then exit 0 |
| `job.cancel` | control | `{job}` → `{cancelled}` |
| `capture.devices` | control | → `{devices: [{id, name, channels, sampleRate, default?, transport?}]}` |
| `capture.start` | **job** | `{devices?, sampleRate: 16000, channels: 1, encoding: "pcm16le", frameMs?, maxSeconds?, output?}` → `{device, fallback?, audioSeconds, frames, peak, reason, output?}` |
| `capture.stop` | control | `{job}` → `{stopped}` |
| `hotkey.configure` | control | `{bindings: [{id, keys, trigger}], doubleMs?, longMs?}` → `{active, trusted}` |
| `browser.url` | control | `{bundleId, pid?, timeoutMs?}` → `{url, title?, source: "applescript"\|"ax"}` |
| `automation.probe` | control | `{bundleIds, prompt?}` → `{browsers: [{bundleId, state}]}` |

Notifications:

| Notification | Params |
|---|---|
| `capture.level` | `{job, seconds, rms, peak, clipped?}` (dBFS, ~20 Hz) |
| `capture.pcm` | `{job, seq, sampleRate, channels, encoding, data}` (`data` base64; `seq` from 0 without gaps; ~100 ms each) |
| `hotkey.event` | `{binding, action: "down"\|"up"\|"tap"\|"double"\|"long", app?: {bundleId, name, pid}}` |
| `log` | `{level, message, job?}` |

Notes:

- **capture.start** tries `devices` in order, then the default input, moving on when a device yields no audio within 1.2 s (Voxt's watchdog); the result says which one recorded and whether it was a fallback. `output` also writes a WAV.
- **hotkey.configure** replaces every binding. Keys follow `settings.Hotkey` (`Fn`, `Fn+Shift`, `RightCommand`, `Control+Command+V`). Without Accessibility the tap cannot be installed: `trusted` is false and `active` empty, and the app falls back to MyGo's Carbon shortcuts for plain key combinations. `hotkey.event`'s `app` is the app in front when the key went down: the start of the context snapshot (plan §4.2).
- **browser.url** answers for Safari, Chrome, Edge, Brave, Arc and browsers the user adds. A URL is used only to match app enhancement rules, never sent to a model.
- **automation.probe** reports `granted`, `denied`, `notDetermined`, `notRunning` (macOS answers only for a running app) or `unknown`; `prompt` shows macOS's consent prompt for those not determined.

## 5. Testing

- `go test ./internal/helper/proto`: every fixture line decodes strictly with the Go types and encodes back to the same JSON; the sequencing rules hold; every method has a fixture.
- `go test ./internal/helper`: the supervisor against `helpertest`, the fake helper the test binary becomes (`AURALINK_FAKE_HELPER`): handshake, capture with PCM frames, cancel, a stuck cancel, crash and restart, protocol mismatch, missing executable, hotkey events, fixture replay.
- The Swift helper's tests will decode `helper/Fixtures/*.jsonl` too.
