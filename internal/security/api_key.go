package security

import "strings"

// SuspiciousAPIKey reports API keys that are empty, obvious placeholders, or
// too short to plausibly authenticate with a remote provider.
func SuspiciousAPIKey(key string) bool {
	if key == "" {
		return true
	}
	lower := strings.ToLower(key)
	for _, pattern := range []string{
		"your_", "your-", "sk-your", "sk-xxx", "sk-xxxxxxxx",
		"your_api_key", "your-api-key", "your-key",
		"placeholder", "change_me", "changeme",
		"api_key_here", "your_token", "your-token",
		"undefined", "null",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return len(key) < 10
}
