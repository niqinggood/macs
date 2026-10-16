package app

import (
	"io"
	"log"
	"strings"
	"testing"
)

func TestPreferredModelConfigKeysPreferMiniMaxOverDeepSeek(t *testing.T) {
	configs := map[string]ModelConfig{
		"deepseek_model": {
			Type:      "deepseek",
			APIKey:    "deepseek-key",
			ModelName: "deepseek-chat",
			BaseURL:   "https://api.deepseek.com/v1",
		},
		"minimax_model": {
			Type:      "minimax",
			APIKey:    "minimax-key",
			ModelName: "minimax-m3",
			BaseURL:   "https://api.minimaxi.com/v1",
		},
		"hy3_model": {
			Type:      "openai",
			APIKey:    "openrouter-key",
			ModelName: "tencent/hy3-preview",
			BaseURL:   "https://openrouter.ai/api/v1",
		},
		"openai_model": {
			Type:      "openai",
			ModelName: "gpt-4o",
			BaseURL:   "https://api.openai.com/v1",
		},
	}

	keys := preferredModelConfigKeys(configs)
	if len(keys) != 3 {
		t.Fatalf("expected only configured models, got %v", keys)
	}
	if keys[0] != "minimax_model" {
		t.Fatalf("expected minimax_model first, got %v", keys)
	}
	if keys[1] != "hy3_model" {
		t.Fatalf("expected hy3_model fallback second, got %v", keys)
	}
	if keys[2] != "deepseek_model" {
		t.Fatalf("expected deepseek_model fallback third, got %v", keys)
	}
}

func TestPreferredModelConfigKeysSkipUnavailableMiniMax(t *testing.T) {
	configs := map[string]ModelConfig{
		"deepseek_model": {
			Type:      "deepseek",
			APIKey:    "deepseek-key",
			ModelName: "deepseek-chat",
			BaseURL:   "https://api.deepseek.com/v1",
		},
		"minimax_model": {
			Type:      "minimax",
			APIKey:    "YOUR_MINIMAX_API_KEY_HERE",
			ModelName: "minimax-m3",
			BaseURL:   "https://api.minimaxi.com/v1",
		},
	}

	keys := preferredModelConfigKeys(configs)
	if len(keys) != 1 || keys[0] != "deepseek_model" {
		t.Fatalf("expected unavailable minimax to be skipped, got %v", keys)
	}
}

func TestPreferredModelConfigKeysAllowLocalWithoutAPIKey(t *testing.T) {
	configs := map[string]ModelConfig{
		"ollama_model": {
			Type:      "ollama",
			ModelName: "qwen3",
			BaseURL:   "http://localhost:11434/v1",
		},
	}

	keys := preferredModelConfigKeys(configs)
	if len(keys) != 1 || keys[0] != "ollama_model" {
		t.Fatalf("expected local model without key to be available, got %v", keys)
	}
}

func TestGatewayFirstModelConfigPrefersMiniMax(t *testing.T) {
	g := &GatewayService{
		config: &Config{
			ModelConfig: map[string]ModelConfig{
				"deepseek_model": {
					Type:      "deepseek",
					APIKey:    "deepseek-key",
					ModelName: "deepseek-chat",
					BaseURL:   "https://api.deepseek.com/v1",
				},
				"minimax_model": {
					Type:      "minimax",
					APIKey:    "minimax-key",
					ModelName: "minimax-m3",
					BaseURL:   "https://api.minimaxi.com/v1",
				},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	cfg := g.firstModelConfig()
	if cfg == nil {
		t.Fatal("expected model config")
	}
	if cfg.ModelName != "minimax-m3" {
		t.Fatalf("expected minimax-m3, got %q", cfg.ModelName)
	}
}

func TestGatewayFirstModelConfigFallsBackWhenMiniMaxUnavailable(t *testing.T) {
	g := &GatewayService{
		config: &Config{
			ModelConfig: map[string]ModelConfig{
				"deepseek_model": {
					Type:      "deepseek",
					APIKey:    "deepseek-key",
					ModelName: "deepseek-chat",
					BaseURL:   "https://api.deepseek.com/v1",
				},
				"minimax_model": {
					Type:      "minimax",
					APIKey:    "YOUR_MINIMAX_API_KEY_HERE",
					ModelName: "minimax-m3",
					BaseURL:   "https://api.minimaxi.com/v1",
				},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	cfg := g.firstModelConfig()
	if cfg == nil {
		t.Fatal("expected model config")
	}
	if cfg.ModelName != "deepseek-chat" {
		t.Fatalf("expected deepseek fallback, got %q", cfg.ModelName)
	}
}

func TestProviderRegistryResolveForAEPrefersMiniMaxOverDeepSeek(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"deepseek_model": {
				Name:    "deepseek_model",
				Label:   "DeepSeek",
				Type:    "deepseek",
				BaseURL: "https://api.deepseek.com/v1",
				APIKey:  "deepseek-key",
				Models: []ProviderModel{{
					Name:       "deepseek-chat",
					Capability: "text-to-text",
				}},
			},
			"hy3_model": {
				Name:    "hy3_model",
				Label:   "Tencent HY3 Preview (OpenRouter)",
				Type:    "openai",
				BaseURL: "https://openrouter.ai/api/v1",
				APIKey:  "openrouter-key",
				Models: []ProviderModel{{
					Name:       "tencent/hy3-preview",
					Capability: "text-to-text",
				}},
			},
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	provider, model := reg.ResolveForAE("data-analyst", "text-to-text")
	if provider == nil || model == nil {
		t.Fatal("expected provider and model")
	}
	if provider.Name != "minimax_model" || model.Name != "minimax-m3" {
		t.Fatalf("expected minimax-m3, got provider=%v model=%v", provider.Name, model.Name)
	}
	if provider.APIKey != "minimax-key" {
		t.Fatalf("expected minimax key, got %q", provider.APIKey)
	}
}

func TestProviderRegistryResolveForAESkipsUnavailableMiniMaxToHY3BeforeDeepSeek(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"deepseek_model": {
				Name:    "deepseek_model",
				Label:   "DeepSeek",
				Type:    "deepseek",
				BaseURL: "https://api.deepseek.com/v1",
				APIKey:  "deepseek-key",
				Models: []ProviderModel{{
					Name:       "deepseek-chat",
					Capability: "text-to-text",
				}},
			},
			"hy3_model": {
				Name:    "hy3_model",
				Label:   "Tencent HY3 Preview (OpenRouter)",
				Type:    "openai",
				BaseURL: "https://openrouter.ai/api/v1",
				APIKey:  "openrouter-key",
				Models: []ProviderModel{{
					Name:       "tencent/hy3-preview",
					Capability: "text-to-text",
				}},
			},
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "YOUR_MINIMAX_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	provider, model := reg.ResolveForAE("data-analyst", "text-to-text")
	if provider == nil || model == nil {
		t.Fatal("expected fallback provider and model")
	}
	if provider.Name != "hy3_model" || model.Name != "tencent/hy3-preview" {
		t.Fatalf("expected hy3 fallback before deepseek, got provider=%v model=%v", provider.Name, model.Name)
	}
}

func TestProviderRegistryResolveForAESkipsUnavailableMiniMax(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"deepseek_model": {
				Name:    "deepseek_model",
				Label:   "DeepSeek",
				Type:    "deepseek",
				BaseURL: "https://api.deepseek.com/v1",
				APIKey:  "deepseek-key",
				Models: []ProviderModel{{
					Name:       "deepseek-chat",
					Capability: "text-to-text",
				}},
			},
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "YOUR_MINIMAX_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	provider, model := reg.ResolveForAE("data-analyst", "text-to-text")
	if provider == nil || model == nil {
		t.Fatal("expected fallback provider and model")
	}
	if provider.Name != "deepseek_model" || model.Name != "deepseek-chat" {
		t.Fatalf("expected deepseek fallback, got provider=%v model=%v", provider.Name, model.Name)
	}
}

func TestProviderRegistryExplainResolveForAEShowsFallbackReason(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"deepseek_model": {
				Name:    "deepseek_model",
				Label:   "DeepSeek",
				Type:    "deepseek",
				BaseURL: "https://api.deepseek.com/v1",
				APIKey:  "deepseek-key",
				Models: []ProviderModel{{
					Name:       "deepseek-chat",
					Capability: "text-to-text",
				}},
			},
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "YOUR_MINIMAX_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	info := reg.ExplainResolveForAE("data-analyst", "text-to-text")
	if !info.Available {
		t.Fatalf("expected available route, got %#v", info)
	}
	if info.Provider != "deepseek_model" || info.Model != "deepseek-chat" {
		t.Fatalf("expected deepseek fallback route, got %#v", info)
	}
	if info.Source != "provider_preference" {
		t.Fatalf("expected provider_preference source, got %#v", info)
	}
}

func TestProviderRegistryExplainResolveForAEReportsMissingImageRoute(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	info := reg.ExplainResolveForAE("designer", "text-to-image")
	if info.Available {
		t.Fatalf("image route should be unavailable, got %#v", info)
	}
	if info.Model != "" || info.Provider != "" {
		t.Fatalf("unavailable image route should not expose a text model, got %#v", info)
	}
	if info.Reason == "" {
		t.Fatalf("expected missing route reason, got %#v", info)
	}
}

func TestProviderRegistryExplainResolveForAESkipsNonDefaultLocalProviderPreference(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax": {
				Name:    "minimax",
				Label:   "MiniMax",
				Type:    "openai",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
			"ollama": {
				Name:    "ollama",
				Label:   "Ollama",
				Type:    "local",
				BaseURL: "http://localhost:11434/v1",
				Models: []ProviderModel{{
					Name:       "llama3.2-vision",
					Capability: "multi-modal",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	info := reg.ExplainResolveForAE("meeting-secretary", "multi-modal")
	if info.Available {
		t.Fatalf("non-default local provider should not be used by provider_preference, got %#v", info)
	}
	if info.Model == "llama3.2-vision" || info.Provider == "ollama" {
		t.Fatalf("unexpected local model route: %#v", info)
	}
	if !strings.Contains(info.Reason, "explicit model binding") {
		t.Fatalf("reason should mention explicit binding, got %#v", info)
	}
}

func TestProviderRegistryExplicitBindingRejectsMissingAPIKey(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax": {
				Name:    "minimax",
				Label:   "MiniMax",
				Type:    "openai",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "YOUR_MINIMAX_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	info := reg.ExplainResolveForAEBinding("meeting-secretary", `{"text-to-text":"minimax-m3"}`, "text-to-text")
	if info.Available {
		t.Fatalf("missing API key route should not be available, got %#v", info)
	}
	if strings.Contains(strings.ToLower(info.Reason), "api_key missing or placeholder") == false {
		t.Fatalf("reason should mention missing api key, got %#v", info)
	}
}

func TestProviderRegistryExplicitBindingAllowsLocalProvider(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"ollama": {
				Name:    "ollama",
				Label:   "Ollama",
				Type:    "local",
				BaseURL: "http://localhost:11434/v1",
				Models: []ProviderModel{{
					Name:       "llama3.2-vision",
					Capability: "multi-modal",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	info := reg.ExplainResolveForAEBinding("designer", `{"multi-modal":"llama3.2-vision"}`, "multi-modal")
	if !info.Available || info.Provider != "ollama" || info.Model != "llama3.2-vision" {
		t.Fatalf("explicit binding should allow local provider, got %#v", info)
	}
	if info.Source != "model_binding" {
		t.Fatalf("source = %q, want model_binding", info.Source)
	}
}

func TestProviderBestModelDoesNotFallbackImageToText(t *testing.T) {
	provider := &ModelProvider{
		Name: "minimax",
		Models: []ProviderModel{{
			Name:       "minimax-m3",
			Capability: "text-to-text",
		}},
	}
	if model := provider.BestModel("text-to-image"); model != nil {
		t.Fatalf("text-to-image should not fall back to text model, got %#v", model)
	}
}

func TestProviderRegistryResolveForAETextToImageUsesImageProvider(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
			"doubao_model": {
				Name:    "doubao_model",
				Label:   "Doubao",
				Type:    "volcengine",
				BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
				APIKey:  "doubao-key",
				Models: []ProviderModel{{
					Name:       "doubao-seedream-4-0",
					Capability: "text-to-image",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	provider, model := reg.ResolveForAE("designer", "text-to-image")
	if provider == nil || model == nil {
		t.Fatal("expected image provider and model")
	}
	if provider.Name != "doubao_model" || model.Name != "doubao-seedream-4-0" {
		t.Fatalf("expected doubao image model, got provider=%v model=%v", provider.Name, model.Name)
	}
}

func TestProviderRegistryResolveForAETextToImageSkipsUnavailableImageProvider(t *testing.T) {
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax_model": {
				Name:    "minimax_model",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
			"doubao_model": {
				Name:    "doubao_model",
				Label:   "Doubao",
				Type:    "volcengine",
				BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
				APIKey:  "YOUR_DOUBAO_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "doubao-seedream-4-0",
					Capability: "text-to-image",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	provider, model := reg.ResolveForAE("designer", "text-to-image")
	if provider != nil || model != nil {
		t.Fatalf("expected no available image model, got provider=%v model=%v", provider, model)
	}
}

func TestAgentFactorySubAgentModelResolutionUsesRequestedModelName(t *testing.T) {
	factory := &AgentFactory{
		modelConfigs: map[string]*ModelConfig{
			"deepseek_model": {
				Type:      "deepseek",
				APIKey:    "deepseek-key",
				ModelName: "deepseek-chat",
				BaseURL:   "https://api.deepseek.com/v1",
			},
			"minimax_model": {
				Type:      "minimax",
				APIKey:    "minimax-key",
				ModelName: "minimax-m3",
				BaseURL:   "https://api.minimaxi.com/v1",
			},
		},
	}

	cfg := factory.resolveSubAgentModelConfig("minimax-m3")
	if cfg == nil {
		t.Fatal("expected model config")
	}
	if cfg.APIKey != "minimax-key" {
		t.Fatalf("expected minimax key, got %q", cfg.APIKey)
	}
}

func TestAgentFactorySubAgentModelResolutionSkipsUnavailableRequestedModel(t *testing.T) {
	factory := &AgentFactory{
		modelConfigs: map[string]*ModelConfig{
			"deepseek_model": {
				Type:      "deepseek",
				APIKey:    "deepseek-key",
				ModelName: "deepseek-chat",
				BaseURL:   "https://api.deepseek.com/v1",
			},
			"minimax_model": {
				Type:      "minimax",
				APIKey:    "YOUR_MINIMAX_API_KEY_HERE",
				ModelName: "minimax-m3",
				BaseURL:   "https://api.minimaxi.com/v1",
			},
		},
	}

	cfg := factory.resolveSubAgentModelConfig("minimax-m3")
	if cfg == nil {
		t.Fatal("expected fallback model config")
	}
	if cfg.ModelName != "deepseek-chat" {
		t.Fatalf("expected deepseek fallback, got %q", cfg.ModelName)
	}
}

func TestAgentFactorySubAgentModelResolutionUsesRuntimeProviderConfig(t *testing.T) {
	factory := &AgentFactory{
		modelConfigs: map[string]*ModelConfig{
			"deepseek_model": {
				Type:      "deepseek",
				APIKey:    "deepseek-key",
				ModelName: "deepseek-chat",
				BaseURL:   "https://api.deepseek.com/v1",
			},
		},
	}
	factory.UpsertRuntimeModelConfig("minimax-m3", ModelConfig{
		Type:      "minimax",
		APIKey:    "provider-minimax-key",
		ModelName: "minimax-m3",
		BaseURL:   "https://api.minimaxi.com/v1",
	})

	cfg := factory.resolveSubAgentModelConfig("minimax-m3")
	if cfg == nil {
		t.Fatal("expected runtime model config")
	}
	if cfg.APIKey != "provider-minimax-key" {
		t.Fatalf("expected provider minimax key, got %q", cfg.APIKey)
	}
}
