package app

import (
	"strings"
	"testing"
)

func TestContentAndBidPipelinesHaveWaitingInputGates(t *testing.T) {
	pipelines := (&GatewayService{}).defaultPipelineSet()
	for _, name := range []string{"wechat-article", "bid-proposal"} {
		pipeline, ok := pipelines[name]
		if !ok || len(pipeline.Steps) == 0 {
			t.Fatalf("pipeline %s missing", name)
		}
		intake := pipeline.Steps[0]
		if !strings.Contains(intake.Prompt, pipelineWaitingInputMarker) || !strings.Contains(intake.Prompt, pipelineStopMarker) {
			t.Fatalf("pipeline %s intake lacks waiting-input protocol", name)
		}
		if len(intake.Routing) != 1 || intake.Routing[0].Goto != "__stop__" {
			t.Fatalf("pipeline %s intake lacks normal-stop route: %#v", name, intake.Routing)
		}
		if !intake.AllowMissingInput {
			t.Fatalf("pipeline %s intake must explicitly authorize a late clarification stop", name)
		}
	}
}

func TestBidPipelineArtifactsAreExecutionScoped(t *testing.T) {
	pipeline := (&GatewayService{}).defaultPipelineSet()["bid-proposal"]
	if len(pipeline.Steps) != 4 {
		t.Fatalf("bid pipeline steps = %d, want 4", len(pipeline.Steps))
	}
	for i := 1; i < len(pipeline.Steps); i++ {
		contract := pipeline.Steps[i].ArtifactContract
		if contract == nil || len(contract.Paths) != 1 || !strings.Contains(contract.Paths[0], "${execution_id}") {
			t.Fatalf("bid step %d artifact must be execution-scoped: %#v", i, contract)
		}
	}
}

func TestPipelineWaitingInputUsesDistinctCompletionReason(t *testing.T) {
	waitingOutput := "请补充文件。\n" + pipelineWaitingInputMarker + "\n" + pipelineStopMarker
	code, _ := pipelineControlStopReason(waitingOutput)
	if code != aeWaitingInputStatus {
		t.Fatalf("waiting-input completion code = %q, want %q", code, aeWaitingInputStatus)
	}
	code, _ = pipelineControlStopReason("已完成。\n" + pipelineStopMarker)
	if code != "control_stop" {
		t.Fatalf("ordinary stop completion code = %q", code)
	}
}
