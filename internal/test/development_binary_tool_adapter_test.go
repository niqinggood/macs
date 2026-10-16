package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIToolRunsSimpleWorkspaceExecutableWithoutShell(t *testing.T) {
	workspace := t.TempDir()
	path := buildCLIDirectFixture(t, workspace)
	commandPath := "./" + filepath.Base(path)
	if runtime.GOOS == "windows" {
		commandPath = `.\` + filepath.Base(path)
	}
	output, err := NewCLITool(workspace).Execute(context.Background(), map[string]interface{}{
		"command": commandPath + ` "argument with spaces"`,
		"reason":  "verify direct argument-safe execution",
	})
	if err != nil {
		t.Fatalf("direct CLI execution: %v\n%s", err, output)
	}
	if !strings.Contains(output, "ARG=argument with spaces") {
		t.Fatalf("external program did not receive the verbatim argument:\n%s", output)
	}
}

func buildCLIDirectFixture(t *testing.T, workspace string) string {
	t.Helper()
	source := `package main

import (
	"fmt"
	"os"
)

func main() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	fmt.Println("ARG=" + arg)
}
`
	sourcePath := filepath.Join(workspace, "direct-cli-fixture.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(workspace, "direct-cli-fixture")
	if runtime.GOOS == "windows" {
		outputPath += ".exe"
	}
	command := exec.Command("go", "build", "-o", outputPath, sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build direct CLI fixture: %v\n%s", err, output)
	}
	return outputPath
}
