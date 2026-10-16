package app

import (
	"strings"
	"testing"
)

func TestCleanUserFacingFinalAnswerStripsThinkingBlock(t *testing.T) {
	output := `<think>
The user is asking what day of the week it is today.
</think>

今天是 **2026年7月10日，星期五**。`

	got := cleanUserFacingFinalAnswer(output)
	if strings.Contains(got, "The user is asking") || strings.Contains(got, "<think>") {
		t.Fatalf("final answer leaked thinking: %q", got)
	}
	if got != "今天是 **2026年7月10日，星期五**。" {
		t.Fatalf("final answer = %q", got)
	}
}

func TestIsProgressOnlyFinalOutputTreatsThinkingOnlyAsEmpty(t *testing.T) {
	if !isProgressOnlyFinalOutput("<think>hidden chain of thought</think>") {
		t.Fatal("thinking-only output should not be accepted as a final answer")
	}
}
