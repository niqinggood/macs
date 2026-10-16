package testingtools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TestRunnerTool adapts the reusable development test runner to OpenKitty's
// baseline receipts and native execution environments.
type TestRunnerTool struct {
	workspace           string
	timeout             time.Duration
	maxRetry            int
	baselines           *testBaselineStore
	environmentEnabled  func() bool
	activateEnvironment func(id, dir string) (map[string]string, TestEnvironmentMetadata, func(), error)
	describe            func(command, dir string) TestVerificationDescriptor
}

type TestVerificationDescriptor struct {
	Family string
	Root   string
	Target string
}

type TestRunnerOptions struct {
	EnvironmentEnabled   func() bool
	ActivateEnvironment  func(id, dir string) (map[string]string, TestEnvironmentMetadata, func(), error)
	DescribeVerification func(command, dir string) TestVerificationDescriptor
}

type TestEnvironmentMetadata struct {
	ID             string
	Ecosystem      string
	RuntimeVersion string
}

func NewTestRunnerTool(workspace string, options ...TestRunnerOptions) *TestRunnerTool {
	tool := &TestRunnerTool{
		workspace: workspace,
		timeout:   120 * time.Second,
		maxRetry:  3,
		baselines: newTestBaselineStore(),
	}
	if len(options) > 0 {
		tool.environmentEnabled = options[0].EnvironmentEnabled
		tool.activateEnvironment = options[0].ActivateEnvironment
		tool.describe = options[0].DescribeVerification
	}
	return tool
}

func (t *TestRunnerTool) Name() string { return "run_tests" }

func (t *TestRunnerTool) Description() string {
	return "Run focused tests after code changes or diagnose failing test output. " +
		"Supports: go test, pytest, npm test, cargo test. Start with the smallest relevant package/test, then broaden. " +
		"Pass changed_files to safely narrow broad commands to affected tests; Go selection includes reverse dependencies. " +
		"When isolation or dependency reuse is needed, request native_env with tool_detail; after it prepares an environment, pass its environment_id here. " +
		"For a dirty or already-failing repository, run the same scope once with baseline=true before editing, then rerun normally to separate pre-existing failures from new regressions. " +
		"Returns pass/fail status, failed test names, likely file locations, error categories, and suggested fixes. " +
		"Use this in a fix-retry loop: run tests -> analyze failures -> fix code -> run tests again (max 3 iterations)."
}

func (t *TestRunnerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Test command to run. Examples: 'go test ./...', 'pytest -x', 'npm test', 'cargo test'",
			},
			"dir": map[string]interface{}{
				"type":        "string",
				"description": "Working directory for the test command. Default: workspace root.",
			},
			"test_name": map[string]interface{}{
				"type":        "string",
				"description": "Optional: run only a specific test. Example: 'TestMyFunction' or 'test_my_module.py::test_case'",
			},
			"changed_files": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Optional files changed by this task. Broad commands are narrowed only when a safe affected-test mapping is available.",
			},
			"baseline": map[string]interface{}{
				"type":        "boolean",
				"description": "Capture this exact test scope before editing. A later normal run reports pre-existing versus newly introduced failures.",
			},
			"environment_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional completed environment_id returned by native_env. The environment is attached only for this test process.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *TestRunnerTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	command := testString(params, "command", "")
	dir := testString(params, "dir", t.workspace)
	testName := testString(params, "test_name", "")
	changedFiles := testStringSlice(params["changed_files"])
	captureBaseline := testBool(params, "baseline", false)
	environmentID := strings.TrimSpace(testString(params, "environment_id", ""))

	if command == "" {
		return "Error: command is required", nil
	}

	fullCmd := command
	if testName != "" {
		switch {
		case strings.Contains(command, "go test"):
			fullCmd = command + " -run " + testName
		case strings.Contains(command, "pytest"):
			fullCmd = command + " -k " + testName
		case strings.Contains(command, "cargo test"):
			fullCmd = command + " " + testName
		}
	}
	requestedCmd := fullCmd
	selectionNote := ""
	if len(changedFiles) > 0 {
		fullCmd, selectionNote = selectAffectedTestCommand(dir, fullCmd, changedFiles)
	}

	var environment map[string]string
	var environmentNote string
	releaseEnvironment := func() {}
	if environmentID != "" {
		if t.environmentEnabled == nil || !t.environmentEnabled() || t.activateEnvironment == nil {
			return "Error: environment_id was supplied, but native execution environments are disabled", nil
		}
		var metadata TestEnvironmentMetadata
		var activationErr error
		environment, metadata, releaseEnvironment, activationErr = t.activateEnvironment(environmentID, dir)
		if activationErr != nil {
			return fmt.Sprintf("Error: cannot attach native environment %q: %v", environmentID, activationErr), nil
		}
		environmentNote = fmt.Sprintf(
			"### Native Environment\n- id: `%s`\n- ecosystem: `%s`\n- runtime: `%s`\n- cache: reused\n",
			metadata.ID, metadata.Ecosystem, metadata.RuntimeVersion)
	}
	defer releaseEnvironment()

	started := time.Now()
	output, runErr := RunCommandWithContext(ctx, dir, fullCmd, t.timeout, environment)
	duration := time.Since(started)
	result := t.formatTestRunResultWithEnvironment(
		dir, requestedCmd, fullCmd, selectionNote, output, runErr, duration, captureBaseline, environmentID)
	if environmentNote != "" {
		result = environmentNote + "\n" + result
	}
	return result, nil
}

func testString(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return fallback
}

func testBool(values map[string]interface{}, key string, fallback bool) bool {
	if value, ok := values[key].(bool); ok {
		return value
	}
	return fallback
}

func testStringSlice(value interface{}) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
