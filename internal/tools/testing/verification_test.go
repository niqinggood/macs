package testingtools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGoalVerifyProcessHelper(t *testing.T) {
	switch os.Getenv("MACS_VERIFICATION_HELPER") {
	case "failure":
		fmt.Fprintln(os.Stderr, "acceptance assertion failed")
		os.Exit(7)
	case "pass":
		fmt.Println("Ran 2 tests in 0.01s\nOK")
		os.Exit(0)
	case "empty":
		fmt.Println("Ran 0 tests in 0.00s\nOK")
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func verificationHelperCommand(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv("MACS_VERIFICATION_HELPER", mode)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return "& '" + strings.ReplaceAll(executable, "'", "''") + "' '-test.run=^TestGoalVerifyProcessHelper$'"
	}
	return "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' '-test.run=^TestGoalVerifyProcessHelper$'"
}

func TestGoalVerifyCommandsRequireEvidenceAndPreserveFailures(t *testing.T) {
	tool := NewGoalVerifyTool(t.TempDir())
	for _, tc := range []struct{ mode, want string }{{"failure", "FAIL"}, {"pass", "PASS"}, {"empty", "PARTIAL"}} {
		t.Run(tc.mode, func(t *testing.T) {
			r := tool.verifyTests(context.Background(), "", verificationHelperCommand(t, tc.mode))
			if r.Status != tc.want {
				t.Fatalf("result: %#v", r)
			}
			if tc.mode == "failure" && (!strings.Contains(r.Evidence, "acceptance assertion failed") || r.Error == "") {
				t.Fatalf("failure lost its evidence: %#v", r)
			}
		})
	}
	// Lint used to turn every failure into a pass, including a missing executable.
	if r := tool.verifyCommand(context.Background(), "Lint", "macs_nonexistent_linter_82649"); r.Status != "FAIL" {
		t.Fatalf("missing linter passed: %#v", r)
	}
	if r := tool.verifyTests(context.Background(), "Go", "go test ./... || true"); r.Status != "PARTIAL" {
		t.Fatalf("masked test result: %#v", r)
	}
	if r := tool.verifyCommand(context.Background(), "Behavior", "exit 7 || true"); r.Status != "PARTIAL" {
		t.Fatalf("custom script masked failure: %#v", r)
	}
	for _, lang := range []string{"Go", "Python", "Node", "Rust"} {
		for _, command := range []string{defaultTestCmd(lang), defaultBuildCmd(lang), defaultLintCmd(lang)} {
			if strings.Contains(command, "||") || strings.Contains(command, "/dev/null") {
				t.Fatalf("default masks a failure: %s", command)
			}
		}
	}
}

func TestGoalVerifyEmptyAndMixedTestSuites(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"", false}, {"all good", false}, {"Ran 0 tests\nOK", false},
		{"? pkg [no test files]", false}, {"ok pkg 0.2s [no tests to run]", false},
		{"test result: ok. 0 passed; 0 failed", false}, {"Tests: 0 passed", false},
		{"? pkg [no test files]\nok tested 0.2s", true},
		{"test result: ok. 2 passed; 0 failed\ntest result: ok. 0 passed; 0 failed", true},
		{"Tests: 2 passed, 2 total", true}, {"# pass 3", true}, {"3 passing (7ms)", true},
	} {
		if got := hasExecutedTestEvidence(tc.output); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.output, got, tc.want)
		}
	}
}

func TestGoalVerifyUnavailableChecksAndFilesDoNotProveBehavior(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "result.txt"), []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewGoalVerifyTool(workspace)
	for _, check := range []string{"tests", "build", "lint", "behavior", "all"} {
		out, err := tool.Execute(context.Background(), map[string]interface{}{"goal": "meet acceptance criteria", "check": check, "files": "result.txt"})
		if err != nil || !strings.Contains(out, "VERDICT: PARTIAL") || strings.Contains(out, "Task is complete") {
			t.Fatalf("%s: %s %v", check, out, err)
		}
	}
	if out, _ := tool.Execute(context.Background(), map[string]interface{}{"goal": "x", "check": "typo"}); !strings.Contains(out, "Error:") {
		t.Fatalf("invalid check accepted: %s", out)
	}
}

func TestGoalVerifyFileBoundary(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewGoalVerifyTool(workspace)
	for _, path := range []string{"../private.txt", outside, "."} {
		if r := tool.verifyFiles(path); r.Status != "FAIL" {
			t.Fatalf("outside path or directory accepted: %s %#v", path, r)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(workspace, "linked.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if r := tool.verifyFiles("linked.txt"); r.Status != "FAIL" {
			t.Fatalf("symlink escape accepted: %#v", r)
		}
	})
}

func TestGoalVerifyDirtyGitIsInformational(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	workspace := t.TempDir()
	if out, err := RunCommandWithContext(context.Background(), workspace, "git init", 10*time.Second, nil); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "change.txt"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewGoalVerifyTool(workspace).verifyGitStatus(context.Background())
	if r.Status != "INFO" || !strings.Contains(r.Evidence, "change.txt") {
		t.Fatalf("dirty status: %#v", r)
	}
	out := formatGoalVerification("build", []verifyResult{{Check: "Build", Status: "PASS"}, r})
	if !strings.Contains(out, "VERDICT: PASS") {
		t.Fatalf("dirty tree affected verdict: %s", out)
	}
}

func TestVerificationCommandsHonorCancellationAndTimeout(t *testing.T) {
	command := verificationHelperCommand(t, "sleep")
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		started := time.Now()
		_, err := RunCommandWithContext(ctx, t.TempDir(), command, 800*time.Millisecond, nil)
		cancel()
		want := context.DeadlineExceeded
		if canceled {
			want = context.Canceled
		}
		if !errors.Is(err, want) || time.Since(started) > 8*time.Second {
			t.Fatalf("canceled=%v: %v, took %s", canceled, err, time.Since(started))
		}
	}
}

func TestVerificationCommandOutputIsBounded(t *testing.T) {
	var b commandOutputBuffer
	large := []byte(strings.Repeat("x", commandOutputLimit+100))
	// io.Copy must not bypass the cap through a promoted bytes.Buffer.ReadFrom.
	if n, err := io.Copy(&b, io.LimitReader(strings.NewReader(string(large)), int64(len(large)))); n != int64(len(large)) || err != nil {
		t.Fatalf("write: %d %v", n, err)
	}
	_, _ = b.Write([]byte("test summary"))
	if b.Len() > commandOutputLimit || !strings.HasSuffix(b.String(), "test summary") || !strings.Contains(b.String(), "omitted") {
		t.Fatal("bounded output lost tail or exceeded limit")
	}
}
