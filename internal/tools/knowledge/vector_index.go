package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"mac/internal/ragmodel"
)

// ====================== Vector Index ======================
// Per-KB, in-memory cosine similarity index with JSON persistence.
// Files stored at: knowledge/rag/{kb_id}/vectors.json

// VectorEntry is a persisted vector with its chunk reference.
type VectorEntry struct {
	ChunkID uint      `json:"chunk_id"`
	KBID    uint      `json:"kb_id"`
	DocName string    `json:"doc_name"`
	Heading string    `json:"heading"`
	Content string    `json:"content"`
	Vector  []float64 `json:"vector"`
}

// vectorIndex holds all vectors for a single KB.
type vectorIndex struct {
	entries   []VectorEntry
	dimension int
	mu        sync.RWMutex
}

var (
	vectorIndexes   = map[uint]*vectorIndex{} // kbID -> index
	vectorIndexesMu sync.RWMutex
)

// ====================== Index Operations ======================

// addVector adds a single vector to a KB's index (thread-safe).
func AddVector(kbID uint, entry VectorEntry) {
	vectorIndexesMu.Lock()
	idx, ok := vectorIndexes[kbID]
	if !ok {
		idx = &vectorIndex{dimension: len(entry.Vector)}
		vectorIndexes[kbID] = idx
	}
	vectorIndexesMu.Unlock()

	idx.mu.Lock()
	idx.entries = append(idx.entries, entry)
	idx.mu.Unlock()
}

// addVectors adds multiple vectors at once.
func AddVectors(kbID uint, entries []VectorEntry) {
	if len(entries) == 0 {
		return
	}
	vectorIndexesMu.Lock()
	idx, ok := vectorIndexes[kbID]
	if !ok {
		idx = &vectorIndex{dimension: len(entries[0].Vector)}
		vectorIndexes[kbID] = idx
	}
	vectorIndexesMu.Unlock()

	idx.mu.Lock()
	idx.entries = append(idx.entries, entries...)
	idx.mu.Unlock()
}

// removeDocVectors removes all vectors for a document in a KB.
func RemoveDocumentVectors(kbID uint, docName string) {
	vectorIndexesMu.RLock()
	idx, ok := vectorIndexes[kbID]
	vectorIndexesMu.RUnlock()
	if !ok {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	filtered := make([]VectorEntry, 0, len(idx.entries))
	for _, e := range idx.entries {
		if e.DocName != docName {
			filtered = append(filtered, e)
		}
	}
	idx.entries = filtered
}

// removeKBVectors removes all vectors for a KB.
func RemoveKnowledgeBaseVectors(kbID uint) {
	vectorIndexesMu.Lock()
	delete(vectorIndexes, kbID)
	vectorIndexesMu.Unlock()
}

// vectorCount returns the number of vectors in a KB's index.
func VectorCount(kbID uint) int {
	vectorIndexesMu.RLock()
	idx, ok := vectorIndexes[kbID]
	vectorIndexesMu.RUnlock()
	if !ok {
		return 0
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// SnapshotVectors returns a copy suitable for export without exposing index
// locks or mutable internal slices.
func SnapshotVectors(kbID uint) []VectorEntry {
	vectorIndexesMu.RLock()
	idx, ok := vectorIndexes[kbID]
	vectorIndexesMu.RUnlock()
	if !ok {
		return []VectorEntry{}
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entries := make([]VectorEntry, len(idx.entries))
	copy(entries, idx.entries)
	return entries
}

// ====================== Vector Search ======================

// searchVectors finds top-K chunks by cosine similarity to the query vector.
func SearchVectors(kbIDs []uint, queryVec []float64, topK int, sourceDoc string) []VectorResult {
	if len(queryVec) == 0 {
		return nil
	}

	// Collect candidate entries from matching KBs
	var candidates []VectorEntry

	// Build KB set
	kbSet := map[uint]bool{}
	if len(kbIDs) > 0 {
		for _, id := range kbIDs {
			kbSet[id] = true
		}
	}

	vectorIndexesMu.RLock()
	for kbID, idx := range vectorIndexes {
		if len(kbSet) > 0 && !kbSet[kbID] {
			continue
		}
		idx.mu.RLock()
		for _, e := range idx.entries {
			if sourceDoc != "" && e.DocName != sourceDoc {
				continue
			}
			candidates = append(candidates, e)
		}
		idx.mu.RUnlock()
	}
	vectorIndexesMu.RUnlock()

	if len(candidates) == 0 {
		return nil
	}

	// Score by cosine similarity
	type scoredResult struct {
		entry VectorEntry
		score float64
	}
	var scoredList []scoredResult
	for _, c := range candidates {
		s := CosineSimilarity(queryVec, c.Vector)
		if s > 0.001 { // minimum threshold
			scoredList = append(scoredList, scoredResult{entry: c, score: s})
		}
	}

	sort.Slice(scoredList, func(i, j int) bool {
		return scoredList[i].score > scoredList[j].score
	})

	if topK > 0 && len(scoredList) > topK {
		scoredList = scoredList[:topK]
	}

	results := make([]VectorResult, len(scoredList))
	for i, s := range scoredList {
		results[i] = VectorResult{
			ChunkID: s.entry.ChunkID,
			DocName: s.entry.DocName,
			Heading: s.entry.Heading,
			Content: s.entry.Content,
			Score:   s.score,
		}
	}
	return results
}

// ====================== Cosine Similarity ======================

func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ====================== Persistence ======================

func VectorIndexPath(kbID uint) string {
	return filepath.Join("knowledge", "rag", fmt.Sprintf("%d", kbID), "vectors.json")
}

// saveVectorIndex persists a KB's vector index to disk.
func SaveVectorIndex(kbID uint) error {
	vectorIndexesMu.RLock()
	idx, ok := vectorIndexes[kbID]
	vectorIndexesMu.RUnlock()
	if !ok {
		return nil
	}

	idx.mu.RLock()
	entries := make([]VectorEntry, len(idx.entries))
	copy(entries, idx.entries)
	idx.mu.RUnlock()

	p := VectorIndexPath(kbID)
	os.MkdirAll(filepath.Dir(p), 0755)

	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal vectors: %w", err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return fmt.Errorf("write vectors: %w", err)
	}
	return nil
}

// loadVectorIndex loads a KB's vector index from disk into memory.
func LoadVectorIndex(kbID uint) error {
	p := VectorIndexPath(kbID)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no vectors yet, not an error
		}
		return fmt.Errorf("read vectors: %w", err)
	}

	var entries []VectorEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("unmarshal vectors: %w", err)
	}

	if len(entries) > 0 {
		vectorIndexesMu.Lock()
		idx := &vectorIndex{dimension: len(entries[0].Vector)}
		idx.entries = entries
		vectorIndexes[kbID] = idx
		vectorIndexesMu.Unlock()
		log.Printf("[Vector] Loaded %d vectors for KB %d (dim=%d)", len(entries), kbID, len(entries[0].Vector))
	}
	return nil
}

// loadAllVectorIndexes scans knowledge/rag/ and loads all vector indexes.
func LoadAllVectorIndexes() {
	root := filepath.Join("knowledge", "rag")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var kbID uint
		if _, scanErr := fmt.Sscanf(e.Name(), "%d", &kbID); scanErr == nil && kbID > 0 {
			if loadErr := LoadVectorIndex(kbID); loadErr != nil {
				log.Printf("[Vector] Load KB %d failed: %v", kbID, loadErr)
			}
		}
	}
}

// ====================== Index + Embed Chunks ======================

// indexChunksWithEmbedding generates embeddings for chunks and stores them in the vector index.
// Called after a document is processed into chunks.
func IndexChunksWithEmbedding(kbID uint, chunks []ragmodel.Chunk) error {
	if !EmbedderEnabled() || len(chunks) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	entries := make([]VectorEntry, 0, len(chunks))

	for _, c := range chunks {
		text := c.Content
		if len(text) > 2000 { // truncate long texts for embedding
			runes := []rune(text)
			if len(runes) > 2000 {
				text = string(runes[:2000])
			}
		}
		vec, err := EmbedText(ctx, text)
		if err != nil {
			log.Printf("[Vector] Embed chunk %d (doc=%s): %v", c.ChunkIndex, c.SourceDoc, err)
			continue
		}
		entries = append(entries, VectorEntry{
			ChunkID: c.ID,
			KBID:    kbID,
			DocName: c.SourceDoc,
			Heading: c.Heading,
			Content: c.Content,
			Vector:  vec,
		})
	}

	if len(entries) > 0 {
		AddVectors(kbID, entries)
		SaveVectorIndex(kbID)
		log.Printf("[Vector] Indexed %d vectors for KB %d", len(entries), kbID)
	}
	return nil
}

// indexChunkWithEmbedding embeds and indexes a single chunk.
func IndexChunkWithEmbedding(chunk *ragmodel.Chunk) {
	if !EmbedderEnabled() || chunk == nil {
		return
	}

	text := chunk.Content
	if len(text) > 2000 {
		runes := []rune(text)
		if len(runes) > 2000 {
			text = string(runes[:2000])
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	vec, err := EmbedText(ctx, text)
	if err != nil {
		log.Printf("[Vector] Embed chunk %d (doc=%s): %v", chunk.ChunkIndex, chunk.SourceDoc, err)
		return
	}

	AddVector(chunk.KbID, VectorEntry{
		ChunkID: chunk.ID,
		KBID:    chunk.KbID,
		DocName: chunk.SourceDoc,
		Heading: chunk.Heading,
		Content: chunk.Content,
		Vector:  vec,
	})
	SaveVectorIndex(chunk.KbID)
}
