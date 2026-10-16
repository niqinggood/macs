package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func newAgentOSQueueScenarioGateway(t *testing.T, db *gorm.DB, workspace string, agent Agent, gatewayConfig GatewayConfig) *GatewayService {
	t.Helper()
	cfg := &Config{Workspace: workspace, Gateway: gatewayConfig}
	logger := log.New(io.Discard, "", 0)
	factory := &AgentFactory{
		agents: map[string]Agent{"Researcher": agent}, subAgents: make(map[string]Agent),
		globalConfig: cfg, workspace: workspace, fileLogger: logger,
	}
	engine := &MultiAgentEngine{config: cfg, agentFactory: factory, logger: logger, fileLogger: logger}
	gateway := &GatewayService{
		config: cfg, engine: engine, logger: logger, db: db,
		project: NewProjectContext(workspace), tasks: make(map[string]*GatewayTask),
	}
	gateway.initSSE()
	t.Cleanup(func() {
		if gateway.agentOSQueue == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		gateway.agentOSQueue.Stop(ctx)
	})
	return gateway
}

func TestAgentOSDatabaseQueueClaimsExactlyOnceAcrossInstances(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	agent := &agentOSAPIScenarioAgent{result: "claimed once"}
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "gateway", MaxTasks: 2, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	worker := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "worker", MaxTasks: 2, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	worker.ensureAgentOSQueue().Start()

	response := submitAgentOSScenario(gateway, "k8s-client", "claim-once", nil)
	if response.code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body=%s", response.code, response.raw)
	}
	taskID := agentOSTaskIDFromResponse(t, response)
	completed := waitForAgentOSTask(t, gateway, taskID, "completed")
	if completed.Result != "claimed once" || agent.calls.Load() != 1 {
		t.Fatalf("completed=%+v calls=%d", completed, agent.calls.Load())
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.QueueOwner != worker.ensureAgentOSQueue().instanceID || record.RunAttempt != 1 {
		t.Fatalf("owner=%q attempt=%d, worker=%q", record.QueueOwner, record.RunAttempt, worker.ensureAgentOSQueue().instanceID)
	}
}

func TestAgentOSDatabaseQueueEnforcesPerInstanceConcurrency(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	release := make(chan struct{})
	started := make(chan struct{})
	agent := &agentOSAPIScenarioAgent{result: "done", release: release, started: started}
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "all", MaxTasks: 1, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})

	first := submitAgentOSScenario(gateway, "batch", "first", nil)
	second := submitAgentOSScenario(gateway, "batch", "second", nil)
	if first.code != http.StatusAccepted || second.code != http.StatusAccepted {
		t.Fatalf("submit codes = %d, %d", first.code, second.code)
	}
	waitForAgentOSSignal(t, started, "first queue task")
	time.Sleep(200 * time.Millisecond)
	if calls := agent.calls.Load(); calls != 1 {
		t.Fatalf("agent calls while first task blocked = %d, want 1", calls)
	}
	secondID := agentOSTaskIDFromResponse(t, second)
	var queued GatewayTaskRecord
	if err := db.Where("task_id = ?", secondID).First(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if queued.Status != "pending" {
		t.Fatalf("second task status = %q, want pending", queued.Status)
	}
	close(release)
	_ = waitForAgentOSTask(t, gateway, agentOSTaskIDFromResponse(t, first), "completed")
	_ = waitForAgentOSTask(t, gateway, secondID, "completed")
	if calls := agent.calls.Load(); calls != 2 {
		t.Fatalf("final agent calls = %d, want 2", calls)
	}
}

func TestAgentOSDatabaseQueueReclaimsExpiredLease(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	agent := &agentOSAPIScenarioAgent{result: "recovered"}
	worker := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "worker", MaxTasks: 1, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	now := time.Now().Round(0)
	expired := now.Add(-time.Minute)
	available := now.Add(-2 * time.Minute)
	task := &GatewayTask{
		ID: "agt_expired_lease", Status: "running", CurrentPhase: "model", RunAttempt: 1,
		CreatedAt: available, HeartbeatAt: &expired, CallerID: "scheduler",
		ResolvedWorkspace: workspace, QueueName: agentOSQueueName, QueueOwner: "dead-pod",
		LeaseToken: "dead-lease", LeaseUntil: &expired, AvailableAt: &available,
		Request: &MessageRequest{
			TaskID: "agt_expired_lease", TaskType: "chat", SessionId: "expired-session",
			AgentName: "Researcher", Message: "resume safely", Channel: "api",
			ResolvedWorkspace: workspace, CreatedAt: available,
		},
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatal(err)
	}
	worker.ensureAgentOSQueue().Start()
	completed := waitForAgentOSTask(t, worker, task.ID, "completed")
	if completed.RunAttempt != 2 || completed.Result != "recovered" || agent.calls.Load() != 1 {
		t.Fatalf("completed=%+v calls=%d", completed, agent.calls.Load())
	}
}

func TestAgentOSDatabaseQueueRejectsLegacyTaskWithoutSubject(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	gateway := newAgentOSQueueScenarioGateway(
		t,
		db,
		workspace,
		&agentOSAPIScenarioAgent{result: "must not run"},
		GatewayConfig{AgentOSMode: "worker", MaxTasks: 1, MaxQueuedTasks: 10},
	)
	now := time.Now().Round(0)
	record := GatewayTaskRecord{
		TaskID: "legacy-subjectless-task", TenantID: "tenant-a",
		SubjectID: "   ", TenantScoped: true,
		Status: "pending", CurrentPhase: "queued",
		QueueName: agentOSQueueName, AvailableAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ChatMessage{
		SessionID: "legacy-subjectless-session",
		TenantID:  "tenant-a", SubjectID: "   ",
		TaskID: record.TaskID, Role: "user",
		Status: "pending", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	task, recovered, found, err := gateway.ensureAgentOSQueue().claimOne(context.Background())
	if err != nil {
		t.Fatalf("claim legacy task: %v", err)
	}
	if task != nil || recovered || found {
		t.Fatalf("legacy task was claimable: task=%+v recovered=%t found=%t", task, recovered, found)
	}

	var stored GatewayTaskRecord
	if err := db.Where("task_id = ?", record.TaskID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" ||
		stored.CurrentPhase != "identity_migration_required" ||
		stored.ErrorMsg != legacyAgentOSSubjectMigrationError {
		t.Fatalf("legacy task was not failed closed: %+v", stored)
	}
	var message ChatMessage
	if err := db.Where("task_id = ?", record.TaskID).First(&message).Error; err != nil {
		t.Fatal(err)
	}
	if message.Status != "failed" {
		t.Fatalf("legacy task message status = %q, want failed", message.Status)
	}
}

func TestAgentOSDatabaseQueueCancelCrossInstance(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	agent := &agentOSAPIScenarioAgent{
		result: "must not complete", release: release, started: started, finished: finished,
	}
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "gateway", MaxTasks: 1, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	worker := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "worker", MaxTasks: 1, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	worker.ensureAgentOSQueue().Start()
	response := submitAgentOSScenario(gateway, "desktop", "cancel-remote", nil)
	taskID := agentOSTaskIDFromResponse(t, response)
	waitForAgentOSSignal(t, started, "remote worker start")
	canceled := performAgentOSRequest(gateway, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/cancel", nil, nil)
	if canceled.code != http.StatusOK || canceled.body["status"] != "stopped" {
		t.Fatalf("cancel response = %d %s", canceled.code, canceled.raw)
	}
	waitForAgentOSSignal(t, finished, "remote worker cancellation")
	stopped := waitForAgentOSTask(t, gateway, taskID, "stopped")
	if stopped.CurrentPhase != "stopped" || agent.calls.Load() != 1 {
		t.Fatalf("stopped=%+v calls=%d", stopped, agent.calls.Load())
	}
}

func TestAgentOSQueueGracefulShutdownRequeuesUnfinishedLease(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	agent := &agentOSAPIScenarioAgent{result: "late", release: release, started: started, finished: finished}
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, agent, GatewayConfig{
		AgentOSMode: "all", MaxTasks: 1, MaxQueuedTasks: 10, TaskPollIntervalMS: 25,
	})
	response := submitAgentOSScenario(gateway, "scheduler", "shutdown-requeue", nil)
	taskID := agentOSTaskIDFromResponse(t, response)
	waitForAgentOSSignal(t, started, "shutdown task start")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	gateway.ensureAgentOSQueue().Stop(ctx)
	cancel()
	waitForAgentOSSignal(t, finished, "shutdown task cancellation")
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "pending" || record.CurrentPhase != "queued" || record.LeaseToken != "" || record.QueueOwner != "" {
		t.Fatalf("requeued record = %+v", record)
	}
}

func TestAgentOSQueueProbesAndMetrics(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, &agentOSAPIScenarioAgent{result: "ok"}, GatewayConfig{
		AgentOSMode: "all", MaxTasks: 3, MaxQueuedTasks: 7, TaskPollIntervalMS: 25,
	})
	gateway.ensureAgentOSQueue().Start()

	live := httptest.NewRecorder()
	gateway.handleLivez(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if live.Code != http.StatusOK || !strings.Contains(live.Body.String(), `"status":"alive"`) {
		t.Fatalf("livez = %d %s", live.Code, live.Body.String())
	}
	ready := httptest.NewRecorder()
	gateway.handleReadyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readyz = %d %s", ready.Code, ready.Body.String())
	}
	var readyBody map[string]interface{}
	if err := json.Unmarshal(ready.Body.Bytes(), &readyBody); err != nil || readyBody["ready"] != true {
		t.Fatalf("ready body = %#v, err=%v", readyBody, err)
	}
	metrics := httptest.NewRecorder()
	gateway.handleAgentOSMetrics(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{"openkitty_agentos_running_local", "openkitty_agentos_queued_global", "openkitty_agentos_worker_capacity 3"} {
		if !strings.Contains(metrics.Body.String(), metric) {
			t.Fatalf("metric %q missing from:\n%s", metric, metrics.Body.String())
		}
	}
	gateway.ensureAgentOSQueue().BeginDrain()
	notReady := httptest.NewRecorder()
	gateway.handleReadyz(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining readyz = %d %s", notReady.Code, notReady.Body.String())
	}
}

func TestAgentOSQueueRejectsNewWorkWhenSharedQueueIsFull(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	gateway := newAgentOSQueueScenarioGateway(t, db, workspace, &agentOSAPIScenarioAgent{result: "unused"}, GatewayConfig{
		AgentOSMode: "gateway", MaxTasks: 1, MaxQueuedTasks: 1, TaskPollIntervalMS: 25,
	})
	first := submitAgentOSScenario(gateway, "erp", "queue-slot-1", nil)
	if first.code != http.StatusAccepted {
		t.Fatalf("first submit = %d %s", first.code, first.raw)
	}
	second := submitAgentOSScenario(gateway, "erp", "queue-slot-2", nil)
	if second.code != http.StatusTooManyRequests || !strings.Contains(second.raw, "queue is full") {
		t.Fatalf("second submit = %d %s", second.code, second.raw)
	}
	ready := httptest.NewRecorder()
	gateway.handleReadyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"admission_open":false`) {
		t.Fatalf("saturated readiness = %d %s", ready.Code, ready.Body.String())
	}
}

func TestAgentOSToolReceiptIsFencedByWorkerLease(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	now := time.Now().Round(0)
	leaseUntil := now.Add(time.Minute)
	task := &GatewayTask{
		ID: "agt_tool_fence", Status: "running", CurrentPhase: "tool:lookup", CreatedAt: now,
		HeartbeatAt: &now, QueueName: agentOSQueueName, QueueOwner: "worker-a",
		LeaseToken: "lease-a", LeaseUntil: &leaseUntil,
		Request: &MessageRequest{TaskID: "agt_tool_fence", TaskType: "chat", SessionId: "tool-fence"},
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatal(err)
	}
	ledger := newGormToolExecutionLedger(db)
	call := ToolCall{ID: "lookup-1", Name: "lookup", Arguments: map[string]interface{}{"id": 1}}
	oldContext := withAgentOSLeaseToken(context.Background(), "lease-a")
	decision, err := ledger.Begin(oldContext, task.ID, call, true)
	if err != nil || !decision.Execute {
		t.Fatalf("old worker begin = %+v, %v", decision, err)
	}
	if err := db.Model(&GatewayTaskRecord{}).Where("task_id = ?", task.ID).
		Updates(map[string]interface{}{"queue_owner": "worker-b", "lease_token": "lease-b"}).Error; err != nil {
		t.Fatal(err)
	}
	result := ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "old result"}
	if err := ledger.Finish(oldContext, task.ID, call, result, nil, 10); !errors.Is(err, ErrAgentOSLeaseLost) {
		t.Fatalf("old worker finish error = %v, want lease lost", err)
	}
	newContext := withAgentOSLeaseToken(context.Background(), "lease-b")
	decision, err = ledger.Begin(newContext, task.ID, call, true)
	if err != nil || !decision.Execute {
		t.Fatalf("new worker begin = %+v, %v", decision, err)
	}
	result.Content = "new result"
	if err := ledger.Finish(newContext, task.ID, call, result, nil, 12); err != nil {
		t.Fatalf("new worker finish: %v", err)
	}
	receipt := loadAgentOSToolReceipt(t, db, task.ID, call.ID)
	if receipt.LeaseToken != "lease-b" || receipt.Status != "succeeded" || receipt.ResultContent != "new result" {
		t.Fatalf("receipt = %+v", receipt)
	}
}
