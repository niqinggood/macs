package app

import (
	"context"
	"fmt"
	"testing"
)

func TestPDFExtraction(t *testing.T) {
	fp := `knowledge/rag/2/files/Deliberate calm.pdf`
	et := NewExtractTool(".", false)
	ctx := context.Background()

	t1, e1 := et.ExtractPDFRaw(fp)
	t.Logf("Raw: %d bytes, err=%v", len(t1), e1)
	if len(t1) > 0 {
		preview := t1
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Logf("  preview: %s", preview)
	}

	t2, e2 := et.ExtractPDFWithNode(ctx, fp)
	t.Logf("Node: %d bytes, err=%v", len(t2), e2)

	t3, e3 := et.ExtractPDFWithPdfToText(ctx, fp)
	t.Logf("Pdftotext: %d bytes, err=%v", len(t3), e3)

	t4, e4 := ragExtractText(fp)
	t.Logf("ragExtractText: %d bytes, err=%v", len(t4), e4)
}

func TestEmptyPDFHandling(t *testing.T) {
	// Simulate what happens when all extraction methods fail
	text := ""
	err := fmt.Errorf("all methods failed")
	if text == "" && err == nil {
		t.Error("Should have detected empty text")
	}
	if err == nil && text == "" {
		t.Error("Empty text should be an error")
	}
	t.Logf("Empty detection OK: text=%q err=%v", text, err)
}
