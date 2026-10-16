package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"

	"gorm.io/gorm"
)

const ToolReceiptUniqueIndexName = "idx_tool_receipt_call"

type sqliteIndexOwner struct {
	TableName string `gorm:"column:tbl_name"`
}

type sqliteTableColumn struct {
	CID        int    `gorm:"column:cid"`
	Name       string `gorm:"column:name"`
	Type       string `gorm:"column:type"`
	PrimaryKey int    `gorm:"column:pk"`
}

// preserveGatewayGORMReflectionNames gives identifier obfuscators an explicit
// reflection edge for models whose field names are consumed indirectly by
// GORM. Stable column tags remain the primary contract; this also protects
// existing binaries and schemas created before those tags were added.
func PreserveGORMReflectionNames(models ...interface{}) {
	for _, model := range models {
		_ = reflect.TypeOf(model)
	}
	_ = reflect.TypeOf(sqliteTableColumn{})
}

// prepareGatewaySQLiteMigrations removes a named-index collision left by
// older Garble builds. SQLite index names are database-global, so an index on
// an obfuscated legacy table can prevent AutoMigrate from creating the same
// durable index on the new stable table.
func PrepareSQLite(db *gorm.DB, toolReceiptTable string, reflectionModels ...interface{}) error {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return nil
	}
	PreserveGORMReflectionNames(reflectionModels...)

	var ownerTable string
	err := db.Raw(
		"SELECT tbl_name FROM sqlite_master WHERE type = 'index' AND name = ? LIMIT 1",
		ToolReceiptUniqueIndexName,
	).Row().Scan(&ownerTable)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect SQLite index %s: %w", ToolReceiptUniqueIndexName, err)
	}
	ownerTable = strings.TrimSpace(ownerTable)
	if ownerTable != "" && ownerTable != toolReceiptTable {
		if err := db.Exec("DROP INDEX IF EXISTS `idx_tool_receipt_call`").Error; err != nil {
			return fmt.Errorf(
				"remove legacy SQLite index %s from %s: %w",
				ToolReceiptUniqueIndexName,
				ownerTable,
				err,
			)
		}
		log.Printf(
			"[Migration] removed legacy SQLite index %s from obfuscated table %s; AutoMigrate will rebuild it on %s",
			ToolReceiptUniqueIndexName,
			ownerTable,
			toolReceiptTable,
		)
	}

	if err := MigrateLegacyDENotifications(db); err != nil {
		return err
	}
	return MigrateLegacyPipelineTriggers(db)
}

// migrateLegacyDENotifications upgrades the original notification table,
// which did not have an ID column. SQLite cannot add an AUTOINCREMENT primary
// key with ALTER TABLE, so GORM's normal AutoMigrate fails unless the table is
// rebuilt. The rebuild is transactional and preserves every legacy row.
func MigrateLegacyDENotifications(db *gorm.DB) error {
	const (
		tableName = "de_notifications"
		tempName  = "de_notifications__openkitty_migrate"
	)

	var tableCount int
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		tableName,
	).Row().Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect SQLite table %s: %w", tableName, err)
	}
	if tableCount == 0 {
		return nil
	}

	var columns []sqliteTableColumn
	rows, err := db.Raw("PRAGMA table_info(`de_notifications`)").Rows()
	if err != nil {
		return fmt.Errorf("inspect legacy %s schema: %w", tableName, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			column       sqliteTableColumn
			notNull      int
			defaultValue any
		)
		if err := rows.Scan(
			&column.CID,
			&column.Name,
			&column.Type,
			&notNull,
			&defaultValue,
			&column.PrimaryKey,
		); err != nil {
			return fmt.Errorf("read legacy %s schema: %w", tableName, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read legacy %s schema: %w", tableName, err)
	}
	hasIDPrimaryKey := false
	existing := make(map[string]bool, len(columns))
	for _, column := range columns {
		name := strings.ToLower(strings.TrimSpace(column.Name))
		existing[name] = true
		if name == "id" && column.PrimaryKey > 0 {
			hasIDPrimaryKey = true
		}
	}
	if hasIDPrimaryKey {
		return nil
	}

	quoteIdentifier := func(name string) string {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	obfuscatedLayout := !existing["employee_id"] && len(columns) >= 5
	obfuscatedOffset := 0
	if obfuscatedLayout && len(columns) >= 6 && columns[0].PrimaryKey > 0 {
		// Early Garble builds allowed transformed Go field names to leak into
		// GORM's inferred SQLite columns. The model order stayed stable:
		// ID, EmployeeID, Supervisor, Channel, Message, CreatedAt.
		obfuscatedOffset = 1
	}
	selectExpr := func(column string, legacyOrdinal int, fallback string) string {
		if existing[column] {
			return quoteIdentifier(column)
		}
		legacyIndex := legacyOrdinal + obfuscatedOffset
		if obfuscatedLayout && legacyIndex >= 0 && legacyIndex < len(columns) {
			return quoteIdentifier(columns[legacyIndex].Name)
		}
		return fallback
	}

	var migratedRows int64
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DROP TABLE IF EXISTS `" + tempName + "`").Error; err != nil {
			return fmt.Errorf("remove stale migration table: %w", err)
		}
		if err := tx.Exec(
			"CREATE TABLE `" + tempName + "` (" +
				"`id` integer PRIMARY KEY AUTOINCREMENT," +
				"`employee_id` text," +
				"`supervisor` text," +
				"`channel` text," +
				"`message` text," +
				"`created_at` datetime" +
				")",
		).Error; err != nil {
			return fmt.Errorf("create compatible notification table: %w", err)
		}

		insertSQL := "INSERT INTO `" + tempName + "` " +
			"(`employee_id`,`supervisor`,`channel`,`message`,`created_at`) SELECT " +
			selectExpr("employee_id", 0, "''") + "," +
			selectExpr("supervisor", 1, "''") + "," +
			selectExpr("channel", 2, "''") + "," +
			selectExpr("message", 3, "''") + "," +
			selectExpr("created_at", 4, "CURRENT_TIMESTAMP") +
			" FROM `" + tableName + "` ORDER BY rowid"
		result := tx.Exec(insertSQL)
		if result.Error != nil {
			return fmt.Errorf("copy legacy notification rows: %w", result.Error)
		}
		migratedRows = result.RowsAffected

		if err := tx.Exec("DROP TABLE `" + tableName + "`").Error; err != nil {
			return fmt.Errorf("replace legacy notification table: %w", err)
		}
		if err := tx.Exec("ALTER TABLE `" + tempName + "` RENAME TO `" + tableName + "`").Error; err != nil {
			return fmt.Errorf("activate compatible notification table: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate legacy %s primary key: %w", tableName, err)
	}

	log.Printf("[Migration] upgraded legacy %s with an autoincrement primary key; preserved %d row(s)", tableName, migratedRows)
	return nil
}

// migrateLegacyPipelineTriggers normalizes schemas created by older and
// Garble-obfuscated builds. Some releases either omitted the ID column or
// persisted its transformed Go field name. In both cases GORM sees no `id`
// primary key and asks SQLite to add one with ALTER TABLE, which SQLite cannot
// do. Rebuilding the table also gives future binaries a stable schema.
func MigrateLegacyPipelineTriggers(db *gorm.DB) error {
	const (
		tableName = "pipeline_triggers"
		tempName  = "pipeline_triggers__openkitty_migrate"
	)

	var tableCount int
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		tableName,
	).Row().Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect SQLite table %s: %w", tableName, err)
	}
	if tableCount == 0 {
		return nil
	}

	var columns []sqliteTableColumn
	rows, err := db.Raw("PRAGMA table_info(`pipeline_triggers`)").Rows()
	if err != nil {
		return fmt.Errorf("inspect legacy %s schema: %w", tableName, err)
	}
	for rows.Next() {
		var (
			column       sqliteTableColumn
			notNull      int
			defaultValue any
		)
		if err := rows.Scan(
			&column.CID,
			&column.Name,
			&column.Type,
			&notNull,
			&defaultValue,
			&column.PrimaryKey,
		); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy %s schema: %w", tableName, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read legacy %s schema: %w", tableName, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy %s schema rows: %w", tableName, err)
	}

	existing := make(map[string]bool, len(columns))
	primaryKeyColumn := ""
	for _, column := range columns {
		name := strings.ToLower(strings.TrimSpace(column.Name))
		existing[name] = true
		if column.PrimaryKey > 0 && primaryKeyColumn == "" {
			primaryKeyColumn = column.Name
		}
		if name == "id" && column.PrimaryKey > 0 {
			return nil
		}
	}

	quoteIdentifier := func(name string) string {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	obfuscatedLayout := !existing["keyword"] && len(columns) >= 3
	obfuscatedOffset := 0
	if obfuscatedLayout && len(columns) >= 4 && columns[0].PrimaryKey > 0 {
		// Model field order is ID, Keyword, PipelineName, Channel.
		obfuscatedOffset = 1
	}
	selectExpr := func(column string, legacyOrdinal int, fallback string) string {
		if existing[column] {
			return quoteIdentifier(column)
		}
		legacyIndex := legacyOrdinal + obfuscatedOffset
		if obfuscatedLayout && legacyIndex >= 0 && legacyIndex < len(columns) {
			return quoteIdentifier(columns[legacyIndex].Name)
		}
		return fallback
	}

	var migratedRows int64
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DROP TABLE IF EXISTS `" + tempName + "`").Error; err != nil {
			return fmt.Errorf("remove stale migration table: %w", err)
		}
		if err := tx.Exec(
			"CREATE TABLE `" + tempName + "` (" +
				"`id` integer PRIMARY KEY AUTOINCREMENT," +
				"`keyword` text," +
				"`pipeline_name` text," +
				"`channel` text" +
				")",
		).Error; err != nil {
			return fmt.Errorf("create compatible pipeline trigger table: %w", err)
		}

		targetColumns := "`keyword`,`pipeline_name`,`channel`"
		selectColumns := selectExpr("keyword", 0, "''") + "," +
			selectExpr("pipeline_name", 1, "''") + "," +
			selectExpr("channel", 2, "''")
		if primaryKeyColumn != "" {
			targetColumns = "`id`," + targetColumns
			selectColumns = quoteIdentifier(primaryKeyColumn) + "," + selectColumns
		}
		insertSQL := "INSERT INTO `" + tempName + "` (" + targetColumns + ") SELECT " +
			selectColumns + " FROM `" + tableName + "` ORDER BY rowid"
		result := tx.Exec(insertSQL)
		if result.Error != nil {
			return fmt.Errorf("copy legacy pipeline trigger rows: %w", result.Error)
		}
		migratedRows = result.RowsAffected

		if err := tx.Exec("DROP TABLE `" + tableName + "`").Error; err != nil {
			return fmt.Errorf("replace legacy pipeline trigger table: %w", err)
		}
		if err := tx.Exec("ALTER TABLE `" + tempName + "` RENAME TO `" + tableName + "`").Error; err != nil {
			return fmt.Errorf("activate compatible pipeline trigger table: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate legacy %s primary key: %w", tableName, err)
	}

	log.Printf("[Migration] upgraded legacy %s with a stable autoincrement primary key; preserved %d row(s)", tableName, migratedRows)
	return nil
}
