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

## 3. 远程模型（M2）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 3.1 | ASR 的配置、连接测试、错误展示分散在 `Core/RemoteProviders/*` 和 `Transcription/RemoteASR/*` 的十几个文件里，错误多是带文案的 `NSError` | `internal/asr` 只有两种接口（`FileTranscriber` 上传、`Streamer`/`Stream` 流式，事件 Partial/Final/Error），每家一个子包；错误是带 `Kind` 的 `*asr.Error`（config/auth/quota/network/protocol/server/audio/cancelled），文案由 UI 按 Kind 给出 | 会话（M3）只面对一个接口；UI 能按错误类别告诉用户去改哪里 | ✅ |
| 3.2 | Endpoint 允许 http/ws，只有带凭据时才要求 https（`RemoteEndpointSecurityPolicy`） | Endpoint 一律只允许 https/wss，http/ws 只限 loopback，URL 里不能带用户名密码（`asr.CheckEndpoint`、`llm.ProviderConfig.Resolve`）；日志里的 endpoint 去掉 query | Key 总会随请求发出，没有“不带凭据”的情况 | ✅ |
| 3.3 | OpenAI 兼容转写：endpoint 必须填完整的 `/audio/transcriptions` 路径 | 也接受以 `/v1` 结尾的 API 根地址，自动补上路径；ASR 提示词和词典术语合在 `prompt` 字段（最多 800 字节）；25 MB 上限在发送前检查；5xx、429、网络错误重试一次（`internal/asr/openai`） | 用户常把 LLM 的 base URL 直接粘过来 | ✅ |
| 3.4 | 豆包：2.0 用 `bigmodel_async` + `enable_nonstream`，1.0 用 `bigmodel`；开始录音时就发请求，失败稍后才暴露 | 默认值相同，请求字段按 endpoint 路径决定（只有 nostream 发语言，只有 async 发 enable_nonstream）；`Open` 等服务端第一帧确认后才返回 | 自定义中转地址也能拿到合法参数；Key、未开通资源在用户开口前就能报出来 | ✅ |
| 3.5 | 豆包：200 ms 一包，最后发一个负序号的空包 | 同样 200 ms；有剩余音频时随最后一包发出 | 少一帧，和官方 demo 一致 | ✅ |
| 3.6 | 豆包：取 `result.text`，没有时在整个 JSON 里找最长的文本 | 取 `result.text`，没有时拼 utterances；文本变化时才发 partial | 按文档的结构解析，不做启发式遍历；不重复发事件 | ✅ |
| 3.7 | 豆包热词：所有端点最多 5000 个，外加 correct_words | 清洗、去重，按文档限额截断（双向流式约 100 token，nostream 5000 词）；不发未写进文档的 correct_words；ASR 提示词作为对话上下文发送 | 守住文档限额 | ✅ |
| 3.8 | 豆包鉴权只有 App ID + Access Token | 有 App ID 时用 `X-Api-App-Key` + `X-Api-Access-Key`；只填 Key 时用新版控制台的 `X-Api-Key` | 新版控制台只发一个 API Key | ✅ |
| 3.9 | 豆包 error 帧变成一个通用错误 | 错误码和握手状态映射到 Kind（403 “resource not granted” 是未开通 → quota），错误详情带 `X-Tt-Logid` | 用户能知道该去改什么，也方便提工单 | ✅ |
| 3.10 | 阿里百炼：句子之间一律用空格连接 | 只在使用空格的文字之间加空格，中日韩文字和全角标点旁不加 | Voxt 会在中文句子之间多出空格 | ✅ |
| 3.11 | 阿里 qwen3 realtime 预览只显示 `text` | 预览显示 `text + stash`；VAD 模式下只发 `session.finish`，不发 `commit` | 只显示 text 会慢半拍；官方禁止在 VAD 模式下 commit | ✅ |
| 3.12 | 8 kHz 电话模型也发 16 kHz 音频 | 不在列表里，`New` 直接拒绝（KindConfig） | 它们只接受 8 kHz | ✅ |
| 3.13 | 阿里 endpoint 路径会按模型改写成对应协议 | 同样改写，另外支持只填主机、https→wss、粘贴 DashScope HTTP 地址；中转地址保留原路径 | 用户常粘控制台或 LLM 的地址；国际站只需要换主机 | ✅ |
| 3.14 | 阿里 `task-failed` / `error` 变成通用 NSError | 按 DashScope 错误码分类；`NO_VALID_AUDIO`（什么都没说）当作空的 final | 测试连接发的是静音，不能算失败 | ✅ |
| 3.15 | LLM 默认关闭思考（`LLMGenerationSettings.defaultSettings`），DeepSeek 等按 provider 写在代码分支里 | 关闭思考写在预设数据里（`presets.json` 的 `extraBody`：DeepSeek、方舟、Kimi、GLM、MiMo 用 `thinking: disabled`，千问、硅基流动用 `enable_thinking: false`，Gemini 用 `reasoning_effort: none`）；服务端拒绝这些字段时去掉后重发一次 | 语音输入的回复都很短，思考会先占满预算和时间（Voxt 踩过的坑）；改成数据后，加 provider 不用改代码 | ✅ |
| 3.16 | LLM 连接测试：关闭思考、最多 1 token，400/422 算失败 | `llm.Probe`：非流式、不重试、`max_tokens` 8；被截断（哪怕是空文本）算成功，400/422 带服务端说明算失败 | 同样的判断 | ✅ |
| 3.17 | LLM 预设含 Ollama、LM Studio、oMLX 等本地服务 | 不提供本地预设；`custom`（OpenAI 兼容）可以指向本机地址 | 产品范围不做本地模型（RULES） | ✅ |
| 3.18 | MiniMax 用原生 `chatcompletion_v2` 接口 | 用它的 OpenAI 兼容接口 `api.minimaxi.com/v1`，输出里的 `<think>` 由 Client 去掉 | 和其他 provider 共用一个 transport | ◐ 等真实 Key 验证 |
| 3.19 | 每个 provider 的配置页各不相同 | 模型服务页：ASR / LLM 两个 tab，“各功能使用”选择器，服务卡片（状态点、默认模型、Key 掩码、测试），点卡片打开同一种配置对话框（Key、Endpoint、模型下拉 + 自定义、ASR 提示词） | 一种界面覆盖所有 provider | ✅ |

## 4. 待补（按里程碑）

每节在对应里程碑落地时补上。下面先列出方案里已经确定的差异。

| 模块 | Voxt 怎么做 | Auralink 计划怎么做 | 为什么 | 里程碑 |
|---|---|---|---|---|
| 应用增强：匹配顺序 | URL 分组 → 浏览器没有命中任何 URL 时直接回到全局（`EnhancementPromptResolver.resolve`） | URL 分组 → **浏览器 App 本身所在的分组** → App 分组 → 全局 | 用户把 Chrome 放进“工作”分组时，没写 URL 规则的网站也应该按“工作”处理 | M5 |
| 应用增强：上下文快照 | 停止录音时读取前台 App 和 URL | **按下热键时**记下前台 App（helper 的 `hotkey.event` 自带 `app`），同时在后台开始读 URL | AppleScript 要 50–300 ms；用户中途切走窗口也不影响匹配 | M3/M5 |
| Prompt 变量 | 用户 prompt 里用 `{{RAW_TRANSCRIPTION}}` 把识别文本拼进 system | 输入文本永远放在 user 消息里；变量只有 `{{USER_MAIN_LANGUAGE}}`、`{{USER_OTHER_LANGUAGES}}`、`{{TARGET_LANGUAGE}}`、`{{APP_NAME}}` | 避免 prompt 注入，也便于缓存 system | M3 |
| 翻译兜底 | 结果“看起来没翻译”时用更严格的规则重试 | 保留 | 实测有效 | M4 |
| 录音设备 | 1.2 s 看门狗，指定设备失败时回到默认设备 | 保留，放在 helper 的 `capture.start` 里，结果中写明是否回退（`fallback`） | 蓝牙耳机常常没有声音 | M0/M3 |
| 热键 | 默认 preset：fn 转录、fn+Shift 翻译、fn+Control 改写、Control+Command+V 粘贴最近结果（`docs/HotkeyRules.zh-CN.md`） | 同样的默认值（方案 D2，`settings.Defaults`）；判定在 helper，规则表和冲突检查在 Go | 用户已经习惯 | M3 |
| 剪贴板恢复 | 用 changeCount 判断剪贴板是不是自己写的 | 保留 | 避免覆盖用户在这期间复制的内容 | M3 |
