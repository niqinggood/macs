package app

import (
	"context"
	"testing"
)

func TestSelectWindowsShell(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    windowsShellKind
	}{
		{
			name:    "t11 explicit powershell file keeps windows path",
			command: `powershell -NoProfile -ExecutionPolicy Bypass -File E:\adebug2\0708\read_skill.ps1`,
			want:    windowsShellPowerShell,
		},
		{
			name:    "explicit pwsh executable",
			command: `pwsh.exe -NoProfile -File E:\adebug2\0708\read_skill.ps1`,
			want:    windowsShellPowerShell,
		},
		{
			name:    "powershell pipeline preserves current object variable",
			command: `Get-Content -LiteralPath E:\adebug2\0708\music-skill-fetched.md | ForEach-Object { $_.Trim() }`,
			want:    windowsShellPowerShell,
		},
		{
			name:    "explicit cmd",
			command: `cmd.exe /D /C type E:\adebug2\0708\music-skill-fetched.md`,
			want:    windowsShellCmd,
		},
		{
			name:    "cmd builtin and percent variable",
			command: `dir /b "%TEMP%"`,
			want:    windowsShellCmd,
		},
		{
			name:    "unix cat",
			command: `cat /e/adebug2/0708/music-skill-fetched.md`,
			want:    windowsShellGitBash,
		},
		{
			name:    "unix head",
			command: `head -n 80 /e/adebug2/0708/music-skill-fetched.md`,
			want:    windowsShellGitBash,
		},
		{
			name:    "unix cat head pipeline",
			command: `cat /e/adebug2/0708/music-skill-fetched.md | head -n 80`,
			want:    windowsShellGitBash,
		},
		{
			name:    "unix working directory chain",
			command: `cd /e/adebug2/0708 && head -n 80 music-skill-fetched.md`,
			want:    windowsShellGitBash,
		},
		{
			name:    "native windows path defaults to powershell",
			command: `python E:\adebug2\0708\generate_music.py`,
			want:    windowsShellPowerShell,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectWindowsShell(tt.command); got != tt.want {
				t.Fatalf("selectWindowsShell(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestNativeFastPathRejectsCompoundShellCommands(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: `cat music-skill-fetched.md`, want: false},
		{command: `head -n 80 music-skill-fetched.md`, want: false},
		{command: `cat music-skill-fetched.md | head -n 80`, want: true},
		{command: `cat "name|with-pipe.md"`, want: false},
	}
	for _, tt := range tests {
		if got := hasUnquotedShellOperator(tt.command); got != tt.want {
			t.Errorf("hasUnquotedShellOperator(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestWindowsShellCommandPreservesPowerShellArguments(t *testing.T) {
	cli := &CLITool{}

	t.Run("explicit file invocation keeps drive path", func(t *testing.T) {
		const command = `powershell -NoProfile -ExecutionPolicy Bypass -File E:\adebug2\0708\read_skill.ps1`
		cmd := cli.windowsShellCommandContext(context.Background(), windowsShellPowerShell, command)
		if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != `E:\adebug2\0708\read_skill.ps1` {
			t.Fatalf("explicit PowerShell args corrupted: %#v", cmd.Args)
		}
	})

	t.Run("script block is one untouched command argument", func(t *testing.T) {
		const command = `Get-Content -LiteralPath E:\adebug2\0708\music-skill-fetched.md | ForEach-Object { $_.Trim() }`
		cmd := cli.windowsShellCommandContext(context.Background(), windowsShellPowerShell, command)
		if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != command {
			t.Fatalf("PowerShell command argument corrupted: %#v", cmd.Args)
		}
	})
}
