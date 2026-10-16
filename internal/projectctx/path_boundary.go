package projectctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContainsPath performs a lexical, separator-aware containment check.
func ContainsPath(workspace, target string) bool {
	workspace, workspaceErr := filepath.Abs(workspace)
	target, targetErr := filepath.Abs(target)
	if workspaceErr != nil || targetErr != nil {
		return false
	}

	workspaceKey := IdentityKey(filepath.Clean(workspace))
	targetKey := IdentityKey(filepath.Clean(target))
	relative, err := filepath.Rel(workspaceKey, targetKey)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// EnsureResolvedPathInside closes symlink and Windows junction escapes. For a
// path that does not exist yet, it resolves the nearest existing parent.
func EnsureResolvedPathInside(workspace, target string) error {
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve workspace links: %w", err)
	}

	existing := target
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect path %q: %w", target, statErr)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("cannot resolve an existing parent for %q", target)
		}
		existing = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("resolve path links for %q: %w", target, err)
	}
	remaining, err := filepath.Rel(existing, target)
	if err != nil {
		return fmt.Errorf("resolve path suffix for %q: %w", target, err)
	}
	resolvedTarget := filepath.Join(resolvedExisting, remaining)
	if !ContainsPath(resolvedWorkspace, resolvedTarget) {
		return fmt.Errorf("path %q resolves outside this user's workspace", target)
	}
	return nil
}
