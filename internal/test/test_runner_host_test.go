package app

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	developmenttools "mac/internal/tools/development"
)

func TestRunTestsTimeoutReportsDeadline(t *testing.T) {
	tool := NewTestRunnerTool(".")
	tool.timeout = 50 * time.Millisecond

	command := "sleep 0.2"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 200"
	}
	out, err := tool.Execute(context.Background(), map[string]interface{}{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tests Failed", "timeout", "context deadline exceeded"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Fatalf("expected timeout output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestCoreCodingToolDescriptions(t *testing.T) {
	tools := map[string]string{
		"read_file":  NewReadFileTool(".").Description(),
		"write_file": NewWriteFileTool(".").Description(),
		"edit_file":  NewEditFileTool(".").Description(),
		"grep":       NewGrepTool(".").Description(),
		"glob":       NewGlobTool(".").Description(),
		"code_check": developmenttools.NewCodeCheckTool(".").Description(),
		"run_tests":  NewTestRunnerTool(".").Description(),
	}
	checks := map[string]string{
		"read_file":  "before editing",
		"write_file": "Prefer edit_file",
		"edit_file":  "Read the file first",
		"grep":       "before broad reading",
		"glob":       "before grep/read",
		"code_check": "smallest meaningful check",
		"run_tests":  "likely file locations",
	}
	for name, want := range checks {
		if !strings.Contains(tools[name], want) {
			t.Fatalf("%s description should contain %q, got %q", name, want, tools[name])
		}
	}
}
