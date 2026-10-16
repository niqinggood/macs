package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mac/internal/agentresult"
)

type TerminalToolFailureCategory = agentresult.TerminalToolFailureCategory
type TerminalToolFailure = agentresult.TerminalToolFailure

const (
	terminalToolFailureLegalPolicy     = agentresult.TerminalToolFailureLegalPolicy
	terminalToolFailureAuthentication  = agentresult.TerminalToolFailureAuthentication
	terminalToolFailurePermission      = agentresult.TerminalToolFailurePermission
	terminalToolFailureQuota           = agentresult.TerminalToolFailureQuota
	userRequestPolicyConflictErrorCode = agentresult.UserRequestPolicyConflictErrorCode
)

func detectTerminalToolFailure(call ToolCall, result ToolResult, err error) (TerminalToolFailure, bool) {
	return agentresult.DetectTerminalToolFailure(call, result, err, isExternalEvidenceToolName)
}

func buildTerminalToolFailureDiagnostic(input string, call ToolCall, failure TerminalToolFailure) string {
	return agentresult.BuildTerminalToolFailureDiagnostic(input, call, failure)
}

type terminalBoundaryTestTool struct {
	name     string
	output   string
	err      error
	runCount *int
}

func (t terminalBoundaryTestTool) Name() string        { return t.name }
func (t terminalBoundaryTestTool) Description() string { return "terminal boundary test tool" }
func (t terminalBoundaryTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t terminalBoundaryTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	if t.runCount != nil {
		*t.runCount++
	}
	return t.output, t.err
}

func failedToolResult(content string) ToolResult {
	succeeded := false
	return ToolResult{
		Name:               "cli",
		Content:            content,
		ExecutionSucceeded: &succeeded,
	}
}

func TestAgentToolExecutorStopsLaterSerialCallsAtTerminalBoundary(t *testing.T) {
	firstRuns, secondRuns := 0, 0
	registry := NewToolRegistry()
	registry.Register(terminalBoundaryTestTool{
		name:     "blocked-prerequisite",
		output:   "remote: Repository unavailable due to DMCA takedown",
		err:      errors.New("requested URL returned error: 403"),
		runCount: &firstRuns,
	})
	registry.Register(terminalBoundaryTestTool{
		name:     "broader-workaround",
		output:   "should not execute",
		runCount: &secondRuns,
	})

	agent := &LLMAgent{name: "boundary-test", toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summaries := []string{}
	callChain := []map[string]interface{}{}
	results := []string{}
	var totalDurationMs int64
	toolExecCount := 0
	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		TaskID:          "terminal-boundary-task",
		ToolCallSummary: &summaries,
		CallChain:       &callChain,
		TotalDurationMs: &totalDurationMs,
		AllToolResults:  &results,
		ToolExecCount:   &toolExecCount,
	}

	err := executor.Execute(context.Background(), []ToolCall{
		{ID: "call-1", Name: "blocked-prerequisite", Arguments: map[string]interface{}{}},
		{ID: "call-2", Name: "broader-workaround", Arguments: map[string]interface{}{}},
	}, state)
	if err != nil {
		t.Fatalf("terminal boundary should be a recorded task outcome, not an executor crash: %v", err)
	}
	if firstRuns != 1 || secondRuns != 0 {
		t.Fatalf("execution crossed terminal boundary: first=%d second=%d", firstRuns, secondRuns)
	}
	if state.TerminalFailure == nil || state.TerminalFailure.Category != terminalToolFailureLegalPolicy {
		t.Fatalf("terminal boundary was not preserved in execution state: %#v", state.TerminalFailure)
	}
	if toolExecCount != 1 || len(results) != 1 || len(messages) != 1 {
		t.Fatalf("only the blocking call should be recorded: count=%d results=%d messages=%d", toolExecCount, len(results), len(messages))
	}
}

func TestDetectTerminalToolFailureLegalRestriction(t *testing.T) {
	call := ToolCall{ID: "call-1", Name: "cli", Arguments: map[string]interface{}{"command": "install dependency"}}
	result := failedToolResult("remote: Repository unavailable due to DMCA takedown\nfatal: requested URL returned error: 403")

	boundary, ok := detectTerminalToolFailure(call, result, errors.New("exit status 128"))
	if !ok {
		t.Fatal("expected an explicit legal restriction to stop the current route")
	}
	if boundary.Category != terminalToolFailureLegalPolicy {
		t.Fatalf("unexpected category: %#v", boundary)
	}
	if !boundary.RequiresUserAction {
		t.Fatalf("terminal boundary should require user direction: %#v", boundary)
	}
}

func TestDetectTerminalToolFailureKeepsHardSecurityPolicyTerminal(t *testing.T) {
	call := ToolCall{ID: "call-security", Name: "cli"}
	result := failedToolResult("operation blocked by security policy: high-risk command")

	boundary, ok := detectTerminalToolFailure(call, result, errors.New("policy enforcement denied execution"))
	if !ok || boundary.Category != terminalToolFailureLegalPolicy {
		t.Fatalf("hard security policy block must remain terminal: ok=%v boundary=%#v", ok, boundary)
	}
}

func TestDetectTerminalToolFailureLegacyUserRequestPolicyMarkerIsRecoverable(t *testing.T) {
	call := ToolCall{ID: "call-user-policy", Name: "write_file"}
	result := failedToolResult(`[BLOCKED: USER REQUEST POLICY] tool "write_file" is blocked: the user explicitly prohibited file or workspace mutations`)

	if boundary, ok := detectTerminalToolFailure(call, result, errors.New("tool blocked before dispatch")); ok {
		t.Fatalf("legacy user-request policy observations must not terminate the task: %#v", boundary)
	}
}

func TestDetectTerminalToolFailureDoesNotStopOrdinarySourceFailure(t *testing.T) {
	call := ToolCall{ID: "call-2", Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://example.invalid/article"}}
	result := failedToolResult("HTTP error: 403 Forbidden")

	if boundary, ok := detectTerminalToolFailure(call, result, errors.New("403 Forbidden")); ok {
		t.Fatalf("an ordinary source failure may use another in-scope read-only source, got %#v", boundary)
	}
}

func TestDetectTerminalToolFailureCredentialAndPermissionBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		category TerminalToolFailureCategory
	}{
		{
			name:     "invalid credential",
			content:  "authentication failed: invalid API key",
			category: terminalToolFailureAuthentication,
		},
		{
			name:     "privilege required",
			content:  "operation failed: administrator privileges are required",
			category: terminalToolFailurePermission,
		},
		{
			name:     "quota exhausted",
			content:  "account quota exhausted; update billing before retrying",
			category: terminalToolFailureQuota,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := ToolCall{ID: "call-3", Name: "cli"}
			boundary, ok := detectTerminalToolFailure(call, failedToolResult(tt.content), errors.New(tt.content))
			if !ok || boundary.Category != tt.category {
				t.Fatalf("expected %s terminal boundary, got ok=%v boundary=%#v", tt.category, ok, boundary)
			}
		})
	}
}

func TestDetectTerminalToolFailureUsesStructuredErrorCode(t *testing.T) {
	tests := []struct {
		code     string
		category TerminalToolFailureCategory
	}{
		{code: "authentication_required", category: terminalToolFailureAuthentication},
		{code: "permission_denied", category: terminalToolFailurePermission},
	}
	for _, tt := range tests {
		result := failedToolResult("")
		result.ErrorCode = tt.code
		boundary, ok := detectTerminalToolFailure(
			ToolCall{ID: "call-code", Name: "cli"},
			result,
			nil,
		)
		if !ok || boundary.Category != tt.category {
			t.Fatalf("structured terminal code %q was not recognized: ok=%v boundary=%#v", tt.code, ok, boundary)
		}
	}
}

func TestAgentToolExecutorContinuesAfterRecoverableFileLock(t *testing.T) {
	firstRuns, secondRuns := 0, 0
	registry := NewToolRegistry()
	registry.Register(terminalBoundaryTestTool{
		name:     "save-locked-document",
		output:   "PermissionError: [Errno 13] Permission denied: 'proposal.docx'",
		err:      errors.New("exit status 1"),
		runCount: &firstRuns,
	})
	registry.Register(terminalBoundaryTestTool{
		name:     "save-as-new-document",
		output:   "saved proposal_完善版.docx",
		runCount: &secondRuns,
	})

	agent := &LLMAgent{name: "file-lock-recovery-test", toolRegistry: registry}
	executor := NewAgentToolExecutor(agent)
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summaries := []string{}
	callChain := []map[string]interface{}{}
	results := []string{}
	var totalDurationMs int64
	toolExecCount := 0
	state := &ToolExecutionState{
		Messages:        &messages,
		ColdMessages:    &coldMessages,
		TaskID:          "file-lock-recovery-task",
		ToolCallSummary: &summaries,
		CallChain:       &callChain,
		TotalDurationMs: &totalDurationMs,
		AllToolResults:  &results,
		ToolExecCount:   &toolExecCount,
	}

	err := executor.Execute(context.Background(), []ToolCall{
		{ID: "call-lock", Name: "save-locked-document", Arguments: map[string]interface{}{}},
		{ID: "call-save-as", Name: "save-as-new-document", Arguments: map[string]interface{}{}},
	}, state)
	if err != nil {
		t.Fatalf("recoverable file lock batch returned executor error: %v", err)
	}
	if firstRuns != 1 || secondRuns != 1 || state.TerminalFailure != nil {
		t.Fatalf("file lock prevented in-scope save-as recovery: first=%d second=%d terminal=%#v", firstRuns, secondRuns, state.TerminalFailure)
	}
}

func TestDetectTerminalToolFailureLeavesRecoverableAndTransientFailuresOpen(t *testing.T) {
	recoverable := failedToolResult("access denied until the file is refreshed")
	recoverable.Recovery = &ToolRecoveryDirective{
		Code:          "stale_edit_context",
		SuggestedTool: "read_file",
	}
	call := ToolCall{ID: "call-4", Name: "edit_file"}
	if boundary, ok := detectTerminalToolFailure(call, recoverable, errors.New("access denied")); ok {
		t.Fatalf("structured recovery must take precedence, got %#v", boundary)
	}

	transient := failedToolResult("connection reset by peer; retry later")
	if boundary, ok := detectTerminalToolFailure(
		ToolCall{ID: "call-5", Name: "cli"},
		transient,
		errors.New("connection reset by peer"),
	); ok {
		t.Fatalf("transient network errors must remain recoverable, got %#v", boundary)
	}

	// A document can be open in Word while the agent is trying to save it.
	// That is a file-specific, recoverable failure: the same authorized task
	// can save a new copy without requesting elevation or broader access.
	lockedDocument := failedToolResult("Traceback (most recent call last):\n" +
		"PermissionError: [Errno 13] Permission denied: 'E:\\\\产品\\\\OPC-demans\\\\OPC开发平台项目实施方案.docx'")
	if boundary, ok := detectTerminalToolFailure(
		ToolCall{ID: "call-locked-doc", Name: "cli"},
		lockedDocument,
		errors.New("exit status 1"),
	); ok {
		t.Fatalf("a locked output file should allow an in-scope save-as recovery, got %#v", boundary)
	}
}

func TestDetectTerminalToolFailureTreatsSSHCredentialFailureAsAuthentication(t *testing.T) {
	content := "git@example.com: Permission denied (publickey)."
	boundary, ok := detectTerminalToolFailure(
		ToolCall{ID: "call-ssh", Name: "cli"},
		failedToolResult(content),
		errors.New("exit status 255"),
	)
	if !ok || boundary.Category != terminalToolFailureAuthentication {
		t.Fatalf("SSH credential failure should remain terminal authentication, ok=%v boundary=%#v", ok, boundary)
	}
}

func TestBuildTerminalToolFailureDiagnosticIsScopedAndSecretSafe(t *testing.T) {
	call := ToolCall{
		ID:   "call-6",
		Name: "cli",
		Arguments: map[string]interface{}{
			"token": "sk-secret-that-must-not-appear",
		},
	}
	boundary := TerminalToolFailure{
		Category:           terminalToolFailureLegalPolicy,
		Reason:             "所需资源受到法律或平台政策限制。",
		RequiresUserAction: true,
	}

	diagnostic := buildTerminalToolFailureDiagnostic("请安装这个依赖", call, boundary)
	for _, required := range []string{"cli", string(terminalToolFailureLegalPolicy), "没有自动改用其他提供方", "明确"} {
		if !strings.Contains(diagnostic, required) {
			t.Fatalf("diagnostic missing %q: %s", required, diagnostic)
		}
	}
	if strings.Contains(diagnostic, "sk-secret") {
		t.Fatalf("diagnostic leaked tool arguments: %s", diagnostic)
	}
}
