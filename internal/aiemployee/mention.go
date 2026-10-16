package aiemployee

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func MentionAliases(config *Config) []string {
	if config == nil || config.InternalOnly {
		return nil
	}
	seen := map[string]bool{}
	aliases := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		aliases = append(aliases, value)
	}
	add(config.AtName)
	add(config.Name)
	add(config.ID)
	for _, alias := range config.MentionAliases {
		add(alias)
	}
	if config.ID == "meeting-secretary" {
		add("会议秘书")
	}
	if config.ID == "codex-specialist" {
		add("codex")
		add("codex专员")
	}
	if config.ID == "cc-specialist" {
		add("cc")
		add("cc专员")
		add("claude")
		add("claude code")
		add("claude code专员")
	}
	return aliases
}

func FindExplicitMention(message, alias string) (int, string, bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return -1, "", false
	}
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta("@"+alias))
	if err != nil {
		return -1, "", false
	}
	for _, location := range pattern.FindAllStringIndex(message, -1) {
		if location[1] < len(message) {
			next, _ := utf8.DecodeRuneInString(message[location[1]:])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || unicode.IsMark(next) || next == '_' || next == '-' || next == '.' {
				continue
			}
		}
		return location[0], message[location[0]:location[1]], true
	}
	return -1, "", false
}

func CleanMatchedTask(message, mention string) string {
	task := message
	if index := strings.Index(strings.ToLower(message), strings.ToLower(mention)); index >= 0 {
		task = strings.TrimSpace(message[:index] + message[index+len(mention):])
	} else {
		task = strings.TrimSpace(strings.Replace(message, mention, "", 1))
	}
	task = StripLeadingRouterMention(task)
	if task == "" {
		return strings.TrimSpace(message)
	}
	return task
}

func StripLeadingRouterMention(task string) string {
	task = strings.TrimSpace(task)
	for {
		lower := strings.ToLower(task)
		matched := false
		for _, token := range []string{"@browser", "@浏览器"} {
			if lower == token {
				return ""
			}
			if strings.HasPrefix(lower, token+" ") || strings.HasPrefix(lower, token+"\t") || strings.HasPrefix(lower, token+"\n") {
				task = strings.TrimSpace(task[len(token):])
				matched = true
				break
			}
		}
		if !matched {
			return task
		}
	}
}

func HasBareMentionPrefix(message, alias string) bool {
	message = strings.TrimSpace(message)
	alias = strings.TrimSpace(alias)
	if alias == "" || len(message) < len(alias) || !strings.EqualFold(message[:len(alias)], alias) {
		return false
	}
	if len(message) == len(alias) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(message[len(alias):])
	return unicode.IsSpace(next) || strings.ContainsRune(":：,，.。!！?？、;；-—", next)
}
