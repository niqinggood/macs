# AgentOS 异步任务 API

其他服务通过 AgentOS Data API 向 MACS 提交持久化异步任务，再查询状态、
订阅 SSE、追加运行中指令、处理审批或取消任务，不需要维持原始 HTTP 连接。完整机器可读契约见
[`openapi-agentos.yaml`](./openapi-agentos.yaml)，服务身份和岗位配置见
[`enterprise-agent-service.md`](./enterprise-agent-service.md)。

首次部署、启动命令、健康检查和完整调用示例见
[`agentos-quickstart.md`](./agentos-quickstart.md)；Web「帮助 → 接入AgentOS（企业智能后台）」按通用企业业务后端说明模式、部署、启动、调试和调用。企业自部署由自己的业务系统调用本页原生接口；示例只使用本机或占位地址。

## 1. 数据面监听与认证

生产环境建议配置独立数据面监听器：

```yaml
gateway:
  listen_addr: 0.0.0.0:8080
  agent_os_listen_addr: 0.0.0.0:8081
```

也可通过 `OPENKITTY_AGENTOS_LISTEN_ADDR` 设置。配置后，
`/api/v1/agent/*` 只注册到该监听器，并从 Web/管理监听器移除。独立监听器仅包含：

- `/api/v1/agent/roles`
- `/api/v1/agent/tasks` 及任务子资源
- `/api/v1/agent/files`（鉴权后的交付物下载）
- `/v1/models`、`/v1/chat/completions`（兼容接口；原生任务接入使用上述异步 API）
- `/livez`、`/readyz`、`/metrics`

非回环地址（包括 `0.0.0.0`、`[::]` 和专用监听器的 `:端口` 写法）必须同时配置
认证；否则配置校验会拒绝启动。仅本机开发可在回环地址使用兼容的无认证模式。

因此 `/api/chat`、`/api/config`、`/api/shell/jobs`、`/api/system/shutdown`
等 Web 或管理路由不会暴露在 AgentOS 数据面。未配置独立地址时，AgentOS
Data API 挂载在主监听器，适用于本地开发和单监听器部署。

业务服务使用自己的服务凭证：

```http
Authorization: Bearer <service-client-token>
```

租户、调用方、可用岗位和 Scopes 均从认证身份解析。`X-Tenant-ID`、
`X-Caller-ID` 或请求体字段不能覆盖已经认证的身份。

## 2. 发现岗位与固定版本

```http
GET /api/v1/agent/roles
Authorization: Bearer <service-client-token>
```

该接口只返回当前身份有权调用的岗位，响应包含：

```json
{
  "roles": [
    {
      "role_id": "customer-research",
      "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "agent_name": "CustomerResearchAgent",
      "description": "生成有来源的客户研究简报",
      "enabled": true,
      "input_schema": {
        "type": "object",
        "required": ["customer_id"]
      },
      "output_schema": {
        "type": "object",
        "required": ["summary", "sources"]
      },
      "links": {"submit": "/api/v1/agent/tasks"}
    }
  ],
  "count": 1
}
```

`role_revision` 格式为 `r1-` 加 64 位 SHA-256 十六进制摘要。任务接收时，
MACS 会持久化岗位快照，包括 Agent 配置、系统职责、工具和沙箱策略、
模型参数以及输入输出 Schema；模型密钥不写入快照，运行时仍从当前凭证绑定读取。
任务排队、Worker 接管或服务重启后都使用同一快照执行，之后修改岗位配置不会改变
已经接收的任务。

`service_roles[].input_schema` 和 `output_schema` 必须是自包含 JSON Schema，
解析和校验不会访问网络：

- 提交时，`parameters` 不符合 `input_schema` 会返回
  `400 invalid_parameters`，任务不会入队。
- 完成时，字符串结果会先解析为 JSON，再按任务快照中的 `output_schema`
  校验；解析或校验失败会把任务置为 `failed`。

## 3. 提交任务

```http
POST /api/v1/agent/tasks
Authorization: Bearer <service-client-token>
Content-Type: application/json
X-OpenKitty-User-ID: person-42
Idempotency-Key: customer-42-analysis-v1
X-Request-ID: crm-customer-42-v1
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01

{
  "role_id": "customer-research",
  "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "user_id": "person-42",
  "session_id": "customer-follow-up",
  "message": "分析客户资料并给出跟进方案",
  "parameters": {
    "customer_id": "42",
    "priority": "high"
  }
}
```

`role_revision` 可省略；服务端会在接收时解析并固定当前版本。显式版本与当前发布
版本不一致时返回 `409 role_revision_conflict`，调用方应重新发现岗位后决定是否
重试。

受信业务服务代表自然人调用时，需要在 `service_clients[].scopes` 中配置
`users:act_as`，并在任务体传稳定的 `user_id`。服务凭证是调用者（actor），
`user_id` 是数据归属人（subject）；普通服务客户端不能自行切换 subject。
多用户业务服务还应配置 `require_user_id: true`，让服务端拒绝漏传
`X-OpenKitty-User-ID` 的任务提交、查询、SSE、追加指令、取消和审批请求。该 Header
会把每个数据面请求限制到对应自然人；提交体中的 `user_id` 可保留用于业务
审计，但若提供必须与 Header 一致。
服务凭证必须留在受信后端；`tasks:*:own` 表示 actor 创建的任务。启用
`require_user_id` 的多用户凭证只能访问 Header 指定的 subject；如需跨用户运营
视图，应另配不启用该选项的后台凭证。任何服务凭证都不能下发给浏览器或自然人客户端。

同一认证调用方为同一自然人在同一租户内重复提交相同 `Idempotency-Key` 时，
服务端返回原任务，不会重复执行。不同租户、调用方或自然人可以使用相同键。
新任务返回 `202 Accepted`；幂等重放返回 `200 OK`，且
`idempotent_replay` 为 `true`。

```json
{
  "task_id": "agt_260726120000_a1b2c3d4",
  "tenant_id": "tenant-a",
  "subject_id": "person-42",
  "user_id": "person-42",
  "role_id": "customer-research",
  "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "agent_name": "CustomerResearchAgent",
  "session_id": "web_260726120000_12ab34",
  "status": "pending",
  "phase": "queued",
  "run_attempt": 0,
  "queue": "agentos",
  "idempotent_replay": false,
  "links": {
    "self": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4",
    "events": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/events",
    "cancel": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/cancel",
    "followups": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/followups",
    "approvals": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/approvals"
  }
}
```

请求体最多 1 MiB，附件最多 20 个。附件使用当前自然人工作区内的相对路径：

```json
{
  "role_id": "document-review",
  "message": "结合附件完成分析",
  "attachments": [
    {"path": "inputs/requirements.docx"}
  ]
}
```

### 3.1 绑定一个具体 Skill

任务可以显式携带 `skill_run`，让本次执行绑定一个已安装 Skill 的精确版本、参数和内容快照：

```json
{
  "role_id": "customer-research",
  "message": "生成客户研究简报并返回文件链接",
  "skill_run": {
    "skill_name": "guided-report",
    "version": "1.2.3",
    "parameters": {
      "customer_id": "42",
      "tone": "concise"
    },
    "client_reference": "crm-report-42"
  }
}
```

`skill_name` 必填，`version`、`parameters` 和 `client_reference` 可选。服务端会在模型运行前解析名称/别名，校验版本和参数，补齐 Skill 默认参数，冻结 `SKILL.md` 以及 scripts、references、assets 等支持文件，再通过正常工具执行链激活它。已接收任务不会因安装目录之后升级而改变工作流。

Skill Run 与 `collaboration` 是两个独立执行模式，不能在同一请求中组合。它只绑定本任务，不改变同一 session 后续的普通对话、`@员工` 或 Pipeline 路由。本阶段没有价格、订单、订阅、额度或付费权益字段。

Skill 身份和参数属于 AgentOS 幂等指纹。同一调用方复用相同 `Idempotency-Key`、但修改 `skill_run` 时，会得到 payload mismatch，而不是复用或改写原任务。

提交或查询响应会带回：

- `skill_run_request`：任务接收时持久化的原请求。
- `skill_run`：宿主回执，包括 `run_id`、`phase`、精确 Skill 摘要、规范化参数、激活次数、产物和验证结果。

Skill Run phase 为 `resolving -> activated -> running -> verifying -> completed`，也可能进入 `failed` 或 `stopped`。三个终态均不可被恢复流程重新置为 running。取消任务时，任务、未决审批、消息状态和 Skill Run checkpoint 在同一事务中进入 stopped；正常完成时，任务终态与 Skill Run 回执也在同一事务中提交。

不传 `session_id` 时，服务端创建新会话并绑定个人工作区。传入外部
`session_id` 时，服务端仍会按 `tenant_id + subject_id` 转换成内部会话 ID；
它不能作为用户身份，也不能借用其他人的会话或工作区。会话、消息、任务和个人
记忆的归属信息都会持久化到数据库，大文件仍应放在个人工作区或对象存储中。

## 4. 查询与游标分页

```http
GET /api/v1/agent/tasks/{task_id}
GET /api/v1/agent/tasks?role_id=customer-research&status=completed&limit=50
GET /api/v1/agent/tasks?cursor=<next_cursor>&limit=50
X-OpenKitty-User-ID: person-42
```

任务列表支持 `caller_id`、`role_id`、`status`、`limit` 和 `cursor`：

- `limit` 默认 50，范围为 1–100。
- 结果按 `created_at DESC, task_id DESC` 稳定排序。
- 有下一页时响应包含不透明的 `next_cursor`；调用方不得解析或修改游标。
- 身份和 Scope 过滤在分页前完成，查询参数只能缩小结果，不能扩大权限。
- 多用户服务传入 `X-OpenKitty-User-ID` 后，任务列表、单任务读取、SSE、
  追加指令、取消和审批都会在上述过滤之前增加精确 `subject_id` 条件。

任务状态包括：

- 非终态：`pending`、`running`、`waiting_approval`。
- 终态：`completed`、`degraded`、`failed`、`stopped`。

响应还包含 `phase`、`heartbeat_at`、`heartbeat_age_ms`、`run_attempt`、
`last_tool_call_id`、`worker_id` 和 `lease_until` 等运行信息，以及终态的
`result` 或任务级 `error`。

任务、请求、岗位快照和终态结果均持久化。`running` 任务持有有期限的租约并续期；
Worker 崩溃后，其他 Worker 在租约到期后使用新 token 重新领取，旧 Worker 的迟到
写入会因 token 不匹配而被拒绝。

## 5. 向运行中的任务追加指令

普通单 Agent 对话和显式 Skill Run 在 `pending` 或 `running` 状态时，可接受
持久化的补充指令：

```http
POST /api/v1/agent/tasks/{task_id}/followups
Authorization: Bearer <service-client-token>
X-OpenKitty-User-ID: person-42
Idempotency-Key: customer-42-followup-1
Content-Type: application/json

{"message":"改用样品 B，并在结果中保留审计编号"}
```

新指令返回 `202 Accepted` 和独立的 `followup_id`；使用相同幂等键和相同内容
重试返回 `200 OK`，不会重复应用。相同键改写内容返回
`409 idempotency_payload_mismatch`。消息去除首尾空白后不得为空，最大为
60 KiB UTF-8 字节。

```json
{
  "followup_id": "fup_260726120030_e5f6a7b8",
  "task_id": "agt_260726120000_a1b2c3d4",
  "status": "pending",
  "message": "改用样品 B，并在结果中保留审计编号",
  "delivery_attempt": 0,
  "created_at": "2026-07-26T12:00:30Z",
  "claimed_at": null,
  "applied_at": null,
  "cancelled_at": null,
  "idempotent_replay": false,
  "links": {
    "self": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/followups/fup_260726120030_e5f6a7b8",
    "task": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4",
    "events": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/events"
  }
}
```

回执是持久事实，可单条查询或按接收顺序列出：

```http
GET /api/v1/agent/tasks/{task_id}/followups/{followup_id}
GET /api/v1/agent/tasks/{task_id}/followups?limit=50
```

状态按 `pending -> delivering -> applied` 演进；父任务在应用前被取消时变为
`cancelled`。Worker 在当前任务租约下认领指令，把它与会话消息原子提交，然后
取消本轮模型生成并携带补充内容重新推理。服务重启或 Worker 换实例后，新租约可
重新认领未完成的投递。

追加、完成和取消共享同一个持久输入闸门：完成先提交后，新的追加返回 `409`；
追加先提交后，任务不能绕过它进入终态。如果模型结果与新指令同时到达，任务会
重新排队，旧结果不会被当作最终结果。取消会关闭闸门并把尚未应用的回执标为
`cancelled`。

默认服务 Scope 已包含 `tasks:followup:own`；跨同租户调用方追加必须显式授予
`tasks:followup:any`，且两者都不能突破 `tenant_id`、`subject_id` 和
`allowed_roles`。协同任务、显式 AI Employee/Pipeline 以及
`waiting_approval` 当前不接受追加，返回 `409 followup_not_supported` 或
`409 task_not_accepting_followups`，避免把补充指令注入不能安全恢复的编排层。

## 6. 请求关联、Trace Context 与错误

所有 AgentOS Data API 响应都带有 `X-Request-ID`。调用方可以提供由字母、数字和
`-_.:` 组成、最长 128 字符的请求 ID；缺失或不合法时由服务端生成。

可选的 W3C `traceparent` 必须是 version 00 格式。合法值会规范化、持久化到任务、
传递至 Worker 上下文并在响应 Header 中回显；不合法的值会被忽略且不回显。

HTTP 错误统一返回：

```json
{
  "error": "request rate limit exceeded",
  "code": "rate_limited",
  "message": "request rate limit exceeded",
  "request_id": "req_0123456789abcdef0123456789abcdef",
  "retryable": true
}
```

`error` 是与 `message` 相同的兼容字段；客户端逻辑应判断稳定的 `code` 和
`retryable`，日志使用 `request_id` 关联。`429` 应遵循 `Retry-After`；网络超时
或可重试错误应沿用原 `Idempotency-Key`，不要生成新的业务操作键。

## 7. SSE 事件

```http
GET /api/v1/agent/tasks/{task_id}/events
Accept: text/event-stream
```

连接建立后先收到 `connected`，随后可能收到 `task_update`、`progress`、
`followup_accepted`、`followup_applied`、`task_snapshot` 和 `replay_reset`。
实时任务事件带有 SSE `id`；客户端重连时可发送
`Last-Event-ID` Header（不能设置 Header 的客户端也可传 `last_event_id` 查询参数），
服务端会重放当前实例为该任务保留的最近 64 条常规大小事件。

这个重放窗口用于覆盖短暂断网，不是新的任务数据库。进程重启、连接切换到其他 Pod、
游标格式不匹配或游标早于保留窗口时，服务端发送 `replay_reset`，随后继续发送数据库
任务快照。因此不要求 Ingress 粘性会话，调用方也始终应以任务查询/`task_snapshot`
作为最终事实来源。

外部系统应把提交接口当作异步受理，而不是等待完成：保存 `202` 响应中的
`task_id`，用 `links.events` 展示实时进度，以 `links.self` 的持久任务快照作为
最终事实来源。不支持 SSE 的调用方可直接每 1–3 秒轮询；网络重试提交时复用原
`Idempotency-Key`。仓库内的 [Go SDK 预览](../sdk/README.md) 已封装追加指令、
回执查询以及“SSE 优先、断线后自动轮询”的调用方式。

## 8. 取消与审批

取消非终态任务：

```http
POST /api/v1/agent/tasks/{task_id}/cancel
```

`DELETE /api/v1/agent/tasks/{task_id}` 是等价兼容入口。成功取消返回 `stopped`；
终态任务再次取消返回 `409 Conflict`。取消和模型完成并发发生时只允许一个终态
获胜，迟到结果不能覆盖 `stopped`。

任务进入 `waiting_approval` 后，具有相应 Scope 的服务可读取并决定审批：

```http
GET /api/v1/agent/tasks/{task_id}/approvals
POST /api/v1/agent/tasks/{task_id}/approvals/{approval_id}/approve
POST /api/v1/agent/tasks/{task_id}/approvals/{approval_id}/deny
```

审批记录包含任务的 `role_revision`。批准后任务重新排队并继续使用原岗位快照；
拒绝后任务进入 `failed/approval_denied`。

## 9. 工具副作用恢复语义

每个实际工具调用使用 `task_id + tool_call_id` 保存执行回执：

- 已成功的调用返回缓存结果，不再次执行外部副作用。
- 进程中断或结果不确定的写操作禁止自动重放。
- 明确声明为只读的调用可在失败或进程中断后重试。
- 同一 call ID 的工具名、参数或只读分类发生变化时拒绝执行。

因此业务服务可以安全重试任务提交；对于 Agent 内部写操作，MACS 也不会在
无法判断外部结果时盲目重复执行。
