package operations

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pipelinecore "mac/internal/pipeline"

	_ "gorm.io/driver/postgres"
	_ "gorm.io/driver/sqlserver"
)

func truncate(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

// ====================== DB Operator Config ======================

type DBOperatorConfig struct {
	Enabled             bool     `json:"enabled" yaml:"enabled"`
	Driver              string   `json:"driver" yaml:"driver"` // sqlite, mysql, postgres, oracle
	DSN                 string   `json:"dsn" yaml:"dsn"`       // connection string or file path
	MaxRows             int      `json:"max_rows" yaml:"max_rows"`
	Permission          string   `json:"permission" yaml:"permission"` // readonly, readwrite_approve, readwrite, admin
	AllowedTables       []string `json:"allowed_tables,omitempty" yaml:"allowed_tables,omitempty"`
	DeniedColumns       []string `json:"denied_columns,omitempty" yaml:"denied_columns,omitempty"`
	QueryTimeoutSeconds int      `json:"query_timeout_seconds,omitempty" yaml:"query_timeout_seconds,omitempty"`
	OutputDir           string   `json:"-" yaml:"-"`
	// Oracle-specific (requires Oracle Instant Client):
	OracleService string `json:"oracle_service,omitempty" yaml:"oracle_service,omitempty"`
	OracleLibDir  string `json:"oracle_lib_dir,omitempty" yaml:"oracle_lib_dir,omitempty"`
}

func resolvePipelineDatabaseConnection(c pipelinecore.PipelineDatabaseConnection) (DBOperatorConfig, error) {
	if !c.Enabled {
		return DBOperatorConfig{}, fmt.Errorf("pipeline database connection %q is disabled", c.ID)
	}
	dsn := strings.TrimSpace(c.DSN)
	if envName := strings.TrimSpace(c.DSNEnv); envName != "" {
		dsn = strings.TrimSpace(os.Getenv(envName))
		if dsn == "" {
			return DBOperatorConfig{}, fmt.Errorf("pipeline database connection %q requires environment variable %s", c.ID, envName)
		}
	}
	if dsn == "" {
		return DBOperatorConfig{}, fmt.Errorf("pipeline database connection %q has no dsn or dsn_env", c.ID)
	}
	permission := strings.TrimSpace(c.Permission)
	if permission == "" {
		permission = "readonly"
	}
	maxRows := c.MaxRows
	if maxRows <= 0 {
		maxRows = 500
	}
	timeout := c.TimeoutSeconds
	if timeout <= 0 {
		timeout = 20
	}
	return DBOperatorConfig{
		Enabled:             true,
		Driver:              strings.TrimSpace(c.Driver),
		DSN:                 dsn,
		MaxRows:             maxRows,
		Permission:          permission,
		AllowedTables:       append([]string(nil), c.AllowedTables...),
		DeniedColumns:       append([]string(nil), c.DeniedColumns...),
		QueryTimeoutSeconds: timeout,
	}, nil
}

func (c *DBOperatorConfig) canRead() bool { return c.Permission != "" }
func (c *DBOperatorConfig) canWrite() bool {
	return c.Permission == "readwrite" || c.Permission == "readwrite_approve" || c.Permission == "admin"
}
func (c *DBOperatorConfig) canDDL() bool { return c.Permission == "admin" }
func (c *DBOperatorConfig) needsApproval() bool {
	return c.Permission == "readwrite_approve"
}

// ====================== DB Operator Tool ======================

type DBOperatorTool struct {
	config DBOperatorConfig
	db     *sql.DB
	logger *log.Logger

	// Pending write operations awaiting approval
	pendingWrites map[string]*pendingWrite
	mu            sync.Mutex
}

type pendingWrite struct {
	ID        string
	SQL       string
	Args      []interface{}
	CreatedAt time.Time
}

func NewDBOperatorTool(cfg DBOperatorConfig, logger *log.Logger) (*DBOperatorTool, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("db_operator is disabled")
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = 100
	}
	if cfg.QueryTimeoutSeconds <= 0 {
		cfg.QueryTimeoutSeconds = 20
	}
	if strings.TrimSpace(cfg.Permission) == "" {
		cfg.Permission = "readonly"
	}
	if logger == nil {
		logger = log.Default()
	}

	driver, err := databaseSQLDriver(cfg.Driver)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}

	logger.Printf("[DBOperator] Connected: driver=%s permission=%s max_rows=%d", cfg.Driver, cfg.Permission, cfg.MaxRows)
	return &DBOperatorTool{
		config:        cfg,
		db:            db,
		logger:        logger,
		pendingWrites: make(map[string]*pendingWrite),
	}, nil
}

func databaseSQLDriver(driver string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		return "mysql", nil
	case "sqlite", "sqlite3":
		return "sqlite", nil
	case "postgres", "postgresql", "pgx":
		return "pgx", nil
	case "sqlserver", "mssql":
		return "sqlserver", nil
	default:
		return "", fmt.Errorf("unsupported database driver %q (supported: mysql, sqlite, postgres, sqlserver)", driver)
	}
}

func (t *DBOperatorTool) Name() string { return "db_query" }

func (t *DBOperatorTool) Description() string {
	return `Execute SQL queries on the configured database.
- SELECT/read queries run immediately and return results as a table.
- INSERT/UPDATE/DELETE require confirmation — the tool returns a preview and waits for approval.
- Use this to answer data questions, generate reports, or modify records.
- Always write clean, parameterized SQL. Never use string concatenation for values.`
}

func (t *DBOperatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sql": map[string]interface{}{
				"type":        "string",
				"description": "The SQL statement to execute. Use ? placeholders for values.",
			},
			"args": map[string]interface{}{
				"type":        "array",
				"description": "Optional parameter values for ? placeholders",
				"items":       map[string]interface{}{"type": "string"},
			},
			"connection_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional. Connection ID from a previous db_connect call. If omitted, uses the default configured connection.",
			},
			"save_as": map[string]interface{}{
				"type":        "string",
				"description": "Optional CSV filename. Pipeline database tools save it under the execution storage/data directory.",
			},
		},
		"required": []string{"sql"},
	}
}

func (t *DBOperatorTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	sqlStr, _ := params["sql"].(string)
	if sqlStr == "" {
		return "", fmt.Errorf("sql is required")
	}

	var args []interface{}
	if rawArgs, ok := params["args"].([]interface{}); ok {
		args = rawArgs
	}
	saveAs, _ := params["save_as"].(string)

	sqlStr = strings.TrimSpace(sqlStr)
	class, err := classifyDatabaseSQL(sqlStr)
	if err != nil {
		return "", err
	}
	if err := enforceAllowedTables(sqlStr, t.config.AllowedTables); err != nil {
		return "", err
	}
	if err := enforceDeniedColumns(sqlStr, t.config.DeniedColumns); err != nil {
		return "", err
	}
	isWrite := class != databaseSQLRead

	// Apply permission controls
	if !t.config.canRead() {
		return "", fmt.Errorf("database access disabled")
	}
	if isWrite && !t.config.canWrite() {
		return "", fmt.Errorf("write operations not allowed (permission=%s). Set to readwrite or admin.", t.config.Permission)
	}
	if class == databaseSQLDDL && !t.config.canDDL() {
		return "", fmt.Errorf("DDL operations require admin permission (current=%s)", t.config.Permission)
	}

	if isWrite {
		return t.handleWrite(ctx, sqlStr, args)
	}
	return t.handleRead(ctx, sqlStr, args, saveAs)
}

func (t *DBOperatorTool) handleRead(ctx context.Context, sqlStr string, args []interface{}, saveAs string) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(t.config.QueryTimeoutSeconds)*time.Second)
	defer cancel()
	rows, err := t.db.QueryContext(queryCtx, sqlStr, args...)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var result strings.Builder
	result.WriteString(fmt.Sprintf("## Query Result (%d columns)\n\n", len(cols)))

	// Header
	result.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	result.WriteString("|" + strings.Repeat("---|", len(cols)) + "\n")

	count := 0
	var csvRows [][]string
	if strings.TrimSpace(saveAs) != "" {
		csvRows = append(csvRows, append([]string(nil), cols...))
	}
	vals := make([]interface{}, len(cols))
	valPtrs := make([]interface{}, len(cols))
	for i := range vals {
		valPtrs[i] = &vals[i]
	}

	deniedColumns := normalizedNameSet(t.config.DeniedColumns)
	for rows.Next() {
		if count >= t.config.MaxRows {
			result.WriteString(fmt.Sprintf("\n*...truncated at %d rows (max_rows=%d)*\n", t.config.MaxRows, t.config.MaxRows))
			break
		}
		rows.Scan(valPtrs...)
		row := make([]string, len(cols))
		for i, v := range vals {
			if deniedColumns[strings.ToLower(strings.TrimSpace(cols[i]))] {
				row[i] = "[REDACTED]"
				continue
			}
			switch val := v.(type) {
			case nil:
				row[i] = "NULL"
			case []byte:
				s := string(val)
				if len(s) > 100 {
					s = s[:100] + "..."
				}
				row[i] = s
			default:
				s := fmt.Sprintf("%v", val)
				if len(s) > 100 {
					s = s[:100] + "..."
				}
				row[i] = s
			}
		}
		for i := range row {
			row[i] = strings.ReplaceAll(strings.ReplaceAll(row[i], "|", "\\|"), "\n", " ")
		}
		if csvRows != nil {
			csvRow := make([]string, len(row))
			for i := range row {
				csvRow[i] = strings.ReplaceAll(row[i], "\\|", "|")
			}
			csvRows = append(csvRows, csvRow)
		}
		result.WriteString("| " + strings.Join(row, " | ") + " |\n")
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("query rows failed: %w", err)
	}

	result.WriteString(fmt.Sprintf("\n*%d row(s) returned*", count))
	if csvRows != nil {
		path, err := t.writeCSVResult(saveAs, csvRows)
		if err != nil {
			return "", err
		}
		result.WriteString(fmt.Sprintf("\n\nCSV snapshot: `%s`", path))
	}
	t.logger.Printf("[DBOperator] READ: %s → %d rows", truncate(sqlStr, 100), count)
	return result.String(), nil
}

func (t *DBOperatorTool) writeCSVResult(saveAs string, rows [][]string) (string, error) {
	if strings.TrimSpace(t.config.OutputDir) == "" {
		return "", fmt.Errorf("CSV snapshots are not enabled for this database connection")
	}
	name := strings.TrimSpace(saveAs)
	if filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("save_as must be a filename without directories")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".csv") {
		name += ".csv"
	}
	if err := os.MkdirAll(t.config.OutputDir, 0o755); err != nil {
		return "", fmt.Errorf("create database snapshot directory: %w", err)
	}
	path := filepath.Join(t.config.OutputDir, name)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create database CSV snapshot: %w", err)
	}
	writer := csv.NewWriter(file)
	writer.WriteAll(rows)
	writer.Flush()
	writeErr := writer.Error()
	closeErr := file.Close()
	if writeErr != nil {
		return "", fmt.Errorf("write database CSV snapshot: %w", writeErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close database CSV snapshot: %w", closeErr)
	}
	return path, nil
}

func (t *DBOperatorTool) handleWrite(ctx context.Context, sqlStr string, args []interface{}) (string, error) {
	// Preview what will happen
	var preview strings.Builder
	preview.WriteString("## Write Operation Preview\n\n")
	preview.WriteString(fmt.Sprintf("```sql\n%s\n```\n\n", sqlStr))
	preview.WriteString("This is a write operation. ")

	if t.config.needsApproval() {
		// Queue for approval
		id := fmt.Sprintf("dbw_%d", time.Now().UnixNano())
		t.mu.Lock()
		t.pendingWrites[id] = &pendingWrite{
			ID:        id,
			SQL:       sqlStr,
			Args:      args,
			CreatedAt: time.Now(),
		}
		t.mu.Unlock()
		preview.WriteString(fmt.Sprintf("**Awaiting approval.** Reply 'approve %s' to execute, or 'reject %s' to cancel.", id, id))
		return preview.String(), nil
	}

	// Auto-execute
	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(t.config.QueryTimeoutSeconds)*time.Second)
	defer cancel()
	result, err := t.db.ExecContext(queryCtx, sqlStr, args...)
	if err != nil {
		return "", fmt.Errorf("write failed: %w", err)
	}
	affected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()

	t.logger.Printf("[DBOperator] WRITE: %s → %d rows affected, last_id=%d", truncate(sqlStr, 100), affected, lastID)
	return fmt.Sprintf("Executed. %d row(s) affected. Last insert ID: %d", affected, lastID), nil
}

// ApproveWrite executes a pending write operation.
func (t *DBOperatorTool) ApproveWrite(id string) (string, error) {
	t.mu.Lock()
	pw, ok := t.pendingWrites[id]
	if !ok {
		t.mu.Unlock()
		return "", fmt.Errorf("pending write '%s' not found", id)
	}
	delete(t.pendingWrites, id)
	t.mu.Unlock()

	result, err := t.db.Exec(pw.SQL, pw.Args...)
	if err != nil {
		return "", fmt.Errorf("execute failed: %w", err)
	}
	affected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	return fmt.Sprintf("Approved and executed. %d row(s) affected. Last insert ID: %d", affected, lastID), nil
}

// RejectWrite cancels a pending write.
func (t *DBOperatorTool) RejectWrite(id string) string {
	t.mu.Lock()
	delete(t.pendingWrites, id)
	t.mu.Unlock()
	return fmt.Sprintf("Write '%s' rejected and discarded.", id)
}

// PendingWrites returns all pending write operations.
func (t *DBOperatorTool) PendingWrites() []pendingWrite {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]pendingWrite, 0, len(t.pendingWrites))
	for _, pw := range t.pendingWrites {
		out = append(out, *pw)
	}
	return out
}

func (t *DBOperatorTool) Close() error {
	return t.db.Close()
}

type databaseSQLClass int

const (
	databaseSQLRead databaseSQLClass = iota
	databaseSQLWrite
	databaseSQLDDL
)

var databaseTableReferencePattern = regexp.MustCompile(`(?i)\b(?:from|join|update|into|describe|desc)\s+([a-zA-Z0-9_.$\x60"\[\]]+)`)
var databaseCTEPattern = regexp.MustCompile(`(?i)(?:\bwith|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s+as\s*\(`)

func classifyDatabaseSQL(statement string) (databaseSQLClass, error) {
	trimmed := stripLeadingSQLComments(statement)
	if trimmed == "" {
		return databaseSQLRead, fmt.Errorf("sql is required")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return databaseSQLRead, fmt.Errorf("sql contains a NUL byte")
	}
	withoutFinalSemicolon := strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))
	if strings.Contains(withoutFinalSemicolon, ";") {
		return databaseSQLRead, fmt.Errorf("multiple SQL statements are not allowed")
	}
	upper := strings.ToUpper(withoutFinalSemicolon)
	normalizedUpper := " " + strings.Join(strings.Fields(upper), " ") + " "
	for _, forbidden := range []string{
		" INTO OUTFILE", " INTO DUMPFILE", "LOAD_FILE(", "SLEEP(", "BENCHMARK(",
		" ATTACH ", " DETACH ", " VACUUM", " PRAGMA ", " FOR UPDATE", " LOCK IN SHARE MODE",
	} {
		if strings.Contains(normalizedUpper, forbidden) {
			return databaseSQLRead, fmt.Errorf("SQL operation %q is not allowed", strings.TrimSpace(forbidden))
		}
	}
	first := upper
	if idx := strings.IndexAny(first, " \t\r\n("); idx >= 0 {
		first = first[:idx]
	}
	switch first {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN":
		return databaseSQLRead, nil
	case "WITH":
		for _, keyword := range []string{" INSERT ", " UPDATE ", " DELETE ", " REPLACE ", " MERGE "} {
			if strings.Contains(normalizedUpper, keyword) {
				return databaseSQLWrite, nil
			}
		}
		return databaseSQLRead, nil
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE":
		return databaseSQLWrite, nil
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "GRANT", "REVOKE":
		return databaseSQLDDL, nil
	default:
		return databaseSQLRead, fmt.Errorf("unsupported SQL statement type %q", first)
	}
}

func stripLeadingSQLComments(statement string) string {
	remaining := strings.TrimSpace(statement)
	for {
		switch {
		case strings.HasPrefix(remaining, "--"):
			if idx := strings.IndexByte(remaining, '\n'); idx >= 0 {
				remaining = strings.TrimSpace(remaining[idx+1:])
				continue
			}
			return ""
		case strings.HasPrefix(remaining, "/*"):
			if idx := strings.Index(remaining[2:], "*/"); idx >= 0 {
				remaining = strings.TrimSpace(remaining[idx+4:])
				continue
			}
			return remaining
		default:
			return remaining
		}
	}
}

func enforceAllowedTables(statement string, allowed []string) error {
	allowedSet := normalizedNameSet(allowed)
	if len(allowedSet) == 0 {
		return nil
	}
	cteNames := make(map[string]bool)
	for _, match := range databaseCTEPattern.FindAllStringSubmatch(statement, -1) {
		if len(match) > 1 {
			cteNames[normalizeDatabaseObjectName(match[1])] = true
		}
	}
	var denied []string
	for _, match := range databaseTableReferencePattern.FindAllStringSubmatch(statement, -1) {
		if len(match) < 2 {
			continue
		}
		name := normalizeDatabaseObjectName(match[1])
		shortName := name
		if idx := strings.LastIndex(shortName, "."); idx >= 0 {
			shortName = shortName[idx+1:]
		}
		if !allowedSet[name] && !allowedSet[shortName] && !cteNames[name] && !cteNames[shortName] {
			denied = append(denied, name)
		}
	}
	if len(denied) == 0 {
		return nil
	}
	sort.Strings(denied)
	return fmt.Errorf("query references table(s) outside the pipeline allowlist: %s", strings.Join(denied, ", "))
}

func enforceDeniedColumns(statement string, denied []string) error {
	lower := strings.ToLower(statement)
	for _, column := range denied {
		column = normalizeDatabaseObjectName(column)
		if column == "" {
			continue
		}
		pattern := regexp.MustCompile(`(?i)(^|[^a-zA-Z0-9_])` + regexp.QuoteMeta(column) + `([^a-zA-Z0-9_]|$)`)
		if pattern.MatchString(lower) {
			return fmt.Errorf("query explicitly references denied column %q", column)
		}
	}
	return nil
}

func normalizedNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if normalized := normalizeDatabaseObjectName(name); normalized != "" {
			set[normalized] = true
		}
	}
	return set
}

func normalizeDatabaseObjectName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.NewReplacer("`", "", "\"", "", "[", "", "]", "").Replace(name)
	return strings.ToLower(name)
}

// PipelineDBQueryTool lazily opens one Pipeline-owned connection. Lazy open
// keeps disabled or unconfigured built-in profiles from affecting non-DB tasks.
type PipelineDBQueryTool struct {
	profile   pipelinecore.PipelineDatabaseConnection
	logger    *log.Logger
	outputDir string
	mu        sync.Mutex
	op        *DBOperatorTool
	openErr   error
}

func NewPipelineDBQueryTool(profile pipelinecore.PipelineDatabaseConnection, logger *log.Logger, outputDir string) *PipelineDBQueryTool {
	return &PipelineDBQueryTool{profile: profile, logger: logger, outputDir: outputDir}
}

func (t *PipelineDBQueryTool) Name() string { return "db_query" }

func (t *PipelineDBQueryTool) Description() string {
	permission := strings.TrimSpace(t.profile.Permission)
	if permission == "" {
		permission = "readonly"
	}
	return fmt.Sprintf("Execute SQL through Pipeline database connection %q (permission=%s). Only the tables authorized by this Pipeline may be queried.", t.profile.ID, permission)
}

func (t *PipelineDBQueryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sql": map[string]interface{}{
				"type": "string", "description": "One read-only SQL statement scoped to the Pipeline connection allowlist.",
			},
			"args": map[string]interface{}{
				"type": "array", "description": "Optional parameter values for ? placeholders", "items": map[string]interface{}{"type": "string"},
			},
			"save_as": map[string]interface{}{
				"type": "string", "description": "Optional CSV filename saved under .openkitty/storage/data for downstream analysis.",
			},
		},
		"required": []string{"sql"},
	}
}

func (t *PipelineDBQueryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	op, err := t.operator()
	if err != nil {
		return "", err
	}
	return op.Execute(ctx, params)
}

func (t *PipelineDBQueryTool) operator() (*DBOperatorTool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.op != nil || t.openErr != nil {
		return t.op, t.openErr
	}
	cfg, err := resolvePipelineDatabaseConnection(t.profile)
	if err != nil {
		t.openErr = err
		return nil, err
	}
	cfg.OutputDir = t.outputDir
	op, err := NewDBOperatorTool(cfg, t.logger)
	if err != nil {
		t.openErr = fmt.Errorf("pipeline database connection %q failed: %w", t.profile.ID, err)
		return nil, t.openErr
	}
	t.op = op
	return t.op, nil
}

func (t *PipelineDBQueryTool) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.op == nil {
		return nil
	}
	err := t.op.Close()
	t.op = nil
	return err
}

// ====================== DB Connect Tool (ad-hoc connections) ======================

// DBConnectTool lets the AI connect to databases from conversation-provided credentials.
type DBConnectTool struct {
	logger    *log.Logger
	instances map[string]*DBOperatorTool // session-scoped connections
	mu        sync.Mutex
}

func NewDBConnectTool(logger *log.Logger) *DBConnectTool {
	return &DBConnectTool{
		logger:    logger,
		instances: make(map[string]*DBOperatorTool),
	}
}

func (t *DBConnectTool) Name() string { return "db_connect" }
func (t *DBConnectTool) Description() string {
	return `Connect to a database. The user may describe the connection in natural language — YOU must parse it into driver+dsn.
SQLite: driver="sqlite" dsn="/path/to/file.db"
MySQL: driver="mysql" dsn="user:password@tcp(host:port)/dbname"
PostgreSQL: driver="postgres" dsn="postgres://user:pass@host:port/dbname?sslmode=disable"
Examples of user input and how YOU should parse:
- "连一下10.0.1.5的sales库 root 123456" → driver=mysql dsn=root:123456@tcp(10.0.1.5:3306)/sales
- "用本地的data.db" → driver=sqlite dsn=./data.db
- "连接pg://admin:pass@db.example.com:5432/prod" → driver=postgres dsn=postgres://admin:pass@db.example.com:5432/prod?sslmode=disable
Always use default port if not specified: MySQL=3306, PostgreSQL=5432.`
}
func (t *DBConnectTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"driver": map[string]interface{}{
				"type": "string", "description": "Database driver: sqlite, mysql, postgres",
				"enum": []string{"sqlite", "mysql", "postgres"},
			},
			"dsn": map[string]interface{}{
				"type": "string", "description": "Connection string or file path",
			},
		},
		"required": []string{"driver", "dsn"},
	}
}
func (t *DBConnectTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	driver, _ := params["driver"].(string)
	dsn, _ := params["dsn"].(string)
	if driver == "" || dsn == "" {
		return "", fmt.Errorf("driver and dsn are required")
	}

	cfg := DBOperatorConfig{Enabled: true, Driver: driver, DSN: dsn, MaxRows: 100, Permission: "readwrite_approve"}
	op, err := NewDBOperatorTool(cfg, t.logger)
	if err != nil {
		return "", fmt.Errorf("connect failed: %w", err)
	}

	id := fmt.Sprintf("adhoc_%s_%d", driver, time.Now().UnixNano())
	t.mu.Lock()
	t.instances[id] = op
	t.mu.Unlock()

	t.logger.Printf("[DBConnect] Connected: driver=%s id=%s", driver, id)
	return fmt.Sprintf("Connected to %s database.\nConnection ID: %s\nUse in subsequent db_query calls.", driver, id), nil
}

// GetAdhoc returns an ad-hoc connected instance, or nil.
func (t *DBConnectTool) GetAdhoc(id string) *DBOperatorTool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.instances[id]
}
