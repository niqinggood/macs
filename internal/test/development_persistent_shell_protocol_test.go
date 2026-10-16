package app

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPersistentShellCapturesOutputAndPropagatesExitCode(t *testing.T) {
	shell, err := NewPersistentShell("protocol-test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Close()

	command := "echo visible-output; false"
	if shell.GetType() == ShellTypePowerShell {
		command = "Write-Output 'visible-output'; & cmd.exe /c exit 7"
	} else if runtime.GOOS == "windows" && shell.GetType() == ShellTypeCmd {
		command = "echo visible-output & exit /b 7"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := shell.Execute(ctx, command)
	if err == nil {
		t.Fatalf("failing command was reported as successful; output=%q", output)
	}
	if !strings.Contains(output, "visible-output") {
		t.Fatalf("command output was lost: %q (err=%v)", output, err)
	}
}

func TestBackgroundShellCommandPreservesQuotedProgrammingCommand(t *testing.T) {
	command := `go test ./... -run "TestName|TestOther"`
	cmd := backgroundShellCommand(command)
	if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != command {
		t.Fatalf("background command was rewritten: args=%q want final arg %q", cmd.Args, command)
	}
}

func TestExternalCodingRunPromptIncludesRepositoryExecutionContract(t *testing.T) {
	prompt := externalCodingRunPrompt("fix the failing test", `E:\project`)
	for _, required := range []string{
		"inspect repository instructions",
		"preserve unrelated user changes",
		"run the narrowest meaningful check first",
		`E:\project`,
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("coding prompt missing %q:\n%s", required, prompt)
		}
	}
}
