package investmentadvisory

import pipelinecore "mac/internal/pipeline"

func New() pipelinecore.Pipeline {
	return pipelinecore.Pipeline{
		Name: "investment-advisory",
		Desc: "投资顾问：意图分层→按类型取证→自适应研判→聊天或按需报告",
		Steps: []pipelinecore.PipelineStep{
			{
				Name: "自主判断与证据采集",
				Prompt: `你负责投资顾问的按意图取证阶段，不输出最终回答，不写报告文件。

先读取上游任务简报中的 INVESTMENT_INTENT、DECISION_LEVEL、ANSWER_DEPTH 和 PERSONALIZATION_REQUIRED。若字段缺失，再根据用户原始请求自主判断。取证必须随任务类型变化：
- market_brief：获取主要指数、成交额/量能、涨跌家数、板块强弱、资金与风险偏好、当日主要事件；不要求持仓、成本或证券代码。使用 stock_facts 核对宽基指数时必须带交易所前缀，并且只调用行情商：上证综指=sh000001、科创50=sh000688、深证成指=sz399001、创业板指=sz399006；不得用裸 000001/000688，也不得为指数调用 official_report。先单独完成四个指数的腾讯行情批次并读取共同的 Evidence as_of/quote.trade_date，再用这个已确认的交易日查询新闻、板块与资金，不得在行情工具返回前猜一个日期去搜索。第二批可并行用东方财富核对四个相同指数，并补最多两个真正缺失的网页维度。市场广度、板块和资金等工具未覆盖字段再用网页来源补充。
- instrument_analysis/general_strategy/personalized_position：标准化证券名称和代码，优先 stock_facts；同一 as_of 并行核对腾讯、东方财富和官方报告，再补真正缺失的催化、资金、舆情和标的特有风险。
- sector_analysis：获取行业指数或代表性标的、成分强弱、估值/景气线索、资金、政策与产业事件；不得把行业问题缩成一只股票。
- fund_etf_analysis：优先核对产品代码、净值/价格、跟踪标的、费率、规模、流动性和主要持仓；不得套用个股财报框架。
- event_explanation：先确认事件原文、发生时间、发布时间和直接影响链，再补市场反应；不机械抓取全套估值。
- education：只核验定义和必要示例，不做无关行情搜索。

明确 A 股代码的个股任务使用 stock_facts；其他任务直接使用 web_search/web_fetch/platform_search 取得与任务匹配的证据。周末或节假日必须说明最新可获得交易日。调用 stock_facts 时，lookback_days 省略或使用 80，不得传 5、20 等低于 61 的值。

日历事实必须确定性处理：系统上下文给出的当前日期与星期是权威；任何完整日期的星期都按公历计算，不得凭印象填写。周六、周日不是 A 股交易日。stock_facts 的 as_of 是证据截止日，工具返回的 Evidence as_of/quote.trade_date 才是最新可获得交易日；当截止日是周末而返回前一个交易日时，这是正常回溯，不能标成 fetch_failed、数据未发布或“周末盘面缺失”。未核验交易所日历时，不自行编造“下一交易日”的具体日期。

每类缺口最多两个有效来源，达到足够覆盖后停止。严格区分 verified_fact、transparent_inference、unverified、unknown、source_conflict、fetch_failed，并记录来源 URL、数据截至时间和市场状态。`,
				Tools:           []string{"stock_facts", "web_search", "web_fetch", "platform_search"},
				MaxIter:         5,
				ContinueOnError: true,
				// This stage may need several model/tool turns. A two-minute
				// step deadline used to cancel a healthy five-minute model call
				// before its own bounded timeout could take effect.
				TimeoutSeconds:   600,
				OutputTokenLimit: 2600,
			},
			{
				Name: "分类型研究与研判",
				Prompt: `你是投资顾问的研究判断阶段，只使用上游已经取得的证据，不调用工具、不重复搜索。先去重、核对截至时间，并区分事实、推断、冲突和未知；然后只分析当前意图真正需要的维度：

- market_brief：结论先行；指数与量能；市场广度；板块结构；资金和风险偏好；主要驱动；下一交易日观察条件。
- instrument_analysis：标的结论；基本面与估值；价格结构；催化；反证与风险；短中期条件场景。
- sector_analysis：行业强弱与内部结构；景气/估值；资金；政策和产业催化；代表性公司仅作验证，不替代行业结论。
- fund_etf_analysis：跟踪对象；净值/价格和流动性；费率/规模/持仓；跟踪偏差；适用场景和风险。
- event_explanation：事件事实；影响链；受益/受损方向；市场是否已经反映；验证窗口。
- general_strategy：给一般性的触发条件、观察位和失效条件，不因缺少个人持仓而停下，也不计算个性化仓位。
- personalized_position/portfolio_review：仅在上游确认个人参数足够时，结合成本、仓位、期限和风险约束形成个性化方案。
- education：用清晰定义、例子和常见误区回答，不扩写成市场报告。

ANSWER_DEPTH=quick 时只保留最重要结论；standard 给足证据和条件；deep 才形成完整研究底稿。没有相应数据的维度直接省略或简洁说明，不用固定六章节凑完整。所有完整日期的星期必须按公历核对；周末截止日回溯到前一交易日是正常结果，不得把周六、周日写成待补的交易时段。四个宽基指数同涨只能称“主要指数同涨”，没有涨跌家数时不得升级成“全市场普涨”；只有同一指数在两个行情商均成功时，才称该指数已交叉核验。只有证据中存在前一交易日或可比期成交额时，才能称“放量、缩量、回升或处于高位”，否则只陈述当日成交额。`,
				NoTools:          true,
				MaxIter:          1,
				ContinueOnError:  true,
				TimeoutSeconds:   300,
				OutputTokenLimit: 3600,
				OutputContract:   &pipelinecore.PipelineOutputContract{MinChars: 240, OnViolation: "degraded"},
			},
			{
				Name: "投研合成与质量门",
				Prompt: `你负责把上游研究底稿变成自然、直接、用户可立即阅读的投资顾问回答，不调用工具。

先回答用户真正问的问题，不播报 Pipeline、槽位、任务状态、上游、下游、内部简报、position_state、coverage_gap、verified_fact 等内部字段。结构和长度服从 ANSWER_DEPTH：
- quick：约 3-6 个要点，先给一句结论，再给最关键证据和下一观察点。
- standard：结论先行，随后给证据、反证、风险和条件场景。
- deep：完整但不重复的研究正文，可包含数据口径和关键来源。

information/analysis 不询问个人持仓。general_strategy 使用“如果…那么…”的条件式方案，不给精确仓位。personalized_action 只有在参数已充分时才给与成本、仓位、期限和风险相匹配的方案。execution 请求明确说明不能执行真实下单，但可以给模拟计划。

只保留与当前任务相关的标题；市场简报不写个股基本面，基金分析不套个股财报，知识解释不写风险矩阵。数据缺失只说明其影响，不把缺失包装成失败报告。所有日期—星期组合必须按公历一致；不得把周末说成交易日、昨日盘面缺失或数据尚未发布。下一交易日的具体日期未经交易所日历证据核验时直接省略。沿用研究阶段的证据措辞边界：指数同涨不等于全市场普涨，单日成交额不能证明放量，单一行情商不能声称交叉核验。`,
				NoTools:          true,
				MaxIter:          1,
				ContinueOnError:  true,
				TimeoutSeconds:   300,
				OutputTokenLimit: 4200,
			},
			{
				Name:    "结果交付",
				Prompt:  `默认直接交付上一步的自然语言回答，不强制生成文件。只有 ANSWER_DEPTH=deep，或用户明确要求保存、写入、Markdown、报告路径时，才使用 write_file 保存完整报告。用户明确要求只在聊天回答或不要文件时，无论深度如何都直接返回正文，不调用文件工具。不得只给文件路径而省略回答正文。`,
				Tools:   []string{"write_file"},
				MaxIter: 2,
			},
		},
	}
}
