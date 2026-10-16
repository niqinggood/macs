package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	middlewarecore "mac/internal/middleware"
	"mac/internal/ragmodel"
	"mac/internal/security"
	sessioncore "mac/internal/session"
	"mac/internal/textutil"
	knowledgetools "mac/internal/tools/knowledge"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const defaultExecutionTenantID = "default"

func memoryTenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultExecutionTenantID
	}
	return security.BoundedIdentityValue(value, 64)
}

// MemoryEvent 记忆事件表 - 核心表，记录每一次交互
type MemoryEvent struct {
	ID uint `gorm:"primarykey" json:"id"`

	// 标识信息
	TenantID  string `gorm:"index;size:64;not null;default:default" json:"tenant_id,omitempty"`
	AgentName string `gorm:"index;size:100;not null" json:"agent_name"` // 智能体名称
	AgentType string `gorm:"size:50;default:agent" json:"agent_type"`   // agent/team
	SessionID string `gorm:"index;size:100" json:"session_id"`          // 会话ID，用于关联多轮对话

	// 用户信息
	UserID      string `gorm:"index;size:100" json:"user_id"`   // 用户ID
	UserName    string `gorm:"index;size:100" json:"user_name"` // 用户名
	UserChannel string `gorm:"size:50" json:"user_channel"`     // 渠道: web/app/api/wechat/cli

	// 交互内容
	UserQuestion string `gorm:"type:text" json:"user_question"` // 用户问题
	SystemPrompt string `gorm:"type:text" json:"system_prompt"` // 使用的系统提示词（可选）
	FinalAnswer  string `gorm:"type:text" json:"final_answer"`  // 最终回答

	// 输出信息
	OutputType string         `gorm:"size:50" json:"output_type"`   // 输出类型: text/file/image/code
	OutputPath string         `gorm:"size:500" json:"output_path"`  // 输出文件路径（如果有）
	OutputMeta datatypes.JSON `gorm:"type:json" json:"output_meta"` // 输出元数据（如文件大小、格式等）

	// 调用链 - 合并 tool 和 skill
	CallChain datatypes.JSON `gorm:"type:json" json:"call_chain"` // 调用链: [{type, name, success, duration_ms, input_summary, output_summary, error}]
	// 示例: [{"type":"tool","name":"web_search","success":true,"duration_ms":1234},{"type":"skill","name":"summarize","success":true,"duration_ms":567}]

	// 统计信息
	TotalSteps      int   `json:"total_steps"`       // 调用链总步数
	TotalDurationMs int64 `json:"total_duration_ms"` // 总耗时(毫秒)

	// 状态和反馈
	Status   string `gorm:"size:20;default:pending;index" json:"status"` // success/failed/pending
	ErrorMsg string `gorm:"type:text" json:"error_msg"`                  // 错误信息（如果有）

	UserRating   *int   `json:"user_rating"`                    // 用户评分 1-5
	UserFeedback string `gorm:"type:text" json:"user_feedback"` // 用户反馈文本
	IsPositive   *bool  `json:"is_positive"`                    // 是否正面反馈（rating>=4）

	// 学习标记
	Insights  datatypes.JSON `gorm:"type:json" json:"insights"`   // 提取的见解列表
	LearnNote string         `gorm:"type:text" json:"learn_note"` // 学习笔记（人工/自动标注）

	// 时间戳
	StartTime time.Time `gorm:"index" json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserProfile 用户画像表
type UserProfile struct {
	ID uint `gorm:"primarykey" json:"id"`

	TenantID string `gorm:"index;size:64;not null;default:default" json:"tenant_id,omitempty"`
	UserName string `gorm:"index;size:100" json:"user_name"`

	// 基本信息
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	TotalInteractions int       `json:"total_interactions"`
	SuccessRate       float64   `json:"success_rate"`

	// 偏好
	Language      string `gorm:"size:10;default:zh" json:"language"`
	ResponseStyle string `gorm:"size:20;default:balanced" json:"response_style"`

	// 工具偏好统计
	ToolStats  datatypes.JSON `gorm:"type:json" json:"tool_stats"`
	SkillStats datatypes.JSON `gorm:"type:json" json:"skill_stats"`

	// 主题频率（原始数据）
	TopicFrequency datatypes.JSON `gorm:"type:json" json:"topic_frequency"`

	// ===== 新增：用户画像分析维度 =====
	// 性格特质分析
	PersonalityTraits datatypes.JSON `gorm:"type:json" json:"personality_traits"` // {"乐观积极": 5, "好奇求知": 3, "急躁冲动": 1}

	// 职业倾向分析
	CareerAffinity datatypes.JSON `gorm:"type:json" json:"career_affinity"` // {"软件工程师": 10, "产品经理": 3}

	// 关注领域分析
	InterestAreas datatypes.JSON `gorm:"type:json" json:"interest_areas"` // {"人工智能": 8, "开源关注": 5, "投资理财": 2}

	// 沟通风格
	CommunicationStyle datatypes.JSON `gorm:"type:json" json:"communication_style"` // {"礼貌客气": 8, "理性分析": 6}

	// 用户约束
	Constraints      datatypes.JSON `gorm:"type:json" json:"constraints"`
	PositivePatterns datatypes.JSON `gorm:"type:json" json:"positive_patterns"`
	NegativePatterns datatypes.JSON `gorm:"type:json" json:"negative_patterns"`

	// 近期历史
	RecentQuestions datatypes.JSON `gorm:"type:json" json:"recent_questions"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Knowledge 知识表 - 智能体学习到的知识
type Knowledge struct {
	ID          uint   `gorm:"primarykey" json:"id"`
	TenantID    string `gorm:"index;size:64;not null;default:default" json:"tenant_id,omitempty"`
	AgentName   string `gorm:"index;size:100;not null" json:"agent_name"` // 所属智能体
	KnowledgeID string `gorm:"uniqueIndex;size:100" json:"knowledge_id"`  // 知识唯一ID

	// 知识内容
	Title    string         `gorm:"size:500;not null" json:"title"` // 标题
	Summary  string         `gorm:"type:text" json:"summary"`       // 摘要（用于快速浏览）
	Content  string         `gorm:"type:longtext" json:"content"`   // 完整内容
	Category string         `gorm:"size:50;index" json:"category"`  // 分类: lesson/insight/fact/knowledge/tutorial
	Tags     datatypes.JSON `gorm:"type:json" json:"tags"`          // 标签: ["python","deployment","bug"]

	// 来源
	SourceType string `gorm:"size:50" json:"source_type"` // 来源类型: learning/feedback/extraction/manual
	SourceID   string `gorm:"size:100" json:"source_id"`  // 来源ID（如 MemoryEvent 的 ID）

	// RAG 集成
	IsVectorized bool   `gorm:"default:false" json:"is_vectorized"`        // 是否已向量化
	VectorID     string `gorm:"size:100" json:"vector_id"`                 // 向量数据库中的ID
	RAGStatus    string `gorm:"size:20;default:pending" json:"rag_status"` // pending/ready/failed

	// 重要性
	Importance float64 `gorm:"default:0.5" json:"importance"` // 重要性 0-1
	UseCount   int     `gorm:"default:0" json:"use_count"`    // 被使用的次数

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// ToolSkillStat 工具/技能统计表（可选，用于快速查询）
type ToolSkillStat struct {
	ID        uint   `gorm:"primarykey" json:"id"`
	TenantID  string `gorm:"index;size:64;not null;default:default" json:"tenant_id,omitempty"`
	AgentName string `gorm:"index;size:100" json:"agent_name"`
	Type      string `gorm:"size:20;index" json:"type"` // tool/skill
	Name      string `gorm:"size:100;index" json:"name"`

	SuccessCount    int     `json:"success_count"`
	FailCount       int     `json:"fail_count"`
	TotalCount      int     `json:"total_count"`
	TotalDurationMs int64   `json:"total_duration_ms"`
	AvgDurationMs   int64   `json:"avg_duration_ms"`
	SuccessRate     float64 `json:"success_rate"`

	LastUsedAt time.Time `json:"last_used_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AgentLog 智能体日志表 - 用于排查细节
type AgentLog struct {
	ID        uint   `gorm:"primarykey" json:"id"`
	TenantID  string `gorm:"index;size:64;not null;default:default" json:"tenant_id,omitempty"`
	AgentName string `gorm:"index;size:100" json:"agent_name"`
	Level     string `gorm:"size:20;index" json:"level"`    // debug/info/warn/error
	Category  string `gorm:"size:50;index" json:"category"` // memory/tool/skill/thinking/error

	Message   string         `gorm:"type:text" json:"message"`
	Details   datatypes.JSON `gorm:"type:json" json:"details"`   // 结构化详情
	TraceInfo string         `gorm:"size:500" json:"trace_info"` // 调用栈信息

	MemoryID  *uint     `json:"memory_id"` // 关联的 MemoryEvent ID
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

func InitMemoryDB(db *gorm.DB) error {
	// 注意：如果使用 MySQL，需要设置 charset=utf8mb4 以支持 emoji
	err := db.AutoMigrate(
		&MemoryEvent{},
		&UserProfile{},
		&Knowledge{},
		&ToolSkillStat{},
		&AgentLog{},
		&Entry{},
		&memoryMigration{},
		&memoryRetraction{},
		&sourceRecallRecord{},
		&Summary{},
		&ProcessingJob{},
		&reviewSource{},
		&Evidence{},
		&memoryChange{},
		&reviewBudget{},
		&processingSequence{},
		&recallTraceRecord{},
		&promptInputRecord{},
		&recallVector{},
	)
	if err != nil {
		return fmt.Errorf("记忆数据库表结构迁移失败: %w", err)
	}

	// 创建复合索引（如果数据库支持）
	if db.Dialector.Name() == "sqlite" {
		// SQLite 的索引创建
		db.Exec("CREATE INDEX IF NOT EXISTS idx_memory_user_status ON memory_events(tenant_id, user_id, status)")
		db.Exec("CREATE INDEX IF NOT EXISTS idx_memory_agent_created ON memory_events(tenant_id, agent_name, created_at)")
	} else {
		// MySQL/PostgreSQL 的索引创建
		db.Exec("CREATE INDEX IF NOT EXISTS idx_memory_user_status ON memory_events(tenant_id, user_id, status)")
		db.Exec("CREATE INDEX IF NOT EXISTS idx_memory_agent_created ON memory_events(tenant_id, agent_name, created_at)")
	}

	log.Println("记忆数据库表结构初始化成功")
	return nil
}

// 使用示例的辅助函数
func ExampleUsage(db *gorm.DB) {
	// 创建记忆事件
	event := &MemoryEvent{
		AgentName:    "my-agent",
		AgentType:    "agent",
		SessionID:    "sess_123",
		UserID:       "user_456",
		UserName:     "张三",
		UserChannel:  "web",
		UserQuestion: "帮我部署一个 Python 应用",
		FinalAnswer:  "已成功部署，访问地址: http://xxx",
		OutputType:   "text",
		CallChain: datatypes.JSON(`[
			{"type":"tool","name":"read_file","success":true,"duration_ms":123},
			{"type":"skill","name":"deploy_python","success":true,"duration_ms":5000},
			{"type":"tool","name":"http_request","success":true,"duration_ms":234}
		]`),
		TotalSteps:      3,
		TotalDurationMs: 5357,
		Status:          "success",
		UserRating:      ptr(5),
		UserFeedback:    "部署很顺利，文档清晰",
		IsPositive:      ptr(true),
		StartTime:       time.Now().Add(-5 * time.Second),
		EndTime:         time.Now(),
	}
	db.Create(event)

	// 更新用户画像
	var user UserProfile
	if err := db.Where("user_id = ?", "user_456").First(&user).Error; err != nil {
		// 创建新用户
		user = UserProfile{

			UserName:    "张三",
			FirstSeen:   time.Now(),
			LastSeen:    time.Now(),
			ToolStats:   datatypes.JSON(`{}`),
			Constraints: datatypes.JSON(`{}`),
		}
		db.Create(&user)
	}

	// 记录日志
	logEntry := &AgentLog{
		AgentName: "my-agent",
		Level:     "info",
		Category:  "memory",
		Message:   "成功保存记忆事件",
		Details:   datatypes.JSON(`{"event_id": 1}`),
		MemoryID:  &event.ID,
	}
	db.Create(logEntry)
}

func ptr[T any](v T) *T {
	return &v
}

type MemoryStore struct {
	baseDir      string
	db           *gorm.DB
	agentName    string
	memoryWindow int
	logsDir      string // logs/ - 详细日志目录
	mu           sync.RWMutex
	memoryFile   string // MEMORY.md - 人类可读的记忆
}

func NewMemoryStore(memoryDir string, db *gorm.DB, agentName string, memoryWindow int) *MemoryStore {
	os.MkdirAll(memoryDir, 0755)
	logsDir := filepath.Join(memoryDir, "logs")
	os.MkdirAll(logsDir, 0755)
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	ms := &MemoryStore{
		baseDir:      memoryDir,
		db:           db,
		agentName:    agentName,
		memoryWindow: memoryWindow,
		memoryFile:   memoryFile,
		logsDir:      logsDir,
	}
	ms.ensureFTS5()
	return ms
}

func (m *MemoryStore) EventsSince(start time.Time) []MemoryEvent {
	if m == nil || m.db == nil {
		return nil
	}
	var events []MemoryEvent
	m.db.Where("agent_name = ? AND created_at >= ?", m.agentName, start).Find(&events)
	return events
}

// ensureFTS5 创建 SQLite FTS5 全文索引，支持 BM25 排序的跨 session 记忆召回
func (m *MemoryStore) ensureFTS5() {
	// 创建 FTS5 虚拟表（如果不存在）
	m.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS memory_events_fts USING fts5(
		user_question, final_answer, learn_note, session_id, user_name,
		content='memory_events', content_rowid='id'
	)`)

	// 触发器：insert/update/delete 时自动同步 FTS 索引
	m.db.Exec(`CREATE TRIGGER IF NOT EXISTS mem_fts_insert AFTER INSERT ON memory_events BEGIN
		INSERT INTO memory_events_fts(rowid, user_question, final_answer, learn_note, session_id, user_name)
		VALUES (new.id, new.user_question, new.final_answer, new.learn_note, new.session_id, new.user_name);
	END`)
	m.db.Exec(`CREATE TRIGGER IF NOT EXISTS mem_fts_delete AFTER DELETE ON memory_events BEGIN
		INSERT INTO memory_events_fts(memory_events_fts, rowid, user_question, final_answer, learn_note, session_id, user_name)
		VALUES ('delete', old.id, old.user_question, old.final_answer, old.learn_note, old.session_id, old.user_name);
	END`)
	m.db.Exec(`CREATE TRIGGER IF NOT EXISTS mem_fts_update AFTER UPDATE ON memory_events BEGIN
		INSERT INTO memory_events_fts(memory_events_fts, rowid, user_question, final_answer, learn_note, session_id, user_name)
		VALUES ('delete', old.id, old.user_question, old.final_answer, old.learn_note, old.session_id, old.user_name);
		INSERT INTO memory_events_fts(rowid, user_question, final_answer, learn_note, session_id, user_name)
		VALUES (new.id, new.user_question, new.final_answer, new.learn_note, new.session_id, new.user_name);
	END`)

	// 全量重建索引（幂等，已存在的行不会被重复插入）
	m.db.Exec(`INSERT INTO memory_events_fts(rowid, user_question, final_answer, learn_note, session_id, user_name)
		SELECT id, user_question, final_answer, learn_note, session_id, user_name
		FROM memory_events WHERE id NOT IN (SELECT rowid FROM memory_events_fts)`)
}

// memoryFTSResult holds a single FTS5 search result.
type memoryFTSResult struct {
	ID           int
	UserQuestion string
	FinalAnswer  string
	LearnNote    string
	SessionID    string
	UserName     string
	Score        float64
	ScoreKind    string
	MatchMethods []string
}

const (
	memoryScoreBM25      = "bm25"
	memoryScoreVector    = "vector_similarity"
	memoryScoreRRF       = "rrf"
	memoryMatchFTS       = "fts"
	memoryMatchLexical   = "lexical"
	memoryMatchVector    = "vector"
	memoryLexicalScanMax = 200
)

// memoryFTSQuery converts arbitrary user prose into an FTS5 expression made
// only from quoted literal tokens. Passing the raw question to MATCH makes
// punctuation such as ?, parentheses, quotes, OR, and '-' part of FTS syntax
// and can turn a normal user question into a query error.
func memoryFTSQuery(query string) string {
	tokens := memorySearchTokens(query, 12)
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.ReplaceAll(token, `"`, `""`)
		quoted = append(quoted, `"`+token+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// memorySearchTokens intentionally keeps this tokenizer deterministic. FTS5's
// unicode61 tokenizer keeps a contiguous Chinese phrase as one token, so a
// shorter paraphrase may not match it. The lexical fallback below handles that
// case; these tokens primarily make MATCH safe and effective for ASCII/mixed
// text while preserving complete CJK spans.
func memorySearchTokens(text string, limit int) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
	})
	seen := make(map[string]bool, len(parts))
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || ragmodel.IsStopToken(part) || seen[part] {
			continue
		}
		seen[part] = true
		tokens = append(tokens, part)
		if limit > 0 && len(tokens) >= limit {
			break
		}
	}
	return tokens
}

// SearchMemoryFTS 使用 BM25 排序搜索记忆，支持跨 session 召回
func (m *MemoryStore) SearchMemoryFTS(query string, userName string, limit int) []memoryFTSResult {
	return m.SearchMemoryFTSForTenant(defaultExecutionTenantID, query, userName, limit)
}

func (m *MemoryStore) SearchMemoryFTSForTenant(tenantID, query, userName string, limit int) []memoryFTSResult {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	tenantID = memoryTenantID(tenantID)
	q := memoryFTSQuery(query)
	if q == "" {
		return nil
	}

	var rows *gorm.DB
	if userName != "" {
		rows = m.db.Raw(`SELECT memory_events_fts.rowid, memory_events_fts.user_question,
			memory_events_fts.final_answer, memory_events_fts.learn_note,
			memory_events_fts.session_id, memory_events_fts.user_name,
			bm25(memory_events_fts, 3.0, 1.0, 1.5, 0.0, 0.0) AS score
			FROM memory_events_fts
			JOIN memory_events ON memory_events.id = memory_events_fts.rowid
			WHERE memory_events_fts MATCH ? AND memory_events.tenant_id = ?
				AND memory_events.agent_name = ? AND memory_events_fts.user_name = ?
			ORDER BY score LIMIT ?`, q, tenantID, m.agentName, userName, limit)
	} else {
		rows = m.db.Raw(`SELECT memory_events_fts.rowid, memory_events_fts.user_question,
			memory_events_fts.final_answer, memory_events_fts.learn_note,
			memory_events_fts.session_id, memory_events_fts.user_name,
			bm25(memory_events_fts, 3.0, 1.0, 1.5, 0.0, 0.0) AS score
			FROM memory_events_fts
			JOIN memory_events ON memory_events.id = memory_events_fts.rowid
			WHERE memory_events_fts MATCH ? AND memory_events.tenant_id = ? AND memory_events.agent_name = ?
			ORDER BY score LIMIT ?`, q, tenantID, m.agentName, limit)
	}
	var results []memoryFTSResult
	rawRows, err := rows.Rows()
	if err != nil {
		return nil
	}
	defer rawRows.Close()
	for rawRows.Next() {
		var r memoryFTSResult
		if err := rawRows.Scan(&r.ID, &r.UserQuestion, &r.FinalAnswer, &r.LearnNote, &r.SessionID, &r.UserName, &r.Score); err != nil {
			continue
		}
		if r.ID > 0 {
			r.ScoreKind = memoryScoreBM25
			r.MatchMethods = []string{memoryMatchFTS}
			results = append(results, r)
		}
	}
	return results
}

func (m *MemoryStore) SaveEventLog(taskID, taskDesc, action, result, insight string, outputDir string) error {
	content := fmt.Sprintf("### 事件: %s\n\n**操作**: %s\n\n**结果**:\n%s\n\n**心得**: %s", taskDesc, action, textutil.Truncate(result, 300), insight)

	summary := fmt.Sprintf("%s - %s", taskDesc, insight)
	if len(summary) > 180 {
		summary = summary[:180] + "..."
	}

	return m.SaveEventlog(content, "event", []string{"event", taskID}, outputDir)
}

func (m *MemoryStore) SaveEventlog(content string, category string, tags []string, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var builder strings.Builder

	if len(tags) > 0 {
		builder.WriteString(fmt.Sprintf("**标签**: `%s`  ", strings.Join(tags, "`, `")))
	}
	builder.WriteString(fmt.Sprintf("**类别**: %s  \n", category))

	if reference != "" {
		builder.WriteString(fmt.Sprintf("**参考**: %s  \n", reference))
	}

	builder.WriteString("\n")
	builder.WriteString(content)
	builder.WriteString("\n\n---\n")

	f, err := os.OpenFile(m.memoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write([]byte(builder.String()))
	return err
}

func (m *MemoryStore) SaveTaskLog(taskID string, logType string, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 按日期创建日志文件
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(m.logsDir, fmt.Sprintf("%s.log", today))

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("\n[%s] [%s] [%s]\n", timestamp, taskID, logType))
	builder.WriteString(strings.Repeat("-", 80) + "\n")
	builder.Write([]byte(content))
	builder.WriteString("\n" + strings.Repeat("=", 80) + "\n")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write([]byte(builder.String()))
	return err
}

// SaveMemoryEvent 保存记忆事件
func (m *MemoryStore) SaveMemoryEvent(event *MemoryEvent) error {
	if shouldSkipMemoryEvent(event) {
		return nil
	}
	event.TenantID = memoryTenantID(event.TenantID)
	if event.AgentName == "" {
		event.AgentName = m.agentName
	}
	event.UserQuestion = strings.TrimSpace(middlewarecore.StripToolCalls(event.UserQuestion))
	event.FinalAnswer = strings.TrimSpace(middlewarecore.StripToolCalls(event.FinalAnswer))

	// 计算统计信息
	if event.CallChain != nil {
		var chain []map[string]interface{}
		if err := json.Unmarshal(event.CallChain, &chain); err == nil {
			event.TotalSteps = len(chain)

			// 更新工具/技能统计
			for _, step := range chain {
				stepType, _ := step["type"].(string)
				name, _ := step["name"].(string)
				success, _ := step["success"].(bool)
				duration, _ := step["duration_ms"].(float64)

				m.updateToolSkillStat(event.TenantID, stepType, name, success, int64(duration))
			}
		}
	}

	// 保存事件
	result := m.db.Create(event)
	if result.Error != nil {
		return fmt.Errorf("保存记忆事件失败: %w", result.Error)
	}

	// 更新用户画像
	go m.updateUserProfile(event)

	// 如果是成功且有反馈，提取学习内容
	if event.Status == "success" && event.UserFeedback != "" && event.IsPositive != nil && *event.IsPositive {
		go m.extractLearning(event)
	}

	return nil
}

func shouldSkipMemoryEvent(event *MemoryEvent) bool {
	if event == nil {
		return true
	}
	question := strings.TrimSpace(event.UserQuestion)
	answer := strings.TrimSpace(event.FinalAnswer)
	if question == "" && answer == "" {
		return true
	}
	if question != "" && sessioncore.LooksLikeHardInternalContent(question) {
		return true
	}
	if strings.TrimSpace(sessioncore.CleanCarryoverContent("user", question, 0)) == "" &&
		strings.TrimSpace(sessioncore.CleanCarryoverContent("assistant", answer, 0)) == "" {
		return true
	}
	return false
}

func generateConclusion(output string) string {
	// 尝试找结论部分
	lines := strings.Split(output, "\n")

	// 常见结论标记
	markers := []string{"结论：", "结论:", "总结：", "总结:", "综上所述",
		"因此", "所以", "最终", "In conclusion", "Conclusion:"}

	for _, line := range lines {
		lineLower := strings.ToLower(line)
		for _, marker := range markers {
			if strings.Contains(lineLower, strings.ToLower(marker)) {
				// 找到包含标记的行
				if len(line) > 500 {
					return line[:500] + "..."
				}
				return line
			}
		}
	}

	// 如果没有找到标记，取最后一段
	if len(lines) > 0 {
		lastPara := lines[len(lines)-1]
		if len(lastPara) > 500 {
			return lastPara[:500] + "..."
		}
		return lastPara
	}

	// 最后手段：截取开头
	if len(output) > 500 {
		return output[:500] + "..."
	}
	return output
}

func writeToBuilder(builder *strings.Builder, v interface{}) {
	switch val := v.(type) {
	case string:
		builder.WriteString(val)
	case []byte:
		builder.WriteString(string(val))
	case map[string]interface{}:
		jsonData, _ := json.MarshalIndent(val, "", "  ")
		builder.WriteString(string(jsonData))
	default:
		fmt.Fprintf(builder, "%+v", val)
	}
}

func (m *MemoryStore) SaveLog(entryType string, input interface{}, output interface{}, metadata map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 按日期创建日志文件
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(m.logsDir, fmt.Sprintf("%s.log", today))

	// 记录时间戳
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")

	var builder strings.Builder

	// 写入日志条目
	builder.WriteString(strings.Repeat("=", 80) + "\n")
	builder.WriteString(fmt.Sprintf("时间: %s\n", timestamp))
	builder.WriteString(fmt.Sprintf("类型: %s\n", entryType))

	// 写入输入（完整内容）
	builder.WriteString("\n【输入】\n")
	writeToBuilder(&builder, input)

	// 写入输出（完整内容）
	builder.WriteString("\n\n【输出】\n")
	writeToBuilder(&builder, output)

	// 写入元数据
	if len(metadata) > 0 {
		builder.WriteString("\n\n【元数据】\n")
		jsonData, _ := json.MarshalIndent(metadata, "", "  ")
		builder.WriteString(string(jsonData))
	}

	builder.WriteString("\n" + strings.Repeat("=", 80) + "\n\n")

	// 追加到当天的日志文件
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(builder.String())
	return err
}

func (m *MemoryStore) updateUserProfile(event *MemoryEvent) {
	var profile UserProfile
	tenantID := memoryTenantID(event.TenantID)

	// A username is only meaningful inside its tenant partition.
	result := m.db.Where("tenant_id = ? AND user_name = ?", tenantID, event.UserName).First(&profile)
	if result.Error != nil {
		// 创建新画像
		profile = UserProfile{
			TenantID:           tenantID,
			UserName:           event.UserName,
			FirstSeen:          time.Now(),
			LastSeen:           time.Now(),
			ToolStats:          datatypes.JSON(`{}`),
			SkillStats:         datatypes.JSON(`{}`),
			Constraints:        datatypes.JSON(`{}`),
			PersonalityTraits:  datatypes.JSON(`{}`),
			CareerAffinity:     datatypes.JSON(`{}`),
			InterestAreas:      datatypes.JSON(`{}`),
			CommunicationStyle: datatypes.JSON(`{}`),
		}
	}

	profile.LastSeen = time.Now()
	profile.TotalInteractions++

	// 更新成功率
	var totalSuccess int64
	m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND user_name = ? AND status = ?", tenantID, event.UserName, "success").
		Count(&totalSuccess)
	if profile.TotalInteractions > 0 {
		profile.SuccessRate = float64(totalSuccess) / float64(profile.TotalInteractions) * 100
	}

	// 更新主题频率（使用新的 extractTopics 函数）
	if event.UserQuestion != "" {
		topics := extractTopics(event.UserQuestion) // 使用新的基于词典的提取

		var topicFreq map[string]int
		if len(profile.TopicFrequency) > 0 {
			json.Unmarshal(profile.TopicFrequency, &topicFreq)
		}
		if topicFreq == nil {
			topicFreq = make(map[string]int)
		}
		for _, topic := range topics {
			topicFreq[topic]++
		}
		profile.TopicFrequency, _ = json.Marshal(topicFreq)

		// ===== 基于 topicFreq 更新各个分析维度 =====
		updatePersonalityTraits(&profile, topics)
		updateCareerAffinity(&profile, topics)
		updateInterestAreas(&profile, topics)
		updateCommunicationStyle(&profile, topics)
	}

	// 更新最近问题列表
	var recentQuestions []string
	if len(profile.RecentQuestions) > 0 {
		json.Unmarshal(profile.RecentQuestions, &recentQuestions)
	}
	recentQuestions = append([]string{textutil.Truncate(event.UserQuestion, 100)}, recentQuestions...)
	if len(recentQuestions) > 10 {
		recentQuestions = recentQuestions[:10]
	}
	profile.RecentQuestions, _ = json.Marshal(recentQuestions)

	m.db.Save(&profile)

	// EvoMem: 记录用户偏好变化（结构化变更历史，非静态快照）
	evolveMemory := func(field, oldVal, newVal string) {
		if oldVal == newVal || newVal == "" {
			return
		}
		evoContent := fmt.Sprintf("**[%s] 变更**\n旧: %s\n新: %s", field, oldVal, newVal)
		evoEvent := MemoryEvent{
			TenantID:     tenantID,
			AgentName:    "system",
			UserName:     event.UserName,
			UserQuestion: fmt.Sprintf("偏好变更: %s", field),
			FinalAnswer:  evoContent,
			LearnNote:    fmt.Sprintf("[演化] %s: %s → %s", field, oldVal, newVal),
			Status:       "evolution",
			StartTime:    time.Now(),
		}
		m.db.Create(&evoEvent)
	}

	// 检测关键字段变化
	if event.Status == "success" && event.UserFeedback != "" {
		var currentStyle string
		var currentConstraints map[string]interface{}
		json.Unmarshal(profile.CommunicationStyle, &currentStyle)
		json.Unmarshal(profile.Constraints, &currentConstraints)

		// ResponseStyle 变化
		var styleData map[string]interface{}
		json.Unmarshal(profile.CommunicationStyle, &styleData)
		newStyle, _ := styleData["preferred_style"].(string)

		// Constraints 变化（检查 forbidden 项）
		var oldConstraints map[string]interface{}
		if oldProfile, _ := m.GetUserProfileForTenant(tenantID, event.UserName); oldProfile != nil {
			if oldProfile.Constraints != nil {
				json.Unmarshal(oldProfile.Constraints, &oldConstraints)
			}
			oldForbidden, _ := oldConstraints["forbidden"].([]interface{})
			newForbidden, _ := currentConstraints["forbidden"].([]interface{})
			oldForbiddenStr := fmt.Sprintf("%v", oldForbidden)
			newForbiddenStr := fmt.Sprintf("%v", newForbidden)
			if oldForbiddenStr != newForbiddenStr {
				evolveMemory("用户约束/偏好", oldForbiddenStr, newForbiddenStr)
			}
		}
		_ = newStyle
	}
}

// updateToolSkillStat 更新工具/技能统计
func (m *MemoryStore) updateToolSkillStat(tenantID, typ, name string, success bool, durationMs int64) {
	var stat ToolSkillStat
	tenantID = memoryTenantID(tenantID)

	result := m.db.Where("tenant_id = ? AND agent_name = ? AND type = ? AND name = ?",
		tenantID, m.agentName, typ, name).First(&stat)

	if result.Error != nil {
		stat = ToolSkillStat{
			TenantID:  tenantID,
			AgentName: m.agentName,
			Type:      typ,
			Name:      name,
		}
	}

	stat.TotalCount++
	stat.TotalDurationMs += durationMs
	stat.AvgDurationMs = stat.TotalDurationMs / int64(stat.TotalCount)
	stat.LastUsedAt = time.Now()

	if success {
		stat.SuccessCount++
	} else {
		stat.FailCount++
	}

	if stat.TotalCount > 0 {
		stat.SuccessRate = float64(stat.SuccessCount) / float64(stat.TotalCount) * 100
	}

	m.db.Save(&stat)
}

// GetUserProfile 获取用户画像
func (m *MemoryStore) GetUserProfile(userName string) (*UserProfile, error) {
	return m.GetUserProfileForTenant(defaultExecutionTenantID, userName)
}

func (m *MemoryStore) GetUserProfileForTenant(tenantID, userName string) (*UserProfile, error) {
	var profile UserProfile
	err := m.db.Where("tenant_id = ? AND user_name = ?", memoryTenantID(tenantID), userName).
		First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetUserHistory 获取用户历史（最近N条）
func (m *MemoryStore) GetUserHistory(userName string, limit int) ([]MemoryEvent, error) {
	return m.GetUserHistoryForTenant(defaultExecutionTenantID, userName, limit)
}

func (m *MemoryStore) GetUserHistoryForTenant(tenantID, userName string, limit int) ([]MemoryEvent, error) {
	var events []MemoryEvent
	err := m.db.Where("tenant_id = ? AND user_name = ?", memoryTenantID(tenantID), userName).
		Order("created_at DESC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func (m *MemoryStore) GetRecommendedTools(userName string, limit int) ([]string, error) {
	var stats []ToolSkillStat
	err := m.db.Where("tenant_id = ? AND type = ? AND success_rate >= ?",
		defaultExecutionTenantID, "tool", 70).
		Order("success_rate DESC").
		Limit(limit).
		Find(&stats).Error
	if err != nil {
		return nil, err
	}

	tools := make([]string, len(stats))
	for i, stat := range stats {
		tools[i] = stat.Name
	}
	return tools, nil
}

// SaveKnowledge 保存知识
func (m *MemoryStore) SaveKnowledge(knowledge *Knowledge) error {
	if knowledge.KnowledgeID == "" {
		knowledge.KnowledgeID = fmt.Sprintf("know_%d", time.Now().UnixNano())
	}
	knowledge.TenantID = memoryTenantID(knowledge.TenantID)
	if knowledge.AgentName == "" {
		knowledge.AgentName = m.agentName
	}
	return m.db.Create(knowledge).Error
}

// GetKnowledge 获取知识（按相似度/标签）
func (m *MemoryStore) GetKnowledge(tags []string, limit int) ([]Knowledge, error) {
	var knowledge []Knowledge

	query := m.db.Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, m.agentName)

	// 如果有标签，按标签查询
	if len(tags) > 0 {
		// 这里简化处理，实际可以用 JSON 查询
		// MySQL: JSON_OVERLAPS(tags, ?)
		tagQuery := m.db.Where("tags LIKE ?", "%"+tags[0]+"%")
		for _, tag := range tags[1:] {
			tagQuery = tagQuery.Or("tags LIKE ?", "%"+tag+"%")
		}
		query = query.Where(tagQuery)
	}

	err := query.Order("importance DESC, use_count DESC").
		Limit(limit).
		Find(&knowledge).Error
	return knowledge, err
}

// RecordLog 记录日志
func (m *MemoryStore) RecordLog(level, category, message string, details map[string]interface{}, memoryID *uint) error {
	detailsJSON, _ := json.Marshal(details)

	log := &AgentLog{
		TenantID:  defaultExecutionTenantID,
		AgentName: m.agentName,
		Level:     level,
		Category:  category,
		Message:   message,
		Details:   detailsJSON,
		MemoryID:  memoryID,
	}

	return m.db.Create(log).Error
}

// GetMemoryStats 获取记忆统计
func (m *MemoryStore) GetMemoryStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var totalEvents int64
	m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, m.agentName).
		Count(&totalEvents)
	stats["total_events"] = totalEvents

	var successEvents int64
	m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ? AND status = ?", defaultExecutionTenantID, m.agentName, "success").
		Count(&successEvents)
	stats["success_rate"] = float64(successEvents) / float64(totalEvents) * 100

	var totalUsers int64
	m.db.Model(&UserProfile{}).Where("tenant_id = ?", defaultExecutionTenantID).Count(&totalUsers)
	stats["total_users"] = totalUsers

	var totalKnowledge int64
	m.db.Model(&Knowledge{}).
		Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, m.agentName).
		Count(&totalKnowledge)
	stats["total_knowledge"] = totalKnowledge

	return stats, nil
}

// extractLearning 从成功事件中提取学习内容
func (m *MemoryStore) extractLearning(event *MemoryEvent) {
	// 分析调用链，找出成功的模式
	var chain []map[string]interface{}
	if err := json.Unmarshal(event.CallChain, &chain); err != nil {
		return
	}

	// 找出成功的工具序列
	var successPattern []string
	for _, step := range chain {
		if success, ok := step["success"].(bool); ok && success {
			if name, ok := step["name"].(string); ok {
				successPattern = append(successPattern, name)
			}
		}
	}

	if len(successPattern) > 0 {
		// 保存为知识
		knowledge := &Knowledge{
			TenantID:  event.TenantID,
			AgentName: m.agentName,
			Title:     fmt.Sprintf("成功模式: %s", strings.Join(successPattern, " → ")),
			Summary: fmt.Sprintf("在处理「%s」时，使用 %s 成功",
				textutil.Truncate(event.UserQuestion, 50), strings.Join(successPattern, " → ")),
			Content: fmt.Sprintf("问题: %s\n调用链: %v\n反馈: %s",
				event.UserQuestion, successPattern, event.UserFeedback),
			Category:   "pattern",
			SourceType: "feedback",
			SourceID:   fmt.Sprintf("%d", event.ID),
			Importance: 0.7,
		}
		m.db.Create(knowledge)
	}

	// 如果有用户反馈中的关键词，也提取
	if event.UserFeedback != "" {
		// 提取反馈中的重要信息
		insights := extractInsightsFromFeedback(event.UserFeedback)
		for _, insight := range insights {
			knowledge := &Knowledge{
				TenantID:  event.TenantID,
				AgentName: m.agentName,
				Title:     fmt.Sprintf("用户反馈: %s", textutil.Truncate(insight, 50)),
				Summary:   insight,
				Content: fmt.Sprintf("来自用户 %s 的反馈: %s\n原问题: %s",
					event.UserName, insight, textutil.Truncate(event.UserQuestion, 100)),
				Category:   "feedback",
				SourceType: "feedback",
				SourceID:   fmt.Sprintf("%d", event.ID),
				Importance: 0.8,
			}
			m.db.Create(knowledge)
		}
	}
}

var topicDict = map[string][]string{
	// === 原有主题保留（省略重复，只展示新增）===
	"编程开发":     {"python", "java", "golang", "rust", "c++", "javascript", "typescript", "后端", "前端", "全栈", "代码", "编程", "开发", "debug", "调试", "重构", "api", "接口", "数据库", "sql", "mysql", "redis", "mongodb"},
	"前端开发":     {"react", "vue", "angular", "html", "css", "ui", "界面", "组件", "页面", "样式", "响应式"},
	"后端开发":     {"spring", "django", "flask", "gin", "node.js", "微服务", "分布式", "高并发", "负载均衡"},
	"DevOps运维": {"docker", "kubernetes", "k8s", "ci/cd", "jenkins", "部署", "发布", "监控", "日志", "告警", "nginx", "linux"},
	"移动开发":     {"ios", "android", "flutter", "react native", "app", "移动端", "手机应用"},
	"人工智能":     {"ai", "人工智能", "机器学习", "深度学习", "神经网络", "nlp", "自然语言", "计算机视觉", "图像识别", "语音识别", "llm", "大模型", "gpt", "claude", "bert"},
	"数据分析":     {"数据分析", "数据挖掘", "数据仓库", "etl", "bi", "可视化", "报表", "tableau", "power bi", "excel", "统计", "回归", "聚类"},
	"数据科学":     {"pandas", "numpy", "scikit", "tensorflow", "pytorch", "jupyter", "特征工程", "模型训练"},
	"网络安全":     {"安全", "漏洞", "渗透", "防火墙", "加密", "认证", "授权", "oAuth", "jwt", "https", "ddos"},
	"云计算":      {"aws", "azure", "gcp", "阿里云", "腾讯云", "云原生", "serverless", "lambda", "s3", "ec2"},
	"区块链":      {"区块链", "web3", "比特币", "以太坊", "智能合约", "nft", "加密货币", "defi"},

	// === 工作与效率 ===
	"项目管理": {"项目", "进度", "里程碑", "风险", "资源", "deadline", "截止日期", "延期", "交付", "甘特图", "燃尽图"},
	"敏捷开发": {"敏捷", "scrum", "看板", "sprint", "迭代", "站会", "回顾会", "planning", "po", "sm"},
	"产品管理": {"产品", "需求", "prd", "原型", "axure", "figma", "用户故事", "backlog", "优先级", "mvp", "用户体验", "ux"},
	"团队协作": {"协作", "沟通", "同步", "对齐", "会议", "讨论", "brainstorm", "头脑风暴", "反馈", "review"},
	"文档写作": {"文档", "wiki", "confluence", "markdown", "readme", "写", "记录", "笔记", "总结", "周报", "月报", "ppt", "汇报"},

	// === 学习成长 ===
	"学习":   {"学习", "课程", "教程", "培训", "读书", "阅读", "看书", "笔记", "知识", "技能", "提升", "成长"},
	"考试认证": {"考试", "面试", "笔试", "算法题", "leetcode", "刷题", "证书", "认证", "pmp", "aws认证"},
	"研究探索": {"研究", "调研", "探索", "了解", "关注", "趋势", "新技术", "前沿"},

	// === 生活日常 ===
	"财经":   {"钱", "赚钱", "理财", "投资", "股票", "基金", "债券", "期货", "外汇", "加密货币", "保险", "税务", "报税", "预算", "省钱"},
	"股票投资": {"a股", "港股", "美股", "k线", "涨停", "跌停", "牛市", "熊市", "仓位", "买入", "卖出", "持仓", "市盈率"},
	"购物":   {"买", "购物", "淘宝", "京东", "拼多多", "亚马逊", "下单", "退货", "退款", "优惠券", "打折", "促销", "双十一"},
	"食品餐饮": {"吃", "美食", "餐厅", "外卖", "做饭", "菜谱", "食材", "零食", "饮料", "咖啡", "奶茶", "水果"},
	"衣服时尚": {"衣服", "穿搭", "时尚", "品牌", "鞋子", "包包", "配饰", "化妆", "护肤品", "发型", "潮流"},
	"汽车":   {"车", "汽车", "买车", "卖车", "二手车", "加油", "充电", "保养", "维修", "保险", "驾照", "违章", "停车", "特斯拉", "比亚迪"},
	"房产家居": {"房子", "买房", "卖房", "租房", "装修", "家居", "家具", "物业", "房贷", "公积金", "搬家"},
	"电子产品": {"手机", "电脑", "笔记本", "平板", "耳机", "相机", "智能手表", "电视", "音箱", "充电器", "苹果", "华为", "小米", "三星"},
	"数码科技": {"数码", "科技", "黑科技", "智能家居", "iot", "5g", "wifi", "蓝牙"},

	// === 娱乐休闲 ===
	"体育":   {"体育", "运动", "足球", "篮球", "网球", "跑步", "健身", "瑜伽", "游泳", "骑行", "马拉松", "nba", "cba", "世界杯", "奥运会"},
	"电竞游戏": {"游戏", "电竞", "lol", "王者荣耀", "吃鸡", "原神", "steam", "ps5", "xbox", "switch", "打游戏", "上分"},
	"电影电视": {"电影", "电视剧", "综艺", "动漫", "纪录片", "影评", "追剧", "netflix", "爱奇艺", "腾讯视频", "b站"},
	"音乐":   {"音乐", "听歌", "歌曲", "歌手", "专辑", "演唱会", "spotify", "网易云", "qq音乐", "摇滚", "流行", "古典"},
	"阅读":   {"书", "小说", "文学", "科幻", "武侠", "言情", "历史书", "传记", "杂志"},
	"旅游":   {"旅游", "旅行", "景点", "酒店", "民宿", "机票", "攻略", "自驾", "跟团游", "出国", "签证"},

	// === 信息查询 ===
	"天气":   {"天气", "下雨", "晴天", "温度", "天气预报", "空气质量", "pm2.5"},
	"出行交通": {"出行", "交通", "地铁", "公交", "打车", "滴滴", "高德", "地图", "导航", "路况", "高铁", "火车", "飞机", "航班"},
	"新闻资讯": {"新闻", "资讯", "报道", "热点", "热搜", "头条", "时事", "动态"},

	// === 情绪与态度 ===
	"积极肯定": {"很好", "不错", "优秀", "很棒", "赞", "厉害", "牛", "完美", "满意", "喜欢", "感谢", "谢谢"},
	"消极否定": {"不好", "差", "垃圾", "糟糕", "烦", "讨厌", "恶心", "失望", "不满", "生气", "愤怒"},
	"疑惑困惑": {"为什么", "怎么会", "不理解", "不明白", "搞不懂", "啥意思", "什么情况"},
	"求助请求": {"请帮我", "帮忙", "求助", "救", "怎么办", "如何", "怎么", "能不能", "可以吗", "请问"},
	"紧急迫切": {"急", "紧急", "马上", "立刻", "现在", "快点", "赶紧", " ASAP"},
	"不耐烦":  {"你妈的", "靠", "操", "md", "烦死了", "受不了", "滚", "闭嘴"},
	"好奇探索": {"好奇", "想知道", "有没有", "是不是", "真的吗", "可能吗"},

	// === 其他 ===
	"健康医疗": {"健康", "生病", "医院", "医生", "药", "锻炼", "体检", "减肥", "健身", "睡眠", "饮食", "养生", "中医"},
	"社交人际": {"朋友", "家人", "同事", "老板", "客户", "关系", "恋爱", "约会", "婚姻", "孩子", "父母"},
	"时间管理": {"时间", "日程", "计划", "安排", "提醒", "闹钟", "待办", "todo", "日历"},
	"完美主义": {"完美", "精确", "准确无误", "零缺陷", "一丝不苟", "细节", "强迫症", "追求完美", "尽善尽美"},
	"急躁冲动": {"受不了", "等不及", "立刻", "马上", "速度", "快点", "烦死了", "急死了", "暴躁", "火大"},
	"谨慎保守": {"小心", "注意", "谨慎", "稳妥", "安全第一", "风险", "害怕", "担心", "万一", "保险"},
	"乐观积极": {"没问题", "小事", "加油", "努力", "相信", "希望", "美好", "开心", "快乐", "正能量"},
	"悲观消极": {"完蛋", "没希望", "不行", "做不到", "太难了", "放弃", "算了", "随便", "无所谓", "躺平"},
	"好奇求知": {"为什么", "怎么回事", "原理", "原因", "探索", "发现", "新鲜", "有趣", "神奇", "奥秘"},
	"社交外向": {"朋友", "聚会", "活动", "社交", "聊天", "分享", "认识", "一起", "约", "见面"},
	"内向独处": {"一个人", "独自", "安静", "独处", "不想说话", "宅", "在家", "自己", "孤独"},
	"自律严谨": {"计划", "规律", "习惯", "坚持", "每天", "按时", "准时", "纪律", "克制", "自律"},
	"随性自由": {"随意", "随便", "看心情", "再说", "无所谓", "自由", "随缘", "佛系", "不着急"},
	"争强好胜": {"赢", "第一", "最强", "打败", "超越", "比", "竞争", "排名", "成绩", "厉害"},
	"佛系淡定": {"随缘", "看开", "无所谓", "不强求", "放下", "算了", "都可以", "没事", "淡定"},

	// === 职业/角色类 ===
	"软件工程师":     {"后端", "前端", "全栈", "api", "数据库", "算法", "架构", "重构", "debug", "crud", "程序员", "码农"},
	"DevOps工程师": {"部署", "运维", "ci/cd", "容器", "k8s", "监控", "日志", "自动化", "发布", "上线"},
	"数据科学家":     {"数据挖掘", "机器学习", "统计", "模型", "特征", "训练", "预测", "分析", "python", "pandas"},
	"产品经理":      {"需求", "原型", "用户", "体验", "迭代", "优先级", "roadmap", "prd", "axure", "用户故事"},
	"项目经理":      {"进度", "风险", "资源", "里程碑", "deadline", "交付", "排期", "协调", "干系人"},
	"设计师":       {"设计", "ui", "ux", "视觉", "交互", "figma", "sketch", "色彩", "布局", "图标", "品牌"},
	"测试工程师":     {"测试", "bug", "质量", "自动化测试", "用例", "回归", "性能测试", "压力测试", "qa"},
	"运维工程师":     {"服务器", "网络", "linux", "监控", "告警", "备份", "容灾", "高可用", "负载"},
	"技术架构师":     {"架构", "技术选型", "高并发", "分布式", "微服务", "消息队列", "缓存", "数据库设计"},
	"创业者":       {"创业", "融资", "投资人", "bp", "商业模式", "增长", "获客", "盈利", "初创", "founder"},
	"管理者":       {"管理", "领导", "团队", "下属", "绩效", "kpi", "okr", "招聘", "面试", "培养"},
	"销售":        {"销售", "客户", "签单", "业绩", "提成", "拜访", "谈判", "报价", "合同", "客户关系"},
	"市场运营":      {"运营", "增长", "获客", "转化", "留存", "活动", "内容", "社群", "用户", "数据分析"},
	"财务人员":      {"财务", "会计", "报表", "税务", "报销", "预算", "审计", "账目", "发票", "成本"},
	"人力资源":      {"招聘", "面试", "培训", "绩效", "薪酬", "福利", "员工关系", "hr", "人事", "考勤"},
	"法务":        {"合同", "法律", "合规", "知识产权", "专利", "版权", "诉讼", "风险", "条款", "协议"},
	"学生":        {"学习", "考试", "作业", "论文", "毕业", "考研", "留学", "上课", "老师", "同学", "分数"},
	"教师":        {"教学", "学生", "课程", "备课", "讲课", "作业", "考试", "成绩", "教育", "培训"},
	"研究员":       {"研究", "论文", "实验", "数据", "学术", "期刊", "会议", "课题", "基金", "实验室"},
	"自由职业者":     {"自由", "接单", "客户", "远程", "在家", "时间自由", "项目", "收款", "freelance"},
	"咨询顾问":      {"咨询", "顾问", "方案", "客户", "分析", "建议", "报告", "ppt", " presentation"},

	// === 关注领域/兴趣类 ===
	"开源关注": {"开源", "github", "开源项目", "贡献", "pr", "issue", "开源协议", "社区"},
	"技术前沿": {"新技术", "前沿", "趋势", "未来", "创新", "颠覆", "黑科技", "探索"},
	"职业发展": {"职业", "发展", "晋升", "跳槽", "加薪", "offer", "面试", "简历", "职业规划", "成长"},
	"副业赚钱": {"副业", "赚钱", "兼职", "被动收入", "搞钱", "变现", "流量", "电商", "自媒体"},
	"投资理财": {"理财", "投资", "收益", "回报", "资产", "被动收入", "财务自由", "复利", "定投"},
	"创业思考": {"创业想法", "痛点", "机会", "市场", "竞争", "差异化", "mvp", "验证"},
	"效率提升": {"效率", "生产力", "工具", "自动化", "快捷", "省时", "优化", "提效"},
	"知识管理": {"知识库", "笔记", "obsidian", "notion", "印象笔记", "整理", "分类", "标签"},
	"健康生活": {"健康", "养生", "饮食", "运动", "睡眠", "体检", "亚健康", "康复"},
	"极简主义": {"极简", "断舍离", "精简", "收纳", "整理", "清爽", "简约", "less"},
	"环保公益": {"环保", "回收", "低碳", "可持续", "公益", "捐赠", "志愿者", "绿色"},

	// === 沟通风格类 ===
	"礼貌客气":  {"请问", "谢谢", "感谢", "麻烦", "不好意思", "抱歉", "辛苦了", "您好", "请"},
	"直接粗暴":  {"直接说", "简单点", "废话少说", "快点", "别墨迹", "讲重点", "干货"},
	"情绪化表达": {"!!!", "啊啊啊", "崩溃", "疯了", "哭了", "笑死", "哈哈哈", "呜呜"},
	"理性分析":  {"分析", "逻辑", "原因", "数据", "依据", "推理", "论证", "结论", "事实"},
	"感性表达":  {"感觉", "觉得", "好像", "也许", "大概", "似乎", "可能", "希望"},

	// === 特殊关注类 ===
	"安全隐私": {"隐私", "安全", "数据保护", "加密", "匿名", "泄露", "权限", "追踪"},
	"合规规范": {"规范", "标准", "合规", "流程", "制度", "规定", "要求", "必须"},
	"用户体验": {"体验", "易用", "友好", "流畅", "简洁", "美观", "好用", "舒服"},
	"性能优化": {"性能", "优化", "速度", "响应", "延迟", "内存", "cpu", "卡顿", "慢"},
	"可扩展性": {"扩展", "伸缩", "弹性", "容量", "未来", "增长", "scale"},
}

func extractSimpleTopics(text string) []string {
	// 简单实现，实际可以用分词库
	text = strings.ToLower(text)
	topics := []string{}
	seen := make(map[string]bool)

	for topic, keywords := range topicDict {
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				if !seen[topic] {
					seen[topic] = true
					topics = append(topics, topic)
				}
				break // 每个主题只添加一次
			}
		}
	}

	return topics
}

func extractInsightsFromFeedback(feedback string) []string {
	insights := []string{}
	// 简单实现，实际可以用 NLP 分析
	if len(feedback) > 20 {
		insights = append(insights, feedback)
	}
	return insights
}

func (m *MemoryStore) GetMemoryContext(userName, sessionID, currentQuestion string) string {
	return m.GetMemoryContextForTenant(defaultExecutionTenantID, userName, sessionID, currentQuestion)
}

func (m *MemoryStore) GetMemoryContextForTenant(tenantID, userName, sessionID, currentQuestion string) string {
	tenantID = memoryTenantID(tenantID)
	var context strings.Builder

	// 1. 用户特别偏好和约束（只有非默认值才展示）
	if userName != "" {
		profile, err := m.GetUserProfileForTenant(tenantID, userName)
		if err == nil && profile != nil {
			// 检查是否有实际约束
			var hasConstraints bool
			var constraints map[string]interface{}
			artifactPreferences, artifactPreferencesErr := m.GetArtifactFormatPreferencesForTenant(tenantID, userName)
			if profile.Constraints != nil {
				json.Unmarshal(profile.Constraints, &constraints)
				if len(constraints) > 0 {
					hasConstraints = true
				}
			}
			if artifactPreferencesErr == nil && len(artifactPreferences) > 0 {
				hasConstraints = true
			}

			// 检查是否有正面模式
			var positivePatterns []string
			if profile.PositivePatterns != nil {
				json.Unmarshal(profile.PositivePatterns, &positivePatterns)
				if len(positivePatterns) > 0 {
					hasConstraints = true
				}
			}

			// 非默认回答风格
			hasStyle := profile.ResponseStyle != "" && profile.ResponseStyle != "balanced"

			if hasConstraints || hasStyle {
				context.WriteString("## 👤 用户偏好\n\n")

				if hasStyle {
					styleDesc := map[string]string{
						"concise":  "喜欢简洁回答",
						"detailed": "喜欢详细回答",
					}
					if desc, ok := styleDesc[profile.ResponseStyle]; ok {
						context.WriteString(fmt.Sprintf("- %s\n", desc))
					}
				}

				if forbidden, ok := constraints["forbidden"].([]interface{}); ok && len(forbidden) > 0 {
					context.WriteString(fmt.Sprintf("- 避免使用: %s\n", strings.Join(toStringSlice(forbidden), "、")))
				}
				if preferred, ok := constraints["preferred"].([]interface{}); ok && len(preferred) > 0 {
					context.WriteString(fmt.Sprintf("- 优先使用: %s\n", strings.Join(toStringSlice(preferred), "、")))
				}
				if formattedPreferences := FormatArtifactFormatPreferences(artifactPreferences); formattedPreferences != "" {
					context.WriteString(formattedPreferences + "\n")
				}

				if len(positivePatterns) > 0 {
					context.WriteString(fmt.Sprintf("- 认可: %s\n", positivePatterns[0]))
				}

				context.WriteString("\n")
			}
		}
	}

	// 2. 当前会话历史（最近几轮）
	if sessionID != "" {
		var recentEvents []MemoryEvent
		recentQuery := m.db.Where("tenant_id = ? AND agent_name = ? AND session_id = ?", tenantID, m.agentName, sessionID)
		if userName != "" {
			recentQuery = recentQuery.Where("user_name = ?", userName)
		}
		err := recentQuery.
			Order("created_at DESC").
			Limit(3).
			Find(&recentEvents).Error

		if err == nil && len(recentEvents) > 0 {
			var recent strings.Builder
			// 只保留最近2-3轮
			for i := len(recentEvents) - 1; i >= 0; i-- {
				event := recentEvents[i]
				question := sessioncore.CleanCarryoverContent("user", event.UserQuestion, 400)
				answer := sessioncore.CleanCarryoverContent("assistant", event.FinalAnswer, 200)
				if question == "" && answer == "" {
					continue
				}
				if question != "" {
					recent.WriteString(fmt.Sprintf("用户: %s\n", question))
				}
				if answer != "" {
					recent.WriteString(fmt.Sprintf("助手: %s\n", answer))
				}
				recent.WriteString("\n")
			}
			if strings.TrimSpace(recent.String()) != "" {
				context.WriteString("## 💬 最近对话\n\n")
				context.WriteString(recent.String())
			}
		}
	}

	// 3. 工具/技能统计（只展示成功率低的或有问题的）
	var problemTools []string
	var goodTools []string

	var toolStats []ToolSkillStat
	err := m.db.Where("tenant_id = ? AND agent_name = ? AND total_count >= ?",
		tenantID, m.agentName, 3).
		Find(&toolStats).Error

	if err == nil && len(toolStats) > 0 {
		for _, stat := range toolStats {
			if stat.SuccessRate < 50 {
				problemTools = append(problemTools, fmt.Sprintf("%s(%.0f%%)", stat.Name, stat.SuccessRate))
			} else if stat.SuccessRate > 80 && stat.TotalCount >= 5 {
				goodTools = append(goodTools, stat.Name)
			}
		}

		if len(problemTools) > 0 || len(goodTools) > 0 {
			context.WriteString("## 🔧 工具使用提示\n\n")
			if len(problemTools) > 0 {
				context.WriteString(fmt.Sprintf("- 需谨慎: %s\n", strings.Join(problemTools, "、")))
			}
			if len(goodTools) > 0 {
				context.WriteString(fmt.Sprintf("- 效果较好: %s\n", strings.Join(goodTools[:min(3, len(goodTools))], "、")))
			}
			context.WriteString("\n")
		}
	}

	// 4. 相似历史问题（FTS + lexical + optional vector，跨 session 召回）
	if userName != "" && currentQuestion != "" {
		// Fetch beyond the final one-item context budget so several matches from
		// the current session cannot hide a valid older-session memory.
		results := m.SearchMemoryHybridForTenant(tenantID, currentQuestion, userName, 20)
		for _, r := range results {
			// 跳过当前 session 自己的结果（避免自引用）
			if sessionID != "" && r.SessionID == sessionID {
				continue
			}
			context.WriteString("## 📌 参考\n\n")
			context.WriteString(fmt.Sprintf("之前有类似问题「%s」，处理方式: %s。请检查当前状态是否需要重新处理，如交付物已丢失或用户明确要求重做，请重新执行。\n\n",
				textutil.Truncate(r.UserQuestion, 50),
				textutil.Truncate(r.FinalAnswer, 150)))
			break // 只取最相关的一条
		}
	}

	// 5. 相关知识（只取最相关的1-2条）
	if currentQuestion != "" {
		keywords := extractSimpleKeywords(currentQuestion)
		if len(keywords) > 0 {
			var knowledgeList []Knowledge
			var conds []string
			var args []interface{}
			for _, kw := range keywords[:min(2, len(keywords))] {
				conds = append(conds, "title LIKE ? OR summary LIKE ?")
				args = append(args, "%"+kw+"%", "%"+kw+"%")
			}

			err := m.db.Where("tenant_id = ? AND agent_name = ?", tenantID, m.agentName).
				Where("("+strings.Join(conds, " OR ")+")", args...).
				Order("importance DESC").
				Limit(2).
				Find(&knowledgeList).Error

			if err == nil && len(knowledgeList) > 0 {
				context.WriteString("## 📚 相关知识\n\n")
				for _, k := range knowledgeList {
					context.WriteString(fmt.Sprintf("- %s: %s\n", k.Title, textutil.Truncate(k.Summary, 80)))
				}
				context.WriteString("\n")
			}
		}
	}

	return context.String()
}

func findEventIndex(events []MemoryEvent, target MemoryEvent) int {
	for i, e := range events {
		if e.ID == target.ID {
			return i
		}
	}
	return -1
}

func (m *MemoryStore) getSimpleSolution(event MemoryEvent) string {
	// 从调用链提取工具名
	var chain []map[string]interface{}
	if err := json.Unmarshal(event.CallChain, &chain); err == nil && len(chain) > 0 {
		var tools []string
		for _, step := range chain {
			if name, ok := step["name"].(string); ok {
				tools = append(tools, name)
			}
		}
		if len(tools) > 0 {
			return fmt.Sprintf("使用了 %s", strings.Join(tools, " → "))
		}
	}

	// 从回答中提取
	if event.FinalAnswer != "" {
		return textutil.Truncate(event.FinalAnswer, 60)
	}

	return "已处理"
}

// getFeedbackSummary 获取反馈摘要
func (m *MemoryStore) getFeedbackSummary(event MemoryEvent) string {
	if event.UserRating != nil {
		if *event.UserRating >= 4 {
			return "用户满意"
		} else if *event.UserRating <= 2 {
			return "用户不满意"
		}
	}
	return ""
}

// extractSimpleKeywords 简单提取关键词
func extractSimpleKeywords(text string) []string {
	// 去除标点，转小写
	text = strings.ToLower(text)
	punctuations := []string{"?", "？", "!", "！", ".", "。", ",", "，", ":", "：", ";", "；", "\"", "'"}
	for _, p := range punctuations {
		text = strings.ReplaceAll(text, p, " ")
	}

	words := strings.Fields(text)

	// 常见停用词
	stopWords := map[string]bool{
		"的": true, "了": true, "是": true, "在": true, "和": true, "与": true,
		"我": true, "你": true, "他": true, "这": true, "那": true, "有": true,
		"帮": true, "请": true, "问": true, "想": true, "要": true, "能": true,
		"a": true, "an": true, "the": true, "is": true, "are": true, "what": true,
		"how": true, "why": true, "help": true, "please": true,
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

	// 最多3个关键词
	if len(unique) > 3 {
		unique = unique[:3]
	}

	return unique
}

func toStringSlice(arr []interface{}) []string {
	result := make([]string, len(arr))
	for i, v := range arr {
		if s, ok := v.(string); ok {
			result[i] = s
		}
	}
	return result
}

// ====================== Memory Vector Search ======================

var memoryEmbedEnabled atomic.Bool

// SetMemoryEmbed enables vector-based memory search.
func SetMemoryEmbed(mode string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	validMode := mode == string(knowledgetools.EmbeddingModeAPI) ||
		mode == string(knowledgetools.EmbeddingModeOllama) ||
		mode == string(knowledgetools.EmbeddingModeLocalLlama) ||
		mode == string(knowledgetools.EmbeddingModeLocalONNX)
	backendMode := strings.ToLower(strings.TrimSpace(string(knowledgetools.CurrentEmbeddingMode())))
	if validMode && mode == backendMode && knowledgetools.EmbedderEnabled() {
		memoryEmbedEnabled.Store(true)
		log.Printf("[Memory] Vector search enabled (mode=%s)", mode)
	} else {
		memoryEmbedEnabled.Store(false)
		log.Printf("[Memory] Vector search disabled (mode=%s)", mode)
	}
}

// memoryEmbedCache stores embeddings for recent memory entries.
var (
	memoryEmbedCache   = map[string][]float64{} // key: memory text hash
	memoryEmbedCacheMu sync.Mutex
)

// SearchMemoryVector searches memory using embedding similarity.
// Falls back to empty result if embedder not available.
func (m *MemoryStore) SearchMemoryVector(query string, userName string, limit int) []memoryFTSResult {
	return m.SearchMemoryVectorForTenant(defaultExecutionTenantID, query, userName, limit)
}

func (m *MemoryStore) SearchMemoryVectorForTenant(tenantID, query string, userName string, limit int) []memoryFTSResult {
	if !memoryEmbedEnabled.Load() || !knowledgetools.EmbedderEnabled() || strings.TrimSpace(query) == "" {
		return nil
	}
	// Get recent memories
	var events []MemoryEvent
	q := m.db.Where("tenant_id = ? AND agent_name = ?", memoryTenantID(tenantID), m.agentName)
	if userName != "" {
		q = q.Where("user_name = ?", userName)
	}
	q.Order("created_at DESC").Limit(50).Find(&events)
	if len(events) == 0 {
		return nil
	}

	// Embed query
	queryVec, err := knowledgetools.EmbedText(context.Background(), query)
	if err != nil {
		return nil
	}

	type scoredResult struct {
		result memoryFTSResult
		score  float64
	}
	var scored []scoredResult
	for _, e := range events {
		text := e.UserQuestion + " " + e.FinalAnswer
		if text == "" {
			continue
		}
		text = textutil.TruncateRunes(text, 2000)
		// Cache embedding
		memoryEmbedCacheMu.Lock()
		vec, ok := memoryEmbedCache[text]
		if !ok {
			var err error
			vec, err = knowledgetools.EmbedText(context.Background(), text)
			if err != nil {
				memoryEmbedCacheMu.Unlock()
				continue
			}
			memoryEmbedCache[text] = vec
			if len(memoryEmbedCache) > 200 { // evict oldest
				for k := range memoryEmbedCache {
					delete(memoryEmbedCache, k)
					break
				}
			}
		}
		memoryEmbedCacheMu.Unlock()
		sim := knowledgetools.CosineSimilarity(queryVec, vec)
		if sim > 0.3 {
			scored = append(scored, scoredResult{
				result: memoryFTSResult{
					ID:           int(e.ID),
					UserQuestion: e.UserQuestion,
					FinalAnswer:  e.FinalAnswer, LearnNote: e.LearnNote,
					SessionID: e.SessionID, UserName: e.UserName, Score: sim,
					ScoreKind: memoryScoreVector, MatchMethods: []string{memoryMatchVector},
				},
				score: sim,
			})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]memoryFTSResult, len(scored))
	for i, s := range scored {
		results[i] = s.result
	}
	return results
}

// SearchMemoryLexical provides a bounded deterministic fallback for text that
// FTS5's unicode61 tokenizer cannot partially match, especially Chinese prose
// stored as one long token. It ranks by distinctive query terms found in the
// original question/answer and never scans more than the most recent 200 rows.
func (m *MemoryStore) SearchMemoryLexicalForTenant(tenantID, query, userName string, limit int) []memoryFTSResult {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	var events []MemoryEvent
	dbQuery := m.db.Where("tenant_id = ? AND agent_name = ?", memoryTenantID(tenantID), m.agentName)
	if userName != "" {
		dbQuery = dbQuery.Where("user_name = ?", userName)
	}
	if err := dbQuery.Order("created_at DESC, id DESC").Limit(memoryLexicalScanMax).Find(&events).Error; err != nil {
		return nil
	}
	terms := memoryLexicalTerms(query)
	if len(terms) == 0 {
		return nil
	}
	type scoredResult struct {
		result memoryFTSResult
		score  float64
		order  int
	}
	scored := make([]scoredResult, 0, len(events))
	for index, event := range events {
		question := strings.ToLower(event.UserQuestion)
		answer := strings.ToLower(event.FinalAnswer + " " + event.LearnNote)
		score := 0.0
		matched := 0
		for _, term := range terms {
			term = strings.ToLower(term)
			if strings.Contains(question, term) {
				score += 3.0 + float64(len([]rune(term)))/10.0
				matched++
			} else if strings.Contains(answer, term) {
				score += 1.0 + float64(len([]rune(term)))/20.0
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		coverage := float64(matched) / float64(len(terms))
		// A single short shared word is too weak when the query contains several
		// distinct concepts. A long/specific term may stand on its own.
		if coverage < 0.25 && score < 3.6 {
			continue
		}
		score *= 0.5 + coverage
		scored = append(scored, scoredResult{
			result: memoryFTSResult{
				ID: int(event.ID), UserQuestion: event.UserQuestion,
				FinalAnswer: event.FinalAnswer, LearnNote: event.LearnNote,
				SessionID: event.SessionID, UserName: event.UserName,
				Score: score, ScoreKind: memoryMatchLexical, MatchMethods: []string{memoryMatchLexical},
			},
			score: score,
			order: index,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].order < scored[j].order
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]memoryFTSResult, len(scored))
	for i := range scored {
		results[i] = scored[i].result
	}
	return results
}

func memoryLexicalTerms(query string) []string {
	terms := make([]string, 0, 12)
	seen := make(map[string]bool)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || ragmodel.IsStopToken(term) || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, token := range ragmodel.GoJiebaCutSearch(strings.ToLower(query)) {
		add(token)
		if len(terms) >= 12 {
			return terms
		}
	}
	for _, token := range memorySearchTokens(query, 12) {
		add(token)
		if len(terms) >= 12 {
			break
		}
	}
	return terms
}

// SearchMemoryHybrid combines FTS5 keyword + vector semantic search via Reciprocal Rank Fusion.
// More robust than either method alone: FTS protects against cold vector cache,
// vector catches semantic matches that keyword misses. RRF k=60 (standard).
func (m *MemoryStore) SearchMemoryHybrid(query string, userName string, limit int) []memoryFTSResult {
	return m.SearchMemoryHybridForTenant(defaultExecutionTenantID, query, userName, limit)
}

func (m *MemoryStore) SearchMemoryHybridForTenant(tenantID, query string, userName string, limit int) []memoryFTSResult {
	if limit <= 0 {
		limit = 5
	}
	// Merge independent rankers. Vector recall is optional and falls back to
	// FTS + bounded lexical matching when no embedder is configured.
	type result struct {
		id           int
		userQuestion string
		finalAnswer  string
		learnNote    string
		sessionID    string
		userName     string
		ftsRank      int // 0 = not found
		lexicalRank  int // 0 = not found
		vectorRank   int // 0 = not found
		matchMethods []string
	}

	ftsResults := m.SearchMemoryFTSForTenant(tenantID, query, userName, 20)
	lexicalResults := m.SearchMemoryLexicalForTenant(tenantID, query, userName, 20)
	vectorResults := m.SearchMemoryVectorForTenant(tenantID, query, userName, 20)

	// Deduplicate by userQuestion + finalAnswer
	seen := make(map[string]*result)
	var order []string

	// Index FTS results
	for i, r := range ftsResults {
		key := memoryRetrievalKey(r.ID, r.UserQuestion, r.FinalAnswer)
		if _, ok := seen[key]; !ok {
			seen[key] = &result{
				id: r.ID, userQuestion: r.UserQuestion, finalAnswer: r.FinalAnswer,
				learnNote: r.LearnNote, sessionID: r.SessionID, userName: r.UserName,
				ftsRank: i + 1, matchMethods: []string{memoryMatchFTS},
			}
			order = append(order, key)
		}
	}

	// Merge deterministic lexical fallback results.
	for i, r := range lexicalResults {
		key := memoryRetrievalKey(r.ID, r.UserQuestion, r.FinalAnswer)
		if existing, ok := seen[key]; ok {
			existing.lexicalRank = i + 1
			existing.matchMethods = appendMemoryMatchMethod(existing.matchMethods, memoryMatchLexical)
		} else {
			seen[key] = &result{
				id: r.ID, userQuestion: r.UserQuestion, finalAnswer: r.FinalAnswer,
				learnNote: r.LearnNote, sessionID: r.SessionID, userName: r.UserName,
				lexicalRank: i + 1, matchMethods: []string{memoryMatchLexical},
			}
			order = append(order, key)
		}
	}

	// Index vector results (merge if already seen)
	for i, r := range vectorResults {
		key := memoryRetrievalKey(r.ID, r.UserQuestion, r.FinalAnswer)
		if existing, ok := seen[key]; ok {
			existing.vectorRank = i + 1
			existing.matchMethods = appendMemoryMatchMethod(existing.matchMethods, memoryMatchVector)
		} else {
			seen[key] = &result{
				id: r.ID, userQuestion: r.UserQuestion, finalAnswer: r.FinalAnswer,
				learnNote: r.LearnNote, sessionID: r.SessionID, userName: r.UserName,
				vectorRank: i + 1, matchMethods: []string{memoryMatchVector},
			}
			order = append(order, key)
		}
	}

	if len(order) == 0 {
		return nil
	}

	// RRF score: sum(1 / (k + rank_i)) for each ranking method
	const k = 60.0
	rrfScores := make(map[string]float64)
	for _, key := range order {
		r := seen[key]
		score := 0.0
		if r.ftsRank > 0 {
			score += 1.0 / (k + float64(r.ftsRank))
		}
		if r.lexicalRank > 0 {
			score += 1.0 / (k + float64(r.lexicalRank))
		}
		if r.vectorRank > 0 {
			score += 1.0 / (k + float64(r.vectorRank))
		}
		rrfScores[key] = score
	}

	// Sort by RRF score descending
	sort.Slice(order, func(i, j int) bool {
		return rrfScores[order[i]] > rrfScores[order[j]]
	})

	if limit > len(order) {
		limit = len(order)
	}

	results := make([]memoryFTSResult, 0, limit)
	for i := 0; i < limit; i++ {
		r := seen[order[i]]
		results = append(results, memoryFTSResult{
			ID:           r.id,
			UserQuestion: r.userQuestion,
			FinalAnswer:  r.finalAnswer,
			LearnNote:    r.learnNote,
			SessionID:    r.sessionID,
			UserName:     r.userName,
			Score:        rrfScores[order[i]],
			ScoreKind:    memoryScoreRRF,
			MatchMethods: append([]string(nil), r.matchMethods...),
		})
	}
	return results
}

func appendMemoryMatchMethod(methods []string, method string) []string {
	for _, existing := range methods {
		if existing == method {
			return methods
		}
	}
	return append(methods, method)
}

func memoryRetrievalKey(id int, question, answer string) string {
	if id > 0 {
		return fmt.Sprintf("id:%d", id)
	}
	return question + "|||" + answer
}

func (m *MemoryStore) SearchMemoryEvents(query, userID string, limit int) ([]MemoryEvent, error) {
	return m.SearchMemoryEventsForTenant(defaultExecutionTenantID, query, userID, limit)
}

func (m *MemoryStore) SearchMemoryEventsForTenant(tenantID, query, userID string, limit int) ([]MemoryEvent, error) {
	var events []MemoryEvent

	dbQuery := m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ?", memoryTenantID(tenantID), m.agentName)

	if userID != "" {
		dbQuery = dbQuery.Where("user_id = ?", userID)
	}

	if query != "" {
		keywords := extractSimpleKeywords(query)
		if len(keywords) > 0 {
			conditions := m.db.Where("user_question LIKE ? OR final_answer LIKE ?",
				"%"+keywords[0]+"%", "%"+keywords[0]+"%")
			for _, kw := range keywords[1:] {
				conditions = conditions.Or("user_question LIKE ? OR final_answer LIKE ?",
					"%"+kw+"%", "%"+kw+"%")
			}
			dbQuery = dbQuery.Where(conditions)
		}
	}

	err := dbQuery.Order("created_at DESC").Limit(limit).Find(&events).Error
	return events, err
}

func (m *MemoryStore) GetToolSkillStats(typ string, minUsage int, limit int) ([]ToolSkillStat, error) {
	var stats []ToolSkillStat

	dbQuery := m.db.Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, m.agentName)
	if typ != "" {
		dbQuery = dbQuery.Where("type = ?", typ)
	}
	if minUsage > 0 {
		dbQuery = dbQuery.Where("total_count >= ?", minUsage)
	}

	err := dbQuery.Order("success_rate DESC").Limit(limit).Find(&stats).Error
	return stats, err
}

// GetKnowledgeByTags 按标签获取知识
func (m *MemoryStore) GetKnowledgeByTags(tags []string, limit int) ([]Knowledge, error) {
	var knowledge []Knowledge

	query := m.db.Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, m.agentName)
	if len(tags) > 0 {
		tagQuery := m.db.Where("tags LIKE ?", "%"+tags[0]+"%")
		for _, tag := range tags[1:] {
			tagQuery = tagQuery.Or("tags LIKE ?", "%"+tag+"%")
		}
		query = query.Where(tagQuery)
	}

	err := query.Order("importance DESC, created_at DESC").Limit(limit).Find(&knowledge).Error
	return knowledge, err
}

// GetKnowledgeByCategory 按类别获取知识
func (m *MemoryStore) GetKnowledgeByCategory(category string, limit int) ([]Knowledge, error) {
	var knowledge []Knowledge
	err := m.db.Where("tenant_id = ? AND agent_name = ? AND category = ?",
		defaultExecutionTenantID, m.agentName, category).
		Order("importance DESC, created_at DESC").
		Limit(limit).
		Find(&knowledge).Error
	return knowledge, err
}

// IncrementKnowledgeUseCount 增加知识使用次数
func (m *MemoryStore) IncrementKnowledgeUseCount(knowledgeID string) error {
	return m.db.Model(&Knowledge{}).
		Where("tenant_id = ? AND knowledge_id = ?", defaultExecutionTenantID, knowledgeID).
		Update("use_count", gorm.Expr("use_count + 1")).Error
}

// GetRecentEventsByAgent 获取Agent最近事件
func (m *MemoryStore) GetRecentEventsByAgent(agentName string, limit int) ([]MemoryEvent, error) {
	var events []MemoryEvent
	err := m.db.Where("tenant_id = ? AND agent_name = ?", defaultExecutionTenantID, agentName).
		Order("created_at DESC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

// GetEventStatsByDate 按日期获取事件统计
func (m *MemoryStore) GetEventStatsByDate(startDate, endDate time.Time) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var totalEvents int64
	m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ? AND created_at BETWEEN ? AND ?",
			defaultExecutionTenantID, m.agentName, startDate, endDate).
		Count(&totalEvents)
	stats["total_events"] = totalEvents

	var successEvents int64
	m.db.Model(&MemoryEvent{}).
		Where("tenant_id = ? AND agent_name = ? AND status = ? AND created_at BETWEEN ? AND ?",
			defaultExecutionTenantID, m.agentName, "success", startDate, endDate).
		Count(&successEvents)

	if totalEvents > 0 {
		stats["success_rate"] = float64(successEvents) / float64(totalEvents) * 100
	} else {
		stats["success_rate"] = 0
	}

	// 按工具统计
	var toolStats []ToolSkillStat
	m.db.Where("tenant_id = ? AND agent_name = ? AND type = ?",
		defaultExecutionTenantID, m.agentName, "tool").
		Find(&toolStats)
	stats["tool_stats"] = toolStats

	return stats, nil
}

func (m *MemoryStore) GetUserConstraints(userName string) (map[string]interface{}, error) {
	profile, err := m.GetUserProfile(userName)
	if err != nil {
		return nil, err
	}

	var constraints map[string]interface{}
	if profile.Constraints != nil {
		json.Unmarshal(profile.Constraints, &constraints)
	}

	return constraints, nil
}

func (m *MemoryStore) AddUserConstraint(userName, constraintType, value string) error {
	return m.AddUserConstraintForTenant(defaultExecutionTenantID, userName, constraintType, value)
}

func (m *MemoryStore) AddUserConstraintForTenant(tenantID, userName, constraintType, value string) error {
	profile, err := m.getOrCreateUserProfileForTenant(tenantID, userName)
	if err != nil {
		return err
	}

	var constraints map[string]interface{}
	if profile.Constraints != nil {
		if err := json.Unmarshal(profile.Constraints, &constraints); err != nil {
			return fmt.Errorf("decode user constraints: %w", err)
		}
	}
	if constraints == nil {
		constraints = make(map[string]interface{})
	}

	var list []string
	if existing, ok := constraints[constraintType].([]interface{}); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				list = append(list, s)
			}
		}
	}

	// 去重添加
	found := false
	for _, v := range list {
		if v == value {
			found = true
			break
		}
	}
	if !found {
		list = append(list, value)
	}

	constraints[constraintType] = list
	// A newly preferred tool is no longer forbidden, and vice versa.
	opposite := ""
	if constraintType == "forbidden" {
		opposite = "preferred"
	} else if constraintType == "preferred" {
		opposite = "forbidden"
	}
	if opposite != "" {
		if existing, ok := constraints[opposite].([]interface{}); ok {
			filtered := make([]interface{}, 0, len(existing))
			for _, item := range existing {
				if item != value {
					filtered = append(filtered, item)
				}
			}
			constraints[opposite] = filtered
		}
	}
	constraintsJSON, _ := json.Marshal(constraints)
	profile.Constraints = constraintsJSON

	return m.db.Save(profile).Error
}

func (m *MemoryStore) GetRecentTopics(userName string, limit int) (map[string]int, error) {
	profile, err := m.GetUserProfile(userName)
	if err != nil {
		return nil, err
	}

	var topics map[string]int
	if profile.TopicFrequency != nil {
		json.Unmarshal(profile.TopicFrequency, &topics)
	}

	return topics, nil
}

// GetSuccessfulPatterns 获取成功的工具模式
func (m *MemoryStore) GetSuccessfulPatterns(limit int) ([]string, error) {
	var patterns []string

	// 查询成功的记忆事件
	var events []MemoryEvent
	err := m.db.Where("tenant_id = ? AND agent_name = ? AND status = ? AND call_chain IS NOT NULL",
		defaultExecutionTenantID, m.agentName, "success").
		Order("created_at DESC").
		Limit(limit).
		Find(&events).Error
	if err != nil {
		return nil, err
	}

	for _, event := range events {
		var chain []map[string]interface{}
		if err := json.Unmarshal(event.CallChain, &chain); err == nil && len(chain) > 0 {
			var tools []string
			for _, step := range chain {
				if name, ok := step["name"].(string); ok {
					tools = append(tools, name)
				}
			}
			if len(tools) > 0 {
				patterns = append(patterns, strings.Join(tools, " → "))
			}
		}
	}

	return patterns, nil
}

func extractTopics(text string) []string {
	text = strings.ToLower(text)
	topics := []string{}
	seen := make(map[string]bool)

	// 按主题类别顺序匹配，优先级高的先匹配
	for topic, keywords := range topicDict {
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				if !seen[topic] {
					seen[topic] = true
					topics = append(topics, topic)
				}
				break
			}
		}
	}

	return topics
}

func updatePersonalityTraits(profile *UserProfile, topics []string) {
	var traits map[string]int
	if len(profile.PersonalityTraits) > 0 {
		json.Unmarshal(profile.PersonalityTraits, &traits)
	}
	if traits == nil {
		traits = make(map[string]int)
	}

	// 性格特质映射
	personalityMap := map[string][]string{
		"完美主义": {"完美主义"},
		"急躁冲动": {"急躁冲动"},
		"谨慎保守": {"谨慎保守"},
		"乐观积极": {"乐观积极"},
		"悲观消极": {"悲观消极"},
		"好奇求知": {"好奇求知"},
		"社交外向": {"社交外向"},
		"内向独处": {"内向独处"},
		"自律严谨": {"自律严谨"},
		"随性自由": {"随性自由"},
		"争强好胜": {"争强好胜"},
		"佛系淡定": {"佛系淡定"},
	}

	for _, topic := range topics {
		for trait, keywords := range personalityMap {
			for _, kw := range keywords {
				if topic == kw {
					traits[trait]++
					break
				}
			}
		}
	}

	profile.PersonalityTraits, _ = json.Marshal(traits)
}

// updateCareerAffinity 更新职业倾向
func updateCareerAffinity(profile *UserProfile, topics []string) {
	var career map[string]int
	if len(profile.CareerAffinity) > 0 {
		json.Unmarshal(profile.CareerAffinity, &career)
	}
	if career == nil {
		career = make(map[string]int)
	}

	careerMap := map[string][]string{
		"软件工程师":     {"软件工程师", "编程开发", "后端开发", "前端开发"},
		"DevOps工程师": {"DevOps工程师", "DevOps运维"},
		"数据科学家":     {"数据科学家", "数据分析", "数据科学"},
		"产品经理":      {"产品经理", "产品管理"},
		"项目经理":      {"项目经理", "项目管理"},
		"设计师":       {"设计师"},
		"测试工程师":     {"测试工程师"},
		"运维工程师":     {"运维工程师"},
		"技术架构师":     {"技术架构师"},
		"创业者":       {"创业者"},
		"管理者":       {"管理者"},
		"销售":        {"销售"},
		"市场运营":      {"市场运营"},
		"财务人员":      {"财务人员"},
		"人力资源":      {"人力资源"},
		"学生":        {"学生"},
		"教师":        {"教师"},
		"自由职业者":     {"自由职业者"},
		"咨询顾问":      {"咨询顾问"},
	}

	for _, topic := range topics {
		for prof, keywords := range careerMap {
			for _, kw := range keywords {
				if topic == kw {
					career[prof]++
					break
				}
			}
		}
	}

	profile.CareerAffinity, _ = json.Marshal(career)
}

// updateInterestAreas 更新关注领域
func updateInterestAreas(profile *UserProfile, topics []string) {
	var interests map[string]int
	if len(profile.InterestAreas) > 0 {
		json.Unmarshal(profile.InterestAreas, &interests)
	}
	if interests == nil {
		interests = make(map[string]int)
	}

	interestMap := map[string][]string{
		"人工智能": {"人工智能"},
		"开源关注": {"开源关注"},
		"技术前沿": {"技术前沿"},
		"职业发展": {"职业发展"},
		"副业赚钱": {"副业赚钱"},
		"投资理财": {"投资理财"},
		"创业思考": {"创业思考"},
		"效率提升": {"效率提升"},
		"知识管理": {"知识管理"},
		"健康生活": {"健康生活"},
		"极简主义": {"极简主义"},
		"环保公益": {"环保公益"},
		"财经投资": {"财经", "股票投资"},
		"购物消费": {"购物"},
		"游戏娱乐": {"电竞游戏"},
		"影视音乐": {"电影电视", "音乐"},
		"体育运动": {"体育"},
		"旅游出行": {"旅游", "出行交通"},
	}

	for _, topic := range topics {
		for interest, keywords := range interestMap {
			for _, kw := range keywords {
				if topic == kw {
					interests[interest]++
					break
				}
			}
		}
	}

	profile.InterestAreas, _ = json.Marshal(interests)
}

// updateCommunicationStyle 更新沟通风格
func updateCommunicationStyle(profile *UserProfile, topics []string) {
	var style map[string]int
	if len(profile.CommunicationStyle) > 0 {
		json.Unmarshal(profile.CommunicationStyle, &style)
	}
	if style == nil {
		style = make(map[string]int)
	}

	styleMap := map[string][]string{
		"礼貌客气":  {"礼貌客气"},
		"直接粗暴":  {"直接粗暴"},
		"情绪化表达": {"情绪化表达"},
		"理性分析":  {"理性分析"},
		"感性表达":  {"感性表达"},
	}

	for _, topic := range topics {
		for s, keywords := range styleMap {
			for _, kw := range keywords {
				if topic == kw {
					style[s]++
					break
				}
			}
		}
	}

	profile.CommunicationStyle, _ = json.Marshal(style)
}

// ====================== 用户纠正自动学习 ======================

// UserCorrection represents an extracted correction from user input.
type UserCorrection struct {
	Type  string // "forbidden_tool", "preferred_tool", "general_feedback"
	Value string // tool name or feedback content
}

// One-off execution requirements must not become long-term tool preferences.
func DetectDurableUserCorrections(input string) []UserCorrection {
	if !containsAny(strings.ToLower(input), []string{"以后", "今后", "往后", "长期", "默认", "每次", "from now on", "always", "by default", "every time"}) {
		return nil
	}
	return DetectUserCorrections(input)
}

// detectUserCorrections extracts corrections from user input.
// Patterns: "别用X", "不要用X", "应该用X", "用X更好", "不对", "错了", "很好"
func DetectUserCorrections(input string) []UserCorrection {
	var corrections []UserCorrection

	// Forbidden tools: "别用 X" / "不要用 X" / "不要再调 X"
	forbiddenPatterns := []string{
		`别用\s*(\S+)`,
		`不要用\s*(\S+)`,
		`别再?用\s*(\S+)`,
		`换掉\s*(\S+)`,
		`(\S+)\s*不行`,
	}
	for _, pat := range forbiddenPatterns {
		re := regexp.MustCompile(pat)
		if matches := re.FindStringSubmatch(input); len(matches) > 1 {
			tool := matches[1]
			if len(tool) > 1 && len(tool) < 30 {
				corrections = append(corrections, UserCorrection{Type: "forbidden_tool", Value: tool})
			}
		}
	}

	// Preferred tools: "用 X" / "应该用 X" / "X 更好"
	preferredPatterns := []string{
		`应该用\s*(\S+)`,
		`用\s*(\S+)\s*更好`,
		`改用\s*(\S+)`,
		`推荐用\s*(\S+)`,
		`直接用\s*(\S+)`,
	}
	for _, pat := range preferredPatterns {
		re := regexp.MustCompile(pat)
		if matches := re.FindStringSubmatch(input); len(matches) > 1 {
			tool := matches[1]
			if len(tool) > 1 && len(tool) < 30 {
				corrections = append(corrections, UserCorrection{Type: "preferred_tool", Value: tool})
			}
		}
	}

	// General feedback: positive or negative
	if matched, _ := regexp.MatchString(`很好|不错|棒|对的|就是这样|正解`, input); matched {
		corrections = append(corrections, UserCorrection{Type: "general_feedback", Value: "positive"})
	}
	if matched, _ := regexp.MatchString(`不对|错了|不是这样|搞错了|不对不对`, input); matched {
		corrections = append(corrections, UserCorrection{Type: "general_feedback", Value: "negative"})
	}

	return corrections
}

// applyCorrections saves detected corrections to user profile.
func (m *MemoryStore) ApplyCorrections(userName string, corrections []UserCorrection) {
	if err := m.ApplyCorrectionsForTenant(defaultExecutionTenantID, userName, corrections); err != nil {
		log.Printf("[LEARN] save correction failed: %v", err)
	}
}

func (m *MemoryStore) ApplyCorrectionsForTenant(tenantID, userName string, corrections []UserCorrection) error {
	for _, corr := range corrections {
		switch corr.Type {
		case "forbidden_tool":
			if err := m.AddUserConstraintForTenant(tenantID, userName, "forbidden", corr.Value); err != nil {
				return err
			}
			log.Printf("[LEARN] user=%s forbidden_tool=%s", userName, corr.Value)
		case "preferred_tool":
			if err := m.AddUserConstraintForTenant(tenantID, userName, "preferred", corr.Value); err != nil {
				return err
			}
			log.Printf("[LEARN] user=%s preferred_tool=%s", userName, corr.Value)
		}
	}
	return nil
}
