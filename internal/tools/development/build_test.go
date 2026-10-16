package development

import (
	"strings"
	"testing"
)

func TestParseBuildErrorsExtractsGoLocation(t *testing.T) {
	errors := ParseBuildErrors("cmd/server/main.go:12:7: undefined: startServer\n", "Go")
	if len(errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(errors))
	}
	got := errors[0]
	if got.File != "cmd/server/main.go" || got.Line != 12 || got.Col != 7 || got.Message != "undefined: startServer" {
		t.Fatalf("unexpected parsed error: %#v", got)
	}
}

func TestSuggestBuildFixesRecognizesGoFailures(t *testing.T) {
	suggestions := SuggestBuildFixes("undefined: startServer\nmissing go.sum entry", "Go")
	joined := strings.Join(suggestions, "\n")
	for _, expected := range []string{"Undefined symbol", "go mod tidy"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("suggestions missing %q: %q", expected, joined)
		}
	}
}

func TestBuildCommandKeepsTargetAsSingleArgument(t *testing.T) {
	command, arguments := NewBuildTool(".").buildCommand("Go", "./cmd/server")
	if command != "go" || len(arguments) != 2 || arguments[0] != "build" || arguments[1] != "./cmd/server" {
		t.Fatalf("unexpected build command: %q %#v", command, arguments)
	}
}
