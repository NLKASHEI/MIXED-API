# MIXED-API 零基础使用指南：六个来源，一个 URL + 一个 Key

> 本文同时内置在控制台中，随软件版本一起维护。MIXED-API 是个人本机的 **API 聚合器**，不是六家服务的免授权替代品。你只需配置自己想用的来源，不需要六个全部安装。

## 一分钟确认概念

- **MIXED-API**：本机统一服务。浏览器访问 `http://127.0.0.1:8765`；客户端 Base URL `http://127.0.0.1:8765/v1`。
- **Admin Key**：只用来登录 MIXED-API 管理后台。第一次启动在 Windows 的黑色命令行里显示，也保存于软件目录的 `mixed-api.json`。不需要把它给聊天软件。
- **Client API Key**：打开后台后可复制，**这才是**你填给任何 OpenAI 兼容客户端的密钥。
- **上游 API Key**：某个单独代理发给 MIXED-API 的密钥，只填该来源卡片，不等于 ChatGPT 登录密码，也不等于 MIXED-API 的 Client Key。
- **模型 ID**：形如 `codex/gpt-...`、`aistudio/gemini-...`。`/v1/models` 显示**实际上能读取到的模型**，没有启用的来源不会出现。
- **`127.0.0.1`**：永远指运行当前软件的**那一台设备**。手机不能用 `127.0.0.1` 去连接电脑上的服务。MIXED-API 默认只允许本机访问，不自动开启局域网共享。

## 首次使用

1. Windows：从 [Releases](https://github.com/NLKASHEI/MIXED-API/releases) 下载 **`MIXED-API-Windows-x64.exe`**，放进自己新建的文件夹中，直接双击，不需要先安装 Go。如果需要给手机分享模型，则下载 ZIP 便携包（内含 `Start-MIXED-API-LAN.cmd`）。Android：安装 APK 并点击启动服务。**运行中的 Windows 命令行窗口不要关闭**。
2. 打开 `http://127.0.0.1:8765`，填写管理密码。
3. 点击左侧 **「上游服务」**，找到你要连接的来源，展开 **「从零开始 · 安装、登录、密钥填写指引」**。有些来源可以点击 **「一键安装 / 准备」→「打开登录窗口」→「启动上游」**。不会擅自安装或绕过账号授权。
4. 成功启动并登录上游后，勾选 **「启用这个来源」** → 点击页面右上角 **「保存所有配置」** → 点左侧 **「聚合模型」** → 点击 **「刷新列表」**。如果出现 `来源/模型` 即成功，否则查看模型列表下的上游错误提示。
5. 回到 **「总览首页」** 复制统一 URL 和客户端 Key，在任何支持 OpenAI Chat Completions 的客户端中填写：Base URL `http://127.0.0.1:8765/v1`、API Key 为 MIXED-API **客户端** Key、Model 填从模型列表复制的完整名称。

## 安卓使用 Windows 的全部六路后端（最重要）

这里是之前漏掉的关键架构：**电脑为后端宿主机，手机为前端网关。** 安卓 APK 不会、也无法直接启动 Windows Codex/WorkBuddy/Camoufox；现在提供电脑的 **API-only LAN 端口** 做桥接。

1. **电脑：**先让 Windows MIXED-API 后台中的 Codex、WorkBuddy 等上游可用，刷新出真实模型。然后关闭旧启动窗口，双击 `Start-MIXED-API-LAN.cmd`。
2. **电脑：**打开 `http://127.0.0.1:8765`，复制 **客户端 API Key**。千万别复制 Admin Key。
3. **电脑：**运行 `ipconfig`，找到联网网卡 IPv4，比如 `192.168.1.20`；防火墙仅允许 **专用网络** TCP 8766 入站。不要给端口做公网映射。
4. **手机：**安装新版 MIXED-API APK，启动并进入管理后台，打开 **「电脑 MIXED-API」** 卡片。填地址 `http://192.168.1.20:8766/v1`（改成电脑真实 IP），**上游 Key** 填第 2 步的电脑客户端 Key，方式选 `bearer`，勾选启用、保存。
5. **手机：**刷新模型，应显示 `codex/gpt-...`、`workbuddy/...`、`aistudio/...` 等，不会多一个 `pc/`。在手机上的任何 OpenAI 兼容客户端填手机本地 `http://127.0.0.1:8765/v1`，Key 则复制**手机自己的客户端 Key**。

**严格区分三种地址/密钥：**电脑后台 `127.0.0.1:8765`（Admin Key）→ 电脑的 LAN API `电脑IPv4:8766/v1`（电脑客户端 Key）→ 手机本机 API `127.0.0.1:8765/v1`（手机客户端 Key）。

**安全限制：** LAN 端口没有 `/api/config` 或管理后台，不会把 Google Cookie 或登录 Token 作为配置返回；但是 LAN HTTP 本身不加密，只能在可信的家用/私有网络内使用。换 Wi-Fi 或电脑休眠就会断开。公网请使用 VPN，绝不要转发 8766。

## 六个来源逐一说明

### 1. Vertex2OpenAI（`vertex/`）

**官网**：<https://github.com/bad-woman/vertex2openai>；Google Express 申请入口：<https://console.cloud.google.com/expressmode>。

- 申请 Google Agent Platform/Vertex AI Express Mode Key，按官方页面启用项目权限和计费。**Express Key 是 Google 凭证**；不是中转站 Key。
- 安装运行原仓库（例如按其官方 Docker Compose，通常 API `http://127.0.0.1:8050/v1`，后台 `http://127.0.0.1:8050`）。
- 登录原项目后台，在「通道与凭证」填 Express Key；可选 Cookie + Project ID 模式需要自行提供 Google Cloud 有效会话，属于非公开接口，稳定性和账号风险不同。
- 回到 MIXED-API：URL 设 `http://127.0.0.1:8050/v1`，上游 Key 填 **Vertex2OpenAI 的 API_KEY**，Auth 选 `bearer`。

### 2. 独立 Codex CLI（`codex/`）—— 不用装 SillyTavern

**官方项目**：<https://github.com/openai/codex>；Node.js：<https://nodejs.org/>。

- 在 Windows 电脑安装 Node.js LTS，回到 MIXED-API 的 Codex 卡片，点「一键准备/安装」。等几分钟，点「检查安装状态」。等价于 `npm install -g @openai/codex@latest`。
- 点「打开账号授权窗口」（等价于 `codex login`），按官方流程登录 ChatGPT 账户。**需要你手动授权一次**；不会获取、展示、导出或保存 OAuth Token 到 MIXED-API 配置文件。
- 确认该卡片使用 `codex_cli` 内置模式。无需填写 URL 和上游 Key。启用并保存，刷新模型。MIXED-API 直接与官方 CLI 的本机 `app-server` 通信，不需任何酒馆服务器。
- 此适配器只接受 ChatGPT 订阅登录，不会自动使用 `OPENAI_API_KEY` 付费；在代码层禁止执行工具/MCP，防止任意客户端误触工作站命令。注意第三方应用使用订阅额度的授权限制；不承诺任何场景都没有风控风险。
- **Android APK** 不打包桌面 Codex CLI，暂时只能连接自行部署的 HTTP 兼容服务，不能把桌面 Codex 直接运行在 APK 内。

### 3. OpenCode Zen / Go (`opencode/`)

**上游项目**：<https://github.com/jasonxu114514/opencode2api>；OpenCode：<https://opencode.ai/>。

- 下载原项目 Windows Release 或按官方文档自行编译，复制 `config.example.json` 为 `config.json`。
- 在配置中设置真正的 `zen_keys` / `go_keys`，或者明确启用原项目支持的 `anonymous:true`（是否可用以原项目为准）。
- 一定要修改 `server_keys` 和 WebUI 管理密码。`zen_keys` ≠ `server_keys`。
- 用 `opencode2api.exe -config config.json` 启动。默认接口 `http://127.0.0.1:8080/v1`，管理页 `http://127.0.0.1:8081`。
- MIXED-API 上游 Key 填 `server_keys` 的一项，Auth 选 `bearer`；启用并保存。

### 4. WorkBuddy Proxy (`workbuddy/`)

**中转项目**：<https://github.com/sitingge/workboddy-proxy>；WorkBuddy 官方入口 <https://copilot.tencent.com/> / <https://www.workbuddy.ai/>。

- **先在 Windows 安装并登录 WorkBuddy/WorkBuddy AI 桌面客户端**。已经登录就不用重新登录。
- 电脑上有 Node.js LTS 和 Git 后，点击 MIXED-API WorkBuddy 卡片「一键准备环境」：在用户配置目录下载作者的**固定源码版本**；然后点「启动上游后端」。不用自己找 CLI 所在目录，原项目会自动识别。
- 原代理管理面板 `http://127.0.0.1:8790`，API URL `http://127.0.0.1:8790/v1`。看到账号和模型列表才算连接成功。
- WorkBuddy 代理默认本机无上游 Key：MIXED-API 选择 Auth `none`，留空上游 Key。如果你已在原代理设置了 Key，再用 `bearer` 并填写。
- 如果原桌面客户端没有登录，MIXED-API 不能替它绕过认证；请打开正版客户端登录。

### 5. APS2api (`aps/`)

**项目与下载**：<https://github.com/DeconstructedCube/APS2api>；[原作者的部署指南](https://github.com/DeconstructedCube/APS2api/blob/master/%E9%83%A8%E7%BD%B2%E6%8C%87%E5%8D%97.md)。

- 下载与你平台匹配的 Release，解压按原项目脚本启动，首次运行须阅读并同意其规则。
- 打开 `http://127.0.0.1:2156/admin/`，用其首次生成的管理员密码登录；到「密钥」页面创建 `sk-` 开头的**本地** API Key。
- MIXED-API 的 URL `http://127.0.0.1:2156/v1`、上游 Key 用刚生成的 `sk-`、Auth `bearer`。
- 该项目有**PolyForm Noncommercial** 许可，依赖的非公开/匿名 Google 端点也可能变化；MIXED-API 没有打包或修改它。

### 6. AIStudioToAPI (`aistudio/`)

**上游项目**：<https://github.com/iBUHub/AIStudioToAPI>；Google AI Studio：<https://aistudio.google.com/>。

- Windows 上安装 Node.js LTS 与 Git。点「一键准备环境」：MIXED-API 自动下载**固定**源码版本并执行 npm install。
- 点「打开账号授权窗口」：原项目执行 `npm run setup-auth`，准备 Camoufox 浏览器和 Google AI Studio 登录会话。**至少第一次需要自己完成 Google 登录**，或者已在上游相应目录内拥有有效 `configs/auth`。MIXED-API 不收集你的 Google 密码、Cookie、TOTP 码，也不尝试绕过 Google 身份认证。
- 点「启动上游后端」，等价于 `npm start`。后台和接口默认 `http://127.0.0.1:7860`、`http://127.0.0.1:7860/v1`。
- 一键准备环境时 MIXED-API 会强制写入 `HOST=127.0.0.1`，生成**随机** `API_KEYS` 并自动填到本地上游配置，以避免原项目对局域网公开已知默认密钥。**请安装完成后刷新管理后台**，确认 Key 字段保存成功，再启用。
- 原项目代码按 **CC BY-NC 4.0 非商业许可**分发，本工具不会合并其源码。**不想登录网页**则不要选 Build App 浏览器代理，可使用前面的 Vertex Express Key 途径；那是另一种授权渠道，不能混为一谈。

## 常见问题

**“刷新模型显示 0 个模型”**：来源是否启用并保存？原服务后台本身能打开吗？原项目是否已成功登录？MIXED-API 的 URL 应填到 `/v1`，不要写成 `/v1/models`；上游 Key 和 MIXED-API 的 Client Key 不一样。

**“127.0.0.1 连接不上另一台设备”**：这是预期行为。新版 Windows 的显式 LAN 启动脚本提供只带 API 的 8766 端口；从手机连接时使用电脑真实 IPv4，例如 `192.168.1.20:8766`，绝不能填 `127.0.0.1:8766`。

**“Codex 没显示模型”**：先在 Windows PowerShell 运行 `codex --version`、`codex login status`。升级 `npm install -g @openai/codex@latest`。如果 Codex 配置带有 MCP 或第三方 Base URL，本项目会拒绝，以免泄露凭证或在角色对话中运行工具。

**“我能一键免 Google/WorkBuddy 登录吗？”**：不能。MIXED-API 能做的是安装依赖、启动已有客户端/浏览器授权流程、复用由原上游安全保存的有效会话；真正账号认证无法也不应跳过。

**“Windows 和 Android 都能独立运行六套服务吗？”**：目前不行。**两端都能运行 MIXED-API 网关**；Windows 可自行启动三种助手（Codex、WorkBuddy、AIStudio）；Android 只能通过 HTTP 访问其运行环境能到达的上游，不能原生运行 WorkBuddy 桌面客户端或 Camoufox。

## 开发和安全

此项目采用“一个公共接口、不同内部适配器”的结构，并按 [mattpocock/skills](https://github.com/mattpocock/skills) 的小步开发、受控接口和测试审查思想组织。参考 `AGENTS.md`、`GLOSSARY.md`、`docs/adr/`、`go test -race ./...`。所有自动执行的源码安装均由用户在本机后台**明确点击触发**，并固定 Git commit；不会自动分享令牌或默认开启公网端口。

## Windows EXE 与 Android APK 的区别（请先看）

| | Windows EXE | Android APK |
| --- | --- | --- |
| MIXED-API 网关本身 | 单 EXE 直接运行，自动打开浏览器 | APP 内一键开启，轻量配置页面 |
| Codex CLI | 可独立调用已登录的 Codex CLI，不依赖酒馆 | 不含 Codex CLI，通常从电脑桥接 |
| WorkBuddy | 可自动下载安装并启动代理；需已登录官方客户端 | 不含桌面 WorkBuddy，通常从电脑桥接 |
| AIStudioToAPI | 可准备 Node/Camoufox 环境和辅助启动；首次登录需本人完成 | 不含桌面浏览器环境，通常从电脑桥接 |
| 其他三个 HTTP 反代 | 连接本机或网络中已启动的 HTTP API | 连接手机能访问的 HTTP API / 电脑桥接 |
| 后台界面 | 五栏：总览、上游、模型、指引、网络设置 | 精简：本机地址、密钥、上游、模型 |

**一键运行的确切含义：** 双击 `MIXED-API-Windows-x64.exe` 可以直接启动 *MIXED-API 网关*。上游平台是否可用仍取决于对应服务安装、账号授权及可用额度。Windows EXE 不是将六套第三方软件全部静态打包进去。
