package memory

import (
	"context"
	"strings"
	"unicode"

	"gorm.io/gorm/clause"
	"mac/internal/ragmodel"
)

// Supplement the recent pool by match strength BEFORE truncation. All clauses
// retain the trusted namespace and session/state filters.
func (s *Service) searchRecallEntries(ctx context.Context, owner Owner, question string) ([]Entry, error) {
	terms := recallSearchTerms(question)
	if len(terms) == 0 {
		return nil, nil
	}
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	where, args := recallLikeQuery(terms, "key", "value", "tags")
	rank, rankArgs := recallMatchRank(terms, "key", "value", "tags")
	var entries []Entry
	err = query.Where("state = ? AND (scope <> ? OR session_id = ?)", "active", MemorySession, owner.SessionID).
		Where(where, args...).Clauses(clause.OrderBy{Expression: clause.Expr{SQL: rank + " DESC, priority DESC, updated_at DESC, id ASC", Vars: rankArgs}}).
		Limit(200).Find(&entries).Error
	return entries, err
}

func (s *Service) searchRecallSummaries(ctx context.Context, namespace, question string) ([]Summary, error) {
	terms := recallSearchTerms(question)
	if len(terms) == 0 {
		return nil, nil
	}
	where, args := recallLikeQuery(terms, "content")
	rank, rankArgs := recallMatchRank(terms, "content")
	var summaries []Summary
	err := s.db.WithContext(ctx).Where("namespace = ?", namespace).Where(where, args...).
		Clauses(clause.OrderBy{Expression: clause.Expr{SQL: rank + " DESC, updated_at DESC, id ASC", Vars: rankArgs}}).
		Limit(50).Find(&summaries).Error
	return summaries, err
}

func recallLikeQuery(terms []string, columns ...string) (string, []interface{}) {
	var parts []string
	var args []interface{}
	for _, term := range terms {
		for _, column := range columns {
			// columns are fixed implementation constants, never user input.
			parts = append(parts, "LOWER("+column+") LIKE ? ESCAPE '\\'")
			literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
			args = append(args, "%"+literal+"%")
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func recallMatchRank(terms []string, columns ...string) (string, []interface{}) {
	var parts []string
	var args []interface{}
	for _, term := range terms {
		where, values := recallLikeQuery([]string{term}, columns...)
		parts = append(parts, "CASE WHEN "+where+" THEN ? ELSE 0 END")
		args = append(args, values...)
		args = append(args, recallTermWeight(term))
	}
	return "(" + strings.Join(parts, " + ") + ")", args
}

// General conversational fillers carry no topic evidence. Reuse the existing
// segmenter and shared stop words; avoid generating every CJK pair in prose.
var recallConversationWords = map[string]bool{
	"我们": true, "你们": true, "他们": true, "她们": true, "它们": true,
	"这些": true, "那些": true, "这样": true, "那样": true, "这里": true, "那里": true,
	"一下": true, "一下子": true, "看看": true, "聊聊": true, "请问": true, "帮忙": true,
	"麻烦": true, "好的": true, "谢谢": true, "这个": true, "那个": true,
	"please": true, "thanks": true, "thank": true, "hello": true,
}

func recallSearchTerms(question string) []string {
	question = strings.ToLower(truncateSessionRunes(strings.TrimSpace(question), 4096))
	if question == "" {
		return nil
	}
	terms := make([]string, 0, 24)
	seen := map[string]bool{}
	add := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || ragmodel.IsStopToken(term) || recallConversationWords[term] || ragmodel.IsPurePunctuation(term) || seen[term] || len(terms) >= 24 {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	hasHan := strings.ContainsFunc(question, func(r rune) bool { return unicode.Is(unicode.Han, r) })
	if hasHan {
		for _, term := range ragmodel.GoJiebaCutSearch(question) {
			add(term)
		}
	}
	words := strings.FieldsFunc(question, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	for _, word := range words {
		add(word)
	}
	return terms
}

func recallTermWeight(term string) int { return min(8, len([]rune(term))) + 2 }

func memoryRelevance(question, text string) int {
	return memoryRelevanceTerms(recallSearchTerms(question), text)
}

func memoryRelevanceTerms(terms []string, text string) int {
	content := strings.ToLower(text)
	score := 0
	for _, term := range terms {
		if strings.Contains(content, term) {
			score += recallTermWeight(term)
		}
	}
	return min(score, 40)
}
