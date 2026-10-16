package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ====================== Hook Output Schema Validation ======================
// Claude Code 借鉴: Zod-validated hook JSON output

// ValidateHookOutput parses raw hook stdout into a structured HookResultV2.
// Handles three cases:
//  1. Valid JSON with "decision" field → HookResultV2
//  2. Valid JSON without "decision" → treated as passthrough with message
//  3. Plain text → auto-wrapped as {decision: "allow", message: text}
func ValidateHookOutput(raw []byte) (*HookResultV2, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return &HookResultV2{Decision: HookDecisionAllow}, nil
	}

	// Try JSON parse first
	var parsed HookResultV2
	if err := json.Unmarshal(raw, &parsed); err == nil {
		// Valid JSON — check if decision field was present
		if parsed.Decision == "" {
			// JSON with no decision field — treat content as message
			// Re-parse to extract raw message
			var rawMap map[string]interface{}
			json.Unmarshal(raw, &rawMap)
			msg, _ := json.Marshal(rawMap)
			return &HookResultV2{
				Decision: HookDecisionAllow,
				Message:  string(msg),
			}, nil
		}
		// Validate decision value
		switch parsed.Decision {
		case HookDecisionAllow, HookDecisionBlock, HookDecisionPassthrough:
			return &parsed, nil
		default:
			return nil, fmt.Errorf("invalid hook decision: %q (must be allow/block/passthrough)", parsed.Decision)
		}
	}

	// Plain text output → auto-wrap as allow with message
	return &HookResultV2{
		Decision: HookDecisionAllow,
		Message:  s,
	}, nil
}

// ValidateHookOutputStrict requires valid JSON with decision field.
// Returns an error for plain text output.
func ValidateHookOutputStrict(raw []byte) (*HookResultV2, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return &HookResultV2{Decision: HookDecisionAllow}, nil
	}

	var parsed HookResultV2
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("hook output is not valid JSON: %w (got: %.100s)", err, s)
	}
	if parsed.Decision == "" {
		return nil, fmt.Errorf("hook output missing required 'decision' field")
	}
	switch parsed.Decision {
	case HookDecisionAllow, HookDecisionBlock, HookDecisionPassthrough:
		return &parsed, nil
	default:
		return nil, fmt.Errorf("invalid hook decision: %q", parsed.Decision)
	}
}
