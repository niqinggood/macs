package projectctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	developmenttools "mac/internal/tools/development"
)

func TestProjectInstructionLoaderResolvesHierarchyAndPrecedence(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "services", "api")
	mustWriteProjectInstruction(t, filepath.Join(root, "AGENTS.md"), "root agents")
	mustWriteProjectInstruction(t, filepath.Join(target, "CLAUDE.md"), "nested claude")
	mustWriteProjectInstruction(t, filepath.Join(target, ".claude", "rules", "go.md"), `---
paths:
  - "**/*.go"
  - "go.mod"
---
go rule`)
	mustWriteProjectInstruction(t, filepath.Join(target, "AGENTS.override.md"), "nested override")

	loader := NewProjectInstructionLoader("")
	instructions, err := loader.Load(root, target)
	if err != nil {
		t.Fatalf("load project instructions: %v", err)
	}

	prompt := instructions.Prompt()
	order := []string{"root agents", "nested claude", "go rule", "nested override"}
	lastIndex := -1
	for _, value := range order {
		index := strings.Index(prompt, value)
		if index < 0 {
			t.Fatalf("prompt missing %q:\n%s", value, prompt)
		}
		if index <= lastIndex {
			t.Fatalf("prompt precedence is incorrect for %q:\n%s", value, prompt)
		}
		lastIndex = index
	}

	var rule *ProjectInstructionSource
	for index := range instructions.Sources {
		if instructions.Sources[index].Kind == "rule" {
			rule = &instructions.Sources[index]
			break
		}
	}
	if rule == nil {
		t.Fatal("expected a path-scoped rule source")
	}
	if got := strings.Join(rule.Paths, ","); got != "**/*.go,go.mod" {
		t.Fatalf("rule paths = %q", got)
	}
	if rule.Scope != "services/api" {
		t.Fatalf("rule scope = %q", rule.Scope)
	}
	if !strings.Contains(instructions.AuditText(), "AGENTS.override.md") {
		t.Fatalf("audit is missing override source:\n%s", instructions.AuditText())
	}
}

func TestProjectInstructionLoaderExpandsImportsAndAuditsSource(t *testing.T) {
	root := t.TempDir()
	mustWriteProjectInstruction(t, filepath.Join(root, "CLAUDE.md"), "before\n@docs/shared.md\nafter")
	mustWriteProjectInstruction(t, filepath.Join(root, "docs", "shared.md"), "shared guidance")

	loader := NewProjectInstructionLoader("")
	instructions, err := loader.Load(root, root)
	if err != nil {
		t.Fatalf("load project instructions: %v", err)
	}
	prompt := instructions.Prompt()
	for _, want := range []string{"before", "shared guidance", "after", "imported from docs/shared.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expanded prompt missing %q:\n%s", want, prompt)
		}
	}

	foundImport := false
	for _, source := range instructions.Sources {
		if source.Kind == "import" && source.RelativePath == "docs/shared.md" {
			foundImport = true
			if source.ImportedBy != "CLAUDE.md" {
				t.Fatalf("imported_by = %q", source.ImportedBy)
			}
		}
	}
	if !foundImport {
		t.Fatalf("import source missing from audit: %+v", instructions.Sources)
	}
}

func TestProjectInstructionLoaderCacheInvalidatesOnChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWriteProjectInstruction(t, path, "first")
	loader := NewProjectInstructionLoader("")

	first, err := loader.Load(root, root)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.CacheHit {
		t.Fatal("first load should not be a cache hit")
	}
	second, err := loader.Load(root, root)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !second.CacheHit {
		t.Fatal("unchanged second load should be a cache hit")
	}

	mustWriteProjectInstruction(t, path, "second version with a different size")
	third, err := loader.Load(root, root)
	if err != nil {
		t.Fatalf("third load: %v", err)
	}
	if third.CacheHit {
		t.Fatal("changed instructions must invalidate the cache")
	}
	if !strings.Contains(third.Prompt(), "second version") || strings.Contains(third.Prompt(), "first") {
		t.Fatalf("stale prompt after invalidation:\n%s", third.Prompt())
	}
}

func TestProjectInstructionLoaderHandlesCyclesAndBudget(t *testing.T) {
	root := t.TempDir()
	mustWriteProjectInstruction(t, filepath.Join(root, "AGENTS.md"), "@nested.md")
	mustWriteProjectInstruction(t, filepath.Join(root, "nested.md"), "@AGENTS.md\nnested content")
	loader := NewProjectInstructionLoader("")
	loader.maxBytes = 40

	instructions, err := loader.Load(root, root)
	if err != nil {
		t.Fatalf("load cyclic instructions: %v", err)
	}
	if !strings.Contains(instructions.Prompt(), "nested content") {
		t.Fatalf("non-cyclic content should survive:\n%s", instructions.Prompt())
	}
	foundCycleWarning := false
	for _, warning := range instructions.Warnings {
		if strings.Contains(warning, "cyclic") {
			foundCycleWarning = true
		}
	}
	if !foundCycleWarning {
		t.Fatalf("expected cycle warning: %+v", instructions.Warnings)
	}
}

func TestExternalAgentProjectContextUsesUnifiedLoaderWithoutLegacyTruncation(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("project context ", 300) + "\nTAIL_MARKER"
	mustWriteProjectInstruction(t, filepath.Join(root, "CLAUDE.md"), content)
	tool := developmenttools.NewExternalAgentTool(root, ExternalAgentProjectContext)

	context := tool.ProjectContext(root)
	if !strings.Contains(context, "TAIL_MARKER") {
		t.Fatalf("external agent context was unexpectedly truncated: %d bytes", len(context))
	}
}

func mustWriteProjectInstruction(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create instruction directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write instruction file: %v", err)
	}
}
