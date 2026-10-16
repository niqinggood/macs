package textutil

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ============================================================
// Text Cleaner — unified pipeline for all document formats.
// Normalizes extracted text before chunking and indexing.
// ============================================================
//
// Pipeline: Raw text
//   → 1. Encoding repair (UTF-8 validate, BOM strip, nbsp)
//   → 2. Line ending normalize (\r\n→\n, \t→space)
//   → 3. HTML artifacts (tags strip, entity decode)
//   → 4. PDF artifacts (hyphen fix, page numbers, line merge)
//   → 5. Whitespace collapse (3+ spaces→\n, 3+ \n→\n\n)
//   → Clean text
//
// Covers patterns from LightningRAG (UTF validate, HTML clean),
// with PDF-specific additions (hyphenation, page artifacts, paragraph merge).

func CleanRAGText(raw string) string {
	if raw == "" {
		return ""
	}

	// ---- Stage 1: Encoding repair ------------------------------------
	text := raw
	b := []byte(text)
	if !utf8.Valid(b) {
		text = string(bytes.ToValidUTF8(b, []byte(" ")))
	}
	// Unicode normalization (NFC) — RAGFlow/google_util pattern
	text = norm.NFC.String(text)

	// Strip BOM (U+FEFF) and zero-width characters
	text = strings.Map(func(r rune) rune {
		if r == 0xFEFF || r == 0x200B || r == 0x200C || r == 0x200D ||
			r == 0x200E || r == 0x200F || r == 0x2060 || r == 0xFE00 {
			return -1
		}
		return r
	}, text)

	// ASCII control chars (except \n) → space — RAGFlow/graphrag pattern
	text = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' {
			return ' '
		}
		if r >= 0x7F && r <= 0x9F {
			return ' '
		}
		return r
	}, text)

	// Fullwidth → halfwidth (CJK punctuation normalization)
	text = fullwidthToHalfwidth(text)

	// Arabic-Indic & Eastern Arabic digits → ASCII (RAGFlow pattern)
	text = normalizeArabicDigits(text)

	// ---- Stage 2: Line endings & invisible chars ---------------------
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.ReplaceAll(text, " ", " ") // non-breaking space

	// ---- Stage 3: HTML artifacts -------------------------------------
	// Only run if text shows signs of HTML content
	if strings.Contains(text, "<") || strings.Contains(text, "&") {
		text = StripHTML(text)
	}

	// ---- Stage 4: PDF artifacts --------------------------------------
	// Ligature repair, hyphenation, page numbers, line merge
	text = RepairPDFLigatures(text)
	text = repairPDFText(text)

	// ---- Stage 5: Whitespace -----------------------------------------
	// Collapse excess whitespace to clean paragraphs
	text = normalizeWhitespace(text)
	// Post-normalize: collapse multiple spaces within lines
	reSpaces := regexp.MustCompile(` {2,}`)
	text = reSpaces.ReplaceAllString(text, " ")
	// Remove leading/trailing spaces on each line
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	text = strings.Join(lines, "\n")
	// Collapse multiple newlines to single
	text = strings.ReplaceAll(text, "\n\n", "\n")
	text = strings.ReplaceAll(text, "\n\n", "\n")
	text = strings.Trim(text, "\n")

	return text
}

func normalizeWhitespace(text string) string {
	spaceRegex := regexp.MustCompile(`[ \t]+`)
	text = spaceRegex.ReplaceAllString(text, " ")
	lineRegex := regexp.MustCompile(`\n{3,}`)
	text = lineRegex.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// ============================================================
// Stage 3: HTML cleanup (from LightningRAG)
// ============================================================

var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reEntity = regexp.MustCompile(`&[a-zA-Z]+;|&#\d+;`)
)

func StripHTML(text string) string {
	// Remove script/style blocks entirely
	text = reScript.ReplaceAllString(text, "")
	text = reStyle.ReplaceAllString(text, "")

	// Block elements → newline
	for _, tag := range []string{
		"</p>", "</P>", "</div>", "</DIV>",
		"</h1>", "</H1>", "</h2>", "</H2>", "</h3>", "</H3>",
		"</h4>", "</H4>", "</h5>", "</H5>", "</h6>", "</H6>",
		"<br>", "<BR>", "<br/>", "<BR/>", "<br />", "<BR />",
		"</li>", "</LI>", "</tr>", "</TR>",
	} {
		text = strings.ReplaceAll(text, tag, "\n")
	}

	// Strip remaining tags
	text = reTag.ReplaceAllString(text, "")

	// Decode common entities
	text = strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", "\"", "&#39;", "'", "&apos;", "'",
	).Replace(text)

	// Any leftover entity patterns → space
	text = reEntity.ReplaceAllString(text, " ")

	return text
}

// ============================================================
// Stage 4: PDF artifact repair
// ============================================================

// repairPDFLigatures maps common PDF font-encoding artifacts to readable text.
// PDF libraries (like ledongthuc/pdf) often fail on ligatures and special glyphs,
// producing U+FFFD replacement characters. This function applies context-based repair
// for the most common patterns.
func RepairPDFLigatures(text string) string {
	// 1. Known PDF presentation-form ligatures (U+FB00-U+FB06) to ASCII
	text = strings.NewReplacer(
		"ﬀ", "ff",
		"ﬁ", "fi",
		"ﬂ", "fl",
		"ﬃ", "ffi",
		"ﬄ", "ffl",
		"ﬅ", "ft",
		"ﬆ", "st",
	).Replace(text)

	// 2. Context-based repair for U+FFFD (replacement character)
	// The Go PDF lib produces U+FFFD for undecodable glyphs.
	// We try to guess the missing character from common English word patterns.
	if !strings.ContainsRune(text, '�') {
		return text
	}

	// Build a simple triage: for each U+FFFD, check surrounding context
	// and try common substitutions. This is a best-effort heuristic.
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '�' {
			continue
		}

		// Get surrounding context (up to 4 chars each side)
		left := ""
		if i > 0 {
			start := max(0, i-4)
			left = string(runes[start:i])
		}
		right := ""
		if i+1 < len(runes) {
			end := min(len(runes), i+5)
			right = string(runes[i+1 : end])
		}

		repair := guessMissingChar(left, right)
		if repair != 0 {
			runes[i] = repair
		}
	}

	return string(runes)
}

// guessMissingChar tries to identify a missing character from context.
// Returns the guessed rune, or 0 if no guess is confident enough.
func guessMissingChar(left, right string) rune {
	// Pattern: "organi" + U+FFFD + "ations" -> 'z' (organizations)
	if strings.HasSuffix(left, "organi") && strings.HasPrefix(right, "ations") {
		return 'z'
	}
	// Pattern: before "ob" at end or followed by space -> 'j' (job)
	ri := []rune(right)
	if len(ri) >= 2 && ri[0] == 'o' && ri[1] == 'b' && left == "" {
		return 'j'
	}
	// Pattern: "recogni" + U+FFFD + "e" -> 'z' (recognize)
	if strings.HasSuffix(left, "recogni") && strings.HasPrefix(right, "e") {
		return 'z'
	}
	// Pattern: "utili" + U+FFFD + "e" -> 'z' (utilize)
	if strings.HasSuffix(left, "utili") && strings.HasPrefix(right, "e") {
		return 'z'
	}
	// Pattern: "actuali" + U+FFFD + "e" -> 'z' (actualize)
	if strings.HasSuffix(left, "actuali") && strings.HasPrefix(right, "e") {
		return 'z'
	}
	// Pattern: between letters, likely 'z' (common PDF issue with 'z' glyph)
	li := []rune(left)
	ri2 := []rune(right)
	if len(li) > 0 && len(ri2) > 0 && isLetter(li[len(li)-1]) && isLetter(ri2[0]) {
		return 'z' // 'z' is the most commonly garbled letter in PDF extraction
	}
	return 0
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func repairPDFText(text string) string {
	lines := strings.Split(text, "\n")

	// 4a. Fix hyphenated words: "con-\ncrete" → "concrete"
	lines = fixHyphens(lines)

	// 4b. Remove page artifacts (numbers, headers, decorative lines)
	lines = removePageNoise(lines)

	// 4c. Strip repeated headers/footers (same text appearing every N pages)
	lines = stripRepeatedHeaders(lines)

	// 4d. Merge hard-wrapped lines into paragraphs
	return mergeLines(lines)
}

func fixHyphens(lines []string) []string {
	if len(lines) <= 1 {
		return lines
	}
	result := make([]string, 0, len(lines))
	result = append(result, lines[0])
	for i := 1; i < len(lines); i++ {
		prev := result[len(result)-1]
		if strings.HasSuffix(prev, "-") && len(prev) > 2 {
			// Verify it's a word break, not a dash in text
			runes := []rune(prev)
			before := runes[len(runes)-2]
			if unicode.IsLetter(before) {
				result[len(result)-1] = strings.TrimSuffix(prev, "-") + lines[i]
				continue
			}
		}
		result = append(result, lines[i])
	}
	return result
}

func removePageNoise(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Empty line: keep as paragraph break
		if line == "" {
			out = append(out, line)
			continue
		}

		// Pure digits, 1-5 chars → page number
		if isOnlyDigits(line) && len(line) <= 5 {
			continue
		}

		// "Page X" or "Page-X" → header/footer
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "page ") && len(line) < 25 {
			continue
		}

		// Short lines that are only punctuation → decorative
		if len(line) < 10 && isOnlyPunct(line) {
			continue
		}

		out = append(out, line)
	}
	return out
}

// stripRepeatedHeaders removes lines that repeat frequently (headers/footers).
// If the same line appears ≥3 times across the document, it's likely a header/footer.
func stripRepeatedHeaders(lines []string) []string {
	if len(lines) < 10 {
		return lines
	}

	// Count occurrences of each non-empty line
	counts := map[string]int{}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" && len(t) > 5 && len(t) < 120 {
			counts[t]++
		}
	}

	// Lines appearing ≥3 times are likely headers/footers
	repeated := map[string]bool{}
	for line, cnt := range counts {
		if cnt >= 3 {
			repeated[line] = true
		}
	}

	if len(repeated) == 0 {
		return lines
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if repeated[strings.TrimSpace(line)] {
			continue
		}
		out = append(out, line)
	}
	return out
}

func mergeLines(lines []string) string {
	var paras []string
	var buf strings.Builder
	needSpace := false

	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			paras = append(paras, s)
		}
		buf.Reset()
		needSpace = false
	}

	for _, line := range lines {
		// Blank line = paragraph boundary
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}

		runes := []rune(line)
		last := runes[len(runes)-1]
		endsWithPunct := last == '.' || last == '!' || last == '?' ||
			last == '。' || last == '！' || last == '？'

		startsUpper := unicode.IsUpper(runes[0])
		isList := strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "- ") ||
			strings.HasPrefix(line, "• ") ||
			strings.HasPrefix(line, "· ")

		// Lists always start new paragraph
		if isList {
			flush()
			paras = append(paras, line)
			continue
		}

		// Merge if: doesn't end with punct, or is a short continuation line
		merge := !endsWithPunct || (!startsUpper && len(runes) < 60)

		if merge {
			if buf.Len() > 0 {
				if needSpace {
					buf.WriteString(" ")
				}
				buf.WriteString(line)
			} else {
				buf.WriteString(line)
			}
			needSpace = !endsWithPunct
		} else {
			if buf.Len() > 0 {
				buf.WriteString(" " + line)
			} else {
				buf.WriteString(line)
			}
			flush()
		}
	}
	flush()

	return strings.Join(paras, "\n\n")
}

// ============================================================
// Helpers
// ============================================================

// isTextGarbled detects garbled PDF extraction output (RAGFlow pattern).
// Returns true if >50% of characters are PUA, replacement chars, or control chars.
func IsTextGarbled(text string) bool {
	if len(text) < 10 {
		return false
	}
	garbled := 0
	runes := []rune(text)
	for _, r := range runes {
		// Unicode Private Use Areas
		if (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0x10FFFF) {
			garbled++
		}
		// Replacement character
		if r == 0xFFFD {
			garbled++
		}
		// Unassigned/surrogate categories
		if r >= 0xD800 && r <= 0xDFFF {
			garbled++
		}
	}
	return float64(garbled)/float64(len(runes)) > 0.5
}

// fullwidthToHalfwidth converts fullwidth CJK punctuation to halfwidth.
func fullwidthToHalfwidth(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '！' && r <= '～' {
			return r - 0xFEE0
		}
		return r
	}, s)
}

// normalizeArabicDigits converts Arabic-Indic and Eastern Arabic digits to ASCII.
func normalizeArabicDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x0660 && r <= 0x0669 {
			return '0' + (r - 0x0660)
		}
		if r >= 0x06F0 && r <= 0x06F9 {
			return '0' + (r - 0x06F0)
		}
		return r
	}, s)
}

func isOnlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isOnlyPunct(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			return false
		}
	}
	return s != ""
}
