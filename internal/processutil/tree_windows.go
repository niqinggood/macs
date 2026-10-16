//go:build windows

// Package processutil centralizes platform-specific subprocess lifecycle code.
package processutil

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

type windowsCommandTree struct {
	mu       sync.Mutex
	job      windows.Handle
	process  *os.Process
	assigned bool
	closed   bool
}

var windowsCommandTrees sync.Map

func newWindowsCommandTree() *windowsCommandTree {
	tree := &windowsCommandTree{}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return tree
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return tree
	}
	tree.job = job
	return tree
}

func (tree *windowsCommandTree) assign(process *os.Process) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.process = process
	if tree.closed || tree.job == 0 || process == nil {
		return
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(tree.job, handle); err == nil {
		tree.assigned = true
	}
}

func (tree *windowsCommandTree) terminate() error {
	tree.mu.Lock()
	if tree.closed {
		tree.mu.Unlock()
		return nil
	}
	tree.closed = true
	job := tree.job
	process := tree.process
	assigned := tree.assigned
	tree.job = 0
	tree.mu.Unlock()

	var jobErr error
	if job != 0 {
		if assigned {
			jobErr = windows.TerminateJobObject(job, 1)
		}
		closeErr := windows.CloseHandle(job)
		if jobErr == nil {
			jobErr = closeErr
		}
	}
	if assigned && jobErr == nil {
		return nil
	}
	fallbackErr := killWindowsProcessTree(process)
	if jobErr == nil || fallbackErr == nil {
		return nil
	}
	return errors.Join(jobErr, fallbackErr)
}

func configureWindowsCommand(cmd *exec.Cmd, tree *windowsCommandTree, cancelOnContext bool) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup | createNoWindow
	cmd.WaitDelay = 3 * time.Second
	if cancelOnContext {
		cmd.Cancel = tree.terminate
	}
}

func StartTree(cmd *exec.Cmd, cancelOnContext bool) error {
	tree := newWindowsCommandTree()
	configureWindowsCommand(cmd, tree, cancelOnContext)
	if err := cmd.Start(); err != nil {
		_ = tree.terminate()
		return err
	}
	tree.assign(cmd.Process)
	windowsCommandTrees.Store(cmd.Process.Pid, tree)
	return nil
}

func WaitTree(cmd *exec.Cmd) error {
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	err := cmd.Wait()
	if value, ok := windowsCommandTrees.LoadAndDelete(pid); ok {
		_ = value.(*windowsCommandTree).terminate()
	}
	return err
}

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
	if value, ok := windowsCommandTrees.Load(process.Pid); ok {
		return value.(*windowsCommandTree).terminate()
	}
	return killWindowsProcessTree(process)
}

func killWindowsProcessTree(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	taskkill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(process.Pid), "/T", "/F")
	taskkill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	treeErr := taskkill.Run()
	killErr := process.Kill()
	if treeErr == nil || killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(treeErr, killErr)
}
