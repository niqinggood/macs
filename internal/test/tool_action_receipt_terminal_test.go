package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type actionReceiptTestTool struct {
	result string
}

func (t *actionReceiptTestTool) Name() string        { return "ptc_execute" }
func (t *actionReceiptTestTool) Description() string { return "execute a test chatroom action" }
func (t *actionReceiptTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"script": map[string]interface{}{"type": "string"},
		},
		"required": []string{"script"},
	}
}
func (t *actionReceiptTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return t.result, nil
}

func actionReceiptToolCallJSON(t *testing.T) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{map[string]interface{}{
					"id":   "publish_chatroom",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "ptc_execute",
						"arguments": `{"script":"subprocess.run([python, 'chatroom_agent.py', 'post', '7', '@post.md'])"}`,
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

func TestGenerateStopsImmediatelyAfterValidatedActionReceipt(t *testing.T) {
	hash := strings.Repeat("a", 64)
	result := "## PTC Execution\n" +
		"Posted! ID: 2764\n" +
		"Share: /chatroom/share?id=41a25400810ca39f\n" +
		`OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":2764,"topic_id":7,"share_url":"/chatroom/share?id=41a25400810ca39f","content_sha256":"` + hash + `","robot_id":"openkitty-test"}` + "\n" +
		"RC: 0"
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: actionReceiptToolCallJSON(t)},
		{contentType: "application/json", body: completionJSON(t, "stop", "this second model call must not happen")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 6)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(&actionReceiptTestTool{result: result})

	output, err := agent.Generate(context.Background(), "请把准备好的内容发布到 chatroom 话题 7。")
	if err != nil {
		t.Fatalf("Generate returned an error after a successful action: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 1 {
		t.Fatalf("model request count = %d, want exactly 1 after terminal action receipt", got)
	}
	for _, want := range []string{"2764", "/chatroom/share?id=41a25400810ca39f", hash} {
		if !strings.Contains(output, want) {
			t.Fatalf("terminal action output missing %q: %s", want, output)
		}
	}

	recovered, err := RecoverSessionTranscriptFromJournal(agent.transcript.JournalPath())
	if err != nil {
		t.Fatalf("recover generated session journal: %v", err)
	}
	wantTypes := map[string]bool{
		"user":          false,
		"model_request": false,
		"assistant":     false,
		"tool_call":     false,
		"tool_result":   false,
	}
	var recoveredToolCallID, recoveredToolResultID string
	for _, entry := range recovered.Entries {
		if _, ok := wantTypes[entry.Type]; ok {
			wantTypes[entry.Type] = true
		}
		if entry.Type == "tool_call" {
			recoveredToolCallID = entry.ToolCallID
		}
		if entry.Type == "tool_result" {
			recoveredToolResultID = entry.ToolCallID
		}
	}
	for entryType, found := range wantTypes {
		if !found {
			t.Fatalf("generated session journal missing %q entry: %#v", entryType, recovered.Entries)
		}
	}
	if recovered.Status != "success" || recovered.FinalOutput == "" {
		t.Fatalf("generated session journal did not persist terminal state: %#v", recovered)
	}
	if recoveredToolCallID == "" || recoveredToolResultID != recoveredToolCallID {
		t.Fatalf("recovered tool lifecycle IDs do not pair: call=%q result=%q", recoveredToolCallID, recoveredToolResultID)
	}
}
