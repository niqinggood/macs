package app

import (
	"context"
	"testing"
)

func TestNormalizeWebSearchEngineCorrectsModelChosenDuckDuckGoForChinese(t *testing.T) {
	ctx := withWebFetchRetrievalGoal(context.Background(), "调研三款中文企业客服产品")
	if got := normalizeWebSearchEngine(ctx, "duckduckgo", "智齿科技 客服AI 官方价格"); got != "sogou" {
		t.Fatalf("engine = %q, want sogou", got)
	}
	explicit := withWebFetchRetrievalGoal(context.Background(), "请明确使用 duckduckgo 搜索")
	if got := normalizeWebSearchEngine(explicit, "duckduckgo", "智齿科技"); got != "duckduckgo" {
		t.Fatalf("explicit user engine was overridden: %q", got)
	}
}
