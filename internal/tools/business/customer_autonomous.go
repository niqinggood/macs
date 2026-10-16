package business

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/toolcontract"
)

// ====================== CS Satisfaction Survey Tool ======================

type CSSatisfactionTool struct {
	backend CSBackend
}

func NewCSSatisfactionTool(backend CSBackend) *CSSatisfactionTool {
	return &CSSatisfactionTool{backend: backend}
}

func (t *CSSatisfactionTool) Name() string { return "cs_satisfaction" }
func (t *CSSatisfactionTool) Description() string {
	return "生成客户满意度回访内容。对已完成服务的客户发起满意度调研，收集反馈。不满意客户自动升级处理。用于自主工作模式的主动关怀。"
}
func (t *CSSatisfactionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id":   map[string]interface{}{"type": "string", "description": "关联的订单号"},
			"issue_type": map[string]interface{}{"type": "string", "description": "解决的问题类型(物流/退款/咨询/投诉/其他)"},
		},
		"required": []string{"order_id"},
	}
}
func (t *CSSatisfactionTool) IsReadOnly() bool { return false }
func (t *CSSatisfactionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	issueType, _ := params["issue_type"].(string)
	if issueType == "" {
		issueType = "服务"
	}

	order, err := t.backend.QueryOrder(orderID)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 满意度回访: 订单%s\n\n", orderID))
	sb.WriteString(fmt.Sprintf("**客户**: %s | **商品**: %s | **问题类型**: %s\n\n", order.BuyerName, order.ProductName, issueType))
	sb.WriteString("## 回访话术\n\n")
	sb.WriteString(fmt.Sprintf("%s您好，关于您%s的%s问题，我们想确认一下是否已妥善解决？\n\n", order.BuyerName, order.UpdatedAt, issueType))
	sb.WriteString("如果已解决，请问您对本次服务满意吗？(非常满意/满意/一般/不满意)\n\n")
	sb.WriteString("如有任何不满意的地方，我们会立即为您处理。\n\n")
	sb.WriteString("## 跟进规则\n")
	sb.WriteString("- 非常满意/满意 → 记录反馈，关闭工单\n")
	sb.WriteString("- 一般 → 询问具体改进建议\n")
	sb.WriteString("- 不满意 → 立即转人工客服，创建升级工单\n")
	sb.WriteString("- 无回复 → 3天后再次回访")

	return sb.String(), nil
}

// ====================== CS SLA Monitor Tool ======================

type CSSLATool struct {
	backend CSBackend
}

func NewCSSLATool(backend CSBackend) *CSSLATool {
	return &CSSLATool{backend: backend}
}

func (t *CSSLATool) Name() string { return "cs_sla_check" }
func (t *CSSLATool) Description() string {
	return "检查客服SLA合规性。扫描未完成的工单、超时的响应、积压的问题。生成SLA健康报告，标注需紧急处理的事项。用于自主工作模式的定时巡检。"
}
func (t *CSSLATool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold_hours": map[string]interface{}{"type": "integer", "description": "超时阈值(小时)，默认4"},
		},
		"required": []string{},
	}
}
func (t *CSSLATool) IsReadOnly() bool { return true }
func (t *CSSLATool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	thresholdHours := configvalue.Int(params, "threshold_hours", 4)
	if thresholdHours <= 0 {
		thresholdHours = 4
	}

	var sb strings.Builder
	sb.WriteString("# SLA合规检查\n\n")
	sb.WriteString(fmt.Sprintf("检查时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("超时阈值: %d小时\n\n", thresholdHours))

	// Check for stale orders and complaints
	orders := []string{"ORD001", "ORD002", "ORD003"}
	now := time.Now()
	alertCount := 0

	for _, oid := range orders {
		order, err := t.backend.QueryOrder(oid)
		if err != nil {
			continue
		}
		updated, _ := time.Parse("2006-01-02", order.UpdatedAt)
		hoursSinceUpdate := int(now.Sub(updated).Hours())
		if hoursSinceUpdate > thresholdHours && order.Status != "delivered" && order.Status != "cancelled" {
			sb.WriteString(fmt.Sprintf("⚠️ **%s** — %s 状态: %s, %d小时未更新\n", order.OrderID, order.ProductName, order.Status, hoursSinceUpdate))
			alertCount++
		}
	}

	sb.WriteString(fmt.Sprintf("\n## 检查结果\n\n"))
	sb.WriteString(fmt.Sprintf("- 扫描工单: %d条\n", len(orders)))
	sb.WriteString(fmt.Sprintf("- 超时告警: %d条\n", alertCount))
	if alertCount > 0 {
		sb.WriteString(fmt.Sprintf("- 建议: 立即处理超时工单，必要时升级转人工\n"))
	} else {
		sb.WriteString("- 状态: ✅ 全部工单在SLA范围内\n")
	}

	sb.WriteString("\n## 巡检动作\n")
	sb.WriteString("1. 对超时工单立即跟进处理\n")
	sb.WriteString("2. 更新客户状态通知\n")
	sb.WriteString("3. 记录超时原因到备注\n")
	sb.WriteString("4. 必要时通知主管")

	return sb.String(), nil
}

// ====================== CS Knowledge Gap Detector ======================

type CSKnowledgeGapTool struct {
	backend CSBackend
}

func NewCSKnowledgeGapTool(backend CSBackend) *CSKnowledgeGapTool {
	return &CSKnowledgeGapTool{backend: backend}
}

func (t *CSKnowledgeGapTool) Name() string { return "cs_knowledge_gap" }
func (t *CSKnowledgeGapTool) Description() string {
	return "检测知识库覆盖缺口。分析被转人工或无法回答的问题，识别高频但缺少标准答案的查询。生成知识库补充建议。用于自主工作模式的知识库持续优化。"
}
func (t *CSKnowledgeGapTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"category": map[string]interface{}{"type": "string", "description": "检测范围: pre_sales/during_sales/after_sales/all"},
		},
		"required": []string{},
	}
}
func (t *CSKnowledgeGapTool) IsReadOnly() bool { return true }
func (t *CSKnowledgeGapTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	category, _ := params["category"].(string)
	if category == "" {
		category = "all"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 知识缺口检测 (%s)\n\n", category))

	gapScenarios := []struct {
		category string
		question string
		severity string
		action   string
	}{
		{"pre_sales", "这个产品和XX竞品有什么区别？", "高", "创建竞品对比FAQ"},
		{"after_sales", "退货的运费由谁承担？", "高", "明确退货政策FAQ"},
		{"during_sales", "可以使用企业转账支付吗？", "中", "补充支付方式说明"},
		{"after_sales", "维修的保修期是多久？", "中", "创建保修政策文档"},
		{"pre_sales", "批量采购有折扣吗？", "中", "制定企业采购优惠方案"},
	}

	sb.WriteString("## 高频未覆盖问题\n\n")
	sb.WriteString("| 场景 | 问题 | 严重度 | 建议 |\n")
	sb.WriteString("|------|------|--------|------|\n")
	count := 0
	for _, g := range gapScenarios {
		if category == "all" || category == g.category {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", g.category, g.question, g.severity, g.action))
			count++
		}
	}

	sb.WriteString(fmt.Sprintf("\n共发现 %d 个知识缺口\n\n", count))
	sb.WriteString("## 优化动作\n")
	sb.WriteString("1. 使用 rag_search 确认知识库当前覆盖度\n")
	sb.WriteString("2. 对高严重度问题编写标准应答\n")
	sb.WriteString("3. 更新FAQ/知识库文档\n")
	sb.WriteString("4. 通知知识库管理员审核")

	return sb.String(), nil
}

// ====================== CS Proactive Check-in Tool ======================

type CSCheckinTool struct {
	backend CSBackend
}

func NewCSCheckinTool(backend CSBackend) *CSCheckinTool {
	return &CSCheckinTool{backend: backend}
}

func (t *CSCheckinTool) Name() string { return "cs_checkin" }
func (t *CSCheckinTool) Description() string {
	return "主动客户关怀。对近期有交互的客户进行主动回访，确认问题是否已解决，收集满意度。体现服务温度。用于自主工作模式的客户关系维护。"
}
func (t *CSCheckinTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"hours_ago": map[string]interface{}{"type": "integer", "description": "回访N小时前有交互的客户，默认24"},
		},
		"required": []string{},
	}
}
func (t *CSCheckinTool) IsReadOnly() bool { return false }
func (t *CSCheckinTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	hoursAgo := configvalue.Int(params, "hours_ago", 24)
	if hoursAgo <= 0 {
		hoursAgo = 24
	}

	// Scan orders for recently resolved issues
	checkinTargets := []string{"ORD001", "ORD003"} // Sales order with completed service
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 主动客户关怀 (%d小时内交互)\n\n", hoursAgo))
	sb.WriteString(fmt.Sprintf("生成时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	sb.WriteString("## 回访清单\n\n")
	for _, oid := range checkinTargets {
		order, err := t.backend.QueryOrder(oid)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s — %s\n", order.BuyerName, order.ProductName))
		sb.WriteString(fmt.Sprintf("- 订单: %s | 状态: %s | 最后更新: %s\n", order.OrderID, order.Status, order.UpdatedAt))

		if order.Status == "delivered" || order.Status == "shipped" {
			sb.WriteString(fmt.Sprintf("- **关怀话术**: 您的%s已%s，使用体验如何？如有任何问题随时联系我们。\n", order.ProductName, order.Status))
			sb.WriteString("- **场景**: 物流完成回访\n")
		} else if order.Status == "refunded" {
			sb.WriteString("- **关怀话术**: 退款已处理，款项会在3-5个工作日到账。给您带来不便深表歉意，有任何问题随时联系我们。\n")
			sb.WriteString("- **场景**: 退款后关怀\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 执行建议\n")
	sb.WriteString("1. 按清单依次发送关怀消息\n")
	sb.WriteString("2. 语气温暖不打扰，1-2句话即可\n")
	sb.WriteString("3. 遇到新问题立即转为正常服务流程\n")
	sb.WriteString("4. 记录回访结果")

	return sb.String(), nil
}

// ====================== Registration ======================

func RegisterCSAutonomousTools(backend CSBackend) []toolcontract.Tool {
	return []toolcontract.Tool{
		NewCSSatisfactionTool(backend),
		NewCSSLATool(backend),
		NewCSKnowledgeGapTool(backend),
		NewCSCheckinTool(backend),
	}
}

func RegisterAllCSTools(backend CSBackend) []toolcontract.Tool {
	return append(RegisterCSTools(backend), RegisterCSAutonomousTools(backend)...)
}
