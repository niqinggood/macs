package pipeline

import (
	"strings"
	"testing"
)

func TestValidateOutputContractConfig(t *testing.T) {
	valid := &PipelineOutputContract{
		MinChars: 10, RequiredSections: []string{"## Summary"},
		ReportedStatusField: "Delivery status", OnViolation: OutputViolationDegraded,
	}
	if err := ValidateOutputContractConfig(valid, "step 0"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*PipelineOutputContract{
		{},
		{MinChars: -1},
		{MinChars: 1, OnViolation: "ignore"},
		{RequiredSections: []string{"Summary", "## summary"}},
	} {
		if err := ValidateOutputContractConfig(invalid, "step 0"); err == nil {
			t.Fatalf("invalid contract accepted: %#v", invalid)
		}
	}
}

func TestApplyOutputContractFailAndDegraded(t *testing.T) {
	failing := &PipelineOutputContract{MinChars: 100, OnViolation: OutputViolationFail}
	if _, degraded, err := ApplyOutputContract(failing, "short"); err == nil || degraded {
		t.Fatalf("fail policy result: degraded=%v err=%v", degraded, err)
	}

	degradedContract := &PipelineOutputContract{
		RequiredSections: []string{"## Evidence", "## Risks"},
		OnViolation:      OutputViolationDegraded,
	}
	output, degraded, err := ApplyOutputContract(degradedContract, "## Evidence\n\nObserved facts")
	if err != nil || !degraded || !strings.Contains(output, "OpenKitty 输出质量门") || !strings.Contains(output, "## Risks") {
		t.Fatalf("degraded policy output=%q degraded=%v err=%v", output, degraded, err)
	}
}

func TestReportedStatusParsingAndDegradedReason(t *testing.T) {
	contract := &PipelineOutputContract{ReportedStatusField: "交付状态", OnViolation: OutputViolationDegraded}
	output := "# Result\n\n交付状态：degraded"
	status, issue := ParseReportedStatus(output, contract.ReportedStatusField)
	if status != "degraded" || issue != "" {
		t.Fatalf("reported status = %q issue=%q", status, issue)
	}
	if reason := OutputContractDegradedReason(contract, output); reason != "reported_status_degraded" {
		t.Fatalf("degraded reason = %q", reason)
	}
	if _, issue := ParseReportedStatus("交付状态: unknown", contract.ReportedStatusField); issue == "" {
		t.Fatal("invalid reported status was accepted")
	}
}
