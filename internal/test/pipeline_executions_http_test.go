package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandlePipelineExecutionsReturnsArtifactViews(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, ".openkitty", "storage", "report.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("validated report"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{
		ID:                "exec_artifact_list",
		PipelineName:      "demo",
		Status:            "completed",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{{
			Name:   "deliver",
			Status: "completed",
			Output: "Saved report: .openkitty/storage/report.md",
		}},
	}
	g := &GatewayService{
		logger: log.New(io.Discard, "", 0),
		pipelineExec: &PipelineExecutor{
			executions: map[string]*PipelineExecution{exec.ID: exec},
		},
	}

	rec := httptest.NewRecorder()
	g.handlePipelineExecutions(rec, httptest.NewRequest(http.MethodGet, "/api/pipeline/executions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Executions []struct {
			ID        string             `json:"id"`
			Artifacts []PipelineArtifact `json:"artifacts"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Executions) != 1 {
		t.Fatalf("executions = %#v", body.Executions)
	}
	if len(body.Executions[0].Artifacts) != 1 {
		t.Fatalf("artifacts = %#v", body.Executions[0].Artifacts)
	}
	if body.Executions[0].Artifacts[0].Path != ".openkitty/storage/report.md" {
		t.Fatalf("artifact path = %q", body.Executions[0].Artifacts[0].Path)
	}
}
