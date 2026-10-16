package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mac/internal/configvalue"
)

// PluginType classifies what kind of plugin this is.
// Claude Code borrowed: plugin architecture concept
type PluginType string

const (
	PluginAgent     PluginType = "agent"
	PluginHook      PluginType = "hook"
	PluginMCP       PluginType = "mcp"
	PluginSkill     PluginType = "skill"
	PluginTool      PluginType = "tool"
	PluginComposite PluginType = "composite"
)

// PluginInfo describes a registered plugin.
type PluginInfo struct {
	Name        string                 `json:"name"`
	Type        PluginType             `json:"type"`
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	Author      string                 `json:"author,omitempty"`
	Enabled     bool                   `json:"enabled"`
	Source      string                 `json:"source"` // file path or MCP server name
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// PluginRegistry manages all discovered plugins.
type PluginRegistry struct {
	mu      sync.RWMutex
	plugins map[string]*PluginInfo // name -> plugin
	baseDir string
}

func NewPluginRegistry(baseDir string) *PluginRegistry {
	dir := filepath.Join(baseDir, ".openkitty", "plugins")
	os.MkdirAll(dir, 0755)
	return &PluginRegistry{
		plugins: make(map[string]*PluginInfo),
		baseDir: dir,
	}
}

// Register adds a plugin to the registry.
func (pr *PluginRegistry) Register(info *PluginInfo) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	info.Enabled = true
	pr.plugins[info.Name] = info
	log.Printf("[PLUGIN] Registered: %s (%s)", info.Name, info.Type)
}

// Unregister removes a plugin.
func (pr *PluginRegistry) Unregister(name string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	delete(pr.plugins, name)
}

// Enable sets a plugin as active.
func (pr *PluginRegistry) Enable(name string) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	p, ok := pr.plugins[name]
	if !ok {
		return fmt.Errorf("plugin '%s' not found", name)
	}
	p.Enabled = true
	return nil
}

// Disable sets a plugin as inactive.
func (pr *PluginRegistry) Disable(name string) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	p, ok := pr.plugins[name]
	if !ok {
		return fmt.Errorf("plugin '%s' not found", name)
	}
	p.Enabled = false
	return nil
}

// Get returns a plugin by name.
func (pr *PluginRegistry) Get(name string) *PluginInfo {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.plugins[name]
}

// List returns all plugins, optionally filtered by type.
func (pr *PluginRegistry) List(filterType PluginType) []*PluginInfo {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	var result []*PluginInfo
	for _, p := range pr.plugins {
		if filterType == "" || p.Type == filterType {
			result = append(result, p)
		}
	}
	return result
}

// ListEnabled returns all enabled plugins.
func (pr *PluginRegistry) ListEnabled() []*PluginInfo {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	var result []*PluginInfo
	for _, p := range pr.plugins {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result
}

// Status returns a structured status report.
func (pr *PluginRegistry) Status() map[string]interface{} {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	status := map[string]interface{}{
		"total":   len(pr.plugins),
		"enabled": 0,
		"by_type": make(map[PluginType]int),
	}
	enabled := 0
	byType := status["by_type"].(map[PluginType]int)
	for _, p := range pr.plugins {
		if p.Enabled {
			enabled++
		}
		byType[p.Type]++
	}
	status["enabled"] = enabled
	return status
}

// DiscoverPlugins scans the plugin directory for plugin manifests.
func (pr *PluginRegistry) DiscoverPlugins() (int, error) {
	entries, err := os.ReadDir(pr.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".json" {
			continue
		}

		filePath := filepath.Join(pr.baseDir, entry.Name())
		plugins, err := pr.parsePluginManifest(filePath)
		if err != nil {
			log.Printf("[PLUGIN] Failed to parse '%s': %v", entry.Name(), err)
			continue
		}
		for _, p := range plugins {
			pr.Register(p)
			count++
		}
	}

	return count, nil
}

// parsePluginManifest reads a JSON plugin manifest.
func (pr *PluginRegistry) parsePluginManifest(filePath string) ([]*PluginInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Try array first
	var plugins []*PluginInfo
	if err := json.Unmarshal(data, &plugins); err == nil && len(plugins) > 0 {
		for _, p := range plugins {
			p.Source = filePath
		}
		return plugins, nil
	}

	// Try single plugin
	var single PluginInfo
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	if single.Name == "" {
		single.Name = strings.TrimSuffix(filepath.Base(filePath), ".json")
	}
	single.Source = filePath
	return []*PluginInfo{&single}, nil
}

// GenerateExamplePlugin creates an example plugin manifest.
func (pr *PluginRegistry) GenerateExamplePlugin() error {
	examplePath := filepath.Join(pr.baseDir, "example-plugin.json")
	if _, err := os.Stat(examplePath); err == nil {
		return nil
	}

	example := PluginInfo{
		Name:        "my-custom-tool",
		Type:        PluginTool,
		Version:     "1.0.0",
		Description: "A custom tool plugin example",
		Author:      "your-name",
		Metadata: map[string]interface{}{
			"command": "python my_tool.py",
			"tools":   []string{"my_custom_tool"},
		},
	}
	data, _ := json.MarshalIndent([]PluginInfo{example}, "", "  ")
	// Also write a README
	readme := `# OpenKitty Plugin Directory

Place plugin manifest files (*.json) here to extend OpenKitty.

## Plugin Types
- **agent**: Custom AI agent definition
- **hook**: Lifecycle hook scripts
- **mcp**: MCP server connection configs
- **skill**: Reusable skill definitions
- **tool**: Custom tool plugins
- **composite**: Multi-type plugins

## Manifest Format
See example-plugin.json for the basic structure.
Each plugin requires: name, type, version, description.
`
	os.WriteFile(filepath.Join(pr.baseDir, "README.md"), []byte(readme), 0644)
	return os.WriteFile(examplePath, data, 0644)
}

// ==================== manage_plugins tool ====================

type ManagePluginsTool struct {
	registry *PluginRegistry
}

func NewManagePluginsTool(registry *PluginRegistry) *ManagePluginsTool {
	return &ManagePluginsTool{registry: registry}
}

func (t *ManagePluginsTool) Name() string { return "manage_plugins" }

func (t *ManagePluginsTool) Description() string {
	return "View and manage plugins: list by type, enable/disable, see status overview. " +
		"Plugins extend OpenKitty with custom agents, hooks, MCP tools, and skills."
}

func (t *ManagePluginsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'list' (all plugins), 'status' (count by type), 'enable', 'disable', 'discover' (scan directory)",
				"enum":        []string{"list", "status", "enable", "disable", "discover"},
			},
			"type_filter": map[string]interface{}{
				"type":        "string",
				"description": "Filter by plugin type (agent/hook/mcp/skill/tool/composite)",
			},
			"plugin_name": map[string]interface{}{
				"type":        "string",
				"description": "Plugin name (for enable/disable actions)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManagePluginsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "list")
	typeFilter := configvalue.String(params, "type_filter", "")
	pluginName := configvalue.String(params, "plugin_name", "")

	if t.registry == nil {
		return "Plugin registry not available", nil
	}

	switch action {
	case "list":
		var filterType PluginType
		if typeFilter != "" {
			filterType = PluginType(typeFilter)
		}
		plugins := t.registry.List(filterType)
		if len(plugins) == 0 {
			return "No plugins registered. Place .json manifests in .openkitty/plugins/", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Plugins (%d):\n", len(plugins)))
		for _, p := range plugins {
			icon := "[ON]"
			if !p.Enabled {
				icon = "[OFF]"
			}
			sb.WriteString(fmt.Sprintf("%s **%s** (%s) v%s — %s\n", icon, p.Name, p.Type, p.Version, p.Description))
		}
		return sb.String(), nil

	case "status":
		status := t.registry.Status()
		return fmt.Sprintf("Plugin Registry Status:\n- Total: %v\n- Enabled: %v\n- By Type: %v",
			status["total"], status["enabled"], status["by_type"]), nil

	case "enable":
		if pluginName == "" {
			return "Error: plugin_name required for enable", nil
		}
		if err := t.registry.Enable(pluginName); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Plugin '%s' enabled", pluginName), nil

	case "disable":
		if pluginName == "" {
			return "Error: plugin_name required for disable", nil
		}
		if err := t.registry.Disable(pluginName); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Plugin '%s' disabled", pluginName), nil

	case "discover":
		count, err := t.registry.DiscoverPlugins()
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Discovered %d new plugin(s) from .openkitty/plugins/", count), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}
