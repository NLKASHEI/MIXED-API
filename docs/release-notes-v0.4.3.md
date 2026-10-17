# MIXED-API v0.4.3 — Windows 会话与快速V修复

- 快速V按 vertex2openai 源码补齐 Key 项目探测，保存时默认采用项目/global 完整路径；更换 Key 会重新探测。生成前遇到 HTTP429/503 默认最多重试2次，遵守 Retry-After；404、网络中断及已输出的流不会重放或改换模型。
- AIS／无头V点击“打开官方页面”后，在当前默认 Edge／Chrome 的同一标签页进入官方站点。软件等待本人完成登录、条款或验证后自动连接，不再要求返回点“连接会话”，也不要求手动发送初始化消息。
- AIS补齐页面验证函数的包装变体与早期挂钩；无头V使用经 APS2api 和 vertex-singbox 源码核对的匿名请求壳，不再要求登录后的项目页面先发出匿名模板。
- 后台补充重试次数、404解析区域，以及HTTP200正文内的GraphQL/RPC业务错误码。上游拒绝不能显示为成功；聊天、Cookie、Key及原始服务方异常不进日志。
- 修复配置读取时切换来源导致参数串用、临时弹窗/草稿使自动连接卡住，以及内置快速V切回已有HTTP服务时残留原生字段导致400。
- 移除实际返回404且未验证的 gemini-4-argon 内置候选；不会替换客户端明确指定的模型。

## 下载与升级

直接运行 **MIXED-API-Windows-x64.exe**；也可选择 Setup 安装器或 ZIP 便携包。更新前完全退出旧版，保留原配置文件。主程序为独立应用窗口，不弹CMD，不下载上游应用。

AIS／无头V首次仍需在当前浏览器加载EXE内置连接组件。已有旧组件的用户：启动新版并打开官方页面步骤，在 `edge://extensions`（Chrome 使用 `chrome://extensions`）刷新“MIXED 浏览器连接”，再刷新本机连接页。之后进入官方页面，由软件自动等待并连接。保持来源标签页打开；本人处理登录、条款和验证。

## 验证范围与使用提示

发布仅使用本标签成功的GitHub Actions产物，检查包括Go tests/race/vet、来源会话与扩展回归、实际HTTP配置流程、Windows构建和Android Gradle。Android继续暂停，本版不附APK。

快速V已实测过相同Key/模型/代理下global路径正常200/STOP，之后仍出现过429；不能保证global能消除配额不足或服务拥堵。请保持代理运行，后台查看真实错误。浏览器来源使用浏览器自身代理，MIXED的手动代理不修改Edge。

**AIS Build／Playground、无头V的本人浏览器初始化与真实生成仍待验收。** 本版按用户要求发布现有修复，不将模拟协议和CI通过写成所有Google来源已实测可用。问题继续在 [Issue #5](https://github.com/NLKASHEI/MIXED-API/issues/5) 跟踪。

[分步指引](https://github.com/NLKASHEI/MIXED-API/blob/v0.4.3/docs/quickstart-guided.md) · [上游核对](https://github.com/NLKASHEI/MIXED-API/blob/v0.4.3/docs/upstream-network-session-audit.md)
