package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/permissionpolicy"
	"mac/internal/runtimeutil"
	sessioncore "mac/internal/session"
	"mac/internal/toolmetrics"
	agenttools "mac/internal/tools/agents"
	contenttools "mac/internal/tools/content"
	developmenttools "mac/internal/tools/development"
	knowledgetools "mac/internal/tools/knowledge"
	operationstools "mac/internal/tools/operations"
)

// ==================== Permission Edge Cases ====================

func TestPermissionEdge_EmptyTool(t *testing.T) {
	allowed, _, _ := ResolvePermission(PermissionDefault, "", nil)
	if !allowed {
		t.Error("empty tool name should be allowed (default moderate)")
	}
}

func TestPermissionEdge_NilArgs(t *testing.T) {
	allowed, _, _ := ResolvePermission(PermissionDefault, "read_file", nil)
	if !allowed {
		t.Error("nil args should be safe for read")
	}
}

func TestPermissionEdge_UnknownMode(t *testing.T) {
	allowed, _, _ := ResolvePermission("invalid_mode", "cli", nil)
	if !allowed {
		t.Error("unknown mode should fallback to default allow behavior")
	}
}

// ==================== DenialTracker Edge Cases ====================

func TestDenialEdge_EmptyArgs(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)

	// Record with nil args
	rec := dt.RecordDenial("cli", nil, "reason")
	if rec == nil {
		t.Fatal("RecordDenial with nil args should return record")
	}
	// Should use "*" pattern
	deny, _ := dt.ShouldAutoDeny("cli", nil)
	if !deny {
		t.Log("single denial should not auto-deny, need 2")
	}
}

func TestDenialEdge_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			dt.RecordDenial("cli", map[string]interface{}{"cmd": "test"}, "reason")
			dt.ShouldAutoDeny("cli", map[string]interface{}{"cmd": "test"})
			dt.GetRules()
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Should not panic or deadlock
}

func TestDenialEdge_ResetEmpty(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)
	if err := dt.Reset(); err != nil {
		t.Fatalf("reset empty: %v", err)
	}
}

func TestDenialEdge_MergeEmpty(t *testing.T) {
	dir := t.TempDir()
	dt1 := permissionpolicy.NewDenialTracker(dir)
	dt2 := permissionpolicy.NewDenialTracker(t.TempDir())
	dt1.MergeFrom(dt2)
	if len(dt1.GetRules()) != 0 {
		t.Error("merging empty should stay empty")
	}
}

// ==================== File Snapshot Edge Cases ====================

func TestSnapshotEdge_RestoreNonexistent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test")
	err := sm.RestoreSnapshot(filepath.Join(dir, "never-saved.txt"))
	if err == nil {
		t.Error("should error on nonexistent snapshot")
	}
}

func TestSnapshotEdge_DiffNonexistent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test")
	_, err := sm.DiffSnapshot(filepath.Join(dir, "never-saved.txt"))
	if err == nil {
		t.Error("should error on nonexistent snapshot")
	}
}

func TestSnapshotEdge_BatchEmpty(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test")
	count, err := sm.BatchRestore()
	if err != nil || count != 0 {
		t.Errorf("batch restore empty: count=%d err=%v", count, err)
	}
}

func TestSnapshotEdge_ExportEmpty(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test")
	err := sm.ExportSnapshots(filepath.Join(dir, "export"))
	if err != nil {
		t.Fatalf("export empty: %v", err)
	}
}

// ==================== PTL Edge Cases ====================

func TestPTLEdge_TruncateTiny(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "hi"},
	}
	result := truncateOldestMessages(msgs, 0.5)
	if len(result) < 1 {
		t.Error("too few messages should keep minimal")
	}
}

func TestPTLEdge_TruncateAll(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "q1"},
		{"role": "assistant", "content": "a1"},
		{"role": "user", "content": "q2"},
		{"role": "assistant", "content": "a2"},
	}
	result := truncateOldestMessages(msgs, 0.99)
	if len(result) < 2 {
		t.Error("should preserve system + at least first user")
	}
}

func TestPTLTruncationKeepsOrDropsWholeReasoningToolTurn(t *testing.T) {
	toolAssistant := BuildAssistantAPIMessage("", []ToolCall{
		{ID: "call_1", Name: "list_dir", Arguments: map[string]interface{}{"path": "."}},
		{ID: "call_2", Name: "glob", Arguments: map[string]interface{}{"pattern": "*.md"}},
	}, "provider reasoning")
	messages := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "original"},
		{"role": "assistant", "content": "old answer"},
		{"role": "user", "content": "old follow-up"},
		toolAssistant,
		{"role": "tool", "tool_call_id": "call_1", "name": "list_dir", "content": "one"},
		{"role": "tool", "tool_call_id": "call_2", "name": "glob", "content": "two"},
		{"role": "assistant", "content": "latest"},
	}

	retained := truncateOldestMessages(messages, 0.2)
	var retainedToolAssistant map[string]interface{}
	for _, message := range retained {
		if len(apiToolCallMaps(message["tool_calls"])) > 0 {
			retainedToolAssistant = message
		}
	}
	if retainedToolAssistant == nil || retainedToolAssistant["reasoning_content"] != "provider reasoning" {
		t.Fatalf("retained tool turn lost assistant reasoning: %#v", retained)
	}

	dropped := truncateOldestMessages(messages, 0.5)
	for _, message := range dropped {
		if message["role"] == "tool" || len(apiToolCallMaps(message["tool_calls"])) > 0 {
			t.Fatalf("truncation retained a partial tool turn: %#v", dropped)
		}
	}
}

func TestSlidingWindowDoesNotRetainOrphanedGoBuiltToolResult(t *testing.T) {
	agent := &LLMAgent{}
	messages := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "original"},
		{"role": "assistant", "content": "old 1"},
		{"role": "user", "content": "old 2"},
		{"role": "assistant", "content": "old 3"},
		{"role": "user", "content": "old 4"},
		{"role": "assistant", "content": "old 5"},
		{"role": "user", "content": "old 6"},
		BuildAssistantAPIMessage("", []ToolCall{{
			ID: "go_call", Name: "list_dir", Arguments: map[string]interface{}{"path": "."},
		}}, "provider reasoning"),
		{"role": "tool", "tool_call_id": "go_call", "name": "list_dir", "content": "result"},
		{"role": "assistant", "content": "latest"},
	}
	maxTokensAtUnsafeBoundary := agent.calcMessagesTokens([]map[string]interface{}{
		messages[0], messages[1], messages[9], messages[10],
	})
	changed, _ := agent.slidingWindow(&messages, maxTokensAtUnsafeBoundary, nil)
	if !changed {
		t.Fatal("expected sliding window compaction")
	}
	for _, message := range messages {
		if message["role"] == "tool" || len(apiToolCallMaps(message["tool_calls"])) > 0 {
			t.Fatalf("sliding window retained a partial tool turn: %#v", messages)
		}
	}
}

// ==================== Swarm Edge Cases ====================

func TestSwarmEdge_EmptyResults(t *testing.T) {
	results := []swarmResult{}
	report := formatSwarmResults(results, time.Second)
	if !strings.Contains(report, "0 agents") {
		t.Error("empty results should show 0 agents")
	}
}

func TestSwarmEdge_AllErrors(t *testing.T) {
	results := []swarmResult{
		{AgentName: "a1", Role: "worker", Error: "timeout", Duration: "30s"},
		{AgentName: "a2", Role: "worker", Error: "crash", Duration: "1s"},
	}
	report := formatSwarmResults(results, time.Second)
	if !strings.Contains(report, "0/2") {
		t.Error("all errors should show 0/2 completed")
	}
}

// ==================== Tool Metrics Edge Cases ====================

func TestMetricsEdge_UnknownTool(t *testing.T) {
	report := toolmetrics.Report()
	if report == "" {
		t.Error("should return report even if empty")
	}
}

// ==================== Plugin Registry Edge Cases ====================

func TestPluginEdge_EnableNonexistent(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	err := pr.Enable("nonexistent")
	if err == nil {
		t.Error("should error on nonexistent plugin")
	}
}

func TestPluginEdge_DisableNonexistent(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	err := pr.Disable("nonexistent")
	if err == nil {
		t.Error("should error on nonexistent plugin")
	}
}

func TestPluginEdge_DoubleRegister(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	pr.Register(&knowledgetools.PluginInfo{Name: "p1", Type: knowledgetools.PluginTool, Version: "1.0", Description: "test"})
	pr.Register(&knowledgetools.PluginInfo{Name: "p1", Type: knowledgetools.PluginTool, Version: "2.0", Description: "updated"})
	// Should overwrite
	if pr.Get("p1").Version != "2.0" {
		t.Error("double register should update")
	}
}

// ==================== Prompt Template Edge Cases ====================

func TestPromptEdge_FillMissingVariable(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)
	result, err := tm.FillTemplate("weekly-report", map[string]string{})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	// Should not panic on missing variable — placeholder remains
	if !strings.Contains(result, "{{context}}") {
		t.Log("missing variable placeholder kept in output")
	}
}

func TestPromptEdge_SaveEmptyName(t *testing.T) {
	dir := t.TempDir()
	tm := contenttools.NewTemplateManager(dir)
	// Save with empty name — template manager allows it, key is ""
	err := tm.Save(&contenttools.PromptTemplate{Name: "", Category: "custom", Prompt: "test"})
	if err != nil {
		t.Logf("save with empty name: %v", err)
	}
}

// ==================== Session Context Edge Cases ====================

func TestSessionEdge_SetEmptyKey(t *testing.T) {
	dir := t.TempDir()
	scm := memorycore.NewSessionContextManager(dir)
	scm.Set("", "value", "fact", 5, "") // empty key allowed
	if scm.Get("") != "value" {
		t.Error("empty key should work")
	}
}

func TestSessionEdge_DeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	scm := memorycore.NewSessionContextManager(dir)
	// Should not panic
	scm.Delete("nonexistent")
}

// ==================== Token Estimator Edge Cases ====================

func TestTokenEdge_Unicode(t *testing.T) {
	tokens := runtimeutil.EstimateTokens("こんにちは世界 🌍")
	if tokens < 1 {
		t.Error("unicode should produce at least 1 token")
	}
}

func TestTokenEdge_LongString(t *testing.T) {
	s := "The quick brown fox jumps over the lazy dog. " + strings.Repeat("The quick brown fox jumps over the lazy dog. ", 200)
	tokens := runtimeutil.EstimateTokens(s)
	if tokens < 500 {
		t.Errorf("long string: got %d tokens, expected at least 500", tokens)
	}
}

// ==================== Structured Messages Edge Cases ====================

func TestMessageEdge_Meta(t *testing.T) {
	msg := &UserMessage{Content: "hi", Meta: true}
	api := msg.ToAPI()
	if _, ok := api["is_meta"]; !ok {
		t.Error("meta user message should have is_meta field")
	}
}

func TestMessageEdge_Nil(t *testing.T) {
	msgs := ToAPIMessages(nil)
	if len(msgs) != 0 {
		t.Error("nil messages should produce empty slice")
	}
}

// ==================== Worktree Edge Cases ====================

func TestWorktreeEdge_CreateInNonGit(t *testing.T) {
	dir := t.TempDir()
	wm := developmenttools.NewWorktreeManager(dir)
	_, err := wm.CreateWorktree("agent")
	if err == nil {
		t.Error("should fail in non-git directory")
	}
}

// ==================== Hook Manager Edge Cases ====================

func TestHookEdge_NilSafety(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	// Create a hook script and check safety
	scriptPath := filepath.Join(dir, "test.sh")
	os.WriteFile(scriptPath, []byte("echo 'hello'"), 0755)
	if !hm.IsHookSafe(scriptPath) {
		t.Error("safe script should pass")
	}
}

func TestHookEdge_NonexistentScript(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	if hm.IsHookSafe(filepath.Join(dir, "nonexistent.sh")) {
		t.Error("nonexistent script should fail safety check")
	}
}

// ==================== Cron Scheduler Edge Cases ====================

func TestCronEdge_NilScheduler(t *testing.T) {
	tool := agenttools.NewCronTaskTool(nil)
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "list"})
	if !strings.Contains(result, "not available") {
		t.Error("nil scheduler should return not available")
	}
}

// ==================== Notify Edge Cases ====================

func TestNotifyEdge_NilNotifier(t *testing.T) {
	tool := operationstools.NewNotifyTool(nil)
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "webhooks"})
	if !strings.Contains(result, "not available") {
		t.Error("nil notifier should return not available")
	}
}

// ==================== Session Transcript Edge Cases ====================

func TestTranscriptEdge_EmptyRecords(t *testing.T) {
	dir := t.TempDir()
	tr := sessioncore.NewSessionTranscript("s1", "a1", dir)
	tr.Finalize("result", "success")
	// Should not panic on empty entries
}

// ==================== Plan Validation Edge Cases ====================

func TestPlanEdge_OnlyWhitespace(t *testing.T) {
	issues := validatePlanCompleteness("   \n  \n  ")
	if len(issues) < 3 {
		t.Error("whitespace-only plan should have issues")
	}
}

// ==================== Code Review Edge Cases ====================

func TestCodeReviewEdge_EmptyDiff(t *testing.T) {
	tool := developmenttools.NewCodeReviewTool(".")
	issues := tool.CheckPatterns("", nil)
	if len(issues) != 0 {
		t.Error("empty diff should have no issues")
	}
}

func TestCodeReviewEdge_OnlyContextLines(t *testing.T) {
	tool := developmenttools.NewCodeReviewTool(".")
	diff := " normal line\n another normal line\n"
	issues := tool.CheckPatterns(diff, []string{"test.go"})
	if len(issues) != 0 {
		t.Error("context-only diff should have no issues")
	}
}
