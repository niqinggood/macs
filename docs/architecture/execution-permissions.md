# macs 执行权限与计划模式

当前实现：2026-09-29。目标是让用户已经授权的日常任务连续执行，并准确保留明确禁止、身份隔离和危险操作边界。普通工具默认自动执行，不以“任务复杂”作为额外要求确认的理由。

## 模式与明确规则

- `default`、`acceptEdits`、`bypassPermissions` 的普通模式判定都允许工具执行且不额外请求确认；后两者保留兼容，不代表能绕过其他边界。
- `plan` 限制为只读动作及计划工具。宿主使用同一 `PermissionEngine` 作为有效模式来源；进入、退出及管理端切换均同步旧 `SafetyGuard` 兼容视图。重复进入不覆盖进入前模式，重复退出无副作用；初始配置就是 plan 时，退出恢复 default。
- plan 的只读要求先于持久 allow/ask 规则执行：写动作不会因 allow 规则放行，也不会申请一个批准后仍不能执行的审批。可用的只读动作仍受明确 deny/ask 规则约束。
- 非 plan 模式不计算只读属性；plan 每次模式检查只计算一次。已注册工具优先使用 `CallReadOnlyTool.IsReadOnlyForCall(args)`，其次使用 `ReadOnlyTool`，最后才按已知工具名判断；Shell 复用现有命令分类器。只读命令可用于计划调查，构建/测试可能写缓存或产物，不视为只读。
- 规则来源优先级保持 policy → user → project → local → CLI → session，同来源按现有具体程度和行为优先级。模式读写及规则集合替换与检查同步；替换不会让检查短暂落入无规则状态。

模式决策是执行链的一层，不授予越过用户工作区、租户身份、Sandbox、Hook、工具专属路径检查或系统破坏拦截的权限。`--dangerously-skip-permissions` 仍只保留旧参数兼容。

### 工具发现与宿主管理

`manage_permissions` 的 Agent 工具目录只暴露 `view`、`list_rules`、`suggest_rule` 三个可执行的只读动作，不再宣称 Agent 可以切换权限或修改规则。普通任务已被允许时直接执行，无需先调用权限工具。

宿主的策略修改实现继续保留；`set`、`add_rule`、`remove_rule` 不属于 Agent/MCP 调用契约。注册表参数验证和执行策略中间件继续拒绝这些调用，不能为了让目录中的动作可执行而放宽策略边界。读取工具完整 Schema 与按需发现均使用同一份动作列表。

## 用户明确要求与语言判断

`internal/permissionpolicy/user_tool_policy.go` 只根据宿主提供的用户原文判断显式约束，先去除引用、网页及工具输出等数据块。明确“不调用任何工具”“只读检查”“不要创建文件”“不要开始制作”等要求仍然有效。

否定措辞限制在相应语句中。例如：

| 用户要求 | 行为 |
| --- | --- |
| 不要再询问确认，直接生成文件 | 不自动生成禁止写文件的约束；原有执行权限照常检查 |
| 不必解释过程，修改文件并保存结果 | 不把“无需解释”扩大成禁止修改 |
| 不要反复确认。创建文件，然后修改代码 | 否定不跨句覆盖后续授权 |
| Do not edit, create, or delete files | 保留逗号枚举的文件修改禁令 |
| 只读检查，不要开始制作 | 允许可验证为只读的动作，禁止写入 |

这是保守的规则解析，不是完整自然语言语义理解。对象级约束（如“保留原件，另存副本”）尚未统一建模；不要通过关闭全部约束来处理这类精度不足。

## 计划与澄清流程

计划模式用于确有必要的调查与方案整理，普通小任务不必先进入计划模式。`exit_plan_mode` 接受与任务规模相称的方案，结束只读调查并返回方案，不再固定输出“等待用户批准”，也不强制回报五段英文标题缺失。

用户已授权实施时，可继续执行；用户明确要求先审方案时，仍应展示方案并等待。退出计划不会清除上下文中的用户只读/暂缓制作要求，也不会取消已配置工具审批或 Pipeline 的 `require_approval` 节点。计划工具的说明不替代实际授权合同。

`ask_clarification` 只用于实质影响结果的缺失信息或用户要求的核对；常规细节采用合理默认，任务复杂本身不构成追加确认的理由。本次改变工具说明，未另加自动跳过澄清的执行旁路。

## 审批预检与实际执行

AgentOS 仍使用既有持久审批、任务/工具/参数绑定、单次授权和任务恢复协议。

- 对用户已经明确禁止的工具调用，预检不申请审批；执行处返回具体的用户约束错误。
- Sandbox 预检与执行都把真实调用参数交给逐调用只读/能力合同，避免只读查询被当作工具最宽泛的写入能力。
- 预检使用沙箱副本，不污染实际违规计数。
- 执行前仍保留必要的验证，因为直接注册表执行、MCP、恢复和普通 Agent 调用并非同一入口。不能仅因函数名称相似就删除整个检查层。

当前预检并未覆盖全部角色、Hook、工具存在性等拒绝原因；这部分还应继续沿真实执行链梳理。默认模式已经无需普通操作逐个确认，后续重点是准确分类和避免无效审批，而不是增加新的 bypass 开关。

## 代码与验证

宿主状态转换位于 `internal/app/permission_runtime.go`；策略与用户约束位于 `internal/permissionpolicy/`；计划工具位于 `internal/agentloop/plan_tools.go`；审批/Sandbox 适配位于 `internal/app/sandbox_policy.go`，实际执行继续复用现有 Registry 和 Middleware。

```powershell
go test ./internal/permissionpolicy ./internal/approval ./internal/middleware ./internal/toolregistry ./internal/agentloop ./internal/security ./internal/shellpolicy ./internal/tools/skills ./internal/tools/security ./internal/app -count=1
go vet ./internal/permissionpolicy ./internal/agentloop ./internal/middleware ./internal/app
go build -buildvcs=false -trimpath -o NUL ./cmd/macs
```

这些测试覆盖模式恢复、重复进入/退出、只读查询与写动作区分、显式禁令、审批预检和配置规则。它们不代表全仓迁移测试或真实模型任务已全部验收。
