package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"mac/internal/configvalue"
)

const (
	OpenMeteoProvider  = "open_meteo"
	SevenTimerProvider = "seven_timer"

	openMeteoProvider  = OpenMeteoProvider
	sevenTimerProvider = SevenTimerProvider
)

// WeatherForecastTool provides a deterministic, structured alternative to
// scraping JavaScript-heavy weather pages. One call reads one publisher so
// two provider calls can be executed concurrently and audited independently.
type WeatherForecastTool struct {
	client             *http.Client
	geocodingEndpoint  string
	openMeteoEndpoint  string
	sevenTimerEndpoint string
	now                func() time.Time
}

// WeatherForecastConfig supplies optional transport and endpoint overrides.
// Zero values keep the production defaults.
type WeatherForecastConfig struct {
	Client             *http.Client
	GeocodingEndpoint  string
	OpenMeteoEndpoint  string
	SevenTimerEndpoint string
	Now                func() time.Time
}

func NewWeatherForecastTool() *WeatherForecastTool {
	return NewWeatherForecastToolWithConfig(WeatherForecastConfig{})
}

func NewWeatherForecastToolWithConfig(config WeatherForecastConfig) *WeatherForecastTool {
	tool := &WeatherForecastTool{
		client:             &http.Client{Timeout: 12 * time.Second},
		geocodingEndpoint:  "https://geocoding-api.open-meteo.com/v1/search",
		openMeteoEndpoint:  "https://api.open-meteo.com/v1/forecast",
		sevenTimerEndpoint: "https://www.7timer.info/bin/api.pl",
		now:                time.Now,
	}
	if config.Client != nil {
		tool.client = config.Client
	}
	if strings.TrimSpace(config.GeocodingEndpoint) != "" {
		tool.geocodingEndpoint = config.GeocodingEndpoint
	}
	if strings.TrimSpace(config.OpenMeteoEndpoint) != "" {
		tool.openMeteoEndpoint = config.OpenMeteoEndpoint
	}
	if strings.TrimSpace(config.SevenTimerEndpoint) != "" {
		tool.sevenTimerEndpoint = config.SevenTimerEndpoint
	}
	if config.Now != nil {
		tool.now = config.Now
	}
	return tool
}

func (t *WeatherForecastTool) Name() string { return "weather_forecast" }

func (t *WeatherForecastTool) Description() string {
	return "Fetch structured daily weather forecasts without scraping. " +
		"For a request requiring two independent sources, call this tool exactly twice in one parallel batch for the same location and dates: " +
		"provider=open_meteo (ECMWF IFS) and provider=seven_timer (NOAA/NCEP GFS). Compare the returned rows and cite the exact Source URLs. " +
		"Preserve time labels exactly: retrieval_time is not a model update, while forecast_initialization_time is. Daily rows do not support morning/afternoon/evening claims. " +
		"Use generic web_fetch only as a bounded fallback when one provider explicitly fails."
}

func (t *WeatherForecastTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "City or place name, preferably including country/region when ambiguous (for example 上海 or Paris, France).",
			},
			"provider": map[string]interface{}{
				"type":        "string",
				"enum":        []string{openMeteoProvider, sevenTimerProvider},
				"description": "Independent forecast publisher to query. Use both values in parallel when cross-checking is requested.",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     14,
				"default":     7,
				"description": "Number of daily rows to return (1-14, default 7).",
			},
			"start_date": map[string]interface{}{
				"type":        "string",
				"description": "Optional first local forecast date in YYYY-MM-DD. Defaults to today at the resolved location.",
			},
		},
		"required": []string{"location", "provider"},
	}
}

func (t *WeatherForecastTool) MaxResultSize() int { return 24 * 1024 }
func (t *WeatherForecastTool) IsReadOnly() bool   { return true }

type weatherLocation struct {
	Name        string  `json:"name"`
	Admin1      string  `json:"admin1"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Timezone    string  `json:"timezone"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type dailyWeatherRow struct {
	Date                 string
	Condition            string
	TemperatureMaxC      float64
	TemperatureMinC      float64
	PrecipitationRisk    string
	PrecipitationPercent *float64
	PrecipitationMM      *float64
	PrecipitationIndex   *int
	PrecipitationType    string
}

func (t *WeatherForecastTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.client == nil {
		return "", errors.New("weather_forecast is not configured")
	}
	location, _ := params["location"].(string)
	location = strings.TrimSpace(location)
	if location == "" {
		return "", errors.New("location is required")
	}
	provider, _ := params["provider"].(string)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != openMeteoProvider && provider != sevenTimerProvider {
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
	days := configvalue.Int(params, "days", 7)
	if days < 1 {
		days = 1
	}
	if days > 14 {
		days = 14
	}

	resolved, err := t.resolveLocation(ctx, location)
	if err != nil {
		return "", err
	}
	start, err := t.resolveStartDate(params, resolved.Timezone)
	if err != nil {
		return "", err
	}

	var rows []dailyWeatherRow
	var sourceURL, updatedAt, publisher string
	switch provider {
	case openMeteoProvider:
		rows, sourceURL, updatedAt, err = t.fetchOpenMeteo(ctx, resolved, start, days)
		publisher = "Open-Meteo"
	case sevenTimerProvider:
		rows, sourceURL, updatedAt, err = t.fetchSevenTimer(ctx, resolved, start, days)
		publisher = "7Timer (NOAA/NCEP GFS)"
	}
	if err != nil {
		return "", fmt.Errorf("%s forecast failed: %w", provider, err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%s returned no daily forecast rows for %s", provider, start.Format("2006-01-02"))
	}
	return renderWeatherForecast(provider, publisher, sourceURL, updatedAt, resolved, start, days, rows), nil
}

func (t *WeatherForecastTool) resolveLocation(ctx context.Context, name string) (weatherLocation, error) {
	type lookupAttempt struct{ query, language string }
	attempts := []lookupAttempt{{query: name, language: "zh"}}
	qualifiedParts := strings.FieldsFunc(name, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；'
	})
	primary := ""
	if len(qualifiedParts) > 0 {
		primary = strings.TrimSpace(qualifiedParts[0])
	}
	if primary != "" && !strings.EqualFold(primary, strings.TrimSpace(name)) {
		attempts = append(attempts, lookupAttempt{query: primary, language: "en"}, lookupAttempt{query: primary, language: "zh"})
	} else {
		attempts = append(attempts, lookupAttempt{query: name, language: "en"})
	}
	seen := make(map[string]bool)
	for _, attempt := range attempts {
		key := strings.ToLower(strings.TrimSpace(attempt.query)) + "|" + attempt.language
		if strings.TrimSpace(attempt.query) == "" || seen[key] {
			continue
		}
		seen[key] = true
		locations, err := t.lookupWeatherLocations(ctx, attempt.query, attempt.language)
		if err != nil {
			return weatherLocation{}, fmt.Errorf("location lookup failed: %w", err)
		}
		if len(locations) > 0 {
			return bestWeatherLocationMatch(name, locations), nil
		}
	}
	return weatherLocation{}, fmt.Errorf("location %q was not found", name)
}

func (t *WeatherForecastTool) lookupWeatherLocations(ctx context.Context, name, language string) ([]weatherLocation, error) {
	endpoint, err := url.Parse(t.geocodingEndpoint)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("name", name)
	query.Set("count", "10")
	query.Set("language", language)
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()
	var payload struct {
		Results []weatherLocation `json:"results"`
	}
	if err := t.getJSON(ctx, endpoint.String(), "", &payload); err != nil {
		return nil, err
	}
	return payload.Results, nil
}

func bestWeatherLocationMatch(requested string, locations []weatherLocation) weatherLocation {
	if len(locations) == 0 {
		return weatherLocation{}
	}
	parts := strings.FieldsFunc(strings.ToLower(requested), func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；'
	})
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	best, bestScore := locations[0], -1
	for _, location := range locations {
		haystack := strings.ToLower(strings.Join([]string{location.Name, location.Admin1, location.Country, location.CountryCode}, " "))
		score := 0
		for index, part := range parts {
			if part == "" {
				continue
			}
			if strings.Contains(haystack, part) {
				if index == 0 {
					score += 10
				} else {
					score += 20
				}
			}
		}
		if score > bestScore {
			best, bestScore = location, score
		}
	}
	return best
}

func (t *WeatherForecastTool) resolveStartDate(params map[string]interface{}, timezone string) (time.Time, error) {
	zone := time.UTC
	if strings.TrimSpace(timezone) != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			zone = loaded
		}
	}
	if raw, _ := params["start_date"].(string); strings.TrimSpace(raw) != "" {
		parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), zone)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid start_date: %w", err)
		}
		return parsed, nil
	}
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	local := now.In(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone), nil
}

func (t *WeatherForecastTool) getJSON(ctx context.Context, rawURL, userAgent string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func (t *WeatherForecastTool) fetchOpenMeteo(ctx context.Context, location weatherLocation, start time.Time, days int) ([]dailyWeatherRow, string, string, error) {
	endpoint, err := url.Parse(t.openMeteoEndpoint)
	if err != nil {
		return nil, "", "", err
	}
	end := start.AddDate(0, 0, days-1)
	query := endpoint.Query()
	query.Set("latitude", strconv.FormatFloat(location.Latitude, 'f', 5, 64))
	query.Set("longitude", strconv.FormatFloat(location.Longitude, 'f', 5, 64))
	query.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,precipitation_sum")
	query.Set("models", "ecmwf_ifs025")
	query.Set("timezone", fallbackString(location.Timezone, "auto"))
	query.Set("start_date", start.Format("2006-01-02"))
	query.Set("end_date", end.Format("2006-01-02"))
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Timezone string `json:"timezone"`
		Daily    struct {
			Time                        []string  `json:"time"`
			WeatherCode                 []int     `json:"weather_code"`
			TemperatureMax              []float64 `json:"temperature_2m_max"`
			TemperatureMin              []float64 `json:"temperature_2m_min"`
			PrecipitationProbabilityMax []float64 `json:"precipitation_probability_max"`
			PrecipitationSum            []float64 `json:"precipitation_sum"`
		} `json:"daily"`
	}
	if err := t.getJSON(ctx, endpoint.String(), "", &payload); err != nil {
		return nil, endpoint.String(), "", err
	}
	rows := make([]dailyWeatherRow, 0, len(payload.Daily.Time))
	for i, date := range payload.Daily.Time {
		if i >= len(payload.Daily.WeatherCode) || i >= len(payload.Daily.TemperatureMax) || i >= len(payload.Daily.TemperatureMin) {
			continue
		}
		row := dailyWeatherRow{
			Date:            date,
			Condition:       wmoWeatherDescription(payload.Daily.WeatherCode[i]),
			TemperatureMaxC: payload.Daily.TemperatureMax[i],
			TemperatureMinC: payload.Daily.TemperatureMin[i],
		}
		if i < len(payload.Daily.PrecipitationProbabilityMax) {
			value := payload.Daily.PrecipitationProbabilityMax[i]
			row.PrecipitationPercent = &value
		}
		if i < len(payload.Daily.PrecipitationSum) {
			value := payload.Daily.PrecipitationSum[i]
			row.PrecipitationMM = &value
		}
		row.PrecipitationRisk = precipitationRisk(row.PrecipitationPercent, row.PrecipitationMM)
		rows = append(rows, row)
	}
	retrievedAt := t.currentTime().UTC().Format(time.RFC3339)
	return rows, endpoint.String(), "retrieval_time=" + retrievedAt + "; forecast_initialization_time=not_provided", nil
}

type sevenTimerDailyAccumulator struct {
	date              string
	minTemp           float64
	maxTemp           float64
	hasTemp           bool
	maxPrecipIndex    int
	precipitationType string
	condition         string
	conditionHour     int
}

func (t *WeatherForecastTool) fetchSevenTimer(ctx context.Context, location weatherLocation, start time.Time, days int) ([]dailyWeatherRow, string, string, error) {
	endpoint, err := url.Parse(t.sevenTimerEndpoint)
	if err != nil {
		return nil, "", "", err
	}
	query := endpoint.Query()
	query.Set("lon", strconv.FormatFloat(location.Longitude, 'f', 5, 64))
	query.Set("lat", strconv.FormatFloat(location.Latitude, 'f', 5, 64))
	query.Set("product", "civil")
	query.Set("output", "json")
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Product    string `json:"product"`
		Init       string `json:"init"`
		DataSeries []struct {
			Timepoint    int     `json:"timepoint"`
			PrecipType   string  `json:"prec_type"`
			PrecipAmount int     `json:"prec_amount"`
			Temperature  float64 `json:"temp2m"`
			Weather      string  `json:"weather"`
		} `json:"dataseries"`
	}
	if err := t.getJSON(ctx, endpoint.String(), "OpenKitty-Weather/1.0", &payload); err != nil {
		return nil, endpoint.String(), "", err
	}
	initializedAt, err := time.ParseInLocation("2006010215", payload.Init, time.UTC)
	if err != nil {
		return nil, endpoint.String(), "", fmt.Errorf("invalid 7Timer initialization time %q: %w", payload.Init, err)
	}
	zone := time.UTC
	if loaded, loadErr := time.LoadLocation(location.Timezone); loadErr == nil {
		zone = loaded
	}
	end := start.AddDate(0, 0, days)
	byDate := make(map[string]*sevenTimerDailyAccumulator)
	for _, point := range payload.DataSeries {
		local := initializedAt.Add(time.Duration(point.Timepoint) * time.Hour).In(zone)
		if local.Before(start) || !local.Before(end) {
			continue
		}
		date := local.Format("2006-01-02")
		acc := byDate[date]
		if acc == nil {
			acc = &sevenTimerDailyAccumulator{date: date}
			byDate[date] = acc
		}
		if !acc.hasTemp || point.Temperature < acc.minTemp {
			acc.minTemp = point.Temperature
		}
		if !acc.hasTemp || point.Temperature > acc.maxTemp {
			acc.maxTemp = point.Temperature
		}
		acc.hasTemp = true
		if point.PrecipAmount > acc.maxPrecipIndex {
			acc.maxPrecipIndex = point.PrecipAmount
		}
		if value := strings.TrimSpace(point.PrecipType); value != "" && !strings.EqualFold(value, "none") {
			acc.precipitationType = value
		}
		hourDistance := weatherAbsInt(local.Hour() - 12)
		if point.Weather != "" && (acc.condition == "" || hourDistance < acc.conditionHour) {
			acc.condition = sevenTimerWeatherDescription(point.Weather)
			acc.conditionHour = hourDistance
		}
	}

	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	rows := make([]dailyWeatherRow, 0, len(dates))
	for _, date := range dates {
		acc := byDate[date]
		if !acc.hasTemp {
			continue
		}
		index := acc.maxPrecipIndex
		rows = append(rows, dailyWeatherRow{
			Date:               date,
			Condition:          fallbackString(acc.condition, "未知"),
			TemperatureMaxC:    acc.maxTemp,
			TemperatureMinC:    acc.minTemp,
			PrecipitationRisk:  sevenTimerPrecipitationRisk(index, acc.precipitationType),
			PrecipitationIndex: &index,
			PrecipitationType:  fallbackString(acc.precipitationType, "none"),
		})
	}
	updatedAt := "forecast_initialization_time=" + initializedAt.UTC().Format(time.RFC3339) + "; retrieval_time=" + t.currentTime().UTC().Format(time.RFC3339)
	return rows, endpoint.String(), updatedAt, nil
}

func (t *WeatherForecastTool) currentTime() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

func renderWeatherForecast(provider, publisher, sourceURL, updatedAt string, location weatherLocation, start time.Time, requestedDays int, rows []dailyWeatherRow) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Structured weather forecast — %s\n\n", publisher)
	fmt.Fprintf(&out, "Provider key: %s\n", provider)
	fmt.Fprintf(&out, "Resolved location: %s\n", weatherLocationLabel(location))
	fmt.Fprintf(&out, "Coordinates: %.5f, %.5f\n", location.Latitude, location.Longitude)
	fmt.Fprintf(&out, "Timezone: %s\n", fallbackString(location.Timezone, start.Location().String()))
	fmt.Fprintf(&out, "Time metadata (preserve these labels exactly): %s\n", updatedAt)
	fmt.Fprintf(&out, "Source: %s\n", sourceURL)
	fmt.Fprintf(&out, "Coverage: %d/%d requested daily rows\n\n", len(rows), requestedDays)
	out.WriteString("| Date | Weather | High / Low | Precipitation risk | Provider fields |\n")
	out.WriteString("|---|---|---:|---|---|\n")
	for _, row := range rows {
		fields := make([]string, 0, 2)
		if row.PrecipitationPercent != nil {
			fields = append(fields, fmt.Sprintf("max probability %.0f%%", *row.PrecipitationPercent))
		}
		if row.PrecipitationMM != nil {
			fields = append(fields, fmt.Sprintf("amount %.1f mm", *row.PrecipitationMM))
		}
		if row.PrecipitationIndex != nil {
			fields = append(fields, fmt.Sprintf("3-hour precipitation category %d/9", *row.PrecipitationIndex))
		}
		if row.PrecipitationType != "" {
			fields = append(fields, "type "+row.PrecipitationType)
		}
		if len(fields) == 0 {
			fields = append(fields, "precipitation details unavailable")
		}
		fmt.Fprintf(&out, "| %s | %s | %.1f°C / %.1f°C | %s | %s |\n",
			row.Date, row.Condition, row.TemperatureMaxC, row.TemperatureMinC,
			row.PrecipitationRisk, strings.Join(fields, "; "))
	}
	out.WriteString("\nRisk labels are a transparent tool-level summary, not official alerts. For Open-Meteo: high at probability >=60% or daily amount >=10 mm; medium at >=30% or >=1 mm. For 7Timer: high at its 3-hour precipitation category >=5; medium at category >=2 or a precipitation type is reported.\n")
	out.WriteString("Temporal granularity: daily aggregate only. These rows do not support claims about morning, afternoon, evening, overnight, or all-day timing/duration.\n")
	out.WriteString("Synthesis boundary: report model differences numerically; do not invent a physical cause, monotonic trend, official alert, or extra forecast field.\n")
	return out.String()
}

func weatherLocationLabel(location weatherLocation) string {
	parts := []string{strings.TrimSpace(location.Name), strings.TrimSpace(location.Admin1), strings.TrimSpace(location.Country)}
	seen := make(map[string]bool)
	compact := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" && !seen[part] {
			seen[part] = true
			compact = append(compact, part)
		}
	}
	return strings.Join(compact, ", ")
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func precipitationRisk(probability, amount *float64) string {
	if (probability != nil && *probability >= 60) || (amount != nil && *amount >= 10) {
		return "高"
	}
	if (probability != nil && *probability >= 30) || (amount != nil && *amount >= 1) {
		return "中"
	}
	return "低"
}

func wmoWeatherDescription(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1, 2:
		return "晴间多云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "雨"
	case 71, 73, 75, 77:
		return "雪"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95, 96, 99:
		return "雷雨"
	default:
		return fmt.Sprintf("WMO code %d", code)
	}
}

func sevenTimerWeatherDescription(symbol string) string {
	symbol = strings.ToLower(strings.TrimSpace(symbol))
	switch {
	case strings.Contains(symbol, "thunder") || strings.HasPrefix(symbol, "ts"):
		return "雷雨"
	case strings.Contains(symbol, "heavysnow"):
		return "大雪"
	case strings.Contains(symbol, "snow") || strings.Contains(symbol, "sleet"):
		return "雨雪"
	case strings.Contains(symbol, "heavyrain") || strings.Contains(symbol, "tsrain"):
		return "大雨"
	case strings.Contains(symbol, "rainshowers") || strings.Contains(symbol, "showers") || strings.Contains(symbol, "shower"):
		return "阵雨"
	case strings.Contains(symbol, "rain"):
		return "雨"
	case strings.Contains(symbol, "fog"):
		return "雾"
	case strings.Contains(symbol, "humid"):
		return "湿热多云"
	case strings.Contains(symbol, "partlycloudy") || strings.Contains(symbol, "pcloudy") || strings.Contains(symbol, "mcloudy"):
		return "多云"
	case strings.Contains(symbol, "cloudy"):
		return "阴"
	case strings.Contains(symbol, "fair"):
		return "晴间多云"
	case strings.Contains(symbol, "clearsky") || strings.Contains(symbol, "clear"):
		return "晴"
	default:
		return symbol
	}
}

func sevenTimerPrecipitationRisk(index int, precipitationType string) string {
	if index >= 5 {
		return "高"
	}
	if index >= 2 || (strings.TrimSpace(precipitationType) != "" && !strings.EqualFold(precipitationType, "none")) {
		return "中"
	}
	return "低"
}

func weatherAbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
