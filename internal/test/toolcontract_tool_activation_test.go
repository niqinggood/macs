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
)

type activationTestTool struct{ name string }

func (t *activationTestTool) Name() string        { return t.name }
func (t *activationTestTool) Description() string { return "turn activation test tool" }
func (t *activationTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{"type": "string"},
		},
	}
}
func (t *activationTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}

type captureToolSchemaTransport struct {
	mu     sync.Mutex
	bodies []map[string]interface{}
}

func (t *captureToolSchemaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.bodies = append(t.bodies, body)
	t.mu.Unlock()
	response := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(response)),
		Request:    req,
	}, nil
}

func (t *captureToolSchemaTransport) lastBody() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.bodies) == 0 {
		return nil
	}
	return t.bodies[len(t.bodies)-1]
}

func toolSchemaNamesFromRequest(t *testing.T, body map[string]interface{}) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	rawTools, _ := body["tools"].([]interface{})
	for _, raw := range rawTools {
		definition, _ := raw.(map[string]interface{})
		function, _ := definition["function"].(map[string]interface{})
		name, _ := function["name"].(string)
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func newToolActivationRequestTestAgent(registry *ToolRegistry, transport http.RoundTripper) *LLMAgent {
	return &LLMAgent{
		name: "tool-activation-test",
		modelConfig: &ModelConfig{
			Type:        "openai",
			APIKey:      "test-api-key-long-enough",
			ModelName:   "test-model",
			BaseURL:     "http://unit.test/v1",
			MaxTokens:   64,
			Temperature: 0,
		},
		httpClient:   &http.Client{Transport: transport},
		fileLogger:   log.New(io.Discard, "", 0),
		globalConfig: &Config{Runtime: RuntimeConfig{LLMCallTimeoutSeconds: 15, LLMMaxRetries: 1}},
		toolRegistry: registry,
	}
}

func TestToolDetailGetMakesExtendedSchemaCallableInCurrentTurn(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	registry.Register(&activationTestTool{name: "turn_extension"})
	ctx := withNewTurnToolActivation(context.Background())

	detail, _ := registry.Get("tool_detail")
	if _, err := detail.Execute(ctx, map[string]interface{}{
		"action":    "get",
		"tool_name": "turn_extension",
	}); err != nil {
		t.Fatalf("tool_detail get failed: %v", err)
	}

	transport := &captureToolSchemaTransport{}
	agent := newToolActivationRequestTestAgent(registry, transport)
	if _, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "use it"}}); err != nil {
		t.Fatalf("callLLM failed: %v", err)
	}
	names := toolSchemaNamesFromRequest(t, transport.lastBody())
	if !names["turn_extension"] {
		t.Fatalf("activated extension schema missing from next model request: %v", names)
	}
}

func TestSuppressedToolDefinitionsOmitsSchemasFromInternalRepairCall(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	transport := &captureToolSchemaTransport{}
	agent := newToolActivationRequestTestAgent(registry, transport)
	ctx := withSuppressedToolDefinitions(withNewTurnToolActivation(context.Background()))
	if _, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "repair only"}}); err != nil {
		t.Fatalf("callLLM failed: %v", err)
	}
	if _, exists := transport.lastBody()["tools"]; exists {
		t.Fatalf("suppressed internal repair request still exposed tool schemas: %#v", transport.lastBody()["tools"])
	}
}

func TestToolDetailActivationDoesNotLeakAcrossTurns(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	registry.Register(&activationTestTool{name: "isolated_extension"})
	detail, _ := registry.Get("tool_detail")
	firstTurn := withNewTurnToolActivation(context.Background())
	if _, err := detail.Execute(firstTurn, map[string]interface{}{
		"action":    "get",
		"tool_name": "isolated_extension",
	}); err != nil {
		t.Fatalf("first-turn activation failed: %v", err)
	}

	transport := &captureToolSchemaTransport{}
	agent := newToolActivationRequestTestAgent(registry, transport)
	if _, err := agent.callLLM(firstTurn, []map[string]interface{}{{"role": "user", "content": "first"}}); err != nil {
		t.Fatalf("first callLLM failed: %v", err)
	}
	if !toolSchemaNamesFromRequest(t, transport.lastBody())["isolated_extension"] {
		t.Fatal("first turn did not include activated extension")
	}

	secondTurn := withNewTurnToolActivation(context.Background())
	if _, err := agent.callLLM(secondTurn, []map[string]interface{}{{"role": "user", "content": "second"}}); err != nil {
		t.Fatalf("second callLLM failed: %v", err)
	}
	if toolSchemaNamesFromRequest(t, transport.lastBody())["isolated_extension"] {
		t.Fatal("activated extension leaked into a new turn")
	}
}

func TestConfiguredEnabledExtendedToolSchemaIsCallableWithoutDiscovery(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	registry.Register(&activationTestTool{name: "configured_extension"})
	registry.Register(&activationTestTool{name: "unconfigured_extension"})

	transport := &captureToolSchemaTransport{}
	agent := newToolActivationRequestTestAgent(registry, transport)
	agent.enabledTools = []string{"tool_detail", "configured_extension"}
	ctx := withNewTurnToolActivation(context.Background())
	if _, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "perform the assigned task"}}); err != nil {
		t.Fatalf("callLLM failed: %v", err)
	}
	names := toolSchemaNamesFromRequest(t, transport.lastBody())
	if !names["configured_extension"] {
		t.Fatalf("explicitly enabled extension schema is missing: %v", names)
	}
	if names["unconfigured_extension"] {
		t.Fatalf("unconfigured extension schema was exposed: %v", names)
	}
}

func TestToolDetailActivationIsBoundedToFourExtensions(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	detail, _ := registry.Get("tool_detail")
	ctx := withNewTurnToolActivation(context.Background())
	for i, name := range []string{"ext_one", "ext_two", "ext_three", "ext_four", "ext_five"} {
		registry.Register(&activationTestTool{name: name})
		_, err := detail.Execute(ctx, map[string]interface{}{"action": "get", "tool_name": name})
		if i < maxTurnActivatedTools && err != nil {
			t.Fatalf("activation %d failed: %v", i+1, err)
		}
		if i == maxTurnActivatedTools && (err == nil || !strings.Contains(err.Error(), "limit")) {
			t.Fatalf("fifth activation error = %v, want bounded limit error", err)
		}
	}

	activation := turnToolActivationFromContext(ctx)
	if got := len(activation.Names()); got != maxTurnActivatedTools {
		t.Fatalf("activated extensions = %d, want %d", got, maxTurnActivatedTools)
	}
	defs, err := json.Marshal(registry.GetCoreDefinitionsWithActivated(activation.Names()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(defs, []byte(`"ext_five"`)) {
		t.Fatal("fifth extension appeared in callable schemas")
	}
}

func TestToolDetailActivationRejectsUnknownAndSafetyBlacklistedTools(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterBuiltinTools()
	registry.Register(&activationTestTool{name: "delete_file"})
	detail, _ := registry.Get("tool_detail")
	ctx := withNewTurnToolActivation(context.Background())

	if _, err := detail.Execute(ctx, map[string]interface{}{
		"action":    "get",
		"tool_name": "does_not_exist",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown tool error = %v, want not found", err)
	}
	if _, err := detail.Execute(ctx, map[string]interface{}{
		"action":    "get",
		"tool_name": "delete_file",
	}); err == nil || !strings.Contains(err.Error(), "safety policy") {
		t.Fatalf("blacklisted tool error = %v, want safety rejection", err)
	}
	if names := turnToolActivationFromContext(ctx).Names(); len(names) != 0 {
		t.Fatalf("rejected tools changed activation state: %v", names)
	}

	defs, err := json.Marshal(registry.GetCoreDefinitionsWithActivated([]string{"does_not_exist", "delete_file"}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(defs, []byte(`"does_not_exist"`)) || bytes.Contains(defs, []byte(`"delete_file"`)) {
		t.Fatalf("unknown or blacklisted tool appeared in callable schemas: %s", defs)
	}
}
