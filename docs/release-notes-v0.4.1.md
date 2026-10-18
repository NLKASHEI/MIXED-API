# MIXED-API v0.4.1 — Windows 修复版

本版优先修复电脑版的来源会话和反代协议，暂停 APK 发布。

- AIS / 无头V的授权页面改为系统默认 Edge 或 Chrome，使用 MIXED 专用资料目录；主程序仍是独立 EXE 窗口。
- 配置中直接连接会话，不再要求手动发网页测试消息。配置内的“测试对话”经过实际 API 请求。
- 按上游源码补齐 Build 单次调用兼容处理、无头V函数与结构化输出的 GraphQL 参数格式；服务方错误不再被当作成功完成。
- 修复 AIS 准备中取消后的页面复用问题，恢复输入框内容。
- OpenCode 标明免费无头候选、Key 付费、停用及未知模型；补齐匿名鉴权，缺 Key 的付费模型和服务方免费权限限制会明确报错。
- 新版启动时拒绝误连仍运行的旧服务，启动失败显示窗口提示；修复窗口初始化期间关闭的问题。

下载 `MIXED-API-Windows-x64.exe` 直接运行，或使用 `MIXED-API-Windows-x64-Setup.exe` 安装。更新前完全退出旧版。无需下载上游程序。

本版通过 Go tests/race/vet、会话脚本与共享界面回归、Windows 构建和 Android Gradle 构建/测试/Lint 后发布。Android Java 尚未同步本轮协议增量，本版不附 APK。

本人 Google 账号的真实生成仍未验收，不能据此保证 AIS / 无头V已经可用。OpenCode 免费候选不代表当前可用权限，本机匿名 big-pickle 请求曾收到 403 FreeTierError。保留 [Issue #5](https://github.com/NLKASHEI/MIXED-API/issues/5) 跟进实际使用反馈。
