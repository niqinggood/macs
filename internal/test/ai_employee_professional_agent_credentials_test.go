package app

import (
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testProfessionalAgentCredentialDeclaration() []ProfessionalAgentModelCredential {
	return []ProfessionalAgentModelCredential{
		{
			Binding: "reasoning",
			Environment: ProfessionalAgentModelCredentialEnvironment{
				APIKey:    []string{"RESEARCH_AGENT_LLM_API_KEY", "RESEARCH_AGENT_COMPAT_API_KEY"},
				BaseURL:   []string{"RESEARCH_AGENT_LLM_BASE_URL"},
				ModelName: []string{"RESEARCH_AGENT_LLM_MODEL"},
			},
			Required: []string{"api_key", "model_name"},
		},
	}
}

func TestResolveProfessionalAgentModelCredentials(t *testing.T) {
	models := map[string]ModelConfig{
		"selected": {
			Type:      "minimax",
			APIKey:    "selected-secret",
			BaseURL:   "https://model.example/v1",
			ModelName: "example-model",
			MaxTokens: 1234,
		},
		"unrelated": {
			APIKey:    "must-not-leak",
			BaseURL:   "https://unrelated.example/v1",
			ModelName: "unrelated-model",
		},
	}
	config := ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{
		"research-agent": {"reasoning": "selected"},
	}}
	parent := map[string]string{"RESEARCH_AGENT_COMPAT_API_KEY": "explicit-parent-secret"}

	resolved, err := resolveProfessionalAgentModelCredentials(
		"research-agent",
		testProfessionalAgentCredentialDeclaration(),
		config,
		models,
		func(name string) (string, bool) { value, ok := parent[name]; return value, ok },
	)
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if got := resolved["RESEARCH_AGENT_LLM_API_KEY"]; got != "selected-secret" {
		t.Fatalf("unexpected selected API key mapping: %q", got)
	}
	if got := resolved["RESEARCH_AGENT_COMPAT_API_KEY"]; got != "explicit-parent-secret" {
		t.Fatalf("explicit parent environment value must have priority, got %q", got)
	}
	if got := resolved["RESEARCH_AGENT_LLM_BASE_URL"]; got != "https://model.example/v1" {
		t.Fatalf("unexpected base URL mapping: %q", got)
	}
	if got := resolved["RESEARCH_AGENT_LLM_MODEL"]; got != "example-model" {
		t.Fatalf("unexpected model mapping: %q", got)
	}
	for name, value := range resolved {
		if strings.Contains(name, "unrelated") || strings.Contains(value, "must-not-leak") || strings.Contains(value, "unrelated") {
			t.Fatalf("unrelated model profile leaked through %s", name)
		}
	}
	if len(resolved) != 4 {
		t.Fatalf("expected exactly four final variables, got %#v", resolved)
	}
}

func TestProfessionalAgentCredentialEnvironmentDoesNotRequireLegacyAllowlist(t *testing.T) {
	t.Setenv("RESEARCH_AGENT_BOUND_API_KEY", "parent-wins")
	manifest := ProfessionalAgentManifest{
		ID: "research-agent",
		ModelCredentials: []ProfessionalAgentModelCredential{{
			Binding: "reasoning",
			Environment: ProfessionalAgentModelCredentialEnvironment{
				APIKey:    []string{"RESEARCH_AGENT_BOUND_API_KEY"},
				BaseURL:   []string{"RESEARCH_AGENT_BOUND_BASE_URL"},
				ModelName: []string{"RESEARCH_AGENT_BOUND_MODEL"},
			},
			Required: []string{"api_key", "base_url", "model_name"},
		}},
	}
	registry := &ProfessionalAgentRegistry{
		credentialSource: func() (ProfessionalAgentsConfig, map[string]ModelConfig) {
			return ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{
					"research-agent": {"reasoning": "selected"},
				}}, map[string]ModelConfig{
					"selected": {
						APIKey: "profile-value", BaseURL: "https://model.example/v1", ModelName: "example-model",
					},
				}
		},
	}
	resolved, err := registry.resolveModelCredentialEnvironment(manifest)
	if err != nil {
		t.Fatalf("resolve model credential environment: %v", err)
	}
	environment := professionalAgentEnvironment(nil, resolved, manifest.ID, t.TempDir())
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			values[name] = value
		}
	}
	if got := values["RESEARCH_AGENT_BOUND_API_KEY"]; got != "parent-wins" {
		t.Fatalf("parent override was not forwarded: %q", got)
	}
	if got := values["RESEARCH_AGENT_BOUND_BASE_URL"]; got != "https://model.example/v1" {
		t.Fatalf("bound base URL was not forwarded: %q", got)
	}
	if got := values["RESEARCH_AGENT_BOUND_MODEL"]; got != "example-model" {
		t.Fatalf("bound model was not forwarded: %q", got)
	}
	if _, leaked := values["OPENAI_API_KEY"]; leaked {
		t.Fatal("generic host credential unexpectedly entered the child environment")
	}
}

func TestResolveProfessionalAgentModelCredentialsMissingRequiredIsSecretFree(t *testing.T) {
	config := ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{
		"research-agent": {"reasoning": "incomplete"},
	}}
	models := map[string]ModelConfig{
		"incomplete": {APIKey: "super-secret-value", ModelName: ""},
	}

	resolved, err := resolveProfessionalAgentModelCredentials(
		"research-agent", testProfessionalAgentCredentialDeclaration(), config, models, nil,
	)
	if err == nil {
		t.Fatalf("expected missing required field error, got %#v", resolved)
	}
	message := err.Error()
	if !strings.Contains(message, "missing required field model_name") {
		t.Fatalf("unexpected error: %s", message)
	}
	if strings.Contains(message, "super-secret-value") {
		t.Fatal("credential value leaked in error")
	}
}

func TestResolveProfessionalAgentModelCredentialsRejectsDangerousTargets(t *testing.T) {
	tests := []struct {
		name    string
		agentID string
		target  string
	}{
		{name: "outside scope path", agentID: "research-agent", target: "PATH"},
		{name: "outside agent scope", agentID: "research-agent", target: "OTHER_AGENT_API_KEY"},
		{name: "python control namespace", agentID: "python", target: "PYTHON_API_KEY"},
		{name: "openkitty control namespace", agentID: "openkitty", target: "OPENKITTY_API_KEY"},
		{name: "invalid syntax", agentID: "research-agent", target: "RESEARCH_AGENT_bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			declarations := []ProfessionalAgentModelCredential{{
				Binding: "reasoning",
				Environment: ProfessionalAgentModelCredentialEnvironment{
					APIKey: []string{tt.target},
				},
			}}
			config := ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{
				tt.agentID: {"reasoning": "selected"},
			}}
			_, err := resolveProfessionalAgentModelCredentials(tt.agentID, declarations, config, map[string]ModelConfig{
				"selected": {APIKey: "secret"},
			}, nil)
			if err == nil {
				t.Fatalf("expected target %q to be rejected", tt.target)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("credential value leaked in validation error: %s", err)
			}
		})
	}
}

func TestResolveProfessionalAgentModelCredentialsNoConfigurationIsCompatible(t *testing.T) {
	models := map[string]ModelConfig{"selected": {APIKey: "secret", ModelName: "model"}}
	tests := []struct {
		name         string
		declarations []ProfessionalAgentModelCredential
		config       ProfessionalAgentsConfig
	}{
		{name: "no declaration", config: ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{"research-agent": {"reasoning": "selected"}}}},
		{name: "no agent binding", declarations: testProfessionalAgentCredentialDeclaration()},
		{name: "no named binding", declarations: testProfessionalAgentCredentialDeclaration(), config: ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{"research-agent": {"sentiment": "selected"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolveProfessionalAgentModelCredentials("research-agent", tt.declarations, tt.config, models, nil)
			if err != nil {
				t.Fatalf("unexpected compatibility error: %v", err)
			}
			if len(resolved) != 0 {
				t.Fatalf("expected no credential injection, got %#v", resolved)
			}
		})
	}
}

func TestProfessionalAgentModelBindingsParseFromHostConfig(t *testing.T) {
	var config Config
	err := yaml.Unmarshal([]byte(`
professional_agents:
  model_bindings:
    research-agent:
      reasoning: selected
model_cfg:
  selected:
    type: minimax
    api_key: test-secret
    base_url: https://model.example/v1
    model_name: example-model
`), &config)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if got := config.ProfessionalAgents.ModelBindings["research-agent"]["reasoning"]; got != "selected" {
		t.Fatalf("unexpected parsed binding: %q", got)
	}
}

func TestQuantTraderManifestUsesOnlyScopedModelCredentialTargets(t *testing.T) {
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewProfessionalAgentRegistry(workspace, log.New(io.Discard, "", 0))
	manifest, _, err := registry.loadManifest("quant-trader")
	if err != nil {
		t.Fatalf("load quant-trader manifest: %v", err)
	}
	resolved, err := resolveProfessionalAgentModelCredentials(
		manifest.ID,
		manifest.ModelCredentials,
		ProfessionalAgentsConfig{ModelBindings: map[string]map[string]string{
			"quant-trader": {"reasoning": "selected"},
		}},
		map[string]ModelConfig{
			"selected": {APIKey: "test-value", BaseURL: "https://model.example/v1", ModelName: "test-model"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("resolve quant-trader manifest credentials: %v", err)
	}
	want := []string{
		"QUANT_TRADER_MINIMAX_API_KEY",
		"QUANT_TRADER_MINIMAX_CN_API_KEY",
		"QUANT_TRADER_OPENAI_API_KEY",
		"QUANT_TRADER_MINIMAX_BASE_URL",
		"QUANT_TRADER_OPENAI_API_BASE",
		"QUANT_TRADER_MINIMAX_MODEL",
	}
	if len(resolved) != len(want) {
		t.Fatalf("resolved variables = %v, want %d scoped targets", sortedCredentialKeys(resolved), len(want))
	}
	for _, name := range want {
		if strings.TrimSpace(resolved[name]) == "" {
			t.Fatalf("missing scoped target %s", name)
		}
	}
	for name := range resolved {
		if !strings.HasPrefix(name, "QUANT_TRADER_") {
			t.Fatalf("unscoped variable entered quant-trader environment: %s", name)
		}
	}
}

func sortedCredentialKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
