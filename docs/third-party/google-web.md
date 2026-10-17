# Google 网页协议来源

2026-10-17，开发分支记录。源代码集成与个人账号实际推理分开验收。

| 来源 | 固定版本 | 采用范围 |
| --- | --- | --- |
| Mag1cFall/AIStudio2API | 2cbe310ce6ebab093bf83e5078de8296f03770a9 | MIT；MakerSuite JSON+protobuf 字段、Build 代理封装、WAA 函数发现、语音字段与显式工具调用约束。重新组织为 Go/Java 协议模块和隔离的官方页面会话。完整许可见 AIStudio2API-MIT.txt。 |
| DeconstructedCube/aistudio-api | 9bd5a20f25bbfae60c84b59b6a9f167b85c7d1a3 | 只核对协议与依赖说明；没有可核验的 LICENSE 文件，因此不复制其实现。 |
| DeconstructedCube/APS2api | 见 upstream-audit-2026-10-17.md 与 upstreams.json | PolyForm Noncommercial；作为匿名 GraphQL 请求/响应的协议核对输入。未复制其认证模拟器、代理池或调度实现。MIXED 请求模板来自用户当前官方页面，流解析和队列为独立实现。 |

相关模块：`internal/googleweb/`、`internal/gateway/google_*.go`、`internal/desktop/system_browser_windows.go` 与 `android/app/src/main/java/com/mixedapi/local/Google*.java`。

Windows 的 AIS 与无头V使用系统默认 Edge / Chrome 的 MIXED 专用 profile（ADR-0006）；Android 使用 AndroidX WebKit 1.12.1 的系统 WebView profile。网页只负责其自身官方会话的授权、验证及请求传输；Go/Java 负责公开 API、协议转换、错误处理、取消与背压。应用不下载或执行上游项目，也不使用本地未知修改版 EXE。官方网站正常加载的页面资源仍需网络。

测试：公开 HTTP 完整/流式请求、RPC 尾部错误、GraphQL 截断、输出上限、客户端取消，以及浏览器会话排队和窗口启动取消。Windows 已实测独立来源窗口进入官方登录页；个人账号推理、干净系统与 Android 真机仍需分别验证。此记录不能作为“所有上游功能已完整交付”的结论。

本次补齐官方页面初始化、默认浏览器传输和连接/生成分开检查；取消后迟到的页面请求仍被拦截。没有将 DeconstructedCube 未提供可核验许可的实现直接复制，也没有集成 APS 的认证模拟器或 OpenCode 的客户端身份伪装。源代码参考和运行可用性不能混为一谈。

DeconstructedCube/aistudio-api 当前 master 的 README 明确要求安装受控浏览器，使用 Python CDP，并非不依赖浏览器的实现。README 虽链接 MIT，当前根目录没有 LICENSE；此版本仍仅作依赖及协议核对。
