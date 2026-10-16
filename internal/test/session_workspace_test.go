package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateSessionUsesServerIDAndBindsWorkspace(t *testing.T) {
	gateway, db := newSessionListTestGateway(t)
	gateway.project = NewProjectContext(t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"id":"web_old_reused"}`))
	rec := httptest.NewRecorder()
	gateway.handleListSessions(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Session ChatSession `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.ID == "web_old_reused" || !strings.HasPrefix(payload.Session.ID, "web_") {
		t.Fatalf("server session id = %q", payload.Session.ID)
	}
	if payload.Session.Workspace != gateway.project.Workspace || payload.Session.ProjectID != gateway.project.ProjectID {
		t.Fatalf("session workspace binding = %#v, project = %#v", payload.Session, gateway.project)
	}

	foreign := ChatSession{ID: "web_foreign", Name: "web_foreign", Workspace: t.TempDir(), ProjectID: "proj_foreign"}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.bindSessionWorkspace(foreign.ID, "", "", false); err == nil {
		t.Fatal("expected a session bound to another workspace to be rejected")
	}
}
