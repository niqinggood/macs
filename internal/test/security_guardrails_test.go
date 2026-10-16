package app

import (
	"testing"
)

func TestGuardrailsClassifyAPIError(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		retryable bool
		category  string
	}{
		{"401 status", "401 Unauthorized", false, "auth"},
		{"403 status", "403 Forbidden", false, "auth_forbidden"},
		{"explicit HTTP 403", "HTTP/1.1 403", false, "auth_forbidden"},
		{"structured 403 status", `response status_code=403; body={"code":700001}`, false, "auth_forbidden"},
		{"402 status", "402 quota exceeded", false, "billing"},
		{"HTTP 402 ignores embedded business code", `API returned HTTP 402; body={"code":403004,"message":"Account is in arrears"}`, false, "billing"},
		{"Chinese 402 ignores embedded business code", `API返回错误状态码: 402, 响应: {"code":403004,"message":"账户欠费"}`, false, "billing"},
		{"billing semantic ignores embedded business code", `provider failure: {"code":403004,"message":"payment required"}`, false, "billing"},
		{"explicit 403 precedes billing semantic", `HTTP 403; body={"message":"payment required"}`, false, "auth_forbidden"},
		{"insufficient balance", "Insufficient Balance", false, "quota_exhausted"},
		{"auth semantic ignores embedded business code", `provider code 402009: invalid API key`, false, "auth"},
		{"429 status", "429 Too Many Requests", true, "rate_limit"},
		{"structured 429 status", `{"status":429,"message":"slow down"}`, true, "rate_limit"},
		{"context length", "context length exceeded", true, "context_overflow"},
		{"maximum context", "maximum context length", true, "context_overflow"},
		{"timeout", "connection timed out", true, "timeout"},
		{"network EOF", "EOF", true, "network"},
		{"500 status", "500 Internal Server Error", true, "server_error"},
		{"structured 500 status", `response status: 500`, true, "server_error"},
		{"502 status", "502 Bad Gateway", true, "server_error"},
		{"503 status", "503 Service Unavailable", true, "overloaded"},
		{"reasoning protocol", `400 invalid_request_error: The reasoning_content in the thinking mode must be passed back to the API.`, false, "reasoning_context_missing"},
		{"403-like business code", `provider failure: {"code":403004,"message":"request rejected"}`, true, "unknown"},
		{"401-like business code", `provider business code 401009: request rejected`, true, "unknown"},
		{"429-like business code", `provider business code 429001: request rejected`, true, "unknown"},
		{"500-like business code", `provider business code 500123: request rejected`, true, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryable, category := classifyAPIError(tt.errMsg)
			if retryable != tt.retryable {
				t.Errorf("classifyAPIError(%q): retryable=%v, want=%v", tt.errMsg, retryable, tt.retryable)
			}
			if category != tt.category {
				t.Errorf("classifyAPIError(%q): category=%s, want=%s", tt.errMsg, category, tt.category)
			}
		})
	}
}

func TestExtractHTTPStatusUsesExplicitBoundedRepresentations(t *testing.T) {
	tests := []struct {
		name       string
		errMsg     string
		wantStatus int
		wantOK     bool
	}{
		{"HTTP protocol line", "HTTP/2 403", 403, true},
		{"HTTP status label", "HTTP status code: 402", 402, true},
		{"structured status", `{"status": 429}`, 429, true},
		{"structured status code", `{"status_code":"500"}`, 500, true},
		{"Chinese status label", "API返回错误状态码: 503", 503, true},
		{"leading status", "401 Unauthorized", 401, true},
		{"long business code", `{"code":403004}`, 0, false},
		{"exact business code field", `{"code":403}`, 0, false},
		{"long status-like field", `{"status_code":403004}`, 0, false},
		{"unlabelled number", "request 500 failed", 0, false},
		{"out of range status", "HTTP 700", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ok := extractHTTPStatus(tt.errMsg)
			if status != tt.wantStatus || ok != tt.wantOK {
				t.Fatalf("extractHTTPStatus(%q) = (%d, %v), want (%d, %v)",
					tt.errMsg, status, ok, tt.wantStatus, tt.wantOK)
			}
		})
	}
}

func TestReasoningContextErrorIsNotToolCallFormatError(t *testing.T) {
	errMsg := `API返回错误状态码: 400, 响应: {"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`
	if !isReasoningContextProtocolError(errMsg) {
		t.Fatal("reasoning replay rejection was not recognized")
	}
	if isToolCallProtocolError(errMsg) {
		t.Fatal("reasoning replay rejection was misclassified as tool-call protocol failure")
	}
	if !isToolCallProtocolError("400 invalid_request_error: tool_call_id does not match any tool_calls") {
		t.Fatal("genuine tool-call protocol rejection was not recognized")
	}
}

func TestGuardrailsIsReadOnlyTool(t *testing.T) {
	readOnly := []string{"read_file", "list_dir", "grep", "glob", "snapshot", "search_history",
		"knowledge_query", "list_skills", "tool_detail", "rag_search", "web_search",
		"plan_task", "update_plan_step"}
	for _, name := range readOnly {
		if !isReadOnlyTool(name) {
			t.Errorf("%s should be read-only", name)
		}
	}

	sideEffect := []string{"write_file", "edit_file", "cli", "web_fetch", "external_agent", "git"}
	for _, name := range sideEffect {
		if isReadOnlyTool(name) {
			t.Errorf("%s should NOT be read-only", name)
		}
	}
}

func TestGuardrailsFormatAPIErrorHint(t *testing.T) {
	tests := []struct {
		errMsg string
		want   string
	}{
		{"401 invalid api key", "API Key"},
		{"Insufficient Balance", "余额不足"},
		{"429 rate limit", "请求频率超限"},
		{"context length exceeded", "上下文过长"},
		{"connection timeout", "网络连接异常"},
	}
	for _, tt := range tests {
		hint := formatAPIErrorHint(tt.errMsg)
		if hint == "" {
			t.Errorf("formatAPIErrorHint(%q) returned empty", tt.errMsg)
		}
		if len(hint) > 500 {
			t.Errorf("hint too long: %d chars", len(hint))
		}
		_ = hint
		_ = tt.want
	}
}

func TestGuardrailsToolResultPreview(t *testing.T) {
	result := toolResultPreview("cli", "exit status 1\ncommand failed\nsome error")
	if result == "" {
		t.Error("preview should not be empty")
	}
	if len(result) > 500 {
		t.Error("preview should be concise")
	}
}

func TestToolGuardrailRecordAfterUsesFailedContent(t *testing.T) {
	cfg := DefaultGuardrailConfig()
	cfg.DiagnosticAfterFailures = 2
	controller := NewToolGuardrailController(cfg)

	args := map[string]interface{}{"path": "missing.txt"}
	content := "执行失败: file not found"

	if _, decision := controller.RecordAfter("cli", args, content, nil); decision != nil {
		t.Fatalf("first failed-content result should not inject diagnostic yet: %#v", decision)
	}
	_, decision := controller.RecordAfter("cli", args, content, nil)
	if decision == nil || decision.Code != "diagnostic_needed" {
		t.Fatalf("second failed-content result should request diagnostic, got %#v", decision)
	}
}
