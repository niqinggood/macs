package middleware

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/toolcontract"
)

// ExecutionPolicyMiddleware is the single pre-execution authorization seam for
// native tools, MCP calls, and dynamically registered tools.
type ExecutionPolicyMiddleware struct {
	Base
	permissions PermissionChecker
	allowed     map[string]bool
	disallowed  map[string]bool
	hasAllow    bool
	authorize   ApprovalAuthorizer
}

type PermissionChecker interface {
	Check(toolName string, args map[string]interface{}) (allowed bool, approvalRequired bool, reason string)
}

type ApprovalAuthorizer func(context.Context, toolcontract.Call, string) error

func NewExecutionPolicyMiddleware(permissions PermissionChecker, allowed, disallowed []string, authorize ApprovalAuthorizer) *ExecutionPolicyMiddleware {
	allowedSet := toolcontract.StringSet(allowed)
	return &ExecutionPolicyMiddleware{
		Base:        NewBase("execution_policy"),
		permissions: permissions,
		allowed:     allowedSet,
		disallowed:  toolcontract.StringSet(disallowed),
		hasAllow:    len(allowedSet) > 0,
		authorize:   authorize,
	}
}

type executionPolicyError struct {
	code   string
	reason string
}

func (e *executionPolicyError) Error() string {
	if e == nil {
		return "execution policy blocked the tool"
	}
	return e.reason
}

func (e *executionPolicyError) MiddlewareErrorCode() string {
	if e == nil || strings.TrimSpace(e.code) == "" {
		return "policy_denied"
	}
	return e.code
}

func (m *ExecutionPolicyMiddleware) BeforeTool(ctx *Context) error {
	if m == nil || ctx == nil {
		return nil
	}
	toolName := toolcontract.NormalizeToolName(ctx.ToolCall.Name)
	if m.disallowed[toolName] || (m.hasAllow && !m.allowed[toolName]) {
		return &executionPolicyError{
			code:   "role_tool_denied",
			reason: fmt.Sprintf("role %q is not authorized to call tool %q", ctx.RoleID, ctx.ToolCall.Name),
		}
	}

	if controlPlaneToolMutation(ctx.ToolCall) {
		return &executionPolicyError{
			code: "control_plane_only",
			reason: fmt.Sprintf(
				"tool %q action %q changes host policy and cannot be executed from an agent or MCP tool call",
				ctx.ToolCall.Name, stringArgument(ctx.ToolCall.Arguments, "action"),
			),
		}
	}

	if m.permissions == nil {
		return nil
	}
	allowed, approvalRequired, reason := m.permissions.Check(ctx.ToolCall.Name, ctx.ToolCall.Arguments)
	if allowed {
		return nil
	}
	if approvalRequired {
		if m.authorize == nil {
			if strings.TrimSpace(reason) == "" {
				reason = fmt.Sprintf("tool %q requires approval", ctx.ToolCall.Name)
			}
			return &executionPolicyError{code: "approval_required", reason: reason}
		}
		if err := m.authorize(ctx.Context, ctx.ToolCall, reason); err != nil {
			return err
		}
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("tool %q is not allowed by the active permission policy", ctx.ToolCall.Name)
	}
	return &executionPolicyError{code: "permission_denied", reason: reason}
}

func controlPlaneToolMutation(call toolcontract.Call) bool {
	action := strings.ToLower(strings.TrimSpace(stringArgument(call.Arguments, "action")))
	switch toolcontract.NormalizeToolName(call.Name) {
	case "manage_permissions":
		return action == "set" || action == "add_rule" || action == "remove_rule"
	case "sandbox":
		return action == "mode" || action == "allow" || action == "deny"
	case "manage_plugins":
		return action == "enable" || action == "disable" || action == "discover"
	default:
		return false
	}
}

func stringArgument(arguments map[string]interface{}, key string) string {
	value, _ := arguments[key].(string)
	return value
}
