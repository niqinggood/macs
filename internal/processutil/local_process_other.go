//go:build !windows

package processutil

import "os/exec"

func ConfigureLocalProcess(_ *exec.Cmd) {}
