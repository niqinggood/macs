package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageAttachmentNormalizationUsesActualWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "storage", "quarterly report.pdf")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test report"), 0644); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{config: &Config{Workspace: workspace}}

	attachments, err := gateway.normalizeMessageAttachments([]MessageAttachment{
		{Name: "../spoofed.exe", Path: filepath.Join("storage", "quarterly report.pdf"), MIMEType: "application/x-msdownload", Size: 999999, Kind: "image"},
		{Name: "duplicate.pdf", Path: filepath.Join("storage", "quarterly report.pdf")},
	})
	if err != nil {
		t.Fatalf("normalize attachments: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v, want one deduplicated file", attachments)
	}
	attachment := attachments[0]
	if attachment.Path != "storage/quarterly report.pdf" {
		t.Fatalf("path = %q", attachment.Path)
	}
	if attachment.Name != "quarterly report.pdf" {
		t.Fatalf("name should come from the actual file, got %q", attachment.Name)
	}
	if attachment.MIMEType != "application/pdf" || attachment.Kind != "file" || attachment.Size != int64(len("test report")) {
		t.Fatalf("metadata must come from the actual file, got %#v", attachment)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.normalizeMessageAttachments([]MessageAttachment{{Name: "outside", Path: executable}}); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("outside-workspace attachment should be rejected, got %v", err)
	}
}

func TestChatMessagePersistsVisibleTextAndStructuredAttachments(t *testing.T) {
	gateway := newAEPersistenceTestGateway(t)
	workspace := t.TempDir()
	gateway.config = &Config{Workspace: workspace}
	attachment := MessageAttachment{
		Name: "经营报告.docx", Path: "storage/report.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Size: 2048, Kind: "file",
	}
	visible := "请分析这份报告中的现金流"
	if _, err := gateway.saveChatMessageWithAttachments("web_attachment", "user", "", visible, "pending", "task-1", "web", "local", []MessageAttachment{attachment}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session/web_attachment", nil)
	gateway.handleSessionMessages(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Messages []ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if len(response.Messages) != 1 || response.Messages[0].Content != visible {
		t.Fatalf("visible message was polluted: %#v", response.Messages)
	}
	if len(response.Messages[0].Attachments) != 1 || response.Messages[0].Attachments[0].Path != attachment.Path {
		t.Fatalf("attachment did not survive history reload: %#v", response.Messages[0].Attachments)
	}
	agentInput := buildAgentMessageWithAttachments(visible, response.Messages[0].Attachments)
	if !strings.HasPrefix(agentInput, visible) || !strings.Contains(agentInput, attachment.Path) || strings.Contains(response.Messages[0].Content, "OpenKitty 用户附件清单") {
		t.Fatalf("agent/display separation failed: visible=%q agent=%q", response.Messages[0].Content, agentInput)
	}
}

func TestUploadUsesConfiguredWorkspaceAndReturnsAttachmentDescriptor(t *testing.T) {
	workspace := t.TempDir()
	gateway := &GatewayService{
		config: &Config{Workspace: workspace},
		logger: log.New(io.Discard, "", 0),
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "测试文档.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("attachment body")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	gateway.handleUpload(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success  bool   `json:"success"`
		Filename string `json:"filename"`
		Path     string `json:"path"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Path != "storage/测试文档.txt" || response.Kind != "file" {
		t.Fatalf("unexpected upload response: %#v", response)
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(response.Path)))
	if err != nil || string(data) != "attachment body" {
		t.Fatalf("uploaded file mismatch: data=%q err=%v", data, err)
	}
}

func TestImageDownloadSupportsSafeInlinePreviewOnlyForImages(t *testing.T) {
	workspace := t.TempDir()
	storage := filepath.Join(workspace, "storage")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "preview.png"), []byte("not-a-real-png"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "report.html"), []byte("<script>alert(1)</script>"), 0644); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{config: &Config{Workspace: workspace}, logger: log.New(io.Discard, "", 0)}

	imageRequest := httptest.NewRequest(http.MethodGet, "/api/files/download?inline=1&path=storage%2Fpreview.png", nil)
	imageRecorder := httptest.NewRecorder()
	gateway.handleDownloadFile(imageRecorder, imageRequest)
	if got := imageRecorder.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Fatalf("image disposition=%q, want inline", got)
	}

	htmlRequest := httptest.NewRequest(http.MethodGet, "/api/files/download?inline=1&path=storage%2Freport.html", nil)
	htmlRecorder := httptest.NewRecorder()
	gateway.handleDownloadFile(htmlRecorder, htmlRequest)
	if got := htmlRecorder.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("active content disposition=%q, want attachment", got)
	}
}
