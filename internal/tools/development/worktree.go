package development

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mac/internal/gitutil"
)

// WorktreeManager tracks active git worktrees created by sub-agents.
// Claude Code borrowed: EnterWorktreeTool/ExitWorktreeTool
type WorktreeManager struct {
	mu        sync.Mutex
	worktrees map[string]*WorktreeInfo // agentName -> info
	mainDir   string                   // original working directory
}

type WorktreeInfo struct {
	Path       string    // worktree directory path
	Branch     string    // branch name
	AgentName  string    // which agent created it
	CreatedAt  time.Time // when it was created
	BaseBranch string    // branch it was created from
}

func cloneWorktreeInfo(info *WorktreeInfo) *WorktreeInfo {
	if info == nil {
		return nil
	}
	clone := *info
	return &clone
}

func NewWorktreeManager(mainDir string) *WorktreeManager {
	if abs, err := filepath.Abs(mainDir); err == nil {
		mainDir = abs
	}
	if resolved, err := filepath.EvalSymlinks(mainDir); err == nil {
		mainDir = resolved
	}
	return &WorktreeManager{
		worktrees: make(map[string]*WorktreeInfo),
		mainDir:   mainDir,
	}
}

func worktreeRootPath(mainDir string) (string, error) {
	root, err := filepath.Abs(filepath.Join(mainDir, ".openkitty", "worktrees"))
	if err != nil {
		return "", fmt.Errorf("resolve worktree root: %w", err)
	}
	return filepath.Clean(root), nil
}

func safeWorktreeDirName(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "agent"
	}
	sum := sha256.Sum256([]byte(agentName))
	return fmt.Sprintf("%s-%x", sanitizeBranchName(agentName), sum[:4])
}

func worktreePathForAgent(mainDir, agentName string) (root, target string, err error) {
	root, err = worktreeRootPath(mainDir)
	if err != nil {
		return "", "", err
	}
	target = filepath.Join(root, safeWorktreeDirName(agentName))
	if err := ensureWorktreePathWithinRoot(root, target); err != nil {
		return "", "", err
	}
	return root, target, nil
}

func ensureWorktreePathWithinRoot(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve worktree root: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve worktree target: %w", err)
	}
	if !pathIsStrictChild(absRoot, absTarget) {
		return fmt.Errorf("worktree path escapes root: %s", target)
	}
	resolvedRoot := absRoot
	if _, statErr := os.Lstat(absRoot); statErr == nil {
		resolved, resolveErr := filepath.EvalSymlinks(absRoot)
		if resolveErr != nil {
			return fmt.Errorf("resolve worktree root: %w", resolveErr)
		}
		// The managed root itself must be a real directory, not a redirect to
		// another part of the filesystem.
		if !strings.EqualFold(filepath.Clean(absRoot), filepath.Clean(resolved)) {
			return fmt.Errorf("worktree root resolves outside its managed path: %s", root)
		}
		resolvedRoot = resolved
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect worktree root: %w", statErr)
	}

	// Existing paths may be symlinks or Windows junctions. Validate their
	// resolved destinations before any recursive cleanup is attempted.
	if _, statErr := os.Lstat(absTarget); statErr == nil {
		resolvedTarget, targetErr := filepath.EvalSymlinks(absTarget)
		if targetErr != nil {
			return fmt.Errorf("resolve existing worktree path: %w", targetErr)
		}
		if !pathIsStrictChild(resolvedRoot, resolvedTarget) {
			return fmt.Errorf("worktree path resolves outside root: %s", target)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect worktree path: %w", statErr)
	}
	return nil
}

func pathIsStrictChild(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func removeManagedWorktreePath(root, target string) error {
	if err := ensureWorktreePathWithinRoot(root, target); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove managed worktree path: %w", err)
	}
	return nil
}

func runGitIn(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// CreateWorktree creates a new git worktree for an agent on a temporary branch.
const maxConcurrentWorktrees = 5

func (wm *WorktreeManager) CreateWorktree(agentName string) (*WorktreeInfo, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "agent"
	}
	worktreeRoot, worktreePath, err := worktreePathForAgent(wm.mainDir, agentName)
	if err != nil {
		return nil, err
	}

	// Check concurrent worktree limit
	if len(wm.worktrees) >= maxConcurrentWorktrees {
		return nil, fmt.Errorf("max concurrent worktrees reached (%d). Close some first.", maxConcurrentWorktrees)
	}

	// Check if agent already has a worktree
	if existing, ok := wm.worktrees[agentName]; ok {
		return cloneWorktreeInfo(existing), nil
	}

	if !gitutil.IsRepository(wm.mainDir) {
		return nil, fmt.Errorf("not a git repository: %s", wm.mainDir)
	}
	if err := os.MkdirAll(worktreeRoot, 0755); err != nil {
		return nil, fmt.Errorf("create worktree root: %w", err)
	}
	if err := ensureWorktreePathWithinRoot(worktreeRoot, worktreePath); err != nil {
		return nil, err
	}

	// Get current branch as base
	baseBranch := "main"
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = wm.mainDir
	if out, err := cmd.Output(); err == nil {
		baseBranch = strings.TrimSpace(string(out))
	}

	// Create unique branch name
	branchName := fmt.Sprintf("openkitty/%s/%d", sanitizeBranchName(agentName), time.Now().UnixNano())

	// Ensure the branch doesn't already exist
	_, _ = runGitIn(wm.mainDir, "branch", "-D", branchName)

	// Create the branch from current HEAD
	cmd = exec.Command("git", "branch", branchName)
	cmd.Dir = wm.mainDir
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to create branch '%s': %w", branchName, err)
	}

	// Remove existing worktree if any
	_, _ = runGitIn(wm.mainDir, "worktree", "remove", "--force", worktreePath)
	if err := removeManagedWorktreePath(worktreeRoot, worktreePath); err != nil {
		_, _ = runGitIn(wm.mainDir, "branch", "-D", branchName)
		return nil, err
	}

	// Create the worktree
	cmd = exec.Command("git", "worktree", "add", worktreePath, branchName)
	cmd.Dir = wm.mainDir
	if out, err := cmd.CombinedOutput(); err != nil {
		_, _ = runGitIn(wm.mainDir, "branch", "-D", branchName)
		return nil, fmt.Errorf("failed to create worktree: %s — %w", string(out), err)
	}

	info := &WorktreeInfo{
		Path:       worktreePath,
		Branch:     branchName,
		AgentName:  agentName,
		CreatedAt:  time.Now(),
		BaseBranch: baseBranch,
	}
	wm.worktrees[agentName] = info

	log.Printf("[WORKTREE] Created worktree '%s' for agent '%s' (branch: %s)", worktreePath, agentName, branchName)
	return cloneWorktreeInfo(info), nil
}

// RemoveWorktree removes a worktree and optionally merges changes back.
func (wm *WorktreeManager) RemoveWorktree(agentName string, keepChanges bool) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "agent"
	}
	info, ok := wm.worktrees[agentName]
	if !ok {
		return fmt.Errorf("no worktree found for agent '%s'", agentName)
	}
	worktreeRoot, expectedPath, err := worktreePathForAgent(wm.mainDir, agentName)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(info.Path), filepath.Clean(expectedPath)) {
		return fmt.Errorf("refusing unmanaged worktree path for agent '%s': %s", agentName, info.Path)
	}
	if err := ensureWorktreePathWithinRoot(worktreeRoot, info.Path); err != nil {
		return err
	}

	// If keeping changes, commit and merge
	if keepChanges {
		// Stage all changes in worktree
		cmd := exec.Command("git", "add", "-A")
		cmd.Dir = info.Path
		cmd.Run()

		// Commit changes
		commitMsg := fmt.Sprintf("openkitty: worktree changes from %s", agentName)
		cmd = exec.Command("git", "commit", "-m", commitMsg)
		cmd.Dir = info.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			// No changes to commit is OK
			if !strings.Contains(string(out), "nothing to commit") {
				log.Printf("[WORKTREE] commit warning: %s", string(out))
			}
		}

		// Fetch latest to reduce conflict risk
		if out, fetchErr := runGitIn(wm.mainDir, "fetch", "origin", info.BaseBranch); fetchErr != nil {
			log.Printf("[WORKTREE] fetch warning: %s", strings.TrimSpace(string(out)))
		}

		// Rebase onto base branch before merge
		rebaseCmd := exec.Command("git", "rebase", info.BaseBranch)
		rebaseCmd.Dir = info.Path
		if out, err := rebaseCmd.CombinedOutput(); err != nil {
			log.Printf("[WORKTREE] rebase failed, aborting: %s", string(out))
			_, _ = runGitIn(info.Path, "rebase", "--abort")
			return fmt.Errorf("rebase failed for '%s'; worktree retained on branch '%s': %w", agentName, info.Branch, err)
		}

		// Switch to original branch and merge with conflict detection
		if out, checkoutErr := runGitIn(wm.mainDir, "checkout", info.BaseBranch); checkoutErr != nil {
			return fmt.Errorf("checkout base branch '%s' failed: %s: %w", info.BaseBranch, strings.TrimSpace(string(out)), checkoutErr)
		}

		// Try merge with --no-commit to detect conflicts
		mergeCmd := exec.Command("git", "merge", "--no-ff", "--no-commit", info.Branch)
		mergeCmd.Dir = wm.mainDir
		if out, err := mergeCmd.CombinedOutput(); err != nil {
			outStr := string(out)
			if strings.Contains(outStr, "CONFLICT") || strings.Contains(outStr, "conflict") {
				log.Printf("[WORKTREE] MERGE CONFLICT for '%s', aborting and stashing", agentName)
				_, _ = runGitIn(wm.mainDir, "merge", "--abort")
				return fmt.Errorf(
					"merge conflict for '%s'. Changes kept on branch '%s'. Resolve manually with: git checkout %s && git merge %s",
					agentName, info.Branch, info.BaseBranch, info.Branch,
				)
			}
			_, _ = runGitIn(wm.mainDir, "merge", "--abort")
			return fmt.Errorf("merge failed for '%s': %s: %w", agentName, strings.TrimSpace(outStr), err)
		}
		// Auto-commit the merge
		if out, commitErr := runGitIn(wm.mainDir, "commit", "-m", fmt.Sprintf("openkitty: merge worktree '%s'", info.Branch)); commitErr != nil {
			// "Already up to date" leaves no MERGE_HEAD and needs no commit.
			// A real merge commit failure must be aborted so the main worktree is
			// never left in a half-merged state.
			if _, mergeHeadErr := runGitIn(wm.mainDir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); mergeHeadErr == nil {
				_, _ = runGitIn(wm.mainDir, "merge", "--abort")
				return fmt.Errorf("commit merge for '%s' failed: %s: %w", agentName, strings.TrimSpace(string(out)), commitErr)
			}
			if _, diffErr := runGitIn(wm.mainDir, "diff", "--cached", "--quiet"); diffErr != nil {
				return fmt.Errorf("commit merge for '%s' failed: %s: %w", agentName, strings.TrimSpace(string(out)), commitErr)
			}
			log.Printf("[WORKTREE] branch '%s' was already integrated; no merge commit needed", info.Branch)
		}
	}

	// Clean up: remove worktree
	if out, removeErr := runGitIn(wm.mainDir, "worktree", "remove", "--force", info.Path); removeErr != nil {
		return fmt.Errorf("git worktree remove failed for '%s': %s: %w", agentName, strings.TrimSpace(string(out)), removeErr)
	}

	// Remove branch if not keeping
	if !keepChanges {
		if out, branchErr := runGitIn(wm.mainDir, "branch", "-D", info.Branch); branchErr != nil {
			log.Printf("[WORKTREE] branch cleanup warning: %s", strings.TrimSpace(string(out)))
		}
	} else {
		// Clean up the branch after merge
		if out, branchErr := runGitIn(wm.mainDir, "branch", "-d", info.Branch); branchErr != nil {
			log.Printf("[WORKTREE] branch cleanup warning: %s", strings.TrimSpace(string(out)))
		}
	}

	// Clean up directory
	if err := removeManagedWorktreePath(worktreeRoot, info.Path); err != nil {
		return err
	}
	delete(wm.worktrees, agentName)

	log.Printf("[WORKTREE] Removed worktree for agent '%s' (keepChanges=%v)", agentName, keepChanges)
	return nil
}

// GetWorktree returns the worktree info for an agent.
func (wm *WorktreeManager) GetWorktree(agentName string) *WorktreeInfo {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "agent"
	}
	return cloneWorktreeInfo(wm.worktrees[agentName])
}

// CleanupAll removes all active worktrees (called on shutdown).
func (wm *WorktreeManager) CleanupAll() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	remaining := make(map[string]*WorktreeInfo)
	for agentName, info := range wm.worktrees {
		root, expectedPath, err := worktreePathForAgent(wm.mainDir, agentName)
		if err != nil || !strings.EqualFold(filepath.Clean(info.Path), filepath.Clean(expectedPath)) {
			log.Printf("[WORKTREE] refusing cleanup of unmanaged path for '%s': %s (%v)", agentName, info.Path, err)
			remaining[agentName] = info
			continue
		}
		if err := ensureWorktreePathWithinRoot(root, info.Path); err != nil {
			log.Printf("[WORKTREE] refusing unsafe cleanup for '%s': %v", agentName, err)
			remaining[agentName] = info
			continue
		}
		if out, removeErr := runGitIn(wm.mainDir, "worktree", "remove", "--force", info.Path); removeErr != nil {
			log.Printf("[WORKTREE] cleanup failed for '%s': %s", agentName, strings.TrimSpace(string(out)))
			remaining[agentName] = info
			continue
		}
		_, _ = runGitIn(wm.mainDir, "branch", "-D", info.Branch)
		if err := removeManagedWorktreePath(root, info.Path); err != nil {
			log.Printf("[WORKTREE] directory cleanup failed for '%s': %v", agentName, err)
			remaining[agentName] = info
		}
	}
	wm.worktrees = remaining
	log.Printf("[WORKTREE] Cleaned up all worktrees")
}

// HealthCheck verifies all active worktrees are in good state.
func (wm *WorktreeManager) HealthCheck() []string {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	var issues []string
	for name, info := range wm.worktrees {
		if _, err := os.Stat(info.Path); os.IsNotExist(err) {
			issues = append(issues, fmt.Sprintf("worktree '%s' path missing: %s", name, info.Path))
			continue
		}
		cmd := exec.Command("git", "status")
		cmd.Dir = info.Path
		if err := cmd.Run(); err != nil {
			issues = append(issues, fmt.Sprintf("worktree '%s' git status failed: %v", name, err))
		}
	}
	if len(issues) == 0 {
		issues = append(issues, "All worktrees healthy")
	}
	return issues
}

// ListWorktrees returns all active worktrees.
func (wm *WorktreeManager) ListWorktrees() []*WorktreeInfo {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	var result []*WorktreeInfo
	for _, info := range wm.worktrees {
		result = append(result, cloneWorktreeInfo(info))
	}
	return result
}

func sanitizeBranchName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	// Remove non-alphanumeric except hyphens
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result := sb.String()
	if len(result) > 30 {
		result = result[:30]
	}
	if result == "" {
		result = "agent"
	}
	return result
}

// ==================== enter_worktree tool ====================

type EnterWorktreeTool struct {
	manager *WorktreeManager
}

func NewEnterWorktreeTool(manager *WorktreeManager) *EnterWorktreeTool {
	return &EnterWorktreeTool{manager: manager}
}

func (t *EnterWorktreeTool) Name() string { return "enter_worktree" }

func (t *EnterWorktreeTool) Description() string {
	return "Create an isolated git worktree for making changes safely. " +
		"Use this before making significant code changes — your work will be isolated on a separate branch. " +
		"Changes are NOT visible to the main workspace until you call exit_worktree with keep_changes=true. " +
		"The worktree path will be returned — use it as the working directory for subsequent file operations."
}

func (t *EnterWorktreeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "Name to identify this worktree. Default: current agent name.",
			},
		},
	}
}

func (t *EnterWorktreeTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	agentName, _ := params["agent_name"].(string)
	if agentName == "" {
		agentName = "agent"
	}

	if t.manager == nil {
		return "Error: worktree manager not available", nil
	}

	if !gitutil.IsRepository(".") {
		return "Error: current directory is not a git repository. Worktree isolation requires git.", nil
	}

	info, err := t.manager.CreateWorktree(agentName)
	if err != nil {
		return fmt.Sprintf("Error creating worktree: %v", err), nil
	}

	return fmt.Sprintf("Worktree created for '%s':\n- Path: %s\n- Branch: %s\n- Base: %s\n\nUse this path as the working directory for file operations. "+
		"Call exit_worktree with keep_changes=true to merge changes back.",
		agentName, info.Path, info.Branch, info.BaseBranch), nil
}

// ==================== exit_worktree tool ====================

type ExitWorktreeTool struct {
	manager *WorktreeManager
}

func NewExitWorktreeTool(manager *WorktreeManager) *ExitWorktreeTool {
	return &ExitWorktreeTool{manager: manager}
}

func (t *ExitWorktreeTool) Name() string { return "exit_worktree" }

func (t *ExitWorktreeTool) Description() string {
	return "Exit an isolated worktree and optionally merge changes back to the main branch. " +
		"Set keep_changes=true to commit and merge your worktree changes into the base branch. " +
		"Set keep_changes=false to discard all changes and clean up the worktree."
}

func (t *ExitWorktreeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the worktree to exit. Default: current agent name.",
			},
			"keep_changes": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, commit and merge changes back. If false, discard everything.",
			},
		},
		"required": []string{"keep_changes"},
	}
}

func (t *ExitWorktreeTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	agentName, _ := params["agent_name"].(string)
	if agentName == "" {
		agentName = "agent"
	}

	if t.manager == nil {
		return "Error: worktree manager not available", nil
	}

	keepChanges := false
	if v, ok := params["keep_changes"].(bool); ok {
		keepChanges = v
	}

	info := t.manager.GetWorktree(agentName)
	if info == nil {
		return fmt.Sprintf("No worktree found for '%s'. Nothing to exit.", agentName), nil
	}

	if err := t.manager.RemoveWorktree(agentName, keepChanges); err != nil {
		return fmt.Sprintf("Error removing worktree: %v", err), nil
	}

	if keepChanges {
		return fmt.Sprintf("Worktree '%s' merged back to %s.\nBranch '%s' changes have been committed and merged.",
			agentName, info.BaseBranch, info.Branch), nil
	}
	return fmt.Sprintf("Worktree '%s' discarded. All changes on branch '%s' have been removed.",
		agentName, info.Branch), nil
}
