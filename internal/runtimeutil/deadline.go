// Package runtimeutil contains small, dependency-free helpers shared by the
// agent runtime and its orchestration layers.
package runtimeutil

import (
	"context"
	"fmt"
	"time"
)

// DeadlineDeliveryBudgetNotice reserves the closing portion of a bounded turn
// for the user's actual outcome. It is deliberately domain-neutral: the engine
// does not infer an answer or a workflow, it only discourages optional
// exploration when the caller's real deadline is approaching.
func DeadlineDeliveryBudgetNotice(ctx context.Context, startedAt, now time.Time) (string, bool) {
	if ctx == nil {
		return "", false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", false
	}
	total := deadline.Sub(startedAt)
	remaining := deadline.Sub(now)
	if total < 90*time.Second || remaining <= 0 {
		return "", false
	}
	reserve := total * 2 / 5
	if reserve < 45*time.Second {
		reserve = 45 * time.Second
	}
	if reserve > 2*time.Minute {
		reserve = 2 * time.Minute
	}
	if remaining > reserve {
		return "", false
	}
	seconds := int(remaining.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf(`[DEADLINE DELIVERY CHECKPOINT]
Approximately %d seconds remain in this bounded task.
- Stop optional exploration, broad probes, and repeated equivalent checks.
- Preserve correctness: do not guess or claim evidence you do not have.
- If the core result is known, create the user's required deliverables now, starting with the smallest essential artifact, then run the shortest meaningful verification.
- Avoid new helper files unless they are strictly necessary to finish.
- If the result is still unresolved, return an accurate partial result with the concrete blocker instead of spending the remaining budget without a deliverable.`, seconds), true
}
