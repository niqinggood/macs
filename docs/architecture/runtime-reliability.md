# macs 模型调用与配置更新

本文约定普通聊天、内部摘要等辅助模型调用、模型配置重载及设置保存之间应保持一致的行为。

## 本地模型的密钥策略

`internal/routing.ModelRequiresAPIKey` 是宿主判断模型是否必须配置密钥的统一入口：

- 明确配置为 `ollama` 或 `vllm` 的服务允许不填密钥；如提供密钥，调用仍发送该值。
- HTTP/HTTPS 地址的实际主机为 `localhost` 或 IPv4/IPv6 回环地址时，允许不填密钥。
- 其他模型继续要求密钥。内网地址本身不意味着允许匿名调用；按服务类型或已有认证配置处理。

主机判断使用 URL 解析后的 hostname，不通过子串判断。`localhost.example.test`、地址路径中的 `localhost`、用户名中的回环地址，以及模型类型中的 `not-ollama` 均不构成本地模型证明。

应用把该判断传给配置验证和 `CompletionConfig.APIKeyOptional`。后者仅为内部调用参数，不新增用户配置字段；零值仍要求密钥。没有密钥的本地调用和健康检查不发送空的 `Authorization: Bearer`，已配置密钥则正常发送。模型名和服务地址仍需填写。

## 模型配置重载

### 网页对话的本次模型选择

本机网页的 `POST /api/chat` 可以传递 `model_config` 字符串，值必须是现有可用模型配置的 ID。它用于本次普通 Agent 对话、简单问答（`task_type: qa`）或 Skill；浏览器不能通过这个字段传递服务地址、API Key 或模型配置对象。带该字段的请求仅接受回环地址、本机 Host 和同源来源，并拒绝 AgentOS 身份或其他聊天渠道。

网页明确选模优先于当前 Agent 在 YAML 中的独立模型绑定，但只影响本次任务。接收时，宿主冻结选中模型的完整运行配置和当前 Agent 的岗位配置；简单问答复用同一模型快照，并保留不执行工具的行为。临时 Agent 仍通过现有 AgentFactory 和运行时工具注册创建，保留岗位提示词、工具约束和已经绑定的工作目录；不会修改 YAML 中任一 Agent 的模型绑定，也不会替换缓存的 Agent。接收之后调整所选模型配置、环境密钥、默认 Agent 或岗位配置，不改变这次已经接收的任务。

平台模型会按选中模型校验提交浏览器的登录状态，而非按 Agent 的旧 YAML 模型决定。平台登录凭据只绑定到提交者的任务 context；底层模型调用继续使用既有调用幂等标识，不借用其他用户登录状态。模型和岗位快照只在内存保存，任务记录可以保存 `model_config` 配置 ID，但不序列化快照、服务凭据或平台登录凭据。进程重启后，缺失原快照的显式选模任务必须重新提交，不得静默改用当前默认模型。

未传 `model_config` 的 API、渠道和 AgentOS 请求维持原来的模型选择及岗位版本契约。协同团队、外部编码 Agent、AI 员工和工作流不属于此临时选模合同；从普通对话入口携带显式选择转入这些特殊执行链时，宿主明确拒绝，避免显示的模型与实际计费模型不一致。

Gateway 重载判断比较完整的 `ModelConfig`，包括服务类型、模型名称、密钥来源与值、地址、温度、输出 token 上限、思考模式及视觉能力。只改变模型名称或能力参数也必须进入引擎更新流程。

`apiKeyResolved` 是内部解析状态，不独立触发重载；配置相同也不重复更新。这里约定的是模型配置，不代表端口、数据库、Agent 提示词等所有设置都已支持无重启更新。启动专属字段仍按既有提示处理。

引擎更新日志只记录模型和 Agent 数量，不打印完整配置对象，避免凭据进入普通日志。

## 设置保存与工作目录

`Workspace` 可由启动参数 `--path` 指定，在 YAML 中属于运行时字段。`UpdateFullConfig` 读取磁盘配置后，应先通过现有 `preserveRuntimeOnlyConfig` 继承运行中的工作目录，再合并设置、验证和原子保存；`ReloadConfig` 使用同一继承规则。

修改并发数等普通设置不应把工作目录重置为空或退回进程目录。该处理不改变既有租户/任务工作区隔离，也不将本机运行目录写入 YAML 模板。

## 验证

```powershell
go test ./internal/app ./internal/configvalue ./internal/modelruntime ./internal/routing ./internal/tools/management ./internal/toolregistry ./internal/middleware ./internal/permissionpolicy -count=1
go vet ./internal/app ./internal/configvalue ./internal/modelruntime ./internal/routing ./internal/tools/management
go build -buildvcs=false -trimpath -o NUL ./cmd/macs
```

回归用例覆盖内部本地模型调用、带/不带密钥的 HTTP 请求、云端无密钥拒绝、伪装成本地的域名、IPv6 回环地址、模型参数变更检测、未变配置不重载、设置保存后的工作目录保留及工具目录动作范围。HTTP 用例使用本机测试服务，不需要真实模型密钥，也不代表真实模型任务效果已评测。

权限工具的可见动作与宿主修改边界见 [执行权限与计划模式](execution-permissions.md)。
