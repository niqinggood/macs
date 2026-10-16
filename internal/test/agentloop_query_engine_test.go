package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type queryEngineTestTool struct{}

func TestSkillProgressProvenanceIsBoundedAndAuditable(t *testing.T) {
	call := ToolCall{Name: "use_skill"}
	user := "技能: demo\n来源: user（用户目录/磁盘）\nSkill 文件: C:\\private\\skills\\demo\\SKILL.md\nSECRET"
	if got := skillProgressProvenance(call, user); got != "来源: user（用户目录/磁盘）" {
		t.Fatalf("user provenance = %q", got)
	}
	if got := skillProgressProvenance(call, "来源: builtin（内存）\nlong instructions"); got != "来源: builtin（内存）" {
		t.Fatalf("builtin provenance = %q", got)
	}
	if got := skillProgressProvenance(ToolCall{Name: "read_file"}, user); got != "" {
		t.Fatalf("non-Skill tool leaked provenance: %q", got)
	}
	if got := skillProgressProvenance(call, "来源: remote\nSECRET"); got != "" {
		t.Fatalf("untrusted provenance was exposed: %q", got)
	}
}

func (queryEngineTestTool) Name() string        { return "qe_test" }
func (queryEngineTestTool) Description() string { return "query engine test tool" }
func (queryEngineTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}
func (queryEngineTestTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "ok", nil
}

type queryEngineBlockingTool struct{ release <-chan struct{} }

func (queryEngineBlockingTool) Name() string        { return "qe_blocking" }
func (queryEngineBlockingTool) Description() string { return "query engine blocking tool" }
func (queryEngineBlockingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (queryEngineBlockingTool) IsReadOnly() bool { return true }
func (t queryEngineBlockingTool) Execute(context.Context, map[string]interface{}) (string, error) {
	<-t.release
	return "released", nil
}

type queryEnginePanicTool struct{}

func (queryEnginePanicTool) Name() string        { return "qe_panic" }
func (queryEnginePanicTool) Description() string { return "query engine panic tool" }
func (queryEnginePanicTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (queryEnginePanicTool) Execute(context.Context, map[string]interface{}) (string, error) {
	panic("boom")
}

type queryEngineSwitchableTool struct {
	mu    *sync.Mutex
	calls *int
}

func (queryEngineSwitchableTool) Name() string        { return "qe_switchable" }
func (queryEngineSwitchableTool) Description() string { return "query engine switchable test tool" }
func (queryEngineSwitchableTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query":   map[string]interface{}{"type": "string"},
			"succeed": map[string]interface{}{"type": "boolean"},
		},
	}
}
func (queryEngineSwitchableTool) IsReadOnly() bool { return true }
func (t queryEngineSwitchableTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	t.mu.Lock()
	*t.calls++
	t.mu.Unlock()
	if succeed, _ := params["succeed"].(bool); succeed {
		return "ok", nil
	}
	return "", errors.New("upstream unavailable")
}

func TestTurnStateIterationLifecycle(t *testing.T) {
	turn := NewTurnState("do work", "task-1", 2)

	if !turn.CanContinue() {
		t.Fatal("new turn should be able to continue")
	}
	if got := turn.NextIteration(); got != 1 {
		t.Fatalf("first iteration = %d, want 1", got)
	}
	if got := turn.NextIteration(); got != 2 {
		t.Fatalf("second iteration = %d, want 2", got)
	}
	if turn.CanContinue() {
		t.Fatal("turn should stop at max iterations")
	}

	turn.MarkSuccess("done")
	if turn.EventStatus != "success" || turn.FinalOutput != "done" {
		t.Fatalf("success state not recorded: status=%q output=%q", turn.EventStatus, turn.FinalOutput)
	}
}

func TestEnsureToolCallIDsAreStableUniqueAndNonEmpty(t *testing.T) {
	calls := []ToolCall{
		{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://a.example"}},
		{ID: "", Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://b.example"}},
	}
	first := ensureToolCallIDs(calls, "task-1", 3)
	second := ensureToolCallIDs(append([]ToolCall(nil), calls...), "task-1", 3)
	if first[0].ID == "" || first[1].ID == "" || first[0].ID == first[1].ID {
		t.Fatalf("tool call IDs are not unique/non-empty: %#v", first)
	}
	if first[0].ID != second[0].ID || first[1].ID != second[1].ID {
		t.Fatalf("tool call IDs are not stable: %#v vs %#v", first, second)
	}
	third := ensureToolCallIDs(append([]ToolCall(nil), calls...), "task-1", 4)
	if third[0].ID == first[0].ID {
		t.Fatal("tool call ID was reused across iterations")
	}
}

func TestTurnStateIterationBudgetNotice(t *testing.T) {
	turn := NewTurnState("research", "task-1", 5)

	if notice, force := turn.IterationBudgetNotice(false); notice != "" || force {
		t.Fatalf("new turn notice = %q force=%v, want none", notice, force)
	}

	turn.NextIteration()
	turn.NextIteration()
	notice, force := turn.IterationBudgetNotice(false)
	if !strings.Contains(notice, "3 model calls left") || force {
		t.Fatalf("warning notice = %q force=%v", notice, force)
	}

	turn.NextIteration()
	turn.NextIteration()
	turn.NextIteration()
	notice, force = turn.IterationBudgetNotice(false)
	if !strings.Contains(notice, "FINAL ITERATION") || !force {
		t.Fatalf("final notice = %q force=%v", notice, force)
	}
}

func TestTurnStateSingleIterationKeepsAssignedToolWindowOpen(t *testing.T) {
	turn := NewTurnState("create the deliverable", "task-tool-once", 1)
	turn.NextIteration()

	notice, force := turn.IterationBudgetNotice(true)
	if !strings.Contains(notice, "SINGLE ITERATION TOOL WINDOW") || force {
		t.Fatalf("single tool iteration notice = %q force=%v, want open tool window", notice, force)
	}

	noTools := NewTurnState("classify", "task-no-tools-once", 1)
	noTools.NextIteration()
	notice, force = noTools.IterationBudgetNotice(false)
	if !strings.Contains(notice, "FINAL ITERATION") || !force {
		t.Fatalf("single no-tool iteration notice = %q force=%v, want forced final answer", notice, force)
	}
}

func TestWebFetchURLWithQueryHintStillCountsAsDirect(t *testing.T) {
	call := ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{
		"url":   "https://weather.example/shanghai",
		"query": "Shanghai seven-day forecast",
	}}
	if got := searchModeForToolCall(call); got != "direct" {
		t.Fatalf("URL fetch with relevance hint mode = %q, want direct", got)
	}
	record, ok := parseOpenKittyToolRecord(toolResultForFinalization(call, "07/11 32℃/27℃", true))
	if !ok || record.SearchMode != "direct" || record.SourceURL != "https://weather.example/shanghai" {
		t.Fatalf("direct provenance record = %#v, ok=%v", record, ok)
	}
}

func TestRequestedEvidenceCountParsing(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"至少使用两个独立来源", 2},
		{"至少核对两个相互独立、可信的来源", 2},
		{"至少使用两个相互独立的结构化来源", 2},
		{"Use at least 8 authoritative sources", 8},
		{"来源数量：十二", 12},
	}
	for _, test := range tests {
		got := maxRequestedCount(test.input, searchSourceCountAfterNumber, searchSourceCountBeforeNumber)
		if got != test.want {
			t.Errorf("source count for %q = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestQueryEngineProvidesLifecycleComponents(t *testing.T) {
	engine := NewQueryEngine(&LLMAgent{}, nil, nil)
	if engine.Messages() == nil {
		t.Fatal("expected message store")
	}
	if engine.ToolExecutor() == nil {
		t.Fatal("expected tool executor")
	}
	if engine.ToolFinalizer() == nil {
		t.Fatal("expected tool finalizer")
	}
	if engine.ResultProcessor() == nil {
		t.Fatal("expected result processor")
	}
	if engine.StepRecorder() == nil {
		t.Fatal("expected step recorder")
	}
	if engine.LoopGuard() == nil {
		t.Fatal("expected loop guard")
	}
	if engine.Reporter() == nil {
		t.Fatal("expected reporter")
	}
}

func TestBuildAssistantAPIMessagePairsToolCallShape(t *testing.T) {
	msg := BuildAssistantAPIMessage("thinking", []ToolCall{
		{
			ID:   "call_1",
			Name: "read_file",
			Arguments: map[string]interface{}{
				"path": "main.go",
			},
		},
	})

	if msg["role"] != "assistant" {
		t.Fatalf("role = %v, want assistant", msg["role"])
	}
	calls, ok := msg["tool_calls"].([]map[string]interface{})
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls shape = %#v", msg["tool_calls"])
	}
	fn, ok := calls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function shape = %#v", calls[0]["function"])
	}
	if fn["name"] != "read_file" {
		t.Fatalf("tool name = %v, want read_file", fn["name"])
	}
	if fn["arguments"] != `{"path":"main.go"}` {
		t.Fatalf("arguments = %v", fn["arguments"])
	}
}

func TestBuildAssistantAPIMessagePreservesReturnedReasoningOnlyWhenPresent(t *testing.T) {
	withReasoning := BuildAssistantAPIMessage("", []ToolCall{{
		ID: "call_1", Name: "read_file", Arguments: map[string]interface{}{"path": "main.go"},
	}}, "provider reasoning token")
	if got := withReasoning["reasoning_content"]; got != "provider reasoning token" {
		t.Fatalf("reasoning_content = %#v, want provider response preserved", got)
	}

	withoutReasoning := BuildAssistantAPIMessage("", []ToolCall{{
		ID: "call_2", Name: "read_file", Arguments: map[string]interface{}{"path": "main.go"},
	}})
	if _, exists := withoutReasoning["reasoning_content"]; exists {
		t.Fatalf("non-thinking assistant message gained reasoning_content: %#v", withoutReasoning)
	}
}

func TestMessageStoreClonesMessages(t *testing.T) {
	hot := []map[string]interface{}{
		{"role": "system", "content": "base"},
	}
	cold := []map[string]interface{}{
		{"role": "system", "content": "base cold"},
	}
	store := NewMessageStore(hot, cold)

	hot[0]["content"] = "mutated"
	if got := store.Hot()[0]["content"]; got != "base" {
		t.Fatalf("store observed external mutation: %v", got)
	}

	exported := store.Hot()
	exported[0]["content"] = "mutated again"
	if got := store.Hot()[0]["content"]; got != "base" {
		t.Fatalf("store returned mutable internal message: %v", got)
	}

	store.AppendToolResult(ToolResult{ToolCallID: "call_1", Name: "read_file", Content: "ok"}, true)
	hotOut := store.Hot()
	coldOut := store.Cold()
	if len(hotOut) != 2 || len(coldOut) != 2 {
		t.Fatalf("unexpected message counts: hot=%d cold=%d", len(hotOut), len(coldOut))
	}
	if hotOut[1]["role"] != "tool" || hotOut[1]["tool_call_id"] != "call_1" {
		t.Fatalf("tool result message not appended correctly: %#v", hotOut[1])
	}
}

func TestMessagePairAppenderAppendsClonedMessages(t *testing.T) {
	var hot []map[string]interface{}
	var cold []map[string]interface{}
	appender := (&QueryEngine{}).BindMessages(&hot, &cold)
	msg := map[string]interface{}{"role": "system", "content": "base"}

	appender.AppendBoth(msg)
	msg["content"] = "mutated"

	if len(hot) != 1 || len(cold) != 1 {
		t.Fatalf("unexpected counts: hot=%d cold=%d", len(hot), len(cold))
	}
	if hot[0]["content"] != "base" || cold[0]["content"] != "base" {
		t.Fatalf("messages should be cloned: hot=%#v cold=%#v", hot, cold)
	}

	hot[0]["content"] = "hot-mutated"
	if cold[0]["content"] != "base" {
		t.Fatalf("hot and cold should not share maps: hot=%#v cold=%#v", hot, cold)
	}
}

func TestMessagePairAppenderAppendHotOnly(t *testing.T) {
	var hot []map[string]interface{}
	var cold []map[string]interface{}
	appender := (&QueryEngine{}).BindMessages(&hot, &cold)

	appender.AppendHot(map[string]interface{}{"role": "system", "content": "warning"})

	if len(hot) != 1 {
		t.Fatalf("hot count = %d, want 1", len(hot))
	}
	if len(cold) != 0 {
		t.Fatalf("cold should stay empty, got %#v", cold)
	}
}

func TestToolExecutionStateValidateInitializesMaps(t *testing.T) {
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int

	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		ToolCallSummary: &summary,
		CallChain:       &callChain,
		TotalDurationMs: &totalMs,
		AllToolResults:  &results,
		ToolExecCount:   &count,
	}

	if err := state.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if state.FailTracker == nil || state.BrokenTools == nil || state.FailDiagnosticInjected == nil {
		t.Fatal("validate should initialize nil tracking maps")
	}
}

func TestToolExecutionStateAppendRunResult(t *testing.T) {
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		ToolCallSummary: &summary,
		CallChain:       &callChain,
		TotalDurationMs: &totalMs,
		AllToolResults:  &results,
		ToolExecCount:   &count,
	}

	processed, err := state.AppendRunResult(AgentToolRunResult{
		ToolCall:   ToolCall{ID: "call_1", Name: "qe_test"},
		Result:     ToolResult{Content: "ok"},
		DurationMs: 15,
	}, NewToolResultProcessor())
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if !processed.Succeeded || count != 1 || totalMs != 15 {
		t.Fatalf("unexpected counters: processed=%#v count=%d total=%d", processed, count, totalMs)
	}
	if len(messages) != 1 || len(coldMessages) != 1 || messages[0]["tool_call_id"] != "call_1" {
		t.Fatalf("tool message not appended: messages=%#v cold=%#v", messages, coldMessages)
	}
	if len(summary) != 1 || len(results) != 1 {
		t.Fatalf("summary/results not recorded: summary=%#v results=%#v", summary, results)
	}
	if len(callChain) != 1 || callChain[0]["name"] != "qe_test" || callChain[0]["success"] != true {
		t.Fatalf("call chain not recorded: %#v", callChain)
	}
}

type queryEngineProgressCapture struct {
	events []struct {
		taskID      string
		thought     string
		thoughtType string
	}
}

func (c *queryEngineProgressCapture) OnAgentThinking(agentName, thought, thoughtType string) {
	c.OnAgentThinkingForTask("", agentName, thought, thoughtType)
}
func (c *queryEngineProgressCapture) OnAgentThinkingForTask(taskID, agentName, thought, thoughtType string) {
	c.events = append(c.events, struct {
		taskID      string
		thought     string
		thoughtType string
	}{taskID: taskID, thought: thought, thoughtType: thoughtType})
}
func (c *queryEngineProgressCapture) OnAgentAction(agentName, action string, details map[string]interface{}) {
}
func (c *queryEngineProgressCapture) OnAgentError(agentName string, err error, context string) {}
func (c *queryEngineProgressCapture) OnLLMCall(agentName, prompt, response, taskID string, tokenEstimate int) {
}

func TestConcurrentToolExecutionReportsProgressAndCallChain(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(queryEngineTestTool{})
	capture := &queryEngineProgressCapture{}
	agent := &LLMAgent{name: "Researcher", toolRegistry: registry, callback: capture}
	executor := NewAgentToolExecutor(agent)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		TaskID:          "parallel-task",
		ToolCallSummary: &summary,
		CallChain:       &callChain,
		TotalDurationMs: &totalMs,
		AllToolResults:  &results,
		ToolExecCount:   &count,
	}
	ctx := context.WithValue(context.Background(), "task_id", "parallel-task")
	calls := []ToolCall{
		{ID: "call_1", Name: "qe_test", Arguments: map[string]interface{}{}},
		{ID: "call_2", Name: "qe_test", Arguments: map[string]interface{}{}},
	}
	if err := executor.executeBatchConcurrent(ctx, calls, state); err != nil {
		t.Fatalf("parallel execution failed: %v", err)
	}
	starts, completions := 0, 0
	for _, event := range capture.events {
		if event.taskID != "parallel-task" {
			t.Fatalf("progress task id = %q, want parallel-task", event.taskID)
		}
		switch event.thoughtType {
		case "use_tool":
			starts++
		case "rec":
			if strings.Contains(event.thought, "completed") {
				completions++
			}
		}
	}
	if starts != 2 || completions != 2 {
		t.Fatalf("parallel progress starts=%d completions=%d events=%#v", starts, completions, capture.events)
	}
	if count != 2 || len(callChain) != 2 {
		t.Fatalf("parallel observability count=%d callChain=%#v", count, callChain)
	}
	if len(messages) != 2 || len(coldMessages) != 2 || len(results) != 2 {
		t.Fatalf("parallel results were not durably staged: hot=%d cold=%d finalization=%d", len(messages), len(coldMessages), len(results))
	}
	for i := range messages {
		if messages[i]["content"] == "" || coldMessages[i]["content"] == "" {
			t.Fatalf("parallel result %d has empty persisted content: hot=%#v cold=%#v", i, messages[i], coldMessages[i])
		}
	}
}

func newQueryEngineToolExecutionState() *ToolExecutionState {
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	return &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		ToolCallSummary: &summary,
		CallChain:       &callChain,
		TotalDurationMs: &totalMs,
		AllToolResults:  &results,
		ToolExecCount:   &count,
	}
}

func TestConcurrentExecutorBlocksExactCallAfterTwoFailures(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	registry := NewToolRegistry()
	registry.Register(queryEngineSwitchableTool{mu: &mu, calls: &calls})
	executor := NewAgentToolExecutor(&LLMAgent{name: "Researcher", toolRegistry: registry})
	state := newQueryEngineToolExecutionState()
	repeated := ToolCall{Name: "qe_switchable", Arguments: map[string]interface{}{"query": "same"}}

	firstBatch := []ToolCall{
		{ID: "fail_1", Name: repeated.Name, Arguments: repeated.Arguments},
		{ID: "fail_2", Name: repeated.Name, Arguments: repeated.Arguments},
	}
	if err := executor.executeBatchConcurrent(context.Background(), firstBatch, state); err != nil {
		t.Fatalf("first failure batch returned an executor error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("underlying executions = %d, want 2", calls)
	}

	blocked := ToolCall{ID: "blocked_3", Name: repeated.Name, Arguments: repeated.Arguments}
	if err := executor.executeBatchConcurrent(context.Background(), []ToolCall{blocked}, state); err != nil {
		t.Fatalf("blocked batch returned an executor error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("blocked call reached the underlying tool: executions=%d", calls)
	}
	if state.PreventedToolCalls != 1 {
		t.Fatalf("prevented calls = %d, want 1", state.PreventedToolCalls)
	}
	messages := *state.Messages
	if got := messages[len(messages)-1]["content"]; !strings.Contains(got.(string), "duplicate_failed_call_blocked") {
		t.Fatalf("blocked result did not preserve the exact-loop error code: %v", got)
	}
}

func TestExecutorOpensPerToolCircuitAcrossDifferentArguments(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	registry := NewToolRegistry()
	registry.Register(queryEngineSwitchableTool{mu: &mu, calls: &calls})
	executor := NewAgentToolExecutor(&LLMAgent{name: "Researcher", toolRegistry: registry})
	state := newQueryEngineToolExecutionState()

	failures := make([]ToolCall, 0, toolConsecutiveFailureBlockAfter)
	for i := 0; i < toolConsecutiveFailureBlockAfter; i++ {
		failures = append(failures, ToolCall{
			ID:        fmt.Sprintf("variant_%d", i),
			Name:      "qe_switchable",
			Arguments: map[string]interface{}{"query": fmt.Sprintf("different-%d", i)},
		})
	}
	if err := executor.executeBatchConcurrent(context.Background(), failures, state); err != nil {
		t.Fatalf("failure batch returned an executor error: %v", err)
	}
	if calls != toolConsecutiveFailureBlockAfter {
		t.Fatalf("underlying executions = %d, want %d", calls, toolConsecutiveFailureBlockAfter)
	}

	next := ToolCall{ID: "circuit_open", Name: "qe_switchable", Arguments: map[string]interface{}{"query": "another-different-query"}}
	if err := executor.executeBatchSerial(context.Background(), []ToolCall{next}, state); err != nil {
		t.Fatalf("circuit-open batch returned an executor error: %v", err)
	}
	if calls != toolConsecutiveFailureBlockAfter {
		t.Fatalf("open circuit reached the underlying tool: executions=%d", calls)
	}
	messages := *state.Messages
	if got := messages[len(messages)-1]["content"]; !strings.Contains(got.(string), "repeated_tool_failure_circuit_open") {
		t.Fatalf("blocked result did not preserve the per-tool circuit code: %v", got)
	}
}

func TestSuccessfulToolCallResetsConsecutiveFailureCircuit(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	registry := NewToolRegistry()
	registry.Register(queryEngineSwitchableTool{mu: &mu, calls: &calls})
	executor := NewAgentToolExecutor(&LLMAgent{name: "Researcher", toolRegistry: registry})
	state := newQueryEngineToolExecutionState()

	for i := 0; i < toolConsecutiveFailureBlockAfter-1; i++ {
		call := ToolCall{
			ID:        fmt.Sprintf("before_success_%d", i),
			Name:      "qe_switchable",
			Arguments: map[string]interface{}{"query": fmt.Sprintf("failure-%d", i)},
		}
		if err := executor.executeBatchSerial(context.Background(), []ToolCall{call}, state); err != nil {
			t.Fatalf("failure call %d returned an executor error: %v", i, err)
		}
	}
	success := ToolCall{ID: "success", Name: "qe_switchable", Arguments: map[string]interface{}{"succeed": true}}
	if err := executor.executeBatchSerial(context.Background(), []ToolCall{success}, state); err != nil {
		t.Fatalf("success call returned an executor error: %v", err)
	}
	if got := state.ToolConsecutiveFailures["qe_switchable"]; got != 0 {
		t.Fatalf("consecutive failures after success = %d, want 0", got)
	}

	afterSuccess := ToolCall{ID: "after_success", Name: "qe_switchable", Arguments: map[string]interface{}{"query": "allowed-after-reset"}}
	if err := executor.executeBatchSerial(context.Background(), []ToolCall{afterSuccess}, state); err != nil {
		t.Fatalf("post-reset call returned an executor error: %v", err)
	}
	if calls != toolConsecutiveFailureBlockAfter+1 {
		t.Fatalf("post-reset call was unexpectedly blocked: executions=%d", calls)
	}
}

func TestAgentToolExecutorConcurrentCancellationDoesNotHang(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	registry := NewToolRegistry()
	registry.Register(queryEngineBlockingTool{release: release})
	executor := NewAgentToolExecutor(&LLMAgent{name: "Researcher", toolRegistry: registry})
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	state := &ToolExecutionState{
		Messages: &messages, ColdMessages: &coldMessages, TaskID: "cancel-task",
		ToolCallSummary: &summary, CallChain: &callChain, TotalDurationMs: &totalMs,
		AllToolResults: &results, ToolExecCount: &count,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := executor.executeBatchConcurrent(ctx, []ToolCall{{ID: "call-block", Name: "qe_blocking", Arguments: map[string]interface{}{}}}, state)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled agent tool batch remained blocked for %v", elapsed)
	}
}

func TestAgentToolExecutorConvertsPanicToToolFailure(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(queryEnginePanicTool{})
	executor := NewAgentToolExecutor(&LLMAgent{name: "Researcher", toolRegistry: registry})
	run := executor.RunSingleTool(context.Background(), ToolCall{ID: "panic-call", Name: "qe_panic", Arguments: map[string]interface{}{}}, RunSingleToolOptions{})
	if run.Err == nil || !strings.Contains(run.Err.Error(), "panicked") {
		t.Fatalf("expected panic error, got %#v", run)
	}
	if run.Result.ToolCallID != "panic-call" || !strings.Contains(run.Result.Content, "tool panic") {
		t.Fatalf("panic result was not preserved: %#v", run.Result)
	}
}

func TestToolFailureReportsTaskAwareProgress(t *testing.T) {
	capture := &queryEngineProgressCapture{}
	reporter := NewToolCallReporter(&LLMAgent{name: "Researcher", callback: capture}, "failure-task")
	marker := "ROOT_CAUSE_TIMEOUT"
	errorText := "执行失败: " + strings.Repeat("上游网络请求失败；", 40) +
		`fetch: Get "https://www.v2ex.com/api/topics/hot.json?source=progress-test": ` + marker
	reporter.ReportToolFailure(ToolCall{Name: "social_trending"}, errorText)
	if len(capture.events) != 1 || capture.events[0].taskID != "failure-task" || capture.events[0].thoughtType != "tool_error" {
		t.Fatalf("failure progress not reported: %#v", capture.events)
	}
	if !strings.Contains(capture.events[0].thought, marker) {
		t.Fatalf("failure progress truncated useful diagnostics too early: %q", capture.events[0].thought)
	}
}

func TestToolStepRecorderRecordsSuccess(t *testing.T) {
	recorder := NewToolStepRecorder()
	call := ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "main.go"}}
	record := recorder.Record(call, ToolResult{Content: "original"}, "content", nil, 12, true)

	if !record.Succeeded || record.Content != "content" {
		t.Fatalf("unexpected record: %#v", record)
	}
	if record.Step["success"] != true || record.Step["duration_ms"] != int64(12) {
		t.Fatalf("unexpected step: %#v", record.Step)
	}

	var summaries []string
	var chain []map[string]interface{}
	var total int64
	recorder.Append(record, &summaries, &chain, &total)
	if len(summaries) != 1 || len(chain) != 1 || total != 12 {
		t.Fatalf("append failed: summaries=%#v chain=%#v total=%d", summaries, chain, total)
	}
}

func TestToolStepRecorderRecordsFailure(t *testing.T) {
	recorder := NewToolStepRecorder()
	record := recorder.Record(ToolCall{Name: "cli"}, ToolResult{}, "raw failure", context.Canceled, 7, false)

	if record.Succeeded {
		t.Fatal("failure record should not be successful")
	}
	if !strings.Contains(record.Content, "工具执行错误") || record.ErrorSummary != context.Canceled.Error() {
		t.Fatalf("unexpected failure record: %#v", record)
	}
	if record.Step["success"] != false || record.Step["error"] != context.Canceled.Error() {
		t.Fatalf("unexpected failure step: %#v", record.Step)
	}
}

func TestTurnStateBuildsToolExecutionStateWithTrackingMaps(t *testing.T) {
	turn := NewTurnState("input", "task-1", 3)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int

	state := turnToolExecutionState(turn,
		&messages,
		&coldMessages,
		&summary,
		&callChain,
		&totalMs,
		&results,
		&count,
	)

	if err := state.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if state.TaskID != "task-1" {
		t.Fatalf("task id = %q, want task-1", state.TaskID)
	}
	state.FailTracker["read_file:error"] = 1
	state.BrokenTools["read_file"] = true
	state.FailDiagnosticInjected["read_file:error"] = true

	if turn.FailTracker["read_file:error"] != 1 {
		t.Fatal("fail tracker should be shared with turn state")
	}
	if !turn.BrokenTools["read_file"] {
		t.Fatal("broken tools should be shared with turn state")
	}
	if !turn.FailDiagnosticInjected["read_file:error"] {
		t.Fatal("diagnostic markers should be shared with turn state")
	}
}

func TestToolExecutionStateTracksOnlySuccessfulHighRiskMutations(t *testing.T) {
	turn := NewTurnState("input", "task-mutate", 3)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	state := turnToolExecutionState(turn,
		&messages,
		&coldMessages,
		&summary,
		&callChain,
		&totalMs,
		&results,
		&count,
	)

	_, err := state.AppendRunResult(AgentToolRunResult{
		ToolCall: ToolCall{ID: "call-edit", Name: "edit_file", Arguments: map[string]interface{}{"path": "missing.txt"}},
		Result:   ToolResult{ToolCallID: "call-edit", Name: "edit_file", Content: "not_found"},
		Err:      errors.New("not_found"),
	}, NewToolResultProcessor())
	if err != nil {
		t.Fatalf("append failed mutation result: %v", err)
	}
	if state.MutatingToolSucceeded {
		t.Fatal("failed mutating tool must not trigger auto verification")
	}

	_, err = state.AppendRunResult(AgentToolRunResult{
		ToolCall: ToolCall{ID: "call-write", Name: "write_file", Arguments: map[string]interface{}{"path": "ok.txt"}},
		Result:   ToolResult{ToolCallID: "call-write", Name: "write_file", Content: "wrote ok.txt"},
	}, NewToolResultProcessor())
	if err != nil {
		t.Fatalf("append successful mutation result: %v", err)
	}
	if state.MutatingToolSucceeded {
		t.Fatal("ordinary successful writes must not trigger blocking auto verification")
	}

	_, err = state.AppendRunResult(AgentToolRunResult{
		ToolCall: ToolCall{ID: "call-push", Name: "git", Arguments: map[string]interface{}{"action": "push"}},
		Result:   ToolResult{ToolCallID: "call-push", Name: "git", Content: "pushed origin/main"},
	}, NewToolResultProcessor())
	if err != nil {
		t.Fatalf("append successful high-risk result: %v", err)
	}
	if !state.MutatingToolSucceeded {
		t.Fatal("successful high-risk mutation should trigger auto verification")
	}
	if !strings.Contains(state.MutatingToolTrigger, "git") || strings.Contains(state.MutatingToolTrigger, "write_file") {
		t.Fatalf("unexpected mutation trigger: %q", state.MutatingToolTrigger)
	}
}

func TestToolTurnFinalizerAppendsPendingDiagnostics(t *testing.T) {
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	diag := map[string]interface{}{
		"role":    "system",
		"content": "diagnostic",
	}

	finalizer := NewToolTurnFinalizer(nil)
	err := finalizer.Finalize(&ToolTurnFinalizeState{
		Messages:           &messages,
		ColdMessages:       &coldMessages,
		PendingDiagnostics: []map[string]interface{}{diag},
	})
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if len(messages) != 1 || len(coldMessages) != 1 {
		t.Fatalf("diagnostic not appended to both stores: hot=%d cold=%d", len(messages), len(coldMessages))
	}
	if messages[0]["content"] != "diagnostic" || coldMessages[0]["content"] != "diagnostic" {
		t.Fatalf("unexpected diagnostic content: hot=%#v cold=%#v", messages[0], coldMessages[0])
	}

	diag["content"] = "mutated"
	if messages[0]["content"] != "diagnostic" || coldMessages[0]["content"] != "diagnostic" {
		t.Fatal("finalizer should clone diagnostic messages")
	}
	messages[0]["content"] = "mutated hot"
	if coldMessages[0]["content"] != "diagnostic" {
		t.Fatal("hot and cold messages should not share the same map")
	}
}

func TestToolTurnFinalizerKeepsDiagnosticsAfterToolResult(t *testing.T) {
	messages := []map[string]interface{}{
		BuildAssistantAPIMessage("", []ToolCall{{ID: "call-1", Name: "cli", Arguments: map[string]interface{}{"command": "bad"}}}),
		BuildToolResultAPIMessage(ToolResult{ToolCallID: "call-1", Name: "cli", Content: "failed"}, false),
	}
	coldMessages := cloneAPIMessages(messages)

	finalizer := NewToolTurnFinalizer(nil)
	err := finalizer.Finalize(&ToolTurnFinalizeState{
		Messages:           &messages,
		ColdMessages:       &coldMessages,
		PendingDiagnostics: []map[string]interface{}{{"role": "system", "content": "diagnostic"}},
	})
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages length = %d, want 3", len(messages))
	}
	if messages[1]["role"] != "tool" || messages[1]["tool_call_id"] != "call-1" {
		t.Fatalf("tool result must immediately follow assistant tool_calls: %#v", messages)
	}
	if messages[2]["role"] != "system" || messages[2]["content"] != "diagnostic" {
		t.Fatalf("diagnostic should be appended after tool result: %#v", messages)
	}
}

func TestToolTurnFinalizerReplacesStaleInternalDiagnostic(t *testing.T) {
	old := newInternalControlMessage("TOOL RETRY GUARD", "old warning")
	messages := []map[string]interface{}{old}
	coldMessages := cloneAPIMessages(messages)
	latest := newInternalControlMessage("TOOL RETRY GUARD", "latest warning")

	err := NewToolTurnFinalizer(nil).Finalize(&ToolTurnFinalizeState{
		Messages:           &messages,
		ColdMessages:       &coldMessages,
		PendingDiagnostics: []map[string]interface{}{latest},
	})
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	for name, list := range map[string][]map[string]interface{}{"hot": messages, "cold": coldMessages} {
		if len(list) != 1 {
			t.Fatalf("%s retained stale retry diagnostics: %#v", name, list)
		}
		content, _ := list[0]["content"].(string)
		if !strings.Contains(content, "latest warning") || strings.Contains(content, "old warning") {
			t.Fatalf("%s retry diagnostic was not replaced: %q", name, content)
		}
	}
}

func TestToolTurnFinalizerDoesNotAppendToolReviewToConversation(t *testing.T) {
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	finalizer := NewToolTurnFinalizer(&LLMAgent{})

	err := finalizer.Finalize(&ToolTurnFinalizeState{
		Messages:           &messages,
		ColdMessages:       &coldMessages,
		ToolExecCount:      5,
		ToolReviewInterval: 5,
		FailTracker:        map[string]int{"code_check:confirmation required": 2},
		BrokenTools:        map[string]bool{},
		TaskID:             "review-test",
	})
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if len(messages) != 0 || len(coldMessages) != 0 {
		t.Fatalf("tool review should stay out of model conversation: hot=%#v cold=%#v", messages, coldMessages)
	}
}

func TestTurnStateBuildsToolTurnFinalizeState(t *testing.T) {
	turn := NewTurnState("input", "task-2", 4)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	state := turnToolTurnFinalizeState(turn,
		&messages,
		&coldMessages,
		[]map[string]interface{}{{"role": "system", "content": "diag"}},
		5,
		5,
		2,
	)

	if err := state.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if state.TaskID != "task-2" || state.ToolExecCount != 5 || state.ToolReviewInterval != 5 || state.ToolCallCount != 2 {
		t.Fatalf("unexpected finalize state: %#v", state)
	}
	state.FailTracker["cli:error"] = 1
	state.BrokenTools["cli"] = true
	if turn.FailTracker["cli:error"] != 1 || !turn.BrokenTools["cli"] {
		t.Fatal("finalize state should share tracking maps with turn state")
	}
}

func TestAgentToolExecutorRunSingleToolExecutesRegistryTool(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(queryEngineTestTool{})
	agent := &LLMAgent{toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)

	run := executor.RunSingleTool(context.Background(), ToolCall{
		ID:        "call_1",
		Name:      "qe_test",
		Arguments: map[string]interface{}{},
	}, RunSingleToolOptions{Index: 7})

	if run.Index != 7 {
		t.Fatalf("index = %d, want 7", run.Index)
	}
	if run.Err != nil {
		t.Fatalf("run failed: %v", run.Err)
	}
	if run.Result.ToolCallID != "call_1" || run.Result.Name != "qe_test" || run.Result.Content != "ok" {
		t.Fatalf("unexpected result: %#v", run.Result)
	}
}

func TestAgentToolExecutorExecuteAppendsToolResults(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(queryEngineTestTool{})
	agent := &LLMAgent{toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	chain := []map[string]interface{}{}
	var totalMs int64
	results := []string{}
	var count int
	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		TaskID:          "task-1",
		ToolCallSummary: &summary,
		CallChain:       &chain,
		TotalDurationMs: &totalMs,
		AllToolResults:  &results,
		ToolExecCount:   &count,
	}

	err := executor.Execute(context.Background(), []ToolCall{{
		ID:        "call_1",
		Name:      "qe_test",
		Arguments: map[string]interface{}{},
	}}, state)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if count != 1 || len(messages) != 1 || len(coldMessages) != 1 {
		t.Fatalf("tool result not recorded: count=%d messages=%#v cold=%#v", count, messages, coldMessages)
	}
	if messages[0]["tool_call_id"] != "call_1" || messages[0]["content"] != "ok" {
		t.Fatalf("unexpected tool message: %#v", messages[0])
	}
}

func TestAgentToolExecutorRunSingleToolHandlesNilExecutor(t *testing.T) {
	var executor *AgentToolExecutor
	run := executor.RunSingleTool(context.Background(), ToolCall{
		ID:   "call_1",
		Name: "missing",
	}, RunSingleToolOptions{})

	if run.Err == nil {
		t.Fatal("expected nil executor error")
	}
	if !strings.Contains(run.Result.Content, "not initialized") {
		t.Fatalf("expected structured error content, got %#v", run.Result)
	}
}

func TestToolResultProcessorPreservesNonEmptyErrorContent(t *testing.T) {
	processor := NewToolResultProcessor()
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}

	processed := processor.Process(AgentToolRunResult{
		ToolCall: ToolCall{ID: "call_1", Name: "cli"},
		Result:   ToolResult{Content: "[CONFIRMATION REQUIRED] Tool 'cli' is HIGH risk."},
		Err:      context.Canceled,
	}, failTracker, brokenTools)

	if processed.Succeeded {
		t.Fatal("confirmation required content should be marked failed")
	}
	if processed.Content != "[CONFIRMATION REQUIRED] Tool 'cli' is HIGH risk." {
		t.Fatalf("content should be preserved, got %q", processed.Content)
	}
	if strings.Contains(processed.Content, "tool error:") {
		t.Fatalf("non-empty content should not be replaced, got %q", processed.Content)
	}
	if processed.Message["status"] != false {
		t.Fatalf("message should carry failed status: %#v", processed.Message)
	}
}

func TestToolResultProcessorBuildsFallbackErrorContent(t *testing.T) {
	processor := NewToolResultProcessor()
	processed := processor.Process(AgentToolRunResult{
		ToolCall: ToolCall{ID: "call_1", Name: "cli"},
		Err:      context.Canceled,
	}, map[string]int{}, map[string]bool{})

	if processed.Succeeded {
		t.Fatal("error should be marked failed")
	}
	if !strings.Contains(processed.Content, "tool error:") {
		t.Fatalf("expected fallback tool error, got %q", processed.Content)
	}
}

func TestToolResultProcessorDoesNotMarkToolBrokenAfterRepeatedFailures(t *testing.T) {
	processor := NewToolResultProcessor()
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}
	run := AgentToolRunResult{
		ToolCall: ToolCall{ID: "call_1", Name: "cli"},
		Result:   ToolResult{Content: "执行失败: boom"},
	}

	processor.Process(run, failTracker, brokenTools)
	processor.Process(run, failTracker, brokenTools)
	processed := processor.Process(run, failTracker, brokenTools)

	if brokenTools["cli"] {
		t.Fatal("tool should not be marked broken after repeated result failures")
	}
	if len(failTracker) != 1 || failTracker[processed.FailKey] != 3 {
		t.Fatalf("failure stats should still be tracked, got key=%q tracker=%#v", processed.FailKey, failTracker)
	}
}

func TestToolResultProcessorDeduplicatesLongResult(t *testing.T) {
	processor := NewToolResultProcessor()
	hashes := map[string]int{}
	content := strings.Repeat("x", 250)

	first, duplicate, id := processor.DeduplicateLongResult("read_file", content, hashes)
	if duplicate || id != 0 || first != content {
		t.Fatalf("first result should not be duplicate: content=%q duplicate=%v id=%d", first, duplicate, id)
	}

	second, duplicate, id := processor.DeduplicateLongResult("read_file", content, hashes)
	if !duplicate || id != 1 {
		t.Fatalf("second result should be duplicate #1: content=%q duplicate=%v id=%d", second, duplicate, id)
	}
	if !strings.Contains(second, "Duplicate") {
		t.Fatalf("duplicate result should be compact marker, got %q", second)
	}
	if !strings.Contains(second, "executed successfully") || !strings.Contains(second, "Do not rerun") {
		t.Fatalf("duplicate marker must distinguish output compaction from skipped execution, got %q", second)
	}
}

func TestToolResultProcessorTracksNoProgressForReadOnlyTools(t *testing.T) {
	processor := NewToolResultProcessor()
	hashes := map[string]int{}

	if got := processor.TrackNoProgress("read_file", "same", hashes); got != 1 {
		t.Fatalf("first read-only repeat count = %d, want 1", got)
	}
	if got := processor.TrackNoProgress("read_file", "same", hashes); got != 2 {
		t.Fatalf("second read-only repeat count = %d, want 2", got)
	}
	if got := processor.TrackNoProgress("write_file", "same", hashes); got != 0 {
		t.Fatalf("write_file should not be tracked as read-only, got %d", got)
	}
	if got := processor.TrackNoProgress("read_file", strings.Repeat("x", 4000), hashes); got != 0 {
		t.Fatalf("large content should not be tracked, got %d", got)
	}
}

func TestToolResultProcessorTrackFailureRecordsStatsWithoutInjectingDiagnostic(t *testing.T) {
	processor := NewToolResultProcessor()
	failTracker := map[string]int{}
	injected := map[string]bool{}
	signatures := map[string]int{}

	first := processor.TrackFailure("cli", "执行失败: boom", "cli:{}", failTracker, injected, signatures)
	if first.FailCount != 1 {
		t.Fatalf("first fail count = %d, want 1", first.FailCount)
	}
	if signatures["cli:{}"] != 1 {
		t.Fatalf("signature count = %d, want 1", signatures["cli:{}"])
	}

	second := processor.TrackFailure("cli", "执行失败: boom", "cli:{}", failTracker, injected, signatures)
	if second.FailCount != 2 {
		t.Fatalf("second fail count = %d, want 2", second.FailCount)
	}
	if injected[second.FailKey] != true {
		t.Fatal("repeated failure should be recorded for observability")
	}
	if signatures["cli:{}"] != 2 {
		t.Fatalf("signature count = %d, want 2", signatures["cli:{}"])
	}

	third := processor.TrackFailure("cli", "执行失败: boom", "cli:{}", failTracker, injected, signatures)
	if third.FailCount != 3 {
		t.Fatalf("third fail count = %d, want 3", third.FailCount)
	}
}

func TestToolResultProcessorApplyGuardrailAfterNoController(t *testing.T) {
	processor := NewToolResultProcessor()
	result := processor.ApplyGuardrailAfter(nil, AgentToolRunResult{
		ToolCall: ToolCall{Name: "cli"},
	}, "ok")

	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if result.Decision != nil || result.Halt || result.Err != nil {
		t.Fatalf("nil controller should not create decision: %#v", result)
	}
}

func TestToolResultProcessorApplyGuardrailAfterReportsCircuitBreakerWithoutHalting(t *testing.T) {
	processor := NewToolResultProcessor()
	controller := NewToolGuardrailController(GuardrailConfig{
		Enabled:                true,
		MaxConsecutiveFailures: 1,
	})

	result := processor.ApplyGuardrailAfter(controller, AgentToolRunResult{
		ToolCall: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "bad"}},
		Err:      context.Canceled,
	}, "tool error: canceled")

	if result.Halt {
		t.Fatalf("guardrail stats must not halt execution, got %#v", result)
	}
	if result.Decision == nil || result.Decision.ShouldHaltTurn() {
		t.Fatalf("expected non-halting observability decision, got %#v", result.Decision)
	}
	if result.Err != nil {
		t.Fatalf("guardrail observation should not return an execution error, got %v", result.Err)
	}
}

func TestToolCallLoopGuardBlocksAfterRepeatedFailure(t *testing.T) {
	guard := NewToolCallLoopGuard(2)
	call := ToolCall{
		ID:        "call_1",
		Name:      "cli",
		Arguments: map[string]interface{}{"command": "bad"},
	}
	signatures := map[string]int{}

	first := guard.Check(call, signatures)
	if first.Blocked {
		t.Fatalf("first check should not block: %#v", first)
	}
	signatures[first.Signature] = 2

	blocked := guard.Check(call, signatures)
	if !blocked.Blocked {
		t.Fatalf("loop guard should block the third identical failed call: %#v", blocked)
	}
	if blocked.Attempt != 3 {
		t.Fatalf("attempt = %d, want 3", blocked.Attempt)
	}
	if blocked.Err == nil {
		t.Fatal("blocked call should return a structured execution error")
	}
	if blocked.Result.ExecutionSucceeded == nil || *blocked.Result.ExecutionSucceeded {
		t.Fatalf("blocked result should carry authoritative failure status: %#v", blocked.Result)
	}
	if blocked.Result.ErrorCode != "duplicate_failed_call_blocked" ||
		!strings.Contains(blocked.Result.Content, "duplicate_failed_call_blocked") ||
		!strings.Contains(blocked.Message, "[LOOP BLOCKED]") {
		t.Fatalf("unexpected blocked decision: %#v", blocked)
	}
}

func TestToolCallLoopGuardSignatureIncludesNameAndArgs(t *testing.T) {
	guard := NewToolCallLoopGuard(2)
	sig := guard.Signature(ToolCall{
		Name:      "read_file",
		Arguments: map[string]interface{}{"path": "a.go"},
	})
	if !strings.Contains(sig, "read_file:") || !strings.Contains(sig, `"path":"a.go"`) {
		t.Fatalf("unexpected signature: %s", sig)
	}
}

func TestToolCallReporterNoopsWithoutAgent(t *testing.T) {
	reporter := NewToolCallReporter(nil)
	call := ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "echo ok"}}

	reporter.RecordAssistantToolCall("task-1", call)
	reporter.ReportToolStart(call, "tool")
	reporter.SaveToolResult("task-1", call, "ok")
	reporter.ReportToolFailure(call, "boom")
	reporter.ReportToolSuccess(call, "ok", 1)
	reporter.ReportNoProgress("read_file", 3)
	reporter.ReportFailureDiagnostic("cli", 2)
	reporter.ReportLoopBlocked(call, "cli:{}", 3)
	reporter.ReportDeduplicated("read_file", 1)
	reporter.ReportErrorTrack("cli", "exit_status", 2, false)
	reporter.ReportGuardrailCircuitBreaker("halt")
	reporter.ReportToolMessageAppended(call)
}

func TestToolCallReporterOnlyClaimsSavedFileForRealOverflow(t *testing.T) {
	capture := &queryEngineProgressCapture{}
	reporter := NewToolCallReporter(&LLMAgent{name: "Researcher", callback: capture}, "tool-progress")

	reporter.ReportToolSuccess(ToolCall{Name: "read_file"}, strings.Repeat("x", 2000), 500)
	if len(capture.events) != 1 {
		t.Fatalf("events = %d, want 1", len(capture.events))
	}
	if strings.Contains(capture.events[0].thought, "saved to file") {
		t.Fatalf("ordinary large result was falsely reported as saved: %q", capture.events[0].thought)
	}
	if !strings.Contains(capture.events[0].thought, "500 tokens") {
		t.Fatalf("ordinary large result should report token count: %q", capture.events[0].thought)
	}

	capture.events = nil
	reporter.ReportToolSuccess(ToolCall{Name: "grep"}, "preview\n...[完整内容已保存至 storage/tool_results/grep.txt (9000 chars)]", 800)
	if len(capture.events) != 1 || !strings.Contains(capture.events[0].thought, "overflow saved to file") {
		t.Fatalf("real overflow should report saved file: %#v", capture.events)
	}
}

func TestExternalEvidenceToolResultCarriesGroundingBoundary(t *testing.T) {
	msg := BuildToolResultAPIMessage(ToolResult{
		ToolCallID: "call_weather",
		Name:       "web_fetch",
		Content:    "上海 7 月 11 日有雨。Source: https://weather.example/shanghai",
	}, true)
	content, _ := msg["content"].(string)
	for _, want := range []string{
		"上海 7 月 11 日有雨",
		"[EVIDENCE BOUNDARY]",
		"Never fill missing fields with plausible values",
		"cite only URLs actually returned by tools",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("web result message missing %q: %s", want, content)
		}
	}
}

func TestEvidenceBoundaryIsScopedToSuccessfulExternalReads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
		want    bool
	}{
		{name: "web_search", success: true, want: true},
		{name: "social_trending", success: true, want: true},
		{name: "read_file", success: true, want: false},
		{name: "web_fetch", success: false, want: false},
	} {
		msg := BuildToolResultAPIMessage(ToolResult{Name: tc.name, Content: "result"}, tc.success)
		content, _ := msg["content"].(string)
		if got := strings.Contains(content, "[EVIDENCE BOUNDARY]"); got != tc.want {
			t.Fatalf("tool=%s success=%v boundary=%v, want %v: %s", tc.name, tc.success, got, tc.want, content)
		}
	}
}

func TestExternalEvidenceBoundaryIsIdempotent(t *testing.T) {
	once := appendExternalEvidenceBoundary("result")
	twice := appendExternalEvidenceBoundary(once)
	if once != twice {
		t.Fatalf("boundary duplicated:\nonce=%q\ntwice=%q", once, twice)
	}
}

func TestDuplicatePlanHousekeepingCallsAreCollapsedBeforeAssistantMessage(t *testing.T) {
	calls := []ToolCall{
		{ID: "one", Name: "update_plan_step", Arguments: map[string]interface{}{"step_id": "step_1", "status": "completed"}},
		{ID: "duplicate", Name: "update_plan_step", Arguments: map[string]interface{}{"step_id": "step_1", "status": "completed", "title": "ignored extra"}},
		{ID: "next", Name: "update_plan_step", Arguments: map[string]interface{}{"step_id": "step_2", "status": "in_progress"}},
		{ID: "fetch-a", Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://same.example"}},
		{ID: "fetch-b", Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://same.example"}},
	}
	filtered, dropped := deduplicateHousekeepingToolCalls(calls)
	if dropped != 1 || len(filtered) != 4 {
		t.Fatalf("filtered=%#v dropped=%d", filtered, dropped)
	}
	for i, want := range []string{"one", "next", "fetch-a", "fetch-b"} {
		if filtered[i].ID != want {
			t.Fatalf("filtered[%d]=%q, want %q", i, filtered[i].ID, want)
		}
	}
}
