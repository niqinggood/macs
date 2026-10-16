package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type personalMemoryCatalogStorage struct {
	Kind   string   `json:"kind"`
	Tables []string `json:"tables"`
	Paths  []string `json:"paths"`
}

type personalMemoryCatalogRecall struct {
	Mode                 string   `json:"mode"`
	Enabled              *bool    `json:"enabled"`
	Tools                []string `json:"tools"`
	RequiresRuntimeCheck bool     `json:"requires_runtime_check"`
}

type personalMemoryCatalogCounts struct {
	Verified   *int64 `json:"verified"`
	Unverified *int64 `json:"unverified"`
	Unit       string `json:"unit"`
	Bounded    bool   `json:"bounded"`
}

type personalMemoryCatalogSource struct {
	ID        string                       `json:"id"`
	Storage   personalMemoryCatalogStorage `json:"storage"`
	Available bool                         `json:"available"`
	Status    string                       `json:"status"`
	Recall    personalMemoryCatalogRecall  `json:"recall"`
	Counts    personalMemoryCatalogCounts  `json:"counts"`
	NoteCodes []string                     `json:"note_codes"`
}

type personalMemoryCatalog struct {
	Agent            string                        `json:"agent"`
	Agents           []string                      `json:"agents"`
	Scope            string                        `json:"scope"`
	ReadOnly         bool                          `json:"read_only"`
	SessionsVerified int                           `json:"sessions_verified"`
	CountsBounded    bool                          `json:"counts_bounded"`
	Sources          []personalMemoryCatalogSource `json:"sources"`
}

func catalogCount(value int64) *int64 { return &value }

// All locations are relative to the current workspace. Neither a configured
// external database path nor a symlink/junction destination is disclosed.
func personalMemoryCatalogRelativePath(workspace, target string) string {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(target) == "" || !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return ""
	}
	base, baseErr := filepath.Abs(workspace)
	path, pathErr := filepath.Abs(target)
	if baseErr != nil || pathErr != nil {
		return ""
	}
	relative, err := filepath.Rel(base, path)
	if err != nil || relative == "." || strings.ContainsAny(relative, "\r\n\x00") {
		return ""
	}
	return filepath.ToSlash(relative)
}

func (g *GatewayService) personalMemoryCatalogDatabaseStorage(r *http.Request) (personalMemoryCatalogStorage, []string) {
	storage := personalMemoryCatalogStorage{Kind: g.db.Dialector.Name(), Tables: []string{}, Paths: []string{}}
	if storage.Kind != "sqlite" {
		// Never inspect or serialize a MySQL DSN, host, database or connection.
		return storage, nil
	}
	var rows []struct {
		Name string
		File string
	}
	if err := g.db.WithContext(r.Context()).Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
		return storage, []string{"storage_unavailable"}
	}
	for _, row := range rows {
		if row.Name != "main" || row.File == "" {
			continue
		}
		if relative := personalMemoryCatalogRelativePath(g.currentProjectWorkspace(), row.File); relative != "" {
			storage.Paths = append(storage.Paths, relative)
		} else {
			return storage, []string{"custom_sqlite_file"}
		}
	}
	return storage, nil
}

func catalogSource(id, unit, mode string, storage personalMemoryCatalogStorage, tables []string, tools []string, enabled *bool, notes ...string) personalMemoryCatalogSource {
	storage.Tables = tables
	return personalMemoryCatalogSource{ID: id, Storage: storage, Status: "ready", Recall: personalMemoryCatalogRecall{Mode: mode, Enabled: enabled, Tools: tools, RequiresRuntimeCheck: true},
		Counts: personalMemoryCatalogCounts{Unit: unit, Bounded: true}, NoteCodes: notes}
}

func catalogUnavailable(source *personalMemoryCatalogSource, note string) {
	source.Available, source.Status = false, "unavailable"
	source.Counts.Verified, source.Counts.Unverified = nil, nil
	source.NoteCodes = append(source.NoteCodes, note)
}

func (g *GatewayService) catalogTable(r *http.Request, source *personalMemoryCatalogSource, model interface{}) bool {
	if !g.db.WithContext(r.Context()).Migrator().HasTable(model) {
		catalogUnavailable(source, "table_missing")
		return false
	}
	source.Available = true
	return true
}

// The existing runtime is inspected, never lazily constructed. A directory GET
// must not mkdir SCM, import JSON memories, enqueue work or run a model.
func (g *GatewayService) personalMemoryCatalogRuntime(agent string) *LLMAgent {
	value, exists := g.engine.agentFactory.GetAgent(agent)
	runtime, ok := value.(*LLMAgent)
	if !exists || !ok || runtime == nil || !projectctx.SameWorkspace(runtime.workspace, g.currentProjectWorkspace()) {
		return nil
	}
	return runtime
}

func (g *GatewayService) personalMemoryCatalogSessions(r *http.Request, agent string) ([]ChatSession, error) {
	sessions := make([]ChatSession, 0)
	if !g.db.WithContext(r.Context()).Migrator().HasTable(&ChatSession{}) {
		return sessions, nil
	}
	var rows []ChatSession
	if err := g.personalMemorySessionQuery(r, agent).Select("id,user_id,workspace").Order("updated_at DESC,id ASC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Workspace != "" && projectctx.SameWorkspace(row.Workspace, g.currentProjectWorkspace()) {
			sessions = append(sessions, row)
		}
	}
	return sessions, nil
}

func (g *GatewayService) personalMemoryCatalogEvents(r *http.Request, agent string, sessions []ChatSession) *gorm.DB {
	query := g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, agent).
		Where("(user_channel = '' OR user_channel IS NULL OR user_channel = ?) AND status IN ?", "web", []string{"success", "failed"})
	if len(sessions) == 0 {
		return query.Where("1 = 0")
	}
	clauses := make([]string, 0, len(sessions))
	values := make([]interface{}, 0, len(sessions)*2)
	for _, session := range sessions {
		clauses = append(clauses, "(session_id = ? AND user_id = ?)")
		values = append(values, session.ID, session.UserId)
	}
	return query.Where("("+strings.Join(clauses, " OR ")+")", values...)
}

func (g *GatewayService) handlePersonalMemoryCatalog(w http.ResponseWriter, r *http.Request) {
	query, err := httpRequestCatalogQuery(r)
	if err != nil {
		sendJSONError(w, "The memory catalogue accepts only one agent parameter", http.StatusBadRequest)
		return
	}
	agent, names, err := g.personalMemoryAgent(query)
	if err != nil {
		sendJSONError(w, "Memory catalogue is unavailable for this assistant", http.StatusServiceUnavailable)
		return
	}
	sessions, err := g.personalMemoryCatalogSessions(r, agent)
	if err != nil {
		sendJSONError(w, "Unable to verify memory catalogue scope", http.StatusInternalServerError)
		return
	}
	storage, storageNotes := g.personalMemoryCatalogDatabaseStorage(r)
	runtime := g.personalMemoryCatalogRuntime(agent)
	var enabled *bool
	if runtime != nil {
		value := runtime.hasMemory
		enabled = &value
	}
	result := personalMemoryCatalog{Agent: agent, Agents: names, Scope: "current_personal_workspace", ReadOnly: true, SessionsVerified: len(sessions), CountsBounded: true, Sources: []personalMemoryCatalogSource{}}
	sources := []personalMemoryCatalogSource{
		catalogSource("events", "records", "automatic", storage, []string{"memory_events"}, []string{"search_history"}, enabled, "verified_web_sessions", "bounded_recent_scope", "active_relevance_budget"),
		catalogSource("profiles", "records", "none", storage, []string{"user_profiles"}, []string{}, nil, "profile_scope_unverified", "profile_defaults_not_preferences", "web_profile_original_statements_only"),
		catalogSource("knowledge", "records", "automatic", storage, []string{"knowledges"}, []string{"knowledge"}, enabled, "verified_web_sessions", "bounded_recent_scope", "active_relevance_budget"),
		catalogSource("tool_usage", "calls", "automatic", storage, []string{"memory_events", "tool_skill_stats"}, []string{"get_stats"}, enabled, "tool_usage_from_verified_events", "bounded_recent_scope", "web_tool_receipts_only", "active_relevance_budget"),
		catalogSource("entries", "records", "automatic", storage, []string{"memory_entries"}, []string{"agent_memory"}, enabled, "workspace_shared", "active_relevance_budget", "bounded_recent_scope"),
		catalogSource("summaries", "records", "automatic", storage, []string{"memory_summaries"}, []string{"agent_memory"}, enabled, "verified_web_sessions", "session_only", "summary_threshold", "active_relevance_budget", "bounded_recent_scope"),
		catalogSource("session_context", "items", "automatic", personalMemoryCatalogStorage{Kind: "files", Tables: []string{}, Paths: []string{}}, []string{}, []string{"session_context"}, enabled, "session_only", "bounded_recent_scope", "active_relevance_budget"),
		catalogSource("knowledge_graph", "records", "automatic", personalMemoryCatalogStorage{Kind: "sqlite", Tables: []string{}, Paths: []string{}}, []string{"triples", "fact_evidence", "verbatim_memories", "entity_aliases"}, []string{}, enabled, "kg_evidence_required", "verified_web_sessions", "bounded_recent_scope", "active_relevance_budget"),
		catalogSource("learn_files", "files", "on_demand", personalMemoryCatalogStorage{Kind: "files", Tables: []string{}, Paths: []string{}}, []string{}, []string{"learn"}, nil, "learn_files_not_connected", "learn_legacy_directory", "runtime_check_required"),
		catalogSource("rag", "records", "on_demand", storage, []string{"rag_kbs", "rag_chunks"}, []string{"knowledge_query", "knowledge_base"}, nil, "rag_separate_permissions", "runtime_check_required"),
	}
	for i := range sources {
		if sources[i].Storage.Kind == storage.Kind && len(sources[i].Storage.Tables) > 0 && sources[i].ID != "knowledge_graph" {
			sources[i].NoteCodes = append(sources[i].NoteCodes, storageNotes...)
		}
		if enabled != nil && !*enabled && sources[i].Recall.Mode == "automatic" {
			sources[i].NoteCodes = append(sources[i].NoteCodes, "memory_disabled")
		}
		if enabled == nil && sources[i].Recall.Mode == "automatic" {
			sources[i].NoteCodes = append(sources[i].NoteCodes, "runtime_check_required")
		}
	}
	g.personalMemoryCatalogLegacy(r, agent, sessions, sources)
	g.personalMemoryCatalogManaged(r, agent, sessions, runtime, sources)
	g.personalMemoryCatalogFiles(r, agent, sessions, runtime, sources)
	if g.personalMemoryWorkspaceDatabase(r) {
		if refs, archiveErr := g.personalMemoryLegacyPreferenceSources(r); archiveErr == nil {
			sources[6].Counts.Unverified = catalogCount(int64(len(refs)))
			sources[6].NoteCodes = append(sources[6].NoteCodes, "legacy_context_archive")
		}
	}
	result.Sources = sources
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(result)
}

func httpRequestCatalogQuery(r *http.Request) (string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	for key, values := range query {
		if key != "agent" || len(values) != 1 || len(values[0]) > 128 {
			return "", os.ErrInvalid
		}
	}
	return query.Get("agent"), nil
}

func (g *GatewayService) personalMemoryCatalogLegacy(r *http.Request, agent string, sessions []ChatSession, sources []personalMemoryCatalogSource) {
	events, profiles, knowledge, tools := &sources[0], &sources[1], &sources[2], &sources[3]
	if g.catalogTable(r, events, &memorycore.MemoryEvent{}) {
		var count int64
		if err := g.personalMemoryCatalogEvents(r, agent, sessions).Count(&count).Error; err != nil {
			catalogUnavailable(events, "storage_unavailable")
		} else {
			events.Counts.Verified = catalogCount(count)
			// Even a count of orphan rows has no workspace/subject proof. Do not
			// disclose tenant+Agent aggregates as a personal unverified total.
			events.NoteCodes = append(events.NoteCodes, "incomplete_ownership")
		}
	}
	if g.catalogTable(r, profiles, &memorycore.UserProfile{}) {
		// tenant+username cannot prove an entire legacy profile's workspace or
		// subject. Do not read its content, nor count all tenants as personal.
		profiles.Status = "unverified"
	}
	if g.catalogTable(r, knowledge, &memorycore.Knowledge{}) {
		if events.Available {
			var count int64
			err := g.db.WithContext(r.Context()).Model(&memorycore.Knowledge{}).
				Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, agent).
				Where("source_id IN (?)", g.personalMemoryCatalogEvents(r, agent, sessions).Select("CAST(id AS CHAR)")).Count(&count).Error
			if err != nil {
				catalogUnavailable(knowledge, "storage_unavailable")
			} else {
				knowledge.Counts.Verified = catalogCount(count)
			}
		} else {
			knowledge.Status = "unverified"
		}
		knowledge.NoteCodes = append(knowledge.NoteCodes, "incomplete_ownership")
	}
	if events.Available {
		var rows []memorycore.MemoryEvent
		err := g.personalMemoryCatalogEvents(r, agent, sessions).Select("substr(call_chain,1,1048576) AS call_chain").Order("created_at DESC,id DESC").Limit(50).Find(&rows).Error
		if err != nil {
			catalogUnavailable(tools, "storage_unavailable")
		} else {
			var calls int64
			for _, event := range rows {
				calls += int64(len(memorySourceTools(event.CallChain)))
			}
			tools.Available, tools.Counts.Verified = true, catalogCount(calls)
		}
	} else {
		catalogUnavailable(tools, "table_missing")
	}
	// RAG remains on its own authorization path. Table existence does not make
	// a tenant-wide knowledge library readable from personal memory management.
	if g.db.WithContext(r.Context()).Migrator().HasTable("rag_kbs") && g.db.WithContext(r.Context()).Migrator().HasTable("rag_chunks") {
		sources[9].Available, sources[9].Status = true, "unverified"
		if g.personalMemoryWorkspaceDatabase(r) {
			refs, err := g.personalMemoryRAGReferences(r, "", "")
			if err != nil {
				catalogUnavailable(&sources[9], "storage_unavailable")
			} else {
				sources[9].Counts.Unverified = catalogCount(int64(len(refs)))
				sources[9].NoteCodes = append(sources[9].NoteCodes, "rag_workspace_material")
			}
		}
	} else {
		catalogUnavailable(&sources[9], "table_missing")
	}
}

func (g *GatewayService) personalMemoryCatalogManaged(r *http.Request, agent string, sessions []ChatSession, runtime *LLMAgent, sources []personalMemoryCatalogSource) {
	entries, summaries, context := &sources[4], &sources[5], &sources[6]
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, ImportLegacy: false}
	service := memorycore.NewService(g.db, g.currentProjectWorkspace(), agent)
	if runtime != nil && runtime.scm != nil && projectctx.SameWorkspace(runtime.scm.Workspace(), g.currentProjectWorkspace()) && runtime.scm.LastError() == nil {
		if service.SetContextStore(runtime.scm) == nil {
			context.Available = true
			context.Counts.Verified = catalogCount(0)
		}
	}
	if !context.Available {
		context.Status = "unverified"
		context.NoteCodes = append(context.NoteCodes, "runtime_check_required")
	}
	if g.catalogTable(r, entries, &memorycore.Entry{}) {
		_, count, err := service.PageWithFilters(r.Context(), owner, "", "", "", "", 0, 1)
		if err == nil {
			_, shared, sharedErr := memorycore.NewGlobalService(g.db, g.currentProjectWorkspace()).PageWithFilters(r.Context(), owner, memorycore.MemoryGlobal, "", "", "", 0, 1)
			err, count = sharedErr, count+shared
		}
		for _, session := range sessions {
			if err != nil {
				break
			}
			owner.SessionID = session.ID
			_, notes, notesErr := service.PageWithFilters(r.Context(), owner, memorycore.MemorySession, "", "", "", 0, 1)
			err, count = notesErr, count+notes
		}
		if err != nil {
			catalogUnavailable(entries, "storage_unavailable")
		} else {
			entries.Counts.Verified = catalogCount(count)
		}
	}
	if g.catalogTable(r, summaries, &memorycore.Summary{}) {
		var count int64
		for _, session := range sessions {
			owner.SessionID = session.ID
			refs, err := service.WorkingReferences(r.Context(), owner)
			if err != nil {
				catalogUnavailable(summaries, "storage_unavailable")
				context.Counts.Verified = nil
				break
			}
			for _, ref := range refs {
				if ref.SourceType == "summary" {
					count++
				} else if ref.SourceType == "session_context" && context.Counts.Verified != nil {
					*context.Counts.Verified++
				}
			}
		}
		if summaries.Available {
			summaries.Counts.Verified = catalogCount(count)
		}
	}
}

func (g *GatewayService) personalMemoryCatalogFiles(r *http.Request, agent string, sessions []ChatSession, runtime *LLMAgent, sources []personalMemoryCatalogSource) {
	workspace := g.currentProjectWorkspace()
	context, graph, learn := &sources[6], &sources[7], &sources[8]
	contextPath := filepath.Join(workspace, ".openkitty", "context", "context.json")
	if runtime != nil && runtime.scm != nil && projectctx.SameWorkspace(runtime.scm.Workspace(), workspace) {
		contextPath = filepath.Join(runtime.scm.StoreDir(), "context.json")
	}
	if relative := personalMemoryCatalogRelativePath(workspace, contextPath); relative != "" {
		context.Storage.Paths = append(context.Storage.Paths, relative)
	}
	graphPath := filepath.Join(workspace, ".openkitty", "knowledge_graph.db")
	if relative := personalMemoryCatalogRelativePath(workspace, graphPath); relative != "" {
		graph.Storage.Paths = append(graph.Storage.Paths, relative)
	}
	if globalKG != nil && globalKG.BelongsToWorkspace(workspace) && personalMemoryCatalogRelativePath(workspace, graphPath) != "" {
		graph.Available = true
		// Reuse the existing evidence adapter; never construct/migrate a graph.
		refs, err := g.personalMemorySourceReferences(r, agent, "", "", "", "")
		if err != nil {
			catalogUnavailable(graph, "storage_unavailable")
		} else {
			var count int64
			for _, reference := range refs {
				if reference.SourceType == "knowledge_fact" {
					count++
				}
			}
			graph.Counts.Verified = catalogCount(count)
			ids := make([]string, 0, len(sessions))
			for _, session := range sessions {
				ids = append(ids, session.ID)
			}
			_, archived, archiveErr := globalKG.WorkspaceKnowledgeArchive(r.Context(), ids, false, "", 0, 1)
			if archiveErr != nil {
				catalogUnavailable(graph, "storage_unavailable")
			} else {
				unverified := archived - count
				if unverified < 0 {
					unverified = 0
				}
				graph.Counts.Unverified = catalogCount(unverified)
				graph.NoteCodes = append(graph.NoteCodes, "workspace_knowledge_archive", "graph_quotes_separate")
			}
		}
	} else {
		graph.Status = "unverified"
	}
	// learn currently uses this compatibility directory. Files have no trusted
	// subject/session evidence, so neither their contents nor an aggregate is
	// claimed as the current person's memories or automatic Recall inputs.
	learnPath := filepath.Join(workspace, ".claude", "projects", "E--openkitty", "memory")
	if relative := personalMemoryCatalogRelativePath(workspace, learnPath); relative != "" {
		learn.Storage.Paths = append(learn.Storage.Paths, relative)
		if directory, err := os.Stat(learnPath); err == nil && directory.IsDir() {
			learn.Available, learn.Status = true, "unverified"
		} else if os.IsNotExist(err) {
			learn.Status = "unverified"
		} else {
			catalogUnavailable(learn, "storage_unavailable")
		}
	} else {
		catalogUnavailable(learn, "storage_unavailable")
	}
}
