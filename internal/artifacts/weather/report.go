package weather

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mac/internal/toolcontract"
	networktools "mac/internal/tools/network"
)

type parsedWeatherForecast struct {
	provider  string
	display   string
	location  string
	timeMeta  string
	sourceURL string
	rows      map[string]parsedWeatherRow
}

type parsedWeatherRow struct {
	date        string
	condition   string
	temperature string
	risk        string
	fields      string
	highC       float64
	lowC        float64
}

var (
	weatherCoverageLine = regexp.MustCompile(`^Coverage:\s*([0-9]+)\/([0-9]+)\s+requested daily rows\s*$`)
	isoDatePattern      = regexp.MustCompile(`\b(20[0-9]{2})-([01]?[0-9])-([0-3]?[0-9])\b`)
	futureDaysPattern   = regexp.MustCompile(`(?i)(?:未来|接下来|往后|future|next)\s*([0-9]{1,2})\s*(?:天|日|days?)`)
	weatherTemperature  = regexp.MustCompile(`^\s*(-?[0-9]+(?:\.[0-9]+)?)°C\s*\/\s*(-?[0-9]+(?:\.[0-9]+)?)°C\s*$`)
	weatherMobilityNeed = regexp.MustCompile(`(?i)(?:老人|老年|长辈|父母|膝关节|腿脚|行动不便|\b6[0-9]岁|\b7[0-9]岁|senior|elderly|limited\s+mobility)`)
)

func parseWeatherForecastToolResult(result string) (parsedWeatherForecast, bool) {
	record, ok := toolcontract.ParseRecord(result)
	if !ok || !record.Succeeded || !strings.EqualFold(strings.TrimSpace(record.Tool), "weather_forecast") {
		return parsedWeatherForecast{}, false
	}
	parsed := parsedWeatherForecast{rows: make(map[string]parsedWeatherRow)}
	coverageRows, coverageRequested := 0, 0
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "# Structured weather forecast — "):
			parsed.display = strings.TrimSpace(strings.TrimPrefix(line, "# Structured weather forecast — "))
		case strings.HasPrefix(line, "Provider key:"):
			parsed.provider = strings.TrimSpace(strings.TrimPrefix(line, "Provider key:"))
		case strings.HasPrefix(line, "Resolved location:"):
			parsed.location = strings.TrimSpace(strings.TrimPrefix(line, "Resolved location:"))
		case strings.HasPrefix(line, "Time metadata (preserve these labels exactly):"):
			parsed.timeMeta = strings.TrimSpace(strings.TrimPrefix(line, "Time metadata (preserve these labels exactly):"))
		case strings.HasPrefix(line, "Source:"):
			parsed.sourceURL = strings.TrimSpace(strings.TrimPrefix(line, "Source:"))
		case weatherCoverageLine.MatchString(line):
			match := weatherCoverageLine.FindStringSubmatch(line)
			coverageRows, _ = strconv.Atoi(match[1])
			coverageRequested, _ = strconv.Atoi(match[2])
		case strings.HasPrefix(line, "| 20"):
			parts := strings.Split(line, "|")
			if len(parts) < 7 {
				continue
			}
			date := strings.TrimSpace(parts[1])
			if _, err := time.Parse("2006-01-02", date); err != nil {
				continue
			}
			temperature := strings.TrimSpace(parts[3])
			match := weatherTemperature.FindStringSubmatch(temperature)
			if len(match) != 3 {
				continue
			}
			high, highErr := strconv.ParseFloat(match[1], 64)
			low, lowErr := strconv.ParseFloat(match[2], 64)
			if highErr != nil || lowErr != nil {
				continue
			}
			parsed.rows[date] = parsedWeatherRow{
				date:        date,
				condition:   strings.TrimSpace(parts[2]),
				temperature: temperature,
				risk:        strings.TrimSpace(parts[4]),
				fields:      strings.TrimSpace(parts[5]),
				highC:       high,
				lowC:        low,
			}
		}
	}
	if parsed.provider == "" || parsed.display == "" || parsed.location == "" || parsed.timeMeta == "" || parsed.sourceURL == "" {
		return parsedWeatherForecast{}, false
	}
	if parsed.provider != networktools.OpenMeteoProvider && parsed.provider != networktools.SevenTimerProvider {
		return parsedWeatherForecast{}, false
	}
	parsedURL, err := url.Parse(parsed.sourceURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Hostname() == "" {
		return parsedWeatherForecast{}, false
	}
	if coverageRows < 1 || coverageRequested < 1 || coverageRows != len(parsed.rows) || coverageRows < coverageRequested {
		return parsedWeatherForecast{}, false
	}
	return parsed, true
}

func ForecastProvider(result string) (string, bool) {
	parsed, ok := parseWeatherForecastToolResult(result)
	if !ok {
		return "", false
	}
	return parsed.provider, true
}

func BuildForecast(input string, toolResults []string, requiredSources int) string {
	byProvider := make(map[string]parsedWeatherForecast)
	for _, result := range toolResults {
		parsed, ok := parseWeatherForecastToolResult(result)
		if !ok {
			continue
		}
		byProvider[strings.ToLower(parsed.provider)] = parsed
	}
	if len(byProvider) == 0 {
		return ""
	}

	if requiredSources < 1 {
		requiredSources = 1
	}
	if len(byProvider) < requiredSources {
		return ""
	}

	forecasts := make([]parsedWeatherForecast, 0, len(byProvider))
	for _, parsed := range byProvider {
		forecasts = append(forecasts, parsed)
	}
	sort.Slice(forecasts, func(i, j int) bool {
		return weatherProviderOrder(forecasts[i].provider) < weatherProviderOrder(forecasts[j].provider)
	})
	if len(forecasts) > requiredSources {
		forecasts = forecasts[:requiredSources]
	}

	locationKey := normalizeWeatherFallbackLocation(forecasts[0].location)
	hosts := make(map[string]bool)
	for _, forecast := range forecasts {
		if normalizeWeatherFallbackLocation(forecast.location) != locationKey {
			return ""
		}
		parsedURL, _ := url.Parse(forecast.sourceURL)
		host := strings.ToLower(parsedURL.Hostname())
		if hosts[host] {
			return ""
		}
		hosts[host] = true
	}

	dates := weatherFallbackDates(input, forecasts)
	if len(dates) == 0 {
		return ""
	}
	for _, forecast := range forecasts {
		for _, date := range dates {
			if _, ok := forecast.rows[date]; !ok {
				return ""
			}
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# %s未来%d天天气\n\n", compactWeatherFallbackLocation(forecasts[0].location), len(dates))
	out.WriteString("## 数据截至时间与来源\n\n")
	for _, forecast := range forecasts {
		fmt.Fprintf(&out, "- %s：%s；<%s>\n", weatherFallbackDisplay(forecast), forecast.timeMeta, forecast.sourceURL)
	}
	out.WriteString("\n## 逐日双源对比\n\n")
	out.WriteString("| 日期 | ")
	for i, forecast := range forecasts {
		if i > 0 {
			out.WriteString(" | ")
		}
		out.WriteString(weatherFallbackShortDisplay(forecast))
	}
	out.WriteString(" |\n|---")
	for range forecasts {
		out.WriteString("|---")
	}
	out.WriteString("|\n")
	for _, date := range dates {
		parsedDate, _ := time.Parse("2006-01-02", date)
		fmt.Fprintf(&out, "| %s（%s）", date, chineseWeekday(parsedDate.Weekday()))
		for _, forecast := range forecasts {
			row := forecast.rows[date]
			fmt.Fprintf(&out, " | %s；%s；降雨风险%s（%s）", row.condition, row.temperature, row.risk, row.fields)
		}
		out.WriteString(" |\n")
	}

	out.WriteString("\n## 建议\n\n")
	weekendDates, workDates := splitWeatherFallbackDates(dates)
	if len(weekendDates) > 0 {
		severity, riskyDates := weatherFallbackRiskSummary(weekendDates, forecasts)
		if severity > 0 {
			fmt.Fprintf(&out, "- 周末户外：%s至少一个来源达到中或高降雨风险；随身带雨具，并给户外计划保留室内备选。\n", joinShortDates(riskyDates))
		} else {
			out.WriteString("- 周末户外：两源均未给出中高降雨风险，但预报可能变化，出发前仍应查看临近预报。\n")
		}
	}
	if len(workDates) > 0 {
		severity, riskyDates := weatherFallbackRiskSummary(workDates, forecasts)
		if severity > 0 {
			fmt.Fprintf(&out, "- 工作日通勤：%s至少一个来源达到中或高降雨风险；通勤备雨具并为路程留出余量。日聚合数据不能判断具体降雨时段。\n", joinShortDates(riskyDates))
		} else {
			out.WriteString("- 工作日通勤：两源均未给出中高降雨风险；仍建议出门前核对临近预报。\n")
		}
	}
	if hotDates := weatherFallbackHotDates(dates, forecasts, 35); len(hotDates) > 0 {
		fmt.Fprintf(&out, "- 高温准备：%s至少一个模型的最高温达到35°C；注意补水、防晒，并尽量减少高温暴露。\n", joinShortDates(hotDates))
	}
	if date, delta := weatherFallbackLargestHighDifference(dates, forecasts); delta >= 2 {
		fmt.Fprintf(&out, "- 模型差异：%s的最高温预测差异最大，约%.1f°C；安排活动时按较不利一侧准备，但不要把单一模型当作官方结论。\n", shortWeatherDate(date), delta)
	}
	if weatherMobilityNeed.MatchString(input) {
		out.WriteString("- 老人/行动便利：优先选择少步行、少台阶且有电梯或无障碍通道的路线；潮湿路面使用防滑鞋，给行程安排休息点和室内备选。\n")
		out.WriteString("- 健康边界：这里只提供一般出行提示，不给具体饮水量、药物或剂量建议；如有心、肾、关节等既往疾病，请遵循本人医生的个体化建议。\n")
	}

	out.WriteString("\n## 局限\n\n")
	out.WriteString("- 以上是日聚合预报，不能据此声称上午、下午、晚上或全天的具体天气。\n")
	out.WriteString("- “高/中/低”是工具按各来源字段生成的相对风险标签，不是官方气象预警；临近出行请再查当地气象部门。\n")
	return strings.TrimSpace(out.String())
}

func weatherProviderOrder(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case networktools.OpenMeteoProvider:
		return 0
	case networktools.SevenTimerProvider:
		return 1
	default:
		return 100
	}
}

func normalizeWeatherFallbackLocation(location string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(location)), ""))
}

func compactWeatherFallbackLocation(location string) string {
	parts := strings.Split(location, ",")
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		return strings.TrimSpace(parts[0])
	}
	return strings.TrimSpace(location)
}

func weatherFallbackDisplay(forecast parsedWeatherForecast) string {
	switch strings.ToLower(forecast.provider) {
	case networktools.OpenMeteoProvider:
		return "Open-Meteo（ECMWF IFS）"
	case networktools.SevenTimerProvider:
		return "7Timer（NOAA/NCEP GFS）"
	default:
		return forecast.display
	}
}

func weatherFallbackShortDisplay(forecast parsedWeatherForecast) string {
	switch strings.ToLower(forecast.provider) {
	case networktools.OpenMeteoProvider:
		return "ECMWF（Open-Meteo）"
	case networktools.SevenTimerProvider:
		return "GFS（7Timer）"
	default:
		return forecast.display
	}
}

func weatherFallbackDates(input string, forecasts []parsedWeatherForecast) []string {
	dateMatch := isoDatePattern.FindStringSubmatch(input)
	windowMatch := futureDaysPattern.FindStringSubmatch(input)
	if len(dateMatch) == 4 && len(windowMatch) == 2 {
		year, _ := strconv.Atoi(dateMatch[1])
		month, _ := strconv.Atoi(dateMatch[2])
		day, _ := strconv.Atoi(dateMatch[3])
		days, _ := strconv.Atoi(windowMatch[1])
		start := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if days >= 2 && days <= 31 && start.Year() == year && int(start.Month()) == month && start.Day() == day {
			dates := make([]string, 0, days)
			for index := 0; index < days; index++ {
				dates = append(dates, start.AddDate(0, 0, index).Format("2006-01-02"))
			}
			return dates
		}
	}
	if len(forecasts) == 0 {
		return nil
	}
	dates := make([]string, 0, len(forecasts[0].rows))
	for date := range forecasts[0].rows {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	return dates
}

func chineseWeekday(day time.Weekday) string {
	return []string{"日", "一", "二", "三", "四", "五", "六"}[int(day)]
}

func splitWeatherFallbackDates(dates []string) (weekend, work []string) {
	for _, date := range dates {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		if parsed.Weekday() == time.Saturday || parsed.Weekday() == time.Sunday {
			weekend = append(weekend, date)
		} else {
			work = append(work, date)
		}
	}
	return weekend, work
}

func weatherRiskRank(risk string) int {
	switch strings.TrimSpace(risk) {
	case "高":
		return 2
	case "中":
		return 1
	default:
		return 0
	}
}

func weatherFallbackRiskSummary(dates []string, forecasts []parsedWeatherForecast) (int, []string) {
	maxRank := 0
	var risky []string
	for _, date := range dates {
		dateRank := 0
		for _, forecast := range forecasts {
			dateRank = max(dateRank, weatherRiskRank(forecast.rows[date].risk))
		}
		if dateRank > 0 {
			risky = append(risky, date)
		}
		maxRank = max(maxRank, dateRank)
	}
	return maxRank, risky
}

func weatherFallbackHotDates(dates []string, forecasts []parsedWeatherForecast, threshold float64) []string {
	var result []string
	for _, date := range dates {
		hot := false
		for _, forecast := range forecasts {
			if forecast.rows[date].highC >= threshold {
				hot = true
				break
			}
		}
		if hot {
			result = append(result, date)
		}
	}
	return result
}

func weatherFallbackLargestHighDifference(dates []string, forecasts []parsedWeatherForecast) (string, float64) {
	if len(forecasts) < 2 {
		return "", 0
	}
	bestDate, bestDelta := "", 0.0
	for _, date := range dates {
		minimum, maximum := math.Inf(1), math.Inf(-1)
		for _, forecast := range forecasts {
			high := forecast.rows[date].highC
			minimum = math.Min(minimum, high)
			maximum = math.Max(maximum, high)
		}
		if delta := maximum - minimum; delta > bestDelta {
			bestDate, bestDelta = date, delta
		}
	}
	return bestDate, bestDelta
}

func shortWeatherDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format("01-02")
}

func joinShortDates(dates []string) string {
	values := make([]string, 0, len(dates))
	for _, date := range dates {
		values = append(values, shortWeatherDate(date))
	}
	return strings.Join(values, "、")
}
