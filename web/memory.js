(() => {
  "use strict";
  const COPY = {
    zh: {
      title:"记忆",back:"‹ 工作台",short:"短期记忆",long:"长期记忆",preferences:"用户偏好",project:"项目记忆",global:"全局记忆",knowledge:"知识库",history:"历史任务",
      shortDescription:"当前任务的工作记录与摘要。手动记录可修改，自动摘要与上下文只读。",longDescription:"当前 Agent 保存的个人与项目记忆，按问题选作参考。",preferencesDescription:"你保存的表达、格式与办事偏好；待确认内容需先核对，最多 5 条个人偏好可常驻。",projectDescription:"当前项目的事实、决定与经验，供这个工作区的当前 Agent 参考。",globalDescription:"当前工作区所有 Agent 共享的记忆，不跨工作区；只在问题相关时参考。",knowledgeDescription:"你维护的资料与操作手册。通过原知识库管理入口上传、查看或更新文档。",historyDescription:"当前 Agent 最近的真实任务记录。历史只读，可另存经过核对的经验。",
      localNote:"本机管理 · 真实记录",new:"新增记忆",search:"搜索",searchPlaceholder:"搜索标题或内容",agent:"Agent",session:"任务",scope:"范围",kind:"类型",state:"状态",allScopes:"全部范围",allStates:"全部状态",personal:"个人",active:"已生效",candidate:"待确认",refresh:"刷新",clearFilters:"清除筛选",previous:"上一页",next:"下一页",
      how:"记忆如何使用",howBody:"短期记录仅用于选定任务；长期记忆按问题选作参考。待确认内容不会自动用于对话。常驻也受上下文容量限制。遗忘记忆不会删除原始聊天记录，知识库与历史任务保留各自来源。",export:"导出长期记忆",recalls:"高级：查看记忆参考记录",recallHint:"最近 7 天的 20 次记录。选入参考不代表模型实际采用；记录不包含问题或记忆正文。",
      name:"标题",content:"内容",preference:"偏好",fact:"事实",decision:"决定",experience:"经验",cancel:"取消",save:"保存记忆",close:"关闭",categories:"记忆分类",recordList:"记录列表",recordDetail:"记录详情",theme:"切换主题",pinned:"常驻",readonly:"只读",correct:"更正内容",confirm:"确认生效",pin:"设为常驻",unpin:"取消常驻",forget:"遗忘",forgetConfirm:"确认遗忘这条记忆？原始聊天记录仍会保留。",confirmForget:"确认遗忘",
      created:"创建时间",updated:"修改时间",unknownTime:"未记录",source:"来源",version:"版本",reference:"引用",quote:"原文",confirmedRef:"确认引用",confirmedQuote:"确认原文",sourceVersion:"来源与版本",sourceExplicit:"你明确保存",sourceAgent:"Agent 记录",sourceLegacy:"历史导入",sourceCandidate:"从对话提取",sourceSession:"手动会话记录",sourceContext:"自动会话上下文",sourceSummary:"自动会话摘要",sourceHistory:"原始任务记录",sourceKnowledge:"已有知识库管理",
      loading:"正在加载…",count:"共 {count} 条",page:"第 {page} / {pages} 页",empty:"暂无记录",emptyDescription:"此分类还没有记录。你可以新增记忆，或在对话中明确告诉 Agent 需要记住什么。",noMatch:"没有匹配的记录",noMatchBody:"换个关键词，或清除筛选查看全部。",noSession:"还没有可用的任务",noSessionBody:"先与这个 Agent 开始一次对话，之后可查看这次任务的短期记录。",noShort:"此任务暂无短期记录",noShortBody:"Agent 尚未保存工作状态或生成摘要。可手动新增一条只用于此任务的记录。",noHistory:"还没有历史任务",noHistoryBody:"与当前 Agent 开始对话后，真实任务会出现在这里。",choose:"选择左边的记录查看详情。",readFailure:"暂时无法读取记录",retry:"重试",failed:"读取失败，请重试。",
      readOnlyNote:"这条内容由系统自动记录，仅供查看。需要保留自己的结论，可另存为项目经验。",saveExperience:"另存经验",experienceTitle:"任务经验：",editTitle:"更正记忆",saveCorrection:"保存更正",newNote:"保存后立即生效，同一范围内的标题不能重复。",editNote:"只更正内容；标题、类型与范围保持原记录。",candidateNote:"更正后仍待确认，可核对后确认生效。",pinnedNote:"更正后立即生效；内容变化后可重新设为常驻。",sessionNote:"只用于选定任务，不会自动成为长期或全局记忆。",globalNote:"保存后当前工作区所有 Agent 都可按问题选作参考。",experienceNote:"请核对内容再保存。这会新增项目经验，不会修改原始记录。",required:"请填写标题和内容。",conflict:"这条记忆已发生变化。编辑内容已保留，请核对最新记录后再重新打开编辑。",requestFailed:"请求失败，请重试。",saved:"记忆已保存并生效",corrected:"已更正记忆",confirmed:"记忆已生效",pinnedDone:"已设为常驻",unpinnedDone:"已取消常驻",forgotten:"已遗忘这条记忆",
      manageKnowledge:"打开知识库管理",knowledgeTitle:"资料与操作手册",knowledgeBody:"知识库保存你上传的文档和整理的知识。文档需要通过原管理页面预览、替换上传或删除；不会因为放进知识库就强制 AI 按步骤执行。",knowledgeBoundary:"这里保留知识库管理入口，不把不同身份的资料合并到个人记忆列表。",openTask:"打开原任务",historyBody:"原始任务消息只读。可在工作台继续对话；另存经验需要你核对后明确保存。",historyMessages:"任务消息",noMessages:"暂无可读取的消息",user:"你",assistant:"AI",tool:"工具",messagesLimit:"显示此次返回的最近 {count} 条消息。",historyReadFailure:"消息暂时无法读取，可打开原任务查看。",
      noRecall:"暂无参考记录",noRecallBody:"开始对话后，可在这里了解记忆的参考情况。",selected:"选入",excluded:"排除",tokens:"估算 token",recallReady:"已生成",recallRedacted:"已脱敏",recallEmpty:"无可用内容",recallSelected:"选入 {count} 条",redacted:"以下为脱敏前的选择记录。",omitted:"另有 {count} 条明细未记录。",uncategorized:"未分类",supplemental:"补充来源",historyProfile:"历史与画像",graph:"知识图谱",summary:"会话摘要",context:"会话状态",keyword:"关键词召回",semanticReady:"语义召回已就绪",semanticPartial:"语义召回部分可用",semanticUnavailable:"语义暂不可用，使用关键词召回",
      reasonRelevant:"关键词相关",reasonSemantic:"语义相关",reasonHybrid:"关键词与语义相关",reasonPinned:"常驻偏好",reasonSession:"当前任务工作状态",reasonNotRelevant:"与当前问题无关",reasonOverride:"被更具体范围的同名记忆覆盖",reasonRetracted:"已更正或遗忘的旧内容",reasonDuplicate:"重复内容",reasonBudget:"超出上下文预算",reasonPinnedBudget:"超出常驻偏好预算",reasonSafety:"安全扫描未通过"
    },
    en: {
      title:"Memory",back:"‹ Workspace",short:"Short-term memory",long:"Long-term memory",preferences:"User preferences",project:"Project memory",global:"Global memory",knowledge:"Knowledge base",history:"Task history",
      shortDescription:"Working notes and summaries for the selected task. Manual notes can be edited; automatic summaries and context are read-only.",longDescription:"Personal and project memories saved by this Agent, selected when relevant to a question.",preferencesDescription:"Saved preferences for wording, formatting and work. Review unconfirmed content first; up to 5 personal preferences can be pinned.",projectDescription:"Facts, decisions and experience for the current Agent in this project workspace.",globalDescription:"Shared by all Agents in this workspace, never across workspaces. Referenced when relevant to a question.",knowledgeDescription:"Your reference materials and procedures. Use the existing knowledge manager to upload, read or update documents.",historyDescription:"Recent real tasks for the current Agent. History is read-only; reviewed lessons can be saved separately.",
      localNote:"Local management · Real records",new:"Add memory",search:"Search",searchPlaceholder:"Search titles or content",agent:"Agent",session:"Task",scope:"Scope",kind:"Type",state:"Status",allScopes:"All scopes",allStates:"All statuses",personal:"Personal",active:"Active",candidate:"Awaiting confirmation",refresh:"Refresh",clearFilters:"Clear filters",previous:"Previous",next:"Next",
      how:"How memory works",howBody:"Short-term notes belong to the selected task. Long-term memories are selected when relevant. Unconfirmed content is not automatically used. Pinned preferences still have a context budget. Forgetting a memory does not delete original chat records; knowledge and task history keep their own sources.",export:"Export long-term memory",recalls:"Advanced: memory reference records",recallHint:"The latest 20 records from 7 days. Selection does not prove the model used the memory. Records contain no question or memory body.",
      name:"Title",content:"Content",preference:"Preference",fact:"Fact",decision:"Decision",experience:"Experience",cancel:"Cancel",save:"Save memory",close:"Close",categories:"Memory categories",recordList:"Record list",recordDetail:"Record details",theme:"Switch theme",pinned:"Pinned",readonly:"Read-only",correct:"Edit content",confirm:"Confirm",pin:"Pin preference",unpin:"Unpin",forget:"Forget",forgetConfirm:"Forget this memory? Original chat records will be retained.",confirmForget:"Confirm forgetting",
      created:"Created",updated:"Updated",unknownTime:"Not recorded",source:"Source",version:"Version",reference:"Reference",quote:"Original quote",confirmedRef:"Confirmation reference",confirmedQuote:"Confirmation quote",sourceVersion:"Source and version",sourceExplicit:"Explicitly saved by you",sourceAgent:"Agent record",sourceLegacy:"Legacy import",sourceCandidate:"Extracted from conversation",sourceSession:"Manual task note",sourceContext:"Automatic task context",sourceSummary:"Automatic task summary",sourceHistory:"Original task record",sourceKnowledge:"Existing knowledge manager",
      loading:"Loading…",count:"{count} records",page:"Page {page} / {pages}",empty:"No records yet",emptyDescription:"There are no records in this category. Add a memory or explicitly tell the Agent what to remember.",noMatch:"No matching records",noMatchBody:"Try another keyword or clear filters to see all records.",noSession:"No task is available",noSessionBody:"Start a conversation with this Agent, then view its short-term notes here.",noShort:"No short-term notes for this task",noShortBody:"The Agent has not saved working context or a summary. You can add a note used only for this task.",noHistory:"No task history yet",noHistoryBody:"Real tasks appear here after you start conversations with this Agent.",choose:"Select a record on the left to view its details.",readFailure:"Records could not be loaded",retry:"Retry",failed:"Loading failed. Please retry.",
      readOnlyNote:"This content was recorded automatically and is read-only. Save your reviewed conclusions separately as project experience.",saveExperience:"Save as experience",experienceTitle:"Task lesson: ",editTitle:"Edit memory",saveCorrection:"Save correction",newNote:"Saved memories become active immediately. Titles must be unique within the scope.",editNote:"Only content is edited; the original title, type and scope are retained.",candidateNote:"The correction still awaits confirmation. Review it before confirming.",pinnedNote:"Corrections take effect immediately. Changed content can be pinned again.",sessionNote:"Used only for the selected task; it does not automatically become long-term or global memory.",globalNote:"All Agents in this workspace can reference it when relevant after saving.",experienceNote:"Review before saving. This adds project experience without changing the original record.",required:"Enter a title and content.",conflict:"This memory has changed. Your edits are retained. Review the latest record, then reopen the editor.",requestFailed:"The request failed. Please retry.",saved:"Memory saved and active",corrected:"Memory corrected",confirmed:"Memory confirmed",pinnedDone:"Preference pinned",unpinnedDone:"Preference unpinned",forgotten:"Memory forgotten",
      manageKnowledge:"Open knowledge manager",knowledgeTitle:"Reference materials and procedures",knowledgeBody:"The knowledge base holds uploaded documents and curated knowledge. Use its existing manager to preview, replace or delete documents. Adding a document does not force AI to follow its steps.",knowledgeBoundary:"This page links to the knowledge manager without merging materials from different identities into personal memory.",openTask:"Open original task",historyBody:"Original task messages are read-only. Continue in the workspace or explicitly save a reviewed lesson as experience.",historyMessages:"Task messages",noMessages:"No readable messages",user:"You",assistant:"AI",tool:"Tool",messagesLimit:"Showing the latest {count} messages returned for this task.",historyReadFailure:"Messages could not be loaded. Open the original task to view them.",
      noRecall:"No reference records",noRecallBody:"Start a conversation to see how memories were selected.",selected:"Selected",excluded:"Excluded",tokens:"estimated tokens",recallReady:"Generated",recallRedacted:"Redacted",recallEmpty:"No usable content",recallSelected:"{count} selected",redacted:"The selection records below were captured before redaction.",omitted:"{count} additional decisions were not recorded.",uncategorized:"Uncategorized",supplemental:"Supplemental source",historyProfile:"History and profile",graph:"Knowledge graph",summary:"Task summary",context:"Task context",keyword:"Keyword recall",semanticReady:"Semantic recall ready",semanticPartial:"Semantic recall partly available",semanticUnavailable:"Semantic recall unavailable; using keywords",
      reasonRelevant:"Keyword relevance",reasonSemantic:"Semantic relevance",reasonHybrid:"Keyword and semantic relevance",reasonPinned:"Pinned preference",reasonSession:"Current task working context",reasonNotRelevant:"Not relevant to this question",reasonOverride:"Overridden by a more specific scope",reasonRetracted:"Corrected or forgotten old content",reasonDuplicate:"Duplicate",reasonBudget:"Context budget exceeded",reasonPinnedBudget:"Pinned preference budget exceeded",reasonSafety:"Safety scan did not pass"
    }
  };
	Object.assign(COPY.zh,{reviewWhen:"什么时候使用",reviewEvidence:"根据哪些原话或结果",reviewUser:"你的原话",reviewTool:"工具实际结果",reviewTask:"查看原任务"});
	Object.assign(COPY.en,{reviewWhen:"When to use",reviewEvidence:"Supporting statements and results",reviewUser:"Your statement",reviewTool:"Actual tool result",reviewTask:"Open original task"});
  COPY.zh.tooLong="标题最多 256 字、内容最多 8000 字。请先精简，再保存。";
  COPY.en.tooLong="Use up to 256 characters for the title and 8000 for the content. Shorten them before saving.";
  Object.assign(COPY.zh,{recallPaused:"已停止自动参考",recallPause:"停止自动参考",recallResume:"恢复自动参考",recallPauseDone:"已停止自动参考，原记录保留。",recallResumeDone:"已恢复，后续将按问题相关性选用。",recallControlHelp:"这个开关控制后续 Agent 对话的自动参考；原对话和工具按需查询仍可读取。",reasonRecallDisabled:"已由你停止自动参考"});
  Object.assign(COPY.en,{recallPaused:"Automatic recall paused",recallPause:"Pause automatic recall",recallResume:"Resume automatic recall",recallPauseDone:"Automatic recall paused. The original record is retained.",recallResumeDone:"Resumed. Future Agent conversations select it when relevant.",recallControlHelp:"This controls automatic recall in future Agent conversations. Original chats and explicit tool reads remain available.",reasonRecallDisabled:"Automatic recall paused by you"});
  Object.assign(COPY.zh, {
    catalogNote_legacy_context_archive:"这里还保留工作目录中的旧偏好档案，个人归属未核实；浏览不会使临时记录进入自动回想。",
    viewNote_legacy_preferences_unavailable:"部分工作目录历史偏好暂时无法读取；已保存偏好与其他来源仍可查看。",
    sourceLegacyPreference:"历史偏好 · 待核实",legacyPreferenceGroup:"工作目录历史偏好 · 待核实",workspaceArchive:"工作目录历史档案",sourceContextName:"来源范围",
    legacyPreferenceReadOnly:"这是工作目录旧文件中的偏好记录，个人与 Agent 归属尚未核实，不是已确认的个人偏好，也不会因浏览而启用回想。核对后可明确另存为当前 Agent 的偏好；旧文件不会被修改。",
    emptyLong:"当前 Agent 还没有已保存的长期条目。任务对话和工具过程会保留在历史任务；完成一次任务不等于自动保存了长期记忆。",
    emptyPreferences:"当前 Agent 还没有已保存的用户偏好。可以明确告诉它你的表达或格式偏好，或手动保存；任务里的评价不会自动变成偏好。",
    emptyProject:"这个项目中，当前 Agent 还没有已保存的事实、决定或经验。可先查看真实任务过程，再核对并另存经验。",
    emptyGlobal:"这个工作区还没有共享条目。各 Agent 的个人记录不会自动复制到这里；新增全局记忆后才能共享参考。",
    knownTasks:"当前 Agent 有 {count} 个可查看的任务。", viewRecentTasks:"查看历史任务", viewShortNotes:"查看短期记录", chooseAgentHint:"记忆按 Agent 区分。若任务由另一位 Agent 完成，可在上方切换 Agent 查看。",
    emptyDetail:"此分类暂无可展开的条目。你可以查看任务来源或新增一条经过核对的记忆。"
  });
  Object.assign(COPY.en, {
    catalogNote_legacy_context_archive:"Legacy preference archives remain in this workspace. Personal ownership is unverified; viewing them does not add temporary records to automatic recall.",
    viewNote_legacy_preferences_unavailable:"Some legacy workspace preferences could not be read. Saved preferences and other sources remain available.",
    sourceLegacyPreference:"Historical preference · Needs review",legacyPreferenceGroup:"Historical workspace preferences · Needs review",workspaceArchive:"Historical workspace archive",sourceContextName:"Source scope",
    legacyPreferenceReadOnly:"This preference is from a legacy workspace file. Personal and Agent ownership are unverified; it is not a confirmed personal preference and viewing it does not enable recall. Review before explicitly saving it for the current Agent; the original file stays unchanged.",
    emptyLong:"This Agent has no saved long-term entries yet. Conversations and tool activity belong to task history; completing a task does not automatically create a long-term memory.",
    emptyPreferences:"This Agent has no saved user preferences yet. State your wording or formatting preferences explicitly, or save one yourself. Task feedback does not automatically become a preference.",
    emptyProject:"This Agent has no saved facts, decisions or experience in this project yet. Review a real task first, then save a verified lesson.",
    emptyGlobal:"This workspace has no shared entries yet. Individual Agent records are not copied here automatically; add a global memory to share it.",
    knownTasks:"This Agent has {count} tasks available to review.", viewRecentTasks:"View task history", viewShortNotes:"View short-term notes", chooseAgentHint:"Memories are separated by Agent. If another Agent completed the task, select that Agent above.",
    emptyDetail:"There is no entry to expand in this category. Review the original task or add a memory you have verified."
  });
  Object.assign(COPY.zh, {
    sourceExecution:"任务完成记录",sourceInsight:"任务经验原文",sourceFeedback:"用户反馈原文",sourceStatement:"用户偏好原话",sourceKnowledgeFact:"已有知识事实",sourceConversation:"真实问答摘录",
    shortDescription:"当前任务的工作记录、已有摘要和执行来源。手动记录可修改，自动记录与原话只读。",longDescription:"当前 Agent 的已保存记忆，以及真实任务和已有知识中的参考来源。",preferencesDescription:"已保存的表达、格式偏好与用户原话来源。原话摘录核对后可保存为偏好；待确认条目需先核对。最多 5 条个人偏好可常驻。",
    sourceRecord:"来源记录",savedSources:"已保存 {count} 条 · 本次来源 {sources} 条",sourceBoundary:"来源记录是已有任务、原话或知识中的近期摘录，不代表已经保存为长期条目。需要核对后再另存；不会在这里额外调用模型。",
    toolsRecorded:"已记录的工具调用",toolSuccess:"成功",toolFailed:"失败",toolUnknown:"结果未记录",duration:"耗时",feedbackOriginal:"原始用户反馈",rating:"原始评分",userEvidence:"原始用户说明",sourceID:"来源标识",taskStatus:"执行状态",taskSuccess:"成功",taskFailed:"失败",taskPending:"未完成",
    historySources:"任务执行、工具与反馈",sourcesUnavailable:"任务来源暂时无法读取，原始对话仍可查看。",noTaskSources:"此任务暂无可读取的执行或反馈来源。原始对话在下面。",savePreference:"保存为偏好",sourcePreferenceNote:"这是偏好原话的摘录。核对后保存为个人偏好才成为可更正的长期条目。",
    reviewLesson:"请结合这个任务填写：哪些做法有用、遇到什么问题、用户给了什么反馈、下次应怎样处理。仅保存你已经核对的事实。",system:"系统记录"
  });
  Object.assign(COPY.en, {
    sourceExecution:"Task completion record",sourceInsight:"Recorded task lesson",sourceFeedback:"Original user feedback",sourceStatement:"Original preference statement",sourceKnowledgeFact:"Existing knowledge fact",sourceConversation:"Original conversation excerpt",
    shortDescription:"Working notes, available summaries and execution sources for the selected task. Manual notes can be edited; automatic records and original statements are read-only.",longDescription:"This Agent’s saved memories and reference sources from real tasks and existing knowledge.",preferencesDescription:"Saved wording and formatting preferences plus original user statements. Review excerpts before saving them as preferences, and review unconfirmed entries. Up to 5 personal preferences can be pinned.",
    sourceRecord:"Source record",savedSources:"{count} saved · {sources} sources in this response",sourceBoundary:"Source records are recent excerpts from tasks, original statements or existing knowledge, not saved long-term entries. Review before saving separately; this page does not call a model to extract them.",
    toolsRecorded:"Recorded tool calls",toolSuccess:"Succeeded",toolFailed:"Failed",toolUnknown:"Result not recorded",duration:"Duration",feedbackOriginal:"Original user feedback",rating:"Original rating",userEvidence:"Original user statement",sourceID:"Source ID",taskStatus:"Execution status",taskSuccess:"Succeeded",taskFailed:"Failed",taskPending:"Incomplete",
    historySources:"Task execution, tools and feedback",sourcesUnavailable:"Task sources could not be loaded. Original messages are still available.",noTaskSources:"No execution or feedback sources are available for this task. Original messages appear below.",savePreference:"Save as preference",sourcePreferenceNote:"This is an excerpt of an original preference statement. Review and save it as a personal preference to create an editable long-term entry.",
    reviewLesson:"Review this task: what worked, what failed, what feedback the user gave, and what to do next time. Save only facts you have verified.",system:"System record"
  });
  Object.assign(COPY.zh, {
    recollectionTitle:"记忆来源与回想",recollectionFlow:"保存历史 → 找回相关 → 提供本轮参考",recollectionIntro:"记忆是过去积累的资料；遇到相关问题时，系统会选取一部分作为回答参考。",catalogTitle:"查看资料来源",catalogBoundary:"资料已保存不等于每次都会使用；当前问题、归属、相关性和上下文长度都会影响选择。",catalogContext:"当前工作区 · 当前 Agent：{agent} · 可核对的近期任务 {count} 个",catalogSummary:"{count} 种来源",catalogFailure:"来源目录暂时无法读取；当前记录仍可正常查看。",catalogNotLoaded:"展开后查看已有资料的位置和使用方式。",catalogNoSources:"暂未返回来源目录。",catalogPositions:"保存在哪里",catalogDatabase:"应用数据库",catalogFiles:"工作区文件",catalogTables:"数据表",catalogPaths:"相对工作区路径",catalogLocationUnknown:"位置暂未提供",catalogVerified:"当前可核对：{count} {unit}",catalogUnverified:"当前范围待核对：{count} {unit}",catalogCountUnknown:"此来源暂未提供可核对数量",catalogBounded:"有界查询，非全库总数",unitRecords:"条",unitCalls:"次调用",unitItems:"项",unitFiles:"个文件",catalogReady:"来源已接入",catalogDisabled:"当前未开启",catalogUnverifiedStatus:"归属待核对",catalogUnavailable:"暂不可读取",catalogAutomatic:"按问题自动回想",catalogAutomaticOff:"自动回想未开启",catalogAutomaticCheck:"自动回想需运行时确认",catalogOnDemand:"通过工具按需查询",catalogOnDemandOff:"按需查询未开启",catalogOnDemandCheck:"按需查询需运行时确认",catalogNotConnected:"尚未接入回想",catalogTools:"查询工具",catalogRuntimeCheck:"使用时还会检查当前 Agent 的能力与开关。",stepSave:"保存",stepSaveBody:"对话、工具过程、偏好和知识保留在各自原有位置。",stepRecall:"回想",stepRecallBody:"从允许读取的资料中找出与当前问题有关的内容。",stepPrompt:"提供参考",stepPromptBody:"通过归属与安全检查后，一部分内容进入本轮提示词；不是整库塞进去。",
    catalogEvents:"任务与对话历史",catalogEventsBody:"已保存的问答、工具过程、反馈，以及已有任务经验。",catalogProfiles:"旧用户画像与格式偏好",catalogProfilesBody:"表达方式、约束和文件格式偏好。默认值与自动推断不等于你确认过的偏好。",catalogKnowledge:"Agent 学习知识",catalogKnowledgeBody:"从任务积累的做法、经验和反馈知识，与上传文档不同。",catalogToolUsage:"工具与 Skill 使用",catalogToolUsageBody:"使用次数、结果与耗时，是使用记录，不自动成为办事规则。",catalogEntries:"已整理的记忆条目",catalogEntriesBody:"明确保存的偏好、事实、决定和经验；这只是全部记忆中的一部分。",catalogSummaries:"任务摘要",catalogSummariesBody:"已有对话的精简记录，供相关问题回想时参考。",catalogContextSource:"任务工作记录",catalogContextSourceBody:"当前任务的事实、决定、待办等上下文；常驻记录沿用原迁移规则。",catalogGraph:"知识关系与原文",catalogGraphBody:"已有事实关系、引用原文和来源；缺少原文依据的旧记录不能当作确认事实。",catalogLearnFiles:"旧记忆与学习文件",catalogLearnFilesBody:"工作区里原有的记忆 JSON、摘要和学习记录，不另建一份资料库。",catalogRag:"上传的资料与知识库",catalogRagBody:"文档、操作手册、问答和检索分块，通过知识工具按需查找。",
    sourceLearningKnowledge:"Agent 学习知识",knowledgeLearning:"Agent 学习知识",knowledgeDocuments:"上传资料",knowledgeLearningEmpty:"此范围暂没有可核对的 Agent 学习知识。旧库中无法关联原任务的资料不会自动显示在这里。",knowledgeCount:"本次学习知识 {count} 条 · 上传资料独立管理",knowledgeDescription:"任务积累的学习知识与上传资料各有来源；学习知识只读，文档沿用原管理入口。",savedGroup:"已整理记忆",taskSourceGroup:"真实任务与工作记录",preferenceSourceGroup:"明确偏好原话",learningGroup:"任务积累的学习知识",saveMemory:"整理为记忆",learningReadOnly:"这是原有学习知识的摘录。核对后可另存为记忆，原知识记录不会被修改。"
  });
  Object.assign(COPY.en, {
    recollectionTitle:"Memory sources and recall",recollectionFlow:"Save history → Recall relevant content → Supply context",recollectionIntro:"Memory is information accumulated over time. Relevant parts can be selected as context for a later answer.",catalogTitle:"View source directory",catalogBoundary:"Saving information does not mean it is used every time. The current question, ownership, relevance and context budget affect selection.",catalogContext:"Current workspace · Agent: {agent} · {count} recent tasks can be verified",catalogSummary:"{count} source types",catalogFailure:"The source directory could not be loaded. Existing records remain available.",catalogNotLoaded:"Expand to see where information is stored and how it can be used.",catalogNoSources:"No source directory was returned.",catalogPositions:"Storage locations",catalogDatabase:"Application database",catalogFiles:"Workspace files",catalogTables:"Tables",catalogPaths:"Paths relative to the workspace",catalogLocationUnknown:"No location was provided",catalogVerified:"Verifiable here: {count} {unit}",catalogUnverified:"Needs review in this scope: {count} {unit}",catalogCountUnknown:"No verifiable count is available for this source",catalogBounded:"Bounded query, not a database total",unitRecords:"records",unitCalls:"calls",unitItems:"items",unitFiles:"files",catalogReady:"Source connected",catalogDisabled:"Currently disabled",catalogUnverifiedStatus:"Ownership needs review",catalogUnavailable:"Currently unavailable",catalogAutomatic:"Automatic recall when relevant",catalogAutomaticOff:"Automatic recall is disabled",catalogAutomaticCheck:"Automatic recall needs a runtime check",catalogOnDemand:"Queried through tools when needed",catalogOnDemandOff:"On-demand querying is disabled",catalogOnDemandCheck:"On-demand querying needs a runtime check",catalogNotConnected:"Not connected to recall",catalogTools:"Query tools",catalogRuntimeCheck:"The Agent’s current capabilities and settings are also checked when used.",stepSave:"Save",stepSaveBody:"Conversations, tool activity, preferences and knowledge keep their original storage.",stepRecall:"Recall",stepRecallBody:"Find content relevant to the current question within the permitted scope.",stepPrompt:"Supply context",stepPromptBody:"Ownership and safety checks select parts for this turn’s prompt, rather than including the entire database.",
    catalogEvents:"Tasks and conversations",catalogEventsBody:"Saved questions, answers, tool activity, feedback and existing task lessons.",catalogProfiles:"Legacy profile and formatting preferences",catalogProfilesBody:"Response style, constraints and file formats. Defaults and inferred traits are not confirmed preferences.",catalogKnowledge:"Agent learning knowledge",catalogKnowledgeBody:"Methods, experience and feedback accumulated from tasks, separate from uploaded documents.",catalogToolUsage:"Tool and Skill usage",catalogToolUsageBody:"Counts, outcomes and duration record past usage; they do not automatically become work rules.",catalogEntries:"Curated memory entries",catalogEntriesBody:"Explicitly saved preferences, facts, decisions and experience are one part of all memory.",catalogSummaries:"Task summaries",catalogSummariesBody:"Existing concise conversation records, referenced when relevant to a question.",catalogContextSource:"Task working context",catalogContextSourceBody:"Task facts, decisions and to-dos. Durable records retain their existing migration rules.",catalogGraph:"Knowledge relationships and quotations",catalogGraphBody:"Existing facts, quotations and sources. Legacy records without original evidence are not confirmed facts.",catalogLearnFiles:"Legacy memory and learning files",catalogLearnFilesBody:"Existing workspace memory JSON, summaries and learning records, without creating a second store.",catalogRag:"Uploaded reference materials",catalogRagBody:"Documents, procedures, FAQs and retrieval chunks are queried through knowledge tools.",
    sourceLearningKnowledge:"Agent learning knowledge",knowledgeLearning:"Agent learning knowledge",knowledgeDocuments:"Uploaded materials",knowledgeLearningEmpty:"No verifiable Agent learning knowledge is available here. Legacy records that cannot be linked to an original task are not merged into this scope.",knowledgeCount:"{count} learning sources in this response · Uploaded materials managed separately",knowledgeDescription:"Task learning and uploaded materials retain their own sources. Learning records are read-only; documents use their existing manager.",savedGroup:"Curated memories",taskSourceGroup:"Real tasks and working records",preferenceSourceGroup:"Explicit preference statements",learningGroup:"Knowledge accumulated from tasks",saveMemory:"Save reviewed memory",learningReadOnly:"This is an excerpt of existing learning knowledge. Review and save it separately as memory; its original record is unchanged."
  });
  Object.assign(COPY.zh, {
    catalogRules:"读取规则",catalogNote_verified_web_sessions:"仅包含当前工作区经过核实的 Web 任务。",catalogNote_bounded_recent_scope:"核对最近最多 200 个合法会话，不是数据库全部历史。",catalogNote_incomplete_ownership:"部分旧记录缺少完整归属，未并入当前记忆。",catalogNote_memory_disabled:"当前助手的自动记忆没有开启。",catalogNote_runtime_check_required:"每次使用仍需核对当时的工具、权限和配置。",catalogNote_profile_scope_unverified:"旧画像缺少用户与工作区的完整归属证据，未读取其正文。",catalogNote_profile_defaults_not_preferences:"画像的默认值和推断不能冒充你的明确偏好。",catalogNote_tool_usage_from_verified_events:"这里的个人使用数量来自已核实任务的调用链，不是旧统计表的全部数字。",catalogNote_active_relevance_budget:"已生效内容仍按问题相关性和本轮长度预算选择。",catalogNote_workspace_shared:"包括当前所有者在这个工作区中明确保存的共享条目。",catalogNote_session_only:"会话内容仅在合法所属任务中读取。",catalogNote_summary_threshold:"摘要达到既有条件后才生成；没有摘要不代表对话丢失。",catalogNote_kg_evidence_required:"旧关系和原文缺少证据时不会自动成为确认事实。",catalogNote_learn_files_not_connected:"学习文件没有接入自动回想，需通过文件或知识工具按需查询。",catalogNote_learn_legacy_directory:"学习记录沿用已有兼容目录。",catalogNote_rag_separate_permissions:"上传资料保留知识库原有的权限与管理方式。",catalogNote_storage_unavailable:"此存储暂不可读取，不能据此判断资料数量为零。",catalogNote_custom_sqlite_file:"应用数据库使用工作区外的 SQLite 文件；这里不显示它的绝对路径。",catalogNote_table_missing:"这张数据表尚未建立，其他来源中的资料仍保留。"
  });
  Object.assign(COPY.en, {
    catalogRules:"Read rules",catalogNote_verified_web_sessions:"Only verified Web tasks from this workspace are included.",catalogNote_bounded_recent_scope:"Up to 200 recent valid conversations are checked, rather than all database history.",catalogNote_incomplete_ownership:"Some legacy records lack complete ownership evidence and are not merged here.",catalogNote_memory_disabled:"Automatic memory is disabled for this Agent.",catalogNote_runtime_check_required:"Tools, permissions and settings are checked again when used.",catalogNote_profile_scope_unverified:"The legacy profile lacks complete user and workspace evidence, so its content is not read.",catalogNote_profile_defaults_not_preferences:"Profile defaults and inferred traits are not explicit user preferences.",catalogNote_tool_usage_from_verified_events:"Personal usage counts come from verified task call chains, rather than the entire legacy statistics table.",catalogNote_active_relevance_budget:"Active content is selected by relevance and this turn’s context budget.",catalogNote_workspace_shared:"Includes shared entries explicitly saved by the current owner in this workspace.",catalogNote_session_only:"Conversation content is read only within its valid task scope.",catalogNote_summary_threshold:"Summaries are generated when existing conditions are met. A missing summary does not mean the conversation was lost.",catalogNote_kg_evidence_required:"Legacy relationships and quotations without evidence are not treated as confirmed facts.",catalogNote_learn_files_not_connected:"Learning files are not connected to automatic recall. File or knowledge tools can query them when needed.",catalogNote_learn_legacy_directory:"Learning records retain their existing compatibility directory.",catalogNote_rag_separate_permissions:"Uploaded materials retain the knowledge base’s permissions and management.",catalogNote_storage_unavailable:"This store cannot currently be read; that does not establish a zero record count.",catalogNote_custom_sqlite_file:"The application uses a SQLite file outside the workspace. Its absolute path is not exposed here.",catalogNote_table_missing:"This table has not been created. Information in other sources is retained."
  });
  COPY.zh.catalogUnverified="归属未核实：{count} {unit}";
  COPY.en.catalogUnverified="Ownership unverified: {count} {unit}";
  COPY.zh.catalogNote_incomplete_ownership="这些旧记录缺少来源归属，未并入当前个人内容；数量不代表你的记忆总量。";
  COPY.en.catalogNote_incomplete_ownership="These legacy records lack ownership evidence and are not merged into your personal content. This count is not your memory total.";
  Object.assign(COPY.zh, {
    "shortDescription": "只看选定任务的工作上下文、摘要和手动短期记录；原对话与工具过程在历史任务。",
    "longDescription": "已经沉淀的事实、决定和经验，以及任务留下的经验与学习知识；原始任务日志单独放在历史任务。",
    "preferencesDescription": "你明确说过的表达、格式和办事偏好，以及已保存的偏好条目。默认画像和一次性要求不冒充长期偏好。",
    "projectDescription": "这个项目中已保存的事实、决定和经验，以及能够核实来源的项目知识。",
    "globalDescription": "查看当前目录的共同说明、共享记忆和各 Agent 的已保存记录；汇总查看不会把个人内容自动共享。",
    "knowledgeDescription": "任务学习知识、旧知识图谱与原文、上传资料按真实来源展示。可筛选来源；上传资料在原知识库中管理。",
    "emptyLong": "当前范围没有已沉淀的事实或经验。完整对话与工具过程在历史任务；短期工作状态在短期记忆。",
    "emptyPreferences": "当前范围还没有可核实的明确偏好。原画像默认值不会作为偏好展示；可手动保存你的长期要求。",
    "emptyGlobal": "当前目录没有可读取的共同说明、共享条目或各 Agent 的已保存记录。这里只汇总归属已核实的内容。",
    "emptyKnowledge": "当前筛选下没有可读取的知识。可切换来源或打开上传资料管理；归属未核实的资料不会伪装成个人知识。",
    "knowledgeSource": "知识来源",
    "knowledgeAll": "全部来源",
    "knowledgeGraph": "图谱关系",
    "knowledgeLearning": "任务学习知识",
    "knowledgeRag": "上传资料",
    "knowledgeCount": "当前可见知识 {count} 条",
    "knowledgeCountUnknown": "暂未提供可核实的知识数量",
    "savedSourcesUnknown": "已保存数量待核对 · 本次来源 {sources} 条",
    "countUnknown": "暂未提供可核实的条目数量",
    "sourceAgentName": "来源 Agent",
    "sharedWorkspace": "当前目录共同资料",
    "sourceLocation": "保存位置",
    "sourceWorkspaceInstructions": "目录共同说明",
    "sourceAgentMemory": "Agent 的已保存记忆",
    "sourceProfileConstraint": "已核实的用户格式要求",
    "sourceGraphLegacy": "旧知识图谱关系",
    "sourceGraphFact": "有来源的知识图谱记录",
    "sourceRagKnowledge": "上传资料库",
    "workspaceSourceGroup": "目录共同说明",
    "agentSourceGroup": "各 Agent 的已保存记忆",
    "graphSourceGroup": "图谱与原文知识",
    "ragSourceGroup": "上传资料",
    "workspaceReadOnly": "这是当前目录的 AGENTS.md 共同说明，仅供查看；修改请回到原文件。是否用于任务取决于实际运行配置。",
    "agentMemoryReadOnly": "这是其他位置已保存记忆的只读汇总，不会自动加入当前 Agent。另存前请核对内容；原 Agent 的记录不会被修改。",
    "graphLegacyReadOnly": "这是工作区旧图谱记录，个人归属尚未核实，也不是已确认事实。可阅读参考；需要另存时请先核对来源与内容。",
    "graphFactReadOnly": "这是已有图谱记录，保留其原始来源，不等于已确认的记忆条目。另存前请核对内容。",
    "ragReadOnly": "这里展示资料库的已有信息。文档、原文与管理操作请在上传资料管理中查看。",
    "reviewForAgent": "核对后另存到 {agent}",
    "reviewSourceNote": "核对后由你明确保存为当前 Agent 的记忆；这不会修改或确认原始来源。",
    "shortSourceGroup": "当前任务工作上下文",
    "taskSourceGroup": "已沉淀的经验来源"
});
  Object.assign(COPY.en, {
    "shortDescription": "Working context, summaries and manual notes for the selected task. Original messages and tool activity remain in task history.",
    "longDescription": "Accumulated facts, decisions and experience, including existing task lessons and learned knowledge. Raw task logs remain in task history.",
    "preferencesDescription": "Explicit wording, formatting and work preferences, together with saved preference entries. Profile defaults and one-off requests are not long-term preferences.",
    "projectDescription": "Saved facts, decisions and experience in this project, together with project knowledge whose source can be verified.",
    "globalDescription": "Shared directory instructions, shared memories and saved records from its Agents. Viewing an aggregate does not automatically share personal content.",
    "knowledgeDescription": "Existing task learning, knowledge graphs and quotations, and uploaded materials retain their real sources. Filter by source; uploaded materials use their existing manager.",
    "emptyLong": "There are no accumulated facts or lessons in this scope. Original messages and tool activity remain in task history; working context belongs to short-term memory.",
    "emptyPreferences": "No verifiable explicit preferences are available in this scope. Profile defaults are not shown as preferences. You can save a long-term requirement yourself.",
    "emptyGlobal": "No readable shared instructions, shared entries or saved Agent records are available in this directory. Only verified ownership is aggregated here.",
    "emptyKnowledge": "No readable knowledge matches this filter. Change the source or open the uploaded-materials manager. Unverified material is not presented as personal knowledge.",
    "knowledgeSource": "Knowledge source",
    "knowledgeAll": "All sources",
    "knowledgeGraph": "Graph relationships",
    "knowledgeLearning": "Task learning",
    "knowledgeRag": "Uploaded materials",
    "knowledgeCount": "{count} visible knowledge records",
    "knowledgeCountUnknown": "No verifiable knowledge count is available",
    "savedSourcesUnknown": "Saved count needs review · {sources} sources in this response",
    "countUnknown": "No verifiable entry count is available",
    "sourceAgentName": "Source Agent",
    "sharedWorkspace": "Shared directory material",
    "sourceLocation": "Storage location",
    "sourceWorkspaceInstructions": "Shared directory instructions",
    "sourceAgentMemory": "Saved Agent memory",
    "sourceProfileConstraint": "Verified user formatting requirement",
    "sourceGraphLegacy": "Legacy knowledge graph relationships",
    "sourceGraphFact": "Sourced knowledge graph record",
    "sourceRagKnowledge": "Uploaded knowledge base",
    "workspaceSourceGroup": "Shared directory instructions",
    "agentSourceGroup": "Saved records from Agents",
    "graphSourceGroup": "Graphs and quotations",
    "ragSourceGroup": "Uploaded materials",
    "workspaceReadOnly": "These are this directory's AGENTS.md instructions and are read-only here. Edit the original file to change them. Task use depends on runtime settings.",
    "agentMemoryReadOnly": "This is a read-only aggregate of memory saved elsewhere. It is not automatically added to the current Agent. Review before saving; the original Agent record stays unchanged.",
    "graphLegacyReadOnly": "This is a legacy workspace graph or quotation. Personal ownership is unverified and it is not a confirmed fact. Review its source and content before saving separately.",
    "graphFactReadOnly": "This existing graph record retains its original source. It is not a confirmed memory entry; review its content before saving separately.",
    "ragReadOnly": "This shows existing knowledge-base information. Read and manage documents in the uploaded-materials manager.",
    "reviewForAgent": "Review and save for {agent}",
    "reviewSourceNote": "Review, then explicitly save as memory for the current Agent. This neither modifies nor confirms the original source.",
    "shortSourceGroup": "Current task working context",
    "taskSourceGroup": "Accumulated task lessons"
});
  Object.assign(COPY.zh, {
    catalogNote_workspace_knowledge_archive:"知识库可查看这个工作目录保存的旧知识积累；归属未核实的来源只读，浏览不会确认事实或启用自动回想。",
    catalogNote_graph_quotes_separate:"图谱关系与历史原文分别查看和统计，不能把两种数量相加当成独立记忆数。",
    catalogNote_rag_workspace_material:"上传资料属于工作目录的知识库，不等于当前 Agent 的个人记忆；描述与数量来自已有资料库信息。"
  });
  Object.assign(COPY.en, {
    catalogNote_workspace_knowledge_archive:"The knowledge view can show legacy material stored in this workspace. Unverified sources are read-only; viewing them neither confirms facts nor enables automatic recall.",
    catalogNote_graph_quotes_separate:"Graph relationships and historical quotations are shown and counted separately. Adding their counts does not establish a count of distinct memories.",
    catalogNote_rag_workspace_material:"Uploaded materials belong to the workspace knowledge base rather than this Agent's personal memory. Descriptions and counts come from existing metadata."
  });
  Object.assign(COPY.zh, {
    recollectionTitle:"回想是怎么发生的",recallExplainBrief:"按当前问题选择参考",catalogTitle:"高级：存储来源与位置",
    recollectionIntro:"先检索相关内容，再核对来源和适用范围，合适的部分才作为本轮参考。浏览旧记录不会确认其内容。",
    longDescription:"你保存的记忆和可追溯的经验；旧资料单独待核对。",preferencesDescription:"区分已保存偏好、用户原话和待核对的旧记录。",knowledgeDescription:"先核对记录内容和来源；历史提取记录与原文可单独浏览。",
    savedGroup:"你保存的记忆",confirmedGroup:"你已确认的记忆",pendingGroup:"等待你确认",taskSourceGroup:"可追溯的任务记录",learningGroup:"AI 积累的学习记录",preferenceSourceGroup:"可核对的偏好原话",graphSourceGroup:"历史提取记录",legacyPreferenceGroup:"历史偏好记录",
    archiveTitle:"待核对的历史资料",archiveCount:"本次返回 {count} 条",archiveBrowse:"浏览旧资料",archiveBoundary:"这些旧记录的个人归属或原话证据尚不完整。仅供查阅；不会因浏览而变成已确认记忆。",browseExtractions:"浏览历史提取记录",browseQuotations:"浏览历史原文片段",archiveEmpty:"请明确选择要浏览的历史资料。",emptyTrusted:"暂无可核对的资料",emptyTrustedBody:"可查看下面的历史资料，核对原始内容后再决定是否另存。",
    knowledgeCurated:"可核对资料",knowledgeAll:"所有资料（含历史）",knowledgeGraph:"历史提取记录",knowledgeQuotes:"历史原文片段",knowledgeCount:"当前筛选 {count} 条资料",knowledgeArchiveCount:"当前历史筛选可浏览 {count} 条记录",
    sourceGraphLegacy:"历史提取记录",sourceGraphFact:"有原话可核对的提取记录",sourceGraphQuotation:"历史原文片段",sourceLegacyPreference:"历史偏好记录",
    statusSaved:"你保存的记忆",statusConfirmed:"你已确认",statusCandidate:"等待确认",statusArchive:"历史资料 · 待核对",statusQuoteVerified:"有原话可核对",statusTaskVerified:"有原任务可追溯",statusGenerated:"AI 产生的记录 · 需核对",statusSourceUnknown:"来源证据待核对",statusMetadata:"资料库信息",
    provenanceTitle:"来源与回想",provenanceIdentity:"归属核对",identityVerified:"来源归属已核对",identityArchive:"工作目录历史档案，个人归属未核实",identityUnknown:"归属暂未核实",provenanceContent:"这条是什么",contentSaved:"你手动保存的内容",contentConfirmed:"你明确确认过的内容",contentStatement:"用户原话记录",contentGenerated:"AI 生成或提取的记录，需核对内容",contentHistorical:"未核实的历史记录",contentUnknown:"内容性质暂未核实",
    provenanceQuote:"原话证据",quoteVerified:"有可核对的用户原话",quoteUnverified:"有旧摘录，尚未核实",quoteMissing:"缺少可核对的用户原话",quoteUnknown:"原话证据暂未核实",provenanceTask:"原任务",taskVerified:"有已核对归属的原任务",taskMissing:"暂无可验证的原任务链接",provenanceMissing:"仍缺少的证据",provenanceRecordID:"原记录标识",provenanceRecall:"如何参与回想",recallUnknown:"回想状态暂未核实",recallConditions:"使用条件",recordNotUsedProof:"归属已核对不代表内容准确；可参与回想也不代表本轮已使用。",
    relationSubject:"记录对象",relationPredicate:"提取关系",relationObject:"提取内容",extractedRecord:"历史提取内容",originalSnippet:"历史原文片段",sourceDetails:"时间、原记录与存储位置"
  });
  Object.assign(COPY.en, {
    recollectionTitle:"How recall works",recallExplainBrief:"References selected for the current question",catalogTitle:"Advanced: storage sources and locations",
    recollectionIntro:"Relevant content is retrieved, then its source and scope are checked before parts can supply context. Browsing a legacy record does not confirm it.",
    longDescription:"Saved memories and traceable lessons. Legacy material is reviewed separately.",preferencesDescription:"Saved preferences, original user statements and unverified legacy records are kept separate.",knowledgeDescription:"Review content and its source first. Historical extraction records and quotations can be browsed separately.",
    savedGroup:"Memories you saved",confirmedGroup:"Memories you confirmed",pendingGroup:"Awaiting your confirmation",taskSourceGroup:"Traceable task records",learningGroup:"AI learning records",preferenceSourceGroup:"Verifiable preference statements",graphSourceGroup:"Historical extraction records",legacyPreferenceGroup:"Historical preference records",
    archiveTitle:"Historical material to review",archiveCount:"{count} records in this response",archiveBrowse:"Browse legacy material",archiveBoundary:"Personal ownership or original evidence is incomplete for these records. Browsing them does not make them confirmed memories.",browseExtractions:"Browse historical extractions",browseQuotations:"Browse historical quotations",archiveEmpty:"Choose the historical material you want to browse.",emptyTrusted:"No verifiable material yet",emptyTrustedBody:"Browse historical material below, then review the original content before deciding whether to save it separately.",
    knowledgeCurated:"Verifiable material",knowledgeAll:"All material, including history",knowledgeGraph:"Historical extractions",knowledgeQuotes:"Historical quotation snippets",knowledgeCount:"{count} records in this filter",knowledgeArchiveCount:"{count} historical records available in this filter",
    sourceGraphLegacy:"Historical extraction record",sourceGraphFact:"Extraction with a verifiable quotation",sourceGraphQuotation:"Historical quotation snippet",sourceLegacyPreference:"Historical preference record",
    statusSaved:"Memory you saved",statusConfirmed:"Confirmed by you",statusCandidate:"Awaiting confirmation",statusArchive:"Historical material · Needs review",statusQuoteVerified:"Original quotation available",statusTaskVerified:"Original task traceable",statusGenerated:"AI-generated record · Review content",statusSourceUnknown:"Source evidence needs review",statusMetadata:"Knowledge-base metadata",
    provenanceTitle:"Source and recall",provenanceIdentity:"Ownership",identityVerified:"Source ownership verified",identityArchive:"Workspace archive; personal ownership unverified",identityUnknown:"Ownership not yet verified",provenanceContent:"What this record is",contentSaved:"Content you saved manually",contentConfirmed:"Content you explicitly confirmed",contentStatement:"Original user statement",contentGenerated:"AI-generated or extracted record; review its content",contentHistorical:"Unverified historical record",contentUnknown:"Content status not yet verified",
    provenanceQuote:"Original evidence",quoteVerified:"Verifiable original user statement",quoteUnverified:"Legacy excerpt; not yet verified",quoteMissing:"No verifiable original user statement",quoteUnknown:"Original evidence not yet verified",provenanceTask:"Original task",taskVerified:"Original task ownership verified",taskMissing:"No verified task link is available",provenanceMissing:"Evidence still missing",provenanceRecordID:"Original record ID",provenanceRecall:"How recall can use it",recallUnknown:"Recall status not yet verified",recallConditions:"Conditions for use",recordNotUsedProof:"Verified ownership does not establish correct content. Recall eligibility does not establish use in this turn.",
    relationSubject:"Recorded subject",relationPredicate:"Extracted relation",relationObject:"Extracted content",extractedRecord:"Historical extraction",originalSnippet:"Historical quotation snippet",sourceDetails:"Dates, original record and storage location"
  });
  const categories = ["short","long","preferences","project","global","knowledge","history"];
  Object.assign(COPY.zh,{contentInstruction:"工作目录的共享指令",viewOriginalConversation:"查看原对话",recallCandidate:"尚未确认，不参与自动回想。",recallArchive:"网页 Agent 不会自动读取这条旧资料；核对后可另存为记忆。",recallInstructions:"作为项目指令按当前工作目录加载；不属于事实记忆召回。",recallTool:"通过相关工具按需查询，浏览页面不会启用它。",recallSeparate:"保留为原始对话历史，不作为独立记忆来源自动召回。",recallDisabled:"此 Agent 当前未开启自动记忆。",recallConditional:"Agent 模式下，来源归属和当前问题匹配后才有机会选入上下文。",sourceGraphFact:"历史提取记录 · 原话待核对",recordNotUsedProof:"来源可追溯不代表内容正确。回想记录可证明是否选入上下文，不能证明模型实际采纳。",stepRecallBody:"Agent 模式先检查用户、工作目录、Agent 和会话范围，再检索相关记录；可用时也做语义检索。",stepPromptBody:"确认记录版本有效、未遗忘，并通过安全检查；按范围优先级和上下文预算选入参考。简单问答不走这条 Agent 记忆链路。"});
  Object.assign(COPY.en,{contentInstruction:"Shared workspace instructions",viewOriginalConversation:"View original conversation",recallCandidate:"Not confirmed; excluded from automatic recall.",recallArchive:"Web Agents do not automatically recall this archive. Review before saving separately.",recallInstructions:"Loaded as workspace instructions, outside factual memory recall.",recallTool:"Queried through the relevant tool when needed. Browsing does not activate it.",recallSeparate:"Kept as original conversation history, outside independent memory recall.",recallDisabled:"Automatic memory is disabled for this Agent.",recallConditional:"In Agent mode, matching ownership and relevance can make this record eligible for context.",sourceGraphFact:"Historical extraction · Evidence needs review",recordNotUsedProof:"A traceable source does not establish correct content. Recall traces show context selection, not whether the model relied on it.",stepRecallBody:"Agent mode checks the user, workspace, Agent and session before retrieving relevant records; semantic retrieval is used when available.",stepPromptBody:"Active versions, retractions and safety are checked. Scope precedence and the context budget determine selection. Direct Q&A bypasses this Agent memory path."});
  const conditionCopy={
    memory_enabled:["Agent 已开启记忆","Agent memory is enabled"],agent_mode:["仅 Agent 模式；简单问答不使用此链路","Agent mode; direct Q&A bypasses this path"],same_owner_workspace_agent:["用户归属、工作目录与 Agent 相符","User ownership, workspace and Agent match"],same_owner_workspace_shared:["同一授权工作目录中的共享记忆","Shared memory in the authorized workspace"],active_version:["使用最新且有效的记忆版本","The latest active version is used"],relevance_or_pinned_preference:["与问题相关，或是你置顶的偏好","Relevant to the question or a pinned preference"],relevance:["与当前问题匹配","Relevant to the current question"],scope_precedence:["同一标题按会话、个人、项目、全局优先级选择","Same-key scope precedence: session, user, project, global"],token_budget:["上下文空间足够；不会读取整库","Fits the context budget; the whole database is not included"],not_retracted:["未被遗忘或撤回","Not forgotten or retracted"],security_scan:["通过内容安全检查","Passes the content safety check"],actual_selection_requires_trace:["本次是否选入参考请看回想记录","Check recall traces for selection in a turn"],model_use_not_measured:["回想记录不能证明模型实际采纳","Traces do not measure model reliance"],selected_session:["限当前选定的会话","Restricted to the selected session"],context_limit:["短期上下文按数量和长度限制","Working context has count and length limits"],selected_or_relevant_session:["当前或与问题相关的会话摘要","A current or relevant session summary"],verified_web_session:["原网页会话归属可核对","The original Web conversation ownership is verified"],web_owner_bound:["使用提交任务时固定的用户与平台账号范围","Uses the user and platform-account scope captured at submission"],supported_adapter_source:["该来源已接入网页召回适配","The source is connected to Web recall"],source_limit:["仅检索有界的近期记录","Retrieval is bounded to recent records"],original_quote_verified:["提取内容具有可核对的原始用户话语","An extraction has a verifiable original user quotation"],personal_web_recall_only:["此说明指个人网页 Agent 的召回","This describes personal Web Agent recall"],review_and_save_required:["需要你先核对，再明确另存","Review and explicitly save separately"],view_does_not_activate:["浏览不会确认或激活记录","Browsing does not confirm or activate the record"],non_web_compatibility_separate:["其他渠道的旧兼容路径另行处理","Other channels have separate legacy compatibility paths"],rag_tool_available:["Agent 可以使用知识检索工具","The Agent has a knowledge retrieval tool"],knowledge_base_selected:["已选定要检索的资料库","A knowledge base is selected"],indexed_document_match:["文档索引匹配当前查询","Indexed document content matches the query"],metadata_is_not_document_evidence:["资料库名称和数量不是文档原文证据","Knowledge-base metadata is not document evidence"],current_chat_context_separate:["当前对话上下文由独立机制维护","Current chat context is maintained separately"],original_user_statement_adapter:["查看原用户话语，完整画像不会自动传入","Review the original statement; the full profile is not injected"],full_profile_not_recalled:["完整旧画像不参与网页自动回想","The full legacy profile is excluded from automatic Web recall"],runtime_path_unverified:["实际召回路径尚待核实","The runtime recall path remains unverified"]
  };
  for(const [code,[zh,en]] of Object.entries(conditionCopy)){COPY.zh["condition_"+code]=zh;COPY.en["condition_"+code]=en;}
  Object.assign(COPY.zh,{sourceLegacyPreference:"以前保存的偏好",legacyPreferenceGroup:"以前保存的偏好",archiveTitle:"以前的资料 · 需要你核对",statusArchive:"需要你核对",archiveBoundary:"这些资料保存在当前工作目录中，但部分缺少原对话或用户信息。先看内容和来源；确认无误后，可以另存为你的记忆。",identityArchive:"在本工作目录找到，缺少个人归属证据",quoteMissing:"没有找到对应的用户原话",quoteUnverified:"保存过摘录，但还没有找到对应原对话",legacyPreferenceReadOnly:"这是以前保存的偏好。因为缺少原对话或用户信息，暂不能确认属于当前用户。核对内容后可另存为你的偏好，旧记录会保留。",savePreference:"核对后保存为偏好",tagsTitle:"分类标签",toolsTag:"工具调用",preferenceTag:"用户偏好",styleTag:"用户风格",experienceTag:"任务经验",factTag:"知识记录",savedTag:"已保存标签",noSavedTags:"原记录没有保存标签",toolsRecorded:"实际调用的工具",tagSource:"标签来源",tagSourceBody:"标签来自原记录保存的标签、工具回执或明确的用户原话；不会把系统推测写成用户偏好。"});
  Object.assign(COPY.en,{sourceLegacyPreference:"Previously saved preferences",legacyPreferenceGroup:"Previously saved preferences",archiveTitle:"Earlier material · Review first",statusArchive:"Review first",archiveBoundary:"This material is stored in the current workspace, but some records lack an original conversation or user identity. Review the content and source before saving it as your memory.",identityArchive:"Found in this workspace; personal ownership evidence is missing",quoteMissing:"No matching original user statement was found",quoteUnverified:"An excerpt was stored, but its original conversation is not verified",legacyPreferenceReadOnly:"This preference was saved earlier. Its original conversation or user identity is missing, so it cannot yet be attributed to the current user. Review before saving it as your preference; the old record is retained.",savePreference:"Review and save preference",tagsTitle:"Classification tags",toolsTag:"Tool calls",preferenceTag:"User preferences",styleTag:"User style",experienceTag:"Task experience",factTag:"Knowledge record",savedTag:"Stored tags",noSavedTags:"No tags were stored on the original record",toolsRecorded:"Tools actually called",tagSource:"Where tags come from",tagSourceBody:"Tags come from stored record tags, tool receipts or explicit user statements. System inference is not presented as a user preference."});
  Object.assign(COPY.zh,{preferencesDescription:"查看你保存的偏好、明确说过的回答风格和以前的偏好资料。",styleExplanation:"用户风格包括你明确要求的回答长度、语气、语言等。旧画像里的沟通风格只是系统统计，没有对应原话时不会当成你的偏好。",catalogNote_web_profile_original_statements_only:"完整旧画像不用于网页自动回想；仅可核对的原用户话语通过独立来源参与。",catalogNote_web_tool_receipts_only:"网页回想可参考已核对任务的工具调用回执，不读取整份工具统计。"});
  Object.assign(COPY.en,{preferencesDescription:"Your saved preferences, explicitly requested response style and earlier preference records.",styleExplanation:"User style covers response length, tone and language you explicitly requested. Legacy communication-style statistics are system inference and are not treated as your preferences without original statements.",catalogNote_web_profile_original_statements_only:"The full legacy profile is excluded from automatic Web recall. Verifiable original statements use a separate source adapter.",catalogNote_web_tool_receipts_only:"Web recall can reference verified task tool receipts, without reading the entire tool-statistics profile."});
  Object.assign(COPY.zh,{statusEventVerified:"有执行记录可核对",originalTaskID:"原任务 ID",originalEventID:"执行记录 ID",originalMessageID:"原消息 ID",missing_source_location_missing:"原保存位置",missing_source_record_id_missing:"原记录标识",missing_original_user_quote_unverified:"对应用户原话",missing_personal_ownership_unverified:"对应用户信息",missing_original_task_id_missing:"原任务 ID（已有执行记录，但未保存任务关联）"});
  Object.assign(COPY.en,{statusEventVerified:"Execution record traceable",originalTaskID:"Original task ID",originalEventID:"Execution record ID",originalMessageID:"Original message ID",missing_source_location_missing:"Original storage location",missing_source_record_id_missing:"Original record ID",missing_original_user_quote_unverified:"Matching original user statement",missing_personal_ownership_unverified:"User ownership evidence",missing_original_task_id_missing:"Original task ID (an execution record exists, without a saved task link)"});
  Object.assign(COPY.zh,{condition_non_durable:"短期上下文，不自动当成长久记忆",condition_same_owner_namespace:"同一授权记忆空间",condition_current_session:"仅当前会话的提取记录",condition_user_quote_verified:"对应的用户原话已核对",recallEvidenceMissing:"缺少对应原话，暂不作为自动记忆参考。",recallOwnerMissing:"缺少个人归属证据，暂不作为自动记忆参考。",reasonStale:"记录已失效或适用范围改变",reasonVersionChanged:"检索期间内容已修改",archiveExtractionHelp:"历史提取记录：AI 以前把内容拆成对象、关系和内容。它是一次提取结果，需要核对原文。",archiveQuotationHelp:"历史原文片段：旧知识库保存的文字摘录。缺少原对话时，不能证明是谁说的。"});
  Object.assign(COPY.en,{condition_non_durable:"Working context, not automatically durable memory",condition_same_owner_namespace:"Same authorized memory namespace",condition_current_session:"Extraction from the current conversation only",condition_user_quote_verified:"The original user quotation has been checked",recallEvidenceMissing:"Matching original evidence is missing; excluded from automatic memory context.",recallOwnerMissing:"Personal ownership evidence is missing; excluded from automatic memory context.",reasonStale:"Record is inactive or its scope changed",reasonVersionChanged:"Content changed during retrieval",archiveExtractionHelp:"Historical extractions: AI previously separated content into a subject, relation and value. Review the original before trusting the extraction.",archiveQuotationHelp:"Historical quotations: text excerpts retained in the old knowledge store. Without the original conversation, the speaker cannot be verified."});
  Object.assign(COPY.zh,{styleExplanation:"用户风格：你明确要求的回答长度、语气、语言。系统推测不算已确认偏好。",sourcePreferenceNote:"这条来自你的原话。核对后可保存为可编辑的长期偏好。",readOnlyNote:"原记录只读。需要修改时，可核对后另存一条记忆。"});
  Object.assign(COPY.en,{styleExplanation:"User style covers explicitly requested response length, tone or language. Inferred statistics are kept separate.",sourcePreferenceNote:"An original user statement. Review before saving it as an editable long-term preference.",readOnlyNote:"The original record is read-only. Review and save a separate memory to make changes."});
  COPY.zh.toolsNotRecorded="这条原记录没有保存工具调用明细。";COPY.en.toolsNotRecorded="Tool-call details were not saved on this original record.";
  Object.assign(COPY.zh,{
    inputTitle:"模型输入引用的记忆",inputBoundary:"这里记录本次模型请求包含的已存内容。是否送达请看调用状态，不代表模型一定采用。",inputCall:"模型调用",inputCategory:"来源分类",inputAllCategories:"全部来源",otherStorage:"现存记忆与资料",otherStorageNote:"浏览已保存的资料；这里的记录不代表本次模型输入已引用。",
    inputCallLabel:"第 {index} 次调用 · {time}",inputSucceeded:"已发送至模型",inputAttempted:"尝试发送",inputFailed:"发送失败",inputStatusUnknown:"发送状态未记录",inputSourceCount:"本次已记录 {count} 个来源",inputNoReceipt:"暂无对话引用记录",inputNoReceiptBody:"旧任务的实际输入无法还原；现存记忆与资料仍可在下方查看。",inputNoSources:"这次输入没有已记录的记忆来源",inputNoSourcesBody:"这不代表数据库没有资料，也不代表其他模型调用没有引用。未记录出处的内容不代表未使用。",inputNoFilter:"这个分类没有引用来源",inputNoFilterBody:"选择“全部来源”查看这次调用的其他引用。",
    inputExcerpt:"本次提交的记忆片段",inputExcerptMissing:"本次没有保存可显示的输入片段，无法还原。",inputOrigin:"确切出处",inputCurrentContent:"当前来源内容",inputCapturedRevision:"输入当时的版本",inputCurrentRevision:"当前来源版本",inputRevisionChanged:"原来源已变化。上面的历史输入片段保持不变。",inputCurrentMissing:"原来源已不存在，历史输入片段仍保留。",inputCurrentUnavailable:"当前来源暂时无法读取。",inputCurrentReadOnly:"原来源目前只读。",inputEditSource:"修改原来源",inputDeleteSource:"删除原来源",inputSaveSource:"保存原来源",inputEditNote:"直接修改原记录。历史输入片段保持不变。",inputDeleteConfirm:"确认删除这个原来源？之后的调用将无法再从这里引用；历史输入记录保留。",inputConfirmDelete:"确认删除原来源",inputSourceSaved:"原来源已更新，历史输入片段未变。",inputSourceDeleted:"原来源已删除，历史输入片段保留。",inputSourceConflict:"原来源已变化。编辑内容已保留，请刷新并核对最新内容后重新打开编辑。",inputSourceRequired:"请填写原来源内容。",inputSourceDraftPending:"有未保存的来源编辑。请先保存或处理草稿，再切换任务或模型调用。",inputSourceNoRevision:"缺少原来源版本，暂时无法修改。",inputSourceReadonly:"此原来源不支持这项操作。",inputRecordTime:"调用记录时间",inputTaskID:"原任务标识",inputRecordID:"输入记录标识",inputSourceID:"引用来源标识",inputNoLocation:"确切出处未记录",inputUnnamedSource:"未命名来源",inputLoadFailure:"暂时无法读取模型输入引用",inputCategoryUnknown:"未分类"
  });
  Object.assign(COPY.en,{
    inputTitle:"Memory referenced in model input",inputBoundary:"These are stored contents included in this model request. Check the call status for delivery; this does not prove the model used them.",inputCall:"Model call",inputCategory:"Source category",inputAllCategories:"All sources",otherStorage:"Existing memories and material",otherStorageNote:"Browse stored material. These records do not prove it was included in this model input.",
    inputCallLabel:"Call {index} · {time}",inputSucceeded:"Sent to model",inputAttempted:"Send attempted",inputFailed:"Send failed",inputStatusUnknown:"Send status not recorded",inputSourceCount:"{count} sources recorded for this input",inputNoReceipt:"No conversation input record yet",inputNoReceiptBody:"Actual inputs from older tasks cannot be reconstructed. Existing memories and material are available below.",inputNoSources:"No memory sources recorded in this input",inputNoSourcesBody:"This does not mean storage is empty or that other model calls referenced no memories. Content without a recorded source may still have been included.",inputNoFilter:"No references in this category",inputNoFilterBody:"Select All sources to see the other references from this call.",
    inputExcerpt:"Memory excerpt submitted in this request",inputExcerptMissing:"A displayable input excerpt was not saved and cannot be reconstructed.",inputOrigin:"Exact source",inputCurrentContent:"Current source content",inputCapturedRevision:"Version at input time",inputCurrentRevision:"Current source version",inputRevisionChanged:"The source has changed. The historical input excerpt above remains unchanged.",inputCurrentMissing:"The original source no longer exists. The historical input excerpt is retained.",inputCurrentUnavailable:"The current source could not be read.",inputCurrentReadOnly:"The original source is currently read-only.",inputEditSource:"Edit original source",inputDeleteSource:"Delete original source",inputSaveSource:"Save original source",inputEditNote:"This changes the original record. Historical input excerpts remain unchanged.",inputDeleteConfirm:"Delete this original source? Future calls cannot reference it here; historical input records are retained.",inputConfirmDelete:"Delete original source",inputSourceSaved:"Original source updated; the historical input excerpt is unchanged.",inputSourceDeleted:"Original source deleted; historical input excerpt retained.",inputSourceConflict:"The original source changed. Your edit is retained. Refresh and review the current content before reopening the editor.",inputSourceRequired:"Enter the original source content.",inputSourceDraftPending:"An original-source edit is unsaved. Save or resolve the draft before switching tasks or model calls.",inputSourceNoRevision:"The original-source revision is missing; editing is unavailable.",inputSourceReadonly:"This operation is unavailable for the original source.",inputRecordTime:"Call record time",inputTaskID:"Original task ID",inputRecordID:"Input record ID",inputSourceID:"Referenced source ID",inputNoLocation:"Exact source was not recorded",inputUnnamedSource:"Untitled source",inputLoadFailure:"Model input references could not be read",inputCategoryUnknown:"Uncategorized"
  });
  Object.assign(COPY.zh,{organizeTodoUse:"待办保存的是计划。AI 查询待办后，工具结果才进入后续对话；不自动成为长期记忆。",organizeSavedFacts:"已保存的事实与结论",organizeTaskMaterial:"历史任务参考（原文）",promptProvider_tool_learning:"AI 学习笔记",promptProviderBody_tool_learning:"learn 工具保存的原始笔记。工具查阅摘要或读取正文后才进入后续回合，不自动注入整个目录。"});
  Object.assign(COPY.en,{organizeTodoUse:"Scheduled tasks are plans. Tool lookups add their results to later turns; they do not become long-term memory automatically.",organizeSavedFacts:"Saved facts and conclusions",organizeTaskMaterial:"Historical task references",promptProvider_tool_learning:"AI learning notes",promptProviderBody_tool_learning:"Original notes saved by the learn tool. Looking up summaries or reading the files adds tool output to later turns; the directory is not injected automatically."});
  Object.assign(COPY.zh,{
    promptTitle:"会进入提示词的历史信息",promptBoundary:"按当前代码的注入机制展示原始记录；直接带入或按问题召回，不等于每条都在某次调用中使用。",promptSession:"当前会话",promptLatestSession:"最近的合法会话",promptCount:"当前来源 {count} 条",promptDirect:"直接带入",promptRelevant:"按问题召回",promptModeUnknown:"注入方式未记录",promptDirectBody:"由当前会话或系统输入构建路径带入，仍受上下文长度和安全检查限制。",promptRelevantBody:"先按当前问题筛选，再按范围、相关性、预算和安全检查选择。",promptEmpty:"当前会话暂无可显示的历史信息",promptEmptyBody:"这里列出代码实际接入的历史来源，不用新调用记录或资料库存冒充。可切换会话，并查看下方注入机制。",promptExcerpt:"历史信息原始片段",promptMechanism:"如何进入提示词",promptInjection:"进入的位置",promptHistory:"会话历史消息",promptMemory:"记忆上下文",promptSystem:"系统提示词",promptPositionUnknown:"注入位置未记录",promptCodePath:"实际代码路径",promptProviders:"这些信息如何进入提示词",promptProviderAvailable:"当前可用",promptProviderUnavailable:"当前不可用",promptProviderCount:"可显示 {count} 条",promptProviderCountUnknown:"数量未核实",promptSourceSaved:"原来源已更新",promptSourceDeleted:"原来源已删除",promptEditNote:"直接修改原来源记录，之后构建输入时按实际注入机制读取。",promptLoadFailure:"暂时无法读取提示词历史来源",recordedInputs:"查看某次实际调用",otherStorage:"其他保存资料（不自动进入提示词）",otherStorageNote:"这里是保存的资料库存。是否接入提示词，请以上方实际注入来源为准。"
  });
  Object.assign(COPY.en,{
    promptTitle:"Historical information entering prompts",promptBoundary:"Original records from the current injection code. Content is included directly or recalled for a question; this does not prove every record was used in a particular call.",promptSession:"Current conversation",promptLatestSession:"Latest authorized conversation",promptCount:"{count} current sources",promptDirect:"Included directly",promptRelevant:"Recalled for the question",promptModeUnknown:"Injection mode not recorded",promptDirectBody:"Included by the conversation or system-input builder, subject to context limits and safety checks.",promptRelevantBody:"Filtered for the current question, then selected by scope, relevance, budget and safety checks.",promptEmpty:"No displayable history for this conversation",promptEmptyBody:"This lists history sources connected by the actual code. A new call record or a storage inventory is not a substitute. Select another conversation or inspect the injection mechanisms below.",promptExcerpt:"Original historical excerpt",promptMechanism:"How it enters the prompt",promptInjection:"Input position",promptHistory:"Conversation history messages",promptMemory:"Memory context",promptSystem:"System prompt",promptPositionUnknown:"Input position not recorded",promptCodePath:"Actual code path",promptProviders:"How these sources enter prompts",promptProviderAvailable:"Available now",promptProviderUnavailable:"Unavailable now",promptProviderCount:"{count} displayable records",promptProviderCountUnknown:"Count unverified",promptSourceSaved:"Original source updated",promptSourceDeleted:"Original source deleted",promptEditNote:"This changes the original record. Future inputs read it through the actual injection mechanism.",promptLoadFailure:"Prompt history sources could not be read",recordedInputs:"Inspect a particular model call",otherStorage:"Other stored material (not automatically injected)",otherStorageNote:"This is a storage inventory. Use the actual injection sources above to see whether material is connected to prompt construction."
  });
  Object.assign(COPY.en,{
    promptProvider_session_history:"Conversation history messages",promptProviderBody_session_history:"The latest 50 user and assistant messages from the authorized conversation are cleaned and included as history. Later compression may shorten them.",
    promptProvider_web_history:"Stored task results and lessons",promptProviderBody_web_history:"Verified task results, lessons, explicit quotations and associated learning knowledge are filtered by the question, retractions, budget and safety checks.",
    promptProvider_project_instructions:"Project instructions",promptProviderBody_project_instructions:"The instruction loader resolves project and global files into the dynamic system message. External global files remain read-only.",
    promptProvider_unified_memory:"Preferences, working notes and summaries",promptProviderBody_unified_memory:"Current-session notes and pinned preferences participate in context. Other active memories and summaries are recalled for the question, subject to scope, safety and context limits."
  });
  const pageSize = 20;
  const $ = (id) => document.getElementById(id);
  const query = new URLSearchParams(location.search);
  if(query.get("embed")==="1")document.documentElement.classList.add("memory-embedded");
  const state = {
    category: categories.includes(query.get("view") || query.get("category")) ? (query.get("view") || query.get("category")) : "preferences",
    agent: query.get("agent") || "", session: query.get("session_id") || query.get("session") || "",
    agents: [], sessions: [], items: [], viewNotes: [], total: 0, totalKnown: true, offset: 0, selected: "", archiveOpen: false,
    busy: false, loaded: false, loading: false, metadataLoaded: false, loadSeq: 0, traceSeq: 0, detailSeq: 0,
    controller: null, detailController: null, traceController: null,
    searchTimer: null, noticeTimer: null, editing: null, editContext: null, forget: "",
    drafts: new Map(), messages: new Map(), historySources: new Map(),
    catalog: null, catalogAgent: "", catalogPendingAgent: "", catalogSeq: 0, catalogController: null, catalogError: false
  };
  const entries = $("entries"), detail = $("memory-detail"), editor = $("memory-editor");
  const inputState={agent:state.agent,session:state.session,inputID:"",inputs:[],input:null,current:Object.create(null),selected:"",filter:"",agents:[],sessions:[],loaded:false,seq:0,controller:null,busy:false,deleting:"",drafts:new Map(),editorContext:null};
  const promptState={agent:state.agent,session:"",viewID:"",sources:[],current:Object.create(null),selected:"",filter:"",agents:[],sessions:[],providers:[],loaded:false,seq:0,controller:null,deleting:""};
  const memoryGroupOrder=["all","preferences","project","knowledge","tasks"];
  const memoryGroupState={group:"all",preferenceView:null,preferenceArchiveOpen:false,preferencePreviewMode:"all",taskView:"summaries",libraries:{preferences:{items:[],loaded:false,loading:false,total:0,offset:0},knowledge:{items:[],loaded:false,loading:false,total:0,offset:0,source:"materials",materialTotal:null}},seq:0,controller:null};
  const conversationState={seq:0,controller:null,sessionID:"",limit:200,messages:[],total:0,hasMore:false,error:""};
  const preferenceConversationCache=new Map();
  Object.assign(COPY.zh,{
    promptTitle:"记忆管理",promptBoundary:"按用途查看历史信息，了解 AI 什么时候参考它。",
    organizeShort:"短期记忆",organizePreferences:"用户偏好",organizeLong:"长期事实与经验",organizeProject:"项目与全局约定",organizeKnowledge:"知识库",organizeLearning:"AI 学习积累",
    organizeShortDescription:"当前会话的历史消息、工作状态和会话摘要。",
    organizeShortUse:"续聊时：历史消息直接加入对话；当前工作记录作为本轮参考。摘要按问题相关性选取，长对话还会压缩。",
    organizePreferencesDescription:"回答语言、表达风格、文档格式等你希望 AI 记住的习惯。",
    organizePreferencesUse:"设为常驻的偏好，AI 每轮优先带入；其他已保存偏好，AI 按当前问题挑选。旧偏好记录目前不自动使用，核对并保存后才作为你的偏好。",
    organizeLongDescription:"跨任务可复用的事实、结论与经验；相同任务结果和笔记合并展示，原始记录分别保留。",
    organizeLongUse:"开始新任务时，AI 挑选与当前问题相关的事实和经验作为参考。旧任务结果单独标为原文，需要核对后才能作为可靠结论。",
    organizeProjectDescription:"项目规则、工作区共享约定，以及实际加载的 AGENTS 等说明。",
    organizeProjectUse:"说明文件在任务开始时加入系统要求；已保存的项目和全局事实按问题召回。全局表示当前工作区共享，不跨工作区。",
    organizeKnowledgeDescription:"上传资料、学习知识，以及工作区保存的旧知识记录。",
    organizeKnowledgeUse:"文档经检索后，找到的片段通过工具结果进入下一轮；有出处的学习知识可按问题召回。旧知识记录只供核对，未自动全部带入。",
    organizeLearningDescription:"learn 工具实际保存的学习笔记，保留原文件与修改时间。",
    organizeLearningUse:"AI 查阅笔记摘要或正文后，内容通过工具结果进入后续回合；不会自动把整个笔记目录塞进提示词。任务总结在“长期事实与经验”查看。",
    organizeDirect:"随会话带入",organizeRelevant:"相关时参考",organizeOnDemand:"工具查阅后带入",organizeStored:"尚未用于新对话",organizeOldPreference:"旧偏好记录",organizeOldKnowledge:"旧知识记录",organizeRecords:"{count} 条内容",organizeOrigins:"{count} 处原始来源",organizeSourceRecord:"原始记录",organizeResult:"任务结果",organizeLesson:"任务学习笔记",organizeYourMessage:"你之前说",organizeAssistantMessage:"AI 的历史回复",organizeTask:"任务参考：{title}",organizeBody:"内容",organizeSourceDetails:"出处与进入方式",organizeAddPreference:"新增偏好",organizeConfirmPreference:"核对并保存为我的偏好",organizeConfirmedNote:"这会明确保存为你的偏好；旧记录保留，不会被系统自动当成偏好。",organizeKnowledgeSource:"查看内容",organizeKnowledgeAll:"全部已存知识",organizeKnowledgeVerified:"可核对的知识",organizeKnowledgeLearning:"任务学习知识",organizeKnowledgeDocuments:"上传资料",organizeKnowledgeGraph:"历史提取记录",organizeKnowledgeQuotes:"历史原文",organizeKnowledgeManage:"管理上传资料",organizeTodo:"查看待办任务",organizeOnlySelectedDelete:"这里只修改或删除选中的原始记录；其他来源保留。",organizePartial:"列表按页读取，来源范围保持原权限。",organizeUnavailable:"这部分资料暂时无法读取，不能据此判断没有记录。",organizeNoShort:"这个会话还没有历史上下文。",organizeNoPreferences:"还没有已启用的偏好。你可以核对旧记录，或新增自己的要求。",organizeNoLong:"还没有可显示的长期事实或任务经验。",organizeNoProject:"当前没有加载项目约定。",organizeNoKnowledge:"当前没有可显示的知识记录。",organizeNoLearning:"还没有保存学习笔记；不会把工具使用次数冒充学习结论。"
  });
  Object.assign(COPY.en,{
    promptTitle:"Memory management",promptBoundary:"Review historical information by purpose and see when AI refers to it.",
    organizeShort:"Short-term memory",organizePreferences:"User preferences",organizeLong:"Long-term facts & experience",organizeProject:"Project & global rules",organizeKnowledge:"Knowledge base",organizeLearning:"AI learning",
    organizeShortDescription:"Messages, working state and summaries from this conversation.",organizeShortUse:"On follow-up turns, messages become conversation history and working records become context. Summaries are selected by relevance; long conversations may be compressed.",
    organizePreferencesDescription:"Your language, response style and document-format preferences.",organizePreferencesUse:"Pinned preferences receive priority; other enabled preferences are recalled by relevance. Old preference records are not used automatically until you review and save them.",
    organizeLongDescription:"Reusable facts, conclusions and experience. Equal task results and notes are grouped; original records remain separate.",organizeLongUse:"Relevant information is selected for a new task and added as context. Historical task results remain source material, rather than verified conclusions.",
    organizeProjectDescription:"Project rules, workspace-wide conventions and loaded instruction files.",organizeProjectUse:"Instruction files become system requirements at task start. Saved project and global facts are recalled by relevance. Global means shared within this workspace.",
    organizeKnowledgeDescription:"Uploaded material, learned knowledge and historical workspace knowledge.",organizeKnowledgeUse:"Search results enter the next turn as tool output. Knowledge with a verified source can be recalled by relevance. Historical records remain available for review and are not injected wholesale.",
    organizeLearningDescription:"Learning notes saved by the learn tool, with their original files and modification dates.",organizeLearningUse:"Reading a note summary or body adds tool output to a subsequent turn. The full directory is not injected automatically. Task lessons appear under long-term facts and experience.",
    organizeDirect:"Conversation context",organizeRelevant:"Recalled when relevant",organizeOnDemand:"After tool lookup",organizeStored:"Not used automatically",organizeOldPreference:"Old preference record",organizeOldKnowledge:"Historical knowledge",organizeRecords:"{count} items",organizeOrigins:"{count} original sources",organizeSourceRecord:"Original record",organizeResult:"Task result",organizeLesson:"Task learning note",organizeYourMessage:"Your earlier message",organizeAssistantMessage:"Earlier AI response",organizeTask:"Task reference: {title}",organizeBody:"Content",organizeSourceDetails:"Source and context use",organizeAddPreference:"Add preference",organizeConfirmPreference:"Review and save as my preference",organizeConfirmedNote:"This explicitly saves your preference. The old record is retained and is not adopted automatically.",organizeKnowledgeSource:"Show",organizeKnowledgeAll:"All stored knowledge",organizeKnowledgeVerified:"Verifiable knowledge",organizeKnowledgeLearning:"Task learning",organizeKnowledgeDocuments:"Uploaded material",organizeKnowledgeGraph:"Historical extractions",organizeKnowledgeQuotes:"Original quotations",organizeKnowledgeManage:"Manage uploaded material",organizeTodo:"View scheduled tasks",organizeOnlySelectedDelete:"Editing or deleting affects only this selected original record. Other sources remain.",organizePartial:"Records are paged and keep their original access scope.",organizeUnavailable:"This source could not be read; this does not establish that it is empty.",organizeNoShort:"No historical context in this conversation yet.",organizeNoPreferences:"No enabled preferences yet. Review old records or add your own requirements.",organizeNoLong:"No long-term facts or task experience to display yet.",organizeNoProject:"No project rules are currently loaded.",organizeNoKnowledge:"No knowledge records to display here.",organizeNoLearning:"No saved learning notes yet. Tool usage counts are not treated as learned conclusions."
  });
  Object.assign(COPY.zh,{
    organizePreferencesDescription:"个人习惯，以及明确写明客户、项目或 case 的格式与处理要求。",
    organizePreferencesUse:"本次会话的要求只用于续聊；其他已接入偏好按问题相关性选择，常驻偏好优先。客户、case 的适用条件需要写明。旧提取记录不会自动作为偏好。",
    organizeActivePreferences:"可供 AI 参考的偏好（{count}）",organizePendingPreferences:"尚未整理的旧记录（{count}）",organizeOldPreference:"历史提取记录",
    organizePreferenceReview:"这些是旧系统提取的记录，可能含整段对话，尚未确认是偏好。请只保存你希望长期适用的要求。",
    organizeNoActivePreferences:"目前没有可供 AI 参考的偏好，AI 不会把下面的旧记录当成你的要求。",
    organizePreferenceScope:"适用范围",organizePreferenceScope_user:"个人通用（当前助手）",organizePreferenceScope_project:"当前项目 / 工作区",organizePreferenceScope_session:"仅本次会话",organizePreferenceScope_global:"工作区共享",organizePreferenceScope_unknown:"原记录未注明",
    organizePreferenceEditor:"填写明确要求，保存后供 AI 在所选范围内参考。本次任务选“仅本次会话”。客户或 case 要在内容里写明，目前还没有单独绑定客户或 case 编号。",
    organizeAiUse:"AI 怎么用",organizeHumanUse:"你怎么处理",organizePreferenceAiUse:"保存后，本次会话的要求会随续聊带入；其他偏好在问题相关时选作参考。常驻偏好优先，当前明确要求优先于历史偏好。",
    organizePreferenceExample:"例如：“客户 A 的报告沿用原模板”。之后处理客户 A 的报告时，AI 才可能选中这条要求作参考。",
    organizePreferenceSteps:"查看旧记录，找出客户、case 或个人的明确要求。|点击“整理成一条偏好”，填写对象、要求和适用条件。|选择适用范围并保存。没有明确要求的旧记录可以保留，不必逐条整理。",
    organizeConfirmPreference:"整理成一条偏好",organizeOriginalRecord:"查看这条旧记录的完整原文",organizePreferenceName:"对象与要求",organizePreferenceNameExample:"例如：客户 A · 报告格式",organizePreferenceValueExample:"处理客户 A 的报告时，沿用客户提供的模板；保留表格结构。",
    organizeClaim:"记录中的说法",organizeCapabilityClaim:"{subject} 具备“{object}”能力。",organizeFactSourceSubject:"记录对象",organizeFactExpired:"已结束有效期",organizeFactFuture:"尚未到生效日期",organizeFactMissingEvidence:"缺少可核对的原文证据",organizeFactUnverifiedEvidence:"已有依据尚未核对",organizeFactArchivedUse:"这条是保存的历史记录。目前不作为自动回想的依据，也不会因你在这里查看就进入新对话。",organizeFactCapabilityUse:"这条描述助手被认为具有某种能力。真正能否浏览网页、操作浏览器，要看当前可用工具及权限。",organizeFactValidity:"记录有效期",organizeFactEvidence:"原文依据",organizeFactRawRelation:"原始关系名称"
  });
  Object.assign(COPY.en,{
    organizePreferencesDescription:"Personal habits and requirements with explicit customer, project or case context.",
    organizePreferencesUse:"Session requirements apply to follow-up turns in that conversation. Other connected preferences are selected by relevance, with pinned ones receiving priority. State customer or case conditions explicitly. Historical extractions are not adopted automatically.",
    organizeActivePreferences:"Preferences available to AI ({count})",organizePendingPreferences:"Unreviewed historical records ({count})",organizeOldPreference:"Historical extraction",
    organizePreferenceReview:"These old extractions may contain entire conversations and are not confirmed preferences. Save only the requirements you want to keep.",
    organizeNoActivePreferences:"No explicit preferences have been saved. AI does not treat the historical records below as your requirements.",
    organizePreferenceScope:"Applies to",organizePreferenceScope_user:"Personal defaults for this assistant",organizePreferenceScope_project:"Current project / workspace",organizePreferenceScope_session:"This conversation only",organizePreferenceScope_global:"Shared workspace",organizePreferenceScope_unknown:"Not recorded",
    organizePreferenceEditor:"Save an explicit requirement for AI to reference within the chosen scope. Select this conversation only for a single task. State customer or case conditions in the content; separate IDs are not currently bound.",
    organizeAiUse:"How AI uses this",organizeHumanUse:"What you can do",organizePreferenceAiUse:"Saved session requirements accompany follow-up turns. Other preferences are selected by relevance, with pinned ones receiving priority. Your current instructions take priority over historical preferences.",
    organizePreferenceExample:"For example: use customer A's original report template. AI may select this requirement when working on a report for customer A.",
    organizePreferenceSteps:"Read a historical record and find an explicit personal, customer or case requirement.|Click Save an explicit preference and specify who it applies to, the requirement and its conditions.|Choose the scope and save. Records without an explicit requirement can be left as they are.",
    organizeConfirmPreference:"Save an explicit preference",organizeOriginalRecord:"Read the complete original record",organizePreferenceName:"Who and what",organizePreferenceNameExample:"For example: customer A - report format",organizePreferenceValueExample:"For customer A's reports, use the customer's template and preserve the table structure.",
    organizeClaim:"Recorded claim",organizeCapabilityClaim:"{subject} has the capability: {object}.",organizeFactSourceSubject:"Recorded subject",organizeFactExpired:"Validity has ended",organizeFactFuture:"Not yet effective",organizeFactMissingEvidence:"No verifiable original evidence",organizeFactUnverifiedEvidence:"Stored evidence has not been verified",organizeFactArchivedUse:"This is a stored historical record. It is not eligible for automatic recall; viewing it here does not add it to new conversations.",organizeFactCapabilityUse:"This describes a capability attributed to the assistant. Actual browser access and automation depend on the currently available tools and permissions.",organizeFactValidity:"Recorded validity",organizeFactEvidence:"Original evidence",organizeFactRawRelation:"Original relation"
  });
  Object.assign(COPY.zh,{
    organizeKnowledgeDescription:"先看具体资料和学习笔记；原文摘录、提取的关系记录分开查看。",
    knowledgeMaterials:"资料与笔记",knowledgeQuotes:"原文摘录",knowledgeRelations:"提取记录",knowledgeMore:"其他来源",organizeKnowledgeManage:"打开文档管理",
    knowledgeMaterialsUse:"任务学习知识按问题相关性选作参考；原文件笔记、上传资料在 AI 查阅或检索后带入。每份内容保留真实出处。",
    knowledgeQuotesUse:"这里是历史保存的文字片段。片段可能只是对话或文件状态说明，不能当作对应文章的全文。",
    knowledgeRelationsUse:"这里是从历史内容中提取的对象和关系，例如某助手被描述具备某能力；不作为文章列表。",
    knowledgeAllUse:"这里显示不同类型的存储记录；关系、摘录、资料和笔记会标明各自类型。",
    knowledgeNoteType:"学习笔记 · 原文件",knowledgeLearnedType:"任务学习知识",knowledgeCollectionType:"资料库目录",knowledgeQuoteType:"历史文字摘录",knowledgeRelationType:"历史关系提取",
    knowledgeCapabilityTitle:"能力描述：{topic}",knowledgeRelationTitle:"关于“{subject}”的提取记录",knowledgeQuoteTitle:"摘录：{text}",knowledgeRecordTitle:"提取记录 #{id}",knowledgeRecordID:"记录 #{id}",knowledgeOrigin:"这份内容来自哪里",knowledgeArticle:"对应资料",knowledgeNoArticle:"未保存对应文章的标题或正文",knowledgeRelatedObject:"关联对象",knowledgeFile:"原文件",knowledgeSourceConversation:"历史对话中的提取记录",knowledgeSourceStoredText:"旧记录中的文字摘录",knowledgeSourceTask:"任务学习记录",knowledgeSourceCollection:"上传资料库",
    knowledgeNoArticleHelp:"它只保存了对象之间的关系，无法据此打开一篇文章。",knowledgeQuoteHelp:"它保存的是这段摘录，未关联可打开的文章或完整文件。关联对象不是文章标题。",
    knowledgeSavedContent:"保存的内容",knowledgeSavedSummary:"保存的摘要",knowledgeCollectionHelp:"这个条目是资料库目录；具体文档名称和原文在文档管理中查看。",knowledgeOpenCollection:"查看库内文档",knowledgeEmptyMaterials:"这里暂时没有可查看的资料或笔记。可以打开文档管理；原文摘录和提取记录仍在各自分类中。",knowledgeReadNote:"按文章阅读已保存的笔记正文；编辑仍修改同一份原文件。",knowledgeSourceExtraction:"历史提取记录"
  });
  Object.assign(COPY.en,{
    organizeKnowledgeDescription:"Read reference material and saved notes. Historical excerpts and extracted relations have separate views.",
    knowledgeMaterials:"Materials & notes",knowledgeQuotes:"Text excerpts",knowledgeRelations:"Extracted records",knowledgeMore:"Other sources",organizeKnowledgeManage:"Open document manager",
    knowledgeMaterialsUse:"Task learning is recalled when relevant. Original notes and uploaded documents enter context after tool reading or retrieval. Each item keeps its actual origin.",
    knowledgeQuotesUse:"These are saved historical excerpts, which may be conversation snippets or file-status notes. They are not the full text of an article.",
    knowledgeRelationsUse:"These are extracted subjects and relations, such as a capability attributed to an assistant. They are not article titles.",
    knowledgeAllUse:"Different stored record types are shown with separate labels for relations, excerpts, materials and notes.",
    knowledgeNoteType:"Learning note · Original file",knowledgeLearnedType:"Task learning",knowledgeCollectionType:"Document collection",knowledgeQuoteType:"Historical excerpt",knowledgeRelationType:"Extracted relation",
    knowledgeCapabilityTitle:"Capability claim: {topic}",knowledgeRelationTitle:"Extracted record about {subject}",knowledgeQuoteTitle:"Excerpt: {text}",knowledgeRecordTitle:"Extracted record #{id}",knowledgeRecordID:"Record #{id}",knowledgeOrigin:"Where this content comes from",knowledgeArticle:"Related material",knowledgeNoArticle:"No related article title or body was saved",knowledgeRelatedObject:"Related subject",knowledgeFile:"Original file",knowledgeSourceConversation:"Historical conversation extraction",knowledgeSourceStoredText:"Historical text excerpt",knowledgeSourceTask:"Task learning record",knowledgeSourceCollection:"Uploaded document collection",
    knowledgeNoArticleHelp:"Only the relation between subjects was saved. It cannot open a corresponding article.",knowledgeQuoteHelp:"The stored excerpt has no link to an article or complete file. The related subject is not an article title.",
    knowledgeSavedContent:"Stored content",knowledgeSavedSummary:"Saved summary",knowledgeCollectionHelp:"This is a collection index. View its document names and originals in the document manager.",knowledgeOpenCollection:"View collection documents",knowledgeEmptyMaterials:"No readable materials or notes in this view yet. Open the document manager, or use the separate excerpt and extraction views.",knowledgeReadNote:"Read the saved note as a document. Edits still update the same original file.",knowledgeSourceExtraction:"Historical extraction"
  });
  Object.assign(COPY.zh,{
    short:"当前对话记录",long:"其他保存的参考",organizeShort:"当前对话记录",organizeLong:"其他保存的参考",
    promptBoundary:"当前对话在任务中查看；这里管理可供后续参考的偏好、约定、资料和学习记录。",
    organizeLearningDescription:"已保存的学习笔记、任务原始参考和其他参考内容，保留真实出处与时间。",
    organizeLearningUse:"任务参考和已保存内容按问题相关性选用；原文件笔记经 AI 查阅后，通过工具结果进入后续对话。任务原文需要核对后再作为结论。",
    organizeSavedLearningNotes:"已保存的学习笔记",organizeSavedReferences:"其他保存的参考",organizeNoLearning:"还没有可显示的学习笔记或任务参考。"
  });
  Object.assign(COPY.en,{
    short:"Current conversation records",long:"Other saved references",organizeShort:"Current conversation records",organizeLong:"Other saved references",
    promptBoundary:"View conversations in Tasks. Manage preferences, rules, material and learning records for later reference here.",
    organizeLearningDescription:"Saved learning notes, original task references and other retained context, with their actual origins and dates.",
    organizeLearningUse:"Task references and saved context are selected by relevance. Reading original note files adds tool output to later turns. Review task source material before adopting conclusions.",
    organizeSavedLearningNotes:"Saved learning notes",organizeSavedReferences:"Other saved references",organizeNoLearning:"No saved learning notes or task references to show yet."
  });
  Object.assign(COPY.zh,{
    organizeAll:"全部记忆",organizeAllDescription:"查看 AI 可参考的已存内容。点一条查看原内容和出处；可编辑的记录直接修改，也可以新增自己的要求。",
    organizeAllUse:"直接带入、问题相关时选用、查阅后带入，按每条标记区分；并非每次全部发送。某次实际发送了哪些，可在下方“查看某次实际调用”中核对。",
    organizeNoAll:"还没有可显示的记忆。可以点击“新增记忆”保存明确的要求或参考内容。",
    memoryAdd:"新增记忆",memoryAddNote:"写明对象、内容和适用条件，选择范围后保存。保存后的条目按真实调用路径供 AI 参考；当前明确要求优先。",
    memoryPosition:"原始位置",memoryWhen:"什么时候进入提示词",memorySavedType:"已保存参考内容",memoryPreferenceType:"已保存偏好",memoryProjectType:"项目或全局说明",memoryTaskType:"任务原始参考",memoryKnowledgeType:"任务学习知识",memoryNoteType:"原文件学习笔记",
    memoryDirectUse:"构建上下文时直接带入，仍受当前范围和容量限制。",memoryRelevantUse:"提问时按范围和相关性选择，选中后加入本轮提示词。",memoryReadUse:"AI 查阅该内容后，作为工具结果进入后续提示词。",
    memoryArchive:"核对旧偏好记录",memoryArchiveNote:"这些旧记录尚未作为偏好使用。有明确要求的，可以核对后另存为偏好。",inputEditSource:"编辑内容",inputSaveSource:"保存修改",
    organizeTaskMaterial:"以前任务的结果与总结",organizeSavedLearningNotes:"知识与笔记",organizeSavedReferences:"已保存的记忆",organizeLesson:"任务总结",
    memoryTaskResultTitle:"任务结果：{title}",memoryTaskSummaryTitle:"任务总结：{title}",memoryTaskResultType:"以前任务的结果",memoryTaskSummaryType:"以前任务的总结",memorySummaryType:"已保存的对话摘要",
    memoryTaskResultAbout:"这是以前任务结束时保存的结果。新问题与它相关时，AI 会选取其中的内容作为参考。",
    memoryTaskSummaryAbout:"这是以前任务保存的总结；部分旧任务的总结与结果相同。新问题相关时，AI 会选取其中的内容作为参考。",
    memoryNoteAbout:"这是学习功能保存的一篇笔记，也可以在“知识库”中查看。AI 查阅笔记后，内容才进入后续提示词。",
    memorySummaryAbout:"这是以前对话保存的摘要。新问题相关时，AI 会选择其中的内容作为参考。",
    otherStorage:"旧记录核对",otherStorageNote:"这里用于核对旧记录。需要继续使用的明确要求，可以另存为记忆。",closeOldRecords:"收起旧记录",
    promptBoundary:"管理现在保存的记忆，供今后的对话参考。可以新增、修改或删除，切换对话不会切换这份清单。",
    organizeAllDescription:"选择一条查看当前内容、来源和适用范围。保存修改后，后续对话将读取更新后的内容。",
    organizeAllUse:"以后使用时，按每条记忆标明的范围和条件读取；相关性、优先级和上下文容量决定本轮采用哪些内容。",
    memoryWhen:"以后何时使用",memoryScope:"适用范围",memoryWorkspaceScope:"当前助手与工作区",memoryTaskScope:"原记录所属用户、助手与项目",promptEditNote:"保存后，AI 在后续对话中读取更新的内容。",
    organizePreferencesUse:"偏好按个人或项目范围保存，问题相关时供 AI 参考。常驻偏好优先，当前明确要求优先于历史偏好。旧提取记录不会自动作为偏好。",
    organizePreferenceAiUse:"偏好按个人或项目范围保存，问题相关时供 AI 参考。常驻偏好优先，当前明确要求优先于历史偏好。",
    organizePreferenceEditor:"选择个人通用或当前项目，保存可长期参考的明确要求。客户或 case 的适用条件请写进内容；目前还没有单独绑定客户或 case 编号。",
    organizePreferenceSteps:"有明确要求时，点击“新增偏好”。|写明适用对象、要求和条件，选择个人或项目范围后保存。|需要核对旧偏好时，展开下方入口；没有明确要求的记录可以保留。",
    promptProvider_unified_memory:"偏好、事实与摘要",promptProviderBody_unified_memory:"已保存记忆按用户、项目或工作区范围使用，再按当前问题、优先级和容量选择。这里管理长期保存的内容。"
  });
  Object.assign(COPY.en,{
    organizeAll:"All memories",organizeAllDescription:"Review stored content available to AI. Select an item to read its original content and origin, edit supported sources, or add your own requirements.",
    organizeAllUse:"Each item states whether it enters directly, by relevance, or after reading. Not every item is sent on every turn. Inspect a particular model call below to check actual inclusion.",
    organizeNoAll:"No memories to display yet. Add an explicit requirement or reference.",
    memoryAdd:"Add memory",memoryAddNote:"State who it applies to, the content and its conditions, then choose a scope. Saved content is referenced through the actual invocation path. Current instructions take priority.",
    memoryPosition:"Original location",memoryWhen:"When it enters the prompt",memorySavedType:"Saved reference",memoryPreferenceType:"Saved preference",memoryProjectType:"Project or global instructions",memoryTaskType:"Original task reference",memoryKnowledgeType:"Task learning knowledge",memoryNoteType:"Original learning-note file",
    memoryDirectUse:"Included during context construction, subject to scope and capacity.",memoryRelevantUse:"Selected by scope and relevance to the question, then added to this turn's prompt.",memoryReadUse:"After AI reads it, the tool output enters subsequent prompts.",
    memoryArchive:"Review old preference records",memoryArchiveNote:"These old records are not used as preferences. Review explicit requirements and save them as preferences if needed.",inputEditSource:"Edit content",inputSaveSource:"Save changes",
    organizeTaskMaterial:"Results and summaries from earlier tasks",organizeSavedLearningNotes:"Knowledge and notes",organizeSavedReferences:"Saved memories",organizeLesson:"Task summary",
    memoryTaskResultTitle:"Task result: {title}",memoryTaskSummaryTitle:"Task summary: {title}",memoryTaskResultType:"Earlier task result",memoryTaskSummaryType:"Earlier task summary",memorySummaryType:"Saved conversation summary",
    memoryTaskResultAbout:"This is the saved result of an earlier task. AI selects relevant parts as reference when a new question relates to it.",
    memoryTaskSummaryAbout:"This is a saved task summary. Some older tasks have identical results and summaries. AI selects relevant parts as reference for a new question.",
    memoryNoteAbout:"This note was saved through the learning feature and is also available in Knowledge. Its content enters a later prompt after AI reads it.",
    memorySummaryAbout:"This is a saved summary of an earlier conversation. AI selects relevant parts as reference for a new question.",
    otherStorage:"Review old records",otherStorageNote:"Review older records here. Save explicit requirements as memories when you want to keep using them.",closeOldRecords:"Close old records",
    promptBoundary:"Manage memories saved now for future conversations. Add, edit or delete them; switching conversations does not change this inventory.",
    organizeAllDescription:"Select an item to review its current content, source and scope. Future conversations read the updated content after you save changes.",
    organizeAllUse:"Future use follows each memory's scope and conditions. Relevance, priority and available context determine which content is selected for a turn.",
    memoryWhen:"When it will be used",memoryScope:"Applies to",memoryWorkspaceScope:"Current assistant and workspace",memoryTaskScope:"The original record's user, assistant and project",promptEditNote:"After saving, AI reads the updated content in future conversations.",
    organizePreferencesUse:"Preferences are saved for a person or project and referenced when relevant. Pinned preferences have priority, and your current instructions override historical preferences. Old extractions are not adopted automatically.",
    organizePreferenceAiUse:"Preferences are saved for a person or project and referenced when relevant. Pinned preferences have priority, and your current instructions override historical preferences.",
    organizePreferenceEditor:"Choose personal defaults or the current project and save an explicit requirement for future reference. State customer or case conditions in the content; separate IDs are not currently bound.",
    organizePreferenceSteps:"Click Add preference when you have an explicit requirement.|State who it applies to, the requirement and conditions, then choose personal or project scope and save.|Open the old-preference entry below if review is needed. Records without an explicit requirement can be left as they are.",
    promptProvider_unified_memory:"Preferences, facts and summaries",promptProviderBody_unified_memory:"Saved memories retain their user, project or workspace scope and are selected by the current question, priority and available context. This view manages persistent content."
  });
  Object.assign(COPY.zh,{
    memoryUseGuide:"AI 如何使用这些内容",memoryOriginSummary:"来源与适用范围",memoryPreferenceGuide:"如何管理偏好",memoryActiveTab:"已启用 ({count})",memoryHistoryTab:"历史归纳 ({count})",memoryHistoryNote:"这些是以前提取的偏好，尚未作为你的偏好使用。请核对对象和要求，再保存需要沿用的内容。",memoryHistoryCount:"{count} 条历史归纳 · 尚未启用",memoryHistoryEmpty:"没有找到可展示的历史偏好。",memorySavedQuote:"保存的原话或摘录",memoryContentTruncated:"内容较长，当前显示前 8,000 字符。",memoryRecallSummary:"AI 回想时参考的摘要",memoryEditSummary:"编辑回想摘要",
    organizeTasks:"任务结果与总结",organizeTasksDescription:"以前任务保存的结果与总结。相同任务的重复内容合并显示，可以查看原任务出处。",organizeTasksUse:"新问题相关时，按用户、助手和项目范围选择其中的内容，加入提示词作为参考。",organizeNoTasks:"还没有保存的任务结果与总结。",
    promptBoundary:"管理保存的内容，供以后的对话参考。",organizeAllDescription:"查看具体内容，编辑记忆，追溯来源。",organizePreferencesDescription:"已启用的偏好与历史归纳分开查看。",organizeKnowledgeDescription:"查看学习笔记、保存的文字和提取内容。",knowledgeMore:"来源筛选",knowledgeRelations:"提取的内容",organizeClaim:"保存的具体内容"
  });
  Object.assign(COPY.en,{
    memoryUseGuide:"How AI uses this content",memoryOriginSummary:"Source and scope",memoryPreferenceGuide:"Managing preferences",memoryActiveTab:"Enabled ({count})",memoryHistoryTab:"Historical ({count})",memoryHistoryNote:"These earlier extractions are not enabled as your preferences. Review who and what they concern before saving requirements you want to keep.",memoryHistoryCount:"{count} historical extractions · Not enabled",memoryHistoryEmpty:"No historical preferences to display.",memorySavedQuote:"Saved quotation or excerpt",memoryContentTruncated:"Showing the first 8,000 characters of this longer record.",memoryRecallSummary:"Summary used for recall",memoryEditSummary:"Edit recall summary",
    organizeTasks:"Task results & summaries",organizeTasksDescription:"Saved results and summaries from earlier tasks. Duplicate content from the same task is grouped with its original source.",organizeTasksUse:"Relevant content is selected within its user, assistant and project scope, then added to the prompt as reference.",organizeNoTasks:"No saved task results or summaries yet.",
    promptBoundary:"Manage saved content for future conversations.",organizeAllDescription:"Read content, edit memories and trace their sources.",organizePreferencesDescription:"Review enabled preferences separately from earlier extractions.",organizeKnowledgeDescription:"Read learning notes, saved text and extracted content.",knowledgeMore:"Filter sources",knowledgeRelations:"Extracted content",organizeClaim:"Saved content"
  });
  Object.assign(COPY.zh,{memoryHistoricalMaterial:"查看历史资料",memoryHistoricalMaterialNote:"这里保留旧文字摘录和自动提取记录；它们不是完整文章，尚未作为已确认知识使用。"});
  Object.assign(COPY.en,{memoryHistoricalMaterial:"Browse historical material",memoryHistoricalMaterialNote:"Earlier excerpts and extractions are retained here. They are not full articles or adopted as verified knowledge."});
  Object.assign(COPY.zh,{organizeTasks:"任务结果与摘要",organizeTaskMaterial:"以前任务的结果与摘要",memoryTaskSummaries:"摘要 ({count})",memoryTaskResults:"结果 ({count})",memoryTaskAll:"全部 ({count})",memoryRepeatedSummary:"与任务结果相同，未保存独立总结。",memoryExtractedSummary:"这是任务结果的摘录，未保存独立总结。",memoryTaskSummaryTitle:"任务摘要：{title}",memoryTaskSummaryType:"以前任务的摘要",organizeTasksDescription:"默认查看保存的任务摘要，也可以切换到完整结果。结果摘录会单独标明。"});
  Object.assign(COPY.en,{organizeTasks:"Task results & summaries",memoryTaskSummaries:"Summaries ({count})",memoryTaskResults:"Results ({count})",memoryTaskAll:"All ({count})",memoryRepeatedSummary:"Identical to the task result; no separate synthesis was saved.",memoryExtractedSummary:"This is an excerpt of the task result; no separate synthesis was saved.",organizeTasksDescription:"Saved summaries are shown first. Switch to full results; extracted text is marked explicitly."});
  Object.assign(COPY.zh,{organizeNoTasks:"暂无符合自动参考条件的任务记录。原始对话仍可在“全部任务”中查看。",organizeTasksDescription:"有执行记录、明确反馈或失败原因的历史任务，供以后相关问题参考。普通聊天仍保留在任务历史中。",memoryTaskHelp:"点击记录查看内容。可停止自动参考并保留原文，也可编辑原记录；值得长期保留的要求可用“新增记忆”保存。摘要只展示独立内容，不重复列出回答摘句。"});
  Object.assign(COPY.en,{organizeNoTasks:"No task records currently qualify for automatic recall. Original conversations remain available in All tasks.",organizeTasksDescription:"Earlier tasks with execution evidence, explicit feedback or failure details, available for relevant future questions. Ordinary chats remain in task history.",memoryTaskHelp:"Select a record to read it, pause automatic recall or edit the original. Use Add memory for a lasting requirement. Summaries contain independent content, without repeating answer excerpts."});
  function language() { return String(window.openKittyLanguage || document.documentElement.lang || "zh").toLowerCase().startsWith("en") ? "en" : "zh"; }
  function t(key, values) { let text = COPY[language()][key] || key; if (values) for (const [name,value] of Object.entries(values)) text = text.replaceAll("{"+name+"}", String(value)); return text; }
  function element(tag, cls, text) { const item = document.createElement(tag); if (cls) item.className = cls; if (text !== undefined) item.textContent = String(text); return item; }
  function button(label, handler, cls = "") { const item = element("button", cls, label); item.type = "button"; item.addEventListener("click", handler); return item; }
  Object.assign(COPY.zh,{
    memoryActiveTab:"已保存的偏好 ({count})",memoryHistoryTab:"待你确认 ({count})",
    memoryHistoryCount:"{count} 项内容可核对后保存",organizeActivePreferences:"{count} 项偏好可供以后参考",
    organizePreferencesDescription:"保存你希望 AI 以后遵循的语言、格式和办事要求。",
    memoryHistoryNote:"这些是 AI 提取出的说法，可能有误。认可的内容请点“保存为以后使用的偏好”。保存成功后，相关 Agent 任务会自动参考，无须另外启动。",
    organizePreferenceAiUse:"保存后就可供以后的 Agent 任务参考，无须另外启动。AI 只在问题相关时选用，本次明确要求优先；简单问答不使用长期偏好。",
    organizePreferenceSteps:"查看具体要求和用户原话；没有原话时，请按你现在的意愿判断。|点击“保存为以后使用的偏好”，修改成你认可的完整要求。|保存后在“已保存的偏好”查看，随时可以修改或删除。",
    organizeConfirmPreference:"保存为以后使用的偏好",organizePreferenceEditor:"确认并修改为你希望以后执行的要求，再保存。保存后进入“已保存的偏好”，以后相关 Agent 任务可参考；客户或项目条件请写在内容中。",
    preferenceReviewTitle:"确认以后使用的偏好",preferenceSave:"保存，供以后参考",preferenceConfirmUse:"确认，供以后参考",preferenceEdit:"修改偏好",preferenceDelete:"删除偏好",
    preferenceSavedUse:"已保存。以后相关 Agent 任务可参考这条偏好，无须再启动；可以在这里修改或删除。",
    preferenceOldClaim:"旧记录提到的要求",preferenceClaimTitle:"旧记录认为：{claim}",preferenceReviewUse:"认可并保存后才供以后参考；不适合的内容可以跳过。",
    preferenceReviewBadge:"需要你确认",preferenceCandidateBadge:"AI 提取 · 待确认",preferenceSaveCandidate:"保存修改，仍待确认",preferenceCandidateEditor:"保存修改后仍待确认。回到记录点击“确认，供以后参考”，才会用于以后的相关 Agent 任务。",
    preferenceCandidateSaved:"已保存修改，仍待确认。请在“待你确认”中点击“确认，供以后参考”。",
    preferenceRequirement:"具体要求",preferenceBasis:"这个说法的依据",preferenceManualBasis:"由你明确填写或修改，依据是你保存的这条要求。",preferenceUnknownBasis:"没有找到可展示的用户原话，请核对这条要求。",
    preferenceUnverifiedQuote:"旧记录保存的摘录（尚未核实是否是对应用户原话）",preferenceQuoteLoading:"正在读取这条偏好的依据…",preferenceBasisUnavailable:"本页没有读取到这条偏好的依据，请刷新或在原记录中核对。",
    preferenceMissingQuote:"旧记录没有保存对应的用户原话，无法证明你曾这样要求过。请改成你现在认可的要求后再保存。",
    preferenceOriginal:"核对保存前的依据",preferenceOriginalClaim:"旧提取内容",preferenceOriginalQuote:"对应用户原话",preferenceNoQuote:"没有找到对应用户原话",
    preferenceArchiveToggle:"查看已失效或尚未到日期的旧记录 ({count})",preferenceArchiveHelp:"这些旧记录保留在原库中，默认收起。即使现在重新认可，也需要另存成当前要求。",
    preferenceOriginalCount:"重复内容合并展示，保留 {count} 条原始记录。",preferenceAIExcluded:"已过滤 {count} 条助手自身的描述。",
    preferenceConversation:"原始对话",preferenceConversationButton:"查看原始对话",preferenceConversationMissing:"旧提取记录没有保存具体会话编号，无法确定来自哪次对话。这不代表原始对话被删除，可以到“全部任务”查找。",
    preferenceConversationLinked:"已找到关联会话，下面直接加载其中的用户原话，供你核对。",preferenceLinkedQuoteNote:"旧提取没有标记具体依据是哪一句；原始对话可以通过会话编号找回。",preferenceConversationScope:"当前页没有可供查看的原始对话关联。",preferenceConversationHistory:"查看全部任务",preferenceConversationManual:"这条是你现在填写或修改的要求，不要求有一段历史对话作为依据。",
    preferencePreviewStats:"已加载 {count} 条用户发言（本次读取 {messages} 条对话）。",preferencePreviewPartial:"仅加载了最近的部分消息，查看完整对话可继续读取。",preferencePreviewEmpty:"已读取关联会话，本次读取的消息中没有用户发言。",
    preferenceConversationReference:"会话编号：{id}",preferenceConversationNote:"历史原文 · 用于核对偏好依据",preferenceConversationPartial:"显示最近 {count} 条，共 {total} 条。",preferenceConversationMore:"读取更多对话",preferenceConversationEmpty:"这个会话当前没有可读取的消息。",preferenceConversationLimit:"已达到本页 2000 条消息的读取上限，更早内容请在“全部任务”查看。",conversationMessageCount:"{count} 条消息",
    organizeFactMissingEvidence:"没有找到对应用户原话",organizeNoPreferences:"还没有保存的偏好。可以新增自己的要求，或核对旧记录后保存。"
  });
  Object.assign(COPY.en,{
    memoryActiveTab:"Saved preferences ({count})",memoryHistoryTab:"Review and save ({count})",
    memoryHistoryCount:"{count} requirements to review before saving",organizeActivePreferences:"{count} preferences available for future tasks",
    organizePreferencesDescription:"Save language, format and work requirements you want AI to follow in future.",
    memoryHistoryNote:"These AI extractions may be wrong. Choose “Save as a future preference” for requirements you agree with. Once saved, relevant Agent tasks can recall them; no separate activation is needed.",
    organizePreferenceAiUse:"Saved preferences are available to future Agent tasks without separate activation. Selection depends on relevance; current instructions take priority. Direct answers do not use long-term preferences.",
    organizePreferenceSteps:"Read the requirement and user quote; without a quote, decide what you want now.|Choose “Save as a future preference” and edit it into a complete requirement.|Find it under Saved preferences, where you can edit or delete it.",
    organizeConfirmPreference:"Save as a future preference",organizePreferenceEditor:"Edit this into a requirement you want followed, then save. It will appear under Saved preferences and be available to relevant Agent tasks. Include customer or project conditions in the text.",
    preferenceReviewTitle:"Confirm a future preference",preferenceSave:"Save for future tasks",preferenceConfirmUse:"Confirm for future tasks",preferenceEdit:"Edit preference",preferenceDelete:"Delete preference",
    preferenceSavedUse:"Saved for relevant future Agent tasks. No separate activation is needed; you can edit or delete it here.",
    preferenceOldClaim:"Requirement in the old record",preferenceClaimTitle:"Earlier extraction: {claim}",preferenceReviewUse:"Review and save before using in future tasks. You can skip requirements you do not want.",
    preferenceReviewBadge:"Needs your review",preferenceCandidateBadge:"AI extraction · Needs confirmation",preferenceSaveCandidate:"Save edits; confirmation still needed",preferenceCandidateEditor:"Saving edits keeps this pending. Return to the record and choose “Confirm for future tasks” before relevant Agent tasks can use it.",
    preferenceCandidateSaved:"Edits saved; confirmation still needed. Choose “Confirm for future tasks” under Review and save.",
    preferenceRequirement:"Specific requirement",preferenceBasis:"Evidence for this requirement",preferenceManualBasis:"You explicitly entered or edited this. The saved requirement is your instruction.",preferenceUnknownBasis:"No readable user quote was found. Please review this requirement.",
    preferenceUnverifiedQuote:"Excerpt saved in the old record (not verified as the matching user quote)",preferenceQuoteLoading:"Loading the evidence for this preference…",preferenceBasisUnavailable:"The evidence for this preference was not loaded on this page. Refresh or check the original record.",
    preferenceMissingQuote:"The old record has no matching user quote, so it cannot prove you asked for this. Edit it into a requirement you agree with now before saving.",
    preferenceOriginal:"Evidence to review before saving",preferenceOriginalClaim:"Earlier extraction",preferenceOriginalQuote:"Matching user quote",preferenceNoQuote:"No matching user quote found",
    preferenceArchiveToggle:"Earlier records outside their recorded dates ({count})",preferenceArchiveHelp:"These originals stay in the database and are collapsed by default. Save a new requirement if you want to use one now.",
    preferenceOriginalCount:"Duplicates are grouped; {count} original records are retained.",preferenceAIExcluded:"Filtered {count} descriptions of the assistant itself.",
    preferenceConversation:"Original conversation",preferenceConversationButton:"View original conversation",preferenceConversationMissing:"The old extraction did not save a conversation ID, so its original conversation cannot be identified. This does not mean the conversation was deleted. Search All tasks to find it.",
    preferenceConversationLinked:"A linked conversation was found. Its user messages are loaded below for you to review.",preferenceLinkedQuoteNote:"The old extraction did not identify the exact supporting message. The original conversation can be retrieved using its ID.",preferenceConversationScope:"No original conversation link is available on this page.",preferenceConversationHistory:"Browse all tasks",preferenceConversationManual:"This is a requirement you entered or edited now; it does not need a historical conversation as evidence.",
    preferencePreviewStats:"Loaded {count} user messages from {messages} conversation messages.",preferencePreviewPartial:"Only the most recent messages were loaded. Open the full conversation to load more.",preferencePreviewEmpty:"The linked conversation was loaded; this set contains no user messages.",
    preferenceConversationReference:"Conversation ID: {id}",preferenceConversationNote:"Original messages · Review the evidence for this preference",preferenceConversationPartial:"Showing the latest {count} of {total} messages.",preferenceConversationMore:"Load more messages",preferenceConversationEmpty:"No messages are currently available for this conversation.",preferenceConversationLimit:"This view has reached its 2,000-message limit. Find earlier messages in All tasks.",conversationMessageCount:"{count} messages",
    organizeFactMissingEvidence:"No matching user quote found",organizeNoPreferences:"No saved preferences yet. Add your own requirement or review an earlier extraction before saving."
  });
  Object.assign(COPY.zh,{
    conversationPreviewAll:"完整对话 ({count})",conversationPreviewUser:"仅用户原话 ({count})",conversationPreviewStats:"已加载 {count} 条消息，包含用户和 AI 的原始交流。",
    preferenceConversationLinked:"已找回关联会话。下方默认显示完整交流，核对偏好时可切换到“仅用户原话”。",
    memoryGuideIntro:"你在这里保存和确认记忆，AI 在以后办事时根据任务需要参考。可以把它理解为：先留下经验，再在合适的时候翻出来用。",
    memoryGuideSaveTitle:"保存或确认记忆",memoryGuideSaveBody:"写下以后要沿用的要求，或核对并确认 AI 归纳出的内容。",
    memoryGuideTaskTitle:"开始 Agent 任务",memoryGuideTaskBody:"在对应助手和工作区，让 AI 开始一项新任务。",
    memoryGuideFindTitle:"查找相关内容",memoryGuideFindBody:"AI 挑选适用的要求和相似经验；需要资料时，再查阅笔记或知识库。",
    memoryGuideUseTitle:"带着背景处理",memoryGuideUseBody:"选中的内容和你本次的要求一起交给模型，帮助它接着已有经验办事。",
    memoryGuideExampleTitle:"举个例子",memoryGuideExample:"你保存“给客户甲的报告默认用中文”。下次让 AI 给客户甲写报告，它可以找到这条要求，将它作为背景参考；如果这次你明确要求英文，就以本次要求为准。",
    memoryGuideBoundary:"这套长期记忆流程适用于 Agent 模式；简单问答不使用这里的长期记忆。待确认的归纳需先确认，保存的内容也只在适用范围和容量内参考。",
    memoryGuideSources:"每类内容的使用路径",memoryGuideTechnical:"技术说明（供开发者查阅）",memoryGuideCount:"当前可查看 {count} 条",memoryGuideEmpty:"目前没有可查看的记录",memoryGuideUnavailable:"暂时无法读取这类内容",memoryGuideUnknown:"使用方式以这条记录的“出处与进入方式”为准。",
    memoryGuideMode_direct:"开始任务时读取",memoryGuideMode_relevant:"与任务相关时参考",memoryGuideMode_on_demand:"需要时查阅",
    promptProvider_project_instructions:"项目说明与全局规则",promptProviderBody_project_instructions:"这里保存的是办事规则，比如报告格式和项目约定。AI 在对应工作区开始任务时，先读取这些说明，再处理你的请求。",
    memoryGuidePath_project_instructions:"你维护项目说明|在这个工作区开始任务|AI 先读规则，再办事",
    promptProvider_unified_memory:"已保存的偏好、事实与总结",promptProviderBody_unified_memory:"你保存或确认的要求和经验，可以供以后使用。AI 根据当前任务挑选相关内容；新要求优先，已删除的条目不会继续作为这类记忆使用。",
    memoryGuidePath_unified_memory:"你新增或确认记忆|AI 查找相关要求和经验|选中的内容成为背景参考",
    promptProvider_web_history:"历史任务的结果与经验",promptProviderBody_web_history:"过去任务留下的结果、执行记录、反馈和总结，帮助 AI 参考做过的事。只有与当前问题相关、来源可追溯的部分才会选用。",
    memoryGuidePath_web_history:"以前任务留下结果和反馈|新任务与以前相似|AI 参考有用的经验",
    promptProvider_tool_learning:"AI 自己积累的学习笔记",promptProviderBody_tool_learning:"AI 将经验保存成笔记，你可以在知识库里查看。任务需要这些知识时，AI 先查阅笔记，再把查到的内容作为参考。",
    memoryGuidePath_tool_learning:"AI 把经验保存成笔记|任务需要时查阅|查到的内容用于本次处理",
    promptProvider_session_history:"当前对话的上下文",promptProviderBody_session_history:"用来记住你在这次聊天里刚刚说过什么，方便连续交流。它和跨任务的长期记忆分别使用。",memoryGuidePath_session_history:"你继续当前聊天|AI 读取前面的交流|接着前文处理问题"
  });
  Object.assign(COPY.en,{
    conversationPreviewAll:"Full conversation ({count})",conversationPreviewUser:"User messages only ({count})",conversationPreviewStats:"Loaded {count} original messages from the user and AI.",
    preferenceConversationLinked:"The linked conversation was retrieved. Full messages appear below; switch to user messages only when reviewing a preference.",
    memoryGuideIntro:"Save and confirm memories here. When AI handles a future task, it can look back at relevant requirements and experience.",
    memoryGuideSaveTitle:"Save or confirm",memoryGuideSaveBody:"Write a requirement to keep using, or review and confirm an AI extraction.",
    memoryGuideTaskTitle:"Start an Agent task",memoryGuideTaskBody:"Start a new task with the appropriate assistant and workspace.",
    memoryGuideFindTitle:"Find relevant context",memoryGuideFindBody:"AI selects applicable requirements and experience, and consults notes or knowledge when needed.",
    memoryGuideUseTitle:"Work with context",memoryGuideUseBody:"Selected content accompanies your current request so the model can build on previous experience.",
    memoryGuideExampleTitle:"An example",memoryGuideExample:"Save “Use Chinese for customer A’s reports by default.” When you ask for another report for customer A, AI can find this requirement and use it as context. If you explicitly ask for English this time, your current instruction takes priority.",
    memoryGuideBoundary:"Long-term memory applies to Agent mode. Direct answers do not use these memories. Pending extractions require confirmation; saved content remains subject to its scope and available context.",
    memoryGuideSources:"How each type is used",memoryGuideTechnical:"Technical details for developers",memoryGuideCount:"{count} records available to view",memoryGuideEmpty:"No records available to view yet",memoryGuideUnavailable:"This source cannot be read right now",memoryGuideUnknown:"See this record’s source and context-use explanation.",
    memoryGuideMode_direct:"Read when starting a task",memoryGuideMode_relevant:"Used when relevant",memoryGuideMode_on_demand:"Consulted when needed",
    promptProvider_project_instructions:"Project instructions and shared rules",promptProviderBody_project_instructions:"Working rules such as report formats and project conventions are read before AI handles a task in the corresponding workspace.",memoryGuidePath_project_instructions:"Maintain project instructions|Start a task in this workspace|AI reads the rules before working",
    promptProvider_unified_memory:"Saved preferences, facts and summaries",promptProviderBody_unified_memory:"Saved or confirmed requirements and experience are available for future tasks. AI selects relevant content; current instructions take priority and deleted entries are excluded.",memoryGuidePath_unified_memory:"Add or confirm a memory|AI finds relevant requirements and experience|Selected content becomes task context",
    promptProvider_web_history:"Previous task results and experience",promptProviderBody_web_history:"Earlier results, execution records, feedback and summaries help AI learn from completed work. Relevant, traceable material can be selected for a new task.",memoryGuidePath_web_history:"Earlier work leaves results and feedback|A new task is related|AI consults useful experience",
    promptProvider_tool_learning:"AI learning notes",promptProviderBody_tool_learning:"AI saves lessons in notes that you can read in the knowledge library. When a task needs this knowledge, AI consults the notes and uses what it finds as context.",memoryGuidePath_tool_learning:"AI saves lessons in notes|Consult notes when needed|Use retrieved content for the current task",
    promptProvider_session_history:"Current conversation context",promptProviderBody_session_history:"Earlier messages in this conversation support continuous discussion. They are used separately from memory shared across tasks.",memoryGuidePath_session_history:"Continue the current chat|AI reads earlier exchanges|Handle the next request in context"
  });
  function dateLabel(value) {
    if (!value || String(value).startsWith("0001-")) return t("unknownTime");
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? t("unknownTime") : date.toLocaleString(language()==="en" ? "en-GB" : "zh-CN", {year:"numeric",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",second:"2-digit",hour12:false});
  }
  function notice(text, error = false) { clearTimeout(state.noticeTimer); $("message").textContent = text; $("message").classList.toggle("error",error); if (text && !error) state.noticeTimer = setTimeout(()=>notice(""),4500); }
  async function responseJSON(response) {
    const data = await response.json().catch(()=>({}));
    if (!response.ok) { const error = new Error(response.status===409 && /version|Memory changed/i.test(data.error || "") ? t("conflict") : data.error || t("requestFailed")); error.status=response.status; throw error; }
    return data;
  }
  function recordID(item) { return item._type+":"+item.id; }
  function recordTitle(item) { return item._type==="history" ? item.name || item.id : item._type==="knowledge" ? t("knowledgeTitle") : item.key; }
  function writable(item) { return item && item._type==="memory" && !item.read_only && (item.scope==="global" || !item.agent_name || item.agent_name===state.agent) && Number.isInteger(item.version) && item.version>0; }
  function scopeLabel(scope) { return t(({user:"personal",project:"project",global:"global",session:"short"})[scope] || "long"); }
  function sourceLabel(source) {
    const names={user_explicit:"sourceExplicit",service:"sourceAgent",legacy:"sourceLegacy",agent_candidate:"sourceCandidate",session_working:"sourceSession",session_context:"sourceContext",summary:"sourceSummary",history:"sourceHistory",knowledge:"sourceKnowledge",task_execution:"sourceExecution",task_insight:"sourceInsight",user_feedback:"sourceFeedback",user_statement:"sourceStatement",knowledge_fact:"sourceKnowledgeFact",conversation:"sourceConversation",legacy_preference:"sourceLegacyPreference",learning_knowledge:"sourceLearningKnowledge",workspace_instructions:"sourceWorkspaceInstructions",agent_memory:"sourceAgentMemory",user_profile_constraint:"sourceProfileConstraint",knowledge_graph_legacy:"sourceGraphLegacy",knowledge_graph_verbatim_legacy:"sourceGraphQuotation",knowledge_graph_fact:"sourceGraphFact",rag_knowledge:"sourceRagKnowledge"};
    return names[source] ? t(names[source]) : source || t("unknownTime");
  }
  const catalogKinds={events:"catalogEvents",profiles:"catalogProfiles",knowledge:"catalogKnowledge",tool_usage:"catalogToolUsage",entries:"catalogEntries",summaries:"catalogSummaries",session_context:"catalogContextSource",knowledge_graph:"catalogGraph",learn_files:"catalogLearnFiles",rag:"catalogRag"};
  function catalogRecall(source) {
    const recall=source.recall || {};
    if(recall.mode==="automatic")return t(recall.enabled===true?"catalogAutomatic":recall.enabled===false?"catalogAutomaticOff":"catalogAutomaticCheck");
    if(recall.mode==="on_demand")return t(recall.enabled===true?"catalogOnDemand":recall.enabled===false?"catalogOnDemandOff":"catalogOnDemandCheck");
    return t("catalogNotConnected");
  }
  function renderCatalog() {
    const steps=$("recollection-steps");steps.replaceChildren(...[["stepSave","stepSaveBody"],["stepRecall","stepRecallBody"],["stepPrompt","stepPromptBody"]].map(([title,body],index)=>{const item=element("div","recollection-step");item.append(element("strong","",(index+1)+" · "+t(title)),element("p","",t(body)));return item;}));
    const output=$("catalog-sources"),opened=new Set([...output.querySelectorAll("[data-catalog-source]")].filter(item=>item.querySelector("details")?.open).map(item=>item.dataset.catalogSource));
    $("catalog-summary").textContent="";$("catalog-context").textContent="";
    if(state.catalogError) {output.replaceChildren(empty(t("catalogFailure"),"",t("retry"),()=>loadCatalog(true)));return;}
    if(!state.catalog || state.catalogAgent!==state.agent) {output.replaceChildren(element("p","catalog-context",t(state.catalogPendingAgent?"loading":"catalogNotLoaded")));return;}
    const data=state.catalog,sources=data.sources.filter(source=>source&&Object.hasOwn(catalogKinds,source.id));
    $("catalog-summary").textContent=t("catalogSummary",{count:sources.length});
    const context=data.sessions_verified;
    $("catalog-context").textContent=Number.isSafeInteger(context)&&context>=0?t("catalogContext",{agent:state.agent,count:context.toLocaleString()}):t("agent")+" · "+state.agent;
    output.replaceChildren();
    for(const source of sources) {
      const key=catalogKinds[source.id],row=element("article","catalog-source");row.dataset.catalogSource=source.id;
      const header=element("div","catalog-source-heading");header.append(element("h4","",t(key)),element("span","catalog-recall",catalogRecall(source)));row.append(header,element("p","catalog-source-purpose",t(key+"Body")));
      const stats=element("div","catalog-source-stats"),counts=source.counts || {};
      const unit=t(({records:"unitRecords",calls:"unitCalls",items:"unitItems",files:"unitFiles"})[counts.unit] || "unitItems");
      if(!Number.isSafeInteger(counts.verified)||counts.verified<0)stats.append(element("span","",t("catalogCountUnknown")));
      for(const [field,label] of [["verified","catalogVerified"],["unverified","catalogUnverified"]])if(Number.isSafeInteger(counts[field])&&counts[field]>=0)stats.append(element("span","",t(label,{count:counts[field].toLocaleString(),unit})));
      if(counts.bounded===true)stats.append(element("span","catalog-query-bound",t("catalogBounded")));
      row.append(stats);
      const status=({ready:"catalogReady",disabled:"catalogDisabled",unverified:"catalogUnverifiedStatus",unavailable:"catalogUnavailable"})[source.status];
      if(status&&source.status!=="ready")row.append(element("p","catalog-source-note",t(status)));
      const codes=Array.isArray(source.note_codes)?source.note_codes:[];
      const noteKeys=[...new Set(codes)].filter(code=>typeof code==="string"&&Object.hasOwn(COPY[language()],"catalogNote_"+code));
      const critical=noteKeys.find(code=>["profile_scope_unverified","legacy_context_archive","workspace_knowledge_archive","rag_workspace_material","incomplete_ownership","storage_unavailable","table_missing","kg_evidence_required","memory_disabled"].includes(code));
      if(critical)row.append(element("p","catalog-source-note",t("catalogNote_"+critical)));
      const storage=source.storage || {},locations=element("details","catalog-positions");locations.open=opened.has(source.id);locations.append(element("summary","",t("catalogPositions")));const body=element("div","catalog-position-body");
      const tables=Array.isArray(storage.tables)?storage.tables.filter(item=>typeof item==="string"):[],paths=Array.isArray(storage.paths)?storage.paths.filter(item=>typeof item==="string"):[];
      if(tables.length){body.append(element("span","catalog-position-label",t("catalogDatabase")+(storage.kind==="sqlite"?" · SQLite":storage.kind==="mysql"?" · MySQL":"")));for(const table of tables)body.append(element("code","",table));}
      if(paths.length){body.append(element("span","catalog-position-label",t("catalogPaths")));for(const path of paths)body.append(element("code","",path));}
      if(!tables.length&&!paths.length)body.append(element("span","",t("catalogLocationUnknown")));
      const tools=Array.isArray(source.recall?.tools)?source.recall.tools.filter(tool=>typeof tool==="string"):[];
      if(tools.length){body.append(element("span","catalog-position-label",t("catalogTools")));for(const tool of tools)body.append(element("code","",tool));}
      if(noteKeys.length){body.append(element("span","catalog-position-label",t("catalogRules")));for(const code of noteKeys)body.append(element("p","catalog-source-note",t("catalogNote_"+code)));}
      if(source.recall?.requires_runtime_check===true)body.append(element("p","catalog-source-note",t("catalogRuntimeCheck")));
      locations.append(body);row.append(locations);output.append(row);
    }
    if(!sources.length)output.append(element("p","catalog-context",t("catalogNoSources")));
  }
  async function loadCatalog(force=false) {
    const agent=state.agent;if(!agent)return;
    if(!force&&(state.catalogAgent===agent&&state.catalog || state.catalogPendingAgent===agent))return;
    const seq=++state.catalogSeq;state.catalogController?.abort();state.catalogController=new AbortController();state.catalogPendingAgent=agent;state.catalogError=false;renderCatalog();
    try {
      const data=await responseJSON(await fetch("/api/memory/catalog?"+new URLSearchParams({agent}),{cache:"no-store",signal:state.catalogController.signal}));
      if(seq!==state.catalogSeq||state.agent!==agent)return;
      if(!Array.isArray(data.sources)||data.agent!==agent)throw new Error(t("catalogNoSources"));
      state.catalog=data;state.catalogAgent=agent;state.catalogError=false;
    } catch(error){if(seq===state.catalogSeq&&error.name!=="AbortError"){state.catalog=null;state.catalogAgent="";state.catalogError=true;}}
    finally{if(seq===state.catalogSeq){state.catalogPendingAgent="";renderCatalog();}}
  }
  function invalidateCatalog() {state.catalogSeq++;state.catalogController?.abort();state.catalog=null;state.catalogAgent="";state.catalogPendingAgent="";state.catalogError=false;}
  function empty(title, text, action, handler) { const item=element("div","empty"); item.append(element("h3","",title),element("p","",text)); if(action)item.append(button(action,handler)); return item; }
  function filtered() { return Boolean($("search").value.trim() || $("state").value || $("scope").value || state.category==="knowledge"&&$("knowledge-source").value!=="curated"); }
  function createAllowed() { return !["knowledge","history"].includes(state.category) && state.loaded && !!state.agent && (state.category!=="short" || !!state.session); }
  function countLabel() {
    if(state.category==="knowledge")return state.totalKnown?t("knowledgeCount",{count:state.total}):t("knowledgeCountUnknown");
    const sources=state.items.filter(item=>item._type==="supplemental").length;
    if(!state.totalKnown)return t(sources?"savedSourcesUnknown":"countUnknown",{sources});
    return sources?t("savedSources",{count:state.total,sources}):t("count",{count:state.total});
  }
  function setBusy(busy) {
    state.busy=busy;
    $("agent").disabled=busy || !state.agents.length; $("session").disabled=busy || !state.sessions.length;
    $("new-memory").disabled=busy || !createAllowed();
    for(const id of ["editor-save","editor-close","editor-cancel","refresh","previous","next"]) $(id).disabled=busy || ((id==="previous" && state.offset===0) || (id==="next" && state.offset+pageSize>=state.total));
    document.querySelectorAll("[data-category]").forEach(item=>item.disabled=busy);
    document.querySelectorAll(".memory-card,#memory-detail .actions button,#memory-detail .forget-confirm button").forEach(item=>item.disabled=busy || !state.loaded);
    renderPromptControls();
    document.dispatchEvent(new CustomEvent("macs:memory-statechange",{detail:{saving:busy}}));
  }
  function updateControls() {
    const viewNotes=state.viewNotes.filter(code=>Object.hasOwn(COPY[language()],"viewNote_"+code));$("view-notes").hidden=!viewNotes.length;$("view-notes").textContent=viewNotes.map(code=>t("viewNote_"+code)).join(" ");
    $("view-title").textContent=t(state.category); $("view-description").textContent=t(state.category+"Description");
    document.querySelectorAll("[data-category]").forEach(item=>{ if(item.dataset.category===state.category)item.setAttribute("aria-current","page");else item.removeAttribute("aria-current"); });
    $("session-label").hidden=state.category!=="short";
    $("scope-label").hidden=true;
    $("state-label").hidden=["knowledge","history"].includes(state.category);
    $("search-form").hidden=false;
    $("search-form").querySelector(".search-label").hidden=false;
    $("knowledge-source-label").hidden=state.category!=="knowledge";$("manage-knowledge").hidden=state.category!=="knowledge";
    $("new-memory").hidden=["knowledge","history"].includes(state.category);
    $("clear-filters").hidden=!filtered();
    $("recall-panel").hidden=["short","knowledge","history"].includes(state.category);
    $("export").hidden=["short","knowledge","history"].includes(state.category);
    const exportParams=new URLSearchParams({agent:state.agent}); if(state.category==="global")exportParams.set("scope","global");
    $("export").href="/api/memory/export?"+exportParams; $("export").setAttribute("aria-disabled",state.loaded&&state.agent?"false":"true");
    setBusy(state.busy);
  }
  function updateMetadata(data) {
    state.metadataLoaded=true;
    state.agent=data.agent || state.agent;
    if(Array.isArray(data.agents))state.agents=data.agents;
    if(Array.isArray(data.sessions))state.sessions=data.sessions;
    $("agent").replaceChildren(...state.agents.map(name=>{const item=element("option","",name);item.value=name;return item;})); $("agent").value=state.agent;
    if(!state.sessions.some(item=>item.id===state.session))state.session=state.sessions[0]?.id || "";
    $("session").replaceChildren(...state.sessions.map(session=>{const item=element("option","",session.name || session.id);item.value=session.id;return item;})); $("session").value=state.session;
    const url=new URL(location.href); if(state.agent)url.searchParams.set("agent",state.agent); url.searchParams.set("view",state.category);
    if(state.category==="knowledge")url.searchParams.set("knowledge_source",$("knowledge-source").value);else url.searchParams.delete("knowledge_source");
    if(state.session)url.searchParams.set("session_id",state.session);else url.searchParams.delete("session_id");
    history.replaceState(null,"",url);
    if($("memory-catalog").open)loadCatalog();else renderCatalog();
  }
  function renderCards() {
    entries.replaceChildren();
    if(state.category==="preferences")entries.append(element("p","classification-note",t("styleExplanation")));
    const archiveItem=item=>["legacy_preference","knowledge_graph_legacy","knowledge_graph_verbatim_legacy","knowledge_graph_fact"].includes(item.source_type);
    const currentItems=state.items.filter(item=>!archiveItem(item)),archived=state.items.filter(archiveItem);
    if(!currentItems.some(item=>recordID(item)===state.selected)&&!(state.archiveOpen&&archived.some(item=>recordID(item)===state.selected)))state.selected=currentItems.length?recordID(currentItems[0]):state.archiveOpen&&archived.length?recordID(archived[0]):"";
    detail.hidden=!state.selected;$("memory-browser").classList.toggle("empty-browser",!state.selected);
    if(!currentItems.length&&!(state.archiveOpen&&archived.length)) {
      const noTask=state.category==="short" && !state.session;
      const key=filtered()?"noMatch":state.category==="knowledge"?"emptyTrusted":state.category==="short"?(noTask?"noSession":"noShort"):state.category==="history"?"noHistory":"empty";
      const explanation=key==="emptyTrusted"?"emptyTrustedBody":key==="empty"?"empty"+state.category.charAt(0).toUpperCase()+state.category.slice(1):key==="noMatch"?"noMatchBody":key==="noSession"?"noSessionBody":key==="noShort"?"noShortBody":"noHistoryBody";
      const box=empty(t(key),t(COPY.zh[explanation]?explanation:"emptyDescription"),filtered()?t("clearFilters"):createAllowed()?t("new"):"",filtered()?()=>resetFilters():()=>openEditor());
      if(!filtered()&&state.category!=="knowledge") {
        if(state.category!=="global")box.append(element("p","detail-note",t("chooseAgentHint")));
        if(state.sessions.length && state.category!=="history") {
          box.append(element("p","detail-note",t("knownTasks",{count:state.sessions.length})));
          const actions=element("div","empty-actions");actions.append(button(t("viewRecentTasks"),()=>selectCategory("history")));
          if(state.category!=="short")actions.append(button(t("viewShortNotes"),()=>selectCategory("short")));box.append(actions);
        }
      }
      entries.append(box);detail.replaceChildren(empty(t("title"),t("emptyDetail")));
    }
    const groupFor=item=>item.source_type==="workspace_instructions"?"workspaceSourceGroup":item.source_type==="agent_memory"?"agentSourceGroup":item.source_type==="rag_knowledge"?"ragSourceGroup":item._type==="knowledge"?"knowledgeDocuments":item.source_type==="learning_knowledge"?"learningGroup":item._type==="memory"?(item.state==="candidate"?"pendingGroup":item.provenance?.content==="user_confirmed"?"confirmedGroup":"savedGroup"):item.kind==="preference"&&item._type==="supplemental"?"preferenceSourceGroup":item._type==="supplemental"?(state.category==="short"?"shortSourceGroup":"taskSourceGroup"):"";
    const groupOrder=["savedGroup","confirmedGroup","pendingGroup","workspaceSourceGroup","agentSourceGroup","shortSourceGroup","taskSourceGroup","preferenceSourceGroup","learningGroup","ragSourceGroup","knowledgeDocuments",""];
    const visibleItems=currentItems.slice().sort((a,b)=>groupOrder.indexOf(groupFor(a))-groupOrder.indexOf(groupFor(b)));
    let previousGroup="";
    for(const item of visibleItems) {
      const group=groupFor(item);
      if(group&&group!==previousGroup){entries.append(element("h3","card-group-label",t(group)));previousGroup=group;}
      entries.append(memoryCard(item));
    }
    if(archived.length||state.category==="knowledge") {
      const archive=element("details","memory-archive");archive.open=state.archiveOpen;
      const summary=element("summary","",t("archiveTitle"));if(archived.length)summary.append(element("span","archive-count",state.category==="knowledge"&&state.totalKnown&&["graph","quotes"].includes($("knowledge-source").value)?t("knowledgeArchiveCount",{count:state.total}):t("archiveCount",{count:archived.length})));archive.append(summary);
      archive.addEventListener("toggle",()=>{state.archiveOpen=archive.open;});
      const body=element("div","archive-body");body.append(element("p","detail-note",t("archiveBoundary")));
      if(state.category==="knowledge") {body.append(element("p","detail-note",t("archiveExtractionHelp")),element("p","detail-note",t("archiveQuotationHelp")));const actions=element("div","actions");for(const [source,label] of [["graph","browseExtractions"],["quotes","browseQuotations"]])actions.append(button(t(label),()=>{state.archiveOpen=true;state.offset=0;state.selected="";$("knowledge-source").value=source;load();}));body.append(actions);}
      for(const item of archived)body.append(memoryCard(item));
      archive.append(body);entries.append(archive);
    }
    if(state.selected)renderDetail();
  }
  function memoryCard(item) {
    const card=button("",()=>selectRecord(recordID(item)),"memory-card");card.dataset.id=recordID(item);card.setAttribute("aria-pressed",String(recordID(item)===state.selected));card.setAttribute("aria-controls","memory-detail");
    const title=element("span","card-title",recordTitle(item));if(item.pinned)title.append(element("span","badge",t("pinned")));card.append(title);
    card.append(element("span","badge "+(recordStatus(item)==="statusArchive"||item.state==="candidate"?"pending":"readonly"),t(recordStatus(item))),element("span","card-preview",item.value||t("historyBody")));
    const tags=recordTags(item);if(tags.length){const row=element("span","record-tags");row.title=t("tagSourceBody");for(const tag of tags.slice(0,5))row.append(element("span","record-tag",tag));card.append(row);}
    card.append(element("span","card-meta",dateLabel(item.updated_at||item.created_at)+" · "+sourceLabel(item.source_type)));return card;
  }
  function recordTags(item) {
    let saved=[];if(Array.isArray(item.tags))saved=item.tags;else if(typeof item.tags==="string"){try{const parsed=JSON.parse(item.tags);if(Array.isArray(parsed))saved=parsed;}catch{}}
    const tags=saved.filter(tag=>typeof tag==="string"&&tag.trim()).map(tag=>tag.slice(0,80));
    if(item.kind==="preference")tags.push(t("preferenceTag"));
    if(Array.isArray(item.tools)&&item.tools.length)tags.push(t("toolsTag"));
    if(["task_insight","learning_knowledge"].includes(item.source_type))tags.push(t("experienceTag"));
    if(item.kind==="fact")tags.push(t("factTag"));
    if(item.source_type==="user_statement"&&/沟通|交流|回复|回答|结论|简洁|详细|语气|风格|communication|concise|tone|response|answer/i.test(item.user_evidence||item.value||""))tags.push(t("styleTag"));
    return [...new Set(tags)].slice(0,20);
  }
  function recordStatus(item) {
    const p=item.provenance;
    if(item.state==="candidate")return "statusCandidate";
    if(p?.content==="user_saved")return "statusSaved";
    if(p?.content==="user_confirmed")return "statusConfirmed";
    if(["legacy_preference","knowledge_graph_legacy","knowledge_graph_verbatim_legacy","knowledge_graph_fact"].includes(item.source_type))return "statusArchive";
    if(item.source_type==="rag_knowledge")return "statusMetadata";
    if(p?.evidence?.user_quote==="verified")return "statusQuoteVerified";
    if(p?.evidence?.task_link==="verified")return "statusTaskVerified";
    if(p?.evidence?.event_link==="verified")return "statusEventVerified";
    return p?.content==="model_generated"?"statusGenerated":"statusSourceUnknown";
  }
  function selectRecord(id) { if(state.busy||!state.loaded)return;state.selected=id;state.forget="";state.detailSeq++;state.detailController?.abort();renderCards();[...entries.querySelectorAll(".memory-card")].find(item=>item.dataset.id===id)?.focus({preventScroll:true}); }
  function detailTimes(item) {
    const times=element("dl","detail-times");
    for(const [key,value] of [["created",item.created_at],["updated",item.updated_at],["source",sourceLabel(item.source_type)]]) {times.append(element("dt","",t(key)),element("dd","",key==="source"?value:dateLabel(value)));}
    const origin=sourceAgentText(item);if(origin)times.append(element("dt","",t(item.agent_name?"sourceAgentName":"sourceContextName")),element("dd","",origin));
    if(typeof item.source_location==="string"&&item.source_location)times.append(element("dt","",t("sourceLocation")),element("dd","",item.source_location));
    return times;
  }
  function sourceAgentText(item) {return typeof item.agent_name==="string"&&item.agent_name?item.agent_name:["legacy_preference","knowledge_graph_legacy","knowledge_graph_verbatim_legacy"].includes(item.source_type)?t("workspaceArchive"):item.scope==="global"||["workspace_instructions","rag_knowledge"].includes(item.source_type)?t("sharedWorkspace"):"";}
  function sourceReadOnlyKey(item) {return ({legacy_preference:"legacyPreferenceReadOnly",workspace_instructions:"workspaceReadOnly",agent_memory:"agentMemoryReadOnly",knowledge_graph_legacy:"graphLegacyReadOnly",knowledge_graph_verbatim_legacy:"graphQuotationReadOnly",knowledge_graph_fact:"graphFactReadOnly",rag_knowledge:"ragReadOnly"})[item.source_type] || (item.source_type==="learning_knowledge"?"learningReadOnly":item.kind==="preference"?"sourcePreferenceNote":"readOnlyNote");}
  function knowledgeRecord(item) {return ["learning_knowledge","knowledge_graph_legacy","knowledge_graph_verbatim_legacy","knowledge_graph_fact"].includes(item.source_type);}
  function renderSource(item) {
    const source=element("details","source-detail");source.append(element("summary","",t("sourceDetails")),detailTimes(item));const fields=element("dl");
    for(const [key,value] of [["version",item.version],["source",sourceLabel(item.source_type)],["reference",item.source_ref],["quote",item.source_quote],["confirmedRef",item.confirmed_ref],["confirmedQuote",item.confirmed_quote],["sourceID",item.source_id],["userEvidence",item.user_evidence]])if(value!==undefined&&value!=="")fields.append(element("dt","",t(key)),element("dd","",value));
    source.append(fields); return source;
  }
  function renderProvenance(item) {
    const box=element("section","record-provenance");box.append(element("h4","",t("provenanceTitle")));
    const p=item.provenance;if(!p){box.append(element("p","detail-note",t("recallUnknown")));return box;}
    const fields=element("dl","provenance-fields");
    for(const [label,key] of [["provenanceContent",({user_saved:"contentSaved",user_confirmed:"contentConfirmed",user_statement:"contentStatement",model_generated:"contentGenerated",historical_unverified:"contentHistorical",workspace_instruction:"contentInstruction",document_metadata:"statusMetadata",candidate:"statusCandidate"})[p.content]||"contentUnknown"],["provenanceIdentity",({verified:"identityVerified",workspace_archive:"identityArchive"})[p.identity]||"identityUnknown"],["provenanceQuote",({verified:"quoteVerified",stored_unverified:"quoteUnverified",missing:"quoteMissing"})[p.evidence?.user_quote]||"quoteUnknown"]])fields.append(element("dt","",t(label)),element("dd","",t(key)));
    box.append(fields);
    if(p.evidence?.user_quote==="verified"&&p.evidence.verified_quote)box.append(element("blockquote","original-evidence",p.evidence.verified_quote));
    const recall=p.recall||{},key=recall.eligibility==="candidate_excluded"?"recallCandidate":recall.eligibility==="archive_only"?"recallArchive":recall.eligibility==="evidence_unverified"?"recallEvidenceMissing":recall.eligibility==="ownership_unverified"?"recallOwnerMissing":recall.path==="workspace_instructions"?"recallInstructions":recall.path==="on_demand"?"recallTool":recall.eligibility==="not_memory_source"?"recallSeparate":recall.enabled===false?"recallDisabled":recall.eligibility==="conditional"?"recallConditional":"recallUnknown";
    box.append(element("p","recall-eligibility",t(key)));
    if(recall.conditions?.length){const conditions=element("details","recall-conditions");conditions.append(element("summary","",t("recallConditions")));const list=element("ul");for(const code of recall.conditions){const label=COPY[language()]["condition_"+code];if(label)list.append(element("li","",label));}if(list.children.length){conditions.append(list);box.append(conditions);}}
    if(p.identity==="verified"&&p.origin?.session_id){const link=element("a","manage-link",t("viewOriginalConversation"));link.href="/web/?session="+encodeURIComponent(p.origin.session_id);box.append(link);}
    if(p.origin?.location||p.origin?.record_id){const origin=element("p","provenance-origin");origin.append(element("span","",p.origin.location||""),element("code","",p.origin.record_id||""));box.append(origin);}
    const ids=element("dl","provenance-fields provenance-record-ids");for(const [key,value] of [["originalTaskID",p.origin?.task_id],["originalEventID",p.origin?.event_id],["originalMessageID",p.origin?.message_id]])if(value)ids.append(element("dt","",t(key)),element("dd","",value));if(ids.children.length)box.append(ids);
    if(p.missing?.length){const missing=p.missing.map(code=>COPY[language()]["missing_"+code]).filter(Boolean);if(missing.length)box.append(element("p","detail-note provenance-missing",t("provenanceMissing")+"："+missing.join(language()==="en"?"; ":"；")));}
    box.append(element("p","detail-note provenance-boundary",t("recordNotUsedProof")));return box;
  }
  function appendRecordBody(container,item) {
    if(item.graph&&item.source_type!=="knowledge_graph_verbatim_legacy") {
      const relation=element("dl","graph-record");for(const [label,value] of [["relationSubject",item.graph.subject],["relationPredicate",item.graph.predicate],["relationObject",item.graph.object]])relation.append(element("dt","",t(label)),element("dd","",value||t("unknownTime")));container.append(relation);
    } else container.append(element("p","value",item.value));
    if(item.when_to_use)container.append(element("p","detail-note",t("reviewWhen")+"："+item.when_to_use));
    if(Array.isArray(item.evidence)&&item.evidence.length) {
      const section=element("section","provenance-block");section.append(element("h4","",t("reviewEvidence")));
      for(const proof of item.evidence.slice(0,12)) {
        section.append(element("p","detail-note",t(proof.role==="user"?"reviewUser":"reviewTool")),element("blockquote","",proof.quote));
        const link=element("a","manage-link",t("reviewTask")+" · "+proof.task_id);link.href="/web/?session="+encodeURIComponent(proof.session_id);section.append(link);
      }
      container.append(section);
    }
  }
  function renderDetail() {
    const item=state.items.find(item=>recordID(item)===state.selected);if(!item)return;
    detail.replaceChildren(element("h3","detail-title",recordTitle(item)));
    const badges=element("div","detail-badges");
    if(item.scope)badges.append(element("span","badge",scopeLabel(item.scope)));
    if(item.kind)badges.append(element("span","badge",t(item.kind)));
    if(item.read_only)badges.append(element("span","badge readonly",t("readonly")));
    if(item._type==="supplemental")badges.append(element("span","badge readonly",t("sourceRecord")));
    if(item.state==="candidate")badges.append(element("span","badge pending",t("candidate")));
    if(item.pinned)badges.append(element("span","badge",t("pinned")));
    if(item.ownership_verified===false)badges.append(element("span","badge pending",t("ownershipUnverified")));
    detail.append(badges);
    const tags=recordTags(item);if(tags.length){const row=element("div","record-tags detail-tags");row.title=t("tagSourceBody");row.setAttribute("aria-label",t("tagsTitle"));row.append(element("span","tags-heading",t("tagsTitle")));for(const tag of tags)row.append(element("span","record-tag",tag));detail.append(row);}
    if(item._type==="knowledge") {
      detail.append(element("p","value",t("knowledgeBody")),element("p","detail-note",t("knowledgeBoundary")));
      const link=element("a","manage-link",t("manageKnowledge"));link.href="/web/?panel=knowledge";detail.append(link);return;
    }
    if(item._type==="history") {
      detail.append(detailTimes(item));
      detail.append(element("p","detail-note",t("historyBody")));const actions=element("div","actions");
      const link=element("a","manage-link",t("openTask"));link.href="/web/?session="+encodeURIComponent(item.id);actions.append(link);
      actions.append(button(t("saveExperience"),()=>saveAsExperience(item)));detail.append(actions);
      const body=element("div","history-messages");body.id="history-messages";detail.append(body);renderHistory(item,body);setBusy(state.busy);return;
    }
    appendRecordBody(detail,item);detail.append(renderProvenance(item));
    if(item._type==="supplemental")appendExecutionDetails(detail,item);
    const actions=element("div","actions");
    if(writable(item)) {
      actions.append(button(t("correct"),()=>openEditor(item)));
      if(item.state==="candidate")actions.append(button(t("confirm"),()=>mutate("confirm",item),"primary"));
      if(item.scope==="user"&&item.kind==="preference"&&item.state==="active")actions.append(button(t(item.pinned?"unpin":"pin"),()=>mutate(item.pinned?"unpin":"pin",item)));
      actions.append(button(t("forget"),()=>{state.forget=recordID(item);renderDetail();},"danger"));
    } else {
      detail.append(element("p","detail-note source-readonly-note",t(sourceReadOnlyKey(item))));
      if(item.source_type==="rag_knowledge"){const link=element("a","manage-link",t("manageKnowledge"));link.href="/web/?panel=knowledge";actions.append(link);}
      else actions.append(button(t(item.source_type==="agent_memory"?"reviewForAgent":knowledgeRecord(item)||item.source_type==="workspace_instructions"?"saveMemory":item.kind==="preference"?"savePreference":"saveExperience",{agent:state.agent}),()=>saveAsExperience(item)));
    }
    detail.append(actions);
    if(state.forget===recordID(item)) {
      const confirm=element("div","forget-confirm");confirm.append(element("p","",t("forgetConfirm")));const row=element("div","actions");row.append(button(t("cancel"),()=>{state.forget="";renderDetail();}),button(t("confirmForget"),()=>mutate("forget",item),"danger"));confirm.append(row);detail.append(confirm);
    }
    detail.append(renderSource(item));setBusy(state.busy);
  }
  async function renderHistory(item,body) {
    const sourcesPanel=element("div","history-sources"),messagesPanel=element("div","history-original-messages");body.append(sourcesPanel,messagesPanel);
    if(state.messages.has(item.id))fillHistory(messagesPanel,state.messages.get(item.id));else messagesPanel.append(element("p","detail-note",t("loading")));
    if(state.historySources.has(item.id))fillHistorySources(sourcesPanel,state.historySources.get(item.id));else sourcesPanel.append(element("p","detail-note",t("loading")));
    if(state.messages.has(item.id)&&state.historySources.has(item.id))return;
    const seq=++state.detailSeq;
    state.detailController?.abort();state.detailController=new AbortController();
    const signal=state.detailController.signal,params=new URLSearchParams({agent:state.agent,view:"history",session_id:item.id,limit:"1"});
    const jobs=[state.messages.has(item.id)?Promise.resolve({messages:state.messages.get(item.id)}):fetch("/api/session/"+encodeURIComponent(item.id)+"?limit=200",{cache:"no-store",signal}).then(responseJSON),state.historySources.has(item.id)?Promise.resolve({supplemental:state.historySources.get(item.id)}):fetch("/api/memory?"+params,{cache:"no-store",signal}).then(responseJSON)];
    const results=await Promise.allSettled(jobs);
    if(seq!==state.detailSeq||state.category!=="history"||state.selected!==recordID(item))return;
    if(results[0].status==="fulfilled") {const messages=Array.isArray(results[0].value.messages)?results[0].value.messages:[];state.messages.set(item.id,messages);fillHistory(messagesPanel,messages);}else messagesPanel.replaceChildren(element("p","detail-note",t("historyReadFailure")));
    if(results[1].status==="fulfilled") {const sources=normalizeSources(results[1].value.supplemental).filter(source=>source.session_id===item.id);state.historySources.set(item.id,sources);fillHistorySources(sourcesPanel,sources);}else sourcesPanel.replaceChildren(element("p","detail-note",t("sourcesUnavailable")));
  }
  function normalizeSources(sources) { return (Array.isArray(sources)?sources:[]).filter(item=>item&&typeof item.id==="string"&&typeof item.key==="string"&&typeof item.value==="string").map(item=>Object.assign({},item,{_type:"supplemental",read_only:true})); }
  function appendExecutionDetails(container,item) {
    if(item.task_status)container.append(element("p","detail-note",t("taskStatus")+" · "+(({success:t("taskSuccess"),failed:t("taskFailed"),pending:t("taskPending")})[item.task_status]||item.task_status)));
    if(Array.isArray(item.tools)&&item.tools.length) {
      const tools=element("details","source-tools");tools.open=true;tools.append(element("summary","",t("toolsRecorded")+" ("+item.tools.length+")"));const list=element("ol","source-tool-list");
      for(const tool of item.tools) {const row=element("li","source-tool");row.append(element("strong","",tool.name||t("tool")),element("span","",t(tool.success===true?"toolSuccess":tool.success===false?"toolFailed":"toolUnknown")));if(Number.isFinite(tool.duration_ms)&&tool.duration_ms>=0)row.append(element("span","",t("duration")+" "+tool.duration_ms.toLocaleString()+" ms"));list.append(row);}tools.append(list);container.append(tools);
    } else if(["task_execution","task_insight"].includes(item.source_type))container.append(element("p","detail-note",t("toolsNotRecorded")));
    if(item.user_feedback) {const feedback=element("div","source-feedback");feedback.append(element("h4","",t("feedbackOriginal")),element("p","value",item.user_feedback));container.append(feedback);}
    if(typeof item.user_rating==="number"&&Number.isFinite(item.user_rating))container.append(element("p","detail-note",t("rating")+" · "+item.user_rating));
  }
  function fillHistorySources(body,sources) {
    body.replaceChildren();body.append(element("h4","",t("historySources")));
    if(!sources.length){body.append(element("p","detail-note",t("noTaskSources")));return;}
    body.append(element("p","detail-note",t("sourceBoundary")));
    const order=["task_execution","user_feedback","task_insight","learning_knowledge","user_statement","knowledge_fact","conversation","summary","session_context"];
    const ordered=sources.slice().sort((a,b)=>(order.indexOf(a.source_type)<0?99:order.indexOf(a.source_type))-(order.indexOf(b.source_type)<0?99:order.indexOf(b.source_type)));
    for(const [index,item] of ordered.entries()) {
      const section=element("details","history-source");section.open=index===0;section.append(element("summary","",sourceLabel(item.source_type)+" · "+recordTitle(item)));const content=element("div","history-source-body");appendRecordBody(content,item);appendExecutionDetails(content,item);content.append(renderProvenance(item),renderSource(item));const actions=element("div","actions");actions.append(button(t(item.kind==="preference"?"savePreference":"saveExperience"),()=>saveAsExperience(item)));content.append(actions);section.append(content);body.append(section);
    }
  }
  function fillHistory(body,messages) {
    body.replaceChildren(); if(!messages.length){body.append(element("p","detail-note",t("noMessages")));return;}
    body.append(element("p","detail-note",t("messagesLimit",{count:messages.length})));
    for(const message of messages) {
      const row=element("section","history-message");row.append(element("div","history-message-header",t(["user","assistant","tool","system"].includes(message.role)?message.role:"assistant")+" · "+dateLabel(message.created_at)),element("p","value",message.content || ""));
      if(Array.isArray(message.attachments)&&message.attachments.length)row.append(element("p","detail-note",message.attachments.map(item=>item.name || "").filter(Boolean).join(" · ")));
      body.append(row);
    }
  }
  function saveAsExperience(item) {
    if(item.source_type==="legacy_preference"){openEditor(null,{key:"",value:"",scope:"user",kind:"preference",preferenceIntent:true,experience:true,sourceID:recordID(item)});return;}
    let value=item.value || "";
    if(item._type==="history")value="";
    const preference=item._type==="supplemental"&&item.kind==="preference",learning=knowledgeRecord(item)||item.source_type==="workspace_instructions",reviewNote=item.source_type==="legacy_preference"?"legacyPreferenceReadOnly":item.source_type==="agent_memory"?"agentMemoryReadOnly":learning?sourceReadOnlyKey(item):"";
    openEditor(null,{key:(preference||learning?"":t("experienceTitle"))+recordTitle(item),value,scope:preference?"user":"project",kind:preference?"preference":learning&&item.kind==="fact"?"fact":"experience",experience:true,learning,reviewNote,history:item._type==="history",sourceID:recordID(item)});
  }
  function draftKey() { return state.agent+"|"+state.category+"|"+(state.category==="short"?state.session:""); }
  function editorText() {
    const item=state.editing,ctx=state.editContext;
    const preference=ctx?.preferenceIntent||item?.kind==="preference";
    $("editor-title").textContent=t(ctx?.reviewedSource?"preferenceReviewTitle":item?"editTitle":preference?"organizeAddPreference":"new");$("editor-save").textContent=t(preference?(item?.state==="candidate"?"preferenceSaveCandidate":"preferenceSave"):item?"saveCorrection":"save");
    $("editor-note").textContent=ctx?.reviewNote?t(ctx.reviewNote):ctx?.learning?t("learningReadOnly"):ctx?.experience?t($("editor-kind").value==="preference"?"sourcePreferenceNote":"experienceNote"):item?t(item.state==="candidate"?"candidateNote":item.pinned?"pinnedNote":"editNote"):$("editor-scope").value==="session"?t("sessionNote"):$("editor-scope").value==="global"?t("globalNote"):t("newNote");
    if(ctx?.preferenceIntent){$("editor-note").textContent=t("organizePreferenceEditor");for(const option of $("editor-scope").options)option.textContent=t("organizePreferenceScope_"+option.value);}
    else for(const option of $("editor-scope").options)option.textContent=t(({user:"personal",project:"project",global:"global",session:"short"})[option.value]);
    if(preference&&item?.state==="candidate")$("editor-note").textContent=t("preferenceCandidateEditor");
    $("editor-key").previousElementSibling.textContent=t(ctx?.preferenceIntent?"organizePreferenceName":"name");$("editor-key").placeholder=ctx?.preferenceIntent?t("organizePreferenceNameExample"):"";
    $("editor-value").placeholder=ctx?.preferenceIntent?t("organizePreferenceValueExample"):ctx?.history?t("reviewLesson"):"";
    const original=$("editor-preference-original");original.hidden=!ctx?.reviewedSource;
    if(ctx?.reviewedSource){const row=ctx.reviewedSource._libraryRow;$("editor-preference-claim").textContent=row.graph?window.MacsMemoryGroups.preferenceStatement(row.graph,language()):row.value||"";$("editor-preference-quote").textContent=row.graph?.quoted_evidence||row.source_quote||t(window.MacsMemoryGroups.preferenceConversations(row,memoryGroupState.libraries.preferences.sessions).length?"preferenceLinkedQuoteNote":"preferenceNoQuote");}
  }
  function openEditor(item=null,prefill=null) {
    if(state.busy||!state.agent||(!item&&!prefill&&!createAllowed())||(item&&!writable(item)))return;
    const key=draftKey()+(prefill?.experience?"|experience:"+prefill.sourceID:"|new");
    state.editing=item;state.editContext={agent:state.agent,session:state.session,category:state.category,experience:!!prefill?.experience,learning:!!prefill?.learning,preferenceIntent:!!prefill?.preferenceIntent,reviewedSource:prefill?.reviewedSource||null,reviewNote:prefill?.reviewNote || "",history:!!prefill?.history,draftKey:key};
    const saved=item?null:state.drafts.get(key) || prefill;
    $("editor-form").reset();
    $("editor-key").value=item?item.key:saved?.key || "";$("editor-value").value=item?item.value:saved?.value || "";
    const defaultScope=state.category==="short"?"session":state.category==="global"?"global":state.category==="project"?"project":"user";
    $("editor-scope").value=item?item.scope:saved?.scope || defaultScope;$("editor-kind").value=item?item.kind:saved?.kind || (state.category==="preferences"?"preference":defaultScope==="user"?"preference":"fact");
    for(const option of $("editor-scope").options){option.hidden=item?option.value!==item.scope:prefill? !["user","project"].includes(option.value):defaultScope==="global"?option.value!=="global":defaultScope==="session"?option.value!=="session":!["user","project"].includes(option.value);option.disabled=option.hidden;}
    for(const id of ["editor-key","editor-scope","editor-kind"])$(id).disabled=!!item||id==="editor-kind"&&!!prefill?.preferenceIntent;
    $("editor-error").hidden=true;editorText();editor.showModal();$(item?"editor-value":"editor-key").focus();
  }
  function retainDraft() {
    if(state.editContext&&!state.editing)state.drafts.set(state.editContext.draftKey,{key:$("editor-key").value,value:$("editor-value").value,scope:$("editor-scope").value,kind:$("editor-kind").value});
  }
  function closeEditor() {if(!state.busy){retainDraft();editor.close();}}
  function actionPayload(action,item) { const payload={agent:state.agent,action,key:item.key,scope:item.scope,expected_version:item.version};if(item.scope==="session")payload.session_id=item.session_id || state.session;return payload; }
  async function writeMemory(payload) {return responseJSON(await fetch("/api/memory",{method:"POST",cache:"no-store",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)}));}
  async function saveEditor(event) {
    event.preventDefault();if(state.busy)return;
    const key=$("editor-key").value.trim(),value=$("editor-value").value.trim(),scope=$("editor-scope").value,kind=$("editor-kind").value;
    if(!key||!value){$("editor-error").textContent=t("required");$("editor-error").hidden=false;return;}
    if(Array.from(key).length>256||Array.from(value).length>8000){$("editor-error").textContent=t("tooLong");$("editor-error").hidden=false;return;}
    const item=state.editing,ctx=state.editContext;const payload=item?Object.assign(actionPayload("correct",item),{agent:ctx.agent,value}):{agent:ctx.agent,action:"create",key,value,scope,kind};
    if(scope==="session")payload.session_id=item?.session_id || ctx.session;
    setBusy(true);$("editor-error").hidden=true;
    try {
      await writeMemory(payload);invalidateCatalog();state.drafts.delete(ctx.draftKey);editor.close();state.editing=null;state.forget="";
      if(!item){state.category=scope==="global"?"global":scope==="session"?"short":scope==="project"?"project":kind==="preference"?"preferences":"long";resetFilters(false);}
      if(kind==="preference"){memoryGroupState.group="preferences";memoryGroupState.preferenceView=item?.state==="candidate"?"history":"active";}
      const loaded=await load();await loadPromptSources();
      if(kind==="preference"){const saved=promptState.sources.find(source=>source.title===key&&window.MacsMemoryGroups.groups(source,"").includes("preferences"));if(saved){promptState.selected=saved.id;renderPromptSources();}promptNotice(t(item?.state==="candidate"?"preferenceCandidateSaved":"preferenceSavedUse"));}
      if(loaded)notice(t(item?"corrected":"saved"));
    } catch(error) {if(error.status===409)await load();$("editor-error").textContent=error.message;$("editor-error").hidden=false;}
    finally{setBusy(false);}
  }
  async function mutate(action,item) {
    if(state.busy||!writable(item))return;setBusy(true);
    try{await writeMemory(actionPayload(action,item));invalidateCatalog();state.forget="";if(item.kind==="preference"){memoryGroupState.group="preferences";memoryGroupState.preferenceView="active";}const loaded=await load();await loadPromptSources();if(item.kind==="preference"&&action==="confirm")promptNotice(t("preferenceSavedUse"));if(loaded)notice(t(({confirm:"confirmed",pin:"pinnedDone",unpin:"unpinnedDone",forget:"forgotten"})[action]));}
    catch(error){if(error.status===409)await load();notice(error.message,true);}
    finally{setBusy(false);}
  }
  function resetFilters(reload=true){clearTimeout(state.searchTimer);for(const id of ["scope","state","search"])$(id).value="";$("knowledge-source").value="curated";state.archiveOpen=false;state.offset=0;if(reload)load();}
  function memoryParams() {
    const params=new URLSearchParams({view:state.category,offset:String(state.offset),limit:String(pageSize)});if(state.agent)params.set("agent",state.agent);
    const scope=({short:"session",project:"project",global:"global"})[state.category] || "";if(scope)params.set("scope",scope);
    if(state.category==="knowledge")params.set("knowledge_source",$("knowledge-source").value);if(state.category==="preferences")params.set("kind","preference");if(state.category==="short")params.set("session_id",state.session);
    for(const key of ["state","search"])if($(key).value.trim())params.set(key,$(key).value.trim());
    return params;
  }
  async function load() {
    clearTimeout(state.searchTimer);const seq=++state.loadSeq;state.loading=true;state.controller?.abort();state.controller=new AbortController();state.detailController?.abort();state.detailSeq++;
    if(state.category==="history"){state.messages.clear();state.historySources.clear();}
    entries.setAttribute("aria-busy","true");$("count").textContent=t("loading");$("pager").hidden=true;state.loaded=false;state.items=[];state.viewNotes=[];entries.replaceChildren(empty(t("loading"),""));detail.hidden=true;$("memory-browser").classList.add("empty-browser");detail.replaceChildren(empty(t("title"),t("choose")));updateControls();
    try {
      let data;
      if(state.category==="short"&&(!state.metadataLoaded||!state.session)) {
        const params=new URLSearchParams({view:"short",scope:"session",limit:"1"});if(state.agent)params.set("agent",state.agent);
        data=await responseJSON(await fetch("/api/memory?"+params,{cache:"no-store",signal:state.controller.signal}));
        if(seq!==state.loadSeq)return false;updateMetadata(data);
        if(state.category==="short"&&state.session)return load();
      } else data=await responseJSON(await fetch("/api/memory?"+memoryParams(),{cache:"no-store",signal:state.controller.signal}));
      if(seq!==state.loadSeq)return false;updateMetadata(data);state.loaded=true;state.viewNotes=Array.isArray(data.view_notes)?data.view_notes.filter(code=>typeof code==="string"):[];
      if(state.category==="knowledge") {state.items=normalizeSources(data.supplemental);const total=data.sources_total;state.totalKnown=Number.isSafeInteger(total)&&total>=0;state.total=state.totalKnown?total:0;if(state.totalKnown&&state.offset>0&&state.offset>=state.total){state.offset=Math.max(0,Math.floor((state.total-1)/pageSize)*pageSize);return load();}}
      else if(state.category==="history") {const search=$("search").value.trim().toLowerCase();state.items=state.sessions.filter(item=>!search||(item.name||item.id).toLowerCase().includes(search)).map(item=>Object.assign({},item,{_type:"history",source_type:"history",read_only:true}));state.total=state.items.length;state.totalKnown=true;}
      else {
        const provenance=new Map((Array.isArray(data.entry_provenance)?data.entry_provenance:[]).filter(item=>item.record_kind==="entry").map(item=>[item.id,item]));
        const notes=(Array.isArray(data.entries)?data.entries:[]).map(item=>Object.assign({},item,{_type:"memory",provenance:provenance.get(item.id)}));
        const supplemental=normalizeSources(data.supplemental);
        state.items=notes.concat(supplemental);state.totalKnown=Number.isSafeInteger(data.total)&&data.total>=0;state.total=state.totalKnown?data.total:0;
        if(!notes.length&&state.offset>0){state.offset=Math.max(0,Math.floor((state.total-1)/pageSize)*pageSize);return load();}
      }
      updateControls();renderCards();$("count").textContent=countLabel();
      $("page-number").textContent=t("page",{page:Math.floor(state.offset/pageSize)+1,pages:Math.max(1,Math.ceil(state.total/pageSize))});$("pager").hidden=state.category==="history"||!state.totalKnown||state.total<=pageSize;
      setBusy(state.busy);notice("");if($("recall-panel").open&&!$("recall-panel").hidden)loadTraces();return true;
    } catch(error) {
      if(seq!==state.loadSeq||error.name==="AbortError")return false;
      entries.replaceChildren(empty(t("readFailure"),error.message,t("retry"),load));detail.replaceChildren(empty(t("title"),t("choose")));$("count").textContent="";notice(t("failed"),true);updateControls();return false;
    } finally{if(seq===state.loadSeq){state.loading=false;entries.setAttribute("aria-busy","false");}}
  }
  function promptNotice(text,error=false){$("prompt-message").textContent=text;$("prompt-message").classList.toggle("error",error);}
  function promptModeName(mode){return t(({direct:"organizeDirect",relevant:"organizeRelevant",on_demand:"organizeOnDemand",stored:"organizeStored"})[mode]||"promptModeUnknown");}
  function promptSourceModeName(source){return source.recall?.enabled===false?t("recallPaused"):source._libraryGroup==="preferences"?t(source._libraryRow?.state==="candidate"?"preferenceCandidateBadge":"preferenceReviewBadge"):promptModeName(source.mode);}
  function currentPromptSource(source){return source._library?{content:source._libraryContent,editable:false,deletable:false}:Object.hasOwn(promptState.current,source.id)?promptState.current[source.id]:null;}
  function groupCopyKey(group){return "organize"+group.charAt(0).toUpperCase()+group.slice(1);}
  function organizedSources(){return promptState.sources.concat(...Object.values(memoryGroupState.libraries).map(library=>library.items));}
  function knowledgeSources(){
    const library=memoryGroupState.libraries.knowledge,choice=library.source;
    const core=promptState.sources.filter(source=>choice==="materials"?["tool_learning","learning_knowledge"].includes(source.kind):choice==="graph"?source.kind==="knowledge_fact":choice==="learning"?source.kind==="learning_knowledge":["all","curated"].includes(choice)&&["tool_learning","learning_knowledge","knowledge_fact"].includes(source.kind));
    return core.map(source=>Object.assign({},source,{_libraryGroup:"knowledge"})).concat(library.items);
  }
  function knowledgeMaterialCount(){const library=memoryGroupState.libraries.knowledge;return (library.materialTotal??promptState.sources.filter(source=>source.kind==="learning_knowledge").length)+promptState.sources.filter(source=>source.kind==="tool_learning").length;}
  async function refreshKnowledgeMaterialCount(){
    const library=memoryGroupState.libraries.knowledge;library.materialController?.abort();library.materialController=new AbortController();const controller=library.materialController,agent=promptState.agent;
    try{const data=await responseJSON(await fetch("/api/memory?"+new URLSearchParams({agent,view:"knowledge",knowledge_source:"materials",limit:"1"}),{cache:"no-store",signal:controller.signal}));if(controller===library.materialController&&agent===promptState.agent&&Number.isSafeInteger(data.sources_total)){library.materialTotal=data.sources_total;renderMemoryGroups();}}
    catch(error){if(error.name!=="AbortError"&&controller===library.materialController)library.materialTotal=null;}
  }
  function activePreferenceRecords(){return window.MacsMemoryGroups.assemble(promptState.sources,promptState.current,"preferences","");}
  function preferenceArchive(){return window.MacsMemoryGroups.preferenceArchive(memoryGroupState.libraries.preferences.items,promptState.agents);}
  function pendingPreferences(){return window.MacsMemoryGroups.assemble(memoryGroupState.libraries.preferences.items.filter(source=>source._libraryRow?.kind==="preference"&&source._libraryRow?.state==="candidate"),promptState.current,"preferences","");}
  function reviewPreferences(){const archive=preferenceArchive();return pendingPreferences().concat(archive.current,memoryGroupState.preferenceArchiveOpen?archive.previous:[]);}
  function preferenceView(){return memoryGroupState.preferenceView||(activePreferenceRecords().length||!reviewPreferences().length?"active":"history");}
  function taskRecords(view="all"){return window.MacsMemoryGroups.assemble(promptState.sources.filter(source=>view==="all"||source.kind===(view==="summaries"?"task_insight":"task_execution")),promptState.current,"tasks","");}
  function organizedRecords(group=memoryGroupState.group){if(group==="tasks")return taskRecords(memoryGroupState.taskView);if(group==="preferences"&&preferenceView()==="history")return reviewPreferences();return window.MacsMemoryGroups.assemble(group==="knowledge"?knowledgeSources():group==="all"?promptState.sources:group==="preferences"?promptState.sources:organizedSources(),promptState.current,group,promptState.session);}
  function relationLabel(predicate){const labels={has_capability:["能力描述","Capability"],language_capability:["语言能力","Language"],prefers:["偏好","Preference"],prefers_tone:["语气偏好","Tone preference"],prefers_report_format:["报告格式","Report format"],prefers_style:["风格偏好","Style preference"],prefers_language:["语言偏好","Language preference"],prefers_response_length:["回答长度","Response length"],prefers_tool:["工具偏好","Tool preference"],dislikes:["不喜欢","Dislikes"],language_preference:["语言偏好","Language preference"],testing_framework_preference:["测试工具偏好","Testing tool preference"],transportation_preference:["出行偏好","Transport preference"]};return labels[predicate]?.[language()==="en"?1:0]||predicate;}
  function revealOldMemoryMaterial(){const panel=$("other-memory-storage");panel.hidden=false;panel.open=true;}
  function openOldMemoryMaterial(){if(promptSwitchBlocked())return;revealOldMemoryMaterial();state.archiveOpen=true;state.category="preferences";state.agent=promptState.agent;state.offset=0;state.selected="";load();$("other-memory-storage").scrollIntoView({block:"start",behavior:"smooth"});}
  function memorySourceType(source){return t(/:preference$/.test(source.kind||"")?"memoryPreferenceType":source.injection==="system"?"memoryProjectType":source.kind==="task_execution"?"memoryTaskResultType":source.kind==="task_insight"?"memoryTaskSummaryType":source.kind==="summary"?"memorySummaryType":source.kind==="learning_knowledge"?"memoryKnowledgeType":source.kind==="tool_learning"?"memoryNoteType":"memorySavedType");}
  function memorySourceScope(source){const scope=/^long_term:(user|project|global|session):/.exec(source.kind||"")?.[1];return scope?t("organizePreferenceScope_"+scope):t(["task_execution","task_insight","user_statement","user_feedback","learning_knowledge"].includes(source.kind)?"memoryTaskScope":"memoryWorkspaceScope");}
  function knowledgeType(source){return t(source.kind==="tool_learning"?"knowledgeNoteType":source.kind==="learning_knowledge"?"knowledgeLearnedType":source.kind==="rag_knowledge"?"knowledgeCollectionType":source.kind==="knowledge_graph_verbatim_legacy"?"knowledgeQuoteType":"knowledgeRelationType");}
  function openKnowledgeDocuments(){if(promptSwitchBlocked())return;if(window.parent!==window&&typeof window.parent.openRagAdmin==="function"){window.parent.closeMemoryPanel?.();window.parent.openRagAdmin();}else if(window.parent!==window&&typeof window.parent.openKnowledgePanel==="function"){window.parent.closeMemoryPanel?.();window.parent.openKnowledgePanel();}else location.assign("/web/?panel=knowledge");}
  function chooseKnowledgeView(choice){if(promptSwitchBlocked())return;const library=memoryGroupState.libraries.knowledge;if(library.source===choice)return;library.source=choice;library.offset=0;library.items=[];library.loaded=false;promptState.selected="";loadMemoryLibrary("knowledge",true);}
  function organizedTitle(source){
    if(memoryGroupState.group==="preferences"&&!source._libraryRow?.graph)return window.MacsMemoryGroups.brief(window.MacsMemoryGroups.content(source,promptState.current),100)||source.title||t("inputUnnamedSource");
    if(String(source.kind||"").startsWith("long_term:")&&/^remember_[a-f0-9]{32}$/.test(source.title||""))return window.MacsMemoryGroups.brief(window.MacsMemoryGroups.content(source,promptState.current),60)||t("inputUnnamedSource");
    if(source.kind==="legacy_preference")return t("organizeOldPreference")+" · "+dateLabel(source.updated_at||source.created_at);
    const graph=source._libraryRow?.graph;if(graph)return memoryGroupState.group==="preferences"?t("preferenceClaimTitle",{claim:window.MacsMemoryGroups.preferenceStatement(graph,language())}):graph.predicate==="has_capability"?t("knowledgeCapabilityTitle",{topic:graph.object}):graph.subject+" · "+relationLabel(graph.predicate);
    if(source.kind==="knowledge_graph_verbatim_legacy")return t("knowledgeQuoteTitle",{text:window.MacsMemoryGroups.brief(source._libraryContent,48)});
    if(source.injection==="history_messages")return t(source.role==="assistant"?"organizeAssistantMessage":"organizeYourMessage");
    if(["task_execution","task_insight"].includes(source.kind))return t(source.kind==="task_execution"?"memoryTaskResultTitle":"memoryTaskSummaryTitle",{title:String(source.title||"").replace(/^(任务结果|已有任务经验)\s*·\s*/,"")||t("sourceHistory")});
    return source.title||t("inputUnnamedSource");
  }
  function graphClaim(graph){return memoryGroupState.group==="preferences"?window.MacsMemoryGroups.preferenceStatement(graph,language()):graph.predicate==="has_capability"?t("organizeCapabilityClaim",graph):graph.subject+" · "+relationLabel(graph.predicate)+"："+graph.object;}
  function organizedPreview(source){
    if(memoryGroupState.group==="preferences"){
      const row=source._memoryEntry||source._libraryRow,graph=row?.graph;
      const quote=graph?.quoted_evidence||(row?.evidence||[]).find(proof=>proof.role==="user"&&proof.quote)?.quote||(["user_statement","user_feedback","user_explicit"].includes(row?.source_type)?row.source_quote:"");
      return quote?t("preferenceBasis")+": "+window.MacsMemoryGroups.brief(quote,110):t(graph?"preferenceNoQuote":row?.source_type==="user_explicit"||row?.source_ref==="local-memory-manager"?"preferenceManualBasis":"preferenceUnknownBasis");
    }
    return window.MacsMemoryGroups.brief(source._libraryRow?.graph?graphClaim(source._libraryRow.graph):window.MacsMemoryGroups.content(source,promptState.current),135)||t("inputExcerptMissing");
  }
  function selectMemoryGroup(group){
    if(!memoryGroupOrder.includes(group)||promptSwitchBlocked())return;
    if(group==="tasks"&&memoryGroupState.taskView==="summaries"&&!taskRecords("summaries").length&&taskRecords("results").length)memoryGroupState.taskView="results";
    memoryGroupState.group=group;promptState.selected="";promptState.deleting="";renderPromptSources();
    if(memoryGroupState.libraries[group]&&!memoryGroupState.libraries[group].loaded)loadMemoryLibrary(group);
  }
  function renderMemoryGroups(){
    const nav=$("prompt-groups");nav.replaceChildren();
    for(const group of memoryGroupOrder){
      const library=memoryGroupState.libraries[group],records=organizedRecords(group),count=group==="tasks"?taskRecords().length:group==="preferences"?activePreferenceRecords().length:group==="knowledge"?knowledgeMaterialCount():library?.loaded?Math.max(records.length,library.total):records.length;
      const item=button("",()=>selectMemoryGroup(group),"prompt-group-button");item.dataset.memoryGroup=group;item.setAttribute("aria-pressed",String(group===memoryGroupState.group));
      item.disabled=state.busy||inputState.busy;item.append(element("span","",t(groupCopyKey(group))),element("span","prompt-group-count",library?.loading?"…":count));nav.append(item);
    }
    const group=memoryGroupState.group,key=groupCopyKey(group);$("prompt-group-title").textContent=t(key);$("prompt-group-description").textContent=t(key+"Description");$("prompt-group-use").textContent=t(key+"Use");
    if(group==="knowledge")$("prompt-group-use").textContent=t(({materials:"knowledgeMaterialsUse",quotes:"knowledgeQuotesUse",graph:"knowledgeRelationsUse",all:"knowledgeAllUse",rag:"knowledgeMaterialsUse",learning:"knowledgeMaterialsUse",curated:"knowledgeAllUse"})[memoryGroupState.libraries.knowledge.source]||"knowledgeAllUse");
    $("prompt-group-use").hidden=group==="preferences";$("prompt-group-use").closest("details").hidden=group==="preferences";
    $("prompt-add-preference").hidden=group!=="preferences";$("prompt-add-preference").disabled=state.busy||inputState.busy||!promptState.loaded;
    $("prompt-add-memory").disabled=state.busy||inputState.busy||!promptState.loaded;
    renderMemoryLibraryControls();
  }
  function renderMemoryLibraryControls(){
    const output=$("prompt-library-controls"),group=memoryGroupState.group,oldArchive=output.querySelector(".knowledge-archive-controls");if(oldArchive)memoryGroupState.libraries.knowledge.archiveOpen=oldArchive.open;output.replaceChildren();output.hidden=!["preferences","knowledge","learning","tasks"].includes(group);
    if(group==="tasks"){
      const tabs=element("div","knowledge-view-tabs");
      for(const [value,key]of [["summaries","memoryTaskSummaries"],["results","memoryTaskResults"],["all","memoryTaskAll"]]){const tab=button(t(key,{count:taskRecords(value).length}),()=>{if(promptSwitchBlocked())return;memoryGroupState.taskView=value;promptState.selected="";renderPromptSources();});tab.dataset.taskView=value;tab.setAttribute("aria-pressed",String(memoryGroupState.taskView===value));tabs.append(tab);}output.append(tabs,element("p","detail-note task-use-help",t("memoryTaskHelp")));
    }else if(group==="preferences"){
      const tabs=element("div","knowledge-view-tabs");
      const archived=preferenceArchive();
      for(const [value,key,count]of [["active","memoryActiveTab",activePreferenceRecords().length],["history","memoryHistoryTab",pendingPreferences().length+archived.current.length]]){const tab=button(t(key,{count}),()=>{if(promptSwitchBlocked())return;memoryGroupState.preferenceView=value;promptState.selected="";renderPromptSources();});tab.dataset.preferenceView=value;tab.setAttribute("aria-pressed",String(preferenceView()===value));tabs.append(tab);}output.append(tabs);
      const guide=element("details","memory-reading-summary");guide.id="preference-use-guide";guide.append(element("summary","",t("memoryPreferenceGuide")));
      if(preferenceView()==="history")output.append(element("p","detail-note",t("memoryHistoryNote")));
      else output.append(element("p","detail-note",t("organizePreferenceAiUse")));
      const ai=element("section"),human=element("section"),steps=element("ol");ai.append(element("h4","",t("organizeAiUse")));
      if(!organizedRecords(group).some(record=>record.members.some(source=>source.mode!=="stored")))ai.append(element("p","",t("organizeNoActivePreferences")));
      ai.append(element("p","",t("organizePreferenceAiUse")),element("p","detail-note",t("organizePreferenceExample")));human.append(element("h4","",t("organizeHumanUse")));
      for(const step of t("organizePreferenceSteps").split("|"))steps.append(element("li","",step));human.append(steps);guide.append(ai,human);output.append(guide);
      if(preferenceView()==="history"&&archived.previous.length){const toggle=button(t("preferenceArchiveToggle",{count:archived.previous.length}),()=>{if(promptSwitchBlocked())return;memoryGroupState.preferenceArchiveOpen=!memoryGroupState.preferenceArchiveOpen;promptState.selected="";renderPromptSources();},"text-button");toggle.setAttribute("aria-expanded",String(memoryGroupState.preferenceArchiveOpen));output.append(toggle);if(memoryGroupState.preferenceArchiveOpen)output.append(element("p","detail-note",t("preferenceArchiveHelp")));}
      if(archived.originalCount){const audit=element("details","memory-reading-summary preference-counts");audit.append(element("summary","",t("preferenceOriginalCount",{count:archived.originalCount})));if(archived.excluded)audit.append(element("p","detail-note",t("preferenceAIExcluded",{count:archived.excluded})));output.append(audit);}
      const archiveCount=memoryGroupState.libraries.preferences.items.filter(source=>source.kind==="legacy_preference").length;
      if(archiveCount){const old=element("details","memory-old-material");old.append(element("summary","",t("memoryArchive")+" ("+archiveCount+")"),element("p","detail-note",t("memoryArchiveNote")),button(t("memoryArchive"),openOldMemoryMaterial));output.append(old);}
    }else if(group==="knowledge"){
      const tabs=element("div","knowledge-view-tabs"),archive=element("details","memory-reading-summary knowledge-archive-controls");tabs.setAttribute("aria-label",t("organizeKnowledgeSource"));archive.open=memoryGroupState.libraries.knowledge.archiveOpen||memoryGroupState.libraries.knowledge.source!=="materials";archive.append(element("summary","",t("memoryHistoricalMaterial")));
      for(const [value,key]of [["materials","knowledgeMaterials"],["quotes","knowledgeQuotes"],["graph","knowledgeRelations"]]){const item=button(t(key),()=>chooseKnowledgeView(value));item.dataset.knowledgeView=value;item.setAttribute("aria-pressed",String(memoryGroupState.libraries.knowledge.source===value));tabs.append(item);}archive.append(tabs,element("p","detail-note",t("memoryHistoricalMaterialNote")));
      const label=element("label","",t("knowledgeMore")),select=element("select");select.id="organized-knowledge-source";
      for(const [value,key]of [["materials","knowledgeMaterials"],["quotes","knowledgeQuotes"],["graph","knowledgeRelations"],["rag","organizeKnowledgeDocuments"],["learning","organizeKnowledgeLearning"],["curated","organizeKnowledgeVerified"],["all","organizeKnowledgeAll"]]){const option=element("option","",t(key));option.value=value;select.append(option);}
      select.value=memoryGroupState.libraries.knowledge.source;select.addEventListener("change",()=>chooseKnowledgeView(select.value));label.append(select);archive.append(label);output.append(button(t("organizeKnowledgeManage"),openKnowledgeDocuments),archive);
    }else if(group==="learning"){
      output.append(button(t("organizeTodo"),()=>{if(promptSwitchBlocked())return;if(window.parent!==window&&typeof window.parent.openTodoPanel==="function"){window.parent.closeMemoryPanel?.();window.parent.openTodoPanel();}else location.assign("/web/?panel=todo");},""),element("span","detail-note",t("organizeTodoUse")));
    }
  }
  function librarySource(row,group,entry=false){
    const kind=entry?"memory_entry":row.source_type||"stored_knowledge",id=String(row.id||row.source_id||row.key||"");
    let origin=row.source_location||"",prefix="";
    if(entry){origin="memory_entries#"+id+".value";prefix=origin;}
    else if(["user_statement","user_feedback"].includes(kind)){origin="chat_messages#"+row.source_id+".content";prefix=origin;}
    else if(kind==="learning_knowledge"){prefix="knowledges#"+row.source_id+".";origin=prefix;}
    else if(["knowledge_fact","knowledge_graph_fact","knowledge_graph_legacy"].includes(kind)){prefix=".openkitty/knowledge_graph.db:triples#"+row.source_id;origin=prefix;}
    const existing=prefix&&promptState.sources.find(source=>source.source_label===prefix||prefix.endsWith(".")&&source.source_label?.startsWith(prefix));
    if(existing){if(entry)existing._memoryEntry=row;if(kind==="learning_knowledge"&&row.original_content){existing._articleContent=row.original_content;existing._articleContentTruncated=!!row.content_truncated;}return null;}
    return {id:"saved:"+group+":"+kind+":"+id,kind,category:group,title:row.key||row.title||t("inputUnnamedSource"),source_label:origin||row.source_type,
      excerpt:row.value||"",mode:"stored",injection:"",created_at:row.created_at,updated_at:row.updated_at,_library:true,_libraryGroup:group,_libraryKey:kind+":"+id,_libraryContent:row.original_content||row.value||"",_libraryRow:Object.assign({},row,{_type:entry?"memory":"supplemental"})};
  }
  async function loadMemoryLibrary(group,force=false){
    const library=memoryGroupState.libraries[group];if(!library||!promptState.agent||library.loading&&!force||library.loaded&&!force)return;
    library.controller?.abort();library.controller=new AbortController();const controller=library.controller,agent=promptState.agent;library.loading=true;library.error=false;renderMemoryGroups();
    const params=new URLSearchParams({agent,view:group,limit:group==="knowledge"?"20":"100",offset:String(library.offset)});if(group==="knowledge")params.set("knowledge_source",library.source);
    try{
      const data=await responseJSON(await fetch("/api/memory?"+params,{cache:"no-store",signal:controller.signal}));if(controller!==library.controller||agent!==promptState.agent)return;
      library.sessions=Array.isArray(data.sessions)?data.sessions:[];
      const provenance=new Map((data.entry_provenance||[]).map(item=>[item.id,item]));
      const rows=[];for(const row of data.entries||[]){const source=librarySource(Object.assign({},row,{provenance:provenance.get(row.id)}),group,true);if(source)rows.push(source);}for(const row of data.supplemental||[]){const source=librarySource(row,group);if(source)rows.push(source);}
      library.items=rows;library.total=group==="knowledge"&&Number.isSafeInteger(data.sources_total)?data.sources_total:(data.total||0)+(data.supplemental?.length||0);if(group==="knowledge"&&library.source==="materials")library.materialTotal=library.total;library.loaded=true;
    }catch(error){if(controller===library.controller&&error.name!=="AbortError"){library.error=true;library.loaded=false;}}
    finally{if(controller===library.controller){library.loading=false;if(memoryGroupState.group===group)renderPromptSources();else renderMemoryGroups();}}
  }
  function refreshMemoryLibraries(){for(const library of Object.values(memoryGroupState.libraries)){library.items=[];library.sessions=[];library.loaded=false;library.offset=0;}for(const [group,library]of Object.entries(memoryGroupState.libraries)){if(group==="knowledge"){library.materialTotal=null;library.materialController?.abort();if(library.source!=="materials")refreshKnowledgeMaterialCount();}loadMemoryLibrary(group,true);}}
  function promptSwitchBlocked(){if(state.busy||inputState.busy||window.getMacsMemoryDirtyState?.().dirty){promptNotice(t("inputSourceDraftPending"),true);renderPromptControls();return true;}return false;}
  function renderPromptControls(){
    const disabled=state.busy||inputState.busy;
    const fill=(id,rows,value)=>{const select=$(id);select.replaceChildren(...rows.map(row=>{const option=element("option","",row.label);option.value=row.id;return option;}));select.value=value;select.disabled=disabled||!rows.length;};
    fill("prompt-agent",promptState.agents.map(name=>({id:name,label:name})),promptState.agent);
    const categories=[...new Set(promptState.sources.map(source=>source.category||"unknown"))];fill("prompt-category",[{id:"",label:t("inputAllCategories")},...categories.map(category=>({id:category,label:inputCategoryName(category)}))],promptState.filter);
    $("prompt-refresh").disabled=disabled;document.querySelectorAll("#prompt-sources button,#prompt-detail button").forEach(button=>button.disabled=disabled);renderMemoryGroups();
  }
  function renderPromptProviders(){
    const output=$("prompt-provider-list"),steps=element("ol","memory-use-steps");output.replaceChildren(element("p","memory-guide-intro",t("memoryGuideIntro")));
    for(const [index,name] of ["Save","Task","Find","Use"].entries()){const step=element("li","memory-use-step");step.append(element("span","memory-step-number",index+1),element("h4","",t("memoryGuide"+name+"Title")),element("p","",t("memoryGuide"+name+"Body")));steps.append(step);}output.append(steps);
    const example=element("aside","memory-guide-example");example.append(element("h4","",t("memoryGuideExampleTitle")),element("p","",t("memoryGuideExample")));output.append(example,element("p","memory-guide-boundary",t("memoryGuideBoundary")),element("h3","memory-guide-sources-title",t("memoryGuideSources")));
    const cards=element("div","memory-guide-sources"),technical=element("details","memory-guide-technical");technical.append(element("summary","",t("memoryGuideTechnical")));
    for(const provider of promptState.providers){const card=element("article","prompt-provider"),heading=element("div","prompt-provider-heading"),label=COPY[language()]["promptProvider_"+provider.id]||provider.label||provider.id,description=COPY[language()]["promptProviderBody_"+provider.id]||t("memoryGuideUnknown");heading.append(element("h4","",label),element("span","badge",COPY[language()]["memoryGuideMode_"+provider.mode]||promptModeName(provider.mode)));card.append(heading,element("p","",description));
      const path=COPY[language()]["memoryGuidePath_"+provider.id];if(path){const flow=element("ol","memory-source-path");for(const text of path.split("|"))flow.append(element("li","",text));card.append(flow);}
      const status=provider.available===false?t("memoryGuideUnavailable"):provider.count===0?t("memoryGuideEmpty"):Number.isSafeInteger(provider.count)?t("memoryGuideCount",{count:provider.count}):"";if(status)card.append(element("p","detail-note",status));cards.append(card);
      if(provider.code_path){const item=element("div","memory-guide-code");item.append(element("strong","",label),element("code","",provider.code_path));technical.append(item);}
    }
    output.append(cards);if(technical.children.length>1)output.append(technical);
  }
  function renderPromptSources(){
    renderPromptControls();renderPromptProviders();const output=$("prompt-sources"),detail=$("prompt-detail"),group=memoryGroupState.group,library=memoryGroupState.libraries[group],records=organizedRecords();
    output.replaceChildren();detail.replaceChildren();detail.hidden=true;$("prompt-summary").replaceChildren();if(!promptState.loaded)return;
    const count=records.length,origins=records.reduce((total,record)=>total+record.members.length,0),activePreferences=activePreferenceRecords().length;
    if(group==="preferences")$("prompt-summary").append(element("span","",t(preferenceView()==="history"?"memoryHistoryCount":"organizeActivePreferences",{count:preferenceView()==="history"?count:activePreferences})));
    else $("prompt-summary").append(element("span","",t("organizeRecords",{count})));
    if(origins!==count)$("prompt-summary").append(element("span","",t("organizeOrigins",{count:origins})));
    if(library?.error)output.append(empty(t("readFailure"),t("organizeUnavailable"),t("retry"),()=>loadMemoryLibrary(group,true)));
    if(!records.length){output.append(empty(t(library?.loading?"loading":groupCopyKey(group)),library?.loading?"":group==="knowledge"&&library.source==="materials"?t("knowledgeEmptyMaterials"):t("organizeNo"+group.charAt(0).toUpperCase()+group.slice(1))));$("prompt-browser").classList.add("empty-browser");return;}
    const sectionFor=record=>group==="all"?record.members.some(item=>item.injection==="system")?"organizeProject":record.members.some(item=>/:preference$/.test(item.kind||""))?"organizePreferences":record.members.some(item=>item.kind==="tool_learning"||item.category==="knowledge")?"organizeSavedLearningNotes":record.members.some(item=>["task_execution","task_insight"].includes(item.kind))?"organizeTaskMaterial":"organizeSavedReferences":group==="preferences"?preferenceView()==="history"?"":"organizeActivePreferences":"";
    records.sort((a,b)=>sectionFor(a).localeCompare(sectionFor(b))||new Date(b.members[0].updated_at||b.members[0].created_at||0)-new Date(a.members[0].updated_at||a.members[0].created_at||0));
    $("prompt-browser").classList.remove("empty-browser");if(!records.some(record=>record.members.some(source=>source.id===promptState.selected)))promptState.selected=records[0].members[0].id;
    let previousSection="";
    for(const record of records){const source=record.members[0],section=sectionFor(record);if(section&&section!==previousSection){output.append(element("h4","card-group-label",t(section,{count:section==="organizeActivePreferences"?activePreferences:count-activePreferences})));previousSection=section;}const selected=record.members.some(item=>item.id===promptState.selected),card=button("",()=>{promptState.selected=source.id;promptState.deleting="";renderPromptSources();detail.scrollTop=0;[...output.querySelectorAll("button")].find(item=>item.dataset.sourceId===source.id)?.focus({preventScroll:true});if(window.matchMedia("(max-width: 760px)").matches)detail.scrollIntoView({block:"start",behavior:"smooth"});},"memory-card");card.dataset.sourceId=source.id;card.setAttribute("aria-pressed",String(selected));card.setAttribute("aria-controls","prompt-detail");
      const meta=element("span","prompt-card-mechanism");meta.append(element("span","badge"+(source.mode==="direct"?"":" readonly"),promptSourceModeName(source)));if(record.members.length>1)meta.append(element("span","",t("organizeOrigins",{count:record.members.length})));
      if(group==="knowledge"){meta.prepend(element("span","knowledge-type",knowledgeType(source)));if(source._libraryRow?.source_id)meta.append(element("span","",t("knowledgeRecordID",{id:source._libraryRow.source_id})));}
      card.append(element("span","card-title",organizedTitle(source)),element("span","card-preview",organizedPreview(source)),meta,element("time","card-meta",dateLabel(source.updated_at||source.created_at)));output.append(card);
    }
    if(library?.loaded&&library.total>library.offset+20&&group==="knowledge"||group==="knowledge"&&library?.offset>0){const pager=element("div","organized-pager"),previous=button(t("previous"),()=>{if(promptSwitchBlocked())return;library.offset=Math.max(0,library.offset-20);loadMemoryLibrary(group,true);}),next=button(t("next"),()=>{if(promptSwitchBlocked())return;library.offset+=20;loadMemoryLibrary(group,true);});previous.disabled=library.offset===0;next.disabled=library.offset+20>=library.total;pager.append(previous,element("span","",(library.offset+1)+"–"+Math.min(library.offset+20,library.total)+" / "+library.total),next);output.append(pager);}
    const record=records.find(item=>item.members.some(source=>source.id===promptState.selected));detail.hidden=false;renderPromptDetail(record.members.find(source=>source.id===promptState.selected),record);renderPromptControls();
  }
  function renderPromptDetail(source,record){
    const detail=$("prompt-detail"),current=currentPromptSource(source),inKnowledge=memoryGroupState.group==="knowledge",inPreferences=memoryGroupState.group==="preferences",metadata=document.createDocumentFragment();
    detail.append(element("h3","detail-title",organizedTitle(source)));
    const mechanism=element("div","prompt-source-mechanism");mechanism.append(element("span","badge",promptSourceModeName(source)));detail.append(mechanism);
    const aboutKey=({task_execution:"memoryTaskResultAbout",task_insight:"memoryTaskSummaryAbout",tool_learning:"memoryNoteAbout",summary:"memorySummaryAbout"})[source.kind];
    if(aboutKey)metadata.append(element("p","memory-content-explanation",t(aboutKey)));
    if(memoryGroupState.group==="all"){
      const origin=element("details","knowledge-origin"),fields=element("dl");
      for(const [key,value]of [["kind",memorySourceType(source)],["memoryScope",memorySourceScope(source)],["memoryPosition",source.source_label],["memoryWhen",t(({direct:"memoryDirectUse",relevant:"memoryRelevantUse",on_demand:"memoryReadUse"})[source.mode]||"organizeStored")],["updated",source.updated_at||source.created_at?dateLabel(source.updated_at||source.created_at):null]])if(value)fields.append(element("dt","",t(key)),element("dd","",value));origin.prepend(element("summary","",t("memoryOriginSummary")));origin.append(fields);metadata.append(origin);
    }
    if(inKnowledge){
      const origin=element("details","knowledge-origin"),fields=element("dl");
      const graph=source._libraryRow?.graph,quote=source.kind==="knowledge_graph_verbatim_legacy",file=source.kind==="tool_learning";
      for(const [key,value]of [["kind",knowledgeType(source)],["knowledgeRelatedObject",graph?.subject||quote&&source._libraryRow.key],["source",file?t("promptProvider_tool_learning"):graph?t(String(graph.origin||"").startsWith("conversation")?"knowledgeSourceConversation":"knowledgeSourceExtraction"):quote?t("knowledgeSourceStoredText"):source.kind==="rag_knowledge"?t("knowledgeSourceCollection"):t("knowledgeSourceTask")],["knowledgeArticle",graph||quote?t("knowledgeNoArticle"):file?source.title:null],["knowledgeFile",file?source.source_label:null],["updated",source.updated_at||source.created_at?dateLabel(source.updated_at||source.created_at):null]])if(value)fields.append(element("dt","",t(key)),element("dd","",value));origin.prepend(element("summary","",t("memoryOriginSummary")));origin.append(fields);
      if(graph)origin.append(element("p","detail-note",t("knowledgeNoArticleHelp")));else if(quote)origin.append(element("p","detail-note",t("knowledgeQuoteHelp")));else if(file)origin.append(element("p","detail-note",t("knowledgeReadNote")));metadata.append(origin);
    }
    if(memoryGroupState.group==="preferences"){
      const scope=source.kind==="legacy_preference"||source._libraryRow?.graph?"unknown":source._libraryRow?.scope||/^long_term:(user|project|session|global):/.exec(source.kind||"")?.[1]||"unknown";
      metadata.append(element("p","detail-note",t("organizePreferenceScope")+" · "+t("organizePreferenceScope_"+scope)));
      if(source.kind==="legacy_preference")metadata.append(element("p","detail-note",t("organizePreferenceReview")));
    }
    if(record?.members.length>1){const label=element("label","source-record-selector",t("organizeSourceRecord")),select=element("select");select.id="prompt-origin-record";for(const member of record.members){const title=inPreferences&&member._libraryRow?.graph?dateLabel(member.updated_at||member.created_at)+" · #"+(member._libraryRow.source_id||member._libraryRow.id):member.kind==="task_execution"?t("organizeResult"):member.kind==="task_insight"?t("organizeLesson"):organizedTitle(member);const option=element("option","",title);option.value=member.id;select.append(option);}select.value=source.id;select.addEventListener("change",()=>{if(promptSwitchBlocked())return;promptState.selected=select.value;promptState.deleting="";renderPromptSources();});label.append(select);detail.append(label);if(!inPreferences)detail.append(element("p","detail-note",t("organizeOnlySelectedDelete")));}
    const graph=source._libraryRow?.graph,factStatus=window.MacsMemoryGroups.factStatus(graph);
    if(graph){
      const facts=element("div","graph-record-explanation"),statuses=element("div","actions");facts.append(element("h4","",t(inPreferences?"preferenceOldClaim":"organizeClaim")),element("p","value",graphClaim(graph)));
      if(factStatus.expired)statuses.append(element("span","badge pending",t("organizeFactExpired")));
      else if(factStatus.future)statuses.append(element("span","badge pending",t("organizeFactFuture")));
      if(factStatus.evidence!=="quote_verified")statuses.append(element("span","badge pending",t(factStatus.evidence==="missing"?"organizeFactMissingEvidence":"organizeFactUnverifiedEvidence")));
      if(statuses.children.length)facts.append(statuses);
      if(inPreferences){const evidence=element("section","preference-evidence");evidence.append(element("h4","",t("preferenceBasis")));if(graph.quoted_evidence)evidence.append(element("p","detail-note",t(factStatus.evidence==="quote_verified"?"preferenceOriginalQuote":"preferenceUnverifiedQuote")),element("blockquote","",graph.quoted_evidence));else evidence.append(element("p","preference-evidence-missing",t(window.MacsMemoryGroups.preferenceConversations(source._libraryRow,memoryGroupState.libraries.preferences.sessions).length?"preferenceLinkedQuoteNote":"preferenceMissingQuote")));appendPreferenceConversation(evidence,source._libraryRow);facts.append(evidence,element("p","detail-note",t("preferenceReviewUse")));}
      else {if(graph.quoted_evidence)facts.append(element("h4","",t("memorySavedQuote")),element("p","value",graph.quoted_evidence));facts.append(element("p","detail-note",t("organizeFactArchivedUse")));}
      if(graph.predicate==="has_capability")facts.append(element("p","detail-note",t("organizeFactCapabilityUse")));detail.append(facts);
    }
    const savedArticle=source._articleContent,summaryOnly=!!savedArticle&&/\.summary$/.test(source.source_label||"");
    const now=element("section","organized-content");if(!graph&&source.kind!=="legacy_preference")now.append(element("h4","",t(inPreferences?"preferenceRequirement":inKnowledge&&!savedArticle?/\.summary$/.test(source.source_label||"")?"knowledgeSavedSummary":"knowledgeSavedContent":"organizeBody")));
    if(source.kind==="task_insight"){const event=/^memory_events#([^\s.]+)\./.exec(source.source_label||"")?.[1],result=event&&promptState.sources.find(item=>item.kind==="task_execution"&&item.source_label==="memory_events#"+event+".final_answer");if(result){const full=window.MacsMemoryGroups.content(result,promptState.current).trim(),summary=String(current?.content||"").trim();if(summary&&full.includes(summary))now.append(element("p","detail-note task-summary-origin",t(full===summary?"memoryRepeatedSummary":"memoryExtractedSummary")));}}
    if(!current||current.error)now.append(element("p","detail-note",t("inputCurrentUnavailable")));
    else if(current.missing)now.append(element("p","detail-note",t("inputCurrentMissing")));
    else {
      const versioned=current.revision!==undefined&&current.revision!==null&&current.revision!=="",actions=element("div","actions");
      if(source.recall&&versioned){const toggle=button(t(source.recall.enabled?"recallPause":"recallResume"),()=>setPromptRecallEnabled(source));toggle.dataset.recallToggle="true";actions.append(toggle);metadata.append(element("p","detail-note",t("recallControlHelp")));}
      if(current.editable===true&&versioned)actions.append(button(t(inPreferences?"preferenceEdit":summaryOnly?"memoryEditSummary":"inputEditSource"),()=>openInputSourceEditor(source,"prompt"),"primary"));
      if(current.deletable===true&&versioned)actions.append(button(t(inPreferences?"preferenceDelete":"inputDeleteSource"),()=>{if(promptSwitchBlocked())return;promptState.deleting=source.id;renderPromptSources();},"danger"));
      if(source._libraryRow&&writable(source._libraryRow)){actions.append(button(t(inPreferences?"preferenceEdit":"inputEditSource"),()=>openEditor(source._libraryRow),"primary"));if(inPreferences){if(source._libraryRow.state==="candidate")actions.append(button(t("preferenceConfirmUse"),()=>mutate("confirm",source._libraryRow),"primary"));actions.append(button(t("preferenceDelete"),()=>{if(promptSwitchBlocked())return;promptState.deleting=source.id;renderPromptSources();},"danger"));}}
      else if(source._libraryGroup==="preferences")actions.append(button(t("organizeConfirmPreference"),()=>{state.agent=promptState.agent;const claim=graph?window.MacsMemoryGroups.preferenceStatement(graph,language()):"";openEditor(null,{key:graph?graph.subject+" · "+relationLabel(graph.predicate):"",value:claim,scope:"user",kind:"preference",preferenceIntent:true,reviewedSource:source,experience:true,sourceID:source.id});},"primary"));
      if(source.kind==="legacy_preference"){
        if(actions.children.length)now.append(actions);
        const original=element("details","source-detail preference-original");original.append(element("summary","",t("organizeOriginalRecord")),element("p","value",typeof current.content==="string"?current.content:t("inputCurrentUnavailable")));now.append(original);
      }else{
        if(actions.children.length)now.append(actions);
        if(!graph){let value=savedArticle||(typeof current.content==="string"?current.content:t("inputCurrentUnavailable"));if(/\.insights$/.test(source.source_label||"")){try{const items=JSON.parse(value);if(Array.isArray(items)&&items.every(item=>typeof item==="string"))value=items.map(item=>"- "+item).join("\n");}catch(_){}}now.append(["tool_learning","learning_knowledge","task_execution","task_insight"].includes(source.kind)&&window.MacsMemoryReader?window.MacsMemoryReader.render(value):element("p","value",value));}
        if(summaryOnly){const summary=element("details","source-detail");summary.append(element("summary","",t("memoryRecallSummary")),element("p","value",current.content));now.append(summary);}
        if(source._articleContentTruncated||source._libraryRow?.content_truncated)now.append(element("p","detail-note",t("memoryContentTruncated")));
        if(inKnowledge&&source.kind==="rag_knowledge")now.append(element("p","detail-note",t("knowledgeCollectionHelp")),button(t("knowledgeOpenCollection"),openKnowledgeDocuments,"primary"));
        if(!actions.children.length)now.append(element("p","detail-note",t("inputCurrentReadOnly")));
      }
    }
    if(inPreferences&&!graph)appendPreferenceEvidence(now,source);
    detail.append(now,metadata);
    if(promptState.deleting===source.id){const confirm=element("div","forget-confirm"),actions=element("div","actions");confirm.append(element("p","",t("inputDeleteConfirm")));actions.append(button(t("cancel"),()=>{promptState.deleting="";renderPromptSources();}),button(t("inputConfirmDelete"),()=>source._libraryRow&&writable(source._libraryRow)?mutate("forget",source._libraryRow):mutateInputSource("delete",source,"prompt"),"danger"));confirm.append(actions);detail.append(confirm);}
    const code=element("details","source-detail");code.append(element("summary","",t("organizeSourceDetails")));const fields=element("dl");for(const [key,value]of [["inputOrigin",source.source_label],["promptInjection",source.injection?t(({history_messages:"promptHistory",memory_prefix:"promptMemory",system:"promptSystem",tool_result:"organizeOnDemand"})[source.injection]||"promptPositionUnknown"):t("organizeStored")],["promptCodePath",source.code_path||source.path],["created",source.created_at?dateLabel(source.created_at):null],["updated",source.updated_at?dateLabel(source.updated_at):null]])if(value)fields.append(element("dt","",t(key)),element("dd","",value));code.append(fields);
    if(graph){for(const [key,value]of [["organizeFactSourceSubject",graph.subject],["organizeFactRawRelation",graph.predicate],["organizeFactValidity",[graph.valid_from,graph.valid_to].filter(Boolean).join(" → ")],["organizeFactEvidence",graph.quoted_evidence||source._libraryRow.quoted_evidence||t("organizeFactMissingEvidence")],["inputOrigin",graph.origin]])if(value)fields.append(element("dt","",t(key)),element("dd","",value));}
    if(source.excerpt&&source.excerpt!==current?.content)code.append(element("h4","",t("promptExcerpt")),element("p","value",source.excerpt));detail.append(code);
  }
  function appendPreferenceEvidence(container,source){
    const row=source._memoryEntry||source._libraryRow,section=element("section","preference-evidence");section.append(element("h4","",t("preferenceBasis")));
    if(!row)section.append(element("p","detail-note",t(memoryGroupState.libraries.preferences.loading?"preferenceQuoteLoading":"preferenceBasisUnavailable")));
    else {
      const quotes=(Array.isArray(row.evidence)?row.evidence:[]).filter(proof=>proof.role==="user"&&typeof proof.quote==="string"&&proof.quote.trim());
      if(quotes.length){for(const proof of quotes){section.append(element("blockquote","",proof.quote));const origin=element("p","detail-note",dateLabel(proof.created_at));if(proof.task_id)origin.append(document.createTextNode(" · "+t("originalTaskID")+": "+proof.task_id));section.append(origin);}}
      else if(["user_statement","user_feedback","user_explicit"].includes(row.source_type)&&row.source_quote)section.append(element("blockquote","",row.source_quote));
      else if(row.source_type==="user_explicit"||row.source_ref==="local-memory-manager")section.append(element("p","",t("preferenceManualBasis")));
      else section.append(element("p","preference-evidence-missing",t("preferenceUnknownBasis")));
      if(row.when_to_use)section.append(element("p","detail-note",t("reviewWhen")+": "+row.when_to_use));
      appendPreferenceConversation(section,row);
    }
    container.append(section);
  }
  function appendPreferenceConversation(container,row){
    const sessions=window.MacsMemoryGroups.preferenceConversations(row,memoryGroupState.libraries.preferences.sessions),section=element("section","preference-conversation-origin"),heading=element("div","conversation-origin-heading");heading.append(element("h4","",t("preferenceConversation")));section.append(heading);
    if(sessions.length){
      if(row.graph&&row.graph.evidence_availability!=="quote_verified")section.append(element("p","detail-note",t("preferenceConversationLinked")));
      for(const id of sessions){const action=button(t("preferenceConversationButton")+(sessions.length>1?" · "+id:""),()=>openPreferenceConversation(id),"conversation-open-link");action.dataset.conversationId=id;heading.append(action);const reference=element("p","conversation-reference",t("preferenceConversationReference",{id}));reference.title=reference.textContent;section.append(reference);const preview=element("div","preference-original-user-messages");preview.dataset.conversationPreview=preferenceConversationKey(id);section.append(preview);renderPreferenceConversationPreview(preview,id);}
    }else if(row.source_type==="user_explicit"&&!row.source_quote)section.append(element("p","detail-note",t("preferenceConversationManual")));
    else {
      section.append(element("p","detail-note",t(row.graph||row.source_type==="legacy_preference"?"preferenceConversationMissing":"preferenceConversationScope")));
      section.append(button(t("preferenceConversationHistory"),()=>{if(promptSwitchBlocked())return;if(window.parent!==window&&typeof window.parent.openTaskPanel==="function"){window.parent.closeMemoryPanel?.();window.parent.openTaskPanel();}else location.assign("/web/");}));
    }
    container.append(section);
  }
  function preferenceConversationKey(id){return JSON.stringify([promptState.agent,id]);}
  function clearPreferenceConversationCache(){for(const item of preferenceConversationCache.values())item.controller?.abort();preferenceConversationCache.clear();}
  function renderPreferenceConversationPreview(output,id){
    const key=preferenceConversationKey(id),item=preferenceConversationCache.get(key);output.replaceChildren();
    if(!item){output.append(element("p","detail-note",t("loading")));loadPreferenceConversationPreview(id);return;}
    if(item.loading){output.append(element("p","detail-note",t("loading")));return;}
    if(item.error){output.append(empty(t("readFailure"),item.error,t("retry"),()=>{preferenceConversationCache.delete(key);loadPreferenceConversationPreview(id);}));return;}
    const userMessages=item.messages.filter(message=>message.role==="user"),userOnly=memoryGroupState.preferencePreviewMode==="user",messages=userOnly?userMessages:item.messages,tabs=element("div","conversation-preview-tabs");
    for(const [mode,key,count] of [["all","conversationPreviewAll",item.messages.length],["user","conversationPreviewUser",userMessages.length]]){const action=button(t(key,{count}),()=>{memoryGroupState.preferencePreviewMode=mode;renderPromptSources();});action.dataset.previewMode=mode;action.setAttribute("aria-pressed",String(memoryGroupState.preferencePreviewMode===mode));tabs.append(action);}output.append(tabs);
    output.append(element("p","detail-note",t(userOnly?"preferencePreviewStats":"conversationPreviewStats",{count:messages.length,messages:item.messageCount})));
    if(item.hasMore)output.append(element("p","detail-note",t("preferencePreviewPartial")));
    if(!messages.length)output.append(element("p","detail-note",t(userOnly?"preferencePreviewEmpty":"preferenceConversationEmpty")));
    let previousDay="";
    for(const message of messages){const stamp=conversationStamp(message.created_at);if(stamp.day&&stamp.day!==previousDay){output.append(element("p","conversation-day-divider",stamp.day));previousDay=stamp.day;}if(!userOnly){output.append(conversationMessage(message));continue;}const record=element("section","preference-user-quote"),body=element("div","conversation-preview-bubble");record.dataset.messageId=message.id||"";record.append(conversationTime(message.created_at),body);body.append(element("p","value",message.content));appendConversationAttachments(body,message);output.append(record);}
  }
  async function loadPreferenceConversationPreview(id){
    const key=preferenceConversationKey(id);if(preferenceConversationCache.has(key))return;
    if(preferenceConversationCache.size>=32){const oldest=preferenceConversationCache.keys().next().value;preferenceConversationCache.get(oldest).controller?.abort();preferenceConversationCache.delete(oldest);}
    const item={loading:true,controller:new AbortController(),messages:[],messageCount:0,hasMore:false,error:""};preferenceConversationCache.set(key,item);
    try{const data=await responseJSON(await fetch("/api/session/"+encodeURIComponent(id)+"?limit=200",{cache:"no-store",signal:item.controller.signal}));if(preferenceConversationCache.get(key)!==item)return;const messages=window.MacsMemoryGroups.conversationMessages(data,id);if(!messages)throw new Error(t("preferenceConversationScope"));item.messages=messages;item.messageCount=data.messages.length;item.hasMore=data.has_more===true;}
    catch(error){if(error.name==="AbortError"||preferenceConversationCache.get(key)!==item)return;item.error=error.message;}
    finally{if(preferenceConversationCache.get(key)===item){item.loading=false;item.controller=null;document.querySelectorAll("[data-conversation-preview]").forEach(output=>{if(output.dataset.conversationPreview===key)renderPreferenceConversationPreview(output,id);});}}
  }
  function renderPreferenceConversation(){
    $("conversation-reference").textContent=t("preferenceConversationReference",{id:conversationState.sessionID});$("conversation-note").textContent=t("preferenceConversationNote");$("conversation-close").setAttribute("aria-label",t("close"));
    $("conversation-count").textContent=conversationState.error?t("readFailure"):conversationState.controller?t("loading"):t("conversationMessageCount",{count:conversationState.messages.length});
    const output=$("conversation-messages");output.replaceChildren();
    if(conversationState.error){output.append(empty(t("readFailure"),conversationState.error,t("retry"),()=>loadPreferenceConversation()));return;}
    if(!conversationState.messages.length){output.append(element("p","detail-note",t(conversationState.controller?"loading":"preferenceConversationEmpty")));return;}
    if(conversationState.hasMore)output.append(element("p","detail-note",t("preferenceConversationPartial",{count:conversationState.messages.length,total:conversationState.total})));
    let previousDay="";
    for(const message of conversationState.messages){const stamp=conversationStamp(message.created_at);if(stamp.day&&stamp.day!==previousDay){output.append(element("p","conversation-day-divider",stamp.day));previousDay=stamp.day;}output.append(conversationMessage(message));}
    if(conversationState.hasMore&&conversationState.limit<2000)output.append(button(t("preferenceConversationMore"),()=>{conversationState.limit=Math.min(2000,conversationState.limit*2);loadPreferenceConversation();}));
    else if(conversationState.hasMore)output.append(element("p","detail-note",t("preferenceConversationLimit")));
  }
  function conversationStamp(value){
    const date=new Date(value);if(!value||String(value).startsWith("0001-")||Number.isNaN(date.getTime()))return {day:"",time:t("unknownTime")};
    const locale=language()==="en"?"en-GB":"zh-CN";return {day:date.toLocaleDateString(locale,{year:"numeric",month:language()==="en"?"short":"long",day:"numeric"}),time:date.toLocaleTimeString(locale,{hour:"2-digit",minute:"2-digit",hour12:false})};
  }
  function conversationTime(value){const stamp=conversationStamp(value),time=element("time","conversation-time",stamp.time);if(value&&!Number.isNaN(new Date(value).getTime()))time.dateTime=value;time.title=dateLabel(value);return time;}
  function appendConversationAttachments(container,message){if(!Array.isArray(message.attachments))return;const files=element("div","conversation-attachments");for(const item of message.attachments)if(item?.name)files.append(element("span","conversation-attachment",item.name));if(files.children.length)container.append(files);}
  function conversationMessage(message){
    const role=["user","assistant","tool","system"].includes(message.role)?message.role:"assistant",card=element("section","history-message preference-conversation-message"),avatar=element("span","conversation-avatar",role==="user"?language()==="en"?"U":"你":role==="assistant"?"AI":role==="tool"?"↗":"·"),body=element("div","conversation-message-body"),meta=element("div","conversation-message-meta"),bubble=element("div","conversation-bubble");card.dataset.role=role;avatar.setAttribute("aria-hidden","true");meta.append(element("span","conversation-role",t(role)),conversationTime(message.created_at));body.append(meta,bubble);card.append(avatar,body);
    if(role==="assistant"&&window.MacsMemoryReader)bubble.append(window.MacsMemoryReader.render(message.content||""));else bubble.append(element("p","value",message.content||""));appendConversationAttachments(bubble,message);return card;
  }
  async function loadPreferenceConversation(){
    conversationState.controller?.abort();const controller=new AbortController(),seq=++conversationState.seq,id=conversationState.sessionID;conversationState.controller=controller;conversationState.error="";renderPreferenceConversation();$("conversation-messages").setAttribute("aria-busy","true");
    try{const data=await responseJSON(await fetch("/api/session/"+encodeURIComponent(id)+"?limit="+conversationState.limit,{cache:"no-store",signal:controller.signal}));if(seq!==conversationState.seq||!$("memory-conversation").open)return;if(data.session_id!==id)throw new Error(t("preferenceConversationScope"));conversationState.messages=(Array.isArray(data.messages)?data.messages:[]).filter(message=>["user","assistant","tool","system"].includes(message.role));conversationState.total=data.total||conversationState.messages.length;conversationState.hasMore=data.has_more===true;}
    catch(error){if(seq!==conversationState.seq||error.name==="AbortError")return;conversationState.error=error.message;}
    finally{if(seq===conversationState.seq){conversationState.controller=null;$("conversation-messages").setAttribute("aria-busy","false");if($("memory-conversation").open)renderPreferenceConversation();}}
  }
  function openPreferenceConversation(id){
    if(state.busy||inputState.busy)return;conversationState.sessionID=id;conversationState.limit=200;conversationState.messages=[];conversationState.total=0;conversationState.hasMore=false;conversationState.error="";$("memory-conversation").showModal();loadPreferenceConversation();
  }
  $("conversation-close").addEventListener("click",()=>$("memory-conversation").close());
  $("memory-conversation").addEventListener("close",()=>{conversationState.seq++;conversationState.controller?.abort();conversationState.controller=null;conversationState.messages=[];$("conversation-messages").replaceChildren();});
  async function setPromptRecallEnabled(source){
    if(promptSwitchBlocked())return;
    const current=currentPromptSource(source);if(!current?.revision||!source.recall)return;
    const enabled=!source.recall.enabled;
    setBusy(true);promptNotice("");
    try{
      await responseJSON(await fetch("/api/memory/prompt-sources",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({agent:promptState.agent,view_id:promptState.viewID,source_id:source.id,action:"set_recall_enabled",expected_revision:current.revision,expected_recall_version:source.recall.version,recall_enabled:enabled})}));
      if(await loadPromptSources()){const updated=promptState.sources.find(item=>item.source_label===source.source_label&&item.kind===source.kind);if(updated){promptState.selected=updated.id;renderPromptSources();}promptNotice(t(enabled?"recallResumeDone":"recallPauseDone"));}
    }catch(error){promptNotice(error.message,true);}finally{setBusy(false);renderPromptControls();}
  }
  async function loadPromptSources(){
    const seq=++promptState.seq;promptState.controller?.abort();promptState.controller=new AbortController();$("prompt-sources").setAttribute("aria-busy","true");if(!promptState.loaded)$("prompt-sources").replaceChildren(empty(t("loading"),""));
    try{
      const params=new URLSearchParams({include_learning:"1",mode:"management"});if(promptState.agent)params.set("agent",promptState.agent);
      const data=await responseJSON(await fetch("/api/memory/prompt-sources?"+params,{cache:"no-store",signal:promptState.controller.signal}));if(seq!==promptState.seq)return false;
      promptState.agent=typeof data.agent==="string"?data.agent:promptState.agent;promptState.viewID=typeof data.view_id==="string"?data.view_id:"";promptState.agents=Array.isArray(data.agents)?data.agents.filter(name=>typeof name==="string"):[];promptState.sources=Array.isArray(data.sources)?data.sources.filter(source=>source&&typeof source.id==="string"):[];promptState.providers=Array.isArray(data.providers)?data.providers.filter(provider=>provider&&typeof provider.id==="string"):[];promptState.current=data.current_sources&&typeof data.current_sources==="object"&&!Array.isArray(data.current_sources)?data.current_sources:Object.create(null);promptState.loaded=true;
      if(!promptState.sources.some(source=>(source.category||"unknown")===promptState.filter))promptState.filter="";
      if(state.agent!==promptState.agent){state.controller?.abort();state.loadSeq++;state.loaded=false;state.loading=false;state.metadataLoaded=false;state.session="";invalidateCatalog();}
      state.agent=promptState.agent;
      if(inputState.agent!==promptState.agent){inputState.controller?.abort();inputState.seq++;inputState.loaded=false;inputState.inputs=[];inputState.input=null;inputState.inputID="";inputState.selected="";inputState.agent=promptState.agent;inputState.session="";renderInputs();if($("recorded-model-inputs").open)loadInputs();}
      clearPreferenceConversationCache();refreshMemoryLibraries();renderPromptSources();promptNotice("");return true;
    }catch(error){if(seq!==promptState.seq||error.name==="AbortError")return false;promptState.loaded=false;$("prompt-sources").replaceChildren(empty(t("promptLoadFailure"),error.message,t("retry"),loadPromptSources));$("prompt-detail").hidden=true;$("prompt-summary").textContent="";promptNotice(error.message,true);renderPromptControls();return false;}
    finally{if(seq===promptState.seq)$("prompt-sources").setAttribute("aria-busy","false");}
  }
  $("memory-prompt-controls").addEventListener("submit",event=>{event.preventDefault();loadPromptSources();});
  $("prompt-add-preference").addEventListener("click",()=>{if(promptSwitchBlocked())return;state.agent=promptState.agent;openEditor(null,{key:"",value:"",scope:"user",kind:"preference",preferenceIntent:true});});
  $("prompt-add-memory").addEventListener("click",()=>{if(promptSwitchBlocked())return;state.agent=promptState.agent;openEditor(null,{key:"",value:"",scope:"user",kind:"fact",reviewNote:"memoryAddNote"});});
  $("prompt-agent").addEventListener("change",()=>{if(promptSwitchBlocked())return;promptState.agent=$("prompt-agent").value;promptState.session="";promptState.selected="";loadPromptSources();});
  $("prompt-category").addEventListener("change",()=>{promptState.filter=$("prompt-category").value;promptState.selected="";promptState.deleting="";renderPromptSources();});
  $("recorded-model-inputs").addEventListener("toggle",()=>{if($("recorded-model-inputs").open)loadInputs();});
  $("recorded-model-inputs").hidden=query.get("inspect")==="1"?false:true;
  function inputNotice(text,error=false){$("input-message").textContent=text;$("input-message").classList.toggle("error",error);}
  function inputSources(){return Array.isArray(inputState.input?.sources)?inputState.input.sources.filter(source=>source&&typeof source.id==="string"):[];}
  function currentInputSource(source){return Object.hasOwn(inputState.current,source.id)?inputState.current[source.id]:null;}
  function inputCategoryName(category){return t(({short:"short",session:"short",long:"long",user:"personal",preference:"preferences",preferences:"preferences",project:"project",global:"global",knowledge:"knowledge",history:"history"})[category]||"inputCategoryUnknown");}
  function inputStatusName(status){return t(({succeeded:"inputSucceeded",attempted:"inputAttempted",failed:"inputFailed"})[status]||"inputStatusUnknown");}
  function inputDraftKey(source){return inputState.inputID+"|"+source.id;}
  function inputEditingDirty(){const ctx=inputState.editorContext;return $("memory-source-editor").open&&ctx&&$("source-editor-value").value!==ctx.initial;}
  function blockInputSwitch(){if(state.busy||inputState.busy||inputEditingDirty()||inputState.drafts.size||window.getMacsMemoryDirtyState?.().dirty){inputNotice(t("inputSourceDraftPending"),true);renderInputControls();return true;}return false;}
  function renderInputControls(){
    const disabled=inputState.busy||state.busy;
    const fill=(id,rows,value)=>{const select=$(id);select.replaceChildren(...rows.map(row=>{const option=element("option","",row.label);option.value=row.id;return option;}));select.value=value;select.disabled=disabled||!rows.length;};
    fill("input-agent",inputState.agents.map(name=>({id:name,label:name})),inputState.agent);
    fill("input-session",[{id:"",label:t("inputLatestTask")},...inputState.sessions.map(session=>({id:session.id,label:session.name||session.id}))],inputState.session);
    fill("input-call",inputState.inputs.map(input=>({id:input.id,label:t("inputCallLabel",{index:input.call_index??t("unknownTime"),time:dateLabel(input.created_at)})})),inputState.inputID);
    const kinds=[...new Set(inputSources().map(source=>source.category||"unknown"))];
    fill("input-category",[{id:"",label:t("inputAllCategories")},...kinds.map(kind=>({id:kind,label:inputCategoryName(kind)}))],inputState.filter);
    for(const id of ["input-call","input-category"])$(id).closest("label").hidden=inputState.loaded&&!inputState.input&&!inputState.inputs.length;
    $("input-refresh").disabled=disabled;
    for(const id of ["source-editor-save","source-editor-close","source-editor-cancel"])$(id).disabled=disabled;
    document.querySelectorAll("#input-sources button,#input-detail button").forEach(item=>item.disabled=disabled);
  }
  function renderInputs(){
    renderInputControls();const output=$("input-sources"),detail=$("input-detail"),sources=inputSources(),visible=sources.filter(source=>!inputState.filter||(source.category||"unknown")===inputState.filter);
    output.replaceChildren();$("input-summary").replaceChildren();detail.replaceChildren();detail.hidden=true;
    if(!inputState.loaded)return;
    $("memory-input-view").classList.toggle("no-input-record",!inputState.input);
    if(!inputState.input){output.append(empty(t("inputNoReceipt"),t("inputNoReceiptBody")));$("input-browser").classList.add("empty-browser");return;}
    const summary=$("input-summary");summary.append(element("span","badge"+(inputState.input.status==="failed"?" pending":""),inputStatusName(inputState.input.status)),element("span","",t("inputSourceCount",{count:sources.length})),element("time","",dateLabel(inputState.input.created_at)));
    if(!visible.length){output.append(empty(t(inputState.filter?"inputNoFilter":"inputNoSources"),t(inputState.filter?"inputNoFilterBody":"inputNoSourcesBody")));$("input-browser").classList.add("empty-browser");return;}
    $("input-browser").classList.remove("empty-browser");
    if(!visible.some(source=>source.id===inputState.selected))inputState.selected=visible[0].id;
    for(const source of visible){const card=button("",()=>{inputState.selected=source.id;inputState.deleting="";renderInputs();[...output.querySelectorAll("button")].find(item=>item.dataset.sourceId===source.id)?.focus({preventScroll:true});},"memory-card");card.dataset.sourceId=source.id;card.setAttribute("aria-pressed",String(source.id===inputState.selected));card.append(element("span","card-title",source.title||t("inputUnnamedSource")),element("span","card-preview",source.excerpt||t("inputExcerptMissing")),element("span","card-meta",inputCategoryName(source.category)),element("span","card-origin",source.source_label||t("inputNoLocation")));output.append(card);}
    const source=visible.find(source=>source.id===inputState.selected);detail.hidden=false;renderInputDetail(source);renderInputControls();
  }
  function renderInputDetail(source){
    const detail=$("input-detail"),current=currentInputSource(source),receipt=inputState.input;
    detail.append(element("h3","detail-title",source.title||t("inputUnnamedSource")));
    const captured=element("section","input-captured");captured.append(element("h4","",t("inputExcerpt")),element("p","value",source.excerpt||t("inputExcerptMissing")));detail.append(captured);
    const location=element("dl","input-origin-fields");location.append(element("dt","",t("inputOrigin")),element("dd","",source.source_label||t("inputNoLocation")));
    detail.append(location);
    const now=element("section","input-current");now.append(element("h4","",t("inputCurrentContent")));
    if(!current||current.error){now.append(element("p","detail-note",t("inputCurrentUnavailable")));}
    else if(current.missing){now.append(element("p","detail-note",t("inputCurrentMissing")));}
    else {
      now.append(element("p","value",typeof current.content==="string"?current.content:t("inputCurrentUnavailable")));
      if(typeof current.digest==="string"&&typeof source.captured_revision==="string"&&current.digest!==source.captured_revision)now.append(element("p","detail-note input-changed",t("inputRevisionChanged")));
      if(current.editable!==true&&current.deletable!==true)now.append(element("p","detail-note",t("inputCurrentReadOnly")));
      const actions=element("div","actions");const versioned=current.revision!==undefined&&current.revision!==null&&current.revision!=="";
      if(current.editable===true&&versioned)actions.append(button(t("inputEditSource"),()=>openInputSourceEditor(source),"primary"));
      if(current.deletable===true&&versioned)actions.append(button(t("inputDeleteSource"),()=>{if(blockInputSwitch())return;inputState.deleting=source.id;renderInputs();},"danger"));
      if(actions.children.length)now.append(actions);
      if((current.editable===true||current.deletable===true)&&!versioned)now.append(element("p","detail-note",t("inputSourceNoRevision")));
    }
    detail.append(now);
    if(inputState.deleting===source.id){const confirm=element("div","forget-confirm");confirm.append(element("p","",t("inputDeleteConfirm")));const actions=element("div","actions");actions.append(button(t("cancel"),()=>{inputState.deleting="";renderInputs();}),button(t("inputConfirmDelete"),()=>mutateInputSource("delete",source),"danger"));confirm.append(actions);detail.append(confirm);}
    const meta=element("details","source-detail");meta.append(element("summary","",t("sourceDetails")));const fields=element("dl");for(const [key,value]of [["inputRecordTime",dateLabel(receipt.created_at)],["inputTaskID",receipt.task_id],["inputRecordID",receipt.id],["inputSourceID",source.id],["inputCapturedRevision",typeof source.captured_revision==="string"?source.captured_revision.slice(0,8):null],["inputCurrentRevision",typeof current?.digest==="string"?current.digest.slice(0,8):null]])if(value)fields.append(element("dt","",t(key)),element("dd","",value));meta.append(fields);detail.append(meta);
  }
  async function loadInputs(){
    const seq=++inputState.seq;inputState.controller?.abort();inputState.controller=new AbortController();$("input-sources").setAttribute("aria-busy","true");
    if(!inputState.loaded)$("input-sources").replaceChildren(empty(t("loading"),""));
    try{
      const params=new URLSearchParams();if(inputState.agent)params.set("agent",inputState.agent);if(inputState.session)params.set("session_id",inputState.session);if(inputState.inputID)params.set("input_id",inputState.inputID);
      const data=await responseJSON(await fetch("/api/memory/inputs?"+params,{cache:"no-store",signal:inputState.controller.signal}));if(seq!==inputState.seq)return false;
      inputState.agent=typeof data.agent==="string"?data.agent:inputState.agent;inputState.agents=Array.isArray(data.agents)?data.agents.filter(name=>typeof name==="string"):[];inputState.sessions=Array.isArray(data.sessions)?data.sessions.filter(session=>session&&typeof session.id==="string"):[];
      inputState.inputs=Array.isArray(data.inputs)?data.inputs.filter(input=>input&&typeof input.id==="string"):[];inputState.input=data.input&&typeof data.input.id==="string"?data.input:null;
      inputState.inputID=inputState.input?.id||"";if(inputState.input?.session_id)inputState.session=inputState.input.session_id;
      inputState.current=data.current_sources&&typeof data.current_sources==="object"&&!Array.isArray(data.current_sources)?data.current_sources:Object.create(null);inputState.loaded=true;
      if(!inputSources().some(source=>(source.category||"unknown")===inputState.filter))inputState.filter="";
      renderInputs();inputNotice("");return true;
    }catch(error){if(seq!==inputState.seq||error.name==="AbortError")return false;inputState.loaded=false;$("input-sources").replaceChildren(empty(t("inputLoadFailure"),error.message,t("retry"),loadInputs));$("input-detail").hidden=true;$("input-summary").replaceChildren();inputNotice(error.message,true);renderInputControls();return false;}
    finally{if(seq===inputState.seq)$("input-sources").setAttribute("aria-busy","false");}
  }
  function openInputSourceEditor(source,mode="receipt"){
    if(inputState.busy||state.busy)return;const viewer=mode==="prompt"?promptState:inputState,current=mode==="prompt"?currentPromptSource(source):currentInputSource(source);if(!current||current.missing||current.error||current.editable!==true)return;
    const key=mode==="prompt"?"prompt|"+viewer.agent+"|"+viewer.session+"|"+source.kind+"|"+(source.source_label||source.id):"receipt|"+viewer.inputID+"|"+source.id,draft=inputState.drafts.get(key);inputState.editorContext={source,mode,viewID:viewer.viewID,inputID:viewer.inputID,agent:viewer.agent,current:Object.assign({},current),revision:current.revision,initial:current.content||"",draftKey:key,conflicted:false};
    $("source-editor-value").value=draft?.value??current.content??"";$("source-editor-location").textContent=source.source_label||t("inputNoLocation");$("source-editor-note").textContent=t(mode==="prompt"?"promptEditNote":"inputEditNote");$("source-editor-error").hidden=true;$("source-editor-save").disabled=false;$("memory-source-editor").showModal();$("source-editor-value").focus();
  }
  function retainInputDraft(){const ctx=inputState.editorContext;if(!ctx)return;const value=$("source-editor-value").value;if(value!==ctx.initial)inputState.drafts.set(ctx.draftKey,{value});else inputState.drafts.delete(ctx.draftKey);document.dispatchEvent(new CustomEvent("macs:memory-statechange",{detail:{saving:inputState.busy}}));}
  function closeInputSourceEditor(){if(!inputState.busy){retainInputDraft();$("memory-source-editor").close();}}
  async function mutateInputSource(action,source,requestedMode="receipt"){
    if(inputState.busy||state.busy)return;const ctx=inputState.editorContext,mode=action==="update"?ctx?.mode:requestedMode,viewer=mode==="prompt"?promptState:inputState,current=action==="update"?ctx?.current:mode==="prompt"?currentPromptSource(source):currentInputSource(source),notify=mode==="prompt"?promptNotice:inputNotice;
    if(!current||current.missing||current.error||current[action==="update"?"editable":"deletable"]!==true){notify(t("inputSourceReadonly"),true);return;}
    const revision=action==="update"?ctx?.revision:current.revision;if(revision===undefined||revision===null||revision===""){notify(t("inputSourceNoRevision"),true);return;}
    const payload={agent:action==="update"?ctx.agent:viewer.agent,source_id:source.id,action,expected_revision:revision};if(mode==="prompt")payload.view_id=action==="update"?ctx.viewID:viewer.viewID;else payload.input_id=action==="update"?ctx.inputID:viewer.inputID;
    if(action==="update"){if(ctx.conflicted)return;payload.value=$("source-editor-value").value;if(!payload.value.trim()){$("source-editor-error").textContent=t("inputSourceRequired");$("source-editor-error").hidden=false;return;}}
    inputState.busy=true;setBusy(true);renderInputControls();renderPromptControls();$("source-editor-error").hidden=true;
    try{
      await responseJSON(await fetch(mode==="prompt"?"/api/memory/prompt-sources":"/api/memory/inputs",{method:"POST",cache:"no-store",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)}));
      if(action==="update"){inputState.drafts.delete(ctx.draftKey);$("memory-source-editor").close();inputState.editorContext=null;}
      viewer.deleting="";invalidateCatalog();state.loaded=false;await (mode==="prompt"?loadPromptSources():loadInputs());notify(t(mode==="prompt"?(action==="update"?"promptSourceSaved":"promptSourceDeleted"):(action==="update"?"inputSourceSaved":"inputSourceDeleted")));
    }catch(error){const text=error.status===409?t("inputSourceConflict"):error.message;if(action==="update"){retainInputDraft();if(error.status===409)ctx.conflicted=true;$("source-editor-error").textContent=text;$("source-editor-error").hidden=false;}else notify(text,true);}
    finally{inputState.busy=false;setBusy(false);renderInputControls();renderPromptControls();if(inputState.editorContext?.conflicted)$("source-editor-save").disabled=true;}
  }
  function translateInputs(){renderInputs();$("input-sources").setAttribute("aria-label",t("inputTitle"));$("input-detail").setAttribute("aria-label",t("recordDetail"));$("source-editor-close").setAttribute("aria-label",t("close"));}
  COPY.zh.inputLatestTask="最近一次有记录的任务";COPY.en.inputLatestTask="Latest task with a record";
  $("memory-input-controls").addEventListener("submit",event=>{event.preventDefault();loadInputs();});
  $("input-agent").addEventListener("change",()=>{if(blockInputSwitch())return;inputState.agent=$("input-agent").value;inputState.session="";inputState.inputID="";inputState.selected="";state.metadataLoaded=false;state.loaded=false;invalidateCatalog();loadInputs();});
  $("input-session").addEventListener("change",()=>{if(blockInputSwitch())return;inputState.session=$("input-session").value;inputState.inputID="";inputState.selected="";loadInputs();});
  $("input-call").addEventListener("change",()=>{if(blockInputSwitch())return;inputState.inputID=$("input-call").value;inputState.selected="";loadInputs();});
  $("input-category").addEventListener("change",()=>{inputState.filter=$("input-category").value;inputState.selected="";inputState.deleting="";renderInputs();});
  $("source-editor-form").addEventListener("submit",event=>{event.preventDefault();if(inputState.editorContext)mutateInputSource("update",inputState.editorContext.source);});
  $("source-editor-close").addEventListener("click",closeInputSourceEditor);$("source-editor-cancel").addEventListener("click",closeInputSourceEditor);
  $("memory-source-editor").addEventListener("cancel",event=>{if(inputState.busy)event.preventDefault();else retainInputDraft();});
  $("source-editor-value").addEventListener("input",()=>document.dispatchEvent(new CustomEvent("macs:memory-statechange",{detail:{saving:inputState.busy}})));
  $("other-memory-storage").addEventListener("toggle",()=>{if($("other-memory-storage").open&&!state.loaded&&!state.loading&&!state.busy)load();});
  const reasons={relevant:"reasonRelevant",semantic:"reasonSemantic",hybrid:"reasonHybrid",pinned:"reasonPinned",session_working:"reasonSession",not_relevant:"reasonNotRelevant",scope_override:"reasonOverride",retracted:"reasonRetracted",duplicate:"reasonDuplicate",budget:"reasonBudget",pinned_budget:"reasonPinnedBudget",safety_filter:"reasonSafety",stale:"reasonStale",version_changed:"reasonVersionChanged",disabled_by_user:"reasonRecallDisabled"};
  function recallSource(source="") {if(["user_statement","user_feedback","task_execution","task_insight","learning_knowledge","knowledge_fact"].includes(source))return sourceLabel(source);if(source.startsWith("long_term:user:"))return t("personal");if(source.startsWith("long_term:project:"))return t("project");if(source.startsWith("long_term:global:"))return t("global");if(source.startsWith("long_term:session:")||source==="session_context")return t("context");return t(({summary:"summary",history_and_profile:"historyProfile",knowledge_graph:"graph",history:"history"})[source]||"supplemental");}
  async function loadTraces() {
    const seq=++state.traceSeq,agent=state.agent;state.traceController?.abort();state.traceController=new AbortController();const output=$("recall-traces");output.replaceChildren(element("p","hint",t("loading")));
    try {
      const params=new URLSearchParams({agent});if(state.category==="global")params.set("scope","global");
      const data=await responseJSON(await fetch("/api/memory/recalls?"+params,{cache:"no-store",signal:state.traceController.signal}));if(seq!==state.traceSeq||agent!==state.agent)return;
      output.replaceChildren(...(data.traces||[]).map(trace=>{
        const item=element("details","recall-record"),chosen=(trace.decisions||[]).filter(decision=>decision.selected).length;
        item.append(element("summary","",dateLabel(trace.created_at)+" · "+t(({ready:"recallReady",redacted:"recallRedacted",empty:"recallEmpty"})[trace.status]||"uncategorized")+" · "+t("recallSelected",{count:chosen})),element("p","hint",t(({disabled:"keyword",ready:"semanticReady",partial:"semanticPartial",unavailable:"semanticUnavailable"})[trace.semantic_status]||"uncategorized")+" · "+trace.tokens+" "+t("tokens")));
        if(trace.status==="redacted")item.append(element("p","hint",t("redacted")));
        const rows=element("ul","recall-decisions");for(const decision of trace.decisions||[])rows.append(element("li",decision.selected?"selected":"",t(decision.selected?"selected":"excluded")+" · "+recallSource(decision.source)+" · "+t(reasons[decision.reason]||"uncategorized")+" · "+String(decision.id||"").slice(0,12)));item.append(rows);if(trace.omitted_decisions)item.append(element("p","hint",t("omitted",{count:trace.omitted_decisions})));return item;
      }));
      if(!output.children.length)output.append(empty(t("noRecall"),t("noRecallBody")));
    } catch(error){if(seq===state.traceSeq&&error.name!=="AbortError")output.replaceChildren(empty(t("readFailure"),error.message,t("retry"),loadTraces));}
  }
  function selectCategory(category) {
    if(state.busy||!categories.includes(category))return;state.category=category;state.selected="";state.forget="";state.offset=0;state.traceSeq++;state.traceController?.abort();resetFilters(false);updateControls();load();
    document.querySelector('[data-category="'+category+'"]').scrollIntoView({block:"nearest",inline:"nearest"});
  }
  function translate() {
    document.title=t("title")+" · macs";document.querySelectorAll("[data-memory-label]").forEach(item=>item.textContent=t(item.dataset.memoryLabel));
    $("search").placeholder=t("searchPlaceholder");$("memory-categories").setAttribute("aria-label",t("categories"));entries.setAttribute("aria-label",t("recordList"));detail.setAttribute("aria-label",t("recordDetail"));$("editor-close").setAttribute("aria-label",t("close"));$("themeToggle").setAttribute("aria-label",t("theme"));$("themeToggle").title=t("theme");
    updateControls();renderCatalog();translateInputs();renderPromptSources();if(state.loaded){renderCards();$("count").textContent=countLabel();$("page-number").textContent=t("page",{page:Math.floor(state.offset/pageSize)+1,pages:Math.max(1,Math.ceil(state.total/pageSize))});}
    if(editor.open)editorText();if($("memory-source-editor").open)$("source-editor-note").textContent=t(inputState.editorContext?.mode==="prompt"?"promptEditNote":"inputEditNote");if($("recall-panel").open&&!$("recall-panel").hidden)loadTraces();
    if($("memory-conversation").open)renderPreferenceConversation();
  }
  document.querySelectorAll("[data-category]").forEach(item=>item.addEventListener("click",()=>selectCategory(item.dataset.category)));
  $("new-memory").addEventListener("click",()=>openEditor());$("editor-form").addEventListener("submit",saveEditor);$("editor-close").addEventListener("click",closeEditor);$("editor-cancel").addEventListener("click",closeEditor);
  editor.addEventListener("cancel",event=>{if(state.busy)event.preventDefault();else retainDraft();});
  $("editor-scope").addEventListener("change",()=>{if(!state.editing)$("editor-kind").value=state.editContext?.preferenceIntent||$("editor-scope").value==="user"?"preference":"fact";editorText();});
  $("agent").addEventListener("change",()=>{state.agent=$("agent").value;state.session="";state.selected="";state.sessions=[];state.metadataLoaded=false;state.messages.clear();state.historySources.clear();state.traceSeq++;state.traceController?.abort();state.offset=0;load();});
  $("session").addEventListener("change",()=>{state.session=$("session").value;state.selected="";state.offset=0;load();});
  for(const key of ["scope","state","knowledge-source"])$(key).addEventListener("change",()=>{state.offset=0;state.selected="";if(key==="knowledge-source")state.archiveOpen=["all","graph","quotes"].includes($(key).value);load();});
  $("search-form").addEventListener("submit",event=>{event.preventDefault();state.offset=0;invalidateCatalog();load();});
  $("memory-catalog").addEventListener("toggle",()=>{if($("memory-catalog").open)loadCatalog();});
  $("search").addEventListener("input",()=>{clearTimeout(state.searchTimer);state.loadSeq++;state.controller?.abort();state.searchTimer=setTimeout(()=>{state.offset=0;state.selected="";load();},250);});
  $("clear-filters").addEventListener("click",()=>resetFilters());$("previous").addEventListener("click",()=>{state.offset=Math.max(0,state.offset-pageSize);state.selected="";load();});$("next").addEventListener("click",()=>{state.offset+=pageSize;state.selected="";load();});
  $("export").addEventListener("click",event=>{if($("export").getAttribute("aria-disabled")==="true")event.preventDefault();});$("recall-panel").addEventListener("toggle",()=>{if($("recall-panel").open)loadTraces();});$("refresh-recalls").addEventListener("click",loadTraces);
  document.addEventListener("openkitty:languagechange",translate);
  COPY.zh.graphQuotationReadOnly="这是工作区保存的旧知识原文，个人归属尚未核实；不代表已确认事实或用户偏好。核对来源后可另存，原文不会被修改。";COPY.en.graphQuotationReadOnly="This is a legacy knowledge quotation stored in the workspace. Personal ownership is unverified; it is not a confirmed fact or user preference. Review the source before saving separately; the original stays unchanged.";
  COPY.zh.ownershipUnverified="归属未核实";COPY.en.ownershipUnverified="Ownership unverified";
  COPY.zh.categoryDraftPending="当前记忆草稿已保留。请先保存或处理草稿，再切换到另一分类。";COPY.en.categoryDraftPending="Your memory draft is retained. Save or resolve it before switching to another category.";
  window.macsMemorySelectCategory=(category)=>{
    if(!categories.includes(category))return false;
    if(category===state.category){revealOldMemoryMaterial();return true;}
    if(state.busy||window.getMacsMemoryDirtyState?.().dirty){notice(t("categoryDraftPending"),true);return false;}
    revealOldMemoryMaterial();selectCategory(category);return true;
  };
  window.getMacsMemoryDirtyState=()=>({
    dirty:inputState.drafts.size>0||inputEditingDirty()||[...state.drafts.values()].some(item=>Boolean(item.key?.trim()||item.value?.trim()))||(editor.open&&(state.editing?$("editor-value").value!==state.editing.value:Boolean($("editor-key").value.trim()||$("editor-value").value.trim()))),
    saving:state.busy||inputState.busy,editorOpen:editor.open||$("memory-source-editor").open,agent:state.agent,session:state.session
  });
  if(query.get("embed")==="1")document.addEventListener("click",event=>{
    const link=event.target.closest("a[href]");if(link&&link.classList.contains("manage-link")){link.target="_blank";link.rel="noopener";}
  });
  $("knowledge-source").value=["curated","all","graph","quotes","learning","rag"].includes(query.get("knowledge_source"))?query.get("knowledge_source"):"curated";
  state.archiveOpen=["all","graph","quotes"].includes($("knowledge-source").value)&&state.category==="knowledge";
  translate();loadPromptSources();
  $("other-memory-storage").addEventListener("toggle",()=>{if(!$("other-memory-storage").open)$("other-memory-storage").hidden=true;});
  $("close-old-records").addEventListener("click",()=>{if(promptSwitchBlocked())return;$("other-memory-storage").open=false;$("other-memory-storage").hidden=true;$("prompt-groups").scrollIntoView({block:"start"});});
  if(categories.includes(query.get("view")||query.get("category"))){revealOldMemoryMaterial();}
})();
