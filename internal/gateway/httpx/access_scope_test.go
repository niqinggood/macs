package httpx

import "testing"

func TestDefaultAgentOSScopesAllowOnlyOwnedTaskFollowups(t *testing.T) {
	identity := AgentOSAccessIdentity{
		TenantID: "tenant-a", ServiceID: "crm", TenantScoped: true,
		AllowedRoles: []string{"assistant"},
	}
	owned := AgentOSTaskAccessBoundary{
		TenantID: "tenant-a", RoleID: "assistant", CallerID: "crm", DataPlane: true,
	}
	peer := owned
	peer.CallerID = "other-service"
	if !AgentOSIdentityCanFollowupTask(identity, owned) {
		t.Fatal("default service scopes did not allow an owned task follow-up")
	}
	if AgentOSIdentityCanFollowupTask(identity, peer) {
		t.Fatal("default service scopes allowed a peer task follow-up")
	}
}

func TestAgentOSFollowupAnyRemainsTenantSubjectAndRoleBounded(t *testing.T) {
	identity := AgentOSAccessIdentity{
		TenantID: "tenant-a", SubjectID: "alice", ServiceID: "operator",
		TenantScoped: true, SubjectScoped: true,
		AllowedRoles: []string{"assistant"},
		Scopes:       []string{AgentOSScopeTasksFollowupAny},
	}
	allowed := AgentOSTaskAccessBoundary{
		TenantID: "tenant-a", SubjectID: "alice", RoleID: "assistant", CallerID: "crm", DataPlane: true,
	}
	if !AgentOSIdentityCanFollowupTask(identity, allowed) {
		t.Fatal("followup:any did not allow a task inside every immutable boundary")
	}
	for name, task := range map[string]AgentOSTaskAccessBoundary{
		"tenant":  {TenantID: "tenant-b", SubjectID: "alice", RoleID: "assistant", CallerID: "crm"},
		"subject": {TenantID: "tenant-a", SubjectID: "bob", RoleID: "assistant", CallerID: "crm"},
		"role":    {TenantID: "tenant-a", SubjectID: "alice", RoleID: "admin", CallerID: "crm"},
	} {
		if AgentOSIdentityCanFollowupTask(identity, task) {
			t.Fatalf("followup:any escaped %s boundary", name)
		}
	}
}
