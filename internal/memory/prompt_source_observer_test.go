package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptSourceObserverMasksInheritedCallbacksAndRequiresExactRepresentation(t *testing.T) {
	called := 0
	ctx := WithPromptSourceObserver(context.Background(), func(PromptSourceObservation) { called++ })
	item := RecallItem{Content: "actual source", Locator: PromptLocator{Kind: "entry", Digest: "opaque-digest"}}
	observePromptSource(ctx, item, "prefix actual source suffix")
	if called != 1 {
		t.Fatal("host observer was not called")
	}
	observePromptSource(WithPromptSourceObserver(ctx, nil), item, "actual source")
	observePromptSource(ctx, item, "truncated actual")
	item.Locator = PromptLocator{}
	observePromptSource(ctx, item, "actual source")
	if called != 1 {
		t.Fatal("inherited or unrepresented source was observed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	item.Locator = PromptLocator{Kind: "entry", Digest: "opaque-digest"}
	observePromptSource(cancelled, item, "actual source")
	if called != 1 {
		t.Fatal("cancelled source was observed")
	}
}

func TestPromptSourceObserverAgentMemoryReadsExactToolJSONAndOriginalLocator(t *testing.T) {
	service, owner := promptTestService(t)
	value := "artifact concision\nUse \"quoted\" values and \\ paths."
	if err := service.CreateWithSource(context.Background(), owner, "format", value, MemoryProject, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	foreign := owner
	foreign.SubjectID = "foreign-user"
	if err := service.CreateWithSource(context.Background(), foreign, "private", "FOREIGN_MEMORY", MemoryProject, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	entry, _ := service.Lookup(context.Background(), owner, "format", MemoryProject)
	tool := NewAgentMemoryServiceTool(service, func(context.Context) (Owner, error) { return owner, nil }, nil)
	for _, action := range []string{"recall", "list", "snapshot"} {
		t.Run(action, func(t *testing.T) {
			var observations []PromptSourceObservation
			ctx := WithPromptSourceObserver(context.Background(), func(observation PromptSourceObservation) { observations = append(observations, observation) })
			result, err := tool.Execute(ctx, map[string]interface{}{"action": action, "key": "format", "scope": "project", "query": "artifact concision"})
			if err != nil || len(observations) != 1 || strings.Contains(result, "FOREIGN_MEMORY") {
				t.Fatalf("observed %s: %d %v", action, len(observations), err)
			}
			observation := observations[0]
			if observation.Item.ID != entry.ID || observation.Item.Locator.ID != entry.ID || observation.Item.Locator.Version != 1 || observation.Item.Locator.Digest != promptContentDigest(value) || observation.Item.Locator.SessionID != "" && action != "snapshot" {
				t.Fatalf("original source locator: %#v", observation.Item.Locator)
			}
			if !strings.Contains(result, observation.Block) || !strings.Contains(result, observation.Item.Content) {
				t.Fatal("observer did not describe the actual returned bytes")
			}
			// The ordinary provider JSON escapes the tool JSON a second time.
			// Decode only the provider transport, just as the runtime capture does.
			body, _ := json.Marshal(map[string]interface{}{"messages": []map[string]string{{"role": "tool", "content": result}}})
			var wire struct {
				Messages []struct{ Role, Content string }
			}
			if err := json.Unmarshal(body, &wire); err != nil || !strings.Contains(wire.Messages[0].Content, observation.Block) || !strings.Contains(wire.Messages[0].Content, observation.Item.Content) {
				t.Fatalf("source bytes absent from decoded outbound tool content: %v", err)
			}
			if action != "snapshot" {
				encodedValue, _ := json.Marshal(value)
				if observation.Item.Content != string(encodedValue) || observation.Block != result {
					t.Fatal("JSON value excerpt was reconstructed as unescaped raw text")
				}
			}
		})
	}
	blocked := NewAgentMemoryServiceTool(service, func(context.Context) (Owner, error) { return owner, nil }, func(context.Context, bool) bool { return false })
	called := 0
	ctx := WithPromptSourceObserver(context.Background(), func(PromptSourceObservation) { called++ })
	if _, err := blocked.Execute(ctx, map[string]interface{}{"action": "recall", "key": "format", "scope": "project"}); err == nil || called != 0 {
		t.Fatal("disabled tool emitted a source observation")
	}
}

func TestPromptSourceObserverContextReadsOnlyTrustedSessionTuple(t *testing.T) {
	service, owner := promptTestService(t)
	scm := NewSessionContextManager(service.workspace + "/.openkitty/context")
	scm.SetWorkspace(service.workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	value := "working artifact\nUse \"short\" wording."
	if err := scm.Set("notes", value, "fact", 5, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("private", "FOREIGN_CONTEXT", "fact", 5, "foreign-session"); err != nil {
		t.Fatal(err)
	}
	if err := scm.SetDurable("legacy", "UNVERIFIED_DURABLE", "fact", 5); err != nil {
		t.Fatal(err)
	}
	tool := NewSessionContextServiceTool(scm, service, func(context.Context) (Owner, error) { return owner, nil }, nil)
	for _, action := range []string{"get", "list", "inject"} {
		t.Run(action, func(t *testing.T) {
			var observations []PromptSourceObservation
			ctx := context.WithValue(context.Background(), "session_id", owner.SessionID)
			ctx = WithPromptSourceObserver(ctx, func(observation PromptSourceObservation) { observations = append(observations, observation) })
			result, err := tool.Execute(ctx, map[string]interface{}{"action": action, "key": "notes", "scope": "session", "session_id": "foreign-session"})
			if err != nil || len(observations) != 1 || strings.Contains(result, "FOREIGN_CONTEXT") {
				t.Fatalf("context %s: %d %v", action, len(observations), err)
			}
			observed := observations[0]
			if observed.Item.Locator.Kind != "context" || observed.Item.Locator.Key != "notes" || observed.Item.Locator.SessionID != owner.SessionID || observed.Item.Locator.Digest != promptContentDigest(value) || !strings.Contains(result, observed.Block) || !strings.Contains(result, observed.Item.Content) {
				t.Fatalf("context observation did not locate its exact trusted tuple: %#v", observed)
			}
		})
	}
	// The compatibility tool can display durable context; this adapter must not
	// invent personal ownership or an editable session locator for that legacy row.
	legacy := NewSessionContextTool(scm)
	called := 0
	ctx := context.WithValue(context.Background(), "session_id", owner.SessionID)
	ctx = WithPromptSourceObserver(ctx, func(PromptSourceObservation) { called++ })
	if _, err := legacy.Execute(ctx, map[string]interface{}{"action": "get", "key": "legacy", "scope": "durable"}); err != nil || called != 0 {
		t.Fatal("durable compatibility read was assigned an invented session owner")
	}
}
