package computer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sessionFromResult(t *testing.T, output string) *Session {
	t.Helper()
	var result struct {
		Session *Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.Session == nil {
		t.Fatalf("missing session: %s (%v)", output, err)
	}
	return result.Session
}

func TestComputerRejectsStaleObservationWithoutBackendAction(t *testing.T) {
	calls := 0
	tool := NewTool(t.TempDir(), nil, Operations{Desktop: func(context.Context, map[string]interface{}) (string, error) {
		calls++
		return "Active window: Example", nil
	}})
	out, err := tool.Execute(context.Background(), map[string]interface{}{"action": "observe", "surface": "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	first := sessionFromResult(t, out)
	if !first.ObservationValid || first.ObservationID == "" {
		t.Fatal("missing observation revision")
	}
	out, err = tool.Execute(context.Background(), map[string]interface{}{"action": "observe", "session_id": first.ID})
	if err != nil {
		t.Fatal(err)
	}
	second := sessionFromResult(t, out)
	before := calls
	out, err = tool.Execute(context.Background(), map[string]interface{}{"action": "act", "session_id": first.ID, "observation_id": first.ObservationID, "operation": "click", "x": 10, "y": 20})
	if err != nil || !strings.Contains(out, "stale_observation") || calls != before {
		t.Fatalf("stale action executed: %s %v", out, err)
	}
	out, err = tool.Execute(context.Background(), map[string]interface{}{"action": "act", "session_id": first.ID, "observation_id": second.ObservationID, "operation": "click", "x": 10, "y": 20, "observe_after": false})
	if err != nil {
		t.Fatal(err)
	}
	if sessionFromResult(t, out).ObservationValid {
		t.Fatal("mutation left old observation valid")
	}
	before = calls
	out, _ = tool.Execute(context.Background(), map[string]interface{}{"action": "act", "session_id": first.ID, "observation_id": second.ObservationID, "operation": "click"})
	if calls != before || !strings.Contains(out, "stale_observation") {
		t.Fatal("mutated observation reused")
	}
}

func TestComputerPerStepAssertionStopsEvenWithContinueOnError(t *testing.T) {
	var actions []string
	tool := NewTool(t.TempDir(), nil, Operations{BrowserNavigate: func(context.Context, map[string]interface{}) (string, error) { return "Opened tab example", nil }, BrowserPage: func(_ context.Context, p map[string]interface{}) (string, error) {
		a := stringParam(p, "action")
		actions = append(actions, a)
		if a == "wait" {
			return "", errors.New("expected state did not appear")
		}
		return "ok", nil
	}})
	out, err := tool.Execute(context.Background(), map[string]interface{}{"action": "run", "surface": "browser", "target_id": "example", "observe_after": false, "stop_on_error": false, "steps": []interface{}{
		map[string]interface{}{"operation": "click", "selector": "#next", "expect_text": "Review"},
		map[string]interface{}{"operation": "click", "selector": "#submit"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "click,wait" {
		t.Fatalf("continued after failed state check: %v", actions)
	}
	s := sessionFromResult(t, out)
	if len(s.Steps) != 1 || s.Steps[0].Verification != "failed" || s.Steps[0].Status != "failed" || !strings.Contains(out, `"automatic_replay_allowed": false`) {
		t.Fatalf("lost failure receipt: %s", out)
	}
}

func TestComputerSuccessfulStepAssertionRetainedForReplay(t *testing.T) {
	tool := NewTool(t.TempDir(), nil, Operations{Desktop: func(_ context.Context, p map[string]interface{}) (string, error) { return "Active window: Review", nil }})
	out, err := tool.Execute(context.Background(), map[string]interface{}{"action": "run", "surface": "desktop", "observe_after": false, "steps": []interface{}{map[string]interface{}{"operation": "key", "key": "enter", "expect_window": "Review"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := sessionFromResult(t, out)
	if s.Steps[0].Verification != "passed" || s.Steps[0].Arguments["expect_window"] != "Review" {
		t.Fatalf("assertion not retained: %s", out)
	}
	if len(s.Assertions) != 0 {
		t.Fatal("intermediate condition incorrectly promoted to final condition")
	}
}

func TestComputerDirectCallsSerializeAndWaitingCallCanCancel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	tool := NewTool(t.TempDir(), nil, Operations{Desktop: func(ctx context.Context, _ map[string]interface{}) (string, error) {
		if count.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "ok", nil
	}})
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(context.Background(), map[string]interface{}{"action": "observe", "surface": "desktop"})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := tool.Execute(ctx, map[string]interface{}{"action": "observe", "surface": "desktop"})
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting call not canceled: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal("concurrent backend action escaped serialization")
	}
}

func TestComputerObservationDoesNotAcceptFailedDesktopReceipt(t *testing.T) {
	tool := NewTool(t.TempDir(), nil, Operations{Desktop: func(context.Context, map[string]interface{}) (string, error) {
		return "Status: failed\nError: capture denied", nil
	}})
	out, err := tool.Execute(context.Background(), map[string]interface{}{"action": "observe", "surface": "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	s := sessionFromResult(t, out)
	if s.ObservationValid || s.Status != SessionFailed {
		t.Fatalf("failed observation accepted: %s", out)
	}
}

func TestComputerInvalidContractDoesNotOpenOrMutateTarget(t *testing.T) {
	for _, params := range []map[string]interface{}{
		{"action": "run", "steps": []interface{}{}},
		{"action": "act"},
		{"action": "run", "steps": []interface{}{map[string]interface{}{"operation": "click"}, map[string]interface{}{"operation": "click", "expect_window": "Review"}}},
		{"action": "act", "operation": "click", "expect_text": 123},
	} {
		calls := 0
		backend := func(context.Context, map[string]interface{}) (string, error) {
			calls++
			return "Opened tab example", nil
		}
		tool := NewTool(t.TempDir(), nil, Operations{BrowserNavigate: backend, BrowserPage: backend})
		params["surface"], params["url"] = "browser", "https://example.invalid"
		if _, err := tool.Execute(context.Background(), params); err == nil || calls != 0 {
			t.Fatalf("invalid contract reached backend: %v calls=%d err=%v", params, calls, err)
		}
	}
}
