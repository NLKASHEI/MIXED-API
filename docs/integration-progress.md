# 内置重构进度

开发分支记录，2026-10-17。不是 Release 验收结论，当前正式版仍为 v0.3.1。

| 功能 | 当前代码 | 验证范围 |
| --- | --- | --- |
| Windows 独立窗口 | Go + WebView2；普通启动不再打开外部浏览器 | 本机实际窗口显示；新的 GUI EXE 已核验 PE 子系统 |
| 离线渲染资源 | 固定版 155.0.4283.60 随 EXE 嵌入，首次本地展开并逐文件校验 | 本机包哈希、微软签名、实际展开与窗口启动；尚无干净虚拟机验收 |
| 快速V | Go 直接调用 Express REST，内置消息、工具、思考与流转换 | 黑盒模拟上游；未验证个人 Key 推理 |
| OpenCode | Go 直接请求当前官方端点；内置审查过的协议目录，支持 Chat→Responses、Chat→Anthropic、Chat→Gemini 的完整/流式转换 | 黑盒模拟上游、并行工具与思考、真实 HTTP 取消、错误不回退；其他反向协议转换待补全 |
| WorkBuddy | 原生授权轮询与 HTTP，非流式客户端由 SSE 收集 | 黑盒模拟授权/推理；实际账号、刷新与更多协议待验证/补全 |
| Codex | 原生设备码授权、授权文件导入、Responses HTTP；Chat/Responses 输出 | 黑盒模拟授权/推理；实际账号、刷新待验证/补全 |
| 无头V | Go/Java 内置 GraphQL 请求、Gemini 转换、官方页面会话、取消和流校验 | 模拟协议回归；本人官方页面生成与真机待验证 |
| AIS | Go/Java 分别内置 Build RPC 与 Playground JSPB，独立模型目录；隔离官方页面保存授权状态 | Windows 授权窗口已到官方登录页，用户选择暂不登录；模拟协议通过，真实推理待验 |

旧上游下载器、运行时安装器、本地 EXE 导入与外部进程启动代码已删除。准备步骤只配置内置核心，外部启动接口返回 410。无头V参数直接由应用保存并执行：请求时限、仅限生成前 429/503 的重试、完整收集后发送。默认不重试，不在部分生成或断线后重放。

UI 使用「工作台」「模型列表」「诊断」「设置」「后台管理」等功能名称，删除宣传式标题。名称采用「快速V」「无头V」「OpenCode」「WorkBuddy」「Codex」「AIS」；AIS 内部按实际能力区分 Build 与 Playground，不把不同仓库实现作为用户运行版本。

Android 新增纯 Java AIS/无头V模块，通过系统 WebView 的独立 profile 与 document-start 脚本承载用户官方会话；无 JavascriptInterface，不下载上游程序。需要系统 WebView 支持隔离会话，能力不足时配置页明确提示。另四个来源目前仍由电脑或手动 HTTP 桥接提供，不能称为 Android 六核心全部内置。共享页面、配置与源清单同步。电脑模型明确使用 `pc/电脑模型ID`，不再将不匹配的来源静默转给电脑。

Windows 发布链需要 `-tags bundled`；开发时未带该标签的构建仅可使用已安装的 WebView2，不能作为完整交付包。正常安装与便携启动直接使用 EXE，局域网快捷方式传 `--lan`，发布包不附 CMD 启动器。

当前验证：Go tests、vet、共享页面检查及 Playwright UI 回归通过。个人账号、Android 真机、干净 Windows 与最终发布资产仍待完成。本记录不关闭 Issue #3，不触发功能 Release。

OpenCode v58 / v59 / 当前主分支和新增 AIS 来源的核对结果见 [2026-10-17 上游核对](upstream-audit-2026-10-17.md)。本轮双轴审查及已修复问题见 [审查记录](review-integrated-core.md)。

## 此检查点的限制

AIS/无头V当前公共接口为 Chat Completions；其他协议明确返回 501。文本、base64 图片、函数工具、思考、结构化输出及流式帧已有协议转换测试。Playground 的强制工具选择、音频/TTS与部分高级工具未完成；Build 对已在目录识别的免费模型使用增量 RPC，对未知/订阅模型使用单次 RPC 后输出 SSE。不能把 SSE 形式当成所有模型均可逐字输出的证明。

本次新 Java 模块需以对应提交的 Android CI 为准；真实设备尚未测试。账号刷新、跨协议覆盖、个人账号生成、干净 Windows、APK真机及正式发布资产仍是未关闭的交付条件。用户明确暂不登录，继续独立开发，不代登录、不把该项写成通过。

最新已验证检查点：`4698324`，Actions [39008922441](https://github.com/NLKASHEI/MIXED-API/actions/runs/39008922441) 的 Windows 构建、Go race/vet、UI 回归、Android 构建/单元测试/Lint 全部通过。构建产物存在于 Actions，尚未创建新 Release。本机新版 EXE 工作台与后台已实际打开，直接子进程为内嵌 WebView2；该观察不是完整进程追踪或干净系统验收。
