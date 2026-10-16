# MIXED-API · 统一的本地 OpenAI 兼容中转站

[![CI](https://github.com/NLKASHEI/MIXED-API/actions/workflows/release.yml/badge.svg)](https://github.com/NLKASHEI/MIXED-API/actions/workflows/release.yml)

**六个可配置上游 → 一个 API Base URL + 一个客户端 Key → 自动按模型 ID 分流。**

> 注：仓库内早期的深蓝色网页效果图属于旧 UI，新版 Windows 已改用原生系统窗口；不要把旧截图当作当前主界面。

Windows 默认打开原生 Win32 管理窗口，无需浏览器；Android 使用原生应用控制页。旧版网页后台仍保留为高级兼容入口。

本项目只实现一个本地网关，**不复制、捆绑、不尝试绕过上游认证**。上游授权、额度、收费和软件许可证继续生效。

## 一键接入：软件会按实际进度告诉你下一步

**点左侧来源的「接入」后，不要猜怎么操作。** Windows 会先执行 **① 自动准备**，然后**停在 ② 本人授权／确认**，在确实需要网页授权时自动打开官方站点或交互登录窗口。**只有用户点击「我已完成，下一步」后才会进入 ③ 模型验证**；失败会回到 ②，不能自动跳过。登录成功但没有模型不会显示接入成功。软件可直接从下拉框复制完整模型 ID，底部保留精简请求错误记录。

[查看每个来源如何接入、成功后如何填写客户端的零基础分步指引](docs/quickstart-guided.md)。**Windows 默认原生窗口，不用打开浏览器管理后台**；需要高级选项时才进入网页。

## 软件的“一键接入”与永久更新入口

**Windows：打开 EXE 的原生主窗口，直接点击对应来源的「一键接入」即可。** MIXED-API 会在用户点击后执行固定版本下载、SHA-256 校验（对有官方编译包的来源）、安装所需依赖、自动生成仅用于本机的 Key / 配置、启用来源并启动。只有账号必须本人授权时才额外处理，不要求手工复制 JSON、寻找原项目下载链接或自己填写本机端口。

**Windows 原生窗口顶部「检查更新」是永久入口**，也可以在未登录时打开 [GitHub 最新正式版](https://github.com/NLKASHEI/MIXED-API/releases/latest)。Windows 支持检查最新版本、下载并验证官方 Setup.exe 的 SHA-256、打开安装向导，仍需由用户确认安装（并在更新安装版时退出正在运行的旧进程）；系统不会偷偷升级或丢弃用户的 `mixed-api.json`。

**Android** 的 APP 首页和管理页也有固定更新入口，但 Android 的 APK 安装需要系统用户确认。当前云端 APK 采用 Debug 签名，若两次构建的签名不同，不能覆盖安装旧版；必须在 GitHub Actions 配置持久签名密钥，才可保证之后同签名升级。当前安卓主要提供本地网关和电脑桥接，**不能原生运行 Windows Codex CLI、WorkBuddy、Camoufox**。

| 来源 | 软件能自动做的事 | 用户唯一需要做的授权或输入 |
| --- | --- | --- |
| Codex | 安装 CLI、直连 app-server、汇总模型 | 第一次本人登录 ChatGPT 并确认官方授权；无须 SillyTavern |
| Vertex2OpenAI | 下载固定源码、Python 虚拟环境、代理 Key、启动 | 自己的 Google Express Key 或上游要求的合法认证 |
| OpenCode | 下载已编译版本、SHA-256 校验、生成服务端 Key / 配置、启动 | 可选 Zen Key；匿名模式是否可用取决于原项目 |
| WorkBuddy | 下载代理后端并启动、自动寻找已登录的桌面客户端 | 需要已安装并登录官方 WorkBuddy 客户端 |
| APS2api | 下载已编译版本、SHA-256 校验、本地密钥/管理密码、启动 | 确认许可和使用规则；原项目监听所有网卡，应只在可信网络使用 |
| AIStudioToAPI | 下载源码、npm 依赖、随机本地 Key 和回环地址、启动 | 首次在 Camoufox 中本人完成 Google AI Studio 网页登录 |

注意：**六路是 Windows 上受软件管理的可选能力，不是六套第三方授权凭证都已获得。** 安装 Node.js / Python 等系统运行时若 Windows 未刷新 PATH，需要重启 MIXED-API 后再次点击一键接入。上游启动不保证账号已经授权或存在模型，最终是否可用以「聚合模型」列表和真实请求为准。

## 全新原生管理窗口：不开浏览器也能直接管理

- **Windows 双击 EXE / 安装版快捷方式**：直接打开原生窗口，顶部复制统一 URL 和客户端 Key，中部六个「一键接入」按钮，以及仅在需要时填写的 Vertex Express / OpenCode Zen 官方 Key 输入区；底部持续更新 **HTTP 请求状态码、响应时间、后端来源、错误原因**。界面使用较大字号，不用从五级网页导航里找设置。Windows EXE 默认隐藏控制台窗口；要无界面部署，可用命令行 `--no-browser`。
- **Android 直接打开 APK**：原生「启动服务」「复制地址/Key」「连接上游」「检查模型」「实时日志」五个功能区。电脑桥接只需要电脑局域网 URL 和电脑客户端 Key；手机自动保管本地 Key，网页仅是可选的高级入口。
- **日志安全**：仅在进程内保留有限条请求记录，成功请求不保存正文；失败只截取简短错误提示并做密钥脱敏。管理员接口 `GET /api/logs` 支持读取诊断、`DELETE /api/logs` 清除；同局域网开放的 8766 端口不能查看日志或管理页面。
- **入口不改变 API**：客户端还是填 `http://127.0.0.1:8765/v1` 和 MIXED-API 客户端 Key。切换 UI 不影响模型名前缀和上游服务设置。首次第三方授权仍由用户本人完成。

## Release 下载：安装版 / 免安装版 / Android

在仓库右侧点击 **Releases → Latest**，选择以下附件：

- **Windows 一键安装包 `MIXED-API-Windows-x64-Setup.exe`**：下载后双击，按照中文向导安装，会创建桌面/开始菜单快捷方式，支持从 Windows「已安装的应用」卸载。使用当前用户的 `%LOCALAPPDATA%\\MIXED-API`，不申请管理员权限；升级会保留原先生成的 `mixed-api.json`。
- **Windows 免安装包 `MIXED-API-Windows-x64.zip`**：解压到任意可写文件夹，双击 `Start-MIXED-API.cmd`。ZIP 含 `Start-MIXED-API-LAN.cmd`，可显式开启仅用于 API 的局域网 8766 端口。
- **Windows 单独 EXE `MIXED-API-Windows-x64.exe`**：下载后放进独立文件夹直接运行；它是便携主程序，**不是安装程序**，不会创建快捷方式或卸载信息。
- **Android APK `MIXED-API-Android.apk`**：下载后安装，打开 APP 并启动本地网关。手机上用不了 Windows 专属的 Codex/WorkBuddy 桌面进程，连接电脑的 LAN API 则可以复用其模型。

安装器目前未进行 Windows 代码签名。Windows 可能弹出 SmartScreen 提示；请仅从本仓库官方 Release 下载。所有上游凭证仍需要依照对应指引配置；**装好安装包不代表六家上游已经自动登录**。

## 一键使用与保姆级反代指引（最新）

**首次使用请先看 [零基础使用指南](docs/quickstart-zh.md)**。管理后台的每张上游卡片也内置逐步教程和官方网址，解释每个 Key 的来源。

- **Codex：已直连 CLI，不需要 SillyTavern**。Windows 点击「一键安装 Codex」→「打开账号授权窗口」→ 启用并刷新模型。首次会自动弹出 Codex 官方登录窗口，需要本人确认 ChatGPT 授权；MIXED-API 不保存 OAuth Token。仅使用 ChatGPT 订阅账号，拒绝 API Key 登录和任何 MCP/工具。
- **WorkBuddy：** Windows 点击「一键准备环境」→「启动后端」。程序从可信仓库固定提交下载中转并由上游自动检测已登录的正版 WorkBuddy 客户端；第一次仍需本人在客户端登录。
- **AI Studio Build：** Windows 点击「一键准备环境」→「打开账号授权窗口」→「启动后端」。自动安装 Node 依赖并设置本地随机上游密钥/回环监听；原项目 Camoufox 登录需要本人完成一次。无需浏览器认证时请考虑官方 Express Key 模式而不是 Build App 浏览器代理。
- **其余三项：** 在控制台按每个来源的「从零开始配置向导」安装对应项目，填写它们各自的服务器 Key 和 API 地址。

> **实际打包区别：** `MIXED-API-Windows-x64-Setup.exe` 才是真正的一键安装器，`MIXED-API-Windows-x64.exe` 是可独立双击的 Go 可执行文件，**无须安装 Go、Node 或 Python 就能运行聚合网关**。Node/Git/Codex 只在使用对应的桌面上游时需要；ZIP 版多了启动脚本和 LAN 模式入口。

> **平台差异：** Windows 版拥有本机 Codex 适配器和部分自动安装功能；Android APK 提供统一 HTTP 网关与教程，但不能在 APK 内运行桌面版 Codex、WorkBuddy、Camoufox。所有来源依赖各自权限/额度，未经真实账号登录不保证端到端成功。


## 快速开始

### Windows（x64）

1. 在 [GitHub Releases](https://github.com/NLKASHEI/MIXED-API/releases/latest) **优先下载 `MIXED-API-Windows-x64-Setup.exe`**，按向导完成一键安装后点击桌面或开始菜单快捷方式。无需安装 Go。也可下载 ZIP 便携版（含 EXE 和 LAN 启动脚本），或者仅下载单个 EXE。
2. 安装版从快捷方式启动；ZIP 用户双击 `Start-MIXED-API.cmd`；单文件用户双击 `MIXED-API-Windows-x64.exe`。**现在直接弹出原生管理窗口，不需要打开浏览器或输入 Admin Key**。客户端 API Key 与 Admin Key 会自动生成并保存在用户配置文件。
3. EXE 原生窗口里可直接复制统一 API 地址和客户端 Key，点击六个来源的「一键接入」，如有需要在下方粘贴官方 Vertex Express / OpenCode Zen Key。Windows SmartScreen 对未签名预览 EXE 可能提出警告，请仅从本仓库 Releases 下载，核实文件后再决定是否运行。
4. 原生窗口会自动下载、安装、生成本地 Key 并启动受管来源。ChatGPT / Google AI Studio / WorkBuddy 的首次账号授权需要本人完成。
5. 配置完成后用任意 OpenAI 兼容客户端读取 `/v1/models`；窗口底部实时显示 API 请求、错误码及原因。在客户端使用 `http://127.0.0.1:8765/v1` 和 **MIXED-API 客户端 API Key**。

提示：直接下载的 EXE 无需安装 Go/Node；完整 ZIP 内部的可执行文件名是 `MIXED-API.exe`，另附可选启动脚本。**一键启动的是聚合网关本身**。需要客户端登录的上游仍需预先配置；可以在管理界面填写 Windows 本机启动命令，由网关启动已安装的后端。

### Android（独立 APK，推荐「电脑桥接」模式）

**重要事实：** Windows Codex CLI / WorkBuddy / Camoufox 没有随 APK 移植到 Android；APK 自己不能运行这些桌面程序。为了让手机像 PC 一样得到**六上游统一模型列表**，本项目新增受限的 LAN API 共享：

1. 电脑安装 Windows 包，在配置后台启用、调通你需要的后端，例如 `codex/gpt-*` 或 `workbuddy/*`。
2. 电脑退出原来的 MIXED-API，**下载 ZIP 便携包**并解压（单独 EXE 附件不包含脚本），改为双击 **`Start-MIXED-API-LAN.cmd`**。电脑本机后台仍为 `http://127.0.0.1:8765`；另外仅共享 `0.0.0.0:8766` 的 `/v1/*` API 和 `/health`，**拒绝管理页面、`/api/config`、安装或进程控制请求**。
3. 在电脑输入 `ipconfig` 查当前 Wi-Fi/以太网的真实 IPv4，例如 `192.168.1.20`。防火墙仅在**专用网络**放行 TCP 8766。电脑、手机连接同一可信 Wi-Fi，**不能使用 PC 的 `127.0.0.1`**。
4. 在手机 APK 后台开启 **「电脑 MIXED-API（优先推荐）」**，把上游 URL 设为 `http://192.168.1.20:8766/v1`（换成真实 IP），上游 Key 填**电脑 MIXED-API 的客户端 API Key**，Auth=`bearer`。勾选启用，保存并刷新模型。
5. 手机的任何第三方客户端仍只需填 `http://127.0.0.1:8765/v1` 和**手机自己生成的客户端 API Key**。手机模型 ID 保持 `codex/gpt-...`、`workbuddy/...` 等，**不会套一层 `pc/`**。电脑关闭、掉线或者离开同一个网络后，就无法使用电脑上的模型。

此桥接方案不需要 SillyTavern，纯粹是两个 MIXED-API 网关之间的模型转发。局域网 HTTP **不加密**；仅限可信网络，切勿将 8766 直接映射到公网。如需外网连接，请先使用可靠的加密 VPN。

**只用安卓、完全不依赖电脑？** 可以选择自己在 Android 可运行、并能提供标准 HTTP API 的后端。但 **Codex 桌面 CLI、WorkBuddy 客户端、Camoufox/AIStudio 浏览器后端不能凭当前 APK 原生运行**，这些模型在纯手机离线/独立状态不可用。



1. 下载 Release 附件 `MIXED-API-Android.apk`，安装 Android 8.0+ 设备。
2. 点击「启动本地代理」，再点「打开管理后台」。
3. 在 APP 内复制 **Admin Key** 登录管理界面；根据说明启用已部署的上游。
4. 手机端任何本机客户端使用 `http://127.0.0.1:8765/v1` 和 APP 内复制的 **客户端 API Key**。

**注意：** 旧版 APK 不支持自动桥接电脑；升级到新增了「电脑 MIXED-API」卡片的版本后使用。APK 的前台服务仍可能被系统电池策略终止。

## 聚合规则

| 上游 ID | 上游项目 | 默认 API URL | 需要先做什么 |
|---|---|---|---|
| `vertex` | [vertex2openai](https://github.com/bad-woman/vertex2openai) | `http://127.0.0.1:8050/v1` | Express Key 或 Cookie/Project ID；启动原版服务 |
| `codex` | [OpenAI 官方 Codex CLI](https://github.com/openai/codex) | **Windows 内置适配器，不使用端口 URL** | 安装 Codex CLI 并执行 `codex login`，无需 SillyTavern 服务端；Android 版仅可连接独立的兼容 HTTP 上游 |
| `opencode` | [opencode2api](https://github.com/jasonxu114514/opencode2api) | `http://127.0.0.1:8080/v1` | 设置 upstream/本地 server key，启动 Go 版服务 |
| `workbuddy` | [workboddy-proxy](https://github.com/sitingge/workboddy-proxy) | `http://127.0.0.1:8790/v1` | 安装并登录桌面 WorkBuddy，运行 `node wb-relay.js` |
| `aps` | [APS2api](https://github.com/DeconstructedCube/APS2api) | `http://127.0.0.1:2156/v1` | 启动程序并在 `/admin/` 建立本地 API Key |
| `aistudio` | [AIStudioToAPI](https://github.com/iBUHub/AIStudioToAPI) | `http://127.0.0.1:7860/v1` | 在电脑执行 `npm run setup-auth` 完成 Camoufox 登录，再运行 `npm start` |

各上游默认 **禁用**。成功启用并连接后，`GET /v1/models` 汇总其返回的模型，并把模型 ID 加上前缀：`vertex/gemini-...`、`codex/gpt-...`、`aistudio/gemini-...` 等。只凭前缀选择上游，不会跨供应商自动切换，不会进行额度合并和负载均衡。

支持：`GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/embeddings`、`POST /v1/messages`、`POST /v1/messages/count_tokens`。除 Codex CLI 目前只支持文本 Chat Completions 外，其他接口只在**上游本来支持相应路径**时可用，不会自动转换协议。保留普通和 SSE 流式数据，普通请求会把模型前缀从请求体移除后交给上游。上游响应中模型字段仍可能是未加前缀的原始 ID。

### 客户端示例

```bash
curl http://127.0.0.1:8765/v1/models \
  -H 'Authorization: Bearer sk-mix-你的客户端Key'
```

```bash
curl http://127.0.0.1:8765/v1/chat/completions \
  -H 'Authorization: Bearer sk-mix-你的客户端Key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"codex/选择模型列表里的具体模型","messages":[{"role":"user","content":"你好"}]}'
```

### 开发测试

Requires Go >=1.23 to compile the Windows gateway; JDK 17 and Android SDK to compile the APK.

```bash
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/mixed-api -no-browser
```

Android project under `android/` (Gradle + Android Java, without Python runtime). CI 直接把 Windows x64 `.exe`（另附便携 ZIP）和 Android Debug-signed `.apk` 上传到 GitHub **prerelease**; debug signature is for self-use and testing, not Play Store deployment. Device installation/network behavior remains to be tested.

## 安全和许可

- 默认只绑定 `127.0.0.1:8765`。在 Windows **明确使用 `Start-MIXED-API-LAN.cmd` 时**，额外开放 LAN 端口 `8766`，但**该端口只接受带客户端 Key 的推理 API**；管理页面、Admin Key 和上游安装操作永不通过此端口访问。只在可信专用网络开启，不要端口映射到公网。
- 本地随机生成两套密钥：**Client API Key** 只能用 API，**Admin Key** 才可查看和更改包含上游密钥的配置。Desktop 密钥文件权限设为 `0600`；Android 通过私有 SharedPreferences 保存，**没有加密备份/硬件密钥库**。
- 不要把 Google Cookie、OpenAI 授权 token、WorkBuddy 会话或本地配置文件提交到 GitHub，也不要将这些信息发给不受信任的人。
- 订阅额度不等同于可供任意软件使用的通用 API。请遵守各自的许可证、服务条款、额度以及授权用途。本项目不保证使用第三方反向代理不触发服务商限制。
- 本仓库只发行**自行编写的网关**，使用上游的公开 HTTP 接口；**不会把原项目源码重新打包分发**。特别注意 OpenCode 的 GPL-3、原 SillyTavern-Subscriptions 的 AGPL-3（本版本不捆绑它的代码）、APS2api 的 PolyForm Noncommercial 和 AIStudioToAPI 的 CC BY-NC 4.0，均有各自分发或使用条件。

开发约定见 [`AGENTS.md`](AGENTS.md)、[`GLOSSARY.md`](GLOSSARY.md)、[`docs/adr/0001-gateway-adapters.md`](docs/adr/0001-gateway-adapters.md)，以模块边界、TDD 和安全审查为中心，参考 [mattpocock/skills](https://github.com/mattpocock/skills) 的工程实践，**没有直接复制其 skills 内容**。

### Android 同签名更新（维护者设置一次）

Android 的 Debug APK 只有**使用与已安装包相同的签名密钥**才能覆盖升级。项目 CI 支持维护者在 GitHub Secrets 中设置 `MIXED_API_ANDROID_DEBUG_KEYSTORE_B64`，其内容是私有 debug.keystore 的 base64；文件必须含 `androiddebugkey` 别名及标准 debug keystore 密码 `android`。**切勿提交 keystore 文件或 base64 到公开仓库。** 首次采用持久签名时，如果以前装的是不同签名的测试包，仍可能需要备份配置后卸载旧版一次；此后保留同一签名才可正常升级。当前未设置 Secret 的构建仅作预览，不能承诺无损覆盖升级。
