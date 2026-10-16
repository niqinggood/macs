package app

import (
	"strings"
	"testing"

	"mac/internal/conversation"
)

func TestBuildExportHTMLPreservesMediaAndRedactsWithoutChangingMessages(t *testing.T) {
	generation := &conversation.Generation{Capability: "music", ModelName: "Music", Task: conversation.GenerationTask{
		Status: "completed", Title: "Song <script>alert(1)</script>", Lyrics: "api_key=fixture_private_credential_12345\n20%% chorus", ResultURL: "/api/platform/media/music/42/audio", PaymentStatus: "free",
	}}
	messages := []ChatMessage{{Role: "assistant", Content: "<script>alert(2)</script>\n20%% complete", Generation: generation}}
	output := buildExportHTML("<unsafe-session>", messages)
	for _, want := range []string{"<audio controls", "播放 / 下载音乐", "20%% complete", "20%% chorus", "&lt;unsafe-session&gt;", "&lt;script&gt;", "[REDACTED]", "0 积分"} {
		if !strings.Contains(output, want) {
			t.Fatalf("export missing %q", want)
		}
	}
	if strings.Contains(output, "<script>") || strings.Contains(output, "fixture_private_credential_12345") || strings.Contains(output, "20%%%%") {
		t.Fatal("unsafe or altered text in export")
	}
	if generation.Task.Lyrics != "api_key=fixture_private_credential_12345\n20%% chorus" || messages[0].Content != "<script>alert(2)</script>\n20%% complete" {
		t.Fatal("export changed original messages")
	}
}
