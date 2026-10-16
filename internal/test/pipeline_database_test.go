package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineDatabaseConfigurationRequiresStepBindingWhenAmbiguous(t *testing.T) {
	p := Pipeline{
		Name: "db-pipeline",
		DatabaseConnections: []PipelineDatabaseConnection{
			{ID: "primary_db", Driver: "mysql", DSNEnv: "PRIMARY_DSN", Permission: "readonly"},
			{ID: "archive_db", Driver: "postgres", DSNEnv: "ARCHIVE_DSN", Permission: "readonly"},
		},
		Steps: []PipelineStep{{Name: "query", Tools: []string{"db_query"}}},
	}
	if err := validatePipelineDatabaseConnections(p); err == nil || !strings.Contains(err.Error(), "must choose one") {
		t.Fatalf("expected ambiguous connection error, got %v", err)
	}
	p.Steps[0].DatabaseConnection = "primary_db"
	if err := validatePipelineDatabaseConnections(p); err != nil {
		t.Fatalf("valid pipeline database configuration rejected: %v", err)
	}
}

func TestPipelineDatabaseConfigurationRejectsPlaintextRemoteDSN(t *testing.T) {
	p := Pipeline{
		Name: "unsafe",
		DatabaseConnections: []PipelineDatabaseConnection{{
			ID: "customer_db", Enabled: true, Driver: "mysql", DSN: "root:secret@tcp(localhost:3306)/sales",
		}},
		Steps: []PipelineStep{{Name: "query", Tools: []string{"db_query"}, DatabaseConnection: "customer_db"}},
	}
	if err := validatePipelineDatabaseConnections(p); err == nil || !strings.Contains(err.Error(), "must use dsn_env") {
		t.Fatalf("expected plaintext DSN rejection, got %v", err)
	}
}

func TestPipelineDatabaseConfigurationRejectsWritePermission(t *testing.T) {
	p := Pipeline{
		Name: "unsafe-write",
		DatabaseConnections: []PipelineDatabaseConnection{{
			ID: "customer_db", Driver: "mysql", DSNEnv: "CUSTOMER_DSN", Permission: "readwrite",
		}},
		Steps: []PipelineStep{{Name: "query", Tools: []string{"db_query"}, DatabaseConnection: "customer_db"}},
	}
	if err := validatePipelineDatabaseConnections(p); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("expected pipeline write permission rejection, got %v", err)
	}
}

func TestPipelineDBRecordRoundTripsDatabaseConnections(t *testing.T) {
	connections := []PipelineDatabaseConnection{{
		ID: "analytics_db", Driver: "sqlite", DSNEnv: "ANALYTICS_DSN", Permission: "readonly", MaxRows: 250,
	}}
	steps := []PipelineStep{{Name: "load", Tools: []string{"db_query"}, DatabaseConnection: "analytics_db"}}
	connectionsJSON, _ := json.Marshal(connections)
	stepsJSON, _ := json.Marshal(steps)
	record := PipelineDBRecord{
		Name: "analytics", Description: "test", StepsJSON: string(stepsJSON), DatabaseConnectionsJSON: string(connectionsJSON),
	}
	p := record.ToPipeline()
	if len(p.DatabaseConnections) != 1 || p.DatabaseConnections[0].ID != "analytics_db" {
		t.Fatalf("database connections not restored: %#v", p.DatabaseConnections)
	}
	if len(p.Steps) != 1 || p.Steps[0].DatabaseConnection != "analytics_db" {
		t.Fatalf("step database binding not restored: %#v", p.Steps)
	}
}

func TestCustomerServiceAndDataAnalystOwnSeparatePipelineConnections(t *testing.T) {
	pipelines := (&GatewayService{}).defaultPipelineSet()
	cs := pipelines["cs-router"]
	analytics := pipelines["data-analysis-pipeline"]
	if len(cs.DatabaseConnections) != 1 || cs.DatabaseConnections[0].ID != "customer_db" {
		t.Fatalf("customer service pipeline connection = %#v", cs.DatabaseConnections)
	}
	if len(analytics.DatabaseConnections) != 1 || analytics.DatabaseConnections[0].ID != "analytics_db" {
		t.Fatalf("data analyst pipeline connection = %#v", analytics.DatabaseConnections)
	}
	if cs.DatabaseConnections[0].DSNEnv == analytics.DatabaseConnections[0].DSNEnv {
		t.Fatal("customer service and data analyst must not share the same DSN environment variable")
	}
	if cs.DatabaseConnections[0].Permission != "readonly" || analytics.DatabaseConnections[0].Permission != "readonly" {
		t.Fatal("built-in AI employee database connections must default to readonly")
	}
	for _, p := range []Pipeline{cs, analytics} {
		if err := validatePipelineDatabaseConnections(p); err != nil {
			t.Fatalf("built-in pipeline database configuration rejected: %v", err)
		}
	}
}

func TestPipelineStepRegistryProvidesItsBoundDatabaseTool(t *testing.T) {
	exec := &PipelineExecution{
		databaseConnections: map[string]PipelineDatabaseConnection{
			"customer_db": {ID: "customer_db", Driver: "sqlite", DSNEnv: "CUSTOMER_DSN", Permission: "readonly"},
		},
		stepDatabaseConnections: map[int]string{0: "customer_db"},
		databaseTools:           make(map[string]*PipelineDBQueryTool),
	}
	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	registry := executor.buildStepRegistry([]string{"db_query"}, exec, 0)
	tool, ok := registry.Get("db_query")
	if !ok {
		t.Fatal("pipeline-bound db_query was not registered")
	}
	if _, ok := tool.(*auditTool); !ok {
		t.Fatalf("pipeline db_query should retain pipeline auditing, got %T", tool)
	}
	exec.closeDatabaseTools()
}

func TestPipelineDBQueryToolReadOnlyAllowlistRedactionAndSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "customers.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE v_customer_profile (id INTEGER PRIMARY KEY, name TEXT, password_hash TEXT); INSERT INTO v_customer_profile(name,password_hash) VALUES ('Alice','secret'); CREATE TABLE private_notes(id INTEGER);`); err != nil {
		db.Close()
		t.Fatalf("seed sqlite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite seed db: %v", err)
	}

	t.Setenv("PIPELINE_TEST_DB_DSN", dbPath)
	outputDir := filepath.Join(t.TempDir(), "snapshots")
	tool := NewPipelineDBQueryTool(PipelineDatabaseConnection{
		ID: "customer_db", Enabled: true, Driver: "sqlite", DSNEnv: "PIPELINE_TEST_DB_DSN",
		Permission: "readonly", AllowedTables: []string{"v_customer_profile"}, DeniedColumns: []string{"password_hash"},
		MaxRows: 10, TimeoutSeconds: 5,
	}, log.New(io.Discard, "", 0), outputDir)
	defer tool.Close()

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"sql": "SELECT * FROM v_customer_profile", "save_as": "customer_snapshot.csv",
	})
	if err != nil {
		t.Fatalf("pipeline db query failed: %v", err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "[REDACTED]") || strings.Contains(result, "secret") {
		t.Fatalf("unexpected redacted query result: %s", result)
	}
	snapshot, err := os.ReadFile(filepath.Join(outputDir, "customer_snapshot.csv"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(snapshot), "[REDACTED]") || strings.Contains(string(snapshot), "secret") {
		t.Fatalf("snapshot did not preserve redaction: %s", snapshot)
	}

	if _, err := tool.Execute(context.Background(), map[string]interface{}{"sql": "UPDATE v_customer_profile SET name='Bob'"}); err == nil || !strings.Contains(err.Error(), "write operations not allowed") {
		t.Fatalf("readonly write should fail, got %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"sql": "SELECT * FROM private_notes"}); err == nil || !strings.Contains(err.Error(), "outside the pipeline allowlist") {
		t.Fatalf("non-allowlisted table should fail, got %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"sql": "SELECT password_hash AS value FROM v_customer_profile"}); err == nil || !strings.Contains(err.Error(), "denied column") {
		t.Fatalf("explicit sensitive column should fail, got %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"sql": "-- bounded query\nWITH recent AS (SELECT id, name FROM v_customer_profile) SELECT * FROM recent"}); err != nil {
		t.Fatalf("read-only CTE over an allowlisted table should pass: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"sql": "SELECT 1; SELECT 2"}); err == nil || !strings.Contains(err.Error(), "multiple SQL statements") {
		t.Fatalf("multiple statements should fail, got %v", err)
	}
}
