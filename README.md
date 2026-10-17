# MIXED-API

一个客户端地址，管理快速V、无头V、Codex、OpenCode、WorkBuddy 和 AIS。

**当前是内置核心开发分支。正式 Release 仍为 v0.3.1，以下新架构尚未完成全部验收。** 构建结果见 [Actions](https://github.com/NLKASHEI/MIXED-API/actions)，能力与限制见[集成进度](docs/integration-progress.md)。旧版使用方式见 [v0.3.1 文档](https://github.com/NLKASHEI/MIXED-API/blob/v0.3.1/README.md)。

## 启动与连接

Windows 构建产物提供独立 `MIXED-API-Windows-x64.exe`、便携 ZIP 和 Setup 安装包。直接运行 EXE，或通过安装后的快捷方式启动。主界面是应用自己的窗口，包含大字号工作台、模型、诊断、后台管理和分步配置。

完整 Windows EXE 包含固定版本的 WebView2 运行资源，首次启动在本地展开并校验，不要求另行下载渲染器、上游程序、Go、Node 或 Python。浏览器运行资源使 EXE 体积较大；网站授权与模型请求仍需要网络。普通启动不使用 CMD 启动器。

| 客户端项目 | 填写内容 |
| --- | --- |
| Base URL | 工作台显示的统一地址，默认 `http://127.0.0.1:8765/v1` |
| API Key | 工作台「复制 Key」取得的客户端密钥 |
| 模型 | 模型页复制的完整 ID，或明确绑定目标的自定义别名 |

客户端 Key 不能进入后台；`/admin` 和 `/api/*` 使用独立 Admin Key，只在本机管理监听开放。

## 来源配置

六个来源默认关闭。点击「接入」后，按当前步骤准备核心、完成本人授权、检查模型；说明、输入框与获取入口放在对应步骤里。

| 名称 | Windows 内置接入方式 | 模型命名 |
| --- | --- | --- |
| 快速V | 填写自己的 Google Express API Key | `vertex/模型ID` |
| 无头V | 应用内的官方页面会话，不填 Express Key；首次按提示确认规则并发送测试消息 | `aps/模型ID` |
| Codex | 官方设备码授权或本人的授权文件导入 | `codex/模型ID` |
| OpenCode | 可选 Zen Key，匿名模式是否可用由服务方决定 | `opencode/模型ID` |
| WorkBuddy | 官方账号授权；原生轮询与请求，不启动桌面客户端 | `workbuddy/模型ID` |
| AIS | 隔离的官方页面完成本人授权与测试消息，Build、Playground 分别读取模型目录 | `aistudio/build/模型ID`、`aistudio/playground/模型ID` |

这是当前代码的接入方式，实际账号验证范围见[集成进度](docs/integration-progress.md)。模型目录能读取不代表当前账号一定能生成。来源失败会直接报错，不自动切换供应商或账号。

后台可以启停内置来源、查看诊断、配置无头V的等待时限、限流重试与回复方式。仅对生成开始前的 429/503 按设置重试并尊重等待时间，不重放中断的生成。旧的外部程序下载、运行时安装、EXE 导入和命令启动入口已移除。

## Android

APK 使用纯 Java 服务与同一份管理页面。AIS/无头V已有 Java 协议模块，使用支持独立 profile 的系统 WebView；设备能力不足时配置页会提示。其他四路目前仍需明确配置电脑或 HTTP 桥接，不能将本轮 APK 称为六核心全部内置。CI 通过不等于真机通过。

连接电脑时，在电脑打开安装后的「局域网模式」快捷方式，或给 EXE 增加 `--lan` 参数。手机「电脑 MIXED-API」中填写电脑的 `http://实际IPv4:8766/v1` 与电脑客户端 Key；手机 AI 客户端仍使用手机工作台的唯一地址。

电脑模型在手机端明确显示为 `pc/电脑模型ID`。指定来源未启用时不会转给电脑。8766 只提供推理 API，不开放后台。

## 更新与数据

正式版从 [Release](https://github.com/NLKASHEI/MIXED-API/releases) 获取，发布资产必须来自通过检查的 Actions。`Install-MIXED-API.ps1` 只安装正式版，支持 `-Destination 'D:\Apps\MIXED-API'`；不会安装本分支的开发产物。

配置在 `mixed-api.json`，原生账号数据在同目录 `.mixed-accounts`；Windows 使用 DPAPI 保护账号令牌。官方页面会话在 `%LOCALAPPDATA%\MIXED-API\provider-*`，主窗口数据在 `desktop-profile`，可重新展开的渲染资源在 `renderer`。备份配置与私密会话时自行保管，不能提交或上传到仓库。

Windows 产物尚无代码签名。Android 当前使用 Debug 签名；只有 CI 配置持久签名密钥后才能保证覆盖升级。详细说明见[使用指引](docs/quickstart-zh.md)。

## 开发验证

Go 1.23+，Android 使用 JDK 17 / SDK 35 / Gradle 8.13，Node 22+ 只用于开发检查。

```text
go test ./...
go vet ./...
npm ci
npm run check:ui
node scripts/test-google-session.mjs
npx playwright install chromium
go build -o .ui-gateway ./cmd/mixed-api
```

设置 `MIXED_TEST_BINARY` 指向刚构建的程序后运行 `npm run test:ui`。CI 在 Linux 执行 Go 竞态检测，并执行 Android assemble、单元测试与 Lint。

Windows 完整离线产物先运行 `scripts/prepare-webview2.ps1` 获取并核验构建资源，再用 `go build -tags bundled -ldflags "-H=windowsgui" ./cmd/mixed-api` 构建。未带 bundled 的开发构建依赖已安装的系统渲染器，不是完整交付包。

工程按 [AGENTS.md](AGENTS.md) 与 [mattpocock/skills](https://github.com/mattpocock/skills) 的 TDD、深模块接口、规范/规格双轴审查开展。[上游核对](docs/upstream-audit-2026-10-17.md)、[第三方许可](THIRD_PARTY_NOTICES.md)、[验收规格](docs/spec-integrated-desktop.md)分别记录来源、许可和发布条件。
