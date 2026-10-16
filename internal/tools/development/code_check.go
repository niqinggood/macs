package development

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mac/internal/processutil"
	"mac/internal/validationhint"
)

// CodeCheckTool validates code: syntax, lint, test, format.
// Claude Code-style: after writing code, always check it works.
type CodeCheckTool struct {
	workspace             string
	validateOfficePackage func(string) error
}

func NewCodeCheckTool(ws string, officeValidators ...func(string) error) *CodeCheckTool {
	tool := &CodeCheckTool{workspace: ws}
	if len(officeValidators) > 0 {
		tool.validateOfficePackage = officeValidators[0]
	}
	return tool
}
func (t *CodeCheckTool) Name() string { return "code_check" }
func (t *CodeCheckTool) Description() string {
	return "Validate code after writing or modifying files. Run the smallest meaningful check first, then broaden if it passes. " +
		"Actions: syntax(file; also validates DOCX/XLSX/PPTX OPC relationships), test(pkg), lint(file), format(file), build, run(file), html_check(file). " +
		"Use run_tests when you need structured failure analysis, likely locations, and suggested fixes."
}
func (t *CodeCheckTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"syntax", "test", "lint", "format", "build", "run", "html_check"},
				"description": "What to check: syntax(compile check), test(run tests), lint, format(gofmt), build(go build), run(execute), html_check(common HTML/inline JS issues)",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Target file or package path",
			},
			"args": map[string]interface{}{
				"type":        "string",
				"description": "Additional arguments (test flags, build tags, run args)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CodeCheckTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	file, _ := params["file"].(string)
	extraArgs, _ := params["args"].(string)

	switch action {
	case "syntax":
		return t.checkSyntax(ctx, file)
	case "test":
		return t.runTests(ctx, file, extraArgs)
	case "lint":
		return t.runLint(ctx, file)
	case "format":
		return t.formatCode(ctx, file)
	case "build":
		return t.buildProject(ctx, file)
	case "html_check":
		return t.checkHTMLSyntax(ctx, file)
	case "run":
		return t.runCode(ctx, file, extraArgs)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *CodeCheckTool) checkSyntax(ctx context.Context, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file path required for syntax check")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		pkg := validationhint.GoTestPackagePath(t.workspace, path)
		cmd := exec.CommandContext(ctx, "go", "test", "-run", "^$", "-count=1", pkg)
		cmd.Dir = t.workspace
		if cmd.Dir == "" {
			cmd.Dir = filepath.Dir(path)
			pkg = "."
			cmd.Args[len(cmd.Args)-1] = pkg
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[FAIL] Go compile check failed for %s (%s):\n%s", path, pkg, string(out)), nil
		}
		return fmt.Sprintf("[OK] Go compile check OK: %s (%s)", path, pkg), nil

	case ".py":
		cmd, cmdErr := processutil.PythonCommandContext(ctx, "-m", "py_compile", path)
		if cmdErr != nil {
			return "[FAIL] Python syntax check could not start: " + cmdErr.Error(), nil
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[FAIL] Python syntax error:\n%s", commandFailureDetail(out, err)), nil
		}
		return "[OK] Python syntax OK: " + path, nil

	case ".js", ".jsx", ".ts", ".tsx":
		cmd := exec.CommandContext(ctx, "node", "--check", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[FAIL] JS syntax error:\n%s", string(out)), nil
		}
		return "[OK] JS syntax OK: " + path, nil

	case ".html":
		return "[OK] HTML file (no syntax check): " + path, nil

	case ".docx", ".xlsx", ".pptx":
		if t.validateOfficePackage == nil {
			return "[FAIL] OOXML package validator is not configured", nil
		}
		if err := t.validateOfficePackage(path); err != nil {
			return fmt.Sprintf("[FAIL] OOXML package validation failed for %s: %v", path, err), nil
		}
		return "[OK] OOXML package relationships valid: " + path, nil

	default:
		return fmt.Sprintf("[WARN] No syntax checker for %s files", ext), nil
	}
}

func (t *CodeCheckTool) runTests(ctx context.Context, pkg, args string) (string, error) {
	if pkg == "" {
		pkg = "./..."
	}
	cmdArgs := []string{"test"}
	if args != "" {
		cmdArgs = append(cmdArgs, processutil.ParseCommandLine(args)...)
	}
	cmdArgs = append(cmdArgs, pkg)

	// Detect language
	if strings.HasSuffix(pkg, ".py") || strings.Contains(pkg, "test_") {
		cmd, cmdErr := processutil.PythonCommandContext(ctx, "-m", "pytest", pkg, "-v")
		if cmdErr != nil {
			return "[FAIL] Python tests could not start: " + cmdErr.Error(), nil
		}
		cmd.Dir = t.workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "[FAIL] Python tests failed:\n" + commandFailureDetail(out, err), nil
		}
		return string(out), nil
	}

	// Default: Go test
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = t.workspace
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (t *CodeCheckTool) runLint(ctx context.Context, file string) (string, error) {
	if file == "" {
		file = "./..."
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".go":
		// Try golangci-lint first, then go vet
		cmd := exec.CommandContext(ctx, "golangci-lint", "run", path)
		cmd.Dir = filepath.Dir(path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Fallback to go vet
			cmd2 := exec.CommandContext(ctx, "go", "vet", path)
			out2, _ := cmd2.CombinedOutput()
			return fmt.Sprintf("[WARN] Lint issues:\n%s\n\nGo vet:\n%s", string(out), string(out2)), nil
		}
		return "[OK] Lint OK: " + path, nil

	case ".py":
		cmd, cmdErr := processutil.PythonCommandContext(ctx, "-m", "flake8", path)
		if cmdErr != nil {
			return "[WARN] Python lint could not start: " + cmdErr.Error(), nil
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[WARN] Python lint:\n%s", commandFailureDetail(out, err)), nil
		}
		return "[OK] Python lint OK", nil

	default:
		return fmt.Sprintf("[WARN] No linter for %s", ext), nil
	}
}

func (t *CodeCheckTool) formatCode(ctx context.Context, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file path required")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".go":
		cmd := exec.CommandContext(ctx, "gofmt", "-w", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[FAIL] Format failed: %s", string(out)), nil
		}
		return "[OK] Formatted: " + path, nil

	case ".py":
		cmd, cmdErr := processutil.PythonCommandContext(ctx, "-m", "black", path)
		if cmdErr != nil {
			return "[WARN] Python formatter could not start: " + cmdErr.Error(), nil
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("[WARN] Format: %s", commandFailureDetail(out, err)), nil
		}
		return "[OK] Formatted: " + path, nil

	default:
		return fmt.Sprintf("[WARN] No formatter for %s", ext), nil
	}
}

func (t *CodeCheckTool) buildProject(ctx context.Context, target string) (string, error) {
	if target == "" {
		target = "."
	}

	tmpDir, err := os.MkdirTemp("", "openkitty-build-*")
	if err != nil {
		return "", fmt.Errorf("create build temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	output := filepath.Join(tmpDir, "build_output")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", output, target)
	cmd.Dir = t.workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("[FAIL] Build failed:\n%s", string(out)), nil
	}
	return "[OK] Build OK", nil
}

func (t *CodeCheckTool) runCode(ctx context.Context, file, args string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file required to run")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	ext := strings.ToLower(filepath.Ext(path))

	var cmd *exec.Cmd
	switch ext {
	case ".go":
		cmd = exec.CommandContext(ctx, "go", "run", path)
	case ".py":
		var cmdErr error
		cmd, cmdErr = processutil.PythonCommandContext(ctx, path)
		if cmdErr != nil {
			return "[FAIL] Run error:\n" + cmdErr.Error(), nil
		}
	case ".js":
		cmd = exec.CommandContext(ctx, "node", path)
	case ".html":
		return fmt.Sprintf("[OK] HTML file ready: %s (open in browser)", path), nil
	default:
		return fmt.Sprintf("[WARN] Cannot run %s files directly", ext), nil
	}
	if args != "" {
		cmd.Args = append(cmd.Args, processutil.ParseCommandLine(args)...)
	}
	// Tool arguments and user-created artifacts are workspace-relative across
	// OpenKitty. Keep the same rule when the executable itself lives in a nested
	// skill runtime directory; Python still adds the script directory to sys.path.
	cmd.Dir = t.workspace
	if strings.TrimSpace(cmd.Dir) == "" {
		cmd.Dir = filepath.Dir(path)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("[FAIL] Run error:\n%s", commandFailureDetail(out, err)), nil
	}
	result := strings.TrimSpace(string(out))
	if len(result) > 2000 {
		result = result[:2000] + "\n...(truncated)"
	}
	return result, nil
}

func commandFailureDetail(out []byte, err error) string {
	detail := strings.TrimSpace(string(out))
	if err != nil {
		if detail != "" {
			detail += "\n"
		}
		detail += err.Error()
	}
	if detail == "" {
		return "command failed without diagnostic output"
	}
	return detail
}

// ==================== project_init tool ====================

type ProjectInitTool struct{ workspace string }

func NewProjectInitTool(ws string) *ProjectInitTool { return &ProjectInitTool{ws} }
func (t *ProjectInitTool) Name() string             { return "project_init" }
func (t *ProjectInitTool) Description() string {
	return "Initialize a new project with proper structure. Creates directories, starter files, and dependency config. " +
		"Types: go(cli), go(web), python, node, html(spa)."
}
func (t *ProjectInitTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":  map[string]interface{}{"type": "string", "description": "Project name (used as directory name)"},
			"ptype": map[string]interface{}{"type": "string", "description": "Project type: go-cli, go-web, python, node, html-spa"},
			"dir":   map[string]interface{}{"type": "string", "description": "Parent directory (default: storage/)"},
		},
		"required": []string{"name", "ptype"},
	}
}
func (t *ProjectInitTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	ptype, _ := params["ptype"].(string)
	dir, _ := params["dir"].(string)
	if dir == "" {
		dir = "storage"
	}
	projectDir := filepath.Join(t.workspace, dir, name)
	os.MkdirAll(projectDir, 0755)

	files := make(map[string]string)

	switch ptype {
	case "go-cli":
		files["main.go"] = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n"
		files[filepath.Join("cmd", name, "main.go")] = "package main\n\nfunc main() {\n\t// entry point\n}\n"
		files["go.mod"] = "module " + name + "\n\ngo 1.21\n"
		files["README.md"] = "# " + name + "\n\n## Build\n```bash\ngo build -o " + name + " .\n```\n"
	case "python":
		files["main.py"] = "#!/usr/bin/env python3\n\ndef main():\n    print(\"Hello\")\n\nif __name__ == \"__main__\":\n    main()\n"
		files["requirements.txt"] = "# dependencies\n"
		files["README.md"] = "# " + name + "\n\n## Run\n```bash\npython main.py\n```\n"
	case "node":
		files["index.js"] = "#!/usr/bin/env node\n\nconsole.log(\"Hello\");\n"
		files["package.json"] = "{\n  \"name\": \"" + name + "\",\n  \"version\": \"1.0.0\",\n  \"main\": \"index.js\"\n}\n"
		files["README.md"] = "# " + name + "\n\n## Run\n```bash\nnode index.js\n```\n"
	case "html-spa":
		files["index.html"] = "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n  <meta charset=\"UTF-8\">\n  <title>" + name + "</title>\n  <style>body{font-family:Arial;max-width:800px;margin:40px auto;padding:0 20px;}</style>\n</head>\n<body>\n  <h1>" + name + "</h1>\n  <script>\n    // Your JavaScript here\n  </script>\n</body>\n</html>\n"
		files["README.md"] = "# " + name + "\n\nOpen index.html in browser.\n"
	default:
		return "", fmt.Errorf("unknown project type: %s (use: go-cli, python, node, html-spa)", ptype)
	}

	for path, content := range files {
		fullPath := filepath.Join(projectDir, path)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	return fmt.Sprintf("[OK] Project initialized: %s (%s)\nFiles: %d", projectDir, ptype, len(files)), nil
}

// checkHTMLSyntax validates HTML files for common JS-in-HTML escaping issues
func (t *CodeCheckTool) checkHTMLSyntax(ctx context.Context, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file path required")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	var issues []string
	lines := strings.Split(content, "\n")
	inScript := false
	// script block tracking

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Track script blocks
		if strings.Contains(trimmed, "<script") && !strings.Contains(trimmed, "</script") {
			inScript = true
		}
		if inScript && strings.Contains(trimmed, "</script>") {
			inScript = false
		}
		// In script: check for < that could break HTML parser
		if inScript {
			// Skip comments
			stripped := line
			if idx := strings.Index(stripped, "//"); idx >= 0 {
				stripped = stripped[:idx]
			}
			// Check for < used as operator (comparison, not HTML tag)
			// Pattern: < followed by space/digit/letter but not starting a known HTML-safe pattern
			for j := 0; j < len(stripped)-1; j++ {
				if stripped[j] == '<' {
					next := stripped[j+1]
					// If < is followed by letter that could start a tag, or by space then letter
					if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') {
						if !strings.HasPrefix(stripped[j:], "</") {
							issues = append(issues, fmt.Sprintf("  Line %d: possible unescaped '<' in JS: ...%s...", i+1, strings.TrimSpace(stripped[max(0, j-5):min(len(stripped), j+15)])))
						}
					}
				}
			}
		}
		// Check for unclosed tags
		if strings.Count(line, "<script") > strings.Count(line, "</script>") {
			if !strings.Contains(line, "</script>") && strings.Contains(line, "<script") && !strings.Contains(line, "/>") {
				// OK - multi-line script
			}
		}
	}

	// Check for common issues
	if strings.Contains(content, "document.write") {
		issues = append(issues, "  Warning: document.write detected (may cause issues)")
	}

	if len(issues) == 0 {
		return "HTML syntax check passed. No issues found.", nil
	}

	result := fmt.Sprintf("HTML check: %d issues found:\n%s\n\nTip: In <script> blocks, replace < with \x3C or use template literals instead of inline HTML strings.", len(issues), strings.Join(issues, "\n"))
	return result, nil
}
