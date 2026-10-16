package app

import (
	"context"
	"strings"
	"testing"

	knowledgetools "mac/internal/tools/knowledge"
)

func TestManagePluginsList(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	pr.Register(&knowledgetools.PluginInfo{Name: "p1", Type: knowledgetools.PluginTool, Version: "1.0", Description: "tool plugin"})
	pr.Register(&knowledgetools.PluginInfo{Name: "p2", Type: knowledgetools.PluginAgent, Version: "2.0", Description: "agent plugin"})

	tool := knowledgetools.NewManagePluginsTool(pr)

	// List all
	result, err := tool.Execute(nil, map[string]interface{}{"action": "list"})
	if err != nil || result == "" {
		t.Fatal("list should return result")
	}
	if !strings.Contains(result, "p1") || !strings.Contains(result, "p2") {
		t.Error("should list all plugins")
	}

	// Filter by type
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "list", "type_filter": "tool"})
	if !strings.Contains(result, "p1") || strings.Contains(result, "p2") {
		t.Error("type filter should only show tool plugins")
	}

	// Status
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "status"})
	if !strings.Contains(result, "2") {
		t.Error("status should show total count")
	}
}

func TestManagePluginsEnableDisable(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	pr.Register(&knowledgetools.PluginInfo{Name: "test", Type: knowledgetools.PluginTool, Version: "1.0", Description: "test"})

	tool := knowledgetools.NewManagePluginsTool(pr)

	// Disable
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "disable", "plugin_name": "test"})
	if !strings.Contains(result, "disabled") {
		t.Error("should disable plugin")
	}
	if pr.Get("test").Enabled {
		t.Error("plugin should be disabled")
	}

	// Enable
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "enable", "plugin_name": "test"})
	if !strings.Contains(result, "enabled") {
		t.Error("should enable plugin")
	}
	if !pr.Get("test").Enabled {
		t.Error("plugin should be enabled")
	}
}

func TestManagePluginsDiscover(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	// Create example plugin
	pr.GenerateExamplePlugin()

	tool := knowledgetools.NewManagePluginsTool(pr)
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "discover"})
	if !strings.Contains(result, "Discovered") {
		t.Error("discover should find plugins")
	}
}

func TestPluginRegistryListByType(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	pr.Register(&knowledgetools.PluginInfo{Name: "agent1", Type: knowledgetools.PluginAgent, Version: "1.0", Description: "a"})
	pr.Register(&knowledgetools.PluginInfo{Name: "tool1", Type: knowledgetools.PluginTool, Version: "1.0", Description: "t"})
	pr.Register(&knowledgetools.PluginInfo{Name: "hook1", Type: knowledgetools.PluginHook, Version: "1.0", Description: "h"})

	if len(pr.List("")) != 3 {
		t.Error("should have 3 plugins total")
	}
	if len(pr.List(knowledgetools.PluginAgent)) != 1 {
		t.Error("should have 1 agent plugin")
	}
	if len(pr.List(knowledgetools.PluginTool)) != 1 {
		t.Error("should have 1 tool plugin")
	}
}

func TestPluginStatus(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	pr.Register(&knowledgetools.PluginInfo{Name: "a", Type: knowledgetools.PluginAgent, Version: "1.0", Description: "a"})
	pr.Register(&knowledgetools.PluginInfo{Name: "b", Type: knowledgetools.PluginTool, Version: "1.0", Description: "b"})
	pr.Disable("b")

	status := pr.Status()
	if status["total"].(int) != 2 {
		t.Error("total should be 2")
	}
	if status["enabled"].(int) != 1 {
		t.Error("enabled should be 1")
	}
}

func TestManagePermissionsProjectRulesLifecycle(t *testing.T) {
	workspace := t.TempDir()
	agent := &LLMAgent{
		workspace:        workspace,
		safetyGuard:      &SafetyGuard{PermissionMode: PermissionDefault},
		permissionEngine: NewPermissionEngine(PermissionDefault),
		globalConfig:     &Config{Workspace: workspace},
		toolRegistry:     NewToolRegistry(),
		enabledTools:     nil,
		maxIterations:    1,
		fileLogger:       testLogger,
	}
	tool := &ManagePermissionsTool{agent: agent}

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add_rule",
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("add_rule returned error: %v", err)
	}
	if !strings.Contains(out, "allow cli(go test)") {
		t.Fatalf("add_rule output missing rule: %s", out)
	}
	allowed, confirm, reason := agent.permissionEngine.Check("cli", map[string]interface{}{"command": "go test ./..."})
	if !allowed || confirm || reason != "" {
		t.Fatalf("engine did not apply project rule: allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}

	out, err = tool.Execute(context.Background(), map[string]interface{}{"action": "list_rules"})
	if err != nil {
		t.Fatalf("list_rules returned error: %v", err)
	}
	if !strings.Contains(out, "0. allow cli(go test)") {
		t.Fatalf("list_rules output missing rule: %s", out)
	}

	out, err = tool.Execute(context.Background(), map[string]interface{}{"action": "remove_rule", "index": 0})
	if err != nil {
		t.Fatalf("remove_rule returned error: %v", err)
	}
	if !strings.Contains(out, "Removed project permission rule") {
		t.Fatalf("remove_rule output unexpected: %s", out)
	}
}

func TestManagePermissionsSuggestRule(t *testing.T) {
	agent := &LLMAgent{safetyGuard: &SafetyGuard{PermissionMode: PermissionDefault}, permissionEngine: NewPermissionEngine(PermissionDefault)}
	out, err := (&ManagePermissionsTool{agent: agent}).Execute(context.Background(), map[string]interface{}{
		"action":  "suggest_rule",
		"command": "npm run build",
	})
	if err != nil {
		t.Fatalf("suggest_rule returned error: %v", err)
	}
	if !strings.Contains(out, "cli(npm run build)") || !strings.Contains(out, "build_test") {
		t.Fatalf("unexpected suggest output: %s", out)
	}
}
