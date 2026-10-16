package channels

import (
	"strings"
	"testing"
)

func TestQQApprovalButtonDataRoundTrip(t *testing.T) {
	data := QQApprovalButtonData("cxap_000456", "approve")
	decision, id, ok := ParseQQApprovalButtonData(data)
	if !ok {
		t.Fatal("expected button data to parse")
	}
	if decision != "accept" || id != "cxap_000456" {
		t.Fatalf("parsed (%q, %q), want (accept, cxap_000456)", decision, id)
	}
}

func TestQQApprovalKeyboardContainsCallbackData(t *testing.T) {
	request := &ApprovalRequest{ID: "cxap_000789"}
	keyboard := BuildQQApprovalKeyboard(request)
	if keyboard == nil || keyboard.Content == nil || len(keyboard.Content.Rows) != 1 || len(keyboard.Content.Rows[0].Buttons) != 2 {
		t.Fatalf("unexpected keyboard: %#v", keyboard)
	}
	data := keyboard.Content.Rows[0].Buttons[0].Action.Data
	if !strings.Contains(data, "cxap_000789") || !strings.Contains(data, "accept") {
		t.Fatalf("unexpected approve button data: %q", data)
	}
}

func TestQQApprovalTextIncludesShellAnalysis(t *testing.T) {
	request := &ApprovalRequest{
		ID:   "cxap_000999",
		Kind: "exec",
		ShellAnalysis: &ApprovalShellAnalysis{
			Class:         "build_test",
			Risk:          "moderate",
			SuggestedRule: "cli(go test)",
		},
	}
	text := FormatQQApprovalText(request)
	for _, expected := range []string{"Shell analysis", "Class: build_test", "Risk: moderate", "Suggested rule: cli(go test)"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("QQ approval text missing %q: %s", expected, text)
		}
	}
}
