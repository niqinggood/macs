package ragmodel

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 Vector Search — pure Go, zero dependencies, cross-platform.
// BM25 is a significant upgrade over TF-IDF: it handles term saturation and document length normalization.

type VectorIndex struct {
	docs      []indexedDoc
	df        map[string]int
	totalDocs int
	avgDocLen float64 // average document length in terms
}

type indexedDoc struct {
	id      string
	content string
	terms   []string // raw terms for BM25 scoring
	tf      map[string]float64
	docLen  int
}

func NewVectorIndex() *VectorIndex {
	return &VectorIndex{df: make(map[string]int)}
}

func (v *VectorIndex) Add(id, content string) {
	terms := tokenize(content)
	if len(terms) == 0 {
		return
	}
	tf := make(map[string]float64)
	seen := make(map[string]bool)
	for _, t := range terms {
		tf[t]++
		if !seen[t] {
			v.df[t]++
			seen[t] = true
		}
	}
	v.docs = append(v.docs, indexedDoc{id: id, content: content, terms: terms, tf: tf, docLen: len(terms)})
	v.totalDocs++
	v.avgDocLen = float64(v.totalTerms()) / float64(v.totalDocs)
}

func (v *VectorIndex) totalTerms() int {
	n := 0
	for _, d := range v.docs {
		n += d.docLen
	}
	return n
}

// BM25 parameters (standard defaults)
const (
	bm25k1 = 1.5  // term frequency saturation
	bm25b  = 0.75 // document length normalization
)

// Search finds top-N docs using BM25 scoring.
func (v *VectorIndex) Search(query string, limit int) []SearchHit {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 || len(v.docs) == 0 {
		return nil
	}

	// Count query term frequencies
	queryTF := make(map[string]float64)
	for _, t := range queryTerms {
		queryTF[t]++
	}

	type hit struct {
		id      string
		content string
		score   float64
	}
	var hits []hit
	N := float64(v.totalDocs)

	for _, doc := range v.docs {
		score := v.bm25Score(queryTF, doc, N)
		if score > 0.001 {
			hits = append(hits, hit{doc.id, doc.content, score})
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}

	result := make([]SearchHit, len(hits))
	for i, h := range hits {
		result[i] = SearchHit{ID: h.id, Content: h.content, Score: h.score}
	}
	return result
}

// bm25Score computes BM25 score for a query against a document.
func (v *VectorIndex) bm25Score(queryTF map[string]float64, doc indexedDoc, N float64) float64 {
	score := 0.0
	for term, qf := range queryTF {
		df := float64(v.df[term])
		if df == 0 {
			continue
		}
		// IDF component
		idf := math.Log(1 + (N-df+0.5)/(df+0.5))
		// TF component with saturation
		tf := float64(doc.tf[term])
		docLenNorm := 1 - bm25b + bm25b*float64(doc.docLen)/v.avgDocLen
		tfScore := (tf * (bm25k1 + 1)) / (tf + bm25k1*docLenNorm)
		// Query term frequency boost
		score += idf * tfScore * qf
	}
	return score
}

type SearchHit struct {
	ID      string
	Content string
	Score   float64
}

// tokenize splits text into terms.
// Chinese: uses bigrams (and optionally gojieba if available).
// English: word-level tokenization.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	terms := tryGojiebaTokenize(text)
	if len(terms) > 0 {
		return terms
	}
	return basicTokenize(text)
}

// tryGojiebaTokenize attempts Chinese word segmentation using gojieba.
func tryGojiebaTokenize(text string) []string {
	// Check if text has CJK characters
	hasCJK := false
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			hasCJK = true
			break
		}
	}
	if !hasCJK {
		return nil
	}
	return gojiebaCut(text)
}

// basicTokenize is the fallback: bigrams for Chinese, words for English.
func basicTokenize(text string) []string {
	var terms []string
	var current strings.Builder

	var cjkBuf []rune
	flushCJK := func() {
		for i := 0; i < len(cjkBuf)-1; i++ {
			terms = append(terms, string(cjkBuf[i])+string(cjkBuf[i+1]))
		}
		for _, r := range cjkBuf {
			terms = append(terms, string(r))
		}
		cjkBuf = nil
	}

	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			if current.Len() > 0 {
				terms = append(terms, current.String())
				current.Reset()
			}
			cjkBuf = append(cjkBuf, r)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			flushCJK()
			current.WriteRune(r)
		} else {
			flushCJK()
			if current.Len() > 0 {
				terms = append(terms, current.String())
				current.Reset()
			}
		}
	}
	flushCJK()
	if current.Len() > 0 {
		terms = append(terms, current.String())
	}

	filtered := make([]string, 0, len(terms))
	for _, t := range terms {
		if len(t) >= 1 {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
