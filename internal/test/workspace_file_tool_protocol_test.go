package app

import (
	"os"
	"strings"
	"testing"

	"mac/internal/toolcontract"
)

func TestSystemPromptsAdvertiseStructuralJSONAndBoundedWrites(t *testing.T) {
	prompts := map[string]string{
		"static":        systemPromptStatic(true),
		"agent_static":  (&LLMAgent{}).buildSystemPromptStatic(),
		"legacy_prompt": (&LLMAgent{}).buildSystemPrompt(),
	}
	for name, prompt := range prompts {
		for _, want := range []string{
			"json_patch for structural changes to existing JSON",
			"emit path before content",
			"6,000 characters",
			"Never repeat a pathless full payload",
			"never let the host guess a missing path",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s missing file protocol %q", name, want)
			}
		}
	}
}

func TestExplicitAbsoluteLocalPathGuidanceOverridesWorkspaceAssumptions(t *testing.T) {
	path := `E:\产品\OPC-demans`
	guidance := explicitLocalPathGuidance("请研究 " + path + " 目录里的材料")
	for _, want := range []string{
		path,
		"pass each exact path directly to list_dir",
		"do not infer a sandbox denial merely because a path is outside the workspace",
		"shell_analyze does not execute commands",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("explicit-path guidance missing %q: %s", want, guidance)
		}
	}
	if got := explicitLocalPathGuidance("请研究 workspace/docs 里的材料"); got != "" {
		t.Fatalf("relative path unexpectedly received absolute-path guidance: %s", got)
	}
	quotedPath := `E:\产品资料\OPC demans\竞标方案.docx`
	quotedGuidance := explicitLocalPathGuidance(`请分析 "` + quotedPath + `"`)
	if !strings.Contains(quotedGuidance, quotedPath) {
		t.Fatalf("quoted absolute path with spaces was truncated: %s", quotedGuidance)
	}
	if strings.Contains(quotedGuidance, "- `E:\\产品资料\\OPC`\n") {
		t.Fatalf("quoted absolute path emitted a truncated duplicate: %s", quotedGuidance)
	}

	for name, prompt := range map[string]string{
		"static": systemPromptStatic(true),
		"legacy": (&LLMAgent{}).buildSystemPrompt(),
	} {
		for _, want := range []string{
			"pass that exact path directly to list_dir",
			"Being outside the current workspace is not itself proof of denial",
			"shell_analyze only analyzes a command; it never executes one",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt missing explicit-path safeguard %q", name, want)
			}
		}
	}
}

func TestJSONPatchIsCorePathScopedMutation(t *testing.T) {
	if !CoreTools["json_patch"] {
		t.Fatal("json_patch must expose its schema as a core tool")
	}
	tool := NewJSONPatchTool(t.TempDir())
	registry := NewToolRegistry()
	registry.Register(tool)
	call := ToolCall{Name: "json_patch", Arguments: map[string]interface{}{"path": "data.json"}}
	if got := toolcontract.ClassifyToolParallel(call.Name, call.Arguments, registry, nil); got != toolcontract.PathScopedParallel {
		t.Fatalf("json_patch parallel class = %v, want path-scoped", got)
	}
	if !toolCallMutatesDeliverable(call) {
		t.Fatal("json_patch must be classified as a deliverable mutation")
	}
}

func TestBasePPTUsesJSONPatchForExistingWorkflowRepairs(t *testing.T) {
	data, err := os.ReadFile("builtin/presentation/base-ppt/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"use `json_patch` with JSON Pointer paths",
		"Do not rewrite the entire workflow",
		"never append partial fragments to a JSON document",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base-ppt file recovery guidance missing %q", want)
		}
	}
}
