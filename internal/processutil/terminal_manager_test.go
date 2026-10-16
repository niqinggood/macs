package processutil

import (
	"context"
	"testing"
	"time"
)

type testShellIdentity struct {
	tenantID     string
	serviceID    string
	controlPlane bool
}

type testShellIdentityKey struct{}

func withTestShellIdentity(ctx context.Context, identity testShellIdentity) context.Context {
	return context.WithValue(ctx, testShellIdentityKey{}, identity)
}

func testShellAccess(ctx context.Context, owner ShellResourceOwner) bool {
	identity, ok := ctx.Value(testShellIdentityKey{}).(testShellIdentity)
	if !owner.Protected {
		return !ok || identity.controlPlane
	}
	return ok && (identity.controlPlane || (identity.tenantID == owner.TenantID && identity.serviceID == owner.ServiceID))
}

func TestTerminalManagerListSessionsSnapshot(t *testing.T) {
	mgr := &TerminalManager{sessions: map[string]*PersistentShell{}}
	now := time.Now()
	mgr.sessions["task1"] = &PersistentShell{
		id:         "task1",
		shellType:  ShellTypeBash,
		workDir:    "/tmp/project",
		lastActive: now,
		healthy:    true,
	}

	sessions := mgr.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.TaskID != "task1" || got.Type != ShellTypeBash || got.WorkDir != "/tmp/project" || !got.Healthy {
		t.Fatalf("unexpected session info: %#v", got)
	}
	if !got.LastActive.Equal(now) {
		t.Fatalf("unexpected last active: %v", got.LastActive)
	}
}

func TestTerminalManagerListSessionsIsolatesTenantServiceOwners(t *testing.T) {
	now := time.Now()
	mgr := &TerminalManager{
		sessions: map[string]*PersistentShell{
			"legacy": {
				id: "legacy", shellType: ShellTypeBash, workDir: "/legacy",
				lastActive: now, healthy: true,
			},
			"owned": {
				id: "owned", shellType: ShellTypeBash, workDir: "/private",
				lastActive: now, healthy: true,
			},
		},
		owners: map[string]ShellResourceOwner{
			"owned": {TenantID: "tenant-a", ServiceID: "owner", Protected: true},
		},
		canAccess: testShellAccess,
	}
	ownerCtx := withTestShellIdentity(context.Background(), testShellIdentity{
		tenantID: "tenant-a", serviceID: "owner",
	})
	peerCtx := withTestShellIdentity(context.Background(), testShellIdentity{
		tenantID: "tenant-a", serviceID: "peer",
	})
	controlCtx := withTestShellIdentity(context.Background(), testShellIdentity{controlPlane: true})

	if got := mgr.ListSessions(); len(got) != 1 || got[0].TaskID != "legacy" {
		t.Fatalf("legacy session list leaked protected session: %+v", got)
	}
	if got := mgr.ListSessionsForContext(ownerCtx); len(got) != 1 || got[0].TaskID != "owned" {
		t.Fatalf("owner session list mismatch: %+v", got)
	}
	if got := mgr.ListSessionsForContext(peerCtx); len(got) != 0 {
		t.Fatalf("peer session list leaked protected session: %+v", got)
	}
	if got := mgr.ListSessionsForContext(controlCtx); len(got) != 2 {
		t.Fatalf("control plane should see all sessions: %+v", got)
	}
}
