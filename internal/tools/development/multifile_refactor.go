package development

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// MultiFileRefactorTool analyzes and refactors across multiple files.
// Finds cross-file code duplication, suggests symbol moves, and
// detects import cycles.
type MultiFileRefactorTool struct {
	workspace string
}

func NewMultiFileRefactorTool(workspace string) *MultiFileRefactorTool {
	return &MultiFileRefactorTool{workspace: workspace}
}

func (t *MultiFileRefactorTool) Name() string { return "refactor_multi" }

func (t *MultiFileRefactorTool) Description() string {
	return "Cross-file refactoring analysis. " +
		"Actions: duplicates (find duplicated code across files), " +
		"move (suggest symbols that belong in other files), " +
		"cycles (detect import cycles), " +
		"cohesion (analyze file cohesion and suggest splits)."
}

func (t *MultiFileRefactorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"duplicates", "move", "cycles", "cohesion"},
				"description": "duplicates: cross-file code duplication. move: suggest symbol relocation. cycles: detect import cycles. cohesion: file cohesion analysis.",
			},
			"dir": map[string]interface{}{
				"type":        "string",
				"description": "Directory to analyze (default: workspace)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MultiFileRefactorTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "duplicates")
	dir := configvalue.String(params, "dir", t.workspace)

	switch action {
	case "duplicates":
		return t.findDuplicates(dir)
	case "move":
		return t.suggestMoves(dir)
	case "cycles":
		return t.detectCycles(dir)
	case "cohesion":
		return t.analyzeCohesion(dir)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Cross-file Duplicates ==================

type dupBlock struct {
	Content string
	Files   []string
	Lines   []int
	Count   int
}

func (t *MultiFileRefactorTool) findDuplicates(dir string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Cross-File Duplication: `%s`\n\n", dir))

	// Collect all function bodies across Go files
	type funcBody struct {
		File    string
		Name    string
		Line    int
		Content string
	}

	var bodies []funcBody
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil
		}

		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Normalize body content (strip whitespace, normalize identifiers)
			content := normalizeCode(string(src[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset]))
			if len(content) > 50 {
				bodies = append(bodies, funcBody{
					File:    path,
					Name:    fn.Name.Name,
					Line:    fset.Position(fn.Pos()).Line,
					Content: content,
				})
			}
		}
		return nil
	})

	// Find similar bodies
	var dups []dupBlock
	seen := make(map[string]bool)

	for i := 0; i < len(bodies); i++ {
		if seen[bodies[i].Name] {
			continue
		}
		block := dupBlock{
			Content: bodies[i].Content,
			Files:   []string{bodies[i].File},
			Lines:   []int{bodies[i].Line},
		}

		for j := i + 1; j < len(bodies); j++ {
			if seen[bodies[j].Name] {
				continue
			}
			if similarity(bodies[i].Content, bodies[j].Content) > 0.85 {
				block.Files = append(block.Files, bodies[j].File)
				block.Lines = append(block.Lines, bodies[j].Line)
				seen[bodies[j].Name] = true
			}
		}

		if len(block.Files) >= 2 {
			block.Count = len(block.Files)
			dups = append(dups, block)
			seen[bodies[i].Name] = true
		}
	}

	if len(dups) == 0 {
		sb.WriteString("✅ No significant code duplication found across files.\n")
		return sb.String(), nil
	}

	// Sort by count descending
	sort.Slice(dups, func(i, j int) bool { return dups[i].Count > dups[j].Count })

	sb.WriteString(fmt.Sprintf("### %d Duplicated Blocks Found\n\n", len(dups)))
	for _, d := range dups {
		sb.WriteString(fmt.Sprintf("#### Repeated %d times\n\n", d.Count))
		for i, f := range d.Files {
			rel, _ := filepath.Rel(dir, f)
			sb.WriteString(fmt.Sprintf("- `%s` (line %d)\n", rel, d.Lines[i]))
		}
		sb.WriteString(fmt.Sprintf("\n```go\n%s\n```\n\n", textutil.Truncate(d.Content, 200)))
	}

	sb.WriteString("**Suggestion:** Extract shared code into a common function or utility file.\n")
	return sb.String(), nil
}

// ================== Suggest Moves ==================

type symbolInfo2 struct {
	Name   string
	Kind   string
	File   string
	Line   int
	UsedIn []string // files that use this symbol
}

func (t *MultiFileRefactorTool) suggestMoves(dir string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Move Suggestions: `%s`\n\n", dir))

	// Collect exported symbols and their usage
	symbols := make(map[string]*symbolInfo2)
	fileSymbols := make(map[string][]string) // file -> symbols defined

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, _ := os.ReadFile(path)
		fset := token.NewFileSet()
		node, _ := parser.ParseFile(fset, path, src, parser.ParseComments)
		if node == nil {
			return nil
		}

		for _, decl := range node.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if ast.IsExported(d.Name.Name) {
					name := d.Name.Name
					symbols[name] = &symbolInfo2{
						Name: name, Kind: "function",
						File: path, Line: fset.Position(d.Pos()).Line,
					}
					fileSymbols[path] = append(fileSymbols[path], name)
				}
			case *ast.GenDecl:
				if d.Tok == token.TYPE {
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ast.IsExported(ts.Name.Name) {
							symbols[ts.Name.Name] = &symbolInfo2{
								Name: ts.Name.Name, Kind: "type",
								File: path, Line: fset.Position(ts.Pos()).Line,
							}
							fileSymbols[path] = append(fileSymbols[path], ts.Name.Name)
						}
					}
				}
			}
		}
		return nil
	})

	// Find symbols used in only one other file — candidates for moving
	var suggestions []string
	for name, sym := range symbols {
		if len(sym.UsedIn) == 1 && sym.UsedIn[0] != sym.File {
			suggestions = append(suggestions, fmt.Sprintf("- `%s` (%s in `%s`) is only used in `%s` — consider moving it there", name, sym.Kind, filepath.Base(sym.File), filepath.Base(sym.UsedIn[0])))
		}
	}

	if len(suggestions) > 0 {
		sb.WriteString("### Symbols Used in Only One Other File\n\n")
		for _, s := range suggestions {
			sb.WriteString(s + "\n")
		}
		sb.WriteString("\n")
	}

	// Check if any file defines only one symbol used elsewhere
	for file, syms := range fileSymbols {
		if len(syms) == 1 {
			sb.WriteString(fmt.Sprintf("- `%s` defines only `%s` — consider consolidating into the usage site\n", filepath.Base(file), syms[0]))
		}
	}

	if len(suggestions) == 0 && sb.Len() == 0 {
		sb.WriteString("No symbol move suggestions at this time.\n")
	}

	return sb.String(), nil
}

// ================== Import Cycle Detection ==================

type importGraph struct {
	Package string
	Imports map[string]bool
}

func (t *MultiFileRefactorTool) detectCycles(dir string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Import Cycle Detection: `%s`\n\n", dir))

	// Build import graph
	graph := make(map[string]map[string]bool)

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, _ := os.ReadFile(path)
		fset := token.NewFileSet()
		node, _ := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if node == nil {
			return nil
		}

		pkg := filepath.Dir(path)
		if _, ok := graph[pkg]; !ok {
			graph[pkg] = make(map[string]bool)
		}
		for _, imp := range node.Imports {
			impPath := strings.Trim(imp.Path.Value, "\"")
			graph[pkg][impPath] = true
		}
		return nil
	})

	// Detect cycles via DFS
	type cycle struct {
		Path []string
	}
	var cycles []cycle
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(node string, path []string) bool
	dfs = func(node string, path []string) bool {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for dep := range graph[node] {
			if !visited[dep] {
				if dfs(dep, path) {
					return true
				}
			} else if recStack[dep] {
				// Find start of cycle
				cycleStart := -1
				for i, p := range path {
					if p == dep {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cycles = append(cycles, cycle{Path: path[cycleStart:]})
				}
				return true
			}
		}
		recStack[node] = false
		return false
	}

	for node := range graph {
		if !visited[node] {
			dfs(node, nil)
		}
	}

	if len(cycles) == 0 {
		sb.WriteString("✅ No import cycles detected.\n")
	} else {
		sb.WriteString(fmt.Sprintf("⚠️ **%d import cycle(s) found:**\n\n", len(cycles)))
		for i, c := range cycles {
			sb.WriteString(fmt.Sprintf("#### Cycle %d\n\n", i+1))
			sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", strings.Join(c.Path, " → ")))
		}
		sb.WriteString("**Fix:** Extract shared code into a common package or use interfaces to break cycles.\n")
	}

	return sb.String(), nil
}

// ================== Cohesion Analysis ==================

func (t *MultiFileRefactorTool) analyzeCohesion(dir string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## File Cohesion Analysis: `%s`\n\n", dir))

	type fileStats struct {
		Path       string
		Lines      int
		Functions  int
		Types      int
		Categories []string // inferred categories from function names
	}

	var files []fileStats

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, _ := os.ReadFile(path)
		fset := token.NewFileSet()
		node, _ := parser.ParseFile(fset, path, src, parser.ParseComments)
		if node == nil {
			return nil
		}

		fs := fileStats{Path: path, Lines: len(strings.Split(string(src), "\n"))}
		categories := make(map[string]int)

		for _, decl := range node.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				fs.Functions++
				cat := categorizeFunc(d.Name.Name)
				categories[cat]++
			case *ast.GenDecl:
				if d.Tok == token.TYPE {
					for range d.Specs {
						fs.Types++
					}
				}
			}
		}

		for cat, count := range categories {
			if count >= 2 {
				fs.Categories = append(fs.Categories, cat)
			}
		}
		files = append(files, fs)
		return nil
	})

	sort.Slice(files, func(i, j int) bool { return files[i].Lines > files[j].Lines })

	for _, f := range files {
		if f.Lines > 300 {
			rel, _ := filepath.Rel(dir, f.Path)
			sb.WriteString(fmt.Sprintf("### `%s` (%d lines, %d funcs, %d types)\n\n", rel, f.Lines, f.Functions, f.Types))

			if len(f.Categories) > 1 {
				sb.WriteString(fmt.Sprintf("⚠️ Mixed responsibilities: %s\n\n", strings.Join(f.Categories, ", ")))
				sb.WriteString(fmt.Sprintf("**Suggestion:** Split into separate files by concern (%s).\n\n", strings.Join(f.Categories, ", ")))
			} else if f.Functions > 15 {
				sb.WriteString(fmt.Sprintf("ℹ️ Large file with %d functions — consider splitting by sub-feature.\n\n", f.Functions))
			} else {
				sb.WriteString("Cohesion looks reasonable.\n\n")
			}
		}
	}

	if sb.Len() == len(fmt.Sprintf("## File Cohesion Analysis: `%s`\n\n", dir)) {
		sb.WriteString("All files are reasonably sized and cohesive.\n")
	}

	return sb.String(), nil
}

// ================== Helpers ==================

func normalizeCode(code string) string {
	// Remove whitespace, comments, and normalize
	var result strings.Builder
	inLineComment := false
	inBlockComment := false
	inString := false
	var stringChar byte

	for i := 0; i < len(code); i++ {
		ch := code[i]

		if inString {
			result.WriteByte(ch)
			if ch == stringChar && (i == 0 || code[i-1] != '\\') {
				inString = false
			}
			continue
		}

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(code) && code[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if ch == '/' && i+1 < len(code) {
			if code[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if code[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		if ch == '"' || ch == '\'' || ch == '`' {
			inString = true
			stringChar = ch
			result.WriteByte(ch)
			continue
		}

		// Skip whitespace
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			continue
		}

		result.WriteByte(ch)
	}
	return result.String()
}

func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) < 20 || len(b) < 20 {
		return 0
	}

	// Simple Jaccard-like similarity on character trigrams
	trigrams := make(map[string]int)
	for i := 0; i < len(a)-2; i++ {
		trigrams[a[i:i+3]]++
	}

	common := 0
	for i := 0; i < len(b)-2; i++ {
		tri := b[i : i+3]
		if count, ok := trigrams[tri]; ok && count > 0 {
			common++
			trigrams[tri]--
		}
	}

	total := (len(a) + len(b) - 4) / 3
	if total == 0 {
		return 0
	}
	return float64(common) / float64(total)
}

func categorizeFunc(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "handle") || strings.Contains(name, "serve") || strings.Contains(name, "route"):
		return "HTTP/routing"
	case strings.Contains(name, "read") || strings.Contains(name, "write") || strings.Contains(name, "open") || strings.Contains(name, "save"):
		return "I/O"
	case strings.Contains(name, "parse") || strings.Contains(name, "decode") || strings.Contains(name, "encode") || strings.Contains(name, "marshal"):
		return "Serialization"
	case strings.Contains(name, "validate") || strings.Contains(name, "check"):
		return "Validation"
	case strings.Contains(name, "new") || strings.Contains(name, "create") || strings.Contains(name, "build"):
		return "Construction"
	case strings.Contains(name, "get") || strings.Contains(name, "find") || strings.Contains(name, "list") || strings.Contains(name, "search"):
		return "Data access"
	case strings.Contains(name, "test") || strings.Contains(name, "mock"):
		return "Testing"
	case strings.Contains(name, "run") || strings.Contains(name, "exec") || strings.Contains(name, "start") || strings.Contains(name, "stop"):
		return "Lifecycle"
	case strings.Contains(name, "convert") || strings.Contains(name, "transform") || strings.Contains(name, "format"):
		return "Transformation"
	default:
		return "General"
	}
}
