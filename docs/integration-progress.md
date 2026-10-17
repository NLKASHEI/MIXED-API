# 内置重构进度

开发分支 `feat/integrated-desktop-core`，2026-10-17。v0.4.0 发布 Windows 内置核心版与 Android 测试版，实际资产和对应构建见 [Release](https://github.com/NLKASHEI/MIXED-API/releases/tag/v0.4.0)。以下分别记录已验证范围与未完成事项。

六个来源已有 Go / Java 协议模块，默认关闭。v0.4.0 的 AIS / 无头V官方页面初始化存在缺陷，用户已报告 Windows 实际不可用，不能称为六路完整可用交付。修复与验收范围见 [Windows 会话修复](windows-session-repair-2026-10-17.md)。旧上游下载器、外部 EXE 导入与进程启动器已删除。用户配置账号或 Key，不选择上游运行版本；客户端共用 `http://127.0.0.1:8765/v1`。

| 来源 | Windows / Android 当前实现 | 验证范围 |
| --- | --- | --- |
| 快速V | Express Key、Gemini、工具、思考、图片/音频/PDF、语音生成 | 公共 HTTP 模拟上游回归 |
| 无头V | 官方页面会话、GraphQL、Gemini、超时与生成前限流重试 | 公共 HTTP、截断、取消和会话生命周期回归 |
| AIS Build | 官方会话、Build RPC、独立目录、Gemini 与媒体转换 | 公共 HTTP 模拟 RPC；未知/订阅模型使用 unary，目录确认的免费模型可增量 |
| AIS Playground | 官方会话、JSPB、独立目录、工具选择约束、媒体与语音 | 公共 HTTP、工具完整/流式约束、RPC 尾部校验 |
| OpenCode | 按模型目录分派 Chat / Responses / Messages / Gemini；不运行 Node 上游 | 三客户端协议、工具往返、用量、截断与取消回归 |
| Codex | 设备码、PKCE、本人授权文件导入、Responses、过期/401 刷新 | 模拟授权、刷新合并、轮换保存、注销竞态、账号身份保护 |
| WorkBuddy | 原生授权轮询、账号信息、HTTP/SSE、自动刷新 | 模拟授权与生成、刷新和网络取消回归 |

三个客户端入口 `/chat/completions`、`/responses`、`/messages` 保持选定来源与模型；来源原生支持时直接使用原生协议，否则使用公共转换模块。无法等价转换的状态、缓存或媒体选项明确报错。详见[协议能力与测试](protocol-parity.md)。

Windows 主窗口使用捆绑 WebView2；完整构建为单个 GUI EXE，首次本地展开并校验固定渲染资源。已有本机窗口、后台、资源展开与窗口生命周期证据；干净 Windows 离线安装、便携启动、完整进程/网络验收尚未完成。

Android 为纯 Java 本地核心，共享管理 HTML 与配置。电脑共享只是可选功能，模型使用 `pc/电脑模型ID`。API 35 系统栏/刘海/键盘适配、前台服务停止通知、账号 Keystore、备份排除和文件选择已实现；系统 WebView 必须支持隔离 profile，配置步骤会报告能力缺失。JVM 模拟平台测试不等于系统 WebView、Keystore 或实体手机验收。

`100ef39` 的 [Actions 39020922946](https://github.com/NLKASHEI/MIXED-API/actions/runs/39020922946) 已通过 Go race/tests/vet、Windows 构建、UI 回归、Android 55 项测试与构建/lint，以及 Android 15 干净模拟器的安装、工作台、后台鉴权、通知停止、重启、Keystore 加密写入/读取和账号注销。模拟器没有登录真实账号，不能代替实体手机验收。

后续 Android 打包已改为不可调试的 Release 变体，增加实际 APK 证书校验和覆盖升级测试。`ad97606` 的 [Actions 39022950817](https://github.com/NLKASHEI/MIXED-API/actions/runs/39022950817) 已通过全部检查，包含 55 项 Android 测试、lintRelease（0 错误，5 警告）和模拟器覆盖升级验收。v0.4.0 的资产另由其 tag 工作流重新构建与验收，不使用旧版本产物改名。固定正式密钥尚未配置，本次 APK 单独标为临时签名测试版；用户要求先发布 Windows，再补 APK。见[签名配置](android-signing.md)。

用户明确暂不登录，实际本人账号生成继续保留为未验证。后续仍需完成兼容性收尾、干净系统/设备证据与最终资产核验；不关闭 [Issue #3](https://github.com/NLKASHEI/MIXED-API/issues/3)，不将测试夹具当成全部上游能力已交付。

来源、许可与采用范围见[上游核对](upstream-audit-2026-10-17.md)、[Google 协议来源](third-party/google-web.md)；实质缺陷和无需整改的提示分别记录在[审查记录](review-integrated-core.md)。
