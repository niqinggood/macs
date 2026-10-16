package app

import (
	"context"

	memorycore "mac/internal/memory"
)

type explicitMemoryTurnKey struct{}

type explicitMemoryTurnEvidence struct {
	input string
}

// beginExplicitMemoryUserTurn captures only the host's separate request
// evidence. Generate's input can instead be a scheduled task, attachment
// envelope or another agent's output, and is deliberately not a fallback.
// A nested Generate sees the existing marker and cannot borrow its parent's
// automatic write authority. Existing memory-tool evidence is left untouched.
func beginExplicitMemoryUserTurn(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	var turn explicitMemoryTurnEvidence
	_, nested := ctx.Value(explicitMemoryTurnKey{}).(explicitMemoryTurnEvidence)
	if !nested && !isInternalMemoryExecutionTurn(ctx) {
		if evidence, ok := ctx.Value(tenantRequestEvidenceContextKey{}).(tenantRequestEvidence); ok {
			turn.input = evidence.UserInput
		}
	}
	return context.WithValue(ctx, explicitMemoryTurnKey{}, turn)
}

// This derived context is used only for the automatic writer. Neither a
// missing nor explicitly empty request may fall back to old tool evidence.
func explicitMemoryWriteContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	turn, _ := ctx.Value(explicitMemoryTurnKey{}).(explicitMemoryTurnEvidence)
	return memorycore.WithUserTurn(ctx, turn.input)
}
