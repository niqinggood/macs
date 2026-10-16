package operations

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mac/internal/textutil"
)

// UpdateInfo describes an available update.
type UpdateInfo struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	ReleaseDate string `json:"release_date"`
	ChangeLog   string `json:"changelog"`
	Size        int64  `json:"size"`
}

// UpdateProgress tracks download progress for the UI.
type UpdateProgress struct {
	TaskID     string `json:"task_id"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	Percent    int    `json:"percent"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
}

// UpdateChecker handles automatic update detection and installation.
type UpdateChecker struct {
	repoOwner  string // GitHub repo owner
	repoName   string // GitHub repo name
	currentVer string
	client     *http.Client
	logger     *log.Logger
	updateURL  string // Custom update URL (overrides GitHub API)
}

func NewUpdateChecker(repoOwner, repoName, currentVer string) *UpdateChecker {
	return &UpdateChecker{
		repoOwner:  repoOwner,
		repoName:   repoName,
		currentVer: currentVer,
		client:     &http.Client{Timeout: 15 * time.Second},
		logger:     log.New(os.Stdout, "[Update] ", log.LstdFlags),
	}
}

// SetCustomURL sets a custom update manifest URL (for self-hosted updates).
func (u *UpdateChecker) SetCustomURL(url string) {
	u.updateURL = url
}

// CheckForUpdate queries the update source and returns info if a newer version exists.
// Returns nil if current is latest.
func (u *UpdateChecker) CheckForUpdate() (*UpdateInfo, error) {
	if u.currentVer == "dev" {
		u.logger.Println("开发版本，跳过更新检查")
		return nil, nil
	}

	var info *UpdateInfo
	var err error

	if u.updateURL != "" {
		info, err = u.checkCustomURL()
	} else {
		info, err = u.checkGitHubReleases()
	}

	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}

	// Compare versions
	if !IsNewer(info.Version, u.currentVer) {
		u.logger.Printf("当前已是最新版本 %s", u.currentVer)
		return nil, nil
	}

	u.logger.Printf("发现新版本: %s -> %s", u.currentVer, info.Version)
	return info, nil
}

// checkGitHubReleases queries the GitHub Releases API for the latest release.
func (u *UpdateChecker) checkGitHubReleases() (*UpdateInfo, error) {
	if u.repoOwner == "" || u.repoName == "" {
		return nil, fmt.Errorf("GitHub repo not configured")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", u.repoOwner, u.repoName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "openkitty-updater")

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("更新源暂不可用(HTTP %d)，请稍后重试", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
			Size        int64  `json:"size"`
		} `json:"assets"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse release failed: %w", err)
	}

	version := strings.TrimPrefix(release.TagName, "v")

	// Find matching asset for current OS/arch
	assetName := fmt.Sprintf("openkitty_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	var downloadURL string
	var size int64
	for _, a := range release.Assets {
		if strings.Contains(strings.ToLower(a.Name), strings.ToLower(runtime.GOOS)) &&
			strings.Contains(a.Name, runtime.GOARCH) {
			downloadURL = a.DownloadURL
			size = a.Size
			break
		}
	}
	// Fallback: first asset
	if downloadURL == "" && len(release.Assets) > 0 {
		downloadURL = release.Assets[0].DownloadURL
		size = release.Assets[0].Size
	}

	_ = assetName // Use for exact match in future

	return &UpdateInfo{
		Version:     version,
		DownloadURL: downloadURL,
		ReleaseDate: release.PublishedAt,
		ChangeLog:   release.Body,
		Size:        size,
	}, nil
}

// checkCustomURL queries a self-hosted update manifest.
// Supports both flat format (GitHub-style) and nested "latest" format (novaexel API).
func (u *UpdateChecker) checkCustomURL() (*UpdateInfo, error) {
	resp, err := u.client.Get(u.updateURL)
	if err != nil {
		return nil, fmt.Errorf("update server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		msg := map[int]string{
			502: "更新服务器暂时故障(502)，请稍后重试或访问 novaexel.com 手动下载",
			503: "更新服务器正在维护(503)，请稍后重试",
			404: "未找到更新信息，可能产品未上架",
			500: "更新服务器内部错误(500)，请稍后重试",
		}[resp.StatusCode]
		if msg == "" {
			msg = fmt.Sprintf("更新服务器返回异常状态 %d，请稍后重试", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	// Read full body so we can try multiple parse strategies
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Strategy 1: novaexel nested format {"latest":{"version":...,"url":...}}
	var nested struct {
		Latest *struct {
			Version      string `json:"version"`
			URL          string `json:"url"`
			ReleaseDate  string `json:"release_date"`
			ReleaseNotes string `json:"release_notes"`
			Size         int64  `json:"size"`
			Checksum     string `json:"checksum"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Latest != nil && nested.Latest.Version != "" {
		return &UpdateInfo{
			Version:     nested.Latest.Version,
			DownloadURL: nested.Latest.URL,
			ReleaseDate: nested.Latest.ReleaseDate,
			ChangeLog:   nested.Latest.ReleaseNotes,
			Size:        nested.Latest.Size,
		}, nil
	}

	// Strategy 2: flat format {"version":..., "download_url":..., ...}
	var flat struct {
		Version     string `json:"version"`
		DownloadURL string `json:"download_url"`
		ChangeLog   string `json:"changelog"`
		ReleaseDate string `json:"release_date"`
		Size        int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &flat); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}

	return &UpdateInfo{
		Version:     flat.Version,
		DownloadURL: flat.DownloadURL,
		ReleaseDate: flat.ReleaseDate,
		ChangeLog:   flat.ChangeLog,
		Size:        flat.Size,
	}, nil
}

// progressWriter wraps an io.Writer and calls a callback with bytes written.
type progressWriter struct {
	w        io.Writer
	total    int64
	written  int64
	callback func(written, total int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.written += int64(n)
	if pw.callback != nil {
		pw.callback(pw.written, pw.total)
	}
	return n, err
}

// DownloadUpdate downloads the new binary to a temp file.
// If progress is non-nil, it's called with (bytesWritten, totalBytes) during download.
func (u *UpdateChecker) DownloadUpdate(info *UpdateInfo, progress func(int64, int64)) (string, error) {
	if info.DownloadURL == "" {
		return "", fmt.Errorf("no download URL")
	}

	u.logger.Printf("下载更新: %s (%d bytes)", info.Version, info.Size)

	dlClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := dlClient.Get(info.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载失败，服务器返回 %d，请检查网络或手动下载", resp.StatusCode)
	}

	totalSize := resp.ContentLength

	dlName := info.DownloadURL
	if idx := strings.LastIndex(dlName, "/"); idx >= 0 {
		dlName = dlName[idx+1:]
	}
	if idx := strings.Index(dlName, "?"); idx >= 0 {
		dlName = dlName[:idx]
	}
	tmpFile := filepath.Join(os.TempDir(), dlName)

	f, err := os.Create(tmpFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Wrap with progress tracking
	pw := &progressWriter{w: f, total: totalSize, callback: progress}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		os.Remove(tmpFile)
		return "", err
	}

	u.logger.Printf("下载完成: %s", tmpFile)
	return tmpFile, nil
}

// ApplyUpdate replaces the current binary with the new one and restarts.
// On Windows: creates a batch script to do the swap (can't replace running exe).
// On Linux/macOS: renames old, moves new, restarts.
func (u *UpdateChecker) ApplyUpdate(newBinary string) error {
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find current executable: %w", err)
	}

	u.logger.Printf("应用更新: %s -> %s", currentExe, newBinary)

	switch runtime.GOOS {
	case "windows":
		return u.applyWindows(currentExe, newBinary)
	default:
		return u.applyUnix(currentExe, newBinary)
	}
}

func (u *UpdateChecker) applyWindows(currentExe, newBinary string) error {
	// Windows can't delete a running exe, so we create a batch script
	// that waits for the process to exit, then swaps files and restarts
	backupExe := currentExe + ".bak"
	scriptPath := filepath.Join(os.TempDir(), "openkitty_update.bat")

	targetDir := filepath.Dir(currentExe)
	targetExe := filepath.Join(targetDir, "openkitty.exe")

	script := fmt.Sprintf(`@echo off
echo Updating openkitty...
:retry
timeout /t 1 /nobreak > nul
move /Y "%s" "%s" >nul 2>&1
if exist "%s" goto retry
move /Y "%s" "%s"
echo Done. Restarting...
start "" "%s"
del "%%~f0"
`, currentExe, backupExe, currentExe, newBinary, targetExe, targetExe)

	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		return err
	}

	// Run the batch script and exit
	cmd := exec.Command("cmd", "/c", scriptPath)
	cmd.Start()

	u.logger.Println("更新脚本已启动，正在退出...")
	os.Exit(0)
	return nil
}

func (u *UpdateChecker) applyUnix(currentExe, newBinary string) error {
	backupExe := currentExe + ".bak"

	// Backup current
	if err := os.Rename(currentExe, backupExe); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Move new binary in place
	if err := os.Rename(newBinary, currentExe); err != nil {
		// Try to restore backup
		os.Rename(backupExe, currentExe)
		return fmt.Errorf("replace failed: %w", err)
	}

	// Make executable
	os.Chmod(currentExe, 0755)

	u.logger.Println("更新完成，重启中...")

	// Restart
	cmd := exec.Command(currentExe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Start()

	os.Exit(0)
	return nil
}

// StartBackgroundChecker runs periodic update checks in the background.
func (u *UpdateChecker) StartBackgroundChecker(interval time.Duration) {
	u.logger.Printf("后台更新检查已启动 (间隔: %v)", interval)

	go func() {
		// Initial check after 30 seconds
		time.Sleep(30 * time.Second)

		for {
			info, err := u.CheckForUpdate()
			if err != nil {
				u.logger.Printf("更新检查失败: %v", err)
			} else if info != nil {
				u.logger.Printf("📦 新版本可用: %s -> %s", u.currentVer, info.Version)
				u.logger.Printf("   更新日志: %s", textutil.Truncate(info.ChangeLog, 200))
				if err := u.AutoUpdate(); err != nil {
					u.logger.Printf("自动更新失败: %v", err)
				}
			}

			time.Sleep(interval)
		}
	}()
}

// AutoUpdate checks, downloads, and applies updates automatically.
// Use with caution — only enable if user opts in.
func (u *UpdateChecker) AutoUpdate() error {
	info, err := u.CheckForUpdate()
	if err != nil {
		return err
	}
	if info == nil {
		return nil // Already latest
	}

	newBinary, err := u.DownloadUpdate(info, nil)
	if err != nil {
		return err
	}

	return u.ApplyUpdate(newBinary)
}

// isNewer compares two semver-like version strings.
func IsNewer(new, current string) bool {
	// Strip 'v' prefix
	new = strings.TrimPrefix(new, "v")
	current = strings.TrimPrefix(current, "v")

	np := strings.Split(new, ".")
	cp := strings.Split(current, ".")

	for i := 0; i < len(np) && i < len(cp); i++ {
		var nv, cv int
		fmt.Sscanf(np[i], "%d", &nv)
		fmt.Sscanf(cp[i], "%d", &cv)

		if nv > cv {
			return true
		}
		if nv < cv {
			return false
		}
	}

	// All compared parts equal; longer version (e.g. 1.0.1 vs 1.0) wins
	return len(np) > len(cp)
}
