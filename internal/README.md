# OpenKitty `internal` 代码导读：目录做什么，一条请求如何跑完

这份文档解释 `E:\openkitty\internal` 里的 Go 代码逻辑。

它面向第一次接触 OpenKitty 源码的人，重点不是逐个罗列文件，而是回答下面几个问题：

- 程序从哪里启动？
- Gateway、AgentOS、Agent、Skill、Tool、Pipeline、AI Employee 分别是什么？
- 用户发来一条消息后，代码到底经过了哪些层？
- 为什么同一个主题既出现在 `internal/app`，又出现在独立目录？
- 想修改某个功能时，应该先看哪个目录、哪个函数？

本文以当前源码为准。`docs/architecture/go-modularization.md` 记录的是迁移过程和边界约定；本文记录的是迁移后的运行逻辑和阅读方法，两者用途不同。

## 先回答最容易困惑的三个问题

### 1. 为什么目录叫 `internal`

`internal` 是 Go 语言约定的特殊目录，不是 OpenKitty 自己发明的名字。

当代码位于：

```text
mac/internal/xxx
```

只有 `internal` 上级目录范围内的 Go 代码可以导入它。对本项目来说，就是仓库内部的 `cmd/*`、`internal/*` 等代码可以使用，仓库外部的另一个 Go 项目不能把 `mac/internal/app` 当公共 SDK 导入。

它表达的是：

> 这些包是 OpenKitty 的内部实现，可以在仓库内拆分和重构，但不承诺给外部项目提供稳定 API。

这也解释了为什么可复用的公共能力应放在 `pkg/*`，而产品内部运行逻辑放在 `internal/*`。

### 2. `internal/app` 到底是什么

先说结论：`internal/app` 是应用装配层，也是目前仍在继续瘦身的核心宿主层。

它负责把下面这些独立能力接起来：

```text
配置 + 数据库 + 模型 + Agent + Tool + Skill + Gateway
     + AgentOS + Pipeline + AI Employee + Channel
```

因此，`app` 里应该保留：

- 应用启动和停止；
- 全局配置到各领域对象的转换；
- Gateway 路由和具体服务对象的绑定；
- 跨模块工作流；
- 必须同时认识多个领域类型的适配代码。

`app` 里不适合继续新增：

- 单一工具的完整实现；
- 纯文本、路径、进程等通用算法；
- 不依赖 `GatewayService`、`LLMAgent`、`Config` 的领域规则；
- 只为转发另一个包而存在的大量兼容壳。

所以“文件已经少于 30 个”和“`app` 已经很小”不是一回事。当前 `app` 有 27 个非测试 Go 文件，但其中 `mac.go`、`gateway.go`、`pipeline.go` 等仍然很大。接下来的瘦身重点应是降低这些文件的职责和行数，而不是只追求文件数量。

### 3. 为什么 `agentos`、`pipeline`、`aiemployee` 在两个地方都有代码

这是有意的分层，不是简单的重复：

| 中立领域包 | `internal/app` 中的宿主代码 | 两者区别 |
| --- | --- | --- |
| `internal/agentos` | `app/agent_os_api.go` | 前者放请求、Schema、持久化和协议模型；后者绑定认证、Gateway、任务队列和真实 Agent 执行 |
| `internal/pipeline` | `app/pipeline.go` 等 | 前者放检查点、返工、输出契约、槽位等领域规则；后者持有 `GatewayService`、`AgentFactory` 并调度每一步 |
| `internal/aiemployee` | `app/ai_employee.go` | 前者放配置、模板、工作时间和质量策略；后者负责注册、匹配、派发、执行记录和 Pipeline 集成 |
| `internal/gateway/*` | `app/gateway.go` | 前者放可独立测试的 HTTP、风险、文档和迁移组件；后者拥有完整 HTTP 服务与全部路由 |
| `internal/session` | `app/mac.go`、`app/gateway.go` | 前者放 transcript/checkpoint/recovery；后者决定何时创建、保存和恢复 |

判断一段代码应该放哪，可以用一个简单问题：

> 如果把 `GatewayService`、`LLMAgent`、应用总配置和全局单例拿掉，这段逻辑还能独立成立吗？

如果能，通常应该下沉到对应的 `internal/<domain>`；如果不能，它很可能属于 `internal/app` 的装配或跨模块协调职责。

## 0. 如果只想先看懂结论

OpenKitty 当前可以概括为五层：

```text
第一层：程序入口
cmd/macs/main.go
        |
        v
第二层：应用装配和宿主
internal/app
        |
        +-------------------+--------------------+
        |                   |                    |
        v                   v                    v
第三层：入口协议       第三层：执行编排       第三层：能力目录
gateway/channels/mcp   agentloop/pipeline      skills/tools
agentos/auth           aiemployee/session      middleware/registry
        |                   |                    |
        +-------------------+--------------------+
                            |
                            v
第四层：基础运行能力
modelruntime / memory / rag / security / projectctx / processutil
                            |
                            v
第五层：外部世界
模型 API、数据库、文件系统、浏览器、Shell、聊天平台、MCP Server
```

一条普通 Web 对话的主链是：

```text
POST /api/chat
  -> GatewayService.handleChat
  -> 创建 GatewayTask
  -> processTaskWithSession
  -> 选择或临时创建 LLMAgent
  -> LLMAgent.Generate
  -> 模型返回文本或 ToolCall
  -> ToolRegistry + Middleware 执行工具
  -> 把工具结果交回模型继续思考
  -> 验证最终结果
  -> 保存消息、任务、transcript、memory
  -> JSON / SSE 返回
```

AgentOS 并没有另写一套 Agent。它是在普通执行主链外增加了一层可靠的 API、身份、持久化队列、租约、幂等、审批和事件流。

Pipeline 也不是另一个模型。它把一个大任务拆成多个有顺序、有输入输出契约、可暂停恢复的 Agent 步骤。

Skill 不是 Agent。Skill 是一套可被 Agent 激活的说明和执行能力；Tool 是模型能够发起的一次具体动作。

## 1. 程序如何启动

### 1.1 真正的可执行入口很薄

入口在 [`../cmd/macs/main.go`](../cmd/macs/main.go)：

```go
func main() {
    app.CurrentVersion = CurrentVersion
    app.Main()
}
```

这里不实现业务逻辑，只做两件事：

1. 把构建时版本号传给应用层；
2. 调用 `internal/app.Main()`。

### 1.2 `app.Main()` 做什么

主启动逻辑位于 [`app/mac.go`](app/mac.go) 的 `Main()`、`run()`、`NewBot()` 和 `ClawBot.Run()`。

启动顺序可以简化为：

```text
app.Main
  -> run
     -> 解析 --config、--port、--path、--mcp 等参数
     -> 查找并读取 openkitty-config.yaml
     -> 确定 workspace
     -> 初始化数据库
     -> 初始化 memory/todo 数据表或全局状态
     -> NewBot(configPath)
        -> LoadOrSetupConfig
        -> initWorkspace
        -> 加载 Feature Flags 和 Hooks
        -> NewMultiAgentEngine
        -> Gateway 开启时 NewGatewayService
        -> 创建 HeartbeatService
     -> ClawBot.Run
```

### 1.3 `ClawBot.Run()` 有两种工作模式

`Gateway` 开启时：

```text
启动配置监听
  -> 后台启动 Gateway HTTP 服务
  -> 后台启动 heartbeat
  -> 等待 SIGINT / SIGTERM
  -> Stop：关闭 Gateway、Channel、Heartbeat、Memory 写队列和 Engine 日志
```

`Gateway` 关闭时：

```text
启动配置监听
  -> 直接执行一次 MultiAgentEngine.Run
  -> 输出默认配置中的协作任务结果
  -> Stop
```

因此，`ClawBot` 可以理解为整个进程的生命周期总管；`MultiAgentEngine` 是 Agent 团队运行时；`GatewayService` 是对外服务宿主。

### 1.4 `--mcp` 是一条特殊短路

启动参数包含 `--mcp` 时，不会走完整 Web Gateway：

```text
openkitty --mcp
  -> 创建最小 GatewayService 宿主
  -> RunMCPStdio
  -> 通过标准输入输出提供 MCP Server
```

它用于让 Claude Code 等 MCP Host 把 OpenKitty 当成 MCP Server 调用。

普通 Web 设置的连接测试由 [`app/model_connection_api.go`](app/model_connection_api.go) 提供：`POST /api/config/model-test` 接收 `{name, model}`，返回 `{ok, code}`，不会保存配置或创建任务。它复用本机个人管理入口的同源、回环地址限制；请求体上限 32 KiB，调用绑定请求取消和 20 秒超时。隐藏密钥只能复用于原服务地址与类型，改变地址后必须显式提供密钥。测试通过 [`modelruntime/connection.go`](modelruntime/connection.go) 验证 OpenAI 兼容响应，禁止携带密钥跟随重定向；Ollama 使用兼容的 `/v1` 接口，其他原生协议需通过正常对话验证。


科技前沿的公开帖子读取复用 `internal/tools/knowledge` 的 Nova 只读客户端：`GET /api/community/feed` 提供卡片与本地阅读链接，`GET /api/community/post?id=<正整数>` 提供完整文章的展示字段。`internal/community/post.go` 不接受上游 URL、不转发账号凭据，沿用客户端的 GET、响应大小、身份核对和取消边界；`web/community.html` 在本地渲染并过滤不安全 HTML 和链接，来源取证使用的 API URL 保持原样。

`GET /api/platform/models` 是本机同源的模型目录代理，使用账号服务地址（包括现有环境覆盖）读取 `/api/macs/v1/catalog?type=all`。目录与直接生成必须指向同一账号后台，不能因选择自定义文本服务而切到另一套同名模型编号。公开读取不发送账号密钥，只返回文本、图片、音乐、视频的名称、来源、可用状态、`direct_generation` 能力和后台价格；模型调用仍使用独立的账号认证与计费接口。

目录的 `billing.price` 保留上游金额是否存在：缺失或 null 继续返回 null，不能以 Go 零值推定免费。前端的 `formatPlatformModelPrice` 统一用于模型选择卡片、Agent 独立模型提示、媒体输入区和高级设置选项，只有明确 `unit=free` 才显示免费；`per_success` 需有限且非负的积分金额，否则显示价格待确认。已有平台配置按公开目录 ID 匹配费用，`platform:default` 缺少确定默认型号时不推断价格。

`POST /api/chat` 的 `task_type=qa` 使用 `app/simple_chat.go` 调用现有 `modelruntime.CallMessages`，仅提交 system/user/assistant 文本消息，不创建 Agent、不注册工具、不识别 Skill/AI 员工/Codex/任务流命令。此模式复用普通 Web 的会话隔离、任务取消和消息持久化；附件及显式 `skill_run` 在路由前拒绝。调用采用当前选择的模型、密钥环境引用、单次尝试及最长 120 秒超时，不另建进度协议。

Web 历史消息按 `created_at`、`id` 两列确定顺序：读取最近 N 条时两列倒序截取，再反转为正序展示；导出与首末消息查询使用对应的完整排序。一次提交的用户消息和生成结果可能具有完全相同的时间，必须以消息 ID 保持先提问、后结果，避免刷新或分页后顺序反转。

`internal/auth/platform_media.go` 提供同源的 `/api/platform/media/{image|music|video}`：POST 提交选定 `platform:<id>` 的生成请求，GET 查看当前账号记录，GET `/<id>` 查询持久任务，音乐 GET `/<id>/audio` 通过账号会话转发音频并支持 Range。它只允许固定平台路径、限定字段及响应大小；前端不能选择上游 URL、账号、密钥或收费金额。图片和视频复用平台 `/api/macs/v1/creation/*`，音乐复用 `/api/macs/v1/music/jobs*`；实际生成和结算仍由 Nova 现有引擎负责。会话凭据禁止随重定向转发；不自动重试生成提交，不确定结果要求用户先核对记录。响应仅保留展示字段，不透传供应商私有响应或音乐请求中的 API Key。

对话内直接生成由 [`app/conversation_media_api.go`](app/conversation_media_api.go) 装配，消息快照合同在 [`conversation/generation.go`](conversation/generation.go)。`POST /api/conversation/media` 接收本地 `request_id`（32 位十六进制）、现有 `web_` 会话、实际平台型号、提示和生成参数；图片、视频可附当前工作区 `storage/` 下的 1 张图片（最多 10 MiB）。本机后端先验证目录、身份和附件，再用数据库事务绑定会话账号、保存用户消息及唯一请求预留，之后仅提交一次平台调用。相同账号和载荷重复提交同一 `request_id` 只返回已有快照；冲突拒绝，不重放收费操作。`ChatMessage.Generation` 保存无凭据的模型、平台任务和结果，`MediaOwner` 保存平台地址与账号的摘要，读历史、搜索和导出均按该身份过滤。

`GET /api/conversation/media/<request_id>` 读取快照，非终态有平台任务号时更新持久状态；没有任务号的中断提交标为 `submission_unknown`，需核对平台记录后显式开始新请求。它复用平台已有持久任务接口，不创建 AgentOS 任务、不新增进度协议。`POST /api/conversation/media/<request_id>/reference` 仅从当前账号保存的已完成图片结果取回同平台 `/uploads/` 资源，不发送账号凭据、不跟随重定向，验证大小和图片格式后保存为当前工作区附件。`/api/platform/media/upload` 和 `/config` 封装固定平台参考图上传及视频能力读取，前者重新构造且验证图片 multipart，后者只返回公开能力字段。所有操作保留本机回环、同源、会话所属工作区、请求取消和响应大小边界。

`GET /api/conversation/media/<request_id>/image` 复用同一账号、会话和工作区校验，仅读取该消息保存的已完成图片。`auth/platform_image.go` 限制同平台 `/uploads/` 路径、禁止重定向及发送账号凭据，并限制 20 MiB 图片与 20 秒请求时间；结果私有且不缓存。参考图复用仍限制 10 MiB。浏览器导出通过该接口和既有工作区文件接口将图片内嵌为 data URI，避免平台缺少 CORS 时截图遗漏图片；读取失败中止导出。`conversation/export.go` 负责无 I/O 的媒体与实际计费快照 HTML，宿主导出加载图片但不修改原消息或再次调用生成模型。音乐导出保留标题、播放器、歌词与播放链接，PNG 使用静态唱片卡片。

Web 端 [`../web/conversation_media.js`](../web/conversation_media.js) 复用普通输入框、附件预览及历史消息渲染，选图片、音乐、视频即切换到简单问答，结果显示为可预览图片或可播放媒体。轮询只读任务，刷新或重开会话不重新提交；切换账号停止旧轮询并重载可见记录。独立创作窗口保留平台历史核对入口。

Web 网站账号由 `internal/auth/AccountHandler` 管理：`POST /api/account/login` 代平台登录并建立 HttpOnly 本机会话，`GET /api/account/me` 确认身份，`GET /api/account/summary` 通过服务器持有的平台会话并发读取 `/nova/wallet` 和 `/nova/wallet/api-keys`，仅返回用户名、积分余额和启用/总密钥数量，不转发平台 Token、Key、Secret 或完整上游响应。摘要请求有同源检查、8 秒总超时、响应大小上限及禁止重定向的凭据边界，单项失败返回空值，平台返回 401 时撤销本机会话。`POST /api/account/logout` 清除会话并通知平台退出。会话保存在进程内，重启后需重新登录；浏览器无需跨站 Cookie。`GET /api/account/config` 保留既有外部服务 URL，并补充 `local_login_url`、`local_me_url`、`local_logout_url`、`summary_url` 供本机客户端发现。

## 2. `NewMultiAgentEngine` 如何把 Agent 建起来

核心实现位于 [`app/mac.go`](app/mac.go)。

`NewMultiAgentEngine(config)` 主要完成：

1. 校验模型、Agent、Gateway、沙箱等配置；
2. 创建执行记录器和会议纪要对象；
3. 创建日志目录与 Engine 日志文件；
4. 创建 `SessionManager`；
5. 创建 `AgentFactory`；
6. 按配置创建所有 Agent；
7. 给 Agent 注册 sub-agent / collaboration 等运行时工具；
8. 启动过期 session 清理协程。

这里的几个对象不要混淆：

| 对象 | 含义 |
| --- | --- |
| `Agent` | 最小接口，核心能力是 `Generate(ctx, input)` |
| `LLMAgent` | 真实的 LLM Agent，实现提示词、模型调用、工具循环、记忆和结果校验 |
| `AgentFactory` | 根据 Agent 配置和模型配置创建/缓存 Agent，也能创建隔离的临时 Agent |
| `MultiAgentEngine` | 管理多个 Agent、协作模式、session 和执行记录 |
| `ClawBot` | 管理整个应用的启动、热更新和停止 |

### 2.1 Agent 不是只有一个模型地址

`NewLLMAgent` 会把一批运行能力装到 Agent 身上，包括：

- 模型配置与降级策略；
- Tool Registry；
- Middleware Chain；
- Skills Loader；
- Memory 与 Session Context；
- Sandbox 与 Permission Engine；
- Hooks；
- Transcript 和恢复检查点；
- workspace 和文件历史；
- 最大迭代次数、Token 限制、并发限制。

因此 `LLMAgent.Generate()` 不是简单的“请求一次大模型 API”，而是一个完整的 Agent 回合执行器。

## 3. 普通对话请求如何跑完

### 3.1 HTTP 入口

Gateway 在 [`app/gateway.go`](app/gateway.go) 的 `GatewayService.Start()` 中注册路由。

普通聊天入口是：

```text
POST /api/chat
```

对应 `GatewayService.handleChat()`。

`handleChat` 的主要职责是入口校验和建任务，而不是直接调用模型：

1. 检查请求方法、Content-Type、请求大小和必要字段；
2. 处理 multipart 附件或普通 JSON；
3. 解析显式 `@skill`；
4. 解析 `/pipeline <name>` 或 `/run <name>` 快捷命令；
5. 校验 Agent 名称，防止路径穿越式名称；
6. 绑定 session 对应的 workspace；
7. 生成任务 ID；
8. 保存用户消息；
9. 创建 `GatewayTask`、进度通道和中断通道；
10. 异步调用 `processTaskWithSession`；
11. 非流式请求先返回任务 ID，流式请求进入 SSE 输出。

### 3.2 `processTaskWithSession` 是 Gateway 到 Agent 的桥

这个函数仍在 [`app/gateway.go`](app/gateway.go)。它负责把 HTTP 世界转换成 Agent 执行上下文：

```text
GatewayTask
  -> 写入 task_id、session_id、channel、user_id
  -> 写入 tenant / subject / role / service identity
  -> 绑定真实 workspace
  -> 加载最近会话历史
  -> 按 task_type 选择 chat、skill、task 或 collaboration
  -> 选择已有 Agent，或为该 workspace 创建临时 Agent
  -> agent.Generate(ctx, message)
  -> 保存结果、任务状态和 assistant 消息
  -> 广播进度并结束 JSON/SSE
```

显式 AI Employee、Skill Run 和 AgentOS 任务也会在这里进入各自分支，但最终仍复用 Agent/Pipeline 的执行能力。

### 3.3 中断为什么能生效

每个 `GatewayTask` 有自己的 `InterruptCh` 和可取消 context。

Agent 执行期间如果 `/api/interrupt` 收到补充消息：

1. 当前模型调用的 context 被取消；
2. 新消息追加到当前输入和对话历史；
3. 中断消息持久化；
4. 使用补充后的上下文重新调用 `Generate`。

它不是另起一个无关任务，而是中止当前 generation 后继续同一任务语义。

## 4. `LLMAgent.Generate()` 内部到底做了什么

这是理解 OpenKitty 的最关键函数，位于 [`app/mac.go`](app/mac.go)。可以把它分成六个阶段。

### 阶段 A：建立本轮安全和身份边界

进入 `Generate` 后，代码会把这些信息绑定到 context：

- 当前 Agent；
- workspace；
- task/session/user/tenant；
- sandbox policy；
- 本轮工具激活状态；
- 显式 Skill 要求；
- Pipeline step 或 AI Employee 身份；
- 模型输出 Token 上限。

租户任务会先检查 workspace 越界。如果请求本身要求访问其他租户或边界外目录，会在模型和工具执行前直接拒绝。

### 阶段 B：建立 transcript 和恢复状态

每个回合都会创建 `SessionTranscript`，记录：

- 用户消息；
- 模型响应；
- ToolCall 和工具结果；
- 生命周期事件；
- 最终结果；
- 可恢复 checkpoint。

如果这是恢复任务，则从 `TurnCheckpoint` 还原消息、迭代次数、待执行工具和计划状态，而不是从头重新思考。

### 阶段 C：构造提示词和上下文

提示词被拆成两部分：

- 静态部分：全局规则、工具使用原则、基础身份，尽量让模型缓存命中；
- 动态部分：当前 Agent/AI Employee 身份、workspace、租户边界、Skill Run、当前时间等。

用户消息前还会按条件加入：

- 最近会话历史；
- Memory 召回；
- Session Context；
- Knowledge Graph；
- 自动匹配到的 Skill 摘要；
- 当前产物和 Pipeline 执行约束。

### 阶段 D：模型—工具循环

这是 Agent 的核心循环：

```text
调用模型
  |
  +-- 只返回最终文本 ----------> 进入最终答案校验
  |
  +-- 返回一个或多个 ToolCall
          -> 为 ToolCall 补稳定 ID
          -> 保存 tools_pending checkpoint
          -> 审批预检
          -> Guardrail / 重复调用检查
          -> 判断能否并行执行
          -> ToolRegistry.Execute
          -> Middleware BeforeTool
          -> 具体 Tool.Execute
          -> Middleware AfterTool
          -> 规范化、脱敏、压缩工具结果
          -> 把 tool message 追加回模型消息
          -> 保存 after_tools checkpoint
          -> 下一轮再次调用模型
```

多工具只有在彼此独立、没有审批等待、没有目标文件冲突且不属于危险组合时才会并行，否则串行执行。

### 阶段 E：防止“看似完成，实际没完成”

最终文本不会无条件放行。代码还会检查：

- 模型是否只说“我将处理”，却没有给最终结果；
- 模型是否伪造了没有真正执行的 ToolCall；
- 用户明确要求的文件是否真的写出；
- 写出的文件是否完整回读并满足字段契约；
- PPT、二进制产物、研究引用是否满足交付条件；
- 显式 Skill 是否按要求完成并产生产物；
- 当前计划是否还有未完成步骤；
- 工具是否陷入重复失败或无进展循环；
- 外部高风险事实是否有真实证据支持。

部分问题允许一次有边界的修复；仍不满足时会明确返回失败或降级结果，不把进度描述冒充完成结果。

### 阶段 F：收尾和持久化

完成后会：

- 清理对外答案中的内部控制信息；
- 保存最终答案和任务日志；
- 提取 session memory；
- 更新 Knowledge Graph 或长期记忆；
- 保存工具统计；
- 完成 transcript；
- 触发 Hook 和进度事件；
- 返回标准化的成功或失败结果。

## 5. Tool 系统如何工作

### 5.1 三层结构

Tool 相关代码不是都应该放在一个目录，当前分成三层：

| 层 | 目录 | 职责 |
| --- | --- | --- |
| 合同层 | `toolcontract` | 定义 Tool、Call、Result、回执、读写属性、并行安全和结果规范 |
| 注册执行层 | `toolregistry` | 注册、查找、按策略筛选、生成模型 Schema、串接 Middleware、执行 Tool |
| 具体实现层 | `tools/*` | 文件、网络、浏览器、开发、知识库等真实动作 |

一个 Tool 的最小思想是：

```go
Name() string
Description() string
Parameters() map[string]interface{}
Execute(ctx context.Context, params map[string]interface{}) (string, error)
```

模型看到的是名称、描述和参数 Schema；运行时拿到的是具体 Tool 对象。

### 5.2 为什么有 `tool_detail`

工具很多，如果把所有完整 Schema 都塞进每次模型请求，会浪费大量 Token。

`toolregistry` 把少数核心工具直接暴露，把扩展工具先以摘要形式提供。模型需要某个扩展工具时，先调用 `tool_detail` 搜索或激活完整 Schema。激活状态只属于当前用户回合，避免不同 session 相互污染。

### 5.3 Middleware 在哪里生效

[`middleware`](middleware) 不是 HTTP 中间件，而主要是 Agent/Tool 执行中间件。

`Registry.Execute` 会把真实 `Tool.Execute` 包在 Middleware Chain 中：

```text
BeforeTool
  -> 可以清洗输入、检查权限、要求澄清、阻止危险路径或预算超限
  -> Tool.Execute
AfterTool
  -> 可以过滤输出、修复悬空 ToolCall、统计错误和 Token
```

主要中间件包括：

- 输入清洗；
- permission / execution policy；
- path write guard；
- clarification；
- loop detection；
- tool error；
- tool output budget；
- response filter；
- token budget；
- safety finish reason。

### 5.4 `internal/tools` 的 17 类工具

| 子目录 | 主要能力 |
| --- | --- |
| `agents` | sub-agent、协作、swarm、主动模式、cron 和运行控制 |
| `analysis` | 数据科学、SQL EDA、高级分析 |
| `browser` | CDP 浏览器页面操作 |
| `business` | 客服、客户上下文、Lead、营销和业务 Schema |
| `content` | 文档解析/生成/转换、PDF、PPTX、表格、OCR、视觉、音视频 |
| `desktop` | 本机桌面任务、窗口/键鼠/截图审计 |
| `development` | build、debug、AST、LSP、重构、代码审查、Git、worktree |
| `knowledge` | RAG、索引、embedding、知识库、资料获取、能力目录 |
| `legal` | 法律领域工具 |
| `management` | 管理类工具集合 |
| `network` | HTTP/API、搜索、网页抓取、邮件、天气、股票和社交平台 |
| `operations` | 数据库、容器、CI/CD、远程主机、系统运维、任务和遥测 |
| `plugins` | Plugin 发现、启停和管理 |
| `security` | PII、Shell 安全分析和沙箱辅助 |
| `skills` | Skill 列表、激活、创建、策展、市场和 ClawHub |
| `testing` | 测试生成、执行、修复重试和目标验收 |
| `workspace` | 文件读写编辑、grep/glob、项目地图、notepad、REPL、snapshot |

新增 Tool 时，真实实现应优先放进对应 `internal/tools/<category>`，`internal/app` 只负责注入它需要的依赖并注册。

`goal_verify` 的 PASS/FAIL/PARTIAL、行为断言与命令取消合同见[目标验收说明](../docs/architecture/goal-verification.md)。

## 6. Skill 系统如何工作

Skill 代码主要分布在：

- [`skills`](skills)：加载、解析、检索、缓存、安全和显式 Skill 契约；
- [`tools/skills`](tools/skills)：提供给模型调用的 `use_skill`、创建、安装和管理工具；
- [`app/skill_run.go`](app/skill_run.go)：把一次指定 Skill 执行绑定到 GatewayTask，并记录快照与回执。

### 6.1 普通自动匹配

普通对话进入 `LLMAgent.Generate` 后，`SkillsLoader` 会按用户问题检索相关 Skill，把最匹配的摘要放进上下文。模型确实需要完整流程时，再调用 `use_skill` 激活详细说明。

`GET /api/skills` 为每项技能增加只用于展示的 `category`，由 `internal/skills/catalog_category.go` 根据标签、用途名称、内置索引、内置资源分组及说明确定，无法判断时为 `other`。加载器在同一读锁下生成分类快照；磁盘中尚未加载的用户技能复用同一个分类函数，不向客户端公开文件路径。`web/skill_panel.js` 使用分类筛选和卡片标签，搜索与分类共同筛选且数量跟随搜索更新；分类不改变匹配、激活或执行权限，也不调用模型。

### 6.2 显式 `@skill`

用户明确写 `@elder` 这类请求时，Gateway 会在建任务前解析 mention：

```text
消息文本
  -> resolveSkillMention
  -> 解析 Skill 名与剩余任务文本
  -> 生成 SkillRunRequest
  -> beginSkillRun
  -> 校验参数
  -> 固化当前 Skill bundle 和 digest
  -> 把 Skill Run 契约注入 Agent context
  -> Agent 按冻结版本执行
  -> finishSkillRun 记录 phase、产物和 receipt
```

“冻结版本”很重要：任务开始后即使磁盘上的 Skill 被热更新，当前任务也继续使用开始时的内容，保证可重复和可审计。

### 6.3 Skill 支持哪些执行方式

`SkillsLoader` 注册了 Python、Node/JS、Shell、HTTP 和 Builtin executor。纯说明型 Builtin Skill 可以只向模型注入工作流；带 runtime 的 Skill 会在当前授权 workspace 中准备独立运行目录。

Skill 本身不能扩大权限。它最后调用文件、网络或进程能力时，仍要经过本轮 sandbox、permission 和 Tool Middleware。

## 7. AgentOS 如何工作

### 7.1 AgentOS 是可靠 API 外壳

AgentOS 入口通常位于：

```text
/api/v1/agent/roles
/api/v1/agent/tasks
/api/v1/agent/tasks/{id}
/api/v1/agent/tasks/{id}/events
/api/v1/agent/tasks/{id}/cancel
/api/v1/agent/tasks/{id}/approvals
/api/v1/agent/files
```

路由表和通用 HTTP 边界在 [`gateway/httpx`](gateway/httpx)，具体处理器和队列绑定在 [`app/agent_os_api.go`](app/agent_os_api.go)。

### 7.2 提交任务时发生什么

`POST /api/v1/agent/tasks` 的简化流程是：

```text
RequestMiddleware + CORS + RateLimit
  -> authenticateAgentOSRequest
  -> 检查 tasks:submit scope
  -> 解析并限制 JSON 大小
  -> 校验 initial_messages、assistant_identity、skill_run
  -> 解析 service role 到真实 agent
  -> 建立 tenant + subject 身份
  -> 为用户绑定私有 workspace 和 session
  -> 规范化附件
  -> 创建 GatewayTask(status=pending, phase=queued)
  -> 在一个数据库事务中保存队列记录与用户消息
  -> 处理 Idempotency-Key
  -> 唤醒队列
  -> HTTP 202 返回 task_id 和查询链接
```

API 返回 `running` 或 `pending` 只代表任务已接收/已开始，不代表已经产生最终内容。客户端需要轮询 task URL 或订阅 events，直到 `completed`、`degraded`、`failed`、`stopped` 等终态。

### 7.3 队列为什么有 lease

`agentOSQueueRuntime` 使用数据库队列和租约：

1. Worker 查找可执行的 `pending` 任务，或租约已过期的 `running` 任务；
2. 原子更新 `queue_owner`、`lease_token`、`lease_until`；
3. 启动任务并周期续租；
4. 执行期间仍调用 `processTaskWithSession`，所以与普通对话共用 Agent 主链；
5. Worker 崩溃或停止续租后，其他 Worker 可以重新认领；
6. 关机时先停止接单，等待活动任务，超时则把自己拥有的任务重新排队。

这解决的是“HTTP 请求断了、进程重启了、多个 Worker 同时抢任务”时如何不丢任务、不重复执行的问题。

### 7.4 `internal/agentos` 放什么

| 文件方向 | 职责 |
| --- | --- |
| `request.go` / `http_contract.go` | 请求上下文、JSON 错误、HTTP 协议约定 |
| `schema.go` | 输入输出 JSON Schema 校验 |
| `runtime_model.go` | 队列任务、幂等、工具回执等持久化模型 |
| `execution_runtime.go` | 执行消息、租约和运行时辅助 |
| `artifact.go` / `upload.go` | 产物和上传边界 |

这些代码不持有完整 `GatewayService`；真正“认证后调用哪个 Agent、如何排队”的逻辑仍在 `app`。

## 8. Pipeline 如何工作

### 8.1 Pipeline 是有状态的多步骤执行

入口对象是 [`app/pipeline.go`](app/pipeline.go) 中的 `PipelineExecutor`。

`PipelineExecutor.Run` 会：

1. 从 YAML/数据库、动态注册表或内置模板中找到 Pipeline；
2. 注入 AI Employee intake step（如果当前上下文要求）；
3. 校验返工、输出契约和数据库连接；
4. 生成 execution ID 和稳定的 parent task ID；
5. 固定本次运行使用的 Pipeline 快照和 workspace；
6. 初始化每个 StepResult、slot 和 checkpoint；
7. 保存到数据库；
8. 后台进入 interactive 或 API 执行模式。

### 8.2 每一步怎么执行

`runStepsFrom` 负责步骤循环。一个 Step 可以是：

- 普通 Agent prompt；
- 指定 Professional Agent；
- 多个并行分支；
- 无工具的纯整理步骤；
- 需要人工审批的 HITL 步骤；
- 带路由、重复、返工和输出契约的步骤；
- 读取上游 slot 或产物的步骤。

每一步执行前后都会更新 checkpoint 和数据库。进程中断后，可以从当前 Step 恢复，不必从第 0 步全部重跑。

### 8.3 三个 Pipeline 目录的区别

| 位置 | 放什么 |
| --- | --- |
| `internal/app/pipeline.go` | 真实 Executor、Step 调度、Agent/Gateway/DB 集成 |
| `internal/pipeline` | checkpoint、durable、rework、slot、source/output contract 等中立规则 |
| `internal/pipelines/*` | 具体内置 Pipeline，例如 `investmentadvisory`、`pptpolish` |

新增一个独立的内置 Pipeline 模板，应优先放到 `internal/pipelines/<name>`；如果只是通用的 Pipeline 规则，应放到 `internal/pipeline`；只有需要调用 Agent、Gateway 或数据库宿主的编排才留在 `app`。

### 8.4 返工与恢复不是一回事

- 恢复：进程中断或等待审批后，从 checkpoint 继续同一次执行；
- 返工：某一步产物不满足审计或输出契约，提升 revision 后重新做相关步骤；
- retry：同一步的暂时性调用失败，在有限次数内再次尝试。

三者有不同的状态和审计含义，不应混为一次普通循环。

## 9. AI Employee 如何工作

AI Employee 是“带固定身份、模型绑定、工具范围、工作流和工作记录的长期角色”。它可以直接执行，也可以把任务派给 Pipeline。

主要流程位于 [`app/ai_employee.go`](app/ai_employee.go)：

```text
配置或数据库中的 AIEmployeeConfig
  -> AIEmployeeManager.Register
  -> 用户 @员工名 / AgentOS 显式指定 / 定时任务
  -> MatchAtName / Dispatch
  -> 预检输入、文件和工作区
  -> 选择 direct pipeline 或配置 pipeline
  -> 绑定 employee execution identity
  -> PipelineExecutor.Run
  -> 记录 AEWorkRecord
  -> 发送进度、结果和交付物
```

`internal/aiemployee` 则放不依赖宿主的配置和策略：

- `config.go`：员工配置模型；
- `mention.go`：mention 解析；
- `intake.go`：任务 intake；
- `templates.go`：默认模板；
- `cron_scheduler.go`：工作时间和调度辅助；
- `domain_quality.go`、`output_policy.go`：领域质量和输出策略；
- `minimum_progress.go`、`final_recovery.go`：最低进展和最终恢复判断；
- `records.go`：工作记录模型；
- `supervisor_router.go`：主管路由判断。

AI Employee 和 Pipeline 不应合成同一个目录：前者是长期角色和派工主体，后者是任务步骤编排机制。它们是“谁来工作”和“工作分几步”的关系。

## 10. Channel 与 Gateway 的关系

`Gateway` 是服务入口和任务宿主，`Channel` 是飞书、微信、钉钉、Telegram、QQ、邮件等消息平台适配器。

Channel 相关分成两层：

- [`channels`](channels)：各平台协议、媒体、语音、消息投递实现；
- [`channelhost`](channelhost)：Channel 生命周期、配置、注册管理，以及接入宿主的中立接口。

Gateway 创建 `channelhost.Manager`，并通过 runtime adapter 给 Channel 提供：

- 收到消息后如何交给 Agent；
- 如何保存 session；
- 如何发送结果和产物；
- 如何处理审批卡片；
- 如何报告状态。

因此平台 SDK 细节放 `channels`，认识 `GatewayService` 的胶水留在 `app/gateway.go`。

## 11. Memory、Session、Conversation 和 RAG 不要混淆

| 模块 | 解决的问题 |
| --- | --- |
| `conversation` | 对话消息压缩边界和缓存清理 |
| `session` | 一次/多次会话的 transcript、journal、turn checkpoint 和恢复 |
| `memory` | 用户、项目、session 的长期事实与主动记忆 |
| `ragmodel` | 知识库、Chunk、FAQ、SearchLog 等数据库模型和分词/BM25 辅助 |
| `rag` | 把数据库、索引、embedding、混合检索串起来 |
| `tools/knowledge` | 暴露给 Agent 的 RAG/知识库/资料获取工具 |
| `compaction` | 更底层的消息压缩算法 |

一句话区分：

- Session 回答“这一段对话发生了什么”；
- Memory 回答“这个用户或项目有哪些值得长期记住的事实”；
- RAG 回答“知识库里有哪些文档能支撑当前问题”；
- Conversation/Compaction 回答“上下文太长时保留什么、压缩什么”。

手工记忆的 scope、会话可见性、旧数据迁移和持久化回执契约见
[`docs/architecture/memory.md`](../docs/architecture/memory.md)。`agent_memory` 的
`user` 和 `project` 当前均限定在 workspace 内，`session` 只对当前会话可见。

## 12. 其他目录按职责怎么理解

### 12.1 入口与协议

| 目录 | 主要职责 |
| --- | --- |
| `agentos` | AgentOS 协议、Schema、队列模型、上传与产物合同 |
| `auth` | Tenant session、SSO、账户认证 |
| `gateway/document` | 文档分析和分类 HTTP Handler |
| `gateway/httpx` | Gateway 可复用 HTTP 边界、AgentOS 路由、身份和访问范围 |
| `gateway/migrations` | Gateway 数据库迁移 |
| `gateway/risk` | 请求内容风险与 AgentOS 安全提示 |
| `mcp` | MCP Client、Server、Transport、Tool Adapter 和 Resource Tool |
| `routing` | 意图和能力路由规则 |

### 12.2 Agent 与执行编排

| 目录 | 主要职责 |
| --- | --- |
| `agentloop` | TurnState、计划、工具失败恢复、lease 和流式执行辅助 |
| `agentresult` | 最终结果和终止型工具失败的标准表达 |
| `agents` | Agent 领域辅助类型 |
| `aiemployee` | AI Employee 配置和纯策略 |
| `approval` | 工具审批记录与审批状态 |
| `executioninspect` | 执行记录解析和检查 |
| `llmapp` | LLM App 工具与其系统提示、控制协议 |
| `pipeline` | Pipeline 的中立领域规则 |
| `pipelines` | 具体内置 Pipeline 模板 |
| `app/professional_agent.go` | 专业 Agent 的宿主构造和调用适配 |

### 12.3 数据、结果与事件

| 目录 | 主要职责 |
| --- | --- |
| `artifacts/*` | 二进制、股票、天气等结构化产物模型 |
| `attachments` | 附件规范化和可见消息构造 |
| `eventbus` | 进程内事件发布订阅和前端进度桥接 |
| `hooks` | Hook 配置、生命周期、执行器和管理工具 |
| `meetingminutes` | 多 Agent 会议纪要 |
| `runevents` | 运行事件和 OTLP 导出 |
| `todo` | Todo 模型与管理器 |

### 12.4 模型和知识

| 目录 | 主要职责 |
| --- | --- |
| `modelruntime` | 模型调用重试、超时、fallback、健康检查、输出策略和本地 LLM |
| `memory` | 长期记忆、知识图谱、session context 和写队列 |
| `rag` / `ragmodel` | 检索执行与持久化模型 |
| `research` | 研究任务的证据、时效、交付和 fast path 规则 |

### 12.5 安全、权限和隔离

| 目录 | 主要职责 |
| --- | --- |
| `security` | Token、身份、凭据边界、证据规则、TLS、License、安全 Hook |
| `permissionpolicy` | Permission Mode、Plan Mode 和工具动作决策 |
| `shellpolicy` | Shell 命令风险分类，不直接执行命令 |
| `tenant` | 租户配置和租户目录管理 |
| `projectctx` | workspace 规范化、项目/客户上下文和私有工作区 |
| `skillpath` | Skill 路径、接口元数据与安装位置规则 |

### 12.6 平台基础能力

| 目录 | 主要职责 |
| --- | --- |
| `browserruntime` | Chromium 安装缓存和浏览器任务生命周期 |
| `buildinfo` | 版本参数和版本输出 |
| `codeutil` | 代码文本辅助 |
| `coding` | 编码任务的交付和验证规则 |
| `configvalue` | 配置取值、默认值、环境变量和损坏配置恢复 |
| `featureflags` | 线程安全功能开关 |
| `fsutil` | 原子写入和文件候选定位 |
| `gitutil` | Git 辅助 |
| `globalstate` | 暂时仍由应用共享的全局运行对象 |
| `nativeenv` | 本机运行环境识别 |
| `portablepath` | Windows、Unix、UNC 路径兼容处理 |
| `processutil` | 子进程、PTY、命令树、Shell session 和 Python 运行时定位 |
| `ptc` | PTC 执行能力 |
| `runtimeutil` | deadline、heartbeat、Token 估算等轻量运行辅助 |
| `spreadsheet` | 表格领域辅助 |
| `textnormalize` / `textutil` | 模型文本清理、截断、标题和通用格式化 |
| `validationhint` | 根据改动文件推导最小验证命令 |
| `voice` | 唤醒和语音入口运行时 |

## 13. `internal/app` 当前 27 个文件分别是什么

### 主运行链

| 文件 | 主要职责 |
| --- | --- |
| `mac.go` | 应用主配置、LLMAgent、AgentFactory、MultiAgentEngine、ClawBot、CLI 启动 |
| `gateway.go` | GatewayService、HTTP 路由、任务/session、SSE/WS、Channel 和大量宿主适配 |
| `query_engine.go` | Tool 消息、执行状态、循环检测、结果处理和单/并行 Tool Executor |
| `model_provider.go` | 模型提供商和路由配置 |
| `sandbox_policy.go` | 应用配置到沙箱/权限执行策略的适配 |
| `execution_identity.go` | tenant、user、role、service 等执行身份在 context 中的绑定 |
| `tenant_execution_boundary.go` | 租户 workspace 和输出边界的宿主检查 |

### AgentOS、AI Employee 和 Pipeline

| 文件 | 主要职责 |
| --- | --- |
| `agent_os_api.go` | AgentOS Handler、认证绑定、持久化队列、lease、roles、events、artifact |
| `ai_employee.go` | AIEmployeeManager、注册、mention、派发、工作记录和 Pipeline 集成 |
| `pipeline.go` | Pipeline 模型宿主、Executor、Step 调度、持久化和恢复 |
| `pipeline_rework.go` | app 层返工执行与 revision 处理 |
| `pipeline_source_contract.go` | app 层文件读取、产物路径和来源契约适配 |
| `presentation_runtime_gate.go` | PPT 产物生成、回读、QA 与交付门禁 |
| `professional_agent.go` | 专业 Agent 的隔离构造和运行 |
| `artifact_center.go` | 产物索引、解析、交付与 Gateway 集成 |

### Skill、子 Agent 和专业流程

| 文件 | 主要职责 |
| --- | --- |
| `skill_run.go` | 显式 Skill Run、冻结 bundle、参数、phase、receipt 和 Skill API |
| `sub_agent_tool.go` | 子 Agent/Swarm 的创建、隔离和并行执行适配 |
| `assemble_team.go` | 动态组建 Agent 团队 |
| `research_fastpath.go` | 研究任务的宿主级快速路径和工具约束 |
| `rag_api.go` | RAG HTTP API 与数据层集成 |
| `ai_employee.go` | 同时也是 AI Employee 专业流程的主要宿主 |

### Coding、Codex 和其他宿主能力

| 文件 | 主要职责 |
| --- | --- |
| `coding_session.go` | 编码 session 生命周期 |
| `codex_interactive.go` | Codex 交互会话管理 |
| `codex_app_server.go` | Codex App Server 协议宿主 |
| `openai_compat.go` | OpenAI 兼容的 models/chat completions API |
| `autonomous_worker.go` | 历史自主 Worker 宿主逻辑，目前调度主责已更多交给 Todo/Pipeline |
| `recovery_host.go` | 任务和 session 恢复的应用级协调 |
| `config_values.go` | 应用总配置的动态读取适配 |
| `agent_os_api.go` | 同时承载 AgentOS data plane 的应用绑定 |

文件表出现一个文件覆盖多个主题，正说明 `app` 仍是后续瘦身重点。迁移时应先抽出稳定接口，再移动实现，不能只按文件名前缀机械移动。

## 14. 当前测试目录需要特别说明

当前 [`test`](test) 下有 343 个 `_test.go` 文件，并且它们都声明为：

```go
package app
```

但 Go 以“目录”而不是 `package` 声明文字来确定编译包。也就是说：

```text
internal/app   -> import path: mac/internal/app
internal/test  -> import path: mac/internal/test
```

即使两个目录都写 `package app`，它们也不是同一个编译包。`internal/test` 里的测试如果直接使用 `internal/app` 的未导出符号，就会出现 `undefined`。

因此当前 `internal/test` 更像临时集中停放区，不是最终推荐结构。后续整理测试时应按被测实现归位：

```text
测试 app 的私有逻辑       -> internal/app/*_test.go，package app
只测 app 公共 API         -> 可用 package app_test 并显式 import mac/internal/app
测试 pipeline 领域逻辑    -> internal/pipeline/*_test.go
测试具体工具              -> internal/tools/<category>/*_test.go
测试 skills               -> internal/skills/*_test.go
```

这不是代码风格问题，而是会直接影响 `go test ./...` 是否能编译。

## 15. 修改功能时从哪里下手

| 想改的功能 | 先看这里 | 通常还会涉及 |
| --- | --- | --- |
| 普通聊天 API | `app/gateway.go: handleChat` | `processTaskWithSession`、`LLMAgent.Generate` |
| API Key / AgentOS | `app/agent_os_api.go` | `agentos`、`gateway/httpx`、`auth`、`security` |
| 模型调用和降级 | `app/mac.go: callLLM*` | `modelruntime`、`model_provider.go` |
| 工具执行异常 | `app/query_engine.go` | `toolregistry`、`middleware`、具体 `tools/*` |
| 新增文件/网络工具 | 对应 `tools/<category>` | `toolcontract`、`app` 中的注册点 |
| `@skill` 不生效 | `app/skill_run.go` | `skills/mention.go`、`skills/loader.go`、`tools/skills` |
| Pipeline 步骤错误 | `app/pipeline.go` | `pipeline`、`pipelines/<name>` |
| AI Employee 路由错误 | `app/ai_employee.go` | `aiemployee/mention.go`、`supervisor_router.go` |
| session 丢失或无法恢复 | `session` | `app/recovery_host.go`、Gateway 持久化 |
| workspace 越界 | `projectctx`、`security` | `tenant_execution_boundary.go`、Middleware |
| RAG 结果不对 | `rag/search.go` | `ragmodel`、`tools/knowledge` |
| 飞书/微信等平台问题 | `channels/<platform>.go` | `channelhost`、Gateway runtime adapter |
| MCP 工具问题 | `mcp` | `app` 中 MCP 注册和 `RunMCPStdio` |

## 16. 推荐的源码阅读顺序

### 只想看懂一次普通对话

按这个顺序最省时间：

1. `cmd/macs/main.go`；
2. `app/mac.go` 的 `Main`、`run`、`NewBot`、`ClawBot.Run`；
3. `app/gateway.go` 的 `GatewayService.Start`、`handleChat`、`processTaskWithSession`；
4. `app/mac.go` 的 `NewLLMAgent`、`LLMAgent.Generate`；
5. `app/query_engine.go`；
6. `toolregistry/registry.go`；
7. `middleware/chain.go`；
8. 任选一个简单 Tool，例如 `tools/workspace/list_dir.go`。

### 想看懂 AgentOS

1. `agentos/http_contract.go` 和 `agentos/runtime_model.go`；
2. `gateway/httpx/agentos_server.go`；
3. `app/agent_os_api.go` 的 `agentOSDataHandler`；
4. `handleAgentOSTasks` 和 `submitAgentOSTask`；
5. `agentOSQueueRuntime.run`、`claimOne`、`startClaimedTask`；
6. 回到 `app/gateway.go: processTaskWithSession`；
7. 最后读 `handleAgentOSTaskEvents` 和审批/取消处理。

### 想看懂 Pipeline

1. `internal/pipeline/config.go`；
2. `internal/pipelines/<具体流程>`；
3. `app/pipeline.go: PipelineExecutor.Run`；
4. `app/pipeline.go: runStepsFrom`；
5. `internal/pipeline/checkpoint.go`；
6. `internal/pipeline/output_contract.go`、`rework.go`、`slots.go`；
7. 再看 `app/pipeline_source_contract.go` 和 `pipeline_rework.go`。

### 想看懂 Skill

1. `skills/loader.go`；
2. `skills/mention.go`；
3. `tools/skills/access.go`；
4. `app/skill_run.go: resolveSkillMention`；
5. `beginSkillRun` 和 `finishSkillRun`；
6. `skills/explicit_policy.go`。

## 17. 后续继续瘦身 `internal/app` 的判断原则

建议按下面顺序继续，而不是按文件名批量搬：

1. 先找只依赖标准库或底层合同的纯函数；
2. 给目标包定义最小输入/输出类型；
3. 让 `app` 通过 interface、callback 或小型 DTO 注入宿主依赖；
4. 把实现和对应测试一起迁移；
5. 删除旧实现，不保留没有真实调用价值的兼容层；
6. 用 import graph 检查不能让领域包反向导入 `internal/app`；
7. 最后再处理需要同时认识 Gateway、Agent、DB 的跨模块代码。

最值得优先继续拆的是：

- `gateway.go` 中已经能独立成领域 Handler/Service 的部分；
- `mac.go` 中工具注册与具体领域适配；
- `pipeline.go` 中不需要 `GatewayService`/`AgentFactory` 的校验和状态算法；
- `agent_os_api.go` 中可由 `agentos` 或 `gateway/httpx` 持有的协议与数据库纯逻辑；
- `presentation_runtime_gate.go` 中可进入内容/PPT 领域包的检查逻辑。

暂时不应机械移动的是：

- `ClawBot` 和 `NewBot`；
- `NewGatewayService` 和完整进程生命周期；
- `LLMAgent.Generate` 的跨模块总编排；
- `PipelineExecutor` 中真实调用 Agent、广播 Gateway 事件和管理 DB 生命周期的部分；
- 必须同时绑定身份、租户、workspace、权限与任务状态的代码。

## 18. 最后用一句话记住整体架构

```text
cmd/macs 负责进入程序；
internal/app 负责把整个产品装起来；
gateway/channels/agentos/mcp 负责接收请求；
LLMAgent + agentloop 负责思考和循环；
skills 告诉 Agent 应该按什么方法做；
tools 让 Agent 真正执行动作；
middleware/security/permissionpolicy 约束动作边界；
pipeline 把复杂任务拆成可恢复步骤；
aiemployee 把固定角色与 Pipeline 组合成长期员工；
session/memory/rag 保存上下文并提供知识；
eventbus/hooks/artifacts 把过程和结果交付出去。
```

读源码时始终顺着“入口 -> 装配 -> 执行 -> 工具 -> 持久化/交付”这条链走，就不会被目录数量和大文件绕晕。
