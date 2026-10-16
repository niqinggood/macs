package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutputDisclaimsArtifactDelivery(t *testing.T) {
	for _, text := range []string{
		"本轮因预算耗尽未实际落盘 .md 与 .png",
		"## 真实产物路径（计划落盘位置）",
		"The report was not generated.",
	} {
		if !outputDisclaimsArtifactDelivery(text) {
			t.Fatalf("expected delivery disclaimer for %q", text)
		}
	}
	if outputDisclaimsArtifactDelivery("报告已生成并保存到 .openkitty/storage/report.md") {
		t.Fatal("successful delivery was treated as a disclaimer")
	}
}

func TestOutputClaimsArtifactDeliveryRequiresCompletedLanguage(t *testing.T) {
	for _, text := range []string{
		"报告已生成并保存到 .openkitty/storage/report.md",
		"成功写入 E:\\work\\report.md",
		"Report has been saved to .openkitty/storage/report.md",
	} {
		if !outputClaimsArtifactDelivery(text) {
			t.Fatalf("expected completed delivery claim for %q", text)
		}
	}
	for _, text := range []string{
		"后续输出报告至 E:\\work\\report.md",
		"最终文件路径：E:\\work\\report.md",
		"计划保存到 .openkitty/storage/report.md",
	} {
		if outputClaimsArtifactDelivery(text) {
			t.Fatalf("future/path-only mention treated as completed delivery: %q", text)
		}
	}
}

func TestValidateFastPipelineArtifactsRequiresFreshRequestedFiles(t *testing.T) {
	root := t.TempDir()
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: root}}}
	exec := &PipelineExecution{
		PipelineName: dataAnalysisLocalFastPipelineName,
		Input:        "输出 .openkitty/storage/report.md 和 .openkitty/storage/chart.png",
		CreatedAt:    time.Now(),
	}
	if err := executor.validateFastPipelineArtifacts(exec); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing artifacts should fail, got %v", err)
	}
	dir := filepath.Join(root, ".openkitty", "storage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(strings.Repeat("report ", 60)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), []byte(strings.Repeat("png", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := executor.validateFastPipelineArtifacts(exec); err != nil {
		t.Fatalf("fresh artifacts should pass: %v", err)
	}
}
