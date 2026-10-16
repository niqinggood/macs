package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
)

func TestPersonalMemoryViewsLegacyPreferencesRemainReadonlyUnverifiedAndWorkspaceBound(t *testing.T) {
	g := newMemoryScopeGateway(t)
	workspace := g.currentProjectWorkspace()
	path := filepath.Join(workspace, ".openkitty", "context", "context.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]interface{})
	for index := 0; index < 103; index++ {
		rows[fmt.Sprintf("preference_%d", index)] = map[string]interface{}{"key": fmt.Sprintf("old preference %d", index), "value": "Previously saved workspace preference", "category": "preference"}
	}
	rows["session"] = map[string]interface{}{"key": "FOREIGN_SESSION", "value": "DO_NOT_EXPOSE", "category": "preference", "session_id": "other"}
	rows["workspace"] = map[string]interface{}{"key": "FOREIGN_WORKSPACE", "value": "DO_NOT_EXPOSE", "category": "preference", "workspace": t.TempDir()}
	rows["secret"] = map[string]interface{}{"key": "historic secret", "value": "api_key=fixture_private_credential_12345", "category": "preference"}
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// An external SQLite cannot establish the local database/workspace archive
	// boundary, even when a similarly named context file exists in this folder.
	result, _ := memoryViewRead(t, g, "view=preferences")
	if len(result.Supplemental) != 0 {
		t.Fatal("external database enabled local legacy archive")
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(workspace, "local.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := memorycore.InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&ChatSession{}, &GatewayTaskRecord{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	g.db = db
	if err := db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	result, body := memoryViewRead(t, g, "view=preferences")
	if result.Total != 0 || len(result.Entries) != 0 || len(result.Supplemental) != 104 || strings.Contains(body, "fixture_private_credential_12345") || strings.Contains(body, "DO_NOT_EXPOSE") {
		t.Fatal("real old preference archive was lost, exposed or promoted", body)
	}
	for _, ref := range result.Supplemental {
		if ref.SourceType != "legacy_preference" || ref.AgentName != "" || ref.OwnershipVerified == nil || *ref.OwnershipVerified || !ref.ReadOnly || ref.Scope != memorycore.MemoryProject {
			t.Fatal("unverified historical preference falsely attributed/activated", ref)
		}
		if ref.Provenance == nil || ref.Provenance.Identity != "workspace_archive" || ref.Provenance.Content != "historical_unverified" || ref.Provenance.Origin.Location != ".openkitty/context/context.json" || ref.Provenance.Origin.RecordID != ref.SourceID || ref.Provenance.Evidence.UserQuote != "missing" || ref.Provenance.Recall.Eligibility != "archive_only" || ref.Provenance.Recall.Enabled != nil {
			t.Fatal("archive provenance fabricated evidence or automatic recall", ref.Provenance)
		}
	}
	after, _ := os.ReadFile(path)
	if string(data) != string(after) {
		t.Fatal("listing preferences migrated or rewrote the old SCM file")
	}
	var entries int64
	if err := db.Model(&memorycore.Entry{}).Count(&entries).Error; err != nil || entries != 0 {
		t.Fatal("legacy preferences automatically created confirmed entries", err)
	}
	for _, query := range []string{"view=short", "view=long", "view=global", "view=history", "view=preferences&state=candidate"} {
		result, _ := memoryViewRead(t, g, query)
		for _, ref := range result.Supplemental {
			if ref.SourceType == "legacy_preference" {
				t.Fatal("archive escaped preferences or active-only source filtering", query)
			}
		}
	}
	// A damaged archive does not hide personal preferences or fail the view.
	if err := os.WriteFile(path, []byte("bad JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	_, body = memoryViewRead(t, g, "view=preferences")
	if !strings.Contains(body, "legacy_preferences_unavailable") {
		t.Fatal("corrupt archive should be safely explained", body)
	}
}
