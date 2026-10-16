package app

import (
	"context"
	"strings"
	"testing"
)

func TestDecisionRuntimeRequiresExplicitProviderOptIn(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "synthetic-test-key")
	t.Setenv("OPENKITTY_DECISION_MODEL", "jev-test")
	t.Setenv("OPENKITTY_DECISION_TIMEOUT_MS", "1500")
	for _, test := range []struct {
		provider, key, timeout string
		available              bool
	}{
		{"", "synthetic-test-key", "1500", false},
		{"other", "synthetic-test-key", "1500", false},
		{"jev", "", "1500", false},
		{"jev", "synthetic-test-key", "0", false},
		{"jev", "synthetic-test-key", "bad", false},
		{"jev", "synthetic-test-key", "1500", true},
	} {
		t.Setenv("OPENKITTY_DECISION_PROVIDER", test.provider)
		t.Setenv("TYPESAFE_API_KEY", test.key)
		t.Setenv("OPENKITTY_DECISION_TIMEOUT_MS", test.timeout)
		out, err := newDecisionTool().Execute(context.Background(), map[string]interface{}{"action": "status"})
		if err != nil || strings.Contains(out, `"available":true`) != test.available || strings.Contains(out, "synthetic-test-key") {
			t.Fatalf("unsafe or unexpected provider status: %s %v", out, err)
		}
	}
}
