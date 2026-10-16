package development

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mac/internal/configvalue"
)

// ==================== lsp_diagnostics tool ====================

type LSPDiagnosticsTool struct {
	manager *LSPManager
}

func NewLSPDiagnosticsTool(manager *LSPManager) *LSPDiagnosticsTool {
	return &LSPDiagnosticsTool{manager: manager}
}

func (t *LSPDiagnosticsTool) Name() string { return "lsp_diagnostics" }

func (t *LSPDiagnosticsTool) Description() string {
	return "Get diagnostics (errors, warnings, hints) for a file from the language server. " +
		"Shows actual compiler/linter errors at each line. Use this to find bugs before building."
}

func (t *LSPDiagnosticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath": map[string]interface{}{"type": "string", "description": "File to check"},
		},
		"required": []string{"filePath"},
	}
}

type LSPDiagnostic struct {
	Range    LSPRange    `json:"range"`
	Severity int         `json:"severity"` // 1=error, 2=warning, 3=info, 4=hint
	Message  string      `json:"message"`
	Source   string      `json:"source,omitempty"`
	Code     interface{} `json:"code,omitempty"`
}

func (t *LSPDiagnosticsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	if filePath == "" {
		return "Error: filePath is required", nil
	}

	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}

	diags, err := client.GetDiagnostics(filePath)
	if err != nil {
		return fmt.Sprintf("Diagnostics failed: %v", err), nil
	}

	if len(diags) == 0 {
		return fmt.Sprintf("## Diagnostics: `%s`\n\n✅ No issues found.", filePath), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Diagnostics: `%s`\n\n", filePath))

	// Count by severity
	errors, warnings, infos, hints := 0, 0, 0, 0
	for _, d := range diags {
		switch d.Severity {
		case 1:
			errors++
		case 2:
			warnings++
		case 3:
			infos++
		case 4:
			hints++
		}
	}
	sb.WriteString(fmt.Sprintf("🔴 %d errors | 🟡 %d warnings | ℹ️ %d info | 💡 %d hints\n\n", errors, warnings, infos, hints))

	// Group by severity
	currentSeverity := -1
	for _, d := range diags {
		if d.Severity != currentSeverity {
			currentSeverity = d.Severity
			switch currentSeverity {
			case 1:
				sb.WriteString("### Errors\n\n")
			case 2:
				sb.WriteString("### Warnings\n\n")
			case 3:
				sb.WriteString("### Info\n\n")
			case 4:
				sb.WriteString("### Hints\n\n")
			}
		}
		line := d.Range.Start.Line + 1 // LSP uses 0-based lines, we display 1-based
		char := d.Range.Start.Character
		sev := severityLabel(d.Severity)
		sb.WriteString(fmt.Sprintf("%s **Line %d, Col %d**: %s\n", sev, line, char, d.Message))
		if d.Source != "" {
			sb.WriteString(fmt.Sprintf("  *Source: %s*\n", d.Source))
		}
	}

	sb.WriteString(fmt.Sprintf("\n**Summary:** %d issue(s) total\n", len(diags)))

	return sb.String(), nil
}

// ==================== lsp_code_action tool ====================

type LSPCodeActionTool struct {
	manager *LSPManager
}

func NewLSPCodeActionTool(manager *LSPManager) *LSPCodeActionTool {
	return &LSPCodeActionTool{manager: manager}
}

func (t *LSPCodeActionTool) Name() string { return "lsp_code_action" }

func (t *LSPCodeActionTool) Description() string {
	return "Get available code actions (quick fixes) from the language server for a position or range. " +
		"Examples: auto-fix imports, extract function, add error handling."
}

func (t *LSPCodeActionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":      map[string]interface{}{"type": "string", "description": "File path"},
			"line":          map[string]interface{}{"type": "integer", "description": "Line number (0-based)"},
			"character":     map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
			"end_line":      map[string]interface{}{"type": "integer", "description": "End line for range (optional)"},
			"end_character": map[string]interface{}{"type": "integer", "description": "End character for range (optional)"},
		},
		"required": []string{"filePath", "line", "character"},
	}
}

type LSPCodeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind,omitempty"`
	IsPreferred bool            `json:"isPreferred,omitempty"`
	Edit        json.RawMessage `json:"edit,omitempty"`
}

func (t *LSPCodeActionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	line := configvalue.Int(params, "line", 0)
	character := configvalue.Int(params, "character", 0)
	endLine := configvalue.Int(params, "end_line", line)
	endChar := configvalue.Int(params, "end_character", character)

	if filePath == "" {
		return "Error: filePath is required", nil
	}

	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}

	actions, err := client.GetCodeActions(filePath, line, character, endLine, endChar)
	if err != nil {
		return fmt.Sprintf("Code actions failed: %v", err), nil
	}

	if len(actions) == 0 {
		return fmt.Sprintf("## Code Actions: `%s:%d:%d`\n\nNo code actions available at this position.", filePath, line+1, character), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Code Actions: `%s:%d:%d`\n\n", filePath, line+1, character))
	sb.WriteString(fmt.Sprintf("**%d action(s) available:**\n\n", len(actions)))

	for i, a := range actions {
		pref := ""
		if a.IsPreferred {
			pref = " ⭐ (preferred)"
		}
		kind := ""
		if a.Kind != "" {
			kind = fmt.Sprintf(" [%s]", a.Kind)
		}
		sb.WriteString(fmt.Sprintf("%d. **%s**%s%s\n", i+1, a.Title, kind, pref))
	}

	return sb.String(), nil
}

// ==================== lsp_completion tool ====================

type LSPCompletionTool struct {
	manager *LSPManager
}

func NewLSPCompletionTool(manager *LSPManager) *LSPCompletionTool {
	return &LSPCompletionTool{manager: manager}
}

func (t *LSPCompletionTool) Name() string { return "lsp_completion" }

func (t *LSPCompletionTool) Description() string {
	return "Get code completion suggestions from the language server at a position. " +
		"Returns available symbols, methods, and keywords with type information."
}

func (t *LSPCompletionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "File path"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
		},
		"required": []string{"filePath", "line", "character"},
	}
}

type LSPCompletionItem struct {
	Label      string `json:"label"`
	Kind       int    `json:"kind,omitempty"`
	Detail     string `json:"detail,omitempty"`
	InsertText string `json:"insertText,omitempty"`
	SortText   string `json:"sortText,omitempty"`
}

func (t *LSPCompletionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	line := configvalue.Int(params, "line", 0)
	character := configvalue.Int(params, "character", 0)

	if filePath == "" {
		return "Error: filePath is required", nil
	}

	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}

	items, err := client.GetCompletion(filePath, line, character)
	if err != nil {
		return fmt.Sprintf("Completion failed: %v", err), nil
	}

	if len(items) == 0 {
		return fmt.Sprintf("## Completion: `%s:%d:%d`\n\nNo completions available at this position.", filePath, line+1, character), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Completion: `%s:%d:%d`\n\n", filePath, line+1, character))
	sb.WriteString(fmt.Sprintf("**%d suggestion(s):**\n\n", len(items)))
	sb.WriteString("| Symbol | Kind | Detail |\n")
	sb.WriteString("|--------|------|--------|\n")

	limit := 20
	for i, item := range items {
		if i >= limit {
			sb.WriteString(fmt.Sprintf("| ... | %d more | ... |\n", len(items)-limit))
			break
		}
		detail := item.Detail
		if len(detail) > 60 {
			detail = detail[:60] + "..."
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", item.Label, completionKind(item.Kind), detail))
	}

	return sb.String(), nil
}

// ==================== lsp_rename tool ====================

type LSPRenameTool struct {
	manager *LSPManager
}

func NewLSPRenameTool(manager *LSPManager) *LSPRenameTool {
	return &LSPRenameTool{manager: manager}
}

func (t *LSPRenameTool) Name() string { return "lsp_rename" }

func (t *LSPRenameTool) Description() string {
	return "Rename a symbol across the entire project. " +
		"Finds all references and applies the rename safely. Use for refactoring."
}

func (t *LSPRenameTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "File path"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character (0-based)"},
			"new_name":  map[string]interface{}{"type": "string", "description": "New name for the symbol"},
		},
		"required": []string{"filePath", "line", "character", "new_name"},
	}
}

func (t *LSPRenameTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	line := configvalue.Int(params, "line", 0)
	character := configvalue.Int(params, "character", 0)
	newName := configvalue.String(params, "new_name", "")

	if filePath == "" || newName == "" {
		return "Error: filePath and new_name are required", nil
	}

	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}

	changes, err := client.Rename(filePath, line, character, newName)
	if err != nil {
		return fmt.Sprintf("Rename failed: %v", err), nil
	}

	if len(changes) == 0 {
		return "No references found to rename.", nil
	}

	var sb strings.Builder
	totalEdits := 0
	sb.WriteString(fmt.Sprintf("## Rename: → `%s`\n\n", newName))
	sb.WriteString("| File | Edits |\n")
	sb.WriteString("|------|-------|\n")

	for file, edits := range changes {
		sb.WriteString(fmt.Sprintf("| `%s` | %d |\n", file, len(edits)))
		totalEdits += len(edits)
	}
	sb.WriteString(fmt.Sprintf("\n**%d edits across %d files**\n", totalEdits, len(changes)))
	sb.WriteString("\nUse `edit_file` to apply these changes.\n")

	return sb.String(), nil
}

// ==================== Helper functions ====================

func severityLabel(s int) string {
	switch s {
	case 1:
		return "🔴"
	case 2:
		return "🟡"
	case 3:
		return "ℹ️"
	case 4:
		return "💡"
	default:
		return "  "
	}
}

func completionKind(k int) string {
	kinds := map[int]string{
		1:  "Text",
		2:  "Method",
		3:  "Function",
		4:  "Constructor",
		5:  "Field",
		6:  "Variable",
		7:  "Class",
		8:  "Interface",
		9:  "Module",
		10: "Property",
		11: "Unit",
		12: "Value",
		13: "Enum",
		14: "Keyword",
		15: "Snippet",
		16: "Color",
		17: "File",
		18: "Reference",
		19: "Folder",
		20: "EnumMember",
		21: "Constant",
		22: "Struct",
		23: "Event",
		24: "Operator",
		25: "TypeParameter",
	}
	if s, ok := kinds[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind%d", k)
}

// ==================== LSPClient enhanced methods ====================

func (c *LSPClient) GetDiagnostics(filePath string) ([]LSPDiagnostic, error) {
	if err := c.syncDocumentFromDisk(filePath, true); err != nil {
		return nil, err
	}
	uri := c.fileURI(filePath)
	result, pullErr := c.sendRequestWithTimeout("textDocument/diagnostic", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	}, 3*time.Second)
	if pullErr == nil {
		var response struct {
			Items []LSPDiagnostic `json:"items"`
		}
		if err := json.Unmarshal(result, &response); err == nil {
			c.storeDiagnostics(uri, response.Items)
			return response.Items, nil
		}
	}

	// Servers that implement push diagnostics may publish just after didSave.
	// Wait briefly for the reader loop instead of fabricating an empty change.
	deadline := time.NewTimer(750 * time.Millisecond)
	defer deadline.Stop()
	for {
		if diagnostics, ok := c.cachedDiagnostics(uri); ok {
			return diagnostics, nil
		}
		select {
		case <-c.diagNotify:
			continue
		case <-deadline.C:
			if pullErr != nil {
				return nil, pullErr
			}
			return nil, nil
		case <-c.done:
			return nil, c.transportError()
		}
	}
}

func (c *LSPClient) GetCodeActions(filePath string, line, character, endLine, endCharacter int) ([]LSPCodeAction, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": c.fileURI(filePath),
		},
		"range": map[string]interface{}{
			"start": map[string]int{"line": line, "character": character},
			"end":   map[string]int{"line": endLine, "character": endCharacter},
		},
		"context": map[string]interface{}{
			"diagnostics": []interface{}{},
		},
	}

	result, err := c.sendRequest("textDocument/codeAction", params)
	if err != nil {
		return nil, err
	}

	var actions []LSPCodeAction
	if err := json.Unmarshal(result, &actions); err != nil {
		// Try as map response
		var container struct {
			Actions []LSPCodeAction `json:"actions"`
		}
		json.Unmarshal(result, &container)
		return container.Actions, nil
	}
	return actions, nil
}

func (c *LSPClient) GetCompletion(filePath string, line, character int) ([]LSPCompletionItem, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": c.fileURI(filePath),
		},
		"position": map[string]int{
			"line":      line,
			"character": character,
		},
	}

	result, err := c.sendRequest("textDocument/completion", params)
	if err != nil {
		return nil, err
	}

	// LSP returns CompletionList or CompletionItem[]
	var response struct {
		Items      []LSPCompletionItem `json:"items"`
		IsComplete bool                `json:"isIncomplete"`
	}
	if err := json.Unmarshal(result, &response); err == nil && len(response.Items) > 0 {
		return response.Items, nil
	}

	// Try as direct array
	var items []LSPCompletionItem
	if err := json.Unmarshal(result, &items); err == nil {
		return items, nil
	}

	// Fallback: return empty (no completions available)
	return nil, nil
}

func (c *LSPClient) Rename(filePath string, line, character int, newName string) (map[string][]interface{}, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": c.fileURI(filePath),
		},
		"position": map[string]int{
			"line":      line,
			"character": character,
		},
		"newName": newName,
	}

	result, err := c.sendRequest("textDocument/rename", params)
	if err != nil {
		return nil, err
	}

	// Response is WorkspaceEdit -> changes: map[uri][]TextEdit
	var edit struct {
		Changes map[string][]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(result, &edit); err != nil {
		return nil, err
	}

	changes := make(map[string][]interface{})
	for uri, edits := range edit.Changes {
		var parsed []interface{}
		for _, e := range edits {
			parsed = append(parsed, e)
		}
		changes[uri] = parsed
	}
	return changes, nil
}
