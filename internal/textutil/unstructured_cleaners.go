package textutil

import (
	"regexp"
	"strings"
)

// ====================== Unstructured-style Text Cleaners ======================
// Port of Unstructured's cleaners/core.py (490 lines).
// Provides document text cleaning: whitespace, bullets, dashes, ligatures, quotes, etc.

// Ligature replacements (port of clean_ligatures)
var ligatureMapFull = map[string]string{
	"æ": "ae", "Æ": "AE",
	"ﬀ": "ff", "ﬁ": "fi", "ﬂ": "fl",
	"ﬃ": "ffi", "ﬄ": "ffl",
	"ﬅ": "ft", "ﬆ": "st",
	"ʪ": "ls", "ʦ": "ts",
	"œ": "oe", "Œ": "OE", "ȹ": "qp",
}

// Unicode quote replacements (port of replace_unicode_quotes)
// WARNING: Do NOT include single-byte CP-1252 replacements (0x91-0x94)
// because they overlap with valid CJK UTF-8 byte sequences!
// e.g. 电话 UTF-8 = 0xE7 0x94 0xB5 → 0x94 matches \x94 → "" corrupts text
var unicodeQuoteReplacements = strings.NewReplacer(
	"&apos;", "'",
	"â€™", "'",
	"â€œ", "“",
	"â€", "”",
	"â€˜", "'",
	"â€”", "—",
	"â€“", "–",
	"â€¦", "…",
	"â€¨", "—",
)

// Unicode bullet characters for cleaning
var reUnicodeBulletsCleaner = regexp.MustCompile(
	`^[\x95•‣⁃ㅤ⁌⁍∙○●◘◦☙❥❧⦾⦿–-\*\x95·]`,
)

var reDash = regexp.MustCompile(`[-–]`)
var reExtraWhitespace = regexp.MustCompile(`[ \n]|([ ]{2,})`)
var reMultipleSpaces = regexp.MustCompile(`[ ]{2,}`)
var reParagraphBreak = regexp.MustCompile(`\s*\n\s*`)
var reDoubleParagraph = regexp.MustCompile(`(\s*\n\s*){2,}`)

// cleanExtraWhitespace collapses extra whitespace and replaces non-breaking spaces.
// Port of Unstructured's clean_extra_whitespace().
func CleanExtraWhitespace(text string) string {
	cleaned := strings.ReplaceAll(text, " ", " ")
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	cleaned = reMultipleSpaces.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

// cleanDashes replaces dash characters with spaces.
// Port of Unstructured's clean_dashes().
func CleanDashes(text string) string {
	return strings.TrimSpace(reDash.ReplaceAllString(text, " "))
}

// cleanBullets removes leading unicode bullets from text.
// Port of Unstructured's clean_bullets().
func CleanBullets(text string) string {
	loc := reUnicodeBulletsCleaner.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimSpace(text[loc[1]:])
}

// cleanTrailingPunct removes trailing punctuation.
// Port of Unstructured's clean_trailing_punctuation().
func CleanTrailingPunct(text string) string {
	return strings.TrimRight(strings.TrimSpace(text), ".,:;")
}

// cleanNonASCII removes non-ASCII characters.
// Port of Unstructured's clean_non_ascii_chars().
func CleanNonASCII(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r <= 127 {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// cleanLigatures replaces typographic ligatures with equivalent characters.
// Port of Unstructured's clean_ligatures().
func CleanLigatures(text string) string {
	for k, v := range ligatureMapFull {
		text = strings.ReplaceAll(text, k, v)
	}
	return text
}

// replaceUnicodeQuotes fixes common Unicode encoding artifacts in text.
// Port of Unstructured's replace_unicode_quotes().
func ReplaceUnicodeQuotes(text string) string {
	return unicodeQuoteReplacements.Replace(text)
}

// cleanOrderedBullets removes numbered/alphanumeric bullet prefixes like "1.1" or "a.b".
// Port of Unstructured's clean_ordered_bullets().
func CleanOrderedBullets(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return text
	}
	first := parts[0]
	if !strings.Contains(first, ".") || strings.Contains(first, "..") {
		return text
	}
	bullet := strings.Split(first, ".")
	if bullet[len(bullet)-1] == "" {
		bullet = bullet[:len(bullet)-1]
	}
	if len(bullet[0]) > 2 {
		return text
	}
	return strings.Join(parts[1:], " ")
}

// Bulk cleaners for English text (port of Unstructured's patterns for paragraph grouping)

// groupBrokenParagraphs merges broken paragraphs separated by single line breaks.
// Port of Unstructured's group_broken_paragraphs().
func GroupBrokenParagraphs(text string) string {
	paragraphs := reDoubleParagraph.Split(text, -1)
	var clean []string
	for _, para := range paragraphs {
		stripped := strings.TrimSpace(para)
		if stripped == "" {
			continue
		}
		// Check if it's a bullet list paragraph
		if reUnicodeBulletsCleaner.MatchString(stripped) {
			clean = append(clean, GroupBulletPara(stripped)...)
			continue
		}
		// Check if all lines are short (likely not a real paragraph)
		lines := reParagraphBreak.Split(para, -1)
		allShort := true
		for _, line := range lines {
			if len(strings.Fields(strings.TrimSpace(line))) >= 5 {
				allShort = false
				break
			}
		}
		if allShort {
			for _, line := range lines {
				if s := strings.TrimSpace(line); s != "" {
					clean = append(clean, s)
				}
			}
		} else {
			clean = append(clean, strings.TrimSpace(reParagraphBreak.ReplaceAllString(para, " ")))
		}
	}
	return strings.Join(clean, "\n\n")
}

func GroupBulletPara(paragraph string) []string {
	var clean []string
	parts := reUnicodeBulletsCleaner.Split(paragraph, -1)
	for _, bullet := range parts {
		if b := strings.TrimSpace(bullet); b != "" {
			clean = append(clean, strings.TrimSpace(reParagraphBreak.ReplaceAllString(bullet, " ")))
		}
	}
	return clean
}

// cleanPrefix removes a regex pattern prefix from text.
// Port of Unstructured's clean_prefix().
func CleanPrefix(text, pattern string, ignoreCase, strip bool) string {
	flags := ""
	if ignoreCase {
		flags = "(?i)"
	}
	re := regexp.MustCompile(flags + "^" + pattern)
	cleaned := re.ReplaceAllString(text, "")
	if strip {
		cleaned = strings.TrimLeft(cleaned, " \t")
	}
	return cleaned
}

// cleanPostfix removes a regex pattern suffix from text.
// Port of Unstructured's clean_postfix().
func CleanPostfix(text, pattern string, ignoreCase, strip bool) string {
	flags := ""
	if ignoreCase {
		flags = "(?i)"
	}
	re := regexp.MustCompile(flags + pattern + "$")
	cleaned := re.ReplaceAllString(text, "")
	if strip {
		cleaned = strings.TrimRight(cleaned, " \t")
	}
	return cleaned
}

// Clean is the central text cleaning function, mirrors Unstructured's clean().
func CleanText(text string, opts CleanOptions) string {
	s := text
	if opts.Lowercase {
		s = strings.ToLower(s)
	}
	if opts.TrailingPunctuation {
		s = CleanTrailingPunct(s)
	}
	if opts.Dashes {
		s = CleanDashes(s)
	}
	if opts.ExtraWhitespace {
		s = CleanExtraWhitespace(s)
	}
	if opts.Bullets {
		s = CleanBullets(s)
	}
	return strings.TrimSpace(s)
}

type CleanOptions struct {
	ExtraWhitespace     bool
	Dashes              bool
	Bullets             bool
	TrailingPunctuation bool
	Lowercase           bool
}

func DefaultCleanOptions() CleanOptions {
	return CleanOptions{
		ExtraWhitespace:     true,
		Dashes:              false,
		Bullets:             true,
		TrailingPunctuation: false,
	}
}

// CleanDocument applies the full cleaning pipeline to document text.
func CleanDocument(text string) string {
	text = ReplaceUnicodeQuotes(text)
	text = CleanLigatures(text)
	// Skip groupBrokenParagraphs — Go regex \s*\n\s* can eat leading CJK bytes
	// when combined with page prefix patterns. CJK text is already cleaned by
	// cleanPlainText in the extraction step.
	return text
}

// CleanForChunking applies aggressive whitespace cleaning (good for chunking, bad for classification).
func CleanForChunking(text string) string {
	text = ReplaceUnicodeQuotes(text)
	text = CleanLigatures(text)
	text = CleanExtraWhitespace(text)
	text = GroupBrokenParagraphs(text)
	return text
}
