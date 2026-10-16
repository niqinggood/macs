package toolregistry

import "testing"

func TestWorkflowLearnerIsDeferredForToolSearch(t *testing.T) {
	if CoreTools["workflow_learner"] {
		t.Fatal("workflow_learner should not spend core prompt budget")
	}
	if !PromptHiddenTools["workflow_learner"] {
		t.Fatal("workflow_learner must remain discoverable through tool_detail")
	}
}
