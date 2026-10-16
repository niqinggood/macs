package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
	"mac/internal/toolcontract"
	"mac/internal/toolregistry"
)

func TestMemoryDisabledStopsContextAndRegistryTools(t *testing.T) {
	workspace := t.TempDir()
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	if err := scm.SetDurable("preference", "private preference", "preference", 5); err != nil {
		t.Fatal(err)
	}
	agent := &LLMAgent{workspace: workspace, scm: scm, hasMemory: false}
	if prompt := agent.buildSessionContextPrompt("one", 10); prompt != "" {
		t.Fatalf("disabled memory injected context: %q", prompt)
	}
	store := memorycore.NewAgentMemoryStore(workspace, "assistant")
	if err := store.Remember("key", "private value", memorycore.MemoryUser, 5, nil, "one"); err != nil {
		t.Fatal(err)
	}
	registry := toolregistry.New(toolregistry.Dependencies{})
	registry.Register(memorycore.NewAgentMemoryToolWithAccess(store, "", agent.allowMemoryTool))
	registry.Register(memorycore.NewSessionContextToolWithAccess(scm, agent.allowMemoryTool))
	ctx := context.WithValue(context.Background(), "session_id", "one")
	for _, name := range []string{"agent_memory", "session_context"} {
		result, err := registry.Execute(ctx, toolcontract.Call{ID: "test", Name: name, Arguments: map[string]interface{}{"action": "list"}})
		if err != nil || !strings.Contains(result.Content, "disabled") || strings.Contains(result.Content, "private") || result.ExecutionSucceeded == nil || *result.ExecutionSucceeded {
			t.Fatalf("disabled tool result: %#v, %v", result, err)
		}
	}
	result, err := registry.Execute(ctx, toolcontract.Call{ID: "delete", Name: "agent_memory", Arguments: map[string]interface{}{"action": "forget", "scope": "user", "key": "key"}})
	if err != nil || !strings.Contains(result.Content, "Forgotten") || store.Recall("key") != "" {
		t.Fatalf("disabled memory removal: %#v, %v", result, err)
	}
	agent.hasMemory = true
	isolated := context.WithValue(ctx, "pipeline_isolated_context", true)
	if agent.allowMemoryTool(isolated, false) || agent.allowMemoryTool(isolated, true) {
		t.Fatal("isolated pipeline gained memory access")
	}
}

func TestKnowledgeMemoryRequiresEnabledAgentAndMatchingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(workspace, ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	previous := globalKG
	globalKG = kg
	defer func() { globalKG = previous }()
	if err := kg.AddTriple("Project", "uses", "scoped knowledge", "manual", "", "", 1); err != nil {
		t.Fatal(err)
	}
	agent := &LLMAgent{workspace: workspace, hasMemory: false}
	ctx := context.Background()
	if prompt := agent.buildKnowledgeMemoryPrompt(ctx, "one"); prompt != "" {
		t.Fatal("disabled agent read graph")
	}
	agent.hasMemory = true
	if prompt := agent.buildKnowledgeMemoryPrompt(ctx, "one"); !strings.Contains(prompt, "scoped knowledge") {
		t.Fatalf("enabled agent cannot recall local graph: %q", prompt)
	}
	agent.workspace = t.TempDir()
	if prompt := agent.buildKnowledgeMemoryPrompt(ctx, "one"); prompt != "" {
		t.Fatal("foreign workspace read global graph")
	}
	agent.workspace = workspace
	isolated := context.WithValue(ctx, "pipeline_isolated_context", true)
	if prompt := agent.buildKnowledgeMemoryPrompt(isolated, "one"); prompt != "" {
		t.Fatal("isolated pipeline read global graph")
	}
}
