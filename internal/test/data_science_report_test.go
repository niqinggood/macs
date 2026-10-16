package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	developmenttools "mac/internal/tools/development"
)

func TestDataScienceReportGeneratesVerifiedArtifacts(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python unavailable")
	}
	if err := exec.Command(python, "-c", "import pandas, matplotlib").Run(); err != nil {
		t.Skip("pandas/matplotlib unavailable")
	}
	root := t.TempDir()
	csvPath := filepath.Join(root, "funnel.csv")
	csv := "week,channel,visits,add_to_cart,orders,revenue,refund_amount,ad_spend\n" +
		"W1,search,100,20,10,1000,20,200\n" +
		"W2,search,100,200,11,1100,20,200\n" +
		"W1,live,80,30,15,1800,40,300\n" +
		"W2,live,90,35,16,1900,3000,300\n"
	if err := os.WriteFile(csvPath, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := developmenttools.NewDataScienceTool(root)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"task":        "report",
		"data_file":   csvPath,
		"report_path": ".openkitty/storage/report.md",
		"chart_path":  ".openkitty/storage/chart.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "status: success") || !strings.Contains(result, `"anomalies": 2`) {
		t.Fatalf("unexpected result: %s", result)
	}
	if strings.Contains(result, "Glyph ") && strings.Contains(result, "missing from font") {
		t.Fatalf("generated chart has missing glyph warnings: %s", result)
	}
	for _, item := range []struct {
		path string
		min  int64
	}{{filepath.Join(root, ".openkitty", "storage", "report.md"), 300}, {filepath.Join(root, ".openkitty", "storage", "chart.png"), 1024}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Size() < item.min {
			t.Fatalf("artifact invalid %s: info=%v err=%v", item.path, info, err)
		}
	}
}
