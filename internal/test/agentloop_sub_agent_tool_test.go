package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type blockingSubAgent struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (agent *blockingSubAgent) Name() string       { return "blocking" }
func (agent *blockingSubAgent) GetType() AgentType { return AgentTypeCustom }
func (agent *blockingSubAgent) Generate(ctx context.Context, _ string) (string, error) {
	close(agent.started)
	if agent.finished != nil {
		defer close(agent.finished)
	}
	select {
	case <-agent.release:
		return "background worker done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestSubAgentToolLoadsBuiltinVerificationDefinition(t *testing.T) {
	workspace := t.TempDir()
	tool := NewSubAgentTool(nil, workspace, log.New(os.Stdout, "", 0), nil)

	def, ok := tool.resolveDefinition("verification", "")
	if !ok {
		t.Fatal("verification definition should be available")
	}
	if def.Name != "verification" {
		t.Fatalf("unexpected definition: %#v", def)
	}
	if len(def.Tools) == 0 || len(def.DisallowedTools) == 0 {
		t.Fatalf("verification definition missing tool policy: %#v", def)
	}
}

func TestSubAgentToolFileDefinitionOverridesBuiltin(t *testing.T) {
	workspace := t.TempDir()
	agentsDir := filepath.Join(workspace, ".openkitty", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "verification.yaml"), []byte(`name: verification
description: custom verifier
prompt: custom prompt
tools:
  - read_file
disallowed_tools:
  - cli
max_turns: 3
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewSubAgentTool(nil, workspace, log.New(os.Stdout, "", 0), nil)
	def, ok := tool.resolveDefinition("verification", "")
	if !ok {
		t.Fatal("verification definition should be available")
	}
	if def.SystemPrompt != "custom prompt" || def.MaxTurns != 3 {
		t.Fatalf("file definition should override builtin, got %#v", def)
	}
	if len(def.Tools) != 1 || def.Tools[0] != "read_file" {
		t.Fatalf("unexpected tools: %#v", def.Tools)
	}
}

func TestParallelSubAgentToolParametersIncludeAgentType(t *testing.T) {
	tool := NewParallelSubAgentTool(nil, log.New(os.Stdout, "", 0))
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	tasks := props["tasks"].(map[string]interface{})
	items := tasks["items"].(map[string]interface{})
	itemProps := items["properties"].(map[string]interface{})
	if _, ok := itemProps["agent_type"]; !ok {
		t.Fatalf("parallel_sub_agent task schema should expose agent_type: %#v", itemProps)
	}
	maxIterations := itemProps["max_iterations"].(map[string]interface{})
	if maximum := maxIterations["maximum"]; maximum != 150 {
		t.Fatalf("parallel_sub_agent max_iterations maximum=%v, want 150", maximum)
	}
}

func TestSubAgentOutputContractDoesNotInventFileDeliverables(t *testing.T) {
	for _, required := range []string{
		"If and only if the delegated task explicitly requires",
		"For response-only research, analysis, review, planning, or drafting, do not create or modify files",
		"Do not invent a destination path",
	} {
		if !strings.Contains(subAgentOutputContract, required) {
			t.Fatalf("sub-agent output contract is missing %q: %s", required, subAgentOutputContract)
		}
	}
}

func TestSubAgentProgressReportingIsOptionalForBoundedAndVerificationWork(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(explicitObligationTestTool{name: "report_progress"})

	tests := []struct {
		name       string
		agentType  string
		iterations int
	}{
		{name: "two turn worker", agentType: "research", iterations: 2},
		{name: "verification worker", agentType: "verification", iterations: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guidance := subAgentProgressReportingGuidance(tt.agentType, tt.iterations)
			if obligations := extractExplicitToolObligations(guidance, registry); len(obligations) != 0 {
				t.Fatalf("optional progress guidance created a hard tool obligation: %#v\n%s", obligations, guidance)
			}
		})
	}
}

func TestSubAgentProgressReportingRemainsRequiredForOrdinaryLongWork(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(explicitObligationTestTool{name: "report_progress"})

	guidance := subAgentProgressReportingGuidance("research", 8)
	obligations := extractExplicitToolObligations(guidance, registry)
	if len(obligations) != 1 || obligations[0].Name != "report_progress" {
		t.Fatalf("long-running progress guidance lost its heartbeat obligation: %#v\n%s", obligations, guidance)
	}
}

func TestSubAgentToolBackgroundExecutionAndStatus(t *testing.T) {
	worker := &blockingSubAgent{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	factory := &AgentFactory{
		testSubAgentCreator: func(AgentConfig, string) (Agent, error) {
			return worker, nil
		},
	}
	tool := NewSubAgentTool(factory, t.TempDir(), nil, nil)
	statusTool := NewAgentStatusTool(tool)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"task":       "inspect the repository and report relevant findings",
		"agent_name": "researcher",
		"background": true,
	})
	if err != nil {
		t.Fatalf("start background sub-agent: %v", err)
	}
	if !strings.Contains(result, "agent_status") {
		t.Fatalf("background response should explain status retrieval: %s", result)
	}

	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background worker did not start")
	}
	running, err := statusTool.Execute(context.Background(), map[string]interface{}{
		"agent_name": "researcher",
	})
	if err != nil {
		t.Fatalf("read running status: %v", err)
	}
	if !strings.Contains(running, `"status": "running"`) {
		t.Fatalf("expected running status, got: %s", running)
	}

	close(worker.release)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		statuses := tool.StatusSnapshot("researcher", true)
		if len(statuses) == 1 && statuses[0].Status == "completed" {
			if !strings.Contains(statuses[0].Result, "background worker done") {
				t.Fatalf("completed result was not retained: %+v", statuses[0])
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("background worker did not complete: %+v", statuses)
		case <-ticker.C:
		}
	}

	completed, err := statusTool.Execute(context.Background(), map[string]interface{}{
		"agent_name":     "researcher",
		"include_result": true,
	})
	if err != nil {
		t.Fatalf("read completed status: %v", err)
	}
	if !strings.Contains(completed, `"status": "completed"`) ||
		!strings.Contains(completed, "background worker done") {
		t.Fatalf("completed status/result missing: %s", completed)
	}
	if !statusTool.IsReadOnly() {
		t.Fatal("agent_status must be read-only")
	}
}

func TestSubAgentToolParametersExposeBackground(t *testing.T) {
	tool := NewSubAgentTool(nil, t.TempDir(), nil, nil)
	properties := tool.Parameters()["properties"].(map[string]interface{})
	if _, ok := properties["background"]; !ok {
		t.Fatalf("sub_agent schema should expose background: %#v", properties)
	}
}

func TestSubAgentToolStopPreservesStoppedStatus(t *testing.T) {
	worker := &blockingSubAgent{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	factory := &AgentFactory{
		testSubAgentCreator: func(AgentConfig, string) (Agent, error) {
			return worker, nil
		},
	}
	tool := NewSubAgentTool(factory, t.TempDir(), nil, nil)
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"task":       "inspect the implementation until cancellation is requested",
		"agent_name": "cancelled-reviewer",
		"background": true,
	}); err != nil {
		t.Fatalf("start background sub-agent: %v", err)
	}
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background worker did not start")
	}
	if err := tool.KillSubAgent("cancelled-reviewer"); err != nil {
		t.Fatalf("stop background sub-agent: %v", err)
	}
	select {
	case <-worker.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled background worker did not exit")
	}

	statuses := tool.StatusSnapshot("cancelled-reviewer", true)
	if len(statuses) != 1 || statuses[0].Status != "stopped" {
		t.Fatalf("stop status should survive worker cancellation: %+v", statuses)
	}
	if statuses[0].EndedAt.IsZero() {
		t.Fatalf("stopped session should record an end time: %+v", statuses[0])
	}
}
