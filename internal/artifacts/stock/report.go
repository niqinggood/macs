package stock

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	securitycore "mac/internal/security"
	"mac/internal/toolcontract"
	networktools "mac/internal/tools/network"
)

var externalURLPattern = securitycore.ExternalURLPattern()

var stockStressPctPattern = regexp.MustCompile(`(?:下滑|下降|减少|降低|跌)s*([0-9]{1,3}(?:\.[0-9]+)?)\s*%`)

func StructuredFactsFromResults(results []string) map[string]map[string]networktools.StockFactsResult {
	grouped := make(map[string]map[string]networktools.StockFactsResult)
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || !strings.EqualFold(record.Tool, "stock_facts") {
			continue
		}
		for _, line := range strings.Split(result, "\n") {
			idx := strings.Index(line, networktools.StockFactsResultMarker)
			if idx < 0 {
				continue
			}
			raw := strings.TrimSpace(line[idx+len(networktools.StockFactsResultMarker):])
			var facts networktools.StockFactsResult
			if json.Unmarshal([]byte(raw), &facts) != nil || facts.Symbol == "" || facts.Provider == "" {
				continue
			}
			if grouped[facts.Symbol] == nil {
				grouped[facts.Symbol] = make(map[string]networktools.StockFactsResult)
			}
			grouped[facts.Symbol][facts.Provider] = facts
			break
		}
	}
	return grouped
}

func BuildComparisonArtifact(input, targetPath string, results []string, requested bool) (string, bool) {
	if !requested || !strings.HasSuffix(strings.ToLower(strings.TrimSpace(targetPath)), ".md") {
		return "", false
	}
	grouped := StructuredFactsFromResults(results)
	if len(grouped) != 2 {
		return "", false
	}
	symbols := make([]string, 0, 2)
	for symbol, providers := range grouped {
		if providers[networktools.StockFactsProviderTencentMarket].Quote == nil ||
			providers[networktools.StockFactsProviderTencentMarket].History == nil ||
			providers[networktools.StockFactsProviderEastmoney].Quote == nil ||
			providers[networktools.StockFactsProviderOfficialReport].Report == nil {
			return "", false
		}
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	left, right := grouped[symbols[0]], grouped[symbols[1]]
	leftTencent, rightTencent := left[networktools.StockFactsProviderTencentMarket], right[networktools.StockFactsProviderTencentMarket]
	leftEast, rightEast := left[networktools.StockFactsProviderEastmoney], right[networktools.StockFactsProviderEastmoney]
	leftOfficial, rightOfficial := left[networktools.StockFactsProviderOfficialReport], right[networktools.StockFactsProviderOfficialReport]

	stress := "用户给定的压力情景"
	if match := stockStressPctPattern.FindStringSubmatch(input); len(match) == 2 {
		stress = "销量下降 " + match[1] + "%"
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# %s（%s）与 %s（%s）事实型比较\n\n", leftTencent.CompanyName, leftTencent.Symbol, rightTencent.CompanyName, rightTencent.Symbol)
	fmt.Fprintf(&out, "> 行情截至：%s；财报期：%s / %s。%s是条件假设，不代表已经发生，也不构成买卖建议。\n\n",
		leftTencent.Quote.TradeDate, leftOfficial.Report.Period, rightOfficial.Report.Period, stress)

	out.WriteString("## 1. 行情与近期走势\n\n")
	out.WriteString("| 指标 | " + leftTencent.CompanyName + " | " + rightTencent.CompanyName + " |\n|---|---:|---:|\n")
	writeStockCompareRow(&out, "收盘价（CNY）", OptionalRawNumber(leftTencent.Quote.Close), OptionalRawNumber(rightTencent.Quote.Close))
	writeStockCompareRow(&out, "5日未复权价格变化", FormatPercent(leftTencent.History.Return5DPct), FormatPercent(rightTencent.History.Return5DPct))
	writeStockCompareRow(&out, "20日未复权价格变化", FormatPercent(leftTencent.History.Return20DPct), FormatPercent(rightTencent.History.Return20DPct))
	writeStockCompareRow(&out, "60日未复权价格变化", FormatPercent(leftTencent.History.Return60DPct), FormatPercent(rightTencent.History.Return60DPct))
	writeStockCompareRow(&out, "全样本期未复权价格变化", FormatPercent(leftTencent.History.ReturnPeriodPct), FormatPercent(rightTencent.History.ReturnPeriodPct))
	writeStockCompareRow(&out, "MA5 / MA20 / MA60", formatThree(leftTencent.History.MA5, leftTencent.History.MA20, leftTencent.History.MA60), formatThree(rightTencent.History.MA5, rightTencent.History.MA20, rightTencent.History.MA60))
	out.WriteString("\n以上为未复权价格变化，不含现金分红。腾讯与东方财富的收盘价用于交叉核对；它们不自动构成独立交易所数据来源。\n\n")

	out.WriteString("## 2. 财务数据（保持工具返回的原始 CNY）\n\n")
	out.WriteString("| 指标 | " + leftTencent.CompanyName + " | " + rightTencent.CompanyName + " |\n|---|---:|---:|\n")
	writeStockCompareMetricRow(&out, "营业收入", leftOfficial.Report.Metrics["revenue"], rightOfficial.Report.Metrics["revenue"])
	writeStockCompareMetricRow(&out, "归母净利润", leftOfficial.Report.Metrics["parent_net_profit"], rightOfficial.Report.Metrics["parent_net_profit"])
	writeStockCompareMetricRow(&out, "经营活动现金流净额", leftOfficial.Report.Metrics["operating_cash_flow"], rightOfficial.Report.Metrics["operating_cash_flow"])
	writeStockCompareMetricRow(&out, "基本每股收益", leftOfficial.Report.Metrics["eps"], rightOfficial.Report.Metrics["eps"])
	writeStockCompareMetricRow(&out, "ROE", leftOfficial.Report.Metrics["roe"], rightOfficial.Report.Metrics["roe"])
	out.WriteString("\n`not_found_after_pdf_extraction` 表示官方报告和链接已取得，但该字段没有被结构化解析；报告保留为未知，不用网页数字或上一轮内容补齐。\n\n")

	out.WriteString("## 3. 估值口径\n\n")
	out.WriteString("| 指标 | " + leftTencent.CompanyName + " | " + rightTencent.CompanyName + " |\n|---|---:|---:|\n")
	writeStockCompareRow(&out, "腾讯 PE TTM", OptionalRawNumber(leftTencent.Valuation.PETTM), OptionalRawNumber(rightTencent.Valuation.PETTM))
	writeStockCompareRow(&out, "东方财富 PE TTM", OptionalRawNumber(leftEast.Valuation.PETTM), OptionalRawNumber(rightEast.Valuation.PETTM))
	writeStockCompareRow(&out, "腾讯 PB", OptionalRawNumber(leftTencent.Valuation.PB), OptionalRawNumber(rightTencent.Valuation.PB))
	writeStockCompareRow(&out, "东方财富 PB", OptionalRawNumber(leftEast.Valuation.PB), OptionalRawNumber(rightEast.Valuation.PB))
	writeStockCompareRow(&out, "腾讯总市值标签（CNY）", OptionalRawNumber(leftTencent.Valuation.MarketCap), OptionalRawNumber(rightTencent.Valuation.MarketCap))
	writeStockCompareRow(&out, "东方财富总市值标签（CNY）", OptionalRawNumber(leftEast.Valuation.MarketCap), OptionalRawNumber(rightEast.Valuation.MarketCap))
	out.WriteString("\nPE 与 PB 必须逐项比较：一项更低不代表所有估值指标都更低。两家行情商的市值标签如有冲突，原样并列，不平均、不猜测冲突原因。\n\n")

	out.WriteString("## 4. 行业位置、经营驱动与证据边界\n\n")
	out.WriteString("- 结构化股票证据可确认两家公司身份、行情、最近报告及公告标题，但不能单独证明市场份额、全球排名或各业务收入占比。\n")
	out.WriteString("- 本轮定性检索只作为候选来源发现；未取得并冻结到结构化证据中的排名、份额和业务占比均列为未知，不从常识补写。\n")
	out.WriteString("- 可验证的后续路径：读取下方官方年报全文中的分部信息、主营业务说明与管理层讨论，再补充行业协会的同口径数据。\n\n")

	out.WriteString("## 5. 压力情景\n\n")
	fmt.Fprintf(&out, "%s仅作为敏感性假设。由于当前证据没有两家公司新能源车、动力电池、储能等分部收入占比，也没有销量到收入/利润的弹性，不能把 15%% 直接乘总营收，更不能给出确定的利润或股价变化。\n\n", stress)
	out.WriteString("可复核的计算框架为：`收入冲击 = 相关分部收入 × 销量变化 × 收入弹性`；`利润冲击 = 收入冲击 × 增量利润率 + 固定成本影响`。当前缺失项包括相关分部收入、收入弹性、增量利润率和固定成本，因此本报告只给框架，不填假数字。\n\n")

	out.WriteString("## 6. 直接来源\n\n")
	for _, source := range []networktools.StockFactsResult{leftTencent, leftEast, leftOfficial, rightTencent, rightEast, rightOfficial} {
		fmt.Fprintf(&out, "- %s %s｜retrieval_time=%s｜as_of=%s｜%s\n", source.CompanyName, source.Provider, source.RetrievalTime, source.AsOf, source.SourceURL)
	}
	qualitativeURLs := qualitativeEvidenceURLs(results)
	if len(qualitativeURLs) > 0 {
		out.WriteString("\n定性检索返回的候选链接（仅供继续核验，不把搜索摘要当作已证实排名）：\n")
		for _, rawURL := range qualitativeURLs {
			fmt.Fprintf(&out, "- %s\n", rawURL)
		}
	}
	return strings.TrimSpace(out.String()) + "\n", true
}

func BuildToolCall(input, targetPath string, results []string, iteration int, requested bool) (toolcontract.Call, bool) {
	content, ok := BuildComparisonArtifact(input, targetPath, results, requested)
	if !ok {
		return toolcontract.Call{}, false
	}
	return toolcontract.Call{
		ID:   fmt.Sprintf("host_stock_compare_%d", iteration),
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    targetPath,
			"content": content,
			"append":  false,
		},
	}, true
}

func writeStockCompareRow(out *strings.Builder, label, left, right string) {
	fmt.Fprintf(out, "| %s | %s | %s |\n", label, left, right)
}

func writeStockCompareMetricRow(out *strings.Builder, label string, left, right networktools.StockReportMetric) {
	writeStockCompareRow(out, label, RawReportMetric(left), RawReportMetric(right))
}

func RawReportMetric(metric networktools.StockReportMetric) string {
	if metric.Status != "observed" || metric.Current == nil {
		if metric.Status == "" {
			return "未知"
		}
		return "未知（" + metric.Status + "）"
	}
	current := OptionalRawNumber(metric.Current) + " " + metric.Unit
	if metric.YoYPct != nil {
		current += "；同比 " + FormatPercent(*metric.YoYPct)
	}
	return current
}

func OptionalRawNumber(value *float64) string {
	if value == nil {
		return "未知"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func FormatPercent(value float64) string { return strconv.FormatFloat(value, 'f', 2, 64) + "%" }

func formatThree(first, second, third float64) string {
	return fmt.Sprintf("%.2f / %.2f / %.2f", first, second, third)
}

func qualitativeEvidenceURLs(results []string) []string {
	seen := make(map[string]bool)
	var urls []string
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || (record.Tool != "web_fetch" && record.Tool != "web_search") {
			continue
		}
		for _, rawURL := range externalURLPattern.FindAllString(result, -1) {
			rawURL = strings.TrimRight(rawURL, ".,，。;；:`\")")
			if seen[rawURL] {
				continue
			}
			seen[rawURL] = true
			urls = append(urls, rawURL)
			if len(urls) >= 8 {
				return urls
			}
		}
	}
	return urls
}
