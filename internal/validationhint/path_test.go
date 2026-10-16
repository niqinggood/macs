package validationhint

import (
	"strings"
	"testing"
)

func TestSuggestedValidationForPaths(t *testing.T) {
	hint := SuggestedForPaths([]string{"main.go", "web/app.ts", "web/style.css"})
	for _, want := range []string{
		`run_tests command="go test ./..."`,
		`run_tests command="npm test"`,
		`code_check action=build`,
		`open in browser`,
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("expected validation hint %q, got:\n%s", want, hint)
		}
	}
}

func TestSuggestedValidationForPathsInWorkspaceFocusesGoPackage(t *testing.T) {
	hint := SuggestedForPathsInWorkspace(`E:\repo`, []string{`E:\repo\pkg\calc.go`})
	for _, want := range []string{
		`run_tests command="go test ./..."`,
		`run_tests command="go test ./pkg"`,
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("expected focused validation hint %q, got:\n%s", want, hint)
		}
	}
}

func TestSuggestedValidationForPathsEmpty(t *testing.T) {
	if got := SuggestedForPaths([]string{"README.md"}); got != "" {
		t.Fatalf("expected no validation hint for docs-only change, got %q", got)
	}
}
