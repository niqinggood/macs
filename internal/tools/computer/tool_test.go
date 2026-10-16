package computer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputerBrowserRunPersistsTargetObservesAndVerifies(t *testing.T) {
	workspace := t.TempDir()
	var calls []string
	ops := Operations{
		BrowserNavigate: func(_ context.Context, params map[string]interface{}) (string, error) {
			calls = append(calls, "navigate:"+stringParam(params, "url"))
			return "Opened tab target-42\nTitle: LIMS\nURL: http://lims.local", nil
		},
		BrowserPage: func(_ context.Context, params map[string]interface{}) (string, error) {
			action := stringParam(params, "action")
			calls = append(calls, action+":"+stringParam(params, "target_id"))
			if action == "screenshot" {
				path := stringParam(params, "file")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					return "", err
				}
				if err := os.WriteFile(path, []byte("png"), 0644); err != nil {
					return "", err
				}
				return "screenshot saved", nil
			}
			if action == "extract" {
				return "Visible text: 检测结果已保存\n[3] <button> 保存", nil
			}
			return action + " completed", nil
		},
		BrowserClose: func(_ context.Context, params map[string]interface{}) (string, error) {
			calls = append(calls, "close:"+stringParam(params, "target_id"))
			return "closed", nil
		},
	}
	tool := NewTool(workspace, nil, ops)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":      "run",
		"surface":     "browser",
		"url":         "http://lims.local",
		"objective":   "录入样品结果",
		"expect_text": "检测结果已保存",
		"steps": []interface{}{
			map[string]interface{}{"operation": "input", "index": float64(2), "text": "S-001"},
			map[string]interface{}{"operation": "click", "index": float64(3)},
		},
	})
	if err != nil {
		t.Fatalf("computer run failed: %v\n%s", err, out)
	}
	var result struct {
		OK      bool     `json:"ok"`
		Session *Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out)
	}
	if !result.OK || result.Session == nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Session.TargetID != "target-42" || result.Session.Status != SessionActive {
		t.Fatalf("session=%#v", result.Session)
	}
	if result.Session.LastScreenshotPath == "" || result.Session.LastObservation == "" {
		t.Fatalf("observation was not retained: %#v", result.Session)
	}
	if len(result.Session.Assertions) != 1 || result.Session.Assertions[0].Expected != "检测结果已保存" {
		t.Fatalf("verification evidence was not retained: %#v", result.Session.Assertions)
	}
	if len(result.Session.Steps) < 1 || result.Session.Steps[0].Arguments["text"] != "S-001" {
		t.Fatalf("replay arguments were not retained: %#v", result.Session.Steps)
	}
	if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(result.Session.LastScreenshotPath))); err != nil {
		t.Fatalf("screenshot missing: %v", err)
	}
	wantCalls := []string{
		"navigate:http://lims.local",
		"input:target-42",
		"click:target-42",
		"wait:target-42",
		"extract:target-42",
		"screenshot:target-42",
	}
	if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("calls=%v want=%v", calls, wantCalls)
	}

	closeOut, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":     "close",
		"session_id": result.Session.ID,
	})
	if err != nil || !strings.Contains(closeOut, `"status": "closed"`) {
		t.Fatalf("close failed: err=%v out=%s", err, closeOut)
	}
}

func TestComputerDesktopRunUsesOnePersistentSession(t *testing.T) {
	workspace := t.TempDir()
	var calls []map[string]interface{}
	tool := NewTool(workspace, nil, Operations{
		Desktop: func(_ context.Context, params map[string]interface{}) (string, error) {
			copy := make(map[string]interface{}, len(params))
			for key, value := range params {
				copy[key] = value
			}
			calls = append(calls, copy)
			if path := stringParam(params, "screenshot_file"); path != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					return "", err
				}
				if err := os.WriteFile(path, []byte("png"), 0644); err != nil {
					return "", err
				}
			}
			return "Active window: Laboratory LIMS", nil
		},
	})
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "run",
		"surface":       "desktop",
		"objective":     "采集实验数据",
		"expect_window": "LIMS",
		"steps": []interface{}{
			map[string]interface{}{"operation": "click", "x": float64(200), "y": float64(120)},
			map[string]interface{}{"operation": "type", "text": "S-001"},
			map[string]interface{}{"operation": "key", "key": "enter"},
		},
	})
	if err != nil {
		t.Fatalf("desktop run failed: %v\n%s", err, out)
	}
	var result struct {
		OK      bool     `json:"ok"`
		Session *Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Session == nil || result.Session.Surface != "desktop" {
		t.Fatalf("result=%#v", result)
	}
	if len(calls) != 5 { // three actions, window verification, final observation
		t.Fatalf("desktop calls=%d %#v", len(calls), calls)
	}
	if calls[4]["screenshot_file"] == "" {
		t.Fatalf("final observation did not request a screenshot: %#v", calls[4])
	}
	if len(result.Session.Steps) != 4 {
		t.Fatalf("session steps=%d want 4", len(result.Session.Steps))
	}
}

func TestComputerRunStopsAfterFailedStep(t *testing.T) {
	count := 0
	tool := NewTool(t.TempDir(), nil, Operations{
		Desktop: func(_ context.Context, params map[string]interface{}) (string, error) {
			count++
			if stringParam(params, "action") == "click" {
				return "", os.ErrPermission
			}
			return "ok", nil
		},
	})
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "run",
		"surface":       "desktop",
		"observe_after": false,
		"steps": []interface{}{
			map[string]interface{}{"operation": "click", "x": float64(1), "y": float64(2)},
			map[string]interface{}{"operation": "key", "key": "enter"},
		},
	})
	if err != nil {
		t.Fatalf("run should return a structured failed result, got error: %v", err)
	}
	if count != 1 || !strings.Contains(out, `"ok": false`) || !strings.Contains(out, `"status": "failed"`) {
		t.Fatalf("count=%d out=%s", count, out)
	}
}

func TestComputerRunDetectsStructuredDesktopFailure(t *testing.T) {
	count := 0
	tool := NewTool(t.TempDir(), nil, Operations{
		Desktop: func(_ context.Context, _ map[string]interface{}) (string, error) {
			count++
			return "## Desktop task\n\n- Status: failed\n- Error: no desktop input backend available\n", nil
		},
	})
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "run",
		"surface":       "desktop",
		"observe_after": false,
		"steps": []interface{}{
			map[string]interface{}{"operation": "click", "x": float64(1), "y": float64(2)},
			map[string]interface{}{"operation": "key", "key": "enter"},
		},
	})
	if err != nil {
		t.Fatalf("run should return a structured failed result, got error: %v", err)
	}
	if count != 1 || !strings.Contains(out, `"ok": false`) || !strings.Contains(out, "no desktop input backend available") {
		t.Fatalf("count=%d out=%s", count, out)
	}
}

func TestComputerToolMetadataAndSchema(t *testing.T) {
	tool := NewTool(".", nil, Operations{})
	metadata := tool.ToolSearchMetadata()
	if !contains(metadata.Aliases, "use_computer") || !contains(metadata.Aliases, "电脑操作") {
		t.Fatalf("aliases=%v", metadata.Aliases)
	}
	properties := tool.Parameters()["properties"].(map[string]interface{})
	if properties["steps"] == nil || properties["session_id"] == nil {
		t.Fatalf("computer schema is incomplete: %v", properties)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
