package toolcontract

import (
	"encoding/json"
	"strings"
)

const RecordPrefix = "OPENKITTY_TOOL_RECORD="

// Record is the durable host receipt prepended to finalized tool output.
type Record struct {
	Tool                      string   `json:"tool"`
	Succeeded                 bool     `json:"succeeded"`
	ArtifactPath              string   `json:"artifact_path,omitempty"`
	ActionIntent              string   `json:"action_intent,omitempty"`
	SearchMode                string   `json:"search_mode,omitempty"`
	EvidenceHints             []string `json:"evidence_hints,omitempty"`
	SourceURL                 string   `json:"source_url,omitempty"`
	EvidenceDateKeys          []string `json:"evidence_date_keys,omitempty"`
	EvidenceDailyDateKeys     []string `json:"evidence_daily_date_keys,omitempty"`
	VerificationFamily        string   `json:"verification_family,omitempty"`
	VerificationRoot          string   `json:"verification_root,omitempty"`
	VerificationTarget        string   `json:"verification_target,omitempty"`
	VerificationMasked        bool     `json:"verification_masked,omitempty"`
	VerificationEmpty         bool     `json:"verification_empty,omitempty"`
	VerificationBaseline      bool     `json:"verification_baseline,omitempty"`
	VerificationBaselineOnly  bool     `json:"verification_baseline_only,omitempty"`
	VerificationFailureIDs    []string `json:"verification_failure_ids,omitempty"`
	VerificationNewFailureIDs []string `json:"verification_new_failure_ids,omitempty"`
}

func ParseRecord(text string) (Record, bool) {
	for _, line := range strings.Split(text, "\n") {
		index := strings.Index(line, RecordPrefix)
		if index < 0 {
			continue
		}
		raw := strings.TrimSpace(line[index+len(RecordPrefix):])
		var record Record
		if err := json.Unmarshal([]byte(raw), &record); err == nil && strings.TrimSpace(record.Tool) != "" {
			return record, true
		}
	}
	return Record{}, false
}
