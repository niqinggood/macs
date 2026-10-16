//go:build !windows

// Package processutil centralizes platform-specific subprocess lifecycle code.
package processutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func StartTree(cmd *exec.Cmd, cancelOnContext bool) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = 3 * time.Second
	if cancelOnContext {
		cmd.Cancel = func() error { return TerminateTree(cmd.Process) }
	}
	return cmd.Start()
}

func WaitTree(cmd *exec.Cmd) error { return cmd.Wait() }

func RunTree(cmd *exec.Cmd, cancelOnContext bool) error {
	if err := StartTree(cmd, cancelOnContext); err != nil {
		return err
	}
	return WaitTree(cmd)
}

func TerminateTree(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	treeErr := syscall.Kill(-process.Pid, syscall.SIGKILL)
	killErr := process.Kill()
	if treeErr == nil || errors.Is(treeErr, syscall.ESRCH) || killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(treeErr, killErr)
}
