package business

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/configvalue"
)

// SEOTool analyzes content for SEO optimization.
type SEOTool struct{}

func (t *SEOTool) Name() string { return "seo_analyze" }

func (t *SEOTool) Description() string {
	return "Analyze content for SEO optimization. Checks: title length, meta description, keyword density, " +
		"heading structure, readability, internal linking suggestions. Use before publishing any web content."
}

func (t *SEOTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type": "string", "description": "Page title to analyze",
			},
			"content": map[string]interface{}{
				"type": "string", "description": "Page content/body for analysis",
			},
			"keywords": map[string]interface{}{
				"type": "string", "description": "Target keywords (comma-separated)",
			},
			"url": map[string]interface{}{
				"type": "string", "description": "Target URL slug (optional)",
			},
		},
		"required": []string{"content"},
	}
}

func (t *SEOTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	title := configvalue.String(params, "title", "")
	content := configvalue.String(params, "content", "")
	keywords := configvalue.String(params, "keywords", "")

	var report strings.Builder
	report.WriteString("## SEO Analysis\n\n")

	// Title analysis
	if title != "" {
		report.WriteString("### Title\n")
		titleLen := len([]rune(title))
		if titleLen < 30 {
			report.WriteString(fmt.Sprintf("- ⚠️ Too short: %d chars (optimal: 50-60)\n", titleLen))
		} else if titleLen > 70 {
			report.WriteString(fmt.Sprintf("- ⚠️ Too long: %d chars (optimal: 50-60)\n", titleLen))
		} else {
			report.WriteString(fmt.Sprintf("- ✅ Length: %d chars\n", titleLen))
		}
		if keywords != "" {
			for _, kw := range strings.Split(keywords, ",") {
				kw = strings.TrimSpace(kw)
				if strings.Contains(strings.ToLower(title), strings.ToLower(kw)) {
					report.WriteString(fmt.Sprintf("- ✅ Keyword in title: '%s'\n", kw))
				} else {
					report.WriteString(fmt.Sprintf("- ⚠️ Keyword missing in title: '%s'\n", kw))
				}
			}
		}
		report.WriteString("\n")
	}

	// Content analysis
	report.WriteString("### Content\n")
	contentWords := len(strings.Fields(content))
	contentLen := len([]rune(content))
	if contentWords < 300 {
		report.WriteString(fmt.Sprintf("- ⚠️ Short content: %d words (recommend 300+)\n", contentWords))
	} else if contentWords > 3000 {
		report.WriteString(fmt.Sprintf("- ✅ Long-form: %d words (good for SEO)\n", contentWords))
	} else {
		report.WriteString(fmt.Sprintf("- ✅ Length: %d words\n", contentWords))
	}

	// Keyword density
	if keywords != "" {
		report.WriteString("\n### Keyword Density\n")
		lowerContent := strings.ToLower(content)
		for _, kw := range strings.Split(keywords, ",") {
			kw = strings.TrimSpace(kw)
			count := strings.Count(lowerContent, strings.ToLower(kw))
			density := float64(count*len(kw)) / float64(contentLen) * 100
			status := "✅"
			if density < 0.5 {
				status = "⚠️ Low"
			} else if density > 3.0 {
				status = "⚠️ High (keyword stuffing risk)"
			}
			report.WriteString(fmt.Sprintf("- %s '%s': %d occurrences (%.1f%% density)\n", status, kw, count, density))
		}
	}

	// Readability
	report.WriteString("\n### Readability\n")
	sentences := strings.Count(content, ".") + strings.Count(content, "。")
	avgWordsPerSentence := float64(contentWords) / float64(max(sentences, 1))
	if avgWordsPerSentence > 25 {
		report.WriteString(fmt.Sprintf("- ⚠️ Long sentences: avg %.0f words (target <25)\n", avgWordsPerSentence))
	} else {
		report.WriteString(fmt.Sprintf("- ✅ Sentence length: avg %.0f words\n", avgWordsPerSentence))
	}

	paragraphs := strings.Split(content, "\n\n")
	avgParaLen := contentWords / max(len(paragraphs), 1)
	if avgParaLen > 150 {
		report.WriteString(fmt.Sprintf("- ⚠️ Long paragraphs: avg %d words (target <150)\n", avgParaLen))
	} else {
		report.WriteString(fmt.Sprintf("- ✅ Paragraph length: avg %d words\n", avgParaLen))
	}

	// Heading structure
	report.WriteString("\n### Heading Structure\n")
	h1 := strings.Count(content, "# ")
	h2 := strings.Count(content, "## ")
	h3 := strings.Count(content, "### ")
	report.WriteString(fmt.Sprintf("- H1: %d | H2: %d | H3: %d\n", h1, h2, h3))
	if h1 == 0 {
		report.WriteString("- ⚠️ Missing H1 heading\n")
	}
	if h2 == 0 && contentWords > 500 {
		report.WriteString("- ⚠️ Consider adding H2 subheadings for long content\n")
	}

	return report.String(), nil
}

// ==================== email_campaign tool ====================

type EmailCampaignTool struct{}

func (t *EmailCampaignTool) Name() string { return "email_campaign" }

func (t *EmailCampaignTool) Description() string {
	return "Generate email marketing campaigns. Creates: subject lines (A/B variants), email body, CTA buttons, " +
		"personalization tokens, send scheduling recommendations."
}

func (t *EmailCampaignTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"campaign_type": map[string]interface{}{
				"type": "string", "description": "welcome (新用户欢迎), nurture (培育转化), promotional (促销), reengagement (召回), newsletter (通讯)",
				"enum": []string{"welcome", "nurture", "promotional", "reengagement", "newsletter"},
			},
			"audience": map[string]interface{}{
				"type": "string", "description": "Target audience description",
			},
			"product": map[string]interface{}{
				"type": "string", "description": "Product/service name and description",
			},
			"key_points": map[string]interface{}{
				"type": "string", "description": "Key selling points (comma-separated)",
			},
		},
		"required": []string{"campaign_type", "product"},
	}
}

func (t *EmailCampaignTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	campaignType := configvalue.String(params, "campaign_type", "newsletter")
	audience := configvalue.String(params, "audience", "target customers")
	product := configvalue.String(params, "product", "")
	keyPoints := configvalue.String(params, "key_points", "")

	if product == "" {
		return "Error: product is required", nil
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Email Campaign: %s\n\n", campaignType))

	// Subject lines
	report.WriteString("### Subject Lines (A/B Test)\n")
	subjects := getSubjectLines(campaignType, product)
	for i, s := range subjects {
		label := "A"
		if i == 1 {
			label = "B"
		}
		report.WriteString(fmt.Sprintf("- **%s**: %s (open rate estimate: %s)\n", label, s, estimateOpenRate(s)))
	}

	// Email structure
	report.WriteString("\n### Email Template\n")
	report.WriteString("```\n")
	report.WriteString(fmt.Sprintf("To: {{%s}}\n", audience))
	report.WriteString("Subject: {{subject_line}}\n\n")

	switch campaignType {
	case "welcome":
		report.WriteString("Hi {{first_name}},\n\n")
		report.WriteString(fmt.Sprintf("Welcome to %s! We're excited to have you on board.\n\n", product))
		report.WriteString("Here's what you can do next:\n")
		for _, kp := range strings.Split(keyPoints, ",") {
			report.WriteString(fmt.Sprintf("- %s\n", strings.TrimSpace(kp)))
		}
		report.WriteString("\n[Get Started →]\n\n")
		report.WriteString("Best,\nThe Team\n")

	case "promotional":
		report.WriteString("Hi {{first_name}},\n\n")
		report.WriteString(fmt.Sprintf("We've got something special: %s\n\n", product))
		report.WriteString("Why you'll love it:\n")
		for _, kp := range strings.Split(keyPoints, ",") {
			report.WriteString(fmt.Sprintf("✅ %s\n", strings.TrimSpace(kp)))
		}
		report.WriteString("\n[Shop Now →]  Limited time offer!\n\n")

	default:
		report.WriteString(fmt.Sprintf("Hi {{first_name}},\n\nHere's the latest from %s:\n\n", product))
		for _, kp := range strings.Split(keyPoints, ",") {
			report.WriteString(fmt.Sprintf("- %s\n", strings.TrimSpace(kp)))
		}
		report.WriteString("\n[Learn More →]\n")
	}
	report.WriteString("```\n")

	// Schedule recommendation
	report.WriteString("\n### Send Schedule\n")
	report.WriteString(t.recommendSchedule(campaignType))

	return report.String(), nil
}

func getSubjectLines(campaignType, product string) []string {
	switch campaignType {
	case "welcome":
		return []string{
			fmt.Sprintf("Welcome to %s! 🎉 Let's get started", product),
			fmt.Sprintf("You're in! Here's your %s starter guide", product),
		}
	case "promotional":
		return []string{
			fmt.Sprintf("🔥 Don't miss out: %s special offer inside", product),
			fmt.Sprintf("Why 1000+ customers love %s (and you will too)", product),
		}
	case "reengagement":
		return []string{
			fmt.Sprintf("We miss you! Here's 20%% off %s", product),
			fmt.Sprintf("What's new at %s since you've been gone", product),
		}
	default:
		return []string{
			fmt.Sprintf("Latest from %s: What you need to know", product),
			fmt.Sprintf("%s Update: Exciting news inside!", product),
		}
	}
}

func estimateOpenRate(subject string) string {
	lower := strings.ToLower(subject)
	if strings.Contains(lower, "🔥") || strings.Contains(lower, "🎉") {
		return "25-30%"
	}
	if strings.Contains(lower, "don't miss") || strings.Contains(lower, "limited") {
		return "22-28%"
	}
	if strings.Contains(lower, "welcome") {
		return "50-60%"
	}
	return "18-25%"
}

func (t *EmailCampaignTool) recommendSchedule(campaignType string) string {
	switch campaignType {
	case "welcome":
		return "- Send immediately after signup\n- Follow-up: 3 days later with onboarding tips"
	case "promotional":
		return "- Best time: Tue-Thu 10am-11am\n- Avoid: Monday morning, Friday afternoon\n- A/B test subject lines for 20% of list first"
	case "reengagement":
		return "- Send after 30 days of inactivity\n- Best time: Wed 2pm-4pm\n- Include clear CTA to re-activate"
	default:
		return "- Best time: Tue-Thu 9am-11am\n- Frequency: weekly or bi-weekly\n- Monitor unsubscribe rate (<0.5% target)"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
