package llmapp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

func resolveLLMAppJSONSchema(schema map[string]interface{}) (*jsonschema.Resolved, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode output schema: %w", err)
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode output schema: %w", err)
	}
	// Application contracts are self-contained. A schema must never trigger a
	// network read while an application is being created or executed.
	resolved, err := parsed.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema: %w", err)
	}
	return resolved, nil
}

func validateLLMAppJSONSchema(schema map[string]interface{}) error {
	_, err := resolveLLMAppJSONSchema(schema)
	return err
}

func decodeLLMAppStructuredOutput(output string) (interface{}, error) {
	var value interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return value, nil
}

func validateLLMAppJSONValue(schema map[string]interface{}, value interface{}) error {
	resolved, err := resolveLLMAppJSONSchema(schema)
	if err != nil || resolved == nil {
		return err
	}
	if err := resolved.Validate(value); err != nil {
		return fmt.Errorf("JSON schema validation failed: %w", err)
	}
	return nil
}
