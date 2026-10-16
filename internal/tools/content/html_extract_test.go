package content

import (
	"strings"
	"testing"
)

func TestExtractHTMLReturnsVisibleTextOnly(t *testing.T) {
	text, err := ExtractHTML([]byte(`<html><head><title>Hidden title</title><style>.x{}</style></head><body><h1>Visible</h1><script>hidden()</script><p>Body text</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Visible") || !strings.Contains(text, "Body text") {
		t.Fatalf("visible text missing: %q", text)
	}
	if strings.Contains(text, "Hidden title") || strings.Contains(text, "hidden()") {
		t.Fatalf("non-visible text leaked: %q", text)
	}
}
