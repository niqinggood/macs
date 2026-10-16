package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type pipelineCheckpointTestAgent struct {
	calls                 *atomic.Int32
	taskID                string
	persistID             string
	resumeState           *turnResumeState
	publishCheckpointPath string
	publishTranscriptPath string
	afterPublish          func()
	output                string
	generateErr           error
}

type pipelineCheckpointSequenceAgent struct {
	calls   *atomic.Int32
	outputs []string
	errors  []error
}

func (agent *pipelineCheckpointSequenceAgent) Name() string       { return "pipeline-checkpoint-sequence" }
func (agent *pipelineCheckpointSequenceAgent) GetType() AgentType { return AgentTypeCustom }
func (agent *pipelineCheckpointSequenceAgent) Generate(context.Context, string) (string, error) {
	call := int(agent.calls.Add(1)) - 1
	var output string
	var err error
	if call >= 0 && call < len(agent.outputs) {
		output = agent.outputs[call]
	}
	if call >= 0 && call < len(agent.errors) {
		err = agent.errors[call]
	}
	return output, err
}

func (agent *pipelineCheckpointTestAgent) Name() string       { return "pipeline-checkpoint-test" }
func (agent *pipelineCheckpointTestAgent) GetType() AgentType { return AgentTypeCustom }
func (agent *pipelineCheckpointTestAgent) Generate(ctx context.Context, _ string) (string, error) {
	if agent.calls != nil {
		agent.calls.Add(1)
	}
	agent.taskID = taskIDFromContext(ctx)
	agent.persistID = turnPersistenceIDFromContext(ctx, "")
	if agent.publishCheckpointPath != "" || agent.publishTranscriptPath != "" {
		publishTurnPersistencePaths(ctx, agent.publishCheckpointPath, agent.publishTranscriptPath)
		if agent.afterPublish != nil {
			agent.afterPublish()
		}
	}
	if state, ok := turnResumeStateFromContext(ctx); ok {
		agent.resumeState = &state
	}
	return agent.output, agent.generateErr
}

func newPipelineCheckpointTestExecutor(t *testing.T, workspace string, creator func(AgentConfig, string) (Agent, error)) (*PipelineExecutor, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(workspace, "pipeline-checkpoint-test.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&PipelineExecutionRecord{}, &GatewayTaskRecord{}); err != nil {
		t.Fatalf("migrate pipeline checkpoint tables: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	gateway := &GatewayService{
		config: &Config{Workspace: workspace},
		db:     db,
		logger: log.New(io.Discard, "", 0),
	}
	factory := &AgentFactory{
		subAgents:           make(map[string]Agent),
		testSubAgentCreator: creator,
	}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	gateway.pipelineExec = executor
	return executor, db
}

func pipelineCheckpointTestExecution(workspace string) *PipelineExecution {
	pipeline := Pipeline{Name: "checkpoint-pipeline", Steps: []PipelineStep{{Name: "draft", NoTools: true, MaxIter: 1}}}
	return &PipelineExecution{
		ID:                "pipe-checkpoint-test",
		PipelineName:      pipeline.Name,
		ResolvedWorkspace: normalizeProjectWorkspace(workspace),
		CurrentStep:       0,
		Status:            "running",
		Steps:             []StepResult{{Name: "draft", Status: "running", Revision: 1}},
		TaskID:            "gateway-parent-task",
		Mode:              RunModeInteractive,
		Input:             "write the result",
		CreatedAt:         time.Now(),
		RuntimePipeline:   &pipeline,
		Checkpoint: &PipelineCheckpoint{
			Version: pipelineCheckpointVersion, PipelineExecID: "pipe-checkpoint-test",
			ParentTaskID: "gateway-parent-task", StepIndex: 0, StepName: "draft",
			StepRevision: 1, Attempt: 1, Phase: pipelineCheckpointPhaseStepRunning,
			RouteEntry: -1, Turns: make(map[string]*PipelineTurnCheckpoint), UpdatedAt: time.Now(),
		},
	}
}

func TestPipelineStepTurnUsesStablePersistenceIdentityWithoutReplacingParentTask(t *testing.T) {
	workspace := t.TempDir()
	var calls atomic.Int32
	agent := &pipelineCheckpointTestAgent{calls: &calls, output: "durable result"}
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return agent, nil
	})
	exec := pipelineCheckpointTestExecution(workspace)
	ctx := context.WithValue(context.Background(), "task_id", exec.TaskID)
	step := &exec.RuntimePipeline.Steps[0]

	output, err := executor.executeStep(ctx, exec, step, 0, "rendered prompt", "session-1")
	if err != nil || output != "durable result" {
		t.Fatalf("executeStep output=%q err=%v", output, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Generate calls=%d, want 1", calls.Load())
	}
	if agent.taskID != exec.TaskID {
		t.Fatalf("outer task id=%q, want %q", agent.taskID, exec.TaskID)
	}
	if agent.persistID == "" || agent.persistID == exec.TaskID {
		t.Fatalf("turn persistence id=%q, want stable child id", agent.persistID)
	}
	key := pipelineTurnCheckpointKey(exec.ID, 0, 1, 1, step, "rendered prompt")
	turn := exec.Checkpoint.Turns[key]
	if turn == nil || turn.TaskID != agent.persistID || turn.Phase != "success" || turn.Output != output {
		t.Fatalf("persisted turn=%+v", turn)
	}
	if _, err := os.Stat(pipelineCheckpointPath(workspace, exec.ID)); err != nil {
		t.Fatalf("pipeline checkpoint sidecar: %v", err)
	}
}

func TestPipelineStepPublishesTurnSidecarBeforeGenerateReturns(t *testing.T) {
	workspace := t.TempDir()
	checkpointPath := filepath.Join(workspace, "live-turn.checkpoint.json")
	transcriptPath := filepath.Join(workspace, "live-turn.json")
	agent := &pipelineCheckpointTestAgent{
		publishCheckpointPath: checkpointPath,
		publishTranscriptPath: transcriptPath,
		output:                "durable result",
	}
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return agent, nil
	})
	exec := pipelineCheckpointTestExecution(workspace)
	step := &exec.RuntimePipeline.Steps[0]
	prompt := "publish the live turn sidecar"
	key := pipelineTurnCheckpointKey(exec.ID, 0, 1, 1, step, prompt)
	publishedBeforeReturn := false
	agent.afterPublish = func() {
		persisted, err := loadPipelineCheckpoint(pipelineCheckpointPath(workspace, exec.ID))
		if err != nil {
			t.Errorf("load live pipeline checkpoint: %v", err)
			return
		}
		turn := persisted.Turns[key]
		publishedBeforeReturn = persisted.ActiveTurnKey == key &&
			persisted.TurnCheckpoint == checkpointPath &&
			persisted.TurnTranscript == transcriptPath &&
			turn != nil && turn.CheckpointPath == checkpointPath && turn.TranscriptPath == transcriptPath
	}

	output, err := executor.executeStep(
		context.WithValue(context.Background(), "task_id", exec.TaskID),
		exec,
		step,
		0,
		prompt,
		"session-1",
	)
	if err != nil || output != "durable result" {
		t.Fatalf("executeStep output=%q err=%v", output, err)
	}
	if !publishedBeforeReturn {
		t.Fatal("pipeline checkpoint did not publish live Turn sidecar paths before Generate returned")
	}
	turn := exec.Checkpoint.Turns[key]
	if turn == nil || turn.CheckpointPath != checkpointPath || turn.TranscriptPath != transcriptPath {
		t.Fatalf("completed turn discarded early-published paths: %+v", turn)
	}
}

func TestPipelineStepTurnReusesTerminalCheckpointAfterRestart(t *testing.T) {
	workspace := t.TempDir()
	exec := pipelineCheckpointTestExecution(workspace)
	step := &exec.RuntimePipeline.Steps[0]
	prompt := "same deterministic prompt"
	key := pipelineTurnCheckpointKey(exec.ID, 0, 1, 1, step, prompt)
	turnTaskID := pipelineStepExecutionID(exec.ID, 0, 1, 1, key)
	exec.Checkpoint.Turns[key] = &PipelineTurnCheckpoint{
		Key: key, TaskID: turnTaskID, ReceiptTaskID: exec.TaskID,
		StepIndex: 0, StepRevision: 1, Attempt: 1, Phase: "success",
		Output: "already completed before crash", UpdatedAt: time.Now(),
	}
	if err := writePipelineCheckpoint(pipelineCheckpointPath(workspace, exec.ID), exec.Checkpoint); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return &pipelineCheckpointTestAgent{calls: &calls, output: "duplicate"}, nil
	})

	output, err := executor.executeStep(context.WithValue(context.Background(), "task_id", exec.TaskID), exec, step, 0, prompt, "session-1")
	if err != nil || output != "already completed before crash" {
		t.Fatalf("resumed output=%q err=%v", output, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("terminal checkpoint issued %d duplicate model call(s)", calls.Load())
	}
}

func TestPipelineStepRetryReusesEachDurableAttemptAfterRestart(t *testing.T) {
	workspace := t.TempDir()
	var firstProcessCalls atomic.Int32
	sequence := &pipelineCheckpointSequenceAgent{
		calls:   &firstProcessCalls,
		outputs: []string{"partial result", "final result"},
		errors:  []error{errors.New("temporary provider failure"), nil},
	}
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return sequence, nil
	})
	executor.SetRetrySleep(func(time.Duration) {})
	exec := pipelineCheckpointTestExecution(workspace)
	exec.RuntimePipeline.Steps[0].Repeat = &StepRepeat{Max: 2}
	step := &exec.RuntimePipeline.Steps[0]
	prompt := "retry with stable attempt identities"

	output, err := executor.executeStep(context.WithValue(context.Background(), "task_id", exec.TaskID), exec, step, 0, prompt, "session-1")
	if err != nil || output != "final result" {
		t.Fatalf("first process output=%q err=%v", output, err)
	}
	if firstProcessCalls.Load() != 2 {
		t.Fatalf("first process calls=%d, want 2", firstProcessCalls.Load())
	}

	restarted := executor.loadFromDB(exec.ID)
	if restarted == nil || restarted.RuntimePipeline == nil {
		t.Fatal("durable execution could not be reloaded")
	}
	var restartedCalls atomic.Int32
	restartedFactory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(AgentConfig, string) (Agent, error) {
			return &pipelineCheckpointTestAgent{calls: &restartedCalls, output: "duplicate"}, nil
		},
	}
	restartedExecutor := NewPipelineExecutor(restartedFactory, executor.gateway, log.New(io.Discard, "", 0))
	restartedExecutor.SetRetrySleep(func(time.Duration) {})

	output, err = restartedExecutor.executeStep(
		context.WithValue(context.Background(), "task_id", restarted.TaskID),
		restarted,
		&restarted.RuntimePipeline.Steps[0],
		0,
		prompt,
		"session-1",
	)
	if err != nil || output != "final result" {
		t.Fatalf("restarted output=%q err=%v", output, err)
	}
	if restartedCalls.Load() != 0 {
		t.Fatalf("restart issued %d duplicate model call(s)", restartedCalls.Load())
	}
}

func TestPipelineStepTurnInjectsInterruptedTurnCheckpoint(t *testing.T) {
	workspace := t.TempDir()
	exec := pipelineCheckpointTestExecution(workspace)
	step := &exec.RuntimePipeline.Steps[0]
	prompt := "resume interrupted prompt"
	key := pipelineTurnCheckpointKey(exec.ID, 0, 1, 1, step, prompt)
	turnTaskID := pipelineStepExecutionID(exec.ID, 0, 1, 1, key)
	transcript := NewSessionTranscript(turnTaskID, "pipeline-checkpoint-test", filepath.Join(workspace, ".openkitty", "logs", "transcripts"))
	checkpoint := &TurnCheckpoint{
		TaskID: turnTaskID, ReceiptTaskID: exec.TaskID, Input: prompt,
		Phase: turnCheckpointPhaseToolsPending, Iteration: 2, MaxIterations: 5,
		Messages:         []map[string]interface{}{{"role": "user", "content": prompt}},
		PendingToolCalls: []TurnCheckpointToolCall{{ID: "call-pending", Name: "read_file", Arguments: map[string]interface{}{"path": "input.md"}}},
	}
	if err := transcript.RecordTurnCheckpoint(checkpoint); err != nil {
		t.Fatalf("record interrupted turn: %v", err)
	}
	exec.Checkpoint.Turns[key] = &PipelineTurnCheckpoint{
		Key: key, TaskID: turnTaskID, ReceiptTaskID: exec.TaskID,
		StepIndex: 0, StepRevision: 1, Attempt: 1, Phase: "running",
		CheckpointPath: transcript.CheckpointPath(), UpdatedAt: time.Now(),
	}
	agent := &pipelineCheckpointTestAgent{output: "resumed"}
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return agent, nil
	})
	output, err := executor.executeStep(context.WithValue(context.Background(), "task_id", exec.TaskID), exec, step, 0, prompt, "session-1")
	if err != nil || output != "resumed" {
		t.Fatalf("resume output=%q err=%v", output, err)
	}
	if agent.resumeState == nil || agent.resumeState.Checkpoint == nil {
		t.Fatal("sub-agent did not receive structured TurnCheckpoint")
	}
	if pending := agent.resumeState.Checkpoint.PendingCalls(); len(pending) != 1 || pending[0].ID != "call-pending" {
		t.Fatalf("pending calls=%+v", pending)
	}
	if agent.resumeState.SourceTaskID != exec.TaskID {
		t.Fatalf("receipt namespace=%q, want parent %q", agent.resumeState.SourceTaskID, exec.TaskID)
	}
}

func TestPipelineRecoveryDefersAgentOSExecutionWithoutDestroyingState(t *testing.T) {
	workspace := t.TempDir()
	executor, db := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return &pipelineCheckpointTestAgent{output: "unexpected"}, nil
	})
	exec := pipelineCheckpointTestExecution(workspace)
	if err := db.Create(&GatewayTaskRecord{TaskID: exec.TaskID, QueueName: agentOSQueueName, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("create AgentOS parent: %v", err)
	}
	executor.saveToDB(exec)
	executor2 := NewPipelineExecutor(executor.agentFactory, executor.gateway, log.New(io.Discard, "", 0))
	executor.gateway.pipelineExec = executor2

	executor2.RecoverOrphanExecutions()
	recovered, ok := executor2.GetExecution(exec.ID)
	if !ok {
		t.Fatal("orphan execution was not reloaded")
	}
	if recovered.Status != "running" || recovered.running {
		t.Fatalf("recovered status=%q running=%v", recovered.Status, recovered.running)
	}
	var record PipelineExecutionRecord
	if err := db.Where("exec_id = ?", exec.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" || strings.TrimSpace(record.StateJSON) == "" {
		t.Fatalf("durable record status=%q state=%q", record.Status, record.StateJSON)
	}
	if adopted, found := executor2.FindRecoverableExecutionByTaskID(exec.TaskID, "", ""); !found || adopted.ID != exec.ID {
		t.Fatalf("AgentOS could not adopt recovered execution: found=%v exec=%+v", found, adopted)
	}
}

func TestPipelineResumeInterruptedRunsFromPersistedStep(t *testing.T) {
	workspace := t.TempDir()
	var calls atomic.Int32
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return &pipelineCheckpointTestAgent{calls: &calls, output: "resumed step output"}, nil
	})
	exec := pipelineCheckpointTestExecution(workspace)
	executor.saveToDB(exec)

	restarted := NewPipelineExecutor(executor.agentFactory, executor.gateway, log.New(io.Discard, "", 0))
	restartedExec := restarted.loadFromDB(exec.ID)
	if restartedExec == nil {
		t.Fatal("persisted execution could not be loaded")
	}
	restarted.mu.Lock()
	restarted.executions[restartedExec.ID] = restartedExec
	restarted.mu.Unlock()

	if _, err := restarted.ResumeInterrupted(context.Background(), restartedExec.ID); err != nil {
		t.Fatalf("ResumeInterrupted: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		recovered, ok := restarted.GetExecution(restartedExec.ID)
		if !ok {
			t.Fatal("resumed execution disappeared")
		}
		recovered.mu.Lock()
		status := recovered.Status
		running := recovered.running
		stepStatus := recovered.Steps[0].Status
		stepOutput := recovered.Steps[0].Output
		recovered.mu.Unlock()
		if status == "completed" && !running {
			if stepStatus != "completed" || stepOutput != "resumed step output" {
				t.Fatalf("resumed step status=%q output=%q", stepStatus, stepOutput)
			}
			break
		}
		if status == "failed" || status == "stopped" || time.Now().After(deadline) {
			t.Fatalf("resumed execution status=%q", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("resumed Generate calls=%d, want 1", calls.Load())
	}
}

func TestParallelTurnCheckpointKeepsParentWorkflowCursor(t *testing.T) {
	workspace := t.TempDir()
	executor, _ := newPipelineCheckpointTestExecutor(t, workspace, func(AgentConfig, string) (Agent, error) {
		return &pipelineCheckpointTestAgent{output: "branch result"}, nil
	})
	exec := pipelineCheckpointTestExecution(workspace)
	exec.Steps = append(exec.Steps, StepResult{Name: "fanout", Status: "running", Revision: 1})
	exec.CurrentStep = 1
	exec.Checkpoint.StepIndex = 1
	branch := &PipelineStep{Name: "branch-a", NoTools: true, MaxIter: 1}
	auditIndex := parallelAuditStepIndex(1, 0)

	if _, err := executor.executeStep(context.WithValue(context.Background(), "task_id", exec.TaskID), exec, branch, auditIndex, "branch prompt", "session-1"); err != nil {
		t.Fatal(err)
	}
	if exec.Checkpoint.StepIndex != 1 {
		t.Fatalf("parallel branch moved workflow cursor to %d, want parent 1", exec.Checkpoint.StepIndex)
	}
	key := pipelineTurnCheckpointKey(exec.ID, auditIndex, 1, 1, branch, "branch prompt")
	if turn := exec.Checkpoint.Turns[key]; turn == nil || turn.StepIndex != auditIndex {
		t.Fatalf("branch-local turn identity lost: %+v", turn)
	}
}
