package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mac/internal/agentos"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func openAgentOSFollowupAppDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+agentos.NewLeaseToken()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&GatewayTaskRecord{},
		&ChatSession{},
		&ChatMessage{},
		&SkillRunRecord{},
		&agentos.TaskFollowupState{},
		&agentos.TaskFollowupRecord{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func newAgentOSFollowupTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	gateway := &GatewayService{
		db:     openAgentOSFollowupAppDB(t),
		config: &Config{},
		tasks:  make(map[string]*GatewayTask),
		logger: log.New(io.Discard, "", 0),
	}
	gateway.initSSE()
	return gateway
}

func seedAgentOSFollowupTask(
	t *testing.T,
	gateway *GatewayService,
	taskID, status, leaseToken string,
) *GatewayTask {
	t.Helper()
	now := time.Now().Round(0)
	sessionID := "session-" + taskID
	if err := gateway.db.Create(&ChatSession{
		ID: sessionID, Name: sessionID, AgentName: "Assistant",
		TenantID: "tenant-a", SubjectID: "alice", UserId: "alice",
		MessageCount: 1, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	request := &MessageRequest{
		TaskID: taskID, TaskType: "chat", SessionId: sessionID,
		TenantID: "tenant-a", SubjectID: "alice", RoleID: "Assistant",
		Message: "initial request", AgentName: "Assistant", Channel: "api",
		UserId: "alice", CreatedAt: now,
	}
	task := &GatewayTask{
		ID: taskID, Request: request, TenantID: "tenant-a", SubjectID: "alice",
		TenantScoped: true, RoleID: "Assistant", CallerID: "crm",
		Status: status, CurrentPhase: status, CreatedAt: now,
		QueueName: agentOSQueueName, QueueOwner: "worker-a", LeaseToken: leaseToken,
		ResultCh: make(chan interface{}, 1), ProgressCh: make(chan map[string]interface{}, 1),
		InterruptCh: make(chan string, 1), FollowupCh: make(chan *agentos.TaskFollowupRecord, 1),
	}
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.Create(&ChatMessage{
		SessionID: sessionID, TenantID: "tenant-a", SubjectID: "alice",
		Role: "user", Content: request.Message, Status: status, TaskID: taskID,
		Channel: "api", UserId: "alice", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := agentos.EnsureTaskFollowupState(
		gateway.db, taskID, !isTerminalGatewayTaskStatus(status), now,
	); err != nil {
		t.Fatal(err)
	}
	return task
}

func performAgentOSFollowupRequest(
	t *testing.T,
	gateway *GatewayService,
	method, taskID, suffix string,
	body interface{},
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, "/api/v1/agent/tasks/"+taskID+"/followups"+suffix, reader)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	gateway.handleAgentOSTaskFollowups(
		response,
		request,
		ExecutionIdentity{ControlPlane: true, TenantID: "tenant-a", ServiceID: "control"},
		taskID,
		func() []string {
			if suffix == "" {
				return nil
			}
			return []string{suffix[1:]}
		}(),
	)
	return response
}

func TestAgentOSTaskFollowupEndpointPersistsAndReplaysIdempotently(t *testing.T) {
	gateway := newAgentOSFollowupTestGateway(t)
	task := seedAgentOSFollowupTask(t, gateway, "agt-followup-http", "pending", "")
	body := map[string]interface{}{"message": "Use sample B instead"}
	first := performAgentOSFollowupRequest(t, gateway, http.MethodPost, task.ID, "", body, "change-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var accepted map[string]interface{}
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	followupID, _ := accepted["followup_id"].(string)
	if followupID == "" || accepted["status"] != agentos.TaskFollowupStatusPending {
		t.Fatalf("accepted response=%v", accepted)
	}

	replay := performAgentOSFollowupRequest(t, gateway, http.MethodPost, task.ID, "", body, "change-1")
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayed map[string]interface{}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed["followup_id"] != followupID || replayed["idempotent_replay"] != true {
		t.Fatalf("replay response=%v", replayed)
	}

	mismatch := performAgentOSFollowupRequest(
		t, gateway, http.MethodPost, task.ID, "",
		map[string]interface{}{"message": "Use sample C instead"}, "change-1",
	)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
	var state agentos.TaskFollowupState
	if err := gateway.db.Where("task_id = ?", task.ID).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.AcceptedCount != 1 || state.AppliedCount != 0 {
		t.Fatalf("state=%#v", state)
	}
	if err := gateway.db.Model(&GatewayTaskRecord{}).
		Where("task_id = ?", task.ID).
		Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	terminalReplay := performAgentOSFollowupRequest(t, gateway, http.MethodPost, task.ID, "", body, "change-1")
	if terminalReplay.Code != http.StatusOK {
		t.Fatalf("terminal replay status=%d body=%s", terminalReplay.Code, terminalReplay.Body.String())
	}

	detail := performAgentOSFollowupRequest(t, gateway, http.MethodGet, task.ID, "/"+followupID, nil, "")
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte("Use sample B instead")) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
}

func TestAgentOSTaskFollowupEndpointRejectsTerminalAndCollaborationTasks(t *testing.T) {
	gateway := newAgentOSFollowupTestGateway(t)
	terminal := seedAgentOSFollowupTask(t, gateway, "agt-followup-terminal", "completed", "")
	response := performAgentOSFollowupRequest(
		t, gateway, http.MethodPost, terminal.ID, "",
		map[string]interface{}{"message": "too late"}, "",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("terminal status=%d body=%s", response.Code, response.Body.String())
	}

	collaboration := seedAgentOSFollowupTask(t, gateway, "agt-followup-collab", "pending", "")
	collaboration.Request.TaskType = "collaboration"
	collaboration.Request.Collaboration = &CollaborationConfig{}
	if err := SaveGatewayTaskToDB(gateway.db, collaboration); err != nil {
		t.Fatal(err)
	}
	response = performAgentOSFollowupRequest(
		t, gateway, http.MethodPost, collaboration.ID, "",
		map[string]interface{}{"message": "unsupported"}, "",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("collaboration status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFinishGatewayTaskRequeuesWhenFollowupIsPending(t *testing.T) {
	gateway := newAgentOSFollowupTestGateway(t)
	task := seedAgentOSFollowupTask(t, gateway, "agt-followup-finish", "running", "lease-a")
	gateway.tasks[task.ID] = task
	now := time.Now().Round(0)
	record := agentos.TaskFollowupRecord{
		FollowupID: agentos.NewTaskFollowupID(now), TaskID: task.ID,
		TenantID: task.TenantID, SubjectID: task.SubjectID, RoleID: task.RoleID,
		CallerID: "crm", Message: "change the answer", Status: agentos.TaskFollowupStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gateway.db.Transaction(func(tx *gorm.DB) error {
		if err := agentos.ReserveTaskFollowupSlot(tx, task.ID, now); err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, applied := gateway.finishGatewayTask(task, "completed", "stale answer", nil)
	if applied || snapshot == nil || snapshot.Status != "pending" || snapshot.CurrentPhase != "followup_queued" {
		t.Fatalf("finish applied=%v snapshot=%#v", applied, snapshot)
	}
	var durable GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&durable).Error; err != nil {
		t.Fatal(err)
	}
	if durable.Status != "pending" || durable.LeaseToken != "" || durable.CompletedAt != nil {
		t.Fatalf("durable task=%#v", durable)
	}
	var assistantMessages int64
	if err := gateway.db.Model(&ChatMessage{}).
		Where("task_id = ? AND role = ?", task.ID, "assistant").
		Count(&assistantMessages).Error; err != nil {
		t.Fatal(err)
	}
	if assistantMessages != 0 {
		t.Fatalf("assistant messages=%d, want 0", assistantMessages)
	}
}

func TestAppliedFollowupAndCompletionAreDurable(t *testing.T) {
	gateway := newAgentOSFollowupTestGateway(t)
	task := seedAgentOSFollowupTask(t, gateway, "agt-followup-applied", "running", "lease-a")
	gateway.tasks[task.ID] = task
	now := time.Now().Round(0)
	record := agentos.TaskFollowupRecord{
		FollowupID: agentos.NewTaskFollowupID(now), TaskID: task.ID,
		TenantID: task.TenantID, SubjectID: task.SubjectID, RoleID: task.RoleID,
		CallerID: "crm", Message: "include the audit id", Status: agentos.TaskFollowupStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gateway.db.Transaction(func(tx *gorm.DB) error {
		if err := agentos.ReserveTaskFollowupSlot(tx, task.ID, now); err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := agentos.ClaimNextTaskFollowup(
		context.Background(), gateway.db, task.ID, task.LeaseToken, time.Now(),
	)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := gateway.applyAgentOSTaskFollowup(context.Background(), task, claimed); err != nil {
		t.Fatal(err)
	}
	terminal, applied := gateway.finishGatewayTask(task, "completed", "final answer", nil)
	if !applied || terminal == nil || terminal.Status != "completed" {
		t.Fatalf("finish applied=%v terminal=%#v", applied, terminal)
	}
	var state agentos.TaskFollowupState
	if err := gateway.db.Where("task_id = ?", task.ID).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.Open || state.AcceptedCount != 1 || state.AppliedCount != 1 {
		t.Fatalf("state=%#v", state)
	}
	var messages []ChatMessage
	if err := gateway.db.Where("task_id = ?", task.ID).Order("created_at ASC, id ASC").Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[1].Content != record.Message || messages[2].Content != "final answer" {
		t.Fatalf("messages=%#v", messages)
	}
}
