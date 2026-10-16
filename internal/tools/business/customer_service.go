package business

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"mac/internal/toolcontract"
)

// The universal CSBackend interface is defined in context_assembler.go.
// CSMockBackend implements the core ecommerce methods. The full universal
// implementation is MockCSBackend in context_assembler.go which embeds this.

// ====================== CS Data Types ======================

type CSOrder struct {
	OrderID     string  `json:"order_id"`
	Status      string  `json:"status"` // pending/paid/shipped/delivered/cancelled/refunded
	Amount      float64 `json:"amount"`
	ProductName string  `json:"product_name"`
	BuyerName   string  `json:"buyer_name"`
	Address     string  `json:"address"`
	TrackingNo  string  `json:"tracking_no,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type CSLogistics struct {
	TrackingNo  string       `json:"tracking_no"`
	Carrier     string       `json:"carrier"`
	Status      string       `json:"status"` // in_transit/out_for_delivery/delivered/exception
	Origin      string       `json:"origin"`
	Destination string       `json:"destination"`
	Events      []CSLogEvent `json:"events"`
	EstimatedAt string       `json:"estimated_at,omitempty"`
}

type CSLogEvent struct {
	Time     string `json:"time"`
	Status   string `json:"status"`
	Location string `json:"location"`
	Desc     string `json:"desc"`
}

type CSRefundResult struct {
	RefundID  string  `json:"refund_id"`
	Success   bool    `json:"success"`
	Amount    float64 `json:"amount"`
	Message   string  `json:"message"`
	CreatedAt string  `json:"created_at"`
}

type CSProduct struct {
	Name        string  `json:"name"`
	SKU         string  `json:"sku"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	OnPromotion bool    `json:"on_promotion"`
}

// ====================== Mock Backend ======================

type CSMockBackend struct {
	orders    map[string]*CSOrder
	logistics map[string]*CSLogistics
	products  []CSProduct
	mu        sync.RWMutex
}

func NewCSMockBackend() *CSMockBackend {
	m := &CSMockBackend{
		orders:    make(map[string]*CSOrder),
		logistics: make(map[string]*CSLogistics),
		products:  make([]CSProduct, 0),
	}
	m.seedData()
	return m
}

func (m *CSMockBackend) seedData() {
	m.orders["ORD001"] = &CSOrder{
		OrderID: "ORD001", Status: "shipped", Amount: 299.00,
		ProductName: "无线蓝牙耳机 Pro", BuyerName: "张三",
		Address: "北京市朝阳区xxx路10号", TrackingNo: "SF1234567890",
		CreatedAt: "2026-06-25", UpdatedAt: "2026-06-26",
	}
	m.orders["ORD002"] = &CSOrder{
		OrderID: "ORD002", Status: "pending", Amount: 1599.00,
		ProductName: "智能手表 S3", BuyerName: "李四",
		Address:   "上海市浦东新区xxx路20号",
		CreatedAt: "2026-06-27", UpdatedAt: "2026-06-27",
	}
	m.orders["ORD003"] = &CSOrder{
		OrderID: "ORD003", Status: "delivered", Amount: 49.90,
		ProductName: "USB-C 数据线 2米", BuyerName: "王五",
		Address: "广州市天河区xxx路30号", TrackingNo: "YT9876543210",
		CreatedAt: "2026-06-20", UpdatedAt: "2026-06-23",
	}

	m.logistics["SF1234567890"] = &CSLogistics{
		TrackingNo: "SF1234567890", Carrier: "顺丰速运",
		Status: "in_transit", Origin: "深圳", Destination: "北京",
		EstimatedAt: "2026-06-28",
		Events: []CSLogEvent{
			{Time: "2026-06-26 10:00", Status: "已揽收", Location: "深圳集散中心", Desc: "快件已揽收"},
			{Time: "2026-06-26 18:00", Status: "运输中", Location: "广州中转场", Desc: "快件离开广州中转场"},
			{Time: "2026-06-27 08:00", Status: "运输中", Location: "北京中转场", Desc: "快件到达北京中转场"},
		},
	}
	m.logistics["YT9876543210"] = &CSLogistics{
		TrackingNo: "YT9876543210", Carrier: "圆通速递",
		Status: "delivered", Origin: "杭州", Destination: "广州",
		Events: []CSLogEvent{
			{Time: "2026-06-21 09:00", Status: "已揽收", Location: "杭州", Desc: "快件已揽收"},
			{Time: "2026-06-22 14:00", Status: "运输中", Location: "广州中转场", Desc: "快件到达广州"},
			{Time: "2026-06-23 11:00", Status: "已签收", Location: "广州天河区", Desc: "快件已由本人签收"},
		},
	}

	m.products = []CSProduct{
		{Name: "无线蓝牙耳机 Pro", SKU: "BT-001", Price: 299.00, Stock: 150, Category: "数码配件", Description: "主动降噪，续航40小时", OnPromotion: true},
		{Name: "智能手表 S3", SKU: "WT-002", Price: 1599.00, Stock: 50, Category: "智能穿戴", Description: "心率血氧监测，GPS定位", OnPromotion: false},
		{Name: "USB-C 数据线 2米", SKU: "CB-003", Price: 49.90, Stock: 500, Category: "数码配件", Description: "100W快充，编织材质", OnPromotion: false},
		{Name: "手机壳 透明", SKU: "AC-004", Price: 29.90, Stock: 300, Category: "手机配件", Description: "防摔硅胶，不发黄", OnPromotion: true},
	}
}

func (m *CSMockBackend) QueryOrder(orderID string) (*CSOrder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orderID = strings.ToUpper(strings.TrimSpace(orderID))
	if o, ok := m.orders[orderID]; ok {
		return o, nil
	}
	return nil, fmt.Errorf("订单 %s 未找到", orderID)
}

func (m *CSMockBackend) QueryLogistics(trackingNo string) (*CSLogistics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	trackingNo = strings.TrimSpace(trackingNo)
	if l, ok := m.logistics[trackingNo]; ok {
		return l, nil
	}
	return nil, fmt.Errorf("运单 %s 未找到", trackingNo)
}

func (m *CSMockBackend) RefundOrder(orderID, reason string) (*CSRefundResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orderID = strings.ToUpper(strings.TrimSpace(orderID))
	o, ok := m.orders[orderID]
	if !ok {
		return nil, fmt.Errorf("订单 %s 未找到", orderID)
	}
	if o.Status == "refunded" {
		return nil, fmt.Errorf("订单 %s 已退款，无法重复操作", orderID)
	}
	o.Status = "refunded"
	return &CSRefundResult{
		RefundID: fmt.Sprintf("RFND_%s_%d", orderID, time.Now().UnixNano()%100000),
		Success:  true, Amount: o.Amount,
		Message:   fmt.Sprintf("退款成功，预计3-5个工作日到账。退款原因: %s", reason),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *CSMockBackend) CancelOrder(orderID string) (*CSRefundResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orderID = strings.ToUpper(strings.TrimSpace(orderID))
	o, ok := m.orders[orderID]
	if !ok {
		return nil, fmt.Errorf("订单 %s 未找到", orderID)
	}
	if o.Status != "pending" {
		return nil, fmt.Errorf("订单 %s 当前状态为%s，无法取消。仅待支付订单可取消", orderID, o.Status)
	}
	o.Status = "cancelled"
	return &CSRefundResult{
		RefundID: fmt.Sprintf("CNCL_%s_%d", orderID, time.Now().UnixNano()%100000),
		Success:  true, Amount: o.Amount,
		Message:   "订单已成功取消",
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *CSMockBackend) ChangeAddress(orderID, newAddress string) (*CSRefundResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orderID = strings.ToUpper(strings.TrimSpace(orderID))
	o, ok := m.orders[orderID]
	if !ok {
		return nil, fmt.Errorf("订单 %s 未找到", orderID)
	}
	if o.Status == "shipped" || o.Status == "delivered" {
		return nil, fmt.Errorf("订单 %s 已%s，无法修改地址", orderID, o.Status)
	}
	oldAddr := o.Address
	o.Address = newAddress
	return &CSRefundResult{
		RefundID:  fmt.Sprintf("ADDRCHG_%s_%d", orderID, time.Now().UnixNano()%100000),
		Success:   true,
		Message:   fmt.Sprintf("地址已从 [%s] 修改为 [%s]", oldAddr, newAddress),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (m *CSMockBackend) QueryProduct(productName string) ([]CSProduct, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []CSProduct
	productName = strings.ToLower(strings.TrimSpace(productName))
	for _, p := range m.products {
		if strings.Contains(strings.ToLower(p.Name), productName) ||
			strings.Contains(strings.ToLower(p.SKU), productName) ||
			strings.Contains(strings.ToLower(p.Category), productName) {
			results = append(results, p)
		}
	}
	return results, nil
}

func (m *CSMockBackend) SubmitComplaint(orderID, reason, description string) (string, error) {
	ticketID := fmt.Sprintf("TK_%d", time.Now().UnixNano()%1000000)
	return fmt.Sprintf("投诉工单已创建: %s\n订单: %s\n原因: %s\n描述: %s\n客服将在24小时内联系您", ticketID, orderID, reason, description), nil
}

// ====================== CS Tool: Order Query ======================

type CSOrderQueryTool struct {
	backend CSBackend
}

func NewCSOrderQueryTool(backend CSBackend) *CSOrderQueryTool {
	return &CSOrderQueryTool{backend: backend}
}

func (t *CSOrderQueryTool) Name() string { return "cs_order_query" }
func (t *CSOrderQueryTool) Description() string {
	return "查询订单状态和详情。输入订单号(如ORD001)，返回订单状态、金额、商品、收货地址、物流单号等信息。"
}
func (t *CSOrderQueryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id": map[string]interface{}{"type": "string", "description": "订单号"},
		},
		"required": []string{"order_id"},
	}
}
func (t *CSOrderQueryTool) IsReadOnly() bool { return true }
func (t *CSOrderQueryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	order, err := t.backend.QueryOrder(orderID)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}
	data, _ := json.MarshalIndent(order, "", "  ")
	return fmt.Sprintf("订单查询结果:\n```json\n%s\n```", string(data)), nil
}

// ====================== CS Tool: Logistics Tracking ======================

type CSLogisticsTool struct {
	backend CSBackend
}

func NewCSLogisticsTool(backend CSBackend) *CSLogisticsTool {
	return &CSLogisticsTool{backend: backend}
}

func (t *CSLogisticsTool) Name() string { return "cs_logistics_track" }
func (t *CSLogisticsTool) Description() string {
	return "查询物流轨迹。输入物流单号或订单号，返回物流公司、当前状态、各节点轨迹、预计到达时间。"
}
func (t *CSLogisticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tracking_no": map[string]interface{}{"type": "string", "description": "物流单号或订单号"},
		},
		"required": []string{"tracking_no"},
	}
}
func (t *CSLogisticsTool) IsReadOnly() bool { return true }
func (t *CSLogisticsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	trackingNo, _ := params["tracking_no"].(string)

	// Try as order ID first, then as tracking number
	logistics, err := t.backend.QueryLogistics(trackingNo)
	if err != nil {
		// Try as order ID → extract tracking number
		order, orderErr := t.backend.QueryOrder(trackingNo)
		if orderErr == nil && order.TrackingNo != "" {
			logistics, err = t.backend.QueryLogistics(order.TrackingNo)
		}
	}
	if err != nil {
		return fmt.Sprintf("查询失败: %v。请确认单号是否正确，或转人工客服查询。", err), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 物流追踪: %s\n\n", logistics.TrackingNo))
	sb.WriteString(fmt.Sprintf("- **快递公司**: %s\n", logistics.Carrier))
	sb.WriteString(fmt.Sprintf("- **当前状态**: %s\n", logistics.Status))
	sb.WriteString(fmt.Sprintf("- **出发地**: %s → **目的地**: %s\n", logistics.Origin, logistics.Destination))
	if logistics.EstimatedAt != "" {
		sb.WriteString(fmt.Sprintf("- **预计到达**: %s\n", logistics.EstimatedAt))
	}
	sb.WriteString("\n### 物流轨迹\n\n")
	for _, evt := range logistics.Events {
		sb.WriteString(fmt.Sprintf("- **[%s]** %s — %s (%s)\n", evt.Time, evt.Status, evt.Desc, evt.Location))
	}
	return sb.String(), nil
}

// ====================== CS Tool: Refund ======================

type CSRefundTool struct {
	backend CSBackend
}

func NewCSRefundTool(backend CSBackend) *CSRefundTool {
	return &CSRefundTool{backend: backend}
}

func (t *CSRefundTool) Name() string { return "cs_order_refund" }
func (t *CSRefundTool) Description() string {
	return "处理订单退款。需要订单号和退款原因。退款后金额将在3-5个工作日原路返回。注意：已退款订单不可重复操作。"
}
func (t *CSRefundTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id": map[string]interface{}{"type": "string", "description": "要退款的订单号"},
			"reason":   map[string]interface{}{"type": "string", "description": "退款原因"},
		},
		"required": []string{"order_id", "reason"},
	}
}
func (t *CSRefundTool) IsReadOnly() bool { return false }
func (t *CSRefundTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	reason, _ := params["reason"].(string)
	if reason == "" {
		reason = "用户申请退款"
	}
	result, err := t.backend.RefundOrder(orderID, reason)
	if err != nil {
		return fmt.Sprintf("退款失败: %v", err), nil
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return fmt.Sprintf("退款处理结果:\n```json\n%s\n```", string(data)), nil
}

// ====================== CS Tool: Address Change ======================

type CSAddressChangeTool struct {
	backend CSBackend
}

func NewCSAddressChangeTool(backend CSBackend) *CSAddressChangeTool {
	return &CSAddressChangeTool{backend: backend}
}

func (t *CSAddressChangeTool) Name() string { return "cs_change_address" }
func (t *CSAddressChangeTool) Description() string {
	return "修改未发货订单的收货地址。已发货/已签收的订单无法修改地址。需要提供订单号和新地址。"
}
func (t *CSAddressChangeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id":    map[string]interface{}{"type": "string", "description": "订单号"},
			"new_address": map[string]interface{}{"type": "string", "description": "新收货地址（完整地址）"},
		},
		"required": []string{"order_id", "new_address"},
	}
}
func (t *CSAddressChangeTool) IsReadOnly() bool { return false }
func (t *CSAddressChangeTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	newAddr, _ := params["new_address"].(string)
	result, err := t.backend.ChangeAddress(orderID, newAddr)
	if err != nil {
		return fmt.Sprintf("地址修改失败: %v", err), nil
	}
	return result.Message, nil
}

// ====================== CS Tool: Cancel Order ======================

type CSCancelOrderTool struct {
	backend CSBackend
}

func NewCSCancelOrderTool(backend CSBackend) *CSCancelOrderTool {
	return &CSCancelOrderTool{backend: backend}
}

func (t *CSCancelOrderTool) Name() string { return "cs_cancel_order" }
func (t *CSCancelOrderTool) Description() string {
	return "取消待支付订单。仅状态为pending(待支付)的订单可取消。已支付/已发货/已完成的订单无法取消，需要走退款流程。"
}
func (t *CSCancelOrderTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id": map[string]interface{}{"type": "string", "description": "要取消的订单号"},
		},
		"required": []string{"order_id"},
	}
}
func (t *CSCancelOrderTool) IsReadOnly() bool { return false }
func (t *CSCancelOrderTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	result, err := t.backend.CancelOrder(orderID)
	if err != nil {
		return fmt.Sprintf("取消订单失败: %v", err), nil
	}
	return result.Message, nil
}

// ====================== CS Tool: Product Query ======================

type CSProductQueryTool struct {
	backend CSBackend
}

func NewCSProductQueryTool(backend CSBackend) *CSProductQueryTool {
	return &CSProductQueryTool{backend: backend}
}

func (t *CSProductQueryTool) Name() string { return "cs_product_query" }
func (t *CSProductQueryTool) Description() string {
	return "查询商品信息。可按商品名称、SKU、分类搜索。返回价格、库存、促销状态、商品描述等。"
}
func (t *CSProductQueryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "商品名称、SKU或分类关键词"},
		},
		"required": []string{"query"},
	}
}
func (t *CSProductQueryTool) IsReadOnly() bool { return true }
func (t *CSProductQueryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	products, err := t.backend.QueryProduct(query)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}
	if len(products) == 0 {
		return fmt.Sprintf("未找到与 '%s' 相关的商品。建议尝试其他关键词或转人工客服。", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 商品查询结果: %s\n\n", query))
	sb.WriteString("| 商品 | SKU | 价格 | 库存 | 分类 | 促销 |\n")
	sb.WriteString("|------|-----|------|------|------|------|\n")
	for _, p := range products {
		promo := "否"
		if p.OnPromotion {
			promo = "促销中"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | ¥%.2f | %d | %s | %s |\n",
			p.Name, p.SKU, p.Price, p.Stock, p.Category, promo))
	}
	sb.WriteString(fmt.Sprintf("\n共 %d 件商品\n", len(products)))
	return sb.String(), nil
}

// ====================== CS Tool: Submit Complaint ======================

type CSComplaintTool struct {
	backend CSBackend
}

func NewCSComplaintTool(backend CSBackend) *CSComplaintTool {
	return &CSComplaintTool{backend: backend}
}

func (t *CSComplaintTool) Name() string { return "cs_submit_complaint" }
func (t *CSComplaintTool) Description() string {
	return "提交投诉工单。当用户对订单/物流/服务不满意时使用。需要订单号、投诉原因和详细描述。工单创建后客服团队将在24小时内跟进。"
}
func (t *CSComplaintTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"order_id":    map[string]interface{}{"type": "string", "description": "相关订单号"},
			"reason":      map[string]interface{}{"type": "string", "description": "投诉原因（商品问题/物流问题/服务问题/其他）"},
			"description": map[string]interface{}{"type": "string", "description": "详细描述"},
		},
		"required": []string{"order_id", "reason", "description"},
	}
}
func (t *CSComplaintTool) IsReadOnly() bool { return false }
func (t *CSComplaintTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	orderID, _ := params["order_id"].(string)
	reason, _ := params["reason"].(string)
	description, _ := params["description"].(string)
	result, err := t.backend.SubmitComplaint(orderID, reason, description)
	if err != nil {
		return fmt.Sprintf("提交投诉失败: %v", err), nil
	}
	return result, nil
}

// ====================== Tool Registry Helper ======================

// RegisterCSTools registers all customer service business tools.
func RegisterCSTools(backend CSBackend) []toolcontract.Tool {
	return []toolcontract.Tool{
		NewCSOrderQueryTool(backend),
		NewCSLogisticsTool(backend),
		NewCSRefundTool(backend),
		NewCSAddressChangeTool(backend),
		NewCSCancelOrderTool(backend),
		NewCSProductQueryTool(backend),
		NewCSComplaintTool(backend),
	}
}

var globalCSBackend CSBackend

func GetCSBackend() CSBackend {
	if globalCSBackend == nil {
		globalCSBackend = NewMockCSBackend()
	}
	return globalCSBackend
}

func SetCSBackend(backend CSBackend) {
	globalCSBackend = backend
}
