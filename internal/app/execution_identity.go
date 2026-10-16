package app

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"mac/internal/agentos"
	authcore "mac/internal/auth"
	gatewayhttp "mac/internal/gateway/httpx"
	"mac/internal/processutil"
	"mac/internal/security"
)

const defaultExecutionTenantID = "default"

const (
	agentOSScopeTasksSubmit      = gatewayhttp.AgentOSScopeTasksSubmit
	agentOSScopeTasksReadOwn     = gatewayhttp.AgentOSScopeTasksReadOwn
	agentOSScopeTasksReadRole    = gatewayhttp.AgentOSScopeTasksReadRole
	agentOSScopeTasksReadAny     = gatewayhttp.AgentOSScopeTasksReadAny
	agentOSScopeTasksCancelOwn   = gatewayhttp.AgentOSScopeTasksCancelOwn
	agentOSScopeTasksCancelAny   = gatewayhttp.AgentOSScopeTasksCancelAny
	agentOSScopeTasksFollowupOwn = gatewayhttp.AgentOSScopeTasksFollowupOwn
	agentOSScopeTasksFollowupAny = gatewayhttp.AgentOSScopeTasksFollowupAny
	agentOSScopeApprovalsRead    = gatewayhttp.AgentOSScopeApprovalsRead
	agentOSScopeApprovalsDecide  = gatewayhttp.AgentOSScopeApprovalsDecide
	agentOSScopeUsersActAs       = gatewayhttp.AgentOSScopeUsersActAs
)

var (
	ErrSessionOwnerConflict = gatewayhttp.ErrSessionOwnerConflict
	ErrTaskOwnerConflict    = gatewayhttp.ErrTaskOwnerConflict
)

// ExecutionIdentity is the host-authenticated subject attached to an agent run.
// It is intentionally not populated from model arguments or arbitrary tenant
// headers. The unexported sealed context value below prevents a tool from
// manufacturing a more privileged identity with context.WithValue.
type ExecutionIdentity struct {
	TenantID      string   `json:"tenant_id"`
	UserID        string   `json:"user_id,omitempty"`
	SubjectID     string   `json:"subject_id,omitempty"`
	RoleID        string   `json:"role_id,omitempty"`
	AgentName     string   `json:"agent_name,omitempty"`
	Source        string   `json:"source,omitempty"`
	ServiceID     string   `json:"service_id,omitempty"`
	AllowedRoles  []string `json:"allowed_roles,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	Service       bool     `json:"service"`
	ServiceClient bool     `json:"service_client,omitempty"`
	TenantScoped  bool     `json:"tenant_scoped"`
	SubjectScoped bool     `json:"subject_scoped,omitempty"`
	RequireUserID bool     `json:"require_user_id,omitempty"`
	ControlPlane  bool     `json:"control_plane"`
	CanApprove    bool     `json:"can_approve"`
}

func isSupportedAgentOSScope(scope string) bool {
	return gatewayhttp.IsSupportedAgentOSScope(scope)
}

func canonicalAgentOSScopes(scopes []string) []string {
	return gatewayhttp.CanonicalAgentOSScopes(scopes)
}

func effectiveAgentOSServiceScopes(configured []string, canApprove bool) []string {
	return gatewayhttp.EffectiveAgentOSServiceScopes(configured, canApprove)
}

func agentOSAccessIdentity(identity ExecutionIdentity) gatewayhttp.AgentOSAccessIdentity {
	identity = normalizeExecutionIdentity(identity)
	return gatewayhttp.AgentOSAccessIdentity{
		TenantID:      identity.TenantID,
		SubjectID:     identity.SubjectID,
		ServiceID:     identity.ServiceID,
		AllowedRoles:  identity.AllowedRoles,
		Scopes:        identity.Scopes,
		ControlPlane:  identity.ControlPlane,
		TenantScoped:  identity.TenantScoped,
		SubjectScoped: identity.SubjectScoped,
		CanApprove:    identity.CanApprove,
	}
}

func executionIdentityHasAgentOSScope(identity ExecutionIdentity, scope string) bool {
	return gatewayhttp.AgentOSIdentityHasScope(agentOSAccessIdentity(identity), scope)
}

func normalizePersonalOwnerTenantID(tenantID string) string {
	return gatewayhttp.NormalizePersonalOwnerTenantID(tenantID, defaultExecutionTenantID)
}

func samePersistedPersonalOwner(
	leftTenantID, leftSubjectID, rightTenantID, rightSubjectID string,
) bool {
	return gatewayhttp.SamePersistedPersonalOwner(
		leftTenantID,
		leftSubjectID,
		rightTenantID,
		rightSubjectID,
		defaultExecutionTenantID,
	)
}

type executionIdentityGrant struct {
	Identity ExecutionIdentity
	seal     *struct{}
}

type executionIdentityContextKey struct{}

var executionIdentitySeal = &struct{}{}

func normalizeExecutionIdentity(identity ExecutionIdentity) ExecutionIdentity {
	identity.TenantID = boundedIdentityValue(identity.TenantID, 64)
	identity.UserID = boundedIdentityValue(identity.UserID, 128)
	identity.SubjectID = boundedIdentityValue(identity.SubjectID, 128)
	identity.RoleID = boundedIdentityValue(identity.RoleID, 128)
	identity.AgentName = boundedIdentityValue(identity.AgentName, 128)
	identity.Source = boundedIdentityValue(identity.Source, 64)
	identity.ServiceID = boundedIdentityValue(identity.ServiceID, 128)
	allowedRoles := make([]string, 0, len(identity.AllowedRoles))
	for _, roleID := range identity.AllowedRoles {
		roleID = boundedIdentityValue(roleID, 128)
		if roleID != "" {
			allowedRoles = append(allowedRoles, roleID)
		}
	}
	identity.AllowedRoles = allowedRoles
	identity.Scopes = canonicalAgentOSScopes(identity.Scopes)
	if identity.TenantID == "" {
		identity.TenantID = defaultExecutionTenantID
	}
	if identity.SubjectID == "" {
		identity.SubjectID = identity.UserID
	}
	return identity
}

func boundedIdentityValue(value string, limit int) string {
	return security.BoundedIdentityValue(value, limit)
}

func withExecutionIdentity(ctx context.Context, identity ExecutionIdentity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, executionIdentityContextKey{}, executionIdentityGrant{
		Identity: normalizeExecutionIdentity(identity),
		seal:     executionIdentitySeal,
	})
}

func executionIdentityFromContext(ctx context.Context) (ExecutionIdentity, bool) {
	if ctx == nil {
		return ExecutionIdentity{}, false
	}
	grant, ok := ctx.Value(executionIdentityContextKey{}).(executionIdentityGrant)
	if !ok || grant.seal != executionIdentitySeal {
		return ExecutionIdentity{}, false
	}
	return normalizeExecutionIdentity(grant.Identity), true
}

// bindExecutionAgent records the actual agent selected by the host while
// preserving the authenticated tenant, user, and original role grant.
func bindExecutionAgent(ctx context.Context, agentName string) context.Context {
	identity, ok := executionIdentityFromContext(ctx)
	if !ok {
		identity = ExecutionIdentity{
			TenantID: defaultExecutionTenantID,
			Source:   "local_agent",
		}
	}
	identity.AgentName = boundedIdentityValue(agentName, 128)
	if identity.RoleID == "" {
		identity.RoleID = identity.AgentName
	}
	return withExecutionIdentity(ctx, identity)
}

// shellResourceOwnerFromContext narrows the sealed execution identity to the
// ownership metadata required by process-scoped terminal resources.
func shellResourceOwnerFromContext(ctx context.Context) (processutil.ShellResourceOwner, error) {
	identity, ok := executionIdentityFromContext(ctx)
	if !ok || (!identity.TenantScoped && !identity.ControlPlane) {
		return processutil.ShellResourceOwner{}, nil
	}
	identity = normalizeExecutionIdentity(identity)
	serviceID := strings.TrimSpace(identity.ServiceID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(identity.UserID)
	}
	if identity.ControlPlane && serviceID == "" {
		serviceID = "openkitty-control-plane"
	}
	if serviceID == "" {
		return processutil.ShellResourceOwner{}, fmt.Errorf("tenant-scoped shell resources require a service owner")
	}
	return processutil.ShellResourceOwner{
		TenantID:  identity.TenantID,
		ServiceID: serviceID,
		Protected: true,
	}, nil
}

func shellResourceAccessible(ctx context.Context, owner processutil.ShellResourceOwner) bool {
	identity, ok := executionIdentityFromContext(ctx)
	if !owner.Protected {
		return !ok || identity.ControlPlane || !identity.TenantScoped
	}
	if !ok {
		return false
	}
	identity = normalizeExecutionIdentity(identity)
	if identity.ControlPlane {
		return true
	}
	if !identity.TenantScoped || identity.TenantID != owner.TenantID {
		return false
	}
	serviceID := strings.TrimSpace(identity.ServiceID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(identity.UserID)
	}
	return serviceID != "" && serviceID == owner.ServiceID
}

func secureTokenEqual(left, right string) bool {
	if left == "" || right == "" || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func requestBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) >= 7 && strings.EqualFold(value[:7], "Bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return ""
}

// authenticateExecutionRequest turns transport credentials into a sealed
// execution identity. X-Tenant-ID and body tenant fields are deliberately
// ignored; only a tenant session/token or a configured control-plane key can
// establish tenant authority.
func (g *GatewayService) authenticateExecutionRequest(r *http.Request, source string) (context.Context, error) {
	if r == nil {
		return nil, fmt.Errorf("request is required")
	}
	callerID := agentos.NormalizeCallerID(r.Header.Get("X-Caller-ID"))
	base := ExecutionIdentity{
		TenantID:  defaultExecutionTenantID,
		UserID:    callerID,
		SubjectID: callerID,
		Source:    source,
		ServiceID: callerID,
		Service:   true,
	}

	if sessionToken := strings.TrimSpace(r.Header.Get("X-Auth-Token")); sessionToken != "" {
		if session := authcore.GetTenantSession(sessionToken); session != nil {
			base.TenantID = session.TenantID
			base.UserID = session.Username
			base.SubjectID = session.SubjectID
			if strings.TrimSpace(base.SubjectID) == "" {
				base.SubjectID = session.Username
			}
			base.ServiceID = "user:" + session.Username
			base.Scopes = effectiveAgentOSServiceScopes(nil, false)
			base.TenantScoped = true
			base.SubjectScoped = true
			return withExecutionIdentity(r.Context(), base), nil
		}
	}

	token := requestBearerToken(r)
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-API-Key"))
	}
	if token != "" && g != nil && g.config != nil {
		for _, client := range g.config.ServiceClients {
			if !client.Enabled || !secureTokenEqual(token, strings.TrimSpace(client.Token)) {
				continue
			}
			base.TenantID = client.TenantID
			base.UserID = "service:" + client.ID
			base.SubjectID = base.UserID
			base.ServiceID = client.ID
			base.ServiceClient = true
			base.AllowedRoles = append([]string(nil), client.AllowedRoles...)
			base.CanApprove = client.CanApprove
			base.RequireUserID = client.RequireUserID
			base.Scopes = effectiveAgentOSServiceScopes(client.Scopes, client.CanApprove)
			base.TenantScoped = true
			return withExecutionIdentity(r.Context(), base), nil
		}
	}
	if token != "" && g != nil && g.tenantMgr != nil {
		if tenant := g.tenantMgr.GetByToken(token); tenant != nil {
			base.TenantID = tenant.ID
			base.UserID = "tenant:" + tenant.ID
			base.SubjectID = base.UserID
			base.ServiceID = "tenant:" + tenant.ID
			// A tenant token is an administrative credential for its tenant.
			// It remains tenant/role bounded but may operate every task there.
			base.Scopes = []string{
				agentOSScopeTasksSubmit,
				agentOSScopeTasksReadAny,
				agentOSScopeTasksCancelAny,
				agentOSScopeTasksFollowupAny,
				agentOSScopeApprovalsRead,
			}
			base.TenantScoped = true
			return withExecutionIdentity(r.Context(), base), nil
		}
	}

	if g != nil && g.config != nil {
		if secureTokenEqual(token, strings.TrimSpace(g.config.Gateway.APIKey)) ||
			secureTokenEqual(token, strings.TrimSpace(g.config.Gateway.AuthToken)) {
			base.ControlPlane = true
			base.ServiceID = "openkitty-control-plane"
			return withExecutionIdentity(r.Context(), base), nil
		}
	}

	authRequired := false
	if g != nil {
		if g.config != nil {
			authRequired = g.config.Gateway.AuthEnabled ||
				strings.TrimSpace(g.config.Gateway.APIKey) != "" ||
				strings.TrimSpace(g.config.Gateway.AuthToken) != "" ||
				len(g.config.ServiceClients) > 0
		}
		if g.tenantMgr != nil && g.tenantMgr.Enabled() {
			authRequired = true
		}
	}
	if authRequired {
		return nil, fmt.Errorf("valid service or tenant credentials are required")
	}
	// Legacy loopback/no-auth mode has no separate operator credential. It
	// already permits task execution, so preserve local approval usability.
	base.CanApprove = true
	return withExecutionIdentity(r.Context(), base), nil
}
