package content

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/configvalue"
)

// ListenTool captures audio from microphone and transcribes to text.
// Supports short voice commands and extended meeting recording.
type ListenTool struct{ workspace string }

func NewListenTool(ws string) *ListenTool { return &ListenTool{ws} }

func (t *ListenTool) Name() string { return "listen" }
func (t *ListenTool) Description() string {
	return "Listen to voice: record from microphone and transcribe to text. Like Siri for openkitty. " +
		"Actions: command(short voice command), meeting(record meeting), wait(wait for speech like Siri — listens until you speak, then transcribes). " +
		"Use when user wants voice input, needs meeting recording, or asks to 'listen to me'. " +
		"Windows: built-in SAPI speech recognition (free, Chinese+English, offline)."
}

func (t *ListenTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"command", "meeting", "wait"},
				"description": "command: short voice command. meeting: record meeting. wait: like Siri — waits silently until you speak, then captures and transcribes.",
			},
			"duration": map[string]interface{}{
				"type":        "integer",
				"description": "Recording duration in seconds. command: 3-10 (default 5). meeting: 60-3600 (default 300 for 5min).",
			},
			"language": map[string]interface{}{
				"type":        "string",
				"description": "Language: zh-CN, en-US, auto (default: zh-CN for meetings)",
			},
		},
	}
}

func (t *ListenTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "command")
	if action == "" {
		action = "command"
	}
	language := configvalue.String(params, "language", "zh-CN")
	if language == "" {
		language = "zh-CN"
	}

	duration := configvalue.Int(params, "duration", 0)
	if duration <= 0 && action == "meeting" {
		duration = 300 // 5 minutes default
	} else if duration <= 0 {
		duration = 5
	}

	// Cap durations
	if action == "command" && duration > 30 {
		duration = 30
	}
	if action == "meeting" && duration > 3600 {
		duration = 3600
	}
	if action != "command" && action != "meeting" && action != "wait" {
		return "", fmt.Errorf("unknown action: %s", action)
	}

	audioDir, err := t.audioDirectory(ctx)
	if err != nil {
		return "", err
	}

	switch action {
	case "command":
		return t.recordCommand(ctx, duration, language, audioDir)
	case "meeting":
		return t.recordMeeting(ctx, duration, language, audioDir)
	case "wait":
		return t.waitForSpeech(ctx, duration, language, audioDir)
	}
	return "", fmt.Errorf("unknown action: %s", action)
}

func (t *ListenTool) audioDirectory(ctx context.Context) (string, error) {
	taskID := ""
	if id, ok := ctx.Value("task_id").(string); ok {
		taskID = id
	}
	if strings.TrimSpace(taskID) == "" {
		taskID = fmt.Sprintf("listen_%s", time.Now().Format("20060102_150405"))
	}
	taskID = safeMediaTaskID(taskID)
	audioDir := filepath.Join(t.workspace, ".openkitty", "storage", taskID, "recordings")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		return "", fmt.Errorf("create audio recording directory: %w", err)
	}
	return audioDir, nil
}

// recordCommand: short voice capture for commands
func (t *ListenTool) recordCommand(ctx context.Context, duration int, language, dir string) (string, error) {
	outputFile := filepath.Join(dir, fmt.Sprintf("cmd_%s.wav", time.Now().Format("150405")))

	// Windows SAPI speech recognition (instant, no file needed)
	if _, err := exec.LookPath("powershell"); err == nil {
		result, err := t.listenWithSAPI(ctx, duration, outputFile)
		if err == nil && result != "" {
			return fmt.Sprintf("🎤 Heard: %s", result), nil
		}
	}

	// Fallback: record WAV then transcribe
	if err := t.recordAudio(ctx, duration, outputFile); err != nil {
		return "", fmt.Errorf("recording failed: %w", err)
	}
	text, err := t.transcribe(ctx, outputFile, language)
	if err != nil {
		return "", fmt.Errorf("transcription failed: %w", err)
	}
	if text == "" {
		return "🎤 Heard nothing (silence or unclear)", nil
	}
	return fmt.Sprintf("🎤 Heard: %s", text), nil
}

// recordMeeting: extended recording with chunked transcription
func (t *ListenTool) recordMeeting(ctx context.Context, duration int, language, dir string) (string, error) {
	meetingTime := time.Now()
	meetingID := meetingTime.Format("20060102_150405")

	t.logMeeting("开始录制会议", meetingID, duration)

	// For meetings, record in chunks for reliable transcription
	chunkDuration := 60 // 1 minute chunks
	chunks := (duration + chunkDuration - 1) / chunkDuration

	var fullTranscript []string
	var chunkFiles []string

recordingLoop:
	for i := 0; i < chunks; i++ {
		select {
		case <-ctx.Done():
			break recordingLoop
		default:
		}

		remaining := duration - (i * chunkDuration)
		if remaining > chunkDuration {
			remaining = chunkDuration
		}
		if remaining <= 0 {
			break
		}

		chunkFile := filepath.Join(dir, fmt.Sprintf("meeting_%s_chunk%d.wav", meetingID, i+1))
		t.logMeeting(fmt.Sprintf("录制第 %d/%d 段 (%ds)", i+1, chunks, remaining), meetingID, 0)

		if err := t.recordAudio(ctx, remaining, chunkFile); err != nil {
			t.logMeeting(fmt.Sprintf("第%d段录制失败: %v", i+1, err), meetingID, 0)
			continue
		}
		chunkFiles = append(chunkFiles, chunkFile)

		// Transcribe chunk
		text, err := t.transcribe(ctx, chunkFile, language)
		if err != nil {
			t.logMeeting(fmt.Sprintf("第%d段转写失败: %v", i+1, err), meetingID, 0)
			fullTranscript = append(fullTranscript, fmt.Sprintf("[第%d段转写失败]", i+1))
			continue
		}
		if text != "" {
			fullTranscript = append(fullTranscript, text)
		}
	}

	// Build complete transcript
	transcript := strings.Join(fullTranscript, "\n")
	transcriptFile := filepath.Join(dir, fmt.Sprintf("meeting_%s_transcript.txt", meetingID))
	if err := os.WriteFile(transcriptFile, []byte(transcript), 0o644); err != nil {
		return "", fmt.Errorf("write meeting transcript: %w", err)
	}

	// Clean up chunks (keep transcript)
	for _, f := range chunkFiles {
		os.Remove(f)
	}

	durationActual := time.Since(meetingTime)

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📝 会议记录完成 (%s)\n", durationActual.Round(time.Second)))
	result.WriteString(fmt.Sprintf("   转录文件: %s\n", transcriptFile))
	result.WriteString(fmt.Sprintf("   转录长度: %d 字符\n\n", len(transcript)))
	result.WriteString("--- 会议转录 ---\n")
	result.WriteString(transcript)
	result.WriteString("\n--- 结束 ---\n\n")
	result.WriteString("请基于以上转录内容生成会议纪要：提取关键决策、行动项、责任人。")

	return result.String(), nil
}

// waitForSpeech: like Siri — waits silently until speech is detected, then records and transcribes.
func (t *ListenTool) waitForSpeech(ctx context.Context, timeout int, language, dir string) (string, error) {
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}

	// Windows: use SAPI continuous recognition (most Siri-like)
	if _, err := exec.LookPath("powershell"); err == nil {
		return t.waitWithSAPI(ctx, timeout, dir)
	}

	// Fallback: energy-based VAD via Python
	return t.waitWithVAD(ctx, timeout, language, dir)
}

// waitWithSAPI: uses Windows SAPI continuous recognition — event-driven, like Siri.
func (t *ListenTool) waitWithSAPI(ctx context.Context, timeout int, dir string) (string, error) {
	outputFile := filepath.Join(dir, fmt.Sprintf("wake_%s.txt", time.Now().Format("150405")))

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech
$recognizer = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$recognizer.SetInputToDefaultAudioDevice()
$dictation = New-Object System.Speech.Recognition.DictationGrammar
$recognizer.LoadGrammar($dictation)

$results = [System.Collections.ArrayList]::new()
$action = { $results.Add($args[0].Result.Text) > $null }
$recognizer.add_SpeechRecognized($action)

# Continuous recognition for the timeout period
$recognizer.RecognizeAsync([System.Speech.Recognition.RecognizeMode]::Multiple)  # Continuous mode
$deadline = [DateTime]::Now.AddSeconds(%d)

while ([DateTime]::Now -lt $deadline) {
	Start-Sleep -Milliseconds 500
	if ($results.Count -gt 0) {
		# Got speech, stop listening
		break
	}
}
$recognizer.RecognizeAsyncStop()

if ($results.Count -gt 0) {
	$text = ($results -join " ")
	$text | Out-File -FilePath '%s' -Encoding UTF8
	Write-Output $text
} else {
	Write-Output ""
}
`, timeout, strings.ReplaceAll(outputFile, "'", "''"))

	cmd := exec.CommandContext(ctx, "powershell", "-Command", script)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "🎤 等待了但没听到说话", nil
	}
	return fmt.Sprintf("🎤 Heard: %s", result), nil
}

// waitWithVAD: energy-based voice activity detection via Python + pyaudio.
func (t *ListenTool) waitWithVAD(ctx context.Context, timeout int, language, dir string) (string, error) {
	outputFile := filepath.Join(dir, fmt.Sprintf("wake_%s.wav", time.Now().Format("150405")))

	if _, err := exec.LookPath("python"); err == nil {
		script := fmt.Sprintf(`
import pyaudio, wave, sys, struct, math
try:
    CHUNK = 1024
    FORMAT = pyaudio.paInt16
    CHANNELS = 1
    RATE = 16000
    SILENCE_THRESHOLD = 500  # RMS energy threshold
    SILENCE_FRAMES = 30      # frames of silence to stop

    p = pyaudio.PyAudio()
    stream = p.open(format=FORMAT, channels=CHANNELS, rate=RATE,
                    input=True, frames_per_buffer=CHUNK)

    frames = []
    speaking = False
    silence_count = 0
    max_frames = int(RATE / CHUNK * %d)  # timeout

    for i in range(max_frames):
        data = stream.read(CHUNK)
        # Calculate RMS energy
        count = len(data) // 2
        shorts = struct.unpack('h' * count, data)
        rms = math.sqrt(sum(s*s for s in shorts) / count)

        if rms > SILENCE_THRESHOLD:
            if not speaking:
                speaking = True
            frames.append(data)
            silence_count = 0
        elif speaking:
            frames.append(data)
            silence_count += 1
            if silence_count > SILENCE_FRAMES:
                break  # End of utterance

    stream.stop_stream()
    stream.close()
    p.terminate()

    if len(frames) > 0:
        wf = wave.open(r'%s', 'wb')
        wf.setnchannels(CHANNELS)
        wf.setsampwidth(p.get_sample_size(FORMAT))
        wf.setframerate(RATE)
        wf.writeframes(b''.join(frames))
        wf.close()
        print('SPEECH_DETECTED')
    else:
        print('NO_SPEECH')
except Exception as e:
    print(f'Error: {e}', file=sys.stderr)
    sys.exit(1)
`, timeout, strings.ReplaceAll(outputFile, "\\", "\\\\"))

		cmd := exec.CommandContext(ctx, "python", "-c", script)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "SPEECH_DETECTED") {
			return "🎤 等待了但没听到说话", nil
		}

		// Transcribe the captured speech
		text, err := t.transcribe(ctx, outputFile, language)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "🎤 检测到说话但未能识别内容", nil
		}
		os.Remove(outputFile)
		return fmt.Sprintf("🎤 Heard: %s", text), nil
	}

	return "", fmt.Errorf("no audio support available (install pyaudio)")
}

func (t *ListenTool) logMeeting(msg, meetingID string, duration int) {
	log.Printf("[Meeting %s] %s", meetingID, msg)
}

// ========== low-level recording / transcription ==========

func (t *ListenTool) listenWithSAPI(ctx context.Context, duration int, outputFile string) (string, error) {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech
$recognizer = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$recognizer.SetInputToDefaultAudioDevice()
$dictation = New-Object System.Speech.Recognition.DictationGrammar
$recognizer.LoadGrammar($dictation)

$result = $null
$done = $false
$action = { $result = $args[0].Result.Text; $done = $true }
$recognizer.add_SpeechRecognized($action)

$recognizer.RecognizeAsync([System.Speech.Recognition.RecognizeMode]::Single)
$timeout = [DateTime]::Now.AddSeconds(%d)
while (-not $done -and [DateTime]::Now -lt $timeout) {
	Start-Sleep -Milliseconds 200
}
$recognizer.RecognizeAsyncStop()

if ($result) {
	$result | Out-File -FilePath '%s' -Encoding UTF8
	Write-Output $result
} else {
	Write-Output ""
}
`, duration, strings.ReplaceAll(outputFile, "'", "''"))

	cmd := exec.CommandContext(ctx, "powershell", "-Command", script)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *ListenTool) recordAudio(ctx context.Context, duration int, output string) error {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.CommandContext(ctx, "ffmpeg",
			"-f", "dshow", "-i", "audio=default",
			"-t", fmt.Sprintf("%d", duration),
			"-y", output,
		)
		return cmd.Run()
	}

	if _, err := exec.LookPath("python"); err == nil {
		script := fmt.Sprintf(`
import pyaudio, wave, sys
try:
    CHUNK = 1024
    FORMAT = pyaudio.paInt16
    CHANNELS = 1
    RATE = 16000
    RECORD_SECONDS = %d
    p = pyaudio.PyAudio()
    stream = p.open(format=FORMAT, channels=CHANNELS, rate=RATE, input=True, frames_per_buffer=CHUNK)
    frames = []
    for i in range(0, int(RATE / CHUNK * RECORD_SECONDS)):
        data = stream.read(CHUNK)
        frames.append(data)
    stream.stop_stream()
    stream.close()
    p.terminate()
    wf = wave.open(r'%s', 'wb')
    wf.setnchannels(CHANNELS)
    wf.setsampwidth(p.get_sample_size(FORMAT))
    wf.setframerate(RATE)
    wf.writeframes(b''.join(frames))
    wf.close()
except Exception as e:
    print(f'Error: {e}', file=sys.stderr)
    sys.exit(1)
`, duration, strings.ReplaceAll(output, "\\", "\\\\"))

		cmd := exec.CommandContext(ctx, "python", "-c", script)
		return cmd.Run()
	}

	return fmt.Errorf("no audio recording tool available (install ffmpeg or pyaudio)")
}

func (t *ListenTool) transcribe(ctx context.Context, audioPath, language string) (string, error) {
	if _, err := exec.LookPath("whisper"); err == nil {
		lang := language
		if lang == "" {
			lang = "zh"
		}
		cmd := exec.CommandContext(ctx, "whisper", audioPath,
			"--model", "small", "--language", lang,
			"--output_format", "txt", "--output_dir", filepath.Dir(audioPath),
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			txtPath := strings.TrimSuffix(audioPath, ".wav") + ".txt"
			if data, err := os.ReadFile(txtPath); err == nil {
				return strings.TrimSpace(string(data)), nil
			}
		}
		_ = out
	}

	if _, err := exec.LookPath("python"); err == nil {
		script := fmt.Sprintf(`
import sys
try:
    import whisper
    model = whisper.load_model("small")
    result = model.transcribe(r"%s", language="%s")
    print(result["text"])
except Exception as e:
    print(f"WHISPER_ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, strings.ReplaceAll(audioPath, "\\", "\\\\"), language)

		cmd := exec.CommandContext(ctx, "python", "-c", script)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text := strings.TrimSpace(string(out))
			if text != "" && !strings.Contains(text, "WHISPER_ERROR") {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("no transcription engine available (install whisper or whisper.cpp)")
}
