package app

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mac/internal/modelruntime"
	"mac/internal/toolcontract"
	"mac/internal/toolregistry"
)

func TestQualityLocalModelInternalCompletion(t *testing.T) {
	for _, provider := range []string{"ollama", "vllm", "openai"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected model route: %s", r.URL.Path)
				}
				if _, exists := r.Header["Authorization"]; exists {
					t.Error("anonymous local model request carried an Authorization header")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[{"message":{"content":"summary ready"}}]}`)
			}))
			defer server.Close()
			model := ModelConfig{Type: provider, BaseURL: server.URL + "/v1", ModelName: "local-model"}
			if err := ValidateModelConfig("local", &model); err != nil {
				t.Errorf("local model rejected by configuration validation: %v", err)
			}
			agent := &LLMAgent{modelConfig: &model, httpClient: server.Client()}
			ctx := modelruntime.WithCallMaxAttemptsOverride(context.Background(), 1)
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			got, err := agent.callLLMInternal(ctx, []map[string]interface{}{{"role": "user", "content": "summarize"}}, 32)
			if err != nil || got != "summary ready" || calls != 1 {
				t.Fatalf("internal local completion = %q, %v; calls=%d", got, err, calls)
			}
		})
	}
}

func TestQualityCloudModelStillRequiresKey(t *testing.T) {
	model := ModelConfig{Type: "openai", BaseURL: "https://api.example.test/v1", ModelName: "cloud-model"}
	if err := ValidateModelConfig("cloud", &model); err == nil {
		t.Fatal("cloud model without a key was accepted")
	}
	_, err := modelruntime.CallMessages(context.Background(), nil, completionConfig(&model),
		[]map[string]interface{}{{"role": "user", "content": "hello"}}, 32, 0,
		modelruntime.CallPolicy{AttemptTimeout: time.Second, MaxAttempts: 1})
	if err == nil {
		t.Fatal("cloud completion without a key was accepted")
	}
}

func TestQualityReloadDetectsAllModelSettings(t *testing.T) {
	base := ModelConfig{Type: "ollama", BaseURL: "http://localhost:11434/v1", ModelName: "old-model", Temperature: 0.2, MaxTokens: 128}
	changes := map[string]func(*ModelConfig){
		"provider":    func(m *ModelConfig) { m.Type = "vllm" },
		"model":       func(m *ModelConfig) { m.ModelName = "new-model" },
		"temperature": func(m *ModelConfig) { m.Temperature = 0.8 },
		"max_tokens":  func(m *ModelConfig) { m.MaxTokens = 2048 },
		"thinking":    func(m *ModelConfig) { m.ThinkingMode = "enabled" },
		"vision":      func(m *ModelConfig) { m.SupportsVision = true },
		"key_source":  func(m *ModelConfig) { m.APIKeyEnv = "LOCAL_MODEL_KEY" },
	}
	old := &Config{ModelConfig: map[string]ModelConfig{"local": base}}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			updated := base
			change(&updated)
			if !(&GatewayService{}).needUpdateEngine(old, &Config{ModelConfig: map[string]ModelConfig{"local": updated}}) {
				t.Fatal("model setting changed but engine reload was skipped")
			}
		})
	}
	if (&GatewayService{}).needUpdateEngine(old, &Config{ModelConfig: map[string]ModelConfig{"local": base}}) {
		t.Fatal("unchanged model caused unnecessary reload")
	}
	resolved := base
	resolved.apiKeyResolved = true
	if (&GatewayService{}).needUpdateEngine(old, &Config{ModelConfig: map[string]ModelConfig{"local": resolved}}) {
		t.Fatal("internal resolution bookkeeping caused unnecessary reload")
	}
}

func TestQualitySettingsSavePreservesRuntimeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	config := &Config{
		Workspace:   workspace,
		ModelConfig: map[string]ModelConfig{"local": {Type: "ollama", ModelName: "local-model", BaseURL: "http://localhost:11434/v1"}},
		Agents:      []AgentConfig{{Name: "helper", Type: AgentTypeCustom, Model: "local"}},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{config: config, configpath: path, logger: log.New(io.Discard, "", 0)}
	if err := gateway.UpdateFullConfig(map[string]interface{}{
		"gateway": map[string]interface{}{"max_tasks": float64(3)},
	}); err != nil {
		t.Fatal(err)
	}
	if gateway.config.Workspace != workspace {
		t.Fatalf("saving unrelated settings changed workspace to %q", gateway.config.Workspace)
	}
	persisted, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Gateway.MaxTasks != 3 {
		t.Fatalf("settings not persisted: max_tasks=%d", persisted.Gateway.MaxTasks)
	}
	if persisted.Workspace != "" {
		t.Fatal("runtime-only workspace leaked into YAML")
	}
}

func TestQualityPermissionCatalogOnlyAdvertisesExecutableActions(t *testing.T) {
	registry := toolregistry.New(toolregistry.Dependencies{})
	registry.Register(&ManagePermissionsTool{})
	definition, ok := registry.GetToolDefinition("manage_permissions")
	if !ok {
		t.Fatal("permission tool not discoverable")
	}
	parameters := definition["function"].(map[string]interface{})["parameters"].(map[string]interface{})
	properties := parameters["properties"].(map[string]interface{})
	actions := properties["action"].(map[string]interface{})["enum"].([]string)
	if !reflect.DeepEqual(actions, []string{"view", "list_rules", "suggest_rule"}) {
		t.Fatalf("catalog advertised unavailable actions: %v", actions)
	}
	for _, action := range []string{"set", "add_rule", "remove_rule"} {
		if err := toolcontract.ValidateArguments(parameters, map[string]interface{}{"action": action}); err == nil {
			t.Errorf("host-only action %s was accepted by the agent schema", action)
		}
	}
}
