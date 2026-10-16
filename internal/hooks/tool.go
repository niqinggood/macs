package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookManageTool provides LLM access to the hook system.
// Wraps both the legacy HookManager (shell scripts) and HookManagerV2.
type HookManageTool struct {
	hookManager   *HookManager
	hookManagerV2 *HookManagerV2
	workspace     string
}

func NewHookManageTool(hm *HookManager, workspace string) *HookManageTool {
	return &HookManageTool{
		hookManager:   hm,
		hookManagerV2: GetGlobalHookManagerV2(),
		workspace:     workspace,
	}
}

func (t *HookManageTool) Name() string { return "hook" }

func (t *HookManageTool) Description() string {
	return "Manage automated behaviors via hooks. " +
		"Actions: list (show all hooks), add (create a hook script), " +
		"run (manually trigger a hook event), info (show hooks directory). " +
		"Hooks can be shell scripts in .openkitty/hooks/ or configured in settings. " +
		"Events: PreToolUse, PostToolUse, PostToolFailure, BeforeModel, AfterModel, " +
		"SessionStart, SessionEnd, UserPromptSubmit, PreCompact, PostCompact, Stop, Error."
}

func (t *HookManageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "add", "run", "info"},
				"description": "list: show hooks. add: create hook script. run: trigger event. info: show config.",
			},
			"event": map[string]interface{}{
				"type":        "string",
				"description": "Hook event: pre_tool_use, post_tool_use, pre_compact, post_compact, session_start, stop",
			},
			"script": map[string]interface{}{
				"type":        "string",
				"description": "Shell script content (for add action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *HookManageTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := toolStringParam(params, "action", "list")

	switch action {
	case "list":
		var sb strings.Builder
		sb.WriteString("## Registered Hooks\n\n")

		// Legacy shell-script hooks
		registered := t.hookManager.ListRegistered()
		if len(registered) > 0 {
			sb.WriteString("### Shell Script Hooks\n\n")
			for _, ht := range registered {
				scriptPath := t.hookManager.FindHookScript(ht)
				info, _ := os.Stat(scriptPath)
				size := ""
				if info != nil {
					size = fmt.Sprintf(" (%d bytes)", info.Size())
				}
				sb.WriteString(fmt.Sprintf("- **%s** — `%s`%s\n", ht, scriptPath, size))
			}
		}

		// V2 configured hooks
		if t.hookManagerV2 != nil {
			v2Count := t.hookManagerV2.ConfigCount()
			if v2Count > 0 {
				sb.WriteString("\n### Configured Hooks (settings.json)\n\n")
				for event, configs := range t.hookManagerV2.SnapshotConfig().Enabled {
					for _, c := range configs {
						detail := ""
						switch c.Type {
						case HookExecShell:
							detail = fmt.Sprintf("script: %s", c.Command)
						case HookExecHTTP:
							detail = fmt.Sprintf("url: %s", c.URL)
						case HookExecCallback:
							detail = fmt.Sprintf("callback: %s", c.Command)
						default:
							detail = fmt.Sprintf("type: %s", c.Type)
						}
						sb.WriteString(fmt.Sprintf("- **%s** → %s (%s)\n", event, c.Type, detail))
					}
				}
			}
		}

		totalCount := len(registered)
		if t.hookManagerV2 != nil {
			totalCount += t.hookManagerV2.ConfigCount()
		}
		if totalCount == 0 {
			sb.WriteString("No hooks registered.\n\n")
			sb.WriteString("### Shell Script Hooks\n\n")
			sb.WriteString("Create scripts in `.openkitty/hooks/`:\n")
			sb.WriteString("- `pre_tool_use.sh` — before tool execution\n")
			sb.WriteString("- `post_tool_use.sh` — after tool execution\n")
			sb.WriteString("- `session_start.sh` — when session begins\n")
			sb.WriteString("- `stop.sh` — when session ends\n\n")
			sb.WriteString("### Configured Hooks\n\n")
			sb.WriteString("Add to `settings.json`:\n")
			sb.WriteString("```json\n{\"hooks\": {\"enabled\": {\"PreToolUse\": [{\"type\": \"shell\", \"command\": \".openkitty/hooks/pre_tool_use.sh\"}]}}}\n```")
		}
		sb.WriteString(fmt.Sprintf("\n**%d hook(s)** in `%s`\n", totalCount, t.hookManager.HooksDir()))
		return sb.String(), nil

	case "add":
		event := toolStringParam(params, "event", "")
		script := toolStringParam(params, "script", "")
		if event == "" || script == "" {
			return "Error: 'event' and 'script' are required for add action.", nil
		}

		// Map event name to filename
		eventMap := map[string]string{
			"pre_tool_use":  "pre_tool_use.sh",
			"post_tool_use": "post_tool_use.sh",
			"pre_compact":   "pre_compact.sh",
			"post_compact":  "post_compact.sh",
			"session_start": "session_start.sh",
			"stop":          "stop.sh",
		}

		filename, ok := eventMap[event]
		if !ok {
			return fmt.Sprintf("Unknown event: %s. Valid: %s", event, strings.Join(eventKeys(eventMap), ", ")), nil
		}

		scriptPath := filepath.Join(t.hookManager.HooksDir(), filename)
		if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
			return "", fmt.Errorf("write hook script: %w", err)
		}

		return fmt.Sprintf("✅ Hook script created: `%s`\n\nEvent: %s\n\nScript:\n```bash\n%s\n```",
			scriptPath, event, script), nil

	case "run":
		event := toolStringParam(params, "event", "")
		if event == "" {
			return "Error: 'event' is required for run action.", nil
		}

		switch event {
		case "pre_tool_use":
			result, _ := t.hookManager.RunPreToolUse("test", "", "test_tool", nil)
			return renderResult("pre_tool_use", result), nil
		case "post_tool_use":
			result, _ := t.hookManager.RunPostToolUse("test", "", "test_tool", nil, "ok", nil)
			return renderResult("post_tool_use", result), nil
		case "pre_compact":
			result, _ := t.hookManager.RunPreCompact("test")
			return renderResult("pre_compact", result), nil
		case "session_start":
			result, _ := t.hookManager.RunSessionStart("test", "")
			return renderResult("session_start", result), nil
		case "stop":
			result, _ := t.hookManager.RunStop("test", "")
			return renderResult("stop", result), nil
		default:
			return fmt.Sprintf("Unknown event: %s", event), nil
		}

	case "info":
		var sb strings.Builder
		sb.WriteString("## Hook System Info\n\n")
		sb.WriteString(fmt.Sprintf("**Hooks directory:** `%s`\n\n", t.hookManager.HooksDir()))
		sb.WriteString("### How Hooks Work\n\n")
		sb.WriteString("1. Place a shell script (`.sh`) in the hooks directory named after the event\n")
		sb.WriteString("2. The script receives a JSON context via stdin with event details\n")
		sb.WriteString("3. Exit code 0 = allow, non-zero = block\n\n")
		sb.WriteString("### JSON Context Format\n\n")
		sb.WriteString("```json\n")
		sb.WriteString(`{"hook_type":"pre_tool_use","agent_name":"...","tool_name":"...","params":{...},"timestamp":...}`)
		sb.WriteString("\n```\n\n")
		sb.WriteString("### Output Options\n\n")
		sb.WriteString("- No output = default allow\n")
		sb.WriteString("- Plain text = displayed as message\n")
		sb.WriteString("- JSON `{\"allowed\":false,\"message\":\"...\"}` = block with reason\n")
		sb.WriteString("- JSON `{\"allowed\":true,\"modified\":true,\"new_params\":{...}}` = modify params\n")

		return sb.String(), nil
	}

	return "Unknown action", nil
}

func renderResult(name string, result *HookExecutionResult) string {
	if result == nil {
		return fmt.Sprintf("## Hook: `%s`\n\nNo result.", name)
	}
	status := "✅ Allowed"
	if !result.Allowed {
		status = "🚫 Blocked"
	}
	msg := fmt.Sprintf("## Hook: `%s`\n\n**Status:** %s\n\n", name, status)
	if result.Message != "" {
		msg += fmt.Sprintf("**Message:** %s\n", result.Message)
	}
	if result.Modified {
		msg += "**Modified:** params were modified by hook\n"
	}
	return msg
}

func eventKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func toolStringParam(params map[string]interface{}, key, fallback string) string {
	if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
