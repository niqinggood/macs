package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTaskListTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&GatewayTaskRecord{}); err != nil {
		t.Fatalf("migrate tasks: %v", err)
	}
	return &GatewayService{
		db:     db,
		logger: log.New(io.Discard, "", 0),
		tasks:  make(map[string]*GatewayTask),
	}
}

func decodeTaskListResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestHandleListTasksDBFallbackHonorsLimitAndStatus(t *testing.T) {
	gw := newTaskListTestGateway(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		status := "completed"
		if i%2 == 0 {
			status = "running"
		}
		if err := gw.db.Create(&GatewayTaskRecord{
			TaskID:    "task-" + string(rune('a'+i)),
			Status:    status,
			TaskType:  "ae",
			Message:   "message",
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatalf("insert task: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks?limit=2&status=running", nil)
	rr := httptest.NewRecorder()
	gw.handleListTasks(rr, req)

	body := decodeTaskListResponse(t, rr)
	tasks := body["tasks"].([]interface{})
	if len(tasks) != 2 {
		t.Fatalf("tasks length = %d, want 2", len(tasks))
	}
	for _, item := range tasks {
		task := item.(map[string]interface{})
		if task["status"] != "running" {
			t.Fatalf("unexpected status in filtered tasks: %#v", task)
		}
	}
}

func TestHandleListTasksMemoryHonorsLimitAndSortsNewestFirst(t *testing.T) {
	gw := newTaskListTestGateway(t)
	now := time.Now()
	gw.tasks["old"] = &GatewayTask{ID: "old", Status: "running", CreatedAt: now.Add(-2 * time.Hour)}
	gw.tasks["new"] = &GatewayTask{ID: "new", Status: "running", CreatedAt: now}
	gw.tasks["mid"] = &GatewayTask{ID: "mid", Status: "running", CreatedAt: now.Add(-1 * time.Hour)}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks?limit=2", nil)
	rr := httptest.NewRecorder()
	gw.handleListTasks(rr, req)

	body := decodeTaskListResponse(t, rr)
	tasks := body["tasks"].([]interface{})
	if len(tasks) != 2 {
		t.Fatalf("tasks length = %d, want 2", len(tasks))
	}
	first := tasks[0].(map[string]interface{})
	second := tasks[1].(map[string]interface{})
	if first["id"] != "new" || second["id"] != "mid" {
		t.Fatalf("tasks not sorted newest first or not limited: %#v", tasks)
	}
}

func TestHandleListRunningTasksDefaultsToLightweightActiveOnly(t *testing.T) {
	gw := newTaskListTestGateway(t)
	now := time.Now()
	gw.tasks["run"] = &GatewayTask{ID: "run", Status: "running", CreatedAt: now, Request: &MessageRequest{SessionId: "s1", Message: "active"}}
	gw.tasks["done"] = &GatewayTask{ID: "done", Status: "completed", CreatedAt: now.Add(-time.Minute), Result: strings.Repeat("large result ", 10000)}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/running", nil)
	rr := httptest.NewRecorder()
	gw.handleListRunningTasks(rr, req)
	body := decodeTaskListResponse(t, rr)
	if got := len(body["running"].([]interface{})); got != 1 {
		t.Fatalf("running length = %d, want 1", got)
	}
	if got := len(body["completed"].([]interface{})); got != 0 {
		t.Fatalf("default running endpoint returned %d historical task(s)", got)
	}
	if rr.Body.Len() > 5000 {
		t.Fatalf("default running response is unexpectedly large: %d bytes", rr.Body.Len())
	}
}

func TestHandleListRunningTasksHistoryAndResultsAreExplicit(t *testing.T) {
	gw := newTaskListTestGateway(t)
	gw.tasks["done"] = &GatewayTask{ID: "done", Status: "completed", CreatedAt: time.Now(), Result: "finished result"}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/running?include_history=1&limit=1", nil)
	rr := httptest.NewRecorder()
	gw.handleListRunningTasks(rr, req)
	body := decodeTaskListResponse(t, rr)
	completed := body["completed"].([]interface{})
	if len(completed) != 1 {
		t.Fatalf("explicit history length = %d, want 1", len(completed))
	}
	if _, exists := completed[0].(map[string]interface{})["result"]; exists {
		t.Fatal("history returned result without include_result=1")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tasks/running?include_history=1&include_result=1&limit=1", nil)
	rr = httptest.NewRecorder()
	gw.handleListRunningTasks(rr, req)
	body = decodeTaskListResponse(t, rr)
	completed = body["completed"].([]interface{})
	if got := completed[0].(map[string]interface{})["result"]; got != "finished result" {
		t.Fatalf("explicit result = %#v, want finished result", got)
	}
}

func TestHandleGetTaskPreservesFailedPartialResult(t *testing.T) {
	gw := newTaskListTestGateway(t)
	gw.tasks["partial"] = &GatewayTask{ID: "partial", Status: "failed", CreatedAt: time.Now(), Result: "usable partial answer", Error: fmt.Errorf("evidence incomplete")}
	req := httptest.NewRequest(http.MethodGet, "/api/task/get/partial", nil)
	rr := httptest.NewRecorder()
	gw.handleGetTask(rr, req)
	body := decodeTaskListResponse(t, rr)
	if body["result"] != "usable partial answer" || body["error"] != "evidence incomplete" {
		t.Fatalf("failed task did not preserve both partial result and error: %#v", body)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/task/partial", nil)
	rr = httptest.NewRecorder()
	gw.handleTaskStatus(rr, req)
	body = decodeTaskListResponse(t, rr)
	if body["result"] != "usable partial answer" || body["error"] != "evidence incomplete" {
		t.Fatalf("polled failed task did not preserve both partial result and error: %#v", body)
	}
}

func TestTaskDetailEndpointsFallBackToRecoveredDatabaseRecord(t *testing.T) {
	gw := newTaskListTestGateway(t)
	createdAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	completedAt := time.Now().Truncate(time.Second)
	if err := gw.db.Create(&GatewayTaskRecord{
		TaskID:      "recovered-task",
		SessionID:   "recovered-session",
		UserID:      "user-1",
		TaskType:    "chat",
		Status:      "failed",
		Message:     "original request",
		AgentName:   "Researcher",
		Channel:     "web",
		Result:      `"usable partial result"`,
		ErrorMsg:    orphanGatewayTaskError,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
		UpdatedAt:   completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{path: "/api/task/recovered-task", handler: gw.handleTaskStatus},
		{path: "/api/task/get/recovered-task", handler: gw.handleGetTask},
	} {
		req := httptest.NewRequest(http.MethodGet, endpoint.path, nil)
		rr := httptest.NewRecorder()
		endpoint.handler(rr, req)
		body := decodeTaskListResponse(t, rr)
		if body["status"] != "failed" || body["result"] != "usable partial result" || body["error"] != orphanGatewayTaskError {
			t.Fatalf("DB fallback response for %s = %#v", endpoint.path, body)
		}
	}
}

func TestHandleListTasksIncludesPipelineProgressForAETask(t *testing.T) {
	gw := newTaskListTestGateway(t)
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID:           "pipe_demo_1",
		PipelineName: "lead-research",
		Status:       "running",
		CurrentStep:  1,
		Steps: []StepResult{
			{Name: "project", Status: "completed"},
			{Name: "search", Status: "running"},
		},
		CreatedAt: time.Now(),
	}
	gw.pipelineExec.executions[exec.ID] = exec
	gw.tasks["pipe_demo_1-de"] = &GatewayTask{
		ID:        "pipe_demo_1-de",
		Status:    "running",
		CreatedAt: time.Now(),
		Request:   &MessageRequest{TaskType: "ae", Message: "@lead demo"},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tasks?limit=1", nil)
	rr := httptest.NewRecorder()
	gw.handleListTasks(rr, req)

	body := decodeTaskListResponse(t, rr)
	tasks := body["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("tasks length = %d, want 1", len(tasks))
	}
	task := tasks[0].(map[string]interface{})
	progress, ok := task["pipeline_progress"].(map[string]interface{})
	if !ok {
		t.Fatalf("task missing pipeline_progress: %#v", task)
	}
	if progress["exec_id"] != "pipe_demo_1" || progress["pipeline_name"] != "lead-research" {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	if int(progress["total_steps"].(float64)) != 2 {
		t.Fatalf("total_steps = %#v, want 2", progress["total_steps"])
	}
}

func TestHandleBatchStopTasksCallsCancel(t *testing.T) {
	gw := newTaskListTestGateway(t)
	cancelled := false
	gw.tasks["task-1"] = &GatewayTask{
		ID:        "task-1",
		Status:    "running",
		CreatedAt: time.Now(),
		Cancel: func() {
			cancelled = true
		},
		ResultCh: make(chan interface{}, 1),
		Request:  &MessageRequest{TaskID: "task-1", TaskType: "ae", Message: "demo"},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/stop/batch", bytes.NewBufferString(`{"task_ids":["task-1"]}`))
	rr := httptest.NewRecorder()
	gw.handleBatchStopTasks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !cancelled {
		t.Fatalf("expected batch stop to call task cancel")
	}
	if gw.tasks["task-1"].Status != "stopped" {
		t.Fatalf("status = %q, want stopped", gw.tasks["task-1"].Status)
	}
	select {
	case got := <-gw.tasks["task-1"].ResultCh:
		t.Fatalf("stop handler must not write the process-owned result channel: %#v", got)
	default:
	}
}
