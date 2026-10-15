# MIXED-API · 统一的本地 OpenAI 兼容中转站

[![CI](https://github.com/NLKASHEI/MIXED-API/actions/workflows/release.yml/badge.svg)](https://github.com/NLKASHEI/MIXED-API/actions/workflows/release.yml)

**六个可配置上游 → 一个 API Base URL + 一个客户端 Key → 自动按模型 ID 分流。**

本项目只实现一个本地网关，**不复制、捆绑、不尝试绕过上游认证**。上游授权、额度、收费和软件许可证继续生效。

## 一键使用与保姆级反代指引（最新）

**首次使用请先看 [零基础使用指南](docs/quickstart-zh.md)**。管理后台的每张上游卡片也内置逐步教程和官方网址，解释每个 Key 的来源。

- **Codex：已直连 CLI，不需要 SillyTavern**。Windows 点击「一键安装 Codex」→「打开账号授权窗口」→ 启用并刷新模型。第一次需本人通过 ChatGPT 正式授权；MIXED-API 不保存 OAuth Token。仅使用 ChatGPT 订阅账号，拒绝 API Key 登录和任何 MCP/工具。
- **WorkBuddy：** Windows 点击「一键准备环境」→「启动后端」。程序从可信仓库固定提交下载中转并由上游自动检测已登录的正版 WorkBuddy 客户端；第一次仍需本人在客户端登录。
- **AI Studio Build：** Windows 点击「一键准备环境」→「打开账号授权窗口」→「启动后端」。自动安装 Node 依赖并设置本地随机上游密钥/回环监听；原项目 Camoufox 登录需要本人完成一次。无需浏览器认证时请考虑官方 Express Key 模式而不是 Build App 浏览器代理。
- **其余三项：** 在控制台按每个来源的「从零开始配置向导」安装对应项目，填写它们各自的服务器 Key 和 API 地址。

> **平台差异：** Windows 版拥有本机 Codex 适配器和部分自动安装功能；Android APK 提供统一 HTTP 网关与教程，但不能在 APK 内运行桌面版 Codex、WorkBuddy、Camoufox。所有来源依赖各自权限/额度，未经真实账号登录不保证端到端成功。


## 快速开始

### Windows（x64）

1. 在 [GitHub Releases](https://github.com/NLKASHEI/MIXED-API/releases) 下载 `MIXED-API-Windows-x64.zip` 并解压。
2. 双击 `Start-MIXED-API.cmd`。首次运行自动生成独立的 **API Key** 和 **Admin Key**，并保存到当前目录的 `mixed-api.json`。
3. 打开浏览器 `http://127.0.0.1:8765`，输入控制台显示的 **Admin Key**。
4. 先根据管理面板指引安装/启动相应上游，再填入上游 `Base URL`、该上游自己的 Key，开启并保存。
5. 客户端只需填 `http://127.0.0.1:8765/v1` 和 **MIXED-API API Key**，然后选择 `上游ID/原模型名`。

提示：Windows 版为不需要额外安装 Go/Node 的单文件 `MIXED-API.exe`，配一个启动脚本。**一键启动的是聚合网关本身**。需要客户端登录的上游仍需预先配置；可以在管理界面填写 Windows 本机启动命令，由网关启动已安装的后端。

### Android（独立 APK）

1. 下载 Release 附件 `MIXED-API-Android.apk`，安装 Android 8.0+ 设备。
2. 点击「启动本地代理」，再点「打开管理后台」。
3. 在 APP 内复制 **Admin Key** 登录管理界面；根据说明启用已部署的上游。
4. 手机端任何本机客户端使用 `http://127.0.0.1:8765/v1` 和 APP 内复制的 **客户端 API Key**。

**重要限制：** Android APK 是网络中转器，不会在手机里自动安装或运行 WorkBuddy、Camoufox、Codex CLI，也不包含 Python/Go 的六套原项目。手机 `127.0.0.1` 是**手机自己**，不是你的电脑。如果上游运行在电脑上，要确保其监听地址允许 LAN 访问、配置鉴权及防火墙，并在 APP 里填电脑的局域网地址；一些上游只允许回环访问，不能直接跨设备连接。不要直接公开上游 API 或转发端口。Android 前台服务也可能受到系统电池策略限制。

## 聚合规则

| 上游 ID | 上游项目 | 默认 API URL | 需要先做什么 |
|---|---|---|---|
| `vertex` | [vertex2openai](https://github.com/bad-woman/vertex2openai) | `http://127.0.0.1:8050/v1` | Express Key 或 Cookie/Project ID；启动原版服务 |
| `codex` | [Codex CLI（MIXED-API 直连 app-server，不需要 SillyTavern）](https://github.com/LukaTheHero/Codex CLI（MIXED-API 直连 app-server，不需要 SillyTavern）) | `http://127.0.0.1:8901/codex/v1` | 在运行 SillyTavern 的电脑安装服务端插件、Codex CLI，并执行 `codex login` |
| `opencode` | [opencode2api](https://github.com/jasonxu114514/opencode2api) | `http://127.0.0.1:8080/v1` | 设置 upstream/本地 server key，启动 Go 版服务 |
| `workbuddy` | [workboddy-proxy](https://github.com/sitingge/workboddy-proxy) | `http://127.0.0.1:8790/v1` | 安装并登录桌面 WorkBuddy，运行 `node wb-relay.js` |
| `aps` | [APS2api](https://github.com/DeconstructedCube/APS2api) | `http://127.0.0.1:2156/v1` | 启动程序并在 `/admin/` 建立本地 API Key |
| `aistudio` | [AIStudioToAPI](https://github.com/iBUHub/AIStudioToAPI) | `http://127.0.0.1:7860/v1` | 在电脑执行 `npm run setup-auth` 完成 Camoufox 登录，再运行 `npm start` |

各上游默认 **禁用**。成功启用并连接后，`GET /v1/models` 汇总其返回的模型，并把模型 ID 加上前缀：`vertex/gemini-...`、`codex/gpt-...`、`aistudio/gemini-...` 等。只凭前缀选择上游，不会跨供应商自动切换，不会进行额度合并和负载均衡。

支持：`GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/embeddings`、`POST /v1/messages`、`POST /v1/messages/count_tokens`。最后几个接口只在**上游本来支持相应路径**时可用，不会自动转换协议。保留普通和 SSE 流式数据，普通请求会把模型前缀从请求体移除后交给上游。上游响应中模型字段仍可能是未加前缀的原始 ID。

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

Android project under `android/` (Gradle + Android Java, without Python runtime). CI builds the Windows x64 `.exe` and the Android Debug-signed `.apk` and publishes each successful build as a GitHub **prerelease**; debug signature is for self-use and testing, not Play Store deployment. Device installation/network behavior remains to be tested.

## 安全和许可

- 默认只绑定 `127.0.0.1:8765`。为避免意外暴露管理后台，两个平台均拒绝在公网/局域网监听。只管理你本机安装的上游；跨设备访问请使用明确受控的端口转发或由客户端连独立安全网关。
- 本地随机生成两套密钥：**Client API Key** 只能用 API，**Admin Key** 才可查看和更改包含上游密钥的配置。Desktop 密钥文件权限设为 `0600`；Android 通过私有 SharedPreferences 保存，**没有加密备份/硬件密钥库**。
- 不要把 Google Cookie、OpenAI 授权 token、WorkBuddy 会话或本地配置文件提交到 GitHub，也不要将这些信息发给不受信任的人。
- 订阅额度不等同于可供任意软件使用的通用 API。请遵守各自的许可证、服务条款、额度以及授权用途。本项目不保证使用第三方反向代理不触发服务商限制。
- 本仓库只发行**自行编写的网关**，使用上游的公开 HTTP 接口；**不会把原项目源码重新打包分发**。特别注意 OpenCode 的 GPL-3、Codex CLI（MIXED-API 直连 app-server，不需要 SillyTavern） 的 AGPL-3、APS2api 的 PolyForm Noncommercial 和 AIStudioToAPI 的 CC BY-NC 4.0，均有各自分发或使用条件。

开发约定见 [`AGENTS.md`](AGENTS.md)、[`GLOSSARY.md`](GLOSSARY.md)、[`docs/adr/0001-gateway-adapters.md`](docs/adr/0001-gateway-adapters.md)，以模块边界、TDD 和安全审查为中心，参考 [mattpocock/skills](https://github.com/mattpocock/skills) 的工程实践，**没有直接复制其 skills 内容**。
