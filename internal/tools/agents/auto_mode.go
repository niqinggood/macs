package agents

import (
	"context"
	"fmt"

	"mac/internal/configvalue"
)

type AutoModeSettings struct {
	ReadOnlyToolCount int
	MaxDenials        int
	KeepRecent        int
	MaxContextTokens  int
}

type AutoModeRuntime interface {
	EnableAutoMode() AutoModeSettings
	DisableAutoMode()
	AutoModeStats() map[string]interface{}
	AllowTool(string)
	BlockTool(string)
	ResetAutoModeDenials()
	AutoModeSettings() AutoModeSettings
}

type AutoModeTool struct {
	runtime AutoModeRuntime
}

func NewAutoModeTool(runtime AutoModeRuntime) *AutoModeTool {
	return &AutoModeTool{runtime: runtime}
}

func (t *AutoModeTool) Name() string { return "auto_mode" }

func (t *AutoModeTool) Description() string {
	return "Control auto mode settings. " +
		"Actions: enable (turn on auto-approval for safe tools), " +
		"disable (turn off auto mode), " +
		"stats (show classification statistics), " +
		"allow (add tool to allow list), " +
		"block (add tool to block list), " +
		"compact (show context compaction settings)."
}

func (t *AutoModeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"enable", "disable", "stats", "allow", "block", "compact", "reset"},
				"description": "enable: turn on auto mode. disable: turn off. stats: show stats. allow/block: manage tool lists. compact: show compaction settings. reset: reset denial counter.",
			},
			"tool": map[string]interface{}{
				"type":        "string",
				"description": "Tool name for allow/block actions",
			},
		},
		"required": []string{"action"},
	}
}

func (t *AutoModeTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "Auto mode is not available", nil
	}
	action := configvalue.String(params, "action", "stats")
	toolName := configvalue.String(params, "tool", "")
	switch action {
	case "enable":
		settings := t.runtime.EnableAutoMode()
		return "## Auto Mode\n\n✅ Auto mode enabled. Safe tool calls will be auto-approved.\n\n" +
			fmt.Sprintf("Read-only tools: %d auto-approved.\nWrite tools: auto-approved within workspace.\nDestructive/remote: always prompt.\n", settings.ReadOnlyToolCount) +
			fmt.Sprintf("\nMax denials before fallback: %d\n", settings.MaxDenials), nil
	case "disable":
		t.runtime.DisableAutoMode()
		return "## Auto Mode\n\n⏸️ Auto mode disabled. All tool calls require approval.", nil
	case "stats":
		stats := t.runtime.AutoModeStats()
		return fmt.Sprintf("## Auto Mode Stats\n\n"+
			"| Metric | Value |\n|--------|-------|\n"+
			"| Enabled | %v |\n| Denials | %d/%d |\n"+
			"| Safe approvals | %d |\n| Denied | %d |\n"+
			"| Read-only tools | %d |\n",
			stats["enabled"], stats["denial_count"], stats["max_denials"],
			stats["total_safe"], stats["total_denied"], stats["read_only"]), nil
	case "allow":
		if toolName == "" {
			return "Error: 'tool' parameter required for allow action.", nil
		}
		t.runtime.AllowTool(toolName)
		return fmt.Sprintf("✅ `%s` added to auto-mode allow list.", toolName), nil
	case "block":
		if toolName == "" {
			return "Error: 'tool' parameter required for block action.", nil
		}
		t.runtime.BlockTool(toolName)
		return fmt.Sprintf("🚫 `%s` added to auto-mode block list.", toolName), nil
	case "compact":
		settings := t.runtime.AutoModeSettings()
		return "## Context Compaction\n\n" +
			"Compaction strategies available:\n" +
			"- **Summary:** Replace old messages with LLM summary (for >300K tokens)\n" +
			fmt.Sprintf("- **Truncate:** Keep first 3 + last %d messages (for >200K tokens)\n", settings.KeepRecent) +
			fmt.Sprintf("- **Key Messages:** Keep important messages by content patterns (for >%dK tokens)\n", settings.MaxContextTokens/1000) +
			"\nCompaction triggers automatically when approaching context limit.\n", nil
	case "reset":
		t.runtime.ResetAutoModeDenials()
		return "✅ Denial counter reset.", nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}
