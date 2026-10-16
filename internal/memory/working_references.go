package memory

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// WorkingReference is a read-only source, not an editable Entry. Its IDs
// cannot be submitted to the keyed Entry writer to overwrite SCM or summaries.
type WorkingReference struct {
	Tags              []string          `json:"tags,omitempty"`
	ID                string            `json:"id"`
	Key               string            `json:"key"`
	Value             string            `json:"value"`
	OriginalContent   string            `json:"original_content,omitempty"`
	ContentTruncated  bool              `json:"content_truncated,omitempty"`
	Scope             MemoryScope       `json:"scope"`
	Kind              string            `json:"kind"`
	SourceType        string            `json:"source_type"`
	SessionID         string            `json:"session_id"`
	CreatedAt         *time.Time        `json:"created_at,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
	ReadOnly          bool              `json:"read_only"`
	SourceID          string            `json:"source_id,omitempty"`
	AgentName         string            `json:"agent_name,omitempty"`
	SourceLocation    string            `json:"source_location,omitempty"`
	OwnershipVerified *bool             `json:"ownership_verified,omitempty"`
	Evidence          string            `json:"user_evidence,omitempty"`
	TaskStatus        string            `json:"task_status,omitempty"`
	Tools             []SourceTool      `json:"tools,omitempty"`
	Feedback          string            `json:"user_feedback,omitempty"`
	Rating            *int              `json:"user_rating,omitempty"`
	Graph             *GraphSource      `json:"graph,omitempty"`
	Provenance        *RecordProvenance `json:"provenance,omitempty"`
}

// SourceTool exposes an execution receipt's identity and outcome only. Tool
// arguments, provider errors and raw input/output never enter this DTO.
type SourceTool struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Success    *bool  `json:"success,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

// WorkingReferences reads only this namespace's selected session and the
// matching workspace's SCM. The trusted host validates the session first.
func (s *Service) WorkingReferences(ctx context.Context, owner Owner) ([]WorkingReference, error) {
	query, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if s.global || owner.SessionID == "" {
		return nil, errors.New("working references require a local session")
	}
	result := make([]WorkingReference, 0)
	if s.contextStore != nil {
		items := filterContextItemsForWorkspace(filterInternalExecutionContextItems(s.contextStore.ListVisible("", owner.SessionID, 0)), s.workspace)
		for _, item := range items {
			if item.Durable || item.SessionID != owner.SessionID {
				continue
			}
			var created *time.Time
			if !item.CreatedAt.IsZero() {
				timestamp := item.CreatedAt
				created = &timestamp
			}
			kind := "fact"
			if item.Category == "preference" || item.Category == "decision" {
				kind = item.Category
			}
			result = append(result, WorkingReference{ID: "scm:" + memoryHash(namespace, owner.SessionID, item.Key), Key: item.Key, Value: item.Value, Scope: MemorySession, Kind: kind, SourceType: "session_context", SessionID: owner.SessionID, CreatedAt: created, UpdatedAt: item.UpdatedAt, ReadOnly: true})
			if len(result) >= 100 {
				break
			}
		}
	}
	var summary Summary
	err = query.Where("session_id = ?", owner.SessionID).First(&summary).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil && summary.Content != "" {
		// SourceCreatedAt is the input snapshot time, not a fabricated creation
		// time for a summary that can be revised. Expose only its actual update.
		result = append(result, WorkingReference{ID: "summary:" + summary.ID, SourceID: summary.ID, Key: "summary", Value: summary.Content, Scope: MemorySession, Kind: "fact", SourceType: "summary", SessionID: owner.SessionID, UpdatedAt: summary.UpdatedAt, ReadOnly: true})
	}
	return result, nil
}
