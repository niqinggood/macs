package app

import (
	"context"
	"testing"
)

func TestNormalizeLLMResponseRepairsUnambiguousShellAlias(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(registryNameMockTool{name: "cli"})

	calls := []ToolCall{{ID: "call_shell", Name: "shell", Arguments: map[string]interface{}{"command": "ls -la", "reason": "inspect target"}}}
	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Name != "cli" {
		t.Fatalf("shell alias was not repaired to advertised cli tool: %#v", fixed)
	}

	malformed := []ToolCall{{ID: "call_shell_bad", Name: "shell", Arguments: map[string]interface{}{"query": "ls -la"}}}
	_, untouched := normalizeLLMResponse("", malformed, registry)
	if len(untouched) != 1 || untouched[0].Name != "shell" {
		t.Fatalf("ambiguous shell-shaped call must remain untouched: %#v", untouched)
	}
}

func TestNormalizeLLMResponseUnwrapsSchemaValidParametersEnvelope(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(normalizationSchemaTool{})

	calls := []ToolCall{{
		ID:   "wrapped",
		Name: "normalization_schema",
		Arguments: map[string]interface{}{
			"parameters": map[string]interface{}{
				"skill_name": "ppt-art-director",
				"task":       "build the deck",
			},
		},
	}}
	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Arguments["skill_name"] != "ppt-art-director" || fixed[0].Arguments["task"] != "build the deck" {
		t.Fatalf("valid parameters envelope was not unwrapped: %#v", fixed)
	}
	if _, remains := fixed[0].Arguments["parameters"]; remains {
		t.Fatalf("parameters envelope remains after repair: %#v", fixed[0].Arguments)
	}
}

func TestNormalizeLLMResponseDoesNotGuessInvalidParametersEnvelope(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(normalizationSchemaTool{})

	tests := []ToolCall{
		{Name: "normalization_schema", Arguments: map[string]interface{}{"parameters": map[string]interface{}{"skill_name": "x"}}},
		{Name: "normalization_schema", Arguments: map[string]interface{}{"parameters": map[string]interface{}{"skill_name": "x", "task": "y", "unknown": true}}},
		{Name: "normalization_schema", Arguments: map[string]interface{}{"skill_name": "top", "parameters": map[string]interface{}{"skill_name": "nested", "task": "y"}}},
	}
	for _, call := range tests {
		_, fixed := normalizeLLMResponse("", []ToolCall{call}, registry)
		if len(fixed) != 1 {
			t.Fatalf("tool call unexpectedly removed: %#v", fixed)
		}
		if _, remains := fixed[0].Arguments["parameters"]; !remains {
			t.Fatalf("ambiguous/invalid envelope must remain for ordinary validation: %#v", fixed[0].Arguments)
		}
	}
}

func TestNormalizeLLMResponseLiftsRequiredFieldFromLegitimateParametersObject(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(normalizationUseSkillTool{})
	calls := []ToolCall{{
		Name: "normalization_use_skill",
		Arguments: map[string]interface{}{
			"parameters": map[string]interface{}{
				"skill_name": "ppt-art-director",
				"task":       "build the deck",
			},
		},
	}}
	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Arguments["skill_name"] != "ppt-art-director" {
		t.Fatalf("required skill_name was not lifted: %#v", fixed)
	}
	parameters, ok := fixed[0].Arguments["parameters"].(map[string]interface{})
	if !ok || parameters["task"] != "build the deck" {
		t.Fatalf("remaining legitimate skill parameters were not preserved: %#v", fixed[0].Arguments)
	}
	if _, duplicated := parameters["skill_name"]; duplicated {
		t.Fatalf("lifted field still duplicated inside parameters: %#v", parameters)
	}
}

func TestNormalizeLLMResponseRepairsEditFilePathAlias(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewEditFileTool(t.TempDir()))
	calls := []ToolCall{{
		Name: "edit_file",
		Arguments: map[string]interface{}{
			"path":       "notes.md",
			"old_string": "before",
			"new_string": "after",
		},
	}}

	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Arguments["file_path"] != "notes.md" {
		t.Fatalf("edit_file path alias was not repaired: %#v", fixed)
	}
	if _, remains := fixed[0].Arguments["path"]; remains {
		t.Fatalf("path alias remains after repair: %#v", fixed[0].Arguments)
	}
}

func TestNormalizeLLMResponseRepairsWatchFileAsVisionInspection(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(registryNameMockTool{name: "watch"})
	registry.Register(NewVisionTool("", "", ""))
	calls := []ToolCall{{
		ID:   "inspect_render",
		Name: "watch",
		Arguments: map[string]interface{}{
			"action":   "analyze",
			"file":     ".openkitty/storage/rendered_pages/contact_sheet.jpg",
			"question": "inspect every slide for overlap and clipping",
		},
	}}

	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Name != "vision" {
		t.Fatalf("watch(file=image) was not repaired to vision: %#v", fixed)
	}
	if fixed[0].Arguments["path"] != ".openkitty/storage/rendered_pages/contact_sheet.jpg" ||
		fixed[0].Arguments["question"] != "inspect every slide for overlap and clipping" {
		t.Fatalf("vision arguments were not canonicalized: %#v", fixed[0].Arguments)
	}
	if _, leaked := fixed[0].Arguments["action"]; leaked {
		t.Fatalf("watch-only arguments leaked into vision: %#v", fixed[0].Arguments)
	}
}

func TestNormalizeLLMResponseLeavesRealWatchAndNonImageFilesUntouched(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(registryNameMockTool{name: "watch"})
	registry.Register(NewVisionTool("", "", ""))
	tests := []ToolCall{
		{Name: "watch", Arguments: map[string]interface{}{"action": "screenshot", "analyze": true}},
		{Name: "watch", Arguments: map[string]interface{}{"file": "notes.txt"}},
	}
	for _, call := range tests {
		_, fixed := normalizeLLMResponse("", []ToolCall{call}, registry)
		if len(fixed) != 1 || fixed[0].Name != "watch" {
			t.Fatalf("legitimate/ambiguous watch call was rewritten: %#v", fixed)
		}
	}
}

func TestNormalizeLLMResponseDoesNotOverrideExplicitEditFilePath(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewEditFileTool(t.TempDir()))
	calls := []ToolCall{{
		Name: "edit_file",
		Arguments: map[string]interface{}{
			"path":       "alias.md",
			"file_path":  "canonical.md",
			"old_string": "before",
			"new_string": "after",
		},
	}}

	_, fixed := normalizeLLMResponse("", calls, registry)
	if len(fixed) != 1 || fixed[0].Arguments["file_path"] != "canonical.md" || fixed[0].Arguments["path"] != "alias.md" {
		t.Fatalf("explicit edit_file file_path must remain authoritative: %#v", fixed)
	}
}

type normalizationSchemaTool struct{}

func (normalizationSchemaTool) Name() string        { return "normalization_schema" }
func (normalizationSchemaTool) Description() string { return "test" }
func (normalizationSchemaTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{"type": "string"},
			"task":       map[string]interface{}{"type": "string"},
		},
		"required": []string{"skill_name", "task"},
	}
}
func (normalizationSchemaTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "", nil
}

type normalizationUseSkillTool struct{}

func (normalizationUseSkillTool) Name() string        { return "normalization_use_skill" }
func (normalizationUseSkillTool) Description() string { return "test" }
func (normalizationUseSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{"type": "string"},
			"parameters": map[string]interface{}{"type": "object"},
		},
		"required": []string{"skill_name"},
	}
}
func (normalizationUseSkillTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "", nil
}
