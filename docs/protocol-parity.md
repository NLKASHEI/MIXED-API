# 协议转换验收记录

开发分支，2026-10-17。此记录不是发布验收结论。

Windows 内置核心新增 Responses / Messages → Chat 的请求转换及完整、流式输出。来源原生支持请求协议时仍走原生协议；转换不改变来源或模型，不自动从 Build 改投 Playground。已验证文本、函数声明、工具调用和结果、用量、流取消、长度截断、非法完成状态。原生协议中的状态功能由服务方处理；转换到无状态来源时，previous_response_id、conversation、后台运行等不能被丢弃后执行。

工具签名通过 `thought_signature` 扩展字段往返保留。客户端回传工具项目时需要保留该字段；删除未知字段的客户端还需要兼容验证。Chat 工具名允许分片，Responses / Messages 没有等价的工具名增量事件，因此转换器在工具身份和参数收集完成后发送工具块；普通文本持续增量输出。收集有大小限制，客户端断开取消原始请求。正常结束但工具参数损坏产生错误；因 token 限制中断保留 incomplete 状态。

本轮公共 HTTP 回归：Responses → 快速V工具和用量；Messages → OpenCode Chat 工具；Responses 实际 HTTP 断开取消；Messages 流正常/损坏参数；Gemini 签名工具两轮调用；分片工具名；完整与流式的长度截断及未知结束状态。

尚待扩展：Android 同等转换；其他高级媒体与来源专有参数；完整客户端互操作、本人账号和设备验收。不能据此宣称全部协议或全部客户端均已通过。

Android 检查点：快速V直接调用 Gemini；OpenCode 按内置共享目录分派 Chat、Responses、Messages、Gemini。Chat 客户端的完整/流式请求已覆盖工具调用、思考文本与签名、用量、缺失终止帧和损坏工具参数。新增本地账号加密存储、导入、重启读取和注销，生产密钥来自 Android Keystore；JVM 测试仅注入 JCE 密钥，不替代设备 Keystore 验收。当前本机 assembleDebug、全部单元测试、lintDebug 通过（lint 0 错误、10 警告）。账号刷新、反向协议桥接及设备验收继续开发，不能将此检查点当作正式 release。

Android 后续账号检查点：Codex 导入后的目录/生成走内置账号请求，自动刷新仅在临近过期或生成前 401；同来源并发合并、原子保存轮换、短暂失败退避、注销/重导入撤销旧刷新。HTTP 测试覆盖六个同时请求仅刷新一次、重启恢复、连续两次 401 轮换、忽略取消的旧刷新迟到。WorkBuddy 刷新编码已移植，授权流程和端到端测试尚待补齐；仍不能将目录可用等同真实推理或发布验收。

进一步完成：Android WorkBuddy v2 授权状态、账号身份、刷新和 Chat 生成；Codex 设备授权和 PKCE 表单交换。均使用公共管理/推理接口及模拟服务方响应验证，未进行本人登录。真实本地 HTTP 服务另验证 PKCE Content-Type、读取挂起时的客户端断开、503/408/重定向不重复提交生成。传输取消从可阻塞的 HttpURLConnection.disconnect 改为 OkHttp Call.cancel；新增依赖和平台边界见 ADR-0005、第三方声明。

2026-10-17 授权与网络检查点：Android Codex / WorkBuddy 授权、单次请求取消及六来源准备/启停入口的本机回归完成。AGP 8.13.2 + Gradle 8.13 修正 OkHttp Kotlin 元数据与旧 lint 的不兼容；assembleDebug、32 项 HTTP/平台测试、lintDebug 通过，lint 0 错误、13 警告（平台特性检查、备份策略和目标 API 等继续审查）。Go test ./...、go vet ./...、共享资源校验通过。此为开发检查点，真实账号和设备验收仍未通过，未发布。

2026-10-17 协议/配置检查点：Android 新增原生 Responses / Messages SSE，Codex 原生 Responses 完整结果收集，及 Responses / Messages 到指定 Chat 来源的双向完整与增量转换；覆盖 AIS Build、Playground、无头V，无跨来源回退。工具名称分片、签名、用量、长度截断、缺少完成帧均有公共 HTTP 回归；三个客户端协议在真实 HTTP 挂起读取中均可先收到增量，再断开取消。跨协议不支持的状态/缓存选项先报错。配置 UI 增加可点击授权链接、本人 Codex 文件导入，修复电脑别名重复前缀；Android 系统文件选择与销毁取消经平台测试。Go test/vet、共享资源、真实浏览器桌面/390px 布局回归通过；Android assembleDebug + 38 项测试 + lintDebug 通过（0 错误、13 警告）。高级媒体、深层参数、系统服务/备份策略及本人账号/设备验收仍需完成，此记录不是 release 验收。


2026-10-17 平台与账号检查点：Android targetSdk 35，新增系统栏/刘海/键盘避让、用户可停止的 specialUse 本机监听通知、配置及会话的备份/迁移排除。电脑共享与外部 HTTP 改用同一可取消传输；独立 Standards 复核发现停止监听器未关闭已接受连接，已用 18 个并发/排队客户端复现并修复。三处协议复核问题（Responses 输出回传、多个签名思考块、原生 Messages 完成工具校验）在 023b177 修复。Go/Android 拒绝刷新后的账号身份变化，过期且不可刷新的凭证显示过期；Go 导入拒绝 CR/LF/NUL 且不替换原账号。Go 全量 tests/vet、Android assembleDebug、47 项公共接口/平台回归和 lintDebug 通过（0 错误、11 警告）。API 35 模拟平台测试不是真机测试；ADB 当前未连接设备。Spec 轴仍由主代理审查，未宣称真实账号、干净系统或最终 Release 验收。
