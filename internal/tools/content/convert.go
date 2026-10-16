package content

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mac/internal/fsutil"
	"mac/internal/spreadsheet"
)

const pptGenerationTimeout = 120 * time.Second

// ConvertDependencies keeps application-owned authorization, secret
// redaction, runtime materialization and cross-domain document writing out of
// the reusable conversion tool.
type ConvertDependencies struct {
	EnforcePath        func(context.Context, string, string) error
	RedactForExport    func(string) string
	WriteDOCX          func(path, markdown string) error
	ResolvePython      func() (string, []string)
	OpenKittyHome      func() string
	EmbeddedRuntimeDir func(context.Context, string, string) string
}

func defaultConvertDependencies() ConvertDependencies {
	return ConvertDependencies{
		EnforcePath:        func(context.Context, string, string) error { return nil },
		RedactForExport:    func(value string) string { return value },
		WriteDOCX:          func(string, string) error { return fmt.Errorf("DOCX writer is not configured") },
		ResolvePython:      func() (string, []string) { return "python", nil },
		OpenKittyHome:      func() string { return "" },
		EmbeddedRuntimeDir: func(context.Context, string, string) string { return "" },
	}
}

func mergeConvertDependencies(defaults, supplied ConvertDependencies) ConvertDependencies {
	if supplied.EnforcePath != nil {
		defaults.EnforcePath = supplied.EnforcePath
	}
	if supplied.RedactForExport != nil {
		defaults.RedactForExport = supplied.RedactForExport
	}
	if supplied.WriteDOCX != nil {
		defaults.WriteDOCX = supplied.WriteDOCX
	}
	if supplied.ResolvePython != nil {
		defaults.ResolvePython = supplied.ResolvePython
	}
	if supplied.OpenKittyHome != nil {
		defaults.OpenKittyHome = supplied.OpenKittyHome
	}
	if supplied.EmbeddedRuntimeDir != nil {
		defaults.EmbeddedRuntimeDir = supplied.EmbeddedRuntimeDir
	}
	return defaults
}

// ConvertTool converts between document formats, primarily markdown → PDF/DOCX/PPTX/HTML.
type ConvertTool struct {
	workspace string
	deps      ConvertDependencies
}

func NewConvertTool(workspace string, dependencies ...ConvertDependencies) *ConvertTool {
	deps := defaultConvertDependencies()
	if len(dependencies) > 0 {
		deps = mergeConvertDependencies(deps, dependencies[0])
	}
	return &ConvertTool{workspace: workspace, deps: deps}
}

func (t *ConvertTool) Name() string { return "convert" }

func (t *ConvertTool) Description() string {
	return "Convert markdown content or files to PDF, DOCX (Word), PPTX (PowerPoint), HTML, CSV, or a real XLSX workbook. Markdown-to-XLSX requires at least one Markdown table. " +
		"Accepts either a file path or raw markdown content. " +
		"Example: convert(input='report.md', output='report.pdf') or convert(markdown='# Title', output='out.html')"
}

func (t *ConvertTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input": map[string]interface{}{
				"type":        "string",
				"description": "Input markdown file path (e.g. 'storage/report.md')",
			},
			"markdown": map[string]interface{}{
				"type":        "string",
				"description": "Raw markdown content to convert (alternative to input file)",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output file path. Format determined by extension: .pdf .html .docx .pptx .csv .xlsx",
			},
			"generator": map[string]interface{}{
				"type":        "string",
				"description": "Required for direct PPTX conversion. base-ppt is the explicit fast/fallback structured route; pptmaster only exports an already prepared upstream project declared with pptmaster_project: <path>. Regular audience-facing PPTs must use ppt-art-director's task-specific per-deck builder instead of convert.",
			},
		},
		"required": []string{"output"},
	}
}

func (t *ConvertTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	output, _ := params["output"].(string)
	inputFile, _ := params["input"].(string)
	markdown, _ := params["markdown"].(string)
	generator, _ := params["generator"].(string)

	if output == "" {
		return "", fmt.Errorf("output path is required")
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, output); err != nil {
		return "", err
	}

	// Read markdown content
	if markdown == "" && inputFile != "" {
		if err := t.deps.EnforcePath(ctx, t.workspace, inputFile); err != nil {
			return "", err
		}
		inputPath := inputFile
		if !filepath.IsAbs(inputPath) {
			inputPath = filepath.Join(t.workspace, inputPath)
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return "", fmt.Errorf("read input file: %w", err)
		}
		markdown = string(data)
	}
	if markdown == "" {
		return "", fmt.Errorf("either 'input' file or 'markdown' content is required")
	}

	// Resolve output path
	outPath := output
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(t.workspace, outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(outPath))
	// Conversion is an explicit export boundary. Redact only the exported copy;
	// the source file, model context, tool result, and persisted workflow state
	// remain byte-for-byte unchanged.
	markdown = t.deps.RedactForExport(markdown)
	var (
		result string
		err    error
	)
	switch ext {
	case ".html":
		result, err = t.toHTML(markdown, outPath)
	case ".pdf":
		result, err = t.toPDF(ctx, markdown, outPath)
	case ".docx":
		result, err = t.toDOCX(markdown, outPath)
	case ".pptx":
		result, err = t.toPPTX(ctx, markdown, outPath, generator)
	case ".csv", ".xlsx":
		result, err = t.toCSV(markdown, outPath)
	default:
		return "", fmt.Errorf("unsupported output format: %s (use .html .pdf .docx .pptx .csv .xlsx)", ext)
	}
	if err != nil {
		return "", err
	}
	info, statErr := os.Stat(outPath)
	if statErr != nil {
		return "", fmt.Errorf("conversion did not create requested output %s: %w", output, statErr)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("conversion created unusable output %s", output)
	}
	return result, nil
}

// toHTML converts markdown to a styled HTML page.
func (t *ConvertTool) toHTML(markdown, outPath string) (string, error) {
	html := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8">
<style>
@page{size:A4;margin:1.8cm 2cm;@bottom-center{content:counter(page);font-size:9px;color:#999;font-family:'Microsoft YaHei','PingFang SC',Arial,sans-serif}}
body{font-family:'Microsoft YaHei','PingFang SC','Noto Sans SC',-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;max-width:820px;margin:0 auto;padding:10px 20px 40px;line-height:1.8;color:#1a1a1a;font-size:10.5pt;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale}
h1{font-size:1.8em;color:#111;border-bottom:3px solid #1a73e8;padding-bottom:10px;margin-top:20px;margin-bottom:20px}
h2{font-size:1.4em;color:#1e40af;margin-top:28px;margin-bottom:12px;padding-bottom:4px;border-bottom:1px solid #e5e7eb}
h3{font-size:1.15em;color:#374151;margin-top:20px;margin-bottom:8px}
h4{font-size:1.05em;color:#4b5563;margin-top:16px;margin-bottom:6px}
p{margin:8px 0 16px}
a{color:#2563eb;text-decoration:none}
a:hover{text-decoration:underline}
img{max-width:100%%;height:auto;display:block;margin:16px auto;border-radius:6px;box-shadow:0 2px 8px rgba(0,0,0,0.1)}
table{border-collapse:collapse;width:100%%;margin:16px 0;font-size:0.95em;border-radius:4px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,0.06)}
thead,tbody{width:100%%}
th,td{border:1px solid #d1d5db;padding:10px 14px;text-align:left}
th{background:#1a73e8;color:#fff;font-weight:600;border-bottom:2px solid #1557b0}
td{background:#fff}
tr:nth-child(even) td{background:#f8fafc}
tr:hover td{background:#e8f0fe}
code{background:#f1f5f9;padding:2px 6px;border-radius:4px;font-size:0.9em;font-family:'Cascadia Code','Fira Code','SF Mono','Consolas',monospace;border:1px solid #e2e8f0}
pre{background:#1e293b;color:#e2e8f0;padding:20px;border-radius:8px;overflow-x:auto;line-height:1.6;font-size:0.88em;margin:16px 0}
pre code{background:none;padding:0;border:none;color:inherit;font-size:inherit}
blockquote{background:#f0f4ff;border-left:4px solid #1a73e8;margin:16px 0;padding:14px 18px;color:#374151;border-radius:0 6px 6px 0;font-size:0.95em}
ul,ol{padding-left:24px;margin:8px 0 16px}
li{margin-bottom:8px;line-height:1.7}
ul ul,ol ol,ul ol,ol ul{margin:4px 0}
hr{border:none;border-top:1px solid #e5e7eb;margin:24px 0}
strong{color:#111}
</style></head><body>%s</body></html>`, markdown)
	return t.writeFile(outPath, html)
}

// toPDF converts markdown to PDF via chromedp (headless Chrome print).
func (t *ConvertTool) toPDF(ctx context.Context, markdown, outPath string) (string, error) {
	// First create an HTML file, then print to PDF via chromedp
	htmlPath := strings.TrimSuffix(outPath, ".pdf") + "_temp.html"
	if _, err := t.toHTML(markdown, htmlPath); err != nil {
		return "", err
	}
	defer os.Remove(htmlPath)

	// Use chromedp to print HTML to PDF
	absHTML, _ := filepath.Abs(htmlPath)
	fileURL := "file:///" + strings.ReplaceAll(absHTML, "\\", "/")

	// Try chromedp first
	if err := t.printPDFWithChromeDP(ctx, fileURL, outPath); err == nil {
		return t.formatResult(outPath), nil
	}

	// Fallback: use the Node rendering ecosystem before requiring Python.
	if err := t.printPDFWithNode(htmlPath, outPath); err == nil {
		return t.formatResult(outPath), nil
	}

	// Last runtime fallback: Python/WeasyPrint.
	if err := t.printPDFWithWeasyPrint(htmlPath, outPath); err == nil {
		return t.formatResult(outPath), nil
	}

	// Last resort: save the HTML and tell the user
	htmlOut := strings.TrimSuffix(outPath, ".pdf") + ".html"
	t.toHTML(markdown, htmlOut)
	return fmt.Sprintf("PDF generation requires headless Chrome. Saved as HTML instead: %s", htmlOut), nil
}

func (t *ConvertTool) printPDFWithChromeDP(ctx context.Context, url, outPath string) error {
	// Use chromedp package if available
	if _, err := exec.LookPath("chromedp"); err != nil {
		// chromedp is a Go library, not a CLI — check if Chrome exists
		chromePaths := []string{"google-chrome", "chrome", "chromium", "chromium-browser",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`}
		var chromePath string
		for _, p := range chromePaths {
			if _, err := exec.LookPath(p); err == nil {
				chromePath = p
				break
			}
			if _, err := os.Stat(p); err == nil {
				chromePath = p
				break
			}
		}
		if chromePath == "" {
			return fmt.Errorf("chrome not found")
		}
		cmd := exec.CommandContext(ctx, chromePath,
			"--headless", "--disable-gpu", "--no-sandbox",
			"--print-to-pdf="+outPath, url)
		return cmd.Run()
	}
	return fmt.Errorf("not implemented")
}

func (t *ConvertTool) printPDFWithWeasyPrint(htmlPath, outPath string) error {
	cmd := exec.Command("weasyprint", htmlPath, outPath)
	cmd.Env = os.Environ()
	return cmd.Run()
}

func (t *ConvertTool) printPDFWithNode(htmlPath, outPath string) error {
	// Try marked + puppeteer via node one-liner
	script := fmt.Sprintf(`const fs=require('fs');const html=fs.readFileSync('%s','utf8');const puppeteer=require('puppeteer');(async()=>{const b=await puppeteer.launch({headless:true,args:['--no-sandbox']});const p=await b.newPage();await p.setContent(html);await p.pdf({path:'%s',format:'A4'});await b.close()})()`,
		strings.ReplaceAll(htmlPath, "\\", "\\\\"), strings.ReplaceAll(outPath, "\\", "\\\\"))
	cmd := exec.Command("node", "-e", script)
	return cmd.Run()
}

// toDOCX converts markdown to Word document (simple paragraph-based approach).
func (t *ConvertTool) toDOCX(markdown, outPath string) (string, error) {
	// Use pandoc if available (best quality)
	if _, err := exec.LookPath("pandoc"); err == nil {
		cmd := exec.Command("pandoc", "-f", "markdown", "-t", "docx", "-o", outPath)
		cmd.Stdin = strings.NewReader(markdown)
		cmd.Env = os.Environ()
		if err := cmd.Run(); err == nil {
			return t.formatResult(outPath), nil
		}
	}

	// Deterministic fallback: reuse the built-in OOXML writer. A missing optional
	// desktop dependency must not be reported as a successful conversion without
	// the requested file.
	if err := t.deps.WriteDOCX(outPath, markdown); err != nil {
		return "", fmt.Errorf("write DOCX fallback: %w", err)
	}
	return t.formatResult(outPath), nil
}

// toPPTX converts markdown to PowerPoint using the configured generator route.
func (t *ConvertTool) toPPTX(ctx context.Context, markdown, outPath, generator string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pptGenerationTimeout)
	defer cancel()
	markdown, inlineGenerator := extractPPTGeneratorDirective(markdown)
	if strings.TrimSpace(generator) == "" {
		generator = inlineGenerator
	}
	generator = normalizePPTGenerator(generator)

	absOut, _ := filepath.Abs(outPath)
	switch generator {
	case "":
		return "", fmt.Errorf("PPTX route is required: regular audience-facing decks must use ppt-art-director and its task-specific per-deck builder; use generator=base-ppt only for an explicit fast/fallback structured deck, or generator=pptmaster only after completing the prepared upstream project")
	case "pptmaster":
		if err := t.tryPPTMaster(ctx, markdown, absOut); err != nil {
			return "", fmt.Errorf("pptmaster export failed without silent downgrade: %w", err)
		}
		return t.formatResult(outPath), nil
	case "base-ppt":
		return t.tryBasePPT(ctx, markdown, outPath, absOut)
	default:
		return "", fmt.Errorf("unsupported PPTX generator %q: regular decks use the ppt-art-director task-specific builder; direct convert supports only base-ppt and prepared pptmaster exports", generator)
	}
}

// ToPPTX exposes direct conversion for application compatibility adapters.
func (t *ConvertTool) ToPPTX(ctx context.Context, markdown, outPath, generator string) (string, error) {
	return t.toPPTX(ctx, markdown, outPath, generator)
}

func (t *ConvertTool) tryBasePPT(ctx context.Context, markdown, outPath, absOut string) (string, error) {
	pythonScript := t.basePPTScriptPathForContext(ctx, "generate.py")
	if pythonScript != "" {
		tmpJSON := filepath.Join(os.TempDir(), "convert_ppt_"+time.Now().Format("150405.000000000")+".json")
		defer os.Remove(tmpJSON)
		if err := t.buildPPTWorkflow(ctx, markdown, filepath.Base(outPath), tmpJSON); err != nil {
			return "", err
		}

		python, prefixArgs := t.deps.ResolvePython()
		args := append(prefixArgs, pythonScript, tmpJSON, absOut)
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = filepath.Dir(pythonScript)
		lastOutput, generateErr := cmd.CombinedOutput()
		if generateErr == nil {
			return t.formatResult(outPath), nil
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("PPT generation canceled or timed out: %w", ctx.Err())
		}
		if jsErr := t.toPPTXWithJSFallback(ctx, markdown, absOut); jsErr == nil {
			return t.formatResult(outPath), nil
		} else if ctx.Err() != nil {
			return "", fmt.Errorf("PPT generation canceled or timed out: %w", ctx.Err())
		}
		return "", fmt.Errorf("premium ppt generation failed:\n%s", strings.TrimSpace(string(lastOutput)))
	}

	if err := t.toPPTXWithJSFallback(ctx, markdown, absOut); err != nil {
		return "", err
	}
	return t.formatResult(outPath), nil
}

// TryBasePPT exposes the explicit base-ppt route to host adapters.
func (t *ConvertTool) TryBasePPT(ctx context.Context, markdown, outPath, absOut string) (string, error) {
	return t.tryBasePPT(ctx, markdown, outPath, absOut)
}

func (t *ConvertTool) tryPPTMaster(ctx context.Context, markdown, absOut string) error {
	projectPath := extractPPTMasterProjectPath(markdown)
	if projectPath == "" {
		return fmt.Errorf("pptmaster export requires a prepared upstream project; run use_skill ppt-master first and pass pptmaster_project: <project-path>")
	}

	pptMasterRoot, err := t.resolveBundledPPTMasterRoot(ctx)
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(pptMasterRoot, "skills", "ppt-master", "scripts", "svg_to_pptx.py")
	if info, statErr := os.Stat(scriptPath); statErr != nil || info.IsDir() {
		return fmt.Errorf("pptmaster export script not found: %s", scriptPath)
	}

	projectPath = strings.TrimSpace(projectPath)
	if !filepath.IsAbs(projectPath) {
		projectPath = filepath.Join(t.workspace, projectPath)
	}
	if _, err := os.Stat(projectPath); err != nil {
		return fmt.Errorf("pptmaster project not found: %w", err)
	}

	python, prefixArgs := t.deps.ResolvePython()
	args := append(prefixArgs, scriptPath, projectPath, "-s", "final", "-o", absOut)
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = filepath.Dir(scriptPath)
	lastOutput, exportErr := cmd.CombinedOutput()
	if exportErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("PPTmaster export canceled or timed out: %w", ctx.Err())
	}
	return fmt.Errorf("pptmaster export failed: %s", strings.TrimSpace(string(lastOutput)))
}

func (t *ConvertTool) resolveBundledPPTMasterRoot(ctx context.Context) (string, error) {
	runtimeDir := t.deps.EmbeddedRuntimeDir(ctx, t.workspace, filepath.Join("presentation", "ppt-master"))
	if strings.TrimSpace(runtimeDir) == "" {
		return "", fmt.Errorf("bundled ppt-master runtime is unavailable")
	}
	return runtimeDir, nil
}

func extractPPTMasterProjectPath(markdown string) string {
	re := regexp.MustCompile(`(?im)^\s*pptmaster_project\s*[:=]\s*(.+?)\s*$`)
	match := re.FindStringSubmatch(markdown)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func extractPPTGeneratorDirective(markdown string) (string, string) {
	lines := strings.Split(markdown, "\n")
	cleaned := make([]string, 0, len(lines))
	generator := ""
	re := regexp.MustCompile(`(?i)^\s*generator\s*[:=]\s*([a-z0-9_-]+)\s*$`)
	for _, line := range lines {
		match := re.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 2 && generator == "" {
			generator = match[1]
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n")), generator
}

func normalizePPTGenerator(generator string) string {
	switch strings.ToLower(strings.TrimSpace(generator)) {
	case "":
		return ""
	case "base-ppt", "base_ppt", "baseppt", "better-ppt", "better_ppt", "betterppt":
		return "base-ppt"
	case "pptmaster", "ppt-master", "ppt_master", "pptmaster-main":
		return "pptmaster"
	default:
		return strings.ToLower(strings.TrimSpace(generator))
	}
}

// NormalizePPTGenerator canonicalizes supported generator aliases.
func NormalizePPTGenerator(generator string) string {
	return normalizePPTGenerator(generator)
}

func (t *ConvertTool) buildPPTWorkflow(ctx context.Context, markdown, fallbackTitle, outJSON string) error {
	plannerScript := t.basePPTScriptPathForContext(ctx, "plan_workflow.py")
	if plannerScript != "" {
		tmpMD := filepath.Join(os.TempDir(), "convert_plan_"+time.Now().Format("150405.000000000")+".md")
		if err := os.WriteFile(tmpMD, []byte(markdown), 0644); err != nil {
			return fmt.Errorf("write planner markdown: %w", err)
		}
		defer os.Remove(tmpMD)

		python, prefixArgs := t.deps.ResolvePython()
		args := append(prefixArgs, plannerScript, "--input", tmpMD, "--output", outJSON, "--title", fallbackTitle)
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = filepath.Dir(plannerScript)
		lastOutput, plannerErr := cmd.CombinedOutput()
		if plannerErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("PPT planning canceled or timed out: %w", ctx.Err())
		}
		if len(lastOutput) > 0 {
			// Fall through to the Go planner, but preserve the planner failure in logs when needed.
		}
	}

	workflow := markdownToPremiumPPTWorkflow(markdown, fallbackTitle)
	payload, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("build ppt workflow: %w", err)
	}
	if err := os.WriteFile(outJSON, payload, 0644); err != nil {
		return fmt.Errorf("write ppt workflow: %w", err)
	}
	return nil
}

func (t *ConvertTool) toPPTXWithJSFallback(ctx context.Context, markdown, outPath string) error {
	scriptPath := t.basePPTScriptPathForContext(ctx, "generate.js")
	if scriptPath == "" {
		return fmt.Errorf("base-ppt skill not found")
	}

	tmpMD := filepath.Join(os.TempDir(), "convert_"+time.Now().Format("150405.000000000")+".md")
	if err := os.WriteFile(tmpMD, []byte(markdown), 0644); err != nil {
		return err
	}
	defer os.Remove(tmpMD)

	absMD, _ := filepath.Abs(tmpMD)
	jsTheme := "default"
	switch detectPPTProfile(markdown).Theme {
	case "exhibit", "mckinsey":
		jsTheme = "finance"
	case "google_style":
		jsTheme = "tech"
	case "smart_red":
		jsTheme = "fjord"
	}
	cmd := exec.CommandContext(ctx, "node", scriptPath, absMD, outPath, jsTheme)
	cmd.Dir = filepath.Dir(scriptPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ppt generation failed: %v\n%s", err, string(output))
	}
	return nil
}

func (t *ConvertTool) basePPTScriptPath(scriptName string) string {
	return t.basePPTScriptPathForContext(context.Background(), scriptName)
}

// BasePPTScriptPath resolves a bundled base-ppt helper for host adapters.
func (t *ConvertTool) BasePPTScriptPath(scriptName string) string {
	return t.basePPTScriptPath(scriptName)
}

func (t *ConvertTool) basePPTScriptPathForContext(ctx context.Context, scriptName string) string {
	// Refresh the generated runtime copy before considering cached runtime
	// candidates. Otherwise a long-lived installation can keep executing an old
	// embedded generator after the OpenKitty binary has been upgraded.
	embeddedRuntimeDir := t.deps.EmbeddedRuntimeDir(ctx, t.workspace, filepath.Join("presentation", "base-ppt"))
	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path != "" {
			candidates = append(candidates, path)
		}
	}
	openKittyHome := t.deps.OpenKittyHome()
	for _, base := range []string{t.workspace, currentProcessWorkingDir(), currentExecutableDir(), openKittyHome, filepath.Join(openKittyHome, "runtime")} {
		addBasePPTScriptCandidates(&candidates, base, scriptName)
	}
	if path := fsutil.FirstExistingPath(candidates...); path != "" {
		return path
	}
	if embeddedRuntimeDir != "" {
		add(filepath.Join(embeddedRuntimeDir, scriptName))
	}
	return fsutil.FirstExistingPath(candidates...)
}

func currentProcessWorkingDir() string {
	dir, _ := os.Getwd()
	return dir
}

func currentExecutableDir() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(path)
}

func addBasePPTScriptCandidates(candidates *[]string, base, scriptName string) {
	base = strings.TrimSpace(base)
	if base == "" {
		return
	}
	current, err := filepath.Abs(base)
	if err != nil {
		current = base
	}
	seen := map[string]bool{}
	for {
		if !seen[current] {
			seen[current] = true
			for _, rel := range []string{
				filepath.Join("skills", "presentation", "base-ppt", scriptName),
				filepath.Join("skills", "base-ppt", scriptName),
				filepath.Join("skills", "presentation", "better-ppt", scriptName),
				filepath.Join("skills", "better-ppt", scriptName),
				filepath.Join("builtin", "presentation", "base-ppt", scriptName),
				filepath.Join("runtime", "builtin", "presentation", "base-ppt", scriptName),
				filepath.Join("builtin", "presentation", "better-ppt", scriptName),
				filepath.Join("runtime", "builtin", "presentation", "better-ppt", scriptName),
			} {
				*candidates = append(*candidates, filepath.Join(current, rel))
			}
		}
		parent := filepath.Dir(current)
		if parent == current || parent == "." || parent == "" {
			break
		}
		current = parent
	}
}

type pptSection struct {
	title string
	items []string
}

type pptProfile struct {
	Theme       string
	Eyebrow     string
	Subtitle    string
	Summary     string
	Agenda      string
	Actions     string
	EndTitle    string
	EndSubtitle string
	Tags        []string
	Footer      string
}

func markdownToPremiumPPTWorkflow(markdown, fallbackTitle string) map[string]interface{} {
	title := strings.TrimSuffix(fallbackTitle, filepath.Ext(fallbackTitle))
	if title == "" {
		title = "Presentation"
	}
	sections := []pptSection{}
	current := pptSection{title: "Key Takeaways"}

	flush := func() {
		if current.title == "" && len(current.items) == 0 {
			return
		}
		if len(current.items) > 0 || current.title != "Key Takeaways" {
			current.items = uniquePPTItems(current.items, 8)
			sections = append(sections, current)
		}
		current = pptSection{}
	}

	for _, raw := range strings.Split(markdown, "\n") {
		line := cleanPPTLine(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# "):
			candidate := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if candidate != "" {
				title = truncatePPTText(candidate, 54)
			}
		case strings.HasPrefix(line, "## "):
			flush()
			current = pptSection{title: truncatePPTText(strings.TrimSpace(strings.TrimPrefix(line, "## ")), 44)}
		case strings.HasPrefix(line, "### "):
			item := truncatePPTText(strings.TrimSpace(strings.TrimPrefix(line, "### ")), 92)
			if item != "" {
				current.items = append(current.items, item)
			}
		default:
			item := strings.TrimLeft(line, "-*0123456789.) ")
			for _, part := range splitPPTSentence(item) {
				if part != "" {
					current.items = append(current.items, truncatePPTText(part, 96))
				}
			}
		}
	}
	flush()
	if len(sections) == 0 {
		sections = append(sections, pptSection{title: "Key Takeaways", items: []string{truncatePPTText(markdown, 96)}})
	}

	prof := detectPPTProfile(markdown)
	summaryItems := collectPPTSummaryItems(sections, 5)
	agendaItems := collectPPTAgendaItems(sections, 5)
	metrics := extractPPTMetrics(summaryItems)

	slides := []map[string]interface{}{
		{
			"type":     "title",
			"eyebrow":  prof.Eyebrow,
			"title":    title,
			"subtitle": prof.Subtitle,
			"tags":     prof.Tags,
		},
	}

	if len(agendaItems) > 0 {
		slides = append(slides, map[string]interface{}{
			"type":    "content",
			"eyebrow": "00",
			"title":   prof.Agenda,
			"insight": "Start from the core question, then move through evidence, implications and next actions.",
			"footer":  prof.Footer,
			"blocks": []map[string]interface{}{
				{
					"type":  "bullets",
					"span":  "left",
					"items": agendaItems,
				},
				{
					"type":    "text",
					"span":    "right",
					"heading": "How to read",
					"body":    "This deck follows a conclusion-first structure borrowed from consulting templates: summarize the call, show supporting signals, then end with actions and risks.",
				},
			},
		})
	}

	summaryBlocks := []map[string]interface{}{}
	if len(metrics) >= 2 {
		summaryBlocks = append(summaryBlocks, map[string]interface{}{
			"type":    "kpi",
			"span":    "full",
			"metrics": metrics,
		})
	}
	summaryBlocks = append(summaryBlocks, map[string]interface{}{
		"type":  "bullets",
		"span":  "full",
		"items": summaryItems,
	})
	slides = append(slides, map[string]interface{}{
		"type":    "content",
		"eyebrow": "01",
		"title":   prof.Summary,
		"insight": joinedPPTInsight(summaryItems),
		"footer":  prof.Footer,
		"blocks":  summaryBlocks,
	})

	maxSections := 4
	for idx, sec := range sections {
		if idx >= maxSections {
			break
		}
		secTitle := sec.title
		if secTitle == "" {
			secTitle = fmt.Sprintf("Insight %d", idx+1)
		}
		items := uniquePPTItems(sec.items, 6)
		if len(items) == 0 {
			items = []string{"Summarize the available material into a concise decision-ready view."}
		}

		slides = append(slides, map[string]interface{}{
			"type":     "section",
			"number":   fmt.Sprintf("%02d", idx+1),
			"title":    secTitle,
			"subtitle": truncatePPTText(items[0], 72),
		})

		slides = append(slides, map[string]interface{}{
			"type":    "content",
			"eyebrow": fmt.Sprintf("%02d", idx+2),
			"title":   secTitle,
			"insight": joinedPPTInsight(items),
			"footer":  prof.Footer,
			"blocks":  buildPPTSectionBlocks(items),
		})
	}

	actionItems := derivePPTActionItems(sections)
	slides = append(slides, map[string]interface{}{
		"type":    "content",
		"eyebrow": "99",
		"title":   prof.Actions,
		"insight": "Close with explicit actions, not generic wrap-up text.",
		"footer":  prof.Footer,
		"blocks": []map[string]interface{}{
			{
				"type":  "bullets",
				"span":  "left",
				"items": actionItems,
			},
			{
				"type":   "quote",
				"span":   "right",
				"text":   "A good deck ends by making the next decision obvious.",
				"author": "OpenKitty PPT flow",
			},
		},
	})
	slides = append(slides, map[string]interface{}{
		"type":     "end",
		"title":    prof.EndTitle,
		"subtitle": prof.EndSubtitle,
		"contacts": []string{"Generated by OpenKitty", "Theme: " + prof.Theme},
	})

	return map[string]interface{}{
		"meta": map[string]interface{}{
			"title": title,
			"theme": prof.Theme,
		},
		"slides": slides,
	}
}

func detectPPTProfile(markdown string) pptProfile {
	text := strings.ToLower(markdown)
	switch {
	case containsAnyKeyword(text, "stock", "equity", "fund", "valuation", "financial", "investment", "investor", "trading", "portfolio", "market", "profit", "revenue", "ebitda", "advisor", "security", "bond"):
		return pptProfile{
			Theme:       "exhibit",
			Eyebrow:     "Investment readout",
			Subtitle:    "Conclusion-first analysis with signals, evidence and actions.",
			Summary:     "Executive Summary",
			Agenda:      "Readout Structure",
			Actions:     "Decision And Follow-up",
			EndTitle:    "Key Takeaways",
			EndSubtitle: "Validate assumptions, monitor triggers and update the call.",
			Tags:        []string{"Conclusion", "Evidence", "Risk"},
			Footer:      "Exhibit-style investment summary",
		}
	case containsAnyKeyword(text, "government", "soe", "policy", "governance", "public", "city", "regulation", "official", "compliance"):
		return pptProfile{
			Theme:       "government_blue",
			Eyebrow:     "Project briefing",
			Subtitle:    "Structured report with formal hierarchy and execution framing.",
			Summary:     "Core Summary",
			Agenda:      "Report Outline",
			Actions:     "Execution Priorities",
			EndTitle:    "Report Complete",
			EndSubtitle: "Proceed with review, alignment and implementation planning.",
			Tags:        []string{"Summary", "Execution", "Governance"},
			Footer:      "Government-blue structured report",
		}
	case containsAnyKeyword(text, "ai", "model", "platform", "system", "architecture", "product", "roadmap", "engineering", "developer", "tech", "saas", "api", "agent"):
		return pptProfile{
			Theme:       "google_style",
			Eyebrow:     "Product and technology review",
			Subtitle:    "Clean, high-whitespace deck with architecture and roadmap emphasis.",
			Summary:     "What Matters Most",
			Agenda:      "Storyline",
			Actions:     "Build And Launch",
			EndTitle:    "Next Milestones",
			EndSubtitle: "Convert findings into roadmap, owners and checkpoints.",
			Tags:        []string{"Product", "Architecture", "Roadmap"},
			Footer:      "Google-style product review",
		}
	case containsAnyKeyword(text, "sales", "lead", "growth", "customer", "pipeline", "marketing", "crm", "prospect", "outreach", "conversion"):
		return pptProfile{
			Theme:       "smart_red",
			Eyebrow:     "Growth and sales plan",
			Subtitle:    "Action-driven deck with market signals, funnel logic and next moves.",
			Summary:     "Growth Summary",
			Agenda:      "Conversion Path",
			Actions:     "Commercial Next Steps",
			EndTitle:    "Go Execute",
			EndSubtitle: "Turn the analysis into outreach, qualification and close actions.",
			Tags:        []string{"Pipeline", "Conversion", "Action"},
			Footer:      "Smart-red commercial summary",
		}
	default:
		return pptProfile{
			Theme:       "mckinsey",
			Eyebrow:     "Structured business review",
			Subtitle:    "Consulting-style deck with explicit structure, evidence and next steps.",
			Summary:     "Executive Summary",
			Agenda:      "Agenda",
			Actions:     "Recommended Actions",
			EndTitle:    "Thank You",
			EndSubtitle: "Review the findings and move into the next decision cycle.",
			Tags:        []string{"Insight", "Analysis", "Action"},
			Footer:      "McKinsey-style structured report",
		}
	}
}

func containsAnyKeyword(text string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func collectPPTAgendaItems(sections []pptSection, limit int) []string {
	items := []string{}
	for idx, sec := range sections {
		title := strings.TrimSpace(sec.title)
		if title == "" {
			continue
		}
		items = append(items, fmt.Sprintf("%d. %s", idx+1, truncatePPTText(title, 42)))
		if len(items) >= limit {
			break
		}
	}
	return items
}

func collectPPTSummaryItems(sections []pptSection, limit int) []string {
	items := []string{}
	for _, sec := range sections {
		if len(sec.items) > 0 {
			items = append(items, sec.items[0])
		}
		for _, item := range sec.items[1:] {
			if len(items) >= limit {
				break
			}
			if len([]rune(item)) > 18 {
				items = append(items, item)
			}
		}
		if len(items) >= limit {
			break
		}
	}
	items = uniquePPTItems(items, limit)
	if len(items) == 0 {
		return []string{"Summarize the available material into a concise decision-ready view."}
	}
	return items
}

func extractPPTMetrics(items []string) []map[string]interface{} {
	re := regexp.MustCompile(`([0-9]+(?:\\.[0-9]+)?(?:%|x|X|bp|bps|k|K|m|M|b|B)?)`)
	metrics := []map[string]interface{}{}
	for _, item := range items {
		match := re.FindStringSubmatch(item)
		if len(match) < 2 {
			continue
		}
		value := match[1]
		label := strings.TrimSpace(strings.Replace(item, value, "", 1))
		label = strings.Trim(label, ":-,.; ")
		if label == "" {
			label = "Key metric"
		}
		metrics = append(metrics, map[string]interface{}{
			"value": truncatePPTText(value, 18),
			"label": truncatePPTText(label, 26),
			"sub":   "Source text extracted",
		})
		if len(metrics) >= 4 {
			break
		}
	}
	return metrics
}

func buildPPTSectionBlocks(items []string) []map[string]interface{} {
	metrics := extractPPTMetrics(items)
	switch {
	case len(metrics) >= 2 && len(items) >= 3:
		return []map[string]interface{}{
			{"type": "kpi", "span": "full", "metrics": metrics},
			{"type": "bullets", "span": "full", "items": items},
		}
	case len(items) >= 4:
		left, right := splitPPTItems(items)
		return []map[string]interface{}{
			{"type": "bullets", "span": "left", "items": left},
			{"type": "bullets", "span": "right", "items": right},
		}
	default:
		return []map[string]interface{}{
			{"type": "text", "span": "full", "heading": "Key points", "body": strings.Join(items, "\n")},
		}
	}
}

func splitPPTItems(items []string) ([]string, []string) {
	if len(items) <= 1 {
		return items, []string{"Add one more supporting point or evidence source."}
	}
	mid := (len(items) + 1) / 2
	left := items[:mid]
	right := items[mid:]
	if len(right) == 0 {
		right = []string{"Add one more supporting point or evidence source."}
	}
	return left, right
}

func derivePPTActionItems(sections []pptSection) []string {
	items := []string{}
	for _, sec := range sections {
		if sec.title == "" {
			continue
		}
		items = append(items, "Validate "+truncatePPTText(sec.title, 40)+" with fresh evidence.")
		if len(items) >= 3 {
			break
		}
	}
	if len(items) == 0 {
		items = []string{
			"Validate the core assumptions with up-to-date evidence.",
			"Turn the key findings into owners, timing and milestones.",
			"Track the main risks and define response triggers.",
		}
	}
	return items
}

func joinedPPTInsight(items []string) string {
	if len(items) == 0 {
		return "Summarize the available material into a concise decision-ready view."
	}
	if len(items) == 1 {
		return truncatePPTText(items[0], 88)
	}
	return truncatePPTText(items[0]+"; "+items[1], 88)
}

func cleanPPTLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`")
	line = strings.ReplaceAll(line, "**", "")
	line = strings.ReplaceAll(line, "__", "")
	line = strings.ReplaceAll(line, "\t", " ")
	return strings.Join(strings.Fields(line), " ")
}

func splitPPTSentence(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= 110 {
		return []string{text}
	}
	separators := []string{". ", "; ", ": "}
	parts := []string{text}
	for _, sep := range separators {
		if strings.Contains(text, sep) {
			parts = strings.Split(text, sep)
			break
		}
	}
	return parts
}

func truncatePPTText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes-3])) + "..."
}

func uniquePPTItems(items []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range items {
		item = truncatePPTText(strings.TrimSpace(item), 96)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// toCSV extracts tables from markdown and saves as CSV.
func (t *ConvertTool) toCSV(markdown, outPath string) (string, error) {
	var rows [][]string
	lines := strings.Split(markdown, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") {
			// Skip separator lines like |---|---|
			if strings.Contains(line, "---") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			row := make([]string, 0, len(cells))
			for _, cell := range cells {
				row = append(row, strings.TrimSpace(cell))
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no markdown table found to convert to CSV/XLSX")
	}
	if strings.EqualFold(filepath.Ext(outPath), ".xlsx") {
		if _, err := spreadsheet.WriteXLSXWorkbook(context.Background(), outPath, rows, true); err != nil {
			return "", err
		}
		return t.formatResult(outPath), nil
	}
	var csvContent strings.Builder
	writer := csv.NewWriter(&csvContent)
	writer.WriteAll(rows)
	if err := writer.Error(); err != nil {
		return "", err
	}
	return t.writeFile(outPath, csvContent.String())
}

func (t *ConvertTool) writeFile(path, content string) (string, error) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return t.formatResult(path), nil
}

func (t *ConvertTool) formatResult(path string) string {
	info, _ := os.Stat(path)
	size := ""
	if info != nil {
		size = fmt.Sprintf(" (%.1f KB)", float64(info.Size())/1024)
	}
	return fmt.Sprintf("✅ Converted: %s%s", path, size)
}
