package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mac/internal/textnormalize"
)

func TestSanitizeModelTextStripsTerminalControls(t *testing.T) {
	input := "\x1b[31mred\x1b[0m \x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"
	if got := textnormalize.ModelText(input); got != "red link" {
		t.Fatalf("sanitizeModelText() = %q, want %q", got, "red link")
	}
}

func TestSanitizeModelTextDropsOnlyStructuredTerminalTail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"observed bare JSON residue", `{"final":"A"}[e~[`, `{"final":"A"}`},
		{"repeated generic residue", `{"final":"A"}[?25l[[31m`, `{"final":"A"}`},
		{"closed fence residue", "```json\n{\"final\":\"A\"}\n```[e~[", "```json\n{\"final\":\"A\"}\n```"},
		{"complete Chinese prose residue", "任务已经完成。[e~[", "任务已经完成。"},
		{"complete English prose residue", "The task is complete.[?25l[", "The task is complete."},
		{"legitimate bracket annotation", `{"final":"A"} [done]`, `{"final":"A"} [done]`},
		{"legitimate prose after JSON", `{"final":"A"} explanatory text`, `{"final":"A"} explanatory text`},
		{"same bytes inside prose", "terminal example: [e~[", "terminal example: [e~["},
		{"same bytes inside fenced code", "```text\n[e~[\n```", "```text\n[e~[\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := textnormalize.ModelText(tt.in); got != tt.want {
				t.Fatalf("sanitizeModelText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeProviderJSONDoesNotRepairInvalidEnvelope(t *testing.T) {
	raw := []byte("{\"content\":\"ok" + string(rune(0x1b)) + "[31m\"}")
	var decoded struct {
		Content string `json:"content"`
	}
	if err := textnormalize.DecodeProviderJSON(raw, &decoded); err == nil {
		t.Fatal("literal control byte in provider JSON must be rejected, not silently repaired")
	}

	valid := []byte(`{"content":"\u001b[31mred\u001b[0m"}`)
	if err := textnormalize.DecodeProviderJSON(valid, &decoded); err != nil {
		t.Fatalf("escaped control sequence should remain valid JSON: %v", err)
	}
	if got := textnormalize.ModelText(decoded.Content); got != "red" {
		t.Fatalf("decoded content = %q, want red", got)
	}

	// Lock in the original hazard: the old envelope-level cleaner removed ESC
	// and made the malformed payload parseable with printable residue left over.
	cleanedEnvelope := CleanBytes(raw)
	if err := json.Unmarshal(cleanedEnvelope, &decoded); err != nil {
		t.Fatalf("test setup no longer demonstrates the lossy-cleaning hazard: %v", err)
	}
	if !strings.Contains(decoded.Content, "[31m") {
		t.Fatalf("expected printable terminal fragment after lossy cleanup, got %q", decoded.Content)
	}
}

func TestStreamingSanitizesAfterChunkAssembly(t *testing.T) {
	var callbackContent strings.Builder
	callback := func(chunk StreamChunk) {
		if strings.ContainsAny(chunk.Content+chunk.ReasoningContent, "\x00\x1b") {
			t.Fatalf("callback received a terminal control byte: %#v", chunk)
		}
		callbackContent.WriteString(chunk.Content)
	}
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"\u001b["}}]}`,
		`data: {"choices":[{"delta":{"content":"31mred"}}]}`,
		`data: {"choices":[{"delta":{"content":"\u001b[0m"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	response, err := parseSSEStream(strings.NewReader(stream), callback)
	if err != nil {
		t.Fatalf("parseSSEStream() error = %v", err)
	}
	if response.Content != "red" {
		t.Fatalf("streamed content = %q, want red", response.Content)
	}
	if got := callbackContent.String(); got != "red" {
		t.Fatalf("callback content = %q, want red", got)
	}
}

func TestStreamingCallbackSanitizesCrossChunkCSIAndOSC(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"\u001b["}}]}`,
		`data: {"choices":[{"delta":{"content":"38;5;196mred"}}]}`,
		`data: {"choices":[{"delta":{"content":"\u001b[0"}}]}`,
		`data: {"choices":[{"delta":{"content":"m terminal example: [e~["}}]}`,
		`data: {"choices":[{"delta":{"content":"\u001b]0;window"}}]}`,
		`data: {"choices":[{"delta":{"content":" title\u0007visible\u0000"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"\u001b]8;;https://example.com"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"\u001b\\reason\u001b]8;;\u001b\\"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")

	var callbackContent, callbackReasoning strings.Builder
	callbackCount := 0
	response, err := parseSSEStream(strings.NewReader(stream), func(chunk StreamChunk) {
		callbackCount++
		all := chunk.Content + chunk.ReasoningContent + chunk.ToolCallName + chunk.ToolCallArgs
		if strings.ContainsAny(all, "\x00\x07\x1b") {
			t.Fatalf("callback received raw terminal controls: %#v", chunk)
		}
		for _, residue := range []string{"38;5;196m", "[0m", "window title"} {
			if strings.Contains(all, residue) {
				t.Fatalf("callback received printable terminal residue %q: %#v", residue, chunk)
			}
		}
		callbackContent.WriteString(chunk.Content)
		callbackReasoning.WriteString(chunk.ReasoningContent)
	})
	if err != nil {
		t.Fatalf("parseSSEStream() error = %v", err)
	}

	const wantContent = "red terminal example: [e~[visible"
	if got := callbackContent.String(); got != wantContent {
		t.Fatalf("callback content = %q, want %q", got, wantContent)
	}
	if got := callbackReasoning.String(); got != "reason" {
		t.Fatalf("callback reasoning = %q, want reason", got)
	}
	if response.Content != wantContent || response.ReasoningContent != "reason" {
		t.Fatalf("final response = %#v, want content %q and reasoning reason", response, wantContent)
	}
	if callbackCount == 0 {
		t.Fatal("expected sanitized callback events")
	}
}

func TestStreamingEmitterSanitizesToolFieldsAcrossChunks(t *testing.T) {
	var names, args strings.Builder
	emitter := newSanitizedStreamEmitter(func(chunk StreamChunk) {
		names.WriteString(chunk.ToolCallName)
		args.WriteString(chunk.ToolCallArgs)
	})
	emitter.Emit(StreamChunk{ToolCallName: "\x1b]0;hidden", toolCallID: "call-1"})
	emitter.Emit(StreamChunk{ToolCallName: "\x07write_file", ToolCallArgs: "\x1b[", toolCallID: "call-1"})
	emitter.Emit(StreamChunk{ToolCallArgs: "31m{\"path\":\"ok\"}", toolCallID: "call-1"})
	emitter.Finish()

	if got := names.String(); got != "write_file" {
		t.Fatalf("tool name callback = %q, want write_file", got)
	}
	if got := args.String(); got != `{"path":"ok"}` {
		t.Fatalf("tool args callback = %q, want JSON arguments", got)
	}
}

func TestAutoGenAssistantStrictEnvelopeAndContentSanitization(t *testing.T) {
	t.Run("valid envelope preserves legitimate display text", func(t *testing.T) {
		contents := []string{
			`{"normal":"json"}`,
			"```text\n[e~[\n```",
			"合法展示示例：terminal example: [e~[",
			"\x1b[31mred\x1b[0m",
		}
		for _, content := range contents {
			content := content
			t.Run(content, func(t *testing.T) {
				got, err := callAutoGenTestProvider(t, func(w http.ResponseWriter) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"choices": []interface{}{map[string]interface{}{
							"message": map[string]interface{}{"content": content},
						}},
					})
				})
				if err != nil {
					t.Fatalf("Generate() error = %v", err)
				}
				want := textnormalize.ModelText(content)
				if got != want {
					t.Fatalf("Generate() = %q, want %q", got, want)
				}
			})
		}
	})

	t.Run("malformed raw envelope is rejected", func(t *testing.T) {
		_, err := callAutoGenTestProvider(t, func(w http.ResponseWriter) {
			_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"ok\x1b[31m\"}}]}"))
		})
		if err == nil {
			t.Fatal("Generate() accepted provider JSON containing a literal control byte")
		}
		if !strings.Contains(err.Error(), "解析响应失败") {
			t.Fatalf("Generate() error = %q, want strict decode failure", err)
		}
	})
}

func callAutoGenTestProvider(t *testing.T, writeResponse func(http.ResponseWriter)) (string, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeResponse(w)
	}))
	defer server.Close()

	agent := NewAutoGenAssistant(AgentConfig{
		Name:          "test-autogen",
		SystemMessage: "test",
		MaxTokens:     128,
	}, &ModelConfig{
		ModelName: "test-model",
		BaseURL:   server.URL,
	}, log.New(io.Discard, "", 0))
	return agent.Generate(context.Background(), "hello")
}
