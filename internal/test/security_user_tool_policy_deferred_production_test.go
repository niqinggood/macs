package app

import "testing"

func TestUserToolPolicyTreatsDeferredProductionAsCurrentTurnBoundary(t *testing.T) {
	request := "请先判断这些约束能否同时满足；只给我两个取舍方案和推荐，不要开始制作。"
	policy := deriveUserToolPolicy(request)
	if policy.ForbidAllTools || policy.ForbidMutations || !policy.ForbidFileMutations {
		t.Fatalf("deferred-production policy = %#v, want file mutations forbidden only", policy)
	}

	for _, call := range []ToolCall{
		{Name: "write_file"},
		{Name: "convert"},
		{Name: "notepad", Arguments: map[string]interface{}{"action": "append", "content": "draft"}},
		{Name: "cli", Arguments: map[string]interface{}{"command": "mkdir output"}},
	} {
		if blocked, _ := policy.blocks(call, nil); !blocked {
			t.Errorf("deferred-production request did not block %#v", call)
		}
	}
	if blocked, reason := policy.blocks(ToolCall{Name: "read_file"}, nil); blocked {
		t.Fatalf("read-only inspection was blocked: %s", reason)
	}
}

func TestUserToolPolicyAllowsProductionAfterExplicitFollowUp(t *testing.T) {
	request := "选择推荐方案。现在可以开始，请在 runs/A23 生成 PPT、Excel 和验收说明。"
	if policy := deriveUserToolPolicy(request); policy.ForbidFileMutations {
		t.Fatalf("explicit production follow-up remained blocked: %#v", policy)
	}
}

func TestUserToolPolicyRecognizesEnglishDeferredImplementation(t *testing.T) {
	policy := deriveUserToolPolicy("Give me two trade-off options; do not start implementation yet.")
	if !policy.ForbidFileMutations {
		t.Fatalf("English deferred implementation was not recognized: %#v", policy)
	}
}

func TestUserToolPolicyDoesNotLetOneNegativeAlternativeCancelAuthorizedProduction(t *testing.T) {
	request := "不要生成 PDF，请生成一份新的 PPTX。"
	if policy := deriveUserToolPolicy(request); policy.ForbidFileMutations {
		t.Fatalf("negative alternative overrode the authorized deliverable: %#v", policy)
	}
}
