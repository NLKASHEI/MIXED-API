# 协议能力与测试

v0.4.5 Windows新增：WorkBuddy三站账号/刷新隔离，Google已有账号检查与官方SDK加载，Gemini 4路由，快速V交付前重试/完整缓存/可选客户端续传。Android仅同步共享资源；Java尚无三站授权与续传模块，UI按平台隐藏这些选项，不发布APK。完整边界见ADR-0009及release-notes-v0.4.5.md。

2026-10-17，开发分支。Go 与 Android Java 共享配置及管理 HTML，分别实现本地 HTTP 核心；模拟服务方响应、实际 TCP 取消、平台模拟测试与真实账号/设备验收分别记录。

| 能力 | Go / Java 当前行为 | 测试 |
| --- | --- | --- |
| Chat、Responses、Messages | 原生协议直通或选定模型内转换；不换来源 | 三入口完整/流式、反向转换、错误与截断 |
| 客户端函数 | 声明、调用、结果、分片工具名、并行工具、参数完成校验 | 公共 HTTP 两轮往返，损坏参数不报成功 |
| Google 工具选择 | auto / none / required / 指定函数 / parallel_tool_calls=false；Playground 限制声明并加入调用提示，结果必须满足约束 | 完整/流式 × 正确/错名/缺少/禁止/多次调用 |
| 思考与引用 | 文本、Anthropic 签名/加密块、Responses 原始 reasoning 项目、URL 引用、拒绝内容 | 已覆盖的转换保留；无法表示的协议组合报错 |
| Google 签名 | thought_signature 随工具调用回传；google-gemini-v1 的 reasoning.text 保留文本思考签名 | 两轮工具/思考回归，其他协议签名生成前拒绝 |
| 输入媒体 | 文本、base64 图片；Google 新增 input_audio(wav/mp3) 与 file.file_data(data URL：PDF/图片/音视频) | 实际请求体核验；不代下载任意远程媒体 |
| 输出媒体 | 图片保持 Markdown data URL；Google 音频放在 Chat message.media / delta.media 扩展数组 | 防止非文本输出被转换成空白成功 |
| 语音 | /v1/audio/speech 返回 WAV 或 24 kHz 单声道 16-bit PCM；复用快速V/无头V/Build/Playground 的账号与取消 | 四能力 WAV/PCM、声音参数、损坏容器、声道与采样率校验 |
| 账号 | Codex / WorkBuddy 本人授权、导入、加密保存、过期或生成前 401 刷新、并发合并与轮换 | 重启读取、暂时失败退避、注销/重导入拒绝迟到刷新、账号身份变化拒绝 |
| HTTP 生命周期 | 有界读取、背压、客户端断开取消、停止监听关闭已接受和排队连接 | 实际 TCP 挂起读取、18 客户端停止、100 次立即重绑 |

## 客户端使用

模型 ID 始终为 `来源/原模型`。AIS 用 `aistudio/build/模型` 或 `aistudio/playground/模型`；别名只绑定一个目标。

普通语音请求示例（模型应从本人可用目录选择）：

```json
{"model":"aistudio/playground/可用语音模型","input":"你好","voice":"Kore","response_format":"wav"}
```

语音接口支持 `instructions` 描述朗读方式；`speed` 只接受 1。当前没有 MP3/Opus/FLAC 转码器，其他格式返回 400。WAV 保留来源采样率；PCM 要求 24 kHz，避免客户端按错误速率播放。未提供声音时默认 Zephyr。

Google Chat 可使用 `modalities:["text","audio"]` 和 `audio:{"voice":"Kore","format":"pcm16"}`；同时请求文字时不会静默覆盖为纯音频。`media_resolution` 为 low/medium/high。原始音频扩展为 `media:[{"mime_type":"audio/L16;codec=pcm;rate=24000","data":"base64..."}]`。不识别该扩展的客户端应使用二进制语音接口。

客户端需保留并回传 `thought_signature`、`reasoning_details`。后者的 `format` 区分来源协议：Anthropic 使用签名/加密内容；`openai-responses-v1` 的 `reasoning.item` 保留完整原始 item，包括 encrypted_content。应用不解密这些字段，不输出到日志。删除扩展字段的客户端还需兼容验收。

## 明确边界

- 非原生 Responses 的 previous_response_id、conversation、后台运行等无法等价实现时，生成前报错；不丢掉参数继续执行。
- Responses / Messages 不承诺表示 Google 原始音频；遇到该输出返回错误，不返回空白 completed。
- Playground 的工具选择约束不等于实现任意服务端搜索/代码执行或完整 JSON Schema 校验器。多说话人、转录和其他来源专有选项尚未纳入公共接口；不能据此声称全部上游功能完整融合。
- Build 使用 unary 的模型会先收集响应再发送 SSE；SSE 格式不等于来源支持逐 token 生成。
- API 35 / Robolectric、HTTP 夹具、CI 构建不能替代本人账号生成、Android 实体设备和干净 Windows 的实际产物验收。

## Windows 优先期间的差异

用户于 2026-10-17 暂停 Android 开发和发布。以下新修复只在 Windows Go 核心生效：无头V的 GraphQL Schema 转换及外层错误检查、Build 同模型的 unary 兼容重试、OpenCode 匿名 `public` 鉴权及付费模型 Key 前置检查、FreeTierError 说明。Android Java 尚未同步，不能把旧 APK 当作本次修复包。

AIS 请求准备阶段取消、输入框恢复仍通过共享脚本同步；Windows 取消确认必须为 true，否则先关闭失效页面。构建继续检查共享资源一致与 Android Gradle，设备验收暂停。具体 CI 及真实账号验收以[重构进度](integration-progress.md)和对应 Actions 为准。

Windows v0.4.2 新增系统/手动代理、快速V项目区域、扩展连接当前浏览器、详细网络诊断、OpenCode免费清单过滤。Android Java尚未移植；共享HTML隐藏Windows代理与区域入口，APK不随本次发布。

Windows v0.4.3 开发增量：Key 项目探测、快速V有限重试与超时、浏览器自动等待连接、GraphQL/RPC业务码日志仅在 Go 中实现。共享 HTML/会话脚本同步；Android 仍使用手动连接按钮，Java 不新增这些接口或请求策略，不发布 APK。
