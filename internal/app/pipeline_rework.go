package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	pipelinecore "mac/internal/pipeline"
	"mac/internal/toolregistry"
)

const pipelineReworkControlPrefix = pipelinecore.ReworkControlPrefix

type StepRework = pipelinecore.StepRework
type PipelineReworkRequest = pipelinecore.PipelineReworkRequest
type PipelineReworkRecord = pipelinecore.PipelineReworkRecord
type pipelineReworkControl = pipelinecore.ReworkControl

type PipelineStepRevision struct {
	Revision       int                     `json:"revision"`
	Status         string                  `json:"status"`
	Output         string                  `json:"output,omitempty"`
	Error          string                  `json:"error,omitempty"`
	DurationMs     int64                   `json:"duration_ms,omitempty"`
	ToolCalls      []ToolCallRecord        `json:"tool_calls,omitempty"`
	SourceSnapshot *PipelineSourceSnapshot `json:"source_snapshot,omitempty"`
	ArchivedAt     time.Time               `json:"archived_at"`
}

type pipelineReworkOutcome struct {
	Output         string
	Requested      bool
	TargetIndex    int
	Attempt        int
	Max            int
	Reason         string
	Degraded       bool
	DegradedReason string
}

func validatePipelineReworkConfig(pipeline Pipeline) error {
	steps := make([]pipelinecore.ReworkStepSpec, len(pipeline.Steps))
	for index, step := range pipeline.Steps {
		steps[index] = pipelinecore.ReworkStepSpec{
			Name:              step.Name,
			Rework:            step.Rework,
			HasParallel:       len(step.Parallel) > 0,
			RequiresApproval:  step.RequireApproval,
			ProducesArtifacts: step.ArtifactContract != nil,
			Routes:            make([]pipelinecore.ReworkRouteSpec, 0, len(step.Routing)),
		}
		for _, route := range step.Routing {
			steps[index].Routes = append(steps[index].Routes, pipelinecore.ReworkRouteSpec{Goto: route.Goto})
		}
	}
	return pipelinecore.ValidateReworkConfig(steps)
}

func pipelineReworkMax(config *StepRework) int {
	return pipelinecore.ReworkMax(config)
}

func pipelineReworkEdgeKey(from, target int) string {
	return pipelinecore.ReworkEdgeKey(from, target)
}

func appendPipelineReworkReviewInstruction(prompt string, step *PipelineStep) string {
	if step == nil || step.Rework == nil {
		return prompt
	}
	return pipelinecore.AppendReworkReviewInstruction(prompt, step.Rework)
}

func parsePipelineReworkControl(output string) (pipelineReworkControl, string, bool, error) {
	return pipelinecore.ParseReworkControl(output)
}

func pendingPipelineReworkContext(exec *PipelineExecution, stepIndex int) string {
	if exec == nil {
		return ""
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	request := exec.PendingRework
	if request == nil || request.TargetIndex != stepIndex {
		return ""
	}
	return pipelinecore.PendingReworkContext(request, pipelinecore.PresentationReworkPolicy(request))
}

func (e *PipelineExecutor) processPipelineReworkDecision(exec *PipelineExecution, steps []PipelineStep, reviewerIndex int, output string) (pipelineReworkOutcome, error) {
	outcome := pipelineReworkOutcome{Output: output, TargetIndex: -1}
	if exec == nil || reviewerIndex < 0 || reviewerIndex >= len(steps) || steps[reviewerIndex].Rework == nil {
		return outcome, nil
	}
	config := steps[reviewerIndex].Rework
	targetIndex := findStepIndex(steps, config.Target)
	if targetIndex < 0 || targetIndex >= reviewerIndex {
		return outcome, fmt.Errorf("invalid rework target %q for step %q", config.Target, steps[reviewerIndex].Name)
	}
	control, cleaned, _, err := parsePipelineReworkControl(output)
	if err != nil {
		return outcome, err
	}
	outcome.Output = cleaned
	if control.Status != "retry_required" {
		exec.mu.Lock()
		if exec.PendingRework != nil && exec.PendingRework.FromStepIndex == reviewerIndex {
			resolvePipelineReworkLocked(exec, "accepted")
			exec.PendingRework = nil
		}
		exec.mu.Unlock()
		return outcome, nil
	}

	reason := control.Reason
	if reason == "" {
		reason = "reviewer marked the upstream result unusable without a specific reason"
	}
	maxReworks := pipelineReworkMax(config)
	edgeKey := pipelineReworkEdgeKey(reviewerIndex, targetIndex)

	exec.mu.Lock()
	if exec.ReworkCounts == nil {
		exec.ReworkCounts = make(map[string]int)
	}
	used := exec.ReworkCounts[edgeKey]
	if used >= maxReworks {
		resolvePipelineReworkLocked(exec, "exhausted")
		exec.PendingRework = nil
		exec.mu.Unlock()
		if strings.EqualFold(strings.TrimSpace(config.OnExhausted), "degraded") {
			outcome.Output = fmt.Sprintf("## Upstream rework exhausted\n\nReviewer: %s\n\nRemaining gap: %s\n\nThe pipeline continued with this limitation explicitly marked; downstream stages must not promote the missing material to verified fact.", steps[reviewerIndex].Name, reason)
			outcome.Degraded = true
			outcome.DegradedReason = "upstream_rework_exhausted: " + truncate(reason, 300)
			outcome.Reason = reason
			outcome.Attempt = used
			outcome.Max = maxReworks
			return outcome, nil
		}
		return outcome, fmt.Errorf("upstream rework exhausted after %d attempt: %s", maxReworks, reason)
	}
	exec.mu.Unlock()

	if !config.AllowSideEffects && !e.pipelineReworkRangeIsReadOnly(steps, targetIndex, reviewerIndex) {
		return outcome, fmt.Errorf("rework target %q includes tools with side effects; set allow_side_effects only with an idempotent design", steps[targetIndex].Name)
	}

	exec.mu.Lock()
	previousOutput := exec.Steps[targetIndex].Output
	attempt := exec.ReworkCounts[edgeKey] + 1
	exec.ReworkCounts[edgeKey] = attempt
	requestedAt := time.Now()
	request := &PipelineReworkRequest{
		FromStepIndex: reviewerIndex, FromStepName: steps[reviewerIndex].Name,
		TargetIndex: targetIndex, TargetName: steps[targetIndex].Name,
		Attempt: attempt, Max: maxReworks, Reason: reason,
		PreviousOutput: truncate(previousOutput, 12000), AllowSideEffects: config.AllowSideEffects, RequestedAt: requestedAt,
	}
	archiveAndResetPipelineReworkRangeLocked(exec, targetIndex, reviewerIndex, cleaned, reason)
	exec.PendingRework = request
	exec.CurrentStep = targetIndex
	hash := sha256.Sum256([]byte(previousOutput))
	exec.ReworkHistory = append(exec.ReworkHistory, PipelineReworkRecord{
		FromStepIndex: reviewerIndex, FromStepName: steps[reviewerIndex].Name,
		TargetIndex: targetIndex, TargetName: steps[targetIndex].Name,
		Attempt: attempt, Max: maxReworks, Reason: reason,
		PreviousOutputSHA256: hex.EncodeToString(hash[:]), Outcome: "requested", RequestedAt: requestedAt,
	})
	exec.mu.Unlock()

	e.removePipelineReworkArtifacts(exec, steps, targetIndex, reviewerIndex)
	exec.resetPresentationProductionEvidenceForStep(targetIndex, reason)
	outcome.Requested = true
	outcome.TargetIndex = targetIndex
	outcome.Attempt = attempt
	outcome.Max = maxReworks
	outcome.Reason = reason
	return outcome, nil
}

func resolvePipelineReworkLocked(exec *PipelineExecution, outcome string) {
	now := time.Now()
	for i := len(exec.ReworkHistory) - 1; i >= 0; i-- {
		if exec.ReworkHistory[i].Outcome == "requested" {
			exec.ReworkHistory[i].Outcome = outcome
			exec.ReworkHistory[i].ResolvedAt = &now
			return
		}
	}
}

func archiveAndResetPipelineReworkRangeLocked(exec *PipelineExecution, targetIndex, reviewerIndex int, reviewerOutput, reason string) {
	for index := targetIndex; index <= reviewerIndex && index < len(exec.Steps); index++ {
		current := exec.Steps[index]
		if current.Revision <= 0 {
			current.Revision = 1
		}
		if index == reviewerIndex {
			current.Output = reviewerOutput
			current.Status = "rework_requested"
			current.Error = reason
		}
		var snapshot *PipelineSourceSnapshot
		if current.SourceSnapshot != nil {
			copy := *current.SourceSnapshot
			snapshot = &copy
		}
		exec.Steps[index].Revisions = append(exec.Steps[index].Revisions, PipelineStepRevision{
			Revision: current.Revision, Status: current.Status, Output: current.Output, Error: current.Error,
			DurationMs: current.DurationMs, ToolCalls: append([]ToolCallRecord(nil), current.ToolCalls...),
			SourceSnapshot: snapshot, ArchivedAt: time.Now(),
		})
		exec.Steps[index].Output = ""
		exec.Steps[index].Status = "pending"
		exec.Steps[index].Error = ""
		exec.Steps[index].DurationMs = 0
		exec.Steps[index].ToolCalls = nil
		exec.Steps[index].SourceSnapshot = nil
		exec.Steps[index].Revision = current.Revision + 1
	}
	for auditIndex := range exec.parallelToolCalls {
		parentIndex, _, ok := decodeParallelAuditStepIndex(auditIndex)
		if ok && parentIndex >= targetIndex && parentIndex <= reviewerIndex {
			delete(exec.parallelToolCalls, auditIndex)
		}
	}
}

func (e *PipelineExecutor) pipelineReworkRangeIsReadOnly(steps []PipelineStep, targetIndex, reviewerIndex int) bool {
	var registry *toolregistry.Registry
	for index := targetIndex; index < reviewerIndex; index++ {
		step := &steps[index]
		if step.NoTools {
			continue
		}
		if registry == nil && e != nil && e.agentFactory != nil {
			registry = e.getFullToolRegistry()
		}
		tools := step.Tools
		if len(tools) == 0 || registry == nil {
			return false
		}
		for _, name := range tools {
			if !pipelineReworkReadOnlyTool(registry, name) {
				return false
			}
		}
	}
	return true
}

func pipelineReworkReadOnlyTool(registry *toolregistry.Registry, name string) bool {
	if pipelinecore.IsKnownReworkReadOnlyTool(name) {
		return true
	}
	return registry != nil && isToolReadOnly(registry, name)
}

// pipelineStepExecutionTools removes automatically expanded interactive
// browser fallbacks during a read-only rework. The original explicitly
// configured research tools remain available, but a revision cannot acquire a
// broader mutation surface than its first run.
func pipelineStepExecutionTools(exec *PipelineExecution, step *PipelineStep, stepIndex int) []string {
	if step == nil {
		return nil
	}
	if step.NoTools {
		return []string{}
	}
	if exec != nil {
		exec.mu.Lock()
		pending := exec.PendingRework
		isReadOnlyRevision := pending != nil && pending.TargetIndex == stepIndex && !pending.AllowSideEffects
		exec.mu.Unlock()
		if isReadOnlyRevision && len(step.Tools) > 0 {
			return uniqueSortedStrings(step.Tools)
		}
	}
	return stepToolAllowlist(step)
}

func (e *PipelineExecutor) removePipelineReworkArtifacts(exec *PipelineExecution, steps []PipelineStep, targetIndex, reviewerIndex int) {
	for index := targetIndex; index < reviewerIndex; index++ {
		contract := steps[index].ArtifactContract
		if contract == nil {
			continue
		}
		for _, path := range pipelineArtifactContractPaths(exec, contract) {
			fullPath, ok := e.storageArtifactPath(path)
			if !ok {
				continue
			}
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) && e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Could not remove superseded rework artifact %s: %v", fullPath, err)
			}
		}
	}
}
