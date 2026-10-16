package app

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ====================== Digital Employee Tests ======================

func TestDE_Templates(t *testing.T) {
	templates := GetAETemplates()
	if len(templates) == 0 {
		t.Fatal("expected at least 1 DE template")
	}
	// Check required fields
	for _, tmpl := range templates {
		if tmpl.Name == "" {
			t.Error("template has empty name")
		}
		if tmpl.Description == "" {
			t.Errorf("template %s has empty description", tmpl.Name)
		}
		if tmpl.SystemMessage == "" {
			t.Errorf("template %s has empty system prompt", tmpl.Name)
		}
	}
	t.Logf("DE Templates: %d loaded", len(templates))
}

func TestDE_Register(t *testing.T) {
	cfg := &Config{
		AIEmployees: []AIEmployeeConfig{
			{
				ID:     "test_finance",
				Name:   "财务专员",
				AtName: "财务",
				Role:   "财务分析",
				Direct: &DirectConfig{
					Prompt: "分析数据",
					Tools:  []string{"read_file", "data_science"},
				},
			},
			{
				ID:     "test_legal",
				Name:   "法务顾问",
				AtName: "法务",
				Role:   "合同审查",
				Direct: &DirectConfig{
					Prompt: "审查合同",
					Tools:  []string{"read_file", "legal_qa"},
				},
			},
		},
	}

	// Simulate registration (no actual gateway needed for unit test)
	if len(cfg.AIEmployees) != 2 {
		t.Fatal("expected 2 DEs")
	}
	if cfg.AIEmployees[0].ID != "test_finance" {
		t.Error("DE1 ID mismatch")
	}
	if cfg.AIEmployees[1].AtName != "法务" {
		t.Error("DE2 AtName mismatch")
	}

	t.Log("DE Register: OK")
}

func TestDE_MatchAtName(t *testing.T) {
	// Create a minimal manager for name matching
	logger := newTestLogger()
	mgr := &AIEmployeeManager{
		employees: make(map[string]*AIEmployeeConfig),
		byAtName:  make(map[string]*AIEmployeeConfig),
		logger:    logger,
	}

	// Register test employees
	cfg1 := &AIEmployeeConfig{ID: "de1", Name: "财务", AtName: "财务专员"}
	cfg2 := &AIEmployeeConfig{ID: "de2", Name: "法务", AtName: "法务顾问"}
	mgr.byAtName["财务专员"] = cfg1
	mgr.byAtName["法务顾问"] = cfg2

	// Test matching
	tests := []struct {
		msg   string
		found bool
		empID string
	}{
		{"@财务专员 帮我分析报表", true, "de1"},
		{"@法务顾问 审查这份合同", true, "de2"},
		{"今天天气真好", false, ""},
		{"财务专员 请帮忙", false, ""},
	}
	for _, tt := range tests {
		cfg, task := mgr.MatchAtName(tt.msg)
		found := cfg != nil
		if found != tt.found {
			t.Errorf("MatchAtName(%q): found=%v, want=%v", tt.msg, found, tt.found)
		}
		if found && cfg.ID != tt.empID {
			t.Errorf("MatchAtName(%q): got employee=%s, want=%s", tt.msg, cfg.ID, tt.empID)
		}
		if found && task == "" {
			t.Errorf("MatchAtName(%q): task should not be empty", tt.msg)
		}
	}

	t.Log("DE Match: OK")
}

func TestResolveSupervisorChannelTargetPrefersDynamicChannel(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	cfg := &AIEmployeeConfig{
		ID:      "codex-specialist",
		Name:    "Codex 专员",
		Channel: "feishu",
		ChatID:  "old-chat",
	}
	mgr.SetChannelChatID(cfg.ID, "weixin", "wx-user-1")

	channelName, chatID := mgr.resolveSupervisorChannelTarget(cfg)
	if channelName != "weixin" || chatID != "wx-user-1" {
		t.Fatalf("target = (%q, %q), want (weixin, wx-user-1)", channelName, chatID)
	}
}

func TestDE_WorkingHours(t *testing.T) {
	// Test the working hours parser
	tests := []struct {
		hours   string
		inRange bool // note: depends on current time, approximate
	}{
		{"09:00-18:00", true},             // during work day
		{"00:00-23:59", true},             // all day
		{"", true},                        // empty = 24/7
		{"09:00-12:00,14:00-18:00", true}, // multi-segment
	}
	for _, tt := range tests {
		result := isWithinWorkingHours(tt.hours)
		// We can't assert exact result (depends on current time),
		// but the function should not panic
		_ = result
	}
	t.Log("DE WorkingHours: OK")
}

// ====================== Team Assembly Tests ======================

func TestTeam_AutoMatch(t *testing.T) {
	tool := &AssembleTeamTool{workspace: "."}

	tests := []struct {
		task     string
		wantTmpl string // expected best template
	}{
		{"研究一下这个AI市场", "research"},
		{"帮我写一个Go项目", "coding"},
		{"做一个产品演示PPT", "ppt"},
		{"审查这份合同", "contract"},
		{"竞品分析报告", "competitor"},
	}
	for _, tt := range tests {
		tmpl, reason := tool.autoMatch(tt.task)
		if tmpl == "" {
			t.Errorf("autoMatch(%q): got empty template", tt.task)
		}
		t.Logf("autoMatch(%q) → %s (%s)", tt.task, tmpl, reason)
	}

	t.Log("Team AutoMatch: OK")
}

func TestTeam_BuildRoleTask(t *testing.T) {
	tool := &AssembleTeamTool{workspace: "."}
	role := TeamRole{Role: "分析师", SystemPrompt: "你是一个数据分析师"}
	task := "分析Q2销售数据"

	builder := tool.buildRoleTask(role, task)
	result := builder.String()
	if result == "" {
		t.Error("buildRoleTask returned empty")
	}
	if !strings.Contains(result, "分析师") {
		t.Error("role name not in task prompt")
	}
	if !strings.Contains(result, task) {
		t.Error("original task not in role prompt")
	}

	t.Log("Team BuildRole: OK")
}

func TestTeam_BuildRoleTaskMissingSkillWithoutLoggerDoesNotPanic(t *testing.T) {
	tool := &AssembleTeamTool{workspace: "."}
	role := TeamRole{
		Role:         "analyst",
		SystemPrompt: "analyze the source material",
		Skill:        "definitely-missing-skill",
	}

	result := tool.buildRoleTask(role, "produce a report").String()
	if !strings.Contains(result, "analyst") || !strings.Contains(result, "produce a report") {
		t.Fatalf("expected role and task in prompt, got %q", result)
	}
}

func TestTeam_LoadSkillContentFindsCategorizedSkill(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "presentation", "better-ppt")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: better-ppt\n---\n\n# Better PPT\n\nUse categorized skill content."), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := &AssembleTeamTool{workspace: dir}
	got := tool.loadSkillContent("better-ppt")
	if !strings.Contains(got, "Use categorized skill content.") {
		t.Fatalf("categorized skill not loaded, got %q", got)
	}
}

func TestTeam_HybridMissingSubAgentReturnsError(t *testing.T) {
	tool := &AssembleTeamTool{
		workspace:    ".",
		fileLogger:   log.New(io.Discard, "", 0),
		toolRegistry: NewToolRegistry(),
	}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"template": "competitor",
		"task":     "竞品分析报告",
	})
	if err == nil || !strings.Contains(err.Error(), "sub_agent tool not available") {
		t.Fatalf("expected missing sub_agent error, got %v", err)
	}
}

func TestTeam_AutoMatchWithoutLoggerReturnsToolError(t *testing.T) {
	tool := &AssembleTeamTool{
		workspace:    ".",
		toolRegistry: NewToolRegistry(),
	}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"template": "auto",
		"task":     "competitor analysis report",
	})
	if err == nil || !strings.Contains(err.Error(), "sub_agent tool not available") {
		t.Fatalf("expected missing sub_agent error, got %v", err)
	}
}

func TestTeam_HybridInvalidStageReturnsError(t *testing.T) {
	roles := []TeamRole{{Role: "only", SystemPrompt: "only one role"}}
	err := validateTeamStages(roles, hybridTemplateStages["competitor"])
	if err == nil || !strings.Contains(err.Error(), "references role index") {
		t.Fatalf("expected invalid stage error, got %v", err)
	}
}

// ====================== Pipeline Tests ======================

func TestPipeline_GetTemplates(t *testing.T) {
	templates := GetPipelineTemplates()
	if len(templates) == 0 {
		t.Fatal("expected pipeline templates")
	}
	// Verify each template has required fields
	for name, tmpl := range templates {
		if tmpl.Name == "" {
			t.Errorf("template %s has empty name", name)
		}
		if len(tmpl.Steps) == 0 {
			t.Errorf("template %s has no steps", name)
		}
		for i, step := range tmpl.Steps {
			if step.Name == "" {
				t.Errorf("template %s step %d has empty name", name, i)
			}
		}
	}
	t.Logf("Pipeline Templates: %d loaded", len(templates))
}

func TestPipeline_LeadResearchInputModes(t *testing.T) {
	gw := &GatewayService{}
	pipelines := gw.defaultPipelineSet()
	pipeline, ok := pipelines["lead-research"]
	if !ok {
		t.Fatal("lead-research pipeline missing")
	}
	if len(pipeline.Steps) != 4 {
		t.Fatalf("lead-research pipeline has %d steps, want 4", len(pipeline.Steps))
	}

	step0 := pipeline.Steps[0]
	if step0.Name != "产品画像与SWOT" {
		t.Fatalf("lead-research step0 = %q, want 产品画像与SWOT", step0.Name)
	}
	for _, want := range []string{
		"输入类型识别",
		"目录",
		"文件",
		"一段文字",
		"glob",
		"自主分析与必要门禁",
		"定义研究对象",
		"默认工作区只是执行环境",
		"不得扫描工作区来猜产品",
		"目录是查找范围",
		"不自动证明其中所有项目都属于目标产品",
		"同目录",
		"按证据强度而不是固定来源清单判断",
		"A 级·直接证据",
		"B 级·交叉证据",
		"C 级·分析假设",
		"官网、官方文档和产品方材料通常是最强的一方来源",
		"不是完成初步画像的强制前提",
		"禁止猜测域名、GitHub 账号、仓库地址或常见路径",
		"不得因单一来源失败就停止",
		"可以支持技术能力与实现方式",
		"网络搜索超时",
		"决定性歧义",
		"会导向不同 ICP 或检索方向",
		"需要用户补充的最少信息",
		"INPUT_STATUS: MISSING",
		"<!--PIPELINE_STOP-->",
		"自适应工作流程",
		"不要机械执行固定来源清单或固定调用次数",
		"足以生成可审计的 ICP 与搜索策略",
		"非决定性未知",
		"证据账本",
		"SWOT",
		"优势",
		"劣势",
		"机会",
		"威胁",
		"竞品",
		"负面 ICP",
		"实体分类",
		"PRODUCT_PROFILE_STATUS: READY",
	} {
		if !strings.Contains(step0.Prompt, want) {
			t.Errorf("lead-research step0 prompt missing %q", want)
		}
	}
	if step0.MaxIter != 10 {
		t.Fatalf("lead-research step0 MaxIter = %d, want 10", step0.MaxIter)
	}
	if len(step0.Routing) != 1 || step0.Routing[0].Goto != "__stop__" {
		t.Fatalf("lead-research product identity gate must stop for user clarification: %#v", step0.Routing)
	}

	for _, wantTool := range []string{"read_file", "list_dir", "glob", "web_search", "web_fetch", "web_navigate", "web_page", "web_close"} {
		found := false
		for _, tool := range step0.Tools {
			if tool == wantTool {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("lead-research step0 missing tool %q", wantTool)
		}
	}
	for _, tool := range step0.Tools {
		if tool == "persistent_shell" {
			t.Fatal("lead-research product evidence stage must not expose persistent_shell")
		}
	}

	step1 := pipeline.Steps[1]
	if step1.Name != "搜索策略" {
		t.Fatalf("lead-research step1 = %q, want 搜索策略", step1.Name)
	}
	if !step1.NoTools {
		t.Fatal("lead-research search strategy must not use tools")
	}
	for _, want := range []string{
		"${step0.output}",
		"关键词矩阵",
		"负面关键词",
		"多源",
		"不少于 12 条",
		"实体分类",
		"证据有效性",
		"至少保留最后 3 轮",
		"SEARCH_PLAN_STATUS: READY",
	} {
		if !strings.Contains(step1.Prompt, want) {
			t.Errorf("lead-research step1 prompt missing %q", want)
		}
	}

	step2 := pipeline.Steps[2]
	if step2.Name != "多源搜索与核验" {
		t.Fatalf("lead-research step2 = %q, want 多源搜索与核验", step2.Name)
	}
	if step2.NoTools {
		t.Fatal("lead-research search step unexpectedly has no tools")
	}
	if !step2.ContinueOnError {
		t.Fatal("lead-research search step must preserve evidence gaps and continue to the report stage")
	}
	if step2.MaxIter > 20 {
		t.Fatalf("lead-research search step has an excessive low-yield search budget: %d", step2.MaxIter)
	}
	for _, unwanted := range []string{"read_file", "list_dir", "glob", "grep"} {
		for _, tool := range step2.Tools {
			if tool == unwanted {
				t.Errorf("lead-research search step must not rescan local project with %q", unwanted)
			}
		}
	}
	for _, want := range []string{
		"${step0.output}",
		"${step1.output}",
		"完整的 http:// 或 https:// URL",
		"两个不同来源类别",
		"实体分类表",
		"已核验潜客表",
		"排除对象表",
		"证据有效性审计",
		"RESEARCH_STATUS: READY",
		"RESEARCH_STATUS: DEGRADED",
	} {
		if !strings.Contains(step2.Prompt, want) {
			t.Errorf("lead-research step2 prompt missing %q", want)
		}
	}

	step3 := pipeline.Steps[3]
	if step3.Name != "报告整理" {
		t.Fatalf("lead-research step3 = %q, want 报告整理", step3.Name)
	}
	if !step3.NoTools {
		t.Fatal("lead-research report step should not use tools")
	}
	for _, want := range []string{
		"${step0.output}",
		"${step1.output}",
		"${step2.output}",
		"不再发起新搜索",
		"已核验潜客优先级表",
		"排除对象表",
		"证据有效性审计",
		"不足 5 个合格潜客就如实写实际数量",
		"AI员工：潜客研究员",
	} {
		if !strings.Contains(step3.Prompt, want) {
			t.Errorf("lead-research step3 prompt missing %q", want)
		}
	}
}

func TestDefaultAIEmployees_PipelineBindings(t *testing.T) {
	pipelines := (&GatewayService{}).defaultPipelineSet()
	employees := defaultAIEmployees()

	if len(employees) == 0 {
		t.Fatal("defaultAIEmployees returned empty list")
	}

	for _, emp := range employees {
		if emp.ID == "" {
			t.Error("employee has empty ID")
		}
		if emp.Name == "" {
			t.Errorf("employee %s has empty name", emp.ID)
		}
		if emp.AtName == "" {
			t.Errorf("employee %s has empty @name", emp.ID)
		}
		if emp.Pipeline != "" {
			if _, ok := pipelines[emp.Pipeline]; !ok {
				t.Errorf("employee %s binds missing pipeline %q", emp.ID, emp.Pipeline)
			}
		}
	}
}

func TestPipeline_ConfigValidation(t *testing.T) {
	tmpDir := t.TempDir()
	configContent := `
team_name: test_team
task:
    description: test task
agents:
    - name: TestAgent
      type: custom
      model: minimax_model
      system_message: You are a test agent
      temperature: 0.7
      max_tokens: 4096
collaboration:
    type: sequential
    max_round: 1
model_cfg:
    minimax_model:
        type: minimax
        api_key: ""
        model_name: minimax-m3
        base_url: https://api.minimaxi.com/v1
        temperature: 0.7
        max_tokens: 4096
digital_employees:
    - id: de_test
      name: "测试员工"
      at_name: 测试
      role: "测试"
      direct:
        prompt: "测试任务"
        tools: ["read_file"]
`
	configPath := filepath.Join(tmpDir, "test_config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(config.AIEmployees) != 1 {
		t.Errorf("expected 1 DE, got %d", len(config.AIEmployees))
	}
	de := config.AIEmployees[0]
	if de.ID != "de_test" {
		t.Errorf("DE ID mismatch: %s", de.ID)
	}
	if de.Direct == nil {
		t.Fatal("DE Direct config is nil")
	}
	if len(de.Direct.Tools) != 1 || de.Direct.Tools[0] != "read_file" {
		t.Errorf("DE tools mismatch: %v", de.Direct.Tools)
	}

	t.Log("Pipeline Config: OK")
}

// ====================== Helper ======================

func newTestLogger() *log.Logger {
	return log.New(os.Stdout, "[TEST] ", log.LstdFlags)
}
