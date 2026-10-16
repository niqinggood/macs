package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

const recallTraceRetention = 7 * 24 * time.Hour
const maxRecallTraces = 100

type recallTraceRecord struct {
	ID               string `gorm:"primaryKey;size:64"`
	Namespace        string `gorm:"index:idx_memory_recall_scope_time,priority:1;size:64;not null"`
	SessionRef       string `gorm:"size:64"`
	TurnRef          string `gorm:"size:64"`
	Status           string `gorm:"size:16"`
	Tokens           int
	Decisions        string `gorm:"type:text"`
	OmittedDecisions int
	SemanticStatus   string    `gorm:"size:16"`
	CreatedAt        time.Time `gorm:"index:idx_memory_recall_scope_time,priority:2"`
}

func (recallTraceRecord) TableName() string { return "memory_recall_traces" }

type RecallTrace struct {
	ID               string           `json:"id"`
	SessionRef       string           `json:"session_ref,omitempty"`
	TurnRef          string           `json:"turn_ref,omitempty"`
	Status           string           `json:"status"`
	Tokens           int              `json:"tokens"`
	Decisions        []RecallDecision `json:"decisions"`
	OmittedDecisions int              `json:"omitted_decisions"`
	SemanticStatus   string           `json:"semantic_status,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
}

func traceSource(source string) string {
	switch source {
	case "summary", "session_context", "history", "history_and_profile", "knowledge_graph",
		"user_statement", "user_feedback", "task_execution", "task_insight", "learning_knowledge", "knowledge_fact":
		return source
	}
	for _, scope := range []MemoryScope{MemoryUser, MemoryProject, MemorySession, MemoryGlobal} {
		for _, kind := range []string{"preference", "fact", "decision", "experience"} {
			if source == "long_term:"+string(scope)+":"+kind {
				return source
			}
		}
	}
	return "supplemental"
}

func traceReason(reason string) string {
	switch reason {
	case "session_working", "relevant", "semantic", "hybrid", "pinned", "scope_override", "not_relevant", "retracted", "duplicate", "budget", "pinned_budget", "safety_filter", "stale", "version_changed", "disabled_by_user":
		return reason
	}
	return "unspecified"
}

// SaveRecallTrace records selection metadata after the host's safety scan.
// Failure is diagnostic only: the host must not fail a user's turn for this.
// No question, key, content, raw session/task reference, or source quote is saved.
func (s *Service) SaveRecallTrace(ctx context.Context, owner Owner, turnRef, status string, result RecallResult) error {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	if status != "ready" && status != "empty" && status != "blocked" && status != "redacted" {
		return errors.New("invalid recall trace status")
	}
	decisions := make([]RecallDecision, 0, min(len(result.Decisions), maxRecallDecisions))
	for _, decision := range result.Decisions[:min(len(result.Decisions), maxRecallDecisions)] {
		if decoded, err := hex.DecodeString(decision.ID); err != nil || len(decoded) != 32 {
			decision.ID = memoryHash(namespace, decision.ID)
		}
		decision.Source = traceSource(decision.Source)
		decision.Reason = traceReason(decision.Reason)
		if status == "blocked" && decision.Selected {
			decision.Selected = false
			decision.Reason = "safety_filter"
		}
		decisions = append(decisions, decision)
	}
	data, err := json.Marshal(decisions)
	if err != nil {
		return err
	}
	now := time.Now()
	record := recallTraceRecord{ID: rand.Text(), Namespace: namespace, Status: status, Tokens: result.Tokens, Decisions: string(data), OmittedDecisions: result.OmittedDecisions + max(0, len(result.Decisions)-maxRecallDecisions), CreatedAt: now}
	switch result.SemanticStatus {
	case "disabled", "ready", "partial", "unavailable":
		record.SemanticStatus = result.SemanticStatus
	}
	if strings.TrimSpace(owner.SessionID) != "" {
		record.SessionRef = memoryHash(namespace, owner.SessionID)
	}
	if strings.TrimSpace(turnRef) != "" {
		record.TurnRef = memoryHash(namespace, turnRef)
	}
	if status == "blocked" || status == "empty" {
		record.Tokens = 0
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := tx.Where("namespace = ? AND created_at < ?", namespace, now.Add(-recallTraceRetention)).Delete(&recallTraceRecord{}).Error; err != nil {
			return err
		}
		var keep []string
		if err := tx.Model(&recallTraceRecord{}).Where("namespace = ?", namespace).Order("created_at DESC, id DESC").Limit(maxRecallTraces).Pluck("id", &keep).Error; err != nil {
			return err
		}
		return tx.Where("namespace = ? AND id NOT IN ?", namespace, keep).Delete(&recallTraceRecord{}).Error
	})
}

func (s *Service) RecentRecallTraces(ctx context.Context, owner Owner, limit int) ([]RecallTrace, error) {
	query, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 20 {
		return nil, errors.New("recall trace limit must be 1-20")
	}
	query = query.Where("created_at >= ?", time.Now().Add(-recallTraceRetention))
	if owner.SessionID != "" {
		query = query.Where("session_ref = ?", memoryHash(namespace, owner.SessionID))
	}
	var records []recallTraceRecord
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	traces := make([]RecallTrace, 0, len(records))
	for _, record := range records {
		trace := RecallTrace{ID: record.ID, SessionRef: record.SessionRef, TurnRef: record.TurnRef, Status: record.Status, Tokens: record.Tokens, OmittedDecisions: record.OmittedDecisions, CreatedAt: record.CreatedAt}
		trace.SemanticStatus = record.SemanticStatus
		if err := json.Unmarshal([]byte(record.Decisions), &trace.Decisions); err != nil {
			return nil, errors.New("invalid stored recall trace")
		}
		traces = append(traces, trace)
	}
	return traces, nil
}
