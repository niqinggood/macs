package macs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubmitTaskSendsIdentityAndIdempotency(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/agent/tasks" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer service-token" {
			t.Errorf("Authorization = %q", got)
			return
		}
		if got := r.Header.Get("X-OpenKitty-User-ID"); got != "person-42" {
			t.Errorf("act-as user = %q", got)
			return
		}
		if got := r.Header.Get("Idempotency-Key"); got != "lims-order-42" {
			t.Errorf("Idempotency-Key = %q", got)
			return
		}
		var request SubmitTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.RoleID != "laboratory-report" || request.Message != "fill report" {
			t.Errorf("request = %#v", request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"agt_42","status":"pending","created_at":"2026-09-13T00:00:00Z","links":{}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithBearerToken("service-token"), WithActAsUser("person-42"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.SubmitTask(context.Background(), SubmitTaskRequest{
		RoleID:  "laboratory-report",
		Message: "fill report",
	}, "lims-order-42")
	if err != nil {
		t.Fatal(err)
	}
	if task.TaskID != "agt_42" || task.Status != "pending" {
		t.Fatalf("task = %#v", task)
	}
}

func TestAPIErrorPreservesRetryInformation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"queue full","code":"queue_full","message":"queue full","request_id":"req-1","retryable":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetTask(context.Background(), "agt_42")
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %T %v", err, err)
	}
	if apiError.Code != "queue_full" || !apiError.Retryable || apiError.RetryAfter != 3*time.Second {
		t.Fatalf("API error = %#v", apiError)
	}
}

func TestListAndCancelTask(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agent/tasks":
			if r.URL.Query().Get("role_id") != "lab" || r.URL.Query().Get("limit") != "7" {
				t.Errorf("query = %s", r.URL.RawQuery)
				return
			}
			_, _ = w.Write([]byte(`{"tasks":[],"count":0,"next_cursor":"next"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agent/tasks/agt_42/cancel":
			_, _ = w.Write([]byte(`{"task_id":"agt_42","status":"stopped","created_at":"2026-09-13T00:00:00Z","links":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListTasks(context.Background(), ListTaskOptions{RoleID: "lab", Limit: 7})
	if err != nil || page.NextCursor != "next" {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
	task, err := client.CancelTask(context.Background(), "agt_42")
	if err != nil || !task.Terminal() {
		t.Fatalf("task = %#v, err = %v", task, err)
	}
}

func TestTaskFollowupMethods(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agent/tasks/agt_42/followups":
			if r.Header.Get("Idempotency-Key") != "correction-1" {
				t.Errorf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			var request struct {
				Message string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Message != "use sample B" {
				t.Errorf("request=%#v err=%v", request, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"followup_id":"fup_1","task_id":"agt_42","status":"pending","message":"use sample B","created_at":"2026-09-15T00:00:00Z","links":{}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agent/tasks/agt_42/followups/fup_1":
			_, _ = w.Write([]byte(`{"followup_id":"fup_1","task_id":"agt_42","status":"applied","message":"use sample B","created_at":"2026-09-15T00:00:00Z","links":{}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agent/tasks/agt_42/followups":
			if r.URL.Query().Get("limit") != "10" {
				t.Errorf("limit = %q", r.URL.Query().Get("limit"))
			}
			_, _ = w.Write([]byte(`{"followups":[{"followup_id":"fup_1","task_id":"agt_42","status":"applied","message":"use sample B","created_at":"2026-09-15T00:00:00Z","links":{}}],"count":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	followup, err := client.AddTaskFollowup(context.Background(), "agt_42", "use sample B", "correction-1")
	if err != nil || followup.FollowupID != "fup_1" {
		t.Fatalf("followup=%#v err=%v", followup, err)
	}
	stored, err := client.GetTaskFollowup(context.Background(), "agt_42", "fup_1")
	if err != nil || stored.Status != "applied" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	list, err := client.ListTaskFollowups(context.Background(), "agt_42", 10)
	if err != nil || list.Count != 1 || len(list.Followups) != 1 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
}
