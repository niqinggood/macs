package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mac/internal/textutil"
)

const ReworkControlPrefix = "OPENKITTY_REWORK_V1 "

// StepRework configures a bounded revision of an earlier stage.
type StepRework struct {
	Target           string `json:"target" yaml:"target"`
	MaxReworks       int    `json:"max_reworks,omitempty" yaml:"max_reworks,omitempty"`
	OnExhausted      string `json:"on_exhausted,omitempty" yaml:"on_exhausted,omitempty"`
	AllowSideEffects bool   `json:"allow_side_effects,omitempty" yaml:"allow_side_effects,omitempty"`
}

type PipelineReworkRequest struct {
	FromStepIndex    int       `json:"from_step_index"`
	FromStepName     string    `json:"from_step_name"`
	TargetIndex      int       `json:"target_step_index"`
	TargetName       string    `json:"target_step_name"`
	Attempt          int       `json:"attempt"`
	Max              int       `json:"max"`
	Reason           string    `json:"reason"`
	PreviousOutput   string    `json:"previous_output,omitempty"`
	AllowSideEffects bool      `json:"allow_side_effects,omitempty"`
	RequestedAt      time.Time `json:"requested_at"`
}

type PipelineReworkRecord struct {
	FromStepIndex        int        `json:"from_step_index"`
	FromStepName         string     `json:"from_step_name"`
	TargetIndex          int        `json:"target_step_index"`
	TargetName           string     `json:"target_step_name"`
	Attempt              int        `json:"attempt"`
	Max                  int        `json:"max"`
	Reason               string     `json:"reason"`
	PreviousOutputSHA256 string     `json:"previous_output_sha256,omitempty"`
	Outcome              string     `json:"outcome"`
	RequestedAt          time.Time  `json:"requested_at"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
}

type ReworkControl struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type ReworkRouteSpec struct {
	Goto string
}

// ReworkStepSpec is the stable graph-validation view supplied by the host.
type ReworkStepSpec struct {
	Name              string
	Rework            *StepRework
	HasParallel       bool
	RequiresApproval  bool
	ProducesArtifacts bool
	Routes            []ReworkRouteSpec
}

// ValidateReworkConfig enforces an acyclic, bounded reviewer graph.
func ValidateReworkConfig(steps []ReworkStepSpec) error {
	for reviewerIndex := range steps {
		step := steps[reviewerIndex]
		if step.Rework == nil {
			continue
		}
		targetIndex := findReworkStepIndex(steps, strings.TrimSpace(step.Rework.Target))
		if targetIndex < 0 {
			return fmt.Errorf("step %q rework target %q does not exist", step.Name, step.Rework.Target)
		}
		if targetIndex >= reviewerIndex {
			return fmt.Errorf("step %q rework target must be an earlier step", step.Name)
		}
		if step.Rework.MaxReworks < 0 || step.Rework.MaxReworks > 2 {
			return fmt.Errorf("step %q max_reworks must be between 0 and 2", step.Name)
		}
		switch strings.ToLower(strings.TrimSpace(step.Rework.OnExhausted)) {
		case "", "fail", "degraded":
		default:
			return fmt.Errorf("step %q on_exhausted must be fail or degraded", step.Name)
		}
		if step.HasParallel || step.RequiresApproval || step.ProducesArtifacts {
			return fmt.Errorf("step %q rework reviewer cannot be parallel, approval-gated, or artifact-producing", step.Name)
		}
		for _, route := range step.Routes {
			if target := findReworkStepIndex(steps, route.Goto); target >= 0 && target < reviewerIndex {
				return fmt.Errorf("step %q cannot combine rework with backward routing", step.Name)
			}
		}
	}
	return nil
}

func findReworkStepIndex(steps []ReworkStepSpec, name string) int {
	for index, step := range steps {
		if step.Name == name {
			return index
		}
	}
	return -1
}

func ReworkMax(config *StepRework) int {
	if config == nil || config.MaxReworks <= 0 {
		return 1
	}
	if config.MaxReworks > 2 {
		return 2
	}
	return config.MaxReworks
}

func ReworkEdgeKey(from, target int) string {
	return fmt.Sprintf("%d->%d", from, target)
}

// AppendReworkReviewInstruction declares the machine-readable final-line
// acceptance protocol without owning any application execution state.
func AppendReworkReviewInstruction(prompt string, config *StepRework) string {
	if config == nil {
		return prompt
	}
	limitText := "at most once"
	if maxReworks := ReworkMax(config); maxReworks > 1 {
		limitText = fmt.Sprintf("at most %d times", maxReworks)
	}
	return prompt + fmt.Sprintf(`

---

## Structured upstream acceptance gate
- Review whether the upstream result is usable for this step. A partial result with explicit unknowns is normally usable; do not request rework merely to seek more breadth.
- Request rework only when the upstream result is structurally unusable or misses a concrete required field and a bounded rerun can plausibly repair it.
- Your last non-empty line must be exactly one JSON record with this prefix:
  %s{"status":"accepted","reason":""}
  or %s{"status":"retry_required","reason":"specific repair instructions"}
- The host may rerun %q %s. Never place this control record anywhere except the final line.`, ReworkControlPrefix, ReworkControlPrefix, config.Target, limitText)
}

// ParseReworkControl recognizes a control record only on the last non-empty
// line, preventing ordinary reviewer prose from changing execution flow.
func ParseReworkControl(output string) (ReworkControl, string, bool, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ReworkControl{}, "", false, nil
	}
	lines := strings.Split(trimmed, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, ReworkControlPrefix) {
		return ReworkControl{Status: "accepted"}, trimmed, false, nil
	}
	var control ReworkControl
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(last, ReworkControlPrefix))), &control); err != nil {
		return ReworkControl{}, "", true, fmt.Errorf("invalid pipeline rework control record: %w", err)
	}
	control.Status = strings.ToLower(strings.TrimSpace(control.Status))
	control.Reason = strings.TrimSpace(control.Reason)
	if control.Status != "accepted" && control.Status != "retry_required" {
		return ReworkControl{}, "", true, fmt.Errorf("invalid pipeline rework status %q", control.Status)
	}
	cleaned := strings.TrimSpace(strings.Join(lines[:len(lines)-1], "\n"))
	if control.Status == "accepted" && cleaned == "" {
		return ReworkControl{}, "", true, fmt.Errorf("pipeline reviewer accepted upstream output without producing its own result")
	}
	return control, cleaned, true, nil
}

// PendingReworkContext formats a stored request for the target step. The host
// may supply a domain-specific revision policy such as the PPT audit rules.
func PendingReworkContext(request *PipelineReworkRequest, revisionPolicy string) string {
	if request == nil {
		return ""
	}
	return fmt.Sprintf(`## Downstream rework request (attempt %d/%d)
Reviewer: %s
Repair reason: %s

Revise the previous result below. Preserve verified facts and successful read-only evidence; address only the stated gap, and do not broaden the task or repeat equivalent successful calls.%s

### Previous result
%s`, request.Attempt, request.Max, request.FromStepName, request.Reason, revisionPolicy, textutil.Truncate(request.PreviousOutput, 8000))
}

// PresentationReworkMode classifies a PPT review request without depending on
// the presentation runtime.
func PresentationReworkMode(reason string) string {
	lowerReason := strings.ToLower(reason)
	switch {
	case strings.Contains(lowerReason, "stale"), strings.Contains(lowerReason, "predate"), strings.Contains(lowerReason, "rerender"):
		return "freshness"
	case strings.Contains(lowerReason, "audience grounding"), strings.Contains(lowerReason, "content grounding"), strings.Contains(lowerReason, "content fidelity"), strings.Contains(lowerReason, "numeric provenance"), strings.Contains(lowerReason, "unsupported numeric"):
		return "content"
	default:
		return "audit"
	}
}

func PresentationReworkPolicy(request *PipelineReworkRequest) string {
	if request == nil || strings.TrimSpace(request.TargetName) != "视觉润色" || strings.TrimSpace(request.FromStepName) != "交付自查" {
		return ""
	}
	switch PresentationReworkMode(request.Reason) {
	case "freshness":
		return `

### PPT freshness-only revision rules
The task-local deck_spec.json, build_deck.py, final PPTX, and run-scoped preview already exist. Do not call use_skill or scaffold_task_pptx.py again. Freeze the existing deck_spec.json, builder, and final PPTX bytes: do not edit or rebuild any of them. Run the bundled audit on that exact PPTX, render only with the bundled render_pptx script into the exact contracted run-scoped preview, then call vision on contact_sheet.jpg and every slide_NN.png image. Never call LibreOffice, pdftoppm, PIL, or a custom render command directly. Stop after reporting the exact unchanged PPTX path and complete fresh evidence.`
	case "content":
		return `

### PPT content-only revision rules
The task-local deck_spec.json, build_deck.py, final PPTX, and run-scoped preview already exist. Do not call use_skill or scaffold_task_pptx.py again. Change only the listed cells or visible text in deck_spec.json with edit_file or write_file; shell and inline Python are not editing tools. Keep the selected route, layout system, and all verified facts. Run the task builder as one direct command that preserves its real exit status; a failed interpreter or builder does not consume the one successful-build budget. Then run the bundled audit exactly once, render only with the bundled render_pptx script into the exact contracted run-scoped preview, and call vision on contact_sheet.jpg plus every slide_NN.png. Never call LibreOffice, pdftoppm, PIL, or a custom render command directly. Do not make another edit after that render.`
	default:
		return `

### PPT audit revision rules
The task-local deck_spec.json, per-slide build_deck.py, final PPTX, and run-scoped preview already exist. Do not call use_skill or scaffold_task_pptx.py again. Do not inspect pptx_design.py, read the binary PPTX with read_file, probe unrelated paths, or rebuild the deck from scratch. Correct only the concrete audit defect with edit_file/write_file; preserve the completed TASK_PAGE_RENDERERS contract and touch only the affected page function when builder geometry must change. Run the existing builder as a direct real-exit command, rerun the bundled audit_pptx.py with the user's font floors, render only with the bundled render_pptx script to the same run-scoped preview, then call vision on contact_sheet.jpg and every slide_NN.png again. Never call LibreOffice, pdftoppm, PIL, or a custom render command directly. The revised bytes cannot inherit the prior audit or visual pass.`
	}
}

func IsKnownReworkReadOnlyTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "stock_facts", "web_fetch", "web_search", "platform_search", "rag_search", "source_acquire", "read_file", "extract", "list_dir", "grep", "glob":
		return true
	default:
		return false
	}
}
