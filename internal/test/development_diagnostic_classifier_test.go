package app

import (
	"strings"
	"testing"
)

func TestIsModelAPIKeyConfigErrorRequiresFailureEvidence(t *testing.T) {
	for _, text := range []string{
		"API key saved to .openkitty/service_key.txt. Usage: run read.",
		"Use the apiKey field when posting to an external service.",
		"This placeholder text is part of a template example.",
		"HTTP 401 details are not available in this excerpt.",
	} {
		if isModelAPIKeyConfigError(text) {
			t.Fatalf("benign text should not be classified as model API key config error: %q", text)
		}
	}

	for _, text := range []string{
		"api_key missing or placeholder",
		"missing api key",
		"invalid api key",
		"401 unauthorized",
		"authentication failed",
	} {
		if !isModelAPIKeyConfigError(text) {
			t.Fatalf("auth failure text should be classified as API key config error: %q", text)
		}
	}
}

func TestIterationDiagnosticDoesNotTreatGenericToolNamesAsAuthContext(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"",
		13,
		120,
		12,
		nil,
		[]string{"ptc_execute", "web_fetch", "read_file"},
		[]string{
			"ptc_execute: http_status",
			"web_fetch: tool output error: web_fetch",
			"read_file: network",
		},
		map[string]int{
			"ptc_execute:http_status":     1,
			"web_fetch:tool output error": 1,
			"read_file:network":           1,
		},
		nil,
	)
	if strings.Contains(out, "外部服务或工具鉴权失败") || strings.Contains(out, "API Key") {
		t.Fatalf("generic tool/network failures should not be classified as auth/API key: %s", out)
	}
	if !strings.Contains(out, "工具或网络访问异常") {
		t.Fatalf("expected generic tool/network diagnosis, got: %s", out)
	}
}

func TestExtractErrorSignatureClassifiesCompactedOutputBeforeEncoding(t *testing.T) {
	content := "Tool Result\n" +
		"status: failed_compacted\n" +
		"summary: output exceeded 8000 chars\n" +
		"details_file: .openkitty/runtime/tool-results/read_file.log\n" +
		"original_chars: 20559\n" +
		"key_output:\n" +
		"open(path, encoding='utf-8').read()\n"

	if got := extractErrorSignature(content); got != "output_too_large" {
		t.Fatalf("signature = %q, want output_too_large", got)
	}
}

func TestExtractErrorSignatureClassifiesInvalidToolArgs(t *testing.T) {
	content := `Tool call rejected [invalid_args]: missing required parameter "path"`

	if got := extractErrorSignature(content); got != "invalid_args" {
		t.Fatalf("signature = %q, want invalid_args", got)
	}
}

func TestExtractErrorSignatureDoesNotTreatCLIFailureStatusAsHTTP(t *testing.T) {
	content := "Command Execution\n" +
		"status: failed\n" +
		"失败原因: exit status 1\n" +
		"PermissionError: [Errno 13] Permission denied: 'proposal.docx'\n"

	if got := extractErrorSignature(content); got != "permission" {
		t.Fatalf("signature = %q, want permission", got)
	}
}

func TestExtractErrorSignatureStillRecognizesHTTPFailures(t *testing.T) {
	for _, content := range []string{
		"HTTP 403 Forbidden",
		"request returned error: 429",
		"response status code: 503",
	} {
		if got := extractErrorSignature(content); got != "http_status" {
			t.Fatalf("signature for %q = %q, want http_status", content, got)
		}
	}
}
