package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestPrivateNetworkProtectionConfigDefaultsDisabled(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"tools_config":{"enabled":true}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("omitted private_network_protection must default to false")
	}
	if err := json.Unmarshal([]byte(`{"tools_config":{"enabled":true,"private_network_protection":true}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("explicit private_network_protection=true was not applied")
	}
	cfg = Config{}
	if err := yaml.Unmarshal([]byte("tools_config:\n  enabled: true\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("omitted YAML private_network_protection must default to false")
	}
	if err := yaml.Unmarshal([]byte("tools_config:\n  enabled: true\n  private_network_protection: true\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("explicit YAML private_network_protection=true was not applied")
	}
}

func TestWebFetchBatchExecutesConcurrentlyAndPreservesResultOrder(t *testing.T) {
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(180 * time.Millisecond)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte("# Verified fixture\n\n" + strings.Repeat("parallel evidence ", 20)))
	}))
	defer server.Close()

	registry := NewToolRegistry()
	registry.Register(NewWebFetchTool(3, "", false, nil, false))
	agent := &LLMAgent{toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)
	messages, coldMessages := []map[string]interface{}{}, []map[string]interface{}{}
	summary, chain, results := []string{}, []map[string]interface{}{}, []string{}
	var totalMs int64
	count := 0
	state := &ToolExecutionState{
		Messages: &messages, ColdMessages: &coldMessages, TaskID: "web-fetch-parallel",
		ToolCallSummary: &summary, CallChain: &chain, TotalDurationMs: &totalMs,
		AllToolResults: &results, ToolExecCount: &count,
	}
	calls := []ToolCall{
		{ID: "first", Name: "web_fetch", Arguments: map[string]interface{}{"url": server.URL + "/first.md"}},
		{ID: "second", Name: "web_fetch", Arguments: map[string]interface{}{"url": server.URL + "/second.md"}},
		{ID: "third", Name: "web_fetch", Arguments: map[string]interface{}{"url": server.URL + "/third.md"}},
	}
	started := time.Now()
	if err := executor.Execute(context.Background(), calls, state); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("web_fetch calls took %v; likely serialized", elapsed)
	}
	if peak.Load() < 2 || peak.Load() > 3 {
		t.Fatalf("web_fetch peak concurrency = %d, want 2..3", peak.Load())
	}
	if count != 3 || len(messages) != 3 || len(results) != 3 {
		t.Fatalf("parallel results incomplete: count=%d messages=%d results=%d", count, len(messages), len(results))
	}
	for i, wantID := range []string{"first", "second", "third"} {
		if messages[i]["tool_call_id"] != wantID {
			t.Fatalf("result order[%d] = %v, want %s", i, messages[i]["tool_call_id"], wantID)
		}
	}
}
