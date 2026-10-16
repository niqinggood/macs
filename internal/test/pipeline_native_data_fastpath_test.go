package app

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeDataAnalysisFastPathCompletesWithoutModel(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil || exec.Command(python, "-c", "import pandas, matplotlib").Run() != nil {
		t.Skip("python data runtime unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, ".openkitty", "storage", "input.csv")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	csv := "week,channel,visits,add_to_cart,orders,revenue,refund_amount,ad_spend\nW1,a,100,20,10,1000,20,200\nW2,a,100,200,10,1000,20,200\nW1,b,80,30,15,1800,40,300\nW2,b,90,35,16,1900,3000,300\n"
	if err := os.WriteFile(source, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{config: &Config{Workspace: root}}
	executor := &PipelineExecutor{gateway: gateway, fileLogger: log.New(io.Discard, "", 0)}
	execution := &PipelineExecution{
		ID:           "native-fast-test",
		PipelineName: dataAnalysisLocalFastPipelineName,
		Status:       "running",
		CreatedAt:    time.Now(),
		Input:        "读取 " + source + " 输出 .openkitty/storage/report.md 和 .openkitty/storage/chart.png",
		Steps:        []StepResult{{Name: "分析并交付", Status: "pending"}},
	}
	pipeline := dataAnalystLocalFastPipeline()
	executor.runNativeDataAnalysisFastPath(context.Background(), execution, pipeline, execution.Input)
	if execution.Status != "completed" || execution.Steps[0].Status != "completed" {
		t.Fatalf("native fast path failed: status=%s step=%#v", execution.Status, execution.Steps[0])
	}
	if len(execution.Steps[0].ToolCalls) != 1 || execution.Steps[0].ToolCalls[0].ToolName != "data_science" {
		t.Fatalf("unexpected tool audit: %#v", execution.Steps[0].ToolCalls)
	}
}
