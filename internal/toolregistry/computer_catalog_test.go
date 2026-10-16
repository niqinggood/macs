package toolregistry

import "testing"

func TestComputerIsACoreTool(t *testing.T) {
	if !CoreTools["computer"] {
		t.Fatal("computer must remain a core tool so agents can enter an observe-act-verify loop without discovery")
	}
	if PromptHiddenTools["computer"] {
		t.Fatal("computer must not be prompt-hidden")
	}
}
