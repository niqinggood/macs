package memory

import (
	"context"
	"unicode"
)

// Preserve original positions while comparing the existing whitespace-free
// hashes, so a forgotten Latin word matches "Go compiler" but not "Google".
func retractionFilter(ctx context.Context, records []memoryRetraction) func(string) bool {
	byLength := map[int]map[string]bool{}
	for _, record := range records {
		if byLength[record.Length] == nil {
			byLength[record.Length] = map[string]bool{}
		}
		byLength[record.Length][record.Digest] = true
	}
	return func(text string) bool {
		original := []rune(text)
		normalized := make([]rune, 0, len(original))
		positions := make([]int, 0, len(original))
		for index, r := range original {
			if !unicode.IsSpace(r) {
				normalized = append(normalized, unicode.ToLower(r))
				positions = append(positions, index)
			}
		}
		for length, digests := range byLength {
			if ctx.Err() != nil {
				return true
			}
			for start := 0; length > 0 && start+length <= len(normalized); start++ {
				end := start + length
				if !digests[retractionDigest(string(normalized[start:end]))] {
					continue
				}
				left, right := positions[start], positions[end-1]
				if memoryWordRune(normalized[start]) && left > 0 && memoryWordRune(original[left-1]) {
					continue
				}
				if memoryWordRune(normalized[end-1]) && right+1 < len(original) && memoryWordRune(original[right+1]) {
					continue
				}
				return true
			}
		}
		return false
	}
}

func memoryWordRune(r rune) bool {
	return r == '_' || unicode.IsDigit(r) || (unicode.IsLetter(r) && !unicode.Is(unicode.Han, r))
}
