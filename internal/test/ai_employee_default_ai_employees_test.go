package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultAIEmployeesIncludeCodingSpecialists(t *testing.T) {
	employees := defaultAIEmployees()
	byID := make(map[string]AIEmployeeConfig, len(employees))
	for _, employee := range employees {
		byID[employee.ID] = employee
	}

	tests := []struct {
		id          string
		wantAtName  string
		wantAgent   string
		wantSummary string
	}{
		{"codex-specialist", "codex专员", "agent: codex", "Codex 输出摘要"},
		{"cc-specialist", "cc专员", "agent: claude", "Claude Code 输出摘要"},
	}

	for _, tt := range tests {
		employee, ok := byID[tt.id]
		if !ok {
			t.Fatalf("missing default employee %s", tt.id)
		}
		if employee.AtName != tt.wantAtName {
			t.Fatalf("%s AtName = %q, want %q", tt.id, employee.AtName, tt.wantAtName)
		}
		if employee.Direct == nil {
			t.Fatalf("%s should use direct external agent workflow", tt.id)
		}
		if len(employee.Direct.Tools) != 1 || employee.Direct.Tools[0] != "external_agent" {
			t.Fatalf("%s tools = %v, want [external_agent]", tt.id, employee.Direct.Tools)
		}
		if !strings.Contains(employee.Direct.Prompt, tt.wantAgent) {
			t.Fatalf("%s prompt should bind %q", tt.id, tt.wantAgent)
		}
		if !strings.Contains(employee.Direct.Prompt, tt.wantSummary) {
			t.Fatalf("%s prompt should require output summary %q", tt.id, tt.wantSummary)
		}
		if tt.id == "codex-specialist" {
			for _, want := range []string{
				"PPT/海报/生图",
				"builtin/presentation/ppt-art-director/SKILL.md",
				"builtin/presentation/ppt-master/SKILL.md",
				"builtin/presentation/base-ppt/SKILL.md",
				"builtin/presentation/paper-operators-visual/SKILL.md",
				"embedded builtin runtime",
				"python-pptx/PptxGenJS",
				"HTML/canvas",
				"完整上游 Strategist/Executor",
				"视觉检查结果",
				"回退",
			} {
				if !strings.Contains(employee.Direct.Prompt, want) {
					t.Fatalf("%s prompt should include PPT/material reference %q", tt.id, want)
				}
			}
			if strings.Contains(employee.Direct.Prompt, "pptmaster_generate") {
				t.Fatalf("%s prompt must not expose the removed Go PPT generator", tt.id)
			}
		}
	}
}

func TestDefaultInvestmentAdvisorUsesPipelineWorkflow(t *testing.T) {
	employees := defaultAIEmployees()
	var advisor *AIEmployeeConfig
	for i := range employees {
		if employees[i].ID == "investment-advisor" {
			advisor = &employees[i]
			break
		}
	}

	if advisor == nil {
		t.Fatal("missing investment-advisor")
	}
	if advisor.Direct != nil {
		t.Fatal("investment-advisor should use the investment-advisory pipeline, not direct workflow")
	}
	if advisor.Pipeline != "investment-advisory" {
		t.Fatalf("investment-advisor pipeline = %q, want investment-advisory", advisor.Pipeline)
	}
	if got := aeDisplayPipeline(advisor); got != "investment-advisory" {
		t.Fatalf("aeDisplayPipeline(investment-advisor) = %q, want investment-advisory", got)
	}
	if strings.TrimSpace(advisor.ModelBinding) != "" {
		t.Fatalf("investment-advisor should inherit the configured AI employee default instead of hard-coding a provider, binding=%q", advisor.ModelBinding)
	}
	for _, want := range []string{
		"信息查询", "客观分析", "一般策略", "个性化操作", "真实执行",
		"行情、大盘、指数", "不询问持仓、成本、资产负债", "不要求用户批准只读行情查询",
		"能买吗、怎么看", "最少参数", "默认用自然语言直接回答",
	} {
		if !strings.Contains(advisor.SystemMessage, want) {
			t.Fatalf("investment-advisor system message should require %q", want)
		}
	}
}

func TestDefaultAIEmployeesHideNonCoreEmployees(t *testing.T) {
	employees := defaultAIEmployees()
	visible := map[string]bool{}
	hidden := 0
	for _, employee := range employees {
		if employee.Hidden {
			hidden++
			continue
		}
		visible[employee.ID] = true
	}

	wantVisible := []string{
		"investment-advisor",
		"fundraising-advisor",
		"meeting-secretary",
		"content-editor",
		"lead-researcher",
		"lead-acquisition",
		"customer-service",
		"data-analyst",
		"ppt-polish-specialist",
		"product-manager",
		"codex-specialist",
	}
	if len(visible) != len(wantVisible) {
		t.Fatalf("visible employees = %v, want only %v", visible, wantVisible)
	}
	for _, id := range wantVisible {
		if !visible[id] {
			t.Fatalf("expected %s to be visible; visible=%v", id, visible)
		}
	}
	if hidden == 0 {
		t.Fatal("expected some default employees to be hidden")
	}
}

func TestDefaultCustomerAcquisitionEmployeeNotesAreNotNames(t *testing.T) {
	employees := defaultAIEmployees()
	byID := make(map[string]AIEmployeeConfig, len(employees))
	for _, employee := range employees {
		byID[employee.ID] = employee
	}

	tests := []struct {
		id       string
		wantName string
		wantNote string
	}{
		{"lead-researcher", "\u6f5c\u5ba2\u7814\u7a76\u5458", "\u53d1\u73b0\u5ba2\u6237"},
		{"lead-acquisition", "\u83b7\u5ba2\u4e13\u5458", "\u8f6c\u5316\u5ba2\u6237"},
		{"customer-service", "\u667a\u80fd\u5ba2\u670d", ""},
	}

	for _, tt := range tests {
		employee, ok := byID[tt.id]
		if !ok {
			t.Fatalf("missing default employee %s", tt.id)
		}
		if employee.Name != tt.wantName {
			t.Fatalf("%s Name = %q, want %q", tt.id, employee.Name, tt.wantName)
		}
		if strings.Contains(employee.Name, "\uff08") || strings.Contains(employee.Name, "(") {
			t.Fatalf("%s Name should not contain UI note: %q", tt.id, employee.Name)
		}
		if employee.Note != tt.wantNote {
			t.Fatalf("%s Note = %q, want %q", tt.id, employee.Note, tt.wantNote)
		}
	}
}

func TestBuiltInCustomerAcquisitionAliasesMoveLegacyNotesOutOfName(t *testing.T) {
	tests := []struct {
		cfg      AIEmployeeConfig
		wantName string
		wantNote string
	}{
		{AIEmployeeConfig{ID: "lead-researcher", Name: "\u6f5c\u5ba2\u7814\u7a76\u5458\uff08\u53d1\u73b0\u5ba2\u6237\uff09"}, "\u6f5c\u5ba2\u7814\u7a76\u5458", "\u53d1\u73b0\u5ba2\u6237"},
		{AIEmployeeConfig{ID: "lead-acquisition", Name: "\u83b7\u5ba2\u4e13\u5458\uff08\u8f6c\u5316\u5ba2\u6237\uff09"}, "\u83b7\u5ba2\u4e13\u5458", "\u8f6c\u5316\u5ba2\u6237"},
		{AIEmployeeConfig{ID: "customer-service", Name: "\u667a\u80fd\u5ba2\u670d\uff08\u8f6c\u5316\u5ba2\u6237\uff09", Note: "\u8f6c\u5316\u5ba2\u6237"}, "\u667a\u80fd\u5ba2\u670d", ""},
	}

	for _, tt := range tests {
		cfg := tt.cfg
		applyBuiltInAEAliases(&cfg)
		if cfg.Name != tt.wantName || cfg.Note != tt.wantNote {
			t.Fatalf("%s = name:%q note:%q, want name:%q note:%q", cfg.ID, cfg.Name, cfg.Note, tt.wantName, tt.wantNote)
		}
	}
}

func TestDefaultDataAnalystRequiresCompleteReport(t *testing.T) {
	employees := defaultAIEmployees()
	var dataAnalyst *AIEmployeeConfig
	for i := range employees {
		if employees[i].ID == "data-analyst" {
			dataAnalyst = &employees[i]
			break
		}
	}
	if dataAnalyst == nil {
		t.Fatal("missing data-analyst")
	}
	if dataAnalyst.Pipeline != "data-analysis-pipeline" {
		t.Fatalf("data-analyst pipeline = %q, want data-analysis-pipeline", dataAnalyst.Pipeline)
	}
	for _, want := range []string{
		"完整报告",
		"Markdown 报告",
		"数据质量",
		"图表解读",
		"产出文件清单",
		"PPT",
		".pptx",
	} {
		if !strings.Contains(dataAnalyst.SystemMessage, want) {
			t.Fatalf("data-analyst system message should contain %q", want)
		}
	}
}

func TestAEWorkLogHidesNonCoreEmployeesByDefault(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(defaultAIEmployees())
	g := &GatewayService{aeManager: mgr}

	req := httptest.NewRequest("GET", "/api/ae/worklog", nil)
	rec := httptest.NewRecorder()
	g.handleAEWorkLog(rec, req)

	var body struct {
		Employees []struct {
			EmployeeID   string `json:"employee_id"`
			Hidden       bool   `json:"hidden"`
			InternalOnly bool   `json:"internal_only"`
		} `json:"employees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Employees) != 11 {
		t.Fatalf("visible worklog employees = %d, want 11: %+v", len(body.Employees), body.Employees)
	}
	for _, employee := range body.Employees {
		if employee.Hidden {
			t.Fatalf("hidden employee leaked into default worklog: %+v", employee)
		}
	}

	req = httptest.NewRequest("GET", "/api/ae/worklog?include_hidden=1", nil)
	rec = httptest.NewRecorder()
	g.handleAEWorkLog(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode include_hidden response: %v", err)
	}
	if len(body.Employees) <= 11 {
		t.Fatalf("include_hidden employees = %d, want hidden employees included", len(body.Employees))
	}
	for _, employee := range body.Employees {
		if employee.InternalOnly {
			t.Fatalf("internal-only facade leaked through include_hidden without include_internal: %+v", employee)
		}
	}

	req = httptest.NewRequest("GET", "/api/ae/worklog?include_hidden=1&include_internal=1", nil)
	rec = httptest.NewRecorder()
	g.handleAEWorkLog(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode include_internal response: %v", err)
	}
	internalCount := 0
	for _, employee := range body.Employees {
		if employee.InternalOnly {
			internalCount++
		}
	}
	if internalCount != 3 {
		t.Fatalf("internal diagnostic employees = %d, want 3", internalCount)
	}
}

func TestDefaultFinancialProfessionalFacadesAreRetainedButInternalOnly(t *testing.T) {
	employees := defaultAIEmployees()
	byID := make(map[string]AIEmployeeConfig, len(employees))
	for _, employee := range employees {
		byID[employee.ID] = employee
	}

	tests := []struct {
		id         string
		name       string
		atName     string
		pipeline   string
		capability string
	}{
		{"quant-trader", "AI交易员", "交易员", "ai-trader", "trading"},
		{"investment-research-manager", "AI投研经理", "投研经理", "investment-research-manager", "research-management"},
		{"financial-intelligence-analyst", "AI金信分析师", "金信分析师", "financial-intelligence-analyst", "financial-intelligence"},
	}
	pipelines := (&GatewayService{}).defaultPipelineSet()
	for _, tt := range tests {
		employee, ok := byID[tt.id]
		if !ok {
			t.Fatalf("missing default financial employee %s", tt.id)
		}
		if employee.Name != tt.name || employee.AtName != tt.atName || employee.Pipeline != tt.pipeline {
			t.Fatalf("%s identity = name:%q at:%q pipeline:%q", tt.id, employee.Name, employee.AtName, employee.Pipeline)
		}
		if !employee.Hidden || !employee.InternalOnly {
			t.Fatalf("%s should be hidden and internal-only: %#v", tt.id, employee)
		}
		if !strings.Contains(employee.SystemMessage, "不得执行真实下单") {
			t.Fatalf("%s must forbid real orders", tt.id)
		}

		pipeline, ok := pipelines[tt.pipeline]
		if !ok || len(pipeline.Steps) != 1 {
			t.Fatalf("%s pipeline = %#v, want one isolated step", tt.id, pipeline)
		}
		step := pipeline.Steps[0]
		if step.ProfessionalAgent == nil || step.ProfessionalAgent.ID != "quant-trader" || step.ProfessionalAgent.Capability != tt.capability {
			t.Fatalf("%s professional route = %#v, want quant-trader/%s", tt.id, step.ProfessionalAgent, tt.capability)
		}
		if !step.NoTools || step.Prompt != "${input}" {
			t.Fatalf("%s pipeline must forward input directly without host tools: %#v", tt.id, step)
		}
	}

	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(employees)
	mentions := []struct {
		message string
		wantID  string
	}{
		{"@投资顾问 分析新安股份", "investment-advisor"},
		{"@交易员 分析新安股份", ""},
		{"@AI交易员 分析新安股份", ""},
		{"@量化交易员 分析新安股份", ""},
		{"@AI量化交易员 分析新安股份", ""},
		{"@投研经理 评审新安股份", ""},
		{"@AI投研经理 评审新安股份", ""},
		{"@金信分析师 分析新安股份公告", ""},
		{"@AI金信分析师 分析新安股份公告", ""},
		{"交易员 分析新安股份", ""},
		{"投研经理 评审新安股份", ""},
		{"金信分析师 分析新安股份公告", ""},
		{"@金融分析师 分析新安股份公告", ""},
	}
	for _, tt := range mentions {
		cfg, task := mgr.MatchAtName(tt.message)
		if tt.wantID == "" {
			if cfg != nil {
				t.Fatalf("MatchAtName(%q) = %s, want no professional employee", tt.message, cfg.ID)
			}
			continue
		}
		if cfg == nil || cfg.ID != tt.wantID {
			t.Fatalf("MatchAtName(%q) = %#v, want %s", tt.message, cfg, tt.wantID)
		}
		if !strings.Contains(task, "新安股份") {
			t.Fatalf("MatchAtName(%q) task = %q, want original task", tt.message, task)
		}
	}
}

func TestAEWorkLogDoesNotAttachLegacyCompositeHistoryToFinancialFacades(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(defaultAIEmployees())
	now := time.Now()
	mgr.workLog["quant-trader"] = []*AEWorkRecord{
		{ID: "legacy-composite", EmployeeID: "quant-trader", Pipeline: "quant-trader", Status: "completed", Result: "old three-backend composite", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "current-trading", EmployeeID: "quant-trader", Pipeline: "ai-trader", Status: "completed", Result: "trading-only result", CreatedAt: now, UpdatedAt: now},
	}
	mgr.workLog["investment-research-manager"] = []*AEWorkRecord{
		{ID: "wrong-manager", EmployeeID: "investment-research-manager", Pipeline: "quant-trader", Status: "completed", Result: "wrong composite result", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "current-manager", EmployeeID: "investment-research-manager", Pipeline: "investment-research-manager", Status: "completed", Result: "manager-only result", CreatedAt: now, UpdatedAt: now},
	}
	mgr.workLog["financial-intelligence-analyst"] = []*AEWorkRecord{
		{ID: "wrong-analyst", EmployeeID: "financial-intelligence-analyst", Pipeline: "quant-trader", Status: "completed", Result: "wrong composite result", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "current-analyst", EmployeeID: "financial-intelligence-analyst", Pipeline: "financial-intelligence-analyst", Status: "completed", Result: "analyst-only result", CreatedAt: now, UpdatedAt: now},
	}

	g := &GatewayService{aeManager: mgr}
	req := httptest.NewRequest("GET", "/api/ae/worklog?include_hidden=1&include_internal=1", nil)
	rec := httptest.NewRecorder()
	g.handleAEWorkLog(rec, req)
	var body struct {
		Employees []struct {
			EmployeeID string          `json:"employee_id"`
			TotalTasks int             `json:"total_tasks"`
			RecentWork []*AEWorkRecord `json:"recent_work"`
		} `json:"employees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode worklog: %v", err)
	}
	wantRecord := map[string]string{
		"quant-trader":                   "current-trading",
		"investment-research-manager":    "current-manager",
		"financial-intelligence-analyst": "current-analyst",
	}
	for employeeID, recordID := range wantRecord {
		found := false
		for _, employee := range body.Employees {
			if employee.EmployeeID != employeeID {
				continue
			}
			found = true
			if employee.TotalTasks != 1 || len(employee.RecentWork) != 1 || employee.RecentWork[0].ID != recordID {
				t.Fatalf("%s worklog leaked legacy pipeline history: %+v", employeeID, employee)
			}
		}
		if !found {
			t.Fatalf("missing worklog employee %s", employeeID)
		}
	}
}

func TestDefaultAIEmployeesKeepRoleBasedSpecialistNames(t *testing.T) {
	employees := defaultAIEmployees()
	byID := make(map[string]AIEmployeeConfig, len(employees))
	for _, employee := range employees {
		byID[employee.ID] = employee
	}

	tests := []struct {
		id       string
		name     string
		atName   string
		pipeline string
		hidden   bool
	}{
		{"ppt-polish-specialist", "PPT润色专员", "PPT润色", "ppt-polish", false},
		{"product-manager", "产品经理", "产品经理", "product-management", false},
		{"bid-specialist", "投竞标专员", "投竞标", "bid-proposal", true},
	}
	for _, tt := range tests {
		employee, ok := byID[tt.id]
		if !ok {
			t.Fatalf("missing default employee %s", tt.id)
		}
		if employee.Name != tt.name || employee.AtName != tt.atName || employee.Pipeline != tt.pipeline {
			t.Fatalf("%s = name:%q at:%q pipeline:%q", tt.id, employee.Name, employee.AtName, employee.Pipeline)
		}
		if employee.Hidden != tt.hidden {
			t.Fatalf("%s hidden = %v, want %v", tt.id, employee.Hidden, tt.hidden)
		}
	}
}

func TestRoleBasedSpecialistMentionsRouteToStableEmployeeIDs(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(defaultAIEmployees())

	tests := []struct {
		message string
		wantID  string
	}{
		{"@PPT润色 优化这一页", "ppt-polish-specialist"},
		{"@产品经理 补充验收标准", "product-manager"},
		{"@报价专员 按WBS给三档报价", "product-manager"},
	}

	for _, tt := range tests {
		cfg, task := mgr.MatchAtName(tt.message)
		if cfg == nil || cfg.ID != tt.wantID {
			got := ""
			if cfg != nil {
				got = cfg.ID
			}
			t.Fatalf("MatchAtName(%q) = %q, want %q", tt.message, got, tt.wantID)
		}
		if strings.TrimSpace(task) == "" {
			t.Fatalf("MatchAtName(%q) returned an empty task", tt.message)
		}
	}
}

func TestCodexSpecialistMentionAliasesRequireAtSign(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(defaultAIEmployees())

	tests := []struct {
		message string
		wantID  string
	}{
		{"@codeX 修复登录 bug", "codex-specialist"},
		{"@codex 修复登录 bug", "codex-specialist"},
		{"@codex专员 修复登录 bug", "codex-specialist"},
		{"@Codex专员 修复登录 bug", "codex-specialist"},
		{"codex 修复登录 bug", ""},
		{"codex专员 修复登录 bug", ""},
	}

	for _, tt := range tests {
		cfg, task := mgr.MatchAtName(tt.message)
		if tt.wantID == "" {
			if cfg != nil {
				t.Fatalf("MatchAtName(%q) = %s, want no match", tt.message, cfg.ID)
			}
			continue
		}
		if cfg == nil {
			t.Fatalf("MatchAtName(%q) returned no match", tt.message)
		}
		if cfg.ID != tt.wantID {
			t.Fatalf("MatchAtName(%q) = %s, want %s", tt.message, cfg.ID, tt.wantID)
		}
		if !strings.Contains(task, "修复登录 bug") {
			t.Fatalf("MatchAtName(%q) task = %q", tt.message, task)
		}
	}
}

func TestCCSpecialistMentionAliasesRequireAtSign(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(defaultAIEmployees())

	tests := []struct {
		message string
		wantID  string
	}{
		{"@cc 修复测试", "cc-specialist"},
		{"@CC 修复测试", "cc-specialist"},
		{"@cc专员 修复测试", "cc-specialist"},
		{"@CC专员 修复测试", "cc-specialist"},
		{"@claude 修复测试", "cc-specialist"},
		{"@claude code 修复测试", "cc-specialist"},
		{"@claude code专员 修复测试", "cc-specialist"},
		{"@Claude Code专员 修复测试", "cc-specialist"},
		{"cc 修复测试", ""},
		{"cc专员 修复测试", ""},
		{"claude code专员 修复测试", ""},
	}

	for _, tt := range tests {
		cfg, task := mgr.MatchAtName(tt.message)
		if tt.wantID == "" {
			if cfg != nil {
				t.Fatalf("MatchAtName(%q) = %s, want no match", tt.message, cfg.ID)
			}
			continue
		}
		if cfg == nil {
			t.Fatalf("MatchAtName(%q) returned no match", tt.message)
		}
		if cfg.ID != tt.wantID {
			t.Fatalf("MatchAtName(%q) = %s, want %s", tt.message, cfg.ID, tt.wantID)
		}
		if !strings.Contains(task, "修复测试") {
			t.Fatalf("MatchAtName(%q) task = %q", tt.message, task)
		}
	}
}
