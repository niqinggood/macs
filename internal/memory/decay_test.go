package memory

import (
	"strings"
	"testing"
	"time"
)

func TestDecayAtUsesHalfLife(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	score := DecayAt("2026-08-29", 7, now)
	if score < 0.49 || score > 0.51 {
		t.Fatalf("one half-life score = %v, want about 0.5", score)
	}
}

func TestWeightFactsPrefersRecentFact(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	weighted := WeightFacts([]Fact{
		{Subject: "old", Predicate: "uses", CreatedAt: "2026-08-01"},
		{Subject: "new", Predicate: "uses", CreatedAt: "2026-09-05"},
	}, 10, now)
	if len(weighted) != 2 || weighted[0].Subject != "new" {
		t.Fatalf("weighted facts = %#v", weighted)
	}
}

func TestBuildProactiveMemoryFiltersAndFormats(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	facts := []Fact{
		{Subject: "A", Predicate: "uses", Object: "Go", Confidence: 0.9, CreatedAt: "2026-09-05"},
		{Subject: "B", Predicate: "uses", Object: "Git", Confidence: 0.9, CreatedAt: "2026-09-04"},
		{Subject: "C", Predicate: "uses", Object: "SQLite", Confidence: 0.9, CreatedAt: "2026-09-03"},
		{Subject: "hidden", Predicate: "uses", Object: "secret", Confidence: 1, CreatedAt: "2026-09-05"},
	}
	got := BuildProactiveMemory(
		facts,
		now,
		func(fact Fact) bool { return fact.Subject != "hidden" },
		nil,
	)
	if !strings.Contains(got, "Recent Context") || !strings.Contains(got, "A → uses → Go") {
		t.Fatalf("proactive memory = %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("filtered fact leaked: %q", got)
	}
}
