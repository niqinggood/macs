package textutil

import "strings"

// IsNumberedHeading recognizes decimal section prefixes such as "1.",
// "1.2 Title", and "1 Title".
func IsNumberedHeading(line string) bool {
	if len(line) < 2 || line[0] < '0' || line[0] > '9' {
		return false
	}
	for index := 1; index < len(line) && index < 10; index++ {
		switch current := line[index]; {
		case current == '.':
			if index+1 >= len(line) {
				return true
			}
			next := line[index+1]
			if next == ' ' || next == '\t' || (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') {
				return true
			}
			if next >= '0' && next <= '9' {
				continue
			}
			return false
		case current == ' ':
			return true
		case current < '0' || current > '9':
			return false
		}
	}
	return false
}

// IsChineseNumberedHeading recognizes common Chinese section prefixes such
// as "一、" and "第X章".
func IsChineseNumberedHeading(line string) bool {
	runes := []rune(line)
	if len(runes) < 2 {
		return false
	}
	if runes[1] == '、' && strings.ContainsRune("一二三四五六七八九十", runes[0]) {
		return true
	}
	return strings.HasPrefix(line, "第")
}
