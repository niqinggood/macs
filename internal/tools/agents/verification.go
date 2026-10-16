package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultVerificationTimeout = 75 * time.Second

// VerificationRunner is the narrow child-agent surface used by VerifyWorkTool.
type VerificationRunner interface {
	Execute(context.Context, map[string]interface{}) (string, error)
}

type ArtifactCheck struct {
	Label string
	Err   error
}

type ArtifactChecker func(workspace, claim string, evidence []string) []ArtifactCheck
type VerificationOutputNormalizer func(string) string

// VerifyWorkTool coordinates a bounded, read-only verification run.
type VerifyWorkTool struct {
	runner          VerificationRunner
	workspace       string
	checkArtifacts  ArtifactChecker
	normalizeOutput VerificationOutputNormalizer
	timeout         time.Duration
}

func NewVerifyWorkTool(
	runner VerificationRunner,
	workspace string,
	checkArtifacts ArtifactChecker,
	normalizeOutput VerificationOutputNormalizer,
	timeout time.Duration,
) *VerifyWorkTool {
	if timeout <= 0 {
		timeout = defaultVerificationTimeout
	}
	return &VerifyWorkTool{
		runner:          runner,
		workspace:       workspace,
		checkArtifacts:  checkArtifacts,
		normalizeOutput: normalizeOutput,
		timeout:         timeout,
	}
}

func (t *VerifyWorkTool) Name() string { return "verify_work" }

func (t *VerifyWorkTool) Description() string {
	return "Run the read-only verification agent against claimed work and acceptance criteria. Use after changes to get an independent PASS/FAIL/PARTIAL verdict."
}

func (t *VerifyWorkTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"claim": map[string]interface{}{
				"type":        "string",
				"description": "What is claimed to be complete.",
			},
			"acceptance_criteria": map[string]interface{}{
				"type":        "string",
				"description": "Concrete criteria the verification agent should check.",
			},
			"evidence": map[string]interface{}{
				"type":        "array",
				"description": "Optional files, commands, logs, or outputs the verification agent should inspect.",
				"items":       map[string]interface{}{"type": "string"},
			},
			"max_iterations": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum verification turns. Defaults to 2; raise it only when the user explicitly requests deep verification.",
				"minimum":     1,
				"maximum":     12,
			},
		},
	}
}

func (t *VerifyWorkTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runner == nil {
		return "", fmt.Errorf("verification agent is not available")
	}
	claim := strings.TrimSpace(stringParam(params, "claim", ""))
	criteria := strings.TrimSpace(stringParam(params, "acceptance_criteria", ""))
	if claim == "" && criteria == "" {
		return "", fmt.Errorf("claim or acceptance_criteria is required")
	}

	maxIterations := boundedVerificationIterations(params["max_iterations"])
	evidenceItems := StringSliceParam(params["evidence"])
	var artifactChecks []ArtifactCheck
	if t.checkArtifacts != nil {
		artifactChecks = t.checkArtifacts(t.workspace, claim, evidenceItems)
	}
	for _, check := range artifactChecks {
		if check.Err != nil {
			return fmt.Sprintf("EVIDENCE:\n- Host read-only OPC validation failed for %s: %v\n\nVERDICT: FAIL", check.Label, check.Err), nil
		}
	}

	prompt := buildVerificationPrompt(claim, criteria, evidenceItems, artifactChecks)
	verifyCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	result, err := t.runner.Execute(verifyCtx, map[string]interface{}{
		"agent_name":     fmt.Sprintf("verification_%d", time.Now().UnixNano()%100000),
		"agent_type":     "verification",
		"task":           prompt,
		"max_iterations": float64(maxIterations),
	})
	if err != nil && errors.Is(verifyCtx.Err(), context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Sprintf("Verification stopped after the %s fast-path budget. Existing evidence was preserved; deep verification was not retried.\n\nVERDICT: PARTIAL", t.timeout.Round(time.Second)), nil
	}
	if err != nil {
		return result, err
	}
	if t.normalizeOutput != nil {
		result = t.normalizeOutput(result)
	}
	return result, nil
}

func boundedVerificationIterations(value interface{}) int {
	iterations := 2
	switch raw := value.(type) {
	case float64:
		if raw > 0 {
			iterations = int(raw)
		}
	case int:
		if raw > 0 {
			iterations = raw
		}
	}
	if iterations < 1 {
		return 1
	}
	if iterations > 12 {
		return 12
	}
	return iterations
}

func buildVerificationPrompt(claim, criteria string, evidence []string, artifactChecks []ArtifactCheck) string {
	var output strings.Builder
	output.WriteString("Verify the supplied evidence first. Do not modify files or persistent state, and never repeat an external side effect.\n")
	output.WriteString("If the evidence already supports a verdict, stop immediately. Otherwise run at most one small, targeted read-only check; do not scan the whole project.\n\n")
	if claim != "" {
		output.WriteString("Claim:\n")
		output.WriteString(claim)
		output.WriteString("\n\n")
	}
	if criteria != "" {
		output.WriteString("Acceptance criteria:\n")
		output.WriteString(criteria)
		output.WriteString("\n\n")
	}
	if len(evidence) > 0 {
		output.WriteString("Evidence hints to inspect:\n")
		for _, item := range evidence {
			output.WriteString("- ")
			output.WriteString(item)
			output.WriteString("\n")
		}
		output.WriteString("\n")
	}
	if len(artifactChecks) > 0 {
		output.WriteString("Host read-only Office package checks (authoritative):\n")
		for _, check := range artifactChecks {
			output.WriteString("- PASS: ")
			output.WriteString(check.Label)
			output.WriteString(" has complete OPC relationship references and targets.\n")
		}
		output.WriteString("\n")
	}
	output.WriteString("Return an EVIDENCE: section with exact files, commands, URLs, statuses, and observed outputs. Finish with exactly one VERDICT: PASS, VERDICT: FAIL, or VERDICT: PARTIAL line. PASS without concrete evidence is invalid.")
	return output.String()
}

func StringSliceParam(value interface{}) []string {
	switch values := value.(type) {
	case []string:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok {
				if text = strings.TrimSpace(text); text != "" {
					result = append(result, text)
				}
			}
		}
		return result
	default:
		return nil
	}
}
