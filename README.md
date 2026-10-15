# Auralink

**Speak anywhere on your Mac, get text where your cursor is.** Auralink is a voice input app for macOS that uses remote models only (you bring the keys):

- **Transcribe**: hold or tap a shortcut, speak, and the cleaned-up text is typed into the app in front.
- **Translate**: what you say, or the text you select, into your language.
- **Rewrite**: say how to change the selection ("shorter", "more formal"), or what to write when nothing is selected.
- **App enhancement**: scene rules per app or website (a chat app, your mail), added on top of the clean-up.
- **Dictionary**: names and terms you use, sent as hints to speech recognition and as a glossary to the language model.

[中文说明](README.zh-CN.md)

## Status

**Pre-alpha.** This is the M1 skeleton: the window, the settings, the store, the permissions page and the helper's protocol and supervisor work; the features do not yet. There is no release.

Auralink is a rewrite of what [Voxt](https://github.com/hehehai/voxt) does, with remote models only, on the engineering and UI of [AudioSmith](https://github.com/hehehai/AudioSmith) (Go + [MyGo](https://github.com/egoist/mygo), no cgo). See [docs/architecture.md](docs/architecture.md) and [docs/behavior.md](docs/behavior.md).

## Build

Needs Go 1.27.1 (`GOTOOLCHAIN=auto` fetches it) and, to package, a Mac with macOS 15 or later on Apple silicon.

```sh
go tool mygo dev                     # run with hot reload
scripts/stage-fake-helper.sh         # until the Swift helper exists (helper/README.md)
go tool mygo build                   # build/darwin-arm64/Auralink.app and a .dmg
```

From Linux, `scripts/mac.sh <command>` mirrors the project to a Mac (`MAC_HOST`, default `mac-air`; `MAC_DIR`, default `code/Auralink`) and runs the command there.

## Tests

```sh
CGO_ENABLED=0 go test ./...          # everything; runs on Linux
go tool mygo vet .                   # go vet and MyGo's UI-lifetime checks
AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .   # every page as PNG, light/dark, en/zh-Hans
```

The store's tests use MyGo's SQLite plugin, which downloads its library once (checked against its SHA-256) or uses `MYGO_SQLITE_LIBRARY`. More in [docs/architecture.md](docs/architecture.md) §6.

## Licence

Apache License 2.0 ([LICENSE](LICENSE)). Parts come from AudioSmith; icons are Solar by 480 Design under CC BY 4.0 ([NOTICE](NOTICE)).
