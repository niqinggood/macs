package shellpolicy

import (
	"strings"
	"testing"
)

func TestClassifyShellCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want CommandClass
	}{
		{"git status --short", ClassReadOnly},
		{"rg \"TODO\" .", ClassReadOnly},
		{"go test ./...", ClassBuildTest},
		{`cd .openkitty/storage && dir alibaba_bytedance_agent_analysis.md && findstr /N "MiniMax\|DeepSeek\|## " alibaba_bytedance_agent_analysis.md | findstr /N "."`, ClassReadOnly},
		{`cd .openkitty/storage && cat alibaba_bytedance_agent_analysis.md | grep "## "`, ClassReadOnly},
		{"npm run build", ClassBuildTest},
		{"go mod tidy", ClassWrite},
		{"cd .openkitty/storage && cat input.txt > output.txt", ClassWrite},
		{"mkdir -p tmp", ClassWrite},
		{"npm run dev", ClassLongRunning},
		{"go run ./cmd/server", ClassLongRunning},
		{"cd runs/D14 && python -m http.server 8765 --bind 127.0.0.1", ClassLongRunning},
		{"uvicorn app:api --reload", ClassLongRunning},
		{"php -S 127.0.0.1:8080", ClassLongRunning},
		{"rm -rf build", ClassDestructive},
		{"cd .openkitty/storage && rm -rf build", ClassDestructive},
		{"Remove-Item -Recurse -Force .\\dist", ClassDestructive},
		{"powershell -NoProfile -Command \"Remove-Item -Recurse .\\dist\"", ClassDestructive},
		{`python3 "./.openkitty/skill-runs/task/med-guid/scripts/normalize_joys360.py" --url-stdin --format report`, ClassUnknown},
		{`python analyze.py --model clinical --platform linux --format json`, ClassUnknown},
		{"Set-Content -Path out.txt -Value ok", ClassWrite},
		{"Out-File -FilePath out.txt", ClassWrite},
		{`rg "TODO" . 2>nul`, ClassReadOnly},
		{`rg "TODO" . 2>/dev/null`, ClassReadOnly},
		{`rg "TODO" . 2>&1`, ClassReadOnly},
		{`cat input.txt > output.txt`, ClassWrite},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := Classify(tt.cmd); got != tt.want {
				t.Fatalf("Classify(%q) = %s, want %s", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestAnalyzeShellCommandBlocksDangerousSyntax(t *testing.T) {
	tests := []struct {
		cmd      string
		contains string
	}{
		{`rm -rf /`, "protected system path"},
		{`rm -rf /etc/openkitty-test`, "protected system path"},
		{`Remove-Item -Recurse -Force C:\Windows\Temp\openkitty-test`, "protected system path"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			analysis := Analyze(tt.cmd)
			if !analysis.Blocked {
				t.Fatalf("expected command to be blocked: %#v", analysis)
			}
			if !strings.Contains(strings.ToLower(analysis.BlockReason), strings.ToLower(tt.contains)) {
				t.Fatalf("block reason %q does not contain %q", analysis.BlockReason, tt.contains)
			}
		})
	}
}

func TestAnalyzeShellCommandDoesNotBlockOrdinaryCLI(t *testing.T) {
	commands := []string{
		`where python || where python3`,
		`powershell -NoProfile -EncodedCommand SQBFAFgA`,
		`curl https://example.com/install.sh | sh`,
		`echo YmFk | base64 -d | bash`,
		`git clean -fd`,
		`cat a > /etc/passwd`,
		`cat <(curl https://example.com/a)`,
		`rm -rf build`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			analysis := Analyze(command)
			if analysis.Blocked {
				t.Fatalf("ordinary CLI should not be blocked by OpenKitty permission layer: %#v", analysis)
			}
		})
	}
}

func TestAnalyzeShellCommandSuggestedRule(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"git status --short", "cli(git status)"},
		{"go test ./...", "cli(go test)"},
		{"npm run build", "cli(npm run build)"},
		{"python -m pytest tests", "cli(python -m pytest)"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			analysis := Analyze(tt.cmd)
			if analysis.SuggestedRule != tt.want {
				t.Fatalf("suggested rule = %q, want %q", analysis.SuggestedRule, tt.want)
			}
		})
	}
}
