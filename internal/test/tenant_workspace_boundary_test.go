package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/projectctx"
)

func newTenantBoundaryTestWorkspace(t *testing.T) (string, string) {
	t.Helper()
	workspace := t.TempDir()
	tenantDir := filepath.Join(workspace, ".openkitty", "tenants", "tenant-a")
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "secret.txt"), []byte("tenant secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace, tenantDir
}

func TestLegacyWorkspacePathRejectsReservedTenantSubtree(t *testing.T) {
	workspace, _ := newTenantBoundaryTestWorkspace(t)
	for _, requested := range []string{
		".openkitty/tenants/tenant-a/secret.txt",
		".OPENKITTY/TENANTS/tenant-a/secret.txt",
		".openkitty/unused/../tenants/tenant-a/secret.txt",
	} {
		t.Run(requested, func(t *testing.T) {
			_, err := projectctx.ResolveLegacyWorkspacePath(workspace, requested)
			if !errors.Is(err, projectctx.ErrReservedTenantWorkspacePath) {
				t.Fatalf("resolveLegacyWorkspacePath(%q) error = %v, want reserved tenant rejection", requested, err)
			}
		})
	}

	allowedDir := filepath.Join(workspace, ".openkitty", "tenants-public")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := projectctx.ResolveLegacyWorkspacePath(workspace, ".openkitty/tenants-public"); err != nil {
		t.Fatalf("similar non-reserved path was rejected: %v", err)
	}
	if _, err := projectctx.ResolveLegacyWorkspacePath(workspace, "../outside.txt"); !errors.Is(err, projectctx.ErrLegacyWorkspaceEscape) {
		t.Fatalf("workspace traversal error = %v, want workspace escape", err)
	}
}

func TestLegacyWebFileEndpointsHideReservedTenantSubtree(t *testing.T) {
	workspace, _ := newTenantBoundaryTestWorkspace(t)
	gateway := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}

	listReserved := httptest.NewRecorder()
	gateway.handleListFiles(
		listReserved,
		httptest.NewRequest(http.MethodGet, "/api/files/list?path=.openkitty%2Ftenants", nil),
	)
	if listReserved.Code != http.StatusForbidden {
		t.Fatalf("reserved list status = %d, body=%s", listReserved.Code, listReserved.Body.String())
	}

	listParent := httptest.NewRecorder()
	gateway.handleListFiles(
		listParent,
		httptest.NewRequest(http.MethodGet, "/api/files/list?path=.openkitty", nil),
	)
	if listParent.Code != http.StatusOK {
		t.Fatalf("parent list status = %d, body=%s", listParent.Code, listParent.Body.String())
	}
	var response struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.Unmarshal(listParent.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, file := range response.Files {
		if strings.EqualFold(file.Name, "tenants") {
			t.Fatalf("reserved tenant directory leaked in file listing: %#v", response.Files)
		}
	}

	download := httptest.NewRecorder()
	gateway.handleDownloadFile(
		download,
		httptest.NewRequest(http.MethodGet, "/api/files/download?path=.openkitty%2Ftenants%2Ftenant-a%2Fsecret.txt", nil),
	)
	if download.Code != http.StatusForbidden || strings.Contains(download.Body.String(), "tenant secret") {
		t.Fatalf("reserved download status=%d body=%q", download.Code, download.Body.String())
	}
}

func TestLegacyAttachmentRejectsTenantFileButAgentOSExplicitWorkspaceAllowsIt(t *testing.T) {
	workspace, tenantDir := newTenantBoundaryTestWorkspace(t)
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	legacyPath := filepath.ToSlash(filepath.Join(".openkitty", "tenants", "tenant-a", "secret.txt"))

	if _, err := gateway.normalizeMessageAttachments([]MessageAttachment{{Path: legacyPath}}); err == nil ||
		!strings.Contains(err.Error(), "reserved tenant workspace") {
		t.Fatalf("legacy attachment error = %v, want reserved tenant rejection", err)
	}

	attachments, err := gateway.normalizeMessageAttachmentsAt(
		[]MessageAttachment{{Path: "secret.txt"}},
		tenantDir,
	)
	if err != nil {
		t.Fatalf("AgentOS tenant attachment was rejected: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Path != "secret.txt" ||
		attachments[0].Size != int64(len("tenant secret")) {
		t.Fatalf("unexpected AgentOS attachment: %#v", attachments)
	}
}

func TestLegacyTenantBoundaryRejectsSymlinkAliases(t *testing.T) {
	workspace, tenantDir := newTenantBoundaryTestWorkspace(t)
	alias := filepath.Join(workspace, "tenant-alias")
	if err := os.Symlink(tenantDir, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}

	if _, err := projectctx.ResolveLegacyWorkspacePath(workspace, "tenant-alias/secret.txt"); !errors.Is(err, projectctx.ErrReservedTenantWorkspacePath) {
		t.Fatalf("symlink alias error = %v, want reserved tenant rejection", err)
	}
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	if _, err := gateway.normalizeMessageAttachments([]MessageAttachment{{Path: "tenant-alias/secret.txt"}}); err == nil ||
		!strings.Contains(err.Error(), "reserved tenant workspace") {
		t.Fatalf("legacy symlink attachment error = %v", err)
	}
}

func TestLegacyUploadRejectsStorageSymlinkIntoTenantWorkspace(t *testing.T) {
	workspace, tenantDir := newTenantBoundaryTestWorkspace(t)
	if err := os.Symlink(tenantDir, filepath.Join(workspace, "storage")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	gateway := &GatewayService{config: &Config{Workspace: workspace}}

	_, err := gateway.saveUploadedFile("injected.txt", "text/plain", bytes.NewBufferString("injected"))
	if err == nil {
		t.Fatal("upload through tenant storage symlink unexpectedly succeeded")
	}
	if _, statErr := os.Stat(filepath.Join(tenantDir, "injected.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("upload wrote into tenant workspace: %v", statErr)
	}
}
