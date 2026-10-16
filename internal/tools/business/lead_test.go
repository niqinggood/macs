package business

import (
	"context"
	"strings"
	"testing"
)

func TestMockLeadBackendSavesAndRetrievesLead(t *testing.T) {
	backend := NewMockLeadBackend()
	id, err := backend.SaveLead(&LeadRecord{Name: "Ada", Company: "Example", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := backend.GetLead(id)
	if err != nil {
		t.Fatal(err)
	}
	if lead.Name != "Ada" || lead.Company != "Example" {
		t.Fatalf("saved lead = %#v", lead)
	}
}

func TestRegisterAllLeadToolsReturnsCompleteToolFamily(t *testing.T) {
	tools := RegisterAllLeadTools(NewMockLeadBackend())
	want := map[string]bool{
		"lead_research": false,
		"lead_save":     false,
		"lead_score":    false,
		"lead_outreach": false,
		"lead_pipeline": false,
		"lead_update":   false,
		"lead_discover": false,
		"lead_enrich":   false,
		"lead_nurture":  false,
	}
	for _, tool := range tools {
		if _, ok := want[tool.Name()]; ok {
			want[tool.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing registered tool %q", name)
		}
	}
}

func TestLeadDiscoverUsesConfiguredResultLimit(t *testing.T) {
	tool := NewLeadDiscoverTool(NewMockLeadBackend())
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"industry":    "SaaS",
		"max_results": float64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "最多3条") {
		t.Fatalf("result limit missing from output: %q", result)
	}
}
