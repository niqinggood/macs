package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"mac/internal/configvalue"
)

// InstructionProvider supplies already-resolved project instruction content.
// The tool does not depend on the host loader implementation.
type InstructionProvider interface {
	LoadInstructions(workspace, target string) (audit, prompt string, err error)
}

type ProjectInstructionsTool struct {
	workspace string
	provider  InstructionProvider
}

func NewProjectInstructionsTool(workspace string, provider InstructionProvider) *ProjectInstructionsTool {
	return &ProjectInstructionsTool{workspace: workspace, provider: provider}
}

func (tool *ProjectInstructionsTool) Name() string { return "project_instructions" }

func (tool *ProjectInstructionsTool) Description() string {
	return "Audit the AGENTS.md, CLAUDE.md, local overrides, rule files, and imports currently applied to a project directory. Use when project rules are unclear or unexpected."
}

func (tool *ProjectInstructionsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_dir": map[string]interface{}{
				"type": "string", "description": "Optional target directory. Relative paths resolve from the workspace.",
			},
			"include_content": map[string]interface{}{
				"type": "boolean", "description": "Include the fully resolved instruction prompt after the provenance audit. Default false.",
			},
		},
	}
}

func (tool *ProjectInstructionsTool) IsReadOnly() bool { return true }

func (tool *ProjectInstructionsTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if tool == nil || tool.provider == nil {
		return "", fmt.Errorf("project instruction loader is not initialized")
	}
	target := strings.TrimSpace(configvalue.String(params, "target_dir", ""))
	if target == "" {
		target = tool.workspace
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(tool.workspace, target)
	}
	audit, prompt, err := tool.provider.LoadInstructions(tool.workspace, target)
	if err != nil {
		return "", err
	}
	includeContent, _ := params["include_content"].(bool)
	if includeContent && prompt != "" {
		audit += "\n\n" + prompt
	}
	return audit, nil
}
