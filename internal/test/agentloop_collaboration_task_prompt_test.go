package app

import (
	"strings"
	"testing"
)

func TestSequentialCollaborationPromptsIncludeAssignedResponsibility(t *testing.T) {
	e := &MultiAgentEngine{}
	first := e.buildFirstAgentPrompt("Researcher", "制定新品上市方案", "只负责市场与客群假设")
	if !strings.Contains(first, "只负责市场与客群假设") {
		t.Fatalf("first member prompt lost task description: %s", first)
	}

	previous := []TaskResult{{AgentName: "Researcher", Status: Completed, Output: "市场假设成果"}}
	middle := e.buildSequentialAgentPrompt("Developer", "制定新品上市方案", previous, 2, 3, "只负责财务测算")
	for _, want := range []string{"只负责财务测算", "市场假设成果", "intermediate hand-off"} {
		if !strings.Contains(middle, want) {
			t.Fatalf("middle member prompt missing %q: %s", want, middle)
		}
	}
	if strings.Contains(middle, "Your output will be the FINAL answer") {
		t.Fatalf("middle member must not be told its output is final: %s", middle)
	}

	final := e.buildSequentialAgentPrompt("Assistant", "制定新品上市方案", previous, 3, 3, "汇总最终执行方案")
	for _, want := range []string{"汇总最终执行方案", "FINAL answer sent to the user"} {
		if !strings.Contains(final, want) {
			t.Fatalf("final member prompt missing %q: %s", want, final)
		}
	}
}

func TestParallelCollaborationPromptIncludesAssignedResponsibility(t *testing.T) {
	e := &MultiAgentEngine{}
	prompt := e.buildParallelAgentPrompt("Finance", "制定新品上市方案", "只负责三情景财务模型")
	for _, want := range []string{"制定新品上市方案", "只负责三情景财务模型", "Work only on your assigned responsibility"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("parallel prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildSkippedSequentialResultsKeepsConfiguredTeamVisible(t *testing.T) {
	tasks := []TaskConfig{
		{TaskID: "finance", Agent: "Developer"},
		{TaskID: "lead", Agent: "Assistant"},
	}
	results := buildSkippedSequentialResults(tasks, "market")
	if len(results) != 2 {
		t.Fatalf("got %d skipped results, want 2", len(results))
	}
	for i, result := range results {
		if result.Status != Failed || result.TaskID != tasks[i].TaskID || !strings.Contains(result.Error, "market") {
			t.Fatalf("unexpected skipped result %d: %+v", i, result)
		}
	}
}

func TestCollaborationPartialSuccessMapsToGatewayError(t *testing.T) {
	partial := map[string]interface{}{
		"status":           "partial_success",
		"successful_tasks": 1,
		"failed_tasks":     2,
	}
	err := collaborationResultError(partial)
	if err == nil || !strings.Contains(err.Error(), "1 successful") || !strings.Contains(err.Error(), "2 failed") {
		t.Fatalf("partial collaboration must become a truthful gateway error, got %v", err)
	}
	if err := collaborationResultError(map[string]interface{}{"status": "success"}); err != nil {
		t.Fatalf("successful collaboration must stay successful: %v", err)
	}
}
