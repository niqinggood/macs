package channels

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareContentExtractsDownloadImage(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".openkitty", "storage", "codex-images", "s1", "rag.png")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0x89, 0x50, 0x4e, 0x47}, 0644); err != nil {
		t.Fatal(err)
	}
	content := "![Codex 生成图片](/api/files/download?path=%2F.openkitty%2Fstorage%2Fcodex-images%2Fs1%2Frag.png)\n\n" +
		"文件：.openkitty/storage/codex-images/s1/rag.png\n\nRAG 原理说明"
	prepared := PrepareContent(workspace, content)
	if len(prepared.Images) != 1 {
		t.Fatalf("images len = %d, want 1", len(prepared.Images))
	}
	if !strings.HasSuffix(filepath.ToSlash(prepared.Images[0].LocalPath), ".openkitty/storage/codex-images/s1/rag.png") {
		t.Fatalf("unexpected image path: %s", prepared.Images[0].LocalPath)
	}
	if strings.Contains(prepared.Text, "api/files/download") || strings.Contains(prepared.Text, "codex-images/s1/rag.png") {
		t.Fatalf("image artifact leaked into text:\n%s", prepared.Text)
	}
	if !strings.Contains(prepared.Text, "RAG 原理说明") {
		t.Fatalf("text missing explanation:\n%s", prepared.Text)
	}
}

func TestPrepareContentRejectsImageOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	prepared := PrepareContent(workspace, "![outside]("+filepath.ToSlash(outside)+")")
	if len(prepared.Images) != 0 {
		t.Fatalf("outside image must not be resolved: %#v", prepared.Images)
	}
}
