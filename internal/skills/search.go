package skills

import (
	"context"
	"fmt"
	"strings"
)

// SearchSkillsWithLLM uses an LLM call to find the most relevant skills for a query.
// This is the fallback when keyword matching yields too few results.
// Claude Code borrowed: EXPERIMENTAL_SKILL_SEARCH
func (l *SkillsLoader) SearchSkillsWithLLM(ctx context.Context, query string, limit int) ([]string, error) {
	if l.llmCaller == nil {
		return nil, fmt.Errorf("LLM caller not available for skill search")
	}

	// Build skill index for the LLM
	var skillList strings.Builder
	count := 0
	l.mu.RLock()
	for name, skill := range l.skills {
		if count >= 0 && count < 50 { // limit to 50 skills in prompt
			skillList.WriteString(fmt.Sprintf("- %s: %s\n", name, PromptSafeDescription(skill.Metadata.Description)))
		}
		count++
	}
	l.mu.RUnlock()

	if count == 0 {
		return nil, nil
	}

	prompt := fmt.Sprintf(`Given the user query: "%s"

List the MOST relevant skills from this catalog (return only skill names, one per line, max %d):
%s

Rules:
- Match semantic intent, not just keywords
- Prefer skills directly related to the task
- Return skill names exactly as listed
- If nothing matches, return "none"`, query, limit, skillList.String())

	result, err := l.llmCaller(ctx, prompt, 256)
	if err != nil {
		return nil, err
	}

	// Parse the result
	l.mu.RLock()
	defer l.mu.RUnlock()
	var skills []string
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(strings.TrimSpace(line), "-* ")
		if line == "" || line == "none" {
			continue
		}
		// Verify the skill exists
		if _, ok := l.skills[line]; ok {
			skills = append(skills, line)
		}
	}
	if len(skills) > limit {
		skills = skills[:limit]
	}
	return skills, nil
}

// GetTopSkillsHybrid combines keyword matching with LLM fallback for better results.
func (l *SkillsLoader) GetTopSkillsHybrid(ctx context.Context, query string, limit int) []string {
	// Step 1: Try cache
	if l.skillCache != nil {
		if cached, ok := l.skillCache.Get(query); ok {
			return cached
		}
	}

	// Step 2: Keyword matching (fast)
	keywords := extractIntentKeywordsEnhanced(query)
	keywordResults := l.GetTopSkills(keywords, limit)

	// Step 3: If keyword results are sufficient, return them
	if len(keywordResults) >= 3 || l.SkillCount() < 5 {
		if l.skillCache != nil {
			l.skillCache.Set(query, keywordResults)
		}
		return keywordResults
	}

	// Step 4: LLM fallback for better semantic matching
	if l.llmCaller != nil && ctx != nil {
		llmResults, err := l.SearchSkillsWithLLM(ctx, query, limit)
		if err == nil && len(llmResults) > 0 {
			// Merge: keyword results first, then LLM results (deduplicated)
			seen := make(map[string]bool)
			var merged []string
			for _, s := range keywordResults {
				if !seen[s] {
					merged = append(merged, s)
					seen[s] = true
				}
			}
			for _, s := range llmResults {
				if !seen[s] {
					merged = append(merged, s)
					seen[s] = true
				}
			}
			if len(merged) > limit {
				merged = merged[:limit]
			}
			if l.skillCache != nil {
				l.skillCache.Set(query, merged)
			}
			return merged
		}
	}

	return keywordResults
}

// extractIntentKeywordsEnhanced adds fuzzy matching and more patterns.
func extractIntentKeywordsEnhanced(question string) []string {
	base := extractIntentKeywords(question)

	// Add derived keywords based on common patterns
	lower := strings.ToLower(question)

	// Task complexity hints
	if strings.Contains(lower, "复杂") || strings.Contains(lower, "完整") || strings.Contains(lower, "多步骤") ||
		strings.Contains(lower, "complex") || strings.Contains(lower, "multi-step") {
		base = append(base, "workflow", "pipeline", "plan")
	}
	if strings.Contains(lower, "紧急") || strings.Contains(lower, "尽快") || strings.Contains(lower, "马上") ||
		strings.Contains(lower, "urgent") || strings.Contains(lower, "asap") {
		base = append(base, "quick", "fast")
	}
	if strings.Contains(lower, "bug") || strings.Contains(lower, "修复") || strings.Contains(lower, "报错") ||
		strings.Contains(lower, "故障") || strings.Contains(lower, "失败") || strings.Contains(lower, "不工作") {
		base = append(base, "debug", "fix", "diagnose")
	}

	// Tool hints
	if strings.Contains(lower, "ppt") || strings.Contains(lower, "pptx") || strings.Contains(lower, "幻灯片") || strings.Contains(lower, "演示") {
		base = append(base, "ppt", "presentation", "report")
	}
	if strings.Contains(lower, "pdf") {
		base = append(base, "pdf", "document", "extract")
		if strings.Contains(lower, "文字") || strings.Contains(lower, "文本") || strings.Contains(lower, "转成") ||
			strings.Contains(lower, "转换") || strings.Contains(lower, "extract") {
			base = append(base, "ocr", "convert")
		}
	}
	if strings.Contains(lower, "邮件") || strings.Contains(lower, "email") || strings.Contains(lower, "发邮件") {
		base = append(base, "email", "send", "notify")
	}

	// Data format hints
	if strings.Contains(lower, ".csv") || strings.Contains(lower, "csv") || strings.Contains(lower, "表格数据") {
		base = append(base, "csv", "data", "table", "analysis")
	}
	if strings.Contains(lower, ".xlsx") || strings.Contains(lower, "xlsx") || strings.Contains(lower, ".xls") ||
		strings.Contains(lower, "excel") || strings.Contains(lower, "表格") {
		base = append(base, "excel", "spreadsheet", "data", "table")
	}

	// Deduplicate
	seen := make(map[string]bool)
	var result []string
	for _, k := range base {
		if !seen[k] {
			result = append(result, k)
			seen[k] = true
		}
	}
	return result
}

// SkillsLoader.llmCaller is set by the agent factory when LLM is available.
type SkillLLMCaller func(ctx context.Context, prompt string, maxTokens int) (string, error)

// SetLLMCaller sets the LLM caller for skill search.
func (l *SkillsLoader) SetLLMCaller(caller SkillLLMCaller) {
	l.llmCaller = caller
}
