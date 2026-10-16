package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchPrefersGoParserBeforeNodeReadability(t *testing.T) {
	article := strings.Repeat("Native Go article content with enough detail for extraction. ", 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "<!doctype html><html><head><title>Native route</title></head><body><article>%s</article></body></html>", article)
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	readabilityCalls := 0
	tool.SetReadabilityExtractor(func(string, string) (string, error) {
		readabilityCalls++
		return strings.Repeat("Node fallback content ", 40), nil
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if readabilityCalls != 0 {
		t.Fatalf("Node Readability called %d times after Go extraction succeeded", readabilityCalls)
	}
	if !strings.Contains(result, "Native Go article content") {
		t.Fatalf("Go-extracted content missing: %s", result)
	}
}

func TestWebFetchUsesNodeReadabilityWhenGoContentIsTooShort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<!doctype html><html><head><title>Short</title></head><body><article>short</article></body></html>")
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	readabilityCalls := 0
	tool.SetReadabilityExtractor(func(string, string) (string, error) {
		readabilityCalls++
		return "NODE_FALLBACK_MARKER " + strings.Repeat("complete article ", 40), nil
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if readabilityCalls != 1 {
		t.Fatalf("Node Readability calls=%d, want 1", readabilityCalls)
	}
	if !strings.Contains(result, "NODE_FALLBACK_MARKER") {
		t.Fatalf("Node fallback content missing: %s", result)
	}
}
