package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionContextSameKeyIsScopedBySession(t *testing.T) {
	scm := NewSessionContextManager(t.TempDir())
	if err := scm.Set("status", "one", "fact", 5, "session-one"); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("status", "two", "fact", 5, "session-two"); err != nil {
		t.Fatal(err)
	}

	if got := scm.GetVisible("status", "session-one"); got != "one" {
		t.Fatalf("session one got %q", got)
	}
	if got := scm.GetVisible("status", "session-two"); got != "two" {
		t.Fatalf("session two got %q", got)
	}
	if got := scm.GetVisible("status", "session-three"); got != "" {
		t.Fatalf("foreign session value leaked: %q", got)
	}
	if got := len(scm.List("fact", 10)); got != 2 {
		t.Fatalf("expected both scoped records, got %d", got)
	}
}

func TestSessionContextToolGetDeleteCannotReachForeignSession(t *testing.T) {
	scm := NewSessionContextManager(t.TempDir())
	tool := &SessionContextTool{manager: scm}
	if err := scm.Set("private", "session-one-secret", "fact", 5, "session-one"); err != nil {
		t.Fatal(err)
	}
	ctxTwo := context.WithValue(context.Background(), "session_id", "session-two")

	got, err := tool.Execute(ctxTwo, map[string]interface{}{"action": "get", "key": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "session-one-secret") || !strings.Contains(got, "No context") {
		t.Fatalf("tool read a foreign session: %q", got)
	}
	got, err = tool.Execute(ctxTwo, map[string]interface{}{"action": "delete", "key": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "No context") {
		t.Fatalf("foreign delete should report not found: %q", got)
	}
	if got := scm.GetVisible("private", "session-one"); got != "session-one-secret" {
		t.Fatalf("foreign delete removed owner value: %q", got)
	}

	if _, err := tool.Execute(ctxTwo, map[string]interface{}{
		"action": "set", "key": "shared", "value": "durable-value", "category": "fact",
	}); err != nil {
		t.Fatal(err)
	}
	if got := scm.GetVisible("shared", "session-one"); got != "durable-value" {
		t.Fatalf("explicit durable tool value not visible: %q", got)
	}
	got, err = tool.Execute(ctxTwo, map[string]interface{}{"action": "delete", "key": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "No context") || scm.GetDurable("shared") != "durable-value" {
		t.Fatalf("default delete touched durable context: result=%q durable=%q", got, scm.GetDurable("shared"))
	}
	if _, err := tool.Execute(ctxTwo, map[string]interface{}{"action": "delete", "key": "shared", "scope": "durable"}); err != nil {
		t.Fatal(err)
	}
	if scm.GetDurable("shared") != "" {
		t.Fatal("explicit durable delete did not remove durable context")
	}
}

func TestForgedDurableSessionIDDoesNotGrantDurability(t *testing.T) {
	dir := t.TempDir()
	scm := NewSessionContextManager(dir)
	if err := scm.Set("forged", "not-durable", "fact", 8, durableContextSessionID); err != nil {
		t.Fatal(err)
	}
	if got := scm.GetVisible("forged", "another-session"); got != "" {
		t.Fatalf("forged sentinel leaked as durable: %q", got)
	}
	if got := scm.GetVisible("forged", durableContextSessionID); got != "not-durable" {
		t.Fatalf("ordinary sentinel-named session lost its own value: %q", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]*ContextItem
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	for storageKey, item := range persisted {
		if item.Key == "forged" {
			if item.Durable {
				t.Fatalf("forged item persisted as durable: %#v", item)
			}
			decodedSession, _, durable, ok := decodeContextStorageKey(storageKey)
			if !ok || durable || decodedSession != durableContextSessionID {
				t.Fatalf("forged item has unsafe storage key %q", storageKey)
			}
		}
	}
}

func TestSessionContextMigratesLegacyDurableRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	legacy := map[string]*ContextItem{
		"answer-style": {
			Key: "answer-style", Value: "concise", Category: "preference", Priority: 9,
			CreatedAt: now, UpdatedAt: now, SessionID: durableContextSessionID,
		},
		"project_path": {
			Key: "project_path", Value: "C:/old-task", Category: "fact", Priority: 7,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "context.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	scm := NewSessionContextManager(dir)
	if got := scm.GetVisible("answer-style", "new-session"); got != "concise" {
		t.Fatalf("legacy explicit tool record was not migrated: %q", got)
	}
	if got := scm.GetVisible("project_path", "new-session"); got != "" {
		t.Fatalf("legacy automatic record leaked into a new session: %q", got)
	}
	items := scm.List("", 10)
	for _, item := range items {
		if item.Key == "answer-style" && (!item.Durable || item.SessionID != "") {
			t.Fatalf("legacy explicit record has wrong migrated scope: %#v", item)
		}
	}
}

func TestSessionContextManagerSharedConcurrentWritesPersistAll(t *testing.T) {
	dir := t.TempDir()
	const writers = 24
	managers := make([]*SessionContextManager, writers)
	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scm := NewSessionContextManager(dir)
			managers[i] = scm
			if err := scm.Set(fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i), "fact", 5, "session"); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	for i := 1; i < len(managers); i++ {
		if managers[i] != managers[0] {
			t.Fatal("same context directory returned multiple manager instances")
		}
	}
	sharedSem := managers[0].derivationSemaphore()
	if sharedSem != managers[writers-1].derivationSemaphore() {
		t.Fatal("agents sharing a workspace did not share the derivation semaphore")
	}
	sharedSem <- struct{}{}
	select {
	case sharedSem <- struct{}{}:
		t.Fatal("workspace derivation semaphore allowed an unbounded second job")
	default:
		// Generate uses this same non-blocking branch to skip while busy.
	}
	<-sharedSem

	data, err := os.ReadFile(filepath.Join(dir, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]*ContextItem
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("concurrent save produced invalid JSON: %v", err)
	}
	if len(persisted) != writers {
		t.Fatalf("concurrent writes lost records: got %d want %d", len(persisted), writers)
	}
}

func TestSessionContextPromptUsesOwnedManagerAndIsBounded(t *testing.T) {
	owned := NewSessionContextManager(t.TempDir())
	foreign := NewSessionContextManager(t.TempDir())
	if err := owned.Set("owned", "right-manager", "fact", 10, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := owned.SetDurable("shared", "durable", "fact", 9); err != nil {
		t.Fatal(err)
	}
	if err := owned.Set("other", "wrong-session", "fact", 10, "s2"); err != nil {
		t.Fatal(err)
	}
	if err := foreign.SetDurable("foreign", "wrong-manager", "fact", 10); err != nil {
		t.Fatal(err)
	}

	prompt := owned.BuildContextPromptForSession("s1", 2)
	if !strings.Contains(prompt, "right-manager") || !strings.Contains(prompt, "durable") {
		t.Fatalf("owned current+durable context missing: %q", prompt)
	}
	if strings.Contains(prompt, "wrong-manager") || strings.Contains(prompt, "wrong-session") {
		t.Fatalf("foreign context entered Generate prompt: %q", prompt)
	}
	if strings.Count(prompt, "\n-") > 2 {
		t.Fatalf("prompt builder exceeded item limit: %q", prompt)
	}
}

func TestSessionContextSaveErrorIsReturnedAndRecorded(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "context")
	scm := NewSessionContextManager(store)
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("key", "value", "fact", 5, "s1"); err == nil {
		t.Fatal("save failure was swallowed")
	}
	if scm.LastError() == nil {
		t.Fatal("save failure was not recorded")
	}
}
