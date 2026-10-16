package compaction

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"mac/internal/agentloop"
	"mac/internal/conversation"
	memorycore "mac/internal/memory"
	"mac/internal/runtimeutil"
	skilldomain "mac/internal/skills"
	"mac/internal/toolcontract"
)

// ====================== Smart Compactor — LLM-Driven Context Compression ======================
// Claude Code 借鉴: forked-agent style structured summarization with PTL retry chain.
//
// Replaces rule-based truncation (pressLongMessages/deterministicCompress) with
// LLM-driven semantic summaries. Falls back gracefully to deterministic methods
// if the LLM compaction fails.

// LLMCaller is the minimal interface for making internal LLM calls.
// Implemented by LLMAgent.callLLMInternal.
type LLMCaller func(ctx context.Context, messages []map[string]interface{}, maxTokens int) (string, error)

// CompactSessionContext carries state that must be restored after compaction.
type CompactSessionContext struct {
	ActiveSkills        []string // currently loaded skill names
	InvokedSkillsPrompt string   // recently invoked skill instructions
	ActivePlan          string   // plan mode instructions (if active)
	RecentFiles         []string // recently read file paths (max 5)
	Workspace           string   // project workspace path
}

// SmartCompactor performs LLM-driven conversation summarization.
type SmartCompactor struct {
	config     conversation.CompressConfig
	callLLM    LLMCaller
	contextMgr *conversation.ContextManager
	logger     *log.Logger
}

// NewSmartCompactor creates a SmartCompactor with the given config and LLM caller.
func NewSmartCompactor(cfg conversation.CompressConfig, callLLM LLMCaller, contextMgr *conversation.ContextManager, logger *log.Logger) *SmartCompactor {
	if logger == nil {
		logger = log.Default()
	}
	return &SmartCompactor{
		config:     cfg,
		callLLM:    callLLM,
		contextMgr: contextMgr,
		logger:     logger,
	}
}

// Compact performs LLM-driven compression of old messages, keeping recent ones intact.
// Returns the new message list, a compact boundary marker, and any error.
//
// On failure: returns the original messages + error (caller should fall back to deterministic).
func (sc *SmartCompactor) Compact(messages []map[string]interface{}, sessionCtx *CompactSessionContext) ([]map[string]interface{}, *conversation.CompactBoundary, error) {
	if sc.callLLM == nil {
		return messages, nil, fmt.Errorf("no LLM caller available for smart compaction")
	}

	n := len(messages)
	keepRecent := sc.config.TailProtectMessages
	if keepRecent <= 0 {
		keepRecent = 8
	}
	// Minimum segment size to attempt compaction
	if n <= keepRecent+6 {
		return messages, nil, fmt.Errorf("too few messages for compaction (%d total, %d keep)", n, keepRecent)
	}

	oldMsgs := messages[:n-keepRecent]
	recentMsgs := messages[n-keepRecent:]

	// Fix orphan tool messages in recentMsgs by pulling in their parent tool_call
	recentMsgs = sc.fixOrphanToolMessages(oldMsgs, recentMsgs)
	if len(recentMsgs) > n-2 {
		// Fixed too many orphans — not enough to compact
		return messages, nil, fmt.Errorf("too many orphan tool messages, skipping compaction")
	}
	oldMsgs = messages[:len(messages)-len(recentMsgs)]

	// Strip media blocks before sending to compaction LLM
	oldMsgs = conversation.StripMediaFromMessages(oldMsgs)

	preTokens := toolcontract.EstimateMessageTokens(oldMsgs)
	sc.logger.Printf("[SmartCompactor] compacting %d messages (~%d tokens), keeping %d recent",
		len(oldMsgs), preTokens, len(recentMsgs))

	// System messages are omitted from the rendered conversation, so explicitly
	// fold the previous semantic summary into repeated compactions.
	previousSummary := conversation.LatestSummary(oldMsgs)

	// Extract continuation facts without an LLM. This block is injected beside
	// the semantic summary and parsed back on the next compaction.
	mechanicalState := conversation.BuildMechanicalState(oldMsgs)
	mechanicalText := conversation.RenderMechanicalState(mechanicalState)

	// Try LLM compaction with PTL retry.
	summary, err := sc.compactWithPTLRetry(oldMsgs, previousSummary)
	if err != nil {
		sc.logger.Printf("[SmartCompactor] LLM compaction failed: %v (will fall back)", err)
		if sc.contextMgr != nil {
			sc.contextMgr.OnCompressFailure(conversation.CompressDeep, err)
		}
		return messages, nil, err
	}

	// Truncate summary if too long
	if runtimeutil.EstimateTokens(summary) > sc.config.SummaryMaxTokens {
		summary = summary[:min(len(summary), sc.config.SummaryMaxTokens*4)] + "\n[Summary truncated]"
	}

	// Build result with compact boundary marker
	boundary := conversation.CompactBoundary{
		Trigger:    "auto",
		PreTokens:  preTokens,
		PostTokens: runtimeutil.EstimateTokens(summary + mechanicalText),
		Timestamp:  time.Now().Unix(),
		CompactID:  fmt.Sprintf("compact_%d", time.Now().UnixNano()),
	}

	result := make([]map[string]interface{}, 0, 3+len(recentMsgs))
	// Preserve system prompt (messages[0])
	result = append(result, messages[0])

	// Inject summary with disclaimer prefix
	result = append(result, map[string]interface{}{
		"role":    "system",
		"content": fmt.Sprintf(conversation.SummaryInstructionPrefix+"\n[Conversation Summary — %d earlier turns (~%d tokens)]\n%s", len(oldMsgs), preTokens, summary),
		"subtype": "compact_boundary",
		"compact_metadata": map[string]interface{}{
			"trigger":    boundary.Trigger,
			"pre_tokens": boundary.PreTokens,
			"compact_id": boundary.CompactID,
		},
	})
	if mechanicalText != "" {
		result = append(result, map[string]interface{}{
			"role":    "user",
			"content": mechanicalText,
			"subtype": conversation.CompactStateSubtype,
		})
	}

	// Restore post-compact state (skills, plan, files)
	if sessionCtx != nil {
		result = sc.restorePostCompactState(result, sessionCtx)
	}

	// Append recent messages
	result = append(result, recentMsgs...)

	if sc.contextMgr != nil {
		sc.contextMgr.OnCompressSuccess(conversation.CompressDeep)
	}

	sc.logger.Printf("[SmartCompactor] compaction complete: %d → %d messages (%d → ~%d tokens)",
		n, len(result), preTokens, runtimeutil.EstimateTokens(summary+mechanicalText))
	return result, &boundary, nil
}

// compactWithPTLRetry attempts LLM summarization with progressive truncation on failure.
// Claude Code 借鉴: truncateHeadForPTLRetry — 3 retries, dropping 25% each time.
func (sc *SmartCompactor) compactWithPTLRetry(oldMsgs []map[string]interface{}, previousSummary string) (string, error) {
	const maxRetries = 3
	const dropFraction = 0.25

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	msgs := oldMsgs
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Build conversation text from messages
		convText := sc.buildConversationText(msgs)
		if strings.TrimSpace(convText) == "" {
			return "", fmt.Errorf("empty conversation text after truncation")
		}

		prompt := sc.buildCompactionPrompt(convText, previousSummary)

		summary, err := sc.callLLM(ctx, []map[string]interface{}{
			{"role": "system", "content": compactionSystemPrompt},
			{"role": "user", "content": prompt},
		}, sc.config.SummaryMaxTokens)

		if err == nil {
			return summary, nil
		}

		// Check if prompt was too long (typical API error patterns)
		errStr := err.Error()
		isPTLError := strings.Contains(errStr, "context_length_exceeded") ||
			strings.Contains(errStr, "maximum context length") ||
			strings.Contains(errStr, "413") ||
			strings.Contains(errStr, "prompt is too long") ||
			strings.Contains(errStr, "Request too large")

		if !isPTLError || attempt >= maxRetries-1 {
			return "", fmt.Errorf("compaction LLM call failed (attempt %d): %w", attempt+1, err)
		}

		// Progressive truncation: drop oldest 25%
		dropCount := max(1, int(float64(len(msgs))*dropFraction))
		msgs = msgs[dropCount:]
		sc.logger.Printf("[SmartCompactor] PTL retry %d/%d: dropped %d messages, %d remaining",
			attempt+1, maxRetries, dropCount, len(msgs))
	}

	return "", fmt.Errorf("compaction failed after %d retries", maxRetries)
}

// buildConversationText converts messages to a compact text representation.
func (sc *SmartCompactor) buildConversationText(messages []map[string]interface{}) string {
	var sb strings.Builder
	for i, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		subtype, _ := msg["subtype"].(string)
		if role == "system" || subtype == conversation.CompactStateSubtype {
			continue
		}
		// Preserve both ends: corrections and hard constraints are often appended
		// after pasted bulk, where prefix-only clipping cannot see them.
		clipRunes := conversation.CompactSummaryOtherClipRunes
		if role == "user" {
			clipRunes = conversation.CompactSummaryUserClipRunes
		}
		content = conversation.ClipText(content, clipRunes)
		toolName, _ := msg["name"].(string)
		if toolName != "" && role == "tool" {
			sb.WriteString(fmt.Sprintf("[#%d tool:%s]: %s\n", i, toolName, content))
		} else if role == "tool" {
			sb.WriteString(fmt.Sprintf("[#%d tool]: %s\n", i, content))
		} else {
			sb.WriteString(fmt.Sprintf("[#%d %s]: %s\n", i, role, content))
		}
	}
	return sb.String()
}

const compactionSystemPrompt = `You are a precise conversation summarizer. Summarize the conversation into a structured format.
Follow these rules STRICTLY:
1. Be factual and brief — use bullet points
2. Record decisions, code changes, file paths, and error messages precisely
3. Mark completed items as [DONE], ongoing items as [IN PROGRESS]
4. Prioritize RECENT over old — recent items should be at the top
5. NEVER re-express old user instructions as active work items
6. Keep the output under the requested token limit

Output format:
## Current Work
- [IN PROGRESS] the MOST RECENT active task
## Key Decisions
- decision: rationale
## Files Modified
- path: what changed
## Errors & Fixes
- error: how fixed
## History (older, lower priority)
- [DONE] completed items
## Pending
- items not yet started`

// buildCompactionPrompt creates the user prompt for the compaction LLM call.
func (sc *SmartCompactor) buildCompactionPrompt(conversationText string, existingSummary string) string {
	var sb strings.Builder

	sb.WriteString("Summarize this conversation into the structured format described in the system prompt.\n\n")

	if existingSummary != "" {
		sb.WriteString("### Previous Summary (preserve and update)\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\n")
	}

	sb.WriteString("### Conversation\n")
	sb.WriteString(conversationText)

	return sb.String()
}

// fixOrphanToolMessages ensures recentMsgs doesn't start with orphan tool results.
// If it does, pulls in the parent tool_call message from oldMsgs.
func (sc *SmartCompactor) fixOrphanToolMessages(oldMsgs, recentMsgs []map[string]interface{}) []map[string]interface{} {
	for len(recentMsgs) > 0 {
		firstRole, _ := recentMsgs[0]["role"].(string)
		if firstRole != "tool" {
			break
		}
		// Find the parent tool_call in oldMsgs
		found := false
		for j := len(oldMsgs) - 1; j >= 0; j-- {
			if _, ok := oldMsgs[j]["tool_calls"]; ok {
				recentMsgs = append(oldMsgs[j:], recentMsgs...)
				oldMsgs = oldMsgs[:j] // this modification is only used locally by caller
				found = true
				break
			}
		}
		if !found {
			// No parent found — drop the orphan
			recentMsgs = recentMsgs[1:]
		}
	}
	return recentMsgs
}

// restorePostCompactState re-injects session context after compaction.
// Claude Code 借鉴: post-compact restoration with per-component token budgets.
func (sc *SmartCompactor) restorePostCompactState(messages []map[string]interface{}, sessionCtx *CompactSessionContext) []map[string]interface{} {
	if sessionCtx == nil {
		return messages
	}

	// The workspace is the execution coordinate system for every relative file
	// path. It used to be captured in CompactSessionContext but never restored,
	// so a long task could retain ".openkitty/storage/foo" while forgetting which
	// workspace owned it. Re-inject it before plans, skills, and recent files so
	// post-compaction tool calls keep the same scope as pre-compaction calls.
	if workspace := memorycore.NormalizeContextPath(sessionCtx.Workspace); workspace != "" {
		messages = append(messages, map[string]interface{}{
			"role": "system",
			"content": fmt.Sprintf(`[WORKSPACE CONTEXT]
Current workspace: %s
This remains the canonical base directory after context compaction. Resolve relative project and .openkitty/storage paths against it. CLI commands also start here unless work_dir explicitly overrides it; a shell-local cd does not change the agent workspace. Keep filesystem discovery within this workspace unless the user explicitly names another location or the task evidence requires one.`, workspace),
		})
	}

	// Re-inject active plan instructions (token budget: 5000)
	if sessionCtx.ActivePlan != "" {
		planText := sessionCtx.ActivePlan
		if runtimeutil.EstimateTokens(planText) > 5000 {
			planText = planText[:5000*4] + "\n[Plan truncated]"
		}
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": "[PLAN MODE ACTIVE]\n" + planText,
		})
	}

	// Re-inject active skill names (token budget: 2000 total)
	if len(sessionCtx.ActiveSkills) > 0 {
		skillList := strings.Join(sessionCtx.ActiveSkills, ", ")
		if len(skillList) > 8000 {
			skillList = skillList[:8000]
		}
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": fmt.Sprintf("[ACTIVE SKILLS: %s]\nThe above skills are still loaded and available.", skillList),
		})
	}

	if sessionCtx.InvokedSkillsPrompt != "" {
		text := sessionCtx.InvokedSkillsPrompt
		if len(text) > 16000 {
			text = text[:16000] + "\n[Invoked skills truncated]"
		}
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": text,
		})
	}

	// Re-inject recent file paths (token budget: 5000 total, 1000 per file, max 5 files)
	if len(sessionCtx.RecentFiles) > 0 {
		var fileLines []string
		totalTokens := 0
		for i, f := range sessionCtx.RecentFiles {
			if i >= 5 || totalTokens > 5000 {
				break
			}
			line := fmt.Sprintf("- %s", f)
			fileLines = append(fileLines, line)
			totalTokens += runtimeutil.EstimateTokens(line)
		}
		if len(fileLines) > 0 {
			messages = append(messages, map[string]interface{}{
				"role":    "system",
				"content": "[RECENTLY ACCESSED FILES]\n" + strings.Join(fileLines, "\n"),
			})
		}
	}

	return messages
}

// ====================== Integration Helpers ======================

// BuildCompactSessionContext extracts session context from the LLMAgent state.
func BuildSessionContext(
	ctx context.Context,
	workspace string,
	messages []map[string]interface{},
	loader *skilldomain.SkillsLoader,
	plan *agentloop.ActivePlan,
) *CompactSessionContext {
	sessionCtx := &CompactSessionContext{
		Workspace: workspace,
	}

	// Active skills — gather from skill commands/tools that were used
	sessionCtx.ActiveSkills = extractActiveSkills(messages)
	if loader != nil {
		sessionCtx.InvokedSkillsPrompt = loader.BuildInvokedSkillsPromptForContext(ctx, 5, 3500)
	}

	// Active plan
	if plan != nil {
		sessionCtx.ActivePlan = plan.FormatProgress()
	}

	// Recent files (extracted from recent tool calls)
	sessionCtx.RecentFiles = extractRecentFiles(messages, 5)

	return sessionCtx
}

// extractActiveSkills scans messages for skill invocations and returns active skill names.
func extractActiveSkills(messages []map[string]interface{}) []string {
	seen := make(map[string]bool)
	var skills []string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		// Check for use_skill tool calls
		for _, tcMap := range toolcontract.ToolCallMaps(msg["tool_calls"]) {
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				name, _ := fn["name"].(string)
				if name == "use_skill" || name == "skill" {
					args, _ := fn["arguments"].(string)
					skillName := extractSkillName(args)
					if skillName != "" && !seen[skillName] {
						seen[skillName] = true
						skills = append([]string{skillName}, skills...)
					}
				}
			}
		}
	}
	return skills
}

func extractSkillName(argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	for _, key := range []string{`"skill_name"`, `"name"`, `"skill"`} {
		if idx := strings.Index(argsJSON, key); idx >= 0 {
			rest := argsJSON[idx+len(key):]
			rest = strings.TrimLeft(rest, `": `)
			rest = strings.TrimSpace(rest)
			end := strings.IndexAny(rest, `",`)
			if end > 0 {
				return rest[:end]
			}
		}
	}
	return ""
}

// extractRecentFiles scans messages for recent read_file tool calls and returns file paths.
func extractRecentFiles(messages []map[string]interface{}, maxFiles int) []string {
	seen := make(map[string]bool)
	var files []string

	// Scan from most recent to oldest
	for i := len(messages) - 1; i >= 0 && len(files) < maxFiles; i-- {
		msg := messages[i]
		toolName, _ := msg["name"].(string)
		if toolName == "read_file" || toolName == "edit_file" || toolName == "write_file" {
			content, _ := msg["content"].(string)
			if content != "" {
				// Try to extract file path from the content or from tool_calls args
				path := extractFilePathFromToolResult(content)
				if path != "" && !seen[path] {
					seen[path] = true
					files = append([]string{path}, files...)
				}
			}
		}
		// Also check tool_calls for file paths in arguments
		for _, tcMap := range toolcontract.ToolCallMaps(msg["tool_calls"]) {
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				name, _ := fn["name"].(string)
				if name == "read_file" || name == "edit_file" || name == "write_file" {
					args, _ := fn["arguments"].(string)
					path := extractFilePathFromArgs(name, args)
					if path != "" && !seen[path] {
						seen[path] = true
						if len(files) < maxFiles {
							files = append(files, path)
						}
					}
				}
			}
		}
	}
	return files
}

// extractFilePathFromToolResult tries to extract a file path from tool result content.
func extractFilePathFromToolResult(content string) string {
	// Basic heuristic: first line often contains the file path
	if idx := strings.Index(content, "\n"); idx > 0 {
		firstLine := content[:idx]
		if len(firstLine) < 200 && !strings.Contains(firstLine, " ") {
			return firstLine
		}
	}
	return ""
}

// extractFilePathFromArgs tries to extract a file path from JSON tool arguments.
func extractFilePathFromArgs(toolName string, argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	// Simple extraction: look for "file_path" or "path" fields
	for _, key := range []string{`"file_path"`, `"path"`, `"file"`} {
		if idx := strings.Index(argsJSON, key); idx >= 0 {
			rest := argsJSON[idx+len(key):]
			// Skip ":\s*"
			rest = strings.TrimLeft(rest, `": `)
			rest = strings.TrimSpace(rest)
			// Read until next " or ,
			end := strings.IndexAny(rest, `",`)
			if end > 0 {
				path := rest[:end]
				if len(path) < 500 && len(path) > 1 {
					return path
				}
			}
		}
	}
	return ""
}
