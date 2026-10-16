package app

import "testing"

func TestUpdateEngineCollaborationUsesAndRestoresRequestRuntimeSettings(t *testing.T) {
	engine := &MultiAgentEngine{
		config: &Config{
			Task: Task{
				Description: "original description",
				Input:       "original input",
			},
			Collaboration: CollaborationConfig{
				Type:            GroupChat,
				MaxRound:        3,
				FailureStrategy: Stop,
			},
		},
		maxRound:        3,
		failureStrategy: Stop,
	}
	gateway := &GatewayService{engine: engine}
	requestConfig := CollaborationConfig{
		Type:            GroupChat,
		MaxRound:        1,
		FailureStrategy: Continue,
	}

	restore := gateway.updateEngineCollaboration(requestConfig, "current input")

	if engine.maxRound != 1 {
		t.Fatalf("runtime max round = %d, want 1", engine.maxRound)
	}
	if engine.failureStrategy != Continue {
		t.Fatalf("runtime failure strategy = %q, want %q", engine.failureStrategy, Continue)
	}
	if engine.config.Collaboration.MaxRound != 1 {
		t.Fatalf(
			"config max round = %d, want 1",
			engine.config.Collaboration.MaxRound,
		)
	}
	if engine.config.Task.Input != "current input" {
		t.Fatalf("task input = %q, want current input", engine.config.Task.Input)
	}

	restore()

	if engine.maxRound != 3 {
		t.Fatalf("restored runtime max round = %d, want 3", engine.maxRound)
	}
	if engine.failureStrategy != Stop {
		t.Fatalf("restored failure strategy = %q, want %q", engine.failureStrategy, Stop)
	}
	if engine.config.Collaboration.MaxRound != 3 {
		t.Fatalf(
			"restored config max round = %d, want 3",
			engine.config.Collaboration.MaxRound,
		)
	}
	if engine.config.Task.Input != "original input" {
		t.Fatalf("restored task input = %q, want original input", engine.config.Task.Input)
	}
}
