# Auralink architecture

*State: M1, the skeleton (2026-10-15). Product plan: the project's `plan/auralink-plan.md` (§4 architecture, §5 data, §8 engineering). Behaviour compared with Voxt: [behavior.md](behavior.md). Helper wire format: [helper-protocol.md](helper-protocol.md).*

Auralink is a Go app on MyGo (native UI, no cgo) with one small Swift helper process. It reuses AudioSmith's engineering (AudioSmith @f171c86, Apache-2.0): its window shell, UI kit and tokens, settings, store, i18n, logs and notification code, and its engine supervisor reduced to one helper. What it took and how it changed is in §5.

## 1. Package map

```
github.com/hehehai/Auralink
├── *.go                  package main: the UI (views + view state) and the wiring
├── internal/
│   ├── core/             event bus, ULIDs, error sentinels, notifications and log lines        (leaf)
│   ├── settings/         the typed, versioned settings document (v1) + Service                 (leaf)
│   ├── store/            persistence contract: Documents, History, Dictionary
│   │   └── sqlitestore/  SQLite (MyGo plugin): migrations, backup, FTS5 trigram, maintenance
│   ├── helper/           supervisor of auralink-helper: handshake, restarts, stderr to the log
│   │   ├── proto/        the helper protocol's wire types (leaf); fixtures in helper/Fixtures/
│   │   └── helpertest/   a fake helper for tests (the test binary re-runs itself); cmd/fakehelper
│   ├── i18n/             T / N / X keyed by English source; locales/*.json; dates              (leaf)
│   ├── icons/            Solar icons (names.txt → solar_gen.go)
│   ├── logs/             log ring + rotated files
│   ├── notify/           the notification centre (kept in the store's documents)
│   ├── lang/             the language table, 652 languages                                     (leaf)
│   ├── secrets/          API keys in the Keychain, service "Auralink"                          (leaf)
│   ├── netx/             the proxy (settings.Network) and the HTTP client every provider uses  (leaf)
│   └── permission/       Microphone and Accessibility (purego on darwin; Fake for tests)       (leaf)
├── helper/               the Swift helper (to come: helper/README.md), Fixtures/, Support/
└── third_party/mygo/     MyGo 0.4.8 with two patches (scripts/vendor-mygo.sh)
```

Later milestones add, as the plan's §4.1 lists: `internal/asr` and the `internal/llm` taken from AudioSmith (M2), `internal/session`, `compose`, `deliver`, `textio`, `hotkey` (M3), `appctx` (M5).

### Import rules

Arrows point to what a package may import. No cycles. Leaves import nothing internal.

```
main ──▶ every internal package
store ──▶ core                 store/sqlitestore ──▶ core, store
helper ──▶ core, helper/proto  helper/helpertest ──▶ helper/proto
notify, logs ──▶ core
core, settings, helper/proto, i18n, lang, secrets, netx, permission ──▶ (nothing internal)
icons ──▶ mygo/ui
```

The domain packages (session, compose, appctx, asr, llm…) will be pure Go behind narrow interfaces, with the macOS parts (recording, the event tap, AX, AppleScript, pasting) behind them: in the helper, or in purego code like `permission`. That keeps their logic testable on Linux with fakes (plan §1.4).

Event types live next to their publisher, and their topic starts with the publisher's name. They satisfy `core.Event` (a `Topic() string` method) without importing `core`:

| Event | Topic | Publisher |
|---|---|---|
| `core.NotificationPosted`, `core.LogAppended` | `notification.posted`, `log.appended` | notify, logs |
| `settings.Changed` | `settings.changed` | the settings store, after saving |
| `helper.StatusChanged` | `helper.status` | the supervisor |

### Why the UI stays in package main

MyGo builds the root `main` package (`mygo dev`, `mygo build`, `ui.View`); views need the window and its main thread. The domain, which must be goroutine-safe and testable, is in `internal/`. Each area of the UI keeps its fields in a struct of its own, embedded in `app` (app.go).

## 2. Processes and data flow

```
          MyGo main thread                          goroutines
┌──────────────────────────────┐  calls (go …)  ┌──────────────────────────────────────────┐
│ views (build pass: no I/O)   │──────────────▶ │ library: store (SQLite) · bus · settings │
│ app view state + snapshots   │                │ helper.Supervisor ──stdio JSON lines──▶ auralink-helper (Swift)
│        ▲ Window.Update       │                │    capture.* · hotkey.* · browser.url    │
└────────┼─────────────────────┘                └──────────────────────────────────────────┘
   core.Bus subscribers ◀── Publish ─────────────┘
   (wiring code, once)
```

main.go wires, in order: the single-instance lock → `resolveDataDir` (finishes a pending move or erase of the data folder) → `openLibrary` (store migrated, bus, settings) → `newHelperHost` (finds the helper) → `newApp` → `connectShell` (log files, deep links) → `connectHelper` (the supervisor, its status and notifications). When MyGo is ready: the window, `startHelper`, the menu bar icon. On quit: the tray, the helper (`helper.shutdown`), the notifications and logs, then the database.

There is no job queue (plan §5): a session (M3) lives in memory, owned by one state machine, and writes one history entry when it ends.

## 3. Threading and state rules

1. **The build pass does no I/O.** A view reads only `app` fields, snapshots and memory-only getters (`settings.Service.Get`, `i18n.T`). Store, Keychain, network and helper calls happen in handlers that start a goroutine (`a.async`, `helperCall`).
2. **`app` is main-thread only.** Goroutines reach it through `a.post(fn)` (`Window.Update`). In tests `post` runs fn at once, or queues it (`postQueue`) in tests that run real goroutines (the supervisor against the fake helper process).
3. **Bus subscriptions are set up once, in wiring code** (`openShell`, `connectHelper`), never in a build pass. Handlers must not block. Publishers publish after their change is durable, never while holding a lock.
4. **Domain packages are goroutine-safe**, take a `context.Context` when they may block, return copies, and wrap `core.Err*` sentinels.
5. **MyGo lifetime rules hold** (`.agents/skills/mygo-maintenance`): keys before stateful widgets; never store elements; dialogs on goroutines; persistent callbacks capture `c.Services()`, not the `Context`.
6. **The helper is stateless.** Truth (history, dictionary, settings, rules, keys) lives in Go. A helper crash fails its calls (`core.ErrHelper`) and the supervisor restarts it; nothing is lost. Late notifications of a finished job are dropped.

### Seams inside package main

| Seam | Defined in | Used by |
|---|---|---|
| `a.lib` (`*library`: store, bus, settings) | library.go | every area |
| `a.navigate(path)`, `a.showPage(name)`, `a.currentPage()`, `a.shownPath()` | shell.go, shell_settings.go | palette, deep links, notifications, tests |
| `navGroups`, `shortcutPages` (⌘1–⌘6), `pageTitle`, `a.navBadge` | shell.go | sidebar, rail, palette, Go menu |
| `a.page(c, p)`, `placeholderText`, `a.pageShown(path)` | pages.go | the router; milestones replace a page's placeholder here |
| `a.updateSettings(fn)`, `a.prefs`, `settingsSections()` | shell_settings.go | every area that changes a setting |
| `a.helperStatus()`, `helperCall[T](a, method, params, timeout, then)`, `a.helperNotified(m)`, `a.restartHelper()` | helperlink.go | status bar, permissions, Diagnostics; sessions (M3) |
| `a.permSys` (`permission.System`), `a.recheckPermissions()`, `a.probeAutomation(prompt, ids)`, `a.browsers()` | permissions.go | Permissions page, Diagnostics, onboarding (M6) |
| `a.toast(msg)`, `a.notify(…)`, `a.post(fn)`, `a.async(fn)` | app.go, shell_status.go | everyone |
| `hotkeyGlyphs(keys)` | shell_commands.go | ? sheet, menu bar, feature pages |
| Design tokens: `tok(t)`, `px`, `fs*`, `sp*`, `radius*`; `accentName` (teal) | ui_tokens.go | every view |
| UI kit (AudioSmith 0.3): buttons by rank, `pageHeader`, `card`, `formRow`, `settingRow`, `emptyState`, `notice`, `sidePanel`, `dialogFrame`… | ui_kit.go, shell_settings.go | every view |

## 4. Data on disk (macOS)

| What | Where |
|---|---|
| Database: history (+ `history_fts`), dictionary terms, documents (`settings:app`, `appctx:rules`, `notifications`) | `~/Library/Application Support/Auralink/auralink.db` (WAL; `auralink.db.bak-<v>` before each migration) |
| Recordings kept with history entries | `…/Auralink/audio/<id>.wav` (only with Settings → Privacy → Keep the recordings) |
| Data folder location | `…/Auralink/location.json` when the user moved it (Settings → Storage); `move.json`, `reset.json` while a move or an erase waits for the next start |
| Logs | `~/Library/Logs/Auralink/auralink.log` (app, rotated at 5 MB) and `helper.log` (the helper's stderr) |
| Secrets | the login Keychain, service "Auralink": `asr.<provider>`, `llm.<provider>` |
| Helper | `Auralink.app/Contents/Resources/helper/auralink-helper` (or `$AURALINK_HELPER`) |

## 5. What came from AudioSmith, and how it changed

| Auralink | From AudioSmith @f171c86 | Change |
|---|---|---|
| `ui_kit.go`, `ui_tokens.go` | same files | comments; `setAccent` (accent review shots) dropped |
| `shell*.go` | same files | Auralink's sidebar groups and pages (a first group without a title); status bar: provider placeholder and the helper instead of engine, jobs and downloads; no ⌘E quick switch; ? sheet lists the global shortcuts from the settings |
| `shell_settings.go`, `settings_*.go`, `storage_move.go`, `sysinfo.go` | same files | six sections: General (app and main language, menu bar, open at login), Appearance, Network (proxy only, provider hosts), Privacy (history, recordings, text in logs), Storage (Auralink's folders; erase removes provider keys), Diagnostics and about (helper instead of engine) |
| `permissions.go` | same file | a page of the sidebar; browsers' Automation through the helper |
| `tray.go` | `dictation_tray.go` | Auralink's menu; a sound-wave icon |
| `internal/core` | `core` | bus, ids, errors, notifications, log lines only; `Stream` dropped |
| `internal/settings` | `settings` | the Store kept; the document is new (v1, plan §3–§5) |
| `internal/store`, `sqlitestore` | same | Open, migrations, backup, `CheckFile`, `RewritePaths` kept; new schema and repositories; `Stats` instead of `Usage` |
| `internal/helper` (+ `proto`, `helpertest`) | `engine` (+ `proto`, `enginetest`) | one process instead of roles; no scheduler, memory budget or stats polling; notifications without a job go to `OnNotify`; protocol 1 with the helper's methods |
| `internal/secrets` | `hub/token.go` (`Keychain`, `Secret`) | service "Auralink"; no HF token |
| `internal/netx` | `hub/proxy.go` | unchanged rules |
| `internal/permission` | `dictation/system_darwin.go` | Microphone and Accessibility only |
| `internal/i18n`, `icons`, `logs`, `notify`, `lang` | same | zh-Hans rebuilt from what Auralink uses (304 strings); 38 icons; log file `auralink.log` |
| `third_party/mygo`, `scripts/vendor-mygo.sh`, `scripts/mac.sh`, CI | same | renamed |

Not taken: jobs, hub (downloads), catalog, licence, engine's MLX side, services, dsp, translate, text, doc, dub, api, mcp, workflow, telephony, watermark, gallery, voicedesign, audio. `internal/llm` and the dictation's paste and clipboard code come in M2 and M3.

## 6. Testing

| What | Command | Notes |
|---|---|---|
| All Go tests | `CGO_ENABLED=0 go test ./...` | Views in `ui.Tester` with a fake supervisor in process; the real supervisor against the fake helper process; protocol fixtures; the store over real SQLite (the plugin's library is downloaded and checked once, or `MYGO_SQLITE_LIBRARY`); i18n key parity |
| MyGo vet | `go tool mygo vet .` | `go vet ./...` and the UI-lifetime checks |
| Race | `CGO_ENABLED=1 go test -race ./...` | needs cgo |
| Snapshots | `AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .` | every page, the settings sections and the overlays, light/dark, en/zh-Hans, 2×; `AURALINK_SNAPSHOTS_SIZE=820x520` for the smallest window |
| Mac checks | `scripts/mac.sh 'AURALINK_MAC=1 go test -run "TestMacKeychain|TestNative" -v ./internal/secrets ./internal/permission'` | the Keychain and the permission calls |
| Packaged app | `scripts/mac.sh 'scripts/stage-fake-helper.sh && go tool mygo build'` | until the Swift helper exists, with the fake one |
| Text fit | `AURALINK_TEXTFIT_PROBE=1` / `AURALINK_TEXTFIT_AUDIT=1 go test -run 'TestTextFits|TestTranslationsFit' .` | meaningful on macOS (Core Text) |
