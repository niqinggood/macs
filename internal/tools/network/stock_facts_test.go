package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var stockFactsTestNow = time.Date(2026, 7, 11, 8, 0, 0, 0, stockFactsShanghaiZone)

func TestStockFactsLiveEndToEnd(t *testing.T) {
	if os.Getenv("OPENKITTY_LIVE_STOCK_FACTS") != "1" {
		t.Skip("set OPENKITTY_LIVE_STOCK_FACTS=1 to exercise public market/disclosure endpoints")
	}
	tool := NewStockFactsTool()
	tool.now = func() time.Time { return stockFactsTestNow }
	tool.requestTimeout = 15 * time.Second
	for _, provider := range []string{stockFactsProviderTencentMarket, stockFactsProviderEastmoney, stockFactsProviderOfficialReport} {
		params := map[string]interface{}{"symbol": "600519", "provider": provider, "as_of": "2026-07-11", "lookback_days": 80}
		output, err := tool.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("live %s: %v", provider, err)
		}
		marker, size := parseStockFactsTestMarker(t, output)
		quoteJSON, _ := json.Marshal(marker.Quote)
		t.Logf("%s source=%s marker_bytes=%d company=%q exchange=%s quote=%s", provider, marker.SourceURL, size, marker.CompanyName, marker.Exchange, quoteJSON)
		if marker.Provider != provider || marker.Symbol != "600519" {
			t.Fatalf("live marker mismatch: provider=%q symbol=%q", marker.Provider, marker.Symbol)
		}
	}
}

func TestStockFactsTencentMarketTwoGenericSymbols(t *testing.T) {
	tests := []struct {
		symbol, name, prefix, exchange string
	}{
		{"600519", "贵州茅台", "sh", "SSE"},
		{"002594", "比亚迪", "sz", "SZSE"},
	}
	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			server := newTencentStockFactsServer(t, tt.symbol, tt.name, tt.prefix, false)
			defer server.Close()
			tool := newStockFactsTestTool(server.Client(), server.URL)
			output, err := tool.Execute(context.Background(), map[string]interface{}{
				"symbol": tt.symbol, "provider": stockFactsProviderTencentMarket,
				"as_of": "2026-07-11", "lookback_days": 80,
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !strings.HasPrefix(output, stockFactsResultMarker) {
				t.Fatalf("structured marker is not the first output line: %q", output[:min(len(output), 80)])
			}
			result, markerLen := parseStockFactsTestMarker(t, output)
			if markerLen > stockFactsMaxMarkerSize || result.Symbol != tt.symbol || result.CompanyName != tt.name || result.Exchange != tt.exchange {
				t.Fatalf("unexpected marker (%d bytes): %#v", markerLen, result)
			}
			if result.History == nil || result.History.Adjustment != "none" || result.History.TotalReturnIncludesDividends || result.History.Observations != 80 {
				t.Fatalf("unadjusted history contract lost: %#v", result.History)
			}
			want5 := roundedStockFact((179.0/174.0-1)*100, 4)
			if math.Abs(result.History.Return5DPct-want5) > 0.00001 || result.History.MA60 <= 0 || result.History.Range60Low <= 0 {
				t.Fatalf("history calculations are wrong: %#v", result.History)
			}
			if result.Quote == nil || result.Quote.TradeDate != "2026-07-10" || result.Quote.PriceKind != "latest_completed_daily_bar" || result.Quote.Close == nil || *result.Quote.Close != 179 {
				t.Fatalf("latest completed quote is wrong: %#v", result.Quote)
			}
			if result.Valuation == nil || valueOrZero(result.Valuation.PETTM) != 20 || valueOrZero(result.Valuation.PB) != 5 || valueOrZero(result.Valuation.PEDynamic) != 18 || valueOrZero(result.Valuation.PEStatic) != 19 {
				t.Fatalf("Tencent qt valuation mapping is wrong: %#v", result.Valuation)
			}
			if !strings.Contains(output, "Cash dividends are NOT included") || !strings.Contains(result.SourceURL, "none") {
				t.Fatalf("price-return boundary/source missing: %s", output)
			}
		})
	}
}

func TestStockFactsQualifiedIndexUsesExplicitExchange(t *testing.T) {
	server := newTencentStockFactsServer(t, "000001", "上证指数", "sh", false)
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "sh000001", "provider": stockFactsProviderTencentMarket,
		"as_of": "2026-07-11", "lookback_days": 80,
	})
	if err != nil {
		t.Fatalf("Execute qualified index: %v", err)
	}
	result, _ := parseStockFactsTestMarker(t, output)
	if result.Symbol != "000001" || result.Exchange != "SSE" || result.CompanyName != "上证指数" {
		t.Fatalf("qualified index resolved to the wrong target: %#v", result)
	}

	_, barePrefix, bareSecID, _, _, err := stockFactsExchange("000001")
	if err != nil || barePrefix != "sz" || bareSecID != "0.000001" {
		t.Fatalf("bare stock compatibility changed: prefix=%q secid=%q err=%v", barePrefix, bareSecID, err)
	}
	_, qualifiedPrefix, qualifiedSecID, _, _, err := stockFactsExchange("000001", "sh")
	if err != nil || qualifiedPrefix != "sh" || qualifiedSecID != "1.000001" {
		t.Fatalf("qualified index routing is wrong: prefix=%q secid=%q err=%v", qualifiedPrefix, qualifiedSecID, err)
	}
}

func TestStockFactsNormalizesUnsupportedLookbackAndExplainsWeekendCutoff(t *testing.T) {
	server := newTencentStockFactsServer(t, "000001", "上证指数", "sh", false)
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "sh000001", "provider": stockFactsProviderTencentMarket,
		"as_of": "2026-07-11", "lookback_days": 5,
	})
	if err != nil {
		t.Fatalf("Execute should normalize a harmless lookback mismatch: %v", err)
	}
	result, _ := parseStockFactsTestMarker(t, output)
	if result.RequestedAsOf != "2026-07-11" || result.AsOf != "2026-07-10" {
		t.Fatalf("cutoff/trading-date provenance is wrong: %#v", result)
	}
	if result.History == nil || result.History.Observations != 80 {
		t.Fatalf("unsupported lookback did not fall back to 80: %#v", result.History)
	}
	for _, want := range []string{
		"Requested cutoff: 2026-07-11 (Saturday)",
		"the requested cutoff is a weekend",
		"do not label the weekend as a missing session",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("calendar provenance note missing %q:\n%s", want, output)
		}
	}
}

func TestStockFactsEastmoneyQualifiedIndexUsesExplicitExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eastmoney" || r.URL.Query().Get("secid") != "1.000688" {
			http.Error(w, "wrong qualified index target", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"rc":0,"data":{"f43":17900,"f44":18000,"f45":17500,"f46":17600,"f47":12345,"f48":987654321,"f57":"000688","f58":"科创50","f60":17800,"f86":1783670400,"f116":2200000000000,"f162":0,"f163":0,"f164":0,"f167":0}}`)
	}))
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "sh000688", "provider": stockFactsProviderEastmoney, "as_of": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("Execute qualified Eastmoney index: %v", err)
	}
	result, _ := parseStockFactsTestMarker(t, output)
	if result.Symbol != "000688" || result.Exchange != "SSE" || result.CompanyName != "科创50" {
		t.Fatalf("qualified Eastmoney index resolved to the wrong target: %#v", result)
	}
}

func TestStockFactsEastmoneyKeepsProviderValuationLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eastmoney" || r.URL.Query().Get("secid") != "1.600519" {
			http.Error(w, "wrong target", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"rc":0,"data":{"f43":17900,"f44":18000,"f45":17500,"f46":17600,"f47":12345,"f48":987654321,"f57":"600519","f58":"贵州茅台","f60":17800,"f86":1783670400,"f116":2200000000000,"f162":1382,"f163":1830,"f164":1821,"f167":639}}`)
	}))
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderEastmoney, "as_of": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result, _ := parseStockFactsTestMarker(t, output)
	if result.Exchange != "SSE" || result.Quote == nil || valueOrZero(result.Quote.Close) != 179 || result.Valuation == nil {
		t.Fatalf("unexpected Eastmoney result: %#v", result)
	}
	if valueOrZero(result.Valuation.PEDynamic) != 13.82 || valueOrZero(result.Valuation.PEStatic) != 18.30 || valueOrZero(result.Valuation.PETTM) != 18.21 || valueOrZero(result.Valuation.PB) != 6.39 {
		t.Fatalf("Eastmoney provider field scaling changed: %#v", result.Valuation)
	}
	if !strings.Contains(output, "Preserve this provider's labels; do not average") {
		t.Fatalf("valuation provenance boundary missing: %s", output)
	}
}

func TestParseStockReportMetricsExtractedTextFixture(t *testing.T) {
	metrics, err := parseStockReportMetrics(stockReportTextFixture("600519", "贵州茅台"))
	if err != nil {
		t.Fatalf("parseStockReportMetrics: %v", err)
	}
	wants := map[string][3]float64{
		"revenue":             {100000, 80000, 25},
		"parent_net_profit":   {50000, 40000, 25},
		"operating_cash_flow": {60000, 30000, 100},
		"eps":                 {2, 1.5, 33.33},
		"roe":                 {10, 8, 2},
	}
	for key, want := range wants {
		metric := metrics[key]
		if valueOrZero(metric.Current) != want[0] || valueOrZero(metric.Prior) != want[1] || valueOrZero(metric.YoYPct) != want[2] || metric.Status != "observed" {
			t.Errorf("%s = %#v, want %v", key, metric, want)
		}
	}
	if _, err := parseStockReportMetrics("600519 营业收入 1 1 0% 归属于上市公司股东的净利润 1 1 0%"); err == nil || !strings.Contains(err.Error(), "operating_cash_flow") {
		t.Fatalf("missing mandatory report fields should fail, got %v", err)
	}
}

func TestParseStockReportMetricsBareYoYAndROEDecrease(t *testing.T) {
	text := `600519 贵州茅台 2026年第一季度报告
营业收入（元） 100.00 93.86 6 .54
归属于上市公司股东的净利润（元） 50.00 45.00 11.11
经营活动产生的现金流量净额（元） 26909891269.13 8809195646.38 2 05.48
基本每股收益（元/股） 2.00 1.50 33.33
加权平均净资产收益率 10.57 1 0.92 减少0 .35个百分点`
	metrics, err := parseStockReportMetrics(text)
	if err != nil {
		t.Fatalf("parse bare regulatory table values: %v", err)
	}
	if got := valueOrZero(metrics["revenue"].YoYPct); got != 6.54 {
		t.Fatalf("bare revenue yoy = %v", got)
	}
	if got := valueOrZero(metrics["operating_cash_flow"].YoYPct); got != 205.48 {
		t.Fatalf("split cash-flow yoy = %v", got)
	}
	if got := valueOrZero(metrics["roe"].YoYPct); got != -0.35 {
		t.Fatalf("ROE decrease should be negative percentage points, got %v", got)
	}
}

func TestParseStockReportMetricsScalesExplicitThousandYuanTable(t *testing.T) {
	text := `300750 宁德时代 2025年年度报告
主要会计数据和财务指标 单位：人民币千元
营业收入 423701834 362012554 17.04%
经营活动产生的现金流量净额 133219982 96990345 37.35%`
	metrics := parseStockReportMetricsPartial(text)
	revenue := metrics["revenue"]
	cashFlow := metrics["operating_cash_flow"]
	if revenue.Status != "observed" || valueOrZero(revenue.Current) != 423701834000 || valueOrZero(revenue.Prior) != 362012554000 {
		t.Fatalf("thousand-yuan revenue scale was lost: current=%.0f prior=%.0f metric=%#v", valueOrZero(revenue.Current), valueOrZero(revenue.Prior), revenue)
	}
	if cashFlow.Status != "observed" || valueOrZero(cashFlow.Current) != 133219982000 || valueOrZero(cashFlow.Prior) != 96990345000 {
		t.Fatalf("thousand-yuan cash-flow scale was lost: current=%.0f prior=%.0f metric=%#v", valueOrZero(cashFlow.Current), valueOrZero(cashFlow.Prior), cashFlow)
	}
}

func TestStockFactsOfficialReportUsesCNINFOAndPDFText(t *testing.T) {
	server := newCNINFOStockFactsServer(t, "600519", "贵州茅台", "gssh0600519", "application/pdf", true)
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	tool.pdfTextExtractor = func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(data), "%PDF") {
			t.Fatalf("extractor received invalid temporary PDF: err=%v data=%q", err, data)
		}
		return stockReportTextFixture("600519", "贵州茅台")
	}
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderOfficialReport, "as_of": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result, markerLen := parseStockFactsTestMarker(t, output)
	if markerLen > stockFactsMaxMarkerSize {
		t.Fatalf("official marker too large: %d", markerLen)
	}
	if result.Exchange != "SSE" || result.Report == nil || result.Report.Period != "2026-03-31" || result.Report.Type != "quarterly" || result.Report.Unaudited == nil || !*result.Report.Unaudited {
		t.Fatalf("official report metadata is wrong: %#v", result)
	}
	if len(result.Report.Metrics) != 5 || len(result.Announcements) != 3 || result.AnnouncementQueryStatus != "observed" {
		t.Fatalf("official coverage is incomplete: %#v", result)
	}
	if !strings.Contains(output, "Titles below are disclosure facts only, not proof of a risk event") {
		t.Fatalf("announcement inference boundary missing: %s", output)
	}
}

func TestStockFactsOfficialReportReturnsPartialMetricsWithoutProviderFailure(t *testing.T) {
	server := newCNINFOStockFactsServer(t, "600519", "贵州茅台", "gssh0600519", "application/pdf", true)
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	tool.pdfTextExtractor = func(string) string {
		return `600519 贵州茅台 2026年第一季度报告
营业收入（元） 100.00 93.86 6.54
基本每股收益（元/股） 2.00 1.50 33.33`
	}
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderOfficialReport, "as_of": "2026-07-11",
	})
	if err != nil {
		t.Fatalf("partial official report should remain a successful lookup: %v", err)
	}
	result, _ := parseStockFactsTestMarker(t, output)
	if result.Report == nil || result.Report.Metrics["revenue"].Status != "observed" {
		t.Fatalf("observed official metric was lost: %#v", result.Report)
	}
	missing := result.Report.Metrics["parent_net_profit"]
	if missing.Status != "not_found_after_pdf_extraction" || missing.Current != nil || missing.Prior != nil || missing.YoYPct != nil {
		t.Fatalf("missing official metric was not represented safely: %#v", missing)
	}
	if !strings.Contains(output, "successful official-source lookup") {
		t.Fatalf("partial-success guidance missing: %s", output)
	}
}

func TestStockFactsRejectsFutureAndWrongTargetBeforeTrust(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		io.WriteString(w, `[]`)
	}))
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderEastmoney, "as_of": "2026-07-12",
	})
	if err == nil || !strings.Contains(err.Error(), "future") || calls.Load() != 0 {
		t.Fatalf("future as_of should fail before HTTP: calls=%d err=%v", calls.Load(), err)
	}

	wrong := newTencentStockFactsServer(t, "000001", "平安银行", "sz", false)
	defer wrong.Close()
	tool = newStockFactsTestTool(wrong.Client(), wrong.URL)
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderTencentMarket, "as_of": "2026-07-11", "lookback_days": 80,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "target") && !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("wrong-target provider response should fail: %v", err)
	}
}

func TestStockFactsOfficialReportRejectsWrongContentTypeAndPDFSignature(t *testing.T) {
	tests := []struct {
		name, contentType string
		validSignature    bool
		want              string
	}{
		{"html", "text/html", true, "application/pdf"},
		{"fake_pdf", "application/pdf", false, "%PDF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCNINFOStockFactsServer(t, "002594", "比亚迪", "gssz0002594", tt.contentType, tt.validSignature)
			defer server.Close()
			tool := newStockFactsTestTool(server.Client(), server.URL)
			tool.pdfTextExtractor = func(string) string { return stockReportTextFixture("002594", "比亚迪") }
			_, err := tool.Execute(context.Background(), map[string]interface{}{
				"symbol": "002594", "provider": stockFactsProviderOfficialReport, "as_of": "2026-07-11",
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestStockFactsHTTPTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	tool := newStockFactsTestTool(server.Client(), server.URL)
	tool.requestTimeout = 10 * time.Millisecond
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol": "600519", "provider": stockFactsProviderEastmoney, "as_of": "2026-07-11",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("timeout was not enforced: %v", err)
	}
}

func newStockFactsTestTool(client *http.Client, base string) *StockFactsTool {
	tool := NewStockFactsTool()
	tool.client = client
	tool.tencentQuoteEndpoint = base + "/quote?q="
	tool.tencentKlineEndpoint = base + "/kline"
	tool.eastmoneyEndpoint = base + "/eastmoney"
	tool.cninfoSearchEndpoint = base + "/search"
	tool.cninfoQueryEndpoint = base + "/query"
	tool.cninfoStaticBase = base + "/static/"
	tool.now = func() time.Time { return stockFactsTestNow }
	return tool
}

func newTencentStockFactsServer(t *testing.T, symbol, name, prefix string, adjusted bool) *httptest.Server {
	t.Helper()
	providerSymbol := prefix + symbol
	fields := tencentTestFields(symbol, name, 179)
	rows := makeTencentTestRows(t, 80)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/quote":
			if r.URL.Query().Get("q") != providerSymbol {
				http.Error(w, "wrong quote target", http.StatusBadRequest)
				return
			}
			var encoded bytes.Buffer
			writer := transform.NewWriter(&encoded, simplifiedchinese.GBK.NewEncoder())
			if _, err := io.WriteString(writer, "v_"+providerSymbol+"=\""+strings.Join(fields, "~")+"\";"); err != nil {
				t.Fatalf("encode Tencent quote fixture: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close Tencent quote fixture encoder: %v", err)
			}
			w.Header().Set("Content-Type", "text/plain; charset=GBK")
			if _, err := w.Write(encoded.Bytes()); err != nil {
				t.Fatalf("write Tencent quote fixture: %v", err)
			}
		case "/kline":
			param := r.URL.Query().Get("param")
			if !strings.HasPrefix(param, providerSymbol+",day,,,80,") || !strings.HasSuffix(param, ",none") {
				http.Error(w, "not an unadjusted request", http.StatusBadRequest)
				return
			}
			node := map[string]interface{}{"day": rows, "qt": map[string]interface{}{providerSymbol: fields}}
			if adjusted {
				node["qfqday"] = rows
			}
			payload := map[string]interface{}{"code": 0, "msg": "", "data": map[string]interface{}{providerSymbol: node}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

func tencentTestFields(symbol, name string, close float64) []string {
	fields := make([]string, 84)
	fields[0] = "1"
	fields[1] = name
	fields[2] = symbol
	fields[3] = fmt.Sprintf("%.2f", close)
	fields[4] = fmt.Sprintf("%.2f", close-1)
	fields[5] = fmt.Sprintf("%.2f", close-0.5)
	fields[6] = "12345"
	fields[30] = "20260710160000"
	fields[33] = fmt.Sprintf("%.2f", close+1)
	fields[34] = fmt.Sprintf("%.2f", close-1)
	fields[37] = "98765"
	fields[39] = "20.00"
	fields[44] = "22000.00"
	fields[46] = "5.00"
	fields[52] = "18.00"
	fields[53] = "19.00"
	return fields
}

func makeTencentTestRows(t *testing.T, count int) [][]string {
	t.Helper()
	dates := make([]time.Time, 0, count)
	for day := time.Date(2026, 7, 10, 0, 0, 0, 0, stockFactsShanghaiZone); len(dates) < count; day = day.AddDate(0, 0, -1) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			dates = append(dates, day)
		}
	}
	for left, right := 0, len(dates)-1; left < right; left, right = left+1, right-1 {
		dates[left], dates[right] = dates[right], dates[left]
	}
	rows := make([][]string, 0, count)
	for i, date := range dates {
		close := 100 + float64(i)
		rows = append(rows, []string{
			date.Format("2006-01-02"), fmt.Sprintf("%.2f", close-0.5), fmt.Sprintf("%.2f", close),
			fmt.Sprintf("%.2f", close+1), fmt.Sprintf("%.2f", close-1), fmt.Sprintf("%d", 1000+i),
		})
	}
	return rows
}

func newCNINFOStockFactsServer(t *testing.T, symbol, name, orgID, pdfContentType string, validSignature bool) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if r.Method != http.MethodPost || r.URL.Query().Get("keyWord") != symbol {
				http.Error(w, "bad search", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode([]map[string]string{{"code": symbol, "orgId": orgID, "zwjc": name, "delisted": "false"}})
		case "/query":
			if err := r.ParseForm(); err != nil || r.Form.Get("stock") != symbol+","+orgID {
				http.Error(w, "bad announcement query", http.StatusBadRequest)
				return
			}
			items := []map[string]interface{}{}
			if r.Form.Get("category") != "" {
				items = append(items, cninfoTestAnnouncement(symbol, name, orgID, name+"2026年第一季度报告", "finalpage/report.PDF", time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)))
			} else {
				for i, title := range []string{"2026年第一季度报告", "关于召开股东大会的通知", "董事会决议公告"} {
					items = append(items, cninfoTestAnnouncement(symbol, name, orgID, name+title, fmt.Sprintf("finalpage/a%d.PDF", i), time.Date(2026, 7, 10-i, 0, 0, 0, 0, time.UTC)))
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"announcements": items})
		case "/static/finalpage/report.PDF":
			w.Header().Set("Content-Type", pdfContentType)
			if validSignature {
				io.WriteString(w, "%PDF-1.4\nmock report")
			} else {
				io.WriteString(w, "not a pdf")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func cninfoTestAnnouncement(symbol, name, orgID, title, adjunct string, at time.Time) map[string]interface{} {
	return map[string]interface{}{
		"secCode": symbol, "secName": name, "orgId": orgID, "announcementId": "id",
		"announcementTitle": title, "announcementTime": at.UnixMilli(), "adjunctUrl": adjunct, "adjunctType": "PDF",
	}
}

func stockReportTextFixture(symbol, name string) string {
	return fmt.Sprintf(`%s %s 2026年第一季度报告
本报告期 上年同期 同比增减
营业收入（元） 100,000.00 80,000.00 25.00%%
归属于上市公司股东的净利润（元） 50,000.00 40,000.00 25.00%%
经营活动产生的现金流量净额（元） 60,000.00 30,000.00 100.00%%
基本每股收益（元/股） 2.00 1.50 33.33%%
加权平均净资产收益率 10.00%% 8.00%% 2.00%%
以上财务数据未经审计。`, symbol, name)
}

func TestSummarizeStockHistoryIncludesFullRequestedPeriodReturn(t *testing.T) {
	bars := make([]stockDailyBar, 250)
	for i := range bars {
		bars[i] = stockDailyBar{Date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i), Close: 100 + float64(i)}
	}
	history := summarizeStockHistory(bars)
	want := roundedStockFact((349.0/100.0-1)*100, 4)
	if history.ReturnPeriodPct != want {
		t.Fatalf("full period return = %v, want %v", history.ReturnPeriodPct, want)
	}
	if history.Observations != 250 || history.FirstDate == history.LastDate {
		t.Fatalf("history period metadata = %#v", history)
	}
}

func parseStockFactsTestMarker(t *testing.T, output string) (stockFactsResult, int) {
	t.Helper()
	line := strings.SplitN(output, "\n", 2)[0]
	if !strings.HasPrefix(line, stockFactsResultMarker) {
		t.Fatalf("missing marker: %s", line)
	}
	raw := strings.TrimPrefix(line, stockFactsResultMarker)
	var result stockFactsResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode marker: %v\n%s", err, raw)
	}
	return result, len(raw)
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func urlValues(raw string) url.Values {
	values, _ := url.ParseQuery(raw)
	return values
}
