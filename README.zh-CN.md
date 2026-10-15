# Auralink

**在 Mac 上任何地方说话，文字直接出现在光标处。** Auralink 是一个只用远程模型的 macOS 语音输入 app（使用你自己的 API Key）：

- **转录**：按住或按下快捷键说话，整理后的文字直接输入到当前 App。
- **翻译**：把你说的话或选中的文字翻译成你的语言。
- **转写**：说出对选中文字的修改要求（“精简一点”“改正式”），或者不选文字，直接说出要写的内容。
- **应用增强**：按 App 或网站设置场景规则（聊天软件、邮件等），叠加在基础清理之上。
- **词典**：你常用的名称和术语，作为提示发给语音识别、作为术语表发给语言模型。

[English](README.md)

## 状态

**Pre-alpha。** 目前是 M1 骨架：窗口、设置、存储、权限页，以及辅助进程（helper）的协议和监管已经可用；三个功能还没有实现，也没有发布版本。

Auralink 按 [Voxt](https://github.com/hehehai/voxt) 的行为重写（只用远程模型），工程和界面沿用 [AudioSmith](https://github.com/hehehai/AudioSmith)（Go + [MyGo](https://github.com/egoist/mygo)，不用 cgo）。详见 [docs/architecture.md](docs/architecture.md) 和 [docs/behavior.md](docs/behavior.md)。

## 构建

需要 Go 1.27.1（`GOTOOLCHAIN=auto` 会自动获取）；打包需要 macOS 15 及以上的 Apple 芯片 Mac。

```sh
go tool mygo dev                     # 热重载运行
scripts/stage-fake-helper.sh         # Swift helper 完成之前先用假的（见 helper/README.md）
go tool mygo build                   # 生成 build/darwin-arm64/Auralink.app 和 .dmg
```

在 Linux 上开发时，`scripts/mac.sh <命令>` 会把项目同步到 Mac（`MAC_HOST` 默认 `mac-air`，`MAC_DIR` 默认 `code/Auralink`）并在那边执行。

## 测试

```sh
CGO_ENABLED=0 go test ./...          # 全部测试，Linux 上可跑
go tool mygo vet .                   # go vet 加 MyGo 的 UI 生命周期检查
AURALINK_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .   # 每个页面的截图：浅色/深色 × 中/英
```

## 许可证

Apache License 2.0（[LICENSE](LICENSE)）。部分代码来自 AudioSmith；图标为 480 Design 的 Solar，以 CC BY 4.0 授权（[NOTICE](NOTICE)）。
