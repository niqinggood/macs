package app

import (
	"io"
	"log"
	"strings"
	"testing"
)

func TestAIEmployeeMatchAcceptsNameAliasWithoutPartialTaskLeak(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "lead-researcher",
		Name:     "\u6f5c\u5ba2\u7814\u7a76\u5458",
		AtName:   "\u6f5c\u5ba2\u7814\u7a76",
		Role:     "lead research",
		Pipeline: "lead-research",
	}})

	cfg, task := mgr.MatchAtName("\u0040\u6f5c\u5ba2\u7814\u7a76\u5458 \u5e2e\u6211\u627eAI\u5e73\u53f0\u6f5c\u5728\u5ba2\u6237")
	if cfg == nil || cfg.ID != "lead-researcher" {
		t.Fatalf("matched employee = %#v, want lead-researcher", cfg)
	}
	if strings.Contains(task, "@") || strings.Contains(task, "\u5458") {
		t.Fatalf("task still contains leaked partial mention: %q", task)
	}
	if !strings.Contains(task, "AI") {
		t.Fatalf("task lost user request: %q", task)
	}
}

func TestAIEmployeeMatchAcceptsBuiltInSecretaryAliases(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "meeting-secretary",
		Name:     "\u79d8\u4e66",
		AtName:   "\u79d8\u4e66",
		Role:     "meeting notes",
		Pipeline: "meeting-summary",
	}})

	for _, msg := range []string{
		"\u0040\u4f1a\u8bae\u79d8\u4e66 \u6574\u7406\u4f1a\u8bae\u7eaa\u8981",
		"@meeting-secretary summarize meeting",
	} {
		cfg, task := mgr.MatchAtName(msg)
		if cfg == nil || cfg.ID != "meeting-secretary" {
			t.Fatalf("MatchAtName(%q) matched %#v, want meeting-secretary", msg, cfg)
		}
		if strings.TrimSpace(task) == "" || strings.Contains(task, "@") {
			t.Fatalf("MatchAtName(%q) extracted bad task %q", msg, task)
		}
	}

	mgr.Remove("meeting-secretary")
	cfg, _ := mgr.MatchAtName("\u0040\u4f1a\u8bae\u79d8\u4e66 \u6574\u7406\u4f1a\u8bae")
	if cfg != nil {
		t.Fatalf("removed secretary still matched: %#v", cfg)
	}
}

func TestAIEmployeeMatchStripsLeadingRouterMention(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "lead-researcher",
		Name:     "\u6f5c\u5ba2\u7814\u7a76\u5458",
		AtName:   "\u6f5c\u5ba2\u7814\u7a76",
		Role:     "lead research",
		Pipeline: "lead-research",
	}})

	cfg, task := mgr.MatchAtName("\u0040Browser \u0040\u6f5c\u5ba2\u7814\u7a76\u5458 \u5e2e\u6211\u627eAI\u5e73\u53f0\u6f5c\u5728\u5ba2\u6237")
	if cfg == nil || cfg.ID != "lead-researcher" {
		t.Fatalf("matched employee = %#v, want lead-researcher", cfg)
	}
	if strings.Contains(task, "@Browser") || strings.Contains(task, "\u6f5c\u5ba2\u7814\u7a76\u5458") {
		t.Fatalf("task should strip router mention and employee mention: %q", task)
	}
	if !strings.Contains(task, "AI") {
		t.Fatalf("task lost user request: %q", task)
	}
}

func TestAIEmployeeMatchRequiresExplicitEmployeeMentionAfterRouterMention(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "lead-researcher",
		Name:     "\u6f5c\u5ba2\u7814\u7a76\u5458",
		AtName:   "\u6f5c\u5ba2\u7814\u7a76",
		Role:     "lead research",
		Pipeline: "lead-research",
	}})

	cfg, _ := mgr.MatchAtName("\u0040Browser \u6f5c\u5ba2\u7814\u7a76\u5458 \u5e2e\u6211\u627eAI\u5e73\u53f0\u6f5c\u5728\u5ba2\u6237")
	if cfg != nil {
		t.Fatalf("bare employee name after router mention must not activate employee: %#v", cfg)
	}
}

func TestAIEmployeeMatchOnlyRemovesFirstEmployeeMention(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "meeting-secretary",
		Name:     "\u79d8\u4e66",
		AtName:   "\u79d8\u4e66",
		Role:     "meeting notes",
		Pipeline: "meeting-summary",
	}})

	_, task := mgr.MatchAtName("\u0040\u79d8\u4e66 \u8bf7\u628a\u79d8\u4e66\u7684\u5de5\u4f5c\u6e05\u5355\u6574\u7406\u6210\u8868\u683c")
	if !strings.Contains(task, "\u79d8\u4e66\u7684\u5de5\u4f5c\u6e05\u5355") {
		t.Fatalf("task should preserve later employee-name text: %q", task)
	}
}

func TestAIEmployeeMatchRequiresCompleteMentionBoundary(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "quant-trader",
		Name:     "AI交易员",
		AtName:   "交易员",
		Role:     "paper trading research",
		Pipeline: "quant-trader",
	}})

	for _, message := range []string{
		"@交易员助理 分析新安股份",
		"请联系@交易员团队分析新安股份",
		"@quant-trader-helper analyze 600596.SS",
	} {
		if cfg, _ := mgr.MatchAtName(message); cfg != nil {
			t.Fatalf("partial mention %q must not activate employee: %#v", message, cfg)
		}
	}

	cfg, task := mgr.MatchAtName("@交易员：分析新安股份")
	if cfg == nil || cfg.ID != "quant-trader" || !strings.Contains(task, "分析新安股份") {
		t.Fatalf("complete punctuated mention did not route correctly: cfg=%#v task=%q", cfg, task)
	}
}

func TestAIEmployeeMatchRoutesFirstExplicitEmployeeMention(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{
		{ID: "quant-trader", Name: "AI交易员", AtName: "交易员", Pipeline: "quant-trader"},
		{ID: "investment-research-manager", Name: "AI投研经理", AtName: "投研经理", Pipeline: "investment-research-manager"},
	})

	cfg, task := mgr.MatchAtName("@交易员 请让 @投研经理 复核结论")
	if cfg == nil || cfg.ID != "quant-trader" {
		t.Fatalf("first explicit employee should win, got %#v", cfg)
	}
	if !strings.Contains(task, "@投研经理") {
		t.Fatalf("task should preserve the later employee mention: %q", task)
	}
}
