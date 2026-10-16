package network

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mac/internal/toolcontract"
)

var fallbackISODatePattern = regexp.MustCompile(`\b(20[0-9]{2})-([01]?[0-9])-([0-3]?[0-9])\b`)

const fuelPriceStructuredPrefix = "OPENKITTY_FUEL_PRICE_RESULT="

// fuelPriceFallbackPayload intentionally has a renderer-private name. The
// fetcher may keep additional non-JSON bookkeeping fields without coupling the
// trusted finalizer to those implementation details.
type fuelPriceFallbackPayload struct {
	Version                int                 `json:"version"`
	Provider               string              `json:"provider"`
	Publisher              string              `json:"publisher"`
	Region                 string              `json:"region"`
	SourceURL              string              `json:"source_url"`
	RetrievalTime          string              `json:"retrieval_time"`
	PublishedTime          string              `json:"published_time"`
	EffectiveTime          string              `json:"effective_time"`
	UpdateTime             string              `json:"update_time"`
	Prices                 map[string]*float64 `json:"prices"`
	PriceStatus            map[string]string   `json:"price_status"`
	NextAdjustmentDate     string              `json:"next_adjustment_date"`
	CrudeChangeRate        *float64            `json:"crude_change_rate"`
	PredictedDirection     string              `json:"predicted_direction"`
	PredictedChangeCNYPerL *float64            `json:"predicted_change_cny_per_l"`
	ForecastCalculationDay string              `json:"forecast_calculation_day"`
}

type parsedFuelPrice struct {
	result fuelPriceFallbackPayload
	prices map[string]int
	host   string
}

var (
	fuelPriceIntentPattern = regexp.MustCompile(`(?i)(?:油价|成品油|汽油|柴油|gasoline|diesel|fuel\s+price)`)
	fuelOtherDomainPattern = regexp.MustCompile(`(?i)(?:天气|股票|基金|新闻|航班|酒店|weather|stock|flight|headline)`)
	fuelHistoryPattern     = regexp.MustCompile(`(?i)(?:沿用|接着|继续使用|根据刚才|上一轮|上一次|as\s+above|previous\s+turn)`)
	fuelFleetPattern       = regexp.MustCompile(`(?i)(?:车队|逐车|里程|百公里油耗|月度成本|fleet|mileage)`)
	fuelVolumePattern      = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(?:升|l(?:iter|itre)?s?)`)
)

type FuelPriceFallbackOptions struct {
	IsFastReadOnly      func(string) bool
	RequiredSourceCount func(string) int
}

func BuildFuelPriceFallback(input string, toolResults []string, options ...FuelPriceFallbackOptions) string {
	var opts FuelPriceFallbackOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if !isStandaloneFuelPriceTurn(input, opts.IsFastReadOnly) {
		return ""
	}

	byProvider := make(map[string]parsedFuelPrice)
	conflictingProvider := make(map[string]bool)
	for _, raw := range toolResults {
		parsed, ok := parseFuelPriceToolResult(input, raw)
		if !ok {
			continue
		}
		provider := parsed.result.Provider
		if previous, exists := byProvider[provider]; exists {
			if !sameParsedFuelPrice(previous, parsed) {
				conflictingProvider[provider] = true
			}
			continue
		}
		byProvider[provider] = parsed
	}
	for provider := range conflictingProvider {
		delete(byProvider, provider)
	}

	requiredSources := 0
	if opts.RequiredSourceCount != nil {
		requiredSources = opts.RequiredSourceCount(input)
	}
	if requiredSources < 1 {
		requiredSources = 1
	}
	if len(byProvider) < requiredSources {
		return ""
	}
	// The official regional notice anchors 92/95/diesel. 98-octane is not
	// government-priced, so two independent market publishers must corroborate it.
	if _, ok := byProvider["regional_official"]; !ok {
		return ""
	}
	for _, grade := range []string{"92", "95", "98", "0_diesel"} {
		count := 0
		for _, parsed := range byProvider {
			if _, ok := parsed.prices[grade]; ok {
				count++
			}
		}
		if count < 2 {
			return ""
		}
	}
	if fuelForecastRequested(input) && !hasFuelForecast(byProvider) {
		return ""
	}

	providers := make([]parsedFuelPrice, 0, len(byProvider))
	for _, provider := range []string{"regional_official", "nxvav", "tuanyou"} {
		if parsed, ok := byProvider[provider]; ok {
			providers = append(providers, parsed)
		}
	}
	if len(providers) < requiredSources {
		return ""
	}
	return renderFuelPriceFallback(input, providers)
}

func isStandaloneFuelPriceTurn(input string, isFastReadOnly func(string) bool) bool {
	if isFastReadOnly == nil || !isFastReadOnly(input) || !fuelPriceIntentPattern.MatchString(input) {
		return false
	}
	if fuelOtherDomainPattern.MatchString(input) || fuelHistoryPattern.MatchString(input) || fuelFleetPattern.MatchString(input) {
		return false
	}
	return true
}

func parseFuelPriceToolResult(input, raw string) (parsedFuelPrice, bool) {
	record, ok := toolcontract.ParseRecord(raw)
	if !ok || !record.Succeeded || !strings.EqualFold(strings.TrimSpace(record.Tool), "fuel_price") ||
		!strings.EqualFold(strings.TrimSpace(record.SearchMode), "direct") {
		return parsedFuelPrice{}, false
	}

	var markers []fuelPriceFallbackPayload
	for _, line := range strings.Split(raw, "\n") {
		idx := strings.Index(line, fuelPriceStructuredPrefix)
		if idx < 0 {
			continue
		}
		var marker fuelPriceFallbackPayload
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[idx+len(fuelPriceStructuredPrefix):])), &marker); err == nil {
			markers = append(markers, marker)
		}
	}
	if len(markers) != 1 {
		return parsedFuelPrice{}, false
	}
	marker := markers[0]
	marker.Provider = strings.ToLower(strings.TrimSpace(marker.Provider))
	marker.Region = strings.TrimSpace(marker.Region)
	marker.SourceURL = strings.TrimSpace(marker.SourceURL)
	if marker.Provider != "regional_official" && marker.Provider != "nxvav" && marker.Provider != "tuanyou" {
		return parsedFuelPrice{}, false
	}
	if marker.Publisher == "" || marker.Region == "" || !fuelInputContainsRegion(input, marker.Region) {
		return parsedFuelPrice{}, false
	}
	parsedURL, err := url.Parse(marker.SourceURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Hostname() == "" {
		return parsedFuelPrice{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(parsedURL.Hostname()), "www.")
	expectedHost := map[string]string{
		"regional_official": "fgw.sh.gov.cn",
		"nxvav":             "api.nxvav.cn",
		"tuanyou":           "tuanyou.net",
	}[marker.Provider]
	if host != expectedHost {
		return parsedFuelPrice{}, false
	}
	if record.SourceURL != "" && sourceHost(record.SourceURL) != host {
		return parsedFuelPrice{}, false
	}
	if _, err := time.Parse(time.RFC3339, marker.RetrievalTime); err != nil {
		return parsedFuelPrice{}, false
	}

	prices := make(map[string]int)
	for _, grade := range []string{"92", "95", "98", "0_diesel"} {
		value := marker.Prices[grade]
		if value == nil {
			continue
		}
		cents, valid := strictFuelPriceCents(*value)
		if !valid {
			return parsedFuelPrice{}, false
		}
		prices[grade] = cents
	}
	if _, ok := prices["92"]; !ok {
		return parsedFuelPrice{}, false
	}
	if _, ok := prices["95"]; !ok {
		return parsedFuelPrice{}, false
	}
	if _, ok := prices["0_diesel"]; !ok {
		return parsedFuelPrice{}, false
	}
	if marker.Provider != "regional_official" {
		if _, ok := prices["98"]; !ok {
			return parsedFuelPrice{}, false
		}
	} else if _, has98 := prices["98"]; has98 {
		return parsedFuelPrice{}, false
	}
	return parsedFuelPrice{result: marker, prices: prices, host: host}, true
}

func strictFuelPriceCents(value float64) (int, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 1 || value > 30 {
		return 0, false
	}
	cents := int(math.Round(value * 100))
	if math.Abs(value-float64(cents)/100) > 0.000001 {
		return 0, false
	}
	return cents, true
}

func fuelInputContainsRegion(input, region string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
		value = strings.TrimSuffix(value, "市")
		value = strings.TrimSuffix(value, "省")
		return value
	}
	needle := normalize(region)
	haystack := normalize(input)
	return needle != "" && strings.Contains(haystack, needle)
}

func sameParsedFuelPrice(a, b parsedFuelPrice) bool {
	aJSON, _ := json.Marshal(a.result)
	bJSON, _ := json.Marshal(b.result)
	return string(aJSON) == string(bJSON)
}

func fuelForecastRequested(input string) bool {
	lower := strings.ToLower(input)
	return strings.Contains(lower, "预测") || strings.Contains(lower, "调价") || strings.Contains(lower, "forecast")
}

func hasFuelForecast(providers map[string]parsedFuelPrice) bool {
	for _, parsed := range providers {
		if hasMeaningfulFuelForecast(parsed.result) {
			return true
		}
	}
	return false
}

func hasMeaningfulFuelForecast(marker fuelPriceFallbackPayload) bool {
	direction := strings.ToLower(strings.TrimSpace(marker.PredictedDirection))
	return marker.NextAdjustmentDate != "" || marker.CrudeChangeRate != nil || marker.PredictedChangeCNYPerL != nil ||
		marker.ForecastCalculationDay != "" || (direction != "" && direction != "unknown")
}

func renderFuelPriceFallback(input string, providers []parsedFuelPrice) string {
	region := providers[0].result.Region
	targetDate := ""
	if match := fallbackISODatePattern.FindStringSubmatch(input); len(match) == 4 {
		targetDate = strings.Join(match[1:], "-")
	}
	if targetDate == "" {
		targetDate = providers[0].result.RetrievalTime[:10]
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# %s成品油价格（%s）\n\n", region, targetDate)
	out.WriteString("## 数据时间与来源\n\n")
	for _, parsed := range providers {
		marker := parsed.result
		times := nonEmptyFuelTimes(marker)
		fmt.Fprintf(&out, "- %s（%s）：%s；[来源直链](%s)\n", marker.Publisher, fuelProviderKind(marker.Provider), strings.Join(times, "；"), marker.SourceURL)
	}

	out.WriteString("\n## 当前执行价交叉核对（元/升）\n\n")
	out.WriteString("| 品类 | 上海发改委 | NxVav | 团油 | 核验结果 |\n|---|---:|---:|---:|---|\n")
	for _, grade := range []string{"92", "95", "98", "0_diesel"} {
		values := make([]int, 0, len(providers))
		cells := make(map[string]string)
		for _, parsed := range providers {
			if cents, ok := parsed.prices[grade]; ok {
				values = append(values, cents)
				cells[parsed.result.Provider] = formatFuelCents(cents)
			} else {
				status := strings.TrimSpace(parsed.result.PriceStatus[grade])
				if status == "" {
					status = "未发布"
				}
				cells[parsed.result.Provider] = fuelPriceStatusLabel(status)
			}
		}
		status := fuelAgreementLabel(values)
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s |\n", fuelGradeLabel(grade), cells["regional_official"], cells["nxvav"], cells["tuanyou"], status)
	}

	if liters, ok := requestedFuelLiters(input); ok {
		out.WriteString("\n## 加油成本\n\n")
		price92, ok92 := agreedFuelPrice(providers, "92")
		price95, ok95 := agreedFuelPrice(providers, "95")
		if ok92 && ok95 {
			cost92 := float64(price92) * liters
			cost95 := float64(price95) * liters
			fmt.Fprintf(&out, "- 92号：¥%s/L × %s L = **¥%.2f**\n", formatFuelCents(price92), formatFuelLiters(liters), cost92/100)
			fmt.Fprintf(&out, "- 95号：¥%s/L × %s L = **¥%.2f**\n", formatFuelCents(price95), formatFuelLiters(liters), cost95/100)
			fmt.Fprintf(&out, "- 差额：¥%.2f - ¥%.2f = **¥%.2f**\n", cost95/100, cost92/100, (cost95-cost92)/100)
		} else {
			out.WriteString("各来源对当前价存在冲突，不取平均值；按来源分别计算：\n\n")
			out.WriteString("| 来源 | 92号成本 | 95号成本 | 差额 |\n|---|---:|---:|---:|\n")
			for _, parsed := range providers {
				p92, ok92 := parsed.prices["92"]
				p95, ok95 := parsed.prices["95"]
				if !ok92 || !ok95 {
					continue
				}
				c92, c95 := float64(p92)*liters/100, float64(p95)*liters/100
				fmt.Fprintf(&out, "| %s | ¥%.2f | ¥%.2f | ¥%.2f |\n", parsed.result.Publisher, c92, c95, c95-c92)
			}
		}
	}

	if fuelForecastRequested(input) {
		out.WriteString("\n## 下一次调价预测（尚未生效）\n\n")
		for _, parsed := range providers {
			marker := parsed.result
			if !hasMeaningfulFuelForecast(marker) {
				continue
			}
			parts := make([]string, 0, 4)
			if marker.ForecastCalculationDay != "" {
				parts = append(parts, "测算日 "+marker.ForecastCalculationDay)
			}
			if marker.NextAdjustmentDate != "" {
				parts = append(parts, "下一窗口 "+marker.NextAdjustmentDate)
			}
			if marker.CrudeChangeRate != nil {
				parts = append(parts, fmt.Sprintf("原油变化率 %.2f%%", *marker.CrudeChangeRate))
			}
			if marker.PredictedDirection != "" || marker.PredictedChangeCNYPerL != nil {
				prediction := fuelDirectionLabel(marker.PredictedDirection)
				if marker.PredictedChangeCNYPerL != nil {
					prediction += fmt.Sprintf(" %.2f元/升", math.Abs(*marker.PredictedChangeCNYPerL))
				}
				parts = append(parts, "预测"+prediction)
			}
			fmt.Fprintf(&out, "- %s：%s。该预测不是当前执行价，也未用于上述成本计算。[预测来源](%s)\n", marker.Publisher, strings.Join(parts, "；"), marker.SourceURL)
		}
	}

	out.WriteString("\n## 口径说明\n\n")
	out.WriteString("- 上海发改委发布的是92号、95号汽油和0号柴油的最高零售价格；98号由经营企业结合市场自主定价，因此表中仅列两个市场来源交叉值。\n")
	out.WriteString("- 加油站实际成交价可能因企业定价与优惠活动不同；上表没有用预测值替代当前价格，也没有对冲突值取平均。\n")
	return strings.TrimSpace(out.String())
}

func nonEmptyFuelTimes(marker fuelPriceFallbackPayload) []string {
	pairs := []struct{ label, value string }{
		{"retrieval_time", marker.RetrievalTime},
		{"published_time", marker.PublishedTime},
		{"effective_time", marker.EffectiveTime},
		{"update_time", marker.UpdateTime},
	}
	values := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if strings.TrimSpace(pair.value) != "" {
			values = append(values, pair.label+"="+pair.value)
		}
	}
	return values
}

func fuelProviderKind(provider string) string {
	if provider == "regional_official" {
		return "政府最高零售价"
	}
	return "市场参考价"
}

func fuelGradeLabel(grade string) string {
	return map[string]string{"92": "92号汽油", "95": "95号汽油", "98": "98号汽油", "0_diesel": "0号柴油"}[grade]
}

func fuelPriceStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "not_published_by_official_enterprise_priced":
		return "官方未发布（企业自主定价）"
	case "official_maximum_retail_price":
		return "官方最高零售价"
	default:
		return strings.TrimSpace(status)
	}
}

func formatFuelCents(cents int) string { return fmt.Sprintf("%.2f", float64(cents)/100) }

func fuelAgreementLabel(values []int) string {
	if len(values) < 2 {
		return "来源不足"
	}
	unique := make(map[int]bool)
	for _, value := range values {
		unique[value] = true
	}
	if len(unique) == 1 {
		return "一致"
	}
	return "冲突（逐源列出，不取平均）"
}

func agreedFuelPrice(providers []parsedFuelPrice, grade string) (int, bool) {
	unique := make(map[int]bool)
	for _, parsed := range providers {
		if cents, ok := parsed.prices[grade]; ok {
			unique[cents] = true
		}
	}
	if len(unique) != 1 {
		return 0, false
	}
	for cents := range unique {
		return cents, true
	}
	return 0, false
}

func requestedFuelLiters(input string) (float64, bool) {
	match := fuelVolumePattern.FindStringSubmatch(strings.ToLower(input))
	if len(match) != 2 {
		return 0, false
	}
	liters, err := strconv.ParseFloat(match[1], 64)
	if err != nil || liters <= 0 || liters > 100000 {
		return 0, false
	}
	return liters, true
}

func formatFuelLiters(liters float64) string {
	if math.Mod(liters, 1) == 0 {
		return strconv.FormatInt(int64(liters), 10)
	}
	return strconv.FormatFloat(liters, 'f', -1, 64)
}

func fuelDirectionLabel(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "down", "decrease", "drop", "lower":
		return "下调"
	case "up", "increase", "raise", "higher":
		return "上调"
	case "unchanged", "flat":
		return "搁浅/不变"
	case "uncertain":
		return "方向不确定"
	default:
		return strings.TrimSpace(direction)
	}
}

// Keep deterministic output stable if callers ever supply providers in an
// unexpected order outside buildFuelPriceFallback.
func sortFuelProviders(providers []parsedFuelPrice) {
	order := map[string]int{"regional_official": 0, "nxvav": 1, "tuanyou": 2}
	sort.SliceStable(providers, func(i, j int) bool {
		return order[providers[i].result.Provider] < order[providers[j].result.Provider]
	})
}
