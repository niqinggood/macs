package app

import (
	"testing"

	developmenttools "mac/internal/tools/development"
)

func TestDebugToolGateTriggersBothConfigReloadPaths(t *testing.T) {
	oldConfig := &Config{}
	oldConfig.ToolsConfig.Enabled = true
	newConfig := &Config{}
	newConfig.ToolsConfig.Enabled = true
	newConfig.ToolsConfig.DisabledTools = []string{"debug"}

	if !debugToolGateChanged(oldConfig, newConfig) {
		t.Fatal("debug gate change was not detected")
	}
	if !(&GatewayService{}).needUpdateEngine(oldConfig, newConfig) {
		t.Fatal("gateway reload path did not detect the debug gate change")
	}
	if !(&ClawBot{}).needUpdateEngine(oldConfig, newConfig) {
		t.Fatal("process config watcher did not detect the debug gate change")
	}
}

func TestSyncDebugToolGateUpdatesExistingAgentRegistry(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(developmenttools.NewDebugTool())
	agent := &LLMAgent{name: "Assistant", toolRegistry: registry}
	engine := &MultiAgentEngine{agentFactory: &AgentFactory{
		agents: map[string]Agent{"Assistant": agent},
	}}
	config := &Config{Agents: []AgentConfig{{Name: "Assistant"}}}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.DisabledTools = []string{"debug"}

	engine.syncDebugToolGate(config)
	if _, exists := registry.Get("debug"); exists {
		t.Fatal("debug tool remained registered after disabling it")
	}

	config.ToolsConfig.DisabledTools = nil
	engine.syncDebugToolGate(config)
	if _, exists := registry.Get("debug"); !exists {
		t.Fatal("debug tool was not restored after enabling it")
	}

	config.Agents[0].DisallowedTools = []string{"debug"}
	engine.syncDebugToolGate(config)
	if _, exists := registry.Get("debug"); exists {
		t.Fatal("global enable must not override an agent-level debug denial")
	}
}
