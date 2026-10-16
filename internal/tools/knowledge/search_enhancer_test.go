package knowledge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchEnhancerExpandsAndReranks(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		content := "A sufficiently long hypothetical answer."
		if calls == 2 {
			content = "2,1,2"
		}
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"" + content + "\"}}]}"))
	}))
	t.Cleanup(server.Close)

	model := SearchModelConfig{APIKey: "test", Model: "model", BaseURL: server.URL}
	enhancer := NewSearchEnhancer(server.Client())
	enhancer.ConfigureHyDE(true, model)
	enhancer.ConfigureReranker("api", model)

	expanded := enhancer.Expand("question")
	if !strings.Contains(expanded, "hypothetical answer") {
		t.Fatalf("expanded query = %q", expanded)
	}
	results := enhancer.Rerank("question", []RAGResult{
		{SourceDoc: "first", Content: "first passage"},
		{SourceDoc: "second", Content: "second passage"},
	}, 2)
	if len(results) != 2 || results[0].SourceDoc != "second" || results[1].SourceDoc != "first" {
		t.Fatalf("reranked results = %#v", results)
	}
}

func TestSearchEnhancerFallsBackWhenDisabledOrRequestFails(t *testing.T) {
	results := []RAGResult{{SourceDoc: "original", Content: "passage"}}
	enhancer := NewSearchEnhancer(nil)
	if got := enhancer.Expand("question"); got != "question" {
		t.Fatalf("disabled expansion = %q", got)
	}
	if got := enhancer.Rerank("question", results, 1); got[0].SourceDoc != "original" {
		t.Fatalf("disabled rerank = %#v", got)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	model := SearchModelConfig{APIKey: "test", Model: "model", BaseURL: server.URL}
	enhancer = NewSearchEnhancer(server.Client())
	enhancer.ConfigureHyDE(true, model)
	enhancer.ConfigureReranker("api", model)
	if got := enhancer.Expand("question"); got != "question" {
		t.Fatalf("failed expansion = %q", got)
	}
	if got := enhancer.Rerank("question", results, 1); got[0].SourceDoc != "original" {
		t.Fatalf("failed rerank = %#v", got)
	}
}
