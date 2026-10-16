package app

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	sessioncore "mac/internal/session"
	"mac/internal/textutil"
)

func TestGatewaySessionHistoryCleansEveryInjectionRepresentation(t *testing.T) {
	longUserText := strings.Repeat("界", gatewaySessionHistoryMaxRunes+25)
	raw := []ChatMessage{
		{
			Role: "user",
			Content: "## Session\nweb_test, task 1\nCold data: internal cold path\n\n" +
				"Current date: 2026-07-11 Friday\n\n## User Request\n真正的问题",
		},
		{
			Role: "assistant",
			Content: "真正的回答\n\nTool/Skill Review\n" +
				"Failure statistics\nGuardrail\nlist_dir: 1 failure",
		},
		{
			Role: "assistant",
			Content: "[Auto verification required]\n" +
				"Before giving the final answer, call `verify_work`.\n" +
				"Original user request:\n修复文件\nUse `verify_work` with evidence.",
		},
		{Role: "system", Content: "[INTERNAL CONTROL — REFLECTION]\nhidden"},
		{Role: "tool", Content: "secret tool result"},
		{Role: "user", Content: longUserText},
	}
	original := append([]ChatMessage(nil), raw...)

	history := buildGatewaySessionHistory(raw)

	if !reflect.DeepEqual(raw, original) {
		t.Fatal("building model context must not mutate persisted chat rows")
	}
	if len(history.Messages) != 3 {
		t.Fatalf("clean message count = %d, want 3: %#v", len(history.Messages), history.Messages)
	}
	wants := []struct {
		role    string
		content string
	}{
		{role: "user", content: "真正的问题"},
		{role: "assistant", content: "真正的回答"},
	}
	for i, want := range wants {
		if got := history.Messages[i]["role"]; got != want.role {
			t.Fatalf("message %d role = %#v, want %q", i, got, want.role)
		}
		if got := history.Messages[i]["content"]; got != want.content {
			t.Fatalf("message %d content = %#v, want %q", i, got, want.content)
		}
	}
	truncated, _ := history.Messages[2]["content"].(string)
	if utf8.RuneCountInString(truncated) != gatewaySessionHistoryMaxRunes+3 ||
		!strings.HasSuffix(truncated, "...") {
		t.Fatalf("history must use rune-safe truncation, got %d runes suffix=%q",
			utf8.RuneCountInString(truncated), textutil.TruncateRunes(truncated, 12))
	}
	for _, leaked := range []string{
		"internal cold path", "Tool/Skill Review", "Failure statistics",
		"Auto verification", "secret tool result", "INTERNAL CONTROL",
	} {
		if strings.Contains(history.Context, leaked) {
			t.Fatalf("internal content %q leaked into collaboration context:\n%s", leaked, history.Context)
		}
	}
}

func TestGatewaySessionHistoryMessagesAndCollaborationContextAreEquivalent(t *testing.T) {
	history := buildGatewaySessionHistory([]ChatMessage{
		{Role: "USER", Content: "请分析明天天气"},
		{Role: " assistant ", Content: "明天有雨，带伞。"},
		{Role: "unknown", Content: "must not be treated as a user"},
	})

	if len(history.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(history.Messages))
	}
	for _, message := range history.Messages {
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		label := "用户"
		if role == "assistant" {
			label = "助手"
		}
		wantLine := label + ": " + content + "\n"
		if strings.Count(history.Context, wantLine) != 1 {
			t.Fatalf("collaboration history is not equivalent to role-aware messages; missing %q in:\n%s", wantLine, history.Context)
		}
	}
	if strings.Contains(history.Context, "unknown") {
		t.Fatalf("unknown role leaked into collaboration context: %s", history.Context)
	}
}

func TestSessionCarryoverKeepsLegitimateTechnicalDiscussionAndFencedExamples(t *testing.T) {
	userContent := `这是文档讨论，不是运行时前缀。
list_dir、verification_ 和 report_progress 都是本文的普通字面量。

## User Request
这个标题也是文档内容。

` + "```json\n" + `{"name":"list_dir","arguments":{"path":"."}}` + "\n```\n" +
		"<tool_call>{\"name\":\"report_progress\"}</tool_call>"

	gotUser := sessioncore.CleanCarryoverContent("user", userContent, 5000)
	for _, want := range []string{
		"list_dir", "verification_", "report_progress", "## User Request",
		"```json", `{"name":"list_dir"`, "<tool_call>",
	} {
		if !strings.Contains(gotUser, want) {
			t.Fatalf("legitimate user content %q was removed:\n%s", want, gotUser)
		}
	}

	assistantContent := "工具调用示例：\n\n```xml\n" +
		"<tool_call>{\"name\":\"list_dir\"}</tool_call>\n```\n\n" +
		"在业务文档中，verification_ 和 report_progress 只是名称。"
	gotAssistant := sessioncore.CleanCarryoverContent("assistant", assistantContent, 5000)
	for _, want := range []string{"```xml", "<tool_call>", "list_dir", "verification_", "report_progress"} {
		if !strings.Contains(gotAssistant, want) {
			t.Fatalf("legitimate assistant example %q was removed:\n%s", want, gotAssistant)
		}
	}
}
