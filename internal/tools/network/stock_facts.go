package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"mac/internal/configvalue"
)

const (
	stockFactsProviderTencentMarket  = "tencent_market"
	stockFactsProviderEastmoney      = "eastmoney_market"
	stockFactsProviderOfficialReport = "official_report"

	stockFactsResultMarker  = "OPENKITTY_STOCK_FACTS_RESULT="
	stockFactsMaxMarkerSize = 1800
	stockFactsMaxJSONBody   = int64(4 * 1024 * 1024)
	stockFactsMaxPDFBody    = int64(32 * 1024 * 1024)
)

const (
	StockFactsProviderTencentMarket  = stockFactsProviderTencentMarket
	StockFactsProviderEastmoney      = stockFactsProviderEastmoney
	StockFactsProviderOfficialReport = stockFactsProviderOfficialReport
	StockFactsResultMarker           = stockFactsResultMarker
)

var (
	stockFactsSymbolPattern = regexp.MustCompile(`(?i)^(?:(?:sh|sz))?[0-9]{6}$`)
	stockFactsShanghaiZone  = time.FixedZone("Asia/Shanghai", 8*60*60)
	stockFactsNumberToken   = regexp.MustCompile(`[-+]?(?:\([0-9][0-9,]*(?:\.[0-9]+)?\)|[0-9][0-9,]*(?:\.[0-9]+)?)(?:%|％)?`)
	stockFactsSplitDecimal  = regexp.MustCompile(`([0-9])\s+\.\s*([0-9])`)
)

// StockFactsTool fetches one narrowly scoped, structured stock evidence set per
// call. Calling the three providers concurrently keeps failures isolated and
// preserves the different quote, market-data, and official-disclosure
// provenance instead of blending their values.
type StockFactsTool struct {
	client *http.Client

	tencentQuoteEndpoint string
	tencentKlineEndpoint string
	eastmoneyEndpoint    string
	cninfoSearchEndpoint string
	cninfoQueryEndpoint  string
	cninfoStaticBase     string

	requestTimeout   time.Duration
	now              func() time.Time
	pdfTextExtractor func(string) string
}

func NewStockFactsTool() *StockFactsTool {
	return NewStockFactsToolWithPDFExtractor(nil)
}

func NewStockFactsToolWithPDFExtractor(extractor func(string) string) *StockFactsTool {
	return &StockFactsTool{
		client:               &http.Client{Timeout: 12 * time.Second},
		tencentQuoteEndpoint: "https://qt.gtimg.cn/q=",
		tencentKlineEndpoint: "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get",
		eastmoneyEndpoint:    "https://push2delay.eastmoney.com/api/qt/stock/get",
		cninfoSearchEndpoint: "https://www.cninfo.com.cn/new/information/topSearch/query",
		cninfoQueryEndpoint:  "https://www.cninfo.com.cn/new/hisAnnouncement/query",
		cninfoStaticBase:     "https://static.cninfo.com.cn/",
		requestTimeout:       12 * time.Second,
		now:                  time.Now,
		pdfTextExtractor:     extractor,
	}
}

func (t *StockFactsTool) Name() string { return "stock_facts" }

func (t *StockFactsTool) Description() string {
	return "Fetch compact, structured read-only facts for one A-share stock or explicitly exchange-qualified mainland index. " +
		"For a full single-stock factual analysis, call exactly three times in one parallel batch with the same symbol/as_of: " +
		"provider=tencent_market (unadjusted daily prices and price returns), provider=eastmoney_market (cross-source quote and explicitly labelled valuation fields), and provider=official_report (latest CNINFO periodic report plus the latest three official announcement titles). " +
		"Tencent returns are price returns from adjustment=none and do not include cash dividends. Keep Tencent and Eastmoney valuation values separate; do not average them. " +
		"Official announcement titles are facts only and must not be turned into an asserted risk event without reading supporting disclosure text. " +
		"A successful official_report response may contain metric status=not_found_after_pdf_extraction; keep that value unknown instead of treating the provider as failed or repeating broad web searches. " +
		"For broad-market indices, use an explicit sh/sz prefix (for example sh000001, sh000688, sz399001, sz399006) and only the Tencent/Eastmoney market providers; never request official_report for an index. " +
		"Omit lookback_days unless a long price history is needed; unsupported values are safely normalized to the default. If the requested as_of is a weekend and the returned trade_date is the prior weekday, that is an expected latest-trading-day result, not a fetch failure."
}

func (t *StockFactsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"symbol": map[string]interface{}{
				"type":        "string",
				"pattern":     "^(?:[sS][hH]|[sS][zZ])?[0-9]{6}$",
				"description": "Six-digit A-share stock code, or an sh/sz-qualified mainland index code. Prefix is required for ambiguous indices such as sh000001 (Shanghai Composite) and sh000688 (STAR 50); bare 000001 remains the Shenzhen stock.",
			},
			"provider": map[string]interface{}{
				"type": "string",
				"enum": []string{
					stockFactsProviderTencentMarket,
					stockFactsProviderEastmoney,
					stockFactsProviderOfficialReport,
				},
			},
			"as_of": map[string]interface{}{
				"type":        "string",
				"description": "Optional evidence cutoff date in YYYY-MM-DD. Defaults to today in Asia/Shanghai and cannot be in the future.",
			},
			"lookback_days": map[string]interface{}{
				"type":        "integer",
				"minimum":     61,
				"maximum":     250,
				"default":     80,
				"description": "Optional unadjusted trading-day rows requested from Tencent. Omit to use 80. At least 61 are required to calculate 60-day price return and MA60; unsupported supplied values are normalized to 80.",
			},
		},
		"required": []string{"symbol", "provider"},
	}
}

func (t *StockFactsTool) MaxResultSize() int { return 24 * 1024 }
func (t *StockFactsTool) IsReadOnly() bool   { return true }

type stockFactsResult struct {
	Version                 int                  `json:"version"`
	Provider                string               `json:"provider"`
	Symbol                  string               `json:"symbol"`
	Market                  string               `json:"market"`
	Exchange                string               `json:"exchange"`
	CompanyName             string               `json:"company_name"`
	Currency                string               `json:"currency"`
	Publisher               string               `json:"publisher"`
	SourceURL               string               `json:"source_url"`
	RetrievalTime           string               `json:"retrieval_time"`
	RequestedAsOf           string               `json:"requested_as_of,omitempty"`
	AsOf                    string               `json:"as_of"`
	Quote                   *stockQuoteFacts     `json:"quote,omitempty"`
	History                 *stockHistoryFacts   `json:"history,omitempty"`
	Valuation               *stockValuationFacts `json:"valuation,omitempty"`
	Report                  *stockReportFacts    `json:"report,omitempty"`
	Announcements           []stockAnnouncement  `json:"announcements,omitempty"`
	AnnouncementQueryStatus string               `json:"announcement_query_status,omitempty"`
}

type StockFactsResult = stockFactsResult

type stockQuoteFacts struct {
	TradeDate   string   `json:"trade_date"`
	QuoteTime   string   `json:"quote_time"`
	MarketState string   `json:"market_state"`
	PriceKind   string   `json:"price_kind"`
	Open        *float64 `json:"open,omitempty"`
	High        *float64 `json:"high,omitempty"`
	Low         *float64 `json:"low,omitempty"`
	Close       *float64 `json:"close,omitempty"`
	PrevClose   *float64 `json:"prev_close,omitempty"`
	Volume      *int64   `json:"volume,omitempty"`
	Amount      *float64 `json:"amount,omitempty"`
}

type StockQuoteFacts = stockQuoteFacts

type stockHistoryFacts struct {
	Adjustment                   string  `json:"adjustment"`
	TotalReturnIncludesDividends bool    `json:"total_return_includes_dividends"`
	Observations                 int     `json:"observations"`
	FirstDate                    string  `json:"first_date"`
	LastDate                     string  `json:"last_date"`
	Return5DPct                  float64 `json:"return_5d_pct"`
	Return20DPct                 float64 `json:"return_20d_pct"`
	Return60DPct                 float64 `json:"return_60d_pct"`
	ReturnPeriodPct              float64 `json:"return_period_pct"`
	MA5                          float64 `json:"ma5"`
	MA20                         float64 `json:"ma20"`
	MA60                         float64 `json:"ma60"`
	Range20Low                   float64 `json:"range20_low"`
	Range20High                  float64 `json:"range20_high"`
	Range60Low                   float64 `json:"range60_low"`
	Range60High                  float64 `json:"range60_high"`
}

type StockHistoryFacts = stockHistoryFacts

type stockValuationFacts struct {
	AsOf         string            `json:"as_of"`
	PEDynamic    *float64          `json:"pe_dynamic,omitempty"`
	PEStatic     *float64          `json:"pe_static,omitempty"`
	PETTM        *float64          `json:"pe_ttm,omitempty"`
	PB           *float64          `json:"pb,omitempty"`
	MarketCap    *float64          `json:"market_cap,omitempty"`
	MetricStatus map[string]string `json:"metric_status"`
}

type StockValuationFacts = stockValuationFacts

type stockReportFacts struct {
	Title            string                       `json:"title"`
	Type             string                       `json:"type"`
	Period           string                       `json:"period"`
	AnnouncementDate string                       `json:"announcement_date"`
	Unaudited        *bool                        `json:"unaudited,omitempty"`
	SourceURL        string                       `json:"source_url"`
	Metrics          map[string]stockReportMetric `json:"metrics"`
}

type StockReportFacts = stockReportFacts

type stockReportMetric struct {
	Current *float64 `json:"current,omitempty"`
	Prior   *float64 `json:"prior,omitempty"`
	YoYPct  *float64 `json:"yoy_pct,omitempty"`
	Unit    string   `json:"unit"`
	Status  string   `json:"status"`
}

type StockReportMetric = stockReportMetric

type stockAnnouncement struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	URL   string `json:"url"`
}

type StockAnnouncement = stockAnnouncement

type stockDailyBar struct {
	Date   time.Time
	Open   float64
	Close  float64
	High   float64
	Low    float64
	Volume float64
}

type tencentQuotePayload struct {
	Name       string
	Symbol     string
	Current    float64
	PrevClose  float64
	Open       float64
	High       float64
	Low        float64
	VolumeLots float64
	Amount10K  float64
	QuoteTime  time.Time
	PETTM      *float64
	PB         *float64
	PEDynamic  *float64
	PEStatic   *float64
	MarketCap  *float64
}

type cninfoSecurity struct {
	Code     string `json:"code"`
	OrgID    string `json:"orgId"`
	Name     string `json:"zwjc"`
	Delisted string `json:"delisted"`
}

type cninfoAnnouncementResponse struct {
	Announcements []cninfoAnnouncement `json:"announcements"`
}

type cninfoAnnouncement struct {
	SecCode           string `json:"secCode"`
	SecName           string `json:"secName"`
	OrgID             string `json:"orgId"`
	AnnouncementID    string `json:"announcementId"`
	AnnouncementTitle string `json:"announcementTitle"`
	AnnouncementTime  int64  `json:"announcementTime"`
	AdjunctURL        string `json:"adjunctUrl"`
	AdjunctType       string `json:"adjunctType"`
}

func (t *StockFactsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.client == nil {
		return "", errors.New("stock_facts is not configured")
	}
	if t.requestTimeout <= 0 || t.requestTimeout > 30*time.Second {
		return "", fmt.Errorf("stock_facts HTTP timeout must be within (0, 30s], got %s", t.requestTimeout)
	}
	rawSymbol, _ := params["symbol"].(string)
	symbol, exchangeHint, err := normalizeStockFactsSymbol(rawSymbol)
	if err != nil {
		return "", err
	}
	provider, _ := params["provider"].(string)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != stockFactsProviderTencentMarket && provider != stockFactsProviderEastmoney && provider != stockFactsProviderOfficialReport {
		return "", fmt.Errorf("unsupported stock facts provider %q", provider)
	}
	asOf, err := t.resolveAsOf(params)
	if err != nil {
		return "", err
	}
	lookback := configvalue.Int(params, "lookback_days", 80)
	if lookback < 61 || lookback > 250 {
		// Tool-call arguments are model-generated. A harmless preference outside
		// the supported analytics window should not discard an otherwise valid
		// quote request; fall back to the schema default and keep the fetch bounded.
		lookback = 80
	}

	var result stockFactsResult
	switch provider {
	case stockFactsProviderTencentMarket:
		result, err = t.fetchTencentMarket(ctx, symbol, exchangeHint, asOf, lookback)
	case stockFactsProviderEastmoney:
		result, err = t.fetchEastmoneyMarket(ctx, symbol, exchangeHint, asOf)
	case stockFactsProviderOfficialReport:
		result, err = t.fetchOfficialReport(ctx, symbol, exchangeHint, asOf)
	}
	if err != nil {
		return "", fmt.Errorf("%s stock facts failed: %w", provider, err)
	}
	result.RequestedAsOf = asOf.Format("2006-01-02")
	if err := validateStockFactsResult(result, symbol, provider, asOf); err != nil {
		return "", fmt.Errorf("%s stock facts failed validation: %w", provider, err)
	}
	return renderStockFactsResult(result)
}

func (t *StockFactsTool) resolveAsOf(params map[string]interface{}) (time.Time, error) {
	now := t.currentTime().In(stockFactsShanghaiZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, stockFactsShanghaiZone)
	raw, _ := params["as_of"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return today, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", raw, stockFactsShanghaiZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("as_of must use YYYY-MM-DD: %w", err)
	}
	if parsed.Year() < 1990 {
		return time.Time{}, errors.New("as_of is outside the supported market-data range")
	}
	if parsed.After(today) {
		return time.Time{}, fmt.Errorf("as_of %s is in the future relative to %s", raw, today.Format("2006-01-02"))
	}
	return parsed, nil
}

func (t *StockFactsTool) currentTime() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

func normalizeStockFactsSymbol(raw string) (symbol, exchangeHint string, err error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if !stockFactsSymbolPattern.MatchString(normalized) {
		return "", "", fmt.Errorf("symbol must be a six-digit A-share stock code or an sh/sz-qualified mainland index code, got %q", strings.TrimSpace(raw))
	}
	if len(normalized) == 8 {
		return normalized[2:], normalized[:2], nil
	}
	return normalized, "", nil
}

func NormalizeStockFactsSymbol(raw string) (symbol, exchangeHint string, err error) {
	return normalizeStockFactsSymbol(raw)
}

func stockFactsExchange(symbol string, explicitPrefix ...string) (exchange, tencentPrefix, eastmoneySecID, cninfoColumn, cninfoPlate string, err error) {
	prefix := ""
	if len(explicitPrefix) > 0 {
		prefix = strings.ToLower(strings.TrimSpace(explicitPrefix[0]))
	}
	switch prefix {
	case "sh":
		return "SSE", "sh", "1." + symbol, "sse", "sh", nil
	case "sz":
		return "SZSE", "sz", "0." + symbol, "szse", "sz", nil
	case "":
		// Preserve the long-standing stock-code inference for unqualified
		// symbols. In particular, bare 000001 is Ping An Bank; callers that
		// mean the Shanghai Composite must say sh000001.
	default:
		return "", "", "", "", "", fmt.Errorf("unsupported stock_facts exchange prefix %q", prefix)
	}
	switch symbol[0] {
	case '6':
		return "SSE", "sh", "1." + symbol, "sse", "sh", nil
	case '0', '3':
		return "SZSE", "sz", "0." + symbol, "szse", "sz", nil
	default:
		return "", "", "", "", "", fmt.Errorf("stock_facts currently supports SSE (6xxxxx) and SZSE (0xxxxx/3xxxxx), got %s", symbol)
	}
}

func StockFactsExchange(symbol string, explicitPrefix ...string) (exchange, tencentPrefix, eastmoneySecID, cninfoColumn, cninfoPlate string, err error) {
	return stockFactsExchange(symbol, explicitPrefix...)
}

func (t *StockFactsTool) fetchTencentMarket(ctx context.Context, symbol, exchangeHint string, asOf time.Time, lookback int) (stockFactsResult, error) {
	exchange, prefix, _, _, _, err := stockFactsExchange(symbol, exchangeHint)
	if err != nil {
		return stockFactsResult{}, err
	}
	providerSymbol := prefix + symbol
	quoteURL := strings.TrimSpace(t.tencentQuoteEndpoint) + providerSymbol
	quoteBody, _, err := t.doGET(ctx, quoteURL, "text/plain,*/*", stockFactsMaxJSONBody)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("fetch Tencent quote: %w", err)
	}
	quote, err := parseTencentQuoteText(decodeTencentQuoteBody(quoteBody), providerSymbol)
	if err != nil {
		return stockFactsResult{}, err
	}

	klineURL, err := addStockFactsQuery(t.tencentKlineEndpoint, map[string]string{
		"param": fmt.Sprintf("%s,day,,,%d,none", providerSymbol, lookback),
	})
	if err != nil {
		return stockFactsResult{}, err
	}
	klineBody, actualKlineURL, err := t.doGET(ctx, klineURL, "application/json,*/*", stockFactsMaxJSONBody)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("fetch Tencent unadjusted daily bars: %w", err)
	}
	bars, klineQuote, err := parseTencentKlineJSON(klineBody, providerSymbol)
	if err != nil {
		return stockFactsResult{}, err
	}
	if quote.Symbol != symbol || klineQuote.Symbol != symbol || quote.Name == "" || klineQuote.Name == "" || quote.Name != klineQuote.Name {
		return stockFactsResult{}, fmt.Errorf("Tencent responses do not identify the same target: quote=%s/%s kline=%s/%s", quote.Symbol, quote.Name, klineQuote.Symbol, klineQuote.Name)
	}
	if quote.QuoteTime.After(t.currentTime().Add(10*time.Minute)) || klineQuote.QuoteTime.After(t.currentTime().Add(10*time.Minute)) {
		return stockFactsResult{}, errors.New("Tencent quote timestamp is in the future")
	}

	completed := completedTencentBars(bars, asOf, quote.QuoteTime, t.currentTime())
	if len(completed) < 61 {
		return stockFactsResult{}, fmt.Errorf("Tencent returned only %d completed unadjusted bars through %s; need at least 61", len(completed), asOf.Format("2006-01-02"))
	}
	latest := completed[len(completed)-1]
	previous := completed[len(completed)-2]
	history := summarizeStockHistory(completed)
	volume := int64(math.Round(latest.Volume * 100))
	closeValue, openValue, highValue, lowValue, previousValue := latest.Close, latest.Open, latest.High, latest.Low, previous.Close
	amount := (*float64)(nil)
	if quote.QuoteTime.In(stockFactsShanghaiZone).Format("2006-01-02") == latest.Date.Format("2006-01-02") && quote.Amount10K >= 0 {
		value := quote.Amount10K * 10000
		amount = &value
	}
	marketState := stockMarketState(quote.QuoteTime, t.currentTime())
	valuation := valuationFromTencent(klineQuote)
	if klineQuote.QuoteTime.In(stockFactsShanghaiZone).Format("2006-01-02") > asOf.Format("2006-01-02") {
		valuation = nil
	}
	return stockFactsResult{
		Version:       1,
		Provider:      stockFactsProviderTencentMarket,
		Symbol:        symbol,
		Market:        "cn_a",
		Exchange:      exchange,
		CompanyName:   quote.Name,
		Currency:      "CNY",
		Publisher:     "腾讯证券行情",
		SourceURL:     actualKlineURL,
		RetrievalTime: t.currentTime().UTC().Format(time.RFC3339),
		AsOf:          latest.Date.Format("2006-01-02"),
		Quote: &stockQuoteFacts{
			TradeDate:   latest.Date.Format("2006-01-02"),
			QuoteTime:   quote.QuoteTime.Format(time.RFC3339),
			MarketState: marketState,
			PriceKind:   "latest_completed_daily_bar",
			Open:        &openValue,
			High:        &highValue,
			Low:         &lowValue,
			Close:       &closeValue,
			PrevClose:   &previousValue,
			Volume:      &volume,
			Amount:      amount,
		},
		History:   &history,
		Valuation: valuation,
	}, nil
}

// qt.gtimg.cn returns its legacy quote payload as GBK, while test doubles and
// some proxies may already transcode it to UTF-8. Decode only when the bytes
// are not valid UTF-8 so both contracts remain supported without corrupting
// an already-correct company name.
func decodeTencentQuoteBody(body []byte) string {
	if utf8.Valid(body) {
		return string(body)
	}
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(body), simplifiedchinese.GBK.NewDecoder()))
	if err != nil {
		return string(body)
	}
	return string(decoded)
}

func parseTencentQuoteText(raw, providerSymbol string) (tencentQuotePayload, error) {
	prefix := "v_" + providerSymbol + "=\""
	start := strings.Index(raw, prefix)
	if start < 0 {
		return tencentQuotePayload{}, fmt.Errorf("Tencent quote response does not contain %s", providerSymbol)
	}
	rest := raw[start+len(prefix):]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return tencentQuotePayload{}, errors.New("Tencent quote response is unterminated")
	}
	return parseTencentQuoteFields(strings.Split(rest[:end], "~"), providerSymbol)
}

func parseTencentQuoteFields(fields []string, providerSymbol string) (tencentQuotePayload, error) {
	if len(fields) < 54 {
		return tencentQuotePayload{}, fmt.Errorf("Tencent quote has %d fields; need at least 54", len(fields))
	}
	symbol := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(providerSymbol, "sh"), "sz"), "bj")
	if strings.TrimSpace(fields[2]) != symbol {
		return tencentQuotePayload{}, fmt.Errorf("Tencent quote target mismatch: got %q, want %q", fields[2], symbol)
	}
	parseRequired := func(index int, label string) (float64, error) {
		value, ok := parseStockFactsFloat(fields[index])
		if !ok {
			return 0, fmt.Errorf("Tencent quote missing or invalid %s", label)
		}
		return value, nil
	}
	current, err := parseRequired(3, "current price")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	prevClose, err := parseRequired(4, "previous close")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	open, err := parseRequired(5, "open")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	volume, err := parseRequired(6, "volume")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	high, err := parseRequired(33, "high")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	low, err := parseRequired(34, "low")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	amount, err := parseRequired(37, "amount")
	if err != nil {
		return tencentQuotePayload{}, err
	}
	quoteTime, err := time.ParseInLocation("20060102150405", strings.TrimSpace(fields[30]), stockFactsShanghaiZone)
	if err != nil {
		return tencentQuotePayload{}, fmt.Errorf("invalid Tencent quote time %q: %w", fields[30], err)
	}
	if err := validateOHLC(open, high, low, current); err != nil {
		return tencentQuotePayload{}, fmt.Errorf("Tencent quote: %w", err)
	}
	payload := tencentQuotePayload{
		Name:       strings.TrimSpace(fields[1]),
		Symbol:     symbol,
		Current:    current,
		PrevClose:  prevClose,
		Open:       open,
		High:       high,
		Low:        low,
		VolumeLots: volume,
		Amount10K:  amount,
		QuoteTime:  quoteTime,
		PETTM:      optionalPositiveStockFloat(fields[39]),
		PB:         optionalPositiveStockFloat(fields[46]),
		PEDynamic:  optionalPositiveStockFloat(fields[52]),
		PEStatic:   optionalPositiveStockFloat(fields[53]),
	}
	if marketCapYi, ok := parseStockFactsFloat(fields[44]); ok && marketCapYi > 0 {
		marketCap := marketCapYi * 100000000
		payload.MarketCap = &marketCap
	}
	if payload.Name == "" {
		return tencentQuotePayload{}, errors.New("Tencent quote company name is empty")
	}
	return payload, nil
}

func parseTencentKlineJSON(body []byte, providerSymbol string) ([]stockDailyBar, tencentQuotePayload, error) {
	var envelope struct {
		Code int                        `json:"code"`
		Msg  string                     `json:"msg"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, tencentQuotePayload{}, fmt.Errorf("decode Tencent kline JSON: %w", err)
	}
	if envelope.Code != 0 {
		return nil, tencentQuotePayload{}, fmt.Errorf("Tencent kline code=%d msg=%s", envelope.Code, envelope.Msg)
	}
	raw, ok := envelope.Data[providerSymbol]
	if !ok || len(envelope.Data) != 1 {
		return nil, tencentQuotePayload{}, fmt.Errorf("Tencent kline target mismatch for %s", providerSymbol)
	}
	var node struct {
		Day    [][]json.RawMessage          `json:"day"`
		QFQDay [][]json.RawMessage          `json:"qfqday"`
		QT     map[string][]json.RawMessage `json:"qt"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, tencentQuotePayload{}, fmt.Errorf("decode Tencent kline target: %w", err)
	}
	// adjustment=none must return day. Accepting qfqday here would silently turn
	// a price-return request into a corporate-action-adjusted return.
	if len(node.Day) == 0 || len(node.QFQDay) != 0 {
		return nil, tencentQuotePayload{}, errors.New("Tencent did not return unadjusted day rows for adjustment=none")
	}
	qtRaw, ok := node.QT[providerSymbol]
	if !ok {
		return nil, tencentQuotePayload{}, errors.New("Tencent kline response lacks matching qt snapshot")
	}
	qtFields := make([]string, len(qtRaw))
	for i, value := range qtRaw {
		if err := json.Unmarshal(value, &qtFields[i]); err != nil {
			return nil, tencentQuotePayload{}, fmt.Errorf("decode Tencent qt field %d: %w", i, err)
		}
	}
	quote, err := parseTencentQuoteFields(qtFields, providerSymbol)
	if err != nil {
		return nil, tencentQuotePayload{}, err
	}
	bars := make([]stockDailyBar, 0, len(node.Day))
	for i, row := range node.Day {
		if len(row) < 6 {
			return nil, tencentQuotePayload{}, fmt.Errorf("Tencent day row %d has %d columns", i, len(row))
		}
		var dateRaw string
		if err := json.Unmarshal(row[0], &dateRaw); err != nil {
			return nil, tencentQuotePayload{}, fmt.Errorf("decode Tencent day row %d date: %w", i, err)
		}
		date, err := time.ParseInLocation("2006-01-02", dateRaw, stockFactsShanghaiZone)
		if err != nil {
			return nil, tencentQuotePayload{}, fmt.Errorf("invalid Tencent day row %d date: %w", i, err)
		}
		values := make([]float64, 5)
		for column := 1; column <= 5; column++ {
			var text string
			if err := json.Unmarshal(row[column], &text); err != nil {
				return nil, tencentQuotePayload{}, fmt.Errorf("decode Tencent day row %d column %d: %w", i, column, err)
			}
			value, ok := parseStockFactsFloat(text)
			if !ok {
				return nil, tencentQuotePayload{}, fmt.Errorf("invalid Tencent day row %d column %d", i, column)
			}
			values[column-1] = value
		}
		bar := stockDailyBar{Date: date, Open: values[0], Close: values[1], High: values[2], Low: values[3], Volume: values[4]}
		if err := validateOHLC(bar.Open, bar.High, bar.Low, bar.Close); err != nil {
			return nil, tencentQuotePayload{}, fmt.Errorf("Tencent day row %d: %w", i, err)
		}
		if bar.Volume < 0 || math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) {
			return nil, tencentQuotePayload{}, fmt.Errorf("Tencent day row %d has invalid volume", i)
		}
		bars = append(bars, bar)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
	for i := 1; i < len(bars); i++ {
		if !bars[i].Date.After(bars[i-1].Date) {
			return nil, tencentQuotePayload{}, errors.New("Tencent day rows contain duplicate or unsorted dates")
		}
	}
	return bars, quote, nil
}

func completedTencentBars(bars []stockDailyBar, asOf, quoteTime, now time.Time) []stockDailyBar {
	cutoff := asOf.Format("2006-01-02")
	quoteLocal := quoteTime.In(stockFactsShanghaiZone)
	quoteDate := quoteLocal.Format("2006-01-02")
	nowDate := now.In(stockFactsShanghaiZone).Format("2006-01-02")
	quoteDayCompleted := quoteDate < nowDate || quoteLocal.Hour() >= 15
	result := make([]stockDailyBar, 0, len(bars))
	for _, bar := range bars {
		date := bar.Date.Format("2006-01-02")
		if date > cutoff || (date == quoteDate && !quoteDayCompleted) {
			continue
		}
		result = append(result, bar)
	}
	return result
}

func summarizeStockHistory(bars []stockDailyBar) stockHistoryFacts {
	last := len(bars) - 1
	return stockHistoryFacts{
		Adjustment:                   "none",
		TotalReturnIncludesDividends: false,
		Observations:                 len(bars),
		FirstDate:                    bars[0].Date.Format("2006-01-02"),
		LastDate:                     bars[last].Date.Format("2006-01-02"),
		Return5DPct:                  roundedStockFact((bars[last].Close/bars[last-5].Close-1)*100, 4),
		Return20DPct:                 roundedStockFact((bars[last].Close/bars[last-20].Close-1)*100, 4),
		Return60DPct:                 roundedStockFact((bars[last].Close/bars[last-60].Close-1)*100, 4),
		ReturnPeriodPct:              roundedStockFact((bars[last].Close/bars[0].Close-1)*100, 4),
		MA5:                          stockMovingAverage(bars, 5),
		MA20:                         stockMovingAverage(bars, 20),
		MA60:                         stockMovingAverage(bars, 60),
		Range20Low:                   stockRangeLow(bars, 20),
		Range20High:                  stockRangeHigh(bars, 20),
		Range60Low:                   stockRangeLow(bars, 60),
		Range60High:                  stockRangeHigh(bars, 60),
	}
}

func stockMovingAverage(bars []stockDailyBar, days int) float64 {
	sum := 0.0
	for _, bar := range bars[len(bars)-days:] {
		sum += bar.Close
	}
	return roundedStockFact(sum/float64(days), 4)
}

func stockRangeLow(bars []stockDailyBar, days int) float64 {
	value := math.Inf(1)
	for _, bar := range bars[len(bars)-days:] {
		value = math.Min(value, bar.Low)
	}
	return roundedStockFact(value, 4)
}

func stockRangeHigh(bars []stockDailyBar, days int) float64 {
	value := math.Inf(-1)
	for _, bar := range bars[len(bars)-days:] {
		value = math.Max(value, bar.High)
	}
	return roundedStockFact(value, 4)
}

func valuationFromTencent(quote tencentQuotePayload) *stockValuationFacts {
	status := map[string]string{
		"pe_dynamic": stockValuationStatus(quote.PEDynamic),
		"pe_static":  stockValuationStatus(quote.PEStatic),
		"pe_ttm":     stockValuationStatus(quote.PETTM),
		"pb":         stockValuationStatus(quote.PB),
		"market_cap": stockValuationStatus(quote.MarketCap),
	}
	return &stockValuationFacts{
		AsOf:         quote.QuoteTime.In(stockFactsShanghaiZone).Format("2006-01-02"),
		PEDynamic:    quote.PEDynamic,
		PEStatic:     quote.PEStatic,
		PETTM:        quote.PETTM,
		PB:           quote.PB,
		MarketCap:    quote.MarketCap,
		MetricStatus: status,
	}
}

func (t *StockFactsTool) fetchEastmoneyMarket(ctx context.Context, symbol, exchangeHint string, asOf time.Time) (stockFactsResult, error) {
	exchange, _, secID, _, _, err := stockFactsExchange(symbol, exchangeHint)
	if err != nil {
		return stockFactsResult{}, err
	}
	rawURL, err := addStockFactsQuery(t.eastmoneyEndpoint, map[string]string{
		"secid":  secID,
		"fields": "f43,f44,f45,f46,f47,f48,f57,f58,f60,f86,f116,f162,f163,f164,f167",
	})
	if err != nil {
		return stockFactsResult{}, err
	}
	body, actualURL, err := t.doGET(ctx, rawURL, "application/json,*/*", stockFactsMaxJSONBody)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("fetch Eastmoney snapshot: %w", err)
	}
	var envelope struct {
		RC   int                        `json:"rc"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return stockFactsResult{}, fmt.Errorf("decode Eastmoney JSON: %w", err)
	}
	if envelope.RC != 0 || envelope.Data == nil {
		return stockFactsResult{}, fmt.Errorf("Eastmoney returned rc=%d or empty data", envelope.RC)
	}
	code, ok := stockRawString(envelope.Data["f57"])
	if !ok || code != symbol {
		return stockFactsResult{}, fmt.Errorf("Eastmoney target mismatch: got %q, want %q", code, symbol)
	}
	name, ok := stockRawString(envelope.Data["f58"])
	if !ok || strings.TrimSpace(name) == "" {
		return stockFactsResult{}, errors.New("Eastmoney company name is empty")
	}
	unixTime, ok := stockRawInt64(envelope.Data["f86"])
	if !ok || unixTime <= 0 {
		return stockFactsResult{}, errors.New("Eastmoney f86 quote time is invalid")
	}
	quoteTime := time.Unix(unixTime, 0).In(stockFactsShanghaiZone)
	if quoteTime.After(t.currentTime().Add(10 * time.Minute)) {
		return stockFactsResult{}, errors.New("Eastmoney quote timestamp is in the future")
	}
	tradeDate := quoteTime.Format("2006-01-02")
	if tradeDate > asOf.Format("2006-01-02") {
		return stockFactsResult{}, fmt.Errorf("Eastmoney current snapshot (%s) is newer than requested as_of %s", tradeDate, asOf.Format("2006-01-02"))
	}
	price, err := requiredScaledEastmoney(envelope.Data, "f43", 100)
	if err != nil {
		return stockFactsResult{}, err
	}
	prevClose, err := requiredScaledEastmoney(envelope.Data, "f60", 100)
	if err != nil {
		return stockFactsResult{}, err
	}
	open := optionalScaledEastmoney(envelope.Data, "f46", 100)
	high := optionalScaledEastmoney(envelope.Data, "f44", 100)
	low := optionalScaledEastmoney(envelope.Data, "f45", 100)
	if open != nil && high != nil && low != nil {
		if err := validateOHLC(*open, *high, *low, price); err != nil {
			return stockFactsResult{}, fmt.Errorf("Eastmoney quote: %w", err)
		}
	}
	volume := optionalEastmoneyInt64(envelope.Data, "f47", 100)
	amount := optionalRawEastmoneyFloat(envelope.Data, "f48")
	marketCap := optionalRawEastmoneyFloat(envelope.Data, "f116")
	valuation := &stockValuationFacts{
		AsOf:         tradeDate,
		PEDynamic:    optionalScaledEastmoney(envelope.Data, "f162", 100),
		PEStatic:     optionalScaledEastmoney(envelope.Data, "f163", 100),
		PETTM:        optionalScaledEastmoney(envelope.Data, "f164", 100),
		PB:           optionalScaledEastmoney(envelope.Data, "f167", 100),
		MarketCap:    marketCap,
		MetricStatus: make(map[string]string, 5),
	}
	valuation.MetricStatus["pe_dynamic"] = stockValuationStatus(valuation.PEDynamic)
	valuation.MetricStatus["pe_static"] = stockValuationStatus(valuation.PEStatic)
	valuation.MetricStatus["pe_ttm"] = stockValuationStatus(valuation.PETTM)
	valuation.MetricStatus["pb"] = stockValuationStatus(valuation.PB)
	valuation.MetricStatus["market_cap"] = stockValuationStatus(valuation.MarketCap)
	state := stockMarketState(quoteTime, t.currentTime())
	priceKind := "latest_close"
	if state == "intraday" {
		priceKind = "intraday_last"
	}
	return stockFactsResult{
		Version:       1,
		Provider:      stockFactsProviderEastmoney,
		Symbol:        symbol,
		Market:        "cn_a",
		Exchange:      exchange,
		CompanyName:   strings.TrimSpace(name),
		Currency:      "CNY",
		Publisher:     "东方财富行情中心",
		SourceURL:     actualURL,
		RetrievalTime: t.currentTime().UTC().Format(time.RFC3339),
		AsOf:          tradeDate,
		Quote: &stockQuoteFacts{
			TradeDate:   tradeDate,
			QuoteTime:   quoteTime.Format(time.RFC3339),
			MarketState: state,
			PriceKind:   priceKind,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       &price,
			PrevClose:   &prevClose,
			Volume:      volume,
			Amount:      amount,
		},
		Valuation: valuation,
	}, nil
}

func (t *StockFactsTool) fetchOfficialReport(ctx context.Context, symbol, exchangeHint string, asOf time.Time) (stockFactsResult, error) {
	exchange, _, _, column, plate, err := stockFactsExchange(symbol, exchangeHint)
	if err != nil {
		return stockFactsResult{}, err
	}
	security, err := t.findCNINFOSecurity(ctx, symbol)
	if err != nil {
		return stockFactsResult{}, err
	}
	start := asOf.AddDate(-5, 0, 0)
	reportAnnouncements, err := t.queryCNINFO(ctx, security, column, plate, start, asOf,
		"category_ndbg_szsh;category_bndbg_szsh;category_yjdbg_szsh;category_sjdbg_szsh", 20)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("query CNINFO periodic reports: %w", err)
	}
	selected, ok := chooseLatestCNINFOReport(reportAnnouncements, symbol, asOf)
	if !ok {
		return stockFactsResult{}, fmt.Errorf("CNINFO returned no complete periodic report for %s through %s", symbol, asOf.Format("2006-01-02"))
	}
	reportURL, err := resolveCNINFOStaticURL(t.cninfoStaticBase, selected.AdjunctURL)
	if err != nil {
		return stockFactsResult{}, err
	}
	pdfBody, actualReportURL, err := t.doGET(ctx, reportURL, "application/pdf", stockFactsMaxPDFBody)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("download CNINFO report PDF: %w", err)
	}
	// doGET validates status and size; the format checks are deliberately kept
	// here because HTML error pages are often returned with HTTP 200.
	if !bytes.HasPrefix(pdfBody, []byte("%PDF")) {
		return stockFactsResult{}, errors.New("CNINFO report body is not a PDF (%PDF signature missing)")
	}
	text, err := t.extractStockReportPDF(pdfBody)
	if err != nil {
		return stockFactsResult{}, err
	}
	if !strings.Contains(text, symbol) && !strings.Contains(text, security.Name) {
		return stockFactsResult{}, errors.New("extracted CNINFO report text does not identify the requested target")
	}
	reportType, period, err := stockReportPeriod(selected.AnnouncementTitle)
	if err != nil {
		return stockFactsResult{}, err
	}
	metrics := parseStockReportMetricsPartial(text)
	unaudited := stockReportUnaudited(text)
	announcementDate := time.UnixMilli(selected.AnnouncementTime).In(stockFactsShanghaiZone).Format("2006-01-02")

	recentStart := asOf.AddDate(-1, 0, 0)
	recent, err := t.queryCNINFO(ctx, security, column, plate, recentStart, asOf, "", 10)
	if err != nil {
		return stockFactsResult{}, fmt.Errorf("query recent CNINFO announcements: %w", err)
	}
	announcements := compactCNINFOAnnouncements(recent, symbol, asOf, t.cninfoStaticBase, 3)
	queryStatus := "observed_zero"
	if len(announcements) > 0 {
		queryStatus = "observed"
	}
	return stockFactsResult{
		Version:       1,
		Provider:      stockFactsProviderOfficialReport,
		Symbol:        symbol,
		Market:        "cn_a",
		Exchange:      exchange,
		CompanyName:   security.Name,
		Currency:      "CNY",
		Publisher:     "巨潮资讯网（法定信息披露平台）",
		SourceURL:     actualReportURL,
		RetrievalTime: t.currentTime().UTC().Format(time.RFC3339),
		AsOf:          period,
		Report: &stockReportFacts{
			Title:            compactStockText(selected.AnnouncementTitle),
			Type:             reportType,
			Period:           period,
			AnnouncementDate: announcementDate,
			Unaudited:        unaudited,
			SourceURL:        actualReportURL,
			Metrics:          metrics,
		},
		Announcements:           announcements,
		AnnouncementQueryStatus: queryStatus,
	}, nil
}

func (t *StockFactsTool) findCNINFOSecurity(ctx context.Context, symbol string) (cninfoSecurity, error) {
	rawURL, err := addStockFactsQuery(t.cninfoSearchEndpoint, map[string]string{"keyWord": symbol, "maxNum": "10"})
	if err != nil {
		return cninfoSecurity{}, err
	}
	body, _, err := t.doPOSTForm(ctx, rawURL, nil, stockFactsMaxJSONBody)
	if err != nil {
		return cninfoSecurity{}, fmt.Errorf("CNINFO security lookup: %w", err)
	}
	var securities []cninfoSecurity
	if err := json.Unmarshal(body, &securities); err != nil {
		return cninfoSecurity{}, fmt.Errorf("decode CNINFO security lookup: %w", err)
	}
	for _, security := range securities {
		if strings.TrimSpace(security.Code) == symbol && strings.TrimSpace(security.OrgID) != "" && strings.TrimSpace(security.Name) != "" && !strings.EqualFold(security.Delisted, "true") {
			security.Name = compactStockText(security.Name)
			return security, nil
		}
	}
	return cninfoSecurity{}, fmt.Errorf("CNINFO lookup did not return active target %s", symbol)
}

func (t *StockFactsTool) queryCNINFO(ctx context.Context, security cninfoSecurity, column, plate string, start, end time.Time, category string, pageSize int) ([]cninfoAnnouncement, error) {
	form := url.Values{}
	form.Set("pageNum", "1")
	form.Set("pageSize", strconv.Itoa(pageSize))
	form.Set("column", column)
	form.Set("tabName", "fulltext")
	form.Set("plate", plate)
	form.Set("stock", security.Code+","+security.OrgID)
	form.Set("searchkey", "")
	form.Set("secid", "")
	form.Set("category", category)
	form.Set("trade", "")
	form.Set("seDate", start.Format("2006-01-02")+"~"+end.Format("2006-01-02"))
	form.Set("sortName", "time")
	form.Set("sortType", "desc")
	form.Set("isHLtitle", "true")
	body, _, err := t.doPOSTForm(ctx, t.cninfoQueryEndpoint, form, stockFactsMaxJSONBody)
	if err != nil {
		return nil, err
	}
	var response cninfoAnnouncementResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode CNINFO announcements: %w", err)
	}
	for _, announcement := range response.Announcements {
		if announcement.SecCode != security.Code || (announcement.OrgID != "" && announcement.OrgID != security.OrgID) {
			return nil, fmt.Errorf("CNINFO announcement target mismatch: %s/%s", announcement.SecCode, announcement.OrgID)
		}
	}
	return response.Announcements, nil
}

func chooseLatestCNINFOReport(items []cninfoAnnouncement, symbol string, asOf time.Time) (cninfoAnnouncement, bool) {
	filtered := make([]cninfoAnnouncement, 0, len(items))
	cutoff := asOf.Format("2006-01-02")
	for _, item := range items {
		title := compactStockText(item.AnnouncementTitle)
		date := time.UnixMilli(item.AnnouncementTime).In(stockFactsShanghaiZone).Format("2006-01-02")
		if item.SecCode != symbol || date > cutoff || item.AdjunctURL == "" || !strings.EqualFold(item.AdjunctType, "PDF") {
			continue
		}
		if strings.Contains(title, "摘要") || strings.Contains(title, "英文") || strings.Contains(title, "取消") {
			continue
		}
		if _, _, err := stockReportPeriod(title); err != nil {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return cninfoAnnouncement{}, false
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].AnnouncementTime > filtered[j].AnnouncementTime
	})
	return filtered[0], true
}

func compactCNINFOAnnouncements(items []cninfoAnnouncement, symbol string, asOf time.Time, staticBase string, limit int) []stockAnnouncement {
	cutoff := asOf.Format("2006-01-02")
	sort.SliceStable(items, func(i, j int) bool { return items[i].AnnouncementTime > items[j].AnnouncementTime })
	result := make([]stockAnnouncement, 0, limit)
	seen := make(map[string]bool)
	for _, item := range items {
		date := time.UnixMilli(item.AnnouncementTime).In(stockFactsShanghaiZone).Format("2006-01-02")
		if item.SecCode != symbol || date > cutoff || item.AdjunctURL == "" {
			continue
		}
		directURL, err := resolveCNINFOStaticURL(staticBase, item.AdjunctURL)
		if err != nil || seen[directURL] {
			continue
		}
		seen[directURL] = true
		result = append(result, stockAnnouncement{Title: compactStockText(item.AnnouncementTitle), Date: date, URL: directURL})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func resolveCNINFOStaticURL(baseURL, adjunct string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" {
		return "", fmt.Errorf("invalid CNINFO static base %q", baseURL)
	}
	ref, err := url.Parse(strings.TrimSpace(adjunct))
	if err != nil || strings.Contains(ref.Path, "..") {
		return "", fmt.Errorf("invalid CNINFO adjunct URL %q", adjunct)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Hostname(), base.Hostname()) {
		return "", errors.New("CNINFO adjunct URL escaped the configured static host")
	}
	return resolved.String(), nil
}

func (t *StockFactsTool) extractStockReportPDF(pdfBody []byte) (string, error) {
	file, err := os.CreateTemp("", "openkitty-stock-report-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temporary report PDF: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(pdfBody); err != nil {
		file.Close()
		return "", fmt.Errorf("write temporary report PDF: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temporary report PDF: %w", err)
	}
	if t.pdfTextExtractor == nil {
		return "", errors.New("PDF text extractor is not configured")
	}
	text := strings.TrimSpace(t.pdfTextExtractor(path))
	if text == "" {
		return "", errors.New("CNINFO report PDF yielded no extractable text")
	}
	return text, nil
}

func stockReportPeriod(title string) (reportType, period string, err error) {
	title = compactStockText(title)
	yearMatch := regexp.MustCompile(`(20[0-9]{2})年`).FindStringSubmatch(title)
	if len(yearMatch) != 2 {
		return "", "", fmt.Errorf("report title lacks a year: %s", title)
	}
	year := yearMatch[1]
	switch {
	case strings.Contains(title, "第一季度报告"):
		return "quarterly", year + "-03-31", nil
	case strings.Contains(title, "半年度报告"):
		return "semiannual", year + "-06-30", nil
	case strings.Contains(title, "第三季度报告"):
		return "quarterly", year + "-09-30", nil
	case strings.Contains(title, "年度报告"):
		return "annual", year + "-12-31", nil
	default:
		return "", "", fmt.Errorf("not a supported periodic report title: %s", title)
	}
}

type stockReportMetricSpec struct {
	key           string
	aliases       []string
	unit          string
	percentMetric bool
}

var stockReportMetricSpecs = []stockReportMetricSpec{
	{"revenue", []string{"营业收入"}, "CNY", false},
	{"parent_net_profit", []string{"归属于上市公司股东的净利润", "归属于母公司股东的净利润"}, "CNY", false},
	{"operating_cash_flow", []string{"经营活动产生的现金流量净额"}, "CNY", false},
	{"eps", []string{"基本每股收益"}, "CNY/share", false},
	{"roe", []string{"加权平均净资产收益率"}, "%", true},
}

func parseStockReportMetrics(text string) (map[string]stockReportMetric, error) {
	metrics := parseStockReportMetricsPartial(text)
	for _, spec := range stockReportMetricSpecs {
		if metrics[spec.key].Status != "observed" {
			return nil, fmt.Errorf("official report is missing structured current/prior/yoy values for %s", spec.key)
		}
	}
	return metrics, nil
}

func parseStockReportMetricsPartial(text string) map[string]stockReportMetric {
	normalized := normalizeStockReportText(text)
	metrics := make(map[string]stockReportMetric, len(stockReportMetricSpecs))
	for _, spec := range stockReportMetricSpecs {
		metric, ok := parseStockReportMetric(normalized, spec.aliases, spec.unit, spec.percentMetric)
		if !ok {
			metrics[spec.key] = stockReportMetric{Unit: spec.unit, Status: "not_found_after_pdf_extraction"}
			continue
		}
		metrics[spec.key] = metric
	}
	return metrics
}

func parseStockReportMetric(text string, aliases []string, unit string, percentMetric bool) (stockReportMetric, bool) {
	for _, alias := range aliases {
		searchFrom := 0
		for {
			relative := strings.Index(text[searchFrom:], alias)
			if relative < 0 {
				break
			}
			matchStart := searchFrom + relative
			start := matchStart + len(alias)
			segment := firstStockRunes(text[start:], 360)
			tokens := stockFactsNumberToken.FindAllString(segment, 12)
			metric, ok := stockMetricFromTokens(tokens, segment, unit, percentMetric)
			if ok {
				if unit == "CNY" {
					scale := stockReportCNYUnitScale(text, matchStart)
					if scale != 1 {
						current, prior := *metric.Current*scale, *metric.Prior*scale
						if !validStockMetricValue(current, unit) || !validStockMetricValue(prior, unit) {
							searchFrom = start
							continue
						}
						metric.Current, metric.Prior = &current, &prior
					}
				}
				return metric, true
			}
			searchFrom = start
			if searchFrom >= len(text) {
				break
			}
		}
	}
	return stockReportMetric{}, false
}

// stockReportCNYUnitScale reads the closest explicit table unit around a
// matched metric label. CNINFO annual reports commonly publish primary
// financial tables in 人民币千元, while other issuers use 元. Treating the raw
// token as yuan silently creates a 1,000x error, so scale only from an explicit
// nearby unit marker and otherwise preserve the historical 1-yuan default.
func stockReportCNYUnitScale(text string, matchStart int) float64 {
	if matchStart < 0 || matchStart > len(text) {
		return 1
	}
	prefix := []rune(text[:matchStart])
	if len(prefix) > 2000 {
		prefix = prefix[len(prefix)-2000:]
	}
	context := string(prefix) + " " + firstStockRunes(text[matchStart:], 120)
	context = strings.NewReplacer("：", ":", "（", "(", "）", ")", " ", "").Replace(context)
	type unitMarker struct {
		text  string
		scale float64
	}
	markers := []unitMarker{
		{"单位:人民币百万元", 1e6}, {"单位:百万元", 1e6}, {"(人民币百万元)", 1e6}, {"(百万元)", 1e6},
		{"单位:人民币万元", 1e4}, {"单位:万元", 1e4}, {"(人民币万元)", 1e4}, {"(万元)", 1e4},
		{"单位:人民币千元", 1e3}, {"单位:千元", 1e3}, {"(人民币千元)", 1e3}, {"(千元)", 1e3},
		{"单位:人民币元", 1}, {"单位:元", 1}, {"(人民币元)", 1}, {"(元)", 1},
	}
	bestEnd, bestLength, scale := -1, -1, 1.0
	for _, marker := range markers {
		if idx := strings.LastIndex(context, marker.text); idx >= 0 {
			end := idx + len(marker.text)
			if end > bestEnd || (end == bestEnd && len(marker.text) > bestLength) {
				bestEnd, bestLength, scale = end, len(marker.text), marker.scale
			}
		}
	}
	return scale
}

func stockMetricFromTokens(tokens []string, segment, unit string, percentMetric bool) (stockReportMetric, bool) {
	values := make([]float64, 0, len(tokens))
	percentValues := make([]float64, 0, len(tokens))
	for _, token := range tokens {
		value, ok := parseStockFactsFloat(strings.TrimRight(strings.TrimSpace(token), "%％"))
		if !ok {
			continue
		}
		if strings.HasSuffix(token, "%") || strings.HasSuffix(token, "％") {
			percentValues = append(percentValues, value)
		} else {
			values = append(values, value)
		}
	}
	var current, prior, yoy float64
	if percentMetric {
		// Go PDF extraction can split a table cell at a glyph boundary (for
		// example, 10.92 becomes "1 0.92"). Enumerate only one- or two-token
		// cells for the first three values and require the reported percentage-
		// point change to reconcile with current minus prior.
		decreasing := strings.Contains(segment, "减少") || strings.Contains(segment, "下降")
		increasing := strings.Contains(segment, "增加") || strings.Contains(segment, "上升")
		parseCell := func(start, width int) (float64, bool) {
			if start < 0 || width < 1 || width > 2 || start+width > len(tokens) {
				return 0, false
			}
			var joined strings.Builder
			for _, token := range tokens[start : start+width] {
				joined.WriteString(strings.TrimRight(strings.TrimSpace(token), "%％"))
			}
			return parseStockFactsFloat(joined.String())
		}
		matched := false
		for currentWidth := 1; currentWidth <= 2 && !matched; currentWidth++ {
			for priorWidth := 1; priorWidth <= 2 && !matched; priorWidth++ {
				for yoyWidth := 1; yoyWidth <= 2; yoyWidth++ {
					candidateCurrent, currentOK := parseCell(0, currentWidth)
					candidatePrior, priorOK := parseCell(currentWidth, priorWidth)
					candidateYoY, yoyOK := parseCell(currentWidth+priorWidth, yoyWidth)
					if !currentOK || !priorOK || !yoyOK {
						continue
					}
					if decreasing {
						candidateYoY = -math.Abs(candidateYoY)
					} else if increasing {
						candidateYoY = math.Abs(candidateYoY)
					}
					if math.Abs((candidateCurrent-candidatePrior)-candidateYoY) <= 0.02 {
						current, prior, yoy = candidateCurrent, candidatePrior, candidateYoY
						matched = true
						break
					}
				}
			}
		}
		if !matched {
			return stockReportMetric{}, false
		}
	} else {
		if len(values) < 2 {
			return stockReportMetric{}, false
		}
		current, prior = values[0], values[1]
		if prior == 0 {
			return stockReportMetric{}, false
		}
		expectedYoY := (current/prior - 1) * 100
		candidates := make([]float64, 0, 3)
		if len(percentValues) > 0 {
			candidates = append(candidates, percentValues[0])
		}
		if len(values) >= 3 {
			candidates = append(candidates, values[2])
		}
		// Some PDF text extractors split a wide table cell such as 205.48
		// into "2 05.48". Only combine the first two candidate fragments,
		// and accept the reconstruction solely when it agrees with the
		// independently calculated current/prior change.
		if len(tokens) >= 4 {
			left := strings.TrimRight(strings.TrimSpace(tokens[2]), "%％")
			right := strings.TrimRight(strings.TrimSpace(tokens[3]), "%％")
			if combined, ok := parseStockFactsFloat(left + right); ok {
				candidates = append(candidates, combined)
			}
		}
		matched := false
		for _, candidate := range candidates {
			if math.Abs(expectedYoY-candidate) <= 0.02 {
				yoy = candidate
				matched = true
				break
			}
		}
		if !matched {
			return stockReportMetric{}, false
		}
	}
	if !validStockMetricValue(current, unit) || !validStockMetricValue(prior, unit) || math.Abs(yoy) > 100000 {
		return stockReportMetric{}, false
	}
	return stockReportMetric{Current: &current, Prior: &prior, YoYPct: &yoy, Unit: unit, Status: "observed"}, true
}

func validStockMetricValue(value float64, unit string) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	switch unit {
	case "CNY":
		return math.Abs(value) <= 1e16
	case "CNY/share":
		return math.Abs(value) <= 1e6
	case "%":
		return math.Abs(value) <= 10000
	default:
		return false
	}
}

func stockReportUnaudited(text string) *bool {
	compact := compactStockText(text)
	if strings.Contains(compact, "未经审计") {
		value := true
		return &value
	}
	if strings.Contains(compact, "审计报告") || strings.Contains(compact, "经审计") {
		value := false
		return &value
	}
	return nil
}

func normalizeStockReportText(text string) string {
	replacer := strings.NewReplacer("，", ",", "％", "%", "（", "(", "）", ")", "\u00a0", " ", "\r", " ", "\n", " ", "\t", " ")
	normalized := replacer.Replace(text)
	normalized = stockFactsSplitDecimal.ReplaceAllString(normalized, "$1.$2")
	return strings.Join(strings.Fields(normalized), " ")
}

func (t *StockFactsTool) doGET(ctx context.Context, rawURL, accept string, maxBytes int64) ([]byte, string, error) {
	endpoint, err := validStockFactsURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "OpenKitty-StockFacts/1.0 (+read-only)")
	if strings.Contains(endpoint.Hostname(), "cninfo.com.cn") {
		req.Header.Set("Referer", "https://www.cninfo.com.cn/")
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, endpoint.String(), err
	}
	defer resp.Body.Close()
	actualURL := endpoint.String()
	if resp.Request != nil && resp.Request.URL != nil {
		actualURL = resp.Request.URL.String()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, actualURL, fmt.Errorf("HTTP %d: %s", resp.StatusCode, compactStockText(string(body)))
	}
	if maxBytes == stockFactsMaxPDFBody {
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
		if contentType != "application/pdf" {
			return nil, actualURL, fmt.Errorf("expected application/pdf, got %q", resp.Header.Get("Content-Type"))
		}
	}
	return readStockFactsBody(resp.Body, maxBytes, actualURL)
}

func (t *StockFactsTool) doPOSTForm(ctx context.Context, rawURL string, form url.Values, maxBytes int64) ([]byte, string, error) {
	endpoint, err := validStockFactsURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	body := ""
	if form != nil {
		body = form.Encode()
	}
	requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json,*/*")
	req.Header.Set("User-Agent", "OpenKitty-StockFacts/1.0 (+read-only)")
	req.Header.Set("Referer", "https://www.cninfo.com.cn/")
	req.Header.Set("Origin", "https://www.cninfo.com.cn")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, endpoint.String(), err
	}
	defer resp.Body.Close()
	actualURL := endpoint.String()
	if resp.Request != nil && resp.Request.URL != nil {
		actualURL = resp.Request.URL.String()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, actualURL, fmt.Errorf("HTTP %d: %s", resp.StatusCode, compactStockText(string(responseBody)))
	}
	return readStockFactsBody(resp.Body, maxBytes, actualURL)
}

func readStockFactsBody(reader io.Reader, maxBytes int64, actualURL string) ([]byte, string, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, actualURL, err
	}
	if int64(len(body)) > maxBytes {
		return nil, actualURL, fmt.Errorf("HTTP response exceeds %d bytes", maxBytes)
	}
	return body, actualURL, nil
}

func validStockFactsURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid stock-facts endpoint %q", raw)
	}
	return parsed, nil
}

func addStockFactsQuery(raw string, values map[string]string) (string, error) {
	parsed, err := validStockFactsURL(raw)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, value := range values {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func validateStockFactsResult(result stockFactsResult, symbol, provider string, requestedAsOf time.Time) error {
	if result.Version != 1 || result.Provider != provider || result.Symbol != symbol || result.Market != "cn_a" {
		return errors.New("result identity/version does not match the request")
	}
	if result.Exchange == "" || result.CompanyName == "" || result.Currency != "CNY" || result.Publisher == "" || result.SourceURL == "" {
		return errors.New("result provenance or identity is incomplete")
	}
	if _, err := validStockFactsURL(result.SourceURL); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, result.RetrievalTime); err != nil {
		return fmt.Errorf("invalid retrieval_time: %w", err)
	}
	asOf, err := time.ParseInLocation("2006-01-02", result.AsOf, stockFactsShanghaiZone)
	if err != nil || asOf.After(requestedAsOf) {
		return fmt.Errorf("result as_of %q exceeds or does not match the requested cutoff", result.AsOf)
	}
	if provider == stockFactsProviderTencentMarket {
		if result.Quote == nil || result.History == nil || result.History.Adjustment != "none" || result.History.TotalReturnIncludesDividends || result.History.Observations < 61 {
			return errors.New("Tencent result lacks complete unadjusted quote/history evidence")
		}
		if result.Quote.PriceKind != "latest_completed_daily_bar" || result.Quote.TradeDate != result.History.LastDate {
			return errors.New("Tencent latest completed close and history are inconsistent")
		}
	}
	if provider == stockFactsProviderEastmoney {
		if result.Quote == nil || result.Quote.Close == nil || result.Valuation == nil {
			return errors.New("Eastmoney result lacks quote or valuation evidence")
		}
	}
	if provider == stockFactsProviderOfficialReport {
		if result.Report == nil || len(result.Report.Metrics) != 5 || result.Report.SourceURL != result.SourceURL {
			return errors.New("official result lacks the complete report metric set")
		}
		expectedUnits := map[string]string{"revenue": "CNY", "parent_net_profit": "CNY", "operating_cash_flow": "CNY", "eps": "CNY/share", "roe": "%"}
		for _, key := range []string{"revenue", "parent_net_profit", "operating_cash_flow", "eps", "roe"} {
			metric, ok := result.Report.Metrics[key]
			if !ok || metric.Unit != expectedUnits[key] {
				return fmt.Errorf("official report metric %s is missing or has the wrong unit", key)
			}
			switch metric.Status {
			case "observed":
				if metric.Current == nil || metric.Prior == nil || metric.YoYPct == nil {
					return fmt.Errorf("official report metric %s is marked observed but incomplete", key)
				}
			case "not_found_after_pdf_extraction":
				if metric.Current != nil || metric.Prior != nil || metric.YoYPct != nil {
					return fmt.Errorf("official report metric %s has values despite missing status", key)
				}
			default:
				return fmt.Errorf("official report metric %s has invalid status %q", key, metric.Status)
			}
		}
		if result.AnnouncementQueryStatus != "observed" && result.AnnouncementQueryStatus != "observed_zero" {
			return errors.New("invalid official announcement query status")
		}
		if len(result.Announcements) > 3 {
			return errors.New("official announcement list exceeds the compact limit")
		}
	}
	return nil
}

func renderStockFactsResult(result stockFactsResult) (string, error) {
	markerJSON, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode stock facts marker: %w", err)
	}
	if len(markerJSON) > stockFactsMaxMarkerSize {
		return "", fmt.Errorf("stock facts marker is %d bytes; compact limit is %d", len(markerJSON), stockFactsMaxMarkerSize)
	}
	var out strings.Builder
	// The marker must be the first line: the host stores only the first 2,000
	// characters for deterministic finalization evidence.
	fmt.Fprintf(&out, "%s%s\n", stockFactsResultMarker, markerJSON)
	fmt.Fprintf(&out, "# Structured stock facts — %s\n", result.Publisher)
	fmt.Fprintf(&out, "Provider key: %s\nTarget: %s %s (%s/%s)\n", result.Provider, result.CompanyName, result.Symbol, result.Market, result.Exchange)
	fmt.Fprintf(&out, "Retrieval time: %s\n", result.RetrievalTime)
	if requested, err := time.Parse("2006-01-02", result.RequestedAsOf); err == nil {
		fmt.Fprintf(&out, "Requested cutoff: %s (%s)\n", result.RequestedAsOf, requested.Weekday())
		if result.Quote != nil && (requested.Weekday() == time.Saturday || requested.Weekday() == time.Sunday) && result.AsOf < result.RequestedAsOf {
			fmt.Fprintf(&out, "Calendar note: the requested cutoff is a weekend. A prior trading-day result is expected; do not label the weekend as a missing session, unpublished quote, or fetch failure.\n")
		}
	}
	fmt.Fprintf(&out, "Evidence as_of: %s\nSource: %s\n", result.AsOf, result.SourceURL)
	if result.Quote != nil {
		q := result.Quote
		fmt.Fprintf(&out, "Quote: trade_date=%s; quote_time=%s; state=%s; kind=%s; O/H/L/C=%s/%s/%s/%s; prev_close=%s; volume=%s shares; amount=%s CNY\n",
			q.TradeDate, q.QuoteTime, q.MarketState, q.PriceKind, stockFloatText(q.Open), stockFloatText(q.High), stockFloatText(q.Low), stockFloatText(q.Close), stockFloatText(q.PrevClose), stockIntText(q.Volume), stockFloatText(q.Amount))
	}
	if result.History != nil {
		h := result.History
		fmt.Fprintf(&out, "Unadjusted price history: %d rows %s..%s; returns 5d/20d/60d/full-period=%.4f%%/%.4f%%/%.4f%%/%.4f%%; MA5/20/60=%.4f/%.4f/%.4f; ranges20/60=%.4f..%.4f/%.4f..%.4f. Cash dividends are NOT included.\n",
			h.Observations, h.FirstDate, h.LastDate, h.Return5DPct, h.Return20DPct, h.Return60DPct, h.ReturnPeriodPct, h.MA5, h.MA20, h.MA60, h.Range20Low, h.Range20High, h.Range60Low, h.Range60High)
	}
	if result.Valuation != nil {
		v := result.Valuation
		fmt.Fprintf(&out, "Provider-labelled valuation (%s): dynamic_PE=%s; static_PE=%s; TTM_PE=%s; PB=%s; market_cap=%s CNY. Preserve this provider's labels; do not average with another provider.\n",
			v.AsOf, stockFloatText(v.PEDynamic), stockFloatText(v.PEStatic), stockFloatText(v.PETTM), stockFloatText(v.PB), stockFloatText(v.MarketCap))
	}
	if result.Report != nil {
		r := result.Report
		fmt.Fprintf(&out, "Official report: %s; type=%s; period=%s; announced=%s; unaudited=%s; Source=%s\n", r.Title, r.Type, r.Period, r.AnnouncementDate, stockBoolText(r.Unaudited), r.SourceURL)
		missingMetrics := 0
		for _, key := range []string{"revenue", "parent_net_profit", "operating_cash_flow", "eps", "roe"} {
			metric := r.Metrics[key]
			fmt.Fprintf(&out, "- %s: current=%s; prior=%s; yoy=%s%%; unit=%s; status=%s\n", key, stockFloatText(metric.Current), stockFloatText(metric.Prior), stockFloatText(metric.YoYPct), metric.Unit, metric.Status)
			if metric.Status == "not_found_after_pdf_extraction" {
				missingMetrics++
			}
		}
		if missingMetrics > 0 {
			fmt.Fprintf(&out, "Metric coverage is partial (%d/5 missing after PDF extraction). This is a successful official-source lookup: preserve the report metadata, mark those values unknown, and do not repeat generic web searches merely to fill them.\n", missingMetrics)
		}
		fmt.Fprintf(&out, "Recent official announcement query: %s. Titles below are disclosure facts only, not proof of a risk event.\n", result.AnnouncementQueryStatus)
		for _, announcement := range result.Announcements {
			fmt.Fprintf(&out, "- %s | %s | %s\n", announcement.Date, announcement.Title, announcement.URL)
		}
	}
	return out.String(), nil
}

func RenderStockFactsResult(result StockFactsResult) (string, error) {
	return renderStockFactsResult(result)
}

func stockMarketState(quoteTime, now time.Time) string {
	quoteLocal := quoteTime.In(stockFactsShanghaiZone)
	nowLocal := now.In(stockFactsShanghaiZone)
	if quoteLocal.Format("2006-01-02") < nowLocal.Format("2006-01-02") || quoteLocal.Hour() >= 15 {
		return "closed"
	}
	return "intraday"
}

func validateOHLC(open, high, low, close float64) error {
	for _, value := range []float64{open, high, low, close} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 10000000 {
			return fmt.Errorf("invalid price %.8g", value)
		}
	}
	if high < low || high < open || high < close || low > open || low > close {
		return fmt.Errorf("inconsistent OHLC %.4f/%.4f/%.4f/%.4f", open, high, low, close)
	}
	return nil
}

func parseStockFactsFloat(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "--" || strings.EqualFold(raw, "null") {
		return 0, false
	}
	negative := strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")")
	raw = strings.Trim(raw, "()")
	raw = strings.ReplaceAll(raw, ",", "")
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	if negative {
		value = -value
	}
	return value, true
}

func optionalPositiveStockFloat(raw string) *float64 {
	value, ok := parseStockFactsFloat(raw)
	if !ok || value <= 0 {
		return nil
	}
	return &value
}

func stockValuationStatus(value *float64) string {
	if value == nil {
		return "not_provided"
	}
	if *value <= 0 {
		return "not_applicable"
	}
	return "observed"
}

func stockRawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value), true
	}
	return strings.TrimSpace(string(raw)), true
}

func stockRawFloat(raw json.RawMessage) (float64, bool) {
	value, ok := stockRawString(raw)
	if !ok {
		return 0, false
	}
	return parseStockFactsFloat(value)
}

func stockRawInt64(raw json.RawMessage) (int64, bool) {
	value, ok := stockRawFloat(raw)
	if !ok || value != math.Trunc(value) || value > math.MaxInt64 || value < math.MinInt64 {
		return 0, false
	}
	return int64(value), true
}

func requiredScaledEastmoney(data map[string]json.RawMessage, key string, scale float64) (float64, error) {
	value, ok := stockRawFloat(data[key])
	if !ok {
		return 0, fmt.Errorf("Eastmoney %s is missing or invalid", key)
	}
	value /= scale
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("Eastmoney %s has invalid scaled value", key)
	}
	return value, nil
}

func optionalScaledEastmoney(data map[string]json.RawMessage, key string, scale float64) *float64 {
	value, ok := stockRawFloat(data[key])
	if !ok {
		return nil
	}
	value /= scale
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return nil
	}
	return &value
}

func optionalRawEastmoneyFloat(data map[string]json.RawMessage, key string) *float64 {
	value, ok := stockRawFloat(data[key])
	if !ok || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func optionalEastmoneyInt64(data map[string]json.RawMessage, key string, multiplier int64) *int64 {
	value, ok := stockRawFloat(data[key])
	if !ok || value < 0 || value > float64(math.MaxInt64/multiplier) {
		return nil
	}
	converted := int64(math.Round(value * float64(multiplier)))
	return &converted
}

func roundedStockFact(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}

func compactStockText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func firstStockRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func stockFloatText(value *float64) string {
	if value == nil {
		return "not_provided"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func stockIntText(value *int64) string {
	if value == nil {
		return "not_provided"
	}
	return strconv.FormatInt(*value, 10)
}

func stockBoolText(value *bool) string {
	if value == nil {
		return "not_stated"
	}
	return strconv.FormatBool(*value)
}
