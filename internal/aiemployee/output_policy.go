package aiemployee

import (
	"context"
	"strings"

	"mac/internal/permissionpolicy"
	"mac/internal/toolcontract"
)

const ArtifactPolicyExplicitRequest = permissionpolicy.ArtifactPolicyExplicitRequest

type outputPolicyContextKey struct{}

type OutputPolicy struct {
	ExplicitArtifactRequired bool
	ArtifactAuthorized       bool
	OriginalTask             string
}

func ExplicitArtifactRequest(trustedInput string) bool {
	return permissionpolicy.ExplicitAIEmployeeArtifactRequest(trustedInput)
}

func WithOutputPolicy(ctx context.Context, direct *DirectConfig, originalTask string, artifactAuthorized bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if direct == nil || !strings.EqualFold(strings.TrimSpace(direct.ArtifactPolicy), ArtifactPolicyExplicitRequest) {
		return ctx
	}
	return context.WithValue(ctx, outputPolicyContextKey{}, OutputPolicy{
		ExplicitArtifactRequired: true,
		ArtifactAuthorized:       artifactAuthorized,
		OriginalTask:             originalTask,
	})
}

func OutputPolicyBlocksTool(context.Context, toolcontract.Call) (bool, string) {
	return false, ""
}

func DirectToolsForRequest(direct *DirectConfig, _ string) []string {
	if direct == nil {
		return nil
	}
	return append([]string(nil), direct.Tools...)
}
