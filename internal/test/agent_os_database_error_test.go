package app

import (
	"net/http"
	"testing"
)

func TestAgentOSTaskReadAPIsReportDatabaseOutage(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	sqlDB, err := g.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	targets := []string{
		"/api/v1/agent/tasks",
		"/api/v1/agent/tasks/agt_unavailable",
		"/api/v1/agent/tasks/agt_unavailable/events",
		"/api/v1/agent/tasks/agt_unavailable/approvals",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			response := performAgentOSRequest(
				g, http.MethodGet, target, nil, nil,
			)
			if response.code != http.StatusServiceUnavailable {
				t.Fatalf(
					"status=%d, want 503; body=%s",
					response.code, response.raw,
				)
			}
			if response.body["code"] != "service_unavailable" {
				t.Fatalf("error contract=%#v", response.body)
			}
		})
	}
}
