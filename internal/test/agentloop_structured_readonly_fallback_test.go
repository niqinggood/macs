package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	weatherartifact "mac/internal/artifacts/weather"
)

type structuredFallbackWeatherTool struct {
	openMeteo  string
	sevenTimer string
}

func (structuredFallbackWeatherTool) Name() string { return "weather_forecast" }
func (structuredFallbackWeatherTool) Description() string {
	return "structured weather test tool"
}
func (structuredFallbackWeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"provider":   map[string]interface{}{"type": "string"},
			"location":   map[string]interface{}{"type": "string"},
			"days":       map[string]interface{}{"type": "integer"},
			"start_date": map[string]interface{}{"type": "string"},
		},
	}
}
func (t structuredFallbackWeatherTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if params["provider"] == sevenTimerProvider {
		return t.sevenTimer, nil
	}
	return t.openMeteo, nil
}
func (structuredFallbackWeatherTool) IsReadOnly() bool   { return true }
func (structuredFallbackWeatherTool) MaxResultSize() int { return 24 * 1024 }

func weatherFallbackRecord(provider, display, metadata, source string, rows []string) string {
	record := `OPENKITTY_TOOL_RECORD={"tool":"weather_forecast","succeeded":true,"search_mode":"direct","source_url":"` + source + `"}`
	return record + "\n# Structured weather forecast — " + display + "\n\n" +
		"Provider key: " + provider + "\n" +
		"Resolved location: 上海, 上海市, 中国\n" +
		"Coordinates: 31.22222, 121.45806\n" +
		"Timezone: Asia/Shanghai\n" +
		"Time metadata (preserve these labels exactly): " + metadata + "\n" +
		"Source: " + source + "\n" +
		"Coverage: 7/7 requested daily rows\n\n" +
		"| Date | Weather | High / Low | Precipitation risk | Provider fields |\n" +
		"|---|---|---:|---|---|\n" + strings.Join(rows, "\n")
}

func TestBuildWeatherForecastFallbackRequiresCompleteIndependentStructuredResults(t *testing.T) {
	openRows := []string{
		"| 2026-07-11 | 毛毛雨 | 31.6°C / 26.6°C | 高 | max probability 65%; amount 0.4 mm |",
		"| 2026-07-12 | 毛毛雨 | 31.1°C / 26.6°C | 高 | max probability 98%; amount 7.4 mm |",
		"| 2026-07-13 | 阴 | 32.4°C / 27.8°C | 低 | max probability 21%; amount 0.0 mm |",
		"| 2026-07-14 | 雷雨 | 34.3°C / 27.9°C | 中 | max probability 31%; amount 0.7 mm |",
		"| 2026-07-15 | 雷雨 | 38.0°C / 28.3°C | 中 | max probability 47%; amount 0.4 mm |",
		"| 2026-07-16 | 雷雨 | 36.0°C / 29.4°C | 高 | max probability 68%; amount 0.6 mm |",
		"| 2026-07-17 | 雷雨 | 34.3°C / 27.7°C | 高 | max probability 77%; amount 7.4 mm |",
	}
	gfsRows := []string{
		"| 2026-07-11 | 阴 | 27.0°C / 26.0°C | 中 | 3-hour precipitation category 2/9; type rain |",
		"| 2026-07-12 | 雨 | 28.0°C / 26.0°C | 中 | 3-hour precipitation category 1/9; type rain |",
		"| 2026-07-13 | 阴 | 30.0°C / 28.0°C | 中 | 3-hour precipitation category 0/9; type rain |",
		"| 2026-07-14 | 阴 | 31.0°C / 27.0°C | 中 | 3-hour precipitation category 0/9; type rain |",
		"| 2026-07-15 | 雷雨 | 31.0°C / 27.0°C | 中 | 3-hour precipitation category 0/9; type rain |",
		"| 2026-07-16 | 雷雨 | 30.0°C / 27.0°C | 中 | 3-hour precipitation category 0/9; type rain |",
		"| 2026-07-17 | 雷雨 | 28.0°C / 27.0°C | 中 | 3-hour precipitation category 0/9; type rain |",
	}
	openURL := "https://api.open-meteo.com/v1/forecast?latitude=31.2&models=ecmwf_ifs025"
	gfsURL := "https://www.7timer.info/bin/api.pl?lat=31.2&product=civil&output=json"
	results := []string{
		weatherFallbackRecord("open_meteo", "Open-Meteo", "retrieval_time=2026-07-10T22:47:50Z; forecast_initialization_time=not_provided", openURL, openRows),
		weatherFallbackRecord("seven_timer", "7Timer (NOAA/NCEP GFS)", "forecast_initialization_time=2026-07-10T12:00:00Z; retrieval_time=2026-07-10T22:47:51Z", gfsURL, gfsRows),
	}
	input := "请查询从2026-07-11起上海未来7天的天气，至少两个独立来源，逐日列出并给通勤建议和来源链接"
	got := buildStructuredReadOnlyFallback(input, results)
	for _, want := range []string{"上海未来7天天气", "逐日双源对比", openURL, gfsURL, "retrieval_time=", "forecast_initialization_time=", "周末户外", "工作日通勤", "日聚合", "约7.0°C"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "不能据此声称上午、下午、晚上或全天") {
		t.Fatalf("fallback must state the daily-granularity boundary: %s", got)
	}
	if got := buildStructuredReadOnlyFallback(input, results[:1]); got != "" {
		t.Fatalf("one source must not satisfy a two-source request: %s", got)
	}
	if got := buildStructuredReadOnlyFallback(input+"，并生成一个PPT文件", results); got != "" {
		t.Fatalf("artifact task must not be completed by a chat-only fallback: %s", got)
	}

	elderInput := input + `，对象是65岁膝关节不太好的老人，保存到 E:\work\weather.md`
	call, ok := buildDeterministicWeatherArtifactToolCall(elderInput, `E:\work\weather.md`, results, 2)
	if !ok || call.Name != "write_file" {
		t.Fatalf("complete weather evidence did not create deterministic artifact call: %#v", call)
	}
	content, _ := call.Arguments["content"].(string)
	for _, want := range []string{"老人/行动便利", "不给具体饮水量、药物或剂量建议", openURL, gfsURL} {
		if !strings.Contains(content, want) {
			t.Fatalf("elder weather artifact missing %q:\n%s", want, content)
		}
	}
	for _, unsafe := range []string{"藿香正气", "1500", "2000ml"} {
		if strings.Contains(content, unsafe) {
			t.Fatalf("deterministic weather artifact contained unsafe unsolicited advice %q: %s", unsafe, content)
		}
	}
	if !textArtifactEvidenceReady(results) {
		t.Fatal("two independent complete weather providers must open deterministic artifact delivery")
	}
}

func TestParseWeatherForecastToolResultRejectsFailedOrTruncatedCoverage(t *testing.T) {
	row := []string{"| 2026-07-11 | 雨 | 30.0°C / 25.0°C | 高 | probability 80% |"}
	result := weatherFallbackRecord("open_meteo", "Open-Meteo", "retrieval_time=2026-07-11T00:00:00Z", "https://api.open-meteo.com/v1/forecast", row)
	if _, ok := weatherartifact.ForecastProvider(result); ok {
		t.Fatal("declared 7/7 result with one row should be rejected")
	}
	failed := strings.Replace(result, `"succeeded":true`, `"succeeded":false`, 1)
	if _, ok := weatherartifact.ForecastProvider(failed); ok {
		t.Fatal("failed tool result should be rejected")
	}
}

func TestGenerateDirectlyCompletesFromStructuredWeatherEvidence(t *testing.T) {
	openRows := make([]string, 0, 7)
	gfsRows := make([]string, 0, 7)
	for day := 11; day <= 17; day++ {
		openRows = append(openRows, "| 2026-07-"+strconv.Itoa(day)+" | 雨 | 36.0°C / 27.0°C | 高 | max probability 70%; amount 2.0 mm |")
		gfsRows = append(gfsRows, "| 2026-07-"+strconv.Itoa(day)+" | 阴 | 30.0°C / 26.0°C | 中 | 3-hour precipitation category 2/9; type rain |")
	}
	openURL := "https://api.open-meteo.com/v1/forecast?latitude=31.2&models=ecmwf_ifs025"
	gfsURL := "https://www.7timer.info/bin/api.pl?lat=31.2&product=civil&output=json"
	stripHostRecord := func(value string) string {
		parts := strings.SplitN(value, "\n", 2)
		if len(parts) != 2 {
			t.Fatal("fixture missing host record separator")
		}
		return parts[1]
	}
	tool := structuredFallbackWeatherTool{
		openMeteo:  stripHostRecord(weatherFallbackRecord("open_meteo", "Open-Meteo", "retrieval_time=2026-07-10T22:47:50Z; forecast_initialization_time=not_provided", openURL, openRows)),
		sevenTimer: stripHostRecord(weatherFallbackRecord("seven_timer", "7Timer (NOAA/NCEP GFS)", "forecast_initialization_time=2026-07-10T12:00:00Z; retrieval_time=2026-07-10T22:47:51Z", gfsURL, gfsRows)),
	}

	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{"id": "call_open", "type": "function", "function": map[string]interface{}{"name": "weather_forecast", "arguments": `{"location":"上海","provider":"open_meteo","days":7,"start_date":"2026-07-11"}`}},
					map[string]interface{}{"id": "call_gfs", "type": "function", "function": map[string]interface{}{"name": "weather_forecast", "arguments": `{"location":"上海","provider":"seven_timer","days":7,"start_date":"2026-07-11"}`}},
				},
			},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{contentType: "application/json", body: string(body)}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 3)
	agent.maxTokens = 393216
	agent.modelConfig.MaxTokens = 393216
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(tool)

	ctx := context.WithValue(context.Background(), "task_id", "web_structured_direct-t1")
	input := "请联网查询从今天（2026-07-11）起上海未来7天的天气，至少核对两个独立来源，逐日列出最高最低温和降雨风险，并给周末户外、工作日通勤建议和来源链接。"
	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("Generate: %v\n%s", err, output)
	}
	if transport.requestCount() != 1 {
		t.Fatalf("structured direct delivery made %d model calls, want exactly 1", transport.requestCount())
	}
	for _, want := range []string{"逐日双源对比", "周末户外", "工作日通勤", openURL, gfsURL} {
		if !strings.Contains(output, want) {
			t.Fatalf("direct output missing %q:\n%s", want, output)
		}
	}
	assertRequestMaxTokens(t, transport, 4096)

	coldPath := filepath.Join(agent.workspace, ".openkitty", "logs", "web_structured_direct", "t1.json")
	coldData, err := os.ReadFile(coldPath)
	if err != nil {
		t.Fatalf("parallel cold transcript was not saved before direct delivery: %v", err)
	}
	if strings.Count(string(coldData), `"role": "tool"`) != 2 || !strings.Contains(string(coldData), "2026-07-17") {
		t.Fatalf("cold transcript lacks the two complete tool results: %s", coldData)
	}
}
