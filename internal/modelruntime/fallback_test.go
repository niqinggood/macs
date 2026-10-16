package modelruntime

import (
	"context"
	"errors"
	"testing"
)

type testModel struct {
	name string
	key  string
}

var testTraits = ModelTraits[testModel]{
	Configured: func(model testModel) bool { return model.key != "" },
	Name:       func(model testModel) string { return model.name },
}

func TestFallbackChainFallsBackAndSnapshotsStats(t *testing.T) {
	chain := NewFallbackChain("default", []testModel{{"primary", "a"}, {"secondary", "b"}}, testTraits, nil)
	result, err := chain.Call(context.Background(), func(model testModel) (string, error) {
		if model.name == "primary" {
			return "", errors.New("primary unavailable")
		}
		return model.name, nil
	})
	if err != nil || result != "secondary" {
		t.Fatalf("Call() = %q, %v", result, err)
	}
	stats := chain.Stats()
	if stats.PrimaryCalls != 1 || stats.PrimaryErrors != 1 || stats.FallbackCalls != 1 || stats.FallbackErrors != 0 {
		t.Fatalf("Stats() = %#v", stats)
	}
	stats.PrimaryCalls = 99
	if chain.Stats().PrimaryCalls != 1 {
		t.Fatal("Stats exposed mutable internal state")
	}
}

func TestFallbackChainSkipsUnconfiguredModels(t *testing.T) {
	chain := NewFallbackChain("default", []testModel{{"primary", ""}, {"secondary", "b"}}, testTraits, nil)
	result, err := chain.Call(context.Background(), func(model testModel) (string, error) { return model.name, nil })
	if err != nil || result != "secondary" {
		t.Fatalf("Call() = %q, %v", result, err)
	}
}

func TestRegistryRemovesStaleDefault(t *testing.T) {
	registry := NewRegistry[testModel]()
	registry.Set("default", NewFallbackChain("default", []testModel{{"primary", "a"}}, testTraits, nil))
	if registry.Get("missing") == nil {
		t.Fatal("default chain was not returned")
	}
	registry.Set("default", nil)
	if registry.Get("default") != nil {
		t.Fatal("default chain was not removed")
	}
}
