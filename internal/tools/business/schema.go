package business

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"mac/internal/configvalue"
	"mac/internal/toolcontract"
)

// ====================== Business Schema ======================

// BusinessSchema declares what data and actions a business has.
// One schema serves ALL agent tools — no more hardcoded tool names.
//
// Example for ecommerce:
//
//	RecordTypes: [orders, tickets]
//	Actions: [refund, cancel, change_address, submit_complaint]
//
// Example for banking:
//
//	RecordTypes: [transactions, loans, cards]
//	Actions: [transfer, freeze_card, dispute_charge]
type BusinessSchema struct {
	Name        string          `json:"name"` // "ecommerce"/"banking"/"saas"/"logistics"
	RecordTypes []RecordTypeDef `json:"record_types"`
	Actions     []ActionDef     `json:"actions"`
}

// RecordTypeDef declares one type of business record.
type RecordTypeDef struct {
	Name        string     `json:"name"`        // "订单" / "交易" / "订阅"
	Key         string     `json:"key"`         // "order" / "transaction" / "subscription"
	Description string     `json:"description"` // "客户的购买订单记录"
	Fields      []FieldDef `json:"fields"`      // What fields to show
	SearchBy    []string   `json:"search_by"`   // Which fields can be queried: ["customer_id", "order_id", "status"]
}

// FieldDef declares one field in a record.
type FieldDef struct {
	Name string `json:"name"` // "订单号" / "金额" / "状态"
	Key  string `json:"key"`  // "order_id" / "amount" / "status"
	Type string `json:"type"` // "string" / "number" / "date"
}

// ActionDef declares one executable business action.
type ActionDef struct {
	Name        string     `json:"name"`        // "退款" / "取消订单" / "冻结卡片"
	Key         string     `json:"key"`         // "refund" / "cancel" / "freeze_card"
	Description string     `json:"description"` // What this action does
	TargetType  string     `json:"target_type"` // Which record type this acts on: "order" / "transaction"
	Params      []ParamDef `json:"params"`      // Required parameters
	Confirm     bool       `json:"confirm"`     // True if confirmation needed before executing
}

// ParamDef declares a parameter for an action.
type ParamDef struct {
	Name        string `json:"name"` // "订单号" / "退款原因"
	Key         string `json:"key"`  // "order_id" / "reason"
	Type        string `json:"type"` // "string" / "number"
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// ====================== Generic Backend ======================

// GenericRecord is a single record from any business system.
// Fields are dynamic — the business schema defines the shape.
type GenericRecord map[string]interface{}

// GenericResult is the output of a query or action.
type GenericResult struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    []GenericRecord `json:"data,omitempty"`
	RefID   string          `json:"ref_id,omitempty"`
	Total   int             `json:"total,omitempty"`
}

// BusinessDataProvider is the SINGLE interface any business needs to implement.
// No more per-tool hardcoding. The schema declares what's available,
// this interface provides the data.
type BusinessDataProvider interface {
	// Query records by type and filters. The schema's RecordTypes define valid types.
	Query(recordType string, filters map[string]string, limit int) (*GenericResult, error)

	// Execute a business action. The schema's Actions define valid actions.
	Execute(action string, targetID string, params map[string]string) (*GenericResult, error)

	// GetSchema returns the business schema that drives tool generation.
	GetSchema() *BusinessSchema
}

// ====================== Auto-Generated Tools ======================

// recordQueryTool is a dynamically generated tool for querying one record type.
type recordQueryTool struct {
	recDef   RecordTypeDef
	provider BusinessDataProvider
}

func newRecordQueryTool(recDef RecordTypeDef, provider BusinessDataProvider) *recordQueryTool {
	return &recordQueryTool{recDef: recDef, provider: provider}
}

func (t *recordQueryTool) Name() string {
	return fmt.Sprintf("query_%s", t.recDef.Key)
}

func (t *recordQueryTool) Description() string {
	return fmt.Sprintf("查询%s。%s。可按%s查询。",
		t.recDef.Name, t.recDef.Description,
		strings.Join(t.recDef.SearchBy, "/"))
}

func (t *recordQueryTool) Parameters() map[string]interface{} {
	props := map[string]interface{}{}
	for _, f := range t.recDef.SearchBy {
		props[f] = map[string]interface{}{"type": "string", "description": fmt.Sprintf("按%s筛选", f)}
	}
	props["limit"] = map[string]interface{}{"type": "integer", "description": "返回条数，默认5"}
	return map[string]interface{}{
		"type":       "object",
		"properties": props,
		"required":   []string{},
	}
}

func (t *recordQueryTool) IsReadOnly() bool { return true }

func (t *recordQueryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filters := map[string]string{}
	for _, f := range t.recDef.SearchBy {
		if v, ok := params[f].(string); ok && v != "" {
			filters[f] = v
		}
	}
	limit := configvalue.Int(params, "limit", 5)
	if limit <= 0 {
		limit = 5
	}

	result, err := t.provider.Query(t.recDef.Key, filters, limit)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}
	if !result.Success {
		return result.Message, nil
	}
	if len(result.Data) == 0 {
		return fmt.Sprintf("未找到%s记录", t.recDef.Name), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## %s (共%d条)\n\n", t.recDef.Name, result.Total)
	fmt.Fprintf(&sb, "| %s |\n", strings.Join(t.fieldHeaders(), " | "))
	fmt.Fprintf(&sb, "| %s |\n", strings.Repeat("------|", len(t.recDef.Fields)))
	for _, r := range result.Data {
		sb.WriteString("|")
		for _, f := range t.recDef.Fields {
			fmt.Fprintf(&sb, " %v |", r[f.Key])
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func (t *recordQueryTool) fieldHeaders() []string {
	var h []string
	for _, f := range t.recDef.Fields {
		h = append(h, f.Name)
	}
	return h
}

// actionTool is a dynamically generated tool for executing one business action.
type actionTool struct {
	actDef   ActionDef
	provider BusinessDataProvider
}

func newActionTool(actDef ActionDef, provider BusinessDataProvider) *actionTool {
	return &actionTool{actDef: actDef, provider: provider}
}

func (t *actionTool) Name() string {
	return fmt.Sprintf("do_%s", t.actDef.Key)
}

func (t *actionTool) Description() string {
	confirm := ""
	if t.actDef.Confirm {
		confirm = "此操作需要确认，请仔细核对信息。"
	}
	return fmt.Sprintf("%s。%s%s", t.actDef.Description, t.actDef.Name, confirm)
}

func (t *actionTool) Parameters() map[string]interface{} {
	props := map[string]interface{}{
		"target_id": map[string]interface{}{"type": "string", "description": fmt.Sprintf("要操作的%sID", t.actDef.TargetType)},
	}
	for _, p := range t.actDef.Params {
		props[p.Key] = map[string]interface{}{"type": p.Type, "description": p.Description}
	}
	required := []string{"target_id"}
	for _, p := range t.actDef.Params {
		if p.Required {
			required = append(required, p.Key)
		}
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func (t *actionTool) IsReadOnly() bool { return false }

func (t *actionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	targetID, _ := params["target_id"].(string)
	actParams := map[string]string{}
	for _, p := range t.actDef.Params {
		if v, ok := params[p.Key].(string); ok && v != "" {
			actParams[p.Key] = v
		}
	}

	result, err := t.provider.Execute(t.actDef.Key, targetID, actParams)
	if err != nil {
		return fmt.Sprintf("执行失败: %v", err), nil
	}

	status := "成功"
	if !result.Success {
		status = "失败"
	}
	return fmt.Sprintf("%s: %s\n%s\n操作编号: %s",
		t.actDef.Name, status, result.Message, result.RefID), nil
}

// ====================== Tool Generator ======================

// GenerateTools creates all query + action tools from a business schema.
func GenerateTools(provider BusinessDataProvider) []toolcontract.Tool {
	schema := provider.GetSchema()
	var tools []toolcontract.Tool

	// One query tool per record type
	for _, rec := range schema.RecordTypes {
		tools = append(tools, newRecordQueryTool(rec, provider))
	}

	// One action tool per action
	for _, act := range schema.Actions {
		tools = append(tools, newActionTool(act, provider))
	}

	return tools
}

// ====================== Pre-built Schemas ======================

// EcommerceSchema is the standard schema for ecommerce customer service.
func EcommerceSchema() *BusinessSchema {
	return &BusinessSchema{
		Name: "ecommerce",
		RecordTypes: []RecordTypeDef{
			{
				Name: "订单", Key: "order", Description: "客户的购买订单记录",
				Fields: []FieldDef{
					{Name: "订单号", Key: "order_id", Type: "string"},
					{Name: "商品", Key: "product", Type: "string"},
					{Name: "金额", Key: "amount", Type: "number"},
					{Name: "状态", Key: "status", Type: "string"},
					{Name: "时间", Key: "created_at", Type: "date"},
				},
				SearchBy: []string{"customer_id", "order_id", "status"},
			},
			{
				Name: "工单", Key: "ticket", Description: "客户的客服工单记录",
				Fields: []FieldDef{
					{Name: "工单号", Key: "ticket_id", Type: "string"},
					{Name: "类型", Key: "type", Type: "string"},
					{Name: "状态", Key: "status", Type: "string"},
					{Name: "时间", Key: "created_at", Type: "date"},
				},
				SearchBy: []string{"customer_id", "ticket_id", "status"},
			},
		},
		Actions: []ActionDef{
			{
				Name: "退款", Key: "refund", Description: "为订单办理退款",
				TargetType: "order",
				Params:     []ParamDef{{Name: "退款原因", Key: "reason", Type: "string", Required: true, Description: "退款原因"}},
				Confirm:    true,
			},
			{
				Name: "取消订单", Key: "cancel", Description: "取消待支付订单",
				TargetType: "order",
				Params:     []ParamDef{},
				Confirm:    true,
			},
			{
				Name: "修改地址", Key: "change_address", Description: "修改未发货订单的收货地址",
				TargetType: "order",
				Params:     []ParamDef{{Name: "新地址", Key: "new_address", Type: "string", Required: true, Description: "新的收货地址"}},
				Confirm:    false,
			},
			{
				Name: "提交投诉", Key: "submit_complaint", Description: "为客户提交正式投诉工单",
				TargetType: "ticket",
				Params: []ParamDef{
					{Name: "投诉原因", Key: "reason", Type: "string", Required: true, Description: "投诉原因分类"},
					{Name: "详细描述", Key: "description", Type: "string", Required: true, Description: "详细描述投诉内容"},
				},
				Confirm: false,
			},
		},
	}
}

// BankingSchema is the schema for banking/finance customer service.
func BankingSchema() *BusinessSchema {
	return &BusinessSchema{
		Name: "banking",
		RecordTypes: []RecordTypeDef{
			{
				Name: "交易记录", Key: "transaction", Description: "账户交易流水",
				Fields: []FieldDef{
					{Name: "交易号", Key: "txn_id", Type: "string"},
					{Name: "类型", Key: "type", Type: "string"},
					{Name: "金额", Key: "amount", Type: "number"},
					{Name: "余额", Key: "balance", Type: "number"},
					{Name: "时间", Key: "created_at", Type: "date"},
				},
				SearchBy: []string{"customer_id", "txn_id", "type"},
			},
			{
				Name: "理财产品", Key: "holding", Description: "客户持有的理财产品",
				Fields: []FieldDef{
					{Name: "产品名称", Key: "product_name", Type: "string"},
					{Name: "持有金额", Key: "amount", Type: "number"},
					{Name: "收益率", Key: "yield", Type: "string"},
					{Name: "到期日", Key: "maturity", Type: "date"},
				},
				SearchBy: []string{"customer_id", "product_name"},
			},
		},
		Actions: []ActionDef{
			{
				Name: "冻结卡片", Key: "freeze_card", Description: "为客户挂失/冻结银行卡",
				TargetType: "transaction",
				Params:     []ParamDef{{Name: "冻结原因", Key: "reason", Type: "string", Required: true, Description: "挂失/盗刷/其他"}},
				Confirm:    true,
			},
			{
				Name: "交易争议", Key: "dispute", Description: "对可疑交易发起争议处理",
				TargetType: "transaction",
				Params:     []ParamDef{{Name: "争议原因", Key: "reason", Type: "string", Required: true, Description: "未授权/金额错误/重复扣款"}},
				Confirm:    true,
			},
		},
	}
}

// SaaSSchema is the schema for SaaS customer success.
func SaaSSchema() *BusinessSchema {
	return &BusinessSchema{
		Name: "saas",
		RecordTypes: []RecordTypeDef{
			{
				Name: "订阅", Key: "subscription", Description: "客户的订阅计划和用量",
				Fields: []FieldDef{
					{Name: "计划", Key: "plan", Type: "string"},
					{Name: "状态", Key: "status", Type: "string"},
					{Name: "月费", Key: "monthly_fee", Type: "number"},
					{Name: "坐席数", Key: "seats", Type: "number"},
					{Name: "到期日", Key: "renewal_date", Type: "date"},
				},
				SearchBy: []string{"customer_id", "plan", "status"},
			},
		},
		Actions: []ActionDef{
			{
				Name: "升级计划", Key: "upgrade", Description: "为客户升级订阅计划",
				TargetType: "subscription",
				Params:     []ParamDef{{Name: "目标计划", Key: "target_plan", Type: "string", Required: true, Description: "要升级到的计划名称"}},
				Confirm:    true,
			},
			{
				Name: "暂停订阅", Key: "suspend", Description: "暂停客户订阅",
				TargetType: "subscription",
				Params:     []ParamDef{{Name: "暂停原因", Key: "reason", Type: "string", Required: true, Description: "暂停原因"}},
				Confirm:    true,
			},
		},
	}
}

// ====================== Schema-aware Mock Provider ======================

// MockBusinessProvider implements BusinessDataProvider with schema-aware mock data.
type MockBusinessProvider struct {
	schema   *BusinessSchema
	csMock   *CSMockBackend
	custMock map[string][]GenericRecord // customerID -> records
	mu       sync.RWMutex
}

func NewMockBusinessProvider(schema *BusinessSchema) *MockBusinessProvider {
	m := &MockBusinessProvider{
		schema:   schema,
		csMock:   NewCSMockBackend(),
		custMock: make(map[string][]GenericRecord),
	}
	m.seedEcommerceData()
	return m
}

func (m *MockBusinessProvider) seedEcommerceData() {
	m.custMock["CUST001"] = []GenericRecord{
		{"order_id": "ORD001", "product": "无线蓝牙耳机 Pro", "amount": 299.00, "status": "shipped", "created_at": "2026-06-22", "customer_id": "CUST001"},
		{"order_id": "ORD005", "product": "手机壳 透明", "amount": 29.90, "status": "delivered", "created_at": "2026-05-15", "customer_id": "CUST001"},
	}
	m.custMock["CUST002"] = []GenericRecord{
		{"order_id": "ORD002", "product": "智能手表 S3", "amount": 1599.00, "status": "pending", "created_at": "2026-06-27", "customer_id": "CUST002"},
	}
	m.custMock["CUST003"] = []GenericRecord{
		{"order_id": "ORD003", "product": "USB-C 数据线 2米", "amount": 49.90, "status": "delivered", "created_at": "2026-06-20", "customer_id": "CUST003"},
		{"order_id": "ORD006", "product": "无线蓝牙耳机 Pro", "amount": 299.00, "status": "delivered", "created_at": "2026-04-10", "customer_id": "CUST003"},
		{"order_id": "ORD007", "product": "智能手表 S3", "amount": 1599.00, "status": "delivered", "created_at": "2026-03-05", "customer_id": "CUST003"},
	}
}

func (m *MockBusinessProvider) Query(recordType string, filters map[string]string, limit int) (*GenericResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []GenericRecord

	switch recordType {
	case "order":
		custID := filters["customer_id"]
		orderID := filters["order_id"]
		status := filters["status"]

		for _, recs := range m.custMock {
			for _, r := range recs {
				if custID != "" && r["customer_id"] != custID {
					continue
				}
				if orderID != "" && r["order_id"] != orderID {
					continue
				}
				if status != "" && r["status"] != status {
					continue
				}
				results = append(results, r)
			}
		}

	case "ticket":
		results = []GenericRecord{
			{"ticket_id": "TK001", "type": "物流查询", "status": "已解决", "created_at": "2026-06-25", "customer_id": "CUST001"},
			{"ticket_id": "TK002", "type": "技术问题", "status": "处理中", "created_at": "2026-06-27", "customer_id": "CUST001"},
		}
	}

	total := len(results)
	if total > limit {
		results = results[:limit]
	}
	if results == nil {
		results = []GenericRecord{}
	}

	return &GenericResult{Success: true, Data: results, Total: total}, nil
}

func (m *MockBusinessProvider) Execute(action string, targetID string, params map[string]string) (*GenericResult, error) {
	switch action {
	case "refund":
		result, err := m.csMock.RefundOrder(targetID, params["reason"])
		if err != nil {
			return &GenericResult{Success: false, Message: err.Error()}, nil
		}
		return &GenericResult{Success: true, Message: result.Message, RefID: result.RefundID}, nil

	case "cancel":
		result, err := m.csMock.CancelOrder(targetID)
		if err != nil {
			return &GenericResult{Success: false, Message: err.Error()}, nil
		}
		return &GenericResult{Success: true, Message: result.Message, RefID: result.RefundID}, nil

	case "change_address":
		result, err := m.csMock.ChangeAddress(targetID, params["new_address"])
		if err != nil {
			return &GenericResult{Success: false, Message: err.Error()}, nil
		}
		return &GenericResult{Success: true, Message: result.Message}, nil

	case "submit_complaint":
		msg, err := m.csMock.SubmitComplaint(targetID, params["reason"], params["description"])
		if err != nil {
			return &GenericResult{Success: false, Message: err.Error()}, nil
		}
		return &GenericResult{Success: true, Message: msg}, nil
	}

	return &GenericResult{Success: false, Message: fmt.Sprintf("未知操作: %s", action)}, nil
}

func (m *MockBusinessProvider) GetSchema() *BusinessSchema {
	return m.schema
}

// ====================== Global Registry ======================

var (
	globalBusinessProvider BusinessDataProvider
	globalBusinessSchema   *BusinessSchema
	globalSchemaMu         sync.RWMutex
)

// InitBusinessSystem sets up the business schema + provider and returns generated tools.
// Call this once during gateway initialization.
// To switch business type, call again with a different schema + provider.
func InitBusinessSystem(provider BusinessDataProvider) []toolcontract.Tool {
	globalSchemaMu.Lock()
	defer globalSchemaMu.Unlock()

	globalBusinessProvider = provider
	globalBusinessSchema = provider.GetSchema()

	tools := GenerateTools(provider)

	// Also register the schema inspection tool
	tools = append(tools, &schemaInfoTool{provider: provider})

	return tools
}

// GetBusinessProvider returns the current provider for context assembly.
func GetBusinessProvider() BusinessDataProvider {
	globalSchemaMu.RLock()
	defer globalSchemaMu.RUnlock()
	return globalBusinessProvider
}

// schemaInfoTool lets agents inspect what data and actions are available.
type schemaInfoTool struct {
	provider BusinessDataProvider
}

func (t *schemaInfoTool) Name() string { return "business_info" }
func (t *schemaInfoTool) Description() string {
	return "查看当前业务系统的数据模型和可用操作。返回: 可查询的记录类型、可执行的操作、及各自的参数。"
}
func (t *schemaInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *schemaInfoTool) IsReadOnly() bool { return true }
func (t *schemaInfoTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	schema := t.provider.GetSchema()
	data, _ := json.MarshalIndent(schema, "", "  ")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 业务系统: %s\n\n", schema.Name))

	sb.WriteString("## 可查询数据\n")
	for _, r := range schema.RecordTypes {
		fmt.Fprintf(&sb, "- **query_%s**: 查询%s (%s)\n", r.Key, r.Name, r.Description)
	}
	sb.WriteString("\n## 可执行操作\n")
	for _, a := range schema.Actions {
		fmt.Fprintf(&sb, "- **do_%s**: %s (作用于%s)\n", a.Key, a.Name, a.TargetType)
	}
	sb.WriteString(fmt.Sprintf("\n```json\n%s\n```", string(data)))
	return sb.String(), nil
}
