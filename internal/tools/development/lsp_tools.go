package development

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/configvalue"
)

// ==================== lsp_definition tool ====================

type LSPDefinitionTool struct {
	manager *LSPManager
}

func NewLSPDefinitionTool(manager *LSPManager) *LSPDefinitionTool {
	return &LSPDefinitionTool{manager: manager}
}

func (t *LSPDefinitionTool) Name() string { return "lsp_definition" }
func (t *LSPDefinitionTool) Description() string {
	return "Go to the definition of a symbol at the given position in a file. Returns file path, line, and character range."
}
func (t *LSPDefinitionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "Absolute or relative path to the file"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
		},
		"required": []string{"filePath", "line", "character"},
	}
}
func (t *LSPDefinitionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
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
	locs, err := client.GoToDefinition(filePath, line, character)
	if err != nil {
		return fmt.Sprintf("Definition lookup failed: %v", err), nil
	}
	if len(locs) == 0 {
		return "No definition found.", nil
	}
	return formatLSPLocations(locs, filePath), nil
}

// ==================== lsp_references tool ====================

type LSPReferencesTool struct {
	manager *LSPManager
}

func NewLSPReferencesTool(manager *LSPManager) *LSPReferencesTool {
	return &LSPReferencesTool{manager: manager}
}

func (t *LSPReferencesTool) Name() string { return "lsp_references" }
func (t *LSPReferencesTool) Description() string {
	return "Find all references to a symbol at the given position. Shows everywhere the symbol is used."
}
func (t *LSPReferencesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "File path"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
		},
		"required": []string{"filePath", "line", "character"},
	}
}
func (t *LSPReferencesTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
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
	locs, err := client.FindReferences(filePath, line, character)
	if err != nil {
		return fmt.Sprintf("References lookup failed: %v", err), nil
	}
	if len(locs) == 0 {
		return "No references found.", nil
	}
	return formatLSPLocations(locs, filePath), nil
}

// ==================== lsp_hover tool ====================

type LSPHoverTool struct {
	manager *LSPManager
}

func NewLSPHoverTool(manager *LSPManager) *LSPHoverTool {
	return &LSPHoverTool{manager: manager}
}

func (t *LSPHoverTool) Name() string { return "lsp_hover" }
func (t *LSPHoverTool) Description() string {
	return "Get documentation/type information for a symbol at the given position (hover)."
}
func (t *LSPHoverTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "File path"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
		},
		"required": []string{"filePath", "line", "character"},
	}
}
func (t *LSPHoverTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
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
	hover, err := client.Hover(filePath, line, character)
	if err != nil {
		return fmt.Sprintf("Hover failed: %v", err), nil
	}
	return hover, nil
}

// ==================== lsp_symbols tool ====================

type LSPSymbolsTool struct {
	manager *LSPManager
}

func NewLSPSymbolsTool(manager *LSPManager) *LSPSymbolsTool {
	return &LSPSymbolsTool{manager: manager}
}

func (t *LSPSymbolsTool) Name() string { return "lsp_symbols" }
func (t *LSPSymbolsTool) Description() string {
	return "List all symbols (functions, classes, variables) in a file with their locations."
}
func (t *LSPSymbolsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath": map[string]interface{}{"type": "string", "description": "File path to analyze"},
		},
		"required": []string{"filePath"},
	}
}
func (t *LSPSymbolsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	if filePath == "" {
		return "Error: filePath is required", nil
	}
	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}
	symbols, err := client.DocumentSymbol(filePath)
	if err != nil {
		return fmt.Sprintf("Symbol lookup failed: %v", err), nil
	}
	if len(symbols) == 0 {
		return "No symbols found in this file.", nil
	}
	return formatDocumentSymbols(symbols, 0), nil
}

// ==================== lsp_call_hierarchy tool ====================

type LSPCallHierarchyTool struct {
	manager *LSPManager
}

func NewLSPCallHierarchyTool(manager *LSPManager) *LSPCallHierarchyTool {
	return &LSPCallHierarchyTool{manager: manager}
}

func (t *LSPCallHierarchyTool) Name() string { return "lsp_call_hierarchy" }
func (t *LSPCallHierarchyTool) Description() string {
	return "Show the call hierarchy for a function: which functions call it (incoming) and which it calls (outgoing)."
}
func (t *LSPCallHierarchyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":  map[string]interface{}{"type": "string", "description": "File path"},
			"line":      map[string]interface{}{"type": "integer", "description": "Line (0-based)"},
			"character": map[string]interface{}{"type": "integer", "description": "Character offset (0-based)"},
			"direction": map[string]interface{}{"type": "string", "description": "'incoming' (callers) or 'outgoing' (callees)", "enum": []string{"incoming", "outgoing"}},
		},
		"required": []string{"filePath", "line", "character", "direction"},
	}
}
func (t *LSPCallHierarchyTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath := configvalue.String(params, "filePath", "")
	line := configvalue.Int(params, "line", 0)
	character := configvalue.Int(params, "character", 0)
	direction := configvalue.String(params, "direction", "")
	if filePath == "" {
		return "Error: filePath is required", nil
	}
	client, _, err := t.manager.GetClient(filePath)
	if err != nil {
		return fmt.Sprintf("LSP error: %v", err), nil
	}

	items, err := client.PrepareCallHierarchy(filePath, line, character)
	if err != nil {
		return fmt.Sprintf("Call hierarchy failed: %v", err), nil
	}
	if len(items) == 0 {
		return "No call hierarchy information available for this position.", nil
	}

	var sb strings.Builder
	if direction == "incoming" {
		calls, err := client.IncomingCalls(items[0])
		if err != nil {
			return fmt.Sprintf("Incoming calls failed: %v", err), nil
		}
		sb.WriteString(fmt.Sprintf("Incoming calls (callers of %s):\n", items[0].Name))
		for i, c := range calls {
			sb.WriteString(fmt.Sprintf("  %d. %s (L%d)\n", i+1, c.From.Name, c.From.Range.Start.Line))
		}
		if len(calls) == 0 {
			sb.WriteString("  (none found)\n")
		}
	} else {
		calls, err := client.OutgoingCalls(items[0])
		if err != nil {
			return fmt.Sprintf("Outgoing calls failed: %v", err), nil
		}
		sb.WriteString(fmt.Sprintf("Outgoing calls (callees of %s):\n", items[0].Name))
		for i, c := range calls {
			sb.WriteString(fmt.Sprintf("  %d. %s (L%d)\n", i+1, c.To.Name, c.To.Range.Start.Line))
		}
		if len(calls) == 0 {
			sb.WriteString("  (none found)\n")
		}
	}
	return sb.String(), nil
}
