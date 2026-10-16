package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mac/internal/toolcontract"
)

type Registry interface {
	ListNames() []string
	Get(name string) (toolcontract.Tool, bool)
}

// CapabilitiesTool lists all available tools grouped by category.
type CapabilitiesTool struct {
	registry Registry
}

func NewCapabilitiesTool(registry Registry) *CapabilitiesTool {
	return &CapabilitiesTool{registry: registry}
}

func (t *CapabilitiesTool) Name() string { return "capabilities" }

func (t *CapabilitiesTool) Description() string {
	return "List all available tools and capabilities grouped by category. " +
		"Use this to discover what the system can do before starting a task."
}

func (t *CapabilitiesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *CapabilitiesTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.registry == nil {
		return "Tool registry not available", nil
	}

	categories := map[string][]string{
		"File Operations":       {"read_file", "write_file", "edit_file", "json_patch", "list_dir", "glob", "grep", "snapshot"},
		"Shell & Execution":     {"cli", "shell_sessions", "persistent_shell", "code_check"},
		"Web & Network":         {"web_fetch", "web_search", "agent_reach", "social_trending", "platform_search"},
		"Browser Automation":    {"computer", "browser_task", "web_targets", "web_navigate", "web_page", "web_close", "web_replay"},
		"Desktop & Local Apps":  {"computer", "desktop_task", "watch", "screenshot", "camera"},
		"Git & Version Control": {"git", "git_pr", "git_pr_review"},
		"Code Intelligence":     {"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols", "lsp_call_hierarchy", "code_review"},
		"Binary Inspection":     {"binary"},
		"Testing":               {"run_tests", "fix_and_retry"},
		"AI & Agents":           {"assemble_team", "swarm", "external_agent", "sub_agent"},
		"Knowledge & RAG":       {"knowledge_query", "search_history", "rag_search", "rag_extract"},
		"Planning & Tasks":      {"plan_task", "update_plan_step", "enter_plan_mode", "exit_plan_mode", "todo_write"},
		"Diagnostics & Status":  {"doctor", "system_status", "circuit_breaker"},
		"Automation":            {"cron_task", "notify", "prompt_template"},
		"Management":            {"manage_hooks", "manage_agents", "manage_plugins", "session_context"},
		"File History":          {"undo_snapshot"},
		"Worktree Isolation":    {"enter_worktree", "exit_worktree"},
		"Multimedia":            {"speak", "listen", "watch", "camera", "screenshot", "audio"},
		"Documents":             {"extract", "convert", "markdown_to_pdf"},
	}

	allNames := t.registry.ListNames()
	registered := make(map[string]bool)
	for _, name := range allNames {
		registered[name] = true
	}

	var sb strings.Builder
	sb.WriteString("## OpenKitty Capabilities\n\n")
	totalListed := 0

	catNames := make([]string, 0, len(categories))
	for cat := range categories {
		catNames = append(catNames, cat)
	}
	sort.Strings(catNames)

	for _, cat := range catNames {
		tools := categories[cat]
		var available []string
		for _, name := range tools {
			if registered[name] {
				available = append(available, name)
			}
		}
		if len(available) > 0 {
			sb.WriteString(fmt.Sprintf("### %s (%d)\n", cat, len(available)))
			for _, name := range available {
				if tool, ok := t.registry.Get(name); ok {
					sb.WriteString(fmt.Sprintf("- **%s**: %s\n", name, tool.Description()))
				}
			}
			sb.WriteString("\n")
			totalListed += len(available)
		}
	}

	sb.WriteString(fmt.Sprintf("---\n**%d tools available** across %d categories.\n", totalListed, len(catNames)))
	return sb.String(), nil
}
