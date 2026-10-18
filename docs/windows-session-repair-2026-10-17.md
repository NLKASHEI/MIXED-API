# Windows v0.4.0 来源会话问题

用户报告：Windows EXE 的 AIS 与无头V不能使用，来源说明仍写开发中，登录错误地进入内嵌浏览器，手动消息不能验证反代。OpenCode 未区分免费和 Key 模型。

根因核对：旧版 Go/Java 协议转换已经存在，但官方页面会话初始化缺失；成功的手动网页请求也不能证明 MIXED 请求成功。AIS 源码中调用 Run 获取会话的流程此前没有完整接入。无头V 的当前页面是 Agent Studio 的 contenteditable，不能依赖旧输入框及笼统的测试消息提示。

修复：默认浏览器专用资料目录、自动初始化与取消阻断、具体状态、配置页真实对话测试；OpenCode 模型 access 字段与收费说明。上游核对：AIStudio2API 2cbe310ce6ebab093bf83e5078de8296f03770a9，opencode-proxy 27626a9d192b988938bb3d5f0e4fa241af7ae309。没有加入客户端身份伪装。

本地验证：Go 全量测试与 vet、共享资源校验、JS 初始化/拒绝/取消测试、Playwright 实际 HTTP 界面回归通过；Windows 默认 Edge 打开、Target 关闭、取消清理、同 profile 重开通过；Android 59 项 JVM 测试、assembleDebug、lintDebug 通过。Windows 来源页面仅检查未登录官方页面，不代替本人账号推理。

标准轴发现并修复：取消后迟到初始化请求外发、Android 初始化断开不取消、测试结果标错模型，以及窗口目标关闭误判存活。需求轴由主代理核对：默认浏览器仅 Edge/Chrome；Google 实际推理、干净 Windows、Android 真机未验收。真实账号推理尚未通过，不能把本修复称为 AIS / 无头V完全可用版。

本地完整打包 EXE 验收：GUI 子系统、独立可见主窗口、本地 /health、账号接口启动系统默认 Edge、正常关闭退出通过；使用单独的空白测试配置，无登录、无真实生成。对应开发构建为 v0.4.1-dev，不能当作正式 Release 资产。

后续 Windows 修复：旧版仍运行时，新 EXE 原先只验证管理密钥就打开旧服务。现通过受保护的 `/api/status` 核对服务名及构建版本；缺失或不同版本一律拒绝接入。配置错误、端口冲突和窗口启动失败会显示原生提示，无控制台依赖，不回显配置。新增真实单 EXE 验收脚本覆盖空渲染缓存、重复打开、关闭、配置保留及旧服务拒绝。

前次 Actions 39028457829 的 verify、Windows、Android Gradle 构建通过，但 Android 模拟器因通知停止按钮未找到而失败，不能称整条 CI 通过。用户随后明确暂停 Android，Windows 优先；当前不上传 APK、不运行 Android 设备验收，继续保留 Gradle 回归。模拟器缺陷及真实 Google 生成仍未结案。

本轮核心补齐：按 APS 上游参数格式转换函数与结构化输出 Schema，拦截外层 GraphQL 错误；采用 AIStudio2API 的同模型 Build 单次调用兼容规则（含 RPC code 12 和明确的 400）；AIS 请求准备取消必须确认或销毁页面，恢复输入框且保留用户后来输入；OpenCode 匿名目录/推理使用 public 凭据，付费模型缺 Key 直接提示，FreeTierError 不回显原错误内容。协议夹具中的付费模型补入合成 Key，保留原转换断言。

Go tests/vet、JS 会话回归及共享资源检查作为核心收尾检查。已进行的本机窗口检查只证明 HWND、资源展开与退出行为，不证明最终页面加载、安装体验或个人账号生成。用户要求外围体验由其测试，本轮不追加 UI、安装器或 Android 设备验收循环。
