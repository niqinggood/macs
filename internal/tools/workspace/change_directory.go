package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ChangeDirTool 切换 session 工作目录，子 agent 继承
type ChangeDirTool struct {
	currentDir string
	setDir     func(string) // 回调：修改 agent 的 workspace
}

func NewChangeDirTool(initDir string, setDir func(string)) *ChangeDirTool {
	return &ChangeDirTool{currentDir: initDir, setDir: setDir}
}

func (t *ChangeDirTool) Name() string { return "change_directory" }
func (t *ChangeDirTool) Description() string {
	return "Change the session's current working directory. " +
		"Sub-agents and subsequent cli calls will use this directory. " +
		"Pass '~' or '.' to reset to the workspace root. " +
		"Use this instead of adding work_dir= to every cli call."
}

func (t *ChangeDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Target directory path (relative or absolute). Use '~' or '.' to reset to workspace root.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ChangeDirTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	dir, _ := params["path"].(string)
	if dir == "" {
		return "", fmt.Errorf("path is required")
	}
	base := t.currentDir
	if dir == "~" || dir == "." {
		if t.setDir != nil {
			t.setDir(base)
		}
		return "Working directory reset to: " + base, nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(base, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("directory not found: %s (%w)", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", dir)
	}
	if t.setDir != nil {
		t.setDir(dir)
	}
	t.currentDir = dir
	return fmt.Sprintf("Changed working directory:\n  %s → %s", base, dir), nil
}
