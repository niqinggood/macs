package app

import (
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"mac/internal/aiemployee"
	gatewayhttp "mac/internal/gateway/httpx"
	operationstools "mac/internal/tools/operations"
)

// ====================== Tool Registry ======================

func TestToolRegistryRegisterAndGet(t *testing.T) {
	reg := NewToolRegistry()
	if len(reg.ListNames()) != 0 {
		t.Error("new registry should be empty")
	}

	reg.Register(&ToolDetailTool{registry: reg})
	if _, ok := reg.Get("tool_detail"); !ok {
		t.Error("tool_detail should be registered")
	}
	if len(reg.ListNames()) != 1 {
		t.Errorf("expected 1 tool, got %d", len(reg.ListNames()))
	}
}

func TestToolRegistryCoreTools(t *testing.T) {
	if len(CoreTools) < 20 {
		t.Errorf("CoreTools count dropped: %d (was >=25)", len(CoreTools))
	}
	required := []string{"read_file", "write_file", "edit_file", "json_patch", "web_fetch", "cli", "grep", "glob", "project_map", "list_skills", "use_skill", "convert", "source_acquire"}
	for _, name := range required {
		if !CoreTools[name] {
			t.Errorf("required core tool missing: %s", name)
		}
	}
}

func TestToolRegistryHiddenTools(t *testing.T) {
	if len(PromptHiddenTools) == 0 {
		t.Error("PromptHiddenTools should not be empty")
	}
}

func TestToolRegistryGetDefinitions(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&ToolDetailTool{registry: reg})

	// tool_detail is a core tool
	coreDefs := reg.GetCoreDefinitions()
	if len(coreDefs) != 1 {
		t.Errorf("expected 1 core definition, got %d", len(coreDefs))
	}

	allDefs := reg.GetDefinitions()
	if len(allDefs) != 1 {
		t.Errorf("expected 1 definition, got %d", len(allDefs))
	}
}

func TestToolDetailToolExecute(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&ToolDetailTool{registry: reg})
	tool := &ToolDetailTool{registry: reg}

	result, err := tool.Execute(nil, map[string]interface{}{"tool_name": "tool_detail"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "tool_detail") {
		t.Error("result should contain tool_detail")
	}

	_, err = tool.Execute(nil, map[string]interface{}{"tool_name": "nonexistent"})
	if err == nil {
		t.Error("should error for nonexistent tool")
	}
}

// ====================== Rate Limiter ======================

func TestRateLimiterAllow(t *testing.T) {
	rl := gatewayhttp.NewRateLimiter(60, 10)

	// Initial burst should all be allowed
	for i := 0; i < 10; i++ {
		if !rl.Allow("test") {
			t.Errorf("burst request %d should be allowed", i+1)
		}
	}

	// 11th request should be denied (bucket exhausted)
	if rl.Allow("test") {
		t.Error("11th request should be denied (bucket empty)")
	}
}

func TestRateLimiterPerKey(t *testing.T) {
	rl := gatewayhttp.NewRateLimiter(60, 10)

	// Exhaust key1
	for i := 0; i < 10; i++ {
		rl.Allow("key1")
	}

	// key2 should still have full quota
	if !rl.Allow("key2") {
		t.Error("key2 should have its own bucket")
	}
}

// ====================== isNewer ======================

func TestIsNewer(t *testing.T) {
	tests := []struct {
		newer, current string
		want           bool
	}{
		{"1.1.0", "1.0.0", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"v1.1.0", "v1.0.0", true},
		{"v2.0", "v1.9.9", true},
		{"1.10.0", "1.9.0", true},
		{"1.0", "1.0.0", false},
		{"1.0.0", "1.0", true},
	}
	for _, tt := range tests {
		got := operationstools.IsNewer(tt.newer, tt.current)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.newer, tt.current, got, tt.want)
		}
	}
}

// ====================== stripToolCalls ======================

func TestStripToolCalls(t *testing.T) {
	input := "Hello\n<tool_call>some function call</tool_call>\nWorld"
	got := stripToolCalls(input)
	if strings.Contains(got, "tool_call") {
		t.Errorf("tool_call tag not stripped: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("content lost: %q", got)
	}
}

func TestStripToolCallsNoTags(t *testing.T) {
	input := "Plain text without any tags."
	got := stripToolCalls(input)
	if got != input {
		t.Errorf("plain text modified: %q", got)
	}
}

func TestStripToolCallsCollapseNewlines(t *testing.T) {
	input := "Line1\n\n\n\n\n\nLine2"
	got := stripToolCalls(input)
	if strings.Count(got, "\n") > 3 {
		t.Errorf("newlines not collapsed: %d newlines in %q", strings.Count(got, "\n"), got)
	}
}

// ====================== Guardrails ======================

func TestIsReadOnlyTool(t *testing.T) {
	readOnly := []string{"read_file", "list_dir", "grep", "glob", "snapshot", "search_history", "knowledge_query", "list_skills", "get_stats"}
	for _, name := range readOnly {
		if !isReadOnlyTool(name) {
			t.Errorf("%s should be read-only", name)
		}
	}
	if isReadOnlyTool("write_file") {
		t.Error("write_file should NOT be read-only")
	}
	if isReadOnlyTool("cli") {
		t.Error("cli should NOT be read-only")
	}
}

func TestClassifyAPIError(t *testing.T) {
	tests := []struct {
		errMsg    string
		retryable bool
		category  string
	}{
		{"401 Unauthorized", false, "auth"},
		{"invalid API key", false, "auth"},
		{"402 quota exceeded", false, "billing"},
		{"429 Too Many Requests", true, "rate_limit"},
		{"context length exceeded", true, "context_overflow"},
		{"connection timeout", true, "timeout"},
		{"500 Internal Server Error", true, "server_error"},
		{"unknown error XYZ", true, "unknown"},
	}
	for _, tt := range tests {
		retryable, category := classifyAPIError(tt.errMsg)
		if retryable != tt.retryable || category != tt.category {
			t.Errorf("classifyAPIError(%q) = (%v, %q), want (%v, %q)",
				tt.errMsg, retryable, category, tt.retryable, tt.category)
		}
	}
}

func TestFormatAPIErrorHint(t *testing.T) {
	t.Skip("formatAPIErrorHint not yet implemented")
}

func TestToolResultPreview(t *testing.T) {
	out := toolResultPreview("cli", "line1\nline2\nline3\nexit status 1: error")
	if !strings.Contains(out, "[cli]") {
		t.Errorf("cli preview broken: %q", out)
	}
	out = toolResultPreview("grep", "match1\nmatch2")
	if !strings.Contains(out, "[grep]") {
		t.Errorf("grep preview broken: %q", out)
	}
	out = toolResultPreview("unknown_tool", "short output")
	if out != "short output" {
		t.Error("short unknown tool result should be kept as-is")
	}
}

// ====================== Utilities ======================

func TestMin(t *testing.T) {
	if min(1, 2) != 1 {
		t.Error("min(1,2) should be 1")
	}
	if min(5, 3) != 3 {
		t.Error("min(5,3) should be 3")
	}
	if min(0, 0) != 0 {
		t.Error("min(0,0) should be 0")
	}
}

func TestTruncate(t *testing.T) {
	// truncate counts words or characters via countWordsAndChars
	// Returns original if count <= maxLen, otherwise appends truncated marker

	// "Hello World" = 2 words, so it should not be truncated
	s := "Hello World"
	if truncate(s, 5) != s {
		t.Errorf("truncate(%q, 5) should return original (2 words <= 5)", s)
	}

	// "abcdefghij" = 1 word unit, so it should not be truncated
	if truncate("abcdefghij", 3) != "abcdefghij" {
		t.Errorf("truncate 1-word with maxLen=3 should return original")
	}

	// CJK: "浣犲ソ涓栫晫" = 4 chars, 4 > 2 鈫?truncated with info suffix
	r := truncate("浣犲ソ涓栫晫", 2)
	if !strings.Contains(r, "浣犲ソ") {
		t.Errorf("truncate CJK should contain first 2 chars, got %q", r)
	}
	if !strings.Contains(r, "truncated") {
		t.Errorf("truncate should append truncation info, got %q", r)
	}
}

func TestIsWithinWorkingHours(t *testing.T) {
	// Empty string means no working hours configured, defaults to outside hours
	if aiemployee.IsWithinWorkingHours("") {
		t.Error("empty hours should return false (no matching window)")
	}

	now := time.Now()
	// Build a segment that covers current time 卤 2 minutes
	start := now.Add(-2 * time.Minute).Format("15:04")
	end := now.Add(2 * time.Minute).Format("15:04")
	segment := start + "-" + end
	if !aiemployee.IsWithinWorkingHours(segment) {
		t.Errorf("current time should be within %s", segment)
	}

	// Past segment should not match
	if aiemployee.IsWithinWorkingHours("03:00-03:30") {
		t.Log("unexpected: 03:00-03:30 matched (possible if running at ~3am)")
	}

	// Two segments, one of which covers now.
	multiSegment := "03:00-03:30," + segment
	if !aiemployee.IsWithinWorkingHours(multiSegment) {
		t.Errorf("should be within %s", multiSegment)
	}
}

// ====================== Pipeline Templates ======================

func TestGetPipelineTemplatesNotEmpty(t *testing.T) {
	templates := GetPipelineTemplates()
	if len(templates) == 0 {
		t.Fatal("pipeline templates should not be empty")
	}
	t.Logf("Pipeline templates: %d", len(templates))
}

func TestPipelineTemplatesHaveRequiredFields(t *testing.T) {
	for name, tmpl := range GetPipelineTemplates() {
		if tmpl.Name == "" {
			t.Errorf("template %q has empty name", name)
		}
		if tmpl.Description == "" {
			t.Errorf("template %q has empty description", name)
		}
		if len(tmpl.Steps) == 0 {
			t.Errorf("template %q has no steps", name)
		}
		for i, step := range tmpl.Steps {
			if step.Name == "" {
				t.Errorf("template %q step[%d] has empty name", name, i)
			}
			if step.Prompt == "" {
				t.Errorf("template %q step[%d] has empty prompt", name, i)
			}
			if step.MaxIter == 0 {
				t.Errorf("template %q step[%d] has zero MaxIter", name, i)
			}
		}
	}
}

func TestPipelineTemplatesKnownTemplates(t *testing.T) {
	required := []string{
		"ticket_resolution", "contract_review", "daily_report",
	}
	for _, name := range required {
		if _, ok := GetPipelineTemplates()[name]; !ok {
			t.Errorf("required pipeline template missing: %s", name)
		}
	}
}

func TestShouldPersistAssistantCompletion(t *testing.T) {
	if shouldPersistAssistantCompletion("ae", "pipe_direct_meeting-secretary_20260630_032857-de") {
		t.Error("ae task should not persist assistant completion twice")
	}
	if shouldPersistAssistantCompletion("chat", "pipe_direct_meeting-secretary_20260630_032857-de") {
		t.Error("-de task should not persist assistant completion twice")
	}
	if !shouldPersistAssistantCompletion("chat", "ws_123456") {
		t.Error("normal chat task should persist assistant completion")
	}
}

func TestInvestmentAdvisoryPipelineActiveStructuredWorkflow(t *testing.T) {
	if retiredBuiltinPipelines()["investment-advisory"] {
		t.Fatal("investment-advisory should be active")
	}
	for _, name := range []string{"lead-acquisition", "lead-research", "wide-research", "data-analysis-pipeline", "sales-support", "product-management"} {
		if retiredBuiltinPipelines()[name] {
			t.Fatalf("%s should remain active", name)
		}
	}

	pipeline := (&GatewayService{}).defaultPipelineSet()["investment-advisory"]
	wantSteps := []string{"自主判断与证据采集", "分类型研究与研判", "投研合成与质量门", "结果交付"}
	if len(pipeline.Steps) != len(wantSteps) {
		t.Fatalf("investment-advisory steps = %d, want %d", len(pipeline.Steps), len(wantSteps))
	}
	for i, want := range wantSteps {
		if pipeline.Steps[i].Name != want {
			t.Fatalf("step[%d] = %q, want %q", i, pipeline.Steps[i].Name, want)
		}
	}
	if pipeline.Steps[0].NoTools || pipeline.Steps[0].MaxIter > 5 {
		t.Fatalf("自主采集 step should be bounded and tool-enabled: %#v", pipeline.Steps[0])
	}
	for _, want := range []string{"stock_facts", "同一 as_of", "最新可获得交易日", "达到足够覆盖后停止", "transparent_inference"} {
		if !strings.Contains(pipeline.Steps[0].Prompt, want) {
			t.Fatalf("自主采集 prompt missing %q", want)
		}
	}
	collectionTools := strings.Join(pipeline.Steps[0].Tools, ",")
	for _, want := range []string{"stock_facts", "web_search", "web_fetch", "platform_search"} {
		if !strings.Contains(collectionTools, want) {
			t.Fatalf("自主采集 tools missing %q: %v", want, pipeline.Steps[0].Tools)
		}
	}
	if !pipeline.Steps[1].NoTools || !pipeline.Steps[1].ContinueOnError || len(pipeline.Steps[1].Parallel) != 0 || pipeline.Steps[1].MaxIter != 1 || len(pipeline.Steps[1].Tools) != 0 {
		t.Fatalf("分类型研究与研判 should be one coherent degradable analysis call: %#v", pipeline.Steps[1])
	}
	if pipeline.Steps[1].OutputContract == nil || pipeline.Steps[1].OutputContract.MinChars != 240 || len(pipeline.Steps[1].OutputContract.RequiredSections) != 0 {
		t.Fatalf("分类型研究与研判 should use an adaptive output contract: %#v", pipeline.Steps[1].OutputContract)
	}
	if !pipeline.Steps[2].NoTools || !pipeline.Steps[2].ContinueOnError || pipeline.Steps[2].MaxIter != 1 {
		t.Fatalf("投研合成与质量门 should be one bounded degradable model call: %#v", pipeline.Steps[2])
	}
	deliveryTools := strings.Join(pipeline.Steps[3].Tools, ",")
	if strings.Contains(deliveryTools, "read_file") || !strings.Contains(deliveryTools, "write_file") {
		t.Fatalf("交付 step should write once without a redundant read, tools=%v", pipeline.Steps[3].Tools)
	}
	combinedPrompts := pipeline.Steps[1].Prompt + pipeline.Steps[2].Prompt + pipeline.Steps[3].Prompt
	for _, want := range []string{"只分析当前意图真正需要的维度", "market_brief", "instrument_analysis", "sector_analysis", "fund_etf_analysis", "event_explanation", "personalized_position", "不用固定六章节凑完整", "默认直接交付"} {
		if !strings.Contains(combinedPrompts, want) {
			t.Fatalf("adaptive investment prompt missing %q", want)
		}
	}
	for _, step := range pipeline.Steps {
		if step.ProfessionalAgent != nil {
			t.Fatalf("unified investment advisor must not call professional-agent facades: %#v", step.ProfessionalAgent)
		}
	}
}

// ====================== DE Templates ======================

func TestGetAETemplatesNotEmpty(t *testing.T) {
	templates := aiemployee.Templates()
	if len(templates) < 5 {
		t.Fatalf("expected at least 5 DE templates, got %d", len(templates))
	}
	t.Logf("DE templates: %d", len(templates))
}

func TestApplyTemplate(t *testing.T) {
	cfg := &AIEmployeeConfig{}
	err := aiemployee.ApplyTemplate("customer_service", cfg)
	if err != nil {
		t.Fatalf("ApplyTemplate: %v", err)
	}
	if cfg.SystemMessage == "" {
		t.Error("SystemMessage should be set by template")
	}
	if cfg.Direct == nil {
		t.Error("Direct config should be set by template")
	}

	err = aiemployee.ApplyTemplate("nonexistent_template", &AIEmployeeConfig{})
	if err == nil {
		t.Error("should error for nonexistent template")
	}
}

// ====================== Default DEs ======================

func TestDefaultAIEmployeesCount(t *testing.T) {
	des := defaultAIEmployees()
	if len(des) < 28 {
		t.Errorf("default DE count: %d (expected 28+)", len(des))
	}

	var community, commercial int
	for _, de := range des {
		switch de.Tier {
		case "commercial":
			commercial++
		default:
			community++ // empty or "community" both mean free tier
		}
	}
	t.Logf("Default DEs: %d total (%d community, %d commercial)", len(des), community, commercial)
	if community < 12 {
		t.Errorf("community DEs: %d (expected 12+)", community)
	}
	if commercial < 16 {
		t.Errorf("commercial DEs: %d (expected 16+)", commercial)
	}
}

func TestDefaultAIEmployeesHaveRequiredFields(t *testing.T) {
	for _, de := range defaultAIEmployees() {
		if de.ID == "" {
			t.Error("DE has empty ID")
		}
		if de.Name == "" {
			t.Errorf("DE %s has empty Name", de.ID)
		}
		if de.AtName == "" {
			t.Errorf("DE %s has empty AtName", de.ID)
		}
		if de.Pipeline == "" && de.Direct == nil {
			t.Errorf("DE %s has no Pipeline or Direct", de.ID)
		}
		if de.Tier != "" && de.Tier != "community" && de.Tier != "commercial" {
			t.Errorf("DE %s has invalid tier: %s", de.ID, de.Tier)
		}
	}
}

func TestMeetingSecretaryReferenceTools(t *testing.T) {
	employees := defaultAIEmployees()
	var meetingSecretary *AIEmployeeConfig
	for i := range employees {
		if employees[i].ID == "meeting-secretary" {
			meetingSecretary = &employees[i]
			break
		}
	}
	if meetingSecretary == nil {
		t.Fatal("meeting-secretary not found")
	}
	if meetingSecretary.Direct == nil {
		t.Fatal("meeting-secretary should use Direct config")
	}
	required := map[string]bool{"extract": false, "read_file": false, "write_file": false}
	for _, tool := range meetingSecretary.Direct.Tools {
		if _, ok := required[tool]; ok {
			required[tool] = true
		}
	}
	for name, ok := range required {
		if !ok {
			t.Errorf("meeting-secretary missing tool: %s", name)
		}
	}
}

func TestDefaultDEUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, de := range defaultAIEmployees() {
		if seen[de.ID] {
			t.Errorf("duplicate DE ID: %s", de.ID)
		}
		seen[de.ID] = true
	}
}

// ====================== DE Manager ======================

func TestDEManagerRegister(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(os.Stderr, "", 0))
	mgr.Register([]AIEmployeeConfig{
		{ID: "test-de", Name: "测试员工", AtName: "测试", Pipeline: "test-pipeline", Tier: "community"},
	})

	if _, ok := mgr.employees["test-de"]; !ok {
		t.Error("DE not registered by ID")
	}
	if _, ok := mgr.byAtName["测试"]; !ok {
		t.Error("DE not registered by AtName")
	}
}

func TestDEManagerMatchAtName(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(os.Stderr, "", 0))
	mgr.Register([]AIEmployeeConfig{
		{ID: "de-1", Name: "investment advisor", AtName: "investment-advisor", Pipeline: "investment", Tier: "community"},
	})

	cfg, task := mgr.MatchAtName("@investment-advisor analyze stock")
	if cfg == nil {
		t.Fatal("should match @investment-advisor")
	}
	if cfg.ID != "de-1" {
		t.Errorf("matched wrong DE: %s", cfg.ID)
	}
	if !strings.Contains(task, "analyze") {
		t.Errorf("task extraction wrong: %q", task)
	}

	cfg, _ = mgr.MatchAtName("just chatting")
	if cfg != nil {
		t.Error("should not match non-existent DE")
	}
}

// ====================== Config ======================

func TestRateLimiterDefaults(t *testing.T) {
	rl := gatewayhttp.NewRateLimiter(60, 10)
	for i := 0; i < 10; i++ {
		if !rl.Allow("defaults") {
			t.Fatalf("default burst request %d should be allowed", i+1)
		}
	}
	if rl.Allow("defaults") {
		t.Error("request beyond configured burst should be denied")
	}
}

// ====================== Channel helpers ======================

func TestGetStringHelper(t *testing.T) {
	m := map[string]interface{}{"key": "value", "num": 42}
	if getString(m, "key", "default") != "value" {
		t.Error("getString should return existing value")
	}
	if getString(m, "missing", "default") != "default" {
		t.Error("getString should return default")
	}
}

func TestGetBoolHelper(t *testing.T) {
	m := map[string]interface{}{"enabled": true}
	if !getBool(m, "enabled", false) {
		t.Error("getBool should return true")
	}
	if getBool(m, "missing", true) != true {
		t.Error("getBool should return default")
	}
}

func TestGetIntHelper(t *testing.T) {
	m := map[string]interface{}{"count": 42}
	if getInt(m, "count", 0) != 42 {
		t.Error("getInt should return existing value")
	}
	if getInt(m, "missing", 10) != 10 {
		t.Error("getInt should return default")
	}
}

// ====================== New AI Coding Pipelines ======================

func TestDefaultPipelineSetNewPipelines(t *testing.T) {
	// Create minimal GatewayService to call defaultPipelineSet
	g := &GatewayService{}
	pipelines := g.defaultPipelineSet()

	newPipelines := []string{
		"daily-standup", "pr-review", "release-manager",
		"code-ingest", "code-qa",
		"onboarding-walkthrough",
		"auto-remediation", "deploy-rollback-decision",
	}
	for _, name := range newPipelines {
		p, ok := pipelines[name]
		if !ok {
			t.Errorf("new pipeline missing: %s", name)
			continue
		}
		if len(p.Steps) == 0 {
			t.Errorf("pipeline %s has no steps", name)
		}
		t.Logf("  %s: %s (%d steps)", name, p.Desc, len(p.Steps))
	}
}

func TestSystemPromptCodingProtocol(t *testing.T) {
	prompt := systemPromptStatic(true)
	mustContain := []string{
		"## Code Development Protocol",
		"Read before changing",
		"Search efficiently",
		"Keep changes narrow",
		"Verify honestly",
		"for unfamiliar projects, call project_map first",
		"Analyze (project_map+grep+read_file)",
		"For ordinary codebase edits, start by reading and searching the repository instead of doing skill discovery.",
	}
	for _, want := range mustContain {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "Always call `list_skills <keyword>` first") {
		t.Fatal("system prompt should not force list_skills before ordinary code edits")
	}
}

func TestDefaultAIEmployeesNewDEs(t *testing.T) {
	des := defaultAIEmployees()
	idMap := map[string]bool{}
	for _, de := range des {
		idMap[de.ID] = true
	}

	newDEs := []string{"mentor-buddy", "code-review-bot"}
	for _, id := range newDEs {
		if !idMap[id] {
			t.Errorf("new DE missing: %s", id)
		}
	}

	// Verify project-assistant role updated
	for _, de := range des {
		if de.ID == "project-assistant" {
			if !strings.Contains(de.Role, "站会") {
				t.Error("project-assistant role should mention 站会")
			}
			if !strings.Contains(de.Role, "PR审查") {
				t.Error("project-assistant role should mention PR审查")
			}
		}
		if de.ID == "it-ops" {
			if !strings.Contains(de.Role, "自动修复") {
				t.Error("it-ops role should mention 自动修复")
			}
		}
	}
}
