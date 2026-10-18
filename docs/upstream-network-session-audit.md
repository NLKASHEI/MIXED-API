# Windows 代理与会话源码核对

2026-10-17：GitHub 默认分支最新 SHA 已逐项核对，与本次本地参考源码一致。

| 来源 | 固定提交 | 核对内容与本次处理 |
| --- | --- | --- |
| bad-woman/vertex2openai | 53a45b63bfef39e783edf8ea9c1e06800fc17496 | `app/http_options.py` 将 PROXY_URL 同时传给同步/异步客户端；`express_key_probe.py`、`model_loader.py` 也使用代理；`upstreams/express_sdk.py:resolve_express_model_path` 支持 Key 项目与区域路径。MIXED 共用 Go transport，流式与普通推理均过同一个代理选择器，新增可选项目/区域。 |
| Mag1cFall/AIStudio2API | 2cbe310ce6ebab093bf83e5078de8296f03770a9 | `internal/camoufoxnative/bidi.go` 的 snapshotHookExpression / takeProofExpression；`internal/aistudio/build.go` 的 Build RPC 与同模型兼容策略。MIXED 复用已有协议模块与当前官方页面函数发现，Windows 运输层改为内置扩展连接用户日常浏览器。 |
| DeconstructedCube/APS2api | 01328896c435b4b172b1d25f9fc04ff86b40df31 | `internal/transport/client.go`、`internal/vertex/stream.go` 将会话绑定到代理；`internal/transform/` 转换请求参数。MIXED 的无头V通过本人官方页面会话发送 GraphQL，使用浏览器网络；不把 Go 手动代理当作它的出口。未复制认证模拟和挑战绕过。 |
| zkygwxf-bot/opencode-proxy | 27626a9d192b988938bb3d5f0e4fa241af7ae309 | 默认 public 凭据、免费/停用清单、协议端点和 FreeTierError 的不同含义。MIXED 只展示清单与实时目录交集，不把“-free”后缀当作充分证据，不加入客户端身份伪造或收费回退。 |
| hawklithm/workbuddy2api | 4354f83bd95d6995ef944ef342f95668ac1290b8 | `src/codebuddy_proxy/codebuddy_client_demo.py` 的 token 刷新只处理授权；刷新不恢复积分。积分提示结合既有 WorkBuddy relay README 的计费说明。保持已可用的业务连接流程，补充逐步指引。 |

差异：快速V上游的 SSL_CERT_FILE 不等于关闭证书校验。当前 Windows 使用系统信任库，私有 CA 应先由用户按其组织要求导入系统信任库；MIXED 没有增加跳过 TLS 校验的开关，也不声称已支持上游所有环境变量。Google 浏览器请求代理由 Edge / Chrome 设置决定，浏览器不向应用披露实际节点。

WorkBuddy 上游没有确定的积分耗尽专属错误码、统一恢复周期或自动补积分实现。指引要求查看本人对应站点官方账号用量页；重新授权、刷新令牌、刷新模型均不能补积分。不能根据 HTTP 403/429 擅自判定余额为零。

验证：公开 HTTP 的普通/流式快速V请求经真实本机 CONNECT 代理和 TLS 测试服务成功；失败代理状态、DNS/超时、凭据不进日志、项目/区域路径有回归。浏览器桥 WebSocket 的来源/令牌校验、发送/取消/释放和扩展异步命令顺序有离线测试。个人 Google 登录及真实模型生成未代用户操作，不属于这些测试的结论。

许可：协议来源许可沿用 `docs/third-party/`；新引入 go-ieproxy 的 MIT 与 x/net、x/text 的 BSD 许可包含在 THIRD_PARTY_NOTICES.md。扩展桥为本项目实现，移除不再使用的 chromedp 及其依赖。

## v0.4.3 开发修复与实测边界

用户指定的 `D:\vertex2openai` 位于同一 53a45b6 提交。逐项核对 `express_key_probe.py`、`upstreams/express_sdk.py`、`api_helpers.py` 与 `config.py`：项目探测通过不存在的模型识别 Key 绑定项目，不消耗真实生成；完整模型路径在固定 aiplatform.googleapis.com 域名使用项目与区域；上游默认 global。MIXED 保存新 Key 时探测，明确填写 global，换 Key 清空旧项目绑定；失败不保存假项目。已有手动项目/区域继续保留。

快速V只有生成前明确 HTTP 429/503 才按同模型、同 Key 重试。MIXED 默认 2 次、总时限 300 秒，可在后台调整；不照搬上游 10 次默认值。遵守 Retry-After，取消立即停止，网络中断、404 和已经输出的流不重放。后台保留尝试次数、重试原因及从 Google 404 结构中提取的路由区域。移除未验证且本次实际返回 404 的 gemini-4-argon 候选，不擅自替换用户指定模型。

本机真实证据：相同 Key、模型及本机 HTTP 代理，默认 publisher 路由的 gemini-3.8-flash 流式请求返回 429；显式 Key 项目/global 路径有一次 HTTP 200，输出 OK 并正常 STOP。之后经当前运行 EXE 再请求仍出现 429。结论是完整路径与重试需补齐，不能声称 global 必然修复额度/拥堵，也不能把 429 一律归因代理或错误模型名。项目编号、Key、完整请求及配置不进入仓库。

AIS Build/Playground 与无头V：修复函数发现、页面加载时机与匿名请求壳；当前默认浏览器自动等待和连接见 ADR-0008。用户明确暂不刷新扩展或操作登录，这两路真实会话初始化和生成继续未验收。不会把旧版本 EXE 的运行结果当作本轮源码构建结果，不下载新的发布二进制到用户电脑。
