//go:build windows

package processutil

import (
	"os/exec"
	"syscall"
)

func ConfigureLocalProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
