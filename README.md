# Auralink

**Speak anywhere on your Mac, get text where your cursor is.** Auralink is a voice input app for macOS that uses remote models only (you bring the keys):

- **Transcribe**: hold or tap a shortcut, speak, and the cleaned-up text is typed into the app in front.
- **Translate**: what you say, or the text you select, into the language you choose.
- **Rewrite**: say how to change the selection ("shorter", "more formal"), or what to write when nothing is selected.
- **App enhancement**: scene rules per app or website (a chat app, your mail), added on top of the clean-up.
- **Dictionary**: names and terms you use, sent as hints to speech recognition and as a glossary to the language model.

[中文说明](README.zh-CN.md)

## Status

**Pre-alpha.** The skeleton (M1), the remote model layer (M2), the Swift helper, Transcribe (M3), Translate and Rewrite (M4) are written and tested on Linux against a fake helper; they are not yet checked on a Mac by hand. App enhancement and the dictionary come next. There is no release.

Auralink is a rewrite of what [Voxt](https://github.com/hehehai/voxt) does, with remote models only, on the engineering and UI of [AudioSmith](https://github.com/hehehai/AudioSmith) (Go + [MyGo](https://github.com/egoist/mygo), no cgo). See [docs/architecture.md](docs/architecture.md) and [docs/behavior.md](docs/behavior.md).

## Build

Needs Go 1.27.1 (`GOTOOLCHAIN=auto` fetches it) and, to package, a Mac with macOS 15 or later on Apple silicon and Xcode (the Swift helper, [helper/](helper/README.md)).

```sh
scripts/build-helper.sh              # the Swift helper, into resources/darwin-arm64/helper/
go tool mygo dev                     # run with hot reload
go tool mygo build                   # build/darwin-arm64/Auralink.app and a .dmg
```

From Linux, `scripts/mac.sh <command>` mirrors the project to a Mac (`MAC_HOST`, default `mac-air`; `MAC_DIR`, default `code/Auralink`) and runs the command there: `scripts/mac.sh 'scripts/build-helper.sh && go tool mygo build'`.

### Signing during development

macOS keeps the Microphone and Accessibility grants for a signature. An ad hoc signed build (the default) has a new one each time the app is rebuilt, so after a rebuild macOS forgets them: the microphone turns **denied without asking again**, and Accessibility still shows on in System Settings but does not work. The helper's grants are the app's (macOS checks the helper against Auralink.app, its responsible process), so the same happens to it. Ways around it:

- **Change only the helper:** keep one built app and run it with a helper outside the bundle. The helper inherits the app's grants wherever it lives, so rebuilding it costs nothing:
  ```sh
  scripts/build-helper.sh --out ~/auralink-dev
  open --env AURALINK_HELPER=$HOME/auralink-dev/auralink-helper build/darwin-arm64/Auralink.app
  ```
- **Start over:** `tccutil reset Microphone dev.hehehai.auralink` (likewise `Accessibility`), then open the app and grant again. If a stale Accessibility entry stays on but does nothing, remove it with − in System Settings and grant again.
- **Sign with a fixed identity:** an Apple Development certificate keeps the grants across rebuilds: `go tool mygo build -sign "Apple Development: …"` (or `macos.signingIdentity` in a local mygo.json; `AURALINK_SIGN_IDENTITY` for `scripts/build-helper.sh`). Over ssh, `codesign` fails with `errSecInternalComponent` until the key may be used without the GUI: once, in Terminal on the Mac, `security set-key-partition-list -S apple-tool:,apple: -s ~/Library/Keychains/login.keychain-db`.

## Tests

```sh
CGO_ENABLED=0 go test ./...          # everything; runs on Linux
go tool mygo vet .                   # go vet and MyGo's UI-lifetime checks
AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .   # every page as PNG, light/dark, en/zh-Hans
cd helper && swift test              # the Swift helper (on a Mac): its wire types against the same fixtures, the hotkey recognizer
```

The store's tests use MyGo's SQLite plugin, which downloads its library once (checked against its SHA-256) or uses `MYGO_SQLITE_LIBRARY`. More in [docs/architecture.md](docs/architecture.md) §6.

## Licence

Apache License 2.0 ([LICENSE](LICENSE)). Parts come from AudioSmith; icons are Solar by 480 Design under CC BY 4.0 ([NOTICE](NOTICE)).
