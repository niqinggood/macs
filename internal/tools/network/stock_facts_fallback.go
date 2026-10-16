package network

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mac/internal/toolcontract"
)

var stockFallbackISODatePattern = regexp.MustCompile(`\b(20[0-9]{2})-([01]?[0-9])-([0-3]?[0-9])\b`)

const stockFactsStructuredPrefix = "OPENKITTY_STOCK_FACTS_RESULT="

type stockFallbackPayload struct {
	Version                 int                         `json:"version"`
	Provider                string                      `json:"provider"`
	Symbol                  string                      `json:"symbol"`
	Market                  string                      `json:"market"`
	Exchange                string                      `json:"exchange"`
	CompanyName             string                      `json:"company_name"`
	Currency                string                      `json:"currency"`
	Publisher               string                      `json:"publisher"`
	SourceURL               string                      `json:"source_url"`
	RetrievalTime           string                      `json:"retrieval_time"`
	AsOf                    string                      `json:"as_of"`
	Quote                   *stockFallbackQuote         `json:"quote,omitempty"`
	History                 *stockFallbackHistory       `json:"history,omitempty"`
	Valuation               *stockFallbackValuation     `json:"valuation,omitempty"`
	Report                  *stockFallbackReport        `json:"report,omitempty"`
	Announcements           []stockFallbackAnnouncement `json:"announcements,omitempty"`
	AnnouncementQueryStatus string                      `json:"announcement_query_status,omitempty"`
}

type stockFallbackQuote struct {
	TradeDate   string   `json:"trade_date"`
	QuoteTime   string   `json:"quote_time"`
	MarketState string   `json:"market_state"`
	PriceKind   string   `json:"price_kind"`
	Open        *float64 `json:"open"`
	High        *float64 `json:"high"`
	Low         *float64 `json:"low"`
	Close       *float64 `json:"close"`
	PrevClose   *float64 `json:"prev_close"`
	Volume      *float64 `json:"volume"`
	Amount      *float64 `json:"amount"`
}

type stockFallbackHistory struct {
	Adjustment                   string   `json:"adjustment"`
	TotalReturnIncludesDividends bool     `json:"total_return_includes_dividends"`
	Observations                 int      `json:"observations"`
	FirstDate                    string   `json:"first_date"`
	LastDate                     string   `json:"last_date"`
	Return5DPct                  *float64 `json:"return_5d_pct"`
	Return20DPct                 *float64 `json:"return_20d_pct"`
	Return60DPct                 *float64 `json:"return_60d_pct"`
	MA5                          *float64 `json:"ma5"`
	MA20                         *float64 `json:"ma20"`
	MA60                         *float64 `json:"ma60"`
	Range20Low                   *float64 `json:"range20_low"`
	Range20High                  *float64 `json:"range20_high"`
	Range60Low                   *float64 `json:"range60_low"`
	Range60High                  *float64 `json:"range60_high"`
}

type stockFallbackValuation struct {
	AsOf      string   `json:"as_of"`
	PEDynamic *float64 `json:"pe_dynamic"`
	PEStatic  *float64 `json:"pe_static"`
	PETTM     *float64 `json:"pe_ttm"`
	PB        *float64 `json:"pb"`
	MarketCap *float64 `json:"market_cap"`
}

type stockFallbackReport struct {
	Title            string                         `json:"title"`
	Type             string                         `json:"type"`
	Period           string                         `json:"period"`
	AnnouncementDate string                         `json:"announcement_date"`
	Unaudited        *bool                          `json:"unaudited"`
	SourceURL        string                         `json:"source_url"`
	Metrics          map[string]stockFallbackMetric `json:"metrics"`
}

type stockFallbackMetric struct {
	Current *float64 `json:"current"`
	Prior   *float64 `json:"prior"`
	YoyPct  *float64 `json:"yoy_pct"`
	Unit    string   `json:"unit"`
	Status  string   `json:"status"`
}

type stockFallbackAnnouncement struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	URL   string `json:"url"`
}

type parsedStockFacts struct {
	result stockFallbackPayload
	host   string
}

var (
	stockFactsIntentPattern = regexp.MustCompile(`(?i)(?:股票|A股|股价|行情|估值|财报|财务|上市公司|stock|equity|shares?)`)
	stockSymbolPattern      = regexp.MustCompile(`(?m)(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`)
	stockOtherDomainPattern = regexp.MustCompile(`(?i)(?:天气|油价|成品油|新闻|热点|航班|酒店|weather|fuel|headline|flight)`)
	stockComparePattern     = regexp.MustCompile(`(?i)(?:对比|比较|加入|两只|两家|多股|组合|压力情景|压力测试|compare|versus|portfolio|stress\s+test)`)
	stockHistoryPattern     = regexp.MustCompile(`(?i)(?:沿用|根据刚才|上一轮|上一次|继续上轮|previous\s+turn|as\s+above)`)
	stockSpecialBankPattern = regexp.MustCompile(`(?i)(?:净息差|不良率|拨备覆盖率|资产质量|信用成本|net\s+interest\s+margin|non.?performing)`)
	stockExtendedAskPattern = regexp.MustCompile(`(?i)(?:以及|还持有|同时分析|并分析|行业排名|宏观|批价|目标价|资金流|北向|龙虎榜|市占率|库存|半年走势|一年走势|年初至今|演示稿|幻灯片|roadshow|fund\s*flow|target\s*price)`)
)

type StockFactsFallbackOptions struct {
	IsFastReadOnly func(string) bool
}

func BuildStockFactsFallback(input string, toolResults []string, options ...StockFactsFallbackOptions) string {
	var opts StockFactsFallbackOptions
	if len(options) > 0 {
		opts = options[0]
	}
	symbol, ok := standaloneStockSymbol(input, opts.IsFastReadOnly)
	if !ok {
		return ""
	}
	byProvider := make(map[string]parsedStockFacts)
	conflicts := make(map[string]bool)
	for _, raw := range toolResults {
		parsed, valid := parseStockFactsToolResult(input, symbol, raw)
		if !valid {
			continue
		}
		provider := parsed.result.Provider
		if previous, exists := byProvider[provider]; exists {
			left, _ := json.Marshal(previous.result)
			right, _ := json.Marshal(parsed.result)
			if string(left) != string(right) {
				conflicts[provider] = true
			}
			continue
		}
		byProvider[provider] = parsed
	}
	for provider := range conflicts {
		delete(byProvider, provider)
	}
	for _, provider := range []string{"tencent_market", "eastmoney_market", "official_report"} {
		if _, exists := byProvider[provider]; !exists {
			return ""
		}
	}
	if !consistentStockFacts(byProvider, symbol) {
		return ""
	}
	return renderStockFactsFallback(input, byProvider)
}

func standaloneStockSymbol(input string, isFastReadOnly func(string) bool) (string, bool) {
	if isFastReadOnly == nil || !isFastReadOnly(input) || !stockFactsIntentPattern.MatchString(input) ||
		stockOtherDomainPattern.MatchString(input) || stockComparePattern.MatchString(input) ||
		stockHistoryPattern.MatchString(input) || stockSpecialBankPattern.MatchString(input) || stockExtendedAskPattern.MatchString(input) ||
		len(stockFallbackISODatePattern.FindAllString(input, -1)) > 1 {
		return "", false
	}
	unique := make(map[string]bool)
	for _, match := range stockSymbolPattern.FindAllStringSubmatch(input, -1) {
		if len(match) == 2 {
			unique[match[1]] = true
		}
	}
	if len(unique) != 1 {
		return "", false
	}
	for symbol := range unique {
		return symbol, true
	}
	return "", false
}

func parseStockFactsToolResult(input, symbol, raw string) (parsedStockFacts, bool) {
	record, ok := toolcontract.ParseRecord(raw)
	if !ok || !record.Succeeded || !strings.EqualFold(strings.TrimSpace(record.Tool), "stock_facts") ||
		!strings.EqualFold(strings.TrimSpace(record.SearchMode), "direct") {
		return parsedStockFacts{}, false
	}
	var markers []stockFallbackPayload
	for _, line := range strings.Split(raw, "\n") {
		if idx := strings.Index(line, stockFactsStructuredPrefix); idx >= 0 {
			var marker stockFallbackPayload
			if err := json.Unmarshal([]byte(strings.TrimSpace(line[idx+len(stockFactsStructuredPrefix):])), &marker); err == nil {
				markers = append(markers, marker)
			}
		}
	}
	if len(markers) != 1 {
		return parsedStockFacts{}, false
	}
	marker := markers[0]
	marker.Provider = strings.ToLower(strings.TrimSpace(marker.Provider))
	marker.Symbol = strings.TrimSpace(marker.Symbol)
	if marker.Version != 1 || marker.Symbol != symbol || !strings.Contains(input, symbol) ||
		(marker.Provider != "tencent_market" && marker.Provider != "eastmoney_market" && marker.Provider != "official_report") {
		return parsedStockFacts{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(marker.Market), "cn_a") || marker.Exchange == "" ||
		marker.CompanyName == "" || marker.Publisher == "" || !strings.EqualFold(strings.TrimSpace(marker.Currency), "CNY") {
		return parsedStockFacts{}, false
	}
	if _, err := time.Parse(time.RFC3339, marker.RetrievalTime); err != nil {
		return parsedStockFacts{}, false
	}
	if _, err := time.Parse("2006-01-02", marker.AsOf); err != nil {
		return parsedStockFacts{}, false
	}
	parsedURL, err := url.Parse(strings.TrimSpace(marker.SourceURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Hostname() == "" {
		return parsedStockFacts{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(parsedURL.Hostname()), "www.")
	if !allowedStockProviderHost(marker.Provider, host) || !allowedStockProviderHost(marker.Provider, sourceHost(record.SourceURL)) {
		return parsedStockFacts{}, false
	}
	if marker.Provider == "official_report" {
		if !validOfficialStockPayload(input, marker) {
			return parsedStockFacts{}, false
		}
	} else if !validMarketStockPayload(input, marker) {
		return parsedStockFacts{}, false
	}
	return parsedStockFacts{result: marker, host: host}, true
}

func allowedStockProviderHost(provider, host string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	switch provider {
	case "tencent_market":
		return host == "web.ifzq.gtimg.cn" || host == "qt.gtimg.cn"
	case "eastmoney_market":
		return host == "push2.eastmoney.com" || host == "push2delay.eastmoney.com"
	case "official_report":
		return host == "cninfo.com.cn" || host == "static.cninfo.com.cn"
	default:
		return false
	}
}

func validMarketStockPayload(input string, marker stockFallbackPayload) bool {
	quote := marker.Quote
	if quote == nil || quote.Close == nil || quote.Open == nil || quote.High == nil || quote.Low == nil || quote.PrevClose == nil ||
		strings.ToLower(strings.TrimSpace(quote.MarketState)) != "closed" {
		return false
	}
	if _, err := time.Parse(time.RFC3339, quote.QuoteTime); err != nil {
		return false
	}
	tradeDate, err := time.Parse("2006-01-02", quote.TradeDate)
	if err != nil || !validStockNumber(*quote.Close, 0, 100000) || !validStockNumber(*quote.Open, 0, 100000) ||
		!validStockNumber(*quote.High, 0, 100000) || !validStockNumber(*quote.Low, 0, 100000) ||
		!validStockNumber(*quote.PrevClose, 0, 100000) || *quote.Low > math.Min(*quote.Open, *quote.Close)+0.001 ||
		*quote.High+0.001 < math.Max(*quote.Open, *quote.Close) {
		return false
	}
	target := stockFallbackRequestDate(input, marker)
	if tradeDate.After(target) || target.Sub(tradeDate) > 10*24*time.Hour {
		return false
	}
	if marker.Valuation == nil || marker.Valuation.PETTM == nil || marker.Valuation.MarketCap == nil ||
		!validStockNumber(*marker.Valuation.PETTM, -1000, 10000) || !validStockNumber(*marker.Valuation.MarketCap, 0, 1e17) {
		return false
	}
	if marker.Provider == "tencent_market" {
		if quote.PriceKind != "latest_completed_daily_bar" {
			return false
		}
		h := marker.History
		if h == nil || h.Adjustment != "none" || h.TotalReturnIncludesDividends || h.Observations < 61 ||
			h.LastDate != quote.TradeDate || h.Return5DPct == nil || h.Return20DPct == nil || h.Return60DPct == nil ||
			h.MA5 == nil || h.MA20 == nil || h.MA60 == nil || h.Range20Low == nil || h.Range20High == nil ||
			h.Range60Low == nil || h.Range60High == nil {
			return false
		}
	} else if quote.PriceKind != "latest_close" {
		return false
	}
	return true
}

func validOfficialStockPayload(input string, marker stockFallbackPayload) bool {
	report := marker.Report
	if report == nil || report.Title == "" || report.Type == "" || report.Period == "" || report.AnnouncementDate == "" || report.SourceURL == "" ||
		report.Unaudited == nil || marker.AsOf != report.Period || canonicalCitationURLKey(marker.SourceURL) != canonicalCitationURLKey(report.SourceURL) {
		return false
	}
	period, err := time.Parse("2006-01-02", report.Period)
	if err != nil {
		return false
	}
	announcementDate, err := time.Parse("2006-01-02", report.AnnouncementDate)
	if err != nil {
		return false
	}
	target := stockFallbackRequestDate(input, marker)
	if period.After(announcementDate) || announcementDate.After(target) {
		return false
	}
	reportURL, err := url.Parse(report.SourceURL)
	if err != nil || reportURL.Scheme != "https" || !allowedStockProviderHost("official_report", sourceHost(report.SourceURL)) {
		return false
	}
	expectedUnits := map[string]string{"revenue": "CNY", "parent_net_profit": "CNY", "operating_cash_flow": "CNY", "eps": "CNY/share", "roe": "%"}
	for _, key := range []string{"revenue", "parent_net_profit", "operating_cash_flow", "eps", "roe"} {
		metric, ok := report.Metrics[key]
		if !ok || metric.Current == nil || metric.Prior == nil || metric.YoyPct == nil || metric.Unit != expectedUnits[key] ||
			(metric.Status != "observed" && metric.Status != "verified") || !finiteStockNumber(*metric.Current) ||
			!finiteStockNumber(*metric.Prior) || !finiteStockNumber(*metric.YoyPct) {
			return false
		}
	}
	if marker.AnnouncementQueryStatus != "observed" && marker.AnnouncementQueryStatus != "observed_zero" {
		return false
	}
	if len(marker.Announcements) > 3 || (marker.AnnouncementQueryStatus == "observed" && len(marker.Announcements) == 0) ||
		(marker.AnnouncementQueryStatus == "observed_zero" && len(marker.Announcements) != 0) {
		return false
	}
	for _, announcement := range marker.Announcements {
		if announcement.Title == "" {
			return false
		}
		announcementDay, err := time.Parse("2006-01-02", announcement.Date)
		if err != nil || announcementDay.After(target) {
			return false
		}
		announcementURL, err := url.Parse(announcement.URL)
		if err != nil || announcementURL.Scheme != "https" || !allowedStockProviderHost("official_report", sourceHost(announcement.URL)) {
			return false
		}
	}
	return true
}

func validStockNumber(value, minimum, maximum float64) bool {
	return finiteStockNumber(value) && value > minimum && value < maximum
}

func finiteStockNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func stockFallbackRequestDate(input string, marker stockFallbackPayload) time.Time {
	if match := stockFallbackISODatePattern.FindStringSubmatch(input); len(match) == 4 {
		if parsed, err := time.Parse("2006-1-2", strings.Join(match[1:], "-")); err == nil {
			return parsed
		}
	}
	if retrieved, err := time.Parse(time.RFC3339, marker.RetrievalTime); err == nil {
		local := retrieved.In(time.FixedZone("Asia/Shanghai", 8*60*60))
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	}
	parsed, _ := time.Parse("2006-01-02", marker.AsOf)
	return parsed
}

func consistentStockFacts(byProvider map[string]parsedStockFacts, symbol string) bool {
	var company, exchange, tradeDate string
	var closeValue float64
	for _, provider := range []string{"tencent_market", "eastmoney_market", "official_report"} {
		parsed := byProvider[provider].result
		if parsed.Symbol != symbol {
			return false
		}
		normalizedCompany := strings.ToLower(strings.Join(strings.Fields(parsed.CompanyName), ""))
		if company == "" {
			company = normalizedCompany
			exchange = strings.ToUpper(strings.TrimSpace(parsed.Exchange))
		} else if normalizedCompany != company || strings.ToUpper(strings.TrimSpace(parsed.Exchange)) != exchange {
			return false
		}
		if parsed.Quote != nil {
			if tradeDate == "" {
				tradeDate = parsed.Quote.TradeDate
				closeValue = *parsed.Quote.Close
			} else if parsed.Quote.TradeDate != tradeDate || math.Abs(*parsed.Quote.Close-closeValue) > 0.011 {
				return false
			}
		}
	}
	if tradeDate == "" {
		return false
	}
	return consistentStockQuotes(byProvider["tencent_market"].result.Quote, byProvider["eastmoney_market"].result.Quote)
}

func consistentStockQuotes(first, second *stockFallbackQuote) bool {
	if first == nil || second == nil || first.TradeDate != second.TradeDate {
		return false
	}
	for _, pair := range [][2]*float64{
		{first.Open, second.Open}, {first.High, second.High}, {first.Low, second.Low},
		{first.Close, second.Close}, {first.PrevClose, second.PrevClose},
	} {
		if pair[0] == nil || pair[1] == nil || math.Abs(*pair[0]-*pair[1]) > 0.011 {
			return false
		}
	}
	if first.Amount != nil && second.Amount != nil {
		tolerance := math.Max(5e6, math.Max(math.Abs(*first.Amount), math.Abs(*second.Amount))*0.002)
		if math.Abs(*first.Amount-*second.Amount) > tolerance {
			return false
		}
	}
	return true
}

func renderStockFactsFallback(input string, byProvider map[string]parsedStockFacts) string {
	tencent := byProvider["tencent_market"].result
	eastmoney := byProvider["eastmoney_market"].result
	official := byProvider["official_report"].result
	quote := tencent.Quote
	history := tencent.History
	report := official.Report
	target := stockFallbackRequestDate(input, tencent).Format("2006-01-02")

	var out strings.Builder
	fmt.Fprintf(&out, "# %s（%s）事实型分析（截至 %s）\n\n", tencent.CompanyName, tencent.Symbol, target)
	out.WriteString("## 数据截至时间与直接来源\n\n")
	for _, item := range []stockFallbackPayload{tencent, eastmoney, official} {
		fmt.Fprintf(&out, "- %s：retrieval_time=%s；as_of=%s；[来源直链](%s)\n", item.Publisher, item.RetrievalTime, item.AsOf, item.SourceURL)
	}

	out.WriteString("\n## 已核实事实\n\n### 最新已完成交易日行情\n\n")
	out.WriteString("| 项目 | 腾讯行情 | 东方财富 | 核验 |\n|---|---:|---:|---|\n")
	fmt.Fprintf(&out, "| 交易日 | %s | %s | %s |\n", quote.TradeDate, eastmoney.Quote.TradeDate, stockAgreementText(quote.TradeDate == eastmoney.Quote.TradeDate))
	fmt.Fprintf(&out, "| 收盘价（元） | %.2f | %.2f | %s |\n", *quote.Close, *eastmoney.Quote.Close, stockNumberAgreement(*quote.Close, *eastmoney.Quote.Close, 0.011))
	fmt.Fprintf(&out, "| 开 / 高 / 低（元） | %.2f / %.2f / %.2f | %.2f / %.2f / %.2f | %s |\n",
		*quote.Open, *quote.High, *quote.Low, *eastmoney.Quote.Open, *eastmoney.Quote.High, *eastmoney.Quote.Low,
		stockOHLCVerification(quote, eastmoney.Quote))
	fmt.Fprintf(&out, "| 昨收（元） | %.2f | %.2f | %s |\n", *quote.PrevClose, *eastmoney.Quote.PrevClose, stockNumberAgreement(*quote.PrevClose, *eastmoney.Quote.PrevClose, 0.011))
	if quote.Amount != nil && eastmoney.Quote.Amount != nil {
		fmt.Fprintf(&out, "| 成交额（亿元） | %.2f | %.2f | %s |\n", *quote.Amount/1e8, *eastmoney.Quote.Amount/1e8, stockNumberAgreement(*quote.Amount, *eastmoney.Quote.Amount, 5e6))
	}

	out.WriteString("\n### 近期走势（未复权价格变动，不含现金分红）\n\n")
	fmt.Fprintf(&out, "- 数据窗口：%s 至 %s，共 %d 个交易日观测。\n", history.FirstDate, history.LastDate, history.Observations)
	fmt.Fprintf(&out, "- 相对 5 / 20 / 60 个交易日前收盘：**%+.2f%% / %+.2f%% / %+.2f%%**。\n", *history.Return5DPct, *history.Return20DPct, *history.Return60DPct)
	fmt.Fprintf(&out, "- MA5 / MA20 / MA60：%.2f / %.2f / %.2f 元。\n", *history.MA5, *history.MA20, *history.MA60)
	fmt.Fprintf(&out, "- 20日区间 %.2f–%.2f 元；60日区间 %.2f–%.2f 元。以上是价格口径，不是含分红总回报。\n", *history.Range20Low, *history.Range20High, *history.Range60Low, *history.Range60High)

	out.WriteString("\n### 估值口径（供应商口径并列，不平均）\n\n")
	out.WriteString("| 指标 | 腾讯 | 东方财富 | 说明 |\n|---|---:|---:|---|\n")
	writeStockValuationRow(&out, "动态 PE", tencent.Valuation.PEDynamic, eastmoney.Valuation.PEDynamic)
	writeStockValuationRow(&out, "静态 PE", tencent.Valuation.PEStatic, eastmoney.Valuation.PEStatic)
	writeStockValuationRow(&out, "PE TTM", tencent.Valuation.PETTM, eastmoney.Valuation.PETTM)
	writeStockValuationRow(&out, "PB", tencent.Valuation.PB, eastmoney.Valuation.PB)
	if tencent.Valuation.MarketCap != nil && eastmoney.Valuation.MarketCap != nil {
		fmt.Fprintf(&out, "| 总市值 | %.2f亿元 | %.2f亿元 | %s |\n", *tencent.Valuation.MarketCap/1e8, *eastmoney.Valuation.MarketCap/1e8, stockNumberAgreement(*tencent.Valuation.MarketCap, *eastmoney.Valuation.MarketCap, 1e9))
	}

	out.WriteString("\n### 最近一期官方财务报告\n\n")
	fmt.Fprintf(&out, "- 报告：[%s](%s)；报告期 %s；公告日 %s；审计口径：%s。\n\n", report.Title, report.SourceURL, report.Period, report.AnnouncementDate, stockAuditLabel(report.Unaudited))
	out.WriteString("| 指标 | 本期 | 上年同期 | 同比/变化 |\n|---|---:|---:|---:|\n")
	for _, key := range []string{"revenue", "parent_net_profit", "operating_cash_flow", "eps", "roe"} {
		metric := report.Metrics[key]
		change := fmt.Sprintf("%+.2f%%", *metric.YoyPct)
		if key == "roe" {
			change = fmt.Sprintf("%+.2f个百分点", *metric.YoyPct)
		}
		fmt.Fprintf(&out, "| %s | %s | %s | %s |\n", stockMetricLabel(key), formatStockMetric(*metric.Current, metric.Unit), formatStockMetric(*metric.Prior, metric.Unit), change)
	}

	if official.AnnouncementQueryStatus == "observed" && len(official.Announcements) > 0 {
		out.WriteString("\n### 最近官方公告（只列标题，不据标题推导事件）\n\n")
		for _, announcement := range official.Announcements {
			fmt.Fprintf(&out, "- %s：[%s](%s)\n", announcement.Date, announcement.Title, announcement.URL)
		}
	} else {
		out.WriteString("\n- 官方公告查询已完成，当前查询窗口内未返回额外公告；这不等于公司不存在任何风险。\n")
	}

	revenue := report.Metrics["revenue"]
	profit := report.Metrics["parent_net_profit"]
	cashFlow := report.Metrics["operating_cash_flow"]
	out.WriteString("\n## 透明推断：经营表现驱动指标\n\n")
	fmt.Fprintf(&out, "- 收入同比 %+.2f%%、归母净利润同比 %+.2f%%；利润增速相对收入增速的差为 %+.2f 个百分点。这里只描述增速关系，不把差异擅自归因于价格、销量、渠道或成本。\n", *revenue.YoyPct, *profit.YoyPct, *profit.YoyPct-*revenue.YoyPct)
	fmt.Fprintf(&out, "- 经营现金流同比 %+.2f%%，本期经营现金流/归母净利润为 %s；单季现金流波动较大，需结合后续报告确认持续性。\n", *cashFlow.YoyPct, formatStockRatio(*cashFlow.Current, *profit.Current))
	fmt.Fprintf(&out, "- 二级市场价格相对 20 个交易日前变动 %+.2f%%；PE TTM 的腾讯/东方财富口径分别为 %s / %s（%s）。这些是市场定价观察，不是经营因果。\n",
		*history.Return20DPct, formatOptionalStockNumber(tencent.Valuation.PETTM), formatOptionalStockNumber(eastmoney.Valuation.PETTM),
		stockOptionalNumberAgreement(tencent.Valuation.PETTM, eastmoney.Valuation.PETTM))

	out.WriteString("\n## 风险观察清单\n\n")
	fmt.Fprintf(&out, "1. **价格波动风险（事实依据）**：60个交易日未复权价格变动 %+.2f%%，区间 %.2f–%.2f 元；历史区间不预测未来方向。\n", *history.Return60DPct, *history.Range60Low, *history.Range60High)
	fmt.Fprintf(&out, "2. **盈利兑现风险（事实依据）**：收入同比 %+.2f%%，归母净利润同比 %+.2f%%；后续若利润持续慢于收入，盈利质量与费用/税率结构需进一步核对。\n", *revenue.YoyPct, *profit.YoyPct)
	fmt.Fprintf(&out, "3. **现金流正常化风险（事实依据）**：经营现金流同比 %+.2f%%。较大同比变化可能受基数与收付节奏影响，当前结构化证据不足以断言其可持续。\n", *cashFlow.YoyPct)
	fmt.Fprintf(&out, "4. **估值口径与估值压缩风险（事实依据）**：PE TTM 两家为 %s 与 %s，PB 两家为 %s 与 %s；供应商值如有差异必须并列。不能平均，也不能据此给目标价。\n", formatOptionalStockNumber(tencent.Valuation.PETTM), formatOptionalStockNumber(eastmoney.Valuation.PETTM), formatOptionalStockNumber(tencent.Valuation.PB), formatOptionalStockNumber(eastmoney.Valuation.PB))
	fmt.Fprintf(&out, "5. **财报时点风险（事实依据）**：最新报告期为 %s，且为%s；报告日之后的经营变化尚未进入这组财务数字。\n", report.Period, stockAuditLabel(report.Unaudited))
	out.WriteString("6. **行业竞争与政策监管风险（通用框架）**：当前结构化证据未证明发生了具体负面事件；这里只提示需求、竞争和监管变化可能影响上市公司，需用后续公告与经营数据监测。\n")

	out.WriteString("\n## 未知项与边界\n\n")
	out.WriteString("- 现有结构化证据不能解释收入/利润变化的具体产品、价格、销量、渠道或区域原因；需要报告分部表或管理层说明才能归因。\n")
	out.WriteString("- 未核验未来盈利预测、行业库存/批价、机构目标价及公告标题之外的正文含义。\n")
	out.WriteString("- 本分析不给确定性涨跌预测、目标价或买卖指令。\n")
	return strings.TrimSpace(out.String())
}

func stockAgreementText(ok bool) string {
	if ok {
		return "一致"
	}
	return "冲突（不裁决）"
}

func stockNumberAgreement(a, b, tolerance float64) string {
	return stockAgreementText(math.Abs(a-b) <= tolerance)
}

func stockOHLCVerification(first, second *stockFallbackQuote) string {
	if first == nil || second == nil {
		return "来源不足"
	}
	for _, pair := range [][2]*float64{{first.Open, second.Open}, {first.High, second.High}, {first.Low, second.Low}} {
		if pair[0] == nil || pair[1] == nil || math.Abs(*pair[0]-*pair[1]) > 0.011 {
			return "冲突（不裁决）"
		}
	}
	return "一致"
}

func writeStockValuationRow(out *strings.Builder, label string, first, second *float64) {
	firstText, secondText := formatOptionalStockNumber(first), formatOptionalStockNumber(second)
	status := "至少一方未提供"
	if first != nil && second != nil {
		status = stockNumberAgreement(*first, *second, 0.011)
	}
	fmt.Fprintf(out, "| %s | %s | %s | %s |\n", label, firstText, secondText, status)
}

func stockOptionalNumberAgreement(first, second *float64) string {
	if first == nil || second == nil {
		return "至少一方未提供"
	}
	return stockNumberAgreement(*first, *second, 0.011)
}

func formatOptionalStockNumber(value *float64) string {
	if value == nil {
		return "未提供"
	}
	return strconv.FormatFloat(*value, 'f', 2, 64)
}

func stockAuditLabel(unaudited *bool) string {
	if unaudited == nil {
		return "未说明"
	}
	if *unaudited {
		return "未经审计"
	}
	return "报告未标记为未经审计"
}

func stockMetricLabel(key string) string {
	return map[string]string{
		"revenue":             "营业收入",
		"parent_net_profit":   "归母净利润",
		"operating_cash_flow": "经营活动现金流净额",
		"eps":                 "基本每股收益",
		"roe":                 "加权ROE",
	}[key]
}

func formatStockMetric(value float64, unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "cny", "yuan", "元":
		return fmt.Sprintf("%.2f亿元", value/1e8)
	case "cny/share", "yuan/share", "元/股":
		return fmt.Sprintf("%.2f元/股", value)
	case "%", "percent", "percentage_point":
		return fmt.Sprintf("%.2f%%", value)
	default:
		return fmt.Sprintf("%.2f %s", value, unit)
	}
}

func formatStockRatio(numerator, denominator float64) string {
	if math.Abs(denominator) < 0.000001 {
		return "不可计算（分母为零）"
	}
	return fmt.Sprintf("约%.2f倍", numerator/denominator)
}
