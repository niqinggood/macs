package app

import (
	"context"
	"strings"
	"testing"
)

func coverageResult(rawURL, content string) string {
	return toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": rawURL}}, content, true)
}

func TestExpectedResearchDatesAndEvidenceCoverage(t *testing.T) {
	expected := expectedResearchDates("从今天（2026-07-11）起查询未来7天天气")
	if len(expected) != 7 || expected[0].monthDay != "07-11" || expected[6].monthDay != "07-17" {
		t.Fatalf("expected dates = %#v", expected)
	}
	keys := evidenceDateKeysForContent("11日 12日 07/13 7月14日 2026-07-15 16日")
	if got := countExpectedDateCoverage(expected, keys); got != 6 {
		t.Fatalf("coverage = %d, want 6; keys=%v", got, keys)
	}
}

func TestTimeSeriesRetrievalFitMarksPartialWithoutExtraModelCall(t *testing.T) {
	ctx := withWebFetchRetrievalGoal(context.Background(), "从2026-07-11起查询未来7天天气")
	partial := annotateTimeSeriesRetrievalFit(ctx, "2026-07-11 当前天气，未来预报未呈现")
	if !strings.HasPrefix(partial, "[RETRIEVAL_STATUS: PARTIAL date_coverage=0/7]") {
		t.Fatalf("partial annotation = %q", partial)
	}
	complete := annotateTimeSeriesRetrievalFit(ctx, strings.Join([]string{
		"07/11 rain 32℃/27℃", "07/12 rain 31℃/27℃", "07/13 cloudy 32℃/27℃",
		"07/14 cloudy 35℃/27℃", "07/15 rain 35℃/28℃", "07/16 rain 32℃/27℃", "07/17 rain 30℃/26℃",
	}, "\n"))
	if !strings.HasPrefix(complete, "[RETRIEVAL_STATUS: COMPLETE date_coverage=7/7]") {
		t.Fatalf("complete annotation = %q", complete)
	}
}

func TestDailyCoverageRejectsHourlyRowsAndMissingDailyFields(t *testing.T) {
	content := "07/16 周四 中雨 32℃ 27℃\n17日02:00 2.3mm 29.1℃\n05:00 2.3mm 28.4℃\n07/17 周五 数据缺失 —"
	keys := evidenceDailyDateKeysForContent(content)
	expected := expectedResearchDates("从2026-07-16起查询未来2天天气")
	if got := countExpectedDateCoverage(expected, keys); got != 1 {
		t.Fatalf("hourly/missing row counted as daily coverage: got=%d keys=%v", got, keys)
	}
}

func TestResearchCoverageRequiresSubstantialOverlapAndOneFullSource(t *testing.T) {
	input := "从今天（2026-07-11）起查询未来7天天气，至少核对两个独立来源"
	results := []string{
		coverageResult("https://source-a.example/weather", "11日 32℃/27℃\n12日 31℃/27℃\n13日 32℃/27℃\n14日 35℃/27℃\n15日 35℃/28℃\n16日 32℃/27℃"),
		coverageResult("https://source-b.example/weather", "2026-07-11 当前天气和气候概述"),
	}
	issues := strings.Join(researchCoverageIssues(input, results), " | ")
	if !strings.Contains(issues, "only 1") || !strings.Contains(issues, "no direct source covers all 7") {
		t.Fatalf("coverage issues = %s", issues)
	}

	results = append(results, coverageResult("https://source-c.example/weather", "07/11 32℃/27℃\n07/12 31℃/27℃\n07/13 32℃/27℃\n07/14 35℃/27℃\n07/15 35℃/28℃\n07/16 32℃/27℃\n07/17 30℃/26℃"))
	if issues := researchCoverageIssues(input, results); len(issues) != 0 {
		t.Fatalf("substantial+full coverage should pass: %v", issues)
	}
}

func TestResearchCoverageGroupsSharedCMAProvenance(t *testing.T) {
	input := "从2026-07-11起查询未来3天天气，至少核对两个独立来源"
	rows := "07/11 32℃/27℃\n07/12 31℃/27℃\n07/13 32℃/27℃"
	results := []string{
		coverageResult("https://www.weather.com.cn/weather/101020100.shtml", rows),
		coverageResult("https://www.nmc.cn/publish/forecast/ASH/shanghai.html", rows),
	}
	issues := strings.Join(researchCoverageIssues(input, results), " | ")
	if !strings.Contains(issues, "only 1") {
		t.Fatalf("shared CMA provenance counted as independent: %s", issues)
	}
}

func TestResearchCoverageDoesNotApplyToNonWindowResearch(t *testing.T) {
	if issues := researchCoverageIssues("至少核对两个来源，分析今天油价", nil); len(issues) != 0 {
		t.Fatalf("non-window request received date coverage issues: %v", issues)
	}
}
