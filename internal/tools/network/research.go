package network

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/textutil"
)

// ArXivTool searches arXiv directly via its API.
type ArXivTool struct {
	client *http.Client
}

func NewArXivTool() *ArXivTool {
	return &ArXivTool{client: &http.Client{Timeout: 30 * time.Second}}
}

func (t *ArXivTool) Name() string { return "arxiv_search" }

func (t *ArXivTool) Description() string {
	return "Search arXiv for preprints. Returns: title, authors, abstract, PDF link, categories, submission date. " +
		"Supports keyword, author, and ID search. Much faster than Semantic Scholar for recent papers."
}

func (t *ArXivTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type": "string", "description": "Search query: keywords, author name, or arXiv ID (e.g. 2301.12345)",
			},
			"search_type": map[string]interface{}{
				"type": "string", "description": "keyword (default), author, id",
				"enum": []string{"keyword", "author", "id"},
			},
			"max_results": map[string]interface{}{
				"type": "integer", "description": "Max results (default 10, max 30)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *ArXivTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query := getString(params, "query", "")
	searchType := getString(params, "search_type", "keyword")
	maxResults := getInt(params, "max_results", 10)
	if maxResults > 30 {
		maxResults = 30
	}

	if query == "" {
		return "Error: query is required", nil
	}

	var apiURL string
	switch searchType {
	case "id":
		apiURL = fmt.Sprintf("http://export.arxiv.org/api/query?id_list=%s&max_results=1", url.QueryEscape(query))
	case "author":
		apiURL = fmt.Sprintf("http://export.arxiv.org/api/query?search_query=au:%s&start=0&max_results=%d&sortBy=lastUpdatedDate&sortOrder=descending",
			url.QueryEscape(query), maxResults)
	default:
		apiURL = fmt.Sprintf("http://export.arxiv.org/api/query?search_query=all:%s&start=0&max_results=%d&sortBy=relevance&sortOrder=descending",
			url.QueryEscape(query), maxResults)
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("arXiv API error: %v", err), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	return parseArXivResponse(string(body), maxResults), nil
}

func parseArXivResponse(xmlData string, limit int) string {
	type ArxivEntry struct {
		Title     string   `xml:"title"`
		Summary   string   `xml:"summary"`
		Authors   []string `xml:"author>name"`
		Published string   `xml:"published"`
		Updated   string   `xml:"updated"`
		ID        string   `xml:"id"`
		PDFLink   string   `xml:"link"`
		Category  string   `xml:"category"`
	}

	var result strings.Builder
	result.WriteString("## arXiv Search Results\n\n")

	// Simple XML parsing for arXiv Atom feed
	entries := strings.Split(xmlData, "<entry>")
	if len(entries) < 2 {
		return "No results found. Try different keywords or check arxiv.org directly."
	}

	count := 0
	for i, entry := range entries {
		if i == 0 || count >= limit {
			continue
		}

		title := extractXMLTag(entry, "title")
		summary := extractXMLTag(entry, "summary")
		published := extractXMLTag(entry, "published")
		arxivID := extractXMLTag(entry, "id")

		// Clean arXiv ID
		arxivID = strings.TrimPrefix(arxivID, "http://arxiv.org/abs/")

		authors := extractXMLTags(entry, "name")
		categories := extractXMLTags(entry, "category")

		if title == "" {
			continue
		}
		count++

		result.WriteString(fmt.Sprintf("### %d. %s\n", count, _cleanArxivText(title)))
		if len(authors) > 0 {
			joined := strings.Join(authors[:MinInt(5, len(authors))], ", ")
			if len(authors) > 5 {
				joined += " et al."
			}
			result.WriteString(fmt.Sprintf("**Authors**: %s\n", joined))
		}
		if published != "" {
			result.WriteString(fmt.Sprintf("**Published**: %s\n", published[:10]))
		}
		if len(categories) > 0 {
			result.WriteString(fmt.Sprintf("**Categories**: %s\n", strings.Join(categories[:MinInt(3, len(categories))], ", ")))
		}
		if summary != "" {
			result.WriteString(fmt.Sprintf("**Abstract**: %s\n", textutil.Truncate(_cleanArxivText(summary), 400)))
		}
		result.WriteString(fmt.Sprintf("**arXiv**: https://arxiv.org/abs/%s\n", arxivID))
		result.WriteString(fmt.Sprintf("**PDF**: https://arxiv.org/pdf/%s.pdf\n", arxivID))
		result.WriteString("\n---\n\n")
	}

	result.WriteString(fmt.Sprintf("Found %d papers. Open PDFs directly or use web_fetch for full text.\n", count))
	return result.String()
}

func extractXMLTag(xml, tag string) string {
	start := strings.Index(xml, "<"+tag+">")
	if start < 0 {
		start = strings.Index(xml, "<"+tag+" ")
	}
	if start < 0 {
		return ""
	}
	if strings.Contains(xml[start:], "</"+tag+">") {
		contentStart := strings.Index(xml[start:], ">") + start + 1
		contentEnd := strings.Index(xml[contentStart:], "</"+tag+">") + contentStart
		return strings.TrimSpace(xml[contentStart:contentEnd])
	}
	return ""
}

func extractXMLTags(xml, tag string) []string {
	var results []string
	for {
		start := strings.Index(xml, "<"+tag+">")
		if start < 0 {
			break
		}
		end := strings.Index(xml[start:], "</"+tag+">")
		if end < 0 {
			break
		}
		results = append(results, strings.TrimSpace(xml[start+len(tag)+2:start+end]))
		xml = xml[start+end+len(tag)+3:]
	}
	return results
}

func _cleanArxivText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	return strings.TrimSpace(s)
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ==================== latex_gen tool ====================

type LaTeXTool struct {
	workspace string
}

func NewLaTeXTool(workspace string) *LaTeXTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "latex"), 0755)
	return &LaTeXTool{workspace: workspace}
}

func (t *LaTeXTool) Name() string { return "latex_gen" }

func (t *LaTeXTool) Description() string {
	return "Generate LaTeX documents: academic papers, reports, presentations (beamer), CVs. " +
		"Creates compilable .tex files with proper structure. Use for: paper drafting, report generation, slide decks."
}

func (t *LaTeXTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"doc_type": map[string]interface{}{
				"type": "string", "description": "paper (academic), report (technical), beamer (slides), cv (resume), letter",
				"enum": []string{"paper", "report", "beamer", "cv", "letter"},
			},
			"title": map[string]interface{}{
				"type": "string", "description": "Document title",
			},
			"author": map[string]interface{}{
				"type": "string", "description": "Author name(s)",
			},
			"abstract": map[string]interface{}{
				"type": "string", "description": "Abstract text (for paper type)",
			},
			"sections": map[string]interface{}{
				"type": "string", "description": "Section titles, comma-separated. e.g. 'Introduction, Methods, Results, Conclusion'",
			},
			"references": map[string]interface{}{
				"type": "string", "description": "BibTeX references (for paper type)",
			},
		},
		"required": []string{"doc_type", "title"},
	}
}

func (t *LaTeXTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	docType := getString(params, "doc_type", "paper")
	title := getString(params, "title", "")
	author := getString(params, "author", "Author")
	abstract := getString(params, "abstract", "")
	sections := getString(params, "sections", "")
	bibRefs := getString(params, "references", "")

	if title == "" {
		return "Error: title is required", nil
	}

	latex := t.generateLaTeX(docType, title, author, abstract, sections, bibRefs)

	filename := fmt.Sprintf("%s_%s.tex", docType, time.Now().Format("20060102_150405"))
	filePath := filepath.Join(t.workspace, ".openkitty", "storage", "latex", filename)
	os.WriteFile(filePath, []byte(latex), 0644)

	compileCmd := ""
	if docType == "beamer" {
		compileCmd = "pdflatex " + filename + " && pdflatex " + filename
	} else if bibRefs != "" {
		compileCmd = "pdflatex " + filename + " && bibtex " + strings.TrimSuffix(filename, ".tex") + " && pdflatex " + filename + " && pdflatex " + filename
	} else {
		compileCmd = "pdflatex " + filename + " && pdflatex " + filename
	}

	return fmt.Sprintf("## LaTeX Document Generated\n\n**Type**: %s\n**File**: `%s`\n\nTo compile:\n```bash\ncd storage/latex\n%s\n```\n\nPreview:\n```latex\n%s\n```",
		docType, filePath, compileCmd, textutil.Truncate(latex, 1500)), nil
}

func (t *LaTeXTool) generateLaTeX(docType, title, author, abstract, sections, bibRefs string) string {
	var sb strings.Builder

	switch docType {
	case "paper":
		sb.WriteString(`\documentclass[12pt,a4paper]{article}
\usepackage[utf8]{inputenc}
\usepackage{amsmath,amssymb}
\usepackage{graphicx}
\usepackage{hyperref}
\usepackage{natbib}
\usepackage[margin=1in]{geometry}

\title{` + title + `}
\author{` + author + `}
\date{\today}

\begin{document}
\maketitle

\begin{abstract}
` + abstract + `
\end{abstract}

`)

		for i, sec := range strings.Split(sections, ",") {
			sec = strings.TrimSpace(sec)
			if sec != "" {
				sb.WriteString(fmt.Sprintf("\\section{%s}\n\\label{sec:%d}\n\nContent for %s...\n\n", sec, i+1, sec))
			}
		}

		if bibRefs != "" {
			sb.WriteString(`\bibliographystyle{plainnat}
\bibliography{references}
`)
		}

		sb.WriteString(`\end{document}`)

	case "beamer":
		sb.WriteString(fmt.Sprintf(`\documentclass{beamer}
\usetheme{Madrid}
\title{%s}
\author{%s}
\date{\today}

\begin{document}

\begin{frame}
\titlepage
\end{frame}

`, title, author))

		for _, sec := range strings.Split(sections, ",") {
			sec = strings.TrimSpace(sec)
			if sec != "" {
				sb.WriteString(fmt.Sprintf(`\begin{frame}{%s}
\begin{itemize}
\item Point 1
\item Point 2
\item Point 3
\end{itemize}
\end{frame}

`, sec))
			}
		}

		sb.WriteString(`\begin{frame}{Thank You}
\begin{center}
\Large Questions?
\end{center}
\end{frame}

\end{document}`)

	case "report":
		sb.WriteString(fmt.Sprintf(`\documentclass[12pt,a4paper]{report}
\usepackage[utf8]{inputenc}
\usepackage{graphicx}
\usepackage{hyperref}
\usepackage[margin=1in]{geometry}

\title{%s}
\author{%s}
\date{\today}

\begin{document}
\maketitle
\tableofcontents

`, title, author))

		for i, sec := range strings.Split(sections, ",") {
			sec = strings.TrimSpace(sec)
			if sec != "" {
				sb.WriteString(fmt.Sprintf("\\chapter{%s}\nContent for %s...\n\n", sec, sec))
				_ = i
			}
		}
		sb.WriteString(`\end{document}`)

	case "cv":
		sb.WriteString(fmt.Sprintf(`\documentclass[11pt,a4paper]{article}
\usepackage[utf8]{inputenc}
\usepackage[margin=0.8in]{geometry}

\begin{document}
\begin{center}
{\LARGE %s}\\[4pt]
{\large %s}
\end{center}

\section*{Summary}
Professional with extensive experience...

\section*{Experience}
\subsection*{Position — Company (Year--Year)}
\begin{itemize}
\item Achievement 1
\item Achievement 2
\end{itemize}

\section*{Education}
\subsection*{Degree — Institution (Year)}
Details...

\section*{Skills}
\begin{itemize}
\item Skill category: technologies
\item Skill category: methodologies
\end{itemize}

\end{document}`, author, "Contact info"))

	default:
		sb.WriteString(fmt.Sprintf(`\documentclass[12pt]{letter}
\begin{document}
\begin{letter}{Recipient\\Address}

\opening{Dear Sir/Madam,}
%s
\closing{Sincerely,}
%s
\end{letter}
\end{document}`, title, author))
	}

	return sb.String()
}

// ==================== literature_review tool ====================

type LiteratureReviewTool struct{}

func (t *LiteratureReviewTool) Name() string { return "literature_review" }

func (t *LiteratureReviewTool) Description() string {
	return "Generate a structured literature review outline. Provide a topic and key papers, " +
		"get a properly structured review template with: introduction, thematic sections, comparison table, gaps, conclusion."
}

func (t *LiteratureReviewTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"topic": map[string]interface{}{
				"type": "string", "description": "Research topic for the literature review",
			},
			"papers": map[string]interface{}{
				"type": "string", "description": "Key papers (one per line): Title, Authors (Year). Key findings.",
			},
			"themes": map[string]interface{}{
				"type": "string", "description": "Thematic sections to organize by (comma-separated)",
			},
		},
		"required": []string{"topic"},
	}
}

func (t *LiteratureReviewTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	topic := getString(params, "topic", "")
	papers := getString(params, "papers", "")
	themes := getString(params, "themes", "")

	if topic == "" {
		return "Error: topic is required", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("# Literature Review: %s\n\n", topic))

	result.WriteString("## 1. Introduction\n")
	result.WriteString(fmt.Sprintf("This review surveys the literature on %s. ", topic))
	result.WriteString("We examine key contributions, identify thematic clusters, and highlight research gaps.\n\n")

	if themes != "" {
		result.WriteString("## 2. Thematic Analysis\n\n")
		for i, theme := range strings.Split(themes, ",") {
			theme = strings.TrimSpace(theme)
			result.WriteString(fmt.Sprintf("### 2.%d %s\n", i+1, theme))
			result.WriteString("- [Paper citations and discussion for this theme]\n\n")
		}
	}

	if papers != "" {
		result.WriteString("## 3. Key Papers\n\n")
		result.WriteString("| # | Paper | Key Contribution | Methodology |\n")
		result.WriteString("|---|-------|-----------------|-------------|\n")
		for i, paper := range strings.Split(papers, "\n") {
			paper = strings.TrimSpace(paper)
			if paper == "" {
				continue
			}
			result.WriteString(fmt.Sprintf("| %d | %s | | |\n", i+1, paper))
		}
		result.WriteString("\n")
	}

	result.WriteString("## 4. Comparison\n\n")
	result.WriteString("| Dimension | Key Findings | Consensus |\n")
	result.WriteString("|-----------|-------------|----------|\n")
	result.WriteString("| Methodology | | |\n")
	result.WriteString("| Performance | | |\n")
	result.WriteString("| Limitations | | |\n\n")

	result.WriteString("## 5. Research Gaps\n")
	result.WriteString("- Gap 1: [describe identified gap]\n")
	result.WriteString("- Gap 2: [describe identified gap]\n")
	result.WriteString("- Future work: [suggested directions]\n\n")

	result.WriteString("## 6. Conclusion\n")
	result.WriteString("Summary of key findings, consensus areas, and promising directions for future research.\n")
	result.WriteString(fmt.Sprintf("\n---\n*Generated literature review template. Fill in the details from your research.*\n"))

	return result.String(), nil
}
