package app

import (
	"net/http"
	"testing"
	"time"
)

func seedAgentOSCancellationState(
	t *testing.T,
	g *GatewayService,
	taskID string,
	taskStatus string,
	messageStatus string,
	approvalStatus string,
) {
	t.Helper()
	now := time.Now().Round(0)
	record := GatewayTaskRecord{
		TaskID: taskID, TenantID: defaultExecutionTenantID, RoleID: "Researcher",
		SessionID: "session-" + taskID, UserID: "local-user",
		TaskType: "chat", Status: taskStatus, Message: "cancel atomically",
		AgentName: "Researcher", Channel: "api", CallerID: "local-client",
		QueueName: agentOSQueueName, CurrentPhase: taskStatus,
		LeaseToken:  "lease-" + taskID,
		AvailableAt: &now, HeartbeatAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := g.db.Create(&record).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if messageStatus != "" {
		message := ChatMessage{
			SessionID: record.SessionID, Role: "user", AgentName: record.AgentName,
			Content: "cancel atomically", Status: messageStatus, TaskID: taskID,
			Channel: "api", UserId: record.UserID, CreatedAt: now,
		}
		if err := g.db.Create(&message).Error; err != nil {
			t.Fatalf("seed chat message: %v", err)
		}
	}
	if approvalStatus != "" {
		approval := ApprovalRequestRecord{
			ApprovalID: "approval-" + taskID, ApprovalKey: "key-" + taskID,
			Generation: 1, TaskID: taskID, TenantID: record.TenantID,
			RoleID: record.RoleID, ToolCallID: "call-" + taskID,
			ToolName: "production_write", ArgumentsHash: "arguments-" + taskID,
			Reason: "operator approval required", Status: approvalStatus,
			RequestedAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := g.db.Create(&approval).Error; err != nil {
			t.Fatalf("seed approval: %v", err)
		}
	}
}

func assertAgentOSCancellationState(
	t *testing.T,
	g *GatewayService,
	taskID string,
	wantTask string,
	wantMessage string,
	wantApproval string,
) {
	t.Helper()
	var task GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Status != wantTask {
		t.Fatalf("task status=%q, want %q", task.Status, wantTask)
	}
	if wantMessage != "" {
		var message ChatMessage
		if err := g.db.Where("task_id = ?", taskID).First(&message).Error; err != nil {
			t.Fatalf("load chat message: %v", err)
		}
		if message.Status != wantMessage {
			t.Fatalf("message status=%q, want %q", message.Status, wantMessage)
		}
	}
	if wantApproval != "" {
		var approval ApprovalRequestRecord
		if err := g.db.Where("task_id = ?", taskID).First(&approval).Error; err != nil {
			t.Fatalf("load approval: %v", err)
		}
		if approval.Status != wantApproval {
			t.Fatalf("approval status=%q, want %q", approval.Status, wantApproval)
		}
	}
}

func TestAgentOSCancellationCommitsTaskApprovalAndMessageAtomically(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	const taskID = "agentos-cancel-atomic-success"
	seedAgentOSCancellationState(
		t,
		g,
		taskID,
		"waiting_approval",
		"waiting_approval",
		approvalStatusPending,
	)

	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/cancel",
		nil,
		nil,
	)
	if response.code != http.StatusOK || response.body["status"] != "stopped" {
		t.Fatalf("cancel response=%d %s", response.code, response.raw)
	}
	assertAgentOSCancellationState(
		t,
		g,
		taskID,
		"stopped",
		"stopped",
		approvalStatusCancelled,
	)
}

func TestAgentOSCancellationRollsBackAndReportsDatabaseFailure(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	const taskID = "agentos-cancel-atomic-rollback"
	seedAgentOSCancellationState(
		t,
		g,
		taskID,
		"waiting_approval",
		"waiting_approval",
		approvalStatusPending,
	)
	if err := g.db.Exec(`
		CREATE TRIGGER reject_agentos_message_cancel
		BEFORE UPDATE OF status ON chat_messages
		WHEN OLD.task_id = 'agentos-cancel-atomic-rollback'
			AND NEW.status = 'stopped'
		BEGIN
			SELECT RAISE(ABORT, 'forced cancellation persistence failure');
		END
	`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/cancel",
		nil,
		nil,
	)
	if response.code != http.StatusServiceUnavailable {
		t.Fatalf("cancel failure code=%d, want 503: %s", response.code, response.raw)
	}
	if response.body["code"] != "service_unavailable" {
		t.Fatalf("cancel failure contract=%#v", response.body)
	}
	assertAgentOSCancellationState(
		t,
		g,
		taskID,
		"waiting_approval",
		"waiting_approval",
		approvalStatusPending,
	)
}

func TestAgentOSCancellationDistinguishesMissingAndTerminalTasks(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	missing := performAgentOSRequest(
		g,
		http.MethodDelete,
		"/api/v1/agent/tasks/missing-agentos-task",
		nil,
		nil,
	)
	if missing.code != http.StatusNotFound {
		t.Fatalf("missing cancel code=%d, want 404: %s", missing.code, missing.raw)
	}

	const terminalTaskID = "agentos-cancel-terminal"
	seedAgentOSCancellationState(
		t,
		g,
		terminalTaskID,
		"completed",
		"completed",
		approvalStatusConsumed,
	)
	terminal := performAgentOSRequest(
		g,
		http.MethodDelete,
		"/api/v1/agent/tasks/"+terminalTaskID,
		nil,
		nil,
	)
	if terminal.code != http.StatusConflict {
		t.Fatalf("terminal cancel code=%d, want 409: %s", terminal.code, terminal.raw)
	}
	assertAgentOSCancellationState(
		t,
		g,
		terminalTaskID,
		"completed",
		"completed",
		approvalStatusConsumed,
	)
}
