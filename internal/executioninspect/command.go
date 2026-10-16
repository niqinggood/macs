package executioninspect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mac/internal/agentos"
	"mac/internal/configvalue"
	sessioncore "mac/internal/session"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type executionInspectCommandOptions struct {
	Journal      string
	TaskID       string
	Workspace    string
	DatabasePath string
	NoReceipts   bool
	Compact      bool
	Help         bool
}

func InspectionRequested(args []string) bool {
	return len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "inspect-execution")
}

func parseExecutionInspectCommandArgs(args []string) (executionInspectCommandOptions, error) {
	options := executionInspectCommandOptions{}
	readValue := func(index *int, flag string) (string, error) {
		if *index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		(*index)++
		return strings.TrimSpace(args[*index]), nil
	}
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; arg {
		case "--help", "-h":
			options.Help = true
		case "--journal", "-j":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Journal = value
		case "--task", "-t":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.TaskID = value
		case "--path", "-C":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Workspace = value
		case "--db":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.DatabasePath = value
		case "--no-receipts":
			options.NoReceipts = true
		case "--compact":
			options.Compact = true
		default:
			if strings.HasPrefix(arg, "-") {
				return options, fmt.Errorf("unknown inspect-execution option %q", arg)
			}
			if options.Journal != "" {
				return options, fmt.Errorf("unexpected positional argument %q", arg)
			}
			options.Journal = strings.TrimSpace(arg)
		}
	}
	if options.Help {
		return options, nil
	}
	if options.Journal == "" && options.TaskID == "" {
		return options, fmt.Errorf("provide --journal <path> or --task <task-id>")
	}
	if options.NoReceipts && options.DatabasePath != "" {
		return options, fmt.Errorf("--db and --no-receipts cannot be used together")
	}
	return options, nil
}

func executionInspectCommandUsage() string {
	return `OpenKitty execution inspector (read-only, on demand)

Usage:
  openkitty inspect-execution --task <task-id> [-C workspace]
  openkitty inspect-execution --journal <transcript.jsonl> [--db <sqlite-db>]

Options:
  -t, --task <id>         Inspect the newest journal for a task
  -j, --journal <path>    Inspect an explicit transcript JSONL journal
  -C, --path <dir>        Workspace used to find journals and the default DB
      --db <path>         Explicit SQLite DB used for receipt evidence
      --no-receipts       Do not query execution receipts (more conservative)
      --compact           Emit compact JSON instead of indented JSON
  -h, --help              Show this help

The command never starts an agent or replays a tool. It reports whether the
durable evidence is safe to resume, needs reconciliation, or must be blocked.`
}

func RunCommand(args []string) int {
	options, err := parseExecutionInspectCommandArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openkitty inspect-execution: %v\n\n%s\n", err, executionInspectCommandUsage())
		return 2
	}
	if options.Help {
		fmt.Println(executionInspectCommandUsage())
		return 0
	}

	journalPath, workspace, err := resolveExecutionInspectionJournal(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openkitty inspect-execution: %v\n", err)
		return 1
	}
	transcript, err := sessioncore.RecoverSessionTranscriptFromJournal(journalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openkitty inspect-execution: recover journal: %v\n", err)
		return 1
	}
	if options.TaskID != "" && transcript.SessionID != options.TaskID {
		fmt.Fprintf(os.Stderr, "openkitty inspect-execution: journal belongs to task %q, not %q\n", transcript.SessionID, options.TaskID)
		return 1
	}

	receiptTaskID := transcript.SessionID
	if transcript.Checkpoint != nil {
		receiptTaskID = firstNonEmpty(transcript.Checkpoint.ReceiptTaskID, transcript.Checkpoint.TaskID, transcript.SessionID)
	}
	coverage := ExecutionReceiptCoverage{TaskID: receiptTaskID}
	var receipts []agentos.ToolExecutionReceipt
	if options.NoReceipts {
		coverage.Detail = "receipt lookup was disabled by --no-receipts"
	} else {
		databasePath := strings.TrimSpace(options.DatabasePath)
		if databasePath == "" {
			databasePath = configvalue.ConfiguredSQLitePath(filepath.Join(workspace, ".openkitty", "openkitty.db"))
		}
		receipts, coverage = loadExecutionInspectionReceipts(databasePath, receiptTaskID)
	}

	report := InspectExecution(transcript, receipts, coverage)
	var encoded []byte
	if options.Compact {
		encoded, err = json.Marshal(report)
	} else {
		encoded, err = json.MarshalIndent(report, "", "  ")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "openkitty inspect-execution: encode report: %v\n", err)
		return 1
	}
	fmt.Println(string(encoded))
	return 0
}

func resolveExecutionInspectionJournal(options executionInspectCommandOptions) (string, string, error) {
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("resolve current workspace: %w", err)
		}
		workspace = current
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace: %w", err)
	}
	workspace = filepath.Clean(absWorkspace)

	journalPath := strings.TrimSpace(options.Journal)
	if journalPath == "" {
		journalPath, err = newestExecutionJournalForTask(sessioncore.TranscriptDirectory(workspace), options.TaskID)
		if err != nil {
			return "", "", err
		}
	} else {
		journalPath, err = filepath.Abs(journalPath)
		if err != nil {
			return "", "", fmt.Errorf("resolve journal path: %w", err)
		}
		journalPath = filepath.Clean(journalPath)
		if options.Workspace == "" {
			if inferred, ok := inferExecutionWorkspaceFromJournal(journalPath); ok {
				workspace = inferred
			}
		}
	}
	info, err := os.Stat(journalPath)
	if err != nil {
		return "", "", fmt.Errorf("inspect journal: %w", err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("journal path is a directory: %s", journalPath)
	}
	return journalPath, workspace, nil
}

func newestExecutionJournalForTask(transcriptDir, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task id is empty")
	}
	pattern := filepath.Join(transcriptDir, "transcript_"+sessioncore.SafeTranscriptFileID(taskID)+"_*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("find task journal: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no transcript journal found for task %q under %s", taskID, transcriptDir)
	}
	sort.Slice(matches, func(i, j int) bool {
		left, leftErr := os.Stat(matches[i])
		right, rightErr := os.Stat(matches[j])
		if leftErr != nil || rightErr != nil {
			return matches[i] > matches[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return matches[0], nil
}

func inferExecutionWorkspaceFromJournal(journalPath string) (string, bool) {
	transcripts := filepath.Dir(journalPath)
	logs := filepath.Dir(transcripts)
	openKittyDir := filepath.Dir(logs)
	if !strings.EqualFold(filepath.Base(transcripts), "transcripts") ||
		!strings.EqualFold(filepath.Base(logs), "logs") ||
		!strings.EqualFold(filepath.Base(openKittyDir), ".openkitty") {
		return "", false
	}
	return filepath.Dir(openKittyDir), true
}

func loadExecutionInspectionReceipts(databasePath, taskID string) ([]agentos.ToolExecutionReceipt, ExecutionReceiptCoverage) {
	coverage := ExecutionReceiptCoverage{
		TaskID: strings.TrimSpace(taskID),
		Source: "sqlite",
	}
	databasePath = strings.TrimSpace(databasePath)
	if databasePath == "" {
		coverage.Detail = "no SQLite database path was available"
		return nil, coverage
	}
	absPath, err := filepath.Abs(databasePath)
	if err != nil {
		coverage.Detail = fmt.Sprintf("resolve SQLite database: %v", err)
		return nil, coverage
	}
	absPath = filepath.Clean(absPath)
	coverage.Source = absPath
	info, err := os.Stat(absPath)
	if err != nil {
		coverage.Detail = fmt.Sprintf("inspect SQLite database: %v", err)
		return nil, coverage
	}
	if info.IsDir() {
		coverage.Detail = "SQLite database path is a directory"
		return nil, coverage
	}

	databaseURL := "file:" + filepath.ToSlash(absPath) + "?mode=ro"
	database, err := gorm.Open(sqlite.Open(databaseURL), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		coverage.Detail = fmt.Sprintf("open SQLite database read-only: %v", err)
		return nil, coverage
	}
	if sqlDB, dbErr := database.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	coverage.Available = true

	var taskCount int64
	if err := database.Table("gateway_task_records").Where("task_id = ?", coverage.TaskID).Count(&taskCount).Error; err != nil {
		coverage.Detail = fmt.Sprintf("query task receipt coverage: %v", err)
		return nil, coverage
	}
	coverage.Authoritative = taskCount == 1

	var receipts []agentos.ToolExecutionReceipt
	query := database.Where("task_id = ?", coverage.TaskID).Order("tool_call_id ASC").Find(&receipts)
	if query.Error != nil {
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			query.Error = nil
		} else {
			coverage.Detail = fmt.Sprintf("query tool receipts: %v", query.Error)
			coverage.Authoritative = false
			return nil, coverage
		}
	}
	coverage.ReceiptCount = len(receipts)
	if coverage.Authoritative {
		coverage.Detail = "task and tool receipt tables were queried successfully in read-only mode"
	} else {
		coverage.Detail = "receipt query succeeded, but no matching Gateway task proves ledger coverage"
	}
	return receipts, coverage
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
