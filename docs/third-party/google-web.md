# Google 网页协议来源

2026-10-17，开发分支记录。源代码集成与个人账号实际推理分开验收。

| 来源 | 固定版本 | 采用范围 |
| --- | --- | --- |
| Mag1cFall/AIStudio2API | ebb27d109dfa8bcea96d481846f3f0a7536d89f0 | MIT；MakerSuite JSON+protobuf 字段、Build 代理封装、WAA 函数发现方式。重新组织为 Go 协议模块和隔离的官方页面会话。完整许可见 AIStudio2API-MIT.txt。 |
| DeconstructedCube/aistudio-api | 6d1f243b811834a88fecac5615e6a161ea112efb | 只核对协议与依赖说明；没有可核验的 LICENSE 文件，因此不复制其实现。 |
| DeconstructedCube/APS2api | 见 upstream-audit-2026-10-17.md 与 upstreams.json | PolyForm Noncommercial；作为匿名 GraphQL 请求/响应的协议核对输入。未复制其认证模拟器、代理池或调度实现。MIXED 请求模板来自用户当前官方页面，流解析和队列为独立实现。 |

相关模块：`internal/googleweb/`、`internal/gateway/google_*.go`、`internal/desktop/provider_windows.go` 与 `android/app/src/main/java/com/mixedapi/local/Google*.java`。

Windows 的 AIS 与无头V分别使用独立 WebView2 profile；Android 使用 AndroidX WebKit 1.12.1 的系统 WebView profile。网页只负责其自身官方会话的授权、验证及请求传输；Go/Java 负责公开 API、协议转换、错误处理、取消与背压。应用不下载或执行上游项目，也不使用本地未知修改版 EXE。官方网站正常加载的页面资源仍需网络。

测试：公开 HTTP 完整/流式请求、RPC 尾部错误、GraphQL 截断、输出上限、客户端取消，以及浏览器会话排队和窗口启动取消。Windows 已实测独立来源窗口进入官方登录页；个人账号推理、干净系统与 Android 真机仍需分别验证。此记录不能作为“所有上游功能已完整交付”的结论。
