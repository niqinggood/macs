package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"mac/internal/agentresult"
)

type scriptedLLMHTTPResponse struct {
	statusCode  int
	contentType string
	body        string
}

type scriptedLLMTransport struct {
	mu        sync.Mutex
	responses []scriptedLLMHTTPResponse
	requests  [][]byte
}

func (t *scriptedLLMTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests = append(t.requests, append([]byte(nil), body...))
	index := len(t.requests) - 1
	response := t.responses[len(t.responses)-1]
	if index < len(t.responses) {
		response = t.responses[index]
	}
	header := make(http.Header)
	header.Set("Content-Type", response.contentType)
	statusCode := response.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    req,
	}, nil
}

func (t *scriptedLLMTransport) requestCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.requests)
}

func (t *scriptedLLMTransport) requestContains(text string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, request := range t.requests {
		if bytes.Contains(request, []byte(text)) {
			return true
		}
	}
	return false
}

func completionJSON(t *testing.T, finishReason, content string) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": finishReason,
			"message": map[string]interface{}{
				"content": content,
			},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func completionSSE(t *testing.T, finishReason, content string) string {
	t.Helper()
	contentChunk, err := json.Marshal(map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta":         map[string]interface{}{"content": content},
			"finish_reason": nil,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	finishChunk, err := json.Marshal(map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta":         map[string]interface{}{},
			"finish_reason": finishReason,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(contentChunk) + "\n\n" +
		"data: " + string(finishChunk) + "\n\n" +
		"data: [DONE]\n\n"
}

type incompleteResponseTestCallback struct{}

func (incompleteResponseTestCallback) OnAgentThinking(string, string, string)               {}
func (incompleteResponseTestCallback) OnAgentAction(string, string, map[string]interface{}) {}
func (incompleteResponseTestCallback) OnAgentError(string, error, string)                   {}
func (incompleteResponseTestCallback) OnLLMCall(string, string, string, string, int)        {}

type terminalProgressCapture struct {
	mu    sync.Mutex
	types []string
}

func (c *terminalProgressCapture) OnAgentThinking(_ string, _ string, thoughtType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.types = append(c.types, thoughtType)
}

func (*terminalProgressCapture) OnAgentAction(string, string, map[string]interface{}) {}
func (*terminalProgressCapture) OnAgentError(string, error, string)                   {}
func (*terminalProgressCapture) OnLLMCall(string, string, string, string, int)        {}

func (c *terminalProgressCapture) saw(thoughtType string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, got := range c.types {
		if got == thoughtType {
			return true
		}
	}
	return false
}

func newIncompleteResponseTestAgent(t *testing.T, transport http.RoundTripper, streaming bool, maxIterations int) *LLMAgent {
	t.Helper()
	workspace := t.TempDir()
	config := &Config{
		Workspace: workspace,
		Runtime: RuntimeConfig{
			LLMCallTimeoutSeconds: 15,
			LLMMaxRetries:         1,
			CompressMsgThreshold:  1000,
			MaxContextChars:       1 << 28,
		},
	}
	callback := AgentCallback(nil)
	if streaming {
		callback = incompleteResponseTestCallback{}
	}
	return &LLMAgent{
		name:          "incomplete-response-test",
		modelConfig:   &ModelConfig{Type: "openai", APIKey: "test-api-key-long-enough", ModelName: "test-model", BaseURL: "http://unit.test/v1", MaxTokens: 512},
		temperature:   0,
		maxTokens:     512,
		httpClient:    &http.Client{Transport: transport},
		callback:      callback,
		toolRegistry:  nil,
		maxIterations: maxIterations,
		workspace:     workspace,
		fileLogger:    log.New(io.Discard, "", 0),
		globalConfig:  config,
		useStreaming:  streaming,
	}
}

func TestIncompleteFinishReasonClassification(t *testing.T) {
	for _, reason := range []string{"length", "abort", "ABORTED", "incomplete", "max_tokens", "max_output_tokens", "token_limit", "response_incomplete"} {
		if !isIncompleteLLMFinishReason(reason) {
			t.Errorf("finish_reason %q should be incomplete", reason)
		}
	}
	for _, reason := range []string{"stop", "tool_calls", "content_filter", ""} {
		if isIncompleteLLMFinishReason(reason) {
			t.Errorf("finish_reason %q should not use the incomplete-output path", reason)
		}
	}
}

func TestPipelineStepSuppressesProvisionalAgentTerminalProgress(t *testing.T) {
	response := completionJSON(t, "stop", "内容重构已经完成，逐页标题、核心观点与验收口径均已整理。")
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        response,
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 2)
	capture := &terminalProgressCapture{}
	agent.callback = capture

	ctx := context.WithValue(context.Background(), "pipeline_step_execution", true)
	if _, err := agent.Generate(ctx, "完成当前流水线步骤"); err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if capture.saw("completion") || capture.saw("completion_failed") {
		t.Fatalf("pipeline sub-agent leaked a provisional terminal event: %#v", capture.types)
	}

	transport = &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        response,
	}}}
	agent = newIncompleteResponseTestAgent(t, transport, false, 2)
	capture = &terminalProgressCapture{}
	agent.callback = capture
	if _, err := agent.Generate(context.Background(), "完成普通用户任务"); err != nil {
		t.Fatalf("ordinary Generate error: %v", err)
	}
	if !capture.saw("completion") {
		t.Fatalf("ordinary task lost its terminal completion event: %#v", capture.types)
	}
}

func TestIncompletePresentationRecoveryPreservesAudienceFacingRoute(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	prompt := buildIncompleteLLMResponseRepairPromptForContext(ctx, "length")
	content, _ := prompt["content"].(string)
	for _, want := range []string{
		"Presentation-specific recovery",
		"every substantive audience-facing deck, regardless of page count",
		"register one page-specific renderer for every slide",
		"run the scaffold first",
		"task-local deck_spec.json",
		"read the task builder once",
		"standalone compact JSON",
		"explicitly simple/fast/internal/recurring/fixed-schema",
		"do not call it or another presentation Skill again",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("presentation completion repair missing %q: %s", want, content)
		}
	}
	for _, forbidden := range []string{"Do not retry a large Python", "call plan_task"} {
		if !strings.Contains(content, forbidden) {
			t.Fatalf("presentation completion repair missing prohibition %q: %s", forbidden, content)
		}
	}
}

func TestIncompletePresentationRecoveryContinuesExistingScaffold(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.Scaffolded = true
	state.BuilderPath = ".openkitty/storage/ppt_work_run/build_deck.py"
	state.mu.Unlock()

	prompt := buildIncompleteLLMResponseRepairPromptForContext(ctx, "length")
	content, _ := prompt["content"].(string)
	for _, want := range []string{
		"task-local scaffolded per-deck builder already exists",
		"sibling deck_spec.json",
		"needs_customization=false",
		"Do not inspect pptx_design.py",
		"bounded anchored edits",
		"TASK PAGE COMPOSITIONS",
		state.BuilderPath,
		".openkitty/storage/ppt_work_run/deck_spec.json",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("scaffold recovery missing %q: %s", want, content)
		}
	}
}

func TestGenerateRewritesIncompleteNonStreamingResponseOnce(t *testing.T) {
	for _, reason := range []string{"abort", "length", "incomplete"} {
		t.Run(reason, func(t *testing.T) {
			transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
				{contentType: "application/json", body: completionJSON(t, reason, "partial answer that must not escape")},
				{contentType: "application/json", body: completionJSON(t, "stop", "完整且简洁的最终回答。")},
			}}
			agent := newIncompleteResponseTestAgent(t, transport, false, 3)

			output, err := agent.Generate(context.Background(), "请给出完整回答")
			if err != nil {
				t.Fatalf("Generate error: %v", err)
			}
			if output != "完整且简洁的最终回答。" {
				t.Fatalf("Generate output = %q", output)
			}
			if transport.requestCount() != 2 {
				t.Fatalf("request count = %d, want 2", transport.requestCount())
			}
			if !transport.requestContains("OUTPUT COMPLETION REPAIR") {
				t.Fatal("concise completion repair prompt was not sent")
			}
			if strings.Contains(output, "partial answer") {
				t.Fatalf("partial response leaked into final output: %q", output)
			}
		})
	}
}

func TestGenerateRepairsIncompleteResponseAfterIterationBudgetIsExhausted(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: completionJSON(t, "length", "partial final stage answer")},
		{contentType: "application/json", body: completionJSON(t, "stop", "complete compact stage answer")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 1)

	output, err := agent.Generate(context.Background(), "answer this bounded pipeline stage")
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if output != "complete compact stage answer" {
		t.Fatalf("Generate output = %q", output)
	}
	if transport.requestCount() != 2 {
		t.Fatalf("request count = %d, want one response plus one completion repair", transport.requestCount())
	}
	if !transport.requestContains("OUTPUT COMPLETION REPAIR") {
		t.Fatal("exhausted-budget completion repair prompt was not sent")
	}
}

func TestGenerateRejectsSecondIncompleteResponseWithoutSuccess(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: completionJSON(t, "abort", "first partial")},
		{contentType: "application/json", body: completionJSON(t, "length", "second partial")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 3)

	output, err := agent.Generate(context.Background(), "请给出完整回答")
	if err == nil {
		t.Fatal("Generate reported success after a second incomplete response")
	}
	if _, ok := agentresult.AsExecutionError(err); !ok {
		t.Fatalf("Generate error = %T %v, want AgentExecutionError", err, err)
	}
	if !strings.Contains(output, "未能生成可用最终回答") || !strings.Contains(output, "finish_reason=length") {
		t.Fatalf("missing deterministic incomplete-response diagnostic: %q", output)
	}
	if strings.Contains(output, "first partial") || strings.Contains(output, "second partial") {
		t.Fatalf("partial provider output leaked into failure result: %q", output)
	}
	if transport.requestCount() != 2 {
		t.Fatalf("request count = %d, want exactly 2", transport.requestCount())
	}
}

func TestGenerateRewritesIncompleteStreamingResponseOnce(t *testing.T) {
	for _, reason := range []string{"abort", "length"} {
		t.Run(reason, func(t *testing.T) {
			transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
				{contentType: "text/event-stream", body: completionSSE(t, reason, "stream partial")},
				{contentType: "text/event-stream", body: completionSSE(t, "stop", "流式完整最终回答。")},
			}}
			agent := newIncompleteResponseTestAgent(t, transport, true, 3)

			output, err := agent.Generate(context.Background(), "请给出流式完整回答")
			if err != nil {
				t.Fatalf("Generate error: %v", err)
			}
			if output != "流式完整最终回答。" {
				t.Fatalf("Generate output = %q", output)
			}
			if transport.requestCount() != 2 || !transport.requestContains("OUTPUT COMPLETION REPAIR") {
				t.Fatalf("stream repair requests=%d marker=%v", transport.requestCount(), transport.requestContains("OUTPUT COMPLETION REPAIR"))
			}
		})
	}
}

func TestIncompleteStreamingResponseSkipsToolCollectorWait(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "text/event-stream", body: completionSSE(t, "abort", "stream partial"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, true, 2)
	agent.toolRegistry = NewToolRegistry()

	started := time.Now()
	response, err := agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "answer"}}, nil, nil)
	if err != nil {
		t.Fatalf("callLLMStream: %v", err)
	}
	if response == nil || response.FinishReason != "abort" || len(response.ToolCalls) != 0 {
		t.Fatalf("unexpected incomplete stream response: %+v", response)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("incomplete stream waited %v for an unauthorized tool collector", elapsed)
	}
}

func TestGraceCompletionHelperAllowsOnlyOneConciseRewrite(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: completionJSON(t, "abort", "partial grace")},
		{contentType: "application/json", body: completionJSON(t, "stop", "complete grace")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 1)
	attempts := 0
	response, err := agent.callLLMWithBoundedCompletionRepair(context.Background(), []map[string]interface{}{{"role": "user", "content": "summarize"}}, &attempts, "grace summary")
	if err != nil || response == nil || response.Content != "complete grace" {
		t.Fatalf("grace repair response=%+v err=%v", response, err)
	}
	if attempts != 1 || transport.requestCount() != 2 {
		t.Fatalf("attempts/requests = %d/%d, want 1/2", attempts, transport.requestCount())
	}

	secondTransport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json", body: completionJSON(t, "abort", "another partial"),
	}}}
	agent.httpClient = &http.Client{Transport: secondTransport}
	if _, err := agent.callLLMWithBoundedCompletionRepair(context.Background(), []map[string]interface{}{{"role": "user", "content": "summarize again"}}, &attempts, "grace synthesis"); err == nil {
		t.Fatal("shared grace repair budget allowed a second rewrite")
	}
	if secondTransport.requestCount() != 1 {
		t.Fatalf("second grace request count = %d, want 1 without another rewrite", secondTransport.requestCount())
	}
}
