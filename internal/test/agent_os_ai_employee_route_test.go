package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAgentOSAIEmployeeManager() *AIEmployeeManager {
	cfg := &AIEmployeeConfig{
		ID:       "investment-advisor",
		Name:     "投资顾问",
		AtName:   "投资顾问",
		Pipeline: "investment-advisory",
	}
	return &AIEmployeeManager{
		employees: map[string]*AIEmployeeConfig{cfg.ID: cfg},
		byAtName:  map[string]*AIEmployeeConfig{cfg.AtName: cfg},
	}
}

func TestAgentOSExplicitMentionDropsCallerRoleWrapper(t *testing.T) {
	manager := testAgentOSAIEmployeeManager()
	input := `你是运行在云端的通用 OpenKitty AI 员工。本次切换为“投资顾问”工作模式。
先了解并确认用户的风险承受能力、投资期限、资产与负债情况；关键信息不足时先澄清。

用户原始任务：
@投资顾问 请作为投资顾问，今天大盘行情如何`

	normalized := normalizeAgentOSExplicitAEMentionMessage(manager, input)
	if normalized != "@投资顾问 请作为投资顾问，今天大盘行情如何" {
		t.Fatalf("normalized current request = %q", normalized)
	}
	cfg, task, current := matchAgentOSExplicitAIEmployee(manager, input)
	if cfg == nil || cfg.ID != "investment-advisor" {
		t.Fatalf("explicit employee was not matched: %#v", cfg)
	}
	if task != "请作为投资顾问，今天大盘行情如何" || current != normalized {
		t.Fatalf("task=%q current=%q", task, current)
	}
	for _, forbidden := range []string{"风险承受能力", "资产与负债", "关键信息不足时先澄清"} {
		if strings.Contains(task, forbidden) {
			t.Fatalf("caller role wrapper leaked into employee task: %q", task)
		}
	}
}

func TestAgentOSHistoricalMentionCannotActivateEmployee(t *testing.T) {
	manager := testAgentOSAIEmployeeManager()
	input := `调用方提供了以下历史对话。

[user]
@投资顾问 分析昨天市场

当前用户请求：
明天上海天气怎么样`
	cfg, task, current := matchAgentOSExplicitAIEmployee(manager, input)
	if cfg != nil || task != "" {
		t.Fatalf("historical mention activated employee: cfg=%#v task=%q", cfg, task)
	}
	if current != "明天上海天气怎么样" {
		t.Fatalf("current request = %q", current)
	}
	if normalized := normalizeAgentOSExplicitAEMentionMessage(manager, input); normalized != strings.TrimSpace(input) {
		t.Fatalf("non-employee request should retain caller context: %q", normalized)
	}
}

func TestAgentOSDirectMentionWithoutWrapperStillRoutes(t *testing.T) {
	manager := testAgentOSAIEmployeeManager()
	input := "@投资顾问 新安股份现在能买吗"
	cfg, task, current := matchAgentOSExplicitAIEmployee(manager, input)
	if cfg == nil || cfg.ID != "investment-advisor" || task != "新安股份现在能买吗" || current != input {
		t.Fatalf("cfg=%#v task=%q current=%q", cfg, task, current)
	}
}

func TestAgentOSExplicitMentionSealsCurrentUserIntentForPipelineGuards(t *testing.T) {
	manager := testAgentOSAIEmployeeManager()
	wrapped := `调用方角色说明：先读取工作目录文件并搜索。

用户原始任务：
@投资顾问 分析一下当前 A 股行情和大盘涨跌原因`
	current := normalizeAgentOSExplicitAEMentionMessage(manager, wrapped)
	ctx := withTenantRequestEvidence(restrictedTenantTestContext(t.TempDir()), current, nil)
	classificationInput := tenantUserInputFromContext(ctx, "阶段提示中包含文件、目录、搜索等内部执行规则")
	if classificationInput != current {
		t.Fatalf("sealed input=%q, want current user turn %q", classificationInput, current)
	}
	if tenantFilesystemFactRequest(classificationInput) {
		t.Fatalf("market request was misclassified as a filesystem fact request: %q", classificationInput)
	}
}

func TestAgentOSTaskPhaseLabelHidesInternalEmployeeID(t *testing.T) {
	gateway := &GatewayService{aeManager: testAgentOSAIEmployeeManager()}
	task := &GatewayTask{ID: "agt-test", Status: "running", CurrentPhase: "ai_employee:investment-advisor"}
	response := gateway.agentOSTaskResponse(task, false)
	if response["phase"] != "投资顾问" || response["phase_label"] != "投资顾问" {
		t.Fatalf("public phase labels leaked internal id: %#v", response)
	}
	if response["phase_code"] != "ai_employee:investment-advisor" {
		t.Fatalf("machine-readable phase code missing: %#v", response)
	}
	for code, want := range map[string]string{
		"queued": "等待执行", "reasoning": "正在分析", "tool:web_search": "正在查询资料", "completed": "已完成",
	} {
		if got := gateway.agentOSTaskPhaseLabel(code); got != want {
			t.Fatalf("phase label %q = %q, want %q", code, got, want)
		}
	}
}

func TestInvestmentAdvisorSystemPromptUsesDecisionLevels(t *testing.T) {
	var advisor *AIEmployeeConfig
	for _, cfg := range defaultAIEmployees() {
		if cfg.ID == "investment-advisor" {
			copy := cfg
			advisor = &copy
			break
		}
	}
	if advisor == nil {
		t.Fatal("investment advisor default is missing")
	}
	for _, want := range []string{
		"信息查询", "一般策略", "个性化操作", "不询问持仓、成本、资产负债",
		"一般性的“能买吗、怎么看、是否值得关注”", "最少参数", "不要求用户批准只读行情查询",
	} {
		if !strings.Contains(advisor.SystemMessage, want) {
			t.Fatalf("investment advisor system prompt missing %q: %s", want, advisor.SystemMessage)
		}
	}
}

func TestAgentOSExplicitAIEmployeeResultPersistsArtifactDownloadLink(t *testing.T) {
	workspace := t.TempDir()
	pptx := filepath.Join(workspace, ".openkitty", "storage", "fundraising_bp_delivery.pptx")
	if err := os.MkdirAll(filepath.Dir(pptx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pptx, []byte("validated pptx"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{
		ID:                "pipe_direct_fundraising-advisor_delivery",
		PipelineName:      "direct_fundraising-advisor",
		Status:            "degraded",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{{
			Name:   "融资BP演示文稿生成",
			Status: "degraded",
			Output: "融资 BP 演示文稿已生成。\n- PPTX真实路径：.openkitty/storage/fundraising_bp_delivery.pptx",
		}},
	}

	result := agentOSExplicitAIEmployeeResult(exec)
	for _, want := range []string{
		"融资 BP 演示文稿已生成",
		"交付物：",
		"[fundraising_bp_delivery.pptx](/api/v1/agent/files?path=.openkitty%2Fstorage%2Ffundraising_bp_delivery.pptx)",
		"交付摘要：[pipe_direct_fundraising-advisor_delivery](/api/delivery/summary?exec_id=pipe_direct_fundraising-advisor_delivery)",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("persisted AgentOS result missing %q:\n%s", want, result)
		}
	}
}
