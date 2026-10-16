package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestWriteFileRejectsDirectPPTXWrite(t *testing.T) {
	got, err := NewWriteFileTool(t.TempDir()).Execute(context.Background(), map[string]interface{}{
		"path":    "deck.pptx",
		"content": "not a real pptx",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"write failed", "cannot create binary PowerPoint files directly"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected result to contain %q, got %q", want, got)
		}
	}
}
