package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ====================== Obsidian Vault Tool ======================
//
// Reads and analyzes an Obsidian vault (a directory of markdown files).
// Provides search, statistics, backlink analysis, and auto-tagging.
// The AI agent can then use read_file/write_file to update individual notes.

type ObsidianVaultTool struct{}

func NewObsidianVaultTool() *ObsidianVaultTool { return &ObsidianVaultTool{} }

func (t *ObsidianVaultTool) Name() string { return "vault_explore" }
func (t *ObsidianVaultTool) Description() string {
	return `Explore an Obsidian vault directory. Actions:
  - "stats": note count, tags, orphan notes, recent changes
  - "search": full-text search across all notes (requires "query" param)
  - "links": find forward and backlinks for a note (requires "note" param)
  - "orphans": list notes with no incoming links
  - "tags": list all tags with usage counts
  - "recent": list recently modified notes`
}
func (t *ObsidianVaultTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: stats, search, links, orphans, tags, recent",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the Obsidian vault root directory (default: ./vault)",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (for 'search' action)",
			},
			"note": map[string]interface{}{
				"type":        "string",
				"description": "Note filename without path (for 'links' action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ObsidianVaultTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	vaultPath, _ := params["path"].(string)
	if vaultPath == "" {
		vaultPath = "./vault"
	}

	if _, err := os.Stat(vaultPath); os.IsNotExist(err) {
		return fmt.Sprintf("Vault directory not found: %s. Create it or set a different path.", vaultPath), nil
	}

	switch action {
	case "stats":
		return t.stats(vaultPath)
	case "search":
		query, _ := params["query"].(string)
		if query == "" {
			return "Please provide a 'query' parameter for search.", nil
		}
		return t.search(vaultPath, query)
	case "links":
		note, _ := params["note"].(string)
		if note == "" {
			return "Please provide a 'note' parameter for link analysis.", nil
		}
		return t.links(vaultPath, note)
	case "orphans":
		return t.orphans(vaultPath)
	case "tags":
		return t.tagStats(vaultPath)
	case "recent":
		return t.recent(vaultPath)
	default:
		return fmt.Sprintf("Unknown action: %s. Use: stats, search, links, orphans, tags, recent.", action), nil
	}
}

// ====================== Vault Operations ======================

type vaultNote struct {
	Path      string
	Name      string
	Size      int64
	ModTime   time.Time
	Links     []string // outgoing wikilinks
	Backlinks []string // notes that link to this one
	Tags      []string
}

func (t *ObsidianVaultTool) scanVault(vaultPath string) ([]*vaultNote, error) {
	var notes []*vaultNote
	linkRE := regexp.MustCompile(`\[\[([^\]|#]+)(?:[|#][^\]]+)?\]\]`)
	tagRE := regexp.MustCompile(`#([a-zA-Z一-鿿_\-/]+)`)

	err := filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		relPath, _ := filepath.Rel(vaultPath, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		n := &vaultNote{
			Path:    relPath,
			Name:    strings.TrimSuffix(info.Name(), ".md"),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}

		// Extract outgoing links
		matches := linkRE.FindAllStringSubmatch(content, -1)
		seen := map[string]bool{}
		for _, m := range matches {
			link := strings.TrimSpace(m[1])
			if !seen[link] {
				n.Links = append(n.Links, link)
				seen[link] = true
			}
		}

		// Extract tags
		tagMatches := tagRE.FindAllStringSubmatch(content, -1)
		tagSeen := map[string]bool{}
		for _, m := range tagMatches {
			tag := strings.ToLower(m[1])
			if !tagSeen[tag] {
				n.Tags = append(n.Tags, tag)
				tagSeen[tag] = true
			}
		}

		notes = append(notes, n)
		return nil
	})

	// Compute backlinks
	nameIndex := map[string]*vaultNote{}
	for _, n := range notes {
		nameIndex[strings.ToLower(n.Name)] = n
	}
	for _, n := range notes {
		for _, link := range n.Links {
			if target, ok := nameIndex[strings.ToLower(link)]; ok {
				target.Backlinks = append(target.Backlinks, n.Name)
			}
		}
	}

	return notes, err
}

func (t *ObsidianVaultTool) stats(vaultPath string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	totalLinks := 0
	totalTags := 0
	orphanCount := 0
	var allTags []string
	for _, n := range notes {
		totalLinks += len(n.Links)
		totalTags += len(n.Tags)
		if len(n.Backlinks) == 0 && len(n.Links) == 0 {
			orphanCount++
		}
		allTags = append(allTags, n.Tags...)
	}
	// Top 10 tags
	tagCount := map[string]int{}
	for _, t := range allTags {
		tagCount[t]++
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range tagCount {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	topN := 10
	if len(sorted) < topN {
		topN = len(sorted)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Vault Stats: %s\n\n", vaultPath))
	sb.WriteString(fmt.Sprintf("- **Notes**: %d\n", len(notes)))
	sb.WriteString(fmt.Sprintf("- **Links**: %d total (avg %.1f per note)\n", totalLinks, float64(totalLinks)/float64(max(len(notes), 1))))
	sb.WriteString(fmt.Sprintf("- **Tags**: %d unique\n", len(tagCount)))
	sb.WriteString(fmt.Sprintf("- **Orphans**: %d notes with no links\n\n", orphanCount))
	sb.WriteString("### Top Tags\n")
	for _, kv := range sorted[:topN] {
		sb.WriteString(fmt.Sprintf("- #%s (%d)\n", kv.k, kv.v))
	}
	if orphanCount > 0 {
		sb.WriteString("\n### Orphan Notes\n")
		for _, n := range notes {
			if len(n.Backlinks) == 0 && len(n.Links) == 0 && orphanCount <= 20 {
				sb.WriteString(fmt.Sprintf("- %s\n", n.Name))
			}
		}
	}
	return sb.String(), nil
}

func (t *ObsidianVaultTool) search(vaultPath, query string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	queryLower := strings.ToLower(query)
	var results []*vaultNote
	for _, n := range notes {
		data, err := os.ReadFile(filepath.Join(vaultPath, n.Path))
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(strings.ToLower(content), queryLower) ||
			strings.Contains(strings.ToLower(n.Name), queryLower) {
			results = append(results, n)
		}
	}
	if len(results) == 0 {
		return fmt.Sprintf("No notes found matching '%s'", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Search: '%s' (%d results)\n\n", query, len(results)))
	for _, n := range results {
		backlinks := ""
		if len(n.Backlinks) > 0 {
			backlinks = fmt.Sprintf(" (linked by: %s)", strings.Join(n.Backlinks[:min(3, len(n.Backlinks))], ", "))
		}
		sb.WriteString(fmt.Sprintf("- **[[%s]]**%s — %d links, modified %s\n",
			n.Name, backlinks, len(n.Links), n.ModTime.Format("01-02 15:04")))
	}
	return sb.String(), nil
}

func (t *ObsidianVaultTool) links(vaultPath, noteName string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	nameIndex := map[string]*vaultNote{}
	for _, n := range notes {
		nameIndex[strings.ToLower(n.Name)] = n
	}
	n, ok := nameIndex[strings.ToLower(noteName)]
	if !ok {
		return fmt.Sprintf("Note '%s' not found in vault.", noteName), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Links for [[%s]]\n\n", n.Name))
	sb.WriteString(fmt.Sprintf("### Outgoing (%d)\n", len(n.Links)))
	for _, l := range n.Links {
		exists := ""
		if _, ok := nameIndex[strings.ToLower(l)]; !ok {
			exists = " ⚠️ (not found)"
		}
		sb.WriteString(fmt.Sprintf("- [[%s]]%s\n", l, exists))
	}
	sb.WriteString(fmt.Sprintf("\n### Backlinks (%d)\n", len(n.Backlinks)))
	if len(n.Backlinks) == 0 {
		sb.WriteString("- None — this is an orphan note\n")
	} else {
		for _, b := range n.Backlinks {
			sb.WriteString(fmt.Sprintf("- [[%s]]\n", b))
		}
	}
	if len(n.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("\n### Tags\n"))
		for _, t := range n.Tags {
			sb.WriteString(fmt.Sprintf("- #%s\n", t))
		}
	}
	return sb.String(), nil
}

func (t *ObsidianVaultTool) orphans(vaultPath string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	var orphans []*vaultNote
	for _, n := range notes {
		if len(n.Backlinks) == 0 {
			orphans = append(orphans, n)
		}
	}
	if len(orphans) == 0 {
		return "No orphan notes found. Every note has at least one incoming link.", nil
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ModTime.After(orphans[j].ModTime) })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Orphan Notes (%d)\n\n", len(orphans)))
	for _, n := range orphans {
		outLinks := ""
		if len(n.Links) > 0 {
			outLinks = fmt.Sprintf(" → %s", strings.Join(n.Links[:min(3, len(n.Links))], ", "))
		}
		sb.WriteString(fmt.Sprintf("- **[[%s]]**%s (modified %s)\n",
			n.Name, outLinks, n.ModTime.Format("01-02")))
	}
	sb.WriteString("\n💡 Run `/obsidian-connect` to suggest links for these notes.")
	return sb.String(), nil
}

func (t *ObsidianVaultTool) tagStats(vaultPath string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	tagCount := map[string]int{}
	for _, n := range notes {
		for _, t := range n.Tags {
			tagCount[t]++
		}
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range tagCount {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Tags (%d unique)\n\n", len(sorted)))
	for _, kv := range sorted {
		sb.WriteString(fmt.Sprintf("- #%s (%d notes)\n", kv.k, kv.v))
	}
	return sb.String(), nil
}

func (t *ObsidianVaultTool) recent(vaultPath string) (string, error) {
	notes, err := t.scanVault(vaultPath)
	if err != nil {
		return "", err
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].ModTime.After(notes[j].ModTime) })
	limit := min(20, len(notes))
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Recently Modified (%d)\n\n", limit))
	for _, n := range notes[:limit] {
		sb.WriteString(fmt.Sprintf("- **[[%s]]** — %s (%d links)\n",
			n.Name, n.ModTime.Format("01-02 15:04"), len(n.Links)))
	}
	return sb.String(), nil
}
