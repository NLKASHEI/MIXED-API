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
