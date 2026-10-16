package middleware

import "mac/internal/permissionpolicy"

// DenialTrackerMiddleware wraps the existing DenialTracker as an AgentMiddleware.
// It hooks AfterTool to track denials and escalate when limits are exceeded.
type DenialTrackerMiddleware struct {
	Base
	tracker *permissionpolicy.DenialTracker
}

func NewDenialTrackerMiddleware(tracker *permissionpolicy.DenialTracker) *DenialTrackerMiddleware {
	if tracker == nil {
		return nil
	}
	return &DenialTrackerMiddleware{
		Base:    NewBase("denial_tracker"),
		tracker: tracker,
	}
}

func (m *DenialTrackerMiddleware) AfterTool(ctx *Context) error {
	if m == nil || m.tracker == nil {
		return nil
	}

	// Check if this tool was blocked/denied
	wasBlocked := false
	if ctx.ToolResult != nil {
		content := ctx.ToolResult.Content
		if len(content) > 0 {
			if len(content) >= 8 && content[:8] == "[BLOCKED" {
				wasBlocked = true
			}
			if len(content) >= 22 && content[:22] == "[CONFIRMATION REQUIRED]" {
				wasBlocked = true
			}
			if len(content) >= 16 && content[:16] == "[FORCED STOP] " {
				wasBlocked = true
			}
		}
	}
	// Also check guardrailBlockError from earlier middleware
	if ctx.ToolErr != nil {
		if _, ok := ctx.ToolErr.(*GuardrailBlockError); ok {
			wasBlocked = true
		}
	}

	if wasBlocked {
		m.tracker.RecordUserDenial()
		ctx.SetMeta("denial_tracker.escalate", m.tracker.ShouldFallback())
	} else {
		m.tracker.RecordUserApproval()
	}

	return nil
}
