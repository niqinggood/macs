// Package textutil provides text shaping helpers shared across runtime modules.
package textutil

import (
	"fmt"
	"unicode/utf8"
)

// TruncateRunes limits text by Unicode code points without adding diagnostics.
func TruncateRunes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "..."
}

// Truncate limits English words and Chinese characters while preserving the
// historical OpenKitty truncation semantics and diagnostic suffix.
func Truncate(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	originalLength := countWordsAndCharacters(value)
	if originalLength <= maximum {
		return value
	}

	result := make([]rune, 0, len(runes))
	accumulated := 0

truncateLoop:
	for index, current := range runes {
		switch {
		case isChinese(current):
			if accumulated+1 > maximum {
				break truncateLoop
			}
			result = append(result, current)
			accumulated++
		case isLetterOrDigit(current):
			if index == 0 || !isLetterOrDigit(runes[index-1]) {
				if accumulated+1 > maximum {
					break truncateLoop
				}
				accumulated++
			}
			result = append(result, current)
		default:
			result = append(result, current)
		}
	}

	truncated := string(result)
	finalLength := countWordsAndCharacters(truncated)
	ratio := float64(finalLength) / float64(originalLength) * 100
	return truncated + "..." + fmt.Sprintf("[truncated: %d→%d, %.1f%%]", originalLength, finalLength, ratio)
}

// TruncateUTF8Bytes limits encoded bytes without splitting a Unicode rune.
func TruncateUTF8Bytes(text string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(text) <= maximum {
		return text
	}
	suffix := "...[truncated]"
	if len(suffix) >= maximum {
		return suffix[:maximum]
	}
	cut := maximum - len(suffix)
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	return text[:cut] + suffix
}

// TailUTF8Bytes returns the largest UTF-8-safe suffix within maximum bytes.
func TailUTF8Bytes(text string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(text) <= maximum {
		return text
	}
	start := len(text) - maximum
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

func countWordsAndCharacters(value string) int {
	count := 0
	inWord := false
	for _, current := range value {
		if isChinese(current) {
			count++
			inWord = false
		} else if isLetterOrDigit(current) {
			if !inWord {
				count++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return count
}

func isChinese(current rune) bool {
	return (current >= 0x4E00 && current <= 0x9FFF) ||
		(current >= 0x3400 && current <= 0x4DBF) ||
		(current >= 0x20000 && current <= 0x2A6DF) ||
		(current >= 0xF900 && current <= 0xFAFF)
}

func isLetterOrDigit(current rune) bool {
	return (current >= 'a' && current <= 'z') ||
		(current >= 'A' && current <= 'Z') ||
		(current >= '0' && current <= '9')
}
