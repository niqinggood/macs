package network

import (
	"context"
	"fmt"
	"strings"
)

// ====================== Platform Search Tool ======================

// PlatformSearchTool searches social platforms for customer intent signals.
// Unlike generic web_search which returns news/funding results,
// this searches platforms where BUYERS express needs.
type PlatformSearchTool struct{}

func NewPlatformSearchTool() *PlatformSearchTool {
	return &PlatformSearchTool{}
}

func (t *PlatformSearchTool) Name() string { return "platform_search" }
func (t *PlatformSearchTool) Description() string {
	return "在社交/电商/问答平台上搜索潜在客户的需求信号。搜索知乎提问、小红书帖子、脉脉讨论等，发现正在寻找解决方案的潜在客户。结果比通用搜索更有针对性——找的是需求方,不是供给方。"
}
func (t *PlatformSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"keyword":  map[string]interface{}{"type": "string", "description": "搜索关键词,例如: 客服系统推荐、AI客服哪家好"},
			"platform": map[string]interface{}{"type": "string", "description": "目标平台: zhihu/xiaohongshu/maimai/taobao/xianyu/all"},
			"intent":   map[string]interface{}{"type": "string", "description": "需求类型: buying(正在选型)/complaining(抱怨现有方案)/asking(咨询)/all"},
		},
		"required": []string{"keyword"},
	}
}
func (t *PlatformSearchTool) IsReadOnly() bool { return true }
func (t *PlatformSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	keyword, _ := params["keyword"].(string)
	platform, _ := params["platform"].(string)
	intent, _ := params["intent"].(string)

	if platform == "" {
		platform = "all"
	}
	if intent == "" {
		intent = "all"
	}

	var sb strings.Builder
	sb.WriteString("# 平台获客搜索任务\n\n")
	sb.WriteString(fmt.Sprintf("**搜索关键词**: %s\n", keyword))
	sb.WriteString(fmt.Sprintf("**目标平台**: %s\n", platform))
	sb.WriteString(fmt.Sprintf("**需求类型**: %s\n\n", intent))

	// Generate platform-specific search queries with intent signal keywords
	sb.WriteString("## 搜索指令\n\n")
	sb.WriteString("使用 web_search 工具执行以下搜索（每次搜一条,最多搜5条）:\n\n")

	queries := t.buildQueries(keyword, platform, intent)
	for i, q := range queries {
		sb.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, q))
	}

	sb.WriteString("\n## 意图信号识别\n\n")
	sb.WriteString("搜索结果中,标记以下信号为高价值线索:\n")
	sb.WriteString("- 明确问[推荐/哪家好/求推荐] → buying信号,评分80+\n")
	sb.WriteString("- 抱怨现有方案[不好用/太贵了/想换] → switching信号,评分70+\n")
	sb.WriteString("- 行业讨论[客服系统怎么选/对比] → researching信号,评分60+\n")
	sb.WriteString("- 纯技术讨论/新闻/招聘 → 非线索,跳过\n\n")

	sb.WriteString("## 后续动作\n")
	sb.WriteString("1. 发现需求信号 → 使用 web_fetch 了解详情\n")
	sb.WriteString("2. 确认为真实需求 → 使用 lead_save 建档\n")
	sb.WriteString("3. 记录来源平台和原始链接\n")
	sb.WriteString("4. 输出获客线索清单:平台/用户/需求描述/信号强度/来源链接\n")

	return sb.String(), nil
}

// buildQueries generates platform-specific search queries with intent signal keywords.
func (t *PlatformSearchTool) buildQueries(keyword, platform, intent string) []string {
	var queries []string

	intentModifiers := map[string]string{
		"buying":      "推荐 OR 哪家好 OR 求推荐 OR 对比 OR 选型",
		"complaining": "不好用 OR 太贵了 OR 想换 OR 坑 OR 踩坑",
		"asking":      "怎么选 OR 怎么样 OR 好不好 OR 行不行",
		"all":         "推荐 OR 哪家好 OR 不好用 OR 想换 OR 怎么选",
	}

	modifier := intentModifiers[intent]
	if modifier == "" {
		modifier = intentModifiers["all"]
	}

	platformSites := map[string]string{
		"zhihu":       "site:zhihu.com",
		"xiaohongshu": "site:xiaohongshu.com",
		"maimai":      "site:maimai.cn",
		"taobao":      "site:taobao.com",
		"xianyu":      "site:goofish.com OR site:2.taobao.com",
		"douyin":      "site:douyin.com",
	}

	if platform != "all" {
		if site, ok := platformSites[platform]; ok {
			queries = append(queries, fmt.Sprintf("%s %s %s", keyword, modifier, site))
		}
	} else {
		for _, site := range platformSites {
			queries = append(queries, fmt.Sprintf("%s %s %s", keyword, modifier, site))
		}
	}

	// Add general intent queries (not platform-specific)
	queries = append(queries,
		fmt.Sprintf("\"%s\" %s", keyword, modifier),
		fmt.Sprintf("%s 客户 需求 采购", keyword),
	)

	if len(queries) > 8 {
		queries = queries[:8]
	}

	return queries
}
