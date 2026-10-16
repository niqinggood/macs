package app

import (
	"strings"
	"testing"
)

func TestProductManagerPipelineCoversPRDWBSQuotesAndBids(t *testing.T) {
	pipeline, ok := (&GatewayService{}).defaultPipelineSet()["product-management"]
	if !ok {
		t.Fatal("product-management pipeline missing")
	}
	wantSteps := []string{"需求基线", "PRD与项目拆分", "工期成本与报价", "标书与投标材料", "交付复核"}
	if len(pipeline.Steps) != len(wantSteps) {
		t.Fatalf("steps = %d, want %d", len(pipeline.Steps), len(wantSteps))
	}
	for i, want := range wantSteps {
		if pipeline.Steps[i].Name != want {
			t.Fatalf("step %d = %q, want %q", i, pipeline.Steps[i].Name, want)
		}
	}

	allPrompts := ""
	for _, step := range pipeline.Steps {
		allPrompts += "\n" + step.Prompt
	}
	for _, want := range []string{
		"project_prd_${execution_id}.md",
		"project_wbs_${execution_id}.md",
		"project_estimate_quote_${execution_id}.md",
		"project_estimate_quote_${execution_id}.csv",
		"project_bid_response_${execution_id}.md",
		"project_bid_draft_${execution_id}.md",
		"内部直接成本",
		"客户报价",
		"低/基准/高三档",
		"不得创建空模板",
		"严禁虚构资质",
	} {
		if !strings.Contains(allPrompts, want) {
			t.Fatalf("product-manager pipeline missing %q", want)
		}
	}
}

func TestRoleBasedSpecialistPoliciesMigrateWithBuiltInDefinitions(t *testing.T) {
	for _, id := range []string{"ppt-polish-specialist", "product-manager", "bid-specialist"} {
		if !isPolicyManagedBuiltInAIEmployee(id) {
			t.Fatalf("%s should use the product-managed built-in policy", id)
		}
	}

	tests := []struct {
		id     string
		name   string
		atName string
	}{
		{"ppt-polish-specialist", "PPT润色专员", "PPT润色"},
		{"product-manager", "产品经理", "产品经理"},
	}
	for _, tt := range tests {
		def, ok := policyManagedBuiltInAIEmployeeDefault(tt.id)
		if !ok || def.Name != tt.name || def.AtName != tt.atName {
			t.Fatalf("default %s = %#v, want name/mention %s", tt.id, def, tt.name)
		}
		stale := &AIEmployeeConfig{ID: tt.id, Name: "旧名称", AtName: "旧入口", Hidden: true}
		applyBuiltInAEPolicy(stale, &def)
		if stale.Name != tt.name || stale.AtName != tt.atName || stale.Hidden {
			t.Fatalf("migrated %s = %#v", tt.id, stale)
		}
	}
}

func TestProductManagerQuoteStagesEnforceDeterministicReconciliation(t *testing.T) {
	pipeline := (&GatewayService{}).defaultPipelineSet()["product-management"]
	if len(pipeline.Steps) != 5 {
		t.Fatalf("steps = %d, want 5", len(pipeline.Steps))
	}

	quote := pipeline.Steps[2]
	for _, want := range []string{
		"ROUND_UP_0_1_K",
		"client_quote_raw_krmb",
		"ceil(client_quote_raw_krmb * 10) / 10",
		"rounding_rule",
		"total_days",
	} {
		if !strings.Contains(quote.Prompt, want) {
			t.Fatalf("quote prompt missing %q", want)
		}
	}
	if !containsPipelineTool(quote.Tools, "persistent_shell") {
		t.Fatal("quote step must use persistent_shell for deterministic arithmetic")
	}

	review := pipeline.Steps[4]
	for _, want := range []string{
		"mismatch_count=0",
		"client_quote_raw_krmb",
		"ceil(client_quote_raw_krmb * 10) / 10",
		"100%",
	} {
		if !strings.Contains(review.Prompt, want) {
			t.Fatalf("review prompt missing %q", want)
		}
	}
	for _, tool := range []string{"read_file", "persistent_shell", "write_file"} {
		if !containsPipelineTool(review.Tools, tool) {
			t.Fatalf("review step missing tool %q", tool)
		}
	}
}

func containsPipelineTool(tools []string, want string) bool {
	for _, tool := range tools {
		if tool == want {
			return true
		}
	}
	return false
}
