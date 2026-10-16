package network

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/toolcontract"
)

// WebSearchTool is a compatibility wrapper around web_fetch search mode.
// Many agents and pipelines learned to call web_search; web_fetch is the
// concrete implementation that already supports engine+query searching.
type WebSearchTool struct {
	fetch toolcontract.Tool
}

func NewWebSearchTool(fetch toolcontract.Tool) *WebSearchTool {
	return &WebSearchTool{fetch: fetch}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web by query. Delegates to web_fetch engine+query mode. Engine is optional: current news uses bing_news, Chinese queries use sogou, and English/technical queries use duckduckgo unless explicitly overridden."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query.",
			},
			"q": map[string]interface{}{
				"type":        "string",
				"description": "Alias for query.",
			},
			"engine": map[string]interface{}{
				"type":        "string",
				"description": "Search engine. Use bing_news for current-news discovery, sogou for other Chinese sources, and duckduckgo/bing for English or technical sources.",
				"enum":        []string{"bing_news", "sogou", "360", "bing", "baidu", "google", "duckduckgo", "toutiao", "wechat", "yahoo", "brave", "ecosia"},
			},
			"maxResults": map[string]interface{}{
				"type":        "integer",
				"description": "Max search results (default: 10, max: 20).",
				"minimum":     1,
				"maximum":     20,
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Alias for maxResults.",
				"minimum":     1,
				"maximum":     20,
			},
		},
		"required": []string{},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.fetch == nil {
		return "", fmt.Errorf("web_fetch delegate is not configured")
	}

	query := stringParam(params, "query")
	if query == "" {
		query = stringParam(params, "q")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	engine := strings.TrimSpace(stringParam(params, "engine"))

	delegated := map[string]interface{}{
		"query": query,
	}
	if engine != "" {
		delegated["engine"] = engine
	}
	if v, ok := params["maxResults"]; ok {
		delegated["maxResults"] = v
	} else if v, ok := params["max_results"]; ok {
		delegated["maxResults"] = v
	}

	return t.fetch.Execute(ctx, delegated)
}

func (t *WebSearchTool) MaxResultSize() int {
	if t.fetch != nil {
		if limited, ok := t.fetch.(toolcontract.SizeLimitedTool); ok {
			return limited.MaxResultSize()
		}
	}
	return 100000
}

func stringParam(params map[string]interface{}, key string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
