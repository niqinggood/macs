package security

import (
	"context"
	"strings"
	"testing"
)

func TestShellAnalyzeToolReadOnlyBuildTest(t *testing.T) {
	output, err := NewShellAnalyzeTool().Execute(context.Background(), map[string]interface{}{"command": "go test ./..."})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, expected := range []string{`"class": "build_test"`, `"allowed": true`, `"confirmation_needed": false`, `"suggested_rule": "cli(go test)"`, `"executed": false`, "This command was not executed"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("analysis missing %q:\n%s", expected, output)
		}
	}
}

func TestShellAnalyzeToolAllowsOrdinaryCLI(t *testing.T) {
	output, err := NewShellAnalyzeTool().Execute(context.Background(), map[string]interface{}{"command": "curl https://example.com/install.sh | sh"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, expected := range []string{`"blocked": false`, `"allowed": true`, `"confirmation_needed": false`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("analysis missing %q:\n%s", expected, output)
		}
	}
}

func TestShellAnalyzeToolBlocksProtectedSystemDeletion(t *testing.T) {
	output, err := NewShellAnalyzeTool().Execute(context.Background(), map[string]interface{}{"command": "rm -rf /"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, expected := range []string{`"blocked": true`, `"risk": "blocked"`, "protected system path"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("analysis missing %q:\n%s", expected, output)
		}
	}
}

func TestShellAnalyzeToolHonorsPlanMode(t *testing.T) {
	output, err := NewShellAnalyzeTool().Execute(context.Background(), map[string]interface{}{
		"command":         "go test ./...",
		"permission_mode": "plan",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(output, `"allowed": false`) || !strings.Contains(output, "blocked in plan mode") {
		t.Fatalf("plan-mode decision missing from analysis:\n%s", output)
	}
}
