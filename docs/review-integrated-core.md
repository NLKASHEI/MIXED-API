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


2026-10-17 平台与账号检查点：Android targetSdk 35，新增系统栏/刘海/键盘避让、用户可停止的 specialUse 本机监听通知、配置及会话的备份/迁移排除。电脑共享与外部 HTTP 改用同一可取消传输；独立 Standards 复核发现停止监听器未关闭已接受连接，已用 18 个并发/排队客户端复现并修复。三处协议复核问题（Responses 输出回传、多个签名思考块、原生 Messages 完成工具校验）在 023b177 修复。Go/Android 拒绝刷新后的账号身份变化，过期且不可刷新的凭证显示过期；Go 导入拒绝 CR/LF/NUL 且不替换原账号。Go 全量 tests/vet、Android assembleDebug、47 项公共接口/平台回归和 lintDebug 通过（0 错误、11 警告）。API 35 模拟平台测试不是真机测试；ADB 当前未连接设备。Spec 轴仍由主代理审查，未宣称真实账号、干净系统或最终 Release 验收。


## 2026-10-17 媒体、工具约束与 PR 报错分类

当前 PR #4 及旧 PR #1/#2 的线上 review/comments 均为空。本轮红叉来自 Actions，不能将它们全解释成代码审查结论。

需要修复并有失败回归的行为：音频经 Responses 转换丢失后返回空白成功；audio 配置覆盖用户声明的文字输出；Go 接受双声道 PCM 却写成单声道 WAV；WAV 尾部/填充校验遗漏；显式工具约束未验证；Responses 加密思考项目丢失；Android 关闭监听后立即重绑的端口释放竞态。均已修复，协议正反例与 100 次关闭重绑纳入测试。

测试自身修正：旧 TestExpressInputToolRoundTrip 强制 weather 函数，却让模拟服务方返回文字并期望成功。现模拟真实函数返回，保留原请求映射断言，不放松新合约。

CI 环境修正：Android 模拟器首次找不到 adb，随后日志明确报 Unknown AVD name；设置平台工具 PATH 和一致的 ANDROID_USER_HOME / ANDROID_AVD_HOME。其失败不说明业务协议坏了，但未通过前也不计作设备验收。保留日志，不能靠删除验收步骤让红叉变绿。

无需随本次业务改动处理：依赖存在新版、Gradle 9 弃用预告、第三方 Actions 的 Node 20/punycode 提示；当前实际构建成功。Android Lint 的 JavaScript 使用是本机管理页/官方会话的已审查设计，不因通用提示删除必要能力。未采用无复现路径的风格重构。

独立 Standards 定向复核媒体校验、工具约束、reasoning.item 往返和监听关闭修复后，未发现新增可行动缺陷；4 个 Go 定向 HTTP 测试独立通过。Spec 由主代理核对：账号真实生成、干净 Windows、实体 Android 与完整专有能力仍不可写成通过。当前本机 Go tests/vet、Android 54 项测试及构建/lint 通过；端口修复需新的 Linux CI 验证。

Gemini 文字思考签名补充：公开 HTTP 两轮回归先复现 thoughtSignature 丢失，Go/Java 同步保留 google-gemini-v1 reasoning.text，并在回传其他协议签名时生成前报错。本机全量 Go tests/vet、Android assembleDebug/55 项测试/lintDebug 通过。909037e 的 Linux Android CI 已通过含100次重绑回归；模拟器验收仍单列。

`100ef39` 的 [Actions 39020922946](https://github.com/NLKASHEI/MIXED-API/actions/runs/39020922946) 全部通过，Android 15 模拟器实际完成安装、界面、默认关闭来源、管理隔离、通知停止、重启、Keystore 和注销检查。通知失败经截图/系统状态确认是脚本滚动离开通知栏及系统延迟显示；修改定位和等待，没有改变正常通知策略。

发布链另有实质问题：Debug APK 和临时签名影响发布与覆盖升级。新的实际 APK 检查先拒绝现有 Debug 包，改用不可调试 Release 后通过，错误证书指纹仍被拒绝。PR 使用临时测试密钥，非 PR 缺固定密钥/密码/预期指纹时失败；升级验收使用不同 versionCode、同签名包验证配置和 Keystore 保留，再全新安装目标包。独立 Standards 审查未发现可行动缺陷；Spec 自查明确该基线为同源码较低版本，不代表旧 v0.3.1 数据迁移、实体设备或真实账号已经通过。正式密钥尚未配置，不发布临时签名包。


## v0.4.0 发布范围复核

用户后续明确要求发布 Release，并要求先发布电脑版，再补 APK。固定正式签名配置未完成，本次采用明确命名的 Android 测试版，不作为固定签名的替代方案。此前“不发布临时签名包”的发布计划由该后续要求调整；`signed` 模式仍缺密钥即失败。

Standards 独立只读审查：未发现本轮发布改动中的阻断性或实质问题。Windows 先上传，Android-preview 随后单独上传并更新校验清单；验收基线不进入 Release。预览模式不读取正式私钥或密码；两种模式保留完整测试、APK 实际检查和模拟器门禁。

Spec 主代理复核：发布顺序和 APK 命名符合最新请求；本人账号、干净 Windows、实体 Android 与高级专有能力的验收缺口继续披露，Issue #3 保持开放。没有将本次发布等同于旧规格全部完成。

提交前 Go test ./...、go vet ./...、actionlint 和发布依赖/顺序核对通过。v0.4.0 最终资产必须由对应 tag 的 Actions 构建并验收后上传；本记录不预先证明 Release 附件已上传。

## v0.4.1 Windows 核心修复审查

本轮固定基线 `5cbc768`，依据 Issue #5、ADR-0006 和用户的 Windows 核心优先要求，独立审查仅覆盖协议、鉴权、取消与发布范围；没有追加外围 UI/安装器/Android 设备验证。

### Standards

核心增量未发现新的可行动缺陷。APS 不支持的约束明确拒绝；GraphQL 外层错误阻止完成；OpenCode 错误固定脱敏；准备阶段取消未确认先销毁页面。Build 新增 RPC 错误解析经定向复核：读取有界，不回显正文，成功、授权、额度和已有生成帧的响应不触发兼容重试。

### Spec

发现并修复 1 项：Build 仅处理 HTTP 405/501，遗漏上游 code 12 及明确 unsupported 的 400 分支。已按同一固定版本的 `sendBuild` 和 `DecodeRPCError` 接入，公开 HTTP 先复现 4 个失败后通过；独立复核关闭该项。其余核心新增行为符合本轮范围。Google 本人账号生成仍未验收，Issue #5 保持开放。

此前发布范围审查发现 Android `none` 仍上传 APK，已修为只保留 Gradle 构建/测试/Lint，不准备签名或上传 APK。Windows 本机验收仅证明 HWND/资源/退出，不宣称最终页面加载或干净安装通过；这些外围体验按用户要求留待人工测试。

首次 v0.4.1 标签构建 39030745680 的核心 verify 通过，Android 打包暴露 `none` 仍绑定空 SigningConfig 的配置错误。修复为仅 `none` 不绑定签名，生成的未签名内部检查产物不上传；signed/preview 的签名与发布门禁保持有效。该次失败未发布 Release 资产；在新的提交重新执行标签构建，不能引用失败运行作为发布依据。
