package development

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBinaryToolGenericGoInspection(t *testing.T) {
	workspace := t.TempDir()
	binaryPath := buildBinaryInspectionFixture(t, workspace, runtime.GOOS, "inspection-fixture")
	tool := NewBinaryTool(workspace)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	analyze, err := tool.Execute(ctx, map[string]interface{}{
		"action": "analyze",
		"file":   filepath.Base(binaryPath),
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	expectedFormat := map[string]string{"windows": "pe", "linux": "elf", "darwin": "macho"}[runtime.GOOS]
	if expectedFormat != "" && !strings.Contains(analyze, "| Format | "+expectedFormat+" |") {
		t.Fatalf("analyze did not identify %s:\n%s", expectedFormat, analyze)
	}
	if !strings.Contains(analyze, "| Arch | "+runtime.GOARCH+" |") {
		t.Fatalf("analyze did not identify %s:\n%s", runtime.GOARCH, analyze)
	}

	printable, err := tool.Execute(ctx, map[string]interface{}{
		"action":     "strings",
		"file":       filepath.Base(binaryPath),
		"contains":   "ORCHID-BINARY-MARKER",
		"min_length": 8,
		"limit":      10,
	})
	if err != nil {
		t.Fatalf("strings: %v", err)
	}
	if !strings.Contains(printable, "ORCHID-BINARY-MARKER-9F2A") {
		t.Fatalf("strings did not find the marker:\n%s", printable)
	}

	symbols, err := tool.Execute(ctx, map[string]interface{}{
		"action":   "symbols",
		"file":     filepath.Base(binaryPath),
		"contains": "main.scorePacket",
		"limit":    10,
	})
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	if !strings.Contains(symbols, "main.scorePacket") {
		t.Fatalf("symbols did not find the fixture function:\n%s", symbols)
	}

	disassembly, err := tool.Execute(ctx, map[string]interface{}{
		"action":    "disassemble",
		"file":      filepath.Base(binaryPath),
		"symbol":    `main\.scorePacket`,
		"max_lines": 160,
	})
	if err != nil {
		t.Fatalf("disassemble: %v", err)
	}
	if !strings.Contains(disassembly, "TEXT main.scorePacket(SB)") ||
		!strings.Contains(disassembly, "Source: go tool objdump") {
		t.Fatalf("unexpected disassembly:\n%s", disassembly)
	}

	runOutput, err := tool.Execute(ctx, map[string]interface{}{
		"action":  "run",
		"file":    filepath.Base(binaryPath),
		"args":    []interface{}{"value with spaces"},
		"timeout": 10,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(runOutput, "Exit code: 0") ||
		!strings.Contains(runOutput, "ARG=value with spaces") {
		t.Fatalf("unexpected direct run output:\n%s", runOutput)
	}
	nonZeroOutput, err := tool.Execute(ctx, map[string]interface{}{
		"action":  "run",
		"file":    filepath.Base(binaryPath),
		"args":    []interface{}{"fail"},
		"timeout": 10,
	})
	if err != nil {
		t.Fatalf("non-zero process exit should remain inspectable evidence: %v", err)
	}
	if !strings.Contains(nonZeroOutput, "Exit code: 7") ||
		!strings.Contains(nonZeroOutput, "EXPECTED FAILURE") {
		t.Fatalf("unexpected non-zero run output:\n%s", nonZeroOutput)
	}
}

func TestBinaryToolDetectsCrossCompiledFormatsWithoutHostUtilities(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles three small Go fixtures")
	}
	workspace := t.TempDir()
	tool := NewBinaryTool(workspace)
	for _, testCase := range []struct {
		goos       string
		format     string
		outputName string
	}{
		{goos: "windows", format: "pe", outputName: "fixture-windows.exe"},
		{goos: "linux", format: "elf", outputName: "fixture-linux"},
		{goos: "darwin", format: "macho", outputName: "fixture-darwin"},
	} {
		t.Run(testCase.goos, func(t *testing.T) {
			path := buildBinaryInspectionFixture(t, workspace, testCase.goos, testCase.outputName)
			output, err := tool.Execute(context.Background(), map[string]interface{}{
				"action": "analyze",
				"file":   filepath.Base(path),
			})
			if err != nil {
				t.Fatalf("analyze %s: %v", testCase.goos, err)
			}
			if !strings.Contains(output, "| Format | "+testCase.format+" |") ||
				!strings.Contains(output, "| Arch | amd64 |") {
				t.Fatalf("unexpected %s analysis:\n%s", testCase.goos, output)
			}
		})
	}
}

func TestBinaryToolExtractsFilteredASCIIAndUTF16Strings(t *testing.T) {
	workspace := t.TempDir()
	payload := []byte{0x00, 0xff, 'G', 'E', 'N', 'E', 'R', 'A', 'L', '_', 'A', 'S', 'C', 'I', 'I', 0x00}
	for _, current := range []byte("GENERAL_UTF16") {
		payload = append(payload, current, 0x00)
	}
	payload = append(payload, 0x00, 0xff)
	if err := os.WriteFile(filepath.Join(workspace, "sample.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := NewBinaryTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":     "strings",
		"file":       "sample.bin",
		"contains":   "general_",
		"min_length": 6,
		"limit":      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"GENERAL_ASCII", "GENERAL_UTF16", "[ascii]", "[utf16le]"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q from string output:\n%s", expected, output)
		}
	}
}

func TestBinaryToolRequiresFocusedDisassemblySymbol(t *testing.T) {
	workspace := t.TempDir()
	path := buildBinaryInspectionFixture(t, workspace, runtime.GOOS, "focus-fixture")
	_, err := NewBinaryTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "disassemble",
		"file":   filepath.Base(path),
	})
	if err == nil || !strings.Contains(err.Error(), "'symbol' is required") {
		t.Fatalf("expected focused-symbol error, got %v", err)
	}
}

func TestAnnotateDisassemblyImmediatesUsesInstructionWidth(t *testing.T) {
	lines := annotateDisassemblyImmediates([]string{
		"main.go:14  0x140001000  MOVL $-0x18ac2b24, 0xc(SP)",
		"main.go:14  0x140001008  MOVQ $0x42984a0d81abf910, DX",
		"main.go:15  0x140001012  ADDQ $0x1, AX",
	})
	if !strings.Contains(lines[0], "imm32=0xe753d4dc bytes_le=dc d4 53 e7") {
		t.Fatalf("negative 32-bit immediate was not normalized correctly: %s", lines[0])
	}
	if !strings.Contains(lines[1], "imm64=0x42984a0d81abf910 bytes_le=10 f9 ab 81 0d 4a 98 42") {
		t.Fatalf("positive 64-bit immediate was not normalized correctly: %s", lines[1])
	}
	if lines[2] != "main.go:15  0x140001012  ADDQ $0x1, AX" {
		t.Fatalf("non-MOV instruction should remain unchanged: %s", lines[2])
	}
}

func buildBinaryInspectionFixture(t *testing.T, workspace, targetOS, outputName string) string {
	t.Helper()
	source := `package main

import (
	"fmt"
	"os"
)

const marker = "ORCHID-BINARY-MARKER-9F2A"

//go:noinline
func scorePacket(value byte) byte {
	return (value ^ 0x5a) + 7
}

func main() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	if arg == "fail" {
		fmt.Fprintln(os.Stderr, "EXPECTED FAILURE")
		os.Exit(7)
	}
	fmt.Println(marker, scorePacket(11), "ARG="+arg)
}
`
	sourcePath := filepath.Join(workspace, outputName+".go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(workspace, outputName)
	if targetOS == "windows" && filepath.Ext(outputPath) == "" {
		outputPath += ".exe"
	}
	command := exec.Command("go", "build", "-gcflags=all=-l", "-o", outputPath, sourcePath)
	command.Env = append(os.Environ(), "GOOS="+targetOS, "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s fixture: %v\n%s", targetOS, err, output)
	}
	return outputPath
}
