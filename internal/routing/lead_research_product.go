package routing

import (
	"net/url"
	"strings"
)

// LeadResearchProductSource is an operator-configured product identity hint.
// URLs remain candidates until the research flow opens and verifies them.
type LeadResearchProductSource struct {
	Name         string   `json:"name" yaml:"name"`
	Aliases      []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	OfficialURLs []string `json:"official_urls" yaml:"official_urls"`
	Note         string   `json:"note,omitempty" yaml:"note,omitempty"`
}

func DedupeLeadResearchProductSources(sources []LeadResearchProductSource) []LeadResearchProductSource {
	seen := make(map[string]bool)
	out := make([]LeadResearchProductSource, 0, len(sources))
	for _, source := range sources {
		source.Name = strings.TrimSpace(source.Name)
		if source.Name == "" {
			continue
		}
		urls := make([]string, 0, len(source.OfficialURLs))
		for _, rawURL := range source.OfficialURLs {
			if normalized := NormalizeLeadResearchURL(rawURL); normalized != "" {
				urls = append(urls, normalized)
			}
		}
		source.OfficialURLs = urls
		if len(source.OfficialURLs) == 0 {
			continue
		}
		key := strings.ToLower(source.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, source)
	}
	return out
}

func MatchLeadResearchProductSources(sources []LeadResearchProductSource, task string) []LeadResearchProductSource {
	lowerTask := strings.ToLower(strings.TrimSpace(task))
	if lowerTask == "" {
		return nil
	}
	var matched []LeadResearchProductSource
	for _, source := range DedupeLeadResearchProductSources(sources) {
		aliases := append([]string{source.Name}, source.Aliases...)
		for _, alias := range aliases {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" && strings.Contains(lowerTask, alias) {
				matched = append(matched, source)
				break
			}
		}
	}
	return matched
}

func NormalizeLeadResearchURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	parsed.Fragment = ""
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return strings.TrimRight(parsed.String(), "/")
}

func LeadResearchTextContainsURL(text, normalizedURL string) bool {
	if normalizedURL == "" {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, candidate := range []string{strings.ToLower(normalizedURL), strings.ToLower(normalizedURL) + "/"} {
		if strings.Contains(lowerText, candidate) {
			return true
		}
	}
	return false
}
