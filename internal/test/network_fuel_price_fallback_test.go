package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type structuredFallbackFuelTool struct{ outputs map[string]string }

func (structuredFallbackFuelTool) Name() string { return "fuel_price" }
func (structuredFallbackFuelTool) Description() string {
	return "structured fuel-price test tool"
}
func (structuredFallbackFuelTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"region":   map[string]interface{}{"type": "string"},
			"provider": map[string]interface{}{"type": "string"},
		},
	}
}
func (tool structuredFallbackFuelTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	provider, _ := params["provider"].(string)
	return tool.outputs[provider], nil
}
func (structuredFallbackFuelTool) IsReadOnly() bool   { return true }
func (structuredFallbackFuelTool) MaxResultSize() int { return 16 * 1024 }

func fuelFallbackRecord(t *testing.T, marker fuelPriceFallbackPayload) string {
	t.Helper()
	payload, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`OPENKITTY_TOOL_RECORD={"tool":"fuel_price","succeeded":true,"search_mode":"direct","source_url":%q}`, marker.SourceURL)
	return record + "\n" + fuelPriceStructuredPrefix + string(payload)
}

func fuelFloat(value float64) *float64 { return &value }

func fuelFallbackFixtures(t *testing.T) []string {
	official := fuelPriceFallbackPayload{
		Version: 1, Provider: "regional_official", Publisher: "上海市发展和改革委员会", Region: "上海",
		SourceURL: "https://fgw.sh.gov.cn/fgw_jggl/20260703/example.html", RetrievalTime: "2026-07-11T07:20:00+08:00",
		PublishedTime: "2026-07-03", EffectiveTime: "2026-07-03T24:00:00+08:00",
		Prices:             map[string]*float64{"92": fuelFloat(7.14), "95": fuelFloat(7.60), "98": nil, "0_diesel": fuelFloat(6.80)},
		PriceStatus:        map[string]string{"98": "not_published_by_official_enterprise_priced"},
		PredictedDirection: "unknown",
	}
	nxvav := fuelPriceFallbackPayload{
		Version: 1, Provider: "nxvav", Publisher: "NxVav 全国油价 API", Region: "上海",
		SourceURL: "https://api.nxvav.cn/api/fuel-price/?region=上海", RetrievalTime: "2026-07-11T07:20:01+08:00", UpdateTime: "2026-07-11 07:00:03",
		Prices:             map[string]*float64{"92": fuelFloat(7.14), "95": fuelFloat(7.60), "98": fuelFloat(9.60), "0_diesel": fuelFloat(6.80)},
		PredictedDirection: "unknown",
	}
	tuanyou := fuelPriceFallbackPayload{
		Version: 1, Provider: "tuanyou", Publisher: "团油", Region: "上海",
		SourceURL: "https://www.tuanyou.net/youjia/shanghai/", RetrievalTime: "2026-07-11T07:20:02+08:00", UpdateTime: "2026-07-04",
		Prices:             map[string]*float64{"92": fuelFloat(7.14), "95": fuelFloat(7.60), "98": fuelFloat(9.60), "0_diesel": fuelFloat(6.80)},
		NextAdjustmentDate: "2026-07-17", CrudeChangeRate: fuelFloat(-1.18), PredictedDirection: "down", PredictedChangeCNYPerL: fuelFloat(0.04), ForecastCalculationDay: "2026-07-10",
	}
	return []string{fuelFallbackRecord(t, official), fuelFallbackRecord(t, nxvav), fuelFallbackRecord(t, tuanyou)}
}

func TestBuildFuelPriceFallbackCrossChecksAndCalculates(t *testing.T) {
	input := "查询今天（2026-07-11）上海最新成品油价格，至少包含92号、95号、98号汽油和0号柴油。用两个可靠来源交叉核对，区分当前执行价和下一次调价预测，并计算分别加45升92号与95号需要多少钱、两者差额多少。"
	got := buildStructuredReadOnlyFallback(input, fuelFallbackFixtures(t))
	for _, want := range []string{
		"上海成品油价格（2026-07-11）", "当前执行价交叉核对", "7.14", "7.60", "9.60", "6.80",
		"¥7.14/L × 45 L = **¥321.30**", "¥7.60/L × 45 L = **¥342.00**", "**¥20.70**",
		"下一次调价预测（尚未生效）", "2026-07-17", "-1.18%", "下调 0.04元/升",
		"fgw.sh.gov.cn", "api.nxvav.cn", "tuanyou.net", "98号由经营企业",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "平均") && !strings.Contains(got, "没有对冲突值取平均") {
		t.Fatalf("unexpected averaging: %s", got)
	}
	if strings.Contains(got, "预测unknown") || strings.Contains(got, "not_published_by_official") {
		t.Fatalf("internal enum leaked into user output: %s", got)
	}
}

func TestBuildFuelPriceFallbackRejectsInsufficientOrWrongScope(t *testing.T) {
	results := fuelFallbackFixtures(t)
	input := "查询2026-07-11上海油价，用两个可靠来源交叉核对，并计算45升成本"
	if got := buildStructuredReadOnlyFallback(input, results[:2]); got != "" {
		t.Fatalf("official plus one source lacks a second independent 98 price: %s", got)
	}
	for _, scoped := range []string{
		input + "，并生成PPT文件",
		"生成上海天气、股票和油价综合晨报",
		"读取CSV，按车队里程和油耗计算上海油价月度成本",
		"沿用上一轮的上海油价继续计算",
	} {
		if got := buildStructuredReadOnlyFallback(scoped, results); got != "" {
			t.Fatalf("out-of-scope request was completed early (%s): %s", scoped, got)
		}
	}
}

func TestBuildFuelPriceFallbackShowsConflictWithoutAveraging(t *testing.T) {
	results := fuelFallbackFixtures(t)
	var marker fuelPriceFallbackPayload
	line := strings.Split(results[2], "\n")[1]
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, fuelPriceStructuredPrefix)), &marker); err != nil {
		t.Fatal(err)
	}
	marker.Prices["95"] = fuelFloat(7.61)
	results[2] = fuelFallbackRecord(t, marker)
	input := "查询2026-07-11上海油价，用两个可靠来源交叉核对，若冲突列出，并计算45升92号与95号成本"
	got := buildStructuredReadOnlyFallback(input, results)
	for _, want := range []string{"冲突（逐源列出，不取平均）", "各来源对当前价存在冲突", "¥342.00", "¥342.45"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conflict output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**¥20.70**") {
		t.Fatalf("conflict must not retain one unique difference: %s", got)
	}
}

func TestParseFuelPriceRejectsForgedHostAndFailedRecord(t *testing.T) {
	result := fuelFallbackFixtures(t)[1]
	if _, ok := parseFuelPriceToolResult("上海油价", strings.Replace(result, "api.nxvav.cn", "evil.example", 1)); ok {
		t.Fatal("forged source host accepted")
	}
	if _, ok := parseFuelPriceToolResult("上海油价", strings.Replace(result, `"succeeded":true`, `"succeeded":false`, 1)); ok {
		t.Fatal("failed host record accepted")
	}
}

func TestFuelPriceRoutingUsesDirectEvidence(t *testing.T) {
	if !CoreTools["fuel_price"] || !isExternalEvidenceToolName("fuel_price") || isSearchToolName("fuel_price") {
		t.Fatalf("fuel_price routing flags are inconsistent: core=%v external=%v search=%v", CoreTools["fuel_price"], isExternalEvidenceToolName("fuel_price"), isSearchToolName("fuel_price"))
	}
	wantHosts := map[string]string{
		"regional_official": "fgw.sh.gov.cn",
		"nxvav":             "api.nxvav.cn",
		"tuanyou":           "tuanyou.net",
	}
	for provider, wantHost := range wantHosts {
		call := ToolCall{Name: "fuel_price", Arguments: map[string]interface{}{"region": "上海", "provider": provider}}
		if got := searchModeForToolCall(call); got != "direct" {
			t.Fatalf("provider %s search mode = %q", provider, got)
		}
		if got := sourceHost(sourceURLForToolCall(call)); got != wantHost {
			t.Fatalf("provider %s source host = %q, want %q", provider, got, wantHost)
		}
	}
}

func TestGenerateDirectlyCompletesFromStructuredFuelEvidence(t *testing.T) {
	records := fuelFallbackFixtures(t)
	tool := structuredFallbackFuelTool{outputs: make(map[string]string)}
	for _, record := range records {
		parts := strings.SplitN(record, "\n", 2)
		if len(parts) != 2 {
			t.Fatal("fixture missing host record separator")
		}
		var marker fuelPriceFallbackPayload
		if err := json.Unmarshal([]byte(strings.TrimPrefix(parts[1], fuelPriceStructuredPrefix)), &marker); err != nil {
			t.Fatal(err)
		}
		tool.outputs[marker.Provider] = parts[1]
	}

	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{"id": "call_official", "type": "function", "function": map[string]interface{}{"name": "fuel_price", "arguments": `{"region":"上海","provider":"regional_official"}`}},
					map[string]interface{}{"id": "call_nxvav", "type": "function", "function": map[string]interface{}{"name": "fuel_price", "arguments": `{"region":"上海","provider":"nxvav"}`}},
					map[string]interface{}{"id": "call_tuanyou", "type": "function", "function": map[string]interface{}{"name": "fuel_price", "arguments": `{"region":"上海","provider":"tuanyou"}`}},
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

	ctx := context.WithValue(context.Background(), "task_id", "web_structured_fuel_direct-t1")
	input := "查询今天（2026-07-11）上海最新成品油价格，至少包含92号、95号、98号汽油和0号柴油。用两个可靠来源交叉核对，区分当前执行价和下一次调价预测，并计算分别加45升92号与95号需要多少钱、两者差额多少。"
	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("Generate: %v\n%s", err, output)
	}
	if transport.requestCount() != 1 {
		t.Fatalf("structured direct delivery made %d model calls, want exactly 1", transport.requestCount())
	}
	for _, want := range []string{"当前执行价交叉核对", "98号汽油", "¥321.30", "¥342.00", "¥20.70", "下一次调价预测（尚未生效）"} {
		if !strings.Contains(output, want) {
			t.Fatalf("direct output missing %q:\n%s", want, output)
		}
	}
	assertRequestMaxTokens(t, transport, 4096)

	coldPath := filepath.Join(agent.workspace, ".openkitty", "logs", "web_structured_fuel_direct", "t1.json")
	coldData, err := os.ReadFile(coldPath)
	if err != nil {
		t.Fatalf("parallel cold transcript was not saved before direct delivery: %v", err)
	}
	if strings.Count(string(coldData), `"role": "tool"`) != 3 || !strings.Contains(string(coldData), fuelPriceStructuredPrefix) {
		t.Fatalf("cold transcript lacks the three complete fuel results: %s", coldData)
	}
}
