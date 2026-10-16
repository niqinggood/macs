package app

import (
	"strings"
	"testing"
)

func TestWebUsesOneAgentModeToggleButton(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	appJS := mustReadWebTestFile(t, "web/app.js")

	if strings.Count(indexHTML, `class="mode-btn`) != 1 {
		t.Fatal("single-agent and multi-agent mode must share exactly one toggle button")
	}
	for _, marker := range []string{
		`id="modeToggleBtn"`,
		`onclick="toggleAgentMode()"`,
		`id="modeToggleLabel"`,
		`/web/app.js?v=`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("agent mode toggle UI missing %q", marker)
		}
	}
	for _, retired := range []string{
		`onclick="switchMode('single')"`,
		`onclick="switchMode('collab')"`,
	} {
		if strings.Contains(indexHTML, retired) {
			t.Fatalf("legacy separate agent mode button remains: %q", retired)
		}
	}
	for _, marker := range []string{
		"function toggleAgentMode()",
		"switchMode(currentMode === 'single' ? 'collab' : 'single')",
		"label.textContent = isCollab ? '多Agent' : '单Agent'",
		"button.setAttribute('aria-pressed', isCollab ? 'true' : 'false')",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("agent mode toggle behavior missing %q", marker)
		}
	}
}

func TestWebDefaultsToAssistAgentWithoutOverwritingASelection(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	chatJS := mustReadWebTestFile(t, "web/chat.js")

	for _, marker := range []string{
		"function preferredAgentName(agentList)",
		"normalizedName === 'assist'",
		"normalizedName.indexOf('assist') === 0",
		"previousAgentExists ? previousAgent : preferredAgentName(agents)",
		`/web/chat.js?v=`,
	} {
		source := chatJS
		if strings.HasPrefix(marker, "/web/") {
			source = indexHTML
		}
		if !strings.Contains(source, marker) {
			t.Fatalf("assist default-agent behavior missing %q", marker)
		}
	}
}
