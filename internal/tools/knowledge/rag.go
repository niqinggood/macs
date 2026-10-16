package knowledge

import (
	"context"
	"fmt"
	"strings"
)

type RAGSearcher func(ids []uint, query, sessionID, sourceDoc string, limit int) ([]RAGResult, error)
type TenantIdentityResolver func(context.Context) (tenantID string, scoped bool)
type KnowledgeBaseAuthorizer func(tenantID string, requested []uint) ([]uint, error)

// RAGSearchTool searches configured knowledge bases without depending on the
// root application package. Persistence and tenant identity are injected by
// the assembly layer.
type RAGSearchTool struct {
	kbIDs     []uint
	tenantID  string
	search    RAGSearcher
	identity  TenantIdentityResolver
	authorize KnowledgeBaseAuthorizer
}

func NewRAGSearchTool(kbIDs []uint, tenantID string, search RAGSearcher, identity TenantIdentityResolver, authorize KnowledgeBaseAuthorizer) *RAGSearchTool {
	return &RAGSearchTool{
		kbIDs: append([]uint(nil), kbIDs...), tenantID: tenantID,
		search: search, identity: identity, authorize: authorize,
	}
}

func (t *RAGSearchTool) Name() string { return "rag_search" }

func (t *RAGSearchTool) Description() string {
	return "Search the RAG knowledge base for domain-specific information. " +
		"Use this when the user asks about topics covered by configured knowledge bases. " +
		"Returns cited excerpts with source document names and relevance scores."
}

func (t *RAGSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type": "string", "description": "Search query in natural language. Be specific.",
			},
			"top_k": map[string]interface{}{
				"type": "integer", "description": "Number of results to return (default 5, max 10)",
				"minimum": 1, "maximum": 10,
			},
		},
		"required": []string{"query"},
	}
}

func (t *RAGSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", fmt.Errorf("rag_search is not configured")
	}
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("rag_search: query is required")
	}

	topK := 5
	switch value := params["top_k"].(type) {
	case float64:
		if value >= 1 && value <= 10 {
			topK = int(value)
		}
	case int:
		if value >= 1 && value <= 10 {
			topK = value
		}
	}

	kbIDs := append([]uint(nil), t.kbIDs...)
	tenantID := strings.TrimSpace(t.tenantID)
	if t.identity != nil {
		if resolved, scoped := t.identity(ctx); scoped {
			tenantID = strings.TrimSpace(resolved)
		}
	}
	if tenantID != "" && t.authorize == nil {
		return "", fmt.Errorf("rag_search: tenant authorization is unavailable")
	}
	if tenantID != "" {
		var err error
		kbIDs, err = t.authorize(tenantID, kbIDs)
		if err != nil {
			return "", fmt.Errorf("rag_search: %w", err)
		}
	}
	if len(kbIDs) == 0 {
		return "未配置知识库。请在 config.yaml 为该 agent 设置 rag_kb_ids。", nil
	}
	if t.search == nil {
		return "", fmt.Errorf("rag_search is not configured")
	}

	sessionID, _ := ctx.Value("session_id").(string)
	results, err := t.search(kbIDs, query, sessionID, "", topK)
	if err != nil {
		return "", fmt.Errorf("rag_search: %w", err)
	}
	if len(results) == 0 {
		return "知识库中未找到相关内容。", nil
	}
	return formatRAGResults(results), nil
}

func formatRAGResults(results []RAGResult) string {
	var output strings.Builder
	fmt.Fprintf(&output, "从知识库检索到 %d 条相关内容：\n\n", len(results))
	for index, result := range results {
		content := result.Content
		runes := []rune(content)
		if len(runes) > 500 {
			content = string(runes[:500]) + "..."
		}
		score := int(result.Score / 10 * 100)
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		fmt.Fprintf(&output, "--- 结果 #%d (相关度: %d%%) ---\n来源文档: %s\n%s\n\n", index+1, score, result.SourceDoc, content)
	}
	output.WriteString("请基于以上检索结果回答用户问题，并在回答中引用来源文档名称。")
	return output.String()
}
