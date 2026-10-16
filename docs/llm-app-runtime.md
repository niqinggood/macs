# MACS `llm_app` 托管运行时

`llm_app` 是 MACS（麦卡斯）的轻量 AI 应用执行器。它适合把明确、重复、可验证的模型工作从通用 Agent 主循环中拆出来执行，同时仍由 MACS 负责持久化、取消、恢复、预算、工具执行和审计。

它不是另一套权限系统，也不是 AgentOS 的替代协议：外部系统仍统一通过 AgentOS 异步任务 API 调用 MACS；`llm_app` 作为同一 Agent 运行时里的工具执行具体 AI 应用。

## 已支持的工作流

- `prompt`：单输入、单模型任务。
- `batch`：有限列表的并发模型任务；模型循环仍开放时可追加条目。
- `pdf_pages`：确定性提取 PDF 页面和表格，再按页执行模型任务。
- `graph`：最多 64 个节点的有向无环图，支持 `source`、`model`、`tool`、`validate`、`reduce`、`sink`。

当前仍是有限任务运行时，不包含麦克风、目录、消息队列或网络监听器等永久 `service` source。此类常驻接入应由渠道、AgentOS 调度器或专用适配器产生有限任务，不能把一个无法结束的监听循环塞进 `llm_app` worker。

## 启动、幂等与父子生命周期

`start` 立即返回 `run_id`。调用方应为可重试提交设置稳定的 `idempotency_key`：

```json
{
  "action": "start",
  "name": "invoice-review",
  "input": "review this payload",
  "idempotency_key": "invoice-2026-0042-v1",
  "lifecycle": "detached"
}
```

- 同一身份、同一 `idempotency_key`、同一应用版本和同一输入会返回原 `run_id`，响应中的 `idempotent_replay` 为 `true`。
- 同一 key 搭配不同输入、应用版本或 lifecycle 会被明确拒绝，不能悄悄变成第二次执行。
- 磁盘只保存 key 的 SHA-256 摘要，不保存调用方原始 key。
- `detached` 是兼容默认值：它不跟随提交请求结束，适合后台继续执行的有限批任务；它仍受显式 stop 和运行总时长预算约束。
- `attached` 需要显式选择：父任务取消会取消正在执行的模型/工具调用，并把运行收敛为 `stopped`。
- 进程重启后不存在可继续绑定的旧内存 context，恢复 worker 会按持久任务继续执行，并恢复最小 owner 边界；不会恢复 control-plane 或审批权限。
- run 同时持久化原执行 Agent 的 `executor_binding_id`。同一 workspace 有多个 Agent 时，只允许匹配的 Agent 模型与 Tool Registry 接管；对应运行时尚未注册时任务保持待恢复，不会借用“最后注册的 Agent”。

## 运行总预算与真实用量

预算属于整个 run，而不是某一次 HTTP 模型请求：

```json
{
  "budget": {
    "max_model_calls": 20,
    "max_total_tokens": 100000,
    "max_duration_seconds": 1800,
    "max_cost_usd": 2.5,
    "input_cost_per_million_tokens": 1.0,
    "output_cost_per_million_tokens": 4.0
  }
}
```

- 每次调用前会按估算输入 token 和最大输出 token 预留额度，避免并发 worker 一起冲破硬上限。
- Provider 返回 `prompt_tokens`、`completion_tokens` 和 request ID 时，MACS 会保存实际值；不返回时保留有界估算。
- `usage.unknown_model_calls` 表示进程退出时已经发出、但未能确认最终回执的模型调用。恢复可以安全重做计算，但调用方应把这部分视为可能重复计费。
- 成本上限只有在应用显式配置输入/输出单价时才启用；MACS 不内置可能过期的供应商价格表。
- 预算不足时不会再发起下一次 Provider 调用，run 以明确的 budget error 失败。
- `max_model_calls` 统计 MACS 发起的逻辑模型调用；底层 `modelruntime` 针对瞬时 HTTP 故障的内部重试不拆成多个 run call。若部署要求对每次 Provider HTTP 尝试做绝对计费封顶，应在模型网关侧关闭/限制重试并设置供应商额度；MACS 仍保存最终回执的实际 usage，并把无确定回执的调用计入 `unknown_model_calls`。

## 完整 JSON Schema 输出合同

应用、运行计划和图节点的 `output_schema` 在保存前会编译检查，运行结果使用完整 JSON Schema 校验，而不只是检查根类型和 `required`。嵌套对象、数组、`const`、数值范围和 `additionalProperties` 等约束均由同一校验器处理。

有 schema 的模型输出会同时保存：

- `output`：Provider 返回的原始 JSON 文本；
- `value`：解析后的结构化 JSON 值，供程序直接消费。

校验失败可按 `health.validation_retries` 请求模型修复；每次修复仍计入 run 总调用、token、时长和成本预算。

## 最小应用图

下面的图把输入交给模型，随后调用已注册工具，校验结果并输出：

```json
{
  "name": "classify-and-lookup",
  "workflow": "graph",
  "graph": {
    "output_node": "result",
    "nodes": [
      {"id": "source", "type": "source", "source": "input"},
      {
        "id": "classify",
        "type": "model",
        "depends_on": ["source"],
        "prompt_template": "Classify: {{value}}",
        "max_tokens": 256,
        "output_schema": {
          "type": "object",
          "required": ["category"],
          "properties": {"category": {"type": "string"}}
        }
      },
      {
        "id": "lookup",
        "type": "tool",
        "depends_on": ["classify"],
        "tool": "knowledge_search",
        "arguments": {"query": "{{node.classify}}"}
      },
      {
        "id": "result",
        "type": "sink",
        "depends_on": ["lookup"]
      }
    ]
  }
}
```

节点规则：

| 节点 | 作用 |
| --- | --- |
| `source` | 从 start 请求读取 `input`、`items` 或 workspace 内受路径边界保护的 `file` |
| `model` | 使用小提示词调用已配置模型，可独立声明 JSON Schema |
| `tool` | 调用现有 Tool Registry；Schema、Middleware、现有权限和结果判定全部沿用，不存在图专用旁路 |
| `validate` | 对上游结构化值执行 JSON Schema 校验 |
| `reduce` | 确定性执行 `collect`、`concat`、`merge_objects` 或 `first` |
| `sink` | 选择并可再次校验最终输出；结果写入 `result.output` |

模板支持 `{{input}}`、当前依赖值 `{{value}}` 和指定节点值 `{{node.<id>}}`。若一个字符串完全等于 `{{value}}` 或 `{{node.<id>}}`，工具参数会保留对象/数组类型，而不是强制转成字符串。

## 崩溃恢复与 effect 语义

每个模型条目和图节点都采用 `pending -> effect_pending -> settled`：

1. 在模型或工具调用前，先原子保存 effect ID、输入快照、计划版本和 replay policy。
2. 只有拿到明确结果并完成校验后，才原子写入 `settled`。
3. `result.json` 从 settled checkpoint 聚合；`results.jsonl` 只是观察日志，不再充当唯一事实来源。

恢复策略：

| 中断点 | 恢复行为 |
| --- | --- |
| 已 `settled` 的条目/节点 | 直接复用，不重复执行 |
| 未调用外部能力的 `pending` | 从保存的计划版本继续 |
| 模型调用或只读工具停在 `effect_pending` | 标为未知 effect 后安全重做；模型调用增加 `unknown_model_calls` |
| 写工具停在 `effect_pending` | 返回 `effect outcome is unknown`，禁止自动重放，等待人工核对后创建替代运行 |

这不是对任意外部系统宣称绝对 exactly-once。只有外部系统本身也接受稳定 call ID/幂等键并返回可核对回执时，才能进一步缩小“已执行但本地未落盘”的不确定窗口。

## Worker 租约与 fencing

每个非终态 run 只有一个带随机 token 的 worker lease。heartbeat 会续租；重启实例只有在旧租约过期后才接管。所有 checkpoint、用量、事件和终态提交都在同一文件锁临界区内完成 token 核对与写盘，避免“先检查、后写入”的竞态；旧 worker 即使迟到返回，也不能覆盖新 worker 的结果。自动恢复扫描遇到未过期旧租约时会按到期时间重试，不要求人工先查询状态。

文件型运行目录不承担跨实例命令转发：若请求落到非 lease owner 实例，状态和事件仍可读，但 pause/replan/stop 等实时控制会明确返回 `run is owned by another worker instance`，不能伪装成已生效。多 Pod 的统一入口仍应使用 AgentOS 队列与网关路由。

## 运行文件

运行状态仍保存在兼容目录 `.openkitty/` 下；这个路径是历史接口，不代表对外品牌名：

```text
.openkitty/llm_apps/
  definitions/<app>.json
  idempotency/<key_digest>.json
  runs/<run_id>/
    definition.json       frozen application definition
    plan.json             latest mutable execution plan
    plans/                immutable plan revision history
    request.json          initial private input
    inputs.jsonl          appended batch items, when present
    items/*.json          authoritative item checkpoints
    nodes/*.json          authoritative graph-node checkpoints
    commands/*.json       command intent and settled receipt
    commands.jsonl        applied-command audit stream
    events.jsonl          ordered runtime events
    results.jsonl         item observation stream
    lease.json            current worker lease (nonterminal only)
    result.json           terminal aggregate
    run.json              durable status, usage, budget and health
```

## 运行中控制

应用定义在 run 创建后冻结；prompt/batch/PDF 的可变部分位于带 revision/digest 的运行计划。`replan` 只影响尚未 claim 的条目，并可用 `expected_plan_revision` 做乐观并发检查。graph 的拓扑和节点合同在整个 run 内保持不可变，需要改变时应保存新的应用 revision；`enqueue_items` 仅适用于仍开放队列的 `batch`。

每条 command 在改变运行前先保存 durable intent。相同 `command_id` 和相同 payload 返回原 receipt；相同 ID 搭配不同 payload 会被拒绝。若进程恰好在“动作可能完成、receipt 尚未 settled”的窗口退出，恢复返回 `command outcome is unknown`，不会把命令盲目执行第二次。

事件查询继续使用 `after_seq`、`limit`、`wait_seconds` 和可选 `event_types`。除既有 run/item/plan/health 事件外，图工作流还发出：

- `graph.node_started`
- `graph.node_completed`
- `graph.node_failed`
- `run.recovered`

事件不包含完整输入和输出；敏感数据保留在 workspace 私有运行文件中。

## 明确边界

- graph tool 节点不能增加文件、网络、密钥或工具权限，只能经过当前 Agent 已有的 Registry 和 Middleware。
- 模型名仍必须存在于 MACS 配置；`llm_app` 不做静默模型降级。
- owner 隔离同样覆盖状态、事件、命令、健康决策和结果。
- `pdf_pages` 当前只有文本/表格提取，不隐式启用 OCR；扫描 PDF 应先走已有 OCR/文档工具或增加显式适配节点。
- graph 目前按拓扑顺序确定性执行；批工作流仍负责高吞吐并发。后续只有在保持相同 checkpoint、预算和 effect 语义时，才应增加图分支并行。
