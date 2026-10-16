package memory

import (
	"context"
	"errors"
	"strings"
)

type ConversationFactSource struct {
	Fact     Fact
	Evidence string
}

// ConversationFactSources only reads facts with evidence and an explicitly
// supplied conversation audience. The host must validate those conversations
// and match each quote against their persisted user messages before display.
func (kg *KnowledgeGraph) ConversationFactSources(ctx context.Context, sessionIDs []string, limit int) ([]ConversationFactSource, error) {
	if kg == nil || kg.db == nil || len(sessionIDs) > 200 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid conversation fact source request")
	}
	result := make([]ConversationFactSource, 0)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	args := make([]interface{}, 0, len(sessionIDs)+1)
	placeholders := make([]string, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		if strings.TrimSpace(id) == "" || len(id) > 64 {
			return nil, errors.New("invalid conversation audience")
		}
		placeholders = append(placeholders, "?")
		args = append(args, ConversationSessionSource(id))
	}
	args = append(args, limit)
	kg.mu.RLock()
	defer kg.mu.RUnlock()
	rows, err := kg.db.QueryContext(ctx, `SELECT t.id,t.subject,t.predicate,t.object,t.source,t.valid_from,t.valid_to,t.confidence,t.created_at,e.content
		FROM triples t JOIN fact_evidence e ON e.fact_id=t.id
		WHERE t.source IN (`+strings.Join(placeholders, ",")+`) AND (t.valid_from='' OR t.valid_from <= date('now')) AND (t.valid_to='' OR t.valid_to > date('now'))
		ORDER BY t.created_at DESC,t.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ConversationFactSource
		fact := &item.Fact
		if err := rows.Scan(&fact.ID, &fact.Subject, &fact.Predicate, &fact.Object, &fact.Source, &fact.ValidFrom, &fact.ValidTo, &fact.Confidence, &fact.CreatedAt, &item.Evidence); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
