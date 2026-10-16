package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mac/internal/fsutil"
	"mac/internal/validationhint"
)

type MultiEditTool struct {
	workspace string
	deps      FileToolDependencies
}

func NewMultiEditTool(ws string, dependencies ...FileToolDependencies) *MultiEditTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &MultiEditTool{workspace: ws, deps: deps}
}

func (t *MultiEditTool) Name() string { return "multiedit" }

func (t *MultiEditTool) Description() string {
	return "Perform multiple find-and-replace edits atomically across one or more files. " +
		"All edits are dry-run validated first; if any old_string is missing or ambiguous, NO changes are applied " +
		"and the failing edit includes closest candidate regions. Use this instead of multiple edit_file calls when making coordinated changes. " +
		"The first edit can use empty old_string with a file_path to create a new file. " +
		"Each edit entry: {file_path, old_string, new_string}. Existing files must be read before editing."
}

func (t *MultiEditTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"edits": map[string]interface{}{
				"type":        "array",
				"description": "Array of edit operations to apply atomically",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"file_path": map[string]interface{}{
							"type":        "string",
							"description": "The file to modify (absolute or relative to workspace)",
						},
						"old_string": map[string]interface{}{
							"type":        "string",
							"description": "The exact text to find. Empty for new file creation (only first edit).",
						},
						"new_string": map[string]interface{}{
							"type":        "string",
							"description": "The replacement text",
						},
					},
					"required": []string{"file_path", "old_string", "new_string"},
				},
			},
		},
		"required": []string{"edits"},
	}
}

func (t *MultiEditTool) SnapshotPaths(args map[string]interface{}) []string {
	rawEdits, ok := args["edits"].([]interface{})
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	paths := make([]string, 0, len(rawEdits))
	for _, rawEdit := range rawEdits {
		edit, ok := rawEdit.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := edit["file_path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(t.workspace, path)
		}
		resolved, err := t.deps.SafeJoin(t.workspace, path)
		if err != nil {
			continue
		}
		key := t.deps.IdentityKey(filepath.Clean(resolved))
		if seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, resolved)
	}
	sort.Slice(paths, func(i, j int) bool {
		return t.deps.IdentityKey(paths[i]) < t.deps.IdentityKey(paths[j])
	})
	return paths
}

type multiEditOp struct {
	filePath   string
	oldStr     string
	newStr     string
	before     string
	matchedOld string
	foundIdx   int
	create     bool
}

func (t *MultiEditTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	editsRaw, ok := params["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return "", fmt.Errorf("edits array is required and must not be empty")
	}

	// Parse and validate edits
	var ops []multiEditOp
	for i, raw := range editsRaw {
		e, ok := raw.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("edit[%d]: must be an object", i)
		}
		fp, _ := e["file_path"].(string)
		oldStr, _ := e["old_string"].(string)
		newStr, _ := e["new_string"].(string)
		if fp == "" {
			return "", fmt.Errorf("edit[%d]: file_path is required", i)
		}
		if err := t.deps.EnforcePath(ctx, t.workspace, fp); err != nil {
			return "", fmt.Errorf("edit[%d]: %w", i, err)
		}
		if !filepath.IsAbs(fp) {
			fp = filepath.Join(t.workspace, fp)
		}
		if safePath, err := t.deps.SafeJoin(t.workspace, fp); err == nil {
			fp = safePath
		}
		if t.deps.IsProtectedPath(fp) {
			return "", fmt.Errorf("BLOCKED: '%s' is a system-protected path. %s", fp, t.deps.ProtectionWarning(fp))
		}
		ops = append(ops, multiEditOp{filePath: fp, oldStr: oldStr, newStr: newStr, foundIdx: -1})
	}

	unlock := lockMultiEditPaths(ops)
	defer unlock()

	// Phase 1: dry-run all edits in memory. Any failure aborts without writing.
	contents := make(map[string]string)
	created := make(map[string]bool)
	for i := range ops {
		if ops[i].oldStr == "" {
			if _, err := os.Stat(ops[i].filePath); err == nil {
				return "", fmt.Errorf("edit[%d]: cannot create '%s' — file already exists. Use edit_file to modify it.", i, ops[i].filePath)
			}
			if created[ops[i].filePath] {
				return "", fmt.Errorf("edit[%d]: cannot create '%s' twice in the same multiedit.", i, ops[i].filePath)
			}
			contents[ops[i].filePath] = ops[i].newStr
			created[ops[i].filePath] = true
			ops[i].create = true
			continue
		}

		if err := GetFileStateRegistry().MustHaveRead(ops[i].filePath, ReaderIDFromContext(ctx)); err != nil {
			return "", fmt.Errorf("edit[%d]: %w", i, err)
		}
		content, ok := contents[ops[i].filePath]
		if !ok {
			data, err := os.ReadFile(ops[i].filePath)
			if err != nil {
				return "", fmt.Errorf("edit[%d]: failed to read '%s': %w", i, ops[i].filePath, err)
			}
			content = string(data)
			contents[ops[i].filePath] = content
		}

		idx, matchedContent, matchedOld := findMultiEditMatch(content, ops[i].oldStr)
		if idx < 0 {
			return "", fmt.Errorf("edit[%d]: no changes applied.\n%s", i,
				buildOldStringNotFoundMessage(ops[i].filePath, t.workspace, ops[i].oldStr, content))
		}
		if count := strings.Count(matchedContent, matchedOld); count > 1 {
			return "", fmt.Errorf("edit[%d]: old_string found %d times in '%s'. No changes applied — add surrounding context to make it unique.",
				i, count, ops[i].filePath)
		}
		ops[i].foundIdx = idx
		ops[i].before = matchedContent
		ops[i].matchedOld = matchedOld
		contents[ops[i].filePath] = matchedContent[:idx] + ops[i].newStr + matchedContent[idx+len(matchedOld):]
	}

	paths := sortedContentPaths(contents)
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("no changes applied: failed to create directory for '%s': %w", path, err)
		}
	}

	beforeWrite := make(map[string][]byte)
	beforeModes := make(map[string]os.FileMode)
	// Validate every existing file and capture every rollback image before the
	// first write. A stale file late in sorted order must not leave earlier
	// files partially committed.
	for _, path := range paths {
		if created[path] {
			continue
		}
		if stale := GetFileStateRegistry().CheckBeforeWrite(path, ReaderIDFromContext(ctx)); !stale.OK {
			return "", fmt.Errorf("no changes applied: %s", stale.Message)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("no changes applied: failed to read backup content for '%s': %w", path, err)
		}
		beforeWrite[path] = data
		if info, statErr := os.Stat(path); statErr == nil {
			beforeModes[path] = info.Mode().Perm()
		}
		PreEditBackup(path, ctx)
	}

	var written []string
	for _, path := range paths {
		mode := os.FileMode(0644)
		if existingMode, ok := beforeModes[path]; ok {
			mode = existingMode
		}
		if err := t.deps.AtomicWrite(path, []byte(contents[path]), mode); err != nil {
			rollbackMsg := rollbackMultiEditWritesWithWriter(written, beforeWrite, created, t.deps.AtomicWrite, beforeModes)
			if rollbackMsg != "" {
				rollbackMsg = " " + rollbackMsg
			}
			return "", fmt.Errorf("no changes applied: failed to write '%s': %w.%s", path, err, rollbackMsg)
		}
		written = append(written, path)
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("Dry-run validated and applied %d edit(s) atomically across %d file(s):\n", len(ops), len(paths)))
	for i := range ops {
		rel, _ := filepath.Rel(t.workspace, ops[i].filePath)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = ops[i].filePath
		}
		if ops[i].create {
			report.WriteString(fmt.Sprintf("  [%d] Created %s (%d bytes)\n", i, rel, len(ops[i].newStr)))
		} else {
			oldLen := len(ops[i].matchedOld)
			newLen := len(ops[i].newStr)
			report.WriteString(fmt.Sprintf("  [%d] %s: replaced %d->%d chars at offset %d\n", i, rel, oldLen, newLen, ops[i].foundIdx))
			report.WriteString(buildChangeDiff(ops[i].before, ops[i].foundIdx, ops[i].matchedOld, ops[i].newStr, false))
		}
	}
	for _, path := range paths {
		postEditReport, postEditErr := t.deps.FinalizeWrite(t.workspace, path, ReaderIDFromContext(ctx))
		report.WriteString(postEditReport)
		report.WriteString("\n")
		if postEditErr != nil {
			return "", fmt.Errorf("multiedit was committed, but the final state of %s could not be verified: %w", path, postEditErr)
		}
	}
	if hint := validationhint.SuggestedForPathsInWorkspace(t.workspace, paths); hint != "" {
		report.WriteString(hint)
	}
	return report.String(), nil
}

func rollbackMultiEditWrites(written []string, beforeWrite map[string][]byte, created map[string]bool, modeSets ...map[string]os.FileMode) string {
	return rollbackMultiEditWritesWithWriter(written, beforeWrite, created, fsutil.WriteAtomic, modeSets...)
}

func RollbackMultiEditWrites(written []string, beforeWrite map[string][]byte, created map[string]bool, modeSets ...map[string]os.FileMode) string {
	return rollbackMultiEditWrites(written, beforeWrite, created, modeSets...)
}

func rollbackMultiEditWritesWithWriter(written []string, beforeWrite map[string][]byte, created map[string]bool, writer func(string, []byte, os.FileMode) error, modeSets ...map[string]os.FileMode) string {
	if len(written) == 0 {
		return ""
	}
	var failures []string
	for i := len(written) - 1; i >= 0; i-- {
		path := written[i]
		if created[path] {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("remove created %s: %v", path, err))
			}
			continue
		}
		data, ok := beforeWrite[path]
		if !ok {
			failures = append(failures, fmt.Sprintf("missing backup for %s", path))
			continue
		}
		mode := os.FileMode(0644)
		if len(modeSets) > 0 {
			if originalMode, ok := modeSets[0][path]; ok {
				mode = originalMode
			}
		}
		if err := writer(path, data, mode); err != nil {
			failures = append(failures, fmt.Sprintf("restore %s: %v", path, err))
		}
	}
	if len(failures) > 0 {
		return "Rollback attempted but had errors: " + strings.Join(failures, "; ")
	}
	return "Rollback completed."
}

func lockMultiEditPaths(ops []multiEditOp) func() {
	seen := make(map[string]bool)
	var paths []string
	for _, op := range ops {
		absPath, _ := filepath.Abs(op.filePath)
		if seen[absPath] {
			continue
		}
		seen[absPath] = true
		paths = append(paths, absPath)
	}
	sort.Strings(paths)
	for _, path := range paths {
		GetFileStateRegistry().LockPath(path)
	}
	return func() {
		for i := len(paths) - 1; i >= 0; i-- {
			GetFileStateRegistry().UnlockPath(paths[i])
		}
	}
}

func findMultiEditMatch(content, oldStr string) (int, string, string) {
	idx := strings.Index(content, oldStr)
	if idx >= 0 {
		return idx, content, oldStr
	}
	normalizedOld, normalizedContent := normalizeEditStrings(oldStr, content)
	if normalizedOld != oldStr {
		idx = strings.Index(normalizedContent, normalizedOld)
		if idx >= 0 {
			return idx, normalizedContent, normalizedOld
		}
	}
	return -1, content, oldStr
}

func sortedContentPaths(contents map[string]string) []string {
	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
