package operations

import (
	"strings"
	"testing"
)

func TestFilterProcessOutputTreatsFilterAsText(t *testing.T) {
	output := "root 1 server\nalice 2 worker\nbob 3 helper\n"
	if got := filterProcessOutput(output, "WORKER"); strings.TrimSpace(got) != "alice 2 worker" {
		t.Fatalf("unexpected filtered output: %q", got)
	}
	if got := filterProcessOutput(output, "; rm -rf /"); got != "" {
		t.Fatalf("shell-like filter should remain plain unmatched text: %q", got)
	}
}

func TestEnvironmentInfoRedactsSecrets(t *testing.T) {
	result := environmentInfo([]string{"NORMAL=value", "API_TOKEN=secret", "PASSWORD=hunter2"})
	if !strings.Contains(result, "NORMAL=value") || !strings.Contains(result, "API_TOKEN=***hidden***") || strings.Contains(result, "hunter2") || strings.Contains(result, "=secret") {
		t.Fatalf("environment redaction failed: %q", result)
	}
}

func TestDetectPackageManager(t *testing.T) {
	if got := DetectPackageManager("github.com/acme/tool@latest"); got != "npm" {
		t.Fatalf("historical manager precedence changed: %q", got)
	}
	if got := DetectPackageManager("requests"); got != "pip" {
		t.Fatalf("default manager = %q, want pip", got)
	}
}
