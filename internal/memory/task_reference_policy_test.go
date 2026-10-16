package memory

import (
	"strings"
	"testing"

	"gorm.io/datatypes"
)

func TestTaskReferencePolicyUsesEvidenceInsteadOfQuestionWording(t *testing.T) {
	for _, test := range []struct {
		name            string
		event           MemoryEvent
		result, insight bool
	}{
		{"plain_answer", MemoryEvent{Status: "success", UserQuestion: "hi", FinalAnswer: "Hello", LearnNote: "Hello"}, false, false},
		{"long_answer_no_receipt", MemoryEvent{Status: "success", FinalAnswer: strings.Repeat("A detailed explanation. ", 100), LearnNote: "A detailed explanation."}, false, false},
		{"claimed_output", MemoryEvent{Status: "success", OutputType: "file", OutputPath: "report.docx", FinalAnswer: "I generated report.docx"}, false, false},
		{"short_request_real_tool", MemoryEvent{Status: "success", UserQuestion: "好", FinalAnswer: "The verified document was saved.", CallChain: datatypes.JSON(`[{"type":"tool","name":"write_file","success":true}]`)}, true, false},
		{"real_failed_tool", MemoryEvent{Status: "failed", FinalAnswer: "Connection rejected", LearnNote: "Check server configuration before retrying", CallChain: datatypes.JSON(`[{"type":"tool","name":"fetch","success":false}]`)}, true, true},
		{"host_failure", MemoryEvent{Status: "failed", FinalAnswer: "Unable to complete", ErrorMsg: "Upstream timeout"}, true, false},
		{"pending", MemoryEvent{Status: "pending", FinalAnswer: "Still running", UserFeedback: "Please continue"}, false, false},
		{"explicit_feedback", MemoryEvent{Status: "success", FinalAnswer: "Selected layout", UserFeedback: "The grouped layout is suitable"}, true, false},
		{"llm_only", MemoryEvent{Status: "success", FinalAnswer: "Generic explanation", CallChain: datatypes.JSON(`[{"type":"llm","name":"completion","success":true}]`)}, false, false},
		{"unknown_outcome", MemoryEvent{Status: "success", FinalAnswer: "Generic explanation", CallChain: datatypes.JSON(`[{"type":"tool","name":"write_file"}]`)}, false, false},
		{"invalid_outcome", MemoryEvent{Status: "success", FinalAnswer: "Generic explanation", CallChain: datatypes.JSON(`[{"type":"tool","name":"write_file","success":"true"}]`)}, false, false},
		{"result_fragment", MemoryEvent{Status: "success", FinalAnswer: "First verify inputs.\nThen inspect outputs.", LearnNote: "verify inputs.", UserFeedback: "Good"}, true, false},
		{"whitespace_equivalent", MemoryEvent{Status: "success", FinalAnswer: "Verify  inputs.\r\nThen inspect outputs.", LearnNote: "Verify inputs. Then inspect outputs.", UserFeedback: "Good"}, true, false},
		{"independent_lesson", MemoryEvent{Status: "success", FinalAnswer: "Saved report", LearnNote: "Use explicit locale when parsing dates", UserFeedback: "Good"}, true, true},
		{"insights_fallback", MemoryEvent{Status: "failed", ErrorMsg: "Failure", Insights: datatypes.JSON(`["Use a bounded retry"]`)}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := SelectTaskMemoryReferences(test.event, nil)
			if selection.IncludeResult != test.result || selection.IncludeInsight != test.insight {
				t.Fatalf("selection=%+v", selection)
			}
		})
	}
}

func TestTaskReferenceLearningRequiresOriginalExecution(t *testing.T) {
	parent := MemoryEvent{ID: 17, TenantID: "default", AgentName: "assistant", UserID: "alice", SessionID: "chat", UserChannel: "web", Status: "failed", CallChain: datatypes.JSON(`[{"type":"tool","name":"browser","success":false,"error":"PRIVATE_ERROR","input_summary":"PRIVATE_INPUT"}]`)}
	for _, kind := range []string{"failure_lesson", "skill_hint"} {
		derived := NewLearningEventWithEvidence(&parent, kind, "host generated", "Verify the prerequisite before retrying")
		if len(derived.CallChain) != 0 || strings.Contains(string(derived.OutputMeta), "PRIVATE") {
			t.Fatal("derived learning copied tool stats or private execution text")
		}
		if TaskReferenceLearningSource(*derived) != parent.ID || !SelectTaskMemoryReferences(*derived, &parent).IncludeResult {
			t.Fatal("real derived lesson was lost")
		}
		if SelectTaskMemoryReferences(*derived, nil).IncludeResult {
			t.Fatal("unresolved parent was trusted")
		}
		foreign := parent
		foreign.UserID = "bob"
		if SelectTaskMemoryReferences(*derived, &foreign).IncludeResult {
			t.Fatal("foreign parent was trusted")
		}
		changed := parent
		changed.CallChain = datatypes.JSON(`[{"type":"tool","name":"browser","success":true}]`)
		if SelectTaskMemoryReferences(*derived, &changed).IncludeResult {
			t.Fatal("changed original receipt was trusted")
		}
	}
	old := NewLearningEvent(&parent, "lessons learned", "legacy title alone")
	if SelectTaskMemoryReferences(*old, &parent).IncludeResult {
		t.Fatal("title inferred a learning classification")
	}
}

func TestTaskReferenceToolsBoundsAndMixedInvalidRows(t *testing.T) {
	raw := []byte(`[{"type":"llm","name":"model","success":true},{"type":"tool","name":"invalid","success":"true"},{"type":"skill","name":"report","success":false,"duration_ms":"ignored","input_summary":"private"}]`)
	tools := TaskReferenceTools(raw)
	if len(tools) != 1 || tools[0].Name != "report" || *tools[0].Success {
		t.Fatalf("unexpected receipts: %+v", tools)
	}
	if len(TaskReferenceTools([]byte(strings.Repeat(" ", 1<<20+1)))) != 0 {
		t.Fatal("oversized input accepted")
	}
	if NewLearningEventWithEvidence(nil, "failure_lesson", "a", "b") != nil {
		t.Fatal("nil source accepted")
	}
}
