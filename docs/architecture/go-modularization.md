# OpenKitty Go 模块化边界

## 当前验证限制

主程序及生产包可以独立构建，但完整测试和发布质量门尚未通过。`internal/test` 中部分旧测试仍按应用包的同包测试编写，搬离实现目录后无法访问未导出符号；`internal/session` 的测试存在导入环，部分工具测试仍引用迁移前的构造函数。修复时应将测试放回实现包，或改为只验证公开接口，不能通过跳过质量门掩盖问题。

此外，完整测试仍报告记忆事件召回、会话产物路径、技能测试资源相对路径及快速研究网页压缩的断言失败。构建成功、服务健康、聚焦测试通过和完整测试通过需要分别记录；跨平台交叉编译也不能代替对应系统上的运行验收。

## 目标

OpenKitty 过去的大部分 Go 文件都在仓库根目录，并共同属于 `package main`。这不是单纯的目录问题：同包文件可以直接访问彼此的未导出符号，机械搬动文件会立即改变编译单元，容易产生循环依赖、资源嵌入失效和行为回归。

本次重构采用“先建立依赖方向，再迁移文件”的方式。当前 `cmd/macs` 只保留可执行入口，应用装配与尚待继续拆分的兼容代码位于 `internal/app`，根目录只保留资源嵌入包；稳定的领域能力继续下沉到对应的 `internal/*` 包。

## 当前目录与职责

| 包 | 职责 | 依赖约束 |
| --- | --- | --- |
| `internal/agentloop` | 单轮状态、执行计划、可取消独占 Lease 与可恢复工具失败跟踪 | 依赖 Tool 合同，不依赖 Gateway |
| `internal/agentresult` | Agent 终态错误、用户可见部分结果 | 不依赖 Gateway、Channel 或持久化类型 |
| `internal/buildinfo` | CLI 版本参数识别、版本行格式化 | 只依赖标准库 |
| `internal/browserruntime` | 托管 Chromium 下载/缓存与浏览器任务生命周期 | CDP 页面操作、录制和回放导出由根装配层注入 |
| `internal/channels` | 渠道传输契约、公共运行时、消息格式化、媒体与语音处理，以及飞书、微信、Telegram、钉钉、QQ、企业微信和邮件平台协议实现 | 通过 `Runtime`/回调接入 Agent 与 Gateway；不反向依赖根包 |
| 根包 `mac`（`assets.go`） | 集中嵌入 `web/`、`builtin/`、默认技能、提取脚本和默认配置模板 | 只导出静态资源，不依赖主程序或内部业务模块 |
| `cmd/macs` | 正式可执行入口与版本注入 | 只能调用 `internal/app` 的公开启动函数；不得放置业务实现或流程测试 |
| `cmd/openkitty` | 旧启动命令的兼容入口 | 调用同一套 `internal/app`；不新增业务逻辑 |
| `internal/app` | 应用装配、遗留兼容适配和跨模块流程测试 | 可以依赖根资源包与其他 `internal/*`；新领域算法不得新增在此层 |
| `internal/configvalue` | 动态配置取值、默认值、损坏配置恢复及模型密钥环境引用解析 | 只依赖标准库；具体 `Config` 转换留在可执行装配层 |
| `internal/conversation` | 压缩边界、轻量压缩、压缩后缓存清理 | 不依赖模型提供商或 Agent 实现 |
| `internal/eventbus` | 运行时事件发布、过滤订阅与取消订阅 | 不依赖 Gateway、Agent 或持久化实现 |
| `internal/featureflags` | 线程安全的功能开关注册表与产品层级开关 | 配置适配留在根装配层 |
| `internal/gateway/httpx` | 同源检查、静态资源响应头、HTTP Token Bucket 限流 | 只依赖标准库，不持有 `GatewayService` |
| `internal/gateway/risk` | Gateway 输入内容风险分类与公开提示 | 纯策略模块；日志审计留在根装配层 |
| `internal/hooks` | Hook 合同、配置、V2/旧版管理器、管理工具及全部执行器 | Shell 安全与 LLM 调用由装配层注入 |
| `internal/memory` | 事实模型、Deriver、Dreamer、Agent Memory、Session Context 持久化及其工具 | 数据库、LLM、会话清洗和知识图谱通过接口或回调注入 |
| `internal/middleware` | AgentContext、中间件合同、输入输出过滤、澄清、工具错误与 Token 预算 | 只依赖 Tool/Message 合同和轻量运行时工具 |
| `internal/modelruntime` | 泛型模型降级链、并发统计、链注册表、定时健康检查和兼容 Chat 探测 | 通过泛型 Traits 接入 `ModelConfig`，不依赖可执行程序配置类型 |
| `internal/pipeline` | 流水线检查点、恢复点计算及后续调度领域实现 | 不依赖根包；数据库与 Gateway 通过接口或装配层接入 |
| `internal/permissionpolicy` | PermissionMode、Plan 模式判定与工具动作提取 | 只通过注入的只读分类器了解工具，不依赖 Registry |
| `internal/portablepath` | Windows、Unix、UNC 路径的跨平台词法处理 | 只依赖标准库 |
| `internal/processutil` | 进程树启动/终止、PTY、本地子进程平台配置、命令行解析、Python 启动与内置脚本定位 | 不依赖根包业务类型 |
| `internal/projectctx` | Project ID、workspace 规范化、客户持久化与上下文绑定 | 不依赖 Gateway、Session 或工具实现；客户模型额外依赖 GORM |
| `internal/ragmodel` | RAG 知识库、分块、FAQ 与检索日志持久化模型 | 只依赖 GORM，不依赖检索或 HTTP 实现 |
| `internal/runtimeutil` | 截止时间交付检查点、运行心跳、文本 Token 估算 | 只依赖标准库 |
| `internal/routing` | 可选能力的意图识别、自动路由条件和 Chatroom 交付状态规则 | 不依赖 Skill Loader、宿主工具回执类型或具体工具 |
| `internal/security` | Token、本机地址、自签名 TLS、凭据边界与外部证据高风险声明校验 | 不依赖 `GatewayService`、租户或配置模型 |
| `internal/shellpolicy` | Shell 命令分类、风险识别与权限规则建议 | 只依赖标准库，不执行命令 |
| `internal/skillpath` | 可执行文件相对的用户 Skill 安装目录 | 不依赖 workspace 或当前用户主目录 |
| `internal/textnormalize` | 日历纠错、模型文本与流式终端控制序列清理 | 不依赖 LLM 提供商或交付渠道 |
| `internal/textutil` | 中英文混排文本截断、标题识别与非结构化元素格式化 | 不依赖 Agent、Tool 或 Gateway |
| `internal/fsutil` | 原子文件写入、Windows rename 重试与候选文件定位 | 只依赖标准库，可被根兼容层和内部工具共同复用 |
| `internal/toolcontract` | Tool、ToolCall、ToolResult、消息/API 转换、恢复与沙箱能力合同 | 作为 Registry、Middleware、Agent loop 的共同底层依赖 |
| `internal/toolmetrics` | 进程内工具调用次数、失败率与耗时统计 | 不依赖具体 Tool 实现 |
| `internal/tools/business` | CRM Lead、客户服务、客户身份注入与 Schema 驱动业务工具 | 持久化后端与会话身份通过领域接口或装配层注入，不依赖 Gateway |
| `internal/tools/content` | 文档生成/提取/分区与格式转换、微信文章交付包、PDF/PPTX/EPUB/RTF/Email/ODT/HTML 解析、XLSX、TTS、Vision、截图、图片、摄像头与音频能力 | 内容生产与媒体感知共享 workspace 上下文；路径策略、脱敏、提取脚本、内置运行时和原生截图由根层注入 |
| `internal/tools/desktop` | 本机桌面任务、文件目标检查、窗口/键鼠操作、截图和审计回执 | Gateway HTTP 路由与全局实例生命周期留在应用装配层 |
| `internal/tools/development` | AST、跨文件重构、测试运行与诊断、目标验收、代码检查、项目初始化、外部 Agent、测试生成、IDE、Build、Perf、Debug、代码库索引和语言识别 | KG、项目说明、会话路径与 Office 校验通过窄接口注入；子进程绑定 context 超时 |
| `internal/tools/knowledge` | RAG 分块/排序、Bleve 索引生命周期、检索缓存、HyDE/LLM 重排、嵌入运行时、向量索引、知识库工具、资料获取和工具能力目录 | 租户身份、授权、持久化查询和本地 LLM 通过窄接口注入 |
| `internal/tools/network` | API 调用与测试、天气、Web 搜索、邮件发送/分诊、社媒趋势/RSS 与 HTML 正文解析 | 配置和 HTTP Client 显式注入；请求目标与私网策略集中管理 |
| `internal/tools/operations` | 数据库、CI/CD、容器、远程执行、服务器健康检查、日志追踪、Kubernetes 诊断、Feature Flags 与 SysAdmin | 外部进程绑定 context，不依赖根包配置类型 |
| `internal/tools/plugins` | Plugin 注册表、发现、启停和 `manage_plugins` Tool | 不依赖 Skill/RAG；应用层只持有注册表生命周期 |
| `internal/tools/security` | PII 脱敏与 Shell 命令只读分析 | 复用 permissionpolicy 与 shellpolicy，注册逻辑留在根装配层 |
| `internal/tools/skills` | Skill 列表、激活、创建、保存、策展、市场安装和 ClawHub 适配 Tool | 通过 `SkillsRuntime` 接入应用 Skill Loader；逐调用沙箱策略留在应用适配层 |
| `internal/tools/workspace` | 文件操作、项目规则与地图、Notepad、REPL | 工作区边界检查由装配层注入，不依赖 Agent/Gateway |
| `internal/validationhint` | 根据变更文件推导最小验证命令 | 只依赖跨平台路径模块 |
| `pkg/codegraph` | 可独立复用的代码图能力 | 可以被内部模块和外部程序导入 |
| `cmd/releaseguard` | 发布前安全检查命令 | 独立命令入口 |

依赖方向固定为：

```text
根 package assets（只持有嵌入资源）
        │
        ▼
cmd/macs（正式入口；cmd/openkitty 为兼容入口）
        │
        ▼
internal/app（应用装配和迁移中的兼容层）
        │
        ├── internal/*（稳定的产品内部模块）
        └── pkg/*（可复用公共包）

internal/* 不能反向导入 cmd/*；稳定领域包不能导入 internal/app
基础包不能依赖 Agent、Gateway、数据库等上层业务类型
```

## 本次已经迁移的文件

- `version_info.go` → `internal/buildinfo/version.go`
- `deadline_budget.go`、`progress_heartbeat.go`、Token 文本估算 → `internal/runtimeutil/`
- `portable_path.go` → `internal/portablepath/path.go`
- `command_tree_*.go`、`codex_pty*.go`、`local_llm_process_*.go` → `internal/processutil/`
- `tls_helper.go`、认证 Token 与本机地址判断 → `internal/security/`
- `hook_types.go`、`hook_schema.go` 及四类 Hook 执行器 → `internal/hooks/`
- `channel_delivery.go`、`channel_coding_command.go`、`channel_media.go`、`channel_voice.go` → `internal/channels/`
- `feishu.go`、`weixin_channel.go`、`telegram_channel.go`、`dingding.go`、`qq.go`、`weixinwork.go`、`email_channel.go` → `internal/channels/`；根包仅保留 `channel_*_adapter.go`，Weixin Gateway HTTP 配置拆入 `weixin_gateway.go`
- `channel_artifacts.go` 的图片解析与原生媒体降级、`ChannelManager` 注册表和生命周期 → `internal/channels/`；根层仅保留 Gateway 装配适配器
- 原先误放在 `channel.go` 的动态配置取值函数 → `internal/configvalue/`
- Gateway 同源检查、静态资源响应头、限流和内容风险分类 → `internal/gateway/`
- 文件操作、项目规则、Notepad、REPL 与 Snapshot → `internal/tools/workspace/`
- RAG、资料获取与知识能力目录 → `internal/tools/knowledge/`；Skill 创建、使用和市场工具 → `internal/tools/skills/`
- PII 与 Shell Analyze → `internal/tools/security/`；Web/API/Weather → `internal/tools/network/`
- Build、Perf、Codebase Index → `internal/tools/development/`；根包仅负责接入 KG、Session Context 和工具注册表
- AST、Refactor、Test Gen、IDE → `internal/tools/development/`
- Test Runner 的命令执行、失败诊断、输出压缩与 Fix-Retry，以及跨文件重构工具 → `internal/tools/development/`；根包只保留测试基线和原生环境适配
- Goal Verify 的目标文件检查、测试/构建/Lint 验收与摘要生成 → `internal/tools/development/`
- Code Check 与 Project Init → `internal/tools/development/`；命令行拆分、Python 启动和内置脚本定位 → `internal/processutil/`
- `pipeline_durable.go` 及其纯单元测试 → `internal/pipeline/`，作为流水线领域迁移的首个稳定边界
- CI/CD、Container、Remote、DB、服务器健康检查、日志追踪、Kubernetes 诊断、Feature Flags、SysAdmin → `internal/tools/operations/`
- Listen、Voice、Watch、Vision、Doc Gen、XLSX、Media、Camera 与 Audio → `internal/tools/content/`
- Lead/CRM、Customer Service、客户上下文和业务 Schema → `internal/tools/business/`
- 插件注册与管理工具 → `internal/tools/plugins/`；Social Trending/RSS 与 HTML Markdown 解析 → `internal/tools/network/`
- 根包原子写文件实现和候选文件定位 → `internal/fsutil/`；根包仅在仍有多处调用时保留兼容包装函数
- `shell_command_classification.go` → `internal/shellpolicy/classification.go`
- `permission_mode.go` → `internal/permissionpolicy/mode.go`
- `rag_models.go`、`customer.go` → 独立的持久化模型模块
- 可复用中间件、工具结果判定、验证建议 → 对应的 `internal/*` 模块
- 对应的纯单元测试随实现迁入模块目录。

仍需保留的兼容与装配代码现在位于 `internal/app`。例如 `message_token_estimator.go` 兼容应用内部两种 `tool_calls` 表示；相关算法和安全原语已经下沉，适配层不再拥有这些实现。

## 入口与资源边界

主程序入口位于 `cmd/macs`，应用实现位于 `internal/app`。由于 Go 的嵌入模式不能使用 `..` 访问父目录，根目录的 `assets.go` 作为独立 `package assets` 集中持有 `builtin/`、`default-skills/`、`web/`、提取脚本和配置模板，并将 `embed.FS`/内容注入应用层。

这样无需复制庞大的资源树，也不会让 `internal/app` 通过相对路径读取源码目录。测试的 `TestMain` 会定位 `go.mod` 并切回仓库根目录，因此既有资源夹具仍保持原行为。

## 后续迁移顺序

1. **稳定公共契约**：将 Tool、Message、Turn State、事件和配置视图拆成不依赖具体实现的接口与数据类型。
2. **拆 Agent 运行循环**：把 turn checkpoint、lease、loop guard、tool execution、result finalization 分离到 `internal/agentloop`。
3. **拆 AgentOS 与 Skill**：把 Skill 发现/激活/冻结、AI 员工、Pipeline 调度分别放入 `internal/skills`、`internal/agentos`、`internal/pipeline`。
4. **拆接入层**：HTTP/WebSocket、渠道适配、认证适配进入 `internal/gateway` 和 `internal/channels`，只调用应用服务接口。
5. **拆工具域**：文件、Shell、搜索、文档、演示文稿等按工具族组织到 `internal/tools/<domain>`；注册表负责装配，不持有工具实现。
6. **继续缩小装配层**：将 `internal/app` 中仍然成组耦合的 AgentOS、Pipeline、Skill 与 Gateway 实现逐批下沉；`cmd/macs` 始终只保留启动入口。

## 每批迁移的验收条件

- 除应用装配包 `internal/app` 外，新增的稳定 `internal/*` 包不能导入 `mac` 根资源包、`internal/app` 或 `cmd/*`，也不能通过复制类型规避边界。
- 行为测试随实现迁移；跨模块流程测试留在上层。
- Windows 与非 Windows 的 build tags 保持配对。
- `go test ./... -count=1 -failfast` 必须通过。
- `go vet`/发布检查和 `git diff --check` 不得新增错误。
- 不在同一批次同时修改协议含义和目录结构；先保持行为，再做功能演进。

## 2026-09-04 续作

本轮从中断现场继续完成了以下边界收口：

- 补齐并纳入版本控制知识与 Skill 工具域：Capabilities、RAG、Source Acquire、Nova Chatroom 归入 `internal/tools/knowledge/`，Skill 创建/市场和 ClawHub 归入 `internal/tools/skills/`。
- `agent_reach_tool.go` 的完整路由实现迁入 `internal/tools/network/agent_reach.go`；根包只注入既有 Web/Social fallback。
- Reactive Store、Token Budget、Prompt Cache 分段与 Cache Break 诊断迁入 `internal/runtimeutil/`。
- SSE 事件与多路广播迁入 `internal/eventbus/`。
- FFmpeg 查找、下载与解包迁入 `internal/processutil/`。
- Lazy Tool、条件 Schema 与 Feature-gated Tool 迁入 `internal/toolcontract/`。
- 财务图表伪造数据防护迁入 `internal/security/`。

该阶段根目录中的同名小文件还是兼容/装配层，现已随应用实现迁入 `internal/app`。旧 `.gitignore` 的 `knowledge/` 规则已收窄为 `/knowledge/`，避免误忽略 `internal/tools/knowledge/` 源码。

本轮继续按“优先使用现有领域目录、避免一文件一目录”的原则完成：

- Circuit Breaker、持久事件日志、Prompt Dump、共享状态与模型选择等运行时能力分别归入 `internal/runtimeutil/`、`internal/eventbus/`、`internal/routing/`。
- 拒绝跟踪、许可提供器和身份校验归入 `internal/permissionpolicy/`、`internal/security/`、`internal/projectctx/`。
- 文件历史、文件协作和快照实现归入 `internal/tools/workspace/`；Skill 搜索缓存、Idea Tree 和 Obsidian Vault 归入 `internal/tools/knowledge/`，自动 Skill Tool 归入 `internal/tools/skills/`。
- 平台搜索、邮件分诊、模型比较、学术搜索和社媒发布归入 `internal/tools/network/`。
- Prompt 模板、法律/营销工具及 Markdown 转 PDF 归入 `internal/tools/content/`。
- 依赖分析、SQL/EDA、数据科学、进阶分析和代码审查归入 `internal/tools/development/`。
- ArXiv/LaTeX/文献综述归入 `internal/tools/network/`，自动更新器归入 `internal/tools/operations/`；构建版本变量继续留在 `package main`，保持现有 `ldflags` 兼容。
- 文档清洗、语义分类、标题分块和 RAG 清洗归入 `internal/textutil/`；RAG 结果格式化归入 `internal/ragmodel/`。
- 提示注入检测规则归入 `internal/security/`，进度消息翻译归入 `internal/eventbus/`。
- 中医专家回答安全守卫归入 `internal/security/`，Office Open XML 包完整性校验归入 `internal/tools/content/`。
- RAG 分词、查询改写、会话查询记忆和 BM25 索引成组归入 `internal/ragmodel/`，没有为每个算法单独建目录。
- 投资请求的纯文本意图分类归入 `internal/tools/network/`；依赖流水线锁和文件写入策略的装配逻辑继续留在根包。
- 遥测与 Webhook 通知实现归入 `internal/tools/operations/`，生成式 UI 归入 `internal/tools/content/`，沙箱实现归入 `internal/tools/security/`。
- ECC Skill 加载/转换与 Nova `source_acquire` 回执验证归入 `internal/tools/knowledge/`；根层只转换流水线调用记录并注入路径匹配器。
- JSON Pointer/RFC 6902 补丁算法归入 `internal/tools/development/`，工作区、LSP 与原子写入事务仍由根层装配。
- 文件扩展名和 magic bytes 识别归入 `internal/textutil/`，工作区包含及符号链接逃逸检查归入 `internal/projectctx/`。
- 专业 Agent 的凭据绑定规则归入 `internal/security/`；根层只把现有 `ModelConfig` 转为中立配置视图并保留注册表装配。
- AG-UI 协议类型、事件发射器和事件映射归入 `internal/eventbus/`；依赖 `GatewayService` 的 SSE HTTP 处理器继续留在根层。
- AI 员工输出文件请求的纯文本判定归入 `internal/permissionpolicy/`；可信指令过滤、上下文解析和工具策略继续由根层组合。
- PaddleOCR 与 Tesseract 的引擎、下载和本地运行实现归入 `internal/tools/content/`；依赖现有 PDF 页渲染流程的适配器留在根层。
- 工具 JSON Schema 校验、修复提示和 LLM 参数信封/别名纠偏归入 `internal/toolcontract/`；根层仅把 `ToolRegistry` 收窄为 schema 查询回调。
- 内置 Skill 探测、Lead Research 产品 URL 规范化/匹配及微信视频号素材路由归入 `internal/routing/`；Skill 加载器和可信工具回执解析仍留在装配层。
- SMTP 运维通知的发送和正文构造归入 `internal/tools/operations/`；根层只汇总 Gateway/RAG/AI 员工状态。
- 旧 Web 工作区的租户保留目录、大小写和符号链接逃逸检查整体归入 `internal/projectctx/`。
- 旧版安全 Hook 链归入 `internal/security/`，只读路径保护中间件归入 `internal/middleware/`。
- 不可自动恢复的工具失败分类与用户诊断归入 `internal/agentresult/`；外部证据工具的具体清单由根层注入。
- 租户知识库 ID 的完整归属校验归入 `internal/ragmodel/`；认证身份到租户分区的映射仍由根层完成。
- 已删除法律、营销、数据科学、SQL/EDA、学术搜索、社媒发布、生成式 UI、Binary、API Test、CI/CD、Container、DB、Doc Gen、IDE、Listen、Project Map、Refactor、Test Gen、Voice 等纯类型别名/构造器转发文件；注册处现在直接引用对应的 `internal/tools/<domain>` 包。
- 许可证验证与密钥能力由 `internal/security/` 直接提供，`GatewayService` 的用户数校验留在现有租户认证装配层；根目录不再保留 `license.go` 转发面。
- Prompt Cache、Lazy Tool、Idea Tree、RAG 分词/改写以及文档分类/分块/清洗的调用方已改为显式引用所属内部包，并删除残留根包兼容壳；本批没有新建任何单文件领域目录。
- Reactive Store 与 Token Budget 的生产调用和测试直接引用 `internal/runtimeutil/`；代码感知 RAG 分块直接引用 `internal/ragmodel/`；财务图表安全判定直接引用 `internal/security/`。
- FFmpeg 解析由摄像头、文档和通道装配直接调用 `internal/processutil/`，不再通过根目录函数转发。
- Agent Reach、AST、Build、Perf、Git PR、Worktree 和天气工具的注册点已改为直接引用所属工具包；相应纯别名适配文件已删除。
- 配置热重载直接调用 `internal/configvalue/`，自动更新类型与实现直接使用 `internal/tools/operations/`；仅 `CurrentVersion` 留在主入口变量区以保持构建参数兼容。
- 文件历史、文件读写协作与快照的生产调用已直接使用 `internal/tools/workspace/`；根目录不再保留这三层转发文件，相关旧测试只保留测试期兼容别名。
- 遥测注册与上报直接使用 `internal/tools/operations/`，文档清洗调用直接使用 `internal/textutil/`，数据库启动配置直接使用 `internal/configvalue/`，Skill 界面元数据直接使用 `internal/skillpath/`。
- SSE 类型和构造器的生产接口与测试均直接使用 `internal/eventbus/`；频道消息格式化与命令预处理直接使用 `internal/channels/`，删除未使用的根转发函数。
- Git 与 SysAdmin 注册点直接使用对应内部工具包，通用文本截断统一调用 `internal/textutil/`；Prompt Cache Break 的全局实例归入现有全局状态装配文件，不再单列别名文件。
- LSP 管理器、诊断类型及全部 LSP 工具注册直接使用 `internal/tools/development/`，文件编辑事务不再依赖根目录 LSP 别名层。
- Feature Flags 的类型和 Registry 直接使用 `internal/featureflags/`，根配置转换合并到现有 `config_values.go`；Circuit Breaker 的生产调用和测试直接使用 `internal/runtimeutil/`。
- 事件日志和拒绝跟踪器的类型分别直接使用 `internal/eventbus/` 与 `internal/permissionpolicy/`，仅进程单例及初始化保留在现有 `global_state.go`。
- Prompt Template、Auto Skill、Nova Chatroom 与频道语音处理直接使用所属内容、知识和频道内部包；核心工具标记合并到现有 Source Acquire 装配，不新增注册文件。
- 沙箱配置、实例、路径包含检查和工具注册直接使用 `internal/tools/security/`；旧根包兼容名仅在现有测试文件中提供，不进入生产构建。
- Prompt 注入检测器、工具输出/记忆/Skill 扫描直接使用 `internal/security/`，仅检测器进程单例留在现有 `global_state.go`。
- 模型校验、模型选择和 Gateway 环境覆盖的根配置转换合并到现有 `config_values.go`，删除三个零散配置转发文件。
- 个人身份验证、分区 ID、会话前缀和个人工作区直接使用 `internal/security/` 与 `internal/projectctx/`；兼容帮助函数仅保留在现有边界测试中。
- WebFetch 的正文可读性与 CSS Token 提取直接使用 `internal/tools/network/`，并删除已无调用的快照方法；Code Review 的生产注册和测试直接使用 `internal/tools/development/`。
- 邮件分诊与模型比较的配置转换归并到统一配置装配；Markdown PDF、Remote、Snapshot 和 ClawHub 在唯一注册点显式注入脱敏、安全、路径及 Skill 扫描回调。
- Source Acquire 回执验证在流水线调用点直接使用 `internal/tools/knowledge/` 的中立调用记录；Debug 与 UI 文件只保留真实配置门控和 Gateway HTTP 处理器。
- 股票事实工具、结构化结果、供应商常量和证券代码路由由生产调用点直接引用 `internal/tools/network/`；PDF 提取器仅在工具注册点显式注入，删除根目录股票转发壳。
- Source Acquire 的生产路径和测试直接构造 `internal/tools/knowledge/` 实现，核心工具名单归并到现有 `tools.go`；项目说明工具的根加载器桥接归并到已有 `project_instructions.go`。
- Webhook 通知器和 Notify Tool 的生产代码与测试直接使用 `internal/tools/operations/`，不再维护根包镜像类型；Prompt Dump 仅有的缓存指标生命周期方法归并回 `CacheMetricsTracker`。
- Obsidian Vault 实现继续由 `internal/tools/knowledge/` 持有，依赖根 `AgentFactory` 的注册桥接归并到已有知识工具模块，没有为装配胶水新建目录。
- Gateway 的同源校验、静态资源响应头和令牌桶限流由生产入口直接调用 `internal/gateway/httpx/`；根测试只验证公开行为，不再依赖转发层私有字段。
- 删除根包中已无任何调用的 RAG 格式化兼容面；有效格式化实现仍集中在 `internal/ragmodel/`。
- 删除已无调用的根 Paddle/Tesseract OCR 包装类型；Local LLM 的数据目录、文件、解压、可执行文件查找和下载逻辑直接复用 `internal/tools/content/`，PDF 流程测试亦直接使用该包。
- 内容风险的分类与脱敏摘要审计统一归入 `internal/gateway/risk/`，根 Gateway 仅传入日志器；UI 语言路由和持久化测试直接调用 `internal/gateway/httpx/`。
- RAG、代码库知识和专业 Agent 凭据的必要根依赖注入分别归并到现有 `rag_search.go`、`knowledge_graph.go` 和 `professional_agent.go`，删除三个孤立适配文件。
- 删除未被调用的旧 Gateway 认证检查/令牌读取方法；仍有效的令牌初始化并回 Gateway 生命周期，随机令牌生成继续直接使用 `internal/security/`。
- AgentOS 的初始助手消息、展示身份校验、任务上下文传递与身份提示词契约整体迁入已有 `internal/gateway/httpx/`；API、Gateway 和模型调用点显式使用该包，不再由根包持有协议实现。
- Persistent Shell、终端会话管理、Windows/Unix 的 Shell 创建、默认 Shell、Git Bash 探测与自检整体迁入已有 `internal/processutil/`；根层通过两个窄回调注入已封装的执行身份所有者与访问判断。
- 后台 Shell Job 的生命周期、进程树终止和日志尾读归入 `internal/processutil/`，面向模型的 `shell_sessions` 工具归入已有 `internal/tools/operations/`；旧根目录 `shell_jobs.go` 已删除。
- Tool Detail 搜索和进度消息翻译的根目录转发壳已删除；装配点分别直接复用 `internal/toolcontract/` 与 `internal/eventbus/`。
- AgentOS 请求文本的提示注入/敏感操作信号判定、上下文约束提示和脱敏哈希审计归入 `internal/gateway/risk/`；根层只注入现有路径提取器与检测器，仍保持“提示而不预先阻断”。
- AgentOS 数据面路由表、监听地址回环判断和独立 HTTP Server 生命周期归入 `internal/gateway/httpx/`；根层仅绑定具体处理器、中间件和配置。
- 旧 Web API 与 AgentOS 的任务标记、SSE 命名空间和失败关闭判定状态归入 `internal/gateway/httpx/`；数据库及内存查询通过窄回调注入，删除根目录 `agent_os_legacy_boundary.go`。
- AgentOS 的 Scope 规范化、任务读写/审批授权和列表 SQL 隔离策略归入 `internal/gateway/httpx/`；根层只把现有执行身份与任务模型转换为中立边界视图，删除根目录 `agent_os_access_scope.go`。
- 个人会话/任务的租户与主体所有者比较、历史查询隔离和精确记录加载归入 `internal/gateway/httpx/`；根层只负责从 `GatewayTask` 提取可信所有者，删除根目录 `personal_session_boundary.go`。
- 测试运行器的通用命令执行、失败归因、输出压缩和 Fix-Retry，以及跨文件重构工具整体归入 `internal/tools/development/`；根 `test_runner.go` 缩为基线回执与原生环境注入适配器。
- 内存型流水线检查点和恢复点计算归入新建的 `internal/pipeline/`，对应纯单元测试随实现迁移；工具注册点直接构造内部实现。
- 许可证校验测试改为在 `internal/security/` 内使用临时 RSA 密钥与临时许可证，公开构建不再依赖本机签名许可证。

这些迁移均复用已有且已经包含多份源码的目录；兼容别名、参数转换或依赖注入统一收拢到 `internal/app`。

`internal/app/knowledge_graph.go` 仍属装配层：数据库所有权尚与主程序模型耦合；Deriver、Dreamer、衰减规则和写队列已经迁入 `internal/memory`，待持久化接口稳定后再下沉图存储本体。

`internal/app/local_llm.go` 仍属装配层：其进程生命周期直接调用主程序系统消息和进度推送函数，并被 RAG embedding 复用。后续应先注入通知/进度接口，再整体迁入运行时或模型提供器模块。

## 2026-09-05 续作

- `goal_verify_tool.go` 整体迁入 `internal/tools/development/goal_verify.go`，动态参数解析和文本截断直接复用基础内部包；根入口只负责注入 workspace 并注册工具。
- `ops_tools.go` 的主机配置、SSH 连接、服务器健康检查、日志追踪和 Kubernetes 诊断整体迁入 `internal/tools/operations/health.go`；根配置直接使用中立的 `OpsHostConfig`，不再持有镜像类型。
- 为上述边界新增独立单元测试，覆盖默认配置、工作区文件验收、未知目标、远程日志提示和只读子命令拒绝路径。
- `code_tools.go` 整体迁入 `internal/tools/development/code_check.go`；通用命令行解析、Python 解释器选择和内置运行时脚本定位归入 `internal/processutil/`，Office Open XML 校验由装配层注入，避免开发工具反向依赖内容工具。
- `media_tools.go` 与 `audo_camera.go` 分别迁入 `internal/tools/content/media.go` 和 `camera_audio.go`；原生 CDP 截图继续由根层窄函数注入，工具实现不再依赖 `package main`。
- Lead/CRM、客户服务、自主工具、客户身份注入及业务 Schema 整体归入新建的 `internal/tools/business/`；Gateway 直接装配业务包，客户持久化模型继续由 `internal/projectctx/` 持有。
- `plugin_system.go` 最终重分类到 `internal/tools/plugins/manage.go`；`social_tools.go` 与 HTML 正文/Markdown 解析迁入 `internal/tools/network/`，HTTP Client、检索目标和私网地址策略均形成可测试边界。
- 删除根 `shared_state.go` 兼容壳：共享状态直接使用 `internal/runtimeutil/`，Gateway HTTP 适配合并到现有 `gateway.go`，管理工具直接使用 `internal/tools/operations/`。
- 为以上模块补齐包内单元测试并更新根装配层调用；本批处理后根目录生产 Go 文件由 241 个降至 228 个。
- 邮件发送工具迁入 `internal/tools/network/mail.go`；分诊配置与发送配置使用不同类型，Gateway 的 HTML 转义改用标准库。
- 托管浏览器安装器和浏览器任务管理器迁入 `internal/browserruntime/`；根层只保留 CDP 操作及 HTTP 处理器适配。
- 外部 Agent 会话工具迁入 `internal/tools/development/external_agent.go`；项目说明加载改为构造注入，并移除包加载时创建目录的副作用。
- Go PDF 布局解析、Office/电子书/邮件格式提取、HTML 正文提取、Extract/Speak 与自动文档分区整体迁入 `internal/tools/content/`。
- DOCX/PDF 结构识别与 RAG 固定/语义分块迁入内容和知识工具包；标题、停用词及非结构化格式化由基础包公开窄 API 共享。
- RAG 结果模型、命中词、近重复过滤、文本重合度和加权 RRF 排序迁入 `internal/tools/knowledge/ranking.go`，根检索文件只负责 Bleve/GORM/LLM 编排。
- API、Ollama、本地 Llama 与可选 ONNX 嵌入运行时迁入 `internal/tools/knowledge/`；本地 LLM 使用工厂注入，ONNX stub/实现使用互斥 build tag。
- 向量索引、余弦相似度、筛选、JSON 持久化及分块嵌入迁入 `internal/tools/knowledge/vector_index.go`；根导出 API 只读取快照。
- `knowledge_base_tool.go` 整体迁入知识工具包；租户身份、授权、RAG FTS 和知识表 FTS 通过依赖回调接入。
- 上述续作把根目录生产 Go 文件从 228 个进一步降至 214 个，并为新边界补齐包内行为测试。
- Bleve 关键词索引、批量同步、损坏备份与筛选检索迁入 `internal/tools/knowledge/bleve_index.go`；混合检索缓存按 session 隔离，HyDE 和 LLM 重排统一由 `SearchEnhancer` 持有。
- 事实模型、Deriver、Dreamer、记忆衰减/主动格式化、异步写队列和会话记忆提取器迁入 `internal/memory/`；根层仅注入 GORM、LLM、会话清洗与路径回调。
- 配置损坏恢复和模型 API Key 环境引用规则归入 `internal/configvalue/`；模型降级链、并发统计、健康调度及 Chat Completion 探测归入 `internal/modelruntime/`。
- 模型单次调用的超时夹取、尝试次数、`Retry-After`、HTTP 重试判定和可取消等待已从应用包迁入 `internal/modelruntime/call_policy.go`；`internal/app` 仅保留 `RuntimeConfig` 与 `LLMAgent` 的装配适配。
- 外部证据高风险声明规则归入 `internal/security/`，Chatroom 请求/最新回执/失败追加规则归入 `internal/routing/`；宿主工具记录解析仍留在装配层。
- 将根目录剩余 592 个 Go 文件（其中 208 个生产文件）迁入 `internal/app/` 并改为 `package app`；当时的 `cmd/openkitty/` 只保留调用 `app.Main()` 的启动文件，根目录仅保留 `assets.go`。当前正式构建入口为 `./cmd/macs`，旧入口继续兼容。

Pipeline 检查点模型、阶段常量、稳定 ID、深拷贝/裁剪、重复次数转换、原子持久化和终态 transcript 读取已迁入 `internal/pipeline/checkpoint.go`。`internal/app/pipeline.go` 只保留把应用层 `PipelineStep` 与 `TurnCheckpoint` 转换为领域视图的窄适配。
Pipeline 输出契约的配置校验、章节/状态验证与 degraded 结果生成已迁入 `internal/pipeline/output_contract.go`；应用层只负责遍历自己的步骤树。Pipeline 演进指标、建议生成和指标文件持久化已迁入 `internal/pipeline/evolve.go`，通过中立 `ExecutionSnapshot` 接收执行数据，不再依赖 `PipelineExecution`。
Pipeline 槽位契约、领域派生规则、填充/缺参提示协议、填充回执与已审计产物路径提取已迁入 `internal/pipeline/slots.go`；执行锁、续跑上下文以及 `PipelineStep`/`StepResult` 转换仍由应用层负责。
Pipeline 返工配置与审计模型、返工图校验、末行控制记录解析、PPT 返工提示策略和已知只读工具判定已迁入 `internal/pipeline/rework.go`；应用层只保留执行状态变更、revision 归档、工具注册表兜底判定和旧产物清理。
Pipeline 来源/产物契约模型、来源引用与定位解析、工具参数路径匹配、Markdown/微信 HTML 来源标记校验，以及 PPT 受众内容契约判定已迁入 `internal/pipeline/source_contract.go`。文件冻结、真实路径边界、PPTX 解析和工具调用审计仍由应用层及内容工具负责。

`plan_tool.go` 的计划创建、步骤更新、计划模式切换和完整性校验已整体迁入 `internal/agentloop/plan_tools.go`；`internal/app` 只注入 Agent 运行时与进度事件回调。
`write.go` 的文件读写、多格式输出、范围读取和 Office 写入能力已整体迁入 `internal/tools/workspace/write_read.go`；工作区路径策略、保护提示、原子写入和 LSP 收尾通过依赖接口注入。
`web_fetch.go` 的网页抓取、搜索引擎路由、正文提取、降级搜索及可选 LLM 压缩已整体迁入 `internal/tools/network/web_fetch.go`；应用层只转换模型配置，并注入 PDF 提取、研究快路径和时间序列覆盖标注。

本轮按“先迁移非测试主体、测试后续统一整理”的规则继续收口 Tool：

- `edit_file_tool.go`、`multiedit_tool.go`、`json_patch_tool.go` 迁入 `internal/tools/workspace/`；路径边界、系统文件保护、原子写入和 LSP 收尾由 `FileToolDependencies` 注入。
- Tool 熔断、重试保护和结果压缩包装迁入 `internal/toolcontract/`；应用层只保留 Registry 批量包装和只读分类适配。
- `learn_tool.go` 与 Agent Memory Tool 分别迁入 `internal/tools/knowledge/learn.go` 和 `internal/memory/agent_memory.go`。
- `list_skills`、`use_skill`、`curate_skills`、`create_skill`、`save_skill`、`skill_market` 和 `clawhub` 按职责统一迁入 `internal/tools/skills/`，不再混入知识检索包。依赖应用 Skill 元数据的逐调用只读/沙箱判断仍留在 `internal/app` 适配层。
- Plugin 注册表和 `manage_plugins` 从知识包重分类到 `internal/tools/plugins/manage.go`；原知识包仅保留待测试统一迁移后删除的兼容别名。
- `converter.go` 的 HTML/PDF/DOCX/PPTX/CSV/XLSX 转换主体迁入 `internal/tools/content/convert.go`；路径授权、导出脱敏、DOCX 写入、Python 解析和嵌入运行时物化由应用层注入。
- `list_dir` 迁入 `internal/tools/workspace/list_dir.go`，复用统一工作区路径策略；通用 `http` Tool 迁入 `internal/tools/network/http.go`。
- `db_query`、Pipeline 数据库查询和 `db_connect` 迁入 `internal/tools/operations/database_operator.go`；Pipeline 只保留配置引用和生命周期装配。
- `desktop_task` 的任务模型、管理器、桌面交互、截图与审计实现迁入 `internal/tools/desktop/task.go`；`internal/app` 只保留 Gateway HTTP 路由、全局实例和旧测试兼容入口。
- `computer` 的持续会话、观察—操作—验证循环和短动作组编排位于 `internal/tools/computer/`；应用层只注入既有 Browser/Desktop Tool 回调，平台交互仍分别归属 `internal/tools/browser/` 和 `internal/tools/desktop/`。
- `wechat_article_bundle` 的 Markdown/HTML/封面生成和安全发布实现迁入 `internal/tools/content/wechat_article_bundle.go`；应用层只保留核心工具注册及兼容入口。
- `session_context` 的持久化管理器和 Tool 迁入现有 `internal/memory/session_context.go`，与 Agent Memory 共用记忆领域边界；应用层保留会话装配适配。
- 本批不迁移、不拆分 `_test.go` 文件；既有测试仅用于兼容性验证，待非测试主体边界稳定后再统一归类。

完成本批后，仓库根目录仍只有 `assets.go` 一个 Go 文件，`internal/app` 的非测试 Go 文件为 192 个。剩余文件中相当一部分是 Gateway、Pipeline、Agent 生命周期和权限/注册适配，不应仅因实现了 `Tool` 接口就机械搬入工具目录。
