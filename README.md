# MIXED-API

一个统一连接地址，把你有权使用的模型接进同一个工作台。

新版界面采用浅色、大字号和分步配置，包含工作台、模型库、诊断、独立后台管理和更新入口。后台直接访问本机 `/admin`，需要独立的 Admin Key；从 EXE 启动会自动连接本机，无需手工登录。

## 下载与安装

前往 [最新 Release](https://github.com/NLKASHEI/MIXED-API/releases/latest)：

- **Windows 安装版**：下载 `MIXED-API-Windows-x64-Setup.exe`，双击安装。
- **Windows 便携版**：下载 ZIP 并解压，双击 `Start-MIXED-API.cmd`；也可直接运行独立 EXE。
- **Android**：下载 `MIXED-API-Android.apk`。APP 会启动本地服务并打开同一份响应式工作台。

Windows 默认打开新版浏览器工作台，旧 Win32 窗口已移除。运行网关无需安装 Go、Node 或 Python；只有启用依赖相应运行时的来源时，软件才按需准备组件。

**后续重构要求，尚未实现**：Windows 改为应用自身的独立窗口，保持简约大字号界面；协议与反代核心融入应用，必要运行资源随包交付，取消首次使用时下载上游程序或安装开发环境。v0.3.1 仍采用上述浏览器和外部组件方案，不能视作已完成内置融合。验收标准见[一体化桌面应用规格](docs/spec-integrated-desktop.md)。

喜欢命令行的用户可从 Release 下载 `Install-MIXED-API.ps1` 后运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\Install-MIXED-API.ps1
```

默认安装到 `%LOCALAPPDATA%\MIXED-API`。可加 `-Destination 'D:\Apps\MIXED-API'` 指定位置。脚本核验官方 ZIP 的 SHA-256，升级前备份已有 `mixed-api.json`；不会结束其他进程。目标目录的软件仍在运行时，会提示先关闭再重试。

## 只需要给客户端填三项

| 项目 | 填什么 |
| --- | --- |
| 统一连接地址 / Base URL | `http://127.0.0.1:8765/v1`（如自定义监听地址，以工作台显示为准） |
| API Key | 工作台「复制 Key」取得的客户端 Key |
| 模型 | 模型页复制的完整 ID，或自己设置的模型别名 |

六路模型都使用同一个客户端 URL。内部组件的端口和通信 Key 由软件处理，不需要分别填到客户端。客户端 Key 不能进入后台管理。

## 接入来源：界面逐步告诉你怎么做

| 界面名称 | 你需要准备什么 | 内部来源 |
| --- | --- | --- |
| **快速V** | 自己的 Google **Express API Key**，第 2 步提供获取入口和输入框 | [vertex2openai](https://github.com/bad-woman/vertex2openai) |
| **无头V** | 按第 2 步在组件窗口阅读并确认使用规则；不填 Express Key | [APS2api](https://github.com/DeconstructedCube/APS2api) 及同源本地组件 |
| Codex | 本人的 ChatGPT 账号授权，无需 SillyTavern | [Codex CLI](https://github.com/openai/codex) |
| OpenCode | 可选自己的 Zen Key；匿名模式能否使用取决于上游 | [opencode2api](https://github.com/jasonxu114514/opencode2api) |
| WorkBuddy | 已安装并登录的官方桌面客户端 | [workboddy-proxy](https://github.com/sitingge/workboddy-proxy) |
| AI Studio | 本人在 Camoufox 窗口完成 Google 登录 | [AIStudioToAPI](https://github.com/iBUHub/AIStudioToAPI) |

每个来源都是三个实际步骤：

1. **软件准备**：只准备运行组件与本地配置，不把安装完成当成已授权。
2. **完成授权**：当前页面告诉你要做什么、在哪里获取 Key、在哪里登录；本人确认后才继续。
3. **验证并使用**：检查模型，复制唯一客户端地址、客户端 Key 和模型 ID，最后发送一次真实消息。

模型目录不等同于推理权限。失败时显示原因，可以返回上一步修改；不会悄悄切换到另一个来源。

## 融合在 MIXED 中的能力

- 一个工作台管理账号接入、运行组件、模型和排错。
- 并发检查各来源，稳定输出并去重模型；一个慢来源不会串行拖住所有探测。
- 模型别名由 MIXED 路由层实现，例如把 `daily-chat` 固定指向 `vertex/实际模型ID`；来源不可用时明确报错，不跨账号替换。
- 后台统一管理启动、停止和连接配置。无头V的超时、重试、回复显示方式都有用途说明，修改前保存配置备份。
- 本地同源组件作为无头V内部实现接入，不新增另一个来源，也不让普通用户选择程序版本。

[上游核对记录](docs/upstream-audit-2026-10-16.json) 和 [集成决策](docs/adr/0002-unified-console-and-model-aliases.md) 解释版本与模块边界。内部组件仍保留各自授权、服务限制和许可证；不能把安装成功宣传为所有账号均已可用。

## 手机使用

手机可直接接入可达的 HTTP 服务；复用电脑上的来源更方便：

1. 电脑用 `Start-MIXED-API-LAN.cmd` 启动，先完成所需来源接入。
2. 手机和电脑连同一个可信 Wi-Fi，在手机「电脑 MIXED-API」的逐步指引里填写电脑实际 IPv4 的 8766 地址，以及电脑的客户端 Key。
3. 手机 AI 客户端仍填写手机工作台显示的统一 URL 与手机客户端 Key。

8766 仅共享推理 API，不提供管理页面、安装和配置接口。不要把这个端口映射到公网。手机 APK 不会在 Android 内运行桌面 Codex、WorkBuddy 或 Camoufox。无头V自身的内部服务可能监听局域网，只在可信网络使用。

## 更新、备份与已知限制

- 「设置 → 检查更新」从本仓库正式 Release 获取版本。Windows 下载并校验 Setup.exe 后打开安装向导；Android 点 Release 后由系统确认安装。
- 所有发布包附 `SHA256SUMS`。Windows EXE 与安装包尚未代码签名。
- 备份 `mixed-api.json` 和 `%APPDATA%\MIXED-API` 可保留网关设置及受管组件数据。它们可能包含私密凭证，请勿上传。
- Android APK 当前为 Debug 签名；只有持续使用同一签名密钥才能保证覆盖升级。维护者设置 `MIXED_API_ANDROID_DEBUG_KEYSTORE_B64` 后可持久签名，详情见完整指引。
- 六路真实个人账号推理、Android 真机安装/网络/后台保活尚未全面验证。CI 构建与浏览器测试不能替代这些验证。

## 开发与构建

Go 1.23+；Android 使用 JDK 17 / SDK 35；Node 22+ 仅用于开发检查，不是网页运行依赖。

```text
go test ./...
go vet ./...
go test -race ./...          # CI 在 Linux 上执行
npm ci
npm run check:ui
npx playwright install chromium
go build -o .ui-gateway ./cmd/mixed-api
```

设置 `MIXED_TEST_BINARY` 指向刚构建的程序后，执行 `npm run test:ui`；它使用临时配置与本地测试服务，不登录个人账号。`go run ./cmd/mixed-api --no-browser` 提供无界面运行。新版三份 HTML 和上游清单必须同步，CI 会检查。

`python scripts/audit_upstreams.py --output audit.json` 只读核对六个原仓库，不自动改变固定版本。维护导入已有同源 Windows 包可用 `MIXED-API.exe --config <配置路径> --import-engine <包目录>`；此维护入口只准备隔离副本、不启动，不导入用户会话或规则同意状态。普通用户使用自动准备流程，不需要处理组件路径。

工程约定见 [AGENTS.md](AGENTS.md)。采用 [mattpocock/skills](https://github.com/mattpocock/skills) 的 TDD、模块设计和规范/需求双轴审查；发布仅从通过验证的 GitHub Actions 产物生成。
