package app

import (
	"testing"

	"mac/internal/textnormalize"
)

func TestNormalizeUserFacingCalendarLabels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ISO after", input: "最近交易日 2026-08-07 周四", want: "最近交易日 2026-08-07 周五"},
		{name: "ISO parenthesized", input: "2026-08-08（星期五）休市", want: "2026-08-08（星期六）休市"},
		{name: "weekday before ISO", input: "下周一（2026-08-11）观察", want: "下周二（2026-08-11）观察"},
		{name: "Chinese date", input: "2026年8月9日，周六", want: "2026年8月9日，周日"},
		{name: "correct label", input: "2026-08-10 周一", want: "2026-08-10 周一"},
		{name: "invalid date", input: "2026-02-30 周一", want: "2026-02-30 周一"},
		{name: "partial date stays unchanged", input: "8月7日周四", want: "8月7日周四"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := textnormalize.CalendarLabels(tt.input); got != tt.want {
				t.Fatalf("normalizeUserFacingCalendarLabels(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUserFacingCleanersNormalizeCalendarLabels(t *testing.T) {
	input := "# 行情\n2026-08-07（周四）收盘。"
	want := "# 行情\n2026-08-07（周五）收盘。"
	if got := cleanUserFacingFinalAnswer(input); got != want {
		t.Fatalf("cleanUserFacingFinalAnswer = %q, want %q", got, want)
	}
	if got := cleanPipelineUserOutput(input); got != want {
		t.Fatalf("cleanPipelineUserOutput = %q, want %q", got, want)
	}
}
