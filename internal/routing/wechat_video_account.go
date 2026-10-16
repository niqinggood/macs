package routing

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mac/internal/toolcontract"
)

var wechatVideoAccountURLPattern = regexp.MustCompile(`(?i)https?://weixin\.qq\.com/sph/[a-z0-9_-]+`)

func IsWechatVideoAccountExtractionRequest(input string) bool {
	return wechatVideoAccountURLPattern.MatchString(strings.TrimSpace(input))
}

func WechatVideoAccountSourceContractPrompt() string {
	return `## WeChat Video Account Media Contract
The user supplied a public https://weixin.qq.com/sph/... media link. Call use_skill with skill_name="wx-video-account-notes" before using generic web_fetch. Follow that skill's runtime pipeline to resolve and download the actual media, then read note_materials.json (and asr.txt/ocr.txt when needed) before answering. Page caption, tags, author, or date alone do not prove the video's spoken or visual content. If the resolver or OCR/ASR fails, report partial completion and request the uploaded media; never mark caption-only extraction as full success or speculate about unseen content.`
}

func WechatVideoAccountMediaEvidenceReady(results []string) bool {
	for _, result := range results {
		lower := strings.ToLower(result)
		if strings.Contains(lower, "note_materials.json") &&
			strings.Contains(lower, "asr_lines") &&
			strings.Contains(lower, "ocr_lines") {
			return true
		}
	}
	return false
}

func WechatVideoAccountMediaAttempted(results []string) bool {
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(record.Tool)) {
		case "cli", "ptc_execute", "persistent_shell", "read_file":
			lower := strings.ToLower(result)
			if strings.Contains(lower, "wx-video-account-notes") ||
				strings.Contains(lower, "note_materials.json") ||
				strings.Contains(lower, "fetch_video_profile") ||
				strings.Contains(lower, "[pipeline]") {
				return true
			}
		}
	}
	return false
}

func ToolArtifactPath(call toolcontract.Call) string {
	var keys []string
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "read_file", "write_file":
		keys = []string{"path", "file_path"}
	case "edit_file":
		keys = []string{"file_path", "path"}
	case "convert":
		keys = []string{"output"}
	default:
		return ""
	}
	for _, key := range keys {
		if value, ok := call.Arguments[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func BuildWechatVideoAccountSynthesisMessages(input, evidence, materialPath string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"role": "system",
			"content": `[WECHAT VIDEO MATERIALS CLOSED]
The actual WeChat Video Account media pipeline has finished. Answer the user's request now using only the extracted material below. Do not call tools, inspect directories, read files, run code, plan, or narrate the workflow.

Treat metadata/caption, OCR frames, and ASR speech as different evidence types. Summarize the video's actual spoken content, not merely its page caption. Correct only obvious ASR homophone/punctuation errors when context makes the correction safe; mark uncertain names, numbers, or claims as uncertain instead of inventing them. Mention that machine transcription can contain errors, and give the saved material path so the user can inspect the full extraction. Return one complete, useful answer in the user's language.`,
		},
		{"role": "user", "content": strings.TrimSpace(input)},
		{
			"role":    "system",
			"content": fmt.Sprintf("Actual extracted material path: %s\n\nActual extracted material:\n%s", materialPath, evidence),
		},
	}
}

func BuildWechatVideoAccountSynthesisMessagesFromResults(input string, results []string) []map[string]interface{} {
	evidence, materialPath := WechatVideoAccountMaterialEvidence(results)
	return BuildWechatVideoAccountSynthesisMessages(input, evidence, materialPath)
}

func WechatVideoAccountMaterialEvidence(results []string) (string, string) {
	for i := len(results) - 1; i >= 0; i-- {
		record, ok := toolcontract.ParseRecord(results[i])
		if !ok || !record.Succeeded || !strings.EqualFold(record.Tool, "read_file") {
			continue
		}
		path := strings.Trim(strings.TrimSpace(record.ArtifactPath), `"`)
		if !strings.EqualFold(filepath.Base(path), "note_materials.json") {
			continue
		}
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return CompactWechatVideoMaterial(string(raw)), path
		}
		content := results[i]
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
		return CompactWechatVideoMaterial(content), path
	}

	for i := len(results) - 1; i >= 0; i-- {
		if WechatVideoAccountMediaEvidenceReady([]string{results[i]}) {
			content := results[i]
			if idx := strings.Index(content, "\n"); idx >= 0 {
				content = content[idx+1:]
			}
			return CompactWechatVideoMaterial(content), "note_materials.json"
		}
	}
	return "No readable material packet was retained.", "note_materials.json"
}

func CompactWechatVideoMaterial(raw string) string {
	runes := []rune(strings.TrimSpace(raw))
	const maxRunes = 60000
	if len(runes) <= maxRunes {
		return string(runes)
	}
	const edgeRunes = 25000
	const middleRunes = 10000
	middleStart := len(runes)/2 - middleRunes/2
	return string(runes[:edgeRunes]) +
		"\n\n[... middle transcript excerpt selected by host ...]\n\n" +
		string(runes[middleStart:middleStart+middleRunes]) +
		"\n\n[... final transcript excerpt selected by host ...]\n\n" +
		string(runes[len(runes)-edgeRunes:])
}

func WechatVideoAccountSynthesisFallback(materialPath string) string {
	return fmt.Sprintf("视频媒体已实际下载并完成 OCR/ASR 提取，完整素材保存在 %s。最终整理模型没有返回可用正文，因此系统已停止继续调用工具，避免无意义循环。", materialPath)
}

func WechatVideoAccountSynthesisFallbackFromResults(results []string) string {
	_, path := WechatVideoAccountMaterialEvidence(results)
	return WechatVideoAccountSynthesisFallback(path)
}
