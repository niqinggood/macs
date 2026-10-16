package pipeline

import (
	"strings"
	"testing"
)

func TestValidateReworkConfigEnforcesBoundedEarlierTarget(t *testing.T) {
	valid := []ReworkStepSpec{
		{Name: "collect"},
		{Name: "review", Rework: &StepRework{Target: "collect", MaxReworks: 2, OnExhausted: "degraded"}},
	}
	if err := ValidateReworkConfig(valid); err != nil {
		t.Fatal(err)
	}

	invalid := append([]ReworkStepSpec(nil), valid...)
	invalid[1].Rework = &StepRework{Target: "collect", MaxReworks: 3}
	if err := ValidateReworkConfig(invalid); err == nil || !strings.Contains(err.Error(), "max_reworks") {
		t.Fatalf("invalid rework limit was accepted: %v", err)
	}

	backwardRoute := append([]ReworkStepSpec(nil), valid...)
	backwardRoute[1].Routes = []ReworkRouteSpec{{Goto: "collect"}}
	if err := ValidateReworkConfig(backwardRoute); err == nil || !strings.Contains(err.Error(), "backward routing") {
		t.Fatalf("backward route was accepted: %v", err)
	}
}

func TestReworkControlMustBeStructuredFinalLine(t *testing.T) {
	prompt := AppendReworkReviewInstruction("review", &StepRework{Target: "collect"})
	if !strings.Contains(prompt, ReworkControlPrefix) || !strings.Contains(prompt, "at most once") {
		t.Fatalf("review prompt lacks protocol: %s", prompt)
	}

	control, cleaned, found, err := ParseReworkControl("usable result\n" + ReworkControlPrefix + `{"status":"accepted"}`)
	if err != nil || !found || control.Status != "accepted" || cleaned != "usable result" {
		t.Fatalf("control parse failed: control=%#v cleaned=%q found=%v err=%v", control, cleaned, found, err)
	}
	_, unchanged, found, err := ParseReworkControl(ReworkControlPrefix + `{"status":"retry_required"}` + "\nordinary tail")
	if err != nil || found || !strings.Contains(unchanged, "ordinary tail") {
		t.Fatalf("non-final control changed flow: found=%v unchanged=%q err=%v", found, unchanged, err)
	}
}

func TestPendingReworkContextAddsPresentationPolicy(t *testing.T) {
	request := &PipelineReworkRequest{
		FromStepName: "交付自查", TargetName: "视觉润色", Attempt: 1, Max: 2,
		Reason: "stale preview must be rerendered", PreviousOutput: "deck v1",
	}
	policy := PresentationReworkPolicy(request)
	context := PendingReworkContext(request, policy)
	for _, want := range []string{"attempt 1/2", "freshness-only", "deck v1", "stale preview"} {
		if !strings.Contains(context, want) {
			t.Fatalf("rework context missing %q: %s", want, context)
		}
	}
}

func TestKnownReworkReadOnlyToolsAreExplicit(t *testing.T) {
	if !IsKnownReworkReadOnlyTool("web_search") || !IsKnownReworkReadOnlyTool("READ_FILE") {
		t.Fatal("known read-only research tools were rejected")
	}
	if IsKnownReworkReadOnlyTool("write_file") {
		t.Fatal("mutation tool was classified as read-only")
	}
}
