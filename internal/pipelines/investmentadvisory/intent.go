package investmentadvisory

import (
	"regexp"
	"strings"
)

const (
	InvestmentIntentMarketBrief        = "market_brief"
	InvestmentIntentInstrumentAnalysis = "instrument_analysis"
	InvestmentIntentSectorAnalysis     = "sector_analysis"
	InvestmentIntentFundETFAnalysis    = "fund_etf_analysis"
	InvestmentIntentEventExplanation   = "event_explanation"
	InvestmentIntentGeneralStrategy    = "general_strategy"
	InvestmentIntentPersonalPosition   = "personalized_position"
	InvestmentIntentPortfolioReview    = "portfolio_review"
	InvestmentIntentEducation          = "education"

	InvestmentDecisionInformation        = "information"
	InvestmentDecisionAnalysis           = "analysis"
	InvestmentDecisionGeneralStrategy    = "general_strategy"
	InvestmentDecisionPersonalizedAction = "personalized_action"
	InvestmentDecisionExecution          = "execution"
)

type InvestmentRequestProfile struct {
	Intent                  string
	DecisionLevel           string
	AnswerDepth             string
	PersonalizationRequired bool
	MissingDecisiveFields   []string
}

var (
	investmentCostBasisPattern     = regexp.MustCompile(`(?i)(?:成本价?|买入价|持仓均价|均价)[^。；;\n%]{0,16}\d`)
	investmentPositionRatioPattern = regexp.MustCompile(`(?i)(?:仓位|持仓占比|占总仓|占比)[^。；;\n]{0,16}(?:\d+(?:\.\d+)?\s*%|\d+\s*成)`)
	investmentCapitalPattern       = regexp.MustCompile(`(?i)(?:账户规模|本金|可用资金|计划投入|投入资金|资金规模)[^。；;\n]{0,16}\d`)
)

func ClassifyInvestmentRequest(input string) InvestmentRequestProfile {
	text := strings.ToLower(strings.TrimSpace(input))
	profile := InvestmentRequestProfile{
		Intent:        InvestmentIntentInstrumentAnalysis,
		DecisionLevel: InvestmentDecisionAnalysis,
		AnswerDepth:   "standard",
	}

	hasHolding := containsAnyText(text, []string{
		"我持有", "我有", "已持有", "已经持有", "已买入", "买了", "被套", "套牢",
		"我的持仓", "持仓成本", "成本价", "持仓均价", "仓位",
	})
	hasAction := containsAnyText(text, []string{
		"怎么操作", "如何操作", "该怎么做", "应该怎么办", "怎么办",
		"加仓", "补仓", "减仓", "清仓", "卖出", "止损", "止盈", "持有还是",
		"能买吗", "能不能买", "该不该买", "买入", "卖不卖", "要不要卖",
	})
	hasExecution := containsAnyText(text, []string{"帮我下单", "替我下单", "直接下单", "执行买入", "执行卖出", "真实交易"})
	hasPortfolio := containsAnyText(text, []string{"持仓组合", "投资组合", "组合诊断", "组合分析", "资产配置", "调仓", "多只持仓"})

	switch {
	case hasPortfolio:
		profile.Intent = InvestmentIntentPortfolioReview
	case containsAnyText(text, []string{"基金", "etf", "指数基金", "债券", "可转债", "债基"}):
		profile.Intent = InvestmentIntentFundETFAnalysis
	case containsAnyText(text, []string{"板块", "行业", "赛道", "主题机会", "产业链"}):
		profile.Intent = InvestmentIntentSectorAnalysis
	case containsAnyText(text, []string{"财报", "年报", "季报", "公告", "新闻", "消息", "事件影响", "政策影响", "降息", "加息"}):
		profile.Intent = InvestmentIntentEventExplanation
	case containsAnyText(text, []string{"今天市场", "今日市场", "市场行情", "大盘", "股市行情", "市场复盘", "盘面", "指数表现", "涨跌家数", "成交额"}):
		profile.Intent = InvestmentIntentMarketBrief
	case containsAnyText(text, []string{"是什么", "什么意思", "怎么理解", "如何理解", "区别是什么", "投资知识"}):
		profile.Intent = InvestmentIntentEducation
	case hasHolding && hasAction:
		profile.Intent = InvestmentIntentPersonalPosition
	case hasAction:
		profile.Intent = InvestmentIntentGeneralStrategy
	}

	switch {
	case hasExecution:
		profile.DecisionLevel = InvestmentDecisionExecution
	case hasPortfolio || (hasHolding && hasAction):
		profile.DecisionLevel = InvestmentDecisionPersonalizedAction
		profile.PersonalizationRequired = true
	case hasAction:
		profile.DecisionLevel = InvestmentDecisionGeneralStrategy
	case profile.Intent == InvestmentIntentMarketBrief || profile.Intent == InvestmentIntentEducation:
		profile.DecisionLevel = InvestmentDecisionInformation
	default:
		profile.DecisionLevel = InvestmentDecisionAnalysis
	}

	switch {
	case containsAnyText(text, []string{"深度报告", "完整报告", "全面分析", "详细分析", "深度分析", "保存报告", "生成报告", "写入文件"}):
		profile.AnswerDepth = "deep"
	case containsAnyText(text, []string{"简单说", "简要", "快速", "一句话", "概览", "简评"}):
		profile.AnswerDepth = "quick"
	case profile.Intent == InvestmentIntentMarketBrief || profile.Intent == InvestmentIntentEducation:
		profile.AnswerDepth = "quick"
	}

	if profile.PersonalizationRequired {
		if !investmentCostBasisPattern.MatchString(text) {
			profile.MissingDecisiveFields = append(profile.MissingDecisiveFields, "持仓成本")
		}
		if !investmentPositionRatioPattern.MatchString(text) {
			profile.MissingDecisiveFields = append(profile.MissingDecisiveFields, "当前仓位比例")
		}
		if !containsAnyText(text, []string{"短线", "波段", "中线", "中长期", "长期", "持有周期", "计划持有", "交易周期"}) {
			profile.MissingDecisiveFields = append(profile.MissingDecisiveFields, "计划持有周期")
		}
		if !containsAnyText(text, []string{"风险偏好", "最大回撤", "最大亏损", "可承受亏损", "止损", "保守", "稳健", "激进", "高风险", "低风险"}) {
			profile.MissingDecisiveFields = append(profile.MissingDecisiveFields, "可承受风险或最大亏损")
		}
		if containsAnyText(text, []string{"买多少", "卖多少", "多少仓位", "几成仓", "具体金额", "多少股"}) &&
			!investmentCapitalPattern.MatchString(text) {
			profile.MissingDecisiveFields = append(profile.MissingDecisiveFields, "可用于该策略的资金或账户规模")
		}
	}
	return profile
}

func InvestmentIntentUsesStockFacts(intent string) bool {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case InvestmentIntentInstrumentAnalysis, InvestmentIntentGeneralStrategy, InvestmentIntentPersonalPosition:
		return true
	default:
		return false
	}
}

func containsAnyText(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
