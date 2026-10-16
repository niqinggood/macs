package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type PathGuard func(ctx context.Context, workspace, requestedPath string) error

type GlobTool struct {
	workspace string
	pathGuard PathGuard
}

func NewGlobTool(workspace string, pathGuard PathGuard) *GlobTool {
	return &GlobTool{workspace: workspace, pathGuard: pathGuard}
}

func (t *GlobTool) Name() string { return "glob" }
func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern before grep/read. " +
		"Use this to discover source, tests, configs, routes, and docs by name (e.g. '**/*_test.go', 'src/**/*.tsx', '*.yaml'). " +
		"Returns matching file paths sorted by modification time (newest first). Use glob for file discovery, grep for content search."
}

func (t *GlobTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Glob pattern (e.g. '**/*.go', '*.md', 'src/**/*_test.py', '**/*.{js,ts}')",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Subdirectory to search in. Default: entire workspace.",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results. Default: 100.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchPath := t.workspace
	sub, _ := params["path"].(string)
	if t.pathGuard != nil {
		if err := t.pathGuard(ctx, t.workspace, sub); err != nil {
			return "", err
		}
	}
	if sub != "" {
		if filepath.IsAbs(sub) {
			searchPath = sub // 信任用户显式指定的绝对路径
		} else {
			searchPath = filepath.Join(t.workspace, sub)
		}
	}

	maxResults := 100
	if n, ok := params["max_results"].(float64); ok && n > 0 {
		maxResults = int(n)
	}

	skipDirs := map[string]bool{
		".git": true, ".openkitty": true, "node_modules": true, "__pycache__": true,
		".venv": true, "vendor": true, ".next": true, "dist": true,
		"build": true, ".claude": true, ".idea": true, ".vscode": true,
		"memories": true, "logs": true, "bak": true, "oldversion": true,
	}

	type entry struct {
		path    string
		modTime int64
	}
	var results []entry

	// Determine if pattern has a directory prefix
	hasDirPrefix := strings.Contains(pattern, "/") || strings.Contains(pattern, "\\")

	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(t.workspace, path)

		if hasDirPrefix {
			if !matchWorkspaceGlob(pattern, rel, info.Name()) {
				return nil
			}
		} else if !matchWorkspaceGlob(pattern, rel, info.Name()) {
			return nil
		}

		results = append(results, entry{path: rel, modTime: info.ModTime().Unix()})
		if len(results) > maxResults*2 {
			return filepath.SkipAll
		}
		return nil
	})

	sort.Slice(results, func(i, j int) bool {
		return results[i].modTime > results[j].modTime
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}

	if len(results) == 0 {
		return fmt.Sprintf("No files matching '%s'", pattern), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d files matching '%s':\n", len(results), pattern))
	for _, e := range results {
		b.WriteString(e.path)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func matchWorkspaceGlob(pattern, rel, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	rel = filepath.ToSlash(rel)
	name = filepath.ToSlash(name)
	for _, p := range expandBraceGlob(filepath.ToSlash(pattern)) {
		target := name
		if strings.Contains(p, "/") {
			target = rel
		}
		if globPatternToRegex(p).MatchString(target) {
			return true
		}
	}
	return false
}

func expandBraceGlob(pattern string) []string {
	start := strings.Index(pattern, "{")
	if start < 0 {
		return []string{pattern}
	}
	endRel := strings.Index(pattern[start:], "}")
	if endRel < 0 {
		return []string{pattern}
	}
	end := start + endRel
	prefix, suffix := pattern[:start], pattern[end+1:]
	parts := strings.Split(pattern[start+1:end], ",")
	var expanded []string
	for _, part := range parts {
		for _, rest := range expandBraceGlob(suffix) {
			expanded = append(expanded, prefix+part+rest)
		}
	}
	return expanded
}

func globPatternToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if strings.HasPrefix(pattern[i:], "**/") {
				b.WriteString("(?:.*/)?")
				i += 3
			} else if strings.HasPrefix(pattern[i:], "**") {
				b.WriteString(".*")
				i += 2
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
