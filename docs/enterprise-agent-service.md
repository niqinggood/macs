# MACS 企业 Agent 服务调用

初次接入可先按 [`macs AgentOS 部署与调用入门`](./agentos-quickstart.md) 跑通本机服务，再参考本篇配置企业岗位和权限。

MACS 的 AgentOS 接口用于“提交业务工作并异步取回结果”，不是让调用方临时改写 Agent。职责、系统提示词、工具范围、只读目录、权限规则和最大迭代次数都由服务端配置持有。

## 1. 发布一个岗位

先在 `agents` 中定义岗位能力，再通过 `service_roles` 显式发布：

```yaml
agents:
  - name: CustomerResearchAgent
    model: minimax_model
    type: custom
    system_message: |
      你是企业客户研究员。只做客户背景核验、证据整理和跟进建议。
      不发送消息，不修改 CRM，不执行部署。
    enabled_tools: [web_fetch, read_file, write_file]
    disallowed_tools: [cli, email, sandbox, manage_permissions]
    read_only_paths: [config, secrets]
    sandbox:
      mode: strict
      allowed_paths: ["."]
      allowed_hosts: ["api.example-crm.com"]
      shell_policy: deny
      network_policy: allow
      sub_agent_policy: deny
    max_iterations: 30

service_roles:
  - id: customer-research
    agent_name: CustomerResearchAgent
    description: 生成有来源的客户研究简报
    enabled: true
    allowed_tenants: [tenant-a]
    allowed_callers: [crm-service]
    input_schema:
      type: object
      additionalProperties: false
      required: [customer_id]
      properties:
        customer_id: {type: string}
        priority: {type: string, enum: [normal, high]}
    output_schema:
      type: object
      additionalProperties: false
      required: [summary, sources]
      properties:
        summary: {type: string}
        sources:
          type: array
          items: {type: string}

tenants:
  - id: tenant-a
    name: 租户 A
    token: <long-random-tenant-administration-token>

service_clients:
  - id: crm-service
    tenant_id: tenant-a
    token: <long-random-crm-service-token>
    enabled: true
    allowed_roles: [customer-research]
    scopes: [tasks:submit, tasks:read:own, tasks:cancel:own, tasks:followup:own, users:act_as]
    require_user_id: true
  - id: approval-operator
    tenant_id: tenant-a
    token: <long-random-operator-token>
    enabled: true
    allowed_roles: [customer-research]
    scopes: [approvals:read, approvals:decide]
```

只要配置了至少一个 `service_roles`，AgentOS 就进入显式发布模式：未发布、停用、租户不匹配或调用方不匹配的岗位不能提交任务。`service_clients` 将一个独立凭证绑定到固定租户和允许调用的岗位；`allowed_callers` 校验的是这个经过认证的服务 ID，而不是请求方自报的 Header。未配置 `service_roles` 时使用隐式发布模式，已配置的普通 Agent 可按精确名称调用。

`input_schema` 和 `output_schema` 是自包含 JSON Schema，加载时不允许通过网络解析外部引用。任务提交前，`parameters` 必须通过输入 Schema；失败返回 `400 invalid_parameters`，任务不会入队。任务完成时，结果必须通过任务快照中的输出 Schema；字符串结果先解析为 JSON，解析或校验失败会使任务进入 `failed`。

`scopes` 省略时使用最小默认值：`tasks:submit`、`tasks:read:own`、
`tasks:cancel:own`、`tasks:followup:own`。可显式配置的 Scope 包括：

- `tasks:submit`
- `tasks:read:own`、`tasks:read:role`、`tasks:read:any`
- `tasks:cancel:own`、`tasks:cancel:any`
- `tasks:followup:own`、`tasks:followup:any`
- `approvals:read`、`approvals:decide`
- `users:act_as`：允许受信业务服务通过 `X-OpenKitty-User-ID` 指定稳定的自然人

所有任务和审批 Scope 都先受固定租户和 `allowed_roles` 约束；`*:any` 只表示该租户内的任意调用方，不会越过岗位边界。任务列表会自动套用调用身份的过滤条件，`caller_id` 查询参数只能缩小结果，不能扩大权限。

面向多个自然人的业务服务应同时设置 `require_user_id: true`。该选项要求显式授予
`users:act_as`，并要求任务提交、查询、SSE、追加指令、取消和审批请求都携带
`X-OpenKitty-User-ID`。服务端会把任务 API 访问精确限制到该自然人；提交体若
同时提供 `user_id`，两者必须一致。单一服务主体或需要跨用户运营视图的独立后台
凭证可保持默认值 `false`。

## 2. 数据面隔离与服务调用

生产环境建议为 AgentOS 配置独立监听器：

```yaml
gateway:
  listen_addr: 0.0.0.0:8080
  agent_os_listen_addr: 0.0.0.0:8081
```

也可以设置 `OPENKITTY_AGENTOS_LISTEN_ADDR`。配置后，`/api/v1/agent/*`
从 Web/管理监听器移除，只在独立数据面提供；独立监听器还提供 `/livez`、
`/readyz` 和 `/metrics`，不会注册 `/api/chat`、`/api/config`、
`/api/shell/jobs` 或 `/api/system/shutdown` 等路由。未配置独立地址时，
AgentOS Data API 挂载到主监听器。

非回环数据面必须配置认证；`0.0.0.0`、`[::]` 以及专用监听器的 `:端口`
都属于非回环绑定。若没有服务客户端、租户或 Gateway 管理凭证，配置校验会拒绝启动。

业务服务使用自己的 `service_clients[].token`；平台管理密钥使用 `X-API-Key`。不要传 `X-Tenant-ID`，MACS 不信任调用方自报的租户。`X-Caller-ID` 也不参与已认证请求的授权。

先发现当前身份可用的岗位：

```http
GET /api/v1/agent/roles
Authorization: Bearer <crm-service-token>
```

响应返回 `role_id`、`role_revision`、`agent_name`、描述、启用状态、输入输出
Schema 和提交链接。`role_revision` 格式为 `r1-` 加 64 位 SHA-256 摘要。
MACS 在接收任务时持久化不可变岗位快照，包括系统职责、工具、权限、沙箱、
模型参数和 Schema，但不包含模型密钥。排队、Worker 接管和服务重启后仍按该快照
执行；模型凭证继续从当前服务端配置读取，便于独立轮换。

```http
POST /api/v1/agent/tasks
Authorization: Bearer <crm-service-token>
X-OpenKitty-User-ID: person-42
Idempotency-Key: customer-42-research-v1
X-Request-ID: crm-customer-42-v1
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
Content-Type: application/json

{
  "role_id": "customer-research",
  "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "user_id": "person-42",
  "session_id": "customer-follow-up",
  "message": "核验客户 42 的公司背景并给出三条跟进建议",
  "parameters": {
    "customer_id": "42",
    "priority": "high"
  }
}
```

`role_revision` 可省略，服务端会固定接收时的当前版本；显式版本不再是当前发布
版本时返回 `409 role_revision_conflict`。

这里的服务凭证标识实际调用者（`actor`），`X-OpenKitty-User-ID` 和提交体中
与其一致的 `user_id` 标识数据归属的自然人
（`subject`）。只有配置了 `users:act_as` 的服务客户端才能提交 `user_id`。
MACS 不直接使用外部 `session_id` 作为身份：业务服务可以为同一人的多轮
对话重复传入同一个稳定会话键，服务端会按
`tenant_id + subject_id` 生成内部会话命名空间，并把任务、会话、消息、幂等键、
工作区和个人记忆绑定到该 `subject_id`。因此 Alice 和 Bob 即使传入完全相同的
`session_id` 与 `Idempotency-Key`，也会得到不同的会话、任务和个人数据空间。

服务 Token 是受信后端身份，不能下发给浏览器。未启用 `require_user_id` 时，
`tasks:*:own` 仍表示该 actor 创建的任务，业务后端可以统一轮询其代表所有自然人
提交的任务；启用后每个请求还必须匹配 Header 中的自然人。自然人认证
以及“当前请求只能使用谁的 `user_id`”由持有 Token 的业务后端负责。

成功返回 `202 Accepted`：

```json
{
  "task_id": "agt_260726120000_a1b2c3d4",
  "tenant_id": "tenant-a",
  "subject_id": "person-42",
  "user_id": "person-42",
  "role_id": "customer-research",
  "role_revision": "r1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "agent_name": "CustomerResearchAgent",
  "status": "pending",
  "links": {
    "self": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4",
    "events": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/events",
    "cancel": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/cancel",
    "followups": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/followups",
    "approvals": "/api/v1/agent/tasks/agt_260726120000_a1b2c3d4/approvals"
  }
}
```

随后可查询、订阅或取消：

```http
GET /api/v1/agent/tasks/{task_id}
GET /api/v1/agent/tasks/{task_id}/events
POST /api/v1/agent/tasks/{task_id}/followups
GET /api/v1/agent/tasks/{task_id}/followups/{followup_id}
POST /api/v1/agent/tasks/{task_id}/cancel
GET /api/v1/agent/tasks?role_id=customer-research&status=completed&limit=50
```

这些接口先执行租户和岗位归属检查，再检查任务 Scope。默认服务身份只能查询、
订阅、追加和取消自己创建的任务；跨调用方访问必须显式授予
`tasks:read:role`、`tasks:read:any`、`tasks:followup:any` 或
`tasks:cancel:any`。跨租户和无权访问的任务按 `404` 返回，避免泄露任务是否存在。
任务提交幂等键按 `tenant + caller + subject + key` 隔离；追加指令还会加入
`task_id` 分区。

追加指令只面向 `pending` / `running` 的普通单 Agent 对话和显式 Skill Run。
服务端先持久化独立回执，再由持有父任务租约的 Worker 认领并原子追加到会话；
任务完成、取消和追加共享持久输入闸门，因此先受理的指令不会被并发终态越过。
调用方应为每条追加使用独立稳定的 `Idempotency-Key`，保存返回的
`followup_id`，并通过回执查询 `pending`、`delivering`、`applied` 或
`cancelled`。协同、显式 AI Employee/Pipeline 和 `waiting_approval` 当前拒绝
追加，以免绕过其编排与审批边界。

任务列表支持 `caller_id`、`role_id`、`status`、`limit` 和不透明 `cursor`。
`limit` 默认 50，范围为 1–100；结果按 `created_at DESC, task_id DESC`
稳定排序，有下一页时返回 `next_cursor`。授权过滤先于分页执行，过滤参数不能扩大
身份权限。

所有 AgentOS Data API 响应都包含 `X-Request-ID`。合法的 W3C version 00
`traceparent` 会持久化到任务、传递到 Worker 并在响应 Header 中回显；不合法值
会被忽略。错误统一返回：

```json
{
  "error": "request rate limit exceeded",
  "code": "rate_limited",
  "message": "request rate limit exceeded",
  "request_id": "req_0123456789abcdef0123456789abcdef",
  "retryable": true
}
```

调用方应根据 `code` 和 `retryable` 决定处理方式，并使用 `request_id` 关联日志。

## 3. `ask` 审批与恢复

权限规则命中 `ask` 时，工具不会执行，整个工具批次会在任何副作用发生前暂停。任务状态变为 `waiting_approval`，审批记录持久化工具名、脱敏参数摘要、参数哈希、租户、岗位、`role_revision` 和到期时间。批准只对同一任务、同一工具、同一参数生效一次；批准后继续使用原岗位快照。

```http
GET /api/v1/agent/tasks/{task_id}/approvals

POST /api/v1/agent/tasks/{task_id}/approvals/{approval_id}/approve
Authorization: Bearer <approval-operator-token>
Content-Type: application/json

{"reason":"变更单 CR-42 已批准"}
```

批准后任务重新进入共享队列，由 Worker 发起新的运行尝试；拒绝后任务进入 `failed/approval_denied`，不会执行工具：

```http
POST /api/v1/agent/tasks/{task_id}/approvals/{approval_id}/deny
Authorization: Bearer <approval-operator-token>
Content-Type: application/json

{"reason":"不在变更窗口"}
```

只有控制面密钥或具备 `approvals:decide` 的同租户服务身份能作出决定；`approvals:read` 独立控制审批记录读取，操作员的 `allowed_roles` 同时限制它可审批的岗位。`can_approve: true` 是同时授予这两个审批 Scope 的配置快捷方式。跨租户仍返回 `404`。`gateway.approval_ttl_seconds` 控制等待期限，默认 86400 秒；到期的审批和任务会分别进入 `expired` 与 `failed/approval_expired`。

## 4. MCP 调用

需要直接调用岗位工具的外部 Agent 可以连接 `POST /mcp`，使用同一服务凭证，并通过 `X-OpenKitty-Agent` 指定发布的岗位 ID：

```http
POST /mcp
Authorization: Bearer <crm-service-token>
X-OpenKitty-Agent: customer-research
Content-Type: application/json

{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}
```

工具列表和实际执行都按岗位解析，并经过相同的工具白名单、项目权限和中间件。租户级 MCP 身份不能读取全局 session、task、员工或 skill 资源。

## 5. 当前安全边界

- MCP 和 AgentOS 内部工具调用统一经过参数校验、岗位工具白名单、项目权限规则、审批、沙箱策略、循环保护和结果规范化。
- `sandbox` 的 mode/allow/deny、`manage_permissions` 的 set/add/remove，以及插件启停不能由 Agent 工具调用修改。
- `strict` 沙箱把文件参数和明显的 Shell 路径限制在工作区/显式允许目录，按真实目录边界和符号链接祖先校验；网络策略阻止元数据、回环、私网和链路本地 IP，或按 `allowed_hosts` 收紧。`readonly` 还会拒绝未声明为只读的工具。
- 配置 `allowed_hosts` 后，远程 MCP 会同时校验其固定服务端点；无法声明或从参数中提取网络目标的扩展工具会被保守拒绝，避免绕过主机白名单。
- 子 Agent 继承父 Agent 的沙箱策略并只能叠加限制，不能通过自己的配置切换到 `none` 来削弱父级边界。
- 租户任务、角色、调用方和工作区随队列持久化；Worker 重启或换 Pod 后仍保持相同边界。
- 租户附件只能从该租户工作区读取。
- AgentOS 的每任务临时 Agent 暂不挂载会通过共享工厂创建执行者的
  `assemble_team/sub_agent/parallel_sub_agent/swarm/verify_work`；普通本地 Agent
  不受影响。待子 Agent 能完整继承 subject、workspace 和任务账本后再开放。
- 配置查询会对 `service_clients[].token` 脱敏；生产部署仍应通过 Secret 挂载配置，并定期轮换每个服务的独立凭证。

当前沙箱是统一的宿主策略边界，不等同于内核级文件系统虚拟化。对于不可信脚本、第三方二进制和需要强对抗隔离的任务，生产部署仍应把 Worker 放入独立容器/低权限账户，并配置只读根文件系统、工作区卷和出站网络策略。

生产部署仍应在反向代理层启用 TLS、限流和密钥轮换。内置用户名密码登录不应作为企业身份源；企业部署应接入 OIDC/SSO 和持久化短期令牌。
