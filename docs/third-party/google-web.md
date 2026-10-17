# Google 网页协议来源

2026-10-17，开发分支记录。源代码集成与个人账号实际推理分开验收。

| 来源 | 固定版本 | 采用范围 |
| --- | --- | --- |
| Mag1cFall/AIStudio2API | 2cbe310ce6ebab093bf83e5078de8296f03770a9 | MIT；MakerSuite JSON+protobuf 字段、Build 代理封装、WAA 函数发现、语音字段与显式工具调用约束。Windows 同时采用 `sendBuild` 的单次调用兼容分支和 `DecodeRPCError` 的状态数组解析。完整许可见 AIStudio2API-MIT.txt。 |
| DeconstructedCube/aistudio-api | 9bd5a20f25bbfae60c84b59b6a9f167b85c7d1a3 | 只核对协议与依赖说明；没有可核验的 LICENSE 文件，因此不复制其实现。 |
| DeconstructedCube/APS2api | 01328896c435b4b172b1d25f9fc04ff86b40df31 | PolyForm Noncommercial；`internal/transform/schema.go`、`request_config.go` 作为匿名 GraphQL 参数格式的核对输入。未复制其实现；MIXED Schema 转换器明确拒绝无法保留的约束，不采用静默删除、猜测类型或钳制参数。请求模板来自用户当前官方页面。 |

相关模块：`internal/googleweb/`、`internal/gateway/google_*.go`、`internal/desktop/system_browser_windows.go` 与 `android/app/src/main/java/com/mixedapi/local/Google*.java`。

Windows 的 AIS 与无头V使用系统默认 Edge / Chrome 的 MIXED 专用 profile（ADR-0006）；Android 使用 AndroidX WebKit 1.12.1 的系统 WebView profile。网页只负责其自身官方会话的授权、验证及请求传输；Go/Java 负责公开 API、协议转换、错误处理、取消与背压。应用不下载或执行上游项目，也不使用本地未知修改版 EXE。官方网站正常加载的页面资源仍需网络。

测试：公开 HTTP 完整/流式请求、RPC 尾部错误、GraphQL 截断、输出上限、客户端取消，以及浏览器会话排队和窗口启动取消。Windows 已实测独立来源窗口进入官方登录页；个人账号推理、干净系统与 Android 真机仍需分别验证。此记录不能作为“所有上游功能已完整交付”的结论。

本次补齐官方页面初始化、默认浏览器传输和连接/生成分开检查；取消后迟到的页面请求仍被拦截。没有将 DeconstructedCube 未提供可核验许可的实现直接复制，也没有集成 APS 的认证模拟器或 OpenCode 的客户端身份伪装。源代码参考和运行可用性不能混为一谈。

DeconstructedCube/aistudio-api 当前 master 的 README 明确要求安装受控浏览器，使用 Python CDP，并非不依赖浏览器的实现。README 虽链接 MIT，当前根目录没有 LICENSE；此版本仍仅作依赖及协议核对。

Windows 本轮接入的上游 Build 兼容规则：目录确认的免费模型单次调用收到 405/501、RPC code 12 或明确说明不支持 unary/stream 的 400 时，改用同一 Build 模型的流式 RPC，再聚合为普通响应。补充有界错误读取、禁止已生成内容后重发、授权及额度错误不进入兼容重试；不改走 Playground。对应 `google_build.go` 与公开 HTTP 的 `TestBuildUsesUpstreamUnaryCompatibilityWithinSameChannel`。

APS 参数转换对应 `google_aps.go`：大写类型、properties 的 key/value 数组、字符串长度约束及 responseSchema；外层 GraphQL errors 不能被当作成功完成。测试见 `google_aps_test.go`。本轮这些 Go 增量未移植至 Android Java，见协议差异记录。

本机参考文件与上述 GitHub 固定提交的 Git blob SHA 已核对一致：AIStudio2API build.go `09dfe081fa62028c67238188450069b0fbe66317`、client.go `1c929acf306a35640b9f221b290ae7a284bf5816`；APS schema.go `feb10accff96051a4b933d7a899c7a0f476f396c`、request_config.go `8e6faa257c935cc276f33f51f0ce8f12ad70a46c`。
