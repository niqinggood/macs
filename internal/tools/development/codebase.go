package development

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/configvalue"
	"mac/pkg/codegraph"
)

type CodeFact struct {
	Subject   string
	Predicate string
	Object    string
}

// CodeKnowledgeStore is the narrow storage contract required by CodebaseTool.
type CodeKnowledgeStore interface {
	codegraph.KGStore
	RecentCodeFacts(limit int) ([]CodeFact, error)
}

type CodeKnowledgeStoreProvider func() CodeKnowledgeStore
type ProjectPathResolver func(context.Context) string

// CodebaseTool indexes Go symbols into a knowledge graph and reports its state.
type CodebaseTool struct {
	storeProvider       CodeKnowledgeStoreProvider
	projectPathResolver ProjectPathResolver
}

func NewCodebaseTool(storeProvider CodeKnowledgeStoreProvider, projectPathResolver ProjectPathResolver) *CodebaseTool {
	return &CodebaseTool{storeProvider: storeProvider, projectPathResolver: projectPathResolver}
}

func (tool *CodebaseTool) Name() string { return "codebase_index" }

func (tool *CodebaseTool) Description() string {
	return "Index a Go codebase into the knowledge graph. " +
		"Use this when the user asks about code structure, function relationships, " +
		"or wants to find definitions. After indexing, query the knowledge graph for symbols. " +
		"Actions: index (scan project), status (show summary)"
}

func (tool *CodebaseTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "description": "index (scan codebase), status (show summary)",
				"enum": []string{"index", "status"},
			},
			"path": map[string]interface{}{
				"type": "string", "description": "Project root path to index",
			},
		},
		"required": []string{"action"},
	}
}

func (tool *CodebaseTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch configvalue.String(params, "action", "status") {
	case "index":
		return tool.index(ctx, params)
	case "status":
		return tool.status()
	default:
		return "Unknown action", nil
	}
}

func (tool *CodebaseTool) store() CodeKnowledgeStore {
	if tool == nil || tool.storeProvider == nil {
		return nil
	}
	return tool.storeProvider()
}

func (tool *CodebaseTool) index(ctx context.Context, params map[string]interface{}) (string, error) {
	store := tool.store()
	if store == nil {
		return "Knowledge Graph not initialized", nil
	}
	path := strings.TrimSpace(configvalue.String(params, "path", ""))
	if path == "" && tool.projectPathResolver != nil {
		path = strings.TrimSpace(tool.projectPathResolver(ctx))
	}
	if path == "" {
		return "No path specified. Use session_context to set project_path first.", nil
	}
	graph, err := codegraph.ScanGoProject(path)
	if err != nil {
		return fmt.Sprintf("Scan failed: %v", err), nil
	}
	if err := graph.IndexToKG(store, path); err != nil {
		return fmt.Sprintf("Index failed: %v", err), nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Indexed %s: %d files, %d symbols\n\n", path, graph.Files, len(graph.Symbols)))
	kinds := map[string]int{}
	for _, symbol := range graph.Symbols {
		kinds[symbol.Kind]++
	}
	for _, kind := range []string{"const", "func", "interface", "method", "type", "var"} {
		if count := kinds[kind]; count > 0 {
			result.WriteString(fmt.Sprintf("- %s: %d\n", kind, count))
			delete(kinds, kind)
		}
	}
	for kind, count := range kinds {
		result.WriteString(fmt.Sprintf("- %s: %d\n", kind, count))
	}
	result.WriteString("\nUse knowledge graph queries to explore the codebase.")
	return result.String(), nil
}

func (tool *CodebaseTool) status() (string, error) {
	store := tool.store()
	if store == nil {
		return "Knowledge Graph not initialized", nil
	}
	facts, err := store.RecentCodeFacts(10)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(facts) == 0 {
		return "No code indexed yet. Use codebase_index action=index path=<project>.", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("KG has %d+ facts. Recent code symbols:\n", len(facts)))
	for _, fact := range facts {
		if fact.Predicate == "is_a" || fact.Predicate == "calls" {
			result.WriteString(fmt.Sprintf("- %s %s %s\n", fact.Subject, fact.Predicate, fact.Object))
		}
	}
	return result.String(), nil
}
