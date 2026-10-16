package app

import (
	"testing"
	"time"

	gatewayhttp "mac/internal/gateway/httpx"
)

func TestPersistAgentOSSubmissionRollsBackTranscriptWhenTaskWriteFails(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	gateway := &GatewayService{db: db}
	session := ChatSession{
		ID: "atomic-session", Name: "atomic-session",
		AgentName: "Researcher", MessageCount: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_atomic_agentos_task
		BEFORE INSERT ON gateway_task_records
		WHEN NEW.task_id = 'atomic-task'
		BEGIN
			SELECT RAISE(FAIL, 'forced task persistence failure');
		END
	`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "atomic-task", TenantID: defaultExecutionTenantID,
		RoleID: "Researcher", Status: "pending", CurrentPhase: "queued",
		CallerID: "atomic-service", QueueName: agentOSQueueName,
		CreatedAt: now, AvailableAt: &now, HeartbeatAt: &now,
		Request: &MessageRequest{
			TaskID: "atomic-task", TaskType: "chat",
			SessionId: "atomic-session", TenantID: defaultExecutionTenantID,
			RoleID: "Researcher", AgentName: "Researcher",
			Message: "sensitive enterprise request", Channel: "api",
			InitialMessages: []gatewayhttp.AgentOSInitialMessage{
				{Role: "assistant", Content: "I am ready to help."},
			},
			AssistantIdentity: &gatewayhttp.AgentOSAssistantIdentity{Name: "盈盈", Role: "融资顾问"},
		},
	}
	idempotency := &AgentTaskRequestRecord{
		KeyHash:     "04bc7a8f28b9b8f9d0e265ae9dcaa7ac5327f3c1a530b61b14d91d5e278c1764",
		RequestHash: "be4e37b1f0d8f3fa62be7245612a6b0fa7cb55781327c188f2068e9cc7786e2a",
		TenantID:    defaultExecutionTenantID, RoleID: "Researcher",
		CallerID: "atomic-service", TaskID: task.ID, CreatedAt: now,
	}
	if err := gateway.persistAgentOSSubmission(
		task,
		"sensitive enterprise request",
		nil,
		idempotency,
	); err == nil {
		t.Fatal("forced task persistence failure was ignored")
	}

	var messageCount int64
	if err := db.Model(&ChatMessage{}).
		Where("session_id = ?", session.ID).
		Count(&messageCount).Error; err != nil {
		t.Fatalf("count transcript rows: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("orphan AgentOS transcript rows = %d, want 0", messageCount)
	}
	var taskCount int64
	if err := db.Model(&GatewayTaskRecord{}).
		Where("task_id = ?", task.ID).
		Count(&taskCount).Error; err != nil {
		t.Fatalf("count task rows: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("partial AgentOS task rows = %d, want 0", taskCount)
	}
	var reservationCount int64
	if err := db.Model(&AgentTaskRequestRecord{}).
		Where("task_id = ?", task.ID).
		Count(&reservationCount).Error; err != nil {
		t.Fatalf("count idempotency rows: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("orphan idempotency rows = %d, want 0", reservationCount)
	}
	var persistedSession ChatSession
	if err := db.Where("id = ?", session.ID).First(&persistedSession).Error; err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if persistedSession.MessageCount != 0 {
		t.Fatalf("session message count = %d, want rollback to 0", persistedSession.MessageCount)
	}
}
