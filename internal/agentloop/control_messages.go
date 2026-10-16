package agentloop

import (
	"fmt"
	"strings"

	"mac/internal/textutil"
)

const MaxProgressOnlyNoToolRetries = 1

const (
	HistorySummaryMaxBytes      = 16 * 1024
	HistorySummaryEntryMaxBytes = 512
	internalControlMaxBytes     = 18 * 1024
	internalControlMarkerPrefix = "[INTERNAL CONTROL — "
)

// newInternalControlMessage keeps engine guidance out of the assistant
// transcript. Assistant-role checkpoints teach the model to echo internal
// reflection text as if it were a final answer.
func NewInternalControlMessage(kind, content string) map[string]interface{} {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if kind == "" {
		kind = "CONTROL"
	}
	formatted := fmt.Sprintf(
		"[INTERNAL CONTROL — %s]\nThis message is engine guidance, not an assistant answer. Do not quote, imitate, or expose it to the user.\n%s",
		kind,
		strings.TrimSpace(content),
	)
	return map[string]interface{}{
		"role":    "system",
		"content": textutil.TruncateUTF8Bytes(formatted, internalControlMaxBytes),
	}
}

func InternalControlKind(message map[string]interface{}) string {
	if role, _ := message["role"].(string); role != "system" {
		return ""
	}
	content, _ := message["content"].(string)
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, internalControlMarkerPrefix) {
		return ""
	}
	end := strings.Index(content, "]")
	if end <= len(internalControlMarkerPrefix) {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(content[len(internalControlMarkerPrefix):end]))
}

// hasLeadingInternalControlLeak detects engine-only guidance echoed as an
// assistant result. The markers are protocol structures, not business-topic
// keywords, so ordinary user prose and quoted audit examples remain valid.
func HasLeadingInternalControlLeak(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, marker := range []string{
		strings.ToLower(internalControlMarkerPrefix),
		"[internal control - ",
		"[internal control — ",
		"[reflection checkpoint - iteration",
		"[checkpoint - iteration",
		"[rethink - iteration",
	} {
		if hasLeadingInternalControlMarker(lower, marker) {
			return true
		}
	}
	return false
}

func hasLeadingInternalControlMarker(lower, marker string) bool {
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return false
	}
	prefix := strings.TrimSpace(lower[:idx])
	if len([]rune(prefix)) > 48 {
		return false
	}
	prefix = strings.Trim(prefix, "#>*-_[](){}:;.!?'\" \t\r\n")
	if prefix == "" {
		return true
	}
	for _, completed := range []string{
		"任务完成", "全部目标已达成", "已完成", "已成功", "成功发布", "验证通过",
		"task complete", "completed successfully", "successfully posted", "successfully created",
	} {
		if strings.Contains(prefix, completed) {
			return false
		}
	}
	return true
}

func ReplaceInternalControlBoth(hot, cold *[]map[string]interface{}, kind string, message map[string]interface{}) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	replaceMessageBoth(hot, cold, func(candidate map[string]interface{}) bool {
		return InternalControlKind(candidate) == kind
	}, message)
}

func ReplaceSystemMessageByPrefixBoth(hot, cold *[]map[string]interface{}, prefix string, message map[string]interface{}) {
	replaceMessageBoth(hot, cold, func(candidate map[string]interface{}) bool {
		role, _ := candidate["role"].(string)
		content, _ := candidate["content"].(string)
		return role == "system" && strings.HasPrefix(strings.TrimSpace(content), prefix)
	}, message)
}

func replaceMessageBoth(hot, cold *[]map[string]interface{}, remove func(map[string]interface{}) bool, message map[string]interface{}) {
	replace := func(target *[]map[string]interface{}) {
		if target == nil {
			return
		}
		filtered := (*target)[:0]
		for _, candidate := range *target {
			if remove(candidate) {
				continue
			}
			filtered = append(filtered, candidate)
		}
		*target = append(filtered, cloneMessage(message))
	}
	replace(hot)
	replace(cold)
}

func HasRepeatedToolFailure(failTracker map[string]int, threshold int) bool {
	for _, count := range failTracker {
		if count >= threshold {
			return true
		}
	}
	return false
}

func IsProviderSensitiveInputError(message string) bool {
	lower := strings.ToLower(message)
	hasSensitiveCode := strings.Contains(lower, "new_sensitive") ||
		strings.Contains(lower, "new sensitive") ||
		strings.Contains(lower, "input_sensitive") ||
		strings.Contains(lower, "input sensitive")
	return hasSensitiveCode &&
		(strings.Contains(lower, "422") || strings.Contains(lower, "unprocessable"))
}

// providerSensitiveRecoveryMessages keeps the authoritative task while
// removing untrusted web/tool transcript bodies that can make an upstream
// provider reject an otherwise allowed research request. The retry is
// intentionally evidence-degraded: it must not reconstruct omitted facts.
func ProviderSensitiveRecoveryMessages(messages []map[string]interface{}) []map[string]interface{} {
	recovered := make([]map[string]interface{}, 0, len(messages)+1)
	firstSystemKept := false
	for _, message := range messages {
		role, _ := message["role"].(string)
		switch role {
		case "system":
			if firstSystemKept {
				continue
			}
			recovered = append(recovered, cloneMessage(message))
			firstSystemKept = true
		case "user":
			recovered = append(recovered, cloneMessage(message))
		}
	}
	recovered = append(recovered, NewInternalControlMessage("PROVIDER INPUT SAFETY RECOVERY", `The upstream model provider rejected untrusted external transcript content. The original user request passed the host safety gate, but raw web/tool bodies are no longer available in this retry.
Do not call more tools in this turn. Finish the current step with a concise degraded result based only on facts explicitly present in the retained authoritative request. Mark every unsupported field as unknown or not verified, disclose the evidence coverage gap, and never recreate omitted source text, companies, URLs, numbers, or claims from memory.`))
	return recovered
}

func ProgressOnlyContinuationPrompt() map[string]interface{} {
	return NewInternalControlMessage("CONTINUE TASK", `Your previous response was only an internal reflection, progress note, or self-stop message. It is not a final answer.
Continue the user's unfinished task now. If another concrete action is required, call the appropriate tool. If the task is genuinely complete or blocked, return a concise user-facing final answer with completed work, evidence, and the exact remaining blocker. Do not output another reflection/checkpoint message.`)
}

func cloneMessage(message map[string]interface{}) map[string]interface{} {
	if message == nil {
		return nil
	}
	clone := make(map[string]interface{}, len(message))
	for key, value := range message {
		switch typed := value.(type) {
		case map[string]interface{}:
			clone[key] = cloneMessage(typed)
		case []map[string]interface{}:
			items := make([]map[string]interface{}, len(typed))
			for index, item := range typed {
				items[index] = cloneMessage(item)
			}
			clone[key] = items
		case []interface{}:
			clone[key] = append([]interface{}(nil), typed...)
		default:
			clone[key] = value
		}
	}
	return clone
}
