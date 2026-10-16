package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mac/internal/browserruntime"
)

func TestHandleBrowserTasksAndDetail(t *testing.T) {
	old := globalBrowserTaskManager
	manager := browserruntime.NewBrowserTaskManager()
	globalBrowserTaskManager = manager
	t.Cleanup(func() { globalBrowserTaskManager = old })

	rec := manager.Start("inspect", "https://example.com")
	manager.Update(rec.ID, func(r *browserruntime.BrowserTaskRecord) {
		r.Status = browserruntime.BrowserTaskCompleted
		r.TargetID = "tab-123"
	})
	gw := &GatewayService{}

	listReq := httptest.NewRequest(http.MethodGet, "/api/browser/tasks?limit=5", nil)
	listRR := httptest.NewRecorder()
	gw.handleBrowserTasks(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listBody struct {
		Tasks []browserruntime.BrowserTaskRecord `json:"tasks"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Tasks) != 1 || listBody.Tasks[0].ID != rec.ID {
		t.Fatalf("unexpected list body: %#v", listBody)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/browser/task?id="+rec.ID, nil)
	detailRR := httptest.NewRecorder()
	gw.handleBrowserTaskDetail(detailRR, detailReq)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detailRR.Code, detailRR.Body.String())
	}
	var detailBody struct {
		Task browserruntime.BrowserTaskRecord `json:"task"`
	}
	if err := json.Unmarshal(detailRR.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detailBody.Task.TargetID != "tab-123" {
		t.Fatalf("unexpected detail: %#v", detailBody.Task)
	}
}
