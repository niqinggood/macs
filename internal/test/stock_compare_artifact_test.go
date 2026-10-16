package app

import (
	"strings"
	"testing"

	stockartifact "mac/internal/artifacts/stock"
	"mac/internal/toolcontract"
	networktools "mac/internal/tools/network"
)

type stockFactsResult = networktools.StockFactsResult
type stockQuoteFacts = networktools.StockQuoteFacts
type stockHistoryFacts = networktools.StockHistoryFacts
type stockValuationFacts = networktools.StockValuationFacts
type stockReportFacts = networktools.StockReportFacts
type stockReportMetric = networktools.StockReportMetric

const (
	stockFactsProviderTencentMarket  = networktools.StockFactsProviderTencentMarket
	stockFactsProviderEastmoney      = networktools.StockFactsProviderEastmoney
	stockFactsProviderOfficialReport = networktools.StockFactsProviderOfficialReport
	stockFactsResultMarker           = networktools.StockFactsResultMarker
)

func renderStockFactsResult(result stockFactsResult) (string, error) {
	return networktools.RenderStockFactsResult(result)
}

func TestDeterministicStockComparisonArtifactPreservesRawCNYAndExactURLs(t *testing.T) {
	makeFacts := func(symbol, company string, close, revenue float64) []string {
		pe, pb, marketCap := 20.0, 4.0, 100000000000.0
		prior, yoy := revenue-1000000000, 5.0
		quote := &stockQuoteFacts{TradeDate: "2026-07-10", Close: &close}
		history := &stockHistoryFacts{Return5DPct: 1, Return20DPct: 2, Return60DPct: 3, MA5: close, MA20: close, MA60: close, Adjustment: "none", Observations: 80}
		valuation := &stockValuationFacts{PETTM: &pe, PB: &pb, MarketCap: &marketCap}
		report := &stockReportFacts{Period: "2025-12-31", Metrics: map[string]stockReportMetric{
			"revenue":             {Current: &revenue, Prior: &prior, YoYPct: &yoy, Unit: "CNY", Status: "observed"},
			"parent_net_profit":   {Unit: "CNY", Status: "not_found_after_pdf_extraction"},
			"operating_cash_flow": {Unit: "CNY", Status: "not_found_after_pdf_extraction"},
			"eps":                 {Unit: "CNY/share", Status: "not_found_after_pdf_extraction"},
			"roe":                 {Unit: "%", Status: "not_found_after_pdf_extraction"},
		}}
		base := stockFactsResult{Version: 1, Symbol: symbol, CompanyName: company, RetrievalTime: "2026-07-11T14:00:00Z", AsOf: "2026-07-10", Quote: quote, History: history, Valuation: valuation}
		providers := []stockFactsResult{base, base, base}
		providers[0].Provider, providers[0].SourceURL = stockFactsProviderTencentMarket, "https://web.ifzq.gtimg.cn/exact?x=1"
		providers[1].Provider, providers[1].SourceURL = stockFactsProviderEastmoney, "https://push2delay.eastmoney.com/exact?x=2"
		providers[2].Provider, providers[2].SourceURL, providers[2].Report = stockFactsProviderOfficialReport, "https://static.cninfo.com.cn/exact.pdf", report
		var results []string
		for _, facts := range providers {
			rendered, err := renderStockFactsResult(facts)
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, toolResultForFinalization(ToolCall{Name: "stock_facts"}, rendered, true))
		}
		return results
	}
	results := append(makeFacts("002594", "比亚迪", 90, 803964958000), makeFacts("300750", "宁德时代", 348.76, 423701834000)...)
	content, ok := stockartifact.BuildComparisonArtifact(`销量下滑15%，写入 E:\work\compare.md`, `E:\work\compare.md`, results, true)
	if !ok {
		t.Fatal("deterministic comparison did not activate")
	}
	for _, want := range []string{"803964958000 CNY", "423701834000 CNY", "https://push2delay.eastmoney.com/exact?x=2", "15%", "002594", "300750"} {
		if !strings.Contains(content, want) {
			t.Fatalf("artifact missing %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"亿元", "万元", "北京时间"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("artifact contains forbidden manual conversion/time %q", forbidden)
		}
	}
	call, ok := stockartifact.BuildToolCall(`销量下滑15%，写入 E:\work\compare.md`, `E:\work\compare.md`, results, 4, true)
	if !ok || call.Name != "write_file" {
		t.Fatalf("deterministic write call was not built: %#v", call)
	}
	tool := NewWriteFileTool(t.TempDir())
	if err := toolcontract.ValidateArguments(tool.Parameters(), call.Arguments); err != nil {
		t.Fatalf("host-generated write_file arguments violate the live schema: %v", err)
	}
}
