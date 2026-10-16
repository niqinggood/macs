package app

import (
	"strings"
	"testing"
)

func evidenceGuardWebResult(content string) string {
	return toolResultForFinalization(ToolCall{
		Name:      "web_fetch",
		Arguments: map[string]interface{}{"url": "https://weather.example/shanghai"},
	}, content, true)
}

func TestEvidenceGuardFlagsUnsupportedHazardUpgradeAndURL(t *testing.T) {
	results := []string{evidenceGuardWebResult("Source: https://weather.example/shanghai\n07/16 14:00 precipitation 12mm")}
	candidate := "已发布强对流预警，14时小时雨强12mm。来源：https://other.example/forecast"
	issues := strings.Join(unsupportedHighRiskEvidenceClaims(candidate, results), " | ")
	for _, want := range []string{"强对流", "小时雨强", "uncited URL: https://other.example/forecast"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("issues missing %q: %s", want, issues)
		}
	}
}

func TestEvidenceGuardFlagsUnsupportedHourlyUnitConversion(t *testing.T) {
	results := []string{evidenceGuardWebResult("07/16 14:00 precipitation 12mm (three-hour interval)")}
	issues := strings.Join(unsupportedHighRiskEvidenceClaims("14时单小时降水12mm，即12mm/h。", results), " | ")
	for _, want := range []string{"单小时降水", "mm/h"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("issues missing %q: %s", want, issues)
		}
	}
}

func TestEvidenceGuardAllowsSupportedAlertAndReturnedURL(t *testing.T) {
	results := []string{evidenceGuardWebResult("Source: https://weather.example/shanghai\n" + strings.Repeat("context ", 400) + "上海气象台发布暴雨预警")}
	candidate := "上海气象台发布暴雨预警。来源：https://weather.example/shanghai"
	if issues := unsupportedHighRiskEvidenceClaims(candidate, results); len(issues) != 0 {
		t.Fatalf("supported claim was flagged: %v", issues)
	}
}

func TestEvidenceGuardAllowsURLReturnedByBrowserNavigation(t *testing.T) {
	result := toolResultForFinalization(
		ToolCall{
			Name:      "web_navigate",
			Arguments: map[string]interface{}{"url": "https://www.sicurrent.com/openkitty/"},
		},
		"Opened tab ABC123\nTitle: OpenKitty\nURL: https://www.sicurrent.com/openkitty/",
		true,
	)
	candidate := "Browser verification succeeded: https://www.sicurrent.com/openkitty/"
	if issues := unsupportedHighRiskEvidenceClaims(candidate, []string{result}); len(issues) != 0 {
		t.Fatalf("browser-returned URL was rejected as uncited: %v", issues)
	}
	apiMessage := BuildToolResultAPIMessage(
		ToolResult{Name: "web_navigate", Content: "URL: https://www.sicurrent.com/openkitty/"},
		true,
	)
	apiContent, _ := apiMessage["content"].(string)
	if !strings.Contains(apiContent, externalEvidenceBoundary) {
		t.Fatal("browser navigation result is missing the external evidence boundary")
	}
}

func TestEvidenceGuardFlagsNamedEventAndUnverifiedContact(t *testing.T) {
	results := []string{evidenceGuardWebResult("Source: https://weather.example/shanghai\n本周有阵雨")}
	candidate := "受台风“示例风暴”外围环流影响，请拨打123456咨询。"
	issues := strings.Join(unsupportedHighRiskEvidenceClaims(candidate, results), " | ")
	for _, want := range []string{"外围环流", "named event: 示例风暴", "contact detail: 123456"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("issues missing %q: %s", want, issues)
		}
	}
}

func TestEvidenceGuardDoesNotTreatStormContextLabelAsNamedEvent(t *testing.T) {
	results := []string{evidenceGuardWebResult("Source: https://weather.example/shanghai\n本周有阵雨")}
	issues := strings.Join(unsupportedHighRiskEvidenceClaims("台风背景仍不明确，建议关注后续天气变化。", results), " | ")
	if strings.Contains(issues, "named event: 背景") {
		t.Fatalf("generic storm context label was treated as a named event: %s", issues)
	}

	issues = strings.Join(unsupportedHighRiskEvidenceClaims(`受台风"巴威"影响。`, results), " | ")
	if !strings.Contains(issues, "named event: 巴威") {
		t.Fatalf("actual quoted storm name was not checked: %s", issues)
	}
}

func TestEvidenceGuardDoesNotParsePlatformStyleAsTyphoon(t *testing.T) {
	results := []string{evidenceGuardWebResult("Source: https://example.com/product\n产品工作流说明")}
	candidate := "系统支持多平台风格适配内容，并按不同渠道输出素材。"
	if issues := unsupportedHighRiskEvidenceClaims(candidate, results); len(issues) != 0 {
		t.Fatalf("platform-style wording was treated as a named tropical event: %v", issues)
	}
}

func TestEvidenceGuardRejectsSubDailyTimingAbsentFromDailyEvidence(t *testing.T) {
	results := []string{evidenceGuardWebResult("2026-07-11 雨 31℃/26℃\n2026-07-12 雨 30℃/25℃\nTemporal granularity: daily aggregate only")}
	issues := strings.Join(unsupportedHighRiskEvidenceClaims("降雨最强窗口为周六晚至周日全天。", results), " | ")
	if !strings.Contains(issues, "unsupported sub-daily weather timing") {
		t.Fatalf("unsupported daypart claim was not rejected: %s", issues)
	}

	results = []string{evidenceGuardWebResult("逐小时预报明确写明：周六晚有降雨。")}
	if issues := unsupportedHighRiskEvidenceClaims("周六晚有降雨。", results); len(issues) != 0 {
		t.Fatalf("supported daypart claim was rejected: %v", issues)
	}
}

func TestEvidenceGuardRejectsUnsupportedWeatherModelCausality(t *testing.T) {
	results := []string{evidenceGuardWebResult("ECMWF 最高温 38℃；GFS 最高温 31℃。两源存在分歧。")}
	issues := strings.Join(unsupportedHighRiskEvidenceClaims("GFS 用更厚云层和降水压制了最高气温。", results), " | ")
	if !strings.Contains(issues, "unsupported model-causality explanation") {
		t.Fatalf("unsupported model causality was not rejected: %s", issues)
	}

	results = []string{evidenceGuardWebResult("GFS 因为更厚云层而压制了最高气温。")}
	if issues := unsupportedHighRiskEvidenceClaims("GFS 因为更厚云层而压制了最高气温。", results); len(issues) != 0 {
		t.Fatalf("explicitly supported model causality was rejected: %v", issues)
	}
}

func TestEvidenceRepairPromptRequiresNoToolFullRevision(t *testing.T) {
	prompt := buildEvidenceRepairPrompt([]string{"强对流", "小时雨强"})
	content, _ := prompt["content"].(string)
	for _, want := range []string{"EVIDENCE REPAIR REQUIRED", "without calling tools", "revised full final answer", "convert interval samples", "sub-daily timing", "forecast models differ", "Keep it compact"} {
		if !strings.Contains(content, want) {
			t.Fatalf("repair prompt missing %q: %s", want, content)
		}
	}
}

func TestUnsupportedHighRiskEvidenceClaimsSplitsChineseDelimitedURLs(t *testing.T) {
	results := []string{
		"OPENKITTY_TOOL_RESULT_V1 {\"tool\":\"web_fetch\",\"succeeded\":true}\nhttps://a.example/product\nhttps://b.example/pricing",
	}
	candidate := "来源：https://a.example/product、https://b.example/pricing。"
	if issues := unsupportedHighRiskEvidenceClaims(candidate, results); len(issues) != 0 {
		t.Fatalf("expected both Chinese-delimited URLs to match evidence, got %v", issues)
	}
}

func TestUnsupportedHighRiskEvidenceClaimsStopsAtChineseParenthesis(t *testing.T) {
	results := []string{
		"OPENKITTY_TOOL_RESULT_V1 {\"tool\":\"web_fetch\",\"succeeded\":true}\nhttps://a.example/product",
	}
	candidate := "https://a.example/product（已抓取）"
	if issues := unsupportedHighRiskEvidenceClaims(candidate, results); len(issues) != 0 {
		t.Fatalf("Chinese parenthetical annotation became part of URL: %v", issues)
	}
}
