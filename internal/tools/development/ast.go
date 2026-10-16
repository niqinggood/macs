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

// AstTool provides AST-aware code analysis: real cyclomatic complexity,
// function/type/symbol extraction, call graphs, dead code detection,
// and semantic diff between code versions.
//
// Currently supports Go via go/parser + go/ast. Multi-language support
// via tree-sitter is planned.
type AstTool struct {
	workspace string
}

func NewAstTool(workspace string) *AstTool {
	return &AstTool{workspace: workspace}
}

func (t *AstTool) Name() string { return "ast_analyze" }

func (t *AstTool) Description() string {
	return "AST-aware code analysis for Go files. " +
		"Actions: complexity (real cyclomatic complexity, not regex), " +
		"functions (list all functions with signatures and call graphs), " +
		"symbols (all exported types/functions/constants), " +
		"dead_code (potentially unused exported symbols), " +
		"call_graph (full call graph for a file), " +
		"semantic_diff (compare two files and report what changed at the semantic level)."
}

func (t *AstTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"complexity", "functions", "symbols",
					"dead_code", "call_graph", "semantic_diff",
				},
				"description": "Analysis action to perform",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Target Go file path",
			},
			"file2": map[string]interface{}{
				"type":        "string",
				"description": "Second file for comparison (semantic_diff only)",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Raw Go source content (takes precedence over file path)",
			},
			"content2": map[string]interface{}{
				"type":        "string",
				"description": "Second raw content for comparison (semantic_diff only)",
			},
		},
		"required": []string{"action"},
	}
}

// ================== Execute ==================

func (t *AstTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action := configvalue.String(params, "action", "")
	file := configvalue.String(params, "file", "")
	file2 := configvalue.String(params, "file2", "")
	content := configvalue.String(params, "content", "")
	content2 := configvalue.String(params, "content2", "")

	var src []byte
	var err error

	if content != "" {
		src = []byte(content)
	} else if file != "" {
		fullPath := t.resolvePath(file)
		src, err = os.ReadFile(fullPath)
		if err != nil {
			return "", fmt.Errorf("read file %s: %w", file, err)
		}
	} else {
		return "", fmt.Errorf("either 'file' or 'content' is required")
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file, src, parser.ParseComments)
	if err != nil {
		return fmt.Sprintf("Parse error: %v", err), nil
	}

	switch action {
	case "complexity":
		return t.analyzeComplexity(fset, node, file), nil
	case "functions":
		return t.analyzeFunctions(fset, node, file), nil
	case "symbols":
		return t.analyzeSymbols(fset, node, file), nil
	case "dead_code":
		return t.analyzeDeadCode(fset, node, file), nil
	case "call_graph":
		return t.analyzeCallGraph(fset, node, file), nil
	case "semantic_diff":
		var src2 []byte
		if content2 != "" {
			src2 = []byte(content2)
		} else if file2 != "" {
			src2, err = os.ReadFile(t.resolvePath(file2))
			if err != nil {
				return "", fmt.Errorf("read file2 %s: %w", file2, err)
			}
		} else {
			return "", fmt.Errorf("semantic_diff requires 'file2' or 'content2'")
		}
		fset2 := token.NewFileSet()
		node2, err := parser.ParseFile(fset2, file2, src2, parser.ParseComments)
		if err != nil {
			return fmt.Sprintf("Parse error in file2: %v", err), nil
		}
		return t.analyzeSemanticDiff(fset, node, fset2, node2, file, file2), nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *AstTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.workspace, path)
}

// ================== Cyclomatic Complexity ==================

type funcComplexity struct {
	Name       string
	Receiver   string
	Line       int
	Complexity int
	Level      string // "low", "medium", "high", "very-high"
}

func (t *AstTool) analyzeComplexity(fset *token.FileSet, node *ast.File, file string) string {
	var funcs []funcComplexity
	totalComplexity := 0

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		c := cyclomaticComplexity(fn.Body)
		recv := ""
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recv = codeutil.ReceiverName(fn.Recv.List[0])
		}

		level := "low"
		switch {
		case c > 30:
			level = "very-high"
		case c > 15:
			level = "high"
		case c > 7:
			level = "medium"
		}

		funcs = append(funcs, funcComplexity{
			Name:       fn.Name.Name,
			Receiver:   recv,
			Line:       fset.Position(fn.Pos()).Line,
			Complexity: c,
			Level:      level,
		})
		totalComplexity += c
	}

	// Sort by complexity descending
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Complexity > funcs[j].Complexity
	})

	var sb strings.Builder
	pkgName := "main"
	if node.Name != nil {
		pkgName = node.Name.Name
	}
	sb.WriteString(fmt.Sprintf("## Cyclomatic Complexity: %s (package %s)\n\n", file, pkgName))
	sb.WriteString(fmt.Sprintf("**Total complexity: %d** across %d functions\n\n", totalComplexity, len(funcs)))

	sb.WriteString("| Complexity | Level | Function | Line |\n")
	sb.WriteString("|-----------|-------|----------|------|\n")
	for _, f := range funcs {
		fullName := f.Name
		if f.Receiver != "" {
			fullName = fmt.Sprintf("(%s).%s", f.Receiver, f.Name)
		}
		sb.WriteString(fmt.Sprintf("| %d | %s | `%s` | %d |\n",
			f.Complexity, levelIcon(f.Level), fullName, f.Line))
	}
	sb.WriteString("\n")

	// Count by level
	low, med, high, vhigh := 0, 0, 0, 0
	for _, f := range funcs {
		switch f.Level {
		case "very-high":
			vhigh++
		case "high":
			high++
		case "medium":
			med++
		default:
			low++
		}
	}
	sb.WriteString(fmt.Sprintf("**Summary:** %d low | %d medium | %d high | %d very-high\n\n", low, med, high, vhigh))
	sb.WriteString("**Guidelines:** ≤7 good, 8-15 needs review, 16-30 refactor recommended, >30 must refactor\n")

	return sb.String()
}

func cyclomaticComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	c := 1 // base complexity
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			c++
		case *ast.CaseClause:
			// Don't count default clause
			if cc, ok := n.(*ast.CaseClause); ok && cc.List != nil {
				c++
			}
		case *ast.CommClause:
			c++
		case *ast.BinaryExpr:
			if be, ok := n.(*ast.BinaryExpr); ok {
				switch be.Op {
				case token.LAND, token.LOR:
					// Boolean operators in conditionals add complexity
					if isInConditional(n) {
						c++
					}
				}
			}
		}
		return true
	})
	return c
}

func isInConditional(n ast.Node) bool {
	// Walk up to see if this is inside an if/for/switch condition
	// For simplicity, we just count all LAND/LOR which is common practice
	return true
}

func levelIcon(level string) string {
	switch level {
	case "very-high":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	default:
		return "🟢"
	}
}

// ================== Functions Analysis ==================

type funcDetail struct {
	Name      string
	Receiver  string
	Signature string
	Line      int
	Lines     int
	Exported  bool
	Doc       string
	CallsTo   []string
	CalledBy  []string
}

func (t *AstTool) analyzeFunctions(fset *token.FileSet, node *ast.File, file string) string {
	// First pass: collect all functions
	var funcs []funcDetail
	callMap := make(map[string][]string) // funcName -> []calledFuncNames

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

		// Build signature
		sig := codeutil.BuildSignature(fn)

		// Extract calls
		calls := extractCalls(fn.Body)
		fullName := fn.Name.Name
		if recv != "" {
			fullName = recv + "." + fn.Name.Name
		}
		callMap[fullName] = calls

		doc := ""
		if fn.Doc != nil {
			doc = strings.TrimSpace(fn.Doc.Text())
			// Truncate long docs
			if len(doc) > 200 {
				doc = doc[:200] + "..."
			}
		}

		funcs = append(funcs, funcDetail{
			Name:      fn.Name.Name,
			Receiver:  recv,
			Signature: sig,
			Line:      start,
			Lines:     end - start + 1,
			Exported:  ast.IsExported(fn.Name.Name),
			Doc:       doc,
			CallsTo:   calls,
		})
	}

	// Build reverse call map (who calls whom)
	callers := make(map[string][]string)
	for caller, callees := range callMap {
		for _, callee := range callees {
			callers[callee] = append(callers[callee], caller)
		}
	}

	// Sort by line number
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Line < funcs[j].Line
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Functions: %s\n\n", file))
	sb.WriteString(fmt.Sprintf("**%d functions**\n\n", len(funcs)))

	for _, fd := range funcs {
		vis := ""
		if fd.Exported {
			vis = " (exported)"
		}
		sb.WriteString(fmt.Sprintf("### %s%s\n", fd.Name, vis))
		sb.WriteString(fmt.Sprintf("```go\n%s\n```\n", fd.Signature))
		sb.WriteString(fmt.Sprintf("Line %d, %d lines\n", fd.Line, fd.Lines))
		if fd.Doc != "" {
			sb.WriteString(fmt.Sprintf("\n%s\n", fd.Doc))
		}
		if len(fd.CallsTo) > 0 {
			sb.WriteString(fmt.Sprintf("\nCalls: %s\n", strings.Join(uniqueStrs(fd.CallsTo), ", ")))
		}

		fullName := fd.Name
		if fd.Receiver != "" {
			fullName = fd.Receiver + "." + fd.Name
		}
		if callers, ok := callers[fullName]; ok && len(callers) > 0 {
			sb.WriteString(fmt.Sprintf("\nCalled by: %s\n", strings.Join(uniqueStrs(callers), ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func extractCalls(body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}
	var calls []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, fun.Name)
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok {
				calls = append(calls, id.Name+"."+fun.Sel.Name)
			} else {
				calls = append(calls, "."+fun.Sel.Name)
			}
		}
		return true
	})
	return calls
}

// ================== Symbols Analysis ==================

type symbolInfo struct {
	Name     string
	Kind     string // "function", "type", "const", "var"
	Exported bool
	Line     int
	Detail   string // brief description
}

func (t *AstTool) analyzeSymbols(fset *token.FileSet, node *ast.File, file string) string {
	var symbols []symbolInfo

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			symbols = append(symbols, symbolInfo{
				Name:     d.Name.Name,
				Kind:     "function",
				Exported: ast.IsExported(d.Name.Name),
				Line:     fset.Position(d.Pos()).Line,
				Detail:   "func",
			})

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind := "type"
					switch s.Type.(type) {
					case *ast.StructType:
						kind = "struct"
					case *ast.InterfaceType:
						kind = "interface"
					}
					symbols = append(symbols, symbolInfo{
						Name:     s.Name.Name,
						Kind:     kind,
						Exported: ast.IsExported(s.Name.Name),
						Line:     fset.Position(s.Pos()).Line,
						Detail:   kind,
					})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						symbols = append(symbols, symbolInfo{
							Name:     name.Name,
							Kind:     kind,
							Exported: ast.IsExported(name.Name),
							Line:     fset.Position(name.Pos()).Line,
							Detail:   kind,
						})
					}
				}
			}
		}
	}

	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Kind != symbols[j].Kind {
			return symbols[i].Kind < symbols[j].Kind
		}
		return symbols[i].Line < symbols[j].Line
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Symbols: %s\n\n", file))
	sb.WriteString("| Exported | Kind | Name | Line |\n")
	sb.WriteString("|----------|------|------|------|\n")

	exportedCount := 0
	for _, s := range symbols {
		exp := ""
		if s.Exported {
			exp = "✓"
			exportedCount++
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | `%s` | %d |\n", exp, s.Kind, s.Name, s.Line))
	}
	sb.WriteString(fmt.Sprintf("\n**%d symbols** (%d exported, %d unexported)\n", len(symbols), exportedCount, len(symbols)-exportedCount))

	return sb.String()
}

// ================== Dead Code Detection ==================

func (t *AstTool) analyzeDeadCode(fset *token.FileSet, node *ast.File, file string) string {
	// Collect all exported symbols and all internal references
	var exportedFuncs []string
	var exportedTypes []string
	allRefs := make(map[string]bool)

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if ast.IsExported(d.Name.Name) {
				exportedFuncs = append(exportedFuncs, d.Name.Name)
			}
			// Collect calls from non-exported code
			if !ast.IsExported(d.Name.Name) && d.Body != nil {
				for _, call := range extractCalls(d.Body) {
					allRefs[call] = true
				}
			}

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					if ast.IsExported(ts.Name.Name) {
						exportedTypes = append(exportedTypes, ts.Name.Name)
					}
				}
			}
		}
	}

	// Check which exported symbols aren't referenced within the file
	// Note: This is intra-file only — inter-file dead code requires cross-package analysis
	var potentiallyDead []string
	for _, name := range exportedFuncs {
		if !allRefs[name] && name != "main" && name != "init" {
			potentiallyDead = append(potentiallyDead, fmt.Sprintf("function `%s` (no internal callers)", name))
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Dead Code Check: %s\n\n", file))
	sb.WriteString("⚠️ Intra-file analysis only — cross-package usage not checked.\n\n")

	if len(potentiallyDead) == 0 {
		sb.WriteString("**No obviously dead exported symbols detected.**\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("### Potentially Unused Exports (%d)\n\n", len(potentiallyDead)))
		for _, d := range potentiallyDead {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n**Note:** These may be used by other packages. Verify before removing.\n")
	}

	// Report unexported functions that are never called
	var uncalled []string
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if ast.IsExported(fn.Name.Name) {
			continue
		}
		if fn.Name.Name == "init" {
			continue
		}
		if !allRefs[fn.Name.Name] {
			uncalled = append(uncalled, fmt.Sprintf("`%s` (line %d)", fn.Name.Name, fset.Position(fn.Pos()).Line))
		}
	}
	if len(uncalled) > 0 {
		sb.WriteString(fmt.Sprintf("### Unexported Functions With No Callers (%d)\n\n", len(uncalled)))
		for _, u := range uncalled {
			sb.WriteString(fmt.Sprintf("- %s\n", u))
		}
		sb.WriteString("\nThese may be unused or only called from tests.\n")
	}

	sb.WriteString(fmt.Sprintf("\n**Exported:** %d functions, %d types\n", len(exportedFuncs), len(exportedTypes)))
	return sb.String()
}

// ================== Call Graph ==================

func (t *AstTool) analyzeCallGraph(fset *token.FileSet, node *ast.File, file string) string {
	type edge struct {
		caller string
		callee string
		line   int
	}

	var edges []edge
	funcSet := make(map[string]bool)

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		callerName := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			callerName = codeutil.ReceiverName(fn.Recv.List[0]) + "." + callerName
		}
		funcSet[callerName] = true

		if fn.Body == nil {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				edges = append(edges, edge{
					caller: callerName,
					callee: fun.Name,
					line:   fset.Position(call.Pos()).Line,
				})
			case *ast.SelectorExpr:
				calleeName := fun.Sel.Name
				if id, ok := fun.X.(*ast.Ident); ok {
					calleeName = id.Name + "." + calleeName
				}
				edges = append(edges, edge{
					caller: callerName,
					callee: calleeName,
					line:   fset.Position(call.Pos()).Line,
				})
			}
			return true
		})
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Call Graph: %s\n\n", file))

	// Group by caller
	callerMap := make(map[string][]edge)
	for _, e := range edges {
		callerMap[e.caller] = append(callerMap[e.caller], e)
	}

	// Sort callers
	callers := make([]string, 0, len(callerMap))
	for c := range callerMap {
		callers = append(callers, c)
	}
	sort.Strings(callers)

	for _, caller := range callers {
		e := callerMap[caller]
		callees := make([]string, len(e))
		for i, ed := range e {
			callees[i] = ed.callee
		}
		sb.WriteString(fmt.Sprintf("**%s** → %s\n", caller, strings.Join(callees, ", ")))
	}

	if len(edges) == 0 {
		sb.WriteString("No function calls found in this file.\n")
	}

	return sb.String()
}

// ================== Semantic Diff ==================

type semanticChange struct {
	Kind    string // "added", "removed", "modified", "signature_changed"
	Symbol  string // function or type name
	Details string
	OldLine int
	NewLine int
}

func (t *AstTool) analyzeSemanticDiff(fset1 *token.FileSet, node1 *ast.File, fset2 *token.FileSet, node2 *ast.File, file1, file2 string) string {
	funcs1 := collectFuncSignatures(fset1, node1)
	funcs2 := collectFuncSignatures(fset2, node2)

	types1 := collectTypeNames(node1)
	types2 := collectTypeNames(node2)

	imports1 := collectImports(node1)
	imports2 := collectImports(node2)

	var changes []semanticChange

	// Functions: added, removed, signature_changed
	for name, sig1 := range funcs1 {
		if sig2, ok := funcs2[name]; ok {
			if sig1 != sig2 {
				changes = append(changes, semanticChange{
					Kind:    "signature_changed",
					Symbol:  name,
					Details: fmt.Sprintf("was: %s\n  now: %s", sig1, sig2),
				})
			}
		} else {
			changes = append(changes, semanticChange{
				Kind:    "removed",
				Symbol:  name,
				Details: sig1,
			})
		}
	}
	for name, sig2 := range funcs2 {
		if _, ok := funcs1[name]; !ok {
			changes = append(changes, semanticChange{
				Kind:    "added",
				Symbol:  name,
				Details: sig2,
			})
		}
	}

	// Types: added, removed
	for t := range types1 {
		if _, ok := types2[t]; !ok {
			changes = append(changes, semanticChange{
				Kind:    "removed",
				Symbol:  t,
				Details: "type removed",
			})
		}
	}
	for t := range types2 {
		if _, ok := types1[t]; !ok {
			changes = append(changes, semanticChange{
				Kind:    "added",
				Symbol:  t,
				Details: "type added",
			})
		}
	}

	// Imports
	for _, imp := range imports1 {
		found := false
		for _, imp2 := range imports2 {
			if imp == imp2 {
				found = true
				break
			}
		}
		if !found {
			changes = append(changes, semanticChange{
				Kind:    "removed",
				Symbol:  "import " + imp,
				Details: "import removed",
			})
		}
	}
	for _, imp := range imports2 {
		found := false
		for _, imp1 := range imports1 {
			if imp == imp1 {
				found = true
				break
			}
		}
		if !found {
			changes = append(changes, semanticChange{
				Kind:    "added",
				Symbol:  "import " + imp,
				Details: "import added",
			})
		}
	}

	// Format output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Semantic Diff\n\n"))
	sb.WriteString(fmt.Sprintf("**Old:** %s (%d funcs, %d types, %d imports)\n",
		file1, len(funcs1), len(types1), len(imports1)))
	sb.WriteString(fmt.Sprintf("**New:** %s (%d funcs, %d types, %d imports)\n\n",
		file2, len(funcs2), len(types2), len(imports2)))

	if len(changes) == 0 {
		sb.WriteString("**No semantic changes detected.** Symbols unchanged.\n")
		return sb.String()
	}

	// Group by kind
	added := filterChanges(changes, "added")
	removed := filterChanges(changes, "removed")
	sigChanged := filterChanges(changes, "signature_changed")

	if len(added) > 0 {
		sb.WriteString(fmt.Sprintf("### Added (%d)\n\n", len(added)))
		for _, c := range added {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", c.Symbol, c.Details))
		}
		sb.WriteString("\n")
	}

	// BREAKING: removed symbols
	if len(removed) > 0 {
		sb.WriteString(fmt.Sprintf("### Removed ⚠️ BREAKING (%d)\n\n", len(removed)))
		for _, c := range removed {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", c.Symbol, c.Details))
		}
		sb.WriteString("\n")
	}

	if len(sigChanged) > 0 {
		sb.WriteString(fmt.Sprintf("### Signature Changed ⚠️ (%d)\n\n", len(sigChanged)))
		for _, c := range sigChanged {
			sb.WriteString(fmt.Sprintf("- **%s**\n  %s\n", c.Symbol, c.Details))
		}
		sb.WriteString("\n")
	}

	// Summary
	breaking := len(removed) + len(sigChanged)
	sb.WriteString(fmt.Sprintf("**Summary:** %d changes (%d breaking)\n", len(changes), breaking))

	return sb.String()
}

func collectFuncSignatures(fset *token.FileSet, node *ast.File) map[string]string {
	result := make(map[string]string)
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = codeutil.ReceiverName(fn.Recv.List[0]) + "." + name
		}
		result[name] = codeutil.BuildSignature(fn)
	}
	return result
}

func collectTypeNames(node *ast.File) map[string]bool {
	result := make(map[string]bool)
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				result[ts.Name.Name] = true
			}
		}
	}
	return result
}

func collectImports(node *ast.File) []string {
	var result []string
	for _, imp := range node.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		if imp.Name != nil {
			path = imp.Name.Name + " " + strings.Trim(imp.Path.Value, "\"")
		}
		result = append(result, path)
	}
	return result
}

func filterChanges(changes []semanticChange, kind string) []semanticChange {
	var result []semanticChange
	for _, c := range changes {
		if c.Kind == kind {
			result = append(result, c)
		}
	}
	return result
}

// ================== Helpers ==================

func uniqueStrs(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
