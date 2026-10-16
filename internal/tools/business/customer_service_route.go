package business

import (
	"context"
	"encoding/json"
	"fmt"
)

type CustomerServiceCategory string

const (
	CSPreSales    CustomerServiceCategory = "pre_sales"
	CSDuringSales CustomerServiceCategory = "during_sales"
	CSAfterSales  CustomerServiceCategory = "after_sales"
	CSGeneral     CustomerServiceCategory = "general"
)

type CustomerServiceIntent struct {
	Category    CustomerServiceCategory `json:"category"`
	SubIntent   string                  `json:"sub_intent"`
	Confidence  float64                 `json:"confidence"`
	NeedHandoff bool                    `json:"need_handoff"`
	Reasoning   string                  `json:"reasoning"`
}

type CustomerServiceClassifier interface {
	Classify(string) CustomerServiceIntent
}

// CSRouteTool exposes customer-service intent classification to pipelines.
type CSRouteTool struct {
	classifier CustomerServiceClassifier
}

func NewCSRouteTool(classifier CustomerServiceClassifier) *CSRouteTool {
	return &CSRouteTool{classifier: classifier}
}

func (t *CSRouteTool) Name() string { return "cs_route" }

func (t *CSRouteTool) Description() string {
	return "分析客户的意图，分类为售前/售中/售后/通用。返回分类结果和置信度。用于智能路由到对应的客服子 Agent。"
}

func (t *CSRouteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "客户的消息内容"},
		},
		"required": []string{"message"},
	}
}

func (t *CSRouteTool) IsReadOnly() bool { return true }

func (t *CSRouteTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.classifier == nil {
		return "", fmt.Errorf("customer-service classifier is not available")
	}
	message, _ := params["message"].(string)
	result := t.classifier.Classify(message)
	data, _ := json.MarshalIndent(result, "", "  ")
	return fmt.Sprintf("意图分类结果:\n```json\n%s\n```", string(data)), nil
}
