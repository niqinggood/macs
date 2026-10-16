package memory

import (
	"encoding/json"
	"time"
)

// NewLearningEvent inherits the full trusted identity of the source turn.
// It intentionally does not copy prompts, artifacts or user feedback.
func NewLearningEvent(source *MemoryEvent, question, answer string) *MemoryEvent {
	if source == nil {
		return nil
	}
	now := time.Now()
	return &MemoryEvent{TenantID: source.TenantID, AgentName: source.AgentName,
		AgentType: source.AgentType, UserID: source.UserID, UserName: source.UserName,
		UserChannel: source.UserChannel, SessionID: source.SessionID,
		UserQuestion: question, FinalAnswer: answer, Status: "success", StartTime: now, EndTime: now}
}

// NewLearningEventWithEvidence links a derived lesson to its original
// execution. Receipts stay in metadata so saving the lesson cannot increment
// the original tools' execution statistics a second time.
func NewLearningEventWithEvidence(source *MemoryEvent, kind, question, answer string) *MemoryEvent {
	event := NewLearningEvent(source, question, answer)
	if event == nil || source.ID == 0 || (kind != "failure_lesson" && kind != "skill_hint") {
		return event
	}
	tools := TaskReferenceTools(source.CallChain)
	if len(tools) == 0 {
		return event
	}
	event.OutputMeta, _ = json.Marshal(map[string]learningEventEvidence{"memory_learning": {Kind: kind, SourceEventID: source.ID, Tools: tools}})
	return event
}
