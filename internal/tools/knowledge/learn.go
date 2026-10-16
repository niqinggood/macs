package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LearnTool implements Dream & Distill — cross-session learning from
// event traces and git history. Extracts patterns, failures, and
// successful fixes to improve future sessions.
type LearnTool struct {
	workspace string
}

func stringParameter(params map[string]interface{}, key, fallback string) string {
	if value, ok := params[key].(string); ok {
		return value
	}
	return fallback
}

func NewLearnTool(workspace string) *LearnTool {
	return &LearnTool{workspace: workspace}
}

// LearningMemoryDirectory retains the existing learn-tool storage location.
// Readers use the same path instead of maintaining a second learning store.
func LearningMemoryDirectory(workspace string) string {
	return filepath.Join(workspace, ".claude", "projects", "E--openkitty", "memory")
}

func (t *LearnTool) Name() string { return "learn" }

func (t *LearnTool) Description() string {
	return "Cross-session learning from event traces and git history. " +
		"Actions: analyze (scan recent events/git for patterns and insights), " +
		"distill (extract reusable knowledge from session traces), " +
		"insights (show learned patterns from past sessions), " +
		"memory (save a specific lesson to memory). " +
		"Inspired by MiMo-Code's Dream & Distill."
}

func (t *LearnTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"analyze", "distill", "insights", "memory"},
				"description": "analyze: scan events + git for patterns. distill: extract reusable patterns. insights: show past learnings. memory: save lesson to memory.",
			},
			"since": map[string]interface{}{
				"type":        "string",
				"description": "Time range for analysis: '1h', '24h', '7d', '30d'. Default: '7d'.",
			},
			"lesson": map[string]interface{}{
				"type":        "string",
				"description": "For memory action: the lesson to save (markdown format).",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "For memory action: title for the memory entry.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *LearnTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := stringParameter(params, "action", "analyze")
	since := stringParameter(params, "since", "7d")

	switch action {
	case "analyze":
		return t.analyze(ctx, since)
	case "distill":
		return t.distill(ctx, since)
	case "insights":
		return t.showInsights()
	case "memory":
		lesson := stringParameter(params, "lesson", "")
		title := stringParameter(params, "title", "")
		if lesson == "" {
			return "Error: 'lesson' parameter is required for memory action.", nil
		}
		return t.saveMemory(title, lesson)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Analyze ==================

type eventRecord struct {
	Time    string `json:"time"`
	Type    string `json:"type"`
	Agent   string `json:"agent"`
	TaskID  string `json:"task_id"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

type analysisReport struct {
	EventCount      int
	ErrorCount      int
	TopErrors       []stringCount
	TopTools        []stringCount
	RecurringBugs   []string
	GitActivity     []string
	Recommendations []string
}

type stringCount struct {
	Key   string
	Count int
}

func (t *LearnTool) analyze(ctx context.Context, since string) (string, error) {
	var report strings.Builder
	report.WriteString("## Session Analysis\n\n")

	// 1. Analyze events
	eventsPath := filepath.Join(t.workspace, ".openkitty", "events.jsonl")
	events, err := t.loadEvents(eventsPath, since)
	if err != nil {
		report.WriteString(fmt.Sprintf("⚠️ Events not available: %v\n\n", err))
	} else {
		report.WriteString(t.analyzeEvents(events, since))
	}

	// 2. Analyze git history
	report.WriteString(t.analyzeGitHistory(since))

	// 3. Generate recommendations
	report.WriteString(t.generateRecommendations(events, since))

	return report.String(), nil
}

func (t *LearnTool) loadEvents(path, since string) ([]eventRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sinceTime := parseSince(since)
	var events []eventRecord

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev eventRecord
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		evTime, err := time.Parse(time.RFC3339Nano, ev.Time)
		if err != nil {
			continue
		}
		if evTime.After(sinceTime) {
			events = append(events, ev)
		}
	}
	return events, nil
}

func (t *LearnTool) analyzeEvents(events []eventRecord, since string) string {
	var sb strings.Builder
	sb.WriteString("### Event Summary\n\n")

	errorCount := 0
	toolErrors := make(map[string]int)
	agentErrors := make(map[string]int)
	errorPatterns := make(map[string]int)

	for _, ev := range events {
		if ev.Type == "error" {
			errorCount++
			toolErrors[ev.Tool]++

			// Extract agent from task_id
			parts := strings.Split(ev.TaskID, "_")
			if len(parts) > 0 {
				agent := parts[0]
				if agent == "pipe" && len(parts) > 2 {
					agent = strings.Join(parts[:3], "_")
				}
				agentErrors[agent]++
			}

			// Group by error pattern
			pattern := classifyError(ev.Summary)
			errorPatterns[pattern]++
		}
	}

	sb.WriteString(fmt.Sprintf("**%d events, %d errors (%d%% error rate)**\n\n",
		len(events), errorCount, pct(errorCount, len(events))))

	// Top error patterns
	sb.WriteString("#### Error Patterns\n\n")
	topPatterns := topN(errorPatterns, 5)
	for _, p := range topPatterns {
		sb.WriteString(fmt.Sprintf("- **%s**: %d occurrences\n", p.Key, p.Count))
	}

	if len(topPatterns) == 0 {
		sb.WriteString("No errors found in this period.\n")
	}

	// Top failing tools
	sb.WriteString("\n#### Tools With Most Errors\n\n")
	topTools := topN(toolErrors, 5)
	for _, t := range topTools {
		sb.WriteString(fmt.Sprintf("- `%s`: %d errors\n", t.Key, t.Count))
	}

	// Most affected agents
	sb.WriteString("\n#### Most Affected Agents\n\n")
	topAgents := topN(agentErrors, 5)
	for _, a := range topAgents {
		sb.WriteString(fmt.Sprintf("- `%s`: %d errors\n", a.Key, a.Count))
	}
	sb.WriteString("\n")

	return sb.String()
}

func (t *LearnTool) analyzeGitHistory(since string) string {
	var sb strings.Builder
	sb.WriteString("### Git Activity\n\n")

	cmd := exec.Command("git", "log",
		"--since="+since+" days ago",
		"--oneline", "--format=%h %s", "-30")
	cmd.Dir = t.workspace
	out, err := cmd.Output()
	if err != nil {
		sb.WriteString("Git history not available.\n\n")
		return sb.String()
	}

	commits := strings.Split(strings.TrimSpace(string(out)), "\n")
	sb.WriteString(fmt.Sprintf("**%d commits** in the last %s:\n\n", len(commits), since))

	// Categorize commits
	fixes, features, refactors := 0, 0, 0
	for _, c := range commits {
		lower := strings.ToLower(c)
		switch {
		case strings.Contains(lower, "fix") || strings.Contains(lower, "bug"):
			fixes++
			sb.WriteString(fmt.Sprintf("- 🔧 %s\n", c))
		case strings.Contains(lower, "feat") || strings.Contains(lower, "add"):
			features++
			sb.WriteString(fmt.Sprintf("- ✨ %s\n", c))
		case strings.Contains(lower, "refactor"):
			refactors++
			sb.WriteString(fmt.Sprintf("- ♻️ %s\n", c))
		default:
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	sb.WriteString(fmt.Sprintf("\n**Breakdown:** %d fixes | %d features | %d refactors\n\n", fixes, features, refactors))
	return sb.String()
}

func (t *LearnTool) generateRecommendations(events []eventRecord, since string) string {
	var sb strings.Builder
	sb.WriteString("### Recommendations\n\n")

	if len(events) == 0 {
		sb.WriteString("No events to analyze.\n")
		return sb.String()
	}

	// Count repeated errors
	toolErrors := make(map[string]int)
	for _, ev := range events {
		if ev.Type == "error" {
			toolErrors[ev.Tool]++
		}
	}

	// Find tools with >3 errors
	for tool, count := range toolErrors {
		if count >= 3 {
			sb.WriteString(fmt.Sprintf("- 🔴 **`%s`** failed %d times — investigate tool reliability or agent instruction\n", tool, count))
		}
	}

	// Check for tool-not-found errors
	for _, ev := range events {
		if strings.Contains(ev.Summary, "不存在") || strings.Contains(ev.Summary, "not found") {
			sb.WriteString("- ⚠️ Agents requesting non-existent tools — review tool listing in system prompt\n")
			break
		}
	}

	// Check git for any fix commits suggesting regression patterns
	cmd := exec.Command("git", "log", "--since="+since+" days ago",
		"--oneline", "--grep=fix", "-10")
	cmd.Dir = t.workspace
	out, _ := cmd.Output()
	if fixCount := strings.Count(string(out), "\n"); fixCount > 2 {
		sb.WriteString(fmt.Sprintf("- 📝 %d fix commits in recent period — consider adding regression tests\n", fixCount))
	}

	if sb.Len() == len("### Recommendations\n\n") {
		sb.WriteString("No critical recommendations at this time.\n")
	}

	return sb.String()
}

// ================== Distill ==================

func (t *LearnTool) distill(ctx context.Context, since string) (string, error) {
	var report strings.Builder
	report.WriteString("## Distilled Knowledge\n\n")

	// Load events
	eventsPath := filepath.Join(t.workspace, ".openkitty", "events.jsonl")
	events, err := t.loadEvents(eventsPath, since)
	if err != nil {
		return "", fmt.Errorf("load events: %w", err)
	}

	// Extract patterns
	patterns := t.extractPatterns(events)
	if len(patterns) > 0 {
		report.WriteString("### Recurring Patterns\n\n")
		for _, p := range patterns {
			report.WriteString(fmt.Sprintf("- %s\n", p))
		}
		report.WriteString("\n")
	}

	// Extract successful fixes from git
	fixes := t.extractFixesFromGit(since)
	if len(fixes) > 0 {
		report.WriteString("### Successful Fixes\n\n")
		for _, f := range fixes {
			report.WriteString(fmt.Sprintf("- %s\n", f))
		}
		report.WriteString("\n")
	}

	// Suggest reusable patterns
	report.WriteString("### Suggested Reusable Patterns\n\n")
	suggestions := t.suggestPatterns(events)
	for _, s := range suggestions {
		report.WriteString(fmt.Sprintf("- %s\n", s))
	}

	return report.String(), nil
}

func (t *LearnTool) extractPatterns(events []eventRecord) []string {
	var patterns []string

	// Find error chains (same agent, consecutive errors)
	agentChains := make(map[string]int)
	var prevAgent string
	chainLen := 0

	for _, ev := range events {
		if ev.Type == "error" {
			if ev.Agent == prevAgent {
				chainLen++
			} else {
				if chainLen >= 3 {
					agentChains[prevAgent] = chainLen
				}
				chainLen = 1
				prevAgent = ev.Agent
			}
		} else {
			if chainLen >= 3 {
				agentChains[prevAgent] = chainLen
			}
			chainLen = 0
		}
	}

	for agent, count := range agentChains {
		patterns = append(patterns, fmt.Sprintf("Agent `%s` had %d consecutive errors — possible tool configuration issue", agent, count))
	}

	return patterns
}

func (t *LearnTool) extractFixesFromGit(since string) []string {
	cmd := exec.Command("git", "log",
		"--since="+since+" days ago",
		"--oneline", "--grep=fix", "-10")
	cmd.Dir = t.workspace
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var fixes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			fixes = append(fixes, line)
		}
	}
	return fixes
}

func (t *LearnTool) suggestPatterns(events []eventRecord) []string {
	var suggestions []string

	// Look for patterns that could become reusable knowledge
	hasToolErrors := false
	hasLargeOutputs := false

	for _, ev := range events {
		if ev.Type == "error" && strings.Contains(ev.Summary, "不存在") {
			hasToolErrors = true
		}
		if ev.Type == "error" && strings.Contains(ev.Summary, "encoding") {
			hasLargeOutputs = true
		}
	}

	if hasToolErrors {
		suggestions = append(suggestions, "Add tool availability check before pipeline agents execute — ensure every agent has correct tool list")
	}
	if hasLargeOutputs {
		suggestions = append(suggestions, "Add output size limits to CLI tool — large git log outputs should be truncated to 2000 chars")
	}

	if len(suggestions) == 0 {
		suggestions = append(suggestions, "No clear patterns detected — continue collecting session data")
	}

	return suggestions
}

// ================== Insights ==================

func (t *LearnTool) showInsights() (string, error) {
	memoryDir := LearningMemoryDirectory(t.workspace)
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return "No memory directory found. Start by using the 'memory' action to save lessons.", nil
	}

	var sb strings.Builder
	sb.WriteString("## Learned Insights\n\n")

	insightCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "MEMORY.md" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(memoryDir, entry.Name()))
		if err != nil {
			continue
		}

		content := string(data)
		// Extract name and description from frontmatter
		name := ""
		desc := ""
		lines := strings.Split(content, "\n")
		inFM := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "---" {
				if inFM {
					break
				}
				inFM = true
				continue
			}
			if inFM {
				if strings.HasPrefix(line, "name:") {
					name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
				}
				if strings.HasPrefix(line, "description:") {
					desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				}
			}
		}

		if name != "" {
			sb.WriteString(fmt.Sprintf("- [**%s**](%s) — %s\n", name, entry.Name(), desc))
			insightCount++
		}
	}

	if insightCount == 0 {
		sb.WriteString("No insights saved yet. Use `learn memory` to save lessons from this session.\n")
	}

	sb.WriteString(fmt.Sprintf("\n**%d insights** in memory\n", insightCount))
	return sb.String(), nil
}

// ================== Memory ==================

func (t *LearnTool) saveMemory(title, lesson string) (string, error) {
	memoryDir := LearningMemoryDirectory(t.workspace)
	os.MkdirAll(memoryDir, 0755)

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, slug)

	filename := fmt.Sprintf("learned_%s_%s.md", time.Now().Format("20060102"), slug)
	filepath := filepath.Join(memoryDir, filename)

	content := fmt.Sprintf(`---
name: %s
description: Learned from session analysis — %s
metadata:
  type: feedback
  source: learn_tool
  date: %s
---

%s

**Why:** Auto-extracted by learn tool from session analysis.

**How to apply:** Refer to this insight in future sessions when encountering similar patterns.
`, title, time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"), lesson)

	if err := os.WriteFile(filepath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write memory: %w", err)
	}

	return fmt.Sprintf("## Memory Saved\n\n**Title:** %s\n**File:** %s\n**Content:**\n\n%s", title, filename, lesson), nil
}

// ================== Helpers ==================

func classifyError(summary string) string {
	switch {
	case strings.Contains(summary, "不存在") || strings.Contains(summary, "not found"):
		return "Tool not found"
	case strings.Contains(summary, "timeout") || strings.Contains(summary, "deadline"):
		return "Timeout"
	case strings.Contains(summary, "encoding"):
		return "Encoding/Output handling"
	case strings.Contains(summary, "permission"):
		return "Permission denied"
	case strings.Contains(summary, "parse") || strings.Contains(summary, "syntax"):
		return "Parse/Syntax error"
	case strings.Contains(summary, "connection"):
		return "Connection error"
	case strings.Contains(summary, "http_status"):
		return "HTTP error"
	default:
		return "Unknown error"
	}
}

func parseSince(since string) time.Time {
	duration := time.Duration(7 * 24 * time.Hour) // Default 7d
	switch since {
	case "1h":
		duration = time.Hour
	case "24h", "1d":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	}
	return time.Now().Add(-duration)
}

func topN(m map[string]int, n int) []stringCount {
	var pairs []stringCount
	for k, v := range m {
		pairs = append(pairs, stringCount{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Count > pairs[j].Count
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}

func pct(part, total int) int {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}
