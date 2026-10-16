package processutil

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ffmpegInstallMu sync.Mutex

type ffmpegArchive struct {
	url string
}

func ResolveFFmpegBinary(ctx context.Context, workspace, name string, autoDownload bool) (string, error) {
	if name != "ffmpeg" && name != "ffprobe" && name != "ffplay" {
		return "", fmt.Errorf("unsupported ffmpeg binary: %s", name)
	}

	if path := findLocalFFmpegBinary(workspace, name); path != "" {
		return path, nil
	}
	if path, err := exec.LookPath(binaryName(name)); err == nil {
		return path, nil
	}
	if !autoDownload {
		return "", fmt.Errorf("%s not found\n%s", name, ffmpegInstallHint(workspace, name))
	}

	ffmpegInstallMu.Lock()
	defer ffmpegInstallMu.Unlock()

	if path := findLocalFFmpegBinary(workspace, name); path != "" {
		return path, nil
	}
	if err := downloadFFmpegBinary(ctx, workspace, name); err != nil {
		return "", fmt.Errorf("%w\n%s", err, ffmpegInstallHint(workspace, name))
	}
	if path := findLocalFFmpegBinary(workspace, name); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%s download completed but binary was not found", name)
}

func findLocalFFmpegBinary(workspace, name string) string {
	candidates := []string{}
	if dir := strings.TrimSpace(os.Getenv("OPENKITTY_FFMPEG_DIR")); dir != "" {
		candidates = append(candidates, filepath.Join(dir, binaryName(name)))
	}
	candidates = append(candidates, filepath.Join(ffmpegBinDir(workspace), binaryName(name)))
	for _, candidate := range candidates {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
	}
	return ""
}

func ffmpegBinDir(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "tools", "ffmpeg", runtime.GOOS+"-"+runtime.GOARCH, "bin")
}

func ffmpegDownloadDir(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "tools", "ffmpeg", "_downloads")
}

func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func downloadFFmpegBinary(ctx context.Context, workspace, name string) error {
	archives, err := ffmpegArchives(name)
	if err != nil {
		return err
	}
	binDir := ffmpegBinDir(workspace)
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	downloadDir := ffmpegDownloadDir(workspace)
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return err
	}

	var lastErr error
	for idx, archive := range archives {
		zipPath := filepath.Join(downloadDir, fmt.Sprintf("%s_%s_%s_%d.zip", name, runtime.GOOS, runtime.GOARCH, idx))
		downloadCtx, cancel := context.WithTimeout(ctx, ffmpegDownloadTimeout())
		err := downloadFFmpegArchive(downloadCtx, archive.url, zipPath)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if err := extractBinaryFromZip(zipPath, binDir, name); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("download %s failed: %w", name, lastErr)
	}
	return fmt.Errorf("no archive candidates for %s", name)
}

func ffmpegArchives(name string) ([]ffmpegArchive, error) {
	archives := customFFmpegArchives(name)

	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			if name == "ffprobe" {
				archives = append(archives,
					ffmpegArchive{url: "https://www.novaexel.com/download/ffprobe-6.1-win-64.zip"},
				)
			}
			archives = append(archives,
				ffmpegArchive{url: fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-win-64.zip", name)},
				ffmpegArchive{url: "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip"},
				ffmpegArchive{url: "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"},
			)
		case "arm64":
			archives = append(archives,
				ffmpegArchive{url: "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-winarm64-gpl.zip"},
			)
		default:
			return nil, fmt.Errorf("automatic ffmpeg download is unsupported on windows/%s", runtime.GOARCH)
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			archives = append(archives,
				ffmpegArchive{url: fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-linux-64.zip", name)},
				ffmpegArchive{url: "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.zip"},
			)
		case "arm64":
			archives = append(archives,
				ffmpegArchive{url: fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-linux-arm-64.zip", name)},
				ffmpegArchive{url: "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarm64-gpl.zip"},
			)
		default:
			return nil, fmt.Errorf("automatic ffmpeg download is unsupported on linux/%s", runtime.GOARCH)
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			archives = append(archives,
				ffmpegArchive{url: fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-macos-64.zip", name)},
				ffmpegArchive{url: fmt.Sprintf("https://evermeet.cx/ffmpeg/getrelease/%s/zip", name)},
				ffmpegArchive{url: fmt.Sprintf("https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/%s.zip", name)},
			)
		case "arm64":
			archives = append(archives,
				ffmpegArchive{url: fmt.Sprintf("https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/%s.zip", name)},
			)
		default:
			return nil, fmt.Errorf("automatic ffmpeg download is unsupported on darwin/%s", runtime.GOARCH)
		}
	default:
		return nil, fmt.Errorf("automatic ffmpeg download is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	if len(archives) == 0 {
		return nil, fmt.Errorf("no archive candidates for %s on %s/%s", name, runtime.GOOS, runtime.GOARCH)
	}
	return archives, nil
}

func customFFmpegArchives(name string) []ffmpegArchive {
	envKeys := []string{
		"OPENKITTY_" + strings.ToUpper(name) + "_URL",
		"OPENKITTY_FFMPEG_" + strings.ToUpper(name) + "_URL",
		"OPENKITTY_FFMPEG_ARCHIVE_URL",
	}
	if name == "ffmpeg" {
		envKeys = append([]string{"OPENKITTY_FFMPEG_URL"}, envKeys...)
	}
	archives := make([]ffmpegArchive, 0, len(envKeys))
	seen := map[string]bool{}
	for _, key := range envKeys {
		url := strings.TrimSpace(os.Getenv(key))
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		archives = append(archives, ffmpegArchive{url: url})
	}
	return archives
}

func downloadFFmpegArchive(ctx context.Context, url, output string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "OpenKitty/ffmpeg-resolver")

	client := &http.Client{Timeout: ffmpegDownloadTimeout() + 10*time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned %s", url, resp.Status)
	}

	tmp := output + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, output)
}

func ffmpegDownloadTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("OPENKITTY_FFMPEG_DOWNLOAD_TIMEOUT_SEC"))
	if raw == "" {
		return 180 * time.Second
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 180 * time.Second
	}
	if seconds < 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func ffmpegInstallHint(workspace, name string) string {
	target := filepath.Join(ffmpegBinDir(workspace), binaryName(name))
	urls := ffmpegManualURLs(name)
	var builder strings.Builder
	builder.WriteString("请手动下载对应平台的便携版 zip，解压后把 ")
	builder.WriteString(binaryName(name))
	builder.WriteString(" 放到：")
	builder.WriteString(target)
	if len(urls) > 0 {
		builder.WriteString("\n可用下载地址：")
		for _, url := range urls {
			builder.WriteString("\n- ")
			builder.WriteString(url)
		}
	}
	builder.WriteString("\n也可以设置 OPENKITTY_FFMPEG_DIR 指向包含 ffmpeg/ffprobe 的 bin 目录。")
	return builder.String()
}

func ffmpegManualURLs(name string) []string {
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			if name == "ffprobe" {
				return []string{
					"https://www.novaexel.com/download/ffprobe-6.1-win-64.zip",
					"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/ffprobe-6.1-win-64.zip",
				}
			}
			if name == "ffmpeg" {
				return []string{
					"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/ffmpeg-6.1-win-64.zip",
				}
			}
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			return []string{
				fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-macos-64.zip", name),
			}
		case "arm64":
			return []string{
				fmt.Sprintf("https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/%s.zip", name),
			}
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return []string{
				fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-linux-64.zip", name),
			}
		case "arm64":
			return []string{
				fmt.Sprintf("https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v6.1/%s-6.1-linux-arm-64.zip", name),
			}
		}
	}
	return nil
}

func extractBinaryFromZip(zipPath, binDir, name string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	targetName := binaryName(name)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != targetName {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		target := filepath.Join(binDir, targetName)
		tmp := target + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(tmp)
			return copyErr
		}
		if closeErr != nil {
			_ = os.Remove(tmp)
			return closeErr
		}
		if runtime.GOOS != "windows" {
			_ = os.Chmod(tmp, 0755)
		}
		return os.Rename(tmp, target)
	}
	return fmt.Errorf("%s not found inside %s", targetName, zipPath)
}
