package memory

// GraphSource keeps a graph assertion separate from its evidence. A confidence
// score or a successfully resolved source is never a claim of factual truth.
type GraphSource struct {
	Subject              string   `json:"subject,omitempty"`
	Predicate            string   `json:"predicate,omitempty"`
	Object               string   `json:"object,omitempty"`
	Origin               string   `json:"origin,omitempty"`
	ValidFrom            string   `json:"valid_from,omitempty"`
	ValidTo              string   `json:"valid_to,omitempty"`
	Confidence           *float64 `json:"confidence,omitempty"`
	EvidenceAvailability string   `json:"evidence_availability"`
	QuotedEvidence       string   `json:"quoted_evidence,omitempty"`
}

// RecordProvenance describes what was checked, not whether the stored text is
// true. Entry state and source ownership must not be presented as truth flags.
type RecordProvenance struct {
	ID         string             `json:"id"`
	RecordKind string             `json:"record_kind"`
	Origin     ProvenanceOrigin   `json:"origin"`
	Identity   string             `json:"identity"`
	Content    string             `json:"content"`
	Evidence   ProvenanceEvidence `json:"evidence"`
	Missing    []string           `json:"missing"`
	Recall     ProvenanceRecall   `json:"recall"`
	ReadOnly   bool               `json:"read_only"`
	State      string             `json:"state,omitempty"`
	Version    int                `json:"version,omitempty"`
}

type ProvenanceOrigin struct {
	Location  string `json:"location,omitempty"`
	RecordID  string `json:"record_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

type ProvenanceEvidence struct {
	UserQuote     string `json:"user_quote"` // verified, stored_unverified, missing
	TaskLink      string `json:"task_link"`  // verified, missing
	EventLink     string `json:"event_link"` // verified, missing
	VerifiedQuote string `json:"verified_quote,omitempty"`
}

type ProvenanceRecall struct {
	Path        string   `json:"path"`
	Eligibility string   `json:"eligibility"`
	Enabled     *bool    `json:"enabled"`
	Conditions  []string `json:"conditions"`
}

// ProvenanceFacts comes from an already authorized host read. It cannot grant
// access to a record, activate an archive, or prove a claim merely by quoting it.
type ProvenanceFacts struct {
	Origin            ProvenanceOrigin
	UserQuoteVerified bool
	VerifiedQuote     string
	QuoteStored       bool
	TaskLinked        bool
	EventLinked       bool
	RuntimeEnabled    *bool
}

func baseProvenance(id, kind string, facts ProvenanceFacts) RecordProvenance {
	p := RecordProvenance{ID: id, RecordKind: kind, Origin: facts.Origin, Identity: "unknown", Content: "unknown",
		Evidence: ProvenanceEvidence{UserQuote: "missing", TaskLink: "missing", EventLink: "missing"}, Missing: []string{},
		Recall: ProvenanceRecall{Path: "unknown", Eligibility: "unknown", Enabled: facts.RuntimeEnabled, Conditions: []string{}}}
	if facts.UserQuoteVerified {
		p.Evidence.UserQuote = "verified"
		p.Evidence.VerifiedQuote = facts.VerifiedQuote
	} else if facts.QuoteStored {
		p.Evidence.UserQuote = "stored_unverified"
	}
	if facts.TaskLinked {
		p.Evidence.TaskLink = "verified"
	}
	if facts.EventLinked {
		p.Evidence.EventLink = "verified"
	}
	if p.Origin.Location == "" {
		p.Missing = append(p.Missing, "source_location_missing")
	}
	if p.Origin.RecordID == "" {
		p.Missing = append(p.Missing, "source_record_id_missing")
	}
	return p
}

func entryRecall(scope MemoryScope, state string, enabled *bool) ProvenanceRecall {
	r := ProvenanceRecall{Path: "entry", Eligibility: "conditional", Enabled: enabled,
		Conditions: []string{"memory_enabled", "same_owner_workspace_agent", "active_version", "relevance_or_pinned_preference", "scope_precedence", "token_budget", "not_retracted", "security_scan", "actual_selection_requires_trace", "model_use_not_measured", "agent_mode"}}
	if scope == MemoryGlobal {
		r.Conditions[1] = "same_owner_workspace_shared"
	}
	if scope == MemorySession {
		r.Conditions[3] = "selected_session"
	}
	if state == "candidate" {
		r.Eligibility = "candidate_excluded"
	} else if state != "active" {
		r.Eligibility = "inactive_excluded"
	}
	return r
}

// EntryProvenance records saving/confirmation as a user action, without treating
// either action as independent corroboration of the value or its origin quote.
func EntryProvenance(entry Entry, facts ProvenanceFacts) RecordProvenance {
	p := baseProvenance(entry.ID, "entry", facts)
	p.Identity, p.State, p.Version = "verified", entry.State, entry.Version
	p.Recall = entryRecall(entry.Scope, entry.State, facts.RuntimeEnabled)
	switch {
	case entry.ConfirmedRef != "" || entry.ConfirmedQuote != "":
		p.Content = "user_confirmed"
	case entry.SourceType == "user_explicit" || entry.SourceRef == "local-memory-manager":
		p.Content = "user_saved"
	case entry.State == "candidate":
		p.Content = "candidate"
	case entry.SourceType == "legacy" || entry.SourceType == "":
		p.Content = "historical_unverified"
	default:
		p.Content = "model_generated"
	}
	if p.Evidence.UserQuote != "verified" {
		p.Missing = append(p.Missing, "original_user_quote_unverified")
	}
	return p
}

func SourceProvenance(ref WorkingReference, facts ProvenanceFacts) RecordProvenance {
	p := baseProvenance(ref.ID, "source", facts)
	p.ReadOnly = true
	if ref.OwnershipVerified != nil && *ref.OwnershipVerified {
		p.Identity = "verified"
	} else if ref.OwnershipVerified != nil {
		p.Identity = "workspace_archive"
	}
	p.Content = "model_generated"
	p.Recall.Conditions = []string{"memory_enabled", "same_owner_workspace_agent", "relevance", "token_budget", "not_retracted", "security_scan", "actual_selection_requires_trace", "model_use_not_measured", "agent_mode"}
	p.Recall.Eligibility = "conditional"
	switch ref.SourceType {
	case "session_context":
		p.Recall.Path = "session_context"
		conditions := p.Recall.Conditions[:0]
		for _, code := range p.Recall.Conditions {
			if code != "relevance" {
				conditions = append(conditions, code)
			}
		}
		p.Recall.Conditions = append(conditions, "selected_session", "context_limit", "non_durable")
	case "summary":
		p.Recall.Path = "summary"
		p.Recall.Conditions = append(p.Recall.Conditions, "same_owner_namespace")
	case "task_execution", "task_insight", "learning_knowledge", "user_statement", "user_feedback":
		p.Recall.Path = "scoped_source_adapter"
		p.Recall.Conditions = append(p.Recall.Conditions, "verified_web_session", "web_owner_bound", "supported_adapter_source", "source_limit")
		if ref.SourceType == "user_statement" || ref.SourceType == "user_feedback" {
			p.Content = "user_statement"
		}
	case "conversation":
		p.Recall = ProvenanceRecall{Path: "conversation_history", Eligibility: "not_memory_source", Conditions: []string{"current_chat_context_separate", "view_does_not_activate"}}
	case "user_profile_constraint":
		p.Content = "user_statement"
		p.Recall = ProvenanceRecall{Path: "on_demand", Eligibility: "manual_review", Conditions: []string{"original_user_statement_adapter", "full_profile_not_recalled", "view_does_not_activate"}}
	case "knowledge_fact":
		p.Recall.Path = "knowledge_graph"
		p.Recall.Conditions = append(p.Recall.Conditions, "verified_web_session", "web_owner_bound", "supported_adapter_source", "source_limit", "current_session", "user_quote_verified")
		if !facts.UserQuoteVerified {
			p.Recall.Eligibility = "evidence_unverified"
		}
	case "workspace_instructions":
		p.Content = "workspace_instruction"
		p.Recall = ProvenanceRecall{Path: "workspace_instructions", Eligibility: "conditional", Conditions: []string{"project_instruction_loader", "resolved_workspace_path", "instruction_limits", "model_use_not_measured"}}
		// A whole-file instruction has no row ID; its resolved relative file
		// is the original record locator, not a missing database identifier.
		missing := p.Missing[:0]
		for _, code := range p.Missing {
			if code != "source_record_id_missing" {
				missing = append(missing, code)
			}
		}
		p.Missing = missing
	case "agent_memory":
		p.Content = "stored_memory"
		p.Recall = entryRecall(ref.Scope, "active", facts.RuntimeEnabled)
		p.Recall.Eligibility = "agent_only"
		p.Recall.Conditions = append(p.Recall.Conditions, "source_agent_only_not_shared_by_view")
	case "rag_knowledge":
		p.Content = "document_metadata"
		p.Recall = ProvenanceRecall{Path: "on_demand", Eligibility: "tool_dependent", Conditions: []string{"rag_tool_available", "knowledge_base_selected", "indexed_document_match", "metadata_is_not_document_evidence"}}
	case "legacy_preference", "knowledge_graph_legacy", "knowledge_graph_verbatim_legacy", "knowledge_graph_fact":
		p.Content = "historical_unverified"
		p.Recall = ProvenanceRecall{Path: "archive_only", Eligibility: "archive_only", Conditions: []string{"personal_web_recall_only", "review_and_save_required", "view_does_not_activate", "non_web_compatibility_separate"}}
	default:
		p.Content = "unknown"
		p.Recall = ProvenanceRecall{Path: "unknown", Eligibility: "unknown", Conditions: []string{"runtime_path_unverified"}}
	}
	if p.Identity != "verified" {
		p.Missing = append(p.Missing, "personal_ownership_unverified")
		switch p.Recall.Path {
		case "entry", "session_context", "summary", "scoped_source_adapter", "knowledge_graph":
			p.Recall.Eligibility = "ownership_unverified"
		}
	}
	if p.Evidence.UserQuote != "verified" && p.Content != "workspace_instruction" && p.Content != "document_metadata" {
		p.Missing = append(p.Missing, "original_user_quote_unverified")
	}
	if (ref.SourceType == "task_execution" || ref.SourceType == "task_insight" || ref.SourceType == "learning_knowledge") && !facts.TaskLinked {
		p.Missing = append(p.Missing, "original_task_id_missing")
	}
	return p
}
