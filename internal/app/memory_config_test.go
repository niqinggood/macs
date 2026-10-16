package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mac/internal/meetingminutes"
	memorycore "mac/internal/memory"
)

func TestMemoryConfigDefaultsAndExplicitDisable(t *testing.T) {
	for _, test := range []struct {
		name, extension, contents string
		enabled                   bool
	}{
		{"yaml_missing", ".yaml", "team_name: fixture\n", true},
		{"yaml_section_missing_flag", ".yaml", "memory_config:\n  memory_window: 42\n", true},
		{"yaml_disabled", ".yaml", "memory_config:\n  enabled: false\n", false},
		{"json_missing", ".json", `{"team_name":"fixture"}`, true},
		{"json_disabled", ".json", `{"memory_config":{"enabled":false}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config"+test.extension)
			if err := os.WriteFile(path, []byte(test.contents), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadConfig(path)
			if err != nil || config.MemoryConfig.Enabled != test.enabled {
				t.Fatalf("memory default: enabled=%v err=%v", config, err)
			}
			if err := SaveConfig(path, config); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig(path)
			if err != nil || loaded.MemoryConfig.Enabled != test.enabled {
				t.Fatalf("memory setting changed after save: enabled=%v err=%v", loaded, err)
			}
		})
	}
}

func TestMemoryConfigMergePreservesWindowAndStorage(t *testing.T) {
	config := &Config{}
	config.MemoryConfig.Enabled = true
	config.MemoryConfig.MemoryWindow = 73
	config.MemoryConfig.BaseDir = "fixture-memories"
	config.MemoryConfig.EmbedMode = "auto"
	if err := mergeFullConfig(config, map[string]interface{}{
		"memory_config": map[string]interface{}{"enabled": false},
	}); err != nil {
		t.Fatal(err)
	}
	if config.MemoryConfig.Enabled || config.MemoryConfig.MemoryWindow != 73 ||
		config.MemoryConfig.BaseDir != "fixture-memories" || config.MemoryConfig.EmbedMode != "auto" {
		t.Fatal("changing the master switch changed unrelated memory settings")
	}
	if err := mergeFullConfig(config, map[string]interface{}{"team_name": "updated"}); err != nil {
		t.Fatal(err)
	}
	if config.MemoryConfig.Enabled {
		t.Fatal("an unrelated settings save re-enabled memory")
	}
}

func TestMemoryConfigReloadDetection(t *testing.T) {
	enabled := true
	old := &Config{Agents: []AgentConfig{{Name: "Assistant", HasMemory: &enabled}}}
	old.MemoryConfig.Enabled = true
	check := func(next *Config, want bool) {
		t.Helper()
		if (&GatewayService{}).needUpdateEngine(old, next) != want || (&ClawBot{}).needUpdateEngine(old, next) != want {
			t.Fatalf("memory reload detection: want %v", want)
		}
	}
	next := *old
	next.Agents = append([]AgentConfig(nil), old.Agents...)
	equivalent := true
	next.Agents[0].HasMemory = &equivalent
	check(&next, false)
	next.MemoryConfig.Enabled = false
	check(&next, true)
	next.MemoryConfig.Enabled = true
	disabled := false
	next.Agents[0].HasMemory = &disabled
	check(&next, true)
}

func TestMemoryConfigSettingsAPIAppliesNewAgentRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	_, database := newMemoryServiceTestAgentWithDB(t)
	previousDB := db
	db = database
	t.Cleanup(func() { db = previousDB })
	g := webModelSelectionGateway(t, "http://127.0.0.1:18091")
	g.engine.recorder = NewExecutionRecorder(meetingminutes.NewEnhancedMeetingMinutes())
	g.config.MemoryConfig.MemoryWindow = 73
	g.config.MemoryConfig.BaseDir = filepath.Join(g.config.Workspace, "fixture-memories")
	g.config.MemoryConfig.EmbedMode = "none"
	if err := SaveConfig(g.configpath, g.config); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(g.config.Workspace, "saved-memory.txt")
	if err := os.WriteFile(marker, []byte("existing memory"), 0600); err != nil {
		t.Fatal(err)
	}
	previous, _ := g.engine.agentFactory.GetAgent("Assistant")
	savedMemory := false
	for _, enabled := range []bool{true, false, true} {
		data, _ := json.Marshal(map[string]interface{}{"memory_config": map[string]interface{}{"enabled": enabled}})
		request := httptest.NewRequest(http.MethodPut, "/api/config/sections", bytes.NewReader(data))
		response := httptest.NewRecorder()
		g.handleConfigSections(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("save memory setting: %d %s", response.Code, response.Body.String())
		}
		current, ok := g.engine.agentFactory.GetAgent("Assistant")
		if !ok || current == previous {
			t.Fatal("memory settings retained the old cached runtime")
		}
		agent, ok := current.(*LLMAgent)
		if !ok || agent.hasMemory != enabled || (agent.sessionMemory != nil) != enabled {
			t.Fatalf("new Agent did not apply memory setting %v", enabled)
		}
		owner, err := agent.memoryOwner(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !savedMemory {
			if err := agent.memoryService.Record(context.Background(), owner, "switch-fixture", "retained memory", memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
				t.Fatal(err)
			}
			savedMemory = true
		}
		entry, err := agent.memoryService.Lookup(context.Background(), owner, "switch-fixture", memorycore.MemoryUser)
		if err != nil || entry == nil || entry.Value != "retained memory" {
			t.Fatalf("memory switch lost the persisted SQLite entry: %v", err)
		}
		loaded, err := LoadConfig(g.configpath)
		if err != nil || loaded.MemoryConfig.Enabled != enabled || loaded.MemoryConfig.MemoryWindow != 73 ||
			loaded.MemoryConfig.BaseDir != g.config.MemoryConfig.BaseDir || loaded.MemoryConfig.EmbedMode != "none" {
			t.Fatalf("persisted memory configuration changed: %v", err)
		}
		previous = current
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "existing memory" {
		t.Fatal("switching memory cleared saved data")
	}
}
