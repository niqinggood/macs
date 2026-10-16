package operations

import (
	"net/url"
	"strings"
	"testing"
)

func TestHealthCheckFormat(t *testing.T) {
	results := []HealthCheck{
		ok("system", "OS: linux/amd64", "Go: go1.21"),
		warn("git", "Not a git repository"),
		fail("tools", "Critical tool missing: read_file"),
		ok("hooks", "No hooks installed"),
	}
	report := formatHealthReport(results)
	if report == "" {
		t.Error("report should not be empty")
	}
	if !strings.Contains(report, "ok") && !strings.Contains(report, "warn") && !strings.Contains(report, "error") {
		t.Error("report should contain status indicators")
	}
	if !strings.Contains(report, "Summary") {
		t.Error("report should have summary")
	}
}

func TestHealthCheckStatusFunctions(t *testing.T) {
	o := ok("test", "detail1")
	if o.Status != "ok" || o.Name != "test" || len(o.Details) != 1 {
		t.Error("ok() malformed")
	}
	w := warn("test", "detail1", "detail2")
	if w.Status != "warn" || len(w.Details) != 2 {
		t.Error("warn() malformed")
	}
	f := fail("test")
	if f.Status != "error" || len(f.Details) != 0 {
		t.Error("fail() malformed")
	}
}

func TestDoctorParametersIncludeNetworkSearch(t *testing.T) {
	tool := NewDoctorTool(nil)
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	check := props["check"].(map[string]interface{})
	enum := check["enum"].([]string)
	got := strings.Join(enum, ",")
	if !strings.Contains(got, "network") || !strings.Contains(got, "search") || !strings.Contains(got, "shell") || !strings.Contains(got, "agent_reach") {
		t.Fatalf("doctor check enum should include network/search/shell/agent_reach: %v", enum)
	}
}

func TestDoctorShellSafetyCheck(t *testing.T) {
	check := NewDoctorTool(nil).checkShellSafety()
	if check.Status != "ok" {
		t.Fatalf("shell check status = %s details=%v", check.Status, check.Details)
	}
	joined := strings.Join(check.Details, "\n")
	for _, want := range []string{"Shell analyzer: enabled", "git status --short", "go test ./...", "EncodedCommand", "suggested_rule=cli(go test)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("shell check missing %q: %s", want, joined)
		}
	}
}

func TestSuspiciousResolvedIPFlagsDuckDuckGoMetaRange(t *testing.T) {
	reason := suspiciousResolvedIP("html.duckduckgo.com", "157.240.3.50")
	if !strings.Contains(reason, "Meta") {
		t.Fatalf("expected Meta/Facebook warning, got %q", reason)
	}
	if got := suspiciousResolvedIP("www.bing.com", "157.240.3.50"); got != "" {
		t.Fatalf("non-DuckDuckGo host should not flag Meta range, got %q", got)
	}
}

func TestSanitizeProxyValueRemovesCredentials(t *testing.T) {
	got := sanitizeProxyValue("http://user:pass@127.0.0.1:7890")
	if strings.Contains(got, "user") || strings.Contains(got, "pass") {
		t.Fatalf("proxy credentials leaked: %s", got)
	}
	u, err := url.Parse(got)
	if err != nil || u.Host != "127.0.0.1:7890" {
		t.Fatalf("unexpected sanitized proxy: %s err=%v", got, err)
	}
}

func TestFormatEndpointDiagnosisIncludesLayers(t *testing.T) {
	diag := endpointDiagnosis{
		Name:       "DuckDuckGo",
		Host:       "html.duckduckgo.com",
		IPs:        []string{"157.240.3.50"},
		DNSMS:      12,
		TCP:        "fail: i/o timeout",
		TCPMS:      3000,
		HTTP:       "fail: context deadline exceeded",
		HTTPMS:     5000,
		Suspicious: []string{"157.240.3.50 (known Meta/Facebook range; likely DNS pollution for search host)"},
	}
	line := formatEndpointDiagnosis(diag)
	for _, want := range []string{"DuckDuckGo", "DNS:", "TCP:", "HTTP:", "suspicious DNS", "157.240.3.50"} {
		if !strings.Contains(line, want) {
			t.Fatalf("diagnosis missing %q: %s", want, line)
		}
	}
}
