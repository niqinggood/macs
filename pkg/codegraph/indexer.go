package codegraph

import (
	"fmt"
	"strings"
)

// KGStore is the minimal interface for storing code symbols in a knowledge graph.
type KGStore interface {
	AddTriple(subject, predicate, object, source string, validFrom, validTo string, confidence float64) error
}

// IndexToKG feeds extracted code symbols into a knowledge graph.
func (g *Graph) IndexToKG(kg KGStore, projectPath string) error {
	today := "" // use empty for "now"

	for _, sym := range g.Symbols {
		entity := fmt.Sprintf("%s.%s", sym.Package, sym.Name)
		source := fmt.Sprintf("%s:%d", sym.File, sym.Line)

		// Core identity
		kg.AddTriple(projectPath, "contains", entity, source, today, "", 1.0)
		kg.AddTriple(entity, "is_a", sym.Kind, source, today, "", 1.0)
		kg.AddTriple(entity, "in_package", sym.Package, source, today, "", 1.0)

		if sym.Doc != "" {
			kg.AddTriple(entity, "doc", sym.Doc, source, today, "", 1.0)
		}

		// Method receiver
		if sym.Receiver != "" {
			receiverEntity := fmt.Sprintf("%s.%s", sym.Package, strings.TrimPrefix(sym.Receiver, "*"))
			kg.AddTriple(entity, "method_of", receiverEntity, source, today, "", 1.0)
		}

		// Function calls
		for _, call := range sym.Calls {
			callEntity := resolveCall(call, sym.Package)
			kg.AddTriple(entity, "calls", callEntity, source, today, "", 0.9)
		}

		// Struct fields
		for _, field := range sym.Fields {
			kg.AddTriple(entity, "has_field", field, source, today, "", 1.0)
		}

		// Interface methods
		for _, method := range sym.Methods {
			kg.AddTriple(entity, "requires", method, source, today, "", 1.0)
		}
	}

	// Import relationships
	for _, imp := range g.Imports {
		kg.AddTriple(fmt.Sprintf("pkg:%s", imp.SourcePkg), "imports", fmt.Sprintf("pkg:%s", imp.TargetPkg),
			"", today, "", 1.0)
	}

	return nil
}

// resolveCall tries to qualify a call with its package.
func resolveCall(call, currentPkg string) string {
	if strings.Contains(call, ".") {
		return call // already qualified e.g. "fmt.Println"
	}
	// Local function call
	return fmt.Sprintf("%s.%s", currentPkg, call)
}

// QueryHelpers provides natural-language-like queries over the indexed code graph.
type QueryHelpers struct {
	kg           KGStore
	projectPath  string
}

// NewQueryHelpers creates a query helper for a code graph.
func NewQueryHelpers(kg KGStore, projectPath string) *QueryHelpers {
	return &QueryHelpers{kg: kg, projectPath: projectPath}
}

// FindCallers returns entities that call the given function.
// Example: FindCallers("main.HandleRequest") -> ["api.Serve", "main.main"]
// Note: This is a pattern-based query on the KG triples.
func (q *QueryHelpers) FindDefinition(name string) string {
	return fmt.Sprintf(
		"KG: search triples where subject contains '%s' or object contains '%s'. "+
			"Use the knowledge graph queries to find definitions, callers, and relationships.",
		name, name,
	)
}

// BuildContextPrompt generates a compact code context for the agent.
func (q *QueryHelpers) BuildContextPrompt() string {
	return fmt.Sprintf(
		"Codebase indexed at: %s. "+
			"Use the knowledge graph to find functions, types, and call relationships. "+
			"Query by package.FuncName for specific symbols.",
		q.projectPath,
	)
}
