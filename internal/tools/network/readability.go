package network

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// contentBlock represents a scored DOM element candidate for main content.
type contentBlock struct {
	node    *goquery.Selection
	score   float64
	textLen int
	linkLen int
}

// ExtractMainContent finds the highest-scoring content block (readability-style)
// and returns clean markdown. Returns empty string if nothing found.
func ExtractMainContent(doc *goquery.Document) string {
	if doc == nil {
		return ""
	}

	// Remove obvious noise before scoring
	doc.Find("script, style, nav, footer, iframe, noscript, " +
		"[role=nav], [role=banner], [role=contentinfo], " +
		".nav, .navbar, .footer, .sidebar, .menu, .ad, " +
		".social-share, .comments, .related-posts").Remove()

	// Score all candidate blocks
	var candidates []contentBlock
	doc.Find("article, main, [role=main], section, div, p, pre, blockquote, table, ul, ol, dl").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		textLen := len(strings.TrimSpace(text))
		if textLen < 50 {
			return
		}
		linkLen := 0
		s.Find("a").Each(func(j int, a *goquery.Selection) {
			linkLen += len(strings.TrimSpace(a.Text()))
		})
		linkRatio := float64(linkLen) / float64(max(textLen, 1))
		if linkRatio > 0.5 && textLen < 500 {
			return
		}
		score := scoreContentBlock(s, textLen, linkRatio)
		candidates = append(candidates, contentBlock{s, score, textLen, linkLen})
	})

	if len(candidates) == 0 {
		return ""
	}

	// Sort by score descending
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	best := candidates[0]
	markdown := blockToMarkdown(best.node)

	// If too short, try merging top 3 blocks
	if len(markdown) < 500 && len(candidates) > 1 {
		var merged strings.Builder
		for i := 0; i < min(3, len(candidates)); i++ {
			merged.WriteString(blockToMarkdown(candidates[i].node))
			merged.WriteString("\n\n")
		}
		markdown = merged.String()
	}

	return markdown
}

// scoreContentBlock assigns a quality score to a DOM element.
func scoreContentBlock(s *goquery.Selection, textLen int, linkRatio float64) float64 {
	score := float64(textLen) / 100.0

	switch goquery.NodeName(s) {
	case "article", "main":
		score += 50
	case "section":
		score += 15
	case "p":
		score += 5 * float64(min(1, textLen/200))
	case "pre", "blockquote":
		score += 10
	case "table":
		score += float64(textLen) / 200.0
	}

	class, _ := s.Attr("class")
	id, _ := s.Attr("id")
	combined := strings.ToLower(class + " " + id)

	for _, h := range []string{"content", "article", "post", "body", "main", "entry", "text", "story"} {
		if strings.Contains(combined, h) {
			score += 25
			break
		}
	}
	for _, h := range []string{"nav", "footer", "header", "sidebar", "menu", "ad", "comment", "related", "recommend"} {
		if strings.Contains(combined, h) {
			score -= 30
			break
		}
	}

	if linkRatio > 0.3 {
		score -= linkRatio * 40
	}

	commas := strings.Count(s.Text(), ",") + strings.Count(s.Text(), ".")
	score += float64(commas) / float64(max(textLen, 1)) * 100

	return score
}

// blockToMarkdown converts a scored content block to clean markdown.
func blockToMarkdown(s *goquery.Selection) string {
	var b strings.Builder

	s.Find("h1, h2, h3, h4, h5, h6").Each(func(i int, h *goquery.Selection) {
		lv := 1
		switch goquery.NodeName(h) {
		case "h2":
			lv = 2
		case "h3":
			lv = 3
		case "h4":
			lv = 4
		case "h5":
			lv = 5
		case "h6":
			lv = 6
		}
		b.WriteString(strings.Repeat("#", lv) + " " + strings.TrimSpace(h.Text()) + "\n\n")
	})

	s.Find("p").Each(func(i int, para *goquery.Selection) {
		t := strings.TrimSpace(para.Text())
		if len(t) > 20 {
			b.WriteString(t + "\n\n")
		}
	})

	s.Find("ul, ol").Each(func(i int, list *goquery.Selection) {
		list.Find("li").Each(func(j int, li *goquery.Selection) {
			b.WriteString("- " + strings.TrimSpace(li.Text()) + "\n")
		})
		b.WriteString("\n")
	})

	s.Find("pre, code").Each(func(i int, code *goquery.Selection) {
		b.WriteString("```\n" + code.Text() + "\n```\n\n")
	})

	s.Find("table").Each(func(i int, table *goquery.Selection) {
		table.Find("tr").Each(func(j int, tr *goquery.Selection) {
			var cells []string
			tr.Find("th, td").Each(func(k int, cell *goquery.Selection) {
				cells = append(cells, strings.TrimSpace(cell.Text()))
			})
			if len(cells) > 0 {
				b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
				if j == 0 {
					b.WriteString("|" + strings.Repeat("---|", len(cells)) + "\n")
				}
			}
		})
		b.WriteString("\n")
	})

	r := strings.TrimSpace(b.String())
	if len(r) < 100 {
		r = strings.TrimSpace(s.Text())
	}
	return r
}

// ExtractCSSTokens extracts key CSS design tokens for design analysis.
func ExtractCSSTokens(doc *goquery.Document) string {
	if doc == nil {
		return ""
	}
	var tokens []string
	colors := make(map[string]int)
	doc.Find("*").Each(func(i int, s *goquery.Selection) {
		style, _ := s.Attr("style")
		for _, m := range regexp.MustCompile(`#[0-9a-fA-F]{3,8}`).FindAllString(style, -1) {
			colors[m]++
		}
	})
	if len(colors) > 0 {
		tokens = append(tokens, "**Key Colors:**")
		type kv struct {
			k string
			v int
		}
		var sorted []kv
		for k, v := range colors {
			sorted = append(sorted, kv{k, v})
		}
		for i := 0; i < len(sorted)-1; i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].v > sorted[i].v {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		for i, c := range sorted {
			if i >= 8 {
				break
			}
			tokens = append(tokens, "- `"+c.k+"` ("+formatInt(c.v)+" uses)")
		}
	}
	styleText := ""
	doc.Find("style").Each(func(i int, s *goquery.Selection) { styleText += s.Text() })
	if styleText != "" {
		if m := regexp.MustCompile(`font-family:\s*([^;]+)`).FindAllString(styleText, 3); len(m) > 0 {
			tokens = append(tokens, "\n**Fonts:**")
			for _, f := range m {
				tokens = append(tokens, "- "+f)
			}
		}
		if strings.Contains(styleText, "backdrop-filter") || strings.Contains(styleText, "blur") {
			tokens = append(tokens, "\n**Glassmorphism:** backdrop-filter blur detected")
		}
		if strings.Contains(styleText, "gradient") {
			if m := regexp.MustCompile(`gradient\([^)]+\)`).FindAllString(styleText, 3); len(m) > 0 {
				tokens = append(tokens, "\n**Gradients:**")
				for _, g := range m {
					tokens = append(tokens, "- "+truncate(g, 80))
				}
			}
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, "\n")
}

func formatInt(n int) string { return strings.TrimSpace(fmt.Sprint(n)) }
