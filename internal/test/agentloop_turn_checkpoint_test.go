package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSessionTranscriptTurnCheckpointRoundTrip(t *testing.T) {
	transcript := NewSessionTranscript("checkpoint-task", "Researcher", t.TempDir())
	transcript.RecordUserMessage("continue the report")

	checkpoint := &TurnCheckpoint{
		Input:         "continue the report",
		Phase:         turnCheckpointPhaseToolsPending,
		Iteration:     7,
		MaxIterations: 40,
		Messages: []map[string]interface{}{
			{"role": "system", "content": "old system"},
			{"role": "user", "content": "continue the report"},
			BuildAssistantAPIMessage("", []ToolCall{{
				ID: "call-write", Name: "write_file",
				Arguments:   map[string]interface{}{"path": "report.md", "content": "draft"},
				HostManaged: true,
			}}),
		},
		PendingToolCalls: checkpointToolCalls([]ToolCall{{
			ID: "call-write", Name: "write_file",
			Arguments:   map[string]interface{}{"path": "report.md", "content": "draft"},
			HostManaged: true,
		}}),
		ToolExecCount: 3,
		FailTracker:   map[string]int{"read_file:timeout": 1},
		ActivePlan: &TurnCheckpointPlan{
			PlanID: "checkpoint-plan",
			Steps: []PlanStep{
				{ID: "step_1", Title: "Collect evidence", Status: PlanStepCompleted},
				{ID: "step_2", Title: "Write report", Status: PlanStepInProgress},
			},
		},
	}
	if err := transcript.RecordTurnCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.PendingToolCalls = checkpointToolCalls([]ToolCall{{
		ID: "call-verify", Name: "cli",
		Arguments: map[string]interface{}{"command": "go test ./..."},
	}})
	checkpoint.LastCompletedToolCallID = "call-write"
	if err := transcript.RecordTurnCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(transcript.CheckpointPath()); err != nil {
		t.Fatalf("checkpoint sidecar missing: %v", err)
	}
	recovered, err := RecoverSessionTranscriptFromJournal(transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Checkpoint == nil {
		t.Fatal("recovered transcript lost turn checkpoint")
	}
	got := recovered.Checkpoint
	if got.Revision != 2 || got.Iteration != 7 || got.Phase != turnCheckpointPhaseToolsPending {
		t.Fatalf("unexpected recovered checkpoint: %+v", got)
	}
	pending := got.PendingCalls()
	if len(pending) != 1 || pending[0].ID != "call-verify" || pending[0].Name != "cli" {
		t.Fatalf("pending tool position was not recovered: %#v", pending)
	}
	if got.LastCompletedToolCallID != "call-write" || got.ToolExecCount != 3 || got.FailTracker["read_file:timeout"] != 1 {
		t.Fatalf("checkpoint execution state was not recovered: %+v", got)
	}
	if got.ActivePlan == nil || got.ActivePlan.PlanID != "checkpoint-plan" || len(got.ActivePlan.Steps) != 2 || got.ActivePlan.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("checkpoint active plan was not recovered: %#v", got.ActivePlan)
	}
	journal, err := os.ReadFile(transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(journal), `"event":"turn_checkpoint"`) != 2 {
		t.Fatalf("journal does not contain two checkpoint markers:\n%s", journal)
	}
}

func TestTurnCheckpointVersionOneWithoutPlanRemainsReadable(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "legacy.checkpoint.json"
	legacy := `{
  "version": 1,
  "revision": 3,
  "task_id": "legacy-task",
  "input": "continue",
  "phase": "after_tools",
  "iteration": 2,
  "max_iterations": 8,
  "messages": [{"role":"user","content":"continue"}]
}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := loadTurnCheckpoint(path)
	if err != nil {
		t.Fatalf("version-1 checkpoint without active_plan was rejected: %v", err)
	}
	if checkpoint.ActivePlan != nil || checkpoint.TaskID != "legacy-task" || checkpoint.Revision != 3 {
		t.Fatalf("legacy checkpoint changed unexpectedly: %#v", checkpoint)
	}
}

func TestCheckpointActivePlanSnapshotIsIndependentAndCompletedPlanDoesNotRevive(t *testing.T) {
	plan := &ActivePlan{
		PlanID: "durable-plan",
		Steps: []PlanStep{
			{ID: "step_1", Title: "First", Status: PlanStepInProgress},
			{ID: "step_2", Title: "Second", Status: PlanStepPending},
		},
	}
	snapshot := checkpointActivePlan(plan)
	if snapshot == nil {
		t.Fatal("active plan was not snapshotted")
	}
	if err := plan.MarkStep("step_1", PlanStepCompleted); err != nil {
		t.Fatal(err)
	}
	if snapshot.Steps[0].Status != PlanStepInProgress || snapshot.Steps[1].Status != PlanStepPending {
		t.Fatalf("checkpoint snapshot shared mutable plan state: %#v", snapshot.Steps)
	}

	restored := activePlanFromTurnCheckpoint(snapshot)
	if restored == nil || restored.PlanID != "durable-plan" || restored.Steps[0].Status != PlanStepInProgress {
		t.Fatalf("incomplete plan was not restored: %#v", restored)
	}
	completed := &TurnCheckpointPlan{
		PlanID: "done-plan",
		Steps: []PlanStep{
			{ID: "step_1", Status: PlanStepCompleted},
			{ID: "step_2", Status: PlanStepSkipped},
		},
	}
	if revived := activePlanFromTurnCheckpoint(completed); revived != nil {
		t.Fatalf("completed plan was revived after recovery: %#v", revived)
	}
}

func TestTurnCheckpointRestoresCurrentSystemAndSavedConversation(t *testing.T) {
	current := []map[string]interface{}{
		{"role": "system", "content": "current static"},
		{"role": "system", "content": "current dynamic"},
	}
	saved := []map[string]interface{}{
		{"role": "system", "content": "stale static"},
		{"role": "system", "content": "stale dynamic"},
		{"role": "user", "content": "original request"},
		{"role": "assistant", "content": "working"},
	}

	restored := restoreTurnCheckpointMessages(current, saved)
	if len(restored) != 4 || restored[0]["content"] != "current static" || restored[1]["content"] != "current dynamic" {
		t.Fatalf("current system prefix was not preserved: %#v", restored)
	}
	if restored[2]["content"] != "original request" || restored[3]["content"] != "working" {
		t.Fatalf("saved conversation was not restored: %#v", restored)
	}
}

func TestTurnCheckpointPendingCallUsesOriginalReceiptAndDoesNotRepeatSuccess(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	tool := &agentOSScenarioTool{name: "checkpoint_write", readOnly: false}
	executor := agentOSScenarioExecutor(tool)
	call := ToolCall{
		ID: "call-stable", Name: "checkpoint_write",
		Arguments: map[string]interface{}{"path": "result.txt"},
	}

	first := runAgentOSScenarioTool(executor, agentOSToolContext(db, "interrupted-task"), call)
	if !ToolResultExecutionSucceeded(first.Result, first.Err) || tool.calls.Load() != 1 {
		t.Fatalf("initial tool call = %+v, executions=%d", first, tool.calls.Load())
	}

	resumeCtx := agentOSToolContext(db, "resumed-task")
	resumeCtx = withToolExecutionReceiptTaskID(resumeCtx, "interrupted-task")
	second := executor.RunSingleTool(resumeCtx, call, RunSingleToolOptions{
		SkipHooks: true, SkipSafety: true, SkipSnapshot: true,
	})
	if !ToolResultExecutionSucceeded(second.Result, second.Err) {
		t.Fatalf("reconciled tool call = %+v", second)
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("successful mutating call was executed %d times, want once", tool.calls.Load())
	}

	var oldCount, newCount int64
	if err := db.Model(&ToolExecutionReceipt{}).Where("task_id = ?", "interrupted-task").Count(&oldCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ToolExecutionReceipt{}).Where("task_id = ?", "resumed-task").Count(&newCount).Error; err != nil {
		t.Fatal(err)
	}
	if oldCount != 1 || newCount != 0 {
		t.Fatalf("receipt scope old=%d new=%d, want old=1 new=0", oldCount, newCount)
	}
}

func TestTurnResumeStateContextKeepsSupplementSeparate(t *testing.T) {
	checkpoint := &TurnCheckpoint{
		Version: turnCheckpointVersion, TaskID: "old-task", ReceiptTaskID: "old-task",
		Input: "original", Phase: turnCheckpointPhaseAfterTools,
	}
	ctx := withTurnResumeState(context.Background(), checkpoint, "old-task", "use the final filename")
	state, ok := turnResumeStateFromContext(ctx)
	if !ok || state.Checkpoint == nil {
		t.Fatal("resume state missing")
	}
	if state.SourceTaskID != "old-task" || state.Supplement != "use the final filename" || state.Checkpoint.Input != "original" {
		t.Fatalf("unexpected resume state: %+v", state)
	}
	state.Checkpoint.Input = "mutated"
	again, _ := turnResumeStateFromContext(ctx)
	if again.Checkpoint.Input != "original" {
		t.Fatal("resume context leaked a mutable checkpoint pointer")
	}
}

func TestTurnCheckpointResumeIterationMatchesBoundary(t *testing.T) {
	modelPending := &TurnCheckpoint{Iteration: 9, Phase: turnCheckpointPhaseModelPending}
	toolsPending := &TurnCheckpoint{Iteration: 9, Phase: turnCheckpointPhaseToolsPending}
	afterTools := &TurnCheckpoint{Iteration: 9, Phase: turnCheckpointPhaseAfterTools}
	if got := turnCheckpointResumeIteration(modelPending); got != 8 {
		t.Fatalf("model-pending resume iteration = %d, want 8 so loop retries iteration 9", got)
	}
	if got := turnCheckpointResumeIteration(toolsPending); got != 9 {
		t.Fatalf("tools-pending resume iteration = %d, want 9", got)
	}
	if got := turnCheckpointResumeIteration(afterTools); got != 9 {
		t.Fatalf("after-tools resume iteration = %d, want 9", got)
	}
}

func TestGenerateResumesPendingCheckpointBeforeNextModelTurn(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	tool := &agentOSScenarioTool{
		name: "checkpoint_probe", readOnly: false,
		execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			return "durable-result-marker", nil
		},
	}
	call := ToolCall{
		ID: "call-resume-stable", Name: tool.Name(),
		Arguments: map[string]interface{}{"target": "report.md"},
	}
	initialExecutor := agentOSScenarioExecutor(tool)
	initial := runAgentOSScenarioTool(initialExecutor, agentOSToolContext(db, "interrupted-task"), call)
	if !ToolResultExecutionSucceeded(initial.Result, initial.Err) || tool.calls.Load() != 1 {
		t.Fatalf("initial receipt setup = %+v, executions=%d", initial, tool.calls.Load())
	}

	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "recovered final answer"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 6)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(tool)
	checkpoint := &TurnCheckpoint{
		Version:       turnCheckpointVersion,
		Revision:      4,
		TaskID:        "interrupted-task",
		ReceiptTaskID: "interrupted-task",
		Input:         "finish the interrupted report",
		Phase:         turnCheckpointPhaseToolsPending,
		Iteration:     1,
		MaxIterations: 6,
		Messages: []map[string]interface{}{
			{"role": "system", "content": "stale system"},
			{"role": "user", "content": "finish the interrupted report"},
			BuildAssistantAPIMessage("", []ToolCall{call}),
		},
		PendingToolCalls: checkpointToolCalls([]ToolCall{call}),
	}
	ctx := context.WithValue(context.Background(), "task_id", "resumed-task")
	ctx = withToolExecutionLedger(ctx, newGormToolExecutionLedger(db))
	ctx = withTurnResumeState(ctx, checkpoint, "interrupted-task", "keep the filename")

	output, err := agent.Generate(ctx, "legacy recovery prompt should not replace checkpoint input")
	if err != nil {
		t.Fatalf("Generate resume failed: %v\n%s", err, output)
	}
	if output != "recovered final answer" {
		t.Fatalf("resumed output = %q", output)
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("durably successful pending call executed %d times, want once", tool.calls.Load())
	}
	if transport.requestCount() != 1 {
		t.Fatalf("model request count = %d, want one continuation request", transport.requestCount())
	}
	for _, want := range []string{"durable-result-marker", "call-resume-stable", "finish the interrupted report", "keep the filename"} {
		if !transport.requestContains(want) {
			t.Fatalf("continued model request missing %q", want)
		}
	}
	transport.mu.Lock()
	requestBody := append([]byte(nil), transport.requests[0]...)
	transport.mu.Unlock()
	var request struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(requestBody, &request); err != nil {
		t.Fatalf("decode continued model request: %v", err)
	}
	for index, message := range request.Messages {
		if message["role"] != "assistant" || len(apiToolCallMaps(message["tool_calls"])) == 0 {
			continue
		}
		if index+1 >= len(request.Messages) || request.Messages[index+1]["role"] != "tool" || request.Messages[index+1]["tool_call_id"] != call.ID {
			t.Fatalf("recovery inserted a message between tool call and result: %#v", request.Messages[index:])
		}
		break
	}
	recovered, err := RecoverSessionTranscriptFromJournal(agent.transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "success" || recovered.FinalOutput != "recovered final answer" {
		t.Fatalf("resumed transcript was not finalized: %+v", recovered)
	}
}

func planToolCompletionJSON(t *testing.T, callID, stepID, status string) string {
	t.Helper()
	arguments, err := json.Marshal(map[string]interface{}{"step_id": stepID, "status": status})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{map[string]interface{}{
					"id": callID, "type": "function",
					"function": map[string]interface{}{
						"name": "update_plan_step", "arguments": string(arguments),
					},
				}},
			},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGenerateRestoresActivePlanAndContinuesItsProgress(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: planToolCompletionJSON(t, "call-finish-plan", "step_2", "completed")},
		{contentType: "application/json", body: completionJSON(t, "stop", "recovered plan final answer")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 6)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(NewUpdatePlanStepTool(agent))
	checkpoint := &TurnCheckpoint{
		Version:       turnCheckpointVersion,
		Revision:      5,
		TaskID:        "interrupted-plan-task",
		ReceiptTaskID: "interrupted-plan-task",
		Input:         "finish the durable plan",
		Phase:         turnCheckpointPhaseAfterTools,
		Iteration:     1,
		MaxIterations: 6,
		Messages: []map[string]interface{}{
			{"role": "system", "content": "stale system"},
			{"role": "user", "content": "finish the durable plan"},
		},
		ActivePlan: &TurnCheckpointPlan{
			PlanID: "durable-plan-resume",
			Steps: []PlanStep{
				{ID: "step_1", Title: "Collect inputs", Status: PlanStepCompleted},
				{ID: "step_2", Title: "Deliver result", Description: "Finish after recovery", Status: PlanStepInProgress},
			},
		},
	}
	ctx := context.WithValue(context.Background(), "task_id", "resumed-plan-task")
	ctx = withTurnResumeState(ctx, checkpoint, "interrupted-plan-task", "")

	output, err := agent.Generate(ctx, "legacy prompt")
	if err != nil {
		t.Fatalf("Generate with restored plan failed: %v\n%s", err, output)
	}
	if output != "recovered plan final answer" || transport.requestCount() != 2 {
		t.Fatalf("restored plan continuation output=%q requests=%d", output, transport.requestCount())
	}
	if !transport.requestContains("All steps complete") || !transport.requestContains("Deliver result") {
		t.Fatal("update_plan_step did not operate on the restored plan")
	}
	if agent.GetActivePlan() != nil {
		t.Fatal("completed restored plan remained active after successful completion")
	}
	persisted, err := loadTurnCheckpoint(agent.transcript.CheckpointPath())
	if err != nil {
		t.Fatalf("load continued checkpoint: %v", err)
	}
	if persisted.ActivePlan == nil || persisted.ActivePlan.PlanID != "durable-plan-resume" || persisted.ActivePlan.Steps[1].Status != PlanStepCompleted {
		t.Fatalf("continued checkpoint did not persist completed plan progress: %#v", persisted.ActivePlan)
	}
	if revived := activePlanFromTurnCheckpoint(persisted.ActivePlan); revived != nil {
		t.Fatalf("completed persisted plan would revive on another recovery: %#v", revived)
	}
}
