# AgentOS 服务接入快速指南

AgentOS Data API 面向后端服务之间的异步调用。完整契约见
[`openapi-agentos.yaml`](./openapi-agentos.yaml)，详细运行语义见
[`agent-os-api.md`](./agent-os-api.md)，岗位和权限配置见
[`enterprise-agent-service.md`](./enterprise-agent-service.md)。

## 1. 确认数据面地址和凭证

生产部署建议设置 `gateway.agent_os_listen_addr` 或
`OPENKITTY_AGENTOS_LISTEN_ADDR`。设置后，`/api/v1/agent/*` 只在独立数据面
监听器提供，主 Web/管理监听器不再注册这些路由；独立监听器还提供 `/livez`、
`/readyz` 和 `/metrics`。

监听到非回环地址时必须配置 `service_clients`、租户凭证或 Gateway 管理凭证；
无认证的公网/内网监听会在配置校验阶段被拒绝。

管理员为每个调用方配置独立的 `service_clients` 凭证，并绑定固定租户、岗位和
Scopes。业务服务只保存自己的 Token：

```http
Authorization: Bearer <service-client-token>
```

不要发送 `X-Tenant-ID`，也不要依赖 `X-Caller-ID` 参与授权。

## 2. 发现岗位

```bash
curl https://macs.example.com/api/v1/agent/roles \
  -H "Authorization: Bearer $OPENKITTY_SERVICE_TOKEN"
```

选择响应中的 `role_id`，并保存 `role_revision`。岗位响应同时给出
`input_schema` 和 `output_schema`；调用方应先按输入 Schema 构造
`parameters`。`role_revision` 是 `r1-` 加 64 位 SHA-256 摘要，标识当前不可变
岗位快照。

## 3. 提交任务

每个业务操作生成稳定的幂等键。网络超时后使用同一个键重试：

```bash
curl -i https://macs.example.com/api/v1/agent/tasks \
  -H "Authorization: Bearer $OPENKITTY_SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-OpenKitty-User-ID: person-42" \
  -H "Idempotency-Key: customer-42-research-v1" \
  -H "X-Request-ID: crm-customer-42-v1" \
  -H "traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" \
  -d '{
    "role_id": "customer-research",
    "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "user_id": "person-42",
    "session_id": "customer-follow-up",
    "message": "核验客户 42 的公司背景并给出三条跟进建议",
    "parameters": {"customer_id": "42"}
  }'
```

新任务返回 `202 Accepted`；幂等重放返回 `200 OK`，且
`idempotent_replay` 为 `true`。保存响应中的 `task_id` 和
`role_revision`。

`role_revision` 可省略，此时服务端在接收时固定当前版本。显式版本已经变化时返回
`409 role_revision_conflict`，应重新发现岗位后再决定是否提交。输入不符合岗位
`input_schema` 时返回 `400 invalid_parameters`，不会创建任务。

宿主界面若已先展示员工欢迎语，可在新会话首轮一并提交受限的展示身份和
assistant 历史：

```json
{
  "role_id": "fundraising-advisor",
  "message": "你是谁",
  "initial_messages": [
    {"role": "assistant", "content": "你好，我是盈盈，融资顾问。"}
  ],
  "assistant_identity": {"name": "盈盈", "role": "融资顾问"}
}
```

`initial_messages` 只允许最多 4 条 assistant 消息，并且只能用于空会话；非空
会话返回 `409 initial_messages_require_empty_session`。`assistant_identity` 只有
`name`、`role` 两个展示字段，不接受 prompt/instructions。两字段均进入幂等
指纹；同一幂等键改变欢迎语或身份会返回 `409 idempotency_payload_mismatch`。
该单助手契约不适用于 `collaboration` 任务。

业务服务若代表多个自然人调用，应给该 `service_client` 显式配置
`users:act_as` 和 `require_user_id: true`，并在每次提交中传稳定的
`X-OpenKitty-User-ID`。提交体可同时记录 `user_id`，但必须与 Header 一致。
启用后，所有任务 API 请求漏传该 Header 都会返回
`400 user_id_required`。同一个 Agent 和模型可以
服务所有人；MACS 会按自然人分别持久化任务、会话、消息、幂等空间和记忆，
并分配独立个人工作区。`session_id` 只是该用户内部的会话键，不能替代
`user_id`。

`service_client` Token 只应保存在受信业务后端。`tasks:*:own` 的“own”指该
服务身份创建的任务，因此后端可以统一轮询它代表不同自然人提交的任务；浏览器
和终端用户不应直接持有这个 Token，业务后端需先认证自然人再传入其 `user_id`。

## 4. 查询、分页和取消

以状态查询作为事实来源：

```bash
curl https://macs.example.com/api/v1/agent/tasks/<task_id> \
  -H "Authorization: Bearer $OPENKITTY_SERVICE_TOKEN" \
  -H "X-OpenKitty-User-ID: person-42"
```

状态分为：

- 非终态：`pending`、`running`、`waiting_approval`。
- 终态：`completed`、`degraded`、`failed`、`stopped`。

按岗位列举任务：

```http
GET /api/v1/agent/tasks?role_id=customer-research&status=completed&limit=50
```

`limit` 范围为 1–100，默认 50。有下一页时，把响应中的不透明
`next_cursor` 原样传给 `cursor`：

```http
GET /api/v1/agent/tasks?cursor=<next_cursor>&limit=50
```

需要低延迟更新时连接
`GET /api/v1/agent/tasks/{task_id}/events`。SSE 断线后仍须查询任务快照。

取消非终态任务：

```bash
curl -X POST \
  https://macs.example.com/api/v1/agent/tasks/<task_id>/cancel \
  -H "Authorization: Bearer $OPENKITTY_SERVICE_TOKEN" \
  -H "X-OpenKitty-User-ID: person-42"
```

## 5. 追加运行中指令

普通单 Agent 对话或显式 Skill Run 仍处于 `pending` / `running` 时，可以补充
要求。每一条业务追加都使用自己的稳定幂等键：

```bash
curl -i -X POST \
  https://macs.example.com/api/v1/agent/tasks/<task_id>/followups \
  -H "Authorization: Bearer $OPENKITTY_SERVICE_TOKEN" \
  -H "X-OpenKitty-User-ID: person-42" \
  -H "Idempotency-Key: customer-42-followup-1" \
  -H "Content-Type: application/json" \
  -d '{"message":"改用样品 B，并在结果中保留审计编号"}'
```

新追加返回 `202`；网络超时后原样重试会返回 `200` 和同一个 `followup_id`。
保存回执，并查询到 `applied` 或 `cancelled`：

```http
GET /api/v1/agent/tasks/{task_id}/followups/{followup_id}
GET /api/v1/agent/tasks/{task_id}/followups?limit=50
```

默认服务身份拥有 `tasks:followup:own`。补充内容若先于任务完成被持久化，服务端会
保证它被应用后才能提交最终结果；任务已完成、正在等待审批，或属于协同/
AI Employee/Pipeline 编排时返回 `409`。SSE 会发送 `followup_accepted` 和
`followup_applied`，但最终仍以追加回执和任务快照为准。

## 6. 处理审批

任务进入 `waiting_approval` 后，具有 `approvals:read` 的身份可以读取记录：

```http
GET /api/v1/agent/tasks/{task_id}/approvals
```

具有 `approvals:decide` 且岗位范围匹配的身份可以决定：

```http
POST /api/v1/agent/tasks/{task_id}/approvals/{approval_id}/approve
Authorization: Bearer <approval-operator-token>
Content-Type: application/json

{"reason":"变更单 CR-42 已批准"}
```

拒绝时把最后一个路径段改为 `deny`。批准后任务重新排队并继续使用原
`role_revision`；拒绝后任务进入 `failed/approval_denied`。

## 7. 错误、关联与重试

所有响应都包含 `X-Request-ID`。合法的 W3C version 00 `traceparent` 会传递至
Worker 并在响应中回显；不合法的值会被忽略。

错误响应结构固定为：

```json
{
  "error": "request rate limit exceeded",
  "code": "rate_limited",
  "message": "request rate limit exceeded",
  "request_id": "req_0123456789abcdef0123456789abcdef",
  "retryable": true
}
```

- `429`：遵循 `Retry-After`，使用原幂等键重试。
- `503`、网络超时或 `retryable: true`：保留原幂等键重试提交。
- `409`：重新查询任务、审批或岗位版本，不要盲目重放。
- `401/403`：检查凭证、Scopes 和岗位授权。
- `404`：资源不存在，或当前身份无权获知它是否存在。
