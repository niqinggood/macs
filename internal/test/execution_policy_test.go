package app

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type enterprisePolicyTestTool struct {
	name  string
	calls atomic.Int32
}

func (t *enterprisePolicyTestTool) Name() string        { return t.name }
func (t *enterprisePolicyTestTool) Description() string { return "enterprise policy test tool" }
func (t *enterprisePolicyTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string"},
			"value":  map[string]interface{}{"type": "string"},
		},
	}
}
func (t *enterprisePolicyTestTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	t.calls.Add(1)
	return "ok", nil
}

type identityCaptureMiddleware struct {
	BaseMiddleware
	mu       sync.Mutex
	identity ExecutionIdentity
}

func newIdentityCaptureMiddleware() *identityCaptureMiddleware {
	return &identityCaptureMiddleware{BaseMiddleware: NewBaseMiddleware("identity_capture")}
}

func (m *identityCaptureMiddleware) BeforeTool(ctx *AgentContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity = ExecutionIdentity{
		TenantID:  ctx.TenantID,
		UserID:    ctx.UserID,
		RoleID:    ctx.RoleID,
		AgentName: ctx.AgentName,
		Source:    ctx.Source,
		ServiceID: ctx.ServiceID,
	}
	return nil
}

func (m *identityCaptureMiddleware) snapshot() ExecutionIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.identity
}

func TestToolRegistryPropagatesSealedExecutionIdentity(t *testing.T) {
	registry := NewToolRegistry()
	tool := &enterprisePolicyTestTool{name: "inspect"}
	registry.Register(tool)
	capture := newIdentityCaptureMiddleware()
	registry.SetChain(NewMiddlewareChain(capture))

	ctx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", UserID: "alice", RoleID: "researcher",
		AgentName: "Researcher", Source: "agent_os", ServiceID: "crm", Service: true,
	})
	result, err := registry.Execute(ctx, ToolCall{ID: "call-1", Name: "inspect"})
	if err != nil || result.ExecutionSucceeded == nil || !*result.ExecutionSucceeded {
		t.Fatalf("tool execution failed: result=%#v err=%v", result, err)
	}
	got := capture.snapshot()
	if got.TenantID != "tenant-a" || got.UserID != "alice" || got.RoleID != "researcher" ||
		got.AgentName != "Researcher" || got.Source != "agent_os" || got.ServiceID != "crm" {
		t.Fatalf("identity was not propagated: %#v", got)
	}
}

func TestExecutionPolicyBlocksControlPlaneMutationFromToolCalls(t *testing.T) {
	registry := NewToolRegistry()
	tool := &enterprisePolicyTestTool{name: "sandbox"}
	registry.Register(tool)
	registry.SetChain(NewMiddlewareChain(NewExecutionPolicyMiddleware(
		NewPermissionEngine(PermissionDefault), nil, nil,
	)))

	result, err := registry.Execute(context.Background(), ToolCall{
		ID: "call-disable", Name: "sandbox",
		Arguments: map[string]interface{}{"action": "mode", "sandbox_mode": "none"},
	})
	if err != nil {
		t.Fatalf("blocked tool should return a structured observation, got %v", err)
	}
	if result.ExecutionSucceeded == nil || *result.ExecutionSucceeded ||
		result.ErrorCode != "control_plane_only" || tool.calls.Load() != 0 {
		t.Fatalf("control-plane mutation was not blocked: result=%#v calls=%d", result, tool.calls.Load())
	}

	result, err = registry.Execute(context.Background(), ToolCall{
		ID: "call-status", Name: "sandbox",
		Arguments: map[string]interface{}{"action": "status"},
	})
	if err != nil || result.ExecutionSucceeded == nil || !*result.ExecutionSucceeded || tool.calls.Load() != 1 {
		t.Fatalf("read-only status action should execute: result=%#v err=%v calls=%d", result, err, tool.calls.Load())
	}
}

func TestExecutionPolicyEnforcesRoleAllowlistAndPermissionRules(t *testing.T) {
	registry := NewToolRegistry()
	allowedTool := &enterprisePolicyTestTool{name: "read_file"}
	dynamicTool := &enterprisePolicyTestTool{name: "late_registered_admin"}
	registry.Register(allowedTool)
	registry.Register(dynamicTool)
	permissions := NewPermissionEngine(PermissionDefault)
	permissions.AddRule(PermissionRule{
		Source: SourceProject, Behavior: BehaviorDeny,
		ToolName: "read_file", RuleContent: "secret/*",
	})
	registry.SetChain(NewMiddlewareChain(NewExecutionPolicyMiddleware(
		permissions, []string{"read_file"}, nil,
	)))

	deniedRole, _ := registry.Execute(context.Background(), ToolCall{
		ID: "role-deny", Name: "late_registered_admin",
	})
	if deniedRole.ErrorCode != "role_tool_denied" || dynamicTool.calls.Load() != 0 {
		t.Fatalf("late registered tool escaped the role contract: %#v", deniedRole)
	}

	deniedRule, _ := registry.Execute(context.Background(), ToolCall{
		ID: "rule-deny", Name: "read_file",
		Arguments: map[string]interface{}{"path": "secret/customer.csv"},
	})
	if deniedRule.ErrorCode != "permission_denied" || allowedTool.calls.Load() != 0 {
		t.Fatalf("project deny rule was not enforced: %#v", deniedRule)
	}
}

func TestMCPExecutionUsesRegistryMiddleware(t *testing.T) {
	registry := NewToolRegistry()
	tool := &enterprisePolicyTestTool{name: "inspect"}
	registry.Register(tool)
	capture := newIdentityCaptureMiddleware()
	registry.SetChain(NewMiddlewareChain(capture))

	cfg := &Config{
		DefaultAgent: "PublishedRole",
		Agents:       []AgentConfig{{Name: "PublishedRole"}},
		ServiceRoles: []AgentServiceRoleConfig{{
			ID: "published-role", AgentName: "PublishedRole", Enabled: true,
			AllowedTenants: []string{"tenant-mcp"}, AllowedCallers: []string{"mcp-client"},
		}},
	}
	logger := log.New(io.Discard, "", 0)
	factory := &AgentFactory{
		agents: map[string]Agent{
			"PublishedRole": &LLMAgent{name: "PublishedRole", toolRegistry: registry},
		},
		subAgents: make(map[string]Agent),
	}
	engine := &MultiAgentEngine{config: cfg, agentFactory: factory, logger: logger, fileLogger: logger}
	host := &appMCPServerHost{gateway: &GatewayService{config: cfg, engine: engine, logger: logger}}
	ctx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-mcp", UserID: "service-user", Source: "mcp",
		ServiceID: "mcp-client", AllowedRoles: []string{"published-role"},
		Service: true, TenantScoped: true,
	})
	ctx = host.WithRequestedRole(ctx, "published-role")

	result, err := host.ExecuteTool(ctx, "inspect", map[string]interface{}{"value": "x"}, "mcp_7")
	if err != nil || result.ExecutionSucceeded == nil || !*result.ExecutionSucceeded || tool.calls.Load() != 1 {
		t.Fatalf("MCP registry execution failed: result=%#v err=%v calls=%d", result, err, tool.calls.Load())
	}
	got := capture.snapshot()
	if got.TenantID != "tenant-mcp" || got.RoleID != "published-role" || got.AgentName != "PublishedRole" {
		t.Fatalf("MCP identity did not reach middleware: %#v", got)
	}

	blockedCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-mcp", ServiceID: "mcp-client", Source: "mcp",
		AllowedRoles: []string{"another-role"}, Service: true, TenantScoped: true,
	})
	blockedCtx = host.WithRequestedRole(blockedCtx, "published-role")
	if _, err := host.ExecuteTool(blockedCtx, "inspect", nil, "mcp_8"); err == nil ||
		!strings.Contains(err.Error(), "service identity is not authorized") {
		t.Fatalf("MCP service identity escaped its role grant: %v", err)
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("blocked MCP role executed the tool: calls=%d", tool.calls.Load())
	}
}
