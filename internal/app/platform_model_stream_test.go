package app

import (
	"strings"
	"testing"
)

func TestPlatformBufferedSSEPreservesMultipleToolArguments(t *testing.T) {
	stream := `data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"report.txt\"}"}},{"index":1,"id":"write","type":"function","function":{"name":"write_file","arguments":"{\"content\":\"result\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}` + "\n\ndata: [DONE]\n\n"
	response, err := parseSSEStream(strings.NewReader(stream), func(StreamChunk) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 2 || response.PromptTokens != 10 || response.CompletionTokens != 5 {
		t.Fatalf("response=%+v", response)
	}
	for _, call := range response.ToolCalls {
		if call.Name == "read_file" && call.Arguments["path"] != "report.txt" {
			t.Fatalf("read arguments=%v", call.Arguments)
		}
		if call.Name == "write_file" && call.Arguments["content"] != "result" {
			t.Fatalf("write arguments=%v", call.Arguments)
		}
	}
}
