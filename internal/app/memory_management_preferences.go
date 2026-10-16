package app

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	memorycore "mac/internal/memory"
)

const personalPreferenceSupplementalLimit = 200

// Historical structured preferences are reviewable original relations, not
// active personal defaults. The existing graph archive determines authority.
func (g *GatewayService) personalMemoryStructuredPreferenceSources(r *http.Request, agent, search string, existing []memorycore.WorkingReference) ([]memorycore.WorkingReference, bool, error) {
	workspace := g.currentProjectWorkspace()
	graphPath := personalMemoryCatalogRelativePath(workspace, filepath.Join(workspace, ".openkitty", "knowledge_graph.db"))
	if globalKG == nil || !globalKG.BelongsToWorkspace(workspace) || graphPath == "" {
		return existing, false, nil
	}
	sessions, err := g.personalMemoryCatalogSessions(r, agent)
	if err != nil {
		return existing, false, err
	}
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	remaining := max(0, personalPreferenceSupplementalLimit-len(existing))
	items, total, err := globalKG.WorkspacePreferenceArchive(r.Context(), ids, search, 0, max(1, remaining))
	if err != nil {
		return existing, false, err
	}
	seen := make(map[string]bool)
	for _, ref := range existing {
		if ref.Graph != nil && ref.SourceID != "" {
			seen[ref.SourceID] = true
		}
	}
	result := existing[:min(len(existing), personalPreferenceSupplementalLimit)]
	for _, item := range items {
		id := strconv.FormatInt(item.ID, 10)
		if seen[id] || len(result) >= personalPreferenceSupplementalLimit {
			continue
		}
		verified := false
		created := parseKnowledgeArchiveTime(item.CreatedAt)
		confidence, availability := item.Confidence, "missing"
		if item.Evidence != "" {
			availability = "unverified_quote"
		}
		ref := memorycore.WorkingReference{ID: fmt.Sprintf("graph:%d", item.ID), SourceID: id, Kind: "preference", Scope: memorycore.MemoryProject,
			Key: memorySourceText("assistant", item.Subject+" · "+item.Predicate, 740), Value: memorySourceText("assistant", item.Object, 8000),
			SourceType: "knowledge_graph_legacy", SourceLocation: graphPath + " · triples", ReadOnly: true, OwnershipVerified: &verified,
			CreatedAt: sourceTime(created), UpdatedAt: created, ContentTruncated: item.ContentTruncated,
			Graph: &memorycore.GraphSource{Subject: memorySourceText("assistant", item.Subject, 500), Predicate: item.Predicate,
				Object: memorySourceText("assistant", item.Object, 8000), Origin: memorySourceText("assistant", item.Source, 240),
				ValidFrom: item.ValidFrom, ValidTo: item.ValidTo, Confidence: &confidence, EvidenceAvailability: availability}}
		if strings.HasPrefix(item.Source, "conversation:session:") {
			ref.SourceType, ref.AgentName, ref.SessionID = "knowledge_graph_fact", agent, strings.TrimPrefix(item.Source, "conversation:session:")
		} else {
			ref.Graph.QuotedEvidence = memorySourceText("assistant", item.Evidence, 1200)
		}
		result = append(result, ref)
	}
	return result, total > int64(remaining) || len(existing) > personalPreferenceSupplementalLimit, nil
}
