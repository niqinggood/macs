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

type structuredFallbackStockTool struct{ outputs map[string]string }

func (structuredFallbackStockTool) Name() string { return "stock_facts" }
func (structuredFallbackStockTool) Description() string {
	return "structured stock-facts test tool"
}
func (structuredFallbackStockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"symbol":   map[string]interface{}{"type": "string"},
			"provider": map[string]interface{}{"type": "string"},
			"as_of":    map[string]interface{}{"type": "string"},
		},
	}
}
func (tool structuredFallbackStockTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	provider, _ := params["provider"].(string)
	return tool.outputs[provider], nil
}
func (structuredFallbackStockTool) IsReadOnly() bool   { return true }
func (structuredFallbackStockTool) MaxResultSize() int { return 24 * 1024 }

func stockFloat(value float64) *float64 { return &value }
func stockBool(value bool) *bool        { return &value }

func stockFallbackRecord(t *testing.T, marker stockFallbackPayload, recordSource string) string {
	t.Helper()
	payload, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`OPENKITTY_TOOL_RECORD={"tool":"stock_facts","succeeded":true,"search_mode":"direct","source_url":%q}`, recordSource) +
		"\n" + stockFactsStructuredPrefix + string(payload)
}

func stockFallbackFixtures(t *testing.T) []string {
	quote := &stockFallbackQuote{
		TradeDate: "2026-07-10", QuoteTime: "2026-07-10T16:14:45+08:00", MarketState: "closed", PriceKind: "latest_completed_daily_bar",
		Open: stockFloat(1182.20), High: stockFloat(1204.98), Low: stockFloat(1170.28), Close: stockFloat(1204.98), PrevClose: stockFloat(1182.19),
		Volume: stockFloat(5221300), Amount: stockFloat(6223343642),
	}
	tencent := stockFallbackPayload{
		Version: 1, Provider: "tencent_market", Symbol: "600519", Market: "cn_a", Exchange: "SSE", CompanyName: "贵州茅台", Currency: "CNY",
		Publisher: "腾讯行情", SourceURL: "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=sh600519,day,,,80,none", RetrievalTime: "2026-07-11T00:10:00Z", AsOf: "2026-07-11",
		Quote: quote,
		History: &stockFallbackHistory{
			Adjustment: "none", TotalReturnIncludesDividends: false, Observations: 80, FirstDate: "2026-03-18", LastDate: "2026-07-10",
			Return5DPct: stockFloat(0.8816), Return20DPct: stockFloat(-5.7873), Return60DPct: stockFloat(-16.5127),
			MA5: stockFloat(1196.436), MA20: stockFloat(1213.952), MA60: stockFloat(1302.7148),
			Range20Low: stockFloat(1170.28), Range20High: stockFloat(1282.00), Range60Low: stockFloat(1170.28), Range60High: stockFloat(1450.00),
		},
		Valuation: &stockFallbackValuation{AsOf: "2026-07-10", PEDynamic: stockFloat(13.82), PEStatic: stockFloat(18.30), PETTM: stockFloat(18.21), PB: stockFloat(6.47), MarketCap: stockFloat(1506323327572.98)},
	}
	eastQuote := *quote
	eastQuote.PriceKind = "latest_close"
	eastmoney := stockFallbackPayload{
		Version: 1, Provider: "eastmoney_market", Symbol: "600519", Market: "cn_a", Exchange: "SSE", CompanyName: "贵州茅台", Currency: "CNY",
		Publisher: "东方财富行情", SourceURL: "https://push2.eastmoney.com/api/qt/stock/get?secid=1.600519", RetrievalTime: "2026-07-11T00:10:00Z", AsOf: "2026-07-11",
		Quote:     &eastQuote,
		Valuation: &stockFallbackValuation{AsOf: "2026-07-10", PEDynamic: stockFloat(13.82), PEStatic: stockFloat(18.30), PETTM: stockFloat(18.21), PB: stockFloat(6.39), MarketCap: stockFloat(1506323327572.98)},
	}
	officialURL := "https://static.cninfo.com.cn/finalpage/2026-04-25/1225187851.PDF"
	official := stockFallbackPayload{
		Version: 1, Provider: "official_report", Symbol: "600519", Market: "cn_a", Exchange: "SSE", CompanyName: "贵州茅台", Currency: "CNY",
		Publisher: "巨潮资讯法定披露", SourceURL: officialURL, RetrievalTime: "2026-07-11T00:10:01Z", AsOf: "2026-03-31",
		Report: &stockFallbackReport{
			Title: "贵州茅台2026年第一季度报告", Type: "first_quarter", Period: "2026-03-31", AnnouncementDate: "2026-04-25", Unaudited: stockBool(true), SourceURL: officialURL,
			Metrics: map[string]stockFallbackMetric{
				"revenue":             {Current: stockFloat(53909252220.51), Prior: stockFloat(50600957885.78), YoyPct: stockFloat(6.54), Unit: "CNY", Status: "observed"},
				"parent_net_profit":   {Current: stockFloat(27242512886.45), Prior: stockFloat(26847474238.76), YoyPct: stockFloat(1.47), Unit: "CNY", Status: "observed"},
				"operating_cash_flow": {Current: stockFloat(26909891269.13), Prior: stockFloat(8809195646.38), YoyPct: stockFloat(205.48), Unit: "CNY", Status: "observed"},
				"eps":                 {Current: stockFloat(21.76), Prior: stockFloat(21.38), YoyPct: stockFloat(1.78), Unit: "CNY/share", Status: "observed"},
				"roe":                 {Current: stockFloat(10.57), Prior: stockFloat(10.92), YoyPct: stockFloat(-0.35), Unit: "%", Status: "observed"},
			},
		},
		AnnouncementQueryStatus: "observed",
		Announcements: []stockFallbackAnnouncement{
			{Title: "贵州茅台2025年年度权益分派实施公告", Date: "2026-06-22", URL: "https://static.cninfo.com.cn/finalpage/2026-06-22/1225379934.PDF"},
			{Title: "贵州茅台第五届董事会2026年度第一次会议决议公告", Date: "2026-06-12", URL: "https://static.cninfo.com.cn/finalpage/2026-06-12/1225366262.PDF"},
		},
	}
	return []string{
		stockFallbackRecord(t, tencent, "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=sh600519,day,,,80,none"),
		stockFallbackRecord(t, eastmoney, "https://push2.eastmoney.com/api/qt/stock/get?secid=1.600519"),
		stockFallbackRecord(t, official, "https://www.cninfo.com.cn/new/disclosure/stock?stockCode=600519"),
	}
}

func TestBuildStockFactsFallbackProducesGroundedAnalysis(t *testing.T) {
	input := "请对A股贵州茅台（600519）做一份截至今天（2026-07-11）的事实型分析。必须核对最新可得收盘价及对应交易日、近期走势、最近一期财务数据、估值口径、核心经营驱动和至少5项风险。把事实、你的推断和未知项分开，不给确定性涨跌预测。"
	got := buildStructuredReadOnlyFallback(input, stockFallbackFixtures(t))
	for _, want := range []string{
		"贵州茅台（600519）", "2026-07-10", "1204.98", "未复权价格变动", "+0.88% / -5.79% / -16.51%",
		"PE TTM", "18.21", "6.47", "6.39", "供应商值如有差异", "贵州茅台2026年第一季度报告",
		"539.09亿元", "272.43亿元", "269.10亿元", "21.76元/股", "未经审计", "风险观察清单",
		"-0.35个百分点",
		"通用框架", "未知项与边界", "不含现金分红", "不据标题推导事件", "不平均",
		"web.ifzq.gtimg.cn", "push2.eastmoney.com", "static.cninfo.com.cn",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stock fallback missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n1. **") != 1 || !strings.Contains(got, "\n6. **") {
		t.Fatalf("stock fallback must provide at least six explicitly grounded risk items: %s", got)
	}
	for _, forbidden := range []string{"长上影", "显著高于2022", "确定会上涨", "建议买入"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unsupported claim %q leaked: %s", forbidden, got)
		}
	}
}

func TestBuildStockFactsFallbackUnderstandsTodayWithoutExplicitISODate(t *testing.T) {
	got := buildStructuredReadOnlyFallback("今天请分析A股贵州茅台600519的最新行情、财报、估值和风险", stockFallbackFixtures(t))
	if !strings.Contains(got, "截至 2026-07-11") || !strings.Contains(got, "贵州茅台2026年第一季度报告") {
		t.Fatalf("natural today request did not use retrieval date as its cutoff: %s", got)
	}
}

func TestBuildStockFactsFallbackRejectsIncompleteConflictAndWrongScope(t *testing.T) {
	results := stockFallbackFixtures(t)
	input := "分析A股贵州茅台600519截至2026-07-11的行情、财务、估值和风险"
	if got := buildStructuredReadOnlyFallback(input, results[:2]); got != "" {
		t.Fatalf("missing official report completed early: %s", got)
	}
	var east stockFallbackPayload
	line := strings.Split(results[1], "\n")[1]
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, stockFactsStructuredPrefix)), &east); err != nil {
		t.Fatal(err)
	}
	east.Quote.Close = stockFloat(1205.50)
	conflicted := append([]string(nil), results...)
	conflicted[1] = stockFallbackRecord(t, east, "https://push2.eastmoney.com/api/qt/stock/get?secid=1.600519")
	if got := buildStructuredReadOnlyFallback(input, conflicted); got != "" {
		t.Fatalf("conflicting close prices completed early: %s", got)
	}
	east.Quote.Close = stockFloat(1204.98)
	east.Quote.Open = stockFloat(1183.20)
	conflicted[1] = stockFallbackRecord(t, east, "https://push2.eastmoney.com/api/qt/stock/get?secid=1.600519")
	if got := buildStructuredReadOnlyFallback(input, conflicted); got != "" {
		t.Fatalf("conflicting OHLC values completed early: %s", got)
	}
	for _, scoped := range []string{
		"对比贵州茅台600519和比亚迪002594的股票估值",
		"做天气、油价、贵州茅台600519行情和新闻综合晨报",
		"沿用上一轮贵州茅台600519数据继续分析股票",
		"分析贵州茅台600519股票并生成PPT文件",
		"分析招商银行600036的净息差、资产质量和股票风险",
		"分析贵州茅台600519以及五粮液的股票风险",
		"分析贵州茅台600519的行业排名和资金流",
		"把贵州茅台600519股票分析做成幻灯片",
	} {
		if got := buildStructuredReadOnlyFallback(scoped, results); got != "" {
			t.Fatalf("out-of-scope stock request completed early (%s): %s", scoped, got)
		}
	}
}

func TestParseStockFactsRejectsFailedOrForgedEvidence(t *testing.T) {
	result := stockFallbackFixtures(t)[0]
	if _, ok := parseStockFactsToolResult("分析600519股票", "600519", strings.Replace(result, "web.ifzq.gtimg.cn", "evil.example", 1)); ok {
		t.Fatal("forged Tencent host accepted")
	}
	if _, ok := parseStockFactsToolResult("分析600519股票", "600519", strings.Replace(result, `"succeeded":true`, `"succeeded":false`, 1)); ok {
		t.Fatal("failed host record accepted")
	}
}

func TestStockFactsRoutingIsDirectExternalEvidence(t *testing.T) {
	if !CoreTools["stock_facts"] || !isExternalEvidenceToolName("stock_facts") || isSearchToolName("stock_facts") {
		t.Fatalf("stock_facts routing flags are inconsistent")
	}
	wantHosts := map[string]string{"tencent_market": "web.ifzq.gtimg.cn", "eastmoney_market": "push2delay.eastmoney.com", "official_report": "cninfo.com.cn"}
	for provider, wantHost := range wantHosts {
		call := ToolCall{Name: "stock_facts", Arguments: map[string]interface{}{"symbol": "600519", "provider": provider, "as_of": "2026-07-11"}}
		if got := searchModeForToolCall(call); got != "direct" {
			t.Fatalf("provider %s mode = %q", provider, got)
		}
		if got := sourceHost(sourceURLForToolCall(call)); got != wantHost {
			t.Fatalf("provider %s source host = %q, want %q", provider, got, wantHost)
		}
	}
}

func TestGenerateDirectlyCompletesFromStructuredStockEvidence(t *testing.T) {
	tool := structuredFallbackStockTool{outputs: make(map[string]string)}
	for _, record := range stockFallbackFixtures(t) {
		parts := strings.SplitN(record, "\n", 2)
		if len(parts) != 2 {
			t.Fatal("fixture missing host record separator")
		}
		var marker stockFallbackPayload
		if err := json.Unmarshal([]byte(strings.TrimPrefix(parts[1], stockFactsStructuredPrefix)), &marker); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(marker)
		if err != nil {
			t.Fatal(err)
		}
		var realResult stockFactsResult
		if err := json.Unmarshal(encoded, &realResult); err != nil {
			t.Fatalf("fallback fixture does not match the real stock_facts schema: %v", err)
		}
		if realResult.Valuation != nil {
			realResult.Valuation.MetricStatus = map[string]string{
				"pe_dynamic": "observed", "pe_static": "observed", "pe_ttm": "observed", "pb": "observed", "market_cap": "observed",
			}
		}
		rendered, err := renderStockFactsResult(realResult)
		if err != nil {
			t.Fatalf("real stock_facts renderer rejected %s fixture: %v", marker.Provider, err)
		}
		tool.outputs[marker.Provider] = rendered
	}
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{"id": "call_tencent", "type": "function", "function": map[string]interface{}{"name": "stock_facts", "arguments": `{"symbol":"600519","provider":"tencent_market","as_of":"2026-07-11"}`}},
					map[string]interface{}{"id": "call_eastmoney", "type": "function", "function": map[string]interface{}{"name": "stock_facts", "arguments": `{"symbol":"600519","provider":"eastmoney_market","as_of":"2026-07-11"}`}},
					map[string]interface{}{"id": "call_official", "type": "function", "function": map[string]interface{}{"name": "stock_facts", "arguments": `{"symbol":"600519","provider":"official_report","as_of":"2026-07-11"}`}},
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

	ctx := context.WithValue(context.Background(), "task_id", "web_structured_stock_direct-t1")
	input := "请对A股贵州茅台（600519）做一份截至今天（2026-07-11）的事实型分析。必须核对最新可得收盘价及对应交易日、近期走势、最近一期财务数据、估值口径、核心经营驱动和至少5项风险。行情、财报、公告分别优先使用交易所/公司公告/权威行情源，并给出直接链接与截至时间。把事实、你的推断和未知项分开，不给确定性涨跌预测。"
	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("Generate: %v\n%s", err, output)
	}
	if transport.requestCount() != 1 {
		t.Fatalf("structured stock delivery made %d model calls, want exactly 1", transport.requestCount())
	}
	for _, want := range []string{"贵州茅台（600519）", "1204.98", "贵州茅台2026年第一季度报告", "风险观察清单", "未知项与边界"} {
		if !strings.Contains(output, want) {
			t.Fatalf("direct stock output missing %q:\n%s", want, output)
		}
	}
	assertRequestMaxTokens(t, transport, 4096)

	coldPath := filepath.Join(agent.workspace, ".openkitty", "logs", "web_structured_stock_direct", "t1.json")
	coldData, err := os.ReadFile(coldPath)
	if err != nil {
		t.Fatalf("parallel cold transcript was not saved before direct delivery: %v", err)
	}
	if strings.Count(string(coldData), `"role": "tool"`) != 3 || strings.Count(string(coldData), stockFactsStructuredPrefix) != 3 {
		t.Fatalf("cold transcript lacks three complete stock facts results: %s", coldData)
	}
}
