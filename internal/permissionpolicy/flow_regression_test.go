package permissionpolicy

import (
	"context"
	"mac/internal/toolcontract"
	"testing"
)

type flowReadTool struct{}

func (flowReadTool) Name() string                                                    { return "mixed_tool" }
func (flowReadTool) Description() string                                             { return "test" }
func (flowReadTool) Parameters() map[string]interface{}                              { return nil }
func (flowReadTool) Execute(context.Context, map[string]interface{}) (string, error) { return "", nil }
func (flowReadTool) IsReadOnly() bool                                                { return false }
func (flowReadTool) IsReadOnlyForCall(args map[string]interface{}) bool {
	return args["action"] == "inspect"
}

type flowLookup struct{}

func (flowLookup) Get(name string) (toolcontract.Tool, bool) {
	return flowReadTool{}, name == "mixed_tool"
}

func TestFlowUserRequestDoesNotInventMutationBan(t *testing.T) {
	for _, input := range []string{
		"不要再询问确认，直接生成文件。",
		"不必解释过程，修改文件并保存结果。",
		"不要反复确认。创建文件，然后修改代码。",
		"Don't ask again; create files and modify the code.",
		"不要再解释，直接重启服务。",
	} {
		t.Run(input, func(t *testing.T) {
			if p := DeriveUserToolPolicy(input); p != (UserToolPolicy{}) {
				t.Fatalf("unrelated negation blocked authorized work: %+v", p)
			}
		})
	}
}
func TestFlowReadOnlyPolicyUsesCallContract(t *testing.T) {
	p := UserToolPolicy{ForbidMutations: true}
	for _, action := range []string{"inspect", "write"} {
		call := toolcontract.Call{Name: "mixed_tool", Arguments: map[string]interface{}{"action": action}}
		blocked, _ := p.Blocks(call, flowLookup{}, nil)
		if blocked != (action == "write") {
			t.Fatalf("action %s blocked=%v", action, blocked)
		}
	}
}

func TestFlowExplicitRestrictionsRemainEffective(t *testing.T) {
	for _, input := range []string{
		"不得修改、创建或删除任何文件。",
		"不要修改，创建或删除任何文件。",
		"Do not edit, create, or delete files.",
		"不要保存文件。可以继续分析。",
		"先别开始制作，只给我两个取舍方案。",
		"Give me two options; do not start implementation yet.",
	} {
		t.Run(input, func(t *testing.T) {
			p := DeriveUserToolPolicy(input)
			if !p.ForbidFileMutations {
				t.Fatalf("explicit ban lost: %+v", p)
			}
			if blocked, _ := p.Blocks(toolcontract.Call{Name: "write_file"}, nil, toolcontract.IsReadOnlyToolName); !blocked {
				t.Fatal("write was allowed")
			}
			if blocked, _ := p.Blocks(toolcontract.Call{Name: "read_file"}, nil, toolcontract.IsReadOnlyToolName); blocked {
				t.Fatal("read was unnecessarily blocked")
			}
		})
	}
	if !DeriveUserToolPolicy("不调用任何工具").ForbidAllTools {
		t.Fatal("no-tools restriction lost")
	}
	if !DeriveUserToolPolicy("不要重启服务，也不要修改数据库").ForbidMutations {
		t.Fatal("state restriction lost")
	}
	for _, input := range []string{
		"请修改文件。<quote>不要修改任何文件</quote>",
		"帮我生成报告。\n> 不要生成任何文件",
		"现在开始创建文件，不要反复询问确认。",
	} {
		if p := DeriveUserToolPolicy(input); p != (UserToolPolicy{}) {
			t.Fatalf("quoted or unrelated ban affected task: %q %+v", input, p)
		}
	}
}

func TestFlowPlanUsesPerCallEffectsAndPreservesRules(t *testing.T) {
	calls := 0
	engine := NewEngineForCalls(PermissionPlan, func(name string, args map[string]interface{}) bool {
		calls++
		return ToolCallIsReadOnlyForPolicy(toolcontract.Call{Name: name, Arguments: args}, flowLookup{}, toolcontract.IsReadOnlyToolName)
	})
	if allowed, ask, _ := engine.Check("mixed_tool", map[string]interface{}{"action": "inspect"}); !allowed || ask {
		t.Fatal("read-only action blocked")
	}
	if calls != 1 {
		t.Fatalf("classification repeated %d times", calls)
	}
	for _, behavior := range []PermissionBehavior{BehaviorAllow, BehaviorAsk} {
		engine.ReplaceRules(SourceProject, []PermissionRule{{Behavior: behavior, ToolName: "mixed_tool"}})
		if allowed, ask, _ := engine.Check("mixed_tool", map[string]interface{}{"action": "write"}); allowed || ask {
			t.Fatal("persistent rule bypassed plan or asked for unusable approval")
		}
	}
	engine.ReplaceRules(SourceProject, []PermissionRule{{Behavior: BehaviorDeny, ToolName: "mixed_tool"}})
	if allowed, _, _ := engine.Check("mixed_tool", map[string]interface{}{"action": "inspect"}); allowed {
		t.Fatal("explicit deny ignored")
	}
	engine.SetMode(PermissionDefault)
	engine.ReplaceRules(SourceProject, []PermissionRule{{Behavior: BehaviorAsk, ToolName: "mixed_tool"}})
	calls = 0
	if allowed, ask, _ := engine.Check("mixed_tool", map[string]interface{}{"action": "write"}); allowed || !ask {
		t.Fatal("configured approval removed")
	}
	engine.ReplaceRules(SourceProject, nil)
	if allowed, ask, _ := engine.Check("mixed_tool", map[string]interface{}{"action": "write"}); !allowed || ask {
		t.Fatal("ordinary authorized write unnecessarily blocked")
	}
	if calls != 0 {
		t.Fatal("non-plan mode needlessly classified effects")
	}
}

func TestFlowReadOnlyShellCommandsRemainAvailableInPlan(t *testing.T) {
	engine := NewEngineForCalls(PermissionPlan, func(name string, args map[string]interface{}) bool {
		return ToolCallIsReadOnlyForPolicy(toolcontract.Call{Name: name, Arguments: args}, nil, toolcontract.IsReadOnlyToolName)
	})
	for _, tc := range []struct {
		command string
		allowed bool
	}{
		{"git status", true}, {"git diff", true}, {"rm -rf /", false}, {"go test ./...", false}, {"echo hello > output.txt", false},
	} {
		allowed, ask, _ := engine.Check("cli", map[string]interface{}{"command": tc.command})
		if allowed != tc.allowed || ask {
			t.Fatalf("command %q allowed=%v ask=%v", tc.command, allowed, ask)
		}
	}
}

func TestFlowResolvedRuleIsDetachedFromMutableRuleSet(t *testing.T) {
	rules := NewPermissionRuleSet()
	rules.AddRule(PermissionRule{Source: SourcePolicy, Behavior: BehaviorDeny, ToolName: "write_file"})
	_, rule, ok := rules.Resolve("write_file", "")
	if !ok || rule == nil {
		t.Fatal("rule not found")
	}
	rule.Behavior = BehaviorAllow
	behavior, _, ok := rules.Resolve("write_file", "")
	if !ok || behavior != BehaviorDeny {
		t.Fatal("changing a resolved rule silently mutated the active policy")
	}
}
