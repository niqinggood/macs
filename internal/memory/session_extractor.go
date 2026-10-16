package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
	"mac/internal/runtimeutil"
)

type SessionMemoryConfig struct {
	Enabled                    bool `json:"enabled" yaml:"enabled"`
	MinimumTokensToInit        int  `json:"minimum_tokens_init" yaml:"minimum_tokens_init"`
	MinimumTokensBetweenUpdate int  `json:"minimum_tokens_between" yaml:"minimum_tokens_between"`
	ToolCallsBetweenUpdates    int  `json:"tool_calls_between" yaml:"tool_calls_between"`
	MaxSectionsTokens          int  `json:"max_sections_tokens" yaml:"max_sections_tokens"`
	MaxTotalTokens             int  `json:"max_total_tokens" yaml:"max_total_tokens"`
}

func DefaultSessionMemoryConfig() SessionMemoryConfig {
	return SessionMemoryConfig{
		Enabled:                    true,
		MinimumTokensToInit:        10000,
		MinimumTokensBetweenUpdate: 5000,
		ToolCallsBetweenUpdates:    3,
		MaxSectionsTokens:          2000,
		MaxTotalTokens:             12000,
	}
}

type SessionExtractorDependencies struct {
	CallLLM              LLMCaller
	EstimateMessages     func([]map[string]interface{}) int
	ExtractArtifactPaths func(string) []string
	CleanCarryover       ContentCleaner
	Logger               *log.Logger
	Enabled              func() bool
}

// SessionMemoryExtractor generates summaries with host-supplied persistence
// or compatibility Markdown storage.
type SessionMemoryExtractor struct {
	config SessionMemoryConfig

	mu            sync.Mutex
	lastTokens    int
	toolCallCount int
	lastExtracted time.Time
	extracting    bool
	memoryPath    string
	dependencies  SessionExtractorDependencies
	workspace     string
	sessions      map[string]*SessionMemoryExtractor
}

func NewSessionMemoryExtractor(workspace string, config SessionMemoryConfig, dependencies SessionExtractorDependencies) *SessionMemoryExtractor {
	memoryDir := filepath.Join(workspace, ".openkitty")
	_ = os.MkdirAll(memoryDir, 0755)
	if dependencies.Logger == nil {
		dependencies.Logger = log.Default()
	}
	if dependencies.EstimateMessages == nil {
		dependencies.EstimateMessages = estimateSessionMessages
	}
	if dependencies.CleanCarryover == nil {
		dependencies.CleanCarryover = func(_ string, content string, maxRunes int) string {
			return truncateSessionRunes(strings.TrimSpace(content), maxRunes)
		}
	}
	return &SessionMemoryExtractor{
		config:       config,
		memoryPath:   filepath.Join(memoryDir, "session-memory.md"),
		dependencies: dependencies,
		workspace:    workspace,
		sessions:     make(map[string]*SessionMemoryExtractor),
	}
}

// ForSession gives each conversation its own extraction progress and in-flight
// reservation. Hashing avoids collisions between sanitized or truncated IDs.
func (extractor *SessionMemoryExtractor) ForSession(sessionID string) *SessionMemoryExtractor {
	if extractor == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	extractor.mu.Lock()
	defer extractor.mu.Unlock()
	if child := extractor.sessions[sessionID]; child != nil {
		return child
	}
	child := NewSessionMemoryExtractor(extractor.workspace, extractor.config, extractor.dependencies)
	digest := sha256.Sum256([]byte(sessionID))
	child.SetSession(fmt.Sprintf("s-%x", digest[:16]))
	extractor.sessions[sessionID] = child
	return child
}

func estimateSessionMessages(messages []map[string]interface{}) int {
	total := 0
	for _, message := range messages {
		for _, key := range []string{"content", "reasoning_content"} {
			if content, ok := message[key].(string); ok {
				total += runtimeutil.EstimateTokens(content)
			}
		}
	}
	return total
}

func (extractor *SessionMemoryExtractor) baseMemoryDir() string {
	return filepath.Dir(extractor.memoryPath)
}

func (extractor *SessionMemoryExtractor) SetSession(sessionID string) {
	extractor.mu.Lock()
	defer extractor.mu.Unlock()
	extractor.memoryPath = filepath.Join(extractor.baseMemoryDir(), fmt.Sprintf("session-memory-%s.md", sanitizeSessionID(sessionID)))
}

func (extractor *SessionMemoryExtractor) MemoryPath() string {
	if extractor == nil {
		return ""
	}
	extractor.mu.Lock()
	defer extractor.mu.Unlock()
	return extractor.memoryPath
}

func sanitizeSessionID(id string) string {
	id = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, id)
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

func (extractor *SessionMemoryExtractor) ShouldExtract(estimatedTokens, toolCallsThisTurn int, isNaturalBreak bool) bool {
	if extractor == nil || !extractor.config.Enabled || extractor.dependencies.CallLLM == nil || extractor.dependencies.Enabled != nil && !extractor.dependencies.Enabled() {
		return false
	}
	extractor.mu.Lock()
	defer extractor.mu.Unlock()
	if extractor.extracting || estimatedTokens < extractor.config.MinimumTokensToInit {
		return false
	}
	if estimatedTokens-extractor.lastTokens < extractor.config.MinimumTokensBetweenUpdate {
		return false
	}
	extractor.toolCallCount += toolCallsThisTurn
	if extractor.toolCallCount < extractor.config.ToolCallsBetweenUpdates && !isNaturalBreak {
		return false
	}
	extractor.extracting = true
	return true
}

// ReleaseReservation hands in-flight ownership to the durable processing queue.
func (extractor *SessionMemoryExtractor) ReleaseReservation() {
	if extractor == nil {
		return
	}
	extractor.mu.Lock()
	extractor.extracting = false
	extractor.mu.Unlock()
}

func (extractor *SessionMemoryExtractor) Extract(messages []map[string]interface{}, sessionTitle string) error {
	return extractor.ExtractContext(context.Background(), messages, sessionTitle)
}

func (extractor *SessionMemoryExtractor) ExtractContext(parent context.Context, messages []map[string]interface{}, sessionTitle string) error {
	return extractor.extractContext(parent, messages, sessionTitle, nil, nil)
}

// ExtractContextWithStore separates generation from authoritative persistence.
// Queued summaries use the database's revision/lease fenced commit and never
// overwrite the compatibility Markdown before that commit is accepted.
func (extractor *SessionMemoryExtractor) ExtractContextWithStore(parent context.Context, messages []map[string]interface{}, sessionTitle string, load func(context.Context) (string, error), save func(context.Context, string) error) error {
	if load == nil || save == nil {
		return fmt.Errorf("summary store is required")
	}
	return extractor.extractContext(parent, messages, sessionTitle, load, save)
}

func (extractor *SessionMemoryExtractor) extractContext(parent context.Context, messages []map[string]interface{}, sessionTitle string, load func(context.Context) (string, error), save func(context.Context, string) error) error {
	if extractor == nil || extractor.dependencies.CallLLM == nil {
		return fmt.Errorf("session memory LLM caller is unavailable")
	}
	extractor.mu.Lock()
	defer func() {
		extractor.mu.Lock()
		extractor.extracting = false
		extractor.mu.Unlock()
	}()
	estimatedTokens := extractor.dependencies.EstimateMessages(messages)
	memoryPath := extractor.memoryPath
	extractor.mu.Unlock()
	if !extractor.config.Enabled || extractor.dependencies.Enabled != nil && !extractor.dependencies.Enabled() {
		return fmt.Errorf("session memory is disabled")
	}
	if parent == nil {
		parent = context.Background()
	}

	conversationText := extractor.BuildExtractionText(messages)
	existingContent := ""
	if load != nil {
		var err error
		existingContent, err = load(parent)
		if err != nil {
			return fmt.Errorf("load session summary: %w", err)
		}
	} else if data, err := os.ReadFile(memoryPath); err == nil {
		existingContent = string(data)
	}
	if runtimeutil.EstimateTokens(existingContent) > extractor.config.MaxTotalTokens {
		existingContent = truncateSessionRunes(existingContent, extractor.config.MaxTotalTokens*4)
	}
	prompt := extractor.BuildExtractionPrompt(conversationText, existingContent, sessionTitle)

	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	summary, err := extractor.dependencies.CallLLM(ctx, []map[string]interface{}{
		{"role": "system", "content": SessionMemorySystemPrompt},
		{"role": "user", "content": prompt},
	}, extractor.config.MaxTotalTokens/2)
	if err != nil {
		return fmt.Errorf("session memory extraction: %w", err)
	}
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("session memory extraction returned an empty summary")
	}
	if runtimeutil.EstimateTokens(summary) > extractor.config.MaxTotalTokens {
		summary = truncateSessionRunes(summary, extractor.config.MaxTotalTokens*4) + "\n[Memory truncated to budget]"
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if extractor.dependencies.Enabled != nil && !extractor.dependencies.Enabled() {
		return fmt.Errorf("session memory was disabled before commit")
	}
	if save != nil {
		if err := save(ctx, summary); err != nil {
			return fmt.Errorf("commit session summary: %w", err)
		}
	} else if err := fsutil.WriteAtomic(memoryPath, []byte(summary), 0600); err != nil {
		return fmt.Errorf("write session memory: %w", err)
	}
	extractor.mu.Lock()
	extractor.lastTokens = estimatedTokens
	extractor.lastExtracted = time.Now()
	extractor.toolCallCount = 0
	extractor.mu.Unlock()
	extractor.dependencies.Logger.Printf("[SessionMemory] committed %d tokens", runtimeutil.EstimateTokens(summary))
	return nil
}

func (extractor *SessionMemoryExtractor) BuildExtractionText(messages []map[string]interface{}) string {
	if extractor == nil {
		return ""
	}
	var builder strings.Builder
	start := 0
	if len(messages) > 60 {
		start = len(messages) - 60
	}
	for _, message := range messages[start:] {
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		if role == "system" {
			continue
		}
		toolName, _ := message["name"].(string)
		if role == "tool" {
			if !sessionMemoryToolSucceeded(message, content) {
				failure := truncateSessionRunes(strings.TrimSpace(content), 400)
				if failure == "" {
					failure = "tool execution failed; no result was obtained"
				}
				fmt.Fprintf(&builder, "[tool-failed:%s] %s\n", toolName, failure)
				continue
			}
			if extractor.dependencies.ExtractArtifactPaths == nil {
				continue
			}
			paths := extractor.dependencies.ExtractArtifactPaths(content)
			if len(paths) > 0 {
				fmt.Fprintf(&builder, "[tool-artifacts:%s] %s\n", toolName, strings.Join(paths, ", "))
			}
			continue
		}
		content = extractor.dependencies.CleanCarryover(role, content, 400)
		if content != "" {
			fmt.Fprintf(&builder, "[%s] %s\n", role, content)
		}
	}
	return builder.String()
}

func sessionMemoryToolSucceeded(message map[string]interface{}, content string) bool {
	if status, ok := message["status"].(bool); ok {
		return status
	}
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, marker := range []string{"执行失败", "tool result\nstatus: failed", "status: error", "error_code:", "permission denied", "access denied", "denied runtime or historical artifact"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

const SessionMemorySystemPrompt = `You are a session memory writer. Read the conversation and the existing memory file, then UPDATE the structured memory.
Follow these rules STRICTLY:
1. Preserve ALL sections — do NOT remove any
2. Update each section with new information from the conversation
3. Be FACTUAL — record specific paths, commands, decisions, errors
4. Mark completed items with [DONE], active items with [IN PROGRESS]
5. Keep each section under the token budget
6. Output ONLY the updated markdown — no introduction, no commentary
7. Tool status is authoritative. A [tool-failed:name] record means the action did NOT succeed: never say the file was read, created, verified, or otherwise obtained. Record the failure under Errors and Fixes instead. Only [tool-artifacts:name] records come from successful tool results.

Required sections format:
# Session Memory

## Current State
Brief description of what we're doing RIGHT NOW.

## Task
- The specific task or goal

## Files and Functions
- file_path: what it does / why important

## Key Decisions
- decision: rationale

## Errors and Fixes
- error: how it was resolved

## Worklog
- timestamp: action taken`

func (extractor *SessionMemoryExtractor) BuildExtractionPrompt(conversationText, existingContent, title string) string {
	var builder strings.Builder
	if existingContent != "" {
		builder.WriteString("## Existing Session Memory\n")
		builder.WriteString(existingContent)
		builder.WriteString("\n\n")
	}
	builder.WriteString("## Recent Conversation\n")
	builder.WriteString(conversationText)
	builder.WriteString("\n\nUpdate the session memory based on the conversation above. Preserve ALL sections.")
	if title != "" {
		fmt.Fprintf(&builder, "\nSession title: %s\n", title)
	}
	return builder.String()
}

func truncateSessionRunes(value string, max int) string {
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}
