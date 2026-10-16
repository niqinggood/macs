//go:build windows

package processutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/UserExistsError/conpty"
)

type windowsPTY struct {
	pty *conpty.ConPty
}

func StartPTY(_ context.Context, exe string, args []string, workDir string, env []string) (PTYProcess, error) {
	if !conpty.IsConPtyAvailable() {
		return nil, conpty.ErrConPtyUnsupported
	}
	commandLine := windowsCommandLine(append([]string{exe}, args...))
	pty, err := conpty.Start(
		commandLine,
		conpty.ConPtyWorkDir(workDir),
		conpty.ConPtyEnv(env),
		conpty.ConPtyDimensions(120, 40),
	)
	if err != nil {
		return nil, err
	}
	return &windowsPTY{pty: pty}, nil
}

func (p *windowsPTY) Read(buf []byte) (int, error)  { return p.pty.Read(buf) }
func (p *windowsPTY) Write(buf []byte) (int, error) { return p.pty.Write(buf) }
func (p *windowsPTY) Close() error                  { return p.pty.Close() }

func (p *windowsPTY) Wait(ctx context.Context) error {
	exitCode, err := p.pty.Wait(ctx)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("exit status %d", exitCode)
	}
	return nil
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteWindowsArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteWindowsArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat("\\", backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat("\\", backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat("\\", backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
