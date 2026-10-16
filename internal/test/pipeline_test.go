package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skilldomain "mac/internal/skills"
)

// ====================== renderStepPrompt tests ======================

func TestRenderStepPrompt_Basic(t *testing.T) {
	steps := []StepResult{
		{Name: "step0", Output: "file1.go, file2.go"},
		{Name: "step1", Output: "found 3 issues"},
	}

	result := renderStepPrompt("Check ${step0.output} for ${step1.output}", 2, steps, "原始输入")
	if !strings.Contains(result, "file1.go") || !strings.Contains(result, "found 3 issues") {
		t.Errorf("renderStepPrompt failed: %s", result)
	}
}

func TestRenderStepPrompt_Shorthand(t *testing.T) {
	steps := []StepResult{
		{Name: "scan", Output: "result from scan"},
	}
	result := renderStepPrompt("Based on ${step0}", 1, steps, "input")
	if !strings.Contains(result, "result from scan") {
		t.Errorf("shorthand failed: %s", result)
	}
}

func TestRenderStepPrompt_InputVariable(t *testing.T) {
	result := renderStepPrompt("Task: ${input}", 0, nil, "分析安全漏洞")
	if result != "Task: 分析安全漏洞" {
		t.Errorf("${input} substitution failed: %s", result)
	}
}

func TestStepToolBoundaryInstructionListsAllowedTools(t *testing.T) {
	step := &PipelineStep{
		Name:  "PPT报告",
		Tools: []string{"read_file", "write_file", "persistent_shell"},
	}
	got := stepToolBoundaryInstruction(step)
	for _, want := range []string{
		"本步骤只允许调用这些工具",
		"read_file",
		"write_file",
		"persistent_shell",
		"文件必须保存到当前任务工作区内",
		".openkitty/storage/...",
		"不要使用全局目录、其他用户目录",
		"不要调用未列出的工具",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool boundary should contain %q, got %q", want, got)
		}
	}
}

func TestShouldRetryPipelineStepErrorSkipsToolMissing(t *testing.T) {
	if shouldRetryPipelineStepError(errors.New("工具 edit_file 不存在")) {
		t.Fatal("missing tool errors should not retry the whole step")
	}
	if !shouldRetryPipelineStepError(errors.New("timeout waiting for model response")) {
		t.Fatal("transient timeout should be retryable")
	}
}

func TestPipelineAuditToolPreservesReadFileCapabilities(t *testing.T) {
	workspace := t.TempDir()
	inner := NewReadFileTool(workspace)
	wrapped := &auditTool{inner: inner}
	if got := wrapped.MaxResultSize(); got != 0 {
		t.Fatalf("audit-wrapped read_file max result = %d, want unlimited (0)", got)
	}
	if !wrapped.IsReadOnly() {
		t.Fatal("audit-wrapped read_file should remain read-only")
	}
	path := wrapped.ScopePath(map[string]interface{}{"path": "source.json"})
	if path != filepath.Join(workspace, "source.json") {
		t.Fatalf("audit-wrapped read_file scope = %q", path)
	}
	if !wrapped.ResultIsContentPayload() {
		t.Fatal("audit-wrapped read_file should preserve content-payload semantics")
	}
}

func TestPipelineAuditToolPreservesListSkillsContentPayload(t *testing.T) {
	workspace := t.TempDir()
	loader := skilldomain.NewSkillsLoader(workspace, "test", "")
	loader.RegisterSkill("recovery-guide", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{
			Name:        "recovery-guide",
			Description: "Handle exception, fatal failure, and blocked workflow recovery.",
		},
		Source: "builtin",
		Path:   "builtin://recovery-guide",
	})

	wrapped := &auditTool{inner: NewListSkillsTool(loader)}
	if !wrapped.ResultIsContentPayload() {
		t.Fatal("audit-wrapped list_skills should preserve content-payload semantics")
	}
	registry := NewToolRegistry()
	registry.Register(wrapped)
	result, err := registry.ExecuteRaw(context.Background(), ToolCall{
		ID:        "catalog",
		Name:      "list_skills",
		Arguments: map[string]interface{}{"keywords": []interface{}{"failure"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ToolResultExecutionSucceeded(result, nil) {
		t.Fatalf("audit-wrapped catalog prose was misclassified as failure: %+v", result)
	}
}

func TestRenderStepPrompt_PreviousStepsOnly(t *testing.T) {
	// step2 should NOT have access to step2's own output
	steps := []StepResult{
		{Name: "a", Output: "A_OUT"},
		{Name: "b", Output: "B_OUT"},
		{Name: "c", Output: "C_OUT"},
	}
	// At stepIndex=2, should see step0 and step1, but NOT step2
	result := renderStepPrompt("${step0} ${step1} ${step2}", 2, steps, "in")
	if strings.Contains(result, "C_OUT") {
		t.Errorf("step2 should not see its own output: %s", result)
	}
	if !strings.Contains(result, "A_OUT") || !strings.Contains(result, "B_OUT") {
		t.Errorf("should see previous steps: %s", result)
	}
}

func TestBuildPreviousStepContextIncludesSearchToolEvidence(t *testing.T) {
	steps := []StepResult{
		{
			Name:   "搜索研究",
			Status: "completed",
			Output: "阶段摘要未整理具体公司。",
			ToolCalls: []ToolCallRecord{
				{
					ToolName: "read_file",
					Params:   `{"path":"README.md"}`,
					Result:   "local project context",
				},
				{
					ToolName: "web_fetch",
					Params:   `{"engine":"sogou","query":"金融风控 机器学习平台 招标"}`,
					Result:   "1. 龙江银行数字风控平台项目-公开招标公告\n   URL: https://example.com/bid",
				},
			},
		},
	}

	context := buildPreviousStepContext(1, steps)
	if !strings.Contains(context, "工具调用证据摘录") {
		t.Fatalf("expected tool evidence section, got %s", context)
	}
	if !strings.Contains(context, "自动候选线索摘要") {
		t.Fatalf("expected lead digest section, got %s", context)
	}
	if !strings.Contains(context, "龙江银行数字风控平台项目") {
		t.Fatalf("expected search result evidence, got %s", context)
	}
	if strings.Contains(context, "local project context") {
		t.Fatalf("non-evidence local read_file result should be filtered, got %s", context)
	}
}

func TestPipelineExecutorListExecutionsSortedNewestFirst(t *testing.T) {
	executor := NewPipelineExecutor(nil, nil, log.New(os.Stdout, "", 0))
	now := time.Now()
	executor.executions["old"] = &PipelineExecution{ID: "old", CreatedAt: now.Add(-2 * time.Hour)}
	executor.executions["new"] = &PipelineExecution{ID: "new", CreatedAt: now}
	executor.executions["mid"] = &PipelineExecution{ID: "mid", CreatedAt: now.Add(-1 * time.Hour)}

	execs := executor.ListExecutions(10)
	if len(execs) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(execs))
	}
	got := []string{execs[0].ID, execs[1].ID, execs[2].ID}
	want := []string{"new", "mid", "old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execution order = %v, want %v", got, want)
		}
	}
}

func TestShouldCacheWebFetchResultUsesSharedFailureMarkers(t *testing.T) {
	if shouldCacheWebFetchResult("Tool Result\nstatus: failed_compacted\nsummary: failed") {
		t.Fatal("failed compacted result should not be cached")
	}
	if shouldCacheWebFetchResult("Tool call rejected [invalid_args]: bad url") {
		t.Fatal("invalid tool call result should not be cached")
	}
	if shouldCacheWebFetchResult("error fetching https://example.com") {
		t.Fatal("fetch error should not be cached")
	}
	if !shouldCacheWebFetchResult("Fetched page successfully\n# Example") {
		t.Fatal("successful fetch result should be cacheable")
	}
}

func TestPipelineExecutorListExecutionsAppliesLimitAfterSorting(t *testing.T) {
	executor := NewPipelineExecutor(nil, nil, log.New(os.Stdout, "", 0))
	now := time.Now()
	executor.executions["old"] = &PipelineExecution{ID: "old", CreatedAt: now.Add(-2 * time.Hour)}
	executor.executions["new"] = &PipelineExecution{ID: "new", CreatedAt: now}

	execs := executor.ListExecutions(1)
	if len(execs) != 1 || execs[0].ID != "new" {
		t.Fatalf("limit should keep newest execution, got %+v", execs)
	}
}

// ====================== PipelineConfig tests ======================

func TestPipelineConfig_JSONRoundTrip(t *testing.T) {
	pc := PipelineConfig{
		Triggers: []PipelineTrigger{
			{When: "安全审查", Pipeline: "security-audit", Channel: "web"},
		},
		Pipelines: map[string]Pipeline{
			"security-audit": {
				Name: "security-audit",
				Desc: "安全检查流程",
				Steps: []PipelineStep{
					{Name: "scan", Prompt: "Scan ${input}", Tools: []string{"read", "grep"}, MaxIter: 5, RequireApproval: true, ContinueOnError: true},
					{Name: "report", Prompt: "基于 ${step0.output} 生成报告", Tools: []string{"write"}, MaxIter: 3},
				},
			},
		},
	}

	data, err := json.Marshal(pc)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded PipelineConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Triggers) != 1 || decoded.Triggers[0].Pipeline != "security-audit" {
		t.Errorf("trigger mismatch")
	}
	if p, ok := decoded.Pipelines["security-audit"]; !ok || len(p.Steps) != 2 {
		t.Errorf("pipeline mismatch")
	}
	if decoded.Pipelines["security-audit"].Steps[0].RequireApproval != true {
		t.Errorf("require_approval not preserved")
	}
	if decoded.Pipelines["security-audit"].Steps[0].ContinueOnError != true {
		t.Errorf("continue_on_error not preserved")
	}
}

// ====================== buildStepRegistry tests ======================

func TestBuildStepRegistry_WhitelistOnly(t *testing.T) {
	fullReg := NewToolRegistry()
	fullReg.Register(&mockTool{name: "read_file", desc: "Read a file"})
	fullReg.Register(&mockTool{name: "grep", desc: "Search text"})
	fullReg.Register(&mockTool{name: "write_file", desc: "Write a file"})
	fullReg.Register(&mockTool{name: "cli", desc: "Run command"})

	llmAgent := &LLMAgent{toolRegistry: fullReg}
	af := &AgentFactory{agents: map[string]Agent{"default": llmAgent}}
	e := &PipelineExecutor{agentFactory: af, fileLogger: testLogger}

	reg := e.buildStepRegistry([]string{"read_file", "grep"}, nil, -1)

	if _, ok := reg.Get("read_file"); !ok {
		t.Error("read_file should be in registry")
	}
	if _, ok := reg.Get("grep"); !ok {
		t.Error("grep should be in registry")
	}
	if _, ok := reg.Get("write_file"); ok {
		t.Error("write_file should NOT be in registry")
	}
	if _, ok := reg.Get("cli"); ok {
		t.Error("cli should NOT be in registry")
	}
}

func TestBuildStepRegistry_DefaultAllExceptBlacklist(t *testing.T) {
	fullReg := NewToolRegistry()
	fullReg.Register(&mockTool{name: "read_file", desc: "Read a file"})
	fullReg.Register(&mockTool{name: "write_file", desc: "Write a file"})
	fullReg.Register(&mockTool{name: "persistent_shell", desc: "Run shell"})
	fullReg.Register(&mockTool{name: "delete_file", desc: "Delete file"})

	llmAgent := &LLMAgent{toolRegistry: fullReg}
	af := &AgentFactory{agents: map[string]Agent{"default": llmAgent}}
	e := &PipelineExecutor{agentFactory: af, fileLogger: testLogger}

	reg := e.buildStepRegistry(nil, nil, -1)

	if _, ok := reg.Get("read_file"); !ok {
		t.Error("read_file should be in default registry")
	}
	if _, ok := reg.Get("write_file"); !ok {
		t.Error("write_file should be in default registry")
	}
	if _, ok := reg.Get("persistent_shell"); ok {
		t.Error("persistent_shell should be blocked by pipeline blacklist")
	}
	if _, ok := reg.Get("delete_file"); ok {
		t.Error("delete_file should be blocked by pipeline blacklist")
	}
}

func TestBuildStepRegistry_EmptyWhitelist(t *testing.T) {
	fullReg := NewToolRegistry()
	fullReg.Register(&mockTool{name: "read_file", desc: "Read"})

	llmAgent := &LLMAgent{toolRegistry: fullReg}
	af := &AgentFactory{agents: map[string]Agent{"default": llmAgent}}
	e := &PipelineExecutor{agentFactory: af, fileLogger: testLogger}

	reg := e.buildStepRegistry([]string{}, nil, -1)

	if len(reg.ListNames()) > 0 {
		t.Errorf("empty whitelist should yield empty registry, got %v", reg.ListNames())
	}
}

func TestBuildStepRegistry_PersistentShellAliasUsesCLI(t *testing.T) {
	fullReg := NewToolRegistry()
	fullReg.Register(&mockTool{name: "cli", desc: "Run command"})

	llmAgent := &LLMAgent{toolRegistry: fullReg}
	af := &AgentFactory{agents: map[string]Agent{"default": llmAgent}}
	e := &PipelineExecutor{agentFactory: af, fileLogger: testLogger}

	reg := e.buildStepRegistry([]string{"persistent_shell"}, nil, -1)

	tool, ok := reg.Get("persistent_shell")
	if !ok {
		t.Fatal("persistent_shell alias should be registered when cli is available")
	}
	if tool.Name() != "persistent_shell" {
		t.Fatalf("alias should expose persistent_shell name, got %q", tool.Name())
	}
	if _, ok := reg.Get("cli"); ok {
		t.Fatal("explicit persistent_shell allowlist should not expose cli as an extra tool")
	}
	defs := reg.GetCoreDefinitions()
	found := false
	for _, def := range defs {
		fn, _ := def["function"].(map[string]interface{})
		if fn["name"] == "persistent_shell" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("persistent_shell alias should be exposed in core tool definitions")
	}
}

func TestStepToolAllowlistUsesConfiguredTools(t *testing.T) {
	configured := stepToolAllowlist(&PipelineStep{Tools: []string{"web_search", "lead_save"}})
	want := []string{"web_search", "lead_save", "web_targets", "web_navigate", "web_page", "web_close"}
	if len(configured) != len(want) {
		t.Fatalf("configured allowlist = %v", configured)
	}
	for i, name := range want {
		if configured[i] != name {
			t.Fatalf("configured allowlist[%d]=%q, want %q; full=%v", i, configured[i], name, configured)
		}
	}

	noTools := stepToolAllowlist(&PipelineStep{NoTools: true, Tools: []string{"web_search"}})
	if noTools == nil || len(noTools) != 0 {
		t.Fatalf("no-tools allowlist = %v, want empty non-nil slice", noTools)
	}

	defaultTools := stepToolAllowlist(&PipelineStep{})
	if defaultTools != nil {
		t.Fatalf("default allowlist = %v, want nil for all tools", defaultTools)
	}
}

func TestStepToolAllowlistDoesNotExpandLocalOnlyTools(t *testing.T) {
	configured := stepToolAllowlist(&PipelineStep{Tools: []string{"read_file", "write_file"}})
	want := []string{"read_file", "write_file"}
	if len(configured) != len(want) {
		t.Fatalf("configured allowlist = %v", configured)
	}
	for i, name := range want {
		if configured[i] != name {
			t.Fatalf("configured allowlist[%d]=%q, want %q", i, configured[i], name)
		}
	}
}

func TestExpandWebResearchToolsPreservesExistingBrowserTools(t *testing.T) {
	configured := expandWebResearchTools([]string{"web_fetch", "web_page", "write_file"})
	want := []string{"web_fetch", "web_page", "write_file", "web_targets", "web_navigate", "web_close"}
	if len(configured) != len(want) {
		t.Fatalf("expanded tools = %v", configured)
	}
	for i, name := range want {
		if configured[i] != name {
			t.Fatalf("expanded tools[%d]=%q, want %q; full=%v", i, configured[i], name, configured)
		}
	}
}

func TestBuildStepRegistryAddsBrowserToolsForWebResearch(t *testing.T) {
	fullReg := NewToolRegistry()
	for _, name := range []string{"web_search", "web_targets", "web_navigate", "web_page", "web_close"} {
		fullReg.Register(&mockTool{name: name, desc: name})
	}
	llmAgent := &LLMAgent{toolRegistry: fullReg}
	af := &AgentFactory{agents: map[string]Agent{"default": llmAgent}}
	e := &PipelineExecutor{agentFactory: af, fileLogger: testLogger}

	allowed := stepToolAllowlist(&PipelineStep{Tools: []string{"web_search"}})
	reg := e.buildStepRegistry(allowed, nil, -1)

	for _, name := range []string{"web_search", "web_targets", "web_navigate", "web_page", "web_close"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("%s should be registered for web research steps; got %v", name, reg.ListNames())
		}
	}
}

func TestBuildStepExecutionPromptTreatsInputAsBackground(t *testing.T) {
	prompt := buildStepExecutionPrompt("只做数据加载。")

	for _, want := range []string{
		"## 当前步骤唯一任务",
		"只做数据加载。",
		"## 原始用户需求（仅作为整体背景）",
		"只完成“当前步骤唯一任务”",
		"不要提前执行后续步骤",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %s", want, prompt)
		}
	}
}

func TestBuildStepExecutionPromptForbidsProcessNarration(t *testing.T) {
	prompt := buildStepExecutionPrompt("load data")

	for _, want := range []string{
		"\u6211\u9700\u8981/\u8ba9\u6211/\u6211\u5148",
		"\u76f4\u63a5\u5411\u7528\u6237\u8bf4\u660e\u7f3a\u5c11\u4ec0\u4e48",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain anti-process rule %q, got %s", want, prompt)
		}
	}
}

func TestBuildStepExecutionPromptPreservesUserScopeLimits(t *testing.T) {
	prompt := buildStepExecutionPrompt("generate report")

	for _, want := range []string{
		"任务身份坐标",
		"地区、行业、企业规模、时间和数量是筛选维度，不属于身份坐标",
		"地区、行业、企业规模、时间、数量、风格等普通描述默认是优先条件",
		"明确数值上下限默认属于硬约束",
		"用户同时明确说“没有就放宽/类似的也可以/不足时随便”",
		"逐项放宽获准的优先条件",
		"精确匹配与放宽后的近似候选必须分层展示",
		"不得声称该顺序由用户指定",
		"\u7528\u6237\u660e\u786e\u9650\u5236\u4e3a\u51c6",
		"\u5c0f\u8303\u56f4\u9a8c\u6536/\u53ea\u7ed9\u8ba1\u5212/\u4e0d\u9700\u8981\u5b8c\u6574\u62a5\u544a",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt should preserve user scope limit %q, got %s", want, prompt)
		}
	}
}

func TestDetectPipelineTargetScopeWindowsPath(t *testing.T) {
	target := detectPipelineTargetScope(`@lead 帮我寻找E:\ai-node 目录下项目，AI平台的潜在客户`)
	if target != `E:\ai-node` {
		t.Fatalf("expected E:\\ai-node, got %q", target)
	}
}

func TestDetectPipelineTargetScopeStopsBeforeChineseSentence(t *testing.T) {
	target := detectPipelineTargetScope(`@潜客研究 在E盘下有一个E:\DEV\ai-node是我的一个项目，你帮我寻找潜在客户`)
	if target != `E:\DEV\ai-node` {
		t.Fatalf("expected E:\\DEV\\ai-node, got %q", target)
	}
}

func TestDetectPipelineTargetScopeChineseDrivePath(t *testing.T) {
	target := detectPipelineTargetScope(`@lead 帮我寻找E盘 ai-node 目录下项目，AI平台的潜在客户`)
	if target != `E:\ai-node` {
		t.Fatalf("expected E:\\ai-node, got %q", target)
	}
}

func TestPipelineTargetScopeInstructionBlocksStaleProjectContext(t *testing.T) {
	prompt := appendPipelineTargetScopeInstruction(
		buildStepExecutionPrompt("understand project"),
		`@lead 帮我寻找E盘ai-node 目录下项目，AI平台的潜在客户`,
	)
	for _, want := range []string{
		`E:\ai-node`,
		"\u4e0d\u5f97\u5f15\u7528\u5386\u53f2 session/context/memory \u4e2d\u7684\u9879\u76ee\u753b\u50cf",
		"\u8bc1\u636e\u51b2\u7a81/\u5f85\u9a8c\u8bc1",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("target scope instruction missing %q:\n%s", want, prompt)
		}
	}
}

func TestPipelineUserDeliveryConstraintInstructionKeepsSmallScope(t *testing.T) {
	out := pipelineUserDeliveryConstraintInstruction("请做一次小范围验收，先输出验证计划和3个候选方向，不需要完整全网报告。")
	for _, want := range []string{
		"\u7528\u6237\u663e\u5f0f\u4ea4\u4ed8\u7ea6\u675f\uff08\u6700\u9ad8\u4f18\u5148\u7ea7\uff09",
		"\u4e0d\u5f97\u6269\u5927\u6210\u6a21\u677f\u91cc\u7684\u5b8c\u6574\u5168\u91cf\u62a5\u544a",
		"\u5c0f\u8303\u56f4\u9a8c\u6536/\u9a8c\u8bc1\u8ba1\u5212/\u5019\u9009\u65b9\u5411/\u4e0d\u9700\u8981\u5b8c\u6574\u62a5\u544a",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("delivery constraint missing %q:\n%s", want, out)
		}
	}
}

func TestApplyPipelineDeliveryConstraintTrimsFullReportToSmallScope(t *testing.T) {
	input := "请做一次小范围验收，先输出验证计划和3个候选方向，不需要完整全网报告。"
	full := `## Saddle AI 平台 — 潜客挖掘最终报告

### 1. 项目理解与市场定位
很多完整报告内容。

### 2. AI 平台潜客优先级表
| 优先级 | 客户画像/假设候选公司 | 行业 | 需求信号（推断） |
|--------|----------------------|------|------------------|
| **P0** | **3C电子制造企业** | 电子制造 | 人工目检效率低 |
| **P0** | **钢铁/金属加工企业** | 钢铁/金属 | 表面缺陷检测需求大 |
| **P1** | **中小银行/消费金融公司** | 金融 | 信贷审批模型需快速迭代 |
| **P2** | **系统集成商/SI** | IT服务 | 客户对 AI 能力需求增长 |

### 3. 高优先级潜客详解
这里是完整报告详解。

### 4. 下一轮挖掘计划
1. 搜索招标网站。
2. 搜索招聘信号。
3. 建立线索档案。
---`

	out := applyPipelineDeliveryConstraint(full, input)
	for _, want := range []string{
		"## 小范围验收结果",
		"## 3 个候选方向",
		"3C电子制造企业",
		"钢铁/金属加工企业",
		"中小银行/消费金融公司",
		"## 验证计划",
		"搜索招标网站",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("trimmed output missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{
		"高优先级潜客详解",
		"系统集成商/SI",
		"很多完整报告内容",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("trimmed output should not contain %q:\n%s", notWant, out)
		}
	}
}

func TestBuildRecoverableStepFailureOutputSetsEvidenceBoundary(t *testing.T) {
	out := buildRecoverableStepFailureOutput(&PipelineStep{Name: "\u641c\u7d22\u7814\u7a76"}, errors.New("circuit breaker"))

	for _, want := range []string{
		"\u641c\u7d22\u7814\u7a76",
		"circuit breaker",
		"\u4e0d\u5f97\u5199\u6210\u5df2\u9a8c\u8bc1\u4e8b\u5b9e",
		"\u4e0d\u8981\u8981\u6c42\u7528\u6237\u91cd\u8bd5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("recoverable output missing %q: %s", want, out)
		}
	}
}

func TestIterationLimitFallbackBecomesDegradedStepOutput(t *testing.T) {
	raw := "任务达到迭代上限，未能生成完整最终回答。\n\n## 最后模型输出\n找到3个候选客户。\n\n可恢复文件：\n- .openkitty/logs/pipe_lead.json\n\n已获取到网页搜索结果，但模型未能在迭代预算内整理成报告。请重试或提高该步骤的迭代上限。"
	if !isIterationLimitFallbackOutput(raw) {
		t.Fatal("expected iteration-limit fallback to be detected")
	}

	out := buildDegradedStepOutput(&PipelineStep{Name: "搜索研究"}, raw)
	for _, want := range []string{
		"阶段性降级交付",
		"搜索研究",
		"不要要求用户重试",
		".openkitty/logs/pipe_lead.json",
		"已获取到网页搜索结果",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("degraded output missing %q: %s", want, out)
		}
	}
	for _, notWant := range []string{
		"任务达到迭代上限，未能生成完整最终回答",
		"请重试或提高该步骤的迭代上限",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("degraded output should sanitize %q: %s", notWant, out)
		}
	}
	if isProgressOnlyFinalOutput(out) {
		t.Fatalf("degraded output must remain eligible as a final output: %s", out)
	}
}

func TestEnglishIterationLimitFallbackBecomesDegradedStepOutput(t *testing.T) {
	raw := "Task reached iteration limit. Unable to generate summary."
	if !isIterationLimitFallbackOutput(raw) {
		t.Fatal("expected English iteration-limit fallback to be detected")
	}

	out := buildDegradedStepOutput(&PipelineStep{Name: "搜集信息"}, raw)
	for _, want := range []string{
		"阶段性降级交付",
		"搜集信息",
		"不要要求用户重试",
		"未能生成完整摘要",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("degraded output missing %q: %s", want, out)
		}
	}
	for _, notWant := range []string{
		"Task reached iteration limit",
		"Unable to generate summary",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("degraded output should sanitize %q: %s", notWant, out)
		}
	}
	if isProgressOnlyFinalOutput(out) {
		t.Fatalf("degraded output must remain eligible as a final output: %s", out)
	}
}

func TestPipelineFinalOutputUsesDegradedCompletedStep(t *testing.T) {
	exec := &PipelineExecution{
		Steps: []StepResult{{
			Name:   "搜索研究",
			Status: "completed",
			Output: buildDegradedStepOutput(&PipelineStep{Name: "搜索研究"}, "任务达到迭代上限，未能生成完整最终回答。\n候选客户A"),
			Error:  "iteration_budget_degraded",
		}},
	}
	out := exec.FinalOutput()
	if !strings.Contains(out, "阶段性降级交付") || !strings.Contains(out, "候选客户A") {
		t.Fatalf("final output should use degraded completed step, got %s", out)
	}
}

func TestEvidenceBoundaryInstructionForHighRiskAE(t *testing.T) {
	ctx := context.WithValue(context.Background(), "ae_employee_id", "lead-researcher")
	out := evidenceBoundaryInstruction(ctx)

	for _, want := range []string{
		"证据边界",
		"已验证 / 推断 / 待验证",
		"不要为了凑数量编造公司",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("evidence boundary missing %q: %s", want, out)
		}
	}
}

func TestEvidenceBoundaryInstructionSkippedForLowRiskAE(t *testing.T) {
	ctx := context.WithValue(context.Background(), "ae_employee_id", "meeting-secretary")
	if out := evidenceBoundaryInstruction(ctx); out != "" {
		t.Fatalf("meeting secretary should not receive evidence boundary instruction: %s", out)
	}
}

// ====================== PipelineExecutor trigger matching ======================

func TestMatchTrigger(t *testing.T) {
	gs := &GatewayService{
		config: &Config{
			Pipeline: PipelineConfig{
				Triggers: []PipelineTrigger{
					{When: "安全审查", Pipeline: "security-audit", Channel: "feishu"},
					{When: "代码重构", Pipeline: "refactor", Channel: ""}, // any channel
				},
				Pipelines: map[string]Pipeline{
					"security-audit": {Name: "security-audit", Steps: []PipelineStep{}},
					"refactor":       {Name: "refactor", Steps: []PipelineStep{}},
				},
			},
		},
	}
	e := NewPipelineExecutor(nil, gs, testLogger)

	tests := []struct {
		msg     string
		channel string
		want    string
	}{
		{"帮我做安全审查", "feishu", "security-audit"},
		{"安全审查一下", "feishu", "security-audit"},
		{"安全审查", "web", ""},              // 渠道不匹配
		{"代码重构", "anything", "refactor"}, // 不限渠道
		{"普通聊天", "feishu", ""},
	}

	for _, tt := range tests {
		result, matched := e.MatchTrigger(tt.msg, tt.channel)
		if tt.want == "" && matched {
			t.Errorf("msg=%q channel=%q: expected no match, got %q", tt.msg, tt.channel, result)
		}
		if tt.want != "" && (!matched || result != tt.want) {
			t.Errorf("msg=%q channel=%q: expected %q, got %q (matched=%v)", tt.msg, tt.channel, tt.want, result, matched)
		}
	}
}

// ====================== PipelineExecution state machine ======================

var testLogger = log.New(os.Stderr, "[test] ", log.LstdFlags)

func makeMockGateway(pc PipelineConfig) *GatewayService {
	return &GatewayService{
		config:           &Config{Pipeline: pc},
		sseClients:       make(map[string]map[string]chan Event),
		taskProgressLogs: make(map[string][]string),
	}
}

func TestPipelineExecution_BasicFlow(t *testing.T) {
	pc := PipelineConfig{
		Pipelines: map[string]Pipeline{
			"test-pipe": {
				Name: "test-pipe",
				Steps: []PipelineStep{
					{Name: "step1", Tools: []string{"read"}, MaxIter: 3},
					{Name: "step2", Tools: []string{"write"}, MaxIter: 3},
				},
			},
		},
	}

	gs := makeMockGateway(pc)
	e := NewPipelineExecutor(nil, gs, testLogger)

	exec, err := e.Run(context.Background(), "test-pipe", "test input", "sess-1", RunModeInteractive, "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if exec.ID == "" {
		t.Error("exec ID should not be empty")
	}
	if exec.Status != "running" {
		t.Errorf("status should be running, got %s", exec.Status)
	}
	if len(exec.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(exec.Steps))
	}
	if exec.Steps[0].Status != "pending" {
		t.Errorf("step 0 should be pending, got %s", exec.Steps[0].Status)
	}

	// Stop immediately — goroutine will try to execute step1 but has no agentFactory,
	// so it will fail. Our stop cancels the context so the goroutine exits cleanly.
	e.StopExecution(exec.ID)

	time.Sleep(200 * time.Millisecond)
	retrieved, ok := e.GetExecution(exec.ID)
	if !ok {
		t.Fatal("execution should still exist after stop")
	}
	if retrieved.Status != "stopped" {
		t.Errorf("status should be stopped, got %s", retrieved.Status)
	}
}

func TestPipelineExecutionPreservesParentTaskForPublicProgress(t *testing.T) {
	pc := PipelineConfig{Pipelines: map[string]Pipeline{
		"parent-progress": {Name: "parent-progress", Steps: []PipelineStep{{Name: "研究"}}},
	}}
	gs := makeMockGateway(pc)
	e := NewPipelineExecutor(nil, gs, testLogger)
	ctx := context.WithValue(context.Background(), "task_id", "agt-parent-progress")
	exec, err := e.Run(ctx, "parent-progress", "input", "session", RunModeInteractive, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.StopExecution(exec.ID) }()
	if exec.TaskID != "agt-parent-progress" {
		t.Fatalf("parent task id=%q", exec.TaskID)
	}
}

func TestPipelineExecution_NotFound(t *testing.T) {
	gs := makeMockGateway(PipelineConfig{Pipelines: map[string]Pipeline{}})
	e := NewPipelineExecutor(nil, gs, testLogger)

	_, err := e.Run(context.Background(), "nonexistent", "input", "sess", RunModeInteractive, "")
	if err == nil {
		t.Error("should fail for nonexistent pipeline")
	}
}

func TestPipelineExecutionAllowsInvestmentAdvisory(t *testing.T) {
	gs := makeMockGateway(PipelineConfig{Pipelines: map[string]Pipeline{
		"investment-advisory": {Name: "investment-advisory"},
	}})
	e := NewPipelineExecutor(nil, gs, testLogger)

	exec, err := e.Run(context.Background(), "investment-advisory", "input", "sess", RunModeInteractive, "")
	if err != nil {
		t.Fatalf("investment-advisory should be runnable, got %v", err)
	}
	if exec.PipelineName != "investment-advisory" {
		t.Fatalf("pipeline name = %q, want investment-advisory", exec.PipelineName)
	}
}

func TestListPipelinesIncludesInvestmentAdvisory(t *testing.T) {
	gs := makeMockGateway(PipelineConfig{Pipelines: map[string]Pipeline{
		"investment-advisory": {Name: "investment-advisory", Steps: []PipelineStep{{Name: "legacy"}}},
		"lead-research":       {Name: "lead-research", Steps: []PipelineStep{{Name: "active"}}},
	}})
	e := NewPipelineExecutor(nil, gs, testLogger)

	list := e.ListPipelines()
	got := map[string]bool{}
	for _, p := range list {
		got[p.Name] = true
	}
	for _, want := range []string{"investment-advisory", "lead-research"} {
		if !got[want] {
			t.Fatalf("expected %s in pipeline list, got %#v", want, list)
		}
	}
}

func TestPipelineExecution_ApproveWrongStatus(t *testing.T) {
	gs := makeMockGateway(PipelineConfig{Pipelines: map[string]Pipeline{
		"test": {Name: "test", Steps: []PipelineStep{}},
	}})
	e := NewPipelineExecutor(nil, gs, testLogger)

	err := e.ApproveStep("nonexistent", "ok")
	if err == nil {
		t.Error("should fail for nonexistent execution")
	}
}

func TestPipeline_StopTwice(t *testing.T) {
	pc := PipelineConfig{
		Pipelines: map[string]Pipeline{
			"test": {Name: "test", Steps: []PipelineStep{{Name: "s1", Tools: []string{"read"}, MaxIter: 1}}},
		},
	}
	gs := makeMockGateway(pc)
	e := NewPipelineExecutor(nil, gs, testLogger)

	exec, _ := e.Run(context.Background(), "test", "input", "sess", RunModeInteractive, "")
	time.Sleep(50 * time.Millisecond)

	err1 := e.StopExecution(exec.ID)
	if err1 != nil {
		// May fail if goroutine already completed (agentFactory is nil, step fails immediately)
		t.Logf("first stop result (may be already completed): %v", err1)
	}

	time.Sleep(100 * time.Millisecond)
	err2 := e.StopExecution(exec.ID)
	if err2 == nil {
		t.Error("second stop should fail (already stopped/completed)")
	}
	t.Logf("second stop result (expected fail): %v", err2)
}

// ====================== Skills restriction tests ======================

func TestPipelineStep_HasSkillTools(t *testing.T) {
	// Tools that include list_skills should allow skills
	tools := []string{"read", "list_skills", "use_skill"}
	hasSkills := false
	for _, t := range tools {
		if t == "list_skills" || t == "use_skill" {
			hasSkills = true
			break
		}
	}
	if !hasSkills {
		t.Error("should detect skill tools in whitelist")
	}
}

func TestPipelineStep_NoSkillTools(t *testing.T) {
	tools := []string{"read", "grep"}
	hasSkills := false
	for _, t := range tools {
		if t == "list_skills" || t == "use_skill" {
			hasSkills = true
			break
		}
	}
	if hasSkills {
		t.Error("should NOT detect skill tools in restricted list")
	}
}

// ====================== Routing tests ======================

func TestMatchCondition_Contains(t *testing.T) {
	if !matchCondition("审核通过，可以继续", "contains '审核通过'") {
		t.Error("should match '审核通过'")
	}
	if !matchCondition("审核不通过，请重新提交", "contains '不通过'") {
		t.Error("should match '不通过'")
	}
	if matchCondition("审核通过", "contains '不通过'") {
		t.Error("'审核通过' should NOT match '不通过'")
	}
	t.Log("注意: condition DSL 做子串匹配，使用时关键词需足够精确")
}

func TestMatchCondition_Default(t *testing.T) {
	if !matchCondition("anything", "") {
		t.Error("empty condition should match anything (default route)")
	}
}

func TestMatchRouteConditionRequiresStandaloneFinalPipelineStopMarker(t *testing.T) {
	route := StepRoute{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}
	if matchRouteCondition(nil, "demo", 0, "prompt mentions <!--PIPELINE_STOP-->\n\nfinal answer", route) {
		t.Fatal("stop marker in prompt or earlier text should not trigger route")
	}
	if matchRouteCondition(nil, "demo", 0, "final answer <!--PIPELINE_STOP-->", route) {
		t.Fatal("inline stop marker should not trigger route")
	}
	if !matchRouteCondition(nil, "demo", 0, "final answer\n\n<!--PIPELINE_STOP-->", route) {
		t.Fatal("standalone final stop marker should trigger route")
	}
}

func TestInvestmentAdvisoryStopRequiresEligibleTaskType(t *testing.T) {
	route := StepRoute{When: "contains '<!--PIPELINE_STOP-->'", Goto: "__stop__"}
	rawOutput := "collected information\n\n<!--PIPELINE_STOP-->"

	singleStock := &PipelineExecution{Steps: []StepResult{{
		Name:   "judge",
		Status: "completed",
		Output: `{"task_type":"single_stock","targets":[{"name":"demo"}]}`,
	}, {
		Name:   "金融信息与短线情报",
		Status: "completed",
		Output: "supplemental evidence",
	}, {
		Name:   "投研合成与质量门",
		Status: "running",
	}}}
	if matchRouteCondition(singleStock, "investment-advisory", 2, rawOutput, route) {
		t.Fatal("investment-advisory single_stock task must not stop after information collection")
	}
	if !matchRouteCondition(singleStock, "investment-advisory", 1, rawOutput, route) {
		t.Fatal("control-stop restriction must apply to the named quality gate, not a stale hard-coded index")
	}

	market := &PipelineExecution{Steps: []StepResult{{
		Name:   "judge",
		Status: "completed",
		Output: `{"task_type":"market"}`,
	}, {
		Name:   "金融信息与短线情报",
		Status: "completed",
		Output: "supplemental evidence",
	}, {
		Name:   "投研合成与质量门",
		Status: "running",
	}}}
	if !matchRouteCondition(market, "investment-advisory", 2, rawOutput, route) {
		t.Fatal("investment-advisory market task should allow normal stop")
	}
}

func TestFindStepIndex(t *testing.T) {
	steps := []PipelineStep{
		{Name: "初审"},
		{Name: "审批"},
		{Name: "完成"},
	}
	if idx := findStepIndex(steps, "审批"); idx != 1 {
		t.Errorf("findStepIndex('审批') = %d, want 1", idx)
	}
	if idx := findStepIndex(steps, "不存在"); idx != -1 {
		t.Errorf("findStepIndex('不存在') = %d, want -1", idx)
	}
}

func TestPipelineStep_RoutingRoundTrip(t *testing.T) {
	step := PipelineStep{
		Name: "预算初审",
		Routing: []StepRoute{
			{When: "contains '大额'", Goto: "总监审批"},
			{When: "", Goto: "完成"}, // default
		},
		Repeat: &StepRepeat{Max: 3, Condition: "contains '不通过'", OnMax: "升级处理"},
	}
	data, _ := json.Marshal(step)

	var decoded PipelineStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(decoded.Routing) != 2 {
		t.Errorf("routing count = %d, want 2", len(decoded.Routing))
	}
	if decoded.Repeat == nil || decoded.Repeat.Max != 3 {
		t.Error("repeat config not preserved")
	}
}

// ====================== Mock tool for tests ======================

type mockTool struct {
	name string
	desc string
}

func (m *mockTool) Name() string                       { return m.name }
func (m *mockTool) Description() string                { return m.desc }
func (m *mockTool) Parameters() map[string]interface{} { return nil }
func (m *mockTool) Execute(ctx context.Context, p map[string]interface{}) (string, error) {
	return "mock result", nil
}
