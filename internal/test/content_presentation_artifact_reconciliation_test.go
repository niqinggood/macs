package app

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMinimalPresentationPPTX(t *testing.T, path string) {
	writePresentationPPTXWithSlides(t, path, 1)
}

func writePresentationPPTXWithSlides(t *testing.T, path string, slides int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range map[string]string{
		"[Content_Types].xml":             `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"ppt/presentation.xml":            `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`,
		"padding.bin":                     strings.Repeat("x", 2048),
	} {
		entry, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	for index := 1; index <= slides; index++ {
		entry, createErr := archive.Create(fmt.Sprintf("ppt/slides/slide%d.xml", index))
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPresentationArtifactReconciliationRejectsTooFewSlides(t *testing.T) {
	workspace := t.TempDir()
	ctx := presentationWorkspaceTestContext(workspace, "请生成 10 页 PPT")
	path := filepath.Join(workspace, "short.pptx")
	writePresentationPPTXWithSlides(t, path, 5)

	recordPresentationSuccess(ctx, ToolCall{
		ID:   "build-short-deck",
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `print("` + filepath.ToSlash(path) + `")`,
		},
	}, "RC: 0\nEXISTS: True")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" {
		t.Fatalf("short deck should be blocked: %#v ok=%v", acceptance, ok)
	}
	if issues := strings.Join(acceptance.BlockingIssues, " | "); !strings.Contains(issues, "实际只有 5 页") {
		t.Fatalf("short-deck issue missing actual slide count: %s", issues)
	}
}

func presentationWorkspaceTestContext(workspace, input string) context.Context {
	ctx := withResolvedWorkspace(context.Background(), workspace)
	return withPresentationProductionState(ctx, input)
}

func TestPresentationArtifactReconciliationAcceptsValidCurrentTurnCandidate(t *testing.T) {
	workspace := t.TempDir()
	ctx := presentationWorkspaceTestContext(workspace, "Please create a quick internal PPT draft; skip strict QA")
	path := filepath.Join(workspace, ".openkitty", "storage", "fde_intro.pptx")
	writeMinimalPresentationPPTX(t, path)

	recordPresentationSuccess(ctx, ToolCall{
		ID:   "build-current-turn",
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `from pptx import Presentation
p = "` + filepath.ToSlash(path) + `"
print(p)`,
		},
	}, "RC: 0\nEXISTS: True SIZE: 49644")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "partial" {
		t.Fatalf("valid current-turn PPTX should be deliverable with optional QA warnings: %#v ok=%v", acceptance, ok)
	}
	if len(acceptance.BlockingIssues) != 0 {
		t.Fatalf("valid current-turn PPTX was blocked: %v", acceptance.BlockingIssues)
	}
	if !samePresentationPath(acceptance.DeckPath, ".openkitty/storage/fde_intro.pptx") {
		t.Fatalf("unexpected reconciled path: %q", acceptance.DeckPath)
	}
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.DeckCreated || state.AuditPassed || state.AuditEvidence != "" {
		t.Fatalf("artifact reconciliation was confused with an explicit audit: %#v", state)
	}
}

func TestPresentationArtifactReconciliationRejectsStaleCandidate(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".openkitty", "storage", "old.pptx")
	writeMinimalPresentationPPTX(t, path)
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	ctx := presentationWorkspaceTestContext(workspace, "Please create a PPT")

	recordPresentationSuccess(ctx, ToolCall{
		ID:   "inspect-stale",
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `print("` + filepath.ToSlash(path) + `")`,
		},
	}, "status: success")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || len(acceptance.BlockingIssues) == 0 {
		t.Fatalf("stale PPTX must not become the current-turn deliverable: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationArtifactReconciliationRejectsCrossWorkspaceCandidate(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.pptx")
	ctx := presentationWorkspaceTestContext(workspace, "Please create a PPT")
	writeMinimalPresentationPPTX(t, outside)

	recordPresentationSuccess(ctx, ToolCall{
		ID:   "inspect-outside",
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `print("` + filepath.ToSlash(outside) + `")`,
		},
	}, "status: success")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || len(acceptance.BlockingIssues) == 0 {
		t.Fatalf("cross-workspace PPTX must remain blocked: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationArtifactReconciliationRejectsCorruptCandidate(t *testing.T) {
	workspace := t.TempDir()
	ctx := presentationWorkspaceTestContext(workspace, "Please create a PPT")
	path := filepath.Join(workspace, ".openkitty", "storage", "broken.pptx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("not-a-pptx", 200)), 0o644); err != nil {
		t.Fatal(err)
	}

	recordPresentationSuccess(ctx, ToolCall{
		ID:   "inspect-corrupt",
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `print("` + filepath.ToSlash(path) + `")`,
		},
	}, "status: success")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || len(acceptance.BlockingIssues) == 0 {
		t.Fatalf("corrupt PPTX must remain blocked: %#v ok=%v", acceptance, ok)
	}
}

func TestHostRecoverPPTPolishBriefAfterModelFailureUsesExactValidatedArtifact(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe_ppt-polish_recovery_123",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps:             []StepResult{{Name: "内容重构"}},
	}
	step := &PipelineStep{Name: "内容重构"}
	briefPath := filepath.Join(
		workspace,
		".openkitty",
		"storage",
		"ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md",
	)
	brief := `# FDE 分享制作简报

### Page 1 — 封面
- **唯一核心观点**: FDE 让企业软件真正进入业务现场。
- **支持内容**: 面向普通分享。

### Page 2 — 为什么需要 FDE
- **唯一核心观点**: 产品能力需要结合真实流程才能产生价值。
- **支持内容**: 理解业务；连接数据；推动落地。

### Page 3 — 日常工作
- **唯一核心观点**: FDE 同时连接客户、产品和工程团队。
- **支持内容**: 需求澄清；快速验证；上线复盘。

` + strings.Repeat("补充说明用于验证文件具备真实内容。\n", 40)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}

	output, ready, err := (&PipelineExecutor{}).hostRecoverPPTPolishBriefAfterModelFailure(
		exec,
		step,
		0,
		errors.New("task failed: blocked or incomplete final answer"),
	)
	if err != nil || !ready {
		t.Fatalf("validated brief should recover the step, ready=%v err=%v", ready, err)
	}
	if !strings.Contains(strings.ToLower(output), strings.ToLower(filepath.Clean(briefPath))) || !strings.Contains(output, "规划页数：3") {
		t.Fatalf("recovery output lost exact artifact evidence: %s", output)
	}
}

func TestHostRecoverPPTPolishBriefAfterModelFailureNeverScansAlternateArtifacts(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe_ppt-polish_exact_path",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps:             []StepResult{{Name: "内容重构"}},
	}
	alternate := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_another_run.md")
	if err := os.MkdirAll(filepath.Dir(alternate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternate, []byte(strings.Repeat("valid-looking alternate brief\n", 40)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ready, err := (&PipelineExecutor{}).hostRecoverPPTPolishBriefAfterModelFailure(
		exec,
		&PipelineStep{Name: "内容重构"},
		0,
		errors.New("incomplete final answer"),
	)
	if !ready || err == nil {
		t.Fatalf("missing exact artifact must reject recovery, ready=%v err=%v", ready, err)
	}
}

func TestHostMaterializePPTPolishBriefFromStructuredStepOutput(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe_ppt-polish_structured_output",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps:             []StepResult{{Name: "内容重构"}},
	}
	output := `# FDE 分享制作简报

## 逐页标题与核心观点

| # | 标题 | 唯一核心观点 |
|---|---|---|
| 1 | 封面 | FDE 连接技术与业务 |
| 2 | 为什么需要 | 企业需要把通用能力放进真实流程 |
| 3 | 工作方式 | 诊断、原型、协作与复盘形成闭环 |
| 4 | 角色边界 | FDE 同时理解业务并动手交付 |
| 5 | 适用场景 | 复杂业务和深度集成更适合 FDE |

## 单页支持内容

**P1 封面**：面向普通业务同事的快速分享。
**P2 痛点**：模型能力增长很快；企业流程仍有明显落地鸿沟。
**P3 方法**：理解问题；验证方案；推动上线；复盘沉淀。
**P4 边界**：不是纯咨询；不是普通外包；对业务结果负责。
**P5 场景**：复杂流程；跨部门协作；现有系统深度集成。

## 补充说明

` + strings.Repeat("所有事实均来自当前任务素材，未知项保持未知。\n", 30)

	reconciled, ready, err := (&PipelineExecutor{}).hostMaterializePPTPolishBriefFromStepOutput(
		exec,
		&PipelineStep{Name: "内容重构"},
		0,
		output,
	)
	if err != nil || !ready {
		t.Fatalf("structured model output should be reconciled, ready=%v err=%v", ready, err)
	}
	expected := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md")
	data, readErr := os.ReadFile(expected)
	if readErr != nil {
		t.Fatalf("reconciled brief missing: %v", readErr)
	}
	if !strings.Contains(string(data), "单页支持内容") || !strings.Contains(reconciled, "规划页数：5") {
		t.Fatalf("reconciled brief lost structured content: %s\n%s", reconciled, data)
	}
}

func TestHostMaterializePPTPolishBriefRejectsReceiptOnlyOutput(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe_ppt-polish_receipt_only",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps:             []StepResult{{Name: "内容重构"}},
	}
	_, ready, err := (&PipelineExecutor{}).hostMaterializePPTPolishBriefFromStepOutput(
		exec,
		&PipelineStep{Name: "内容重构"},
		0,
		"内容重构已完成，文件已保存到 /workspace/.openkitty/storage/brief.md。",
	)
	if err != nil || ready {
		t.Fatalf("receipt-only output must not be materialized, ready=%v err=%v", ready, err)
	}
	expected := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md")
	if _, statErr := os.Stat(expected); !os.IsNotExist(statErr) {
		t.Fatalf("receipt-only output unexpectedly created an artifact: %v", statErr)
	}
}
