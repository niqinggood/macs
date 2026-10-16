package app

import (
	"strings"
	"testing"
)

func verificationResult(command, output string, succeeded bool) string {
	return toolResultForFinalization(ToolCall{
		ID:   command,
		Name: "cli",
		Arguments: map[string]interface{}{
			"command": command,
		},
	}, output, succeeded)
}

func TestVerificationGateDoesNotLetDifferentPassingSubsetHideFailure(t *testing.T) {
	input := "基于现有服务加入审计功能，补测试并运行完整回归"
	results := []string{
		verificationResult("cd runs/C05/service && python -m unittest test_inventory -v", "FAILED (failures=1)", false),
		verificationResult("cd runs/C05/service && python -m unittest test_audited -v", "Ran 18 tests\nOK", true),
	}
	issues := unresolvedVerificationIssues(input, results)
	if len(issues) != 1 || !strings.Contains(issues[0], "test_inventory") {
		t.Fatalf("different passing subset resolved the failed suite: %v", issues)
	}

	results = append(results, verificationResult("cd runs/C05/service && python -m unittest -v", "Ran 21 tests\nOK", true))
	if issues := unresolvedVerificationIssues(input, results); len(issues) != 0 {
		t.Fatalf("broader successful suite did not resolve targeted failure: %v", issues)
	}
}

func TestVerificationGateRejectsZeroTestAndMaskedSuccess(t *testing.T) {
	input := "修复服务并运行完整测试"
	failed := verificationResult("cd service && python -m unittest test_inventory -v", "FAILED", false)
	zero := verificationResult("cd service && python -m unittest -v", "Ran 0 tests in 0.000s\nOK", true)
	masked := verificationResult("cd service && python -m unittest -v 2>&1 | tail -20", "Ran 21 tests\nOK", true)
	for _, results := range [][]string{{failed, zero}, {failed, masked}} {
		if issues := unresolvedVerificationIssues(input, results); len(issues) != 1 {
			t.Fatalf("untrustworthy success closed a real test failure: %v", issues)
		}
	}
}

func TestVerificationGateAllowsDiagnosisOnlyFailureReport(t *testing.T) {
	input := "只诊断这个并发测试为什么失败，不要修改代码"
	results := []string{verificationResult("python -m unittest test_inventory -v", "FAILED", false)}
	if issues := unresolvedVerificationIssues(input, results); len(issues) != 0 {
		t.Fatalf("diagnosis-only task was forced to fix its subject: %v", issues)
	}
}

func TestVerificationRecordKeepsScopeWithoutRawCommand(t *testing.T) {
	stored := verificationResult("cd runs/C02/service && python test_service.py", "13 tests passed", true)
	record, ok := parseOpenKittyToolRecord(stored)
	if !ok || record.VerificationFamily != "python-test-script" || record.VerificationTarget != "test_service.py" || record.VerificationRoot != "runs/c02/service" {
		t.Fatalf("verification provenance was not recorded: %#v ok=%v", record, ok)
	}
}
