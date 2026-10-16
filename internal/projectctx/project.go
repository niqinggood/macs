// Package projectctx owns normalized workspace identity and its context value.
package projectctx

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

type resolvedWorkspaceContextKey struct{}

type ProjectContext struct {
	ProjectID string `json:"project_id"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
}

func New(workspace string) ProjectContext {
	normalized := NormalizeWorkspace(workspace)
	name := filepath.Base(normalized)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "default"
	}
	sum := sha1.Sum([]byte(IdentityKey(normalized)))
	return ProjectContext{
		ProjectID: "proj_" + hex.EncodeToString(sum[:])[:12],
		Workspace: normalized,
		Name:      name,
	}
}

func NormalizeWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "."
	}
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = absolute
	}
	workspace = filepath.Clean(workspace)
	if runtime.GOOS == "windows" {
		workspace = strings.ToLower(workspace)
	}
	return workspace
}

func IdentityKey(workspace string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(workspace)
	}
	return workspace
}

func WithResolvedWorkspace(ctx context.Context, workspace string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	workspace = NormalizeWorkspace(workspace)
	if workspace == "" {
		return ctx
	}
	return context.WithValue(ctx, resolvedWorkspaceContextKey{}, workspace)
}

func ResolvedWorkspace(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	workspace, _ := ctx.Value(resolvedWorkspaceContextKey{}).(string)
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	return NormalizeWorkspace(workspace)
}

func SameWorkspace(left, right string) bool {
	return IdentityKey(NormalizeWorkspace(left)) == IdentityKey(NormalizeWorkspace(right))
}

// PersonalWorkspace places one opaque subject partition below the tenant
// workspace without exposing the raw subject identifier in a path.
func PersonalWorkspace(tenantWorkspace, partitionID string) string {
	return NormalizeWorkspace(filepath.Join(
		strings.TrimSpace(tenantWorkspace),
		".openkitty",
		"users",
		"usr_"+partitionID,
	))
}
