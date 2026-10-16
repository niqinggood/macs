package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mac/internal/aiemployee"
)

func TestPrependAIEmployeeIntakeStepDefinesIntentPermissionsAndDelivery(t *testing.T) {
	original := Pipeline{
		Name: "wechat-article",
		Steps: []PipelineStep{
			{
				Name:  "素材准备",
				Tools: []string{"web_search"},
				SourceContract: &PipelineSourceContract{
					Required: true,
				},
			},
			{
				Name:  "公众号交付物",
				Tools: []string{"wechat_article_bundle"},
				ArtifactContract: &PipelineArtifactContract{
					Paths:    []string{".openkitty/storage/article.md"},
					MinBytes: 500,
				},
				OutputContract: &PipelineOutputContract{
					MinChars:         800,
					RequiredSections: []string{"摘要"},
				},
			},
		},
	}
	spec := aiEmployeeIntakeSpec{
		EmployeeID:   "content-editor",
		EmployeeName: "微信公众号编辑",
		Role:         "公众号选题与成稿",
		PipelineName: original.Name,
		ContextPath:  ".openkitty/storage/.system/ae_context/session.json",
	}

	got := prependAIEmployeeIntakeStep(original, spec)
	if len(got.Steps) != 3 || got.Steps[0].Name != aiEmployeeIntakeStepName || !got.Steps[0].NoTools {
		t.Fatalf("unexpected intake pipeline: %#v", got.Steps)
	}
	for _, want := range []string{
		"微信公众号编辑", "公众号选题与成稿", "后续步骤与权限边界",
		"执行流程与交付标准", "约束账本", "任务身份坐标", "筛选维度", "自动放宽顺序", "不得虚构落盘路径", "INTAKE_STATUS: READY", "INPUT_STATUS: MISSING",
		"面向用户给出选项/最少问题",
		"web_search", "wechat_article_bundle", ".openkitty/storage/article.md", "至少 800 字符",
	} {
		if !strings.Contains(got.Steps[0].Prompt, want) {
			t.Fatalf("intake prompt missing %q:\n%s", want, got.Steps[0].Prompt)
		}
	}
	if !aiemployee.ToolListed(got.Steps[1].Tools, "read_file") {
		t.Fatalf("first business step must be able to read approved session context: %#v", got.Steps[1].Tools)
	}
	if !strings.Contains(got.Steps[1].Prompt, spec.ContextPath) {
		t.Fatalf("first business step omitted context continuation rule: %s", got.Steps[1].Prompt)
	}
	if len(original.Steps) != 2 || aiemployee.ToolListed(original.Steps[0].Tools, "read_file") {
		t.Fatal("prepend must not mutate the registered pipeline definition")
	}
}

func TestFundraisingIntakePreservesUnknownObjectRelationships(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "ai-employee-direct-fundraising-advisor",
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeName:         "融资专家",
		AIEmployeeAtName:       "融资专家",
		AIEmployeeOriginalTask: "@融资专家 根据产品、参考工具和补充能力写 BP。",
		Steps: []PipelineStep{
			{Name: "公司、产品与对比研究", Tools: []string{"web_search", "web_fetch"}},
			{Name: "融资专业分析与交付", RequiredSkills: []string{"huanzhi-fa-fundraising"}},
		},
	}
	rule := aiemployee.BuildDomainIntakeRule(pipeline)
	for _, want := range []string{
		"用户补充描述", "关系与实现状态均记为未知", "不得拆成编号",
		"未落地", "Pre-A", "公司、产品与对比研究 → 融资专业分析与交付",
	} {
		if !strings.Contains(rule, want) {
			t.Fatalf("fundraising intake rule missing %q:\n%s", want, rule)
		}
	}
}

func TestMaterializeAESessionContextUsesOnlyCompletedRecentResults(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	workspace := t.TempDir()
	gw.config = &Config{Workspace: workspace}
	mgr := &AIEmployeeManager{gateway: gw}
	now := time.Now()
	rows := []ChatMessage{
		{SessionID: "web_session", Role: "user", Content: "今天微博有哪些热点新闻？", Status: "completed", CreatedAt: now.Add(-3 * time.Minute)},
		{SessionID: "web_session", Role: "assistant", AgentName: "Researcher", Content: "今日热点：A事件、B事件，并附有出处。", Status: "completed", CreatedAt: now.Add(-2 * time.Minute)},
		{SessionID: "web_session", Role: "user", Content: "@微信公众号编辑 整理一份今日热点文章", Status: "running", CreatedAt: now.Add(-time.Minute)},
		{SessionID: "other_session", Role: "assistant", Content: "不应跨会话出现", Status: "completed", CreatedAt: now},
	}
	if err := gw.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed chat messages: %v", err)
	}

	relPath, block := mgr.materializeAESessionContext("web_session")
	if relPath == "" || !strings.Contains(block, aiEmployeeContextPathMarker+" "+relPath) {
		t.Fatalf("missing frozen context marker: path=%q block=%q", relPath, block)
	}
	if !strings.Contains(block, "今日热点：A事件") || !strings.Contains(block, "Researcher") {
		t.Fatalf("completed assistant result missing from preview: %s", block)
	}
	if strings.Contains(block, "@微信公众号编辑") || strings.Contains(block, "不应跨会话出现") {
		t.Fatalf("pending or cross-session content leaked into context: %s", block)
	}

	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read frozen context: %v", err)
	}
	var bundle struct {
		Messages []aiemployee.SessionContextMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("decode frozen context: %v", err)
	}
	if len(bundle.Messages) != 2 || bundle.Messages[1].SourceLocator != "session://web_session/messages/"+jsonUint(rows[1].ID) {
		t.Fatalf("unexpected context bundle: %#v", bundle.Messages)
	}
}

func jsonUint(value uint) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestPipelineSourceClassificationIgnoresInjectedSessionContext(t *testing.T) {
	input := `整理一份今日热点的公众号文章

---

## 同会话候选上下文（宿主已冻结）
OPENKITTY_AE_CONTEXT_PATH: .openkitty/storage/.system/ae_context/session.json

近期内容预览：
- [Researcher · session://s/messages/2] https://example.com/hot-news`
	if pipelineInputReferencesSource(input) {
		t.Fatal("host-injected context must not turn an original-topic request into a user source reference")
	}
	if kind, locator := pipelineReferencedSourceKind(input); kind != "referenced" || locator != "" {
		t.Fatalf("kind=%q locator=%q, want referenced with no user locator", kind, locator)
	}
	explicit := "请改写 https://user.example/article\n\n" + input[strings.Index(input, "## 同会话候选上下文"):]
	if kind, locator := pipelineReferencedSourceKind(explicit); kind != "url" || locator != "https://user.example/article" {
		t.Fatalf("explicit current-turn source lost: kind=%q locator=%q", kind, locator)
	}
}

func TestPipelineAcquisitionEvidenceAcceptsApprovedSessionContextRead(t *testing.T) {
	contextPath := ".openkitty/storage/.system/ae_context/session.json"
	exec := &PipelineExecution{Steps: []StepResult{{
		Name:   "素材准备",
		Status: "running",
		ToolCalls: []ToolCallRecord{{
			ToolName: "read_file",
			Params:   `{"path":".openkitty/storage/.system/ae_context/session.json"}`,
			Result:   `{"messages":[{"source_locator":"session://s/messages/2","content":"热点正文"}]}`,
			Status:   "success",
		}},
	}}}
	input := "整理今日热点文章\n" + aiEmployeeContextPathMarker + " " + contextPath
	if !pipelineStepHasAcquisitionEvidence(exec, 0, input, ".openkitty/storage/.system/frozen/source.json", "", nil) {
		t.Fatal("successful read of the host-frozen session context should be valid acquisition evidence")
	}
}

func TestAIEmployeeIntakeFailureFallbackDefersToDomainGate(t *testing.T) {
	pipeline := Pipeline{Name: "bid-proposal", Steps: []PipelineStep{
		{Name: aiEmployeeIntakeStepName},
		{Name: "招标解读"},
		{Name: "响应矩阵"},
	}}
	got := buildAIEmployeeIntakeFailureFallback(pipeline, "请解读这份招标文件")
	for _, want := range []string{"请解读这份招标文件", "招标解读 → 响应矩阵", "领域门禁", "INTAKE_STATUS: READY"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, pipelineStopMarker) || strings.Contains(got, "INPUT_STATUS: MISSING") {
		t.Fatalf("host fallback must not guess the domain gate outcome: %s", got)
	}
}

func TestAIEmployeeDeterministicMissingInputStopsObviousSourceDependentTasks(t *testing.T) {
	tests := []struct {
		name     string
		pipeline string
		input    string
		want     string
	}{
		{name: "wechat has no topic", pipeline: "wechat-article", input: "帮我整理一篇公众号文章", want: "文章主题/核心观点/目标读者"},
		{name: "ppt has no topic or material", pipeline: "ppt-polish", input: "帮我润色一下PPT", want: "演示主题"},
		{name: "bid demonstrative is not material", pipeline: "bid-proposal", input: "请解读这份招标文件并写标书", want: "招标/采购文件"},
		{name: "personalized investment lacks decisive fields", pipeline: "investment-advisory", input: "我持有新安股份，现在应该加仓还是卖出", want: "持仓成本"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := Pipeline{
				Name: tt.pipeline, AIEmployeeID: "employee-id", AIEmployeeName: "测试员工", AIEmployeeAtName: "测试员工",
				Steps: []PipelineStep{{Name: aiEmployeeIntakeStepName}, {Name: "业务步骤"}},
			}
			got, stopped := buildAIEmployeeDeterministicMissingInput(pipeline, tt.input)
			if !stopped || !strings.Contains(got, tt.want) || !strings.Contains(got, pipelineWaitingInputMarker) || !strings.HasSuffix(got, pipelineStopMarker) {
				t.Fatalf("missing-input guard failed: stopped=%v output=%q", stopped, got)
			}
			for _, want := range []string{"本次 AI员工执行已停止", "@测试员工", "新的完整请求", "从第一个步骤重新执行"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing-input restart contract missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, "可自动继续") || strings.Contains(got, "收到后我会继续") {
				t.Fatalf("missing-input output still promises implicit continuation: %s", got)
			}
		})
	}
}

func TestAIEmployeeDeterministicMissingInputAllowsTopicMaterialAndSessionContext(t *testing.T) {
	tests := []struct {
		pipeline string
		input    string
	}{
		{pipeline: "wechat-article", input: "写一篇关于AI员工如何理解需求的公众号文章"},
		{pipeline: "wechat-article", input: "整理今日热点的公众号文章"},
		{pipeline: "ppt-polish", input: `润色 E:\\slides\\report.pptx`},
		{pipeline: "ppt-polish", input: "做一份 OpenKitty 产品介绍 PPT"},
		{pipeline: "ppt-polish", input: "制作一份面向管理层的季度复盘，8页，咨询风格，结论先行"},
		{pipeline: "bid-proposal", input: "解读 https://example.com/tender.pdf 并写标书"},
		{pipeline: "ppt-polish", input: "帮我润色PPT\n" + aiEmployeeContextPathMarker + " .openkitty/storage/.system/ae_context/s.json"},
		{pipeline: "investment-advisory", input: "分析今天市场行情"},
		{pipeline: "investment-advisory", input: "新安股份现在能买吗"},
		{pipeline: "investment-advisory", input: "我持有新安股份，成本8.5元，占总仓30%，计划中线持有，最大亏损10%，现在怎么操作"},
	}
	for _, tt := range tests {
		if got, stopped := buildAIEmployeeDeterministicMissingInput(Pipeline{Name: tt.pipeline}, tt.input); stopped {
			t.Fatalf("valid input was blocked for %s: %s", tt.pipeline, got)
		}
	}
}

func TestAIEmployeeDeterministicMissingInputRejectsSemanticallyEmptyRequest(t *testing.T) {
	pipeline := Pipeline{
		Name:             "example-workflow",
		AIEmployeeID:     "example-employee",
		AIEmployeeName:   "示例员工",
		AIEmployeeAtName: "示例员工",
		Steps:            []PipelineStep{{Name: aiEmployeeIntakeStepName}, {Name: "业务步骤"}},
	}
	got, stopped := buildAIEmployeeDeterministicMissingInput(pipeline, "帮我做一下。")
	if !stopped {
		t.Fatal("semantically empty request was accepted")
	}
	for _, want := range []string{"泛化指令", "任务动作与对象", "@示例员工", pipelineWaitingInputMarker, pipelineStopMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("generic missing-input response lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "## 意图理解") || strings.Count(got, "？")+strings.Count(got, "?") > 1 {
		t.Fatalf("generic missing-input response became an intake questionnaire: %s", got)
	}
}

func TestAIEmployeeSemanticallyEmptyGuardPreservesShortActionableRequests(t *testing.T) {
	for _, input := range []string{
		"分析英伟达",
		"帮我做BP计划书",
		"审查TS",
		"写周报",
		"处理 https://example.com/material",
		`检查 E:\docs\contract.pdf`,
	} {
		if aiemployee.RequestIsSemanticallyEmpty(input) {
			t.Fatalf("short actionable request was rejected: %q", input)
		}
	}
}

func TestAIEmployeeDeterministicMissingInputUsesOriginalTaskNotEmployeePrefix(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "wechat-article",
		AIEmployeeOriginalTask: "整理一篇公众号文章",
	}
	prefixedInput := "你是微信公众号编辑。负责公众号选题、热点调研、成稿与排版。\n\n---\n\n整理一篇公众号文章"
	got, stopped := buildAIEmployeeDeterministicMissingInput(pipeline, prefixedInput)
	if !stopped || !strings.Contains(got, "文章主题/核心观点/目标读者") {
		t.Fatalf("employee identity prefix bypassed missing-input gate: stopped=%v output=%s", stopped, got)
	}
	if strings.Contains(got, "负责公众号选题") {
		t.Fatalf("employee system prompt leaked into current-input summary: %s", got)
	}
}

func TestAIEmployeeIntakeExplicitPathOverridesStaleMissingDecision(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "lead-research",
		AIEmployeeOriginalTask: `在E盘下有一个E:\DEV\ai-node是我的一个项目，你帮我寻找潜在客户`,
		AIEmployeeContextPath:  ".openkitty/storage/.system/ae_context/session.json",
		Steps: []PipelineStep{
			{Name: aiEmployeeIntakeStepName, NoTools: true},
			{Name: "项目理解", Tools: []string{"read_file", "list_dir", "glob"}},
			{Name: "搜索研究", Tools: []string{"web_search"}},
		},
	}
	stale := `旧扫描只检查了 E:\ai-node，因此请重新提供路径。

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`
	got, overridden := normalizeAIEmployeeIntakeDecision(pipeline, "prefixed history", stale)
	if !overridden {
		t.Fatal("explicit current path should override a stale missing-input decision")
	}
	for _, want := range []string{`E:\DEV\ai-node`, "项目理解 → 搜索研究", "INTAKE_STATUS: READY"} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized intake missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{`E:\ai-node`, pipelineWaitingInputMarker, pipelineStopMarker} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("normalized intake retained stale blocker %q:\n%s", forbidden, got)
		}
	}
}

func TestAIEmployeeIntakeTreatsRequestedChoiceAsDeliverable(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "generic-advisor",
		AIEmployeeOriginalTask: "请先确认任务范围，并让我选择信息起点：①联网检索；②读取材料；③两者结合。选择后再开始正式分析。",
		Steps: []PipelineStep{
			{Name: aiEmployeeIntakeStepName, NoTools: true},
			{Name: "业务答复", NoTools: true},
		},
	}
	raw := `## 意图理解
- 目标结果：先让用户选择信息起点。

## 输入判定
当前缺少用户选择，无法继续。

## 重新发起方式
请复制原请求并重新 @员工。

INPUT_STATUS: MISSING
SLOT_STATUS: MISSING
<!--PIPELINE_STOP-->`
	got, overridden := normalizeAIEmployeeIntakeDecision(pipeline, "generated pipeline prompt", raw)
	if !overridden {
		t.Fatal("an explicitly requested choice should be routed to the user-facing step")
	}
	for _, want := range []string{"先呈现选项", "最少决策问题", "INTAKE_STATUS: READY"} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized interactive intake missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"重新发起方式", pipelineWaitingInputMarker, pipelineSlotMissingMarker, pipelineStopMarker} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("normalized interactive intake retained internal blocker %q:\n%s", forbidden, got)
		}
	}
}

func TestAIEmployeeIntakeTreatsBareStopAfterRequestedChoiceAsReady(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "generic-advisor",
		AIEmployeeOriginalTask: "先让我选择处理方式，再执行后面的任务。",
		Steps:                  []PipelineStep{{Name: aiEmployeeIntakeStepName, NoTools: true}, {Name: "业务答复", NoTools: true}},
	}
	raw := `## 输入判定
请选 ① 直接分析或 ② 先补材料。

<!--PIPELINE_STOP-->`
	got, overridden := normalizeAIEmployeeIntakeDecision(pipeline, "generated pipeline prompt", raw)
	if !overridden || !strings.Contains(got, "INTAKE_STATUS: READY") || strings.Contains(got, pipelineStopMarker) {
		t.Fatalf("bare interactive stop was not routed to the user-facing step: overridden=%v output=%q", overridden, got)
	}
}

func TestAIEmployeeIntakeReadyCannotStopBeforeBusinessSteps(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "direct_domain_advisor",
		AIEmployeeOriginalTask: "请基于我给出的产品资料制作一份完整方案。",
		Steps: []PipelineStep{
			{Name: aiEmployeeIntakeStepName, NoTools: true},
			{Name: "事实研究", Tools: []string{"web_search", "web_fetch"}},
			{Name: "专业交付", NoTools: true},
		},
	}
	raw := `## 意图理解
当前信息足以开始事实研究和专业交付。

## 输入判定
INTAKE_STATUS: READY
<!--PIPELINE_STOP-->`
	got, overridden := normalizeAIEmployeeIntakeDecision(pipeline, "generated pipeline prompt", raw)
	if !overridden {
		t.Fatal("a contradictory READY + stop decision must be normalized")
	}
	if strings.Contains(got, pipelineStopMarker) {
		t.Fatalf("READY intake retained a terminal stop marker: %q", got)
	}
	if !strings.Contains(got, "INTAKE_STATUS: READY") {
		t.Fatalf("READY decision was lost while removing the stop marker: %q", got)
	}
}

func TestPipelineExecutesBusinessStepsAfterContradictoryReadyStop(t *testing.T) {
	original := Pipeline{
		Name: "direct_evidence_first",
		Steps: []PipelineStep{
			{Name: "事实研究", Prompt: "形成事实底稿", NoTools: true, MaxIter: 1},
			{Name: "专业交付", Prompt: "基于事实底稿完成最终回答", NoTools: true, MaxIter: 1},
		},
	}
	pipeline := prependAIEmployeeIntakeStep(original, aiEmployeeIntakeSpec{
		EmployeeID:     "evidence-first-advisor",
		EmployeeName:   "证据优先顾问",
		EmployeeAtName: "证据优先顾问",
		Role:           "先研究再交付",
		OriginalTask:   "请研究这个产品并给出完整结论。",
		PipelineName:   original.Name,
	})
	creator := func(config AgentConfig, _ string) (Agent, error) {
		output := "## 输入判定\nINTAKE_STATUS: READY\n<!--PIPELINE_STOP-->"
		switch {
		case strings.Contains(config.Name, "事实研究"):
			output = "事实底稿：已区分产品事实、用户陈述和未知项，并记录可供下一步使用的来源边界。"
		case strings.Contains(config.Name, "专业交付"):
			output = "## 最终结论\n\n这是基于上游事实底稿形成的完整用户回答；它不是受理计划，也没有把内部步骤暴露为交付结果。"
		}
		return &blackBoxPipelineAgent{name: config.Name, output: output}, nil
	}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(
		&AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator},
		gateway,
		log.New(io.Discard, "", 0),
	)
	execution := &PipelineExecution{
		ID:           "pipe_ready_stop_regression",
		PipelineName: pipeline.Name,
		Status:       "running",
		CreatedAt:    time.Now(),
		Steps:        make([]StepResult, len(pipeline.Steps)),
	}
	for index := range pipeline.Steps {
		execution.Steps[index] = StepResult{Name: pipeline.Steps[index].Name, Status: "pending", Revision: 1}
	}
	executor.runStepsFrom(
		context.Background(),
		execution,
		pipeline,
		0,
		"请研究这个产品并给出完整结论。",
		"ready-stop-session",
		RunModeInteractive,
	)
	if execution.Status != "completed" {
		t.Fatalf("pipeline status = %q, steps=%#v", execution.Status, execution.Steps)
	}
	if len(execution.Steps) != 3 ||
		execution.Steps[1].Status != "completed" ||
		execution.Steps[2].Status != "completed" ||
		!strings.Contains(execution.Steps[2].Output, "最终结论") {
		t.Fatalf("business steps were skipped after READY: %#v", execution.Steps)
	}
}

func TestAIEmployeeInteractiveHandoffInstructionIsGenericAndStepScoped(t *testing.T) {
	pipeline := Pipeline{
		AIEmployeeID:           "generic-advisor",
		AIEmployeeOriginalTask: "先让我选择信息起点，再执行正式分析。",
		Steps: []PipelineStep{
			{Name: aiEmployeeIntakeStepName, NoTools: true},
			{Name: "业务答复", NoTools: true},
		},
	}
	got := appendAIEmployeeInteractiveHandoffInstruction("base prompt", pipeline, &pipeline.Steps[1], 1)
	for _, want := range []string{
		"earliest unresolved user-controlled interaction",
		"complete user-facing deliverable for this turn",
		"every option and confirmation field the user explicitly requested",
		"do not replace them with different profiling questions",
		"Do not expose or summarize an upstream ledger",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("interactive handoff prompt missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"fundraising", "融资", "BP", "CSDN"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("interactive handoff overfit domain %q:\n%s", forbidden, got)
		}
	}

	unrelated := pipeline
	unrelated.AIEmployeeOriginalTask = "直接分析当前材料并给出结果。"
	if unchanged := appendAIEmployeeInteractiveHandoffInstruction("base prompt", unrelated, &unrelated.Steps[1], 1); unchanged != "base prompt" {
		t.Fatalf("non-interactive task received handoff instruction: %q", unchanged)
	}
	if intake := appendAIEmployeeInteractiveHandoffInstruction("base prompt", pipeline, &pipeline.Steps[0], 0); intake != "base prompt" {
		t.Fatalf("intake step received downstream handoff instruction: %q", intake)
	}
}

func TestAIEmployeeExplicitConfirmationCoverageUsesUserClause(t *testing.T) {
	task := "请先确认项目范围、交付格式和已有材料，并让我选择处理方式：①直接处理；②先读材料。"
	output := "请选择：①直接处理；②先读材料。"
	got := ensureAIEmployeeExplicitConfirmationCoverage(task, output)
	if !strings.Contains(got, "另请一并确认：项目范围、交付格式和已有材料。") {
		t.Fatalf("explicit confirmation clause was not preserved: %q", got)
	}

	alreadyCovered := output + "\n项目范围、交付格式和已有材料"
	if got := ensureAIEmployeeExplicitConfirmationCoverage(task, alreadyCovered); got != alreadyCovered {
		t.Fatalf("covered confirmation clause was duplicated: %q", got)
	}

	unrelated := "直接处理当前材料。"
	if got := ensureAIEmployeeExplicitConfirmationCoverage(unrelated, output); got != output {
		t.Fatalf("non-interactive request received a confirmation clause: %q", got)
	}

	for _, forbidden := range []string{"fundraising", "融资", "BP", "CSDN"} {
		if strings.Contains(ensureAIEmployeeExplicitConfirmationCoverage(task, output), forbidden) {
			t.Fatalf("confirmation coverage overfit domain %q", forbidden)
		}
	}
}

func TestAIEmployeeExplicitOptionCoverageRequiresEachOptionBlock(t *testing.T) {
	task := "请先只让我选择信息起点：①联网检索公开资料；②读取我接下来上传的材料；③两者结合。不要假设我已经选择。"
	output := `请选择一种信息起点：

① 联网检索公开资料

③ 两者结合

请回复数字，例如 ②。`
	got := ensureAIEmployeeExplicitConfirmationCoverage(task, output)
	if !strings.Contains(got, "②读取我接下来上传的材料") {
		t.Fatalf("missing option content was not restored from the user's own request: %q", got)
	}
	if strings.Count(got, "①") != 1 || strings.Count(got, "③") != 1 {
		t.Fatalf("already-present option blocks were duplicated: %q", got)
	}

	complete := "①联网检索公开资料；②读取我接下来上传的材料；③两者结合。"
	if got := ensureAIEmployeeExplicitConfirmationCoverage(task, complete); got != complete {
		t.Fatalf("complete inline options were changed: %q", got)
	}
}

func TestAIEmployeeExplicitLabelCoverageUsesUserWording(t *testing.T) {
	task := `把我这句标为项目方陈述，不要当成已核验事实。`
	output := `该表述属于项目方自报，尚无独立来源。`
	got := ensureAIEmployeeExplicitConfirmationCoverage(task, output)
	if !strings.Contains(got, "用户指定标签：**项目方陈述**") {
		t.Fatalf("explicit user-authored label was not preserved: %q", got)
	}
	if labels := aiemployee.ExtractExplicitLabelRequirements(`标注为“待核验假设”；标记为用户输入。`); len(labels) != 2 ||
		labels[0] != "待核验假设" || labels[1] != "用户输入" {
		t.Fatalf("explicit labels were not extracted generically: %q", labels)
	}
}

func TestAIEmployeeExplicitUserProvenanceCoverageIsDeterministic(t *testing.T) {
	task := `我的口头补充方向是：AI 获客工作流。必须明确这句话只来自用户口述，不是两篇原文内容。`
	output := `两篇原文均未出现 AI 获客工作流。`
	got := ensureAIEmployeeExplicitConfirmationCoverage(task, output)
	for _, want := range []string{"暂记为你的业务方向陈述", "不是所给原文内容", "未提供独立来源"} {
		if !strings.Contains(got, want) {
			t.Fatalf("explicit user provenance requirement %q was not restored: %q", want, got)
		}
	}

	alreadyCovered := `AI 获客工作流暂记为你的业务方向陈述，非原文内容，也没有独立来源。`
	if got := ensureAIEmployeeExplicitConfirmationCoverage(task, alreadyCovered); got != alreadyCovered {
		t.Fatalf("already-covered provenance was duplicated: %q", got)
	}

	ordinary := `请核验两篇原文并列出来源。`
	if got := ensureAIEmployeeExplicitConfirmationCoverage(ordinary, output); got != output {
		t.Fatalf("ordinary source request gained an invented user-provenance statement: %q", got)
	}
}

func TestExtractAIEmployeeExplicitCircledOptionsIsDomainAgnostic(t *testing.T) {
	task := "请选择执行方式：①直接处理；②先读文件；③两者结合。后续要求不要提前执行。"
	got := aiemployee.ExtractExplicitCircledOptions(task)
	want := []string{"①直接处理", "②先读文件", "③两者结合"}
	if len(got) != len(want) {
		t.Fatalf("options = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("option %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAIEmployeeIntakeDoesNotOverrideGenuineMissingInput(t *testing.T) {
	pipeline := Pipeline{
		Name:                   "generic-advisor",
		AIEmployeeOriginalTask: "分析我没有提供的那份合同。",
		Steps:                  []PipelineStep{{Name: aiEmployeeIntakeStepName, NoTools: true}, {Name: "业务答复", NoTools: true}},
	}
	raw := "请提供合同正文或文件。\n\nINPUT_STATUS: MISSING\n<!--PIPELINE_STOP-->"
	if got, overridden := normalizeAIEmployeeIntakeDecision(pipeline, "generated pipeline prompt", raw); overridden || got != raw {
		t.Fatalf("genuine missing input was incorrectly normalized: overridden=%v output=%q", overridden, got)
	}
}

func TestAIEmployeeIntakePromptMakesDownstreamValidateExplicitPath(t *testing.T) {
	pipeline := Pipeline{Name: "lead-research", Steps: []PipelineStep{{Name: "项目理解", Tools: []string{"read_file", "list_dir"}}}}
	spec := aiEmployeeIntakeSpec{
		EmployeeID: "lead-researcher", EmployeeName: "潜客研究员", Role: "潜客研究", PipelineName: pipeline.Name,
		OriginalTask: `在E盘下有一个E:\DEV\ai-node是我的一个项目，你帮我寻找潜在客户`,
	}
	got := aiemployee.BuildIntakePrompt(pipeline, spec)
	for _, want := range []string{`E:\DEV\ai-node`, "必须判定 READY", "当前路径覆盖历史对话中的旧路径", "首个获准只读工具的业务步骤验证"} {
		if !strings.Contains(got, want) {
			t.Fatalf("intake prompt missing %q:\n%s", want, got)
		}
	}
}

func TestInvestmentAdvisorIntakeTreatsBroadFinancialResearchAsActionable(t *testing.T) {
	pipeline := Pipeline{
		Name:  "investment-advisory",
		Steps: []PipelineStep{{Name: "自主判断与证据采集", Tools: []string{"stock_facts", "web_search", "web_fetch"}}},
	}
	spec := aiEmployeeIntakeSpec{
		EmployeeID: "investment-advisor", EmployeeName: "投资顾问", EmployeeAtName: "投资顾问",
		Role: "市场与标的研究", PipelineName: pipeline.Name, OriginalTask: "分析今天市场行情",
	}
	got := aiemployee.BuildIntakePrompt(pipeline, spec)
	for _, want := range []string{
		"整体市场/大盘/当前或某日行情", "必须判定 READY", "选择主要本地市场作为明确假设",
		"投资周期、风险偏好、持仓、成本、账户规模", "不得阻断行情、标的、行业、事件或一般策略研究",
		"只有 personalized_action", "INVESTMENT_INTENT", "重新 @投资顾问", "从第一个步骤重新执行",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("investment intake prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPPTPolishIntakeSupportsAllInputModes(t *testing.T) {
	pipeline := Pipeline{
		Name:  "ppt-polish",
		Steps: []PipelineStep{{Name: "素材读取", Tools: []string{"read_file"}}, {Name: "内容重构"}, {Name: "视觉润色"}},
	}
	spec := aiEmployeeIntakeSpec{
		EmployeeID: "ppt-polish-specialist", EmployeeName: "PPT润色专员", EmployeeAtName: "PPT润色",
		Role: "PPT内容与视觉设计", PipelineName: pipeline.Name, OriginalTask: "做一份 OpenKitty 产品介绍 PPT",
	}
	got := aiemployee.BuildIntakePrompt(pipeline, spec)
	for _, want := range []string{
		"PPT_INPUT_MODE", "existing_deck", "source_material", "detailed_brief", "concept_only",
		"不得因为没有原始 PPTX 而要求用户补 PPT 文件",
		"缺失的普通设计参数采用合理默认值",
		"不得机械询问受众、用途、页数或配色",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("PPT intake prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPPTPolishActionableInputDoesNotMistakeGenericReferenceForMaterial(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "帮我润色一下PPT", want: false},
		{input: "根据这个介绍材料做PPT", want: false},
		{input: "做一份 OpenKitty 产品介绍 PPT", want: true},
		{input: `根据 E:\docs\openkitty.md 做介绍PPT`, want: true},
		{input: "制作面向投资人的融资路演，10页，克制科技风", want: true},
	}
	for _, tt := range tests {
		if got := aiemployee.PPTRequestHasActionableInput(tt.input); got != tt.want {
			t.Fatalf("input %q actionable=%v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestAIEmployeeWaitingInputNormalizerRequiresExplicitFreshInvocation(t *testing.T) {
	pipeline := Pipeline{
		Name: "generic", AIEmployeeID: "generic-worker", AIEmployeeName: "通用员工", AIEmployeeAtName: "通用员工",
	}
	raw := "请补充唯一的目标文件；收到后继续。\n\nINPUT_STATUS: MISSING\n<!--PIPELINE_STOP-->"
	got := aiemployee.NormalizeWaitingInputOutput(raw, pipeline)
	for _, want := range []string{"@通用员工", "新的完整请求", "不保留", "从第一个步骤重新执行", pipelineWaitingInputMarker, pipelineStopMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized waiting-input output missing %q: %s", want, got)
		}
	}
	if !strings.HasSuffix(got, pipelineStopMarker) {
		t.Fatalf("pipeline stop marker must remain final: %s", got)
	}
	if strings.Contains(got, "收到后继续") {
		t.Fatalf("normalized output retained an implicit-continuation promise: %s", got)
	}
	if !aiEmployeeOutputRequiresFreshInvocation(got, pipeline) {
		t.Fatal("AI employee waiting-input output must terminate the runtime")
	}
	if aiEmployeeOutputRequiresFreshInvocation(got, Pipeline{Name: "ordinary-pipeline"}) {
		t.Fatal("ordinary non-employee pipeline must keep its existing routing semantics")
	}
	twice := aiemployee.NormalizeWaitingInputOutput(got, pipeline)
	if strings.Count(twice, "## 重新发起方式") != 1 {
		t.Fatalf("waiting-input normalization must be idempotent: %s", twice)
	}
}

func TestAIEmployeeBusinessStepCannotStopForExternalEvidenceGap(t *testing.T) {
	pipeline := Pipeline{
		Name: "investment-advisory", AIEmployeeID: "investment-advisor",
		AIEmployeeName: "投资顾问", AIEmployeeAtName: "投资顾问",
	}
	step := &PipelineStep{Name: "自主判断与证据采集"}
	raw := `已取得部分市场证据，但行情页面没有返回成交额和涨跌家数。

## 重新发起方式
请重新 @投资顾问 并提供这些公开数据。

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`

	got, suppressed := normalizeAIEmployeeStepWaitingInputOutput(raw, pipeline, step)
	if !suppressed {
		t.Fatal("business-step missing-input stop must be suppressed")
	}
	for _, forbidden := range []string{pipelineWaitingInputMarker, pipelineStopMarker, "## 重新发起方式", "请重新 @投资顾问"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("suppressed business-step output retained %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"已取得部分市场证据", "DATA_STATUS: degraded", "不属于需要用户补充的决定性输入", "Pipeline 将继续"} {
		if !strings.Contains(got, want) {
			t.Fatalf("suppressed business-step output missing %q:\n%s", want, got)
		}
	}
	if aiEmployeeOutputRequiresFreshInvocation(got, pipeline) {
		t.Fatal("suppressed business-step output must not terminate the pipeline")
	}

	prompt := appendAIEmployeeClarificationProtocol("执行取证", pipeline, step)
	for _, want := range []string{
		"证据降级协议",
		"工具失败",
		"报告、方案、计划书、BP 等领域名词本身不等于",
		"用户未明确指定文件或格式时",
		"禁止输出 INPUT_STATUS: MISSING",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("business-step protocol missing %q:\n%s", want, prompt)
		}
	}
}

func TestAIEmployeeExplicitLateClarificationStepMayStop(t *testing.T) {
	pipeline := Pipeline{
		Name: "lead-research", AIEmployeeID: "lead-researcher",
		AIEmployeeName: "潜客研究员", AIEmployeeAtName: "潜客研究员",
	}
	step := &PipelineStep{Name: "产品画像与SWOT", AllowMissingInput: true}
	raw := "存在两个同名产品，无法确定目标。\n\nINPUT_STATUS: MISSING\n<!--PIPELINE_STOP-->"

	got, suppressed := normalizeAIEmployeeStepWaitingInputOutput(raw, pipeline, step)
	if suppressed {
		t.Fatal("explicit late-clarification step must retain its stop")
	}
	for _, want := range []string{"@潜客研究员", "## 重新发起方式", pipelineWaitingInputMarker, pipelineStopMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("authorized waiting-input output missing %q:\n%s", want, got)
		}
	}

	prompt := appendAIEmployeeClarificationProtocol("核对产品身份", pipeline, step)
	if !strings.Contains(prompt, "AI员工自主执行与必要缺失协议") {
		t.Fatalf("authorized late-clarification step received the wrong protocol:\n%s", prompt)
	}
}

func TestLeadResearchIntakeDoesNotPromoteOldReportToFacts(t *testing.T) {
	pipeline := Pipeline{
		Name: "lead-research",
		Steps: []PipelineStep{
			{Name: "产品画像与SWOT", Tools: []string{"read_file", "list_dir", "glob", "web_search", "web_fetch"}},
			{Name: "搜索策略", NoTools: true},
			{Name: "多源搜索与核验", Tools: []string{"web_search", "web_fetch"}},
			{Name: "报告整理", NoTools: true},
		},
	}
	spec := aiEmployeeIntakeSpec{
		EmployeeID: "lead-researcher", EmployeeName: "潜客研究员", Role: "潜客研究", PipelineName: pipeline.Name,
		OriginalTask: "分析 E:\\adebug2\\0727 的产品并寻找真正的购买方",
		ContextPath:  ".openkitty/storage/.system/ae_context/session.json",
	}
	got := aiemployee.BuildIntakePrompt(pipeline, spec)
	for _, want := range []string{
		"先识别用户要研究的目标产品",
		"后续步骤主动读取和分析细节",
		"不要求用户预先整理完整产品资料或提供本地路径",
		"产品“网上能找到”",
		"不得因为默认工作区没有资料就要求用户补文件",
		"用户明确指定的产品名称是任务边界",
		"产品身份冲突或核心定位无法唯一确定",
		"旧助手回答",
		"待验证历史假设",
		"不得写成“必须复用”的事实",
		"供给方不得因为“与产品相关”就被预分类为购买方",
		"产品画像与 SWOT → 搜索策略 → 多源搜索与核验 → 报告整理",
		"禁止跳过前两步直接搜索",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("lead-research intake prompt missing %q: %s", want, got)
		}
	}
}

func TestLeadResearchBusinessStepDoesNotReadRuntimeSessionArchive(t *testing.T) {
	contextPath := ".openkitty/storage/.system/ae_context/session.json"
	pipeline := Pipeline{
		Name: "lead-research",
		Steps: []PipelineStep{
			{Name: "产品画像与SWOT", Tools: []string{"read_file", "list_dir", "glob"}},
			{Name: "搜索策略", NoTools: true},
		},
	}
	got := prependAIEmployeeIntakeStep(pipeline, aiEmployeeIntakeSpec{
		EmployeeID:   "lead-researcher",
		EmployeeName: "潜客研究员",
		Role:         "潜客研究",
		PipelineName: pipeline.Name,
		OriginalTask: "帮 OpenKitty 寻找购买方",
		ContextPath:  contextPath,
	})
	if len(got.Steps) < 2 {
		t.Fatalf("unexpected lead pipeline: %#v", got.Steps)
	}
	if strings.Contains(got.Steps[1].Prompt, contextPath) {
		t.Fatalf("lead product evidence step was instructed to read runtime session archive: %s", got.Steps[1].Prompt)
	}
}

func TestExtractPipelineArtifactsIgnoresIntakeDeliveryPlans(t *testing.T) {
	workspace := t.TempDir()
	created := filepath.Join(workspace, ".openkitty", "storage", "created.md")
	if err := os.MkdirAll(filepath.Dir(created), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("created in this run"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{ResolvedWorkspace: workspace, Steps: []StepResult{
		{Name: aiEmployeeIntakeStepName, Status: "completed", Output: "计划交付 .openkitty/storage/not-created.md"},
		{
			Name:   "业务交付",
			Status: "completed",
			Output: "已生成 .openkitty/storage/created.md",
		},
	}}
	artifacts := extractPipelineArtifacts(exec)
	if len(artifacts) != 1 || artifacts[0].Path != ".openkitty/storage/created.md" {
		t.Fatalf("intake plan leaked as artifact: %#v", artifacts)
	}
}
