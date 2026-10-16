package channels

import (
	"regexp"
	"strings"
)

const DefaultReplyLimit = 7000

func FormatReplyForTransport(content string) string {
	return FormatReplyForTransportLimit(content, DefaultReplyLimit)
}

func FormatReplyForTransportLimit(content string, limit int) string {
	content = strings.TrimSpace(content)
	if content == "" || limit <= 0 {
		return content
	}
	if len([]rune(content)) <= limit {
		return content
	}

	meta := ExtractExternalAgentMeta(content)
	if meta != "" {
		meta += "\n\n"
	}
	excerptLimit := limit - len([]rune(meta)) - 120
	if excerptLimit < 1000 {
		excerptLimit = 1000
	}
	return meta + "输出过长，以下为 Claude/Codex 关键输出节选；完整内容见上方报告路径。\n\n" + HeadTailExcerpt(content, excerptLimit)
}

func ExtractExternalAgentMeta(content string) string {
	if !strings.Contains(content, "[External Agent:") && !strings.Contains(content, ".openkitty/storage/external_agents/") {
		return ""
	}
	var lines []string
	if first := FirstNonEmptyLine(content); first != "" && strings.Contains(first, "[External Agent:") {
		lines = append(lines, first)
	}
	if report := FirstRegexSubmatch(content, `(?m)^\[Report:\s*([^\]]+)\]`); report != "" {
		lines = append(lines, "完整执行报告: "+report)
	}
	changed := ExtractChangedFileLines(content, 12)
	if len(changed) > 0 {
		lines = append(lines, "变更文件:")
		lines = append(lines, changed...)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func FirstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func FirstRegexSubmatch(s, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func ExtractChangedFileLines(content string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	out := make([]string, 0, limit)
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[Changed files]" {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "- ") {
			out = append(out, trimmed)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func HeadTailExcerpt(content string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	head := limit * 2 / 3
	tail := limit - head
	return string(runes[:head]) + "\n\n...中间内容已省略...\n\n" + string(runes[len(runes)-tail:])
}
