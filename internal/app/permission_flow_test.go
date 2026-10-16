package app

import (
	"context"
	"io"
	"log"
	approvalcore "mac/internal/approval"
	"mac/internal/permissionpolicy"
	"mac/internal/security"
	"mac/internal/toolcontract"
	"mac/internal/toolregistry"
	securitytools "mac/internal/tools/security"
	"strings"
	"testing"
)

func flowPlanAgent(mode permissionpolicy.PermissionMode) *LLMAgent {
	return &LLMAgent{safetyGuard: &security.SafetyGuard{PermissionMode: mode}, permissionEngine: permissionpolicy.NewEngine(mode, toolcontract.IsReadOnlyToolName), fileLogger: log.New(io.Discard, "", 0)}
}
func TestFlowPlanModeUsesActualPermissionEngine(t *testing.T) {
	a := flowPlanAgent(permissionpolicy.PermissionDefault)
	a.EnterPlanMode()
	allowed, _, _ := a.permissionEngine.Check("write_file", nil)
	if allowed {
		t.Fatal("entering plan mode left actual execution engine writable")
	}
}
func TestFlowPlanModeRepeatedEnterAndExit(t *testing.T) {
	a := flowPlanAgent(permissionpolicy.PermissionDefault)
	a.EnterPlanMode()
	a.EnterPlanMode()
	a.ExitPlanMode()
	if a.IsInPlanMode() || a.safetyGuard.PermissionMode != permissionpolicy.PermissionDefault {
		t.Fatal("repeated enter overwrote original mode; exit remained read-only")
	}
}
func TestFlowExitConfiguredPlanMode(t *testing.T) {
	a := flowPlanAgent(permissionpolicy.PermissionPlan)
	a.ExitPlanMode()
	allowed, _, _ := a.permissionEngine.Check("write_file", nil)
	if !allowed {
		t.Fatal("configured plan mode still blocks execution after exit")
	}
}

func TestFlowPlanModeWithoutLegacyGuard(t *testing.T) {
	a := &LLMAgent{}
	a.EnterPlanMode()
	if !a.IsInPlanMode() {
		t.Fatal("plan transition was silently lost without a legacy guard")
	}
	if allowed, _, _ := a.permissionEngine.Check("write_file", nil); allowed {
		t.Fatal("plan boundary was not installed")
	}
	a.ExitPlanMode()
	if a.IsInPlanMode() {
		t.Fatal("plan mode did not exit")
	}
}

type flowApprovalBroker struct{ calls int }

func (b *flowApprovalBroker) Authorize(_ context.Context, taskID string, call toolcontract.Call, _ string) (approvalcore.ApprovalRequestRecord, error) {
	b.calls++
	return approvalcore.ApprovalRequestRecord{TaskID: taskID, ToolName: call.Name, Status: approvalcore.StatusPending}, nil
}
func (*flowApprovalBroker) Consume(context.Context, string) error { return nil }

type flowMixedTool struct{}

func (flowMixedTool) Name() string                       { return "mixed_tool" }
func (flowMixedTool) Description() string                { return "test" }
func (flowMixedTool) Parameters() map[string]interface{} { return nil }
func (flowMixedTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}
func (flowMixedTool) IsReadOnly() bool { return false }
func (flowMixedTool) IsReadOnlyForCall(args map[string]interface{}) bool {
	return args["action"] == "inspect"
}

func TestFlowPreflightDoesNotAskToOverrideUserRestriction(t *testing.T) {
	engine := permissionpolicy.NewEngine(permissionpolicy.PermissionDefault, toolcontract.IsReadOnlyToolName)
	engine.AddRule(permissionpolicy.PermissionRule{Source: permissionpolicy.SourceProject, ToolName: "write_file", Behavior: permissionpolicy.BehaviorAsk})
	broker := &flowApprovalBroker{}
	ctx := approvalcore.WithToolApprovalBroker(context.WithValue(context.Background(), "task_id", "task"), broker)
	ctx = permissionpolicy.WithUserToolPolicy(ctx, "先不要生成任何文件，先核对数据。")
	call := ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": "report.md", "content": "draft"}}
	if err := preflightToolApprovals(ctx, engine, nil, nil, []ToolCall{call}); err != nil || broker.calls != 0 {
		t.Fatalf("opened unusable approval: calls=%d err=%v", broker.calls, err)
	}
	agent := &LLMAgent{permissionEngine: engine}
	result, err, blocked := agent.checkToolSafety(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "USER REQUEST POLICY") || broker.calls != 0 {
		t.Fatalf("restriction was not preserved: %+v %v calls=%d", result, err, broker.calls)
	}
}

func TestFlowSandboxPreflightUsesSameArgumentsAsExecution(t *testing.T) {
	registry := toolregistry.New(toolregistry.Dependencies{})
	registry.Register(flowMixedTool{})
	config := securitytools.DefaultSandboxConfig()
	config.Mode = securitytools.SandboxReadOnly
	sandbox := securitytools.NewWorkspaceSandbox(t.TempDir(), config)
	for _, action := range []string{"inspect", "write"} {
		call := ToolCall{Name: "mixed_tool", Arguments: map[string]interface{}{"action": action}}
		err := preflightSandboxToolCall(context.Background(), sandbox, registry, call)
		if (err == nil) != (action == "inspect") {
			t.Fatalf("preflight action %s: %v", action, err)
		}
	}
}

func TestFlowHostModeChangeAndExplicitUserReview(t *testing.T) {
	a := flowPlanAgent(permissionpolicy.PermissionBypass)
	a.EnterPlanMode()
	a.EnterPlanMode()
	a.ExitPlanMode()
	a.ExitPlanMode()
	if mode := a.currentPermissionMode(); mode != permissionpolicy.PermissionBypass {
		t.Fatalf("mode changed after repeated transitions: %s", mode)
	}
	runtime := appManagementRuntime{agent: a}
	runtime.SetPermissionMode(string(permissionpolicy.PermissionPlan))
	if !a.IsInPlanMode() {
		t.Fatal("host mode update did not affect plan state")
	}
	runtime.SetPermissionMode(string(permissionpolicy.PermissionDefault))
	if a.IsInPlanMode() {
		t.Fatal("host exit left a stale plan flag")
	}
	a.ExitPlanMode()
	if a.currentPermissionMode() != permissionpolicy.PermissionDefault {
		t.Fatal("redundant exit restored stale mode")
	}
	ctx := permissionpolicy.WithUserToolPolicy(context.Background(), "只做只读检查，不要开始制作。")
	a.EnterPlanMode()
	a.ExitPlanMode()
	if _, err, blocked := a.checkToolSafety(ctx, ToolCall{Name: "write_file"}); !blocked || err == nil {
		t.Fatal("exiting plan mode erased the user's explicit review constraint")
	}
}
