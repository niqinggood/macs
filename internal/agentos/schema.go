package agentos

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

func CloneJSONSchema(schema map[string]interface{}) (map[string]interface{}, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func resolveJSONSchema(schema map[string]interface{}) (*jsonschema.Resolved, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	// Enterprise role contracts are self-contained and cannot load remote
	// schemas while a task is being validated.
	resolved, err := parsed.Resolve(nil)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func ValidateJSONSchema(schema map[string]interface{}) error {
	_, err := resolveJSONSchema(schema)
	return err
}

func ValidateJSON(schema map[string]interface{}, value interface{}) error {
	resolved, err := resolveJSONSchema(schema)
	if err != nil || resolved == nil {
		return err
	}
	return resolved.Validate(value)
}

func ValidateRevisionBaseURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("model base URL is invalid")
	}
	if parsed.User != nil {
		return fmt.Errorf("model base URL must not contain userinfo credentials")
	}
	for key := range parsed.Query() {
		if sensitiveURLQueryKey(key) {
			return fmt.Errorf("model base URL must not contain sensitive query parameters")
		}
	}
	return nil
}

func sensitiveURLQueryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_").Replace(key)
	switch key {
	case "api_key", "apikey", "key",
		"api_secret", "secret", "secret_key",
		"access_token", "refresh_token", "token",
		"client_secret", "password",
		"authorization", "auth",
		"x_api_key", "signature", "sig":
		return true
	default:
		return false
	}
}
