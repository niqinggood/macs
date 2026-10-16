package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBToolGeneratesEscapedCRUDQueries(t *testing.T) {
	report, err := NewDBTool(t.TempDir()).Execute(context.Background(), map[string]interface{}{
		"action": "query",
		"table":  "users`archive",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(report, "`users``archive`") {
		t.Fatalf("query does not escape identifier:\n%s", report)
	}
}

func TestDBToolMigrationsResolveRelativeDirectoryAgainstWorkspace(t *testing.T) {
	workspace := t.TempDir()
	migrationDirectory := filepath.Join(workspace, "db", "migrations")
	if err := os.MkdirAll(migrationDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDirectory, "001_create_users.sql"), []byte("CREATE TABLE users (id INTEGER);"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewDBTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":  "migrate",
		"sql_dir": filepath.Join("db", "migrations"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{"001_create_users.sql", "CREATE TABLE users"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("migration report does not contain %q:\n%s", expected, report)
		}
	}
}

func TestDBToolSchemaScanHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDBTool(t.TempDir()).Execute(ctx, map[string]interface{}{"action": "schema"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Execute() error = %v, want context canceled", err)
	}
}

func TestMaskDSNHidesPassword(t *testing.T) {
	masked := maskDSN("postgres://alice:supersecret@db.example:5432/app")
	if strings.Contains(masked, "supersecret") || masked != "postgres://alice:***@db.example/app" {
		t.Fatalf("maskDSN() = %q", masked)
	}
}
