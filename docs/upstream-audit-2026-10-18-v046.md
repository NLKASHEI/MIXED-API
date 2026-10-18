# v0.4.6 上游核对与采用范围

| 来源 | 固定提交 | 核对与采用 |
| --- | --- | --- |
| hawklithm/workbuddy2api | `4354f83bd95d6995ef944ef342f95668ac1290b8` | 与v0.4.5相同。国内静态目录里的DeepSeek V4条目并非本轮国际站V4.1免费活动，不混用。 |
| dingminhua/dsh-connect-workbuddy | `9f09263e0347adc167edc9b5ccc7d1be66d4f4b6` | MIT；参考src/upstream.ts的固定官方/v3/config目录、站点产品渠道、models/agents结构、credits数值，以及src/catalog.ts的国际DeepSeek V4.1零倍率记录。独立实现Go读取与费用分类，保留MIXED身份兼容标记；不导入本机凭据扫描、自动账号选择或整个DSH应用。许可另存。 |
| Mag1cFall/AIStudio2API | `a784c5193b3ba5c4894d536b2f4cd73e87f99475` | MIT；核对internal/camoufoxnative/worker.go在初始化前安装网络级拦截的行为，独立实现受控标签DNR规则。核对Build原生JSON Schema及Playground本地引用兼容；保留Build/Playground明确路由，不采用自动换账号重发。 |
| DeconstructedCube/aistudio-api | `9bd5a20f25bbfae60c84b59b6a9f167b85c7d1a3`（master） | 与v0.4.5相同；其BotGuard服务捕获、snapshot函数发现继续作为协议材料。没有可核验LICENSE，不复制实现。 |

Build上游新增的`-build`写代码助手采用CodeAssistantOffline任务、临时应用及附件上传，是另一套应用生成流程；本轮没有把它冒充已支持的ProxyUnaryCall/ProxyStreamedCall模型。当前MIXED的`aistudio/build/*`仍明确表示原生Gemini API代理。本轮采用现有反代链可验证的改进，不等同于导入上游全部产品能力。

Playground本地引用只在能够保留约束时展开；循环或冲突引用明确报错，未采用上游的开放节点退化。完整JSON Schema在Build原生字段传递。[Google FunctionDeclaration](https://ai.google.dev/api/generate-content#FunctionDeclaration)区分parameters和parametersJsonSchema；不把完整JSON Schema放进受限OpenAPI字段。

无头V匿名链的生成请求继续credentials:omit，不使用OAuth或Google项目账号。官方验证由官方SDK完成，未复制APS2api的anchor/reload模拟。网络、匿名验证、连接就绪和真实生成分别报告。
