package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentLoaderDiscoverAgents(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")
	os.MkdirAll(agentsDir, 0755)

	// Create a sample agent YAML
	yamlContent := `name: test-agent
description: A test agent
role: tester
system_prompt: You are a test agent.
tools:
  - read_file
  - write_file
tier: community
`
	os.WriteFile(filepath.Join(agentsDir, "test-agent.yaml"), []byte(yamlContent), 0644)

	// Create a JSON agent
	jsonContent := `[{"name": "json-agent", "description": "JSON agent", "role": "helper"}]`
	os.WriteFile(filepath.Join(agentsDir, "agents.json"), []byte(jsonContent), 0644)

	// Create a file that should be ignored
	os.WriteFile(filepath.Join(agentsDir, "README.md"), []byte("# Agents"), 0644)

	loader := NewAgentLoader(agentsDir)
	agents, err := loader.DiscoverAgents()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(agents) != 2 {
		t.Errorf("expected 3 agents (1 yaml + 1 json), got %d", len(agents))
	}

	// Verify YAML agent
	found := false
	for _, a := range agents {
		if a.Name == "test-agent" {
			found = true
			if a.Description != "A test agent" {
				t.Errorf("description mismatch: %s", a.Description)
			}
			if len(a.Tools) != 2 {
				t.Errorf("expected 2 tools, got %d", len(a.Tools))
			}
		}
	}
	if !found {
		t.Error("test-agent not found in discovered agents")
	}
}

func TestAgentLoaderEmptyDir(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")
	os.MkdirAll(agentsDir, 0755)

	loader := NewAgentLoader(agentsDir)
	agents, err := loader.DiscoverAgents()
	if err != nil {
		t.Fatalf("discover empty: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents in empty dir, got %d", len(agents))
	}
}

func TestAgentLoaderInvalidFile(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")
	os.MkdirAll(agentsDir, 0755)

	// Write invalid YAML
	os.WriteFile(filepath.Join(agentsDir, "bad.yaml"), []byte("{invalid yaml!!!"), 0644)

	loader := NewAgentLoader(agentsDir)
	_, err := loader.DiscoverAgents()
	if err != nil {
		t.Fatalf("discover with invalid file should not error: %v", err)
	}
	// Should skip the invalid file, result is empty
}

func TestDiscoveredAgentKeepsExecutionDefinition(t *testing.T) {
	da := &DiscoveredAgent{
		Name:         "my-agent",
		Description:  "Does things",
		Role:         "worker",
		SystemPrompt: "Be helpful",
		Tools:        []string{"read_file", "cli"},
		Tier:         "community",
	}

	if da.Name != "my-agent" || da.EffectivePrompt() != "Be helpful" {
		t.Errorf("definition mismatch: %#v", da)
	}
	if len(da.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(da.Tools))
	}
}

func TestAgentLoaderRichSchema(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")
	os.MkdirAll(agentsDir, 0755)

	yamlContent := `name: verifier-lite
description: Verify work
type: verification
when_to_use: after changes
prompt: |
  Verify with evidence.
tools:
  - read_file
  - cli
disallowed_tools:
  - write_file
permission_mode: default
max_turns: 7
skills:
  - go
memory:
  project: true
background: true
model: inherit
`
	os.WriteFile(filepath.Join(agentsDir, "verifier-lite.yaml"), []byte(yamlContent), 0644)

	loader := NewAgentLoader(agentsDir)
	agents, err := loader.DiscoverAgents()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	agent := agents[0]
	if agent.SystemPrompt != "Verify with evidence." {
		t.Fatalf("prompt was not normalized into system prompt: %q", agent.SystemPrompt)
	}
	if agent.AgentType != "verification" || !agent.Background || !agent.Memory.Project {
		t.Fatalf("rich fields not parsed: %#v", agent)
	}
	if len(agent.DisallowedTools) != 1 || agent.DisallowedTools[0] != "write_file" {
		t.Fatalf("disallowed tools not parsed: %#v", agent.DisallowedTools)
	}
	if model := agent.EffectiveModel(); model != "" {
		t.Fatalf("inherit model should become empty model override, got %q", model)
	}
	if iterations := agent.EffectiveMaxIterations(30); iterations != 7 {
		t.Fatalf("max_turns should map to max iterations, got %d", iterations)
	}
	if agent.PermissionMode != "default" {
		t.Fatalf("permission mode not preserved: %q", agent.PermissionMode)
	}
}

func TestAgentLoaderDiscoverAllIncludesBuiltinVerification(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")
	os.MkdirAll(agentsDir, 0755)

	loader := NewAgentLoader(agentsDir)
	agents, err := loader.DiscoverAllAgents()
	if err != nil {
		t.Fatalf("discover all: %v", err)
	}
	found := false
	for _, agent := range agents {
		if agent.Name == "verification" {
			found = true
			if len(agent.Tools) == 0 || len(agent.DisallowedTools) == 0 {
				t.Fatalf("builtin verification missing tool policy: %#v", agent)
			}
			if agent.MaxTurns <= 0 {
				t.Fatalf("builtin verification missing max turns")
			}
		}
	}
	if !found {
		t.Fatal("builtin verification agent not found")
	}
}

func TestValidateAgentSchemaPermissionAndToolConflict(t *testing.T) {
	issues := ValidateAgentSchema(&DiscoveredAgent{
		Name:            "bad",
		Description:     "bad",
		PermissionMode:  "root",
		Tools:           []string{"read_file", "cli"},
		DisallowedTools: []string{"cli"},
	})
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "permission_mode is invalid") {
		t.Fatalf("expected invalid permission mode issue, got %v", issues)
	}
	if !strings.Contains(joined, "tools and disallowed_tools overlap: cli") {
		t.Fatalf("expected overlap issue, got %v", issues)
	}
}

func TestGenerateDefaultAgentFile(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".openkitty", "agents")

	err := GenerateDefaultAgentFile(agentsDir)
	if err != nil {
		t.Fatalf("generate default: %v", err)
	}

	// Should create example file
	examplePath := filepath.Join(agentsDir, "example-agent.yaml")
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		t.Error("example-agent.yaml should exist")
	}

	// Second call should not overwrite
	err = GenerateDefaultAgentFile(agentsDir)
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
}
