package app

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestPrepareGatewaySQLiteMigrationsUpgradesLegacyDENotifications(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec(`
		CREATE TABLE de_notifications (
			employee_id TEXT,
			supervisor TEXT,
			channel TEXT,
			message TEXT,
			created_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO de_notifications
			(employee_id, supervisor, channel, message, created_at)
		VALUES
			('employee-a', 'owner-a', 'web', 'first', '2026-07-27 10:00:00'),
			('employee-b', 'owner-b', 'email', 'second', '2026-07-27 11:00:00')
	`).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("prepare migration: %v", err)
	}
	if err := db.AutoMigrate(&DENotificationDB{}); err != nil {
		t.Fatalf("AutoMigrate after compatibility rebuild: %v", err)
	}
	// The compatibility pass must be idempotent across later restarts.
	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("repeat compatibility migration: %v", err)
	}

	var rows []DENotificationDB
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read migrated rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 preserved rows, got %d", len(rows))
	}
	if rows[0].ID == 0 || rows[1].ID == 0 || rows[0].ID == rows[1].ID {
		t.Fatalf("migration did not assign stable primary keys: %#v", rows)
	}
	if rows[0].EmployeeID != "employee-a" || rows[0].Message != "first" ||
		rows[1].EmployeeID != "employee-b" || rows[1].Message != "second" {
		t.Fatalf("legacy notification content changed: %#v", rows)
	}

	var columns []sqliteTableColumn
	if err := db.Raw("PRAGMA table_info(`de_notifications`)").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect migrated schema: %v", err)
	}
	foundPrimaryKey := false
	foundColumns := make(map[string]bool, len(columns))
	for _, column := range columns {
		foundColumns[column.Name] = true
		if column.Name == "id" && column.PrimaryKey > 0 {
			foundPrimaryKey = true
		}
	}
	if !foundPrimaryKey {
		t.Fatalf("migrated table does not expose id as primary key: %#v", columns)
	}
	for _, name := range []string{"id", "employee_id", "supervisor", "channel", "message", "created_at"} {
		if !foundColumns[name] {
			t.Fatalf("migrated table lacks stable column %q: %#v", name, columns)
		}
	}
}

func TestPrepareGatewaySQLiteMigrationsPreservesGarbleObfuscatedDENotifications(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec(`
		CREATE TABLE de_notifications (
			obf_id INTEGER PRIMARY KEY AUTOINCREMENT,
			obf_employee TEXT,
			obf_supervisor TEXT,
			obf_channel TEXT,
			obf_message TEXT,
			obf_created DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create obfuscated legacy table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO de_notifications
			(obf_employee, obf_supervisor, obf_channel, obf_message, obf_created)
		VALUES
			('employee-a', 'owner-a', 'web', 'first', '2026-07-27 10:00:00'),
			('employee-b', 'owner-b', 'email', 'second', '2026-07-27 11:00:00')
	`).Error; err != nil {
		t.Fatalf("seed obfuscated legacy rows: %v", err)
	}

	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("prepare migration: %v", err)
	}
	if err := db.AutoMigrate(&DENotificationDB{}); err != nil {
		t.Fatalf("AutoMigrate after obfuscated compatibility rebuild: %v", err)
	}

	var rows []DENotificationDB
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read migrated rows: %v", err)
	}
	if len(rows) != 2 ||
		rows[0].EmployeeID != "employee-a" || rows[0].Supervisor != "owner-a" ||
		rows[0].Channel != "web" || rows[0].Message != "first" ||
		rows[1].EmployeeID != "employee-b" || rows[1].Supervisor != "owner-b" ||
		rows[1].Channel != "email" || rows[1].Message != "second" {
		t.Fatalf("obfuscated legacy notification content changed: %#v", rows)
	}
}

func TestPrepareGatewaySQLiteMigrationsUpgradesLegacyPipelineTriggers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec(`
		CREATE TABLE pipeline_triggers (
			keyword TEXT,
			pipeline_name TEXT,
			channel TEXT
		)
	`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO pipeline_triggers (keyword, pipeline_name, channel)
		VALUES
			('invoice', 'finance', 'web'),
			('deploy', 'devops', 'wechat')
	`).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("prepare migration: %v", err)
	}
	if err := db.AutoMigrate(&PipelineTriggerDBRecord{}); err != nil {
		t.Fatalf("AutoMigrate after compatibility rebuild: %v", err)
	}
	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("repeat compatibility migration: %v", err)
	}

	var records []PipelineTriggerDBRecord
	if err := db.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("read migrated rows: %v", err)
	}
	if len(records) != 2 ||
		records[0].ID == 0 || records[0].Keyword != "invoice" ||
		records[0].PipelineName != "finance" || records[0].Channel != "web" ||
		records[1].ID == 0 || records[1].Keyword != "deploy" ||
		records[1].PipelineName != "devops" || records[1].Channel != "wechat" {
		t.Fatalf("legacy pipeline trigger content changed: %#v", records)
	}
}

func TestPrepareGatewaySQLiteMigrationsNormalizesObfuscatedPipelineTriggerID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec(`
		CREATE TABLE pipeline_triggers (
			garbled_id INTEGER PRIMARY KEY AUTOINCREMENT,
			keyword TEXT,
			pipeline_name TEXT,
			channel TEXT
		)
	`).Error; err != nil {
		t.Fatalf("create obfuscated legacy table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO pipeline_triggers
			(garbled_id, keyword, pipeline_name, channel)
		VALUES
			(7, 'invoice', 'finance', 'web'),
			(11, 'deploy', 'devops', 'wechat')
	`).Error; err != nil {
		t.Fatalf("seed obfuscated legacy rows: %v", err)
	}

	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("prepare migration: %v", err)
	}
	if err := db.AutoMigrate(&PipelineTriggerDBRecord{}); err != nil {
		t.Fatalf("AutoMigrate after compatibility rebuild: %v", err)
	}

	var records []PipelineTriggerDBRecord
	if err := db.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("read migrated rows: %v", err)
	}
	if len(records) != 2 ||
		records[0].ID != 7 || records[0].Keyword != "invoice" ||
		records[1].ID != 11 || records[1].Keyword != "deploy" {
		t.Fatalf("obfuscated pipeline trigger content changed: %#v", records)
	}

	var columns []sqliteTableColumn
	if err := db.Raw("PRAGMA table_info(`pipeline_triggers`)").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect migrated schema: %v", err)
	}
	for _, column := range columns {
		if column.Name == "id" && column.PrimaryKey > 0 {
			return
		}
	}
	t.Fatalf("migrated table does not expose id as primary key: %#v", columns)
}
