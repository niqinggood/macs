package agentresult

import (
	"fmt"
	"strings"
	"unicode"

	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

type TerminalToolFailureCategory string

const (
	TerminalToolFailureLegalPolicy    TerminalToolFailureCategory = "legal_or_policy_block"
	TerminalToolFailureAuthentication TerminalToolFailureCategory = "authentication_required"
	TerminalToolFailurePermission     TerminalToolFailureCategory = "permission_required"
	TerminalToolFailureQuota          TerminalToolFailureCategory = "quota_or_billing_exhausted"

	// userRequestPolicyConflictErrorCode identifies a host-enforced conflict
	// with the current user's turn contract. The attempted call remains blocked,
	// but the agent may continue using tools and actions the user did authorize.
	UserRequestPolicyConflictErrorCode = "user_request_policy_conflict"
)

// TerminalToolFailure describes a failed prerequisite that the host must not
// route around automatically. It is intentionally separate from retry/circuit
// breaker accounting: ordinary source, network, syntax, and command failures
// remain available to the agent's bounded recovery loop.
type TerminalToolFailure struct {
	Category           TerminalToolFailureCategory
	ToolName           string
	ToolCallID         string
	Reason             string
	RequiresUserAction bool
}

func terminalFailureText(result toolcontract.Result, err error) string {
	parts := []string{result.Content, result.ErrorCode}
	if err != nil {
		parts = append(parts, err.Error())
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func containsTerminalFailureMarker(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func newTerminalToolFailure(call toolcontract.Call, category TerminalToolFailureCategory, reason string) TerminalToolFailure {
	return TerminalToolFailure{
		Category:           category,
		ToolName:           strings.TrimSpace(call.Name),
		ToolCallID:         strings.TrimSpace(call.ID),
		Reason:             reason,
		RequiresUserAction: true,
	}
}

func terminalToolFailureFromCode(call toolcontract.Call, code string) (TerminalToolFailure, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	code = strings.NewReplacer("-", "_", " ", "_").Replace(code)
	switch code {
	case "dmca_blocked", "legal_restriction", "policy_blocked", "policy_restriction":
		return newTerminalToolFailure(
			call,
			TerminalToolFailureLegalPolicy,
			"所需资源或操作受到法律、安全策略或平台政策限制。",
		), true
	case "authentication_failed", "authentication_required", "invalid_api_key", "invalid_credentials":
		return newTerminalToolFailure(
			call,
			TerminalToolFailureAuthentication,
			"当前路线缺少有效凭证或登录状态。",
		), true
	case "access_denied", "elevation_required", "permission_denied", "permission_required":
		return newTerminalToolFailure(
			call,
			TerminalToolFailurePermission,
			"当前路线需要尚未具备的访问权限或系统权限。",
		), true
	case "billing_required", "insufficient_credits", "quota_exhausted":
		return newTerminalToolFailure(
			call,
			TerminalToolFailureQuota,
			"当前账号的配额、余额或计费状态无法继续该路线。",
		), true
	default:
		return TerminalToolFailure{}, false
	}
}

// detectTerminalToolFailure recognizes only high-confidence external
// boundaries. A plain HTTP error is deliberately insufficient: read-only
// research may continue through another source already inside the task scope.
func DetectTerminalToolFailure(call toolcontract.Call, result toolcontract.Result, err error, isExternalEvidenceTool func(string) bool) (TerminalToolFailure, bool) {
	if result.Recovery != nil {
		return TerminalToolFailure{}, false
	}
	result.Name = call.Name
	if toolcontract.ToolResultExecutionSucceeded(result, err) {
		return TerminalToolFailure{}, false
	}
	// A user-request policy rejection is an observation about this particular
	// tool call, not an external prerequisite boundary. Keep the rejection in
	// model context so the agent can finish through the already-authorized
	// read-only route. This check is deliberately code-based; platform, hook,
	// legal, authentication, permission, and quota blocks remain terminal below.
	if strings.EqualFold(strings.TrimSpace(result.ErrorCode), UserRequestPolicyConflictErrorCode) {
		return TerminalToolFailure{}, false
	}

	text := terminalFailureText(result, err)
	if strings.TrimSpace(text) == "" {
		return TerminalToolFailure{}, false
	}

	if boundary, ok := terminalToolFailureFromCode(call, result.ErrorCode); ok &&
		boundary.Category == TerminalToolFailureLegalPolicy {
		return boundary, true
	}
	if containsTerminalFailureMarker(
		text,
		"dmca",
		"legal restriction",
		"legal reasons",
		"court order",
		"sanctions restriction",
		"blocked by organizational policy",
		"blocked by security policy",
		"blocked by policy",
		"prohibited by policy",
		"policy restriction",
		"[blocked by hook]",
		"法律限制",
		"因法律原因",
		"政策禁止",
		"受政策限制",
	) {
		return newTerminalToolFailure(
			call,
			TerminalToolFailureLegalPolicy,
			"所需资源或操作受到法律、安全策略或平台政策限制。",
		), true
	}

	// Search/fetch failures are source-local by default. Trying another
	// read-only source is still the same authorized research task.
	if isExternalEvidenceTool != nil && isExternalEvidenceTool(call.Name) {
		return TerminalToolFailure{}, false
	}
	if boundary, ok := terminalToolFailureFromCode(call, result.ErrorCode); ok {
		return boundary, true
	}

	if containsTerminalFailureMarker(
		text,
		"authentication failed",
		"authentication required",
		"invalid api key",
		"invalid access token",
		"invalid authentication token",
		"expired access token",
		"missing credentials",
		"credentials required",
		"permission denied (publickey)",
		"login required",
		"not authenticated",
		"401 unauthorized",
		"认证失败",
		"需要登录",
		"尚未登录",
		"凭证无效",
		"令牌无效",
		"api 密钥无效",
	) {
		return newTerminalToolFailure(
			call,
			TerminalToolFailureAuthentication,
			"当前路线缺少有效凭证或登录状态。",
		), true
	}

	if containsTerminalFailureMarker(
		text,
		"administrator privileges are required",
		"administrator privilege is required",
		"requires administrator privileges",
		"requires elevated privileges",
		"operation requires elevation",
		"must be run as root",
		"sudo password is required",
		"需要管理员权限",
		"需要提升权限",
	) || (strings.Contains(text, "fatal:") &&
		(strings.Contains(text, "403") || strings.Contains(text, "forbidden"))) ||
		(strings.Contains(text, "repository unavailable") &&
			(strings.Contains(text, "403") || strings.Contains(text, "forbidden"))) {
		return newTerminalToolFailure(
			call,
			TerminalToolFailurePermission,
			"当前路线需要尚未具备的访问权限或系统权限。",
		), true
	}

	if containsTerminalFailureMarker(
		text,
		"quota exhausted",
		"quota has been exhausted",
		"monthly quota exceeded",
		"billing required",
		"billing account required",
		"insufficient credits",
		"credit balance is insufficient",
		"payment required",
		"402 payment required",
		"配额已用尽",
		"配额不足",
		"余额不足",
		"需要付费",
	) {
		return newTerminalToolFailure(
			call,
			TerminalToolFailureQuota,
			"当前账号的配额、余额或计费状态无法继续该路线。",
		), true
	}

	return TerminalToolFailure{}, false
}

func BuildTerminalToolFailureDiagnostic(input string, call toolcontract.Call, failure TerminalToolFailure) string {
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		toolName = strings.TrimSpace(failure.ToolName)
	}
	if toolName == "" {
		toolName = "unknown"
	}
	reason := strings.TrimSpace(failure.Reason)
	if reason == "" {
		reason = "当前路线遇到了不可自动恢复的外部前置条件。"
	}
	reason = textutil.Truncate(reason, 240)

	if containsChinese(input) {
		return fmt.Sprintf(
			"任务已在不可自动恢复的前置条件处停止。\n\n- 工具：%s\n- 类型：%s\n- 原因：%s\n\n我没有自动改用其他提供方、提升权限或扩大任务范围。若要采用其他路线，请明确授权，或提供可用的凭证、权限或资源。",
			toolName,
			failure.Category,
			reason,
		)
	}
	return fmt.Sprintf(
		"Task stopped at a non-recoverable prerequisite boundary.\n\n- Tool: %s\n- Category: %s\n- Reason: %s\n\nNo alternate provider, privilege escalation, or broader-scope workaround was attempted. Explicitly authorize another route or provide the required credentials, permissions, or resource.",
		toolName,
		failure.Category,
		reason,
	)
}

func containsChinese(value string) bool {
	for _, current := range value {
		if unicode.Is(unicode.Han, current) {
			return true
		}
	}
	return false
}
