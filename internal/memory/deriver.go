package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

type KnowledgeWriter interface {
	AddFactWithEvidence(context.Context, Fact, string) error
}

type SessionContextWriter interface {
	Set(key, value, category string, priority int, sessionID string) error
}

type LLMCaller func(context.Context, []map[string]interface{}, int) (string, error)
type SessionSourceResolver func(string) string
type ContentCleaner func(role, content string, maxRunes int) string

type DeriverDependencies struct {
	Knowledge     KnowledgeWriter
	Session       SessionContextWriter
	CallLLM       LLMCaller
	SessionSource SessionSourceResolver
	CleanContent  ContentCleaner
	Enabled       func() bool
}

// Deriver extracts structured memories from conversations using LLM.
// Inspired by Honcho's Deriver pipeline — background processing that builds
// user representations over time.
type Deriver struct {
	knowledge     KnowledgeWriter
	session       SessionContextWriter
	callLLM       LLMCaller
	sessionSource SessionSourceResolver
	cleanContent  ContentCleaner
	enabled       func() bool
}

func NewDeriver(dependencies DeriverDependencies) *Deriver {
	return &Deriver{
		knowledge:     dependencies.Knowledge,
		session:       dependencies.Session,
		callLLM:       dependencies.CallLLM,
		sessionSource: dependencies.SessionSource,
		cleanContent:  dependencies.CleanContent,
		enabled:       dependencies.Enabled,
	}
}

// Derive extracts facts, preferences, and entity relationships from a conversation.
// Called after each task completes (in Generate).
func (d *Deriver) Derive(sessionID, userInput, finalOutput string, messages []map[string]interface{}) {
	d.DeriveContext(context.Background(), sessionID, userInput, finalOutput, messages)
}

func (d *Deriver) DeriveContext(ctx context.Context, sessionID, userInput, finalOutput string, messages []map[string]interface{}) {
	if err := d.DeriveContextResult(ctx, sessionID, userInput, finalOutput, messages); err != nil {
		log.Printf("[DERIVER] extraction failed: %v", err)
	}
}

// DeriveContextResult exposes failures to the durable queue for retry. Graph
// evidence writes are idempotent for the same fact and source session.
func (d *Deriver) DeriveContextResult(ctx context.Context, sessionID, userInput, finalOutput string, messages []map[string]interface{}) error {
	if d == nil || d.knowledge == nil || d.callLLM == nil || d.sessionSource == nil {
		return fmt.Errorf("fact derivation dependencies are unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if d.enabled != nil && !d.enabled() {
		return fmt.Errorf("memory is disabled")
	}
	source := d.sessionSource(sessionID)
	if source == "" {
		// Automatic facts without an audience cannot be recalled safely. The raw
		// task transcript remains available through the normal memory store.
		return nil
	}

	// Build a compact conversation summary for the LLM
	summary := d.BuildConversationSummary(userInput, finalOutput, messages)
	if summary == "" {
		return nil
	}

	// Call LLM to extract structured knowledge
	facts, err := d.extractFactsContext(ctx, summary)
	if err != nil {
		return err
	}

	// Store extracted facts
	today := time.Now().Format("2006-01-02")
	stored := 0
	for _, f := range facts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.enabled != nil && !d.enabled() {
			return fmt.Errorf("memory is disabled")
		}
		if f.Subject == "" || f.Predicate == "" || f.Object == "" || !d.hasUserEvidence(f.Verbatim, userInput, messages) {
			continue
		}
		if err := d.knowledge.AddFactWithEvidence(ctx, Fact{Subject: f.Subject, Predicate: f.Predicate, Object: f.Object, Source: source, ValidFrom: today, Confidence: f.Confidence}, f.Verbatim); err != nil {
			return err
		}
		// Keep one authoritative write. SCM is a separate store and cannot be
		// atomically corrected or deleted together with these graph records.
		stored++
	}

	log.Printf("[DERIVER] stored %d supported facts", stored)
	return nil
}

func (d *Deriver) hasUserEvidence(quote, userInput string, messages []map[string]interface{}) bool {
	quote = strings.TrimSpace(quote)
	if len([]rune(quote)) < 4 {
		return false
	}
	if strings.Contains(d.clean("user", userInput, 0), quote) {
		return true
	}
	for _, message := range messages {
		if role, _ := message["role"].(string); role == "user" {
			content, _ := message["content"].(string)
			if strings.Contains(d.clean("user", content, 0), quote) {
				return true
			}
		}
	}
	return false
}

// DeriveFact represents one extracted fact.
type DeriveFact struct {
	Subject     string  `json:"subject"`
	Predicate   string  `json:"predicate"`
	Object      string  `json:"object"`
	Verbatim    string  `json:"verbatim,omitempty"`
	Confidence  float64 `json:"confidence"`
	SCMKey      string  `json:"scm_key,omitempty"`
	SCMCategory string  `json:"scm_category,omitempty"`
	SCMPriority int     `json:"scm_priority,omitempty"`
}

func (d *Deriver) BuildConversationSummary(userInput, finalOutput string, messages []map[string]interface{}) string {
	var sb strings.Builder
	userText := d.clean("user", userInput, 300)
	aiText := d.clean("assistant", finalOutput, 300)
	if userText != "" {
		sb.WriteString("User: " + userText + "\n")
	}
	if aiText != "" {
		sb.WriteString("AI: " + aiText + "\n")
	}
	// Add last 2 exchanges
	count := 0
	for i := len(messages) - 1; i >= 0 && count < 4; i-- {
		role, _ := messages[i]["role"].(string)
		content, _ := messages[i]["content"].(string)
		if role == "user" || role == "assistant" {
			cleaned := d.clean(role, content, 200)
			if cleaned == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, cleaned))
			count++
		}
	}
	return strings.TrimSpace(sb.String())
}

func (d *Deriver) clean(role, content string, maximum int) string {
	if d != nil && d.cleanContent != nil {
		return d.cleanContent(role, content, maximum)
	}
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if maximum > 0 && len(runes) > maximum {
		content = string(runes[:maximum])
	}
	return content
}

func (d *Deriver) extractFacts(conversation string) ([]DeriveFact, error) {
	return d.extractFactsContext(context.Background(), conversation)
}

func (d *Deriver) extractFactsContext(parent context.Context, conversation string) ([]DeriveFact, error) {
	prompt := fmt.Sprintf(`Extract structured knowledge from this conversation. Output a JSON array of facts.

Rules:
- subject: the entity this fact is about (person name, project name, tool name)
- predicate: the relationship (prefers, uses, works_on, is_friend_of, dislikes, has_project, location, skill_level, etc.)
- object: the value (use specific names, not pronouns like "it")
- confidence: 0.0-1.0 (1.0 = explicitly stated, 0.7 = implied)
- verbatim: REQUIRED exact user quote that explicitly supports this fact; never quote an AI answer
- Do not infer user facts from AI answers. Skip a fact if no exact user quote supports it
- Only extract NEW information not obvious from context
- Skip greetings, small talk, and task-internal details

Conversation:
%s

Output ONLY valid JSON array, no explanation:
[{"subject":"...","predicate":"...","object":"...","confidence":0.9,"verbatim":"..."}]`, conversation)

	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	result, err := d.callLLM(ctx, []map[string]interface{}{
		{"role": "system", "content": "You are a precise knowledge extraction system. Extract structured facts from conversations. Output ONLY valid JSON arrays. Never invent facts. Never include explanations."},
		{"role": "user", "content": prompt},
	}, 1024)
	if err != nil {
		return nil, err
	}

	// Parse JSON response
	return ParseDeriveFacts(result)
}

func ParseDeriveFacts(raw string) ([]DeriveFact, error) {
	// Strip markdown code fences if present
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if raw == "" || raw == "[]" {
		return nil, nil
	}

	var facts []DeriveFact
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		// Try to extract just the JSON array
		start := strings.Index(raw, "[")
		end := strings.LastIndex(raw, "]")
		if start >= 0 && end > start {
			raw = raw[start : end+1]
			if err2 := json.Unmarshal([]byte(raw), &facts); err2 != nil {
				return nil, fmt.Errorf("parse facts: %w (original: %w)", err2, err)
			}
		} else {
			return nil, fmt.Errorf("parse facts: %w", err)
		}
	}
	return facts, nil
}
