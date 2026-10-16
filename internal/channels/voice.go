package channels

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ChannelVoiceHandler provides cross-platform voice message processing.
// Unifies audio download, format conversion (AMR/SILK/OGG → WAV), and
// multi-engine speech-to-text transcription across all messaging channels.
type ChannelVoiceHandler struct {
	workspace string
	logger    *log.Logger
	resolver  BinaryResolver
}

type BinaryResolver func(ctx context.Context, workspace, name string, autoDownload bool) (string, error)

func NewChannelVoiceHandler(workspace string, logger *log.Logger, resolver BinaryResolver) *ChannelVoiceHandler {
	if logger == nil {
		logger = log.Default()
	}
	return &ChannelVoiceHandler{workspace: workspace, logger: logger, resolver: resolver}
}

// TranscribeAudio is the unified STT pipeline — same engine priority as voice_tool.go:
// whisper-cpp > python whisper > system STT > cloud API
func (h *ChannelVoiceHandler) TranscribeAudio(audioPath, language string) string {
	h.logger.Printf("[VoiceHandler] Transcribing: %s (lang=%s)", filepath.Base(audioPath), language)

	// Convert to WAV if needed (AMR/SILK/OGG are common chat formats)
	wavPath := h.ensureWAV(audioPath)
	if wavPath != "" {
		defer os.Remove(wavPath)
		audioPath = wavPath
	}

	var text string

	// Engine 1: whisper-cpp (local, offline, fast)
	if whisperPath := findWhisperCPP(); whisperPath != "" {
		cmd := exec.Command("whisper-cpp",
			"-m", whisperPath,
			"-f", audioPath,
			"-l", language,
			"--no-timestamps",
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text = strings.TrimSpace(string(out))
			if text != "" {
				h.logger.Printf("[VoiceHandler] whisper-cpp: %s", truncateText(text, 100))
				return fmt.Sprintf("[语音转文字]\n%s", text)
			}
		}
	}

	// Engine 2: Python whisper (pip install openai-whisper)
	if pythonPath, _ := exec.LookPath("python"); pythonPath != "" {
		script := fmt.Sprintf(`
import whisper, sys
try:
    model = whisper.load_model("base")
    result = model.transcribe(r"%s", language="%s")
    print(result["text"])
except Exception as e:
    print(f"WHISPER_ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, audioPath, language)
		cmd := exec.Command(pythonPath, "-c", script)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text = strings.TrimSpace(string(out))
			if text != "" && !strings.Contains(text, "WHISPER_ERROR") {
				h.logger.Printf("[VoiceHandler] python-whisper: %s", truncateText(text, 100))
				return fmt.Sprintf("[语音转文字]\n%s", text)
			}
		}
	}

	// Engine 3: faster-whisper (pip install faster-whisper)
	if pythonPath, _ := exec.LookPath("python"); pythonPath != "" {
		script := fmt.Sprintf(`
from faster_whisper import WhisperModel
model = WhisperModel("base", device="cpu", compute_type="int8")
segments, _ = model.transcribe(r"%s", language="%s")
print(" ".join(s.text for s in segments))
`, audioPath, language)
		cmd := exec.Command(pythonPath, "-c", script)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text = strings.TrimSpace(string(out))
			if text != "" {
				h.logger.Printf("[VoiceHandler] faster-whisper: %s", truncateText(text, 100))
				return fmt.Sprintf("[语音转文字]\n%s", text)
			}
		}
	}

	// Engine 4: speech_recognition (Google Web Speech API — free, needs network)
	if pythonPath, _ := exec.LookPath("python"); pythonPath != "" {
		script := fmt.Sprintf(`
import speech_recognition as sr, sys
r = sr.Recognizer()
try:
    with sr.AudioFile(r"%s") as source:
        audio = r.record(source)
    text = r.recognize_google(audio, language="%s")
    print(text)
except Exception as e:
    print(f"SR_ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, audioPath, language)
		cmd := exec.Command(pythonPath, "-c", script)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text = strings.TrimSpace(string(out))
			if text != "" && !strings.Contains(text, "SR_ERROR") {
				h.logger.Printf("[VoiceHandler] Google STT: %s", truncateText(text, 100))
				return fmt.Sprintf("[语音转文字]\n%s", text)
			}
		}
	}

	// Engine 5: whisper CLI (original OpenAI whisper)
	if whisperPath, _ := exec.LookPath("whisper"); whisperPath != "" {
		cmd := exec.Command(whisperPath, audioPath,
			"--model", "small", "--language", language,
			"--output_format", "txt", "--output_dir", filepath.Dir(audioPath))
		out, _ := cmd.CombinedOutput()
		txtPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".txt"
		if data, err := os.ReadFile(txtPath); err == nil {
			text = strings.TrimSpace(string(data))
			os.Remove(txtPath)
			if text != "" {
				h.logger.Printf("[VoiceHandler] openai-whisper: %s", truncateText(text, 100))
				return fmt.Sprintf("[语音转文字]\n%s", text)
			}
		}
		_ = out
	}

	// No engine available — save file and inform user
	permDir := filepath.Join(h.workspace, ".openkitty", "storage", "voice_messages")
	os.MkdirAll(permDir, 0755)
	permPath := filepath.Join(permDir, fmt.Sprintf("%s_%s", time.Now().Format("20060102_150405"), filepath.Base(audioPath)))

	if data, err := os.ReadFile(audioPath); err == nil {
		os.WriteFile(permPath, data, 0644)
	}

	info, _ := os.Stat(audioPath)
	size := ""
	if info != nil {
		size = fmt.Sprintf("%d KB", info.Size()/1024)
	}

	return fmt.Sprintf("[语音消息] %s (%s)\n语音文件已保存至: %s\n\n💡 安装语音转写引擎:\n"+
		"  brew install whisper-cpp    (推荐，离线，最快)\n"+
		"  pip install openai-whisper  (Python版本)\n"+
		"  pip install faster-whisper  (更快的Python版本)\n"+
		"  pip install SpeechRecognition (免费云端识别)",
		filepath.Base(audioPath), size, permPath)
}

// ensureWAV converts common chat audio formats (AMR/SILK/OGG) to WAV.
// Messaging platforms typically use compressed formats to save bandwidth.
func (h *ChannelVoiceHandler) ensureWAV(audioPath string) string {
	ext := strings.ToLower(filepath.Ext(audioPath))
	wavPath := strings.TrimSuffix(audioPath, ext) + ".wav"

	switch ext {
	case ".amr":
		h.convertFFmpeg(audioPath, wavPath)
		return wavPath
	case ".silk":
		h.convertSilk(audioPath, wavPath)
		return wavPath
	case ".ogg", ".opus":
		h.convertFFmpeg(audioPath, wavPath)
		return wavPath
	case ".mp3", ".m4a", ".aac", ".flac":
		h.convertFFmpeg(audioPath, wavPath)
		return wavPath
	case ".wav":
		return "" // Already WAV
	}
	return "" // Unknown format, try as-is
}

func (h *ChannelVoiceHandler) convertFFmpeg(input, output string) {
	ffmpegPath, err := h.resolveBinary(context.Background(), "ffmpeg", true)
	if err != nil {
		return
	}
	cmd := exec.Command(ffmpegPath, "-y", "-i", input,
		"-ar", "16000", "-ac", "1", "-sample_fmt", "s16", output)
	cmd.Run()
}

func (h *ChannelVoiceHandler) convertSilk(input, output string) {
	// SILK is Tencent's proprietary codec used by QQ/WeChat
	// Try silk-v3-decoder or ffmpeg with silk support
	if _, err := h.resolveBinary(context.Background(), "ffmpeg", true); err == nil {
		h.convertFFmpeg(input, output)
		if fi, err := os.Stat(output); err == nil && fi.Size() > 0 {
			return
		}
	}

	// Try Python pysilk
	if pythonPath, _ := exec.LookPath("python"); pythonPath != "" {
		script := fmt.Sprintf(`
import sys
try:
    from pysilk import decode
    with open(r"%s", "rb") as f:
        silk_data = f.read()
    pcm_data = decode(silk_data)
    import wave
    with wave.open(r"%s", "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(24000)
        wav.writeframes(pcm_data)
except Exception as e:
    print(f"SILK_ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, input, output)
		exec.Command(pythonPath, "-c", script).Run()
	}
}

func (h *ChannelVoiceHandler) resolveBinary(ctx context.Context, name string, autoDownload bool) (string, error) {
	if h.resolver != nil {
		return h.resolver(ctx, h.workspace, name, autoDownload)
	}
	return exec.LookPath(name)
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

// ProcessReader is a convenience method for channels that provide audio data as a Reader.
func (h *ChannelVoiceHandler) ProcessReader(reader io.Reader, fileName, mimeType string) string {
	mediaDir := filepath.Join(h.workspace, ".openkitty", "storage", "voice_messages")
	os.MkdirAll(mediaDir, 0755)

	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s_%s", time.Now().Format("150405"), fileName))
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Sprintf("[语音] 保存失败: %v", err)
	}
	defer f.Close()
	io.Copy(f, reader)
	f.Close()

	return h.TranscribeAudio(localPath, "zh")
}

// ProcessFile is a convenience method for channels that provide audio as a file path.
func (h *ChannelVoiceHandler) ProcessFile(filePath string) string {
	return h.TranscribeAudio(filePath, "zh")
}

// ProcessURL downloads audio from a URL and transcribes it.
func (h *ChannelVoiceHandler) ProcessURL(url, fileName string) string {
	h.logger.Printf("[VoiceHandler] Downloading voice: %s", url)

	mediaDir := filepath.Join(h.workspace, ".openkitty", "storage", "voice_messages")
	os.MkdirAll(mediaDir, 0755)
	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s_%s", time.Now().Format("150405"), fileName))

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Sprintf("[语音] 下载失败: %v", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Sprintf("[语音] 保存失败: %v", err)
	}
	defer f.Close()
	io.Copy(f, resp.Body)

	return h.TranscribeAudio(localPath, "zh")
}

// findWhisperCPP finds the whisper.cpp model file.
func findWhisperCPP() string {
	paths := []string{
		"ggml-base.bin",
		"ggml-small.bin",
		"ggml-base.en.bin",
		"/usr/local/share/whisper/models/ggml-base.bin",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Also check if whisper-cpp is in PATH
	if _, err := exec.LookPath("whisper-cpp"); err == nil {
		return "base" // whisper-cpp can auto-download
	}
	return ""
}
