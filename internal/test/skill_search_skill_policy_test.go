package app

import (
	"os"
	"strings"
	"testing"
)

func TestMultiSearchSkillKeepsRetrievalAdaptive(t *testing.T) {
	data, err := os.ReadFile("builtin/web-tools/multi-search-engine/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ToLower(string(data))
	for _, want := range []string{
		"decision guidance, not a fixed state machine",
		"continue only while a material evidence gap remains",
		"change the retrieval strategy materially",
		"there is no target number of searches",
		"no special meaning attached to a third or sixth call",
		"never fill it from plausibility or model memory",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("multi-search-engine skill missing adaptive guidance %q", want)
		}
	}
}
