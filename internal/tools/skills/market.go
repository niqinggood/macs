package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MarketTool browses and installs skills from a GitHub-backed marketplace.
type MarketTool struct {
	marketRepo string
	skillsDir  string
	httpClient *http.Client
}

type marketEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      string `json:"author"`
}

func NewMarketTool(repo, skillsDir string) *MarketTool {
	return &MarketTool{
		marketRepo: strings.TrimSpace(repo),
		skillsDir:  skillsDir,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewMarketToolWithClient allows hosts to inject their HTTP transport.
func NewMarketToolWithClient(repo, skillsDir string, client *http.Client) *MarketTool {
	tool := NewMarketTool(repo, skillsDir)
	if client != nil {
		tool.httpClient = client
	}
	return tool
}

func (tool *MarketTool) Name() string { return "skill_market" }

func (tool *MarketTool) Description() string {
	return "Browse and install skills from the skill marketplace. " +
		"Operations: search <keyword>, install <name>, list. " +
		"Skills are downloaded from GitHub and installed locally."
}

func (tool *MarketTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Operation: 'search', 'install', or 'list'",
				"enum":        []string{"search", "install", "list"},
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search keyword or skill name to install",
			},
		},
		"required": []string{"action"},
	}
}

func (tool *MarketTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if tool.marketRepo == "" {
		return "技能市场未配置。请在 config.yaml 的 skills_config 中设置 market_repo（如 'user/repo'）。", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	action, _ := params["action"].(string)
	query, _ := params["query"].(string)
	switch action {
	case "search":
		return tool.searchSkills(ctx, query)
	case "install":
		return tool.installSkill(ctx, query)
	case "list":
		return tool.listSkills(ctx)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (tool *MarketTool) fetch(ctx context.Context, path string) (*http.Response, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", tool.marketRepo, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return tool.httpClient.Do(req)
}

func (tool *MarketTool) fetchIndex(ctx context.Context) ([]marketEntry, error) {
	resp, err := tool.fetch(ctx, "index.json")
	if err != nil {
		return nil, fmt.Errorf("无法访问技能市场: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("技能市场返回 HTTP %d (repo: %s)", resp.StatusCode, tool.marketRepo)
	}
	var entries []marketEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("index.json 解析失败: %w", err)
	}
	return entries, nil
}

func (tool *MarketTool) searchSkills(ctx context.Context, keyword string) (string, error) {
	entries, err := tool.fetchIndex(ctx)
	if err != nil {
		return "", err
	}
	var matched []marketEntry
	keyword = strings.ToLower(keyword)
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name), keyword) ||
			strings.Contains(strings.ToLower(entry.Description), keyword) {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("未找到匹配 '%s' 的技能。尝试用 skill_market list 查看全部。", keyword), nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("找到 %d 个匹配技能：\n\n", len(matched)))
	for _, entry := range matched {
		installed := ""
		if validMarketSkillName(entry.Name) {
			if _, err := os.Stat(filepath.Join(tool.skillsDir, entry.Name, "SKILL.md")); err == nil {
				installed = " [已安装]"
			}
		}
		result.WriteString(fmt.Sprintf("- **%s** v%s by %s%s\n  %s\n\n", entry.Name, entry.Version, entry.Author, installed, entry.Description))
	}
	result.WriteString("使用 skill_market install <name> 安装。")
	return result.String(), nil
}

func (tool *MarketTool) installSkill(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("请指定要安装的技能名称")
	}
	if !validMarketSkillName(name) {
		return "", fmt.Errorf("技能名称必须是单层目录名")
	}
	entries, err := tool.fetchIndex(ctx)
	if err != nil {
		return "", err
	}
	var target *marketEntry
	for index := range entries {
		if strings.EqualFold(entries[index].Name, name) {
			target = &entries[index]
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("技能 '%s' 在市场中未找到。用 skill_market search 查看可用技能。", name), nil
	}
	if !validMarketSkillName(target.Name) {
		return "", fmt.Errorf("技能市场返回了不安全的技能名称 %q", target.Name)
	}

	resp, err := tool.fetch(ctx, target.Name+"/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 '%s' 失败: HTTP %d", target.Name, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	skillDir := filepath.Join(tool.skillsDir, target.Name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	return fmt.Sprintf("已安装 **%s** v%s\n作者: %s\n描述: %s\n\n技能已安装到 %s，下次对话可用 list_skills 发现。",
		target.Name, target.Version, target.Author, target.Description, skillDir), nil
}

func (tool *MarketTool) listSkills(ctx context.Context) (string, error) {
	entries, err := tool.fetchIndex(ctx)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("技能市场 (%s) 共 %d 个技能：\n\n", tool.marketRepo, len(entries)))
	for _, entry := range entries {
		installed := ""
		if validMarketSkillName(entry.Name) {
			if _, err := os.Stat(filepath.Join(tool.skillsDir, entry.Name, "SKILL.md")); err == nil {
				installed = " ✓"
			}
		}
		result.WriteString(fmt.Sprintf("  %s%s - %s (v%s)\n", entry.Name, installed, entry.Description, entry.Version))
	}
	return result.String(), nil
}

func validMarketSkillName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}
