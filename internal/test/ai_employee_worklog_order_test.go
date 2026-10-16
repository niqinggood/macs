package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAEWorkLogReturnsNewestFirstAndKeepsLatestSuccessfulOutput(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID: "test-professional", Name: "Test Professional", AtName: "test", Role: "test", Pipeline: "test-pipeline",
	}})
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.Local)
	mgr.workLog["test-professional"] = []*AEWorkRecord{
		{ID: "success-old", EmployeeID: "test-professional", Task: "old success", Status: "completed", ExecID: "exec-old", CreatedAt: base, UpdatedAt: base.Add(time.Minute)},
		{ID: "failed-new", EmployeeID: "test-professional", Task: "new failure", Status: "failed", ExecID: "exec-failed", CreatedAt: base.Add(3 * time.Hour), UpdatedAt: base.Add(3 * time.Hour)},
		{ID: "success-new", EmployeeID: "test-professional", Task: "new success", Status: "completed", ExecID: "exec-success", CreatedAt: base.Add(2 * time.Hour), UpdatedAt: base.Add(2 * time.Hour)},
		{ID: "degraded-middle", EmployeeID: "test-professional", Task: "middle degraded", Status: "degraded", ExecID: "exec-degraded", CreatedAt: base.Add(time.Hour), UpdatedAt: base.Add(time.Hour)},
	}

	gateway := &GatewayService{aeManager: mgr}
	req := httptest.NewRequest(http.MethodGet, "/api/ae/worklog", nil)
	rr := httptest.NewRecorder()
	gateway.handleAEWorkLog(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Employees []struct {
			EmployeeID    string          `json:"employee_id"`
			Completed     int             `json:"completed"`
			Degraded      int             `json:"degraded"`
			LatestSuccess *AEWorkRecord   `json:"latest_success"`
			RecentWork    []*AEWorkRecord `json:"recent_work"`
		} `json:"employees"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Employees) != 1 {
		t.Fatalf("employees = %d, want 1", len(body.Employees))
	}
	employee := body.Employees[0]
	if employee.Completed != 2 || employee.Degraded != 1 {
		t.Fatalf("status counts = completed:%d degraded:%d", employee.Completed, employee.Degraded)
	}
	if employee.LatestSuccess == nil || employee.LatestSuccess.ID != "success-new" {
		t.Fatalf("latest success = %#v, want success-new", employee.LatestSuccess)
	}
	if len(employee.RecentWork) != 4 || employee.RecentWork[0].ID != "failed-new" || employee.RecentWork[1].ID != "success-new" {
		t.Fatalf("recent work is not newest-first: %#v", employee.RecentWork)
	}
}
