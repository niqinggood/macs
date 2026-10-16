package ragmodel

import "unicode"

var stopTokens = map[string]bool{
	"的": true, "了": true, "是": true, "在": true, "和": true, "与": true, "或": true,
	"我": true, "你": true, "他": true, "她": true, "它": true, "们": true,
	"这": true, "那": true, "有": true, "不": true, "也": true, "就": true,
	"都": true, "要": true, "会": true, "可以": true, "能": true, "可能": true,
	"请": true, "帮": true, "让": true, "把": true, "被": true, "从": true,
	"对": true, "向": true, "给": true, "为": true, "为了": true,
	"什么": true, "怎么": true, "如何": true, "怎样": true, "为什么": true,
	"一个": true, "一些": true, "这个": true, "那个": true, "哪个": true,
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "can": true, "shall": true,
	"to": true, "of": true, "in": true, "for": true, "on": true, "with": true,
	"at": true, "by": true, "from": true, "as": true, "into": true, "through": true,
	"about": true, "between": true, "and": true, "or": true, "not": true, "but": true,
	"if": true, "then": true, "else": true, "when": true, "where": true, "how": true,
}

func isPurePunct(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// IsStopToken reports whether a normalized search token carries no useful
// retrieval signal.
func IsStopToken(value string) bool {
	return stopTokens[value]
}

// IsPurePunctuation reports whether a token contains no letters or digits.
func IsPurePunctuation(value string) bool {
	return isPurePunct(value)
}

func hasCJK(value string) bool {
	for _, r := range value {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func isCJK(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf)
}
