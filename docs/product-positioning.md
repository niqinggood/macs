# macs 产品定位与比较

[简体中文](product-positioning.md) | [English](product-positioning.en.md) · [返回项目介绍](../README.md)

核对日期：2026-10-16。以下依据 macs 当前源码与其他产品的官方公开资料，区分产品取向和已经证明的效果；没有进行统一条件下的竞品实测。

## 核心定位

**macs 是一套可自行部署、可按业务改造的 Agent 工作系统，将自然语言目标、真实执行、可恢复流程、结果验收和经验复用组织在同一运行时中。**

它的价值需要通过连续工作体现。例如：读取业务数据，判断缺失信息，按企业模板生成报告，检查字段与文件，再把任务状态和交付物交回业务系统。后续批次可以复用已核对的模板规则与流程；用户偏好则通过记忆管理。

联网、编程、RAG、MCP 和多 Agent 都是完成工作的能力。更有区分度的产品表达应说明：这些能力怎样共同完成一个任务，哪些结果能够核对，以及成功方法如何再次使用。

本仓库中的 **openkitty 是 macs 的小名和历史兼容标识**。下文的 **OpenClaw 是另一个独立项目**；若比较的是 macs 的旧版本，应按具体版本记录另行比较。

## 与其他产品怎样比较

| 产品 | 官方资料中的方向 | macs 可以强调的已有特点 | 需要承认的边界 |
| --- | --- | --- | --- |
| OpenClaw | 本机或团队部署的助手、消息渠道、Gateway，以及模型和工具扩展 | Go 程序内嵌工作台，组合业务 Pipeline、AgentOS 持久任务、DOCX 模板方案和流程复用 | OpenClaw 同样支持自行部署、记忆和扩展；这些不能宣称为 macs 独有。[官方仓库](https://github.com/openclaw/openclaw) |
| WorkBuddy | 办公工作台、专家与团队协作、生态连接；企业版还提供 OpenAPI 和私有化方案 | 在许可证允许范围内检查和改造实现，自己组合模型、工具、模板引擎和业务任务契约 | 不能说 WorkBuddy 只会聊天，也不能说它没有企业接入或私有化。[产品介绍](https://cloud.tencent.com/product/workbuddy)、[企业版](https://cloud.tencent.com/product/workbuddy-enterprise) |
| Hermes Agent | 长期记忆、历史检索、从经验创建和改进技能 | 将记忆、业务流程契约、文档模板方案及带成功断言的界面流程复用组合使用 | Hermes 的经验学习是其公开重点；没有证据证明 macs 的记忆或学习效果更好。[官方仓库](https://github.com/NousResearch/hermes-agent) |
| Codex | 可复用 Agent 执行环境，以及 CLI、SDK、app-server 等集成入口 | 提供包含工作台、文档引擎、记忆、消息渠道与业务流程的整套应用装配 | Codex 也能嵌入业务产品，不能以“不能接企业系统”或“只会编码”作比较。[OpenAI 官方说明](https://developers.openai.com/blog/codex-as-a-platform) |

这是依据资料作出的定位判断。macs 适合愿意自行部署、关心业务实现可改造性，并需要把任务执行接到已有系统的用户；是否更适合某个团队，应通过实际任务和集成要求判断。

## 用哪些代码支撑优势

| 可以说明的特点 | 已有机制 | 源码与说明 |
| --- | --- | --- |
| 个人入口与业务 API 共用执行能力 | Web、AgentOS、Pipeline 和 AI Employee 由宿主装配到核心 Agent | [`internal/app/`](../internal/app/)、[完整请求链路](../internal/README.md) |
| 任务过程有持久状态 | 任务记录、工具回执、租约、会话及步骤检查点 | [`internal/agentos/`](../internal/agentos/)、[`internal/session/`](../internal/session/)、[运行可靠性](architecture/runtime-reliability.md) |
| 对成熟步骤约束输入与交付 | Pipeline 输入输出契约、返工和恢复 | [`internal/pipeline/`](../internal/pipeline/)、[Pipeline 说明](architecture/PIPELINE_GUIDE.md) |
| 模型理解与确定性填写协同 | Agent 提出映射，DOCX 引擎检查结构、填写并验证，按模板指纹复用方案 | [`internal/tools/content/`](../internal/tools/content/)、[文档自动化](architecture/document-automation.md) |
| 成功操作可编译成复用流程 | 从成功 `computer` Session 提取参数、动作和断言，重放仍经过同一执行入口 | [`internal/workflowlearn/compiler.go`](../internal/workflowlearn/compiler.go)、[界面自动化](architecture/computer-use.md) |
| 记忆可维护 | 选择性召回、更正、遗忘及来源管理 | [`internal/memory/`](../internal/memory/)、[记忆服务](architecture/memory-service.md) |
| 主程序集中交付 | Go 二进制内嵌原生 Web、内置 Skills 和配置模板，本地可用 SQLite | [`assets.go`](../assets.go)、[启动说明](../README.md#快速开始) |

这些代码说明机制存在，并不直接证明任务成功率、稳定性或运行成本领先。

## 当前宣传应保留的准确性

- 记忆、多 Agent、MCP、Skills、联网和自部署在其他产品中也存在，单项功能数量不等于完成质量。
- `workflow_learner` 当前针对成功的 `computer` 会话；不能描述为所有对话、代码任务和业务流程都会自动变成高质量技能。
- 工具验收只对执行过的检查及其证据负责；文件存在或命令退出成功不代表全部业务目标满足。
- 进程恢复遵循检查点与工具回执边界。结果不确定的写操作不能自动重放；SSE 也不能替代持久任务快照。
- Go 主程序内嵌资源，但浏览器、模型、Office/PDF 转换和外部平台仍有运行依赖。当前三平台也没有统一完成双击即用的桌面打包。
- 当前仓库仍有模块化迁移留下的测试问题，不能用主程序构建成功代替全仓测试通过。
- macs 使用源码可见许可证；商业使用与分发范围以 [`LICENSE`](../LICENSE) 为准。不得写成无条件商用或 OSI 开源许可。

## 接下来怎样证明优势

优先围绕已有[业务评测基线](../testdata/blackbox/AGENT_BASELINE.md)测三类任务：已有代码项目修改并验收、固定模板的连续批次交付、业务系统提交并跟踪长任务。统一输入、模型条件、完成标准和重试预算，记录成功率、人工介入次数、重复解释次数、耗时与实际费用。

记忆与流程复用还应比较首次与后续批次：是否减少重复说明，旧偏好是否能被新指令覆盖，遗忘后是否停止召回，复用流程在输入变化时能否维持正确结果。只有取得这些证据后，才适合提出相应的领先性结论。

推荐的项目介绍：**macs，让 Agent 完成可核对的工作，并把经验沉淀为可复用的能力。**
