package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decodeFuelPriceMarker(t *testing.T, output string) fuelPriceToolResult {
	t.Helper()
	found := 0
	var result fuelPriceToolResult
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, fuelPriceResultMarker) {
			continue
		}
		found++
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, fuelPriceResultMarker)), &result); err != nil {
			t.Fatalf("decode marker: %v\n%s", err, line)
		}
	}
	if found != 1 {
		t.Fatalf("marker count = %d, want 1\n%s", found, output)
	}
	return result
}

func fixedFuelPriceToolNow() time.Time {
	return time.Date(2026, 7, 11, 0, 30, 0, 0, time.UTC)
}

func TestFuelPriceOfficialDiscoversLatestAndDoesNotInvent98(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<ul>
<li><a href="/older" title="上海市发展和改革委员会关于车用汽、柴油价格的通知（2026年6月18日）">旧通知</a><span>发布日期：2026-06-18</span></li>
<li><a href="/latest" title="上海市发展和改革委员会关于车用汽、柴油价格的通知（2026年7月3日）">新通知</a><span>发布日期：2026-07-03</span></li>
</ul>`))
	})
	mux.HandleFunc("/older", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "older notice must not be fetched", http.StatusTeapot)
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head>
<meta name="ArticleTitle" content="上海市发展和改革委员会关于车用汽、柴油价格的通知（2026年7月3日）">
<meta name="PubDate" content="2026-07-03 04:07:41"></head><body>
<div id="ivs_content" class="Article_content"><p>上述调整后的价格自2026年7月3日24时起执行。</p>
<table>
<tr><td>92号汽油</td><td>元/升</td><td>7.14</td></tr>
<tr><td>95号汽油</td><td>元/升</td><td>7.60</td></tr>
<tr><td>0号柴油</td><td>元/升</td><td>6.80</td></tr>
</table></div><footer>上海市发展和改革委员会</footer></body></html>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool := NewFuelPriceTool()
	tool.client = server.Client()
	tool.officialIndexEndpoint = server.URL + "/index"
	tool.now = fixedFuelPriceToolNow
	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"region": "Shanghai, China", "provider": fuelPriceProviderRegionalOfficial,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	marker := decodeFuelPriceMarker(t, output)
	if marker.SourceURL != server.URL+"/latest" {
		t.Fatalf("source = %q", marker.SourceURL)
	}
	if marker.Prices["92"] == nil || *marker.Prices["92"] != 7.14 || marker.Prices["98"] != nil {
		t.Fatalf("official prices = %#v", marker.Prices)
	}
	if marker.PublishedTime != "2026-07-03T04:07:41+08:00" || marker.EffectiveTime != "2026-07-04T00:00:00+08:00" {
		t.Fatalf("official times = published %q effective %q", marker.PublishedTime, marker.EffectiveTime)
	}
	for _, want := range []string{"Current retail prices (NOT forecast)", "98 gasoline | not published", "enterprise-priced", "Source: " + server.URL + "/latest"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestFuelPriceNXVAVParsesCompleteRegionMatchedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("region"); got != "上海" {
			t.Errorf("region query = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{"region":"上海市","updated":"2026-07-11 07:00:03","items":[{"name":"92#汽油","price":7.14},{"name":"95#汽油","price":"7.60"},{"name":"98#汽油","price":9.60},{"name":"0#柴油","price":6.80}]}}`))
	}))
	defer server.Close()
	tool := NewFuelPriceTool()
	tool.client = server.Client()
	tool.nxvavEndpoint = server.URL + "/fuel?existing=1"
	tool.now = fixedFuelPriceToolNow

	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"region": "上海市", "provider": fuelPriceProviderNXVAV,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	marker := decodeFuelPriceMarker(t, output)
	if marker.Region != "上海" || marker.UpdateTime != "2026-07-11T07:00:03+08:00" || marker.Prices["98"] == nil || *marker.Prices["98"] != 9.60 {
		t.Fatalf("marker = %#v", marker)
	}
	if !strings.Contains(marker.SourceURL, "existing=1") || !strings.Contains(marker.SourceURL, "region=") {
		t.Fatalf("exact API source URL missing query: %s", marker.SourceURL)
	}
}

func TestFuelPriceTuanyouSeparatesCurrentPricesFromForecast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta name="location" content="province=上海"></head><body>
<h1>上海今日油价</h1><div class="catediqu">
<ul><li><a>92号汽油</a></li><li><a>7.14元/升</a></li></ul>
<ul><li><a>95号汽油</a></li><li><a>7.60元/升</a></li></ul>
<ul><li><a>98号汽油</a></li><li><a>9.60元/升</a></li></ul>
<ul><li><a>0号柴油</a></li><li><a>6.80元/升</a></li></ul></div>
<p>以上油价更新于2026-07-04</p>
<table><tr><td>原油变化率</td><td>下轮油价预测</td><td>当前计算日</td></tr>
<tr><td>-1.18%</td><td>降0.04元/升</td><td>2026年7月10日</td></tr></table>
<div class="xiaci">下次调价：2026-7-17</div></body></html>`))
	}))
	defer server.Close()
	tool := NewFuelPriceTool()
	tool.client = server.Client()
	tool.tuanyouShanghaiEndpoint = server.URL + "/youjia/shanghai/"
	tool.now = fixedFuelPriceToolNow

	output, err := tool.Execute(context.Background(), map[string]interface{}{
		"region": "上海", "provider": fuelPriceProviderTuanyou,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	marker := decodeFuelPriceMarker(t, output)
	if marker.Prices["92"] == nil || *marker.Prices["92"] != 7.14 {
		t.Fatalf("current 92 price changed by prediction: %#v", marker.Prices["92"])
	}
	if marker.PredictedDirection != "down" || marker.PredictedChangeCNYPerL == nil || *marker.PredictedChangeCNYPerL != -0.04 {
		t.Fatalf("forecast = direction %q change %#v", marker.PredictedDirection, marker.PredictedChangeCNYPerL)
	}
	if marker.NextAdjustmentDate != "2026-07-17" || marker.ForecastCalculationDay != "2026-07-10" || marker.CrudeChangeRate == nil || *marker.CrudeChangeRate != -1.18 {
		t.Fatalf("forecast metadata = %#v", marker)
	}
	if !strings.Contains(output, "Next adjustment / forecast (NOT current price)") {
		t.Fatalf("forecast boundary missing:\n%s", output)
	}
}

func TestFuelPriceRejectsMismatchIncompleteOutOfRangeAndBadTimeout(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "region mismatch",
			payload: `{"code":200,"data":{"region":"北京","updated":"2026-07-11 07:00:03","items":[{"name":"92#汽油","price":7.14},{"name":"95#汽油","price":7.60},{"name":"98#汽油","price":9.60},{"name":"0#柴油","price":6.80}]}}`,
			wantErr: "region mismatch",
		},
		{
			name:    "incomplete",
			payload: `{"code":200,"data":{"region":"上海","updated":"2026-07-11 07:00:03","items":[{"name":"92#汽油","price":7.14},{"name":"95#汽油","price":7.60},{"name":"98#汽油","price":9.60}]}}`,
			wantErr: "incomplete",
		},
		{
			name:    "out of range",
			payload: `{"code":200,"data":{"region":"上海","updated":"2026-07-11 07:00:03","items":[{"name":"92#汽油","price":77.14},{"name":"95#汽油","price":7.60},{"name":"98#汽油","price":9.60},{"name":"0#柴油","price":6.80}]}}`,
			wantErr: "outside the accepted range",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.payload))
			}))
			defer server.Close()
			tool := NewFuelPriceTool()
			tool.client = server.Client()
			tool.nxvavEndpoint = server.URL
			_, err := tool.Execute(context.Background(), map[string]interface{}{"region": "上海", "provider": fuelPriceProviderNXVAV})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}

	tool := NewFuelPriceTool()
	tool.requestTimeout = 0
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"region": "上海", "provider": fuelPriceProviderNXVAV}); err == nil || !strings.Contains(err.Error(), "HTTP timeout") {
		t.Fatalf("invalid timeout error = %v", err)
	}
}

func TestFuelPriceHTTPDeadlineIsEnforcedForInjectedClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	tool := NewFuelPriceTool()
	tool.client = server.Client()
	tool.nxvavEndpoint = server.URL
	tool.requestTimeout = 15 * time.Millisecond
	started := time.Now()
	_, err := tool.Execute(context.Background(), map[string]interface{}{"region": "上海", "provider": fuelPriceProviderNXVAV})
	if err == nil || (!strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestFuelPriceToolMetadata(t *testing.T) {
	tool := NewFuelPriceTool()
	if tool.Name() != "fuel_price" || !tool.IsReadOnly() || tool.MaxResultSize() <= 0 {
		t.Fatalf("tool metadata is incomplete")
	}
	parameters := tool.Parameters()
	if parameters["type"] != "object" || !strings.Contains(tool.Description(), "Never report") ||
		!strings.Contains(tool.Description(), "regional_official, nxvav, and tuanyou exactly once each") {
		t.Fatalf("tool schema/description is incomplete")
	}
}
