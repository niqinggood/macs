package app

import (
	"context"
	"strings"
	"testing"
)

func TestCapabilitiesToolListsBrowserAutomation(t *testing.T) {
	reg := NewToolRegistry()
	for _, name := range []string{"web_fetch", "web_search", "agent_reach", "social_trending", "platform_search", "browser_task", "web_targets", "web_navigate", "web_page", "web_close", "web_replay"} {
		reg.Register(&mockTool{name: name, desc: "tool " + name})
	}

	result, err := NewCapabilitiesTool(reg).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("capabilities failed: %v", err)
	}
	for _, want := range []string{"### Browser Automation", "**browser_task**", "**web_navigate**", "**web_page**", "**web_close**", "### Web & Network", "**agent_reach**", "**social_trending**", "**platform_search**"} {
		if !strings.Contains(result, want) {
			t.Fatalf("capabilities output missing %q:\n%s", want, result)
		}
	}
}

func TestCapabilitiesToolIsCoreTool(t *testing.T) {
	if !CoreTools["capabilities"] {
		t.Fatal("capabilities should be a core tool so agents can inspect available abilities without tool_detail")
	}
}
