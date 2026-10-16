package pipeline

import (
	"strings"
	"testing"
)

func TestDeriveSlotContractAppliesDependenciesAndDomainRules(t *testing.T) {
	contract := DeriveSlotContract("wechat-article", SlotStepContractInput{
		Name:              "素材准备",
		Prompt:            "整理素材并确定文章方向。",
		HasSourceContract: true,
		OutputMinChars:    100,
		ArtifactPaths:     []string{"article.md"},
	}, 1, "task_brief", "任务受理与意图理解")

	if contract.ID != "step_01" || len(contract.DependsOn) != 1 || contract.DependsOn[0] != "task_brief" {
		t.Fatalf("unexpected slot identity or dependency: %#v", contract)
	}
	fields := strings.Join(contract.RequiredFields, "|")
	rules := strings.Join(contract.ValidationRules, "|")
	if !strings.Contains(fields, "事实与观点边界") || !strings.Contains(rules, "禁止自行随机选题") {
		t.Fatalf("domain rules were not applied: %#v", contract)
	}
}

func TestCloneSlotContractCopiesSlices(t *testing.T) {
	original := &PipelineSlotContract{ID: "step_01", RequiredFields: []string{"结论"}}
	clone := CloneSlotContract(original)
	clone.RequiredFields[0] = "证据"
	if original.RequiredFields[0] != "结论" {
		t.Fatalf("clone mutated configured contract: %#v", original)
	}
}

func TestAppendSlotInstructionDeclaresProtocol(t *testing.T) {
	got := AppendSlotInstruction("base", &PipelineSlotContract{
		ID: "step_01", TaskDescription: "分析材料", RequiredFields: []string{"证据", "结论"},
	}, "INPUT_STATUS: MISSING", "<!--PIPELINE_STOP-->")
	for _, want := range []string{"slot_id：step_01", "结论；证据", SlotFilledMarker, SlotMissingMarker, "INPUT_STATUS: MISSING", "<!--PIPELINE_STOP-->"} {
		if !strings.Contains(got, want) {
			t.Fatalf("slot prompt missing %q: %s", want, got)
		}
	}
}

func TestSlotArtifactPathsAndFillReceiptUseAuditedToolResults(t *testing.T) {
	paths := SlotArtifactPaths([]SlotToolCall{
		{Params: `{"output_path":".openkitty/storage/result.md"}`, Result: `{"html_path":"storage/result.html"}`},
		{Failed: true, Result: `{"output_path":".openkitty/storage/failed.md"}`},
	})
	if len(paths) != 2 || paths[0] != ".openkitty/storage/result.md" || paths[1] != ".openkitty/storage/result.html" {
		t.Fatalf("unexpected audited paths: %#v", paths)
	}

	receipt := BuildSlotFillRecord(SlotFillInput{
		Contract:              &PipelineSlotContract{ID: "step_01"},
		Status:                SlotFilled,
		CleanOutput:           "实际交付结果",
		ArtifactPaths:         paths,
		HasArtifactContract:   true,
		ArtifactContractPaths: []string{"result.md"},
		ResultAccepted:        true,
	})
	if receipt == nil || receipt.OutputSHA256 == "" || receipt.OutputChars == 0 || len(receipt.Checks) != 3 || !receipt.Checks[2].Passed {
		t.Fatalf("invalid fill receipt: %#v", receipt)
	}
}
