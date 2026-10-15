# MIXED-API · 零基础一步到位指南

你不需要自己去六个项目里下载程序、解压 ZIP、改 JSON、填写自己刚生成的本地 Key。**基础配置由 MIXED-API 执行，账号授权才需要你自己操作。**

## 第一次启动

1. 打开 [最新正式版（永久地址）](https://github.com/NLKASHEI/MIXED-API/releases/latest)，Windows 用户下载 `MIXED-API-Windows-x64-Setup.exe`；不想安装的下载免安装 ZIP。Android 下载 APK。
2. Windows 双击打开软件，自动打开 `http://127.0.0.1:8765`。从首次窗口复制 Admin Key 登录后台。此 Key 和客户端 API Key 不同。
3. 左侧点 **「上游服务」**，选一个来源，点击醒目的 **「▶ 一键接入（自动配置 + 启动）」**，剩下的下载、随机本地 Key、配置文件、启用及启动都会由 MIXED-API 完成。想用哪个开哪个，不必全部开启。
4. 如果提示需要 ChatGPT、Google 或 WorkBuddy 官方登录，按照页面提示在官方授权窗口**本人确认一次**。已有效登录可直接复用；账号限制、模型权限与上游计费仍生效。
5. 左侧点 **「聚合模型」** → 刷新，复制一个真实显示的模型 ID，例如 `codex/gpt-...`。你的任何 OpenAI 兼容客户端只需填写 **Base URL `http://127.0.0.1:8765/v1` + MIXED-API 客户端 Key + 这个完整模型 ID**。

### 六个来源各自还需要什么

| 来源 | 自动完成 | 需要本人提供 |
| --- | --- | --- |
| Codex | 安装 CLI、直连 app-server，无须 SillyTavern | 官方 ChatGPT 首次登录 |
| Vertex2OpenAI | Python 运行环境中的虚拟环境、启动、网关随机 Key | Google Express API Key 或上游合法认证 |
| OpenCode | 安装官方已编译版、配置本地 Key、启动 | 可选 Zen API Key；匿名访问不能保证 |
| WorkBuddy | 下载中转并自动发现桌面客户端 | 官方 WorkBuddy 桌面客户端及登录 |
| APS2api | 下载已编译版、配置本地 Key、启动 | 首次阅读并同意相关规则（非商业授权） |
| AIStudioToAPI | 安装 Node 依赖、配置随机 Key/本地端口、启动 | 在官方网页一次 Google 登录，已保存的有效会话可复用 |

如果缺 Node.js/Python，按 **「一键接入」** 会尝试调用 Windows winget 安装官方运行时。Windows 已运行进程可能不能立即发现新 PATH，**安装完重启 MIXED-API，再点同一个按钮**，无需自己输入命令。

**上游项目页面仅用于查看开源许可证/高级配置，不是基本安装流程。** 不会收集或自动导出你的 Google Cookie、ChatGPT OAuth Token、账号密码。无法保证非公开接口和第三方反代长期可用。

## 永久更新

桌面左侧 **「检查更新」** 始终存在；还可以直接访问 **https://github.com/NLKASHEI/MIXED-API/releases/latest**，不会因新版本改 tag 而失效。Windows 点「检查更新」→「下载并打开安装向导」，软件会验证官方附件的 SHA-256；由你确认安装，必要时关闭正在运行的旧进程。已有 `mixed-api.json` 默认不会被删除。Android APP 首页及管理页面有 **「检查更新 / 下载最新版 APK」**，系统要求本人确认安装。

**Android 当前使用调试签名：** 如果新版 APK 与旧版签名不同，系统将拒绝覆盖安装；此时不要直接卸载以免丢数据。维护者应配置 GitHub Secrets 中的 `MIXED_API_ANDROID_DEBUG_KEYSTORE_B64`，让以后的 Android 构建共用私有签名密钥。你看到按钮不代表我们已经解决了旧版跨签名升级问题。

## Android 使用电脑的模型

Windows 在开始菜单打开「MIXED-API → 局域网模式」（绿色版运行 `Start-MIXED-API-LAN.cmd`）。从电脑运行 `ipconfig` 读取真实局域网 IPv4，并只在可信专用网络开放 8766。手机和电脑连接同一 Wi-Fi；手机 MIXED-API 后台启用 **「电脑 MIXED-API」**，填 `http://电脑IPv4:8766/v1` 和**电脑的客户端 Key**，保存即可。手机上的其他 AI 客户端仍使用 **手机本地** `http://127.0.0.1:8765/v1` + **手机自己的客户端 Key**，模型名保持 `codex/...` 等不额外加前缀。

手机 APK 不能原生运行桌面 Codex CLI、WorkBuddy 客户端和 Camoufox。电脑关机或断网，其模型就不可用。禁止把未加密的 8766 直接映射公网。

## 如果一键接入仍然失败

请在对应卡片点击「检查状态」，查看具体失败原因。配置及运行时都由 MIXED-API 保存到当前用户自己的配置目录；如网络阻断了 GitHub 下载或官方登录不可用，软件不能绕过这些限制。为保护凭证，上游许可证和各平台服务条款始终有效。

