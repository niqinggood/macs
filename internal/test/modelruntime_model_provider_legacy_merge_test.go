package app

import (
	"strings"
	"testing"
)

func TestMergeLegacyModelConfigIntoProvidersUsesExplicitReference(t *testing.T) {
	configured := []ModelProvider{{
		Name:           "minimax",
		Label:          "MiniMax",
		Type:           "openai",
		ModelConfigRef: "minimax_user_test",
		Models: []ProviderModel{{
			Name:       "MiniMax-M2.7",
			Capability: "text-to-text",
		}},
	}}
	legacy := map[string]ModelConfig{
		"minimax_model": {
			Type:      "minimax",
			BaseURL:   "https://stale.example.invalid/v1",
			APIKey:    "stale-key-that-must-not-win",
			ModelName: "minimax-m3",
		},
		"minimax_user_test": {
			Type:      "minimax",
			BaseURL:   "https://api.minimaxi.com/v1",
			APIKey:    "sk-valid-minimax-test-key",
			ModelName: "minimax-m3",
		},
	}

	effective, diagnostics := mergeLegacyModelConfigIntoProviders(configured, legacy)
	if len(effective) != 1 {
		t.Fatalf("effective providers = %d, want 1", len(effective))
	}
	provider := effective[0]
	if provider.BaseURL != "https://api.minimaxi.com/v1" {
		t.Fatalf("base_url = %q", provider.BaseURL)
	}
	if provider.APIKey != "sk-valid-minimax-test-key" {
		t.Fatal("expected credential from the explicitly referenced legacy config")
	}
	if provider.Type != "openai" {
		t.Fatalf("explicit provider type was overwritten: %q", provider.Type)
	}
	if len(provider.Models) != 1 || provider.Models[0].Name != "MiniMax-M2.7" {
		t.Fatalf("explicit models were overwritten: %#v", provider.Models)
	}
	if !providerAvailable(&provider) {
		t.Fatalf("provider should be available: %s", providerUnavailableReason(&provider))
	}
	if configured[0].BaseURL != "" || configured[0].APIKey != "" {
		t.Fatal("merge mutated the configured provider input")
	}
	if len(diagnostics) == 0 || !strings.Contains(strings.Join(diagnostics, "\n"), "minimax_user_test") {
		t.Fatalf("missing non-secret inheritance diagnostic: %v", diagnostics)
	}
}

func TestMergeLegacyModelConfigIntoProvidersReplacesPlaceholderCredential(t *testing.T) {
	configured := []ModelProvider{{
		Name:           "minimax",
		Type:           "minimax",
		BaseURL:        "https://api.minimaxi.com/v1",
		APIKey:         "YOUR_MINIMAX_API_KEY_HERE",
		ModelConfigRef: "minimax_model",
		Models:         []ProviderModel{{Name: "minimax-m3", Capability: "text-to-text"}},
	}}
	legacy := map[string]ModelConfig{
		"minimax_model": {
			Type:      "minimax",
			BaseURL:   "https://api.minimaxi.com/v1",
			APIKey:    "sk-valid-minimax-test-key",
			ModelName: "minimax-m3",
		},
	}

	effective, _ := mergeLegacyModelConfigIntoProviders(configured, legacy)
	if got := effective[0].APIKey; got != "sk-valid-minimax-test-key" {
		t.Fatalf("placeholder provider credential was not replaced from the matching legacy config: %q", got)
	}
	if !providerAvailable(&effective[0]) {
		t.Fatalf("provider should be available after placeholder inheritance: %s", providerUnavailableReason(&effective[0]))
	}
}

func TestMergeLegacyModelConfigIntoProvidersDoesNotCrossEndpointOrigins(t *testing.T) {
	configured := []ModelProvider{{
		Name:           "vendor-proxy",
		Type:           "openai",
		BaseURL:        "https://proxy.example.com/v1",
		ModelConfigRef: "vendor_legacy",
		Models:         []ProviderModel{{Name: "vendor-chat", Capability: "text-to-text"}},
	}}
	legacy := map[string]ModelConfig{
		"vendor_legacy": {
			Type:      "openai",
			BaseURL:   "https://api.vendor.example/v1",
			APIKey:    "sk-valid-vendor-secret",
			ModelName: "vendor-chat",
		},
	}

	effective, _ := mergeLegacyModelConfigIntoProviders(configured, legacy)
	if effective[0].APIKey != "" {
		t.Fatal("credential was copied to a different endpoint origin")
	}
	if reason := providerUnavailableReason(&effective[0]); reason != "api_key missing or placeholder" {
		t.Fatalf("unavailable reason = %q", reason)
	}
}

func TestMergeLegacyModelConfigIntoProvidersAutoMatchesUniqueCanonicalName(t *testing.T) {
	configured := []ModelProvider{{Name: "deepseek", Type: "deepseek"}}
	legacy := map[string]ModelConfig{
		"deepseek_model": {
			Type:      "deepseek",
			BaseURL:   "https://api.deepseek.com/v1",
			APIKey:    "sk-valid-deepseek-key",
			ModelName: "deepseek-chat",
			MaxTokens: 8192,
		},
	}

	effective, _ := mergeLegacyModelConfigIntoProviders(configured, legacy)
	provider := effective[0]
	if provider.BaseURL == "" || provider.APIKey == "" || len(provider.Models) != 1 {
		t.Fatalf("unique canonical match was not inherited: %#v", provider)
	}
	if provider.Models[0].Name != "deepseek-chat" || provider.Models[0].Capability != "text-to-text" {
		t.Fatalf("legacy model conversion = %#v", provider.Models)
	}
}

func TestMergeLegacyModelConfigIntoProvidersRefusesAmbiguousInference(t *testing.T) {
	configured := []ModelProvider{{
		Name:   "minimax",
		Type:   "openai",
		Models: []ProviderModel{{Name: "MiniMax-M2.7", Capability: "text-to-text"}},
	}}
	legacy := map[string]ModelConfig{
		"minimax_model": {
			BaseURL:   "https://one.example/v1",
			APIKey:    "sk-valid-key-one",
			ModelName: "minimax-m3",
		},
		"minimax-model": {
			BaseURL:   "https://two.example/v1",
			APIKey:    "sk-valid-key-two",
			ModelName: "minimax-m3",
		},
	}

	effective, diagnostics := mergeLegacyModelConfigIntoProviders(configured, legacy)
	if effective[0].BaseURL != "" || effective[0].APIKey != "" {
		t.Fatalf("ambiguous legacy entries should not be guessed: %#v", effective[0])
	}
	if !strings.Contains(strings.Join(diagnostics, "\n"), "ambiguous canonical-name matches") {
		t.Fatalf("expected ambiguity diagnostic, got %v", diagnostics)
	}
}

func TestProviderUnavailableReasonRejectsInvalidModelsAndWhitespaceKey(t *testing.T) {
	provider := &ModelProvider{
		Name:    "invalid",
		Type:    "openai",
		BaseURL: "https://api.example.com/v1",
		APIKey:  "            ",
		Models:  []ProviderModel{{}},
	}
	if reason := providerUnavailableReason(provider); reason != "models missing" {
		t.Fatalf("invalid model reason = %q", reason)
	}
	provider.Models = []ProviderModel{{Name: "chat", Capability: "text-to-text"}}
	if reason := providerUnavailableReason(provider); reason != "api_key missing or placeholder" {
		t.Fatalf("whitespace key reason = %q", reason)
	}
}
