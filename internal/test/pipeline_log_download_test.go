package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatPipelineExecutionLogIncludesExecutionDetails(t *testing.T) {
	createdAt := time.Date(2026, 7, 1, 15, 30, 0, 0, time.UTC)
	exec := &PipelineExecution{
		ID:           "exec_log_1",
		PipelineName: "research",
		Status:       "completed",
		TaskID:       "task_1",
		SessionID:    "session_1",
		Input:        "analyze Guizhou Gas",
		CreatedAt:    createdAt,
		Steps: []StepResult{
			{
				Name:       "collect",
				Status:     "completed",
				Output:     "collected data",
				DurationMs: 1200,
				ToolCalls: []ToolCallRecord{{
					Timestamp:  createdAt,
					ToolName:   "web_fetch",
					Params:     `{"query":"Guizhou Gas"}`,
					Result:     "2 results",
					DurationMs: 88,
					Cached:     true,
				}},
			},
			{Name: "summary", Status: "completed", Output: "final investment report"},
		},
		AuditLog: []ApprovalRecord{{
			Timestamp: createdAt,
			Role:      "supervisor",
			Action:    "approved",
			Feedback:  "ok",
		}},
	}

	logText := formatPipelineExecutionLog(exec)
	for _, want := range []string{
		"OpenKitty Pipeline Execution Log",
		"Execution ID: exec_log_1",
		"Pipeline: research",
		"Input",
		"analyze Guizhou Gas",
		"[1] collect",
		"Output:\ncollected data",
		"Tool Calls:",
		"web_fetch duration=88ms cached=true",
		`params: {"query":"Guizhou Gas"}`,
		"Final Output",
		"final investment report",
		"Audit",
		"supervisor approved ok",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n%s", want, logText)
		}
	}
}

func TestHandlePipelineLogDownloadServesAttachment(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec/log:unsafe",
		PipelineName: "assistant",
		Status:       "completed",
		Steps:        []StepResult{{Name: "answer", Status: "completed", Output: "done"}},
	}
	g := &GatewayService{
		pipelineExec: &PipelineExecutor{
			executions: map[string]*PipelineExecution{exec.ID: exec},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/log/download?id="+exec.ID, nil)
	rec := httptest.NewRecorder()
	g.handlePipelineLogDownload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `attachment; filename="pipeline_exec_log_unsafe.log"`) {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Execution ID: "+exec.ID) || !strings.Contains(body, "done") {
		t.Fatalf("unexpected body:\n%s", body)
	}
}

func TestHandlePipelineLogDownloadValidatesRequest(t *testing.T) {
	g := &GatewayService{pipelineExec: &PipelineExecutor{
		executions: map[string]*PipelineExecution{},
		mu:         sync.RWMutex{},
	}}

	rec := httptest.NewRecorder()
	g.handlePipelineLogDownload(rec, httptest.NewRequest(http.MethodGet, "/api/pipeline/log/download", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.handlePipelineLogDownload(rec, httptest.NewRequest(http.MethodGet, "/api/pipeline/log/download?id=missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing execution status = %d", rec.Code)
	}
}
