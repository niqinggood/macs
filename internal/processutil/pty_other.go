//go:build !windows

package processutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixPTY struct {
	cmd  *exec.Cmd
	file *os.File
	done chan error
}

func StartPTY(ctx context.Context, exe string, args []string, workDir string, env []string) (PTYProcess, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = workDir
	cmd.Env = env
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return nil, err
	}
	p := &unixPTY{cmd: cmd, file: file, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	return p, nil
}

func (p *unixPTY) Read(buf []byte) (int, error)  { return p.file.Read(buf) }
func (p *unixPTY) Write(buf []byte) (int, error) { return p.file.Write(buf) }

func (p *unixPTY) Close() error {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

func (p *unixPTY) Wait(ctx context.Context) error {
	select {
	case err := <-p.done:
		return err
	case <-ctx.Done():
		_ = p.Close()
		return fmt.Errorf("wait canceled: %w", ctx.Err())
	}
}
