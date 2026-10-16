package memory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSessionMemoryProgressIsIsolatedAndFailureRetryable(t *testing.T) {
	config := DefaultSessionMemoryConfig()
	config.MinimumTokensToInit = 1
	config.MinimumTokensBetweenUpdate = 1
	config.ToolCallsBetweenUpdates = 1
	fail := true
	root := NewSessionMemoryExtractor(t.TempDir(), config, SessionExtractorDependencies{
		EstimateMessages: func([]map[string]interface{}) int { return 10 },
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			if fail {
				return "", errors.New("model unavailable")
			}
			return "# supported session summary", nil
		},
	})
	one, two := root.ForSession("one/two"), root.ForSession("one-two")
	if one == two || one.MemoryPath() == two.MemoryPath() || one != root.ForSession("one/two") {
		t.Fatal("session state/path collided")
	}
	if !one.ShouldExtract(10, 1, true) {
		t.Fatal("initial extraction not reserved")
	}
	if err := one.Extract(nil, "one"); err == nil {
		t.Fatal("failed model reported success")
	}
	if one.lastTokens != 0 || !one.lastExtracted.IsZero() || !one.ShouldExtract(10, 1, true) {
		t.Fatal("failed extraction advanced success watermark or blocked retry")
	}
	fail = false
	if err := one.Extract(nil, "one"); err != nil {
		t.Fatal(err)
	}
	if one.ShouldExtract(10, 1, true) || !two.ShouldExtract(10, 1, true) {
		t.Fatal("successful progress leaked to another session")
	}
	if data, err := os.ReadFile(one.MemoryPath()); err != nil || !strings.Contains(string(data), "supported session summary") {
		t.Fatalf("summary not persisted: %s, %v", data, err)
	}
}

func TestSessionMemoryCancellationAndDisablePreventCommit(t *testing.T) {
	enabled := true
	extractor := NewSessionMemoryExtractor(t.TempDir(), DefaultSessionMemoryConfig(), SessionExtractorDependencies{
		Enabled: func() bool { return enabled },
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			enabled = false
			return "late summary", nil
		},
	})
	if err := extractor.Extract(nil, "test"); err == nil {
		t.Fatal("late result committed after disabling memory")
	}
	if _, err := os.Stat(extractor.MemoryPath()); !os.IsNotExist(err) {
		t.Fatalf("disabled result created a file: %v", err)
	}
	enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	extractor.dependencies.CallLLM = func(callCtx context.Context, _ []map[string]interface{}, _ int) (string, error) {
		cancel()
		if !errors.Is(callCtx.Err(), context.Canceled) {
			t.Error("model call did not inherit cancellation")
		}
		return "cancelled summary", nil
	}
	if err := extractor.ExtractContext(ctx, nil, "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled extraction = %v", err)
	}
	if _, err := os.Stat(extractor.MemoryPath()); !os.IsNotExist(err) {
		t.Fatalf("cancelled result created a file: %v", err)
	}
}
