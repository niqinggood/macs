package memory

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	knowledgetools "mac/internal/tools/knowledge"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RecallEmbedding is a stable snapshot of one model and its embedding function.
// ModelID must change when the model/vector space changes; credentials are not IDs.
type RecallEmbedding struct {
	ModelID string
	Embed   func(context.Context, string) ([]float64, error)
}

func WithRecallEmbedding(source func() RecallEmbedding) ServiceOption {
	return func(s *Service) { s.recallEmbedding = source }
}

func configuredRecallEmbedding() RecallEmbedding {
	if !memoryEmbedEnabled.Load() {
		return RecallEmbedding{}
	}
	provider, id := knowledgetools.EmbeddingSnapshot()
	if provider == nil {
		return RecallEmbedding{}
	}
	return RecallEmbedding{ModelID: id, Embed: provider.Embed}
}

// Vectors contain no duplicate plaintext. One row per source is replaced when
// the model or revision changes, rather than accumulating obsolete model data.
type recallVector struct {
	ID        string `gorm:"primaryKey;size:64"`
	Namespace string `gorm:"index;size:64;not null"`
	Source    string `gorm:"size:16;not null"`
	SourceID  string `gorm:"size:64;not null"`
	ModelID   string `gorm:"size:64;not null"`
	Revision  string `gorm:"size:64;not null"`
	Vector    string `gorm:"type:text"`
	UpdatedAt time.Time
}

func (recallVector) TableName() string { return "memory_recall_vectors" }

const (
	semanticRecallTimeout     = 3 * time.Second
	semanticRecallPageSize    = 64
	semanticBackfillPerSource = 4
	semanticRecallLimit       = 12
	semanticRecallThreshold   = 0.60
	maxRecallVectorDimension  = 16384
)

type semanticDocument struct {
	id, source, revision, text string
	entry                      *Entry
	summary                    *Summary
}

func semanticEntryDocument(e *Entry) semanticDocument {
	text := e.Key + ": " + e.Value + " " + e.Tags
	return semanticDocument{id: e.ID, source: "entry", revision: memoryHash(strconv.Itoa(e.Version), text), text: text, entry: e}
}

func semanticSummaryDocument(x *Summary) semanticDocument {
	return semanticDocument{id: x.ID, source: "summary", revision: memoryHash(strconv.FormatInt(x.SourceRevision, 10), x.Content), text: x.Content, summary: x}
}

type semanticHit struct {
	doc   semanticDocument
	score float64
}

type semanticRecallResult struct {
	hits   []semanticHit
	status string
}

// semanticRecall scans the namespace in bounded pages, not just the recent
// lexical pool. Cached candidates are scored before bounded backfill so a slow
// embedding request cannot hide already indexed entries later in the scan.
func (s *Service) semanticRecall(parent context.Context, owner Owner, question string, blocked func(string) bool) semanticRecallResult {
	result := semanticRecallResult{status: "disabled"}
	if s.recallEmbedding == nil || strings.TrimSpace(question) == "" {
		return result
	}
	backend := s.recallEmbedding()
	if backend.Embed == nil || backend.ModelID == "" {
		return result
	}
	ctx, cancel := context.WithTimeout(parent, semanticRecallTimeout)
	defer cancel()
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		result.status = "unavailable"
		return result
	}
	query, err := s.recallQuery(ctx, namespace, backend, question)
	query, valid := normalizedRecallVector(query)
	if err != nil || !valid || ctx.Err() != nil {
		result.status = "unavailable"
		return result
	}
	result.status = "ready"
	modelID := memoryHash("recall-v1", backend.ModelID)
	var pending []semanticDocument
	for _, source := range []string{"entry", "summary"} {
		remaining := semanticBackfillPerSource
		// Read indexed candidates first. In a cold namespace, the missing-vector
		// pass only reads enough entries to fill this turn's bounded work list.
		// Scanning the entire unindexed history would otherwise consume the
		// deadline before any progress could be persisted.
		for _, indexed := range []bool{true, false} {
			cursor := ""
		scan:
			for {
				docs, err := s.semanticDocuments(ctx, namespace, owner.SessionID, source, cursor, modelID, indexed)
				if err != nil {
					result.status = "partial"
					return result
				}
				if len(docs) == 0 {
					break
				}
				ids := make([]string, 0, len(docs))
				for _, doc := range docs {
					ids = append(ids, doc.id)
				}
				var rows []recallVector
				if indexed {
					if err := s.db.WithContext(ctx).Where("namespace = ? AND model_id = ? AND source = ? AND source_id IN ?", namespace, modelID, source, ids).Find(&rows).Error; err != nil {
						result.status = "partial"
						return result
					}
				}
				cached := make(map[string]recallVector, len(rows))
				for _, row := range rows {
					cached[row.SourceID] = row
				}
				for _, doc := range docs {
					if ctx.Err() != nil {
						result.status = "partial"
						return result
					}
					// Active entries are authoritative; summaries remain historical and
					// must obey correction/deletion retractions before indexing as well.
					if strings.TrimSpace(doc.text) == "" || doc.summary != nil && blocked(doc.text) {
						continue
					}
					var vector []float64
					if row, ok := cached[doc.id]; ok && row.Revision == doc.revision && len(row.Vector) <= 1024*1024 {
						_ = json.Unmarshal([]byte(row.Vector), &vector)
					}
					vector, valid := normalizedRecallVector(vector)
					if !valid || len(vector) != len(query) {
						if remaining <= 0 {
							result.status = "partial"
							if !indexed {
								break scan
							}
							continue
						}
						remaining--
						pending = append(pending, doc)
						continue
					}
					result.consider(doc, query, vector)
				}
				cursor = docs[len(docs)-1].id
				if len(docs) < semanticRecallPageSize {
					break
				}
			}
		}
	}
	// Use the remaining recall deadline, rather than a shorter per-document
	// timeout that can permanently prevent slower providers from warming up.
	// Ready cached hits survive a failed/slow backfill; no worker is detached.
	for _, doc := range pending {
		if ctx.Err() != nil {
			result.status = "partial"
			break
		}
		vector, err := backend.Embed(ctx, truncateSessionRunes(doc.text, 2000))
		vector, valid := normalizedRecallVector(vector)
		if err != nil || !valid || len(vector) != len(query) {
			result.status = "partial"
			break
		}
		if err := s.saveRecallVector(ctx, namespace, modelID, doc, vector); err != nil {
			result.status = "partial"
			continue
		}
		result.consider(doc, query, vector)
	}
	return result
}

func (r *semanticRecallResult) consider(doc semanticDocument, query, vector []float64) {
	score := 0.0
	for i := range query {
		score += query[i] * vector[i]
	}
	if score < semanticRecallThreshold {
		return
	}
	r.hits = append(r.hits, semanticHit{doc: doc, score: score})
	sort.SliceStable(r.hits, func(i, j int) bool { return r.hits[i].score > r.hits[j].score })
	if len(r.hits) > semanticRecallLimit {
		r.hits = r.hits[:semanticRecallLimit]
	}
}

func (s *Service) semanticDocuments(ctx context.Context, namespace, session, source, cursor, modelID string, indexed bool) ([]semanticDocument, error) {
	query := s.db.WithContext(ctx).Where("namespace = ? AND id > ?", namespace, cursor).Order("id ASC").Limit(semanticRecallPageSize)
	index := s.db.WithContext(ctx).Model(&recallVector{}).Select("source_id").Where("namespace = ? AND model_id = ? AND source = ?", namespace, modelID, source)
	if indexed {
		query = query.Where("id IN (?)", index)
	} else {
		query = query.Where("id NOT IN (?)", index)
	}
	var docs []semanticDocument
	if source == "entry" {
		var entries []Entry
		if err := query.Where("state = ? AND (scope <> ? OR session_id = ?)", "active", MemorySession, session).Find(&entries).Error; err != nil {
			return nil, err
		}
		for i := range entries {
			docs = append(docs, semanticEntryDocument(&entries[i]))
		}
	} else {
		var summaries []Summary
		if err := query.Find(&summaries).Error; err != nil {
			return nil, err
		}
		for i := range summaries {
			docs = append(docs, semanticSummaryDocument(&summaries[i]))
		}
	}
	return docs, nil
}

func (s *Service) saveRecallVector(ctx context.Context, namespace, modelID string, doc semanticDocument, vector []float64) error {
	encoded, _ := json.Marshal(vector)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A conditional no-op write fences correction/deletion while the
		// embedding request was in flight. Keep this lock until the cache commit.
		var fence *gorm.DB
		if e := doc.entry; e != nil {
			fence = tx.Model(&Entry{}).Where("namespace = ? AND id = ? AND version = ? AND state = ?", namespace, e.ID, e.Version, "active").UpdateColumn("version", gorm.Expr("version"))
		} else {
			x := doc.summary
			fence = tx.Model(&Summary{}).Where("namespace = ? AND id = ? AND source_revision = ? AND content = ?", namespace, x.ID, x.SourceRevision, x.Content).UpdateColumn("source_revision", gorm.Expr("source_revision"))
			// Retractions may have changed while embedding this summary.
			var retractions []memoryRetraction
			if err := tx.Where("namespace = ?", namespace).Find(&retractions).Error; err != nil {
				return err
			}
			if retractionFilter(ctx, retractions)(x.Content) {
				return ErrMemorySuperseded
			}
		}
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			// MySQL without clientFoundRows reports zero for a matched no-op
			// UPDATE. That UPDATE still holds the row lock; verify its predicate
			// inside this transaction rather than rejecting valid cache writes.
			var count int64
			current := tx
			if e := doc.entry; e != nil {
				current = current.Model(&Entry{}).Where("namespace = ? AND id = ? AND version = ? AND state = ?", namespace, e.ID, e.Version, "active")
			} else {
				x := doc.summary
				current = current.Model(&Summary{}).Where("namespace = ? AND id = ? AND source_revision = ? AND content = ?", namespace, x.ID, x.SourceRevision, x.Content)
			}
			if err := current.Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return ErrMemorySuperseded
			}
		}
		row := recallVector{ID: memoryHash(namespace, doc.source, doc.id), Namespace: namespace, Source: doc.source, SourceID: doc.id, ModelID: modelID, Revision: doc.revision, Vector: string(encoded), UpdatedAt: time.Now()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"model_id", "revision", "vector", "updated_at"})}).Create(&row).Error
	})
}

func normalizedRecallVector(vector []float64) ([]float64, bool) {
	if len(vector) == 0 || len(vector) > maxRecallVectorDimension {
		return nil, false
	}
	norm := 0.0
	for _, x := range vector {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		norm = math.Hypot(norm, x)
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return nil, false
	}
	result := make([]float64, len(vector))
	for i := range vector {
		result[i] = vector[i] / norm
	}
	return result, true
}
