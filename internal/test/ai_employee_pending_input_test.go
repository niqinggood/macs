package app

import (
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

func TestLatestWaitingInputRecordIsScopedByEmployeeAndSession(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.Default())
	now := time.Now()
	mgr.workLog["content-editor"] = []*AEWorkRecord{
		{ID: "wrong-session", EmployeeID: "content-editor", SessionID: "session-b", Status: aeWaitingInputStatus, UpdatedAt: now},
		{ID: "older", EmployeeID: "content-editor", SessionID: "session-a", Status: aeWaitingInputStatus, UpdatedAt: now.Add(-time.Minute)},
		{ID: "latest", EmployeeID: "content-editor", SessionID: "session-a", Status: aeWaitingInputStatus, UpdatedAt: now},
	}
	mgr.workLog["bid-specialist"] = []*AEWorkRecord{
		{ID: "wrong-employee", EmployeeID: "bid-specialist", SessionID: "session-a", Status: aeWaitingInputStatus, UpdatedAt: now.Add(time.Minute)},
	}

	got := mgr.latestWaitingInputRecord("content-editor", "session-a")
	if got == nil || got.ID != "latest" {
		t.Fatalf("latest scoped pending record = %#v, want latest", got)
	}
	if got := mgr.latestWaitingInputRecord("content-editor", "session-missing"); got != nil {
		t.Fatalf("different session must not inherit pending context: %#v", got)
	}
}

func TestWaitingInputIsNotSupersededByOrdinaryMessage(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.Default())
	now := time.Now()
	mgr.workLog["investment-advisor"] = []*AEWorkRecord{{
		ID: "pending", EmployeeID: "investment-advisor", SessionID: "session-a",
		Status: aeWaitingInputStatus, Result: "缺少决定性输入", UpdatedAt: now,
	}}

	mgr.supersedeWaitingInputForExplicitInvocation(context.Background(), "investment-advisor", "session-a")
	got := mgr.workLog["investment-advisor"][0]
	if got.Status != aeWaitingInputStatus || got.Result != "缺少决定性输入" {
		t.Fatalf("ordinary unmentioned message mutated pending record: %#v", got)
	}
}

func TestExplicitMentionSupersedesPendingAndStartsFresh(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.Default())
	now := time.Now()
	mgr.workLog["investment-advisor"] = []*AEWorkRecord{{
		ID: "pending", EmployeeID: "investment-advisor", SessionID: "session-a",
		Status: aeWaitingInputStatus, Result: "缺少决定性输入", UpdatedAt: now,
	}}

	ctx := withExplicitAEMentionGrant(context.Background(), "investment-advisor")
	mgr.supersedeWaitingInputForExplicitInvocation(ctx, "investment-advisor", "session-a")
	got := mgr.workLog["investment-advisor"][0]
	if got.Status != "cancelled" {
		t.Fatalf("explicit invocation did not supersede pending record: %#v", got)
	}
	for _, want := range []string{"explicit @ invocation", "step 1"} {
		if !strings.Contains(got.Result, want) {
			t.Fatalf("supersede audit result missing %q: %s", want, got.Result)
		}
	}
}
