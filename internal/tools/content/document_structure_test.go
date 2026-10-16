package content

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractDocxStructuredPreservesHeadingStyles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "structured.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	document, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = document.Write([]byte(`<w:document xmlns:w="w"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Executive Summary</w:t></w:r></w:p><w:p><w:r><w:t>Verified body text.</w:t></w:r></w:p></w:body></w:document>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	text := ExtractDocxStructured(path)
	if !strings.Contains(text, "# Executive Summary") || !strings.Contains(text, "Verified body text.") {
		t.Fatalf("structured DOCX output = %q", text)
	}
}

func TestExtractDocxStructuredUsesInjectedFallback(t *testing.T) {
	called := false
	text := ExtractDocxStructuredOrFallback("missing.docx", func(string) string {
		called = true
		return "fallback text"
	})
	if !called || text != "fallback text" {
		t.Fatalf("fallback result = %q, called=%v", text, called)
	}
}

func TestExtractPDFWithHeadingsUsesInjectedExtractor(t *testing.T) {
	text := ExtractPDFWithHeadings("ignored.pdf", func(string) string {
		return "EXECUTIVE SUMMARY\n\nThis is supporting evidence."
	})
	if !strings.Contains(text, "# EXECUTIVE SUMMARY") || !strings.Contains(text, "supporting evidence") {
		t.Fatalf("classified PDF output = %q", text)
	}
}
