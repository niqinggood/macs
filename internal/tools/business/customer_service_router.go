package business

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type CustomerServiceModelConfig struct {
	APIKey    string
	BaseURL   string
	ModelName string
}

type CustomerServiceRouter struct {
	modelConfig *CustomerServiceModelConfig
	logger      *log.Logger
}

func NewCustomerServiceRouter(modelConfig *CustomerServiceModelConfig, logger *log.Logger) *CustomerServiceRouter {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &CustomerServiceRouter{modelConfig: modelConfig, logger: logger}
}

func (r *CustomerServiceRouter) Classify(message string) CustomerServiceIntent {
	if result := r.KeywordMatch(message); result != nil && result.Confidence > 0.6 {
		r.logger.Printf("[CSRouter] keyword match: %s/%s (conf=%.2f)", result.Category, result.SubIntent, result.Confidence)
		return *result
	}
	if r.modelConfig != nil && r.modelConfig.APIKey != "" {
		if result := r.llmClassify(message); result != nil {
			r.logger.Printf("[CSRouter] LLM classify: %s/%s (conf=%.2f)", result.Category, result.SubIntent, result.Confidence)
			return *result
		}
	}
	r.logger.Printf("[CSRouter] defaulting to general inquiry")
	return CustomerServiceIntent{
		Category:    CSGeneral,
		SubIntent:   "general_inquiry",
		Confidence:  0.3,
		NeedHandoff: false,
		Reasoning:   "未匹配到具体意图，作为通用咨询处理",
	}
}

func (r *CustomerServiceRouter) KeywordMatch(message string) *CustomerServiceIntent {
	message = strings.ToLower(message)
	preSalesKeywords := map[string][]string{
		"product_inquiry":   {"有没有", "多少钱", "价格", "规格", "参数", "商品", "产品", "介绍", "推荐", "哪个好", "对比"},
		"stock_check":       {"有货吗", "库存", "缺货", "补货", "什么时候到"},
		"promotion_inquiry": {"促销", "打折", "优惠", "秒杀", "满减", "活动", "折扣"},
	}
	duringSalesKeywords := map[string][]string{
		"order_placement": {"下单", "怎么买", "购买", "支付方式", "微信支付", "支付宝"},
		"order_modify":    {"改地址", "修改地址", "修改订单", "换商品", "加购"},
		"payment_issue":   {"支付失败", "付不了", "扣款", "重复支付", "支付异常"},
		"cancel_order":    {"取消订单", "不想要了", "退单"},
	}
	afterSalesKeywords := map[string][]string{
		"logistics_track": {"物流", "快递", "到哪了", "发货", "什么时候到", "查单号", "快递单"},
		"refund_request":  {"退款", "退货", "退钱", "不退款", "退回去"},
		"complaint":       {"投诉", "举报", "不满意", "差评", "问题", "质量有问题", "破损", "坏的", "虚假"},
		"return_goods":    {"退货地址", "怎么退", "退回哪里", "寄回去"},
	}

	bestScore := 0
	var bestCategory CustomerServiceCategory
	bestSubIntent := ""
	check := func(category CustomerServiceCategory, groups map[string][]string) {
		for subIntent, keywords := range groups {
			score := 0
			for _, keyword := range keywords {
				if strings.Contains(message, keyword) {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestCategory = category
				bestSubIntent = subIntent
			}
		}
	}
	check(CSPreSales, preSalesKeywords)
	check(CSDuringSales, duringSalesKeywords)
	check(CSAfterSales, afterSalesKeywords)
	if bestScore == 0 {
		return nil
	}
	confidence := float64(bestScore) / 4.0
	if confidence > 1.0 {
		confidence = 1.0
	}
	return &CustomerServiceIntent{
		Category:    bestCategory,
		SubIntent:   bestSubIntent,
		Confidence:  confidence,
		NeedHandoff: false,
		Reasoning:   fmt.Sprintf("关键词匹配: score=%d", bestScore),
	}
}

func (r *CustomerServiceRouter) llmClassify(message string) *CustomerServiceIntent {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	resultChannel := make(chan *CustomerServiceIntent, 1)
	go func() {
		content, err := r.callLLM(ctx, buildCustomerServiceIntentPrompt(message))
		if err != nil {
			r.logger.Printf("[CSRouter] LLM error: %v", err)
			resultChannel <- nil
			return
		}
		resultChannel <- r.parseIntentResult(content)
	}()
	select {
	case result := <-resultChannel:
		return result
	case <-ctx.Done():
		r.logger.Printf("[CSRouter] LLM timeout")
		return nil
	}
}

func buildCustomerServiceIntentPrompt(message string) string {
	return `你是一个电商客服意图分类器。分析用户消息，输出JSON。

## 分类体系
- pre_sales (售前): 商品咨询、价格、库存、促销、推荐、对比
- during_sales (售中): 下单、支付问题、修改订单、取消订单、改地址
- after_sales (售后): 物流查询、退款、退货、投诉、质量问题
- general (通用): 问候、闲聊、FAQ、帮助

## 子意图
product_inquiry / stock_check / promotion_inquiry / order_placement / order_modify / payment_issue / cancel_order / logistics_track / refund_request / complaint / return_goods / general_inquiry

## 用户消息
` + message + `

## 输出格式
{"category": "after_sales", "sub_intent": "logistics_track", "confidence": 0.95, "need_handoff": false, "reasoning": "用户询问快递进度"}

need_handoff: 当用户明确要求人工客服、投诉情绪强烈、或涉及敏感问题时设为true
只输出JSON，不要其他内容。`
}

func (r *CustomerServiceRouter) parseIntentResult(content string) *CustomerServiceIntent {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var result CustomerServiceIntent
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		r.logger.Printf("[CSRouter] parse error: %v", err)
		return nil
	}
	switch result.Category {
	case CSPreSales, CSDuringSales, CSAfterSales, CSGeneral:
	default:
		result.Category = CSGeneral
	}
	return &result
}

func (r *CustomerServiceRouter) callLLM(ctx context.Context, prompt string) (string, error) {
	config := r.modelConfig
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":%q}],"max_tokens":256,"temperature":0}`,
		config.ModelName, prompt)
	url := strings.TrimRight(config.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+config.APIKey)
	response, err := (&http.Client{Timeout: 6 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", nil
	}
	return result.Choices[0].Message.Content, nil
}
