package customerservice

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"mac/internal/textutil"
	businesstools "mac/internal/tools/business"

	"gorm.io/gorm"
)

// ====================== CS Risk Control ======================

// csrLevel is the customer service risk assessment level.
type csrLevel string

const (
	csrNone   csrLevel = "none"   // No risk detected
	csrLow    csrLevel = "low"    // Minor concern
	csrMedium csrLevel = "medium" // Flag for review
	csrHigh   csrLevel = "high"   // Block or escalate
)

// csrResult is the output of risk assessment.
type csrResult struct {
	Level   csrLevel `json:"level"`
	Reasons []string `json:"reasons"`
	Blocked bool     `json:"blocked"`
	Action  string   `json:"action"` // "pass"/"flag"/"block"/"escalate"
}

// CSRiskController performs multi-layer risk assessment for customer service.
type CSRiskController struct {
	sensitiveWords map[string]csrLevel
	mu             sync.RWMutex
	logger         *log.Logger
}

func NewCSRiskController(logger *log.Logger) *CSRiskController {
	rc := &CSRiskController{
		sensitiveWords: make(map[string]csrLevel),
		logger:         logger,
	}
	rc.loadDefaults()
	return rc
}

func (rc *CSRiskController) loadDefaults() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, w := range []string{"套现", "刷单", "洗钱", "盗刷", "银行卡密码", "支付密码", "短信验证码"} {
		rc.sensitiveWords[w] = csrHigh
	}
	for _, w := range []string{"投诉12315", "法院", "起诉", "律师函", "消协", "诈骗", "假货", "媒体曝光"} {
		rc.sensitiveWords[w] = csrMedium
	}
	for _, w := range []string{"差评", "退货不退款", "客服态度"} {
		rc.sensitiveWords[w] = csrLow
	}
}

// Assess checks a message for risk. Rule-based, <1ms.
func (rc *CSRiskController) Assess(message string) *csrResult {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	result := &csrResult{Level: csrNone, Action: "pass"}
	message = strings.ToLower(message)

	for word, level := range rc.sensitiveWords {
		if strings.Contains(message, word) {
			result.Reasons = append(result.Reasons, fmt.Sprintf("敏感词: %s", word))
			switch level {
			case csrHigh:
				result.Level = csrHigh
				result.Blocked = true
				result.Action = "escalate"
			case csrMedium:
				if result.Level != csrHigh {
					result.Level = csrMedium
					result.Action = "flag"
				}
			case csrLow:
				if result.Level == csrNone {
					result.Level = csrLow
				}
			}
		}
	}

	if result.Level != csrNone {
		rc.logger.Printf("[CSRisk] level=%s reasons=%v", result.Level, result.Reasons)
	}
	return result
}

// AddSensitiveWord adds a custom sensitive word.
func (rc *CSRiskController) AddSensitiveWord(word string, level csrLevel) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.sensitiveWords[word] = level
}

// ====================== CS Handoff ======================

type HandoffReason string

const (
	HandoffUserRequest    HandoffReason = "user_request"
	HandoffRiskEscalation HandoffReason = "risk_escalation"
	HandoffToolFailure    HandoffReason = "tool_failure"
	HandoffKnowledgeGap   HandoffReason = "knowledge_gap"
	HandoffComplaint      HandoffReason = "complaint"
)

// HandoffResult contains the handoff decision.
type HandoffResult struct {
	ShouldHandoff bool          `json:"should_handoff"`
	Reason        HandoffReason `json:"reason"`
	Priority      string        `json:"priority"`
	Summary       string        `json:"summary"`
	TicketID      string        `json:"ticket_id,omitempty"`
}

// CSHandoffManager manages human agent handoff decisions.
type CSHandoffManager struct {
	logger *log.Logger
	mu     sync.Mutex
}

func NewCSHandoffManager(logger *log.Logger) *CSHandoffManager {
	return &CSHandoffManager{logger: logger}
}

// ShouldHandoff determines if a conversation should be handed off to a human.
func (hm *CSHandoffManager) ShouldHandoff(
	intent businesstools.CustomerServiceIntent,
	risk *csrResult,
	toolSuccessCount, toolFailureCount int,
	message string,
) *HandoffResult {
	result := &HandoffResult{ShouldHandoff: false}

	if intent.NeedHandoff {
		result.ShouldHandoff = true
		result.Reason = HandoffUserRequest
		result.Priority = "urgent"
		result.Summary = fmt.Sprintf("用户消息触发转人工: %s", textutil.Truncate(message, 100))
	}

	if risk != nil && risk.Blocked && risk.Level == csrHigh {
		result.ShouldHandoff = true
		result.Reason = HandoffRiskEscalation
		result.Priority = "urgent"
		result.Summary = fmt.Sprintf("风控高危: %s", strings.Join(risk.Reasons, "; "))
	}

	if toolFailureCount > 0 && toolSuccessCount == 0 && toolFailureCount >= 2 {
		result.ShouldHandoff = true
		result.Reason = HandoffToolFailure
		result.Priority = "normal"
		result.Summary = fmt.Sprintf("工具调用全部失败(%d次)", toolFailureCount)
	}

	msg := strings.ToLower(message)
	if strings.Contains(msg, "转人工") || strings.Contains(msg, "人工客服") ||
		strings.Contains(msg, "找客服") || strings.Contains(msg, "投诉") {
		result.ShouldHandoff = true
		result.Reason = HandoffUserRequest
		result.Priority = "normal"
		result.Summary = "用户要求转人工客服"
	}

	if result.ShouldHandoff {
		result.TicketID = fmt.Sprintf("HANDOFF_%d", syncCounter())
		hm.logger.Printf("[CSHandoff] %s priority=%s ticket=%s", result.Reason, result.Priority, result.TicketID)
	}

	return result
}

func (hm *CSHandoffManager) BuildHandoffMessage(result *HandoffResult) string {
	switch result.Priority {
	case "urgent":
		return fmt.Sprintf("您的问题已标记为紧急，正在转接人工客服。\n工单编号: %s\n预计等待: 1分钟内", result.TicketID)
	case "normal":
		return fmt.Sprintf("已为您转接人工客服，请稍候。\n工单编号: %s\n预计等待: 3分钟内", result.TicketID)
	default:
		return fmt.Sprintf("如需进一步帮助，可随时要求转接人工客服。\n工单编号: %s", result.TicketID)
	}
}

var handoffCounter struct {
	mu    sync.Mutex
	count int64
}

func syncCounter() int64 {
	handoffCounter.mu.Lock()
	defer handoffCounter.mu.Unlock()
	handoffCounter.count++
	return handoffCounter.count
}

// ====================== PII Detection ======================

var (
	piiPhone  = regexp.MustCompile(`1[3-9]\d{9}`)
	piiIDCard = regexp.MustCompile(`\d{17}[\dXx]`)
	piiBank   = regexp.MustCompile(`\d{16,19}`)
	piiEmail  = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
)

func MaskPII(s string) string {
	s = piiPhone.ReplaceAllString(s, "***PHONE***")
	s = piiIDCard.ReplaceAllString(s, "***IDCARD***")
	s = piiBank.ReplaceAllString(s, "***BANKCARD***")
	s = piiEmail.ReplaceAllString(s, "***EMAIL***")
	return s
}

func ContainsPII(s string) bool {
	return piiPhone.MatchString(s) || piiIDCard.MatchString(s) ||
		piiBank.MatchString(s) || piiEmail.MatchString(s)
}

// ====================== CS Quality Guidelines ======================

// ====================== CS Message Check ======================

type CSMessageCheck struct {
	Intent       businesstools.CustomerServiceIntent
	Risk         *csrResult
	Handoff      *HandoffResult
	ShouldBlock  bool
	BlockMessage string
}

func CheckCSMessage(ctx context.Context, message string,
	router *businesstools.CustomerServiceRouter, risk *CSRiskController, handoff *CSHandoffManager,
	toolSuccessCount, toolFailureCount int) *CSMessageCheck {

	result := &CSMessageCheck{}

	if router != nil {
		result.Intent = router.Classify(message)
	}

	if risk != nil {
		result.Risk = risk.Assess(message)
		if result.Risk.Blocked {
			result.ShouldBlock = true
			result.BlockMessage = "您的问题涉及敏感信息，已自动转接人工客服处理。请稍候。"
			return result
		}
	}

	if handoff != nil {
		result.Handoff = handoff.ShouldHandoff(result.Intent, result.Risk, toolSuccessCount, toolFailureCount, message)
		if result.Handoff.ShouldHandoff {
			result.BlockMessage = handoff.BuildHandoffMessage(result.Handoff)
			result.ShouldBlock = true
		}
	}

	return result
}

// ====================== Global Instances ======================

var (
	globalCSRisk    *CSRiskController
	globalCSHandoff *CSHandoffManager
	handoffDB       *gorm.DB
)

func GetCSRiskController() *CSRiskController { return globalCSRisk }
func GetCSHandoffManager() *CSHandoffManager { return globalCSHandoff }

func InitCSMiddleware(logger *log.Logger, db *gorm.DB) {
	globalCSRisk = NewCSRiskController(logger)
	globalCSHandoff = NewCSHandoffManager(logger)
	handoffDB = db
	logger.Printf("[CS] Risk controller + Handoff manager initialized")
}

// ====================== Handoff State Machine ======================

// HandoffState tracks the lifecycle of a human handoff.
type HandoffState string

const (
	HS_AI      HandoffState = "ai"      // AI handling
	HS_PENDING HandoffState = "pending" // Waiting for human to claim
	HS_HUMAN   HandoffState = "human"   // Human operator handling
	HS_CLOSED  HandoffState = "closed"  // Resolved and closed
)

// HandoffRecord is the DB model for handoff tracking.
type HandoffRecord struct {
	ID           uint   `gorm:"primaryKey"`
	TicketID     string `gorm:"uniqueIndex;size:128"`
	SessionID    string `gorm:"index;size:256"`
	Reason       string `gorm:"size:256"`
	Summary      string `gorm:"type:text"`
	Priority     string `gorm:"size:32"`
	State        string `gorm:"size:32;default:pending"`
	Operator     string `gorm:"size:128"`
	OperatorNote string `gorm:"type:text"`
	AISummary    string `gorm:"type:text"` // Summary for AI when returning
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
}

// validTransitions defines allowed state transitions.
var handoffTransitions = map[HandoffState][]HandoffState{
	HS_AI:      {HS_PENDING},
	HS_PENDING: {HS_HUMAN, HS_CLOSED},
	HS_HUMAN:   {HS_AI, HS_CLOSED},
	HS_CLOSED:  {},
}

func (hm *CSHandoffManager) canTransition(from, to HandoffState) bool {
	for _, allowed := range handoffTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// CreateHandoff creates a new handoff record (AI → PENDING).
func (hm *CSHandoffManager) CreateHandoff(sessionID, reason, summary, priority string) *HandoffRecord {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	record := &HandoffRecord{
		TicketID:  fmt.Sprintf("HO_%d", time.Now().UnixNano()%100000000),
		SessionID: sessionID,
		Reason:    reason,
		Summary:   summary,
		Priority:  priority,
		State:     string(HS_PENDING),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if handoffDB != nil {
		handoffDB.Create(record)
	}

	hm.logger.Printf("[Handoff] Created %s: %s (priority=%s)", record.TicketID, reason, priority)
	return record
}

// ClaimHandoff assigns a pending handoff to an operator (PENDING → HUMAN).
func (hm *CSHandoffManager) ClaimHandoff(ticketID, operator string) (*HandoffRecord, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var record HandoffRecord
	if handoffDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := handoffDB.Where("ticket_id = ?", ticketID).First(&record).Error; err != nil {
		return nil, fmt.Errorf("工单 %s 未找到", ticketID)
	}

	currentState := HandoffState(record.State)
	if !hm.canTransition(currentState, HS_HUMAN) {
		return nil, fmt.Errorf("工单 %s 当前状态 %s，无法接单", ticketID, currentState)
	}

	record.State = string(HS_HUMAN)
	record.Operator = operator
	record.UpdatedAt = time.Now()
	handoffDB.Save(&record)

	hm.logger.Printf("[Handoff] %s claimed by %s", ticketID, operator)
	return &record, nil
}

// CompleteHandoff resolves a handoff and returns to AI (HUMAN → AI).
// operatorNote is what the operator tells the AI when handing back.
func (hm *CSHandoffManager) CompleteHandoff(ticketID, operatorNote, aiSummary string) (*HandoffRecord, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var record HandoffRecord
	if handoffDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := handoffDB.Where("ticket_id = ?", ticketID).First(&record).Error; err != nil {
		return nil, fmt.Errorf("工单 %s 未找到", ticketID)
	}

	currentState := HandoffState(record.State)
	if !hm.canTransition(currentState, HS_AI) {
		return nil, fmt.Errorf("工单 %s 当前状态 %s，无法返回AI", ticketID, currentState)
	}

	record.State = string(HS_AI)
	record.OperatorNote = operatorNote
	record.AISummary = aiSummary
	record.UpdatedAt = time.Now()
	handoffDB.Save(&record)

	hm.logger.Printf("[Handoff] %s completed, returning to AI", ticketID)
	return &record, nil
}

// CloseHandoff closes a handoff without returning to AI (PENDING/HUMAN → CLOSED).
func (hm *CSHandoffManager) CloseHandoff(ticketID, reason string) (*HandoffRecord, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var record HandoffRecord
	if handoffDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := handoffDB.Where("ticket_id = ?", ticketID).First(&record).Error; err != nil {
		return nil, fmt.Errorf("工单 %s 未找到", ticketID)
	}

	currentState := HandoffState(record.State)
	if !hm.canTransition(currentState, HS_CLOSED) {
		return nil, fmt.Errorf("工单 %s 当前状态 %s，无法关闭", ticketID, currentState)
	}

	now := time.Now()
	record.State = string(HS_CLOSED)
	record.ClosedAt = &now
	record.OperatorNote = reason
	record.UpdatedAt = now
	handoffDB.Save(&record)

	hm.logger.Printf("[Handoff] %s closed: %s", ticketID, reason)
	return &record, nil
}

// GetPendingHandoffs returns all unclaimed handoff tickets.
func (hm *CSHandoffManager) GetPendingHandoffs() []HandoffRecord {
	if handoffDB == nil {
		return nil
	}
	var records []HandoffRecord
	handoffDB.Where("state = ?", string(HS_PENDING)).Order("created_at ASC").Find(&records)
	return records
}

// GetActiveHandoffs returns all open handoffs (pending + human).
func (hm *CSHandoffManager) GetActiveHandoffs() []HandoffRecord {
	if handoffDB == nil {
		return nil
	}
	var records []HandoffRecord
	handoffDB.Where("state IN ?", []string{string(HS_PENDING), string(HS_HUMAN)}).Order("created_at ASC").Find(&records)
	return records
}

// GetHandoffBySession returns the active handoff for a session, if any.
func (hm *CSHandoffManager) GetHandoffBySession(sessionID string) *HandoffRecord {
	if handoffDB == nil {
		return nil
	}
	var record HandoffRecord
	err := handoffDB.Where("session_id = ? AND state IN ?", sessionID,
		[]string{string(HS_PENDING), string(HS_HUMAN)}).First(&record).Error
	if err != nil {
		return nil
	}
	return &record
}

// BuildHandoffStatusMessage returns a user-facing status message.
func (hm *CSHandoffManager) BuildHandoffStatusMessage(record *HandoffRecord) string {
	state := HandoffState(record.State)
	switch state {
	case HS_PENDING:
		return fmt.Sprintf("已为您转接人工客服，请稍候。工单编号: %s\n预计等待: 3分钟内", record.TicketID)
	case HS_HUMAN:
		return fmt.Sprintf("人工客服 %s 已接入，正在为您处理。工单编号: %s", record.Operator, record.TicketID)
	case HS_CLOSED:
		return "工单已处理完成，如有其他问题欢迎随时咨询。"
	default:
		return "正在为您转接人工客服..."
	}
}

// BuildAIHandoffContext builds the context to inject when returning to AI.
func (hm *CSHandoffManager) BuildAIHandoffContext(record *HandoffRecord) string {
	var sb strings.Builder
	sb.WriteString("## 转人工处理结果\n")
	sb.WriteString(fmt.Sprintf("- 工单: %s\n", record.TicketID))
	sb.WriteString(fmt.Sprintf("- 原因: %s\n", record.Reason))
	if record.Operator != "" {
		sb.WriteString(fmt.Sprintf("- 处理人: %s\n", record.Operator))
	}
	if record.OperatorNote != "" {
		sb.WriteString(fmt.Sprintf("- 处理备注: %s\n", record.OperatorNote))
	}
	if record.AISummary != "" {
		sb.WriteString(fmt.Sprintf("\n## 人工处理摘要\n%s\n", record.AISummary))
	}
	sb.WriteString("\n请根据以上信息继续为客户服务。")
	return sb.String()
}

// ====================== Handoff API Helpers ======================

// initHandoffTable ensures the handoff table exists.
func InitHandoffTable(db *gorm.DB) error {
	return db.AutoMigrate(&HandoffRecord{})
}
