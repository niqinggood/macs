package app

import (
	"strings"
	"testing"
)

func TestDefaultWideResearchPipelineIsParallelAndEvidenceAware(t *testing.T) {
	gw := &GatewayService{}
	pipeline, ok := gw.defaultPipelineSet()["wide-research"]
	if !ok {
		t.Fatal("wide-research should be registered")
	}
	if len(pipeline.Steps) != 4 {
		t.Fatalf("wide-research steps = %d, want 4", len(pipeline.Steps))
	}
	if len(pipeline.Steps[1].Parallel) < 3 {
		t.Fatalf("wide-research should collect evidence in parallel: %#v", pipeline.Steps[1].Parallel)
	}
	combined := ""
	for _, step := range pipeline.Steps {
		combined += step.Prompt + "\n"
		for _, sub := range step.Parallel {
			combined += sub.Prompt + "\n"
		}
	}
	for _, want := range []string{
		"historical session",
		"Deduplicate sources",
		"verified versus inferred",
		".openkitty/storage/wide-research-{timestamp}.md",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("wide-research prompt should contain %q", want)
		}
	}
}
