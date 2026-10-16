package app

import "testing"

func TestResolveAgentIterationLimit(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		want       int
	}{
		{name: "default", configured: 0, want: 150},
		{name: "negative", configured: -1, want: 150},
		{name: "configured lower limit", configured: 75, want: 75},
		{name: "configured ceiling", configured: 150, want: 150},
		{name: "clamped above ceiling", configured: 350, want: 150},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAgentIterationLimit(tt.configured); got != tt.want {
				t.Fatalf("resolveAgentIterationLimit(%d) = %d, want %d", tt.configured, got, tt.want)
			}
		})
	}
}

func TestDefaultAssistantUsesOneHundredFiftyIterationBudget(t *testing.T) {
	for _, agent := range newDefaultConfig.Agents {
		if agent.Name != "Assistant" {
			continue
		}
		if agent.MaxIterations != 150 {
			t.Fatalf("default Assistant max iterations = %d, want 150", agent.MaxIterations)
		}
		return
	}
	t.Fatal("default Assistant config is missing")
}
