package content

import (
	// ：用于打包 ZIP（DOCX 本质是 ZIP）

	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"mac/internal/processutil"
)

type CameraTool struct {
	workspace     string
	captureDir    string
	previewCmd    *exec.Cmd
	captureChan   chan string
	mu            sync.Mutex
	isPreviewing  bool
	currentCancel context.CancelFunc // 当前捕获的取消函数
}

func NewCameraTool(workspace string) *CameraTool {
	captureDir := filepath.Join(workspace, ".openkitty", "tmp")
	_ = os.MkdirAll(captureDir, 0755)

	return &CameraTool{
		workspace:   workspace,
		captureDir:  captureDir,
		captureChan: make(chan string, 100), // 永不关闭
	}
}

func (t *CameraTool) Name() string { return "camera" }

func (t *CameraTool) stopAll() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 取消当前捕获
	if t.currentCancel != nil {
		t.currentCancel()
		t.currentCancel = nil
	}

	// 终止预览进程
	if t.previewCmd != nil && t.previewCmd.Process != nil {
		_ = t.previewCmd.Process.Kill()
		t.previewCmd = nil
	}

	t.isPreviewing = false
	return "🛑 已停止所有摄像头操作", nil
}

func (t *CameraTool) Description() string {
	return `跨平台摄像头实时捕获工具
支持操作：
- preview: 实时预览（窗口显示）
- capture_realtime: 实时帧捕获（后台捕获+可选预览）
- stop: 停止实时预览/捕获
`
}

func (t *CameraTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "操作类型",
				"enum": []string{
					"capture",          // 拍一张照片
					"preview",          // 仅实时预览
					"capture_realtime", // 实时捕获（带/不带预览）
					"stop",             // 停止所有操作
				},
			},
			"fps": map[string]interface{}{
				"type":        "integer",
				"description": "实时捕获帧率（默认5帧/秒）",
				"default":     5,
			},
			"preview": map[string]interface{}{
				"type":        "boolean",
				"description": "捕获时是否显示预览窗口（默认true）",
				"default":     true,
			},
			"duration": map[string]interface{}{
				"type":        "integer",
				"description": "捕获时长（秒，0=无限捕获，直到stop）",
				"default":     0,
			},
			"output_dir": map[string]interface{}{
				"type":        "string",
				"description": "帧保存目录（默认./tmp）",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CameraTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, ok := params["action"].(string)
	if !ok {
		return "", fmt.Errorf("action必须是字符串类型")
	}

	switch action {
	case "capture":
		return t.capturePhoto()
	case "preview":
		return t.startPreview()
	case "capture_realtime":
		fps := 5
		if f, ok := params["fps"].(float64); ok {
			fps = int(f)
		}
		preview := true
		if p, ok := params["preview"].(bool); ok {
			preview = p
		}
		duration := 0
		if d, ok := params["duration"].(float64); ok {
			duration = int(d)
		}
		outputDir := params["output_dir"].(string)
		if outputDir == "" {
			outputDir = filepath.Join(t.captureDir, "realtime")
		}
		return t.startRealtimeCapture(fps, preview, duration, outputDir)
	case "stop":
		return t.stopAll()
	default:
		return "", fmt.Errorf("不支持的操作：%s，仅支持 capture/preview/capture_realtime/stop", action)
	}
}

// 0. 拍一张照片
func (t *CameraTool) capturePhoto() (string, error) {
	os.MkdirAll(t.captureDir, 0755)
	filename := fmt.Sprintf("photo_%s.jpg", time.Now().Format("150405"))
	outputPath := filepath.Join(t.captureDir, filename)
	ffmpegPath, err := processutil.ResolveFFmpegBinary(context.Background(), t.workspace, "ffmpeg", true)
	if err != nil {
		return "", fmt.Errorf("camera capture requires ffmpeg: %w", err)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command(ffmpegPath, "-y", "-f", "dshow", "-i", "video=Integrated Camera",
			"-vframes", "1", "-q:v", "2", outputPath)
	case "darwin":
		cmd = exec.Command(ffmpegPath, "-y", "-f", "avfoundation", "-i", "0:0",
			"-vframes", "1", "-q:v", "2", outputPath)
	case "linux":
		cmd = exec.Command(ffmpegPath, "-y", "-f", "v4l2", "-i", "/dev/video0",
			"-vframes", "1", "-q:v", "2", outputPath)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("camera capture failed: %w (is ffmpeg installed?)", err)
	}

	fi, _ := os.Stat(outputPath)
	size := ""
	if fi != nil {
		size = fmt.Sprintf(" (%.1f KB)", float64(fi.Size())/1024)
	}
	return fmt.Sprintf("📸 Photo saved: %s%s", outputPath, size), nil
}

// 1. 仅实时预览（窗口显示摄像头画面）
func (t *CameraTool) startPreview() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.isPreviewing {
		return "📹 已在实时预览中", nil
	}

	// 构建系统特定的ffplay命令
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Windows: dshow设备（自动识别默认摄像头）
		cmd = exec.Command("ffplay", "-f", "dshow", "-i", "video=Integrated Camera",
			"-window_title", "Camera Preview", "-autoexit")
	case "darwin":
		// macOS: avfoundation设备
		cmd = exec.Command("ffplay", "-f", "avfoundation", "-i", "0:0",
			"-window_title", "Camera Preview", "-autoexit")
	case "linux":
		// Linux: v4l2设备
		cmd = exec.Command("ffplay", "-f", "v4l2", "-i", "/dev/video0",
			"-window_title", "Camera Preview", "-autoexit")
	default:
		return "", fmt.Errorf("不支持的系统：%s", runtime.GOOS)
	}

	// 启动预览进程
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动实时预览失败：%v", err)
	}

	t.previewCmd = cmd
	t.isPreviewing = true

	// 后台监控进程退出
	go func() {
		_ = cmd.Wait()
		t.mu.Lock()
		t.isPreviewing = false
		t.mu.Unlock()
		fmt.Println("📹 实时预览已停止")
	}()

	return "📹 实时预览已启动（关闭窗口/执行stop操作可停止）", nil
}
func (t *CameraTool) startRealtimeCapture(fps int, preview bool, duration int, outputDir string) (string, error) {
	t.mu.Lock()

	// 如果已经有预览/捕获在运行，先停止
	if t.isPreviewing {
		t.mu.Unlock()
		t.stopAll() // 停止当前操作
		t.mu.Lock()
	}

	// 创建新的上下文用于控制本次捕获
	ctx, cancel := context.WithCancel(context.Background())
	t.currentCancel = cancel
	t.isPreviewing = true

	// 确保输出目录存在
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.mu.Unlock()
		cancel()
		return "", fmt.Errorf("创建输出目录失败: %v", err)
	}
	t.mu.Unlock()

	// 如果需要预览，启动预览窗口
	if preview {
		previewResult, err := t.startPreview()
		if err != nil {
			t.stopAll()
			return "", fmt.Errorf("启动预览失败: %v", err)
		}
		fmt.Println(previewResult) // 打印预览启动信息
	}

	// 启动捕获协程
	go func() {
		defer func() {
			// 捕获结束后清理
			t.stopAll()
			fmt.Println("📹 实时捕获协程结束")
		}()

		// 设置捕获时长控制
		var captureCtx context.Context = ctx
		var captureCancel context.CancelFunc

		if duration > 0 {
			captureCtx, captureCancel = context.WithTimeout(ctx, time.Duration(duration)*time.Second)
			defer captureCancel()
		}

		// 计算帧间隔
		interval := time.Second / time.Duration(fps)
		if interval < 10*time.Millisecond {
			interval = 10 * time.Millisecond // 最小间隔10ms，避免过高的CPU占用
		}

		frameCount := 0
		errorCount := 0
		maxErrors := 10 // 连续错误超过10次后停止捕获

		fmt.Printf("📸 开始实时捕获，帧率: %d fps，间隔: %v\n", fps, interval)

		// 主捕获循环
		for {
			select {
			case <-captureCtx.Done():
				// 捕获结束（超时或被取消）
				summaryMsg := fmt.Sprintf("⏹ 实时捕获结束，共捕获 %d 帧", frameCount)
				select {
				case t.captureChan <- summaryMsg:
				default:
					// 通道满时跳过，避免阻塞
				}
				return

			default:
				// 生成帧文件名
				timestamp := time.Now().Format("20060102_150405")
				frameName := fmt.Sprintf("frame_%s_%06d.jpg", timestamp, frameCount)
				framePath := filepath.Join(outputDir, frameName)

				// 根据操作系统构建捕获命令
				var cmd *exec.Cmd
				switch runtime.GOOS {
				case "windows":
					// Windows: 使用 dshow 设备
					cmd = exec.Command("ffmpeg", "-y",
						"-f", "dshow",
						"-i", "video=Integrated Camera",
						"-frames:v", "1",
						"-q:v", "2",
						"-vsync", "0", // 禁用帧同步
						"-an", // 无音频
						framePath)
				case "darwin":
					// macOS: 使用 avfoundation
					cmd = exec.Command("ffmpeg", "-y",
						"-f", "avfoundation",
						"-i", "0:0", // 默认摄像头:麦克风
						"-frames:v", "1",
						"-q:v", "2",
						"-vsync", "0",
						"-an",
						framePath)
				case "linux":
					// Linux: 使用 v4l2
					cmd = exec.Command("ffmpeg", "-y",
						"-f", "v4l2",
						"-i", "/dev/video0",
						"-frames:v", "1",
						"-q:v", "2",
						"-vsync", "0",
						"-an",
						framePath)
				default:
					errMsg := fmt.Sprintf("❌ 不支持的操作系统: %s", runtime.GOOS)
					select {
					case t.captureChan <- errMsg:
					default:
					}
					return
				}

				// 执行捕获
				if err := cmd.Run(); err == nil {
					// 捕获成功
					frameCount++
					successMsg := fmt.Sprintf("📸 捕获帧 %d: %s", frameCount, framePath)

					select {
					case t.captureChan <- successMsg:
					default:
						// 通道满时只保留最新的几条消息
						// 这里简单处理：清空通道后发送
						select {
						case <-t.captureChan:
						default:
						}
						select {
						case t.captureChan <- successMsg + " (队列满，丢弃旧消息)":
						default:
						}
					}

					errorCount = 0 // 重置错误计数
				} else {
					// 捕获失败
					errorCount++
					errMsg := fmt.Sprintf("❌ 捕获帧失败 (错误 %d/%d): %v", errorCount, maxErrors, err)

					select {
					case t.captureChan <- errMsg:
					default:
					}

					// 如果连续错误太多，自动停止
					if errorCount >= maxErrors {
						stopMsg := fmt.Sprintf("⏹ 连续错误 %d 次，停止捕获", maxErrors)
						select {
						case t.captureChan <- stopMsg:
						default:
						}
						return
					}
				}

				// 控制捕获帧率
				time.Sleep(interval)
			}
		}
	}()

	// 启动日志打印协程（非阻塞）
	go func() {
		for {
			select {
			case msg, ok := <-t.captureChan:
				if !ok {
					// 通道已关闭（理论上不会发生，因为我们不关闭通道）
					return
				}
				fmt.Println(msg)
			case <-ctx.Done():
				// 捕获结束，退出打印协程
				return
			}
		}
	}()

	// 构建返回信息
	infoBuilder := strings.Builder{}
	infoBuilder.WriteString("🚀 实时捕获已启动！\n")
	infoBuilder.WriteString(fmt.Sprintf("📊 帧率：%d 帧/秒\n", fps))
	if preview {
		infoBuilder.WriteString("👁 预览窗口：已开启\n")
	} else {
		infoBuilder.WriteString("👁 预览窗口：未开启\n")
	}
	if duration > 0 {
		infoBuilder.WriteString(fmt.Sprintf("⏱ 捕获时长：%d 秒\n", duration))
	} else {
		infoBuilder.WriteString("⏱ 捕获时长：无限（执行 stop 停止）\n")
	}
	infoBuilder.WriteString(fmt.Sprintf("保存目录：%s\n", outputDir))
	infoBuilder.WriteString("💡 实时日志将显示在控制台")

	return infoBuilder.String(), nil
}

// 辅助函数：检查摄像头是否可用
func (t *CameraTool) CheckCamera() (bool, string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("ffmpeg", "-f", "dshow", "-list_devices", "true", "-i", "dummy")
	case "darwin":
		cmd = exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "dummy")
	case "linux":
		cmd = exec.Command("ffmpeg", "-f", "v4l2", "-list_formats", "all", "-i", "/dev/video0")
	default:
		return false, "不支持的系统"
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		// 忽略ffmpeg的非零退出码，只检查输出
		if runtime.GOOS == "windows" && string(out) == "" {
			return false, "未检测到摄像头（dshow设备为空）"
		}
	}

	if runtime.GOOS == "windows" && !strings.Contains(string(out), "Integrated Camera") {
		return false, "未检测到默认摄像头，请检查设备名称"
	}

	return true, "摄像头可用"
}

func index(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// AudioTool 基础音频工具（仅录音和播放）
type AudioTool struct {
	workspace   string
	mu          sync.Mutex
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	isPlaying   bool
	isRecording bool
}

func NewAudioTool(workspace string) *AudioTool {
	audioDir := filepath.Join(workspace, "audio")
	_ = os.MkdirAll(audioDir, 0755)

	return &AudioTool{
		workspace: workspace,
	}
}

func (t *AudioTool) Name() string { return "audio" }

func (t *AudioTool) Description() string {
	return `基础音频操作：录音和播放。
依赖：ffmpeg/ffplay（需安装并加入PATH）

支持操作：
- record: 录音（麦克风输入）
- play: 播放音频文件
- stop: 停止当前录音或播放
- list_devices: 列出可用的音频设备
- check: 检查音频系统状态`
}

func (t *AudioTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"record", "play", "stop", "list_devices", "check"},
				"description": "操作类型",
			},
			"duration": map[string]interface{}{
				"type":        "integer",
				"description": "录音时长（秒），record时必填",
				"minimum":     1,
				"maximum":     300,
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "录音输出路径（默认 workspace/audio/record_时间.wav）",
			},
			"input": map[string]interface{}{
				"type":        "string",
				"description": "要播放的音频文件路径",
			},
			"device": map[string]interface{}{
				"type":        "string",
				"description": "音频设备名称（不指定则使用默认设备）",
			},
			"volume": map[string]interface{}{
				"type":        "integer",
				"description": "播放音量（0-100），默认100",
				"minimum":     0,
				"maximum":     100,
			},
		},
		"required": []string{"action"},
	}
}

func (t *AudioTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)

	// 先检查ffmpeg是否可用
	if action != "check" && action != "list_devices" {
		if err := t.checkFFmpeg(); err != nil {
			return "", fmt.Errorf("ffmpeg未安装: %v\n请访问 https://ffmpeg.org/download.html 安装", err)
		}
	}

	switch action {
	case "record":
		return t.record(params)
	case "play":
		return t.play(params)
	case "stop":
		return t.stop()
	case "list_devices":
		return t.listDevices()
	case "check":
		return t.checkSystem()
	default:
		return "", fmt.Errorf("未知操作: %s", action)
	}
}

// checkFFmpeg 检查ffmpeg是否可用
func (t *AudioTool) checkFFmpeg() error {
	ffmpegPath, err := processutil.ResolveFFmpegBinary(context.Background(), t.workspace, "ffmpeg", true)
	if err != nil {
		return err
	}
	cmd := exec.Command(ffmpegPath, "-version")
	return cmd.Run()
}

// checkSystem 检查音频系统状态
func (t *AudioTool) checkSystem() (string, error) {
	var result strings.Builder
	result.WriteString("🔊 音频系统检查\n")
	result.WriteString(strings.Repeat("=", 50) + "\n\n")

	// 检查ffmpeg
	if err := t.checkFFmpeg(); err == nil {
		result.WriteString("✅ ffmpeg: 已安装\n")
	} else {
		result.WriteString("❌ ffmpeg: 未安装\n")
	}

	// 检查ffplay
	if err := exec.Command("ffplay", "-version").Run(); err == nil {
		result.WriteString("✅ ffplay: 已安装\n")
	} else {
		result.WriteString("❌ ffplay: 未安装\n")
	}

	// 列出音频设备
	devices, _ := t.getAudioDevices()
	result.WriteString(fmt.Sprintf("\n🎤 检测到 %d 个音频输入设备\n", len(devices)))
	for i, dev := range devices {
		result.WriteString(fmt.Sprintf("   %d. %s\n", i+1, dev))
	}

	result.WriteString("\n📝 使用说明：\n")
	result.WriteString("1. 录音: audio record duration=10\n")
	result.WriteString("2. 播放: audio play input=./hello.wav\n")
	result.WriteString("3. 停止: audio stop\n")

	return result.String(), nil
}

// record 录音
func (t *AudioTool) record(params map[string]interface{}) (string, error) {
	t.mu.Lock()
	if t.isRecording || t.isPlaying {
		t.mu.Unlock()
		return "", fmt.Errorf("已有音频操作在进行中，请先执行 stop")
	}
	t.mu.Unlock()

	duration, ok := params["duration"].(float64)
	if !ok || duration <= 0 {
		return "", fmt.Errorf("需要有效的 duration 参数（秒）")
	}

	output, _ := params["output"].(string)
	if output == "" {
		audioDir := filepath.Join(t.workspace, "audio")
		_ = os.MkdirAll(audioDir, 0755)
		output = filepath.Join(audioDir, fmt.Sprintf("record_%s.wav", time.Now().Format("20060102_150405")))
	}

	device, _ := params["device"].(string)

	// 构建ffmpeg命令
	var args []string

	switch runtime.GOOS {
	case "windows":
		// Windows: 使用dshow
		if device == "" {
			// 尝试自动检测可用的音频设备
			devices, _ := t.getAudioDevices()
			if len(devices) > 0 {
				device = devices[0]
				fmt.Printf("自动选择音频设备: %s\n", device)
			} else {
				device = "virtual-audio-capturer" // Windows默认
			}
		}
		args = []string{
			"-y",          // 覆盖输出文件
			"-f", "dshow", // Windows驱动
			"-i", fmt.Sprintf("audio=%s", device), // 音频设备
			"-t", fmt.Sprintf("%.0f", duration), // 录音时长
			"-acodec", "pcm_s16le", // PCM编码
			"-ar", "44100", // 采样率 44.1kHz
			"-ac", "2", // 双声道
			"-af", "volume=2.0", // 音量增益
			output,
		}

	case "darwin":
		// macOS: 使用avfoundation
		if device == "" {
			device = ":0" // 默认麦克风
		}
		args = []string{
			"-y",
			"-f", "avfoundation",
			"-i", device,
			"-t", fmt.Sprintf("%.0f", duration),
			"-acodec", "pcm_s16le",
			"-ar", "44100",
			"-ac", "2",
			output,
		}

	case "linux":
		// Linux: 使用pulse或alsa
		if device == "" {
			device = "default"
		}
		args = []string{
			"-y",
			"-f", "pulse",
			"-i", device,
			"-t", fmt.Sprintf("%.0f", duration),
			"-acodec", "pcm_s16le",
			"-ar", "44100",
			"-ac", "2",
			output,
		}

	default:
		return "", fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	// 打印命令（调试用）
	fmt.Printf("执行命令: ffmpeg %v\n", args)

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())
	ffmpegPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffmpeg", true)
	if err != nil {
		cancel()
		return "", fmt.Errorf("ffmpeg unavailable: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	// 捕获输出
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("创建错误管道失败: %v", err)
	}

	// 启动命令
	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("启动录音失败: %v", err)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.cancel = cancel
	t.isRecording = true
	t.mu.Unlock()

	// 异步等待完成
	go func() {
		// 读取错误输出
		errOutput, _ := io.ReadAll(stderr)
		if len(errOutput) > 0 {
			fmt.Printf("录音调试: %s\n", string(errOutput))
		}

		err := cmd.Wait()

		t.mu.Lock()
		t.cmd = nil
		t.cancel = nil
		t.isRecording = false
		t.mu.Unlock()

		if err != nil && ctx.Err() != context.Canceled {
			fmt.Printf("❌ 录音失败: %v\n", err)
		} else {
			// 获取文件大小
			if info, err := os.Stat(output); err == nil {
				size := info.Size()
				fmt.Printf("✅ 录音完成: %s (%.1f KB)\n", output, float64(size)/1024)
			}
		}
	}()

	deviceInfo := device
	if deviceInfo == "" {
		deviceInfo = "默认设备"
	}

	return fmt.Sprintf("🎤 开始录音 %d 秒\n📁 保存至: %s\n🎚 音频设备: %s\n💡 执行 stop 可提前结束",
		int(duration), output, deviceInfo), nil
}

// play 播放音频
func (t *AudioTool) play(params map[string]interface{}) (string, error) {
	t.mu.Lock()
	if t.isRecording || t.isPlaying {
		t.mu.Unlock()
		return "", fmt.Errorf("已有音频操作在进行中，请先执行 stop")
	}
	t.mu.Unlock()

	input, _ := params["input"].(string)
	if input == "" {
		return "", fmt.Errorf("需要 input 参数指定播放文件")
	}

	if _, err := os.Stat(input); err != nil {
		return "", fmt.Errorf("播放文件不存在: %v", err)
	}

	volume := 100
	if v, ok := params["volume"].(float64); ok {
		volume = int(v)
	}

	// 构建ffplay命令
	args := []string{
		"-nodisp",            // 无显示窗口
		"-autoexit",          // 播放完后自动退出
		"-loglevel", "error", // 只显示错误
		"-volume", fmt.Sprintf("%d", volume), // 音量
		input,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ffplayPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffplay", true)
	if err != nil {
		cancel()
		return "", fmt.Errorf("ffplay unavailable: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffplayPath, args...)

	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("启动播放失败: %v", err)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.cancel = cancel
	t.isPlaying = true
	t.mu.Unlock()

	go func() {
		err := cmd.Wait()

		t.mu.Lock()
		t.cmd = nil
		t.cancel = nil
		t.isPlaying = false
		t.mu.Unlock()

		if err != nil && ctx.Err() != context.Canceled {
			fmt.Printf("❌ 播放失败: %v\n", err)
		} else {
			fmt.Printf("✅ 播放完成: %s\n", input)
		}
	}()

	return fmt.Sprintf("🔊 开始播放: %s (音量: %d%%)\n💡 执行 stop 可停止播放", input, volume), nil
}

// stop 停止当前录音或播放
func (t *AudioTool) stop() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}

	if t.cmd != nil && t.cmd.Process != nil {
		// 尝试优雅停止
		if runtime.GOOS == "windows" {
			t.cmd.Process.Signal(os.Interrupt)
			time.Sleep(100 * time.Millisecond)
		}
		t.cmd.Process.Kill()
		t.cmd = nil
	}

	wasRecording := t.isRecording
	wasPlaying := t.isPlaying

	t.isRecording = false
	t.isPlaying = false

	if wasRecording {
		return "⏹ 录音已停止", nil
	} else if wasPlaying {
		return "⏹ 播放已停止", nil
	} else {
		return "⏹ 没有正在进行的音频操作", nil
	}
}

// listDevices 列出音频设备
func (t *AudioTool) listDevices() (string, error) {
	var result strings.Builder
	result.WriteString("🎤 音频设备列表\n")
	result.WriteString(strings.Repeat("=", 50) + "\n\n")

	devices, err := t.getAudioDevices()
	if err != nil {
		result.WriteString(fmt.Sprintf("获取设备失败: %v\n", err))
	} else {
		if len(devices) == 0 {
			result.WriteString("未找到音频输入设备\n")
		} else {
			result.WriteString("音频输入设备（麦克风）:\n")
			for i, dev := range devices {
				result.WriteString(fmt.Sprintf("  [%d] %s\n", i+1, dev))
			}
		}
	}

	// 尝试获取播放设备
	result.WriteString("\n🔊 音频输出设备（扬声器/耳机）:\n")
	result.WriteString("  [默认] 系统默认输出设备\n")

	result.WriteString("\n📝 使用说明：\n")
	result.WriteString("1. 录音时指定设备: device=\"设备名称\"\n")
	result.WriteString("2. 例如: audio record duration=5 device=\"麦克风 (Realtek Audio)\"\n")
	result.WriteString("3. 不指定设备将自动选择第一个可用设备\n")

	return result.String(), nil
}

// getAudioDevices 获取音频设备列表
func (t *AudioTool) getAudioDevices() ([]string, error) {
	var devices []string

	switch runtime.GOOS {
	case "windows":
		// 使用ffmpeg列出dshow设备
		cmd := exec.Command("ffmpeg", "-f", "dshow", "-list_devices", "true", "-i", "dummy")
		out, _ := cmd.CombinedOutput()
		output := string(out)

		lines := strings.Split(output, "\n")
		inAudioSection := false

		for _, line := range lines {
			line = strings.TrimSpace(line)

			if strings.Contains(line, "DirectShow audio devices") {
				inAudioSection = true
				continue
			}
			if strings.Contains(line, "DirectShow video devices") {
				inAudioSection = false
				continue
			}

			if inAudioSection && strings.Contains(line, "\"") {
				// 提取引号内的设备名
				parts := strings.Split(line, "\"")
				if len(parts) >= 2 {
					deviceName := strings.TrimSpace(parts[1])
					if deviceName != "" {
						devices = append(devices, deviceName)
					}
				}
			}
		}

	case "darwin":
		// macOS: 使用avfoundation列出设备
		cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "dummy")
		out, _ := cmd.CombinedOutput()
		output := string(out)

		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "AVFoundation audio devices") {
				// 解析设备名
				// 这里简化处理，返回默认值
				devices = append(devices, ":0")
			}
		}

	case "linux":
		// Linux: 默认设备
		devices = append(devices, "default")
		devices = append(devices, "hw:0")
	}

	// 如果没找到设备，添加一些常见默认值
	if len(devices) == 0 {
		if runtime.GOOS == "windows" {
			devices = append(devices, "麦克风 (Realtek Audio)")
			devices = append(devices, "Microphone (Realtek Audio)")
			devices = append(devices, "virtual-audio-capturer")
		}
	}

	return devices, nil
}
