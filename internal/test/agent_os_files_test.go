package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentOSFileUploadIsScopedToAuthenticatedSubject(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "attachment processed"}
	gateway, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(
		filepath.Join(t.TempDir(), "tenants"),
		log.New(io.Discard, "", 0),
	)
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	gateway.tenantMgr = manager
	gateway.config.Gateway.AuthEnabled = true
	const serviceToken = "subject_file_service_token_123456789"
	gateway.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "document-service", TenantID: "tenant-a",
		Token: serviceToken, Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"Researcher"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeUsersActAs,
		},
	}}
	handler := gateway.agentOSDataHandler()

	missingSubject := performAgentOSFileUpload(
		t,
		handler,
		serviceToken,
		"",
		"requirements.txt",
		[]byte("private requirements"),
	)
	if missingSubject.Code != http.StatusBadRequest ||
		!strings.Contains(missingSubject.Body.String(), "X-OpenKitty-User-ID") {
		t.Fatalf(
			"missing subject upload status=%d body=%s",
			missingSubject.Code,
			missingSubject.Body.String(),
		)
	}

	aliceUpload := performAgentOSFileUpload(
		t,
		handler,
		serviceToken,
		"alice",
		`..\requirements.txt`,
		[]byte("private requirements"),
	)
	if aliceUpload.Code != http.StatusCreated {
		t.Fatalf(
			"alice upload status=%d body=%s",
			aliceUpload.Code,
			aliceUpload.Body.String(),
		)
	}
	var uploaded struct {
		Attachment MessageAttachment `json:"attachment"`
	}
	if err := json.Unmarshal(aliceUpload.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Attachment.Name != "requirements.txt" ||
		!strings.HasPrefix(uploaded.Attachment.Path, "inputs/upl_") ||
		uploaded.Attachment.Size != int64(len("private requirements")) {
		t.Fatalf("unexpected attachment descriptor: %+v", uploaded.Attachment)
	}

	aliceWorkspace := personalAgentOSWorkspace(
		manager.TenantDir("tenant-a"),
		"tenant-a",
		"alice",
	)
	stored, err := os.ReadFile(filepath.Join(
		aliceWorkspace,
		filepath.FromSlash(uploaded.Attachment.Path),
	))
	if err != nil {
		t.Fatalf("read Alice upload: %v", err)
	}
	if string(stored) != "private requirements" {
		t.Fatalf("stored upload = %q", stored)
	}

	aliceTask := performAgentOSHandlerJSON(
		t,
		handler,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":     "analyze this file",
			"role_id":     "Researcher",
			"session_id":  "alice-files",
			"user_id":     "alice",
			"attachments": []MessageAttachment{uploaded.Attachment},
		},
		serviceToken,
		"alice",
	)
	if aliceTask.Code != http.StatusAccepted {
		t.Fatalf("Alice attachment task status=%d body=%s", aliceTask.Code, aliceTask.Body.String())
	}
	var accepted map[string]interface{}
	if err := json.Unmarshal(aliceTask.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	taskID, _ := accepted["task_id"].(string)
	_ = waitForAgentOSTask(t, gateway, taskID, "completed")
	inputs := agent.inputSnapshot()
	if len(inputs) != 1 || !strings.Contains(inputs[0], uploaded.Attachment.Path) {
		t.Fatalf("Alice attachment did not reach agent manifest: %#v", inputs)
	}

	bobTask := performAgentOSHandlerJSON(
		t,
		handler,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":     "try another user's path",
			"role_id":     "Researcher",
			"session_id":  "bob-files",
			"user_id":     "bob",
			"attachments": []MessageAttachment{uploaded.Attachment},
		},
		serviceToken,
		"bob",
	)
	if bobTask.Code != http.StatusBadRequest ||
		!strings.Contains(bobTask.Body.String(), "attachment is unavailable") {
		t.Fatalf("Bob reused Alice path: status=%d body=%s", bobTask.Code, bobTask.Body.String())
	}
}

func TestAgentOSArtifactDownloadIsScopedToAuthenticatedSubject(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	manager := NewTenantManager(
		filepath.Join(t.TempDir(), "tenants"),
		log.New(io.Discard, "", 0),
	)
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	gateway.tenantMgr = manager
	gateway.config.Gateway.AuthEnabled = true
	const serviceToken = "artifact_download_service_token_123456"
	gateway.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "document-service", TenantID: "tenant-a",
		Token: serviceToken, Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"Researcher"},
		Scopes: []string{
			agentOSScopeTasksReadOwn,
			agentOSScopeUsersActAs,
		},
	}}

	aliceWorkspace := personalAgentOSWorkspace(
		manager.TenantDir("tenant-a"),
		"tenant-a",
		"alice",
	)
	artifactPath := filepath.Join(
		aliceWorkspace,
		".openkitty",
		"storage",
		"上海地区潜在客户清单.md",
	)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const content = "# 上海地区潜在客户清单\n\n仅 Alice 可下载。"
	if err := os.WriteFile(artifactPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := gateway.agentOSDataHandler()
	target := "/api/v1/agent/files?path=" + url.QueryEscape(
		".openkitty/storage/上海地区潜在客户清单.md",
	)
	aliceRequest := httptest.NewRequest(http.MethodGet, target, nil)
	aliceRequest.Header.Set("Authorization", "Bearer "+serviceToken)
	aliceRequest.Header.Set(agentOSUserIDHeader, "alice")
	aliceResponse := httptest.NewRecorder()
	handler.ServeHTTP(aliceResponse, aliceRequest)
	if aliceResponse.Code != http.StatusOK || aliceResponse.Body.String() != content {
		t.Fatalf(
			"Alice download status=%d body=%q",
			aliceResponse.Code,
			aliceResponse.Body.String(),
		)
	}
	if disposition := aliceResponse.Header().Get("Content-Disposition"); !strings.Contains(
		strings.ToLower(disposition),
		"filename*=utf-8''",
	) {
		t.Fatalf("download disposition = %q", disposition)
	}

	bobRequest := httptest.NewRequest(http.MethodGet, target, nil)
	bobRequest.Header.Set("Authorization", "Bearer "+serviceToken)
	bobRequest.Header.Set(agentOSUserIDHeader, "bob")
	bobResponse := httptest.NewRecorder()
	handler.ServeHTTP(bobResponse, bobRequest)
	if bobResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"Bob cross-user download status=%d body=%s",
			bobResponse.Code,
			bobResponse.Body.String(),
		)
	}

	internalRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/agent/files?path=.openkitty%2Fstorage%2F.system%2Fsecret.md",
		nil,
	)
	internalRequest.Header.Set("Authorization", "Bearer "+serviceToken)
	internalRequest.Header.Set(agentOSUserIDHeader, "alice")
	internalResponse := httptest.NewRecorder()
	handler.ServeHTTP(internalResponse, internalRequest)
	if internalResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"internal artifact download status=%d body=%s",
			internalResponse.Code,
			internalResponse.Body.String(),
		)
	}
}

func TestAgentOSTaskResponseIncludesOnlyExistingDownloadableArtifacts(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	workspace := t.TempDir()
	reportPath := filepath.Join(
		workspace,
		".openkitty",
		"storage",
		"上海地区潜在客户清单.md",
	)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte("# verified report"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := &GatewayTask{
		ID:                "agt_artifact_response",
		Status:            "completed",
		Result:            "已生成 `.openkitty/storage/上海地区潜在客户清单.md`，另有不存在的 `.openkitty/storage/missing.md`。",
		ResolvedWorkspace: workspace,
		CreatedAt:         time.Now(),
	}
	response := gateway.agentOSTaskResponse(task, false)
	artifacts, ok := response["artifacts"].([]AgentOSArtifact)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("task artifacts = %#v", response["artifacts"])
	}
	if artifacts[0].Name != "上海地区潜在客户清单.md" ||
		artifacts[0].Path != ".openkitty/storage/上海地区潜在客户清单.md" ||
		artifacts[0].Size != int64(len("# verified report")) ||
		!strings.Contains(artifacts[0].DownloadURL, "/api/v1/agent/files?path=") {
		t.Fatalf("artifact descriptor = %+v", artifacts[0])
	}
}

func TestAgentOSTaskResponseFindsBareFilesListedUnderStorageDirectory(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	workspace := t.TempDir()
	storageDir := filepath.Join(workspace, ".openkitty", "storage")
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"脑科学与未来规划.pptx":          "presentation",
		"preview_13_roadmap.png": "preview",
		"not-mentioned.txt":      "private",
	} {
		if err := os.WriteFile(filepath.Join(storageDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	task := &GatewayTask{
		ID:     "agt_bare_artifact_response",
		Status: "completed",
		Result: "交付物位于 `.openkitty/storage/`：\n\n" +
			"| 文件 | 说明 |\n|---|---|\n" +
			"| **脑科学与未来规划.pptx** | 主交付物 |\n" +
			"| preview_13_roadmap.png | 行动路线图预览图 |",
		ResolvedWorkspace: workspace,
		CreatedAt:         time.Now(),
	}
	response := gateway.agentOSTaskResponse(task, false)
	artifacts, ok := response["artifacts"].([]AgentOSArtifact)
	if !ok || len(artifacts) != 2 {
		t.Fatalf("task artifacts = %#v", response["artifacts"])
	}
	byName := make(map[string]AgentOSArtifact, len(artifacts))
	for _, artifact := range artifacts {
		byName[artifact.Name] = artifact
	}
	for _, name := range []string{"脑科学与未来规划.pptx", "preview_13_roadmap.png"} {
		artifact, exists := byName[name]
		if !exists {
			t.Fatalf("missing artifact %q in %#v", name, artifacts)
		}
		if artifact.Path != ".openkitty/storage/"+name {
			t.Fatalf("artifact %q path = %q", name, artifact.Path)
		}
	}
	if _, exists := byName["not-mentioned.txt"]; exists {
		t.Fatalf("unmentioned file leaked into task artifacts: %#v", artifacts)
	}
}

func performAgentOSFileUpload(
	t *testing.T,
	handler http.Handler,
	serviceToken string,
	subject string,
	fileName string,
	content []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	if subject != "" {
		request.Header.Set(agentOSUserIDHeader, subject)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func performAgentOSHandlerJSON(
	t *testing.T,
	handler http.Handler,
	method string,
	target string,
	payload interface{},
	serviceToken string,
	subject string,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set(agentOSUserIDHeader, subject)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
