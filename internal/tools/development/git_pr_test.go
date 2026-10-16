package development

import (
	"strings"
	"testing"
)

func TestDetectBaseBranch(t *testing.T) {
	// This is a best-effort test — actual git repo detection depends on environment
	result := detectBaseBranch(".")
	// Should return either "main" or "master"
	if result != "main" && result != "master" {
		t.Errorf("detectBaseBranch returned %q, want main or master", result)
	}
}

func TestRunGitCmd(t *testing.T) {
	result := runGitCmd(".", "--version")
	if result == "" {
		t.Skip("git not available")
	}
	if !strings.Contains(result, "git version") {
		t.Errorf("expected git version output, got: %s", result)
	}
}

func TestGetMapString(t *testing.T) {
	m := map[string]interface{}{
		"title": "Hello",
		"count": 42,
	}
	if getMapString(m, "title") != "Hello" {
		t.Error("should get string value")
	}
	if getMapString(m, "missing") != "" {
		t.Error("missing key should return empty")
	}
}

func TestGetMapCommitMsg(t *testing.T) {
	m := map[string]interface{}{
		"messageHeadline": "feat: add feature",
		"messageBody":     "This is a detailed description.",
	}
	msg := getMapCommitMsg(m)
	if !strings.Contains(msg, "feat: add feature") {
		t.Error("should contain headline")
	}
}
