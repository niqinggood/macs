package memory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Triple remains a root compatibility name for the memory domain model.
type Triple = Fact

// KnowledgeGraph is a SQLite-backed temporal entity-relationship graph.
// Competing with Zep/Neo4j but local and free.
type KnowledgeGraph struct {
	mu       sync.RWMutex
	db       *sql.DB
	storeDir string
}

// entityType extracts the likely entity type for indexing priority.
// People > Projects > Tools > Concepts
func entityType(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "用户") || strings.Contains(lower, "user") || strings.Contains(lower, "我") {
		return "person"
	}
	if strings.Contains(lower, "项目") || strings.Contains(lower, "project") || strings.Contains(lower, "/") {
		return "project"
	}
	return "concept"
}

func NewKnowledgeGraph(storeDir string) (*KnowledgeGraph, error) {
	os.MkdirAll(storeDir, 0755)
	dbPath := filepath.Join(storeDir, "knowledge_graph.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open kg db: %w", err)
	}
	db.SetMaxOpenConns(1)

	kg := &KnowledgeGraph{db: db, storeDir: storeDir}
	if err := kg.initSchema(); err != nil {
		return nil, err
	}
	return kg, nil
}

func (kg *KnowledgeGraph) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS triples (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subject TEXT NOT NULL,
		predicate TEXT NOT NULL,
		object TEXT NOT NULL,
		source TEXT DEFAULT '',
		valid_from TEXT DEFAULT '',
		valid_to TEXT DEFAULT '',
		confidence REAL DEFAULT 1.0,
		entity_type TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_triples_subject ON triples(subject);
	CREATE INDEX IF NOT EXISTS idx_triples_predicate ON triples(predicate);
	CREATE INDEX IF NOT EXISTS idx_triples_object ON triples(object);
	CREATE INDEX IF NOT EXISTS idx_triples_type ON triples(entity_type);
	CREATE INDEX IF NOT EXISTS idx_triples_valid ON triples(valid_from, valid_to);

	CREATE TABLE IF NOT EXISTS entity_aliases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		canonical TEXT NOT NULL,
		alias TEXT NOT NULL UNIQUE,
		created_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_aliases_canonical ON entity_aliases(canonical);

	CREATE TABLE IF NOT EXISTS verbatim_memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_verbatim_entity ON verbatim_memories(entity);
	CREATE TABLE IF NOT EXISTS fact_evidence (
		fact_id INTEGER PRIMARY KEY,
		content TEXT NOT NULL,
		FOREIGN KEY(fact_id) REFERENCES triples(id)
	);
	`
	_, err := kg.db.Exec(schema)
	return err
}

// AddTriple adds a fact with temporal validity.
func (kg *KnowledgeGraph) AddTriple(subject, predicate, object, source string, validFrom, validTo string, confidence float64) error {
	kg.mu.Lock()
	defer kg.mu.Unlock()

	// Resolve aliases
	subject = kg.resolveLocked(subject)

	// A different value is not proof of a correction: relations can be
	// multi-valued. Explicit manual corrections use the versioned memory tool.
	eType := entityType(subject)
	_, err := kg.db.Exec(
		`INSERT INTO triples (subject, predicate, object, source, valid_from, valid_to, confidence, entity_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		subject, predicate, object, source, validFrom, validTo, confidence, eType,
	)
	return err
}

// AddFactWithEvidence commits the fact and its validated user quote together.
// Repeated extraction of the same fact in one source is idempotent.
func (kg *KnowledgeGraph) AddFactWithEvidence(ctx context.Context, fact Fact, quote string) error {
	if strings.TrimSpace(quote) == "" || strings.TrimSpace(fact.Subject) == "" || strings.TrimSpace(fact.Predicate) == "" || strings.TrimSpace(fact.Object) == "" || fact.Source == "" {
		return fmt.Errorf("fact and evidence source are required")
	}
	kg.mu.Lock()
	defer kg.mu.Unlock()
	fact.Subject = kg.resolveLocked(fact.Subject)
	tx, err := kg.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM triples WHERE subject = ? AND predicate = ? AND object = ? AND source = ? AND valid_to = '' LIMIT 1`, fact.Subject, fact.Predicate, fact.Object, fact.Source).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		result, err := tx.ExecContext(ctx, `INSERT INTO triples (subject, predicate, object, source, valid_from, valid_to, confidence, entity_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, fact.Subject, fact.Predicate, fact.Object, fact.Source, fact.ValidFrom, fact.ValidTo, fact.Confidence, entityType(fact.Subject))
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO fact_evidence (fact_id, content) VALUES (?, ?)`, id, quote); err != nil {
		return err
	}
	return tx.Commit()
}

func (kg *KnowledgeGraph) FactEvidence(id int64) (string, error) {
	var quote string
	err := kg.db.QueryRow(`SELECT content FROM fact_evidence WHERE fact_id = ?`, id).Scan(&quote)
	return quote, err
}

// The process-global graph must never feed agents in another workspace.
func (kg *KnowledgeGraph) BelongsToWorkspace(workspace string) bool {
	return kg != nil && canonicalContextStoreDir(kg.storeDir) == canonicalContextStoreDir(filepath.Join(workspace, ".openkitty"))
}

// QueryEntity returns all currently-valid facts about an entity.
// asOf filters to facts valid at a specific time (ISO date or empty for "now").
func (kg *KnowledgeGraph) QueryEntity(entity string, asOf string) ([]Triple, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	entity = kg.resolveLocked(entity)
	if asOf == "" {
		asOf = time.Now().Format("2006-01-02")
	}

	rows, err := kg.db.Query(
		`SELECT id, subject, predicate, object, source, valid_from, valid_to, confidence, created_at
		 FROM triples
		 WHERE subject = ? AND (valid_from <= ? OR valid_from = '')
		   AND (valid_to > ? OR valid_to = '')
		 ORDER BY entity_type = 'person' DESC, entity_type = 'project' DESC, created_at DESC`,
		entity, asOf, asOf,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTriples(rows)
}

// QueryRelation finds all connections between two entities.
func (kg *KnowledgeGraph) QueryRelation(subject, object string) ([]Triple, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	subject = kg.resolveLocked(subject)
	object = kg.resolveLocked(object)

	rows, err := kg.db.Query(
		`SELECT id, subject, predicate, object, source, valid_from, valid_to, confidence, created_at
		 FROM triples
		 WHERE (subject = ? AND object = ?) OR (subject = ? AND object = ?)
		 ORDER BY created_at DESC`,
		subject, object, object, subject,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriples(rows)
}

// SearchFacts does a fuzzy search across all triples.
func (kg *KnowledgeGraph) SearchFacts(query string, limit int) ([]Triple, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + query + "%"
	rows, err := kg.db.Query(
		`SELECT id, subject, predicate, object, source, valid_from, valid_to, confidence, created_at
		 FROM triples
		 WHERE subject LIKE ? OR predicate LIKE ? OR object LIKE ?
		 ORDER BY entity_type = 'person' DESC, created_at DESC
		 LIMIT ?`,
		pattern, pattern, pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriples(rows)
}

// Invalidate marks a triple as no longer valid (sets valid_to).
func (kg *KnowledgeGraph) Invalidate(subject, predicate, object string) error {
	kg.mu.Lock()
	defer kg.mu.Unlock()

	subject = kg.resolveLocked(subject)
	now := time.Now().Format("2006-01-02")
	_, err := kg.db.Exec(
		`UPDATE triples SET valid_to = ? WHERE subject = ? AND predicate = ? AND object = ? AND valid_to = ''`,
		now, subject, predicate, object,
	)
	return err
}

// AllFacts returns all currently-valid facts (for system prompt injection).
func (kg *KnowledgeGraph) AllFacts(limit int) ([]Triple, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}
	asOf := time.Now().Format("2006-01-02")
	rows, err := kg.db.Query(
		`SELECT id, subject, predicate, object, source, valid_from, valid_to, confidence, created_at
		 FROM triples
		 WHERE (valid_from <= ? OR valid_from = '')
		   AND (valid_to > ? OR valid_to = '')
		 ORDER BY entity_type = 'person' DESC, entity_type = 'project' DESC, created_at DESC
		 LIMIT ?`,
		asOf, asOf, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriples(rows)
}

// AddAlias registers an alternative name for an entity.
func (kg *KnowledgeGraph) AddAlias(canonical, alias string) error {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	_, err := kg.db.Exec(
		`INSERT OR IGNORE INTO entity_aliases (canonical, alias) VALUES (?, ?)`,
		canonical, alias,
	)
	return err
}

// AddVerbatim stores an original user utterance linked to an entity.
// Verbatim is never summarized — this is the foundational promise (MemPalace).
func (kg *KnowledgeGraph) AddVerbatim(entity, content, source string) error {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	entity = kg.resolveLocked(entity)
	_, err := kg.db.Exec(
		`INSERT INTO verbatim_memories (entity, content, source) VALUES (?, ?, ?)`,
		entity, content, source,
	)
	return err
}

// QueryVerbatim returns original user utterances about an entity.
func (kg *KnowledgeGraph) QueryVerbatim(entity string, limit int) ([]string, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	entity = kg.resolveLocked(entity)
	if limit <= 0 {
		limit = 20
	}
	rows, err := kg.db.Query(
		`SELECT content FROM verbatim_memories WHERE entity = ? ORDER BY created_at DESC LIMIT ?`,
		entity, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		results = append(results, s)
	}
	return results, nil
}

// SearchVerbatim performs TF-IDF weighted search over stored utterances.
// Returns ranked results with relevance scores.
func (kg *KnowledgeGraph) SearchVerbatim(query string, limit int) ([]VerbatimResult, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	rows, err := kg.db.Query(`SELECT id, entity, content, source, created_at FROM verbatim_memories ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect all documents
	type doc struct {
		id      int64
		entity  string
		content string
		source  string
		created string
	}
	var docs []doc
	for rows.Next() {
		var d doc
		rows.Scan(&d.id, &d.entity, &d.content, &d.source, &d.created)
		docs = append(docs, d)
	}

	// TF-IDF scoring
	queryTerms := strings.Fields(strings.ToLower(query))
	if len(queryTerms) == 0 {
		return nil, nil
	}

	// Compute IDF for each term
	termDocCount := make(map[string]int)
	for _, d := range docs {
		seen := make(map[string]bool)
		for _, term := range queryTerms {
			if !seen[term] && strings.Contains(strings.ToLower(d.content), term) {
				termDocCount[term]++
				seen[term] = true
			}
		}
	}

	totalDocs := float64(len(docs))
	var results []VerbatimResult
	for _, d := range docs {
		score := 0.0
		contentLower := strings.ToLower(d.content)
		for _, term := range queryTerms {
			// TF: term frequency in this document
			tf := float64(strings.Count(contentLower, term))
			if tf == 0 {
				continue
			}
			// IDF: inverse document frequency
			idf := 0.0
			if count, ok := termDocCount[term]; ok && count > 0 {
				idf = math.Log(1 + totalDocs/float64(count))
			}
			score += tf * idf
		}
		if score > 0 {
			results = append(results, VerbatimResult{
				ID:        d.id,
				Entity:    d.entity,
				Content:   d.content,
				Source:    d.source,
				CreatedAt: d.created,
				Score:     score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// VerbatimResult is a ranked verbatim memory search result.
type VerbatimResult struct {
	ID        int64   `json:"id"`
	Entity    string  `json:"entity"`
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	CreatedAt string  `json:"created_at"`
	Score     float64 `json:"score"`
}

// RecentFacts returns the most recently added facts.
func (kg *KnowledgeGraph) RecentFacts(limit int) ([]Triple, error) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	rows, err := kg.db.Query(
		`SELECT id, subject, predicate, object, source, valid_from, valid_to, confidence, created_at
		 FROM triples ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriples(rows)
}

// MergeEntities merges alias into canonical, updating all triples and verbatim memories.
func (kg *KnowledgeGraph) MergeEntities(canonical, alias string) error {
	kg.mu.Lock()
	defer kg.mu.Unlock()

	// Register the alias
	kg.db.Exec(`INSERT OR IGNORE INTO entity_aliases (canonical, alias) VALUES (?, ?)`, canonical, alias)

	// Update all triples referencing alias → canonical
	kg.db.Exec(`UPDATE triples SET subject = ? WHERE subject = ?`, canonical, alias)
	kg.db.Exec(`UPDATE triples SET object = ? WHERE object = ?`, canonical, alias)

	// Update verbatim memories
	kg.db.Exec(`UPDATE verbatim_memories SET entity = ? WHERE entity = ?`, canonical, alias)

	return nil
}

// DurableEntityNames returns entities from global/project facts only. Session
// conversation facts are deliberately excluded from background deduplication.
func (kg *KnowledgeGraph) DurableEntityNames() ([]string, error) {
	if kg == nil || kg.db == nil {
		return nil, nil
	}
	rows, err := kg.db.Query(
		"SELECT DISTINCT subject FROM triples " +
			"WHERE source <> 'conversation' AND source NOT LIKE 'conversation:session:%' " +
			"UNION SELECT DISTINCT object FROM triples " +
			"WHERE source <> 'conversation' AND source NOT LIKE 'conversation:session:%'",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entities := make([]string, 0)
	for rows.Next() {
		var entity string
		if err := rows.Scan(&entity); err != nil {
			return nil, err
		}
		entity = strings.TrimSpace(entity)
		if entity != "" && !strings.HasPrefix(entity, "http") && len(entity) > 1 {
			entities = append(entities, entity)
		}
	}
	return entities, rows.Err()
}

// BuildContextPrompt returns a compact summary of the knowledge graph for system prompt injection.
// Inspired by MemPalace's AAAK compression — compact enough for LLM to scan quickly.
func (kg *KnowledgeGraph) BuildContextPrompt(maxItems int) string {
	return kg.BuildContextPromptForSession("", maxItems)
}

func ConversationSessionSource(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "conversation:session:" + sessionID
}

func KnowledgeFactVisibleToSession(f Triple, sessionID string) bool {
	// Legacy conversation facts had no audience metadata. Preserve them in the
	// database, but quarantine them from automatic prompts instead of leaking
	// temporary facts into every future conversation.
	if f.Source == "conversation" {
		return false
	}
	if strings.HasPrefix(f.Source, "conversation:session:") {
		expected := ConversationSessionSource(sessionID)
		return expected != "" && f.Source == expected
	}
	return true
}

// BuildContextPromptForSession injects global/project knowledge and facts from
// the active conversation only. It intentionally does not surface legacy
// unscoped conversation rows.
func (kg *KnowledgeGraph) BuildContextPromptForSession(sessionID string, maxItems int) string {
	candidateLimit := maxItems
	if candidateLimit > 0 {
		candidateLimit *= 10
	}
	facts, err := kg.AllFacts(candidateLimit)
	if err != nil || len(facts) == 0 {
		return ""
	}

	// Group by entity
	entities := make(map[string][]Triple)
	visibleCount := 0
	for _, f := range facts {
		if shouldSuppressKnowledgeFact(f) || !KnowledgeFactVisibleToSession(f, sessionID) {
			continue
		}
		entities[f.Subject] = append(entities[f.Subject], f)
		visibleCount++
		if maxItems > 0 && visibleCount >= maxItems {
			break
		}
	}
	if len(entities) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Knowledge Graph\n")
	sb.WriteString("Known facts (entity → relationship → value):\n")
	for entity, triples := range entities {
		sb.WriteString(fmt.Sprintf("### %s (%s)\n", entity, entityType(entity)))
		for _, t := range triples {
			timeInfo := ""
			if t.ValidFrom != "" {
				timeInfo = fmt.Sprintf(" (since %s", t.ValidFrom)
				if t.ValidTo != "" {
					timeInfo += fmt.Sprintf(" to %s", t.ValidTo)
				}
				timeInfo += ")"
			}
			sb.WriteString(fmt.Sprintf("- %s → %s%s\n", t.Predicate, t.Object, timeInfo))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func shouldSuppressKnowledgeFact(t Triple) bool {
	text := strings.ToLower(strings.TrimSpace(t.Subject + "\n" + t.Predicate + "\n" + t.Object))
	if text == "" {
		return true
	}
	for _, marker := range []string{
		"verification_",
		"you are a sub-agent named",
		"sub-agent",
		"report_progress",
		"auto verification",
		"tool/skill review",
		"guardrail circuit breaker",
		"openkitty-a",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	for _, field := range []string{t.Subject, t.Predicate, t.Object} {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(field)), "sk-") {
			return true
		}
	}
	return false
}

type WeightedTriple = WeightedFact

func MemoryDecay(createdAt string, halfLifeDays float64) float64 {
	return Decay(createdAt, halfLifeDays)
}

// QueryWeighted adapts persisted graph facts to the memory domain's decay
// and predicate-specific weighting policy.
func (kg *KnowledgeGraph) QueryWeighted(entity, asOf string, limit int) ([]WeightedTriple, error) {
	if kg == nil {
		return nil, nil
	}
	triples, err := kg.QueryEntity(entity, asOf)
	if err != nil {
		return nil, err
	}
	return WeightFacts(triples, limit, time.Now()), nil
}

func (kg *KnowledgeGraph) ProactiveMemory() string {
	return kg.ProactiveMemoryForSession("")
}

func (kg *KnowledgeGraph) ProactiveMemoryForSession(sessionID string) string {
	if kg == nil {
		return ""
	}
	facts, err := kg.AllFacts(500)
	if err != nil {
		return ""
	}
	return BuildProactiveMemory(
		facts,
		time.Now(),
		func(fact Fact) bool {
			return KnowledgeFactVisibleToSession(fact, sessionID)
		},
		func(fact Fact) bool {
			return shouldSuppressKnowledgeFact(fact)
		},
	)
}

func (kg *KnowledgeGraph) Close() error {
	return kg.db.Close()
}

func (kg *KnowledgeGraph) resolveLocked(name string) string {
	var canonical string
	err := kg.db.QueryRow(`SELECT canonical FROM entity_aliases WHERE alias = ?`, name).Scan(&canonical)
	if err == nil && canonical != "" {
		return canonical
	}
	return name
}

func scanTriples(rows *sql.Rows) ([]Triple, error) {
	var results []Triple
	for rows.Next() {
		var t Triple
		if err := rows.Scan(&t.ID, &t.Subject, &t.Predicate, &t.Object, &t.Source, &t.ValidFrom, &t.ValidTo, &t.Confidence, &t.CreatedAt); err != nil {
			return results, err
		}
		results = append(results, t)
	}
	return results, nil
}
