package app

import (
	"context"
	"strings"
	"testing"

	"mac/internal/aiemployee"
)

func TestExplicitAIEmployeeArtifactRequestSeparatesContentTypeFromOutputMedium(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "BP content stays in chat", input: "请给我做一个 BP 计划书", want: false},
		{name: "review noun stays in chat", input: "评价并修改我的 BP 摘要，给出尽调清单和投资备忘录", want: false},
		{name: "input file is not output authority", input: "请基于我上传的 BP 文件评价问题并修改内容", want: false},
		{name: "negative file directive stays in chat", input: "请主动联网检索并写 BP；不要生成 PPTX、PDF、Word 或文件，完整内容直接在聊天中交付", want: false},
		{name: "explicit markdown save", input: "请把 BP 保存为 Markdown 文件", want: true},
		{name: "explicit PDF format", input: "请给我一份 PDF 格式的投资备忘录", want: true},
		{name: "explicit file mutation", input: "把这个 BP 文件修改并覆盖保存", want: true},
		{name: "compound negative then positive", input: "不要生成 Word，改为输出 Markdown 文件", want: true},
		{name: "format after descriptive comma", input: "生成一份带目录、附录的 PDF", want: true},
		{name: "described markdown then save", input: "请把五项整理成一份有实质内容、可直接填写的 Markdown 融资准备清单，并保存到 .openkitty/storage/checklist.md", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aiemployee.ExplicitArtifactRequest(tt.input); got != tt.want {
				t.Fatalf("explicitAIEmployeeArtifactRequest(%q)=%v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAIEmployeeArtifactIntentDoesNotActAsAPermissionGate(t *testing.T) {
	direct := &DirectConfig{
		Tools:          []string{"web_search", "persistent_shell", "write_file"},
		ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest,
	}
	chatTools := aiemployee.DirectToolsForRequest(direct, "评价这份 BP 并直接回答")
	for _, want := range []string{"web_search", "persistent_shell", "write_file"} {
		if !containsString(chatTools, want) {
			t.Fatalf("configured tool %q was removed by artifact-intent inference: %v", want, chatTools)
		}
	}
	fileTools := aiemployee.DirectToolsForRequest(direct, "把结果保存为 Markdown 文件")
	if !containsString(fileTools, "write_file") {
		t.Fatalf("explicit artifact request lost write_file: %v", fileTools)
	}

	ctx := aiemployee.WithOutputPolicy(context.Background(), direct, "评价这份 BP 并直接回答", false)
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{Name: "write_file"}); blocked {
		t.Fatalf("write_file was blocked by inferred artifact intent: %s", reason)
	}
	if blocked, _ := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name:      "persistent_shell",
		Arguments: map[string]interface{}{"command": "printf report > result.md"},
	}); blocked {
		t.Fatal("shell file write was blocked by inferred artifact intent")
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name:      "persistent_shell",
		Arguments: map[string]interface{}{"command": "pwd"},
	}); blocked {
		t.Fatalf("read-only shell command was blocked: %s", reason)
	}

	artifactCtx := aiemployee.WithOutputPolicy(
		context.Background(),
		direct,
		"把结果保存到 .openkitty/storage/checklist.md",
		true,
	)
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(artifactCtx, ToolCall{
		Name: "persistent_shell",
		Arguments: map[string]interface{}{
			"command": "cat > .openkitty/storage/checklist.md <<'EOF'\ncontent\nEOF",
		},
	}); blocked {
		t.Fatalf("artifact-intent heuristic blocked a configured shell write: %s", reason)
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(artifactCtx, ToolCall{
		Name:      "persistent_shell",
		Arguments: map[string]interface{}{"command": "wc -c .openkitty/storage/checklist.md"},
	}); blocked {
		t.Fatalf("read-only artifact verification was blocked: %s", reason)
	}
}

func TestMarketAnalystArtifactRequestSeparatesResearchFromGuaranteedDelivery(t *testing.T) {
	direct := &DirectConfig{
		Prompt:         "research with sources",
		Tools:          []string{"web_search", "web_fetch", "write_file"},
		MaxIter:        12,
		ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest,
	}
	pipeline := buildAIEmployeeDirectPipeline(
		"market-analyst",
		"direct_market-analyst",
		direct,
		direct.Tools,
		direct.MaxIter,
		"分析企业 AI 趋势并保存一份可下载的 Markdown 报告。",
	)
	if len(pipeline.Steps) != 2 {
		t.Fatalf("market report pipeline has %d steps, want evidence research plus delivery", len(pipeline.Steps))
	}
	research, delivery := pipeline.Steps[0], pipeline.Steps[1]
	if containsString(research.Tools, "write_file") || research.MaxIter < 12 {
		t.Fatalf("research step did not reserve delivery from file writes or has insufficient budget: %#v", research)
	}
	if len(delivery.Tools) != 1 || delivery.Tools[0] != "write_file" || delivery.ArtifactContract == nil {
		t.Fatalf("market delivery step is not isolated and contracted: %#v", delivery)
	}
	contract := delivery.ArtifactContract
	if !contract.MaterializeOutput || contract.MinBytes < 800 || len(contract.Paths) != 1 ||
		!strings.Contains(contract.Paths[0], "${execution_id}") {
		t.Fatalf("market delivery contract cannot guarantee a run-scoped report: %#v", contract)
	}

	chatOnly := buildAIEmployeeDirectPipeline(
		"market-analyst",
		"direct_market-analyst",
		direct,
		aiemployee.DirectToolsForRequest(direct, "企业 AI 趋势怎么看？"),
		direct.MaxIter,
		"企业 AI 趋势怎么看？",
	)
	if len(chatOnly.Steps) != 1 {
		t.Fatalf("ordinary market question was forced into file delivery: %#v", chatOnly.Steps)
	}
}

func TestAIEmployeeFundraisingEvidenceReviewDoesNotBlockArtifactWrite(t *testing.T) {
	direct := &DirectConfig{
		Tools:          []string{"write_file"},
		ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest,
	}
	base := context.WithValue(context.Background(), "ae_employee_id", "fundraising-advisor")
	ctx := aiemployee.WithOutputPolicy(base, direct, "把融资清单保存为 Markdown 文件；只使用我给出的项目，不新增行业门槛", true)
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    ".openkitty/storage/checklist.md",
			"content": "财务模型\n- 时间区间：通常 12–36 个月。",
		},
	}); blocked {
		t.Fatalf("content heuristic blocked an artifact write: %s", reason)
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    ".openkitty/storage/checklist.md",
			"content": "财务模型\n- 时间区间：待确认。",
		},
	}); blocked {
		t.Fatalf("evidence-bounded artifact was blocked: %s", reason)
	}
}

func TestFundraisingPresentationStepRequiresPerDeckBuilder(t *testing.T) {
	step := fundraisingBPPresentationStep()
	for _, want := range []string{
		"scaffold_task_pptx.py",
		"fundraising_bp_work_${execution_id}/build_deck.py",
		"不得调用 build_task_pptx.py",
		"至少使用 5 种真正不同的构图家族",
		"检查 contact_sheet.jpg 的每一页",
	} {
		if !strings.Contains(step.Prompt, want) {
			t.Fatalf("presentation per-deck build contract missing %q: %s", want, step.Prompt)
		}
	}
	if step.MaxIter != 80 {
		t.Fatalf("fundraising presentation iteration budget = %d, want 80", step.MaxIter)
	}
	for _, tool := range []string{"read_file", "edit_file", "persistent_shell", "vision"} {
		if !containsString(step.Tools, tool) {
			t.Fatalf("fundraising presentation step missing %q: %v", tool, step.Tools)
		}
	}
	contract := step.ArtifactContract
	if contract == nil || contract.MinSlides < 10 || contract.MinVisibleChars < 1200 || contract.MinVisibleChars > 1800 || len(contract.RequiredAnyText) != 0 ||
		!contract.RequireCurrencyAmount || !contract.RequirePercentage || contract.MaxPlaceholderMentions <= 0 {
		t.Fatalf("fundraising deck lacks an executable audience-facing artifact contract: %#v", contract)
	}
}

func TestAIEmployeeFundraisingContentPolicyDoesNotActAsToolPermissionGate(t *testing.T) {
	direct := &DirectConfig{
		Tools:          []string{"read_file", "persistent_shell"},
		ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest,
	}
	base := context.WithValue(context.Background(), "ae_employee_id", "fundraising-advisor")
	ctx := aiemployee.WithOutputPolicy(base, direct, "请基于我给出的链接制作一份 BP 计划书", false)

	for _, call := range []ToolCall{
		{
			Name: "read_file",
			Arguments: map[string]interface{}{
				"path": ".openkitty/runtime/builtin/business-finance/huanzhi-fa-fundraising/references/fundraising-side.md",
			},
		},
		{
			Name: "persistent_shell",
			Arguments: map[string]interface{}{
				"command": "python .openkitty/runtime/builtin/business-finance/skill/scripts/funding_diagnosis.py input.json",
			},
		},
		{
			Name: "read_file",
			Arguments: map[string]interface{}{
				"path": ".openkitty/runtime/tool-results/web_fetch_123.log",
			},
		},
	} {
		if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, call); blocked {
			t.Fatalf("content policy blocked a configured tool call %#v: %s", call, reason)
		}
	}

	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name:      "read_file",
		Arguments: map[string]interface{}{"path": "customer-provided-bp.md"},
	}); blocked {
		t.Fatalf("user material read was blocked: %s", reason)
	}
}

func TestFundraisingDefaultBPDeckAuthorizesOnlyThePresentationArtifactPath(t *testing.T) {
	direct := &DirectConfig{
		Tools:          []string{"web_search", "persistent_shell", "write_file"},
		ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest,
	}
	task := "你研究下 OpenKitty 这个通用可私有化部署的 Agent，给我做一个 BP 计划书。"
	tools := aiemployee.DirectToolsForRequest(direct, task)
	for _, want := range []string{"persistent_shell", "write_file"} {
		if !containsString(tools, want) {
			t.Fatalf("default BP deck removed %q: %v", want, tools)
		}
	}

	base := context.WithValue(context.Background(), "ae_employee_id", "fundraising-advisor")
	ctx := aiemployee.WithOutputPolicy(base, direct, task, aiemployee.ExplicitArtifactRequest(task))
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "persistent_shell",
		Arguments: map[string]interface{}{
			"command": `python3 ".openkitty/runtime/builtin/presentation/ppt-art-director/scripts/scaffold_task_pptx.py" --builder ".openkitty/storage/fundraising_bp_work_x/build_deck.py" --output ".openkitty/storage/fundraising_bp_x.pptx"`,
		},
	}); blocked {
		t.Fatalf("authorized presentation renderer was blocked: %s", reason)
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    ".openkitty/storage/fundraising_bp_work_x/build_deck.py",
			"content": `OUTPUT_PPTX = Path(".openkitty/storage/fundraising_bp_x.pptx")`,
		},
	}); blocked {
		t.Fatalf("run-scoped presentation builder write was blocked: %s", reason)
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    ".openkitty/storage/fundraising_bp_work_x/deck_spec.json",
			"content": `{"needs_customization":false,"slides":[{"kind":"cover","title":"市场第一"}]}`,
		},
	}); blocked {
		t.Fatalf("run-scoped presentation content specification was re-validated as a final report: %s", reason)
	}
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(ctx, ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    ".openkitty/storage/fundraising_bp_spec_pipe_direct_fundraising-advisor_20260801_060520_abc.json",
			"content": `{"needs_customization":false,"slides":[{"kind":"cards","title":"规划估算","items":[{"title":"融资用途","body":"建议融资 1000 万元，研发 45%"}]}]}`,
		},
	}); blocked {
		t.Fatalf("current execution-scoped presentation spec was treated as a final evidence report: %s", reason)
	}
	chatOnly := "请直接在聊天中生成完整 BP，不要保存文件。"
	chatTools := aiemployee.DirectToolsForRequest(direct, chatOnly)
	if !containsString(chatTools, "write_file") {
		t.Fatalf("artifact intent inference removed configured write_file: %v", chatTools)
	}
	chatCtx := aiemployee.WithOutputPolicy(base, direct, chatOnly, false)
	if blocked, reason := aiemployee.OutputPolicyBlocksTool(chatCtx, ToolCall{Name: "write_file"}); blocked {
		t.Fatalf("AI employee artifact heuristic blocked write_file: %s", reason)
	}
}
