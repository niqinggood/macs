package toolcontract

import (
	"encoding/json"
	"regexp"
	"strings"
)

const ActionResultPrefix = "OPENKITTY_ACTION_RESULT="

type ActionReceipt struct {
	OK            bool            `json:"ok"`
	Action        string          `json:"action"`
	ResultID      json.RawMessage `json:"result_id"`
	TopicID       json.RawMessage `json:"topic_id"`
	ShareURL      string          `json:"share_url"`
	ContentSHA256 string          `json:"content_sha256"`
	RobotID       string          `json:"robot_id"`
}

var (
	actionReceiptNumericIDPattern = regexp.MustCompile(`^\d+$`)
	legacyActionPattern           = regexp.MustCompile(`(?mi)^\s*(Posted|Replied)!\s+ID:\s*(\d+)\s*$`)
	legacyActionSuccessPattern    = regexp.MustCompile(`(?mi)^\s*RC:\s*0\s*$`)
)

func ParseActionReceipt(text string) (ActionReceipt, string, string, bool) {
	var lastReceipt ActionReceipt
	var lastResultID, lastTopicID string
	found := false
	for _, line := range strings.Split(text, "\n") {
		idx := strings.Index(line, ActionResultPrefix)
		if idx < 0 {
			continue
		}
		raw := strings.TrimSpace(line[idx+len(ActionResultPrefix):])
		var receipt ActionReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil || !receipt.OK {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(receipt.Action))
		if action != "post" && action != "reply" && action != "comment" && action != "message" {
			continue
		}
		resultID := actionReceiptNumericID(receipt.ResultID)
		if resultID == "" {
			continue
		}
		if receipt.ContentSHA256 != "" && !regexp.MustCompile(`(?i)^[a-f0-9]{64}$`).MatchString(receipt.ContentSHA256) {
			continue
		}
		lastReceipt = receipt
		lastResultID = resultID
		lastTopicID = actionReceiptNumericID(receipt.TopicID)
		found = true
	}
	return lastReceipt, lastResultID, lastTopicID, found
}

func actionReceiptNumericID(raw json.RawMessage) string {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if actionReceiptNumericIDPattern.MatchString(value) {
		return value
	}
	return ""
}

func ParseLegacySuccessfulAction(text string) (action, resultID string, ok bool) {
	match := legacyActionPattern.FindStringSubmatch(text)
	if len(match) < 3 || !legacyActionSuccessPattern.MatchString(text) {
		return "", "", false
	}
	if strings.EqualFold(match[1], "Replied") {
		return "reply", match[2], true
	}
	return "post", match[2], true
}
