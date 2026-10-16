package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jsonPatchTestContext(reader string) context.Context {
	return context.WithValue(context.Background(), "agent_name", reader)
}

func TestJSONPatchToolAppliesStructuralOperationsAtomically(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "workflow.json")
	original := `{
  "slides": [{"blocks": [{"type": "text", "content": "old"}]}],
  "meta": {"remove_me": true}
}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := "json-patch-structural"
	globalFileState.RecordRead(path, reader)

	tool := NewJSONPatchTool(workspace)
	result, err := tool.Execute(jsonPatchTestContext(reader), map[string]interface{}{
		"path": "workflow.json",
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/slides/0/blocks/0/content", "value": "new"},
			map[string]interface{}{"op": "add", "path": "/slides/0/blocks/0/heading", "value": "提示"},
			map[string]interface{}{"op": "add", "path": "/slides/-", "value": map[string]interface{}{"type": "end"}},
			map[string]interface{}{"op": "remove", "path": "/meta/remove_me"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Applied 4 atomic JSON patch operation") {
		t.Fatalf("unexpected result: %s", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("patched file is not valid JSON: %v\n%s", err, data)
	}
	slides := document["slides"].([]interface{})
	if len(slides) != 2 {
		t.Fatalf("slides length = %d, want 2", len(slides))
	}
	block := slides[0].(map[string]interface{})["blocks"].([]interface{})[0].(map[string]interface{})
	if block["content"] != "new" || block["heading"] != "提示" {
		t.Fatalf("block was not structurally patched: %#v", block)
	}
	if _, exists := document["meta"].(map[string]interface{})["remove_me"]; exists {
		t.Fatalf("remove operation did not delete member: %#v", document["meta"])
	}
}

func TestJSONPatchToolLeavesFileUnchangedWhenAnyOperationFails(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "data.json")
	original := []byte(`{"name":"before","items":[1]}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	reader := "json-patch-rollback"
	globalFileState.RecordRead(path, reader)

	_, err := NewJSONPatchTool(workspace).Execute(jsonPatchTestContext(reader), map[string]interface{}{
		"path": "data.json",
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/name", "value": "after"},
			map[string]interface{}{"op": "replace", "path": "/missing", "value": true},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing-member error, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("failed transaction changed file:\n%s", data)
	}
}

func TestJSONPatchToolSupportsEscapedPointerTokens(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "escaped.json")
	if err := os.WriteFile(path, []byte(`{"a/b~c":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := "json-patch-escaped"
	globalFileState.RecordRead(path, reader)
	_, err := NewJSONPatchTool(workspace).Execute(jsonPatchTestContext(reader), map[string]interface{}{
		"path": "escaped.json",
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/a~1b~0c", "value": "new"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"a/b~c": "new"`) {
		t.Fatalf("escaped pointer did not update key: %s", data)
	}
}

func TestJSONPatchToolRequiresPriorRead(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "unread.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewJSONPatchTool(workspace).Execute(jsonPatchTestContext("json-patch-unread"), map[string]interface{}{
		"path": "unread.json",
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/ok", "value": false},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Use read_file first") {
		t.Fatalf("expected read-before-patch error, got %v", err)
	}
}

func TestApplyJSONPatchAtRejectsUnsupportedOperation(t *testing.T) {
	document := map[string]interface{}{"name": "openkitty"}
	if _, err := applyJSONPatchAt(document, nil, "move", nil); err == nil || !strings.Contains(err.Error(), "unsupported operation") {
		t.Fatalf("unsupported operation should return a clear error, got %v", err)
	}
}

func TestParseJSONArrayIndexRejectsLeadingZero(t *testing.T) {
	if _, err := parseJSONArrayIndex("01"); err == nil {
		t.Fatal("RFC 6902 array index with a leading zero must be rejected")
	}
	if index, err := parseJSONArrayIndex("0"); err != nil || index != 0 {
		t.Fatalf("zero index should remain valid, index=%d err=%v", index, err)
	}
}
