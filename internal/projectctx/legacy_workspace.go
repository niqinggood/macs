package projectctx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrLegacyWorkspaceEscape       = errors.New("path is outside the workspace")
	ErrReservedTenantWorkspacePath = errors.New("tenant workspace is reserved")
)

// resolveLegacyWorkspacePath resolves a path supplied to a legacy Web file
// endpoint. Those endpoints operate in the shared project workspace and must
// never be usable as an alternate entrance to AgentOS tenant workspaces.
func ResolveLegacyWorkspacePath(workspaceRoot, requestedPath string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" || requestedPath == "." ||
		requestedPath == "/" || requestedPath == `\` {
		requestedPath = "."
	} else {
		// Web file paths are workspace-relative. A leading slash is the API's
		// historical spelling for the workspace root, not a filesystem root.
		requestedPath = strings.TrimLeft(requestedPath, `/\`)
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(requestedPath)))
	if !PathWithinEqualFold(root, candidate) {
		return "", ErrLegacyWorkspaceEscape
	}

	resolvedRoot, err := ResolvePathThroughExistingAncestor(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolvedCandidate, err := ResolvePathThroughExistingAncestor(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !PathWithinEqualFold(resolvedRoot, resolvedCandidate) {
		return "", ErrLegacyWorkspaceEscape
	}
	if err := RejectReservedTenantWorkspacePath(root, candidate); err != nil {
		return "", err
	}
	return resolvedCandidate, nil
}

// rejectReservedTenantWorkspacePath rejects both the literal reserved subtree
// and aliases that resolve into it. Comparisons are intentionally
// case-insensitive so the boundary has the same behavior on every supported
// filesystem.
func RejectReservedTenantWorkspacePath(workspaceRoot, candidatePath string) error {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	candidate, err := filepath.Abs(candidatePath)
	if err != nil {
		return fmt.Errorf("resolve candidate: %w", err)
	}
	reserved := filepath.Join(root, ".openkitty", "tenants")
	if PathWithinEqualFold(reserved, candidate) {
		return ErrReservedTenantWorkspacePath
	}

	resolvedReserved, err := ResolvePathThroughExistingAncestor(reserved)
	if err != nil {
		return fmt.Errorf("resolve reserved tenant workspace: %w", err)
	}
	resolvedCandidate, err := ResolvePathThroughExistingAncestor(candidate)
	if err != nil {
		return fmt.Errorf("resolve candidate: %w", err)
	}
	if PathWithinEqualFold(resolvedReserved, resolvedCandidate) {
		return ErrReservedTenantWorkspacePath
	}
	return nil
}

func PathWithinEqualFold(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if strings.EqualFold(root, candidate) {
		return true
	}
	rootPrefix := root
	if !strings.HasSuffix(rootPrefix, string(os.PathSeparator)) {
		rootPrefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(
		strings.ToLower(candidate),
		strings.ToLower(rootPrefix),
	)
}

// resolvePathThroughExistingAncestor follows all symlinks in the existing
// portion of a path, then restores any not-yet-created suffix. This lets upload
// targets be checked before creation without treating a missing leaf as safe.
func ResolvePathThroughExistingAncestor(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current = filepath.Clean(current)
	var suffix []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", evalErr
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
