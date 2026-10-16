package security

import (
	"strings"
	"testing"
)

func TestExternalEvidenceHints(t *testing.T) {
	hints := strings.Join(ExternalEvidenceHints("台风贝碧嘉外围环流，来源 https://example.com/weather。"), " | ")
	for _, want := range []string{"https://example.com/weather", "外围环流", "贝碧嘉", "named event: 贝碧嘉"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("hints %q missing %q", hints, want)
		}
	}
}

func TestUnsupportedHighRiskClaims(t *testing.T) {
	issues := strings.Join(UnsupportedHighRiskClaims(
		"受台风贝碧嘉影响，周六晚有强降雨，详见 https://invented.invalid。",
		"预报显示周六有降雨。",
	), " | ")
	for _, want := range []string{"named event: 贝碧嘉", "uncited URL: https://invented.invalid", "unsupported sub-daily weather timing"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("issues %q missing %q", issues, want)
		}
	}
}

func TestGenericStormWordingIsNotNamedEvent(t *testing.T) {
	issues := strings.Join(UnsupportedHighRiskClaims("台风背景仍不明确。", "天气资料有限。"), " | ")
	if strings.Contains(issues, "named event") {
		t.Fatalf("generic wording was treated as a named event: %q", issues)
	}
}
