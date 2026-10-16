package memory

import "context"

// ToolAccess is supplied by the host, never by model arguments. Removal remains
// available when memory is disabled so a user can clean up existing records.
type ToolAccess func(context.Context, bool) bool

func memoryToolAllowed(access ToolAccess, ctx context.Context, removal bool) bool {
	return access == nil || access(ctx, removal)
}
