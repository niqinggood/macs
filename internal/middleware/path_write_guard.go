package middleware

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"mac/internal/toolcontract"
)

// PathWriteGuardMiddleware makes selected workspace paths immutable for an
// agent. This is useful for coding and migration tasks where fixtures, tests,
// generated snapshots, or other reference material may be inspected but must
// not be changed. Direct file-tool writes are rejected before execution. Tools
// that can execute arbitrary code (for example cli and ptc_execute) are checked
// after execution and any protected-path changes are reverted to the state
// captured before the first mutating tool call.
type PathWriteGuardMiddleware struct {
	Base
	workspace string
	patterns  []string
	registry  ToolLookup

	baselineOnce sync.Once
	restoreMu    sync.Mutex
	baseline     map[string]guardedPathState
	baselineErr  error
}

type guardedPathState struct {
	mode       fs.FileMode
	data       []byte
	linkTarget string
}

const pathWriteGuardCheckMetaKey = "path_write_guard.check_after"

// ToolLookup is the narrow registry surface needed to determine whether a tool
// is read-only. It keeps this middleware independent from the root registry.
type ToolLookup interface {
	Get(name string) (toolcontract.Tool, bool)
}

func NewPathWriteGuardMiddleware(workspace string, patterns []string, registry ToolLookup) *PathWriteGuardMiddleware {
	cleaned := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = normalizeGuardPath(pattern)
		if pattern != "" && pattern != "." {
			cleaned = append(cleaned, pattern)
		}
	}
	middleware := &PathWriteGuardMiddleware{
		Base:      NewBase("path_write_guard"),
		workspace: filepath.Clean(workspace),
		patterns:  cleaned,
	}
	middleware.registry = registry
	return middleware
}

func (m *PathWriteGuardMiddleware) BeforeTool(ctx *Context) error {
	if m == nil || ctx == nil || len(m.patterns) == 0 {
		return nil
	}
	for _, target := range writeTargetsForToolCall(ctx.ToolCall) {
		relative := m.relativePath(target)
		for _, pattern := range m.patterns {
			if GuardPathMatches(relative, pattern) {
				return fmt.Errorf(
					"write to %q is blocked because it matches read-only path %q; inspect it if needed, but implement the change in an allowed production path",
					relative,
					pattern,
				)
			}
		}
	}
	if !m.toolMayMutateFilesystem(ctx.ToolCall.Name) {
		return nil
	}
	if err := m.ensureBaseline(); err != nil {
		return fmt.Errorf("cannot enforce read-only paths: %w", err)
	}
	ctx.SetMeta(pathWriteGuardCheckMetaKey, true)
	return nil
}

func (m *PathWriteGuardMiddleware) AfterTool(ctx *Context) error {
	if m == nil || ctx == nil || len(m.patterns) == 0 {
		return nil
	}
	if check, ok := ctx.GetMeta(pathWriteGuardCheckMetaKey); !ok || check != true {
		return nil
	}

	restored, err := m.restoreProtectedPaths()
	if err != nil {
		message := fmt.Sprintf("[BLOCKED] read-only path enforcement failed: %v", err)
		ctx.ToolResult = &toolcontract.Result{
			ToolCallID:         ctx.ToolCall.ID,
			Name:               ctx.ToolCall.Name,
			Content:            message,
			ExecutionSucceeded: toolcontract.OutcomePointer(false),
		}
		ctx.ToolErr = fmt.Errorf("read-only path enforcement failed: %w", err)
		return ctx.ToolErr
	}
	if len(restored) == 0 {
		return nil
	}

	sort.Strings(restored)
	message := fmt.Sprintf(
		"[BLOCKED] tool modified read-only paths; all protected changes were reverted: %s. "+
			"Inspect tests if needed, but make changes only in allowed production paths.",
		strings.Join(restored, ", "),
	)
	if ctx.ToolResult == nil {
		ctx.ToolResult = &toolcontract.Result{ToolCallID: ctx.ToolCall.ID, Name: ctx.ToolCall.Name}
	}
	ctx.ToolResult.Content = message
	ctx.ToolResult.ExecutionSucceeded = toolcontract.OutcomePointer(false)
	ctx.ToolErr = nil
	return nil
}

func (m *PathWriteGuardMiddleware) toolMayMutateFilesystem(toolName string) bool {
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	if m.registry != nil {
		if tool, ok := m.registry.Get(toolName); ok {
			if readOnly, declared := tool.(toolcontract.ReadOnlyTool); declared {
				return !readOnly.IsReadOnly()
			}
		}
	}

	// Some core inspection tools predate the ReadOnlyTool marker. Everything
	// else is treated as potentially mutating so new tools fail closed.
	switch toolName {
	case "read_file", "list_dir", "grep", "glob", "project_map", "tool_detail":
		return false
	default:
		return true
	}
}

func (m *PathWriteGuardMiddleware) ensureBaseline() error {
	m.baselineOnce.Do(func() {
		m.baseline, m.baselineErr = m.captureProtectedPaths()
	})
	return m.baselineErr
}

func (m *PathWriteGuardMiddleware) captureProtectedPaths() (map[string]guardedPathState, error) {
	state := make(map[string]guardedPathState)
	if _, err := os.Stat(m.workspace); err != nil {
		return nil, fmt.Errorf("stat workspace %q: %w", m.workspace, err)
	}

	err := filepath.WalkDir(m.workspace, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == m.workspace {
			return nil
		}
		relative, err := filepath.Rel(m.workspace, current)
		if err != nil {
			return err
		}
		relative = normalizeGuardPath(relative)
		if !m.matchesProtectedPath(relative) {
			return nil
		}

		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		item := guardedPathState{mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			item.linkTarget, err = os.Readlink(current)
		case info.Mode().IsRegular():
			item.data, err = os.ReadFile(current)
		}
		if err != nil {
			return err
		}
		state[relative] = item
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot protected paths: %w", err)
	}
	return state, nil
}

func (m *PathWriteGuardMiddleware) matchesProtectedPath(relative string) bool {
	for _, pattern := range m.patterns {
		if GuardPathMatches(relative, pattern) {
			return true
		}
	}
	return false
}

func (m *PathWriteGuardMiddleware) restoreProtectedPaths() ([]string, error) {
	if err := m.ensureBaseline(); err != nil {
		return nil, err
	}

	m.restoreMu.Lock()
	defer m.restoreMu.Unlock()

	current, err := m.captureProtectedPaths()
	if err != nil {
		return nil, err
	}

	changedSet := make(map[string]struct{})
	var removePaths []string
	for relative, currentState := range current {
		baselineState, existed := m.baseline[relative]
		if !existed {
			changedSet[relative] = struct{}{}
			removePaths = append(removePaths, relative)
			continue
		}
		if guardedPathStatesEqual(currentState, baselineState) {
			continue
		}
		changedSet[relative] = struct{}{}
		if currentState.mode.Type() != baselineState.mode.Type() {
			removePaths = append(removePaths, relative)
		}
	}
	for relative := range m.baseline {
		if _, exists := current[relative]; !exists {
			changedSet[relative] = struct{}{}
		}
	}
	if len(changedSet) == 0 {
		return nil, nil
	}

	sort.Slice(removePaths, func(i, j int) bool {
		return pathDepth(removePaths[i]) > pathDepth(removePaths[j])
	})
	for _, relative := range removePaths {
		if err := m.removeProtectedEntry(relative); err != nil {
			return mapKeys(changedSet), err
		}
	}

	var directories []string
	var files []string
	for relative, baselineState := range m.baseline {
		if _, changed := changedSet[relative]; !changed {
			continue
		}
		if baselineState.mode.IsDir() {
			directories = append(directories, relative)
		} else {
			files = append(files, relative)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return pathDepth(directories[i]) < pathDepth(directories[j])
	})
	for _, relative := range directories {
		absolute, err := m.protectedAbsolutePath(relative)
		if err != nil {
			return mapKeys(changedSet), err
		}
		if err := os.MkdirAll(absolute, m.baseline[relative].mode.Perm()); err != nil {
			return mapKeys(changedSet), fmt.Errorf("restore directory %q: %w", relative, err)
		}
		if err := os.Chmod(absolute, m.baseline[relative].mode.Perm()); err != nil {
			return mapKeys(changedSet), fmt.Errorf("restore directory mode %q: %w", relative, err)
		}
	}
	sort.Strings(files)
	for _, relative := range files {
		if err := m.restoreProtectedEntry(relative, m.baseline[relative]); err != nil {
			return mapKeys(changedSet), err
		}
	}
	return mapKeys(changedSet), nil
}

func (m *PathWriteGuardMiddleware) removeProtectedEntry(relative string) error {
	absolute, err := m.protectedAbsolutePath(relative)
	if err != nil {
		return err
	}
	err = os.Remove(absolute)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove unauthorized protected entry %q: %w", relative, err)
}

func (m *PathWriteGuardMiddleware) restoreProtectedEntry(relative string, state guardedPathState) error {
	absolute, err := m.protectedAbsolutePath(relative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return fmt.Errorf("restore parent for %q: %w", relative, err)
	}

	if state.mode&os.ModeSymlink != 0 {
		if err := os.Remove(absolute); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("replace protected symlink %q: %w", relative, err)
		}
		if err := os.Symlink(state.linkTarget, absolute); err != nil {
			return fmt.Errorf("restore protected symlink %q: %w", relative, err)
		}
		return nil
	}
	if err := os.WriteFile(absolute, state.data, state.mode.Perm()); err != nil {
		return fmt.Errorf("restore protected file %q: %w", relative, err)
	}
	if err := os.Chmod(absolute, state.mode.Perm()); err != nil {
		return fmt.Errorf("restore protected file mode %q: %w", relative, err)
	}
	return nil
}

func (m *PathWriteGuardMiddleware) protectedAbsolutePath(relative string) (string, error) {
	absolute := filepath.Clean(filepath.Join(m.workspace, filepath.FromSlash(relative)))
	check, err := filepath.Rel(m.workspace, absolute)
	if err != nil || check == "." || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("protected path escapes workspace: %q", relative)
	}
	return absolute, nil
}

func guardedPathStatesEqual(left, right guardedPathState) bool {
	if left.mode.Type() != right.mode.Type() || left.mode.Perm() != right.mode.Perm() {
		return false
	}
	if left.mode&os.ModeSymlink != 0 {
		return left.linkTarget == right.linkTarget
	}
	if left.mode.IsRegular() {
		return bytes.Equal(left.data, right.data)
	}
	return true
}

func pathDepth(relative string) int {
	return strings.Count(normalizeGuardPath(relative), "/")
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func (m *PathWriteGuardMiddleware) relativePath(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(m.workspace, resolved)
	}
	relative, err := filepath.Rel(m.workspace, filepath.Clean(resolved))
	if err != nil {
		return normalizeGuardPath(target)
	}
	return normalizeGuardPath(relative)
}

func writeTargetsForToolCall(call toolcontract.Call) []string {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "write_file":
		if value := firstStringArgument(call.Arguments, "path", "file"); value != "" {
			return []string{value}
		}
	case "edit_file":
		if value := firstStringArgument(call.Arguments, "file_path", "path"); value != "" {
			return []string{value}
		}
	case "json_patch":
		if value := firstStringArgument(call.Arguments, "path"); value != "" {
			return []string{value}
		}
	case "multiedit":
		var targets []string
		switch edits := call.Arguments["edits"].(type) {
		case []interface{}:
			for _, raw := range edits {
				if edit, ok := raw.(map[string]interface{}); ok {
					if value := firstStringArgument(edit, "file_path", "path"); value != "" {
						targets = append(targets, value)
					}
				}
			}
		case []map[string]interface{}:
			for _, edit := range edits {
				if value := firstStringArgument(edit, "file_path", "path"); value != "" {
					targets = append(targets, value)
				}
			}
		}
		return targets
	}
	return nil
}

func firstStringArgument(arguments map[string]interface{}, names ...string) string {
	for _, name := range names {
		if value, ok := arguments[name].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeGuardPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func GuardPathMatches(relative, pattern string) bool {
	relative = normalizeGuardPath(relative)
	pattern = normalizeGuardPath(pattern)
	if relative == "" || pattern == "" {
		return false
	}

	// Directory-tree patterns include the directory itself and descendants at
	// arbitrary depth; filepath.Match's "*" does not cross path separators.
	if strings.HasSuffix(pattern, "/**") {
		directory := strings.TrimSuffix(pattern, "/**")
		if strings.HasPrefix(directory, "**/") {
			segment := strings.TrimPrefix(directory, "**/")
			return relative == segment ||
				strings.HasPrefix(relative, segment+"/") ||
				strings.Contains(relative, "/"+segment+"/") ||
				strings.HasSuffix(relative, "/"+segment)
		}
		return relative == directory || strings.HasPrefix(relative, directory+"/")
	}

	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		if matched, _ := path.Match(suffix, relative); matched {
			return true
		}
		for index := strings.IndexByte(relative, '/'); index >= 0; {
			relative = relative[index+1:]
			if matched, _ := path.Match(suffix, relative); matched {
				return true
			}
			index = strings.IndexByte(relative, '/')
		}
		return false
	}

	matched, err := path.Match(pattern, relative)
	return err == nil && matched
}
