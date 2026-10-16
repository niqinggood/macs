package app

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	memorycore "mac/internal/memory"
)

// A tenant-only legacy table is not evidence of a personal workspace. An
// archive may expose workspace material only from a physically bound local DB.
// Shared MySQL and external SQLite remain on their existing authorization path.
func (g *GatewayService) personalMemoryWorkspaceDatabase(r *http.Request) bool {
	storage, notes := g.personalMemoryCatalogDatabaseStorage(r)
	return storage.Kind == "sqlite" && len(storage.Paths) == 1 && len(notes) == 0
}

func (g *GatewayService) personalMemoryRAGReferences(r *http.Request, kind, search string) ([]memorycore.WorkingReference, error) {
	result := make([]memorycore.WorkingReference, 0)
	if !g.personalMemoryWorkspaceDatabase(r) || !g.db.Migrator().HasTable(&RAGKb{}) || kind != "" && kind != "fact" {
		return result, nil
	}
	var kbs []RAGKb
	if err := g.db.WithContext(r.Context()).Model(&RAGKb{}).
		Select("id,name,description,doc_count,chunk_count,created_at,updated_at").
		Where("tenant_id = ?", defaultExecutionTenantID).
		Order("updated_at DESC,id DESC").Limit(200).Find(&kbs).Error; err != nil {
		return nil, err
	}
	verified := false
	for _, kb := range kbs {
		value := memorySourceText("assistant", kb.Description, 1200)
		value = fmt.Sprintf("文档：%d · 检索片段：%d\n%s", kb.DocCount, kb.ChunkCount, value)
		ref := memorycore.WorkingReference{ID: fmt.Sprintf("rag:%d", kb.ID), SourceID: strconv.FormatUint(uint64(kb.ID), 10),
			Key: memorySourceText("assistant", kb.Name, 240), Value: value, Scope: memorycore.MemoryProject, Kind: "fact", SourceType: "rag_knowledge",
			CreatedAt: sourceTime(kb.CreatedAt), UpdatedAt: kb.UpdatedAt, ReadOnly: true, OwnershipVerified: &verified,
			SourceLocation: "rag_kbs"}
		if memorySourceMatches(ref, kind, search) {
			result = append(result, ref)
		}
	}
	return result, nil
}

// Knowledge is a paged browser of existing records. Legacy graph material is
// explicitly unverified, read-only workspace history; viewing it neither
// imports it into memory_entries nor changes any automatic recall decision.
func (g *GatewayService) personalMemoryKnowledgePage(r *http.Request, agent, kind, search string, offset, limit int) ([]memorycore.WorkingReference, int64, error) {
	prefix := make([]memorycore.WorkingReference, 0)
	verifiedFacts := make(map[string]memorycore.WorkingReference)
	source := r.URL.Query().Get("knowledge_source")
	if source == "" {
		source = "curated"
	}
	if source == "materials" || source == "curated" || source == "all" || source == "learning" {
		refs, err := g.personalMemorySourceReferences(r, agent, "", "", kind, search)
		if err != nil {
			return nil, 0, err
		}
		for _, ref := range refs {
			if ref.SourceType == "learning_knowledge" || source != "learning" && source != "materials" && ref.SourceType == "knowledge_fact" {
				ref.AgentName = agent
				if ref.SourceType == "learning_knowledge" {
					ref.SourceLocation = "knowledges"
					// The management reader shows the stored body. Recall keeps its
					// existing summary and token budget in the source adapter.
					if ref.OriginalContent != "" {
						ref.Value = ref.OriginalContent
					}
				} else {
					ref.SourceLocation = ".openkitty/knowledge_graph.db · triples + fact_evidence"
				}
				verified := true
				ref.OwnershipVerified = &verified
				if ref.SourceType == "knowledge_fact" {
					verifiedFacts[ref.SourceID] = ref
					if source != "curated" {
						continue
					}
				}
				prefix = append(prefix, ref)
			}
		}
	}
	if source == "materials" || source == "curated" || source == "all" || source == "rag" {
		refs, err := g.personalMemoryRAGReferences(r, kind, search)
		if err != nil {
			return nil, 0, err
		}
		prefix = append(prefix, refs...)
	}
	sort.SliceStable(prefix, func(i, j int) bool { return prefix[i].UpdatedAt.After(prefix[j].UpdatedAt) })
	result := make([]memorycore.WorkingReference, 0, limit)
	if offset < len(prefix) {
		end := offset + limit
		if end > len(prefix) {
			end = len(prefix)
		}
		result = append(result, prefix[offset:end]...)
	}
	var graphTotal int64
	workspace := g.currentProjectWorkspace()
	graphPath := personalMemoryCatalogRelativePath(workspace, filepath.Join(workspace, ".openkitty", "knowledge_graph.db"))
	if (source == "all" || source == "graph" || source == "quotes") && (kind == "" || kind == "fact") && globalKG != nil && globalKG.BelongsToWorkspace(workspace) && graphPath != "" {
		sessions, err := g.personalMemoryCatalogSessions(r, agent)
		if err != nil {
			return nil, 0, err
		}
		ids := make([]string, 0, len(sessions))
		for _, session := range sessions {
			ids = append(ids, session.ID)
		}
		graphOffset := offset - len(prefix)
		if graphOffset < 0 {
			graphOffset = 0
		}
		pageLimit := limit - len(result)
		if pageLimit == 0 {
			pageLimit = 1 // still obtain the exact archive count
		}
		items, count, err := globalKG.WorkspaceKnowledgeArchive(r.Context(), ids, source == "quotes", search, graphOffset, pageLimit)
		if err != nil {
			return nil, 0, err
		}
		graphTotal = count
		for _, item := range items {
			if len(result) >= limit {
				break
			}
			created := parseKnowledgeArchiveTime(item.CreatedAt)
			table, sourceType, idPrefix := "triples", "knowledge_graph_legacy", "graph"
			if source == "quotes" {
				table, sourceType, idPrefix = "verbatim_memories", "knowledge_graph_verbatim_legacy", "quote"
			} else if strings.HasPrefix(item.Source, "conversation:session:") {
				sourceType = "knowledge_graph_fact"
			}
			verified := false
			ref := memorycore.WorkingReference{ID: fmt.Sprintf("%s:%d", idPrefix, item.ID), SourceID: strconv.FormatInt(item.ID, 10),
				Key: memorySourceText("assistant", item.Title, 240), Value: memorySourceText("assistant", item.Content, 8504), ContentTruncated: item.ContentTruncated,
				Scope: memorycore.MemoryProject, Kind: "fact", SourceType: sourceType, CreatedAt: sourceTime(created), UpdatedAt: created, ReadOnly: true,
				OwnershipVerified: &verified, SourceLocation: graphPath + " · " + table}
			if source != "quotes" {
				confidence := item.Confidence
				availability := "missing"
				if item.Evidence != "" {
					availability = "unverified_quote"
				}
				ref.Graph = &memorycore.GraphSource{Subject: memorySourceText("assistant", item.Subject, 500), Predicate: memorySourceText("assistant", item.Predicate, 500),
					Object: memorySourceText("assistant", item.Object, 8000), Origin: memorySourceText("assistant", item.Source, 240), ValidFrom: item.ValidFrom, ValidTo: item.ValidTo,
					Confidence: &confidence, EvidenceAvailability: availability}
				// Unverified session evidence must still pass the existing
				// original-user-message check before it can be exposed as a quote.
				if !strings.HasPrefix(item.Source, "conversation:session:") {
					ref.Graph.QuotedEvidence = memorySourceText("assistant", item.Evidence, 1200)
				}
			}
			if sourceType == "knowledge_graph_fact" {
				ref.AgentName, ref.SessionID = agent, strings.TrimPrefix(item.Source, "conversation:session:")
			}
			if source != "quotes" {
				if evidenced, ok := verifiedFacts[ref.SourceID]; ok {
					ref = evidenced
				}
			}
			result = append(result, ref)
		}
	}
	return result, int64(len(prefix)) + graphTotal, nil
}

func parseKnowledgeArchiveTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
