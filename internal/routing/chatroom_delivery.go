package routing

import (
	"fmt"
	"regexp"
	"strings"

	"mac/internal/agentloop"
	"mac/internal/toolcontract"
)

var (
	chatroomTargetPattern           = regexp.MustCompile(`(?i)(?:nova\s*)?chatroom|聊天室`)
	chatroomDeliveryPattern         = regexp.MustCompile(`(?i)(?:发|发送|发布|上传|同步|推送|投递|转发)(?:给|至|到|往|进)?\s*(?:nova\s*)?(?:chatroom|聊天室)|(?:post|publish|send|upload|share)\b.{0,32}\b(?:to|on|into)\s+(?:nova\s+)?chatroom`)
	chatroomDeliveryNegativePattern = regexp.MustCompile(`(?i)(?:不要|不用|无需|无须|不必|别|禁止|不得).{0,24}(?:发|发送|发布|上传|同步|推送|投递|转发|post|publish|send|upload|share)`)
)

type ChatroomDeliveryAttempt struct {
	ActionIntent string
	Succeeded    bool
	ReceiptValid bool
}

func ChatroomDeliveryRequested(input string, clauses []string) bool {
	if strings.TrimSpace(input) == "" || !chatroomTargetPattern.MatchString(input) {
		return false
	}
	for _, clause := range clauses {
		if chatroomTargetPattern.MatchString(clause) && chatroomDeliveryPattern.MatchString(clause) && !chatroomDeliveryNegativePattern.MatchString(clause) {
			return true
		}
	}
	return false
}

func ChatroomDeliveryRequestedFromInput(input string) bool {
	input = strings.NewReplacer(". ", "\n", "! ", "\n", "? ", "\n").Replace(input)
	clauses := strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', ';', '，', ',', '\n', '\r':
			return true
		default:
			return false
		}
	})
	return ChatroomDeliveryRequested(input, clauses)
}

// ChatroomDeliveryStatus makes the most recent post/reply authoritative so a
// stale success cannot hide a later failed or uncertain mutation.
func ChatroomDeliveryStatus(attempts []ChatroomDeliveryAttempt) (satisfied, attempted bool) {
	for i := len(attempts) - 1; i >= 0; i-- {
		attempt := attempts[i]
		if attempt.ActionIntent != "post" && attempt.ActionIntent != "reply" {
			continue
		}
		return attempt.Succeeded && attempt.ReceiptValid, true
	}
	return false, false
}

func ChatroomDeliveryStatusFromResults(results []string) (satisfied, attempted bool) {
	attempts := make([]ChatroomDeliveryAttempt, 0, len(results))
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok {
			continue
		}
		validReceipt := false
		if record.Succeeded {
			if receipt, _, _, valid := toolcontract.ParseActionReceipt(result); valid &&
				strings.EqualFold(receipt.Action, record.ActionIntent) && receipt.ContentSHA256 != "" {
				validReceipt = true
			} else if legacyAction, _, valid := toolcontract.ParseLegacySuccessfulAction(result); valid && legacyAction == record.ActionIntent {
				validReceipt = true
			}
		}
		attempts = append(attempts, ChatroomDeliveryAttempt{
			ActionIntent: record.ActionIntent,
			Succeeded:    record.Succeeded,
			ReceiptValid: validReceipt,
		})
	}
	return ChatroomDeliveryStatus(attempts)
}

func ChatroomDeliveryCompletionIssue(requested, satisfied, attempted bool) (string, bool) {
	if !requested || satisfied {
		return "", requested && satisfied
	}
	if attempted {
		return "Chatroom 发布已尝试，但没有可信的成功回执；为避免重复发布，不能自动重试", true
	}
	return "未执行用户明确要求的 Chatroom 发布，或未取得发布结果 ID 与内容哈希回执", false
}

func ChatroomDeliveryCompletionIssueFromResults(input string, results []string) (string, bool) {
	satisfied, attempted := ChatroomDeliveryStatusFromResults(results)
	return ChatroomDeliveryCompletionIssue(ChatroomDeliveryRequestedFromInput(input), satisfied, attempted)
}

func BuildChatroomDeliveryBudgetPrompt(presentationIssues []string) map[string]interface{} {
	presentationInstruction := ""
	if len(presentationIssues) > 0 {
		presentationInstruction = "\nBefore publishing, close these PPT checks first:\n- " + strings.Join(presentationIssues, "\n- ") + "\n"
	}
	return agentloop.NewInternalControlMessage("CHATROOM DELIVERY RESERVE", `[CHATROOM DELIVERY BUDGET RESERVATION]
The user explicitly requested delivery to Chatroom. Do not spend the remaining calls on optional polishing or repeated diagnostics.`+presentationInstruction+`
After all local deliverables and required QA pass, use the builtin nova-community skill and its code_check-based topics -> prepare -> publish workflow. Re-read the frozen final payload before prepare. Execute the live publish at most once with this task's stable intent ID, preserve OPENKITTY_ACTION_RESULT (result ID, share URL, and content SHA-256), and only then finish. Do not merely say that publishing is the next step.`)
}

func BuildChatroomDeliveryRepairPrompt() map[string]interface{} {
	return agentloop.NewInternalControlMessage("CHATROOM DELIVERY REQUIRED", `[CHATROOM DELIVERY CONTRACT]
The local deliverables are ready, but the user explicitly required delivery to Chatroom and there is no successful current-turn action receipt. Use use_skill with skill_name="nova-community" if needed. Follow its code_check-based topics -> prepare -> publish workflow, re-read the frozen payload before prepare, and make the live publish exactly once with this task's stable intent ID. Do not narrate or return a final answer before OPENKITTY_ACTION_RESULT confirms the result ID and content SHA-256.`)
}

func BuildChatroomFinalToolWindowPrompt() map[string]interface{} {
	return agentloop.NewInternalControlMessage("CHATROOM FINAL TOOL WINDOW", `[FINAL CHATROOM TOOL WINDOW]
This is the last planned tool-capable model call. The user explicitly authorized one Chatroom delivery, local production checks are complete, and no publish attempt has occurred. Execute only the remaining nova-community workflow now; if the prepared payload already exists, publish it exactly once with this task's stable intent ID. Do not return narration. A missing or uncertain receipt will fail closed and must not be retried.`)
}

func AppendChatroomDeliveryFailure(output, issue string) string {
	output = strings.TrimSpace(output)
	message := fmt.Sprintf("Chatroom 交付未完成：%s。", strings.TrimSpace(issue))
	if output == "" {
		return message
	}
	if strings.Contains(output, "Chatroom 交付未完成：") {
		return output
	}
	return output + "\n\n" + message
}
