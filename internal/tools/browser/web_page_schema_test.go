package browser

import "testing"

func TestMCPWebPageTargetIDIsOptional(t *testing.T) {
	schema := (&WebPageTool{}).Parameters()
	if schema == nil {
		t.Fatal("web_page tool schema not found")
	}

	required, _ := schema["required"].([]string)
	for _, name := range required {
		if name == "target_id" {
			t.Fatalf("web_page target_id should be optional in MCP schema: %v", required)
		}
	}
	hasAction := false
	for _, name := range required {
		if name == "action" {
			hasAction = true
		}
	}
	if !hasAction {
		t.Fatalf("web_page action should remain required: %v", required)
	}
}
