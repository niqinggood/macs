package coding

import (
	"fmt"
	"regexp"
	"strings"

	"mac/internal/permissionpolicy"
	"mac/internal/shellpolicy"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

type verifyWorkVerdict string

const (
	verifyWorkVerdictUnknown verifyWorkVerdict = ""
	verifyWorkVerdictPass    verifyWorkVerdict = "PASS"
	verifyWorkVerdictFail    verifyWorkVerdict = "FAIL"
	verifyWorkVerdictPartial verifyWorkVerdict = "PARTIAL"
)

var (
	verifyWorkVerdictPattern  = regexp.MustCompile(`(?im)^\s*VERDICT:\s*(PASS|FAIL|PARTIAL)\s*$`)
	verifyWorkEvidencePattern = regexp.MustCompile(`(?im)^\s*(?:EVIDENCE|证据)\s*:`)
)

func parseVerifyWorkVerdict(output string) (verifyWorkVerdict, bool) {
	matches := verifyWorkVerdictPattern.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return verifyWorkVerdictUnknown, false
	}
	verdict := verifyWorkVerdict(strings.ToUpper(matches[0][1]))
	for _, match := range matches[1:] {
		if verifyWorkVerdict(strings.ToUpper(match[1])) != verdict {
			return verifyWorkVerdictPartial, true
		}
	}
	return verdict, true
}

func NormalizeVerifyWorkOutput(output string) string {
	output = strings.TrimSpace(output)
	verdict, found := parseVerifyWorkVerdict(output)
	lines := strings.Split(output, "\n")
	bodyLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if verifyWorkVerdictPattern.MatchString(line) {
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if !found {
		verdict = verifyWorkVerdictPartial
		if body != "" {
			body += "\n\n"
		}
		body += "Verification output omitted the required verdict."
	}
	if verdict == verifyWorkVerdictPass && !verifyWorkEvidencePattern.MatchString(body) {
		verdict = verifyWorkVerdictPartial
		if body != "" {
			body += "\n\n"
		}
		body += "A PASS verdict requires an EVIDENCE section with concrete checks."
	}
	if body == "" {
		body = "No verification evidence was returned."
		if verdict == verifyWorkVerdictPass {
			verdict = verifyWorkVerdictPartial
		}
	}
	return body + "\n\nVERDICT: " + string(verdict)
}

func latestVerifyWorkVerdict(toolResults []string) (verifyWorkVerdict, bool) {
	for i := len(toolResults) - 1; i >= 0; i-- {
		record, ok := toolcontract.ParseRecord(toolResults[i])
		if !ok || !strings.EqualFold(record.Tool, "verify_work") {
			continue
		}
		verdict, found := parseVerifyWorkVerdict(toolResults[i])
		if !record.Succeeded && (!found || verdict == verifyWorkVerdictPass) {
			return verifyWorkVerdictFail, true
		}
		return verdict, found
	}
	return verifyWorkVerdictUnknown, false
}

func AutoVerificationCompletionIssue(required, attempted bool, toolResults []string) string {
	if !required {
		return ""
	}
	if !attempted {
		return "required verify_work was not called"
	}
	verdict, found := latestVerifyWorkVerdict(toolResults)
	if !found {
		return "verify_work was called but did not return a valid verdict"
	}
	switch verdict {
	case verifyWorkVerdictPass:
		return ""
	case verifyWorkVerdictFail:
		return "verify_work returned FAIL"
	case verifyWorkVerdictPartial:
		return "verify_work returned PARTIAL; runtime or acceptance evidence is incomplete"
	default:
		return "verify_work returned an unknown verdict"
	}
}

// buildAutoVerificationToolCall turns a host-required verification into a
// structured, read-only call.  Relying on another model turn to reproduce the
// tool name and schema made the gate nondeterministic and could exhaust the
// iteration budget after the deliverable was already complete.
func BuildAutoVerificationToolCall(userRequest, trigger string, toolCallSummary, toolResults []string, iteration int) toolcontract.Call {
	claim := "Verify the completed state change"
	if trigger = strings.TrimSpace(trigger); trigger != "" {
		claim += ": " + textutil.Truncate(trigger, 500)
	}

	evidence := make([]interface{}, 0, 8)
	for _, item := range tailStrings(toolCallSummary, 4) {
		if item = strings.TrimSpace(item); item != "" {
			evidence = append(evidence, "tool call: "+textutil.Truncate(item, 500))
		}
	}
	for _, item := range tailStrings(toolResults, 4) {
		item = strings.TrimSpace(item)
		if item != "" {
			evidence = append(evidence, "tool result: "+textutil.Truncate(item, 900))
		}
	}

	return toolcontract.Call{
		ID:   fmt.Sprintf("host_auto_verify_%d", iteration),
		Name: "verify_work",
		Arguments: map[string]interface{}{
			"claim":               claim,
			"acceptance_criteria": textutil.Truncate(strings.TrimSpace(userRequest), 1600),
			"evidence":            evidence,
			"max_iterations":      2,
		},
	}
}

func AppendAutoVerificationFailure(answer, issue string) string {
	answer = strings.TrimSpace(answer)
	message := "验证未通过：" + strings.TrimSpace(issue) + "。本次任务不能标记为完成。"
	if answer == "" {
		return message
	}
	if strings.Contains(answer, message) {
		return answer
	}
	return answer + "\n\n" + message
}

func shouldRequestAutoVerification(calls []toolcontract.Call) bool {
	for _, call := range calls {
		if ToolCallRequiresAutoVerification(call) {
			return true
		}
	}
	return false
}

// toolCallRequiresAutoVerification is deliberately narrower than
// toolCallMutatesDeliverable. Ordinary writes are already covered by the
// write result/diff and proportionate deterministic tests; spawning a second
// agent after every write made the final answer wait for minutes without
// adding useful evidence. Keep automatic, blocking verification only for
// unusually risky state changes. Manual verify_work remains available.
func ToolCallRequiresAutoVerification(call toolcontract.Call) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "cli":
		command := strings.ToLower(strings.TrimSpace(permissionpolicy.ExtractAction(call.Arguments)))
		if strings.HasPrefix(command, "git push ") || command == "git push" {
			return true
		}
		return shellpolicy.Analyze(command).Class == shellpolicy.ClassDestructive
	case "git":
		return strings.EqualFold(strings.TrimSpace(permissionpolicy.ExtractAction(call.Arguments)), "push")
	default:
		return false
	}
}

func ShouldInjectAutoVerificationBeforeFinal(required, attempted, planComplete, progressOnly bool) bool {
	return required && !attempted && planComplete && !progressOnly
}

func toolCallMutatesDeliverable(call toolcontract.Call) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "write_file", "edit_file", "json_patch", "multiedit", "project_init", "create_skill":
		return true
	case "cli":
		analysis := shellpolicy.Analyze(permissionpolicy.ExtractAction(call.Arguments))
		switch analysis.Class {
		case shellpolicy.ClassWrite, shellpolicy.ClassDestructive, shellpolicy.ClassLongRunning:
			return true
		default:
			return false
		}
	case "git":
		action := strings.ToLower(strings.TrimSpace(permissionpolicy.ExtractAction(call.Arguments)))
		return action == "add" || action == "commit" || action == "checkout" || action == "restore" || action == "push" || action == "init"
	default:
		return false
	}
}

func HasToolCallNamed(calls []toolcontract.Call, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, call := range calls {
		if strings.EqualFold(strings.TrimSpace(call.Name), name) {
			return true
		}
	}
	return false
}

func summarizeVerificationTriggerCalls(calls []toolcontract.Call) string {
	var parts []string
	for _, call := range calls {
		if !ToolCallRequiresAutoVerification(call) {
			continue
		}
		parts = append(parts, FormatToolCallBrief(call))
	}
	if len(parts) == 0 {
		return "project state changed"
	}
	return strings.Join(parts, ", ")
}

func BuildAutoVerificationPrompt(userRequest, trigger string, toolCallSummary, allToolResults []string) string {
	var b strings.Builder
	b.WriteString("[Auto verification required]\n")
	b.WriteString("Recent tool calls changed project state")
	if strings.TrimSpace(trigger) != "" {
		b.WriteString(": ")
		b.WriteString(trigger)
	}
	b.WriteString(". Before giving the final answer, call `verify_work` once so the read-only verification agent can check the outcome.\n\n")
	b.WriteString("Use `verify_work` with:\n")
	b.WriteString("- `claim`: concise summary of what you believe is complete.\n")
	b.WriteString("- `acceptance_criteria`: the user's request and observable behavior/files that must be true.\n")
	b.WriteString("- `evidence`: relevant files, commands, and test/check outputs from this turn.\n\n")
	if strings.TrimSpace(userRequest) != "" {
		b.WriteString("Original user request:\n")
		b.WriteString(textutil.Truncate(userRequest, 1200))
		b.WriteString("\n\n")
	}
	if len(toolCallSummary) > 0 {
		b.WriteString("Recent tool calls:\n")
		for _, item := range tailStrings(toolCallSummary, 8) {
			b.WriteString("- ")
			b.WriteString(textutil.Truncate(item, 300))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(allToolResults) > 0 {
		b.WriteString("Recent tool result hints:\n")
		for _, item := range tailStrings(allToolResults, 6) {
			b.WriteString("- ")
			b.WriteString(textutil.Truncate(item, 500))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nDo not modify files during verification. The result must contain an `EVIDENCE:` section followed by concrete checks and finish with exactly one `VERDICT: PASS`, `VERDICT: FAIL`, or `VERDICT: PARTIAL` line. If verification cannot fully run, return PARTIAL and state the gap.")
	return b.String()
}

func FormatToolCallBrief(call toolcontract.Call) string {
	name := strings.TrimSpace(call.Name)
	for _, key := range []string{"path", "file", "command", "action", "name"} {
		if value, ok := call.Arguments[key].(string); ok && strings.TrimSpace(value) != "" {
			return fmt.Sprintf("%s(%s=%s)", name, key, textutil.Truncate(strings.TrimSpace(value), 160))
		}
	}
	return name
}

func tailStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[len(values)-limit:]
}
