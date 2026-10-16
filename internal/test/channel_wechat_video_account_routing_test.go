package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolArtifactPathTracksFileReadsAndMutations(t *testing.T) {
	tests := []struct {
		call ToolCall
		want string
	}{
		{call: ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": `E:\work\source.txt`}}, want: `E:\work\source.txt`},
		{call: ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": `E:\work\deck.py`}}, want: `E:\work\deck.py`},
		{call: ToolCall{Name: "edit_file", Arguments: map[string]interface{}{"file_path": `E:\work\deck.py`}}, want: `E:\work\deck.py`},
		{call: ToolCall{Name: "convert", Arguments: map[string]interface{}{"output": `E:\work\report.docx`}}, want: `E:\work\report.docx`},
		{call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"path": `E:\work\ignored.txt`}}, want: ""},
	}
	for _, tt := range tests {
		if got := toolArtifactPath(tt.call); got != tt.want {
			t.Fatalf("toolArtifactPath(%s) = %q, want %q", tt.call.Name, got, tt.want)
		}
	}
}

func TestWechatVideoAccountExtractionRequestRecognizesSphLink(t *testing.T) {
	if !isWechatVideoAccountExtractionRequest("https://weixin.qq.com/sph/AZhhbswU8G 提取这个的内容") {
		t.Fatal("expected WeChat Channels share URL to activate media extraction")
	}
	if isWechatVideoAccountExtractionRequest("https://mp.weixin.qq.com/s/article") {
		t.Fatal("ordinary WeChat article must not activate the video-account route")
	}
}

func TestWechatVideoAccountEvidenceRejectsCaptionOnlyWebFetch(t *testing.T) {
	caption := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://weixin.qq.com/sph/AZhhbswU8G"}},
		"视频号 页面配文 作者 日期",
		true,
	)
	if wechatVideoAccountMediaEvidenceReady([]string{caption}) || wechatVideoAccountMediaAttempted([]string{caption}) {
		t.Fatal("caption-only web_fetch was treated as actual media extraction")
	}
}

func TestWechatVideoAccountEvidenceAcceptsReadMaterials(t *testing.T) {
	materials := toolResultForFinalization(
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": ".openkitty/storage/wx/note_materials.json"}},
		`{"source":{"url":"https://weixin.qq.com/sph/AZhhbswU8G"},"asr_lines":["口播"],"ocr_lines":["字幕"],"status":{"asr":"成功","ocr":"成功"},"file":"note_materials.json"}`,
		true,
	)
	if !wechatVideoAccountMediaEvidenceReady([]string{materials}) {
		t.Fatal("read note_materials.json was not accepted as media extraction evidence")
	}
	if !wechatVideoAccountMediaAttempted([]string{materials}) {
		t.Fatal("read media materials should count as an attempted pipeline")
	}
}

func TestWechatVideoAccountPromptForbidsCaptionOnlySuccess(t *testing.T) {
	prompt := wechatVideoAccountSourceContractPrompt()
	for _, want := range []string{"wx-video-account-notes", "note_materials.json", "caption", "partial completion"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("source contract missing %q: %s", want, prompt)
		}
	}
}

func TestWechatVideoAccountSynthesisReadsMaterialsOnceFromRecordedPath(t *testing.T) {
	materialPath := filepath.Join(t.TempDir(), "note_materials.json")
	material := `{"share_url":"https://weixin.qq.com/sph/AZhhbswU8G","ocr_lines":["画面字幕"],"asr_lines":["真正的视频口播内容"],"status":{"asr":"成功","ocr":"成功"}}`
	if err := os.WriteFile(materialPath, []byte(material), 0o600); err != nil {
		t.Fatal(err)
	}
	stored := toolResultForFinalization(
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": materialPath}},
		material,
		true,
	)
	record, ok := parseOpenKittyToolRecord(stored)
	if !ok || record.ArtifactPath != materialPath {
		t.Fatalf("read_file artifact path was not retained: %#v", record)
	}

	messages := buildWechatVideoAccountSynthesisMessages("提取这个视频内容", []string{stored})
	if len(messages) != 3 {
		t.Fatalf("expected compact three-message synthesis context, got %d", len(messages))
	}
	packet, _ := messages[2]["content"].(string)
	for _, want := range []string{materialPath, "画面字幕", "真正的视频口播内容"} {
		if !strings.Contains(packet, want) {
			t.Fatalf("synthesis evidence missing %q: %s", want, packet)
		}
	}
	control, _ := messages[0]["content"].(string)
	for _, want := range []string{"Do not call tools", "actual spoken content", "machine transcription"} {
		if !strings.Contains(control, want) {
			t.Fatalf("closed-media control missing %q: %s", want, control)
		}
	}
}

func TestCompactWechatVideoMaterialKeepsBeginningMiddleAndEnd(t *testing.T) {
	raw := strings.Repeat("A", 26000) + strings.Repeat("M", 20000) + strings.Repeat("Z", 26000)
	compact := compactWechatVideoMaterial(raw)
	for _, want := range []string{"AAAA", "MMMM", "ZZZZ", "middle transcript excerpt", "final transcript excerpt"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compacted material missing %q", want)
		}
	}
}
