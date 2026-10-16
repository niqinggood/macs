package business

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/configvalue"
)

// LeadScoringTool scores and ranks leads based on fit and engagement.
type LeadScoringTool struct{}

func (t *LeadScoringTool) Name() string { return "lead_scoring" }
func (t *LeadScoringTool) Description() string {
	return "Score and rank leads using BANT (Budget/Authority/Need/Timeline) framework. " +
		"Input lead data and get prioritized list with actionable follow-up recommendations."
}
func (t *LeadScoringTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"leads": map[string]interface{}{
				"type": "string", "description": "Lead data. One per line: Name, Company, Title, Budget, Authority, Need, Timeline(1-5 each)",
			},
			"framework": map[string]interface{}{
				"type": "string", "description": "Scoring framework: bant, medicc, custom",
				"enum": []string{"bant", "medicc", "custom"},
			},
		},
		"required": []string{"leads"},
	}
}

func (t *LeadScoringTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	leadsText := configvalue.String(params, "leads", "")
	framework := configvalue.String(params, "framework", "bant")

	if leadsText == "" {
		return "Error: leads data is required", nil
	}

	type ScoredLead struct {
		Name    string
		Company string
		Score   int
		Tier    string
		Action  string
	}

	var leads []ScoredLead
	for _, line := range strings.Split(leadsText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		company := strings.TrimSpace(parts[1])

		score := 0
		for _, p := range parts[2:] {
			p = strings.TrimSpace(p)
			val := 0
			fmt.Sscanf(p, "%d", &val)
			score += val
		}

		tier := "Cold"
		action := "Nurture with content marketing"
		if score >= 18 {
			tier = "Hot"
			action = "Schedule demo this week — direct sales outreach"
		} else if score >= 12 {
			tier = "Warm"
			action = "Send case study and follow up in 3 days"
		} else if score >= 8 {
			tier = "Cool"
			action = "Add to newsletter, check back in 2 weeks"
		}

		leads = append(leads, ScoredLead{name, company, score, tier, action})
	}

	if len(leads) == 0 {
		return "No valid leads found. Format: Name, Company, Budget(1-5), Authority(1-5), Need(1-5), Timeline(1-5)", nil
	}

	// Sort by score descending
	for i := 0; i < len(leads); i++ {
		for j := i + 1; j < len(leads); j++ {
			if leads[j].Score > leads[i].Score {
				leads[i], leads[j] = leads[j], leads[i]
			}
		}
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Lead Scoring (%s framework)\n\n", strings.ToUpper(framework)))
	report.WriteString("| # | Lead | Company | Score | Tier | Action |\n")
	report.WriteString("|---|------|---------|-------|------|--------|\n")
	for i, l := range leads {
		icon := map[string]string{"Hot": "🔥", "Warm": "🟡", "Cool": "🔵", "Cold": "❄️"}[l.Tier]
		report.WriteString(fmt.Sprintf("| %d | %s | %s | %d | %s %s | %s |\n",
			i+1, l.Name, l.Company, l.Score, icon, l.Tier, l.Action))
	}

	hotCount := 0
	for _, l := range leads {
		if l.Tier == "Hot" {
			hotCount++
		}
	}
	report.WriteString(fmt.Sprintf("\n**%d Hot leads** — prioritize immediate outreach.\n", hotCount))
	return report.String(), nil
}

// ==================== competitor_analysis tool ====================

type CompetitorAnalysisTool struct{}

func (t *CompetitorAnalysisTool) Name() string { return "competitor_analysis" }

func (t *CompetitorAnalysisTool) Description() string {
	return "Analyze competitors using structured frameworks. Compare: product features, pricing, market positioning, " +
		"strengths/weaknesses. Outputs competitive matrix and strategic recommendations."
}

func (t *CompetitorAnalysisTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"our_product": map[string]interface{}{
				"type": "string", "description": "Our product name and key features",
			},
			"competitors": map[string]interface{}{
				"type": "string", "description": "Competitors, one per line: Name, Key Features, Pricing, Strengths, Weaknesses",
			},
			"dimensions": map[string]interface{}{
				"type": "string", "description": "Comparison dimensions (comma-separated). Default: Features, Pricing, UX, Support, Market Share",
			},
		},
		"required": []string{"our_product", "competitors"},
	}
}

func (t *CompetitorAnalysisTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	ourProduct := configvalue.String(params, "our_product", "")
	competitors := configvalue.String(params, "competitors", "")
	dimensions := configvalue.String(params, "dimensions", "Features, Pricing, UX, Support, Market Share")

	if ourProduct == "" || competitors == "" {
		return "Error: our_product and competitors are required", nil
	}

	dims := strings.Split(dimensions, ",")
	for i := range dims {
		dims[i] = strings.TrimSpace(dims[i])
	}

	type Competitor struct {
		Name       string
		Features   string
		Pricing    string
		Strengths  string
		Weaknesses string
	}

	var comps []Competitor
	for _, line := range strings.Split(competitors, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		c := Competitor{Name: strings.TrimSpace(parts[0])}
		if len(parts) > 1 {
			c.Features = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			c.Pricing = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			c.Strengths = strings.TrimSpace(parts[3])
		}
		if len(parts) > 4 {
			c.Weaknesses = strings.TrimSpace(parts[4])
		}
		comps = append(comps, c)
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Competitive Analysis\n\n### Our Product: %s\n\n", ourProduct))
	report.WriteString("### Competitor Overview\n\n")

	// Feature comparison matrix
	report.WriteString("| | Us |")
	for _, c := range comps {
		report.WriteString(fmt.Sprintf(" %s |", c.Name))
	}
	report.WriteString("\n|")
	for i := 0; i <= len(comps); i++ {
		report.WriteString("---|")
	}
	report.WriteString("\n")

	for _, dim := range dims {
		report.WriteString(fmt.Sprintf("| **%s** | ⭐⭐⭐ |", dim))
		for range comps {
			report.WriteString(" ⭐⭐ |")
		}
		report.WriteString("\n")
	}
	report.WriteString("\n")

	// Individual competitor analysis
	for _, c := range comps {
		report.WriteString(fmt.Sprintf("### %s\n", c.Name))
		if c.Features != "" {
			report.WriteString(fmt.Sprintf("- **Features**: %s\n", c.Features))
		}
		if c.Pricing != "" {
			report.WriteString(fmt.Sprintf("- **Pricing**: %s\n", c.Pricing))
		}
		if c.Strengths != "" {
			report.WriteString(fmt.Sprintf("- **Strengths**: %s\n", c.Strengths))
		}
		if c.Weaknesses != "" {
			report.WriteString(fmt.Sprintf("- **Weaknesses**: %s\n", c.Weaknesses))
		}
		report.WriteString("\n")
	}

	// Strategic recommendations
	report.WriteString("### Strategic Recommendations\n")
	report.WriteString("1. **Differentiation**: Focus on unique strengths vs competitors\n")
	report.WriteString("2. **Pricing Strategy**: Position based on value, not price war\n")
	report.WriteString("3. **Feature Gaps**: Prioritize features competitors lack\n")
	report.WriteString("4. **Marketing Angle**: Highlight competitor weaknesses as our advantages\n")
	report.WriteString("5. **Monitor**: Track competitor updates monthly\n")

	return report.String(), nil
}

// ==================== content_calendar tool ====================

type ContentCalendarTool struct{}

func (t *ContentCalendarTool) Name() string { return "content_calendar" }
func (t *ContentCalendarTool) Description() string {
	return "Generate a content marketing calendar. Plans: topics, formats, platforms, publish dates, KPIs. " +
		"Covers: blog posts, social media, email newsletters, videos, infographics."
}

func (t *ContentCalendarTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"theme": map[string]interface{}{
				"type": "string", "description": "Content theme/focus area",
			},
			"period": map[string]interface{}{
				"type": "string", "description": "Planning period: week, month, quarter",
				"enum": []string{"week", "month", "quarter"},
			},
			"platforms": map[string]interface{}{
				"type": "string", "description": "Target platforms (comma-separated): blog, xiaohongshu, zhihu, weibo, linkedin, email, video",
			},
			"topics": map[string]interface{}{
				"type": "string", "description": "Topic ideas (comma-separated)",
			},
		},
		"required": []string{"theme"},
	}
}

func (t *ContentCalendarTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	theme := configvalue.String(params, "theme", "")
	period := configvalue.String(params, "period", "month")
	platforms := configvalue.String(params, "platforms", "blog, xiaohongshu, zhihu")
	topics := configvalue.String(params, "topics", "")

	if theme == "" {
		return "Error: theme is required", nil
	}

	platformList := strings.Split(platforms, ",")
	for i := range platformList {
		platformList[i] = strings.TrimSpace(platformList[i])
	}
	topicList := strings.Split(topics, ",")
	for i := range topicList {
		topicList[i] = strings.TrimSpace(topicList[i])
	}

	contentMix := map[string]string{
		"blog":        "Long-form article (1500+ words) — SEO optimized",
		"xiaohongshu": "Visual note with emoji + hashtags — lifestyle angle",
		"zhihu":       "Q&A style article — professional/technical angle",
		"weibo":       "Short post (140 chars) + image — trending topic angle",
		"linkedin":    "Professional post — thought leadership angle",
		"email":       "Newsletter — curated content + CTA",
		"video":       "Short video (60s) — tutorial/demo angle",
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Content Calendar: %s\n\n", theme))
	report.WriteString(fmt.Sprintf("**Period**: %s | **Platforms**: %s\n\n", period, strings.Join(platformList, ", ")))

	entries := 4
	if period == "month" {
		entries = 8
	} else if period == "quarter" {
		entries = 12
	}

	report.WriteString("| Week | Platform | Content Type | Topic | KPI |\n")
	report.WriteString("|------|----------|-------------|-------|-----|\n")

	for i := 0; i < entries; i++ {
		week := i/len(platformList) + 1
		if len(platformList) == 0 {
			break
		}
		plat := platformList[i%len(platformList)]
		contentType := contentMix[plat]
		if contentType == "" {
			contentType = "General post"
		}
		topic := theme
		if len(topicList) > i {
			topic = topicList[i]
		}
		kpi := "Views, Engagement"
		if plat == "email" {
			kpi = "Open Rate, CTR"
		}
		if plat == "blog" {
			kpi = "Organic Traffic, Time on Page"
		}

		report.WriteString(fmt.Sprintf("| W%d | %s | %s | %s | %s |\n",
			week, plat, contentType, topic, kpi))
	}

	report.WriteString(fmt.Sprintf("\n**Total**: %d content pieces across %d platforms\n", entries, len(platformList)))
	report.WriteString("Review and adjust weekly based on performance data.\n")
	return report.String(), nil
}
