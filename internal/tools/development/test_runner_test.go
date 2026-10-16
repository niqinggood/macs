package development

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestExtractFailedTests(t *testing.T) {
	goOutput := `--- FAIL: TestFoo (0.01s)
--- FAIL: TestBar (0.02s)
--- PASS: TestBaz (0.01s)`
	tests := ExtractFailedTests(goOutput, "go test")
	if len(tests) != 2 {
		t.Fatalf("expected 2 failed tests, got %d", len(tests))
	}
	if tests[0] != "TestFoo" || tests[1] != "TestBar" {
		t.Errorf("got %v, want [TestFoo TestBar]", tests)
	}

	pytestOutput := `FAILED test_module.py::test_foo - AssertionError
FAILED test_module.py::test_bar - ValueError`
	tests = ExtractFailedTests(pytestOutput, "pytest")
	if len(tests) != 2 {
		t.Fatalf("expected 2 pytest failures, got %d", len(tests))
	}
}

func TestExtractErrorMessages(t *testing.T) {
	output := "Error: something went wrong\n  at line 10\n\npanic: nil pointer dereference\n  at main.go:42\n"
	errors := extractErrorMessages(output)
	if len(errors) == 0 {
		t.Error("should extract error messages")
	}
}

func TestSuggestFixes(t *testing.T) {
	output := "undefined: NewThing\ncannot use x as int\nno required module provides package example.com/missing\nWARNING: DATA RACE\n"
	suggestions := strings.Join(suggestFixes(output, "go test"), "\n")
	for _, want := range []string{"undefined symbol", "Type mismatch", "Missing package/module", "Concurrency race"} {
		if !strings.Contains(suggestions, want) {
			t.Fatalf("expected suggestion containing %q, got %s", want, suggestions)
		}
	}
}

func TestExtractErrorLocations(t *testing.T) {
	output := `# example
./handler.go:12:34: undefined: NewThing
src/app.ts(10,5): error TS2304: Cannot find name 'x'.
Traceback (most recent call last):
  File "scripts/run.py", line 42, in main
tests/test_app.py:8: AssertionError`
	locations := extractErrorLocations(output)
	joined := strings.Join(locations, "\n")
	for _, want := range []string{"./handler.go:12:34", "src/app.ts(10,5)", "scripts/run.py:42", "tests/test_app.py:8"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected location %q in %v", want, locations)
		}
	}
}

func TestSuggestedReadRanges(t *testing.T) {
	reads := suggestedReadRanges([]string{"./handler.go:12:34", "src/app.ts(50,5)"})
	joined := strings.Join(reads, "\n")
	for _, want := range []string{
		`read_file path="./handler.go" lines_range="1-32"`,
		`read_file path="src/app.ts" lines_range="30-70"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected suggested read %q, got %v", want, reads)
		}
	}
}

func TestShellCommandUsesPlatformShell(t *testing.T) {
	cmd := ShellCommand(context.Background(), "echo ok")
	if runtime.GOOS == "windows" {
		if cmd.Path == "sh" || cmd.Args[0] == "sh" {
			t.Fatalf("windows run_tests should not require sh: %#v", cmd.Args)
		}
		if cmd.Args[0] != "powershell" {
			t.Fatalf("windows run_tests should use powershell, got %#v", cmd.Args)
		}
		return
	}
	if cmd.Args[0] != "sh" || len(cmd.Args) < 3 || cmd.Args[1] != "-c" {
		t.Fatalf("posix run_tests should use sh -c, got %#v", cmd.Args)
	}
}

func TestAnalyzeTestOutput(t *testing.T) {
	output := "--- FAIL: TestExample (0.01s)\nexample_test.go:12: expected 5, got 3\nError: expected 5, got 3\npanic: assertion failed\n"
	result := AnalyzeTestOutput(output, "go test")
	if result == "" {
		t.Error("analyzeTestOutput returned empty")
	}
	if !strContains(result, "TestExample") {
		t.Error("should mention failed test name")
	}
	if !strContains(result, "Failed Tests") {
		t.Error("should have Failed Tests section")
	}
	if !strContains(result, "Likely Locations") || !strContains(result, "example_test.go:12") {
		t.Error("should include likely file locations")
	}
	if !strContains(result, "Suggested Reads") || !strContains(result, "read_file path=\"example_test.go\"") {
		t.Error("should include suggested read ranges")
	}
	if !strContains(result, "Suggested Rerun") || !strContains(result, "go test -run '^TestExample$'") {
		t.Error("should include focused rerun command")
	}
	if !strContains(result, "Failure Type") || !strContains(result, "assertion") || !strContains(result, "runtime/panic") {
		t.Error("should classify failure type")
	}
	if !strContains(result, "Failure Packet") || !strContains(result, "primary_failed_test: TestExample") {
		t.Error("should include structured failure packet")
	}
	if !strContains(result, "Recovery Plan") || !strContains(result, "inspect -> edit -> focused rerun -> broaden") {
		t.Error("should include a recovery plan")
	}
}

func TestRecoveryPlanForPytestOutput(t *testing.T) {
	output := `FAILED tests/test_app.py::test_case - AssertionError
tests/test_app.py:42: AssertionError
E   AssertionError: expected ok`
	result := AnalyzeTestOutput(output, "pytest")
	for _, want := range []string{
		"command_family: pytest",
		"primary_failed_test: FAILED tests/test_app.py::test_case - AssertionError",
		`read_file path="tests/test_app.py" lines_range="22-62"`,
		"pytest tests/test_app.py::test_case -q",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected pytest recovery output to contain %q, got:\n%s", want, result)
		}
	}
}

func TestRecoveryPlanForJSTestOutput(t *testing.T) {
	output := `FAIL src/app.test.ts
  App
    renders the dashboard

src/app.test.ts:27:13
Error: expected "Dashboard" to be visible`
	result := AnalyzeTestOutput(output, "npx vitest run")
	for _, want := range []string{
		"command_family: javascript",
		"src/app.test.ts:27:13",
		`read_file path="src/app.test.ts" lines_range="7-47"`,
		"npx vitest run",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected JS recovery output to contain %q, got:\n%s", want, result)
		}
	}
}

func TestSuggestedRerunCommandsPytest(t *testing.T) {
	reruns := suggestedRerunCommands("pytest", []string{"FAILED tests/test_app.py::test_case - AssertionError"})
	if len(reruns) != 1 || reruns[0] != "pytest tests/test_app.py::test_case -q" {
		t.Fatalf("unexpected pytest rerun commands: %v", reruns)
	}
}

func TestSuggestedRerunCommandsJSTest(t *testing.T) {
	reruns := suggestedRerunCommands("npm test", []string{"FAIL src/app.test.ts", "● app › renders"})
	if len(reruns) != 1 || reruns[0] != `npm test -- -t "app › renders"` {
		t.Fatalf("unexpected npm test rerun commands: %v", reruns)
	}

	reruns = suggestedRerunCommands("npx vitest run", []string{"● app renders"})
	if len(reruns) != 1 || reruns[0] != `npx vitest run -t "app renders"` {
		t.Fatalf("unexpected vitest rerun commands: %v", reruns)
	}
}

func TestClassifyTestFailure(t *testing.T) {
	categories := strings.Join(classifyTestFailure("undefined: Thing\nno required module provides package x\ndeadline exceeded"), "\n")
	for _, want := range []string{"compile/build", "environment/dependency", "timeout"} {
		if !strings.Contains(categories, want) {
			t.Fatalf("expected category %q, got %s", want, categories)
		}
	}
}

func TestCompactTestOutputFiltersNoise(t *testing.T) {
	output := strings.Join([]string{
		"2026/07/01 01:45:18 [NLP] English word list loaded",
		"2026/07/01 01:45:28 [PLUGIN] Registered: plugin-1",
		"--- FAIL: TestExample (0.01s)",
		"example_test.go:12: expected 5, got 3",
		"Error: expected 5, got 3",
		"2026/07/01 01:45:28 [DEBUG] noisy internals",
	}, "\n")
	result := CompactTestOutput(output, 2000)
	for _, want := range []string{"filtered", "TestExample", "example_test.go:12", "expected 5"} {
		if !strings.Contains(result, want) {
			t.Fatalf("compact output should contain %q, got %q", want, result)
		}
	}
	for _, noise := range []string{"English word list loaded", "Registered: plugin-1", "noisy internals"} {
		if strings.Contains(result, noise) {
			t.Fatalf("compact output should filter %q, got %q", noise, result)
		}
	}
}

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
