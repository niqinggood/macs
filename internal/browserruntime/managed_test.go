package browserruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	securitytools "mac/internal/tools/security"
)

func TestManagedBrowserPlatform(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		want   string
	}{
		{goos: "linux", goarch: "amd64", want: "linux64"},
		{goos: "windows", goarch: "amd64", want: "win64"},
		{goos: "windows", goarch: "386", want: "win32"},
		{goos: "darwin", goarch: "amd64", want: "mac-x64"},
		{goos: "darwin", goarch: "arm64", want: "mac-arm64"},
	}
	for _, test := range tests {
		got, err := managedBrowserPlatform(test.goos, test.goarch)
		if err != nil || got != test.want {
			t.Errorf("managedBrowserPlatform(%q,%q)=(%q,%v), want %q", test.goos, test.goarch, got, err, test.want)
		}
	}
	if _, err := managedBrowserPlatform("linux", "arm64"); err == nil ||
		!strings.Contains(err.Error(), "OPENKITTY_CDP_HOST") {
		t.Fatalf("unsupported platform should recommend remote CDP, got %v", err)
	}
}

func TestManagedBrowserAutoInstallSetting(t *testing.T) {
	for _, disabled := range []string{"0", "false", "no"} {
		t.Setenv("OPENKITTY_BROWSER_AUTO_INSTALL", disabled)
		if managedBrowserAutoInstallEnabled() {
			t.Fatalf("OPENKITTY_BROWSER_AUTO_INSTALL=%q should disable downloads", disabled)
		}
	}
	t.Setenv("OPENKITTY_BROWSER_AUTO_INSTALL", "")
	if !managedBrowserAutoInstallEnabled() {
		t.Fatal("managed browser download should be enabled by default")
	}
}

func TestManagedBrowserInstallerDownloadsOnceAndWorksOffline(t *testing.T) {
	archive := fakeManagedBrowserArchive(t)
	server, manifestHits, archiveHits := fakeManagedBrowserServer(t, archive, 0)
	root := filepath.Join(t.TempDir(), "managed-browser")
	installer := &managedBrowserInstaller{
		root:        root,
		goos:        "linux",
		goarch:      "amd64",
		manifestURL: server.URL + "/manifest.json",
		client:      server.Client(),
	}

	executable, err := installer.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !securitytools.PathWithinRoot(root, executable) || filepath.Base(executable) != "chrome" {
		t.Fatalf("managed executable escaped cache or has wrong name: %s", executable)
	}
	if content, readErr := os.ReadFile(executable); readErr != nil || string(content) != "fake-chrome-binary" {
		t.Fatalf("managed executable=(%q,%v)", content, readErr)
	}
	if manifestHits.Load() != 1 || archiveHits.Load() != 1 {
		t.Fatalf("download counts manifest=%d archive=%d, want 1 each", manifestHits.Load(), archiveHits.Load())
	}

	server.Close()
	offlineInstaller := &managedBrowserInstaller{
		root:        root,
		goos:        "linux",
		goarch:      "amd64",
		manifestURL: server.URL + "/manifest.json",
		client:      server.Client(),
	}
	offlineExecutable, err := offlineInstaller.Ensure(context.Background())
	if err != nil {
		t.Fatalf("installed browser should work without manifest network access: %v", err)
	}
	if offlineExecutable != executable {
		t.Fatalf("offline executable=%q, want %q", offlineExecutable, executable)
	}
}

func TestManagedBrowserInstallerSerializesConcurrentDownloads(t *testing.T) {
	archive := fakeManagedBrowserArchive(t)
	server, manifestHits, archiveHits := fakeManagedBrowserServer(t, archive, 100*time.Millisecond)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-browser")

	const workers = 5
	results := make([]string, workers)
	errorsByWorker := make([]error, workers)
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			<-start
			installer := &managedBrowserInstaller{
				root:        root,
				goos:        "linux",
				goarch:      "amd64",
				manifestURL: server.URL + "/manifest.json",
				client:      server.Client(),
			}
			results[worker], errorsByWorker[worker] = installer.Ensure(context.Background())
		}(index)
	}
	close(start)
	group.Wait()

	for index, workerErr := range errorsByWorker {
		if workerErr != nil {
			t.Fatalf("worker %d failed: %v", index, workerErr)
		}
		if results[index] != results[0] {
			t.Fatalf("worker %d executable=%q, want %q", index, results[index], results[0])
		}
	}
	if manifestHits.Load() != 1 || archiveHits.Load() != 1 {
		t.Fatalf("concurrent download counts manifest=%d archive=%d, want 1 each", manifestHits.Load(), archiveHits.Load())
	}
}

func TestExtractManagedBrowserArchiveRejectsTraversal(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "unsafe.zip")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("escape"))
	validHeader := &zip.FileHeader{Name: "chrome-linux64/chrome", Method: zip.Deflate}
	validHeader.SetMode(0o755)
	valid, err := writer.CreateHeader(validHeader)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = valid.Write([]byte("fake-chrome-binary"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(tempDir, "extract")
	if _, err := extractManagedBrowserArchive(archivePath, destination, "chrome"); err == nil {
		t.Fatal("path traversal archive should be rejected")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("path traversal created a file outside extraction root: %v", err)
	}
}

func TestExtractManagedBrowserArchiveRejectsEscapingSymlink(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "unsafe-symlink.zip")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "chrome-linux64/escape", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("../../outside"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractManagedBrowserArchive(archivePath, filepath.Join(tempDir, "extract"), "chrome"); err == nil {
		t.Fatal("escaping symlink should be rejected")
	}
}

func fakeManagedBrowserArchive(t *testing.T) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "chrome-linux64/chrome", Method: zip.Deflate}
	header.SetMode(0o755)
	executable, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = executable.Write([]byte("fake-chrome-binary"))
	resource, err := writer.Create("chrome-linux64/resources/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = resource.Write([]byte("resource"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func fakeManagedBrowserServer(
	t *testing.T,
	archive []byte,
	archiveDelay time.Duration,
) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var manifestHits atomic.Int32
	var archiveHits atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.json":
			manifestHits.Add(1)
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				response,
				`{"channels":{"Stable":{"version":"150.0.1.2","downloads":{"chrome":[{"platform":"linux64","url":%q}]}}}}`,
				server.URL+"/chrome.zip",
			)
		case "/chrome.zip":
			archiveHits.Add(1)
			if archiveDelay > 0 {
				time.Sleep(archiveDelay)
			}
			response.Header().Set("Content-Type", "application/zip")
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	return server, &manifestHits, &archiveHits
}
