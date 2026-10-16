package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	skilldomain "mac/internal/skills"
)

func TestAgentOSSkillRunChangesIdempotencyFingerprint(t *testing.T) {
	base := agentOSTaskSubmitRequest{Message: "build the report"}
	fingerprint := func(request agentOSTaskSubmitRequest) string {
		t.Helper()
		got, err := agentOSRequestFingerprint(request, "Researcher", "revision-1", "alice", "alice")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	plain := fingerprint(base)
	first := base
	first.SkillRun = &SkillRunRequest{
		SkillName: "guided-report", Version: "1.0.0",
		Parameters: map[string]interface{}{"tone": "concise"},
	}
	second := base
	second.SkillRun = &SkillRunRequest{
		SkillName: "guided-report", Version: "1.0.0",
		Parameters: map[string]interface{}{"tone": "detailed"},
	}
	if plain == fingerprint(first) || fingerprint(first) == fingerprint(second) {
		t.Fatal("Skill identity and parameters must participate in the AgentOS idempotency fingerprint")
	}
	if fingerprint(first) != fingerprint(first) {
		t.Fatal("Skill Run idempotency fingerprint is not deterministic")
	}
}

func TestAgentOSAtSkillMentionBecomesDurableSkillRunBeforeFingerprinting(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "unused"}
	gateway, _ := newAgentOSAPIScenarioGateway(t, agent)
	loader := skillMentionTestLoader()
	llmAgent := &LLMAgent{name: "Researcher", skillsLoader: loader}
	gateway.engine.agentFactory.agents["Researcher"] = llmAgent
	// Keep the queue stopped: this assertion is about the accepted durable
	// routing contract, not model execution.
	gateway.ensureAgentOSQueue().mode = "gateway"

	response := submitAgentOSScenario(gateway, "mention-client", "mention-key", map[string]interface{}{
		"message": "@report build the quarterly report",
	})
	if response.code != http.StatusAccepted && response.code != http.StatusOK {
		t.Fatalf("submit @Skill status=%d body=%s", response.code, response.raw)
	}
	taskID := agentOSTaskIDFromResponse(t, response)
	task, ok := gateway.gatewayTaskSnapshotWithDB(taskID)
	if !ok || task.Request == nil || task.Request.SkillRun == nil {
		t.Fatalf("durable @Skill task missing binding: %#v", task)
	}
	if task.Request.TaskType != "skill" || task.Request.SkillRun.SkillName != "guided-report" ||
		task.Request.SkillRun.ClientReference != "at_mention" ||
		!strings.Contains(task.Request.Message, "显式 @Skill 解析后的当前任务：\nbuild the quarterly report") ||
		!strings.Contains(task.Request.Message, "@report build the quarterly report") {
		t.Fatalf("durable @Skill request = %#v", task.Request)
	}
}

func TestAgentOSSkillRunResponseRestoresRequestAndReceipt(t *testing.T) {
	gateway, workspace := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	now := time.Now().Round(0)
	request := &MessageRequest{
		TaskID: "agentos-skill-response", TaskType: "skill", Message: "build it",
		AgentName: "Researcher", SessionId: "skill-session", TenantID: defaultExecutionTenantID,
		ResolvedWorkspace: workspace,
		SkillRun: &SkillRunRequest{
			SkillName: "guided-report", Version: "1.2.3",
			Parameters: map[string]interface{}{"tone": "concise"}, ClientReference: "client-42",
		},
	}
	task := &GatewayTask{
		ID: request.TaskID, Request: request, Status: "running", TenantID: defaultExecutionTenantID,
		RoleID: "Researcher", RoleRevision: "revision-1", QueueName: agentOSQueueName,
		CurrentPhase: "model", ResolvedWorkspace: workspace, CreatedAt: now,
	}
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatalf("persist AgentOS Skill Run task: %v", err)
	}
	snapshot, _ := json.Marshal(SkillRunSnapshot{
		Name: "guided-report", Version: "1.2.3", Type: skilldomain.SkillTypeBuiltin,
		ContentSHA256: strings.Repeat("a", 64),
	})
	parameters, _ := json.Marshal(request.SkillRun.Parameters)
	if err := gateway.db.Create(&SkillRunRecord{
		RunID: skillRunID(task.ID), TaskID: task.ID, TenantID: task.TenantID,
		SkillName: "guided-report", SkillVersion: "1.2.3", ContentSHA256: strings.Repeat("a", 64),
		SkillSnapshot: string(snapshot), ParametersJSON: string(parameters),
		ActivationID: skillRunActivationCallID, ActivationCount: 1, Phase: skillRunPhaseRunning,
		ClientReference: "client-42", StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("persist Skill Run receipt: %v", err)
	}

	restored, ok := gateway.gatewayTaskSnapshotWithDB(task.ID)
	if !ok || restored.Request == nil || restored.Request.SkillRun == nil {
		t.Fatalf("durable request lost Skill Run binding: %#v", restored)
	}
	response := gateway.agentOSTaskResponse(restored, false)
	restoredRequest, ok := response["skill_run_request"].(*SkillRunRequest)
	if !ok || restoredRequest.SkillName != "guided-report" || restoredRequest.ClientReference != "client-42" {
		t.Fatalf("AgentOS Skill Run request response = %#v", response["skill_run_request"])
	}
	receipt, ok := response["skill_run"].(*SkillRunReceipt)
	if !ok || receipt.Phase != skillRunPhaseRunning || receipt.ActivationCount != 1 || receipt.Skill.Version != "1.2.3" {
		t.Fatalf("AgentOS Skill Run receipt response = %#v", response["skill_run"])
	}
}

func TestAgentOSSkillRunCancellationStopsBoundCheckpoint(t *testing.T) {
	gateway, workspace := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	now := time.Now().Round(0)
	request := &MessageRequest{
		TaskID: "agentos-skill-cancel", TaskType: "skill", Message: "build it",
		AgentName: "Researcher", SessionId: "skill-cancel-session", TenantID: defaultExecutionTenantID,
		ResolvedWorkspace: workspace, SkillRun: &SkillRunRequest{SkillName: "guided-report"},
	}
	task := &GatewayTask{
		ID: request.TaskID, Request: request, Status: "running", TenantID: defaultExecutionTenantID,
		RoleID: "Researcher", RoleRevision: "revision-1", QueueName: agentOSQueueName,
		CurrentPhase: "model", ResolvedWorkspace: workspace, CreatedAt: now,
	}
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatalf("persist AgentOS Skill Run task: %v", err)
	}
	if err := gateway.db.Create(&SkillRunRecord{
		RunID: skillRunID(task.ID), TaskID: task.ID, TenantID: task.TenantID,
		SkillName: "guided-report", ContentSHA256: strings.Repeat("b", 64),
		SkillSnapshot: `{}`, ParametersJSON: `{}`, ActivationID: skillRunActivationCallID,
		ActivationCount: 1, Phase: skillRunPhaseRunning, StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("persist Skill Run checkpoint: %v", err)
	}

	response := performAgentOSRequest(
		gateway, http.MethodPost, "/api/v1/agent/tasks/"+task.ID+"/cancel", nil, nil,
	)
	if response.code != http.StatusOK || response.body["status"] != "stopped" {
		t.Fatalf("cancel response=%d %s", response.code, response.raw)
	}
	var record SkillRunRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Phase != skillRunPhaseStopped || record.CompletedAt == nil {
		t.Fatalf("cancelled Skill Run checkpoint = phase %q completed_at=%v", record.Phase, record.CompletedAt)
	}
	if _, err := gateway.beginSkillRun(context.Background(), task, nil); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("stopped Skill Run was resumable: %v", err)
	}
}

func TestAgentOSOpenAPIDocumentsSkillRunContract(t *testing.T) {
	data, err := os.ReadFile("docs/openapi-agentos.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, marker := range []string{
		"SkillRunRequest:", "SkillRunReceipt:", "skill_run_request:",
		"skill_run:", "content_sha256:", "activation_count:",
	} {
		if !strings.Contains(document, marker) {
			t.Fatalf("AgentOS OpenAPI Skill Run contract missing %q", marker)
		}
	}
}
