package session

import (
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

func TestCleanSessionCarryoverContentStripsAssistantInternals(t *testing.T) {
	content := "<think>hidden</think>\n\nFinal answer\n\nTool/Skill Review\nFailure statistics\nGuardrail\nlist_dir: 1 failure"

	got := CleanCarryoverContent("assistant", content, 500)
	if strings.Contains(got, "<think>") || strings.Contains(got, "Guardrail") || strings.Contains(got, "Failure statistics") {
		t.Fatalf("internal content leaked: %q", got)
	}
	if !strings.Contains(got, "Final answer") {
		t.Fatalf("final answer was lost: %q", got)
	}
}

func TestCleanSessionCarryoverContentDropsAutoVerificationPrompt(t *testing.T) {
	content := "Auto verification required\nOriginal user request:\nmerge these PNGs\nUse `verify_work` with evidence."

	if got := CleanCarryoverContent("assistant", content, 500); got != "" {
		t.Fatalf("auto verification prompt should be dropped, got %q", got)
	}
}

func TestCleanSessionCarryoverContentDropsSubAgentInternals(t *testing.T) {
	prompt := `You are a sub-agent named "verification_76100" working on a delegated task.

## Parent Task Context
Verify the following work independently.

## Important Rules for Sub-Agents
Use report_progress during your task.`
	if got := CleanCarryoverContent("user", prompt, 500); got != "" {
		t.Fatalf("sub-agent prompt should be dropped, got %q", got)
	}

	result := "## Sub-Agent 'verification_76100' Result\n\nVERDICT: PASS"
	if got := CleanCarryoverContent("assistant", result, 500); got != "" {
		t.Fatalf("sub-agent result should be dropped, got %q", got)
	}
}

func TestCleanSessionCarryoverContentStripsRuntimeContextPrefix(t *testing.T) {
	content := "## Recent Context (from memory)\nold fact\n\n## User Request\n到chatroom，对近期帖子回帖"

	got := CleanCarryoverContent("user", content, 500)
	if strings.Contains(got, "Recent Context") || !strings.Contains(got, "到chatroom") {
		t.Fatalf("runtime prefix should be stripped, got %q", got)
	}
}

func TestMemoryDeriverSummaryFiltersInternalContext(t *testing.T) {
	deriver := memorycore.NewDeriver(memorycore.DeriverDependencies{
		CleanContent: CleanCarryoverContent,
	})
	got := deriver.BuildConversationSummary(
		"You are a sub-agent named \"verification_1\" working on a delegated task.\n\n## Parent Task Context\nverify",
		"## Sub-Agent 'verification_1' Result\n\nVERDICT: PASS",
		[]map[string]interface{}{
			{"role": "user", "content": "## Recent Context (from memory)\nold\n\n## User Request\n真正的问题"},
			{"role": "assistant", "content": "<think>hidden</think>\n\n真正回答"},
		},
	)
	if strings.Contains(got, "sub-agent") || strings.Contains(got, "Recent Context") || strings.Contains(got, "<think>") {
		t.Fatalf("internal context leaked into deriver summary: %q", got)
	}
	if !strings.Contains(got, "真正的问题") || !strings.Contains(got, "真正回答") {
		t.Fatalf("visible context missing from deriver summary: %q", got)
	}
}

func TestBuildSessionContextPromptKeepsArtifactsAndCleanDialogue(t *testing.T) {
	messages := []SessionContextChatMessage{
		{Role: "assistant", Content: "Created .openkitty/storage/doubao_pdf_pages/doubao_pages_side_by_side.png\n<think>hidden</think>"},
		{Role: "assistant", Content: "Auto verification required\nOriginal user request:\nUse `verify_work` with evidence."},
		{Role: "user", Content: "please put the two png files side by side"},
	}

	got := BuildSessionContextPromptFromMessages(messages)
	if !strings.Contains(got, "please put the two png files side by side") {
		t.Fatalf("user context missing: %q", got)
	}
	if !strings.Contains(got, ".openkitty/storage/doubao_pdf_pages/doubao_pages_side_by_side.png") {
		t.Fatalf("artifact path missing: %q", got)
	}
	if strings.Contains(got, "Auto verification") || strings.Contains(got, "<think>") {
		t.Fatalf("internal content leaked: %q", got)
	}
}
