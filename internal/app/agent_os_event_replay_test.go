package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type agentOSEventReplayRecorder struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
}

func newAgentOSEventReplayRecorder() *agentOSEventReplayRecorder {
	return &agentOSEventReplayRecorder{header: make(http.Header)}
}

func (recorder *agentOSEventReplayRecorder) Header() http.Header { return recorder.header }
func (recorder *agentOSEventReplayRecorder) WriteHeader(int)     {}
func (recorder *agentOSEventReplayRecorder) Flush()              {}
func (recorder *agentOSEventReplayRecorder) Write(data []byte) (int, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.body.Write(data)
}
func (recorder *agentOSEventReplayRecorder) String() string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.body.String()
}

func TestAgentOSEventJournalAssignsReplayableIDs(t *testing.T) {
	t.Parallel()
	gateway := &GatewayService{}
	gateway.initSSE()
	first := gateway.recordAgentOSEvent(Event{
		Type: "progress", TaskID: "agt-replay",
		Data: map[string]interface{}{"message": "step one"},
	})
	second := gateway.recordAgentOSEvent(Event{
		Type: "progress", TaskID: "agt-replay",
		Data: map[string]interface{}{"message": "step two"},
	})
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("event IDs = %q, %q", first.ID, second.ID)
	}
	replay := gateway.agentOSEvents.After("agt-replay", first.ID)
	if replay.Reset || replay.Gap || len(replay.Records) != 1 || replay.Records[0].ID != second.ID {
		t.Fatalf("replay = %#v", replay)
	}
}

func TestAgentOSEventReplayPreservesApprovalRedaction(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"task_id":"agt-approval","status":"waiting_approval","result":{"arguments":{"secret":"hidden"}}}`)
	redacted := agentOSEventDataForApprovalAccess(raw, false)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["result"] != nil {
		t.Fatalf("result was not redacted: %s", encoded)
	}
	visible := agentOSEventDataForApprovalAccess(raw, true)
	if string(visible.(json.RawMessage)) != string(raw) {
		t.Fatalf("authorized payload changed: %s", visible)
	}
}

func TestAgentOSTaskEventsReplaysAfterLastEventID(t *testing.T) {
	t.Parallel()
	gateway := &GatewayService{}
	gateway.initSSE()
	first := gateway.recordAgentOSEvent(Event{
		Type: "progress", TaskID: "agt-http-replay",
		Data: map[string]interface{}{"message": "step one"},
	})
	second := gateway.recordAgentOSEvent(Event{
		Type: "progress", TaskID: "agt-http-replay",
		Data: map[string]interface{}{"message": "step two"},
	})

	ctx, cancel := context.WithCancel(withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: defaultExecutionTenantID, ServiceID: "replay-test", ControlPlane: true,
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks/agt-http-replay/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", first.ID)
	recorder := newAgentOSEventReplayRecorder()
	done := make(chan struct{})
	go func() {
		gateway.handleAgentOSTaskEvents(recorder, request, "agt-http-replay")
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for !strings.Contains(recorder.String(), "id: "+second.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE replay handler did not stop")
	}
	stream := recorder.String()
	if !strings.Contains(stream, `"message":"step two"`) || strings.Contains(stream, `"message":"step one"`) {
		t.Fatalf("unexpected replay stream: %s", stream)
	}
	if !strings.Contains(stream, `"replay_supported":true`) {
		t.Fatalf("connected event did not advertise replay: %s", stream)
	}
}
