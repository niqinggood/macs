package development

import (
	"os"
	"strings"
	"testing"
)

func TestAstToolComplexity(t *testing.T) {
	src := `package main

func simple() { return }

func medium(x int) string {
	if x > 0 {
		if x > 10 {
			return "big"
		}
		return "small"
	}
	for i := 0; i < x; i++ {
		if i%2 == 0 {
			continue
		}
	}
	return "zero"
}

func complex(x int) string {
	switch {
	case x < 0:
		return "negative"
	case x == 0:
		return "zero"
	case x < 10:
		if x%2 == 0 {
			return "small-even"
		}
		return "small-odd"
	case x < 100:
		for i := 0; i < x; i++ {
			if i%3 == 0 {
				if i%5 == 0 {
					return "fizzbuzz"
				}
			}
		}
	}
	return "unknown"
}
`
	tool := NewAstTool(".")
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":  "complexity",
		"content": src,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "simple") {
		t.Error("missing simple function")
	}
	if !strings.Contains(result, "medium") {
		t.Error("missing medium function")
	}
	if !strings.Contains(result, "complex") {
		t.Error("missing complex function")
	}
	// complex should have higher complexity than simple
	t.Logf("Complexity analysis:\n%s", result)
}

func TestAstToolFunctions(t *testing.T) {
	src := `package test

// Greet returns a greeting message.
func Greet(name string) string {
	return "Hello, " + name
}

// Add sums two integers.
func Add(a, b int) int { return a + b }
`
	tool := NewAstTool(".")
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":  "functions",
		"content": src,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Greet") {
		t.Error("missing Greet function")
	}
	if !strings.Contains(result, "Add") {
		t.Error("missing Add function")
	}
	if !strings.Contains(result, "exported") {
		t.Error("should mark exported functions")
	}
	t.Logf("Function analysis:\n%s", result)
}

func TestAstToolSymbols(t *testing.T) {
	src := `package demo

import "fmt"

const MaxRetries = 3

var DefaultTimeout = 30

type Config struct {
	Host string
	Port int
}

type Runner interface {
	Run() error
}

func NewConfig() *Config { return &Config{} }
`
	tool := NewAstTool(".")
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":  "symbols",
		"content": src,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"MaxRetries", "DefaultTimeout", "Config", "Runner", "NewConfig"} {
		if !strings.Contains(result, name) {
			t.Errorf("missing symbol: %s", name)
		}
	}
	t.Logf("Symbol analysis:\n%s", result)
}

func TestAstToolSemanticDiff(t *testing.T) {
	old := `package demo

func Greet(name string) string { return "Hello, " + name }

func Add(a, b int) int { return a + b }
`
	new := `package demo

import "fmt"

func Greet(name string) string { return "Hi, " + name + "!" }

func Multiply(a, b int) int { return a * b }
`
	tool := NewAstTool(".")
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":   "semantic_diff",
		"content":  old,
		"content2": new,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Removed") || !strings.Contains(result, "Add") {
		t.Error("should report Add as removed")
	}
	if !strings.Contains(result, "Added") || !strings.Contains(result, "Multiply") {
		t.Error("should report Multiply as added")
	}
	// Greet signature (func Greet(name string) string) is the same in both versions
	// so no signature_changed expected — bodies changed but not signatures
	t.Logf("Semantic diff:\n%s", result)
}

func TestAstToolDeadCode(t *testing.T) {
	src := `package demo

// UsedFunc is called by main
func UsedFunc() { doWork() }

// UnusedFunc is never called internally
func UnusedFunc() {}

func doWork() { UsedFunc() }

func main() { UsedFunc() }
`
	tool := NewAstTool(".")
	result, err := tool.Execute(nil, map[string]interface{}{
		"action":  "dead_code",
		"content": src,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "UnusedFunc") {
		t.Error("should flag UnusedFunc as potentially dead")
	}
	// doWork is called by UsedFunc, UsedFunc is exported so it passes
	t.Logf("Dead code analysis:\n%s", result)
}

func TestRefactorToolGoAST(t *testing.T) {
	src := `package test

// ComplexFunc has high cyclomatic complexity
func ComplexFunc(x int) string {
	switch {
	case x < 0:
		return "neg"
	case x == 0:
		return "zero"
	case x < 10:
		if x%2 == 0 {
			if x%3 == 0 {
				return "div6"
			}
		}
		return "small"
	case x < 100:
		for i := 0; i < x; i++ {
			if i%2 == 0 {
				continue
			}
		}
	}
	if x > 100 {
		if x > 200 {
			return "huge"
		}
	}
	return "unknown"
}
`
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"
	if err := writeTestFile(tmpFile, src); err != nil {
		t.Fatal(err)
	}

	tool := NewRefactorTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"file":  tmpFile,
		"check": "all",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Refactor analysis:\n%s", result)
	if !strings.Contains(result, "ComplexFunc") {
		t.Error("should analyze ComplexFunc")
	}
}

func TestRefactorToolNonGo(t *testing.T) {
	src := "def hello(name):\n    return \"Hello, \" + name\n\ndef long_function():\n    a = 1\n    b = 2\n    return a + b\n"
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.py"
	if err := writeTestFile(tmpFile, src); err != nil {
		t.Fatal(err)
	}

	tool := NewRefactorTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"file":  tmpFile,
		"check": "all",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "heuristic") {
		t.Error("should use heuristic for Python files")
	}
	t.Logf("Python refactor:\n%s", result)
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
