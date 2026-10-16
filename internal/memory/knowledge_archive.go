package memory

import (
	"context"
	"errors"
	"strings"
)

// KnowledgeArchiveItem is a historical record, never an authoritative Entry.
// Workspace-only legacy records have no verified conversation audience.
type KnowledgeArchiveItem struct {
	ID               int64
	Title            string
	Content          string
	Source           string
	CreatedAt        string
	Subject          string
	Predicate        string
	Object           string
	ValidFrom        string
	ValidTo          string
	Confidence       float64
	Evidence         string
	ContentTruncated bool
}

// WorkspaceKnowledgeArchive pages existing graph records without importing or
// changing their recall eligibility. The host must authorize the local workspace
// owner and validate the supplied conversation audience. Original quotations
// with a session audience are deliberately excluded: this archive cannot prove
// that their content came from that session's original user messages.
func (kg *KnowledgeGraph) WorkspaceKnowledgeArchive(ctx context.Context, sessionIDs []string, quotes bool, search string, offset, limit int) ([]KnowledgeArchiveItem, int64, error) {
	return kg.workspaceKnowledgeArchive(ctx, sessionIDs, quotes, search, offset, limit, nil)
}

func (kg *KnowledgeGraph) workspaceKnowledgeArchive(ctx context.Context, sessionIDs []string, quotes bool, search string, offset, limit int, predicates []string) ([]KnowledgeArchiveItem, int64, error) {
	maxLimit := 100
	if len(predicates) > 0 {
		maxLimit = 200
	}
	if kg == nil || kg.db == nil || len(sessionIDs) > 200 || offset < 0 || offset > 100000 || limit < 1 || limit > maxLimit || len([]rune(search)) > 100 {
		return nil, 0, errors.New("invalid knowledge archive request")
	}
	args := make([]interface{}, 0, len(sessionIDs)+1)
	audience := "(source NOT LIKE 'conversation:session:%'"
	if !quotes && len(sessionIDs) > 0 {
		placeholders := make([]string, 0, len(sessionIDs))
		for _, id := range sessionIDs {
			if strings.TrimSpace(id) == "" || len(id) > 64 {
				return nil, 0, errors.New("invalid archive audience")
			}
			placeholders = append(placeholders, "?")
			args = append(args, ConversationSessionSource(id))
		}
		audience += " OR source IN (" + strings.Join(placeholders, ",") + ")"
	}
	audience += ")"
	if len(predicates) > 0 {
		placeholders := make([]string, 0, len(predicates))
		for _, predicate := range predicates {
			placeholders = append(placeholders, "?")
			args = append(args, predicate)
		}
		audience += " AND predicate IN (" + strings.Join(placeholders, ",") + ")"
	}
	table, columns, textColumns := "triples", "id,substr(subject,1,500),substr(predicate,1,500)||' · '||substr(object,1,8000),source,created_at", "subject||' '||predicate||' '||object"
	if quotes {
		table, columns, textColumns = "verbatim_memories", "id,substr(entity,1,500),substr(content,1,8000),source,created_at", "entity||' '||content"
		columns += ",length(content)>8000"
	} else {
		columns += ",substr(subject,1,500),substr(predicate,1,500),substr(object,1,8000),valid_from,valid_to,confidence,COALESCE((SELECT substr(content,1,1200) FROM fact_evidence WHERE fact_id=triples.id),''),length(object)>8000"
	}
	if search = strings.TrimSpace(search); search != "" {
		audience += " AND instr(lower(" + textColumns + "),lower(?)) > 0"
		args = append(args, search)
	}
	kg.mu.RLock()
	defer kg.mu.RUnlock()
	var total int64
	if err := kg.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+audience, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := kg.db.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE "+audience+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]KnowledgeArchiveItem, 0, limit)
	for rows.Next() {
		var item KnowledgeArchiveItem
		dest := []interface{}{&item.ID, &item.Title, &item.Content, &item.Source, &item.CreatedAt}
		if !quotes {
			dest = append(dest, &item.Subject, &item.Predicate, &item.Object, &item.ValidFrom, &item.ValidTo, &item.Confidence, &item.Evidence)
		}
		dest = append(dest, &item.ContentTruncated)
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}
