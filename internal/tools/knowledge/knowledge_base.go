package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/textutil"
)

type KnowledgeResult struct {
	Title   string
	Content string
	Score   float64
}

type KnowledgeBaseDependencies struct {
	Identity        TenantIdentityResolver
	Authorize       KnowledgeBaseAuthorizer
	SearchChunks    func(kbIDs []uint, query string, limit int) ([]RAGResult, error)
	SearchKnowledge func(tenantID, query string, limit int) []KnowledgeResult
}

// KnowledgeBaseTool 知识库工具 — 支持外部RAG服务器 + 本地FTS5检索
type KnowledgeBaseTool struct {
	externalURL  string
	localDir     string
	httpClient   *http.Client
	dependencies KnowledgeBaseDependencies
}

func NewKnowledgeBaseTool(extURL, localDir string, dependencies KnowledgeBaseDependencies) *KnowledgeBaseTool {
	return &KnowledgeBaseTool{
		externalURL:  extURL,
		localDir:     localDir,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		dependencies: dependencies,
	}
}

func (t *KnowledgeBaseTool) Name() string { return "knowledge_base" }

func (t *KnowledgeBaseTool) Description() string {
	return "Search the knowledge base for relevant information. " +
		"Use this for FAQ lookup, document search, and retrieving stored knowledge. " +
		"Supports both external RAG server and local FTS5 search."
}

func (t *KnowledgeBaseTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query or question",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Search mode: 'auto' (try external first), 'external', 'local'",
				"enum":        []string{"auto", "external", "local"},
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (default 5)",
				"minimum":     1,
				"maximum":     20,
			},
		},
		"required": []string{"query"},
	}
}

func (t *KnowledgeBaseTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	mode, _ := params["mode"].(string)
	if mode == "" {
		mode = "auto"
	}
	topK := 5
	switch value := params["top_k"].(type) {
	case float64:
		if value >= 1 && value <= 20 {
			topK = int(value)
		}
	case int:
		if value >= 1 && value <= 20 {
			topK = value
		}
	}

	// Extract RAG config from context
	sessionID, _ := ctx.Value("session_id").(string)
	kbIDs, _ := ctx.Value("rag_kb_ids").([]uint)
	multiTurn, _ := ctx.Value("rag_multi_turn").(bool)
	tenantID, _ := ctx.Value("rag_tenant_id").(string)
	if t.dependencies.Identity != nil {
		if identity, scoped := t.dependencies.Identity(ctx); scoped {
			tenantID = identity
		}
	}

	// Resolve and validate the complete set. A mixed list whose first ID belongs
	// to this tenant must never authorize later IDs from another tenant.
	if strings.TrimSpace(tenantID) != "" {
		if t.dependencies.Authorize == nil {
			return "", fmt.Errorf("knowledge_base: tenant authorization is unavailable")
		}
		var err error
		kbIDs, err = t.dependencies.Authorize(tenantID, kbIDs)
		if err != nil {
			return "", fmt.Errorf("knowledge_base: %w", err)
		}
		if len(kbIDs) == 0 {
			return "未配置当前租户的知识库。", nil
		}
	}

	// Disable multi-turn if not configured
	if !multiTurn {
		sessionID = ""
	}

	switch mode {
	case "external":
		if strings.TrimSpace(tenantID) != "" {
			return "", fmt.Errorf("knowledge_base: external search is unavailable for tenant-scoped execution")
		}
		return t.searchExternal(query, topK)
	case "local":
		return t.searchLocal(query, topK, sessionID, kbIDs, tenantID)
	default: // auto
		if t.externalURL != "" && strings.TrimSpace(tenantID) == "" {
			result, err := t.searchExternal(query, topK)
			if err == nil && result != "" {
				return result, nil
			}
		}
		if t.localDir != "" || t.dependencies.SearchChunks != nil || t.dependencies.SearchKnowledge != nil {
			return t.searchLocal(query, topK, sessionID, kbIDs, tenantID)
		}
		return "知识库未配置。请在 config.yaml 设置 skills_config.rag.external_url 或 skills_config.rag.local_enabled。", nil
	}
}

func (t *KnowledgeBaseTool) searchExternal(query string, topK int) (string, error) {
	url := strings.TrimRight(t.externalURL, "/") + "/api/rag/search"
	body, _ := json.Marshal(map[string]interface{}{
		"query": query,
		"top_k": topK,
	})
	resp, err := t.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("RAG服务器连接失败: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("RAG服务器返回 %d: %s", resp.StatusCode, string(data))
	}
	var result struct {
		Results []struct {
			Content string  `json:"content"`
			Score   float64 `json:"score"`
			Title   string  `json:"title"`
		} `json:"results"`
	}
	json.Unmarshal(data, &result)
	if len(result.Results) == 0 {
		return "未找到相关知识。", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("从外部知识库找到 %d 条相关结果：\n\n", len(result.Results)))
	for i, r := range result.Results {
		title := r.Title
		if title == "" {
			title = fmt.Sprintf("结果 #%d", i+1)
		}
		sb.WriteString(fmt.Sprintf("### %s (相关度: %.0f%%)\n%s\n\n", title, r.Score*100, r.Content))
	}
	return sb.String(), nil
}

func (t *KnowledgeBaseTool) searchLocal(
	query string,
	topK int,
	sessionID string,
	kbIDs []uint,
	tenantID string,
) (string, error) {
	// Try RAG chunk search first (Bleve + gse BM25), using FTS5 as fallback.
	// Note: dedicated rag_search tool is the primary path for RAG queries.
	if t.dependencies.SearchChunks != nil {
		fts5Results, err := t.dependencies.SearchChunks(kbIDs, query, topK)
		if err == nil && len(fts5Results) > 0 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("从本地知识库找到 %d 条相关结果：\n\n", len(fts5Results)))
			for _, r := range fts5Results {
				sb.WriteString(fmt.Sprintf("- **%s** (相关度: %.0f%%): %s\n", r.SourceDoc, r.Score*10, textutil.Truncate(r.Content, 300)))
			}
			return sb.String(), nil
		}
	}

	// Fall back to FTS5 search on knowledges table
	if t.dependencies.SearchKnowledge == nil {
		return t.searchLocalFiles(query, topK)
	}
	results := t.dependencies.SearchKnowledge(tenantID, query, topK)
	if len(results) == 0 {
		return t.searchLocalFiles(query, topK)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("从本地知识库找到 %d 条相关结果：\n\n", len(results)))
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", r.Title, textutil.Truncate(r.Content, 300)))

	}
	return sb.String(), nil
}

func (t *KnowledgeBaseTool) searchLocalFiles(query string, topK int) (string, error) {
	if t.localDir == "" {
		return "本地知识库为空。上传文件到 knowledge/ 目录或使用 knowledge_upload 添加。", nil
	}
	entries, err := os.ReadDir(t.localDir)
	if err != nil {
		return "", fmt.Errorf("无法读取知识库目录: %w", err)
	}
	var matches []string
	kw := strings.ToLower(query)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(t.localDir, e.Name()))
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(strings.ToLower(content), kw) {
			matches = append(matches, fmt.Sprintf("**%s**: %s", e.Name(), textutil.Truncate(content, 300)))
		}
	}
	if len(matches) == 0 {
		return "未找到相关知识。", nil
	}
	if len(matches) > topK {
		matches = matches[:topK]
	}
	return strings.Join(matches, "\n\n"), nil
}
