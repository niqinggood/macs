package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

func TestPersonalMemoryKnowledgeMaterialsKeepArticleTitlesSeparateFromRelations(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous; _ = kg.Close() })
	quote := "The fixture product supports browser automation."
	if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", UserId: "fixture-user", SessionID: "source-web", Role: "user", Content: quote}).Error; err != nil {
		t.Fatal(err)
	}
	if err := kg.AddFactWithEvidence(context.Background(), memorycore.Fact{Subject: "fixture product", Predicate: "supports", Object: "browser automation", Source: memorycore.ConversationSessionSource("source-web")}, quote); err != nil {
		t.Fatal(err)
	}
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "success", UserQuestion: "article source", FinalAnswer: "original result"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	for _, article := range []memorycore.Knowledge{
		{TenantID: "default", AgentName: "first", KnowledgeID: "owned-article", Title: "Existing deployment instructions", Summary: "Actual saved instructions", Content: "Original document body", SourceType: "learning", SourceID: strconv.FormatUint(uint64(event.ID), 10)},
		{TenantID: "foreign", AgentName: "first", KnowledgeID: "foreign-article", Title: "FOREIGN_ARTICLE", Summary: "FOREIGN_CONTENT", SourceType: "learning", SourceID: strconv.FormatUint(uint64(event.ID), 10)},
		{TenantID: "default", AgentName: "second", KnowledgeID: "other-agent-article", Title: "OTHER_AGENT_ARTICLE", Summary: "OTHER_AGENT_CONTENT", SourceType: "learning", SourceID: strconv.FormatUint(uint64(event.ID), 10)},
	} {
		if err := g.db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	materials, body := memoryViewRead(t, g, "view=knowledge&knowledge_source=materials")
	if materials.SourcesTotal == nil || *materials.SourcesTotal != 1 || len(materials.Supplemental) != 1 || materials.Supplemental[0].SourceType != "learning_knowledge" || materials.Supplemental[0].Key != "Existing deployment instructions" {
		t.Fatal("articles were replaced by relation subjects or titles were lost", body)
	}
	if materials.Supplemental[0].Value != "Original document body" {
		t.Fatal("article reader showed a summary instead of the saved body", body)
	}
	refs, err := g.personalMemorySourceReferences(httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil), "first", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if ref.SourceType == "learning_knowledge" && ref.Value != "Actual saved instructions" {
			t.Fatal("displaying the article body changed the recall summary", ref.Value)
		}
	}
	if strings.Contains(body, "FOREIGN_") || strings.Contains(body, "OTHER_AGENT_") || strings.Contains(body, "knowledge_fact") {
		t.Fatal("material scope included a foreign article or relation", body)
	}
	curated, body := memoryViewRead(t, g, "view=knowledge&knowledge_source=curated")
	if curated.SourcesTotal == nil || *curated.SourcesTotal != 2 {
		t.Fatal("existing verified relations disappeared from their own view", body)
	}
}

func TestPersonalMemoryKnowledgeReaderKeepsSavedContentAndEvidenceBoundary(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous; _ = kg.Close() })
	quotation := strings.Repeat("saved paragraph ", 120) + "ORIGINAL_END"
	if err := kg.AddVerbatim("readable quote", quotation, "manual"); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"manual", memorycore.ConversationSessionSource("source-web"), memorycore.ConversationSessionSource("foreign-session")} {
		if err := kg.AddFactWithEvidence(context.Background(), memorycore.Fact{Subject: source, Predicate: "contains", Object: quotation, Source: source}, "Stored evidence for "+source); err != nil {
			t.Fatal(err)
		}
	}
	quotes, body := memoryViewRead(t, g, "view=knowledge&knowledge_source=quotes")
	if len(quotes.Supplemental) != 1 || quotes.Supplemental[0].Value != quotation || quotes.Supplemental[0].ContentTruncated {
		t.Fatal("stored quotation was silently reduced to a preview", body)
	}
	graph, body := memoryViewRead(t, g, "view=knowledge&knowledge_source=graph")
	if len(graph.Supplemental) != 2 || strings.Contains(body, "foreign-session") {
		t.Fatal("reader widened the graph audience", body)
	}
	for _, ref := range graph.Supplemental {
		if ref.Graph == nil || ref.Graph.Object != quotation || !strings.Contains(ref.Value, "ORIGINAL_END") || ref.Graph.EvidenceAvailability != "unverified_quote" {
			t.Fatal("graph value or stored evidence state lost", ref)
		}
		if ref.Graph.Origin == "manual" && ref.Graph.QuotedEvidence != "Stored evidence for manual" {
			t.Fatal("authorized workspace evidence was hidden", ref.Graph)
		}
		if strings.HasPrefix(ref.Graph.Origin, "conversation:session:") && ref.Graph.QuotedEvidence != "" {
			t.Fatal("unproven session quotation was exposed", ref.Graph)
		}
	}
}

func TestPersonalMemoryKnowledgePageShowsWorkspaceArchiveWithoutPromoting(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous; _ = kg.Close() })
	for _, source := range []string{"conversation", "manual", memorycore.ConversationSessionSource("source-web"), memorycore.ConversationSessionSource("foreign-session")} {
		if err := kg.AddTriple("fixture", "learned", "existing material", source, "", "", 1); err != nil {
			t.Fatal(err)
		}
		if err := kg.AddVerbatim("fixture", "existing quotation", source); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	type responsePage struct {
		Entries      []memorycore.Entry
		Supplemental []memorycore.WorkingReference
		Total        int64
		SourcesTotal int64 `json:"sources_total"`
	}
	var response responsePage
	first := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?view=knowledge&agent=first&knowledge_source=all&limit=2", nil)
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &response) != nil || response.Total != 0 || response.SourcesTotal != 3 || len(response.Entries) != 0 || len(response.Supplemental) != 2 {
		t.Fatalf("knowledge archive absent: %d %s", first.Code, first.Body.String())
	}
	for _, ref := range response.Supplemental {
		if ref.Graph == nil || ref.Graph.Subject != "fixture" || ref.Graph.Predicate != "learned" || ref.Graph.Object != "existing material" || ref.Graph.EvidenceAvailability != "missing" || ref.Graph.QuotedEvidence != "" {
			t.Fatal("graph content or evidence boundary was lost", ref.Graph)
		}
		if !ref.ReadOnly || ref.OwnershipVerified == nil || *ref.OwnershipVerified || ref.Scope != memorycore.MemoryProject || ref.CreatedAt == nil || !strings.HasPrefix(ref.SourceLocation, ".openkitty/knowledge_graph.db") {
			t.Fatal("legacy knowledge mislabeled as verified personal memory", ref)
		}
		if ref.SourceType == "knowledge_graph_legacy" && ref.AgentName != "" {
			t.Fatal("unowned workspace material mislabeled as current Agent")
		}
	}
	second := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?view=knowledge&agent=first&knowledge_source=all&offset=2&limit=2", nil)
	response = responsePage{}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &response) != nil || response.SourcesTotal != 3 || len(response.Supplemental) != 1 {
		t.Fatalf("knowledge pagination lost old records: %d %s", second.Code, second.Body.String())
	}
	quotes := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?view=knowledge&agent=first&knowledge_source=quotes", nil)
	response = responsePage{}
	if quotes.Code != http.StatusOK || json.Unmarshal(quotes.Body.Bytes(), &response) != nil || response.SourcesTotal != 2 || len(response.Supplemental) != 2 {
		t.Fatalf("quotation archive absent or session content exposed: %d %s", quotes.Code, quotes.Body.String())
	}
	for _, ref := range response.Supplemental {
		if ref.SourceType != "knowledge_graph_verbatim_legacy" || ref.SessionID != "" || ref.AgentName != "" {
			t.Fatal("unverified original quotation acquired a personal audience", ref)
		}
	}
	var count int64
	curated := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?view=knowledge&agent=first", nil)
	response = responsePage{}
	if curated.Code != http.StatusOK || json.Unmarshal(curated.Body.Bytes(), &response) != nil || response.SourcesTotal != 0 || len(response.Supplemental) != 0 {
		t.Fatal("default knowledge view promoted an unevidenced archive", curated.Code)
	}
	if err := g.db.Model(&memorycore.Entry{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("browsing knowledge promoted an entry", count, err)
	}
	if strings.Contains(kg.BuildContextPromptForSession("source-web", 20), "conversation ·") {
		t.Fatal("archive modified runtime recall")
	}
	g.project = projectctx.New(t.TempDir())
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory?knowledge_source=graph", nil)
	refs, total, err := g.personalMemoryKnowledgePage(request, "first", "", "", 0, 20)
	if err != nil || total != 0 || len(refs) != 0 {
		t.Fatal("another workspace's graph was exposed", total, err)
	}
}

func TestPersonalMemoryKnowledgeRAGRequiresBoundLocalDatabaseAndDefaultTenant(t *testing.T) {
	g := newMemoryScopeGateway(t)
	if err := g.db.AutoMigrate(&RAGKb{}, &RAGChunk{}); err != nil {
		t.Fatal(err)
	}
	for _, kb := range []RAGKb{{TenantID: "default", Slug: "mine", Name: "existing manuals", Description: "stored description", DocCount: 7, ChunkCount: 21}, {TenantID: "foreign", Slug: "other", Name: "FOREIGN_TENANT_MATERIAL"}} {
		if err := g.db.Create(&kb).Error; err != nil {
			t.Fatal(err)
		}
	}
	deleted := RAGKb{TenantID: "default", Slug: "deleted", Name: "DELETED_MATERIAL"}
	if err := g.db.Create(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory?knowledge_source=rag", nil)
	r.Header.Set("X-Tenant-ID", "foreign")
	if refs, err := g.personalMemoryRAGReferences(r, "", ""); err != nil || len(refs) != 0 {
		t.Fatal("external/shared SQLite was treated as workspace material", err)
	}
	var paths []struct{ Name, File string }
	if err := g.db.Raw("PRAGMA database_list").Scan(&paths).Error; err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path.Name == "main" {
			g.project = projectctx.New(filepath.Dir(path.File))
		}
	}
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	refs, total, err := g.personalMemoryKnowledgePage(r, "first", "", "", 0, 20)
	if err != nil || total != 1 || len(refs) != 1 || refs[0].SourceType != "rag_knowledge" || !strings.Contains(refs[0].Value, "7") || !strings.Contains(refs[0].Value, "21") || refs[0].AgentName != "" {
		t.Fatal("allowed workspace knowledge metadata missing", total, refs, err)
	}
	if refs, err := g.personalMemoryRAGReferences(r, "", "no match"); err != nil || len(refs) != 0 {
		t.Fatal("RAG search filter ignored", err)
	}
}
