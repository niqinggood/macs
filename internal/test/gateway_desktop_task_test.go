package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopTaskManagerStoresAuditableRecord(t *testing.T) {
	manager := NewDesktopTaskManager()
	rec := manager.Start("inspect workspace", "inspect", ".", ".")
	manager.AddStep(rec.ID, "inspect", "completed", "Target: .", 12, nil)
	manager.Update(rec.ID, func(r *DesktopTaskRecord) {
		r.Status = DesktopTaskCompleted
		r.ScreenshotPath = ".openkitty/storage/desktop_tasks/task/screen.png"
		r.ScreenshotURL = "/api/files/download?path=%2F.openkitty%2Fstorage%2Fdesktop_tasks%2Ftask%2Fscreen.png"
	})

	got, ok := manager.Get(rec.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != DesktopTaskCompleted || got.EstimatedCredit == 0 {
		t.Fatalf("unexpected task: %#v", got)
	}
	if !strings.Contains(got.Markdown, "Desktop task") || !strings.Contains(got.Markdown, "screen.png") {
		t.Fatalf("markdown missing details:\n%s", got.Markdown)
	}
}

func TestInspectDesktopTargetListsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	files, out, err := inspectDesktopTarget(dir, 10)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("files = %#v", files)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("output missing file: %s", out)
	}
}

func TestHandleDesktopTasksAndDetail(t *testing.T) {
	old := globalDesktopTaskManager
	manager := NewDesktopTaskManager()
	globalDesktopTaskManager = manager
	t.Cleanup(func() { globalDesktopTaskManager = old })

	rec := manager.Start("inspect", "inspect", ".", ".")
	manager.Update(rec.ID, func(r *DesktopTaskRecord) {
		r.Status = DesktopTaskCompleted
	})
	gw := &GatewayService{}

	listReq := httptest.NewRequest(http.MethodGet, "/api/desktop/tasks?limit=5", nil)
	listRR := httptest.NewRecorder()
	gw.handleDesktopTasks(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listBody struct {
		Tasks []DesktopTaskRecord `json:"tasks"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Tasks) != 1 || listBody.Tasks[0].ID != rec.ID {
		t.Fatalf("unexpected list body: %#v", listBody)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/desktop/task?id="+rec.ID, nil)
	detailRR := httptest.NewRecorder()
	gw.handleDesktopTaskDetail(detailRR, detailReq)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detailRR.Code, detailRR.Body.String())
	}
	var detailBody struct {
		Task DesktopTaskRecord `json:"task"`
	}
	if err := json.Unmarshal(detailRR.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detailBody.Task.ID != rec.ID {
		t.Fatalf("unexpected detail: %#v", detailBody.Task)
	}
}

func TestDesktopTaskToolDryRunInteraction(t *testing.T) {
	manager := NewDesktopTaskManager()
	tool := NewDesktopTaskTool(t.TempDir(), manager)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":         "click",
		"x":              float64(123),
		"y":              float64(456),
		"dry_run":        true,
		"capture_screen": false,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "dry-run") {
		t.Fatalf("output missing dry-run marker:\n%s", out)
	}
	tasks := manager.List(10)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v", tasks)
	}
	task := tasks[0]
	if task.Status != DesktopTaskCompleted {
		t.Fatalf("status = %s error=%s", task.Status, task.Error)
	}
	if len(task.Interactions) != 1 || task.Interactions[0].Action != "click" || task.Interactions[0].X != 123 || task.Interactions[0].Y != 456 {
		t.Fatalf("unexpected interactions: %#v", task.Interactions)
	}
	if !strings.Contains(task.Markdown, "Interactions") {
		t.Fatalf("markdown missing interactions:\n%s", task.Markdown)
	}
}

func TestWindowsSendKeysToken(t *testing.T) {
	cases := map[string]string{
		"enter":  "{ENTER}",
		"ctrl+s": "^s",
		"alt+f4": "%{F4}",
		"f5":     "{F5}",
	}
	for key, want := range cases {
		got, ok := windowsSendKeysToken(key)
		if !ok || got != want {
			t.Fatalf("windowsSendKeysToken(%q) = %q, %v; want %q, true", key, got, ok, want)
		}
	}
	if _, ok := windowsSendKeysToken("not-a-real-key"); ok {
		t.Fatal("unsupported key accepted")
	}
}
