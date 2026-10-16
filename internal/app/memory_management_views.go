package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type personalMemoryViewQuery struct {
	View, Agent, SessionID, Kind, State, Search string
	Offset, Limit                               int
}

func parsePersonalMemoryViewQuery(r *http.Request) (personalMemoryViewQuery, error) {
	var result personalMemoryViewQuery
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return result, err
	}
	for key, values := range query {
		switch key {
		case "view", "agent", "session_id", "scope", "kind", "state", "search", "offset", "limit", "knowledge_source":
		default:
			return result, errors.New("unknown memory view parameter")
		}
		if len(values) != 1 {
			return result, errors.New("duplicate memory view parameter")
		}
	}
	result.View, result.Agent, result.SessionID = query.Get("view"), query.Get("agent"), query.Get("session_id")
	result.Kind, result.State, result.Search = query.Get("kind"), query.Get("state"), query.Get("search")
	result.Offset, err = personalMemoryQueryInt(query.Get("offset"), 0)
	if err != nil {
		return result, err
	}
	result.Limit, err = personalMemoryQueryInt(query.Get("limit"), 20)
	if err != nil {
		return result, err
	}
	var scope string
	switch result.View {
	case "short", "history":
		scope = "session"
	case "long", "preferences":
		scope = "user"
	case "project":
		scope = "project"
	case "global":
		scope = "global"
	case "knowledge":
	default:
		return result, errors.New("unknown memory view")
	}
	if supplied := query.Get("scope"); supplied != "" && supplied != scope {
		return result, errors.New("memory view scope mismatch")
	}
	if result.SessionID != "" && result.View != "short" && result.View != "history" || len(result.SessionID) > 64 || len(result.Agent) > 128 || result.Offset < 0 || result.Offset > 100000 || result.Limit < 1 || result.Limit > 100 || len([]rune(result.Search)) > 100 {
		return result, errors.New("invalid memory view context or pagination")
	}
	if result.Kind != "" && result.Kind != "preference" && result.Kind != "fact" && result.Kind != "decision" && result.Kind != "experience" || result.State != "" && result.State != "active" && result.State != "candidate" {
		return result, errors.New("invalid memory view filters")
	}
	if result.View == "preferences" && result.Kind != "" && result.Kind != "preference" {
		return result, errors.New("preferences require preference kind")
	}
	if source := query.Get("knowledge_source"); source != "" {
		if result.View != "knowledge" || source != "materials" && source != "curated" && source != "all" && source != "graph" && source != "learning" && source != "rag" && source != "quotes" {
			return result, errors.New("invalid knowledge source")
		}
	}
	return result, nil
}

// View reads never initialize a runtime, create SCM directories, import legacy
// files or enqueue extraction. They use the same trusted local owner as CRUD.
func (g *GatewayService) personalMemoryViewService(agent string) (*memorycore.Service, memorycore.Owner, []string, string, error) {
	agent, names, err := g.personalMemoryAgent(agent)
	if err != nil {
		return nil, memorycore.Owner{}, names, agent, err
	}
	workspace := g.currentProjectWorkspace()
	service := memorycore.NewService(g.db, workspace, agent)
	if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil && runtime.scm != nil && runtime.scm.LastError() == nil && projectctx.SameWorkspace(runtime.scm.Workspace(), workspace) {
		if err := service.SetContextStore(runtime.scm); err != nil {
			return nil, memorycore.Owner{}, names, agent, err
		}
	}
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, ImportLegacy: false}
	return service, owner, names, agent, nil
}

func (g *GatewayService) handlePersonalMemoryView(w http.ResponseWriter, r *http.Request) {
	query, err := parsePersonalMemoryViewQuery(r)
	if err != nil {
		sendJSONError(w, "Invalid memory view, context or filters", http.StatusBadRequest)
		return
	}
	service, owner, names, agent, err := g.personalMemoryViewService(query.Agent)
	if err != nil {
		sendJSONError(w, "Memory view is unavailable for this assistant", http.StatusServiceUnavailable)
		return
	}
	sessions, err := g.personalMemorySessions(r, agent)
	if err != nil {
		sendJSONError(w, "Unable to list verified conversations", http.StatusInternalServerError)
		return
	}
	entries, supplemental := make([]memorycore.Entry, 0), make([]memorycore.WorkingReference, 0)
	viewNotes := make([]string, 0)
	var total, sourcesTotal int64
	var scope memorycore.MemoryScope
	switch query.View {
	case "short", "history":
		scope = memorycore.MemorySession
		if query.SessionID != "" {
			service, owner, err = g.personalMemoryScope(r, service, owner, agent, scope, query.SessionID)
		}
	case "long", "preferences":
		scope = memorycore.MemoryUser
	case "project":
		scope = memorycore.MemoryProject
	case "global":
		scope = memorycore.MemoryGlobal
		service, owner, err = g.personalMemoryScope(r, service, owner, agent, scope, "")
	}
	if err != nil {
		sendJSONError(w, "Conversation is unavailable in this workspace and assistant", http.StatusBadRequest)
		return
	}
	readEntries := query.View != "knowledge" && query.View != "history" && (query.View != "short" || query.SessionID != "")
	if readEntries {
		kind := query.Kind
		var excluded []string
		if query.View == "preferences" {
			kind = "preference"
		} else if query.View == "long" {
			excluded = []string{"preference"}
		}
		entries, total, err = service.PageWithFilters(r.Context(), owner, scope, kind, query.State, query.Search, query.Offset, query.Limit, excluded...)
	}
	if err == nil && query.State != "candidate" {
		switch query.View {
		case "short":
			if query.SessionID != "" {
				supplemental, err = service.WorkingReferences(r.Context(), owner)
			}
		case "knowledge":
			supplemental, sourcesTotal, err = g.personalMemoryKnowledgePage(r, agent, query.Kind, query.Search, query.Offset, query.Limit)
		case "global":
			supplemental, err = g.personalMemoryGlobalViewSources(r, names)
		case "long", "project", "preferences", "history":
			if query.View != "history" || query.SessionID != "" {
				kind := query.Kind
				if query.View == "preferences" {
					kind = "preference"
				}
				supplemental, err = g.personalMemorySourceReferences(r, agent, scope, query.SessionID, kind, query.Search)
				if err == nil && query.View == "preferences" {
					supplemental, err = g.personalMemoryProfilePreferenceSources(r, agent, supplemental)
					if err == nil {
						archive, archiveErr := g.personalMemoryLegacyPreferenceSources(r)
						if archiveErr == nil {
							supplemental = append(supplemental, archive...)
						} else {
							// A corrupt or unsafe old file must not hide valid Entries
							// and preferences proved by original user messages.
							viewNotes = append(viewNotes, "legacy_preferences_unavailable")
						}
						var limited bool
						supplemental, limited, err = g.personalMemoryStructuredPreferenceSources(r, agent, query.Search, supplemental)
						if limited {
							viewNotes = append(viewNotes, "preference_sources_limited")
						}
					}
				}
			}
		}
	}
	if err != nil {
		sendJSONError(w, "Unable to read this memory view", http.StatusInternalServerError)
		return
	}
	filtered := make([]memorycore.WorkingReference, 0, len(supplemental))
	for _, reference := range supplemental {
		// The knowledge adapter searches complete records before paging. Its
		// bounded excerpt may omit the matching text, so do not filter it again.
		if query.View != "knowledge" && (!personalMemoryReferenceInView(reference, query.View) || !memorySourceMatches(reference, query.Kind, query.Search)) {
			continue
		}
		if reference.AgentName == "" && personalMemoryReferenceHasAgent(reference) {
			reference.AgentName = agent
		}
		if personalMemoryReferenceHasAgent(reference) && reference.OwnershipVerified == nil {
			reference.OwnershipVerified = verifiedMemorySource()
		}
		if reference.SourceLocation == "" {
			reference.SourceLocation = g.personalMemoryViewSourceLocation(reference, agent)
		}
		filtered = append(filtered, reference)
	}
	entryProvenance, filtered, err := g.personalMemoryProvenance(r, agent, entries, filtered)
	if err != nil {
		sendJSONError(w, "Unable to verify memory provenance", http.StatusInternalServerError)
		return
	}
	response := map[string]interface{}{"view": query.View, "agents": names, "agent": agent, "entries": entries, "total": total, "offset": query.Offset, "limit": query.Limit,
		"sessions": sessions, "supplemental": filtered, "entry_provenance": entryProvenance, "sources_bounded": true, "view_notes": viewNotes}
	if query.View == "knowledge" {
		response["sources_total"], response["sources_offset"], response["sources_limit"] = sourcesTotal, query.Offset, query.Limit
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(response)
}

func personalMemoryReferenceHasAgent(reference memorycore.WorkingReference) bool {
	if reference.OwnershipVerified != nil && !*reference.OwnershipVerified {
		return false
	}
	switch reference.SourceType {
	case "summary", "session_context", "task_execution", "task_insight", "conversation", "user_statement", "user_feedback", "learning_knowledge", "knowledge_fact", "user_profile_constraint":
		return true
	}
	return false
}

func (g *GatewayService) personalMemoryViewSourceLocation(reference memorycore.WorkingReference, agent string) string {
	switch reference.SourceType {
	case "task_execution", "task_insight":
		return "memory_events"
	case "conversation", "user_statement", "user_feedback":
		return "chat_messages"
	case "summary":
		return "memory_summaries"
	case "learning_knowledge":
		return "knowledges"
	case "knowledge_fact":
		return personalMemoryCatalogRelativePath(g.currentProjectWorkspace(), filepath.Join(g.currentProjectWorkspace(), ".openkitty", "knowledge_graph.db"))
	case "session_context":
		if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil && runtime.scm != nil {
			return personalMemoryCatalogRelativePath(g.currentProjectWorkspace(), filepath.Join(runtime.scm.StoreDir(), "context.json"))
		}
	}
	return ""
}

func personalMemoryReferenceInView(reference memorycore.WorkingReference, view string) bool {
	switch view {
	case "short":
		return reference.SourceType == "summary" || reference.SourceType == "session_context"
	case "long":
		return reference.SourceType == "task_insight" || reference.SourceType == "learning_knowledge"
	case "preferences":
		return reference.Kind == "preference" && (reference.SourceType == "user_statement" || reference.SourceType == "knowledge_fact" || reference.SourceType == "user_profile_constraint" || reference.SourceType == "legacy_preference" || reference.SourceType == "knowledge_graph_legacy" || reference.SourceType == "knowledge_graph_fact")
	case "project":
		return reference.Kind != "preference" && (reference.SourceType == "task_insight" || reference.SourceType == "learning_knowledge" || reference.SourceType == "knowledge_fact")
	case "global":
		return reference.SourceType == "workspace_instructions" || reference.SourceType == "agent_memory"
	case "history":
		return reference.SourceType == "task_execution" || reference.SourceType == "conversation" || reference.SourceType == "user_feedback"
	case "knowledge":
		// The knowledge adapter applies its own source and ownership policy.
		return true
	}
	return false
}

func (g *GatewayService) personalMemoryLegacyPreferenceSources(r *http.Request) ([]memorycore.WorkingReference, error) {
	result := make([]memorycore.WorkingReference, 0)
	// As with legacy KG/RAG archives, do not present a shared MySQL database or
	// an externally configured SQLite location as this personal workspace.
	if !g.personalMemoryWorkspaceDatabase(r) {
		return result, nil
	}
	refs, err := memorycore.ReadLegacyPreferenceArchive(r.Context(), g.currentProjectWorkspace(), 200)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		originalKey := ref.Key
		ref.Key, ref.Value = memorySourceText("assistant", ref.Key, 256), memorySourceText("assistant", ref.Value, 1200)
		if originalKey != ref.Key || filepath.IsAbs(ref.SourceID) || memorySourceText("assistant", ref.SourceID, 2048) != ref.SourceID {
			ref.SourceID = "" // Do not turn redacted text into an invented record ID.
		}
		if ref.Key != "" && ref.Value != "" {
			result = append(result, ref)
		}
	}
	return result, nil
}

func verifiedMemorySource() *bool {
	value := true
	return &value
}

func (g *GatewayService) personalMemoryGlobalViewSources(r *http.Request, names []string) ([]memorycore.WorkingReference, error) {
	result := make([]memorycore.WorkingReference, 0)
	workspace := g.currentProjectWorkspace()
	path := filepath.Join(workspace, "AGENTS.md")
	info, statErr := os.Stat(path)
	if statErr == nil && info.Mode().IsRegular() && info.Size() <= 64<<10 && projectctx.ContainsPath(workspace, path) && projectctx.EnsureResolvedPathInside(workspace, path) == nil {
		file, err := os.Open(path)
		if err == nil {
			defer file.Close()
			info, statErr := file.Stat()
			if statErr == nil && info.Mode().IsRegular() && info.Size() <= 64<<10 {
				data, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
				if readErr == nil && len(data) <= 64<<10 {
					value := memorySourceText("assistant", string(data), 8000)
					if value != "" {
						result = append(result, memorycore.WorkingReference{ID: "workspace:AGENTS.md", Key: "工作区规则（AGENTS.md）", Value: value,
							Scope: memorycore.MemoryGlobal, Kind: "decision", SourceType: "workspace_instructions", SourceLocation: "AGENTS.md", UpdatedAt: info.ModTime(), ReadOnly: true, OwnershipVerified: verifiedMemorySource()})
					}
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, errors.New("workspace instructions unavailable")
		}
	}
	// Only configured, existing runtime assistants belonging to this workspace.
	// A shared view of their stored entries does not change runtime Recall scope.
	configured := make(map[string]bool)
	g.configWriteMu.Lock()
	if g.config != nil {
		for _, config := range g.config.Agents {
			configured[config.Name] = true
		}
	}
	g.configWriteMu.Unlock()
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, ImportLegacy: false}
	for index, name := range names {
		if index >= 50 || len(result) >= 101 {
			break
		}
		if !configured[name] || g.personalMemoryCatalogRuntime(name) == nil {
			continue
		}
		service := memorycore.NewService(g.db, workspace, name)
		entries, _, err := service.PageWithFilters(r.Context(), owner, "", "", "active", "", 0, 20)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			value := memorySourceText("assistant", entry.Value, 1200)
			if value == "" {
				continue
			}
			ref := memorycore.WorkingReference{ID: "agent-memory:" + entry.ID, SourceID: entry.ID, Key: memorySourceText("assistant", entry.Key, 256), Value: value,
				Scope: entry.Scope, Kind: entry.Kind, SourceType: "agent_memory", AgentName: name, SourceLocation: "memory_entries", CreatedAt: sourceTime(entry.CreatedAt), UpdatedAt: entry.UpdatedAt, ReadOnly: true, OwnershipVerified: verifiedMemorySource()}
			p := memorycore.EntryProvenance(entry, memorycore.ProvenanceFacts{Origin: memorycore.ProvenanceOrigin{Location: "memory_entries", RecordID: entry.ID},
				QuoteStored: entry.SourceQuote != "" || entry.ConfirmedQuote != "", RuntimeEnabled: g.personalMemoryRuntimeEnabled(name)})
			p.ID, p.RecordKind, p.ReadOnly, p.Recall.Eligibility = ref.ID, "source", true, "agent_only"
			p.Recall.Conditions = append(p.Recall.Conditions, "source_agent_only_not_shared_by_view")
			ref.Provenance = &p
			result = append(result, ref)
			if len(result) >= 101 {
				break
			}
		}
	}
	return result, nil
}

// Profile rows have no workspace, Agent or subject columns. Reject names with
// any event that cannot be proved part of this exact user's verified sessions.
// Then expose only individual constraints also supported by original messages.
func (g *GatewayService) personalMemoryProfilePreferenceSources(r *http.Request, agent string, references []memorycore.WorkingReference) ([]memorycore.WorkingReference, error) {
	if !g.db.WithContext(r.Context()).Migrator().HasTable(&memorycore.UserProfile{}) || !g.db.WithContext(r.Context()).Migrator().HasTable(&memorycore.MemoryEvent{}) {
		return references, nil
	}
	sessions, err := g.personalMemoryCatalogSessions(r, agent)
	if err != nil {
		return nil, err
	}
	var events []memorycore.MemoryEvent
	if err := g.personalMemoryCatalogEvents(r, agent, sessions).Select("id,session_id,user_name").Where("user_name <> ''").Order("created_at DESC,id DESC").Limit(50).Find(&events).Error; err != nil {
		return nil, err
	}
	byName := make(map[string]map[string]bool)
	for _, event := range events {
		if byName[event.UserName] == nil {
			byName[event.UserName] = make(map[string]bool)
		}
		byName[event.UserName][event.SessionID] = true
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		var all, verified int64
		if err := g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).Where("tenant_id = ? AND user_name = ?", defaultExecutionTenantID, name).Count(&all).Error; err != nil {
			return nil, err
		}
		if err := g.personalMemoryCatalogEvents(r, agent, sessions).Where("user_name = ?", name).Count(&verified).Error; err != nil {
			return nil, err
		}
		if all > 0 && all == verified {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return references, nil
	}
	sort.Strings(names)
	var profiles []memorycore.UserProfile
	if err := g.db.WithContext(r.Context()).Model(&memorycore.UserProfile{}).
		Select("id,user_name,created_at,updated_at,substr(constraints,1,65536) AS constraints").
		Where("tenant_id = ? AND user_name IN ?", defaultExecutionTenantID, names).Order("id ASC").Limit(51).Find(&profiles).Error; err != nil {
		return nil, err
	}
	occurrences := make(map[string]int)
	for _, profile := range profiles {
		occurrences[profile.UserName]++
	}
	for index, reference := range references {
		if reference.SourceType != "user_statement" || reference.Evidence == "" {
			continue
		}
		for _, profile := range profiles {
			if occurrences[profile.UserName] != 1 || !byName[profile.UserName][reference.SessionID] || !profileConstraintMatchesOriginal(profile, reference.Evidence) {
				continue
			}
			// Keep the actual user quote; never reveal unrelated profile fields.
			reference.ID = "profile-constraint:" + strconv.FormatUint(uint64(profile.ID), 10) + ":" + reference.SourceID
			reference.SourceID, reference.SourceType = strconv.FormatUint(uint64(profile.ID), 10), "user_profile_constraint"
			reference.Key, reference.SourceLocation, reference.AgentName = "有原话依据的已保存偏好", "user_profiles.constraints", agent
			reference.CreatedAt, reference.UpdatedAt, reference.OwnershipVerified = sourceTime(profile.CreatedAt), profile.UpdatedAt, verifiedMemorySource()
			references[index] = reference
			break
		}
	}
	return references, nil
}

func profileConstraintMatchesOriginal(profile memorycore.UserProfile, original string) bool {
	if len(profile.Constraints) > 64<<10 {
		return false
	}
	var constraints map[string]json.RawMessage
	if json.Unmarshal(profile.Constraints, &constraints) != nil {
		return false
	}
	for _, correction := range memorycore.DetectDurableUserCorrections(original) {
		key := ""
		if correction.Type == "forbidden_tool" {
			key = "forbidden"
		} else if correction.Type == "preferred_tool" {
			key = "preferred"
		}
		var values []string
		if key != "" && json.Unmarshal(constraints[key], &values) == nil {
			for _, value := range values {
				if value == correction.Value {
					return true
				}
			}
		}
	}
	var stored map[string]map[string]interface{}
	if json.Unmarshal(constraints["artifact_format_preferences"], &stored) != nil {
		return false
	}
	for _, update := range memorycore.DetectArtifactFormatPreferences(original) {
		data, _ := json.Marshal(update)
		var fields map[string]interface{}
		_ = json.Unmarshal(data, &fields)
		candidate := stored[update.Scope]
		if len(candidate) == 0 {
			continue
		}
		matches := true
		for key, value := range fields {
			if key == "scope" {
				continue
			}
			if !reflect.DeepEqual(candidate[key], value) {
				matches = false
				break
			}
		}
		if matches && len(fields) > 1 {
			return true
		}
	}
	return false
}
