package app

import (
	"path/filepath"
	"testing"
)

func TestAPIKeyCheckpointMakesNextLaunchWebReady(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	config := newInteractiveConfig()
	provider := &ProviderConfig{
		Name:          "Checkpoint Provider",
		ModelType:     "openai",
		BaseURL:       "https://example.invalid/v1",
		ModelName:     "checkpoint-model",
		RequireAPIKey: true,
	}

	modelKey := applyProviderDefaults(config, provider, "test-api-key")
	if modelKey != "openai_model" {
		t.Fatalf("unexpected model key: %q", modelKey)
	}
	if err := saveInteractiveCheckpoint(configPath, config); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	loaded, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("next launch should accept the checkpoint without reopening setup: %v", err)
	}
	if valid, reason := ValidateConfig(loaded); !valid {
		t.Fatalf("checkpoint must be a valid startup config: %s", reason)
	}
	got, ok := loaded.ModelConfig[modelKey]
	if !ok {
		t.Fatalf("checkpoint model %q missing", modelKey)
	}
	if got.APIKey != "test-api-key" || got.BaseURL != provider.BaseURL || got.ModelName != provider.ModelName {
		t.Fatalf("checkpoint did not retain provider defaults: %+v", got)
	}
	if !loaded.Gateway.Enabled || !loaded.Gateway.EnableWebUI || loaded.Headless {
		t.Fatalf("next double-click must enter web mode: gateway=%+v headless=%v", loaded.Gateway, loaded.Headless)
	}
	if len(loaded.Agents) == 0 || loaded.Agents[0].Model != modelKey {
		t.Fatalf("default assistant should use checkpoint model %q: %+v", modelKey, loaded.Agents)
	}
}

func TestInteractiveCheckpointRejectsIncompleteProvider(t *testing.T) {
	config := newInteractiveConfig()
	if err := saveInteractiveCheckpoint(filepath.Join(t.TempDir(), "openkitty-config.yaml"), config); err == nil {
		t.Fatal("incomplete setup must not be marked complete")
	}
}
