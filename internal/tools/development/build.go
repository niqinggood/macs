package development

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mac/internal/codeutil"
	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// BuildTool runs project builds and turns compiler output into a concise report.
type BuildTool struct {
	workspace string
	timeout   time.Duration
}

func NewBuildTool(workspace string) *BuildTool {
	return &BuildTool{workspace: workspace, timeout: 120 * time.Second}
}

func (tool *BuildTool) Name() string { return "build" }

func (tool *BuildTool) Description() string {
	return "Run project build and analyze errors. " +
		"Auto-detects build system: Go (go build), Node (npm run build), Python (check syntax), Rust (cargo build). " +
		"Returns structured error analysis with file locations and fix suggestions."
}

func (tool *BuildTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target": map[string]interface{}{
				"type":        "string",
				"description": "Build target. Default: auto-detect. Examples: ./cmd/server, ./..., all",
			},
		},
	}
}

func (tool *BuildTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target := configvalue.String(params, "target", "")
	language := codeutil.DetectProjectLanguage(tool.workspace)
	if language == "" {
		return "No recognized project. Supported: Go, Node, Python, Rust", nil
	}
	command, arguments := tool.buildCommand(language, target)
	if command == "" {
		return "Build system not detected for this project type", nil
	}

	started := time.Now()
	output, buildErr := runBuildCommand(ctx, tool.workspace, command, arguments, tool.timeout)
	duration := time.Since(started)
	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Build: `%s %s`\n\n", command, strings.Join(arguments, " ")))
	if buildErr == nil {
		report.WriteString(fmt.Sprintf("✅ Build successful (%.1fs)\n\n", duration.Seconds()))
		if output != "" {
			report.WriteString("```\n" + textutil.Truncate(output, 500) + "\n```\n")
		}
		return report.String(), nil
	}

	report.WriteString(fmt.Sprintf("❌ Build failed (%.1fs)\n\n", duration.Seconds()))
	errors := ParseBuildErrors(output, language)
	if len(errors) > 0 {
		report.WriteString(fmt.Sprintf("### Errors (%d)\n", len(errors)))
		for index, buildError := range errors {
			if index >= 10 {
				report.WriteString(fmt.Sprintf("\n... (%d more errors)\n", len(errors)-10))
				break
			}
			location := ""
			if buildError.File != "" {
				location = fmt.Sprintf("%s:%d:%d", buildError.File, buildError.Line, buildError.Col)
			}
			report.WriteString(fmt.Sprintf("- **%s**: %s\n", location, buildError.Message))
		}
		report.WriteString("\n")
	}
	suggestions := SuggestBuildFixes(output, language)
	if len(suggestions) > 0 {
		report.WriteString("### Suggested Fixes\n")
		for _, suggestion := range suggestions {
			report.WriteString(fmt.Sprintf("- %s\n", suggestion))
		}
		report.WriteString("\n")
	}
	report.WriteString("### Build Output\n```\n" + textutil.Truncate(output, 2000) + "\n```\n")
	return report.String(), nil
}

type BuildError struct {
	File    string
	Line    int
	Col     int
	Message string
}

func (tool *BuildTool) buildCommand(language, target string) (string, []string) {
	switch language {
	case "Go":
		arguments := []string{"build"}
		if target != "" && target != "all" {
			arguments = append(arguments, target)
		} else {
			arguments = append(arguments, "./...")
		}
		return "go", arguments
	case "Node":
		return "npm", []string{"run", "build"}
	case "Python":
		return "python", []string{"-m", "py_compile", "."}
	case "Rust":
		return "cargo", []string{"build"}
	default:
		return "", nil
	}
}

func ParseBuildErrors(output, language string) []BuildError {
	var errors []BuildError
	lines := strings.Split(output, "\n")
	switch language {
	case "Go":
		for _, line := range lines {
			index := strings.Index(line, ".go:")
			if index < 0 {
				continue
			}
			rest := line[index+4:]
			parts := strings.SplitN(rest, ":", 3)
			buildError := BuildError{File: line[:index+3]}
			if len(parts) >= 1 {
				buildError.Line = parseBuildPosition(parts[0])
			}
			if len(parts) >= 2 {
				buildError.Col = parseBuildPosition(parts[1])
			}
			if len(parts) >= 3 {
				buildError.Message = strings.TrimSpace(parts[2])
			}
			errors = append(errors, buildError)
		}
	case "Node":
		for _, line := range lines {
			if strings.Contains(line, "error") || strings.Contains(line, "Error") {
				errors = append(errors, BuildError{Message: strings.TrimSpace(line)})
			}
		}
	case "Rust":
		for _, line := range lines {
			if strings.Contains(line, "error[") || strings.Contains(line, "error:") {
				errors = append(errors, BuildError{Message: strings.TrimSpace(line)})
			}
		}
	}
	return errors
}

func SuggestBuildFixes(output, language string) []string {
	var suggestions []string
	lower := strings.ToLower(output)
	switch language {
	case "Go":
		if strings.Contains(lower, "undefined:") {
			suggestions = append(suggestions, "Undefined symbol — check imports or function name spelling")
		}
		if strings.Contains(lower, "cannot use") {
			suggestions = append(suggestions, "Type mismatch — check variable types, use lsp_hover for type info")
		}
		if strings.Contains(lower, "imported and not used") {
			suggestions = append(suggestions, "Unused import — remove or use the imported package")
		}
		if strings.Contains(lower, "declared and not used") {
			suggestions = append(suggestions, "Unused variable — remove or use it")
		}
		if strings.Contains(lower, "missing go.sum") {
			suggestions = append(suggestions, "Run `go mod tidy` to fix dependencies")
		}
	case "Node":
		if strings.Contains(lower, "cannot find module") {
			suggestions = append(suggestions, "Missing module — run `npm install`")
		}
		if strings.Contains(lower, "typescript") || strings.Contains(lower, "ts") {
			suggestions = append(suggestions, "TypeScript error — check types, run `npx tsc --noEmit` for full check")
		}
	case "Rust":
		if strings.Contains(lower, "cannot find") {
			suggestions = append(suggestions, "Missing dependency — check Cargo.toml")
		}
		if strings.Contains(lower, "mismatched types") {
			suggestions = append(suggestions, "Type mismatch — check expected vs actual types")
		}
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "Review the build output above for the root cause")
	}
	return suggestions
}

func RunBuildCommand(directory, name string, arguments []string, timeout time.Duration) (string, error) {
	return runBuildCommand(context.Background(), directory, name, arguments, timeout)
}

func runBuildCommand(ctx context.Context, directory, name string, arguments []string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	return string(output), err
}

func parseBuildPosition(value string) int {
	position, _ := strconv.Atoi(strings.TrimSpace(value))
	return position
}
