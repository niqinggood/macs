package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mac/internal/toolregistry"
)

type llmAppRegistryEchoTool struct{ calls atomic.Int32 }

func (tool *llmAppRegistryEchoTool) Name() string        { return "registry_echo" }
func (tool *llmAppRegistryEchoTool) Description() string { return "test registry bridge" }
func (tool *llmAppRegistryEchoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"payload": map[string]interface{}{"type": "string"},
		},
		"required": []string{"payload"},
	}
}
func (tool *llmAppRegistryEchoTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	tool.calls.Add(1)
	data, _ := json.Marshal(map[string]interface{}{"registry": true, "payload": params["payload"]})
	return string(data), nil
}
func (tool *llmAppRegistryEchoTool) IsReadOnly() bool { return true }

func TestLLMAppGraphToolNodeUsesAgentToolRegistry(t *testing.T) {
	workspace := t.TempDir()
	registry := toolregistry.New(toolregistry.Dependencies{})
	echo := &llmAppRegistryEchoTool{}
	registry.Register(echo)
	agent := &LLMAgent{
		name:         "registry-test",
		workspace:    workspace,
		fileLogger:   log.New(io.Discard, "", 0),
		httpClient:   http.DefaultClient,
		toolRegistry: registry,
		modelConfig: &ModelConfig{
			APIKey: "unused-test-key", ModelName: "unused-test-model", BaseURL: "http://127.0.0.1:1/v1",
		},
	}
	tool := NewLLMAppTool(agent)
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "create",
		"definition": map[string]interface{}{
			"name": "registry-graph", "workflow": "graph",
			"graph": map[string]interface{}{
				"output_node": "result",
				"nodes": []interface{}{
					map[string]interface{}{"id": "source", "type": "source", "source": "input"},
					map[string]interface{}{
						"id": "echo", "type": "tool", "depends_on": []interface{}{"source"},
						"tool": "registry_echo", "arguments": map[string]interface{}{"payload": "{{value}}"},
					},
					map[string]interface{}{"id": "result", "type": "sink", "depends_on": []interface{}{"echo"}},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	startedJSON, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "start", "name": "registry-graph", "input": "through-registry",
	})
	if err != nil {
		t.Fatal(err)
	}
	var started map[string]interface{}
	if err := json.Unmarshal([]byte(startedJSON), &started); err != nil {
		t.Fatal(err)
	}
	runID, _ := started["run_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		statusJSON, err := tool.Execute(context.Background(), map[string]interface{}{
			"action": "status", "run_id": runID,
		})
		if err != nil {
			t.Fatal(err)
		}
		var status map[string]interface{}
		_ = json.Unmarshal([]byte(statusJSON), &status)
		if status["status"] == "completed" {
			completed = true
			break
		}
		if status["status"] == "failed" {
			t.Fatalf("graph failed: %s", statusJSON)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("graph did not reach completed status")
	}
	if echo.calls.Load() != 1 {
		t.Fatalf("registry tool calls = %d, want 1", echo.calls.Load())
	}
	resultJSON, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "result", "run_id": runID, "include_outputs": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(resultJSON)) || !containsAll(resultJSON, `"registry": true`, "through-registry") {
		t.Fatalf("unexpected graph result: %s", resultJSON)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
