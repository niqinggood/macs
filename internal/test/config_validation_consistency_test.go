package app

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func singleAgentConfigWithoutCollaborationPlan() *Config {
	return &Config{
		Agents: []AgentConfig{{
			Name:  "Assistant",
			Type:  AgentTypeCustom,
			Model: "local_model",
		}},
		ModelConfig: map[string]ModelConfig{
			"local_model": {
				Type:      "ollama",
				ModelName: "qwen-test",
				BaseURL:   "http://127.0.0.1:11434/v1",
			},
		},
	}
}

func TestSingleAgentConfigWithoutCollaborationTasksPassesEveryValidator(t *testing.T) {
	config := singleAgentConfigWithoutCollaborationPlan()
	if valid, reason := ValidateConfig(config); !valid {
		t.Fatalf("startup validator rejected single-Agent chat config: %s", reason)
	}
	if err := validateConfig(config); err != nil {
		t.Fatalf("engine validator rejected single-Agent chat config: %v", err)
	}
	logger := log.New(io.Discard, "", 0)
	if err := (&GatewayService{logger: logger}).validateConfig(config); err != nil {
		t.Fatalf("Gateway validator rejected single-Agent chat config: %v", err)
	}
	if err := (&ClawBot{logger: logger}).validateConfig(config); err != nil {
		t.Fatalf("CLI reload validator rejected single-Agent chat config: %v", err)
	}
	if len(config.Collaboration.Tasks) != 0 {
		t.Fatal("validation must not invent a collaboration plan")
	}
}

func TestServiceClientValidationBindsKnownTenantAndPublishedRole(t *testing.T) {
	config := singleAgentConfigWithoutCollaborationPlan()
	config.Tenants = []TenantConfig{{ID: "tenant-a", Name: "Tenant A"}}
	config.ServiceRoles = []AgentServiceRoleConfig{{
		ID: "customer-research", AgentName: "Assistant", Enabled: true,
		AllowedTenants: []string{"tenant-a"},
	}}
	config.ServiceClients = []AgentServiceClientConfig{{
		ID: "crm-service", TenantID: "tenant-a",
		Token: "synthetic-service-token-123456789", Enabled: true,
		AllowedRoles: []string{"customer-research"},
	}}
	if err := validateConfig(config); err != nil {
		t.Fatalf("valid service identity mapping was rejected: %v", err)
	}

	config.ServiceClients[0].TenantID = "tenant-b"
	if err := validateConfig(config); err == nil || !strings.Contains(err.Error(), "unknown tenant") {
		t.Fatalf("unknown service tenant should be rejected, got %v", err)
	}
	config.ServiceClients[0].TenantID = "tenant-a"
	config.ServiceClients[0].AllowedRoles = []string{"unpublished-role"}
	if err := validateConfig(config); err == nil || !strings.Contains(err.Error(), "unpublished role") {
		t.Fatalf("unpublished service role should be rejected, got %v", err)
	}
	config.ServiceClients[0].AllowedRoles = []string{"customer-research"}
	config.ServiceClients[0].Token = "too-short"
	if err := validateConfig(config); err == nil || !strings.Contains(err.Error(), "at least 24") {
		t.Fatalf("weak service credential should be rejected, got %v", err)
	}
}

func TestLoadOrSetupConfigKeepsValidSingleAgentFileByteForByte(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	original := []byte(`# compact user config; startup must not rewrite this file
agents:
  - name: Assistant
    type: custom
    model: local_model
model_cfg:
  local_model:
    type: ollama
    model_name: qwen-test
    base_url: http://127.0.0.1:11434/v1
`)
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("single-Agent startup config should load: %v", err)
	}
	if valid, reason := ValidateConfig(loaded); !valid {
		t.Fatalf("loaded config should remain valid: %s", reason)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("startup rewrote a valid compact config:\n%s", after)
	}
	if backups, globErr := filepath.Glob(configPath + ".broken-*.bak*"); globErr != nil || len(backups) != 0 {
		t.Fatalf("valid config must not be backed up as broken: %v, %v", backups, globErr)
	}
}

func TestConfiguredStartupDoesNotAttemptInstallationSideEffects(t *testing.T) {
	if !shouldAttemptSelfInstall([]string{"openkitty.exe"}) {
		t.Fatal("argument-free first launch should retain the install flow")
	}
	for _, args := range [][]string{
		{"openkitty.exe", "--config", `E:\project\openkitty-config.yaml`},
		{"openkitty.exe", "--path", `E:\project`},
		{"openkitty.exe", "--mcp"},
	} {
		if shouldAttemptSelfInstall(args) {
			t.Fatalf("configured/runtime launch must not touch PATH or desktop shortcuts: %#v", args)
		}
	}
}
