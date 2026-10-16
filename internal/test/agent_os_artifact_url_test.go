package app

import (
	"strings"
	"testing"
)

func TestAgentOSArtifactDownloadURLSupportsConfiguredSameOriginProxy(t *testing.T) {
	const artifactPath = ".openkitty/storage/fundraising plan.pptx"
	if got := agentOSArtifactDownloadURLAt(artifactPath, ""); got != "/api/v1/agent/files?path=.openkitty%2Fstorage%2Ffundraising+plan.pptx" {
		t.Fatalf("default download URL = %q", got)
	}
	if got := agentOSArtifactDownloadURLAt(artifactPath, "/nova/ok/files/"); got != "/nova/ok/files?path=.openkitty%2Fstorage%2Ffundraising+plan.pptx" {
		t.Fatalf("Nova download URL = %q", got)
	}
	for _, invalid := range []string{
		"https://files.example.com/download",
		"//files.example.com/download",
		"/nova/ok/files?token=secret",
		"/nova/../",
	} {
		if endpoint, ok := normalizeAgentOSArtifactDownloadEndpoint(invalid); ok {
			t.Fatalf("unsafe endpoint %q normalized to %q", invalid, endpoint)
		}
	}
}

func TestGatewayRewritesPersistedArtifactLinksForNovaWithoutTouchingOtherAPIs(t *testing.T) {
	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{
		AgentOSArtifactDownloadPath: "/nova/ok/files",
	}}}
	input := "交付物：[deck.pptx](/api/v1/agent/files?path=.openkitty%2Fstorage%2Fdeck.pptx)\n" +
		"交付摘要：[run](/api/delivery/summary?exec_id=run)"
	got := gateway.publicAgentOSArtifactLinks(input)
	if !strings.Contains(got, "](/nova/ok/files?path=.openkitty%2Fstorage%2Fdeck.pptx)") {
		t.Fatalf("artifact link was not rewritten: %s", got)
	}
	if !strings.Contains(got, "](/api/delivery/summary?exec_id=run)") {
		t.Fatalf("unrelated API link was changed: %s", got)
	}
}

func TestGatewayRejectsExternalArtifactProxyEndpoint(t *testing.T) {
	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{
		AgentOSArtifactDownloadPath: "https://evil.example/download",
	}}}
	if got := gateway.agentOSArtifactDownloadEndpoint(); got != defaultAgentOSArtifactDownloadEndpoint {
		t.Fatalf("external artifact endpoint was accepted: %q", got)
	}
}
