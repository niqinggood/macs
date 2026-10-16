package app

import (
	"strings"
	"testing"
)

func TestAIEmployeeFinalRecoveryMessagesUseOriginalRequestAndClosedEvidence(t *testing.T) {
	request := "请直接交付一版方案，不要生成文件。"
	results := []string{
		toolResultForFinalization(
			ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "example"}},
			"internal skill instructions",
			true,
		),
		toolResultForFinalization(
			ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://example.com/product"}},
			"Source: https://example.com/product\nOfficial product facts",
			true,
		),
		toolResultForFinalization(
			ToolCall{Name: "web_search", Arguments: map[string]interface{}{"query": "example"}},
			"search failed",
			false,
		),
		toolResultForFinalization(
			ToolCall{Name: "persistent_shell"},
			"internal workspace listing",
			true,
		),
	}

	messages := buildAIEmployeeFinalRecoveryMessages(request, results)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if got := messages[1]["content"]; got != request {
		t.Fatalf("original request = %#v, want %#v", got, request)
	}
	evidence, _ := messages[2]["content"].(string)
	for _, want := range []string{"web_fetch", "https://example.com/product", "Official product facts"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("recovery evidence missing %q: %s", want, evidence)
		}
	}
	for _, forbidden := range []string{"internal skill instructions", "search failed", "internal workspace listing"} {
		if strings.Contains(evidence, forbidden) {
			t.Fatalf("recovery evidence leaked %q: %s", forbidden, evidence)
		}
	}
}

func TestAIEmployeeFinalRecoveryMessagesStayUsefulWithoutEvidence(t *testing.T) {
	messages := buildAIEmployeeFinalRecoveryMessages("请写 v0", nil)
	evidence, _ := messages[2]["content"].(string)
	if !strings.Contains(evidence, "No reusable evidence packet") ||
		!strings.Contains(messages[0]["content"].(string), "complete user-facing answer") {
		t.Fatalf("missing bounded no-evidence recovery contract: %#v", messages)
	}
}
