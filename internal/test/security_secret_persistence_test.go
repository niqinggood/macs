package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	middlewarecore "mac/internal/middleware"
	"mac/internal/toolcontract"
)

const syntheticPersistenceSecret = "sk-openkitty-persistence-1234567890abcdef"

func TestToolResultNormalizerPreservesAccountInfoInPersistedOutput(t *testing.T) {
	workspace := t.TempDir()
	normalizer := toolcontract.NewToolResultNormalizerWithConfig(workspace, toolcontract.ToolResultNormalizerConfig{MaxInlineChars: 40})
	content := strings.Repeat("payload ", 20) + syntheticPersistenceSecret
	preview := normalizer.Normalize("example", content)
	assertNoForcedRedaction(t, preview)
	assertSecretPresentInFiles(t, filepath.Join(workspace, ".openkitty", "runtime", "tool-results"))
}

func TestToolOutputBudgetPreservesAccountInfoInOverflowFile(t *testing.T) {
	root := t.TempDir()
	middleware := middlewarecore.NewToolOutputBudgetMiddleware(middlewarecore.ToolOutputBudgetConfig{
		Enabled:       true,
		MaxChars:      30,
		PreviewHead:   10,
		PreviewTail:   10,
		MaxResultsDir: root,
	})
	result := ToolResult{Content: strings.Repeat("x", 80) + syntheticPersistenceSecret}
	ctx := &AgentContext{SessionID: "session", ToolCall: ToolCall{Name: "example"}, ToolResult: &result}
	if err := middleware.AfterTool(ctx); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}
	assertNoForcedRedaction(t, ctx.ToolResult.Content)
	assertSecretPresentInFiles(t, root)
}

func TestOffloadToolResultPreservesAccountInfo(t *testing.T) {
	agent := &LLMAgent{}
	got := agent.offloadToolResult("example", "tool returned "+syntheticPersistenceSecret)
	assertSecretPresent(t, got)
}

func TestToolCallSummaryPreservesAccountInfo(t *testing.T) {
	got := formatToolCall("cli", map[string]interface{}{
		"command": "invoke --api-key " + syntheticPersistenceSecret,
	}, true)
	assertSecretPresent(t, got)
}

func assertSecretPresent(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, syntheticPersistenceSecret) {
		t.Fatalf("workflow content lost the synthetic account value: %q", text)
	}
	assertNoForcedRedaction(t, text)
}

func assertNoForcedRedaction(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, "[REDACTED]") || strings.Contains(text, "[SCRUBBED]") {
		t.Fatalf("workflow content was forcibly redacted: %q", text)
	}
}

func assertSecretPresentInFiles(t *testing.T, root string) {
	t.Helper()
	found := false
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), syntheticPersistenceSecret) {
			found = true
		}
		if strings.Contains(string(data), "[REDACTED]") || strings.Contains(string(data), "[SCRUBBED]") {
			t.Fatalf("persisted workflow file %s contains a forced-redaction marker", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk persisted outputs: %v", err)
	}
	if !found {
		t.Fatalf("no persisted workflow file under %s retained the synthetic account value", root)
	}
}
