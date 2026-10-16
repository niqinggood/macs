package security

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"mac/internal/toolcontract"
)

var (
	// Stop at common Chinese prose/list punctuation as well as Markdown
	// delimiters. Otherwise adjacent links separated by "、" become one URL.
	externalURLPattern           = regexp.MustCompile("https?://[^\\s\\(\\)\\[\\]\\{\\}<>\\\"'`、，。；：（）【】《》]+")
	hazardAlertPattern           = regexp.MustCompile(`(?:台风|暴雨|雷电|大风|高温|洪水|强对流|寒潮|道路结冰|地质灾害)[^，。；;：:\n]{0,10}(?:预警|警报)`)
	namedStormPattern            = regexp.MustCompile(`(?:台风|飓风|热带风暴)\s*[“"']?([\p{Han}A-Za-z0-9_-]{2,20})`)
	contactNumberPattern         = regexp.MustCompile(`(?:电话|热线|拨打|致电)[^0-9\n]{0,10}([0-9][0-9 -]{4,}[0-9])`)
	subDailyWeatherTimingPattern = regexp.MustCompile(`(?:(?:降雨|下雨|阵雨|雷雨|降水|雨势|风力|气温)[^，。；;\n]{0,24}(?:周[一二三四五六日天](?:早|上午|下午|晚|夜间|全天)|清晨|早晨|上午|中午|下午|傍晚|晚间|晚上|夜间|全天)|(?:周[一二三四五六日天](?:早|上午|下午|晚|夜间|全天)|清晨|早晨|上午|中午|下午|傍晚|晚间|晚上|夜间|全天)[^，。；;\n]{0,24}(?:降雨|下雨|阵雨|雷雨|降水|雨势|风力|气温))`)
	weatherModelCausalPattern    = regexp.MustCompile(`(?i)(?:(?:ECMWF|GFS|Open-Meteo|7Timer|气象模型)[^，。；;\n]{0,60}(?:因为|由于|导致|造成|压制|推高|拉低|源于)|(?:因为|由于)[^，。；;\n]{0,60}(?:ECMWF|GFS|Open-Meteo|7Timer|气象模型))`)
)

var genericStormNameCaptures = map[string]bool{
	"背景": true, "影响": true, "路径": true, "预警": true, "警报": true,
	"天气": true, "外围": true, "环流": true, "动态": true, "消息": true,
	"风险": true, "来袭": true, "过境": true, "期间": true, "中心": true,
	"等级": true, "编号": true, "登陆": true, "登录": true,
	// “平台风格/舞台风格” contains “台风格” but does not name a cyclone.
	"格": true,
}

var highRiskWeatherMarkers = []string{
	"外围环流", "强对流", "小时雨强", "单小时降水", "每小时降水", "mm/h", "毫米/小时",
	"雷暴", "雷雨", "alert issued", "warning issued", "weather advisory",
}

// ExternalURLPattern returns the shared URL recognizer used by root-level
// evidence adapters. regexp.Regexp is safe for concurrent matching.
func ExternalURLPattern() *regexp.Regexp { return externalURLPattern }

func supportedNamedStormCapture(name string) bool {
	name = strings.TrimSpace(strings.Trim(name, `“”"'`))
	if name == "" || genericStormNameCaptures[name] {
		return false
	}
	for generic := range genericStormNameCaptures {
		if strings.HasPrefix(name, generic) {
			return false
		}
	}
	return true
}

// ExternalEvidenceHints extracts bounded, deterministic grounding hints from
// a successful external tool result.
func ExternalEvidenceHints(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	seen := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && len(seen) < 20 {
			seen[value] = true
		}
	}
	for _, rawURL := range externalURLPattern.FindAllString(content, -1) {
		add(strings.TrimRight(rawURL, ".,，。;；:`"))
	}
	for _, marker := range highRiskWeatherMarkers {
		if strings.Contains(strings.ToLower(content), strings.ToLower(marker)) {
			add(marker)
		}
	}
	for _, match := range hazardAlertPattern.FindAllString(content, -1) {
		add(match)
	}
	for _, match := range namedStormPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && supportedNamedStormCapture(match[1]) {
			add("named event: " + match[1])
			add(match[1])
		}
	}
	result := make([]string, 0, len(seen))
	for hint := range seen {
		result = append(result, hint)
	}
	sort.Strings(result)
	return result
}

// UnsupportedHighRiskClaims compares a draft with trusted external evidence.
// It intentionally checks only high-consequence, deterministic upgrades.
func UnsupportedHighRiskClaims(candidate, evidence string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || strings.TrimSpace(evidence) == "" {
		return nil
	}
	candidateLower := strings.ToLower(candidate)
	evidenceLower := strings.ToLower(evidence)
	issues := make(map[string]bool)

	for _, marker := range highRiskWeatherMarkers {
		if strings.Contains(candidateLower, strings.ToLower(marker)) && !strings.Contains(evidenceLower, strings.ToLower(marker)) {
			issues[marker] = true
		}
	}
	for _, match := range hazardAlertPattern.FindAllString(candidate, -1) {
		if !strings.Contains(evidence, match) {
			issues[match] = true
		}
	}
	for _, match := range namedStormPattern.FindAllStringSubmatch(candidate, -1) {
		if len(match) > 1 && supportedNamedStormCapture(match[1]) && !strings.Contains(evidence, match[1]) {
			issues["named event: "+match[1]] = true
		}
	}
	for _, match := range contactNumberPattern.FindAllStringSubmatch(candidate, -1) {
		if len(match) > 1 {
			number := strings.TrimSpace(match[1])
			if !strings.Contains(evidence, number) {
				issues["contact detail: "+number] = true
			}
		}
	}
	for _, match := range subDailyWeatherTimingPattern.FindAllString(candidate, -1) {
		if !strings.Contains(evidence, match) {
			issues["unsupported sub-daily weather timing: "+match] = true
		}
	}
	for _, match := range weatherModelCausalPattern.FindAllString(candidate, -1) {
		if !strings.Contains(evidence, match) {
			issues["unsupported model-causality explanation: "+match] = true
		}
	}
	for _, rawURL := range externalURLPattern.FindAllString(candidate, -1) {
		url := strings.TrimRight(rawURL, ".,，。;；:`")
		if !strings.Contains(evidence, url) {
			issues["uncited URL: "+url] = true
		}
	}

	result := make([]string, 0, len(issues))
	for issue := range issues {
		result = append(result, issue)
	}
	sort.Strings(result)
	return result
}

func IsExternalEvidenceToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_fetch", "web_search", "weather_forecast", "fuel_price", "stock_facts",
		"web_navigate", "web_page", "browser_task", "agent_reach", "social_trending",
		"platform_search", "nova_chatroom_search", "source_acquire":
		return true
	default:
		return false
	}
}

func IsExternalEvidenceSearchToolName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "web_fetch" || name == "web_search" || name == "agent_reach"
}

func ExternalEvidenceSearchMode(call toolcontract.Call) string {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "agent_reach" {
		action, _ := call.Arguments["action"].(string)
		switch strings.ToLower(strings.TrimSpace(action)) {
		case "search":
			return "discovery"
		case "read", "trending", "transcribe":
			return "direct"
		default:
			return ""
		}
	}
	if name == "weather_forecast" || name == "fuel_price" || name == "stock_facts" || name == "social_trending" {
		return "direct"
	}
	if name != "web_fetch" && name != "web_search" {
		return ""
	}
	if name == "web_search" {
		return "discovery"
	}
	url, _ := call.Arguments["url"].(string)
	engine, _ := call.Arguments["engine"].(string)
	if strings.TrimSpace(url) != "" && strings.TrimSpace(engine) == "" {
		return "direct"
	}
	return "discovery"
}

func IsExternalEvidenceToolCall(call toolcontract.Call) bool {
	if !IsExternalEvidenceToolName(call.Name) {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(call.Name), "agent_reach") || ExternalEvidenceSearchMode(call) != ""
}

func IsExternalEvidenceRecord(record toolcontract.Record) bool {
	if !IsExternalEvidenceToolName(record.Tool) {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(record.Tool), "agent_reach") || strings.TrimSpace(record.SearchMode) != ""
}

const externalEvidenceBoundary = "[EVIDENCE BOUNDARY] Final factual claims must be traceable to this turn's tool output. Omit or mark unknown any proper noun, date/time, number or probability, causal attribution, alert/advisory, or contact detail not explicitly present. Never fill missing fields with plausible values, and cite only URLs actually returned by tools. Search snippets are discovery, not proof of detailed fields: while required fields remain missing, use the latest observation to choose a materially different promising read-only source, engine, browser route, or professional Skill; stop when evidence is sufficient or the remaining gap is honest and material. Do not ask permission for read-only retrieval the user already requested."

func AppendExternalEvidenceBoundary(content string) string {
	content = strings.TrimSpace(content)
	if strings.Contains(content, externalEvidenceBoundary) {
		return content
	}
	if content == "" {
		return externalEvidenceBoundary
	}
	return content + "\n\n" + externalEvidenceBoundary
}

func EvidenceHintsForToolResult(call toolcontract.Call, content string) []string {
	if !IsExternalEvidenceToolCall(call) {
		return nil
	}
	return ExternalEvidenceHints(content)
}

func UnsupportedHighRiskClaimsForResults(candidate string, toolResults []string) []string {
	var evidence strings.Builder
	for _, result := range toolResults {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || !IsExternalEvidenceRecord(record) {
			continue
		}
		evidence.WriteString(result)
		evidence.WriteByte('\n')
	}
	return UnsupportedHighRiskClaims(candidate, evidence.String())
}

func BuildEvidenceRepairPrompt(issues []string) map[string]interface{} {
	return map[string]interface{}{
		"role": "system",
		"content": fmt.Sprintf(`[EVIDENCE REPAIR REQUIRED]
Your draft contains high-risk externally verifiable wording that is not explicit in this turn's source evidence: %s.

Return a revised full final answer now, without calling tools. Remove the unsupported wording or downgrade it to a clearly labeled inference/unknown. Do not upgrade forecast values into an official warning, convert interval samples into a different unit, infer sub-daily timing from daily rows, explain why forecast models differ without explicit evidence, or add named events, probabilities, contact details, or URLs absent from the evidence. Preserve supported facts, source links, coverage gaps, and useful recommendations. Keep it compact and output only the revised user-facing answer.`, strings.Join(issues, "; ")),
	}
}
