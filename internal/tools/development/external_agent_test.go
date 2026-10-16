package development

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternalAgentWriteExecutionReport(t *testing.T) {
	dir := t.TempDir()
	tool := NewExternalAgentTool(dir)

	report, err := tool.WriteExecutionReport(dir, "codex", "ext_1", "success", 1200*time.Millisecond, "fix bug", "done", []string{"M api.go"})
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !strings.Contains(report, ".openkitty/storage/external_agents/") {
		t.Fatalf("report path = %q", report)
	}
	data, err := os.ReadFile(filepath.FromSlash(report))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"# External Coding Agent Report",
		"Agent: codex",
		"Conversation: ext_1",
		"M api.go",
		"fix bug",
		"done",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
}

func TestSanitizeExternalAgentName(t *testing.T) {
	if got := sanitizeExternalAgentName("codex/ext 1"); got != "codex_ext_1" {
		t.Fatalf("sanitize = %q", got)
	}
}
