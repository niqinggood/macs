package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"mac/internal/processutil"
	"mac/internal/toolcontract"
	"mac/internal/validationhint"
)

// fileEditLocks 防止并发 task 同时编辑同一文件
type EditFileTool struct {
	workspace string
	deps      FileToolDependencies
}

func NewEditFileTool(ws string, dependencies ...FileToolDependencies) *EditFileTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &EditFileTool{workspace: ws, deps: deps}
}

func (t *EditFileTool) Name() string { return "edit_file" }

// ScopePath implements PathScopedTool for parallel safety
func (t *EditFileTool) ScopePath(args map[string]interface{}) string {
	if p, ok := args["file_path"].(string); ok {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(t.workspace, p)
	}
	return ""
}

func (t *EditFileTool) SnapshotPaths(args map[string]interface{}) []string {
	if path := t.ScopePath(args); path != "" {
		return []string{path}
	}
	return nil
}

func (t *EditFileTool) Description() string {
	return "Edit an existing file with exact string replacement. Read the file first, then provide a small unique old_string " +
		"(usually 2-4 adjacent lines) and the replacement new_string. Preserve indentation and whitespace exactly. " +
		"For structural changes to JSON, use json_patch instead so formatting and key order do not affect matching. " +
		"Only the first occurrence is replaced unless replace_all=true, which should be reserved for deliberate renames or mechanical changes. " +
		"If old_string is not found, re-read the file and retry with current context. Returns a diff showing changed lines."
}

// normalizeQuotes replaces curly/smart quotes with straight quotes for matching.
// Claude Code borrowed: FileEditTool quote normalization (LEFT_SINGLE_CURLY_QUOTE etc.)
func normalizeQuotes(s string) string {
	replacements := map[rune]rune{
		'‘': '\'', // left single quotation mark
		'’': '\'', // right single quotation mark
		'“': '"',  // left double quotation mark
		'”': '"',  // right double quotation mark
		'‚': '\'', // single low-9 quotation mark
		'„': '"',  // double low-9 quotation mark
		'′': '\'', // prime
		'″': '"',  // double prime
	}
	result := make([]rune, 0, len(s))
	for _, r := range s {
		if replacement, ok := replacements[r]; ok {
			result = append(result, replacement)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

func NormalizeQuotes(value string) string { return normalizeQuotes(value) }

// normalizeEditStrings applies quote normalization + trailing whitespace stripping.
func normalizeEditStrings(oldStr, fileContent string) (string, string) {
	oldStr = normalizeQuotes(oldStr)
	fileContent = normalizeQuotes(fileContent)
	// Strip trailing whitespace from each line in oldStr
	lines := strings.Split(oldStr, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	oldStr = strings.Join(lines, "\n")
	return oldStr, fileContent
}

type editMatchCandidate struct {
	start int
	end   int
	score float64
	text  string
}

type recoverableEditError struct {
	message   string
	directive toolcontract.RecoveryDirective
}

func (e *recoverableEditError) Error() string { return e.message }

func (e *recoverableEditError) ToolRecoveryDirective() toolcontract.RecoveryDirective {
	return e.directive
}

func buildOldStringNotFoundMessage(filePath, workspace, oldStr, content string) string {
	var b strings.Builder
	b.WriteString("old_string not found in file. Make sure it matches exactly, including whitespace, indentation, and current file content.")
	candidates := closestEditCandidates(oldStr, content, 3)
	if len(candidates) == 0 {
		b.WriteString(" Re-read the target file and retry with a smaller unique block.")
		return b.String()
	}

	rel, err := filepath.Rel(workspace, filePath)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		rel = filePath
	}
	b.WriteString("\n\nClosest candidate regions:")
	for _, c := range candidates {
		readStart := c.start - 5
		if readStart < 1 {
			readStart = 1
		}
		readEnd := c.end + 5
		b.WriteString(fmt.Sprintf("\n- lines %d-%d (score %.2f). Suggested read: read_file path=%q lines_range=%q\n```text\n%s\n```",
			c.start, c.end, c.score, rel, fmt.Sprintf("%d-%d", readStart, readEnd), c.text))
	}
	b.WriteString("\nUse one candidate with enough surrounding context as the new old_string, or re-read the suggested range first.")
	return b.String()
}

func newEditFileStaleContextError(filePath, workspace, oldStr, content string) error {
	message := buildOldStringNotFoundMessage(filePath, workspace, oldStr, content)
	rel := filePath
	if candidate, err := filepath.Rel(workspace, filePath); err == nil && candidate != "" &&
		candidate != ".." && !strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
		rel = candidate
	}
	arguments := map[string]interface{}{"path": rel}
	if candidates := closestEditCandidates(oldStr, content, 1); len(candidates) > 0 {
		readStart := candidates[0].start - 5
		if readStart < 1 {
			readStart = 1
		}
		arguments["lines_range"] = fmt.Sprintf("%d-%d", readStart, candidates[0].end+5)
	}
	return &recoverableEditError{message: message, directive: toolcontract.RecoveryDirective{
		Code:          "edit_stale_context",
		ResourceKey:   "file:" + filepath.Clean(filePath),
		SuggestedTool: "read_file",
		Arguments:     arguments,
		Instruction: "Read the current target range, then retry edit_file with a new small unique old_string copied exactly " +
			"from that fresh content. Do not reuse the failed arguments and do not use a fuzzy replacement.",
		MaxPrompts: 2,
	}}
}

func closestEditCandidates(oldStr, content string, limit int) []editMatchCandidate {
	oldLines := compactEditLines(oldStr)
	if len(oldLines) == 0 {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	windowLen := len(oldLines)
	if windowLen < 1 {
		windowLen = 1
	}
	var candidates []editMatchCandidate
	for start := 0; start < len(lines); start++ {
		for _, size := range candidateWindowSizes(windowLen) {
			if size <= 0 || start+size > len(lines) {
				continue
			}
			window := lines[start : start+size]
			score := editWindowSimilarity(oldLines, window)
			if score <= 0 {
				continue
			}
			candidates = append(candidates, editMatchCandidate{
				start: start + 1,
				end:   start + size,
				score: score,
				text:  numberedEditWindow(window, start+1),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			spanI := candidates[i].end - candidates[i].start
			spanJ := candidates[j].end - candidates[j].start
			if spanI != spanJ {
				return spanI < spanJ
			}
			return candidates[i].start < candidates[j].start
		}
		return candidates[i].score > candidates[j].score
	})

	deduped := make([]editMatchCandidate, 0, limit)
	for _, c := range candidates {
		overlaps := false
		for _, existing := range deduped {
			if c.start <= existing.end && existing.start <= c.end {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		deduped = append(deduped, c)
		if len(deduped) >= limit {
			break
		}
	}
	return deduped
}

func compactEditLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func candidateWindowSizes(n int) []int {
	sizes := []int{n}
	if n > 1 {
		sizes = append(sizes, n-1)
	}
	sizes = append(sizes, n+1)
	return sizes
}

func editWindowSimilarity(oldLines []string, window []string) float64 {
	oldText := strings.ToLower(strings.Join(oldLines, "\n"))
	windowLines := compactEditLines(strings.Join(window, "\n"))
	windowText := strings.ToLower(strings.Join(windowLines, "\n"))
	if oldText == "" || windowText == "" {
		return 0
	}
	if oldText == windowText {
		return 1
	}

	oldTokens := editTokens(oldText)
	windowTokens := editTokens(windowText)
	if len(oldTokens) == 0 || len(windowTokens) == 0 {
		return 0
	}
	windowSet := make(map[string]bool, len(windowTokens))
	for _, token := range windowTokens {
		windowSet[token] = true
	}
	commonWeight := 0.0
	for _, token := range oldTokens {
		if windowSet[token] {
			commonWeight += 1
			continue
		}
		if hasPartialEditTokenMatch(token, windowTokens) {
			commonWeight += 0.6
		}
	}
	tokenScore := commonWeight / float64(len(oldTokens))

	lineHits := 0
	for _, oldLine := range oldLines {
		oldLine = strings.ToLower(strings.TrimSpace(oldLine))
		for _, line := range windowLines {
			if strings.Contains(strings.ToLower(strings.TrimSpace(line)), oldLine) ||
				strings.Contains(oldLine, strings.ToLower(strings.TrimSpace(line))) {
				lineHits++
				break
			}
		}
	}
	lineScore := float64(lineHits) / float64(len(oldLines))
	return tokenScore*0.65 + lineScore*0.35
}

func hasPartialEditTokenMatch(token string, candidates []string) bool {
	if len(token) < 4 {
		return false
	}
	for _, candidate := range candidates {
		if len(candidate) < 4 {
			continue
		}
		if strings.Contains(candidate, token) || strings.Contains(token, candidate) {
			return true
		}
	}
	return false
}

func editTokens(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == '\\' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= 0x4e00 && r <= 0x9fff))
	})
	seen := make(map[string]bool, len(fields))
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(strings.ToLower(f))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		tokens = append(tokens, f)
	}
	return tokens
}

func numberedEditWindow(lines []string, startLine int) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d: %s", startLine+i, line))
	}
	return b.String()
}

func (t *EditFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "The file to modify (absolute or relative to workspace)",
			},
			"old_string": map[string]interface{}{
				"type":        "string",
				"description": "The exact text to find and replace. Must match exactly including whitespace.",
			},
			"new_string": map[string]interface{}{
				"type":        "string",
				"description": "The text to replace it with (must be different from old_string)",
			},
			"replace_all": map[string]interface{}{
				"type":        "boolean",
				"description": "Replace all occurrences (default: false, only first match)",
			},
			"dry_run": map[string]interface{}{
				"type":        "boolean",
				"description": "Validate the edit and return the diff without writing the file.",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath, _ := params["file_path"].(string)
	oldStr, _ := params["old_string"].(string)
	newStr, _ := params["new_string"].(string)
	replaceAll := false
	if b, ok := params["replace_all"].(bool); ok {
		replaceAll = b
	}
	dryRun := false
	if b, ok := params["dry_run"].(bool); ok {
		dryRun = b
	}

	if filePath == "" || oldStr == "" {
		return "", fmt.Errorf("file_path and old_string are required")
	}
	if oldStr == newStr {
		return "", fmt.Errorf("old_string and new_string must be different")
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, filePath); err != nil {
		return "", err
	}

	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(t.workspace, filePath)
	}
	// Path safety: prevent traversal attacks on relative paths
	if safePath, err := t.deps.SafeJoin(t.workspace, filePath); err == nil {
		filePath = safePath
	}

	// System directory protection
	if t.deps.IsProtectedPath(filePath) {
		return "", fmt.Errorf("BLOCKED: '%s' is a system-protected path. %s", filePath, t.deps.ProtectionWarning(filePath))
	}

	// Shared lock serializes every OpenKitty file mutator for this path.
	GetFileStateRegistry().LockPath(filePath)
	defer GetFileStateRegistry().UnlockPath(filePath)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	content := string(data)

	// Step 1: exact match
	idx := strings.Index(content, oldStr)
	matchedOld := oldStr
	matchedContent := content

	// Step 2: if exact match fails, try quote normalization + trailing whitespace stripping
	if idx < 0 {
		normalizedOld, normalizedContent := normalizeEditStrings(oldStr, content)
		if normalizedOld != oldStr {
			idx = strings.Index(normalizedContent, normalizedOld)
			if idx >= 0 {
				matchedOld = normalizedOld
				matchedContent = normalizedContent
			}
		}
	}

	if idx < 0 {
		return "", newEditFileStaleContextError(filePath, t.workspace, oldStr, content)
	}

	// Step 3: count matches and reject ambiguous replacements
	count := strings.Count(matchedContent, matchedOld)
	if !replaceAll && count > 1 {
		positions := make([]int, 0, count)
		pos := 0
		for {
			i := strings.Index(matchedContent[pos:], matchedOld)
			if i < 0 {
				break
			}
			positions = append(positions, pos+i)
			pos += i + len(matchedOld)
		}
		lineNums := make([]int, len(positions))
		for i, p := range positions {
			lineNums[i] = strings.Count(matchedContent[:p], "\n") + 1
		}
		return "", fmt.Errorf(
			"old_string found %d times in file (lines %v). "+
				"Use replace_all=true to replace all occurrences, "+
				"or add more surrounding context (2-3 lines before/after) to make old_string unique.",
			count, lineNums,
		)
	}
	if replaceAll && !dryRun && count > 1 && isDangerousReplaceAllOldString(matchedOld) {
		return "", fmt.Errorf(
			"replace_all=true rejected: old_string %q is too short or low-signal and matches %d times. "+
				"Run dry_run=true to inspect the diff, or use a longer unique block for a deliberate mechanical change.",
			matchedOld, count,
		)
	}

	var result string
	if replaceAll {
		result = strings.ReplaceAll(matchedContent, matchedOld, newStr)
	} else {
		result = strings.Replace(matchedContent, matchedOld, newStr, 1)
		count = 1
	}

	// Enforce read-before-edit: must have read the file first
	if err := GetFileStateRegistry().MustHaveRead(filePath, ReaderIDFromContext(ctx)); err != nil {
		return "", err
	}

	rel, _ := filepath.Rel(t.workspace, filePath)

	// Build a context diff showing what changed
	diff := buildChangeDiff(matchedContent, idx, matchedOld, newStr, replaceAll)

	if dryRun {
		if replaceAll {
			return fmt.Sprintf("Dry run: would replace %d occurrence(s) in %s\n%s", count, rel, diff), nil
		}
		return fmt.Sprintf("Dry run: would replace 1 occurrence in %s\n%s", rel, diff), nil
	}

	// File coordination: check if another agent modified since our read
	if stale := GetFileStateRegistry().CheckBeforeWrite(filePath, ReaderIDFromContext(ctx)); !stale.OK {
		return "", fmt.Errorf("%s", stale.Message)
	}
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(filePath); statErr == nil {
		mode = info.Mode().Perm()
	}
	PreEditBackup(filePath, ctx) // file history: backup before edit
	if err := t.deps.AtomicWrite(filePath, []byte(result), mode); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	var summary string
	if replaceAll {
		summary = fmt.Sprintf("Replaced %d occurrences in %s", count, rel)
	} else {
		summary = fmt.Sprintf("Replaced 1 occurrence in %s", rel)
	}
	postEditReport, postEditErr := t.deps.FinalizeWrite(t.workspace, filePath, ReaderIDFromContext(ctx))
	summary += postEditReport
	if postEditErr != nil {
		return "", fmt.Errorf("edit was written, but its final state could not be verified: %w\n%s", postEditErr, diff)
	}
	if hint := validationhint.SuggestedForPathsInWorkspace(t.workspace, []string{filePath}); hint != "" {
		summary += hint
	}
	return summary + "\n" + diff, nil
}

func isDangerousReplaceAllOldString(oldStr string) bool {
	trimmed := strings.TrimSpace(oldStr)
	if trimmed == "" {
		return true
	}
	nonSpace := 0
	hasLetterOrDigit := false
	for _, r := range trimmed {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		nonSpace++
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r >= 0x4e00 && r <= 0x9fff {
			hasLetterOrDigit = true
		}
	}
	return nonSpace < 4 || !hasLetterOrDigit
}

func RunAutoFormat(filePath string) string {
	command, args, ok := formatterCommand(filePath)
	if !ok {
		return ""
	}
	before, _ := FingerprintFile(filePath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = filepath.Dir(filePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if ctx.Err() != nil {
			message = "formatter timed out after 30s"
		}
		if message == "" {
			message = err.Error()
		}
		return fmt.Sprintf("[Formatter warning] %s: %s", filepath.Ext(filePath), truncateUTF8Bytes(message, 4000))
	}
	after, fingerprintErr := FingerprintFile(filePath)
	if fingerprintErr == nil && before.ContentSHA256 != after.ContentSHA256 {
		return fmt.Sprintf("[Formatted] %s", filepath.Base(command))
	}
	return ""
}

func formatterCommand(filePath string) (string, []string, bool) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		if command, err := exec.LookPath("gofmt"); err == nil {
			return command, []string{"-w", filePath}, true
		}
	case ".py":
		if command, err := exec.LookPath("black"); err == nil {
			return command, []string{"--quiet", filePath}, true
		}
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".json", ".yaml", ".yml", ".md", ".css", ".html":
		if node, err := exec.LookPath("node"); err == nil {
			if script := findProjectNodeModuleFile(filePath,
				filepath.Join("prettier", "bin", "prettier.cjs"),
				filepath.Join("prettier", "bin-prettier.js"),
			); script != "" {
				return node, []string{script, "--write", filePath}, true
			}
		}
		if command := findProjectExecutable(filePath, "prettier"); command != "" {
			return wrapWindowsCommandScript(command, []string{"--write", filePath})
		}
		if command, err := exec.LookPath("prettier"); err == nil {
			return wrapWindowsCommandScript(command, []string{"--write", filePath})
		}
	}
	return "", nil, false
}

func findProjectNodeModuleFile(filePath string, relativeCandidates ...string) string {
	dir := filepath.Dir(filePath)
	for {
		for _, relative := range relativeCandidates {
			candidate := filepath.Join(dir, "node_modules", relative)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func wrapWindowsCommandScript(command string, args []string) (string, []string, bool) {
	if runtime.GOOS != "windows" || (filepath.Ext(command) != ".cmd" && filepath.Ext(command) != ".bat") {
		return command, args, true
	}
	commandInterpreter := os.Getenv("ComSpec")
	if commandInterpreter == "" {
		commandInterpreter = "cmd.exe"
	}
	return commandInterpreter, append([]string{"/d", "/s", "/c", command}, args...), true
}

func findProjectExecutable(filePath, name string) string {
	dir := filepath.Dir(filePath)
	for {
		candidates := []string{filepath.Join(dir, "node_modules", ".bin", name)}
		if runtime.GOOS == "windows" {
			candidates = append([]string{filepath.Join(dir, "node_modules", ".bin", name+".cmd")}, candidates...)
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func RunPostEditDiag(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch ext {
	case ".go":
		if findAncestorFile(filepath.Dir(filePath), "go.mod") == "" {
			return ""
		}
		cmd = exec.CommandContext(ctx, "go", "test", "-run", "^$")
		cmd.Dir = filepath.Dir(filePath)
	case ".py":
		pythonCommand, commandErr := processutil.PythonCommandContext(ctx,
			"-c",
			"import ast,pathlib,sys; ast.parse(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8'), filename=sys.argv[1])",
			filePath,
		)
		if commandErr != nil {
			return fmt.Sprintf("[Quick verification failed]\n%s", commandErr)
		}
		cmd = pythonCommand
		cmd.Dir = filepath.Dir(filePath)
	case ".js", ".mjs":
		cmd = exec.CommandContext(ctx, "node", "--check", filePath)
		cmd.Dir = filepath.Dir(filePath)
	default:
		return ""
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		message = "quick verification timed out after 45s"
	}
	if message == "" {
		message = err.Error()
	}
	return fmt.Sprintf("[Quick verification failed]\n%s\nFix the issue and rerun the affected verification.", truncateUTF8Bytes(message, 6000))
}

func truncateUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

func findAncestorFile(startDir, name string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func buildChangeDiff(content string, idx int, oldStr, newStr string, replaceAll bool) string {
	// Find the line containing the match
	before := content[:idx]
	matchLine := strings.Count(before, "\n") + 1

	// Get 2 context lines before and after
	lines := strings.Split(content, "\n")
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	ctxStart := matchLine - 3
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := matchLine + len(oldLines) + 2
	if ctxEnd > len(lines) {
		ctxEnd = len(lines)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", matchLine, len(oldLines), matchLine, len(newLines)))

	for i := ctxStart; i < ctxEnd; i++ {
		isRemoved := i >= matchLine-1 && i < matchLine-1+len(oldLines)
		isAdded := i >= matchLine-1 && i < matchLine-1+len(newLines)

		if isRemoved && isAdded && len(oldLines) == len(newLines) {
			// Modified line
			b.WriteString(fmt.Sprintf("- %s\n", lines[i]))
			b.WriteString(fmt.Sprintf("+ %s\n", strings.Replace(lines[i], oldLines[i-(matchLine-1)], newLines[i-(matchLine-1)], 1)))
		} else if isRemoved && !isAdded {
			b.WriteString(fmt.Sprintf("- %s\n", lines[i]))
		} else if !isRemoved && isAdded {
			// Show new lines for added-only context
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", lines[i]))
		}
	}

	// If old and new have different line counts, show all removed and added
	if len(oldLines) != len(newLines) {
		b.Reset()
		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", matchLine, len(oldLines), matchLine, len(newLines)))
		for i := ctxStart; i < matchLine-1; i++ {
			b.WriteString(fmt.Sprintf("  %s\n", lines[i]))
		}
		for _, l := range oldLines {
			b.WriteString(fmt.Sprintf("- %s\n", l))
		}
		for _, l := range newLines {
			b.WriteString(fmt.Sprintf("+ %s\n", l))
		}
		for i := matchLine - 1 + len(oldLines); i < ctxEnd; i++ {
			b.WriteString(fmt.Sprintf("  %s\n", lines[i]))
		}
	}

	return b.String()
}
