package app

import (
	"fmt"
	"testing"

	"mac/internal/configvalue"
	middlewarecore "mac/internal/middleware"
)

func TestTokenBudget_BelowThreshold(t *testing.T) {
	cfg := TokenBudgetConfig{
		Enabled:       true,
		MaxTokens:     10000,
		WarnThreshold: 0.8,
	}
	m := NewTokenBudgetMiddleware(cfg)
	if m == nil {
		t.Fatal("expected non-nil middleware")
	}

	// Small input, should not trigger warning
	warning := m.CountBeforeLLM("s1", []map[string]interface{}{
		{"role": "user", "content": "hello"},
	})
	if warning != "" {
		t.Errorf("unexpected warning below threshold: %s", warning)
	}

	in, out, total := m.Usage("s1")
	if total == 0 {
		t.Error("expected some token usage")
	}
	_ = in
	_ = out
}

func TestTokenBudget_WarnThreshold(t *testing.T) {
	cfg := TokenBudgetConfig{
		Enabled:       true,
		MaxTokens:     50,
		WarnThreshold: 0.1, // trigger at 5 tokens
	}
	m := NewTokenBudgetMiddleware(cfg)

	// This should trigger a warning
	warning := m.CountBeforeLLM("s1", []map[string]interface{}{
		{"role": "user", "content": "this is a longer message that should use more tokens"},
	})
	if warning == "" {
		t.Error("expected warning above threshold")
	}
	if !m.IsWarned("s1") {
		t.Error("session should be marked as warned")
	}

	// Second call should not produce another warning
	warning2 := m.CountBeforeLLM("s1", []map[string]interface{}{
		{"role": "user", "content": "another message"},
	})
	if warning2 != "" {
		t.Errorf("second call should not warn again: %s", warning2)
	}
}

func TestTokenBudget_HardStop(t *testing.T) {
	cfg := TokenBudgetConfig{
		Enabled:           true,
		MaxTokens:         10,
		HardStopThreshold: 0.1, // trigger at 1 token
	}
	m := NewTokenBudgetMiddleware(cfg)

	warning := m.CountBeforeLLM("s1", []map[string]interface{}{
		{"role": "user", "content": "test message"},
	})
	if warning == "" {
		t.Error("expected hard stop warning")
	}
	if !m.IsHardStopped("s1") {
		t.Error("session should be hard-stopped")
	}
	if !m.ShouldStripToolCalls("s1") {
		t.Error("ShouldStripToolCalls should return true")
	}
}

func TestTokenBudget_Disabled(t *testing.T) {
	cfg := DefaultTokenBudgetConfig() // Enabled: false
	m := NewTokenBudgetMiddleware(cfg)
	if m != nil {
		t.Error("disabled config should return nil middleware")
	}
}

func TestLoopDetection_HashBased(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)
	if m == nil {
		t.Fatal("expected non-nil middleware")
	}

	// Same call twice — should trigger warn
	ctx1 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "1", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(200)},
	}}
	_ = m.BeforeTool(ctx1)

	ctx2 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "2", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(200)},
	}}
	err := m.BeforeTool(ctx2)
	if err != nil {
		t.Errorf("second call should not hard-stop yet: %v", err)
	}

	// Third call — should hard-stop
	ctx3 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "3", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(200)},
	}}
	err = m.BeforeTool(ctx3)
	if err == nil {
		t.Error("third identical call should be blocked")
	}
}

func TestLoopDetectionDistinguishesUseSkillNames(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	for i := 0; i < 2; i++ {
		err := m.BeforeTool(&AgentContext{SessionID: "skill-session", ToolCall: ToolCall{
			ID:        fmt.Sprintf("art-%d", i),
			Name:      "use_skill",
			Arguments: map[string]interface{}{"skill_name": "ppt-art-director"},
		}})
		if err != nil {
			t.Fatalf("art-director activation %d unexpectedly blocked: %v", i+1, err)
		}
	}
	err := m.BeforeTool(&AgentContext{SessionID: "skill-session", ToolCall: ToolCall{
		ID:        "master",
		Name:      "use_skill",
		Arguments: map[string]interface{}{"skill_name": "ppt-master"},
	}})
	if err != nil {
		t.Fatalf("a different Skill name must not share the duplicate signature: %v", err)
	}
}

func TestLoopDetection_ReadFilePagination(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 4
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	// Reading same file in different 200-line buckets should NOT be treated as same call
	ctx1 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "1", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(200)},
	}}
	_ = m.BeforeTool(ctx1)

	ctx2 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "2", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(201), "end_line": float64(400)},
	}}
	_ = m.BeforeTool(ctx2)

	ctx3 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "3", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(401), "end_line": float64(600)},
	}}
	err := m.BeforeTool(ctx3)
	if err != nil {
		t.Errorf("pagination across different buckets should NOT be blocked: %v", err)
	}
}

func TestLoopDetection_ReadFileLinesRangePagination(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	calls := []map[string]interface{}{
		{"path": ".openkitty/storage/build_deck.py"},
		{"path": ".openkitty/storage/build_deck.py", "lines_range": "63-115"},
		{"path": ".openkitty/storage/build_deck.py", "lines_range": "115-317"},
	}
	for i, args := range calls {
		err := m.BeforeTool(&AgentContext{SessionID: "range-session", ToolCall: ToolCall{
			ID:        fmt.Sprintf("range-%d", i),
			Name:      "read_file",
			Arguments: args,
		}})
		if err != nil {
			t.Fatalf("distinct lines_range pagination call %d was falsely blocked: %v", i+1, err)
		}
	}
}

func TestLoopDetection_ReadFileSameBucket(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	// Reading same bucket range should be detected
	ctx1 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "1", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(100)},
	}}
	_ = m.BeforeTool(ctx1)

	ctx2 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "2", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(50), "end_line": float64(150)},
	}}
	_ = m.BeforeTool(ctx2) // same bucket 0 — should be caught by hash

	ctx3 := &AgentContext{SessionID: "s1", ToolCall: ToolCall{
		ID: "3", Name: "read_file",
		Arguments: map[string]interface{}{"path": "/tmp/a.txt", "start_line": float64(0), "end_line": float64(100)},
	}}
	err := m.BeforeTool(ctx3) // this one would hit hash bucket 0 again
	if err != nil {
		// Expected — hard stop on 3rd call to same bucket
		t.Logf("correctly blocked on same-bucket read: %v", err)
	}
}

func TestLoopDetectionPlanProgressUsesStepAndStatusSignature(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	progress := []map[string]interface{}{
		{"step_id": "step_1", "status": "in_progress"},
		{"step_id": "step_1", "status": "completed"},
		{"step_id": "step_2", "status": "in_progress"},
		{"step_id": "step_2", "status": "completed"},
	}
	for index, arguments := range progress {
		err := m.BeforeTool(&AgentContext{
			SessionID: "normal-plan-progress",
			ToolCall: ToolCall{
				ID:        fmt.Sprintf("progress-%d", index),
				Name:      "update_plan_step",
				Arguments: arguments,
			},
		})
		if err != nil {
			t.Fatalf("normal plan progress call %d was blocked: %v", index+1, err)
		}
	}

	for index := 0; index < 3; index++ {
		err := m.BeforeTool(&AgentContext{
			SessionID: "repeated-plan-update",
			ToolCall: ToolCall{
				ID:   fmt.Sprintf("repeat-%d", index),
				Name: "update_plan_step",
				Arguments: map[string]interface{}{
					"step_id": "step_1",
					"status":  "completed",
				},
			},
		})
		if index < 2 && err != nil {
			t.Fatalf("repeat %d blocked before hard limit: %v", index+1, err)
		}
		if index == 2 && err == nil {
			t.Fatal("third identical plan update should be blocked")
		}
	}
}

func TestLoopDetectionBrowserLoginActionsUseDistinctSignatures(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	actions := []map[string]interface{}{
		{"action": "extract", "max_text_chars": 3000},
		{"action": "input", "index": 2, "text": "admin"},
		{"action": "input", "index": 4, "text": "secret"},
		{"action": "click", "index": 6},
		{"action": "extract", "max_text_chars": 5000},
	}
	for index, arguments := range actions {
		err := m.BeforeTool(&AgentContext{
			SessionID: "browser-login",
			ToolCall: ToolCall{
				ID:        fmt.Sprintf("browser-action-%d", index),
				Name:      "web_page",
				Arguments: arguments,
			},
		})
		if err != nil {
			t.Fatalf("normal browser login action %d was blocked: %v", index+1, err)
		}
	}
}

func TestLoopDetectionBrowserStillBlocksExactRepeatedAction(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	for index := 0; index < 3; index++ {
		err := m.BeforeTool(&AgentContext{
			SessionID: "browser-repeated-click",
			ToolCall: ToolCall{
				ID:   fmt.Sprintf("browser-click-%d", index),
				Name: "web_page",
				Arguments: map[string]interface{}{
					"action": "click",
					"index":  6,
				},
			},
		})
		if index < 2 && err != nil {
			t.Fatalf("repeat %d blocked before hard limit: %v", index+1, err)
		}
		if index == 2 && err == nil {
			t.Fatal("third identical browser action should be blocked")
		}
	}
}

func TestLoopDetectionBrowserTaskObjectiveAffectsSignature(t *testing.T) {
	cfg := middlewarecore.DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	m := middlewarecore.NewLoopDetectionMiddleware(cfg)

	objectives := []string{
		"capture the login page",
		"inspect the login form",
		"capture the signed-in navigation",
	}
	for index, objective := range objectives {
		err := m.BeforeTool(&AgentContext{
			SessionID: "browser-task-objectives",
			ToolCall: ToolCall{
				ID:   fmt.Sprintf("browser-task-%d", index),
				Name: "browser_task",
				Arguments: map[string]interface{}{
					"url":       "https://example.com/login",
					"objective": objective,
				},
			},
		})
		if err != nil {
			t.Fatalf("browser task objective %d was treated as a duplicate: %v", index+1, err)
		}
	}
}

func TestGuardrailAllowlist_AllowsEnabled(t *testing.T) {
	p := NewAllowlistProvider([]string{"read_file", "grep"}, DefaultGuardrailConfig())
	verdict := p.Check(GuardrailCheck{ToolName: "read_file"})
	if !verdict.Allow {
		t.Errorf("read_file should be allowed: %s", verdict.Reason)
	}
}

func TestGuardrailAllowlist_BlocksDisabled(t *testing.T) {
	p := NewAllowlistProvider([]string{"read_file"}, DefaultGuardrailConfig())
	verdict := p.Check(GuardrailCheck{ToolName: "cli"})
	if verdict.Allow {
		t.Error("cli should be blocked (not in allowlist)")
	}
	if verdict.Code != "tool_disabled" {
		t.Errorf("expected 'tool_disabled' code, got: %s", verdict.Code)
	}
}

func TestGuardrailAllowlist_WarnsHighRisk(t *testing.T) {
	cfg := DefaultGuardrailConfig()
	cfg.Enabled = true
	p := NewAllowlistProvider([]string{"cli", "bash", "rm"}, cfg)
	verdict := p.Check(GuardrailCheck{ToolName: "rm"})
	if !verdict.Allow {
		t.Errorf("high-risk tools should still be allowed with warning: %s", verdict.Reason)
	}
	// rm should trigger ClassifyToolAction → RiskHigh
	if verdict.Warning {
		t.Logf("correctly warned for high-risk tool: %s", verdict.Reason)
	}
}

func TestGuardrailMiddleware_Blocks(t *testing.T) {
	cfg := DefaultGuardrailConfig()
	cfg.Enabled = true
	p := NewAllowlistProvider([]string{"read_file"}, cfg)
	mw := NewGuardrailProviderMiddleware(p)
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}

	ctx := &AgentContext{
		ToolCall: ToolCall{Name: "bash", Arguments: map[string]interface{}{}},
	}
	err := mw.BeforeTool(ctx)
	if err == nil {
		t.Error("expected blocked error for disabled tool")
	}
}

func TestConfigHotReloadBoundary(t *testing.T) {
	old := &Config{
		Gateway: GatewayConfig{ListenAddr: ":8080"},
	}
	new := &Config{
		Gateway: GatewayConfig{ListenAddr: ":9090"},
	}
	warnings := configvalue.DetectStartupFieldChanges(old, new)
	if len(warnings) == 0 {
		t.Error("gateway change should be detected as startup-only")
	}

	// Same config should produce no warnings
	sameWarnings := configvalue.DetectStartupFieldChanges(old, old)
	if len(sameWarnings) > 0 {
		t.Errorf("identical configs should have no warnings: %v", sameWarnings)
	}
}

func TestConfigHotReload_NoChangeIgnores(t *testing.T) {
	cfg := &Config{
		Runtime: RuntimeConfig{SemaphoreLimit: 10},
	}
	warnings := configvalue.DetectStartupFieldChanges(cfg, cfg)
	if len(warnings) > 0 {
		t.Errorf("no changes should produce no warnings: %v", warnings)
	}
}
