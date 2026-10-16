package projectctx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	defaultProjectInstructionMaxBytes     = 96 * 1024
	defaultProjectInstructionMaxFileBytes = 48 * 1024
	defaultProjectInstructionImportDepth  = 8
)

// ProjectInstructionSource describes one file that contributed project-level
// instructions. Content is intentionally kept out of JSON so the audit view can
// be exposed without duplicating or leaking the complete prompt.
type ProjectInstructionSource struct {
	Path         string   `json:"path"`
	RelativePath string   `json:"relative_path"`
	Layer        string   `json:"layer"`
	Scope        string   `json:"scope"`
	Kind         string   `json:"kind"`
	Priority     int      `json:"priority"`
	Paths        []string `json:"paths,omitempty"`
	ImportedBy   string   `json:"imported_by,omitempty"`
	Bytes        int      `json:"bytes"`
	Hash         string   `json:"sha256"`
	Truncated    bool     `json:"truncated,omitempty"`
	Content      string   `json:"-"`
}

// ProjectInstructionSet is the resolved, ordered instruction view for a
// workspace and target directory.
type ProjectInstructionSet struct {
	Root        string                     `json:"root"`
	Target      string                     `json:"target"`
	Sources     []ProjectInstructionSource `json:"sources"`
	Warnings    []string                   `json:"warnings,omitempty"`
	Fingerprint string                     `json:"fingerprint"`
	Truncated   bool                       `json:"truncated,omitempty"`
	CacheHit    bool                       `json:"cache_hit,omitempty"`
}

// Prompt renders the authoritative system-prompt section. Sources are emitted
// in ascending precedence: global, project root, then progressively deeper
// directories. Override/local files are emitted last within a directory.
func (set ProjectInstructionSet) Prompt() string {
	primaryCount := 0
	for _, source := range set.Sources {
		if source.ImportedBy == "" && strings.TrimSpace(source.Content) != "" {
			primaryCount++
		}
	}
	if primaryCount == 0 {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("## Project Instructions\n")
	prompt.WriteString("The following project instructions are authoritative for this workspace. ")
	prompt.WriteString("Apply only path-scoped rules that match the files being handled. ")
	prompt.WriteString("When instructions conflict, the later source wins: global < project root < deeper directory; local/override files win within the same directory.\n")

	for _, source := range set.Sources {
		if source.ImportedBy != "" || strings.TrimSpace(source.Content) == "" {
			continue
		}
		prompt.WriteString("\n### ")
		prompt.WriteString(source.RelativePath)
		prompt.WriteString(" [")
		prompt.WriteString(source.Layer)
		if source.Scope != "" {
			prompt.WriteString("; scope=")
			prompt.WriteString(source.Scope)
		}
		if len(source.Paths) > 0 {
			prompt.WriteString("; paths=")
			prompt.WriteString(strings.Join(source.Paths, ","))
		}
		prompt.WriteString("]\n")
		prompt.WriteString(source.Content)
		if !strings.HasSuffix(source.Content, "\n") {
			prompt.WriteString("\n")
		}
	}
	if set.Truncated {
		prompt.WriteString("\n[Some project instructions were truncated because the configured instruction budget was reached.]\n")
	}
	return strings.TrimSpace(prompt.String())
}

// AuditText returns a compact provenance report without repeating file content.
func (set ProjectInstructionSet) AuditText() string {
	var audit strings.Builder
	fmt.Fprintf(&audit, "Project instruction root: %s\n", set.Root)
	fmt.Fprintf(&audit, "Target directory: %s\n", set.Target)
	fmt.Fprintf(&audit, "Fingerprint: %s\n", set.Fingerprint)
	fmt.Fprintf(&audit, "Cache hit: %t\n", set.CacheHit)
	if len(set.Sources) == 0 {
		audit.WriteString("Sources: none\n")
	} else {
		audit.WriteString("Sources (low to high precedence):\n")
		for _, source := range set.Sources {
			fmt.Fprintf(&audit, "- p%d %s [%s, scope=%s, bytes=%d",
				source.Priority, source.RelativePath, source.Kind, source.Scope, source.Bytes)
			if len(source.Paths) > 0 {
				fmt.Fprintf(&audit, ", paths=%s", strings.Join(source.Paths, ","))
			}
			if source.ImportedBy != "" {
				fmt.Fprintf(&audit, ", imported_by=%s", source.ImportedBy)
			}
			if source.Truncated {
				audit.WriteString(", truncated")
			}
			audit.WriteString("]\n")
		}
	}
	for _, warning := range set.Warnings {
		fmt.Fprintf(&audit, "Warning: %s\n", warning)
	}
	return strings.TrimSpace(audit.String())
}

type projectInstructionCandidate struct {
	Path     string
	Layer    string
	Scope    string
	Kind     string
	Priority int
}

type projectInstructionCacheEntry struct {
	Fingerprint string
	ImportPaths []string
	Set         ProjectInstructionSet
}

// ProjectInstructionLoader resolves and caches hierarchical project
// instructions. Discovery metadata is rechecked on every load so adding,
// editing, or deleting a rule invalidates the cached content immediately.
type ProjectInstructionLoader struct {
	mu             sync.Mutex
	globalDir      string
	maxBytes       int
	maxFileBytes   int
	maxImportDepth int
	cache          map[string]projectInstructionCacheEntry
}

func NewProjectInstructionLoader(globalDir string) *ProjectInstructionLoader {
	if strings.TrimSpace(globalDir) != "" {
		globalDir = normalizeInstructionPath(globalDir)
	}
	return &ProjectInstructionLoader{
		globalDir:      globalDir,
		maxBytes:       defaultProjectInstructionMaxBytes,
		maxFileBytes:   defaultProjectInstructionMaxFileBytes,
		maxImportDepth: defaultProjectInstructionImportDepth,
		cache:          make(map[string]projectInstructionCacheEntry),
	}
}

func ExternalAgentProjectContext(workDir string) string {
	loader := NewProjectInstructionLoader(openKittyHome())
	instructions, err := loader.Load(workDir, workDir)
	if err != nil {
		return ""
	}
	return instructions.Prompt()
}

func openKittyHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openkitty"
	}
	return filepath.Join(home, ".openkitty")
}

// Load resolves project instructions between workspaceRoot and targetDir.
// targetDir defaults to workspaceRoot and must be inside it; otherwise it
// becomes a separate root to prevent unrelated parent instructions leaking in.
func (loader *ProjectInstructionLoader) Load(workspaceRoot, targetDir string) (ProjectInstructionSet, error) {
	if loader == nil {
		return ProjectInstructionSet{}, nil
	}
	root, target, err := resolveProjectInstructionPaths(workspaceRoot, targetDir)
	if err != nil {
		return ProjectInstructionSet{}, err
	}

	candidates, warnings := loader.discover(root, target)
	cacheKey := projectInstructionCacheKey(root, target)

	loader.mu.Lock()
	defer loader.mu.Unlock()

	cached, hasCached := loader.cache[cacheKey]
	fingerprint := projectInstructionMetadataFingerprint(candidates, cached.ImportPaths)
	if hasCached && fingerprint == cached.Fingerprint {
		result := cloneProjectInstructionSet(cached.Set)
		result.CacheHit = true
		return result, nil
	}

	result := ProjectInstructionSet{
		Root:     root,
		Target:   target,
		Warnings: append([]string(nil), warnings...),
	}
	remaining := loader.maxBytes
	for _, candidate := range candidates {
		stack := make(map[string]bool)
		content := loader.loadFile(candidate, "", 0, stack, &remaining, &result)
		if strings.TrimSpace(content) == "" {
			continue
		}
		// The primary source is always the first source appended by loadFile.
		for i := len(result.Sources) - 1; i >= 0; i-- {
			source := &result.Sources[i]
			if sameInstructionPath(source.Path, candidate.Path) && source.ImportedBy == "" {
				source.Content = content
				break
			}
		}
		if remaining <= 0 {
			result.Truncated = true
			break
		}
	}

	importPaths := make([]string, 0)
	for _, source := range result.Sources {
		if source.ImportedBy != "" {
			importPaths = append(importPaths, source.Path)
		}
		if source.Truncated {
			result.Truncated = true
		}
	}
	result.Fingerprint = projectInstructionMetadataFingerprint(candidates, importPaths)
	result.CacheHit = false
	loader.cache[cacheKey] = projectInstructionCacheEntry{
		Fingerprint: result.Fingerprint,
		ImportPaths: append([]string(nil), importPaths...),
		Set:         cloneProjectInstructionSet(result),
	}
	return result, nil
}

func (loader *ProjectInstructionLoader) discover(root, target string) ([]projectInstructionCandidate, []string) {
	var candidates []projectInstructionCandidate
	var warnings []string
	seen := make(map[string]bool)
	priority := 0

	addDirectory := func(dir, layer, scope string) {
		baseFiles := []struct {
			relative string
			kind     string
		}{
			{relative: filepath.Join(".claude", "CLAUDE.md"), kind: "claude"},
			{relative: "CLAUDE.md", kind: "claude"},
			{relative: "AGENTS.md", kind: "agents"},
		}
		overrideFiles := []struct {
			relative string
			kind     string
		}{
			{relative: "CLAUDE.local.md", kind: "override"},
			{relative: "AGENTS.override.md", kind: "override"},
		}

		addFile := func(path, kind string) {
			path = normalizeInstructionPath(path)
			key := instructionPathKey(path)
			if seen[key] || !regularInstructionFile(path) {
				return
			}
			priority++
			seen[key] = true
			candidates = append(candidates, projectInstructionCandidate{
				Path:     path,
				Layer:    layer,
				Scope:    scope,
				Kind:     kind,
				Priority: priority,
			})
		}

		for _, item := range baseFiles {
			addFile(filepath.Join(dir, item.relative), item.kind)
		}
		for _, rulesDir := range []string{
			filepath.Join(dir, ".claude", "rules"),
			filepath.Join(dir, ".openkitty", "rules"),
		} {
			ruleFiles, err := discoverMarkdownRules(rulesDir)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("scan %s: %v", rulesDir, err))
				continue
			}
			for _, ruleFile := range ruleFiles {
				addFile(ruleFile, "rule")
			}
		}
		for _, item := range overrideFiles {
			addFile(filepath.Join(dir, item.relative), item.kind)
		}
	}

	if loader.globalDir != "" {
		addDirectory(loader.globalDir, "global", "global")
	}
	for index, dir := range instructionDirectoryChain(root, target) {
		layer := "directory"
		if index == 0 {
			layer = "project"
		}
		scope := instructionScope(root, dir)
		addDirectory(dir, layer, scope)
	}
	return candidates, warnings
}

func (loader *ProjectInstructionLoader) loadFile(
	candidate projectInstructionCandidate,
	importedBy string,
	depth int,
	stack map[string]bool,
	remaining *int,
	result *ProjectInstructionSet,
) string {
	if depth > loader.maxImportDepth {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("import depth exceeded at %s", candidate.Path))
		return ""
	}
	pathKey := instructionPathKey(candidate.Path)
	if stack[pathKey] {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("cyclic instruction import skipped: %s", candidate.Path))
		return ""
	}
	if *remaining <= 0 {
		result.Truncated = true
		return ""
	}

	raw, fileSize, truncated, err := readInstructionFile(candidate.Path, loader.maxFileBytes, *remaining)
	if err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("read %s: %v", candidate.Path, err))
		return ""
	}
	*remaining -= len(raw)
	if *remaining < 0 {
		*remaining = 0
	}
	body, paths, frontMatterWarning := parseInstructionFrontMatter(string(raw))
	if frontMatterWarning != "" {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%s: %s", candidate.Path, frontMatterWarning))
	}
	source := ProjectInstructionSource{
		Path:         candidate.Path,
		RelativePath: loader.displayPath(result.Root, candidate.Path),
		Layer:        candidate.Layer,
		Scope:        candidate.Scope,
		Kind:         candidate.Kind,
		Priority:     candidate.Priority,
		Paths:        paths,
		ImportedBy:   importedBy,
		Bytes:        len(raw),
		Hash:         instructionHash(raw),
		Truncated:    truncated || fileSize > int64(len(raw)),
	}
	result.Sources = append(result.Sources, source)

	stack[pathKey] = true
	defer delete(stack, pathKey)

	var expanded strings.Builder
	lines := strings.SplitAfter(body, "\n")
	for _, line := range lines {
		importPath, ok := parseInstructionImport(line)
		if !ok {
			expanded.WriteString(line)
			continue
		}
		resolvedImport, err := resolveInstructionImport(candidate.Path, importPath)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("resolve import %q from %s: %v", importPath, candidate.Path, err))
			expanded.WriteString(line)
			continue
		}
		importCandidate := candidate
		importCandidate.Path = resolvedImport
		importCandidate.Kind = "import"
		imported := loader.loadFile(importCandidate, source.RelativePath, depth+1, stack, remaining, result)
		if strings.TrimSpace(imported) == "" {
			expanded.WriteString(line)
			continue
		}
		fmt.Fprintf(&expanded, "<!-- imported from %s -->\n", loader.displayPath(result.Root, resolvedImport))
		expanded.WriteString(imported)
		if !strings.HasSuffix(imported, "\n") {
			expanded.WriteString("\n")
		}
		fmt.Fprintf(&expanded, "<!-- end import %s -->\n", loader.displayPath(result.Root, resolvedImport))
	}
	return strings.TrimSpace(expanded.String())
}

func (loader *ProjectInstructionLoader) displayPath(root, path string) string {
	path = normalizeInstructionPath(path)
	if loader.globalDir != "" && pathWithinInstructionRoot(path, loader.globalDir) {
		relative, err := filepath.Rel(loader.globalDir, path)
		if err == nil {
			return filepath.ToSlash(filepath.Join(".openkitty", relative))
		}
	}
	if pathWithinInstructionRoot(path, root) {
		relative, err := filepath.Rel(root, path)
		if err == nil {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.ToSlash(path)
}

func resolveProjectInstructionPaths(workspaceRoot, targetDir string) (string, string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	root := normalizeInstructionPath(workspaceRoot)
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	if targetDir == "" {
		targetDir = root
	}
	target := normalizeInstructionPath(targetDir)
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		target = filepath.Dir(target)
	}
	if !pathWithinInstructionRoot(target, root) {
		root = target
	}
	return root, target, nil
}

func instructionDirectoryChain(root, target string) []string {
	root = normalizeInstructionPath(root)
	target = normalizeInstructionPath(target)
	if !pathWithinInstructionRoot(target, root) {
		return []string{target}
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." {
		return []string{root}
	}
	directories := []string{root}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		directories = append(directories, current)
	}
	return directories
}

func instructionScope(root, dir string) string {
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == "." || relative == "" {
		return "."
	}
	return filepath.ToSlash(relative)
}

func discoverMarkdownRules(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			files = append(files, normalizeInstructionPath(path))
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool {
		return instructionPathKey(files[i]) < instructionPathKey(files[j])
	})
	return files, err
}

func readInstructionFile(path string, maxFileBytes, remaining int) ([]byte, int64, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	limit := maxFileBytes
	if remaining < limit {
		limit = remaining
	}
	if limit < 0 {
		limit = 0
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, info.Size(), false, err
	}
	truncated := len(data) > limit || info.Size() > int64(limit)
	if len(data) > limit {
		data = data[:limit]
	}
	data = []byte(strings.ToValidUTF8(string(data), "\uFFFD"))
	return data, info.Size(), truncated, nil
}

func parseInstructionFrontMatter(content string) (string, []string, string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return content, nil, ""
	}
	rest := normalized[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return content, nil, "unterminated YAML front matter"
	}
	rawFrontMatter := rest[:end]
	body := rest[end+len("\n---\n"):]
	var frontMatter map[string]interface{}
	if err := yaml.Unmarshal([]byte(rawFrontMatter), &frontMatter); err != nil {
		return body, nil, fmt.Sprintf("invalid YAML front matter: %v", err)
	}
	return body, normalizeInstructionPaths(frontMatter["paths"]), ""
}

func normalizeInstructionPaths(value interface{}) []string {
	var paths []string
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			paths = append(paths, strings.TrimSpace(typed))
		}
	case []interface{}:
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				paths = append(paths, text)
			}
		}
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				paths = append(paths, text)
			}
		}
	}
	return paths
}

func parseInstructionImport(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "@") || len(trimmed) == 1 {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, "@"))
	if strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	if value == "" {
		return "", false
	}
	return value, true
}

func resolveInstructionImport(parentPath, imported string) (string, error) {
	imported = strings.TrimSpace(imported)
	if strings.HasPrefix(imported, "~/") || strings.HasPrefix(imported, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		imported = filepath.Join(home, imported[2:])
	} else if !filepath.IsAbs(imported) {
		imported = filepath.Join(filepath.Dir(parentPath), imported)
	}
	resolved := normalizeInstructionPath(imported)
	if !regularInstructionFile(resolved) {
		return "", fmt.Errorf("file does not exist or is not a regular file")
	}
	return resolved, nil
}

func regularInstructionFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func pathWithinInstructionRoot(path, root string) bool {
	path = normalizeInstructionPath(path)
	root = normalizeInstructionPath(root)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func normalizeInstructionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func NormalizeInstructionPath(path string) string {
	return normalizeInstructionPath(path)
}

func instructionPathKey(path string) string {
	path = normalizeInstructionPath(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func sameInstructionPath(left, right string) bool {
	return instructionPathKey(left) == instructionPathKey(right)
}

func projectInstructionCacheKey(root, target string) string {
	return instructionPathKey(root) + "\x00" + instructionPathKey(target)
}

func projectInstructionMetadataFingerprint(candidates []projectInstructionCandidate, importedPaths []string) string {
	hash := sha256.New()
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		path := normalizeInstructionPath(candidate.Path)
		key := instructionPathKey(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		writeInstructionStatFingerprint(hash, path)
	}
	sortedImports := append([]string(nil), importedPaths...)
	sort.Slice(sortedImports, func(i, j int) bool {
		return instructionPathKey(sortedImports[i]) < instructionPathKey(sortedImports[j])
	})
	for _, path := range sortedImports {
		path = normalizeInstructionPath(path)
		key := instructionPathKey(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		writeInstructionStatFingerprint(hash, path)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeInstructionStatFingerprint(writer io.Writer, path string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(writer, "%s\x00missing\x00", instructionPathKey(path))
		return
	}
	fmt.Fprintf(writer, "%s\x00%d\x00%d\x00%d\x00",
		instructionPathKey(path), info.Size(), info.ModTime().UnixNano(), info.Mode())
}

func instructionHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func cloneProjectInstructionSet(source ProjectInstructionSet) ProjectInstructionSet {
	cloned := source
	cloned.Sources = append([]ProjectInstructionSource(nil), source.Sources...)
	for index := range cloned.Sources {
		cloned.Sources[index].Paths = append([]string(nil), source.Sources[index].Paths...)
	}
	cloned.Warnings = append([]string(nil), source.Warnings...)
	return cloned
}

func (loader *ProjectInstructionLoader) LoadInstructions(workspace, target string) (string, string, error) {
	instructions, err := loader.Load(workspace, target)
	if err != nil {
		return "", "", err
	}
	return instructions.AuditText(), instructions.Prompt(), nil
}
