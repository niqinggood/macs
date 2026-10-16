package processutil

import (
	"reflect"
	"testing"
)

func TestParseCommandLinePreservesQuotedArguments(t *testing.T) {
	want := []string{"command", "value with spaces", "single quoted", "plain"}
	got := ParseCommandLine(`command "value with spaces" 'single quoted' plain`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCommandLine() = %#v, want %#v", got, want)
	}
}

func TestParseCommandLineSkipsRepeatedSpaces(t *testing.T) {
	want := []string{"one", "two"}
	got := ParseCommandLine("  one   two  ")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCommandLine() = %#v, want %#v", got, want)
	}
}
