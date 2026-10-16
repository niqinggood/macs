package app

import "testing"

func TestBrowserOperatorToolVisibility(t *testing.T) {
	core := []string{"browser_task", "web_targets", "web_navigate", "web_page", "web_close"}
	for _, name := range core {
		if !CoreTools[name] {
			t.Fatalf("%s should be a core tool so AI employees can operate browser sessions without tool_detail discovery", name)
		}
		if PromptHiddenTools[name] {
			t.Fatalf("%s should not be prompt-hidden", name)
		}
	}

	lowLevel := []string{"web_eval", "web_click", "web_scroll", "web_screenshot", "web_extract", "web_click_index", "web_input", "web_replay"}
	for _, name := range lowLevel {
		if CoreTools[name] {
			t.Fatalf("%s should stay behind web_page instead of bloating the core tool set", name)
		}
		if !PromptHiddenTools[name] {
			t.Fatalf("%s should be prompt-hidden; use web_page for normal browser interaction", name)
		}
	}
}

func TestBrowserOperatorReadOnlyClassification(t *testing.T) {
	readOnly := []string{"web_targets", "web_extract"}
	for _, name := range readOnly {
		if !isReadOnlyTool(name) {
			t.Fatalf("%s should be read-only", name)
		}
	}

	stateful := []string{"web_navigate", "web_page", "web_close"}
	for _, name := range stateful {
		if isReadOnlyTool(name) {
			t.Fatalf("%s should stay serial because it mutates browser state", name)
		}
	}
}
