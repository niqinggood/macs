package pipeline

import (
	"context"
	"fmt"
	"time"

	"mac/internal/configvalue"
)

// LegacyPipelineStepCheckpoint is the original in-memory helper used by the
// pipeline_checkpoint diagnostic tool. Runtime crash recovery is implemented
// by PipelineCheckpoint in pipeline_checkpoint.go.
// Inspired by Microsoft pg_durable: checkpoint each step so crashes resume cleanly.
type LegacyPipelineStepCheckpoint struct {
	PipelineID string    `json:"pipeline_id"`
	StepIndex  int       `json:"step_index"`
	StepName   string    `json:"step_name"`
	Status     string    `json:"status"` // running, completed, failed
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Retries    int       `json:"retries"`
}

// PipelineDurable wraps pipeline execution with checkpoint-based recovery.
type PipelineDurable struct {
	checkpoints map[string][]LegacyPipelineStepCheckpoint // pipelineID -> steps
}

func NewPipelineDurable() *PipelineDurable {
	return &PipelineDurable{
		checkpoints: make(map[string][]LegacyPipelineStepCheckpoint),
	}
}

// Checkpoint records a step's progress.
func (pd *PipelineDurable) Checkpoint(pipelineID string, stepIndex int, stepName, status, output, errMsg string) {
	cp := LegacyPipelineStepCheckpoint{
		PipelineID: pipelineID,
		StepIndex:  stepIndex,
		StepName:   stepName,
		Status:     status,
		Output:     output,
		Error:      errMsg,
		UpdatedAt:  time.Now(),
	}

	pd.checkpoints[pipelineID] = append(pd.checkpoints[pipelineID], cp)
}

// GetResumePoint returns the first incomplete step after a crash.
// Returns stepIndex to resume from, or -1 if all complete.
func (pd *PipelineDurable) GetResumePoint(pipelineID string) int {
	steps, ok := pd.checkpoints[pipelineID]
	if !ok {
		return 0 // no checkpoints, start from beginning
	}
	for i, cp := range steps {
		if cp.Status == "failed" || cp.Status == "running" {
			return cp.StepIndex // resume from this step
		}
		if cp.Status == "completed" && i == len(steps)-1 {
			return -1 // all done
		}
	}
	return len(steps) // next unexecuted step
}

// Summary returns a human-readable checkpoint summary.
func (pd *PipelineDurable) Summary(pipelineID string) string {
	steps, ok := pd.checkpoints[pipelineID]
	if !ok {
		return "No checkpoints"
	}
	var done, failed, running int
	for _, cp := range steps {
		switch cp.Status {
		case "completed":
			done++
		case "failed":
			failed++
		case "running":
			running++
		}
	}
	return fmt.Sprintf("Pipeline %s: %d steps (%d done, %d failed, %d running)", pipelineID, len(steps), done, failed, running)
}

// DurablePipelineTool exposes checkpoint operations to agents.
type DurablePipelineTool struct {
	durable *PipelineDurable
}

func NewDurablePipelineTool(durable *PipelineDurable) *DurablePipelineTool {
	if durable == nil {
		durable = NewPipelineDurable()
	}
	return &DurablePipelineTool{durable: durable}
}

func (t *DurablePipelineTool) Name() string { return "pipeline_checkpoint" }
func (t *DurablePipelineTool) Description() string {
	return "Manage durable pipeline execution with crash recovery. " +
		"Actions: status (show progress), resume (get next step to run), checkpoint (record step completion)"
}
func (t *DurablePipelineTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"status", "resume", "checkpoint"},
			},
			"pipeline_id": map[string]interface{}{
				"type": "string", "description": "Pipeline ID",
			},
			"step_index": map[string]interface{}{
				"type": "integer", "description": "Step index (0-based)",
			},
			"step_name": map[string]interface{}{
				"type": "string", "description": "Step name",
			},
			"status": map[string]interface{}{
				"type": "string", "description": "completed, failed, or running",
			},
		},
		"required": []string{"action", "pipeline_id"},
	}
}

func (t *DurablePipelineTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "status")
	pid := configvalue.String(params, "pipeline_id", "")

	switch action {
	case "status":
		return t.durable.Summary(pid), nil
	case "resume":
		next := t.durable.GetResumePoint(pid)
		if next < 0 {
			return "All steps completed", nil
		}
		return fmt.Sprintf("Resume from step %d", next), nil
	case "checkpoint":
		idx := configvalue.Int(params, "step_index", 0)
		name := configvalue.String(params, "step_name", "")
		status := configvalue.String(params, "status", "completed")
		t.durable.Checkpoint(pid, idx, name, status, "", "")
		return fmt.Sprintf("Checkpoint saved: %s step %d = %s", pid, idx, status), nil
	}
	return "Unknown action", nil
}
