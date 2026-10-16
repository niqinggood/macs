package voice

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// WakeService provides Siri-like continuous voice listening.
// Runs in background, detects speech, transcribes, and submits tasks.
type WakeService struct {
	submit    func(string)
	enabled   bool
	listening bool
	mu        sync.Mutex
	stopCh    chan struct{}
	logger    *log.Logger
}

func NewWakeService(submit func(string)) *WakeService {
	return &WakeService{
		submit: submit,
		stopCh: make(chan struct{}),
		logger: log.New(log.Writer(), "[Wake] ", log.LstdFlags),
	}
}

func (w *WakeService) Start() {
	w.mu.Lock()
	w.enabled = true
	w.mu.Unlock()

	w.logger.Println("🎤 Siri式语音唤醒服务启动")
	go w.listenLoop()
}

func (w *WakeService) Stop() {
	w.mu.Lock()
	w.enabled = false
	w.mu.Unlock()
	close(w.stopCh)
	w.logger.Println("🛑 语音唤醒服务已停止")
}

func (w *WakeService) IsListening() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.listening
}

// listenLoop continuously listens for voice, like Siri.
func (w *WakeService) listenLoop() {
	w.logger.Println("🎤 进入监听循环")
	for {
		select {
		case <-w.stopCh:
			w.logger.Println("🎤 监听循环收到停止信号")
			return
		default:
		}

		w.mu.Lock()
		if !w.enabled {
			w.mu.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}
		w.listening = true
		w.mu.Unlock()

		// Listen for speech
		w.logger.Println("🎤 等待语音...")
		text, err := w.listenOnce(8)
		if err != nil {
			w.logger.Printf("🎤 监听出错: %v", err)
		}

		w.mu.Lock()
		w.listening = false
		w.mu.Unlock()

		if text == "" {
			time.Sleep(1 * time.Second)
			continue
		}

		w.logger.Printf("🎤 检测到语音: %s", text)

		// Skip if too short or noise
		if len([]rune(text)) < 2 {
			continue
		}

		// Submit as task to the first available agent
		if w.submit != nil {
			w.submit(text)
		}

		// Cool down between utterances
		time.Sleep(3 * time.Second)
	}
}

// listenOnce captures a single utterance using SAPI or VAD.
func (w *WakeService) listenOnce(timeout int) (string, error) {
	// Windows SAPI continuous recognition
	if _, err := exec.LookPath("powershell"); err == nil {
		return w.listenWithSAPI(timeout)
	}
	// Fallback: energy-based VAD via Python
	return w.listenWithVAD(timeout)
}

func (w *WakeService) listenWithSAPI(timeout int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+5)*time.Second)
	defer cancel()

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech
$recognizer = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$recognizer.SetInputToDefaultAudioDevice()
$dictation = New-Object System.Speech.Recognition.DictationGrammar
$recognizer.LoadGrammar($dictation)

$results = [System.Collections.ArrayList]::new()
$action = { $results.Add($args[0].Result.Text) > $null }
$recognizer.add_SpeechRecognized($action)

$recognizer.RecognizeAsync([System.Speech.Recognition.RecognizeMode]::Single)
$deadline = [DateTime]::Now.AddSeconds(%d)

while ([DateTime]::Now -lt $deadline -and $results.Count -eq 0) {
	Start-Sleep -Milliseconds 300
}
$recognizer.RecognizeAsyncStop()

if ($results.Count -gt 0) {
	Write-Output ($results -join " ")
} else {
	Write-Output ""
}
`, timeout)

	cmd := exec.CommandContext(ctx, "powershell", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (w *WakeService) listenWithVAD(timeout int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+5)*time.Second)
	defer cancel()

	if _, err := exec.LookPath("python"); err != nil {
		return "", fmt.Errorf("python not available for VAD")
	}

	script := fmt.Sprintf(`
import pyaudio, struct, math, sys, wave, os, tempfile
try:
    CHUNK = 1024
    FORMAT = pyaudio.paInt16
    CHANNELS = 1
    RATE = 16000
    THRESHOLD = 400
    SILENCE_LIMIT = 25

    p = pyaudio.PyAudio()
    stream = p.open(format=FORMAT, channels=CHANNELS, rate=RATE,
                    input=True, frames_per_buffer=CHUNK)
    frames = []
    speaking = False
    silence = 0
    max_frames = int(RATE / CHUNK * %d)

    for i in range(max_frames):
        data = stream.read(CHUNK)
        count = len(data) // 2
        shorts = struct.unpack('h' * count, data)
        rms = math.sqrt(sum(s*s for s in shorts) / count)

        if rms > THRESHOLD:
            if not speaking:
                speaking = True
            frames.append(data)
            silence = 0
        elif speaking:
            frames.append(data)
            silence += 1
            if silence > SILENCE_LIMIT:
                break

    stream.stop_stream()
    stream.close()
    p.terminate()

    if len(frames) > 10:
        tmp = tempfile.mktemp(suffix='.wav')
        wf = wave.open(tmp, 'wb')
        wf.setnchannels(CHANNELS)
        wf.setsampwidth(p.get_sample_size(FORMAT))
        wf.setframerate(RATE)
        wf.writeframes(b''.join(frames))
        wf.close()

        try:
            import whisper
            model = whisper.load_model('small')
            result = model.transcribe(tmp, language='zh')
            print(result['text'].strip())
        except:
            print('')
        finally:
            os.remove(tmp)
    else:
        print('')
except Exception as e:
    print('', file=sys.stderr)
    sys.exit(1)
`, timeout)

	cmd := exec.CommandContext(ctx, "python", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
