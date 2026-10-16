// Package gitutil provides small, read-only Git helpers shared by host and tools.
package gitutil

import "os/exec"

// IsRepository reports whether directory is inside a Git work tree.
func IsRepository(directory string) bool {
	command := exec.Command("git", "rev-parse", "--git-dir")
	command.Dir = directory
	return command.Run() == nil
}

// Output runs Git in directory and returns its combined output.
func Output(directory string, arguments ...string) string {
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, _ := command.CombinedOutput()
	return string(output)
}
