package configvalue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type DatabaseBootstrapConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

func LoadDatabaseBootstrapConfig(configPath string) (DatabaseBootstrapConfig, error) {
	var config DatabaseBootstrapConfig
	if data, err := os.ReadFile(configPath); err == nil {
		var raw map[string]interface{}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return config, err
		}
		if mysql, ok := stringInterfaceMap(raw["mysql"]); ok {
			config.Host = bootstrapString(mysql["host"])
			config.Port = bootstrapString(mysql["port"])
			config.User = bootstrapString(mysql["user"])
			config.Password = bootstrapString(mysql["passwd"])
			if config.Password == "" {
				config.Password = bootstrapString(mysql["password"])
			}
			config.Database = bootstrapString(mysql["dbname"])
			if config.Database == "" {
				config.Database = bootstrapString(mysql["database"])
			}
		}
	} else if !os.IsNotExist(err) {
		return config, err
	}
	applyDatabaseEnv := func(name string, target *string) {
		if value, ok := os.LookupEnv(name); ok {
			*target = strings.TrimSpace(value)
		}
	}
	applyDatabaseEnv("OPENKITTY_MYSQL_HOST", &config.Host)
	applyDatabaseEnv("OPENKITTY_MYSQL_PORT", &config.Port)
	applyDatabaseEnv("OPENKITTY_MYSQL_USER", &config.User)
	applyDatabaseEnv("OPENKITTY_MYSQL_PASSWORD", &config.Password)
	applyDatabaseEnv("OPENKITTY_MYSQL_DATABASE", &config.Database)
	if config.Host != "" && config.Port == "" {
		config.Port = "3306"
	}
	return config, nil
}

func stringInterfaceMap(value interface{}) (map[string]interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		return typed, true
	case map[interface{}]interface{}:
		converted := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			converted[fmt.Sprint(key)] = item
		}
		return converted, true
	default:
		return nil, false
	}
}

func bootstrapString(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func ConfiguredSQLitePath(fallback string) string {
	if configured := strings.TrimSpace(os.Getenv("OPENKITTY_SQLITE_PATH")); configured != "" {
		if absolute, err := filepath.Abs(configured); err == nil {
			return absolute
		}
		return configured
	}
	return fallback
}
