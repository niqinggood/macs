package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type investmentSearchResolverTool struct {
	result string
	err    error
	params map[string]interface{}
}

func (t *investmentSearchResolverTool) Name() string                       { return "web_search" }
func (t *investmentSearchResolverTool) Description() string                { return "test resolver" }
func (t *investmentSearchResolverTool) Parameters() map[string]interface{} { return nil }
func (t *investmentSearchResolverTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	t.params = params
	return t.result, t.err
}

func TestExplicitAShareCodesAreUniqueOrderedAndBounded(t *testing.T) {
	got := explicitAShareCodes("比较 600519、300750、600036，再看一次600519，并忽略 998899 和 00700")
	want := []string{"600519", "300750", "600036"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	got = explicitAShareCodes("600001 600002 600003 600004 600005")
	if len(got) != 4 {
		t.Fatalf("bounded codes = %v, want 4", got)
	}
}

func TestResolveAShareCodeFromCompanyNameSearchAndValidateName(t *testing.T) {
	tool := &investmentSearchResolverTool{result: "新安股份 (600596) 的行情与公司公告"}
	codes, err := resolveAShareCodesFromSearch(context.Background(), tool, "统一投资顾问规则\n---\n分析下新安股份后市情况")
	if err != nil || !reflect.DeepEqual(codes, []string{"600596"}) {
		t.Fatalf("resolved codes=%v err=%v", codes, err)
	}
	if got := tool.params["query"]; got != "分析下新安股份后市情况 A股 证券代码" {
		t.Fatalf("resolution query=%q", got)
	}
	if !investmentInputMentionsCompany("分析下新安股份后市情况", "新安股份") {
		t.Fatal("resolved structured company name should validate against the user request")
	}
	if investmentInputMentionsCompany("分析下新安股份后市情况", "贵州茅台") {
		t.Fatal("unrelated structured company name must not validate")
	}
}

func TestHostInvestmentFastPathOnlyActivatesForFirstStandardStep(t *testing.T) {
	executor := &PipelineExecutor{}
	output, ready, err := executor.hostCollectInvestmentEvidence(nil, &PipelineExecution{PipelineName: "other"}, &PipelineStep{Name: "自主判断与证据采集"}, 0, "600519")
	if err != nil || ready || output != "" {
		t.Fatalf("unrelated pipeline activated host investment path: output=%q ready=%v err=%v", output, ready, err)
	}
}

func TestHostInvestmentFastPathFollowsStepNameAfterIntakeSlot(t *testing.T) {
	fixtures := stockFallbackFixtures(t)
	fullRegistry := NewToolRegistry()
	fullRegistry.Register(structuredFallbackStockTool{outputs: map[string]string{
		stockFactsProviderTencentMarket:  fixtures[0],
		stockFactsProviderEastmoney:      fixtures[1],
		stockFactsProviderOfficialReport: fixtures[2],
	}})
	executor := &PipelineExecutor{agentFactory: &AgentFactory{agents: map[string]Agent{
		"default": &LLMAgent{toolRegistry: fullRegistry},
	}}}
	execution := &PipelineExecution{PipelineName: "investment-advisory", Steps: []StepResult{
		{Name: "任务受理与意图理解", Status: "completed"},
		{Name: "自主判断与证据采集", Status: "running"},
	}}

	output, ready, err := executor.hostCollectInvestmentEvidence(context.Background(), execution, &PipelineStep{Name: "自主判断与证据采集"}, 1, "分析 600519")
	if err != nil || !ready || !strings.Contains(output, "HOST_STRUCTURED_A_SHARE_EVIDENCE: READY") {
		t.Fatalf("shifted investment evidence step was not activated: ready=%v err=%v output=%s", ready, err, output)
	}
}

func TestSplitHostFallbackDecisionRemovesHiddenSection(t *testing.T) {
	evidence, decision := splitHostFallbackDecision("facts\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n## 排序\n风险矩阵\n<!--HOST_FALLBACK_DECISION_END-->\nmore facts")
	if decision != "## 排序\n风险矩阵" {
		t.Fatalf("decision = %q", decision)
	}
	if evidence != "facts\n\nmore facts" {
		t.Fatalf("evidence = %q", evidence)
	}
}

func TestInvestmentQualityGateUsesDeterministicHostFallback(t *testing.T) {
	fallback := buildDeterministicInvestmentFallback(map[string]map[string]stockFactsResult{}, nil, "not_provided")
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		Steps: []StepResult{{
			Name:   "自主判断与证据采集",
			Output: "HOST_STRUCTURED_A_SHARE_EVIDENCE: READY\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n" + fallback + "\n<!--HOST_FALLBACK_DECISION_END-->",
		}},
	}
	got, ready := (&PipelineExecutor{}).hostFallbackInvestmentQualityGate(exec, &PipelineStep{Name: "投研合成与质量门"}, "分析600596，未提供持仓")
	if !ready {
		t.Fatal("quality gate fallback was not activated")
	}
	for _, want := range []string{"交付状态: no_decision", "position_state: not_provided", "已启用宿主旧版兜底", "## 综合结论"} {
		if !strings.Contains(got, want) {
			t.Fatalf("quality gate fallback missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "no usable final answer") {
		t.Fatalf("internal model diagnostic leaked into host fallback: %s", got)
	}
}

func TestInvestmentPositionStateNeverInventsHoldings(t *testing.T) {
	for _, input := range []string{
		"分析 600596 新安股份",
		"我未提供持仓、成本和账户规模，只做研究",
		"持仓信息未提供，请不要假设",
	} {
		if got := investmentPositionState(input); got != "not_provided" {
			t.Fatalf("investmentPositionState(%q) = %q, want not_provided", input, got)
		}
	}
	for _, input := range []string{"我目前空仓，分析600596", "我的持仓成本价是12元", "账户规模20万，如何控制风险"} {
		if got := investmentPositionState(input); got != "provided" {
			t.Fatalf("investmentPositionState(%q) = %q, want provided", input, got)
		}
	}
}

func TestNormalizeInvestmentPositionStateOutputUsesCanonicalHostState(t *testing.T) {
	input := "## 证据覆盖与质量门\n\n交付状态: degraded\n\nposition_state: none（用户未提供）\n\n## 综合结论\n观察"
	output := normalizeInvestmentPositionStateOutput(input, "not_provided")
	if !strings.Contains(output, "position_state: not_provided") || strings.Contains(output, "position_state: none") {
		t.Fatalf("position state was not normalized: %s", output)
	}
	withoutField := normalizeInvestmentPositionStateOutput("交付状态: degraded\n\n## 综合结论\n观察", "provided")
	if !strings.Contains(withoutField, "交付状态: degraded\n\nposition_state: provided") {
		t.Fatalf("canonical position state was not inserted after delivery status: %s", withoutField)
	}
}

func TestInvestmentDeliveryExplainsInternalDegradedStatusInPlainChinese(t *testing.T) {
	decision := "## 证据覆盖与质量门\n\n**交付状态: degraded**\n\n- **coverage_gap**: 经营性现金流未知，无法验证利润的现金实现质量。\n- **coverage_gap**: 社区舆情和分析师评级未采集。\n\n## 综合结论\n\n基于现有可靠资料谨慎观察。"
	delivery := investmentDecisionForDelivery(decision) + "\n\n" + investmentPlainLanguageDataNotice(decision)
	for _, forbidden := range []string{"degraded", "coverage_gap", "unknown", "****"} {
		if strings.Contains(delivery, forbidden) {
			t.Fatalf("user-facing delivery leaked %q:\n%s", forbidden, delivery)
		}
	}
	for _, want := range []string{"## 结论先行", "## 数据边界", "本次分析已形成", "经营性现金流暂未取得", "社区舆情和分析师评级暂未取得", "相关判断采用谨慎口径"} {
		if !strings.Contains(delivery, want) {
			t.Fatalf("plain-language delivery missing %q:\n%s", want, delivery)
		}
	}
}

func TestInvestmentDeliveryFallbackNoticeNamesConcreteMissingData(t *testing.T) {
	notice := investmentPlainLanguageDataNotice("## 证据覆盖与质量门\n\n部分资料尚未形成完整结果")
	for _, want := range []string{"经营现金流", "资金面", "近期公告正文", "相关判断采用谨慎口径"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("fallback notice missing %q: %s", want, notice)
		}
	}
}

func TestInvestmentMarketBriefFallbackNoticeUsesMarketFields(t *testing.T) {
	notice := investmentPlainLanguageDataNoticeForIntent(
		"## 证据覆盖与质量门\n\n部分资料尚未形成完整结果",
		investmentIntentMarketBrief,
	)
	for _, want := range []string{"指数", "成交", "市场广度", "板块", "资金", "不据缺失项推断涨跌原因"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("market notice missing %q: %s", want, notice)
		}
	}
	for _, forbidden := range []string{"经营现金流", "近期公告正文", "持仓", "风险偏好"} {
		if strings.Contains(notice, forbidden) {
			t.Fatalf("market notice retained unrelated field %q: %s", forbidden, notice)
		}
	}
}

func TestDeterministicInvestmentFallbackUsesUnifiedAdvisorQualitySections(t *testing.T) {
	output := buildDeterministicInvestmentFallback(map[string]map[string]stockFactsResult{}, nil, "not_provided")
	for _, want := range []string{
		"## 证据覆盖与质量门", "交付状态: no_decision", "position_state: not_provided",
		"## 基本面与估值", "## 金融信息与情绪", "## 短线交易情报",
		"## 风险预算与失效条件", "## 综合结论", "未执行任何订单",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("fallback missing %q:\n%s", want, output)
		}
	}
}

func TestDeterministicInvestmentFallbackProvidesUsefulConditionalOutlook(t *testing.T) {
	closePrice := 12.8
	revenue := 4082504670.84
	revenueYoY := 12.69
	profit := 96280648.02
	profitYoY := 190.61
	grouped := map[string]map[string]stockFactsResult{
		"600596": {
			stockFactsProviderTencentMarket: {
				CompanyName: "新安股份",
				Quote:       &stockQuoteFacts{TradeDate: "2026-07-14", Close: &closePrice},
				History: &stockHistoryFacts{
					Return5DPct: 3.2, Return20DPct: 8.4, Return60DPct: 15.1,
					MA5: 12.4, MA20: 11.8, MA60: 10.9,
					Range20High: 13.2, Range60Low: 9.7,
				},
				Valuation: &stockValuationFacts{},
			},
			stockFactsProviderEastmoney: {Valuation: &stockValuationFacts{}},
			stockFactsProviderOfficialReport: {Report: &stockReportFacts{
				Title: "2026年第一季度报告", Period: "2026Q1", AnnouncementDate: "2026-04-29",
				Metrics: map[string]stockReportMetric{
					"revenue":           {Current: &revenue, YoYPct: &revenueYoY, Unit: "CNY", Status: "observed"},
					"parent_net_profit": {Current: &profit, YoYPct: &profitYoY, Unit: "CNY", Status: "observed"},
				},
			}},
		},
	}
	output := buildDeterministicInvestmentFallback(grouped, []string{"600596"}, "not_provided")
	for _, want := range []string{
		"交付状态: degraded", "营业收入：4082504670.84 CNY；同比 12.69%", "归母净利润：96280648.02 CNY；同比 190.61%",
		"当前判定为**中期结构偏强**", "后市处于偏强观察区", "20日区间高点 13.20", "60日区间低点 9.70",
		"盈利驱动与验证链条", "三种条件情景", "标的风险矩阵", "最强支持证据", "最强反证",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("conditional outlook missing %q:\n%s", want, output)
		}
	}
}

func TestHostInvestmentReportFindsFiveUnifiedStagesByName(t *testing.T) {
	fallback := "## 证据覆盖与质量门\n\n- 交付状态: no_decision\n\n## 综合结论\n\n证据不足。"
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		Steps: []StepResult{
			{Name: "自主判断与证据采集", Status: "completed", Output: "HOST_STRUCTURED_A_SHARE_EVIDENCE: READY\nFACTS\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n" + fallback + "\n<!--HOST_FALLBACK_DECISION_END-->"},
			{Name: "金融信息与短线情报", Status: "completed", Output: "## 金融信息事件账本\n\nclaim_id=C1\n\n## 短线交易情报\n\nunknown\n\n## 覆盖缺口与冲突\n\n资金面 unknown"},
			{Name: "并行投研分析", Status: "completed", Output: "## 基本面与估值\n\n多因子审查\n\n## 金融信息与情绪\n\nC1\n\n## 短线交易情报\n\nconditional\n\n## 标的特有风险\n\nR1"},
			{Name: "投研合成与质量门", Status: "completed", Output: "## 证据覆盖与质量门\n\n交付状态: completed\n\n## 基本面与估值\n\npartial\n\n## 金融信息与情绪\n\nC1\n\n## 短线交易情报\n\nconditional\n\n## 风险预算与失效条件\n\nunknown\n\n## 综合结论\n\n观察"},
			{Name: "报告交付", Status: "running"},
		},
	}
	step := &PipelineStep{Name: "报告交付", ArtifactContract: &PipelineArtifactContract{MaterializeOutput: true}}
	report, ready, err := (&PipelineExecutor{}).hostDeliverInvestmentReport(nil, exec, step, 4, "分析600596")
	if err != nil || !ready {
		t.Fatalf("host report ready=%v err=%v", ready, err)
	}
	for _, want := range []string{"## 结论先行", "## 走势与关键位置", "未执行任何真实交易"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{"HOST_FALLBACK_DECISION", "claim_id=", "阶段审查", "position_state", "交付状态:"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("internal pipeline content leaked into report %q: %s", forbidden, report)
		}
	}
}

func TestHostInvestmentReportUsesRestoredSingleResearchStageWithoutDuplication(t *testing.T) {
	fallback := buildDeterministicInvestmentFallback(map[string]map[string]stockFactsResult{}, nil, "not_provided")
	evidence := "HOST_STRUCTURED_A_SHARE_EVIDENCE: READY\n## 600596 新安股份\n- 行情事实：trade_date=2026-07-14；close=11.53 CNY。\n- 行情直接来源：https://quote.example/600596\n- 最新官方报告：2026年第一季度报告；source=https://report.example/q1.pdf\n- 近期官方公告：\n  - 2026-07-10｜业绩预增公告｜https://report.example/forecast.pdf\n\n不应重复的原始边界说明\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n" + fallback + "\n<!--HOST_FALLBACK_DECISION_END-->"
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		Steps: []StepResult{
			{Name: "自主判断与证据采集", Status: "completed", Output: evidence},
			{Name: "旧版深度投研与融合分析", Status: "completed", Output: "## 证据合并与质量门禁\nverified\n## 基本面与估值分析\n盈利改善\n## 短线态势分析\n偏弱\n## 消息舆情与催化分析\n业绩预增\n## 标的风险矩阵\n趋势风险\n## 综合研判\n谨慎观察"},
			{Name: "投研合成与质量门", Status: "completed", Output: "## 证据覆盖与质量门\n交付状态: degraded\nposition_state: not_provided\n## 基本面与估值\n盈利改善\n## 金融信息与情绪\n业绩预增\n## 短线交易情报\n偏弱\n## 风险预算与失效条件\n趋势风险\n## 综合结论\n谨慎观察"},
			{Name: "报告交付", Status: "running"},
		},
	}
	step := &PipelineStep{Name: "报告交付", ArtifactContract: &PipelineArtifactContract{MaterializeOutput: true}}
	report, ready, err := (&PipelineExecutor{}).hostDeliverInvestmentReport(nil, exec, step, 3, "请分析新安股份")
	if err != nil || !ready {
		t.Fatalf("restored host report ready=%v err=%v", ready, err)
	}
	for _, want := range []string{"盈利改善", "## 关键来源与数据口径", "https://quote.example/600596", "https://report.example/forecast.pdf"} {
		if !strings.Contains(report, want) {
			t.Fatalf("restored report missing %q:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{"## 旧版深度投研补充", "## 并行投研分析阶段审查", "## 金融信息与短线情报阶段审查", "不应重复的原始边界说明"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("restored report retained obsolete/duplicate content %q:\n%s", forbidden, report)
		}
	}
	if strings.Contains(report, "position_state") || strings.Contains(report, "交付状态:") {
		t.Fatalf("internal audit fields leaked into delivery:\n%s", report)
	}
}

func TestHostInvestmentReportReplacesFailurePlaceholdersWithUnifiedFallback(t *testing.T) {
	fallback := buildDeterministicInvestmentFallback(map[string]map[string]stockFactsResult{}, nil, "not_provided")
	failurePlaceholder := "## 阶段降级\n步骤：投研合成与质量门\n原因：task failed: no usable final answer\n\n## 后续步骤使用规则\n- 本阶段未形成完整结果，后续步骤只能使用已存在的上游材料和可验证事实。"
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		Steps: []StepResult{
			{Name: "自主判断与证据采集", Status: "completed", Output: "HOST_STRUCTURED_A_SHARE_EVIDENCE: READY\nFACTS\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n" + fallback + "\n<!--HOST_FALLBACK_DECISION_END-->"},
			{Name: "金融信息与短线情报", Status: "degraded", Error: "task failed: no usable final answer", Output: failurePlaceholder},
			{Name: "并行投研分析", Status: "degraded", Error: "task failed: no usable final answer", Output: failurePlaceholder},
			{Name: "投研合成与质量门", Status: "degraded", Error: "task failed: no usable final answer", Output: failurePlaceholder},
			{Name: "报告交付", Status: "running"},
		},
	}
	step := &PipelineStep{Name: "报告交付", ArtifactContract: &PipelineArtifactContract{MaterializeOutput: true}}
	report, ready, err := (&PipelineExecutor{}).hostDeliverInvestmentReport(nil, exec, step, 4, "分析600596，未提供持仓")
	if err != nil || !ready {
		t.Fatalf("host report ready=%v err=%v", ready, err)
	}
	for _, want := range []string{
		"## 结论先行", "## 走势与关键位置",
		"## 主要风险与失效条件", "## 数据边界", "风险提示",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("fallback report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "## 阶段降级") {
		t.Fatalf("generic failure placeholder leaked into final report: %s", report)
	}
}

func TestHostInvestmentReportGuaranteesDegradedDeliveryWhenNewEnhancementsFail(t *testing.T) {
	failurePlaceholder := "## 阶段降级\n步骤：可选增强\n原因：upstream unavailable\n\n## 后续步骤使用规则\n- 本阶段未形成完整结果，后续步骤只能使用已存在的上游材料和可验证事实。"
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		Steps: []StepResult{
			{Name: "自主判断与证据采集", Status: "degraded", Error: "all structured providers failed", Output: failurePlaceholder},
			{Name: "金融信息与短线情报", Status: "degraded", Error: "optional branches failed", Output: failurePlaceholder},
			{Name: "并行投研分析", Status: "degraded", Error: "optional branches failed", Output: failurePlaceholder},
			{Name: "投研合成与质量门", Status: "degraded", Error: "model unavailable", Output: failurePlaceholder},
			{Name: "报告交付", Status: "running"},
		},
	}
	step := &PipelineStep{Name: "报告交付", ArtifactContract: &PipelineArtifactContract{MaterializeOutput: true}}
	report, ready, err := (&PipelineExecutor{}).hostDeliverInvestmentReport(nil, exec, step, 4, "分析600596，未提供持仓")
	if err != nil || !ready {
		t.Fatalf("guaranteed degraded report ready=%v err=%v", ready, err)
	}
	for _, want := range []string{
		"## 当前无法核验的关键问题", "## 下一步需要什么",
		"尚未取得可交叉验证的最新行情", "证券代码已识别为 600596", "无需重复提供", "未执行任何真实交易",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("guaranteed report missing %q:\n%s", want, report)
		}
	}
	if len([]byte(report)) < 500 {
		t.Fatalf("guaranteed chat delivery too small: %d bytes", len([]byte(report)))
	}
	for _, forbidden := range []string{"整体状态：degraded", "交付状态:", "position_state", "AI Hedge", "金信", "DATA_STATUS", "阶段审查"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("internal audit content leaked into guaranteed delivery %q:\n%s", forbidden, report)
		}
	}
}

func TestHostInvestmentReportNeverLeaksInternalCharacterCountGate(t *testing.T) {
	internalGate := `无法访问或披露当前用户工作目录之外的文件、目录或宿主机信息。

---

## OpenKitty 输出质量门（DEGRADED）

- 状态：degraded
- 缺口：正文字符数 54，低于契约要求 120。
- 下游规则：保留现有内容，但必须披露上述缺口，不得将其视为完整验收结果。`
	cleanedGate, foundGate := stripInvestmentInternalQualityGate(internalGate)
	if !foundGate || !unusableInvestmentDecision(cleanedGate) {
		t.Fatalf("internal gate preprocessing failed: found=%v cleaned=%q", foundGate, cleanedGate)
	}
	exec := &PipelineExecution{
		PipelineName: "investment-advisory",
		RuntimePipeline: &Pipeline{
			AIEmployeeOriginalTask: "分析一下当前 A 股行情和大盘涨跌原因。只在聊天中回答，不要保存、生成或导出任何文件。",
		},
		Steps: []StepResult{
			{Name: aiEmployeeIntakeStepName, Status: "completed", Output: "INVESTMENT_INTENT: market_brief\nANSWER_DEPTH: deep"},
			{Name: "自主判断与证据采集", Status: "degraded", Error: "tenant evidence gate", Output: tenantFilesystemEvidenceFailure},
			{Name: "分类型研究与研判", Status: "degraded", Error: "upstream unavailable", Output: tenantFilesystemEvidenceFailure},
			{Name: "投研合成与质量门", Status: "degraded", Error: "output contract", Output: internalGate},
			{Name: "结果交付", Status: "running"},
		},
	}
	report, ready, err := (&PipelineExecutor{}).hostDeliverInvestmentReport(
		nil,
		exec,
		&PipelineStep{Name: "结果交付"},
		4,
		"generated delivery prompt asks for a Markdown report",
	)
	if err != nil || !ready {
		t.Fatalf("host report ready=%v err=%v", ready, err)
	}
	for _, forbidden := range []string{"OpenKitty 输出质量门", "正文字符数", "低于契约要求", "当前用户工作目录之外"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("internal gate leaked %q:\n%s", forbidden, report)
		}
	}
	if !strings.Contains(report, "部分指数、成交、市场广度、板块或资金字段") || !strings.Contains(report, "风险提示") {
		t.Fatalf("bounded user-facing fallback missing:\n%s", report)
	}
	if strings.Contains(report, "## 数据缺口") || strings.Contains(report, "经营现金流") {
		t.Fatalf("market brief retained a duplicated or unrelated data-gap section:\n%s", report)
	}
}

func TestInvestmentParallelFailuresAreSummarizedWithoutProviderInternals(t *testing.T) {
	merged := "## 并行步骤结果\n\n### 支路一\nDATA_STATUS: fetch_failed\n原因：API返回错误状态码: 422 request_id=secret\n\n### 支路二\nDATA_STATUS: fetch_failed\n原因：parallel step wall deadline exceeded after 1m0s"
	if !investmentParallelStageAllFailed(merged) {
		t.Fatal("all-failed parallel output was not recognized")
	}
	cleaned := sanitizeInvestmentParallelFailures(merged)
	for _, forbidden := range []string{"422", "request_id", "wall deadline"} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("provider internals leaked after sanitization: %s", cleaned)
		}
	}
	if !strings.Contains(cleaned, "已降级，不影响旧版主流程交付") {
		t.Fatalf("user-facing degraded reason missing: %s", cleaned)
	}
}
