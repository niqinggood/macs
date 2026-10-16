package llmapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func executeLLMAppAction(
	t *testing.T,
	tool *LLMAppTool,
	params map[string]interface{},
) map[string]interface{} {
	t.Helper()
	output, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("llm_app %v: %v", params["action"], err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode llm_app output: %v\n%s", err, output)
	}
	return decoded
}

func waitForLLMAppStatus(
	t *testing.T,
	tool *LLMAppTool,
	runID string,
	wanted ...string,
) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	wantedSet := make(map[string]bool, len(wanted))
	for _, status := range wanted {
		wantedSet[status] = true
	}
	for time.Now().Before(deadline) {
		status := executeLLMAppAction(t, tool, map[string]interface{}{
			"action": "status", "run_id": runID,
		})
		if wantedSet[fmt.Sprint(status["status"])] {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach one of %v", runID, wanted)
	return nil
}

func createTestLLMApp(
	t *testing.T,
	tool *LLMAppTool,
	definition map[string]interface{},
) {
	t.Helper()
	response := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "create", "definition": definition,
	})
	if response["status"] != "created" {
		t.Fatalf("unexpected create response: %#v", response)
	}
}

func TestLLMAppBatchUsesOnlyApplicationPromptAndReturnsDurableResult(t *testing.T) {
	workspace := t.TempDir()
	var mu sync.Mutex
	var captured [][]map[string]interface{}
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		mu.Lock()
		copyMessages := make([]map[string]interface{}, len(messages))
		for i, message := range messages {
			copyMessages[i] = map[string]interface{}{
				"role": message["role"], "content": message["content"],
			}
		}
		captured = append(captured, copyMessages)
		mu.Unlock()
		return `{"label":"ok"}`, nil
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "row-classifier", "workflow": "batch",
		"system_prompt":   "Return a compact classification.",
		"prompt_template": "Classify item {{index}}: {{item}}",
		"concurrency":     2,
		"output_schema": map[string]interface{}{
			"type": "object", "required": []interface{}{"label"},
		},
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "row-classifier",
		"items": []interface{}{"alpha", "beta"},
	})
	runID := fmt.Sprint(started["run_id"])
	status := waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	progress, _ := status["progress"].(map[string]interface{})
	if int(progress["completed"].(float64)) != 2 || int(progress["total"].(float64)) != 2 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": runID, "include_outputs": true,
	})
	if result["result_path"] == "" {
		t.Fatalf("result path missing: %#v", result)
	}
	if _, err := os.Stat(fmt.Sprint(result["result_path"])); err != nil {
		t.Fatalf("durable result missing: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(captured))
	}
	for _, messages := range captured {
		if len(messages) != 2 || messages[0]["role"] != "system" {
			t.Fatalf("unexpected prompt messages: %#v", messages)
		}
		joined := fmt.Sprint(messages)
		if !strings.Contains(joined, "Return a compact classification") {
			t.Fatalf("application system prompt missing: %s", joined)
		}
		for _, forbidden := range []string{"Core Principles", "Reflection-Before-Action", "Available Tools"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("general-agent prompt leaked into llm_app call: %q", forbidden)
			}
		}
	}
}

func TestLLMAppStopCancelsInFlightModelCall(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	var once sync.Once
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		once.Do(func() { close(startedCall) })
		<-ctx.Done()
		return "", ctx.Err()
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "blocking-call", "workflow": "prompt",
		"prompt_template": "Process {{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "blocking-call", "input": "wait",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	stopped := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "stop", "run_id": runID,
	})
	if stopped["status"] != llmAppStatusStopped {
		t.Fatalf("expected stopped status, got %#v", stopped)
	}
	status := waitForLLMAppStatus(t, tool, runID, llmAppStatusStopped)
	if status["phase"] != llmAppStatusStopped {
		t.Fatalf("expected stopped phase, got %#v", status)
	}
}

func TestLLMAppAcceptsHealthDecisionAndStopsRun(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	var once sync.Once
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		once.Do(func() { close(startedCall) })
		<-ctx.Done()
		return "", ctx.Err()
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "health-controlled", "workflow": "prompt",
		"prompt_template": "Process {{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "health-controlled", "input": "wait",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	health := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "health", "run_id": runID,
		"decision": map[string]interface{}{
			"status": "unhealthy", "reason": "quality watchdog rejected output",
			"recommended_action": "stop", "evidence": []interface{}{"quality_gate_failed"},
		},
	})
	if health["status"] != llmAppStatusStopped {
		t.Fatalf("health stop did not stop run: %#v", health)
	}
}

func TestLLMAppPauseAndResumeStopsDispatchAtItemBoundary(t *testing.T) {
	workspace := t.TempDir()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
				return "first", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		case 2:
			close(secondStarted)
			return "second", nil
		default:
			return "extra", nil
		}
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "pausable-loop", "workflow": "batch",
		"prompt_template": "Process {{item}}", "concurrency": 1,
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "pausable-loop",
		"items": []interface{}{"first", "second"},
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first item did not start")
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "pause", "run_id": runID,
	})
	close(releaseFirst)
	waitForLLMAppStatus(t, tool, runID, llmAppStatusPaused)
	select {
	case <-secondStarted:
		t.Fatal("second item started while run was paused")
	case <-time.After(100 * time.Millisecond):
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "resume", "run_id": runID,
	})
	waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second item did not run after resume")
	}
}

func TestLLMAppPDFWorkflowProcessesPageItemsAndReportsProgress(t *testing.T) {
	workspace := t.TempDir()
	pdfPath := filepath.Join(workspace, "sample.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7\nmock"), 0o600); err != nil {
		t.Fatal(err)
	}
	extractor := func(
		ctx context.Context,
		path string,
		progress func(current, total int),
	) ([]llmAppWorkItem, error) {
		if path != pdfPath {
			return nil, fmt.Errorf("unexpected PDF path %s", path)
		}
		items := make([]llmAppWorkItem, 0, 3)
		for page := 1; page <= 3; page++ {
			progress(page, 3)
			items = append(items, llmAppWorkItem{
				Index: page - 1, ID: fmt.Sprintf("page_%06d", page),
				Text:     fmt.Sprintf("page %d text", page),
				Metadata: map[string]string{"page": fmt.Sprint(page)},
			})
		}
		return items, nil
	}
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		return fmt.Sprintf("analyzed:%s", messages[len(messages)-1]["content"]), nil
	}
	tool := newLLMAppToolForTest(workspace, caller, extractor)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "pdf-page-review", "workflow": "pdf_pages",
		"prompt_template": "Review page {{page}}: {{item}}", "concurrency": 2,
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "pdf-page-review", "file": pdfPath,
	})
	runID := fmt.Sprint(started["run_id"])
	status := waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	progress := status["progress"].(map[string]interface{})
	if int(progress["completed"].(float64)) != 3 || progress["percent"].(float64) != 100 {
		t.Fatalf("unexpected PDF progress: %#v", progress)
	}
	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": runID, "include_outputs": true,
	})
	nested, ok := result["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result payload: %#v", result)
	}
	results, _ := nested["results"].([]interface{})
	if len(results) != 3 {
		t.Fatalf("expected 3 PDF page results, got %#v", results)
	}
}

func TestLLMAppRejectsTraversalNameAndInvalidOutputSchema(t *testing.T) {
	tool := newLLMAppToolForTest(t.TempDir(), func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		return "not-json", nil
	}, nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "create",
		"definition": map[string]interface{}{
			"name": "../escape", "workflow": "prompt", "prompt_template": "{{item}}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected traversal name rejection, got %v", err)
	}

	definition := LLMAppDefinition{
		Name: "schema", Engine: "native", Workflow: "prompt", PromptTemplate: "{{item}}",
		OutputSchema: map[string]interface{}{"type": "object", "required": []string{"value"}},
		Health:       LLMAppHealthPolicy{ValidationRetries: 0}, MaxTokens: 10,
	}
	result, _, _ := processLLMAppItem(
		context.Background(), definition, "", llmAppWorkItem{ID: "one", Text: "input"},
		tool.modelCaller,
	)
	if result.Error == "" || !strings.Contains(result.Error, "invalid JSON") {
		t.Fatalf("expected schema validation error, got %#v", result)
	}
}

func TestLLMAppModelCancellationErrorIsPreserved(t *testing.T) {
	definition := LLMAppDefinition{
		Name: "cancel", Engine: "native", Workflow: "prompt", PromptTemplate: "{{item}}", MaxTokens: 10,
	}
	result, _, _ := processLLMAppItem(
		context.Background(), definition, "", llmAppWorkItem{ID: "one", Text: "input"},
		func(context.Context, string, []map[string]interface{}, int, float64) (string, error) {
			return "", context.Canceled
		},
	)
	if !strings.Contains(result.Error, context.Canceled.Error()) {
		t.Fatalf("expected cancellation error, got %#v", result)
	}
}

func TestLLMAppReplanChangesOnlyUnclaimedItemsAndScalesConcurrency(t *testing.T) {
	workspace := t.TempDir()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	newPlanCalls := make(chan struct{}, 2)
	var callNumber atomic.Int32
	var mu sync.Mutex
	var prompts []string
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		prompt := fmt.Sprint(messages[len(messages)-1]["content"])
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		if callNumber.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		} else {
			newPlanCalls <- struct{}{}
		}
		return prompt, nil
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "runtime-replan", "workflow": "batch", "concurrency": 1,
		"prompt_template": "OLD {{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "runtime-replan",
		"items": []interface{}{"first", "second", "third"},
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first item did not start")
	}
	command := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"command_id": "cmd_replan_once", "type": "replan",
			"expected_plan_revision": 1, "reason": "route remaining rows as quotations",
			"changes": map[string]interface{}{
				"prompt_template":  "NEW {{var:category}} {{item}}",
				"prompt_variables": map[string]interface{}{"category": "quotation"},
				"concurrency":      3,
			},
		},
	})
	receipt := command["command"].(map[string]interface{})
	if int(receipt["plan_revision"].(float64)) != 2 {
		t.Fatalf("unexpected command receipt: %#v", receipt)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-newPlanCalls:
		case <-time.After(2 * time.Second):
			t.Fatal("increased concurrency did not start remaining work while the first call was blocked")
		}
	}
	close(releaseFirst)
	status := waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	if int(status["plan_revision"].(float64)) != 2 {
		t.Fatalf("run did not retain revised plan: %#v", status)
	}
	runDir := filepath.Dir(fmt.Sprint(status["event_path"]))
	for _, revision := range []int{1, 2} {
		if _, err := os.Stat(llmAppPlanRevisionPath(runDir, revision)); err != nil {
			t.Fatalf("immutable plan revision %d is missing: %v", revision, err)
		}
	}

	mu.Lock()
	captured := append([]string(nil), prompts...)
	mu.Unlock()
	if len(captured) != 3 || !strings.HasPrefix(captured[0], "OLD first") {
		t.Fatalf("first claimed item did not retain plan revision 1: %#v", captured)
	}
	for _, prompt := range captured[1:] {
		if !strings.HasPrefix(prompt, "NEW quotation ") {
			t.Fatalf("unclaimed item did not use revised plan: %#v", captured)
		}
	}

	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": runID, "include_outputs": true,
	})
	rows := result["result"].(map[string]interface{})["results"].([]interface{})
	revisions := map[int]int{}
	for _, raw := range rows {
		row := raw.(map[string]interface{})
		revisions[int(row["plan_revision"].(float64))]++
	}
	if revisions[1] != 1 || revisions[2] != 2 {
		t.Fatalf("unexpected per-item plan revisions: %#v", revisions)
	}

	eventResponse := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "events", "run_id": runID, "after_seq": 0, "limit": 500,
	})
	events := eventResponse["events"].([]interface{})
	foundPlanRevision := false
	foundCommand := false
	previousSequence := 0
	for _, raw := range events {
		event := raw.(map[string]interface{})
		sequence := int(event["sequence"].(float64))
		if sequence <= previousSequence {
			t.Fatalf("event sequence is not strictly increasing: %#v", events)
		}
		previousSequence = sequence
		switch event["type"] {
		case llmAppEventPlanRevised:
			foundPlanRevision = true
		case llmAppEventCommandApplied:
			foundCommand = true
		}
	}
	if !foundPlanRevision || !foundCommand || eventResponse["terminal"] != true {
		t.Fatalf("missing durable control events: %#v", eventResponse)
	}
}

func TestLLMAppCommandEnqueuesItemsIdempotently(t *testing.T) {
	workspace := t.TempDir()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		once.Do(func() { close(firstStarted) })
		select {
		case <-releaseFirst:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return fmt.Sprint(messages[len(messages)-1]["content"]), nil
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "appendable-batch", "workflow": "batch", "concurrency": 1,
		"prompt_template": "ROW {{item}}", "max_items": 10,
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "appendable-batch", "items": []interface{}{"one"},
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial item did not start")
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "pause", "run_id": runID,
	})
	params := map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"command_id": "cmd_enqueue_once", "type": "enqueue_items",
			"reason": "new rows arrived", "items": []interface{}{"two", "three"},
		},
	}
	firstReceipt := executeLLMAppAction(t, tool, params)
	secondReceipt := executeLLMAppAction(t, tool, params)
	if fmt.Sprint(firstReceipt["command"].(map[string]interface{})["applied_at"]) !=
		fmt.Sprint(secondReceipt["command"].(map[string]interface{})["applied_at"]) {
		t.Fatalf("duplicate command did not return its original receipt")
	}
	close(releaseFirst)
	waitForLLMAppStatus(t, tool, runID, llmAppStatusPaused)
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "resume", "run_id": runID,
	})
	status := waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	progress := status["progress"].(map[string]interface{})
	if int(progress["completed"].(float64)) != 3 || int(progress["total"].(float64)) != 3 {
		t.Fatalf("enqueued items were not included exactly once: %#v", progress)
	}
	result := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "result", "run_id": runID, "include_outputs": true,
	})
	rows := result["result"].(map[string]interface{})["results"].([]interface{})
	if len(rows) != 3 {
		t.Fatalf("expected three durable results, got %#v", rows)
	}
	commandPath := fmt.Sprint(status["command_path"])
	data, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 1 {
		t.Fatalf("idempotent command was persisted %d times: %s", lines, data)
	}
}

func TestLLMAppCommandRejectsRevisionConflictAndUnknownChanges(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		select {
		case <-startedCall:
		default:
			close(startedCall)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "revision-guard", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "revision-guard", "input": "hold",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"type": "replan", "expected_plan_revision": 99,
			"changes": map[string]interface{}{"temperature": 0.4},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("expected optimistic revision conflict, got %v", err)
	}
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"type": "replan", "changes": map[string]interface{}{"arbitrary_tool_access": true},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict command schema rejection, got %v", err)
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "stop", "run_id": runID,
	})
}

func TestLLMAppPDFReplanDuringExtractionAppliesAtModelBoundary(t *testing.T) {
	workspace := t.TempDir()
	pdfPath := filepath.Join(workspace, "routing.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7\nmock"), 0o600); err != nil {
		t.Fatal(err)
	}
	extractorStarted := make(chan struct{})
	releaseExtractor := make(chan struct{})
	extractor := func(
		ctx context.Context,
		path string,
		progress func(current, total int),
	) ([]llmAppWorkItem, error) {
		close(extractorStarted)
		select {
		case <-releaseExtractor:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		progress(1, 2)
		progress(2, 2)
		return []llmAppWorkItem{
			{Index: 0, ID: "page_000001", Text: "quote page", Metadata: map[string]string{"page": "1"}},
			{Index: 1, ID: "page_000002", Text: "technical page", Metadata: map[string]string{"page": "2"}},
		}, nil
	}
	var mu sync.Mutex
	var prompts []string
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		prompt := fmt.Sprint(messages[len(messages)-1]["content"])
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		return prompt, nil
	}
	tool := newLLMAppToolForTest(workspace, caller, extractor)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "pdf-routing-replan", "workflow": "pdf_pages",
		"prompt_template": "OLD page {{page}} {{item}}", "concurrency": 2,
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "pdf-routing-replan", "file": pdfPath,
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-extractorStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("PDF extraction did not start")
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"type": "replan", "expected_plan_revision": 1,
			"reason": "PDF was identified as quotation material",
			"changes": map[string]interface{}{
				"prompt_template": "ROUTED quotation page {{page}} {{item}}",
			},
		},
	})
	close(releaseExtractor)
	waitForLLMAppStatus(t, tool, runID, llmAppStatusCompleted)
	mu.Lock()
	captured := append([]string(nil), prompts...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected two PDF model calls, got %#v", captured)
	}
	for _, prompt := range captured {
		if !strings.HasPrefix(prompt, "ROUTED quotation page") {
			t.Fatalf("PDF page crossed the model boundary with the stale plan: %#v", captured)
		}
	}
}

func TestLLMAppEventsLongPollWakesOnReplan(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		select {
		case <-startedCall:
		default:
			close(startedCall)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "event-waiter", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "event-waiter", "input": "hold",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	status := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "status", "run_id": runID,
	})
	after := int(status["event_sequence"].(float64))
	type eventResult struct {
		output string
		err    error
	}
	waited := make(chan eventResult, 1)
	go func() {
		output, err := tool.Execute(context.Background(), map[string]interface{}{
			"action": "events", "run_id": runID,
			"after_seq": after, "wait_seconds": 5,
		})
		waited <- eventResult{output: output, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "command", "run_id": runID,
		"command": map[string]interface{}{
			"type": "replan", "changes": map[string]interface{}{"temperature": 0.2},
		},
	})
	select {
	case result := <-waited:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !strings.Contains(result.output, llmAppEventPlanRevised) {
			t.Fatalf("long poll did not return the replan event: %s", result.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event long poll did not wake after replan")
	}
	executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "stop", "run_id": runID,
	})
}

func TestLLMAppTerminalEventAndStatusAreCommittedTogether(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	releaseCall := make(chan struct{})
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		close(startedCall)
		select {
		case <-releaseCall:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	tool := newLLMAppToolForTest(workspace, caller, nil)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "terminal-event", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	started := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "start", "name": "terminal-event", "input": "finish",
	})
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	status := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "status", "run_id": runID,
	})
	after := int(status["event_sequence"].(float64))
	close(releaseCall)
	events := executeLLMAppAction(t, tool, map[string]interface{}{
		"action": "events", "run_id": runID, "after_seq": after, "wait_seconds": 5,
		"event_types": []interface{}{llmAppEventRunCompleted},
	})
	if events["terminal"] != true || events["status"] != llmAppStatusCompleted {
		t.Fatalf("terminal event became visible before terminal status was committed: %#v", events)
	}
	if !strings.Contains(fmt.Sprint(events["events"]), llmAppEventRunCompleted) {
		t.Fatalf("terminal event missing from terminal snapshot: %#v", events)
	}
}

func TestLLMAppCommandsAndEventsRespectRunOwnership(t *testing.T) {
	workspace := t.TempDir()
	startedCall := make(chan struct{})
	caller := func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (string, error) {
		select {
		case <-startedCall:
		default:
			close(startedCall)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	type identityKey struct{}
	withIdentity := func(ctx context.Context, identity Identity) context.Context {
		return context.WithValue(ctx, identityKey{}, identity)
	}
	resolveIdentity := func(ctx context.Context) (Identity, bool) {
		identity, ok := ctx.Value(identityKey{}).(Identity)
		return identity, ok
	}
	tool := NewTool(workspace, log.New(os.Stderr, "[llm_app_test] ", 0), caller, nil, resolveIdentity)
	createTestLLMApp(t, tool, map[string]interface{}{
		"name": "owned-run", "workflow": "prompt", "prompt_template": "{{item}}",
	})
	ownerCtx := withIdentity(context.Background(), Identity{
		TenantID: "tenant-a", SubjectID: "alice",
	})
	startOutput, err := tool.Execute(ownerCtx, map[string]interface{}{
		"action": "start", "name": "owned-run", "input": "hold",
	})
	if err != nil {
		t.Fatal(err)
	}
	var started map[string]interface{}
	if err := json.Unmarshal([]byte(startOutput), &started); err != nil {
		t.Fatal(err)
	}
	runID := fmt.Sprint(started["run_id"])
	select {
	case <-startedCall:
	case <-time.After(2 * time.Second):
		t.Fatal("model call did not start")
	}
	peerCtx := withIdentity(context.Background(), Identity{
		TenantID: "tenant-a", SubjectID: "bob",
	})
	for _, action := range []string{"events", "command"} {
		params := map[string]interface{}{"action": action, "run_id": runID}
		if action == "command" {
			params["command"] = map[string]interface{}{
				"type": "replan", "changes": map[string]interface{}{"temperature": 0.1},
			}
		}
		if _, err := tool.Execute(peerCtx, params); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("cross-subject %s should be hidden, got %v", action, err)
		}
	}
	controlCtx := withIdentity(context.Background(), Identity{ControlPlane: true})
	if _, err := tool.Execute(controlCtx, map[string]interface{}{
		"action": "events", "run_id": runID,
	}); err != nil {
		t.Fatalf("control plane could not inspect events: %v", err)
	}
	if _, err := tool.Execute(ownerCtx, map[string]interface{}{
		"action": "stop", "run_id": runID,
	}); err != nil {
		t.Fatal(err)
	}
}
