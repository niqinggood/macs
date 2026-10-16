package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEffectiveLLMMaxTokensOnlyLowersConfiguredCeiling(t *testing.T) {
	ctx := withLLMOutputTokenLimit(context.Background(), 4096)
	if got := effectiveLLMMaxTokens(ctx, 393216); got != 4096 {
		t.Fatalf("large configured ceiling = %d, want 4096", got)
	}
	if got := effectiveLLMMaxTokens(ctx, 2048); got != 2048 {
		t.Fatalf("small configured ceiling = %d, want 2048", got)
	}
	if got := effectiveLLMMaxTokens(context.Background(), 8192); got != 8192 {
		t.Fatalf("unlimited context = %d, want 8192", got)
	}
}

func TestEffectiveLLMTemperatureUsesTurnLocalOverride(t *testing.T) {
	if got := effectiveLLMTemperature(context.Background(), 0.7); got != 0.7 {
		t.Fatalf("default temperature = %v, want 0.7", got)
	}
	ctx := withLLMTemperatureOverride(context.Background(), 0.2)
	if got := effectiveLLMTemperature(ctx, 0.7); got != 0.2 {
		t.Fatalf("overridden temperature = %v, want 0.2", got)
	}
	ctx = withLLMTemperatureOverride(context.Background(), 0)
	if got := effectiveLLMTemperature(ctx, 0.7); got != 0 {
		t.Fatalf("zero temperature override = %v, want 0", got)
	}
}

func TestLLMThinkingDisabledIsTurnLocal(t *testing.T) {
	if llmThinkingDisabled(context.Background()) {
		t.Fatal("thinking should not be disabled by default")
	}
	ctx := withLLMThinkingDisabled(context.Background())
	if !llmThinkingDisabled(ctx) {
		t.Fatal("turn-local thinking disable override was lost")
	}
}

func TestMiniMaxM3RequestExplicitlyDisablesThinkingForClosedDelivery(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "ok"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 2)
	agent.modelConfig.ModelName = "MiniMax-M3"
	ctx := withLLMThinkingDisabled(context.Background())
	if _, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "write report"}}); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	var request map[string]interface{}
	if err := json.Unmarshal(transport.requests[0], &request); err != nil {
		t.Fatal(err)
	}
	thinking, _ := request["thinking"].(map[string]interface{})
	if thinking["type"] != "disabled" {
		t.Fatalf("MiniMax-M3 thinking = %#v, want disabled", request["thinking"])
	}
}

func TestAdaptiveLLMOutputTokenLimitIsScopedToReadOnlyResearch(t *testing.T) {
	if got := adaptiveLLMOutputTokenLimit("请联网查询未来7天天气并给出来源"); got != 4096 {
		t.Fatalf("read-only research limit = %d, want 4096", got)
	}
	if got := adaptiveLLMOutputTokenLimit("整理今天8条科技新闻并给来源和摘要"); got != 5632 {
		t.Fatalf("current-news output limit = %d, want 5632", got)
	}
	if got := adaptiveLLMOutputTokenLimit("调研天气并生成一个PPT文件"); got != 0 {
		t.Fatalf("artifact task limit = %d, want 0", got)
	}
	if got := adaptiveLLMOutputTokenLimit("调研价格并写一份需要确认的报价清单"); got != 0 {
		t.Fatalf("long checklist artifact limit = %d, want 0", got)
	}
	for _, input := range []string{"查询天气后发到chatroom", "查询油价并发给同事", "查询新闻后同步到群里"} {
		if got := adaptiveLLMOutputTokenLimit(input); got != 0 {
			t.Fatalf("side-effect task %q limit = %d, want 0", input, got)
		}
	}
}

func TestLLMRequestsHonorTurnLocalOutputLimit(t *testing.T) {
	t.Run("non-streaming", func(t *testing.T) {
		transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
			contentType: "application/json",
			body:        completionJSON(t, "stop", "ok"),
		}}}
		agent := newIncompleteResponseTestAgent(t, transport, false, 2)
		agent.maxTokens = 393216
		agent.modelConfig.MaxTokens = 393216
		ctx := withLLMOutputTokenLimit(context.Background(), 4096)
		if _, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "weather"}}); err != nil {
			t.Fatal(err)
		}
		assertRequestMaxTokens(t, transport, 4096)
	})

	t.Run("streaming", func(t *testing.T) {
		transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
			contentType: "text/event-stream",
			body:        completionSSE(t, "stop", "ok"),
		}}}
		agent := newIncompleteResponseTestAgent(t, transport, true, 2)
		agent.maxTokens = 393216
		agent.modelConfig.MaxTokens = 393216
		ctx := withLLMOutputTokenLimit(context.Background(), 4096)
		if _, err := agent.callLLMStream(ctx, []map[string]interface{}{{"role": "user", "content": "weather"}}, nil, nil); err != nil {
			t.Fatal(err)
		}
		assertRequestMaxTokens(t, transport, 4096)
	})
}

func TestLLMRequestsHonorTurnLocalTemperature(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming], func(t *testing.T) {
			contentType := "application/json"
			body := completionJSON(t, "stop", "ok")
			if streaming {
				contentType = "text/event-stream"
				body = completionSSE(t, "stop", "ok")
			}
			transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{contentType: contentType, body: body}}}
			agent := newIncompleteResponseTestAgent(t, transport, streaming, 2)
			ctx := withLLMTemperatureOverride(context.Background(), 0.2)
			var err error
			if streaming {
				_, err = agent.callLLMStream(ctx, []map[string]interface{}{{"role": "user", "content": "news"}}, nil, nil)
			} else {
				_, err = agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "news"}})
			}
			if err != nil {
				t.Fatal(err)
			}
			transport.mu.Lock()
			defer transport.mu.Unlock()
			var request map[string]interface{}
			if err := json.Unmarshal(transport.requests[0], &request); err != nil {
				t.Fatal(err)
			}
			if got, _ := request["temperature"].(float64); got != 0.2 {
				t.Fatalf("temperature = %v, want 0.2", request["temperature"])
			}
		})
	}
}

func TestStreamingMiniMaxRequestHonorsThinkingDisabled(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "text/event-stream",
		body:        completionSSE(t, "stop", "ok"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, true, 2)
	agent.modelConfig.ModelName = "MiniMax-M3"
	ctx := withLLMThinkingDisabled(context.Background())
	if _, err := agent.callLLMStream(ctx, []map[string]interface{}{{"role": "user", "content": "report"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	var request map[string]interface{}
	if err := json.Unmarshal(transport.requests[0], &request); err != nil {
		t.Fatal(err)
	}
	thinking, _ := request["thinking"].(map[string]interface{})
	if got, _ := thinking["type"].(string); got != "disabled" {
		t.Fatalf("thinking.type = %q, want disabled; request=%v", got, request)
	}
}

func TestStreamingExplicitThinkingModeEnablesReasoning(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "text/event-stream",
		body:        completionSSE(t, "stop", "ok"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, true, 2)
	agent.modelConfig.ModelName = "deepseek-reasoner"
	agent.modelConfig.ThinkingMode = "enabled"
	if _, err := agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "reason"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	request := capturedLLMRequest(t, transport, 0)
	thinking, _ := request["thinking"].(map[string]interface{})
	if got, _ := thinking["type"].(string); got != "enabled" {
		t.Fatalf("streaming thinking.type = %q, want enabled; request=%v", got, request)
	}
}

func assertRequestMaxTokens(t *testing.T, transport *scriptedLLMTransport, want int) {
	t.Helper()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(transport.requests))
	}
	var request map[string]interface{}
	if err := json.Unmarshal(transport.requests[0], &request); err != nil {
		t.Fatal(err)
	}
	got, _ := request["max_tokens"].(float64)
	if int(got) != want {
		t.Fatalf("max_tokens = %v, want %d", request["max_tokens"], want)
	}
}
