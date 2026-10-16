package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type presentationBudgetTestTool struct {
	name  string
	count int
}

func (t *presentationBudgetTestTool) Name() string        { return t.name }
func (t *presentationBudgetTestTool) Description() string { return "presentation budget test tool" }
func (t *presentationBudgetTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": true,
	}
}
func (t *presentationBudgetTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	t.count++
	return "status: success\ncompleted", nil
}

func presentationBudgetToolCallJSON(t *testing.T, id, name string, arguments map[string]interface{}) string {
	t.Helper()
	args, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{map[string]interface{}{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": string(args),
					},
				}},
			},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPresentationDeliveryReservationKeepsMostBudgetForBuildingAndQA(t *testing.T) {
	tests := []struct {
		maxIterations int
		wantRemaining int
	}{
		{maxIterations: 45, wantRemaining: 30},
		{maxIterations: 30, wantRemaining: 20},
		{maxIterations: 10, wantRemaining: 6},
		{maxIterations: 6, wantRemaining: 5},
		{maxIterations: 1, wantRemaining: 0},
	}
	for _, tt := range tests {
		if got := presentationDeliveryReservationRemaining(tt.maxIterations); got != tt.wantRemaining {
			t.Fatalf("reservation for max=%d is %d, want %d", tt.maxIterations, got, tt.wantRemaining)
		}
	}
}

func TestPresentationOptionalQADoesNotConsumeFinalIteration(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "skill", "use_skill", map[string]interface{}{"skill_name": "ppt-art-director"})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "scaffold", "cli", map[string]interface{}{"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "read-builder", "read_file", map[string]interface{}{"path": `build_deck.py`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "customize-builder", "edit_file", map[string]interface{}{"path": `build_deck.py`, "old_string": "TASK_PAGE_RENDERERS = {}", "new_string": "TASK_PAGE_RENDERERS = {1: render_slide_01}"})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "build", "cli", map[string]interface{}{"command": `python build_deck.py --output report.pptx`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "audit", "cli", map[string]interface{}{"command": `python C:\runtime\scripts\audit_pptx.py report.pptx`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "render", "cli", map[string]interface{}{"command": `powershell -File C:\runtime\scripts\render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`})},
		// Pixel inspection is optional in the default mode. The final iteration
		// remains available for a useful delivery answer instead of forcing a
		// particular tool or failing the whole deck.
		{contentType: "application/json", body: completionJSON(t, "stop", "PPT 已生成：report.pptx")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 8)
	registry := NewToolRegistry()
	useSkill := &presentationBudgetTestTool{name: "use_skill"}
	cli := &presentationBudgetTestTool{name: "cli"}
	vision := &presentationBudgetTestTool{name: "vision"}
	registry.Register(useSkill)
	registry.Register(cli)
	registry.Register(vision)
	registry.Register(&presentationBudgetTestTool{name: "read_file"})
	registry.Register(&presentationBudgetTestTool{name: "edit_file"})
	agent.toolRegistry = registry

	output, err := agent.Generate(context.Background(), "Create a quick internal PPT draft; skip strict QA")
	if err != nil {
		t.Fatalf("Generate returned error: %v\n%s", err, output)
	}
	if vision.count != 0 {
		t.Fatalf("vision execution count = %d, want 0 for optional QA", vision.count)
	}
	if got := transport.requestCount(); got != 8 {
		t.Fatalf("model request count = %d, want 8", got)
	}
	if !strings.Contains(output, "PPT 已生成：report.pptx") || !strings.Contains(output, "建议打开后快速确认") {
		t.Fatalf("final output did not preserve the deck with a concise user-facing note: %s", output)
	}
	if transport.requestContains("FINAL PRESENTATION QA TOOL WINDOW") {
		t.Fatal("optional QA unexpectedly consumed the final tool window")
	}
}

func TestPresentationDeckWithoutRendererReturnsSuccessWithExplicitWarnings(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "skill", "use_skill", map[string]interface{}{"skill_name": "ppt-art-director"})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "scaffold", "cli", map[string]interface{}{"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "read-builder", "read_file", map[string]interface{}{"path": `build_deck.py`})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "customize-builder", "edit_file", map[string]interface{}{"path": `build_deck.py`, "old_string": "TASK_PAGE_RENDERERS = {}", "new_string": "TASK_PAGE_RENDERERS = {1: render_slide_01}"})},
		{contentType: "application/json", body: presentationBudgetToolCallJSON(t, "build", "cli", map[string]interface{}{"command": `python build_deck.py --output report.pptx`})},
		{contentType: "application/json", body: completionJSON(t, "stop", "我已经逐页检查，所有页面布局正常、无重叠。")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 6)
	registry := NewToolRegistry()
	registry.Register(&presentationBudgetTestTool{name: "use_skill"})
	registry.Register(&presentationBudgetTestTool{name: "cli"})
	registry.Register(&presentationBudgetTestTool{name: "read_file"})
	registry.Register(&presentationBudgetTestTool{name: "edit_file"})
	agent.toolRegistry = registry

	output, err := agent.Generate(context.Background(), "Create a quick internal PPT draft; skip strict QA")
	if err != nil {
		t.Fatalf("missing optional renderer must not fail a generated deck: %v\n%s", err, output)
	}
	for _, want := range []string{"PPT 已生成：report.pptx", "文件可以下载和继续编辑", "建议打开后快速确认"} {
		if !strings.Contains(output, want) {
			t.Fatalf("delivery note missing %q: %s", want, output)
		}
	}
	for _, internal := range []string{"验收状态：部分验证", "结构完整性验证证据", "像素级视觉检查证据"} {
		if strings.Contains(output, internal) {
			t.Fatalf("internal QA diagnostic leaked into user output: %s", output)
		}
	}
	if strings.Contains(output, "我已经逐页检查") {
		t.Fatalf("unsupported model QA claim leaked into the accepted final output: %s", output)
	}
}
