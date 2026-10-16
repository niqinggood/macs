package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/configvalue"
)

func TestLoadConfigParseErrorIncludesResolvedPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	if err := os.WriteFile(configPath, []byte("team_name: [broken\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig should reject malformed YAML")
	}
	var parseErr *configvalue.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected ConfigParseError, got %T: %v", err, err)
	}
	wantPath, _ := filepath.Abs(configPath)
	if parseErr.Path != filepath.Clean(wantPath) {
		t.Fatalf("parse error path = %q, want %q", parseErr.Path, wantPath)
	}
	if !strings.Contains(err.Error(), filepath.Clean(wantPath)) {
		t.Fatalf("error does not include exact config path: %v", err)
	}
}

func TestLoadOrSetupConfigBacksUpMalformedYAMLAndRegeneratesDefault(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	broken := []byte("team_name: [broken\n")
	if err := os.WriteFile(configPath, broken, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if valid, reason := ValidateConfig(config); !valid {
		t.Fatalf("regenerated config is invalid: %s", reason)
	}

	assertCorruptConfigBackup(t, configPath, broken)
	recreated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recreated, defaultConfigYAML) {
		t.Fatal("regenerated YAML does not match the embedded default")
	}
}

func TestLoadOrSetupConfigBacksUpMalformedJSONAndRegeneratesJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.json")
	broken := []byte(`{"team_name":`)
	if err := os.WriteFile(configPath, broken, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("startup JSON recovery failed: %v", err)
	}
	if valid, reason := ValidateConfig(config); !valid {
		t.Fatalf("regenerated JSON config is invalid: %s", reason)
	}

	assertCorruptConfigBackup(t, configPath, broken)
	recreated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(recreated) {
		t.Fatalf("regenerated config is not valid JSON: %s", recreated)
	}
}

func TestLoadOrSetupConfigBacksUpSemanticInvalidConfigAndStartsWebRepair(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	broken := []byte("team_name: still-parseable\nmodel_config: {}\nagents: []\n")
	if err := os.WriteFile(configPath, broken, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("semantic startup recovery failed: %v", err)
	}
	if valid, reason := ValidateConfig(config); !valid {
		t.Fatalf("Web-repair config is invalid: %s", reason)
	}
	if err := validateConfig(config); err != nil {
		t.Fatalf("Web-repair config cannot initialize the engine: %v", err)
	}
	if !config.Gateway.Enabled || !config.Gateway.EnableWebUI {
		t.Fatalf("recovered config must start the Web repair surface: %#v", config.Gateway)
	}
	assertCorruptConfigBackup(t, configPath, broken)
}

func TestLoadOrSetupConfigMissingFileCreatesWebConfigWithoutPrompt(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "openkitty-config.yaml")
	config, err := LoadOrSetupConfig(configPath)
	if err != nil {
		t.Fatalf("missing config should generate a Web-enabled template: %v", err)
	}
	if config == nil || !config.Gateway.Enabled || !config.Gateway.EnableWebUI {
		t.Fatalf("generated config must enable Web settings: %#v", config)
	}
	if config.ServiceRoles == nil {
		t.Fatal("generated config must expose an empty service_roles list")
	}
	if config.Gateway.AgentOSListenAddr != "" {
		t.Fatalf(
			"generated AgentOS listener must remain disabled by default: %q",
			config.Gateway.AgentOSListenAddr,
		)
	}
	if err := validateConfig(config); err != nil {
		t.Fatalf("generated config cannot initialize the engine: %v", err)
	}
	generated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("generated config not saved at resolved path: %v", err)
	}
	for _, field := range []string{
		"service_roles: []",
		"agent_os_listen_addr: \"\"",
	} {
		if !strings.Contains(string(generated), field) {
			t.Fatalf("generated config does not expose %q:\n%s", field, generated)
		}
	}
	if matches, err := filepath.Glob(configPath + ".broken-*.bak*"); err != nil || len(matches) != 0 {
		t.Fatalf("missing config must not create a fake broken backup: matches=%v err=%v", matches, err)
	}
}

func TestEmbeddedDefaultConfigExposesAgentOSDeploymentFields(t *testing.T) {
	content := string(defaultConfigYAML)
	for _, field := range []string{
		"\nservice_roles: []\n",
		"\n    agent_os_listen_addr: \"\"\n",
	} {
		if !strings.Contains(content, field) {
			t.Fatalf("embedded default config does not expose %q", strings.TrimSpace(field))
		}
	}
}

func TestSaveConfigKeepsEmptyAgentOSDeploymentFields(t *testing.T) {
	config := generateTemplateConfig()
	config.ServiceRoles = nil
	config.Gateway.AgentOSListenAddr = ""
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatalf("save config: %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	for _, field := range []string{
		"service_roles: []",
		"agent_os_listen_addr: \"\"",
	} {
		if !strings.Contains(string(content), field) {
			t.Fatalf("saved config does not expose %q:\n%s", field, content)
		}
	}
}

func TestLoadOrSetupConfigDoesNotOverwriteUnsupportedConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.txt")
	original := []byte("keep me exactly\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadOrSetupConfig(configPath)
	if err == nil || config != nil {
		t.Fatalf("unsupported config should fail without recovery: config=%#v err=%v", config, err)
	}
	if !strings.Contains(err.Error(), configvalue.ResolvePath(configPath)) {
		t.Fatalf("error must print the exact config path: %v", err)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("unsupported config was overwritten: got=%q want=%q", got, original)
	}
	if matches, globErr := filepath.Glob(configPath + ".broken-*.bak*"); globErr != nil || len(matches) != 0 {
		t.Fatalf("unsafe read failure must not create a backup/replacement: matches=%v err=%v", matches, globErr)
	}
}

func TestGatewayListenAddrWithPortSurvivesStartupRecoveryOverride(t *testing.T) {
	tests := []struct {
		name string
		addr string
		port string
		want string
	}{
		{name: "ipv4", addr: "127.0.0.1:8080", port: "18091", want: "127.0.0.1:18091"},
		{name: "all interfaces", addr: ":8080", port: "18092", want: ":18092"},
		{name: "host without port", addr: "localhost", port: "18093", want: "localhost:18093"},
		{name: "ipv6", addr: "[::1]:8080", port: "18094", want: "[::1]:18094"},
		{name: "empty", addr: "", port: "18095", want: "127.0.0.1:18095"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := gatewayListenAddrWithPort(test.addr, test.port)
			if err != nil || got != test.want {
				t.Fatalf("gatewayListenAddrWithPort(%q, %q) = %q, %v; want %q", test.addr, test.port, got, err, test.want)
			}
		})
	}
	for _, port := range []string{"", "0", "65536", "not-a-port"} {
		if _, err := gatewayListenAddrWithPort("127.0.0.1:8080", port); err == nil {
			t.Fatalf("invalid port %q should fail", port)
		}
	}
}

func assertCorruptConfigBackup(t *testing.T, configPath string, want []byte) {
	t.Helper()
	matches, err := filepath.Glob(configPath + ".broken-*.bak*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup count = %d, want 1: %v", len(matches), matches)
	}
	got, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("backup content changed: got %q, want %q", got, want)
	}
}
