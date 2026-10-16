package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssessAEReadinessDirectEmployeeReady(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:     "meeting-secretary",
		Name:   "secretary",
		AtName: "secretary",
		Role:   "meeting notes",
		Direct: &DirectConfig{Prompt: "summarize meeting notes", Tools: []string{"read_file", "write_file"}},
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "ready" {
		t.Fatalf("level = %q, want ready; readiness=%#v", readiness.Level, readiness)
	}
	if !readiness.OfficeReady {
		t.Fatal("meeting secretary should be marked office-ready")
	}
	if readiness.SampleTask == "" {
		t.Fatal("office-ready employee should include sample task")
	}
}

func TestAssessAEReadinessBuiltinPipelineReady(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:       "lead-researcher",
		Name:     "lead researcher",
		AtName:   "lead",
		Role:     "lead research",
		Pipeline: "lead-research",
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "ready" {
		t.Fatalf("level = %q, want ready; readiness=%#v", readiness.Level, readiness)
	}
	if !readiness.EvidenceRequired {
		t.Fatal("lead researcher should require evidence boundary")
	}
}

func TestAssessAEReadinessMissingPipelineIsPlaceholder(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:       "custom",
		Name:     "custom",
		AtName:   "custom",
		Role:     "custom work",
		Pipeline: "missing-pipeline",
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "placeholder" {
		t.Fatalf("level = %q, want placeholder; readiness=%#v", readiness.Level, readiness)
	}
	if readiness.Score >= 45 {
		t.Fatalf("missing pipeline score = %d, want < 45", readiness.Score)
	}
}

func TestAssessAEReadinessExpectedSingleStepPipelineCanBeReady(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:       "cs-pre-sales",
		Name:     "pre sales",
		AtName:   "pre",
		Role:     "answer product questions",
		Pipeline: "cs-pre-sales",
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "ready" {
		t.Fatalf("single-step service pipeline should be ready, got %#v", readiness)
	}
}

func TestAssessAEReadinessExpectedDirectSingleStepCanBeReady(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:     "market-analyst",
		Name:   "market analyst",
		AtName: "market",
		Role:   "market analysis",
		Direct: &DirectConfig{Prompt: "analyze market", Tools: []string{"web_fetch", "web_search"}},
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "ready" {
		t.Fatalf("expected direct single-step employee should be ready, got %#v", readiness)
	}
}

func TestAEModelRoutesDefaultToRequiredTextOnly(t *testing.T) {
	oldRegistry := globalProviderRegistry
	defer func() { globalProviderRegistry = oldRegistry }()
	globalProviderRegistry = nil

	cfg := &AIEmployeeConfig{ID: "meeting-secretary", Name: "secretary"}
	routes := aeModelRoutes(cfg)
	if len(routes) != 1 {
		t.Fatalf("routes length = %d, want 1: %#v", len(routes), routes)
	}
	if routes[0].Capability != "text-to-text" {
		t.Fatalf("capability = %q, want text-to-text", routes[0].Capability)
	}
}

func TestAssessAEReadinessRequiresAllBoundModelsReady(t *testing.T) {
	oldRegistry := globalProviderRegistry
	defer func() { globalProviderRegistry = oldRegistry }()
	globalProviderRegistry = &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax": {
				Name:    "minimax",
				Label:   "MiniMax",
				Type:    "openai",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "sk-valid-minimax-key",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
			"vision": {
				Name:    "vision",
				Label:   "Vision",
				Type:    "openai",
				BaseURL: "https://api.example.com/v1",
				APIKey:  "YOUR_VISION_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "vision-model",
					Capability: "multi-modal",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:           "meeting-secretary",
		Name:         "secretary",
		AtName:       "secretary",
		Role:         "meeting notes",
		ModelBinding: `{"text-to-text":"minimax-m3","multi-modal":"vision-model"}`,
		Direct:       &DirectConfig{Prompt: "summarize meeting notes"},
	}

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "placeholder" {
		t.Fatalf("level = %q, want placeholder when a bound model is unavailable; readiness=%#v", readiness.Level, readiness)
	}
	if !strings.Contains(strings.Join(readiness.Reasons, "\n"), "multi-modal") {
		t.Fatalf("reasons should mention unavailable multi-modal route: %#v", readiness.Reasons)
	}
}

func TestAssessAEReadinessUnexpectedSingleStepPipelineNeedsReview(t *testing.T) {
	gw := &GatewayService{}
	cfg := &AIEmployeeConfig{
		ID:       "custom-one-step",
		Name:     "custom",
		AtName:   "custom",
		Role:     "custom work",
		Pipeline: "one-step-custom",
	}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	gw.pipelineExec.RegisterDynamic("one-step-custom", Pipeline{
		Name: "one-step-custom",
		Steps: []PipelineStep{{
			Name: "respond", Prompt: "do work", Tools: []string{"read_file"}, MaxIter: 3,
		}},
	})

	readiness := gw.assessAEReadiness(cfg)
	if readiness.Level != "needs_review" {
		t.Fatalf("unexpected single-step pipeline should need review, got %#v", readiness)
	}
}

func TestHandleAEReadinessSummary(t *testing.T) {
	gw := &GatewayService{
		logger: log.New(io.Discard, "", 0),
	}
	gw.aeManager = NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	gw.aeManager.Register([]AIEmployeeConfig{
		{
			ID:     "meeting-secretary",
			Name:   "secretary",
			AtName: "secretary",
			Role:   "meeting notes",
			Direct: &DirectConfig{Prompt: "summarize meeting notes"},
		},
		{
			ID:       "custom-placeholder",
			Name:     "custom",
			AtName:   "custom",
			Role:     "custom work",
			Pipeline: "missing-pipeline",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/ae/readiness", nil)
	rr := httptest.NewRecorder()
	gw.handleAEReadiness(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK      bool           `json:"ok"`
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.OK {
		t.Fatal("expected ok=true")
	}
	if body.Summary["ready"] != 1 || body.Summary["placeholder"] != 1 {
		t.Fatalf("unexpected summary: %#v", body.Summary)
	}
}

func TestDefaultAIEmployeesHaveNoReadinessPlaceholders(t *testing.T) {
	gw := &GatewayService{}
	for _, cfg := range defaultAIEmployees() {
		readiness := gw.assessAEReadiness(&cfg)
		if readiness.Level == "placeholder" {
			t.Fatalf("default employee %s is placeholder: %#v", cfg.ID, readiness)
		}
	}
}
