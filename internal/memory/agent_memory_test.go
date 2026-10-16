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
)

func memoryTestContext(sessionID string) context.Context {
	return context.WithValue(context.Background(), "session_id", sessionID)
}

func TestAgentMemorySameKeyScopesAndSessions(t *testing.T) {
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	for _, item := range []struct {
		scope          MemoryScope
		session, value string
	}{
		{MemoryUser, "", "user-default"},
		{MemoryProject, "", "project-default"},
		{MemorySession, "one", "one-only"},
		{MemorySession, "two", "two-only"},
	} {
		if err := store.Remember("style", item.value, item.scope, 5, nil, item.session); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.RecallForSession("style", "", "one"); got != "one-only" {
		t.Fatalf("session precedence: %q", got)
	}
	if got := store.RecallForSession("style", "", "two"); got != "two-only" {
		t.Fatalf("second session: %q", got)
	}
	if got := store.Recall("style"); got != "project-default" {
		t.Fatalf("durable precedence: %q", got)
	}
	if got := store.RecallForSession("style", MemoryUser, "one"); got != "user-default" {
		t.Fatalf("explicit scope: %q", got)
	}
	if err := store.Forget("style"); err == nil {
		t.Fatal("ambiguous compatibility delete succeeded")
	}
	if deleted, err := store.ForgetForSession("style", MemorySession, "one"); err != nil || !deleted {
		t.Fatalf("delete own session: %v, %v", deleted, err)
	}
	if got := store.RecallForSession("style", MemorySession, "two"); got != "two-only" {
		t.Fatalf("other session lost: %q", got)
	}
	data, err := os.ReadFile(store.filePath())
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]*AgentMemory
	if err := json.Unmarshal(data, &persisted); err != nil || len(persisted) != 4 {
		t.Fatalf("persisted distinct records: %s, %v", data, err)
	}
	// Load from disk without the process registry, as a fresh process would.
	reloaded := &AgentMemoryStore{
		agentMemoryState: &agentMemoryState{memories: make(map[string]*AgentMemory)},
		storeDir:         store.storeDir, agentName: store.agentName,
	}
	reloaded.load()
	if reloaded.loadErr != nil || reloaded.RecallForSession("style", MemorySession, "two") != "two-only" ||
		reloaded.RecallForSession("style", MemoryUser, "") != "user-default" {
		t.Fatalf("disk reload lost scopes: %v", reloaded.loadErr)
	}
}

func TestAgentMemoryToolCannotReadOrForgetForeignSession(t *testing.T) {
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	if err := store.Remember("private", "owner-secret", MemorySession, 5, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	tool := NewAgentMemoryTool(store, "owner")
	for _, action := range []string{"recall", "list", "snapshot", "forget"} {
		got, err := tool.Execute(memoryTestContext("other"), map[string]interface{}{
			"action": action, "key": "private", "scope": "session", "session_id": "owner",
		})
		if err != nil || strings.Contains(got, "owner-secret") || strings.Contains(got, "Forgotten") {
			t.Fatalf("foreign %s: %q, %v", action, got, err)
		}
	}
	if got := store.RecallForSession("private", MemorySession, "owner"); got != "owner-secret" {
		t.Fatalf("owner lost memory: %q", got)
	}
	if _, err := tool.Execute(memoryTestContext("owner"), map[string]interface{}{"action": "forget", "key": "private"}); err == nil {
		t.Fatal("delete without scope succeeded")
	}
}

func TestAgentMemoryToolUsesCurrentContextForWrites(t *testing.T) {
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	tool := NewAgentMemoryTool(store, "constructor-session")
	if _, err := tool.Execute(memoryTestContext("current"), map[string]interface{}{"action": "remember", "key": "status", "value": "current-only"}); err != nil {
		t.Fatal(err)
	}
	if got := store.RecallForSession("status", MemorySession, "current"); got != "current-only" {
		t.Fatalf("current session: %q", got)
	}
	if got := store.RecallForSession("status", MemorySession, "constructor-session"); got != "" {
		t.Fatalf("stale constructor audience: %q", got)
	}
	if _, err := tool.Execute(memoryTestContext(""), map[string]interface{}{"action": "remember", "key": "bad", "value": "unscoped"}); err == nil {
		t.Fatal("empty context fell back to constructor session")
	}
	if _, err := NewAgentMemoryTool(store, "").Execute(context.Background(), map[string]interface{}{"action": "remember", "key": "bad", "value": "unscoped"}); err == nil {
		t.Fatal("unscoped session write succeeded")
	}
	if _, err := tool.Execute(memoryTestContext("current"), map[string]interface{}{"action": "remember", "scope": "invalid", "key": "bad", "value": "bad"}); err == nil {
		t.Fatal("invalid scope accepted")
	}
}

func TestAgentMemoryMigratesLegacyAndQuarantinesUnknownAudience(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".openkitty", "memories")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]*AgentMemory{
		"durable":  {Key: "durable", Value: "keep-durable", Scope: MemoryProject, Version: 3},
		"private":  {Key: "private", Value: "keep-owner", Scope: MemorySession, SessionID: "owner", Version: 2},
		"unscoped": {Key: "unscoped", Value: "quarantined-secret", Scope: MemorySession, Version: 1},
	}
	data, _ := json.Marshal(legacy)
	path := filepath.Join(dir, "assistant.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewAgentMemoryStore(workspace, "assistant")
	if got := store.SnapshotForSession(20, "owner"); !strings.Contains(got, "keep-owner") || !strings.Contains(got, "keep-durable") || strings.Contains(got, "quarantined-secret") {
		t.Fatalf("migration visibility: %q", got)
	}
	if got := store.SnapshotForSession(20, "other"); strings.Contains(got, "keep-owner") {
		t.Fatalf("legacy session leaked: %q", got)
	}
	if err := store.Remember("new", "new-value", MemoryProject, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "quarantined-secret") {
		t.Fatal("legacy record was discarded")
	}
	if err := store.Remember("durable", "updated", MemoryProject, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := store.List(MemoryProject, "", 10); len(got) != 2 || store.memories[agentMemoryStorageKey("durable", MemoryProject, "")].Version != 4 {
		t.Fatalf("legacy version lost: %#v", got)
	}
}

func TestAgentMemoryPersistenceFailureDoesNotAcknowledgeOrMutate(t *testing.T) {
	workspace := t.TempDir()
	store := NewAgentMemoryStore(workspace, "assistant")
	if err := store.Remember("existing", "saved-value", MemoryProject, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	path := store.filePath()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(workspace, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	store.storeDir = filepath.Join(blocker, "child")
	tool := NewAgentMemoryTool(store, "")
	for _, params := range []map[string]interface{}{
		{"action": "remember", "scope": "project", "key": "new", "value": "failed-new"},
		{"action": "remember", "scope": "project", "key": "existing", "value": "failed-update"},
		{"action": "forget", "scope": "project", "key": "existing"},
	} {
		got, err := tool.Execute(context.Background(), params)
		if err == nil || got != "" {
			t.Fatalf("false success: %q, %v", got, err)
		}
	}
	if store.Recall("existing") != "saved-value" || store.Recall("new") != "" {
		t.Fatal("failed operation changed in-memory state")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("saved file changed: %v", err)
	}
}

func TestAgentMemoryCorruptFileIsPreserved(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".openkitty", "memories")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "assistant.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewAgentMemoryTool(NewAgentMemoryStore(workspace, "assistant"), "")
	for _, action := range []string{"remember", "recall", "list", "snapshot", "forget"} {
		if got, err := tool.Execute(context.Background(), map[string]interface{}{"action": action, "scope": "project", "key": "key", "value": "value"}); err == nil || got != "" {
			t.Fatalf("corrupt file %s: %q, %v", action, got, err)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != "{broken" {
		t.Fatal("corrupt data was overwritten")
	}
}

func TestAgentMemorySharedInstancesPersistConcurrentWrites(t *testing.T) {
	workspace := t.TempDir()
	one := NewAgentMemoryStore(workspace, "assistant")
	two := NewAgentMemoryStore(workspace, "assistant")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			store := one
			if index%2 == 0 {
				store = two
			}
			if err := store.Remember(fmt.Sprintf("key-%d", index), "value", MemoryProject, 5, nil, ""); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(one.List("", "", 0)) != 20 || len(two.List("", "", 0)) != 20 {
		t.Fatal("instances lost updates")
	}
	data, err := os.ReadFile(one.filePath())
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]*AgentMemory
	if err := json.Unmarshal(data, &persisted); err != nil || len(persisted) != 20 {
		t.Fatalf("lost persisted updates: %v, %s", err, data)
	}
}

func TestAgentMemoryWorkspaceIsolationAndSnapshotCopies(t *testing.T) {
	one := NewAgentMemoryStore(t.TempDir(), "assistant")
	two := NewAgentMemoryStore(t.TempDir(), "assistant")
	if err := one.Remember("private", "one-only", MemoryProject, 5, []string{"original"}, ""); err != nil {
		t.Fatal(err)
	}
	if two.Recall("private") != "" {
		t.Fatal("workspace boundary leaked")
	}
	items := one.List("", "", 10)
	items[0].Value = "mutated"
	items[0].Tags[0] = "mutated"
	if one.Recall("private") != "one-only" || one.List("", "original", 10)[0].Tags[0] != "original" {
		t.Fatal("caller mutated stored memory")
	}
}

func TestAgentMemoryRejectsEscapingAgentName(t *testing.T) {
	for _, name := range []string{"../outside", `..\outside`, "C:outside", ""} {
		store := NewAgentMemoryStore(t.TempDir(), name)
		if err := store.Remember("key", "value", MemoryProject, 5, nil, ""); err == nil {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
}
