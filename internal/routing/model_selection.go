package routing

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"mac/internal/security"
)

// ModelCandidate contains the provider fields needed for availability and
// preference routing without coupling the package to application config.
type ModelCandidate struct {
	Type        string
	APIKey      string
	ModelName   string
	BaseURL     string
	AccountAuth bool
}

// preferredModelConfigKeys returns configured models in cost-aware runtime order.
// MiniMax is preferred by default, HY3/OpenRouter is the low-cost fallback,
// while DeepSeek remains available after cheaper text routes.
func PreferredModelKeys(configs map[string]ModelCandidate) []string {
	keys := make([]string, 0, len(configs))
	for key, cfg := range configs {
		if IsModelAvailable(cfg) {
			keys = append(keys, key)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left := ModelPreferenceScore(keys[i], configs[keys[i]])
		right := ModelPreferenceScore(keys[j], configs[keys[j]])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	return keys
}

func IsModelAvailable(cfg ModelCandidate) bool {
	if strings.TrimSpace(cfg.ModelName) == "" {
		return false
	}
	if ModelRequiresAPIKey(cfg) && security.SuspiciousAPIKey(cfg.APIKey) {
		return false
	}
	return true
}

func ModelRequiresAPIKey(cfg ModelCandidate) bool {
	if cfg.AccountAuth {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "ollama", "vllm":
		return false
	}
	endpoint, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

func ModelPreferenceScore(name string, cfg ModelCandidate) int {
	value := strings.ToLower(strings.Join([]string{name, cfg.Type, cfg.ModelName, cfg.BaseURL}, " "))
	switch {
	case strings.Contains(value, "minimax") || strings.Contains(value, "mini-max"):
		return 0
	case strings.Contains(value, "hy3-preview") || strings.Contains(value, "openrouter") || strings.Contains(value, "hunyuan"):
		return 10
	case strings.Contains(value, "qwen") || strings.Contains(value, "tongyi"):
		return 20
	case strings.Contains(value, "doubao") || strings.Contains(value, "volcengine"):
		return 30
	case strings.Contains(value, "gpt-4o-mini"):
		return 40
	case strings.Contains(value, "openai"):
		return 50
	case strings.Contains(value, "deepseek"):
		return 80
	default:
		return 60
	}
}
