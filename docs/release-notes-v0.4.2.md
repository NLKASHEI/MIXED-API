# MIXED-API v0.4.2 — Windows 代理与当前浏览器修复

- 快速V默认读取 Windows 系统代理，兼容 PROXY_URL / HTTP(S)代理环境变量，并可在设置中手动填写 HTTP/HTTPS/SOCKS5。启动后切换系统代理也会刷新；代理失败不偷偷直连。配置步骤新增可选项目 ID 与区域，解决默认区域不适配的场景。
- AIS／无头V改为连接当前 Edge／Chrome 的普通账号资料，不再另建独立浏览器 profile 或启动调试浏览器。首次使用按打开的连接页，在浏览器加载 EXE 自带的连接扩展，再刷新连接页。扩展无需另行下载；主程序仍是独立 EXE 应用窗口。
- 后台请求详情增加请求编号、模型、实际 Go 出口、连接地址、失败阶段、代理 CONNECT 状态、上游 HTTP 状态与业务码、Retry-After，并支持复制。浏览器请求明确显示由浏览器管理网络，不冒充 Go 的出口。不记录聊天、Cookie 或 Key。
- OpenCode统一模型列表只显示上游免费清单与实时目录的交集；不展示付费、停用和费用不明的模型。免费请求使用 public 凭据，不自动切换付费模型。
- 每个来源的配置步骤补入代理、权限和额度注意事项。WorkBuddy特别说明：积分耗尽应查看对应官方账号用量和恢复规则；重新授权、刷新令牌、刷新模型均不会补积分。

更新前完全退出旧版。下载 `MIXED-API-Windows-x64.exe` 直接运行，或使用 Setup 安装；无需上游程序。AIS／无头V首次连接需要本人在当前浏览器加载一次内置扩展，步骤会显示扩展文件夹。保持来源标签页打开，登录、条款和验证由本人完成。

Go tests/race/vet、扩展命令顺序与会话回归、共享界面、Windows构建、Android Gradle检查通过后由 Actions 发布。额外本机测试已确认真实Chromium扩展与Go公开API的合成生成链路；这不等于本人Google账号推理验收。

仍需注意：Google实际生成由本人账号验证；浏览器用自己的代理设置，应用手动代理不会修改Edge。OpenCode免费层可能返回FreeTierError（服务方限制客户端），会明确报错，不保证所有免费候选当前可用。Android继续暂停，本次不附APK。

详细配置见[分步指引](https://github.com/NLKASHEI/MIXED-API/blob/feat/integrated-desktop-core/docs/quickstart-guided.md)，源码核对见[上游审计](https://github.com/NLKASHEI/MIXED-API/blob/feat/integrated-desktop-core/docs/upstream-network-session-audit.md)。
