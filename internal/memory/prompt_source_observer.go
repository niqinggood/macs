package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PromptSourceObservation is a private sidecar of a real returned representation.
// It is not evidence that the result survived later trimming or reached a model;
// the host must match Block against the final serialized outbound request.
type PromptSourceObservation struct {
	Item  RecallItem
	Block string
}

type promptSourceObserverKey struct{}
type promptSourceObserver struct {
	observe func(PromptSourceObservation)
}

// WithPromptSourceObserver is set by the host, never from model arguments.
// A nil callback deliberately masks any inherited parent-agent observer.
func WithPromptSourceObserver(ctx context.Context, observe func(PromptSourceObservation)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, promptSourceObserverKey{}, promptSourceObserver{observe: observe})
}

func observePromptSource(ctx context.Context, item RecallItem, block string) {
	if ctx == nil || ctx.Err() != nil || item.Locator.Kind == "" || item.Locator.Digest == "" || item.Content == "" || block == "" || !strings.Contains(block, item.Content) {
		return
	}
	observer, ok := ctx.Value(promptSourceObserverKey{}).(promptSourceObserver)
	if ok && observer.observe != nil {
		observer.observe(PromptSourceObservation{Item: item, Block: block})
	}
}

func observePromptRecallResult(ctx context.Context, result RecallResult) {
	for _, item := range result.Items {
		block := fmt.Sprintf("\n[%s; id=%s; version=%d; source_session=%s]\n%s\n", item.Source, item.ID, item.Version, item.SourceSessionID, item.Content)
		if strings.Contains(result.Prompt, block) {
			observePromptSource(ctx, item, block)
		}
	}
}

func observePromptEntryToolResult(ctx context.Context, entry Entry, block string, encodedValue bool) {
	content := entry.Value
	if encodedValue {
		data, err := json.Marshal(content)
		if err != nil {
			return
		}
		content = string(data)
	}
	locator := PromptLocator{Kind: "entry", ID: entry.ID, Key: entry.Key, Field: "value", Version: entry.Version, Digest: promptContentDigest(entry.Value)}
	if entry.Scope == MemorySession {
		locator.SessionID = entry.SessionID
	} else if entry.Scope == MemoryGlobal {
		locator.Field = "global"
	}
	observePromptSource(ctx, RecallItem{ID: entry.ID, Source: "long_term:" + string(entry.Scope) + ":" + entry.Kind,
		Content: content, Version: entry.Version, SourceSessionID: entry.SourceSessionID, Locator: locator}, block)
}

func promptSessionContextSnapshot(manager *SessionContextManager, key, sessionID string) *ContextItem {
	if manager == nil || sessionID == "" {
		return nil
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return cloneContextItem(manager.items[contextStorageKey(key, sessionID, false)])
}

func observePromptContextToolResult(ctx context.Context, item *ContextItem, sessionID, block, content string) {
	// Durable/legacy context has no personal ownership proof here. This adapter
	// observes only the actual authorized current-session tuple read by the tool.
	if item == nil || item.Durable || sessionID == "" || item.SessionID != sessionID {
		return
	}
	locator := PromptLocator{Kind: "context", Key: item.Key, SessionID: sessionID, Field: "session", Digest: promptContentDigest(item.Value)}
	observePromptSource(ctx, RecallItem{ID: contextStorageKey(item.Key, sessionID, false), Source: "session_context",
		Content: content, SourceSessionID: sessionID, Locator: locator}, block)
}
