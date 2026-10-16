package processutil

import (
	"context"
	"os/exec"
	"runtime"
)

// ShellCommand creates a command using the platform's native non-interactive shell.
func ShellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
