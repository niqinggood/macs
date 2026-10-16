package content

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

// DocGenTool generates code documentation: missing doc detection,
// doc comment generation, and API reference documents.
type DocGenTool struct {
	workspace string
}

func NewDocGenTool(workspace string) *DocGenTool {
	return &DocGenTool{workspace: workspace}
}

func (t *DocGenTool) Name() string { return "doc_gen" }

func (t *DocGenTool) Description() string {
	return "Generate code documentation. " +
		"Actions: missing (find undocumented exported symbols), " +
		"generate (generate doc comments for undocumented functions), " +
		"reference (generate API reference doc for a package). " +
		"Go: AST-based. Python/JS: heuristic."
}

func (t *DocGenTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"missing", "generate", "reference"},
				"description": "missing: list undocumented symbols. generate: create doc comments. reference: API reference doc.",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Target file to analyze",
			},
			"pkg": map[string]interface{}{
				"type":        "string",
				"description": "Package directory for reference generation (recursive)",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output file for generated docs",
			},
		},
		"required": []string{"action"},
	}
}

func (t *DocGenTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action := configvalue.String(params, "action", "missing")
	file := configvalue.String(params, "file", "")
	pkg := configvalue.String(params, "pkg", ".")
	output := configvalue.String(params, "output", "")
	if file != "" && !filepath.IsAbs(file) {
		file = filepath.Join(t.workspace, file)
	}
	if output != "" && !filepath.IsAbs(output) {
		output = filepath.Join(t.workspace, output)
	}

	switch action {
	case "missing":
		return t.findMissingDocs(ctx, file, pkg)
	case "generate":
		return t.generateDocs(ctx, file, output)
	case "reference":
		return t.generateReference(ctx, pkg, output)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Missing Docs ==================

func (t *DocGenTool) findMissingDocs(ctx context.Context, file, pkg string) (string, error) {
	if file != "" {
		return t.missingInFile(file)
	}
	return t.missingInPackage(ctx, pkg)
}

func (t *DocGenTool) missingInFile(filePath string) (string, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Sprintf("Parse error: %v (use heuristic for non-Go)", err), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Documentation Gaps: `%s`\n\n", filepath.Base(filePath)))

	undocumented := 0

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !ast.IsExported(d.Name.Name) {
				continue
			}
			if d.Doc == nil || strings.TrimSpace(d.Doc.Text()) == "" {
				undocumented++
				line := fset.Position(d.Pos()).Line
				sig := codeutil.BuildSignature(d)
				sb.WriteString(fmt.Sprintf("### `%s` (line %d)\n", d.Name.Name, line))
				sb.WriteString(fmt.Sprintf("```go\n%s\n```\n\n", sig))
				suggestion := t.suggestDoc(d)
				sb.WriteString(fmt.Sprintf("**Suggested doc:**\n```go\n// %s\n```\n\n", suggestion))
			}

		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ast.IsExported(ts.Name.Name) {
					continue
				}
				if d.Doc == nil || strings.TrimSpace(d.Doc.Text()) == "" {
					undocumented++
					line := fset.Position(ts.Pos()).Line
					kind := "type"
					switch ts.Type.(type) {
					case *ast.StructType:
						kind = "struct"
					case *ast.InterfaceType:
						kind = "interface"
					}
					sb.WriteString(fmt.Sprintf("### `%s` %s (line %d)\n", ts.Name.Name, kind, line))
					suggestion := t.suggestTypeDoc(ts)
					sb.WriteString(fmt.Sprintf("**Suggested doc:**\n```go\n// %s\n```\n\n", suggestion))
				}
			}
		}
	}

	if undocumented == 0 {
		sb.WriteString("✅ All exported symbols are documented.\n")
	} else {
		sb.WriteString(fmt.Sprintf("**%d undocumented exported symbols found.**\n\n", undocumented))
		sb.WriteString("Use `doc_gen generate` to add suggested comments.\n")
	}

	return sb.String(), nil
}

func (t *DocGenTool) missingInPackage(ctx context.Context, pkg string) (string, error) {
	// Walk package directory for Go files
	var goFiles []string
	packagePath := pkg
	if !filepath.IsAbs(packagePath) {
		packagePath = filepath.Join(t.workspace, packagePath)
	}
	walkErr := filepath.Walk(packagePath, func(path string, info os.FileInfo, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("scan package documentation: %w", walkErr)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Documentation Audit: %s\n\n", pkg))
	sb.WriteString(fmt.Sprintf("**%d Go files** in package\n\n", len(goFiles)))

	totalUndocumented := 0
	for _, f := range goFiles {
		result, err := t.missingInFile(f)
		if err != nil {
			continue
		}
		// Extract just the count
		if strings.Contains(result, "undocumented") {
			lines := strings.Split(result, "\n")
			for _, l := range lines {
				if strings.Contains(l, "All exported symbols are documented") {
					sb.WriteString(fmt.Sprintf("- `%s`: ✅ all documented\n", filepath.Base(f)))
				}
				if strings.Contains(l, "undocumented") && strings.Contains(l, "**") {
					sb.WriteString(fmt.Sprintf("- `%s`: %s\n", filepath.Base(f), strings.TrimSpace(l)))
				}
			}
		}
		_ = totalUndocumented
	}
	return sb.String(), nil
}

// ================== Generate Docs ==================

func (t *DocGenTool) generateDocs(ctx context.Context, filePath, output string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	src, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Sprintf("Parse error: %v", err), nil
	}

	var insertions []struct {
		line    int
		comment string
	}

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !ast.IsExported(d.Name.Name) {
				continue
			}
			if d.Doc == nil || strings.TrimSpace(d.Doc.Text()) == "" {
				line := fset.Position(d.Pos()).Line
				comment := t.suggestDoc(d)
				insertions = append(insertions, struct {
					line    int
					comment string
				}{line, comment})
			}

		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ast.IsExported(ts.Name.Name) {
					continue
				}
				if d.Doc == nil || strings.TrimSpace(d.Doc.Text()) == "" {
					line := fset.Position(d.Pos()).Line
					comment := t.suggestTypeDoc(ts)
					insertions = append(insertions, struct {
						line    int
						comment string
					}{line, comment})
				}
			}
		}
	}

	if len(insertions) == 0 {
		return "All exported symbols already documented.", nil
	}

	// Sort by line number descending so we can insert from bottom up
	sort.Slice(insertions, func(i, j int) bool {
		return insertions[i].line > insertions[j].line
	})

	// Insert comments
	lines := strings.Split(string(src), "\n")
	for _, ins := range insertions {
		idx := ins.line - 1 // 0-indexed
		if idx < 0 || idx > len(lines) {
			continue
		}
		// Insert before the function/type declaration
		commentLines := strings.Split(ins.comment, "\n")
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:idx]...)
		newLines = append(newLines, "")
		for _, c := range commentLines {
			newLines = append(newLines, "// "+c)
		}
		newLines = append(newLines, lines[idx:]...)
		lines = newLines
	}

	result := strings.Join(lines, "\n")

	if output == "" {
		output = filePath
	}
	if err := os.WriteFile(output, []byte(result), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("## Documentation Generated\n\n**File:** %s\n**Added:** %d doc comments\n\n**Tip:** Review the generated comments for accuracy.", output, len(insertions)), nil
}

// ================== Reference ==================

func (t *DocGenTool) generateReference(ctx context.Context, pkg, output string) (string, error) {
	var goFiles []string
	pkgPath := pkg
	if !filepath.IsAbs(pkgPath) {
		pkgPath = filepath.Join(t.workspace, pkgPath)
	}
	walkErr := filepath.Walk(pkgPath, func(path string, info os.FileInfo, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("scan API reference package: %w", walkErr)
	}

	if len(goFiles) == 0 {
		return "No Go files found in package.", nil
	}

	var sb strings.Builder
	packageName := pkg
	if packageName == "." {
		packageName = filepath.Base(t.workspace)
	}
	sb.WriteString(fmt.Sprintf("# %s — API Reference\n\n", packageName))
	sb.WriteString("> Auto-generated by OpenKitty doc_gen\n\n")

	type apiFunc struct {
		Name      string
		Signature string
		Doc       string
		Receiver  string
	}
	type apiType struct {
		Name   string
		Kind   string
		Doc    string
		Fields []string
	}

	var functions []apiFunc
	var types []apiType

	for _, filePath := range goFiles {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		src, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
		if err != nil {
			continue
		}

		for _, decl := range node.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !ast.IsExported(d.Name.Name) {
					continue
				}
				doc := ""
				if d.Doc != nil {
					doc = strings.TrimSpace(d.Doc.Text())
				}
				recv := ""
				if d.Recv != nil && len(d.Recv.List) > 0 {
					recv = codeutil.ReceiverName(d.Recv.List[0])
				}
				functions = append(functions, apiFunc{
					Name:      d.Name.Name,
					Signature: codeutil.BuildSignature(d),
					Doc:       doc,
					Receiver:  recv,
				})

			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ast.IsExported(ts.Name.Name) {
						continue
					}
					doc := ""
					if d.Doc != nil {
						doc = strings.TrimSpace(d.Doc.Text())
					}
					kind := "type"
					var fields []string
					switch t := ts.Type.(type) {
					case *ast.StructType:
						kind = "struct"
						if t.Fields != nil {
							for _, f := range t.Fields.List {
								ft := codeutil.TypeString(f.Type)
								if len(f.Names) > 0 {
									for _, n := range f.Names {
										fields = append(fields, fmt.Sprintf("%s %s", n.Name, ft))
									}
								} else {
									fields = append(fields, ft)
								}
							}
						}
					case *ast.InterfaceType:
						kind = "interface"
						if t.Methods != nil {
							for _, m := range t.Methods.List {
								if len(m.Names) > 0 {
									if functionType, ok := m.Type.(*ast.FuncType); ok {
										fields = append(fields, buildFuncType(m.Names[0].Name, functionType))
									}
								}
							}
						}
					}
					types = append(types, apiType{
						Name:   ts.Name.Name,
						Kind:   kind,
						Doc:    doc,
						Fields: fields,
					})
				}
			}
		}
	}

	// Sort
	sort.Slice(functions, func(i, j int) bool {
		return functions[i].Name < functions[j].Name
	})
	sort.Slice(types, func(i, j int) bool {
		return types[i].Name < types[j].Name
	})

	// Types section
	if len(types) > 0 {
		sb.WriteString("## Types\n\n")
		for _, typ := range types {
			sb.WriteString(fmt.Sprintf("### `%s` %s\n\n", typ.Name, typ.Kind))
			if typ.Doc != "" {
				sb.WriteString(typ.Doc + "\n\n")
			}
			if len(typ.Fields) > 0 {
				sb.WriteString("| Field/Method | Type |\n")
				sb.WriteString("|-------------|------|\n")
				for _, f := range typ.Fields {
					parts := strings.SplitN(f, " ", 2)
					name := parts[0]
					ftype := ""
					if len(parts) > 1 {
						ftype = parts[1]
					}
					sb.WriteString(fmt.Sprintf("| %s | `%s` |\n", name, ftype))
				}
				sb.WriteString("\n")
			}
		}
	}

	// Functions section
	if len(functions) > 0 {
		sb.WriteString("## Functions\n\n")
		for _, fn := range functions {
			methodOf := ""
			if fn.Receiver != "" {
				methodOf = fmt.Sprintf(" (method of `%s`)", fn.Receiver)
			}
			sb.WriteString(fmt.Sprintf("### `%s`%s\n\n", fn.Name, methodOf))
			sb.WriteString(fmt.Sprintf("```go\n%s\n```\n\n", fn.Signature))
			if fn.Doc != "" {
				sb.WriteString(fn.Doc + "\n\n")
			}
		}
	}

	sb.WriteString(fmt.Sprintf("---\n*Generated from %d file(s) in `%s`*\n", len(goFiles), pkg))

	if output != "" {
		if err := os.WriteFile(output, []byte(sb.String()), 0o644); err != nil {
			return "", fmt.Errorf("write API reference: %w", err)
		}
		return fmt.Sprintf("Reference saved to: %s", output), nil
	}

	return sb.String(), nil
}

// ================== Doc Suggestions ==================

func (t *DocGenTool) suggestDoc(fn *ast.FuncDecl) string {
	name := fn.Name.Name

	// Analyze function name to generate meaningful doc
	doc := inferDocFromName(name)

	// Add parameter descriptions
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
		doc += "\n"
		for _, p := range fn.Type.Params.List {
			pt := codeutil.TypeString(p.Type)
			if len(p.Names) > 0 {
				for _, n := range p.Names {
					doc += fmt.Sprintf("\n//   - %s: %s", n.Name, inferParamDoc(n.Name, pt))
				}
			}
		}
	}

	// Add return descriptions
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		doc += "\n//"
		for _, r := range fn.Type.Results.List {
			rt := codeutil.TypeString(r.Type)
			if len(r.Names) > 0 {
				doc += fmt.Sprintf("\n// Returns %s (%s): ...", r.Names[0].Name, rt)
			} else {
				doc += fmt.Sprintf("\n// Returns %s: ...", rt)
			}
		}
	}

	return doc
}

func (t *DocGenTool) suggestTypeDoc(ts *ast.TypeSpec) string {
	name := ts.Name.Name
	doc := fmt.Sprintf("%s represents ...", name)

	switch ts.Type.(type) {
	case *ast.StructType:
		doc = fmt.Sprintf("%s holds ...", name)
	case *ast.InterfaceType:
		doc = fmt.Sprintf("%s defines the interface for ...", name)
	}

	return doc
}

func inferDocFromName(name string) string {
	// Handle common naming patterns
	switch {
	case strings.HasPrefix(name, "New"):
		thing := strings.TrimPrefix(name, "New")
		thing = camelToWords(thing)
		return fmt.Sprintf("New%s creates a new %s.", name, strings.ToLower(thing))

	case strings.HasPrefix(name, "Get"):
		thing := strings.TrimPrefix(name, "Get")
		thing = camelToWords(thing)
		return fmt.Sprintf("Get%s returns the %s.", name, strings.ToLower(thing))

	case strings.HasPrefix(name, "Set"):
		thing := strings.TrimPrefix(name, "Set")
		return fmt.Sprintf("%s sets the %s.", name, camelToWords(thing))

	case strings.HasPrefix(name, "Is"), strings.HasPrefix(name, "Has"), strings.HasPrefix(name, "Can"):
		return fmt.Sprintf("%s reports whether ...", name)

	case strings.HasPrefix(name, "Parse"), strings.HasPrefix(name, "Decode"):
		return fmt.Sprintf("%s parses input and returns ...", name)

	case strings.HasPrefix(name, "Encode"), strings.HasPrefix(name, "Marshal"):
		return fmt.Sprintf("%s encodes data into ...", name)

	case strings.HasPrefix(name, "Write"), strings.HasPrefix(name, "Read"):
		return fmt.Sprintf("%s performs I/O on ...", name)

	case strings.HasPrefix(name, "Run"), strings.HasPrefix(name, "Start"):
		return fmt.Sprintf("%s starts execution of ...", name)

	case strings.HasPrefix(name, "Handle"), strings.HasPrefix(name, "Serve"):
		return fmt.Sprintf("%s handles requests for ...", name)

	case strings.HasPrefix(name, "Validate"), strings.HasPrefix(name, "Check"):
		return fmt.Sprintf("%s validates ... and returns an error if invalid.", name)

	default:
		return fmt.Sprintf("%s does something important.", name)
	}
}

func inferParamDoc(name, typ string) string {
	switch {
	case name == "ctx" && typ == "context.Context":
		return "the context for cancellation and deadlines"
	case name == "name":
		return "the name to use"
	case name == "id":
		return "the unique identifier"
	case name == "path", name == "filepath":
		return "the file path"
	case name == "key":
		return "the lookup key"
	case typ == "error":
		return "an error, if any"
	case typ == "string":
		return "a string value"
	case typ == "int", typ == "int64":
		return "an integer value"
	case typ == "bool":
		return "a boolean flag"
	case strings.HasPrefix(typ, "[]"):
		return "a slice of values"
	case strings.HasPrefix(typ, "map["):
		return "a map of key-value pairs"
	case strings.Contains(name, "timeout") || strings.Contains(name, "deadline"):
		return "the maximum duration to wait"
	case strings.Contains(name, "limit") || strings.Contains(name, "max"):
		return "the maximum allowed value"
	default:
		return fmt.Sprintf("the %s parameter", name)
	}
}

func camelToWords(s string) string {
	var words []string
	current := ""
	for _, r := range s {
		if r >= 'A' && r <= 'Z' && len(current) > 0 {
			words = append(words, current)
			current = string(r)
		} else {
			current += string(r)
		}
	}
	if current != "" {
		words = append(words, current)
	}
	return strings.Join(words, " ")
}

func buildFuncType(name string, ft *ast.FuncType) string {
	sig := name + "("
	if ft.Params != nil {
		params := make([]string, 0, len(ft.Params.List))
		for _, p := range ft.Params.List {
			pt := codeutil.TypeString(p.Type)
			if len(p.Names) > 0 {
				for _, n := range p.Names {
					params = append(params, n.Name+" "+pt)
				}
			} else {
				params = append(params, pt)
			}
		}
		sig += strings.Join(params, ", ")
	}
	sig += ")"
	if ft.Results != nil && len(ft.Results.List) > 0 {
		results := make([]string, 0, len(ft.Results.List))
		for _, r := range ft.Results.List {
			results = append(results, codeutil.TypeString(r.Type))
		}
		sig += " " + strings.Join(results, ", ")
	}
	return sig
}
