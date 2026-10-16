package security

import (
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	first := GenerateToken(48)
	second := GenerateToken(48)
	if !strings.HasPrefix(first, "ok_") || len(first) != 51 {
		t.Fatalf("unexpected token shape: %q", first)
	}
	if first == second {
		t.Fatal("two generated tokens unexpectedly matched")
	}
}

func TestIsLocalAddress(t *testing.T) {
	for _, value := range []string{"", "localhost", "127.0.0.1", "::1"} {
		if !IsLocalAddress(value) {
			t.Errorf("IsLocalAddress(%q) = false, want true", value)
		}
	}
	if IsLocalAddress("not-an-ip") {
		t.Fatal("invalid address was classified as local")
	}
}
