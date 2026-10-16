package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	fuelPriceProviderRegionalOfficial = "regional_official"
	fuelPriceProviderNXVAV            = "nxvav"
	fuelPriceProviderTuanyou          = "tuanyou"

	fuelPriceResultMarker = "OPENKITTY_FUEL_PRICE_RESULT="
	fuelPriceMaxBodyBytes = int64(3 * 1024 * 1024)
)

var fuelPriceShanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// FuelPriceTool fetches current automotive fuel prices from one explicitly
// selected publisher. Current prices and forward-looking adjustment estimates
// are represented separately so a model cannot silently treat a forecast as a
// price already in effect.
type FuelPriceTool struct {
	client                  *http.Client
	officialIndexEndpoint   string
	nxvavEndpoint           string
	tuanyouShanghaiEndpoint string
	requestTimeout          time.Duration
	now                     func() time.Time
}

func NewFuelPriceTool() *FuelPriceTool {
	return &FuelPriceTool{
		client:                  &http.Client{Timeout: 10 * time.Second},
		officialIndexEndpoint:   "https://fgw.sh.gov.cn/fgw_jggl/index.html",
		nxvavEndpoint:           "https://api.nxvav.cn/api/fuel-price/",
		tuanyouShanghaiEndpoint: "https://www.tuanyou.net/youjia/shanghai/",
		requestTimeout:          10 * time.Second,
		now:                     time.Now,
	}
}

func (t *FuelPriceTool) Name() string { return "fuel_price" }

func (t *FuelPriceTool) Description() string {
	return "Fetch structured current automotive fuel prices from one read-only publisher. " +
		"provider=regional_official discovers and parses the latest Shanghai Development and Reform Commission fuel-price notice; " +
		"provider=nxvav queries its regional JSON endpoint; provider=tuanyou parses its Shanghai current-price snapshot and separately labelled next-adjustment forecast. " +
		"For a Shanghai request that includes 98-octane or asks to cross-check every listed grade, call regional_official, nxvav, and tuanyou exactly once each in the same parallel batch: the official notice intentionally has no 98 value, so both market publishers are required to corroborate 98. " +
		"For a request limited to official grades, regional_official plus one secondary provider is sufficient. Cite each exact Source URL. " +
		"Never report next_adjustment_date, predicted_direction, or predicted_change as a current price."
}

func (t *FuelPriceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"region": map[string]interface{}{
				"type":        "string",
				"description": "Province-level region name. Shanghai accepts 上海, 上海市, Shanghai, or Shanghai, China.",
			},
			"provider": map[string]interface{}{
				"type": "string",
				"enum": []string{
					fuelPriceProviderRegionalOfficial,
					fuelPriceProviderNXVAV,
					fuelPriceProviderTuanyou,
				},
				"description": "Publisher to query. regional_official and tuanyou currently support Shanghai; nxvav accepts its supported Chinese regions.",
			},
		},
		"required": []string{"region", "provider"},
	}
}

func (t *FuelPriceTool) MaxResultSize() int { return 16 * 1024 }
func (t *FuelPriceTool) IsReadOnly() bool   { return true }

type fuelPriceToolResult struct {
	Version                    int                 `json:"version"`
	Provider                   string              `json:"provider"`
	Publisher                  string              `json:"publisher"`
	Region                     string              `json:"region"`
	SourceURL                  string              `json:"source_url"`
	RetrievalTime              string              `json:"retrieval_time"`
	PublishedTime              string              `json:"published_time"`
	EffectiveTime              string              `json:"effective_time"`
	UpdateTime                 string              `json:"update_time"`
	Prices                     map[string]*float64 `json:"prices"`
	PriceStatus                map[string]string   `json:"price_status"`
	NextAdjustmentDate         string              `json:"next_adjustment_date"`
	CrudeChangeRate            *float64            `json:"crude_change_rate"`
	PredictedDirection         string              `json:"predicted_direction"`
	PredictedChangeCNYPerL     *float64            `json:"predicted_change_cny_per_l"`
	ForecastCalculationDay     string              `json:"forecast_calculation_day"`
	RequiredCurrentGradeCount  int                 `json:"-"`
	AvailableCurrentGradeCount int                 `json:"-"`
}

func (t *FuelPriceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.client == nil {
		return "", errors.New("fuel_price is not configured")
	}
	if t.requestTimeout <= 0 || t.requestTimeout > 30*time.Second {
		return "", fmt.Errorf("fuel_price HTTP timeout must be within (0, 30s], got %s", t.requestTimeout)
	}
	rawRegion, _ := params["region"].(string)
	rawRegion = strings.TrimSpace(rawRegion)
	if rawRegion == "" {
		return "", errors.New("region is required")
	}
	provider, _ := params["provider"].(string)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != fuelPriceProviderRegionalOfficial && provider != fuelPriceProviderNXVAV && provider != fuelPriceProviderTuanyou {
		return "", fmt.Errorf("unsupported fuel-price provider %q", provider)
	}

	var result fuelPriceToolResult
	var err error
	switch provider {
	case fuelPriceProviderRegionalOfficial:
		result, err = t.fetchShanghaiOfficial(ctx, rawRegion)
	case fuelPriceProviderNXVAV:
		result, err = t.fetchNXVAV(ctx, rawRegion)
	case fuelPriceProviderTuanyou:
		result, err = t.fetchTuanyouShanghai(ctx, rawRegion)
	}
	if err != nil {
		return "", fmt.Errorf("%s fuel price failed: %w", provider, err)
	}
	if err := validateFuelPriceResult(result); err != nil {
		return "", fmt.Errorf("%s fuel price failed validation: %w", provider, err)
	}
	return renderFuelPriceResult(result)
}

func (t *FuelPriceTool) fetchShanghaiOfficial(ctx context.Context, requestedRegion string) (fuelPriceToolResult, error) {
	region, ok := normalizeShanghaiFuelRegion(requestedRegion)
	if !ok {
		return fuelPriceToolResult{}, fmt.Errorf("regional_official currently supports Shanghai only; region %q is not supported", requestedRegion)
	}
	indexBody, indexURL, err := t.get(ctx, t.officialIndexEndpoint, "text/html,application/xhtml+xml")
	if err != nil {
		return fuelPriceToolResult{}, fmt.Errorf("fetch official index: %w", err)
	}
	detailURL, err := discoverLatestShanghaiFuelNotice(indexBody, indexURL)
	if err != nil {
		return fuelPriceToolResult{}, err
	}
	detailBody, sourceURL, err := t.get(ctx, detailURL, "text/html,application/xhtml+xml")
	if err != nil {
		return fuelPriceToolResult{}, fmt.Errorf("fetch latest official notice: %w", err)
	}
	prices, publishedTime, effectiveTime, err := parseShanghaiOfficialFuelNotice(detailBody)
	if err != nil {
		return fuelPriceToolResult{}, err
	}
	prices["98"] = nil
	return fuelPriceToolResult{
		Version:       1,
		Provider:      fuelPriceProviderRegionalOfficial,
		Publisher:     "上海市发展和改革委员会",
		Region:        region,
		SourceURL:     sourceURL,
		RetrievalTime: t.currentTime().UTC().Format(time.RFC3339),
		PublishedTime: publishedTime,
		EffectiveTime: effectiveTime,
		UpdateTime:    "",
		Prices:        prices,
		PriceStatus: map[string]string{
			"92":       "official_maximum_retail_price",
			"95":       "official_maximum_retail_price",
			"98":       "not_published_by_official_enterprise_priced",
			"0_diesel": "official_maximum_retail_price",
		},
		NextAdjustmentDate:         "",
		PredictedDirection:         "unknown",
		ForecastCalculationDay:     "",
		RequiredCurrentGradeCount:  3,
		AvailableCurrentGradeCount: 3,
	}, nil
}

type fuelPriceNoticeCandidate struct {
	URL       string
	Published time.Time
}

func discoverLatestShanghaiFuelNotice(body []byte, indexURL string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("parse official index HTML: %w", err)
	}
	base, err := url.Parse(indexURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid official index URL %q", indexURL)
	}
	candidates := make([]fuelPriceNoticeCandidate, 0, 4)
	doc.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
		title := compactFuelPriceText(selection.AttrOr("title", "") + " " + selection.Text())
		if !isShanghaiFuelNoticeTitle(title) {
			return
		}
		href := strings.TrimSpace(selection.AttrOr("href", ""))
		if href == "" {
			return
		}
		publishedRaw := extractFuelPriceDate(selection.Closest("li").Text() + " " + title + " " + href)
		published, ok := parseFuelPriceDate(publishedRaw)
		if !ok {
			return
		}
		resolved, resolveErr := base.Parse(href)
		if resolveErr != nil || (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
			return
		}
		// The official index historically emits absolute http links even when the
		// same document is served canonically over HTTPS. Keep the index scheme for
		// same-host links so the fetched and reported source is the safe endpoint.
		if base.Scheme == "https" && resolved.Scheme == "http" && strings.EqualFold(base.Hostname(), resolved.Hostname()) {
			resolved.Scheme = "https"
		}
		resolved.Fragment = ""
		candidates = append(candidates, fuelPriceNoticeCandidate{URL: resolved.String(), Published: published})
	})
	if len(candidates) == 0 {
		return "", errors.New("official index contained no dated vehicle gasoline/diesel price notice")
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Published.After(candidates[j].Published) })
	return candidates[0].URL, nil
}

func isShanghaiFuelNoticeTitle(value string) bool {
	value = strings.ReplaceAll(compactFuelPriceText(value), "，", ",")
	return strings.Contains(value, "车用汽,柴油价格的通知") ||
		(strings.Contains(value, "车用汽") && strings.Contains(value, "柴油价格") && strings.Contains(value, "通知"))
}

func parseShanghaiOfficialFuelNotice(body []byte) (map[string]*float64, string, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, "", "", fmt.Errorf("parse official notice HTML: %w", err)
	}
	title := compactFuelPriceText(doc.Find("meta[name='ArticleTitle']").First().AttrOr("content", "") + " " + doc.Find("h1,h2.Article-title").First().Text())
	if !isShanghaiFuelNoticeTitle(title) || (!strings.Contains(title, "上海市发展和改革委员会") && !strings.Contains(compactFuelPriceText(doc.Text()), "上海市发展和改革委员会")) {
		return nil, "", "", errors.New("official detail page is not a Shanghai vehicle gasoline/diesel price notice")
	}

	publishedRaw := strings.TrimSpace(doc.Find("meta[name='PubDate']").First().AttrOr("content", ""))
	if publishedRaw == "" {
		publishedRaw = extractFuelPriceDate(doc.Find(".Article-time").First().Text())
	}
	publishedTime, ok := normalizeFuelPriceLocalTimestamp(publishedRaw)
	if !ok {
		return nil, "", "", errors.New("official notice publication time is missing or invalid")
	}

	contentText := compactFuelPriceText(doc.Find("#ivs_content,.Article_content").First().Text())
	if contentText == "" {
		contentText = compactFuelPriceText(doc.Text())
	}
	effectiveTime, ok := parseShanghaiFuelEffectiveTime(contentText)
	if !ok {
		return nil, "", "", errors.New("official notice effective time is missing or invalid")
	}

	prices := make(map[string]*float64)
	currentGrade := ""
	doc.Find("#ivs_content table tr,.Article_content table tr").Each(func(_ int, row *goquery.Selection) {
		cells := make([]string, 0, row.Find("th,td").Length())
		row.Find("th,td").Each(func(_ int, cell *goquery.Selection) {
			cells = append(cells, compactFuelPriceText(cell.Text()))
		})
		for _, cell := range cells {
			if grade := fuelPriceGradeKey(cell); grade != "" {
				currentGrade = grade
				break
			}
		}
		if currentGrade == "" || (currentGrade != "92" && currentGrade != "95" && currentGrade != "0_diesel") {
			return
		}
		for i, cell := range cells {
			if !strings.Contains(strings.ReplaceAll(cell, " ", ""), "元/升") {
				continue
			}
			for j := i + 1; j < len(cells); j++ {
				if value, valueOK := parseFuelPriceNumber(cells[j]); valueOK {
					setFuelPriceIfConsistent(prices, currentGrade, value)
					return
				}
			}
			if match := regexp.MustCompile(`元\s*/\s*升\s*([0-9]+(?:\.[0-9]+)?)`).FindStringSubmatch(strings.Join(cells, " ")); len(match) == 2 {
				if value, parseErr := strconv.ParseFloat(match[1], 64); parseErr == nil {
					setFuelPriceIfConsistent(prices, currentGrade, value)
				}
			}
		}
	})
	for _, grade := range []string{"92", "95", "0_diesel"} {
		if prices[grade] == nil {
			return nil, "", "", fmt.Errorf("official notice is incomplete: missing %s price in CNY/L", grade)
		}
	}
	return prices, publishedTime, effectiveTime, nil
}

func parseShanghaiFuelEffectiveTime(text string) (string, bool) {
	pattern := regexp.MustCompile(`自\s*([0-9]{4})\s*年\s*([0-9]{1,2})\s*月\s*([0-9]{1,2})\s*日\s*([0-9]{1,2})\s*时(?:\s*([0-9]{1,2})\s*分)?\s*起执行`)
	match := pattern.FindStringSubmatch(text)
	if len(match) != 6 {
		return "", false
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	hour, _ := strconv.Atoi(match[4])
	minute := 0
	if match[5] != "" {
		minute, _ = strconv.Atoi(match[5])
	}
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 || (hour == 24 && minute != 0) {
		return "", false
	}
	base := time.Date(year, time.Month(month), day, 0, 0, 0, 0, fuelPriceShanghaiLocation)
	if base.Year() != year || int(base.Month()) != month || base.Day() != day {
		return "", false
	}
	return base.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute).Format(time.RFC3339), true
}

func (t *FuelPriceTool) fetchNXVAV(ctx context.Context, requestedRegion string) (fuelPriceToolResult, error) {
	region := canonicalFuelPriceRegion(requestedRegion)
	if region == "" {
		return fuelPriceToolResult{}, fmt.Errorf("invalid region %q", requestedRegion)
	}
	endpoint, err := url.Parse(t.nxvavEndpoint)
	if err != nil {
		return fuelPriceToolResult{}, fmt.Errorf("invalid nxvav endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("region", region)
	endpoint.RawQuery = query.Encode()
	body, sourceURL, err := t.get(ctx, endpoint.String(), "application/json")
	if err != nil {
		return fuelPriceToolResult{}, err
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Region    string `json:"region"`
			Updated   string `json:"updated"`
			UpdatedAt int64  `json:"updated_at"`
			Items     []struct {
				Name  string          `json:"name"`
				Price json.RawMessage `json:"price"`
			} `json:"items"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fuelPriceToolResult{}, fmt.Errorf("decode nxvav JSON: %w", err)
	}
	if payload.Code != http.StatusOK {
		return fuelPriceToolResult{}, fmt.Errorf("nxvav returned code %d: %s", payload.Code, strings.TrimSpace(payload.Message))
	}
	returnedRegion := canonicalFuelPriceRegion(payload.Data.Region)
	if returnedRegion == "" || !fuelPriceRegionsEqual(region, returnedRegion) {
		return fuelPriceToolResult{}, fmt.Errorf("nxvav region mismatch: requested %q, returned %q", region, payload.Data.Region)
	}
	prices := make(map[string]*float64)
	for _, item := range payload.Data.Items {
		grade := fuelPriceGradeKey(item.Name)
		if grade == "" {
			continue
		}
		value, valueOK := parseFuelPriceJSONNumber(item.Price)
		if !valueOK {
			return fuelPriceToolResult{}, fmt.Errorf("nxvav returned invalid price for %s", item.Name)
		}
		if existing := prices[grade]; existing != nil && math.Abs(*existing-value) > 0.0001 {
			return fuelPriceToolResult{}, fmt.Errorf("nxvav returned conflicting duplicate prices for %s", grade)
		}
		prices[grade] = fuelPriceFloatPointer(value)
	}
	for _, grade := range fuelPriceGradeOrder() {
		if prices[grade] == nil {
			return fuelPriceToolResult{}, fmt.Errorf("nxvav response is incomplete: missing %s", grade)
		}
	}
	updateTime, ok := normalizeFuelPriceLocalTimestamp(payload.Data.Updated)
	if !ok && payload.Data.UpdatedAt > 0 {
		updateTime = time.UnixMilli(payload.Data.UpdatedAt).In(fuelPriceShanghaiLocation).Format(time.RFC3339)
		ok = true
	}
	if !ok {
		return fuelPriceToolResult{}, errors.New("nxvav update time is missing or invalid")
	}
	status := make(map[string]string, 4)
	for _, grade := range fuelPriceGradeOrder() {
		status[grade] = "current_price_reported_by_provider"
	}
	return fuelPriceToolResult{
		Version:                    1,
		Provider:                   fuelPriceProviderNXVAV,
		Publisher:                  "NXVAV fuel-price API",
		Region:                     returnedRegion,
		SourceURL:                  sourceURL,
		RetrievalTime:              t.currentTime().UTC().Format(time.RFC3339),
		UpdateTime:                 updateTime,
		Prices:                     prices,
		PriceStatus:                status,
		PredictedDirection:         "unknown",
		RequiredCurrentGradeCount:  4,
		AvailableCurrentGradeCount: 4,
	}, nil
}

func (t *FuelPriceTool) fetchTuanyouShanghai(ctx context.Context, requestedRegion string) (fuelPriceToolResult, error) {
	region, ok := normalizeShanghaiFuelRegion(requestedRegion)
	if !ok {
		return fuelPriceToolResult{}, fmt.Errorf("tuanyou currently supports Shanghai only; region %q is not supported", requestedRegion)
	}
	body, sourceURL, err := t.get(ctx, t.tuanyouShanghaiEndpoint, "text/html,application/xhtml+xml")
	if err != nil {
		return fuelPriceToolResult{}, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return fuelPriceToolResult{}, fmt.Errorf("parse tuanyou HTML: %w", err)
	}
	pageRegion := strings.TrimSpace(doc.Find("meta[name='location']").First().AttrOr("content", ""))
	pageRegion = strings.TrimSpace(strings.TrimPrefix(pageRegion, "province="))
	if pageRegion == "" {
		pageRegion = strings.TrimSuffix(compactFuelPriceText(doc.Find("h1").First().Text()), "今日油价")
	}
	normalizedPageRegion := canonicalFuelPriceRegion(pageRegion)
	if normalizedPageRegion == "" || !fuelPriceRegionsEqual(region, normalizedPageRegion) {
		return fuelPriceToolResult{}, fmt.Errorf("tuanyou region mismatch: requested %q, page identifies %q", region, pageRegion)
	}

	prices := make(map[string]*float64)
	doc.Find(".catediqu ul").Each(func(_ int, row *goquery.Selection) {
		cells := make([]string, 0, 3)
		row.Find("li,a").Each(func(_ int, cell *goquery.Selection) {
			value := compactFuelPriceText(cell.Text())
			if value != "" && (len(cells) == 0 || cells[len(cells)-1] != value) {
				cells = append(cells, value)
			}
		})
		if len(cells) < 2 {
			return
		}
		grade := fuelPriceGradeKey(cells[0])
		if grade == "" {
			return
		}
		if value, valueOK := parseFuelPriceNumber(cells[1]); valueOK {
			setFuelPriceIfConsistent(prices, grade, value)
		}
	})
	if len(prices) < 4 {
		description := doc.Find("meta[name='description']").First().AttrOr("content", "")
		for _, grade := range fuelPriceGradeOrder() {
			if prices[grade] != nil {
				continue
			}
			label := map[string]string{"92": "92号汽油", "95": "95号汽油", "98": "98号汽油", "0_diesel": "0号柴油"}[grade]
			pattern := regexp.MustCompile(regexp.QuoteMeta(label) + `\s*[：:]?\s*([0-9]+(?:\.[0-9]+)?)\s*元\s*/\s*升`)
			if match := pattern.FindStringSubmatch(description); len(match) == 2 {
				if value, parseErr := strconv.ParseFloat(match[1], 64); parseErr == nil {
					prices[grade] = fuelPriceFloatPointer(value)
				}
			}
		}
	}
	for _, grade := range fuelPriceGradeOrder() {
		if prices[grade] == nil {
			return fuelPriceToolResult{}, fmt.Errorf("tuanyou page is incomplete: missing %s", grade)
		}
	}

	pageText := compactFuelPriceText(doc.Text())
	updateMatch := regexp.MustCompile(`以上油价更新于\s*([0-9]{4}[-年][0-9]{1,2}[-月][0-9]{1,2}(?:日)?)`).FindStringSubmatch(pageText)
	if len(updateMatch) != 2 {
		return fuelPriceToolResult{}, errors.New("tuanyou current-price update date is missing")
	}
	updateTime, updateOK := normalizeFuelPriceLocalTimestamp(updateMatch[1])
	if !updateOK {
		return fuelPriceToolResult{}, fmt.Errorf("tuanyou current-price update date %q is invalid", updateMatch[1])
	}

	nextAdjustment := ""
	if match := regexp.MustCompile(`下次调价[：:]?\s*([0-9]{4}[-年][0-9]{1,2}[-月][0-9]{1,2}(?:日)?)`).FindStringSubmatch(pageText); len(match) == 2 {
		if normalized, dateOK := normalizeFuelPriceLocalTimestamp(match[1]); dateOK {
			nextAdjustment = normalized
		}
	}
	var crudeRate *float64
	predictedDirection := "unknown"
	var predictedChange *float64
	calculationDay := ""
	doc.Find("table tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		cells := make([]string, 0, row.Find("th,td").Length())
		row.Find("th,td").Each(func(_ int, cell *goquery.Selection) {
			cells = append(cells, compactFuelPriceText(cell.Text()))
		})
		if len(cells) < 2 || !strings.Contains(cells[0], "%") {
			return true
		}
		if rate, rateOK := parseFuelPricePercent(cells[0]); rateOK {
			crudeRate = fuelPriceFloatPointer(rate)
		}
		predictionText := cells[1]
		predictedDirection = fuelPricePredictionDirection(predictionText)
		if predictedDirection != "unknown" {
			if change, changeOK := parseFuelPriceNumber(predictionText); changeOK {
				if predictedDirection == "down" {
					change = -math.Abs(change)
				} else if predictedDirection == "up" {
					change = math.Abs(change)
				} else {
					change = 0
				}
				predictedChange = fuelPriceFloatPointer(change)
			}
		}
		for i := len(cells) - 1; i >= 0; i-- {
			if dateRaw := extractFuelPriceDate(cells[i]); dateRaw != "" {
				if normalized, dateOK := normalizeFuelPriceLocalTimestamp(dateRaw); dateOK {
					calculationDay = normalized
					break
				}
			}
		}
		return false
	})
	status := make(map[string]string, 4)
	for _, grade := range fuelPriceGradeOrder() {
		status[grade] = "current_price_reported_by_provider"
	}
	return fuelPriceToolResult{
		Version:                    1,
		Provider:                   fuelPriceProviderTuanyou,
		Publisher:                  "团友油价",
		Region:                     normalizedPageRegion,
		SourceURL:                  sourceURL,
		RetrievalTime:              t.currentTime().UTC().Format(time.RFC3339),
		UpdateTime:                 updateTime,
		Prices:                     prices,
		PriceStatus:                status,
		NextAdjustmentDate:         nextAdjustment,
		CrudeChangeRate:            crudeRate,
		PredictedDirection:         predictedDirection,
		PredictedChangeCNYPerL:     predictedChange,
		ForecastCalculationDay:     calculationDay,
		RequiredCurrentGradeCount:  4,
		AvailableCurrentGradeCount: 4,
	}, nil
}

func (t *FuelPriceTool) get(ctx context.Context, rawURL, accept string) ([]byte, string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, "", fmt.Errorf("invalid HTTP endpoint %q", rawURL)
	}
	requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "OpenKitty-FuelPrice/1.0 (+read-only)")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	actualURL := endpoint.String()
	if resp.Request != nil && resp.Request.URL != nil {
		actualURL = resp.Request.URL.String()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, actualURL, fmt.Errorf("HTTP %d: %s", resp.StatusCode, compactFuelPriceText(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fuelPriceMaxBodyBytes+1))
	if err != nil {
		return nil, actualURL, err
	}
	if int64(len(body)) > fuelPriceMaxBodyBytes {
		return nil, actualURL, fmt.Errorf("HTTP response exceeds %d bytes", fuelPriceMaxBodyBytes)
	}
	return body, actualURL, nil
}

func (t *FuelPriceTool) currentTime() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

func validateFuelPriceResult(result fuelPriceToolResult) error {
	if result.Version != 1 || strings.TrimSpace(result.Provider) == "" || strings.TrimSpace(result.Publisher) == "" {
		return errors.New("missing result identity")
	}
	if strings.TrimSpace(result.Region) == "" {
		return errors.New("missing result region")
	}
	if parsed, err := url.Parse(result.SourceURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("missing or invalid source URL")
	}
	if _, err := time.Parse(time.RFC3339, result.RetrievalTime); err != nil {
		return fmt.Errorf("invalid retrieval_time: %w", err)
	}
	if result.Prices == nil || result.PriceStatus == nil {
		return errors.New("missing price fields")
	}
	for _, grade := range fuelPriceGradeOrder() {
		value := result.Prices[grade]
		if value != nil {
			if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 1 || *value > 30 {
				return fmt.Errorf("%s price %.4f CNY/L is outside the accepted range [1, 30]", grade, *value)
			}
		}
		if strings.TrimSpace(result.PriceStatus[grade]) == "" {
			return fmt.Errorf("missing price status for %s", grade)
		}
	}
	if result.Provider == fuelPriceProviderRegionalOfficial {
		if result.Prices["92"] == nil || result.Prices["95"] == nil || result.Prices["0_diesel"] == nil || result.Prices["98"] != nil {
			return errors.New("official result must contain 92/95/0-diesel and must not invent a 98 price")
		}
		if result.PublishedTime == "" || result.EffectiveTime == "" {
			return errors.New("official publication/effective time is incomplete")
		}
	} else {
		for _, grade := range fuelPriceGradeOrder() {
			if result.Prices[grade] == nil {
				return fmt.Errorf("current prices are incomplete: missing %s", grade)
			}
		}
		if result.UpdateTime == "" {
			return errors.New("provider update time is missing")
		}
	}
	if result.PredictedDirection == "" {
		result.PredictedDirection = "unknown"
	}
	if result.PredictedDirection != "unknown" && result.PredictedDirection != "up" && result.PredictedDirection != "down" && result.PredictedDirection != "unchanged" {
		return fmt.Errorf("invalid predicted direction %q", result.PredictedDirection)
	}
	if result.PredictedDirection == "unknown" && result.PredictedChangeCNYPerL != nil {
		return errors.New("predicted change must be null when direction is unknown")
	}
	if result.RequiredCurrentGradeCount <= 0 || result.AvailableCurrentGradeCount != result.RequiredCurrentGradeCount {
		return fmt.Errorf("price completeness is %d/%d", result.AvailableCurrentGradeCount, result.RequiredCurrentGradeCount)
	}
	return nil
}

func renderFuelPriceResult(result fuelPriceToolResult) (string, error) {
	markerJSON, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode fuel-price result marker: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# Structured fuel price — %s\n", result.Publisher)
	fmt.Fprintf(&out, "%s%s\n\n", fuelPriceResultMarker, markerJSON)
	fmt.Fprintf(&out, "Provider key: %s\n", result.Provider)
	fmt.Fprintf(&out, "Region: %s\n", result.Region)
	out.WriteString("Currency/unit: CNY/L\n")
	fmt.Fprintf(&out, "Time metadata (preserve labels exactly): retrieval_time=%s; published_time=%s; effective_time=%s; update_time=%s\n",
		result.RetrievalTime, fuelPriceValueOrNotProvided(result.PublishedTime), fuelPriceValueOrNotProvided(result.EffectiveTime), fuelPriceValueOrNotProvided(result.UpdateTime))
	fmt.Fprintf(&out, "Source: %s\n\n", result.SourceURL)
	out.WriteString("## Current retail prices (NOT forecast)\n\n")
	out.WriteString("| Grade | Price (CNY/L) | Status / authority |\n")
	out.WriteString("|---|---:|---|\n")
	labels := map[string]string{"92": "92 gasoline", "95": "95 gasoline", "98": "98 gasoline", "0_diesel": "0 diesel"}
	for _, grade := range fuelPriceGradeOrder() {
		value := "not published"
		if result.Prices[grade] != nil {
			value = fmt.Sprintf("%.2f", *result.Prices[grade])
		}
		fmt.Fprintf(&out, "| %s | %s | %s |\n", labels[grade], value, result.PriceStatus[grade])
	}
	fmt.Fprintf(&out, "\nCompleteness: current required grades %d/%d.\n", result.AvailableCurrentGradeCount, result.RequiredCurrentGradeCount)
	if result.Provider == fuelPriceProviderRegionalOfficial {
		out.WriteString("Official scope: values are maximum retail prices. The notice does not publish a 98-gasoline price; that grade is enterprise-priced and no 98 value is inferred.\n")
	}
	out.WriteString("\n## Next adjustment / forecast (NOT current price)\n\n")
	fmt.Fprintf(&out, "next_adjustment_date: %s\n", fuelPriceValueOrUnknown(result.NextAdjustmentDate))
	fmt.Fprintf(&out, "crude_change_rate: %s\n", fuelPricePercentOrUnknown(result.CrudeChangeRate))
	fmt.Fprintf(&out, "predicted_direction: %s\n", fuelPriceValueOrUnknown(result.PredictedDirection))
	fmt.Fprintf(&out, "predicted_change_cny_per_l: %s\n", fuelPriceNumberOrUnknown(result.PredictedChangeCNYPerL))
	fmt.Fprintf(&out, "forecast_calculation_day: %s\n", fuelPriceValueOrUnknown(result.ForecastCalculationDay))
	out.WriteString("\nBoundary: forecast fields describe a possible future adjustment only. Never substitute them for, add them to, or subtract them from the current-price table unless a later effective official notice is fetched.\n")
	return out.String(), nil
}

func fuelPriceGradeOrder() []string { return []string{"92", "95", "98", "0_diesel"} }

func fuelPriceGradeKey(value string) string {
	compact := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(compactFuelPriceText(value), " ", ""), "＃", "#"))
	switch {
	case strings.Contains(compact, "92号汽油") || strings.Contains(compact, "92#汽油"):
		return "92"
	case strings.Contains(compact, "95号汽油") || strings.Contains(compact, "95#汽油"):
		return "95"
	case strings.Contains(compact, "98号汽油") || strings.Contains(compact, "98#汽油"):
		return "98"
	case regexp.MustCompile(`(^|[^0-9-])0(?:号|#)?柴油`).MatchString(compact):
		return "0_diesel"
	default:
		return ""
	}
}

func setFuelPriceIfConsistent(prices map[string]*float64, grade string, value float64) {
	if prices[grade] == nil {
		prices[grade] = fuelPriceFloatPointer(value)
	}
}

func parseFuelPriceJSONNumber(raw json.RawMessage) (float64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return 0, false
		}
		return parseFuelPriceNumber(value)
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func parseFuelPriceNumber(value string) (float64, bool) {
	match := regexp.MustCompile(`[+-]?[0-9]+(?:\.[0-9]+)?`).FindString(compactFuelPriceText(value))
	if match == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(match, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func parseFuelPricePercent(value string) (float64, bool) {
	match := regexp.MustCompile(`([+-]?[0-9]+(?:\.[0-9]+)?)\s*%`).FindStringSubmatch(value)
	if len(match) != 2 {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(match[1], 64)
	return parsed, err == nil && parsed >= -100 && parsed <= 100
}

func fuelPricePredictionDirection(value string) string {
	value = compactFuelPriceText(value)
	switch {
	case strings.Contains(value, "下调") || strings.Contains(value, "下降") || strings.Contains(value, "降"):
		return "down"
	case strings.Contains(value, "上调") || strings.Contains(value, "上涨") || strings.Contains(value, "涨"):
		return "up"
	case strings.Contains(value, "不调整") || strings.Contains(value, "搁浅") || strings.Contains(value, "持平"):
		return "unchanged"
	default:
		return "unknown"
	}
}

func normalizeShanghaiFuelRegion(value string) (string, bool) {
	canonical := canonicalFuelPriceRegion(value)
	if strings.EqualFold(canonical, "上海") {
		return "上海", true
	}
	return "", false
}

func canonicalFuelPriceRegion(value string) string {
	value = compactFuelPriceText(value)
	value = strings.TrimSpace(strings.TrimSuffix(value, "省"))
	value = strings.TrimSpace(strings.TrimSuffix(value, "市"))
	english := strings.ToLower(strings.ReplaceAll(value, " ", ""))
	if english == "shanghai" || english == "shanghai,china" || value == "上海" {
		return "上海"
	}
	return value
}

func fuelPriceRegionsEqual(left, right string) bool {
	return strings.EqualFold(canonicalFuelPriceRegion(left), canonicalFuelPriceRegion(right))
}

func extractFuelPriceDate(value string) string {
	if match := regexp.MustCompile(`[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}`).FindString(value); match != "" {
		return match
	}
	return regexp.MustCompile(`[0-9]{4}\s*年\s*[0-9]{1,2}\s*月\s*[0-9]{1,2}\s*日`).FindString(value)
}

func parseFuelPriceDate(value string) (time.Time, bool) {
	normalized, ok := normalizeFuelPriceLocalTimestamp(value)
	if !ok {
		return time.Time{}, false
	}
	if len(normalized) == len("2006-01-02") {
		parsed, err := time.ParseInLocation("2006-01-02", normalized, fuelPriceShanghaiLocation)
		return parsed, err == nil
	}
	parsed, err := time.Parse(time.RFC3339, normalized)
	return parsed, err == nil
}

func normalizeFuelPriceLocalTimestamp(value string) (string, bool) {
	value = compactFuelPriceText(value)
	if value == "" {
		return "", false
	}
	chinese := regexp.MustCompile(`^([0-9]{4})\s*年\s*([0-9]{1,2})\s*月\s*([0-9]{1,2})\s*日(?:\s*([0-9]{1,2})\s*时)?(?:\s*([0-9]{1,2})\s*分)?(?:\s*([0-9]{1,2})\s*秒)?$`)
	if match := chinese.FindStringSubmatch(value); len(match) == 7 {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		hour, minute, second := 0, 0, 0
		hasTime := match[4] != ""
		if hasTime {
			hour, _ = strconv.Atoi(match[4])
		}
		if match[5] != "" {
			minute, _ = strconv.Atoi(match[5])
		}
		if match[6] != "" {
			second, _ = strconv.Atoi(match[6])
		}
		parsed := time.Date(year, time.Month(month), day, hour, minute, second, 0, fuelPriceShanghaiLocation)
		if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day || parsed.Hour() != hour {
			return "", false
		}
		if !hasTime {
			return parsed.Format("2006-01-02"), true
		}
		return parsed.Format(time.RFC3339), true
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-1-2 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, fuelPriceShanghaiLocation); err == nil {
			return parsed.Format(time.RFC3339), true
		}
	}
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if parsed, err := time.ParseInLocation(layout, value, fuelPriceShanghaiLocation); err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}

func compactFuelPriceText(value string) string {
	return strings.Join(strings.Fields(stdhtml.UnescapeString(strings.TrimSpace(value))), " ")
}

func fuelPriceFloatPointer(value float64) *float64 { return &value }

func fuelPriceValueOrNotProvided(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not_provided"
	}
	return value
}

func fuelPriceValueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func fuelPricePercentOrUnknown(value *float64) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.2f%%", *value)
}

func fuelPriceNumberOrUnknown(value *float64) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.2f", *value)
}
