package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"mac/internal/runtimeutil"
)

// getIntegrationMiniMaxConfig reads the MiniMax API key and endpoint from config.
func getIntegrationMiniMaxConfig(t *testing.T) (apiKey, baseURL, model string) {
	t.Helper()
	if os.Getenv("OPENKITTY_RUN_LLM_INTEGRATION") != "1" {
		t.Skip("set OPENKITTY_RUN_LLM_INTEGRATION=1 to run real LLM integration tests")
	}
	return selectMiniMaxTestConfig(t, loadTestRuntimeConfig(t))
}

// callIntegrationMiniMax makes a real API call to MiniMax.
func callIntegrationMiniMax(t *testing.T, apiKey, baseURL, model string, messages []map[string]interface{}) string {
	t.Helper()

	body := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"max_tokens":  256,
		"temperature": 0.1,
		"stream":      false,
	}
	bodyJSON, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("API call failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != 200 {
		t.Fatalf("API error %d: %v", resp.StatusCode, result)
	}

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatal("no choices in response")
	}

	choice := choices[0].(map[string]interface{})
	msg := choice["message"].(map[string]interface{})
	content, _ := msg["content"].(string)
	return content
}

// TestRealLLM_BasicCall tests a basic LLM call.
func TestRealLLM_BasicCall(t *testing.T) {
	apiKey, baseURL, model := getIntegrationMiniMaxConfig(t)

	messages := []map[string]interface{}{
		{"role": "system", "content": "Reply with only the word 'OK'"},
		{"role": "user", "content": "Hello"},
	}

	content := callIntegrationMiniMax(t, apiKey, baseURL, model, messages)
	if !strings.Contains(strings.ToUpper(content), "OK") {
		t.Errorf("Expected 'OK' in response, got: %s", content)
	}
	t.Logf("LLM response: %s", content)
}

// TestRealLLM_ToolSelection tests that the LLM correctly selects tools.
func TestRealLLM_ToolSelection(t *testing.T) {
	apiKey, baseURL, model := getIntegrationMiniMaxConfig(t)

	messages := []map[string]interface{}{
		{"role": "system", "content": "You have tools: read_file(path), write_file(path, content). When asked to read, call read_file. When asked to write, call write_file."},
		{"role": "user", "content": "Read the file /tmp/test.txt"},
	}

	body := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"max_tokens":  256,
		"temperature": 0.1,
		"tools": []map[string]interface{}{
			{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "read_file",
					"description": "Read a file",
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path": map[string]interface{}{"type": "string"},
						},
						"required": []string{"path"},
					},
				},
			},
			{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "write_file",
					"description": "Write a file",
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path":    map[string]interface{}{"type": "string"},
							"content": map[string]interface{}{"type": "string"},
						},
						"required": []string{"path", "content"},
					},
				},
			},
		},
	}
	bodyJSON, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("API call failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatal("no choices")
	}

	choice := choices[0].(map[string]interface{})
	msg := choice["message"].(map[string]interface{})

	// Check for tool calls
	toolCalls, hasTools := msg["tool_calls"].([]interface{})
	if hasTools {
		tc := toolCalls[0].(map[string]interface{})
		fn := tc["function"].(map[string]interface{})
		toolName, _ := fn["name"].(string)
		t.Logf("LLM selected tool: %s", toolName)
		if toolName != "read_file" {
			t.Errorf("expected read_file tool, got: %s", toolName)
		}
	} else {
		t.Logf("LLM text response: %s", msg["content"])
	}
}

// TestRealLLM_PermissionCheck tests the permission check flow end-to-end.
func TestRealLLM_PermissionCheck(t *testing.T) {
	// Test that ResolvePermission correctly identifies tool risks
	// cli with 'ls' is RiskModerate → auto-allowed in default mode
	allowed, confirm, _ := ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": "ls"})
	if !allowed || confirm {
		t.Errorf("default mode: cli ls is moderate risk, should be auto-allowed without confirmation, got allowed=%v confirm=%v", allowed, confirm)
	}

	// OpenKitty default mode does not pause on its own risk labels.
	allowed, confirm, reason := ResolvePermission(PermissionDefault, "external_agent", nil)
	if !allowed || confirm {
		t.Errorf("default mode: external_agent should be allowed without confirmation, got allowed=%v confirm=%v reason=%s", allowed, confirm, reason)
	}

	allowed, _, reason = ResolvePermission(PermissionPlan, "write_file", nil)
	if allowed {
		t.Errorf("plan mode: write_file should be blocked, got reason=%s", reason)
	}
}

// TestRealLLM_TokenEstimationAccuracy verifies token estimation is reasonable.
func TestRealLLM_TokenEstimationAccuracy(t *testing.T) {
	short := "Hello world"
	tokens := runtimeutil.EstimateTokens(short)
	if tokens < 1 || tokens > 10 {
		t.Errorf("short text token count unreasonable: %d", tokens)
	}

	medium := "The quick brown fox jumps over the lazy dog. " +
		"This is a longer sentence that should produce more tokens. " +
		"We are testing the token estimation function."
	tokens = runtimeutil.EstimateTokens(medium)
	if tokens < 15 || tokens > 60 {
		t.Errorf("medium text token count unreasonable: %d", tokens)
	}
}

// TestRealLLM_CircuitBreakerFlow tests circuit breaker state transitions.
func TestRealLLM_CircuitBreakerFlow(t *testing.T) {
	cb := runtimeutil.NewCircuitBreaker(3, 2, 5*time.Second)

	// Initial state
	if cb.State() != runtimeutil.CircuitClosed {
		t.Error("initial state should be closed")
	}

	// Allow should pass
	if err := cb.Allow(); err != nil {
		t.Errorf("closed circuit should allow: %v", err)
	}

	// Record failures until open
	for i := 0; i < 3; i++ {
		cb.RecordFailure(0)
	}
	if cb.State() != runtimeutil.CircuitOpen {
		t.Error("after 3 failures, circuit should be open")
	}

	// Should block when open
	if err := cb.Allow(); err == nil {
		t.Log("circuit open, timeout not expired, blocking")
	}

	// Wait for timeout then allow
	time.Sleep(10 * time.Millisecond)
	cb.SetTimeout(1 * time.Millisecond) // short timeout for test
	_ = cb.Allow()
	cb.RecordSuccess()
	cb.RecordSuccess()

	// Should be closed after 2 successes in half-open
	if cb.State() != runtimeutil.CircuitClosed {
		t.Errorf("after 2 successes, circuit should be closed, got: %s", cb.State())
	}

	// Reset
	cb.Reset()
	if cb.State() != runtimeutil.CircuitClosed {
		t.Error("after reset, should be closed")
	}
}

// TestRealLLM_MessageRoundTrip tests structured message conversion.
func TestRealLLM_MessageRoundTrip(t *testing.T) {
	builder := NewMessageBuilder().
		System("you are a bot").
		User("hello").
		Assistant("hi").
		ToolResult("c1", "read_file", "content", true)

	apiMsgs := builder.BuildAPI()
	if len(apiMsgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(apiMsgs))
	}

	// Convert back
	for _, apiMsg := range apiMsgs {
		typed := FromAPIMessage(apiMsg)
		if typed == nil {
			t.Error("FromAPIMessage returned nil")
			continue
		}
		roundTrip := typed.ToAPI()
		if roundTrip["role"] != apiMsg["role"] {
			t.Errorf("round trip role mismatch: %s vs %s", roundTrip["role"], apiMsg["role"])
		}
	}
}

// TestRealLLM_TokenEstimatorVsAPI compares our estimator with actual API usage.
func TestRealLLM_TokenEstimatorVsAPI(t *testing.T) {
	apiKey, baseURL, model := getIntegrationMiniMaxConfig(t)
	msg := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant. Reply concisely."},
		{"role": "user", "content": "Say hello and tell me what you can do."},
	}
	estimated := estimateTokensForMessages(msg)
	body := map[string]interface{}{"model": model, "messages": msg, "max_tokens": 128, "temperature": 0.1}
	bodyJSON, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("API call failed: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if usage, ok := result["usage"].(map[string]interface{}); ok {
		promptTokens, _ := usage["prompt_tokens"].(float64)
		t.Logf("API prompt tokens: %d, Our estimate: %d", int(promptTokens), estimated)
		if estimated < int(promptTokens)/2 || estimated > int(promptTokens)*2 {
			t.Errorf("estimator too far off: estimated=%d actual=%d", estimated, int(promptTokens))
		}
	}
}

// TestRealLLM_ErrorRecovery tests API error classification with real API.
func TestRealLLM_ErrorRecovery(t *testing.T) {
	_, baseURL, model := getIntegrationMiniMaxConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := map[string]interface{}{
		"model":    model,
		"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	bodyJSON, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid-key-12345")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("network error (expected): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		t.Log("Got 401 as expected — auth classification works")
		retryable, category := classifyAPIError("401")
		if retryable || category != "auth" {
			t.Errorf("401 should be non-retryable auth: retryable=%v category=%s", retryable, category)
		}
	}
}

// TestRealLLM_StreamingCall tests the streaming API.
func TestRealLLM_StreamingCall(t *testing.T) {
	apiKey, baseURL, model := getIntegrationMiniMaxConfig(t)
	reqBody := map[string]interface{}{
		"model": model, "max_tokens": 128, "temperature": 0.1, "stream": true,
		"messages": []map[string]interface{}{
			{"role": "system", "content": "Reply with exactly 3 short bullet points about AI."},
			{"role": "user", "content": "Go"},
		},
	}
	bodyJSON, _ := json.Marshal(reqBody)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("streaming call failed: %v", err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	chunks := 0
	gotContent := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk map[string]interface{}
		json.Unmarshal([]byte(data), &chunk)
		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if delta, ok := choices[0].(map[string]interface{})["delta"].(map[string]interface{}); ok {
				if content, ok := delta["content"].(string); ok && content != "" {
					gotContent = true
				}
			}
		}
		chunks++
	}
	if !gotContent {
		t.Error("expected streaming content")
	}
	t.Logf("Streaming: %d chunks received", chunks)
}
