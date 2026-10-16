package knowledge

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/ragmodel"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

type ChunkLoader func() ([]ragmodel.Chunk, error)

// BleveIndex owns the keyword index lifecycle and retrieval algorithm. The
// application supplies persisted chunks through a loader so this package does
// not depend on the root database or gateway.
type BleveIndex struct {
	path   string
	loader ChunkLoader

	mu    sync.Mutex
	index bleve.Index
	ready bool
}

func NewBleveIndex(path string, loader ChunkLoader) *BleveIndex {
	return &BleveIndex{path: filepath.Clean(path), loader: loader}
}

func (store *BleveIndex) open() (bleve.Index, error) {
	if store == nil {
		return nil, fmt.Errorf("bleve index is not configured")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.index != nil {
		return store.index, nil
	}

	idx, err := bleve.Open(store.path)
	if err == nil {
		store.index = idx
		store.ready = true
		count, _ := idx.DocCount()
		log.Printf("[RAG] Bleve opened: %d docs", count)
		return idx, nil
	}

	if _, statErr := os.Stat(store.path); statErr == nil {
		backup := store.path + ".bak." + time.Now().Format("20060102_150405")
		log.Printf("[RAG] Bleve open failed (corrupt?): %v — backing up to %s", err, backup)
		if renameErr := os.Rename(store.path, backup); renameErr != nil {
			log.Printf("[RAG] backup rename failed: %v, removing", renameErr)
			if removeErr := os.RemoveAll(store.path); removeErr != nil {
				return nil, fmt.Errorf("remove unusable bleve index: %w", removeErr)
			}
		}
	}
	if err := os.MkdirAll(store.path, 0o755); err != nil {
		return nil, fmt.Errorf("create bleve index directory: %w", err)
	}
	log.Printf("[RAG] Creating new Bleve index at: %s", store.path)

	idx, err = bleve.New(store.path, newRAGIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("bleve create: %w", err)
	}
	store.index = idx

	chunks, err := store.loadInitialChunks()
	if err != nil {
		_ = idx.Close()
		store.index = nil
		return nil, fmt.Errorf("load initial RAG chunks: %w", err)
	}
	if err := indexChunkBatch(idx, chunks); err != nil {
		_ = idx.Close()
		store.index = nil
		return nil, err
	}
	store.ready = true
	return idx, nil
}

func newRAGIndexMapping() mapping.IndexMapping {
	mapping := bleve.NewIndexMapping()
	document := bleve.NewDocumentMapping()

	tokens := bleve.NewTextFieldMapping()
	tokens.Analyzer = "standard"
	tokens.Store = false
	tokens.IncludeTermVectors = true
	tokens.Name = "tokens"
	document.AddFieldMapping(tokens)

	content := bleve.NewTextFieldMapping()
	content.Analyzer = "keyword"
	content.Store = true
	content.Index = false
	content.Name = "content"
	document.AddFieldMapping(content)

	source := bleve.NewTextFieldMapping()
	source.Analyzer = "keyword"
	source.Store = true
	source.Index = false
	source.Name = "source_doc"
	document.AddFieldMapping(source)

	heading := bleve.NewTextFieldMapping()
	heading.Analyzer = "standard"
	heading.Store = true
	heading.IncludeTermVectors = true
	heading.Name = "heading"
	document.AddFieldMapping(heading)

	document.DefaultAnalyzer = "standard"
	mapping.AddDocumentMapping("chunk", document)
	return mapping
}

func (store *BleveIndex) loadInitialChunks() ([]ragmodel.Chunk, error) {
	if store.loader == nil {
		return nil, nil
	}
	return store.loader()
}

func chunkIndexDocument(chunk ragmodel.Chunk) map[string]interface{} {
	return map[string]interface{}{
		"tokens":     ragmodel.SegText(chunk.Content),
		"content":    chunk.Content,
		"source_doc": chunk.SourceDoc,
		"heading":    chunk.Heading,
		"kb_id":      strconv.FormatUint(uint64(chunk.KbID), 10),
	}
}

func indexChunkBatch(index bleve.Index, chunks []ragmodel.Chunk) error {
	if len(chunks) == 0 {
		log.Print("[RAG] No chunks in DB to import")
		return nil
	}
	log.Printf("[RAG] Importing %d chunks from DB...", len(chunks))
	started := time.Now()
	batch := index.NewBatch()
	for _, chunk := range chunks {
		if err := batch.Index(strconv.FormatUint(uint64(chunk.ID), 10), chunkIndexDocument(chunk)); err != nil {
			return fmt.Errorf("prepare bleve chunk batch: %w", err)
		}
		if batch.Size() >= 100 {
			if err := index.Batch(batch); err != nil {
				return fmt.Errorf("index bleve chunk batch: %w", err)
			}
			batch = index.NewBatch()
		}
	}
	if batch.Size() > 0 {
		if err := index.Batch(batch); err != nil {
			return fmt.Errorf("index final bleve chunk batch: %w", err)
		}
	}
	log.Printf("[RAG] Import done: %d chunks in %v", len(chunks), time.Since(started).Round(time.Millisecond))
	return nil
}

func (store *BleveIndex) IndexChunk(chunk *ragmodel.Chunk) error {
	if chunk == nil {
		return fmt.Errorf("cannot index a nil RAG chunk")
	}
	index, err := store.open()
	if err != nil {
		return err
	}
	return index.Index(strconv.FormatUint(uint64(chunk.ID), 10), chunkIndexDocument(*chunk))
}

func (store *BleveIndex) DeleteChunkIDs(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	index, err := store.open()
	if err != nil {
		return err
	}
	batch := index.NewBatch()
	for _, id := range ids {
		batch.Delete(strconv.FormatUint(uint64(id), 10))
	}
	return index.Batch(batch)
}

func (store *BleveIndex) SyncChunks(chunks []ragmodel.Chunk) error {
	index, err := store.open()
	if err != nil {
		return err
	}
	return indexChunkBatch(index, chunks)
}

func (store *BleveIndex) Ready() bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.ready
}

func (store *BleveIndex) DocumentCount() (uint64, error) {
	if !store.Ready() {
		return 0, nil
	}
	index, err := store.open()
	if err != nil {
		return 0, err
	}
	return index.DocCount()
}

func (store *BleveIndex) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.index == nil {
		return nil
	}
	err := store.index.Close()
	store.index = nil
	store.ready = false
	return err
}

// Search performs the BM25 keyword path, including query rewriting, heading
// boosts, scope filters, duplicate removal and matched-token reporting.
func (store *BleveIndex) Search(kbIDs []uint, query, sessionID, sourceDoc string, limit int) ([]RAGResult, error) {
	if limit <= 0 {
		return []RAGResult{}, nil
	}
	index, err := store.open()
	if err != nil {
		return nil, fmt.Errorf("bleve unavailable: %w", err)
	}

	rewritten := ragmodel.RewriteQuery(query)
	if sessionID != "" {
		rewritten = ragmodel.RagEnrichQuery(sessionID, rewritten)
		defer ragmodel.RagRememberQuery(sessionID, query)
	}
	segmented := ragmodel.SegTextForSearch(rewritten)
	log.Printf("[RAG] query=%q → tokens=%q", query, segmented)
	tokens := strings.Fields(segmented)
	if len(tokens) == 0 {
		return []RAGResult{}, nil
	}

	disjunction := bleve.NewDisjunctionQuery()
	weights := ragmodel.TokenWeights(tokens)
	for index, token := range tokens {
		match := bleve.NewMatchQuery(token)
		match.SetField("tokens")
		match.SetBoost(weights[index])
		disjunction.AddQuery(match)
	}
	phrase := bleve.NewMatchPhraseQuery(segmented)
	phrase.SetField("tokens")
	boolean := bleve.NewBooleanQuery()
	boolean.AddMust(disjunction)
	boolean.AddShould(phrase)

	request := bleve.NewSearchRequest(boolean)
	request.Size = limit * 3
	request.Fields = []string{"content", "source_doc", "heading", "kb_id"}
	response, err := index.Search(request)
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}

	keywords := make(map[string]bool, len(tokens))
	for _, keyword := range tokens {
		keywords[strings.ToLower(keyword)] = true
	}
	results := make([]RAGResult, 0, len(response.Hits))
	for _, hit := range response.Hits {
		source, _ := hit.Fields["source_doc"].(string)
		content, _ := hit.Fields["content"].(string)
		heading, _ := hit.Fields["heading"].(string)
		kbID, _ := hit.Fields["kb_id"].(string)
		score := hit.Score
		if heading != "" {
			lowerHeading := strings.ToLower(heading)
			for keyword := range keywords {
				if len(keyword) >= 2 && strings.Contains(lowerHeading, keyword) {
					score *= 1.3
					break
				}
			}
		}
		results = append(results, RAGResult{
			SourceDoc: source,
			Content:   content,
			Score:     score,
			KBID:      kbID,
		})
	}

	if len(kbIDs) > 0 {
		allowed := make(map[uint]bool, len(kbIDs))
		for _, id := range kbIDs {
			allowed[id] = true
		}
		filtered := make([]RAGResult, 0, len(results))
		for _, result := range results {
			parsed, parseErr := strconv.ParseUint(result.KBID, 10, 64)
			if parseErr == nil && allowed[uint(parsed)] {
				filtered = append(filtered, result)
			}
		}
		results = filtered
	}
	if sourceDoc != "" {
		filtered := make([]RAGResult, 0, len(results))
		for _, result := range results {
			if result.SourceDoc == sourceDoc {
				filtered = append(filtered, result)
			}
		}
		results = filtered
	}
	filtered := make([]RAGResult, 0, len(results))
	for _, result := range results {
		if result.Score >= 0.1 {
			filtered = append(filtered, result)
		}
	}
	deduplicated := DeduplicateResults(filtered, limit)
	for index := range deduplicated {
		deduplicated[index].MatchedTokens = FindMatchedTokens(deduplicated[index].Content, tokens)
	}
	return deduplicated, nil
}
