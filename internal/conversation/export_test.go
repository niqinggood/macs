package conversation

import (
	"strings"
	"testing"
)

func TestExportGenerationIncludesImageAndActualBilling(t *testing.T) {
	cost := 1.5
	generation := &Generation{Capability: "image", Task: GenerationTask{Status: "completed", ResultURL: `https://example.com/result.png?x=" onerror="alert(1)`, PaymentStatus: "charged", Cost: &cost}}
	output := ExportGenerationHTML(generation)
	if !strings.Contains(output, "<img ") || !strings.Contains(output, "已扣费") || !strings.Contains(output, "1.5 积分") || strings.Contains(output, ` onerror="`) || !strings.Contains(output, "&#34;") {
		t.Fatal("image or billing export is incorrect", output)
	}
	generation.Capability = "video"
	generation.Task.PaymentStatus = "refunded"
	output = ExportGenerationHTML(generation)
	if strings.Contains(output, "<img ") || !strings.Contains(output, "已退款") || !strings.Contains(output, "1.5 积分") {
		t.Fatal("video refund missing", output)
	}
	generation.Task.PaymentStatus = "free"
	generation.Task.Cost = nil
	if !strings.Contains(ExportGenerationHTML(generation), "0 积分") {
		t.Fatal("free model was presented as paid")
	}
}

func TestExportGenerationRejectsActiveAndUnsafeImageSources(t *testing.T) {
	for _, source := range []string{"javascript:alert(1)", "file:///private.png", "data:text/html,<script>x</script>", "https://secret@example.com/image.png", ""} {
		generation := &Generation{Capability: "image", Task: GenerationTask{Status: "completed", ResultURL: source}}
		if strings.Contains(ExportGenerationHTML(generation), "<img ") {
			t.Fatalf("unsafe source allowed: %q", source)
		}
	}
	generation := &Generation{Capability: "image", Task: GenerationTask{Status: "queued", ResultURL: "https://example.com/image.png"}}
	if strings.Contains(ExportGenerationHTML(generation), "<img ") {
		t.Fatal("pending image presented as completed")
	}
}

func TestExportGenerationIncludesMusicTitlePlayerLyricsAndBilling(t *testing.T) {
	generation := &Generation{Capability: "music", ModelName: "Music model", Task: GenerationTask{
		Status: "completed", Title: `Song <script>`, ResultURL: "/api/platform/media/music/42/audio", Lyrics: "First line\n<script>alert(1)</script>", PaymentStatus: "free",
	}}
	output := ExportGenerationHTML(generation)
	for _, want := range []string{"<audio controls", "Song &lt;script&gt;", "播放 / 下载音乐", "Music model", "歌词", "0 积分"} {
		if !strings.Contains(output, want) {
			t.Fatalf("music export missing %q: %s", want, output)
		}
	}
	if strings.Contains(output, "<script>") {
		t.Fatal("music metadata was not escaped")
	}
	for _, unsafe := range []string{"javascript:alert(1)", "/api/platform/media/music/../../private/audio", "//foreign.example/audio", "/api/platform/media/music/42/audio?token=secret"} {
		generation.Task.ResultURL = unsafe
		if strings.Contains(ExportGenerationHTML(generation), "<audio") {
			t.Fatalf("unsafe audio link exported: %q", unsafe)
		}
	}
}
