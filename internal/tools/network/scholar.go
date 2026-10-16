package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mac/internal/configvalue"
)

func getString(values map[string]interface{}, key, fallback string) string {
	return configvalue.String(values, key, fallback)
}

func getInt(values map[string]interface{}, key string, fallback int) int {
	return configvalue.Int(values, key, fallback)
}

// ScholarSearchTool searches academic papers and manages citations.
type ScholarSearchTool struct {
	client *http.Client
}

func NewScholarSearchTool() *ScholarSearchTool {
	return &ScholarSearchTool{client: &http.Client{Timeout: 30 * time.Second}}
}

func (t *ScholarSearchTool) Name() string { return "scholar_search" }

func (t *ScholarSearchTool) Description() string {
	return "Search academic papers via Semantic Scholar API. " +
		"Returns: title, authors, year, citations, abstract, PDF links. " +
		"Supports: keyword search, author search, paper lookup by DOI/title."
}

func (t *ScholarSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type": "string", "description": "Search query: keywords, paper title, author name, or DOI",
			},
			"search_type": map[string]interface{}{
				"type": "string", "description": "keyword (default), title, author, doi",
				"enum": []string{"keyword", "title", "author", "doi"},
			},
			"limit": map[string]interface{}{
				"type": "integer", "description": "Max results (default 10, max 20)",
			},
			"year_from": map[string]interface{}{
				"type": "string", "description": "Filter: papers from this year (e.g. 2023)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *ScholarSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query := getString(params, "query", "")
	searchType := getString(params, "search_type", "keyword")
	limit := getInt(params, "limit", 10)
	yearFrom := getString(params, "year_from", "")

	if query == "" {
		return "Error: query is required", nil
	}
	if limit > 20 {
		limit = 20
	}

	// Build API URL
	baseURL := "https://api.semanticscholar.org/graph/v1/paper"
	var apiURL string

	switch searchType {
	case "keyword":
		apiURL = fmt.Sprintf("%s/search?query=%s&limit=%d&fields=title,authors,year,citationCount,abstract,externalIds,url,venue",
			baseURL, url.QueryEscape(query), limit)
		if yearFrom != "" {
			apiURL += "&year=" + yearFrom + "-"
		}
	case "title":
		apiURL = fmt.Sprintf("%s/search?query=%s&limit=%d&fields=title,authors,year,citationCount,abstract,externalIds,url",
			baseURL, url.QueryEscape(query), limit)
	case "author":
		apiURL = fmt.Sprintf("%s/search?query=%s&limit=%d&fields=title,authors,year,citationCount,abstract,externalIds",
			baseURL, url.QueryEscape(query), limit)
	case "doi":
		doi := strings.TrimPrefix(query, "https://doi.org/")
		apiURL = fmt.Sprintf("%s/%s?fields=title,authors,year,citationCount,abstract,externalIds,url",
			baseURL, url.QueryEscape(doi))
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("Search failed: %v", err), nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return fmt.Sprintf("API error %d: %s", resp.StatusCode, truncate(string(body), 500)), nil
	}

	// Parse and format results
	return formatScholarResults(body, limit), nil
}

func formatScholarResults(body []byte, limit int) string {
	var result strings.Builder
	result.WriteString("## Academic Paper Search\n\n")

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Sprintf("Results:\n```json\n%s\n```", truncate(string(body), 2000))
	}

	// Single paper result
	if _, ok := data["title"].(string); ok {
		result.WriteString(formatPaper(data))
		return result.String()
	}

	// Search results
	papers, ok := data["data"].([]interface{})
	if !ok {
		return fmt.Sprintf("No results found for your query.\n\nRaw: ```json\n%s\n```", truncate(string(body), 1000))
	}

	result.WriteString(fmt.Sprintf("Found %d results:\n\n", len(papers)))
	for i, p := range papers {
		if i >= limit {
			break
		}
		if paper, ok := p.(map[string]interface{}); ok {
			result.WriteString(formatPaper(paper))
			if i < len(papers)-1 {
				result.WriteString("---\n\n")
			}
		}
	}
	return result.String()
}

func formatPaper(p map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s\n", getMapStr(p, "title")))

	if authors, ok := p["authors"].([]interface{}); ok {
		names := make([]string, 0, len(authors))
		for _, a := range authors {
			if am, ok := a.(map[string]interface{}); ok {
				names = append(names, getMapStr(am, "name"))
			}
		}
		sb.WriteString(fmt.Sprintf("**Authors**: %s\n", strings.Join(names, ", ")))
	}

	if year, ok := p["year"].(float64); ok && year > 0 {
		sb.WriteString(fmt.Sprintf("**Year**: %d", int(year)))
	}
	if citations, ok := p["citationCount"].(float64); ok {
		sb.WriteString(fmt.Sprintf(" | **Citations**: %d", int(citations)))
	}
	sb.WriteString("\n")

	if venue, ok := p["venue"].(string); ok && venue != "" {
		sb.WriteString(fmt.Sprintf("**Venue**: %s\n", venue))
	}
	if abstract, ok := p["abstract"].(string); ok && abstract != "" {
		sb.WriteString(fmt.Sprintf("**Abstract**: %s\n", truncate(abstract, 300)))
	}
	if extIDs, ok := p["externalIds"].(map[string]interface{}); ok {
		if doi, ok := extIDs["DOI"].(string); ok {
			sb.WriteString(fmt.Sprintf("**DOI**: https://doi.org/%s\n", doi))
		}
		if arxiv, ok := extIDs["ArXiv"].(string); ok {
			sb.WriteString(fmt.Sprintf("**arXiv**: https://arxiv.org/abs/%s\n", arxiv))
		}
	}
	if paperURL, ok := p["url"].(string); ok && paperURL != "" {
		sb.WriteString(fmt.Sprintf("**URL**: %s\n", paperURL))
	}
	return sb.String()
}

func getMapStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ==================== citation_format tool ====================

type CitationTool struct{}

func (t *CitationTool) Name() string { return "citation_format" }
func (t *CitationTool) Description() string {
	return "Generate citations in multiple formats. Supports: BibTeX, APA, MLA, Chicago, Vancouver. " +
		"Provide paper details (authors, title, year, journal, DOI) and get properly formatted citations."
}
func (t *CitationTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"format": map[string]interface{}{
				"type": "string", "description": "Citation format: bibtex, apa, mla, chicago, vancouver",
				"enum": []string{"bibtex", "apa", "mla", "chicago", "vancouver"},
			},
			"authors": map[string]interface{}{
				"type": "string", "description": "Authors (e.g. Smith, J.; Jones, M.)",
			},
			"title": map[string]interface{}{
				"type": "string", "description": "Paper title",
			},
			"journal": map[string]interface{}{
				"type": "string", "description": "Journal/conference name",
			},
			"year":   map[string]interface{}{"type": "string", "description": "Publication year"},
			"volume": map[string]interface{}{"type": "string", "description": "Volume number"},
			"pages":  map[string]interface{}{"type": "string", "description": "Page numbers"},
			"doi":    map[string]interface{}{"type": "string", "description": "DOI identifier"},
		},
		"required": []string{"format", "authors", "title", "year"},
	}
}

func (t *CitationTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	format := getString(params, "format", "bibtex")
	authors := getString(params, "authors", "")
	title := getString(params, "title", "")
	journal := getString(params, "journal", "")
	year := getString(params, "year", "")
	volume := getString(params, "volume", "")
	pages := getString(params, "pages", "")
	doi := getString(params, "doi", "")

	firstAuthor := strings.Split(authors, ",")[0]
	lastName := strings.TrimSpace(strings.Split(firstAuthor, ";")[0])
	citationKey := fmt.Sprintf("%s%s", strings.ToLower(lastName), year)

	switch format {
	case "bibtex":
		return fmt.Sprintf("@article{%s,\n  author = {%s},\n  title = {%s},\n  journal = {%s},\n  year = {%s},\n  volume = {%s},\n  pages = {%s},\n  doi = {%s}\n}",
			citationKey, authors, title, journal, year, volume, pages, doi), nil

	case "apa":
		return fmt.Sprintf("%s (%s). %s. %s, %s, %s. https://doi.org/%s",
			authors, year, title, journal, volume, pages, doi), nil

	case "mla":
		return fmt.Sprintf("%s. \"%s.\" %s, vol. %s, no. %s, %s, pp. %s. DOI: %s.",
			authors, title, journal, volume, year, year, pages, doi), nil

	case "chicago":
		return fmt.Sprintf("%s. \"%s.\" %s %s (%s): %s. https://doi.org/%s.",
			authors, title, journal, volume, year, pages, doi), nil

	case "vancouver":
		return fmt.Sprintf("%s. %s. %s. %s;%s:%s. DOI: %s.",
			authors, title, journal, year, volume, pages, doi), nil
	}
	return "", nil
}
