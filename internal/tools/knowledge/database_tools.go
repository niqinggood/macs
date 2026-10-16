package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/ragmodel"
	"mac/internal/textutil"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type TenantIDResolver func(context.Context) string

type memoryEventRecord struct {
	UserQuestion string
	UserName     string
	FinalAnswer  string
	CallChain    datatypes.JSON
	UserRating   *int
	CreatedAt    time.Time
}

func (memoryEventRecord) TableName() string { return "memory_events" }

type toolSkillStatRecord struct {
	SuccessCount  int
	FailCount     int
	TotalCount    int
	AvgDurationMs int64
	SuccessRate   float64
	LastUsedAt    time.Time
}

func (toolSkillStatRecord) TableName() string { return "tool_skill_stats" }

type knowledgeRecord struct {
	ID           uint
	TenantID     string
	AgentName    string
	KnowledgeID  string
	Title        string
	Summary      string
	Content      string
	Category     string
	Tags         datatypes.JSON
	SourceType   string
	SourceID     string
	IsVectorized bool
	VectorID     string
	RAGStatus    string
	Importance   float64
	UseCount     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt
}

func (knowledgeRecord) TableName() string { return "knowledges" }

func resolveTenantID(ctx context.Context, resolver TenantIDResolver) string {
	if resolver == nil {
		return "default"
	}
	if tenantID := strings.TrimSpace(resolver(ctx)); tenantID != "" {
		return tenantID
	}
	return "default"
}

// ==================== 1. 搜索历史任务工具 ====================

type SearchHistoryTool struct {
	db       *gorm.DB
	tenantID TenantIDResolver
}

func NewSearchHistoryTool(db *gorm.DB, tenantID TenantIDResolver) *SearchHistoryTool {
	return &SearchHistoryTool{db: db, tenantID: tenantID}
}

func (t *SearchHistoryTool) Name() string {
	return "search_history"
}

func (t *SearchHistoryTool) Description() string {
	return "搜索历史任务记录，查找类似任务的解决方案和经验，可以作为当前任务的参考"
}

func (t *SearchHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "要搜索的任务描述或关键词，比如'部署Python应用'、'读取配置文件'",
			},
			"user_name": map[string]interface{}{
				"type":        "string",
				"description": "指定用户名，留空则搜索所有用户的历史",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "搜索最近N天的记录，默认0表示不限",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "返回几条最相关的历史任务，默认3",
				"minimum":     1,
				"maximum":     5,
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchHistoryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return "请提供搜索关键词", nil
	}

	userName, _ := params["user_name"].(string)
	days, _ := params["days"].(float64)
	limit := 3
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	// 提取关键词
	keywords := extractKeywords(query)

	// 搜索历史成功任务
	var events []memoryEventRecord
	dbQuery := t.db.Model(&memoryEventRecord{}).
		Where("tenant_id = ? AND status = ?", resolveTenantID(ctx, t.tenantID), "success").
		Order("created_at DESC")

	if userName != "" {
		dbQuery = dbQuery.Where("user_name = ?", userName)
	}

	// 按天数过滤
	if days > 0 {
		since := time.Now().AddDate(0, 0, -int(days))
		dbQuery = dbQuery.Where("created_at >= ?", since)
	}

	// 关键词匹配
	if len(keywords) > 0 {
		keywords = keywords[:min(3, len(keywords))]
		keywordQuery := t.db.Where("user_question LIKE ?", "%"+keywords[0]+"%")
		for _, kw := range keywords[1:] {
			keywordQuery = keywordQuery.Or("user_question LIKE ?", "%"+kw+"%")
		}
		dbQuery = dbQuery.Where(keywordQuery)
	} else {
		dbQuery = dbQuery.Where("user_question LIKE ?", "%"+query+"%")
	}

	err := dbQuery.Limit(limit).Find(&events).Error
	if err != nil {
		return "", fmt.Errorf("搜索历史失败: %w", err)
	}

	if len(events) == 0 {
		return fmt.Sprintf("未找到与「%s」相关的历史任务", query), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("## 📋 相关历史任务\n\n"))
	result.WriteString(fmt.Sprintf("搜索: %s\n", query))
	if days > 0 {
		result.WriteString(fmt.Sprintf("时间范围: 最近 %d 天\n\n", int(days)))
	} else {
		result.WriteString("\n")
	}

	for i, event := range events {
		result.WriteString(fmt.Sprintf("### %d. %s\n", i+1, event.UserQuestion))
		result.WriteString(fmt.Sprintf("- 时间: %s\n", event.CreatedAt.Format("2006-01-02 15:04")))
		result.WriteString(fmt.Sprintf("- 用户: %s\n", event.UserName))

		// 提取使用的工具链
		if event.CallChain != nil {
			var chain []map[string]interface{}
			if json.Unmarshal(event.CallChain, &chain) == nil && len(chain) > 0 {
				tools := make([]string, 0)
				for _, step := range chain {
					if name, ok := step["name"].(string); ok {
						tools = append(tools, name)
					}
				}
				if len(tools) > 0 {
					result.WriteString(fmt.Sprintf("- 使用工具: %s\n", strings.Join(tools, " → ")))
				}
			}
		}

		// 提取解决方案
		if event.FinalAnswer != "" {
			solution := extractSolutionFromAnswer(event.FinalAnswer)
			result.WriteString(fmt.Sprintf("- 解决方案: %s\n", solution))
		}

		// 如果有反馈
		if event.UserRating != nil && *event.UserRating >= 4 {
			result.WriteString("- 用户反馈: ✅ 满意\n")
		}

		result.WriteString("\n")
	}

	return result.String(), nil
}

// ==================== 2. 查看工具/技能统计 ====================

type GetStatsTool struct {
	db       *gorm.DB
	tenantID TenantIDResolver
}

func NewGetStatsTool(db *gorm.DB, tenantID TenantIDResolver) *GetStatsTool {
	return &GetStatsTool{db: db, tenantID: tenantID}
}

func (t *GetStatsTool) Name() string {
	return "get_stats"
}

func (t *GetStatsTool) Description() string {
	return "查看工具或技能的使用统计，包括成功率、使用次数、平均耗时，帮助判断是否使用某个工具"
}

func (t *GetStatsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "工具或技能名称，如'list_dir'、'web_search'",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "类型：tool 或 skill，默认tool",
				"enum":        []string{"tool", "skill"},
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetStatsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return "请提供工具或技能名称", nil
	}

	typ, _ := params["type"].(string)
	if typ == "" {
		typ = "tool"
	}

	var stat toolSkillStatRecord
	err := t.db.Where("tenant_id = ? AND name = ? AND type = ?",
		resolveTenantID(ctx, t.tenantID), name, typ).First(&stat).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Sprintf("未找到 %s「%s」的统计信息", typ, name), nil
		}
		return "", fmt.Errorf("查询失败: %w", err)
	}

	var result strings.Builder
	icon := map[string]string{"tool": "🔧", "skill": "⚡"}
	result.WriteString(fmt.Sprintf("## %s %s 统计\n\n", icon[typ], name))
	result.WriteString(fmt.Sprintf("- 使用次数: %d\n", stat.TotalCount))
	result.WriteString(fmt.Sprintf("- 成功率: %.1f%%\n", stat.SuccessRate))
	result.WriteString(fmt.Sprintf("- 平均耗时: %d ms\n", stat.AvgDurationMs))
	result.WriteString(fmt.Sprintf("- 最后使用: %s\n", stat.LastUsedAt.Format("01-02 15:04")))

	// 使用建议
	if stat.SuccessRate >= 80 {
		result.WriteString("\n✅ 建议: 成功率较高，可以放心使用")
	} else if stat.SuccessRate >= 50 {
		result.WriteString("\n⚠️ 建议: 成功率一般，使用时需注意")
	} else if stat.SuccessRate > 0 {
		result.WriteString("\n❌ 建议: 成功率较低，建议检查使用方式或找替代方案")
	}

	return result.String(), nil
}

// ==================== 3. 知识管理工具 ====================

type KnowledgeTool struct {
	db        *gorm.DB
	agentName string
	tenantID  TenantIDResolver
}

func NewKnowledgeTool(db *gorm.DB, agentName string, tenantID TenantIDResolver) *KnowledgeTool {
	return &KnowledgeTool{
		db:        db,
		agentName: agentName,
		tenantID:  tenantID,
	}
}

func (t *KnowledgeTool) Name() string {
	return "knowledge"
}

func (t *KnowledgeTool) Description() string {
	return "管理知识库：search(搜索知识)、list(列出知识)、add(添加知识)"
}

func (t *KnowledgeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "操作: search(搜索知识), list(列出知识列表), add(添加知识)",
				"enum":        []string{"search", "list", "add"},
			},
			// search 参数
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词（action=search时使用）",
			},
			"get_detail": map[string]interface{}{
				"type":        "boolean",
				"description": "是否返回详细内容（action=search时使用），默认false",
			},
			// list 参数
			"category": map[string]interface{}{
				"type":        "string",
				"description": "按类别过滤（action=list时使用）",
			},
			"tag": map[string]interface{}{
				"type":        "string",
				"description": "按标签过滤（action=list时使用）",
			},
			// add 参数
			"title": map[string]interface{}{
				"type":        "string",
				"description": "知识标题（action=add时使用）",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "知识内容（action=add时使用）",
			},
			"summary": map[string]interface{}{
				"type":        "string",
				"description": "知识摘要（action=add时使用，可选）",
			},
			"knowledge_category": map[string]interface{}{
				"type":        "string",
				"description": "类别（action=add时使用）：lesson/insight/fact/knowledge/pattern",
				"enum":        []string{"lesson", "insight", "fact", "knowledge", "pattern"},
			},
			"tags": map[string]interface{}{
				"type":        "array",
				"description": "标签列表（action=add时使用），如 [\"python\",\"deployment\"]",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"importance": map[string]interface{}{
				"type":        "number",
				"description": "重要性 0-1（action=add时使用），默认0.5",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "返回数量（action=search/list时使用），默认5",
			},
		},
		"required": []string{"action"},
	}
}

func (t *KnowledgeTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	tenantID := resolveTenantID(ctx, t.tenantID)

	switch action {
	case "search":
		return t.searchKnowledge(tenantID, params)
	case "list":
		return t.listKnowledge(tenantID, params)
	case "add":
		return t.addKnowledge(tenantID, params)
	default:
		return fmt.Sprintf("不支持的操作: %s", action), nil
	}
}

func (t *KnowledgeTool) searchKnowledge(tenantID string, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return "请提供搜索关键词 (query)", nil
	}

	getDetail, _ := params["get_detail"].(bool)
	limit := 5
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	var knowledge []knowledgeRecord
	dbQuery := t.db.Where("tenant_id = ? AND agent_name = ?", tenantID, t.agentName).
		Where("title LIKE ? OR summary LIKE ? OR content LIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%")

	err := dbQuery.Order("importance DESC, created_at DESC").Limit(limit).Find(&knowledge).Error
	if err != nil {
		return "", fmt.Errorf("搜索失败: %w", err)
	}

	if len(knowledge) == 0 {
		return fmt.Sprintf("未找到与「%s」相关的知识", query), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("## 📚 搜索结果: %s\n\n", query))

	for i, k := range knowledge {
		result.WriteString(fmt.Sprintf("### %d. %s\n", i+1, k.Title))
		result.WriteString(fmt.Sprintf("- 类别: %s\n", k.Category))
		if k.Tags != nil {
			var tags []string
			json.Unmarshal(k.Tags, &tags)
			if len(tags) > 0 {
				result.WriteString(fmt.Sprintf("- 标签: %s\n", strings.Join(tags, "、")))
			}
		}

		if getDetail {
			result.WriteString(fmt.Sprintf("\n%s\n", k.Content))
		} else {
			result.WriteString(fmt.Sprintf("- 摘要: %s\n", k.Summary))
		}
		result.WriteString("\n")
	}

	return result.String(), nil
}

func (t *KnowledgeTool) listKnowledge(tenantID string, params map[string]interface{}) (string, error) {
	category, _ := params["category"].(string)
	tag, _ := params["tag"].(string)
	limit := 20
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	dbQuery := t.db.Model(&knowledgeRecord{}).
		Where("tenant_id = ? AND agent_name = ?", tenantID, t.agentName)

	if category != "" {
		dbQuery = dbQuery.Where("category = ?", category)
	}
	if tag != "" {
		dbQuery = dbQuery.Where("tags LIKE ?", "%"+tag+"%")
	}

	var knowledge []knowledgeRecord
	err := dbQuery.Order("importance DESC, created_at DESC").Limit(limit).Find(&knowledge).Error
	if err != nil {
		return "", fmt.Errorf("查询列表失败: %w", err)
	}

	if len(knowledge) == 0 {
		return "知识库为空", nil
	}

	var result strings.Builder
	result.WriteString("## 📚 知识列表\n\n")
	result.WriteString("| 标题 | 类别 | 重要性 | 创建时间 |\n")
	result.WriteString("|------|------|--------|----------|\n")

	for _, k := range knowledge {
		result.WriteString(fmt.Sprintf("| %s | %s | %.1f | %s |\n",
			textutil.TruncateRunes(k.Title, 40),
			k.Category,
			k.Importance,
			k.CreatedAt.Format("01-02"),
		))
	}

	result.WriteString(fmt.Sprintf("\n共 %d 条知识", len(knowledge)))

	return result.String(), nil
}

func (t *KnowledgeTool) addKnowledge(tenantID string, params map[string]interface{}) (string, error) {
	title, _ := params["title"].(string)
	if title == "" {
		return "请提供知识标题", nil
	}

	content, _ := params["content"].(string)
	if content == "" {
		return "请提供知识内容", nil
	}

	summary, _ := params["summary"].(string)
	if summary == "" {
		if len(content) > 200 {
			summary = content[:200] + "..."
		} else {
			summary = content
		}
	}

	category, _ := params["knowledge_category"].(string)
	if category == "" {
		category = "knowledge"
	}

	var tags []string
	if tagsRaw, ok := params["tags"].([]interface{}); ok {
		for _, tag := range tagsRaw {
			if tagStr, ok := tag.(string); ok {
				tags = append(tags, tagStr)
			}
		}
	}

	importance := 0.5
	if imp, ok := params["importance"].(float64); ok {
		importance = imp
		if importance < 0 {
			importance = 0
		}
		if importance > 1 {
			importance = 1
		}
	}

	tagsJSON, _ := json.Marshal(tags)

	knowledge := &knowledgeRecord{
		TenantID:    tenantID,
		AgentName:   t.agentName,
		KnowledgeID: fmt.Sprintf("know_%d", time.Now().UnixNano()),
		Title:       title,
		Summary:     summary,
		Content:     content,
		Category:    category,
		Tags:        tagsJSON,
		Importance:  importance,
		SourceType:  "manual",
	}

	if err := t.db.Create(knowledge).Error; err != nil {
		return "", fmt.Errorf("保存知识失败: %w", err)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("✅ 知识已保存\n\n"))
	result.WriteString(fmt.Sprintf("标题: %s\n", title))
	result.WriteString(fmt.Sprintf("类别: %s\n", category))
	if len(tags) > 0 {
		result.WriteString(fmt.Sprintf("标签: %s\n", strings.Join(tags, "、")))
	}
	result.WriteString(fmt.Sprintf("重要性: %.1f⭐\n\n", importance))
	result.WriteString(fmt.Sprintf("摘要: %s", summary))

	return result.String(), nil
}

func extractKeywords(text string) []string {
	text = strings.ToLower(text)
	punctuations := []string{"?", "？", "!", "！", ".", "。", ",", "，", ":", "：", ";", "；"}
	for _, p := range punctuations {
		text = strings.ReplaceAll(text, p, " ")
	}

	words := strings.Fields(text)

	stopWords := map[string]bool{
		"的": true, "了": true, "是": true, "在": true, "和": true, "与": true,
		"我": true, "你": true, "他": true, "这": true, "那": true, "有": true,
		"帮": true, "请": true, "问": true, "想": true, "要": true, "能": true,
		"a": true, "an": true, "the": true, "is": true, "are": true,
	}

	keywords := []string{}
	for _, word := range words {
		if len(word) > 1 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}

	// 去重
	seen := make(map[string]bool)
	unique := []string{}
	for _, kw := range keywords {
		if !seen[kw] {
			seen[kw] = true
			unique = append(unique, kw)
		}
	}

	if len(unique) > 5 {
		unique = unique[:5]
	}

	return unique
}

func extractSolutionFromAnswer(answer string) string {
	// 尝试找到关键信息
	sentences := strings.Split(answer, "。")
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) == 0 {
			continue
		}

		keyMarkers := []string{"成功", "完成", "结果是", "答案是", "建议", "推荐"}
		for _, marker := range keyMarkers {
			if strings.Contains(sentence, marker) {
				if len(sentence) > 150 {
					return sentence[:150] + "..."
				}
				return sentence
			}
		}
	}

	if len(sentences) > 0 && len(sentences[0]) > 0 {
		first := strings.TrimSpace(sentences[0])
		if len(first) > 150 {
			return first[:150] + "..."
		}
		return first
	}

	if len(answer) > 150 {
		return answer[:150] + "..."
	}
	return answer
}

// ==================== Knowledge Query (RAG-style retrieval) ====================

type KnowledgeQueryTool struct {
	db        *gorm.DB
	workspace string
	tenantID  TenantIDResolver
}

func NewKnowledgeQueryTool(db *gorm.DB, workspace string, tenantID TenantIDResolver) *KnowledgeQueryTool {
	return &KnowledgeQueryTool{db: db, workspace: workspace, tenantID: tenantID}
}

func (t *KnowledgeQueryTool) Name() string { return "knowledge_query" }

func (t *KnowledgeQueryTool) Description() string {
	return "Search the knowledge base for information relevant to a query. " +
		"Returns the most relevant chunks with source attribution. " +
		"Use this before answering questions that may have been covered in previously extracted documents."
}

func (t *KnowledgeQueryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "What are you looking for? Full sentence or keywords.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Max results (default 3, max 5)",
				"minimum":     1, "maximum": 5,
			},
		},
		"required": []string{"query"},
	}
}

func (t *KnowledgeQueryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	limit := 3
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}
	if limit > 5 {
		limit = 5
	}

	// Search knowledge base
	allKnowledge := t.loadAllKnowledge(resolveTenantID(ctx, t.tenantID))
	if len(allKnowledge) == 0 {
		return "No knowledge chunks found. Use extract(file='doc.pdf') to index documents first.", nil
	}

	// Build TF-IDF index and search by semantic similarity
	idx := ragmodel.NewVectorIndex()
	for _, k := range allKnowledge {
		idx.Add(k.source, k.content)
	}
	hits := idx.Search(query, limit)

	if len(hits) == 0 {
		return fmt.Sprintf("No relevant knowledge found for: %s", query), nil
	}

	// Format results
	var out strings.Builder
	out.WriteString(fmt.Sprintf("## 🔍 Knowledge Results for: %s\n\n", query))
	for i, h := range hits {
		out.WriteString(fmt.Sprintf("### Result %d (score: %.3f, source: %s)\n\n", i+1, h.Score, h.ID))
		content := h.Content
		if len(content) > 800 {
			content = content[:800] + "..."
		}
		out.WriteString(content + "\n\n")
	}
	out.WriteString(fmt.Sprintf("---\n*TF-IDF semantic search, showing top %d*", min(limit, len(hits))))
	return out.String(), nil
}

// loadAllKnowledge loads all knowledge chunks from the workspace.
func (t *KnowledgeQueryTool) loadAllKnowledge(tenantID string) []struct{ content, source string } {
	var result []struct{ content, source string }

	knowledgeDir := filepath.Join(t.workspace, "knowledge_chunks")
	entries, err := os.ReadDir(knowledgeDir)
	if err != nil {
		// Also try DB-based knowledge
		if t.db != nil {
			var knowledges []knowledgeRecord
			t.db.Where("tenant_id = ?", tenantID).
				Order("created_at DESC").Limit(50).Find(&knowledges)
			for _, k := range knowledges {
				result = append(result, struct{ content, source string }{
					k.Content, k.Category + "/" + k.Title,
				})
			}
		}
		return result
	}

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_chunks.md") {
			data, err := os.ReadFile(filepath.Join(knowledgeDir, e.Name()))
			if err != nil {
				continue
			}
			// Parse chunks from the file
			sections := strings.Split(string(data), "\n## Chunk ")
			for _, s := range sections[1:] { // skip header
				idx := strings.Index(s, "\n")
				if idx > 0 {
					chunk := strings.TrimSpace(s[idx:])
					if len(chunk) > 50 {
						result = append(result, struct{ content, source string }{
							content: chunk,
							source:  strings.TrimSuffix(e.Name(), "_chunks.md"),
						})
					}
				}
			}
		}
	}
	return result
}
