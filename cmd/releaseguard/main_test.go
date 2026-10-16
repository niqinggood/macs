package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScanTextRejectsRemoteLiteralCredentialAndPublicHost(t *testing.T) {
	text := "ssh.connect(\"8.8.8.8\", username=\"administrator\", pass" + "word=\"live-value\")"
	findings := scanText("builtin/devops/example.md", []byte(text))
	if len(findings) < 2 {
		t.Fatalf("expected credential and public-host findings, got %#v", findings)
	}
}

func TestScanTextAllowsParameterizedRemoteExample(t *testing.T) {
	text := "remote(action=\"deploy\", host=\"<host>\", username=\"<ssh-user>\")"
	if findings := scanText("builtin/devops/example.md", []byte(text)); len(findings) != 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
}

func TestDocumentationAndPrivateAddressesAreNotPublic(t *testing.T) {
	for _, value := range []string{"10.0.0.1", "192.168.1.2", "198.51.100.7", "203.0.113.9"} {
		if isPublicIPv4(value) {
			t.Fatalf("expected %s to be non-public for release scanning", value)
		}
	}
	if !isPublicIPv4("8.8.8.8") {
		t.Fatal("expected public address to be detected")
	}
}

func TestPlaceholderRecognition(t *testing.T) {
	for _, value := range []string{"<secret-store-reference>", "${OPENKITTY_TOKEN}", "YOUR_API_KEY", "not-a-real-secret"} {
		if !isPlaceholder(value) {
			t.Fatalf("expected placeholder: %s", value)
		}
	}
}

func TestScanTextAllowsSyntheticSecretShapesInTests(t *testing.T) {
	text := "const fake = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890\"\n-----BEGIN PRIVATE KEY-----"
	if findings := scanText("security_guard_test.go", []byte(text)); len(findings) != 0 {
		t.Fatalf("unexpected findings for synthetic test data: %#v", findings)
	}
}

func TestTrackedRuntimeConfigFindingsRejectsSignedLicense(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "licenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "licenses", "default.lic"), []byte("signed-license"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "licenses/default.lic"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, output)
		}
	}
	if findings := trackedRuntimeConfigFindings(root); len(findings) != 1 {
		t.Fatalf("expected one license finding, got %#v", findings)
	}
}
