package app

import (
	"context"
	"io"
	"log"
	"testing"
)

func newExplicitMentionTestRouter(t *testing.T, autoRoute bool) *SupervisorRouter {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	mgr := NewAIEmployeeManager(nil, logger)
	mgr.Register([]AIEmployeeConfig{{
		ID:       "quant-trader",
		Name:     "量化交易员",
		AtName:   "量化交易员",
		Role:     "quantitative research investment analysis",
		Pipeline: "quant-trader",
	}})
	return NewSupervisorRouter(SupervisorConfig{
		Identity:             "assistant",
		AutoRouteAIEmployees: autoRoute,
	}, mgr, nil, logger)
}

func TestSupervisorDoesNotImplicitlyActivateAIEmployee(t *testing.T) {
	router := newExplicitMentionTestRouter(t, false)
	result := router.Route(context.Background(), "请让量化交易员分析 NVDA")
	if result.Mode == ModeAE || result.TargetAE != "" {
		t.Fatalf("implicit route = %#v, want main assistant route", result)
	}
}

func TestSupervisorExplicitMentionAlwaysActivatesAIEmployee(t *testing.T) {
	router := newExplicitMentionTestRouter(t, false)
	result := router.Route(context.Background(), "@量化交易员 分析 NVDA")
	if result.Mode != ModeAE || result.TargetAE != "quant-trader" {
		t.Fatalf("explicit route = %#v, want quant-trader", result)
	}
}

func TestSupervisorLegacyImplicitRoutingRequiresOptIn(t *testing.T) {
	router := newExplicitMentionTestRouter(t, true)
	result := router.Route(context.Background(), "量化交易员 quantitative research investment analysis quant trader")
	if result.Mode != ModeAE || result.TargetAE != "quant-trader" {
		t.Fatalf("opt-in implicit route = %#v, want quant-trader", result)
	}
}
