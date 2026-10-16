package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	developmenttools "mac/internal/tools/development"
)

func TestCodeReviewCheckPatterns(t *testing.T) {
	tool := developmenttools.NewCodeReviewTool(".")

	// Test diff with various issues
	diff := `+++ b/main.go
@@ -1,0 +1,10 @@
+password = "secret123"
+fmt.Println("debug info")
+_, err := doSomething()
+panic("something went wrong")
+TODO: fix this later
+query := "SELECT * FROM users WHERE id = " + userInput
+os.Chmod(path, 0777)
+_ = err
+`

	issues := tool.CheckPatterns(diff, []string{"main.go"})
	if len(issues) < 3 {
		t.Errorf("should find at least 3 issues, got %d", len(issues))
	}

	// Check specific patterns
	found := map[string]bool{}
	for _, issue := range issues {
		found[issue.Category] = true
	}
	if !found["security"] {
		t.Error("should find security issues")
	}
	if !found["error-handling"] {
		t.Error("should find error handling issues")
	}
}

func TestCodeReviewSecurityScan(t *testing.T) {
	tool := developmenttools.NewCodeReviewTool(".")

	diff := `exec.Command(userInput)
os.Chmod("/tmp/x", 0777)`
	issues := tool.SecurityScan(diff)
	if len(issues) < 1 {
		t.Error("should find security issues")
	}
}

func TestCodeReviewSuggestTests(t *testing.T) {
	tool := developmenttools.NewCodeReviewTool(".")

	// Source changes without tests
	suggestions := tool.SuggestTests([]string{"main.go", "util.go"}, "some diff")
	if len(suggestions) == 0 {
		t.Error("should suggest adding tests")
	}

	// With test files
	suggestions = tool.SuggestTests([]string{"main.go", "main_test.go"}, "some diff")
	hasSuggestion := false
	for _, s := range suggestions {
		if strings.Contains(s, "No test files changed") {
			hasSuggestion = true
		}
	}
	if hasSuggestion {
		t.Error("should not complain when test files are present")
	}
}

func TestCodeReviewCountChanges(t *testing.T) {
	diff := "+line1\n+line2\n-line3\n normal\n+line4\n"
	add, del := developmenttools.CountChanges(diff)
	if add != 3 || del != 1 {
		t.Errorf("countChanges: add=%d del=%d, want add=3 del=1", add, del)
	}
}

func TestCodeReviewParseDiffLineNumber(t *testing.T) {
	n := developmenttools.ParseDiffLineNumber("@@ -1,5 +10,7 @@ func test() {")
	if n != 10 {
		t.Errorf("parseDiffLineNumber: got %d, want 10", n)
	}
}

func TestCodeReviewIncludesUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := os.WriteFile(
		filepath.Join(root, "new.go"),
		[]byte("package sample\n\nvar password = \"secret123\"\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	diff, files, err := developmenttools.NewCodeReviewTool(root).GetUnstagedDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "new.go" {
		t.Fatalf("untracked files = %#v", files)
	}
	for _, want := range []string{"--- /dev/null", "+++ b/new.go", `+var password = "secret123"`} {
		if !strings.Contains(diff, want) {
			t.Fatalf("synthetic diff missing %q:\n%s", want, diff)
		}
	}
}

func TestMatchHardcodedSecret(t *testing.T) {
	if !developmenttools.MatchHardcodedSecret(`password = "secret123"`) {
		t.Error("should detect hardcoded password")
	}
	if developmenttools.MatchHardcodedSecret(`password = os.Getenv("PASS")`) {
		t.Error("should not flag env-based passwords")
	}
}

func TestMatchSQLInjectionRisk(t *testing.T) {
	if !developmenttools.MatchSQLInjectionRisk(`query := "SELECT * FROM users"`) {
		t.Error("should detect raw SQL")
	}
	if !developmenttools.MatchSQLInjectionRisk(`fmt.Sprintf("SELECT * FROM %s", table)`) {
		t.Error("should detect Sprintf SQL")
	}
}

func TestMatchIgnoredError(t *testing.T) {
	if !developmenttools.MatchIgnoredError(`result, _ := doSomething()`) {
		t.Error("should detect ignored error with _")
	}
}
