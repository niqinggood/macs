# macs Agent 协作手册

本文件适用于整个仓库。它的目标是让新会话中的 AI 编码代理快速恢复项目上下文，并给出修改、验证和交付时必须遵守的仓库级约定。若子目录以后出现更具体的 `AGENTS.md`，则子目录文件对其作用域优先。

## 开始工作前

1. 先运行 `git status --short --branch`，再查看相关文件的 diff。此仓库可能处于持续模块化迁移中；未跟踪文件也可能是当前有效源码，不能当作临时文件删除。
2. 用 `rg` / `rg --files` 定位符号和文件，不要凭旧路径猜测。先搜索现有实现、相邻测试和注册点，再新增类型或目录。
3. 阅读与任务直接相关的文档：
   - 系统入口和请求全链路：`internal/README.md`
   - Go 包边界和迁移规则：`docs/architecture/go-modularization.md`
   - 产品能力和公开接口：`README.md`
   - AgentOS：`docs/agent-os-api.md`、`docs/openapi-agentos.yaml`
   - Pipeline：`docs/architecture/PIPELINE_GUIDE.md`
   - 脚本入口：`scripts/README.md`
4. 以当前源码和测试为最高事实来源。部分历史文档、工作日志及 `README.md` 中的旧文件路径可能落后于模块化迁移，引用前必须在磁盘上确认。
5. 保留用户已有改动。不要为了得到“干净工作区”而 reset、checkout、批量移动或删除不属于当前任务的文件。

## 一句话理解系统

macs（麦卡斯，Multi-Agent Collaboration System）是以 Go 1.26 为主的 AI Agent 平台：单二进制内嵌原生 Web UI 和内置 Skills，对外提供普通聊天、AgentOS 异步任务 API、Pipeline、AI Employee、多 Agent 协作、RAG、MCP 及多聊天渠道，并通过 Tool Registry 与 Middleware 控制真实动作、权限和恢复。产品小名是 openkitty。

## 品牌与授权

- 按用户最新偏好（2026-09-29），新增或修改的产品展示名、文档与交付包名称统一使用小写 `macs`；中文首次出现可写作“macs（麦卡斯）”。后续回复也使用 `macs`。此规则取代此前大写品牌要求；已有协议、环境变量、代码符号和许可证正式名称保持原约定，不作机械改名。
- 正式名称为 `macs`，小名为 `openkitty`，两者均使用小写。产品介绍可说明小名；正式标题、安装包品牌和新增功能名称仍使用 `macs`。历史兼容标识保持原约定。
- `.openkitty/`、`openkitty-config.yaml`、`OPENKITTY_*`、`openkitty.exe`、浏览器事件名和旧 API 字段属于兼容接口，未经完整迁移方案不得机械替换。
- 新增源码默认受根目录 `LICENSE` 约束；复制第三方代码时必须保留其独立许可证和 NOTICE，不能擅自改成 MACS 许可证。
- 正式发布前必须同时检查 `LICENSE`、`NOTICE`、`COMMERCIAL-LICENSE.md`、`TRADEMARKS.md` 与 `BRANDING.md`。详细命名规则以 `BRANDING.md` 为准。

几个概念不要混淆：

- Agent 是执行主体；`LLMAgent.Generate` 驱动“模型—工具—再推理”循环。
- Tool 是模型可调用的一次具体动作；合同、注册执行和具体实现分层存放。
- Skill 是给 Agent 激活的一套说明与能力约束，不是另一个 Agent。
- Pipeline 是可持久化、可恢复、带输入输出契约的多步骤 Agent 编排。
- AI Employee 是面向业务的身份、路由和 Pipeline 绑定层。
- AgentOS 是复用同一 Agent 运行时的可靠 API 外壳，增加身份、队列、租约、幂等、审批、事件流和恢复。

外部系统调用 Agent/AI Employee 时统一走异步 AgentOS 契约：提交立即返回 `202 + task_id + links`，实时状态走任务 SSE，最终状态以持久任务快照为准。`internal/eventstream/` 提供与业务类型无关的有界事件重放；`Last-Event-ID` 只覆盖当前实例最近窗口，收到 `replay_reset` 后必须回查任务快照。不要为 LIMS、代码、研究或某个前端另建一套进度协议，也不要把瞬时 SSE 误当持久任务数据库。对外客户端实现在 `sdk/go/macs/`。

## 启动与主链路

可执行入口固定为：

```text
cmd/openkitty/main.go
  -> internal/app.Main()
  -> run()
  -> NewBot()
  -> ClawBot.Run()
```

根目录 `assets.go` 是独立的 `package assets`，只负责通过 `go:embed` 打包 `web/`、`builtin/`、`default-skills/`、提取脚本和默认配置模板。不要把业务逻辑放回根包，也不要复制这些资源树到 `cmd/` 或 `internal/app/`。

普通 Web 对话的核心调用链：

```text
POST /api/chat
  -> GatewayService.handleChat
  -> processTaskWithSession
  -> LLMAgent.Generate
  -> ToolRegistry + Middleware + Tool.Execute
  -> 结果校验
  -> 消息 / task / transcript / memory 持久化
  -> JSON 或 SSE
```

常用源码阅读入口：

- 启动、Agent 创建、模型工具循环：`internal/app/mac.go`
- HTTP/WebSocket Gateway 和普通任务：`internal/app/gateway.go`
- AgentOS 宿主与队列：`internal/app/agent_os_api.go`、`internal/agentos/`、`internal/gateway/httpx/`
- Pipeline 宿主与领域规则：`internal/app/pipeline.go`、`internal/pipeline/`、`internal/pipelines/`
- 显式 `@skill`：`internal/app/skill_run.go`、`internal/skills/`、`internal/tools/skills/`
- AI Employee：`internal/app/ai_employee.go`、`internal/aiemployee/`
- Tool 执行：`internal/toolcontract/`、`internal/toolregistry/`、`internal/middleware/`、`internal/tools/`

### 能力发现、文档自动化与偏好

- `tool_detail` 是内置的按需工具发现入口：`action=search` 支持名称、别名、标签以及中英文能力词，`action=get` 返回完整 Schema 并只在当前回合激活扩展工具。合同在 `internal/toolcontract/`，目录和回合激活在 `internal/toolregistry/`。新增领域工具时，可选实现 `ToolSearchMetadataProvider`，不要另建平行的 `tool_search` 注册体系。
- DOCX 模板自动化由 `internal/tools/content/extract.go` 暴露，长期说明见 `docs/architecture/document-automation.md`。`docx_template_fill.go` 负责保真填写，`docx_legacy_fields.go` 统一处理 `MERGEFIELD`、可填写书签和类型化内容控件，`docx_tables.go` 检查表格拓扑，`docx_table_cells.go` 提供稳定单元格地址、候选推断与固定格填写，`docx_data_mapping.go` 负责 LIMS/企业字段匹配和置信度，`docx_repeat_rows.go` 按数组复制模板行，`docx_template_profile.go` 按模板结构指纹在 `.openkitty/document-templates/` 保存可复用规则。先用 `inspect_template`/`preview_mapping` 让 Agent 生成声明式 `field_mapping`、`table_cells` 与 `repeat_rows`，首次成功填写可启用 `save_template_profile`，后续批次用 `use_template_profile`；正式填写优先启用 `strict` 和 `verify_complete`。方案文件不得包含样品值或实验结果。
- Web 端“工作台 → 文档自动化”由 `web/document_studio.js` 与 `web/document_studio.css` 提供完整的导入、映射核对、Word 预览、生成和方案复用界面；`internal/app/document_automation_api.go` 只封装既有 `extract` DOCX 引擎，禁止另写一套页面专用填表实现。对应 API 是 `/api/document-automation/inspect`、`/generate` 和 `/profiles`。
- 统一界面自动化入口是 `computer`，长期说明见 `docs/architecture/computer-use.md`。`internal/tools/computer/` 只编排会话、短动作组、观察和验证；浏览器执行必须复用 `internal/tools/browser/`，桌面执行必须复用 `internal/tools/desktop/`，不要新增平行的鼠标或浏览器实现。Linux Web 流程优先使用 CDP；桌面流程先调用 `computer(action=status)` 检查 X11/Wayland 后端。
- `workflow_learner` 把一次成功的 `computer` Session 编译成参数化 `recipe.json` 和可激活的 `SKILL.md`，支持 `preview`、`save`、`replay`、`evaluate`、`inspect`、`list`。编译器和确定性评测位于 `internal/workflowlearn/`，工具适配位于 `internal/tools/skills/workflow_learner.go`；执行必须回调现有 `computer`，不得绕开统一界面后端。样品值、实验结果等演示输入必须替换为 `${inputs.<name>}`，不得写入复用方案；正式复用保留成功断言，并对 DOM 序号和桌面坐标给出稳定性警告。
- 用户对 Word、Excel、PPT、PDF 等复杂产物的长期格式偏好在 `internal/memory/artifact_preferences.go` 中解析并存入 `UserProfile.Constraints.artifact_format_preferences`，无需新增数据库列。当前请求中的明确格式要求始终覆盖长期默认；一次性格式要求不得自动写入画像。

## 目录与代码归属

| 路径 | 主要职责 | 修改原则 |
| --- | --- | --- |
| `cmd/openkitty/` | 单一产品入口和版本注入 | 只调用 `internal/app` 的公开启动函数，不放业务实现 |
| `cmd/releaseguard/` | 发布包凭据与敏感内容检查 | 所有正式构建都应先运行 |
| `internal/app/` | 应用装配、生命周期、跨模块协调和迁移兼容 | 只保留必须同时认识 Gateway、Agent、DB、配置等多域类型的逻辑；不要新增可独立测试的领域算法 |
| `internal/agentloop/` | 回合状态、计划、Lease、恢复 | 依赖底层合同，不依赖 Gateway |
| `internal/agentos/` | AgentOS 请求、Schema、持久化和协议模型 | 认证、真实任务执行和 Gateway 绑定留在宿主层 |
| `internal/pipeline/` | 检查点、返工、槽位、输出/来源契约 | 具体业务流程放 `internal/pipelines/<name>`，跨域调度留在 `app` |
| `internal/aiemployee/` | 员工配置、匹配、工作时间和质量策略 | 派发与 Gateway/Pipeline 集成由 `app` 装配 |
| `internal/channels/`、`internal/channelhost/` | 渠道协议、消息格式和渠道生命周期 | 通过 Runtime/回调接 Agent，不反向依赖 `app` |
| `internal/modelruntime/` | 模型调用策略、降级、并发与健康检查 | 不依赖应用总配置类型 |
| `internal/memory/`、`internal/session/`、`internal/conversation/` | 长期事实/记忆、会话持久化与恢复、上下文压缩 | 不要把三者当成同一层数据 |
| `internal/ragmodel/`、`internal/rag/`、`internal/tools/knowledge/` | RAG 数据模型、宿主检索和知识工具 | 数据模型不反向依赖 HTTP/Agent 实现 |
| `internal/toolcontract/` | Tool/Call/Result、读写属性、恢复和沙箱合同 | 保持稳定、轻量，供 Registry/Middleware/Agent loop 共用 |
| `internal/toolregistry/` | 工具注册、筛选、Schema 暴露和执行 | 不在 Registry 内堆具体工具实现 |
| `internal/middleware/` | Agent/Tool 执行策略与前后处理 | 这不是普通 HTTP middleware |
| `internal/tools/<category>/` | workspace、network、content、development、operations、skills 等具体工具 | 新 Tool 放最接近的现有类别，通过窄接口/回调注入宿主依赖 |
| `internal/security/`、`internal/permissionpolicy/`、`internal/projectctx/` | 凭据边界、权限模式、租户/workspace 隔离 | 安全边界不能为兼容或测试便利而绕过 |
| `pkg/codegraph/` | 仓库内外均可复用的代码图能力 | 不能依赖产品宿主层 |
| `builtin/`、`default-skills/` | 会嵌入发行二进制的 Skills 和运行时资源 | 只能包含通用参数，严禁真实凭据、主机和个人路径 |
| `web/` | 无打包步骤的原生 HTML/CSS/JS | 修改后做 JS 语法检查；重建 Go 二进制才能更新嵌入资源 |
| `electron-app/` | 包装已构建 `openkitty.exe` 的 Electron 桌面壳 | Go 后端仍从 `cmd/openkitty` 构建 |
| `docs/`、`scripts/`、`testdata/` | 文档、自动化入口、测试夹具 | 一次性本机脚本和私有材料放 `.local/`，不要堆到根目录 |

依赖方向应保持为：

```text
cmd/openkitty -> internal/app -> internal/* + pkg/* + 根 package assets
```

稳定领域包不得导入 `internal/app`，任何 `internal/*` 都不得反向导入 `cmd/openkitty`；基础工具包不得依赖 Gateway、Agent、数据库等上层业务类型。遇到循环依赖时，优先抽取小合同、DTO、interface 或 callback，不要通过全局变量或复制类型绕过。

## 实现约定

- 从泛化能力角度修复业务场景：先抽象结构信号、稳定地址、声明式计划、可复用执行器与评测，再把具体模板作为测试夹具。禁止在产品逻辑里按企业名、模板名、字段原文、固定样品类型或某个示例行号堆叠 `if/else`；只有协议、文件格式或可验证结构差异才可以成为分支条件。
- 修改行为时优先补或更新靠近实现的测试。纯领域逻辑与测试一起进入对应 `internal/<domain>`；只测试公开 API 时可用外部测试包。
- 新增 Tool 时同时确认：Tool 合同、读写/并行/审批属性、参数 Schema、注册点、Middleware/权限影响、结果回执和测试。具体实现不得为了访问宿主对象直接搬进 `internal/app`。
- `internal/app` 中相同主题与独立领域目录同时存在通常是“宿主装配 + 中立领域”的有意分层，不要仅因名字重复就合并。
- 涉及任务终态、取消、恢复或重放时，保留幂等、Lease fencing、checkpoint 和“写操作结果不确定时禁止自动重放”的语义。
- 涉及多租户、用户身份、workspace、文件路径、网络访问和 Shell 时，必须保留现有边界检查，并增加越权/逃逸失败用例。
- 外部进程和长操作必须绑定 `context` 的取消/超时；不要引入无法终止的后台命令。
- 跨平台进程与路径逻辑使用现有 `processutil` / `portablepath`，并维持 `windows` 与 `!windows` build-tag 实现对称。
- 不手改 vendored/minified 前端库，如 `web/mermaid.min.js`、`web/pdf*.js` 等；除非任务就是升级对应依赖。
- Go 文件必须 `gofmt`。仓库存在记录于 `scripts/gofmt_legacy.txt` 的历史格式债务，但新改文件不能增加债务。
- 文本默认 UTF-8；仓库通过 `.gitattributes` 规定大多数文本为 LF，`.ps1`/`.bat` 为 CRLF。

## 配置、数据与安全

- 仓库只提交 `openkitty-config.template.yaml`；真实 `openkitty-config.yaml` 已被忽略，不能提交、复制到文档或测试夹具。
- 模型密钥优先通过环境变量引用；数据库和 Gateway 也支持 `OPENKITTY_*` 环境覆盖。不要在代码、Skill、脚本、日志、fixture 或示例中写真实 Token、密码、私钥、生产地址和个人绝对路径。
- `builtin/` 会整体进入发布二进制，因此其脚本、模板、reference 和生成资源同样属于凭据扫描范围。
- 数据库、日志、记忆、交付物、缓存和本机操作历史属于运行时状态。使用现有忽略目录，新的本机专用内容优先放 `.local/`。
- 修改配置结构时同步检查模板、默认值、环境覆盖、配置恢复/热重载、序列化脱敏和相应测试。
- 不要用 `--dangerously-skip-permissions` 绕过系统/磁盘破坏边界；该参数只为旧启动脚本兼容，危险操作仍必须被拦截。

## 运行、构建与验证

从仓库根目录运行。模块名虽然是 `mac`，但产品构建入口不是根目录，而是 `./cmd/openkitty`。

```powershell
# 本地启动；实际配置文件保持未跟踪
go run ./cmd/openkitty --config .\openkitty-config.yaml --path .

# 常用入口参数
go run ./cmd/openkitty --version
go run ./cmd/openkitty --mcp

# 聚焦测试 / 只编译
go test ./internal/<package> -run '^TestName$' -count=1
go test ./internal/<package> -run '^$' -count=1

# 全仓测试和构建
go test ./... -count=1
go build -buildvcs=false -trimpath ./...

# 发布凭据检查
go run ./cmd/releaseguard

# 与 CI 一致的 Windows 质量门
.\scripts\quality_gate.ps1
.\scripts\quality_gate.ps1 -CompileOnly
```

Linux/macOS 对应入口是 `bash scripts/quality_gate.sh [--compile-only]`。质量门依次执行 release guard、`go mod verify`、gofmt 检查、工作区/暂存区 whitespace 检查、`go vet ./...`、测试和全包构建。正式平台包使用 `scripts/build/` 下脚本，不要另写一套版本注入和打包流程。

前端改动至少对所有改过的脚本运行：

```powershell
node --check web/app.js
node --check web/chat.js
```

再根据修改范围启动服务并检查 `http://localhost:8090/web`、浏览器控制台以及相关 API/SSE 行为。`package.json` 提供的是文档/渲染辅助依赖，不代表 Web UI 有 bundler 或 npm build 步骤。

### 当前模块化迁移基线（2026-09-13）

`internal/test/` 目前集中停放了大量声明为 `package app` 的旧测试，但 Go 按目录确定包，`internal/test` 与 `internal/app` 不是同一编译包。已知 `go test ./internal/test -run '^$' -count=1` 会因直接引用 `internal/app` 的未导出符号而出现 `undefined`，因此当前 `go test ./...`/质量门不是绿色基线。

同一迁移还留下若干相邻测试断点：`internal/session` 的测试导入环、`internal/mcp`、`internal/nativeenv`、`internal/projectctx`、`internal/tools/development`、`internal/tools/knowledge` 的旧符号引用；`internal/memory` 全量测试目前还有 `TestSessionMemoryExtractionTextKeepsToolArtifactsOnly` 的既有产物路径断言失败。判断新改动时先运行被改包的聚焦测试与主程序构建，再把这些全仓失败作为迁移债务单独报告。

处理相关测试时，应把测试移回被测实现所在目录，或改成 `package app_test` 后只测试公开 API；不要在 `internal/test` 堆大批兼容别名来掩盖边界问题。开始代码任务前先记录基线失败，结束时区分“既有迁移失败”和“本次新增失败”。迁移完成后应删除或更新本段状态说明。

## 完成任务前

1. 回看 `git diff --check` 和 `git status --short`，确认没有覆盖用户改动、加入运行时文件或意外生成大文件。
2. 运行与修改范围最接近的测试；能运行全量质量门时再运行。若受已知基线阻挡，报告精确命令、首个可操作错误和已通过的聚焦验证。
3. 涉及 API、配置、包边界、运行流程或脚本入口时，同步更新最接近的长期文档；不要用工作日志代替规范文档。
4. 最终交付说明修改了什么、验证了什么、仍有哪些已知限制。不要把“已编译”描述成“完整测试通过”。
