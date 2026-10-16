package app

import (
	"io"
	"log"
	"strings"
	"testing"
)

func TestProfessionalOnlyEmployeeReadinessDoesNotRequireHostModelRoute(t *testing.T) {
	oldRegistry := globalProviderRegistry
	defer func() { globalProviderRegistry = oldRegistry }()
	globalProviderRegistry = &ModelProviderRegistry{
		providers: map[string]*ModelProvider{},
		logger:    log.New(io.Discard, "", 0),
	}

	gateway := &GatewayService{}
	gateway.pipelineExec = NewPipelineExecutor(nil, gateway, log.New(io.Discard, "", 0))
	gateway.pipelineExec.RegisterDynamic("professional-role", Pipeline{
		Name: "professional-role",
		Steps: []PipelineStep{{
			Name:              "isolated role",
			Prompt:            "dispatch isolated role",
			ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "trading"},
			MaxIter:           1,
		}},
	})
	cfg := &AIEmployeeConfig{
		ID: "professional-role", Name: "Professional Role", AtName: "professional", Role: "isolated work", Pipeline: "professional-role",
	}

	readiness := gateway.assessAEReadiness(cfg)
	if readiness.Level != "needs_review" {
		t.Fatalf("professional-only employee should not claim backend-ready before a health probe: %#v", readiness)
	}
	reasons := strings.Join(readiness.Reasons, "\n")
	if !strings.Contains(reasons, "professional-agent pipeline configured") ||
		!strings.Contains(reasons, "backend health is checked at execution") ||
		strings.Contains(reasons, "model route unavailable") {
		t.Fatalf("unexpected readiness reasons: %#v", readiness.Reasons)
	}
}
