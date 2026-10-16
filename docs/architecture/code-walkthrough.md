# macs 核心代码导读

[简体中文](code-walkthrough.md) | [English](code-walkthrough.en.md) · [项目介绍](../../README.md) · [完整请求链路](../../internal/README.md)

这份导读先跟踪一次真实任务，再说明任务恢复、经验沉淀和文档交付。符号与路径以当前源码为准；`internal/app` 仍包含迁移中的宿主实现。

## 1. 先建立三层关系

```text
cmd/openkitty：可执行入口和版本传递
    ↓
internal/app：配置、生命周期、Gateway、Agent 与数据库的装配
    ↓
internal/* 和 pkg/*：领域规则、工具合同、具体能力和可复用组件
```

根目录 [`assets.go`](../../assets.go) 是独立的资源包，通过 `go:embed` 打包 Web、内置技能和配置模板。业务逻辑不应放回根包。稳定领域包也不应反向依赖 `internal/app`。

## 2. 程序启动：从 main 读到可用服务

从 [`cmd/openkitty/main.go`](../../cmd/openkitty/main.go) 开始。它将发布版本传给 `app.CurrentVersion`，然后调用 `app.Main()`。

主要宿主逻辑位于 [`internal/app/mac.go`](../../internal/app/mac.go)：

```text
Main → run → 配置与工作目录处理 → NewBot → ClawBot.Run
```

`FindConfig` 查找显式配置、项目配置和用户配置；首次运行可从内嵌模板生成默认配置。`NewBot` 负责装配，`ClawBot.Run` 管理已配置服务的运行。阅读时先查函数，不必顺序读完整个大文件。

## 3. 一条 Web Agent 对话怎样进入执行

[`internal/app/gateway.go`](../../internal/app/gateway.go) 中的 `GatewayService.handleChat` 接收 `POST /api/chat`，处理请求并进入相应任务路径。Agent 任务会进入 `processTaskWithSession`，将工作区、会话、执行身份、取消信号以及适用的恢复状态绑定到执行上下文。

```text
Web 请求
  → Gateway：解析请求、选择任务路径
  → processTaskWithSession：建立执行上下文
  → LLMAgent.Generate：驱动模型与工具
  → 结果校验、消息与任务持久化
  → JSON 或 SSE
```

这是 Agent 模式的阅读路线。简单问答在 [`simple_chat.go`](../../internal/app/simple_chat.go) 中走单独的模型调用路径；直接媒体生成也有自己的任务快照。不能把每次界面发送都视为 Agent 工具循环。

## 4. Agent 循环：模型怎样真正做事

核心入口是 [`LLMAgent.Generate`](../../internal/app/mac.go)。开始时会获取执行租约，并在存在合法恢复状态时还原回合检查点。随后建立当前回合的工具激活、记忆、技能和模型上下文。

阅读这个函数时，跟踪以下状态变化：

1. 当前任务、消息和计划如何进入模型上下文。
2. 模型输出普通文本还是工具调用。
3. 工具调用如何交给 Registry 与 Middleware。
4. 工具回执如何返回消息链，触发下一次推理。
5. 何时继续、停止、取消、报错或交付结果。
6. 哪些状态在工具前后保存，哪些结果会进入会话或适用的长期记忆。

[`internal/agentloop/`](../../internal/agentloop/) 保存回合状态、租约和恢复等中立规则；[`internal/modelruntime/`](../../internal/modelruntime/) 管理模型调用相关策略。两者的职责与宿主装配不同。

结果验收入口之一是 [`goal_verify`](../../internal/tools/testing/goal_verify.go)，提供 `PASS / FAIL / PARTIAL` 以及具体检查证据。它对选定检查负责，不能仅凭某个文件存在就证明整个需求完成。详见[目标验收](goal-verification.md)。

## 5. Tool、Skill 和 Pipeline 怎样配合

一次具体动作遵循 [`toolcontract.Tool`](../../internal/toolcontract/tool.go)：

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, params map[string]interface{}) (string, error)
}
```

[`internal/toolregistry/registry.go`](../../internal/toolregistry/registry.go) 管理注册和执行，`tool_detail` 负责按需发现；[`activation.go`](../../internal/toolregistry/activation.go) 管理当前回合激活。具体实现位于 `internal/tools/<category>/`，执行策略位于 [`internal/middleware/`](../../internal/middleware/)。

Skill 在 [`internal/skills/`](../../internal/skills/) 中加载和激活操作说明，显式 Skill 的宿主路径在 [`skill_run.go`](../../internal/app/skill_run.go)。它提供做事方法与约束，不会因为激活 Skill 就创建另一个 Agent。

Pipeline 把工作组织成多步骤流程。[`internal/app/pipeline.go`](../../internal/app/pipeline.go) 负责跨域调度，领域合同在 [`internal/pipeline/`](../../internal/pipeline/)，包括步骤检查点、输出契约、来源契约与返工规则。AI Employee 则提供业务身份、路由及流程绑定，入口见 [`ai_employee.go`](../../internal/app/ai_employee.go)。

## 6. AgentOS：让执行进入业务系统

从 [`internal/app/agent_os_api.go`](../../internal/app/agent_os_api.go) 跟踪 HTTP 接入与宿主协调，再看 [`internal/agentos/`](../../internal/agentos/) 的协议和持久化模型。

```text
业务系统提交新任务
  → 202 + task_id + links
  → 持久队列、租约与执行宿主
  → 核心 Agent / Pipeline
  → 最终任务快照与交付物
```

这里复用核心执行能力。外部系统通过持久快照读取最终事实，SSE 提供近期进度与有界重放；重放窗口失效后需要重新查询快照。

恢复还要结合 [`internal/session/checkpoint.go`](../../internal/session/checkpoint.go)、[`recovery.go`](../../internal/session/recovery.go)、[`internal/app/recovery_host.go`](../../internal/app/recovery_host.go) 和工具回执阅读。**模型或工具的稳定边界可以恢复；写入结果不确定时不能自动重放。** 改动这条链路时应同时考虑幂等、取消、租约过期和终态一致性。

完整契约见 [AgentOS API](../agent-os-api.md) 和[运行可靠性](runtime-reliability.md)。

## 7. 记忆、会话与知识库为何分开

| 组件 | 主要问题 | 阅读入口 |
| --- | --- | --- |
| memory | 长期保留什么、当前召回什么、怎样更正或遗忘 | [`internal/memory/`](../../internal/memory/)、[记忆服务](memory-service.md) |
| session | 执行过程怎样保存，回合如何恢复 | [`internal/session/`](../../internal/session/) |
| conversation | 当前对话的上下文与压缩 | [`internal/conversation/`](../../internal/conversation/) |
| RAG | 从导入资料中检索哪些内容 | [`internal/ragmodel/`](../../internal/ragmodel/)、[`internal/rag/`](../../internal/rag/)、[`internal/tools/knowledge/`](../../internal/tools/knowledge/) |

例如，用户长期要求报告附参考来源，应由记忆管理；某次工具调用的回执属于执行历史；一份规范文件属于知识资料。三者可能共同参与当前任务，但有不同的生命周期。遗忘一个条目也不等于删除所有原始消息和派生资料。

## 8. 两个体现复用能力的实现

**文档模板方案。** 从 [`internal/tools/content/extract.go`](../../internal/tools/content/extract.go) 进入检查与填写。模型生成声明式 `field_mapping`、`table_cells`、`repeat_rows`；通用 OOXML 引擎执行。可复用规则由 [`docx_template_profile.go`](../../internal/tools/content/docx_template_profile.go) 按结构指纹保存。Web 宿主 [`document_automation_api.go`](../../internal/app/document_automation_api.go) 复用同一引擎。详见[文档自动化](document-automation.md)。

**界面流程学习。** [`internal/tools/computer/`](../../internal/tools/computer/) 组织观察、操作与验证，并复用浏览器和桌面后端。成功会话由 [`internal/workflowlearn/compiler.go`](../../internal/workflowlearn/compiler.go) 编译为带参数和断言的方案；[`workflow_learner.go`](../../internal/tools/skills/workflow_learner.go) 适配 Agent 工具。重放仍调用 `computer`，不会绕开统一执行入口。详见[界面自动化](computer-use.md)。

这两种复用都需要检查实际结果。界面布局、定位方式或模板结构变化后，已有方案可能需要重新核验。

## 9. 第一次修改时怎样选入口

| 想改什么 | 从哪里开始 |
| --- | --- |
| 新增一种工具能力 | 相邻 `internal/tools/<category>/` 实现、Tool 合同、注册与发现、执行策略及相邻测试 |
| 调整记忆召回或遗忘 | `internal/memory/` 及对应宿主调用，区分长期条目与历史记录 |
| 修改任务取消或恢复 | AgentOS、会话检查点、恢复宿主、工具回执及终态测试 |
| 扩展 DOCX 模板支持 | 现有结构检查、稳定地址、声明式映射与模板夹具，避免企业名称特判 |
| 修改 Web 展示 | `web/` 中现有 HTML/CSS/JS，遵守已有设计，检查改动脚本语法后重建嵌入资源 |

在仓库根目录定位符号：

```bash
rg -n 'func .*handleChat|func .*processTaskWithSession' internal/app/gateway.go
rg -n 'func .*Generate|WithNewTurnActivation' internal/app/mac.go
rg -n 'type Tool interface|func .*Execute' internal/toolcontract internal/toolregistry
```

动手前查看 `git status` 和相关 diff；改动后运行最接近的测试、`git diff --check` 和必要的主程序构建。已知全仓测试迁移问题见 [`AGENTS.md`](../../AGENTS.md)，不能把既有失败与新回归混报。包依赖约定见 [Go 模块化边界](go-modularization.md)。
