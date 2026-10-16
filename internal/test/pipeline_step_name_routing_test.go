package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPPTPolishHostRoutingFollowsStepNamesAfterIntakeSlot(t *testing.T) {
	workspace := t.TempDir()
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}}
	execution := &PipelineExecution{
		ID:           "pipe_ppt_shifted",
		PipelineName: "ppt-polish",
		Steps: []StepResult{
			{Name: "任务受理与意图理解", Status: "completed"},
			{Name: "素材读取", Status: "completed", Output: "## PPTX 素材宿主解析\n- 现有页数：5\n\n## 逐页可编辑文字\n\n# Slide 1\n\n标题"},
			{Name: "内容重构", Status: "running"},
			{Name: "视觉润色", Status: "pending"},
			{Name: "交付自查", Status: "pending"},
		},
	}

	output, ready, err := executor.hostRunPPTPolishStep(nil, execution, &PipelineStep{Name: "内容重构"}, 2, "")
	if err != nil || !ready {
		t.Fatalf("shifted PPT content step did not use host route: ready=%v err=%v output=%s", ready, err, output)
	}
	if !strings.Contains(output, "规划页数：5") {
		t.Fatalf("shifted material slot was not read: %s", output)
	}
	brief := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_pipe_ppt_shifted.md")
	if _, err := os.Stat(brief); err != nil {
		t.Fatalf("PPT preserve brief missing: %v", err)
	}
}

func TestPPTPolishDetailedRequestHostFallbackSurvivesEmptyMaterialModel(t *testing.T) {
	workspace := t.TempDir()
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}}
	request := `请直接制作固定 4 页、可编辑的管理层 PPTX，文件名 request_only_resilience.pptx，不要联网。
主题：通用 AgentOS 能力增强
受众：公司管理层与产品、研发负责人
视觉：暖白背景、深海军蓝主色，正文至少 14pt。
1. 封面：说明从可用走向可信交付。
2. 目标对比：保留任务成功率 72% 到 90%。
3. 核心诊断：说明任务边界、工具恢复和质量验收。
4. 下一步：列出 30、60、90 天决策。`
	execution := &PipelineExecution{
		ID:           "pipe_request_only_resilience",
		PipelineName: "ppt-polish",
		Input:        request,
		Steps: []StepResult{
			{Name: "任务受理与意图理解", Status: "completed"},
			{Name: "素材读取", Status: "running"},
			{Name: "内容重构", Status: "pending"},
			{Name: "视觉润色", Status: "pending"},
			{Name: "交付自查", Status: "pending"},
		},
	}

	material, ready, err := executor.hostRunPPTPolishStep(nil, execution, &PipelineStep{Name: "素材读取"}, 1, request)
	if err != nil || !ready {
		t.Fatalf("request-only material fallback did not activate: ready=%v err=%v output=%s", ready, err, material)
	}
	for _, want := range []string{"PPT_INPUT_MODE: detailed_brief", "用户当前请求（逐字保留）", "固定 4 页", "任务成功率 72% 到 90%"} {
		if !strings.Contains(material, want) {
			t.Fatalf("request-only material fallback dropped %q:\n%s", want, material)
		}
	}

	execution.Steps[1].Status = "completed"
	execution.Steps[1].Output = material
	briefOutput, ready, err := executor.hostRunPPTPolishStep(nil, execution, &PipelineStep{Name: "内容重构"}, 2, material)
	if err != nil || !ready {
		t.Fatalf("detailed request brief fallback did not activate: ready=%v err=%v output=%s", ready, err, briefOutput)
	}
	if !strings.Contains(briefOutput, "规划页数：4") || !strings.Contains(briefOutput, "未使用外部来源或补造业务事实") {
		t.Fatalf("detailed request brief receipt is incomplete: %s", briefOutput)
	}
	briefPath := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_pipe_request_only_resilience.md")
	brief, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatalf("read request-only content brief: %v", err)
	}
	briefText := string(brief)
	for _, want := range []string{"### Slide 01 — 封面", "### Slide 04 — 下一步", "任务成功率 72% 到 90%", "保留的原始数据与引用"} {
		if !strings.Contains(briefText, want) {
			t.Fatalf("request-only content brief dropped %q:\n%s", want, briefText)
		}
	}
	if got := pptPolishAudiencePageCount(briefText); got != 4 {
		t.Fatalf("request-only brief page count=%d, want 4", got)
	}
}

func TestPPTPolishRequestHostFallbackDefersToExternalMaterialTools(t *testing.T) {
	workspace := t.TempDir()
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}}
	for _, request := range []string{
		"请基于 https://example.com/source 制作 6 页 PPTX。",
		"请润色 source.pdf 并输出 polished_result.pptx。",
	} {
		execution := &PipelineExecution{
			ID:           "pipe_external_material",
			PipelineName: "ppt-polish",
			Input:        request,
			Steps:        []StepResult{{Name: "素材读取", Status: "running"}},
		}
		output, ready, err := executor.hostRunPPTPolishStep(nil, execution, &PipelineStep{Name: "素材读取"}, 0, request)
		if err != nil || ready || output != "" {
			t.Fatalf("external material was incorrectly treated as request-only prose: request=%q ready=%v err=%v output=%s", request, ready, err, output)
		}
	}
}

func TestPipelineStepResultIndexUsesSemanticName(t *testing.T) {
	execution := &PipelineExecution{Steps: []StepResult{{Name: "任务受理与意图理解"}, {Name: "素材读取"}}}
	if got := pipelineStepResultIndex(execution, "素材读取"); got != 1 {
		t.Fatalf("semantic step index = %d, want 1", got)
	}
	if got := pipelineStepResultIndex(execution, "不存在"); got != -1 {
		t.Fatalf("missing semantic step index = %d, want -1", got)
	}
}

func TestPPTPolishDeliveryRejectsUnchangedSourceDeck(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.pptx")
	if err := os.WriteFile(source, []byte("not-needed-for-no-op-guard"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}}
	execution := &PipelineExecution{
		ID:           "pipe_noop",
		PipelineName: "ppt-polish",
		Steps: []StepResult{
			{Name: "素材读取", Status: "completed", Output: "素材路径：" + source},
			{Name: "内容重构", Status: "completed", Output: "brief"},
			{Name: "视觉润色", Status: "completed", Output: "PPTX真实路径：" + source},
			{Name: "交付自查", Status: "running"},
		},
	}

	_, ready, err := executor.hostValidatePPTPolish(nil, execution, 3, source)
	if err == nil || ready || !strings.Contains(err.Error(), "unchanged source deck") {
		t.Fatalf("source deck was accepted as polished delivery: ready=%v err=%v", ready, err)
	}
}

func TestPPTPolishWorkspacePathsResolvePerExecution(t *testing.T) {
	workspace := t.TempDir()
	brief := filepath.Join(workspace, ".openkitty", "storage", "brief.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("# brief"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved := firstExistingWorkspacePathWithExtension("制作简报：.openkitty/storage/brief.md", ".md", workspace)
	if !sameLocalPath(resolved, brief) {
		t.Fatalf("relative brief path resolved to %q, want %q", resolved, brief)
	}
	resolved = firstExistingWorkspacePathWithExtension("真实 Markdown 路径：`./.openkitty/storage/brief.md`", ".md", workspace)
	if !sameLocalPath(resolved, brief) {
		t.Fatalf("dot-relative brief path resolved to %q, want %q", resolved, brief)
	}
	target := pptPolishDefaultOutputPath(&PipelineExecution{ID: "pipe_ppt_123"}, workspace)
	if !strings.HasSuffix(filepath.ToSlash(target), "/.openkitty/storage/ppt_polished_pipe_ppt_123.pptx") {
		t.Fatalf("unexpected per-execution output path: %s", target)
	}
}

func TestPPTPolishHostRouteIsConnectedToExecutionLoop(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("internal", "app", "pipeline.go"))
	if err != nil {
		t.Fatal(err)
	}
	call := "hostRunPPTPolishStep(ctx, exec, step, i, stepInput)"
	if !strings.Contains(string(data), call) {
		t.Fatalf("PPT host route exists but is not connected to the execution loop: missing %s", call)
	}
}
