package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"
)

type requiredSkillRecordingTool struct {
	calls  []map[string]interface{}
	result string
	err    error
}

func (t *requiredSkillRecordingTool) Name() string { return "use_skill" }
func (t *requiredSkillRecordingTool) Description() string {
	return "activate one skill"
}
func (t *requiredSkillRecordingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{"type": "string"},
		},
		"required": []string{"skill_name"},
	}
}
func (t *requiredSkillRecordingTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	t.calls = append(t.calls, params)
	return t.result, t.err
}

func TestActivateRequiredPipelineSkillsUsesExactTopLevelArgument(t *testing.T) {
	tool := &requiredSkillRecordingTool{result: "运行时目录: .openkitty/runtime/builtin/example-skill\nskill instructions"}
	fullRegistry := NewToolRegistry()
	fullRegistry.Register(tool)
	executor := &PipelineExecutor{}
	execution := &PipelineExecution{
		ID:    "exec-required-skill",
		Steps: []StepResult{{Name: "respond", Revision: 1}},
	}
	registry := executor.buildStepRegistryFrom(fullRegistry, []string{"use_skill"}, execution, 0)
	step := &PipelineStep{
		Name:           "respond",
		Tools:          []string{"use_skill"},
		RequiredSkills: []string{"example-skill", " example-skill "},
	}

	prompt, err := executor.activateRequiredPipelineSkills(context.Background(), step, registry, `answer the task with "<Runtime Directory>/scripts/build.py"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("use_skill calls = %d, want 1", len(tool.calls))
	}
	if len(tool.calls[0]) != 1 || tool.calls[0]["skill_name"] != "example-skill" {
		t.Fatalf("use_skill params = %#v, want exact top-level skill_name", tool.calls[0])
	}
	if len(execution.Steps[0].ToolCalls) != 1 {
		t.Fatalf("audit tool calls = %#v, want one recorded activation", execution.Steps[0].ToolCalls)
	}
	record := execution.Steps[0].ToolCalls[0]
	if record.ToolName != "use_skill" || record.Params != `{"skill_name":"example-skill"}` || record.Status != "success" {
		t.Fatalf("required skill audit record = %#v", record)
	}
	for _, want := range []string{"answer the task", "Host-activated required skills", "example-skill", "skill instructions", `.openkitty/runtime/builtin/example-skill/scripts/build.py`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("activated prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "<Runtime Directory>") {
		t.Fatalf("portable runtime placeholder was not resolved after host activation: %s", prompt)
	}
}

func TestPipelineRequiredSkillRuntimeDirectorySupportsEnglishAndChineseLabels(t *testing.T) {
	for _, result := range []string{
		"运行时目录： .openkitty/runtime/builtin/ppt-art-director",
		"Runtime Directory: /tmp/runtime/ppt-art-director",
	} {
		if got := skilldomain.RuntimeDirectoryFromActivation(result); got == "" || strings.Contains(got, "Runtime Directory") || strings.Contains(got, "运行时目录") {
			t.Fatalf("runtime directory was not parsed from %q: %q", result, got)
		}
	}
}

func TestActivateRequiredPipelineSkillsFailsClosed(t *testing.T) {
	executor := &PipelineExecutor{}
	step := &PipelineStep{RequiredSkills: []string{"example-skill"}}
	if _, err := executor.activateRequiredPipelineSkills(context.Background(), step, NewToolRegistry(), "answer"); err == nil {
		t.Fatal("missing use_skill tool was accepted")
	}

	tool := &requiredSkillRecordingTool{err: errors.New("activation failed")}
	registry := NewToolRegistry()
	registry.Register(tool)
	if _, err := executor.activateRequiredPipelineSkills(context.Background(), step, registry, "answer"); err == nil {
		t.Fatal("failed skill activation was accepted")
	}
}
