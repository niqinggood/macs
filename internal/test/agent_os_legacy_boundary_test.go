package app

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mac/internal/eventbus"
	gatewayhttp "mac/internal/gateway/httpx"

	"github.com/gorilla/websocket"
)

func seedLegacyBoundaryTask(
	t *testing.T,
	gateway *GatewayService,
	taskID, sessionID, queueName, message, result string,
) *GatewayTask {
	t.Helper()
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: taskID,
		Request: &MessageRequest{
			TaskID: taskID, TaskType: "chat", SessionId: sessionID,
			Message: message, AgentName: "Researcher", Channel: "api",
		},
		Status: "pending", Result: result, CreatedAt: now,
		HeartbeatAt: &now, CurrentPhase: "queued", QueueName: queueName,
	}
	gateway.tasksMu.Lock()
	gateway.tasks[taskID] = task
	gateway.tasksMu.Unlock()
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatal(err)
	}
	return task
}

func serveLegacyBoundary(
	handler http.HandlerFunc,
	method, target string,
	body interface{},
) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, reader)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func TestLegacyTaskAPIsCannotReadOrMutateAgentOSTasks(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	agentTask := seedLegacyBoundaryTask(
		t, gateway,
		"agt_hidden_legacy_boundary", "agentos-session", agentOSQueueName,
		"tenant secret request", "tenant secret result",
	)
	legacyTask := seedLegacyBoundaryTask(
		t, gateway,
		"legacy-visible-boundary", "legacy-session", "",
		"ordinary web request", "ordinary web result",
	)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"detail": serveLegacyBoundary(
			gateway.handleGetTask,
			http.MethodGet,
			"/api/task/get/"+agentTask.ID,
			nil,
		),
		"status": serveLegacyBoundary(
			gateway.handleTaskStatus,
			http.MethodGet,
			"/api/task/"+agentTask.ID,
			nil,
		),
		"trace": serveLegacyBoundary(
			gateway.handleTaskTrace,
			http.MethodGet,
			"/api/trace/"+agentTask.ID,
			nil,
		),
		"progress_log": serveLegacyBoundary(
			gateway.handleProgressLogDownload,
			http.MethodGet,
			"/api/task/progress-log/"+agentTask.ID,
			nil,
		),
	} {
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s exposed AgentOS task: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}

	visible := serveLegacyBoundary(
		gateway.handleGetTask,
		http.MethodGet,
		"/api/task/get/"+legacyTask.ID,
		nil,
	)
	if visible.Code != http.StatusOK || !strings.Contains(visible.Body.String(), "ordinary web result") {
		t.Fatalf("ordinary Web task compatibility broke: status=%d body=%s", visible.Code, visible.Body.String())
	}

	listed := serveLegacyBoundary(gateway.handleListTasks, http.MethodGet, "/api/tasks", nil)
	if listed.Code != http.StatusOK ||
		strings.Contains(listed.Body.String(), agentTask.ID) ||
		!strings.Contains(listed.Body.String(), legacyTask.ID) {
		t.Fatalf("legacy task list crossed data planes: status=%d body=%s", listed.Code, listed.Body.String())
	}
	running := serveLegacyBoundary(
		gateway.handleListRunningTasks,
		http.MethodGet,
		"/api/tasks/running?include_history=true&include_result=true",
		nil,
	)
	if running.Code != http.StatusOK ||
		strings.Contains(running.Body.String(), agentTask.ID) ||
		!strings.Contains(running.Body.String(), legacyTask.ID) {
		t.Fatalf("legacy running list crossed data planes: status=%d body=%s", running.Code, running.Body.String())
	}

	stopByID := serveLegacyBoundary(
		gateway.handleStopTaskByID,
		http.MethodPost,
		"/api/task/stop/"+agentTask.ID,
		nil,
	)
	if stopByID.Code != http.StatusNotFound {
		t.Fatalf("legacy stop-by-id reached AgentOS task: status=%d body=%s", stopByID.Code, stopByID.Body.String())
	}
	stopBody := serveLegacyBoundary(
		gateway.handleStopTask,
		http.MethodPost,
		"/api/task/stop",
		map[string]interface{}{"task_id": agentTask.ID},
	)
	if stopBody.Code != http.StatusNotFound {
		t.Fatalf("legacy stop reached AgentOS task: status=%d body=%s", stopBody.Code, stopBody.Body.String())
	}
	batch := serveLegacyBoundary(
		gateway.handleBatchStopTasks,
		http.MethodPost,
		"/api/tasks/stop/batch",
		map[string]interface{}{"task_ids": []string{agentTask.ID}},
	)
	if batch.Code != http.StatusOK ||
		!strings.Contains(batch.Body.String(), `"status":"not_found"`) {
		t.Fatalf("legacy batch stop reached AgentOS task: status=%d body=%s", batch.Code, batch.Body.String())
	}
	durable, ok := gateway.gatewayTaskSnapshotWithDB(agentTask.ID)
	if !ok || durable.Status != "pending" {
		t.Fatalf("legacy stop changed AgentOS task: %#v", durable)
	}

	interrupts := make(chan string, 1)
	storeInterruptChannel(agentTask.Request.SessionId, agentTask.ID, interrupts)
	t.Cleanup(func() { deleteInterruptChannel(agentTask.Request.SessionId, agentTask.ID) })
	interrupted := serveLegacyBoundary(
		gateway.handleInterrupt,
		http.MethodPost,
		"/api/interrupt",
		map[string]interface{}{
			"session_id": agentTask.Request.SessionId,
			"task_id":    agentTask.ID,
			"message":    "override the enterprise task",
		},
	)
	if interrupted.Code != http.StatusOK ||
		!strings.Contains(interrupted.Body.String(), "no_active_task") {
		t.Fatalf("legacy interrupt reached AgentOS task: status=%d body=%s", interrupted.Code, interrupted.Body.String())
	}
	select {
	case injected := <-interrupts:
		t.Fatalf("legacy message was injected into AgentOS task: %q", injected)
	default:
	}
}

func TestLegacySessionAPIsExcludePersonalRowsWithoutTaskMarker(t *testing.T) {
	gateway, workspace := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	now := time.Now().Round(0)
	sessions := []ChatSession{
		{
			ID: "personal-without-task-marker", Name: "personal",
			TenantID: defaultExecutionTenantID, SubjectID: "person-a",
			ProjectID: gateway.project.ProjectID, Workspace: workspace,
			MessageCount: 1, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "ordinary-without-task-marker", Name: "ordinary",
			TenantID: defaultExecutionTenantID, SubjectID: "",
			ProjectID: gateway.project.ProjectID, Workspace: workspace,
			MessageCount: 1, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := gateway.db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	messages := []ChatMessage{
		{
			SessionID: sessions[0].ID, TenantID: defaultExecutionTenantID,
			SubjectID: "person-a", Role: "user", Content: "private-personal-secret",
			Status: "completed", CreatedAt: now,
		},
		{
			SessionID: sessions[1].ID, TenantID: defaultExecutionTenantID,
			SubjectID: "", Role: "user", Content: "ordinary-visible-message",
			Status: "completed", CreatedAt: now,
		},
	}
	if err := gateway.db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	listed := serveLegacyBoundary(
		gateway.handleListSessions,
		http.MethodGet,
		"/api/sessions",
		nil,
	)
	if listed.Code != http.StatusOK ||
		strings.Contains(listed.Body.String(), sessions[0].ID) ||
		!strings.Contains(listed.Body.String(), sessions[1].ID) {
		t.Fatalf("legacy session list crossed personal boundary: status=%d body=%s", listed.Code, listed.Body.String())
	}

	personalMessages := serveLegacyBoundary(
		gateway.handleSessionMessages,
		http.MethodGet,
		"/api/session/"+sessions[0].ID,
		nil,
	)
	if personalMessages.Code != http.StatusOK ||
		strings.Contains(personalMessages.Body.String(), "private-personal-secret") ||
		!strings.Contains(personalMessages.Body.String(), `"count":0`) {
		t.Fatalf("legacy message API exposed personal rows: status=%d body=%s", personalMessages.Code, personalMessages.Body.String())
	}

	exported := serveLegacyBoundary(
		gateway.handleSessionExport,
		http.MethodGet,
		"/api/session/export/"+sessions[0].ID,
		nil,
	)
	if exported.Code != http.StatusNotFound ||
		strings.Contains(exported.Body.String(), "private-personal-secret") {
		t.Fatalf("legacy export exposed personal rows: status=%d body=%s", exported.Code, exported.Body.String())
	}
}

func TestAgentOSSSEUsesAnIsolatedSubscriptionNamespace(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	agentTask := seedLegacyBoundaryTask(
		t, gateway,
		"agt_sse_boundary", "agentos-sse-session", agentOSQueueName,
		"secret", "",
	)
	legacyTask := seedLegacyBoundaryTask(
		t, gateway,
		"legacy-sse-boundary", "legacy-sse-session", "",
		"visible", "",
	)

	agentEvents := make(chan Event, 4)
	rawAgentEvents := make(chan Event, 4)
	legacyEvents := make(chan Event, 4)
	globalEvents := make(chan Event, 8)
	gateway.sseMutex.Lock()
	gateway.sseClients[gatewayhttp.AgentOSSSEKey(agentTask.ID)] = map[string]chan Event{"agent": agentEvents}
	gateway.sseClients[agentTask.ID] = map[string]chan Event{"legacy-guesser": rawAgentEvents}
	gateway.sseClients[legacyTask.ID] = map[string]chan Event{"legacy": legacyEvents}
	gateway.sseClients["*"] = map[string]chan Event{"global": globalEvents}
	gateway.sseMutex.Unlock()

	gateway.broadcastTaskUpdate(agentTask.ID, "running", "secret result", nil)
	assertReceivesTaskEvent(t, agentEvents, agentTask.ID)
	assertNoTaskEvent(t, rawAgentEvents, agentTask.ID)
	assertNoTaskEvent(t, globalEvents, agentTask.ID)

	gateway.BroadcastProgress(ProgressEvent{
		Type: "model", TaskID: agentTask.ID, Message: "secret progress", Status: "running",
	})
	assertReceivesTaskEvent(t, agentEvents, agentTask.ID)
	assertNoTaskEvent(t, rawAgentEvents, agentTask.ID)
	assertNoTaskEvent(t, globalEvents, agentTask.ID)

	gateway.BroadcastSSEEvent(eventbus.SSEEvent{
		Type: eventbus.SSETextDelta, TaskID: agentTask.ID, Content: "secret typed event",
	})
	assertReceivesTaskEvent(t, agentEvents, agentTask.ID)
	assertNoTaskEvent(t, rawAgentEvents, agentTask.ID)
	assertNoTaskEvent(t, globalEvents, agentTask.ID)

	gateway.broadcastTaskUpdate(legacyTask.ID, "running", "visible result", nil)
	assertReceivesTaskEvent(t, legacyEvents, legacyTask.ID)
	assertReceivesTaskEvent(t, globalEvents, legacyTask.ID)
}

func TestLegacySSERejectsReservedAgentOSSubscriptionKeys(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/events?task_id=agentos%3Aattacker-selected-task",
		nil,
	).WithContext(ctx)
	recorder := httptest.NewRecorder()
	gateway.handleSSE(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf(
			"legacy SSE accepted reserved AgentOS key: status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	gateway.sseMutex.RLock()
	_, registered := gateway.sseClients["agentos:attacker-selected-task"]
	gateway.sseMutex.RUnlock()
	if registered {
		t.Fatal("legacy SSE registered a client in the internal AgentOS namespace")
	}
}

func TestLegacyExecutionEntrypointsRejectAgentOSSessions(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "ordinary web result"}
	gateway, _ := newAgentOSAPIScenarioGateway(t, agent)
	agentTask := seedLegacyBoundaryTask(
		t, gateway,
		"agt_session_execution_boundary", "agentos_execution_session", agentOSQueueName,
		"tenant secret request", "tenant secret result",
	)

	var messagesBefore int64
	if err := gateway.db.Model(&ChatMessage{}).
		Where("session_id = ?", agentTask.Request.SessionId).
		Count(&messagesBefore).Error; err != nil {
		t.Fatal(err)
	}
	gateway.tasksMu.RLock()
	tasksBefore := len(gateway.tasks)
	gateway.tasksMu.RUnlock()

	requests := []struct {
		name    string
		handler http.HandlerFunc
		body    interface{}
	}{
		{
			name:    "chat",
			handler: gateway.handleChat,
			body: MessageRequest{
				TaskType: "chat", Message: "pollute chat", SessionId: agentTask.Request.SessionId,
				AgentName: "Researcher", Channel: "web",
			},
		},
		{
			name:    "collaboration",
			handler: gateway.handleCollaboration,
			body: map[string]interface{}{
				"task_type": "collaboration", "message": "pollute collaboration",
				"session_id": agentTask.Request.SessionId,
			},
		},
		{
			name:    "task",
			handler: gateway.handleTask,
			body: MessageRequest{
				TaskType: "chat", Message: "pollute task", SessionId: agentTask.Request.SessionId,
				AgentName: "Researcher",
			},
		},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			response := serveLegacyBoundary(
				test.handler,
				http.MethodPost,
				"/api/"+test.name,
				test.body,
			)
			if response.Code != http.StatusNotFound {
				t.Fatalf(
					"entrypoint accepted AgentOS session: status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}

	t.Run("multipart_chat", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("message", "pollute multipart chat"); err != nil {
			t.Fatal(err)
		}
		sessionAlias := strings.ReplaceAll(agentTask.Request.SessionId, "_", "/")
		if err := writer.WriteField("session_id", sessionAlias); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/chat", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		recorder := httptest.NewRecorder()
		gateway.handleChat(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf(
				"multipart chat accepted AgentOS session: status=%d body=%s",
				recorder.Code,
				recorder.Body.String(),
			)
		}
	})

	t.Run("websocket_handshake", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/ws?session_id="+agentTask.Request.SessionId,
			nil,
		)
		recorder := httptest.NewRecorder()
		gateway.handleWebSocket(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf(
				"WebSocket handshake accepted AgentOS session: status=%d body=%s",
				recorder.Code,
				recorder.Body.String(),
			)
		}
	})

	t.Run("websocket_chat_message", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(gateway.handleWebSocket))
		defer server.Close()
		socketURL := "ws" + strings.TrimPrefix(server.URL, "http")
		conn, response, err := websocket.DefaultDialer.Dial(socketURL, nil)
		if err != nil {
			status := 0
			if response != nil {
				status = response.StatusCode
			}
			t.Fatalf("dial WebSocket: %v (status=%d)", err, status)
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var connected map[string]interface{}
		if err := conn.ReadJSON(&connected); err != nil {
			t.Fatalf("read WebSocket greeting: %v", err)
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"type": "chat", "message": "pollute websocket",
			"session_id": agentTask.Request.SessionId, "agent_name": "Researcher",
		}); err != nil {
			t.Fatalf("send WebSocket chat: %v", err)
		}
		var rejected map[string]interface{}
		if err := conn.ReadJSON(&rejected); err != nil {
			t.Fatalf("read WebSocket rejection: %v", err)
		}
		if rejected["type"] != "error" || rejected["code"] != "session_not_found" {
			t.Fatalf("unexpected WebSocket response: %#v", rejected)
		}
	})

	var messagesAfter int64
	if err := gateway.db.Model(&ChatMessage{}).
		Where("session_id = ?", agentTask.Request.SessionId).
		Count(&messagesAfter).Error; err != nil {
		t.Fatal(err)
	}
	if messagesAfter != messagesBefore {
		t.Fatalf(
			"legacy entrypoint polluted AgentOS history: before=%d after=%d",
			messagesBefore,
			messagesAfter,
		)
	}
	gateway.tasksMu.RLock()
	tasksAfter := len(gateway.tasks)
	gateway.tasksMu.RUnlock()
	if tasksAfter != tasksBefore {
		t.Fatalf(
			"legacy entrypoint created tasks for AgentOS session: before=%d after=%d",
			tasksBefore,
			tasksAfter,
		)
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("legacy entrypoint executed AgentOS session %d times", agent.calls.Load())
	}

	ordinary := serveLegacyBoundary(
		gateway.handleTask,
		http.MethodPost,
		"/api/task",
		MessageRequest{
			TaskType: "chat", Message: "ordinary request", SessionId: "ordinary-web-session",
			AgentName: "Researcher",
		},
	)
	if ordinary.Code != http.StatusOK ||
		!strings.Contains(ordinary.Body.String(), "ordinary web result") {
		t.Fatalf(
			"ordinary Web session compatibility broke: status=%d body=%s",
			ordinary.Code,
			ordinary.Body.String(),
		)
	}
}

func assertReceivesTaskEvent(t *testing.T, events <-chan Event, taskID string) {
	t.Helper()
	select {
	case event := <-events:
		if event.TaskID != taskID {
			t.Fatalf("event task=%q, want %q", event.TaskID, taskID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for task event %s", taskID)
	}
}

func assertNoTaskEvent(t *testing.T, events <-chan Event, taskID string) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("task %s leaked through legacy SSE: %#v", taskID, event)
	case <-time.After(20 * time.Millisecond):
	}
}
