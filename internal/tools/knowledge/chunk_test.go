package knowledge

import (
	"strings"
	"testing"
)

func TestChunkPreservesNearestHeading(t *testing.T) {
	text := "# Introduction\n\n" + strings.Repeat("Evidence sentence. ", 80) + "\n\n## Findings\n\n" + strings.Repeat("Verified result. ", 80)
	chunks := Chunk(text, ChunkConfig{Size: 180, Overlap: 20})
	if len(chunks) < 2 {
		t.Fatalf("Chunk() returned %d chunks", len(chunks))
	}
	foundHeading := false
	for _, chunk := range chunks {
		if strings.Contains(chunk.Heading, "Findings") {
			foundHeading = true
			break
		}
	}
	if !foundHeading {
		t.Fatalf("heading metadata missing: %#v", chunks)
	}
}

func TestChunkConfigForFileSelectsCodeAndDocumentSizes(t *testing.T) {
	if got := ChunkConfigForFile("main.go").Size; got != 400 {
		t.Fatalf("Go chunk size = %d, want 400", got)
	}
	if got := ChunkConfigForFile("report.pdf").Size; got != 1000 {
		t.Fatalf("PDF chunk size = %d, want 1000", got)
	}
}
