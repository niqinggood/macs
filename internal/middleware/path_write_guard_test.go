package middleware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/toolcontract"
)

func TestPathWriteGuardBlocksReadOnlyTrees(t *testing.T) {
	middleware := NewPathWriteGuardMiddleware(`C:\work\repo`, []string{"tests/**", "**/tests/**"}, nil)

	for _, testCase := range []struct {
		name string
		call toolcontract.Call
	}{
		{
			name: "edit root test",
			call: toolcontract.Call{Name: "edit_file", Arguments: map[string]interface{}{
				"file_path": "tests/test_blueprints.py",
			}},
		},
		{
			name: "write nested test",
			call: toolcontract.Call{Name: "write_file", Arguments: map[string]interface{}{
				"path": "sympy/core/tests/test_symbol.py",
			}},
		},
		{
			name: "json patch test fixture",
			call: toolcontract.Call{Name: "json_patch", Arguments: map[string]interface{}{
				"path": "pkg/tests/fixtures/example.json",
			}},
		},
		{
			name: "atomic multiedit containing test",
			call: toolcontract.Call{Name: "multiedit", Arguments: map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{"file_path": "src/flask/blueprints.py"},
					map[string]interface{}{"file_path": "tests/test_blueprints.py"},
				},
			}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := middleware.BeforeTool(&Context{ToolCall: testCase.call})
			if err == nil || !strings.Contains(err.Error(), "read-only path") {
				t.Fatalf("expected read-only path rejection, got %v", err)
			}
		})
	}
}

func TestPathWriteGuardAllowsProductionEditsAndReads(t *testing.T) {
	middleware := NewPathWriteGuardMiddleware(t.TempDir(), []string{"tests/**", "**/tests/**"}, nil)
	calls := []toolcontract.Call{
		{Name: "edit_file", Arguments: map[string]interface{}{"file_path": "src/flask/blueprints.py"}},
		{Name: "read_file", Arguments: map[string]interface{}{"path": "tests/test_blueprints.py"}},
		{Name: "multiedit", Arguments: map[string]interface{}{"edits": []interface{}{
			map[string]interface{}{"file_path": "src/flask/app.py"},
			map[string]interface{}{"file_path": "src/flask/blueprints.py"},
		}}},
	}
	for _, call := range calls {
		if err := middleware.BeforeTool(&Context{ToolCall: call}); err != nil {
			t.Fatalf("%s should be allowed: %v", call.Name, err)
		}
	}
}

func TestPathWriteGuardRevertsArbitraryToolChanges(t *testing.T) {
	workspace := t.TempDir()
	testPath := filepath.Join(workspace, "tests", "test_example.py")
	deletedPath := filepath.Join(workspace, "tests", "keep.py")
	newPath := filepath.Join(workspace, "tests", "unauthorized.py")
	productionPath := filepath.Join(workspace, "src", "example.py")
	for _, directory := range []string{filepath.Dir(testPath), filepath.Dir(productionPath)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(testPath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deletedPath, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(productionPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	middleware := NewPathWriteGuardMiddleware(workspace, []string{"tests/**"}, nil)
	ctx := &Context{
		ToolCall: toolcontract.Call{ID: "cli-write", Name: "cli", Arguments: map[string]interface{}{
			"command": "python helper.py",
		}},
		Metadata: make(map[string]any),
	}
	if err := middleware.BeforeTool(ctx); err != nil {
		t.Fatalf("arbitrary tool should execute under post-write enforcement: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deletedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(productionPath, []byte("allowed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.ToolResult = &toolcontract.Result{
		ToolCallID:         ctx.ToolCall.ID,
		Name:               ctx.ToolCall.Name,
		Content:            "command succeeded",
		ExecutionSucceeded: toolcontract.OutcomePointer(true),
	}
	if err := middleware.AfterTool(ctx); err != nil {
		t.Fatalf("restore protected paths: %v", err)
	}

	assertFileContent := func(path, want string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if got := string(data); got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	assertFileContent(testPath, "original\n")
	assertFileContent(deletedPath, "keep\n")
	assertFileContent(productionPath, "allowed\n")
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("unauthorized new test file still exists: %v", err)
	}
	if ctx.ToolResult == nil || ctx.ToolResult.ExecutionSucceeded == nil || *ctx.ToolResult.ExecutionSucceeded {
		t.Fatalf("expected tool result to be marked failed: %#v", ctx.ToolResult)
	}
	if !strings.Contains(ctx.ToolResult.Content, "all protected changes were reverted") {
		t.Fatalf("missing model-visible rollback message: %q", ctx.ToolResult.Content)
	}
}
