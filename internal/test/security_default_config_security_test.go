package app

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultConfigSourcesContainNoUsableCredentials(t *testing.T) {
	t.Run("embedded YAML", func(t *testing.T) {
		var config interface{}
		if err := yaml.Unmarshal(defaultConfigYAML, &config); err != nil {
			t.Fatalf("parse embedded default config: %v", err)
		}
		assertSafeDefaultConfig(t, config)
	})

	t.Run("generated config", func(t *testing.T) {
		data, err := yaml.Marshal(generateTemplateConfig())
		if err != nil {
			t.Fatalf("serialize generated default config: %v", err)
		}
		var config interface{}
		if err := yaml.Unmarshal(data, &config); err != nil {
			t.Fatalf("parse generated default config: %v", err)
		}
		assertSafeDefaultConfig(t, config)
	})

	t.Run("legacy JSON", func(t *testing.T) {
		data, err := os.ReadFile("config.json")
		if err != nil {
			t.Fatalf("read config.json: %v", err)
		}
		var config interface{}
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("parse config.json: %v", err)
		}
		assertSafeDefaultConfig(t, config)
	})
}

func assertSafeDefaultConfig(t *testing.T, config interface{}) {
	t.Helper()
	root, ok := config.(map[string]interface{})
	if !ok {
		t.Fatalf("default config root has type %T, want mapping", config)
	}
	if _, exists := root["workspace"]; exists {
		t.Fatal("default config must not persist the runtime workspace")
	}
	walkDefaultConfig(t, "", root)
}

func walkDefaultConfig(t *testing.T, path string, value interface{}) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if text, ok := child.(string); ok {
				if isSensitiveConfigField(key) &&
					strings.TrimSpace(text) != "" &&
					!isDefaultCredentialPlaceholder(text) {
					t.Fatalf("default config contains a credential at %s", childPath)
				}
				if strings.EqualFold(strings.TrimSpace(text), "deepseek-chat") {
					t.Fatalf("default config contains retired model deepseek-chat at %s", childPath)
				}
			}
			walkDefaultConfig(t, childPath, child)
		}
	case []interface{}:
		for index, child := range typed {
			walkDefaultConfig(t, path+"["+strconv.Itoa(index)+"]", child)
		}
	}
}

func isDefaultCredentialPlaceholder(value string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	for _, prefix := range []string{"YOUR_", "REPLACE_", "CHANGE_ME", "EXAMPLE_"} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
