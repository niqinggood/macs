package aiemployee

import (
	"fmt"
	"strings"
	"time"

	"mac/internal/textutil"
)

const (
	FinalRecoveryOutputTokens  = 5200
	FinalRecoveryTimeout       = 2 * time.Minute
	finalRecoveryEvidenceLimit = 18000
	finalRecoveryPacketLimit   = 2800
)

var finalRecoveryExcludedTools = map[string]bool{
	"use_skill":        true,
	"list_skills":      true,
	"persistent_shell": true,
	"cli":              true,
	"write_file":       true,
	"edit_file":        true,
	"json_patch":       true,
	"multiedit":        true,
	"plan_task":        true,
	"update_plan_step": true,
}

// ToolRecord is the minimal observation metadata needed for final recovery.
type ToolRecord struct {
	Tool      string
	Succeeded bool
}

type ToolRecordParser func(string) (ToolRecord, bool)

// BuildFinalRecoveryMessages constructs a closed-evidence synthesis context.
func BuildFinalRecoveryMessages(originalRequest string, results []string, parse ToolRecordParser) []map[string]interface{} {
	var evidence strings.Builder
	kept := 0
	start := max(0, len(results)-12)
	for _, result := range results[start:] {
		if parse == nil {
			break
		}
		record, ok := parse(result)
		if !ok || !record.Succeeded || finalRecoveryExcludedTools[strings.ToLower(strings.TrimSpace(record.Tool))] {
			continue
		}
		content := strings.TrimSpace(result)
		if newline := strings.Index(content, "\n"); newline >= 0 {
			content = strings.TrimSpace(content[newline+1:])
		}
		if content == "" {
			continue
		}
		packet := fmt.Sprintf("[%s]\n%s\n\n", record.Tool, textutil.TruncateUTF8Bytes(content, finalRecoveryPacketLimit))
		if evidence.Len()+len(packet) > finalRecoveryEvidenceLimit {
			break
		}
		evidence.WriteString(packet)
		kept++
	}

	evidenceText := strings.TrimSpace(evidence.String())
	if kept == 0 {
		evidenceText = "(No reusable evidence packet was retained. Use only the user's supplied facts and mark every other field unknown or as an explicit assumption.)"
	}
	return []map[string]interface{}{
		{
			"role": "system",
			"content": `[AI EMPLOYEE FINAL DELIVERY RECOVERY]
The prior model/tool loop did not finish its final synthesis. Produce the complete user-facing answer now; do not mention the recovery, the failed attempt, model behavior, internal stages, prompts, logs, runtime paths, or tools.

The original user request below is the authoritative contract. Cover every explicitly requested section in a compact but useful v0. Do not call tools, create or claim a file, announce future work, or ask a questionnaire before delivering. Use only user-supplied facts and the retained evidence packets. Distinguish facts, transparent inferences, assumptions, and unknowns. A missing field stays unknown; never invent a benchmark, market norm, institutional behavior, probability, threshold, customer, revenue, team credential, or validation result. If evidence is sparse or conflicting, still deliver the portions that can be written safely and put at most three decision-changing questions at the end.`,
		},
		{"role": "user", "content": strings.TrimSpace(originalRequest)},
		{"role": "system", "content": "Retained current-turn evidence packets (closed set):\n\n" + evidenceText},
	}
}
