package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/fsutil"
)

type ProjectMapTool struct{ workspace string }

func NewProjectMapTool(workspace string) *ProjectMapTool {
	return &ProjectMapTool{workspace: workspace}
}

func (t *ProjectMapTool) Name() string { return "project_map" }

func (t *ProjectMapTool) IsReadOnly() bool { return true }

func (t *ProjectMapTool) Description() string {
	return "Build a concise project map before coding. Detects languages/frameworks, key config files, likely entry points, test/build commands, important directories, and suggested files to read next. Use this at the start of unfamiliar codebase work."
}

func (t *ProjectMapTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Project root path. Defaults to workspace.",
			},
			"max_files": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of files to scan. Default: 500.",
			},
			"refresh": map[string]interface{}{
				"type":        "boolean",
				"description": "Force a fresh scan and update the runtime cache.",
			},
			"use_cache": map[string]interface{}{
				"type":        "boolean",
				"description": "Use a recent project_map cache when available. Default: true.",
			},
		},
	}
}

type projectMapCache struct {
	Root        string    `json:"root"`
	MaxFiles    int       `json:"max_files"`
	GeneratedAt time.Time `json:"generated_at"`
	Output      string    `json:"output"`
}

type projectMapInfo struct {
	Root            string
	Languages       []string
	Frameworks      []string
	PackageManagers []string
	ConfigFiles     []string
	EntryPoints     []string
	TestFiles       []string
	TestDirs        []string
	SourceTestPairs []string
	CodeAnchors     []string
	TestCommands    []string
	BuildCommands   []string
	DevCommands     []string
	KeyDirs         []string
	SuggestedReads  []string
	ScannedFiles    int
}

func (t *ProjectMapTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := configvalue.String(params, "path", t.workspace)
	if root == "" {
		root = "."
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(t.workspace, root)
	}
	root = filepath.Clean(root)
	maxFiles := configvalue.Int(params, "max_files", 500)
	if maxFiles <= 0 {
		maxFiles = 500
	}
	refresh := configvalue.Bool(params, "refresh", false)
	useCache := configvalue.Bool(params, "use_cache", true)

	if useCache && !refresh {
		if cached, ok := readProjectMapCache(root, maxFiles, 5*time.Minute); ok {
			return cached, nil
		}
	}

	info, err := buildProjectMap(ctx, root, maxFiles)
	if err != nil {
		return "", err
	}
	output := formatProjectMap(info)
	_ = writeProjectMapCache(root, maxFiles, output)
	return output, nil
}

func projectMapCachePath(root string) string {
	return filepath.Join(root, ".openkitty", "runtime", "project-map.json")
}

func readProjectMapCache(root string, maxFiles int, maxAge time.Duration) (string, bool) {
	data, err := os.ReadFile(projectMapCachePath(root))
	if err != nil {
		return "", false
	}
	var cache projectMapCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return "", false
	}
	if filepath.Clean(cache.Root) != filepath.Clean(root) || cache.MaxFiles != maxFiles || cache.Output == "" {
		return "", false
	}
	if cache.GeneratedAt.IsZero() || time.Since(cache.GeneratedAt) > maxAge {
		return "", false
	}
	return cache.Output + "\nCache: hit. Use refresh=true to rescan.\n", true
}

func writeProjectMapCache(root string, maxFiles int, output string) error {
	path := projectMapCachePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(projectMapCache{
		Root:        filepath.Clean(root),
		MaxFiles:    maxFiles,
		GeneratedAt: time.Now(),
		Output:      output,
	}, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, data, 0o644)
}

func buildProjectMap(ctx context.Context, root string, maxFiles int) (projectMapInfo, error) {
	stat, err := os.Stat(root)
	if err != nil {
		return projectMapInfo{}, fmt.Errorf("project root not found: %w", err)
	}
	if !stat.IsDir() {
		root = filepath.Dir(root)
	}

	info := projectMapInfo{Root: root}
	addUnique := func(dst *[]string, value string) {
		value = filepath.ToSlash(strings.TrimSpace(value))
		if value == "" {
			return
		}
		for _, existing := range *dst {
			if existing == value {
				return
			}
		}
		*dst = append(*dst, value)
	}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldSkipProjectMapDir(d.Name()) {
				return filepath.SkipDir
			}
			if strings.Count(rel, "/") == 0 {
				addUnique(&info.KeyDirs, rel)
			}
			if strings.Count(rel, "/") >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if info.ScannedFiles >= maxFiles {
			return filepath.SkipAll
		}
		info.ScannedFiles++
		name := d.Name()
		ext := strings.ToLower(filepath.Ext(name))

		switch name {
		case "go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json",
			"pyproject.toml", "requirements.txt", "setup.py", "pytest.ini", "Cargo.toml", "Makefile",
			"makefile", "justfile", "Justfile", "Taskfile.yml", "Taskfile.yaml", "README.md", "README",
			"Dockerfile", "docker-compose.yml", "compose.yml", "tsconfig.json", "vite.config.ts",
			"next.config.js", "next.config.mjs":
			addUnique(&info.ConfigFiles, rel)
			addUnique(&info.SuggestedReads, rel)
		}

		switch ext {
		case ".go":
			addUnique(&info.Languages, "Go")
			if strings.HasSuffix(name, "_test.go") {
				addUnique(&info.TestFiles, rel)
				addUnique(&info.TestDirs, filepath.ToSlash(filepath.Dir(rel)))
				addGoSourceTestPair(&info, rel, addUnique)
			}
			if name == "main.go" || strings.HasSuffix(rel, "/main.go") {
				addUnique(&info.EntryPoints, rel)
			}
		case ".py":
			addUnique(&info.Languages, "Python")
			if isPythonTestFile(rel, name) {
				addUnique(&info.TestFiles, rel)
				addUnique(&info.TestDirs, filepath.ToSlash(filepath.Dir(rel)))
				addPythonSourceTestPair(&info, rel, addUnique)
			}
			if name == "main.py" || name == "app.py" || strings.HasPrefix(rel, "src/") {
				addUnique(&info.EntryPoints, rel)
			}
		case ".js", ".jsx", ".ts", ".tsx", ".mjs":
			addUnique(&info.Languages, "Node/JS/TS")
			if isJSTestFile(rel, name) {
				addUnique(&info.TestFiles, rel)
				addUnique(&info.TestDirs, filepath.ToSlash(filepath.Dir(rel)))
				addJSSourceTestPair(&info, rel, addUnique)
			}
			if strings.HasPrefix(rel, "src/") || name == "index.ts" || name == "index.js" || name == "main.ts" || name == "main.js" {
				addUnique(&info.EntryPoints, rel)
			}
		case ".rs":
			addUnique(&info.Languages, "Rust")
			if rel == "src/main.rs" || rel == "src/lib.rs" {
				addUnique(&info.EntryPoints, rel)
			}
		}
		detectProjectCodeAnchors(path, rel, ext, &info, addUnique)
		return nil
	})
	if walkErr != nil {
		return projectMapInfo{}, fmt.Errorf("scan project: %w", walkErr)
	}

	enrichProjectMapFromFiles(root, &info)
	sortProjectMap(&info)
	return info, nil
}

func shouldSkipProjectMapDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".next", ".venv", "venv", "__pycache__",
		".openkitty", "logs", "coverage", "target", ".idea", ".vscode":
		return true
	default:
		return false
	}
}

func isPythonTestFile(rel, name string) bool {
	return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") ||
		strings.Contains(filepath.ToSlash(rel), "/tests/")
}

func isJSTestFile(rel, name string) bool {
	rel = filepath.ToSlash(rel)
	lower := strings.ToLower(name)
	return strings.Contains(rel, "/__tests__/") ||
		strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.")
}

func addGoSourceTestPair(info *projectMapInfo, testRel string, add func(*[]string, string)) {
	if !strings.HasSuffix(testRel, "_test.go") {
		return
	}
	source := strings.TrimSuffix(testRel, "_test.go") + ".go"
	add(&info.SourceTestPairs, source+" <-> "+testRel)
}

func addPythonSourceTestPair(info *projectMapInfo, testRel string, add func(*[]string, string)) {
	dir, name := filepath.ToSlash(filepath.Dir(testRel)), filepath.Base(testRel)
	var source string
	switch {
	case strings.HasPrefix(name, "test_"):
		source = strings.TrimPrefix(name, "test_")
	case strings.HasSuffix(name, "_test.py"):
		source = strings.TrimSuffix(name, "_test.py") + ".py"
	}
	if source == "" {
		return
	}
	if dir == "." || dir == "tests" {
		add(&info.SourceTestPairs, source+" <-> "+testRel)
		return
	}
	add(&info.SourceTestPairs, filepath.ToSlash(filepath.Join(dir, source))+" <-> "+testRel)
}

func addJSSourceTestPair(info *projectMapInfo, testRel string, add func(*[]string, string)) {
	dir, name := filepath.ToSlash(filepath.Dir(testRel)), filepath.Base(testRel)
	source := name
	for _, marker := range []string{".test.", ".spec."} {
		if strings.Contains(source, marker) {
			source = strings.Replace(source, marker, ".", 1)
			break
		}
	}
	if source == name {
		return
	}
	if dir == "." || strings.HasSuffix(dir, "__tests__") {
		add(&info.SourceTestPairs, source+" <-> "+testRel)
		return
	}
	add(&info.SourceTestPairs, filepath.ToSlash(filepath.Join(dir, source))+" <-> "+testRel)
}

func enrichProjectMapFromFiles(root string, info *projectMapInfo) {
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, rel))
		return err == nil
	}
	add := func(dst *[]string, value string) {
		for _, existing := range *dst {
			if existing == value {
				return
			}
		}
		*dst = append(*dst, value)
	}

	if has("go.mod") {
		add(&info.Languages, "Go")
		add(&info.PackageManagers, "Go modules")
		add(&info.TestCommands, "go test ./...")
		add(&info.BuildCommands, "go build ./...")
	}
	if has("Cargo.toml") {
		add(&info.Languages, "Rust")
		add(&info.PackageManagers, "Cargo")
		add(&info.TestCommands, "cargo test")
		add(&info.BuildCommands, "cargo build")
	}
	if has("pyproject.toml") || has("requirements.txt") || has("setup.py") {
		add(&info.Languages, "Python")
		if has("pyproject.toml") {
			add(&info.PackageManagers, "pyproject")
		}
		if has("requirements.txt") {
			add(&info.PackageManagers, "pip")
		}
		add(&info.TestCommands, "pytest")
	}
	if has("package.json") {
		add(&info.Languages, "Node/JS/TS")
		addNodePackageManager(info, has, add)
		enrichFromPackageJSON(filepath.Join(root, "package.json"), info)
	}
	enrichFromMakefile(filepath.Join(root, "Makefile"), info)
	enrichFromMakefile(filepath.Join(root, "makefile"), info)
	enrichFromJustfile(filepath.Join(root, "justfile"), info)
	enrichFromJustfile(filepath.Join(root, "Justfile"), info)
	enrichFromTaskfile(filepath.Join(root, "Taskfile.yml"), info)
	enrichFromTaskfile(filepath.Join(root, "Taskfile.yaml"), info)
}

func enrichFromPackageJSON(path string, info *projectMapInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
		Deps    map[string]string `json:"dependencies"`
		DevDeps map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}
	add := func(dst *[]string, value string) {
		for _, existing := range *dst {
			if existing == value {
				return
			}
		}
		*dst = append(*dst, value)
	}
	for name := range pkg.Scripts {
		switch name {
		case "test":
			add(&info.TestCommands, "npm test")
		case "build":
			add(&info.BuildCommands, "npm run build")
		case "dev":
			add(&info.DevCommands, "npm run dev")
		}
	}
	deps := make(map[string]string)
	for k, v := range pkg.Deps {
		deps[k] = v
	}
	for k, v := range pkg.DevDeps {
		deps[k] = v
	}
	for dep := range deps {
		switch dep {
		case "react":
			add(&info.Frameworks, "React")
		case "next":
			add(&info.Frameworks, "Next.js")
		case "vite":
			add(&info.Frameworks, "Vite")
		case "vue":
			add(&info.Frameworks, "Vue")
		case "express":
			add(&info.Frameworks, "Express")
		case "jest":
			add(&info.Frameworks, "Jest")
		case "vitest":
			add(&info.Frameworks, "Vitest")
		}
	}
}

func addNodePackageManager(info *projectMapInfo, has func(string) bool, add func(*[]string, string)) {
	switch {
	case has("pnpm-lock.yaml"):
		add(&info.PackageManagers, "pnpm")
	case has("yarn.lock"):
		add(&info.PackageManagers, "yarn")
	case has("package-lock.json"):
		add(&info.PackageManagers, "npm")
	default:
		add(&info.PackageManagers, "npm")
	}
}

func enrichFromMakefile(path string, info *projectMapInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	addCommandTargets(string(data), "make", info)
}

func enrichFromJustfile(path string, info *projectMapInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	addCommandTargets(string(data), "just", info)
}

func enrichFromTaskfile(path string, info *projectMapInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	addCommandTargets(string(data), "task", info)
}

func addCommandTargets(content, runner string, info *projectMapInfo) {
	lines := strings.Split(content, "\n")
	add := func(dst *[]string, value string) {
		for _, existing := range *dst {
			if existing == value {
				return
			}
		}
		*dst = append(*dst, value)
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") || strings.HasPrefix(line, "-") {
			continue
		}
		target := commandTargetName(line)
		switch target {
		case "test", "tests", "check", "verify":
			add(&info.TestCommands, runner+" "+target)
		case "build", "compile":
			add(&info.BuildCommands, runner+" "+target)
		case "dev", "serve", "server", "run":
			add(&info.DevCommands, runner+" "+target)
		}
	}
}

func commandTargetName(line string) string {
	if idx := strings.Index(line, ":"); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSuffix(fields[0], ":")
}

func detectProjectCodeAnchors(path, rel, ext string, info *projectMapInfo, add func(*[]string, string)) {
	switch ext {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".mjs":
	default:
		return
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Size() > 512*1024 {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	addedForFile := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		label := projectAnchorLabel(ext, trimmed)
		if label == "" {
			continue
		}
		add(&info.CodeAnchors, fmt.Sprintf("%s:%d - %s", rel, i+1, label))
		add(&info.SuggestedReads, rel)
		addedForFile++
		if addedForFile >= 5 || len(info.CodeAnchors) >= 40 {
			return
		}
	}
}

func projectAnchorLabel(ext, line string) string {
	lower := strings.ToLower(line)
	switch ext {
	case ".go":
		switch {
		case strings.Contains(line, ".Register(") || strings.Contains(line, "Register("):
			return "registration point"
		case strings.Contains(line, "HandleFunc(") || strings.Contains(line, ".Handle("):
			return "HTTP route"
		case strings.HasPrefix(line, "func main("):
			return "main entry"
		case strings.Contains(line, "NewToolRegistry("):
			return "tool registry setup"
		}
	case ".py":
		switch {
		case strings.Contains(line, "@app.route") || strings.Contains(line, "APIRouter("):
			return "HTTP route"
		case strings.HasPrefix(line, "if __name__ == "):
			return "script entry"
		case strings.Contains(lower, "click.group(") || strings.Contains(lower, "typer.typer("):
			return "CLI entry"
		}
	case ".js", ".jsx", ".ts", ".tsx", ".mjs":
		switch {
		case strings.Contains(line, "createBrowserRouter(") || strings.Contains(line, "<Route"):
			return "frontend route"
		case strings.Contains(line, ".get(") || strings.Contains(line, ".post(") ||
			strings.Contains(line, ".put(") || strings.Contains(line, ".delete("):
			return "HTTP route"
		case strings.Contains(line, "app.use(") || strings.Contains(line, "router.use("):
			return "middleware/route registration"
		case strings.HasPrefix(line, "export default function") || strings.HasPrefix(line, "export function"):
			return "exported component/function"
		}
	}
	return ""
}

func sortProjectMap(info *projectMapInfo) {
	sort.Strings(info.Languages)
	sort.Strings(info.Frameworks)
	sort.Strings(info.PackageManagers)
	sort.Strings(info.ConfigFiles)
	sort.Strings(info.EntryPoints)
	sort.Strings(info.TestFiles)
	sort.Strings(info.TestDirs)
	sort.Strings(info.SourceTestPairs)
	sort.Strings(info.CodeAnchors)
	sort.Strings(info.TestCommands)
	sort.Strings(info.BuildCommands)
	sort.Strings(info.DevCommands)
	sort.Strings(info.KeyDirs)
	sort.Strings(info.SuggestedReads)
	if len(info.EntryPoints) > 12 {
		info.EntryPoints = info.EntryPoints[:12]
	}
	if len(info.TestFiles) > 12 {
		info.TestFiles = info.TestFiles[:12]
	}
	if len(info.TestDirs) > 12 {
		info.TestDirs = info.TestDirs[:12]
	}
	if len(info.SourceTestPairs) > 12 {
		info.SourceTestPairs = info.SourceTestPairs[:12]
	}
	if len(info.CodeAnchors) > 20 {
		info.CodeAnchors = info.CodeAnchors[:20]
	}
	if len(info.SuggestedReads) > 12 {
		info.SuggestedReads = info.SuggestedReads[:12]
	}
}

func formatProjectMap(info projectMapInfo) string {
	var b strings.Builder
	b.WriteString("## Project Map\n\n")
	b.WriteString(fmt.Sprintf("Root: `%s`\n", info.Root))
	b.WriteString(fmt.Sprintf("Scanned files: %d\n\n", info.ScannedFiles))
	writeProjectMapList(&b, "Languages", info.Languages)
	writeProjectMapList(&b, "Frameworks", info.Frameworks)
	writeProjectMapList(&b, "Package Managers", info.PackageManagers)
	writeProjectMapList(&b, "Key Config Files", info.ConfigFiles)
	writeProjectMapList(&b, "Key Directories", info.KeyDirs)
	writeProjectMapList(&b, "Likely Entry Points", info.EntryPoints)
	writeProjectMapList(&b, "Test Files", info.TestFiles)
	writeProjectMapList(&b, "Test Directories", info.TestDirs)
	writeProjectMapList(&b, "Source/Test Pair Hints", info.SourceTestPairs)
	writeProjectMapList(&b, "Development Anchors", info.CodeAnchors)
	writeProjectMapList(&b, "Test Commands", info.TestCommands)
	writeProjectMapList(&b, "Build/Run Commands", info.BuildCommands)
	writeProjectMapList(&b, "Dev Server Commands", info.DevCommands)
	writeProjectMapList(&b, "Suggested Reads", info.SuggestedReads)
	if len(info.SuggestedReads) > 0 {
		b.WriteString("\nNext: read the suggested files before editing, then use grep for targeted symbols/usages.\n")
	}
	return b.String()
}

func writeProjectMapList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("### " + title + "\n")
	for _, item := range items {
		b.WriteString("- " + item + "\n")
	}
	b.WriteString("\n")
}
