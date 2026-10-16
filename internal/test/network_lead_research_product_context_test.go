package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestLeadResearchProductSourceContextHasNoBuiltInProductSpecialCases(t *testing.T) {
	exec := &PipelineExecution{
		PipelineName: "lead-research",
		RuntimePipeline: &Pipeline{
			Name:                   "lead-research",
			AIEmployeeOriginalTask: "帮未配置的 Example Product 寻找潜客",
		},
	}
	got := appendLeadResearchProductSourceContext(
		"base prompt",
		nil,
		exec,
		&PipelineStep{Name: "产品画像与SWOT"},
		"",
	)
	if got != "base prompt" {
		t.Fatalf("generic lead research injected a built-in product special case: %s", got)
	}
}

func TestLeadResearchProductSourceContextSupportsConfiguredProducts(t *testing.T) {
	gateway := &GatewayService{config: &Config{
		LeadResearchProducts: []LeadResearchProductSource{{
			Name:         "Acme ERP",
			Aliases:      []string{"acme"},
			OfficialURLs: []string{"https://products.example.test/acme"},
		}},
	}}
	exec := &PipelineExecution{
		PipelineName: "lead-research",
		RuntimePipeline: &Pipeline{
			Name:                   "lead-research",
			AIEmployeeOriginalTask: "研究 Acme 的购买方",
		},
	}
	got := appendLeadResearchProductSourceContext(
		"base prompt",
		gateway,
		exec,
		&PipelineStep{Name: "产品画像与SWOT"},
		"",
	)
	for _, want := range []string{
		"宿主提供的目标产品身份候选",
		"Acme ERP",
		"https://products.example.test/acme",
		"不是完成画像的必要条件",
		"实质改变产品定位、ICP 或检索方向",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("configured product source context missing %q: %s", want, got)
		}
	}
}

func TestLeadResearchProductIdentityGuardAllowsCatalogURLAndBlocksGuess(t *testing.T) {
	gateway := &GatewayService{config: &Config{
		LeadResearchProducts: []LeadResearchProductSource{{
			Name:         "Acme ERP",
			Aliases:      []string{"acme"},
			OfficialURLs: []string{"https://products.example.test/acme"},
		}},
	}}
	exec := leadEvidenceTestExecution()
	exec.RuntimePipeline.AIEmployeeOriginalTask = "帮 Acme ERP 找上海潜客"

	allowedInner := &leadEvidenceRecordingTool{name: "web_fetch", result: "official product page"}
	allowed := guardLeadResearchEvidenceTool(allowedInner, exec, 1, gateway)
	if _, err := allowed.Execute(context.Background(), map[string]interface{}{
		"url": "https://products.example.test/acme",
	}); err != nil {
		t.Fatalf("catalog URL was rejected: %v", err)
	}

	guessedInner := &leadEvidenceRecordingTool{name: "web_fetch", result: "guessed page"}
	guessed := guardLeadResearchEvidenceTool(guessedInner, exec, 1, gateway)
	if _, err := guessed.Execute(context.Background(), map[string]interface{}{
		"url": "https://github.com/acme/erp",
	}); err == nil || !strings.Contains(err.Error(), "denied guessed URL") {
		t.Fatalf("guessed URL was not rejected: %v", err)
	}
	if guessedInner.params != nil {
		t.Fatal("guessed URL reached the underlying fetch tool")
	}
}

func TestLeadResearchProductIdentityGuardAllowsSearchDiscoveredURL(t *testing.T) {
	exec := leadEvidenceTestExecution()
	exec.RuntimePipeline.AIEmployeeOriginalTask = "研究 Acme ERP"
	exec.Steps = []StepResult{
		{Name: aiEmployeeIntakeStepName},
		{
			Name: "产品画像与SWOT",
			ToolCalls: []ToolCallRecord{{
				ToolName: "web_search",
				Status:   "success",
				Result:   "1. Acme official\nhttps://acme.example.test/product",
			}},
		},
	}
	inner := &leadEvidenceRecordingTool{name: "web_fetch", result: "verified product page"}
	guarded := guardLeadResearchEvidenceTool(inner, exec, 1)
	if _, err := guarded.Execute(context.Background(), map[string]interface{}{
		"url": "https://acme.example.test/product",
	}); err != nil {
		t.Fatalf("search-discovered URL was rejected: %v", err)
	}
}

func TestLeadResearchProductIdentityGuardAllowsUserSuppliedURL(t *testing.T) {
	exec := leadEvidenceTestExecution()
	exec.RuntimePipeline.AIEmployeeOriginalTask = "研究 https://vendor.example.test/product 的购买方"
	inner := &leadEvidenceRecordingTool{name: "web_fetch", result: "user supplied product page"}
	guarded := guardLeadResearchEvidenceTool(inner, exec, 1)
	if _, err := guarded.Execute(context.Background(), map[string]interface{}{
		"url": "https://vendor.example.test/product",
	}); err != nil {
		t.Fatalf("user-supplied URL was rejected: %v", err)
	}
}

func TestLeadResearchProductCatalogConfigCompilesWithPipelineExecutor(t *testing.T) {
	gateway := &GatewayService{config: &Config{LeadResearchProducts: []LeadResearchProductSource{{
		Name: "Acme", OfficialURLs: []string{"https://acme.example.test/"},
	}}}}
	if executor := NewPipelineExecutor(nil, gateway, log.New(io.Discard, "", 0)); executor == nil {
		t.Fatal("pipeline executor was not created")
	}
}
