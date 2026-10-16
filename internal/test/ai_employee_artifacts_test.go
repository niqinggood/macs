package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineResultWithArtifactsAppendsDownloadLinks(t *testing.T) {
	workspace := t.TempDir()
	reportPath := filepath.Join(workspace, ".openkitty", "storage", "report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte("validated report"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{
		ID:                "exec_ae_artifacts",
		PipelineName:      "data-analysis",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{
				Name:   "report",
				Status: "completed",
				Output: "report saved at .openkitty/storage/report.md",
			},
		},
	}

	result := pipelineResultWithArtifacts(exec, "分析完成。")
	for _, want := range []string{
		"分析完成。",
		"交付物：",
		"[report.md](/api/v1/agent/files?path=.openkitty%2Fstorage%2Freport.md)",
		"report",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q\n%s", want, result)
		}
	}
}

func TestPipelineArtifactsRecognizeChineseDeliveryLabels(t *testing.T) {
	workspace := t.TempDir()
	for _, rel := range []string{
		".openkitty/storage/wechat_article_demo.md",
		".openkitty/storage/wechat_article_demo.html",
	} {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("validated artifact"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &PipelineExecution{
		ID:                "pipe_wechat-artifact-links",
		PipelineName:      "wechat-article",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{{
			Name:   "公众号交付物",
			Status: "completed",
			Output: "## 交付文件\n\n- Markdown：.openkitty/storage/wechat_article_demo.md\n- 微信兼容 HTML：.openkitty/storage/wechat_article_demo.html",
		}},
	}

	result := pipelineResultWithArtifacts(exec, "文章正文")
	for _, want := range []string{
		"[wechat_article_demo.md](/api/v1/agent/files?path=.openkitty%2Fstorage%2Fwechat_article_demo.md)",
		"[wechat_article_demo.html](/api/v1/agent/files?path=.openkitty%2Fstorage%2Fwechat_article_demo.html)",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("Chinese delivery label did not become an artifact link %q:\n%s", want, result)
		}
	}
}

func TestPipelineArtifactsIgnoreUnvalidatedOutputPathMentions(t *testing.T) {
	exec := &PipelineExecution{
		ID:                "exec_missing_artifact",
		PipelineName:      "direct_market-analyst",
		ResolvedWorkspace: t.TempDir(),
		Steps: []StepResult{{
			Name:   "respond",
			Status: "completed",
			Output: "本轮未调用 write_file；如需落盘，目标路径是 .openkitty/storage/missing-report.md。",
		}},
	}
	if artifacts := extractPipelineArtifacts(exec); len(artifacts) != 0 {
		t.Fatalf("nonexistent path mention became a download artifact: %#v", artifacts)
	}
	result := pipelineResultWithArtifacts(exec, exec.Steps[0].Output)
	if strings.Contains(result, "/api/v1/agent/files?") || strings.Contains(result, "交付物：") {
		t.Fatalf("nonexistent output path became a user-visible download link: %s", result)
	}
}

func TestPipelineResultUsesHostValidatedWriteFileReceipt(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_verified_write",
		PipelineName: "direct",
		Steps: []StepResult{{
			Name:   "respond",
			Status: "completed",
			Output: "文件已确认存在并按要求保存。",
			ToolCalls: []ToolCallRecord{{
				ToolName: "write_file",
				Status:   "success",
				Artifacts: []ToolCallArtifactRecord{{
					Path:      ".openkitty/storage/fundraising.md",
					Size:      128,
					Validated: true,
				}},
			}},
		}},
	}
	result := pipelineResultWithArtifacts(exec, exec.Steps[0].Output)
	if !strings.Contains(result, "[fundraising.md](/api/v1/agent/files?path=.openkitty%2Fstorage%2Ffundraising.md)") {
		t.Fatalf("validated write_file receipt did not become a delivery link: %s", result)
	}
}

func TestValidatedWriteFilePipelineArtifactRequiresWorkspaceStorageFile(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, ".openkitty", "storage", "fundraising.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("validated content"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, ok := validatedWriteFilePipelineArtifact(&PipelineExecution{ResolvedWorkspace: workspace}, target)
	if !ok || record.Path != ".openkitty/storage/fundraising.md" || !record.Validated || record.Size == 0 {
		t.Fatalf("validated artifact = %#v, ok=%v", record, ok)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := validatedWriteFilePipelineArtifact(&PipelineExecution{ResolvedWorkspace: workspace}, outside); ok {
		t.Fatal("outside-workspace file was accepted as a validated artifact")
	}
}

func TestPipelineArtifactsIncludeValidatedContractFileBuiltByShell(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe_contract_artifact",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{{
			Name: "演示文稿生成", Status: "completed", Output: "PPT 已生成并通过校验。",
		}},
	}
	exec.RuntimePipeline = &Pipeline{Steps: []PipelineStep{{
		Name: "演示文稿生成",
		ArtifactContract: &PipelineArtifactContract{
			Paths: []string{".openkitty/storage/deck_${execution_id}.pptx"}, MinBytes: 8,
		},
	}}}
	path := filepath.Join(workspace, ".openkitty", "storage", "deck_pipe_contract_artifact.pptx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("validated-pptx"), 0o644); err != nil {
		t.Fatal(err)
	}

	artifacts := extractPipelineArtifacts(exec)
	if len(artifacts) != 1 {
		t.Fatalf("contract artifact was not surfaced: %#v", artifacts)
	}
	artifact := artifacts[0]
	if artifact.Path != ".openkitty/storage/deck_pipe_contract_artifact.pptx" ||
		artifact.Size != int64(len("validated-pptx")) || !artifact.Validated ||
		!strings.Contains(artifact.DownloadURL, "/api/v1/agent/files?path=") {
		t.Fatalf("unexpected contract artifact: %#v", artifact)
	}
	result := pipelineResultWithArtifacts(exec, "交付完成。")
	if !strings.Contains(result, "[deck_pipe_contract_artifact.pptx]") {
		t.Fatalf("contract artifact did not become a download link: %s", result)
	}
}
