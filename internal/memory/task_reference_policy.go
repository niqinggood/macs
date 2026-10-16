package memory

import (
	"encoding/json"
	"strings"
	"unicode"
)

// TaskReferenceSelection only controls future automatic candidates. It never
// removes task history or turns an assistant's answer into a user preference.
type TaskReferenceSelection struct {
	IncludeResult  bool
	IncludeInsight bool
	Reason         string
}

type learningEventEvidence struct {
	Kind          string       `json:"kind"`
	SourceEventID uint         `json:"source_event_id"`
	Tools         []SourceTool `json:"tools"`
}

// TaskReferenceTools reads execution outcomes, not model requests or an
// answer's claim that a tool ran. Arguments and outputs are never copied.
func TaskReferenceTools(raw []byte) []SourceTool {
	if len(raw) > 1<<20 {
		return nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make([]SourceTool, 0, min(len(values), 50))
	for _, rawValue := range values {
		var value struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Success *bool  `json:"success"`
		}
		if json.Unmarshal(rawValue, &value) != nil {
			continue
		}
		if (value.Type != "tool" && value.Type != "skill") || strings.TrimSpace(value.Name) == "" || len(value.Name) > 200 || strings.IndexFunc(value.Name, unicode.IsControl) >= 0 || value.Success == nil {
			continue
		}
		result = append(result, SourceTool{Type: value.Type, Name: value.Name, Success: value.Success})
		if len(result) == 50 {
			break
		}
	}
	return result
}

func taskLearningEvidence(event MemoryEvent) (learningEventEvidence, bool) {
	var envelope struct {
		Learning learningEventEvidence `json:"memory_learning"`
	}
	if len(event.OutputMeta) > 64<<10 || json.Unmarshal(event.OutputMeta, &envelope) != nil {
		return learningEventEvidence{}, false
	}
	evidence := envelope.Learning
	if evidence.SourceEventID == 0 || evidence.SourceEventID == event.ID || (evidence.Kind != "failure_lesson" && evidence.Kind != "skill_hint") || len(evidence.Tools) == 0 || len(evidence.Tools) > 50 {
		return learningEventEvidence{}, false
	}
	encoded, _ := json.Marshal(evidence.Tools)
	if len(TaskReferenceTools(encoded)) != len(evidence.Tools) {
		return learningEventEvidence{}, false
	}
	return evidence, true
}

// TaskReferenceLearningSource is only a lookup hint. The host must authorize
// the original event before passing it to SelectTaskMemoryReferences.
func TaskReferenceLearningSource(event MemoryEvent) uint {
	evidence, ok := taskLearningEvidence(event)
	if !ok {
		return 0
	}
	return evidence.SourceEventID
}

func verifiedLearningEvent(event MemoryEvent, parent *MemoryEvent) bool {
	evidence, ok := taskLearningEvidence(event)
	if !ok || parent == nil || evidence.SourceEventID != parent.ID || event.TenantID != parent.TenantID || event.AgentName != parent.AgentName || event.UserID != parent.UserID || event.SessionID != parent.SessionID || event.UserChannel != parent.UserChannel {
		return false
	}
	type receipt struct {
		kind, name string
		success    bool
	}
	available := make(map[receipt]int)
	for _, tool := range TaskReferenceTools(parent.CallChain) {
		available[receipt{tool.Type, tool.Name, *tool.Success}]++
	}
	for _, tool := range evidence.Tools {
		key := receipt{tool.Type, tool.Name, *tool.Success}
		if available[key] == 0 {
			return false
		}
		available[key]--
	}
	return true
}

// SelectTaskMemoryReferences selects useful original representations using
// persisted structural evidence. No greeting list, wording heuristic, model
// preference inference, or filesystem access is involved.
func SelectTaskMemoryReferences(event MemoryEvent, learningParent *MemoryEvent) TaskReferenceSelection {
	selection := TaskReferenceSelection{}
	if event.Status != "success" && event.Status != "failed" {
		return selection
	}
	switch {
	case len(TaskReferenceTools(event.CallChain)) > 0:
		selection.Reason = "execution_receipt"
	case strings.TrimSpace(event.UserFeedback) != "":
		selection.Reason = "user_feedback"
	case event.Status == "failed" && strings.TrimSpace(event.ErrorMsg) != "":
		selection.Reason = "failure_record"
	case verifiedLearningEvent(event, learningParent):
		selection.Reason = "derived_execution_learning"
	default:
		return selection
	}
	result := strings.Join(strings.Fields(event.FinalAnswer), " ")
	insight := event.LearnNote
	if strings.TrimSpace(insight) == "" && len(event.Insights) <= 64<<10 {
		var values []string
		if json.Unmarshal(event.Insights, &values) == nil {
			insight = strings.Join(values, "\n")
		}
	}
	insight = strings.Join(strings.Fields(insight), " ")
	selection.IncludeResult = result != ""
	// An identical excerpt of this same task adds no independent lesson. Keep
	// the fuller result and leave both original fields untouched for history.
	selection.IncludeInsight = insight != "" && (result == "" || !strings.Contains(result, insight))
	return selection
}
