package textutil

import (
	"log"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

var (
	gseSeg  gse.Segmenter
	gseOnce sync.Once
)

// ====================== Unstructured-style Element Classifier (Go port) ======================
// Faithful port of Unstructured's text_type.py + elements.py core classification logic.
// Reference: https://github.com/Unstructured-IO/unstructured (Apache 2.0)
// Zero external deps. Pure Go regex + unicode.

type ElementType int

const (
	ElemTitle         ElementType = iota // e.g. "Annual Report 2025"
	ElemSectionHeader                    // e.g. "Financial Highlights"
	ElemSubHeader                        // e.g. "Revenue Breakdown by Region"
	ElemNarrativeText                    // body paragraph
	ElemListItem                         // bullet / numbered item
	ElemTable                            // tabular content
	ElemPageBreak                        // page separator
	ElemUncategorized                    // fallback / noise
)

var elemTypeNames = map[ElementType]string{
	ElemTitle:         "Title",
	ElemSectionHeader: "SectionHeader",
	ElemSubHeader:     "SubHeader",
	ElemNarrativeText: "NarrativeText",
	ElemListItem:      "ListItem",
	ElemTable:         "Table",
	ElemPageBreak:     "PageBreak",
	ElemUncategorized: "Uncategorized",
}

func (e ElementType) String() string { return elemTypeNames[e] }

type ClassifiedElement struct {
	Type ElementType
	Text string
}

// FormatClassifiedElements renders classified document elements as compact
// Markdown while preserving their semantic heading and list boundaries.
func FormatClassifiedElements(elements []ClassifiedElement) string {
	var output strings.Builder
	output.WriteString("\n")
	for _, element := range elements {
		switch element.Type {
		case ElemTitle:
			output.WriteString("\n# " + element.Text + "\n\n")
		case ElemSectionHeader:
			output.WriteString("\n## " + element.Text + "\n\n")
		case ElemSubHeader:
			output.WriteString("\n### " + element.Text + "\n\n")
		case ElemListItem:
			output.WriteString("* " + element.Text + "\n")
		case ElemTable:
			output.WriteString("\n" + element.Text + "\n")
		case ElemPageBreak:
			output.WriteString("\n---\n")
		default:
			output.WriteString(element.Text + "\n\n")
		}
	}
	return strings.TrimSpace(output.String())
}

// ====================== Regex Patterns (ported from nlp/patterns.py) ======================

var (
	// Unicode bullets from Unstructured's UNICODE_BULLETS list
	reUnicodeBullet = regexp.MustCompile(
		`^(|•|‣|⁃|ㅤ|⁌|⁍|` +
			`∙|○|●|◘|◦|☙|❥|❧|` +
			`⦾|⦿|-|–|·|\*)`,
	)
	// Enumerated bullets: "1.", "a)", "(1)"
	reEnumeratedBullet = regexp.MustCompile(`^(?:(?:\d{1,3}|[a-z][A-Z])\.?){1,3}`)
	// Numbered list: "1. text" or "1) text"
	reNumberedList = regexp.MustCompile(`^\d+(\.|\))\s(.+)`)
	// Ends with punctuation: .!?。！？
	reEndsInPunct = regexp.MustCompile(`[.。!！?？]\s*$`)
	// Email address
	reEmail = regexp.MustCompile(`^[a-z0-9\.\-+_]+@[a-z0-9\.\-+_]+\.[a-z]+$`)
	// Page break: "---", "=== PAGE 1 ===", plain page number
	rePageBreak = regexp.MustCompile(`(?i)^(?:[-–—=]{4,}$|page\s*\d+|^\d{1,4}$)`)
	// URL
	reURL = regexp.MustCompile(`^https?://|^www\.`)
	// Table: pipe delimiters
	reTablePipe = regexp.MustCompile(`\|.*\|`)
	// All uppercase
	reAllCaps = regexp.MustCompile(`^[A-Z\s\d\p{P}]+$`)
	// Chinese numbered heading: "一、", "二.", "三、"
	reChineseNumbered = regexp.MustCompile(`^[一二三四五六七八九十百千]+[、．.]`)
	// Numbered section: "1.2.3", "Section 1"
	reNumberedSection = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3})+\s`)
	// Sentence boundary for simple sentence counting
	reSentenceBoundary = regexp.MustCompile(`[.。!！?？][\s\n]`)
)

// ====================== Classification Thresholds ======================

const (
	titleMaxWords       = 12  // max words for a title
	sentenceMinWords    = 5   // min words to count as a sentence
	narrativeCapThresh  = 0.5 // cap ratio > this → not narrative
	nonAlphaThresh      = 0.5 // non-alpha ratio > this → not title/narrative
	titleNonAlphaThresh = 0.5 // title-specific
)

// ====================== Main Classification ======================

// ClassifyText partitions raw document text into semantic elements.
// This is the Go equivalent of Unstructured's auto.partition → pdf.partition_pdf_or_image.
func ClassifyText(text string) []ClassifiedElement {
	lines := strings.Split(text, "\n")
	var elems []ClassifiedElement
	var buf strings.Builder
	bufType := ElemUncategorized
	prevBlank := true

	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			elems = append(elems, ClassifiedElement{Type: bufType, Text: s})
		}
		buf.Reset()
		bufType = ElemUncategorized
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			prevBlank = true
			continue
		}

		// Page break
		if rePageBreak.MatchString(trimmed) && len(trimmed) < 25 {
			flush()
			elems = append(elems, ClassifiedElement{Type: ElemPageBreak, Text: ""})
			prevBlank = true
			continue
		}

		// Skip noise: email addresses, URLs
		if reEmail.MatchString(trimmed) || reURL.MatchString(trimmed) {
			continue
		}

		elemType := classifyLine(trimmed, prevBlank)

		// Merge consecutive same-type elements (except headings)
		if elemType == bufType &&
			elemType != ElemTitle && elemType != ElemSectionHeader && elemType != ElemSubHeader &&
			elemType != ElemListItem {
			buf.WriteString(" ")
			buf.WriteString(trimmed)
		} else {
			flush()
			buf.WriteString(trimmed)
			bufType = elemType
		}
		prevBlank = false
	}
	flush()
	return elems
}

// classifyLine determines the element type of a single line.
// Faithful to Unstructured's is_possible_title + is_possible_narrative_text + is_bulleted_text.
func classifyLine(line string, followsBlank bool) ElementType {
	// Table: at least 2 pipe delimiters (markdown table format) or tab-separated columns
	if strings.Count(line, "|") >= 2 && strings.Count(line, "|") < 50 {
		return ElemTable
	}
	if isTableRow(line) {
		return ElemTable
	}

	// Chinese numbered headings
	if reChineseNumbered.MatchString(line) && wordCount(line) <= titleMaxWords {
		return ElemSectionHeader
	}
	// Numbered section headings
	if reNumberedSection.MatchString(line) && wordCount(line) <= titleMaxWords {
		return ElemSectionHeader
	}

	// Numbered list: "1. text"
	if reNumberedList.MatchString(line) {
		return ElemListItem
	}

	// Bulleted list
	if isBulletedText(line) {
		return ElemListItem
	}

	// Title check (ported from is_possible_title)
	if isPossibleTitle(line) {
		wc := wordCount(line)
		if wc <= 3 || reAllCaps.MatchString(line) {
			return ElemTitle
		}
		if wc <= 6 {
			return ElemSectionHeader
		}
		return ElemSubHeader
	}

	// Narrative text check (ported from is_possible_narrative_text)
	if isPossibleNarrativeText(line) {
		return ElemNarrativeText
	}

	// Short line following blank → likely section header
	if followsBlank && wordCount(line) <= 6 && !reEndsInPunct.MatchString(line) && hasEnoughAlpha(line) {
		return ElemSectionHeader
	}

	if len(line) > 5 {
		return ElemNarrativeText
	}
	return ElemUncategorized
}

// ====================== is_possible_title (ported) ======================

func isPossibleTitle(text string) bool {
	if len(text) == 0 {
		return false
	}

	// All-caps + ends with punctuation → NOT a title (likely a header/footer)
	if strings.ToUpper(text) == text && reEndsInPunct.MatchString(text) {
		return false
	}

	// Word count check
	if wordCount(text) > titleMaxWords {
		return false
	}

	// Non-alpha ratio check
	if underNonAlphaRatio(text, titleNonAlphaThresh) {
		return false
	}

	// Comma ending → not a title (e.g. "To My Dearest Friends,")
	if strings.TrimSpace(text) != "" && text[len(text)-1] == ',' {
		return false
	}

	// All numeric → not a title
	if isAllDigits(text) {
		return false
	}

	// Sentence count: titles should be ≤ 1 sentence (min 5 words per sentence)
	if sentenceCount(text, sentenceMinWords) > 1 {
		return false
	}

	return true
}

// ====================== is_possible_narrative_text (ported) ======================

func isPossibleNarrativeText(text string) bool {
	if len(text) == 0 {
		return false
	}

	// All numeric → not narrative
	if isAllDigits(text) {
		return false
	}

	// Cap ratio: too many capitalized words → not narrative (likely a title)
	if exceedsCapRatio(text, narrativeCapThresh) {
		return false
	}

	// Non-alpha ratio: not enough letters → not narrative
	if underNonAlphaRatio(text, nonAlphaThresh) {
		return false
	}

	// Must have 2+ sentences OR contain a verb-like structure
	// (Simplified from Unstructured's POS tagger dependency)
	if sentenceCount(text, sentenceMinWords) < 2 && !containsVerbLike(text) {
		return false
	}

	return true
}

// ====================== is_bulleted_text (ported) ======================

func isBulletedText(text string) bool {
	return reUnicodeBullet.MatchString(strings.TrimSpace(text))
}

// ====================== exceeds_cap_ratio (ported) ======================

func exceedsCapRatio(text string, threshold float64) bool {
	// Only apply to single-sentence text
	if sentenceCount(text, 3) > 1 {
		return false
	}
	// All uppercase → exceeds
	if strings.ToUpper(text) == text {
		return true
	}
	// Tokenize: keep only alpha tokens (matches Unstructured behavior)
	tokens := wordTokenizeAlpha(text)
	if len(tokens) == 0 {
		return true // avoid misclassification as narrative
	}
	capitalized := 0
	for _, t := range tokens {
		if len(t) > 0 && (unicode.IsUpper([]rune(t)[0]) || strings.ToUpper(t) == t) {
			capitalized++
		}
	}
	return float64(capitalized)/float64(len(tokens)) > threshold
}

// ====================== under_non_alpha_ratio (ported) ======================

func underNonAlphaRatio(text string, threshold float64) bool {
	if len(text) == 0 {
		return false
	}
	alphaCount := 0
	totalCount := 0
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		totalCount++
		if unicode.IsLetter(r) {
			alphaCount++
		}
	}
	if totalCount == 0 {
		return false
	}
	return float64(alphaCount)/float64(totalCount) < threshold
}

// ====================== sentence_count (ported) ======================

func sentenceCount(text string, minWords int) int {
	sentences := sentTokenize(text)
	count := 0
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if wordCount(removePunctuation(s)) >= minWords {
			count++
		}
	}
	if count == 0 && wordCount(text) >= minWords {
		return 1
	}
	return count
}

// sentTokenize splits text into sentences with abbreviation awareness.
func sentTokenize(text string) []string {
	if text == "" {
		return nil
	}
	rawParts := reSentenceBoundary.Split(text, -1)
	var sentences []string
	for i, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i > 0 && len(sentences) > 0 {
			prev := strings.ToLower(strings.TrimSpace(sentences[len(sentences)-1]))
			words := strings.Fields(prev)
			if len(words) > 0 {
				lastWord := strings.TrimSuffix(strings.ToLower(words[len(words)-1]), ".")
				if abbreviations[lastWord] {
					sentences[len(sentences)-1] = prev + ". " + part
					continue
				}
			}
		}
		sentences = append(sentences, part)
	}
	return sentences
}

var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
	"sr": true, "jr": true, "st": true, "ave": true, "blvd": true,
	"inc": true, "ltd": true, "co": true, "corp": true, "dept": true,
	"vs": true, "etc": true, "al": true, "approx": true,
	"jan": true, "feb": true, "mar": true, "apr": true, "jun": true,
	"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	"vol": true, "ed": true, "e.g": true, "i.e": true, "a.m": true, "p.m": true,
	"no": true, "nos": true, "art": true, "fig": true, "rev": true, "sec": true,
	"u.s": true, "u.k": true, "a.d": true, "b.c": true, "ph.d": true,
}

// ====================== Helpers ======================

// getGseSeg returns the global gse segmenter (lazy init, thread-safe).
func getGseSeg() *gse.Segmenter {
	gseOnce.Do(func() {
		var err error
		gseSeg, err = gse.NewEmbed("zh,en")
		if err != nil {
			log.Printf("[NLP] gse init fallback: %v", err)
			gseSeg, _ = gse.NewEmbed()
		}
	})
	return &gseSeg
}

func wordCount(text string) int {
	return len(strings.Fields(text))
}

func wordTokenizeAlpha(text string) []string {
	seg := getGseSeg()
	tokens := seg.CutAll(text)
	var alphaOnly []string
	for _, t := range tokens {
		isAlpha := true
		for _, r := range t {
			if !unicode.IsLetter(r) {
				isAlpha = false
				break
			}
		}
		if isAlpha && len(t) > 0 {
			alphaOnly = append(alphaOnly, t)
		}
	}
	return alphaOnly
}

func isAllDigits(text string) bool {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, ",", ""), ".", ""), " ", "")
	for _, r := range cleaned {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(cleaned) > 0
}

func hasEnoughAlpha(text string) bool {
	return !underNonAlphaRatio(text, nonAlphaThresh)
}

func removePunctuation(text string) string {
	var out strings.Builder
	for _, r := range text {
		if !unicode.IsPunct(r) && r != '.' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func containsVerbLike(text string) bool {
	// Use gse POS tagging for accurate verb detection (Chinese + English).
	// gse POS tags follow ICTCLAS: v=verb, vd=adverb-verb, vn=noun-verb, vi=intransitive
	seg := getGseSeg()
	segments := seg.ModeSegment([]byte(text), false)
	for _, s := range segments {
		pos := s.Token().Pos()
		if strings.HasPrefix(pos, "v") || strings.HasPrefix(pos, "V") {
			return true
		}
	}
	// Fallback: simple pattern matching for edge cases gse might miss
	verbPatterns := []string{
		" is ", " are ", " was ", " were ", " has ", " have ", " had ",
		" do ", " does ", " did ", " can ", " could ", " will ", " would ",
		" should ", " may ", " might ", " must ", " shall ",
	}
	lower := strings.ToLower(" " + text + " ")
	for _, p := range verbPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isTableRow(line string) bool {
	if strings.Count(line, "\t") >= 2 {
		return true
	}
	// Check for 3+ columns separated by 3+ spaces
	reSpaces := regexp.MustCompile(`\s{3,}`)
	parts := reSpaces.Split(line, -1)
	nonEmpty := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty++
		}
	}
	return nonEmpty >= 3 && len(parts) <= 20
}
