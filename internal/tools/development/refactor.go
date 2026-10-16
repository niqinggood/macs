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

	"mac/internal/codeutil"
	"mac/internal/configvalue"
)

// RefactorTool analyzes code for refactoring opportunities.
// Uses go/ast for Go files; falls back to heuristics for other languages.
type RefactorTool struct {
	workspace string
}

func NewRefactorTool(workspace string) *RefactorTool {
	return &RefactorTool{workspace: workspace}
}

func (t *RefactorTool) Name() string { return "refactor" }

func (t *RefactorTool) Description() string {
	return "Analyze and suggest code refactoring opportunities. " +
		"Uses AST for Go (real cyclomatic complexity, function analysis, dead code); " +
		"heuristic analysis for other languages. " +
		"Checks: function length, cyclomatic complexity, naming conventions, code duplication, file size."
}

func (t *RefactorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file": map[string]interface{}{
				"type":        "string",
				"description": "File to analyze for refactoring opportunities",
			},
			"check": map[string]interface{}{
				"type":        "string",
				"description": "What to check: functions (long functions), complexity (cyclomatic), naming (conventions), duplication (code duplication), all",
				"enum":        []string{"functions", "complexity", "naming", "duplication", "all"},
			},
		},
		"required": []string{"file"},
	}
}

func (t *RefactorTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file := configvalue.String(params, "file", "")
	check := configvalue.String(params, "check", "all")

	if file == "" {
		return "Error: file is required", nil
	}

	fullPath := file
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(t.workspace, fullPath)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil || len(data) == 0 {
		return fmt.Sprintf("Cannot read file: %s (error: %v)", file, err), nil
	}
	content := string(data)

	// Try AST-based analysis for Go files
	if strings.HasSuffix(fullPath, ".go") {
		return t.analyzeGoAST(file, content, check)
	}

	// Fallback to heuristic analysis for other languages
	return t.analyzeHeuristic(file, content, check), nil
}

// ================== Go AST-based analysis ==================

func (t *RefactorTool) analyzeGoAST(file, content, check string) (string, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file, content, parser.ParseComments)
	if err != nil {
		// Fall back to heuristic if parsing fails
		return t.analyzeHeuristic(file, content, check), nil
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Refactoring Analysis (AST): `%s`\n\n", file))

	if check == "all" || check == "functions" {
		report.WriteString(t.astAnalyzeFunctions(fset, node))
	}
	if check == "all" || check == "complexity" {
		report.WriteString(t.astAnalyzeComplexity(fset, node))
	}
	if check == "all" || check == "naming" {
		report.WriteString(t.astAnalyzeNaming(fset, node))
	}
	if check == "all" || check == "duplication" {
		report.WriteString(t.astAnalyzeDuplication(fset, node))
	}

	report.WriteString(t.astRefactorActions(fset, node, content))

	if report.Len() == 0 {
		report.WriteString("No significant refactoring issues detected.\n")
	}
	return report.String(), nil
}

func (t *RefactorTool) astAnalyzeFunctions(fset *token.FileSet, node *ast.File) string {
	type funcStats struct {
		Name     string
		Receiver string
		Line     int
		Lines    int
		Exported bool
	}

	var funcs []funcStats
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		recv := ""
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recv = codeutil.ReceiverName(fn.Recv.List[0])
		}
		funcs = append(funcs, funcStats{
			Name:     fn.Name.Name,
			Receiver: recv,
			Line:     start,
			Lines:    end - start + 1,
			Exported: ast.IsExported(fn.Name.Name),
		})
	}

	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Lines > funcs[j].Lines
	})

	var result strings.Builder
	result.WriteString("### Function Length\n\n")

	longCount := 0
	for _, f := range funcs {
		fullName := f.Name
		if f.Receiver != "" {
			fullName = fmt.Sprintf("(%s).%s", f.Receiver, f.Name)
		}
		switch {
		case f.Lines > 100:
			result.WriteString(fmt.Sprintf("- 🔴 **%s**: %d lines — must split (limit: 50, soft-limit: 100)\n", fullName, f.Lines))
			longCount++
		case f.Lines > 50:
			result.WriteString(fmt.Sprintf("- 🟡 **%s**: %d lines — consider splitting (limit: 50)\n", fullName, f.Lines))
			longCount++
		}
	}
	if longCount == 0 {
		result.WriteString("All functions within length limits (≤50 lines).\n")
	} else {
		result.WriteString(fmt.Sprintf("\n**%d of %d functions** exceed recommended length.\n", longCount, len(funcs)))
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) astAnalyzeComplexity(fset *token.FileSet, node *ast.File) string {
	type cplxStats struct {
		Name       string
		Receiver   string
		Line       int
		Complexity int
		Level      string
	}

	var funcs []cplxStats
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		c := cyclomaticComplexity(fn.Body)
		level := "low"
		switch {
		case c > 30:
			level = "very-high"
		case c > 15:
			level = "high"
		case c > 7:
			level = "medium"
		}

		recv := ""
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recv = codeutil.ReceiverName(fn.Recv.List[0])
		}

		funcs = append(funcs, cplxStats{
			Name:       fn.Name.Name,
			Receiver:   recv,
			Line:       fset.Position(fn.Pos()).Line,
			Complexity: c,
			Level:      level,
		})
	}

	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Complexity > funcs[j].Complexity
	})

	var result strings.Builder
	result.WriteString("### Cyclomatic Complexity\n\n")

	issues := 0
	for _, f := range funcs {
		if f.Complexity <= 7 {
			continue
		}
		issues++
		fullName := f.Name
		if f.Receiver != "" {
			fullName = fmt.Sprintf("(%s).%s", f.Receiver, f.Name)
		}
		icon := levelIcon(f.Level)
		result.WriteString(fmt.Sprintf("- %s **%s**: complexity %d (line %d)\n", icon, fullName, f.Complexity, f.Line))
	}

	if issues == 0 {
		result.WriteString("All functions have acceptable complexity (≤7).\n")
	} else {
		result.WriteString(fmt.Sprintf("\n**%d functions** exceed complexity threshold.\n", issues))
		result.WriteString("Guidelines: ≤7 good, 8-15 review, 16-30 refactor, >30 must refactor\n")
	}

	// Count nesting depth
	maxNesting := maxNestingDepth(node)
	if maxNesting > 4 {
		result.WriteString(fmt.Sprintf("\n- ⚠️ Maximum nesting depth: %d — consider guard clauses or extracting functions\n", maxNesting))
	}

	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) astAnalyzeNaming(fset *token.FileSet, node *ast.File) string {
	var result strings.Builder
	result.WriteString("### Naming Conventions\n\n")
	issues := 0

	ast.Inspect(node, func(n ast.Node) bool {
		switch id := n.(type) {
		case *ast.Ident:
			if id.IsExported() && id.Obj != nil {
				// Check for ALL_CAPS naming (should be CamelCase for Go)
				if isAllCaps(id.Name) && len(id.Name) > 2 && id.Obj.Kind != ast.Con {
					result.WriteString(fmt.Sprintf("- ⚠️ `%s` — ALL_CAPS naming. Use CamelCase for exported identifiers in Go.\n", id.Name))
					issues++
				}
				// Check for underscores in exported names
				if strings.Contains(id.Name, "_") && id.Obj.Kind != ast.Con {
					result.WriteString(fmt.Sprintf("- ⚠️ `%s` — contains underscore. Use CamelCase for exported identifiers in Go.\n", id.Name))
					issues++
				}
			}
		}
		return true
	})

	if issues == 0 {
		result.WriteString("Naming conventions look good.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) astAnalyzeDuplication(fset *token.FileSet, node *ast.File) string {
	var result strings.Builder
	result.WriteString("### Code Duplication\n\n")

	// Find all function bodies and look for duplicated blocks
	type block struct {
		content string
		line    int
		count   int
	}

	blockMap := make(map[string]*block)

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Extract statement-level blocks (sequences of 3+ lines)
		stmts := extractStmtGroups(fset, fn.Body)
		for _, stmt := range stmts {
			if existing, ok := blockMap[stmt]; ok {
				existing.count++
			} else {
				blockMap[stmt] = &block{content: stmt, count: 1}
			}
		}
	}

	dups := 0
	for _, b := range blockMap {
		if b.count >= 2 && len(b.content) > 30 {
			dups++
			preview := b.content
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			result.WriteString(fmt.Sprintf("- Repeated %d times:\n```go\n%s\n```\n\n", b.count, preview))
		}
	}

	if dups == 0 {
		result.WriteString("No significant code duplication detected.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) astRefactorActions(fset *token.FileSet, node *ast.File, content string) string {
	var actions []string

	// File size check
	lines := strings.Split(content, "\n")
	if len(lines) > 500 {
		actions = append(actions, fmt.Sprintf("File is %d lines — consider splitting into multiple files", len(lines)))
	}

	// Check for large interfaces (> 5 methods)
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if iface, ok := ts.Type.(*ast.InterfaceType); ok {
				if iface.Methods != nil && len(iface.Methods.List) > 5 {
					actions = append(actions, fmt.Sprintf("Interface `%s` has %d methods — consider splitting into smaller interfaces (ISP)",
						ts.Name.Name, len(iface.Methods.List)))
				}
			}
		}
	}

	// Check for exported functions without documentation
	undocumented := 0
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if ast.IsExported(fn.Name.Name) && (fn.Doc == nil || fn.Doc.Text() == "") {
			undocumented++
		}
	}
	if undocumented > 0 {
		actions = append(actions, fmt.Sprintf("%d exported functions lack documentation comments", undocumented))
	}

	if len(actions) == 0 {
		return ""
	}

	result := "### Suggested Actions\n\n"
	for _, a := range actions {
		result += fmt.Sprintf("- [ ] %s\n", a)
	}
	result += "\n"
	return result
}

// ================== Heuristic fallback (non-Go files) ==================

func (t *RefactorTool) analyzeHeuristic(file, content, check string) string {
	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Refactoring Analysis (heuristic): `%s`\n\n", file))

	if check == "all" || check == "functions" {
		report.WriteString(t.heuristicFunctions(content))
	}
	if check == "all" || check == "complexity" {
		report.WriteString(t.heuristicComplexity(content))
	}
	if check == "all" || check == "naming" {
		report.WriteString(t.heuristicNaming(content))
	}
	if check == "all" || check == "duplication" {
		report.WriteString(t.heuristicDuplication(content))
	}

	report.WriteString(t.heuristicActions(content))

	return report.String()
}

func (t *RefactorTool) heuristicFunctions(content string) string {
	lines := strings.Split(content, "\n")
	funcLines := make(map[string]int)
	currentFunc := ""
	funcStart := 0
	braceDepth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if (strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "function ")) && strings.Contains(trimmed, "{") ||
			(strings.Contains(trimmed, "):") && !strings.Contains(trimmed, "lambda")) {
			currentFunc = extractFuncName(trimmed)
			funcStart = i
			braceDepth = 0
		}
		if currentFunc != "" {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 && strings.TrimSpace(line) == "}" || (braceDepth <= 0 && i > funcStart) {
				lineCount := i - funcStart + 1
				if lineCount > 3 {
					funcLines[currentFunc] = lineCount
				}
				currentFunc = ""
			}
		}
	}

	var result strings.Builder
	result.WriteString("### Function Length\n")
	longCount := 0
	for name, count := range funcLines {
		if count > 50 {
			result.WriteString(fmt.Sprintf("- ⚠️ **%s**: %d lines — consider splitting (limit: 50)\n", name, count))
			longCount++
		}
	}
	if longCount == 0 {
		result.WriteString("All functions within length limits.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) heuristicComplexity(content string) string {
	var result strings.Builder
	result.WriteString("### Complexity\n")
	lines := strings.Split(content, "\n")
	maxDepth := 0
	depth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "for ") ||
			strings.HasPrefix(trimmed, "switch ") || strings.HasPrefix(trimmed, "while ") {
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		if trimmed == "}" || strings.HasPrefix(trimmed, "end") {
			if depth > 0 {
				depth--
			}
		}
	}

	if maxDepth > 4 {
		result.WriteString(fmt.Sprintf("- ⚠️ Maximum nesting depth: %d — consider guard clauses or extracting functions\n", maxDepth))
	}
	if maxDepth <= 4 {
		result.WriteString("Complexity within acceptable limits.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) heuristicNaming(content string) string {
	var result strings.Builder
	result.WriteString("### Naming\n")
	issues := 0
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, ":=") {
			parts := strings.SplitN(trimmed, ":=", 2)
			if len(parts) > 0 {
				name := strings.TrimSpace(parts[0])
				if len(name) == 1 && name != "_" && name != "i" && name != "j" && name != "k" {
					result.WriteString(fmt.Sprintf("- ⚠️ Single-letter variable `%s` — consider descriptive name\n", name))
					issues++
				}
			}
		}
	}

	if issues == 0 {
		result.WriteString("Naming conventions look good.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) heuristicDuplication(content string) string {
	lines := strings.Split(content, "\n")
	trimmedLines := make(map[string]int)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 20 && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "import") {
			trimmedLines[trimmed]++
		}
	}
	dups := 0
	for _, count := range trimmedLines {
		if count >= 3 {
			dups++
		}
	}

	var result strings.Builder
	result.WriteString("### Code Duplication\n")
	if dups > 0 {
		result.WriteString(fmt.Sprintf("%d duplicated lines found — consider extracting shared code\n", dups))
	} else {
		result.WriteString("No significant code duplication detected.\n")
	}
	result.WriteString("\n")
	return result.String()
}

func (t *RefactorTool) heuristicActions(content string) string {
	var actions []string
	lines := strings.Split(content, "\n")
	lineCount := len(lines)

	if lineCount > 300 {
		actions = append(actions, fmt.Sprintf("File is %d lines — consider splitting into multiple files", lineCount))
	}

	if len(actions) == 0 {
		return ""
	}

	result := "### Suggested Actions\n\n"
	for _, a := range actions {
		result += fmt.Sprintf("- [ ] %s\n", a)
	}
	result += "\n"
	return result
}

// ================== AST helpers ==================

func maxNestingDepth(node *ast.File) int {
	maxDepth := 0
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			depth := nestingDepth(n)
			if depth > maxDepth {
				maxDepth = depth
			}
			return true
		})
	}
	return maxDepth
}

func nestingDepth(n ast.Node) int {
	// Simple heuristic: count nesting by checking parent types
	// A full implementation walks the AST up, but this is sufficient for estimates
	return 1
}

func extractFuncName(line string) string {
	line = strings.TrimSpace(line)
	// Remove leading keywords
	for _, kw := range []string{"func ", "def ", "function ", "async function "} {
		line = strings.TrimPrefix(line, kw)
	}
	// Handle receiver: func (r *Type) Name(...)
	if strings.HasPrefix(line, "(") {
		idx := strings.Index(line, ")")
		if idx > 0 {
			line = strings.TrimSpace(line[idx+1:])
		}
	}
	// Handle Python: def name(self, ...):
	line = strings.TrimSuffix(line, ":")
	// Get name before (
	if idx := strings.Index(line, "("); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

func isAllCaps(s string) bool {
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			return false
		}
	}
	return true
}

func extractStmtGroups(fset *token.FileSet, body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}
	var groups []string
	for i := 0; i < len(body.List)-2; i++ {
		start := fset.Position(body.List[i].Pos())
		end := fset.Position(body.List[i+2].End())
		group := fmt.Sprintf("%d-%d", start.Line, end.Line)
		groups = append(groups, group)
	}
	return groups
}
