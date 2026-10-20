# 发布与更新

Auralink 是开源软件（GNU AGPL-3.0，[LICENSE](../LICENSE)），仓库 [hehehai/Auralink](https://github.com/hehehai/Auralink) 是公开的；下载、更新和更新日志都在官网 [actnow.dev](https://actnow.dev/auralink)，**没有 GitHub Release**。公开仓库的 GitHub Actions 标准 runner（包括 macOS）不计额度。

发布版本在 GitHub Actions 上构建：打一个 `v0.x.y` tag（正式版）或 `v0.x.y-beta.n` tag（beta 版），[release 工作流](../.github/workflows/release.yml)就在 macOS runner 上编译辅助进程和 app，用 Developer ID 签名，用 Apple ID 和 App 专用密码公证，用更新密钥签名更新包，然后用官网的发布脚本 [scripts/actnow-publish.sh](../scripts/actnow-publish.sh)（`https://actnow.dev/actnow-publish.sh` 的副本，见 §2.3）把文件交给官网后台。后台校验（大小、sha256、清单、Ed25519 签名、CHANGELOG）、存进 R2、写更新日志、发布，**清单最后写**。CI 只需要 `ACTNOW_INGEST_TOKEN`，没有 R2 的密钥。官网的说明在 actnow-www 仓库的 `docs/release-pipeline.md`。

```
tag v0.1.0-beta.2 ─▶ release.yml（macos-26，contents: read）
   │  检查版本号、CHANGELOG、Secret（发布时缺 ACTNOW_INGEST_TOKEN 就在构建前失败）
   ├─ 家族提示音：有 ACTNOW_SOUNDS_TOKEN 时从私有库 hehehai/actnow-sounds 拉到 resources/sounds（scripts/sync-sounds.sh），没有就用系统声音
   ├─ 签名钥匙串、公证凭据
   ├─ build-helper.sh → release-config.sh（合并 release/mygo.release.json：公钥、url、prereleases）
   ├─ go tool mygo build（不加 -upload）：签名、公证、staple；更新包、增量（从官网两个通道的清单）、清单
   ├─ release-verify.sh：签名、公证、Gatekeeper、清单
   ├─ actnow-publish.sh --app auralink --dir build/darwin-arm64 --changelog CHANGELOG.md --min-macos 15.0
   │     PR 和试跑加 --dry-run：只做本地检查、打印请求，不发任何请求
   │     发布：POST /api/releases/sessions → 逐个上传（DMG、更新包、增量、清单）→ POST …/complete
   │        └▶ 官网后台：校验 → R2 actnow-releases/auralink[/beta]/ → 更新日志、下载页 → 发布（清单最后）
   └─ 产物存为这次运行的 Artifact（发布的保留 90 天，试跑 7 天）
```

| 文件（R2 `actnow-releases`） | 官网地址 | 用途 |
|---|---|---|
| `auralink/update-darwin-arm64.json` | `https://actnow.dev/updates/auralink/update-darwin-arm64.json` | 正式版通道的清单：版本、更新说明、文件的大小和签名 |
| `auralink/beta/update-darwin-arm64.json` | `…/updates/auralink/beta/update-darwin-arm64.json` | beta 通道的清单：**后台保证始终是 beta 和正式版中较新的那个** |
| `auralink[/beta]/auralink-0.x.y-darwin-arm64.tar.gz` | 同目录 | 完整更新包（里面的 app 已 staple） |
| `auralink[/beta]/auralink-<旧版本>-to-0.x.y-darwin-arm64.delta` | 同目录 | 增量更新，最多 3 个旧版本各一个（§4.2） |
| `auralink[/beta]/Auralink-0.x.y-arm64.dmg` | 不公开：`https://actnow.dev/download/auralink` 登录后下载 | 新用户安装（已公证、已 staple）；脚本把 MyGo 文件名里的空格换成横线 |

官网只公开 `update-*.json`（no-cache）和 `*.tar.gz`、`*.delta` 等（缓存一年、immutable），DMG 只能经登录后的下载页拿到。MyGo 的签名在清单里，没有单独的 `.sig` 文件。

更新用的是 MyGo 官方方案（`mygo.Updater` + updater 插件的 `native.Plugin` 窗口），说明见 MyGo 的 `docs/updates.md`、`docs/plugins/updater.md`；beta 通道是我们的补丁 0006（`third_party/mygo-patches/`）。

只发 Apple 芯片（darwin/arm64）：辅助进程只编 arm64，Intel Mac 暂不支持。

## 公开仓库：提交和公开前的检查

仓库是公开的，提交的东西（包括历史）任何人都能看到：

- **秘密**：证书、`.p12`、`.p8`、私钥、token、API key、`.env` 一律不进仓库（只放在 GitHub Secrets、密码管理器和 `~/.config`）。改动较大或久未检查时，扫一遍全部历史：`gitleaks git --log-opts=--all .`（只应报测试里的假 key：`providers_page_test.go`、`internal/secrets/secrets_test.go`）。
- **不能再分发的素材**：家族提示音（`resources/sounds/`，React Bits Sound Lab）只在构建时拉取；新加第三方素材前先看 [NOTICE](../NOTICE) 和 AGENTS.md 的许可规则。
- 一旦推上去了：立刻作废泄漏的秘密（换证书、换 token、换更新密钥，见 §5），再用 `git filter-repo` 删掉文件并强推；只删不作废没有用，别人可能已经拉过。
- 2026-10-20 转为公开前扫过一次：gitleaks 全部分支、tag 和 GitHub 上的 `refs/pull/*`，只有上面两处假 key；提示音只在没推送过的本地提交里，公开前已经从这些提交里删掉。

## 0. 两个通道

| | 正式版 | beta 版 |
|---|---|---|
| 版本号、tag | `0.2.0` / `v0.2.0` | `0.2.0-beta.1` / `v0.2.0-beta.1`（版本号里有 `-`） |
| 更新源（R2） | `https://actnow.dev/updates/auralink`（`auralink/`） | `https://actnow.dev/updates/auralink/beta`（`auralink/beta/`） |
| 清单里文件的地址 | `…/updates/auralink/<文件>` | `…/updates/auralink/beta/<文件>`（后台会检查） |
| 谁会收到 | 所有人 | 只有打开了 设置 → 诊断与关于 → **Beta 更新** 的人 |

- **Beta 更新开关**：打开时检查更新读 beta 通道的清单（beta 和正式版中较新的那个），比当前版本新就提示；读不到（网络、清单坏了）时退回正式版的清单。关闭时只读正式版的清单。
- **beta 通道取较新的**：由官网后台保证：发 beta 时 beta 通道指向它；发正式版时正式版通道指向它，beta 通道也指向它，除非已有更新的 beta。所以开着开关的人只读一个清单就够了。
- **版本号只能往上走**（后台的规则）：新版本必须高于这个通道发布过的所有版本（beta 还要高于正式版），已发布或下架过的版本号不能再用。
- **默认值**：用户没动过开关时，当前运行的是 beta 就默认打开（beta 用户能收到下一个 beta），是正式版就默认关闭。用户拨过一次就以用户为准。
- **不降级**：只会更新到比当前**更新**的版本。beta 用户关掉开关后，要等到比当前 beta 新的正式版发布才会更新（例如在 `0.2.0-beta.1` 上关掉，`0.1.1` 不会装，`0.2.0` 会装）。设置里的说明文字会这样写。
- **还没有正式版时**（现在：前几个版本都是 beta），正式版通道的清单是 404，关着开关检查更新显示“已是最新”，不报错（`mygo.ErrNotPublished`）。

## 1. 一次性准备

需要 Apple Developer Program 账号。签名用 **Developer ID Application: wei guan (779M533DL5)**。mac-air 的钥匙串里还有另一个团队（RY2TQTYSKQ，Xiaofeng Cao）的两张 Developer ID 证书，导出时不要选错；工作流按 `APPLE_TEAM_ID` 选身份，并按证书的 SHA-1 签名（[scripts/release-identity.sh](../scripts/release-identity.sh)），同名证书也不会混淆。

下面的命令都不含秘密值：要输入的地方由命令提示，或者从文件读。都在 **mac-air** 上执行。

### 1.1 Developer ID Application 证书 → .p12

1. 确认钥匙串里有这个身份（应该看到一行 `Developer ID Application: wei guan (779M533DL5)`）：
   ```sh
   security find-identity -v -p codesigning | grep 779M533DL5
   ```
   没有的话：Xcode → Settings → Accounts → 选 779M533DL5 的团队 → Manage Certificates → 左下角 + → **Developer ID Application**。
2. 导出（`security export` 只能把钥匙串里**所有**身份一起导出，所以用图形界面，只导出这一个）：
   1. 打开“钥匙串访问”（Keychain Access），左边选 **登录**（login），上面选 **我的证书**（My Certificates）。
   2. 右上角搜索框输入 `779M533DL5`，只剩 **Developer ID Application: wei guan (779M533DL5)** 一行。点左边的三角展开，下面应该有一把私钥。不要选 `Xiaofeng Cao (RY2TQTYSKQ)` 那两行。
   3. 右键这一行（证书那行，不是私钥那行）→ **导出“Developer ID Application: wei guan (779M533DL5)”…** → 文件格式选 **个人信息交换 (.p12)** → 存为 `~/Desktop/auralink-developer-id.p12` → 设一个密码（存进密码管理器，后面 `MACOS_CERTIFICATE_PASSWORD` 用）→ 系统可能会要求输入登录钥匙串的密码。
3. 按工作流的方式检查这个 .p12：导入一个临时钥匙串，只应找到 779M533DL5 的一个身份（会提示输入 .p12 密码）：
   ```sh
   cd ~/code/Auralink   # 任何一份 Auralink 的 checkout，要有 scripts/release-identity.sh
   kc="$(mktemp -d)/p12-check.keychain-db"
   security create-keychain -p p12-check "$kc"
   security import ~/Desktop/auralink-developer-id.p12 -k "$kc" -f pkcs12 -T /usr/bin/codesign
   security find-identity -v -p codesigning "$kc"          # 应该只有 1 个：wei guan (779M533DL5)
   scripts/release-identity.sh 779M533DL5 "$kc"           # 打印 <SHA-1><Tab>Developer ID Application: wei guan (779M533DL5)
   security delete-keychain "$kc"
   ```
   （`security import` 不带 `-P` 时会弹窗问密码，密码不进 shell 历史。）

### 1.2 App 专用密码（公证用）

公证用开发者账号的 Apple ID（york@actnow.dev）加一个 **App 专用密码**（不是 Apple ID 的登录密码）：

1. 打开 [account.apple.com](https://account.apple.com)，用 york@actnow.dev 登录 → **登录与安全** → **App 专用密码** → 生成，名称写 `Auralink notarization`。
2. 复制显示的密码（格式 `xxxx-xxxx-xxxx-xxxx`，**只显示一次**），先存进密码管理器。
3. 可选，在本机先验证一次（存进登录钥匙串的 `auralink-notary` 配置，和 CI 无关）。不加 `--password`，notarytool 会提示输入：
   ```sh
   xcrun notarytool store-credentials auralink-notary --apple-id york@actnow.dev --team-id 779M533DL5
   ```
   成功会显示 “Validating your credentials… Success”。

以后改了 Apple ID 的密码，App 专用密码会全部失效，要重新生成并更新 `APPLE_APP_PASSWORD`。

### 1.3 更新签名密钥

已经在 mac-air 上生成好（线程 #27，`go tool mygo keygen`）：

- 私钥：`~/.config/auralink-release/update-keys/mygo-update.key`（只有你能读，600；目录 700）
- 公钥：同目录的 `mygo-update.pub`，内容已经写进 [release/mygo.release.json](../release/mygo.release.json) 的 `updates.publicKey`。

**把私钥存进密码管理器**（例如 1Password 的“安全备注”，或附件）。已安装的 Auralink 只接受这把私钥签的更新：丢了它，已安装的 app 就再也收不到自动更新（见第 5 节）。私钥不进仓库，不发给任何人，只放在密码管理器和 GitHub Secret 里。

### 1.4 GitHub Secrets（7 个，加上提示音的 1 个）

在 mac-air 上执行。mac-air 上 `gh` 的登录已经失效，先重新登录（选 GitHub.com → HTTPS → 浏览器登录）：

```sh
export PATH=/opt/homebrew/bin:$PATH
gh auth login -h github.com
gh auth status
```

签名、公证和更新密钥（已经设好，换证书或密码时照这个更新；每条命令要么从文件读，要么在提示时粘贴，值不会出现在命令行和 shell 历史里；Team ID 和 Apple ID 不是秘密，直接写在命令里）：

```sh
REPO=hehehai/Auralink
base64 -i ~/Desktop/auralink-developer-id.p12 | gh secret set MACOS_CERTIFICATE -R "$REPO"
gh secret set MACOS_CERTIFICATE_PASSWORD -R "$REPO"            # 提示时粘贴 .p12 的密码
gh secret set APPLE_TEAM_ID -R "$REPO" --body 779M533DL5
gh secret set APPLE_ID -R "$REPO" --body york@actnow.dev
gh secret set APPLE_APP_PASSWORD -R "$REPO"                    # 提示时粘贴 App 专用密码
gh secret set MYGO_UPDATER_PRIVATE_KEY -R "$REPO" < ~/.config/auralink-release/update-keys/mygo-update.key
```

发布到官网（只有真正发布时才用到；试跑和 PR 不需要）：`ACTNOW_INGEST_TOKEN`，官网给 Auralink 的 token（`RELEASE_INGEST_TOKEN_AURALINK`，只能操作 Auralink 的版本；值在 `~/.config/actnow/ingest-tokens.env` 里 Auralink 那一行）。**已经设好。**以后要换：

```sh
gh secret set ACTNOW_INGEST_TOKEN -R "$REPO"      # 提示时粘贴
gh secret list -R "$REPO"                          # 应该列出这 7 个（加上 ACTNOW_SOUNDS_TOKEN）
```

家族提示音：`ACTNOW_SOUNDS_TOKEN`，私有仓库 hehehai/actnow-sounds 的只读 token（fine-grained，只给这个仓库的 Contents: Read）。音频是 React Bits Sound Lab 的（MIT + Commons Clause：可以放在 app 里，不能单独再分发），所以**永远不进本仓库，历史里也不能有**（`.gitignore` 忽略 `resources/sounds/`）。没有这个 Secret 时工作流给出警告，照常构建，app 播放 macOS 系统声音：

```sh
gh secret set ACTNOW_SOUNDS_TOKEN -R "$REPO"      # 提示时粘贴
```

设好以后，把 `.p12` 存进密码管理器，删掉桌面上的副本（`rm ~/Desktop/auralink-developer-id.p12`）。工作流里这些值都会被打码，notarytool 也不回显它们；发布脚本也不打印 token。不用 GitHub Environment 审批（用户 10-16 定）：Secret 只在 tag 构建和手动运行时用到，PR 构建拿不到。缺任何一个时，发布运行在构建之前就失败，并列出缺的名字。

### 1.5 试跑

不打 tag，手动跑一次（签名、公证都会做，但不发布：发布脚本加 `--dry-run`，只检查、打印要发给官网的请求；产物留在这次运行的 Artifacts 里，保留 7 天）：

```sh
gh workflow run release.yml -R hehehai/Auralink --ref main -f dry_run=true                    # 含公证
gh workflow run release.yml -R hehehai/Auralink --ref main -f dry_run=true -f notarize=false  # 不公证，省时间
gh run watch -R hehehai/Auralink
```

CHANGELOG 里还没有这个版本的一节时，试跑把 `## [Unreleased]` 当作这一节（只在 runner 的工作目录里改）。日志里 “Signing keychain” 一步应该打印 `Signing as Developer ID Application: wei guan (779M533DL5) (<SHA-1>)`。“Verify the app” 一步会检查：app 和辅助进程的签名（`codesign --verify --deep --strict`）、两者的 `audio-input` entitlement、hardened runtime、时间戳、同一个 Team 而且就是 `APPLE_TEAM_ID`、`spctl -a -vv`（应为 `source=Notarized Developer ID`）、app 和 dmg 的 `stapler validate`、更新清单、`CFBundleVersion` 只有数字（试跑不公证时跳过 Gatekeeper 和 staple 两项：模式 `unnotarized`）。脚本是 [scripts/release-verify.sh](../scripts/release-verify.sh)。“Publish to actnow.dev (dry run)” 一步列出要上传的文件（DMG、更新包、增量、清单）、大小和 SHA-256，以及请求体（版本、通道、notes 和 notesZh 的开头）。

在本机对一次构建做同样的检查（不需要 token，不发请求）：

```sh
bash scripts/actnow-publish.sh --app auralink --dir build/darwin-arm64 --changelog CHANGELOG.md --min-macos 15.0 --dry-run
```

## 2. 发一个版本

### 2.1 步骤（beta 和正式版一样）

1. **改版本号**，两处要一致（`scripts/build-helper.sh` 和工作流都会检查）。beta 用 `0.x.y-beta.n`，正式版用 `0.x.y`：
   ```sh
   v=0.1.0-beta.2        # 或 0.1.0
   jq --arg v "$v" '.version = $v' mygo.json > mygo.json.tmp && mv mygo.json.tmp mygo.json
   /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $v" helper/Support/Info.plist   # macOS
   ```
   app 的 `CFBundleShortVersionString` 是完整版本号（`0.1.0-beta.2`，更新比较用它）；`CFBundleVersion` 由 MyGo 写成数字部分（`0.1.0`，补丁 0007，Apple 要求只含数字和点）。
2. **写 CHANGELOG.md**：把最上面的 `## [Unreleased]` 改成 `## [0.1.0-beta.2] - 2026-11-02`（标题里的版本号必须和 mygo.json 完全一样，包括 `-beta.2`），英文在前，中文在后，小标题一一对应：
   ```markdown
   ## [0.1.0-beta.2] - 2026-11-02

   ### Fixed

   - The overlay no longer …

   ### 修复

   - 浮窗不再……
   ```
   更新窗口显示整节（MyGo 从 CHANGELOG.md 取，`## [版本]` 到下一个 `## `）。官网用同一节，由发布脚本分成两半：从第一个含汉字的 `### ` 小标题起是 `notesZh`，之前是 `notes`（只写 `### 中文` 这类标记的，这一行去掉）；后台检查清单里的说明包含 `notes`。没有这一节，发布构建失败；没有中文，工作流给出警告。
3. 提交、推到 main，等 CI 通过（`gh run list -R hehehai/Auralink --branch main -L 3`）。可以先做一次 §1.5 的试跑。
4. **打 tag**（tag 必须等于 `v` + mygo.json 的版本，否则工作流直接失败）：
   ```sh
   git tag -a "v$v" -m "Auralink $v"
   git push origin "v$v"
   gh run watch -R hehehai/Auralink
   ```
5. 工作流跑完就**已经发布**了（Auralink 在官网的设置是“complete 后自动发布”）：打开 Auralink 的人下次检查就会看到（beta 只有开着 Beta 更新的人），官网的更新日志和下载页也有了。“Publish to actnow.dev” 一步的最后打印版本状态（`published`）和改动了的更新源；也可以：
   ```sh
   curl -s https://actnow.dev/updates/auralink/beta/update-darwin-arm64.json | jq '{version, deltas: [.deltas[]?.from]}'
   curl -s https://actnow.dev/updates/auralink/update-darwin-arm64.json | jq .version    # 正式版
   ```
   打开 https://actnow.dev/auralink/changelog 看说明；登录 https://actnow.dev/download/auralink 下载 DMG 装一遍：`spctl -a -vv /Applications/Auralink.app` 应显示 `accepted` 和 `source=Notarized Developer ID`。

**发布失败**：脚本打印官网的错误（`error`、`message`、`details.errors`；不含 token），并中止这次会话（已传的文件由后台删掉）。常见的：

| 官网回复 | 意思 | 处理 |
|---|---|---|
| 401 / 403 | token 缺失、不对，或是别的 app 的 | 更新 `ACTNOW_INGEST_TOKEN` |
| 409 `version_exists` | 这个版本号已经有了 | 发一个新版本号（已发布、下架过的都不能再用） |
| 422 `version_not_newer` | 不比这个通道发布过的版本高 | 改版本号 |
| 422 `invalid_artifacts` / `verification_failed` | 文件名、大小、sha256、清单地址、签名或 notes 不对（`details.errors` 逐条列出） | 看是哪一条；清单地址不对多半是发布配置没合并 |

网络或官网 5xx 时脚本自己重试 3 次。修好原因后重新跑这次的工作流（同一个 tag：`gh run rerun <run-id> -R hehehai/Auralink`），或者在 tag 的 checkout 里用这次运行的 Artifact 手动发布：
```sh
gh run download <run-id> -R hehehai/Auralink -n "Auralink-$v-darwin-arm64" -D /tmp/auralink-$v
ACTNOW_INGEST_TOKEN=… bash scripts/actnow-publish.sh --app auralink --dir /tmp/auralink-$v --changelog CHANGELOG.md --min-macos 15.0
```

### 2.2 v0.1.0-beta.1 不发布

`0.1.0-beta.1` 的 tag 构建内置的是 GitHub 更新源，仓库改为私有以后匿名读不到它的 Release，装了也收不到更新，所以不发布（用户 10-16 定）：删掉它的 GitHub 草稿；官网里已经导入的 beta.1 保持未发布（可以在面板删除）。CHANGELOG 里它的一节标为未发布，内容并入 0.1.0-beta.2。第一个发布的版本是 **0.1.0-beta.2**，只发到官网，没有“GitHub 和官网各发一次”的过渡；它是 actnow.dev 上的第一个版本，没有增量，从下一个版本起才有。

### 2.3 发布脚本（actnow-publish.sh）

`scripts/actnow-publish.sh` 是官网脚本 `https://actnow.dev/actnow-publish.sh`（actnow-www 仓库的 `scripts/actnow-publish.sh`）**原样复制**进仓库的，不在 CI 里每次下载最新的：

- 发布用的代码在仓库里、经过审阅，每次运行用的是同一份字节；官网的脚本变了，不会悄悄改变我们的发布。
- PR 验证、试跑和 Mac 上的更新 e2e 都用同一份做 `--dry-run`，不依赖网络。

release 工作流的 lint 任务每次比较这份和官网现在提供的：不一样就给出**警告**（不失败），附上官网版本的 sha256。同步：

```sh
curl -fsSL https://actnow.dev/actnow-publish.sh -o scripts/actnow-publish.sh
git diff scripts/actnow-publish.sh        # 看改了什么，再提交
```

官网改了 API、旧脚本不能用时，发布会失败并给出官网的错误；同步后重跑即可。

## 3. 从 ad-hoc 换到 Developer ID：授权（TCC）

macOS 按 bundle ID + 代码签名的要求（designated requirement）记住麦克风和辅助功能授权。ad-hoc 签名的要求就是那次构建的哈希，Developer ID 的是“Apple 签发、Team ID 为 X、标识为 dev.hehehai.auralink”。所以：

- **第一次从自己编的 ad-hoc 版本换到发布版**，旧授权对不上：麦克风变成“拒绝且不再弹窗”，辅助功能开关看着开着但不起作用。换之前执行一次，再打开发布版重新授权：
  ```sh
  tccutil reset Microphone dev.hehehai.auralink
  tccutil reset Accessibility dev.hehehai.auralink
  ```
  如果辅助功能列表里还留着一个不起作用的 Auralink，用 − 删掉再加。
- **之后的更新**（同一个 Team ID、同一个 bundle ID）授权保留，beta 和正式版之间也一样。`identifier` 发布后就不能再改。
- 开发时用 Apple Development 证书签名的构建，要求和 Developer ID 不同，与发布版之间切换也要 reset 一次。
- 钥匙串里模型服务的 API Key 不受影响：条目是 `/usr/bin/security` 建的，信任的是它，不是 Auralink 的签名。

## 4. 更新怎么工作

### 4.1 检查和安装，以及“装好但还没重启”

- 插件每天在后台检查一次官网的更新源（启动后 10 秒起），有新版本时弹出更新窗口（`ShowInactive`，不抢正在打字的 App 的焦点），显示更新说明，提供 **安装更新 / 稍后提醒 / 跳过这个版本**。窗口语言跟随 Auralink 的界面语言（插件只取一次：改了界面语言，要下次启动才跟着变）。
- 手动入口：应用菜单（关于之后）、菜单栏图标的菜单、⌘K、设置 → 诊断与关于 → 更新（还有“Beta 更新”“自动检查”“自动安装更新”三个开关和上次检查时间）。三处都按 Beta 更新开关的状态检查。
- 安装时先下载增量，校验签名，用已安装的 app 拼出新版本、逐个文件核对 SHA-256；任何一步不对就改下载完整包。结果和发布的 app 逐字节一致（代码签名、公证票据都在）。
- 装好后插件提示重启（`App.Relaunch`：先走 Auralink 的退出流程，停掉辅助进程，再启动新版本）。
- **“自动安装更新”默认关**（插件的默认值）。开着时后台发现的更新会直接装好，但不会自动重启：MyGo 是在 app 运行时直接替换 bundle 的，旧进程继续跑，磁盘上的辅助进程已经是新版本。为此：
  - supervisor 每次启动辅助进程前比较磁盘上 `Info.plist` 的版本和正在运行的版本（`internal/appbundle`）；不一样就不再启动磁盘上的新辅助进程（`helper.FailUpdated`，也不自动重试）；新辅助进程如果说另一个协议版本，同样归为“已更新”，而不是“请重新编译辅助进程”。
  - app 每分钟、每次检查之后看一次磁盘上的版本；变了就在侧边栏底部出一张卡片“已安装更新 / 重启 Auralink 以完成更新”，首页和设置 → 诊断与关于也显示“重启 Auralink”。重启后卡片消失。

### 4.2 增量（delta）在两个通道之间

MyGo 原来的做法：构建时读更新源上已发布的清单（`updates.url` 的 `update-darwin-arm64.json`），从它和它清单里列的前几个版本（最多 3 个）生成增量。只有一个通道时这够了；有了 beta 通道，正式版的清单里没有 beta，beta 用户就拿不到下一个正式版的增量。

补丁 0006 的 `updates.prereleases`（[release/mygo.release.json](../release/mygo.release.json) 里打开）让构建同时读**两个通道的清单**（`updates.url/update-darwin-arm64.json` 和 `updates.url/beta/update-darwin-arm64.json`）：先放两个通道各自的版本，再按版本号从高到低放它们清单里列的旧版本，去重、只取比当前低的，最多 3 个。增量文件和这个版本的更新包放在同一个目录（正式版在 `auralink/`，beta 在 `auralink/beta/`），清单里是完整的 URL，所以正式版的清单复制到 beta 通道以后照样指向 `auralink/` 下的文件。

| 构建（已发布的） | 增量来自 |
|---|---|
| `0.1.0-beta.2`（无） | 无（第一个版本） |
| `0.1.0-beta.3`（beta.2） | beta.2 |
| `0.1.0`（beta.2、beta.3） | beta.3、beta.2 |
| `0.2.0-beta.1`（…、0.1.0） | 0.1.0、beta.3、beta.2 |
| `0.1.1`（…、0.2.0-beta.1） | 0.1.0、beta.3、beta.2（0.2.0-beta.1 比 0.1.1 新，不算；beta 通道仍是 0.2.0-beta.1） |

所以两个通道的最新版本的用户总能拿到增量；更旧的版本、或者增量用不上（改过 app）的，下完整包。§6 的 e2e 实测了同样的表（版本号从 beta.1 起）。

## 5. 回滚、密钥和证书出问题时

- **发布后发现问题**：在官网面板（https://actnow.dev/panel/releases）操作：**下架**（Unpublish）这个版本，或者对上一个版本 **Roll back to this**（把更新的版本标为 yanked，更新源指回它）。后台重新计算并写回公开清单。**已经装了新版本的不会降级**，回滚只阻止继续推送。修好后发一个**更高**的版本（0.1.2 或 0.2.0-beta.2）；下架过的版本号不能再用。已经装了坏版本、急需回退的人，可以从官网下载旧版本的 DMG 拖进“应用程序”覆盖（同一 Developer ID，授权保留），之后照常收到新版本。
- **更新私钥丢失**：已安装的 app 只认这把私钥，再也收不到自动更新。只能用新密钥发新版本（换 `release/mygo.release.json` 里的公钥），并请所有用户手动下载安装一次。所以一定要在密码管理器里留一份。
- **更新私钥泄漏**：拿到它、又能往更新源上放文件的人（官网后台还会用面板里登记的公钥验签名，但拿到私钥的人签出来的照样验得过），可以给所有用户推送任意代码。立即：用旧私钥签发一个只换了公钥的新版本（之后的更新改用新密钥，官网面板里的公钥也要换），撤换 GitHub Secret，检查仓库、Actions 和官网面板的访问权限。
- **ingest token 泄漏**：拿到它的人能以 Auralink 的名义建发布会话，但文件要过后台的签名校验（公钥在面板里），所以推送不了恶意更新；可以占用版本号、发布已签名的旧构建。请官网项目换掉 `RELEASE_INGEST_TOKEN_AURALINK`，更新 `ACTNOW_INGEST_TOKEN`，在面板里检查 Auralink 的版本和审计日志。
- **App 专用密码泄漏或不用了**：在 account.apple.com → 登录与安全 → App 专用密码 里撤销它，重新生成一个，更新 `APPLE_APP_PASSWORD`。它只能用于公证这类服务，不能登录 Apple ID。
- **Developer ID 证书**：有效期 5 年，过期后带时间戳签的旧版本仍能运行，只是不能再签新版本；换新证书（同一 Team）后授权保留，更新 `MACOS_CERTIFICATE` 和密码即可（工作流按 Team 选，按 SHA-1 签）。证书被吊销则已签名的版本可能无法启动。

## 6. 不用证书测试更新

两个脚本在 Mac 上用一把**测试**密钥和 ad-hoc 签名把整条更新链跑一遍：按发布配置构建（`release/mygo.release.json`，`url` 换成测试的），每个构建先用官网的发布脚本 `actnow-publish.sh --dry-run` 检查（文件名、通道、清单地址、CHANGELOG 拆出的中英文说明），再由 [scripts/update-server](../scripts/update-server/main.go) `-publish` 像官网后台那样摆进一个代表 R2 桶的目录（beta 进 `beta/`，清单最后，beta 通道的清单取较新的，版本号必须更高），`-site` 在本机回环地址上用 HTTPS 模拟 actnow.dev 的 `/updates/auralink[/beta]/`（只提供官网会提供的文件，DMG 之类一律 404；自签证书只通过 `SSL_CERT_FILE` 让这几个进程信任）。真正的上传和后台校验只在官网那边测（actnow-www 的 `pnpm e2e:release`）。不开窗口、不抢焦点、不启动辅助进程、不碰钥匙串，nice、两个并发，可用内存低于 40% 就停；结束时删掉测试密钥、构建和更新源（`KEEP=1` 保留在 `build/` 下）。在独立目录里跑：

```sh
MAC_DIR=code/Auralink-site scripts/mac.sh scripts/mac-update-e2e.sh   # 安装方式：增量、完整包、增量失败后改完整包、已最新
MAC_DIR=code/Auralink-site scripts/mac.sh scripts/mac-beta-e2e.sh     # 两个通道
```

[scripts/mac-update-e2e.sh](../scripts/mac-update-e2e.sh)：造 0.1.0 和 0.1.1 两个正式版（bundle ID `dev.hehehai.auralink.updtest`），把装好的 0.1.0 用 `-tags updatecheck` 编进去的 `--update-check`（[update_check.go](../update_check.go)，发布版没有）驱动真实的 `mygo.Updater.Check/Install`，检查三种安装方式、装完的 bundle 和发布的逐文件一致、旧进程不启动新辅助进程、新版本“已是最新”；还检查 app 的 Resources 里有 LICENSE、NOTICE、LICENSES/，发布脚本的 `--dry-run` 接受这两个构建，正式版的清单也进了 beta 通道。

[scripts/mac-beta-e2e.sh](../scripts/mac-beta-e2e.sh)（bundle ID `dev.hehehai.auralink.betatest`）：依次构建并发布 `0.1.0-beta.1`、`0.1.0-beta.2`、`0.1.0`、`0.2.0-beta.1`、`0.1.1`，每次发布后检查两个通道的清单是哪个版本，并让已安装的各版本在 Beta 更新开 / 关 / 默认下检查并安装：

| 场景 | 已安装 | Beta 更新 | 期望 |
|---|---|---|---|
| a | beta.1（只有 beta） | 默认（开） | beta.2，增量 |
| b | beta.1（只有 beta） | 关 | 无（正式版通道 404：已是最新） |
| c / d / e | beta.2 / beta.2 / beta.1（有 0.1.0 后） | 默认 / 关 / 开 | 0.1.0，增量（beta 通道此时也是 0.1.0） |
| n | beta.2，app 被改过 | 开 | 0.1.0，增量失败后完整包 |
| f / g | 0.1.0（有 0.2.0-beta.1 后） | 默认（关） / 开 | 无 / 0.2.0-beta.1 增量 |
| h / i | beta.2（有 0.2.0-beta.1 后） | 默认 / 关 | 0.2.0-beta.1 / 0.1.0，增量 |
| j / k | 0.2.0-beta.1（有 0.1.1 后） | 关 / 默认 | 无（不降级到 0.1.1；beta 通道仍是 0.2.0-beta.1） |
| l / m | 0.1.0（有 0.1.1 后） | 默认 / 开 | 0.1.1 / 0.2.0-beta.1（beta 通道取较新的），增量 |
| o / p | 0.1.0 / 0.2.0-beta.1，beta 通道的清单坏了 | 开 | 退回正式版：0.1.1 / 报错（正式版没有更新的） |

还检查每个版本的增量来自哪些版本（§4.2 的表）、发布脚本的 `--dry-run` 接受每个构建（beta 走 beta 通道、中英文说明都有）、`CFBundleVersion` 是数字、更新源只被请求过 `/updates/auralink/` 下的文件、没有请求 DMG。

## 7. 只能用真证书、真官网验证的

第一次真实发版（或 1.5 的试跑）之后，在 mac-air 上确认：

1. 公证通过；`release-verify.sh` 全部通过（hardened runtime、时间戳、Team、Gatekeeper、staple）。如果公证因为辅助进程放在 `Contents/Resources` 被拒，看 `xcrun notarytool log` 再处理（MyGo 支持这种布局，目前预期能过）。
2. reset TCC 后授权麦克风和辅助功能，录音、粘贴、读浏览器网址都正常；主程序的 `audio-input` entitlement 在 hardened runtime 下够用（主进程自己请求麦克风、辅助进程录音两条路都要试）。
3. 第一次真实发布：后台校验通过（签名用面板里登记的公钥）、版本是 `published`、两个通道的清单对、DMG 能从下载页下载、更新日志显示中英文说明。
4. 发 `0.1.0-beta.3` 后：在 beta.2 里“安装更新 → 重启”，**授权仍然有效**；增量和完整包各走一次；
5. 安装后点“稍后”：旧进程还能不能录音和粘贴（磁盘上的代码已经换了），卡片“已安装更新”是否出现，点“重启 Auralink”后一切正常；
6. `App.Relaunch` 是直接 exec 新的可执行文件（不经 LaunchServices），重启后的进程麦克风和辅助功能是否正常。

## 8. 为什么这样做

- **走官网，不用 GitHub Release**（用户 10-16 定）：仓库改为私有后，Release 的文件匿名读不到；官网统一管下载（要登录）、更新源和更新日志，家族的几个 app 都一样。文件经官网后台进 R2（官网 10-16 晚的新契约）：CI 不持有 R2 密钥，校验、发布顺序、beta 取较新的、版本号只升不降都由后台统一保证。
- **`updates` 不写进仓库的 mygo.json**，只在发布时由 [scripts/release-config.sh](../scripts/release-config.sh) 合并 [release/mygo.release.json](../release/mygo.release.json)。核实过：`mygo.Updater.Enabled()` 只在 `mygo dev` 的开发构建里为 false；任何带 `updates` 的 `go tool mygo build`（包括 mac-air 上各线程的 ad-hoc 测试构建）都会链接更新源，放在可写目录里就会每天去检查，发布后会提示、甚至在开了自动安装时用正式版替换掉测试构建（替换只看路径，独立 bundle ID 的也一样）。公钥本身不是秘密，放在仓库里便于审阅。签名身份和公证配置同样只在 CI 里注入，所以 notarize 不会让本地 ad-hoc 构建报错。
- **两个更新源，beta 取较新的**：和 Voxt（Sparkle 的两个 appcast）一样，也是官网的约定。开着 Beta 更新的 app 只读一个清单；“较新”由官网后台在发布时算好。读 beta 清单失败时退回正式版，所以 beta 通道出问题时 beta 用户仍能收到正式版。
- **发布配置里 url 始终是正式版的，加 `prereleases`**，而不是像官网文档那样 beta 构建把 url 改成 `…/beta`：app 要能在两个通道之间切换（Beta 更新开关），就得同时知道两个更新源，增量也要读两个通道的清单；补丁 0006 让 beta 构建的文件地址落在 `…/beta/` 下，结果和官网要求的一样（后台检查的是清单里的地址）。
- **按 SHA-1 选签名身份**：codesign 遇到两张同名证书会报 “ambiguous”（mac-air 上 RY2TQTYSKQ 就有两张同名的），按哈希签不会。CI 的临时钥匙串里只有导入的那一张，脚本仍按 `APPLE_TEAM_ID` 核对。
- **主程序也有 `audio-input` entitlement**（[helper/Support/app.entitlements](../helper/Support/app.entitlements)）：hardened runtime 下主进程自己请求麦克风时需要；辅助进程录音时 macOS 也把主程序当作负责进程。成本为零。
- **只在 tag 上发布**；手动运行默认试跑（可以跳过公证）；PR 只做不带 Secret 的 ad-hoc 构建、检查和发布脚本的 `--dry-run`。action 固定到 commit，job 只有 `contents: read`（发布用官网的 token，不写仓库），同一个 ref 不并发、不中途取消，缺 Secret 时第一步就报出缺哪几个。
