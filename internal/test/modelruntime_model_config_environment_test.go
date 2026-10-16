package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const modelEnvSyntheticSecret = "synthetic-runtime-key-123456789"

func writeModelEnvFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func modelEnvRuntimeConfig(environmentName string) *Config {
	return &Config{ModelConfig: map[string]ModelConfig{
		"primary": {
			Type:        "openai",
			APIKey:      modelEnvSyntheticSecret,
			APIKeyEnv:   environmentName,
			ModelName:   "synthetic-model",
			BaseURL:     "https://example.invalid/v1",
			Temperature: 0.2,
			MaxTokens:   1024,
		},
	}}
}

func TestLoadConfigResolvesAPIKeyEnvFromYAMLBeforeValidation(t *testing.T) {
	const environmentName = "OPENKITTY_TEST_MODEL_ENV_YAML"
	t.Setenv(environmentName, modelEnvSyntheticSecret)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeModelEnvFixture(t, path, fmt.Sprintf(`model_cfg:
  primary:
    type: openai
    api_key_env: %s
    model_name: synthetic-model
    base_url: https://example.invalid/v1
`, environmentName))

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	model := config.ModelConfig["primary"]
	if model.APIKey != modelEnvSyntheticSecret {
		t.Fatalf("runtime key was not resolved")
	}
	if model.APIKeyEnv != environmentName {
		t.Fatalf("APIKeyEnv = %q, want %q", model.APIKeyEnv, environmentName)
	}
	if err := ValidateModelConfig("primary", &model); err != nil {
		t.Fatalf("resolved model should validate: %v", err)
	}
}

func TestLoadConfigResolvesInlineAPIKeyEnvironmentReferencesFromJSON(t *testing.T) {
	const environmentName = "OPENKITTY_TEST_MODEL_ENV_JSON"
	t.Setenv(environmentName, modelEnvSyntheticSecret)

	for _, reference := range []string{"${" + environmentName + "}", "env:" + environmentName} {
		t.Run(reference[:3], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			payload := map[string]interface{}{
				"model_cfg": map[string]interface{}{
					"primary": map[string]interface{}{
						"type":       "openai",
						"api_key":    reference,
						"model_name": "synthetic-model",
						"base_url":   "https://example.invalid/v1",
					},
				},
			}
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			writeModelEnvFixture(t, path, string(data))

			config, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			model := config.ModelConfig["primary"]
			if model.APIKey != modelEnvSyntheticSecret || model.APIKeyEnv != environmentName {
				t.Fatalf("reference was not resolved: key matched=%v env=%q", model.APIKey == modelEnvSyntheticSecret, model.APIKeyEnv)
			}
		})
	}
}

func TestSaveConfigPersistsEnvironmentReferenceWithoutResolvedSecret(t *testing.T) {
	const environmentName = "OPENKITTY_TEST_MODEL_ENV_SAVE"
	t.Setenv(environmentName, modelEnvSyntheticSecret)

	for _, extension := range []string{".yaml", ".json"} {
		t.Run(extension, func(t *testing.T) {
			config := modelEnvRuntimeConfig(environmentName)
			path := filepath.Join(t.TempDir(), "config"+extension)
			if err := SaveConfig(path, config); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read saved config: %v", err)
			}
			if strings.Contains(string(data), modelEnvSyntheticSecret) {
				t.Fatalf("saved config leaked the resolved secret")
			}
			if !strings.Contains(string(data), environmentName) {
				t.Fatalf("saved config did not retain environment reference: %s", data)
			}
			if extension == ".json" && !json.Valid(data) {
				t.Fatalf("SaveConfig wrote invalid JSON: %s", data)
			}
			if got := config.ModelConfig["primary"].APIKey; got != modelEnvSyntheticSecret {
				t.Fatalf("SaveConfig mutated runtime key")
			}

			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("reload saved config: %v", err)
			}
			if got := reloaded.ModelConfig["primary"].APIKey; got != modelEnvSyntheticSecret {
				t.Fatalf("round trip runtime key was not resolved")
			}
		})
	}
}

func TestLoadConfigRejectsMissingAndEmptyModelAPIKeyEnvironment(t *testing.T) {
	const missingName = "OPENKITTY_TEST_MODEL_ENV_MISSING_8F31D"
	oldValue, oldPresent := os.LookupEnv(missingName)
	if err := os.Unsetenv(missingName); err != nil {
		t.Fatalf("unset synthetic environment variable: %v", err)
	}
	t.Cleanup(func() {
		if oldPresent {
			_ = os.Setenv(missingName, oldValue)
		} else {
			_ = os.Unsetenv(missingName)
		}
	})

	path := filepath.Join(t.TempDir(), "missing.yaml")
	writeModelEnvFixture(t, path, "model_cfg:\n  primary:\n    api_key_env: "+missingName+"\n")
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), missingName) || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("missing variable error = %v", err)
	}

	const emptyName = "OPENKITTY_TEST_MODEL_ENV_EMPTY"
	t.Setenv(emptyName, " \t ")
	path = filepath.Join(t.TempDir(), "empty.json")
	writeModelEnvFixture(t, path, `{"model_cfg":{"primary":{"api_key":"env:`+emptyName+`"}}}`)
	_, err = LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), emptyName) || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty variable error = %v", err)
	}
}

func TestLoadConfigRejectsInvalidEnvironmentNameWithoutLeakingAPIKey(t *testing.T) {
	const sensitiveFixtureValue = "synthetic-sensitive-value-must-not-appear"
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	writeModelEnvFixture(t, path, `model_cfg:
  primary:
    api_key_env: INVALID-NAME
    api_key: `+sensitiveFixtureValue+`
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "invalid environment variable name") {
		t.Fatalf("invalid name error = %v", err)
	}
	if strings.Contains(err.Error(), sensitiveFixtureValue) {
		t.Fatalf("error leaked API key material: %v", err)
	}
}

func TestLegacyPlaintextModelAPIKeyRemainsCompatible(t *testing.T) {
	const legacyKey = "synthetic-legacy-key-123456789"
	fixtures := map[string]string{
		".yaml": "model_cfg:\n  primary:\n    api_key: " + legacyKey + "\n",
		".json": `{"model_cfg":{"primary":{"api_key":"` + legacyKey + `"}}}`,
	}
	for extension, fixture := range fixtures {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy"+extension)
			writeModelEnvFixture(t, path, fixture)
			config, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			model := config.ModelConfig["primary"]
			if model.APIKey != legacyKey || model.APIKeyEnv != "" {
				t.Fatalf("legacy API key changed during load")
			}
			savedPath := filepath.Join(t.TempDir(), "saved"+extension)
			if err := SaveConfig(savedPath, config); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}
			data, err := os.ReadFile(savedPath)
			if err != nil {
				t.Fatalf("read saved legacy config: %v", err)
			}
			if !strings.Contains(string(data), legacyKey) {
				t.Fatalf("legacy plaintext compatibility was not preserved")
			}
		})
	}
}

func newModelEnvHistoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open history DB: %v", err)
	}
	if err := database.AutoMigrate(&ConfigHistory{}); err != nil {
		t.Fatalf("migrate history DB: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get history SQL DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return database
}

func assertLatestModelEnvHistoryIsSafe(t *testing.T, database *gorm.DB, environmentName string) {
	t.Helper()
	var history ConfigHistory
	if err := database.Order("id desc").First(&history).Error; err != nil {
		t.Fatalf("load config history: %v", err)
	}
	if strings.Contains(history.Config, modelEnvSyntheticSecret) {
		t.Fatalf("config history leaked the resolved secret")
	}
	if !strings.Contains(history.Config, environmentName) {
		t.Fatalf("config history did not retain environment reference: %s", history.Config)
	}
}

func TestGatewayAndClawBotConfigHistoryUsePersistenceView(t *testing.T) {
	const environmentName = "OPENKITTY_TEST_MODEL_ENV_HISTORY"
	config := modelEnvRuntimeConfig(environmentName)
	testLogger := log.New(io.Discard, "", 0)

	t.Run("gateway", func(t *testing.T) {
		database := newModelEnvHistoryDB(t)
		gateway := &GatewayService{db: database, logger: testLogger}
		gateway.saveConfigHistory(config)
		assertLatestModelEnvHistoryIsSafe(t, database, environmentName)
	})

	t.Run("clawbot", func(t *testing.T) {
		database := newModelEnvHistoryDB(t)
		bot := &ClawBot{
			gateway: &GatewayService{db: database, logger: testLogger},
			logger:  testLogger,
		}
		bot.saveConfigHistory(config)
		assertLatestModelEnvHistoryIsSafe(t, database, environmentName)
	})
}
