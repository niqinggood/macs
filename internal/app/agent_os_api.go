package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"mac/internal/agentloop"
	"mac/internal/agentos"
	"mac/internal/agentresult"
	agentcore "mac/internal/agents"
	approvalcore "mac/internal/approval"
	attachmentcore "mac/internal/attachments"
	gatewayhttp "mac/internal/gateway/httpx"
	gatewayrisk "mac/internal/gateway/risk"
	"mac/internal/globalstate"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
	"mac/internal/security"
	skilldomain "mac/internal/skills"
	tenantcore "mac/internal/tenant"
	"mac/internal/textutil"
	developmenttools "mac/internal/tools/development"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const maxAgentOSSSEPerIdentity = 16

var agentOSRejectedAuthRateLimiter = gatewayhttp.NewRateLimiter(30, 10)

func (g *GatewayService) agentOSRateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, err := g.authenticateExecutionRequest(r, "agent_os")
		if err != nil {
			peer := strings.TrimSpace(r.RemoteAddr)
			if host, _, splitErr := net.SplitHostPort(peer); splitErr == nil {
				peer = host
			}
			peer = textutil.TruncateUTF8Bytes(peer, 128)
			if peer == "" {
				peer = "unknown"
			}
			if !agentOSRejectedAuthRateLimiter.Allow(peer) {
				w.Header().Set("Retry-After", "1")
				agentos.SendJSONError(w, r, "rate_limited", "authentication request rate limit exceeded", http.StatusTooManyRequests, true)
				return
			}
			next(w, r)
			return
		}
		identity, ok := executionIdentityFromContext(ctx)
		if !ok {
			next(w, r)
			return
		}
		identity = normalizeExecutionIdentity(identity)
		key := identity.TenantID + "\x00" + identity.ServiceID
		if !apiRateLimiter.Allow(key) {
			w.Header().Set("Retry-After", "1")
			agentos.SendJSONError(w, r, "rate_limited", "request rate limit exceeded", http.StatusTooManyRequests, true)
			return
		}
		next(w, r.WithContext(ctx))
	}
}

func (g *GatewayService) acquireAgentOSSSEConnection(identity ExecutionIdentity) (func(), bool) {
	if g == nil {
		return func() {}, false
	}
	identity = normalizeExecutionIdentity(identity)
	key := identity.TenantID + "\x00" + identity.ServiceID
	g.agentOSSSEConnectionsMu.Lock()
	if g.agentOSSSEConnections == nil {
		g.agentOSSSEConnections = make(map[string]int)
	}
	if g.agentOSSSEConnections[key] >= maxAgentOSSSEPerIdentity {
		g.agentOSSSEConnectionsMu.Unlock()
		return func() {}, false
	}
	g.agentOSSSEConnections[key]++
	g.agentOSSSEConnectionsMu.Unlock()

	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		g.agentOSSSEConnectionsMu.Lock()
		if remaining := g.agentOSSSEConnections[key] - 1; remaining > 0 {
			g.agentOSSSEConnections[key] = remaining
		} else {
			delete(g.agentOSSSEConnections, key)
		}
		g.agentOSSSEConnectionsMu.Unlock()
	}, true
}

func (g *GatewayService) withAgentOSRuntime(ctx context.Context) context.Context {
	ctx = agentos.WithTaskRuntimeObserver(ctx, g)
	if g != nil && g.db != nil {
		ctx = agentos.WithToolExecutionLedger(ctx, agentos.NewGormToolExecutionLedger(g.db))
	}
	return ctx
}

func agentOSRequestSecurityOptions() gatewayrisk.AgentOSRequestSecurityOptions {
	return gatewayrisk.AgentOSRequestSecurityOptions{
		Detector:                globalstate.PromptInjectionDetector(),
		PathCandidates:          sandboxTextPaths,
		UsesForeignAbsolutePath: tenantPathUsesForeignAbsoluteSyntax,
	}
}

func (g *GatewayService) assessAndLogAgentOSRequestSecurity(input string) gatewayrisk.AgentOSRequestSecurityDecision {
	var logger gatewayrisk.AdvisoryLogger
	if g != nil {
		logger = g.logger
	}
	return gatewayrisk.AssessAndLogAgentOSRequestSecurity(logger, input, agentOSRequestSecurityOptions())
}

func agentOSAuthenticationConfigured(config *Config) bool {
	if config == nil {
		return false
	}
	return config.Gateway.AuthEnabled ||
		strings.TrimSpace(config.Gateway.APIKey) != "" ||
		strings.TrimSpace(config.Gateway.AuthToken) != "" ||
		len(config.ServiceClients) > 0 ||
		len(config.Tenants) > 0
}

// agentOSDataHandler binds host handlers and middleware to the reusable data
// plane route table owned by internal/gateway/httpx.
func (g *GatewayService) agentOSDataHandler() http.Handler {
	wrap := func(handler http.HandlerFunc) http.Handler {
		return agentos.RequestMiddleware(agentos.CORSHandler(g.agentOSRateLimitMiddleware(handler)))
	}
	notFound := wrap(func(w http.ResponseWriter, r *http.Request) {
		agentos.SendJSONError(w, r, "not_found", "endpoint not found", http.StatusNotFound, false)
	})
	return gatewayhttp.NewAgentOSDataPlaneHandler(gatewayhttp.AgentOSDataPlaneHandlers{
		Roles:           wrap(g.handleAgentOSRoles),
		Tasks:           wrap(g.handleAgentOSTasks),
		TaskByID:        wrap(g.handleAgentOSTaskByID),
		Files:           wrap(g.handleAgentOSFiles),
		Models:          wrap(g.handleOpenAICompatModels),
		ChatCompletions: wrap(g.handleOpenAICompatChatCompletions),
		Live:            http.HandlerFunc(g.handleLivez),
		Ready:           http.HandlerFunc(g.handleReadyz),
		Metrics:         http.HandlerFunc(g.handleAgentOSMetrics),
		APINotFound:     notFound,
	})
}

// mountAgentOSDataPlaneOnMainMux exposes both the native AgentOS routes and
// the OpenAI-compatible facade when no dedicated data-plane listener is set.
// Registering only the native prefix makes /v1/models and
// /v1/chat/completions unreachable even though the inner handler owns them.
func mountAgentOSDataPlaneOnMainMux(mux *http.ServeMux, handler http.Handler) {
	if mux == nil || handler == nil {
		return
	}
	mux.Handle("/api/v1/agent", handler)
	mux.Handle("/api/v1/agent/", handler)
	mux.Handle("/v1/", handler)
}

func (g *GatewayService) startDedicatedAgentOSDataPlane(handler http.Handler) error {
	if g == nil || g.config == nil {
		return nil
	}
	server, err := gatewayhttp.StartDedicatedAgentOSDataPlane(
		g.config.Gateway.AgentOSListenAddr,
		handler,
		g.logger,
	)
	if err != nil {
		return err
	}
	g.agentOSHTTPServer = server
	return nil
}

func (g *GatewayService) markAgentOSTaskID(taskID string) {
	if g != nil {
		g.agentOSBoundary.MarkTaskID(taskID)
	}
}

func (g *GatewayService) unmarkAgentOSTaskID(taskID string) {
	if g != nil {
		g.agentOSBoundary.UnmarkTaskID(taskID)
	}
}

func (g *GatewayService) isAgentOSTaskID(taskID string) bool {
	if g == nil {
		return false
	}
	durableLookup := func(taskID string) (string, bool, error) {
		if g.db == nil {
			return "", false, nil
		}
		var record GatewayTaskRecord
		err := g.db.Select("task_id", "queue_name").Where("task_id = ?", taskID).First(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return record.QueueName, true, nil
	}
	memoryLookup := func(taskID string) (string, bool, error) {
		g.tasksMu.RLock()
		task := g.tasks[taskID]
		g.tasksMu.RUnlock()
		if task == nil {
			return "", false, nil
		}
		return task.QueueName, true, nil
	}
	return g.agentOSBoundary.IsTaskID(taskID, agentOSQueueName, durableLookup, memoryLookup, g.logger)
}

func (g *GatewayService) isAgentOSSessionID(sessionID string) bool {
	if g == nil {
		return false
	}
	memoryLookup := func(sessionID string) bool {
		g.tasksMu.RLock()
		defer g.tasksMu.RUnlock()
		for _, task := range g.tasks {
			if task != nil && task.QueueName == agentOSQueueName &&
				task.Request != nil && strings.TrimSpace(task.Request.SessionId) == sessionID {
				return true
			}
		}
		return false
	}
	durableLookup := func(sessionID string) (bool, error) {
		if g.db == nil {
			return false, nil
		}
		var count int64
		err := g.db.Model(&GatewayTaskRecord{}).
			Where("queue_name = ? AND session_id = ?", agentOSQueueName, sessionID).
			Limit(1).
			Count(&count).Error
		return count > 0, err
	}
	return g.agentOSBoundary.IsSessionID(sessionID, memoryLookup, durableLookup, g.logger)
}

func (g *GatewayService) rejectLegacyAgentOSSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if !g.isAgentOSSessionID(sessionID) {
		return false
	}
	http.NotFound(w, r)
	return true
}

func (g *GatewayService) sseTargetsForTask(taskID string) []string {
	return g.agentOSBoundary.SSETargets(taskID, g.isAgentOSTaskID)
}

func (g *GatewayService) broadcastAgentOSTaskEvent(taskID, eventType string, data interface{}) {
	if g == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(eventType) == "" {
		return
	}
	event := g.recordAgentOSEvent(Event{
		Type: strings.TrimSpace(eventType), TaskID: strings.TrimSpace(taskID), Data: data,
	})
	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()
	for _, key := range g.sseTargetsForTask(taskID) {
		for _, channel := range g.sseClients[key] {
			select {
			case channel <- event:
			default:
			}
		}
	}
}

type agentOSTaskSubmitRequest struct {
	Message           string                                `json:"message"`
	SessionID         string                                `json:"session_id,omitempty"`
	RoleID            string                                `json:"role_id,omitempty"`
	RoleRevision      string                                `json:"role_revision,omitempty"`
	AgentName         string                                `json:"agent_name,omitempty"`
	UserID            string                                `json:"user_id,omitempty"`
	CallerID          string                                `json:"caller_id,omitempty"`
	IdempotencyKey    string                                `json:"idempotency_key,omitempty"`
	Attachments       []attachmentcore.File                 `json:"attachments,omitempty"`
	Parameters        interface{}                           `json:"parameters,omitempty"`
	Collaboration     *CollaborationConfig                  `json:"collaboration,omitempty"`
	SkillRun          *SkillRunRequest                      `json:"skill_run,omitempty"`
	InitialMessages   []gatewayhttp.AgentOSInitialMessage   `json:"initial_messages,omitempty"`
	AssistantIdentity *gatewayhttp.AgentOSAssistantIdentity `json:"assistant_identity,omitempty"`
}

const agentOSUserIDHeader = "X-OpenKitty-User-ID"

type agentOSRequestIdentityError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (err *agentOSRequestIdentityError) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

type agentOSIdempotencyFingerprint struct {
	RoleID            string                                `json:"role_id"`
	RoleRevision      string                                `json:"role_revision"`
	Message           string                                `json:"message"`
	SessionID         string                                `json:"session_id,omitempty"`
	UserID            string                                `json:"user_id,omitempty"`
	SubjectID         string                                `json:"subject_id,omitempty"`
	Attachments       []attachmentcore.File                 `json:"attachments,omitempty"`
	Parameters        interface{}                           `json:"parameters,omitempty"`
	Collaboration     *CollaborationConfig                  `json:"collaboration,omitempty"`
	SkillRun          *SkillRunRequest                      `json:"skill_run,omitempty"`
	InitialMessages   []gatewayhttp.AgentOSInitialMessage   `json:"initial_messages,omitempty"`
	AssistantIdentity *gatewayhttp.AgentOSAssistantIdentity `json:"assistant_identity,omitempty"`
}

func agentOSRequestFingerprint(
	submitted agentOSTaskSubmitRequest,
	roleID, requestedRoleRevision, effectiveUserID, effectiveSubjectID string,
) (string, error) {
	sessionID := strings.NewReplacer("..", "", "/", "_", "\\", "_").
		Replace(strings.TrimSpace(submitted.SessionID))
	payload, err := json.Marshal(agentOSIdempotencyFingerprint{
		RoleID:            strings.TrimSpace(roleID),
		RoleRevision:      strings.TrimSpace(requestedRoleRevision),
		Message:           strings.TrimSpace(submitted.Message),
		SessionID:         sessionID,
		UserID:            strings.TrimSpace(effectiveUserID),
		SubjectID:         strings.TrimSpace(effectiveSubjectID),
		Attachments:       submitted.Attachments,
		Parameters:        submitted.Parameters,
		Collaboration:     submitted.Collaboration,
		SkillRun:          cloneSkillRunRequest(submitted.SkillRun),
		InitialMessages:   submitted.InitialMessages,
		AssistantIdentity: submitted.AssistantIdentity,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func isAgentOSServiceClientIdentity(identity ExecutionIdentity) bool {
	identity = normalizeExecutionIdentity(identity)
	return identity.ServiceClient ||
		identity.TenantScoped &&
			identity.ServiceID != "" &&
			identity.UserID == "service:"+identity.ServiceID
}

func bindAgentOSRequestSubject(
	identity ExecutionIdentity,
	r *http.Request,
) (ExecutionIdentity, *agentOSRequestIdentityError) {
	identity = normalizeExecutionIdentity(identity)
	headerValues := []string(nil)
	if r != nil {
		headerValues = r.Header.Values(agentOSUserIDHeader)
	}
	if len(headerValues) > 1 {
		return identity, &agentOSRequestIdentityError{
			Code:       "invalid_user_id",
			Message:    agentOSUserIDHeader + " must be provided at most once",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	rawSubjectID := ""
	if len(headerValues) == 1 {
		rawSubjectID = strings.TrimSpace(headerValues[0])
	}
	if rawSubjectID == "" {
		if isAgentOSServiceClientIdentity(identity) &&
			identity.RequireUserID &&
			agentos.RequestUsesPersonalData(r) {
			return identity, &agentOSRequestIdentityError{
				Code:       "user_id_required",
				Message:    agentOSUserIDHeader + " is required for this service client",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		return identity, nil
	}

	subjectID, err := security.ValidatePersonalSubjectID(rawSubjectID)
	if err != nil || subjectID == "" {
		return identity, &agentOSRequestIdentityError{
			Code:       "invalid_user_id",
			Message:    agentOSUserIDHeader + " must be a valid user identifier",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if isAgentOSServiceClientIdentity(identity) {
		if !executionIdentityHasAgentOSScope(identity, agentOSScopeUsersActAs) {
			return identity, &agentOSRequestIdentityError{
				Code:       "user_act_as_forbidden",
				Message:    "users:act_as permission is required",
				HTTPStatus: http.StatusForbidden,
			}
		}
		identity.UserID = subjectID
		identity.SubjectID = subjectID
		identity.SubjectScoped = true
		return normalizeExecutionIdentity(identity), nil
	}
	if identity.SubjectScoped && identity.SubjectID == subjectID {
		return identity, nil
	}
	return identity, &agentOSRequestIdentityError{
		Code:       "user_id_mismatch",
		Message:    agentOSUserIDHeader + " cannot override the authenticated subject",
		HTTPStatus: http.StatusForbidden,
	}
}

func (g *GatewayService) resolveAgentOSRequestIdentity(
	r *http.Request,
) (context.Context, ExecutionIdentity, *agentOSRequestIdentityError) {
	ctx, err := g.authenticateExecutionRequest(r, "agent_os")
	if err != nil {
		return nil, ExecutionIdentity{}, &agentOSRequestIdentityError{
			Code:       "unauthorized",
			Message:    "unauthorized",
			HTTPStatus: http.StatusUnauthorized,
		}
	}
	identity, ok := executionIdentityFromContext(ctx)
	if !ok {
		return nil, ExecutionIdentity{}, &agentOSRequestIdentityError{
			Code:       "identity_unavailable",
			Message:    "execution identity unavailable",
			HTTPStatus: http.StatusInternalServerError,
		}
	}
	identity, identityErr := bindAgentOSRequestSubject(identity, r)
	if identityErr != nil {
		return nil, ExecutionIdentity{}, identityErr
	}
	return withExecutionIdentity(ctx, identity), identity, nil
}

func (g *GatewayService) authenticateAgentOSRequest(w http.ResponseWriter, r *http.Request) (ExecutionIdentity, bool) {
	if identity, ok := executionIdentityFromContext(r.Context()); ok {
		identity, identityErr := bindAgentOSRequestSubject(identity, r)
		if identityErr != nil {
			agentos.SendJSONError(
				w, r, identityErr.Code, identityErr.Message,
				identityErr.HTTPStatus, false,
			)
			return ExecutionIdentity{}, false
		}
		*r = *r.WithContext(withExecutionIdentity(r.Context(), identity))
		return identity, true
	}
	ctx, identity, identityErr := g.resolveAgentOSRequestIdentity(r)
	if identityErr != nil {
		agentos.SendJSONError(
			w, r, identityErr.Code, identityErr.Message,
			identityErr.HTTPStatus, false,
		)
		return ExecutionIdentity{}, false
	}
	*r = *r.WithContext(ctx)
	return identity, true
}

func (g *GatewayService) resolveAgentOSRole(requested, callerID string, identity ExecutionIdentity) (string, string, error) {
	if g == nil || g.engine == nil {
		return "", "", fmt.Errorf("agent engine is unavailable")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = g.engine.GetDefaultAgentName()
	}
	if requested == "" {
		return "", "", fmt.Errorf("no default agent role is configured")
	}
	agents := g.engine.getAllAgents()
	if g.config != nil && len(g.config.ServiceRoles) > 0 {
		for _, role := range g.config.ServiceRoles {
			roleID := strings.TrimSpace(role.ID)
			agentName := strings.TrimSpace(role.AgentName)
			if roleID != requested && agentName != requested {
				continue
			}
			if !role.Enabled {
				return "", "", fmt.Errorf("agent role %q is disabled", roleID)
			}
			if !agentos.ServiceRoleAllows(identity.AllowedRoles, roleID) {
				return "", "", fmt.Errorf("service identity is not authorized for agent role %q", roleID)
			}
			if !agentos.ServiceRoleAllows(role.AllowedTenants, identity.TenantID) {
				return "", "", fmt.Errorf("tenant is not authorized for agent role %q", roleID)
			}
			if !agentos.ServiceRoleAllows(role.AllowedCallers, callerID) {
				return "", "", fmt.Errorf("caller is not authorized for agent role %q", roleID)
			}
			if _, ok := agents[agentName]; !ok {
				return "", "", fmt.Errorf("published agent role %q references missing agent %q", roleID, agentName)
			}
			return roleID, agentName, nil
		}
		return "", "", fmt.Errorf("agent role %q is not published", requested)
	}
	if _, ok := agents[requested]; !ok {
		return "", "", fmt.Errorf("agent role %q is not published", requested)
	}
	if !agentos.ServiceRoleAllows(identity.AllowedRoles, requested) {
		return "", "", fmt.Errorf("service identity is not authorized for agent role %q", requested)
	}
	return requested, requested, nil
}

func normalizedAgentOSTaskTenant(task *GatewayTask) string {
	if task == nil {
		return ""
	}
	tenantID := strings.TrimSpace(task.TenantID)
	if tenantID == "" && task.Request != nil {
		tenantID = strings.TrimSpace(task.Request.TenantID)
	}
	if tenantID == "" {
		tenantID = defaultExecutionTenantID
	}
	return tenantID
}

func normalizedAgentOSTaskRole(task *GatewayTask) string {
	if task == nil {
		return ""
	}
	roleID := strings.TrimSpace(task.RoleID)
	if roleID == "" && task.Request != nil {
		roleID = strings.TrimSpace(task.Request.RoleID)
		if roleID == "" {
			roleID = strings.TrimSpace(task.Request.AgentName)
		}
	}
	return roleID
}

func normalizedAgentOSTaskCaller(task *GatewayTask) string {
	if task == nil {
		return ""
	}
	callerID := strings.TrimSpace(task.CallerID)
	if callerID == "" {
		return ""
	}
	return agentos.NormalizeCallerID(callerID)
}

func normalizedAgentOSTaskSubject(task *GatewayTask) string {
	if task == nil {
		return ""
	}
	subjectID := strings.TrimSpace(task.SubjectID)
	if subjectID == "" && task.Request != nil {
		subjectID = strings.TrimSpace(task.Request.SubjectID)
	}
	return subjectID
}

func agentOSTaskAccessBoundary(task *GatewayTask) gatewayhttp.AgentOSTaskAccessBoundary {
	if task == nil {
		return gatewayhttp.AgentOSTaskAccessBoundary{}
	}
	return gatewayhttp.AgentOSTaskAccessBoundary{
		TenantID:  normalizedAgentOSTaskTenant(task),
		SubjectID: normalizedAgentOSTaskSubject(task),
		RoleID:    normalizedAgentOSTaskRole(task),
		CallerID:  normalizedAgentOSTaskCaller(task),
		DataPlane: strings.TrimSpace(task.QueueName) == agentOSQueueName,
	}
}

func isAgentOSDataPlaneTask(task *GatewayTask) bool {
	return task != nil && agentOSTaskAccessBoundary(task).DataPlane
}

func executionIdentityMatchesTaskBoundary(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityMatchesTaskBoundary(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityMatchesTaskTenant(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityMatchesTaskTenant(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityOwnsTask(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityOwnsTask(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityCanReadTask(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityCanReadTask(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityCanCancelTask(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityCanCancelTask(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityCanFollowupTask(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityCanFollowupTask(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func executionIdentityCanReadApproval(identity ExecutionIdentity, task *GatewayTask) bool {
	return task != nil && gatewayhttp.AgentOSIdentityCanReadApproval(
		agentOSAccessIdentity(identity),
		agentOSTaskAccessBoundary(task),
	)
}

func scopeAgentOSTaskListQuery(query *gorm.DB, identity ExecutionIdentity) *gorm.DB {
	return gatewayhttp.ScopeAgentOSTaskListQuery(
		query,
		agentOSAccessIdentity(identity),
		defaultExecutionTenantID,
	)
}

// gatewayTaskPersonalOwner returns an authenticated personal owner only for a
// task whose trusted runtime metadata says it is scoped. A subject supplied in
// a legacy JSON request cannot create an authenticated owner.
func gatewayTaskPersonalOwner(task *GatewayTask) (tenantID, subjectID string, scoped bool) {
	if task == nil || (!task.TenantScoped && task.QueueName != agentOSQueueName) {
		return "", "", false
	}
	tenantID = normalizePersonalOwnerTenantID(task.TenantID)
	subjectID = strings.TrimSpace(task.SubjectID)
	if task.Request != nil {
		if strings.TrimSpace(task.TenantID) == "" {
			tenantID = normalizePersonalOwnerTenantID(task.Request.TenantID)
		}
		if subjectID == "" {
			subjectID = strings.TrimSpace(task.Request.SubjectID)
		}
	}
	return tenantID, subjectID, true
}

func scopeChatHistoryForTask(query *gorm.DB, task *GatewayTask) *gorm.DB {
	tenantID, subjectID, scoped := gatewayTaskPersonalOwner(task)
	return gatewayhttp.ScopeChatHistoryForOwner(
		query,
		gatewayhttp.AgentOSPersonalOwner{
			TenantID:  tenantID,
			SubjectID: subjectID,
			Scoped:    scoped,
		},
		defaultExecutionTenantID,
	)
}

func scopeRowsToPersistedOwner(query *gorm.DB, tenantID, subjectID string) *gorm.DB {
	return gatewayhttp.ScopeRowsToPersistedOwner(
		query,
		tenantID,
		subjectID,
		defaultExecutionTenantID,
	)
}

func LoadTaskFromDBOwned(db *gorm.DB, taskID, tenantID, subjectID string) (*GatewayTaskRecord, error) {
	var record GatewayTaskRecord
	err := gatewayhttp.LoadOwnedRecord(
		db,
		&record,
		taskID,
		tenantID,
		subjectID,
		defaultExecutionTenantID,
	)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func executionIdentityCanAccessTask(identity ExecutionIdentity, task *GatewayTask) bool {
	return executionIdentityCanReadTask(identity, task)
}

func (g *GatewayService) agentOSTaskResponse(task *GatewayTask, idempotentReplay bool) map[string]interface{} {
	phaseCode := strings.TrimSpace(task.CurrentPhase)
	phaseLabel := g.agentOSTaskPhaseLabel(phaseCode)
	response := map[string]interface{}{
		"task_id":           task.ID,
		"tenant_id":         normalizedAgentOSTaskTenant(task),
		"role_id":           task.RoleID,
		"role_revision":     task.RoleRevision,
		"status":            task.Status,
		"phase":             phaseLabel,
		"phase_code":        phaseCode,
		"phase_label":       phaseLabel,
		"run_attempt":       task.RunAttempt,
		"last_tool_call_id": task.LastToolCallID,
		"heartbeat_at":      task.HeartbeatAt,
		"created_at":        task.CreatedAt,
		"completed_at":      task.CompletedAt,
		"idempotent_replay": idempotentReplay,
		"links": map[string]string{
			"self":      "/api/v1/agent/tasks/" + url.PathEscape(task.ID),
			"events":    "/api/v1/agent/tasks/" + url.PathEscape(task.ID) + "/events",
			"cancel":    "/api/v1/agent/tasks/" + url.PathEscape(task.ID) + "/cancel",
			"followups": "/api/v1/agent/tasks/" + url.PathEscape(task.ID) + "/followups",
			"approvals": "/api/v1/agent/tasks/" + url.PathEscape(task.ID) + "/approvals",
		},
	}
	if task.QueueName != "" {
		response["queue"] = task.QueueName
		response["worker_id"] = task.QueueOwner
		response["lease_until"] = task.LeaseUntil
		response["available_at"] = task.AvailableAt
	}
	if task.Request != nil {
		response["session_id"] = task.Request.SessionId
		response["agent_name"] = task.Request.AgentName
		if task.Request.SkillRun != nil {
			response["skill_run_request"] = task.Request.SkillRun
		}
		if userID := strings.TrimSpace(task.Request.UserId); userID != "" {
			response["user_id"] = userID
		}
		subjectID := strings.TrimSpace(task.SubjectID)
		if subjectID == "" {
			subjectID = strings.TrimSpace(task.Request.SubjectID)
		}
		if subjectID == "" {
			subjectID = strings.TrimSpace(task.Request.UserId)
		}
		if subjectID != "" {
			response["subject_id"] = subjectID
		}
		if strings.TrimSpace(fmt.Sprint(response["role_id"])) == "" {
			response["role_id"] = task.Request.RoleID
		}
	}
	if receipt := g.skillRunReceiptForTask(task.ID); receipt != nil {
		response["skill_run"] = receipt
	}
	if task.Result != nil {
		response["result"] = agentos.PublicResponseValue(task.Result)
	}
	if artifacts := g.agentOSTaskArtifacts(task); len(artifacts) > 0 {
		response["artifacts"] = artifacts
	}
	if task.Error != nil {
		response["error"] = task.Error.Error()
	}
	if task.HeartbeatAt != nil && !isTerminalGatewayTaskStatus(task.Status) {
		response["heartbeat_age_ms"] = time.Since(*task.HeartbeatAt).Milliseconds()
	}
	return response
}

func (g *GatewayService) agentOSTaskPhaseLabel(phase string) string {
	phase = strings.TrimSpace(phase)
	if stepName := agentOSBusinessStepFromPhase(phase); stepName != "" {
		return stepName
	}
	const prefix = "ai_employee:"
	if strings.HasPrefix(phase, prefix) {
		employeeID := strings.TrimSpace(strings.TrimPrefix(phase, prefix))
		if g != nil && g.aeManager != nil {
			if cfg, ok := g.aeManager.GetEmployee(employeeID); ok {
				if name := strings.TrimSpace(cfg.Name); name != "" {
					return name
				}
			}
		}
		return "AI员工"
	}
	if strings.HasPrefix(phase, "tool:") {
		return "正在查询资料"
	}
	switch phase {
	case "queued", "pending":
		return "等待执行"
	case "claimed", "starting":
		return "准备执行"
	case "reasoning", "model":
		return "正在分析"
	case "collaboration":
		return "正在协调"
	case "waiting_approval":
		return "等待确认"
	case "completed":
		return "已完成"
	case "degraded":
		return "已完成，部分资料暂缺"
	case "failed":
		return "执行失败"
	case "stopped":
		return "已停止"
	}
	return phase
}

func (g *GatewayService) agentOSTaskResponseForIdentity(
	task *GatewayTask,
	idempotentReplay bool,
	identity ExecutionIdentity,
) map[string]interface{} {
	response := g.agentOSTaskResponse(task, idempotentReplay)
	if task.Status == "waiting_approval" && g != nil && g.db != nil &&
		executionIdentityCanReadApproval(identity, task) {
		var pending approvalcore.ApprovalRequestRecord
		if err := g.db.Where("task_id = ? AND status = ?", task.ID, approvalcore.StatusPending).
			Order("requested_at asc").
			First(&pending).Error; err == nil {
			response["pending_approval"] = pending
		}
	}
	return response
}

func (g *GatewayService) findAgentOSIdempotentTask(keyHash string) (*GatewayTask, string, bool) {
	if g == nil || g.db == nil || strings.TrimSpace(keyHash) == "" {
		return nil, "", false
	}
	var request agentos.AgentTaskRequestRecord
	if err := g.db.Where("key_hash = ?", keyHash).First(&request).Error; err != nil {
		return nil, "", false
	}
	for attempt := 0; attempt < 5; attempt++ {
		if task, ok := g.gatewayTaskSnapshotWithDB(request.TaskID); ok {
			return task, request.RequestHash, true
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	return nil, request.RequestHash, false
}

func (g *GatewayService) handleAgentOSTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.authenticateAgentOSRequest(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodPost:
		g.submitAgentOSTask(w, r)
	case http.MethodGet:
		g.listAgentOSTasks(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *GatewayService) submitAgentOSTask(w http.ResponseWriter, r *http.Request) {
	identity, ok := executionIdentityFromContext(r.Context())
	if !ok {
		agentos.SendStatusError(w, r, "execution identity unavailable", http.StatusInternalServerError)
		return
	}
	if !executionIdentityHasAgentOSScope(identity, agentOSScopeTasksSubmit) {
		agentos.SendStatusError(w, r, "task submission permission is required", http.StatusForbidden)
		return
	}
	queue := g.ensureAgentOSQueue()
	queue.Start()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var submitted agentOSTaskSubmitRequest
	if err := agentos.DecodeJSONBody(r.Body, &submitted); err != nil {
		agentos.SendStatusError(w, r, "invalid request body", http.StatusBadRequest)
		return
	}
	initialMessages, err := gatewayhttp.NormalizeAgentOSInitialMessages(submitted.InitialMessages)
	if err != nil {
		agentos.SendJSONError(w, r, "invalid_initial_messages", err.Error(), http.StatusBadRequest, false)
		return
	}
	submitted.InitialMessages = initialMessages
	assistantIdentity, err := gatewayhttp.NormalizeAgentOSAssistantIdentity(submitted.AssistantIdentity)
	if err != nil {
		agentos.SendJSONError(w, r, "invalid_assistant_identity", err.Error(), http.StatusBadRequest, false)
		return
	}
	submitted.AssistantIdentity = assistantIdentity
	skillRun, err := normalizeSkillRunRequest(submitted.SkillRun)
	if err != nil {
		agentos.SendJSONError(w, r, "invalid_skill_run", err.Error(), http.StatusBadRequest, false)
		return
	}
	submitted.SkillRun = skillRun
	visibleMessage := strings.TrimSpace(submitted.Message)
	agentMessage := visibleMessage
	reservedForEmployee := false
	if visibleMessage == "" && len(submitted.Attachments) == 0 {
		agentos.SendStatusError(w, r, "message or attachments are required", http.StatusBadRequest)
		return
	}
	if submitted.SkillRun == nil {
		if employee, _, _ := matchAgentOSExplicitAIEmployee(g.aeManager, visibleMessage); employee != nil {
			reservedForEmployee = true
			visibleMessage = normalizeAgentOSExplicitAEMentionMessage(g.aeManager, visibleMessage)
			agentMessage = visibleMessage
		}
	}
	submitted.Message = visibleMessage
	g.assessAndLogGatewayUserContent("agentos", visibleMessage)
	g.assessAndLogAgentOSRequestSecurity(visibleMessage)
	if strings.Contains(submitted.AgentName, "/") || strings.Contains(submitted.AgentName, "\\") || strings.Contains(submitted.AgentName, "..") {
		agentos.SendStatusError(w, r, "invalid agent_name", http.StatusBadRequest)
		return
	}
	if strings.Contains(submitted.RoleID, "/") || strings.Contains(submitted.RoleID, "\\") || strings.Contains(submitted.RoleID, "..") {
		agentos.SendStatusError(w, r, "invalid role_id", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(submitted.SessionID)) > 64 {
		agentos.SendStatusError(w, r, "session_id is too long", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(submitted.UserID)) > 128 {
		agentos.SendStatusError(
			w,
			r,
			"user_id must be at most 128 UTF-8 bytes and contain no control characters",
			http.StatusBadRequest,
		)
		return
	}
	if len(strings.TrimSpace(submitted.CallerID)) > 128 {
		agentos.SendStatusError(w, r, "caller_id is too long", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(submitted.RoleID)) > 128 {
		agentos.SendStatusError(w, r, "role_id is too long", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(submitted.AgentName)) > 100 {
		agentos.SendStatusError(w, r, "agent_name is too long", http.StatusBadRequest)
		return
	}
	// Authorization uses the host-authenticated service identity. X-Caller-ID
	// remains available only in legacy no-auth mode for compatibility and
	// correlation; it cannot override a service-client, tenant, or control-plane
	// credential.
	callerID := agentos.NormalizeCallerID(identity.ServiceID)
	if callerID == "anonymous" && !identity.TenantScoped && !identity.ControlPlane &&
		strings.TrimSpace(submitted.CallerID) != "" {
		callerID = agentos.NormalizeCallerID(submitted.CallerID)
	}
	requestedRole := strings.TrimSpace(submitted.RoleID)
	if requestedRole == "" {
		requestedRole = strings.TrimSpace(submitted.AgentName)
	}
	roleID, agentName, err := g.resolveAgentOSRole(requestedRole, callerID, identity)
	if err != nil {
		agentos.SendJSONError(w, r, "role_unavailable", agentos.PublicRoleError(err), http.StatusBadRequest, false)
		return
	}
	if strings.TrimSpace(submitted.AgentName) != "" &&
		strings.TrimSpace(submitted.AgentName) != agentName &&
		strings.TrimSpace(submitted.AgentName) != roleID {
		agentos.SendStatusError(w, r, "agent_name does not match the published role", http.StatusBadRequest)
		return
	}
	if submitted.SkillRun == nil && !reservedForEmployee {
		currentTurn, scoped := agentos.CurrentRequest(visibleMessage)
		// Resolve against the published role's concrete Agent. Different roles
		// may expose different Skill catalogs, so the caller's optional
		// agent_name is not authoritative here.
		mentionRun, skillTask, matched, mentionErr := g.resolveSkillMention(currentTurn, agentName)
		if mentionErr != nil {
			agentos.SendJSONError(w, r, "invalid_skill_mention", mentionErr.Error(), http.StatusBadRequest, false)
			return
		}
		if matched {
			mentionRun, mentionErr = normalizeSkillRunRequest(mentionRun)
			if mentionErr != nil {
				agentos.SendJSONError(w, r, "invalid_skill_mention", mentionErr.Error(), http.StatusBadRequest, false)
				return
			}
			submitted.SkillRun = mentionRun
			agentMessage = skilldomain.PrependMentionToTask(visibleMessage, skillTask)
			if scoped {
				// A host-bound Skill must execute only the current user turn;
				// caller-authored role wrappers are not part of its task.
				visibleMessage = currentTurn
				agentMessage = skilldomain.PrependMentionToTask(currentTurn, skillTask)
			}
		}
	}
	submitted.Message = visibleMessage
	if submitted.Collaboration != nil {
		if submitted.SkillRun != nil {
			agentos.SendJSONError(
				w, r, "unsupported_skill_collaboration",
				"skill_run and collaboration are separate execution modes",
				http.StatusBadRequest, false,
			)
			return
		}
		if len(submitted.InitialMessages) > 0 || submitted.AssistantIdentity != nil {
			agentos.SendJSONError(
				w,
				r,
				"unsupported_conversation_contract",
				"initial_messages and assistant_identity are only supported for single-assistant chat tasks",
				http.StatusBadRequest,
				false,
			)
			return
		}
		normalized, normalizeErr := g.normalizeAgentOSCollaboration(
			*submitted.Collaboration,
			callerID,
			identity,
		)
		if normalizeErr != nil {
			agentos.SendJSONError(
				w,
				r,
				"invalid_collaboration",
				normalizeErr.Error(),
				http.StatusBadRequest,
				false,
			)
			return
		}
		submitted.Collaboration = &normalized
	}
	identity.RoleID = roleID
	identity.AgentName = agentName
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(submitted.IdempotencyKey)
	}
	if len(idempotencyKey) > 256 {
		agentos.SendStatusError(w, r, "Idempotency-Key is too long", http.StatusBadRequest)
		return
	}
	effectiveUserID := identity.UserID
	effectiveSubjectID := identity.SubjectID
	subjectScopedIdempotency := isAgentOSServiceClientIdentity(identity) &&
		identity.SubjectScoped
	if strings.TrimSpace(effectiveSubjectID) == "" {
		effectiveSubjectID = effectiveUserID
	}
	submittedUserID, subjectErr := security.ValidatePersonalSubjectID(submitted.UserID)
	if subjectErr != nil {
		agentos.SendStatusError(w, r, subjectErr.Error(), http.StatusBadRequest)
		return
	}
	if isAgentOSServiceClientIdentity(identity) &&
		identity.RequireUserID &&
		!identity.SubjectScoped {
		agentos.SendJSONError(
			w,
			r,
			"user_id_required",
			agentOSUserIDHeader+" is required for this service client",
			http.StatusBadRequest,
			false,
		)
		return
	}
	switch {
	case isAgentOSServiceClientIdentity(identity) && identity.SubjectScoped:
		if submittedUserID != "" && submittedUserID != effectiveSubjectID {
			agentos.SendJSONError(
				w,
				r,
				"user_id_mismatch",
				"user_id must match "+agentOSUserIDHeader,
				http.StatusBadRequest,
				false,
			)
			return
		}
		subjectScopedIdempotency = true
	case isAgentOSServiceClientIdentity(identity) && submittedUserID != "":
		if !executionIdentityHasAgentOSScope(identity, agentOSScopeUsersActAs) {
			agentos.SendStatusError(w, r, "users:act_as permission is required", http.StatusForbidden)
			return
		}
		effectiveUserID = submittedUserID
		effectiveSubjectID = submittedUserID
		subjectScopedIdempotency = true
	case !identity.TenantScoped && submittedUserID != "":
		// Preserve local/control-plane compatibility. Authenticated tenant
		// users retain their host-established stable subject.
		effectiveUserID = submittedUserID
		effectiveSubjectID = submittedUserID
		subjectScopedIdempotency = true
	}
	identity.UserID = effectiveUserID
	identity.SubjectID = effectiveSubjectID
	identity = normalizeExecutionIdentity(identity)
	effectiveUserID = identity.UserID
	effectiveSubjectID = identity.SubjectID
	r = r.WithContext(withExecutionIdentity(r.Context(), identity))
	idempotencySubjectID := ""
	if subjectScopedIdempotency {
		idempotencySubjectID = effectiveSubjectID
	}
	requestHash, err := agentOSRequestFingerprint(
		submitted,
		roleID,
		strings.TrimSpace(submitted.RoleRevision),
		effectiveUserID,
		idempotencySubjectID,
	)
	if err != nil {
		agentos.SendStatusError(w, r, "failed to fingerprint task request", http.StatusInternalServerError)
		return
	}
	if idempotencyKey != "" {
		keyHash := agentos.TaskIdempotencyHash(
			defaultExecutionTenantID,
			identity.TenantID,
			callerID,
			idempotencySubjectID,
			idempotencyKey,
		)
		if existing, storedRequestHash, found := g.findAgentOSIdempotentTask(keyHash); found {
			if storedRequestHash != "" && storedRequestHash != requestHash {
				agentos.SendJSONError(
					w,
					r,
					"idempotency_payload_mismatch",
					"Idempotency-Key was already used for a different request",
					http.StatusConflict,
					false,
				)
				return
			}
			// Replays use the accepted task's immutable role contract, but the
			// current identity must still retain read access.
			if !isAgentOSDataPlaneTask(existing) || !executionIdentityCanReadTask(identity, existing) {
				agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Location", "/api/v1/agent/tasks/"+url.PathEscape(existing.ID))
			_ = json.NewEncoder(w).Encode(g.agentOSTaskResponseForIdentity(existing, true, identity))
			return
		}
	}

	roleRevision, roleSnapshot, err := g.captureAgentOSRoleRevision(roleID, agentName)
	if err != nil {
		agentos.SendStatusError(w, r, "failed to resolve agent role revision", http.StatusInternalServerError)
		return
	}
	if requestedRevision := strings.TrimSpace(submitted.RoleRevision); requestedRevision != "" &&
		requestedRevision != roleRevision {
		agentos.SendJSONError(
			w, r, "role_revision_conflict",
			"requested agent role revision is no longer current",
			http.StatusConflict, false,
		)
		return
	}
	if err := g.validateAgentOSRoleInput(roleID, submitted.Parameters); err != nil {
		agentos.SendJSONError(
			w, r, "invalid_parameters",
			"parameters do not match the role input schema",
			http.StatusBadRequest, false,
		)
		return
	}

	taskID := agentos.NewTaskID(time.Now())
	canSubmit, queueStats, capacityErr := queue.canSubmit(r.Context())
	if capacityErr != nil {
		agentos.SendStatusError(w, r, "AgentOS task database is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !canSubmit {
		if queueStats.Saturated {
			w.Header().Set("Retry-After", "2")
			agentos.SendJSONError(w, r, "queue_full", "AgentOS task queue is full", http.StatusTooManyRequests, true)
		} else {
			agentos.SendStatusError(w, r, "this MACS instance is not accepting AgentOS submissions", http.StatusServiceUnavailable)
		}
		return
	}

	sessionID := strings.TrimSpace(submitted.SessionID)
	clientProvidedSession := sessionID != ""
	if sessionID == "" {
		sessionID = newServerWebSessionID(time.Now())
	}
	sessionID = strings.NewReplacer("..", "", "/", "_", "\\", "_").Replace(sessionID)
	requestedWorkspace := g.currentProjectWorkspace()
	if identity.TenantScoped {
		if g.tenantMgr == nil || !tenantcore.ValidID(identity.TenantID) {
			agentos.SendStatusError(w, r, "tenant workspace is unavailable", http.StatusForbidden)
			return
		}
		sessionID = agentos.ScopeSessionID(defaultExecutionTenantID, identity.TenantID, sessionID, effectiveSubjectID)
		requestedWorkspace = projectctx.PersonalWorkspace(
			g.tenantMgr.TenantDir(identity.TenantID),
			security.PersonalPartitionID(identity.TenantID, effectiveSubjectID, defaultExecutionTenantID),
		)
		if err := os.MkdirAll(requestedWorkspace, 0755); err != nil {
			agentos.SendStatusError(w, r, "failed to prepare tenant workspace", http.StatusInternalServerError)
			return
		}
	}
	userID := effectiveUserID
	workspace, err := g.bindSessionWorkspaceOwnedAt(
		sessionID, agentName, identity.TenantID, effectiveSubjectID, requestedWorkspace,
		!clientProvidedSession || identity.TenantScoped,
	)
	if err != nil {
		agentos.SendJSONError(w, r, "workspace_conflict", agentos.PublicWorkspaceError(err), http.StatusConflict, false)
		return
	}
	attachments, err := attachmentcore.Normalize(submitted.Attachments, workspace, true)
	if err != nil {
		agentos.SendJSONError(w, r, "invalid_attachments", agentos.PublicAttachmentError(err), http.StatusBadRequest, false)
		return
	}
	now := time.Now()
	taskType := "chat"
	if submitted.Collaboration != nil {
		taskType = "collaboration"
	} else if submitted.SkillRun != nil {
		taskType = "skill"
	}
	req := &MessageRequest{
		TaskID: taskID, TaskType: taskType, SessionId: sessionID,
		TenantID: identity.TenantID, SubjectID: effectiveSubjectID, RoleID: roleID,
		Message:           attachmentcore.AgentMessage(agentMessage, attachments),
		Attachments:       attachments,
		AgentName:         agentName,
		Collaboration:     submitted.Collaboration,
		SkillRun:          cloneSkillRunRequest(submitted.SkillRun),
		Parameters:        submitted.Parameters,
		InitialMessages:   append([]gatewayhttp.AgentOSInitialMessage(nil), submitted.InitialMessages...),
		AssistantIdentity: submitted.AssistantIdentity,
		Channel:           "api",
		UserId:            userID,
		CreatedAt:         now,
		ResolvedWorkspace: workspace,
		RequestID:         agentos.RequestIDFromContext(r.Context()),
		Traceparent:       agentos.TraceparentFromContext(r.Context()),
	}
	heartbeat := now
	task := &GatewayTask{
		ID: taskID, Request: req, TenantID: identity.TenantID,
		SubjectID: effectiveSubjectID, TenantScoped: identity.TenantScoped, RoleID: roleID,
		RoleRevision: roleRevision, RoleSnapshot: roleSnapshot,
		Status: "pending", CurrentPhase: "queued",
		HeartbeatAt: &heartbeat, CallerID: callerID, CreatedAt: now,
		ResolvedWorkspace: workspace, ResultCh: make(chan interface{}, 1),
		ProgressCh: make(chan map[string]interface{}, 20), InterruptCh: make(chan string, 5),
		QueueName: agentOSQueueName, AvailableAt: &now,
	}
	var idempotencyRecord *agentos.AgentTaskRequestRecord
	if idempotencyKey != "" {
		idempotencyRecord = &agentos.AgentTaskRequestRecord{
			KeyHash: agentos.TaskIdempotencyHash(
				defaultExecutionTenantID,
				identity.TenantID,
				callerID,
				idempotencySubjectID,
				idempotencyKey,
			),
			RequestHash: requestHash,
			TenantID:    identity.TenantID,
			RoleID:      roleID,
			CallerID:    callerID,
			SubjectID:   effectiveSubjectID,
			TaskID:      taskID,
			CreatedAt:   now,
		}
	}
	if err := g.persistAgentOSSubmission(
		task,
		visibleMessage,
		attachments,
		idempotencyRecord,
	); err != nil {
		if errors.Is(err, errAgentOSInitialMessagesRequireEmptySession) {
			agentos.SendJSONError(
				w,
				r,
				"initial_messages_require_empty_session",
				"initial_messages can only be used with an empty session",
				http.StatusConflict,
				false,
			)
			return
		}
		if idempotencyRecord != nil {
			existing, storedRequestHash, found := g.findAgentOSIdempotentTask(
				idempotencyRecord.KeyHash,
			)
			if found {
				if storedRequestHash != "" && storedRequestHash != requestHash {
					agentos.SendJSONError(
						w,
						r,
						"idempotency_payload_mismatch",
						"Idempotency-Key was already used for a different request",
						http.StatusConflict,
						false,
					)
					return
				}
				if !isAgentOSDataPlaneTask(existing) ||
					!executionIdentityCanReadTask(identity, existing) {
					agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set(
					"Location",
					"/api/v1/agent/tasks/"+url.PathEscape(existing.ID),
				)
				_ = json.NewEncoder(w).Encode(
					g.agentOSTaskResponseForIdentity(existing, true, identity),
				)
				return
			}
		}
		agentos.SendStatusError(w, r, "failed to persist task submission", http.StatusInternalServerError)
		return
	}
	g.markAgentOSTaskID(taskID)
	g.broadcastTaskUpdate(taskID, "pending", nil, nil)
	// Build the accepted representation before the worker can mutate task. The
	// HTTP response is therefore race-free and always describes the queued
	// submission that the caller just created.
	acceptedResponse := g.agentOSTaskResponseForIdentity(cloneGatewayTask(task), false, identity)
	queue.Signal()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Location", "/api/v1/agent/tasks/"+url.PathEscape(taskID))
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(acceptedResponse)
}

// persistAgentOSSubmission commits the durable queue row and its user-visible
// transcript entry together. Without this boundary, a task-row failure could
// leave an AgentOS prompt in the legacy chat tables without the queue marker
// that keeps those sessions out of the unauthenticated Web compatibility API.
var errAgentOSInitialMessagesRequireEmptySession = errors.New("initial_messages require an empty session")

func (g *GatewayService) persistAgentOSSubmission(
	task *GatewayTask,
	visibleMessage string,
	attachments []attachmentcore.File,
	idempotencyRecord *agentos.AgentTaskRequestRecord,
) error {
	if g == nil || g.db == nil {
		return fmt.Errorf("database unavailable")
	}
	if task == nil || task.Request == nil {
		return fmt.Errorf("task request is required")
	}
	return g.db.Transaction(func(tx *gorm.DB) error {
		seedCount := len(task.Request.InitialMessages)
		messageCountDelta := seedCount + 1
		ownerTenantID := normalizePersonalOwnerTenantID(task.Request.TenantID)
		ownerSubjectID := strings.TrimSpace(task.Request.SubjectID)
		messageCreatedAt := task.CreatedAt
		if messageCreatedAt.IsZero() {
			messageCreatedAt = time.Now()
		}

		if idempotencyRecord != nil {
			record := *idempotencyRecord
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("reserve idempotency key: %w", err)
			}
		}

		if seedCount > 0 {
			// Reserving message_count=0 before inserting any transcript rows makes
			// the empty-session rule race-safe. Any later failure rolls this update
			// back together with the messages, idempotency row, and task record.
			sessionUpdate := tx.Model(&ChatSession{}).
				Where(
					"id = ? AND tenant_id = ? AND subject_id = ? AND message_count = 0",
					task.Request.SessionId,
					ownerTenantID,
					ownerSubjectID,
				).
				Updates(map[string]interface{}{
					"message_count": messageCountDelta,
					"agent_name":    task.Request.AgentName,
					"updated_at":    messageCreatedAt.Add(time.Duration(seedCount) * time.Microsecond),
				})
			if sessionUpdate.Error != nil {
				return fmt.Errorf("reserve empty task session: %w", sessionUpdate.Error)
			}
			if sessionUpdate.RowsAffected != 1 {
				return errAgentOSInitialMessagesRequireEmptySession
			}

			var existingMessages int64
			if err := tx.Model(&ChatMessage{}).
				Where(
					"session_id = ? AND tenant_id = ? AND subject_id = ?",
					task.Request.SessionId,
					ownerTenantID,
					ownerSubjectID,
				).
				Count(&existingMessages).Error; err != nil {
				return fmt.Errorf("check empty task session: %w", err)
			}
			if existingMessages != 0 {
				return errAgentOSInitialMessagesRequireEmptySession
			}

			seedAgentName := task.Request.AgentName
			if task.Request.AssistantIdentity != nil {
				seedAgentName = task.Request.AssistantIdentity.Name
			}
			for index, seed := range task.Request.InitialMessages {
				message := ChatMessage{
					SessionID: task.Request.SessionId,
					TenantID:  task.Request.TenantID,
					SubjectID: task.Request.SubjectID,
					Role:      "assistant",
					AgentName: seedAgentName,
					Content:   seed.Content,
					Status:    "completed",
					Channel:   "api",
					UserId:    task.Request.UserId,
					CreatedAt: messageCreatedAt.Add(time.Duration(index) * time.Microsecond),
				}
				if err := tx.Create(&message).Error; err != nil {
					return fmt.Errorf("persist initial assistant message: %w", err)
				}
			}
		}

		message := ChatMessage{
			SessionID:   task.Request.SessionId,
			TenantID:    task.Request.TenantID,
			SubjectID:   task.Request.SubjectID,
			Role:        "user",
			AgentName:   task.Request.AgentName,
			Content:     visibleMessage,
			Attachments: append([]attachmentcore.File(nil), attachments...),
			Status:      "pending",
			TaskID:      task.ID,
			Channel:     "api",
			UserId:      task.Request.UserId,
			CreatedAt:   messageCreatedAt.Add(time.Duration(seedCount) * time.Microsecond),
		}
		if err := tx.Create(&message).Error; err != nil {
			return fmt.Errorf("persist task message: %w", err)
		}
		if seedCount == 0 {
			sessionUpdate := tx.Model(&ChatSession{}).
				Where(
					"id = ? AND tenant_id = ? AND subject_id = ?",
					task.Request.SessionId,
					ownerTenantID,
					ownerSubjectID,
				).
				Updates(map[string]interface{}{
					"message_count": gorm.Expr("message_count + 1"),
					"agent_name":    task.Request.AgentName,
					"updated_at":    message.CreatedAt,
				})
			if sessionUpdate.Error != nil {
				return fmt.Errorf("update task session: %w", sessionUpdate.Error)
			}
			if sessionUpdate.RowsAffected != 1 {
				return fmt.Errorf("task session is unavailable")
			}
		}
		if err := SaveGatewayTaskToDB(tx, task); err != nil {
			return fmt.Errorf("persist task record: %w", err)
		}
		if err := agentos.EnsureTaskFollowupState(tx, task.ID, true, messageCreatedAt); err != nil {
			return fmt.Errorf("persist task follow-up state: %w", err)
		}
		return nil
	})
}

func (g *GatewayService) listAgentOSTasks(w http.ResponseWriter, r *http.Request) {
	if g == nil || g.db == nil {
		agentos.SendStatusError(w, r, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, ok := executionIdentityFromContext(r.Context())
	if !ok {
		agentos.SendStatusError(w, r, "execution identity unavailable", http.StatusInternalServerError)
		return
	}
	limit, err := agentos.ParseTaskListLimit(r)
	if err != nil {
		agentos.SendJSONError(w, r, "invalid_limit", "limit must be an integer between 1 and 100", http.StatusBadRequest, false)
		return
	}

	// Authorization is part of the database query, before pagination is
	// evaluated. A cursor can only narrow the already-authorized result set.
	query := scopeAgentOSTaskListQuery(
		g.db.Model(&GatewayTaskRecord{}).Where("queue_name = ?", agentOSQueueName),
		identity,
	)
	if caller := strings.TrimSpace(r.URL.Query().Get("caller_id")); caller != "" {
		query = query.Where("caller_id = ?", agentos.NormalizeCallerID(caller))
	}
	if roleID := strings.TrimSpace(r.URL.Query().Get("role_id")); roleID != "" {
		query = query.Where("role_id = ?", roleID)
	}
	if rawStatuses, present := r.URL.Query()["status"]; present {
		if len(rawStatuses) != 1 || !agentos.ValidTaskStatus(strings.TrimSpace(rawStatuses[0])) {
			agentos.SendJSONError(
				w, r, "invalid_status",
				"status is not a supported AgentOS task status",
				http.StatusBadRequest, false,
			)
			return
		}
		query = query.Where("status = ?", strings.TrimSpace(rawStatuses[0]))
	}
	if rawCursors, present := r.URL.Query()["cursor"]; present {
		if len(rawCursors) != 1 || strings.TrimSpace(rawCursors[0]) == "" {
			agentos.SendJSONError(w, r, "invalid_cursor", "cursor is invalid", http.StatusBadRequest, false)
			return
		}
		rawCursor := strings.TrimSpace(rawCursors[0])
		createdAt, taskID, cursorErr := agentos.DecodeTaskListCursor(rawCursor)
		if cursorErr != nil {
			agentos.SendJSONError(w, r, "invalid_cursor", "cursor is invalid", http.StatusBadRequest, false)
			return
		}
		query = query.Where(
			"created_at < ? OR (created_at = ? AND task_id < ?)",
			createdAt, createdAt, taskID,
		)
	}

	var records []GatewayTaskRecord
	query = query.Order("created_at DESC, task_id DESC").Limit(limit + 1)
	if err := query.Find(&records).Error; err != nil {
		agentos.SendStatusError(
			w, r, "AgentOS task database is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	hasNext := len(records) > limit
	if hasNext {
		records = records[:limit]
	}
	items := make([]map[string]interface{}, 0, len(records))
	for i := range records {
		task := g.overlayAgentOSQueueTaskSnapshot(gatewayTaskFromRecord(records[i]))
		if executionIdentityCanReadTask(identity, task) {
			items = append(items, g.agentOSTaskResponseForIdentity(task, false, identity))
		}
	}
	response := map[string]interface{}{"tasks": items, "count": len(items)}
	if hasNext && len(records) > 0 {
		last := records[len(records)-1]
		response["next_cursor"] = agentos.EncodeTaskListCursor(last.CreatedAt, last.TaskID)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(response)
}

func (g *GatewayService) handleAgentOSTaskCancellation(
	w http.ResponseWriter,
	r *http.Request,
	identity ExecutionIdentity,
	taskID string,
) {
	current, err := g.loadAgentOSQueueTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, ErrAgentOSTaskNotFound) {
			agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
			return
		}
		agentos.SendStatusError(
			w,
			r,
			"AgentOS task database is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	if !executionIdentityCanCancelTask(identity, current) {
		// Preserve existence hiding across tenant, role, and service scopes.
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
		return
	}

	task, err := g.cancelAgentOSQueueTask(r.Context(), taskID)
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(
			g.agentOSTaskResponseForIdentity(task, false, identity),
		)
	case errors.Is(err, ErrAgentOSTaskNotFound):
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
	case errors.Is(err, ErrAgentOSTaskAlreadyTerminal):
		agentos.SendStatusError(w, r, "task is already terminal", http.StatusConflict)
	default:
		agentos.SendStatusError(
			w,
			r,
			"AgentOS task database is unavailable",
			http.StatusServiceUnavailable,
		)
	}
}

type agentOSTaskFollowupRequest struct {
	Message        string `json:"message"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

var (
	errAgentOSTaskFollowupUnsupported = errors.New("task execution mode does not support follow-ups")
	errAgentOSTaskFollowupState       = errors.New("task is not accepting follow-ups")
)

func (g *GatewayService) agentOSTaskSupportsFollowups(task *GatewayTask) bool {
	if !isAgentOSDataPlaneTask(task) || task.Request == nil || task.Request.Collaboration != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.Request.TaskType)) {
	case "", "chat":
		if task.Request.SkillRun == nil && g != nil && g.aeManager != nil {
			if cfg, _, _ := matchAgentOSExplicitAIEmployee(g.aeManager, task.Request.Message); cfg != nil {
				return false
			}
		}
		return true
	case "skill":
		return task.Request.SkillRun != nil
	default:
		return false
	}
}

func agentOSTaskFollowupResponse(record *agentos.TaskFollowupRecord, replay bool) map[string]interface{} {
	if record == nil {
		return map[string]interface{}{}
	}
	base := "/api/v1/agent/tasks/" + url.PathEscape(record.TaskID) + "/followups"
	return map[string]interface{}{
		"followup_id":       record.FollowupID,
		"task_id":           record.TaskID,
		"status":            record.Status,
		"message":           record.Message,
		"delivery_attempt":  record.DeliveryAttempt,
		"created_at":        record.CreatedAt,
		"claimed_at":        record.ClaimedAt,
		"applied_at":        record.AppliedAt,
		"cancelled_at":      record.CancelledAt,
		"idempotent_replay": replay,
		"links": map[string]string{
			"self":   base + "/" + url.PathEscape(record.FollowupID),
			"task":   "/api/v1/agent/tasks/" + url.PathEscape(record.TaskID),
			"events": "/api/v1/agent/tasks/" + url.PathEscape(record.TaskID) + "/events",
		},
	}
}

func (g *GatewayService) findAgentOSTaskFollowupByHash(
	ctx context.Context,
	hash string,
) (*agentos.TaskFollowupRecord, bool) {
	if g == nil || g.db == nil || strings.TrimSpace(hash) == "" {
		return nil, false
	}
	var record agentos.TaskFollowupRecord
	if err := g.db.WithContext(ctx).Where("idempotency_hash = ?", strings.TrimSpace(hash)).First(&record).Error; err != nil {
		return nil, false
	}
	return &record, true
}

func (g *GatewayService) writeAgentOSTaskFollowupReplay(
	w http.ResponseWriter,
	r *http.Request,
	record *agentos.TaskFollowupRecord,
	requestHash string,
) bool {
	if record == nil {
		return false
	}
	if record.RequestHash != "" && record.RequestHash != requestHash {
		agentos.SendJSONError(
			w,
			r,
			"idempotency_payload_mismatch",
			"Idempotency-Key was already used for a different follow-up",
			http.StatusConflict,
			false,
		)
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set(
		"Location",
		"/api/v1/agent/tasks/"+url.PathEscape(record.TaskID)+"/followups/"+url.PathEscape(record.FollowupID),
	)
	_ = json.NewEncoder(w).Encode(agentOSTaskFollowupResponse(record, true))
	return true
}

func (g *GatewayService) handleAgentOSTaskFollowups(
	w http.ResponseWriter,
	r *http.Request,
	identity ExecutionIdentity,
	taskID string,
	tail []string,
) {
	task, err := g.loadAgentOSQueueTask(r.Context(), taskID)
	if errors.Is(err, ErrAgentOSTaskNotFound) {
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		agentos.SendStatusError(w, r, "AgentOS task database is unavailable", http.StatusServiceUnavailable)
		return
	}

	if r.Method == http.MethodGet {
		if !executionIdentityCanReadTask(identity, task) {
			agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
			return
		}
		if len(tail) > 1 {
			agentos.SendStatusError(w, r, "follow-up not found", http.StatusNotFound)
			return
		}
		if len(tail) == 1 {
			followupID, decodeErr := url.PathUnescape(tail[0])
			if decodeErr != nil || strings.TrimSpace(followupID) == "" {
				agentos.SendStatusError(w, r, "invalid follow-up id", http.StatusBadRequest)
				return
			}
			var record agentos.TaskFollowupRecord
			queryErr := g.db.WithContext(r.Context()).
				Where("task_id = ? AND followup_id = ?", task.ID, followupID).
				First(&record).Error
			if errors.Is(queryErr, gorm.ErrRecordNotFound) {
				agentos.SendStatusError(w, r, "follow-up not found", http.StatusNotFound)
				return
			}
			if queryErr != nil {
				agentos.SendStatusError(w, r, "AgentOS task database is unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(agentOSTaskFollowupResponse(&record, false))
			return
		}

		limit, limitErr := agentos.ParseTaskListLimit(r)
		if limitErr != nil {
			agentos.SendJSONError(w, r, "invalid_limit", "limit must be an integer between 1 and 100", http.StatusBadRequest, false)
			return
		}
		var records []agentos.TaskFollowupRecord
		if queryErr := g.db.WithContext(r.Context()).
			Where("task_id = ?", task.ID).
			Order("created_at ASC, id ASC").
			Limit(limit).
			Find(&records).Error; queryErr != nil {
			agentos.SendStatusError(w, r, "AgentOS task database is unavailable", http.StatusServiceUnavailable)
			return
		}
		items := make([]map[string]interface{}, 0, len(records))
		for index := range records {
			items = append(items, agentOSTaskFollowupResponse(&records[index], false))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"followups": items,
			"count":     len(items),
		})
		return
	}

	if r.Method != http.MethodPost || len(tail) != 0 {
		w.Header().Set("Allow", "GET, POST")
		agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !executionIdentityCanFollowupTask(identity, task) {
		// Follow the task read/cancel contract and hide existence when the
		// caller lacks either the immutable boundary or the operation scope.
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
		return
	}
	if !g.agentOSTaskSupportsFollowups(task) {
		agentos.SendJSONError(w, r, "followup_not_supported", errAgentOSTaskFollowupUnsupported.Error(), http.StatusConflict, false)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var submitted agentOSTaskFollowupRequest
	if err := agentos.DecodeJSONBody(r.Body, &submitted); err != nil {
		agentos.SendStatusError(w, r, "invalid request body", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(submitted.Message)
	if message == "" {
		agentos.SendStatusError(w, r, "follow-up message is required", http.StatusBadRequest)
		return
	}
	if len([]byte(message)) > 60<<10 {
		agentos.SendStatusError(w, r, "follow-up message is too long", http.StatusBadRequest)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(submitted.IdempotencyKey)
	}
	if len(idempotencyKey) > 256 {
		agentos.SendStatusError(w, r, "Idempotency-Key is too long", http.StatusBadRequest)
		return
	}

	g.assessAndLogGatewayUserContent("agentos_followup", message)
	g.assessAndLogAgentOSRequestSecurity(message)
	requestHash := agentos.TaskFollowupRequestHash(message)
	callerID := agentos.NormalizeCallerID(identity.ServiceID)
	var idempotencyHash *string
	if idempotencyKey != "" {
		hash := agentos.TaskFollowupIdempotencyHash(
			defaultExecutionTenantID,
			identity.TenantID,
			callerID,
			normalizedAgentOSTaskSubject(task),
			task.ID,
			idempotencyKey,
		)
		idempotencyHash = &hash
		if existing, found := g.findAgentOSTaskFollowupByHash(r.Context(), hash); found {
			g.writeAgentOSTaskFollowupReplay(w, r, existing, requestHash)
			return
		}
	}
	if task.Status != "pending" && task.Status != "running" {
		agentos.SendJSONError(w, r, "task_not_accepting_followups", errAgentOSTaskFollowupState.Error(), http.StatusConflict, false)
		return
	}

	now := time.Now().Round(0)
	record := &agentos.TaskFollowupRecord{
		FollowupID:      agentos.NewTaskFollowupID(now),
		TaskID:          task.ID,
		TenantID:        normalizedAgentOSTaskTenant(task),
		SubjectID:       normalizedAgentOSTaskSubject(task),
		RoleID:          normalizedAgentOSTaskRole(task),
		CallerID:        callerID,
		Message:         message,
		Status:          agentos.TaskFollowupStatusPending,
		IdempotencyHash: idempotencyHash,
		RequestHash:     requestHash,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err = g.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := agentos.EnsureTaskFollowupState(tx, task.ID, true, now); err != nil {
			return err
		}
		if err := agentos.ReserveTaskFollowupSlot(tx, task.ID, now); err != nil {
			return err
		}
		var current GatewayTaskRecord
		if err := tx.Where("task_id = ? AND queue_name = ?", task.ID, agentOSQueueName).First(&current).Error; err != nil {
			return err
		}
		currentTask := gatewayTaskFromRecord(current)
		if (current.Status != "pending" && current.Status != "running") ||
			!g.agentOSTaskSupportsFollowups(currentTask) {
			return errAgentOSTaskFollowupState
		}
		return tx.Create(record).Error
	})
	if err != nil {
		if idempotencyHash != nil {
			if existing, found := g.findAgentOSTaskFollowupByHash(r.Context(), *idempotencyHash); found {
				g.writeAgentOSTaskFollowupReplay(w, r, existing, requestHash)
				return
			}
		}
		if errors.Is(err, agentos.ErrTaskFollowupsClosed) || errors.Is(err, errAgentOSTaskFollowupState) {
			agentos.SendJSONError(w, r, "task_not_accepting_followups", errAgentOSTaskFollowupState.Error(), http.StatusConflict, false)
			return
		}
		agentos.SendStatusError(w, r, "failed to persist task follow-up", http.StatusServiceUnavailable)
		return
	}

	g.markAgentOSTaskID(task.ID)
	g.broadcastAgentOSTaskEvent(task.ID, "followup_accepted", agentOSTaskFollowupResponse(record, false))
	g.ensureAgentOSQueue().Signal()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set(
		"Location",
		"/api/v1/agent/tasks/"+url.PathEscape(task.ID)+"/followups/"+url.PathEscape(record.FollowupID),
	)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(agentOSTaskFollowupResponse(record, false))
}

func persistAgentOSTaskFollowupMessageWithDB(
	tx *gorm.DB,
	task *GatewayTask,
	record *agentos.TaskFollowupRecord,
) error {
	if tx == nil || task == nil || task.Request == nil || record == nil {
		return fmt.Errorf("task follow-up transcript context is unavailable")
	}
	tenantID, subjectID, scoped := gatewayTaskPersonalOwner(task)
	if !scoped || strings.TrimSpace(subjectID) == "" {
		return fmt.Errorf("AgentOS task follow-up has no personal owner")
	}
	if !samePersistedPersonalOwner(tenantID, subjectID, record.TenantID, record.SubjectID) {
		return fmt.Errorf("%w: follow-up and task owners differ", ErrTaskOwnerConflict)
	}

	var session ChatSession
	query := scopeRowsToPersistedOwner(
		tx.Where("id = ?", task.Request.SessionId),
		tenantID,
		subjectID,
	)
	if err := query.First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: task session is missing or belongs to another subject", ErrSessionOwnerConflict)
		}
		return fmt.Errorf("load task follow-up session: %w", err)
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	message := ChatMessage{
		SessionID: task.Request.SessionId,
		TenantID:  normalizePersonalOwnerTenantID(session.TenantID),
		SubjectID: strings.TrimSpace(session.SubjectID),
		Role:      "user",
		AgentName: task.Request.AgentName,
		Content:   record.Message,
		Status:    "running",
		TaskID:    task.ID,
		Channel:   task.Request.Channel,
		UserId:    task.Request.UserId,
		CreatedAt: createdAt,
	}
	if err := tx.Create(&message).Error; err != nil {
		return fmt.Errorf("persist task follow-up message: %w", err)
	}
	updates := map[string]interface{}{
		"message_count": gorm.Expr("message_count + 1"),
		"updated_at":    createdAt,
	}
	if task.Request.AgentName != "" {
		updates["agent_name"] = task.Request.AgentName
	}
	if task.Request.UserId != "" {
		updates["user_id"] = task.Request.UserId
	}
	if err := tx.Model(&session).Updates(updates).Error; err != nil {
		return fmt.Errorf("update task follow-up session: %w", err)
	}
	return nil
}

// persistAgentOSAssistantCompletionWithDB is intentionally transaction-aware:
// the assistant turn, drained follow-up gate, task terminal snapshot, and Skill
// Run receipt become visible together.
func persistAgentOSAssistantCompletionWithDB(tx *gorm.DB, task *GatewayTask) error {
	if tx == nil || task == nil || task.Request == nil ||
		task.Status != "completed" ||
		strings.TrimSpace(task.Request.SessionId) == "" ||
		!shouldPersistAssistantCompletion(task.Request.TaskType, task.ID) {
		return nil
	}
	tenantID, subjectID, scoped := gatewayTaskPersonalOwner(task)
	if !scoped || strings.TrimSpace(subjectID) == "" {
		return fmt.Errorf("AgentOS completion has no personal owner")
	}
	var existing ChatMessage
	lookup := scopeRowsToPersistedOwner(
		tx.Where("task_id = ? AND role = ?", task.ID, "assistant"),
		tenantID,
		subjectID,
	).First(&existing)
	if lookup.Error == nil {
		return nil
	}
	if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load existing AgentOS completion: %w", lookup.Error)
	}

	var session ChatSession
	sessionQuery := scopeRowsToPersistedOwner(
		tx.Where("id = ?", task.Request.SessionId),
		tenantID,
		subjectID,
	)
	if err := sessionQuery.First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: task session is missing or belongs to another subject", ErrSessionOwnerConflict)
		}
		return fmt.Errorf("load AgentOS completion session: %w", err)
	}
	createdAt := time.Now()
	if task.CompletedAt != nil {
		createdAt = *task.CompletedAt
	}
	message := ChatMessage{
		SessionID: task.Request.SessionId,
		TenantID:  normalizePersonalOwnerTenantID(session.TenantID),
		SubjectID: strings.TrimSpace(session.SubjectID),
		Role:      "assistant",
		AgentName: task.Request.AgentName,
		Content:   gatewayResultText(task.Result),
		Status:    "completed",
		TaskID:    task.ID,
		Channel:   task.Request.Channel,
		UserId:    task.Request.UserId,
		CreatedAt: createdAt,
	}
	if err := tx.Create(&message).Error; err != nil {
		return fmt.Errorf("persist AgentOS completion message: %w", err)
	}
	updates := map[string]interface{}{
		"message_count": gorm.Expr("message_count + 1"),
		"updated_at":    createdAt,
	}
	if task.Request.AgentName != "" {
		updates["agent_name"] = task.Request.AgentName
	}
	if task.Request.UserId != "" {
		updates["user_id"] = task.Request.UserId
	}
	if err := tx.Model(&session).Updates(updates).Error; err != nil {
		return fmt.Errorf("update AgentOS completion session: %w", err)
	}
	return nil
}

func (g *GatewayService) applyAgentOSTaskFollowup(
	ctx context.Context,
	task *GatewayTask,
	record *agentos.TaskFollowupRecord,
) error {
	if g == nil || g.db == nil || task == nil || record == nil {
		return fmt.Errorf("AgentOS task follow-up runtime is unavailable")
	}
	applied, newlyApplied, err := agentos.ApplyClaimedTaskFollowup(
		ctx,
		g.db,
		task.ID,
		record.FollowupID,
		task.LeaseToken,
		time.Now(),
		func(tx *gorm.DB, stored *agentos.TaskFollowupRecord) error {
			return persistAgentOSTaskFollowupMessageWithDB(tx, task, stored)
		},
	)
	if err != nil {
		return err
	}
	if newlyApplied {
		g.broadcastAgentOSTaskEvent(
			task.ID,
			"followup_applied",
			agentOSTaskFollowupResponse(applied, false),
		)
	}
	return nil
}

func (g *GatewayService) handleAgentOSTaskByID(w http.ResponseWriter, r *http.Request) {
	identity, authenticated := g.authenticateAgentOSRequest(w, r)
	if !authenticated {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/agent/tasks/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		agentos.SendStatusError(w, r, "task id is required", http.StatusBadRequest)
		return
	}
	taskID, err := url.PathUnescape(parts[0])
	if err != nil {
		agentos.SendStatusError(w, r, "invalid task id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if action == "events" && r.Method == http.MethodGet {
		task, loadErr := g.loadAgentOSQueueTaskSnapshot(r.Context(), taskID)
		if errors.Is(loadErr, ErrAgentOSTaskNotFound) ||
			loadErr == nil && !executionIdentityCanReadTask(identity, task) {
			agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
			return
		}
		if loadErr != nil {
			agentos.SendStatusError(
				w, r, "AgentOS task database is unavailable",
				http.StatusServiceUnavailable,
			)
			return
		}
		g.handleAgentOSTaskEvents(w, r, taskID)
		return
	}
	if action == "approvals" {
		tail := []string(nil)
		if len(parts) > 2 {
			tail = parts[2:]
		}
		g.handleAgentOSTaskApprovals(w, r, identity, taskID, tail)
		return
	}
	if action == "followups" {
		tail := []string(nil)
		if len(parts) > 2 {
			tail = parts[2:]
		}
		g.handleAgentOSTaskFollowups(w, r, identity, taskID, tail)
		return
	}
	if action == "cancel" && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
		g.handleAgentOSTaskCancellation(w, r, identity, taskID)
		return
	}
	if action != "" {
		agentos.SendStatusError(w, r, "unknown task action", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodDelete {
		g.handleAgentOSTaskCancellation(w, r, identity, taskID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, DELETE")
		agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	task, loadErr := g.loadAgentOSQueueTaskSnapshot(r.Context(), taskID)
	if errors.Is(loadErr, ErrAgentOSTaskNotFound) ||
		loadErr == nil && !executionIdentityCanReadTask(identity, task) {
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
		return
	}
	if loadErr != nil {
		agentos.SendStatusError(
			w, r, "AgentOS task database is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(g.agentOSTaskResponseForIdentity(task, false, identity))
}

func matchAgentOSExplicitAIEmployee(manager *AIEmployeeManager, message string) (*AIEmployeeConfig, string, string) {
	if manager == nil {
		return nil, "", ""
	}
	current, scoped := agentos.CurrentRequest(message)
	if scoped {
		cfg, task := manager.MatchAtName(current)
		if cfg == nil {
			return nil, "", current
		}
		return cfg, task, current
	}
	cfg, task := manager.MatchAtName(message)
	return cfg, task, strings.TrimSpace(message)
}

// normalizeAgentOSExplicitAEMentionMessage removes untrusted caller role
// wrappers only when the current user turn contains a valid explicit mention.
// The stored and executed task then reflects what the user actually asked.
func normalizeAgentOSExplicitAEMentionMessage(manager *AIEmployeeManager, message string) string {
	cfg, _, current := matchAgentOSExplicitAIEmployee(manager, message)
	if cfg == nil {
		return strings.TrimSpace(message)
	}
	return current
}

func (g *GatewayService) runAgentOSExplicitAIEmployee(
	ctx context.Context,
	task *GatewayTask,
	cfg *AIEmployeeConfig,
	employeeTask string,
) (string, error) {
	if g == nil || g.aeManager == nil || g.pipelineExec == nil || task == nil || task.Request == nil || cfg == nil {
		return "", fmt.Errorf("AI employee runtime is unavailable")
	}
	dispatchCtx := withAESuppressResultChatPersistence(ctx)
	// Explicit AgentOS @mentions bypass the ordinary chat-agent setup, which is
	// where tenantRequestEvidence is normally sealed. Preserve the actual user
	// turn here so pipeline stage prompts cannot be mistaken for a filesystem
	// request by the tenant evidence gate.
	userInput := normalizeAgentOSExplicitAEMentionMessage(g.aeManager, task.Request.Message)
	dispatchCtx = withTenantRequestEvidence(dispatchCtx, userInput, task.Request.Attachments)
	// Publish the employee identity before dispatch starts its pipeline. The
	// first business-stage event can then safely replace this coarse phase
	// without a post-dispatch race reverting the UI to a generic label.
	g.ObserveTaskRuntime(task.ID, "ai_employee:"+cfg.ID, "")
	record, err := g.aeManager.DispatchExplicitMention(
		dispatchCtx,
		cfg.ID,
		strings.TrimSpace(employeeTask),
		task.Request.SessionId,
	)
	if err != nil {
		return "", err
	}
	if record == nil {
		return "", fmt.Errorf("AI employee dispatch returned no work record")
	}
	if record.Status == "queued" || strings.TrimSpace(record.ExecID) == "" {
		return fmt.Sprintf("AI员工「%s」当前正在处理其他任务。本次请求已进入员工队列；如需独立执行，请稍后重新 @%s 发起。", cfg.Name, cfg.AtName), nil
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		exec, ok := g.pipelineExec.GetExecutionByID(record.ExecID)
		if ok {
			switch exec.Status {
			case "completed", "degraded":
				result := strings.TrimSpace(g.publicAgentOSArtifactLinks(agentOSExplicitAIEmployeeResult(exec)))
				if result == "" {
					result = fmt.Sprintf("AI员工「%s」任务已完成。", cfg.Name)
				}
				return result, nil
			case "failed", "stopped":
				result := strings.TrimSpace(g.publicAgentOSArtifactLinks(agentOSExplicitAIEmployeeResult(exec)))
				if result == "" {
					result = fmt.Sprintf("AI员工「%s」任务%s。", cfg.Name, exec.Status)
				}
				return result, fmt.Errorf("AI employee pipeline %s", exec.Status)
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// agentOSExplicitAIEmployeeResult keeps the synchronous AgentOS response on
// the same delivery path as the background AI-employee watcher. Without this
// enrichment, a pipeline can create and validate an artifact while the
// persisted chat message contains only a filesystem path, so the user loses
// the download link after the live notification disappears.
func agentOSExplicitAIEmployeeResult(exec *PipelineExecution) string {
	result := strings.TrimSpace(summarizeAEPipelineResult(exec))
	if exec == nil {
		return result
	}
	switch strings.TrimSpace(exec.Status) {
	case "completed", "degraded":
		return strings.TrimSpace(pipelineResultWithArtifacts(exec, result))
	default:
		return result
	}
}

var ErrAgentOSApprovalConflict = errors.New("approval state changed concurrently")

func (g *GatewayService) withAgentOSApprovalRuntime(ctx context.Context) context.Context {
	if g == nil || g.db == nil {
		return ctx
	}
	approvalTTL := approvalcore.DefaultTTL
	if g.config != nil && g.config.Gateway.ApprovalTTLSeconds > 0 {
		approvalTTL = time.Duration(g.config.Gateway.ApprovalTTLSeconds) * time.Second
	}
	return approvalcore.WithToolApprovalBroker(ctx, approvalcore.NewGormToolApprovalBroker(g.db, approvalTTL))
}

func executionIdentityCanDecideApproval(identity ExecutionIdentity, task *GatewayTask) bool {
	return executionIdentityMatchesTaskBoundary(identity, task) &&
		executionIdentityHasAgentOSScope(identity, agentOSScopeApprovalsDecide)
}

func approvalDecisionActor(identity ExecutionIdentity) string {
	for _, value := range []string{identity.ServiceID, identity.UserID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	if identity.ControlPlane {
		return "openkitty-control-plane"
	}
	return "unknown"
}

func (g *GatewayService) listTaskApprovals(taskID string) ([]approvalcore.ApprovalRequestRecord, error) {
	if g == nil || g.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	var approvals []approvalcore.ApprovalRequestRecord
	if err := g.db.Where("task_id = ?", taskID).
		Order("requested_at asc").
		Find(&approvals).Error; err != nil {
		return nil, err
	}
	return approvals, nil
}

func (g *GatewayService) handleAgentOSTaskApprovals(
	w http.ResponseWriter,
	r *http.Request,
	identity ExecutionIdentity,
	taskID string,
	tail []string,
) {
	task, loadErr := g.loadAgentOSQueueTaskSnapshot(r.Context(), taskID)
	if errors.Is(loadErr, ErrAgentOSTaskNotFound) ||
		loadErr == nil && !executionIdentityMatchesTaskTenant(identity, task) {
		agentos.SendStatusError(w, r, "task not found", http.StatusNotFound)
		return
	}
	if loadErr != nil {
		agentos.SendStatusError(
			w, r, "AgentOS task database is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}

	if len(tail) == 0 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !executionIdentityCanReadApproval(identity, task) {
			agentos.SendStatusError(w, r, "approval read permission is required", http.StatusForbidden)
			return
		}
		approvals, err := g.listTaskApprovals(taskID)
		if err != nil {
			agentos.SendStatusError(w, r, "failed to list approvals", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"task_id": taskID, "approvals": approvals, "count": len(approvals),
		})
		return
	}

	if len(tail) != 2 || r.Method != http.MethodPost {
		agentos.SendStatusError(w, r, "unknown approval action", http.StatusNotFound)
		return
	}
	if !executionIdentityCanDecideApproval(identity, task) {
		agentos.SendStatusError(w, r, "approval permission is required", http.StatusForbidden)
		return
	}
	approvalID := strings.TrimSpace(tail[0])
	action := strings.ToLower(strings.TrimSpace(tail[1]))
	if approvalID == "" || (action != "approve" && action != "deny") {
		agentos.SendStatusError(w, r, "unknown approval action", http.StatusNotFound)
		return
	}

	var body struct {
		Reason string `json:"reason,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := agentos.DecodeJSONBody(r.Body, &body); err != nil {
			agentos.SendStatusError(w, r, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if len(body.Reason) > 1024 {
		agentos.SendStatusError(w, r, "reason is too long", http.StatusBadRequest)
		return
	}

	approval, updatedTask, err := g.decideTaskApproval(
		taskID,
		approvalID,
		action == "approve",
		approvalDecisionActor(identity),
		body.Reason,
	)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			agentos.SendStatusError(w, r, "approval not found", http.StatusNotFound)
		case errors.Is(err, ErrAgentOSApprovalConflict):
			agentos.SendJSONError(w, r, "approval_conflict", "approval cannot be decided in its current state", http.StatusConflict, false)
		default:
			agentos.SendStatusError(
				w, r, "AgentOS approval database is unavailable",
				http.StatusServiceUnavailable,
			)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"approval": approval,
		"task":     g.agentOSTaskResponseForIdentity(updatedTask, false, identity),
	})
}

func (g *GatewayService) decideTaskApproval(
	taskID, approvalID string,
	approve bool,
	decidedBy, decisionReason string,
) (approvalcore.ApprovalRequestRecord, *GatewayTask, error) {
	if g == nil || g.db == nil {
		return approvalcore.ApprovalRequestRecord{}, nil, fmt.Errorf("database unavailable")
	}
	decidedBy = boundedIdentityValue(decidedBy, 128)
	decisionReason = textutil.TruncateUTF8Bytes(strings.TrimSpace(decisionReason), 1024)
	now := time.Now().Round(0)
	if _, err := approvalcore.ExpireAgentOSApprovals(g.db, now); err != nil {
		return approvalcore.ApprovalRequestRecord{}, nil, err
	}
	var approval approvalcore.ApprovalRequestRecord
	var taskRecord GatewayTaskRecord
	err := g.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("task_id = ? AND approval_id = ?", taskID, approvalID).
			First(&approval).Error; err != nil {
			return err
		}
		if approval.Status != approvalcore.StatusPending {
			return fmt.Errorf("%w: approval is already %s", ErrAgentOSApprovalConflict, approval.Status)
		}
		if err := tx.
			Where("task_id = ? AND queue_name = ?", taskID, agentOSQueueName).
			First(&taskRecord).Error; err != nil {
			return err
		}
		if taskRecord.Status != "waiting_approval" {
			return fmt.Errorf(
				"%w: task is %s, not waiting for approval",
				ErrAgentOSApprovalConflict,
				taskRecord.Status,
			)
		}

		approvalStatus := approvalcore.StatusDenied
		if approve {
			approvalStatus = approvalcore.StatusApproved
		}
		approvalUpdate := tx.Model(&approvalcore.ApprovalRequestRecord{}).
			Where("approval_id = ? AND task_id = ? AND status = ?", approvalID, taskID, approvalcore.StatusPending).
			Updates(map[string]interface{}{
				"status": approvalStatus, "decided_at": now,
				"decided_by": decidedBy, "decision_reason": decisionReason,
				"updated_at": now,
			})
		if approvalUpdate.Error != nil {
			return approvalUpdate.Error
		}
		if approvalUpdate.RowsAffected != 1 {
			return fmt.Errorf("%w: approval decision lost a concurrent update", ErrAgentOSApprovalConflict)
		}

		taskUpdates := map[string]interface{}{}
		if approve {
			taskUpdates = map[string]interface{}{
				"status": "pending", "current_phase": "queued",
				"error_msg": "", "result": "", "completed_at": nil,
				"queue_owner": "", "lease_token": "", "lease_until": nil,
				"available_at": now, "claimed_at": nil,
				"heartbeat_at": now, "updated_at": now,
			}
		} else {
			message := "task approval was denied"
			if decisionReason != "" {
				message += ": " + decisionReason
			}
			taskUpdates = map[string]interface{}{
				"status": "failed", "current_phase": "approval_denied",
				"error_msg": message, "completed_at": now,
				"queue_owner": "", "lease_token": "", "lease_until": nil,
				"available_at": nil, "heartbeat_at": now, "updated_at": now,
			}
			if err := agentos.EnsureTaskFollowupState(tx, taskID, true, now); err != nil {
				return err
			}
			if err := agentos.CancelTaskFollowups(tx, taskID, now); err != nil {
				return err
			}
		}
		taskUpdate := tx.Model(&GatewayTaskRecord{}).
			Where(
				"task_id = ? AND queue_name = ? AND status = ?",
				taskID, agentOSQueueName, "waiting_approval",
			).
			Updates(taskUpdates)
		if taskUpdate.Error != nil {
			return taskUpdate.Error
		}
		if taskUpdate.RowsAffected != 1 {
			return fmt.Errorf("%w: task approval state changed concurrently", ErrAgentOSApprovalConflict)
		}
		if !approve {
			if err := tx.Model(&approvalcore.ApprovalRequestRecord{}).
				Where("task_id = ? AND approval_id <> ? AND status IN ?", taskID, approvalID, []string{approvalcore.StatusPending, approvalcore.StatusApproved}).
				Updates(map[string]interface{}{
					"status": approvalcore.StatusCancelled, "updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		messageStatus := "pending"
		messageStatuses := []string{"waiting_approval"}
		if !approve {
			messageStatus = "failed"
			messageStatuses = []string{"pending", "running", "waiting_approval"}
		}
		messageUpdate := scopeRowsToPersistedOwner(
			tx.Model(&ChatMessage{}).
				Where(
					"task_id = ? AND status IN ?",
					taskID, messageStatuses,
				),
			taskRecord.TenantID,
			taskRecord.SubjectID,
		)
		if approve {
			messageUpdate = messageUpdate.Where("role = ?", "user")
		}
		if err := messageUpdate.Update("status", messageStatus).Error; err != nil {
			return fmt.Errorf("update approval transcript: %w", err)
		}
		return tx.
			Where("task_id = ? AND queue_name = ?", taskID, agentOSQueueName).
			First(&taskRecord).Error
	})
	if err != nil {
		return approvalcore.ApprovalRequestRecord{}, nil, err
	}
	if err := g.db.Where("approval_id = ?", approvalID).First(&approval).Error; err != nil {
		return approvalcore.ApprovalRequestRecord{}, nil, err
	}
	durableTask := gatewayTaskFromRecord(taskRecord)

	g.tasksMu.Lock()
	if approve {
		// claimOne treats any live non-terminal local task as in flight. The
		// waiting worker has already returned, so remove its closed-channel
		// snapshot and let the queue construct a fresh runtime on claim.
		delete(g.tasks, taskID)
	} else if local := g.tasks[taskID]; local != nil {
		local.Status = durableTask.Status
		local.CurrentPhase = durableTask.CurrentPhase
		local.Error = durableTask.Error
		local.CompletedAt = durableTask.CompletedAt
		local.HeartbeatAt = durableTask.HeartbeatAt
		local.QueueOwner = ""
		local.LeaseToken = ""
		local.LeaseUntil = nil
	}
	g.tasksMu.Unlock()

	if approve {
		g.ensureAgentOSQueue().Signal()
		g.broadcastTaskUpdate(taskID, "pending", nil, nil)
	} else {
		g.broadcastTaskUpdate(taskID, "failed", durableTask.Result, durableTask.Error)
	}
	return approval, durableTask, nil
}

func (g *GatewayService) suspendGatewayTaskForApproval(
	task *GatewayTask,
	required *approvalcore.ApprovalRequiredError,
) (*GatewayTask, error) {
	if g == nil || g.db == nil || task == nil || required == nil {
		return nil, fmt.Errorf("approval suspension is unavailable")
	}
	now := time.Now().Round(0)
	query := g.db.Model(&GatewayTaskRecord{}).
		Where("task_id = ? AND status = ?", task.ID, "running")
	if task.LeaseToken != "" {
		query = query.Where("lease_token = ?", task.LeaseToken)
	}
	result := query.Updates(map[string]interface{}{
		"status": "waiting_approval", "current_phase": "waiting_approval",
		"error_msg": "", "completed_at": nil,
		"queue_owner": "", "lease_token": "", "lease_until": nil,
		"available_at": nil, "claimed_at": nil,
		"heartbeat_at": now, "updated_at": now,
		"last_tool_call_id": required.Request.ToolCallID,
	})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, agentos.ErrLeaseLost
	}

	g.tasksMu.Lock()
	local := g.tasks[task.ID]
	if local == nil {
		local = task
		g.tasks[task.ID] = local
	}
	local.Status = "waiting_approval"
	local.CurrentPhase = "waiting_approval"
	local.Error = nil
	local.CompletedAt = nil
	local.HeartbeatAt = &now
	local.LastToolCallID = required.Request.ToolCallID
	local.QueueOwner = ""
	local.LeaseToken = ""
	local.LeaseUntil = nil
	local.AvailableAt = nil
	local.ClaimedAt = nil
	local.Cancel = nil
	snapshot := cloneGatewayTask(local)
	g.tasksMu.Unlock()

	_ = scopeRowsToPersistedOwner(
		g.db.Model(&ChatMessage{}).
			Where("task_id = ? AND role = ? AND status IN ?", task.ID, "user", []string{"pending", "running"}),
		task.TenantID,
		task.SubjectID,
	).
		Update("status", "waiting_approval").Error
	g.broadcastTaskUpdate(task.ID, "waiting_approval", map[string]interface{}{
		"approval_id": required.Request.ApprovalID,
		"tool_name":   required.Request.ToolName,
	}, nil)
	return snapshot, nil
}

const agentOSArtifactStoragePrefix = agentos.ArtifactStoragePrefix
const defaultAgentOSArtifactDownloadEndpoint = agentos.DefaultArtifactDownloadEndpoint

var agentOSArtifactDownloadURL = agentos.ArtifactDownloadURL
var normalizeAgentOSArtifactDownloadEndpoint = agentos.NormalizeArtifactDownloadEndpoint
var agentOSArtifactDownloadURLAt = agentos.ArtifactDownloadURLAt

func (g *GatewayService) agentOSArtifactDownloadEndpoint() string {
	if g == nil || g.config == nil {
		return defaultAgentOSArtifactDownloadEndpoint
	}
	endpoint, ok := normalizeAgentOSArtifactDownloadEndpoint(g.config.Gateway.AgentOSArtifactDownloadPath)
	if !ok || endpoint == "" {
		return defaultAgentOSArtifactDownloadEndpoint
	}
	return endpoint
}

func (g *GatewayService) publicAgentOSArtifactLinks(value string) string {
	endpoint := g.agentOSArtifactDownloadEndpoint()
	if endpoint == defaultAgentOSArtifactDownloadEndpoint {
		return value
	}
	return strings.ReplaceAll(
		value,
		defaultAgentOSArtifactDownloadEndpoint+"?path=",
		endpoint+"?path=",
	)
}

type AgentOSArtifact = agentos.Artifact

var normalizeAgentOSArtifactPath = agentos.NormalizeArtifactPath

func (g *GatewayService) agentOSTaskArtifacts(task *GatewayTask) []AgentOSArtifact {
	if g == nil || task == nil || task.Result == nil {
		return nil
	}
	workspace := strings.TrimSpace(task.ResolvedWorkspace)
	if workspace == "" && task.Request != nil {
		workspace = strings.TrimSpace(task.Request.ResolvedWorkspace)
	}
	if workspace == "" {
		return nil
	}

	resultText := gatewayResultText(task.Result)
	paths := extractArtifactPaths(resultText)
	paths = append(paths, referencedAgentOSStorageFiles(workspace, resultText)...)
	if len(paths) == 0 {
		return nil
	}
	artifacts := make([]AgentOSArtifact, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, candidate := range paths {
		path, err := normalizeAgentOSArtifactPath(candidate)
		if err != nil || seen[strings.ToLower(path)] {
			continue
		}
		normalized, err := attachmentcore.Normalize([]attachmentcore.File{{Path: path}}, workspace, true)
		if err != nil || len(normalized) != 1 {
			continue
		}
		attachment := normalized[0]
		path, err = normalizeAgentOSArtifactPath(attachment.Path)
		if err != nil {
			continue
		}
		seen[strings.ToLower(path)] = true
		artifacts = append(artifacts, AgentOSArtifact{
			Name:        attachment.Name,
			Path:        path,
			MIMEType:    attachment.MIMEType,
			Size:        attachment.Size,
			Kind:        attachment.Kind,
			DownloadURL: agentOSArtifactDownloadURLAt(path, g.agentOSArtifactDownloadEndpoint()),
		})
	}
	return artifacts
}

// referencedAgentOSStorageFiles covers a common model response shape:
// the response names ".openkitty/storage/" once and then lists bare filenames
// in a Markdown table. Only regular files that both exist in the subject's
// isolated workspace and are named verbatim in the result are returned.
var referencedAgentOSStorageFiles = agentos.ReferencedStorageFiles

func (g *GatewayService) handleAgentOSArtifactDownload(
	w http.ResponseWriter,
	r *http.Request,
	identity ExecutionIdentity,
) {
	if !executionIdentityHasAgentOSScope(identity, agentOSScopeTasksReadOwn) {
		agentos.SendStatusError(w, r, "task read permission is required", http.StatusForbidden)
		return
	}
	path, err := normalizeAgentOSArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"invalid_artifact",
			"artifact path is invalid",
			http.StatusBadRequest,
			false,
		)
		return
	}
	workspace, err := g.agentOSAttachmentWorkspace(identity)
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"workspace_unavailable",
			"tenant workspace is unavailable",
			http.StatusForbidden,
			false,
		)
		return
	}
	normalized, err := attachmentcore.Normalize([]attachmentcore.File{{Path: path}}, workspace, true)
	if err != nil || len(normalized) != 1 {
		agentos.SendJSONError(
			w,
			r,
			"artifact_not_found",
			"artifact is unavailable",
			http.StatusNotFound,
			false,
		)
		return
	}
	attachment := normalized[0]
	path, err = normalizeAgentOSArtifactPath(attachment.Path)
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"invalid_artifact",
			"artifact path is invalid",
			http.StatusBadRequest,
			false,
		)
		return
	}
	fullPath := filepath.Join(workspace, filepath.FromSlash(path))
	file, err := os.Open(fullPath)
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"artifact_not_found",
			"artifact is unavailable",
			http.StatusNotFound,
			false,
		)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		agentos.SendJSONError(
			w,
			r,
			"artifact_not_found",
			"artifact is unavailable",
			http.StatusNotFound,
			false,
		)
		return
	}

	contentType := strings.TrimSpace(attachment.MIMEType)
	if contentType == "" || contentType == "application/octet-stream" {
		if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Name))); detected != "" {
			contentType = detected
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(
		"attachment",
		map[string]string{"filename": attachment.Name},
	))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, attachment.Name, info.ModTime(), file)
}

const (
	minAgentOSCollaborationParticipants = 2
	maxAgentOSCollaborationParticipants = 8
	maxAgentOSCollaborationRounds       = 5
)

type scopedCollaborationParticipant struct {
	task  TaskConfig
	agent Agent
}

func (g *GatewayService) normalizeAgentOSCollaboration(
	input CollaborationConfig,
	callerID string,
	identity ExecutionIdentity,
) (CollaborationConfig, error) {
	normalized := CollaborationConfig{
		Type:            input.Type,
		MaxRound:        input.MaxRound,
		FailureStrategy: input.FailureStrategy,
		HostAgent:       strings.TrimSpace(input.HostAgent),
	}
	switch normalized.Type {
	case Sequential, Parallel, GroupChat, Vote, GroupChatWithHost:
	default:
		return CollaborationConfig{}, fmt.Errorf("不支持的多 Agent 协作模式")
	}
	if len(input.Tasks) < minAgentOSCollaborationParticipants ||
		len(input.Tasks) > maxAgentOSCollaborationParticipants {
		return CollaborationConfig{}, fmt.Errorf(
			"多 Agent 协作需要选择 %d–%d 个成员",
			minAgentOSCollaborationParticipants,
			maxAgentOSCollaborationParticipants,
		)
	}
	if normalized.MaxRound <= 0 {
		normalized.MaxRound = 2
	}
	if normalized.MaxRound > maxAgentOSCollaborationRounds {
		return CollaborationConfig{}, fmt.Errorf(
			"讨论轮数不能超过 %d 轮",
			maxAgentOSCollaborationRounds,
		)
	}
	switch normalized.FailureStrategy {
	case "":
		normalized.FailureStrategy = Continue
	case Stop, Continue, Retry:
	default:
		return CollaborationConfig{}, fmt.Errorf("失败处理方式无效")
	}

	taskIDs := make(map[string]struct{}, len(input.Tasks))
	agentNames := make(map[string]struct{}, len(input.Tasks))
	roleToAgent := make(map[string]string, len(input.Tasks))
	normalized.Tasks = make([]TaskConfig, 0, len(input.Tasks))
	for index, task := range input.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("member_%d", index+1)
		}
		if len(taskID) > 64 ||
			strings.Contains(taskID, "/") ||
			strings.Contains(taskID, "\\") ||
			strings.Contains(taskID, "..") {
			return CollaborationConfig{}, fmt.Errorf("第 %d 个成员的任务标识无效", index+1)
		}
		if _, exists := taskIDs[taskID]; exists {
			return CollaborationConfig{}, fmt.Errorf("多 Agent 成员的任务标识不能重复")
		}
		taskIDs[taskID] = struct{}{}

		requestedRole := strings.TrimSpace(task.Agent)
		if requestedRole == "" {
			return CollaborationConfig{}, fmt.Errorf("第 %d 个成员未指定 Agent", index+1)
		}
		_, agentName, err := g.resolveAgentOSRole(requestedRole, callerID, identity)
		if err != nil {
			return CollaborationConfig{}, fmt.Errorf(
				"成员 %q 当前不可用或无权使用",
				requestedRole,
			)
		}
		if _, exists := agentNames[agentName]; exists {
			return CollaborationConfig{}, fmt.Errorf("同一个 Agent 不能重复加入一次协作")
		}
		agentNames[agentName] = struct{}{}
		roleToAgent[requestedRole] = agentName
		roleToAgent[agentName] = agentName

		description := strings.TrimSpace(task.Description)
		if len([]rune(description)) > 500 {
			return CollaborationConfig{}, fmt.Errorf("成员职责说明不能超过 500 个字符")
		}
		alias := strings.TrimSpace(task.Alias)
		if len([]rune(alias)) > 32 || strings.IndexFunc(alias, unicode.IsControl) >= 0 {
			return CollaborationConfig{}, fmt.Errorf("第 %d 个成员的别名无效或超过 32 个字符", index+1)
		}
		normalized.Tasks = append(normalized.Tasks, TaskConfig{
			TaskID:      taskID,
			Agent:       agentName,
			Alias:       alias,
			Type:        strings.TrimSpace(task.Type),
			Description: description,
		})
	}

	if normalized.Type == GroupChatWithHost {
		hostAgent, exists := roleToAgent[normalized.HostAgent]
		if normalized.HostAgent == "" || !exists {
			return CollaborationConfig{}, fmt.Errorf("有主持讨论需要从参与成员中指定主持人")
		}
		normalized.HostAgent = hostAgent
	}
	if normalized.Type != GroupChatWithHost {
		normalized.HostAgent = ""
	}
	return normalized, nil
}

func (g *GatewayService) loadScopedCollaborationParticipants(
	config CollaborationConfig,
	workspace string,
) ([]scopedCollaborationParticipant, func(), error) {
	participants := make([]scopedCollaborationParticipant, 0, len(config.Tasks))
	cleanups := make([]func(), 0, len(config.Tasks))
	cleanupAll := func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
	for _, task := range config.Tasks {
		foundName, agent, found, err := g.engine.NewAgentForTaskInWorkspace(task.Agent, workspace)
		if err != nil {
			cleanupAll()
			return nil, func() {}, fmt.Errorf("创建协作成员 %s 失败: %w", task.Agent, err)
		}
		if !found || agent == nil {
			cleanupAll()
			return nil, func() {}, fmt.Errorf("协作成员 %s 当前不可用", task.Agent)
		}
		task.Agent = foundName
		participants = append(participants, scopedCollaborationParticipant{
			task:  task,
			agent: agent,
		})
		cleanups = append(cleanups, newAgentRuntimeCleanup(agent))
	}
	return participants, cleanupAll, nil
}

func (g *GatewayService) runScopedCollaboration(
	ctx context.Context,
	req *MessageRequest,
	participants []scopedCollaborationParticipant,
) (map[string]interface{}, error) {
	config := *req.Collaboration
	startedAt := time.Now()
	g.ObserveTaskRuntime(req.TaskID, "collaboration", "")
	g.BroadcastProgress(ProgressEvent{
		Type:      "collaboration_start",
		TaskID:    req.TaskID,
		Message:   collaborationProgressLabel(config.Type, len(participants)),
		Status:    "running",
		Timestamp: time.Now().UnixMilli(),
	})

	var (
		results     []TaskResult
		finalAnswer string
		err         error
	)
	switch config.Type {
	case Sequential:
		results, finalAnswer, err = g.runScopedSequential(ctx, req, participants)
	case Parallel:
		results, finalAnswer, err = g.runScopedParallel(ctx, req, participants, false)
	case Vote:
		results, finalAnswer, err = g.runScopedVote(ctx, req, participants)
	case GroupChat:
		results, finalAnswer, err = g.runScopedGroupDiscussion(ctx, req, participants, "")
	case GroupChatWithHost:
		results, finalAnswer, err = g.runScopedGroupDiscussion(
			ctx,
			req,
			participants,
			config.HostAgent,
		)
	default:
		err = fmt.Errorf("不支持的多 Agent 协作模式")
	}
	if err != nil {
		return nil, err
	}
	finalAnswer = strings.TrimSpace(finalAnswer)
	if finalAnswer == "" {
		return nil, fmt.Errorf("多 Agent 协作未形成可用结论")
	}
	finalAnswer = composeScopedCollaborationDelivery(
		config.Type,
		finalAnswer,
		results,
		len(participants),
	)

	failed := 0
	for _, result := range results {
		if result.Status == Failed {
			failed++
		}
	}
	participantNames := make([]string, 0, len(participants))
	for _, participant := range participants {
		participantNames = append(participantNames, scopedParticipantDisplayName(participant.task))
	}
	hostDisplayName := ""
	if config.HostAgent != "" {
		for _, participant := range participants {
			if participant.task.Agent == config.HostAgent {
				hostDisplayName = scopedParticipantDisplayName(participant.task)
				break
			}
		}
	}
	return map[string]interface{}{
		"content": finalAnswer,
		"status":  "success",
		"collaboration": map[string]interface{}{
			"type":         string(config.Type),
			"participants": participantNames,
			"max_round":    config.MaxRound,
			"host_agent":   config.HostAgent,
			"host_alias":   hostDisplayName,
		},
		"task_results":       results,
		"successful_tasks":   len(results) - failed,
		"failed_tasks":       failed,
		"execution_duration": time.Since(startedAt).Seconds(),
	}, nil
}

func composeScopedCollaborationDelivery(
	mode CollaborationType,
	finalAnswer string,
	results []TaskResult,
	participantCount int,
) string {
	finalAnswer = strings.TrimSpace(finalAnswer)
	switch mode {
	case Parallel, GroupChat, GroupChatWithHost:
	default:
		return finalAnswer
	}

	type contribution struct {
		title  string
		output string
		round  int
	}
	contributions := make([]contribution, 0, len(results))
	for index, result := range results {
		if result.Status != Completed {
			continue
		}
		output := strings.TrimSpace(fmt.Sprint(result.Output))
		if output == "" {
			continue
		}
		title := strings.TrimSpace(result.AgentName)
		if title == "" {
			title = strings.TrimSpace(result.TaskID)
		}
		if title == "" {
			title = fmt.Sprintf("成员 %d", len(contributions)+1)
		}
		round := 0
		if (mode == GroupChat || mode == GroupChatWithHost) && participantCount > 0 {
			round = index/participantCount + 1
		}
		contributions = append(contributions, contribution{
			title:  title,
			output: output,
			round:  round,
		})
	}
	if len(contributions) == 0 {
		return finalAnswer
	}

	const maxDeliveryRunes = 19000
	header := "## 综合结论\n\n"
	detailHeader := "\n\n---\n\n## 协作分析明细\n\n" +
		"以下保留各 Agent 的有效成稿，便于核对不同视角、证据和分歧。\n\n"
	baseRunes := len([]rune(header + finalAnswer + detailHeader))
	if baseRunes >= maxDeliveryRunes {
		return strictCompactScopedText(header+finalAnswer, maxDeliveryRunes)
	}

	structureRunes := 0
	previousRound := 0
	for _, item := range contributions {
		if item.round > 0 && item.round != previousRound {
			structureRunes += len([]rune(fmt.Sprintf("### 第 %d 轮\n\n", item.round)))
			previousRound = item.round
		}
		headingLevel := "###"
		if item.round > 0 {
			headingLevel = "####"
		}
		structureRunes += len([]rune(headingLevel + " " + item.title + "\n\n"))
	}
	remaining := maxDeliveryRunes - baseRunes - structureRunes
	if remaining <= 0 {
		return strictCompactScopedText(header+finalAnswer, maxDeliveryRunes)
	}
	perContribution := remaining / len(contributions)
	var output strings.Builder
	output.WriteString(header)
	output.WriteString(finalAnswer)
	output.WriteString(detailHeader)
	previousRound = 0
	for _, item := range contributions {
		if item.round > 0 && item.round != previousRound {
			output.WriteString(fmt.Sprintf("### 第 %d 轮\n\n", item.round))
			previousRound = item.round
		}
		headingLevel := "###"
		if item.round > 0 {
			headingLevel = "####"
		}
		detailBudget := perContribution - 2
		if detailBudget < 80 {
			continue
		}
		output.WriteString(headingLevel + " " + item.title + "\n\n")
		output.WriteString(compactScopedText(item.output, detailBudget))
		output.WriteString("\n\n")
	}
	return strictCompactScopedText(output.String(), maxDeliveryRunes)
}

func strictCompactScopedText(value string, maxRunes int) string {
	value = compactScopedText(value, maxRunes)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func collaborationProgressLabel(mode CollaborationType, participants int) string {
	label := map[CollaborationType]string{
		Sequential:        "正在顺序协作",
		Parallel:          "正在并行分析",
		GroupChat:         "正在多方讨论",
		Vote:              "正在投票决策",
		GroupChatWithHost: "正在进行主持讨论",
	}[mode]
	if label == "" {
		label = "正在多 Agent 协作"
	}
	return fmt.Sprintf("%s（%d 位成员）", label, participants)
}

func (g *GatewayService) generateScopedContribution(
	ctx context.Context,
	req *MessageRequest,
	participant scopedCollaborationParticipant,
	prompt string,
) TaskResult {
	displayName := scopedParticipantDisplayName(participant.task)
	result := TaskResult{
		TaskID:    participant.task.TaskID,
		AgentName: displayName,
		Status:    Failed,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}
	g.BroadcastProgress(ProgressEvent{
		Type:      "agent_start",
		TaskID:    req.TaskID,
		AgentName: displayName,
		Message:   displayName + " 正在分析",
		Status:    "running",
		Timestamp: time.Now().UnixMilli(),
	})
	attempts := 1
	if req.Collaboration.FailureStrategy == Retry {
		attempts = 2
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		result.Output, err = participant.agent.Generate(ctx, prompt)
		if err == nil && strings.TrimSpace(fmt.Sprint(result.Output)) != "" {
			result.Status = Completed
			result.Error = ""
			g.BroadcastProgress(ProgressEvent{
				Type:      "agent_complete",
				TaskID:    req.TaskID,
				AgentName: displayName,
				Message:   displayName + " 已完成",
				Status:    "success",
				Timestamp: time.Now().UnixMilli(),
			})
			return result
		}
	}
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Error = "未生成可用内容"
	}
	g.BroadcastProgress(ProgressEvent{
		Type:      "agent_error",
		TaskID:    req.TaskID,
		AgentName: displayName,
		Message:   displayName + " 未能完成",
		Status:    "error",
		Timestamp: time.Now().UnixMilli(),
	})
	return result
}

func scopedParticipantDisplayName(task TaskConfig) string {
	if alias := strings.TrimSpace(task.Alias); alias != "" {
		return alias
	}
	if name := strings.TrimSpace(task.Agent); name != "" {
		return name
	}
	return "协作成员"
}

func scopedParticipantPromptIdentity(task TaskConfig) string {
	displayName := scopedParticipantDisplayName(task)
	if strings.TrimSpace(task.Alias) == "" || displayName == strings.TrimSpace(task.Agent) {
		return displayName
	}
	return fmt.Sprintf("%s（执行角色：%s）", displayName, strings.TrimSpace(task.Agent))
}

func scopedResponsibility(task TaskConfig, fallback string) string {
	if value := strings.TrimSpace(task.Description); value != "" {
		return value
	}
	return fallback
}

func (g *GatewayService) runScopedSequential(
	ctx context.Context,
	req *MessageRequest,
	participants []scopedCollaborationParticipant,
) ([]TaskResult, string, error) {
	results := make([]TaskResult, 0, len(participants))
	history := ""
	for index, participant := range participants {
		finalStep := index == len(participants)-1
		prompt := fmt.Sprintf(`# 多 Agent 顺序协作

用户的原始任务：
%s

你是第 %d/%d 位成员：%s
本轮职责：%s

此前成员的有效贡献：
%s

要求：
- 核对此前贡献，纠错并补充，不要机械重复。
- 事实、推断与未知信息要分开表述。
- 始终回答用户的原始任务，不要介绍协作机制。`,
			req.Message,
			index+1,
			len(participants),
			scopedParticipantPromptIdentity(participant.task),
			scopedResponsibility(participant.task, "承接前序结果并从你的专业视角补充"),
			emptyScopedHistory(history),
		)
		if finalStep {
			prompt += "\n- 你是最后一位成员，请输出一份完整、清晰、直接面向用户的最终答案。"
		} else {
			prompt += "\n- 输出可供下一位成员继续加工的实质性分析。"
		}
		result := g.generateScopedContribution(ctx, req, participant, prompt)
		results = append(results, result)
		if result.Status == Failed {
			if req.Collaboration.FailureStrategy != Continue {
				return results, "", fmt.Errorf("%s 未能完成顺序协作", scopedParticipantDisplayName(participant.task))
			}
			continue
		}
		output := strings.TrimSpace(fmt.Sprint(result.Output))
		history = appendScopedHistory(history, scopedParticipantDisplayName(participant.task), output)
	}
	for index := len(results) - 1; index >= 0; index-- {
		if results[index].Status == Completed {
			return results, strings.TrimSpace(fmt.Sprint(results[index].Output)), nil
		}
	}
	return results, "", fmt.Errorf("顺序协作没有成员成功完成")
}

func (g *GatewayService) runScopedParallel(
	ctx context.Context,
	req *MessageRequest,
	participants []scopedCollaborationParticipant,
	vote bool,
) ([]TaskResult, string, error) {
	results := make([]TaskResult, len(participants))
	limit := 4
	if g.config != nil && g.config.Runtime.SemaphoreLimit > 0 {
		limit = g.config.Runtime.SemaphoreLimit
	}
	if limit > len(participants) {
		limit = len(participants)
	}
	semaphore := make(chan struct{}, limit)
	var wait sync.WaitGroup
	for index, participant := range participants {
		index, participant := index, participant
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = TaskResult{
					TaskID: participant.task.TaskID, AgentName: scopedParticipantDisplayName(participant.task),
					Status: Failed, Error: ctx.Err().Error(),
					Timestamp: time.Now().Format("2006-01-02 15:04:05"),
				}
				return
			}
			instruction := `请独立分析，不参考其他成员的答案。明确事实、依据、推断、风险和未知项。
输出一份可供主分析师汇总的实质性结论，不要介绍协作机制。`
			if vote {
				instruction = `请独立判断并投票。回答第一行必须严格写成“**我选择：选项名称**”，
随后说明理由、关键依据、风险和你放弃其他选项的原因。`
			}
			prompt := fmt.Sprintf(`# 多 Agent 独立分析

用户的原始任务：
%s

你的身份：%s
你的职责：%s

%s`,
				req.Message,
				scopedParticipantPromptIdentity(participant.task),
				scopedResponsibility(participant.task, "从独立专业视角分析并交叉验证"),
				instruction,
			)
			results[index] = g.generateScopedContribution(ctx, req, participant, prompt)
		}()
	}
	wait.Wait()
	if vote {
		return results, "", nil
	}
	contributions := successfulScopedContributions(results)
	if len(contributions) == 0 {
		return results, "", fmt.Errorf("并行协作没有成员成功完成")
	}
	finalAnswer := g.synthesizeScopedContributions(
		ctx,
		req,
		participants[0],
		contributions,
		"综合独立分析，保留关键分歧并形成一份直接面向用户的最终答案",
	)
	if strings.TrimSpace(finalAnswer) == "" {
		finalAnswer = contributions
	}
	return results, finalAnswer, nil
}

func (g *GatewayService) runScopedVote(
	ctx context.Context,
	req *MessageRequest,
	participants []scopedCollaborationParticipant,
) ([]TaskResult, string, error) {
	results, _, err := g.runScopedParallel(ctx, req, participants, true)
	if err != nil {
		return results, "", err
	}
	type voteDetail struct {
		agent  string
		choice string
		reason string
	}
	details := make([]voteDetail, 0, len(results))
	counts := make(map[string]int)
	for _, result := range results {
		if result.Status != Completed {
			continue
		}
		output := strings.TrimSpace(fmt.Sprint(result.Output))
		choice, _ := g.engine.parseVoteResponse(output)
		if strings.TrimSpace(choice) == "" {
			choice = "未明确"
		}
		counts[choice]++
		details = append(details, voteDetail{
			agent: result.AgentName, choice: choice, reason: output,
		})
	}
	if len(details) == 0 {
		return results, "", fmt.Errorf("投票协作没有形成有效票")
	}
	type voteCount struct {
		choice string
		count  int
	}
	ranking := make([]voteCount, 0, len(counts))
	for choice, count := range counts {
		ranking = append(ranking, voteCount{choice: choice, count: count})
	}
	sort.Slice(ranking, func(i, j int) bool {
		if ranking[i].count == ranking[j].count {
			return ranking[i].choice < ranking[j].choice
		}
		return ranking[i].count > ranking[j].count
	})
	var summary strings.Builder
	summary.WriteString("## 投票结果\n\n")
	summary.WriteString(fmt.Sprintf(
		"**票数最高：%s（%d/%d 票）**\n\n",
		ranking[0].choice,
		ranking[0].count,
		len(details),
	))
	if len(ranking) > 1 && ranking[0].count == ranking[1].count {
		summary.WriteString("当前最高票出现并列，不能把结果表述为已达成共识。\n\n")
	}
	summary.WriteString("### 各成员意见\n\n")
	for _, detail := range details {
		summary.WriteString(fmt.Sprintf(
			"- **%s → %s**：%s\n",
			detail.agent,
			detail.choice,
			compactScopedText(detail.reason, 800),
		))
	}
	return results, summary.String(), nil
}

func (g *GatewayService) runScopedGroupDiscussion(
	ctx context.Context,
	req *MessageRequest,
	participants []scopedCollaborationParticipant,
	hostAgentName string,
) ([]TaskResult, string, error) {
	rounds := req.Collaboration.MaxRound
	if rounds <= 0 {
		rounds = 2
	}
	hostIndex := 0
	hostDisplayName := ""
	if hostAgentName != "" {
		for index, participant := range participants {
			if participant.task.Agent == hostAgentName {
				hostIndex = index
				hostDisplayName = scopedParticipantDisplayName(participant.task)
				break
			}
		}
	}
	history := ""
	results := make([]TaskResult, 0, len(participants)*rounds)
	for round := 1; round <= rounds; round++ {
		g.BroadcastProgress(ProgressEvent{
			Type:      "collaboration_round",
			TaskID:    req.TaskID,
			AgentName: hostDisplayName,
			Message:   fmt.Sprintf("正在进行第 %d/%d 轮讨论", round, rounds),
			Status:    "running",
			Timestamp: time.Now().UnixMilli(),
		})
		for _, participant := range participants {
			prompt := fmt.Sprintf(`# 多 Agent 讨论

用户的原始任务：
%s

当前是第 %d/%d 轮。你的身份：%s
你的职责：%s

目前的讨论记录：
%s

请针对原始任务继续讨论：
- 先检查已有观点中的证据、遗漏或矛盾。
- 补充新的有效信息；可以明确赞同或反驳，但不要复述。
- 不要介绍自己，不要向用户反问协作流程。
- 本轮发言简洁、具体，并为最终结论服务。`,
				req.Message,
				round,
				rounds,
				scopedParticipantPromptIdentity(participant.task),
				scopedResponsibility(participant.task, "提出独立观点并审查其他成员的结论"),
				emptyScopedHistory(history),
			)
			result := g.generateScopedContribution(ctx, req, participant, prompt)
			results = append(results, result)
			if result.Status == Completed {
				history = appendScopedHistory(
					history,
					scopedParticipantDisplayName(participant.task),
					strings.TrimSpace(fmt.Sprint(result.Output)),
				)
			} else if req.Collaboration.FailureStrategy == Stop {
				return results, "", fmt.Errorf("%s 未能完成讨论", scopedParticipantDisplayName(participant.task))
			}
		}
	}
	if strings.TrimSpace(history) == "" {
		return results, "", fmt.Errorf("多方讨论没有形成有效内容")
	}
	synthesisRole := "主分析师"
	synthesisInstruction := "综合讨论内容，明确共识、分歧、证据缺口和最终建议"
	if hostAgentName != "" {
		synthesisRole = "主持人"
		synthesisInstruction = "作为主持人总结讨论，明确共识、重要少数意见、证据缺口和最终结论"
	}
	finalAnswer := g.synthesizeScopedContributions(
		ctx,
		req,
		participants[hostIndex],
		history,
		synthesisRole+"："+synthesisInstruction,
	)
	if strings.TrimSpace(finalAnswer) == "" {
		finalAnswer = history
	}
	return results, finalAnswer, nil
}

func (g *GatewayService) synthesizeScopedContributions(
	ctx context.Context,
	req *MessageRequest,
	synthesizer scopedCollaborationParticipant,
	contributions string,
	instruction string,
) string {
	prompt := fmt.Sprintf(`# 多 Agent 最终汇总

用户的原始任务：
%s

各成员的分析：
%s

你的任务：
%s。

要求：
- 输出直接面向用户的完整答案，不描述内部流程。
- 优先采用有证据支持且彼此一致的结论。
- 对仍有分歧或证据不足之处明确说明，不要强行达成一致。
- 删除重复内容和开发日志，保留可执行、可核验的信息。`,
		req.Message,
		compactScopedText(contributions, 24000),
		instruction,
	)
	result := g.generateScopedContribution(ctx, req, synthesizer, prompt)
	if result.Status != Completed {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(result.Output))
}

func successfulScopedContributions(results []TaskResult) string {
	var output strings.Builder
	for _, result := range results {
		if result.Status != Completed {
			continue
		}
		output.WriteString("### ")
		output.WriteString(result.AgentName)
		output.WriteString("\n")
		output.WriteString(strings.TrimSpace(fmt.Sprint(result.Output)))
		output.WriteString("\n\n")
	}
	return strings.TrimSpace(output.String())
}

func appendScopedHistory(history, agentName, output string) string {
	entry := fmt.Sprintf("### %s\n%s\n\n", agentName, output)
	return compactScopedText(history+entry, 24000)
}

func emptyScopedHistory(history string) string {
	if strings.TrimSpace(history) == "" {
		return "暂无，这是第一位发言者。"
	}
	return compactScopedText(history, 24000)
}

func compactScopedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	keepHead := maxRunes * 2 / 3
	keepTail := maxRunes - keepHead
	return string(runes[:keepHead]) +
		"\n\n……中间内容因上下文长度已压缩……\n\n" +
		string(runes[len(runes)-keepTail:])
}

func (g *GatewayService) handleAgentOSFiles(w http.ResponseWriter, r *http.Request) {
	identity, ok := g.authenticateAgentOSRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		g.handleAgentOSArtifactDownload(w, r, identity)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !executionIdentityHasAgentOSScope(identity, agentOSScopeTasksSubmit) {
		agentos.SendStatusError(w, r, "task submission permission is required", http.StatusForbidden)
		return
	}

	workspace, err := g.agentOSAttachmentWorkspace(identity)
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"workspace_unavailable",
			"tenant workspace is unavailable",
			http.StatusForbidden,
			false,
		)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, agentos.MaxMultipartBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		agentos.SendJSONError(
			w,
			r,
			"invalid_file",
			"file is missing or exceeds 25 MB",
			http.StatusBadRequest,
			false,
		)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		agentos.SendJSONError(
			w,
			r,
			"invalid_file",
			"file is required",
			http.StatusBadRequest,
			false,
		)
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > agentos.MaxUploadBytes {
		agentos.SendJSONError(
			w,
			r,
			"invalid_file",
			"file must be between 1 byte and 25 MB",
			http.StatusBadRequest,
			false,
		)
		return
	}

	attachment, err := agentos.SaveUploadedFile(
		workspace,
		header.Filename,
		file,
	)
	if err != nil {
		if errors.Is(err, agentos.ErrUploadTooLarge) ||
			errors.Is(err, agentos.ErrUploadEmpty) {
			agentos.SendJSONError(
				w,
				r,
				"invalid_file",
				"file must be between 1 byte and 25 MB",
				http.StatusBadRequest,
				false,
			)
			return
		}
		if g.logger != nil {
			g.logger.Printf("[AgentOS] save attachment failed: %v", err)
		}
		agentos.SendJSONError(
			w,
			r,
			"upload_failed",
			"file upload failed",
			http.StatusInternalServerError,
			true,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"attachment":  attachment,
		"uploaded_at": time.Now(),
	})
}

func (g *GatewayService) agentOSAttachmentWorkspace(
	identity ExecutionIdentity,
) (string, error) {
	identity = normalizeExecutionIdentity(identity)
	workspace := g.currentProjectWorkspace()
	if identity.TenantScoped {
		if g.tenantMgr == nil || !tenantcore.ValidID(identity.TenantID) {
			return "", errors.New("tenant manager is unavailable")
		}
		subjectID, err := security.ValidatePersonalSubjectID(identity.SubjectID)
		if err != nil || subjectID == "" {
			return "", errors.New("subject identity is unavailable")
		}
		workspace = projectctx.PersonalWorkspace(
			g.tenantMgr.TenantDir(identity.TenantID),
			security.PersonalPartitionID(identity.TenantID, subjectID, defaultExecutionTenantID),
		)
	}
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is unavailable")
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", fmt.Errorf("prepare upload workspace: %w", err)
	}
	return workspace, nil
}

const agentOSQueueName = "agentos"

const legacyAgentOSSubjectMigrationError = "task predates personal subject ownership; resubmit it"

var (
	ErrAgentOSTaskNotFound        = errors.New("AgentOS task was not found")
	ErrAgentOSTaskAlreadyTerminal = errors.New("AgentOS task is already terminal")
)

type agentOSQueueRuntime struct {
	gateway       *GatewayService
	instanceID    string
	mode          string
	maxRunning    int
	maxQueued     int64
	leaseDuration time.Duration
	pollInterval  time.Duration
	wake          chan struct{}

	mu            sync.Mutex
	started       bool
	ctx           context.Context
	cancel        context.CancelFunc
	loopWG        sync.WaitGroup
	activeWG      sync.WaitGroup
	active        atomic.Int64
	approvalSweep atomic.Int64
	accepting     atomic.Bool
	draining      atomic.Bool
}

type agentOSQueueStats struct {
	InstanceID      string `json:"instance_id"`
	Mode            string `json:"mode"`
	RunningLocal    int64  `json:"running_local"`
	RunningGlobal   int64  `json:"running_global"`
	QueuedGlobal    int64  `json:"queued_global"`
	WaitingApproval int64  `json:"waiting_approval"`
	MaxRunning      int    `json:"max_running"`
	MaxQueued       int64  `json:"max_queued"`
	Accepting       bool   `json:"accepting"`
	Draining        bool   `json:"draining"`
	Saturated       bool   `json:"saturated"`
	DBReady         bool   `json:"db_ready"`
}

func newAgentOSQueueRuntime(g *GatewayService) *agentOSQueueRuntime {
	maxRunning := 10
	maxQueued := int64(100)
	leaseDuration := 60 * time.Second
	pollInterval := 100 * time.Millisecond
	mode := "all"
	if g != nil && g.config != nil {
		cfg := g.config.Gateway
		if cfg.MaxTasks > 0 {
			maxRunning = cfg.MaxTasks
		}
		if cfg.MaxQueuedTasks > 0 {
			maxQueued = int64(cfg.MaxQueuedTasks)
		}
		if cfg.TaskLeaseSeconds > 0 {
			leaseDuration = time.Duration(cfg.TaskLeaseSeconds) * time.Second
		}
		if cfg.TaskPollIntervalMS > 0 {
			pollInterval = time.Duration(cfg.TaskPollIntervalMS) * time.Millisecond
		}
		mode = agentos.NormalizeMode(cfg.AgentOSMode)
	}
	if maxRunning < 1 {
		maxRunning = 1
	}
	if maxRunning > 256 {
		maxRunning = 256
	}
	if maxQueued < 1 {
		maxQueued = 1
	}
	if leaseDuration < 10*time.Second {
		leaseDuration = 10 * time.Second
	}
	if pollInterval < 25*time.Millisecond {
		pollInterval = 25 * time.Millisecond
	}
	if pollInterval > 10*time.Second {
		pollInterval = 10 * time.Second
	}
	return &agentOSQueueRuntime{
		gateway: g, instanceID: agentos.NewRuntimeInstanceID(), mode: mode,
		maxRunning: maxRunning, maxQueued: maxQueued,
		leaseDuration: leaseDuration, pollInterval: pollInterval,
		wake: make(chan struct{}, 1),
	}
}

func agentOSTaskLeaseDuration(g *GatewayService) time.Duration {
	if g != nil {
		g.agentOSQueueMu.Lock()
		q := g.agentOSQueue
		g.agentOSQueueMu.Unlock()
		if q != nil {
			return q.leaseDuration
		}
		if g.config != nil && g.config.Gateway.TaskLeaseSeconds > 0 {
			return time.Duration(g.config.Gateway.TaskLeaseSeconds) * time.Second
		}
	}
	return 60 * time.Second
}

func (g *GatewayService) ensureAgentOSQueue() *agentOSQueueRuntime {
	if g == nil {
		return nil
	}
	g.agentOSQueueMu.Lock()
	defer g.agentOSQueueMu.Unlock()
	if g.agentOSQueue == nil {
		g.agentOSQueue = newAgentOSQueueRuntime(g)
	}
	return g.agentOSQueue
}

func (q *agentOSQueueRuntime) workerEnabled() bool  { return q != nil && q.mode != "gateway" }
func (q *agentOSQueueRuntime) gatewayEnabled() bool { return q != nil && q.mode != "worker" }

func (q *agentOSQueueRuntime) Start() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.ctx, q.cancel = context.WithCancel(context.Background())
	q.started = true
	q.draining.Store(false)
	q.accepting.Store(q.gatewayEnabled())
	if q.workerEnabled() {
		q.loopWG.Add(1)
		go q.run()
	}
	q.mu.Unlock()
}

func (q *agentOSQueueRuntime) Signal() {
	if q == nil {
		return
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *agentOSQueueRuntime) BeginDrain() {
	if q == nil {
		return
	}
	q.draining.Store(true)
	q.accepting.Store(false)
}

func (q *agentOSQueueRuntime) Stop(ctx context.Context) {
	if q == nil {
		return
	}
	q.BeginDrain()
	q.mu.Lock()
	cancel := q.cancel
	started := q.started
	q.mu.Unlock()
	if !started {
		return
	}
	if cancel != nil {
		cancel()
	}
	loopDone := make(chan struct{})
	go func() {
		q.loopWG.Wait()
		close(loopDone)
	}()
	select {
	case <-loopDone:
	case <-ctx.Done():
	}

	activeDone := make(chan struct{})
	go func() {
		q.activeWG.Wait()
		close(activeDone)
	}()
	select {
	case <-activeDone:
		return
	case <-ctx.Done():
		q.requeueOwnedTasks()
		q.cancelOwnedTasks()
	}
}

func (q *agentOSQueueRuntime) run() {
	defer q.loopWG.Done()
	ticker := time.NewTicker(q.pollInterval)
	defer ticker.Stop()
	for {
		if q.ctx.Err() != nil {
			return
		}
		q.sweepExpiredApprovals()
		claimedAny := false
		for q.active.Load() < int64(q.maxRunning) && !q.draining.Load() {
			task, recovered, ok, err := q.claimOne(q.ctx)
			if err != nil {
				if q.gateway != nil && q.gateway.logger != nil && q.ctx.Err() == nil {
					q.gateway.logger.Printf("[AgentOSQueue] claim failed: %v", err)
				}
				break
			}
			if !ok {
				break
			}
			claimedAny = true
			q.startClaimedTask(task, recovered)
		}
		if claimedAny {
			continue
		}
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

func (q *agentOSQueueRuntime) sweepExpiredApprovals() {
	if q == nil || q.gateway == nil || q.gateway.db == nil {
		return
	}
	now := time.Now()
	last := q.approvalSweep.Load()
	if last != 0 && now.UnixNano()-last < int64(30*time.Second) {
		return
	}
	if !q.approvalSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	if expired, err := approvalcore.ExpireAgentOSApprovals(q.gateway.db, now); err != nil {
		if q.gateway.logger != nil {
			q.gateway.logger.Printf("[AgentOSQueue] approval expiry sweep failed: %v", err)
		}
	} else if expired > 0 && q.gateway.logger != nil {
		q.gateway.logger.Printf("[AgentOSQueue] expired %d pending approval(s)", expired)
	}
}

func (q *agentOSQueueRuntime) claimOne(ctx context.Context) (*GatewayTask, bool, bool, error) {
	if q == nil || q.gateway == nil || q.gateway.db == nil {
		return nil, false, false, fmt.Errorf("database is not initialized")
	}
	now := time.Now().Round(0)
	condition := "queue_name = ? AND ((status = ? AND (available_at IS NULL OR available_at <= ?)) OR (status = ? AND lease_until IS NOT NULL AND lease_until <= ?))"
	var candidates []GatewayTaskRecord
	if err := q.gateway.db.WithContext(ctx).
		Where(condition, agentOSQueueName, "pending", now, "running", now).
		Order("created_at ASC").Limit(16).Find(&candidates).Error; err != nil {
		return nil, false, false, err
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.SubjectID) == "" {
			var rejected bool
			err := q.gateway.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := agentos.EnsureTaskFollowupState(tx, candidate.TaskID, true, now); err != nil {
					return err
				}
				if err := agentos.CancelTaskFollowups(tx, candidate.TaskID, now); err != nil {
					return err
				}
				result := tx.Model(&GatewayTaskRecord{}).
					Where(
						"task_id = ? AND TRIM(COALESCE(subject_id, '')) = '' AND "+condition,
						candidate.TaskID,
						agentOSQueueName, "pending", now, "running", now,
					).
					Updates(map[string]interface{}{
						"status":        "failed",
						"current_phase": "identity_migration_required",
						"error_msg":     legacyAgentOSSubjectMigrationError,
						"completed_at":  now,
						"heartbeat_at":  now,
						"updated_at":    now,
						"queue_owner":   "",
						"lease_token":   "",
						"lease_until":   nil,
						"available_at":  nil,
						"claimed_at":    nil,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					return nil
				}
				rejected = true
				return tx.Model(&ChatMessage{}).
					Where(
						"task_id = ? AND tenant_id = ? AND TRIM(COALESCE(subject_id, '')) = '' AND status IN ?",
						candidate.TaskID,
						normalizePersonalOwnerTenantID(candidate.TenantID),
						[]string{"pending", "running"},
					).
					Update("status", "failed").Error
			})
			if err != nil {
				return nil, false, false, err
			}
			if rejected {
				q.gateway.broadcastTaskUpdate(
					candidate.TaskID,
					"failed",
					nil,
					errors.New(legacyAgentOSSubjectMigrationError),
				)
			}
			continue
		}
		if local, ok := q.gateway.gatewayTaskSnapshot(candidate.TaskID); ok && !isTerminalGatewayTaskStatus(local.Status) {
			continue
		}
		token := agentos.NewLeaseToken()
		leaseUntil := now.Add(q.leaseDuration)
		updates := map[string]interface{}{
			"status": "running", "current_phase": "claimed", "queue_owner": q.instanceID,
			"lease_token": token, "lease_until": leaseUntil, "claimed_at": now,
			"heartbeat_at": now, "updated_at": now, "completed_at": nil, "error_msg": "",
			"run_attempt": gorm.Expr("run_attempt + 1"),
		}
		result := q.gateway.db.WithContext(ctx).Model(&GatewayTaskRecord{}).
			Where("task_id = ? AND "+condition, candidate.TaskID, agentOSQueueName, "pending", now, "running", now).
			Updates(updates)
		if result.Error != nil {
			return nil, false, false, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		wasRecovered := candidate.Status == "running"
		var claimed GatewayTaskRecord
		if err := q.gateway.db.WithContext(ctx).Where("task_id = ? AND lease_token = ?", candidate.TaskID, token).First(&claimed).Error; err != nil {
			return nil, false, false, err
		}
		if wasRecovered {
			if _, err := agentos.RecoverTaskToolReceipts(q.gateway.db, claimed.TaskID, now); err != nil {
				return nil, false, false, err
			}
		}
		return gatewayTaskFromRecord(claimed), wasRecovered, true, nil
	}
	return nil, false, false, nil
}

func (q *agentOSQueueRuntime) startClaimedTask(task *GatewayTask, recovered bool) {
	if q == nil || task == nil || task.Request == nil {
		return
	}
	task.ResultCh = make(chan interface{}, 1)
	task.ProgressCh = make(chan map[string]interface{}, 20)
	task.InterruptCh = make(chan string, 5)
	task.FollowupCh = make(chan *agentos.TaskFollowupRecord, 5)
	q.gateway.tasksMu.Lock()
	q.gateway.tasks[task.ID] = task
	q.gateway.tasksMu.Unlock()
	storeInterruptChannel(task.Request.SessionId, task.ID, task.InterruptCh)
	q.active.Add(1)
	q.activeWG.Add(1)
	go func() {
		defer func() {
			if recoveredValue := recover(); recoveredValue != nil {
				q.failPanickedTask(task, recoveredValue)
			}
			q.active.Add(-1)
			q.activeWG.Done()
			q.Signal()
		}()
		if recovered && q.gateway.logger != nil {
			q.gateway.logger.Printf("[AgentOSQueue] reclaimed expired task %s (attempt %d)", task.ID, task.RunAttempt)
		}
		monitorCtx, stopMonitor := context.WithCancel(context.Background())
		monitorDone := make(chan struct{})
		followupDone := make(chan struct{})
		go func() {
			q.monitorLease(monitorCtx, task)
			close(monitorDone)
		}()
		go func() {
			q.pumpTaskFollowups(monitorCtx, task)
			close(followupDone)
		}()
		q.gateway.processTaskWithSession(task, task.Request.SessionId)
		stopMonitor()
		<-monitorDone
		<-followupDone
	}()
}

func (q *agentOSQueueRuntime) pumpTaskFollowups(ctx context.Context, task *GatewayTask) {
	if q == nil || q.gateway == nil || q.gateway.db == nil || task == nil || task.FollowupCh == nil {
		return
	}
	interval := q.pollInterval
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		record, claimed, err := agentos.ClaimNextTaskFollowup(
			ctx, q.gateway.db, task.ID, task.LeaseToken, time.Now(),
		)
		if err != nil && ctx.Err() == nil && q.gateway.logger != nil {
			q.gateway.logger.Printf("[AgentOSQueue] claim task follow-up failed: task=%s err=%v", task.ID, err)
		}
		if claimed {
			select {
			case task.FollowupCh <- record:
				continue
			case <-ctx.Done():
				releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = agentos.ReleaseTaskFollowupClaim(
					releaseCtx, q.gateway.db, record.FollowupID, task.LeaseToken, time.Now(),
				)
				cancel()
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (q *agentOSQueueRuntime) failPanickedTask(task *GatewayTask, recovered interface{}) {
	if q == nil || q.gateway == nil || task == nil {
		return
	}
	err := fmt.Errorf("AgentOS worker panic: %v", recovered)
	_, applied := q.gateway.finishGatewayTask(task, "failed", nil, err)
	if applied {
		q.gateway.broadcastTaskUpdate(task.ID, "failed", nil, err)
	}
}

func (q *agentOSQueueRuntime) monitorLease(ctx context.Context, task *GatewayTask) {
	interval := q.leaseDuration / 3
	if interval > 2*time.Second {
		interval = 2 * time.Second
	}
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().Round(0)
			leaseUntil := now.Add(q.leaseDuration)
			result := q.gateway.db.WithContext(ctx).Model(&GatewayTaskRecord{}).
				Where("task_id = ? AND status = ? AND lease_token = ?", task.ID, "running", task.LeaseToken).
				Updates(map[string]interface{}{
					"lease_until": leaseUntil, "heartbeat_at": now, "updated_at": now,
				})
			if result.Error != nil {
				if task.LeaseUntil != nil && now.After(*task.LeaseUntil) {
					q.markLeaseLost(task, nil)
					return
				}
				continue
			}
			if result.RowsAffected == 0 {
				var current GatewayTaskRecord
				if err := q.gateway.db.Where("task_id = ?", task.ID).First(&current).Error; err == nil {
					q.markLeaseLost(task, &current)
				} else {
					q.markLeaseLost(task, nil)
				}
				return
			}
			q.gateway.tasksMu.Lock()
			if local := q.gateway.tasks[task.ID]; local != nil && local.LeaseToken == task.LeaseToken {
				local.LeaseUntil = &leaseUntil
				local.HeartbeatAt = &now
			}
			q.gateway.tasksMu.Unlock()
		}
	}
}

func (q *agentOSQueueRuntime) markLeaseLost(task *GatewayTask, record *GatewayTaskRecord) {
	if q == nil || q.gateway == nil || task == nil {
		return
	}
	q.gateway.tasksMu.Lock()
	local := q.gateway.tasks[task.ID]
	if local == nil || local.LeaseToken != task.LeaseToken {
		q.gateway.tasksMu.Unlock()
		return
	}
	if record != nil && (isTerminalGatewayTaskStatus(record.Status) || record.Status == "waiting_approval") {
		durable := gatewayTaskFromRecord(*record)
		local.Status = durable.Status
		local.Result = durable.Result
		local.Error = durable.Error
		local.CompletedAt = durable.CompletedAt
		local.CurrentPhase = durable.CurrentPhase
	} else {
		local.Status = "stopped"
		local.CurrentPhase = "lease_lost"
		local.Error = agentos.ErrLeaseLost
		completedAt := time.Now()
		local.CompletedAt = &completedAt
	}
	cancel := local.Cancel
	q.gateway.tasksMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (q *agentOSQueueRuntime) requeueOwnedTasks() {
	if q == nil || q.gateway == nil || q.gateway.db == nil {
		return
	}
	now := time.Now().Round(0)
	_ = q.gateway.db.Model(&GatewayTaskRecord{}).
		Where("queue_name = ? AND queue_owner = ? AND status = ?", agentOSQueueName, q.instanceID, "running").
		Updates(map[string]interface{}{
			"status": "pending", "current_phase": "queued", "queue_owner": "", "lease_token": "",
			"lease_until": nil, "available_at": now, "heartbeat_at": now, "updated_at": now,
		}).Error
}

func (q *agentOSQueueRuntime) cancelOwnedTasks() {
	if q == nil || q.gateway == nil {
		return
	}
	q.gateway.tasksMu.RLock()
	cancels := make([]context.CancelFunc, 0)
	for _, task := range q.gateway.tasks {
		if task != nil && task.QueueName == agentOSQueueName && task.QueueOwner == q.instanceID && task.Cancel != nil && !isTerminalGatewayTaskStatus(task.Status) {
			cancels = append(cancels, task.Cancel)
		}
	}
	q.gateway.tasksMu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (g *GatewayService) loadAgentOSQueueTask(ctx context.Context, taskID string) (*GatewayTask, error) {
	if g == nil || g.db == nil {
		return nil, fmt.Errorf("AgentOS task database is unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrAgentOSTaskNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var record GatewayTaskRecord
	err := g.db.WithContext(ctx).
		Where("task_id = ? AND queue_name = ?", taskID, agentOSQueueName).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentOSTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load AgentOS task: %w", err)
	}
	return gatewayTaskFromRecord(record), nil
}

// loadAgentOSQueueTaskSnapshot uses the durable AgentOS row as the
// authorization boundary, then overlays newer in-process progress only while
// the same worker lease still owns that row. Database failures fail closed
// instead of being misreported as an absent task.
func (g *GatewayService) loadAgentOSQueueTaskSnapshot(
	ctx context.Context,
	taskID string,
) (*GatewayTask, error) {
	durable, err := g.loadAgentOSQueueTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return g.overlayAgentOSQueueTaskSnapshot(durable), nil
}

func (g *GatewayService) overlayAgentOSQueueTaskSnapshot(
	durable *GatewayTask,
) *GatewayTask {
	if durable == nil {
		return nil
	}
	local, ok := g.gatewayTaskSnapshot(durable.ID)
	if !ok ||
		local.QueueName != agentOSQueueName ||
		local.TenantID != durable.TenantID ||
		strings.TrimSpace(local.SubjectID) != strings.TrimSpace(durable.SubjectID) ||
		local.TenantScoped != durable.TenantScoped ||
		local.RoleID != durable.RoleID ||
		local.CallerID != durable.CallerID ||
		isTerminalGatewayTaskStatus(durable.Status) ||
		durable.Status == "waiting_approval" ||
		isTerminalGatewayTaskStatus(local.Status) ||
		(durable.LeaseToken != "" && durable.LeaseToken != local.LeaseToken) {
		return durable
	}
	return local
}

// cancelAgentOSQueueTask commits the task transition and every user-visible
// cancellation side effect as one durable operation. The caller only receives
// success after the task row, outstanding approvals, and transcript state are
// mutually consistent.
func (g *GatewayService) cancelAgentOSQueueTask(ctx context.Context, taskID string) (*GatewayTask, error) {
	if g == nil || g.db == nil {
		return nil, fmt.Errorf("AgentOS task database is unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrAgentOSTaskNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().Round(0)
	var record GatewayTaskRecord
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := agentos.EnsureTaskFollowupState(tx, taskID, true, now); err != nil {
			return fmt.Errorf("initialize AgentOS task follow-up state: %w", err)
		}
		if err := agentos.CancelTaskFollowups(tx, taskID, now); err != nil {
			return fmt.Errorf("cancel AgentOS task follow-ups: %w", err)
		}
		result := tx.Model(&GatewayTaskRecord{}).
			Where(
				"task_id = ? AND queue_name = ? AND status IN ?",
				taskID,
				agentOSQueueName,
				[]string{"pending", "running", "waiting_approval"},
			).
			Updates(map[string]interface{}{
				"status": "stopped", "current_phase": "stopped", "error_msg": "task stopped by user",
				"completed_at": now, "heartbeat_at": now, "updated_at": now,
				"lease_token": "", "lease_until": nil,
			})
		if result.Error != nil {
			return fmt.Errorf("stop AgentOS task: %w", result.Error)
		}
		if err := tx.
			Where("task_id = ? AND queue_name = ?", taskID, agentOSQueueName).
			First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentOSTaskNotFound
			}
			return fmt.Errorf("reload stopped AgentOS task: %w", err)
		}
		if result.RowsAffected == 0 {
			return ErrAgentOSTaskAlreadyTerminal
		}
		if err := tx.Model(&approvalcore.ApprovalRequestRecord{}).
			Where(
				"task_id = ? AND status IN ?",
				taskID,
				[]string{approvalcore.StatusPending, approvalcore.StatusApproved},
			).
			Updates(map[string]interface{}{
				"status": approvalcore.StatusCancelled, "updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("cancel AgentOS task approvals: %w", err)
		}
		if err := scopeRowsToPersistedOwner(
			tx.Model(&ChatMessage{}).
				Where(
					"task_id = ? AND status IN ?",
					taskID,
					[]string{"pending", "running", "waiting_approval"},
				),
			record.TenantID,
			record.SubjectID,
		).
			Update("status", "stopped").Error; err != nil {
			return fmt.Errorf("stop AgentOS task messages: %w", err)
		}
		request := gatewayTaskFromRecord(record).Request
		if err := updateBoundSkillRunTerminalWithDB(
			tx, &GatewayTask{ID: taskID, Request: request}, skillRunPhaseStopped, "task stopped by user", now,
		); err != nil {
			return fmt.Errorf("stop AgentOS Skill Run: %w", err)
		}
		return nil
	})
	durable := gatewayTaskFromRecord(record)
	if err != nil {
		if errors.Is(err, ErrAgentOSTaskAlreadyTerminal) {
			return durable, ErrAgentOSTaskAlreadyTerminal
		}
		if errors.Is(err, ErrAgentOSTaskNotFound) {
			return nil, ErrAgentOSTaskNotFound
		}
		return nil, err
	}

	var cancel context.CancelFunc
	g.tasksMu.Lock()
	if local := g.tasks[taskID]; local != nil {
		local.Status = record.Status
		local.CurrentPhase = record.CurrentPhase
		local.CompletedAt = record.CompletedAt
		local.HeartbeatAt = record.HeartbeatAt
		local.LeaseToken = record.LeaseToken
		local.LeaseUntil = record.LeaseUntil
		local.Error = durable.Error
		cancel = local.Cancel
	}
	g.tasksMu.Unlock()
	if cancel != nil {
		cancel()
	}
	g.broadcastTaskUpdate(taskID, "stopped", durable.Result, durable.Error)
	return durable, nil
}

// stopAgentOSQueueTask preserves the legacy three-value stop contract for
// internal callers. AgentOS HTTP handlers use cancelAgentOSQueueTask directly
// so database failures are not collapsed into a misleading not-found result.
func (g *GatewayService) stopAgentOSQueueTask(taskID string) (*GatewayTask, bool, bool) {
	task, err := g.cancelAgentOSQueueTask(context.Background(), taskID)
	switch {
	case err == nil:
		return task, true, true
	case errors.Is(err, ErrAgentOSTaskAlreadyTerminal):
		return task, true, false
	default:
		return nil, false, false
	}
}

func (q *agentOSQueueRuntime) databaseReady(ctx context.Context) bool {
	if q == nil || q.gateway == nil || q.gateway.db == nil {
		return false
	}
	sqlDB, err := q.gateway.db.DB()
	if err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return sqlDB.PingContext(probeCtx) == nil
}

func (q *agentOSQueueRuntime) stats(ctx context.Context) (agentOSQueueStats, error) {
	stats := agentOSQueueStats{
		InstanceID: q.instanceID, Mode: q.mode, RunningLocal: q.active.Load(),
		MaxRunning: q.maxRunning, MaxQueued: q.maxQueued,
		Accepting: q.accepting.Load(), Draining: q.draining.Load(),
		DBReady: q.databaseReady(ctx),
	}
	if q.gateway == nil || q.gateway.db == nil {
		return stats, fmt.Errorf("database is not initialized")
	}
	if err := q.gateway.db.WithContext(ctx).Model(&GatewayTaskRecord{}).
		Where("queue_name = ? AND status = ?", agentOSQueueName, "pending").Count(&stats.QueuedGlobal).Error; err != nil {
		return stats, err
	}
	if err := q.gateway.db.WithContext(ctx).Model(&GatewayTaskRecord{}).
		Where("queue_name = ? AND status = ?", agentOSQueueName, "running").Count(&stats.RunningGlobal).Error; err != nil {
		return stats, err
	}
	if err := q.gateway.db.WithContext(ctx).Model(&GatewayTaskRecord{}).
		Where("queue_name = ? AND status = ?", agentOSQueueName, "waiting_approval").Count(&stats.WaitingApproval).Error; err != nil {
		return stats, err
	}
	stats.Saturated = stats.QueuedGlobal+stats.WaitingApproval >= stats.MaxQueued
	return stats, nil
}

func (q *agentOSQueueRuntime) canSubmit(ctx context.Context) (bool, agentOSQueueStats, error) {
	stats, err := q.stats(ctx)
	if err != nil {
		return false, stats, err
	}
	if !q.gatewayEnabled() || !q.accepting.Load() || q.draining.Load() {
		return false, stats, nil
	}
	return !stats.Saturated, stats, nil
}

func (g *GatewayService) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "alive", "version": CurrentVersion,
		"uptime_sec": int(time.Since(serverStartTime).Seconds()),
	})
}

func (g *GatewayService) handleReadyz(w http.ResponseWriter, r *http.Request) {
	q := g.ensureAgentOSQueue()
	stats, err := q.stats(r.Context())
	ready := err == nil && stats.DBReady && !stats.Draining
	if err != nil && g.logger != nil {
		g.logger.Printf("[AgentOS] readiness dependency check failed: %v", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": func() string {
			if ready {
				return "ready"
			}
			return "not_ready"
		}(),
		"ready": ready, "admission_open": stats.Accepting && !stats.Saturated,
	})
}

func (g *GatewayService) handleAgentOSMetrics(w http.ResponseWriter, r *http.Request) {
	q := g.ensureAgentOSQueue()
	stats, err := q.stats(r.Context())
	dbReady := 0
	if err == nil && stats.DBReady {
		dbReady = 1
	}
	accepting := 0
	if stats.Accepting && !stats.Saturated {
		accepting = 1
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP openkitty_agentos_running_local AgentOS tasks running on this instance.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_running_local gauge\nopenkitty_agentos_running_local %d\n", stats.RunningLocal)
	fmt.Fprintf(w, "# HELP openkitty_agentos_running_global AgentOS tasks leased across the shared database.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_running_global gauge\nopenkitty_agentos_running_global %d\n", stats.RunningGlobal)
	fmt.Fprintf(w, "# HELP openkitty_agentos_queued_global AgentOS tasks waiting in the shared database.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_queued_global gauge\nopenkitty_agentos_queued_global %d\n", stats.QueuedGlobal)
	fmt.Fprintf(w, "# HELP openkitty_agentos_waiting_approval AgentOS tasks paused for an operator decision.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_waiting_approval gauge\nopenkitty_agentos_waiting_approval %d\n", stats.WaitingApproval)
	fmt.Fprintf(w, "# HELP openkitty_agentos_worker_capacity Maximum AgentOS tasks this instance can run concurrently.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_worker_capacity gauge\nopenkitty_agentos_worker_capacity %d\n", stats.MaxRunning)
	fmt.Fprintf(w, "# HELP openkitty_agentos_admission_open Whether this instance accepts new AgentOS tasks.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_admission_open gauge\nopenkitty_agentos_admission_open %d\n", accepting)
	fmt.Fprintf(w, "# HELP openkitty_agentos_database_ready Whether the shared task database is reachable.\n")
	fmt.Fprintf(w, "# TYPE openkitty_agentos_database_ready gauge\nopenkitty_agentos_database_ready %d\n", dbReady)
}

// handleAgentOSTaskEvents keeps local events low-latency while also polling the
// durable task row. A client connected to a different Pod still receives phase,
// heartbeat, lease-owner and terminal updates without sticky sessions.
func (g *GatewayService) handleAgentOSTaskEvents(w http.ResponseWriter, r *http.Request, taskID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		agentos.SendStatusError(w, r, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	identity, ok := executionIdentityFromContext(r.Context())
	if !ok {
		agentos.SendStatusError(w, r, "execution identity unavailable", http.StatusInternalServerError)
		return
	}
	releaseConnection, acquired := g.acquireAgentOSSSEConnection(identity)
	if !acquired {
		w.Header().Set("Retry-After", "1")
		agentos.SendJSONError(
			w, r, "sse_connection_limit",
			"too many active event streams for this service identity",
			http.StatusTooManyRequests, true,
		)
		return
	}
	defer releaseConnection()
	authorizedTask, _ := g.gatewayTaskSnapshotWithDB(taskID)
	canReadApprovals := executionIdentityCanReadApproval(identity, authorizedTask)
	refreshAuthorization := func(task *GatewayTask) bool {
		_, fresh, identityErr := g.resolveAgentOSRequestIdentity(r)
		if identityErr != nil {
			return false
		}
		if !executionIdentityCanReadTask(fresh, task) {
			return false
		}
		identity = fresh
		canReadApprovals = executionIdentityCanReadApproval(fresh, task)
		return true
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	clientID := boundedIdentityValue(r.URL.Query().Get("client_id"), 128)
	if clientID == "" {
		clientID = "agentos_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	registryClientID := identity.TenantID + "\x00" + identity.ServiceID + "\x00" +
		clientID + "\x00" + strconv.FormatInt(time.Now().UnixNano(), 10)
	events := make(chan Event, 256)
	g.markAgentOSTaskID(taskID)
	subscriptionKey := gatewayhttp.AgentOSSSEKey(taskID)
	g.sseMutex.Lock()
	if g.sseClients[subscriptionKey] == nil {
		g.sseClients[subscriptionKey] = make(map[string]chan Event)
	}
	g.sseClients[subscriptionKey][registryClientID] = events
	g.sseMutex.Unlock()
	defer func() {
		g.sseMutex.Lock()
		if clients := g.sseClients[subscriptionKey]; clients != nil {
			delete(clients, registryClientID)
			if len(clients) == 0 {
				delete(g.sseClients, subscriptionKey)
			}
		}
		g.sseMutex.Unlock()
	}()
	writeEvent := func(eventID, eventType string, data interface{}) bool {
		encoded, err := json.Marshal(data)
		if err != nil {
			return true
		}
		if eventID != "" {
			if _, err := fmt.Fprintf(w, "id: %s\n", eventID); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	lastEventID := boundedIdentityValue(r.Header.Get("Last-Event-ID"), 128)
	if lastEventID == "" {
		lastEventID = boundedIdentityValue(r.URL.Query().Get("last_event_id"), 128)
	}
	if !writeEvent("", "connected", map[string]interface{}{
		"task_id": taskID, "client_id": clientID,
		"replay_supported": g.agentOSEvents != nil,
	}) {
		return
	}
	replayedIDs := make(map[string]bool)
	if lastEventID != "" && g.agentOSEvents != nil {
		replay := g.agentOSEvents.After(taskID, lastEventID)
		if replay.Reset || replay.Gap {
			reason := "history_window_exceeded"
			if replay.Reset {
				reason = "history_unavailable_or_server_changed"
			}
			if !writeEvent("", "replay_reset", map[string]interface{}{
				"task_id": taskID, "reason": reason,
				"requested_event_id": lastEventID,
				"oldest_event_id":    replay.OldestID,
				"latest_event_id":    replay.LatestID,
			}) {
				return
			}
		}
		for _, record := range replay.Records {
			replayedIDs[record.ID] = true
			eventData := agentOSEventDataForApprovalAccess(json.RawMessage(record.Data), canReadApprovals)
			if !writeEvent(record.ID, record.Type, eventData) {
				return
			}
		}
	}
	poll := time.NewTicker(500 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	var lastUpdated time.Time
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			if event.ID != "" && replayedIDs[event.ID] {
				delete(replayedIDs, event.ID)
				continue
			}
			eventData := agentOSEventDataForApprovalAccess(event.Data, canReadApprovals)
			if !writeEvent(event.ID, event.Type, eventData) {
				return
			}
		case <-poll.C:
			if g.db == nil {
				continue
			}
			var record GatewayTaskRecord
			if err := g.db.
				Where("task_id = ? AND queue_name = ?", taskID, agentOSQueueName).
				First(&record).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return
				}
				continue
			}
			task := gatewayTaskFromRecord(record)
			// Long-lived streams must not retain permissions forever. Re-resolve
			// the transport credential and current scopes before every durable
			// snapshot; token rotation or scope/role revocation closes the stream.
			if !refreshAuthorization(task) {
				return
			}
			if !record.UpdatedAt.After(lastUpdated) {
				continue
			}
			lastUpdated = record.UpdatedAt
			if !writeEvent(
				"",
				"task_snapshot",
				g.agentOSTaskResponseForIdentity(task, false, identity),
			) {
				return
			}
			if isTerminalGatewayTaskStatus(record.Status) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func agentOSEventDataForApprovalAccess(data interface{}, canReadApprovals bool) interface{} {
	if canReadApprovals {
		return data
	}
	var decoded map[string]interface{}
	switch typed := data.(type) {
	case map[string]interface{}:
		decoded = typed
	case json.RawMessage:
		if json.Unmarshal(typed, &decoded) != nil {
			return data
		}
	case []byte:
		if json.Unmarshal(typed, &decoded) != nil {
			return data
		}
	default:
		return data
	}
	if strings.TrimSpace(fmt.Sprint(decoded["status"])) != "waiting_approval" {
		return data
	}
	safeData := make(map[string]interface{}, len(decoded))
	for key, value := range decoded {
		safeData[key] = value
	}
	safeData["result"] = nil
	return safeData
}

const agentOSRoleRevisionSchemaVersion = 1

// agentOSModelRevisionSnapshot deliberately excludes credentials. A queued task
// keeps the selected provider/model behavior, while the live API key can still
// be rotated without rewriting durable task records.
type agentOSModelRevisionSnapshot struct {
	Alias          string  `json:"alias"`
	Type           string  `json:"type,omitempty"`
	ModelName      string  `json:"model_name,omitempty"`
	BaseURL        string  `json:"base_url,omitempty"`
	Temperature    float64 `json:"temperature,omitempty"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	ThinkingMode   string  `json:"thinking_mode,omitempty"`
	SupportsVision bool    `json:"supports_vision,omitempty"`
}

// agentOSRoleRevisionSnapshot is the immutable execution contract captured when
// AgentOS accepts a task. Explicitly published service roles must contain a
// complete Agent and Model snapshot. Older legacy configurations without
// service_roles may still use the live-agent compatibility path.
type agentOSRoleRevisionSnapshot struct {
	SchemaVersion  int                           `json:"schema_version"`
	RoleID         string                        `json:"role_id"`
	AgentName      string                        `json:"agent_name"`
	Description    string                        `json:"description,omitempty"`
	AllowedTenants []string                      `json:"allowed_tenants,omitempty"`
	AllowedCallers []string                      `json:"allowed_callers,omitempty"`
	InputSchema    map[string]interface{}        `json:"input_schema,omitempty"`
	OutputSchema   map[string]interface{}        `json:"output_schema,omitempty"`
	Agent          *AgentConfig                  `json:"agent,omitempty"`
	Model          *agentOSModelRevisionSnapshot `json:"model,omitempty"`
}

func cloneAgentConfigForRevision(config AgentConfig) (*AgentConfig, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var cloned AgentConfig
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func (g *GatewayService) validateAgentOSRoleInput(roleID string, parameters interface{}) error {
	if g == nil || g.config == nil || len(g.config.ServiceRoles) == 0 {
		return nil
	}
	for _, role := range g.config.ServiceRoles {
		if strings.TrimSpace(role.ID) != strings.TrimSpace(roleID) {
			continue
		}
		if len(role.InputSchema) == 0 {
			return nil
		}
		if parameters == nil {
			parameters = map[string]interface{}{}
		}
		if err := agentos.ValidateJSON(role.InputSchema, parameters); err != nil {
			return fmt.Errorf("parameters do not match role input schema: %w", err)
		}
		return nil
	}
	return nil
}

// normalizeAgentOSRoleOutput validates the result against the immutable role
// contract and returns the contract-shaped value. LLM agents commonly return a
// JSON object as text; keeping that text after successful schema validation
// forces every business caller to decode result a second time. AgentOS stores
// and exposes the decoded object instead.
func normalizeAgentOSRoleOutput(task *GatewayTask, result interface{}) (interface{}, error) {
	if text, ok := result.(string); ok && agentloop.HasLeadingInternalControlLeak(text) {
		return result, agentresult.NewError(
			"agent returned internal control text instead of a user-facing answer",
			"任务未形成完整的用户交付；内部执行过程已隐藏。请重试该请求。",
		)
	}
	snapshot, err := decodeAgentOSRoleRevision(task)
	if err != nil || snapshot == nil || len(snapshot.OutputSchema) == 0 {
		return result, err
	}
	value := result
	if text, ok := result.(string); ok {
		var decoded interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err != nil {
			return result, fmt.Errorf("agent output is not valid JSON")
		}
		value = decoded
	}
	if err := agentos.ValidateJSON(snapshot.OutputSchema, value); err != nil {
		return result, fmt.Errorf("agent output does not match role output schema: %w", err)
	}
	return value, nil
}

func validateAgentOSRoleOutput(task *GatewayTask, result interface{}) error {
	_, err := normalizeAgentOSRoleOutput(task, result)
	return err
}

func modelRevisionSnapshot(alias string, config ModelConfig) *agentOSModelRevisionSnapshot {
	return &agentOSModelRevisionSnapshot{
		Alias:          strings.TrimSpace(alias),
		Type:           strings.TrimSpace(config.Type),
		ModelName:      strings.TrimSpace(config.ModelName),
		BaseURL:        strings.TrimSpace(config.BaseURL),
		Temperature:    config.Temperature,
		MaxTokens:      config.MaxTokens,
		ThinkingMode:   strings.TrimSpace(config.ThinkingMode),
		SupportsVision: config.SupportsVision,
	}
}

func (snapshot agentOSModelRevisionSnapshot) runtimeConfig(apiKey string) ModelConfig {
	return ModelConfig{
		Type:           snapshot.Type,
		APIKey:         apiKey,
		ModelName:      snapshot.ModelName,
		BaseURL:        snapshot.BaseURL,
		Temperature:    snapshot.Temperature,
		MaxTokens:      snapshot.MaxTokens,
		ThinkingMode:   snapshot.ThinkingMode,
		SupportsVision: snapshot.SupportsVision,
	}
}

func agentOSRoleSnapshotHasSensitiveString(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		raw := []byte(typed)
		return containsAccountInformation(raw)
	case []interface{}:
		for _, item := range typed {
			if agentOSRoleSnapshotHasSensitiveString(item) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range typed {
			if agentOSRoleSnapshotHasSensitiveString(item) {
				return true
			}
		}
	}
	return false
}

func canonicalAgentOSRoleRevision(snapshot agentOSRoleRevisionSnapshot) (string, string, error) {
	snapshot.SchemaVersion = agentOSRoleRevisionSchemaVersion
	if snapshot.Model != nil {
		if err := agentos.ValidateRevisionBaseURL(snapshot.Model.BaseURL); err != nil {
			return "", "", err
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", err
	}
	// Never persist a lossy execution contract. If the export account-pattern
	// detector recognizes credential material anywhere in the snapshot (for
	// example a system prompt, role description, schema default, or model URL),
	// fail this explicit publication validation instead of changing the role.
	if containsAccountInformation(data) {
		return "", "", fmt.Errorf("role revision contains sensitive credential material")
	}
	// JSON escaping can hide quote-delimited credentials from a byte-level
	// pattern (for example an api_key embedded in SystemMessage). Walk the
	// decoded string values as a second, stricter check while retaining the
	// serialized-byte comparison above as the persistence boundary.
	var serializedValue interface{}
	if err := json.Unmarshal(data, &serializedValue); err != nil {
		return "", "", fmt.Errorf("validate role revision serialization: %w", err)
	}
	if agentOSRoleSnapshotHasSensitiveString(serializedValue) {
		return "", "", fmt.Errorf("role revision contains sensitive credential material")
	}
	digest := sha256.Sum256(data)
	return "r1-" + hex.EncodeToString(digest[:]), string(data), nil
}

func (g *GatewayService) captureAgentOSRoleRevision(roleID, agentName string) (string, string, error) {
	roleID = strings.TrimSpace(roleID)
	agentName = strings.TrimSpace(agentName)
	if roleID == "" || agentName == "" {
		return "", "", fmt.Errorf("role id and agent name are required")
	}
	snapshot := agentOSRoleRevisionSnapshot{
		SchemaVersion: agentOSRoleRevisionSchemaVersion,
		RoleID:        roleID,
		AgentName:     agentName,
	}
	explicitRoles := g != nil && g.config != nil && len(g.config.ServiceRoles) > 0
	publishedRoleFound := false

	if g != nil && g.config != nil {
		for _, role := range g.config.ServiceRoles {
			if strings.TrimSpace(role.ID) != roleID {
				continue
			}
			publishedRoleFound = true
			snapshot.Description = strings.TrimSpace(role.Description)
			snapshot.AllowedTenants = append([]string(nil), role.AllowedTenants...)
			snapshot.AllowedCallers = append([]string(nil), role.AllowedCallers...)
			var err error
			snapshot.InputSchema, err = agentos.CloneJSONSchema(role.InputSchema)
			if err != nil {
				return "", "", fmt.Errorf("capture input schema: %w", err)
			}
			snapshot.OutputSchema, err = agentos.CloneJSONSchema(role.OutputSchema)
			if err != nil {
				return "", "", fmt.Errorf("capture output schema: %w", err)
			}
			break
		}
	}
	if explicitRoles && !publishedRoleFound {
		return "", "", fmt.Errorf("published service role %q is unavailable", roleID)
	}

	if g != nil && g.engine != nil {
		foundName, config, ok := g.engine.findAgentConfig(agentName)
		if ok && strings.TrimSpace(foundName) == agentName {
			// Resolve inheritance once when accepting the role contract. Keeping
			// the effective alias in both snapshots lets queued/recovered tasks
			// retain their model even if the composer selection changes later.
			config.Model = effectiveAgentModelKey(g.config, config)
			cloned, err := cloneAgentConfigForRevision(config)
			if err != nil {
				return "", "", fmt.Errorf("capture agent config: %w", err)
			}
			snapshot.Agent = cloned
			if g.config != nil {
				model, exists := g.config.ModelConfig[config.Model]
				if !exists || !isModelConfigAvailable(model) {
					return "", "", fmt.Errorf(
						"agent role %q has no usable model configuration for %q",
						roleID, config.Model,
					)
				}
				snapshot.Model = modelRevisionSnapshot(config.Model, model)
			}
		}
	}
	if explicitRoles {
		if snapshot.Agent == nil {
			return "", "", fmt.Errorf(
				"published service role %q has no immutable agent configuration",
				roleID,
			)
		}
		if snapshot.Model == nil {
			return "", "", fmt.Errorf(
				"published service role %q has no immutable model configuration",
				roleID,
			)
		}
	}

	return canonicalAgentOSRoleRevision(snapshot)
}

func decodeAgentOSRoleRevision(task *GatewayTask) (*agentOSRoleRevisionSnapshot, error) {
	if task == nil || strings.TrimSpace(task.RoleSnapshot) == "" {
		return nil, nil
	}
	var snapshot agentOSRoleRevisionSnapshot
	if err := json.Unmarshal([]byte(task.RoleSnapshot), &snapshot); err != nil {
		return nil, fmt.Errorf("decode role revision: %w", err)
	}
	if snapshot.SchemaVersion != agentOSRoleRevisionSchemaVersion {
		return nil, fmt.Errorf("unsupported role revision schema %d", snapshot.SchemaVersion)
	}
	revision, _, err := canonicalAgentOSRoleRevision(snapshot)
	if err != nil {
		return nil, err
	}
	if expected := strings.TrimSpace(task.RoleRevision); expected != "" && revision != expected {
		return nil, fmt.Errorf("role revision integrity check failed")
	}
	return &snapshot, nil
}

func (g *GatewayService) liveModelCredential(alias string) (string, bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" || g == nil {
		return "", false
	}
	if g.engine != nil && g.engine.agentFactory != nil {
		factory := g.engine.agentFactory
		factory.mu.RLock()
		config, ok := factory.modelConfigs[alias]
		if ok && config != nil {
			apiKey := config.APIKey
			factory.mu.RUnlock()
			return apiKey, true
		}
		factory.mu.RUnlock()
	}
	if g.config != nil {
		config, ok := g.config.ModelConfig[alias]
		if ok {
			return config.APIKey, true
		}
	}
	return "", false
}

func (g *GatewayService) newAgentFromRoleRevision(
	snapshot *agentOSRoleRevisionSnapshot,
	workspace string,
) (string, Agent, bool, func(), error) {
	if snapshot == nil || snapshot.Agent == nil {
		return "", nil, false, nil, nil
	}
	if g == nil || g.engine == nil || g.engine.agentFactory == nil {
		return "", nil, false, nil, fmt.Errorf("agent factory is not initialized")
	}
	if snapshot.Model == nil {
		return "", nil, false, nil, fmt.Errorf("role revision has no model snapshot")
	}
	apiKey, ok := g.liveModelCredential(snapshot.Model.Alias)
	if !ok {
		return "", nil, false, nil, fmt.Errorf(
			"model credential binding %q is unavailable",
			snapshot.Model.Alias,
		)
	}

	parentFactory := g.engine.agentFactory
	modelConfig := snapshot.Model.runtimeConfig(apiKey)
	runtimeConfig := parentFactory.globalConfig
	if runtimeConfig != nil {
		// A published AgentOS role is instantiated inside the authenticated
		// subject's workspace. Do not let an absolute process-wide memory
		// base_dir merge file-backed memory across natural people.
		personalConfig := *runtimeConfig
		personalConfig.Workspace = projectctx.NormalizeWorkspace(workspace)
		personalConfig.MemoryConfig.BaseDir = ""
		runtimeConfig = &personalConfig
	}
	return g.newAgentFromTaskModel(snapshot.AgentName, *snapshot.Agent, modelConfig, workspace, runtimeConfig, true)
}

func (g *GatewayService) resolveChatAgentForGatewayTask(
	task *GatewayTask,
	agentName, workspace string,
) (string, Agent, bool, func(), error) {
	if task != nil && task.QueueName == agentOSQueueName && strings.TrimSpace(task.RoleSnapshot) != "" {
		snapshot, err := decodeAgentOSRoleRevision(task)
		if err != nil {
			return agentName, nil, true, nil, err
		}
		if snapshot != nil && snapshot.Agent != nil {
			return g.newAgentFromRoleRevision(snapshot, workspace)
		}
	}
	if task != nil && task.QueueName != agentOSQueueName && task.webModelSelection != nil {
		if err := validateWebModelTaskSnapshot(task); err != nil {
			return agentName, nil, true, nil, err
		}
		selection := task.webModelSelection
		if selection.agent == nil || selection.agentName != agentName {
			return agentName, nil, true, nil, fmt.Errorf("web model selection agent binding mismatch")
		}
		if g == nil || g.engine == nil || g.engine.agentFactory == nil {
			return agentName, nil, true, nil, fmt.Errorf("agent factory is not initialized")
		}
		return g.newAgentFromTaskModel(selection.agentName, *selection.agent, selection.model, workspace, g.engine.agentFactory.globalConfig, false)
	}
	resolvedName, agent, found, cleanup, err := g.resolveChatAgentForTask(agentName, workspace)
	if err == nil && found && task != nil && task.QueueName == agentOSQueueName {
		if llmAgent, ok := agent.(*LLMAgent); ok {
			// Legacy durable rows may predate role snapshots. They still run
			// under the AgentOS personal boundary and must not regain shared
			// collaboration factories after construction.
			llmAgent.markTaskScopedRuntime()
		}
	}
	return resolvedName, agent, found, cleanup, err
}

func (g *GatewayService) discoverableAgentOSRoles(identity ExecutionIdentity) ([]map[string]interface{}, error) {
	if g == nil || g.engine == nil {
		return nil, fmt.Errorf("agent engine is unavailable")
	}
	callerID := agentos.NormalizeCallerID(identity.ServiceID)
	roles := make([]map[string]interface{}, 0)
	if g.config != nil && len(g.config.ServiceRoles) > 0 {
		availableAgents := g.engine.getAllAgents()
		for _, role := range g.config.ServiceRoles {
			roleID := strings.TrimSpace(role.ID)
			agentName := strings.TrimSpace(role.AgentName)
			if !role.Enabled ||
				!agentos.ServiceRoleAllows(identity.AllowedRoles, roleID) ||
				!agentos.ServiceRoleAllows(role.AllowedTenants, identity.TenantID) ||
				!agentos.ServiceRoleAllows(role.AllowedCallers, callerID) {
				continue
			}
			if _, available := availableAgents[agentName]; !available {
				continue
			}
			revision, _, err := g.captureAgentOSRoleRevision(roleID, agentName)
			if err != nil {
				return nil, err
			}
			roles = append(roles, map[string]interface{}{
				"role_id":       roleID,
				"role_revision": revision,
				"agent_name":    agentName,
				"description":   role.Description,
				"enabled":       true,
				"input_schema":  role.InputSchema,
				"output_schema": role.OutputSchema,
				"links": map[string]string{
					"submit": "/api/v1/agent/tasks",
				},
			})
		}
	} else {
		for _, agentName := range g.engine.GetAgentNames() {
			if !agentos.ServiceRoleAllows(identity.AllowedRoles, agentName) {
				continue
			}
			revision, _, err := g.captureAgentOSRoleRevision(agentName, agentName)
			if err != nil {
				return nil, err
			}
			roles = append(roles, map[string]interface{}{
				"role_id": agentName, "role_revision": revision,
				"agent_name": agentName, "enabled": true,
				"links": map[string]string{
					"submit": "/api/v1/agent/tasks",
				},
			})
		}
	}
	sort.Slice(roles, func(i, j int) bool {
		return fmt.Sprint(roles[i]["role_id"]) < fmt.Sprint(roles[j]["role_id"])
	})
	return roles, nil
}

func (g *GatewayService) handleAgentOSRoles(w http.ResponseWriter, r *http.Request) {
	identity, ok := g.authenticateAgentOSRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		agentos.SendStatusError(w, r, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	roles, err := g.discoverableAgentOSRoles(identity)
	if err != nil {
		agentos.SendStatusError(w, r, "failed to list agent roles", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"roles": roles,
		"count": len(roles),
	})
}

func injectProactiveMemoryForSession(contextPrefix *strings.Builder, sessionID string) {
	if globalKG == nil || contextPrefix == nil {
		return
	}
	if memory := globalKG.ProactiveMemoryForSession(sessionID); memory != "" {
		contextPrefix.WriteString(memory + "\n")
	}
}

type rootCodeKnowledgeStore struct {
	graph *memorycore.KnowledgeGraph
}

func (store *rootCodeKnowledgeStore) AddTriple(subject, predicate, object, source, validFrom, validTo string, confidence float64) error {
	return store.graph.AddTriple(subject, predicate, object, source, validFrom, validTo, confidence)
}

func (store *rootCodeKnowledgeStore) RecentCodeFacts(limit int) ([]developmenttools.CodeFact, error) {
	facts, err := store.graph.RecentFacts(limit)
	if err != nil {
		return nil, err
	}
	result := make([]developmenttools.CodeFact, 0, len(facts))
	for _, fact := range facts {
		result = append(result, developmenttools.CodeFact{
			Subject: fact.Subject, Predicate: fact.Predicate, Object: fact.Object,
		})
	}
	return result, nil
}

func newCodebaseTool() *developmenttools.CodebaseTool {
	return developmenttools.NewCodebaseTool(
		func() developmenttools.CodeKnowledgeStore {
			if globalKG == nil {
				return nil
			}
			return &rootCodeKnowledgeStore{graph: globalKG}
		},
		func(ctx context.Context) string {
			if globalSCM == nil {
				return ""
			}
			sessionID, _ := ctx.Value("session_id").(string)
			return globalSCM.GetForSession("project_path", sessionID)
		},
	)
}

// agentRuntimeCloser is intentionally separate from Agent: long-lived agents
// do not need a per-task cleanup contract, while ephemeral agents do.
type agentRuntimeCloser interface {
	Close()
}

func newAppProactiveEngine(agent *LLMAgent) *agentcore.ProactiveEngine {
	return agentcore.NewProactiveEngine(agent, agentcore.ProactiveConfig{
		Enabled: true, CheckInterval: 5 * time.Minute,
	}, agentcore.ProactiveDependencies{
		DiscoverTasks: func() []agentcore.ProactiveTask {
			if globalKG == nil {
				return nil
			}
			facts, _ := globalKG.AllFacts(50)
			tasks := make([]agentcore.ProactiveTask, 0)
			for _, fact := range facts {
				if fact.Source == "conversation" || strings.HasPrefix(fact.Source, "conversation:session:") {
					continue
				}
				if fact.Predicate == "pending_task" || fact.Predicate == "todo" {
					tasks = append(tasks, agentcore.ProactiveTask{
						ID: fmt.Sprintf("mem_%d", fact.ID), Description: fmt.Sprintf("%s: %s", fact.Subject, fact.Object),
						Priority: 3, CreatedAt: time.Now(), Source: "memory",
					})
				}
			}
			return tasks
		},
		RequestApproval: func(task agentcore.ProactiveTask) {
			if globalSharedState != nil {
				globalSharedState.Set("pending_approval", task)
			}
		},
	})
}

// Close releases background resources owned by this LLMAgent. It is safe to
// call more than once; Gateway cleanup paths may converge during cancellation.
func (a *LLMAgent) Close() {
	if a == nil {
		return
	}
	a.lifecycleCloseOnce.Do(func() {
		// Stop the autonomous worker first so it cannot begin another Generate
		// call while the task-local PTC server is being shut down.
		if a.proactiveEngine != nil {
			a.proactiveEngine.Stop()
		}
		if a.ptcServer != nil {
			a.ptcServer.Stop()
		}
	})
}

// newAgentRuntimeCleanup returns an idempotent cleanup closure for a temporary
// Agent. Keeping this helper generic lets future ephemeral agent types opt in
// without adding type switches at every Gateway call site.
func newAgentRuntimeCleanup(agent Agent) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if closer, ok := agent.(agentRuntimeCloser); ok {
				closer.Close()
			}
		})
	}
}
