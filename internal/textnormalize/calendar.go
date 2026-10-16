// Package textnormalize performs deterministic corrections on user-visible text.
package textnormalize

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	fullCalendarDatePattern  = regexp.MustCompile(`(?:20[0-9]{2}[-/.][0-9]{1,2}[-/.][0-9]{1,2}|20[0-9]{2}年[0-9]{1,2}月[0-9]{1,2}日)`)
	calendarDatePartsPattern = regexp.MustCompile(`^(20[0-9]{2})(?:[-/.]([0-9]{1,2})[-/.]([0-9]{1,2})|年([0-9]{1,2})月([0-9]{1,2})日)$`)
	chineseWeekdayPattern    = regexp.MustCompile(`(?:星期|周)[一二三四五六日天]`)
	dateThenWeekdayPattern   = regexp.MustCompile(`(?:20[0-9]{2}[-/.][0-9]{1,2}[-/.][0-9]{1,2}|20[0-9]{2}年[0-9]{1,2}月[0-9]{1,2}日)[ \t]*[，,]?[ \t]*[（(]?[ \t]*(?:星期|周)[一二三四五六日天]`)
	weekdayThenDatePattern   = regexp.MustCompile(`(?:星期|周)[一二三四五六日天][ \t]*[（(]?[ \t]*(?:20[0-9]{2}[-/.][0-9]{1,2}[-/.][0-9]{1,2}|20[0-9]{2}年[0-9]{1,2}月[0-9]{1,2}日)`)
)

// CalendarLabels deterministically repairs an objective
// calendar inconsistency without changing the date itself. Models are prone to
// guessing weekday labels near month boundaries; a user-facing answer should
// never say, for example, that 2026-08-07 was a Thursday.
func CalendarLabels(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	text = dateThenWeekdayPattern.ReplaceAllStringFunc(text, normalizeCalendarMatch)
	return weekdayThenDatePattern.ReplaceAllStringFunc(text, normalizeCalendarMatch)
}

func normalizeCalendarMatch(match string) string {
	dateText := fullCalendarDatePattern.FindString(match)
	weekdayBounds := chineseWeekdayPattern.FindStringIndex(match)
	if dateText == "" || weekdayBounds == nil {
		return match
	}
	date, ok := parseFullCalendarDate(dateText)
	if !ok {
		return match
	}
	weekdayText := match[weekdayBounds[0]:weekdayBounds[1]]
	prefix := "周"
	if strings.HasPrefix(weekdayText, "星期") {
		prefix = "星期"
	}
	want := prefix + []string{"日", "一", "二", "三", "四", "五", "六"}[int(date.Weekday())]
	if weekdayText == want {
		return match
	}
	return match[:weekdayBounds[0]] + want + match[weekdayBounds[1]:]
}

func parseFullCalendarDate(raw string) (time.Time, bool) {
	parts := calendarDatePartsPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(parts) != 6 {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	monthText, dayText := parts[2], parts[3]
	if monthText == "" {
		monthText, dayText = parts[4], parts[5]
	}
	month, monthErr := strconv.Atoi(monthText)
	day, dayErr := strconv.Atoi(dayText)
	if monthErr != nil || dayErr != nil {
		return time.Time{}, false
	}
	parsed := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day {
		return time.Time{}, false
	}
	return parsed, true
}
