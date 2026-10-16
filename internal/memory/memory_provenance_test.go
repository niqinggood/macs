package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryProvenanceSeparatesIdentityContentEvidenceAndRecall(t *testing.T) {
	enabled, verified, unverified := true, true, false
	facts := ProvenanceFacts{Origin: ProvenanceOrigin{Location: "memory_events", RecordID: "7", EventID: "7"}, RuntimeEnabled: &enabled, EventLinked: true}
	p := SourceProvenance(WorkingReference{ID: "insight:7", SourceType: "task_insight", OwnershipVerified: &verified}, facts)
	if p.Identity != "verified" || p.Content != "model_generated" || p.Evidence.UserQuote != "missing" || p.Evidence.TaskLink != "missing" || p.Evidence.EventLink != "verified" || p.Recall.Path != "scoped_source_adapter" || p.Recall.Eligibility != "conditional" || !containsProvenanceCode(p.Missing, "original_task_id_missing") {
		t.Fatalf("event ownership was confused with quote, task or factual confirmation: %#v", p)
	}
	archive := SourceProvenance(WorkingReference{ID: "old", SourceType: "legacy_preference", OwnershipVerified: &unverified}, ProvenanceFacts{QuoteStored: true, VerifiedQuote: "unsupported archived quote"})
	if archive.Identity != "workspace_archive" || archive.Content != "historical_unverified" || archive.Evidence.UserQuote != "stored_unverified" || archive.Evidence.VerifiedQuote != "" || archive.Recall.Eligibility != "archive_only" || archive.Recall.Enabled != nil {
		t.Fatal("archive was activated or its quote invented", archive)
	}
	entry := Entry{ID: "old", SourceType: "user_explicit", State: "active", Scope: MemoryGlobal, Version: 3, SourceQuote: "a stored unsupported quote"}
	saved := EntryProvenance(entry, ProvenanceFacts{QuoteStored: true, RuntimeEnabled: &enabled})
	if saved.Content != "user_saved" || saved.Evidence.UserQuote != "stored_unverified" || saved.Recall.Eligibility != "conditional" || saved.Version != 3 || saved.RecordKind == archive.RecordKind {
		t.Fatal("saved Entry was marked as true or collided with source identity", saved)
	}
	entry.State, entry.SourceType = "candidate", "agent_candidate"
	candidate := EntryProvenance(entry, ProvenanceFacts{UserQuoteVerified: true})
	if candidate.Content != "candidate" || candidate.Recall.Eligibility != "candidate_excluded" || candidate.Evidence.UserQuote != "verified" {
		t.Fatal("having a genuine quote activated a candidate", candidate)
	}
	entry.State, entry.ConfirmedRef = "active", "local-memory-manager"
	confirmed := EntryProvenance(entry, ProvenanceFacts{})
	if confirmed.Content != "user_confirmed" || confirmed.Evidence.UserQuote != "missing" {
		t.Fatal("manual confirmation fabricated independent quote evidence", confirmed)
	}
	data, _ := json.Marshal([]RecordProvenance{p, saved, archive})
	if strings.Contains(string(data), `"truth"`) || strings.Contains(string(data), `"automatically_used"`) {
		t.Fatal("provenance claims unmeasured factual correctness or actual usage")
	}
}

func TestMemoryProvenanceDescribesDistinctRuntimePaths(t *testing.T) {
	verified := true
	for _, test := range []struct{ source, path, eligibility string }{
		{"summary", "summary", "conditional"}, {"session_context", "session_context", "conditional"},
		{"conversation", "conversation_history", "not_memory_source"}, {"workspace_instructions", "workspace_instructions", "conditional"},
		{"rag_knowledge", "on_demand", "tool_dependent"}, {"user_profile_constraint", "on_demand", "manual_review"},
		{"agent_memory", "entry", "agent_only"}, {"knowledge_fact", "knowledge_graph", "evidence_unverified"},
		{"knowledge_graph_legacy", "archive_only", "archive_only"}, {"knowledge_graph_fact", "archive_only", "archive_only"},
		{"knowledge_graph_verbatim_legacy", "archive_only", "archive_only"}, {"new_unknown_source", "unknown", "unknown"},
	} {
		t.Run(test.source, func(t *testing.T) {
			p := SourceProvenance(WorkingReference{ID: "same-id", SourceType: test.source, OwnershipVerified: &verified}, ProvenanceFacts{})
			if p.Recall.Path != test.path || p.Recall.Eligibility != test.eligibility || p.Recall.Enabled != nil || !p.ReadOnly {
				t.Fatalf("path mislabeled: %#v", p)
			}
			if test.source == "session_context" && (containsProvenanceCode(p.Recall.Conditions, "relevance") || !containsProvenanceCode(p.Recall.Conditions, "non_durable")) {
				t.Fatal("working state was given a nonexistent keyword requirement", p)
			}
			if test.source == "summary" && (!containsProvenanceCode(p.Recall.Conditions, "relevance") || containsProvenanceCode(p.Recall.Conditions, "selected_or_relevant_session")) {
				t.Fatal("current session incorrectly bypasses summary relevance", p)
			}
		})
	}
	graph := SourceProvenance(WorkingReference{ID: "fact:1", SourceType: "knowledge_fact", OwnershipVerified: &verified}, ProvenanceFacts{UserQuoteVerified: true})
	if graph.Recall.Eligibility != "conditional" || !containsProvenanceCode(graph.Recall.Conditions, "current_session") || !containsProvenanceCode(graph.Recall.Conditions, "user_quote_verified") {
		t.Fatal("graph evidence was not required for current-session recall", graph)
	}
}

func containsProvenanceCode(codes []string, expected string) bool {
	for _, code := range codes {
		if code == expected {
			return true
		}
	}
	return false
}
