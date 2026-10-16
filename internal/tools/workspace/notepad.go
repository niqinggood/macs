package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const legacyNotepadRelativePath = ".openkitty/storage/scratchpad.md"

var notepadPathLocks sync.Map

// NotepadTool is a task-scoped scratchpad that survives context pressure.
type NotepadTool struct {
	workspace string
}

func NewNotepadTool(workspace string) *NotepadTool {
	return &NotepadTool{workspace: workspace}
}

func (tool *NotepadTool) Name() string       { return "notepad" }
func (tool *NotepadTool) MaxResultSize() int { return 10000 }

func (tool *NotepadTool) Description() string {
	return "Agent scratchpad for long tasks whose findings must survive substantial context pressure. Do not use it for short or medium single-answer research, to restate the user request, or to copy evidence already present in tool results. " +
		"Actions: append (add a note), read (view all notes), clear (reset). " +
		"Persists across turns and is isolated by the current task_id. Calls without a task_id use an explicit legacy default notepad."
}

func (tool *NotepadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"append", "read", "clear"},
				"description": "append: add a note line. read: show all notes. clear: reset notepad.",
			},
			"content": map[string]interface{}{
				"type": "string", "description": "Note content (required for append action). Supports markdown.",
			},
		},
		"required": []string{"action"},
	}
}

func (tool *NotepadTool) path() string {
	return filepath.Join(tool.workspace, filepath.FromSlash(legacyNotepadRelativePath))
}

func (tool *NotepadTool) pathForContext(ctx context.Context) (string, error) {
	taskID := ""
	if ctx != nil {
		taskID, _ = ctx.Value("task_id").(string)
		taskID = strings.TrimSpace(taskID)
	}
	if taskID == "" {
		return tool.safeWorkspacePath(filepath.FromSlash(legacyNotepadRelativePath))
	}
	filename := sanitizeNotepadTaskID(taskID) + ".md"
	return tool.safeWorkspacePath(filepath.Join(".openkitty", "storage", ".system", "notepads", filename))
}

func (tool *NotepadTool) safeWorkspacePath(relative string) (string, error) {
	workspace := strings.TrimSpace(tool.workspace)
	if workspace == "" {
		workspace = "."
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve notepad workspace: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(root, relative))
	if err != nil {
		return "", fmt.Errorf("resolve notepad path: %w", err)
	}
	relativeToRoot, err := filepath.Rel(root, target)
	if err != nil || relativeToRoot == ".." || filepath.IsAbs(relativeToRoot) || strings.HasPrefix(relativeToRoot, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("notepad path escapes workspace")
	}
	return target, nil
}

func sanitizeNotepadTaskID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	hash := sha256.Sum256([]byte(taskID))
	var prefix strings.Builder
	lastSeparator := false
	for _, character := range taskID {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if isLetter || isDigit || character == '_' || character == '-' {
			if character >= 'A' && character <= 'Z' {
				character += 'a' - 'A'
			}
			prefix.WriteRune(character)
			lastSeparator = false
		} else if !lastSeparator && prefix.Len() > 0 {
			prefix.WriteByte('-')
			lastSeparator = true
		}
		if prefix.Len() >= 64 {
			break
		}
	}
	readable := strings.Trim(prefix.String(), "-_")
	if readable == "" {
		readable = "task"
	}
	return readable + "-" + hex.EncodeToString(hash[:8])
}

func notepadLockForPath(path string) *sync.Mutex {
	canonical := filepath.Clean(path)
	if absolute, err := filepath.Abs(canonical); err == nil {
		canonical = absolute
	}
	canonical = strings.ToLower(canonical)
	lock, _ := notepadPathLocks.LoadOrStore(canonical, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (tool *NotepadTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	content, _ := params["content"].(string)
	path, err := tool.pathForContext(ctx)
	if err != nil {
		return "", err
	}
	pathLock := notepadLockForPath(path)
	pathLock.Lock()
	defer pathLock.Unlock()
	switch action {
	case "append":
		if content == "" {
			return "", fmt.Errorf("content is required for append action")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("create notepad directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("open notepad: %w", err)
		}
		if _, err := file.WriteString(content + "\n"); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("append notepad: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close notepad: %w", err)
		}
		lines, err := countNotepadLines(path)
		if err != nil {
			return "", fmt.Errorf("count notepad lines: %w", err)
		}
		return fmt.Sprintf("✓ 已记录. 当前笔记 %d 行", lines), nil
	case "read":
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "📝 草稿本为空", nil
			}
			return "", err
		}
		if len(data) == 0 {
			return "📝 草稿本为空", nil
		}
		return fmt.Sprintf("📝 草稿本 (%d 行):\n%s", countNotepadLinesFromData(data), string(data)), nil
	case "clear":
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("create notepad directory: %w", err)
		}
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			return "", fmt.Errorf("clear notepad: %w", err)
		}
		return "🗑 草稿本已清空", nil
	default:
		return "", fmt.Errorf("unknown action: %s. Use append, read, or clear", action)
	}
}

func (tool *NotepadTool) countLines() int {
	path := tool.path()
	pathLock := notepadLockForPath(path)
	pathLock.Lock()
	defer pathLock.Unlock()
	lines, err := countNotepadLines(path)
	if err != nil {
		return 0
	}
	return lines
}

func countNotepadLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return countNotepadLinesFromData(data), nil
}

func countNotepadLinesFromData(data []byte) int {
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return 0
	}
	return len(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"))
}
