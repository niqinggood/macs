package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// blackBoxTaskTool is intentionally small: the tests drive the same tool
// registry, safety gate, evidence recorder, pipeline contracts and fallback
// paths used by a real agent turn without depending on a network model.
type blackBoxTaskTool struct {
	name string
}

func (t *blackBoxTaskTool) Name() string        { return t.name }
func (t *blackBoxTaskTool) Description() string { return "black-box task fixture" }
func (t *blackBoxTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": true}
}
func (t *blackBoxTaskTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	switch t.name {
	case "use_skill":
		return "技能: ppt-art-director\n运行时目录: C:\\runtime\\ppt-art-director", nil
	case "cli":
		command, _ := params["command"].(string)
		lower := strings.ToLower(command)
		switch {
		case strings.Contains(lower, "scaffold_task_pptx.py"):
			return `{"run_dir":".openkitty/storage/ppt_runs/blackbox-1","builder":".openkitty/storage/ppt_runs/blackbox-1/build_deck.py","output":"E:/deliverables/final.pptx"}`, nil
		case strings.Contains(lower, "audit_pptx.py"):
			return `{"pptx_structure_valid":true,"slide_count":15}`, nil
		case strings.Contains(lower, "render_pptx.ps1"):
			return `{"ok":true,"presentation_rendered":true,"render_fidelity":"native","slide_count":15}`, nil
		case strings.Contains(lower, "validate_aesthetic_qa.py"):
			return `{"aesthetic_qa_passed":true,"deck":"E:/deliverables/final.pptx","slide_count":15}`, nil
		case strings.Contains(lower, "build_deck.py"):
			return "status: success\nsaved E:/deliverables/final.pptx", nil
		default:
			return "status: success", nil
		}
	case "vision":
		if result, _ := params["mock_result"].(string); strings.TrimSpace(result) != "" {
			return result, nil
		}
		return "渲染页面清晰，未发现裁切、重叠或占位符。", nil
	case "code_check":
		return `{"ok":true,"receipt_id":"publish-blackbox-1"}`, nil
	default:
		return "ok", nil
	}
}

func runBlackBoxTool(executor *AgentToolExecutor, ctx context.Context, id, name string, args map[string]interface{}) AgentToolRunResult {
	return executor.RunSingleTool(ctx, ToolCall{ID: id, Name: name, Arguments: args}, RunSingleToolOptions{})
}

func TestBlackBoxLongPresentationTaskKeepsEvidenceScopedAndRecovers(t *testing.T) {
	registry := NewToolRegistry()
	for _, name := range []string{"use_skill", "cli", "read_file", "edit_file", "vision", "code_check"} {
		registry.Register(&blackBoxTaskTool{name: name})
	}
	agent := &LLMAgent{name: "black-box-presentation", toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)
	ctx := withPresentationProductionState(context.Background(), `请输出 E:\deliverables\final.pptx，参考 E:\examples\reference.pptx 制作 15 页路演稿；严格验收后发布`)

	blockedBuild := runBlackBoxTool(executor, ctx, "build-before-skill", "cli", map[string]interface{}{
		"command": `python build_deck.py --output E:\deliverables\final.pptx`,
	})
	if blockedBuild.Err == nil || !strings.Contains(blockedBuild.Result.Content, "ppt-art-director") {
		t.Fatalf("production gate did not stop a pre-skill build: err=%v result=%s", blockedBuild.Err, blockedBuild.Result.Content)
	}

	if run := runBlackBoxTool(executor, ctx, "skill", "use_skill", map[string]interface{}{"skill_name": "ppt-art-director"}); !ToolResultExecutionSucceeded(run.Result, run.Err) {
		t.Fatalf("skill activation failed: err=%v result=%s", run.Err, run.Result.Content)
	}
	blockedBeforeScaffold := runBlackBoxTool(executor, ctx, "build-before-scaffold", "cli", map[string]interface{}{
		"command": `python build_deck.py --output E:\deliverables\final.pptx`,
	})
	if blockedBeforeScaffold.Err == nil || !strings.Contains(blockedBeforeScaffold.Result.Content, "scaffold") {
		t.Fatalf("level-2 scaffold boundary was bypassed: err=%v result=%s", blockedBeforeScaffold.Err, blockedBeforeScaffold.Result.Content)
	}

	for _, call := range []struct {
		id   string
		name string
		args map[string]interface{}
	}{
		{"render-reference", "cli", map[string]interface{}{"command": `powershell -File render_pptx.ps1 -InputPptx E:\examples\reference.pptx -OutputDir reference-preview`}},
		{"inspect-reference", "vision", map[string]interface{}{"path": `reference-preview\contact_sheet.jpg`}},
		{"scaffold", "cli", map[string]interface{}{"command": `python scaffold_task_pptx.py --builder .openkitty\storage\ppt_runs\blackbox-1\build_deck.py --output E:\deliverables\final.pptx`}},
		{"read-builder", "read_file", map[string]interface{}{"path": `.openkitty\storage\ppt_runs\blackbox-1\build_deck.py`}},
		{"customize-builder", "edit_file", map[string]interface{}{"path": `.openkitty\storage\ppt_runs\blackbox-1\build_deck.py`, "old_string": "TASK_PAGE_RENDERERS = {}", "new_string": "TASK_PAGE_RENDERERS = {1: render_slide_01}"}},
		{"build", "cli", map[string]interface{}{"command": `python .openkitty\storage\ppt_runs\blackbox-1\build_deck.py`}},
		// Evidence from another file must not contaminate the final deck.
		{"audit-wrong", "cli", map[string]interface{}{"command": `python audit_pptx.py E:\deliverables\other.pptx --min-body-font-size 16 --min-title-font-size 35`}},
		{"render-wrong", "cli", map[string]interface{}{"command": `powershell -File render_pptx.ps1 -InputPptx E:\deliverables\other.pptx -OutputDir wrong-preview`}},
		{"audit-final", "cli", map[string]interface{}{"command": `python audit_pptx.py E:\deliverables\final.pptx --min-body-font-size 16 --min-title-font-size 35`}},
		{"render-final", "cli", map[string]interface{}{"command": `powershell -File render_pptx.ps1 -InputPptx E:\deliverables\final.pptx -OutputDir final-preview`}},
	} {
		run := runBlackBoxTool(executor, ctx, call.id, call.name, call.args)
		if !ToolResultExecutionSucceeded(run.Result, run.Err) {
			t.Fatalf("%s failed: err=%v result=%s", call.id, run.Err, run.Result.Content)
		}
	}

	for _, slide := range []int{1, 2, 3, 4, 6, 7, 8, 9, 15} {
		run := runBlackBoxTool(executor, ctx, fmt.Sprintf("inspect-%02d", slide), "vision", map[string]interface{}{
			"path": fmt.Sprintf(`final-preview\slide_%02d.png`, slide),
		})
		if !ToolResultExecutionSucceeded(run.Result, run.Err) {
			t.Fatalf("slide %d inspection failed: %v", slide, run.Err)
		}
	}
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "只覆盖部分") {
		t.Fatalf("strict task accepted a partial 9/15 inspection: %#v ok=%v", acceptance, ok)
	}

	publishArgs := map[string]interface{}{
		"action": "run", "file": `C:\runtime\nova-community\scripts\chatroom_agent.py`, "args": `publish --intent-id blackbox`,
	}
	if run := runBlackBoxTool(executor, ctx, "publish-too-early", "code_check", publishArgs); run.Err == nil {
		t.Fatalf("strict delivery escaped before full QA: %s", run.Result.Content)
	}

	if run := runBlackBoxTool(executor, ctx, "inspect-contact", "vision", map[string]interface{}{
		"path": `final-preview\contact_sheet.jpg`,
	}); !ToolResultExecutionSucceeded(run.Result, run.Err) {
		t.Fatalf("contact-sheet inspection failed: %v", run.Err)
	}
	for _, slide := range []int{5, 10, 11, 12, 13, 14} {
		run := runBlackBoxTool(executor, ctx, fmt.Sprintf("inspect-%02d", slide), "vision", map[string]interface{}{
			"path": fmt.Sprintf(`final-preview\slide_%02d.png`, slide),
		})
		if !ToolResultExecutionSucceeded(run.Result, run.Err) {
			t.Fatalf("remaining slide %d inspection failed: %v", slide, run.Err)
		}
	}
	if run := runBlackBoxTool(executor, ctx, "aesthetic-qa", "cli", map[string]interface{}{
		"command": `python C:\runtime\builtin\presentation\ppt-art-director\scripts\validate_aesthetic_qa.py --qa E:\deliverables\aesthetic_qa.json --deck E:\deliverables\final.pptx --render-dir final-preview --expected-slides 15`,
	}); !ToolResultExecutionSucceeded(run.Result, run.Err) {
		t.Fatalf("aesthetic QA failed: err=%v result=%s", run.Err, run.Result.Content)
	}
	if acceptance, _ = presentationAcceptanceSnapshot(ctx); acceptance.Level != "verified" {
		t.Fatalf("contact sheet plus all individual slides should verify the deck: %#v", acceptance)
	}

	// A defect found after an earlier clean overview remains open until a new
	// deck render replaces the stale pixels.
	runBlackBoxTool(executor, ctx, "defect", "vision", map[string]interface{}{
		"path": `final-preview\slide_10.png`, "mock_result": "发现第 10 页文字裁切，需要修正。",
	})
	if acceptance, _ = presentationAcceptanceSnapshot(ctx); acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "尚未修复") {
		t.Fatalf("observed defect did not reopen strict QA: %#v", acceptance)
	}

	for _, call := range []struct {
		id   string
		name string
		args map[string]interface{}
	}{
		{"rebuild", "cli", map[string]interface{}{"command": `python .openkitty\storage\ppt_runs\blackbox-1\build_deck.py`}},
		{"reaudit", "cli", map[string]interface{}{"command": `python audit_pptx.py E:\deliverables\final.pptx --min-body-font-size 16 --min-title-font-size 35`}},
		{"rerender", "cli", map[string]interface{}{"command": `powershell -File render_pptx.ps1 -InputPptx E:\deliverables\final.pptx -OutputDir final-preview-v2`}},
		{"reinspect-contact", "vision", map[string]interface{}{"path": `final-preview-v2\contact_sheet.jpg`}},
	} {
		run := runBlackBoxTool(executor, ctx, call.id, call.name, call.args)
		if !ToolResultExecutionSucceeded(run.Result, run.Err) {
			t.Fatalf("recovery step %s failed: err=%v result=%s", call.id, run.Err, run.Result.Content)
		}
	}
	for slide := 1; slide <= 15; slide++ {
		run := runBlackBoxTool(executor, ctx, fmt.Sprintf("reinspect-%02d", slide), "vision", map[string]interface{}{
			"path": fmt.Sprintf(`final-preview-v2\slide_%02d.png`, slide),
		})
		if !ToolResultExecutionSucceeded(run.Result, run.Err) {
			t.Fatalf("recovery slide %d inspection failed: %v", slide, run.Err)
		}
	}
	if run := runBlackBoxTool(executor, ctx, "reaesthetic-qa", "cli", map[string]interface{}{
		"command": `python C:\runtime\builtin\presentation\ppt-art-director\scripts\validate_aesthetic_qa.py --qa E:\deliverables\aesthetic_qa.json --deck E:\deliverables\final.pptx --render-dir final-preview-v2 --expected-slides 15`,
	}); !ToolResultExecutionSucceeded(run.Result, run.Err) {
		t.Fatalf("recovery aesthetic QA failed: err=%v result=%s", run.Err, run.Result.Content)
	}
	if acceptance, _ = presentationAcceptanceSnapshot(ctx); acceptance.Level != "verified" {
		t.Fatalf("corrected and rerendered deck did not recover: %#v", acceptance)
	}
	if run := runBlackBoxTool(executor, ctx, "publish", "code_check", publishArgs); !ToolResultExecutionSucceeded(run.Result, run.Err) {
		t.Fatalf("verified delivery was not released: err=%v result=%s", run.Err, run.Result.Content)
	}
}

type blackBoxPipelineAgent struct {
	name   string
	output string
}

func (a *blackBoxPipelineAgent) Name() string       { return a.name }
func (a *blackBoxPipelineAgent) GetType() AgentType { return AgentTypeCustom }
func (a *blackBoxPipelineAgent) Generate(context.Context, string) (string, error) {
	return a.output, nil
}

func TestBlackBoxWechatPipelineRepairsAClaimOnlyDeliveryIntoIllustratedArtifacts(t *testing.T) {
	workspace := t.TempDir()
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	creator := func(config AgentConfig, _ string) (Agent, error) {
		output := "交付已完成，HTML 与 Markdown 均已生成。"
		if strings.Contains(config.Name, "素材准备") {
			output = wechatFallbackBriefFixture
		}
		return &blackBoxPipelineAgent{name: config.Name, output: output}, nil
	}
	factory := &AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := (&GatewayService{}).defaultPipelineSet()["wechat-article"]
	execution := &PipelineExecution{
		ID:           "pipe_blackbox_wechat",
		PipelineName: "wechat-article",
		Status:       "running",
		CreatedAt:    time.Now().Add(-time.Second),
		Steps:        make([]StepResult, len(pipeline.Steps)),
	}
	for index := range pipeline.Steps {
		execution.Steps[index] = StepResult{Name: pipeline.Steps[index].Name, Status: "pending", Revision: 1}
	}

	executor.runStepsFrom(context.Background(), execution, pipeline, 0, "围绕 AI Agent 架构升级写一篇原创公众号文章", "blackbox-session", RunModeInteractive)
	if execution.Status != "completed" {
		t.Fatalf("pipeline did not recover the claim-only delivery: status=%s steps=%#v", execution.Status, execution.Steps)
	}
	delivery := execution.Steps[len(execution.Steps)-1]
	if len(delivery.ToolCalls) != 1 || delivery.ToolCalls[0].ToolName != "wechat_article_bundle" {
		t.Fatalf("host recovery did not leave one auditable bundle receipt: %#v", delivery.ToolCalls)
	}

	mdPath := filepath.Join(workspace, ".openkitty", "storage", "wechat_article_pipe_blackbox_wechat.md")
	htmlPath := filepath.Join(workspace, ".openkitty", "storage", "wechat_article_pipe_blackbox_wechat.html")
	mdData, mdErr := os.ReadFile(mdPath)
	htmlData, htmlErr := os.ReadFile(htmlPath)
	if mdErr != nil || htmlErr != nil {
		t.Fatalf("recovered artifacts are missing: md=%v html=%v", mdErr, htmlErr)
	}
	if !strings.Contains(string(mdData), "wechat_cover_") || !strings.Contains(string(htmlData), "data:image/png;base64,") {
		t.Fatal("recovered article is still a text-only HTML instead of a real illustrated bundle")
	}
	if count := wechatRenderableImageCount(string(htmlData)); count < 1 {
		t.Fatalf("artifact contract cannot see the embedded cover: %d", count)
	}
	coverPath := executor.existingWechatArticleCoverPath(".openkitty/storage/wechat_article_pipe_blackbox_wechat.md", mdData)
	if coverPath == "" {
		t.Fatal("recovery/adoption path lost the generated cover location")
	}
	if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(coverPath))); err != nil {
		t.Fatalf("cover receipt points to a missing file: %v", err)
	}
}

func TestBlackBoxWechatImageFailureIsTransactionalAndRecoverable(t *testing.T) {
	workspace := t.TempDir()
	registry := NewToolRegistry()
	registry.Register(NewWechatArticleBundleTool(workspace))
	executor := NewAgentToolExecutor(&LLMAgent{name: "black-box-wechat", toolRegistry: registry})
	args := map[string]interface{}{
		"markdown":     "# 图文任务\n\n正文。\n\n![数据图](.openkitty/storage/assets/chart.png)",
		"md_path":      ".openkitty/storage/recovery.md",
		"html_path":    ".openkitty/storage/recovery.html",
		"execution_id": "blackbox-image-recovery",
	}

	failed := runBlackBoxTool(executor, context.Background(), "missing-image", "wechat_article_bundle", args)
	if ToolResultExecutionSucceeded(failed.Result, failed.Err) || failed.Result.Recovery == nil || failed.Result.Recovery.Code != "wechat_image_unavailable" {
		t.Fatalf("missing image did not produce structured bounded recovery: err=%v result=%#v", failed.Err, failed.Result)
	}
	for _, name := range []string{"recovery.md", "recovery.html"} {
		if _, err := os.Stat(filepath.Join(workspace, ".openkitty", "storage", name)); !os.IsNotExist(err) {
			t.Fatalf("failed bundle published a partial artifact %s: %v", name, err)
		}
	}
	if covers, _ := filepath.Glob(filepath.Join(workspace, ".openkitty", "storage", "wechat_cover_*.png")); len(covers) != 0 {
		t.Fatalf("failed bundle leaked a cover artifact: %v", covers)
	}

	assetDir := filepath.Join(workspace, ".openkitty", "storage", "assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pngBytes, err := renderWechatArticleCoverPNG("Recovered chart")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "chart.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered := runBlackBoxTool(executor, context.Background(), "valid-image", "wechat_article_bundle", args)
	if !ToolResultExecutionSucceeded(recovered.Result, recovered.Err) || !strings.Contains(recovered.Result.Content, `"image_count":2`) {
		t.Fatalf("retry after correcting the image did not succeed: err=%v result=%s", recovered.Err, recovered.Result.Content)
	}
	htmlData, err := os.ReadFile(filepath.Join(workspace, ".openkitty", "storage", "recovery.html"))
	if err != nil || strings.Count(string(htmlData), "data:image/png;base64,") != 2 {
		t.Fatalf("recovered HTML did not embed cover plus body image: err=%v", err)
	}
}
