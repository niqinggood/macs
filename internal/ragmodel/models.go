package ragmodel

import (
	"time"

	"gorm.io/gorm"
)

// RAGKb represents a knowledge base partition for RAG retrieval.
// Each tenant can create multiple Kbs; digital employees mount them for domain-specific search.
type Kb struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	Slug        string         `gorm:"size:100;uniqueIndex:idx_kb_slug_tenant;not null" json:"slug"` // unique per tenant (e.g. "product_manual")
	Name        string         `gorm:"size:200;not null" json:"name"`
	Description string         `gorm:"size:1000" json:"description"`
	TenantID    string         `gorm:"uniqueIndex:idx_kb_slug_tenant;index;size:100" json:"tenant_id"`
	DocCount    int            `gorm:"default:0" json:"doc_count"`
	ChunkCount  int            `gorm:"default:0" json:"chunk_count"`
	BM25Weight  float64        `gorm:"default:0.5" json:"bm25_weight"`           // 0=full vector, 1=full BM25, default 0.5
	BuildStatus string         `gorm:"size:20;default:idle" json:"build_status"` // idle/building/ready/failed
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName preserves the database contract from the former RAGKb type.
// Renaming the Go type during package extraction must not create a new `kbs`
// table or collide with indexes that already belong to `rag_kbs`.
func (Kb) TableName() string { return "rag_kbs" }

// RAGChunk is a text chunk from a document in a RAG knowledge base.
// Chunks are pre-computed on upload and stored for fast retrieval.
type Chunk struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	KbID       uint      `gorm:"index;not null" json:"kb_id"`
	SourceDoc  string    `gorm:"size:500;not null" json:"source_doc"` // original filename
	ChunkIndex int       `gorm:"not null" json:"chunk_index"`
	Heading    string    `gorm:"size:500" json:"heading"`
	FileHash   string    `gorm:"size:64" json:"file_hash,omitempty"` // nearest heading at chunk time
	Content    string    `gorm:"type:longtext" json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName preserves the database contract from the former RAGChunk type.
func (Chunk) TableName() string { return "rag_chunks" }

// RAGFAQ is a question-answer pair for fast exact/similar matching.
// Checked before full-text search; hit means instant answer, no LLM needed.
type FAQ struct {
	ID            uint           `gorm:"primarykey" json:"id"`
	KbID          uint           `gorm:"index;not null" json:"kb_id"`
	Question      string         `gorm:"type:text;not null" json:"question"`
	Answer        string         `gorm:"type:text;not null" json:"answer"`
	HitCount      int            `gorm:"default:0" json:"hit_count"`
	EmbeddingJSON string         `gorm:"type:text" json:"embedding_json,omitempty"` // JSON array of float64, set when embedder enabled
	TenantID      string         `gorm:"index;size:100" json:"tenant_id"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName preserves the database contract from the former RAGFAQ type.
func (FAQ) TableName() string { return "ragfaqs" }

// RAGSearchLog records queries for analytics and gap discovery.
type SearchLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	KbID      uint      `gorm:"index" json:"kb_id"`
	Query     string    `gorm:"type:text" json:"query"`
	HitCount  int       `gorm:"default:0" json:"hit_count"`
	Feedback  string    `gorm:"size:10" json:"feedback"` // "up", "down", or empty
	SessionID string    `gorm:"size:200" json:"session_id"`
	TenantID  string    `gorm:"index;size:100" json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName preserves the database contract from the former RAGSearchLog type.
func (SearchLog) TableName() string { return "rag_search_logs" }
