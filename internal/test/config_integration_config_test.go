package app

import (
	"strings"
	"testing"
)

func loadTestRuntimeConfig(t *testing.T) *Config {
	t.Helper()
	configPath, err := FindConfig("")
	if err != nil {
		t.Skipf("cannot find openkitty-config.yaml: %v", err)
	}
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Skipf("cannot load config %s: %v", configPath, err)
	}
	return config
}

func selectMiniMaxTestConfig(t *testing.T, config *Config) (apiKey, baseURL, model string) {
	t.Helper()
	for _, name := range []string{"minimax_model", "minimax"} {
		if cfg, ok := config.ModelConfig[name]; ok {
			if cfg.APIKey == "" {
				t.Skipf("%s API key not configured", name)
			}
			return cfg.APIKey, strings.TrimRight(cfg.BaseURL, "/"), cfg.ModelName
		}
	}
	t.Skip("minimax_model not found in openkitty-config.yaml model_cfg")
	return "", "", ""
}
