package middleware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/projectctx"
	"mac/internal/toolcontract"
)

func TestToolOutputBudgetKeepsBoundedUseSkillInstructionsInline(t *testing.T) {
	middleware := NewToolOutputBudgetMiddleware(ToolOutputBudgetConfig{
		Enabled:       true,
		MaxChars:      100,
		PreviewHead:   40,
		PreviewTail:   40,
		MaxResultsDir: filepath.Join(".openkitty", "storage", "tool_results"),
	})
	full := strings.Repeat("skill instruction\n", 1000)
	if len([]rune(full)) > toolcontract.DefaultToolResultNormalizerConfig().SkillMaxInlineChars {
		t.Fatal("test fixture exceeds the bounded inline Skill budget")
	}
	result := toolcontract.Result{Content: full}
	ctx := &Context{
		Context:    projectctx.WithResolvedWorkspace(context.Background(), t.TempDir()),
		SessionID:  "session",
		ToolCall:   toolcontract.Call{Name: "use_skill"},
		ToolResult: &result,
	}
	if err := middleware.AfterTool(ctx); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}
	if result.Content != full {
		t.Fatal("bounded use_skill instructions must remain inline")
	}
}

func TestToolOutputBudgetWritesInsideResolvedTenantWorkspace(t *testing.T) {
	workspace := t.TempDir()
	configured := filepath.Join(".openkitty", "storage", "tool_results")
	middleware := NewToolOutputBudgetMiddleware(ToolOutputBudgetConfig{
		Enabled:       true,
		MaxChars:      100,
		PreviewHead:   40,
		PreviewTail:   40,
		MaxResultsDir: configured,
	})
	result := toolcontract.Result{Content: strings.Repeat("tenant output ", 100)}
	ctx := &Context{
		Context:    projectctx.WithResolvedWorkspace(context.Background(), workspace),
		SessionID:  "tenant-session",
		ToolCall:   toolcontract.Call{Name: "cli"},
		ToolResult: &result,
	}
	if err := middleware.AfterTool(ctx); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}

	wantFile := filepath.Join(workspace, configured, "tenant-session", "cli_1.txt")
	if _, err := os.Stat(wantFile); err != nil {
		t.Fatalf("tenant overflow result was not written inside its workspace: %v", err)
	}
	wantDisplay := filepath.ToSlash(filepath.Join(configured, "tenant-session", "cli_1.txt"))
	if !strings.Contains(result.Content, wantDisplay) {
		t.Fatalf("preview path is not workspace-relative: %s", result.Content)
	}
	if strings.Contains(result.Content, filepath.ToSlash(workspace)) || strings.Contains(result.Content, workspace) {
		t.Fatalf("preview leaked the physical tenant workspace: %s", result.Content)
	}
}
