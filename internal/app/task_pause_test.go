package app

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	sessioncore "mac/internal/session"
)

func newTaskPauseTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	gateway := &GatewayService{
		db:     openAgentOSFollowupAppDB(t),
		config: &Config{Workspace: t.TempDir()},
		tasks:  make(map[string]*GatewayTask),
		logger: log.New(io.Discard, "", 0),
	}
	gateway.initSSE()
	return gateway
}

func seedLocalPauseTask(t *testing.T, gateway *GatewayService, taskID, status string) *GatewayTask {
	t.Helper()
	now := time.Now().Round(0)
	request := &MessageRequest{
		TaskID: taskID, TaskType: "chat", SessionId: "session-" + taskID,
		Message: "continue this work", AgentName: "Assistant", Channel: "web",
		CreatedAt: now,
	}
	task := &GatewayTask{
		ID: taskID, Request: request, Status: status, CurrentPhase: status, CreatedAt: now,
		ResultCh: make(chan interface{}, 1), ProgressCh: make(chan map[string]interface{}, 1),
		InterruptCh: make(chan string, 1),
	}
	gateway.tasks[taskID] = task
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.Create(&ChatMessage{
		SessionID: request.SessionId, Role: "user", Content: request.Message,
		Status: status, TaskID: taskID, Channel: "web", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPauseGatewayTaskPendingIsDurableAndCannotStart(t *testing.T) {
	gateway := newTaskPauseTestGateway(t)
	task := seedLocalPauseTask(t, gateway, "pause-pending", "pending")

	paused, err := gateway.pauseGatewayTask(task.ID)
	if err != nil {
		t.Fatalf("pauseGatewayTask failed: %v", err)
	}
	if paused.Status != "paused" || paused.RecoveryState != sessioncore.RecoveryStateAvailable {
		t.Fatalf("unexpected paused task: status=%q recovery=%q", paused.Status, paused.RecoveryState)
	}

	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "paused" || record.CompletedAt != nil {
		t.Fatalf("unexpected durable task state: status=%q completed_at=%v", record.Status, record.CompletedAt)
	}
	var message ChatMessage
	if err := gateway.db.Where("task_id = ?", task.ID).First(&message).Error; err != nil {
		t.Fatal(err)
	}
	if message.Status != "paused" {
		t.Fatalf("chat message status = %q, want paused", message.Status)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, started := gateway.markGatewayTaskRunning(task, cancel); started {
		t.Fatal("paused task was allowed to start")
	}
}

func TestFinalizeGatewayTaskPausePersistsRecoveryJournal(t *testing.T) {
	gateway := newTaskPauseTestGateway(t)
	task := seedLocalPauseTask(t, gateway, "pause-running", "pausing")
	transcript := sessioncore.NewSessionTranscript(
		task.ID,
		"Assistant",
		sessioncore.TranscriptDirectory(gateway.config.Workspace),
	)
	transcript.RecordUserMessage("continue this work")
	task.RecoveryJournal = transcript.JournalPath()

	paused, ok := gateway.finalizeGatewayTaskPause(task)
	if !ok {
		t.Fatal("pause was not finalized")
	}
	if paused.Status != "paused" || paused.RecoveryEntryCount == 0 {
		t.Fatalf("unexpected paused recovery snapshot: %#v", paused)
	}

	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "paused" || record.RecoveryJournal != transcript.JournalPath() {
		t.Fatalf("unexpected durable recovery state: status=%q journal=%q", record.Status, record.RecoveryJournal)
	}
}

func TestPipelinePauseCompletesAtStepBoundary(t *testing.T) {
	gateway := newTaskPauseTestGateway(t)
	if err := gateway.db.AutoMigrate(&PipelineExecutionRecord{}); err != nil {
		t.Fatal(err)
	}
	executor := NewPipelineExecutor(nil, gateway, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID: "pipeline-pause", PipelineName: "test", ResolvedWorkspace: t.TempDir(),
		Status: "running", CurrentStep: 0, CreatedAt: time.Now(), running: true,
		Steps:      []StepResult{{Name: "collect", Status: "running"}, {Name: "deliver", Status: "pending"}},
		Checkpoint: &PipelineCheckpoint{PipelineExecID: "pipeline-pause"},
	}
	executor.executions[exec.ID] = exec

	if err := executor.PauseExecution(exec.ID); err != nil {
		t.Fatalf("PauseExecution failed: %v", err)
	}
	if exec.Status != "pausing" {
		t.Fatalf("status = %q, want pausing while the current step is active", exec.Status)
	}
	if !executor.pausePipelineAtBoundary(exec, 1) {
		t.Fatal("pipeline did not pause at the step boundary")
	}
	if exec.Status != "paused" || exec.CurrentStep != 1 {
		t.Fatalf("unexpected paused cursor: status=%q current_step=%d", exec.Status, exec.CurrentStep)
	}
	if exec.Checkpoint == nil || exec.Checkpoint.Phase != pipelineCheckpointPhaseStepPending || exec.Checkpoint.StepName != "deliver" {
		t.Fatalf("unexpected checkpoint: %#v", exec.Checkpoint)
	}

	var record PipelineExecutionRecord
	if err := gateway.db.Where("exec_id = ?", exec.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "paused" {
		t.Fatalf("durable pipeline status = %q, want paused", record.Status)
	}
}
