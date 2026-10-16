package httpx

import (
	"strings"

	"gorm.io/gorm"
)

const (
	AgentOSScopeTasksSubmit      = "tasks:submit"
	AgentOSScopeTasksReadOwn     = "tasks:read:own"
	AgentOSScopeTasksReadRole    = "tasks:read:role"
	AgentOSScopeTasksReadAny     = "tasks:read:any"
	AgentOSScopeTasksCancelOwn   = "tasks:cancel:own"
	AgentOSScopeTasksCancelAny   = "tasks:cancel:any"
	AgentOSScopeTasksFollowupOwn = "tasks:followup:own"
	AgentOSScopeTasksFollowupAny = "tasks:followup:any"
	AgentOSScopeApprovalsRead    = "approvals:read"
	AgentOSScopeApprovalsDecide  = "approvals:decide"
	AgentOSScopeUsersActAs       = "users:act_as"
)

var supportedAgentOSScopes = map[string]struct{}{
	AgentOSScopeTasksSubmit:      {},
	AgentOSScopeTasksReadOwn:     {},
	AgentOSScopeTasksReadRole:    {},
	AgentOSScopeTasksReadAny:     {},
	AgentOSScopeTasksCancelOwn:   {},
	AgentOSScopeTasksCancelAny:   {},
	AgentOSScopeTasksFollowupOwn: {},
	AgentOSScopeTasksFollowupAny: {},
	AgentOSScopeApprovalsRead:    {},
	AgentOSScopeApprovalsDecide:  {},
	AgentOSScopeUsersActAs:       {},
}

var defaultAgentOSServiceScopes = []string{
	AgentOSScopeTasksSubmit,
	AgentOSScopeTasksReadOwn,
	AgentOSScopeTasksCancelOwn,
	AgentOSScopeTasksFollowupOwn,
}

// AgentOSAccessIdentity is the authenticated portion of a host identity that
// the AgentOS data-plane authorization policy needs.
type AgentOSAccessIdentity struct {
	TenantID      string
	SubjectID     string
	ServiceID     string
	AllowedRoles  []string
	Scopes        []string
	ControlPlane  bool
	TenantScoped  bool
	SubjectScoped bool
	CanApprove    bool
}

// AgentOSTaskAccessBoundary is the immutable task metadata used for access
// decisions. Keeping this view independent from the host task model prevents
// the HTTP policy package from depending on package main.
type AgentOSTaskAccessBoundary struct {
	TenantID  string
	SubjectID string
	RoleID    string
	CallerID  string
	DataPlane bool
}

func IsSupportedAgentOSScope(scope string) bool {
	_, ok := supportedAgentOSScopes[strings.ToLower(strings.TrimSpace(scope))]
	return ok
}

func CanonicalAgentOSScopes(scopes []string) []string {
	result := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if _, supported := supportedAgentOSScopes[scope]; !supported {
			continue
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result
}

func EffectiveAgentOSServiceScopes(configured []string, canApprove bool) []string {
	scopes := CanonicalAgentOSScopes(configured)
	if len(configured) == 0 {
		scopes = append([]string(nil), defaultAgentOSServiceScopes...)
	}
	if canApprove {
		scopes = appendAgentOSScope(scopes, AgentOSScopeApprovalsRead)
		scopes = appendAgentOSScope(scopes, AgentOSScopeApprovalsDecide)
	}
	return scopes
}

func appendAgentOSScope(scopes []string, scope string) []string {
	for _, existing := range scopes {
		if existing == scope {
			return scopes
		}
	}
	return append(scopes, scope)
}

func normalizeAgentOSAccessIdentity(identity AgentOSAccessIdentity) AgentOSAccessIdentity {
	identity.TenantID = strings.TrimSpace(identity.TenantID)
	identity.SubjectID = strings.TrimSpace(identity.SubjectID)
	identity.ServiceID = strings.TrimSpace(identity.ServiceID)
	identity.Scopes = CanonicalAgentOSScopes(identity.Scopes)
	roles := make([]string, 0, len(identity.AllowedRoles))
	for _, roleID := range identity.AllowedRoles {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			roles = append(roles, roleID)
		}
	}
	identity.AllowedRoles = roles
	return identity
}

func AgentOSIdentityHasScope(identity AgentOSAccessIdentity, scope string) bool {
	identity = normalizeAgentOSAccessIdentity(identity)
	if identity.ControlPlane || !identity.TenantScoped {
		return true
	}
	for _, granted := range EffectiveAgentOSServiceScopes(identity.Scopes, identity.CanApprove) {
		if granted == scope {
			return true
		}
	}
	return false
}

func roleAllows(values []string, subject string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "*" || value == subject {
			return true
		}
	}
	return false
}

// AgentOSIdentityMatchesTaskBoundary applies the immutable host-owned tenant,
// subject, and role bounds before a task scope is considered. A broad "*:any"
// scope therefore cannot escape a service client's configured AllowedRoles.
func AgentOSIdentityMatchesTaskBoundary(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	identity = normalizeAgentOSAccessIdentity(identity)
	if identity.ControlPlane {
		return true
	}
	if !AgentOSIdentityMatchesTaskTenant(identity, task) {
		return false
	}
	if identity.SubjectScoped &&
		(identity.SubjectID == "" || identity.SubjectID != strings.TrimSpace(task.SubjectID)) {
		return false
	}
	roleID := strings.TrimSpace(task.RoleID)
	if roleID == "" {
		return len(identity.AllowedRoles) == 0
	}
	return roleAllows(identity.AllowedRoles, roleID)
}

func AgentOSIdentityMatchesTaskTenant(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	identity = normalizeAgentOSAccessIdentity(identity)
	return identity.ControlPlane || identity.TenantID == strings.TrimSpace(task.TenantID)
}

func AgentOSIdentityOwnsTask(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	callerID := strings.TrimSpace(task.CallerID)
	serviceID := normalizeAgentOSCallerID(identity.ServiceID)
	return callerID != "" && serviceID != "anonymous" && callerID == serviceID
}

func AgentOSIdentityCanReadTask(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	if !AgentOSIdentityMatchesTaskBoundary(identity, task) {
		return false
	}
	if AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadAny) {
		return true
	}
	if AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadRole) && strings.TrimSpace(task.RoleID) != "" {
		return true
	}
	return AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadOwn) &&
		AgentOSIdentityOwnsTask(identity, task)
}

func AgentOSIdentityCanCancelTask(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	if !AgentOSIdentityMatchesTaskBoundary(identity, task) {
		return false
	}
	if AgentOSIdentityHasScope(identity, AgentOSScopeTasksCancelAny) {
		return true
	}
	return AgentOSIdentityHasScope(identity, AgentOSScopeTasksCancelOwn) &&
		AgentOSIdentityOwnsTask(identity, task)
}

func AgentOSIdentityCanFollowupTask(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	if !AgentOSIdentityMatchesTaskBoundary(identity, task) {
		return false
	}
	if AgentOSIdentityHasScope(identity, AgentOSScopeTasksFollowupAny) {
		return true
	}
	return AgentOSIdentityHasScope(identity, AgentOSScopeTasksFollowupOwn) &&
		AgentOSIdentityOwnsTask(identity, task)
}

func AgentOSIdentityCanReadApproval(identity AgentOSAccessIdentity, task AgentOSTaskAccessBoundary) bool {
	return AgentOSIdentityMatchesTaskBoundary(identity, task) &&
		AgentOSIdentityHasScope(identity, AgentOSScopeApprovalsRead)
}

// ScopeAgentOSTaskListQuery applies authorization as SQL predicates so a fixed
// page limit is evaluated after isolation, not before it. Caller filters added
// by the API may only narrow these predicates.
func ScopeAgentOSTaskListQuery(query *gorm.DB, identity AgentOSAccessIdentity, defaultTenantID string) *gorm.DB {
	if query == nil {
		return query
	}
	identity = normalizeAgentOSAccessIdentity(identity)
	if identity.ControlPlane {
		return query
	}
	defaultTenantID = strings.TrimSpace(defaultTenantID)
	if identity.TenantID == defaultTenantID {
		query = query.Where("tenant_id = ? OR tenant_id = '' OR tenant_id IS NULL", identity.TenantID)
	} else {
		query = query.Where("tenant_id = ?", identity.TenantID)
	}
	if identity.SubjectScoped {
		if identity.SubjectID == "" {
			return query.Where("1 = 0")
		}
		query = query.Where("subject_id = ?", identity.SubjectID)
	}

	allowedRoles := make([]string, 0, len(identity.AllowedRoles))
	allRoles := len(identity.AllowedRoles) == 0
	for _, roleID := range identity.AllowedRoles {
		if roleID == "*" {
			allRoles = true
			allowedRoles = nil
			break
		}
		allowedRoles = append(allowedRoles, roleID)
	}
	if !allRoles {
		if len(allowedRoles) == 0 {
			return query.Where("1 = 0")
		}
		query = query.Where("role_id IN ?", allowedRoles)
	}

	switch {
	case AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadAny):
		return query
	case AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadRole):
		// AllowedRoles above remains the upper bound. With no explicit role
		// list, this scope intentionally covers every role in the tenant.
		return query.Where("role_id <> '' AND role_id IS NOT NULL")
	case AgentOSIdentityHasScope(identity, AgentOSScopeTasksReadOwn):
		return query.Where("caller_id = ?", normalizeAgentOSCallerID(identity.ServiceID))
	default:
		return query.Where("1 = 0")
	}
}

func normalizeAgentOSCallerID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "anonymous"
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
