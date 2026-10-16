package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mac/internal/browserruntime"
)

func TestArtifactCenterBuildsItemsFromPipelineExecutions(t *testing.T) {
	workspace := t.TempDir()
	reportPath := filepath.Join(workspace, ".openkitty", "storage", "report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("# report"), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	gw := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID:                "pipe_artifact_1",
		PipelineName:      "wide-research",
		Status:            "completed",
		CreatedAt:         time.Now(),
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{
				Name:   "collect",
				Status: "completed",
				ToolCalls: []ToolCallRecord{{
					ToolName: "web_search",
					Result:   "1. Source\nURL: https://www.sec.gov/report",
				}},
			},
			{
				Name:   "report",
				Status: "completed",
				Output: "Saved to .openkitty/storage/report.md\nEvidence: https://www.sec.gov/report verified",
			},
		},
	}
	gw.pipelineExec.executions[exec.ID] = exec

	items, summary := gw.buildArtifactCenter(20)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want 1", items)
	}
	item := items[0]
	if item.Path != ".openkitty/storage/report.md" {
		t.Fatalf("path = %q", item.Path)
	}
	if item.Type != "report" {
		t.Fatalf("type = %q, want report", item.Type)
	}
	if item.ExecID != exec.ID || item.PipelineName != "wide-research" {
		t.Fatalf("execution metadata = %#v", item)
	}
	if item.Size == 0 {
		t.Fatalf("size should be populated: %#v", item)
	}
	if item.EvidenceLevel == "" || item.SourceCount == 0 {
		t.Fatalf("evidence metadata missing: %#v", item)
	}
	if summary.TotalArtifacts != 1 || summary.ByType["report"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.EstimatedCredits == 0 {
		t.Fatalf("summary credits should be populated: %#v", summary)
	}
}

func TestArtifactCenterIncludesBrowserTaskArtifacts(t *testing.T) {
	old := globalBrowserTaskManager
	manager := browserruntime.NewBrowserTaskManager()
	globalBrowserTaskManager = manager
	t.Cleanup(func() { globalBrowserTaskManager = old })

	workspace := t.TempDir()
	shotPath := filepath.Join(workspace, ".openkitty", "storage", "browser_tasks", "task-1", "screenshot.png")
	replayPath := filepath.Join(workspace, ".openkitty", "storage", "browser_tasks", "task-1", "replay.js")
	if err := os.MkdirAll(filepath.Dir(shotPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(shotPath, []byte("png"), 0644); err != nil {
		t.Fatalf("write screenshot: %v", err)
	}
	if err := os.WriteFile(replayPath, []byte("console.log('replay')"), 0644); err != nil {
		t.Fatalf("write replay: %v", err)
	}

	task := manager.Start("inspect site", "https://example.com")
	manager.Update(task.ID, func(r *browserruntime.BrowserTaskRecord) {
		r.Status = browserruntime.BrowserTaskCompleted
		r.ScreenshotPath = ".openkitty/storage/browser_tasks/task-1/screenshot.png"
		r.ScreenshotURL = "/api/files/download?path=%2F.openkitty%2Fstorage%2Fbrowser_tasks%2Ftask-1%2Fscreenshot.png"
		r.ReplayPath = ".openkitty/storage/browser_tasks/task-1/replay.js"
		r.ReplayURL = "/api/files/download?path=%2F.openkitty%2Fstorage%2Fbrowser_tasks%2Ftask-1%2Freplay.js"
	})

	gw := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}
	items, summary := gw.buildArtifactCenter(20)
	if len(items) != 2 {
		t.Fatalf("items = %#v, want 2", items)
	}
	if summary.TotalArtifacts != 2 || summary.ByType["image"] != 1 || summary.ByType["web"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	for _, item := range items {
		if item.TaskKind != "browser" || item.BrowserTaskID != task.ID {
			t.Fatalf("browser metadata missing: %#v", item)
		}
		if item.Size == 0 {
			t.Fatalf("size missing: %#v", item)
		}
	}
}

func TestArtifactCenterIncludesDesktopTaskArtifacts(t *testing.T) {
	old := globalDesktopTaskManager
	manager := NewDesktopTaskManager()
	globalDesktopTaskManager = manager
	t.Cleanup(func() { globalDesktopTaskManager = old })

	workspace := t.TempDir()
	screenPath := filepath.Join(workspace, ".openkitty", "storage", "desktop_tasks", "task-1", "screen.png")
	if err := os.MkdirAll(filepath.Dir(screenPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(screenPath, []byte("png"), 0644); err != nil {
		t.Fatalf("write screen: %v", err)
	}

	task := manager.Start("inspect desktop", "inspect", workspace, workspace)
	manager.Update(task.ID, func(r *DesktopTaskRecord) {
		r.Status = DesktopTaskCompleted
		r.ScreenshotPath = ".openkitty/storage/desktop_tasks/task-1/screen.png"
		r.ScreenshotURL = "/api/files/download?path=%2F.openkitty%2Fstorage%2Fdesktop_tasks%2Ftask-1%2Fscreen.png"
	})

	gw := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}
	items, summary := gw.buildArtifactCenter(20)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want 1", items)
	}
	item := items[0]
	if item.TaskKind != "desktop" || item.DesktopTaskID != task.ID {
		t.Fatalf("desktop metadata missing: %#v", item)
	}
	if item.Type != "image" || summary.ByType["image"] != 1 {
		t.Fatalf("type/summary = %#v %#v", item, summary)
	}
}

func TestHandleArtifactsReturnsJSON(t *testing.T) {
	gw := &GatewayService{logger: log.New(io.Discard, "", 0)}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	gw.pipelineExec.executions["pipe_empty"] = &PipelineExecution{
		ID:           "pipe_empty",
		PipelineName: "demo",
		Status:       "completed",
		CreatedAt:    time.Now(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts?limit=5", nil)
	rr := httptest.NewRecorder()
	gw.handleArtifacts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["artifacts"].([]interface{}); !ok {
		t.Fatalf("missing artifacts array: %#v", body)
	}
	if _, ok := body["summary"].(map[string]interface{}); !ok {
		t.Fatalf("missing summary: %#v", body)
	}
}

func TestBuildDeliverySummaryIncludesTrustSignals(t *testing.T) {
	workspace := t.TempDir()
	reportPath := filepath.Join(workspace, ".openkitty", "storage", "delivery.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("# delivery"), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	gw := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID:                "delivery_exec_1",
		PipelineName:      "wide-research",
		Status:            "completed",
		CreatedAt:         time.Now(),
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{
				Name:       "research",
				Status:     "completed",
				Output:     "Evidence: https://www.sec.gov/report verified",
				DurationMs: 100,
				ToolCalls: []ToolCallRecord{{
					ToolName: "web_search",
					Result:   "URL: https://www.sec.gov/report",
				}},
			},
			{
				Name:       "deliver",
				Status:     "completed",
				Output:     "Final report saved to .openkitty/storage/delivery.md",
				DurationMs: 200,
			},
		},
	}

	summary := gw.buildDeliverySummary(exec)
	if summary == nil {
		t.Fatal("summary is nil")
	}
	if summary.ExecID != exec.ID || summary.ProgressPercent != 100 {
		t.Fatalf("unexpected summary identity/progress: %#v", summary)
	}
	if summary.ArtifactCount != 1 || summary.Artifacts[0].Name != "delivery.md" {
		t.Fatalf("artifacts = %#v", summary.Artifacts)
	}
	if summary.EstimatedCredits == 0 || len(summary.CreditBreakdown) == 0 {
		t.Fatalf("credits missing: %#v", summary)
	}
	if summary.EvidenceLevel == "" || summary.SourceCount == 0 {
		t.Fatalf("evidence missing: %#v", summary)
	}
	if !strings.Contains(summary.Markdown, "Delivery summary") || !strings.Contains(summary.Markdown, "delivery.md") {
		t.Fatalf("markdown missing expected content:\n%s", summary.Markdown)
	}
}

func TestHandleDeliverySummaryReturnsMarkdown(t *testing.T) {
	gw := &GatewayService{logger: log.New(io.Discard, "", 0)}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	gw.pipelineExec.executions["delivery_exec_api"] = &PipelineExecution{
		ID:           "delivery_exec_api",
		PipelineName: "demo",
		Status:       "failed",
		CreatedAt:    time.Now(),
		Steps: []StepResult{{
			Name:   "run",
			Status: "failed",
			Error:  "tool failed",
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/delivery/summary?exec_id=delivery_exec_api", nil)
	rr := httptest.NewRecorder()
	gw.handleDeliverySummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Delivery DeliverySummary `json:"delivery"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Delivery.ExecID != "delivery_exec_api" {
		t.Fatalf("delivery = %#v", body.Delivery)
	}
	if body.Delivery.RiskLevel != "high" {
		t.Fatalf("risk = %q", body.Delivery.RiskLevel)
	}
	if !strings.Contains(body.Delivery.Markdown, "tool failed") {
		t.Fatalf("markdown missing failure: %s", body.Delivery.Markdown)
	}
}

func TestHandleDeliverySummaryReturnsBrowserTask(t *testing.T) {
	old := globalBrowserTaskManager
	manager := browserruntime.NewBrowserTaskManager()
	globalBrowserTaskManager = manager
	t.Cleanup(func() { globalBrowserTaskManager = old })

	task := manager.Start("inspect checkout", "https://example.com/checkout")
	manager.AddStep(task.ID, "navigate", "completed", "Opened tab tab-1", 10, nil)
	manager.Update(task.ID, func(r *browserruntime.BrowserTaskRecord) {
		r.Status = browserruntime.BrowserTaskCompleted
		r.TargetID = "tab-1"
		r.ExtractExcerpt = "[0]<button> \"Pay\""
	})

	gw := &GatewayService{logger: log.New(io.Discard, "", 0)}
	req := httptest.NewRequest(http.MethodGet, "/api/delivery/summary?id="+task.ID, nil)
	rr := httptest.NewRecorder()
	gw.handleDeliverySummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Delivery DeliverySummary `json:"delivery"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Delivery.ExecID != task.ID || body.Delivery.PipelineName != "browser-task" {
		t.Fatalf("delivery = %#v", body.Delivery)
	}
	if body.Delivery.RiskLevel != "low" || !strings.Contains(body.Delivery.Markdown, "Browser task") {
		t.Fatalf("unexpected browser delivery: %#v markdown=%s", body.Delivery, body.Delivery.Markdown)
	}
}

func TestHandleDeliverySummaryReturnsDesktopTask(t *testing.T) {
	old := globalDesktopTaskManager
	manager := NewDesktopTaskManager()
	globalDesktopTaskManager = manager
	t.Cleanup(func() { globalDesktopTaskManager = old })

	task := manager.Start("inspect local file", "inspect", ".", ".")
	manager.AddStep(task.ID, "inspect", "completed", "Target: .", 10, nil)
	manager.Update(task.ID, func(r *DesktopTaskRecord) {
		r.Status = DesktopTaskCompleted
	})

	gw := &GatewayService{logger: log.New(io.Discard, "", 0)}
	req := httptest.NewRequest(http.MethodGet, "/api/delivery/summary?id="+task.ID, nil)
	rr := httptest.NewRecorder()
	gw.handleDeliverySummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Delivery DeliverySummary `json:"delivery"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Delivery.ExecID != task.ID || body.Delivery.PipelineName != "desktop-task" {
		t.Fatalf("delivery = %#v", body.Delivery)
	}
	if !strings.Contains(body.Delivery.Markdown, "Desktop task") {
		t.Fatalf("markdown missing desktop summary: %s", body.Delivery.Markdown)
	}
}
