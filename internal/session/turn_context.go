package session

import (
	"context"
	"strings"
)

type TurnResumeState struct {
	Checkpoint   *TurnCheckpoint
	SourceTaskID string
	Supplement   string
}

type turnResumeStateContextKey struct{}
type turnPersistenceIDContextKey struct{}
type turnPersistencePathsObserverContextKey struct{}

type TurnPersistencePathsObserver func(checkpointPath, transcriptPath string)

func WithTurnPersistenceID(ctx context.Context, turnID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ctx
	}
	return context.WithValue(ctx, turnPersistenceIDContextKey{}, turnID)
}

func TurnPersistenceIDFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if value, _ := ctx.Value(turnPersistenceIDContextKey{}).(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(fallback)
}

func WithTurnPersistencePathsObserver(ctx context.Context, observer TurnPersistencePathsObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, turnPersistencePathsObserverContextKey{}, observer)
}

func PublishTurnPersistencePaths(ctx context.Context, checkpointPath, transcriptPath string) {
	if ctx == nil {
		return
	}
	observer, _ := ctx.Value(turnPersistencePathsObserverContextKey{}).(TurnPersistencePathsObserver)
	if observer != nil {
		observer(strings.TrimSpace(checkpointPath), strings.TrimSpace(transcriptPath))
	}
}

func WithTurnResumeState(ctx context.Context, checkpoint *TurnCheckpoint, sourceTaskID, supplement string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if checkpoint == nil {
		return ctx
	}
	return context.WithValue(ctx, turnResumeStateContextKey{}, TurnResumeState{
		Checkpoint:   CloneTurnCheckpoint(checkpoint),
		SourceTaskID: strings.TrimSpace(sourceTaskID),
		Supplement:   strings.TrimSpace(supplement),
	})
}

func TurnResumeStateFromContext(ctx context.Context) (TurnResumeState, bool) {
	if ctx == nil {
		return TurnResumeState{}, false
	}
	state, ok := ctx.Value(turnResumeStateContextKey{}).(TurnResumeState)
	if !ok || state.Checkpoint == nil {
		return TurnResumeState{}, false
	}
	state.Checkpoint = CloneTurnCheckpoint(state.Checkpoint)
	return state, true
}
