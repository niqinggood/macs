package permissionpolicy

import (
	"regexp"
	"strings"
)

const ArtifactPolicyExplicitRequest = "explicit-request"

var (
	aiEmployeeOutputActionRe               = regexp.MustCompile(`(?i)(?:另存为|保存(?:为|到)?|存为|写入|落盘|导出|下载|创建|新建|生成|制作|产出|交付|输出|编辑|修改|覆盖|追加|save(?:\s+as)?|write|export|download|create|generate|produce|deliver|output|edit|modify)`)
	aiEmployeeArtifactNounRe               = regexp.MustCompile(`(?i)(?:文件|文档|markdown|\.?md\b|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv|幻灯片|演示文稿|电子表格|可下载(?:版|文件)?|downloadable\s+(?:file|document|version))`)
	aiEmployeeDirectArtifactRe             = regexp.MustCompile(`(?i)(?:(?:给我|提供|交付|需要|我要|想要|请给|请提供|please\s+(?:give|provide|deliver)|i\s+(?:need|want))[^。；;!?！？\n]{0,18}(?:markdown|\.?md\b|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv|文件|文档|幻灯片|演示文稿|电子表格))`)
	aiEmployeeArtifactFormatRe             = regexp.MustCompile(`(?i)(?:(?:以|按|采用|输出为|保存为|存为)[^。；;!?！？\n]{0,12}(?:markdown|\.?md\b|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv|文件|文档|幻灯片|演示文稿|电子表格)(?:\s*格式)?|(?:markdown|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv)\s*(?:格式|文件|文档))`)
	aiEmployeeArtifactObjectBeforeActionRe = regexp.MustCompile(`(?i)(?:把|将|在)[^。；;!?！？\n]{0,8}(?:文件|文档|markdown|\.?md\b|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv|幻灯片|演示文稿|电子表格)[^。；;!?！？\n]{0,12}(?:保存|写入|导出|创建|生成|制作|输出|编辑|修改|覆盖|追加|save|write|export|create|generate|edit|modify)`)
	aiEmployeeArtifactBeforeActionRe       = regexp.MustCompile(`(?i)(?:文件|文档|markdown|\.?md\b|pptx?|powerpoint|pdf|docx?|word|xlsx?|excel|csv|幻灯片|演示文稿|电子表格)[^。；;!?！？\n]{0,28}(?:保存|写入|落盘|导出|下载|创建|生成|制作|输出|覆盖|追加|save|write|export|download|create|generate)`)
)

// explicitAIEmployeeArtifactRequest recognizes an output-artifact instruction,
// not the mere presence of a business-document noun or an input attachment.
// The classifier is intentionally domain-neutral: "write a BP/report/plan" is
// a request for content, while a requested file format, save/export operation,
// or mutation of a named file authorizes a filesystem deliverable.
func ExplicitAIEmployeeArtifactRequest(input string) bool {
	text := strings.TrimSpace(input)
	if text == "" {
		return false
	}
	for _, clause := range splitAIEmployeeOutputIntentClauses(text) {
		lower := strings.ToLower(strings.TrimSpace(clause))
		if lower == "" {
			continue
		}
		if aiEmployeeDirectArtifactRe.MatchString(lower) ||
			aiEmployeeArtifactFormatRe.MatchString(lower) ||
			aiEmployeeArtifactObjectBeforeActionRe.MatchString(lower) ||
			aiEmployeeArtifactBeforeActionRe.MatchString(lower) {
			if !aiEmployeeOutputClauseIsNegated(lower) {
				return true
			}
		}
		actionMatches := aiEmployeeOutputActionRe.FindAllStringIndex(lower, -1)
		for _, action := range actionMatches {
			if aiEmployeeOutputActionIsNegated(lower, action[0]) {
				continue
			}
			after := []rune(lower[action[1]:])
			if len(after) > 28 {
				after = after[:28]
			}
			if aiEmployeeArtifactNounRe.MatchString(string(after)) {
				return true
			}
		}
	}
	return false
}

func splitAIEmployeeOutputIntentClauses(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '；', ';', '！', '!', '？', '?':
			return true
		default:
			return false
		}
	})
}

func aiEmployeeOutputClauseIsNegated(clause string) bool {
	action := aiEmployeeOutputActionRe.FindStringIndex(clause)
	return action != nil && aiEmployeeOutputActionIsNegated(clause, action[0])
}

func aiEmployeeOutputActionIsNegated(clause string, actionByteIndex int) bool {
	if actionByteIndex < 0 {
		return false
	}
	prefixText := clause[:actionByteIndex]
	if separator := strings.LastIndexAny(prefixText, "，,：:"); separator >= 0 {
		prefixText = prefixText[separator+1:]
	}
	prefixRunes := []rune(prefixText)
	if len(prefixRunes) > 12 {
		prefixRunes = prefixRunes[len(prefixRunes)-12:]
	}
	prefix := strings.ToLower(string(prefixRunes))
	return containsAnyText(prefix, []string{
		"不要", "不得", "不能", "禁止", "无需", "不必", "别", "勿",
		"do not", "don't", "must not", "never", "without",
	})
}

func containsAnyText(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
