package knowledge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// SourceProvider is a trusted provider selected by the root assembly layer.
type SourceProvider interface {
	Execute(context.Context, map[string]interface{}) (string, error)
}

// SourceAcquireTool routes a source description to a trusted provider without
// depending on any concrete application or gateway type.
type SourceAcquireTool struct {
	workspace string
	provider  SourceProvider
}

func NewSourceAcquireTool(workspace string, provider SourceProvider) *SourceAcquireTool {
	return &SourceAcquireTool{workspace: workspace, provider: provider}
}

func (t *SourceAcquireTool) Name() string { return "source_acquire" }

func (t *SourceAcquireTool) Description() string {
	return "Acquire and freeze an existing source that the user described without an exact URL or local path. " +
		"Pass the user's source description verbatim; the host selects the trusted read-only provider. " +
		"For an exact URL use web_fetch, and for an exact local path use read_file/extract."
}

func (t *SourceAcquireTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"description": map[string]interface{}{
				"type": "string", "description": "The user's existing-source description verbatim, including the platform/community hint; do not add guessed entities or search terms.",
			},
			"max_results": map[string]interface{}{
				"type": "integer", "minimum": 1, "maximum": 10,
				"description": "Maximum frozen sources. Use 1 for a singular referenced source.",
			},
			"max_pages": map[string]interface{}{
				"type": "integer", "minimum": 1, "maximum": 10,
				"description": "Bounded provider search depth. Default: 5.",
			},
			"output_path": map[string]interface{}{
				"type": "string", "description": "Execution-scoped source.json path under .openkitty/storage/.",
			},
		},
		"required": []string{"description", "output_path"},
	}
}

func (t *SourceAcquireTool) MaxResultSize() int    { return 16 * 1024 }
func (t *SourceAcquireTool) IsReadOnly() bool      { return true }
func (t *SourceAcquireTool) IsNeverParallel() bool { return true }

func (t *SourceAcquireTool) ScopePath(args map[string]interface{}) string {
	raw, _ := args["output_path"].(string)
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Join(t.workspace, filepath.Clean(raw))
}

func (t *SourceAcquireTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", errors.New("source_acquire is not configured")
	}
	description, _ := params["description"].(string)
	description = strings.TrimSpace(description)
	if description == "" {
		return "", errors.New("description is required")
	}
	lower := strings.ToLower(description)
	if containsAny(lower, "chatroom", "nova", "聊天室", "硅基聊天室") {
		if t.provider == nil {
			return "", errors.New("no trusted provider is available for the described source")
		}
		delegated := map[string]interface{}{
			"query": description, "max_results": 1, "max_pages": 5,
			"output_path": params["output_path"],
		}
		if value, ok := params["max_results"]; ok {
			delegated["max_results"] = value
		}
		if value, ok := params["max_pages"]; ok {
			delegated["max_pages"] = value
		}
		return t.provider.Execute(ctx, delegated)
	}
	return "", errors.New("no trusted provider matched the described source; use web_fetch for an exact URL or read_file/extract for an exact local path")
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
