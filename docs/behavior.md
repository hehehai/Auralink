# 行为对照：Voxt 怎么做 / Auralink 怎么做 / 为什么

Auralink 的功能和行为参考 Voxt（`hehehai/voxt` @ `baa6f31`，1.15.0），代码按自己的结构重写，不逐行移植（方案 §1）。每个模块落地时，在这里补一节对照表，作为评审依据：

- **Voxt 怎么做**：写清楚出处（源码文件或文档），只描述行为，不抄代码。
- **Auralink 怎么做**：写实际实现的行为，并指向实现它的包或文件。
- **为什么**：保留的写保留理由；改掉的写改的理由。

状态：✅ 已实现　◐ 部分实现（骨架或接口已经有了）　○ 计划中（写明负责的里程碑）

## 1. 架构原则（方案 §1）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 1.1 | 会话状态分散在 `AppDelegate` 的 43 个 extension 里（`Voxt/App/*Flow.swift`、`Recording/`），取消和收尾由各个 Flow 各自处理 | 一个 `Session` 状态机（idle → recording → recognizing → composing → delivering → done/cancelled/failed）是会话唯一的所有者；所有异步回调都带 sessionID，迟到的一律丢弃 | 身份、取消、“只收尾一次”集中在一个地方，才能写表驱动测试；迟到回调不会污染下一次会话 | ✅ M3：`internal/session`（§5）；helper 的 job 通知也带 `job`，supervisor 丢弃已结束 job 的迟到通知 |
| 1.2 | 语音翻译“先增强、再翻译”，两次 LLM 请求（`docs/Prompt.zh-CN.md`“语音翻译”一节） | 一次会话最多一次 LLM 请求：Prompt Composer 按“功能 × 能力 × 场景 × 词典 × 语言”拼出一个请求 | 少一次往返，延迟和费用减半；两段式在第二段会丢掉第一段的上下文 | ✅ M3 转录、M4 翻译和转写（`internal/compose`，§5、§6）；翻译只有“看起来没翻译”时才多一次严格重试 |
| 1.3 | 命中应用增强分组时，用分组 prompt **整个替换**全局 prompt（`Core/EnhancementPromptResolver.swift`） | 分组 prompt 作为“场景层”**叠加**在基础清理之上 | 用户写的分组规则不完整时，基础清理（去口头语、标点）不会跟着丢 | ○ M5 |
| 1.4 | 规则匹配、prompt 拼接和 AppKit 调用写在一起 | 匹配、组合、热键判定、文本后处理是纯 Go 函数，Linux 上做表驱动测试；录音、事件 tap、AX、AppleScript、粘贴放在窄接口后面，测试换成假实现 | 不需要 Mac 也能测完整链路 | ◐ M1：helper 进程和协议、假 helper（`helpertest`）、`permission.Fake`、in-process 的假 supervisor 都已就位 |
| 1.5 | 设置保存在 UserDefaults 的几百个键里（`Voxt/` 中有 259 处 `UserDefaults`），还保留了旧版单热键之类的迁移代码 | 设置是一个带版本号的类型化文档（`internal/settings`，v1），存在 SQLite 的 `documents` 表里；不兼容、也不读取 Voxt 的键 | 从 v1 开始，没有历史包袱；整个文档可以快照、比较、重置 | ✅ M1 |
| 1.6 | 读输入框用了 CDP（`App/TextInputCDP.swift`），还有截图、窗口上下文等链路 | 不做 CDP、截图和窗口上下文 | 已废弃或小众，权限和维护成本高（方案 §2“不做”） | ✅（不实现） |

## 2. 骨架（M1）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 2.1 | SwiftUI 设置窗口和菜单栏 | AudioSmith 的外壳（Go + MyGo）：侧边栏、⌘K、设置对话框、状态栏、菜单栏图标 | actnow 家族统一 UI（RULES） | ✅ |
| 2.2 | 录音、热键、粘贴都在 app 主进程里 | 录音、事件 tap、AppleScript 放在 Swift helper 子进程；Go 进程负责 UI、业务和网络 | MyGo 不能录音；事件 tap 的回调必须同步决定是否吞键；helper 崩溃不会带走 UI（方案 §4.1） | ✅ helper（`helper/`，Swift）和协议 1（`docs/helper-protocol.md`）已完成：录音、热键、textio、前台 App、浏览器 URL、权限 |
| 2.3 | 历史存在自己的数据库（`Core/VoxtDatabase.swift`） | SQLite（MyGo 插件）迁移 001：`history`（带 trigram FTS，覆盖原文、选中文本、口述指令、结果）、`dictionary_terms`、`documents`；迁移前自动备份，拒绝打开更新版本 app 的数据库 | 中文子串搜索可用；数据可以整体搬家（设置 → 存储） | ✅ |
| 2.4 | API Key 存在钥匙串 | 钥匙串 service "Auralink"，账号 `asr.<id>`、`llm.<id>`（`internal/secrets`） | 同样的做法 | ✅ |
| 2.5 | 权限说明分散在 onboarding 和各设置页 | 权限是设置对话框里的一节（设置 → 权限，在“通用”之后，`/settings/permissions`）：只有麦克风和辅助功能两行，各自说明用途并提供授权入口；首页“检查权限”、⌘K、深链 `auralink://permissions` 和旧路径 `/permissions` 都打开这一节（`permissions.go`、`shell.go` 的 `movedPaths`） | 只剩两项权限，看一次就走，不值得占侧边栏的位置；浏览器网址只用辅助功能（AX）读，不需要浏览器自动化授权（用户 10-15 决定） | ✅ 状态和授权都走 helper（`permissions.*`）；helper 没起来时由 app 自己读。helper 里的 `automation.*` 和 AppleScript 由 M5 删除 |
| 2.6 | 主窗口 820×560 | 主窗口默认 880×580，最小 720×480（`shell.go`）；窗口记住的尺寸优先，旧版本留下的大窗口不会被缩小；最小尺寸下侧边栏列表滚动，设置对话框左栏至少 240 宽，快捷键表可滚动 | 和 Voxt 相近的小窗口，比 AudioSmith 的 1100×720 更适合常驻的输入工具 | ✅ |

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

## 4. 辅助进程（helper，Swift）

做法来自 M0 spike 的实测（项目 `research/spikes.md`），协议见 `docs/helper-protocol.md`。

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 4.1 | 录音、热键、读选中文本、粘贴、读 URL 都在 app 主进程里，各自在不同的 Flow 中调用 | 全部放在一个 Swift 子进程里；Go 只下发策略（设备顺序、是否允许 ⌘C、延迟、粘贴后的按键），helper 不存任何设置 | 事件 tap、AX、剪贴板、注入事件的标记在同一个进程里，不需要来回通信；权限按负责进程算在 Auralink 名下（spike S2 实测） | ✅ |
| 4.2 | 热键：`Hotkey/HotkeyManager.swift` 按修饰键状态判定，单按和双击共用时单按走 `scheduleTapFallback` 延迟触发 | 识别器（`AuralinkHelperKit/HotkeyRecognizer.swift`）按“修饰键会话”判定：从第一个修饰键按下到全部松开算一个和弦；第二次按下在窗口内开始就算双击（默认 500 ms，取系统双击间隔，最多 800 ms）；单按最长 1500 ms；某个修饰键的事件来了状态却没变，说明系统丢了事件，丢弃当前和弦 | 真实按键补测：双击两次松开相隔 450 ms 但第二次按下只隔 303 ms；⇧+fn 按住 983 ms；fn+⌃ 之后 fn 的松开事件会丢 | ✅ 识别器；真实右⌘ 待测 |
| 4.3 | 读选中文本：AX → 浏览器 AppleScript → 模拟 ⌘C；“空选区会复制整行”的 App 名单里不模拟（`SelectedTextSystemSelectionSupport.shouldAttemptSimulatedCopy`）；焦点元素在但没有选区范围时也不模拟 | AX 选中文本 → 选区范围取字符串 → WebKit/Chromium text marker；只有光标没有选区时直接返回空；其余情况由 Go 决定 `allowCopy`（M3 维护整行复制名单）；不用 AppleScript 读浏览器选区 | 策略留在 Go 便于测试；text marker 已覆盖浏览器里的选区（spike S4：Safari、Chrome、Cursor 都能读到），少一次自动化授权 | ✅ helper；名单 ✅ M4（`session.AllowCopy`，§6.2） |
| 4.4 | Electron App 不做特殊处理 | App 切到前台时，helper 给 Electron App 设置 `AXManualAccessibility`，热键按下时 AX 树已经建好 | Chromium 只有被要求时才建 AX 树，而且要过一会儿才生效（spike S4：Cursor 第一次读为空） | ✅ |
| 4.5 | 粘贴：写剪贴板、⌘V，按 changeCount 判断后恢复（`PasteboardTextWriter`） | 同样按 changeCount 只恢复自己写的内容；另外保存全部 item 和类型，写入时带 `org.nspasteboard.TransientType`，剪贴板管理器不记录；`targetPid` 不在前台就不粘贴；粘贴后可以按任意键（`press`） | 剪贴板里的图片、文件不会因为恢复而丢掉；不会粘贴到别的窗口 | ✅ |
| 4.6 | 读 URL：直接跑 AppleScript，失败（-1743）后 5 分钟内不再尝试，再退回 AX（`BrowserContextResolver`）；每次都重新编译脚本 | 先不弹窗地探测自动化状态，只有 `granted` 才跑 AppleScript，否则直接用 AX；编译好的脚本缓存起来；浏览器切到前台时刷新状态 | 未授权时跑 AppleScript 会在用户不知情时弹窗；编译每次多 50–500 ms（spike S5） | ✅ |
| 4.7 | 浏览器授权：权限设置页请求；浏览器没开时直接失败（`PermissionsSettingsView.requestBrowserAutomationPermission`，appNotRunning） | 不请求浏览器自动化授权：设置 → 权限里没有浏览器一栏 | 网址只用 AX 读，4.6 的 AppleScript 路径由 M5 一并删除；用户不必为每个浏览器授权（用户 10-15 决定） | ◐ UI 已删除；helper 的 `automation.request` 由 M5 删除 |
| 4.8 | 浮窗是普通窗口，插入前重新激活目标 App | 录音浮窗用 MyGo 补丁 0003 的 `NonActivating`：点击不激活 Auralink，目标 App 保持焦点；需要打字的窗口才走重新激活（`macx.Activate`） | spike S3：非激活 panel 7/7 生效；重新激活会让焦点闪一下 | ✅ M3：录音浮窗和答案卡片（`overlay.go`）；Auralink 自己在前台时才 `macx.Activate` |

## 5. 会话与转录（M3）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 5.1 | 会话身份和收尾在 `RecordingSessionLifecycle` 里，录音、识别、增强、交付、历史分散在各个 Flow 扩展（`App/Recording/*`、`SessionTextIO.swift`）里，靠 `activeRecordingSessionID` 比对丢弃过期回调 | `session.Manager` 一次只跑一个会话，一个 goroutine 按顺序走完 录音 → 识别 → 整理 → 交付 → 历史；每个回调先过 `Manager.update`：不是当前会话、或者已经收尾，就丢弃；`end` 只生效一次。快照带递增的 `Rev`，UI 只收更新的 | 一个所有者、一个顺序，Linux 上能用假实现把每条路径（取消、超时、迟到回调）都测到（`internal/session` 的表驱动测试） | ✅ |
| 5.2 | 按下热键后先做一串准备（静音、提示音、预热 LLM），再开始录音 | 按下即录音：`capture.start` 立刻发出，服务和词典在另一个 goroutine 里并行解析（读钥匙串、建客户端）；流式服务连上之前的音频先存在内存里，连上后补发 | 用户已经开口了；钥匙串和建连都要时间（spike S1：开始录音本身就要 200–400 ms） | ✅ |
| 5.3 | 录音时把音频写临时文件，历史音频另存 | 录音只在内存里（PCM），结束时按“隐私 → 保存录音”写成 `audio/<id>.wav`；流式服务边录边送，文件服务停止后上传 | 少一次落盘；没有开“保存录音”就什么都不留 | ✅ |
| 5.4 | 很短的录音、什么都没识别到时各处各有判断 | 不到 0.3 s 的录音直接取消，不送服务、不留记录；识别为空或 LLM 说“没有内容”时结束为“没有识别到任何内容”的提示，不插入 | 误触热键不应该花钱，也不应该留下空记录 | ✅ |
| 5.5 | LLM 增强失败时插入原文 | 同样插入原文，浮窗用警告色提示“已插入识别原文”，历史里记下错误；LLM 超时默认 8 s（`session.DefaultLLMTimeout`），识别超时（停止后）30 s | 转录的首要目标是把字放进去；超时要短，否则用户以为卡住了 | ✅ |
| 5.6 | 停止录音的请求和开始录音的请求可能交错 | `capture.stop` 3 s 内没结束录音（和 `capture.start` 交错了），就 `job.cancel` 并把已录的音频当作完整录音继续识别 | 不会出现“停不下来的录音”，也不丢已经说的话 | ✅ |
| 5.7 | 插入：有辅助功能权限就粘贴，否则写剪贴板并提示（`TextOutputDelivery.typeText`）；不检查焦点 | `internal/deliver`：先 `textio.focus`，前台还是按下热键时的 App、且焦点元素能接收文字，才 `textio.paste`（带 `targetPid`）；否则不粘贴，结果留在答案卡片里（插入、复制、关闭）。只有 Auralink 自己在前台时才先 `macx.Activate` 目标 App | 防止把文字粘到别的窗口或没有输入框的地方；答案卡片不抢焦点，用户点回输入框再点“插入”即可 | ✅ |
| 5.8 | — | “能否接收文字”只在确定不行时才算不行：没有焦点元素，或者焦点是按钮、列表、静态文字这类控件；AX 读不出来、网页区域（AXWebArea、AXGroup，contenteditable 常常这样）都照常粘贴 | 宁可多粘一次，也不要让富文本编辑器里的转录都落进卡片 | ✅ 待真机验证各 App |
| 5.9 | 粘贴最近结果：内存里的最近结果，没有时读历史 | 同样（`Manager.Last`，启动时从历史读最近一条成功的结果）；目标不可写时只提示，不弹卡片 | 和 Voxt 一样 | ✅ |
| 5.10 | 热键规则和 common stop 写在 `HotkeyManager` 和各 Flow 里 | `internal/hotkey.Route`：空闲时 tap/双击开始，长按的 down 开始、它自己的 up 停止；录音时任何功能的热键都是“停止”（helper 在 `active` 时 tap 立即触发）；识别、整理中只有 Esc（取消）有效；答案卡片打开时 Esc 关闭卡片 | 纯函数、表驱动测试；Voxt 文档里“普通键 binding 不能 common stop”的问题不存在：所有 binding 都走同一条规则 | ✅ |
| 5.11 | 一个功能可以有多条快捷键（binding 列表）。冲突检查：同一功能同键报重复；跨功能同键同触发方式报重复；粘贴最近结果和其他功能的 tap 相同报重复；系统快捷键提示 | 规则相同（`hotkey.Check`），外加“单独一个普通键会被全局吞掉”的提示；左右侧修饰键（RightCommand）参与比较时，any 和任一侧都算重叠 | 与 Voxt 一致，补上侧别；转录页在每一条下面写出问题，需要辅助功能的条目带标记；冲突时按设置顺序（转录、翻译、转写、粘贴最近结果）先出现的生效（`hotkey.Build`） | ✅ |
| 5.12 | 没有辅助功能时不能用 fn 热键 | 事件 tap 没装上时，把能用 Carbon 表达的热键（⌃⌥⇧⌘ 加一个键，不分左右、没有 fn）注册成 `mygo.GlobalShortcut`；tap 装上后注销。长按和双击在 Carbon 下退化为单按 | 没有权限也至少能用 ⌃⌘V 和普通组合键；转录页提示哪些热键需要辅助功能 | ✅ 待真机验证 |
| 5.13 | M3 只绑定转录和粘贴最近结果 | `liveFeatures`（sessionlink.go）；翻译、转写的热键在 M4 打开 | 没有实现的功能不应该吞掉 fn+⇧ | ✅ M4 三个功能都已绑定 |
| 5.14 | Prompt 变量 `{{RAW_TRANSCRIPTION}}` 把识别文本拼进 system | 输入只在 user 消息里（`<input>…</input>`）；变量 `{{USER_MAIN_LANGUAGE}}`、`{{USER_OTHER_LANGUAGES}}`、`{{TARGET_LANGUAGE}}`、`{{APP_NAME}}` 在内置模板和用户规则里都能用，用户规则里的值不会再被展开 | 避免 prompt 注入，system 对同样的设置保持不变 | ✅ |
| 5.15 | 增强 prompt 一份长文本（中文约 2000 字、英文约 5800 字），基础清理、结构整理、数字规范、示例都写在一起，翻译 prompt 又复制一遍 | 分层（`internal/compose`）：Role、Input policy、基础清理、结构整理（关闭时换成“保持原样”的 Form 层）、Task、用户规则、Scene（M5）、Glossary、Output contract；中英两套模板按主语言选，重新写过、去掉了示例（所有层合起来中文约 1100 字、英文约 2700 字） | 每层只说一件事，能单独开关、单独测（64 个 golden）；示例占篇幅又容易被模型照抄 | ✅ |
| 5.16 | 词典术语作为 glossary 追加，命中时才加 | 启用的术语都参与：在识别文本里出现（拼写或别名，忽略大小写和空格）的排在前面，最多 60 条、2000 字；同时作为 ASR 热词 | 识别错的词本来就不一定“出现”在文本里；数量有上限 | ✅ |
| 5.17 | 模型输出原样使用 | `compose.Clean`：去掉模型回显的 `<input>` 标签，去掉整段包起来的引号（输入本身被引号包起来时不去） | 常见的模型小毛病 | ✅ |
| 5.18 | 录音浮窗：SwiftUI 波形和多种显示模式 | `overlay.go`：非激活浮窗，固定深色；recording（模式图标、16 根 bar、计时、最后 3 行实时文字、停止、×）、working（识别中 / 整理中 / 插入中）、插入后打勾 0.7 s、notice（4 s 自动隐藏，带“打开模型服务 / 打开权限 / 重启辅助进程”）、答案卡片；位置、边距、透明度、圆角读 `settings.Overlay` | 方案 §3.2；“试一试”的会话只显示在转录页，不弹浮窗 | ✅ |
| 5.19 | 菜单栏“开始转录” | 菜单栏的“开始转录”在录音时变成“停止转录”；目标是最近一次 `app.activated` 的 App（helper 就绪时先问一次 `app.frontmost`） | 点菜单栏图标不会激活 Auralink，前台 App 还是用户要输入的那个 | ✅ |
| 5.20 | 历史是一个列表，详情窗口另开 | 历史页按功能分 tab（转录、翻译、转写），FTS 搜索；详情侧栏里有结果、原文、交付方式（插入 / 卡片 / 复制）、服务和模型、各阶段耗时、命中规则（M5）；失败的会话也记一条（有录音时可以在访达中找到）；删除要确认 | 排查“为什么慢、用了哪个模型、为什么没插入”时一眼能看到 | ✅（重新处理 ○ 以后） |
| 5.21 | 开发期手动检查 helper 用 `AURALINK_TRY`（M1 时的临时面板） | 删除，正式链路替代 | 不留调试入口 | ✅ |

## 6. 翻译与转写（M4）

| # | Voxt 怎么做 | Auralink 怎么做 | 为什么 | 状态 |
|---|---|---|---|---|
| 6.1 | 选中文本翻译：先读选区，读到了就不录音（`TranslationFlow.beginSelectedTextTranslationIfPossible`）；转写在识别完口述指令之后才读选区 | 翻译：按下热键时先读选区（`reading` 状态，最多 1.5 s），读到就直接翻译，读不到或读取出错就开始录音。转写：选区和录音并行读取，识别完成后再用（`internal/session`） | 翻译读到选区时不打开麦克风（不会闪一下麦克风指示）；转写要的是“按下热键那一刻”的选区，用户说话时可能已经点到别处 | ✅ |
| 6.2 | 模拟 ⌘C 的名单：VS Code、Sublime、Zed、JetBrains 等“空选区复制整行”的编辑器不模拟（`SelectedTextSystemSelectionSupport.copiesCurrentLineWhenSelectionEmpty`），另有“AX 全黑时”的过滤探测 | 同一份名单（含前缀：ToDesktop/Cursor、JetBrains、Exafunction、Trae、Qoder，和包含 vscode 的 bundle id），由 Go 决定 `allowCopy`（`session.AllowCopy`）；不做“AX 全黑”的二次探测 | 策略在 Go，表驱动测试；helper 已经在 AX 读到“只有光标”时直接返回空（§4.3），二次探测要靠“看起来像整行”的猜测，误判代价是翻译一行用户没选的代码 | ✅ |
| 6.3 | 语音翻译两次 LLM 请求：先增强、再翻译 | 一次请求：基础清理层（翻译版本的规则：先清理再翻译，去掉“不要翻译”“保持语言混合”两条）+ Form + 翻译任务层；选中文本不加清理层，Task 层要求保留换行、列表和 Markdown | §1.2；同一份清理规则只写一处（`cleanup.txt` 用 `{{#if FOR_TRANSLATE}}` 切换两条规则），不复制 | ✅ |
| 6.4 | “看起来没翻译”：结果和原文忽略大小写后相同（`looksUntranslated`），只对选中文本翻译重试 | `compose.Untranslated`：忽略大小写、空格和标点后相同；或者目标语言有独特文字（中日韩、西里尔、阿拉伯……）而结果大多不是这种文字、原文也不是（按词计：一个拉丁词和一个汉字同权）。原文本来就是目标语言的文字时不重试。语音和选区都适用，只重试一次，加一层 Strict；重试也失败时保留第一次的结果 | Voxt 的判断抓不到“清理了但没翻译”的中文结果；按词计数避免“今天部署 Kubernetes”被误判 | ✅ |
| 6.5 | 翻译目标语言默认英语，22 种可选；结果窗口里可以临时切换目标语言重新翻译 | 默认：英语；主要语言是英语时为简体中文（`session.TranslateTarget`）。22 种（简体、繁体中文分开），`{{TARGET_LANGUAGE}}` 用对应语言的名称。答案卡片右上角可以换目标语言，用同一段原文（语音的识别文本或选区）重新翻译，不再录音 | 中文用户“翻译成主要语言”毫无意义；换语言不用重说一遍 | ✅ |
| 6.6 | 选中文本翻译：设置“显示结果窗口”时进窗口，否则直接替换；目标可写性用严格判断（`hasWritableFocusedTextInput`） | “在卡片中显示结果”对语音和选区都生效；替换选区时要求焦点元素在 AX 里明确可编辑（`deliver.Request.NeedEditable`），网页上选中的文字一律进卡片；转录、语音翻译、按指令生成仍用宽松判断（§5.8） | 网页正文选区不可编辑，宽松判断会“粘贴”到无处，结果丢失 | ✅ |
| 6.7 | 转写：先对口述指令做一次增强，再改写，两次请求；失败时把增强后的指令当结果输出 | 一次请求：“理解指令”层（只用于读懂口述指令，明确不作用于选中的文字）+ 改写任务层或生成任务层；失败不输出任何替代文本，答案卡片显示错误和“重试” | §1.2；把指令当结果插进文档没有意义 | ✅ |
| 6.8 | 结构化答案：没有选区时要求 `{title, content}` JSON，解析器容忍代码块和半截 JSON（`RewriteAnswerPayloadParser`，约 560 行）；内容为空时强制非空重试 | 没有选区时用 JSON（`llm.FormatJSON` + schema，服务商支持时走原生 JSON 模式）；`compose.ParseAnswer` 找第一个合法对象；不是 JSON 时按纯文本使用，没有标题；内容为空时失败进卡片（不再自动重试）。流式时 `compose.PartialAnswer` 从半截 JSON 里取出标题和正文 | 解析器只需要两件事：完整对象和流式前缀；空答案多半是指令本身有问题，重试一般还是空 | ✅ |
| 6.9 | 转写结果一律进答案卡片（`shouldPresentRewriteAnswerOverlay`） | 有选区：替换选区（不可编辑时进卡片）；没有选区：结果不超过“较长回答”的字数（默认 400）就直接插入，超过就进卡片；流式过程中一超过就切到卡片 | 方案 §2.1：改写的意义就是原地替换；短回答直接插入更快 | ✅ |
| 6.10 | 答案卡片：生成完成后一次性显示，继续对话时伪流式 | 翻译和转写都流式（`llm.Request.OnDelta`，40 ms 节流写进快照的 `Draft`）：会进卡片的（设置、目标不可写、超长）直接在卡片里出字，其余在浮窗胶囊下方出字；卡片显示标题、目标语言、复制、插入；失败时显示原因、原文和“重试” | 用户能看到进度，长回答不用干等 | ✅ |
| 6.11 | 继续对话：卡片打开时再按转写热键，带上之前的轮次（Responses API 或 role 消息） | 同样：卡片显示的是转写回答时，转写热键录一段追问，之前的轮次作为 user/assistant 消息发送（assistant 用 JSON 形式），加一层 Follow-up；回答替换卡片内容，轮次累积。只用 chat completions | 不依赖服务商的会话状态 | ✅（P1） |
| 6.12 | 翻译、转写失败时只在浮窗显示 2.8 s 的状态文字 | 失败的会话结束为 failed，答案卡片显示原因和原文，“重试”用同样的输入重新开始一个会话（不录音、不再读选区）；历史里记一条失败记录（含原文） | 方案 §4.2；不用重新说一遍 | ✅ |
| 6.13 | 翻译、转写需要的 LLM 没配置时，录完音才报错 | 开始前检查（不做 I/O）：转录要语音服务；翻译要语言模型（语音服务只在没有选区时才需要）；转写两者都要。缺什么就只弹提示，不开麦克风 | 不让用户白说一遍 | ✅ |
| 6.14 | 历史记录原文、选中文本、口述指令、目标语言 | 同样（`SourceText`、`PromptText`、`TargetLang`），转写的口述指令只记在“指令”里；列表第二行显示“→ 目标语言”，详情里翻译有“目标语言”一行 | — | ✅ |
| 6.15 | 翻译和转写的设置页各不相同 | 三个功能页共用一套结构（`transcribe_page.go` 的 `featurePage`）：快捷键、服务、各自的设置（翻译：目标语言、基础清理、在卡片中显示结果；转写：清理口述指令、较长回答放进卡片）、你的规则（含完整提示词预览）、试一试 | 一套代码，三页一致 | ✅ |

## 7. 待补（按里程碑）

每节在对应里程碑落地时补上。下面先列出方案里已经确定的差异。

| 模块 | Voxt 怎么做 | Auralink 计划怎么做 | 为什么 | 里程碑 |
|---|---|---|---|---|
| 应用增强：匹配顺序 | URL 分组 → 浏览器没有命中任何 URL 时直接回到全局（`EnhancementPromptResolver.resolve`） | URL 分组 → **浏览器 App 本身所在的分组** → App 分组 → 全局 | 用户把 Chrome 放进“工作”分组时，没写 URL 规则的网站也应该按“工作”处理 | M5 |
| 应用增强：上下文快照 | 停止录音时读取前台 App 和 URL | **按下热键时**记下前台 App（helper 的 `hotkey.event` 自带 `front`），同时在后台开始读 URL | AppleScript 要 50–300 ms；用户中途切走窗口也不影响匹配 | ◐ M3：前台 App 作为插入目标；URL M5 |
| 录音设备 | 1.2 s 看门狗，指定设备失败时回到默认设备 | 保留，放在 helper 的 `capture.start` 里，`capture.started` 写明是否回退（`fellBack`），见 §4 | 蓝牙耳机常常没有声音 | ✅ helper 和会话（设备顺序来自 `Audio.Devices`） |
| 剪贴板恢复 | 用 changeCount 判断剪贴板是不是自己写的 | 保留（helper 的 `textio.paste`，§4.5） | 避免覆盖用户在这期间复制的内容 | ✅ |
