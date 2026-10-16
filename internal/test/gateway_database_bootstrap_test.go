package app

import (
	"os"
	"path/filepath"
	"testing"

	"mac/internal/configvalue"
)

func TestDatabaseBootstrapConfigSupportsYAMLAndEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	content := []byte("mysql:\n  host: mysql.internal\n  port: 3307\n  user: kitty\n  passwd: from-file\n  dbname: openkitty\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENKITTY_MYSQL_PASSWORD", "from-secret")
	config, err := configvalue.LoadDatabaseBootstrapConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "mysql.internal" || config.Port != "3307" || config.User != "kitty" || config.Password != "from-secret" || config.Database != "openkitty" {
		t.Fatalf("database config = %+v", config)
	}
}

func TestDatabaseBootstrapConfigUsesKubernetesEnvironmentOnly(t *testing.T) {
	t.Setenv("OPENKITTY_MYSQL_HOST", "mysql.default.svc")
	t.Setenv("OPENKITTY_MYSQL_USER", "openkitty")
	t.Setenv("OPENKITTY_MYSQL_PASSWORD", "secret")
	t.Setenv("OPENKITTY_MYSQL_DATABASE", "agentos")
	config, err := configvalue.LoadDatabaseBootstrapConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != "3306" || config.Host != "mysql.default.svc" || config.Database != "agentos" {
		t.Fatalf("database config = %+v", config)
	}
}

func TestConfiguredSQLitePathAllowsPersistentVolumeOverride(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "data", "openkitty.db")
	t.Setenv("OPENKITTY_SQLITE_PATH", configured)
	if got := configvalue.ConfiguredSQLitePath("ignored"); got != configured {
		t.Fatalf("sqlite path = %q, want %q", got, configured)
	}
}
