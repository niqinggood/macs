package business

import (
	"crypto/md5"
	"fmt"
	"log"
	"strings"
	"time"
)

// ====================== Customer Identity ======================

// CustomerProfile holds the minimal identity info injected before dispatch.
// Everything else — orders, tickets, knowledge — the Agent queries via tools.
type CustomerProfile struct {
	CustomerID   string            `json:"customer_id"`
	Name         string            `json:"name"`
	Channel      string            `json:"channel"`
	ExternalID   string            `json:"external_id"`
	Level        string            `json:"level,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	FirstContact string            `json:"first_contact,omitempty"`
	LastContact  string            `json:"last_contact,omitempty"`
	TotalVisits  int               `json:"total_visits"`
}

// ====================== Universal CS Backend ======================

// CSBackend is the universal customer service data interface.
// It includes both the legacy ecommerce methods (for backward compat) and
// the new generic methods. New deployments implement the generic methods;
// the ecommerce methods have default implementations wrapping ExecuteAction.
type CSBackend interface {
	// —— Identity (minimal pre-load) ——
	ResolveCustomer(channel, externalID string) (*CustomerProfile, error)

	// —— Generic records (Agent queries as needed) ——
	GetRecords(customerID string, recordType string, limit int) ([]*BusinessRecord, error)

	// —— Knowledge search (Agent queries as needed) ——
	SearchKnowledge(query string, categories []string, topK int) ([]*KnowledgeResult, error)

	// —— Generic action execution ——
	ExecuteAction(req *ActionRequest) (*ActionResult, error)

	// —— Legacy ecommerce methods (required by existing tools) ——
	QueryOrder(orderID string) (*CSOrder, error)
	QueryLogistics(trackingNo string) (*CSLogistics, error)
	RefundOrder(orderID, reason string) (*CSRefundResult, error)
	CancelOrder(orderID string) (*CSRefundResult, error)
	ChangeAddress(orderID, newAddress string) (*CSRefundResult, error)
	QueryProduct(productName string) ([]CSProduct, error)
	SubmitComplaint(orderID, reason, description string) (string, error)
}

// ====================== Generic Data Types ======================

// BusinessRecord is a generic record from any business system.
type BusinessRecord struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Status    string            `json:"status"`
	Summary   string            `json:"summary"`
	Amount    float64           `json:"amount,omitempty"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
	Details   map[string]string `json:"details,omitempty"`
}

// ActionRequest is a generic business action.
type ActionRequest struct {
	Action     string            `json:"action"`
	TargetID   string            `json:"target_id"`
	Params     map[string]string `json:"params"`
	Reason     string            `json:"reason"`
	OperatorID string            `json:"operator_id"`
}

// ActionResult is the outcome of a business action.
type ActionResult struct {
	Success bool              `json:"success"`
	Message string            `json:"message"`
	RefID   string            `json:"ref_id,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// KnowledgeResult is a single RAG/FAQ search result.
type KnowledgeResult struct {
	Title    string  `json:"title"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
	Category string  `json:"category"`
	Source   string  `json:"source"`
}

// ====================== Identity Injector ======================

// IdentityInjector resolves customer identity and injects minimal context.
// This is the ONLY pre-loaded data. Everything else the Agent queries via tools.
type IdentityInjector struct {
	backend CSBackend
	logger  *log.Logger
}

func NewIdentityInjector(backend CSBackend, logger *log.Logger) *IdentityInjector {
	return &IdentityInjector{backend: backend, logger: logger}
}

// Inject resolves customer identity and returns the minimal context prefix.
// Returns: "customer_id=CUST001 | 张三 | 金牌会员 | 标签:数码爱好者 | 首次:2025-11"
func (ii *IdentityInjector) Inject(channel, externalID string) (string, *CustomerProfile, error) {
	profile, err := ii.backend.ResolveCustomer(channel, externalID)
	if err != nil {
		ii.logger.Printf("[Identity] Resolve error: %v", err)
		return "", nil, err
	}
	if profile == nil {
		return "", nil, nil
	}

	prefix := fmt.Sprintf("[客户身份] customer_id=%s", profile.CustomerID)
	if profile.Name != "" {
		prefix += fmt.Sprintf(" | 姓名=%s", profile.Name)
	}
	if profile.Level != "" {
		prefix += fmt.Sprintf(" | 等级=%s", profile.Level)
	}
	if len(profile.Tags) > 0 {
		prefix += fmt.Sprintf(" | 标签=%s", strings.Join(profile.Tags, ","))
	}
	if profile.FirstContact != "" {
		prefix += fmt.Sprintf(" | 首次联系=%s", profile.FirstContact)
	}
	prefix += fmt.Sprintf(" | 访问次数=%d", profile.TotalVisits)
	for k, v := range profile.Attributes {
		prefix += fmt.Sprintf(" | %s=%s", k, v)
	}
	if profile.Channel != "" {
		prefix += fmt.Sprintf(" | 渠道=%s:%s", profile.Channel, profile.ExternalID)
	}

	ii.logger.Printf("[Identity] %s", prefix)
	return prefix, profile, nil
}

// ====================== Mock Implementation ======================

// MockCSBackend implements the full CSBackend with realistic test data.
type MockCSBackend struct {
	*CSMockBackend
	customers map[string]*CustomerProfile
}

func NewMockCSBackend() *MockCSBackend {
	m := &MockCSBackend{
		CSMockBackend: NewCSMockBackend(),
		customers:     make(map[string]*CustomerProfile),
	}
	m.seedCustomers()
	return m
}

func (m *MockCSBackend) seedCustomers() {
	m.customers["wechat:wx_zhangsan"] = &CustomerProfile{
		CustomerID: "CUST001", Name: "张三", Channel: "wechat", ExternalID: "wx_zhangsan",
		Level: "金牌会员", Tags: []string{"数码爱好者", "回购率高"},
		Attributes:   map[string]string{"公司": "星辰科技", "职位": "技术VP"},
		FirstContact: "2025-11-15", LastContact: time.Now().Format("2006-01-02"), TotalVisits: 23,
	}
	m.customers["wechat:wx_lisi"] = &CustomerProfile{
		CustomerID: "CUST002", Name: "李四", Channel: "wechat", ExternalID: "wx_lisi",
		Level: "普通会员", Tags: []string{"价格敏感", "新客户"},
		FirstContact: "2026-06-22", LastContact: time.Now().Format("2006-01-02"), TotalVisits: 3,
	}
	m.customers["web:user_wangwu"] = &CustomerProfile{
		CustomerID: "CUST003", Name: "王五", Channel: "web", ExternalID: "user_wangwu",
		Level: "企业客户", Tags: []string{"大客户", "长期合作"},
		Attributes:   map[string]string{"公司": "恒达制造", "合同额": "200万+"},
		FirstContact: "2024-03-01", LastContact: time.Now().Format("2006-01-02"), TotalVisits: 156,
	}
}

// --- Universal methods ---

func (m *MockCSBackend) ResolveCustomer(channel, externalID string) (*CustomerProfile, error) {
	key := fmt.Sprintf("%s:%s", channel, externalID)
	if p, ok := m.customers[key]; ok {
		p.LastContact = time.Now().Format("2006-01-02")
		p.TotalVisits++
		return p, nil
	}
	profile := &CustomerProfile{
		CustomerID: fmt.Sprintf("CUST%03d", len(m.customers)+1),
		Channel:    channel, ExternalID: externalID,
		Name: externalID, Level: "新客户",
		FirstContact: time.Now().Format("2006-01-02"),
		LastContact:  time.Now().Format("2006-01-02"),
		TotalVisits:  1,
	}
	m.customers[key] = profile
	return profile, nil
}

func (m *MockCSBackend) GetRecords(customerID string, recordType string, limit int) ([]*BusinessRecord, error) {
	switch recordType {
	case "order":
		return m.getMockOrders(customerID, limit)
	case "ticket":
		return m.getMockTickets(customerID, limit)
	default:
		return nil, nil
	}
}

func (m *MockCSBackend) getMockOrders(customerID string, limit int) ([]*BusinessRecord, error) {
	// Map customers to sample order IDs from the embedded CSMockBackend
	custOrders := map[string][]string{
		"CUST001": {"ORD001"},
		"CUST002": {"ORD002"},
		"CUST003": {"ORD003"},
	}
	orderIDs := custOrders[customerID]
	var records []*BusinessRecord
	for _, oid := range orderIDs {
		order, err := m.QueryOrder(oid)
		if err != nil {
			continue
		}
		records = append(records, &BusinessRecord{
			ID: order.OrderID, Type: "order", Status: order.Status,
			Summary: order.ProductName, Amount: order.Amount,
			CreatedAt: order.CreatedAt, UpdatedAt: order.UpdatedAt,
			Details: map[string]string{"tracking_no": order.TrackingNo, "buyer": order.BuyerName, "address": order.Address},
		})
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (m *MockCSBackend) getMockTickets(customerID string, limit int) ([]*BusinessRecord, error) {
	return []*BusinessRecord{
		{ID: "TK001", Type: "ticket", Status: "resolved",
			Summary: "物流查询 — 已解决", CreatedAt: "2026-06-25", UpdatedAt: "2026-06-25"},
		{ID: "TK002", Type: "ticket", Status: "open",
			Summary: "蓝牙连接问题 — 处理中", CreatedAt: "2026-06-27", UpdatedAt: "2026-06-27"},
	}, nil
}

func (m *MockCSBackend) SearchKnowledge(query string, categories []string, topK int) ([]*KnowledgeResult, error) {
	all := []*KnowledgeResult{
		{Title: "退货政策", Content: "7天无理由退货，30天质量问题换货。退回运费由商家承担。", Score: 0.95, Category: "policy", Source: "help_center"},
		{Title: "蓝牙耳机使用指南", Content: "长按5秒配对，双击切歌。重置方法：同时按住音量+和电源键10秒。", Score: 0.88, Category: "product", Source: "product_manual"},
		{Title: "常见故障排查", Content: "无声：检查电量→重置蓝牙→检查设备音量。充电异常：清洁充电触点→更换充电线。", Score: 0.82, Category: "faq", Source: "troubleshooting"},
		{Title: "保修政策", Content: "非人为损坏保修1年。保修期内免费维修或换新。人为损坏按维修报价收费。", Score: 0.78, Category: "policy", Source: "warranty"},
	}
	var results []*KnowledgeResult
	for _, r := range all {
		inCategory := len(categories) == 0
		for _, c := range categories {
			if r.Category == c {
				inCategory = true
				break
			}
		}
		if inCategory {
			results = append(results, r)
		}
	}
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func (m *MockCSBackend) ExecuteAction(req *ActionRequest) (*ActionResult, error) {
	switch req.Action {
	case "refund":
		result, err := m.RefundOrder(req.TargetID, req.Reason)
		if err != nil {
			return &ActionResult{Success: false, Message: err.Error()}, nil
		}
		return &ActionResult{Success: true, Message: result.Message, RefID: result.RefundID}, nil
	case "cancel":
		result, err := m.CancelOrder(req.TargetID)
		if err != nil {
			return &ActionResult{Success: false, Message: err.Error()}, nil
		}
		return &ActionResult{Success: true, Message: result.Message, RefID: result.RefundID}, nil
	default:
		return &ActionResult{Success: false, Message: fmt.Sprintf("未知操作: %s", req.Action)}, nil
	}
}

// ====================== Persistent Session Binding ======================

// PersistentSessionID generates a deterministic session ID.
// Same channel+externalID always maps to the same session.
func PersistentSessionID(channel, externalID, tenantID string) string {
	input := fmt.Sprintf("%s:%s:%s", channel, externalID, tenantID)
	hash := md5.Sum([]byte(input))
	return fmt.Sprintf("s_%x", hash[:8])
}

// ====================== Global ======================

func GetUniversalBackend() CSBackend {
	return GetCSBackend()
}
