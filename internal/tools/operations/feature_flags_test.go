package operations

import (
	"context"
	"strings"
	"testing"
)

func TestFeatureFlagsToolReportsUnknownFlag(t *testing.T) {
	result, err := (&FeatureFlagsTool{}).Execute(context.Background(), map[string]interface{}{"flag": "not-a-feature"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Unknown feature flag") {
		t.Fatalf("unexpected result: %s", result)
	}
}
