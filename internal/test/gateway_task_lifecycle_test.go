package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"mac/internal/agentresult"
	"mac/internal/meetingminutes"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type contextCapturingAgent struct {
	ctx   context.Context
	input string
}

func (a *contextCapturingAgent) Name() string { return "Researcher" }
func (a *contextCapturingAgent) Generate(ctx context.Context, input string) (string, error) {
	a.ctx = ctx
	a.input = input
	return "ok", nil
}
func (a *contextCapturingAgent) GetType() AgentType { return AgentTypeCustom }

type staticResultAgent struct {
	result string
}

func (a *staticResultAgent) Name() string { return "Researcher" }
func (a *staticResultAgent) Generate(ctx context.Context, input string) (string, error) {
	return a.result, nil
}
func (a *staticResultAgent) GetType() AgentType { return AgentTypeCustom }

type failedResultAgent struct {
	output string
	reason string
}

func (a *failedResultAgent) Name() string { return "Researcher" }
func (a *failedResultAgent) Generate(ctx context.Context, input string) (string, error) {
	return a.output, agentresult.NewError(a.reason, a.output)
}
func (a *failedResultAgent) GetType() AgentType { return AgentTypeCustom }

type cancellationBlockingAgent struct {
	started chan struct{}
}

func (a *cancellationBlockingAgent) Name() string { return "Researcher" }
func (a *cancellationBlockingAgent) Generate(ctx context.Context, input string) (string, error) {
	close(a.started)
	<-ctx.Done()
	return "late result after stop", ctx.Err()
}
func (a *cancellationBlockingAgent) GetType() AgentType { return AgentTypeCustom }

type interruptRestartAgent struct {
	started chan struct{}
	calls   int
	inputs  []string
}

func (a *interruptRestartAgent) Name() string { return "Researcher" }
func (a *interruptRestartAgent) Generate(ctx context.Context, input string) (string, error) {
	a.calls++
	a.inputs = append(a.inputs, input)
	if a.calls == 1 {
		close(a.started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "restarted successfully", nil
}
func (a *interruptRestartAgent) GetType() AgentType { return AgentTypeCustom }

type taskAwareCallbackCapture struct {
	taskID     string
	agentName  string
	thought    string
	thoughtTyp string
	legacyUsed bool
}

func (c *taskAwareCallbackCapture) OnAgentThinking(agentName string, thought string, thoughtType string) {
	c.legacyUsed = true
	c.agentName = agentName
	c.thought = thought
	c.thoughtTyp = thoughtType
}
func (c *taskAwareCallbackCapture) OnAgentThinkingForTask(taskID, agentName, thought, thoughtType string) {
	c.taskID = taskID
	c.agentName = agentName
	c.thought = thought
	c.thoughtTyp = thoughtType
}
func (c *taskAwareCallbackCapture) OnAgentAction(agentName string, action string, details map[string]interface{}) {
}
func (c *taskAwareCallbackCapture) OnAgentError(agentName string, err error, context string) {}
func (c *taskAwareCallbackCapture) OnLLMCall(agentName string, prompt string, response string, taskID string, tokenEstimate int) {
}

func newTaskLifecycleTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	dsn := fmt.Sprintf("file:gateway_task_lifecycle_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	// The gateway writes heartbeats and task phases asynchronously. A single
	// connection keeps every goroutine on the same in-memory SQLite database.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&GatewayTaskRecord{}, &ChatMessage{}, &ChatSession{}, &ToolExecutionReceipt{}, &AgentTaskRequestRecord{}); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return &GatewayService{
		db:               db,
		logger:           log.New(io.Discard, "", 0),
		tasks:            make(map[string]*GatewayTask),
		taskStreams:      make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
	}
}

func TestRecoverOrphanGatewayTasksReconcilesTasksAndMessages(t *testing.T) {
	g := newTaskLifecycleTestGateway(t)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	recoveredAt := time.Now().Truncate(time.Second)
	completedAt := old.Add(time.Minute)
	tasks := []GatewayTaskRecord{
		{TaskID: "orphan-pending", Status: "pending", Result: "partial pending result", CreatedAt: old, UpdatedAt: old},
		{TaskID: "orphan-running", Status: "running", Result: "partial running result", CreatedAt: old, UpdatedAt: old},
		{TaskID: "already-complete", Status: "completed", Result: "final result", CreatedAt: old, UpdatedAt: completedAt, CompletedAt: &completedAt},
		{TaskID: "already-failed", Status: "failed", ErrorMsg: "original failure", CreatedAt: old, UpdatedAt: completedAt, CompletedAt: &completedAt},
	}
	for i := range tasks {
		if err := g.db.Create(&tasks[i]).Error; err != nil {
			t.Fatalf("seed task %s: %v", tasks[i].TaskID, err)
		}
	}
	messages := []ChatMessage{
		{SessionID: "s1", TaskID: "orphan-pending", Role: "user", Status: "pending", Content: "pending"},
		{SessionID: "s1", TaskID: "orphan-running", Role: "user", Status: "running", Content: "running"},
		{SessionID: "s1", TaskID: "orphan-running", Role: "assistant", Status: "completed", Content: "partial"},
		{SessionID: "s2", TaskID: "already-complete", Role: "user", Status: "completed", Content: "complete"},
		{SessionID: "s3", TaskID: "already-failed", Role: "user", Status: "pending", Content: "stale pending"},
	}
	for i := range messages {
		if err := g.db.Create(&messages[i]).Error; err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	recovered, err := recoverOrphanGatewayTasks(g.db, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 2 {
		t.Fatalf("recovered task count = %d, want 2", recovered)
	}

	for taskID, wantResult := range map[string]string{
		"orphan-pending": "partial pending result",
		"orphan-running": "partial running result",
	} {
		var record GatewayTaskRecord
		if err := g.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
			t.Fatal(err)
		}
		if record.Status != "failed" || record.ErrorMsg != orphanGatewayTaskError || record.CompletedAt == nil {
			t.Fatalf("recovered task %s = %#v", taskID, record)
		}
		if record.Result != wantResult {
			t.Fatalf("recovery discarded task %s result: %q", taskID, record.Result)
		}
		if !record.CompletedAt.Equal(recoveredAt) || !record.UpdatedAt.Equal(recoveredAt) {
			t.Fatalf("recovered task %s timestamps = completed %v updated %v, want %v", taskID, record.CompletedAt, record.UpdatedAt, recoveredAt)
		}
	}

	var orphanMessages []ChatMessage
	if err := g.db.Where("task_id IN ?", []string{"orphan-pending", "orphan-running"}).Order("id").Find(&orphanMessages).Error; err != nil {
		t.Fatal(err)
	}
	if len(orphanMessages) != 3 || orphanMessages[0].Status != "failed" || orphanMessages[1].Status != "failed" || orphanMessages[2].Status != "completed" {
		t.Fatalf("orphan message statuses were not reconciled selectively: %#v", orphanMessages)
	}

	var completed GatewayTaskRecord
	if err := g.db.Where("task_id = ?", "already-complete").First(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.Result != "final result" || !completed.UpdatedAt.Equal(completedAt) {
		t.Fatalf("terminal task was modified: %#v", completed)
	}
	var reconciled ChatMessage
	if err := g.db.Where("task_id = ?", "already-failed").First(&reconciled).Error; err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != "failed" {
		t.Fatalf("terminal task's stale message status = %q, want failed", reconciled.Status)
	}

	again, err := recoverOrphanGatewayTasks(g.db, recoveredAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("recovery is not idempotent: second pass changed %d tasks", again)
	}
}

func TestEmitAgentThinkingUsesTaskAwareCallback(t *testing.T) {
	cb := &taskAwareCallbackCapture{}

	emitAgentThinking(cb, "task-1", "Researcher", "working", "rec")

	if cb.legacyUsed {
		t.Fatal("legacy callback should not be used when task-aware callback is available")
	}
	if cb.taskID != "task-1" || cb.agentName != "Researcher" || cb.thought != "working" || cb.thoughtTyp != "rec" {
		t.Fatalf("captured callback = %#v", cb)
	}
}

func TestHeartbeatTouchesOnlyRunningTask(t *testing.T) {
	g := newTaskLifecycleTestGateway(t)
	sqlDB, err := g.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite :memory: is per connection; keep the async heartbeat and the test
	// assertions on the same in-memory database.
	sqlDB.SetMaxOpenConns(1)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, record := range []GatewayTaskRecord{
		{TaskID: "heartbeat-running", Status: "running", CreatedAt: old, UpdatedAt: old},
		{TaskID: "heartbeat-completed", Status: "completed", CreatedAt: old, UpdatedAt: old},
	} {
		if err := g.db.Create(&record).Error; err != nil {
			t.Fatalf("create task %s: %v", record.TaskID, err)
		}
	}

	g.touchRunningTaskHeartbeat("heartbeat-running")
	g.touchRunningTaskHeartbeat("heartbeat-completed")
	deadline := time.Now().Add(time.Second)
	for {
		var running GatewayTaskRecord
		if err := g.db.Where("task_id = ?", "heartbeat-running").First(&running).Error; err != nil {
			t.Fatal(err)
		}
		if running.UpdatedAt.After(old) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("running heartbeat did not advance updated_at: %s", running.UpdatedAt)
		}
		time.Sleep(10 * time.Millisecond)
	}

	var completed GatewayTaskRecord
	if err := g.db.Where("task_id = ?", "heartbeat-completed").First(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if !completed.UpdatedAt.Equal(old) {
		t.Fatalf("completed task heartbeat changed updated_at: got %s want %s", completed.UpdatedAt, old)
	}
}

func TestToolCallReporterUsesTaskAwareCallback(t *testing.T) {
	cb := &taskAwareCallbackCapture{}
	agent := &LLMAgent{name: "Researcher", callback: cb}
	reporter := NewToolCallReporter(agent, "task-2")

	reporter.ReportToolStart(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "a.txt"}}, "tool")

	if cb.taskID != "task-2" {
		t.Fatalf("task id = %q, want task-2", cb.taskID)
	}
	if cb.thoughtTyp != "use_tool" {
		t.Fatalf("thought type = %q, want use_tool", cb.thoughtTyp)
	}
}

func TestPermissionRequestProgressEventIsBroadcast(t *testing.T) {
	engine := &MultiAgentEngine{recorder: NewExecutionRecorder(meetingminutes.NewEnhancedMeetingMinutes())}
	var captured ProgressEvent
	engine.SetProgressBroadcaster(func(evt ProgressEvent) {
		captured = evt
	})

	engine.OnAgentThinkingForTask("task-3", "Researcher", "权限确认：高风险工具调用需要确认", "permission_request")

	if captured.Type != "permission_request" {
		t.Fatalf("progress type = %q, want permission_request", captured.Type)
	}
	if captured.Status != "waiting_approval" {
		t.Fatalf("progress status = %q, want waiting_approval", captured.Status)
	}
	if !strings.Contains(captured.Message, "权限确认") {
		t.Fatalf("progress message = %q, want permission text", captured.Message)
	}
}

func TestFailedCompletionProgressIsNotReportedAsSuccess(t *testing.T) {
	engine := &MultiAgentEngine{recorder: NewExecutionRecorder(meetingminutes.NewEnhancedMeetingMinutes())}
	var captured ProgressEvent
	engine.SetProgressBroadcaster(func(evt ProgressEvent) {
		captured = evt
	})

	engine.OnAgentThinkingForTask("task-failed", "Researcher", "task failed: blocked", "completion_failed")

	if captured.Type != "complete" || captured.Status != "error" {
		t.Fatalf("failed completion event = %#v, want complete/error", captured)
	}
	if !strings.Contains(captured.Message, "Task failed") {
		t.Fatalf("failed completion message = %q", captured.Message)
	}
}

func TestProcessTaskWithSessionHonorsStoppedRecordBeforeSavingRunning(t *testing.T) {
	g := newTaskLifecycleTestGateway(t)
	taskID := "web_stop-t1"
	if err := g.db.Create(&GatewayTaskRecord{
		TaskID:    taskID,
		Status:    "stopped",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed stopped task: %v", err)
	}

	task := &GatewayTask{
		ID:         taskID,
		Request:    &MessageRequest{TaskID: taskID, TaskType: "chat", SessionId: "web_stop", Message: "should not run"},
		Status:     "pending",
		CreatedAt:  time.Now(),
		ResultCh:   make(chan interface{}, 1),
		ProgressCh: make(chan map[string]interface{}, 1),
	}

	g.processTaskWithSession(task, "web_stop")

	select {
	case result, ok := <-task.ResultCh:
		if !ok {
			t.Fatal("expected stopped result before channel close")
		}
		resp, ok := result.(GatewayResponse)
		if !ok || resp.Status != "stopped" {
			t.Fatalf("result = %#v, want stopped GatewayResponse", result)
		}
	default:
		t.Fatal("expected stopped result")
	}
	select {
	case _, ok := <-task.ResultCh:
		if ok {
			t.Fatal("result channel should be closed after stopped result")
		}
	default:
		t.Fatal("result channel should be closed after stopped result")
	}

	var record GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatalf("load task record: %v", err)
	}
	if record.Status != "stopped" {
		t.Fatalf("db status = %q, want stopped", record.Status)
	}
}

func TestProcessTaskWithSessionStopDoesNotBlockWhenResultChannelIsFull(t *testing.T) {
	agent := &cancellationBlockingAgent{started: make(chan struct{})}
	g := newTaskLifecycleTestGateway(t)
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{agents: map[string]Agent{"Researcher": agent}},
	}
	task := &GatewayTask{
		ID: "web_stop_full-t1",
		Request: &MessageRequest{
			TaskID: "web_stop_full-t1", TaskType: "chat", SessionId: "web_stop_full",
			Message: "wait until stopped", AgentName: "Researcher", Channel: "web",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 4),
		InterruptCh: make(chan string, 1),
	}
	task.ResultCh <- "preexisting response"
	g.tasks[task.ID] = task
	done := make(chan struct{})
	go func() {
		g.processTaskWithSession(task, task.Request.SessionId)
		close(done)
	}()

	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	stoppedTask, exists, stopped := g.stopGatewayTask(task.ID)
	if !exists || !stopped || stoppedTask.Status != "stopped" {
		t.Fatalf("stop result = (%#v, %v, %v), want stopped", stoppedTask, exists, stopped)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("processTaskWithSession blocked on a full ResultCh after stop")
	}

	snapshot, ok := g.gatewayTaskSnapshot(task.ID)
	if !ok || snapshot.Status != "stopped" || snapshot.Error == nil {
		t.Fatalf("terminal snapshot = %#v, want stopped with error", snapshot)
	}
	if got := <-task.ResultCh; got != "preexisting response" {
		t.Fatalf("existing result was replaced: %#v", got)
	}
	if _, open := <-task.ResultCh; open {
		t.Fatal("result channel should be closed by the process owner")
	}
	var record GatewayTaskRecord
	if err := g.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatalf("load stopped record: %v", err)
	}
	if record.Status != "stopped" {
		t.Fatalf("db status = %q, want stopped", record.Status)
	}
}

func TestProcessTaskWithSessionIgnoresLegacyGatewayTimeoutUntilExplicitStop(t *testing.T) {
	agent := &cancellationBlockingAgent{started: make(chan struct{})}
	g := newTaskLifecycleTestGateway(t)
	// TaskTimeout remains readable for old configuration files, but the gateway
	// must not impose a whole-task deadline on an agent run.
	g.config = &Config{Gateway: GatewayConfig{TaskTimeout: 1}}
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{agents: map[string]Agent{"Researcher": agent}},
	}
	task := &GatewayTask{
		ID: "web_timeout-t1",
		Request: &MessageRequest{
			TaskID: "web_timeout-t1", TaskType: "chat", SessionId: "web_timeout",
			Message: "wait for configured timeout", AgentName: "Researcher", Channel: "web",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 4),
		InterruptCh: make(chan string, 1),
	}
	g.tasks[task.ID] = task
	done := make(chan struct{})
	go func() {
		g.processTaskWithSession(task, task.Request.SessionId)
		close(done)
	}()

	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	select {
	case <-done:
		t.Fatal("legacy gateway task timeout unexpectedly ended the task")
	case <-time.After(1200 * time.Millisecond):
	}
	if _, exists, stopped := g.stopGatewayTask(task.ID); !exists || !stopped {
		t.Fatalf("explicit stop result = (exists=%v, stopped=%v), want true/true", exists, stopped)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("explicit stop did not cancel the task")
	}
	snapshot, ok := g.gatewayTaskSnapshot(task.ID)
	if !ok || snapshot.Status != "stopped" || snapshot.Error == nil {
		t.Fatalf("stopped task snapshot = %#v, want stopped with cancellation error", snapshot)
	}
}

func TestStopGatewayTaskUpdatesLinkedPendingMessage(t *testing.T) {
	g := newTaskLifecycleTestGateway(t)
	task := &GatewayTask{
		ID:        "web_stop_message-t1",
		Request:   &MessageRequest{TaskID: "web_stop_message-t1", SessionId: "web_stop_message", TaskType: "chat"},
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	g.tasks[task.ID] = task
	if err := g.db.Create(&ChatMessage{
		SessionID: task.Request.SessionId,
		TaskID:    task.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "stop me",
	}).Error; err != nil {
		t.Fatal(err)
	}

	_, exists, stopped := g.stopGatewayTask(task.ID)
	if !exists || !stopped {
		t.Fatalf("stop result = exists %v stopped %v", exists, stopped)
	}
	var message ChatMessage
	if err := g.db.Where("task_id = ?", task.ID).First(&message).Error; err != nil {
		t.Fatal(err)
	}
	if message.Status != "stopped" {
		t.Fatalf("linked message status = %q, want stopped", message.Status)
	}
}

func TestSaveChatMessageCreatesAndUpdatesSession(t *testing.T) {
	g := newTaskLifecycleTestGateway(t)

	if _, err := g.saveChatMessage("web_session", "user", "Researcher", "hello", "completed", "web_session-t1", "web", "user-1"); err != nil {
		t.Fatalf("save first message: %v", err)
	}
	if _, err := g.saveChatMessage("web_session", "assistant", "Researcher", "hi", "completed", "web_session-t1", "web", "user-1"); err != nil {
		t.Fatalf("save second message: %v", err)
	}

	var session ChatSession
	if err := g.db.Where("id = ?", "web_session").First(&session).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.MessageCount != 2 {
		t.Fatalf("message count = %d, want 2", session.MessageCount)
	}
	if session.UserId != "user-1" {
		t.Fatalf("user id = %q, want user-1", session.UserId)
	}
}

func TestMultiAgentEngineDefaultAgentUsesConfigOrder(t *testing.T) {
	first := &contextCapturingAgent{}
	second := &contextCapturingAgent{}
	engine := &MultiAgentEngine{
		config: &Config{
			Agents: []AgentConfig{{Name: "Second"}, {Name: "First"}},
		},
		agentFactory: &AgentFactory{
			agents: map[string]Agent{
				"First":  first,
				"Second": second,
			},
		},
	}

	name, agent, ok := engine.DefaultAgent()
	if !ok {
		t.Fatal("expected default agent")
	}
	if name != "Second" || agent != second {
		t.Fatalf("default agent = (%q, %T), want Second", name, agent)
	}
}

func TestMultiAgentEngineDefaultAgentFallsBackToSortedName(t *testing.T) {
	alpha := &contextCapturingAgent{}
	beta := &contextCapturingAgent{}
	engine := &MultiAgentEngine{
		config: &Config{},
		agentFactory: &AgentFactory{
			agents: map[string]Agent{
				"Beta":  beta,
				"Alpha": alpha,
			},
		},
	}

	name, agent, ok := engine.DefaultAgent()
	if !ok {
		t.Fatal("expected fallback default agent")
	}
	if name != "Alpha" || agent != alpha {
		t.Fatalf("fallback default agent = (%q, %T), want Alpha", name, agent)
	}
}

func TestProcessTaskPassesTaskContextToAgentGenerate(t *testing.T) {
	agent := &contextCapturingAgent{}
	g := &GatewayService{
		logger: log.New(io.Discard, "", 0),
		engine: &MultiAgentEngine{
			config: &Config{
				Agents: []AgentConfig{{Name: "Researcher"}},
			},
			agentFactory: &AgentFactory{
				agents: map[string]Agent{"Researcher": agent},
			},
		},
	}
	task := &GatewayTask{
		ID: "web_ctx-t1",
		Request: &MessageRequest{
			TaskID:    "web_ctx-t1",
			TaskType:  "chat",
			SessionId: "web_ctx",
			Message:   "hello",
			AgentName: "Researcher",
			Channel:   "web",
			UserId:    "user-1",
		},
		Status:     "pending",
		CreatedAt:  time.Now(),
		ResultCh:   make(chan interface{}, 1),
		ProgressCh: make(chan map[string]interface{}, 1),
	}

	g.processTask(task)

	if agent.input != "hello" {
		t.Fatalf("agent input = %q, want hello", agent.input)
	}
	for key, want := range map[string]string{
		"task_id":    "web_ctx-t1",
		"session_id": "web_ctx",
		"channel":    "web",
		"user_id":    "user-1",
	} {
		if got, _ := agent.ctx.Value(key).(string); got != want {
			t.Fatalf("ctx[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestProcessTaskPreservesFailedAgentOutputWithoutProgressChannel(t *testing.T) {
	agent := &failedResultAgent{output: "usable failed result", reason: "evidence incomplete"}
	g := &GatewayService{
		logger: log.New(io.Discard, "", 0),
		engine: &MultiAgentEngine{
			config:       &Config{Agents: []AgentConfig{{Name: "Researcher"}}},
			agentFactory: &AgentFactory{agents: map[string]Agent{"Researcher": agent}},
		},
	}
	task := &GatewayTask{
		ID:      "web_failed-t1",
		Request: &MessageRequest{TaskID: "web_failed-t1", TaskType: "chat", SessionId: "web_failed", Message: "hello", AgentName: "Researcher"},
		Status:  "pending", CreatedAt: time.Now(), ResultCh: make(chan interface{}, 1),
	}
	g.processTask(task)
	if task.Status != "failed" || task.Result != "usable failed result" {
		t.Fatalf("failed task lost result: status=%s result=%#v", task.Status, task.Result)
	}
	result, ok := <-task.ResultCh
	if !ok {
		t.Fatal("failed response was not sent before channel close")
	}
	response, ok := result.(GatewayResponse)
	if !ok || response.Status != "failed" || response.Result != "usable failed result" || response.Error != "evidence incomplete" {
		t.Fatalf("failed response lost output/error: %#v", result)
	}
}

func TestProcessTaskWithSessionKeepsWebUserIdentity(t *testing.T) {
	agent := &contextCapturingAgent{}
	g := newTaskLifecycleTestGateway(t)
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{
			agents: map[string]Agent{"Researcher": agent},
		},
	}

	task := &GatewayTask{
		ID: "web_user-t1",
		Request: &MessageRequest{
			TaskID:    "web_user-t1",
			TaskType:  "chat",
			SessionId: "web_user",
			Message:   "remember me",
			AgentName: "Researcher",
			Channel:   "web",
			UserId:    "user-1",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 20),
		InterruptCh: make(chan string, 1),
	}

	g.processTaskWithSession(task, "web_user")

	for key, want := range map[string]string{
		"task_id":    "web_user-t1",
		"session_id": "web_user",
		"channel":    "web",
		"user_id":    "user-1",
	} {
		if got, _ := agent.ctx.Value(key).(string); got != want {
			t.Fatalf("ctx[%s] = %q, want %q", key, got, want)
		}
	}
	if got, _ := agent.ctx.Value("user_name").(string); got == "local" {
		t.Fatalf("ctx[user_name] = %q, want real user identity to remain user_id-based", got)
	}

	var assistant ChatMessage
	if err := g.db.Where("session_id = ? AND task_id = ? AND role = ?", "web_user", "web_user-t1", "assistant").First(&assistant).Error; err != nil {
		t.Fatalf("load assistant completion message: %v", err)
	}
	if assistant.Content != "ok" {
		t.Fatalf("assistant content = %q, want ok", assistant.Content)
	}
	if assistant.UserId != "user-1" {
		t.Fatalf("assistant user_id = %q, want user-1", assistant.UserId)
	}

	var taskRecord GatewayTaskRecord
	if err := g.db.Where("task_id = ?", "web_user-t1").First(&taskRecord).Error; err != nil {
		t.Fatalf("load completed gateway task: %v", err)
	}
	if taskRecord.Status != "completed" {
		t.Fatalf("gateway task status = %q, want completed", taskRecord.Status)
	}
	if taskRecord.Result != `"ok"` {
		t.Fatalf("gateway task result = %q, want persisted JSON string", taskRecord.Result)
	}
}

func TestInterruptRestartDoesNotBlockWhenProgressBufferIsFull(t *testing.T) {
	agent := &interruptRestartAgent{started: make(chan struct{})}
	g := newTaskLifecycleTestGateway(t)
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{
			agents: map[string]Agent{"Researcher": agent},
		},
	}
	task := &GatewayTask{
		ID: "web_interrupt-t1",
		Request: &MessageRequest{
			TaskID:    "web_interrupt-t1",
			TaskType:  "chat",
			SessionId: "web_interrupt",
			Message:   "initial request",
			AgentName: "Researcher",
			Channel:   "web",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 1),
		InterruptCh: make(chan string, 1),
	}
	// Reproduce a non-streaming web client: global SSE is used, so the legacy
	// task-specific progress channel can be full with no consumer.
	task.ProgressCh <- map[string]interface{}{"type": "stale-progress"}
	g.tasks[task.ID] = task
	storeInterruptChannel(task.Request.SessionId, task.ID, task.InterruptCh)

	go g.processTaskWithSession(task, task.Request.SessionId)
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first Generate call did not start")
	}
	task.InterruptCh <- "supplemental error detail"

	select {
	case raw, ok := <-task.ResultCh:
		if !ok {
			t.Fatal("result channel closed before terminal response")
		}
		response, ok := raw.(GatewayResponse)
		if !ok || response.Status != "completed" {
			t.Fatalf("terminal response = %#v, want completed", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt restart deadlocked on a full progress channel")
	}

	if agent.calls != 2 {
		t.Fatalf("Generate calls = %d, want 2", agent.calls)
	}
	if len(agent.inputs) != 2 || !strings.Contains(agent.inputs[1], "supplemental error detail") {
		t.Fatalf("restart inputs = %#v, want appended interrupt", agent.inputs)
	}
	var record GatewayTaskRecord
	if err := g.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatalf("load task record: %v", err)
	}
	if record.Status != "completed" {
		t.Fatalf("task status = %q, want completed", record.Status)
	}
}

func TestProcessTaskWithSessionStillReturnsWhenDatabaseUnavailable(t *testing.T) {
	agent := &staticResultAgent{result: "completed without database"}
	g := &GatewayService{
		logger: log.New(io.Discard, "", 0),
		engine: &MultiAgentEngine{agentFactory: &AgentFactory{
			agents: map[string]Agent{"Researcher": agent},
		}},
		tasks:            make(map[string]*GatewayTask),
		taskStreams:      make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
	}
	task := &GatewayTask{
		ID: "web_no_db-t1",
		Request: &MessageRequest{
			TaskID: "web_no_db-t1", TaskType: "chat", SessionId: "web_no_db",
			Message: "finish even if persistence is down", AgentName: "Researcher", Channel: "web",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 20),
		InterruptCh: make(chan string, 1),
	}
	g.tasks[task.ID] = task

	g.processTaskWithSession(task, task.Request.SessionId)

	result, ok := <-task.ResultCh
	if !ok {
		t.Fatal("expected terminal response before channel close")
	}
	response, ok := result.(GatewayResponse)
	if !ok || response.Status != "completed" || response.Result != "completed without database" {
		t.Fatalf("terminal response = %#v", result)
	}
}

func TestProcessTaskWithSessionPersistsDiagnosticForEmptyCompletion(t *testing.T) {
	agent := &staticResultAgent{result: ""}
	g := newTaskLifecycleTestGateway(t)
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{
			agents: map[string]Agent{"Researcher": agent},
		},
	}

	task := &GatewayTask{
		ID: "web_empty-t1",
		Request: &MessageRequest{
			TaskID:    "web_empty-t1",
			TaskType:  "chat",
			SessionId: "web_empty",
			Message:   "produce empty",
			AgentName: "Researcher",
			Channel:   "web",
			UserId:    "user-1",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 20),
		InterruptCh: make(chan string, 1),
	}

	g.processTaskWithSession(task, "web_empty")

	var assistant ChatMessage
	if err := g.db.Where("session_id = ? AND task_id = ? AND role = ?", "web_empty", "web_empty-t1", "assistant").First(&assistant).Error; err != nil {
		t.Fatalf("load assistant diagnostic message: %v", err)
	}
	if !strings.Contains(assistant.Content, "未生成可用最终回答") {
		t.Fatalf("assistant content = %q, want diagnostic fallback", assistant.Content)
	}

	var taskRecord GatewayTaskRecord
	if err := g.db.Where("task_id = ?", "web_empty-t1").First(&taskRecord).Error; err != nil {
		t.Fatalf("load empty-result gateway task: %v", err)
	}
	if taskRecord.Result == "" || !strings.Contains(taskRecord.Result, "未生成可用最终回答") {
		t.Fatalf("gateway task result = %q, want persisted diagnostic fallback", taskRecord.Result)
	}

	result, ok := <-task.ResultCh
	if !ok {
		t.Fatal("expected diagnostic completion response before channel close")
	}
	response, ok := result.(GatewayResponse)
	if !ok || response.Status != "completed" || !strings.Contains(fmt.Sprint(response.Result), "未生成可用最终回答") {
		t.Fatalf("completion response = %#v, want non-empty diagnostic result", result)
	}
}

func TestProcessTaskWithSessionPersistsFailedAgentOutputAsFailed(t *testing.T) {
	const diagnostic = "未能生成可用最终回答。\n\n登录成功，但生成动作尚未执行。"
	agent := &failedResultAgent{output: diagnostic, reason: "task failed: no usable final answer"}
	g := newTaskLifecycleTestGateway(t)
	g.engine = &MultiAgentEngine{
		agentFactory: &AgentFactory{
			agents: map[string]Agent{"Researcher": agent},
		},
	}

	task := &GatewayTask{
		ID: "web_failed-t1",
		Request: &MessageRequest{
			TaskID:    "web_failed-t1",
			TaskType:  "chat",
			SessionId: "web_failed",
			Message:   "continue the unfinished action",
			AgentName: "Researcher",
			Channel:   "web",
			UserId:    "user-1",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		ProgressCh:  make(chan map[string]interface{}, 20),
		InterruptCh: make(chan string, 1),
	}
	if _, err := g.saveChatMessage(task.Request.SessionId, "user", "", task.Request.Message, "pending", task.ID, task.Request.Channel, task.Request.UserId); err != nil {
		t.Fatalf("seed pending user message: %v", err)
	}

	g.processTaskWithSession(task, "web_failed")

	var taskRecord GatewayTaskRecord
	if err := g.db.Where("task_id = ?", task.ID).First(&taskRecord).Error; err != nil {
		t.Fatalf("load failed gateway task: %v", err)
	}
	if taskRecord.Status != "failed" {
		t.Fatalf("gateway task status = %q, want failed", taskRecord.Status)
	}
	if !strings.Contains(taskRecord.Result, "登录成功") {
		t.Fatalf("gateway task result = %q, want persisted diagnostic", taskRecord.Result)
	}
	if taskRecord.ErrorMsg != "task failed: no usable final answer" {
		t.Fatalf("gateway task error = %q", taskRecord.ErrorMsg)
	}

	var assistant ChatMessage
	if err := g.db.Where("task_id = ? AND role = ?", task.ID, "assistant").First(&assistant).Error; err != nil {
		t.Fatalf("load failed assistant message: %v", err)
	}
	if assistant.Status != "failed" || assistant.Content != diagnostic {
		t.Fatalf("assistant completion = status %q content %q", assistant.Status, assistant.Content)
	}
	var userMessage ChatMessage
	if err := g.db.Where("task_id = ? AND role = ?", task.ID, "user").First(&userMessage).Error; err != nil {
		t.Fatalf("load failed user message: %v", err)
	}
	if userMessage.Status != "failed" {
		t.Fatalf("user message status = %q, want failed", userMessage.Status)
	}

	result, ok := <-task.ResultCh
	if !ok {
		t.Fatal("expected failed response before channel close")
	}
	response, ok := result.(GatewayResponse)
	if !ok || response.Status != "failed" || !strings.Contains(fmt.Sprint(response.Result), "登录成功") {
		t.Fatalf("failed response = %#v", result)
	}
}

func TestProcessChannelMessagePassesTaskContextAndClosesResult(t *testing.T) {
	agent := &contextCapturingAgent{}
	g := &GatewayService{
		logger: log.New(io.Discard, "", 0),
		tasks:  make(map[string]*GatewayTask),
		engine: &MultiAgentEngine{
			agentFactory: &AgentFactory{
				agents: map[string]Agent{"Researcher": agent},
			},
		},
	}
	task := &GatewayTask{
		ID: "telegram_user-1-t1",
		Request: &MessageRequest{
			TaskID:    "telegram_user-1-t1",
			TaskType:  "chat",
			SessionId: "telegram_user-1",
			Message:   "hello channel",
			Channel:   "telegram",
			UserId:    "user-1",
		},
		Status:    "pending",
		CreatedAt: time.Now(),
		ResultCh:  make(chan interface{}, 1),
	}
	g.tasks[task.ID] = task

	g.processChannelMessage(task, &ChannelMessage{
		Channel:   "telegram",
		UserID:    "user-1",
		Recipient: "recipient-1",
		Content:   "hello channel",
	})

	if agent.input != "hello channel" {
		t.Fatalf("agent input = %q, want hello channel", agent.input)
	}
	for key, want := range map[string]string{
		"task_id":    "telegram_user-1-t1",
		"session_id": "telegram_user-1",
		"channel":    "telegram",
		"user_id":    "user-1",
	} {
		if got, _ := agent.ctx.Value(key).(string); got != want {
			t.Fatalf("ctx[%s] = %q, want %q", key, got, want)
		}
	}
	select {
	case result, ok := <-task.ResultCh:
		if !ok {
			t.Fatal("expected completed result before channel close")
		}
		resp, ok := result.(GatewayResponse)
		if !ok || resp.Status != "completed" || resp.Result != "ok" {
			t.Fatalf("result = %#v, want completed ok GatewayResponse", result)
		}
	default:
		t.Fatal("expected completed result")
	}
	select {
	case _, ok := <-task.ResultCh:
		if ok {
			t.Fatal("result channel should be closed after completed result")
		}
	default:
		t.Fatal("result channel should be closed after completed result")
	}
	if task.Status != "completed" {
		t.Fatalf("task status = %q, want completed", task.Status)
	}
}

func TestHeartbeatTaskContextIncludesTaskMetadata(t *testing.T) {
	ctx := heartbeatTaskContext(HeartbeatTask{Name: "daily summary"})

	for key, want := range map[string]string{
		"session_id": "heartbeat_daily_summary",
		"channel":    "heartbeat",
		"user_id":    "heartbeat",
	} {
		if got, _ := ctx.Value(key).(string); got != want {
			t.Fatalf("ctx[%s] = %q, want %q", key, got, want)
		}
	}
	if got, _ := ctx.Value("task_id").(string); !strings.HasPrefix(got, "heartbeat_daily_summary-t") {
		t.Fatalf("ctx[task_id] = %q, want heartbeat_daily_summary-t*", got)
	}
}
