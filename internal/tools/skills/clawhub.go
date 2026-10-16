package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"mac/internal/skillpath"
)

// ClawHubTool downloads skills from ClawHub and adapts them to openkitty format.
type SkillScanSummary struct {
	Blocking       bool
	FindingCount   int
	Summary        string
	VerboseSummary string
}

type SkillScanner func(string) (SkillScanSummary, error)

// ClawHubTool downloads skills from ClawHub and adapts them to OpenKitty format.
type ClawHubTool struct {
	skillsDir string
	scanner   SkillScanner
}

func NewClawHubTool(skillsDir string) *ClawHubTool {
	if strings.TrimSpace(skillsDir) == "" {
		skillsDir = skillpath.ExecutableSkillsDir()
	}
	return &ClawHubTool{skillsDir: skillsDir}
}

func NewClawHubToolWithScanner(skillsDir string, scanner SkillScanner) *ClawHubTool {
	tool := NewClawHubTool(skillsDir)
	tool.scanner = scanner
	return tool
}

func (t *ClawHubTool) scanSkillDir(path string) (SkillScanSummary, error) {
	if t.scanner == nil {
		return SkillScanSummary{}, fmt.Errorf("skill scanner is not configured")
	}
	return t.scanner(path)
}

func (t *ClawHubTool) Name() string { return "clawhub" }

func (t *ClawHubTool) Description() string {
	return "Search and install skills from ClawHub (openclaw skill registry). " +
		"Downloads openclaw-format SKILL.md files and auto-converts them to openkitty format. " +
		"Actions: search, install, list, convert"
}

func (t *ClawHubTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: search (find skills), install (download & convert), list (show installed), convert (adapt a local SKILL.md)",
				"enum":        []string{"search", "install", "list", "convert"},
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query or skill slug to install",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Source: clawhub (search online), github (clone from GitHub repo), local (adapt local file)",
				"enum":        []string{"clawhub", "github", "local"},
			},
			"github_url": map[string]interface{}{
				"type":        "string",
				"description": "GitHub URL to clone skill from (when source=github)",
			},
			"local_path": map[string]interface{}{
				"type":        "string",
				"description": "Local SKILL.md path to convert (when source=local)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ClawHubTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || strings.TrimSpace(t.skillsDir) == "" {
		return "", fmt.Errorf("skill install directory is unavailable")
	}
	if err := os.MkdirAll(t.skillsDir, 0755); err != nil {
		return "", fmt.Errorf("create skill install directory: %w", err)
	}
	action, _ := params["action"].(string)
	query, _ := params["query"].(string)
	source, _ := params["source"].(string)
	githubURL, _ := params["github_url"].(string)
	localPath, _ := params["local_path"].(string)

	if source == "" {
		source = "clawhub"
	}

	switch action {
	case "search":
		return t.searchSkills(query)
	case "install":
		switch source {
		case "github":
			return t.installFromGitHub(githubURL)
		case "local":
			return t.convertLocalSkill(localPath)
		default:
			return t.installFromClawHub(query)
		}
	case "list":
		return t.listInstalledSkills()
	case "convert":
		return t.convertLocalSkill(localPath)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// searchSkills searches ClawHub for skills matching the query.
func (t *ClawHubTool) searchSkills(query string) (string, error) {
	if query == "" {
		query = "popular"
	}

	// Try npx clawhub search first
	cmd := exec.Command("npx", "--yes", "clawhub@latest", "search", query, "--limit", "10", "--json")
	cmd.Dir = t.skillsDir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}

	// Fallback: search GitHub topics
	url := fmt.Sprintf("https://api.github.com/search/repositories?q=topic:clawhub-skill+%s&sort=stars&per_page=10", query)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "openkitty-clawhub/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Items []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			HTMLURL     string `json:"html_url"`
			Stars       int    `json:"stargazers_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse search results: %w", err)
	}

	if len(result.Items) == 0 {
		return fmt.Sprintf("No skills found for '%s'. Try a different keyword.", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## ClawHub Search: '%s'\n\n", query))
	for i, item := range result.Items {
		sb.WriteString(fmt.Sprintf("%d. **%s** ⭐%d\n   %s\n   %s\n\n",
			i+1, item.Name, item.Stars, item.Description, item.HTMLURL))
	}
	sb.WriteString("\nTo install: `clawhub install <name> --source github --github_url <url>`")
	return sb.String(), nil
}

// installFromGitHub clones a skill repo from GitHub and converts it.
func (t *ClawHubTool) installFromGitHub(githubURL string) (string, error) {
	if githubURL == "" {
		return "", fmt.Errorf("github_url is required for source=github")
	}

	// Extract repo name from URL
	parts := strings.Split(strings.TrimRight(githubURL, "/"), "/")
	repoName := parts[len(parts)-1]
	tmpDir := filepath.Join(os.TempDir(), "clawhub_"+repoName+"_"+fmt.Sprintf("%d", time.Now().UnixNano()))

	// Clone
	cmd := exec.Command("git", "clone", "--depth", "1", githubURL, tmpDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone failed: %s\n%w", string(output), err)
	}
	defer os.RemoveAll(tmpDir)

	return t.convertAndInstall(tmpDir, repoName)
}

// installFromClawHub installs a skill by slug using npx clawhub.
func (t *ClawHubTool) installFromClawHub(slug string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("query (skill slug) is required for install")
	}

	cmd := exec.Command("npx", "--yes", "clawhub@latest", "install", slug, "--json")
	cmd.Dir = t.skillsDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try to find on GitHub
		return t.searchAndInstallFromGitHub(slug)
	}
	return string(output), nil
}

// searchAndInstallFromGitHub searches GitHub for a skill and installs the first match.
func (t *ClawHubTool) searchAndInstallFromGitHub(slug string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/search/repositories?q=%s+topic:clawhub-skill&per_page=1", slug)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "openkitty-clawhub/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub search failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Items []struct {
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	json.Unmarshal(body, &result)

	if len(result.Items) == 0 {
		return "", fmt.Errorf("skill '%s' not found on ClawHub or GitHub. Try searching first.", slug)
	}

	return t.installFromGitHub(result.Items[0].HTMLURL)
}

// convertLocalSkill converts a local SKILL.md from openclaw to openkitty format.
func (t *ClawHubTool) convertLocalSkill(localPath string) (string, error) {
	if localPath == "" {
		return "", fmt.Errorf("local_path is required for convert action")
	}

	content, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	converted, skillName, err := convertOpenClawToOpenKitty(string(content))
	if err != nil {
		return "", fmt.Errorf("convert: %w", err)
	}

	// Save to skills directory
	skillDir := filepath.Join(t.skillsDir, skillName)
	os.MkdirAll(skillDir, 0755)
	outPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(outPath, []byte(converted), 0644); err != nil {
		return "", fmt.Errorf("write converted skill: %w", err)
	}

	return fmt.Sprintf("Converted and saved: %s → skills/%s/SKILL.md", localPath, skillName), nil
}

// convertAndInstall converts all SKILL.md files found in a directory tree.
func (t *ClawHubTool) convertAndInstall(dir, defaultName string) (string, error) {
	var results []string

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := strings.ToLower(info.Name())
		if base != "skill.md" && base != "readme.md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		converted, skillName, err := convertOpenClawToOpenKitty(string(content))
		if err != nil {
			results = append(results, fmt.Sprintf("⚠️  %s: conversion skipped (%v)", filepath.Base(filepath.Dir(path)), err))
			return nil
		}

		if skillName == "" {
			skillName = defaultName
		}

		skillDir := filepath.Join(t.skillsDir, skillName)
		os.MkdirAll(skillDir, 0755)
		outPath := filepath.Join(skillDir, "SKILL.md")
		os.WriteFile(outPath, []byte(converted), 0644)

		// Copy supporting files (scripts, references)
		srcDir := filepath.Dir(path)
		if srcDir != dir {
			copySupportingFiles(srcDir, skillDir)
		}

		// Security scan after install
		if report, err := t.scanSkillDir(skillDir); err == nil {
			if report.Blocking {
				results = append(results, fmt.Sprintf("\xe2\x9a\xa0\xef\xb8\x8f %s \xe5\xae\x89\xe8\xa3\x85\xe6\x88\x90\xe5\x8a\x9f\xe4\xbd\x86\xe5\xae\x89\xe5\x85\xa8\xe6\x89\xab\xe6\x8f\x8f\xe5\x8f\x91\xe7\x8e\xb0\xe4\xb8\xa5\xe9\x87\x8d\xe9\x97\xae\xe9\xa2\x98\xef\xbc\x8c\xe5\xb7\xb2\xe7\xa6\x81\xe7\x94\xa8", skillName))
				os.WriteFile(filepath.Join(skillDir, ".disabled"), []byte(report.Summary), 0644)
				fmt.Printf("[SkillSecurity] %s: CRITICAL\n%s", skillName, report.VerboseSummary)
				return nil
			} else if report.FindingCount > 0 {
				results = append(results, fmt.Sprintf("\xe2\x9c\x85 %s \xe5\xae\x89\xe8\xa3\x85\xe6\x88\x90\xe5\x8a\x9f (\xe5\xae\x89\xe5\x85\xa8\xe6\x89\xab\xe6\x8f\x8f: %d\xe4\xb8\xaa\xe4\xbd\x8e\xe9\xa3\x8e\xe9\x99\xa9\xe5\x8f\x91\xe7\x8e\xb0)", skillName, report.FindingCount))
				fmt.Printf("[SkillSecurity] %s: %s", skillName, report.Summary)
			} else {
				results = append(results, fmt.Sprintf("\xe2\x9c\x85 %s \xe2\x86\x92 skills/%s/SKILL.md", skillName, skillName))
			}
		} else {
			results = append(results, fmt.Sprintf("\xe2\x9c\x85 %s \xe2\x86\x92 skills/%s/SKILL.md", skillName, skillName))
		}
		return nil
	})

	if len(results) == 0 {
		return "No SKILL.md found in the downloaded repo.", nil
	}
	return strings.Join(results, "\n"), nil
}

// listInstalledSkills lists all skills in the workspace.
func (t *ClawHubTool) listInstalledSkills() (string, error) {
	skillsDir := t.skillsDir
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return "", fmt.Errorf("read skills dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("## Installed Skills\n\n")
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}
		content, _ := os.ReadFile(skillFile)
		name, desc := extractSkillInfo(string(content))
		sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", entry.Name(), name, desc))
		count++
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d skills", count))
	return sb.String(), nil
}

// extractSkillInfo extracts name and description from SKILL.md frontmatter.
func extractSkillInfo(content string) (name, desc string) {
	start := strings.Index(content, "---")
	if start < 0 {
		return "", ""
	}
	end := strings.Index(content[start+3:], "---")
	if end < 0 {
		return "", ""
	}
	yamlContent := content[start+3 : start+3+end]
	var raw map[string]interface{}
	yaml.Unmarshal([]byte(yamlContent), &raw)
	if n, ok := raw["name"].(string); ok {
		name = n
	}
	if d, ok := raw["description"].(string); ok {
		desc = truncateStr(d, 100)
	}
	return
}

// OpenClawSkillFrontmatter represents the YAML frontmatter of an openclaw SKILL.md.
type OpenClawSkillFrontmatter struct {
	Name         string                 `yaml:"name"`
	Description  string                 `yaml:"description"`
	Version      string                 `yaml:"version"`
	Homepage     string                 `yaml:"homepage"`
	License      string                 `yaml:"license"`
	ReadWhen     []string               `yaml:"read_when"`
	AllowedTools []string               `yaml:"allowed-tools"`
	Metadata     map[string]interface{} `yaml:"metadata"`
}

// convertOpenClawToOpenKitty converts an openclaw-format SKILL.md to openkitty format.
func convertOpenClawToOpenKitty(content string) (string, string, error) {
	// Extract YAML frontmatter
	start := strings.Index(content, "---")
	if start < 0 {
		return "", "", fmt.Errorf("no YAML frontmatter found")
	}
	end := strings.Index(content[start+3:], "---")
	if end < 0 {
		return "", "", fmt.Errorf("unclosed YAML frontmatter")
	}

	yamlStr := content[start+3 : start+3+end]
	body := strings.TrimSpace(content[start+3+end+3:])

	var oc OpenClawSkillFrontmatter
	if err := yaml.Unmarshal([]byte(yamlStr), &oc); err != nil {
		return "", "", fmt.Errorf("parse YAML: %w", err)
	}

	// Extract metadata.openclaw / metadata.clawdbot / metadata.nanobot
	meta := extractOpenClawMeta(oc.Metadata)

	// Determine skill directory name (slug)
	skillName := oc.Name
	if skillName == "" {
		skillName = slugify(oc.Description)
	}
	if len(skillName) > 64 {
		skillName = skillName[:64]
	}

	// Build openkitty frontmatter
	var kitty strings.Builder
	kitty.WriteString("---\n")
	kitty.WriteString(fmt.Sprintf("name: %s\n", oc.Name))
	kitty.WriteString(fmt.Sprintf("description: %s\n", escapeYAML(oc.Description)))

	// read_when from original or generated
	if len(oc.ReadWhen) > 0 {
		kitty.WriteString("read_when:\n")
		for _, rw := range oc.ReadWhen {
			kitty.WriteString(fmt.Sprintf("  - %s\n", escapeYAML(rw)))
		}
	} else {
		triggers := generateTriggers(oc.Description, oc.Name)
		if len(triggers) > 0 {
			kitty.WriteString("read_when:\n")
			for _, t := range triggers {
				kitty.WriteString(fmt.Sprintf("  - %s\n", escapeYAML(t)))
			}
		}
	}

	// Tags
	tags := generateTags(oc.Description, oc.Name, oc.Homepage)
	if len(tags) > 0 {
		kitty.WriteString("tags:\n")
		for _, tag := range tags {
			kitty.WriteString(fmt.Sprintf("  - %s\n", escapeYAML(tag)))
		}
	}

	// Version
	if oc.Version != "" {
		kitty.WriteString(fmt.Sprintf("version: %s\n", oc.Version))
	}

	// Emoji
	if meta.Emoji != "" {
		kitty.WriteString(fmt.Sprintf("metadata:\n  clawdbot:\n    emoji: \"%s\"\n", meta.Emoji))
	}

	// Homepage
	if oc.Homepage != "" {
		kitty.WriteString(fmt.Sprintf("homepage: %s\n", oc.Homepage))
	}

	// Allowed tools from openclaw requires.bins
	if len(meta.RequiredBins) > 0 {
		kitty.WriteString("allowed-tools:\n")
		for _, bin := range meta.RequiredBins {
			kitty.WriteString(fmt.Sprintf("  - Bash(%s:*)\n", bin))
		}
	}

	// Source info
	kitty.WriteString(fmt.Sprintf("# Converted from openclaw format\n"))
	kitty.WriteString(fmt.Sprintf("# Original: %s\n", oc.Name))
	if meta.Source != "" {
		kitty.WriteString(fmt.Sprintf("# Source: %s\n", meta.Source))
	}
	kitty.WriteString("---\n\n")

	// Adapt body content
	body = adaptBody(body)
	kitty.WriteString(body)

	// Add openkitty-specific notes
	kitty.WriteString("\n\n---\n")
	kitty.WriteString(fmt.Sprintf("*Adapted from openclaw skill '%s' for openkitty.*\n", oc.Name))
	if len(meta.RequiredEnv) > 0 {
		kitty.WriteString("\n## Required Environment\n")
		kitty.WriteString("Set these environment variables before using:\n")
		for _, env := range meta.RequiredEnv {
			kitty.WriteString(fmt.Sprintf("- `%s`\n", env))
		}
	}
	if len(meta.RequiredBins) > 0 {
		kitty.WriteString("\n## Required Tools\n")
		for _, bin := range meta.RequiredBins {
			kitty.WriteString(fmt.Sprintf("- `%s`\n", bin))
		}
	}

	return kitty.String(), skillName, nil
}

type openClawMeta struct {
	RequiredEnv  []string
	RequiredBins []string
	Emoji        string
	Source       string
	Always       bool
	OS           []string
}

func extractOpenClawMeta(metadata map[string]interface{}) openClawMeta {
	var m openClawMeta

	// Try all known metadata keys
	for _, key := range []string{"openclaw", "clawdbot", "clawdis", "nanobot"} {
		if cfg, ok := metadata[key].(map[string]interface{}); ok {
			if req, ok := cfg["requires"].(map[string]interface{}); ok {
				if env, ok := req["env"].([]interface{}); ok {
					for _, e := range env {
						if s, ok := e.(string); ok {
							m.RequiredEnv = append(m.RequiredEnv, s)
						}
					}
				}
				if bins, ok := req["bins"].([]interface{}); ok {
					for _, b := range bins {
						if s, ok := b.(string); ok {
							m.RequiredBins = append(m.RequiredBins, s)
						}
					}
				}
				if anyBins, ok := req["anyBins"].([]interface{}); ok {
					for _, b := range anyBins {
						if s, ok := b.(string); ok {
							m.RequiredBins = append(m.RequiredBins, s)
						}
					}
				}
			}
			if emoji, ok := cfg["emoji"].(string); ok {
				m.Emoji = emoji
			}
			if always, ok := cfg["always"].(bool); ok {
				m.Always = always
			}
			if osList, ok := cfg["os"].([]interface{}); ok {
				for _, o := range osList {
					if s, ok := o.(string); ok {
						m.OS = append(m.OS, s)
					}
				}
			}
		}
	}

	return m
}

// adaptBody adapts openclaw-specific patterns in the markdown body.
func adaptBody(body string) string {
	replacements := []struct{ old, new string }{
		// Path adaptations
		{"~/.claw/", "skills/"},
		{"~/.openclaw/", "skills/"},
		{"~/.nanobot/workspace/skills/", "skills/"},
		{"~/.nanobot/workspace/", ""},
		{"~/.nanobot/", ""},
		{"$CLAW_HOME/", ""},
		{"$HOME/.claw/", "skills/"},
		// Tool name adaptations
		{"clawhub install", "clawhub install --source github"},
		{"npx --yes clawhub", "clawhub"},
		// Common script path fixes
		{"scripts/", "skills/<skill>/scripts/"},
	}

	for _, r := range replacements {
		body = strings.ReplaceAll(body, r.old, r.new)
	}

	// Fix relative script references: `./script.py` → use absolute path hint
	scriptRef := regexp.MustCompile("`\\.?/?scripts?/([^`]+)`")
	body = scriptRef.ReplaceAllString(body, "`skills/<skill>/scripts/$1`")

	// Fix bare script references: `python3 script.py` → `python3 skills/<skill>/scripts/script.py`
	bareScript := regexp.MustCompile("(?i)(python3?|node|bash)\\s+((?:\\.\\/)?([a-zA-Z0-9_-]+\\.(?:py|js|sh)))")
	body = bareScript.ReplaceAllString(body, "$1 skills/<skill>/scripts/$2")

	return body
}

// generateTriggers creates read_when triggers from description and name.
func generateTriggers(desc, name string) []string {
	keywords := extractKeywords(desc + " " + name)
	var triggers []string
	for _, kw := range keywords {
		triggers = append(triggers, "When user asks about "+kw)
	}
	if len(triggers) > 5 {
		triggers = triggers[:5]
	}
	return triggers
}

// generateTags creates tags from description, name, and homepage.
func generateTags(desc, name, homepage string) []string {
	seen := make(map[string]bool)
	var tags []string

	// Extract meaningful words
	all := strings.ToLower(desc + " " + name)
	words := strings.FieldsFunc(all, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == ';' || r == ':' || r == '/' || r == '\\' || r == '-'
	})

	tagWords := []string{
		"api", "cli", "web", "search", "fetch", "scrape", "data", "file",
		"image", "video", "audio", "code", "git", "docker", "kubernetes",
		"monitor", "alert", "notify", "email", "chat", "bot", "agent",
		"ai", "llm", "ppt", "doc", "pdf", "markdown", "html", "css",
		"python", "node", "go", "rust", "bash", "shell", "http", "rest",
		"graphql", "json", "csv", "sql", "redis", "db", "cloud", "aws",
	}

	for _, w := range words {
		w = strings.TrimSpace(w)
		if len(w) < 2 || seen[w] {
			continue
		}
		for _, tw := range tagWords {
			if w == tw {
				seen[w] = true
				tags = append(tags, w)
				break
			}
		}
	}

	return tags
}

// extractKeywords extracts important keywords from text.

// slugify converts a string to a valid skill slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// copySupportingFiles copies non-SKILL.md files from srcDir to dstDir.
func copySupportingFiles(srcDir, dstDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if name == "skill.md" || name == "readme.md" {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		srcData, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		os.WriteFile(dst, srcData, 0644)
	}
}

// escapeYAML escapes a string for YAML single-line value.
func escapeYAML(s string) string {
	if strings.ContainsAny(s, "\n\"':{}[]&*#?|<>!%@`") || len(s) > 80 {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func truncateStr(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func extractKeywords(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127)
	})
	stopWords := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "for": true,
		"is": true, "of": true, "or": true, "the": true, "to": true,
	}
	seen := make(map[string]bool)
	keywords := make([]string, 0, 5)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || stopWords[field] || seen[field] {
			continue
		}
		seen[field] = true
		keywords = append(keywords, field)
		if len(keywords) == 5 {
			break
		}
	}
	return keywords
}

// truncateStr truncates a string to maxLen.
