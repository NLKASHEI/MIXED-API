# MIXED-API v0.4.0

本次先发布 Windows 内置核心版：独立 EXE、Setup 安装包和便携 ZIP；随后补充同版本的 Android 测试版 APK。所有二进制均来自本 tag 的成功构建与验收。

## Windows

- 双击 EXE 打开独立工作台窗口，大字号、后台管理及配置步骤内的指引；普通启动不打开外部浏览器或 CMD。
- EXE 随包包含固定版本渲染资源，首次在本地展开并校验。六个来源的请求与协议模块内置，不下载、启动上游代理应用，也不要求用户安装 Go、Node 或 Python。
- 快速V、无头V、Codex、OpenCode、WorkBuddy、AIS 共用一个客户端地址。AIS 的 Build / Playground 分开选模型，不自动切换来源。
- Chat / Responses / Messages 适配，工具调用、流式取消、思考签名回传；Google 图片、音频/PDF输入及 WAV/PCM 语音输出。
- Codex / WorkBuddy 本人授权、加密账号保存和刷新；管理 Key 与客户端 Key 隔离，六个来源默认关闭。
- 修复协议内容丢失、工具约束漏验、窗口与连接关闭竞态。配置及原有账号仍需按各来源步骤核对。

下载 `MIXED-API-Windows-x64-Setup.exe` 安装，或直接运行 `MIXED-API-Windows-x64.exe`。ZIP 适合便携使用。`Install-MIXED-API.ps1` 支持指定安装目录；`SHA256SUMS` 提供资产校验值。

## 验证与范围

发布工作流执行 Go tests/race/vet、共享界面与浏览器回归、Windows 构建，以及 Android 55 项测试、Lint 和 Android 15 模拟器验收。模拟器验证全新安装、覆盖升级、配置与 Keystore 保留、通知停止、重启和注销；它不代表旧 v0.3.1 迁移或实体手机验收。

用户暂未进行本人账号登录测试；干净 Windows 的离线安装/便携启动和完整进程、网络验收尚未完成。多说话人、转录、完整工具 JSON Schema 校验及部分来源专有参数仍有限制，详见 [协议能力](https://github.com/NLKASHEI/MIXED-API/blob/v0.4.0/docs/protocol-parity.md)。本版不宣称全部上游功能与所有设备已完整验收。

Windows 包尚无代码签名。`MIXED-API-Android-preview.apk` 是不可调试的 Android 测试版，使用临时签名；固定正式签名尚未配置，未来签名变化时可能需要先保存配置、卸载重装并重新授权，不保证后续覆盖升级。它不作为稳定升级渠道。本次发布不关闭后续集成与验收事项。
