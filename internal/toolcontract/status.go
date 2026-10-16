package toolcontract

import "strings"

var toolFailureMarkers = []string{
	"执行失败",
	"tool call rejected",
	"[fail]",
	"[invalid_args]",
	"status: failed",
	"status: failed_compacted",
	"[blocked]",
	"[blocked by hook]",
	"[confirmation required]",
	"tool error:",
	"tests failed",
	"test failed",
	"fail:",
	"build failed",
	"compilation failed",
	"exit status",
	"syntaxerror:",
	"indentationerror:",
	"nameerror:",
	"typeerror:",
	"valueerror:",
	"modulenotfounderror:",
	"filenotfounderror:",
	"panic:",
	"traceback",
	"exception",
	"fatal:",
}

func ToolResultLooksFailed(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "[non-fatal encoding warning]") &&
		(strings.Contains(lower, "exit code was 0") || strings.Contains(lower, "subprocess completed")) {
		return false
	}
	for _, marker := range toolFailureMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func ToolResultLooksFailedForTool(toolName, content string) bool {
	for _, mutationTool := range []string{"edit_file", "write_file", "json_patch", "multi_edit", "multiedit"} {
		if !strings.EqualFold(toolName, mutationTool) {
			continue
		}
		trimmed := strings.TrimSpace(content)
		lower := strings.ToLower(trimmed)
		// Source diffs are content payloads: newly added code can legitimately
		// contain words such as "Exception", "panic", or "failed". A concrete
		// edit receipt is the mutation tool's authoritative success marker and
		// must be evaluated before scanning the diff body for generic failures.
		for _, prefix := range []string{
			"执行失败:", "tool call rejected", "[invalid_args]", "[blocked]",
			"status: failed", "tool error:",
		} {
			if strings.HasPrefix(lower, strings.ToLower(prefix)) {
				return true
			}
		}
		if strings.Contains(lower, "[edit receipt]") {
			return false
		}
		break
	}
	if strings.EqualFold(toolName, "use_skill") {
		trimmed := strings.TrimSpace(content)
		lower := strings.ToLower(trimmed)
		// Skill bodies routinely discuss exceptions, failures, retries, and
		// blocked states as instructions.  Those words are source prose, not the
		// result of executing use_skill.  Only the tool's explicit error envelope
		// may turn a skill activation into a failed tool call.
		for _, prefix := range []string{
			"技能执行失败:", "tool call rejected", "[invalid_args]",
			"status: failed", "tool error:",
		} {
			if strings.HasPrefix(lower, strings.ToLower(prefix)) {
				return true
			}
		}
		if strings.HasPrefix(trimmed, "Tool Result\n") || strings.HasPrefix(trimmed, "Tool Result\r\n") {
			for _, line := range strings.Split(trimmed, "\n") {
				statusLine := strings.ToLower(strings.TrimSpace(line))
				if strings.HasPrefix(statusLine, "status:") {
					status := strings.TrimSpace(strings.TrimPrefix(statusLine, "status:"))
					return status == "failed" || status == "failed_compacted"
				}
			}
		}
		return false
	}
	if strings.EqualFold(toolName, "persistent_shell") {
		lower := strings.ToLower(content)
		if strings.Contains(lower, "状态: 失败") || strings.Contains(lower, "status: failed") {
			return true
		}
		if idx := strings.Index(lower, "output result:"); idx >= 0 {
			return ToolResultLooksFailed(content[idx+len("output result:"):])
		}
		if strings.Contains(lower, "状态: 成功") || strings.Contains(lower, "status: success") {
			return false
		}
	}
	if strings.EqualFold(toolName, "read_file") {
		trimmed := strings.TrimSpace(content)
		if !strings.HasPrefix(trimmed, "Tool Result\n") && !strings.HasPrefix(trimmed, "Tool Result\r\n") {
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "tool call rejected") ||
				strings.HasPrefix(lower, "[invalid_args]") ||
				strings.HasPrefix(lower, "执行失败:") ||
				strings.HasPrefix(lower, "status: failed") {
				return true
			}
			return false
		}
	}
	if strings.EqualFold(toolName, "web_fetch") {
		trimmed := strings.TrimSpace(content)
		// Successful web pages routinely contain prose such as "failed login",
		// "error rate", or "failure recovery". Those words are page content, not
		// an execution status. Only inspect explicit tool envelopes/prefixes here.
		if strings.HasPrefix(trimmed, "Tool Result\n") || strings.HasPrefix(trimmed, "Tool Result\r\n") {
			for _, line := range strings.Split(trimmed, "\n") {
				lowerLine := strings.ToLower(strings.TrimSpace(line))
				if strings.HasPrefix(lowerLine, "status:") {
					status := strings.TrimSpace(strings.TrimPrefix(lowerLine, "status:"))
					return status == "failed" || status == "failed_compacted"
				}
			}
			return ToolResultLooksFailed(content)
		}
		if !strings.HasPrefix(trimmed, "Tool Result\n") && !strings.HasPrefix(trimmed, "Tool Result\r\n") {
			lower := strings.ToLower(trimmed)
			for _, prefix := range []string{
				"执行失败:", "tool call rejected", "[invalid_args]", "[blocked]",
				"status: failed", "tool error:", "error fetching", "http error:",
			} {
				if strings.HasPrefix(lower, strings.ToLower(prefix)) {
					return true
				}
			}
			return false
		}
	}
	return ToolResultLooksFailed(content)
}

func ToolExecutionSucceeded(content string, err error) bool {
	return err == nil && !ToolResultLooksFailed(content)
}

func ToolExecutionSucceededForTool(toolName, content string, err error) bool {
	return err == nil && !ToolResultLooksFailedForTool(toolName, content)
}

func OutcomePointer(value bool) *bool {
	outcome := value
	return &outcome
}

// ToolResultExecutionSucceeded prefers the execution outcome captured at the
// tool boundary. Content heuristics remain as a compatibility fallback for
// manually constructed and older results.
func ToolResultExecutionSucceeded(result Result, err error) bool {
	if err != nil {
		return false
	}
	if result.ExecutionSucceeded != nil {
		return *result.ExecutionSucceeded
	}
	return ToolExecutionSucceededForTool(result.Name, result.Content, nil)
}

func ToolResultContainsAny(content string, markers ...string) bool {
	lower := strings.ToLower(content)
	for _, marker := range markers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}
