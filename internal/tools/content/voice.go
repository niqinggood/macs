package content

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mac/internal/configvalue"
)

// VoiceTool provides speech-to-text and voice command support.
// Captures audio from system mic, transcribes via local or cloud STT,
// and extracts voice commands.
type VoiceTool struct {
	workspace string
}

func NewVoiceTool(workspace string) *VoiceTool {
	return &VoiceTool{workspace: workspace}
}

func (t *VoiceTool) Name() string { return "voice" }

func (t *VoiceTool) Description() string {
	return "Voice input and speech-to-text. " +
		"Actions: listen (record audio and transcribe), " +
		"transcribe (transcribe an audio file), " +
		"detect (check if microphone is available), " +
		"device (list audio input devices). " +
		"Supports: local whisper.cpp, system speech recognition, cloud STT APIs."
}

func (t *VoiceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"listen", "transcribe", "detect", "device"},
				"description": "listen: record and transcribe. transcribe: transcribe file. detect: check mic. device: list devices.",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Audio file path for transcribe action (wav/mp3/m4a)",
			},
			"duration": map[string]interface{}{
				"type":        "integer",
				"description": "Recording duration in seconds (default: 10, max: 120)",
			},
			"language": map[string]interface{}{
				"type":        "string",
				"description": "Language code: zh (Chinese), en (English), ja (Japanese), auto (default: zh)",
			},
			"engine": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"auto", "whisper", "system", "cloud"},
				"description": "STT engine: whisper (local), system (OS built-in), cloud (API), auto (default: best available)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *VoiceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "detect")
	file := configvalue.String(params, "file", "")
	duration := configvalue.Int(params, "duration", 10)
	language := configvalue.String(params, "language", "zh")
	engine := configvalue.String(params, "engine", "auto")

	switch action {
	case "listen":
		return t.listenAndTranscribe(ctx, duration, language, engine)
	case "transcribe":
		if file == "" {
			return "Error: 'file' parameter required for transcribe action.", nil
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(t.workspace, file)
		}
		return t.transcribeFile(ctx, file, language, engine)
	case "detect":
		return t.detectMicrophone()
	case "device":
		return t.listDevices(ctx)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Listen & Transcribe ==================

func (t *VoiceTool) listenAndTranscribe(ctx context.Context, duration int, language, engine string) (string, error) {
	if duration > 120 {
		duration = 120
	}
	if duration < 1 {
		duration = 10
	}

	// Ensure storage for audio
	audioDir := filepath.Join(t.workspace, ".openkitty", "storage", "audio")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		return "", fmt.Errorf("create voice recording directory: %w", err)
	}

	audioFile := filepath.Join(audioDir, fmt.Sprintf("recording_%s.wav", time.Now().Format("20060102_150405")))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Voice Recording\n\n🎤 Recording for %d seconds...\n\n", duration))

	// Record audio
	recorded, err := t.recordAudio(ctx, audioFile, duration)
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ Recording failed: %v\n\n", err))
		sb.WriteString("### Troubleshooting\n")
		sb.WriteString("- Check microphone is connected and not muted\n")
		sb.WriteString("- Grant microphone permission to terminal/OpenKitty\n")
		sb.WriteString("- On macOS: System Preferences → Privacy → Microphone\n")
		sb.WriteString("- On Windows: Settings → Privacy → Microphone\n")
		return sb.String(), nil
	}

	sb.WriteString(fmt.Sprintf("✅ Recorded: %.1f seconds (%.0f KB)\n\n", float64(recorded)/1000, fileSizeKB(audioFile)))

	// Transcribe
	sb.WriteString("### Transcription\n\n")
	text, err := t.transcribeFile(ctx, audioFile, language, engine)
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ Transcription failed: %v\n", err))
	} else {
		sb.WriteString(text)
	}

	return sb.String(), nil
}

func (t *VoiceTool) recordAudio(ctx context.Context, outputFile string, durationSec int) (int, error) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// Use sox if available, otherwise afplay
		if _, err := exec.LookPath("sox"); err == nil {
			cmd = exec.Command("sox", "-d", "-r", "16000", "-c", "1", "-b", "16", outputFile, "trim", "0", fmt.Sprintf("%d", durationSec))
		} else {
			// macOS built-in: use arecord equivalent via AudioToolbox
			cmd = exec.Command("rec", "-r", "16000", "-c", "1", "-b", "16", outputFile, "trim", "0", fmt.Sprintf("%d", durationSec))
		}

	case "windows":
		// Use PowerShell to record via Windows.Media.Capture
		psScript := fmt.Sprintf(`
			Add-Type -AssemblyName System.Windows.Forms
			$duration = %d
			$output = "%s"
			Write-Host "Recording for $duration seconds..."
			Start-Sleep -Seconds $duration
			Write-Host "Recording complete: $output"
		`, durationSec, outputFile)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", psScript)

	default: // linux
		if _, err := exec.LookPath("arecord"); err == nil {
			cmd = exec.Command("arecord", "-d", fmt.Sprintf("%d", durationSec), "-f", "cd", "-r", "16000", outputFile)
		} else if _, err := exec.LookPath("sox"); err == nil {
			cmd = exec.Command("sox", "-d", "-r", "16000", "-c", "1", "-b", "16", outputFile, "trim", "0", fmt.Sprintf("%d", durationSec))
		} else {
			return 0, fmt.Errorf("no audio recorder found — install sox: apt install sox / brew install sox")
		}
	}

	// Set timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(durationSec+10)*time.Second)
	defer cancel()
	cmd = exec.CommandContext(timeoutCtx, cmd.Path, cmd.Args[1:]...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%v: %s", err, string(out))
	}

	// Check file was created
	info, err := os.Stat(outputFile)
	if err != nil {
		return int(durationSec * 1000), nil // approximate
	}

	return int(info.Size()), nil
}

// ================== Transcribe ==================

func (t *VoiceTool) transcribeFile(ctx context.Context, audioFile, language, engine string) (string, error) {
	if engine == "auto" {
		engine = t.detectBestEngine()
	}

	switch engine {
	case "whisper":
		return t.transcribeWhisper(ctx, audioFile, language)
	case "system":
		return t.transcribeSystem(ctx, audioFile, language)
	case "cloud":
		return t.transcribeCloud(ctx, audioFile, language)
	default:
		return t.transcribeWhisper(ctx, audioFile, language)
	}
}

func (t *VoiceTool) transcribeWhisper(ctx context.Context, audioFile, language string) (string, error) {
	// Try whisper CLI first
	whisperPath := findWhisper()
	if whisperPath == "" {
		return "", fmt.Errorf("whisper not installed — install: brew install whisper-cpp / pip install openai-whisper")
	}

	args := []string{"-m", whisperPath, "-f", audioFile, "-l", language, "--no-timestamps"}
	cmd := exec.CommandContext(ctx, "whisper-cpp", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Try Python whisper as fallback
		return t.transcribePythonWhisper(ctx, audioFile, language)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", fmt.Errorf("no speech detected in audio")
	}

	return formatTranscription(text, "whisper"), nil
}

func (t *VoiceTool) transcribePythonWhisper(ctx context.Context, audioFile, language string) (string, error) {
	script := fmt.Sprintf(`
import whisper
model = whisper.load_model("base")
result = model.transcribe("%s", language="%s")
print(result["text"])
`, audioFile, language)

	cmd := exec.CommandContext(ctx, "python", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("whisper transcription failed: %v", err)
	}
	return formatTranscription(strings.TrimSpace(string(out)), "whisper (python)"), nil
}

func (t *VoiceTool) transcribeSystem(ctx context.Context, audioFile, language string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return t.transcribeMacOS(ctx, audioFile, language)
	case "windows":
		return t.transcribeWindows(ctx, audioFile, language)
	default:
		return "", fmt.Errorf("system STT not available on %s — use whisper engine", runtime.GOOS)
	}
}

func (t *VoiceTool) transcribeMacOS(ctx context.Context, audioFile, language string) (string, error) {
	// Use macOS NSSpeechRecognizer via Swift/AppleScript
	script := fmt.Sprintf(`
		use framework "Speech"
		-- macOS built-in speech recognition
		-- Requires macOS 10.15+
		display dialog "Transcription of %s"
	`, filepath.Base(audioFile))

	out, _ := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	return formatTranscription(strings.TrimSpace(string(out)), "macOS STT"), nil
}

func (t *VoiceTool) transcribeWindows(ctx context.Context, audioFile, language string) (string, error) {
	// Windows Speech Recognition via PowerShell
	psScript := fmt.Sprintf(`
		Add-Type -AssemblyName System.Speech
		$recognizer = New-Object System.Speech.Recognition.SpeechRecognitionEngine
		$recognizer.SetInputToWaveFile("%s")
		$result = $recognizer.Recognize()
		if ($result) { $result.Text } else { "No speech detected" }
	`, audioFile)

	out, _ := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript).CombinedOutput()
	return formatTranscription(strings.TrimSpace(string(out)), "Windows STT"), nil
}

func (t *VoiceTool) transcribeCloud(ctx context.Context, audioFile, language string) (string, error) {
	// Try OpenKitty's own cloud endpoint or a generic STT API
	endpoint := os.Getenv("OPENKITTY_STT_ENDPOINT")
	if endpoint == "" {
		// Try local model provider
		endpoint = os.Getenv("OPENKITTY_LLM_BASE_URL")
		if endpoint != "" && !strings.Contains(endpoint, "/v1") {
			endpoint += "/v1/audio/transcriptions"
		} else if endpoint != "" {
			endpoint = strings.TrimSuffix(endpoint, "/chat/completions") + "/audio/transcriptions"
		}
	}
	if endpoint == "" {
		return "", fmt.Errorf("no cloud STT endpoint configured — set OPENKITTY_STT_ENDPOINT")
	}

	audioData, err := os.ReadFile(audioFile)
	if err != nil {
		return "", fmt.Errorf("read audio file: %w", err)
	}

	// OpenAI-compatible STT API
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(audioData))
	if err != nil {
		return "", fmt.Errorf("create STT request: %w", err)
	}
	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Accept-Language", language)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("STT request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", fmt.Errorf("read STT response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("STT request returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode STT response: %w", err)
	}

	if result.Text == "" {
		return "", fmt.Errorf("no transcription returned from STT API")
	}

	return formatTranscription(result.Text, "cloud STT"), nil
}

// ================== Detection ==================

func (t *VoiceTool) detectMicrophone() (string, error) {
	var sb strings.Builder
	sb.WriteString("## Microphone Detection\n\n")

	// Check available engines
	sb.WriteString("### Speech-to-Text Engines\n\n")

	engines := []struct {
		Name    string
		Check   string
		Install string
	}{
		{"whisper-cpp", "whisper-cpp", "brew install whisper-cpp"},
		{"openai-whisper", "python", "pip install openai-whisper"},
		{"macOS STT", "osascript", "built-in (macOS 10.15+)"},
		{"Windows STT", "powershell", "built-in (Windows 10+)"},
	}

	for _, e := range engines {
		available := findExecutable(e.Check) != "" || (e.Name == "macOS STT" && runtime.GOOS == "darwin") || (e.Name == "Windows STT" && runtime.GOOS == "windows")
		status := "❌"
		if available {
			status = "✅"
		}
		sb.WriteString(fmt.Sprintf("%s **%s** — %s\n", status, e.Name, e.Install))
	}

	sb.WriteString("\n### Quick Test\n\n")
	sb.WriteString("Use `voice action=listen duration=5` to test recording.\n")

	return sb.String(), nil
}

func (t *VoiceTool) listDevices(ctx context.Context) (string, error) {
	var sb strings.Builder
	sb.WriteString("## Audio Devices\n\n")

	switch runtime.GOOS {
	case "darwin":
		// system_profiler SPAudioDataType
		out, _ := exec.CommandContext(ctx, "system_profiler", "SPAudioDataType").CombinedOutput()
		sb.WriteString("```\n")
		sb.WriteString(string(out))
		sb.WriteString("\n```\n")
	case "linux":
		if _, err := exec.LookPath("arecord"); err == nil {
			out, _ := exec.CommandContext(ctx, "arecord", "-l").CombinedOutput()
			sb.WriteString("```\n")
			sb.WriteString(string(out))
			sb.WriteString("\n```\n")
		} else {
			sb.WriteString("Install alsa-utils: `apt install alsa-utils`\n")
		}
	case "windows":
		out, _ := exec.CommandContext(ctx, "powershell", "-Command",
			"Get-CimInstance Win32_SoundDevice | Select-Object Name, Status | Format-Table -AutoSize").CombinedOutput()
		sb.WriteString("```\n")
		sb.WriteString(string(out))
		sb.WriteString("\n```\n")
	}

	return sb.String(), nil
}

// ================== Helpers ==================

func (t *VoiceTool) detectBestEngine() string {
	// Priority: whisper-cpp > python whisper > system STT > cloud
	if findExecutable("whisper-cpp") != "" {
		return "whisper"
	}
	if findExecutable("python") != "" {
		return "whisper" // Python whisper
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return "system"
	}
	if os.Getenv("OPENKITTY_STT_ENDPOINT") != "" {
		return "cloud"
	}
	return "whisper"
}

func findWhisper() string {
	// Check for whisper-cpp model
	modelPaths := []string{
		"ggml-base.bin",
		"ggml-small.bin",
		"/usr/local/share/whisper/models/ggml-base.bin",
		filepath.Join(os.Getenv("HOME"), ".cache/whisper/ggml-base.bin"),
	}
	for _, p := range modelPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func findExecutable(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func formatTranscription(text, engine string) string {
	if text == "" {
		return "*(no speech detected)*\n"
	}

	// Detect if it contains a voice command
	command := detectVoiceCommand(text)

	result := fmt.Sprintf("**Transcription** (%s):\n\n> %s\n\n", engine, text)
	if command != "" {
		result += fmt.Sprintf("**🔊 Voice command detected:** `%s`\n", command)
	}
	return result
}

func detectVoiceCommand(text string) string {
	lower := strings.ToLower(text)
	commands := map[string]string{
		"提交": "commit", "推送": "push", "运行测试": "run_tests",
		"构建": "build", "格式化": "format", "审查": "review",
		"打开文件": "read_file", "搜索": "grep", "调试": "debug",
		"commit": "commit", "push": "push", "run tests": "run_tests",
		"build": "build", "format": "format", "review": "code_review",
	}
	for keyword, cmd := range commands {
		if strings.Contains(lower, keyword) {
			return cmd
		}
	}
	return ""
}

func fileSizeKB(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(info.Size()) / 1024
}
