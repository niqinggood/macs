package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStepExecutionIDAndTurnKeyAreDeterministic(t *testing.T) {
	fingerprint := StepFingerprint{
		Name: "draft", Model: "writer", MaxIter: 2,
		Tools: []string{"read_file"}, Prompt: "rendered prompt",
	}
	key := TurnCheckpointKey("exec-1", 2, 1, 1, fingerprint)
	if key == "" || key != TurnCheckpointKey("exec-1", 2, 1, 1, fingerprint) {
		t.Fatal("turn checkpoint key is not deterministic")
	}
	changed := fingerprint
	changed.Prompt = "different prompt"
	if key == TurnCheckpointKey("exec-1", 2, 1, 1, changed) {
		t.Fatal("turn checkpoint key ignored the rendered prompt")
	}
	id := StepExecutionID("exec-1", 2, 1, 1, key)
	if id == "" || id != StepExecutionID("exec-1", 2, 1, 1, key) {
		t.Fatal("step execution id is not deterministic")
	}
}

func TestCheckpointNormalizeCloneAndRepeatCounts(t *testing.T) {
	now := time.Now()
	checkpoint := &PipelineCheckpoint{
		PipelineExecID: " exec-1 ",
		Phase:          CheckpointPhaseStepRunning,
		Turns: map[string]*PipelineTurnCheckpoint{
			" key ": {TaskID: " child ", Phase: "success", UpdatedAt: now},
		},
		RepeatCounts: map[string]int{"2": 3},
	}
	if err := checkpoint.Normalize(""); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Version != CheckpointVersion || checkpoint.Revision != 1 || checkpoint.PipelineExecID != "exec-1" {
		t.Fatalf("normalized checkpoint = %#v", checkpoint)
	}
	if checkpoint.Turns["key"] == nil || checkpoint.Turns["key"].TaskID != "child" {
		t.Fatalf("turn keys were not normalized: %#v", checkpoint.Turns)
	}

	clone := CloneCheckpoint(checkpoint)
	clone.Turns["key"].TaskID = "changed"
	clone.RepeatCounts["2"] = 9
	if checkpoint.Turns["key"].TaskID != "child" || checkpoint.RepeatCounts["2"] != 3 {
		t.Fatal("checkpoint clone shares nested mutable state")
	}
	counts := RepeatCountsFromStrings(RepeatCountsToStrings(map[int]int{2: 3, -1: 4, 4: 0}))
	if len(counts) != 1 || counts[2] != 3 {
		t.Fatalf("repeat count round trip = %#v", counts)
	}
}

func TestCheckpointWriteLoadAndCompletedTranscript(t *testing.T) {
	workspace := t.TempDir()
	path := CheckpointPath(workspace, "exec/unsafe")
	checkpoint := &PipelineCheckpoint{
		PipelineExecID: "exec/unsafe",
		Phase:          CheckpointPhaseStepPending,
		Turns:          make(map[string]*PipelineTurnCheckpoint),
	}
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PipelineExecID != checkpoint.PipelineExecID || loaded.Version != CheckpointVersion {
		t.Fatalf("loaded checkpoint = %#v", loaded)
	}

	transcriptPath := filepath.Join(workspace, "transcript.json")
	data, err := json.Marshal(map[string]interface{}{
		"session_id": "turn-1", "status": "success", "final_output": " done ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, status, ok := LoadCompletedTurnTranscript(transcriptPath, "turn-1")
	if !ok || output != "done" || status != "success" {
		t.Fatalf("completed transcript = %q %q %v", output, status, ok)
	}
	if _, _, ok := LoadCompletedTurnTranscript(transcriptPath, "other-turn"); ok {
		t.Fatal("accepted transcript owned by another turn")
	}
}

func TestPruneTurnCheckpointsKeepsActiveAndRunningTurns(t *testing.T) {
	checkpoint := &PipelineCheckpoint{Turns: make(map[string]*PipelineTurnCheckpoint), ActiveTurnKey: "active"}
	base := time.Now().Add(-time.Hour)
	for index := 0; index < pipelineCheckpointMaxTurns+5; index++ {
		key := StepExecutionID("exec", index, 1, 1)
		checkpoint.Turns[key] = &PipelineTurnCheckpoint{Key: key, Phase: "success", UpdatedAt: base.Add(time.Duration(index) * time.Second)}
	}
	checkpoint.Turns["active"] = &PipelineTurnCheckpoint{Key: "active", Phase: "success", UpdatedAt: base}
	checkpoint.Turns["running"] = &PipelineTurnCheckpoint{Key: "running", Phase: "running", UpdatedAt: base}
	PruneTurnCheckpoints(checkpoint)
	if len(checkpoint.Turns) != pipelineCheckpointMaxTurns || checkpoint.Turns["active"] == nil || checkpoint.Turns["running"] == nil {
		t.Fatalf("pruned checkpoint count=%d active=%v running=%v", len(checkpoint.Turns), checkpoint.Turns["active"] != nil, checkpoint.Turns["running"] != nil)
	}
}
