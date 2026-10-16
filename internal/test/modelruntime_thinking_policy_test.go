package app

import (
	"context"
	"net/http"
	"testing"
)

const adaptiveReasoningProtocolErrorBody = `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`

func TestThinkingPolicyDefaultsToSafeOff(t *testing.T) {
	tests := []struct {
		name string
		cfg  ModelConfig
		want string
	}{
		{name: "MiniMax M3 defaults off", cfg: ModelConfig{ModelName: "MiniMax-M3"}, want: llmThinkingModeDisabled},
		{name: "DeepSeek V4 Pro defaults off", cfg: ModelConfig{ModelName: "deepseek-v4-pro"}, want: llmThinkingModeDisabled},
		{name: "ordinary chat model omits unsupported field", cfg: ModelConfig{ModelName: "deepseek-chat"}},
		{name: "reasoning name no longer auto enables", cfg: ModelConfig{ModelName: "deepseek-reasoner"}},
		{name: "explicit disabled", cfg: ModelConfig{ModelName: "custom-model", ThinkingMode: "disabled"}, want: llmThinkingModeDisabled},
		{name: "explicit enabled", cfg: ModelConfig{ModelName: "custom-model", ThinkingMode: "enabled"}, want: llmThinkingModeEnabled},
		{name: "legacy auto remains opt in", cfg: ModelConfig{ModelName: "deepseek-reasoner", ThinkingMode: "auto"}, want: llmThinkingModeEnabled},
		{name: "legacy auto omits field for ordinary model", cfg: ModelConfig{ModelName: "deepseek-chat", ThinkingMode: "auto"}},
		{name: "unknown mode fails closed", cfg: ModelConfig{ModelName: "deepseek-reasoner", ThinkingMode: "enable"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := effectiveLLMThinkingMode(context.Background(), test.cfg, nil); got != test.want {
				t.Fatalf("thinking mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestThinkingPolicyDowngradesIncompleteReasoningReplay(t *testing.T) {
	reasoningToolTurn := map[string]interface{}{
		"role":              "assistant",
		"reasoning_content": "provider reasoning",
		"tool_calls": []interface{}{map[string]interface{}{
			"id": "call_1",
		}},
	}
	missingReasoningToolTurn := map[string]interface{}{
		"role": "assistant",
		"tool_calls": []interface{}{map[string]interface{}{
			"id": "call_2",
		}},
	}

	if got := effectiveLLMThinkingMode(context.Background(), ModelConfig{ModelName: "future-provider-alias"}, []map[string]interface{}{
		reasoningToolTurn,
		missingReasoningToolTurn,
	}); got != llmThinkingModeDisabled {
		t.Fatalf("mixed reasoning history mode = %q, want disabled", got)
	}

	if got := effectiveLLMThinkingMode(context.Background(), ModelConfig{ModelName: "ordinary-chat"}, []map[string]interface{}{
		missingReasoningToolTurn,
	}); got != "" {
		t.Fatalf("ordinary non-thinking tool history gained mode %q", got)
	}

	if got := effectiveLLMThinkingMode(context.Background(), ModelConfig{ModelName: "custom-reasoner", ThinkingMode: "enabled"}, []map[string]interface{}{
		missingReasoningToolTurn,
	}); got != llmThinkingModeDisabled {
		t.Fatalf("explicit thinking with a replay gap = %q, want disabled", got)
	}
}

func TestStreamingAndNonStreamingUseSameSafeOffPolicy(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "non-streaming"
		contentType := "application/json"
		body := completionJSON(t, "stop", "ok")
		if streaming {
			name = "streaming"
			contentType = "text/event-stream"
			body = completionSSE(t, "stop", "ok")
		}
		t.Run(name, func(t *testing.T) {
			transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
				contentType: contentType,
				body:        body,
			}}}
			agent := newIncompleteResponseTestAgent(t, transport, streaming, 2)
			agent.modelConfig.ModelName = "deepseek-v4-pro"

			var err error
			if streaming {
				_, err = agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "test"}}, nil, nil)
			} else {
				_, err = agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "test"}})
			}
			if err != nil {
				t.Fatal(err)
			}

			request := capturedLLMRequest(t, transport, 0)
			thinking, _ := request["thinking"].(map[string]interface{})
			if got, _ := thinking["type"].(string); got != llmThinkingModeDisabled {
				t.Fatalf("thinking.type = %q, want disabled; request=%v", got, request)
			}
		})
	}
}

func TestProviderModelCarriesThinkingModeIntoAgentConfig(t *testing.T) {
	provider := &ModelProvider{Type: "deepseek", Models: []ProviderModel{{
		Name:         "deepseek-v4-pro",
		Capability:   "text-to-text",
		ThinkingMode: "disabled",
	}}}
	cfg := provider.ToModelConfig(&provider.Models[0])
	if cfg.ThinkingMode != llmThinkingModeDisabled {
		t.Fatalf("thinking mode = %q, want disabled", cfg.ThinkingMode)
	}
}

func TestNonStreamingReasoningErrorLearnsGlobalSafeModeAndRetries(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{statusCode: http.StatusBadRequest, contentType: "application/json", body: adaptiveReasoningProtocolErrorBody},
		{contentType: "application/json", body: completionJSON(t, "stop", "recovered")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 2)
	agent.modelConfig.BaseURL = "http://adaptive-nonstream.test/v1"
	agent.modelConfig.ModelName = "vendor-model-a"
	clearAdaptiveLLMThinkingDisabled(*agent.modelConfig)
	t.Cleanup(func() { clearAdaptiveLLMThinkingDisabled(*agent.modelConfig) })

	response, err := agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "test"}})
	if err != nil {
		t.Fatalf("adaptive call failed: %v", err)
	}
	if response == nil || response.Content != "recovered" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("request count = %d, want one original call plus one protocol recovery", got)
	}
	if _, exists := capturedLLMRequest(t, transport, 0)["thinking"]; exists {
		t.Fatal("first unknown-model request unexpectedly contained thinking")
	}
	assertCapturedThinkingMode(t, transport, 1, llmThinkingModeDisabled)

	// The learned state is process-global, so a different agent using the same
	// endpoint/model starts in safe mode without paying for another failed call.
	nextTransport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "cached"),
	}}}
	nextAgent := newIncompleteResponseTestAgent(t, nextTransport, false, 2)
	nextAgent.modelConfig.BaseURL = agent.modelConfig.BaseURL
	nextAgent.modelConfig.ModelName = agent.modelConfig.ModelName
	if _, err := nextAgent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "next"}}); err != nil {
		t.Fatal(err)
	}
	if got := nextTransport.requestCount(); got != 1 {
		t.Fatalf("cached safe-mode request count = %d, want 1", got)
	}
	assertCapturedThinkingMode(t, nextTransport, 0, llmThinkingModeDisabled)

	// A sibling model on the same endpoint must remain unaffected.
	otherTransport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "other"),
	}}}
	otherAgent := newIncompleteResponseTestAgent(t, otherTransport, false, 2)
	otherAgent.modelConfig.BaseURL = agent.modelConfig.BaseURL
	otherAgent.modelConfig.ModelName = "vendor-model-b"
	if _, err := otherAgent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "other"}}); err != nil {
		t.Fatal(err)
	}
	if _, exists := capturedLLMRequest(t, otherTransport, 0)["thinking"]; exists {
		t.Fatal("one model's learned state leaked to a sibling model")
	}
}

func TestStreamingReasoningErrorLearnsSafeModeAndRetries(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{statusCode: http.StatusBadRequest, contentType: "application/json", body: adaptiveReasoningProtocolErrorBody},
		{contentType: "text/event-stream", body: completionSSE(t, "stop", "recovered")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, true, 2)
	agent.modelConfig.BaseURL = "http://adaptive-stream.test/v1"
	agent.modelConfig.ModelName = "vendor-stream-model"
	clearAdaptiveLLMThinkingDisabled(*agent.modelConfig)
	t.Cleanup(func() { clearAdaptiveLLMThinkingDisabled(*agent.modelConfig) })

	response, err := agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "test"}}, nil, nil)
	if err != nil {
		t.Fatalf("adaptive streaming call failed: %v", err)
	}
	if response == nil || response.Content != "recovered" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
	if _, exists := capturedLLMRequest(t, transport, 0)["thinking"]; exists {
		t.Fatal("first unknown-model streaming request unexpectedly contained thinking")
	}
	assertCapturedThinkingMode(t, transport, 1, llmThinkingModeDisabled)
}

func TestProviderErrorPayloadAlsoLearnsSafeMode(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: adaptiveReasoningProtocolErrorBody},
		{contentType: "application/json", body: completionJSON(t, "stop", "recovered")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 2)
	agent.modelConfig.BaseURL = "http://adaptive-error-payload.test/v1"
	agent.modelConfig.ModelName = "vendor-error-payload-model"
	clearAdaptiveLLMThinkingDisabled(*agent.modelConfig)
	t.Cleanup(func() { clearAdaptiveLLMThinkingDisabled(*agent.modelConfig) })

	response, err := agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "test"}})
	if err != nil {
		t.Fatalf("adaptive provider-error recovery failed: %v", err)
	}
	if response == nil || response.Content != "recovered" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
	assertCapturedThinkingMode(t, transport, 1, llmThinkingModeDisabled)
}

func assertCapturedThinkingMode(t *testing.T, transport *scriptedLLMTransport, requestIndex int, want string) {
	t.Helper()
	request := capturedLLMRequest(t, transport, requestIndex)
	thinking, _ := request["thinking"].(map[string]interface{})
	if got, _ := thinking["type"].(string); got != want {
		t.Fatalf("request %d thinking.type = %q, want %q; request=%v", requestIndex, got, want, request)
	}
}

func clearAdaptiveLLMThinkingDisabled(config ModelConfig) {
	if key := llmThinkingCompatibilityKey(config); key != "" {
		adaptiveLLMThinkingDisabled.Delete(key)
	}
}
