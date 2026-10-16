package app

import (
	"strings"
	"testing"
)

func TestChatroomDeliveryRequested(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"先整理 PPT 和 MD，然后发到 chatroom", true},
		{"Please publish the finished report to Nova Chatroom", true},
		{"把结果同步到聊天室", true},
		{"chatroom 有一篇文章，帮我搜索并总结", false},
		{"只生成报告，不要发布到 chatroom", false},
		{"无需发送到聊天室，保存在本地即可", false},
	}
	for _, tt := range tests {
		if got := chatroomDeliveryRequested(tt.input); got != tt.want {
			t.Errorf("chatroomDeliveryRequested(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTrustedActionIntentRecognizesBuiltinCodeCheckPublish(t *testing.T) {
	publish := ToolCall{Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `publish @"C:\workspace\.openkitty\storage\.system\nova-community\prepared.json" --intent-id weixin_session-t1`,
	}}
	if got := trustedActionIntent(publish); got != "post" {
		t.Fatalf("code_check publish intent = %q, want post", got)
	}

	prepare := publish
	prepare.Arguments = map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `prepare 2 @"final.md" --output "prepared.json"`,
	}
	if got := trustedActionIntent(prepare); got != "" {
		t.Fatalf("read-only prepare was classified as mutation %q", got)
	}

	untrusted := publish
	untrusted.Arguments = map[string]interface{}{
		"action": "run",
		"file":   `C:\workspace\chatroom_agent.py`,
		"args":   `publish @"prepared.json"`,
	}
	if got := trustedActionIntent(untrusted); got != "" {
		t.Fatalf("untrusted script was classified as mutation %q", got)
	}
}

func TestCodeCheckChatroomReceiptClosesDeliveryContract(t *testing.T) {
	hash := strings.Repeat("a", 64)
	call := ToolCall{Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `publish @"prepared.json" --intent-id weixin_session-t1`,
	}}
	content := `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":3097,"topic_id":2,"share_url":"/chatroom/share?id=abc","content_sha256":"` + hash + `","robot_id":"openkitty-test"}`
	stored := toolResultForFinalization(call, content, true)

	satisfied, attempted := chatroomDeliveryStatus([]string{stored})
	if !satisfied || !attempted {
		t.Fatalf("delivery status = satisfied %v attempted %v; stored=%s", satisfied, attempted, stored)
	}
	if issue, _ := chatroomDeliveryCompletionIssue("请发布到 chatroom", []string{stored}); issue != "" {
		t.Fatalf("successful receipt left a delivery issue: %s", issue)
	}
	for _, want := range []string{"3097", "/chatroom/share?id=abc", hash} {
		if deliverable := buildActionDeliverableFinalAnswerFromToolResults([]string{stored}); !strings.Contains(deliverable, want) {
			t.Fatalf("action deliverable missing %q: %s", want, deliverable)
		}
	}
}

func TestFailedChatroomAttemptCannotBeRetriedAsMissing(t *testing.T) {
	call := ToolCall{Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `publish @"prepared.json" --intent-id weixin_session-t1`,
	}}
	stored := toolResultForFinalization(call, "publish timeout; outcome uncertain", false)
	issue, attempted := chatroomDeliveryCompletionIssue("发布到 chatroom", []string{stored})
	if !attempted || !strings.Contains(issue, "不能自动重试") {
		t.Fatalf("failed mutation issue = %q attempted=%v", issue, attempted)
	}
}
