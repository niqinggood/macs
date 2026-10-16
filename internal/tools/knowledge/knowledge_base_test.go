package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeBaseToolSearchesExternalServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Guide","content":"answer","score":0.9}]}`))
	}))
	t.Cleanup(server.Close)

	tool := NewKnowledgeBaseTool(server.URL, "", KnowledgeBaseDependencies{})
	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "question",
		"mode":  "external",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Guide") || !strings.Contains(got, "answer") {
		t.Fatalf("external result = %q", got)
	}
}

func TestKnowledgeBaseToolFallsBackToMarkdownFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "guide.md"), []byte("OpenKitty modular guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewKnowledgeBaseTool("", directory, KnowledgeBaseDependencies{})
	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "modular",
		"mode":  "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "guide.md") {
		t.Fatalf("local result = %q", got)
	}
}

func TestKnowledgeBaseToolRequiresTenantAuthorizer(t *testing.T) {
	tool := NewKnowledgeBaseTool("", "", KnowledgeBaseDependencies{
		Identity: func(context.Context) (string, bool) { return "tenant-a", true },
	})
	_, err := tool.Execute(context.Background(), map[string]interface{}{"query": "question"})
	if err == nil || !strings.Contains(err.Error(), "authorization is unavailable") {
		t.Fatalf("tenant error = %v", err)
	}
}
