# 新增上游核对

本日后续 Windows 修复快照：AIStudio2API `2cbe310ce6ebab093bf83e5078de8296f03770a9`、OpenCode `27626a9d192b988938bb3d5f0e4fa241af7ae309`、APS2api `01328896c435b4b172b1d25f9fc04ff86b40df31`、DeconstructedCube AIS `9bd5a20f25bbfae60c84b59b6a9f167b85c7d1a3`。采用范围、源码文件校验与限制见 [Google 协议来源](third-party/google-web.md) 和 [Windows 修复](windows-session-repair-2026-10-17.md)。下文保留此前各次快照，版本号不表示当前最新版。

补充核对：OpenCode 当前 [v63 提交 8895290](https://github.com/zkygwxf-bot/opencode-proxy/commit/889529028cabe8b8a463ee17c24fd56bbce20ce1)；AIStudio2API 当前 [54faed2](https://github.com/Mag1cFall/AIStudio2API/commit/54faed274d328c377272d6085d35133742d94085)。前者的工具流整理、反向协议转换及取消清理作为本轮测试依据；重新实现于 Go/Java 共享核心，不采用伪装工具或客户端身份的授权门槛处理，也不把孤儿参数丢弃后标成成功。新增搜索循环仍需独立能力和安全审查。后者新增代理池、Build 参数错误回退与 worker 修复，核心 JSPB 字段未变；不采用自动 Build → Playground 回退，继续保留明确能力路由。WorkBuddy `4354f83`、DeconstructedCube AIS `6d1f243` 在此次复查中未变。

核对时间：2026-10-17。原六个项目的历史快照见 `upstream-audit-2026-10-16.json`。下列项目融入原有来源，不增加第七个来源，也不增加用户运行版本选择。

## OpenCode

仓库：[zkygwxf-bot/opencode-proxy](https://github.com/zkygwxf-bot/opencode-proxy)。MIT，原许可保留在 `docs/third-party/opencode-proxy-MIT.txt`。

| 核对对象 | 固定版本 | 校验 |
| --- | --- | --- |
| v58 正式版 | `b517138c5e91f947263b73de81ffce6031673ccb` | 发布 ZIP SHA-256：`e4b9192b5dbc1ec4004b7b55e27a8409e65dbbc210011d514520aa876ad7ea9f` |
| v59 测试版 | `123c689ae0a8673e964522eb40f7f4b573901805` | 发布 ZIP SHA-256：`a2c18d4b74b1b6626357fff4bf370dbcc3b46af375a3ca59ec00d3792f09853c` |
| 当前主分支 | `9c845e3b633e4f5d67e05ce1b0dae3d02ca96c13` | `proxy.mjs` SHA-256：`dd734ec9ac2bfc9189a0254afa433bcb937472780fc22d9a269311347ceeb05b` |

两个 ZIP 的摘要均与 GitHub Release asset digest 一致。v59 的 `worker.js`、`package.json` 与 v58 一致；`proxy.mjs` 的差异是两处默认客户端版本标识。当前主分支的代理文件也仅有这两处差异。README 已列 v59 为测试版，因此不把测试标签自动等同于更稳定的协议实现。

纳入的能力：原生协议目录识别、Chat→Responses、Chat→Anthropic、文本/图片输入、工具调用及结果、思考内容、usage、完整及流式输出。共享转换服务于应用的 Go HTTP 核心，不执行该仓库的 Node 脚本或手机启动器。

联网核对发现，[官方推理 API](https://opencode.ai/v2/docs/console/api/inference/) 已按协议迁移到 `/inference/openai/v1`、`/inference/anthropic/v1` 和 `/inference/google/v1beta`；模型目录为 `/inference/v1/models`。原 v58/v59 使用的 `/zen/v1` 路径不再作为内置核心的固定推理地址。

随包纳入 [anomalyco/models.dev](https://github.com/anomalyco/models.dev) 提交 `f4088b386739663acaf3a88c916d7dfe9ec5efd4` 的 128 个 OpenCode 模型协议/端点映射，MIT 许可原文另存。不随包复制整个数据库，也不据此声称账号有这些模型的权限。已知模型直接使用经审查的内置映射；未知模型允许查询官方元数据，仍不猜测协议。元数据不能把用户凭证导向任意 URL，端点必须匹配核心的官方路径白名单。Go 的公共 Gemini 转换模块也复用于 OpenCode 的 Google 模型。

此次本机无凭证 HTTP 探测收到 Cloudflare 403，未把它写成推理成功；网页工具可以读取官方模型目录。个人账号和请求出口的实际可用性仍需另行验证。

请求头使用 MIXED 自身身份和用户可选 Zen Key；没有导入发行包里的 `session.json`，不注入虚构的 CLI 工具或客户端指纹。原项目的本地搜索/网页抓取工具及其他反向跨协议转换尚未纳入。服务方拒绝某类客户端或额度不足时，返回实际状态和经脱敏的错误，不切换来源。

验证在 `/v1/chat/completions` 公共接口完成：协议目录路由、Chat→Responses、并行工具、Chat→Anthropic 完整/流式工具与思考、授权错误隔离、429/Retry-After、不泄露供应商凭证、客户端取消时真实 HTTP 上游请求结束。尚未使用个人 OpenCode 凭证做真实推理。

## AIS：Build / Playground

- [Mag1cFall/AIStudio2API](https://github.com/Mag1cFall/AIStudio2API)，固定提交 `ebb27d109dfa8bcea96d481846f3f0a7536d89f0`，MIT。分别核对 Build 的 `ProxyUnaryCall/ProxyStreamedCall` 和 Playground 的 `GenerateContent`。其纯 Go WAA 后端与浏览器后端是内部实现选择，不应形成用户运行版本菜单。
- [DeconstructedCube/aistudio-api](https://github.com/DeconstructedCube/aistudio-api)，固定提交 `6d1f243b811834a88fecac5615e6a161ea112efb`。README 声明 MIT，但此次快照未找到独立许可证文件，尚未复用其源代码。实际仍依赖 CloakBrowser；导入 Cookie 不等于无浏览器运行资源。它属于 Playground 的另一种实现，不是额外额度来源。
- 原 [iBUHub/AIStudioToAPI](https://github.com/iBUHub/AIStudioToAPI) Build 实现继续作为协议和能力核对材料，固定提交及 CC-BY-NC-4.0 记录保留在历史清单。

AIS 的真实账号、官方验证、受保护 RPC 和随包资源仍未闭合。当前未将其标为内置可用，也不自动下载这些上游程序。不能用一个未验证的 Cookie 输入框代替完整集成。

## WorkBuddy 与无头V

WorkBuddy 的直接 HTTP 参考改为 [hawklithm/workbuddy2api](https://github.com/hawklithm/workbuddy2api)，固定提交 `4354f83bd95d6995ef944ef342f95668ac1290b8`，MIT。保留原 Node relay 的历史核对记录；新的普通请求不依赖 relay 或官方 CLI。

无头V保持 APS 一个来源。本地 modified EXE 的来源、哈希和不可复现边界未改变；不把本地二进制当成源码集成。不移植自动验证码处理以冒充本人完成验证，原生请求链仍待完成。
