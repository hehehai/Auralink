# Diagnostic logs

*For the team reading a report, and for a user who is asked to send one. What the app writes to its logs when it starts, when it reads the Keychain, when a shortcut or a window does something, and how to get the lines out.*

## 1. Getting the logs

| What the user does | What they get | Has the new lines |
|---|---|---|
| Logs page (sidebar) → **Copy all lines shown** (or select lines and Copy) | The lines the page shows (the source filter applies: choose "All" to get everything) | Yes |
| Settings → 诊断与关于 → **Save a diagnostic bundle** (ZIP) | `diagnostics.txt`, `settings.json` and `logs/auralink.log` (plus the rotated `.1`, `.2`) | Yes, all of them |
| Settings → 诊断与关于 → **Copy diagnostics** | Versions, machine, folders, self-check, and the recent log lines (the newest 200, and only those of the last 30 minutes, whichever is fewer). No recordings, no history content, no keys. | Yes |

The log files are in the logs folder (Settings → Storage shows it). They rotate at 5 MB.

Lines have the form `date time LEVEL source message`, for example:

```
2026-10-29 09:14:02.118 INFO    app          start: Auralink 0.1.0 · data folder ~/Library/Application Support/Auralink (default) · database found · menu bar icon created · close hides to the menu bar: on · login item: off · bundle dev.hehehai.auralink, variant release
```

Sources: `app` (start-up, database, settings, keys, window, menu bar), `session` (sessions and shortcuts), `helper` (the Swift helper), `audio` (the system output, muted while recording).

What is never in a line: text the user wrote or heard, recordings, a key or part of one (not even the mask), a path with the user's name (`~` replaces it).

## 2. Start-up

One line per start, then the rest:

| Line | Level | Meaning |
|---|---|---|
| `start: Auralink <version> · data folder <path> (default) · database found · menu bar icon created · close hides to the menu bar: on · login item: off · bundle <id>, variant <variant>` | INFO | The summary. `(default)` means the default folder, `(moved, Settings → Storage)` another one. `database found` (or `new database`): there was a database before the start. `variant`: `release`, `dev`, `test`, `none (not an app bundle)`; `demo build` for the demo. |
| `data folder files: location.json absent, move.json absent, reset.json absent` | INFO | Files of the default folder that change a start. `location.json` present: the data folder was moved by Settings → Storage. |
| `reset.json is present: the data folder is erased at this start` | WARNING | An erase was asked for and runs at this start. |
| `move.json is present: the data folder is moved at this start` | WARNING | A move was asked for and finishes at this start. |
| `database: opened, schema version 5` | INFO | Opened without migrating. |
| `database: new, schema version 5` | INFO | A new database. |
| `database: migrated from schema version 3 to 5 (a copy of the old file is kept)` | INFO | Migrated; `auralink.db.bak-3` is the copy from before. |
| `start: the database could not be opened (<class>)` | ERROR | The start failed (written to the log file directly). |
| `start: the data folder could not be used (<class>)` | ERROR | The start failed: the chosen data folder is not there, for example. |
| `settings: saved settings read` / `settings: no saved settings, defaults used` | INFO | Where the settings came from. |
| `settings: could not be read (<class>), defaults used` | WARNING | The saved settings did not decode; the next change saves the defaults. |
| `menu bar icon created` / `menu bar icon not created (<class>)` / `menu bar icon not shown` | INFO / WARNING | The menu bar icon (the last is with the setting off). |

`<class>` is an error's name, never its text: codes (`-25308`), a migration number, known phrases (`not found`, `disk I/O error`, `permission denied`…), or the Go type when none is known.

## 3. Keys (the Keychain)

Account names only: `asr.aliyun`, `llm.deepseek`. Keys and masks never appear.

| Line | Level | Meaning |
|---|---|---|
| `key asr.aliyun in the Keychain: found` | INFO | A saved key was read. |
| `key llm.deepseek in the Keychain: missing` | INFO | No saved key. |
| `key llm.deepseek in the Keychain: error (-25308, user interaction not allowed)` | WARNING | The Keychain refused the read. `-25308` is the Keychain's code for "user interaction is not allowed": the Keychain could not ask for access (usually it is locked, or the process cannot show a prompt). |
| `key asr.doubao in the ActNow family's key: found` | INFO | The service uses a key shared with the ActNow family. |
| `key asr.aliyun (Aliyun): saved in the Keychain` | INFO | A key was saved from the Model services dialog. |
| `key asr.aliyun (Aliyun): saving in the Keychain failed (<class>)` | WARNING | Saving failed. |
| `key llm.deepseek (DeepSeek): removed in the Keychain` / `removing … failed (<class>)` | INFO / WARNING | Remove. |

The read lines come once per launch for each configured service (when the Model services page is read at start), and again only when a service's state changes. A line with `error` is the one to look at first.

## 4. Sessions and shortcuts

| Line | Level | Meaning |
|---|---|---|
| `start transcribe in dev.example.app` | INFO | A session started (existing line). |
| `start transcribe not started: the helper is stopped` | INFO | A shortcut or a Try did not start: reasons are `the helper is <state>`, `no speech service is set up`, `no language model is set up`, `the session manager is not ready`. |
| `start transcribe not started: Auralink is in front, on a page with no Try area for it` | INFO | The shortcut was pressed in Auralink, on a page without a Try area for it. |
| `start transcribe refused: a session is recording (id 01K…, started 42s ago)` | WARNING | A start was asked while a session runs; the running session is shown. |
| `shortcut transcribe/0 ignored: a session is composing` | INFO | A press that does nothing in this phase (the session's state is shown: `recording`, `recognizing`, `composing`, `delivering`). |
| `shortcut transcribe/0: stop the recording` / `cancel the session` / `paste the last result` | INFO | The press stopped, cancelled or pasted. |

A press that starts a session has its `start` line. Key releases are not logged.

## 5. The window and quitting

| Line | Level | Meaning |
|---|---|---|
| `window closed: hidden in the menu bar, Auralink keeps running` | INFO | The window was closed and kept in the menu bar (Settings → General on, icon shown). |
| `window closing: Auralink quits (keep running when the window closes is off)` | INFO | Closing quits, because the setting is off. |
| `window closing: Auralink quits (no menu bar icon)` | INFO | Closing quits, because there is no icon. |
| `window closing: Auralink quits (quit was chosen)` | INFO | Quit from the menu, or ⌘Q. |
| `quit requested` | INFO | Quit was asked for (before the app closes anything). |
| `quit: window closed` | INFO | The window's close quits the app. |

If the app quit without a `quit requested` line, the process was ended by the system or from outside.

## 6. For the team: reading a report

1. Start with `start:` (version, variant, folder, database) and any WARNING/ERROR in the first screen of lines.
2. `reset.json` or `move.json present` means data is about to be erased or moved: ask whether the user did that.
3. `key … error (-25308 …)` means the Keychain refused the read. Usually a locked Keychain or a process that cannot show the access prompt. Ask the user to unlock the login Keychain and open the Model services page again.
4. `start … refused` means the user pressed a shortcut while a session ran; that is the state, not a bug, unless the running session is stuck (its `started … ago` is long).
5. `window closing: Auralink quits (…)` tells why the window did not stay in the menu bar.

## 7. Not in the logs, by design

Text, audio, recognised or translated results, prompts, keys and masks, and full paths with the user's name.
