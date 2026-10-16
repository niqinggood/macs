package routing

import "testing"

func TestModelRequiresAPIKeyUsesProviderAndParsedHost(t *testing.T) {
	tests := []struct {
		name, provider, endpoint string
		required                 bool
	}{
		{"ollama", " OLLAMA ", "http://models.example.test/v1", false},
		{"vllm", "vllm", "http://models.example.test/v1", false},
		{"localhost", "openai", "http://localhost:11434/v1", false},
		{"localhost casing", "openai", "http://LOCALHOST.:11434/v1", false},
		{"ipv4", "openai", "http://127.0.0.1:8000/v1", false},
		{"ipv4 loopback range", "openai", "http://127.0.0.2:8000/v1", false},
		{"ipv6", "openai", "http://[::1]:8000/v1", false},
		{"cloud", "openai", "https://api.example.test/v1", true},
		{"lan is not anonymous by default", "openai", "http://192.0.2.1:8000/v1", true},
		{"hostname substring", "openai", "https://localhost.example.test/v1", true},
		{"ip hostname substring", "openai", "https://127.0.0.1.example.test/v1", true},
		{"provider substring", "not-ollama", "https://api.example.test/v1", true},
		{"provider name in host", "openai", "https://ollama.example.test/v1", true},
		{"localhost in path", "openai", "https://api.example.test/localhost/v1", true},
		{"localhost in query", "openai", "https://api.example.test/v1?host=localhost", true},
		{"localhost in userinfo", "openai", "https://localhost@api.example.test/v1", true},
		{"invalid url", "openai", "http://[::1", true},
		{"missing scheme", "openai", "localhost:11434/v1", true},
		{"wrong scheme", "openai", "ftp://localhost/v1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelRequiresAPIKey(ModelCandidate{Type: tt.provider, BaseURL: tt.endpoint}); got != tt.required {
				t.Fatalf("ModelRequiresAPIKey = %v, want %v", got, tt.required)
			}
		})
	}
}
