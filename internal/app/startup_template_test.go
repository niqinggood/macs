package app

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func embeddedStartupTemplateFixture(t *testing.T) *Config {
	t.Helper()
	var config Config
	if err := yaml.Unmarshal(defaultConfigYAML, &config); err != nil {
		t.Fatalf("decode embedded startup template: %v", err)
	}
	return &config
}

func TestEmbeddedStartupTemplateAllowsWebConfigurationWithoutModelKeys(t *testing.T) {
	config := embeddedStartupTemplateFixture(t)
	for name, model := range config.ModelConfig {
		if modelConfigRequiresAPIKey(model) && isModelConfigAvailable(model) {
			t.Fatalf("startup template model %q must not include usable remote credentials", name)
		}
	}
	if valid, reason := ValidateConfig(config); !valid {
		t.Fatalf("credential-free startup template must allow Web configuration: %s", reason)
	}
	if !config.Gateway.Enabled || !config.Gateway.EnableWebUI || config.Headless {
		t.Fatal("startup template must enable the Web UI and automatic browser opening")
	}
	if config.DefaultModel != "" {
		t.Fatalf("startup template must use model fallback, got default %q", config.DefaultModel)
	}
	selected := selectedModelKey(config)
	if selected != "ollama_model" {
		t.Fatalf("keyless template must select its local model, got %q", selected)
	}
	if modelConfigRequiresAPIKey(config.ModelConfig[selected]) {
		t.Fatal("the startup fallback must not require a model API key")
	}
	for _, agent := range config.Agents {
		if got := effectiveAgentModelKey(config, agent); got != selected {
			t.Fatalf("startup Agent %q inherited %q, want %q", agent.Name, got, selected)
		}
	}
}

func TestEmbeddedStartupTemplateRejectsExplicitUnavailableDefault(t *testing.T) {
	config := embeddedStartupTemplateFixture(t)
	config.DefaultModel = "minimax_model"
	if valid, reason := ValidateConfig(config); valid || !strings.Contains(reason, "当前选择的模型 minimax_model 不可用") {
		t.Fatalf("an explicitly selected model without credentials must remain invalid: valid=%v reason=%q", valid, reason)
	}
}

func TestEmbeddedStartupTemplateStillRequiresUsableModel(t *testing.T) {
	config := embeddedStartupTemplateFixture(t)
	config.DefaultModel = ""
	config.ModelConfig = map[string]ModelConfig{"minimax_model": config.ModelConfig["minimax_model"]}
	if selected := selectedModelKey(config); selected != "" {
		t.Fatalf("fallback must not select a remote model without credentials, got %q", selected)
	}
	if valid, reason := ValidateConfig(config); valid || !strings.Contains(reason, "没有可用的模型配置") {
		t.Fatalf("a config without any usable model must remain invalid: valid=%v reason=%q", valid, reason)
	}
}
