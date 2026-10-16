package business

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestRegisterAllCSToolsReturnsCompleteToolFamily(t *testing.T) {
	tools := RegisterAllCSTools(NewMockCSBackend())
	want := map[string]bool{
		"cs_order_query":      false,
		"cs_logistics_track":  false,
		"cs_order_refund":     false,
		"cs_change_address":   false,
		"cs_cancel_order":     false,
		"cs_product_query":    false,
		"cs_submit_complaint": false,
		"cs_satisfaction":     false,
		"cs_sla_check":        false,
		"cs_knowledge_gap":    false,
		"cs_checkin":          false,
	}
	for _, tool := range tools {
		if _, ok := want[tool.Name()]; ok {
			want[tool.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing registered tool %q", name)
		}
	}
}

func TestIdentityInjectorReturnsMinimalCustomerContext(t *testing.T) {
	injector := NewIdentityInjector(NewMockCSBackend(), log.New(io.Discard, "", 0))
	prefix, profile, err := injector.Inject("wechat", "wx_zhangsan")
	if err != nil {
		t.Fatal(err)
	}
	if profile == nil || profile.CustomerID != "CUST001" {
		t.Fatalf("profile = %#v", profile)
	}
	if !strings.Contains(prefix, "customer_id=CUST001") || !strings.Contains(prefix, "姓名=张三") {
		t.Fatalf("identity prefix = %q", prefix)
	}
}

func TestSchemaGeneratedQueryToolUsesConfiguredLimit(t *testing.T) {
	provider := NewMockBusinessProvider(EcommerceSchema())
	var queryTool interface {
		Execute(context.Context, map[string]interface{}) (string, error)
	}
	for _, tool := range GenerateTools(provider) {
		if tool.Name() == "query_order" {
			queryTool = tool
			break
		}
	}
	if queryTool == nil {
		t.Fatal("query_order tool was not generated")
	}
	result, err := queryTool.Execute(context.Background(), map[string]interface{}{"limit": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "共6条") {
		t.Fatalf("query output missing total count: %q", result)
	}
}
