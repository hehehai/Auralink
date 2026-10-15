# Auralink

**在 Mac 上任何地方说话，文字直接出现在光标处。** Auralink 是一个只用远程模型的 macOS 语音输入 app（使用你自己的 API Key）：

- **转录**：按住或按下快捷键说话，整理后的文字直接输入到当前 App。
- **翻译**：把你说的话或选中的文字翻译成你选的语言。
- **转写**：说出对选中文字的修改要求（“精简一点”“改正式”），或者不选文字，直接说出要写的内容。
- **应用增强**：按 App 或网站设置场景规则（聊天软件、邮件等），叠加在基础清理之上。
- **词典**：你常用的名称和术语，作为提示发给语音识别、作为术语表发给语言模型。

[English](README.md)

## 状态

**Pre-alpha。** 骨架（M1）、远程模型层（M2）、Swift 辅助进程、转录（M3）、翻译和转写（M4）都已写完，并在 Linux 上用假的辅助进程测过，还没有在 Mac 上手动验证。接下来是应用增强和词典。还没有发布版本。

Auralink 按 [Voxt](https://github.com/hehehai/voxt) 的行为重写（只用远程模型），工程和界面沿用 [AudioSmith](https://github.com/hehehai/AudioSmith)（Go + [MyGo](https://github.com/egoist/mygo)，不用 cgo）。详见 [docs/architecture.md](docs/architecture.md) 和 [docs/behavior.md](docs/behavior.md)。

## 构建

需要 Go 1.27.1（`GOTOOLCHAIN=auto` 会自动获取）；打包需要 macOS 15 及以上的 Apple 芯片 Mac 和 Xcode（编译 Swift 辅助进程，见 [helper/](helper/README.md)）。

```sh
scripts/build-helper.sh              # 编译辅助进程，放到 resources/darwin-arm64/helper/
go tool mygo dev                     # 热重载运行
go tool mygo build                   # 生成 build/darwin-arm64/Auralink.app 和 .dmg
```

在 Linux 上开发时，`scripts/mac.sh <命令>` 会把项目同步到 Mac（`MAC_HOST` 默认 `mac-air`，`MAC_DIR` 默认 `code/Auralink`）并在那边执行，例如 `scripts/mac.sh 'scripts/build-helper.sh && go tool mygo build'`。

### 开发时的签名

macOS 按签名记住麦克风和辅助功能授权。默认的 ad-hoc 签名每次重编 app 都会变，重编之后授权就失效了：麦克风变成**拒绝，而且不再弹窗**；辅助功能在系统设置里看着还开着，实际不生效。辅助进程的授权算在 app 名下（macOS 按负责进程 Auralink.app 判定），所以它也一样。几个办法：

- **只改辅助进程时**：保留一个编好的 app，让它用 bundle 外的辅助进程。辅助进程放在哪里都继承 app 的授权，重编它不影响授权：
  ```sh
  scripts/build-helper.sh --out ~/auralink-dev
  open --env AURALINK_HELPER=$HOME/auralink-dev/auralink-helper build/darwin-arm64/Auralink.app
  ```
- **清掉重来**：`tccutil reset Microphone dev.hehehai.auralink`（`Accessibility` 同理），再打开 app 重新授权。如果辅助功能条目开着却不生效，在系统设置里用 − 删掉它再重新授权。
- **用固定的签名身份**：用 Apple Development 证书签名，重编后授权仍然有效：`go tool mygo build -sign "Apple Development: …"`（或者在本地的 mygo.json 里设 `macos.signingIdentity`；`scripts/build-helper.sh` 用 `AURALINK_SIGN_IDENTITY`）。通过 ssh 签名时 `codesign` 会报 `errSecInternalComponent`，需要先在 Mac 的终端里执行一次 `security set-key-partition-list -S apple-tool:,apple: -s ~/Library/Keychains/login.keychain-db`。

## 测试

```sh
CGO_ENABLED=0 go test ./...          # 全部测试，Linux 上可跑
go tool mygo vet .                   # go vet 加 MyGo 的 UI 生命周期检查
AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .   # 每个页面的截图：浅色/深色 × 中/英
cd helper && swift test              # Swift 辅助进程（在 Mac 上）：协议类型对同一份 fixtures、热键识别器
```

CI 在 Linux 上跑 Go 这一侧，在 macOS 上跑 Swift 辅助进程的测试。需要权限、界面和真实服务的部分，按 [docs/manual-test.md](docs/manual-test.md) 手动验证（约 35 分钟）。

## 许可证

Apache License 2.0（[LICENSE](LICENSE)）。部分代码来自 AudioSmith；图标为 480 Design 的 Solar，以 CC BY 4.0 授权（[NOTICE](NOTICE)）。
