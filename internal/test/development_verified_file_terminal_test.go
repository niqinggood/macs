package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type verifiedFileTerminalTool struct {
	name   string
	result string
}

func (t *verifiedFileTerminalTool) Name() string        { return t.name }
func (t *verifiedFileTerminalTool) Description() string { return "terminal file receipt test tool" }
func (t *verifiedFileTerminalTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":    map[string]interface{}{"type": "string"},
			"content": map[string]interface{}{"type": "string"},
		},
		"required": []string{"path"},
	}
}
func (t *verifiedFileTerminalTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return t.result, nil
}

func verifiedFileToolCallJSON(t *testing.T, id, name, arguments string) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{map[string]interface{}{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
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

func TestGenerateRecoversVerifiedFileWhenProviderFinalMarkerIsMalformed(t *testing.T) {
	const path = `runs/S27/builtin_skill_evidence.md`
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: verifiedFileToolCallJSON(t, "write", "write_file", `{"path":"`+path+`","content":"source=builtin"}`)},
		{contentType: "application/json", body: verifiedFileToolCallJSON(t, "read", "read_file", `{"path":"`+path+`"}`)},
		{contentType: "application/json", body: completionJSON(t, "stop", "]<]minimax[>[")},
		{contentType: "application/json", body: completionJSON(t, "stop", "]<]minimax[>[")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 4)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(&verifiedFileTerminalTool{name: "write_file", result: "成功写入文件: " + path})
	agent.toolRegistry.Register(&verifiedFileTerminalTool{name: "read_file", result: "# Evidence\nsource=builtin\nPPT 验收"})

	output, err := agent.Generate(context.Background(), "完成这项多阶段证据整理任务，并在文件核验后给出最终结果。")
	if err != nil {
		t.Fatalf("verified artifact should recover provider-only finalization failure: %v\n%s", err, output)
	}
	for _, want := range []string{"已完成并回读核验交付文件", path, "恢复了交付状态"} {
		if !strings.Contains(output, want) {
			t.Fatalf("recovered final output missing %q: %s", want, output)
		}
	}
	if strings.Contains(output, "]<]minimax[>[") || strings.Contains(output, "未能生成可用最终回答") {
		t.Fatalf("provider residue or internal diagnostic escaped recovered output: %s", output)
	}

	recovered, err := RecoverSessionTranscriptFromJournal(agent.transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "success" || !strings.Contains(recovered.FinalOutput, path) {
		t.Fatalf("session journal did not persist recovered success: %#v", recovered)
	}
}
