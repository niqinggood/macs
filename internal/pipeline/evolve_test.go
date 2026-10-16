package pipeline

import (
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineEvolverSavesMetricsInNestedOpenKittyDir(t *testing.T) {
	workspace := t.TempDir()
	metricsPath := filepath.Join(workspace, ".openkitty", "pipeline_metrics.json")
	ev := NewPipelineEvolver(metricsPath, false, log.New(os.Stdout, "", 0))

	ev.Record(ExecutionSnapshot{
		PipelineName: "demo",
		Status:       "completed",
		CreatedAt:    time.Now().Add(-time.Second),
		Steps: []StepSnapshot{
			{Name: "step", Status: "completed"},
		},
	})

	if _, err := os.Stat(metricsPath); err != nil {
		t.Fatalf("expected metrics file at %s: %v", metricsPath, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "pipeline_metrics.json")); !os.IsNotExist(err) {
		t.Fatalf("metrics file should not be saved at workspace root")
	}
}
