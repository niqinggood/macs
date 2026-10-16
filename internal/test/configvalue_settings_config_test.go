package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cloneConfigUpdatesForTest(t *testing.T, value map[string]interface{}) map[string]interface{} {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test updates: %v", err)
	}
	var clone map[string]interface{}
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("unmarshal test updates: %v", err)
	}
	return clone
}

func TestConfigMergesPreserveAdvancedAgentFields(t *testing.T) {
	updates := map[string]interface{}{
		"agents": []interface{}{map[string]interface{}{
			"name":                       "Researcher",
			"original_name":              "researcher-source",
			"type":                       "custom",
			"model":                      "test_model",
			"system_message":             "test",
			"ae_name":                    "research-worker",
			"temperature":                float64(0.2),
			"max_tokens":                 float64(8192),
			"enabled_tools":              []interface{}{"read_file", "edit_file"},
			"disallowed_tools":           []interface{}{"web_fetch"},
			"permission_mode":            "bypassPermissions",
			"supports_multimodal":        true,
			"human_input_mode":           "never",
			"max_consecutive_auto_reply": float64(9),
			"max_iterations":             float64(37),
			"has_memory":                 false,
			"memory_window":              float64(23),
			"always_skills":              []interface{}{"builtin-a"},
			"enabled_skills":             []interface{}{"user-b"},
			"external_skills":            []interface{}{"D:/skills"},
		}},
	}

	mergers := []struct {
		name string
		fn   func(*Config, map[string]interface{}) error
	}{
		{name: "settings sections", fn: mergeFullConfig},
		{name: "full config api", fn: (&GatewayService{}).mergeConfig},
	}
	for _, merger := range mergers {
		t.Run(merger.name, func(t *testing.T) {
			cfg := &Config{}
			if err := merger.fn(cfg, updates); err != nil {
				t.Fatalf("merge: %v", err)
			}
			if len(cfg.Agents) != 1 {
				t.Fatalf("agents = %#v", cfg.Agents)
			}
			agent := cfg.Agents[0]
			if agent.MaxIterations != 37 || agent.MaxConsecutiveReplies != 9 || agent.PermissionMode != "bypassPermissions" ||
				!agent.SupportsMultimodal || agent.OriginalName != "researcher-source" || agent.AEName != "research-worker" {
				t.Fatalf("advanced scalar fields were lost: %#v", agent)
			}
			if !reflect.DeepEqual(agent.EnabledTools, []string{"read_file", "edit_file"}) ||
				!reflect.DeepEqual(agent.DisallowedTools, []string{"web_fetch"}) ||
				!reflect.DeepEqual(agent.AlwaysSkills, []string{"builtin-a"}) ||
				!reflect.DeepEqual(agent.EnabledSkills, []string{"user-b"}) ||
				!reflect.DeepEqual(agent.ExternalSkills, []string{"D:/skills"}) {
				t.Fatalf("advanced list fields were lost: %#v", agent)
			}
			if agent.HasMemory == nil || *agent.HasMemory || agent.MemoryWindow == nil || *agent.MemoryWindow != 23 {
				t.Fatalf("memory fields were lost: %#v", agent)
			}
		})
	}
}

func TestConfigMergesKeepSecretsBehindRedactedPlaceholder(t *testing.T) {
	updates := map[string]interface{}{
		"model_config": map[string]interface{}{
			"secure_model": map[string]interface{}{"api_key": "[REDACTED]", "temperature": float64(0.1)},
		},
		"email_config": map[string]interface{}{"password": "[REDACTED]", "smtp_host": "smtp.changed.test"},
		"channels": []interface{}{map[string]interface{}{
			"name": "chat", "type": "custom", "enabled": true,
			"config": map[string]interface{}{"token": "[REDACTED]", "label": "changed"},
		}},
	}
	newConfig := func() *Config {
		return &Config{
			ModelConfig: map[string]ModelConfig{"secure_model": {Type: "openai", APIKey: "sk-real-secret", ModelName: "model"}},
			EmailConfig: EmailConfig{Password: "mail-real-secret"},
			Channels: []ChannelConfig{{Name: "chat", Type: "custom", Enabled: true, Config: map[string]interface{}{
				"token": "channel-real-secret", "label": "old",
			}}},
		}
	}

	mergers := []struct {
		name string
		fn   func(*Config, map[string]interface{}) error
	}{
		{name: "settings sections", fn: mergeFullConfig},
		{name: "full config api", fn: (&GatewayService{}).mergeConfig},
	}
	for _, merger := range mergers {
		t.Run(merger.name, func(t *testing.T) {
			cfg := newConfig()
			clonedUpdates := cloneConfigUpdatesForTest(t, updates)
			if err := merger.fn(cfg, clonedUpdates); err != nil {
				t.Fatalf("merge: %v", err)
			}
			if got := cfg.ModelConfig["secure_model"].APIKey; got != "sk-real-secret" {
				t.Fatalf("model API key overwritten by placeholder: %q", got)
			}
			if cfg.EmailConfig.Password != "mail-real-secret" {
				t.Fatalf("email password overwritten by placeholder: %q", cfg.EmailConfig.Password)
			}
			if got := cfg.Channels[0].Config["token"]; got != "channel-real-secret" {
				t.Fatalf("channel token overwritten by placeholder: %#v", got)
			}
		})
	}
}

func TestMergeFullConfigTreatsModelConfigAsReplacementSnapshot(t *testing.T) {
	cfg := &Config{
		ModelConfig: map[string]ModelConfig{
			"minimax_model": {
				Type:      "minimax",
				APIKey:    "sk-valid-minimax-key",
				ModelName: "minimax-m3",
				BaseURL:   "https://api.minimaxi.com/v1",
			},
		},
	}

	if err := mergeFullConfig(cfg, map[string]interface{}{"model_config": map[string]interface{}{}}); err != nil {
		t.Fatalf("merge config: %v", err)
	}
	if len(cfg.ModelConfig) != 0 {
		t.Fatalf("explicit empty model snapshot should clear models, got %#v", cfg.ModelConfig)
	}
}

func TestMergeFullConfigRenamesModelAndPreservesRedactedSecret(t *testing.T) {
	cfg := &Config{
		ModelConfig: map[string]ModelConfig{
			"old_model": {
				Type:           "openai",
				APIKey:         "sk-real-secret",
				ModelName:      "gpt-old",
				SupportsVision: true,
			},
		},
		Agents: []AgentConfig{{Name: "Assistant", Model: "new_model"}},
	}
	updates := map[string]interface{}{
		"model_config": map[string]interface{}{
			"new_model": map[string]interface{}{
				"__previous_name": "old_model",
				"type":            "openai",
				"api_key":         "[REDACTED]",
				"model_name":      "gpt-new",
			},
		},
	}

	if err := mergeFullConfig(cfg, updates); err != nil {
		t.Fatalf("merge renamed model: %v", err)
	}
	if _, exists := cfg.ModelConfig["old_model"]; exists {
		t.Fatal("renamed model left the old identifier behind")
	}
	renamed, exists := cfg.ModelConfig["new_model"]
	if !exists {
		t.Fatal("renamed model was not created")
	}
	if renamed.APIKey != "sk-real-secret" {
		t.Fatalf("renamed model secret = %q, want preserved value", renamed.APIKey)
	}
	if !renamed.SupportsVision {
		t.Fatal("renaming lost an advanced model field")
	}
}

func TestMergeFullConfigAllowsClearingAgentsAndChannels(t *testing.T) {
	cfg := &Config{
		Agents:   []AgentConfig{{Name: "Assistant", Model: "local"}},
		Channels: []ChannelConfig{{Name: "chat", Type: "custom", Enabled: true}},
	}
	if err := mergeFullConfig(cfg, map[string]interface{}{
		"agents":   []interface{}{},
		"channels": []interface{}{},
	}); err != nil {
		t.Fatalf("merge empty collections: %v", err)
	}
	if len(cfg.Agents) != 0 || len(cfg.Channels) != 0 {
		t.Fatalf("empty snapshots were ignored: agents=%#v channels=%#v", cfg.Agents, cfg.Channels)
	}
}

func TestConfigMergeAppliesPrivateNetworkProtectionOnlyWhenExplicit(t *testing.T) {
	cfg := &Config{}
	if err := mergeFullConfig(cfg, map[string]interface{}{
		"tools_config": map[string]interface{}{
			"enabled":                    true,
			"private_network_protection": true,
		},
	}); err != nil {
		t.Fatalf("merge full config: %v", err)
	}
	if !cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("mergeFullConfig did not enable private-network protection")
	}

	// Omitting the field must preserve the configured value rather than silently
	// restoring a hidden default.
	if err := mergeFullConfig(cfg, map[string]interface{}{
		"tools_config": map[string]interface{}{"enabled": true},
	}); err != nil {
		t.Fatalf("merge partial tools config: %v", err)
	}
	if !cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("omitted private_network_protection unexpectedly changed the setting")
	}

	gateway := &GatewayService{}
	if err := gateway.mergeConfig(cfg, map[string]interface{}{
		"tools_config": map[string]interface{}{"private_network_protection": false},
	}); err != nil {
		t.Fatalf("merge gateway config: %v", err)
	}
	if cfg.ToolsConfig.PrivateNetworkProtection {
		t.Fatal("explicit false did not disable private-network protection")
	}
}

func TestConfigMergePersistsDebugToolGate(t *testing.T) {
	cfg := &Config{}
	updates := map[string]interface{}{
		"tools_config": map[string]interface{}{
			"disabled_tools": []interface{}{"web_fetch", "debug"},
		},
	}
	if err := mergeFullConfig(cfg, updates); err != nil {
		t.Fatalf("merge full config: %v", err)
	}
	if want := []string{"web_fetch", "debug"}; !reflect.DeepEqual(cfg.ToolsConfig.DisabledTools, want) {
		t.Fatalf("disabled tools = %#v, want %#v", cfg.ToolsConfig.DisabledTools, want)
	}

	if err := mergeFullConfig(cfg, map[string]interface{}{
		"tools_config": map[string]interface{}{"disabled_tools": []interface{}{"web_fetch"}},
	}); err != nil {
		t.Fatalf("re-enable debug tool: %v", err)
	}
	if want := []string{"web_fetch"}; !reflect.DeepEqual(cfg.ToolsConfig.DisabledTools, want) {
		t.Fatalf("disabled tools after enabling debug = %#v, want %#v", cfg.ToolsConfig.DisabledTools, want)
	}
}

func TestValidateSettingsConfigRejectsNoAvailableModel(t *testing.T) {
	cfg := &Config{
		Agents: []AgentConfig{{Name: "Assistant", Model: "bad_model"}},
		ModelConfig: map[string]ModelConfig{
			"bad_model": {Type: "openai", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", APIKey: ""},
		},
	}

	err := validateSettingsConfigForSave(cfg)
	if err == nil {
		t.Fatal("expected config without available models to be rejected")
	}
	if !strings.Contains(err.Error(), "没有可用模型") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSettingsConfigRejectsAgentModelNotAvailable(t *testing.T) {
	cfg := &Config{
		Agents: []AgentConfig{{Name: "Assistant", Model: "bad_model"}},
		ModelConfig: map[string]ModelConfig{
			"good_model": {Type: "ollama", ModelName: "qwen3", BaseURL: "http://localhost:11434/v1"},
			"bad_model":  {Type: "openai", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", APIKey: ""},
		},
	}

	err := validateSettingsConfigForSave(cfg)
	if err == nil {
		t.Fatal("expected agent pointing at unavailable model to be rejected")
	}
	if !strings.Contains(err.Error(), "Assistant") || !strings.Contains(err.Error(), "bad_model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSettingsConfigAllowsLocalModelWithoutAPIKey(t *testing.T) {
	cfg := &Config{
		Agents:      []AgentConfig{{Name: "Assistant", Model: "ollama_model"}},
		ModelConfig: map[string]ModelConfig{"ollama_model": {Type: "ollama", ModelName: "qwen3", BaseURL: "http://localhost:11434/v1"}},
	}
	if err := validateSettingsConfigForSave(cfg); err != nil {
		t.Fatalf("local model should be valid without api key: %v", err)
	}
}

func TestHandleConfigReloadUsesFailureHTTPStatus(t *testing.T) {
	gateway := &GatewayService{
		configpath: filepath.Join(t.TempDir(), "missing-config.yaml"),
		logger:     log.New(io.Discard, "", 0),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/config/reload", nil)

	gateway.handleConfigReload(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("reload status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode reload response: %v", err)
	}
	if response["status"] != "error" {
		t.Fatalf("reload response = %#v", response)
	}
}

func TestHandleConfigSectionsRejectsStaleRevision(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("team_name: first\n"), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	staleRevision, err := configFileRevision(configPath)
	if err != nil {
		t.Fatalf("read initial revision: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("team_name: changed\n"), 0o600); err != nil {
		t.Fatalf("change config: %v", err)
	}

	gateway := &GatewayService{configpath: configPath, logger: log.New(io.Discard, "", 0)}
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"config_revision":"` + staleRevision + `"}`)
	request := httptest.NewRequest(http.MethodPut, "/api/config/sections", body)
	gateway.handleConfigSections(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale save status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestHandleConfigSectionsReloadComparesAgainstPreviousRuntimeConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	initial := &Config{
		TeamName:     "before-save",
		DefaultAgent: "Assistant",
		ModelConfig: map[string]ModelConfig{
			"local": {Type: "ollama", ModelName: "qwen3", BaseURL: "http://localhost:11434/v1"},
		},
		Agents: []AgentConfig{{Name: "Assistant", Model: "local"}},
		Channels: []ChannelConfig{{
			Name: "legacy", Type: "custom", Enabled: false, Config: map[string]interface{}{},
		}},
	}
	if err := SaveConfig(configPath, initial); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	gateway := &GatewayService{
		config:     initial,
		configpath: configPath,
		logger:     log.New(io.Discard, "", 0),
	}
	previousChannelManager := NewChannelManager(gateway)
	gateway.channelManager = previousChannelManager

	updates := map[string]interface{}{
		"team_name":     "after-save",
		"default_agent": "Assistant",
		"model_config": map[string]interface{}{
			"local": map[string]interface{}{
				"type": "ollama", "model_name": "qwen3", "base_url": "http://localhost:11434/v1",
			},
		},
		"agents":   []interface{}{map[string]interface{}{"name": "Assistant", "model": "local"}},
		"channels": []interface{}{},
	}
	body, err := json.Marshal(updates)
	if err != nil {
		t.Fatalf("marshal settings update: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/config/sections", strings.NewReader(string(body)))
	gateway.handleConfigSections(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if gateway.config.TeamName != "after-save" || len(gateway.config.Channels) != 0 {
		t.Fatalf("runtime config was not reloaded: %#v", gateway.config)
	}
	if gateway.channelManager == previousChannelManager {
		t.Fatal("channel reload compared against the already-replaced config and skipped the runtime update")
	}
}
