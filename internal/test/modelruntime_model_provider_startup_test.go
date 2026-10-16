package app

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newProviderStartupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open provider test DB: %v", err)
	}
	if err := db.AutoMigrate(&ModelProviderRecord{}, &AEConfigRecord{}); err != nil {
		t.Fatalf("migrate provider test DB: %v", err)
	}
	return db
}

func TestInitModelProvidersDoesNotRewriteLegacyConfig(t *testing.T) {
	previousRegistry := globalProviderRegistry
	setAIEmployeeDefaultModelBinding("")
	t.Cleanup(func() {
		globalProviderRegistry = previousRegistry
		setAIEmployeeDefaultModelBinding("")
	})

	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	original := []byte("# preserve comments and compact formatting\nmodel_cfg: {}\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	config := &Config{Workspace: t.TempDir(), ModelConfig: map[string]ModelConfig{
		"local_model": {
			Type:      "ollama",
			ModelName: "qwen-test",
			BaseURL:   "http://127.0.0.1:11434/v1",
		},
	}}
	gateway := &GatewayService{
		db:         newProviderStartupTestDB(t),
		config:     config,
		configpath: configPath,
		logger:     log.New(io.Discard, "", 0),
	}

	gateway.initModelProviders(config)
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("provider initialization rewrote config:\n%s", after)
	}
	if len(config.ModelProviders) != 0 {
		t.Fatalf("startup must not materialize model_providers into config: %#v", config.ModelProviders)
	}
	provider, ok := globalProviderRegistry.Get("local_model")
	if !ok || provider == nil || provider.Type != "ollama" {
		t.Fatalf("legacy local model was not available in the runtime registry: %#v", provider)
	}
}

func TestExplicitProviderMigrationBacksUpThenAtomicallySaves(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	original := []byte("# original user formatting\nteam_name: compact\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{
		config:     &Config{},
		configpath: configPath,
		logger:     log.New(io.Discard, "", 0),
	}
	providers := []*ModelProvider{{
		Name:    "local",
		Label:   "Local",
		Type:    "ollama",
		BaseURL: "http://127.0.0.1:11434/v1",
		Models:  []ProviderModel{{Name: "qwen-test", Capability: "text-to-text"}},
	}}

	if err := gateway.saveModelProvidersToConfig(providers); err != nil {
		t.Fatalf("explicit provider migration: %v", err)
	}
	backups, err := filepath.Glob(configPath + ".pre-provider-migration-*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backup count = %d, err=%v", len(backups), err)
	}
	backupBytes, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backupBytes, original) {
		t.Fatalf("migration backup did not preserve original bytes: %q", backupBytes)
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("saved provider config is not parseable: %v", err)
	}
	if len(loaded.ModelProviders) != 1 || loaded.ModelProviders[0].Name != "local" {
		t.Fatalf("explicit provider migration was not saved: %#v", loaded.ModelProviders)
	}

	if err := gateway.saveModelProvidersToConfig(providers); err != nil {
		t.Fatalf("second provider save: %v", err)
	}
	backups, err = filepath.Glob(configPath + ".pre-provider-migration-*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("ordinary provider save created another migration backup: %v, %v", backups, err)
	}
	if atomicTemps, globErr := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".atomic-*")); globErr != nil || len(atomicTemps) != 0 {
		t.Fatalf("atomic config save leaked temporary files: %v, %v", atomicTemps, globErr)
	}
}
