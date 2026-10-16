package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	middlewarecore "mac/internal/middleware"
)

type swebenchCase struct {
	InstanceID       string   `json:"instance_id"`
	Repo             string   `json:"repo"`
	BaseCommit       string   `json:"base_commit"`
	Version          string   `json:"version"`
	Difficulty       string   `json:"difficulty"`
	ProblemStatement string   `json:"problem_statement"`
	FailToPass       []string `json:"fail_to_pass"`
}

type swebenchPrediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

type swebenchRunMetadata struct {
	InstanceID          string        `json:"instance_id"`
	Repository          string        `json:"repository"`
	BaseCommit          string        `json:"base_commit"`
	Model               string        `json:"model"`
	Duration            time.Duration `json:"duration"`
	PatchBytes          int           `json:"patch_bytes"`
	ReadOnlyPathChanges []string      `json:"read_only_path_changes,omitempty"`
	FinalResponse       string        `json:"final_response,omitempty"`
}

// TestSWEbenchMiniMax runs a real OpenKitty coding-agent turn and emits a
// prediction file accepted by the official SWE-bench harness. It is gated so
// normal unit tests never contact a model API or mutate an external checkout.
//
// Required environment:
//
//	OPENKITTY_SWEBENCH=1
//	OPENKITTY_SWEBENCH_WORKSPACE=<clean checkout for the selected instance>
//	OPENKITTY_SWEBENCH_PREDICTIONS=<output JSON path>
//	MINIMAX_API_KEY=<process-only secret>
//
// Optional environment:
//
//	OPENKITTY_SWEBENCH_INSTANCE=pallets__flask-5014
//	OPENKITTY_SWEBENCH_MODEL=MiniMax-M3
//	OPENKITTY_SWEBENCH_BASE_URL=https://api.minimaxi.com/v1
//	OPENKITTY_SWEBENCH_TIMEOUT_MINUTES=15
func TestSWEbenchMiniMax(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_SWEBENCH")) != "1" {
		t.Skip("set OPENKITTY_SWEBENCH=1 to run the external MiniMax/SWE-bench integration test")
	}

	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	if apiKey == "" {
		t.Fatal("MINIMAX_API_KEY is required and must be supplied through the process environment")
	}
	workspace := strings.TrimSpace(os.Getenv("OPENKITTY_SWEBENCH_WORKSPACE"))
	if workspace == "" {
		t.Fatal("OPENKITTY_SWEBENCH_WORKSPACE is required")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if info, statErr := os.Stat(filepath.Join(workspace, ".git")); statErr != nil || !info.IsDir() {
		t.Fatalf("workspace must be a git checkout: %s", workspace)
	}
	requireCleanTrackedWorktree(t, workspace)

	instanceID := envOrDefault("OPENKITTY_SWEBENCH_INSTANCE", "pallets__flask-5014")
	testCase := loadSWEbenchCase(t, instanceID)
	predictionsPath := strings.TrimSpace(os.Getenv("OPENKITTY_SWEBENCH_PREDICTIONS"))
	if predictionsPath == "" {
		t.Fatal("OPENKITTY_SWEBENCH_PREDICTIONS is required")
	}
	predictionsPath, err = filepath.Abs(predictionsPath)
	if err != nil {
		t.Fatalf("resolve predictions path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(predictionsPath), 0o755); err != nil {
		t.Fatalf("create predictions directory: %v", err)
	}

	modelName := envOrDefault("OPENKITTY_SWEBENCH_MODEL", "MiniMax-M3")
	baseURL := envOrDefault("OPENKITTY_SWEBENCH_BASE_URL", "https://api.minimaxi.com/v1")
	modelConfig := ModelConfig{
		Type:         "minimax",
		APIKey:       apiKey,
		ModelName:    modelName,
		BaseURL:      baseURL,
		Temperature:  0.1,
		MaxTokens:    16384,
		ThinkingMode: "disabled",
	}
	globalConfig := &Config{
		Workspace:                  workspace,
		ModelConfig:                map[string]ModelConfig{"swebench-minimax": modelConfig},
		PermissionMode:             "bypassPermissions",
		DangerouslySkipPermissions: true,
	}
	globalConfig.MemoryConfig.Enabled = false
	globalConfig.SkillsConfig.DisEnabled = true
	globalConfig.ToolsConfig.Enabled = true

	memoryDisabled := false
	readOnlyPaths := []string{"tests/**", "**/tests/**"}
	agentConfig := AgentConfig{
		Name:          "SWE-bench",
		Type:          AgentTypeCustom,
		Model:         "swebench-minimax",
		SystemMessage: swebenchCodingSystemMessage,
		Temperature:   0.1,
		MaxTokens:     16384,
		MaxIterations: 60,
		HasMemory:     &memoryDisabled,
		ReadOnlyPaths: readOnlyPaths,
		EnabledTools: []string{
			"read_file",
			"list_dir",
			"grep",
			"glob",
			"project_map",
			"edit_file",
			"multiedit",
			"write_file",
			"json_patch",
			"cli",
			"git",
			"code_check",
			"test_runner",
			"goal_verify",
			"tool_detail",
			"ptc_execute",
		},
	}

	agent := NewLLMAgent(agentConfig, &modelConfig, nil, globalConfig, log.New(io.Discard, "", 0))
	t.Cleanup(func() {
		if agent.ptcServer != nil {
			agent.ptcServer.Stop()
		}
		if agent.lspManager != nil {
			agent.lspManager.Close()
		}
	})

	timeout := time.Duration(envIntOrDefault(t, "OPENKITTY_SWEBENCH_TIMEOUT_MINUTES", 15)) * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	prompt := formatSWEbenchPrompt(testCase)
	started := time.Now()
	finalResponse, generateErr := agent.Generate(ctx, prompt)
	duration := time.Since(started)

	changedPaths := gitChangedPaths(t, workspace)
	var productionPaths []string
	var readOnlyPathChanges []string
	for _, changedPath := range changedPaths {
		if matchesAnyGuardPath(changedPath, readOnlyPaths) {
			readOnlyPathChanges = append(readOnlyPathChanges, changedPath)
		} else {
			productionPaths = append(productionPaths, changedPath)
		}
	}
	patch := gitDiffForPaths(t, workspace, productionPaths)
	predictions := []swebenchPrediction{{
		InstanceID:      testCase.InstanceID,
		ModelNameOrPath: "openkitty/" + modelName,
		ModelPatch:      patch,
	}}
	writeJSONFile(t, predictionsPath, predictions)

	metadata := swebenchRunMetadata{
		InstanceID:          testCase.InstanceID,
		Repository:          testCase.Repo,
		BaseCommit:          testCase.BaseCommit,
		Model:               modelName,
		Duration:            duration,
		PatchBytes:          len(patch),
		ReadOnlyPathChanges: readOnlyPathChanges,
		FinalResponse:       finalResponse,
	}
	writeJSONFile(t, strings.TrimSuffix(predictionsPath, filepath.Ext(predictionsPath))+".meta.json", metadata)

	t.Logf("instance=%s model=%s duration=%s patch_bytes=%d predictions=%s",
		testCase.InstanceID, modelName, duration.Round(time.Millisecond), len(patch), predictionsPath)
	if generateErr != nil {
		t.Fatalf("OpenKitty agent failed after prediction capture: %v", generateErr)
	}
	if len(readOnlyPathChanges) > 0 {
		t.Fatalf("agent bypassed the read-only path guard; excluded from prediction: %s",
			strings.Join(readOnlyPathChanges, ", "))
	}
	if strings.TrimSpace(patch) == "" {
		t.Fatal("OpenKitty agent completed without producing a git patch")
	}
}

const swebenchCodingSystemMessage = `You are OpenKitty's coding agent under SWE-bench evaluation.

Work directly in the current repository and solve the supplied issue completely.
- Inspect the relevant implementation and tests before editing.
- Diagnose the root cause and make the smallest maintainable production-code change.
- Tests are immutable reference material. Never add, edit, delete, rename, or regenerate a test file; the host enforces this as a read-only path.
- Do not modify generated files, lock files, or dependencies merely to make the task pass.
- Preserve public behavior outside the requested change.
- Run the listed FAIL_TO_PASS test first and keep verification focused. Do not run a broad suite unless the focused test passes and the production change plausibly affects a wider area.
- Pre-existing failures outside FAIL_TO_PASS are non-blocking evidence, not a reason to mark the requested task incomplete.
- If local test dependencies are unavailable, still inspect the test and implementation and make the best evidence-based fix.
- Leave all edits in the working tree. Do not commit, stash, reset, restore, or discard changes.
- Do not use the network or ask the user for information.
- Finish with a concise summary of changed files and verification performed.`

func formatSWEbenchPrompt(testCase swebenchCase) string {
	failTests := "(not supplied)"
	if len(testCase.FailToPass) > 0 {
		failTests = strings.Join(testCase.FailToPass, "\n- ")
	}
	return fmt.Sprintf(`Solve this SWE-bench Verified instance.

Instance: %s
Repository: %s
Base commit: %s
Version: %s
Difficulty label: %s

Issue:
%s

Tests expected to change from failing to passing:
- %s

Implement and verify the fix in the current workspace now.`, testCase.InstanceID, testCase.Repo,
		testCase.BaseCommit, testCase.Version, testCase.Difficulty,
		strings.TrimSpace(testCase.ProblemStatement), failTests)
}

func loadSWEbenchCase(t *testing.T, instanceID string) swebenchCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "swebench", "cases.json"))
	if err != nil {
		t.Fatalf("read SWE-bench cases: %v", err)
	}
	var cases []swebenchCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decode SWE-bench cases: %v", err)
	}
	for _, testCase := range cases {
		if testCase.InstanceID == instanceID {
			return testCase
		}
	}
	t.Fatalf("SWE-bench instance %q is not present in testdata/swebench/cases.json", instanceID)
	return swebenchCase{}
}

func requireCleanTrackedWorktree(t *testing.T, workspace string) {
	t.Helper()
	status := gitOutput(t, workspace, "status", "--porcelain", "--untracked-files=no")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("SWE-bench workspace has tracked changes; use a clean checkout:\n%s", status)
	}
}

func gitOutput(t *testing.T, workspace string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", workspace}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func gitChangedPaths(t *testing.T, workspace string) []string {
	t.Helper()
	output := gitOutput(t, workspace, "diff", "--name-only", "-z", "HEAD", "--")
	rawPaths := strings.Split(output, "\x00")
	paths := make([]string, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		if rawPath = strings.TrimSpace(rawPath); rawPath != "" {
			paths = append(paths, filepath.ToSlash(rawPath))
		}
	}
	return paths
}

func gitDiffForPaths(t *testing.T, workspace string, paths []string) string {
	t.Helper()
	if len(paths) == 0 {
		return ""
	}
	args := []string{"diff", "--binary", "--no-ext-diff", "HEAD", "--"}
	args = append(args, paths...)
	return gitOutput(t, workspace, args...)
}

func matchesAnyGuardPath(relative string, patterns []string) bool {
	for _, pattern := range patterns {
		if middlewarecore.GuardPathMatches(relative, pattern) {
			return true
		}
	}
	return false
}

func writeJSONFile(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", name, value)
	}
	return parsed
}
