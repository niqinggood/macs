package browserruntime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	securitytools "mac/internal/tools/security"
)

const (
	chromeForTestingManifestURL       = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"
	managedBrowserMaxManifestBytes    = int64(4 << 20)
	managedBrowserMaxArchiveBytes     = int64(600 << 20)
	managedBrowserMaxExtractedBytes   = int64(1500 << 20)
	managedBrowserMaxArchiveEntries   = 25000
	managedBrowserInstallLockMaxAge   = 30 * time.Minute
	managedBrowserInstallPollInterval = 250 * time.Millisecond
)

type managedBrowserDownload struct {
	Version  string
	Platform string
	URL      string
}

type managedBrowserMetadata struct {
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Executable    string `json:"executable"`
	ArchiveSHA256 string `json:"archive_sha256"`
	InstalledAt   string `json:"installed_at"`
}

type managedBrowserInstaller struct {
	root        string
	goos        string
	goarch      string
	manifestURL string
	client      *http.Client
}

type chromeForTestingManifest struct {
	Channels map[string]struct {
		Version   string `json:"version"`
		Downloads map[string][]struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
		} `json:"downloads"`
	} `json:"channels"`
}

func managedBrowserAutoInstallEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_BROWSER_AUTO_INSTALL")))
	return value != "0" && value != "false" && value != "no"
}

func CacheRoot(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if workspace == "" {
		workspace = "."
	}
	if configured := strings.TrimSpace(os.Getenv("OPENKITTY_BROWSER_CACHE_DIR")); configured != "" {
		if filepath.IsAbs(configured) {
			return filepath.Clean(configured)
		}
		return filepath.Clean(filepath.Join(workspace, configured))
	}
	return filepath.Clean(filepath.Join(workspace, ".openkitty", "runtime", "browser"))
}

func managedBrowserHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Minute,
	}
}

func EnsureExecutable(ctx context.Context, workspace string) (string, error) {
	installer := &managedBrowserInstaller{
		root:        CacheRoot(workspace),
		goos:        runtime.GOOS,
		goarch:      runtime.GOARCH,
		manifestURL: chromeForTestingManifestURL,
		client:      managedBrowserHTTPClient(),
	}
	if executable, ok := installer.installedExecutable(); ok {
		return executable, nil
	}
	if !managedBrowserAutoInstallEnabled() {
		return "", errors.New("managed browser download is disabled by OPENKITTY_BROWSER_AUTO_INSTALL")
	}
	return installer.Ensure(ctx)
}

func managedBrowserPlatform(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		if goarch == "amd64" {
			return "linux64", nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return "win64", nil
		case "386":
			return "win32", nil
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return "mac-x64", nil
		case "arm64":
			return "mac-arm64", nil
		}
	}
	return "", fmt.Errorf(
		"managed Chrome for Testing is unavailable for %s/%s; configure OPENKITTY_CDP_HOST and OPENKITTY_CDP_PORT for a remote browser",
		goos,
		goarch,
	)
}

func managedBrowserExecutableName(goos string) string {
	switch goos {
	case "windows":
		return "chrome.exe"
	case "darwin":
		return "Google Chrome for Testing"
	default:
		return "chrome"
	}
}

func (i *managedBrowserInstaller) normalize() error {
	if i == nil {
		return errors.New("managed browser installer is nil")
	}
	if strings.TrimSpace(i.root) == "" {
		return errors.New("managed browser cache directory is empty")
	}
	root, err := filepath.Abs(i.root)
	if err != nil {
		return fmt.Errorf("resolve managed browser cache directory: %w", err)
	}
	i.root = filepath.Clean(root)
	if i.goos == "" {
		i.goos = runtime.GOOS
	}
	if i.goarch == "" {
		i.goarch = runtime.GOARCH
	}
	if strings.TrimSpace(i.manifestURL) == "" {
		i.manifestURL = chromeForTestingManifestURL
	}
	if i.client == nil {
		i.client = managedBrowserHTTPClient()
	}
	return nil
}

func (i *managedBrowserInstaller) Ensure(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := i.normalize(); err != nil {
		return "", err
	}
	platform, err := managedBrowserPlatform(i.goos, i.goarch)
	if err != nil {
		return "", err
	}
	if executable, ok := i.installedExecutableForPlatform(platform); ok {
		return executable, nil
	}
	if err := os.MkdirAll(i.root, 0o755); err != nil {
		return "", fmt.Errorf("create managed browser cache directory: %w", err)
	}

	release, err := i.acquireInstallLock(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	// Another OpenKitty process may have completed installation while this
	// process waited for the workspace-scoped lock.
	if executable, ok := i.installedExecutableForPlatform(platform); ok {
		return executable, nil
	}

	download, err := i.resolveStableDownload(ctx, platform)
	if err != nil {
		return "", err
	}
	log.Printf("[CDP] downloading managed Chrome for Testing %s (%s) into %s", download.Version, download.Platform, i.root)

	archivePath, archiveSHA256, err := i.downloadArchive(ctx, download.URL)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	stagingDir, err := os.MkdirTemp(i.root, ".browser-install-")
	if err != nil {
		return "", fmt.Errorf("create managed browser staging directory: %w", err)
	}
	stagingDir = filepath.Clean(stagingDir)
	defer os.RemoveAll(stagingDir)

	stagedExecutable, err := extractManagedBrowserArchive(
		archivePath,
		stagingDir,
		managedBrowserExecutableName(i.goos),
	)
	if err != nil {
		return "", err
	}
	executableRelativeToStaging, err := filepath.Rel(stagingDir, stagedExecutable)
	if err != nil || !safeRelativePath(executableRelativeToStaging) {
		return "", errors.New("managed browser archive produced an unsafe executable path")
	}

	versionDirName := sanitizeManagedBrowserPathToken(download.Version)
	if len(archiveSHA256) >= 12 {
		versionDirName += "-" + archiveSHA256[:12]
	}
	finalDir := filepath.Join(i.root, "versions", platform, versionDirName)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return "", fmt.Errorf("create managed browser version directory: %w", err)
	}
	finalExecutable := filepath.Join(finalDir, executableRelativeToStaging)
	if !regularFileExists(finalExecutable) {
		if _, statErr := os.Stat(finalDir); statErr == nil {
			return "", fmt.Errorf("managed browser version directory already exists without a usable executable: %s", finalDir)
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect managed browser version directory: %w", statErr)
		}
		if err := publishManagedBrowserInstallation(ctx, stagingDir, finalDir); err != nil {
			return "", fmt.Errorf("publish managed browser installation: %w", err)
		}
	}
	if !regularFileExists(finalExecutable) {
		return "", fmt.Errorf("managed browser executable is missing after installation: %s", finalExecutable)
	}
	if i.goos != "windows" {
		if err := os.Chmod(finalExecutable, 0o755); err != nil {
			return "", fmt.Errorf("make managed browser executable runnable: %w", err)
		}
	}

	executableRelativeToRoot, err := filepath.Rel(i.root, finalExecutable)
	if err != nil || !safeRelativePath(executableRelativeToRoot) {
		return "", errors.New("managed browser executable escaped its cache directory")
	}
	metadata := managedBrowserMetadata{
		Version:       download.Version,
		Platform:      platform,
		Executable:    filepath.ToSlash(executableRelativeToRoot),
		ArchiveSHA256: archiveSHA256,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeManagedBrowserMetadata(i.root, metadata); err != nil {
		return "", err
	}
	log.Printf("[CDP] managed Chrome for Testing %s is ready", download.Version)
	return finalExecutable, nil
}

func publishManagedBrowserInstallation(ctx context.Context, stagingDir, finalDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		if err := os.Rename(stagingDir, finalDir); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == 7 {
			break
		}
		delay := time.Duration(attempt+1) * 25 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func (i *managedBrowserInstaller) installedExecutable() (string, bool) {
	if i == nil {
		return "", false
	}
	if err := i.normalize(); err != nil {
		return "", false
	}
	platform, err := managedBrowserPlatform(i.goos, i.goarch)
	if err != nil {
		return "", false
	}
	return i.installedExecutableForPlatform(platform)
}

func (i *managedBrowserInstaller) installedExecutableForPlatform(platform string) (string, bool) {
	metadataPath := filepath.Join(i.root, "current.json")
	if data, err := os.ReadFile(metadataPath); err == nil && len(data) <= 64<<10 {
		var metadata managedBrowserMetadata
		if json.Unmarshal(data, &metadata) == nil && metadata.Platform == platform {
			relative := filepath.FromSlash(strings.TrimSpace(metadata.Executable))
			if safeRelativePath(relative) {
				candidate := filepath.Join(i.root, relative)
				if securitytools.PathWithinRoot(i.root, candidate) && regularFileExists(candidate) {
					return candidate, true
				}
			}
		}
	}

	// Recover from a process interruption after the version directory was
	// published but before current.json was replaced.
	versionsRoot := filepath.Join(i.root, "versions", platform)
	expectedName := managedBrowserExecutableName(i.goos)
	var newestPath string
	var newestTime time.Time
	_ = filepath.WalkDir(versionsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() || entry.Name() != expectedName {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		if newestPath == "" || info.ModTime().After(newestTime) {
			newestPath = path
			newestTime = info.ModTime()
		}
		return nil
	})
	return newestPath, newestPath != ""
}

func (i *managedBrowserInstaller) acquireInstallLock(ctx context.Context) (func(), error) {
	lockPath := filepath.Join(i.root, ".install.lock")
	for {
		lockFile, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(lockFile, "pid=%d\ncreated_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = lockFile.Sync()
			return func() {
				_ = lockFile.Close()
				_ = os.Remove(lockPath)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire managed browser install lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > managedBrowserInstallLockMaxAge {
			if removeErr := os.Remove(lockPath); removeErr == nil || os.IsNotExist(removeErr) {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for managed browser installation: %w", ctx.Err())
		case <-time.After(managedBrowserInstallPollInterval):
		}
	}
}

func (i *managedBrowserInstaller) resolveStableDownload(ctx context.Context, platform string) (managedBrowserDownload, error) {
	if err := validateManagedBrowserDownloadURL(i.manifestURL); err != nil {
		return managedBrowserDownload{}, fmt.Errorf("invalid Chrome for Testing manifest URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.manifestURL, nil)
	if err != nil {
		return managedBrowserDownload{}, err
	}
	req.Header.Set("User-Agent", "OpenKitty-managed-browser/1")
	resp, err := i.client.Do(req)
	if err != nil {
		return managedBrowserDownload{}, fmt.Errorf("download Chrome for Testing manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return managedBrowserDownload{}, fmt.Errorf("Chrome for Testing manifest returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, managedBrowserMaxManifestBytes+1))
	if err != nil {
		return managedBrowserDownload{}, fmt.Errorf("read Chrome for Testing manifest: %w", err)
	}
	if int64(len(body)) > managedBrowserMaxManifestBytes {
		return managedBrowserDownload{}, errors.New("Chrome for Testing manifest exceeds size limit")
	}
	var manifest chromeForTestingManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return managedBrowserDownload{}, fmt.Errorf("decode Chrome for Testing manifest: %w", err)
	}
	var stableVersion string
	var stableDownloads map[string][]struct {
		Platform string `json:"platform"`
		URL      string `json:"url"`
	}
	for channel, entry := range manifest.Channels {
		if strings.EqualFold(channel, "stable") {
			stableVersion = strings.TrimSpace(entry.Version)
			stableDownloads = entry.Downloads
			break
		}
	}
	if stableVersion == "" {
		return managedBrowserDownload{}, errors.New("Chrome for Testing manifest has no Stable channel")
	}
	for assetName, downloads := range stableDownloads {
		if !strings.EqualFold(assetName, "chrome") {
			continue
		}
		for _, download := range downloads {
			if download.Platform != platform {
				continue
			}
			if err := validateManagedBrowserDownloadURL(download.URL); err != nil {
				return managedBrowserDownload{}, fmt.Errorf("invalid managed browser archive URL: %w", err)
			}
			return managedBrowserDownload{
				Version:  stableVersion,
				Platform: platform,
				URL:      download.URL,
			}, nil
		}
	}
	return managedBrowserDownload{}, fmt.Errorf("Chrome for Testing Stable has no chrome download for %s", platform)
}

func validateManagedBrowserDownloadURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if parsed.Hostname() == "" {
		return errors.New("URL has no host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("URL must use HTTPS (HTTP is allowed only for loopback tests)")
}

func (i *managedBrowserInstaller) downloadArchive(ctx context.Context, archiveURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "OpenKitty-managed-browser/1")
	resp, err := i.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download managed browser archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("managed browser archive returned %s", resp.Status)
	}
	if resp.ContentLength > managedBrowserMaxArchiveBytes {
		return "", "", errors.New("managed browser archive exceeds size limit")
	}

	archiveFile, err := os.CreateTemp(i.root, ".browser-download-*.zip")
	if err != nil {
		return "", "", fmt.Errorf("create managed browser download file: %w", err)
	}
	archivePath := archiveFile.Name()
	cleanup := true
	defer func() {
		_ = archiveFile.Close()
		if cleanup {
			_ = os.Remove(archivePath)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(archiveFile, hasher),
		io.LimitReader(resp.Body, managedBrowserMaxArchiveBytes+1),
	)
	if err != nil {
		return "", "", fmt.Errorf("save managed browser archive: %w", err)
	}
	if written > managedBrowserMaxArchiveBytes {
		return "", "", errors.New("managed browser archive exceeds size limit")
	}
	if err := archiveFile.Sync(); err != nil {
		return "", "", fmt.Errorf("sync managed browser archive: %w", err)
	}
	if err := archiveFile.Close(); err != nil {
		return "", "", fmt.Errorf("close managed browser archive: %w", err)
	}
	cleanup = false
	return archivePath, fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func extractManagedBrowserArchive(archivePath, destination, executableName string) (string, error) {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve managed browser extraction directory: %w", err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open managed browser archive: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > managedBrowserMaxArchiveEntries {
		return "", errors.New("managed browser archive contains too many entries")
	}

	var declaredBytes uint64
	var extractedBytes int64
	var executablePath string
	for _, file := range reader.File {
		if file == nil {
			continue
		}
		if file.UncompressedSize64 > uint64(managedBrowserMaxExtractedBytes) ||
			declaredBytes > uint64(managedBrowserMaxExtractedBytes)-file.UncompressedSize64 {
			return "", errors.New("managed browser archive exceeds extracted size limit")
		}
		declaredBytes += file.UncompressedSize64

		relativePath := filepath.Clean(filepath.FromSlash(file.Name))
		if relativePath == "." {
			continue
		}
		if !safeRelativePath(relativePath) {
			return "", fmt.Errorf("managed browser archive contains unsafe path %q", file.Name)
		}
		targetPath := filepath.Join(destination, relativePath)
		if !securitytools.PathWithinRoot(destination, targetPath) {
			return "", fmt.Errorf("managed browser archive path escapes destination: %q", file.Name)
		}
		mode := file.Mode()
		if mode.IsDir() || strings.HasSuffix(file.Name, "/") {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return "", fmt.Errorf("create managed browser directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", fmt.Errorf("create managed browser parent directory: %w", err)
		}

		source, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("open managed browser archive entry: %w", err)
		}
		if mode&os.ModeSymlink != 0 {
			linkBytes, readErr := io.ReadAll(io.LimitReader(source, 4097))
			_ = source.Close()
			if readErr != nil {
				return "", fmt.Errorf("read managed browser symlink: %w", readErr)
			}
			if len(linkBytes) == 0 || len(linkBytes) > 4096 {
				return "", errors.New("managed browser archive contains an invalid symlink")
			}
			linkTarget := filepath.Clean(filepath.FromSlash(string(linkBytes)))
			if filepath.IsAbs(linkTarget) ||
				!securitytools.PathWithinRoot(destination, filepath.Join(filepath.Dir(targetPath), linkTarget)) {
				return "", fmt.Errorf("managed browser archive symlink escapes destination: %q", file.Name)
			}
			if err := os.Symlink(linkTarget, targetPath); err != nil {
				return "", fmt.Errorf("create managed browser symlink: %w", err)
			}
			continue
		}
		if mode.Type() != 0 {
			_ = source.Close()
			return "", fmt.Errorf("managed browser archive contains unsupported special entry %q", file.Name)
		}

		permissions := mode.Perm()
		if permissions == 0 {
			permissions = 0o644
		}
		target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, permissions)
		if err != nil {
			_ = source.Close()
			return "", fmt.Errorf("create managed browser file: %w", err)
		}
		remaining := managedBrowserMaxExtractedBytes - extractedBytes
		copied, copyErr := io.Copy(target, io.LimitReader(source, remaining+1))
		closeTargetErr := target.Close()
		closeSourceErr := source.Close()
		extractedBytes += copied
		if copyErr != nil {
			return "", fmt.Errorf("extract managed browser file: %w", copyErr)
		}
		if closeTargetErr != nil {
			return "", fmt.Errorf("close managed browser file: %w", closeTargetErr)
		}
		if closeSourceErr != nil {
			return "", fmt.Errorf("close managed browser archive entry: %w", closeSourceErr)
		}
		if copied > remaining {
			return "", errors.New("managed browser archive exceeds extracted size limit")
		}
		if filepath.Base(targetPath) == executableName {
			executablePath = targetPath
		}
	}
	if executablePath == "" || !regularFileExists(executablePath) {
		return "", fmt.Errorf("managed browser archive does not contain %s", executableName)
	}
	return executablePath, nil
}

func safeRelativePath(path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	return path != "" &&
		path != "." &&
		!filepath.IsAbs(path) &&
		path != ".." &&
		!strings.HasPrefix(path, ".."+string(filepath.Separator)) &&
		filepath.VolumeName(path) == ""
}

func regularFileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func sanitizeManagedBrowserPathToken(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '-', character == '_':
			result.WriteRune(character)
		default:
			result.WriteByte('-')
		}
		if result.Len() >= 80 {
			break
		}
	}
	token := strings.Trim(result.String(), ".-_")
	if token == "" {
		return "unknown"
	}
	return token
}

func writeManagedBrowserMetadata(root string, metadata managedBrowserMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed browser metadata: %w", err)
	}
	tempFile, err := os.CreateTemp(root, ".current-*.json")
	if err != nil {
		return fmt.Errorf("create managed browser metadata: %w", err)
	}
	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		_ = tempFile.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := tempFile.Chmod(0o600); err != nil {
		return fmt.Errorf("secure managed browser metadata: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write managed browser metadata: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync managed browser metadata: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close managed browser metadata: %w", err)
	}
	currentPath := filepath.Join(root, "current.json")
	if err := os.Rename(tempPath, currentPath); err != nil {
		// Windows cannot replace an existing file with os.Rename. current.json is
		// only a cache pointer; installed version directories remain immutable.
		if removeErr := os.Remove(currentPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace managed browser metadata: %w", err)
		}
		if retryErr := os.Rename(tempPath, currentPath); retryErr != nil {
			return fmt.Errorf("publish managed browser metadata: %w", retryErr)
		}
	}
	cleanup = false
	return nil
}
