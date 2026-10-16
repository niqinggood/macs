package development

import (
	"os"
	"strings"
	"testing"
)

func TestGoTestGenTableDriven(t *testing.T) {
	src := `package demo

// Greet returns a greeting.
func Greet(name string) string {
	return "Hello, " + name
}

// Add sums two integers.
func Add(a, b int) int {
	return a + b
}

// Divide performs division and returns an error if dividing by zero.
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, ErrDivideByZero
	}
	return a / b, nil
}
`
	tmpDir := t.TempDir()
	srcFile := tmpDir + "/demo.go"
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewTestGenTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "generate",
		"file":   srcFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Generated test:\n%s", result)

	// Verify output
	if !strings.Contains(result, "TestGreet") {
		t.Error("should generate TestGreet")
	}
	if !strings.Contains(result, "TestAdd") {
		t.Error("should generate TestAdd")
	}
	if !strings.Contains(result, "TestDivide") {
		t.Error("should generate TestDivide")
	}
	if !strings.Contains(result, "description") || !strings.Contains(result, "testCase") {
		t.Error("should generate table-driven test pattern with description field")
	}
	if !strings.Contains(result, "t.Run") {
		t.Error("should use t.Run for subtests")
	}

	// Verify test file was created
	testFile := tmpDir + "/demo_test.go"
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("test file should be created at demo_test.go")
	}
}

func TestGoTestGenSuggest(t *testing.T) {
	src := `package demo

func ProcessItems(items []string, maxItems int) ([]string, error) {
	return items, nil
}
`
	tmpDir := t.TempDir()
	srcFile := tmpDir + "/demo.go"
	os.WriteFile(srcFile, []byte(src), 0644)

	tool := NewTestGenTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "suggest",
		"file":   srcFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Suggestions:\n%s", result)

	if !strings.Contains(result, "ProcessItems") {
		t.Error("should suggest tests for ProcessItems")
	}
	if !strings.Contains(result, "Nil slice") || !strings.Contains(result, "Empty slice") {
		t.Error("should suggest edge cases for slice params")
	}
}

func TestGoTestGenSingleFunction(t *testing.T) {
	src := `package demo

func Greet(name string) string { return "Hello" }

func Farewell(name string) string { return "Goodbye" }
`
	tmpDir := t.TempDir()
	srcFile := tmpDir + "/demo.go"
	os.WriteFile(srcFile, []byte(src), 0644)

	tool := NewTestGenTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":   "generate",
		"file":     srcFile,
		"function": "Greet",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "TestGreet") {
		t.Error("should generate TestGreet")
	}
	if strings.Contains(result, "TestFarewell") {
		t.Error("should NOT generate TestFarewell (not targeted)")
	}
}

func TestPythonTestGenSuggest(t *testing.T) {
	src := "def calculate_total(items, tax_rate):\n    return sum(items) * (1 + tax_rate)\n"
	tmpDir := t.TempDir()
	srcFile := tmpDir + "/calc.py"
	os.WriteFile(srcFile, []byte(src), 0644)

	tool := NewTestGenTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "suggest",
		"file":   srcFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "calculate_total") {
		t.Error("should suggest tests for calculate_total")
	}
}

func TestJSTestGen(t *testing.T) {
	src := "function sum(a, b) {\n  return a + b;\n}\n\nconst multiply = (a, b) => a * b;\n"
	tmpDir := t.TempDir()
	srcFile := tmpDir + "/math.js"
	os.WriteFile(srcFile, []byte(src), 0644)

	tool := NewTestGenTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "generate",
		"file":   srcFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "describe") {
		t.Error("should generate Jest describe blocks")
	}
	if !strings.Contains(result, "test(") {
		t.Error("should generate test() calls")
	}

	// Verify test file was created
	testFile := tmpDir + "/math.test.js"
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("test file should be created at math.test.js")
	}
}
