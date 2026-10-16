package app

import "testing"

func TestPresentationCreationDefersWhenUserRequestsTradeoffsOnly(t *testing.T) {
	request := "我要一个 10 页 PPT。请先判断约束是否能同时满足，只给我两个取舍方案，不要开始制作。"
	if isPresentationCreationRequest(request) {
		t.Fatal("tradeoff-only turn was misclassified as presentation production")
	}
	state := newPresentationProductionState(nil, request)
	if state.Expected {
		t.Fatalf("presentation delivery gate remained active: %#v", state)
	}
}

func TestPresentationCreationActivatesOnLaterExplicitStart(t *testing.T) {
	request := "选择推荐方案，现在可以开始，请生成 PPT、Excel 和验收说明。"
	if !isPresentationCreationRequest(request) {
		t.Fatal("explicit follow-up did not activate presentation production")
	}
	state := newPresentationProductionState(nil, request)
	if !state.Expected {
		t.Fatalf("presentation delivery gate was not activated: %#v", state)
	}
}

func TestPresentationCreationAllowsAuthorizedAlternativeAfterNegation(t *testing.T) {
	if !isPresentationCreationRequest("不要生成旧版 PPT，请生成一份新版 PPT") {
		t.Fatal("one rejected alternative suppressed an explicitly authorized deck")
	}
}
