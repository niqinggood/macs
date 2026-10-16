// Package buildinfo formats macs build and runtime metadata.
package buildinfo

import (
	"fmt"
	"runtime"
)

// VersionRequested reports whether args contain a supported version flag.
func VersionRequested(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--version", "-v":
			return true
		}
	}
	return false
}

// RuntimeVersionLine returns the single-line version shown by the CLI.
func RuntimeVersionLine(version, build string) string {
	return fmt.Sprintf("macs %s (build %s, %s/%s)", version, build, runtime.GOOS, runtime.GOARCH)
}
