package app

import "testing"

func TestMalformedDSMLIsNotAcceptedAsFinalAnswer(t *testing.T) {
	tests := []string{
		`<｜DSML｜tool_calls>{"name":"read_file"}`,
		`answer</｜DSML｜tool_calls>`,
		`answer<｜DSML｜tool_calls`,
		`<｜DSML｜tool_calls><｜DSML｜tool_calls></｜DSML｜tool_calls>`,
	}
	for _, input := range tests {
		if got := cleanUserFacingFinalAnswer(input); got != "" {
			t.Fatalf("terminal answer cleaner leaked malformed DSML: %q", got)
		}
		if !isProgressOnlyFinalOutput(input) {
			t.Fatal("malformed DSML must not be accepted as a final answer")
		}
	}
}
