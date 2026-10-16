package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersionRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long", args: []string{"--version"}, want: true},
		{name: "short", args: []string{"-v"}, want: true},
		{name: "among other args", args: []string{"serve", "--version"}, want: true},
		{name: "missing", args: []string{"serve"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := VersionRequested(test.args); got != test.want {
				t.Fatalf("VersionRequested(%q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestRuntimeVersionLine(t *testing.T) {
	line := RuntimeVersionLine("1.2.3", "abc123")
	for _, expected := range []string{"macs 1.2.3", "build abc123", runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(line, expected) {
			t.Fatalf("RuntimeVersionLine() = %q, missing %q", line, expected)
		}
	}
}
