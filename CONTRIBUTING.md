# Contributing to Auralink

**English** · [简体中文](#参与贡献)

Thank you for helping. Bug reports, ideas, translations and pull requests are all welcome.

## Before you start

- **Bugs**: Settings → Diagnostics and about → Report a bug opens a GitHub issue with the diagnostics filled in (no text, audio or keys). Or open an [issue](https://github.com/hehehai/Auralink/issues/new) by hand: what happened, what you expected, the steps.
- **Changes**: for anything larger than a fix, open an issue first and say what you want to change. Auralink does three things (Transcribe, Translate, Rewrite) with App enhancement and the dictionary, on remote models only; meetings, file transcription, notes and local models are out of its scope.
- Read [AGENTS.md](AGENTS.md) (the rules for changes) and [docs/architecture.md](docs/architecture.md) before changing code.

## Pull requests

1. Fork the repository and branch from `main`.
2. Keep a pull request to one change, with tests. Commit messages and code comments are in English.
3. Every user-visible string goes through `i18n.T` with an English message, and its Simplified Chinese translation goes into `internal/i18n/locales/zh-Hans.json`.
4. A change in behaviour from Voxt goes into [docs/behavior.md](docs/behavior.md); a change users notice gets a line in the `Unreleased` section of [CHANGELOG.md](CHANGELOG.md), in English and Chinese.
5. Run the checks below; CI runs them too and must pass.
6. Say in the pull request what changed and how you checked it. For a change in the UI, add before and after screenshots.

**Third-party material** (a Go module, code, data, icons, sounds) needs a licence compatible with the GNU AGPL-3.0, an entry in [NOTICE](NOTICE) and its licence text in `LICENSES/` (`TestNotice` checks the Go modules). Don't commit build outputs, recordings of your voice, API keys or other secrets.

## Development

Auralink is a Go app on [MyGo](https://github.com/egoist/mygo) (native UI, no cgo) with a small Swift helper. Everything but the helper is plain Go and is tested on Linux; building the app needs a Mac with macOS 15 or later on Apple silicon and Xcode. Go 1.27.1 (`GOTOOLCHAIN=auto` fetches it).

```sh
CGO_ENABLED=0 go test ./...                                  # all tests (Linux and Mac)
go tool mygo vet .                                           # go vet and MyGo's UI checks
git ls-files '*.go' ':!:third_party/**' | xargs gofmt -l    # must print nothing
go test -run TestKeyParity ./internal/i18n                   # strings and zh-Hans
AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .   # every page as PNG
scripts/snapshots.sh check                                   # which snapshots your branch changes
scripts/vendor-mygo.sh --check                               # the vendored MyGo is intact
cd helper && swift test                                      # the Swift helper (on a Mac)
scripts/build-helper.sh && go tool mygo build                # build the app (on a Mac)
```

The store's tests use MyGo's SQLite plugin, which downloads its library once (checked against its SHA-256) or uses `MYGO_SQLITE_LIBRARY`. On Linux the overlay's pixel checks of Chinese text need a CJK font (Ubuntu: `fonts-noto-cjk`). What needs permissions, a screen or real services is checked by hand: [docs/manual-test.md](docs/manual-test.md).

A build from this repository plays macOS's system sounds for the start and the end of a recording: the official builds' sounds come from the ActNow family's private library and are not in the repository ([NOTICE](NOTICE)).

## Licence of contributions and the CLA

Auralink is licensed under the [GNU Affero General Public License, version 3](LICENSE) (AGPL-3.0). Before we can merge your first pull request, you sign the [Contributor License Agreement](CLA.md) (CLA), once:

- You keep the copyright in your work. You license it to the project, which releases it under the AGPL-3.0 and may also release it under other terms, such as a commercial licence or an app-store build. The CLA also promises that versions with your contribution stay available as free software.
- To sign, comment on your pull request with exactly: `I have read the CLA Document and I hereby sign the CLA`. The CLA bot asks you to when you open the pull request, and records your GitHub user name in the project's public signature file. Every author of the pull request's commits signs; link your commits to your GitHub account (commit with an e-mail address registered there).
- If your employer may own what you write, make sure they agree (CLA section 5), or ask for a corporate agreement at riverhohai@gmail.com.

Comment `recheck` if the bot's check doesn't update.

## For maintainers

The CLA workflow (`.github/workflows/cla.yml`) stores signatures in `signatures/version1/cla.json` on the `cla-signatures` branch, which must exist before the first outside pull request:

```sh
git switch --orphan cla-signatures
git commit --allow-empty -m "CLA signatures"
git push origin cla-signatures
git switch main
```

The bot commits to that branch with the workflow's token: don't require pull requests or reviews on it; do block force pushes and deletion (a branch ruleset). A new version of CLA.md that should be signed again goes with a new `path-to-signatures` (`signatures/version2/cla.json`).

---

## 参与贡献

感谢你的帮助。欢迎报告问题、提建议、补翻译和提交 PR。

### 开始之前

- **报告问题**：设置 → 诊断与关于 → 报告问题，会打开一个已填好诊断信息（不含文字、音频和密钥）的 GitHub issue；也可以手动[新建 issue](https://github.com/hehehai/Auralink/issues/new)，写清发生了什么、你期望什么、复现步骤。
- **改动**：比修 bug 大的改动，请先开 issue 说明。Auralink 只做转录、翻译、转写三件事，加上应用增强和词典，只用远程模型；会议、文件转录、笔记和本地模型不在范围内。
- 改代码前请先读 [AGENTS.md](AGENTS.md)（修改规则）和 [docs/architecture.md](docs/architecture.md)。

### 提交 PR

1. Fork 仓库，从 `main` 建分支。
2. 一个 PR 只做一件事，并带上测试。提交信息和代码注释用英文。
3. 界面上的每一句文字都要经过 `i18n.T`（英文原文），并在 `internal/i18n/locales/zh-Hans.json` 里加上简体中文翻译。
4. 和 Voxt 行为不同的地方写进 [docs/behavior.md](docs/behavior.md)；用户能感知的改动，在 [CHANGELOG.md](CHANGELOG.md) 的 `Unreleased` 一节加一条，中英文各一。
5. 跑一遍上面 [Development](#development) 里的检查；CI 也会跑，必须通过。
6. 在 PR 里说明改了什么、怎么验证的；界面改动请附上改前改后的截图。

**第三方素材**（Go 模块、代码、数据、图标、声音）的许可必须与 GNU AGPL-3.0 兼容，在 [NOTICE](NOTICE) 里加一条，并把许可全文放进 `LICENSES/`（Go 模块由 `TestNotice` 检查）。不要提交构建产物、你的录音、API key 或其他秘密。

从本仓库构建的版本，录音开始和结束时播放 macOS 的系统声音：官方版本里的提示音来自 ActNow 家族的私有声音库，不在仓库里（见 [NOTICE](NOTICE)）。

### 许可证与 CLA

Auralink 采用 [GNU AGPL-3.0](LICENSE) 许可。你的第一个 PR 合并前，需要签署一次[贡献者许可协议（CLA）](CLA.md)：

- 你保留自己作品的版权，并授权本项目使用：项目以 AGPL-3.0 发布，也可以用其他条款（例如商业许可、App Store 版本）发布。CLA 同时承诺，包含你贡献的版本始终以自由软件形式提供源代码。
- 签署方法：在你的 PR 里留言，内容与下面这句完全一致：`I have read the CLA Document and I hereby sign the CLA`。CLA 机器人会提示你，并把你的 GitHub 用户名记录在公开的签名文件里。PR 中每个提交的作者都要签署；提交邮箱需要关联到你的 GitHub 账号。
- 如果你的雇主可能拥有你写的代码，请先取得雇主同意（CLA 第 5 条），或发邮件到 riverhohai@gmail.com 申请公司版协议。

CLA 的英文原文是唯一有效文本；本节只是说明。
