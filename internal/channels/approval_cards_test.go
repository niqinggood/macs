package channels

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildFeishuApprovalCardContainsActionValues(t *testing.T) {
	req := &ApprovalRequest{
		ID:      "cxap_000123",
		Kind:    "exec",
		Summary: "Command: go test ./...",
		CWD:     "E:/openkitty",
		ShellAnalysis: &ApprovalShellAnalysis{
			Class:         "build_test",
			Risk:          "moderate",
			SuggestedRule: "cli(go test)",
		},
	}
	card := buildFeishuApprovalCard(req)
	b, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	body := string(b)
	for _, want := range []string{"cxap_000123", "codex_approval", "accept", "decline", "Shell analysis", "cli(go test)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("card body missing %q: %s", want, body)
		}
	}
}
