# MACS 记忆系统

当前统一服务与迁移契约见 [memory-service.md](memory-service.md)。普通 Agent 已接入该服务，
以下模块作为权威集合、兼容来源或证据层协作。

## 领域边界

`internal/memory/` 管理长期事实、用户画像、手工记忆、会话上下文和自动提取。
`internal/session/` 管理原始 transcript、journal、checkpoint 与恢复；
`internal/conversation/` 管理消息压缩。历史记录不等于已经确认的长期事实，
记忆也不能授权动作或证明当前文件、外部系统状态。

目前存在几个独立存储：

| 实现 | 内容 | 存储 |
| --- | --- | --- |
| `MemoryStore` | 任务事件、用户画像、工具统计与历史召回 | 应用数据库及 Agent 日志目录 |
| `Service` | 显式长期条目、候选、版本、删除标记、摘要与后台任务 | 应用数据库的 memory_* 表 |
| `AgentMemoryStore` | 手工 JSON 兼容来源 | `.openkitty/memories/<agent>.json`，一次性导入后不再双写 |
| `SessionContextManager` | 会话状态与旧 durable 来源 | `.openkitty/context/context.json`；新 durable 写入 Service |
| `KnowledgeGraph` | 时序三元组与原文 | `.openkitty/knowledge_graph.db` |
| `SessionMemoryExtractor` | 后台会话摘要生成 | 宿主队列通过 Service 读写数据库；独立文件调用保留 `.openkitty/session-memory-<id>.md` 兼容路径 |

显式记忆与 durable 上下文已统一到 Service，但所有来源尚未形成完整语义血缘与跨存储删除契约。模型侧自动召回和提取
已有统一开关，但历史查询、知识管理与恢复存储各有自己的入口，不能把关闭记忆
理解为禁止一切历史访问。不能因为其中一个模块成功写入，就宣称所有模块已经
同步更新或完全遗忘。

## 当前 Agent 的实际读写链路

`LLMAgent` 直接持有 MemoryStore、SessionContextManager 和 SessionMemoryExtractor；
Deriver 按需创建。知识图谱由 Gateway 初始化后以进程级对象提供，手工 AgentMemoryStore
作为兼容来源绑定当前 workspace 和 Agent。Service 统一普通回合的召回与显式长期读写。

| 数据层 | 主要写入时机 | 普通回合是否自动读入模型上下文 |
| --- | --- | --- |
| 任务事件、画像、统计、Knowledge | Generate 完成保存事件，随后更新统计/画像；显式长期格式偏好在回合开始处理 | 是，经 MemoryStore 生成有界文本 |
| SCM 会话上下文 | session_context 的 session set；回合结束按规则提取会话项 | 是，读取当前 session；durable 由 Service 提供 |
| Service 长期记忆 | agent_memory 及 session_context durable 写入 | 是，核心偏好与相关有效条目经统一预算召回；候选不注入 |
| 知识图谱 | Deriver 从对话提取；其他知识/代码能力也可写入关系 | 是，仅同 workspace 本地 Agent；自动对话事实限原 session |
| 会话摘要 | 满足阈值后经持久任务提取，按有效租约和入队修订提交数据库 | 是，按问题召回；队列不读写旧 Markdown，旧文件不批量扫描 |
| transcript/journal/checkpoint | 用户消息、模型/工具事件和回合状态持续记录 | 通过会话恢复机制使用，不作为确认的长期事实库 |

一轮普通对话大致按以下顺序执行，隔离内部任务与租户边界会跳过部分分支：

```text
可信身份、workspace、session
  -> 处理明确的长期偏好/纠正
  -> Service：有效长期条目 + 画像/历史适配 + SCM 会话项 + 允许的 KG + 相关摘要
  -> 统一预算、去重与原文屏蔽，再进行提示注入扫描
  -> 当前请求 + 会话消息 -> 模型与工具循环
  -> 工具可按需读写 agent_memory / session_context
  -> 保存任务事件与 transcript / checkpoint
  -> 更新画像、提取 SCM 会话项、后台提取 KG 事实与会话摘要（队列摘要提交数据库）
```

MemoryStore 默认自动拼接最近 3 条会话事件、至多 1 条其他会话的相似问题以及
至多 2 条相关 Knowledge。历史召回融合 FTS、中文词项匹配与可选向量结果；
词项降级只扫描最近 200 条事件，向量只扫描最近 50 条事件。向量按查询需要
生成，缓存仅在进程内，约 200 项，没有覆盖完整历史的持久向量索引。

历史适配器仍主要检索任务问答事件。Service 同时收集长期记录、SCM、允许的 KG 和
数据库摘要，统一默认 2000 个估算 token 的预算、片段去重与来源标记。

## 当前可见能力与剩余问题

- 已有可用的历史参考、长期格式偏好、手工键值记忆、会话上下文和实体关系能力。
  “记忆”并非一个单独的数据库或服务，而是这些能力的组合。
- 手工长期记忆已接通自动注入，摘要已有索引消费者；仍受相关性、条数和预算限制。
- 范围语义不一致：画像受租户/用户名约束；手工 JSON 受 workspace/Agent 约束；
  SCM 在 workspace 内共享；KG 是进程级对象。手工 user scope 不是跨项目个人总画像。
- 手工与 durable 上下文共享数据库权威记录；画像、KG 等旧来源仍独立。
  更正或删除后按旧值原文哈希屏蔽补充片段，但不能识别全部语义改写，也不物理清除证据层。
- 画像中的话题、性格、职业倾向等仍有关键词推断逻辑，应与用户明确确认的偏好
  区分。新增 KG 原文校验只证明引用存在，不证明模型理解或关系推断必然正确。
- 摘要与 Deriver 已使用持久任务、幂等和租约，失败可重试。恢复依赖后续允许的同一范围回合，
  没有独立常驻轮询器；无新回合时不会自行处理。
- 旧记录不能仅因新增规则而被当作已验证事实；手工来源会话与 KG 原文证据也不是
  同一种追溯信息。统一管理页面、跨存储完全遗忘和质量评测仍未形成完整闭环。

`internal/conversation/` 的压缩解决当前模型窗口长度，`internal/session/` 的恢复
解决任务连续性；RAG 解决外部资料检索。这三者与长期记忆协作，但不能互相替代。

## 手工记忆的范围

`agent_memory` 的当前范围如下，所有范围均绑定个人 workspace 与 Agent 命名空间：

| `scope` | 语义 | 会话可见性 |
| --- | --- | --- |
| `user` | 当前 workspace 内持久用户偏好 | 跨该 workspace 的会话可见 |
| `project` | 当前 workspace 内持久项目事实 | 跨该 workspace 的会话可见 |
| `session` | 当前会话的临时事实、状态 | 仅原会话可见 |

`user` 当前不表示跨 workspace 的个人全局记忆，`project` 也不表示通过版本控制
自动共享。后续若实现跨项目画像，应另行定义身份绑定和迁移，不能直接扩大
现有文件的读权限。

## 当前简化版运行规则

默认运行仍使用现有 Go、SQLite 和 JSON 文件，不增加外部记忆服务。
`memory_config.enabled` 是总开关；Agent 的 `has_memory: false` 可以进一步关闭，
`has_memory: true` 不能覆盖总开关。配置改变后重新创建 Agent 或重启生效，本版
不承诺在线热切换。关闭后停止模型侧记忆召回、保存和自动提取；手工清理的
`agent_memory(forget)` / `session_context(delete)` 仍可用。隔离的 Pipeline 内部
执行不能通过这些工具读取或删除用户记忆。聊天历史和恢复 checkpoint 保持各自语义。

宿主仅向属于同一 workspace 的本地 Agent 注入进程级知识图谱，并扫描召回文本。
受限租户执行不读取或写入该共享图谱；它仍可使用原有私有 workspace 上下文。
不启动进程级 Dreamer，以避免它越过 Agent 开关持续处理记忆。

新增的自动事实必须带可在清理后的用户消息中找到的原文引用，AI 回答或虚构引用不能
单独作为证据。事实与 `fact_evidence` 在 SQLite 事务中提交，重复提取同一来源
的同一事实不重复插入；不再额外双写 SCM。原文存在不等于模型理解必然正确，
自动事实仍限制在原会话，没有自动晋升为跨会话事实。

`AddTriple` 默认允许同一关系多个值并存，不能根据不同 object 自动宣布旧事实
失效。手工记忆的定点纠正使用下面的 `correct` 操作。

摘要在完整响应后按实际 session 入持久队列；各 session 拥有独立进度和哈希文件路径。
失败、空结果、取消或关闭不会推进成功水位，摘要采用原子写入，并在成功后写入数据库索引。
新摘要按问题召回，旧文件保留但不批量导入。恢复依赖后续允许的回合；具体规则见
[统一记忆服务](memory-service.md#持久后台处理)。

工具偏好纠正使用当前用户的租户分区；“这次不要用”之类一次性要求不再自动
写为长期偏好。只有带“以后”“默认”等长期意图的纠正进入画像。

存储键使用 JSON 编码的 `[scope, session_id, key]`；持久范围的 `session_id` 为空。
同名 key 在不同 scope、不同 session 中独立保存和维护版本。

### Tool 行为

- `remember` 默认使用 `session`；没有活动会话则返回错误，可显式选择 `user` 或
  `project` 保存长期信息。priority 限制在 1–10。
- `recall` 不指定 scope 时依次查当前 session、project、user；指定 scope 时仅查询
  该范围。
- `list` 和 `snapshot` 仅返回当前 workspace 的持久记忆及当前会话记忆。
- `forget` 必须显式指定 scope，只删除指定范围的 key。session 删除仅针对当前
  会话，不根据模型参数选择另一会话。
- `correct` 必须指定 key、value、scope 和 `expected_version`。先通过 `recall`
  或 `list` 取得当前版本；版本变化后拒绝旧纠正，避免覆盖另一回合的新修改。
- `recall` / `list` 返回版本与 `source_session_id`，它记录工具调用来自哪个会话，
  不表示内容已获人工确认，也不等于逐条原文证据。
- 会话 ID 从工具执行 context 的 `session_id` 读取；构造器中的会话 ID 仅用于
  context 没有该字段的独立调用。context 显式提供空 ID 时不得回退。
- 保存、删除失败返回 Go error，不输出成功回执，并恢复修改前的内存状态。

旧的 `Recall`、`List`、`Snapshot` 方法仅读取持久范围；运行时应使用对应的
`*ForSession` 方法。兼容 `Forget` 只删除唯一的持久同名 key；存在多个范围时
返回歧义错误。`Remember`、`Forget`、`Merge` 的调用者应检查返回的持久化错误。

### 迁移与可靠性

以下 JSON 锁、Merge 与原子文件规则描述兼容 AgentMemoryStore。生产 Agent 的长期读写已切换
数据库 Service；一次性导入、事务版本纠正与持久任务见 [memory-service.md](memory-service.md)。

加载旧 JSON 时根据记录里的 scope、session_id、key 恢复地址，不删除已有数据。
旧 session 记录若没有会话 ID，保留为隔离记录，不进入 Tool 召回；无法推断其
归属时不能自动晋升为全局记忆。文件损坏或不可读时，Tool 返回错误，拒绝用
空数据覆盖原文件；修复文件后需要重启进程重新加载。

同一进程内、同一规范化路径的多个 store 共享锁和状态，避免多个 Agent 实例
覆盖彼此写入。文件通过 `internal/fsutil.WriteAtomic` 写入临时文件、sync 后替换，
使用现有 Windows rename 重试逻辑。返回列表使用副本，调用者不能修改底层记录。

手工 `forget` 清空 value、tags 和来源内容，并保存递增版本的 `deleted` 标记。
该标记不参与 recall/list/snapshot；重新加载或 Merge 更高版本的旧副本不会复活
它。只有一次新的显式 remember 可以恢复该 key。纠正和删除持久化失败时回滚
内存状态。删除范围仍是当前手工记忆记录，不会同时清空 KG、画像、摘要或聊天
历史；本版不提供跨这些独立存储的“一键完全遗忘”。

当前不提供多进程同时写同一文件或运行中重新加载外部编辑的协调协议。需要
多个 MACS 进程共同使用记忆时，应改用事务存储和进程间协调，不能把进程内锁
当作跨进程保证。

## 后续个人版改造

个人版目标架构、数据合同、分阶段实施与验收见
[个人版记忆系统设计](personal-memory-design.md)。该文档描述长期目标，当前仅
落实了本文所列的简化功能，不能用完整提案描述运行时已具备的能力。

已有统一入口与持久提取任务。后续需要完善长期晋升、跨存储语义血缘与遗忘，
失败重试、持久向量索引与来源追溯。个人版界面应支持查看来源、编辑和删除；
统一删除需要覆盖派生事实、召回索引与相关缓存，而不只是删除手工 JSON 中
的一条记录。

新增记忆策略必须继续尊重当前请求、workspace 和身份边界。模型推测不能直接
成为已经确认的用户事实；一次性任务要求不能自动转为长期偏好。

## 验证

```text
go test ./internal/memory -run TestAgentMemory -count=1
go test ./internal/memory -run TestSessionContext -count=1
go test ./internal/memory -run 'TestMemoryTools|TestKnowledgeFacts|TestDeriver|TestMemoryCorrections|TestSessionMemoryProgress|TestSessionMemoryCancellation' -count=1
go test ./internal/app -run 'TestMemoryDisabled|TestKnowledgeMemory' -count=1
go build -buildvcs=false -trimpath ./cmd/macs
go run ./cmd/macs --version
```

全量测试基线中的 `internal/session` 测试导入环和 memory 会话摘要产物断言失败，
与手工记忆的聚焦验证分别报告，不能以构建通过替代完整测试通过。
