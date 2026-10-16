package operations

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// DBTool provides database schema analysis, query generation,
// and migration management.
type DBTool struct {
	workspace string
}

func NewDBTool(workspace string) *DBTool {
	return &DBTool{workspace: workspace}
}

func (t *DBTool) Name() string { return "db" }

func (t *DBTool) Description() string {
	return "Database schema analysis and query tools. " +
		"Actions: schema (analyze table structure), " +
		"query (generate SQL queries from natural description), " +
		"migrate (track and manage schema migrations), " +
		"connect_info (show available database configurations). " +
		"Supports MySQL, PostgreSQL, SQLite."
}

func (t *DBTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"schema", "query", "migrate", "connect_info"},
				"description": "schema: analyze tables. query: generate SQL. migrate: manage migrations. connect_info: show DB config.",
			},
			"dsn": map[string]interface{}{
				"type":        "string",
				"description": "Database connection string (e.g., 'mysql://user:pass@localhost/db')",
			},
			"table": map[string]interface{}{
				"type":        "string",
				"description": "Table name for schema analysis",
			},
			"sql_dir": map[string]interface{}{
				"type":        "string",
				"description": "Directory for SQL migration files (default: storage/migrations)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *DBTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "schema")
	dsn := configvalue.String(params, "dsn", "")
	table := configvalue.String(params, "table", "")
	sqlDir := configvalue.String(params, "sql_dir", "")

	switch action {
	case "schema":
		return t.analyzeSchema(ctx, dsn, table)
	case "query":
		return t.generateQuery(table)
	case "migrate":
		return t.manageMigrations(sqlDir)
	case "connect_info":
		return t.showConnectInfo(), nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Schema Analysis ==================

func (t *DBTool) analyzeSchema(ctx context.Context, dsn, tableName string) (string, error) {
	if dsn == "" {
		return t.analyzeSchemaFromGoFiles(ctx, tableName)
	}

	var sb strings.Builder
	sb.WriteString("## Database Schema\n\n")

	// Try to connect and fetch schema via appropriate CLI
	dbType := detectDBType(dsn)
	sb.WriteString(fmt.Sprintf("**Database:** %s | **DSN:** %s\n\n", dbType, maskDSN(dsn)))

	switch dbType {
	case "mysql":
		sb.WriteString(t.mysqlSchema(ctx, dsn, tableName))
	case "postgres":
		sb.WriteString(t.postgresSchema(ctx, dsn, tableName))
	case "sqlite":
		sb.WriteString(t.sqliteSchema(ctx, dsn, tableName))
	default:
		sb.WriteString("Unrecognized database type. Use mysql://, postgres://, or sqlite:// DSN format.\n")
	}

	return sb.String(), nil
}

func (t *DBTool) mysqlSchema(ctx context.Context, dsn, table string) string {
	// Use mysql CLI if available
	parts := parseDSN(dsn)
	if parts == nil {
		return "Invalid MySQL DSN format. Expected: mysql://user:pass@host:port/dbname\n"
	}

	args := []string{"-h", parts["host"], "-u", parts["user"], "-D", parts["dbname"]}
	if pass, ok := parts["pass"]; ok && pass != "" {
		args = append(args, "-p"+pass)
	}
	if table != "" {
		args = append(args, "-e", fmt.Sprintf("DESCRIBE %s; SHOW INDEX FROM %s;", quoteIdent(table), quoteIdent(table)))
	} else {
		args = append(args, "-e", "SHOW TABLES;")
	}

	out, _ := runDBCommand(ctx, "mysql", args...)
	return fmt.Sprintf("```\n%s\n```\n", out)
}

func (t *DBTool) postgresSchema(ctx context.Context, dsn, table string) string {
	parts := parseDSN(dsn)
	if parts == nil {
		return "Invalid Postgres DSN format.\n"
	}

	conn := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s",
		parts["user"], parts["pass"], parts["host"],
		getOrDefault(parts, "port", "5432"), parts["dbname"])

	if table != "" {
		out, _ := runDBCommand(ctx, "psql", conn, "-c",
			fmt.Sprintf("\\d %s", quoteIdent(table)))
		return fmt.Sprintf("```\n%s\n```\n", out)
	}
	out, _ := runDBCommand(ctx, "psql", conn, "-c", "\\dt")
	return fmt.Sprintf("```\n%s\n```\n", out)
}

func (t *DBTool) sqliteSchema(ctx context.Context, dsn, table string) string {
	dbPath := strings.TrimPrefix(dsn, "sqlite://")
	if table != "" {
		out, _ := runDBCommand(ctx, "sqlite3", dbPath,
			fmt.Sprintf(".schema %s", quoteIdent(table)))
		return fmt.Sprintf("```\n%s\n```\n", out)
	}
	out, _ := runDBCommand(ctx, "sqlite3", dbPath, ".tables")
	return fmt.Sprintf("```\n%s\n```\n", out)
}

// ================== Schema from Go structs ==================

func (t *DBTool) analyzeSchemaFromGoFiles(ctx context.Context, tableName string) (string, error) {
	var sb strings.Builder
	sb.WriteString("## Schema Analysis (from Go structs)\n\n")
	sb.WriteString("⚠️ No DSN provided — scanning Go files for struct definitions.\n\n")

	// Walk workspace for Go files and look for struct tags
	type colDef struct {
		Name     string
		Type     string
		Nullable bool
		PK       bool
		Default  string
		Index    bool
	}

	var tables []struct {
		Name    string
		Columns []colDef
	}

	walkErr := filepath.Walk(t.workspace, func(path string, info os.FileInfo, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(src)

		// Simple regex-free struct tag scanning
		lines := strings.Split(content, "\n")
		var currentTable string
		var currentCols []colDef

		for _, line := range lines {
			line = strings.TrimSpace(line)

			// Detect table name from comment
			if strings.Contains(line, "TableName()") && strings.Contains(line, "return") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 2 {
					currentTable = parts[1]
				}
			}

			// Detect struct field with gorm/db tag
			if strings.Contains(line, "`") && (strings.Contains(line, "gorm") || strings.Contains(line, "db:") || strings.Contains(line, "json:")) {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					name := strings.TrimSpace(parts[0])
					ftype := strings.TrimSpace(parts[1])

					col := colDef{Name: toSnake(name), Type: ftype}

					// Parse tags
					tagStart := strings.Index(line, "`")
					tagEnd := strings.LastIndex(line, "`")
					if tagStart >= 0 && tagEnd > tagStart {
						tag := line[tagStart+1 : tagEnd]
						if strings.Contains(tag, "primaryKey") || strings.Contains(tag, "primary_key") {
							col.PK = true
						}
						if strings.Contains(tag, "not null") {
							col.Nullable = false
						} else {
							col.Nullable = true
						}
						if strings.Contains(tag, "index") || strings.Contains(tag, "uniqueIndex") {
							col.Index = true
						}
						if strings.Contains(tag, "default:") {
							col.Default = extractTagValue(tag, "default:")
						}
					}

					currentCols = append(currentCols, col)
				}
			}

			// End of struct
			if line == "}" && currentTable != "" && len(currentCols) > 0 {
				tables = append(tables, struct {
					Name    string
					Columns []colDef
				}{currentTable, currentCols})
				currentTable = ""
				currentCols = nil
			}
		}

		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("scan Go database models: %w", walkErr)
	}

	if len(tables) == 0 {
		sb.WriteString("No database struct definitions found in Go files.\n")
		return sb.String(), nil
	}

	for _, tbl := range tables {
		if tableName != "" && tbl.Name != tableName {
			continue
		}
		sb.WriteString(fmt.Sprintf("### `%s`\n\n", tbl.Name))
		sb.WriteString("| Column | Type | Nullable | PK | Index | Default |\n")
		sb.WriteString("|--------|------|----------|----|-------|--------|\n")
		for _, col := range tbl.Columns {
			pk := ""
			if col.PK {
				pk = "✓"
			}
			nullable := "✓"
			if !col.Nullable {
				nullable = "✗"
			}
			index := ""
			if col.Index {
				index = "✓"
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				col.Name, col.Type, nullable, pk, index, col.Default))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// ================== Query Generation ==================

func (t *DBTool) generateQuery(table string) (string, error) {
	if table == "" {
		return "## Query Generation\n\nProvide a table name to generate CRUD queries.\n", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## CRUD Queries: `%s`\n\n", table))

	safeTable := quoteIdent(table)

	sb.WriteString("### SELECT (with pagination)\n")
	sb.WriteString(fmt.Sprintf("```sql\nSELECT * FROM %s\nWHERE id = ?\nORDER BY created_at DESC\nLIMIT ? OFFSET ?;\n```\n\n", safeTable))

	sb.WriteString("### INSERT\n")
	sb.WriteString(fmt.Sprintf("```sql\nINSERT INTO %s (name, created_at, updated_at)\nVALUES (?, NOW(), NOW());\n```\n\n", safeTable))

	sb.WriteString("### UPDATE\n")
	sb.WriteString(fmt.Sprintf("```sql\nUPDATE %s\nSET name = ?, updated_at = NOW()\nWHERE id = ?;\n```\n\n", safeTable))

	sb.WriteString("### DELETE (soft)\n")
	sb.WriteString(fmt.Sprintf("```sql\nUPDATE %s\nSET deleted_at = NOW()\nWHERE id = ? AND deleted_at IS NULL;\n```\n\n", safeTable))

	sb.WriteString("### Create Index\n")
	sb.WriteString(fmt.Sprintf("```sql\nCREATE INDEX idx_%s_created_at ON %s(created_at);\n```\n\n", toSnake(table), safeTable))

	sb.WriteString("### Count\n")
	sb.WriteString(fmt.Sprintf("```sql\nSELECT COUNT(*) FROM %s WHERE deleted_at IS NULL;\n```\n\n", safeTable))

	return sb.String(), nil
}

// ================== Migrations ==================

func (t *DBTool) manageMigrations(sqlDir string) (string, error) {
	if sqlDir == "" {
		sqlDir = filepath.Join(t.workspace, ".openkitty", "storage", "migrations")
	} else if !filepath.IsAbs(sqlDir) {
		sqlDir = filepath.Join(t.workspace, sqlDir)
	}
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		return "", fmt.Errorf("create migrations directory: %w", err)
	}

	entries, err := os.ReadDir(sqlDir)
	if err != nil {
		return "", fmt.Errorf("read migrations dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Migrations: `%s`\n\n", sqlDir))

	var migrations []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".sql") || strings.HasSuffix(e.Name(), ".up.sql")) {
			migrations = append(migrations, e.Name())
		}
	}

	if len(migrations) == 0 {
		sb.WriteString("No migration files found.\n\n")
		sb.WriteString("### Create New Migration\n\n")
		sb.WriteString(fmt.Sprintf("```bash\n# Create up migration\necho 'ALTER TABLE example ADD COLUMN new_field VARCHAR(255);' > %s/001_add_new_field.up.sql\n```\n",
			sqlDir))
	} else {
		sb.WriteString(fmt.Sprintf("**%d migration(s):**\n\n", len(migrations)))
		for _, m := range migrations {
			sb.WriteString(fmt.Sprintf("- `%s`\n", m))
		}

		// Read the latest migration
		latest := migrations[len(migrations)-1]
		data, _ := os.ReadFile(filepath.Join(sqlDir, latest))
		preview := textutil.Truncate(string(data), 500)
		if preview != "" {
			sb.WriteString(fmt.Sprintf("\n### Latest: `%s`\n\n```sql\n%s\n```\n", latest, preview))
		}
	}

	return sb.String(), nil
}

// ================== Connect Info ==================

func (t *DBTool) showConnectInfo() string {
	var sb strings.Builder
	sb.WriteString("## Database Connection Info\n\n")
	sb.WriteString("Supported DSN formats:\n\n")
	sb.WriteString("- **MySQL:** `mysql://user:password@host:port/dbname`\n")
	sb.WriteString("- **PostgreSQL:** `postgres://user:password@host:port/dbname?sslmode=disable`\n")
	sb.WriteString("- **SQLite:** `sqlite:///path/to/database.db`\n\n")

	// Check for common env vars
	vars := []string{"DATABASE_URL", "MYSQL_DSN", "POSTGRES_DSN", "DB_HOST", "DB_NAME", "DB_USER"}
	sb.WriteString("### Environment Variables\n\n")
	for _, v := range vars {
		if val := os.Getenv(v); val != "" {
			masked := maskValue(val)
			sb.WriteString(fmt.Sprintf("- `%s=%s`\n", v, masked))
		}
	}
	if sb.Len() == len("## Database Connection Info\n\n")+232 {
		sb.WriteString("No database environment variables set.\n")
	}

	return sb.String()
}

// ================== Helpers ==================

func detectDBType(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "mysql://"):
		return "mysql"
	case strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://"):
		return "postgres"
	case strings.HasPrefix(dsn, "sqlite://"):
		return "sqlite"
	default:
		return "unknown"
	}
}

func parseDSN(dsn string) map[string]string {
	// Simple DSN parser: mysql://user:pass@host:port/dbname
	withoutScheme := dsn
	if idx := strings.Index(dsn, "://"); idx >= 0 {
		withoutScheme = dsn[idx+3:]
	}

	result := make(map[string]string)

	// user:pass@host:port/dbname
	atIdx := strings.LastIndex(withoutScheme, "@")
	if atIdx < 0 {
		return nil
	}
	userPass := withoutScheme[:atIdx]
	hostDB := withoutScheme[atIdx+1:]

	if colonIdx := strings.Index(userPass, ":"); colonIdx >= 0 {
		result["user"] = userPass[:colonIdx]
		result["pass"] = userPass[colonIdx+1:]
	} else {
		result["user"] = userPass
	}

	slashIdx := strings.Index(hostDB, "/")
	if slashIdx >= 0 {
		hostPort := hostDB[:slashIdx]
		result["dbname"] = hostDB[slashIdx+1:]

		if colonIdx := strings.LastIndex(hostPort, ":"); colonIdx >= 0 {
			result["host"] = hostPort[:colonIdx]
			result["port"] = hostPort[colonIdx+1:]
		} else {
			result["host"] = hostPort
		}
	} else {
		result["host"] = hostDB
	}

	return result
}

func maskDSN(dsn string) string {
	parts := parseDSN(dsn)
	if parts == nil {
		return "***"
	}
	return fmt.Sprintf("%s://%s:***@%s/%s",
		detectDBType(dsn), parts["user"], parts["host"], parts["dbname"])
}

func maskValue(s string) string {
	if len(s) > 20 {
		return s[:8] + "..." + s[len(s)-4:]
	}
	if len(s) > 6 {
		return s[:3] + "..."
	}
	return "***"
}

func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func toSnake(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(r + 32) // lowercase
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func getOrDefault(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

func extractTagValue(tag, key string) string {
	// Extract value from tag like `gorm:"default:hello"`
	idx := strings.Index(tag, key)
	if idx < 0 {
		return ""
	}
	rest := tag[idx+len(key):]
	// Find the value up to ; or "
	end := strings.IndexAny(rest, "; \"")
	if end > 0 {
		return rest[:end]
	}
	return rest
}

func runDBCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
