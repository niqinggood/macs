package textutil

import (
	"strings"
	"unicode/utf8"
)

// Cleaner normalizes model and command output before it is displayed or
// persisted as plain text.
type Cleaner struct{}

var defaultCleaner Cleaner

// Clean removes invalid and non-printing runes while retaining common
// whitespace characters.
func Clean(value string) string {
	return defaultCleaner.Clean(value)
}

// CleanBytes is the byte-slice form of Clean.
func CleanBytes(data []byte) []byte {
	return defaultCleaner.CleanBytes(data)
}

func (Cleaner) Clean(value string) string {
	if value == "" {
		return value
	}

	var cleaned strings.Builder
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if r == utf8.RuneError {
			cleaned.WriteByte('?')
			continue
		}
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		cleaned.WriteRune(r)
	}
	return cleaned.String()
}

func (cleaner Cleaner) CleanBytes(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	return []byte(cleaner.Clean(string(data)))
}
