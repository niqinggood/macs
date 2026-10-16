package app

import (
	"strings"
	"testing"

	"mac/internal/runtimeutil"
)

func TestEstimateTokens_English(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	tokens := runtimeutil.EstimateTokens(text)
	// 9 words * ~1.3 = ~12 tokens
	if tokens < 8 || tokens > 20 {
		t.Errorf("English: expected ~12 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_Chinese(t *testing.T) {
	text := "今天天气很好适合出去玩"
	tokens := runtimeutil.EstimateTokens(text)
	// 11 Chinese chars * ~0.9 = ~10 tokens
	if tokens < 6 || tokens > 16 {
		t.Errorf("Chinese: expected ~10 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_Code(t *testing.T) {
	text := "func main() { fmt.Println(\"hello\") }"
	tokens := runtimeutil.EstimateTokens(text)
	// Mix of words, delimiters: should be reasonable
	if tokens < 5 || tokens > 30 {
		t.Errorf("Code: got %d tokens, expected reasonable range", tokens)
	}
}

func TestEstimateTokens_Mixed(t *testing.T) {
	text := "Hello 你好 world 世界! How are you?"
	tokens := runtimeutil.EstimateTokens(text)
	if tokens < 5 || tokens > 25 {
		t.Errorf("Mixed: got %d tokens, expected reasonable range", tokens)
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	tokens := runtimeutil.EstimateTokens("")
	if tokens != 1 {
		t.Errorf("Empty string should return 1 token minimum, got %d", tokens)
	}
}

func TestEstimateTokensForMessages(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "You are a helpful assistant"},
		{"role": "user", "content": "Hello, how are you?"},
		{"role": "assistant", "content": "I am fine, thank you!"},
	}
	tokens := estimateTokensForMessages(msgs)
	if tokens < 10 || tokens > 50 {
		t.Errorf("Messages: expected 10-50 tokens, got %d", tokens)
	}
}

func TestMessageTokenEstimatesIncludeReasoningAndGoBuiltToolCalls(t *testing.T) {
	base := []map[string]interface{}{{"role": "assistant", "content": "ok"}}
	withProtocolFields := []map[string]interface{}{{
		"role":              "assistant",
		"content":           "ok",
		"reasoning_content": strings.Repeat("provider reasoning trace ", 200),
		"tool_calls": []map[string]interface{}{
			{"id": "call_1"},
			{"id": "call_2"},
		},
	}}
	if got, wantMin := estimateTokensForMessages(withProtocolFields), estimateTokensForMessages(base)+100; got < wantMin {
		t.Fatalf("estimate with reasoning/tool calls = %d, want at least %d", got, wantMin)
	}

	agent := &LLMAgent{}
	if got, baseTokens := agent.calcMessagesTokens(withProtocolFields), agent.calcMessagesTokens(base); got <= baseTokens {
		t.Fatalf("agent token estimate ignored reasoning/tool calls: got=%d base=%d", got, baseTokens)
	}
}

func TestEstimateTokens_LongText(t *testing.T) {
	text := ""
	for i := 0; i < 100; i++ {
		text += "The quick brown fox jumps over the lazy dog. "
	}
	tokens := runtimeutil.EstimateTokens(text)
	// 100 * 9 words * 1.3 ≈ 1170
	if tokens < 800 || tokens > 1600 {
		t.Errorf("Long text: expected ~1170 tokens, got %d", tokens)
	}
}
