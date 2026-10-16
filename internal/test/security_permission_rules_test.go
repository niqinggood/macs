package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionRuleContentPrefixMatchesSuggestedCLI(t *testing.T) {
	rule := &PermissionRule{ToolName: "cli", RuleContent: "go test"}
	if !matchRule("cli", "go test ./...", rule) {
		t.Fatal("cli(go test) should match go test ./...")
	}
	if matchRule("cli", "go tool test2", rule) {
		t.Fatal("cli(go test) should not match unrelated command prefix")
	}
}

func TestProjectPermissionRulesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rules := []PermissionRule{
		{Source: SourceProject, Behavior: BehaviorAllow, ToolName: "cli", RuleContent: "go test"},
		{Source: SourceProject, Behavior: BehaviorDeny, ToolName: "cli", RuleContent: "git push --force"},
	}
	if err := SaveProjectPermissionRules(dir, rules); err != nil {
		t.Fatalf("save rules: %v", err)
	}
	got, err := LoadProjectPermissionRules(dir)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rules len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Behavior != BehaviorAllow || got[0].ToolName != "cli" || got[0].RuleContent != "go test" {
		t.Fatalf("unexpected first rule: %#v", got[0])
	}
	if want := filepath.Join(dir, ".openkitty", "permissions.json"); ProjectPermissionRulesPath(dir) != want {
		t.Fatalf("permission rules path = %q, want %q", ProjectPermissionRulesPath(dir), want)
	}
}

func TestPermissionEngineUsesProjectRules(t *testing.T) {
	engine := NewPermissionEngine(PermissionDefault)
	engine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorAllow, ToolName: "cli", RuleContent: "npm run build"})

	allowed, confirm, reason := engine.Check("cli", map[string]interface{}{"command": "npm run build -- --mode production"})
	if !allowed || confirm || reason != "" {
		t.Fatalf("project allow rule should bypass confirmation, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}

func TestLLMAgentCheckToolSafetyEnforcesPermissionRules(t *testing.T) {
	agent := &LLMAgent{
		permissionEngine: NewPermissionEngine(PermissionDefault),
		safetyGuard:      &SafetyGuard{PermissionMode: PermissionDefault},
	}
	agent.permissionEngine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorAllow, ToolName: "cli", RuleContent: "npm install"})
	result, err, handled := agent.checkToolSafety(context.Background(), ToolCall{
		ID:        "call_1",
		Name:      "cli",
		Arguments: map[string]interface{}{"command": "npm install"},
	})
	if handled || err != nil || result.Content != "" {
		t.Fatalf("allow rule should let command proceed, handled=%v err=%v result=%#v", handled, err, result)
	}

	agent.permissionEngine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorDeny, ToolName: "cli", RuleContent: "git push"})
	result, err, handled = agent.checkToolSafety(context.Background(), ToolCall{
		ID:        "call_2",
		Name:      "cli",
		Arguments: map[string]interface{}{"command": "git push origin main"},
	})
	if !handled || err == nil || result.ErrorCode != "permission_denied" || !strings.Contains(result.Content, "PERMISSION POLICY") {
		t.Fatalf("deny rule was not enforced, handled=%v err=%v result=%#v", handled, err, result)
	}
}

func TestPermissionEngineAskRequiresConfirmation(t *testing.T) {
	engine := NewPermissionEngine(PermissionDefault)
	engine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorAsk, ToolName: "cli", RuleContent: "git push"})

	allowed, confirm, reason := engine.Check("cli", map[string]interface{}{"command": "git push origin main"})
	if allowed || !confirm || !strings.Contains(reason, "requires approval") {
		t.Fatalf("ask rule must require approval, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}

func TestLLMAgentCheckToolSafetyRequiresApprovalForAskRule(t *testing.T) {
	agent := &LLMAgent{
		permissionEngine: NewPermissionEngine(PermissionDefault),
		safetyGuard:      &SafetyGuard{PermissionMode: PermissionDefault},
	}
	agent.permissionEngine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorAsk, ToolName: "cli", RuleContent: "git push"})

	result, err, handled := agent.checkToolSafety(context.Background(), ToolCall{
		ID:        "call_ask",
		Name:      "cli",
		Arguments: map[string]interface{}{"command": "git push origin main"},
	})
	if !handled || err == nil || result.ErrorCode != "approval_required" || !strings.Contains(result.Content, "APPROVAL REQUIRED") {
		t.Fatalf("ask rule did not require approval, handled=%v err=%v result=%#v", handled, err, result)
	}
}

func TestPermissionRulesPreferSpecificDenyWithinSameSource(t *testing.T) {
	engine := NewPermissionEngine(PermissionDefault)
	engine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorAllow, ToolName: "cli"})
	engine.AddRule(PermissionRule{Source: SourceProject, Behavior: BehaviorDeny, ToolName: "cli", RuleContent: "git push"})

	allowed, confirm, reason := engine.Check("cli", map[string]interface{}{"command": "git push --force origin main"})
	if allowed || confirm || !strings.Contains(reason, "denied") {
		t.Fatalf("specific deny must beat broad allow, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}
