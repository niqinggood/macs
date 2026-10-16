package app

import (
	"reflect"
	"testing"
)

func TestClassifyInvestmentRequestSeparatesInformationStrategyAndPersonalizedAction(t *testing.T) {
	tests := []struct {
		name                string
		input               string
		wantIntent          string
		wantDecision        string
		wantDepth           string
		wantPersonalization bool
		wantMissing         []string
	}{
		{
			name:         "market brief needs no personal profile",
			input:        "分析今天市场行情",
			wantIntent:   investmentIntentMarketBrief,
			wantDecision: investmentDecisionInformation,
			wantDepth:    "quick",
		},
		{
			name:         "instrument analysis",
			input:        "新安股份怎么看",
			wantIntent:   investmentIntentInstrumentAnalysis,
			wantDecision: investmentDecisionAnalysis,
			wantDepth:    "standard",
		},
		{
			name:         "generic buy question is conditional strategy",
			input:        "新安股份现在能买吗",
			wantIntent:   investmentIntentGeneralStrategy,
			wantDecision: investmentDecisionGeneralStrategy,
			wantDepth:    "standard",
		},
		{
			name:                "holding action needs minimal decisive inputs",
			input:               "我持有新安股份，现在应该加仓还是卖出",
			wantIntent:          investmentIntentPersonalPosition,
			wantDecision:        investmentDecisionPersonalizedAction,
			wantDepth:           "standard",
			wantPersonalization: true,
			wantMissing:         []string{"持仓成本", "当前仓位比例", "计划持有周期", "可承受风险或最大亏损"},
		},
		{
			name:                "complete holding action can proceed",
			input:               "我持有新安股份，成本8.5元，占总仓30%，计划中线持有，最大亏损10%，现在怎么操作",
			wantIntent:          investmentIntentPersonalPosition,
			wantDecision:        investmentDecisionPersonalizedAction,
			wantDepth:           "standard",
			wantPersonalization: true,
		},
		{
			name:         "execution is refused rather than personalized",
			input:        "帮我直接下单买入新安股份",
			wantIntent:   investmentIntentGeneralStrategy,
			wantDecision: investmentDecisionExecution,
			wantDepth:    "standard",
		},
		{
			name:         "deep report requests an artifact depth",
			input:        "给我一份新能源行业的深度报告",
			wantIntent:   investmentIntentSectorAnalysis,
			wantDecision: investmentDecisionAnalysis,
			wantDepth:    "deep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyInvestmentRequest(tt.input)
			if got.Intent != tt.wantIntent || got.DecisionLevel != tt.wantDecision || got.AnswerDepth != tt.wantDepth || got.PersonalizationRequired != tt.wantPersonalization {
				t.Fatalf("profile = %#v", got)
			}
			if !reflect.DeepEqual(got.MissingDecisiveFields, tt.wantMissing) {
				t.Fatalf("missing fields = %v, want %v", got.MissingDecisiveFields, tt.wantMissing)
			}
		})
	}
}

func TestInvestmentRequestWantsArtifactOnlyForDeepOrExplicitSave(t *testing.T) {
	brief := classifyInvestmentRequest("分析今天市场行情")
	if investmentRequestWantsArtifact(brief, "分析今天市场行情") {
		t.Fatal("ordinary market brief should be returned in chat")
	}
	if !investmentRequestWantsArtifact(brief, "分析今天市场行情并保存为 Markdown") {
		t.Fatal("explicit save request should produce an artifact")
	}
	deep := classifyInvestmentRequest("详细分析新安股份并生成完整报告")
	if !investmentRequestWantsArtifact(deep, "详细分析新安股份并生成完整报告") {
		t.Fatal("deep research should produce an artifact")
	}
	if investmentRequestWantsArtifact(
		deep,
		"详细分析当前 A 股行情。只在聊天中回答，不要保存、生成或导出任何文件。",
	) {
		t.Fatal("explicit chat-only request must override deep artifact delivery")
	}
}

func TestInvestmentProfileReadsRunLocalIntakeMarkers(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{
		Name: "需求受理与意图理解",
		Output: `INTAKE_STATUS: READY
INVESTMENT_INTENT: fund_etf_analysis
DECISION_LEVEL: analysis
ANSWER_DEPTH: quick
PERSONALIZATION_REQUIRED: false`,
	}}}
	got := investmentProfileFromExecution(exec, 1, "这个产品怎么看")
	if got.Intent != investmentIntentFundETFAnalysis || got.DecisionLevel != investmentDecisionAnalysis || got.AnswerDepth != "quick" || got.PersonalizationRequired {
		t.Fatalf("marker profile = %#v", got)
	}
}
