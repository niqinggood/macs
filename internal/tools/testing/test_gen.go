package testingtools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"mac/internal/codeutil"
	"mac/internal/configvalue"
)

// TestGenTool generates tests for existing code.
// AST-based generation for Go: table-driven tests with edge cases.
// Template-based generation for Python (pytest) and JS (Jest).
type TestGenTool struct {
	workspace string
}

func NewTestGenTool(workspace string) *TestGenTool {
	return &TestGenTool{workspace: workspace}
}

func (t *TestGenTool) Name() string { return "test_gen" }

func (t *TestGenTool) Description() string {
	return "Generate tests for existing code. " +
		"Actions: generate (create a test file with table-driven tests), " +
		"suggest (list what tests should be written without generating). " +
		"Go: table-driven tests with edge cases (nil, empty, boundary). " +
		"Python: pytest with fixtures. JS/TS: Jest/Vitest."
}

func (t *TestGenTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"generate", "suggest"},
				"description": "generate: create test file on disk. suggest: return test plan without writing.",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Source file to generate tests for",
			},
			"function": map[string]interface{}{
				"type":        "string",
				"description": "Optional: generate tests only for this specific function",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Optional: output file path. Default: <name>_test.<ext>",
			},
		},
		"required": []string{"action", "file"},
	}
}

func (t *TestGenTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action := configvalue.String(params, "action", "")
	file := configvalue.String(params, "file", "")
	targetFunc := configvalue.String(params, "function", "")
	outputPath := configvalue.String(params, "output", "")

	if file == "" {
		return "Error: file is required", nil
	}

	fullPath := file
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(t.workspace, fullPath)
	}
	if outputPath != "" && !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(t.workspace, outputPath)
	}

	src, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Sprintf("Cannot read file: %s: %v", file, err), nil
	}

	ext := strings.ToLower(filepath.Ext(fullPath))
	switch ext {
	case ".go":
		return t.generateGoTests(fullPath, string(src), targetFunc, outputPath, action)
	case ".py":
		return t.generatePythonTests(fullPath, string(src), targetFunc, outputPath, action)
	case ".ts", ".tsx", ".js", ".jsx":
		return t.generateJSTests(fullPath, string(src), targetFunc, outputPath, action)
	default:
		return fmt.Sprintf("Test generation not supported for %s files", ext), nil
	}
}

// ================== Go Test Generation ==================

type goFuncForTest struct {
	Name     string
	Receiver string
	Params   []paramInfo
	Results  []resultInfo
	Exported bool
	IsMethod bool
	Line     int
}

type paramInfo struct {
	Name string
	Type string
}

type resultInfo struct {
	Name string
	Type string
}

func (t *TestGenTool) generateGoTests(filePath, content, targetFunc, outputPath, action string) (string, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return fmt.Sprintf("Parse error: %v", err), nil
	}

	var funcs []goFuncForTest

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Skip unexported unless explicitly targeted
		if targetFunc == "" && !ast.IsExported(fn.Name.Name) {
			continue
		}
		if targetFunc != "" && fn.Name.Name != targetFunc {
			continue
		}
		// Skip init and main
		if fn.Name.Name == "init" || fn.Name.Name == "main" {
			continue
		}

		gf := goFuncForTest{
			Name:     fn.Name.Name,
			Exported: ast.IsExported(fn.Name.Name),
			Line:     fset.Position(fn.Pos()).Line,
		}

		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			gf.Receiver = codeutil.ReceiverName(fn.Recv.List[0])
			gf.IsMethod = true
		}

		// Extract parameters
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				pt := codeutil.TypeString(field.Type)
				for _, name := range field.Names {
					gf.Params = append(gf.Params, paramInfo{Name: name.Name, Type: pt})
				}
				if len(field.Names) == 0 {
					gf.Params = append(gf.Params, paramInfo{Type: pt})
				}
			}
		}

		// Extract return types
		if fn.Type.Results != nil {
			for _, field := range fn.Type.Results.List {
				rt := codeutil.TypeString(field.Type)
				if len(field.Names) > 0 {
					for _, name := range field.Names {
						gf.Results = append(gf.Results, resultInfo{Name: name.Name, Type: rt})
					}
				} else {
					gf.Results = append(gf.Results, resultInfo{Type: rt})
				}
			}
		}

		funcs = append(funcs, gf)
	}

	if len(funcs) == 0 {
		return "No exported functions found to generate tests for.", nil
	}

	// Suggest mode: return test plan
	if action == "suggest" {
		return t.suggestGoTests(funcs, filePath), nil
	}

	// Generate mode: create test file
	packageName := "main"
	if node.Name != nil {
		packageName = node.Name.Name
	}
	testContent := t.generateGoTestFile(packageName, funcs, filePath)

	if outputPath == "" {
		base := filepath.Base(filePath)
		ext := filepath.Ext(base)
		outputPath = strings.TrimSuffix(filePath, ext) + "_test" + ext
	}

	if err := os.WriteFile(outputPath, []byte(testContent), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}

	return fmt.Sprintf("## Test File Generated\n\n**Source:** %s\n**Output:** %s\n**Functions tested:** %d\n\n```go\n%s\n```",
		filePath, outputPath, len(funcs), testContent), nil
}

func (t *TestGenTool) suggestGoTests(funcs []goFuncForTest, filePath string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Test Suggestions: %s\n\n", filepath.Base(filePath)))

	for _, fn := range funcs {
		fullName := fn.Name
		if fn.Receiver != "" {
			fullName = fmt.Sprintf("(%s).%s", fn.Receiver, fn.Name)
		}
		sb.WriteString(fmt.Sprintf("### `%s` (line %d)\n\n", fullName, fn.Line))

		cases := generateEdgeCases(fn.Params, fn.Results)
		sb.WriteString(fmt.Sprintf("**%d test cases suggested:**\n", len(cases)))
		for _, c := range cases {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (t *TestGenTool) generateGoTestFile(packageName string, funcs []goFuncForTest, filePath string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("package %s\n\n", packageName))

	// Check if any function uses special imports
	needsContext := false
	needsReflect := false
	for _, fn := range funcs {
		for _, p := range fn.Params {
			if p.Type == "context.Context" {
				needsContext = true
			}
		}
		for _, r := range fn.Results {
			if r.Type == "interface{}" || r.Type == "any" {
				needsReflect = true
			}
		}
	}

	if needsContext || needsReflect {
		sb.WriteString("import (\n")
		sb.WriteString("\t\"testing\"\n")
		if needsContext {
			sb.WriteString("\t\"context\"\n")
		}
		if needsReflect {
			sb.WriteString("\t\"reflect\"\n")
		}
		sb.WriteString(")\n\n")
	} else {
		sb.WriteString("import \"testing\"\n\n")
	}

	for _, fn := range funcs {
		testName := "Test" + fn.Name
		if fn.Receiver != "" {
			recvClean := strings.TrimPrefix(fn.Receiver, "*")
			testName = "Test" + titleCase(recvClean) + "_" + fn.Name
		}

		sb.WriteString(fmt.Sprintf("func %s(t *testing.T) {\n", testName))
		sb.WriteString(t.generateTableTest(fn))
		sb.WriteString("}\n\n")
	}

	return sb.String()
}

func (t *TestGenTool) generateTableTest(fn goFuncForTest) string {
	var sb strings.Builder

	// Define test case struct
	sb.WriteString("\ttype testCase struct {\n")
	sb.WriteString("\t\tdescription string\n")
	for _, p := range fn.Params {
		// Avoid collision with 'description' field
		fieldName := p.Name
		if fieldName == "description" {
			fieldName = "arg_" + fieldName
		}
		sb.WriteString(fmt.Sprintf("\t\t%s %s\n", fieldName, p.Type))
	}
	for _, r := range fn.Results {
		if r.Name != "" {
			sb.WriteString(fmt.Sprintf("\t\twant%s %s\n", titleCase(r.Name), r.Type))
		} else {
			sb.WriteString(fmt.Sprintf("\t\twant %s\n", r.Type))
		}
	}
	sb.WriteString("\t}\n\n")

	// Generate test cases
	cases := generateTestCases(fn)
	sb.WriteString(fmt.Sprintf("\ttests := []testCase{\n"))
	for _, c := range cases {
		sb.WriteString(fmt.Sprintf("\t\t%s,\n", c))
	}
	sb.WriteString("\t}\n\n")

	// Run test loop
	sb.WriteString("\tfor _, tt := range tests {\n")
	sb.WriteString("\t\tt.Run(tt.description, func(t *testing.T) {\n")

	// Build call
	var callArgs []string
	for _, p := range fn.Params {
		callArgs = append(callArgs, "tt."+p.Name)
	}

	var resultVars []string
	for i, r := range fn.Results {
		vName := "got"
		if r.Name != "" {
			vName = "got" + titleCase(r.Name)
		}
		resultVars = append(resultVars, vName)
		_ = i
	}

	fnCall := fn.Name
	if fn.IsMethod {
		fnCall = fmt.Sprintf("tt.receiver.%s", fn.Name)
		sb.WriteString("\t\t\t// Note: receiver setup required\n")
	}

	if len(resultVars) > 0 {
		sb.WriteString(fmt.Sprintf("\t\t\t%s := %s(%s)\n",
			strings.Join(resultVars, ", "), fnCall, strings.Join(callArgs, ", ")))
	} else {
		sb.WriteString(fmt.Sprintf("\t\t\t%s(%s)\n", fnCall, strings.Join(callArgs, ", ")))
	}

	// Assertions
	for i, r := range fn.Results {
		vName := resultVars[i]
		wantName := "want"
		if r.Name != "" {
			wantName = "want" + titleCase(r.Name)
		}

		if r.Type == "error" {
			sb.WriteString(fmt.Sprintf("\t\t\tif %s != nil != (tt.%s != nil) {\n", vName, wantName))
			sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"error = %%v, wantErr %%v\", %s, tt.%s)\n", vName, wantName))
			sb.WriteString("\t\t\t}\n")
		} else if isComparableType(r.Type) {
			sb.WriteString(fmt.Sprintf("\t\t\tif %s != tt.%s {\n", vName, wantName))
			sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s = %%v, want %%v\", %s, tt.%s)\n",
				vName, vName, wantName))
			sb.WriteString("\t\t\t}\n")
		} else {
			sb.WriteString(fmt.Sprintf("\t\t\tif !reflect.DeepEqual(%s, tt.%s) {\n", vName, wantName))
			sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s = %%v, want %%v\", %s, tt.%s)\n",
				vName, vName, wantName))
			sb.WriteString("\t\t\t}\n")
		}
	}

	sb.WriteString("\t\t})\n")
	sb.WriteString("\t}\n")

	return sb.String()
}

func generateTestCases(fn goFuncForTest) []string {
	var cases []string

	// Helper to format string value with proper quoting
	strVal := func(s string) string {
		return fmt.Sprintf("%q", s)
	}
	_ = strVal

	// Happy path
	happyPath := fmt.Sprintf(`{description: "success"`)
	for _, p := range fn.Params {
		val := defaultValueForType(p.Type, "happy")
		fieldName := p.Name
		if fieldName == "description" {
			fieldName = "arg_description"
		}
		happyPath += fmt.Sprintf(", %s: %s", fieldName, val)
	}
	for _, r := range fn.Results {
		wantName := "want"
		if r.Name != "" {
			wantName = "want" + titleCase(r.Name)
		}
		val := defaultValueForType(r.Type, "happy")
		happyPath += fmt.Sprintf(", %s: %s", wantName, val)
	}
	happyPath += "}"
	cases = append(cases, happyPath)

	// Edge cases
	hasString := false
	hasSlice := false
	hasPointer := false
	hasInt := false
	hasError := false

	for _, p := range fn.Params {
		switch {
		case p.Type == "string":
			hasString = true
		case strings.HasPrefix(p.Type, "[]"):
			hasSlice = true
		case strings.HasPrefix(p.Type, "*"):
			hasPointer = true
		case p.Type == "int" || p.Type == "int64" || p.Type == "float64":
			hasInt = true
		}
	}
	for _, r := range fn.Results {
		if r.Type == "error" {
			hasError = true
		}
	}

	_ = hasError

	if hasString {
		empty := fmt.Sprintf(`{description: "empty string"`)
		for _, p := range fn.Params {
			val := defaultValueForType(p.Type, "zero")
			if p.Type == "string" {
				val = `""`
			}
			empty += fmt.Sprintf(", %s: %s", p.Name, val)
		}
		for _, r := range fn.Results {
			wantName := "want"
			if r.Name != "" {
				wantName = "want" + titleCase(r.Name)
			}
			val := defaultValueForType(r.Type, "zero")
			empty += fmt.Sprintf(", %s: %s", wantName, val)
		}
		empty += "}"
		cases = append(cases, empty)
	}

	if hasSlice {
		null := fmt.Sprintf(`{description: "nil slice"`)
		for _, p := range fn.Params {
			val := defaultValueForType(p.Type, "nil")
			null += fmt.Sprintf(", %s: %s", p.Name, val)
		}
		for _, r := range fn.Results {
			wantName := "want"
			if r.Name != "" {
				wantName = "want" + titleCase(r.Name)
			}
			val := defaultValueForType(r.Type, "nil")
			null += fmt.Sprintf(", %s: %s", wantName, val)
		}
		null += "}"
		cases = append(cases, null)
	}

	if hasInt {
		zero := fmt.Sprintf(`{description: "zero value"`)
		for _, p := range fn.Params {
			val := defaultValueForType(p.Type, "zero")
			zero += fmt.Sprintf(", %s: %s", p.Name, val)
		}
		for _, r := range fn.Results {
			wantName := "want"
			if r.Name != "" {
				wantName = "want" + titleCase(r.Name)
			}
			val := defaultValueForType(r.Type, "zero")
			zero += fmt.Sprintf(", %s: %s", wantName, val)
		}
		zero += "}"
		cases = append(cases, zero)

		neg := fmt.Sprintf(`{description: "negative value"`)
		for _, p := range fn.Params {
			val := defaultValueForType(p.Type, "negative")
			neg += fmt.Sprintf(", %s: %s", p.Name, val)
		}
		for _, r := range fn.Results {
			wantName := "want"
			if r.Name != "" {
				wantName = "want" + titleCase(r.Name)
			}
			val := defaultValueForType(r.Type, "negative")
			neg += fmt.Sprintf(", %s: %s", wantName, val)
		}
		neg += "}"
		cases = append(cases, neg)
	}

	if hasPointer {
		nilPtr := fmt.Sprintf(`{description: "nil pointer"`)
		for _, p := range fn.Params {
			val := defaultValueForType(p.Type, "nil")
			nilPtr += fmt.Sprintf(", %s: %s", p.Name, val)
		}
		for _, r := range fn.Results {
			wantName := "want"
			if r.Name != "" {
				wantName = "want" + titleCase(r.Name)
			}
			val := defaultValueForType(r.Type, "nil")
			nilPtr += fmt.Sprintf(", %s: %s", wantName, val)
		}
		nilPtr += "}"
		cases = append(cases, nilPtr)
	}

	return cases
}

func generateEdgeCases(params []paramInfo, results []resultInfo) []string {
	var cases []string
	fnDesc := describeFunc(params, results)
	cases = append(cases, fmt.Sprintf("✅ Happy path: %s", fnDesc))
	cases = append(cases, "Verify expected output matches")

	for _, p := range params {
		switch {
		case p.Type == "string":
			cases = append(cases, fmt.Sprintf("🔍 Empty string for `%s`", p.Name))
			cases = append(cases, fmt.Sprintf("🔍 Unicode/special chars for `%s`", p.Name))
		case strings.HasPrefix(p.Type, "[]"):
			cases = append(cases, fmt.Sprintf("🔍 Nil slice for `%s`", p.Name))
			cases = append(cases, fmt.Sprintf("🔍 Empty slice for `%s`", p.Name))
		case strings.HasPrefix(p.Type, "*"):
			cases = append(cases, fmt.Sprintf("🔍 Nil pointer for `%s`", p.Name))
		case p.Type == "int" || p.Type == "int64":
			cases = append(cases, fmt.Sprintf("🔍 Zero value for `%s`", p.Name))
			cases = append(cases, fmt.Sprintf("🔍 Negative value for `%s`", p.Name))
		case p.Type == "context.Context":
			cases = append(cases, "🔍 context.Background()")
			cases = append(cases, "🔍 context.WithTimeout()")
		case p.Type == "io.Reader":
			cases = append(cases, "🔍 Nil reader")
			cases = append(cases, "🔍 Empty reader (strings.NewReader(\"\"))")
		}
	}

	for _, r := range results {
		if r.Type == "error" {
			cases = append(cases, "🔍 Verify error is nil on success")
			cases = append(cases, "🔍 Verify error wrapping (errors.Is / errors.As)")
		}
	}

	return cases
}

func describeFunc(params []paramInfo, results []resultInfo) string {
	paramStrs := make([]string, len(params))
	for i, p := range params {
		paramStrs[i] = p.Type
	}
	resultStrs := make([]string, len(results))
	for i, r := range results {
		resultStrs[i] = r.Type
	}

	desc := fmt.Sprintf("(%s)", strings.Join(paramStrs, ", "))
	if len(results) > 0 {
		desc += fmt.Sprintf(" -> (%s)", strings.Join(resultStrs, ", "))
	}
	return desc
}

func defaultValueForType(typ, mode string) string {
	switch typ {
	case "string":
		switch mode {
		case "happy":
			return `"test_value"`
		case "zero", "nil":
			return `""`
		default:
			return `""`
		}
	case "int", "int64", "int32":
		switch mode {
		case "happy":
			return "42"
		case "zero":
			return "0"
		case "negative":
			return "-1"
		default:
			return "0"
		}
	case "float64", "float32":
		switch mode {
		case "happy":
			return "3.14"
		case "zero":
			return "0.0"
		case "negative":
			return "-1.0"
		default:
			return "0.0"
		}
	case "bool":
		return "true"
	case "error":
		switch mode {
		case "happy":
			return "nil"
		default:
			return "fmt.Errorf(\"test error\")"
		}
	case "context.Context":
		return "context.Background()"
	case "io.Reader":
		switch mode {
		case "happy":
			return `strings.NewReader("test")`
		case "nil":
			return "nil"
		default:
			return "nil"
		}
	default:
		if strings.HasPrefix(typ, "[]") {
			switch mode {
			case "happy":
				return typ + "{}"
			case "nil":
				return "nil"
			default:
				return "nil"
			}
		}
		if strings.HasPrefix(typ, "*") {
			switch mode {
			case "happy":
				return "&" + strings.TrimPrefix(typ, "*") + "{}"
			case "nil":
				return "nil"
			default:
				return "nil"
			}
		}
		if strings.HasPrefix(typ, "map[") {
			switch mode {
			case "happy":
				return typ + "{}"
			case "nil":
				return "nil"
			default:
				return "nil"
			}
		}
		// Default for unknown types
		switch mode {
		case "nil":
			return "nil"
		default:
			return typ + "{}"
		}
	}
}

func isComparableType(typ string) bool {
	switch typ {
	case "string", "int", "int64", "int32", "int16", "int8",
		"uint", "uint64", "uint32", "uint16", "uint8",
		"float64", "float32", "bool", "byte", "rune", "error":
		return true
	}
	return false
}

// ================== Python Test Generation ==================

func (t *TestGenTool) generatePythonTests(filePath, content, targetFunc, outputPath, action string) (string, error) {
	// Simple regex-based function extraction for Python
	var funcs []struct {
		name   string
		params string
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "def ") {
			// Extract function name and params
			rest := strings.TrimPrefix(trimmed, "def ")
			parenIdx := strings.Index(rest, "(")
			colonIdx := strings.LastIndex(rest, ":")
			if parenIdx >= 0 && colonIdx > parenIdx {
				name := rest[:parenIdx]
				params := rest[parenIdx+1 : colonIdx]
				if targetFunc == "" || name == targetFunc {
					funcs = append(funcs, struct {
						name   string
						params string
					}{name, params})
				}
			}
		}
	}

	if len(funcs) == 0 {
		return "No functions found to generate tests for.", nil
	}

	if action == "suggest" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Test Suggestions: %s\n\n", filepath.Base(filePath)))
		for _, fn := range funcs {
			sb.WriteString(fmt.Sprintf("### `%s(%s)`\n\n", fn.name, fn.params))
			sb.WriteString("- Happy path with valid inputs\n")
			sb.WriteString("- Edge cases: None, empty, invalid types\n")
			sb.WriteString("- Verify return value and side effects\n\n")
		}
		return sb.String(), nil
	}

	// Generate pytest file
	var sb strings.Builder
	sb.WriteString("# Generated by OpenKitty test_gen\n")
	sb.WriteString("import pytest\n\n")

	for _, fn := range funcs {
		sb.WriteString(fmt.Sprintf("def test_%s():\n", fn.name))
		sb.WriteString(fmt.Sprintf("    \"\"\"Test %s function.\"\"\"\n", fn.name))
		sb.WriteString("    # Arrange\n")
		sb.WriteString("    # Act\n")
		sb.WriteString(fmt.Sprintf("    # result = %s(test_input)\n", fn.name))
		sb.WriteString("    # Assert\n")
		sb.WriteString("    # assert result == expected\n")
		sb.WriteString("    pass  # TODO: implement test\n\n")
	}

	if outputPath == "" {
		base := filepath.Base(filePath)
		ext := filepath.Ext(base)
		outputPath = strings.TrimSuffix(filePath, ext) + "_test" + ext
	}

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}

	return fmt.Sprintf("## Test File Generated\n\n**Source:** %s\n**Output:** %s\n**Functions:** %d\n\n```python\n%s\n```",
		filePath, outputPath, len(funcs), sb.String()), nil
}

// ================== JS/TS Test Generation ==================

func (t *TestGenTool) generateJSTests(filePath, content, targetFunc, outputPath, action string) (string, error) {
	// Simple extraction for JS/TS
	var funcs []struct {
		name   string
		params string
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match: function name(...) or const name = (...) =>
		if strings.Contains(trimmed, "function ") || strings.Contains(trimmed, "=>") {
			// Simple extraction
			for _, kw := range []string{"function ", "const ", "let ", "var "} {
				if idx := strings.Index(trimmed, kw); idx >= 0 {
					rest := trimmed[idx+len(kw):]
					if parenIdx := strings.Index(rest, "("); parenIdx >= 0 {
						name := strings.TrimSpace(rest[:parenIdx])
						if name != "" && name != "=>" {
							funcs = append(funcs, struct {
								name   string
								params string
							}{name, ""})
							break
						}
					}
				}
			}
		}
	}

	if len(funcs) == 0 {
		return "No functions found to generate tests for.", nil
	}

	if action == "suggest" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Test Suggestions: %s\n\n", filepath.Base(filePath)))
		for _, fn := range funcs {
			sb.WriteString(fmt.Sprintf("### `%s()`\n\n", fn.name))
			sb.WriteString("- Happy path with valid inputs\n")
			sb.WriteString("- Edge cases: null, undefined, empty\n\n")
		}
		return sb.String(), nil
	}

	// Generate Jest test file
	var sb strings.Builder
	sb.WriteString("// Generated by OpenKitty test_gen\n\n")

	for _, fn := range funcs {
		sb.WriteString(fmt.Sprintf("describe('%s', () => {\n", fn.name))
		sb.WriteString(fmt.Sprintf("  test('should work correctly', () => {\n"))
		sb.WriteString(fmt.Sprintf("    // const result = %s(testInput);\n", fn.name))
		sb.WriteString(fmt.Sprintf("    // expect(result).toBe(expected);\n"))
		sb.WriteString(fmt.Sprintf("  });\n"))
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  test('should handle edge cases', () => {\n"))
		sb.WriteString(fmt.Sprintf("    // expect(() => %s(null)).not.toThrow();\n", fn.name))
		sb.WriteString(fmt.Sprintf("  });\n"))
		sb.WriteString("});\n\n")
	}

	if outputPath == "" {
		base := filepath.Base(filePath)
		ext := filepath.Ext(base)
		outputPath = strings.TrimSuffix(filePath, ext) + ".test" + ext
	}

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}

	return fmt.Sprintf("## Test File Generated\n\n**Source:** %s\n**Output:** %s\n**Functions:** %d\n\n```javascript\n%s\n```",
		filePath, outputPath, len(funcs), sb.String()), nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
