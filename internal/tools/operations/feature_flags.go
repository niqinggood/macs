package operations

import (
	"context"
	"fmt"

	"mac/internal/configvalue"
	"mac/internal/featureflags"
)

// FeatureFlagsTool exposes the process feature registry as a read-only tool.
type FeatureFlagsTool struct{}

func (tool *FeatureFlagsTool) Name() string { return "feature_flags" }

func (tool *FeatureFlagsTool) Description() string {
	return "Show the status of all feature flags. Use to check which subsystems are currently enabled."
}

func (tool *FeatureFlagsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"flag": map[string]interface{}{
				"type": "string", "description": "Optional: check a specific flag. Empty = show all.",
			},
		},
	}
}

func (tool *FeatureFlagsTool) IsReadOnly() bool { return true }

func (tool *FeatureFlagsTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	requested := configvalue.String(params, "flag", "")
	if requested == "" {
		return featureflags.Global().Status(), nil
	}
	for _, flag := range featureflags.All() {
		if string(flag) != requested {
			continue
		}
		if featureflags.Global().IsEnabled(flag) {
			return fmt.Sprintf("✅ Feature flag `%s` is ENABLED.", flag), nil
		}
		return fmt.Sprintf("⛔ Feature flag `%s` is DISABLED.", flag), nil
	}
	return fmt.Sprintf("Unknown feature flag: %s", requested), nil
}
