package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JSONPatchTool applies a small RFC 6902-style transaction to an existing
// JSON document. It is intentionally structural: formatting and object-key
// order never participate in matching.
type JSONPatchTool struct {
	workspace string
	deps      FileToolDependencies
}

func NewJSONPatchTool(workspace string, dependencies ...FileToolDependencies) *JSONPatchTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &JSONPatchTool{workspace: workspace, deps: deps}
}

func (t *JSONPatchTool) Name() string { return "json_patch" }

func (t *JSONPatchTool) Description() string {
	return "Structurally modify an existing JSON file with atomic add, replace, or remove operations using JSON Pointer paths. " +
		"Use this instead of edit_file when JSON formatting, indentation, or key order may differ. Read the file first. " +
		"All operations are validated in memory and committed together; any failure leaves the file unchanged."
}

func (t *JSONPatchTool) ScopePath(args map[string]interface{}) string {
	raw, _ := args["path"].(string)
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	resolved, err := t.deps.SafeJoin(t.workspace, raw)
	if err != nil {
		return ""
	}
	return resolved
}

func (t *JSONPatchTool) SnapshotPaths(args map[string]interface{}) []string {
	if path := t.ScopePath(args); path != "" {
		return []string{path}
	}
	return nil
}

func (t *JSONPatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Existing JSON file to modify, absolute or relative to the workspace.",
			},
			"operations": map[string]interface{}{
				"type":        "array",
				"description": "Ordered atomic patch operations. JSON Pointer escapes are ~0 for ~ and ~1 for /.",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"op": map[string]interface{}{
							"type": "string",
							"enum": []string{"add", "replace", "remove"},
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "RFC 6901 JSON Pointer, for example /slides/2/blocks/0/body. Use /items/- to append to an array.",
						},
						"value": map[string]interface{}{
							"description": "Required for add and replace; omit for remove.",
						},
					},
					"required": []string{"op", "path"},
				},
			},
		},
		"required": []string{"path", "operations"},
	}
}

func (t *JSONPatchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	rawPath, _ := params["path"].(string)
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, rawPath); err != nil {
		return "", err
	}
	operations, ok := params["operations"].([]interface{})
	if !ok || len(operations) == 0 {
		return "", fmt.Errorf("operations must contain at least one patch operation")
	}
	if len(operations) > 100 {
		return "", fmt.Errorf("operations contains %d entries; maximum is 100 per atomic patch", len(operations))
	}

	fullPath, err := t.deps.SafeJoin(t.workspace, rawPath)
	if err != nil {
		return "", err
	}
	if t.deps.IsProtectedPath(fullPath) {
		return "", fmt.Errorf("BLOCKED: '%s' is a system-protected path. %s", fullPath, t.deps.ProtectionWarning(fullPath))
	}
	readerID := ReaderIDFromContext(ctx)
	if err := GetFileStateRegistry().MustHaveRead(fullPath, readerID); err != nil {
		return "", err
	}

	GetFileStateRegistry().LockPath(fullPath)
	defer GetFileStateRegistry().UnlockPath(fullPath)
	if stale := GetFileStateRegistry().CheckBeforeWrite(fullPath, readerID); !stale.OK {
		return "", fmt.Errorf("%s", stale.Message)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read JSON file: %w", err)
	}
	document, err := decodeJSONDocument(data)
	if err != nil {
		return "", fmt.Errorf("invalid JSON document: %w", err)
	}

	summaries := make([]string, 0, len(operations))
	for i, rawOperation := range operations {
		operation, ok := rawOperation.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("operations[%d] must be an object", i)
		}
		op, _ := operation["op"].(string)
		op = strings.ToLower(strings.TrimSpace(op))
		pointer, _ := operation["path"].(string)
		tokens, err := parseJSONPointer(pointer)
		if err != nil {
			return "", fmt.Errorf("operations[%d].path: %w", i, err)
		}
		value, hasValue := operation["value"]
		if (op == "add" || op == "replace") && !hasValue {
			return "", fmt.Errorf("operations[%d].value is required for %s", i, op)
		}
		if op == "remove" && hasValue {
			return "", fmt.Errorf("operations[%d].value must be omitted for remove", i)
		}
		if op != "add" && op != "replace" && op != "remove" {
			return "", fmt.Errorf("operations[%d].op must be add, replace, or remove", i)
		}

		document, err = applyJSONPatchAt(document, tokens, op, value)
		if err != nil {
			return "", fmt.Errorf("operations[%d] %s %q failed: %w", i, op, pointer, err)
		}
		summaries = append(summaries, op+" "+pointer)
	}

	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal patched JSON: %w", err)
	}
	updated = append(updated, '\n')
	if _, err := decodeJSONDocument(updated); err != nil {
		return "", fmt.Errorf("patched JSON failed validation: %w", err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat JSON file: %w", err)
	}
	PreEditBackup(fullPath, ctx)
	if err := t.deps.AtomicWrite(fullPath, updated, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("commit JSON patch: %w", err)
	}
	postEditReport, postEditErr := t.deps.FinalizeWrite(t.workspace, fullPath, readerID)
	if postEditErr != nil {
		return "", fmt.Errorf("JSON patch was committed, but its final state could not be verified: %w", postEditErr)
	}

	displayPath := rawPath
	if rel, relErr := filepath.Rel(t.workspace, fullPath); relErr == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}
	return fmt.Sprintf("Applied %d atomic JSON patch operation(s) to %s\n%s%s", len(operations), displayPath, strings.Join(summaries, "\n"), postEditReport), nil
}

func decodeJSONDocument(data []byte) (interface{}, error) {
	return DecodeJSONDocument(data)
}

func parseJSONPointer(pointer string) ([]string, error) {
	return ParseJSONPointer(pointer)
}

func applyJSONPatchAt(node interface{}, tokens []string, operation string, value interface{}) (interface{}, error) {
	return ApplyJSONPatchAt(node, tokens, operation, value)
}

func parseJSONArrayIndex(token string) (int, error) {
	return ParseJSONArrayIndex(token)
}
