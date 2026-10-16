package llmapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLLMAppStartIsDurablyIdempotentAndRejectsPayloadMismatch(t *testing.T) {
	workspace := t.TempDir()
	tool := newLLMAppToolForTest(workspace, func(
		context.Context, string, []map[string]interface{}, int, float64,
	) (string, error) {
		return "done", nil
	}, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "idempotent-start", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	params := map[string]interface{}{
		"action": "start", "name": "idempotent-start", "input": "same",
		"idempotency_key": "client-request-42",
	}
	first := executeLLMAppAction(t, tool, params)
	second := executeLLMAppAction(t, tool, params)
	if first["run_id"] != second["run_id"] || second["idempotent_replay"] != true {
		t.Fatalf("start retry did not return the original run: first=%#v second=%#v", first, second)
	}
	if first["lifecycle"] != llmAppLifecycleDetached {
		t.Fatalf("compatibility lifecycle default changed: %#v", first)
	}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "start", "name": "idempotent-start", "input": "different",
		"idempotency_key": "client-request-42",
	})
	if err == nil || !strings.Contains(err.Error(), "different llm_app start payload") {
		t.Fatalf("expected idempotency payload mismatch, got %v", err)
	}
	keyDigest := fmt.Sprint(waitForLLMAppStatus(t, tool, fmt.Sprint(first["run_id"]), llmAppStatusCompleted)["idempotency_digest"])
	if keyDigest == "" || strings.Contains(keyDigest, "client-request-42") {
		t.Fatalf("idempotency key was not safely digested: %q", keyDigest)
	}
}

func TestLLMAppLifecycleAttachedCancelsAndDetachedSurvivesParent(t *testing.T) {
	workspace := t.TempDir()
	releaseDetached := make(chan struct{})
	attachedStarted := make(chan struct{})
	var calls atomic.Int32
	runtime := Runtime{ModelCaller: func(
		ctx context.Context, _ string, _ []map[string]interface{}, _ int, _ float64,
	) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			close(attachedStarted)
			<-ctx.Done()
			return ModelResponse{}, ctx.Err()
		}
		select {
		case <-releaseDetached:
			return ModelResponse{Content: "done"}, nil
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}}
	tool := NewToolWithRuntime(workspace, log.New(io.Discard, "", 0), runtime, nil, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "lifecycle-app", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	attachedCtx, cancelAttached := context.WithCancel(context.Background())
	attachedJSON, err := tool.Execute(attachedCtx, map[string]interface{}{
		"action": "start", "name": "lifecycle-app", "input": "attached", "lifecycle": "attached",
	})
	if err != nil {
		t.Fatal(err)
	}
	var attached map[string]interface{}
	_ = json.Unmarshal([]byte(attachedJSON), &attached)
	select {
	case <-attachedStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("attached model call did not start")
	}
	cancelAttached()
	waitForLLMAppStatus(t, tool, fmt.Sprint(attached["run_id"]), llmAppStatusStopped)

	detachedCtx, cancelDetached := context.WithCancel(context.Background())
	detachedJSON, err := tool.Execute(detachedCtx, map[string]interface{}{
		"action": "start", "name": "lifecycle-app", "input": "detached", "lifecycle": "detached",
	})
	if err != nil {
		t.Fatal(err)
	}
	var detached map[string]interface{}
	_ = json.Unmarshal([]byte(detachedJSON), &detached)
	cancelDetached()
	time.Sleep(100 * time.Millisecond)
	status := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "status", "run_id": fmt.Sprint(detached["run_id"]),
	})
	if isTerminalLLMAppStatus(fmt.Sprint(status["status"])) {
		t.Fatalf("detached run followed parent cancellation: %#v", status)
	}
	close(releaseDetached)
	waitForLLMAppStatus(t, tool, fmt.Sprint(detached["run_id"]), llmAppStatusCompleted)
}

func TestLLMAppAccountsProviderUsageAndEnforcesRunBudget(t *testing.T) {
	workspace := t.TempDir()
	var calls atomic.Int32
	runtime := Runtime{ModelCaller: func(
		context.Context, string, []map[string]interface{}, int, float64,
	) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{
			Content: `{"nested":{"label":"ok"}}`, PromptTokens: 7, CompletionTokens: 4, RequestID: "req-usage-1",
		}, nil
	}}
	tool := NewToolWithRuntime(workspace, log.New(io.Discard, "", 0), runtime, nil, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "usage-app", "workflow": "prompt", "prompt_template": "{{item}}",
		"output_schema": map[string]interface{}{
			"type": "object", "required": []interface{}{"nested"},
			"properties": map[string]interface{}{
				"nested": map[string]interface{}{
					"type": "object", "required": []interface{}{"label"},
					"properties":           map[string]interface{}{"label": map[string]interface{}{"const": "ok"}},
					"additionalProperties": false,
				},
			},
		},
		"budget": map[string]interface{}{"max_model_calls": 1, "max_total_tokens": 4096},
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "usage-app", "input": "measure",
	})
	status := waitForLLMAppStatus(t, tool, fmt.Sprint(started["run_id"]), llmAppStatusCompleted)
	usage := status["usage"].(map[string]interface{})
	if int(usage["model_calls"].(float64)) != 1 || int(usage["input_tokens"].(float64)) != 7 ||
		int(usage["output_tokens"].(float64)) != 4 || usage["last_provider_request_id"] != "req-usage-1" {
		t.Fatalf("provider usage was not retained: %#v", usage)
	}
	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": fmt.Sprint(started["run_id"]), "include_outputs": true,
	})
	row := result["result"].(map[string]interface{})["results"].([]interface{})[0].(map[string]interface{})
	if row["value"].(map[string]interface{})["nested"].(map[string]interface{})["label"] != "ok" {
		t.Fatalf("structured result missing: %#v", row)
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected model call count: %d", calls.Load())
	}
}

func TestLLMAppBudgetStopsValidationRetryBeforeExtraProviderCall(t *testing.T) {
	workspace := t.TempDir()
	var calls atomic.Int32
	tool := NewToolWithRuntime(workspace, log.New(io.Discard, "", 0), Runtime{ModelCaller: func(
		context.Context, string, []map[string]interface{}, int, float64,
	) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{Content: `{}`}, nil
	}}, nil, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "bounded-retry", "workflow": "prompt", "prompt_template": "{{item}}",
		"health": map[string]interface{}{"validation_retries": 1},
		"budget": map[string]interface{}{"max_model_calls": 1},
		"output_schema": map[string]interface{}{
			"type": "object", "required": []interface{}{"ok"},
		},
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "bounded-retry", "input": "bounded",
	})
	status := waitForLLMAppStatus(t, tool, fmt.Sprint(started["run_id"]), llmAppStatusFailed)
	if calls.Load() != 1 {
		t.Fatalf("budget permitted an extra provider call: %d", calls.Load())
	}
	budget := status["budget"].(map[string]interface{})
	if budget["exhausted"] != true || !strings.Contains(fmt.Sprint(budget["reason"]), "model calls") {
		t.Fatalf("budget exhaustion was not exposed: %#v", budget)
	}
}

func TestLLMAppFullJSONSchemaRejectsNestedViolation(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"rows": map[string]interface{}{
				"type": "array", "minItems": 1,
				"items": map[string]interface{}{
					"type": "object", "required": []interface{}{"score"},
					"properties": map[string]interface{}{"score": map[string]interface{}{"type": "integer", "minimum": 0}},
				},
			},
		},
		"required": []interface{}{"rows"},
	}
	if err := validateLLMAppOutput(`{"rows":[{"score":-1}]}`, schema); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected nested schema violation, got %v", err)
	}
	if err := validateLLMAppOutput(`{"rows":[{"score":2}]}`, schema); err != nil {
		t.Fatalf("valid nested result was rejected: %v", err)
	}
}

func TestLLMAppGraphRunsSourceModelToolValidateReduceAndSink(t *testing.T) {
	workspace := t.TempDir()
	var toolCalls atomic.Int32
	runtime := Runtime{
		ModelCaller: func(context.Context, string, []map[string]interface{}, int, float64) (ModelResponse, error) {
			return ModelResponse{Content: `{"label":"ok"}`, PromptTokens: 3, CompletionTokens: 2}, nil
		},
		ToolPolicy: func(name string, arguments map[string]interface{}) (ToolPolicy, error) {
			if name != "existing_echo" {
				return ToolPolicy{}, fmt.Errorf("unexpected tool %s", name)
			}
			return ToolPolicy{ReadOnly: true}, nil
		},
		ToolExecutor: func(ctx context.Context, call ToolCall) (ToolCallResult, error) {
			toolCalls.Add(1)
			payload := call.Arguments["payload"].(map[string]interface{})
			if payload["label"] != "ok" || !strings.Contains(call.CallID, ":graph:echo") {
				return ToolCallResult{}, fmt.Errorf("unexpected tool call: %#v", call)
			}
			return ToolCallResult{Content: `{"seen":true}`, Succeeded: true}, nil
		},
	}
	tool := NewToolWithRuntime(workspace, log.New(io.Discard, "", 0), runtime, nil, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "small-graph", "workflow": "graph",
		"graph": map[string]interface{}{
			"output_node": "result",
			"nodes": []interface{}{
				map[string]interface{}{"id": "source", "type": "source", "source": "input"},
				map[string]interface{}{
					"id": "classify", "type": "model", "depends_on": []interface{}{"source"},
					"prompt_template": "Classify {{value}}", "max_tokens": 64,
					"output_schema": map[string]interface{}{
						"type": "object", "required": []interface{}{"label"},
						"properties": map[string]interface{}{"label": map[string]interface{}{"const": "ok"}},
					},
				},
				map[string]interface{}{
					"id": "echo", "type": "tool", "depends_on": []interface{}{"classify"}, "tool": "existing_echo",
					"arguments": map[string]interface{}{"payload": "{{node.classify}}"},
				},
				map[string]interface{}{
					"id": "checked", "type": "validate", "depends_on": []interface{}{"echo"},
					"output_schema": map[string]interface{}{
						"type": "object", "required": []interface{}{"seen"},
						"properties": map[string]interface{}{"seen": map[string]interface{}{"const": true}},
					},
				},
				map[string]interface{}{
					"id": "joined", "type": "reduce", "depends_on": []interface{}{"classify", "checked"}, "reducer": "collect",
				},
				map[string]interface{}{
					"id": "result", "type": "sink", "depends_on": []interface{}{"joined"},
					"output_schema": map[string]interface{}{"type": "array", "minItems": 2, "maxItems": 2},
				},
			},
		},
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "small-graph", "input": "hello",
	})
	status := waitForLLMAppStatus(t, tool, fmt.Sprint(started["run_id"]), llmAppStatusCompleted)
	progress := status["progress"].(map[string]interface{})
	if int(progress["completed"].(float64)) != 6 || toolCalls.Load() != 1 {
		t.Fatalf("graph did not execute all nodes once: status=%#v tool_calls=%d", status, toolCalls.Load())
	}
	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": fmt.Sprint(started["run_id"]), "include_outputs": true,
	})
	output := result["result"].(map[string]interface{})["output"].([]interface{})
	if len(output) != 2 {
		t.Fatalf("unexpected graph output: %#v", output)
	}
}

func TestLLMAppRecoveryUsesSettledCheckpointsWithoutRepeatingThem(t *testing.T) {
	workspace := t.TempDir()
	manager := &LLMAppManager{
		workspace: workspace, root: filepath.Join(workspace, ".openkitty", "llm_apps"),
		logger: log.New(io.Discard, "", 0), workerID: newLLMAppWorkerID(), runs: map[string]*llmAppManagedRun{},
	}
	definition, err := manager.createDefinition(LLMAppDefinition{
		Name: "recover-batch", Workflow: "batch", PromptTemplate: "{{item}}", Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := newLLMAppRunID()
	now := time.Now().Add(-time.Minute)
	plan, _ := newLLMAppRunPlan(definition, now)
	runDir := manager.runDir(runID)
	record := LLMAppRunRecord{
		RunID: runID, AppName: definition.Name, AppRevision: definition.Revision, AppDigest: definition.Digest,
		PlanRevision: plan.Revision, PlanDigest: plan.Digest, Lifecycle: llmAppLifecycleDetached,
		ExecutorBindingID: "original-agent",
		Status:            llmAppStatusRunning, Phase: "model_loop", CreatedAt: now, LastHeartbeatAt: now, LastProgressAt: now,
		Health:    LLMAppHealth{Status: llmAppHealthHealthy, EvaluatedAt: now},
		EventPath: filepath.Join(runDir, "events.jsonl"), CommandPath: filepath.Join(runDir, "commands.jsonl"),
		Budget: buildLLMAppBudgetSnapshot(definition.Budget, LLMAppUsage{}, 0, 0, ""),
	}
	request := llmAppRunRequest{Items: []interface{}{"already done", "resume me"}}
	for path, value := range map[string]interface{}{
		filepath.Join(runDir, "definition.json"): definition,
		filepath.Join(runDir, "plan.json"):       plan,
		llmAppPlanRevisionPath(runDir, 1):        plan,
		filepath.Join(runDir, "request.json"):    request,
		filepath.Join(runDir, "run.json"):        record,
	} {
		if err := writeLLMAppJSON(path, value); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := llmAppItemsFromValues(request.Items)
	settledResult := LLMAppItemResult{Index: 0, ID: items[0].ID, Output: "kept", PlanRevision: 1}
	settledState := llmAppGraphlessSettledState(items[0], plan, settledResult, now)
	pendingState := llmAppItemState{
		Item: items[1], Status: llmAppItemEffectPending, PlanRevision: 1, PlanDigest: plan.Digest,
		EffectID: runID + ":model:item_000002:0", ReplayPolicy: llmAppReplaySafe,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := writeLLMAppJSON(manager.itemStatePath(runID, items[0]), settledState); err != nil {
		t.Fatal(err)
	}
	if err := writeLLMAppJSON(manager.itemStatePath(runID, items[1]), pendingState); err != nil {
		t.Fatal(err)
	}
	var wrongCalls atomic.Int32
	manager.configureRuntime(Runtime{BindingID: "different-agent", ModelCaller: func(
		context.Context, string, []map[string]interface{}, int, float64,
	) (ModelResponse, error) {
		wrongCalls.Add(1)
		return ModelResponse{Content: "wrong runtime"}, nil
	}})
	var calls atomic.Int32
	manager.configureRuntime(Runtime{BindingID: "original-agent", ModelCaller: func(
		context.Context, string, []map[string]interface{}, int, float64,
	) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{Content: "resumed"}, nil
	}})
	run, err := manager.getRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !isTerminalLLMAppStatus(run.snapshot().Status) {
		time.Sleep(10 * time.Millisecond)
	}
	if run.snapshot().Status != llmAppStatusCompleted {
		t.Fatalf("recovered run did not complete: %#v", run.snapshot())
	}
	if calls.Load() != 1 {
		t.Fatalf("settled item was repeated during recovery; calls=%d", calls.Load())
	}
	if wrongCalls.Load() != 0 {
		t.Fatalf("recovery used another Agent's runtime; calls=%d", wrongCalls.Load())
	}
	if run.snapshot().Usage.UnknownModelCalls != 1 {
		t.Fatalf("unsettled provider effect was not accounted for: %#v", run.snapshot().Usage)
	}
	var result llmAppRunResult
	if err := readLLMAppJSON(run.snapshot().ResultPath, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Output != "kept" || result.Results[1].Output != "resumed" {
		t.Fatalf("recovery did not merge durable checkpoints: %#v", result.Results)
	}
}

func TestLLMAppNeverReplaysUnknownWriteToolEffect(t *testing.T) {
	workspace := t.TempDir()
	manager := &LLMAppManager{
		workspace: workspace, root: filepath.Join(workspace, ".openkitty", "llm_apps"),
		workerID: "worker_recovery", runs: map[string]*llmAppManagedRun{},
	}
	definition := LLMAppDefinition{
		Name: "write-graph", Workflow: llmAppWorkflowGraph, MaxTokens: 64,
		Graph: &LLMAppGraphDefinition{OutputNode: "write", Nodes: []LLMAppGraphNode{
			{ID: "source", Type: llmAppGraphSource, Source: "input"},
			{ID: "write", Type: llmAppGraphTool, DependsOn: []string{"source"}, Tool: "write_file", Arguments: map[string]interface{}{"content": "{{value}}"}},
		}},
	}
	if err := normalizeLLMAppDefinition(&definition); err != nil {
		t.Fatal(err)
	}
	runID, _ := newLLMAppRunID()
	now := time.Now()
	run := &llmAppManagedRun{
		record: LLMAppRunRecord{
			RunID: runID, Status: llmAppStatusRunning, CreatedAt: now,
			LastHeartbeatAt: now, LastProgressAt: now,
		},
		workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{}), budget: definition.Budget,
	}
	if err := os.MkdirAll(manager.runDir(runID), 0o700); err != nil {
		t.Fatal(err)
	}
	acquired, err := manager.acquireRunLease(run, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire test lease: acquired=%v err=%v", acquired, err)
	}
	sourceState := llmAppGraphNodeState{
		NodeID: "source", NodeType: llmAppGraphSource, Status: llmAppItemSettled,
		Output: "payload", CreatedAt: now, UpdatedAt: now,
	}
	writeState := llmAppGraphNodeState{
		NodeID: "write", NodeType: llmAppGraphTool, Status: llmAppItemEffectPending,
		EffectID: runID + ":graph:write", ReplayPolicy: llmAppReplayNever, CreatedAt: now, UpdatedAt: now,
	}
	if err := manager.writeGraphNodeState(run, sourceState); err != nil {
		t.Fatal(err)
	}
	if err := manager.writeGraphNodeState(run, writeState); err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	_, err = manager.executeGraph(context.Background(), run, definition, llmAppRunRequest{Input: "payload"}, Runtime{
		ModelCaller: func(context.Context, string, []map[string]interface{}, int, float64) (ModelResponse, error) {
			return ModelResponse{}, nil
		},
		ToolPolicy: func(string, map[string]interface{}) (ToolPolicy, error) {
			return ToolPolicy{ReadOnly: false}, nil
		},
		ToolExecutor: func(context.Context, ToolCall) (ToolCallResult, error) {
			executions.Add(1)
			return ToolCallResult{Succeeded: true}, nil
		},
	})
	if !errors.Is(err, ErrLLMAppEffectOutcomeUnknown) || executions.Load() != 0 {
		t.Fatalf("unknown write effect was replayed or misclassified: executions=%d err=%v", executions.Load(), err)
	}
	_ = manager.releaseRunLease(run)
}

func TestLLMAppCommandIDRejectsDifferentPayloadAfterDurableAcceptance(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	tool := newLLMAppToolForTest(workspace, func(
		ctx context.Context, _ string, _ []map[string]interface{}, _ int, _ float64,
	) (string, error) {
		select {
		case <-startedCall:
		default:
			close(startedCall)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "command-digest", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "command-digest", "input": "hold",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"command_id": "same-command", "type": "replan", "changes": map[string]interface{}{"temperature": 0.1},
		},
	})
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"command_id": "same-command", "type": "replan", "changes": map[string]interface{}{"temperature": 0.2},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "different command payload") {
		t.Fatalf("command payload mismatch was not rejected: %v", err)
	}
	executeLLMAppAction(t, tool, map[string]interface{}{"action": "stop", "run_id": runID})
}

func llmAppGraphlessSettledState(
	item llmAppWorkItem,
	plan LLMAppRunPlan,
	result LLMAppItemResult,
	now time.Time,
) llmAppItemState {
	return llmAppItemState{
		Item: item, Status: llmAppItemSettled, PlanRevision: plan.Revision, PlanDigest: plan.Digest,
		Result: &result, CreatedAt: now, UpdatedAt: now,
	}
}

func TestLLMAppLeaseFencesAnExpiredWorkerFromLatePersistence(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".openkitty", "llm_apps")
	first := &LLMAppManager{workspace: workspace, root: root, workerID: "worker_first", runs: map[string]*llmAppManagedRun{}}
	second := &LLMAppManager{workspace: workspace, root: root, workerID: "worker_second", runs: map[string]*llmAppManagedRun{}}
	runID, _ := newLLMAppRunID()
	runOne := &llmAppManagedRun{record: LLMAppRunRecord{RunID: runID, Status: llmAppStatusRunning}, workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{})}
	if err := os.MkdirAll(first.runDir(runID), 0o700); err != nil {
		t.Fatal(err)
	}
	acquired, err := first.acquireRunLease(runOne, 40*time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("first worker did not acquire lease: acquired=%v err=%v", acquired, err)
	}
	time.Sleep(60 * time.Millisecond)
	runTwo := &llmAppManagedRun{record: runOne.snapshot(), workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{})}
	acquired, err = second.acquireRunLease(runTwo, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("second worker did not take expired lease: acquired=%v err=%v", acquired, err)
	}
	runOne.mu.Lock()
	runOne.record.Status = llmAppStatusFailed
	runOne.mu.Unlock()
	if err := first.persistRunRecord(runOne); !errors.Is(err, ErrLLMAppLeaseLost) {
		t.Fatalf("late worker was not fenced: %v", err)
	}
	if err := first.releaseRunLease(runOne); !errors.Is(err, ErrLLMAppLeaseLost) {
		t.Fatalf("late worker release did not preserve successor fencing: %v", err)
	}
	var durable LLMAppRunRecord
	if err := readLLMAppJSON(filepath.Join(root, "runs", runID, "run.json"), &durable); err != nil {
		t.Fatal(err)
	}
	if durable.WorkerID != "worker_second" || durable.Status == llmAppStatusFailed {
		t.Fatalf("late worker overwrote new owner: %#v", durable)
	}
	_ = second.releaseRunLease(runTwo)
}

func TestLLMAppIdleNonOwnerRefreshesDurableTerminalState(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".openkitty", "llm_apps")
	manager := &LLMAppManager{
		workspace: workspace, root: root, workerID: "worker_reader",
		runs: map[string]*llmAppManagedRun{}, runtimes: map[string]Runtime{},
	}
	runID, _ := newLLMAppRunID()
	now := time.Now()
	staleRecord := LLMAppRunRecord{
		RunID: runID, Status: llmAppStatusRunning, Phase: "model_loop",
		CreatedAt: now, LastHeartbeatAt: now, LastProgressAt: now,
	}
	run := &llmAppManagedRun{
		record: staleRecord, workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{}),
	}
	manager.runs[runID] = run
	terminalRecord := staleRecord
	terminalRecord.Status = llmAppStatusCompleted
	terminalRecord.Phase = llmAppStatusCompleted
	terminalRecord.WorkerID = "worker_owner"
	terminalRecord.CompletedAt = &now
	if err := writeLLMAppJSON(filepath.Join(manager.runDir(runID), "run.json"), terminalRecord); err != nil {
		t.Fatal(err)
	}

	loaded, err := manager.getRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.snapshot(); got.Status != llmAppStatusCompleted || got.WorkerID != "worker_owner" {
		t.Fatalf("non-owner returned stale in-memory status: %#v", got)
	}
	if _, err := os.Stat(manager.leasePath(runID)); !os.IsNotExist(err) {
		t.Fatalf("terminal run was unexpectedly leased again: %v", err)
	}
}
