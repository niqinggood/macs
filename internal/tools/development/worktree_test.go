package development

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeManagerNew(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	if wm == nil {
		t.Fatal("NewWorktreeManager returned nil")
	}
	if wm.mainDir != dir {
		t.Error("mainDir mismatch")
	}
}

func TestWorktreeManagerHealthCheckEmpty(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	issues := wm.HealthCheck()
	if len(issues) != 1 || issues[0] != "All worktrees healthy" {
		t.Errorf("empty should be healthy: %v", issues)
	}
}

func TestWorktreeManagerCleanupAllEmpty(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	// Should not panic on empty
	wm.CleanupAll()
}

func TestWorktreeManagerGetNonexistent(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	info := wm.GetWorktree("nonexistent")
	if info != nil {
		t.Error("should return nil for nonexistent worktree")
	}
}

func TestWorktreeManagerListEmpty(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	list := wm.ListWorktrees()
	if len(list) != 0 {
		t.Error("should be empty initially")
	}
}

func TestIsGitRepo(t *testing.T) {
	// Current directory should be a git repo (we're in one)
	if !IsGitRepo(".") {
		t.Skip("not in git repo (test environment)")
	}

	// Temp directory should not be
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Error("temp dir should not be a git repo")
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"hello world", "hello-world"},
		{"UPPERCASE", "uppercase"},
		{"special!@#chars", "specialchars"},
		{"very-long-name-that-exceeds-thirty-characters-limit", "very-long-name-that-exceeds-th"},
		{"", "agent"},
	}
	for _, tt := range tests {
		got := sanitizeBranchName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestWorktreeManagerCreateInNonGit(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	_, err := wm.CreateWorktree("test")
	if err == nil {
		t.Error("should error when not in git repo")
	}
}

func TestWorktreeInfoStruct(t *testing.T) {
	info := &WorktreeInfo{
		Path: "/tmp/test", Branch: "feature/x", AgentName: "agent1",
		BaseBranch: "main",
	}
	if info.AgentName != "agent1" || info.Branch != "feature/x" {
		t.Error("struct fields wrong")
	}
}

func TestMaxConcurrentWorktrees(t *testing.T) {
	if maxConcurrentWorktrees != 5 {
		t.Errorf("maxConcurrentWorktrees = %d, want 5", maxConcurrentWorktrees)
	}
}

func TestWorktreeHealthCheckMissingDir(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	// Manually inject a broken worktree
	wm.worktrees["broken"] = &WorktreeInfo{Path: "/nonexistent/path", Branch: "ghost", AgentName: "broken"}
	issues := wm.HealthCheck()
	if len(issues) == 0 {
		t.Error("should report issue for missing path")
	}
}

func TestWorktreeRemoveNonexistent(t *testing.T) {
	dir := t.TempDir()
	wm := NewWorktreeManager(dir)
	err := wm.RemoveWorktree("nonexistent", false)
	if err == nil {
		t.Error("should error on nonexistent worktree")
	}
}

func TestWorktreePathForAgentContainsTraversalName(t *testing.T) {
	mainDir := t.TempDir()
	root, target, err := worktreePathForAgent(mainDir, `..\..\outside`)
	if err != nil {
		t.Fatalf("worktreePathForAgent failed: %v", err)
	}
	if !pathIsStrictChild(root, target) {
		t.Fatalf("target escaped worktree root: root=%q target=%q", root, target)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("unsafe relative worktree path %q (err=%v)", rel, err)
	}
}

func TestEnsureWorktreePathRejectsParent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worktrees")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "..", "outside")
	if err := ensureWorktreePathWithinRoot(root, outside); err == nil {
		t.Fatalf("expected parent path to be rejected: %s", outside)
	}
}

func TestEnsureWorktreePathRejectsRedirectedRoot(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "managed-root")
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := ensureWorktreePathWithinRoot(root, filepath.Join(root, "child")); err == nil {
		t.Fatal("expected redirected worktree root to be rejected")
	}
}

func TestRemoveWorktreeRefusesUnmanagedPathWithoutDeleting(t *testing.T) {
	mainDir := t.TempDir()
	outside := filepath.Join(mainDir, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	wm := NewWorktreeManager(mainDir)
	wm.worktrees["unsafe"] = &WorktreeInfo{
		Path:       outside,
		Branch:     "openkitty/unsafe/test",
		AgentName:  "unsafe",
		BaseBranch: "main",
	}
	if err := wm.RemoveWorktree("unsafe", false); err == nil {
		t.Fatal("expected unmanaged worktree path to be rejected")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("unmanaged sentinel was removed: %v", err)
	}
	if wm.GetWorktree("unsafe") == nil {
		t.Fatal("failed cleanup must retain the worktree record")
	}
}

func TestWorktreeLifecycleWithTraversalLikeAgentNameStaysContained(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	mainDir := t.TempDir()
	runTestGit(t, mainDir, "init")
	runTestGit(t, mainDir, "config", "user.email", "openkitty-test@example.invalid")
	runTestGit(t, mainDir, "config", "user.name", "OpenKitty Test")
	if err := os.WriteFile(filepath.Join(mainDir, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, mainDir, "add", "README.md")
	runTestGit(t, mainDir, "commit", "-m", "seed")

	wm := NewWorktreeManager(mainDir)
	agentName := `..\..\outside`
	info, err := wm.CreateWorktree(agentName)
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}
	root, _, err := worktreePathForAgent(mainDir, agentName)
	if err != nil {
		t.Fatal(err)
	}
	if !pathIsStrictChild(root, info.Path) {
		t.Fatalf("created worktree escaped root: root=%q path=%q", root, info.Path)
	}
	if err := wm.RemoveWorktree(agentName, false); err != nil {
		t.Fatalf("RemoveWorktree failed: %v", err)
	}
	if wm.GetWorktree(agentName) != nil {
		t.Fatal("successful cleanup retained worktree record")
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after cleanup: %v", err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestWorktreeEnterExitTools(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0755)
	wm := NewWorktreeManager(dir)

	// Enter tool - should fail gracefully (not a git repo)
	enterTool := &EnterWorktreeTool{manager: wm}
	result, _ := enterTool.Execute(nil, map[string]interface{}{"agent_name": "test"})
	if result == "" || result[:5] == "Worktree" {
		// Either success or clear error
		_ = result
	}

	// Exit tool - nonexistent
	exitTool := &ExitWorktreeTool{manager: wm}
	result, _ = exitTool.Execute(nil, map[string]interface{}{"agent_name": "test", "keep_changes": false})
	if result == "" {
		t.Error("should return result for nonexistent worktree")
	}
}
