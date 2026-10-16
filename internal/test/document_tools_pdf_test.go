package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPDFFailureReportsAttemptDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\ninvalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewExtractTool(t.TempDir(), false)
	_, err := tool.ExtractPDF(context.Background(), path, "")
	if err == nil {
		t.Fatal("invalid PDF unexpectedly extracted")
	}
	message := err.Error()
	if strings.Contains(message, "<nil>") {
		t.Fatalf("PDF extraction hid the real failures: %s", message)
	}
	for _, method := range []string{"unstructured:", "pdfplumber:", "pypdf:", "native-go:", "raw:"} {
		if !strings.Contains(message, method) {
			t.Fatalf("PDF extraction error omitted %s: %s", method, message)
		}
	}
}
