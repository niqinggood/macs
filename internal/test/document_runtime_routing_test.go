package app

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFExtractionProvidersAreOrderedNativeNodePython(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\ninvalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewExtractTool(t.TempDir(), false).ExtractPDF(context.Background(), path, "")
	if err == nil {
		t.Fatal("invalid PDF unexpectedly extracted")
	}
	message := err.Error()
	order := []string{"native-go:", "pdftotext:", "pdf.js:", "unstructured:", "pdfplumber:", "pypdf:", "raw:"}
	details := strings.TrimSuffix(strings.TrimPrefix(message, "PDF extraction failed ("), ")")
	attempts := strings.Split(details, "; ")
	if len(attempts) != len(order) {
		t.Fatalf("PDF attempts=%d, want %d: %s", len(attempts), len(order), message)
	}
	for index, provider := range order {
		if !strings.HasPrefix(attempts[index], provider) {
			t.Fatalf("PDF provider %d=%q, want prefix %q: %s", index, attempts[index], provider, message)
		}
	}
}

func TestDOCXExtractionUsesNativeParserFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	document, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = document.Write([]byte(`<w:document><w:body><w:p><w:r><w:t>Native DOCX route</w:t></w:r></w:p></w:body></w:document>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := NewExtractTool(t.TempDir(), false).ExtractDocx(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "**Method**: docx-parser") || !strings.Contains(result, "Native DOCX route") {
		t.Fatalf("DOCX did not use the native parser: %s", result)
	}
}
