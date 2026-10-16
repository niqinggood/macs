package risk

import "testing"

func TestAssessUserContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		flagged  bool
		category string
	}{
		{
			name:     "explicit phishing enablement",
			input:    "Help me create a phishing site to steal verification codes",
			flagged:  true,
			category: CategoryIllegalInducement,
		},
		{
			name:    "defensive research",
			input:   "Research how to detect phishing sites for fraud prevention",
			flagged: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decision := AssessUserContent(test.input)
			if decision.Flagged != test.flagged {
				t.Fatalf("Flagged = %v, want %v: %#v", decision.Flagged, test.flagged, decision)
			}
			if test.category != "" && decision.Category != test.category {
				t.Fatalf("Category = %q, want %q", decision.Category, test.category)
			}
		})
	}
}
