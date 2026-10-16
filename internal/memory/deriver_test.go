package memory

import (
	"strings"
	"testing"
)

func TestParseDeriveFactsExtractsEmbeddedJSONArray(t *testing.T) {
	facts, err := ParseDeriveFacts("prefix [{\"subject\":\"User\",\"predicate\":\"prefers\",\"object\":\"dark mode\",\"confidence\":0.9}] suffix")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Subject != "User" || facts[0].Object != "dark mode" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestDeriverBuildConversationSummaryUsesCleaner(t *testing.T) {
	deriver := NewDeriver(DeriverDependencies{
		CleanContent: func(_ string, content string, _ int) string {
			return strings.ReplaceAll(content, "hidden", "")
		},
	})
	summary := deriver.BuildConversationSummary(
		"user hidden request",
		"assistant answer",
		[]map[string]interface{}{{"role": "user", "content": "recent hidden detail"}},
	)
	if strings.Contains(summary, "hidden") || !strings.Contains(summary, "assistant answer") {
		t.Fatalf("summary = %q", summary)
	}
}
