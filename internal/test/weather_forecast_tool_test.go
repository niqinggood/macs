package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	networktools "mac/internal/tools/network"
)

type WeatherForecastTool = networktools.WeatherForecastTool
type WeatherForecastConfig = networktools.WeatherForecastConfig

const (
	openMeteoProvider  = networktools.OpenMeteoProvider
	sevenTimerProvider = networktools.SevenTimerProvider
)

var NewWeatherForecastTool = networktools.NewWeatherForecastTool

func newWeatherForecastToolWithConfig(config WeatherForecastConfig) *WeatherForecastTool {
	return networktools.NewWeatherForecastToolWithConfig(config)
}

func newWeatherForecastTestTool(t *testing.T) (*WeatherForecastTool, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{
				"name": "上海", "admin1": "上海", "country": "中国", "country_code": "CN",
				"timezone": "Asia/Shanghai", "latitude": 31.2304, "longitude": 121.4737,
			}},
		})
	})
	mux.HandleFunc("/open-meteo", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("models"); got != "ecmwf_ifs025" {
			t.Errorf("Open-Meteo model = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"timezone": "Asia/Shanghai",
			"daily": map[string]interface{}{
				"time":                          []string{"2026-07-11", "2026-07-12", "2026-07-13", "2026-07-14", "2026-07-15", "2026-07-16", "2026-07-17"},
				"weather_code":                  []int{61, 63, 3, 2, 80, 65, 1},
				"temperature_2m_max":            []float64{31, 30, 32, 34, 33, 30, 32},
				"temperature_2m_min":            []float64{26, 25, 26, 27, 27, 25, 26},
				"precipitation_probability_max": []float64{70, 60, 20, 10, 45, 80, 15},
				"precipitation_sum":             []float64{12, 8, 0, 0, 2, 18, 0},
			},
		})
	})
	mux.HandleFunc("/seven-timer", func(w http.ResponseWriter, r *http.Request) {
		series := make([]map[string]interface{}, 0, 64)
		for i := 1; i <= 64; i++ {
			series = append(series, map[string]interface{}{
				"timepoint": i * 3, "temp2m": 25 + i%6, "weather": "lightrainday",
				"prec_type": "rain", "prec_amount": 3,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"product": "civil", "init": "2026071012", "dataseries": series,
		})
	})
	server := httptest.NewServer(mux)
	tool := newWeatherForecastToolWithConfig(WeatherForecastConfig{
		Client:             server.Client(),
		GeocodingEndpoint:  server.URL + "/geocode",
		OpenMeteoEndpoint:  server.URL + "/open-meteo",
		SevenTimerEndpoint: server.URL + "/seven-timer",
		Now:                func() time.Time { return time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC) },
	})
	return tool, server.Close
}

func TestWeatherForecastToolReturnsStructuredOpenMeteoRows(t *testing.T) {
	tool, closeServer := newWeatherForecastTestTool(t)
	defer closeServer()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"location": "上海", "provider": openMeteoProvider, "days": 7, "start_date": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Coverage: 7/7", "2026-07-11", "2026-07-17", "70%", "12.0 mm", "Source: ", "retrieval_time=", "forecast_initialization_time=not_provided", "Temporal granularity: daily", "do not invent a physical cause"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}

func TestWeatherForecastToolAggregatesSevenTimerIntoDailyRows(t *testing.T) {
	tool, closeServer := newWeatherForecastTestTool(t)
	defer closeServer()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"location": "上海", "provider": sevenTimerProvider, "days": 7, "start_date": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Coverage: 7/7", "2026-07-11", "2026-07-17", "precipitation category 3/9", "type rain", "forecast_initialization_time=", "retrieval_time=", "Temporal granularity: daily"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}

func TestWeatherForecastResultsSatisfyIndependentDateCoverage(t *testing.T) {
	tool, closeServer := newWeatherForecastTestTool(t)
	defer closeServer()
	results := make([]string, 0, 2)
	for _, provider := range []string{openMeteoProvider, sevenTimerProvider} {
		call := ToolCall{Name: "weather_forecast", Arguments: map[string]interface{}{
			"location": "上海", "provider": provider, "days": 7, "start_date": "2026-07-11",
		}}
		content, err := tool.Execute(context.Background(), call.Arguments)
		if err != nil {
			t.Fatalf("%s Execute: %v", provider, err)
		}
		results = append(results, toolResultForFinalization(call, content, true))
	}
	input := "从今天（2026-07-11）起查询未来7天天气，至少核对两个相互独立、可信的来源"
	if issues := researchCoverageIssues(input, results); len(issues) != 0 {
		t.Fatalf("structured independent weather coverage issues: %v", issues)
	}
}

func TestWeatherForecastToolIsReadOnlyAndCore(t *testing.T) {
	tool := NewWeatherForecastTool()
	if !tool.IsReadOnly() || !CoreTools[tool.Name()] || !isExternalEvidenceToolName(tool.Name()) {
		t.Fatalf("weather tool capability registration incomplete")
	}
}
