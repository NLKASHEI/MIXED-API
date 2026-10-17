# 内置核心审查记录

基线：`1a8317661d17b4e44ad5533ad155c879bcecfe6e`。第一次实现检查点：`ddec7322e3156ad26c11b20fd72aaf6caa385ef4`。按 mattpocock code-review 分别运行规范和规格审查，未将两者混作完成判定。

## Standards

发现并修复：快速V的 `content:null` 产生空 Gemini part；OpenAI strict 工具字段未经转换；渲染器损坏后每次启动重复解包。Express SSE 改用共享有界解析器，取消重复的流帧实现。

对应验证：公开 HTTP 工具往返、named tool choice、Go tests/vet、捆绑构建检查。渲染器修复目录只在逐文件哈希校验通过后复用；干净 Windows 的完整启动与损坏修复仍需产物验收。

## Spec

发现并修复：旧 Codex CLI 配置在升级后仍启动外部程序；后台将内置来源误当子进程；快速V丢失图片输出；named tool choice 被忽略；多候选流只结束一个候选便报告完成。

对应验证：旧配置迁移及原配置备份、后台启停保持 builtin 模式、图片返回、工具选择、第二候选中断时不发送成功结束标志。旧 Codex CLI 适配器与相应外部进程测试已移除，替换为内置 HTTP 和授权接口测试。

未完成项仍独立跟踪：AIS、无头V、个人账号推理、干净 Windows、Android 真机和最终发布资产。本记录不构成全规格通过结论，不关闭 Issue #3。
