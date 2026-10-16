package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mac/internal/nativeenv"

	"gopkg.in/yaml.v3"
)

func TestRunTestsCanAttachNativeEnvironment(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/runner\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := nativeenv.NewNativeEnvironmentManager(project, nativeenv.NativeExecutionConfig{
		Enabled: true, CacheDir: t.TempDir(), MaxCacheMB: 64, TTLHours: 24, PrepareTimeoutSeconds: 30,
	})
	metadata, _, output, err := manager.Prepare(context.Background(), nativeenv.NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeEnvTestSetupCommand("runner"),
		Environment:  map[string]string{"OPENKITTY_TEST_VALUE": "from-cache"},
	})
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	command := `test "$OPENKITTY_TEST_VALUE" = "from-cache"`
	if runtime.GOOS == "windows" {
		command = `if ($env:OPENKITTY_TEST_VALUE -ne 'from-cache') { exit 1 }`
	}
	tool := NewTestRunnerTool(project, manager)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": command, "environment_id": metadata.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Native Environment", metadata.ID, "All Tests Passed"} {
		if !strings.Contains(result, want) {
			t.Fatalf("test result should contain %q:\n%s", want, result)
		}
	}
}

func TestTestBaselineDoesNotCrossNativeEnvironments(t *testing.T) {
	tool := NewTestRunnerTool(t.TempDir())
	command := "pytest tests/test_example.py"
	failure := "FAILED tests/test_example.py::test_case - AssertionError"
	_ = tool.formatTestRunResultWithEnvironment(
		tool.workspace, command, command, "", failure, os.ErrInvalid, time.Second, true, "python-00000000000000000000")
	result := tool.formatTestRunResultWithEnvironment(
		tool.workspace, command, command, "", failure, os.ErrInvalid, time.Second, false, "python-11111111111111111111")
	receipt, ok := parseTestRunReceipt(result)
	if !ok {
		t.Fatalf("missing test receipt:\n%s", result)
	}
	if receipt.BaselineMatched {
		t.Fatalf("baseline from another native environment must not match: %#v", receipt)
	}
}

func TestNativeExecutionConfigYAML(t *testing.T) {
	var config Config
	data := []byte("runtime:\n  native_execution:\n    enabled: true\n    cache_dir: D:\\native-cache\n    max_cache_mb: 2048\n    ttl_hours: 72\n    prepare_timeout_seconds: 600\n")
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	got := nativeenv.NormalizeExecutionConfig(config.Runtime.NativeExecution)
	if !got.Enabled || got.CacheDir != `D:\native-cache` || got.MaxCacheMB != 2048 ||
		got.TTLHours != 72 || got.PrepareTimeoutSeconds != 600 {
		t.Fatalf("unexpected native execution config: %#v", got)
	}
}

func nativeEnvTestSetupCommand(content string) string {
	if runtime.GOOS == "windows" {
		return "$target = Join-Path $env:OPENKITTY_NATIVE_ENV_DIR 'marker.txt'; " +
			"Set-Content -LiteralPath $target -Value '" + content + "' -NoNewline"
	}
	return "printf '%s' '" + content + "' > \"$OPENKITTY_NATIVE_ENV_DIR/marker.txt\""
}
