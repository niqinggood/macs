package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgentOSApprovalDecisionRollsBackWhenTranscriptUpdateFails(t *testing.T) {
	agent := &agentOSApprovalScenarioAgent{call: ToolCall{
		ID: "atomic-approval-call", Name: "publish_customer_export",
		Arguments: map[string]interface{}{"target": "production"},
	}}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	t.Cleanup(func() {
		if g.agentOSQueue == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		g.agentOSQueue.Stop(ctx)
	})

	submitted := submitAgentOSScenario(
		g, "approval-atomicity", "approval-atomicity", nil,
	)
	taskID := agentOSTaskIDFromResponse(t, submitted)
	_ = waitForAgentOSTask(t, g, taskID, "waiting_approval")
	var approval ApprovalRequestRecord
	if err := g.db.Where("task_id = ?", taskID).First(&approval).Error; err != nil {
		t.Fatal(err)
	}

	escapedTaskID := strings.ReplaceAll(taskID, "'", "''")
	trigger := `
CREATE TRIGGER fail_agentos_approval_transcript
BEFORE UPDATE OF status ON chat_messages
WHEN NEW.task_id = '` + escapedTaskID + `'
BEGIN
  SELECT RAISE(FAIL, 'forced transcript failure');
END`
	if err := g.db.Exec(trigger).Error; err != nil {
		t.Fatal(err)
	}

	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+
			approval.ApprovalID+"/approve",
		map[string]interface{}{"reason": "approved but storage fails"},
		nil,
	)
	if response.code != http.StatusServiceUnavailable ||
		response.body["code"] != "service_unavailable" {
		t.Fatalf(
			"approval failure response=%d/%#v body=%s",
			response.code, response.body, response.raw,
		)
	}

	var durableTask GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskID).First(&durableTask).Error; err != nil {
		t.Fatal(err)
	}
	if durableTask.Status != "waiting_approval" {
		t.Fatalf("task escaped rollback with status %q", durableTask.Status)
	}
	if err := g.db.Where("approval_id = ?", approval.ApprovalID).
		First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	if approval.Status != approvalStatusPending || approval.DecidedAt != nil {
		t.Fatalf("approval escaped rollback: %+v", approval)
	}
	var message ChatMessage
	if err := g.db.Where("task_id = ? AND role = ?", taskID, "user").
		First(&message).Error; err != nil {
		t.Fatal(err)
	}
	if message.Status != "waiting_approval" {
		t.Fatalf("transcript escaped rollback with status %q", message.Status)
	}
	local, ok := g.gatewayTaskSnapshot(taskID)
	if !ok || local.Status != "waiting_approval" {
		t.Fatalf("local task changed before transaction committed: %+v", local)
	}
}
