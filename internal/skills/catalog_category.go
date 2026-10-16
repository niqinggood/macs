package skills

import (
	"strings"
	"unicode"
)

// SkillCatalogCategory returns a presentation category, without changing skill
// activation or routing. Explicit tags, identity, and the existing index take
// precedence over a description, which can mention unrelated capabilities.
func SkillCatalogCategory(metadata SkillMetadata, displayName string, indexedCategories []string, builtinDir string) string {
	for _, tag := range metadata.Tags {
		category := catalogCategoryHint(tag)
		// Broad imported tags such as communication describe many skills.
		// Only an explicit UI category overrides the skill's stated purpose.
		if category != "" && (strings.EqualFold(strings.TrimSpace(tag), category) || containsHan(tag)) {
			return category
		}
	}
	if category := catalogCategoryKeywords(metadata.Name + " " + displayName); category != "" {
		return category
	}
	for _, tag := range metadata.Tags {
		if category := catalogCategoryHint(tag); category != "" {
			return category
		}
	}
	// The order is stable even when the index map gives a skill several uses.
	for _, preferred := range []string{"research", "office", "data", "development", "creative", "business", "finance", "automation", "life", "assistant"} {
		for _, indexed := range indexedCategories {
			if catalogCategoryHint(indexed) == preferred {
				return preferred
			}
		}
	}
	parts := strings.Split(strings.ReplaceAll(builtinDir, "\\", "/"), "/")
	for i, part := range parts {
		if part == "builtin" && i+1 < len(parts) {
			if category := catalogCategoryHint(parts[i+1]); category != "" {
				return category
			}
			break
		}
	}
	if category := catalogCategoryKeywords(metadata.Description); category != "" {
		return category
	}
	return "other"
}

func catalogCategoryHint(hint string) string {
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case "office", "document-processing", "documents", "presentation", "slides-decks", "办公文档":
		return "office"
	case "data", "data-analysis", "statistics", "数据分析":
		return "data"
	case "development", "software-development", "coding", "开发编程":
		return "development"
	case "research", "web-automation", "web-tools", "productivity", "搜索研究":
		return "research"
	case "creative", "media", "design", "content-creation", "设计创作":
		return "creative"
	case "business", "legal", "communication", "quotation", "pricing", "商务运营":
		return "business"
	case "finance", "business-finance", "investment", "投资金融":
		return "finance"
	case "automation", "system-ops", "devops", "monitoring", "niche", "自动化运维":
		return "automation"
	case "life", "medical", "health-wellness", "relationships-wellbeing", "hardship-assistance", "culture-entertainment", "生活健康":
		return "life"
	case "assistant", "meta", "memory", "skill-tools", "teamwork", "助手管理":
		return "assistant"
	case "other", "其他":
		return "other"
	}
	return ""
}

func catalogCategoryKeywords(text string) string {
	text = strings.ToLower(text)
	words := " " + strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ") + " "
	rules := []struct {
		category string
		terms    string
	}{
		{"office", "ppt pptx powerpoint slides slide deck decks presentation docx word pdf document documents markdown 文档 表格填充 幻灯片"},
		{"data", "data statistic statistical statistics chart charts sample testing scientist ml 数据分析 统计 图表 样本"},
		{"development", "code codebase coding compile developer programming debugging debug tdd 开发 编程 代码 调试"},
		{"finance", "investment investor invest portfolio stock stocks trading quant finance financial qmt 股市 股票 投资 金融 量化"},
		{"business", "business legal contract contracts quotation pricing marketing market seo lead fundraising patent 商务 法律 合同 报价 营销 融资 专利"},
		{"life", "medical med tcm health relationship relationships hardship luck wellbeing elder life 健康 医疗 中医 情感 帮扶 命理 老人 生活"},
		{"creative", "design creative video music song audio media speech ocr capture content image visual poster posters 视频 音乐 生图 设计 创作 视觉"},
		{"automation", "automation devops deploy deployment backup restore cleanup admin monitor monitoring patrol robot robotics dofbot k8s resilience computer 自动化 运维 部署 备份 巡检 机器人"},
		{"research", "research search reader paper citation academic review literature lit grants brainstorming hypothesis understand summarize news web browser 研究 搜索 检索 论文 文献 阅读 简报"},
		{"assistant", "orchestration skill skills memory sub team teamwork 助手 技能 记忆 团队"},
	}
	for _, rule := range rules {
		for _, term := range strings.Fields(rule.terms) {
			if strings.Contains(words, " "+term+" ") || (containsHan(term) && strings.Contains(text, term)) {
				return rule.category
			}
		}
	}
	return ""
}

func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
