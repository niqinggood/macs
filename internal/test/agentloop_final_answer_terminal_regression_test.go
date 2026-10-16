package app

import (
	"strings"
	"testing"
)

func TestCompletedFinalWithHistoricalToolUnavailableIsAccepted(t *testing.T) {
	output := `## ✅ 任务完成

微博热搜已成功发布到 Nova Chatroom，全部目标已达成。

- Post ID: 2529
- 分享链接：/chatroom/share?id=84d34a77b144f9ce
- 发帖身份：openkitty-Researcher-CN

关键障碍与解决：浏览器工具不可用（9222 端口被占），已改用 PTC 调用 chatroom 脚本。

验证证据：read 命令确认 #2529 已在目标话题中发出，无任何遗留阻塞项。`

	if isProgressOnlyFinalOutput(output) {
		t.Fatal("a completed answer must not be rejected merely because it recounts a historical '工具不可用' obstacle")
	}
	if shouldRetryProgressOnlyNoTool(output, 74, 0) {
		t.Fatal("a completed answer with concrete action evidence must not trigger CONTINUE TASK")
	}
}

func TestLegacyPostedActionResultBuildsDeliverable(t *testing.T) {
	result := `## PTC Execution (5.3s, 0 tool calls)

=== Post to Topic 7 ===
STDOUT:
Posted! ID: 2529
Share: /chatroom/share?id=84d34a77b144f9ce

RC: 0`

	if !looksLikeSuccessfulActionResult(result) {
		t.Fatal("legacy Posted!/Share/RC:0 output must be recognized as a successful action")
	}
	deliverable := buildActionDeliverableFinalAnswer(result)
	if deliverable == "" {
		t.Fatal("legacy successful post output must produce an action deliverable")
	}
	if !strings.Contains(deliverable, "2529") {
		t.Fatalf("action deliverable lost the post ID: %q", deliverable)
	}

	stored := toolResultForFinalization(ToolCall{
		Name: "ptc_execute",
		Arguments: map[string]interface{}{
			"script": `subprocess.run([python, "chatroom_agent.py", "post", "7", "@post.md"])`,
		},
	}, result, true)
	if recovered := buildActionDeliverableFinalAnswerFromToolResults([]string{stored}); !strings.Contains(recovered, "2529") {
		t.Fatalf("legacy result failed the real storage/recovery round trip: %q", recovered)
	}
}

func TestFailureDiagnosticFallbackIsNotRecursivelyWrapped(t *testing.T) {
	first := buildToolResultFallbackSummary(
		"让我继续整理最终结果。",
		48,
		120,
		1,
		nil,
		[]string{"ptc_execute"},
		[]string{"ptc_execute: Tool Result\nstatus: failed\nerror: temporary failure"},
		nil,
		nil,
	)
	second := buildToolResultFallbackSummary(
		first,
		48,
		120,
		1,
		nil,
		[]string{"ptc_execute"},
		[]string{"ptc_execute: Tool Result\nstatus: failed\nerror: temporary failure"},
		nil,
		nil,
	)

	if count := strings.Count(second, "未能生成可用最终回答"); count != 1 {
		t.Fatalf("failure diagnostic was recursively wrapped %d times:\n%s", count, second)
	}
}
