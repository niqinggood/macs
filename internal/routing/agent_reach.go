// Package routing contains intent classification that selects optional runtime capabilities.
package routing

import (
	"regexp"
	"strings"
)

var (
	agentReachURLIntentPattern     = regexp.MustCompile(`(?i)https?://|\b(?:www\.)?[a-z0-9][a-z0-9.-]+\.(?:com|org|net|io|ai|cn|dev|app|fm)(?:/|\b)`)
	agentReachNamedPlatformPattern = regexp.MustCompile(`(?i)(?:github|youtube|youtu\.be|bilibili|b站|哔哩哔哩|twitter|推特|x\.com|reddit|xiaohongshu|xhs|小红书|v2ex|linkedin|领英|xiaoyuzhou|小宇宙|xueqiu|雪球|rss|atom\s*feed)`)
)

// AgentReachAutomaticIntent keeps metadata matching from turning generic
// words such as "search", "code", or "analysis" into an internet route. It
// does not gate direct tool calls; it only controls automatic Skill injection.
func AgentReachAutomaticIntent(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}
	lower := strings.ToLower(query)
	if strings.Contains(lower, "agent-reach") || strings.Contains(lower, "agent reach") ||
		agentReachURLIntentPattern.MatchString(query) || agentReachNamedPlatformPattern.MatchString(query) {
		return true
	}
	for _, phrase := range []string{
		"全网调研", "互联网调研", "网上调研", "网络调研", "上网查", "网上查",
		"上网搜索", "网上搜索", "网络搜索", "搜索互联网", "全网搜索", "看看网上",
		"网上怎么说", "大家怎么评价", "平台讨论", "视频字幕", "播客转录", "网页链接",
		"search the web", "search online", "web search", "web research", "research online",
		"look up online", "find online", "internet research", "read this url", "open this url",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
