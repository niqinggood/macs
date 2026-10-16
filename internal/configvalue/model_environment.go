package configvalue

import (
	"fmt"
	"regexp"
	"strings"
)

var modelAPIKeyEnvironmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ModelAPIKey is the persistence-neutral API-key state used by configuration
// adapters. Resolved distinguishes an environment-derived runtime secret from
// an unresolved inline reference.
type ModelAPIKey struct {
	APIKey    string
	APIKeyEnv string
	Resolved  bool
}

// Serializable removes a resolved secret while retaining its portable
// environment reference.
func (model ModelAPIKey) Serializable() ModelAPIKey {
	environmentPresent := model.APIKeyEnv != ""
	model.APIKeyEnv = strings.TrimSpace(model.APIKeyEnv)
	if environmentPresent {
		model.APIKey = ""
		if model.APIKeyEnv != "" {
			model.APIKey = "${" + model.APIKeyEnv + "}"
		}
	}
	model.Resolved = false
	return model
}

// ParseModelAPIKeyEnvironmentReference recognizes the supported inline forms.
func ParseModelAPIKeyEnvironmentReference(raw string) (string, bool, error) {
	value := strings.TrimSpace(raw)
	var name string
	switch {
	case strings.HasPrefix(value, "${"):
		if len(value) < 4 || !strings.HasSuffix(value, "}") {
			return "", true, fmt.Errorf("malformed ${ENV_NAME} reference")
		}
		name = strings.TrimSpace(value[2 : len(value)-1])
	case strings.HasPrefix(value, "env:"):
		name = strings.TrimSpace(strings.TrimPrefix(value, "env:"))
	default:
		return "", false, nil
	}
	if !modelAPIKeyEnvironmentNamePattern.MatchString(name) {
		return "", true, fmt.Errorf("environment variable name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	return name, true, nil
}

// ModelAPIKeyEnvironmentName validates and returns a model's environment
// source without exposing the API-key value in errors.
func ModelAPIKeyEnvironmentName(modelName string, model ModelAPIKey) (string, error) {
	explicitPresent := model.APIKeyEnv != ""
	explicitName := strings.TrimSpace(model.APIKeyEnv)
	if explicitPresent && !modelAPIKeyEnvironmentNamePattern.MatchString(explicitName) {
		return "", fmt.Errorf("model_cfg.%q api_key_env has an invalid environment variable name; expected [A-Za-z_][A-Za-z0-9_]*", modelName)
	}
	if explicitPresent && model.Resolved {
		return explicitName, nil
	}

	inlineName, inlinePresent, err := ParseModelAPIKeyEnvironmentReference(model.APIKey)
	if err != nil {
		return "", fmt.Errorf("model_cfg.%q api_key has an invalid environment reference: %w", modelName, err)
	}
	if explicitPresent && inlinePresent && explicitName != inlineName {
		return "", fmt.Errorf("model_cfg.%q has conflicting API key environment references %q and %q", modelName, explicitName, inlineName)
	}
	if explicitPresent {
		return explicitName, nil
	}
	if inlinePresent {
		return inlineName, nil
	}
	return "", nil
}

// MergeModelAPIKeyFields applies API-key and environment-source updates as one
// state transition so a stale source or plaintext value cannot survive.
func MergeModelAPIKeyFields(model ModelAPIKey, update map[string]interface{}) ModelAPIKey {
	if update == nil {
		return model
	}
	environmentName, environmentProvided := update["api_key_env"].(string)
	apiKey, apiKeyProvided := update["api_key"].(string)
	if environmentProvided {
		model.APIKeyEnv = environmentName
		if strings.TrimSpace(environmentName) == "" && !apiKeyProvided {
			model.APIKey = ""
		}
	}
	if apiKeyProvided {
		model.APIKey = apiKey
		if !environmentProvided {
			model.APIKeyEnv = ""
		}
	}
	if environmentProvided || apiKeyProvided {
		model.Resolved = false
	}
	return model
}

// ResolveModelAPIKey replaces a configured environment reference with its
// runtime value. Plaintext legacy keys pass through unchanged.
func ResolveModelAPIKey(modelName string, model ModelAPIKey, lookup func(string) (string, bool)) (ModelAPIKey, error) {
	environmentName, err := ModelAPIKeyEnvironmentName(modelName, model)
	if err != nil || environmentName == "" {
		return model, err
	}
	if lookup == nil {
		return model, fmt.Errorf("model API key environment lookup is unavailable")
	}
	value, ok := lookup(environmentName)
	if !ok {
		return model, fmt.Errorf("model_cfg.%q API key environment variable %q is not set", modelName, environmentName)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return model, fmt.Errorf("model_cfg.%q API key environment variable %q is empty", modelName, environmentName)
	}
	model.APIKeyEnv = environmentName
	model.APIKey = value
	model.Resolved = true
	return model, nil
}
