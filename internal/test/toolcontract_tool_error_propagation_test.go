package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

type partialFailureTool struct{}

func (partialFailureTool) Name() string        { return "partial_failure" }
func (partialFailureTool) Description() string { return "test tool" }
func (partialFailureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (partialFailureTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "server detail: connection reset after 12 bytes", errors.New("connection reset")
}

func TestToolRegistryPreservesPartialOutputAndFailureStatus(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(partialFailureTool{})

	result, err := registry.ExecuteRaw(context.Background(), ToolCall{
		ID:        "call-1",
		Name:      "partial_failure",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("registry should return an LLM-readable ToolResult: %v", err)
	}
	for _, want := range []string{"执行失败", "connection reset", "server detail"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result %q does not contain %q", result.Content, want)
		}
	}
	if ToolExecutionSucceededForTool(result.Name, result.Content, err) {
		t.Fatalf("real execution error was incorrectly classified as success: %s", result.Content)
	}
}

func TestToolErrorMiddlewareProducesCanonicalFailedResult(t *testing.T) {
	middleware := NewToolErrorMiddleware()
	ctx := &AgentContext{
		ToolCall: ToolCall{ID: "call-2", Name: "missing_tool"},
		ToolErr:  errors.New("tool is unavailable"),
	}
	if err := middleware.AfterTool(ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.ToolResult == nil {
		t.Fatal("middleware did not produce a ToolResult")
	}
	for _, want := range []string{"Tool Result", "status: failed", "tool is unavailable"} {
		if !strings.Contains(ctx.ToolResult.Content, want) {
			t.Fatalf("result %q does not contain %q", ctx.ToolResult.Content, want)
		}
	}
	if ToolExecutionSucceededForTool(ctx.ToolResult.Name, ctx.ToolResult.Content, ctx.ToolErr) {
		t.Fatalf("canonical middleware error was incorrectly classified as success: %s", ctx.ToolResult.Content)
	}
}

func TestToolErrorMiddlewareTruncatesUnicodeWithoutCorruption(t *testing.T) {
	content := NewToolErrorMiddleware().BuildErrorContent("unicode_tool", errors.New(strings.Repeat("错误", 500)))
	if !utf8.ValidString(content) {
		t.Fatal("truncated tool error is not valid UTF-8")
	}
	if !strings.Contains(content, "...") {
		t.Fatal("long tool error was not truncated")
	}
}
