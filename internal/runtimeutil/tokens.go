package runtimeutil

// EstimateTokens provides a lightweight token-count approximation for context
// window management. It intentionally has no provider or root-package dependency.
func EstimateTokens(text string) int {
	chinese := 0
	words := 0
	code := 0
	inWord := false

	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff || r >= 0x3040 && r <= 0x30ff || r > 127 {
			chinese++
			if inWord {
				words++
				inWord = false
			}
		} else if isTokenDelimiter(r) {
			code++
			if inWord {
				words++
				inWord = false
			}
		} else if r >= '0' && r <= '9' {
			if inWord {
				words++
				inWord = false
			}
		} else if r == ' ' {
			if inWord {
				words++
				inWord = false
			}
		} else {
			inWord = true
		}
	}
	if inWord {
		words++
	}

	tokens := int(float64(chinese)*0.9 + float64(words)*1.3 + float64(code)*0.5)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

func isTokenDelimiter(r rune) bool {
	switch r {
	case '\t', '\n', '\r', ',', '.', '!', '?', ':', ';',
		'(', ')', '[', ']', '{', '}', '"', '\'', '`',
		'+', '-', '*', '/', '=', '<', '>', '&', '|', '^', '~',
		'@', '#', '$', '%', '\\':
		return true
	}
	return false
}
