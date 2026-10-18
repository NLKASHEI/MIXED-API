# v0.4.5 上游核对

本轮聚焦Windows核心；不下载运行上游应用。

| 来源 | 固定提交/文件 | 本轮采用 |
| --- | --- | --- |
| hawklithm/workbuddy2api | `4354f83bd95d6995ef944ef342f95668ac1290b8`，backend_profile.py、models_config.global.json | MIT；国内/CodeBuddy国际固定站点、鉴权路径、站点模型候选、令牌domain回退。保留既有许可原文。 |
| 本地已核对WorkBuddy Node relay | sites.js | WorkBuddy AI国际站 `www.workbuddy.ai` 及 `workbuddy-ai` 产品标识；与CodeBuddy国际站区分。仅独立实现协议事实，未引入指纹伪装或自动刷任务。 |
| bad-woman/vertex2openai | `53a45b63bfef39e783edf8ea9c1e06800fc17496`；用户本地D:\vertex2openai的app/resumable.py、cookie_proxy.py | 按缓冲序号、Last-Event-ID、轮询和幂等请求的行为独立实现Go模块，不运行Python；区分明确promptFeedback拦截与真正断流。 |
| DeconstructedCube/aistudio-api | `9bd5a20f25bbfae60c84b59b6a9f167b85c7d1a3`，gateway/model_discovery.py | 参考官方ListModels调用、公开应用Key与目录结构，在当前官方页面独立实现已有账号探测。未找到可核验LICENSE，未复制源代码。 |
| Google官方模型文档 | [Gemini 4 Argon](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/gemini/4-argon)、[开发指南](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/guides/gemini-4-argon) | 确认模型ID、v1beta1与思考参数；恢复目录，不因单次404删除模型。长解码continuation token尚未纳入。 |

对应实现与回归：workbuddy_region.go / workbuddy_region_test.go、express_stream_retry.go / express_reliability_test.go、resumable.go / resumable_test.go、runtime.js / test-google-session.mjs。模型目录是候选，并非真实账号权限清单；已有登录、协议就绪与成功生成分别验收。不存在“国际站保证不封号”的验证结论。
