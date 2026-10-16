package app

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	gatewayhttp "mac/internal/gateway/httpx"
	memorycore "mac/internal/memory"
)

type personalMemoryAction struct {
	Agent           string                 `json:"agent"`
	Action          string                 `json:"action"`
	Key             string                 `json:"key"`
	Scope           memorycore.MemoryScope `json:"scope"`
	Kind            string                 `json:"kind"`
	Value           string                 `json:"value"`
	ExpectedVersion int                    `json:"expected_version"`
	SessionID       string                 `json:"session_id,omitempty"`
}

// The ordinary Web gateway is not an authenticated multi-user API. Keep this
// personal control surface on the local machine, independent of model tools.
func personalMemoryLocalRequest(r *http.Request) bool {
	if r == nil || !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
		return false
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" {
		return false
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(remote, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	hostIP := net.ParseIP(strings.Trim(host, "[]"))
	return hostIP != nil && hostIP.IsLoopback()
}

func (g *GatewayService) personalMemoryService(agent string) (*memorycore.Service, memorycore.Owner, []string, error) {
	agent, names, err := g.personalMemoryAgent(agent)
	if err != nil {
		return nil, memorycore.Owner{}, names, err
	}
	workspace := g.currentProjectWorkspace()
	service := memorycore.NewService(g.db, workspace, agent)
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := service.SetContextStore(scm); err != nil {
		return nil, memorycore.Owner{}, names, err
	}
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, ImportLegacy: true}
	return service, owner, names, nil
}

// Resolve the existing agent without constructing a store or creating files.
// The read-only catalogue shares the management identity checks, not imports.
func (g *GatewayService) personalMemoryAgent(agent string) (string, []string, error) {
	if g == nil || g.db == nil || g.engine == nil || g.engine.agentFactory == nil {
		return "", nil, errors.New("memory service unavailable")
	}
	names := g.engine.GetAgentNames()
	if agent == "" && len(names) > 0 {
		agent = names[0]
	}
	found := false
	for _, name := range names {
		if name == agent {
			found = true
			break
		}
	}
	if !found {
		return "", names, errors.New("unknown agent")
	}
	return agent, names, nil
}

func personalMemoryQueryInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func (g *GatewayService) handlePersonalMemory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/memory/prompt-sources" {
		g.handlePersonalPromptSources(w, r)
		return
	}
	if r.URL.Path == "/api/memory/inputs" {
		g.handlePersonalMemoryInputs(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !personalMemoryLocalRequest(r) {
		sendJSONError(w, "Personal memory management is available on localhost only", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/api/memory" && r.URL.Path != "/api/memory/export" && r.URL.Path != "/api/memory/recalls" && r.URL.Path != "/api/memory/catalog" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && (r.Method != http.MethodPost || r.URL.Path != "/api/memory") {
		w.Header().Set("Allow", "GET, POST")
		if r.URL.Path != "/api/memory" {
			w.Header().Set("Allow", "GET")
		}
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		g.handlePersonalMemoryAction(w, r)
		return
	}
	if r.URL.Path == "/api/memory/catalog" {
		g.handlePersonalMemoryCatalog(w, r)
		return
	}
	if r.URL.Path == "/api/memory" && r.URL.Query().Has("view") {
		g.handlePersonalMemoryView(w, r)
		return
	}
	service, owner, names, err := g.personalMemoryService(r.URL.Query().Get("agent"))
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/api/memory/recalls" {
		traces, err := service.RecentRecallTraces(r.Context(), owner, 20)
		if err != nil {
			sendJSONError(w, "Unable to read recall records", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"traces": traces})
		return
	}
	agent := serviceAgent(names, r.URL.Query().Get("agent"))
	scope := memorycore.MemoryScope(r.URL.Query().Get("scope"))
	sessionID := r.URL.Query().Get("session_id")
	if r.URL.Path == "/api/memory/export" && scope != "" && scope != memorycore.MemoryGlobal {
		sendJSONError(w, "Export supports all durable memory or global memory", http.StatusBadRequest)
		return
	}
	// A session view without a selected conversation only discovers available
	// sessions. It does not read an unscoped working-state collection.
	if scope != memorycore.MemorySession || sessionID != "" {
		service, owner, err = g.personalMemoryScope(r, service, owner, agent, scope, sessionID)
		if err != nil {
			sendJSONError(w, "Invalid memory context: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if r.URL.Path == "/api/memory/export" {
		if err := service.Import(r.Context(), owner); err != nil {
			sendJSONError(w, "Unable to export memory", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="macs-memory.json"`)
		if err := service.Export(r.Context(), owner, w); err != nil && g.logger != nil {
			g.logger.Printf("memory export failed: %v", err)
		}
		return
	}
	offset, offsetErr := personalMemoryQueryInt(r.URL.Query().Get("offset"), 0)
	limit, limitErr := personalMemoryQueryInt(r.URL.Query().Get("limit"), 20)
	if offsetErr != nil || limitErr != nil {
		sendJSONError(w, "Invalid pagination", http.StatusBadRequest)
		return
	}
	kind, state, search := r.URL.Query().Get("kind"), r.URL.Query().Get("state"), r.URL.Query().Get("search")
	if offset < 0 || offset > 100000 || limit < 1 || limit > 100 || len([]rune(search)) > 100 || state != "" && state != "active" && state != "candidate" || kind != "" && kind != "preference" && kind != "fact" && kind != "decision" && kind != "experience" {
		sendJSONError(w, "Invalid memory filters or pagination", http.StatusBadRequest)
		return
	}
	sessions, err := g.personalMemorySessions(r, agent)
	if err != nil {
		sendJSONError(w, "Unable to list conversations", http.StatusInternalServerError)
		return
	}
	entries := make([]memorycore.Entry, 0)
	var total int64
	if scope != memorycore.MemorySession || sessionID != "" {
		entries, total, err = service.PageWithFilters(r.Context(), owner, scope, kind, state, search, offset, limit)
	}
	if err != nil {
		sendJSONError(w, "Unable to list memory: "+err.Error(), http.StatusBadRequest)
		return
	}
	supplemental := make([]memorycore.WorkingReference, 0)
	if scope == memorycore.MemorySession && sessionID != "" && state != "candidate" {
		references, readErr := service.WorkingReferences(r.Context(), owner)
		if readErr != nil {
			sendJSONError(w, "Unable to read working references", http.StatusInternalServerError)
			return
		}
		for _, reference := range references {
			if memorySourceMatches(reference, kind, search) {
				supplemental = append(supplemental, reference)
			}
		}
	}
	if scope != memorycore.MemoryGlobal && (scope != memorycore.MemorySession || sessionID != "") && state != "candidate" {
		sources, readErr := g.personalMemorySourceReferences(r, agent, scope, sessionID, kind, search)
		if readErr != nil {
			sendJSONError(w, "Unable to read verified memory sources", http.StatusInternalServerError)
			return
		}
		supplemental = append(supplemental, sources...)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"agents": names, "agent": agent, "entries": entries, "total": total, "offset": offset, "limit": limit, "sessions": sessions, "supplemental": supplemental, "sources_bounded": true})
}

func serviceAgent(names []string, requested string) string {
	if requested != "" {
		return requested
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

func (g *GatewayService) handlePersonalMemoryAction(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		sendJSONError(w, "JSON request required", http.StatusUnsupportedMediaType)
		return
	}
	// 8000 Unicode characters plus a 256-character key fit in 64 KiB when
	// submitted as normal UTF-8 JSON. Keep the body bound and strict schema.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var request personalMemoryAction
	if err := decoder.Decode(&request); err != nil {
		sendJSONError(w, "Invalid memory action", http.StatusBadRequest)
		return
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		sendJSONError(w, "Invalid memory action", http.StatusBadRequest)
		return
	}
	request.Key = strings.TrimSpace(request.Key)
	if request.Agent == "" || request.Key == "" || (request.Scope != memorycore.MemoryUser && request.Scope != memorycore.MemoryProject && request.Scope != memorycore.MemoryGlobal && request.Scope != memorycore.MemorySession) || len([]rune(request.Key)) > 256 {
		sendJSONError(w, "Invalid memory key, scope or version", http.StatusBadRequest)
		return
	}
	if request.Action != "create" && request.Action != "confirm" && request.Action != "correct" && request.Action != "forget" && request.Action != "pin" && request.Action != "unpin" {
		sendJSONError(w, "Unknown memory action", http.StatusBadRequest)
		return
	}
	if request.Action == "create" {
		if request.ExpectedVersion != 0 || strings.TrimSpace(request.Value) == "" || len([]rune(request.Value)) > 8000 || request.Kind != "preference" && request.Kind != "fact" && request.Kind != "decision" && request.Kind != "experience" {
			sendJSONError(w, "Invalid new memory", http.StatusBadRequest)
			return
		}
	} else if request.ExpectedVersion < 1 {
		sendJSONError(w, "Invalid memory version", http.StatusBadRequest)
		return
	}
	service, owner, _, err := g.personalMemoryService(request.Agent)
	if err != nil {
		sendJSONError(w, "Unknown agent or memory unavailable", http.StatusNotFound)
		return
	}
	service, owner, err = g.personalMemoryScope(r, service, owner, request.Agent, request.Scope, request.SessionID)
	if err != nil {
		sendJSONError(w, "Invalid memory context: "+err.Error(), http.StatusBadRequest)
		return
	}
	if request.Action == "create" {
		if request.Scope == memorycore.MemorySession {
			err = service.CreateSessionNote(r.Context(), owner, request.Key, request.Value, request.Kind)
		} else {
			err = service.CreateWithSource(r.Context(), owner, request.Key, request.Value, request.Scope, request.Kind, 5, nil, memorycore.WriteSource{Type: "user_explicit", Ref: "local-memory-manager"})
		}
		if errors.Is(err, memorycore.ErrMemoryKeyExists) {
			sendJSONError(w, "此范围内的记忆键已存在，请更正现有记忆或换一个键", http.StatusConflict)
			return
		}
		if err != nil {
			sendJSONError(w, "Unable to create memory", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	current, err := service.Lookup(r.Context(), owner, request.Key, request.Scope)
	if err != nil {
		sendJSONError(w, "Unable to read memory", http.StatusInternalServerError)
		return
	}
	if current == nil || current.Version != request.ExpectedVersion {
		sendJSONError(w, "Memory changed; refresh and retry", http.StatusConflict)
		return
	}
	switch request.Action {
	case "pin", "unpin":
		err = service.SetPinned(r.Context(), owner, request.Key, request.Scope, request.ExpectedVersion, request.Action == "pin")
	case "confirm":
		_, err = service.ConfirmWithSource(r.Context(), owner, request.Key, request.Scope, request.ExpectedVersion, memorycore.WriteSource{Ref: "local-memory-manager"})
	case "correct":
		if strings.TrimSpace(request.Value) == "" || len([]rune(request.Value)) > 8000 {
			sendJSONError(w, "Value must be 1–8000 characters", http.StatusBadRequest)
			return
		}
		var tags []string
		_ = json.Unmarshal([]byte(current.Tags), &tags)
		err = service.RecordWithSource(r.Context(), owner, request.Key, request.Value, request.Scope, current.Kind, current.State, current.Priority, tags, request.ExpectedVersion, memorycore.WriteSource{Type: "user_explicit", Ref: "local-memory-manager"})
	case "forget":
		_, err = service.ForgetExpected(r.Context(), owner, request.Key, request.Scope, request.ExpectedVersion)
	}
	if err != nil {
		message := "记忆已变化，请刷新后重试"
		if request.Action == "pin" {
			message = "仅可常驻已生效的个人或全局偏好，每个范围最多 5 条；记忆变化后请刷新重试"
		}
		sendJSONError(w, message, http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
