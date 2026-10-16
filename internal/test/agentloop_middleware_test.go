package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestMiddlewareChain_Order(t *testing.T) {
	var order []string

	m1 := newTestMiddleware("first")
	m1.before = func(ctx *AgentContext) error {
		order = append(order, "first:before")
		return nil
	}
	m2 := newTestMiddleware("second")
	m2.before = func(ctx *AgentContext) error {
		order = append(order, "second:before")
		return nil
	}

	chain := NewMiddlewareChain(m1, m2)
	ctx := &AgentContext{ToolCall: ToolCall{ID: "1", Name: "test"}}
	_, _ = chain.WrapToolExecution(ctx, func() (ToolResult, error) {
		order = append(order, "execute")
		return ToolResult{ToolCallID: "1", Name: "test", Content: "ok"}, nil
	})

	if len(order) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(order), order)
	}
	if order[0] != "first:before" {
		t.Errorf("first middleware should run first: %v", order)
	}
	if order[1] != "second:before" {
		t.Errorf("second middleware should run second: %v", order)
	}
	if order[2] != "execute" {
		t.Errorf("tool should run after middleware: %v", order)
	}
}

func TestMiddlewareChain_BlockOnError(t *testing.T) {
	m1 := newTestMiddleware("blocker")
	m1.before = func(ctx *AgentContext) error {
		return fmt.Errorf("blocked by policy")
	}
	m2 := newTestMiddleware("should_not_run")
	m2.before = func(ctx *AgentContext) error {
		t.Error("this middleware should not run after blocker")
		return nil
	}

	chain := NewMiddlewareChain(m1, m2)
	ctx := &AgentContext{ToolCall: ToolCall{ID: "1", Name: "test"}}
	result, err := chain.WrapToolExecution(ctx, func() (ToolResult, error) {
		t.Error("tool should not execute after blocker")
		return ToolResult{}, nil
	})

	if err != nil {
		t.Errorf("WrapToolExecution should not return error (error is in result): %v", err)
	}
	if result.Content == "" || result.Content == "ok" {
		t.Errorf("expected blocked message in result, got: %s", result.Content)
	}
}

func TestToolErrorMiddleware_NormalizesError(t *testing.T) {
	m := NewToolErrorMiddleware()
	ctx := &AgentContext{
		ToolCall: ToolCall{ID: "1", Name: "bash"},
		ToolResult: &ToolResult{
			ToolCallID: "1",
			Name:       "bash",
			Content:    "raw error",
		},
		ToolErr: fmt.Errorf("command not found: foobar"),
	}

	if err := m.AfterTool(ctx); err != nil {
		t.Fatal(err)
	}

	if ctx.ToolErr != nil {
		t.Errorf("ToolErr should be cleared, got: %v", ctx.ToolErr)
	}
	if ctx.ToolResult.Content == "raw error" {
		t.Errorf("content should be replaced with normalized error, got: %s", ctx.ToolResult.Content)
	}
}

func TestToolErrorMiddleware_PassesThroughSuccess(t *testing.T) {
	m := NewToolErrorMiddleware()
	ctx := &AgentContext{
		ToolCall: ToolCall{ID: "1", Name: "read_file"},
		ToolResult: &ToolResult{
			ToolCallID: "1",
			Name:       "read_file",
			Content:    "file contents here",
		},
		ToolErr: nil,
	}

	if err := m.AfterTool(ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.ToolResult.Content != "file contents here" {
		t.Errorf("success result should not be modified: %s", ctx.ToolResult.Content)
	}
}

func TestResponseFilterMiddleware_StripsToolCalls(t *testing.T) {
	m := NewResponseFilterMiddleware()
	ctx := &AgentContext{
		Messages: []map[string]interface{}{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "Let me check.<tool_call>\n{\"name\": \"read_file\"}\n</tool_call>\n\nThe file says..."},
		},
	}

	if err := m.AfterModel(ctx); err != nil {
		t.Fatal(err)
	}

	assistant := ctx.Messages[1]["content"].(string)
	if strings.Contains(assistant, "<tool_call>") {
		t.Errorf("tool_call tags should be stripped: %s", assistant)
	}
	if !strings.Contains(assistant, "The file says") {
		t.Errorf("real content should be preserved: %s", assistant)
	}
}

func TestResponseFilterMiddleware_StripsThinking(t *testing.T) {
	m := NewResponseFilterMiddleware()
	ctx := &AgentContext{
		Messages: []map[string]interface{}{
			{"role": "assistant", "content": "<think>I need to analyze this carefully...</think>\n\nThe answer is 42."},
		},
	}

	if err := m.AfterModel(ctx); err != nil {
		t.Fatal(err)
	}

	result := ctx.Messages[0]["content"].(string)
	if strings.Contains(result, "<think>") {
		t.Errorf("thinking blocks should be stripped: %s", result)
	}
	if !strings.Contains(result, "The answer is 42") {
		t.Errorf("real answer should be preserved: %s", result)
	}
}

func TestResponseFilter_NonAssistantIgnored(t *testing.T) {
	m := NewResponseFilterMiddleware()
	ctx := &AgentContext{
		Messages: []map[string]interface{}{
			{"role": "user", "content": "what is <tool_call>test</tool_call>?"},
		},
	}

	if err := m.AfterModel(ctx); err != nil {
		t.Fatal(err)
	}
	result := ctx.Messages[0]["content"].(string)
	if !strings.Contains(result, "<tool_call>") {
		t.Errorf("user messages should not be filtered: %s", result)
	}
}

func TestBuildDefaultChain(t *testing.T) {
	cfg := &Config{}
	chain := BuildDefaultChain(cfg)
	names := chain.List()
	if len(names) < 2 {
		t.Errorf("expected at least 2 middleware, got %d: %v", len(names), names)
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["tool_error"] {
		t.Error("tool_error middleware missing")
	}
	if !found["response_filter"] {
		t.Error("response_filter middleware missing")
	}
}

func TestAgentContext_Metadata(t *testing.T) {
	ctx := &AgentContext{}
	_, ok := ctx.GetMeta("nonexistent")
	if ok {
		t.Error("missing key should return ok=false")
	}
	ctx.SetMeta("count", 5)
	v, ok := ctx.GetMeta("count")
	if !ok || v.(int) != 5 {
		t.Errorf("expected 5, got %v", v)
	}
	ctx.SetMeta("count", 10)
	v, _ = ctx.GetMeta("count")
	if v.(int) != 10 {
		t.Errorf("expected 10 after overwrite, got %v", v)
	}
}

func TestNewMiddlewareChain_Deduplicate(t *testing.T) {
	m1 := newTestMiddleware("a")
	m2 := newTestMiddleware("a")
	chain := NewMiddlewareChain(m1, m2)
	if len(chain.List()) != 1 {
		t.Errorf("duplicate names should be deduplicated, got: %v", chain.List())
	}
}

func TestNewMiddlewareChain_FiltersNils(t *testing.T) {
	m1 := newTestMiddleware("real")
	chain := NewMiddlewareChain(nil, m1, nil)
	if len(chain.List()) != 1 {
		t.Errorf("nils should be filtered, got: %v", chain.List())
	}
}

func TestNewMiddlewareChain_FiltersTypedNilMiddleware(t *testing.T) {
	var denialTracker *DenialTrackerMiddleware
	m1 := newTestMiddleware("real")
	chain := NewMiddlewareChain(denialTracker, m1)
	names := chain.List()
	if len(names) != 1 || names[0] != "real" {
		t.Fatalf("typed nil middleware should be filtered, got: %v", names)
	}
}

// --- helpers ---

func newTestMiddleware(name string) *testMiddleware {
	return &testMiddleware{BaseMiddleware: NewBaseMiddleware(name)}
}

type testMiddleware struct {
	BaseMiddleware
	before func(ctx *AgentContext) error
	after  func(ctx *AgentContext) error
}

func (t *testMiddleware) BeforeTool(ctx *AgentContext) error {
	if t.before != nil {
		return t.before(ctx)
	}
	return nil
}

func (t *testMiddleware) AfterTool(ctx *AgentContext) error {
	if t.after != nil {
		return t.after(ctx)
	}
	return nil
}
