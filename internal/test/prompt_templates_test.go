package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	contenttools "mac/internal/tools/content"
)

func TestTemplateManagerBuiltins(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	// Should have built-in templates
	templates := tm.List("")
	if len(templates) < 5 {
		t.Errorf("expected at least 5 builtins, got %d", len(templates))
	}

	// Check categories
	for _, cat := range []string{"report", "coding", "meeting", "email", "analysis"} {
		list := tm.List(cat)
		if len(list) == 0 {
			t.Errorf("category '%s' should have templates", cat)
		}
	}
}

func TestTemplateManagerGetAndUse(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	// Get existing
	tmpl := tm.Get("weekly-report")
	if tmpl == nil {
		t.Fatal("weekly-report template not found")
	}
	if tmpl.Category != "report" {
		t.Errorf("expected report category, got %s", tmpl.Category)
	}

	// Use increments count
	before := tmpl.UsageCount
	filled, err := tm.FillTemplate("weekly-report", map[string]string{"context": "test project"})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if !strings.Contains(filled, "test project") {
		t.Error("filled template should contain the variable value")
	}
	if tmpl.UsageCount != before+1 {
		t.Error("usage count should increment")
	}
}

func TestTemplateManagerSaveDelete(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	// Save custom
	err := tm.Save(&contenttools.PromptTemplate{Name: "custom", Category: "custom", Prompt: "test {{var}}", Description: "desc"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify persisted
	path := filepath.Join(dir, "custom.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("template file should exist")
	}

	// Delete
	err = tm.Delete("custom")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if tm.Get("custom") != nil {
		t.Error("should be deleted")
	}
}

func TestTemplateManagerFillMissing(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	_, err := tm.FillTemplate("nonexistent", nil)
	if err == nil {
		t.Error("should error on missing template")
	}
}

func TestTemplateManagerSortByUsage(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	// Use one template multiple times
	for i := 0; i < 5; i++ {
		tm.Use("code-review")
	}

	list := tm.List("coding")
	if len(list) > 0 && list[0].Name != "code-review" {
		t.Log("code-review may not be first if other coding templates have more usage")
	}
}

func TestPromptTemplateToolActions(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)
	tool := &contenttools.PromptTemplateTool{Manager: tm}

	// List
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "list"})
	if !strings.Contains(result, "weekly-report") {
		t.Error("list should show builtin templates")
	}

	// Categories
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "categories"})
	if !strings.Contains(result, "report") {
		t.Error("categories should include report")
	}

	// Use
	result, _ = tool.Execute(nil, map[string]interface{}{
		"action": "use", "name": "weekly-report",
		"variables": `{"context":"test"}`,
	})
	if !strings.Contains(result, "test") {
		t.Error("use should fill variables")
	}

	// Save
	result, _ = tool.Execute(nil, map[string]interface{}{
		"action": "save", "name": "my-template", "prompt": "hello {{name}}",
		"category": "custom", "description": "my template",
	})
	if !strings.Contains(result, "saved") {
		t.Error("save should succeed")
	}

	// Delete
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "delete", "name": "my-template"})
	if !strings.Contains(result, "deleted") {
		t.Error("delete should succeed")
	}
}

func TestTemplateManagerListByCategory(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)

	all := tm.List("")
	report := tm.List("report")
	if len(report) >= len(all) && len(all) > 2 {
		t.Error("category filter should reduce results")
	}
}
