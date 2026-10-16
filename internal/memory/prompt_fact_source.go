package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ReadPromptFactSource locates one original, currently valid fact. Its session
// audience must already have been authorized by the host.
func (kg *KnowledgeGraph) ReadPromptFactSource(ctx context.Context, sessionID string, id int64) (*CurrentSource, error) {
	if kg == nil || kg.db == nil || sessionID == "" || id < 1 {
		return nil, errors.New("invalid fact source")
	}
	kg.mu.RLock()
	defer kg.mu.RUnlock()
	var value string
	err := kg.db.QueryRowContext(ctx, `SELECT object FROM triples WHERE id=? AND source=?
		AND (valid_from='' OR valid_from<=date('now')) AND (valid_to='' OR valid_to>date('now'))`, id, ConversationSessionSource(sessionID)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return &CurrentSource{Missing: true}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(value) > 96<<10 {
		return nil, errors.New("fact source exceeds text limit")
	}
	digest := promptContentDigest(value)
	return &CurrentSource{Content: value, Revision: digest, Digest: digest, Editable: true, Deletable: true}, nil
}

func (kg *KnowledgeGraph) ChangePromptFactSource(ctx context.Context, sessionID string, id int64, expected, action, value string) (*CurrentSource, error) {
	if kg == nil || kg.db == nil || sessionID == "" || id < 1 || expected == "" || len(value) > 96<<10 ||
		(action != "delete" && action != "update") || action == "update" && strings.TrimSpace(value) == "" {
		return nil, errors.New("invalid fact source change")
	}
	kg.mu.Lock()
	var original string
	err := kg.db.QueryRowContext(ctx, `SELECT object FROM triples WHERE id=? AND source=?
		AND (valid_from='' OR valid_from<=date('now')) AND (valid_to='' OR valid_to>date('now'))`, id, ConversationSessionSource(sessionID)).Scan(&original)
	if err != nil || promptContentDigest(original) != expected {
		kg.mu.Unlock()
		return nil, ErrPromptSourceConflict
	}
	var result sql.Result
	if action == "delete" {
		// Retain temporal provenance; invalidate only this exact row, never
		// every matching subject/predicate/object in other conversations.
		result, err = kg.db.ExecContext(ctx, `UPDATE triples SET valid_to=date('now') WHERE id=? AND source=? AND object=?
			AND (valid_from='' OR valid_from<=date('now')) AND (valid_to='' OR valid_to>date('now'))`, id, ConversationSessionSource(sessionID), original)
	} else {
		result, err = kg.db.ExecContext(ctx, `UPDATE triples SET object=? WHERE id=? AND source=? AND object=?
			AND (valid_from='' OR valid_from<=date('now')) AND (valid_to='' OR valid_to>date('now'))`, value, id, ConversationSessionSource(sessionID), original)
	}
	if err == nil {
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			err = rowsErr
		} else if rows != 1 {
			err = ErrPromptSourceConflict
		}
	}
	kg.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return kg.ReadPromptFactSource(ctx, sessionID, id)
}
