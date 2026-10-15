# 行为对照：Voxt 怎么做 / Auralink 怎么做 / 为什么

Auralink 的功能和行为参考 Voxt（`hehehai/voxt` @ `baa6f31`，1.15.0），代码按自己的结构重写，不逐行移植（方案 §1）。每个模块落地时，在这里补一节对照表，作为评审依据：

- **Voxt 怎么做**：写清楚出处（源码文件或文档），只描述行为，不抄代码。
- **Auralink 怎么做**：写实际实现的行为，并指向实现它的包或文件。
- **为什么**：保留的写保留理由；改掉的写改的理由。

状态：✅ 已实现　◐ 部分实现（骨架或接口已经有了）　○ 计划中（写明负责的里程碑）

## 1. 架构原则（方案 §1）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 1.1 | 会话状态分散在 `AppDelegate` 的 43 个 extension 里（`Voxt/App/*Flow.swift`、`Recording/`），取消和收尾由各个 Flow 各自处理 | 一个 `Session` 状态机（idle → recording → recognizing → composing → delivering → done/cancelled/failed）是会话唯一的所有者；所有异步回调都带 sessionID，迟到的一律丢弃 | 身份、取消、“只收尾一次”集中在一个地方，才能写表驱动测试；迟到回调不会污染下一次会话 | ◐ M1：helper 协议里每个 job 的通知都带 `job`，supervisor 丢弃已结束 job 的迟到通知（`internal/helper`）。状态机 ○ M3 |
| 1.2 | 语音翻译“先增强、再翻译”，两次 LLM 请求（`docs/Prompt.zh-CN.md`“语音翻译”一节） | 一次会话最多一次 LLM 请求：Prompt Composer 按“功能 × 能力 × 场景 × 词典 × 语言”拼出一个请求 | 少一次往返，延迟和费用减半；两段式在第二段会丢掉第一段的上下文 | ○ M3（`internal/compose`） |
| 1.3 | 命中应用增强分组时，用分组 prompt **整个替换**全局 prompt（`Core/EnhancementPromptResolver.swift`） | 分组 prompt 作为“场景层”**叠加**在基础清理之上 | 用户写的分组规则不完整时，基础清理（去口头语、标点）不会跟着丢 | ○ M5 |
| 1.4 | 规则匹配、prompt 拼接和 AppKit 调用写在一起 | 匹配、组合、热键判定、文本后处理是纯 Go 函数，Linux 上做表驱动测试；录音、事件 tap、AX、AppleScript、粘贴放在窄接口后面，测试换成假实现 | 不需要 Mac 也能测完整链路 | ◐ M1：helper 进程和协议、假 helper（`helpertest`）、`permission.Fake`、in-process 的假 supervisor 都已就位 |
| 1.5 | 设置保存在 UserDefaults 的几百个键里（`Voxt/` 中有 259 处 `UserDefaults`），还保留了旧版单热键之类的迁移代码 | 设置是一个带版本号的类型化文档（`internal/settings`，v1），存在 SQLite 的 `documents` 表里；不兼容、也不读取 Voxt 的键 | 从 v1 开始，没有历史包袱；整个文档可以快照、比较、重置 | ✅ M1 |
| 1.6 | 读输入框用了 CDP（`App/TextInputCDP.swift`），还有截图、窗口上下文等链路 | 不做 CDP、截图和窗口上下文 | 已废弃或小众，权限和维护成本高（方案 §2“不做”） | ✅（不实现） |

## 2. 骨架（M1）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 2.1 | SwiftUI 设置窗口和菜单栏 | AudioSmith 的外壳（Go + MyGo）：侧边栏、⌘K、设置对话框、状态栏、菜单栏图标 | actnow 家族统一 UI（RULES） | ✅ |
| 2.2 | 录音、热键、粘贴都在 app 主进程里 | 录音、事件 tap、AppleScript 放在 Swift helper 子进程；Go 进程负责 UI、业务和网络 | MyGo 不能录音；事件 tap 的回调必须同步决定是否吞键；helper 崩溃不会带走 UI（方案 §4.1） | ◐ 协议和 supervisor 已完成，Swift 端等 M0 spike 结果 |
| 2.3 | 历史存在自己的数据库（`Core/VoxtDatabase.swift`） | SQLite（MyGo 插件）迁移 001：`history`（带 trigram FTS，覆盖原文、选中文本、口述指令、结果）、`dictionary_terms`、`documents`；迁移前自动备份，拒绝打开更新版本 app 的数据库 | 中文子串搜索可用；数据可以整体搬家（设置 → 存储） | ✅ |
| 2.4 | API Key 存在钥匙串 | 钥匙串 service "Auralink"，账号 `asr.<id>`、`llm.<id>`（`internal/secrets`） | 同样的做法 | ✅ |
| 2.5 | 权限说明分散在 onboarding 和各设置页 | 侧边栏有独立的“权限”页：麦克风、辅助功能、每个浏览器的自动化权限，各自说明用途并提供授权入口 | 应用增强需要按浏览器授权，集中在一处更好排查 | ◐ 页面已完成；浏览器状态依赖 helper 的 `automation.probe` |

## 3. 待补（按里程碑）

每节在对应里程碑落地时补上。下面先列出方案里已经确定的差异。

| 模块 | Voxt 怎么做 | Auralink 计划怎么做 | 为什么 | 里程碑 |
|---|---|---|---|---|
| 应用增强：匹配顺序 | URL 分组 → 浏览器没有命中任何 URL 时直接回到全局（`EnhancementPromptResolver.resolve`） | URL 分组 → **浏览器 App 本身所在的分组** → App 分组 → 全局 | 用户把 Chrome 放进“工作”分组时，没写 URL 规则的网站也应该按“工作”处理 | M5 |
| 应用增强：上下文快照 | 停止录音时读取前台 App 和 URL | **按下热键时**记下前台 App（helper 的 `hotkey.event` 自带 `app`），同时在后台开始读 URL | AppleScript 要 50–300 ms；用户中途切走窗口也不影响匹配 | M3/M5 |
| Prompt 变量 | 用户 prompt 里用 `{{RAW_TRANSCRIPTION}}` 把识别文本拼进 system | 输入文本永远放在 user 消息里；变量只有 `{{USER_MAIN_LANGUAGE}}`、`{{USER_OTHER_LANGUAGES}}`、`{{TARGET_LANGUAGE}}`、`{{APP_NAME}}` | 避免 prompt 注入，也便于缓存 system | M3 |
| 翻译兜底 | 结果“看起来没翻译”时用更严格的规则重试 | 保留 | 实测有效 | M4 |
| DeepSeek | 思考占满输出预算时正文为空（Voxt 踩过的坑） | 默认显式关闭思考 | 同左 | M2 |
| 录音设备 | 1.2 s 看门狗，指定设备失败时回到默认设备 | 保留，放在 helper 的 `capture.start` 里，结果中写明是否回退（`fallback`） | 蓝牙耳机常常没有声音 | M0/M3 |
| 热键 | 默认 preset：fn 转录、fn+Shift 翻译、fn+Control 改写、Control+Command+V 粘贴最近结果（`docs/HotkeyRules.zh-CN.md`） | 同样的默认值（方案 D2，`settings.Defaults`）；判定在 helper，规则表和冲突检查在 Go | 用户已经习惯 | M3 |
| 剪贴板恢复 | 用 changeCount 判断剪贴板是不是自己写的 | 保留 | 避免覆盖用户在这期间复制的内容 | M3 |
