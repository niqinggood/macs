package catalog

import (
	"mac/internal/customerservice"
	pipelinecore "mac/internal/pipeline"
	investmentadvisory "mac/internal/pipelines/investmentadvisory"
)

type Pipeline = pipelinecore.Pipeline
type PipelineStep = pipelinecore.PipelineStep
type PipelineOutputContract = pipelinecore.PipelineOutputContract
type PipelineSourceContract = pipelinecore.PipelineSourceContract
type PipelineArtifactContract = pipelinecore.PipelineArtifactContract
type ProfessionalAgentStep = pipelinecore.ProfessionalAgentStep
type StepRoute = pipelinecore.StepRoute
type StepRepeat = pipelinecore.StepRepeat
type StepRework = pipelinecore.StepRework
type StepExample = pipelinecore.StepExample
type PipelineDatabaseConnection = pipelinecore.PipelineDatabaseConnection

// This file owns the built-in AI employee pipeline definitions and prompts.
// Keep gateway.go focused on gateway lifecycle, persistence, and HTTP routing.
func Builtins() map[string]Pipeline {
	pipelines := map[string]Pipeline{
		// ====================== 项目助理智能路由 ======================
		"project-assistant-router": {Name: "project-assistant-router", Desc: "智能路由：LLM意图识别→自动选择站会/PR审查/发版/周报",
			Steps: []PipelineStep{
				{Name: "意图识别", Prompt: `你是项目助理的智能路由器。分析用户请求，确定意图并输出分类标签。

用户请求:

请按以下规则分类：
- 用户要开站会/晨会/standup/每日总结/昨天做了什么/今日计划 → 输出 INTENT:daily_standup
- 用户要review/code review/代码审查/PR检查/审查代码 → 输出 INTENT:pr_review
- 用户要发版/release/发布/打tag/版本发布/changelog → 输出 INTENT:release
- 用户要周报/本周总结/weekly report/工作汇报 → 输出 INTENT:weekly_report
- 用户要让新人了解项目/新人上手/onboarding/项目架构 → 输出 INTENT:onboarding
- 无法确定 → 输出 INTENT:weekly_report

只输出一行，格式: INTENT:xxx`, NoTools: true, MaxIter: 1,
					Routing: []StepRoute{
						{When: "contains 'INTENT:daily_standup'", Goto: "站会纪要生成"},
						{When: "contains 'INTENT:pr_review'", Goto: "PR审查报告"},
						{When: "contains 'INTENT:release'", Goto: "发版管理执行"},
						{When: "contains 'INTENT:onboarding'", Goto: "新人上手引导"},
						{When: "", Goto: "周报生成"},
					}},
				{Name: "站会纪要生成", Prompt: "收集昨日至今的 git 提交记录(persistent_shell: git log --since=yesterday --oneline)。按类型(feat/fix/refactor/docs/test)分类总结。生成 Markdown 站会纪要保存到 storage/daily-standup-{date}.md。格式：日期、昨日完成、今日计划、阻塞项、需协助事项。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 8},
				{Name: "PR审查报告", Prompt: "使用 git tool 获取待审查的 diff(persistent_shell: git diff)。审查：1)安全(密钥/注入/权限) 2)风格(命名/边界/并发/资源) 3)逻辑(错误处理/重复代码)。输出 PR Review 报告到 storage/pr-review-{timestamp}.md。标注严重度(高/中/低)和具体行号。", Tools: []string{"persistent_shell", "read_file", "write_file"}, MaxIter: 12},
				{Name: "发版管理执行", Prompt: "1)使用 git log 生成 Changelog(按feat/fix分类) 2)根据变更建议 semver 版本号 3)运行 code_check 验证(action=test,action=build) 4)如果验证通过，准备 tag 和发版命令。输出发版摘要到 storage/release-{version}.md。注意：实际打tag需审批。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 12, RequireApproval: true},
				{Name: "新人上手引导", Prompt: "扫描项目结构：入口文件、核心模块、依赖关系。读取 README 了解项目定位。追踪用户指定的业务流程（如未指定则选最主要的功能链路）。生成新人上手指南保存到 storage/onboarding-guide.md：环境搭建→项目结构→核心模块→推荐阅读顺序。", Tools: []string{"persistent_shell", "read_file", "write_file"}, MaxIter: 12},
				{Name: "周报生成", Prompt: "收集本周信息：git 提交记录、文件变更、TODO 完成情况。使用 persistent_shell 统计代码变更量。生成专业周报 PPT 保存到 storage/ 目录。包含：本周概览、重点工作、数据图表、下周计划。", Tools: []string{"read_file", "persistent_shell", "write_file"}, MaxIter: 10},
			}},

		"wide-research": {Name: "wide-research", Desc: "Wide Research: scope -> parallel evidence collection -> dedupe -> report",
			Steps: []PipelineStep{
				{Name: "Scope and research plan", Prompt: `Turn the user request into a bounded research plan.

Rules:
- Restate the exact research question, target entities, geography, time window, and excluded scope.
- Split the work into 3-5 independent research tracks.
- Produce concrete search queries for each track.
- Do not use historical session, memory, or unrelated project context as evidence unless it is explicitly present in the current user input.
- If the request is under-specified, state the missing fields and proceed with conservative assumptions.

Output only: scope, assumptions, research tracks, search queries, and evidence-quality requirements.`, NoTools: true, MaxIter: 2},
				{Name: "Parallel evidence collection", NoTools: true, Parallel: []PipelineStep{
					{Name: "Official and primary sources", Prompt: `Use the research plan to collect primary or official sources first: company sites, docs, filings, standards, official announcements, public datasets, or original papers.

For every useful source, capture:
- title
- URL or file path
- publisher/owner
- publish or access date when visible
- 1-2 sentence evidence note
- confidence: high/medium/low

Avoid repeating equivalent searches. If a source cannot be accessed, record the failure instead of inventing facts.`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 8},
					{Name: "Recent web coverage", Prompt: `Collect recent and reputable secondary coverage for the research plan.

Focus on sources that add facts, dates, market movement, product changes, funding, hiring, pricing, or user-visible changes. For every source, record URL, date, key claim, and whether it confirms or conflicts with primary sources.`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 8},
					{Name: "Community and discussion signals", Prompt: `Collect community or social discussion signals only when they help answer the research question.

Look for recurring complaints, praise, adoption signals, workflow evidence, and unresolved questions. Keep anecdotes separate from verified facts. Never treat social discussion as confirmed evidence without corroboration.`, Tools: []string{"web_search", "web_fetch", "platform_search"}, MaxIter: 8},
					{Name: "Competitive and adjacent landscape", Prompt: `Collect adjacent competitors, substitutes, alternatives, or comparable cases related to the research question.

For each item, capture what is comparable, what is different, and which source supports the comparison. Do not pad the list with weakly related names.`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 8},
				}},
				{Name: "Evidence consolidation", Prompt: `Merge the parallel research outputs into a single evidence ledger.

Requirements:
- Deduplicate sources by URL, title, and claim.
- Separate verified facts, likely inferences, weak signals, and contradictions.
- Flag stale evidence and missing evidence.
- Produce a source table with source type, date, claim, confidence, and track.
- Do not introduce new facts that were not present in the collected evidence.`, NoTools: true, MaxIter: 4},
				{Name: "Research report", Prompt: `Produce the final user-facing research report and save it to .openkitty/storage/wide-research-{timestamp}.md.

Report structure:
1. Executive answer
2. Key findings ranked by confidence
3. Evidence table with source links
4. Contradictions and uncertainty
5. Practical implications
6. Recommended next actions
7. Appendix: all useful sources

Be explicit about what is verified versus inferred. Include the saved file path in the final response.`, Tools: []string{"write_file"}, MaxIter: 5},
			}},

		"investment-advisory": {Name: "investment-advisory", Desc: "统一投资顾问：旧版可交付主干 + 可失败的新能力增强支路",
			Steps: []PipelineStep{
				{Name: "自主判断与证据采集", Prompt: `你负责在一个阶段内完成投资任务的范围判断和必要证据采集，不输出最终投资报告，也不得写入用户指定的最终文件。

先自主识别任务属于个股/多标的比较、行业/板块、基金/债券或整体市场，并识别标的、市场、用户投资周期、风险偏好、数据时效和明确交付要求。整体市场、大盘、当前或某日行情本身就是完整的 market 类研究对象，不得仅因范围较宽而追问；未点名具体市场时，结合用户语言、时区、当前交易时段和可取得的数据选择主要本地市场作为明确假设，并补充关键跨市场联动。单独记录 position_state=provided|not_provided；用户没有明确给出投资周期、风险偏好、持仓、成本、账户规模或可交易数量时，标为 unknown/not_provided，先做中性的信息分析和条件场景，不得把这些可选个性化信息当作启动前提。只要能识别金融研究动作和任一研究对象就直接开始；只有对象和动作都无法识别，且获准的只读能力也无法解析时，才输出一句必要的缺失说明并停止调用工具。

证据策略按任务动态选择，不机械跑固定清单：
- 明确 A 股六位代码时，优先使用 stock_facts。对每个代码用同一个 as_of 日期，在一个并行批次中分别调用 provider=tencent_market、eastmoney_market、official_report；行情商用于交叉核对，官方披露用于最新报告和公告。不得先绕去 skill、Shell 或普通行情网页。
- 多标的比较对每个标的采用同口径、同截至日；周末或节假日必须明确“最新可获得交易日”，不得冒充当日盘中行情。
- 行业、市场、港美股、基金、债券，或结构化工具明确不支持/失败时，再自主使用 web_search/web_fetch/platform_search。优先官方披露、交易所、监管机构、公司公告和原始发布者；普通媒体和社区只作补充。
- 网页补充只围绕结构化数据未覆盖且用户真正要求的维度，例如行业逻辑、近期催化、标的特有风险或舆情。每类缺口最多尝试两个来源；达到足够覆盖后立即停止，不为凑数量重复搜索。金融文本中的数字必须同时记录指标名、数值、单位、币种、报告期和来源，缺一项时不能与行情或财报数字混用。

输出一份紧凑、可供下游复核的 EVIDENCE_LEDGER_V2，至少包含：任务类型和标准化标的；position_state；数据截至时间与市场状态；行情、估值、近一年价格表现、最新公开财报核心指标（适用时）；来源 URL；每条主张的 claim_id/source_id；来源冲突；未知及失败原因。附覆盖矩阵：基本面、估值、价格/成交/技术、资金面、公告/新闻/舆情、短线情报，每项标记 verified|partial|unknown|conflict。严格区分 verified_fact、transparent_inference、unverified、unknown、source_conflict 和 fetch_failed。不得承诺收益，不得给确定性买卖口令。用户指定的最终报告路径只作为下游交付目标记录，不能声称本阶段已经保存。`, Tools: []string{"stock_facts", "web_search", "web_fetch", "platform_search"}, MaxIter: 5, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 2400},

				{Name: "金融信息与短线情报", NoTools: true, Prompt: `你是投资顾问流程内的证据增强阶段，不是可单独调用的AI员工，也不得调用 TradingAgents、AI Hedge Fund、FinGPT 或任何 professional agent。先阅读上游 EVIDENCE_LEDGER_V2，只补它明确缺失的金融信息与短线证据；忽略 <!--HOST_FALLBACK_DECISION_BEGIN--> 与 <!--HOST_FALLBACK_DECISION_END--> 之间的宿主兜底文本，它不是证据。不得重复查询已有行情，不得为凑数量搜索。

只允许围绕这些缺口使用 web_search/web_fetch/platform_search：官方公告正文、报告期事件、近期公司或行业消息、舆情分歧。优先交易所/监管/公司原文，再用可靠媒体解释；社区内容一律 unverified。相同底层事件合并，不把转载数量当作置信度。抓取失败是 coverage_gap，不得解释成“没有消息”。

输出必须包含三个二级标题：
## 金融信息事件账本
逐条给出 claim_id、entity、event_type、event_at、published_at、claim、metric/value/unit/currency/report_period（适用时）、source_type、source_id、URL、verification_status、sentiment_drivers、uncertainty_drivers。情绪只能由可引用文本解释，不能只给一个无理由分数。

## 短线交易情报
仅使用已核验行情字段说明 as_of、市场状态、1/5/20/60日表现（有多少写多少）、均线/区间位置、量价、波动、资金面、事件窗口和关键观察位。没有成交量、资金或回测就明确 unknown；不得编造胜率、上涨概率、目标价或订单。当前价、条件入场位与失效位必须逻辑一致，若当前价已越过某条件就说明该场景已失效。position_state=not_provided 时不得写“减仓多少”“剩余仓位”或假定满仓。

## 覆盖缺口与冲突
按基本面、估值、技术/短线、新闻/情绪列 verified|partial|unknown|conflict，写明冲突来源和对后续判断的影响。不得给最终买卖结论，不写文件路径。`, Tools: []string{"web_search", "web_fetch", "platform_search"}, MaxIter: 5, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 2200,
					OutputContract: &PipelineOutputContract{MinChars: 500, RequiredSections: []string{"## 金融信息事件账本", "## 短线交易情报", "## 覆盖缺口与冲突"}, OnViolation: "degraded"},
					Parallel: []PipelineStep{
						{Name: "公告财报与金融信息核验", Prompt: `你是旧版投资顾问主流程中的可选证据增强支路。只阅读上游 EVIDENCE_LEDGER_V2，只补官方公告正文、最新公开财报与报告期事件；不得重复查询已有行情，不得给买卖结论，也不得调用或冒充其他 AI 员工。优先交易所、监管、公司原文，再用可靠媒体解释。抓取失败必须记为 fetch_failed/coverage_gap，不得解释成“没有消息”。

输出必须包含：
## 金融信息事件账本
逐条给出 claim_id、entity、event_type、event_at、published_at、claim、metric/value/unit/currency/report_period（适用时）、source_type、source_id、URL、verification_status。
## 覆盖缺口
列出 verified|partial|unknown|conflict，以及失败来源和影响。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 4, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 1400,
							OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 金融信息事件账本", "## 覆盖缺口"}, OnViolation: "degraded"}},
						{Name: "行业催化与舆情冲突", Prompt: `你是旧版投资顾问主流程中的可选事件与舆情增强支路。只补上游证据缺失的近期公司/行业催化、负面事件与舆情分歧。相同底层事件必须合并；社区内容一律标记 unverified，不把转载数量当置信度。不得给最终买卖结论。任何工具错误只记录 fetch_failed/coverage_gap。

输出必须包含：
## 行业与事件催化
记录 claim_id/source_id、事件发生日、发布日期、直接来源、正负驱动和核验状态。
## 舆情与来源冲突
分开写 verified_fact、transparent_inference、unverified、unknown 和 source_conflict。`, Tools: []string{"web_search", "web_fetch", "platform_search"}, MaxIter: 4, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 1400,
							OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 行业与事件催化", "## 舆情与来源冲突"}, OnViolation: "degraded"}},
						{Name: "短线与资金面证据", Prompt: `你是旧版投资顾问主流程中的可选短线证据增强支路。只使用上游已核验行情并补充真正缺失的量价、波动、资金面和事件窗口；不得重查已有字段。没有成交量、资金或回测时明确 unknown，不得编造胜率、上涨概率、目标价或订单。当前价、观察位和失效位必须逻辑一致。position_state=not_provided 时不得假定仓位或成本。工具失败只标记 fetch_failed。

输出必须包含：
## 短线交易情报
写明 as_of、市场状态、可用周期表现、趋势/动量/波动、量价/资金、事件窗口、观察位与失效条件。
## 资金面与数据缺口
逐项标记 verified|partial|unknown|conflict。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 4, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 1400,
							OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 短线交易情报", "## 资金面与数据缺口"}, OnViolation: "degraded"}},
						{Name: "标的风险证据", Prompt: `你是旧版投资顾问主流程中的可选风险证据增强支路。只补标的特有的监管、诉讼、质押、减持、解禁、担保、安全环保、供应链、行业政策和经营风险。每项必须绑定来源、日期、可观察触发器和影响窗口；不能用行业常识冒充公司事实。失败或无覆盖时写 fetch_failed/unknown，不得阻断后续。

输出必须包含：
## 标的特有风险
列风险事实、来源编号、核验状态、触发器和时间范围。
## 风险证据缺口
说明尚未核验的风险类别，不得声称不存在。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 4, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 1200,
							OutputContract: &PipelineOutputContract{MinChars: 240, RequiredSections: []string{"## 标的特有风险", "## 风险证据缺口"}, OnViolation: "degraded"}},
					}},

				{Name: "并行投研分析", NoTools: true, Parallel: []PipelineStep{
					{Name: "AI Hedge多因子与投研经理分析", Prompt: `把 AI Hedge 的多因子交叉审查和投研经理的组合视角作为方法，分析上游已经采集的证据；这是投资顾问内部的可选只读支路，不调用 AI Hedge Fund、TradingAgents、professional agent 或任何工具。分开评估基本面、估值、质量、成长、动量与风险因子；因子缺数据就标 unknown，不得用默认分数填充。用户未提供账户与持仓时，只给风险等级和需补变量。

输出必须包含：
## 基本面与估值
区分 verified_fact 与 transparent_inference，并说明跨期/跨源可比性。
## 多因子分歧与风险预算
列因子支持、反证、冲突、风险预算边界和会改变判断的新证据。`, NoTools: true, MaxIter: 1, ContinueOnError: true, TimeoutSeconds: 90, OutputTokenLimit: 1400,
						OutputContract: &PipelineOutputContract{MinChars: 300, RequiredSections: []string{"## 基本面与估值", "## 多因子分歧与风险预算"}, OnViolation: "degraded"}},
					{Name: "金信事件与情绪分析", Prompt: `把金信分析师的金融文本、事件时间线和情绪解释方法融入投资顾问，只分析上游已有证据，不调用 FinGPT、professional agent 或工具。区分事件发生日与发布日期；情绪结论必须引用文本驱动，社区信号不得提升为事实。缺失和冲突必须保留。

输出必须包含：
## 金融信息与情绪
按 claim_id/source_id 汇总已核验事件、正负驱动、媒体解释与未核验传闻。
## 证据冲突
列 source_conflict、unknown、影响和需要补充的直接证据。`, NoTools: true, MaxIter: 1, ContinueOnError: true, TimeoutSeconds: 90, OutputTokenLimit: 1200,
						OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 金融信息与情绪", "## 证据冲突"}, OnViolation: "degraded"}},
					{Name: "短线交易情报分析", Prompt: `按旧版投资顾问的短线框架分析上游已核验数据，不调用工具。用条件场景审查趋势、动量、波动、量价/资金、事件窗口、观察位和失效条件。没有回测与样本定义不得给概率；观察位相互矛盾时标 no_decision，不得给确定性买卖指令。

输出必须包含：
## 短线交易情报
只写有证据支持的信号与反信号。
## 观察位与失效条件
写明 as_of、条件、失效逻辑和缺失数据。`, NoTools: true, MaxIter: 1, ContinueOnError: true, TimeoutSeconds: 90, OutputTokenLimit: 1200,
						OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 短线交易情报", "## 观察位与失效条件"}, OnViolation: "degraded"}},
					{Name: "标的风险分析", Prompt: `只基于上游证据形成标的特有风险审查，不调用工具。至少从经营、财务、治理/监管、行业与交易结构中筛出有证据的风险；不能用通用免责声明代替。用户未给持仓时不得计算或假设仓位。

输出必须包含：
## 标的特有风险
逐项给出证据、严重度、时间范围与缓释因素。
## 监测触发器
列可观察触发器、失效条件和 unknown。`, NoTools: true, MaxIter: 1, ContinueOnError: true, TimeoutSeconds: 90, OutputTokenLimit: 1200,
						OutputContract: &PipelineOutputContract{MinChars: 260, RequiredSections: []string{"## 标的特有风险", "## 监测触发器"}, OnViolation: "degraded"}},
				}},

				{Name: "投研合成与质量门", Prompt: `你是旧版投资顾问主干中的投研合成和质量门阶段。只使用已内联的证据账本、可选金融信息/短线增强和可选并行投研分析；任何增强支路的 degraded、fetch_failed、unknown 或缺失都只能降低覆盖度，不能阻断本阶段形成 no_decision 或降级报告。忽略 <!--HOST_FALLBACK_DECISION_BEGIN--> 与 <!--HOST_FALLBACK_DECISION_END--> 之间的宿主兜底文本；不调用工具，不调用或冒充其他AI员工，不复述全部原始数据。输出专业、紧凑的 Markdown。

必须逐项审查四路信号：基本面、估值、技术/短线、新闻/情绪。每一路标记 verified|partial|unknown|conflict，并给出来源编号、截至时间和关键缺口。不能把 default/fallback、无有效交易、quantity=0、confidence=0 或模块缺失包装成有效结论；关键模块缺失时交付状态必须是 degraded 或 no_decision。

必须包含以下二级标题：
## 证据覆盖与质量门
本节第一行必须独立且精确写成“交付状态: completed”“交付状态: degraded”或“交付状态: no_decision”三者之一；行首不要项目符号、粗体或表格符号，全文只出现一次该字段。随后给出数据截至时间、position_state、四路覆盖表和未解决冲突。completed 只允许用于关键主张均可追溯且没有影响结论的关键缺口。

## 基本面与估值
区分财报事实、估值标签、跨期/跨源可比性和 transparent_inference；不能仅凭PE/PB高低判定贵贱，行业/市场任务不适用时明确 N/A 并解释。

## 金融信息与情绪
汇总已核验事件、正负驱动、发布日期与事件发生日、媒体解释和未核验传闻；每个结论引用 claim_id/source_id。

## 短线交易情报
用条件场景说明趋势、动量、波动、量价/资金（仅有数据时）、事件窗口、观察位和失效条件。不得给确定性买卖指令；没有回测和样本定义不得给概率。当前价、观察位和失效位相互矛盾时必须 no_decision。

## 风险预算与失效条件
用户给出账户、持仓和风险约束时才可计算风险预算；否则只给风险等级、条件框架和需要补充的变量，不得假定仓位或成本。列至少三项标的特有风险、可观察触发器、时间范围和缓释因素。

## 综合结论
给出面向用户投资周期的条件式结论、最强支持证据、最强反证、哪些新证据会改变判断。多标的可排序；单标的不得为了模板强行排序。结论不承诺收益、不执行或声称执行真实下单、不输出文件路径。`, NoTools: true, MaxIter: 1, ContinueOnError: true, TimeoutSeconds: 120, OutputTokenLimit: 2600,
					Rework:         &StepRework{Target: "并行投研分析", MaxReworks: 1, OnExhausted: "degraded"},
					OutputContract: &PipelineOutputContract{MinChars: 900, RequiredSections: []string{"## 证据覆盖与质量门", "## 基本面与估值", "## 金融信息与情绪", "## 短线交易情报", "## 风险预算与失效条件", "## 综合结论"}, ReportedStatusField: "交付状态", OnViolation: "degraded"}},

				{Name: "报告交付", Prompt: `沿用旧版投资顾问的强制交付规则，将上游证据账本、可选金融信息与短线增强、可选并行投研分析、投研合成与质量门合并成一份最终 Markdown 报告，写入当前运行产物契约指定的精确路径。任何新增支路失败、超时或缺数据都不得阻断写报告；必须原样保留其 degraded/fetch_failed/unknown/no_decision、来源编号、数据截至时间、position_state、关键未知项和冲突，不得把降级阶段润色成完整成功。报告标题使用“投资顾问多维研究与短线情报报告”，明确研究用途、不构成确定性买卖指令、未执行真实交易。最终回答只给真实路径、整体状态、数据截至时间和关键限制。`, Tools: []string{"write_file"}, MaxIter: 2, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/investment_advisory_${execution_id}.md"}, MinBytes: 1200, TextProvenance: true, RejectPlaceholders: true, MaterializeOutput: true}},
			}},

		"ai-trader": {Name: "ai-trader", Desc: "AI交易员：通过隔离专业 Agent 调用 TradingAgents 形成交易研判",
			Steps: []PipelineStep{
				{
					Name:              "交易研判",
					Prompt:            "${input}",
					NoTools:           true,
					MaxIter:           1,
					TimeoutSeconds:    1200,
					ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "trading"},
				},
			}},

		// quant-trader and the three facade pipelines are retained only for
		// historical execution replay and diagnostics. Normal users enter through
		// investment-advisory; that pipeline never invokes these professional agents.
		"quant-trader": {Name: "quant-trader", Desc: "AI交易员兼容 Pipeline：通过隔离专业 Agent 调用 TradingAgents 形成交易研判",
			Steps: []PipelineStep{
				{
					Name:              "交易研判",
					Prompt:            "${input}",
					NoTools:           true,
					MaxIter:           1,
					TimeoutSeconds:    1200,
					ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "trading"},
				},
			}},

		"investment-research-manager": {Name: "investment-research-manager", Desc: "AI投研经理：通过隔离专业 Agent 调用 AI Hedge Fund 完成研究统筹与模拟组合决策",
			Steps: []PipelineStep{
				{
					Name:              "投研管理",
					Prompt:            "${input}",
					NoTools:           true,
					MaxIter:           1,
					TimeoutSeconds:    1200,
					ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "research-management"},
				},
			}},

		"financial-intelligence-analyst": {Name: "financial-intelligence-analyst", Desc: "AI金信分析师：通过隔离专业 Agent 调用 FinGPT 完成金融信息分析",
			Steps: []PipelineStep{
				{
					Name:              "金融信息分析",
					Prompt:            "${input}",
					NoTools:           true,
					MaxIter:           1,
					TimeoutSeconds:    1200,
					ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "financial-intelligence"},
				},
			}},

		"investment-advisory-legacy": {Name: "investment-advisory-legacy", Desc: "旧版投资顾问深度流水线（保留用于兼容性诊断，不默认加载）",
			Steps: []PipelineStep{
				{Name: "任务判断", Prompt: `阶段边界：本步骤只做任务判断、标的标准化和投资周期识别，不调用工具、不搜索、不输出投资建议。

读取用户原始问题，输出一个 JSON 对象，且只输出 JSON，不要 Markdown、代码围栏或解释：
{
  "task_type": "single_stock|sector|market|unclear",
  "targets": [{"name": "标的名称", "code": "规范代码或空", "market": "A股|港股|美股|基金|债券|未知"}],
  "scope": "用户关注的行业、板块、市场或问题",
  "investment_horizon": "intraday|1-5d|5-20d|medium_long|unknown",
  "short_term_required": true,
  "as_of_requirement": "realtime|latest_trading_day",
  "required_research": ["target_profile", "financials", "valuation", "fund_flow", "technical_short_term", "official_events", "news_sentiment", "risk_events"],
  "missing_user_info": [],
  "user_message": ""
}

判定规则：
- 明确股票代码、公司名、基金、债券或 ETF：task_type=single_stock。
- 行业、板块或主题机会：task_type=sector。
- 大盘、当天行情或整体短线市场：task_type=market。
- 没有可识别标的、市场或行业：task_type=unclear，并在 user_message 中生成一句必要的澄清问题。
- 用户未说明投资周期时 investment_horizon=unknown，但 short_term_required 仍为 true，默认补充短线态势。
- 不得写死财报年份；后续必须使用报告日可获得的最新年报、最新中报/季报及最近交易日数据。`, NoTools: true, MaxIter: 1},

				{Name: "任务分流", Prompt: `本步骤只负责早停分流，不执行个股或板块的深度研究。

读取上一步 JSON：
- task_type=unclear：不要调用工具，直接输出 user_message，并将 <!--PIPELINE_STOP--> 作为单独最后一行。
- task_type=market：获取当天或最近交易日的结构化市场快照，并从官方/权威市场数据、财经新闻和市场情绪至少三类来源交叉核验。输出主要指数、涨跌家数、成交额、领涨领跌板块、资金与短线情绪、当日重大消息、市场级风险、数据截至时间和来源；完成后将 <!--PIPELINE_STOP--> 作为单独最后一行。
- task_type=single_stock 或 sector：不要在本步骤搜索，输出 DEEP_RESEARCH_CONTINUE，交给下一步并行采集；不要输出停止标记。

市场简报不得依赖单次搜索摘要；达到覆盖要求后停止，不按固定一次调用限制。`, Tools: []string{"list_skills", "use_skill", "persistent_shell", "web_search", "web_fetch"}, MaxIter: 8,
					Routing: []StepRoute{{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}}},

				{Name: "并行采集", NoTools: true, Parallel: []PipelineStep{
					{Name: "结构化行情估值与短线", Prompt: `你是结构化金融数据采集员，只采集和计算，不给买卖建议。

根据任务判断中的标的和市场选择数据能力。A 股优先调用 list_skills/use_skill 获取 akshare 与 baostock 的使用说明，再用 persistent_shell 执行其脚本；优先尝试 akshare 的 stock_data.py --type research。港股、美股、基金或债券选择相应结构化数据源。web_fetch 只作结构化接口失败后的备用核验，不能用搜索摘要代替精确数值。

单股至少采集：
1. 数据截至时间、最近交易日、最新价、总市值/流通市值；
2. PE-TTM或动态PE、PB-MRQ、PS-TTM，以及可获得的近3/5年历史分位；亏损导致PE无意义时标记 not_applicable 并说明原因，不能写“缺失”；
3. 1/3/5/10/20日涨跌幅、相对指数/行业强弱、成交额、换手率、量比、近5/20日量能变化；
4. MA5/10/20/60、MACD、RSI、ATR、20/60日区间位置及可复算的支撑/压力观察位；
5. 当日及近3/5/10日资金流向、融资融券、龙虎榜或大宗交易（可获得时）；
6. 每个字段的 source、as_of、fetched_at 和状态。

板块任务采集代表指数/ETF、板块涨跌与成交、代表公司估值分布、强弱排名和短线热度。实时数据必须注明是否盘中、收盘或延迟。输出紧凑的结构化证据，不写方向性结论；失败字段使用 fetch_failed、not_found_after_fallback、not_applicable 或 source_conflict，并保留具体原因。`, Tools: []string{"list_skills", "use_skill", "persistent_shell", "read_file", "web_fetch"}, MaxIter: 10, ContinueOnError: true},

					{Name: "官方公告与财报", Prompt: `你是官方事实采集员。优先检索交易所、法定信息披露平台、公司官网、监管机构和政府/行业主管部门，再用可靠财经媒体作补充。

采集报告日可获得的最新年报、最新中报/季报、最近90日公告及重大事项，覆盖营收、利润、扣非、现金流、负债、ROE、分业务数据，以及业绩预告/快报、分红、回购、增减持、解禁、质押、问询、处罚、诉讼、担保、重大合同、并购重组、停复牌和安全环保事项。不得写死2024/2025等年份。

每条记录包含 event_at、published_at、title、fact、source_type、publisher、URL、confidence、fetched_at。官方查询成功但没有事件时写 observed_zero；访问失败写 fetch_failed，二者不得混淆。只输出事实和来源，不做投资判断。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 10, ContinueOnError: true},

					{Name: "行业新闻与催化", Prompt: `你是近期消息与产业态势采集员。按24小时、7天、30天和90天四个窗口收集与标的直接相关的行业新闻、政策、产品价格、供需库存、竞争格局、订单、技术路线和宏观变量。

优先原始发布者和高可信媒体。转载稿按同一底层事件聚类，只保留最早或最权威来源；区分事件发生时间与文章发布时间。输出事件、影响对象、可能影响期限、来源、URL、可信度以及“新增事实/媒体解读”标签。不要把市场评论写成事实，也不要为了数量重复搜索等价关键词。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 10, ContinueOnError: true},

					{Name: "社区舆情与热度", Prompt: `你是舆情信号采集员。使用 platform_search 及必要的网页检索，覆盖至少两类可访问的社区/社交来源；A股优先雪球、东方财富股吧、微博或同花顺社区，海外标的选择相应平台。

按24小时、7天和30天统计主要讨论主题、讨论量变化、正面/中性/负面倾向、观点分歧、重复叙事和突然升温的关键词。区分媒体情绪、机构观点和散户社区情绪。社区传闻一律标记 unverified，只有官方或独立可靠来源确认后才能升级为事实。输出平台覆盖、样本时间、热度变化、主题、情绪、分歧和代表性来源链接；抓取失败必须说明覆盖缺口，不能把“抓不到”写成“无人讨论”。`, Tools: []string{"platform_search", "web_search", "web_fetch"}, MaxIter: 10, ContinueOnError: true},

					{Name: "标的风险证据", Prompt: `你是投资风险事实采集员。风险点也是本次研究的正式素材，必须主动检索和列证据，不能只在报告末尾生成通用风险模板。

至少覆盖：
- 业绩恶化、现金流、债务与偿债、商誉/存货/应收、审计意见；
- 监管问询、处罚、诉讼仲裁、担保、关联交易、质押、减持、解禁和治理变动；
- 产品质量、安全生产、环保、供应链、客户集中、产能扩张与价格周期；
- 行业政策、竞争、关税/汇率/地缘风险；
- 短线流动性、异常波动、高换手、集中催化落空和事件日历风险。

优先官方来源。每个风险条目包含 risk_type、verified_fact、trigger、time_horizon、severity_evidence、source、URL、published_at、confidence。没有证据时标记待核验，不得编造概率或损失金额；同时记录可能缓释因素。`, Tools: []string{"web_search", "web_fetch"}, MaxIter: 10, ContinueOnError: true},
				}},

				{Name: "证据合并与质量门禁", Prompt: `读取上游文件清单中“并行采集”的完整结果文件，不要只依赖可能被截断的内联摘要。将五条采集支线合并成一个证据账本，严格去重，不调用外部数据源、不引入新事实。

合并要求：
1. 按URL、规范化标题和底层事件去重；区分已验证事实、媒体解读、社区信号、推断、传闻和来源冲突。
2. 校验报告日期、行情 as_of、最近交易日和最新财报期；过期数据必须标记 stale，禁止用旧季度冒充当前态势。
3. 单股P0门禁：标的代码/市场、行情时间与价格、估值或明确的not_applicable原因、最新财报期、最近公告查询结果、1/5/20日量价、短线资金/技术覆盖状态、近期消息/舆情覆盖状态、至少一组标的特有风险证据。
4. 板块P0门禁：代表指数/ETF、近期表现、估值区间、资金/热度、政策/产业催化和特有风险。
5. 状态只允许 observed、observed_zero、not_applicable、stale、source_conflict、fetch_failed、not_found_after_fallback；禁止用裸“缺失”掩盖原因。
6. 若PE/PB或历史分位没有数值，禁止输出“低估”“历史下沿”等估值判断。

输出：目标与时间窗、事实事件时间线、结构化行情/估值/短线表、舆情主题、风险证据表、来源冲突、P0覆盖表、需要定向补数的字段。最后单独输出一行 QUALITY_STATUS: PASS 或 QUALITY_STATUS: PARTIAL。`, Tools: []string{"read_file"}, MaxIter: 4},

				{Name: "定向补数", Prompt: `读取质量门禁结果。若 QUALITY_STATUS: PASS，不调用工具，输出“无需补数”。若为 PARTIAL，只处理P0覆盖表中尚未满足的字段，不重复已完成搜索。

补数顺序：结构化金融数据源或官方披露 → 第二独立来源 → 可靠财经媒体。A股估值优先 akshare 当前快照与 baostock 历史 peTTM/pbMRQ/psTTM；短线数据优先结构化行情、资金流和龙虎榜接口；公告与风险优先交易所/法定披露。每个字段最多尝试两个不同来源，记录尝试结果和错误。

输出新增证据、来源、as_of/fetched_at，以及仍未解决字段的准确状态与原因。没有拿到数值时不得猜测、插值或用旧数据冒充最新数据。`, Tools: []string{"list_skills", "use_skill", "persistent_shell", "platform_search", "web_search", "web_fetch", "read_file"}, MaxIter: 10, ContinueOnError: true},

				{Name: "并行分析", NoTools: true, Parallel: []PipelineStep{
					{Name: "基本面与估值分析", Prompt: `仅基于已合并和补充的证据，分析公司/板块基本面、最新财务趋势、盈利质量和估值水位。估值必须同时给出截至时间、指标值、历史分位或同业坐标及适用性；周期股或亏损企业的PE失真时，切换PB、PS、EV/EBITDA或正常化利润框架。证据不足时说明限制，禁止输出无数值支撑的“低估/高估/历史下沿”。区分事实与推断，不给确定性买卖指令。`, NoTools: true, MaxIter: 4},
					{Name: "短线态势分析", Prompt: `仅基于结构化行情和短线证据，按用户投资周期分析1/3/5/10/20日强弱、量价、换手、资金、技术形态、板块联动、事件催化和流动性风险。输出短线温度、当前驱动、市场是否确认、未来1-5个交易日观察条件和失效条件。技术位必须来自可复算数据，不得凭常识编造；只给条件性观察，不给确定性买卖口令。`, NoTools: true, MaxIter: 4},
					{Name: "消息舆情与催化分析", Prompt: `仅基于事件账本分析最近24小时、7天、30天和90天发生了什么、哪些主题升温或降温、媒体/机构/社区情绪是否一致、市场是否已经反映，以及未来事件日历。官方事实、媒体解释和社区传闻必须分层；来源冲突并列展示。不得用帖子数量直接证明事实。`, NoTools: true, MaxIter: 4},
					{Name: "风险分析", Prompt: `仅基于风险采集素材和财务/市场证据形成标的特有风险矩阵。按高/中/低及短期/中期/长期分类，写清已验证事实、触发条件、可能传导路径、监测指标、来源和缓释因素。优先突出近期公告风险、财务风险、行业价格/政策风险及短线流动性风险；不要输出适用于所有股票的空泛模板。`, NoTools: true, MaxIter: 4},
				}},

				{Name: "输出报告", Prompt: `先使用 read_file 读取上游文件清单中“并行分析”的完整结果文件，再基于质量门禁、定向补数和全部分析结果生成最终 Markdown 报告。必须使用 write_file 保存到 .openkitty/storage/，文件名格式 investment-report-{标的或板块}-{日期}.md，并在最终回答中给出真实路径。

报告结构：
1. 数据截至时间、覆盖状态与一句话态势；
2. 短线态势面板（1/3/5/10/20日、量价、资金、技术、板块联动、1-5日观察条件）；
3. 最近重大消息时间线（24小时/7天/30天/90天）；
4. 舆情与市场关注点（媒体、机构、社区分层及升降温）；
5. 基本面与最新财务；
6. 当前估值水位（数值、分位、同业、适用性）；
7. 标的特有风险矩阵（事实、触发器、期限、监测指标、来源、缓释因素）；
8. 催化与风险日历；
9. 综合观点与条件性观察；
10. 数据源、冲突与覆盖限制。

强制规则：
- 正文不得出现无解释的“【缺失】”。查询成功但无事件写“未观察到/observed_zero”；指标不适用写原因；抓取失败写来源、错误和尝试时间。
- 行情必须注明盘中/收盘/延迟和最近交易日；财报必须是报告日可获得的最新披露期，禁止写死年份。
- 没有估值数值或适用替代指标时，禁止声称低估、高估或历史下沿。
- 社区传闻不得写成事实；转载消息必须合并。
- 不承诺收益，不给确定性买卖指令；所有结论均为研究参考和观察条件。
- 写入后用 read_file 验证文件存在且包含短线态势、消息舆情、估值、风险矩阵和数据来源。`, Tools: []string{"read_file", "write_file"}, MaxIter: 5},
			}},
		"contract-review": {Name: "contract-review", Desc: "合同审查：条款提取→风险分析→合规检查→审查报告",
			Steps: []PipelineStep{
				{Name: "条款提取", Prompt: "使用 read_file 读取合同文件，提取关键条款：付款条件、违约责任、知识产权、保密条款、终止条件等。", Tools: []string{"read_file"}, MaxIter: 5},
				{Name: "风险分析", Prompt: "逐条分析风险：是否公平、是否有隐藏陷阱、是否符合行业惯例。标注风险等级（高/中/低）。", Tools: []string{"rag_search", "web_fetch"}, MaxIter: 8},
				{Name: "合规检查", Prompt: "检查合同是否符合相关法律法规要求。如有不合规之处，给出修改建议。", Tools: []string{"web_fetch"}, MaxIter: 5},
				{Name: "审查报告", Prompt: "生成合同审查报告：摘要、风险矩阵、修改建议、谈判要点。保存到 storage/ 目录。", Tools: []string{"write_file"}, MaxIter: 3, RequireApproval: true},
			}},
		"competitor-analysis": {Name: "competitor-analysis", Desc: "竞对分析：格局扫描→竞对深度分析→战略定位→分析报告",
			Steps: []PipelineStep{
				{Name: "格局扫描", Prompt: "使用 web_fetch 扫描目标市场的竞争格局：主要玩家、市场份额、商业模式、核心优势。收集 5+ 来源。", Tools: []string{"web_fetch"}, MaxIter: 10},
				{Name: "竞对分析", Prompt: "深度分析每个主要竞争对手：产品/服务、定价、渠道、技术、团队、融资。使用 persistent_shell 生成对比表格。", Tools: []string{"web_fetch", "persistent_shell", "read_file"}, MaxIter: 12},
				{Name: "战略定位", Prompt: "基于分析结果，确定自身的差异化定位和竞争策略。给出 SWOT 分析和战略建议。", Tools: []string{"read_file", "write_file"}, MaxIter: 8},
				{Name: "分析报告", Prompt: "生成完整竞对分析报告保存到 storage/ 目录。包含：市场概况、竞对画像、SWOT、战略建议。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"weekly-report": {Name: "weekly-report", Desc: "项目周报：信息收集→数据分析→周报生成",
			Steps: []PipelineStep{
				{Name: "信息收集", Prompt: "收集本周工作信息：git 提交记录、存储文件变更、TODO 执行记录。使用 persistent_shell 统计代码变更量。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "数据分析", Prompt: "分析本周工作数据：代码量、文档数、任务完成率。生成趋势图表。", Tools: []string{"persistent_shell", "read_file"}, MaxIter: 8},
				{Name: "周报生成", Prompt: "生成专业周报 PPT，保存到 storage/ 目录。包含：本周概览、重点工作、数据图表、下周计划。先调用 use_skill ppt-art-director；标准可编辑周报使用 base-ppt，需要定制图解时使用任务专用渲染器。", Tools: []string{"write_file", "persistent_shell", "use_skill"}, MaxIter: 8},
			}},
		"meeting-minutes": {Name: "meeting-minutes", Desc: "会议纪要：录音转写→内容提取→纪要生成",
			Steps: []PipelineStep{
				{Name: "内容提取", Prompt: "从会议录音或记录中提取关键信息：参会人、议题、决议、待办事项、时间节点。使用 OCR 或语音转写工具。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "纪要生成", Prompt: "整理为结构化会议纪要：基本信息→议题讨论→决议事项→待办清单→下次会议时间。保存到 storage/ 目录。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"wechat-article": {Name: "wechat-article", Desc: "公众号文章：素材准备→公众号交付物",
			Steps: []PipelineStep{
				{Name: "素材准备", Prompt: `这是通用微信公众号编辑流程的第一阶段：只收集、读取和整理素材，不写公众号正文，不生成 Markdown/HTML 交付物。

先自主判断用户处于哪一种情况：已经给出可读取的素材、描述了一个需要你自行定位的现有素材，或只给了原创主题。根据素材形态选择当前可用的、最可信且只读的获取能力；用户没有给定位信息时，把用户对素材的描述原样交给通用来源获取工具，不得擅自追加实体、竞品、行业、原因或同义词。只给原创主题时做必要调研，不要求用户补文件，也不捏造“原帖”。

引用现有素材时必须按系统 Source Contract 保存可追溯的执行级来源；用户说的是单份素材就只冻结最终选中的 1 份，不得用同主题内容替代。一旦真实素材获取成功，立即停止其他检索：不重复获取，不在本阶段做事实核验，也不保存 brief/temp/reference 等中间文件；读取冻结源一次后，直接用最终回答输出紧凑素材简报。简报包含：素材定位、目标读者、发布目的、素材明确陈述、素材观点、事实与观点边界、版权风险、最多 2 组待核验关键断言、3-5 个标题角度、摘要方向、正文大纲，以及“可用图片清单”。图片清单只记录用户输入或可信工具回执中已经存在的工作区真实路径，并注明来源、建议用途和授权边界；没有真实图片路径就明确写“无”，不得用提示词、配图建议或素材库占位冒充图片。

输入门禁：如果用户既没有给出可读取素材、来源描述、URL 或本地路径，也没有给出可用于原创写作的明确主题，就不要搜索工作目录、不要猜测来源、不要调用工具。直接用一两句话询问“文章主题/核心观点/目标读者”或“素材/链接/文件”中的必要信息；然后另起一行精确输出 INPUT_STATUS: MISSING，最后一行精确输出 <!--PIPELINE_STOP-->。真实来源获取失败且必须由用户补充定位信息时，也使用同一待补格式正常停止。`, Tools: []string{"source_acquire", "web_search", "web_fetch", "read_file", "write_file", "extract"}, MaxIter: 6, AllowMissingInput: true, SourceContract: &PipelineSourceContract{Required: true, WhenReferenced: true, FrozenPath: ".openkitty/storage/.system/pipeline_sources/${execution_id}/source.json", MinBytes: 300, RequireDownstreamRead: true}, Routing: []StepRoute{{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}}},
				{Name: "公众号交付物", Prompt: `这是通用微信公众号编辑流程的第二阶段：基于上一步素材简报和系统注入的可信素材，完成全部可发布交付物，不再重新选题、重新取材或另找替代素材。

系统已在模型调用前校验并注入冻结源（若本次有冻结源），不要为了满足流程再次 read_file，不做普通网页搜索，也不反复事实核验。把来源观点与事实分开：无法从现有素材可靠确认的金额、日期、财报、人事或并购数字，直接删除或明确写成“据原素材所述/仍需发布前核验”，不得声称已二次核验。

一次完成以下内容：写一篇适合微信阅读、约 1000-1500 汉字的完整文章，包含观点先行的标题、短段落开头钩子、核心分析、素材中的案例或数据、读者可行动建议、结尾总结与自然关注引导；并在文末附 3 个备选标题、40-120 字摘要、封面图提示词、文中配图建议、版权与事实提醒。写工具参数前自行检查素材覆盖、夸大表达、占位符和事实口径。配图建议只是编辑建议，不能冒充已交付图片；若可信上游内容明确给出了工作区内真实图片路径，可用 Markdown 图片语法插入，不能猜路径或写“素材库占位”。

然后只调用一次 wechat_article_bundle：把完整可发布 Markdown 放入 markdown；md_path 必须是 .openkitty/storage/wechat_article_${execution_id}.md；html_path 必须是 .openkitty/storage/wechat_article_${execution_id}.html；execution_id 使用系统给出的当前值；存在冻结源时 source_sha256 使用系统给出的锁定 SHA-256，原创主题则省略 source_sha256。工具默认生成真实封面 PNG，并把本地 Markdown 图片校验后嵌入自包含 HTML；除非 Markdown 已有确定的主图，否则不要关闭 auto_cover。工具回执的 image_count 必须至少为 1；缺失或非法本地图片必须修正，不能把无图 HTML 说成图文交付。不要自行写 HTML，不要在 markdown 中写 OPENKITTY 注释，不创建 temp_source、reference、brief、draft 等中间副本。

工具成功后立即结束。最终回复必须先完整显示刚刚写入 markdown 的公众号正文，不能只给标题、摘要、素材简报或文件路径；正文之后再列出 Markdown 路径、HTML 路径、两份文件的哈希回执和发布前注意事项。不要省略正文，不再读取文件、不再调用其他工具、不等待人工审批。`, Tools: []string{"wechat_article_bundle"}, MaxIter: 4, TimeoutSeconds: 75, OutputTokenLimit: 3200, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/wechat_article_${execution_id}.md", ".openkitty/storage/wechat_article_${execution_id}.html"}, MinBytes: 500, RequireRead: false, TextProvenance: true, RejectPlaceholders: true}},
			}},
		"ppt-polish": {Name: "ppt-polish", Desc: "自适应PPT：输入识别→内容规划→视觉制作→PPTX验收",
			Steps: []PipelineStep{
				{Name: "素材读取", Prompt: `阶段边界：本步骤只识别输入成熟度、读取可信素材并形成制作简报，不生成 PPTX。

先读取上游的 PPT_INPUT_MODE；若缺失则自主判断：
- existing_deck：用户提供已有 PPT/PPTX。读取逐页文字、表格和结构，诊断叙事、信息密度、版式、图表和视觉一致性；保留事实与必要数据。
- source_material：用户只提供 Word/PDF/Markdown/图片/URL/介绍材料。读取材料并提炼主题、事实、核心观点、可用数据、图片和引用；这是从零制作，不得要求补原始 PPT。
- detailed_brief：用户已经详细写明内容、受众、用途、观点、页数或风格。以明确要求为准，不重复询问；普通缺项采用合理默认值。
- concept_only：用户只有明确主题、产品、项目或简短描述。直接形成精简制作简报，根据主题复杂度和信息密度自适应页数，默认受众为需要快速理解该主题的普通业务读者、用途为内部介绍、风格为清晰专业；这些是可覆盖假设，不得冒充用户明示。没有证据的产品数字、客户案例、市场份额和 ROI 不得编造。

PPTX/PDF/DOCX 优先 extract，纯文本/Markdown 用 read_file；URL 或需要时效性事实的主题使用 web_search/web_fetch。凡是结论会随时间变化，或用户要求当前进展，都必须用可追溯的公开资料核验，优先一手与权威来源，记录发布日期和 URL；来源数量以足够支撑关键结论为准，不为了凑数量重复搜索。达到足够内容后停止。输出必须包含 PPT_INPUT_MODE、主题、可信素材与来源、用户明确要求、合理默认假设、事实/推断/未知边界、建议叙事和页数范围。`, Tools: []string{"extract", "read_file", "web_search", "web_fetch"}, MaxIter: 8},
				{Name: "内容重构", Prompt: `阶段边界：本步骤只规划演示内容并生成 Markdown 制作简报，不生成 PPTX。根据 PPT_INPUT_MODE 自适应处理：

- existing_deck：以原稿逐页内容为事实底座，允许重排、合并或拆页；不丢失关键数据和引用，也不继承已被否定的旧视觉方案。
- source_material：从材料中建立“问题/背景→核心观点→证据→方案或结论”的叙事，材料未支持的内容标为未知，不补造案例和数字。
- detailed_brief：严格落实用户明确给出的受众、用途、观点、页数、风格和输出要求。
- concept_only：使用中性默认值建立精简结构，根据主题复杂度、可用证据与单页信息密度自适应页数，不为了显得完整而扩张。优先讲清楚“是什么、关键进展、证据、影响、局限、下一步”，只使用用户描述和已核验公开信息。

保存到 .openkitty/storage/ppt_polish_brief_${execution_id}.md。简报包含：输入模式、受众与用途、核心观点、叙事主线、逐页标题、每页唯一核心观点、支持内容、图表/配图建议、演讲备注、事实来源和待确认项。逐页部分统一使用“### Slide 01 — 页面标题”三级标题；每页紧随一个两列表格，至少包含“唯一核心观点”和“支持内容”两行。完成后输出真实 Markdown 路径和规划页数。`, Tools: []string{"write_file"}, MaxIter: 5},
				{Name: "视觉润色", Prompt: `阶段边界：本步骤必须生成真实 .pptx 文件，不要停留在建议。先调用 use_skill ppt-art-director 并严格执行三级生产：Level 3 高端定制=完整上游 PPT Master；Level 2 常规=从内置脚手架创建任务专用、原生可编辑的 python-pptx/PptxGenJS 构建；Level 1 灾备=base-ppt。base-ppt 直接选路仅用于用户明确要求快速、内部、周期性、固定结构或兼容占位符；正式精美稿必须先完成所选精美路线的真实构建和一次针对性修正，仍失败再尝试另一条适用精美路线，只有两条路线均客观不可用或修正后仍失败时才允许公开降级。禁止空 generator 直接 convert 成 PPTX，禁止用一次性内联大脚本代替 Level 2。

				解释器和渲染命令必须适配当前操作系统：优先直接采用技能针对当前平台给出的命令；只有技能未能确定时，才在一次工具调用中探测可用解释器，不要按多个命令逐个失败试错。只调用一次 use_skill；读取返回的 SKILL.md 后，直接使用技能给出的脚手架命令，不要额外列出或读取 scripts/assets 中已经由技能说明过的文件。逐页定制是所有正式对外、研究、行业、技术、策略、投资人、客户、论坛或管理层稿件的默认方法，与页数无关：先走脚手架，随后完整读取一次本次任务 build_deck.py；用 deck_spec.json 冻结事实、逐页叙事任务、结论、证据形式、构图家族与视觉焦点，设置 needs_customization=false；再用有锚点的 edit_file/multiedit 在 TASK PAGE COMPOSITIONS 区域为每一页登记独立 renderer。cover、statement、cards、split、process、metrics、chart、table、matrix、timeline、closing 只是可复用的底层机制，不得作为整套稿件的最终页型引擎。不要读取或改写 pptx_design.py，也不要用 write_file 覆盖整份 builder；每次 builder 编辑不超过约 6000 字符。8 页稿至少使用 4 种正文构图家族；允许两页连续同类，但禁止三页连续同类；内容完整性高于外观多样性。独立 build_task_pptx.py 紧凑路线只用于用户明确要求简单/快速/内部输出，或真正周期性、固定结构的稿件；页数短本身不是使用理由。Level 2 的任务专用 builder 必须放在当前运行契约指定的 ppt_work_${execution_id}/build_deck.py；PPTX 必须写入当前运行契约给出的唯一精确路径，用户指定文件名时不得另写默认文件名。

existing_deck 是真正的“润色/重构”：保留原稿事实并生成新文件，禁止原地覆盖。source_material、detailed_brief 和 concept_only 是从零制作：不得声称继承不存在的旧稿。用户说“太丑/难看/重做/优化”即明确否定旧视觉方案，必须重新定义视觉命题。若同会话给出一份过去更好看的 PPT，必须把它作为 style-reference，实际读取其页面结构与渲染结果后再制作。投资人、客户、商业计划书和对外汇报在没有更强品牌约束时，默认采用咨询式视觉基线；普通内部介绍采用清晰、克制、易读的设计，不为了“科技感”机械使用黑底霓虹、圆角卡片和装饰点阵。不得使用无来源的“竞品A/B”、模拟指标或 ROI。

事实与数字纪律：用户或可信素材没有提供的基线、目标、比例、金额、时长和效果数字，必须在页面中保留为“未提供”“待确认”或“未统计”，不得为了让表格、图表或视觉检查显得完整而推断、补齐或编造。“未提供/待确认/未统计”在确实缺少来源时是合规披露，不是需要被视觉修正消除的占位符。用户明确给出的结论不得只留在 JSON 元数据中：普通页面用 takeaway 显示；cards 可将 takeaway 渲染为高对比结论栏；process 可用 lateral 表达最多四项横向能力。

用户已要求自动制作或润色，不再重复询问可合理推断的受众、用途、页数和设计默认值。页数根据主题复杂度、证据和可读性自适应，不为了显得完整而堆页；也不得为了满足版式种类、视觉变化或模板限制，删除用户明确要求的事实、指标、表格行和验收条件。保留旧 PPTX，不使用公共 storage/charts、rendered_pages 或共享 preview，不生成模拟事实图表。正文至少 16pt；标题、正文、表格和图示必须在普通放映比例下可读。构建器、audit_pptx.py 和渲染器必须作为保留真实退出码的直接命令运行，不得通过 | tail、Select-Object 等输出过滤命令掩盖失败。构建失败时修复并重跑；只有命令真实成功且精确输出文件存在后，才能声称 PPTX 已生成。

内部迭代预算、工具调用失败、可选视觉检查不可用或公开资料未完全取得，都不是用户输入缺失；禁止因此输出 INPUT_STATUS: MISSING、SLOT_STATUS: MISSING 或要求用户重新发起。精美路线的命令猜测、目录探测或依赖检查失败不计入降级条件；所选路线一次真实构建加一次针对性修正仍未成功时，先切换另一条适用精美路线。只有两条精美路线均客观不可用或修正后仍失败时，才走获准的 Level 1 灾备，并公开具体降级原因。

				返工修正必须使用 edit_file/write_file；禁止用 shell 或内联 Python 改写 deck_spec、builder 或 preview。builder 必须作为保留真实退出码的直接命令运行，禁止用 ; echo、管道、尾随 ls 等方式把失败包装成成功。结构审计只运行内置 audit_pptx.py；最终渲染只运行内置 render_pptx.py/render_pptx.ps1，禁止直接调用 LibreOffice、soffice、pdftoppm、PIL 或自制联系表。用户要求管理层“需确认”的优先级、验收口径、试点团队、预算、负责人或目标必须在观众页面保留为“待确认/管理层确认后确定”，不得擅自指定季度、团队数量或目标值。

				完成一次有界的审计→渲染→真实图片检查→针对性修正→重新审计与复查。渲染页和联系表只能写到当前运行契约指定的 ppt_work_${execution_id}/preview，不能读取或复用其他任务的预览；在租户根目录或 storage 根目录创建 preview 都是错误。本步骤不存在负责预览的下游步骤，不得把渲染或视觉检查推给交付自查。图片检查必须调用 vision(path="相对工作区图片路径", question="检查裁切、重叠、溢出、小字、失衡、空洞内容、乱码、占位符、内部配置文本和层级一致性")，禁止误用 watch(file=...)，不得用当前桌面截图、文件尺寸、像素统计或结构审计冒充视觉检查。8 页及以上正式稿先检查 contact_sheet.jpg，再逐页检查每个 slide_NN.png，形成逐页缺陷清单；contact_sheet 只用于总览导航，不能代替任何一页的独立 vision 调用，也不能单独作为改稿依据，联系表中发现的问题必须在对应完整 slide_NN 上复核。交付自查会核对成功记录，缺少 contact_sheet 或任一逐页记录都会判定失败。修正后重新审计并复查所有改动页。确定最终 PPTX 后立即冻结 deck_spec、builder 和 PPTX 字节；最终渲染之后不得再编辑或重建。若发生任何修改，必须重新审计、把精确最终 PPTX 重新渲染到本次 preview，并在新图片上重新完成联系表和逐页 vision。若用户明确给出正文或标题字号下限，运行 audit_pptx.py 时分别传入 --min-body-font-size 和 --min-title-font-size；审计失败必须先修正，不得把 fatal error 或用户字号下限解释为建议。最终准确说明实际路线、可编辑程度、逐页检查覆盖数和仍未解决的问题。生成后校验 PPTX 真实存在、文件大于 10KB、至少 4 页。`, Tools: []string{"convert", "persistent_shell", "read_file", "write_file", "edit_file", "list_skills", "use_skill", "vision"}, MaxIter: 150, TimeoutSeconds: 1800, OutputTokenLimit: 8000},
				{Name: "交付自查", Prompt: `阶段边界：本步骤只做最终交付检查和说明。读取或校验上一步 PPTX 路径，输出：PPTX真实路径、页数、源素材路径、润色变化摘要、仍需人工确认的内容。不允许说“已生成”但不给路径；不允许伪造文件路径。统一署名“AI员工：PPT润色专员”。`, Tools: []string{"read_file", "persistent_shell"}, MaxIter: 3, Rework: &StepRework{Target: "视觉润色", MaxReworks: 2, OnExhausted: "fail", AllowSideEffects: true}},
			}},
		"bid-proposal": {Name: "bid-proposal", Desc: "投竞标：招标解读→响应矩阵→标书初稿→风险自查",
			Steps: []PipelineStep{
				{Name: "招标解读", Prompt: `阶段边界：本步骤只读取和解读招标/竞标/采购需求材料，不写标书正文。用户给本地文件时优先 extract 或 read_file；用户给网页或要求联网时使用 web_fetch。输出必须包含：项目名称、采购方、投标截止时间（如有）、资格条件、评分办法、关键技术/商务要求、必须提交的材料、废标风险点、缺失信息。没有证据的字段标注“未提供/待确认”。

输入门禁：必须有可读取的招标/采购材料，包括本轮或同会话续接任务中明确给出的本地文件路径、URL、附件路径或足够完整的正文。只有“这份招标文件”但没有真实定位信息不算材料。材料缺失时禁止猜测常见文件名、禁止扫描或搜索工作目录、禁止用 file:// 调用网页工具、禁止生成通用模板。直接说明需要用户补充文件/链接/正文；然后另起一行精确输出 INPUT_STATUS: MISSING，最后一行精确输出 <!--PIPELINE_STOP-->。`, Tools: []string{"extract", "read_file", "web_fetch"}, MaxIter: 8, AllowMissingInput: true, Routing: []StepRoute{{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}}},
				{Name: "响应矩阵", Prompt: `阶段边界：本步骤只生成响应矩阵和工作拆解，不写完整标书。基于招标解读结果，生成逐条响应矩阵并保存到 .openkitty/storage/bid_response_matrix_${execution_id}.md。矩阵字段包括：招标要求、响应策略、我方材料/证据、负责人或待补充、优先级、风险等级。完成后输出真实文件路径和 Top 风险。`, Tools: []string{"write_file"}, MaxIter: 4, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/bid_response_matrix_${execution_id}.md"}, MinBytes: 500}},
				{Name: "标书初稿", Prompt: `阶段边界：本步骤生成可编辑的投标/竞标方案初稿。基于招标解读和响应矩阵，写出 Markdown 标书草案并保存到 .openkitty/storage/bid_proposal_draft_${execution_id}.md；内容至少包含：项目理解、总体方案、技术路线、实施计划、团队与资质、服务保障、商务响应、差异化优势、交付物清单。如果用户明确要求 PPT/路演材料，先调用 use_skill ppt-art-director：高端商业投标路演在条件合适时优先完整上游 PPT Master；其他正式方案无论页数都必须先用内置脚手架创建本次任务独有的 builder，把 deck_spec.json 作为事实与叙事契约，完整读取一次 builder，再用不超过约 6000 字符的锚定小步修改为每一页登记独立 renderer。稳定页型只作底层机制，禁止读取 pptx_design.py，禁止整体覆盖 builder。独立紧凑 build_task_pptx.py 只用于用户明确要求简单/快速/内部输出或真正固定结构、周期性稿件。base-ppt 只作为灾备：所选精美路线真实构建和一次修正均失败后，必须先尝试另一条适用精美路线；只有两条路线均客观不可用或修正后仍失败才可公开降级。生成 .openkitty/storage/bid_proposal_${execution_id}.pptx，并完成真实渲染、逐页视觉检查及一次有针对性的修正。否则不要强制生成 PPT。不得编造资质、客户案例、认证或价格。`, Tools: []string{"read_file", "write_file", "edit_file", "web_fetch", "persistent_shell", "list_skills", "use_skill", "convert", "vision"}, MaxIter: 150, TimeoutSeconds: 1800, OutputTokenLimit: 8000, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/bid_proposal_draft_${execution_id}.md"}, MinBytes: 1000}},
				{Name: "风险自查", Prompt: `阶段边界：本步骤只做最终风险和合规自查。读取本次执行的响应矩阵和标书初稿，生成 .openkitty/storage/bid_risk_checklist_${execution_id}.md，检查：强制条款遗漏、资格/资质缺口、时间节点、报价与商务风险、技术偏离、证明材料缺口、需要人工确认的问题。最终回答必须给出本次执行的响应矩阵、标书初稿、风险清单路径；如果生成了 PPT，也给出本次执行的 PPTX 路径。统一署名“AI员工：投竞标专员”。`, Tools: []string{"read_file", "write_file"}, MaxIter: 5, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/bid_risk_checklist_${execution_id}.md"}, MinBytes: 500}},
			}},
		"data-analysis-pipeline": {Name: "data-analysis-pipeline", Desc: "数据分析：数据加载→探索分析→可视化→输出报告",
			DatabaseConnections: []PipelineDatabaseConnection{{ID: "analytics_db", Enabled: false, Driver: "mysql", DSNEnv: "OPENKITTY_DATA_ANALYST_DB_DSN", Permission: "readonly", MaxRows: 5000, TimeoutSeconds: 30}},
			Steps: []PipelineStep{
				{Name: "数据加载", Prompt: "阶段边界：本步骤只做数据加载和质量检查，忽略用户请求中的分析/图表/报告等后续目标。先判断输入来源：用户指定数据库、数据表或业务查询时，只使用本 Pipeline 的 analytics_db 调用一次 db_query，并设置 save_as=database_snapshot_${execution_id}.csv；用户指定 CSV/Excel/JSON 等文件时，只调用一次 read_file。除非用户明确要求跨源对照，否则不得同时读取文件和数据库。数据库查询必须只读、限定字段和时间范围，禁止 SELECT * 扫描大表；输出需注明连接ID、SQL口径、CSV快照路径、返回行数、截断状态和数据截至时间。文件来源直接输出原始路径、行列数、数据范围、全部列名、字段类型、缺失值/异常值/重复行检查、前几行样例。数据库未启用或查询失败时明确数据缺口，不得编造结果。禁止输出“接下来/我将生成/let me create”等过程话术。", Tools: []string{"read_file", "db_query"}, DatabaseConnection: "analytics_db", MaxIter: 3},
				{Name: "探索分析", Prompt: "阶段边界：本步骤只做统计探索分析，不生成图表，不生成 Markdown 报告。先从上游输出确定本次唯一数据源：数据库来源使用 db_query 返回的 CSV snapshot，文件来源使用用户指定文件；不得退回扫描其他旧文件。使用 persistent_shell 运行 Python (pandas/numpy/scipy) 做字段识别和自适应分析：1) 通用表必须输出行列数、字段类型、缺失/重复/异常、数值列描述统计、分类列 Top 值、时间列范围和趋势；2) 只有存在销售字段（如 leads/qualified/won/revenue/order/deal/amount/sales 等）时，才计算销售漏斗、营收趋势、客单价、转化率和相关性；3) 不得为不存在的字段编造指标；4) 生成 chart_plan，说明后续应画哪些图。必须把结构化分析结果保存到 .openkitty/storage/analysis_data.json，JSON 至少包含 dataset、source_type、source_path、query_scope、schema、quality、metrics、findings、chart_plan、warnings。硬性写文件规则：先删除旧 analysis_data.json；Python 脚本必须把 numpy/pandas 类型转换为原生 Python 类型，json.dump 必须带 default=str；先写 .openkitty/storage/analysis_data.json.tmp，再用 json.load 读回校验，校验成功后用 os.replace 原子替换为 analysis_data.json；只有校验通过且文件大于 100 字节才能输出成功。完成后直接输出关键指标和该 JSON 的真实路径；不要说下一步要生成图表或报告。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 10},
				{Name: "可视化", Prompt: "阶段边界：本步骤只生成图表，不生成 Markdown 报告。使用 persistent_shell 运行 Python (matplotlib/seaborn)。第一步必须用 Python json.load 校验 .openkitty/storage/analysis_data.json，并且只读取其中 source_path 指向的本次文件或数据库 CSV snapshot；如果无效，基于上游明确的数据源重建一个最小有效 analysis_data.json 后再继续，不要扫描旧文件或反复读取坏文件。生成图表前删除 .openkitty/storage/charts/ 下旧的 01_*.png 到 06_*.png，避免把旧图当新图。按 chart_plan 自适应生成最多 6 个 PNG 到 .openkitty/storage/charts/。销售表优先使用固定文件名：01_revenue_trend.png、02_sales_funnel.png、03_conversion_rate_trend.png、04_leads_vs_won.png、05_avg_deal_size.png、06_correlation_heatmap.png；通用表使用相同编号但按字段内容生成趋势图、分布图、分类 Top 图、缺失值图、散点图或相关性热力图。若某类图缺少必要字段，必须跳过并在输出中说明原因，不得失败或编造数据。脚本里完成文件存在和大小验证；执行成功后直接输出真实 PNG 路径、字节数和跳过原因，不要再调用工具做重复验证，不要说下一步要生成报告。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 10},
				{Name: "输出报告", Prompt: "阶段边界：本步骤只生成最终 Markdown 报告和最终摘要。先用 Python json.load 校验 .openkitty/storage/analysis_data.json，并列出 .openkitty/storage/charts/ 下真实存在且本次生成的 PNG；如果 JSON 无效或图表不足，不要循环重试到迭代上限，直接基于上游数据加载/探索文本生成降级报告，并在数据质量章节写明缺失项。基于 analysis_data.json、上游分析结果和真实存在的图表文件，生成完整数据分析 Markdown 报告并用 write_file 保存到 .openkitty/storage/data_analysis_report.md；如果同名文件已存在，可使用 data_analysis_report_{日期或时间}.md。报告结构必须完整，包含：1) 摘要，3-6条结论先行；2) 数据来源与分析口径；3) 数据概览，行列数、字段、时间范围或样本范围；4) 数据质量，缺失、重复、异常和限制；5) 核心发现，每条都给数据证据；6) 图表解读，逐一引用真实 PNG 路径并解释图中结论；7) 建议与下一步行动；8) 产出文件清单，列出报告、analysis_data.json、图表文件和降级/跳过原因。只有数据中真实存在销售字段时才写销售漏斗/营收结论；通用表则写通用统计洞察、异常点、分布、趋势和可行动建议。最终回答必须包含 3-6 条报告摘要和真实存在的文件路径；禁止输出“我将生成/正在生成/下一步生成/let me generate”。", Tools: []string{"read_file", "write_file", "persistent_shell"}, MaxIter: 8},
			}},
		"hr-recruitment": {Name: "hr-recruitment", Desc: "HR招聘：简历分析→JD撰写→题库生成",
			Steps: []PipelineStep{
				{Name: "简历分析", Prompt: "使用 read_file 读取候选人简历，评估匹配度：技能匹配、经验年限、行业背景、教育程度。输出评分和优劣势分析。", Tools: []string{"read_file"}, MaxIter: 5},
				{Name: "JD撰写", Prompt: "根据岗位需求撰写专业职位描述：岗位职责、任职要求、加分项、薪酬范围、企业亮点。", Tools: []string{"read_file", "write_file"}, MaxIter: 5},
				{Name: "题库生成", Prompt: "基于岗位JD生成结构化面试题库：行为面试题、技术题、情景题。每道题标注考察点和评分标准。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"finance-review": {Name: "finance-review", Desc: "财务审核：票据识别→报销审核→报表生成",
			Steps: []PipelineStep{
				{Name: "票据识别", Prompt: "使用 read_file 读取发票/收据文件，提取：开票日期、金额、税号、商品明细。支持图片和PDF。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "报销审核", Prompt: "根据企业报销制度审核：金额合理性、票据合规性、审批流程完整性。标注异常项和处理建议。", Tools: []string{"read_file"}, MaxIter: 5},
				{Name: "报表生成", Prompt: "生成费用报销汇总表保存到 storage/。包含报销人、金额、类别、审核状态。可输出 CSV/Excel。", Tools: []string{"write_file", "persistent_shell"}, MaxIter: 5},
			}},
		"customer-service": {Name: "customer-service", Desc: "客服处理：问题分类→方案匹配→回复建议→报告链接",
			Steps: []PipelineStep{
				{Name: "问题分类", Prompt: "分析客户问题归类：产品使用、售后维修、退换货、投诉建议、账户问题等。判断紧急度和优先级。", Tools: []string{"read_file", "rag_search"}, MaxIter: 5},
				{Name: "回复建议", Prompt: "根据问题类型匹配标准话术和解决方案。语气温暖专业。复杂问题给出升级建议。若需要输出报告，统一使用“接了什么任务 → 交付什么 → 结果如何 → 怎么产出的”的结构，保存到 .openkitty/storage/，并给出报告链接。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"it-operations": {Name: "it-operations", Desc: "IT运维：状态监控→日志分析→故障排查",
			Steps: []PipelineStep{
				{Name: "状态监控", Prompt: "使用 persistent_shell 检查服务器状态：CPU/内存/磁盘/网络。生成当前状态快照，标注异常指标。", Tools: []string{"persistent_shell"}, MaxIter: 8},
				{Name: "日志分析", Prompt: "使用 read_file + persistent_shell (Python) 分析系统/应用日志，提取错误模式和异常事件。输出时间线和频率统计。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "故障排查", Prompt: "根据监控和日志分析结果，给出故障排查步骤和修复建议。区分紧急和非紧急问题。保存到 storage/。", Tools: []string{"read_file", "write_file"}, MaxIter: 5},
			}},
		"product-management": {Name: "product-management", Desc: "产品经理交付方案：需求基线→PRD与WBS→工期成本与报价→标书→交付复核",
			Steps: []PipelineStep{
				{Name: "需求基线", Prompt: `你是产品经理。阶段边界：本步骤只建立可追溯的需求与商务基线，不写最终 PRD、报价或标书。优先读取用户明确给出的附件、路径或正文；PDF/DOCX/XLSX/PPTX 用 extract，纯文本用 read_file，只有用户给 URL 或明确要求联网时才使用 web_fetch。禁止为找材料扫描无关目录。

输出：业务目标、目标用户/采购方、范围内与范围外事项、功能/非功能需求、交付物、约束、依赖、验收口径、时间与预算线索、已知事实、合理假设、待确认项和风险。缺失但不妨碍初步估算的信息采用清晰标注的区间假设继续；只有缺失会实质改变整个方案时才提出最多 5 个关键问题。不得编造客户数据、资质、案例、人员履历、工期、成本或招标条款。`, Tools: []string{"extract", "read_file", "web_fetch"}, MaxIter: 6},
				{Name: "PRD与项目拆分", Prompt: `阶段边界：本步骤负责把需求转成可执行的 PRD 和 WBS。若用户任务涉及需求分析、产品方案、项目规划、估算、报价或标书，均应生成这两份基础文件；若用户明确只要一个很小的答复，可说明跳过原因而不造文件。

PRD 保存到 .openkitty/storage/project_prd_${execution_id}.md，至少包含背景与目标、角色与场景、范围、用户故事/业务流程、功能和非功能需求、验收标准、数据/接口要求、依赖、风险、开放问题。WBS 保存到 .openkitty/storage/project_wbs_${execution_id}.md，按阶段→工作包→任务拆分，列交付物、角色、前置依赖、验收标准、工作量区间和关键路径；不要用季度路线图替代项目拆分。每项工作量必须可回溯到具体范围。完成后只报告真实写入的路径和关键拆分摘要。`, Tools: []string{"write_file"}, MaxIter: 8},
				{Name: "工期成本与报价", Prompt: `阶段边界：本步骤生成透明、可复算的估算和报价，不写标书正文。读取上一步 PRD/WBS（如已生成），保存 Markdown 到 .openkitty/storage/project_estimate_quote_${execution_id}.md，并保存便于复算的 CSV 到 .openkitty/storage/project_estimate_quote_${execution_id}.csv。

必须逐工作包列出角色、人数、投入人日区间、并行关系、历时区间、单价/成本假设、成本小计和风险缓冲。明确区分：工作量（人日）、项目历时、内部直接成本、第三方费用、风险准备金、税费、毛利/加价假设和客户报价；给出低/基准/高三档及计算公式。用户未提供币种、税率、角色单价或毛利时，使用醒目的“估算假设/待商务确认”而非伪装成确定报价；未知第三方费用单列，不填假数字。若用户未要求报价，也应给项目量级与成本估算，但可省略正式报价有效期等商务条款。

数字纪律：必须使用 persistent_shell 运行确定性计算脚本，禁止心算后手填表格。先以完整精度计算 internal_cost → risk_buffer → pre_tax → with_tax → client_quote_raw，所有中间结果禁止提前取整。除非用户指定其他规则，统一采用 ROUND_UP_0_1_K：client_quote_krmb = ceil(client_quote_raw_krmb * 10) / 10。CSV 必须同时包含 client_quote_raw_krmb、client_quote_krmb 和 rounding_rule；每行 total_days 必须等于各角色人日之和，internal_cost 必须等于各角色人日×单价之和。Markdown 明细表、三档摘要和一句话结论只能复用 CSV 的 client_quote_krmb，不得再次独立四舍五入或改成整数。写完后重新读取 CSV，用同一脚本逐行复算，确认误差为 0 再结束。`, Tools: []string{"read_file", "write_file", "persistent_shell"}, MaxIter: 10},
				{Name: "标书与投标材料", Prompt: `阶段边界：只有用户明确要求标书、投标/竞标方案、招标响应，或已提供招标采购材料时才执行；否则输出“本任务不涉及标书，已跳过”且不得创建空模板。需要执行时，基于招标原文、需求基线、PRD/WBS 和估算生成：响应矩阵 .openkitty/storage/project_bid_response_${execution_id}.md，以及可编辑标书初稿 .openkitty/storage/project_bid_draft_${execution_id}.md。

响应矩阵逐条列招标要求、对应章节、响应状态、证据/材料、责任人或待补充、风险和截止时间。标书至少包含项目理解、总体方案、范围与交付物、技术/实施路线、进度与组织、质量与验收、服务保障、商务响应、报价说明、差异化价值、风险与偏差表。招标材料缺失时只生成带明确待确认项的方案骨架，不得声称合规响应；严禁虚构资质、案例、证书、客户名称、团队履历、盖章信息和确定价格。`, Tools: []string{"extract", "read_file", "write_file", "web_fetch"}, MaxIter: 8},
				{Name: "交付复核", Prompt: `阶段边界：本步骤只复核并汇总产品经理本次真实产物。完整读取本次 PRD、WBS、估算 Markdown 与 CSV；不得把上一步自称“一致”当作证据。使用 persistent_shell 解析 CSV 并独立逐行复算：角色人日合计、角色成本合计、风险准备金、税前金额、含税金额、未取整报价，以及 rounding_rule 对应的最终报价；默认 ROUND_UP_0_1_K 必须验证 client_quote_krmb = ceil(client_quote_raw_krmb * 10) / 10，且中间结果不得提前取整。再核对 Markdown 每个档位/场景的数字与 CSV 最终报价是否逐项一致，并检查范围—PRD—WBS—估算—报价之间是否可追溯。

若发现任何公式、取整、跨文件数字或摘要不一致，必须明确列出差异，使用 write_file 修正本次估算 Markdown/CSV 后重新读取并运行同一复算；只有 mismatch_count=0 才能声称“复核通过”或“完全一致”。无法修正时保留真实失败结论，禁止使用“100% 透明/一致”等表述掩盖差异。最终给出 QA 回执（检查行数、取整规则、mismatch_count）、已采用假设、待客户确认项、Top 风险和下一步，并按“PRD / WBS / 估算报价 / 标书材料”列出真实路径或明确说明为何未生成，统一署名“AI员工：产品经理”。`, Tools: []string{"read_file", "persistent_shell", "write_file"}, MaxIter: 8},
			}},
		"risk-management": {Name: "risk-management", Desc: "风控管理：风险识别→评估分级→预警报告",
			Steps: []PipelineStep{
				{Name: "风险识别", Prompt: "使用 web_fetch 和 read_file 搜集信息，识别业务风险：市场风险、信用风险、操作风险、合规风险、声誉风险。", Tools: []string{"web_fetch", "read_file"}, MaxIter: 10},
				{Name: "评估分级", Prompt: "使用 persistent_shell (Python) 构建风险评估矩阵：发生概率 x 影响程度。输出风险热力图和高风险清单。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 8},
				{Name: "预警报告", Prompt: "生成风险预警报告保存到 storage/。包含：风险总览、Top-N风险、应对措施、监控指标。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"sales-support": {Name: "sales-support", Desc: "销售支持：线索跟进→报价生成→回访提醒",
			Steps: []PipelineStep{
				{Name: "线索跟进", Prompt: "阶段边界：本步骤只读取和整理线索，不生成报价单，不生成回访计划。只调用一次 read_file 读取用户指定销售线索数据；按紧急程度、预算匹配度、截止日期排序。直接输出每日跟进清单：客户名称、优先级、上次沟通摘要、下一步行动、负责人/时间建议。禁止输出“let me/接下来/我将生成”等过程话术。", Tools: []string{"read_file"}, MaxIter: 2},
				{Name: "报价生成", Prompt: "阶段边界：本步骤生成正式报价建议文件。基于上一步线索跟进清单和原始数据，生成包含产品明细、单价、数量、折扣、总价、有效期、报价策略的 Markdown 文件，固定保存到 .openkitty/storage/sales_followup_quote.md；同时保存结构化摘要到 .openkitty/storage/sales_followup_quote.json。完成后直接输出两个真实路径和客户报价摘要，不要说下一步要生成回访。", Tools: []string{"write_file", "persistent_shell"}, MaxIter: 6},
				{Name: "回访提醒", Prompt: "阶段边界：本步骤只生成回访提醒计划。基于上游跟进清单和报价结果，给出试用到期、续约提醒、满意度回访、报价有效期、关键截止日等节点。用 write_file 覆盖保存到 .openkitty/storage/sales_followup_reminders.md。最终回答只包含已保存文件路径和今日优先事项摘要；不要调用多余工具，不要输出过程话术。", Tools: []string{"write_file"}, MaxIter: 3},
			}},
		"social-media-ops": {Name: "social-media-ops", Desc: "社媒运营：内容策划→多平台排期→数据复盘",
			Steps: []PipelineStep{
				{Name: "内容策划", Prompt: "根据品牌调性和热点趋势策划内容选题。使用 web_fetch 调研各平台热门话题。输出月度内容日历草案。", Tools: []string{"web_fetch", "read_file"}, MaxIter: 10},
				{Name: "内容制作", Prompt: "撰写各平台适配内容：微博短文、小红书笔记、抖音脚本、LinkedIn文章。注意各平台内容风格差异。", Tools: []string{"write_file"}, MaxIter: 10},
				{Name: "数据复盘", Prompt: "使用 persistent_shell (Python) 分析运营数据：阅读量、互动率、粉丝增长。输出周/月报保存到 storage/。", Tools: []string{"read_file", "persistent_shell", "write_file"}, MaxIter: 8},
			}},
		"supply-chain-mgmt": {Name: "supply-chain-mgmt", Desc: "供应链管理：供应商评估→成本优化→库存分析",
			Steps: []PipelineStep{
				{Name: "供应商评估", Prompt: "使用 web_fetch 搜集供应商信息，建立评估矩阵：价格、质量、交期、服务、资质。使用 persistent_shell 做加权评分。", Tools: []string{"web_fetch", "persistent_shell"}, MaxIter: 10},
				{Name: "成本优化", Prompt: "分析采购数据，识别成本优化机会：集中采购、替代方案、价格谈判建议。使用 Python 做 spend analysis。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "库存分析", Prompt: "分析库存周转数据：ABC分类、安全库存计算、呆滞料识别。给出库存优化建议。保存报告到 storage/。", Tools: []string{"read_file", "persistent_shell", "write_file"}, MaxIter: 8},
			}},
		"translation-service": {Name: "translation-service", Desc: "翻译服务：原文分析→专业翻译→质量检查",
			Steps: []PipelineStep{
				{Name: "原文分析", Prompt: "使用 read_file 读取待翻译文档，分析：领域（技术/商务/法律/医学等）、术语密度、风格要求。", Tools: []string{"read_file"}, MaxIter: 3},
				{Name: "专业翻译", Prompt: "执行高质量翻译。保持原文格式，确保术语一致性，符合目标语言的表达习惯。", Tools: []string{"write_file"}, MaxIter: 10},
				{Name: "质量检查", Prompt: "逐段检查译文质量：准确性、流畅度、术语一致性、格式完整性。标注需人工复核的段落。", Tools: []string{"read_file", "write_file"}, MaxIter: 5},
			}},
		"training-design": {Name: "training-design", Desc: "培训设计：需求调研→课程设计→测评出题",
			Steps: []PipelineStep{
				{Name: "需求调研", Prompt: "分析培训目标和学员画像：知识水平、技能差距、学习偏好。使用 web_fetch 调研行业最佳实践。", Tools: []string{"web_fetch", "read_file"}, MaxIter: 8},
				{Name: "课程设计", Prompt: "设计培训课程大纲：模块划分→学习目标→内容要点→教学活动→时长分配。每模块标注关键知识点。", Tools: []string{"write_file"}, MaxIter: 8},
				{Name: "测评出题", Prompt: "基于课程内容生成知识测评题：选择题、判断题、简答题。每道题标注难度和考察知识点。", Tools: []string{"write_file"}, MaxIter: 8},
			}},
		"quality-inspection": {Name: "quality-inspection", Desc: "质检管理：标准制定→缺陷追踪→质量报告",
			Steps: []PipelineStep{
				{Name: "标准制定", Prompt: "根据产品类型和行业标准制定检验标准：检验项目、抽样方案、判定准则。引用相关国标/行标。", Tools: []string{"web_fetch", "read_file"}, MaxIter: 8},
				{Name: "缺陷追踪", Prompt: "使用 persistent_shell (Python) 分析缺陷数据：缺陷类型分布、趋势图、帕累托图识别核心问题。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "质量报告", Prompt: "生成质量分析报告保存到 storage/。包含：检验概况、缺陷分析、趋势预警、改进建议。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"event-planning": {Name: "event-planning", Desc: "活动策划：方案设计→预算编制→执行复盘",
			Steps: []PipelineStep{
				{Name: "方案设计", Prompt: "根据活动目标设计完整方案：主题、流程、场地、嘉宾、物料清单、应急预案。输出策划案到 storage/。", Tools: []string{"web_fetch", "write_file"}, MaxIter: 10},
				{Name: "预算编制", Prompt: "编制详细活动预算：场地、搭建、餐饮、物料、嘉宾、宣传等分项。使用 persistent_shell 做费用汇总。", Tools: []string{"persistent_shell", "write_file"}, MaxIter: 5},
				{Name: "执行复盘", Prompt: "活动结束后生成复盘报告：目标达成率、参与数据、亮点与不足、改进建议、ROI分析。", Tools: []string{"read_file", "write_file"}, MaxIter: 8},
			}},
		"admin-support": {Name: "admin-support", Desc: "行政支持：会议协调→文档管理→采购比价",
			Steps: []PipelineStep{
				{Name: "会议协调", Prompt: "根据参会人日程协调会议时间，生成会议议程和参会须知。发送会议邀请信息模板。", Tools: []string{"read_file", "write_file"}, MaxIter: 5},
				{Name: "文档管理", Prompt: "整理归档文件：分类、命名规范、目录索引。使用 read_file 扫描文件夹生成文件清单。", Tools: []string{"read_file", "write_file"}, MaxIter: 8},
				{Name: "采购比价", Prompt: "使用 web_fetch 搜索办公用品/服务报价，生成比价表。综合考虑价格、质量、售后服务。", Tools: []string{"web_fetch", "write_file"}, MaxIter: 8},
			}},

		// ====================== 项目助理 DE 强化 ======================
		"daily-standup": {Name: "daily-standup", Desc: "每日站会：git变更收集→分类总结→站会纪要生成",
			Steps: []PipelineStep{
				{Name: "收集变更", Prompt: "使用 git tool 获取昨日至今的 git log（action=log, since=昨天日期, format=oneline, max_count=100）。记录所有提交信息、作者、文件变更统计。使用 git diff 看关键变更内容。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "分类总结", Prompt: "将收集到的 git 提交分为：新功能(feat)、修复(fix)、重构(refactor)、文档(docs)、测试(test)、其他(chore)。每个分类总结1-2句话说明核心变更。标注影响范围和高风险变更。", Tools: []string{"read_file"}, MaxIter: 5},
				{Name: "生成纪要", Prompt: "生成 Markdown 格式每日站会纪要保存到 storage/daily-standup-YYYY-MM-DD.md：1)日期和天气 2)昨日完成(按分类) 3)今日计划(基于未完成项的推测) 4)阻塞项和风险 5)需要协助的事项。格式清晰适合站会朗读。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"pr-review": {Name: "pr-review", Desc: "PR代码审查：diff分析→安全检查→风格检查→逻辑审查→报告",
			Steps: []PipelineStep{
				{Name: "变更分析", Prompt: "使用 git tool（action=diff, branch=对比分支）获取 PR 的全部代码变更。统计：文件数、新增行、删除行、涉及语言。列出所有变更文件和变更类型。如有 --cached 未提交变更也一并检查。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "安全检查", Prompt: "逐文件审查安全问题：1)硬编码密钥/密码/Token 2)SQL注入(XML/字符串拼接) 3)XSS(未转义HTML) 4)命令注入(os.exec未过滤) 5)路径遍历 6)不安全的反序列化 7)缺失权限检查 8)日志泄露敏感信息。每个问题标注严重度(高/中/低)和具体行号。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 15},
				{Name: "风格逻辑", Prompt: "审查代码风格和逻辑：1)命名规范(变量/函数) 2)空值/边界处理 3)并发安全(锁/race) 4)错误处理完整性 5)资源释放(defer close) 6)重复代码 7)函数过长(>50行) 8)不必要依赖。给出具体改进建议。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "生成报告", Prompt: "汇总生成 PR Review 报告保存到 storage/pr-review-{timestamp}.md：1)概览 (变更统计) 2)严重问题 (必须修复) 3)改进建议 (建议修复) 4)代码亮点 (好的实践) 5)总结和投票 (+1批准 / ±0观望 / -1拒绝)。报告应具体到文件和行号。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"release-manager": {Name: "release-manager", Desc: "发版管理：changelog生成→版本号建议→打Tag→构建",
			Steps: []PipelineStep{
				{Name: "生成Changelog", Prompt: "使用 git tool（action=log）获取自上一 tag 以来的所有提交。按类型(feat/fix/refactor/docs/test)分类整理 Changelog。参考 keepachangelog.com 格式。识别是否有 breaking changes 需要大版本升级。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "版本建议", Prompt: "根据 Changelog 内容建议新版本号（遵循 semver）：major(breaking changes) / minor(新功能) / patch(修复)。说明版本升级理由。如为 Go 项目检查 go.mod 版本兼容性。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 5},
				{Name: "构建验证", Prompt: "执行构建验证：1)使用 code_check tool 运行测试(action=test) 2)编译检查(action=build) 3)lint 检查(action=lint)。如有失败则拦截发版。输出验证结果。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "打Tag发版", Prompt: "确认发版：1)将 Changelog 写入 CHANGELOG.md 2)git add + commit 3)git tag 打版本标签 4)生成发版摘要。输出完整的发版命令和结果。注意：此步骤需审批，确保人工确认后再执行 tag 和 push 操作。", Tools: []string{"write_file", "persistent_shell"}, MaxIter: 8, RequireApproval: true},
			}},

		// ====================== 代码知识库 RAG ======================
		"code-ingest": {Name: "code-ingest", Desc: "代码库摄入：扫描→切片→向量化→入库",
			Steps: []PipelineStep{
				{Name: "扫描代码", Prompt: "扫描目标代码目录：使用 glob tool 发现所有源码文件(.go/.py/.js/.ts/.java/.rs等)。过滤 vendor/node_modules/.git 目录。生成文件清单：路径、语言、大小、行数估计。列出目录树结构。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "智能切片", Prompt: "对每个代码文件进行语义切片：1)按函数/类/方法边界切分 2)保留 import/package 声明作为上下文 3)每片 50-200 行 4)保留注释和文档字符串。使用 code_check tool 验证切片后的代码语法完整性。输出切片统计：文件数、总切片数、平均每文件切片数。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 20},
				{Name: "向量化入库", Prompt: "将代码切片通过 RAG 嵌入模型向量化，存入知识库(可用 rag_search tool 的 ingest 模式)。每个切片携带元数据：源文件路径、函数名、行号范围、语言、最后修改时间。使用知识库 slug: code-kb 存储。验证入库数量与切片数量一致。", Tools: []string{"persistent_shell"}, MaxIter: 15, KBSlugs: []string{"code-kb"}},
			}},
		"code-qa": {Name: "code-qa", Desc: "代码问答：理解问题→RAG搜索→代码定位→解答",
			Steps: []PipelineStep{
				{Name: "问题理解", Prompt: "理解用户的代码问题：提取关键概念（函数名/模块名/API名称/错误信息/技术栈）。判断问题类型：定位查询(这个在哪)、理解查询(这段代码什么意思)、修改建议(怎么改)。确定搜索关键词。", NoTools: true, MaxIter: 3},
				{Name: "代码搜索", Prompt: "使用 rag_search tool 在 code-kb 知识库中搜索相关内容。可能需要多轮搜索：先精确匹配函数/类名，再语义搜索相关逻辑。收集 5-10 个最相关的代码片段。记录每个结果的源文件和行号。", Tools: []string{"rag_search"}, MaxIter: 10, KBSlugs: []string{"code-kb"}},
				{Name: "分析解答", Prompt: "综合搜索到的代码片段回答用户问题：1)直接引用代码(标注文件和行号) 2)解释代码逻辑 3)如涉及多处调用，追踪调用链 4)如用户问修改建议，给出 diff 格式的具体修改方案。如果代码库中没有找到相关内容，诚实告知。", Tools: []string{"read_file"}, MaxIter: 8},
			}},

		// ====================== 编程教育 Agent ======================
		"onboarding-walkthrough": {Name: "onboarding-walkthrough", Desc: "新人引导：项目理解→架构讲解→流程追踪→上手建议",
			Steps: []PipelineStep{
				{Name: "项目扫描", Prompt: "扫描项目结构：使用 glob 发现关键文件(入口/main/config/README)，用 git tool 统计项目规模(文件数/行数/语言分布/提交历史)。识别项目类型和技术栈（Go模块/Python包/前端框架等）。描述项目是做什么的。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "架构梳理", Prompt: "分析项目架构：读取入口文件理解启动流程，追踪关键依赖链。识别设计模式。绘制模块依赖关系。标注核心模块和数据流。如项目有 README 先读 README。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 15},
				{Name: "流程追踪", Prompt: "根据用户指定的业务功能（如支付流程、登录流程），追踪完整调用链：从入口 → 中间件 → 业务逻辑 → 数据层 → 外部调用 → 返回。每一步标注文件名和函数名。用 Mermaid 流程图格式输出。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 15},
				{Name: "上手建议", Prompt: "生成新人上手指南保存到 storage/onboarding-guide.md：1)开发环境搭建(依赖/构建/运行) 2)项目结构概览 3)核心模块说明 4)开发规范(代码风格/提交规范) 5)推荐的阅读顺序(从易到难) 6)常见问题和解决方案。", Tools: []string{"write_file"}, MaxIter: 8},
			}},

		// ====================== 运维+编程结合 ======================
		"auto-remediation": {Name: "auto-remediation", Desc: "自动修复：告警分析→日志诊断→根因定位→修复建议→PR生成",
			Steps: []PipelineStep{
				{Name: "告警分析", Prompt: "分析告警信息：提取关键指标(CPU/内存/磁盘/网络/错误率/延迟)。确定影响范围(服务/实例/用户)。判断紧急程度(P0-P4)。拉取相关时间段的系统日志和应用日志。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "根因定位", Prompt: "深入分析日志定位根因：使用 persistent_shell 执行 grep/awk/jq 过滤日志。关联代码库：使用 git blame 查找最近变更的作者和提交。分析最近的部署和配置变更。如果是已知问题模式直接匹配。定位到具体代码文件和行号。", Tools: []string{"persistent_shell", "read_file"}, MaxIter: 15},
				{Name: "修复方案", Prompt: "生成修复方案：1)问题描述 2)根因分析 3)修复方案(具体代码修改，提供 diff 格式) 4)验证步骤(测试/灰度/监控) 5)回滚方案。评估修复风险(A/B/C级)。如需紧急修复建议直接生成 PR。", Tools: []string{"write_file"}, MaxIter: 10},
				{Name: "生成PR", Prompt: "将修复方案转化为可执行的 PR：1)使用 git worktree 创建隔离工作区 2)应用代码修改 3)运行 code_check 验证 4)git commit 5)生成 PR 描述。此步骤需要审批确认后才执行代码修改。", Tools: []string{"write_file", "persistent_shell"}, MaxIter: 10, RequireApproval: true},
			}},
		"deploy-rollback-decision": {Name: "deploy-rollback-decision", Desc: "部署回滚决策：发布后监控→异常检测→回滚建议",
			Steps: []PipelineStep{
				{Name: "部署监控", Prompt: "发布后持续监控关键指标：错误率、延迟P99、QPS、CPU使用率、内存使用率。对比发布前基线数据。采集时间窗口：发布 -5min 到发布 +15min。标注是否有异常波动。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "异常判定", Prompt: "根据监控数据判定是否需要回滚：1)错误率上升 >200% → 建议立即回滚 2)P99延迟上升 >50% → 建议回滚 3)QPS下降 >30% → 警告 4)无异常 → 确认发布成功。综合考虑各指标权重给出决策建议。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "回滚执行", Prompt: "如建议回滚：1)生成回滚命令(git revert/helm rollback/kubectl rollout undo) 2)评估回滚影响(时间窗口/数据一致性) 3)执行回滚并验证服务恢复 4)通知相关人员。如不需要回滚：确认发布成功并更新监控基线。此步骤需审批确认。", Tools: []string{"persistent_shell"}, MaxIter: 8, RequireApproval: true},
			}},

		// ====================== 编程开发能力 ======================
		"developer-router": {Name: "developer-router", Desc: "开发工程师智能路由：LLM意图识别→自动选择编码/调试/测试/解释/重构/文档",
			Steps: []PipelineStep{
				{Name: "意图识别", Prompt: "你是开发工程师的智能路由器。分析用户请求，确定意图并输出分类标签。\n\n用户请求: \n\n分类规则：\n- 用户要初始化项目/生成CLAUDE.md/分析项目结构/setup项目/整理项目规范 → INTENT:project_init\n- 用户要写代码/实现功能/开发/创建 → INTENT:code_gen\n- 用户要修bug/调试/报错/不工作/出错 → INTENT:debug_fix\n- 用户要写测试/加测试/单测 → INTENT:test_gen\n- 用户要解释代码/这段代码什么意思/分析代码逻辑 → INTENT:code_explain\n- 用户要重构/优化代码/改进/整理 → INTENT:code_refactor\n- 用户要写注释/文档/docstring/README → INTENT:code_doc\n- 用户要审查/review代码 → INTENT:code_review\n- 无法确定 → INTENT:code_gen\n\n只输出一行，格式: INTENT:xxx", NoTools: true, MaxIter: 1,
					Routing: []StepRoute{
						{When: "contains 'INTENT:project_init'", Goto: "项目初始化"},
						{When: "contains 'INTENT:code_gen'", Goto: "编码实现"},
						{When: "contains 'INTENT:debug_fix'", Goto: "错误收集"},
						{When: "contains 'INTENT:test_gen'", Goto: "测试分析"},
						{When: "contains 'INTENT:code_explain'", Goto: "代码解读"},
						{When: "contains 'INTENT:code_refactor'", Goto: "重构分析"},
						{When: "contains 'INTENT:code_doc'", Goto: "文档分析"},
						{When: "contains 'INTENT:code_review'", Goto: "PR变更分析"},
					}},
				// project-init steps
				{Name: "项目初始化", Prompt: "扫描项目目录结构，分析技术栈和编码规范，生成 CLAUDE.md 文件。\n\n1) 使用 glob 或 list_dir 扫描项目根目录，识别关键文件(go.mod/package.json/requirements.txt/Makefile等)\n2) 读取关键配置文件确定技术栈、构建命令、测试命令\n3) 读取 README 和主要源文件了解项目架构和编码风格\n4) 生成 CLAUDE.md 保存到项目根目录，包含: 项目概述、构建/测试/运行命令、架构说明、编码规范、常用命令\n\n参考模板：项目概述→项目结构→构建命令→测试命令→编码规范→架构说明", Tools: []string{"persistent_shell", "read_file", "write_file"}, MaxIter: 15},
				// code-gen steps
				{Name: "编码实现", Prompt: "先尝试读取项目根目录的 CLAUDE.md 或 AGENTS.md 了解项目规范。然后理解用户需求：确认语言、技术栈、输入输出、边界条件。\n\n对于复杂的多文件编码任务，优先使用 external_agent 工具（调用 Claude Code CLI），它能够自主读取项目、编写代码、运行测试并修复错误。简单单文件修改可以直接用 edit_file。\n\n严格遵循项目编码规范编写代码并写入文件。", Tools: []string{"read_file", "write_file", "edit_file", "external_agent"}, MaxIter: 12},
				{Name: "构建测试", Prompt: "执行构建验证：code_check build。如有编译错误，读取错误信息，修复代码，重新构建。反复直到编译通过(最多3轮)。", Tools: []string{"persistent_shell", "read_file", "edit_file"}, MaxIter: 12},
				{Name: "测试运行", Prompt: "运行测试：code_check test。如有失败，分析失败原因，修复代码或测试，重新运行。反复直到通过(最多3轮)。输出最终结果。", Tools: []string{"persistent_shell", "read_file", "edit_file"}, MaxIter: 12},
				// debug-fix steps
				{Name: "错误收集", Prompt: "收集错误信息：读取报错日志或错误输出，定位报错文件和行号。理解错误类型。使用 read_file 读取相关代码。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "根因定位", Prompt: "追踪数据流/调用链定位根因。用 git blame 找相关提交。制定修复方案。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "实施修复", Prompt: "用 edit_file 精确修改有问题的代码。保持修改最小化。", Tools: []string{"read_file", "edit_file", "write_file"}, MaxIter: 8},
				{Name: "修复验证", Prompt: "验证修复：code_check build → code_check test。如失败重新分析修复(最多3轮)。输出修复摘要。", Tools: []string{"persistent_shell", "read_file", "edit_file"}, MaxIter: 10},
				// test-gen steps
				{Name: "测试分析", Prompt: "读取目标代码：分析函数签名、逻辑分支、边界条件、依赖。设计测试用例清单(正常/边界/异常/空值)。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "测试编写", Prompt: "按项目测试框架规范编写测试代码(Go: table-driven, Python: pytest, JS: jest)。覆盖所有用例。写入测试文件。", Tools: []string{"read_file", "write_file", "edit_file"}, MaxIter: 12},
				{Name: "测试运行", Prompt: "运行测试验证：code_check test。如有失败修复测试代码。输出测试结果摘要(通过/失败/覆盖)。", Tools: []string{"persistent_shell", "read_file", "edit_file"}, MaxIter: 8},
				// code-explain steps
				{Name: "代码解读", Prompt: "读取目标代码文件或函数。分析：1)功能概述 2)输入输出 3)核心逻辑(逐步讲解) 4)关键算法 5)依赖关系 6)潜在问题。如涉及多文件，追踪调用链。输出详细解释(Markdown格式)。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 12},
				{Name: "调用链追踪", Prompt: "使用 LSP 或 grep 追踪函数的所有调用者和被调用者。绘制调用关系图(Mermaid格式)。标注数据流向和关键转换点。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				// code-refactor steps
				{Name: "重构分析", Prompt: "读取目标代码，分析可改进之处：1)命名优化 2)函数拆分(过长函数) 3)重复代码消除 4)耦合降低 5)错误处理增强 6)性能优化。列出重构建议(按优先级排序)。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 10},
				{Name: "重构执行", Prompt: "按优先级应用重构：每次改一个方面，改完验证编译。使用 edit_file 精确修改。保持功能不变。如涉及多处修改按依赖顺序处理。", Tools: []string{"read_file", "edit_file", "write_file"}, MaxIter: 15},
				{Name: "重构验证", Prompt: "验证重构后代码：code_check build → code_check test → code_check lint。确保原功能不受影响。输出重构前后对比。", Tools: []string{"persistent_shell"}, MaxIter: 8},
				// code-doc steps
				{Name: "文档分析", Prompt: "读取目标代码，识别需要文档化的内容：1)公共函数/方法签名 2)复杂逻辑 3)配置项 4)API接口。确定文档风格(GoDoc/JSDoc/docstring)。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "文档生成", Prompt: "为代码生成文档：1)每个公共函数添加参数说明和返回值 2)复杂逻辑添加行内注释 3)生成或更新 README(如需要)。使用 edit_file 精确插入注释，保持代码格式。", Tools: []string{"read_file", "edit_file", "write_file"}, MaxIter: 12},
				// code-review steps (shared with pr-review)
				{Name: "PR变更分析", Prompt: "使用 git tool 获取变更(diff/log)。统计变更范围。", Tools: []string{"persistent_shell"}, MaxIter: 8},
				{Name: "安全检查", Prompt: "审查安全问题：密钥/注入/XSS/命令注入/路径遍历/缺失权限检查。标注严重度和具体行号。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 12},
				{Name: "风格逻辑", Prompt: "审查风格和逻辑：命名/边界处理/并发安全/错误处理/资源释放/重复代码。给出改进建议。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "审查报告", Prompt: "生成Review报告保存到 storage/：概览→严重问题→改进建议→代码亮点→总结。具体到文件和行号。", Tools: []string{"write_file"}, MaxIter: 5},
			}},
		"code-gen": {Name: "code-gen", Desc: "代码生成：需求理解→编码→测试→审查",

			Steps: []PipelineStep{
				{Name: "需求理解", Prompt: "理解用户需求：确认编程语言、技术栈、输入输出、边界条件。如果是修改现有代码，先用 read_file 读取目标文件。如果是新功能，确认项目结构和规范。列出关键设计决策。", Tools: []string{"read_file"}, MaxIter: 5},
				{Name: "编码实现", Prompt: "编写代码：遵循项目现有风格和命名规范。处理错误、边界条件。添加必要的导入。使用 write_file 或 edit_file 写入代码。如涉及多个文件按依赖顺序编写。", Tools: []string{"read_file", "write_file", "edit_file"}, MaxIter: 12},
				{Name: "测试验证", Prompt: "验证代码正确性：1)使用 code_check tool 做语法检查(action=syntax) 2)编译检查(action=build) 3)运行测试(action=test) 4)lint检查(action=lint)。如有失败则修复代码重新验证。最多反复3次。", Tools: []string{"persistent_shell"}, MaxIter: 10},
				{Name: "代码审查", Prompt: "自我审查代码质量：1)安全性(密钥/注入/XSS) 2)性能(算法复杂度/N+1查询) 3)可读性(命名/函数长度) 4)错误处理 5)并发安全。列出发现的问题和改进建议。", Tools: []string{"read_file"}, MaxIter: 5},
			}},
		"debug-fix": {Name: "debug-fix", Desc: "调试修复：错误分析→根因定位→修复→验证",
			Steps: []PipelineStep{
				{Name: "错误收集", Prompt: "收集错误信息：1)读取编译错误输出或运行日志 2)定位报错的文件和行号 3)理解错误类型(语法/类型/空指针/死锁/逻辑) 4)判断影响范围。使用 read_file 读取相关文件。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "根因分析", Prompt: "定位根因：1)追踪数据流或调用链 2)检查相关变量的赋值和状态 3)用 persistent_shell 添加调试日志或断点 4)如果是回归问题，用 git log/blame 找到引入的提交。总结根因和修复方案。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 12},
				{Name: "实施修复", Prompt: "应用修复：1)使用 edit_file 精确修改有问题的代码 2)保持修改最小化(只改必要的) 3)如涉及多处修改，按依赖顺序处理 4)更新相关注释或文档。", Tools: []string{"read_file", "edit_file", "write_file"}, MaxIter: 10},
				{Name: "回归验证", Prompt: "验证修复：1)code_check syntax → 2)code_check build → 3)code_check test → 4)手动运行验证场景。确保修复没有引入新问题。如果验证失败重新分析。", Tools: []string{"persistent_shell"}, MaxIter: 10},
			}},
		"test-gen": {Name: "test-gen", Desc: "测试生成：代码分析→用例设计→测试编写→运行验证",
			Steps: []PipelineStep{
				{Name: "代码分析", Prompt: "分析目标代码：1)读取函数签名和逻辑 2)识别所有输入组合(正常/边界/异常) 3)理解依赖(Mock需求) 4)确定测试框架和命名规范。列出测试用例清单。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 8},
				{Name: "测试编写", Prompt: "生成测试代码：1)按项目测试框架规范编写(Go: table-driven tests, Python: pytest, JS: jest) 2)覆盖：正常路径/边界值/错误输入/异常处理 3)Mock外部依赖 4)使用 write_file 或 edit_file 写入测试文件。每个测试用例有清晰的 Given/When/Then 注释。", Tools: []string{"read_file", "write_file", "edit_file"}, MaxIter: 12},
				{Name: "运行验证", Prompt: "运行测试验证覆盖率：1)code_check test 执行全部测试 2)如有失败检查是测试问题还是代码问题 3)修复后重新运行直到全部通过。输出测试结果摘要(通过/失败/覆盖率)。", Tools: []string{"persistent_shell"}, MaxIter: 10},
			}},

		// ====================== 智能客服机器人 ======================
		"cs-router": {Name: "cs-router", Desc: "智能客服路由：意图识别→风控检查→路由到售前/售后",
			DatabaseConnections: []PipelineDatabaseConnection{{ID: "customer_db", Enabled: false, Driver: "mysql", DSNEnv: "OPENKITTY_CUSTOMER_SERVICE_DB_DSN", Permission: "readonly", AllowedTables: []string{"v_customer_profile", "v_product_catalog", "v_inventory", "v_order_status", "v_logistics_status", "v_ticket_history"}, DeniedColumns: []string{"password", "password_hash", "id_card", "bank_card"}, MaxRows: 100, TimeoutSeconds: 10}},
			Steps: []PipelineStep{
				{Name: "意图识别", Prompt: `你是智能客服的调度员。分析客户消息，输出路由决策。

客户消息:

## 路由规则
- 客户咨询商品、价格、库存、促销、推荐 → 输出 AGENT:pre_sales
- 客户要下单、支付、改地址、取消订单 → 输出 AGENT:during_sales
- 客户查物流、要退款、退货、投诉 → 输出 AGENT:after_sales
- 问候、闲聊、其他 → 输出 AGENT:general

只输出一行: AGENT:xxx`, NoTools: true, MaxIter: 1,
					Routing: []StepRoute{
						{When: "contains 'AGENT:pre_sales'", Goto: "售前服务"},
						{When: "contains 'AGENT:during_sales'", Goto: "售中服务"},
						{When: "contains 'AGENT:after_sales'", Goto: "售后服务"},
						{When: "", Goto: "通用回复"},
					},
					StepExamples: []StepExample{
						{UserMessage: "这个蓝牙耳机多少钱", RouteTo: "AGENT:pre_sales"},
						{UserMessage: "这款有货吗", RouteTo: "AGENT:pre_sales"},
						{UserMessage: "推荐一款适合跑步的耳机", RouteTo: "AGENT:pre_sales"},
						{UserMessage: "我下单了但是付不了款", RouteTo: "AGENT:during_sales"},
						{UserMessage: "帮我改一下收货地址", RouteTo: "AGENT:during_sales"},
						{UserMessage: "我不要了帮我取消订单", RouteTo: "AGENT:during_sales"},
						{UserMessage: "我的快递到哪了", RouteTo: "AGENT:after_sales"},
						{UserMessage: "收到的耳机左耳没声音要退货", RouteTo: "AGENT:after_sales"},
						{UserMessage: "退款什么时候到账", RouteTo: "AGENT:after_sales"},
						{UserMessage: "我要投诉,商品质量太差了", RouteTo: "AGENT:after_sales"},
						{UserMessage: "你好", RouteTo: "AGENT:general"},
						{UserMessage: "谢谢再见", RouteTo: "AGENT:general"},
					},
				},
				{Name: "售前服务", Prompt: customerservice.GetCSQualityPrompt() + "\n\n## 当前任务\n\n\n你是售前客服专员。职责：商品咨询、价格查询、库存确认、促销活动介绍、商品推荐和对比。\n\n优先使用 cs_product_query 查询商品信息，使用 rag_search 查询FAQ知识库。只有标准客服工具无法覆盖且本 Pipeline 已启用 customer_db 时，才可用 db_query 查询授权只读视图；必须使用最小字段和明确条件，禁止查询或输出敏感字段。\n\n回复时先确认客户需求，再给出具体信息。不确定的信息使用工具查询，不要编造。", Tools: []string{"cs_product_query", "rag_search", "db_query"}, DatabaseConnection: "customer_db", MaxIter: 8},
				{Name: "售后服务", Prompt: customerservice.GetCSQualityPrompt() + "\n\n## 当前任务\n\n\n你是售后客服专员。职责：物流追踪、退款处理、退货指导、投诉受理。\n\n可用工具:\n- cs_order_query: 查询订单详情\n- cs_logistics_track: 追踪物流\n- cs_order_refund: 处理退款\n- cs_submit_complaint: 提交投诉工单\n- db_query: 仅在标准客服查询工具无法覆盖且 customer_db 已启用时查询授权只读视图\n\n处理流程：先查询订单确认信息→再根据客户需求使用对应工具→给出明确方案。db_query 不得执行退款、取消、改址或工单写入；所有写操作仍走专用客服工具及其审批。退款操作前必须确认订单号和退款原因。", Tools: []string{"cs_order_query", "cs_logistics_track", "cs_order_refund", "cs_submit_complaint", "rag_search", "db_query"}, DatabaseConnection: "customer_db", MaxIter: 10},
				{Name: "售中服务", Prompt: customerservice.GetCSQualityPrompt() + "\n\n## 当前任务\n\n\n你是售中客服专员。职责：下单引导、支付问题、修改订单、取消订单、修改地址。\n\n可用工具:\n- cs_order_query: 查询订单\n- cs_cancel_order: 取消待支付订单\n- cs_change_address: 修改未发货订单的地址\n- db_query: 仅在标准客服查询工具无法覆盖且 customer_db 已启用时查询授权只读视图\n\n注意：db_query 不得执行任何写操作；取消和改址必须走专用工具。取消订单仅限pending状态。已发货订单无法修改地址。", Tools: []string{"cs_order_query", "cs_cancel_order", "cs_change_address", "rag_search", "db_query"}, DatabaseConnection: "customer_db", MaxIter: 8},
				{Name: "通用回复", Prompt: customerservice.GetCSQualityPrompt() + "\n\n## 当前任务\n\n\n你是通用客服。处理问候、闲聊、常见FAQ。使用 rag_search 查询知识库获取标准答案。帮助客户引导到正确的服务：商品咨询→售前，订单问题→售中，物流退款→售后。", Tools: []string{"rag_search", "cs_product_query"}, MaxIter: 5},
			}},
		"cs-pre-sales": {Name: "cs-pre-sales", Desc: "售前客服：商品咨询→信息查询→推荐回复",
			Steps: []PipelineStep{
				{Name: "商品查询", Prompt: customerservice.GetCSQualityPrompt() + "\n\n客户咨询: \n\n使用 cs_product_query 搜索商品。用 rag_search 查询FAQ。给出具体信息：价格、库存、规格、促销等。不确定时主动帮客户确认。", Tools: []string{"cs_product_query", "rag_search"}, MaxIter: 8},
			}},
		"cs-after-sales": {Name: "cs-after-sales", Desc: "售后客服：订单确认→问题处理→方案回复",
			Steps: []PipelineStep{
				{Name: "售后处理", Prompt: customerservice.GetCSQualityPrompt() + "\n\n客户问题: \n\n1) 先用 cs_order_query 查询订单确认信息\n2) 根据客户需求使用对应工具(cs_logistics_track/cs_order_refund/cs_submit_complaint)\n3) 退款前确认订单号和原因\n4) 投诉及时提交工单\n5) 问题解决后可使用 cs_satisfaction 发送满意度回访\n\n如果所有工具都失败，建议客户转人工客服。", Tools: []string{"cs_order_query", "cs_logistics_track", "cs_order_refund", "cs_submit_complaint", "cs_satisfaction", "rag_search"}, MaxIter: 12},
			}},

		// ====================== 获客AI员工 ======================
		"lead-acquisition": {Name: "lead-acquisition", Desc: "获客流程：潜客发现→画像分析→意向评级→触达话术→跟进计划",
			Steps: []PipelineStep{
				{Name: "潜客发现", Prompt: `你是获客专员。你的任务是发现和筛选潜在客户。

当前任务:

## 关键区分：谁是客户、谁不是
- **客户** = 需要购买/使用我们产品的人（需求方）
- **竞品** = 跟我们做同样产品的人（供给方）
- 如果你搜到的是"做AI客服的公司"——那是竞品，不是客户
- 客户是"需要AI客服的公司"——比如电商平台、银行、电信运营商
- 搜索时始终问自己：这个公司会买我们的产品吗？

## 工作流程
1. 理解我们卖什么 → 推断谁需要买 → 搜索需要买的公司
2. 使用 web_search 搜索需求方（不要搜供给方）
3. 使用 web_fetch 深入了解有采购需求信号的公司
4. 识别采购需求信号(融资/扩张/招聘/技术升级/新建客服中心)
5. 对有价值的线索使用 lead_save 建档

输出报告时统一署名“AI员工：获客专员”，不要出现 pipe_*、lead-acquisition 等内部名称。
重点关注：公司融资/扩张/招聘/技术栈变化等采购需求信号。`, Tools: []string{"web_search", "web_fetch", "lead_research", "lead_discover", "platform_search"}, MaxIter: 12},
				{Name: "画像分析", Prompt: `深入分析潜客画像，评估是否为理想客户。

## 分析维度
- 公司基本面：规模、行业地位、发展阶段
- 决策链：关键决策人、采购流程
- 痛点匹配：我们的方案能否解决其核心问题
- 预算能力：融资情况、历史IT投入
- 竞争态势：是否有竞品在接触

分析完成后使用 lead_save 存入CRM系统。
使用 lead_score 对线索进行评分(0-100)。
使用 lead_enrich 充实档案中的不足信息。`, Tools: []string{"lead_save", "lead_score", "lead_enrich", "web_fetch", "web_search"}, MaxIter: 10},
				{Name: "触达策略", Prompt: `根据潜客画像制定个性化触达策略。

## 策略制定
1. 确定最佳触达渠道（邮件/微信/电话/LinkedIn）
2. 确定触达时机（为什么是现在？）
3. 制定差异化价值主张
4. 使用 lead_outreach 生成个性化话术

话术要点：
- 一句话说明来意和价值
- 引用同行业成功案例
- 明确的下一步行动邀约
- 专业但不生硬`, Tools: []string{"lead_outreach", "web_search"}, MaxIter: 8},
				{Name: "跟进计划", Prompt: `制定完整的跟进计划和转化策略。

## 计划内容
1. 时间线：第一次触达→二次跟进→产品演示→方案报价→成交
2. 关键节点和检查点
3. 风险预警和应对方案
4. 对低意向线索使用 lead_nurture 制定长期培育计划
5. 使用 lead_update 更新线索状态
6. 使用 lead_pipeline 查看整体获客管道

输出完整的获客作战计划，保存到 storage/lead-plan-{name}.md`, Tools: []string{"lead_update", "lead_pipeline", "lead_nurture", "write_file"}, MaxIter: 8},
			}},
		"lead-research": {Name: "lead-research", Desc: "潜客研究：产品画像与SWOT→搜索策略→多源搜索与核验→报告整理",
			Steps: []PipelineStep{
				{Name: "产品画像与SWOT", Prompt: `## 产品画像与 SWOT
你是获客研究员。当前步骤只负责建立可信的产品画像、SWOT 和客户筛选边界。允许联网搜索和读取目标产品自身的一方资料，完成前禁止搜索潜客公司、采购线索或候选客户。

任务:

## 输入类型识别
用户输入可能是三种形式：
1. 目录：例如 E 盘下的 ai-node、一个项目文件夹、一个代码仓库
2. 文件：例如 README.md、需求文档、产品介绍、配置文件
3. 一段文字：例如产品描述、功能说明、销售线索说明

## 证据与置信度
1. 当前用户本轮明确指定的产品名称、描述、URL、目录或文件定义研究对象。默认工作区只是执行环境；用户没有提供本地路径时，不得扫描工作区来猜产品。
2. 用户提供的目录是查找范围，不自动证明其中所有项目都属于目标产品。使用本地材料前，根据材料自述、项目元数据、品牌、包名或一致的功能链路确认归属；同目录、功能相似或文件最显眼都不足以改变用户指定的目标。
3. 按证据强度而不是固定来源清单判断：
   - A 级·直接证据：用户对目标产品的明确说明，或归属明确的官网、官方文档、产品材料、源码和可验证功能；
   - B 级·交叉证据：两个相互独立且内容一致的可信二手来源；
   - C 级·分析假设：根据 A/B 级证据作出的合理推演，必须写出依据、置信度和后续验证方式；
   - 未知：现有材料无法支持，不得为了补齐表格编造。
4. 来源只能证明其覆盖范围。源码、配置和依赖可以支持技术能力与实现方式，但不能单独证明定价、客户案例、市场份额或商业模式；搜索结果摘要可用于发现候选页面及记录摘要明确展示的信息，详细产品事实应尽量打开原页面核验。
5. 官网、官方文档和产品方材料通常是最强的一方来源，但不是完成初步画像的强制前提。官网不存在、暂时打不开或搜索超时，不等于产品身份一定不明；可以结合用户描述、其他直接材料和独立二手来源形成带置信度的暂定结论。
6. web_fetch/web_navigate 的直接 URL 必须来自用户输入、管理员可选配置或本轮 web_search 的真实返回结果；禁止猜测域名、GitHub 账号、仓库地址或常见路径。当前来源低质量或不可访问时，改变来源类型、搜索引擎或证据路线，不机械重复等价查询。
7. 同会话旧报告或旧助手结论只能作为待验证假设，不得自动视为产品事实，也不得强制复用旧候选公司、评级、价格或市场判断。
8. 事实、用户主张、分析判断、待验证假设和未知必须分层记录。后续 SWOT、ICP、竞品类别和机会判断可以使用已确认事实与明确标注的分析假设，不能把推断伪装成已核实事实。
9. 产品身份不得按功能相似性迁移。外部页面、搜索摘要或目录材料若没有明确出现用户指定的产品名称/标识，就不能作为该产品事实，也不能据此建立“别名、原名、旗下品牌、开源/社区/商业品牌、前身”等身份关系。任何这类关系必须来自用户明确说明，或来自已直接打开且同时明确出现两方名称与关系的一方页面/产品文件；仅搜索结果摘要不够。

## 自主分析与必要门禁
本阶段的目标不是完成一份资料齐全的产品尽调，而是形成“足以推导 ICP、关键词和核验方向”的研究底稿。先自主利用用户输入和当前允许的只读来源分析；不要把补问当成默认步骤。

只有经过合理调查后仍存在“决定性歧义”时才请求用户补充：
- 有两个以上同等合理的产品身份，且选择不同身份会改变目标客户；
- 核心定位存在两个以上有证据支持的解释，且会导向不同 ICP 或检索方向；
- 用户没有提供最小的产品/问题线索，现有只读来源也无法得到可用起点。

以下情况本身不得触发门禁：一个网页抓取失败、没有找到官网、网络搜索超时、价格/客户案例/商业模式/部署细节未知、个别维度证据较弱。此时应调整证据路线，或以“暂定结论 + 置信度 + 待验证问题”继续。只有决定性歧义仍未消除时，才停止并简洁列出已确认内容、候选解释、分歧如何影响 ICP，以及需要用户补充的最少信息；最后两行必须精确输出：
INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->

## 自适应工作流程
1. 先提炼用户已经给出的产品身份、能力、问题、场景和约束，再选择最匹配的证据路线；不要机械执行固定来源清单或固定调用次数。
2. 输入包含明确文件或目录时，在该范围内寻找产品标识和关键材料。大目录可先用 glob 定位少量项目标识文件，再对归属最相关的候选做浅层读取；不得扫描 .openkitty/、storage、logs、数据库、备份、可执行文件或历史步骤输出作为产品证据。
3. 输入是名称、文字或 URL 时，可用精确产品名叠加功能/场景词搜索，跟随搜索真实返回的页面；第一方页面优先，无法获得时用独立来源交叉验证。不得猜 URL，也不得因单一来源失败就停止。
4. 当一条路线连续低收益时，切换来源类型或搜索引擎；已经足以支持搜索策略时及时收敛，不为凑齐所有商业字段反复搜索。
5. 建立证据账本，记录每条结论的证据等级、来源定位、置信度和验证缺口。冲突来源并列保留，不静默选择更符合预期的一项。
6. 从产品、客户、技术、交付、商业等适用维度形成 SWOT。优势、劣势、机会和威胁都要分析；证据不足时写成有依据的假设或未知，并给出后续验证方式。
7. 建立竞品与替代方案地图，包括直接竞品、相邻工具、客户自建、人工流程和维持现状；竞品、供应商和合作伙伴不得被当成潜客。
8. 建立正向 ICP、负面 ICP 和实体分类规则：购买方、合作伙伴、竞品、供应商、生态/媒体中，只有购买方可进入后续候选池。
9. 只要产品身份和核心定位已足以推导 ICP 与关键词，就继续完成分析；只有命中决定性歧义门禁时才停止。无论哪种情况都不能跳过产品分析直接搜索潜客。

## 输出契约
- 项目/产品名称与一句话价值主张
- 产品维度表（用户任务、核心能力、使用场景、部署集成、商业模式/约束、证明材料）
- 证据账本（事实/主张/推断/未知 + 来源）
- SWOT（优势、劣势、机会、威胁）
- 竞品与替代方案地图
- 正向 ICP、负面 ICP、实体分类规则
- 待验证问题与可供下一阶段使用的行业/痛点/购买信号种子词
- 当产品身份、核心价值和购买问题已经足以生成可审计的 ICP 与搜索策略时，即使仍有明确标注的非决定性未知，也在最后一行输出：PRODUCT_PROFILE_STATUS: READY
`, Tools: []string{"read_file", "list_dir", "glob", "web_search", "web_fetch", "web_navigate", "web_page", "web_close"}, MaxIter: 10, AllowMissingInput: true,
					Routing: []StepRoute{{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}}},
				{Name: "搜索策略", Prompt: `基于已经通过宿主校验的产品画像与 SWOT，先设计可审计的多源搜索方案。本步骤无工具权限，不执行搜索。允许使用画像中的已确认事实和明确标注的分析假设；每个假设必须保留置信度与后续核验条件，不能伪装成产品事实。

## 产品画像与 SWOT
${step0.output}

## 策略要求
1. 先从正向 ICP、负面 ICP、SWOT 机会和待验证问题推导搜索假设，禁止只写“AI 公司”“数字化企业”等泛词。
2. 建立关键词矩阵，每个查询必须至少组合：
   - 行业/公司类型；
   - 痛点/业务场景；
   - 购买触发信号（招标、采购、招聘、扩产、融资、系统替换、数字化项目等）；
   - 地区、规模、时间窗口等用户约束。
3. 建立负面关键词：排除直接竞品、供应商、软件厂商名录、泛 AI 资讯、培训、旅游及其他已知噪声。
4. 为每类来源制定独立计划：
   - 第一方：公司官网、公告、招聘、采购页面；
   - 政府/招采：政府采购、公共资源交易、招标公告；
   - 权威新闻/行业媒体；
   - 招聘与企业信息平台。
5. 同一篇转载、镜像或聚合内容只算一个来源。高置信潜客至少需要两个不同来源类别交叉支持，或一个第一方强购买信号。
6. 预先定义实体分类和淘汰规则：购买方可保留；合作伙伴、竞品、供应商、生态/媒体进入排除表。
7. 预先定义评分：产品匹配、需求强度、时效性、证据可信度、约束满足度；“公司规模大”本身不是需求证据。

## 分阶段搜索预算
- 阶段 A：4-6 组发现查询，验证场景与信号词。
- 阶段 B：6-10 组垂直查询，按行业/信号/来源类别寻找购买方。
- 阶段 C：逐公司核验，打开第一方与独立来源。
- 至少保留最后 3 轮给证据去重、分类与汇总。

## 输出契约
- 搜索假设清单
- 关键词矩阵（不少于 12 条可直接执行的精确检索式，按批次和来源分组）
- 负面关键词与噪声排除规则
- 多源渠道计划
- 候选接纳/淘汰规则和评分模型
- 证据有效性判定标准
- 只有完整时，最后一行输出：SEARCH_PLAN_STATUS: READY
`, NoTools: true, MaxIter: 4},
				{Name: "多源搜索与核验", Prompt: `严格执行上一步搜索策略，发现并核验真正的购买方。禁止退回泛化的“先搜再想”。

## 产品画像与 SWOT
${step0.output}

## 已批准搜索策略
${step1.output}

## 执行规则
1. 先按关键词矩阵执行，不得在没有记录原因时临时改成泛词。确需扩词时，记录原查询、失败原因、新查询和变更理由。
2. 每个候选先完成实体分类：购买方、合作伙伴、竞品、供应商、生态/媒体。只有购买方进入潜客表，其余进入排除表。
3. 每个购买方必须回答“它为什么会买”“为什么是现在”，并以可访问来源支持；公司业务介绍或公司规模不能单独构成购买意图。
4. 高置信潜客至少需要两个不同来源类别交叉支持，或一个第一方强购买信号（正式采购/招标/招聘/项目公告）。转载、镜像和聚合内容不算独立来源。
5. web_fetch 只能传完整的 http:// 或 https:// URL，禁止传裸域名；无法打开的来源记为 fetch_failed，不得把搜索摘要伪装成已核验正文。
6. 对 URL、标题、发布日期、信号日期、来源类别、独立性、与产品匹配逻辑逐项检查。过期、无日期、来源冲突、同源转载必须显式标注。
7. 按约束账本先严格筛选；近似候选逐项标注放宽条件。不满足硬约束的对象不得进入合格潜客。
8. 不得编造联系人、预算、采购金额、价格承受力或内部需求。没有足够证据时宁可输出 0 个高置信潜客。
9. 使用 lead_save 时，只保存通过购买方分类和最低证据门槛的线索。
10. 本步骤不再扫描本地项目；产品事实只来自已校验的上游画像。

## 输出契约
- 检索执行记录：原计划查询、实际查询、来源类别、结果质量、变更理由
- 实体分类表：购买方/合作伙伴/竞品/供应商/生态媒体及判定理由
- 已核验潜客表：公司、行业、需求/购买信号、信号日期、来源 URL/标题、来源类别、独立性、匹配原因、约束状态、置信度
- 排除对象表：被排除实体、类别、排除原因
- 证据有效性审计：重复来源、失效链接、时效性、冲突、仍待核验项
- 有满足证据门槛的购买方时，最后一行输出：RESEARCH_STATUS: READY
- 仅完成搜索但没有足够证据的购买方时，也必须输出上述表格并最后一行输出：RESEARCH_STATUS: DEGRADED
`, Tools: []string{"web_search", "web_fetch", "lead_research", "lead_save", "platform_search"}, MaxIter: 20, ContinueOnError: true, TimeoutSeconds: 180},
				{Name: "报告整理", Prompt: `基于已经通过校验的“产品画像与 SWOT”“搜索策略”“多源搜索与核验”结果，整理最终潜客报告。

## 产品画像与 SWOT
${step0.output}

## 搜索策略
${step1.output}

## 多源搜索与核验
${step2.output}

## 整理原则
- 不再发起新搜索，不调用工具，不从系统摘录中捞取未通过上游分类与证据门槛的公司。
- 旧报告中的 S/A/B 评级、价格、联系人或需求判断若未被本轮证据重新验证，一律不得继承。
- 只有上游“已核验潜客表”中的购买方可以进入潜客优先级表；竞品、供应商、合作伙伴和生态公司必须保留在排除对象表。
- 不足 5 个合格潜客就如实写实际数量；不得用低质量公司凑数。
- 每项事实都保留来源 URL/标题、信号日期、来源类别和置信度；推断与事实分开。
- 完全匹配、条件待核验、逐项放宽的近似候选必须分层；硬约束不得自动放宽。
- 保持上游已经校验的目标产品身份；不得在报告阶段新增、改写或升级任何别名、母子品牌、前身、开源/商业版本对应关系。

## 最终报告格式
1. 产品结论与 SWOT：优势、劣势、机会、威胁、关键未知。
2. 本轮检索关键词与多源渠道：计划检索式、实际变更、负面关键词。
3. 已核验潜客优先级表：购买方、购买信号、证据、匹配原因、约束状态、置信度、建议切入点。
4. 排除对象表：竞品/合作伙伴/供应商/生态媒体及排除理由。
5. 证据有效性审计：来源独立性、时效性、失效/冲突/待核验项。
6. 下一轮检索计划：只列本轮证据缺口对应的关键词和来源。
7. 统一署名“AI员工：潜客研究员”，不要出现 pipe_*、lead-research 等内部名称。`, NoTools: true, MaxIter: 5},
			}},
	}
	// Keep the historical definitions available for replay, but make the
	// user-facing advisor use the restored coherent workflow below. The later
	// AI Hedge/投研经理/金信 methods are embedded in its analysis prompt instead
	// of becoming failure-prone standalone employees or eight mandatory calls.
	pipelines["investment-advisory"] = fusedInvestmentAdvisoryPipeline()
	return pipelines
}

func fusedInvestmentAdvisoryPipeline() Pipeline {
	return investmentadvisory.New()
}
