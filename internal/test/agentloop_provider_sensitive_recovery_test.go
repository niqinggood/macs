package app

import (
	"strings"
	"testing"
)

func TestProviderSensitiveInputErrorClassification(t *testing.T) {
	for _, message := range []string{
		`API返回错误状态码: 422, 响应: {"message":"input new_sensitive (1026)"}`,
		`unprocessable_entity_error: input sensitive`,
	} {
		if !isProviderSensitiveInputError(message) {
			t.Fatalf("provider sensitive-input error was not recognized: %q", message)
		}
	}
	for _, message := range []string{
		"API返回错误状态码: 422, invalid JSON",
		"用户在报告中讨论了敏感词识别",
		"API返回错误状态码: 500",
	} {
		if isProviderSensitiveInputError(message) {
			t.Fatalf("ordinary error/text was misclassified: %q", message)
		}
	}
}

func TestProviderSensitiveRecoveryMessagesKeepAuthorityAndDropUntrustedTranscript(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "authoritative system"},
		{"role": "user", "content": "allowed research task"},
		{"role": "assistant", "content": "untrusted assistant summary", "tool_calls": []interface{}{map[string]interface{}{"id": "call-1"}}},
		{"role": "tool", "tool_call_id": "call-1", "content": "SENSITIVE_EXTERNAL_BODY_SENTINEL"},
		{"role": "system", "content": "[Conversation Summary]\nSENSITIVE_SUMMARY_SENTINEL"},
	}

	recovered := providerSensitiveRecoveryMessages(messages)
	if len(recovered) != 3 {
		t.Fatalf("recovered message count = %d, want system + user + recovery control", len(recovered))
	}
	joined := ""
	for _, message := range recovered {
		content, _ := message["content"].(string)
		joined += content + "\n"
		if role, _ := message["role"].(string); role == "tool" || role == "assistant" {
			t.Fatalf("untrusted transcript role survived recovery: %#v", message)
		}
	}
	for _, want := range []string{"authoritative system", "allowed research task", "PROVIDER INPUT SAFETY RECOVERY"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recovery lost %q: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"SENSITIVE_EXTERNAL_BODY_SENTINEL", "SENSITIVE_SUMMARY_SENTINEL"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("recovery retained untrusted content %q: %s", forbidden, joined)
		}
	}
}
