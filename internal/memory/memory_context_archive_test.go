package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeContextArchiveFixture(t *testing.T, workspace string, value interface{}) string {
	t.Helper()
	path := filepath.Join(workspace, ".openkitty", "context", "context.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyPreferenceArchiveReadsOnlyUnscopedWorkspacePreferencesWithoutMigration(t *testing.T) {
	workspace := t.TempDir()
	created, updated := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC), time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	rows := map[string]interface{}{
		"legacy":            map[string]interface{}{"key": "historic preference", "value": "A real old preference", "category": "preference", "created_at": created, "updated_at": updated},
		"same-workspace":    map[string]interface{}{"key": "workspace preference", "value": "Another recorded preference", "category": "preference", "workspace": workspace},
		"foreign-workspace": map[string]interface{}{"key": "FOREIGN_WORKSPACE", "value": "DO_NOT_EXPOSE", "category": "preference", "workspace": t.TempDir()},
		"foreign-session":   map[string]interface{}{"key": "FOREIGN_SESSION", "value": "DO_NOT_EXPOSE", "category": "preference", "session_id": "other-session"},
		"foreign-tenant":    map[string]interface{}{"key": "FOREIGN_TENANT", "value": "DO_NOT_EXPOSE", "category": "preference", "tenant_id": "other"},
		"foreign-subject":   map[string]interface{}{"key": "FOREIGN_SUBJECT", "value": "DO_NOT_EXPOSE", "category": "preference", "subject_id": "other"},
		"foreign-user":      map[string]interface{}{"key": "FOREIGN_USER", "value": "DO_NOT_EXPOSE", "category": "preference", "user_id": "other"},
		"foreign-owner":     map[string]interface{}{"key": "FOREIGN_OWNER", "value": "DO_NOT_EXPOSE", "category": "preference", "owner_id": "other"},
		"not-preference":    map[string]interface{}{"key": "NOT_A_PREFERENCE", "value": "DO_NOT_EXPOSE", "category": "decision"},
		contextStorageKey("FORGED_SESSION", "other-session", false): map[string]interface{}{"key": "FORGED_SESSION", "value": "DO_NOT_EXPOSE", "category": "preference"},
	}
	path := writeContextArchiveFixture(t, workspace, rows)
	before, _ := os.ReadFile(path)
	refs, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 200)
	if err != nil || len(refs) != 2 {
		t.Fatalf("archive filtering: %#v %v", refs, err)
	}
	for _, ref := range refs {
		if ref.SourceType != "legacy_preference" || ref.Scope != MemoryProject || ref.Kind != "preference" || !ref.ReadOnly || ref.OwnershipVerified == nil || *ref.OwnershipVerified || ref.SessionID != "" || ref.AgentName != "" || ref.SourceLocation != ".openkitty/context/context.json" {
			t.Fatal("archive mistaken for confirmed personal or session memory", ref)
		}
		if ref.Key == "historic preference" && (ref.CreatedAt == nil || !ref.CreatedAt.Equal(created) || !ref.UpdatedAt.Equal(updated)) {
			t.Fatal("real historical timestamps were replaced")
		}
		if ref.Key == "historic preference" && ref.SourceID != "legacy" || ref.Key == "workspace preference" && ref.SourceID != "same-workspace" {
			t.Fatal("archive original JSON record ID was guessed or omitted", ref.SourceID)
		}
		if ref.Key == "workspace preference" && (ref.CreatedAt != nil || !ref.UpdatedAt.IsZero()) {
			t.Fatal("unknown dates were fabricated")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || strings.Contains(string(after), "durable") {
		t.Fatal("read-only archive invoked SCM's migration writer")
	}
	bounded, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 1)
	if err != nil || len(bounded) != 1 {
		t.Fatal("archive result limit ignored", err)
	}
}

func TestLegacyPreferenceArchiveBoundsCorruptionCancellationAndMissingFile(t *testing.T) {
	workspace := t.TempDir()
	refs, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 200)
	if err != nil || len(refs) != 0 {
		t.Fatal("missing historical file should be empty", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".openkitty")); !os.IsNotExist(err) {
		t.Fatal("archive created absent directories", err)
	}
	path := writeContextArchiveFixture(t, workspace, map[string]interface{}{})
	for _, data := range []string{strings.Repeat(" ", legacyPreferenceArchiveMaxBytes+1), "invalid-json"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 200); err == nil {
			t.Fatal("oversized or corrupt archive accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadLegacyPreferenceArchive(ctx, workspace, 200); err == nil {
		t.Fatal("cancelled read accepted")
	}
	if _, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 201); err == nil {
		t.Fatal("unbounded archive request accepted")
	}
}

func TestLegacyPreferenceArchiveRejectsSymlinkOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".openkitty", "context", "context.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(outside, []byte(`{"secret":{"value":"DO_NOT_EXPOSE","category":"preference"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if refs, err := ReadLegacyPreferenceArchive(context.Background(), workspace, 200); err == nil || len(refs) != 0 {
		t.Fatal("archive followed an outside symlink", refs, err)
	}
}
