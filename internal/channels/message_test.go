package channels

import (
	"context"
	"strings"
	"testing"
)

func TestAgentContextIncludesTaskMetadata(t *testing.T) {
	ctx := AgentContext(context.Background(), "task-1", "session-1", "qq", "user-1")
	for key, expected := range map[string]string{
		"task_id": "task-1", "session_id": "session-1", "channel": "qq", "user_id": "user-1",
	} {
		if actual, _ := ctx.Value(key).(string); actual != expected {
			t.Fatalf("ctx[%s] = %q, want %q", key, actual, expected)
		}
	}
}

func TestPrepareChannelMessageForAgentClaudeCommand(t *testing.T) {
	got := PrepareMessageForAgent("/claude 修复登录接口的空指针并跑测试")
	for _, want := range []string{
		"external_agent",
		"agent: claude",
		"修复登录接口的空指针并跑测试",
		"Report 路径",
		"Changed files",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prepared command missing %q:\n%s", want, got)
		}
	}
}

func TestPrepareChannelMessageForAgentLeavesNormalMessage(t *testing.T) {
	msg := "帮我总结一下今天的任务"
	if got := PrepareMessageForAgent(msg); got != msg {
		t.Fatalf("normal message changed: %q", got)
	}
}

func TestFormatChannelReplyForTransportKeepsExternalAgentMeta(t *testing.T) {
	content := "[External Agent: codex] [Conversation: ext_1] [Status: success] [Duration: 1.2s]\n" +
		"[Report: .openkitty/storage/external_agents/codex_ext_1.md]\n" +
		"[Changed files]\n- M api.go\n- A api_test.go\n\n" +
		strings.Repeat("codex output\n", 200)

	got := FormatReplyForTransportLimit(content, 600)
	for _, want := range []string{
		"[External Agent: codex]",
		"完整执行报告: .openkitty/storage/external_agents/codex_ext_1.md",
		"- M api.go",
		"输出过长",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted reply missing %q:\n%s", want, got)
		}
	}
}
