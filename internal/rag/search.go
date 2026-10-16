package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"mac/internal/ragmodel"
	knowledgetools "mac/internal/tools/knowledge"

	"gorm.io/gorm"
)

var gatewayDB *gorm.DB

var ragKeywordIndex = knowledgetools.NewBleveIndex(
	filepath.Join("knowledge", "rag", "bleve_index"),
	func() ([]ragmodel.Chunk, error) {
		if gatewayDB == nil {
			return nil, nil
		}
		var chunks []ragmodel.Chunk
		return chunks, gatewayDB.Find(&chunks).Error
	},
)

var ragSearchEnhancer = knowledgetools.NewSearchEnhancer(nil)

func SetDatabase(db *gorm.DB) {
	gatewayDB = db
}

func ConfigureSearchEnhancer(model knowledgetools.SearchModelConfig) {
	ragSearchEnhancer.ConfigureReranker("api", model)
	ragSearchEnhancer.ConfigureHyDE(true, model)
}

func SaveChunk(ch *ragmodel.Chunk) error {
	if gatewayDB == nil {
		return fmt.Errorf("RAG database is unavailable")
	}
	if err := gatewayDB.Create(ch).Error; err != nil {
		return err
	}
	if err := ragKeywordIndex.IndexChunk(ch); err != nil {
		return err
	}
	go knowledgetools.IndexChunkWithEmbedding(ch)
	return nil
}

func DeleteChunks(kbID uint, sourceDoc string) error {
	if gatewayDB == nil {
		return fmt.Errorf("RAG database is unavailable")
	}
	var ids []uint
	gatewayDB.Model(&ragmodel.Chunk{}).Where("kb_id = ? AND source_doc = ?", kbID, sourceDoc).Pluck("id", &ids)
	if err := ragKeywordIndex.DeleteChunkIDs(ids); err != nil {
		return err
	}
	return gatewayDB.Where("kb_id = ? AND source_doc = ?", kbID, sourceDoc).Delete(&ragmodel.Chunk{}).Error
}

func DeleteKBChunks(kbID uint) error {
	if gatewayDB == nil {
		return fmt.Errorf("RAG database is unavailable")
	}
	var ids []uint
	gatewayDB.Model(&ragmodel.Chunk{}).Where("kb_id = ?", kbID).Pluck("id", &ids)
	if err := ragKeywordIndex.DeleteChunkIDs(ids); err != nil {
		return err
	}
	return gatewayDB.Where("kb_id = ?", kbID).Delete(&ragmodel.Chunk{}).Error
}

func searchRAG(kbIDs []uint, query, sessionID, sourceDoc string, limit int) ([]knowledgetools.RAGResult, error) {
	// Extract tenantID from context-like flow (empty for direct API calls)
	tenantID := ""
	tenantResolved := false
	if len(kbIDs) > 0 && gatewayDB != nil {
		var knowledgeBases []ragmodel.Kb
		if err := gatewayDB.Where("id IN ?", kbIDs).Find(&knowledgeBases).Error; err != nil {
			return nil, fmt.Errorf("resolve knowledge bases: %w", err)
		}
		uniqueIDs := make(map[uint]struct{}, len(kbIDs))
		for _, id := range kbIDs {
			uniqueIDs[id] = struct{}{}
		}
		if len(knowledgeBases) != len(uniqueIDs) {
			return nil, fmt.Errorf("one or more knowledge bases are unavailable")
		}
		for _, kb := range knowledgeBases {
			if !tenantResolved {
				tenantID = kb.TenantID
				tenantResolved = true
				continue
			}
			if kb.TenantID != tenantID {
				return nil, fmt.Errorf("knowledge bases must belong to one tenant")
			}
		}
	}

	// Step 1: Try FAQ exact/fuzzy match first
	if faq := searchFAQ(kbIDs, query, tenantID); faq != nil {
		log.Printf("[RAG] FAQ hit: %q", query)
		logSearch(kbIDs, query, tenantID, sessionID, 1)
		return []knowledgetools.RAGResult{{
			SourceDoc: "FAQ: " + faq.Question,
			Content:   faq.Answer,
			Score:     10.0, // FAQ matches get top score
		}}, nil
	}

	deduped, err := ragKeywordIndex.Search(kbIDs, query, sessionID, sourceDoc, limit)
	if err != nil {
		return nil, err
	}

	// Expand with adjacent chunks for richer context
	expanded := expandWithNeighbors(deduped)

	// Log for analytics
	logSearch(kbIDs, query, tenantID, sessionID, len(expanded))

	return expanded, nil
}

// searchFAQ checks FAQ entries for exact/similar match before full-text search.
// Returns the formatted answer if found, empty string if no match.
func searchFAQ(kbIDs []uint, query, tenantID string) *ragmodel.FAQ {
	if gatewayDB == nil {
		return nil
	}
	// Step 1: exact match
	var faq ragmodel.FAQ
	q := gatewayDB.Where("question = ?", query)
	if len(kbIDs) > 0 {
		q = q.Where("kb_id IN ?", kbIDs)
	}
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if q.First(&faq).Error == nil {
		gatewayDB.Model(&faq).UpdateColumn("hit_count", faq.HitCount+1)
		return &faq
	}

	// Step 2: fuzzy match — question contains all keywords
	keywords := ragmodel.FilterTokens(ragmodel.GoJiebaCut(query))
	if len(keywords) >= 2 {
		q := gatewayDB.Model(&ragmodel.FAQ{})
		if len(kbIDs) > 0 {
			q = q.Where("kb_id IN ?", kbIDs)
		}
		if tenantID != "" {
			q = q.Where("tenant_id = ?", tenantID)
		}
		for _, kw := range keywords {
			q = q.Where("question LIKE ?", "%"+kw+"%")
		}
		if q.First(&faq).Error == nil {
			gatewayDB.Model(&faq).UpdateColumn("hit_count", faq.HitCount+1)
			return &faq
		}
	}
	return nil
}

// expandWithNeighbors pulls adjacent chunks from the same document for richer context.
func expandWithNeighbors(results []knowledgetools.RAGResult) []knowledgetools.RAGResult {
	if gatewayDB == nil || len(results) == 0 {
		return results
	}

	expanded := make([]knowledgetools.RAGResult, 0, len(results)*3)
	seen := map[uint]bool{}

	for _, r := range results {
		// Find the source chunk to get its ID and index
		var chunk ragmodel.Chunk
		if err := gatewayDB.Where("source_doc = ? AND content = ?", r.SourceDoc, r.Content).First(&chunk).Error; err != nil {
			expanded = append(expanded, r)
			continue
		}
		if seen[chunk.ID] {
			continue
		}
		seen[chunk.ID] = true
		expanded = append(expanded, r)

		// Fetch 1 neighbor before and 1 after
		var neighbors []ragmodel.Chunk
		gatewayDB.Where("kb_id = ? AND source_doc = ? AND chunk_index BETWEEN ? AND ?",
			chunk.KbID, chunk.SourceDoc, chunk.ChunkIndex-1, chunk.ChunkIndex+1).
			Order("chunk_index").Find(&neighbors)

		for _, n := range neighbors {
			if n.ID == chunk.ID || seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			expanded = append(expanded, knowledgetools.RAGResult{
				SourceDoc: n.SourceDoc,
				Content:   n.Content,
				Score:     r.Score * 0.5, // neighbors get half the score
			})
		}
	}
	return expanded
}

// logSearch records a search query for analytics.
func logSearch(kbIDs []uint, query, tenantID, sessionID string, hitCount int) {
	if gatewayDB == nil {
		return
	}
	kbID := uint(0)
	if len(kbIDs) > 0 {
		kbID = kbIDs[0]
	}
	gatewayDB.Create(&ragmodel.SearchLog{
		KbID: kbID, Query: query, HitCount: hitCount,
		SessionID: sessionID, TenantID: tenantID,
	})
}

func RAGHealth(kbID uint) map[string]interface{} {
	// Global stats
	var dbCount int64
	if gatewayDB != nil {
		q := gatewayDB.Model(&ragmodel.Chunk{})
		if kbID > 0 {
			q = q.Where("kb_id = ?", kbID)
		}
		q.Count(&dbCount)
	}

	bleveCount := uint64(0)
	if ragKeywordIndex.Ready() {
		bleveCount, _ = ragKeywordIndex.DocumentCount()
	}

	status := "healthy"
	if !ragKeywordIndex.Ready() {
		status = "unhealthy"
	}
	if dbCount != int64(bleveCount) && dbCount > 0 && bleveCount > 0 {
		status = "degraded"
	}
	result := map[string]interface{}{
		"ok": true, "db_count": dbCount, "bleve_count": bleveCount,
		"consistent": dbCount == int64(bleveCount), "engine": "bleve + gse",
		"status": status,
	}

	// Per-KB detailed stats
	if gatewayDB != nil && kbID > 0 {
		var kb ragmodel.Kb
		if gatewayDB.First(&kb, kbID).Error == nil {
			// Avg chunk size
			var avgSize float64
			gatewayDB.Model(&ragmodel.Chunk{}).Where("kb_id = ?", kbID).
				Select("AVG(LENGTH(content))").Scan(&avgSize)
			// Unique documents
			var docCount int64
			gatewayDB.Model(&ragmodel.Chunk{}).Where("kb_id = ?", kbID).
				Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
			// FAQ count
			var faqCount int64
			gatewayDB.Model(&ragmodel.FAQ{}).Where("kb_id = ?", kbID).Count(&faqCount)
			// Zero result queries
			var zeroCount int64
			gatewayDB.Model(&ragmodel.SearchLog{}).Where("kb_id = ? AND hit_count = 0", kbID).Count(&zeroCount)

			result["kb_stats"] = map[string]interface{}{
				"name":          kb.Name,
				"bm25_weight":   kb.BM25Weight,
				"doc_count":     docCount,
				"chunk_count":   dbCount,
				"avg_chunk_len": int(avgSize),
				"faq_count":     faqCount,
				"zero_queries":  zeroCount,
				"build_status":  kb.BuildStatus,
			}
		}
	}

	// Top hot queries
	if gatewayDB != nil {
		type hotQuery struct {
			Query string
			Cnt   int
		}
		var hots []hotQuery
		gatewayDB.Model(&ragmodel.SearchLog{}).
			Select("query, COUNT(*) as cnt").
			Where("hit_count > 0").
			Group("query").Order("cnt DESC").Limit(5).
			Find(&hots)
		if len(hots) > 0 {
			result["hot_queries"] = hots
		}
	}

	return result
}

// embedFAQOnSave generates and stores embedding for a FAQ question.
func EmbedFAQOnSave(faq *ragmodel.FAQ) {
	if !knowledgetools.EmbedderEnabled() || faq == nil || faq.Question == "" || gatewayDB == nil {
		return
	}
	vec, err := knowledgetools.EmbedText(context.Background(), faq.Question)
	if err != nil {
		log.Printf("[FAQ] Embed failed (q=%s): %v", faq.Question, err)
		return
	}
	data, _ := json.Marshal(vec)
	faq.EmbeddingJSON = string(data)
	gatewayDB.Model(faq).Update("embedding_json", faq.EmbeddingJSON)
}

// embedAllFAQs generates embeddings for all FAQs that don't have one yet.
func EmbedAllFAQs(kbID uint) int {
	if !knowledgetools.EmbedderEnabled() || gatewayDB == nil {
		return 0
	}
	var faqs []ragmodel.FAQ
	q := gatewayDB.Where("embedding_json = '' OR embedding_json IS NULL")
	if kbID > 0 {
		q = q.Where("kb_id = ?", kbID)
	}
	q.Find(&faqs)
	count := 0
	for i := range faqs {
		EmbedFAQOnSave(&faqs[i])
		count++
	}
	log.Printf("[FAQ] Embedded %d FAQs (kb=%d)", count, kbID)
	return count
}

func SyncIndex(kbID uint) (int, error) {
	if gatewayDB == nil {
		return 0, fmt.Errorf("RAG database is unavailable")
	}
	var chunks []ragmodel.Chunk
	if err := gatewayDB.Where("kb_id = ?", kbID).Find(&chunks).Error; err != nil {
		return 0, err
	}
	if err := ragKeywordIndex.SyncChunks(chunks); err != nil {
		return 0, err
	}
	return len(chunks), nil
}

// ====================== Hybrid Search (BM25 + Vector RRF) ======================

// ====================== Search Hot Cache ======================

var ragSearchCache = knowledgetools.NewRAGSearchCache(50, 5*time.Minute)

// cachedSearchHybrid wraps searchHybrid with an in-memory TTL cache.
func CachedSearchHybrid(kbIDs []uint, query, sessionID, sourceDoc string, limit int) ([]knowledgetools.RAGResult, error) {
	return ragSearchCache.Search(kbIDs, query, sessionID, sourceDoc, limit, SearchHybrid)
}

// searchHybrid combines BM25 keyword search with vector semantic search via RRF.
// Falls back to pure BM25 if embedder is disabled.
func SearchHybrid(kbIDs []uint, query, sessionID, sourceDoc string, limit int) ([]knowledgetools.RAGResult, error) {
	// HyDE query expansion (if enabled)
	expandedQuery := ragSearchEnhancer.Expand(query)

	// Run BM25 path (always available)
	bm25Results, _ := searchRAG(kbIDs, expandedQuery, sessionID, sourceDoc, limit*2)

	// If embedder is disabled, just return BM25
	if !knowledgetools.EmbedderEnabled() {
		return bm25Results, nil
	}

	// Run vector path
	var vecResults []knowledgetools.VectorResult
	if len(query) > 0 {
		vec, err := knowledgetools.EmbedText(context.Background(), expandedQuery)
		if err == nil && len(vec) > 0 {
			vecResults = knowledgetools.SearchVectors(kbIDs, vec, limit*2, sourceDoc)
		}
	}

	// Handle edge cases
	if len(vecResults) == 0 && len(bm25Results) == 0 {
		return nil, nil
	}
	if len(vecResults) == 0 {
		return bm25Results, nil
	}
	if len(bm25Results) == 0 {
		return knowledgetools.VectorResultsToRAG(vecResults, limit), nil
	}

	// Read BM25 weight from KB config (default 0.5)
	bm25Weight := 0.5
	if len(kbIDs) > 0 && gatewayDB != nil {
		var kb ragmodel.Kb
		if gatewayDB.First(&kb, kbIDs[0]).Error == nil && kb.BM25Weight > 0 {
			bm25Weight = kb.BM25Weight
		}
	}

	// RRF fusion with configured weights
	fused := knowledgetools.ReciprocalRankFusion(bm25Results, vecResults, limit*4, bm25Weight)
	return ragSearchEnhancer.Rerank(query, fused, limit), nil
}

// RAGSearchTool is implemented in the knowledge tools package. This root
// constructor only adapts the existing search and tenant ownership services.
type RAGSearchTool = knowledgetools.RAGSearchTool

func NewRAGSearchTool(
	kbIDs []uint,
	tenantID string,
	identityResolver func(context.Context) (string, bool),
	authorizer func(string, []uint) ([]uint, error),
) *RAGSearchTool {
	return knowledgetools.NewRAGSearchTool(
		kbIDs,
		tenantID,
		func(ids []uint, query, sessionID, sourceDoc string, limit int) ([]knowledgetools.RAGResult, error) {
			return SearchHybrid(ids, query, sessionID, sourceDoc, limit)
		},
		identityResolver,
		authorizer,
	)
}
