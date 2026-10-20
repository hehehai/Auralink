# Changelog

All notable changes to Auralink. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [Semantic Versioning](https://semver.org/). Each version's section is what the update window shows: English first, then the same in Chinese under matching headings (docs/release.md).

## [Unreleased]

The first public version, a beta: voice input for macOS on remote models, from [actnow.dev](https://actnow.dev/auralink). It updates to the next beta and to the release that follows (Settings → About → Beta updates). 0.1.0-beta.1 was built but not released; its changes are listed here.

### Added

- **Transcribe**: a shortcut (fn by default; tap, double tap or long press, several per feature) records, a speech service recognises, a language model cleans up (fillers, false starts, punctuation, numbers; an optional structure layer and your own rules), and the text is pasted where the cursor is. Without a language model the text goes in as recognised; when it fails or is slow, too.
- **Translate**: the selection (read through Accessibility, or ⌘C where that is safe) or what you say, into the language you choose; a strict retry when the answer looks untranslated; an answer card to read, copy, insert or translate again into another language.
- **Rewrite**: say how to change the selection, or what to write with nothing selected; every answer is written one line under the pill, then waits in the answer card (as in Voxt), where Insert puts it in place (over the selection) and Continue records a follow-up, answered in plain text in the card's conversation.
- **App enhancement**: groups of rules by app and by website (address rules read through Accessibility, no browser automation), a scene prompt layered on top of the clean-up, the key to press after inserting, the features each group applies to, a live match preview, and the group's label in the overlay and the history.
- **Dictionary**: terms with aliases, sent as hints to the speech services and as a glossary to the language model; CSV and text import and export.
- **Model services**: Doubao and Alibaba Cloud Model Studio streaming recognition and any OpenAI-compatible transcription API; DeepSeek, Qwen, Volcengine Ark, Kimi, GLM, MiniMax, StepFun, MiMo, SiliconFlow, OpenAI, Anthropic, Gemini, OpenRouter and other OpenAI-compatible language models; keys in the Keychain, connection tests, a service per feature.
- **Home**: a slogan with your Transcribe shortcut in a key cap; what is still missing (a service, a permission, the shortcut), each line the way to fix it, and nothing once all is ready; a try of the whole chain on the page until the first result; today's results and characters, the last five results.
- **Setup guide** on the first start (and from Settings → General): privacy, permissions, services, then a real try of Transcribe, Translate and Rewrite, and App enhancement and the dictionary.
- **History**: by feature, searchable, with the texts, where the result went, the services, the rule that matched and each stage's time; recordings kept on request. Right-click an entry, in History or among Home's recent results, to copy its result or its recognised text, show its details, show its recording in Finder, retry it or delete it (with its recording, after a confirmation); a selected History row takes ⌘C and ⌫ too.
- The recording overlay (non-activating, so the app you type into keeps its focus), after Voxt's: the feature's icon and a waveform on a translucent, blurred pill with no buttons and Voxt's proportions (the shortcut stops, Escape cancels; the pointer goes through it), one line of live text under it, the pill as wide as the text up to 300 and the text then scrolled to its newest words, a loader while it works, errors in place of the waveform, nothing after an insert; and the answer card, with start and stop sounds. Every session starts as the pill, and it grows about its edge of the screen without moving; every flow shows the same pill until its result (what the model works on, then its draft, on one line), and the card comes only with the result.
- The answer card, after Voxt's: 440 wide (Voxt's is 560) and as high as its text (up to 220 of it, then it scrolls), its actions in the header (the target language as a capsule opening our own menu in a window of its own, Continue, Insert, Copy, Close), the header as far from the top as from the rule, Voxt's own icons in the pill and the card, why the result is in the card in Insert's tip instead of a line of orange, Copy marking itself for two seconds, Insert hiding the card while it pastes, another language translated in place (the old text with a spinner until the new one comes), and Rewrite follow-ups recorded in the card as a conversation (a small waveform, bubbles); Escape closes it, or cancels a follow-up only.
- Silence left out before recognition (a light energy detector, no model): a file service gets the recording without the silence around the speech and with long pauses shortened, a streaming service nothing before the speech starts, and a recording without speech is not sent at all (Settings → Audio → Skip silence).
- Sound optimization before recognition (pure DSP, no model): a high-pass filter at 80 Hz takes out hum and rumble, and one gain measured on the speech alone raises a quiet voice (up to +12 dB, never lifting the room's noise over -50 dBFS) or lowers a very loud one a little, with a limiter at -1 dBFS; a stream's gain follows slowly and holds through pauses. The recording kept in the history is the original; the history says what was done and the gain (Settings → Audio → Optimize sound, on by default).
- A small window (800×520 by default, 680×440 at the least) with no status bar: notifications stack as cards at the foot of the sidebar (a dot on Settings when it shows only icons), and a missing service or a stopped helper becomes a card with the action that fixes it.
- The menu bar icon: start or stop transcribing, paste the last result, the last three results to copy (one short line each, the whole in the tooltip); it changes while recording. Closing the window keeps Auralink there, and it can open at login.
- Settings: General (languages, the menu bar, login, the setup guide), Permissions, Audio (the microphones in your order of preference, skipping silence, optimizing the sound, sounds), Overlay style (place, opacity, corners, the recording time, with a live preview over a desktop picture), Shortcuts (every feature's keys and their conflicts), Appearance (the sidebar shrinks to icons in a narrow window), Network (proxy), Privacy, Storage, About (versions, updates, the self-check, reporting a problem); English and Simplified Chinese.
- Signed with a Developer ID and notarised. Updates from actnow.dev: a daily check, the new version's notes in a window of its own, a download of only what changed, installed when you say so; Check for Updates… in the app menu, the menu bar icon's menu and Settings → About. An update installed while Auralink runs asks for a restart instead of starting the new helper.
- Beta updates (Settings → About): betas too, or releases only; on by default in a beta. Turning it off never goes back to an older version: a beta stays until a release newer than it.
- **Mute system audio while recording** (Settings → Audio, on by default): the current output device is muted from just after the start sound until the recording stops, so that videos, music or calls are not recorded; a device you muted stays muted, one you unmute during the recording stays on, and a device that cannot be muted leaves the recording as it is (the overlay says so). No permission is needed. If Auralink or its helper stops while muting, the sound comes back at once, or at the next start.
- **Sounds to choose** (Settings → Audio → Play sounds): the start and the stop sound are each chosen from the ActNow family's sound library (from React Bits Sound Lab: taps, toggles, media, system sounds…; Tally and Wrap by default), or macOS's system sounds, or none. A picker searches them, filters by group and tag and plays each with ▶; a Try button plays the choice, and a slider sets their volume. A Voxt pair chosen in an earlier build is kept as those system sounds. The output is muted once the start sound has been heard, not after its file's silent end.
- **ActNow family**: Auralink finds the other ActNow apps on this Mac (AudioSmith, Dicta, Pik…) and tells them what it does and which global shortcuts it uses, so that they can warn about clashes. The app's language and the proxy are shared with them: a change in one applies to all, unless you choose "Use a different setting for this app" (Settings → General, Network). API keys can be shared too: a key saved in Auralink is offered to the other ActNow apps (unless you uncheck "Let other ActNow apps on this Mac use this key"), and a service in Auralink offers the key another ActNow app saved, read only once you click "Use this key". A shortcut another ActNow app also uses is pointed out where you set it (and announced once by the app that set it last), and two ActNow apps no longer paste over each other's clipboard.
- **History settings** (the button beside History's search field): keep the recordings (the same switch as Settings → Privacy), choose the folder they are kept in (the recordings already kept move there, each one copied and checked before the original is deleted; if the folder is on a drive that is not connected, new recordings go to the default folder until it is back), see how much room they take, export them (all, or those of the tab shown) into a folder you choose, named by their date and first words, or delete them all and keep the texts. **Automatic clean-up**, on as in Voxt: entries older than 3 months are deleted with their recordings; you can choose 1 week to 1 year, keep at most 50 to 1000 entries of each feature, or turn it off. A new setting that would delete entries now asks first, with their number.
- **Retry** a history entry from its recording: a failed entry (or one with no text) shows Retry on its row instead of Failed, in its details and in its context menu; other entries with a recording have Transcribe again. The recording is recognised again (streaming services get it through a stream, faster than it was said), then cleaned up, translated or rewritten with your settings as they are now; the entry is updated in place (text, status, time), and nothing is pasted. The overlay's notice of a failed recognition opens the entry and retries it.
- When recognition fails, times out or finds no text, the entry keeps the recording for 7 days even when Keep the recordings is off, so that you can retry it; a successful retry deletes it.
- **Logs** (the Logs page and panel): click a line to copy it; Shift-click, ⌘-click or drag to select lines as in Finder, ⌘A for every line shown; right-click to copy a line, the selection or every line shown. The header shows how many lines are selected, with Copy and Deselect.

### Changed

- The note of which output device Auralink muted is kept in Application Support instead of ~/Library/Caches, so that a cache cleaner run after a crash cannot leave your speakers muted. Logs leave out keys, tokens and proxy passwords.
- Auralink is now proprietary software of actnow, sold at [actnow.dev](https://actnow.dev/auralink) (it was open source up to 0.1.0-beta.1). Settings → About shows the copyright, links to the website and the changelog, and opens the licence and the third-party notices, which now ship inside the app. Report a bug writes an email to hello@actnow.dev instead of a GitHub issue.
- Downloads, updates and release notes come from [actnow.dev](https://actnow.dev/auralink/changelog) instead of GitHub Releases: sign in at actnow.dev/download/auralink to download. Updates come from actnow.dev/updates/auralink, and Beta updates read its beta channel, which always offers the newer of the latest beta and the latest release.
- The menu bar icon is now a speech bubble with a waveform, in thin lines like the system's own icons; while recording, the bubble turns solid.
- A result goes where you are typing when it is ready, not only into the app you started in (as in Voxt): move to another app and click into a text field while Auralink records or works, and it is pasted there; web editors such as Notion or Slack in the browser count as text fields. The app's group's key after the paste is pressed only in the app you started in, whose group shaped the result, and a selection is replaced only in the app it was selected in. Where no text field is focused (Finder, the desktop, a page you are reading) or the focus is in a password field, the result waits in the answer card, which says why; click where it should go, then Insert. Clicking Auralink's own window still sends it back to the app you started in. The history shows the app it was inserted into when that is another one. Insert in the answer card and Paste the last result follow the same rules.

### Known limitations

- Apple silicon and macOS 15 or later only (no Intel build yet).

### 中文

第一个公开版本，是 beta 版：只用远程模型的 macOS 语音输入，在 [actnow.dev](https://actnow.dev/auralink) 下载。它会更新到下一个 beta 和之后的正式版（设置 → 诊断与关于 → Beta 更新）。0.1.0-beta.1 构建了但没有发布，它的内容都列在这里。

### 新增

- **转录**：按住快捷键说话，语音识别服务转成文字，语言模型整理（去口头禅、改标点和数字，可加结构和你自己的规则），粘贴到光标处。
- **翻译**：选中的文字或你说的话，翻译成你选的语言；答案卡片里可以阅读、复制、插入或换一种语言。
- **转写**：说出怎么改选中的文字，或在没有选中时说出要写什么；回答进卡片，可以插入，也可以继续追问。
- **应用增强**：按 App 和网站分组的场景规则；**词典**：术语和别名，发给语音识别和语言模型。
- 首页、设置引导、历史、浮窗和答案卡片、菜单栏图标、静音裁剪和声音优化，以及完整的设置；界面有英文和简体中文。
- **历史记录右键菜单**：在历史页或首页“最近结果”的一条记录上右键，可以复制结果或识别原文、查看详情、在访达中显示录音、重试，或删除（确认后连同录音一起删除）；历史页里选中一行后也可以按 ⌘C 复制、⌫ 删除。
- 使用 Developer ID 签名并经过公证。通过 actnow.dev 更新：每天检查一次，在单独的窗口里显示更新说明，只下载有变化的部分，你同意后才安装。
- Beta 更新（设置 → 诊断与关于）：同时接收 beta 版，或只接收正式版；正在用 beta 时默认打开。关闭后不会降级：已装的 beta 会保留到比它新的正式版发布。
- **录音时静音系统声音**（设置 → 音频，默认开启）：从开始提示音之后到停止录音为止，当前输出设备静音，免得视频、音乐、通话的声音被录进去；你自己静音的设备保持静音，录音中你取消静音后不会再被静音，不支持软件静音的设备照常录音（浮窗会提示）。不需要任何权限。Auralink 或辅助进程在静音期间意外退出时，声音会立即恢复，最迟在下次启动时恢复。
- **可选的提示音**（设置 → 音频 → 播放提示音）：开始和结束提示音分别从 ActNow 家族提示音库里选（来自 React Bits Sound Lab：轻触、开关、媒体、系统等分组；默认是“开录”和“收工”），也可以选 macOS 系统声音或无声。选择器可以搜索、按分组和标签筛选、用 ▶ 逐个试听；试听按钮播放当前选择，滑块调音量。以前版本里选的 Voxt 预设会保留为对应的系统声音。录音时静音系统声音在开始提示音能听到的部分播完后就生效，不等文件末尾的静音。
- **ActNow 家族**：Auralink 会找到这台 Mac 上的其他 ActNow app（AudioSmith、Dicta、Pik 等），并告诉它们自己做什么、用了哪些全局快捷键，方便互相提示冲突。界面语言和代理与它们共用：在一个 app 里改，所有 app 一起生效；也可以选“仅为此 app 单独设置”（设置 → 通用、网络）。API 密钥也可以共用：在 Auralink 里保存的密钥会提供给其他 ActNow app（除非取消勾选“允许这台 Mac 上的其他 ActNow app 使用此密钥”）；配置服务时，如果其他 ActNow app 已经保存了这个厂商的密钥，可以一键使用，点“使用此密钥”之前不会读取它。和其他 ActNow app 用了同一个快捷键时，设置快捷键的地方会提示（后设置的 app 还会发一次通知）；两个 ActNow app 同时粘贴时不会再互相覆盖剪贴板。
- **历史设置**（历史页搜索框右边的按钮）：保存录音（和 设置 → 隐私 是同一个开关）；选择录音存放的文件夹（已有录音会一起移过去，每段先复制并校验再删原文件；文件夹在没接上的外接盘上时，新录音先存到默认文件夹，等它回来）；查看录音占用的空间；把录音（全部，或当前 tab 的）导出到你选的文件夹，以日期和开头几个词命名；或者全部删除、保留文字。**自动清理**，和 Voxt 一样默认开启：超过 3 个月的记录连同录音一起删除；可以选 1 周到 1 年，或每种功能最多保留 50 到 1000 条，也可以关掉。新设置会立即删掉已有记录时，先告诉你会删几条并请你确认。
- **重试**：用历史记录保存的录音再做一遍。失败或没有文字的记录，行上的“失败”换成“重试”，详情和右键菜单里也有；其他有录音的记录是“重新识别”。录音重新识别（流式服务按比说话更快的速度送入），再按现在的设置整理、翻译或转写；结果原地更新这条记录（文字、状态、用时），不会粘贴。浮窗里识别失败的提示，点一下打开这条记录并重试。
- 识别失败、超时或没有识别出文字时，即使“保存录音”关着，这条记录也保留录音 7 天，方便重试；重试成功后删除。
- **日志**（日志页和日志面板）：单击一行就复制这一行；Shift-单击、⌘-单击或拖动，像在访达里一样选择多行，⌘A 选中全部显示的行；右键可以复制这一行、选中的行或全部显示的行。右上角显示已选中几行，以及“复制”和“取消选择”。

### 变更

- Auralink 静音了哪个输出设备的记录改放在 Application Support，不再放在 ~/Library/Caches，崩溃后即使清理了缓存，扬声器也不会一直静音。日志不再记下密钥、令牌和代理密码。
- Auralink 改为 actnow 的专有软件，在 [actnow.dev](https://actnow.dev/auralink) 销售（0.1.0-beta.1 及以前是开源的）。设置 → 诊断与关于显示版权，链接到官网和更新日志，并打开 app 内附带的许可和第三方声明。“报告问题”改为给 hello@actnow.dev 写邮件，不再打开 GitHub issue。
- 下载、更新和更新说明改由 [actnow.dev](https://actnow.dev/auralink/changelog) 提供，不再用 GitHub Releases：登录 actnow.dev/download/auralink 下载。更新来自 actnow.dev/updates/auralink，打开 Beta 更新时读它的 beta 通道，那里始终是最新 beta 和最新正式版中较新的那个。
- 菜单栏图标改为对话气泡加波形，线条和系统图标一样细；录音时气泡变成实心。
- 结果出来时插入到你**当时**正在输入的地方，不再只认开始时的 App（和 Voxt 一样）：录音或处理中切到另一个 App 并点进输入框，结果就粘贴到那里；浏览器里的 Notion、Slack 等网页编辑器也算输入框。应用增强分组的“插入后按键”只在开始时的 App 里按（结果是按那个分组生成的），替换选区也只在选区所在的 App 里做。没有选中输入框（访达、桌面、正在阅读的网页）或焦点在密码框里时，结果留在答案卡片里，并说明原因；点一下要输入的位置，再点“插入”。点了 Auralink 自己的窗口时，仍然回到开始时的 App 插入。插入到别的 App 时，历史详情里会写“已插入到 …”。答案卡片的“插入”和“粘贴最近结果”也是同样的规则。

### 已知限制

- 只支持 Apple 芯片和 macOS 15 及以上。

## [0.1.0-beta.1] - 2026-10-16 (not released)

Built but not released: it updated from GitHub Releases, which the repository, now private, no longer serves. Its changes are in the next version.

构建了但没有发布：它从 GitHub Releases 更新，仓库改为私有以后读不到了。它的内容并入了下一个版本。

