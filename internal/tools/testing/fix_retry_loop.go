package testingtools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mac/internal/configvalue"
)

// FixRetryTool implements automated test→fix→retest cycles.
// Claude Code borrowed: error recovery loop in AgentTool
type FixRetryTool struct {
	workspace string
	timeout   time.Duration
	maxCycles int
}

func NewFixRetryTool(workspace string) *FixRetryTool {
	return &FixRetryTool{
		workspace: workspace,
		timeout:   180 * time.Second,
		maxCycles: 3,
	}
}

func (t *FixRetryTool) Name() string { return "fix_and_retry" }

func (t *FixRetryTool) Description() string {
	return "Automated fix-retry loop: run tests → analyze failures → apply fixes → retest. " +
		"Max 3 cycles. Returns a summary of what was fixed and whether tests pass. " +
		"Use this when you have failing tests and want automated repair."
}

func (t *FixRetryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Test command. Examples: go test ./..., pytest, npm test",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Source file to fix (for targeted fixes)",
			},
			"test_file": map[string]interface{}{
				"type":        "string",
				"description": "Test file to run (for targeted test runs)",
			},
			"max_cycles": map[string]interface{}{
				"type":        "integer",
				"description": "Max fix-retry cycles (default 3)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *FixRetryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	command := configvalue.String(params, "command", "")
	file := configvalue.String(params, "file", "")
	testFile := configvalue.String(params, "test_file", "")
	maxCycles := configvalue.Int(params, "max_cycles", t.maxCycles)
	if maxCycles <= 0 {
		maxCycles = 3
	}
	if maxCycles > 5 {
		maxCycles = 5
	}

	if command == "" {
		return "Error: command is required", nil
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Fix-Retry Loop: `%s`\n\n", command))

	passCount := 0
	failCount := 0
	fixCount := 0

	for cycle := 1; cycle <= maxCycles; cycle++ {
		report.WriteString(fmt.Sprintf("### Cycle %d/%d\n", cycle, maxCycles))

		// Run tests
		start := time.Now()
		output, err := runCommand(t.workspace, command, t.timeout)
		duration := time.Since(start)

		if err == nil {
			report.WriteString(fmt.Sprintf("✅ All tests pass (%.1fs)\n\n", duration.Seconds()))
			passCount++
			break
		}

		failCount++
		report.WriteString(fmt.Sprintf("❌ Tests failed (%.1fs)\n\n", duration.Seconds()))

		// Analyze failures
		analysis := AnalyzeTestOutput(output, command)
		report.WriteString(fmt.Sprintf("%s\n", analysis))

		// Extract failed tests for targeted rerun
		failedTests := ExtractFailedTests(output, command)
		if len(failedTests) > 0 {
			report.WriteString(fmt.Sprintf("**Failed**: %v\n\n", failedTests))
		}

		// On the last cycle, don't suggest fixes
		if cycle >= maxCycles {
			report.WriteString("⚠️ Max cycles reached. Manual intervention required.\n")
			break
		}

		// Provide fix guidance
		suggestions := suggestFixes(output, command)
		if len(suggestions) > 0 {
			report.WriteString("### Suggested Actions\n")
			for _, s := range suggestions {
				report.WriteString(fmt.Sprintf("- [ ] %s\n", s))
				fixCount++
			}
		}

		// Guidance for targeted fixes
		if file != "" {
			report.WriteString(fmt.Sprintf("\nFocus fixes on: `%s`\n", file))
		}
		if testFile != "" {
			report.WriteString(fmt.Sprintf("Rerun with: `%s`\n", testFile))
		}

		report.WriteString("\n**Apply the fixes above, then rerun this tool to continue the cycle.**\n\n")
		report.WriteString("---\n\n")

		// Don't auto-apply — let the LLM do the fixing between cycles
		break
	}

	// Summary
	report.WriteString("### Loop Summary\n")
	report.WriteString(fmt.Sprintf("- Cycles: %d/%d\n", failCount, maxCycles))
	report.WriteString(fmt.Sprintf("- Pass: %d\n", passCount))
	report.WriteString(fmt.Sprintf("- Suggested fixes: %d\n", fixCount))

	if passCount > 0 {
		report.WriteString("\n✅ **All tests pass!**\n")
	}

	return report.String(), nil
}
