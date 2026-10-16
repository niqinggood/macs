package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMountAgentOSDataPlaneOnMainMuxIncludesOpenAICompatibility(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mountAgentOSDataPlaneOnMainMux(mux, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{
		"/api/v1/agent/roles",
		"/api/v1/agent/tasks/task-id/events",
		"/v1/models",
		"/v1/chat/completions",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusNoContent {
				t.Fatalf("GET %s returned %d", path, response.Code)
			}
		})
	}
}
