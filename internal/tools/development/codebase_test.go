package development

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type codebaseTestStore struct {
	triples []CodeFact
	facts   []CodeFact
}

func (store *codebaseTestStore) AddTriple(subject, predicate, object, source, validFrom, validTo string, confidence float64) error {
	store.triples = append(store.triples, CodeFact{Subject: subject, Predicate: predicate, Object: object})
	return nil
}

func (store *codebaseTestStore) RecentCodeFacts(limit int) ([]CodeFact, error) {
	return store.facts, nil
}

func TestCodebaseToolIndexesResolvedProject(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\nfunc main() { run() }\nfunc run() {}\n"), 0644); err != nil {
		t.Fatalf("write Go source: %v", err)
	}
	store := &codebaseTestStore{}
	tool := NewCodebaseTool(func() CodeKnowledgeStore { return store }, func(context.Context) string { return project })
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "index"})
	if err != nil {
		t.Fatalf("index project: %v", err)
	}
	if !strings.Contains(result, "2 symbols") || len(store.triples) == 0 {
		t.Fatalf("project was not indexed: result=%q triples=%#v", result, store.triples)
	}
}

func TestCodebaseToolStatusUsesInjectedStore(t *testing.T) {
	store := &codebaseTestStore{facts: []CodeFact{
		{Subject: "main.run", Predicate: "is_a", Object: "func"},
		{Subject: "main.main", Predicate: "calls", Object: "main.run"},
	}}
	tool := NewCodebaseTool(func() CodeKnowledgeStore { return store }, nil)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "status"})
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(result, "main.main calls main.run") {
		t.Fatalf("unexpected status: %q", result)
	}
}

func TestCodebaseToolFailsClosedWithoutStore(t *testing.T) {
	result, err := (&CodebaseTool{}).Execute(context.Background(), map[string]interface{}{"action": "status"})
	if err != nil || result != "Knowledge Graph not initialized" {
		t.Fatalf("unexpected unconfigured result: result=%q err=%v", result, err)
	}
}
