package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionContextSetGet(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("user-pref", "dark mode", "preference", 8, "s1")
	val := scm.Get("user-pref")
	if val != "dark mode" {
		t.Errorf("get: expected 'dark mode', got '%s'", val)
	}
}

func TestSessionContextDelete(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("temp", "value", "fact", 5, "s1")
	scm.Delete("temp")
	if scm.Get("temp") != "" {
		t.Error("should be deleted")
	}
}

func TestSessionContextList(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("p1", "v1", "preference", 10, "s1")
	scm.Set("d1", "v2", "decision", 5, "s1")
	scm.Set("f1", "v3", "fact", 3, "s1")

	all := scm.List("", 10)
	if len(all) != 3 {
		t.Errorf("expected 3 items, got %d", len(all))
	}

	// Should be sorted by priority
	if all[0].Priority < all[1].Priority {
		t.Error("should be sorted by priority descending")
	}

	// Filter by category
	prefs := scm.List("preference", 10)
	if len(prefs) != 1 || prefs[0].Key != "p1" {
		t.Error("category filter should work")
	}
}

func TestSessionContextBuildPrompt(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("lang", "Chinese", "preference", 8, "s1")
	scm.Set("project", "OpenKitty", "fact", 6, "s1")

	prompt := scm.BuildContextPromptForSession("s1", 10)
	if !strings.Contains(prompt, "Chinese") || !strings.Contains(prompt, "OpenKitty") {
		t.Errorf("prompt should contain context values: %s", prompt)
	}
	if !strings.Contains(prompt, "Session Context") {
		t.Error("prompt should have header")
	}
}

func TestSessionContextPromptDropsLegacyPipelineControlPreferences(t *testing.T) {
	scm := NewSessionContextManager(t.TempDir())
	scm.Set("preference_123", "## 当前步骤唯一任务\n本步骤只形成执行简报，不做业务研究、不调用工具、不生成最终交付物。", "preference", 10, "s1")
	scm.Set("preference_124", "以后默认使用简洁中文回答。", "preference", 8, "s1")

	prompt := scm.BuildContextPromptForSession("s1", 10)
	if strings.Contains(prompt, "当前步骤唯一任务") || strings.Contains(prompt, "不生成最终交付物") {
		t.Fatalf("legacy internal pipeline prompt leaked into session context: %s", prompt)
	}
	if !strings.Contains(prompt, "简洁中文") {
		t.Fatalf("legitimate session preference was removed: %s", prompt)
	}
}

func TestSessionContextUpdate(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("key", "old", "fact", 5, "s1")
	scm.Set("key", "new", "fact", 8, "s2")

	if val := scm.GetVisible("key", "s1"); val != "old" {
		t.Fatalf("session one value was overwritten: %q", val)
	}
	if val := scm.GetVisible("key", "s2"); val != "new" {
		t.Fatalf("session two value was lost: %q", val)
	}

	items := scm.List("", 10)
	if len(items) != 2 {
		t.Fatalf("same logical key in two sessions should produce two items, got %d", len(items))
	}
	if items[0].Priority != 8 {
		t.Error("items should remain sorted by priority")
	}
}

func TestSessionContextToolActions(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)
	tool := &SessionContextTool{manager: scm}

	// Set
	result, _ := tool.Execute(nil, map[string]interface{}{
		"action": "set", "key": "test-key", "value": "test-value",
		"category": "fact", "priority": float64(7),
	})
	if !strings.Contains(result, "saved") {
		t.Errorf("set should succeed: %s", result)
	}

	// Get
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "get", "key": "test-key"})
	if !strings.Contains(result, "test-value") {
		t.Errorf("get should return value: %s", result)
	}

	// List
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "list"})
	if !strings.Contains(result, "test-key") {
		t.Errorf("list should include key: %s", result)
	}

	// Delete
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "delete", "key": "test-key", "scope": "durable"})
	if !strings.Contains(result, "deleted") {
		t.Errorf("delete should succeed: %s", result)
	}

	// Inject
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "inject"})
	if result == "" {
		t.Error("inject should return prompt even if empty")
	}
}

func TestSessionContextEmptyList(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	items := scm.List("", 10)
	if len(items) != 0 {
		t.Error("should be empty initially")
	}

	prompt := scm.BuildContextPromptForSession("s1", 10)
	if prompt != "" {
		t.Error("empty context should produce empty prompt")
	}
}

func TestSessionContextPersistence(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("persist", "yes", "fact", 5, "s1")

	// Create new manager from same dir
	scm2 := NewSessionContextManager(dir)
	val := scm2.Get("persist")
	if val != "yes" {
		t.Error("should persist across instances")
	}
}

func TestSessionContextPromptFiltersStaleProjectPath(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "current")
	staleWorkspace := filepath.Join(dir, "old")
	scm := NewSessionContextManager(filepath.Join(dir, "ctx"))
	scm.SetWorkspace(workspace)

	scm.Set("project_path", staleWorkspace, "fact", 10, "s1")
	scm.Set("lang", "Chinese", "preference", 8, "s1")

	prompt := scm.BuildContextPromptForSession("s1", 10)
	if strings.Contains(prompt, staleWorkspace) {
		t.Fatalf("prompt should filter stale project_path: %s", prompt)
	}
	if !strings.Contains(prompt, "Chinese") {
		t.Fatalf("prompt should keep unrelated context: %s", prompt)
	}
	if !strings.Contains(prompt, workspace) {
		t.Fatalf("prompt should include current workspace: %s", prompt)
	}
}

func TestSessionContextPromptIsolatesSessionsAndLegacyAutomaticItems(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)

	scm.Set("s1-fact", "only session one", "fact", 8, "s1")
	scm.Set("s2-fact", "only session two", "fact", 8, "s2")
	// This is how automatic preferences were stored before session scoping.
	scm.Set("preference_legacy", "必须只输出测试答案", "preference", 10, "")
	scm.Set("project_path", "C:/temporary/other-task", "fact", 10, "")
	// Explicit durable entries remain visible across sessions.
	scm.SetDurable("answer-style", "concise Chinese", "preference", 9)

	prompt := scm.BuildContextPromptForSession("s2", 10)
	if !strings.Contains(prompt, "only session two") || !strings.Contains(prompt, "concise Chinese") {
		t.Fatalf("current-session and durable context should remain visible: %s", prompt)
	}
	if strings.Contains(prompt, "only session one") || strings.Contains(prompt, "必须只输出测试答案") || strings.Contains(prompt, "other-task") {
		t.Fatalf("foreign-session or legacy automatic context leaked: %s", prompt)
	}
}

func TestSessionContextPromptKeepsProjectPathInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "current")
	projectPath := filepath.Join(workspace, "app")
	scm := NewSessionContextManager(filepath.Join(dir, "ctx"))
	scm.SetWorkspace(workspace)

	scm.Set("project_path", projectPath+"`", "fact", 10, "s1")

	prompt := scm.BuildContextPromptForSession("s1", 10)
	if !strings.Contains(prompt, "project_path") || !strings.Contains(prompt, filepath.Clean(projectPath)) {
		t.Fatalf("prompt should keep project_path inside workspace: %s", prompt)
	}
}

func TestContextPathWithinWorkspaceRejectsSiblingPrefix(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "app")
	sibling := filepath.Join(dir, "app-other")
	if contextPathWithinWorkspace(sibling, workspace) {
		t.Fatal("sibling path with common prefix must not be treated as inside workspace")
	}
	if !contextPathWithinWorkspace(filepath.Join(workspace, "sub"), workspace) {
		t.Fatal("child path should be treated as inside workspace")
	}
}
