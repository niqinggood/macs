package runtimeutil

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		min  int
		max  int
	}{
		{name: "English", text: "The quick brown fox jumps over the lazy dog", min: 8, max: 20},
		{name: "Chinese", text: "今天天气很好适合出去玩", min: 6, max: 16},
		{name: "Code", text: `func main() { fmt.Println("hello") }`, min: 5, max: 30},
		{name: "Mixed", text: "Hello 你好 world 世界! How are you?", min: 5, max: 25},
		{name: "Long", text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100), min: 800, max: 1600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EstimateTokens(test.text)
			if got < test.min || got > test.max {
				t.Fatalf("EstimateTokens() = %d, want range [%d, %d]", got, test.min, test.max)
			}
		})
	}
}

func TestEstimateTokensEmpty(t *testing.T) {
	if got := EstimateTokens(""); got != 1 {
		t.Fatalf("EstimateTokens(empty) = %d, want 1", got)
	}
}
