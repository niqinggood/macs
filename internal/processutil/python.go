package processutil

import (
	"context"
	"fmt"
	"os/exec"
)

// ResolvePythonCommand returns the first available Python 3-compatible
// launcher together with any launcher-specific prefix arguments.
func ResolvePythonCommand() (string, []string) {
	return ResolvePythonCommandWithLookPath(exec.LookPath)
}

func ResolvePythonCommandWithLookPath(lookPath func(string) (string, error)) (string, []string) {
	candidates := []struct {
		name string
		args []string
	}{
		{name: "python3"},
		{name: "python"},
		{name: "py", args: []string{"-3"}},
	}
	for _, candidate := range candidates {
		if _, err := lookPath(candidate.name); err == nil {
			return candidate.name, append([]string{}, candidate.args...)
		}
	}
	return "python3", nil
}

func PythonCommandContext(ctx context.Context, args ...string) (*exec.Cmd, error) {
	name, prefixArgs := ResolvePythonCommand()
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("python 3 command not found: %w", err)
	}
	commandArgs := append(append([]string{}, prefixArgs...), args...)
	return exec.CommandContext(ctx, path, commandArgs...), nil
}
