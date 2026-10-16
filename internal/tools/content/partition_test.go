package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/textutil"
)

func TestPartitionDocClassifiesPlainText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("REPORT TITLE\n\nThis is a complete evidence paragraph for classification."), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := PartitionDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if document.Category != textutil.CatText || len(document.Elements) == 0 {
		t.Fatalf("unexpected partition result: %#v", document)
	}
}

func TestExtractDocumentTextRequiresInjectedPDFExtractor(t *testing.T) {
	if _, err := ExtractDocumentText("missing.pdf", textutil.CatPDF); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing PDF dependency error = %v", err)
	}
	text, err := ExtractDocumentText("ignored.pdf", textutil.CatPDF, PartitionDependencies{
		PDF: func(string) string { return "injected PDF text" },
	})
	if err != nil || text != "injected PDF text" {
		t.Fatalf("injected PDF extraction = %q, %v", text, err)
	}
}
