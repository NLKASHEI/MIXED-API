# 内置核心审查记录

基线：`1a8317661d17b4e44ad5533ad155c879bcecfe6e`。第一次实现检查点：`ddec7322e3156ad26c11b20fd72aaf6caa385ef4`。按 mattpocock code-review 分别运行规范和规格审查，未将两者混作完成判定。

## Standards

发现并修复：快速V的 `content:null` 产生空 Gemini part；OpenAI strict 工具字段未经转换；渲染器损坏后每次启动重复解包。Express SSE 改用共享有界解析器，取消重复的流帧实现。

对应验证：公开 HTTP 工具往返、named tool choice、Go tests/vet、捆绑构建检查。渲染器修复目录只在逐文件哈希校验通过后复用；干净 Windows 的完整启动与损坏修复仍需产物验收。

## Spec

发现并修复：旧 Codex CLI 配置在升级后仍启动外部程序；后台将内置来源误当子进程；快速V丢失图片输出；named tool choice 被忽略；多候选流只结束一个候选便报告完成。

对应验证：旧配置迁移及原配置备份、后台启停保持 builtin 模式、图片返回、工具选择、第二候选中断时不发送成功结束标志。旧 Codex CLI 适配器与相应外部进程测试已移除，替换为内置 HTTP 和授权接口测试。

未完成项仍独立跟踪：AIS/无头V真实账号生成、剩余协议与账号刷新、干净 Windows、Android 真机和最终发布资产。本记录不构成全规格通过结论，不关闭 Issue #3。


## 2026-10-17 Google 与 Android 检查点

固定基线保持 `1a8317661d17b4e44ad5533ad155c879bcecfe6e`。新实现检查点为 `46983241508c1e88247fbd0d2dc92eda30cd37de`，完整 Actions [39008922441](https://github.com/NLKASHEI/MIXED-API/actions/runs/39008922441) 的 Windows、Android、verify 全通过，release 因未打标签跳过。

### Standards

本轮独立审查发现 3 项，均已修复并增加回归：Go 非流式候选数未匹配 n；Android 最终限流响应丢失 Retry-After；Android 诊断状态与共享 UI 不一致。没有新增可行动的 Fowler 判断项。

补充自查修复：Android 客户端断开时中止等待和来源生成；取消指令排入 UI 之前不释放会话；Go 窗口不响应取消时关闭失效页面并要求显式重开；GraphQL 连续根对象/数组结果识别及尾部错误；Playground JSON Schema 转换；Google 参数类型验证；原生设置直接生效。

### Spec

本轮 Spec 轴由主代理逐项核对；此前独立 Spec 代理遇到容量限制，没有另建代理绕过，因此不宣称本轮完成两个独立代理并行审查。

- 规格验收 1、3、7：干净 Windows 的离线安装与便携启动、完整进程/网络记录仍待完成。本机已确认独立窗口、后台与渲染进程，不能替代干净系统。
- 规格验收 4：模拟协议、取消和错误测试通过；用户明确暂不登录，真实 AIS/无头V生成不能标记通过。原生转换当前以 Chat Completions 为入口，更多协议和高级媒体、其他账号刷新仍未完成。
- 规格验收 5、7：Android 纯 Java 源码、共享配置、页面和 CI 已验证；系统 WebView 隔离功能、账号窗口及后台生命周期仍需真机验收。

本轮 Standards 独立审查 3 项已修复；Spec 仍有上述 3 组验收缺口，最严重的是缺少真实账号与干净设备证据。两项结果分别记录，不能据此关闭 Issue #3 或发布全功能正式版。

### 恢复路径复核

追加独立审查发现两个生命周期问题：Windows 旧窗口未退出时，新窗口注册相同类名失败；Android 取消回调不返回时，会话锁一直占用。Windows 改用每实例唯一类名和共享窗口回调，实际 Win32 注册测试先复现失败、修复后通过。Android 取消超过 1 秒未确认时，先废弃并销毁旧页再释放锁，旧等待者不能继续使用失效页；迟到回调不影响新页。新增渲染器挂起/确认取消回归，须以本提交的 Android CI 结果为准。

2026-10-17 Android 原生协议增量 Standards 审查（固定 `73d56fd`）发现三项：成功 Responses 的 `error:null` 被误判失败；快速V的 `-search` 后缀丢失搜索工具；Opus 4.8 错用手动思考。均用公共 HTTP 用例复现后修复；4.8 同步修复 Go 与 Java，能力依据 [Claude 官方 thinking 文档](https://platform.claude.com/docs/en/build-with-claude/thinking)。Spec 由主代理继续对照 ADR-0005 和未完成项检查，尚不是全规格验收通过。

Android CI [39014151486](https://github.com/NLKASHEI/MIXED-API/actions/runs/39014151486) 复现 Linux 立即重启监听端口的 BindException。监听器改为绑定前设置地址复用，绑定失败关闭未成功的 socket；复用不改变仅监听 127.0.0.1 的限制。需后续 CI 确认修复，不能把本机 Windows 测试当作 Linux/Android 通过。

后续 [39014712431](https://github.com/NLKASHEI/MIXED-API/actions/runs/39014712431) 在 `5d494f7` 的 verify、Android 全通过，证实端口重启修复；Windows 第一次失败为 Chocolatey 返回 503，重试后也通过。未打发布标签，release 跳过。之后的真实 HTTP 回归复现原生读取取消不及时，已将网络调用换为可取消且 one-shot 的传输；须在后续提交再次运行全量 CI。

定向复核又复现 Windows 启动取消后遗留 WM_QUIT，污染下一次复用的 STA 线程。现仅在消息循环运行时投递退出消息；同 STA 连续创建/销毁回归通过，独立复核确认该修复。上述两项原生窗口测试不需要账号或渲染器；Android 的两个渲染器生命周期测试已在 `f0ea36a` 的 CI 通过。

2026-10-17 授权与网络检查点：Android Codex / WorkBuddy 授权、单次请求取消及六来源准备/启停入口的本机回归完成。AGP 8.13.2 + Gradle 8.13 修正 OkHttp Kotlin 元数据与旧 lint 的不兼容；assembleDebug、32 项 HTTP/平台测试、lintDebug 通过，lint 0 错误、13 警告（平台特性检查、备份策略和目标 API 等继续审查）。Go test ./...、go vet ./...、共享资源校验通过。此为开发检查点，真实账号和设备验收仍未通过，未发布。

2026-10-17 协议/配置检查点：Android 新增原生 Responses / Messages SSE，Codex 原生 Responses 完整结果收集，及 Responses / Messages 到指定 Chat 来源的双向完整与增量转换；覆盖 AIS Build、Playground、无头V，无跨来源回退。工具名称分片、签名、用量、长度截断、缺少完成帧均有公共 HTTP 回归；三个客户端协议在真实 HTTP 挂起读取中均可先收到增量，再断开取消。跨协议不支持的状态/缓存选项先报错。配置 UI 增加可点击授权链接、本人 Codex 文件导入，修复电脑别名重复前缀；Android 系统文件选择与销毁取消经平台测试。Go test/vet、共享资源、真实浏览器桌面/390px 布局回归通过；Android assembleDebug + 38 项测试 + lintDebug 通过（0 错误、13 警告）。高级媒体、深层参数、系统服务/备份策略及本人账号/设备验收仍需完成，此记录不是 release 验收。
