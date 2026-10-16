package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GrepTool struct {
	workspace string
	pathGuard PathGuard
}

const MaxDefaultGrepFileBytes = 2 * 1024 * 1024

func NewGrepTool(workspace string, pathGuard PathGuard) *GrepTool {
	return &GrepTool{workspace: workspace, pathGuard: pathGuard}
}

func (t *GrepTool) Name() string       { return "grep" }
func (t *GrepTool) MaxResultSize() int { return 5000 } // 搜索结果太长说明范围太大,帮助反馈
func (t *GrepTool) Description() string {
	return "Search file contents before broad reading. Returns matching file paths with line numbers and optional context. " +
		"Use this to find symbols, usages, routes, tests, error messages, TODOs, or any text pattern across the codebase. " +
		"Supports regex by default and literal search with literal=true. Narrow large searches with file_pattern/path and use context_lines when the surrounding code matters."
}

func (t *GrepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "The text or regex pattern to search for (case-insensitive by default)",
			},
			"file_pattern": map[string]interface{}{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g. '*.go', '*.py', '**/*.js'). Default: search all text files.",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Subdirectory or file to search in (relative to workspace). Default: entire project.",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of matches to return. Default: 50.",
			},
			"case_sensitive": map[string]interface{}{
				"type":        "boolean",
				"description": "Set to true for case-sensitive search. Default: false.",
			},
			"literal": map[string]interface{}{
				"type":        "boolean",
				"description": "Set to true to search for pattern as plain text instead of regex.",
			},
			"context_lines": map[string]interface{}{
				"type":        "integer",
				"description": "Number of context lines to show around each match. Default: 0.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
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

	searchRoot := filepath.Clean(searchPath)
	explicitFile := false
	if info, err := os.Stat(searchPath); err == nil && !info.IsDir() {
		explicitFile = true
	}

	fileGlob, _ := params["file_pattern"].(string)
	if fileGlob == "" {
		fileGlob = "*"
	}

	maxResults := 50
	if n, ok := params["max_results"].(float64); ok && n > 0 {
		maxResults = int(n)
	}

	caseSensitive := false
	if b, ok := params["case_sensitive"].(bool); ok {
		caseSensitive = b
	}
	literal := false
	if b, ok := params["literal"].(bool); ok {
		literal = b
	}

	contextLines := 0
	if n, ok := params["context_lines"].(float64); ok && n > 0 {
		contextLines = int(n)
	}

	searchPattern := pattern
	if literal {
		searchPattern = regexp.QuoteMeta(pattern)
	}
	if !caseSensitive {
		searchPattern = "(?i)" + searchPattern
	}
	regex, err := regexp.Compile(searchPattern)
	if err != nil {
		return "", fmt.Errorf("invalid grep regex %q: %w. Set literal=true to search this text literally", pattern, err)
	}

	var results []string
	totalMatches := 0

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "__pycache__": true,
		".venv": true, "vendor": true, ".next": true, "dist": true,
		"build": true, ".claude": true, ".idea": true, ".vscode": true,
		".openkitty": true,
	}

	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && filepath.Clean(path) != searchRoot && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(t.workspace, path)
		if rel == "." {
			return nil
		}

		if !matchWorkspaceGlob(fileGlob, rel, info.Name()) {
			return nil
		}

		// Skip binary files
		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}
		if !explicitFile && info.Size() > MaxDefaultGrepFileBytes {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := strings.ReplaceAll(string(data), "\r\n", "\n")
		lines := strings.Split(content, "\n")

		for idx, line := range lines {
			lineNum := idx + 1
			if regex.MatchString(line) {
				totalMatches++
				if totalMatches > maxResults {
					return filepath.SkipAll
				}

				if contextLines > 0 {
					start := lineNum - contextLines
					if start < 1 {
						start = 1
					}
					end := lineNum + contextLines
					if end > len(lines) {
						end = len(lines)
					}
					entry := fmt.Sprintf("\n%s:%d:\n", rel, lineNum)
					for i := start; i <= end; i++ {
						prefix := "  "
						if i == lineNum {
							prefix = "> "
						}
						entry += fmt.Sprintf("%s%d: %s\n", prefix, i, lines[i-1])
					}
					results = append(results, entry)
				} else {
					results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNum, line))
				}
			}
		}
		return nil
	})

	if len(results) == 0 {
		return fmt.Sprintf("No matches found for '%s'", pattern), nil
	}

	summary := fmt.Sprintf("Found %d matches for '%s':\n", totalMatches, pattern)
	return summary + strings.Join(results, "\n"), nil
}

func isBinaryExt(ext string) bool {
	binary := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".o": true, ".a": true, ".obj": true, ".lib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
		".ico": true, ".webp": true, ".svg": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".wav": true,
		".zip": true, ".tar": true, ".gz": true, ".rar": true, ".7z": true,
		".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
		".ttf": true, ".otf": true, ".woff": true, ".woff2": true,
		".db": true, ".sqlite": true, ".sqlite3": true,
	}
	return binary[ext]
}
