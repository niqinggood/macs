package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	channelcore "mac/internal/channels"
)

type captureImageChannel struct {
	*captureReplyChannel
	images []string
}

func (c *captureImageChannel) SendImage(recipient string, imagePath string) error {
	c.recipient = recipient
	c.images = append(c.images, imagePath)
	return nil
}

func writeTestImage(t *testing.T, workspace string) string {
	t.Helper()
	path := filepath.Join(workspace, ".openkitty", "storage", "codex-images", "s1", "rag.png")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte{0x89, 0x50, 0x4e, 0x47}, 0644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	return path
}

func TestPrepareChannelContentExtractsDownloadImage(t *testing.T) {
	workspace := t.TempDir()
	writeTestImage(t, workspace)
	g := &GatewayService{config: &Config{Workspace: workspace}}
	content := "![Codex 生成图片](/api/files/download?path=%2F.openkitty%2Fstorage%2Fcodex-images%2Fs1%2Frag.png)\n\n" +
		"文件：.openkitty/storage/codex-images/s1/rag.png\n\n" +
		"RAG 原理说明"

	got := channelcore.PrepareContent(g.config.Workspace, content)
	if len(got.Images) != 1 {
		t.Fatalf("images len = %d, want 1", len(got.Images))
	}
	if !strings.HasSuffix(filepath.ToSlash(got.Images[0].LocalPath), ".openkitty/storage/codex-images/s1/rag.png") {
		t.Fatalf("unexpected image path: %s", got.Images[0].LocalPath)
	}
	if strings.Contains(got.Text, "api/files/download") || strings.Contains(got.Text, "codex-images/s1/rag.png") {
		t.Fatalf("image artifact leaked into text:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "RAG 原理说明") {
		t.Fatalf("text missing explanation:\n%s", got.Text)
	}
}

func TestSendChannelContentSendsNativeImageWhenSupported(t *testing.T) {
	workspace := t.TempDir()
	imgPath := writeTestImage(t, workspace)
	g := &GatewayService{config: &Config{Workspace: workspace}}
	base := NewBaseChannel("feishu", "feishu")
	ch := &captureImageChannel{captureReplyChannel: &captureReplyChannel{BaseChannel: base}}

	content := "![diagram](.openkitty/storage/codex-images/s1/rag.png)\n\n正文"
	if err := sendChannelContent(g, ch, "chat-1", content, nil); err != nil {
		t.Fatalf("sendChannelContent: %v", err)
	}
	if len(ch.images) != 1 {
		t.Fatalf("sent images = %d, want 1", len(ch.images))
	}
	if filepath.Clean(ch.images[0]) != filepath.Clean(imgPath) {
		t.Fatalf("image path = %s, want %s", ch.images[0], imgPath)
	}
	if ch.count != 1 || ch.content != "正文" {
		t.Fatalf("text send count/content = %d/%q, want 1/正文", ch.count, ch.content)
	}
}

func TestSendChannelContentFallsBackWhenImagesUnsupported(t *testing.T) {
	workspace := t.TempDir()
	writeTestImage(t, workspace)
	g := &GatewayService{config: &Config{Workspace: workspace}}
	ch := &captureReplyChannel{BaseChannel: NewBaseChannel("qq", "qq")}

	content := "![diagram](.openkitty/storage/codex-images/s1/rag.png)\n\n正文"
	if err := sendChannelContent(g, ch, "qq-user", content, nil); err != nil {
		t.Fatalf("sendChannelContent: %v", err)
	}
	if ch.count != 1 {
		t.Fatalf("send count = %d, want 1", ch.count)
	}
	if !strings.Contains(ch.content, "正文") || !strings.Contains(ch.content, "当前渠道未原生发送图片") || !strings.Contains(ch.content, ".openkitty/storage/codex-images/s1/rag.png") {
		t.Fatalf("fallback content missing expected text:\n%s", ch.content)
	}
}
