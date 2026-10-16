package aiemployee

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EmployeeManager is the host capability required by supervisor routing.
type EmployeeManager interface {
	MatchAtName(message string) (*Config, string)
	DispatchExplicitMention(ctx context.Context, employeeID, task, sessionID string) (*WorkRecord, error)
	Dispatch(ctx context.Context, employeeID, task, sessionID string) (*WorkRecord, error)
	GetEmployee(employeeID string) (*Config, bool)
	ListEmployees() []*Config
}

// SupervisorModelConfig is the minimal model configuration used for routing.
type SupervisorModelConfig struct {
	APIKey    string
	ModelName string
	BaseURL   string
}

func truncateSupervisor(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	return value[:maximum] + "..."
}

// WorkMode decides which execution path a user message takes.
type WorkMode string

const (
	ModeAE          WorkMode = "de"          // Route to a specific AI employee pipeline
	ModeExplore     WorkMode = "explore"     // Free-form agent with all tools
	ModeCoordinator WorkMode = "coordinator" // Complex task → parallel sub-agents
	ModeAuto        WorkMode = "auto"        // Explore + auto-approve safe tool calls
)

// ModeResult is the routing decision returned by the supervisor.
type ModeResult struct {
	Mode       WorkMode `json:"mode"`
	TargetAE   string   `json:"target_de,omitempty"` // Employee ID for DE mode
	Confidence float64  `json:"confidence"`
	Reasoning  string   `json:"reasoning,omitempty"`
}

// SupervisorRouter is the top-level entry point that decides work mode.
// It sits before the AIEmployeeManager and determines whether a user
// message should be routed to a DE pipeline or handled by a free-form agent.
type SupervisorRouter struct {
	config    SupervisorConfig
	aeManager EmployeeManager
	modelCfg  *SupervisorModelConfig
	logger    *log.Logger
	mu        sync.RWMutex
}

// NewSupervisorRouter creates a new supervisor with sensible defaults.
// identity defaults to "assistant" (safe, less restrictive).
// fallback defaults based on identity: "explore" for assistant, "clarify" for enterprise.
func NewSupervisorRouter(cfg SupervisorConfig, aeManager EmployeeManager, modelCfg *SupervisorModelConfig, logger *log.Logger) *SupervisorRouter {
	if cfg.Identity == "" {
		cfg.Identity = "assistant"
	}
	if cfg.Fallback == "" {
		if cfg.Identity == "enterprise" {
			cfg.Fallback = "clarify"
		} else {
			cfg.Fallback = "explore"
		}
	}
	return &SupervisorRouter{
		config:    cfg,
		aeManager: aeManager,
		modelCfg:  modelCfg,
		logger:    logger,
	}
}

// Identity returns the configured system identity.
func (sr *SupervisorRouter) Identity() string {
	return sr.config.Identity
}

// Route decides the work mode for a user message.
// It respects the system identity:
//   - enterprise: always tries to route to a DE; no match → clarify
//   - assistant: tries DE first; no match → fallback to explore
func (sr *SupervisorRouter) Route(ctx context.Context, message string) *ModeResult {
	// Layer 1: explicit @mention — always direct DE dispatch
	if sr.aeManager != nil {
		if cfg, task := sr.aeManager.MatchAtName(message); cfg != nil {
			sr.logger.Printf("[Supervisor] @mention matched: %s → %s", cfg.Name, task)
			return &ModeResult{
				Mode:       ModeAE,
				TargetAE:   cfg.ID,
				Confidence: 1.0,
				Reasoning:  "@mention matched: " + cfg.Name,
			}
		}
	}

	// Layer 2: classify intent → match DE
	// AI employees are explicit-address-only by default. Keyword/LLM routing is
	// retained solely as an opt-in compatibility mode.
	classResult := &ModeResult{Mode: ModeAE, Confidence: 0}
	if sr.config.AutoRouteAIEmployees {
		classResult = sr.classify(message)
	}

	switch sr.config.Identity {
	case "enterprise":
		if classResult.TargetAE != "" && classResult.Confidence > 0.5 {
			sr.logger.Printf("[Supervisor] enterprise → DE %s (confidence=%.2f)", classResult.TargetAE, classResult.Confidence)
			return classResult
		}
		// No match in enterprise mode → ask user to clarify
		sr.logger.Printf("[Supervisor] enterprise: no DE match, need clarification")
		return &ModeResult{
			Mode:       ModeAE,
			Confidence: 0,
			Reasoning:  sr.buildClarification(message),
		}

	default: // assistant
		if classResult.TargetAE != "" && classResult.Confidence > 0.6 {
			sr.logger.Printf("[Supervisor] assistant → DE %s (confidence=%.2f)", classResult.TargetAE, classResult.Confidence)
			return classResult
		}
		// Layer 3: detect coordinator / auto mode from message patterns
		if mode := sr.detectSpecialMode(message); mode != nil {
			return mode
		}
		sr.logger.Printf("[Supervisor] assistant: falling back to %s", sr.config.Fallback)
		return &ModeResult{
			Mode:       ModeExplore,
			Confidence: 1.0,
			Reasoning:  "assistant mode: open exploration",
		}
	}
}

// detectSpecialMode checks for coordinator or auto mode signals in the message.
func (sr *SupervisorRouter) detectSpecialMode(message string) *ModeResult {
	msg := strings.ToLower(message)

	// Coordinator: multi-file / complex analysis / parallel work
	coordSignals := []string{
		"分析整个项目", "全面审查", "从头到尾", "整个代码库",
		"分析并给出重构", "并行", "同时做", "全部检查",
	}
	for _, s := range coordSignals {
		if strings.Contains(msg, s) {
			return &ModeResult{Mode: ModeCoordinator, Confidence: 0.85, Reasoning: "复杂多步骤任务，启用Coordinator并行模式"}
		}
	}

	// Auto: batch operations that are safe to auto-approve
	autoSignals := []string{
		"把所有", "全部替换", "批量", "每个文件都",
		"帮我改", "修复所有的",
	}
	for _, s := range autoSignals {
		if strings.Contains(msg, s) {
			return &ModeResult{Mode: ModeAuto, Confidence: 0.80, Reasoning: "批量操作，启用Auto模式自动审批安全调用"}
		}
	}

	return nil
}

// classify matches a user message to the best DE using keyword matching
// with optional LLM enhancement.
func (sr *SupervisorRouter) classify(message string) *ModeResult {
	message = strings.ToLower(message)

	// Tier 1: keyword matching (fast, deterministic, no LLM cost)
	if result := sr.keywordMatch(message); result != nil {
		return result
	}

	// Tier 2: LLM classification (optional, best-effort)
	if sr.modelCfg != nil && sr.modelCfg.APIKey != "" {
		if result := sr.llmClassify(message); result != nil {
			return result
		}
	}

	return &ModeResult{Mode: ModeAE, Confidence: 0}
}

// keywordMatch performs fast keyword-based DE matching.
func (sr *SupervisorRouter) keywordMatch(message string) *ModeResult {
	if sr.aeManager == nil {
		return nil
	}

	type candidate struct {
		cfg   *Config
		score int
	}
	var best *candidate

	for _, cfg := range sr.aeManager.ListEmployees() {
		if cfg == nil || cfg.InternalOnly {
			continue
		}
		score := 0
		keywords := sr.extractKeywords(cfg)

		for _, kw := range keywords {
			if strings.Contains(message, strings.ToLower(kw)) {
				score++
			}
		}

		if score > 0 && (best == nil || score > best.score) {
			best = &candidate{cfg: cfg, score: score}
		}
	}

	if best == nil {
		return nil
	}

	confidence := float64(best.score) / 5.0
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.3 {
		confidence = 0.3
	}

	sr.logger.Printf("[Supervisor] keyword match: %s → %s (score=%d, conf=%.2f)",
		truncateSupervisor(message, 50), best.cfg.Name, best.score, confidence)

	return &ModeResult{
		Mode:       ModeAE,
		TargetAE:   best.cfg.ID,
		Confidence: confidence,
		Reasoning:  fmt.Sprintf("keyword matched: %s", best.cfg.Name),
	}
}

// extractKeywords extracts searchable keywords from a DE config.
func (sr *SupervisorRouter) extractKeywords(cfg *Config) []string {
	if cfg == nil || cfg.InternalOnly {
		return nil
	}
	var kw []string
	if cfg.Name != "" {
		kw = append(kw, cfg.Name)
	}
	if cfg.AtName != "" && cfg.AtName != cfg.Name {
		kw = append(kw, cfg.AtName)
	}
	if cfg.Role != "" {
		for _, part := range strings.Split(cfg.Role, "/") {
			part = strings.TrimSpace(part)
			if part != "" {
				kw = append(kw, part)
			}
		}
	}
	// Add pipeline name fragments as hints
	if cfg.Pipeline != "" {
		for _, part := range strings.Split(cfg.Pipeline, "-") {
			part = strings.TrimSpace(part)
			if len(part) > 2 {
				kw = append(kw, part)
			}
		}
	}
	return kw
}

// llmClassify uses LLM to classify user intent and match a DE.
func (sr *SupervisorRouter) llmClassify(message string) *ModeResult {
	if sr.modelCfg == nil {
		return nil
	}

	prompt := sr.buildClassificationPrompt(message)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	resultCh := make(chan *ModeResult, 1)
	go func() {
		content, err := sr.callLLM(ctx, prompt)
		if err != nil {
			sr.logger.Printf("[Supervisor] LLM classify error: %v", err)
			resultCh <- nil
			return
		}
		resultCh <- sr.parseLLMResult(message, content)
	}()

	select {
	case result := <-resultCh:
		return result
	case <-ctx.Done():
		sr.logger.Printf("[Supervisor] LLM classify timeout")
		return nil
	}
}

// buildClassificationPrompt builds the prompt for LLM-based intent classification.
// Uses few-shot examples (like CogniFlow's 15 hand-crafted examples) for high accuracy.
func (sr *SupervisorRouter) buildClassificationPrompt(message string) string {
	var sb strings.Builder
	sb.WriteString("你是智能路由调度器。分析用户消息，匹配最合适的AI员工。\n\n")
	sb.WriteString("## 可用AI员工\n")

	for _, cfg := range sr.aeManager.ListEmployees() {
		if cfg == nil || cfg.InternalOnly {
			continue
		}
		fmt.Fprintf(&sb, "- ID: %s | 名称: %s | 职责: %s", cfg.ID, cfg.Name, cfg.Role)
		if cfg.Pipeline != "" {
			fmt.Fprintf(&sb, " | 流程: %s", cfg.Pipeline)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n## Few-Shot 示例\n")
	sb.WriteString(sr.generateFewShots())

	sb.WriteString("\n## 当前用户消息\n")
	sb.WriteString(message)
	sb.WriteString("\n\n## 输出格式\n")
	sb.WriteString(`严格返回JSON（不要markdown包裹，不要解释）:
{"target_de": "员工ID", "confidence": 0.95, "reason": "简短理由"}

如果无法匹配任何员工:
{"target_de": "", "confidence": 0, "reason": "无匹配"}`)

	return sb.String()
}

// generateFewShots creates few-shot examples from each registered DE's routing examples.
// Falls back to auto-generated examples when DE has no manually configured examples.
func (sr *SupervisorRouter) generateFewShots() string {
	var examples []string

	// Collect 1-2 examples from each DE
	for _, cfg := range sr.aeManager.ListEmployees() {
		if cfg == nil || cfg.InternalOnly {
			continue
		}
		deExamples := cfg.GetRoutingExamples()
		if len(deExamples) == 0 {
			continue
		}
		// Take at most 2 examples per DE to avoid prompt bloat
		count := 0
		for _, msg := range deExamples {
			if count >= 2 {
				break
			}
			examples = append(examples, fmt.Sprintf(
				`用户: "%s" → {"target_de": "%s", "confidence": 0.90, "reason": "%s"}`,
				msg, cfg.ID, cfg.Name))
			count++
		}
	}

	if len(examples) == 0 {
		return ""
	}

	// Cap total examples at 12 to keep prompt size reasonable
	if len(examples) > 12 {
		examples = examples[:12]
	}

	return strings.Join(examples, "\n") + "\n"
}

// parseLLMResult parses the LLM classification response.
func (sr *SupervisorRouter) parseLLMResult(message, content string) *ModeResult {
	content = strings.TrimSpace(content)
	// Strip markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var llmResult struct {
		TargetAE   string  `json:"target_de"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(content), &llmResult); err != nil {
		sr.logger.Printf("[Supervisor] LLM result parse error: %v (raw: %s)", err, truncateSupervisor(content, 100))
		return nil
	}

	if llmResult.TargetAE == "" {
		return nil
	}

	// Validate the target DE exists
	cfg, exists := sr.aeManager.GetEmployee(llmResult.TargetAE)
	externallyRoutable := exists && cfg != nil && !cfg.InternalOnly
	if !externallyRoutable {
		sr.logger.Printf("[Supervisor] LLM returned unknown DE: %s", llmResult.TargetAE)
		return nil
	}

	sr.logger.Printf("[Supervisor] LLM classify: %s → %s (conf=%.2f)", truncateSupervisor(message, 50), llmResult.TargetAE, llmResult.Confidence)
	return &ModeResult{
		Mode:       ModeAE,
		TargetAE:   llmResult.TargetAE,
		Confidence: llmResult.Confidence,
		Reasoning:  "LLM classified",
	}
}

// callLLM makes a simple chat completion call used for intent classification.
func (sr *SupervisorRouter) callLLM(ctx context.Context, prompt string) (string, error) {
	cfg := sr.modelCfg
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":%q}],"max_tokens":256,"temperature":0}`,
		cfg.ModelName, prompt)

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", nil
	}
	return result.Choices[0].Message.Content, nil
}

// buildClarification generates a prompt asking the user to be more specific,
// listing available DEs as hints.
func (sr *SupervisorRouter) buildClarification(message string) string {
	if sr.aeManager == nil {
		return "请问您需要什么帮助？"
	}

	employees := sr.aeManager.ListEmployees()
	available := make([]*Config, 0, len(employees))
	for _, cfg := range employees {
		if cfg != nil && !cfg.InternalOnly {
			available = append(available, cfg)
		}
	}
	if len(available) == 0 {
		return "请问您需要什么帮助？"
	}

	var sb strings.Builder
	sb.WriteString("请问您需要哪方面的帮助？\n\n可以为您提供以下服务：\n")
	count := 0
	for _, cfg := range available {
		fmt.Fprintf(&sb, "- **%s**：%s\n", cfg.Name, cfg.Role)
		count++
		if count >= 6 {
			break
		}
	}
	if count < len(available) {
		fmt.Fprintf(&sb, "... 及其他 %d 个AI员工\n", len(available)-count)
	}
	return sb.String()
}

// dispatchToDE is a helper that dispatches a message to a DE and sets up
// the SSE progress streaming. Used by the gateway chat handler.
func (sr *SupervisorRouter) DispatchToDE(ctx context.Context, deID, message, sessionID string) (*WorkRecord, string, error) {
	if sr.aeManager == nil {
		return nil, "", fmt.Errorf("AE manager not available")
	}
	var record *WorkRecord
	var err error
	if cfg, task := sr.aeManager.MatchAtName(message); cfg != nil && cfg.ID == deID {
		record, err = sr.aeManager.DispatchExplicitMention(ctx, deID, task, sessionID)
	} else {
		record, err = sr.aeManager.Dispatch(ctx, deID, message, sessionID)
	}
	if err != nil {
		return nil, "", err
	}
	taskID := fmt.Sprintf("%s-de", record.ExecID)
	return record, taskID, nil
}

// GetDeManager exposes the AE manager for the gateway.
func (sr *SupervisorRouter) GetDeManager() EmployeeManager {
	return sr.aeManager
}
