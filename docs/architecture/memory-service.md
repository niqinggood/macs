# macs 统一记忆服务

当前实现：2026-10-14。默认复用应用数据库与 Go 运行时，不增加独立记忆服务、图数据库或向量数据库；语义召回按已有配置使用 embedding 后端。

## Agent 任务后的记忆整理

`internal/memory/reflection.go` 与 `reflection_store.go` 提供第一版任务复盘：受限模型从宿主确认的 Agent 原话、真实工具结果及少量相关旧任务提出偏好、事实或经验候选。简单问答、独立图片/音乐生成、未核实入口与嵌套执行不提交这类任务；混合会话的历史消息不作为本次学习输入。

- 普通本机 Web Agent 的任务终态和 review outbox 在同一数据库事务落库。Generate 仅通过私有 sidecar 提供原话与回执，不授权终态写入。失败任务可以留下真实失败依据，全部模型结论先保持 candidate。
- Gateway 生命周期中的 Go worker 每 15 秒检查一次；任务结束后至少等待 60 秒。它只处理 `review`，旧 summary/derive handler 不会误领 review。普通 Web 的旧全文会话摘要/图谱自动提取退出这条链，避免从混合 QA 历史学习；其他入口的兼容链保持独立。
- 输入最多 8000 字符、20 个工具回执；相关任务最多 12 个，输入资料总量限制 16000 字符。输出最多 2000 token、5 条候选。每个用户/工作区每日最多 12 次调用，耗尽后延至次日；provider 单次调用不自行重试，持久队列最多重试 5 次。
- 模型只能引用本次提供的来源及证据编号。程序核对原话、角色、来源指纹与权限；用户偏好不能采用助手回答或工具结果作为原话。推断习惯至少需要 3 个独立任务，仍必须人工确认。次数和原话存在不保证概括语义正确。
- `memory_review_sources` 保存有界、固定的任务输入；`memory_evidence` 保存记忆与原话/结果的关联；`memory_changes` 记录版本操作；`memory_review_budgets` 记录调用额度。Entry 新增 `when_to_use`，管理分页返回依据，确认后召回带上使用说明。
- 提交候选前，在同一事务核对 worker lease、来源指纹及撤回状态，并锁定已有 Entry；后台不能覆盖人工编辑、确认或删除。编辑/遗忘复盘条目会清理该条目的证据，退役关联的旧复盘输入并清除其负载；旧任务重放或更换 Entry ID 不能再次学习它。其他已存在的独立记忆和原任务历史保留。
- 后台调用沿用原任务选定的模型，并绑定该任务捕获的账号。数据库只保存模型别名，不保存密钥。重启后自定义模型可以从当前配置恢复；平台登录 sidecar 不持久化，恢复任务不能借用其他浏览器登录。

首期宿主接入限本机 Web Agent。跨入口统一学习、完整语义冲突/派生血缘重算、自动经验验证与 Obsidian 同步仍按 [升级方案](memory-system-upgrade-plan.md) 后续实施。MySQL 使用现有 GORM 模型和真实递增提交 fence；本次自动回归使用 SQLite，不等于完成 MySQL 实库联调。

```powershell
go test ./internal/memory -run '^TestReview' -count=1
go test ./internal/app -run '^TestTaskMemoryReview' -count=1
```

本文中的“记忆”指历史积累中以后可能被召回、作为参考进入提示词的内容，包括任务经历、工具与 Skill 经验、反馈、画像、偏好、学习知识和会话状态。`Service` 的条目集合是其中一层，不是全部记忆。保存来源、管理页面展示、自动召回、按需工具读取分别有自己的合同。

## 运行结构

Web「设置 → 工作与记忆 → Agent 记忆」提供默认开启的总开关，写入既有
`memory_config.enabled`。配置未提供该字段时也默认开启，显式 `false` 保持关闭。
保存复用 `/api/config/sections`，保留记忆窗口、目录及 embedding 设置，热更新时
重新创建受影响的 Agent；新任务使用新开关，运行中的任务沿用已接受的设置。
总开关关闭时，Agent 的 `has_memory: true` 不能重新开启记忆；已保存内容仍可查看、
更正或遗忘。后台复盘提交会复核当前 Agent 和开关，关闭时暂停处理，不删除积压任务。

```text
Agent / AgentOS 可信身份
  -> internal/app/memory_runtime.go 身份、工作区和开关适配
  -> internal/memory.Service
       -> 长期记忆：memory_entries
       -> 一次性兼容导入：memory_service_migrations
       -> 旧值召回屏蔽：memory_retractions
       -> 原来源自动参考开关：memory_source_recall_settings
       -> 会话摘要权威存储：memory_summaries
       -> 持久后台任务：memory_processing_jobs
       -> 摘要入队修订序号：memory_processing_sequences
       -> 有界召回诊断：memory_recall_traces
       -> 持久语义向量缓存：memory_recall_vectors
  -> 统一、有界、带来源的模型上下文
```

事件、用户画像、SCM 会话状态和本地图谱保留各自存储。普通网页 Agent 只通过核实归属的独立来源适配器自动参考部分内容；其他渠道仍保留旧兼容适配。原始 transcript、journal、checkpoint 与 RAG 不属于此服务的长期记忆权威集合。

## 历史来源与实际提示词路径

默认应用 SQLite 文件为工作区 `.openkitty/openkitty.db`，`OPENKITTY_SQLITE_PATH` 可覆盖；MySQL 使用配置的应用数据库中的同名表。知识图谱另存工作区 `.openkitty/knowledge_graph.db`，SCM 在 `.openkitty/context/context.json`。完整分类、文件位置和回想入口见 [记忆来源表](memory-system-summary.md#memory-source-map)，不在文档中复制真实数据库配置。

| 来源 | 自动进入普通 Agent 提示词的路径 | 按需入口或边界 |
| --- | --- | --- |
| 网页的任务结果、已有经验、学习知识、用户原话和反馈 | `webMemorySources` 先核实持久会话、用户、助手、项目、工作区及平台账号归属，再将各条来源独立交给 `Recall` 筛选 | 来源是 `user_statement/user_feedback/task_execution/task_insight/learning_knowledge`；学习知识还必须关联合法事件；不拼入完整画像或租户级工具统计 |
| 非网页渠道的旧历史、画像、知识和工具统计 | 兼容 `MemoryStore.GetMemoryContextForTenant` 生成 `history_and_profile` 片段，再进入 `Recall` | `search_history` 查成功经历、`get_stats` 查汇总统计、`knowledge` 查学习知识；旧兼容片段的过滤粒度与网页来源不同，不能当作个人列表授权 |
| `memory_entries`、`memory_summaries`、当前会话 SCM | `Recall` 在可信命名空间内查询 active 条目、相关摘要与当前会话状态 | `agent_memory` 的 recall/list/snapshot、`session_context` 的 get/list/inject；candidate 不自动召回 |
| 工作区知识图谱 | 网页只参考当前合法会话且用户原话证据仍可核对的 `knowledge_fact`；非网页保留 `buildKnowledgeMemoryPrompt` 的旧可见性规则 | 工作区旧关系与原文归档的展示不增加网页自动召回资格；记忆关闭、内部隔离或受限租户时跳过相应路径 |
| `learn` Markdown、旧摘要 Markdown | 不扫描、不自动注入 | `learn insights` 按需读取学习文件；`learn distill` 只返回报告。当前摘要队列使用数据库，旧文件兼容接口不能等同自动召回 |
| 文档资料与 RAG | 不因打开记忆页而自动查询全部资料 | `knowledge_query` / `knowledge_base` 等工具检索后，其结果进入本回合工具上下文 |
| `AGENTS.md` 项目说明 | 已装配的 `projectInstructions` 在动态系统提示中按工作区层级加载 | 属于项目说明，不是 `Recall` 候选；`project_instructions` 可按需核对。全局页的只读展示不改变加载范围 |

普通 Web Agent 路径为 `processTaskWithSession → LLMAgent.Generate → buildMemoryPrompt → Service.Recall → 提示注入扫描 → 模型/工具循环`。自动召回在模型决定调用工具之前发生；后续工具查询是另一个可回想路径，受注册、启用及权限约束。生成召回诊断只证明宿主已准备相关上下文，不证明模型实际采用。

`personalMemoryPromptSourceReferences` 是网页运行时与管理清单共同使用的自动候选入口。`SelectTaskMemoryReferences` 只接纳有真实 tool/skill 布尔执行回执、明确用户反馈、失败原因或可验证衍生学习的任务；普通问答、LLM 调用记录、助手文字中声称生成的产物路径不作为执行证明。同一任务的学习字段只是答案摘句时不重复候选；原始历史字段仍完整保留。失败经验及 Skill 提示通过 `NewLearningEventWithEvidence` 在 `OutputMeta.memory_learning` 记录父事件 ID 和有界工具回执，由宿主批量核验相同归属及父回执；不复制到 CallChain，避免工具统计重复。旧历史浏览入口不受此准入策略限制。

### 原来源停止与恢复自动参考

本机 `POST /api/memory/prompt-sources` 支持 `action=set_recall_enabled`，需要服务端签发的 `view_id/source_id`、原文 `expected_revision`、开关 `expected_recall_version` 和布尔 `recall_enabled`。宿主重新验证原记录归属、内容版本及来源是否属于自动参考路径；客户端不能指定 locator。开关版本从 0 开始独立递增，过期版本返回 409。记忆窗口显示“停止自动参考 / 恢复自动参考”，停用项保留可读、可编辑和可恢复状态。

`memory_source_recall_settings` 只保存原有可信命名空间和来源地址的哈希、状态、版本及更新时间。`SourceRecallKey` 将 memory_event、knowledge、knowledge_fact、summary、chat_message 按原记录和会话定位；同一事件的 final_answer/learn_note/insights、同一知识的正文/摘要共享开关，编辑正文不会自动恢复。`Recall` 在 embedding 与候选补查结束后、组装提示词前重新检查，诊断记录 `disabled_by_user`；查询失败不组装该次记忆上下文。缺失或无效的原源定位不进入网页召回。

停止自动参考不删除原记录、不改变已发送的请求，也不阻止原对话或工具显式查询。Entry 沿用原有更正/遗忘合同；AGENTS.md 等系统约定、当前会话消息不使用该开关。普通 Web 的独立来源支持此控制，非 Web 的无 locator 旧画像/历史大块不能宣称已支持逐条停用。

普通网页聊天的 HTTP 宿主在接受任务时冻结私有来源范围，不能靠 JSON 中填写 `channel=web` 冒充。来源读取前要求当前持久会话仍匹配冻结的项目、工作区和账号；标明 Web 但没有可信范围的回合不会退回旧画像/历史混合适配。每条历史参考单独做关键词相关性、旧值撤回、去重与 token 预算，最多读取 120 条有界来源。推导出的经验仍是历史参考，不成为已确认事实；工作流等独立执行入口不能仅凭网页来源套用普通聊天合同。

简单问答 `task_type=qa → simpleChat` 使用固定系统说明、当前会话的 user/assistant 历史及当前输入，不调用 `LLMAgent.Generate`、统一召回或记忆工具。图片、音乐等直接生成也不走 Agent 记忆循环。各入口的消息或任务记录能够保存和查看，不应将“保存了历史”描述成“本次已读取长期记忆”。

`relook` 没有当前可调用实现；`recall` / `remember` 是 `agent_memory` 的动作。Agent 内部 reflection/rethink 用于当前执行的检查，不是独立复盘工具；重复失败经验另存为 `memory_events`，没有独立的 `traps` 表或工具。

### 旧来源的归属限制

旧图谱仅绑定工作区，`triples` / `verbatim_memories` 没有 tenant、user 或 Agent 列。会话来源格式 `conversation:session:<ID>` 需要宿主先验证持久会话及原话；旧 `conversation` 没有会话归属。`QueryVerbatim` / `SearchVerbatim` 按实体或全文查询，不执行用户过滤，不能直接作为个人列表入口。普通图谱提示屏蔽旧无会话的 conversation，并限制带会话来源的事实只在原会话可见；其他来源的工作区图谱可见性不等于用户确认或完整个人归属。

旧 `user_profiles` 以 tenant + user_name 查询，没有工作区、Agent 或独立 user_id 列。同名画像必须经过可信会话或事件身份核对，默认语言和默认回答风格不等于用户表达的偏好。RAG 知识库具有 tenant 字段，分块继承父知识库范围，没有个人工作区字段；读取应沿用可信的知识库授权，不能用调用方随意提供的租户 Header 将资料并入个人记忆。

## 身份与范围

宿主从认证后的 ExecutionIdentity 构造 Owner。命名空间使用个人分区、租户、subject、规范工作区与 Agent 的哈希，模型参数不能指定或覆盖归属。AgentOS 检查执行工作区与 Agent 工作区一致，并验证个人工作区路径对应当前 subject，阻止误复用另一个用户实例时导入其旧文件。绑定 SCM 时也检查同工作区；既有个人数据库分区机制保持不变。

非租户本地/控制面调用保持个人版工作区兼容语义，不因为字符串上下文中的 user_id 改变权限。多用户接入必须使用认证的 AgentOS subject 边界。

原有 user/project/session 范围与命名空间保持不变：user、project 均受当前个人工作区和 Agent 约束；session 仅当前会话可见。新增 global 表示当前所有者的当前工作区内全部助手共用，不能跨工作区根目录、用户或租户共享，也不是所有人的公共记忆。

`NewGlobalService` 用 `workspace-global-v1`、个人分区、租户、subject 和规范工作区计算独立命名空间，不含 Agent；共享条目的 `agent_name` 为空。它复用 `memory_entries`、CAS、撤回哈希与向量缓存，但不导入旧 JSON/SCM，不绑定会话上下文，也不接受自动摘要或处理任务。只有宿主明确的手工保存能写 active 的 global 条目；普通 `agent_memory` 与 `session_context` 工具的范围仍是原有范围，不能写全局。升级不会改变、晋升或移动现有 user/project/session 内容。

旧历史事件 FTS 和最近会话兼容适配器按分区与 Agent 过滤；传入用户名时再要求用户名一致。未指定用户名的兼容查询仍是分区内当前 Agent 的查询，不是多用户授权入口。网页自动来源改用已核实 Web 会话与事件范围，不依靠放宽旧用户名查询。会话结束产生的失败经验与 Skill 提示继承原事件的完整身份（包括 UserID、UserName、渠道与会话）；已有画像的存储规则不因新增展示而改变。

后续 `LLMAgent.Generate` 保存的事件沿用纠正偏好与召回已使用的用户名规则：明确 `user_name` 优先，缺失时使用已有 `user_id`，两者都缺失则保持为空；不回填旧记录，也不改变可信租户、subject 或工作区归属。

## 长期记忆合同

`Generate` 在拼接上下文之前将可信原始用户回合交给 `RecordExplicitUserTurn`。宿主 `beginExplicitMemoryUserTurn` 仅从私有 `tenantRequestEvidence` 读取原话，再经 `explicitMemoryWriteContext` 传给领域服务；不回退 Generate 的拼装输入、附件说明或字符串 channel，子 Generate 不继承自动写入资格。现有工具的证据合同保持不变。领域服务仅读 `WithUserTurn` 的私有证据，接受 300 字以内单行明确“记住…”或严格的长期默认要求，保留客户、项目及适用条件；引用、一次性要求、问题、明显凭据和提示注入不自动保存。默认 user，明确本项目的要求采用 project；使用 `CreateWithSource` 保存 active 条目及 `user_explicit`、任务引用、原话。内容与范围生成稳定 key，重复输入不改版本，也不覆盖手工更正或复活已遗忘 key。它不导入旧画像、不调用模型、不猜测语义冲突；不同措辞的新要求不会自动替换旧要求，仍可通过已有更正/遗忘操作整理。记忆关闭、内部隔离或无可信交互原话的后台执行不写入；后续召回沿用相关性、范围与预算规则，不自动常驻。

本轮设计参考了 [Mem0 的新增及关联策略](https://github.com/mem0ai/mem0/blob/d26ba5e4518cef7fff3c66d4f715b21adf9feabe/mem0/memory/main.py#L918)、[Graphiti 的事实失效与原证据保留](https://github.com/getzep/graphiti/blob/2f0fb35b749f1ff17377892e099882176e453393/graphiti_core/utils/maintenance/edge_operations.py#L880) 和 [Letta 的记忆质量审查](https://github.com/letta-ai/letta-code/blob/2943efe47e8c49e8ef2b78df100ba8e262c2415a/src/agent/subagents/builtin/reflection-v2.md#L74)。这里只借鉴机制，未复制第三方源码，也未引入其外部服务依赖。

`memory_entries` 保存所有者、Agent、key/value、scope、kind、state、priority、pinned、version、来源会话和创建/更新时间，并记录来源类型、任务引用、短原文引用和候选确认引用。已有数据库通过 GORM 自动迁移新增列；旧记录的来源类型为空或 `legacy`，不能据此追认用户明确授权。

- kind：preference、fact、decision、experience。
- state：active、candidate、deleted。
- 只有 active 参与自动召回；list 可用 state=active/candidate 筛选，筛选在数据库候选集上限之前执行，候选堆积不会挤掉有效条目。
- confirm 需要明确 scope 和 expected_version，仅把匹配版本的 candidate 晋升为 active，不改写内容、标签、原始来源或创建时间；版本递增。候选内容改过、已生效、已删除或属于其他用户/会话时，旧确认请求失败。
- 新推测的 candidate 不得覆盖同 scope/key 的 active 或 deleted 项，冲突推测应使用单独的提案 key；明确的新长期要求仍可通过 active 写入更新或重新记住。推测标记和确认依据仍依赖模型遵守工具说明，并非服务端语义验证或独立人工审批。
- session 写入需要实际会话。remember 默认 session；长期写入必须显式选择 user/project。
- correct 使用 expected_version 做比较并交换，拒绝旧版本覆盖新值。
- forget 清空 value、tags 和来源会话，保留删除标记和递增版本。
- 来源会话用于追溯，不等于逐消息的完整证据链，也不自动证明模型解释正确。
- 生产工具从宿主冻结的用户本轮原文取得来源，模型参数无法指定来源类型或任务引用。只有用户原文以“记住/请记住/帮我记住/remember”等明确指令开头，且指令后的负载与写入 value 完全相同时，user/project 长期写入才会立即 active；其余工具写入成为 candidate，不自动召回。例如“请记住不要使用 Word”不能只凭其中包含 `Word` 就激活值为 `Word` 的记忆。直接传入领域服务的宿主调用仍由宿主负责授权。
- 模型调用 confirm 时，用户本轮必须使用完整确认命令，例如 `确认记忆 color_theme` 或 `confirm memory: color_theme`；命令负载必须与 key 完全一致（区分大小写，可使用成对引号），相似前缀、额外语句和否定不能授权确认；保留原始候选来源，另存确认任务引用和短引用。纠正 active 长期记忆同样需要本轮明确包含新值的记住指令。session 工作状态不受长期晋升规则影响。
- 短引用最多 300 字符，只保存与写入值相同的原文片段，不复制整段用户请求；删除时一并清空引用。该规则保守：自然句中抽取字段、改写、同义表达、一般陈述会进入候选，需要用户核对后确认。它不能证明原文真伪，也不是跨存储完整证据链或人工审批。

原有 `agent_memory` 工具名称和操作保留，增加 kind/state；snapshot 可带 query，数据库服务增加 confirm。生产注册点使用数据库服务，旧 JSON 工具构造器仅供兼容调用且不暴露 confirm。

候选管理示例（身份与会话由宿主提供）：

```json
{"action":"list","state":"candidate","scope":"user"}
{"action":"confirm","key":"document_format","scope":"user","expected_version":1}
{"action":"forget","key":"document_format","scope":"user"}
```

Agent 应先核对候选内容，在用户认可或证据验证后确认；confirm 不要求重新提交 value，可避免确认过程中误改内容。correct 仍可更正内容或按指定状态晋升候选。

`session_context` 的 durable 写入也进入同一集合：preference 映射 user，其余类别映射 project，todo/link 用 fact 加类别标签保存。session 工作状态仍存 SCM。两个工具读写同一长期 key 的版本，不再各自保存一份 durable 值。delete 未明确 scope=durable 时仍只删除当前会话状态。

## 兼容导入

首次允许访问时，在事务中导入当前工作区/Agent 的手工 JSON 和 SCM durable 项，并写入命名空间迁移标记。手工 JSON 同 scope/key 项优先，失去会话归属的旧 session 项不导入。损坏手工文件会返回错误，不用空内容覆盖它。

旧文件保留，不删除、不双写。新运行链路以数据库为权威；迁移标记阻止旧文件在删除、更正或重启后重新恢复内容。首次访问前读取的是构造服务时加载的旧文件状态，因此升级前应停止旧实例，避免新旧版本同时写入。

## 自动召回

普通 `LLMAgent.Generate` 通过 buildMemoryPrompt 生成一个 envelope，统一收集长期条目、SCM 会话项、相关摘要和该入口允许的历史来源。网页使用核实过的独立来源；其他渠道使用旧画像/历史与图谱兼容片段。原来单独注入 SCM、KG 的路径已从普通 Generate 移除；这不是每次工具迭代都全量重读历史。

- 普通 user/project/global 条目必须与本轮问题有关键词或语义相关性；高优先级本身不构成召回资格。当前会话工作状态和用户手动常驻的 user/global 偏好可以不受相关性约束；SCM 会话项最多 10 条且不读取 durable 项。数据库摘要即使属于当前会话也要求相关。网页历史来源与非网页旧补充片段都要求关键词相关性；旧混合片段中的局部命中不代表每句话都相关。
- 查询 embedding 后，普通及共享数据库候选重新核对当前命名空间、ID、版本、active 状态、scope/session 及原元数据；已更正、删除、变成候选或改变归属的旧快照被排除。本轮不把已捕获的旧版本替换成新值，下轮可以选择新版本。当前 SCM 的工作状态在数据库核对之后加入，不把其合成的 version=0 当作数据库记录；取消会终止整个召回。这是外部检索之后的核对，不是锁住数据库直到模型完成。
- 同 key 的有效且符合召回条件的条目按 session > project > user > global 选择；不相关的项目条目不会遮挡相关的个人或全局条目。助手收集自己的条目和同所有者工作区的全局条目，在一个 envelope 中统一执行去重、常驻预算、总 token 预算和最终安全扫描，不能简单拼接两个已生成的提示。
- 使用中英文关键词匹配；中文复用现有分词器和通用停用词，过滤“我们、一下、看看”等口语泛词，不再枚举全部相邻汉字。分词器不可用时保留整段词匹配，召回覆盖会下降；没有新增 embedding 依赖。查询最多处理 4096 字符、24 个去重关键词。除最近/高优先级候选外，另以当前问题的关键词在同一身份、工作区和状态范围内补查较早的条目与摘要，再统一去重、排序和执行预算。补查在 SQL 中先按关键词匹配权重排序，再截断为最多 200 条长期条目与 50 条摘要；之后才参与统一片段排序，防止高优先级的弱匹配先占满候选。LIKE 参数转义通配字符，关键词不能改变身份或状态过滤。
- 默认预算 2000、最多 8000 个估算 token，单片段最多 1200 字符；去重后按相关性与优先级填充。无相关性命中的常驻偏好合计最多占本轮预算的三分之一，仍可能因条目过长或总预算不足被排除。无相关记忆、无常驻偏好且无可用会话状态时返回空上下文。
- 基础长期条目候选集最多 200 条，基础摘要候选集最多最近 50 条；关键词补查另有上述上限，按 key 的 Lookup 不受候选集限制。持久语义缓存另按当前命名空间分页检索，可补充基础候选之外的旧条目。SQLite 的普通 `LIKE` 在大库中仍可能扫描较多记录；语义缓存也受超时和回填预算限制，均不能保证完整覆盖。
- 每个片段带来源类别、ID、版本和来源会话。当前请求优先，记忆不能授予权限或证明当前外部状态。
- 最终 envelope 继续经过已有提示注入扫描。受限租户仍不能读取全局图谱，隔离内部任务仍不能读取或修改个人记忆。

宿主队列生成摘要时，通过 `ExtractContextWithStore` 读取当前数据库摘要并提交到 `memory_summaries`，按当前问题召回；不再先写 Markdown 再写数据库。旧 Markdown 保留，但该队列不再读取、覆盖或双写这些文件，避免失效 worker 在数据库拒绝前污染文件。独立调用 `ExtractContext` 的兼容文件路径仍保留。旧摘要文件不会批量扫描或自动晋升；已有数据库摘要继续使用。

## 持久语义召回

`internal/memory/recall_semantic.go` 在现有关键词通道之外召回改写问法。Gateway 沿用 `embedding` 与 `memory_config.embed_mode` 配置：后端未启用或显式关闭记忆 embedding 时保持关键词行为，不下载或启用新模型。api、ollama、local_llama、local_onnx 复用现有 provider；发送给后端的查询和文档各最多 2000 字符。

- 先按可信 namespace、active 状态和当前 session 过滤，再读取或生成向量。candidate、deleted、其他会话的工作状态和其他所有者的数据不会发送给 embedding 后端。
- `memory_recall_vectors` 每条来源保存一个向量、来源 ID、修订指纹和模型指纹，不复制原文；模型指纹由后端、模型、端点、维度和本地模型文件元数据生成，不包含 API Key。切换模型或修订会重新生成，重建 Service 或进程重启可以继续复用。
- 每轮当前助手与共享集合的语义处理共用 3 秒 context，按 64 条分页读取各可信命名空间中已有当前模型向量的来源，再只读取足以填满本轮回填队列的缺失来源，避免冷启动时先将整个未索引历史加载到内存。先计算已有缓存的相关性，再使用剩余时间补算缺失向量，避免慢回填遮挡后面已经可用的条目和摘要。每个命名空间最多回填 4 条长期记忆、4 条摘要；共享集合不接受自动摘要，所以只有条目回填。共享集合为空时不多发查询 embedding 请求。不启动脱离请求的后台任务，也不另设过短的单文档超时使慢后端一直无法完成回填。冷启动或大量更新时需要多轮逐步填充。已缓存的较早条目不受基础 200/50 条候选限制，但很大的已索引集合仍可能因时间预算而未扫描完。
- 相同问题的查询向量在同一个 Service 内最多缓存 1 分钟、32 条，按最近使用淘汰；缓存键包含 namespace、模型身份与查询文本的哈希，不保存问题原文、不落盘。只缓存成功且有效的向量，取消始终优先；更换模型、所有者或问题不会复用旧查询向量。条目内容仍在每轮从数据库读取，查询缓存不会绕过更正、遗忘或版本校验。
- 余弦相似度至少 0.60 的每个可信命名空间前 12 个候选进入同一次排序、scope 覆盖、去重和 token 预算；语义分值为相似度乘 20，最终相关性取关键词分值与语义分值的较大值。当前阈值需用实际部署模型校准，不能把确定性测试向量的通过率当成模型效果。
- 改写旧值或遗忘时，在原事务内删除该条目的向量和当前命名空间的摘要向量；摘要重新索引仍先过 retraction。向量提交校验 active/版本、摘要修订和 retraction；最终条目选择还对关键词基础候选与语义候选统一做当前版本核对，拒绝 embedding 期间已失效的旧快照。
- 后端报错、非法向量、维度不符或语义超时会退回可用的关键词/缓存结果；用户取消仍取消整个召回。`RecallResult.SemanticStatus` 与召回诊断的 `semantic_status` 为 disabled、ready、partial、unavailable，选入原因增加 semantic、hybrid。ready 表示本轮扫描完成，不代表模型语义判断已被验证。

运行存储、缓存、隔离与回退回归：

```powershell
go test ./internal/memory -run '^Test(SemanticRecall|RecallQueryCache)' -count=1
go test ./internal/memory -run '^TestRecallFencing' -count=1
```

这些测试使用确定性向量；原 `TestRecallEvaluation` 保持不启用 embedding 的 9/12 基线，不能据此声称真实模型的三个改写问题已改善。

## 常驻偏好与召回诊断

管理页可以将当前 Agent、当前用户工作区中最多 5 条 active 的 user/preference 条目设为常驻。默认和旧数据迁移都不自动常驻，priority 不代表用户选择。`SetPinned` 通过版本校验和同一更新语句内的数量检查保护并发；常驻条目独立于基础 200 条候选查询，避免被新条目挤掉。pin/unpin 都增加版本；内容、类型或有效状态变化会取消常驻，遗忘也清除常驻标记。模型记忆工具不提供 pin 参数，只有可信宿主或本机管理界面可以调用该操作。

global/preference 复用同样的版本校验，当前所有者工作区内最多 5 条共享常驻偏好，计数不随选择的 Agent 改变；与原 user 常驻上限分开计数。两类无关键词常驻条目仍共同受一次召回预算的三分之一约束。更正或遗忘全局条目后，召回同时检查当前 Agent 与共享命名空间的撤回哈希；embedding 期间发生更正/遗忘时再读取条目版本和撤回哈希，拒绝过期的共享候选和旧补充片段。遗忘不等于物理删除历史来源。

`agent_memory(action=snapshot)` 仍支持 query；不传 query 时仅召回可用会话状态与常驻偏好。显式查看已存条目继续使用 list/recall，不依赖自动注入。

领域服务 `RecallResult.Decisions` 给出已评估候选的选入/排除原因：相关、常驻、当前会话、范围覆盖、不相关、旧值屏蔽、重复、预算不足，以及 `stale` / `version_changed`。初始查询不读取其他所有者、candidate 或 deleted 内容；初始有效候选在检索期间失效时，诊断只记录其原 ID、版本与排除原因，不保留正文。它不是全库扫描报告。每轮最多保存 512 条决策，优先保留选入项，超出数量单独计数。

宿主在最终安全扫描后将诊断写入 `memory_recall_traces`。仅记录不透明条目 ID、版本、来源类别、相关性分值、排序分值、原因、估算 token 和时间，以及按命名空间散列的会话/任务引用；不保存问题、记忆 key/value、原始会话 ID、原文引用。正常、空结果、阻断和脱敏分别标记 ready/empty/blocked/redacted。blocked 会将选入标记改为 false；redacted 的逐项选择与 token 估算描述扫描前的结果。ready/redacted 表示已生成供模型参考的上下文，不证明后续模型调用成功、实际采用或回答正确。诊断失败只记录宿主错误，不阻断聊天。

每个命名空间最多保留 100 次记录，写入时清理超过 7 天的记录；查询始终隐藏超过 7 天的记录，闲置命名空间的物理清理由下次写入触发。管理界面只展示最近 20 次，沿用现有 localhost 与同源限制，不开放远程多用户管理。读取和写入都绑定用户、工作区、Agent；关闭记忆或隔离内部执行时不新增记录。

## 更正与遗忘的边界

更正或删除条目时，事务保存旧 value 的归一化哈希和长度，不在屏蔽表保存旧内容原文。自动召回会排除包含旧值原文的普通条目、摘要及补充历史、上下文、图谱片段；embedding 后再次读取当前撤回哈希。匹配保留原始字符位置；拉丁文字、数字与下划线要求词边界，因此遗忘 `Go` 会屏蔽 `Go compiler`，不会屏蔽 `Google`。中文仍使用原文片段匹配，空白与大小写归一化规则保持兼容。候选、删除项和版本已变化的快照不自动注入。

这是一项保守的原文匹配保护：可能过滤含相同短语的整个参考片段，不能识别全部语义改写。现有画像、图谱、旧文件与聊天证据不会因此物理删除，历史查询工具也保持自己的权限和留存规则。不能把该操作描述成跨存储完全清除个人数据；完整语义血缘、证据清理和用户账户删除仍需单独实现。

## 持久后台处理

摘要和本地允许的事实提取先入 `memory_processing_jobs`，再由宿主调用 `ProcessPending`。内容哈希提供幂等；每次最多处理 4 个任务，共享 SCM 信号量限制并发。

- 每任务处理超时 2 分钟，领取租约 3 分钟；领取后重新读取实际尝试次数。
- 先选择尝试次数少的可运行任务，再按可用时间和创建时间排序，给尚未尝试的任务处理机会。
- 临时失败最多尝试 5 次，包含首次；前 4 次失败后分别等待至少 30、60、120、240 秒。到达上限进入 `failed`；明确的坏 JSON 等不可重试负载错误一次即进入 `failed`。仍在有效租约中的最后一次尝试不会被提前终止，租约过期后才回收。
- 处理失败只持久化有限类别（如 `handler_failed`、`timeout`、`invalid_payload`），并在批次结束时把类别及任务 ID 返回宿主日志；不在这条记录中写错误原文。
- 进程中断或调用取消后，任务等待租约到期，后续允许的同用户/工作区/Agent 回合可以重新领取；更新完成状态必须匹配领取令牌。
- 每个命名空间、会话的摘要入队操作在事务中分配递增 `SourceRevision`；重复负载不重复入队或增加修订。可信宿主直接保存摘要也分配修订。序号持久保存在 `memory_processing_sequences`，重建 Service 不重置。
- 队列摘要提交在同一事务中校验当前运行任务的令牌、有效租约、命名空间和会话，并比较已接受摘要的修订。旧修订不能覆盖新摘要，任务进入 `superseded`。兼容旧任务的修订为 0，只允许比现有旧摘要来源更新的内容提交，不能覆盖已有非零修订；升级前无来源的摘要使用更新时间作为保守水位。
- 摘要读取或提交失败不推进提取器成功进度；队列只提交数据库，不修改兼容 Markdown。成功和 `superseded` 任务清空 payload，保留幂等记录；`failed` 任务保留 payload，当前没有自动清理或面向用户的重试界面。
- 宿主重新检查记忆开关与隔离边界，所有模型调用绑定 context。

当前没有独立常驻轮询器：恢复由后续允许的用户回合和新任务入队触发；无新回合时不会自行处理。修订序号表示快照**入队顺序**，不是原始消息范围或业务事实时间，无法识别调用方晚入队的旧消息；完整消息水位与派生血缘仍待实现。图谱提取沿用其自身幂等规则，摘要提交的修订保护不能等同于所有图谱副作用的事务化。这些任务只处理记忆，不用于自动重放真实业务动作。

## 召回质量评测

`internal/memory/testdata/recall-eval.json` 保存不含真实用户数据的对话场景：个人偏好、项目决定、纠错经验、230 条无关高优先级记录、较早低优先级记录、候选、遗忘、会话隔离和改写问法。`TestRecallEvaluation` 走真实 SQLite 记忆服务，输出 Hit@1、Hit@5、MRR、改写问法 Hit@5、禁止内容泄漏数和单次最多注入的无关项目记忆数。运行：

```powershell
go test ./internal/memory -run '^TestRecallEvaluation$' -count=1 -v
```

2026-09-29 回归复测的结果（与 09-28 筛选优化后相同）：17 个问题（新增无关问题与空查询的空结果断言），其中 12 个有指定目标；Hit@1 与 Hit@5 均为 9/12，MRR 为 0.750，直接措辞命中 9/9，3 个改写问法均未命中，候选/遗忘/跨会话等禁止内容泄漏为 0。测试固定整体 Hit@5 不低于 75% 且直接措辞 Hit@5 保持 9/9 的初期回归线，并要求禁止内容泄漏为 0。该夹具中的无关高优先级项目记忆注入从最多 2 条降为 0 条；新增测试也覆盖无关个人记忆与补充片段、常驻偏好、预算和诊断隔离。本次改善了选择和解释能力，没有提高改写问法的命中率。

该评测仅覆盖确定性的存储与召回，不评价模型最终回答，也不代表真实用户分布。下一步应增加经脱敏并获授权的失败/纠错样本，再比较查询扩展或语义候选层的收益与误召回；不得用样本专属的关键词规则提高分数。

## 个人版记忆管理

工作台“记忆”沿普通 Web 对话的提示词组装代码反查历史来源，不依赖新任务或新调用记录。`GET /api/memory/prompt-sources?agent=...&session_id=...` 返回当前助手、合法会话、原始来源、当前内容以及实际读取入口。没有选择会话时选最近的合法会话。它只读现有数据，不运行 Agent、模型、embedding、后台处理或导入，不写入调用记录。来源分类只是筛选这些真实输入入口，不把整个数据库或存储目录改称记忆。

主界面使用 `GET /api/memory/prompt-sources?agent=...&mode=management&include_learning=1` 管理当前保存、供未来使用的内容；`mode=management` 禁止同时携带 `session_id`，也不自动选取最近会话。用户/项目/共享条目、合法摘要、说明文件与笔记沿现有来源读取；任务记录逐条核对其原会话及所有者，排除会话消息、SCM 和 session 条目，管理清单不会改变运行时召回范围。父窗口切换对话不重载记忆。主导航为全部记忆、用户偏好、项目与全局约定、知识库；旧资料仅通过核对入口打开，过去调用诊断仅在显式 `inspect=1` 页面显示。编辑仍修改当前来源并保留 CAS，后续调用按既有条件读取更新后的内容。

| 代码读取入口 | 读取的历史内容 | 带入方式 |
| --- | --- | --- |
| `processTaskWithSession → buildGatewaySessionHistory → Generate` | 当前会话最多 50 条用户/助手 `chat_messages.content` | 清理后作为历史消息；后续上下文压缩可能缩减 |
| `buildMemoryPrompt → Service.Recall` | 当前会话非持久 SCM 工作项、会话 Entry、常驻偏好 | 工作上下文与偏好，仍受范围优先级、安全过滤和长度预算约束 |
| `Service.Recall` | 活跃个人、项目、工作区共享 Entry 和合法会话摘要 | 按当前问题相关性召回，不保证每轮全部带入 |
| `buildMemoryPrompt → webMemorySources → Recall` | 可核对用户原话、任务结果、`learn_note/insights`、经任务关联的学习知识及当前会话有原话证据的图谱事实 | 分别通过归属、相关性、撤回规则和预算校验，再进入用户消息前缀 |
| `buildSystemPromptDynamicForContext → ProjectInstructionLoader.Load` | 实际加载的项目/全局说明文件及其导入内容 | 动态系统消息；外部全局文件只读 |

完整旧用户画像、工具统计总表、无可核对会话归属的旧偏好和图谱、未被工具检索的知识库文档不属于普通 Web 自动注入源，保留在另行标明的存储资料中。工具检索结果只有实际调用后才可能进入后续回合；来源清单不把库存数量当作已注入数量。

每个来源显示原始表、字段或文件、记录时间、读取位置及 `direct/relevant` 方式；`direct` 表示对应读取路径直接加入上下文，不能证明未来每个发送回合都保留完整内容。没有问题输入时不运行空问题 Recall 来得出“没有记忆”。原记录通过 `POST /api/memory/prompt-sources` 的 `agent/view_id/source_id/action/expected_revision/value` 修改或删除，沿用原源 CAS 和所有者边界。`view_id/source_id` 为 Gateway 内 15 分钟有效的随机定位句柄，最多 16 份、每份 256 个来源；重启或过期后刷新即可。客户端不能传表、路径、namespace 或自行构造定位器，列表也不创建虚假的模型调用记录。

主模态默认打开“全部记忆”，按真实提示词读取入口汇集可参考的已存内容，直接显示原始位置、记录时间和进入提示词的条件；另提供用户偏好、项目与全局约定、知识库和 AI 学习积累四个筛选入口。当前会话历史、工作状态和本会话摘要不再作为独立记忆分类展示，普通对话由任务页面查看；原有 session API、上下文构建和实际调用诊断保持不变。原任务结果、学习字段、其他会话的相关摘要和未属于偏好/约定/资料的已存参考汇入 AI 学习积累，分别标明学习笔记、任务原始参考和其他保存内容，不冒充经过核实的结论。每类顶部说明读取时机、选取条件和进入提示词的位置。统一“新增记忆”入口复用原有空白编辑窗口及范围授权；原来源的编辑、删除按钮置于正文前。来源正文默认只显示一次；技术路径和调用诊断折叠。同一 `memory_events` 原记录的结果和学习字段内容相同时只合并展示，仍保留各自定位句柄；编辑弹出独立模态，用户选择原字段后仅修改该字段，不批量改写或去重存储。

偏好视图按类型汇集 user/project/global/session preference，保留原范围及实际进入方式，不扩大召回范围。客户、项目或 case 的要求必须写明对象和适用条件；当前只有原有个人、项目/工作区、共享和会话范围，没有独立客户或 case ID 过滤。新增偏好可选择个人通用、当前项目或仅本次合法会话；session 的写入仍经现有 `CreateSessionNote` 和身份验证。偏好侧栏数只计已接入提示词路径的记录，旧提取记录另列数量和核对说明，不能将整段历史对话当作确定偏好。核对后保存打开空表单，由用户填写明确要求，原记录保留。

偏好界面仅将已接入读取路径的偏好列入主清单；未整理的旧记录在“待整理旧资料”折叠入口中单独计数，查看不会把它们加入提示词或导入新条目。界面直接说明 AI 的使用机制和人工处理步骤。人工处理是查看原文、提取明确要求、选择范围、保存；没有明确要求的原文可以保留，不需要逐条转成偏好。整理按钮放在详情顶部，旧记录正文默认折叠；空表单只显示示例占位文字，不把整段历史对话预填成用户要求。新增偏好更换 project/session 范围时仍保持 preference 类型，范围选择复用原有授权契约。

历史图谱详情显示 subject/predicate/object 构成的记录说法，以及原有效期和原文证据状态；`has_capability` 只转为易读的能力描述，不证明当前工具可用。已结束有效期或无证据的旧记录仍可查看原始关系和出处，但查看不会触发自动召回。Web 的自动知识事实来源须通过 `ConversationFactSources` 的有效期、证据、合法会话和原话核对，再通过运行时相关性、范围和预算检查；归档浏览不改变这些条件。

知识库默认显示“资料与笔记”，使用 `knowledge_source=materials` 读取有合法任务关联的学习知识及受限 RAG 资料库目录，排除图谱关系。当前工作区原学习笔记也提供同一来源的阅读入口，复用已有私有来源句柄、原文和修改权限，不新增或复制存储。知识库侧栏只计资料/笔记，不把三元关系或原文片段数量称为文章数。原文摘录、提取记录通过独立切换读取，保留分页、真实记录 ID、关联对象及证据边界。没有保存文章标题/全文的提取记录明确标为未关联文章，不把 `Assistant` 等实体名当成文章标题。笔记正文使用现有 marked 的 token 解析并以 DOM 文本节点展示，原 HTML、图片和链接不创建主动内容或网络请求；原文件的编辑和 CAS 校验不改变。

用户偏好分类直接展示已有偏好和旧偏好记录，知识库分类通过原知识分页接口展示已存资料。旧记录保持原归属与只读限制，并标注未自动注入；核对并保存偏好是用户明确创建自己的长期偏好，不冒充修改旧原文。知识资料仍受原工作区、数据库及来源证据边界约束。保存数据与自动召回源分别标明，不把旧图谱全库数量当作已进入提示词的数量。

`include_learning=1` 额外读取 `learn` 工具真实使用的笔记目录；目录由 `knowledge.LearningMemoryDirectory` 统一提供，保留现有兼容路径，不迁移或复制笔记。学习笔记以 `on_demand/tool_result` 展示：`learn insights` 查询摘要或文件工具读取正文后才进入后续模型回合。界面编辑正文复用原文件的路径检查和版本校验，并保留其元数据；不调用分析、蒸馏或模型。待办入口复用工作台原管理弹窗；待办仅在工具查询或执行结果返回时参与后续上下文，不自动成为长期记忆。

### 某次实际调用的记录

高级折叠区保留 `GET /api/memory/inputs?agent=...&session_id=...&input_id=...`，它返回已经保存的发送记录，而不是默认记忆列表。每份记录包含任务、调用序号、时间、发送状态和在请求中匹配到的来源片段。省略 `input_id` 时选最新记录。旧任务没有这样的记录，不意味着没有可进入提示词的历史信息。

宿主在读取统一 Recall、历史消息和项目说明时建立私有来源映射，在普通、流式及对应协议的最终请求序列化、授权之后、HTTP 发送之前核对实际文本。Recall 的选中状态、检查点和 `MemoryEvent.SystemPrompt` 均不是完整发送凭据：后续压缩或快捷执行可能删减它们。历史消息还须保留真实消息身份及完整角色/正文映射，当前问题里恰好出现相同文字不能被归成旧历史来源。被摘要或重写后失去确切映射的内容不冒充原文引用；本轮新产生的工具回执也不冒充过去存储的记忆。

`agent_memory` 的 snapshot/recall/list 和 `session_context` 的 inject/get/list 通过私有 observer 登记真实返回表示，在工具结果进入后续模型请求后才成为输入引用。JSON 转义的值按真实发送形式展示，修改时仍定位原行、原会话 tuple 及原值摘要。仅执行工具、被取消或返回结果尚未送入下一轮都不是已发送来源；候选统计和固定能力目录也不作为历史记忆。RAG 检索结果目前缺少稳定原片段 ID 的发送映射，不能从文档名或相似内容猜测可写出处；该缺口不影响无 RAG 绑定的助手，启用 RAG 时仍须通过原知识库管理入口管理上传资料。

`memory_prompt_inputs` 保存已匹配的来源摘录及服务器内部定位信息，不保存完整请求、模型凭据或未选中候选。每份记录最多 128 个来源及 1 MiB，每个助手/工作区/所有者命名空间最多 1000 份、保留 30 天。`attempted` 表示开始尝试发送，`succeeded` 表示收到成功 HTTP 响应，`failed` 表示发送失败或服务拒绝；这三者都不证明模型采用了内容。只有宿主封装的普通 Web audience 可以开启记录，字符串 `channel=web`、远程 AgentOS 身份或其他 workspace 不能伪造授权。界面读取时再次验证主会话及原来源会话，服务器定位器不通过 HTTP 返回。

右侧把“本次提交的记忆片段”和“当前来源内容”分开。`POST /api/memory/inputs` 只接受 `agent/input_id/source_id/action/expected_revision/value`；`action` 为 `update` 或 `delete`。客户端选择服务器签发的不透明记录/来源标识，不能提交数据库表、namespace、租户、用户或文件路径。写操作只作用于原记录并校验当前版本，冲突返回 409；历史输入摘录保持不变。

原来源管理支持统一 Entry、精确会话 SCM 项、会话摘要、原历史消息、任务经验/结果字段、有明确授权事件关联的学习知识、确切会话图谱事实，以及当前工作区内实际加载的项目说明文件。摘要更改推进处理修订屏障，已排队的旧任务不能覆盖更正或重建已删摘要；经验删除同时校验并清理 `learn_note/insights`，结果为空时不会把原问题冒充任务回答。图谱删除只失效原 ID，其他会话中相同关系不会被连带删除。工作区外的全局说明只读，符号链接替换及保留租户目录不能借此写入。原消息删除是删除该消息正文来源，不会替用户删除整项任务或交付物；已有其他派生来源仍需分别管理。

新功能从之后的 Agent 模型调用开始记录。旧任务没有保存完整发送出处时明确无法还原；简单问答和直接媒体生成不走 Agent 记忆召回。没有输入记录不代表数据库为空，也不能据此推断用户偏好、工具收据或知识从未被使用。

### 资料来源目录

`GET /api/memory/catalog?agent=<已配置助手>` 返回当前本机个人工作区的来源目录，供记忆窗口的 **查看资料来源 → 保存在哪里** 使用。接口只接受一个可选的 `agent` 参数；省略时沿用默认助手。其他参数、重复参数或非法查询返回 400，未配置的助手被拒绝，非 GET 返回 405。它沿用个人管理接口的 loopback 与同源检查，不是远程 AgentOS 的跨用户目录接口。

响应包含 `agent`、`agents`、`scope="current_personal_workspace"`、`read_only=true`、`sessions_verified`、`counts_bounded=true` 和 `sources`。每个来源给出以下信息：

| 字段 | 含义 |
| --- | --- |
| `id`、`storage.kind/tables/paths` | 来源类型及实际存储位置；路径只返回工作区内的相对路径 |
| `available`、`status` | 存储是否可读，以及 ready / unverified / unavailable；不表示一定有个人记录或模型已使用 |
| `recall.mode`、`enabled`、`tools` | automatic 表示存在自动参考路径，on_demand 表示需工具查询；运行时尚未加载时 enabled 可为 null |
| `recall.requires_runtime_check=true` | 每次真正召回仍需验证身份、会话、开关、相关性与预算 |
| `counts.verified/unverified/unit/bounded` | 经过当前边界核对的局部数量；未知用 null，已核对为空才是 0，不能据此推算全库总数 |
| `note_codes` | 范围、来源证据及未提供计数的原因，不包含记录正文 |

目录列出十种来源：

| 来源 ID | 位置与计数范围 | 回想方式 |
| --- | --- | --- |
| `events` | `memory_events`；最多 200 个已验证 Web 会话内、匹配用户与助手的完成或失败事件 | 自动参考部分相关经历，或 `search_history` 查询 |
| `profiles` | `user_profiles`；旧画像缺完整工作区归属证据，不读取正文或报告个人数量 | 非网页保留旧自动适配；网页不整份参考画像，只使用核实过的用户原话 |
| `knowledge` | `knowledges`；只计 `SourceID` 关联上述可信事件的未软删知识 | 自动参考相关知识，或 `knowledge` 查询 |
| `tool_usage` | `memory_events` / `tool_skill_stats`；目录数量来自最近 50 条可信事件中的实际工具与 Skill 调用，不是统计表的全库汇总 | 网页可参考合法任务来源；非网页保留旧统计提示，或用 `get_stats` 按需查询 |
| `entries` | `memory_entries`；当前命名空间的 active/candidate、已验证会话笔记与同所有者工作区的共享条目 | 有效条目按条件自动召回，或 `agent_memory` 查询 |
| `summaries` | `memory_summaries`；当前命名空间内已验证会话的摘要 | 自动选择相关摘要 |
| `session_context` | 当前 SCM 的工作区内 `context.json`；只计已验证会话可读项 | 当前会话自动参考，或 `session_context` 查询 |
| `knowledge_graph` | 独立 SQLite 的 `triples`、`fact_evidence`、`verbatim_memories`、`entity_aliases`；verified 为有界证据核对结果，unverified 为工作区可见关系归档中尚未核实的数量，旧原文另行分页 | 满足身份与工作区条件的事实可自动参考；查看归档不改变召回资格 |
| `learn_files` | `.claude/projects/E--openkitty/memory/` 兼容目录；仅核对位置和目录是否存在，归属未知，数量为 null | `learn` 按需查询，不自动扫描注入 |
| `rag` | `rag_kbs` / `rag_chunks`；目录不进行全库查询或报告个人数量 | 经独立权限校验，由 `knowledge_query` / `knowledge_base` 按需检索 |

SQLite 路径来自当前连接的文件元数据：只有经路径及 symlink/junction 边界校验、位于当前工作区内的文件才返回相对位置。工作区外的自定义 SQLite 仅提示存在自定义位置，不暴露绝对路径；MySQL 只给逻辑表名，不返回连接串、主机、库名或凭据。

此 GET 只读取已有来源与元数据，不创建 SCM 或图谱、不执行兼容导入、不写数据库、不增加学习知识使用次数，也不调用模型或实际 `Recall`。未知归属的孤儿事件、画像和学习文件数量保持 null，不作为当前个人范围的汇总。页面展开目录不会迁移全库、把来源复制成 Entry，或将 candidate 自动确认；automatic 也不表示某条历史已进入本轮提示词。

### 已保存条目与真实任务来源

任务结束的 `MemoryEvent`、工具调用收据、已有 `LearnNote/Insights`、明确反馈和图谱事实并不等于 `memory_entries`。自动 Deriver 原本写带用户证据的图谱；会话摘要默认累计至少 10000 tokens 才初始化。短对话、普通问答或只做了一次生成时，长期条目数为零不代表没有任务历史。管理 GET 现在同时返回只读 `supplemental` 来源，避免页面把不同存储的任务资料遗漏。

来源只读取当前助手合法 Web 会话：先验证 tenant/subject、Agent、项目、workspace、账号 media owner，并排除 AgentOS 会话，再按精确 session ID、tenant、Agent 和 user ID 读取 `MemoryEvent`。没有会话归属证据的旧记录不会因 Agent 名相同而全部暴露。工具只返回 type/name/success/duration_ms；不读取或返回 system prompt，不返回工具参数、原始输入输出摘要或供应商 error。

`source_type` 区分 `task_execution`（实际结果摘录）、`task_insight`（已有经验记录）、`user_feedback`、`user_statement`（偏好原话）、`knowledge_fact`、`learning_knowledge`（具有可验证事件来源的旧学习知识）、`conversation`（无任务事件时的实际回答摘录），以及原有 `session_context/summary`。每项保留真实 source_id、session_id、创建/更新时间和 read_only；可选 tools、user_feedback、user_rating、user_evidence 均来自原记录。问答摘录不伪称模型已自动归纳，工具收据不伪称长期经验。

`personalMemoryLearnedKnowledgeReferences` 读取 `knowledges` 时，其 `SourceID` 必须精确关联已经通过上述所有者、用户、Agent、工作区、渠道与会话核对的事件，同时要求知识行的分区和 Agent 一致。最多读取 50 条，摘要优先，内容摘录最多 1200 字符，保留真实创建/更新时间；没有匹配事件、归属未知或已失效的来源不展示。查询不增加 `UseCount`、不迁移旧知识，也不自动生成 Entry。上传资料与这些 Agent 学习知识是不同来源。

偏好来源复用长期产物格式/纠正解析，并仅采纳原始用户 `ChatMessage` 中的明确偏好原话；任务事件 `UserQuestion` 可能已被 Skill 或路由处理，不能提升为用户原话来源。助手回答、一次性格式要求、默认画像的 zh/balanced 和统计推断不是用户偏好。`knowledge_fact` 必须有明确 `conversation:session:<id>`、有效时间和证据，且原话仍能在合法用户消息中核对；缺少证据的旧图谱可以作为下述只读历史知识显示，但不获得此可信事实资格。列表不回填画像、运行模型或自动晋升来源。用户核对后可以另存为 Entry，再使用既有版本更正和召回机制；更正/遗忘 Entry 不会改写原任务历史。

长期、项目和偏好分类显示各自对应的只读来源；短期只显示选中会话的工作上下文、摘要与人工笔记，完整结果、工具和反馈在历史任务。global 的可写共享条目仍只由用户手工管理，另可汇总查看目录说明与各助手的已保存条目，不改变原归属。candidate 筛选不混入只读来源。`total` 只统计已保存 Entry；近期任务来源最多 200 个会话、50 个事件、600 条消息、100 个有证据图谱事实及 50 条有关联的学习知识，最终最多 120 项，正文摘录最多 1200 个字符。SCM/summary 沿用各自已有上限。该近期集合无独立分页，不改变 Entry 分页；知识历史另有下述分页，不能将这两种数量与来源目录的局部计数混为全库总数。

来源列表的归属核对和 Runtime 召回是不同查询。网页新适配器复用合法 Web 来源范围；非网页旧 `history_and_profile` 仍可能要求事件 `user_name` 与本轮用户名匹配。新事件的 fallback 不回填旧数据。目录中的“可自动参考”只描述类别路径，完整画像/工具统计等旧兼容路径还需区分入口；它不是每条当前记录已注入的回执。真实选择以召回条件和宿主诊断为准。

### 来源与召回资格

七分类 `view` 响应用 `entry_provenance` 和来源项的 `provenance` 区分归属、内容性质与回想路径，不能把这些字段合成一个“真实记忆”标志：

- `identity` 说明个人归属已核实、只确定为工作区归档，或尚不明确。
- `content` 区分用户保存/确认、用户原话、模型生成、候选、历史未核实资料、项目说明或文档目录；用户保存不证明事实正确。
- `origin` 给出实际记录位置与 ID，以及核实到的会话、事件、消息和原任务链接。原任务只通过合法消息的 TaskID 关联精确持久任务；事件 ID 不充当任务 ID。
- `evidence` 分别说明原话、原任务和事件链接。原话已保存但未在合法原始消息中核实为 `stored_unverified`；`verified_quote` 只返回实际匹配到的那段原话，不能根据另一个已存但未核实的引用猜测。已处理的任务输入不能冒充用户原话。
- `recall.path/eligibility/conditions/enabled` 说明当前入口是否具备候选路径以及还需满足的条件。active 为 conditional；候选排除，归档只供核实，各助手的只读汇总保持原助手范围。SCM 不要求关键词命中；摘要要求相关；网页图谱要求当前会话与核实原话。

这些 GET 不执行实际 Recall，不改写原数据、晋升 Entry 或增加知识使用次数。`missing` 列出缺失证据或来源信息；没有可验证原任务就保留缺失，不能按标题、用户名或模型猜测补出链接。实际选入仍需宿主召回诊断，模型是否采用未被测量。

聚焦回归：`go test ./internal/app -run '^TestPersonalMemory' -count=1` 与 `go test ./internal/memory -run '^TestConversationFactSources' -count=1`。读取实现为 `memory_management_sources.go` 和 `knowledge_sources.go`，没有新增记忆库。

工作台侧栏的“记忆”打开 `/web/memory.html?agent=...`，沿用对话中选中的 Agent，默认查看由提示词代码反查的历史来源。模型调用记录和其他存储资料保留在独立的高级折叠区。旧资料中的原 user/project 内容仍按当前本机工作区与精确 Agent 查看；全局分类使用明确的工作区共享集合，短期分类选择真实 Web 会话后读取会话笔记及只读自动来源。目录记录继续显示创建/更新时间、范围、状态和来源，数据库筛选与分页不依赖自动召回的候选上限。

新增与更正复用同一个编辑窗口，界面将 key 称为“标题”，仍保留原 API 字段及同范围唯一语义。手动新增立即成为 active，并直接写入统一记忆表；同范围的现有或已遗忘 key 不能被新增操作覆盖，用户应更正现有条目或选择新标题。更正和遗忘使用页面显示的版本做比较并交换；冲突不会覆盖较新内容，编辑窗口保留草稿并提示刷新。界面未增加审批或权限开关。有效个人偏好可设为常驻或取消常驻，候选记忆沿用手动确认，导出沿用当前 Agent 的有效长期记忆 JSON，不包括 session 工作状态与已删除条目。

召回记录展示关键词、语义或混合匹配原因，以及语义服务 disabled/ready/partial/unavailable 对应的可读状态。语义部分可用不会被解释为整次召回失败。列表和编辑窗口适配小屏幕，切换视图支持键盘操作。

`GET /api/memory` 接受 `agent`、`scope`（空/user/project/global/session）、`kind`（空/preference/fact/decision/experience）、`state`、`search`、`offset`、`limit`、`session_id`。kind 在 SQL 中先筛选，再统计总数和分页；每页上限 100 条。空 scope 保持原 user/project 集合。响应保留 `entries/total/offset/limit/agents/agent`，增加当前助手最多 200 条已验证 Web 会话元数据 `sessions`，以及只读自动来源 `supplemental`。

### 七类视图与旧知识分页

页面使用 `GET /api/memory?view=<分类>&agent=...` 明确分类；原不带 view 的 scope API 保留兼容。view 可为 short、long、preferences、project、global、knowledge、history。查询只接受 view、agent、session_id、scope、kind、state、search、offset、limit，以及知识分类专用 knowledge_source；不接受身份或工作区参数，重复或不匹配的范围被拒绝。session_id 仅 short/history 可用，knowledge_source 为 all/graph/quotes/learning/rag；每页 1–100 条，关键词最多 100 字符。视图 GET 不构建新 Agent、不创建 SCM、不执行旧文件导入。

| 视图 | 已保存条目 | 只读来源 |
| --- | --- | --- |
| short | 选定会话的 session 笔记 | 当前任务 SCM 与摘要，不重复完整任务日志 |
| long | 当前助手 user 范围的非 preference 条目 | 任务已有经验与具备可信事件来源的学习知识 |
| preferences | user/preference 条目 | 用户明确偏好原话、有依据的偏好事实、经过严格匹配的画像约束，以及明确标为未核实的工作区历史偏好 |
| project | 当前助手 project 条目 | 非偏好的任务经验、学习知识与有证据的图谱事实 |
| global | 当前所有者工作区的显式 global 条目 | 根目录 AGENTS.md 与已配置、已加载且属于同工作区的各助手 active 条目 |
| knowledge | 无 Entry 混入 | 图谱关系、旧原文、任务学习知识与上传资料库元数据，按来源筛选和分页 |
| history | 无 Entry 混入 | 选定合法会话的结果、问答摘录、用户反馈；原消息与实际工具过程保留历史入口 |

全局聚合保留来源 Agent、scope、真实时间和只读属性，每个助手最多 20 条、最多检查 50 个助手，合计最多 100 条来源条目并可加一项目录说明。AGENTS.md 只读根目录内的普通文件，经过真实路径校验，文件不超过 64 KiB，显示最多 8000 字符；不会读取父目录、绕过 symlink 或修改说明文件。汇总查看不会把其他助手的条目写入当前助手、晋升为 global 或扩宽 Runtime Recall 的范围。AGENTS.md 在此显示也不证明本轮任务已加载它。

画像约束只读取有合法原话依据的 forbidden/preferred 工具要求或产物格式偏好。候选用户名来自已核验事件；同名事件必须全部属于当前合法用户/助手/工作区会话范围，同名画像还必须唯一。随后逐项匹配原话解析结果与已保存约束，页面显示实际用户原话，不泄露画像的其他字段。默认值、性格职业推断、身份不明或同名冲突仍不作为个人偏好显示。

旧 SCM 文件另由 `ReadLegacyPreferenceArchive` 只读查看工作区固定 `.openkitty/context/context.json`：只接受 category=preference、无 session_id、没有明确其他工作区或外部身份的记录。此页同样要求应用数据库是物理绑定当前工作区的 SQLite，不能借共享 MySQL 或外置数据库将文件归成个人来源。复合存储 key 中带有会话 ID 的项也被排除，即使公开字段为空；内部执行上下文和工作区不适用的记录继续过滤。文件必须通过真实路径边界检查、为普通文件且不超过 2 MiB，限读并检查取消；损坏或不可读时提示此来源不可用，不当作零条或删除原文件。

返回 `source_type=legacy_preference`、`ownership_verified=false`、真实创建/更新时间与只读位置，不补当前 Agent 或个人身份；最多 200 条，每项内容最多 1200 字符。这些是工作区历史偏好，不是已确认的个性画像或 active Entry。查看不构建 SCM、不迁移文件、不激活 durable 上下文、不改变 Runtime 会话隔离或自动记忆；只有用户核对后显式另存才新增可编辑条目。原文件总数、preference 数量和当前可见数量可能不同，不能承诺全部旧项都会展示。

知识视图用 `sources_total/sources_offset/sources_limit` 表示当前可见知识集合的分页；`entries/total` 仍只代表 Entry。学习知识沿用严格事件关联和有界摘录；图谱使用已有工作区 SQLite 图谱的 `WorkspaceKnowledgeArchive`，只读分页原关系并保留真实时间。带会话来源的关系仅接纳已验证的本助手会话；无会话的旧资料独立标为历史工作区知识、`ownership_verified=false`，不会冒充当前助手或已确认事实。quotes 仅展示没有明确会话 audience 的旧原文；带会话原句暂不通过此归档入口读取，避免无法核对原话来源。分页搜索只过滤这个可见集合，不改变旧事实的有效期、证据或自动召回资格。

`view=knowledge` 的 `knowledge_source` 只接受 materials、curated、all、learning、rag、graph、quotes，省略时仍默认 curated 以保持接口兼容。materials 只包括合法事件关联的学习知识与受限 RAG 父库元数据；图谱关系保留在其他视图。curated 包含合法事件关联的学习知识、核实原话的 `knowledge_fact` 与受限 RAG 父库元数据；不会默认铺开旧图谱归档。all 将学习知识、RAG 元数据和历史图谱关系合并分页：同一原图谱 ID 已有核实事实时，以核实来源替换该条归档，不重复添加或计数。graph / quotes 明确请求历史关系 / 原文归档，继续只读；learning / rag 分别限定对应来源。非法来源值或在其他 view 传此参数会被拒绝。

图谱来源保留 `graph.subject/predicate/object/origin/valid_from/valid_to/confidence`，断言与依据分开显示。`evidence_availability` 为 missing、unverified_quote 或 quote_verified；存在证据文本或置信度高不证明断言真实。核实事实可提供 `quoted_evidence`，逐条 provenance 的 `evidence.verified_quote` 只来自合法原消息中实际匹配的引用，不能根据 source/confirmed 字段猜测。学习知识的 tags 只透传原 `Knowledge.Tags` 中实际存储的标签，不生成新的用户画像或偏好。

上传资料库只展示未软删父库的名称、描述与已有文档/片段数量，不读全体片段正文。只有应用 SQLite 文件经真实路径校验位于当前工作区、且父库为默认本地租户时，才进入此工作区只读元数据视图。共享 MySQL、工作区外 SQLite 或其他租户继续沿既有上传资料管理及授权路径；不接受请求 Header 自报租户以扩大范围。旧图谱原文、资料库和各助手条目保留只读位置与归属提示；需要长期使用的结论由用户核对后显式另存，查看不改变 UseCount、原记录或 Entry 状态。

`scope=session` 未带 session_id 时仅返回可选择的会话及空条目，不读取未限定会话的工作状态；带 ID 后宿主核对已持久会话的当前工作区、project、Agent、tenant/subject、媒体账号边界，排除 AgentOS 会话，随后才查询 session Entry。人工会话笔记通过专门的 `CreateSessionNote` 插入，仍有版本更正与遗忘，下一轮同一会话进入真实 runtime Recall。SCM 与数据库摘要由 `WorkingReferences` 限当前工作区/助手命名空间/会话读取；返回 `read_only:true` 和真实来源时间，摘要只有更新时间，不伪造创建时间，也不提供编辑自动来源的写接口。候选状态筛选排除自动 active 来源，关键词筛选作用于来源标题与内容。

`POST /api/memory` 保持 `agent/action/key/scope`，action 为 create/confirm/correct/forget/pin/unpin。create 还传 `kind/value`，支持 user/project/global，以及带已验证 session_id 的 session 笔记；其余操作仍传 `expected_version`，更正还传 `value`。global 要求合法配置 Agent 作为界面上下文，但共享归属不取该 Agent；global 与 session_id 不能混传。pin/unpin 仅支持 active 的 user 或 global preference。所有修改都保持 CAS 冲突 409、现有或已遗忘同范围标题不得被新增覆盖。

`GET /api/memory/export?agent=...` 保持导出原助手 user/project 长期条目，追加 `scope=global` 可导出当前工作区共享条目；不导出 session/SCM/summary 或 deleted 内容。`GET /api/memory/recalls?agent=...` 返回当前助手最近的召回元数据，global 来源标记为 `long_term:global:<kind>`。知识库和聊天历史继续使用自己的管理/只读入口，不将 RAG、原始聊天和任务持久记录冒充已确认的记忆条目。

管理操作只作用于当前本机工作区，不接受调用方自报的租户、用户或 workspace 身份。普通 Web Gateway 目前不是多用户认证 API，因此这些端点继续要求请求从 loopback 到达、Host 为 localhost/loopback 且浏览器 Origin 同源；平台支付登录不会改变本机记忆所有者。远程 AgentOS 用户不能借此管理其他用户的记忆，多用户管理仍需单独以认证 subject 和个人 workspace 装配端点。

这里的“遗忘”清空统一长期记忆条目并屏蔽旧值召回；不会物理删除原始聊天、旧 SCM/JSON 文件、画像、图谱或 RAG 文档。导出的 JSON 含记忆值和来源片段，应按个人数据保管。

## 偏好管理的可读内容与确认

Web 端“用户偏好”分为“已保存的偏好”和“待你确认”。列表直接展示完整要求的摘要，详情展示具体要求与依据：任务复盘候选引用 `memory_evidence` 中 role=user 的原话、任务编号和原会话入口；明确用户要求可展示 `source_quote`；人工填写或修改则说明依据是用户当前保存的要求，不虚构过去的对话依据。

旧图谱关系标为“旧记录认为：…”。找不到对应原话时明确说明无法证明用户曾这样要求，仍允许用户按现在的意愿修改并另存。页面将同一来源、同一对象、同一关系和值的重复记录合并展示，保留逐条选择原始记录的入口；记录日期已失效或尚未到期的内容默认收起，已知助手自身的描述从偏好列表排除。这些都是展示处理，不迁移、删除原记录，也不改变图谱的召回资格。

旧记录另存为用户明确要求后可供相关 Agent 任务参考，无须启动另一个服务。新复盘候选修改后仍保持 candidate，必须另点“确认，供以后参考”才变为 active，避免把编辑误当确认；简单问答不使用这些长期偏好。自动参考仍受当前助手、工作区、相关性和提示词预算约束。当前页显示的是可供以后参考的记录，不证明某次模型调用已采用。

“原始对话”入口只接纳当前管理响应 `sessions` 范围内的明确会话编号：旧图谱的 `session_id`、用户原话证据的会话或已核验 provenance。点击后复用 `/api/session/{id}`，在只读模态框展示按时间保存的消息，可逐次增加读取量，最多 2000 条。关联会话与偏好原话证据分开说明；裸 `origin=conversation`、日期或自由文本中的会话号不用于猜测出处。没有关联时提示旧记录未保存具体会话号，并提供全部任务入口；读取失败、无消息和部分读取分别显示，不把它们描述成对话被删除。

选中有会话关联的偏好时，依据区自动加载最近 200 条对话，默认展示用户和 AI 的完整交流，可切换为“仅用户原话”；完整对话弹窗同时保留。旧提取未标记具体原话时说明这一点，不将整段会话或助手回答当作偏好的准确依据。浏览器预览缓存最多 32 个会话，只用于展示，刷新或切换助手时取消请求并清空；加载原文不修改历史记录或自动确认偏好。使用说明以保存/确认、开始 Agent 任务、查找相关内容、作为背景处理四步展示，再分别解释项目规则、长期条目、历史任务和按需学习笔记的使用路径；代码路径收进开发者技术说明。

## 验证

```powershell
node --check web/memory.js
node --check web/memory_groups.js
node scripts/qa/test_memory_preferences.cjs
go test ./internal/memory -run '^Test(MemoryHardening|Service|RecallEvaluation)' -count=1
go test ./internal/memory -skip '^TestSessionMemoryExtractionTextKeepsToolArtifactsOnly$' -count=1
go test ./internal/app -run '^Test(MemoryService|PersonalMemory)' -count=1
go test ./internal/memory -run '^TestPrompt' -count=1
go test ./internal/app -run '^Test(PromptMemoryRuntime|PersonalPromptInputs)' -count=1
go test ./internal/app -count=1
go vet ./internal/memory ./internal/app
go build -buildvcs=false -trimpath -o NUL ./cmd/macs
```

覆盖一次性导入、跨会话召回、候选排除、版本冲突、删除后重启不复活、原文屏蔽、摘要召回、预算、不同用户/租户/工作区/Agent 隔离、可信身份不可被字符串参数覆盖、关闭/隔离边界，以及后台重试、恢复和租约 fencing。

仓库既有 internal/session 测试导入环及 memory 工具产物路径断言失败仍需单独报告。测试与构建通过不等于已完成真实模型或多用户 HTTP 端到端验收。
