package agentloop

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// PlanStepStatus 表示计划步骤的执行状态
type PlanStepStatus string

const (
	PlanStepPending    PlanStepStatus = "pending"
	PlanStepInProgress PlanStepStatus = "in_progress"
	PlanStepCompleted  PlanStepStatus = "completed"
	PlanStepSkipped    PlanStepStatus = "skipped"
)

// PlanStep 计划中的一个步骤
type PlanStep struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      PlanStepStatus `json:"status"`
}

// ActivePlan agent 当前会话的执行计划
type ActivePlan struct {
	mu     sync.RWMutex
	PlanID string     `json:"plan_id"`
	Steps  []PlanStep `json:"steps"`
}

// NewActivePlan 创建新的执行计划，第一个步骤自动设为 in_progress
func NewActivePlan(steps []PlanStep) *ActivePlan {
	if len(steps) > 0 && steps[0].Status == PlanStepPending {
		steps[0].Status = PlanStepInProgress
	}
	return &ActivePlan{
		PlanID: fmt.Sprintf("plan_%d", time.Now().UnixNano()),
		Steps:  steps,
	}
}

// MarkStep 更新步骤状态，completed 时自动推进下一步为 in_progress
func (p *ActivePlan) MarkStep(stepID string, status PlanStepStatus) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.Steps {
		if p.Steps[i].ID == stepID {
			if status == PlanStepInProgress {
				for j := range p.Steps {
					if j != i && p.Steps[j].Status == PlanStepInProgress {
						p.Steps[j].Status = PlanStepPending
					}
				}
			}
			p.Steps[i].Status = status
			if status == PlanStepCompleted {
				for j := i + 1; j < len(p.Steps); j++ {
					if p.Steps[j].Status == PlanStepInProgress {
						return nil
					}
				}
				for j := i + 1; j < len(p.Steps); j++ {
					if p.Steps[j].Status == PlanStepPending {
						p.Steps[j].Status = PlanStepInProgress
						break
					}
				}
			}
			return nil
		}
	}
	return fmt.Errorf("step %s not found", stepID)
}

// IsComplete 检查所有步骤是否都已完成或跳过
func (p *ActivePlan) IsComplete() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.Steps {
		if s.Status != PlanStepCompleted && s.Status != PlanStepSkipped {
			return false
		}
	}
	return true
}

// CurrentStepIndex 返回第一个 in_progress 步骤的索引，-1 表示没有
func (p *ActivePlan) CurrentStepIndex() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i, s := range p.Steps {
		if s.Status == PlanStepInProgress {
			return i
		}
	}
	return -1
}

// Snapshot returns a lock-safe copy suitable for persistence and inspection.
func (p *ActivePlan) Snapshot() (string, []PlanStep) {
	if p == nil {
		return "", nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PlanID, append([]PlanStep(nil), p.Steps...)
}

// FormatProgress 返回格式化的进度摘要，用于注入到消息中
func (p *ActivePlan) FormatProgress() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Plan: %s]\n", p.PlanID))

	allDone := true
	hasInProgress := false
	var currentTitle, currentDesc string

	for i, step := range p.Steps {
		icon := map[PlanStepStatus]string{
			PlanStepPending:    "⬜",
			PlanStepInProgress: "🔄",
			PlanStepCompleted:  "✅",
			PlanStepSkipped:    "⏭️",
		}[step.Status]
		b.WriteString(fmt.Sprintf("%s Step %d: %s\n", icon, i+1, step.Title))
		if step.Status != PlanStepCompleted && step.Status != PlanStepSkipped {
			allDone = false
		}
		if step.Status == PlanStepInProgress {
			hasInProgress = true
			currentTitle = step.Title
			currentDesc = step.Description
		}
	}

	if allDone {
		b.WriteString("\nAll steps complete. Provide your final answer and stop.")
		return b.String()
	}

	if hasInProgress {
		b.WriteString(fmt.Sprintf("\nNow on: %s — %s", currentTitle, currentDesc))
	} else {
		// 找第一个 pending
		for _, step := range p.Steps {
			if step.Status == PlanStepPending {
				b.WriteString(fmt.Sprintf("\nNext: %s — %s", step.Title, step.Description))
				break
			}
		}
	}

	return b.String()
}
