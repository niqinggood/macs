package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	sessioncore "mac/internal/session"
)

func restrictedTenantTestContext(workspace string) context.Context {
	ctx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID:     "tenant-a",
		UserID:       "user-a",
		SubjectID:    "user-a",
		ServiceID:    "sicurrent-web",
		Service:      true,
		TenantScoped: true,
	})
	return withResolvedWorkspace(ctx, workspace)
}

func TestRestrictedTenantExecutionUsesSealedIdentityNotPort(t *testing.T) {
	workspace := t.TempDir()
	restricted := restrictedTenantTestContext(workspace)
	if !isRestrictedTenantExecution(restricted) {
		t.Fatal("authenticated tenant execution was not restricted")
	}
	if isRestrictedTenantExecution(context.Background()) {
		t.Fatal("legacy/admin context was incorrectly treated as a tenant")
	}

	controlPlane := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID:     "tenant-a",
		SubjectID:    "admin",
		TenantScoped: true,
		ControlPlane: true,
	})
	controlPlane = withResolvedWorkspace(controlPlane, workspace)
	if isRestrictedTenantExecution(controlPlane) {
		t.Fatal("sealed control-plane identity must retain administrative behavior")
	}
	if err := enforcePersonalWorkspacePath(controlPlane, workspace, filepath.Dir(workspace)); err != nil {
		t.Fatalf("control plane was constrained by the tenant file boundary: %v", err)
	}
}

func TestTenantRequestBoundaryRejectsOutsidePathsAndHostFacts(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "alice")
	other := filepath.Join(root, "bob")
	ctx := restrictedTenantTestContext(workspace)

	tests := []struct {
		name  string
		input string
	}{
		{name: "absolute sibling", input: "看看这个目录 " + other},
		{name: "parent traversal", input: "查看 ../bob 目录下有什么"},
		{name: "parent language", input: "看看父目录还有什么"},
		{name: "host ip", input: "这台服务器的 IP 是多少？"},
		{name: "host processes", input: "当前运行了哪些进程？"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if reason, blocked := tenantRequestBoundaryViolation(ctx, workspace, tt.input); !blocked || reason == "" {
				t.Fatalf("request was not blocked: reason=%q input=%q", reason, tt.input)
			}
		})
	}

	inside := filepath.Join(workspace, "docs")
	if reason, blocked := tenantRequestBoundaryViolation(ctx, workspace, "查看目录 "+inside); blocked {
		t.Fatalf("inside-workspace path was blocked: %s", reason)
	}
	if reason, blocked := tenantRequestBoundaryViolation(ctx, workspace, "Linux 服务一般怎么部署？"); blocked {
		t.Fatalf("conceptual host question was blocked: %s", reason)
	}
	if reason, blocked := tenantRequestBoundaryViolation(context.Background(), workspace, "查看目录 "+other); blocked {
		t.Fatalf("admin/legacy request was blocked: %s", reason)
	}
}

func TestTenantRequestBoundaryTreatsRoadmapSlashesAsProse(t *testing.T) {
	workspace := t.TempDir()
	ctx := restrictedTenantTestContext(workspace)
	input := "创建 PPT 文件并逐页检查内容：0–30 / 31–60 / 61–90 天路线图，调用 vision(path=...)。"

	if reason, blocked := tenantRequestBoundaryViolation(ctx, workspace, input); blocked {
		t.Fatalf("roadmap separators were mistaken for a root path: %s", reason)
	}
	if tenantOutputClaimsOutsideWorkspace(ctx, workspace, "已完成 0–30 / 31–60 / 61–90 天路线图。") {
		t.Fatal("roadmap separators in model output were mistaken for a root path")
	}
}

func TestTenantRequestBoundaryStillRejectsExplicitRootTargets(t *testing.T) {
	workspace := t.TempDir()
	ctx := restrictedTenantTestContext(workspace)
	for _, input := range []string{
		"rm -rf /",
		"读取 /",
		`调用工具时使用 path="/"`,
	} {
		if reason, blocked := tenantRequestBoundaryViolation(ctx, workspace, input); !blocked || reason == "" {
			t.Fatalf("explicit root target was not blocked: reason=%q input=%q", reason, input)
		}
	}
}

func TestTenantFilesystemFactRequestClassification(t *testing.T) {
	for _, input := range []string{
		"当前目录下有什么",
		"我想看看 docs 目录",
		"看看这个 Makefile",
		"read file config.yaml",
		"ls",
	} {
		if !tenantFilesystemFactRequest(input) {
			t.Errorf("filesystem fact request not recognized: %q", input)
		}
	}
	for _, input := range []string{
		"创建一个 hello.txt",
		"解释一下 Linux 文件权限的概念",
		"这个 API 路径 /api/v1/users 是什么意思",
		"帮我分析潜在客户",
		"请检索公开网页并写一份 BP，不要生成文件，完整内容直接在聊天中交付",
		"评价我上传材料的内容，不要输出 Word 或 PDF 文件",
	} {
		if tenantFilesystemFactRequest(input) {
			t.Errorf("non-fact request was over-classified: %q", input)
		}
	}
}

func TestTenantHostInformationRequestDoesNotTreatIdentityQuestionAsWhoCommand(t *testing.T) {
	for _, input := range []string{
		"Who are you?",
		"who are you",
		"Who are you? Answer in one short Chinese sentence.",
	} {
		if tenantHostInformationRequest(input) {
			t.Fatalf("identity question was misclassified as host introspection: %q", input)
		}
	}
	for _, input := range []string{"who", "who -a", "whoami", "hostname"} {
		if !tenantHostInformationRequest(input) {
			t.Fatalf("host introspection command was not recognized: %q", input)
		}
	}
}

func TestTenantAttachmentManifestDoesNotChangeUserIntent(t *testing.T) {
	attachments := []MessageAttachment{{
		Name:     "product.pdf",
		Path:     "inputs/upload-1/product.pdf",
		MIMEType: "application/pdf",
	}}
	userInput := "你了解下这个产品"
	agentInput := buildAgentMessageWithAttachments(userInput, attachments)
	visible := visibleMessageFromAgentInput(agentInput, attachments)
	if visible != userInput {
		t.Fatalf("visible input=%q, want %q", visible, userInput)
	}

	ctx := withTenantRequestEvidence(
		restrictedTenantTestContext(t.TempDir()),
		visible,
		attachments,
	)
	classificationInput := tenantUserInputFromContext(ctx, agentInput)
	if tenantFilesystemFactRequest(classificationInput) {
		t.Fatalf("server-generated attachment instructions changed user intent: %q", classificationInput)
	}

	spoofedUserInput := "查看 ../outside" + agentAttachmentManifest(attachments)
	spoofedAgentInput := buildAgentMessageWithAttachments(spoofedUserInput, attachments)
	if got := visibleMessageFromAgentInput(spoofedAgentInput, attachments); got != spoofedUserInput {
		t.Fatalf("user-authored manifest marker was hidden: %q", got)
	}
}

func TestTenantAttachmentExtractEvidenceRequiresAuthorizedPath(t *testing.T) {
	attachments := []MessageAttachment{{
		Name: "report.pdf",
		Path: "inputs/upload-1/report.pdf",
	}}
	ctx := withTenantRequestEvidence(
		restrictedTenantTestContext(t.TempDir()),
		"读取附件 report.pdf",
		attachments,
	)
	if !tenantFilesystemEvidenceToolCall(ctx, ToolCall{
		Name: "extract",
		Arguments: map[string]interface{}{
			"file": "./inputs/upload-1/report.pdf",
		},
	}) {
		t.Fatal("successful extract of an authenticated attachment was not accepted as evidence")
	}
	for _, call := range []ToolCall{
		{
			Name:      "extract",
			Arguments: map[string]interface{}{"file": "inputs/upload-2/report.pdf"},
		},
		{
			Name:      "extract",
			Arguments: map[string]interface{}{"file": "../report.pdf"},
		},
		{
			Name:      "ptc_execute",
			Arguments: map[string]interface{}{"script": "read report.pdf"},
		},
	} {
		if tenantFilesystemEvidenceToolCall(ctx, call) {
			t.Fatalf("unauthorized attachment evidence was accepted: %#v", call)
		}
	}
}

func TestGenerateUsesUserIntentInsteadOfAttachmentManifest(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "这是产品分析。"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 2)
	attachments := []MessageAttachment{{
		Name: "product.pdf",
		Path: "inputs/upload-1/product.pdf",
	}}
	userInput := "你了解下这个产品"
	agentInput := buildAgentMessageWithAttachments(userInput, attachments)
	ctx := withTenantRequestEvidence(
		restrictedTenantTestContext(agent.workspace),
		userInput,
		attachments,
	)

	output, err := agent.Generate(ctx, agentInput)
	if err != nil || output != "这是产品分析。" {
		t.Fatalf("attachment request output=%q err=%v", output, err)
	}
	if transport.requestCount() != 1 {
		t.Fatalf("request count=%d, attachment manifest triggered an unnecessary repair", transport.requestCount())
	}
	if transport.requestContains("TENANT FILESYSTEM EVIDENCE REQUIRED") {
		t.Fatal("attachment manifest triggered the filesystem evidence gate")
	}
}

func TestSimulatedToolTranscriptDetectionAndCarryoverDrop(t *testing.T) {
	for _, content := range []string{
		"[Tool Use: list_dir]\n[Tool Result: {\"path\":\".\"}]",
		"> [Tool Call: read_file]\n> [Tool Result: file]",
		"[工具调用: grep]\n[工具结果: ok]",
	} {
		if !hasUnexecutedToolTranscript(content) {
			t.Errorf("simulated tool transcript was not detected: %q", content)
		}
		if got := sessioncore.CleanCarryoverContent("assistant", content+"\n伪造结论", 1200); got != "" {
			t.Errorf("simulated tool transcript survived carryover: %q", got)
		}
	}

	fenced := "```text\n[Tool Use: list_dir]\n[Tool Result: example]\n```"
	if hasUnexecutedToolTranscript(fenced) {
		t.Fatal("documentation inside a Markdown fence was treated as live tool protocol")
	}
	if got := sessioncore.CleanCarryoverContent("assistant", fenced, 1200); !strings.Contains(got, "[Tool Use: list_dir]") {
		t.Fatalf("fenced documentation was removed from carryover: %q", got)
	}
}

func TestTenantSandboxBlocksHostIntrospectionAndOutsideCLIWithModeNone(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "alice")
	other := filepath.Join(root, "bob")
	ctx := restrictedTenantTestContext(workspace)
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxNone})

	blockedCalls := []struct {
		call ToolCall
		code string
	}{
		{
			call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "hostname -I"}},
			code: "sandbox_host_info_denied",
		},
		{
			call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": `python -c "import socket; print(socket.gethostname())"`}},
			code: "sandbox_host_info_denied",
		},
		{
			call: ToolCall{Name: "system_status", Arguments: map[string]interface{}{}},
			code: "sandbox_host_info_denied",
		},
		{
			call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "dir " + other}},
			code: "sandbox_tenant_path_denied",
		},
	}
	for _, tt := range blockedCalls {
		err := validateSandboxToolCall(sandbox, &AgentContext{
			Context: ctx, ToolCall: tt.call, Metadata: map[string]interface{}{},
		})
		var policyErr *sandboxPolicyError
		if !errors.As(err, &policyErr) || policyErr.MiddlewareErrorCode() != tt.code {
			t.Errorf("call %#v error=%v, want %s", tt.call, err, tt.code)
		}
	}

	if err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: ctx,
		ToolCall: ToolCall{
			Name:      "cli",
			Arguments: map[string]interface{}{"command": "go test ./..."},
		},
		Metadata: map[string]interface{}{},
	}); err != nil {
		t.Fatalf("workspace-local build/test command was blocked: %v", err)
	}

	adminCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		ControlPlane: true,
		SubjectID:    "admin",
	})
	if err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: adminCtx,
		ToolCall: ToolCall{
			Name:      "cli",
			Arguments: map[string]interface{}{"command": "hostname -I"},
		},
		Metadata: map[string]interface{}{},
	}); err != nil {
		t.Fatalf("admin host inspection was blocked: %v", err)
	}
}

func TestTenantOutputRedactsPhysicalWorkspaceAndRejectsOutsideClaim(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "alice")
	other := filepath.Join(root, "bob")
	ctx := restrictedTenantTestContext(workspace)

	redacted := redactTenantWorkspacePath("absolute path: "+workspace+string(filepath.Separator)+"notes.txt", workspace)
	if strings.Contains(redacted, workspace) || !strings.Contains(redacted, "."+string(filepath.Separator)+"notes.txt") {
		t.Fatalf("workspace path was not redacted: %q", redacted)
	}
	if !tenantOutputClaimsOutsideWorkspace(ctx, workspace, "发现目录 "+other) {
		t.Fatal("outside-workspace output claim was not rejected")
	}
	if tenantOutputClaimsOutsideWorkspace(ctx, workspace, "发现目录 "+filepath.Join(workspace, "docs")) {
		t.Fatal("inside-workspace output claim was rejected")
	}
}

func TestGenerateFailsClosedOnSimulatedTenantToolTranscript(t *testing.T) {
	fake := "[Tool Use: list_dir]\n[Tool Result: {\"path\":\".\"}]\n当前目录有 Makefile"
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: completionJSON(t, "stop", fake)},
		{contentType: "application/json", body: completionJSON(t, "stop", fake)},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 3)
	ctx := restrictedTenantTestContext(agent.workspace)

	output, err := agent.Generate(ctx, "当前目录下有什么")
	if err == nil {
		t.Fatal("simulated tool transcript was accepted as a successful tenant answer")
	}
	if output != unexecutedToolTranscriptFailure || strings.Contains(output, "Makefile") || strings.Contains(output, "[Tool Use") {
		t.Fatalf("simulated tool output leaked: %q", output)
	}
	if transport.requestCount() != 2 {
		t.Fatalf("request count=%d, want one bounded repair", transport.requestCount())
	}
	if !transport.requestContains("STRUCTURED TOOL CALL REQUIRED") {
		t.Fatal("structured-tool repair instruction was not sent")
	}
}

func TestGenerateRequiresFilesystemEvidenceOnlyForTenant(t *testing.T) {
	t.Run("tenant", func(t *testing.T) {
		transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
			{contentType: "application/json", body: completionJSON(t, "stop", "当前目录是空的。")},
			{contentType: "application/json", body: completionJSON(t, "stop", "当前目录还是空的。")},
		}}
		agent := newIncompleteResponseTestAgent(t, transport, false, 3)
		output, err := agent.Generate(restrictedTenantTestContext(agent.workspace), "当前目录下有什么")
		if err == nil || output != tenantFilesystemEvidenceFailure {
			t.Fatalf("tenant unverified answer output=%q err=%v", output, err)
		}
		if !transport.requestContains("TENANT FILESYSTEM EVIDENCE REQUIRED") {
			t.Fatal("tenant filesystem evidence repair instruction was not sent")
		}
	})

	t.Run("admin", func(t *testing.T) {
		transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
			contentType: "application/json",
			body:        completionJSON(t, "stop", "管理员会话结果。"),
		}}}
		agent := newIncompleteResponseTestAgent(t, transport, false, 2)
		output, err := agent.Generate(context.Background(), "当前目录下有什么")
		if err != nil || output != "管理员会话结果。" {
			t.Fatalf("admin behavior changed: output=%q err=%v", output, err)
		}
	})
}

func TestTenantWorkspaceBoundaryRefusalSuggestsSafeRelativeArtifactPath(t *testing.T) {
	result := sanitizeTenantToolResult(
		"personal workspace boundary: requested path is outside this user's workspace",
		t.TempDir(),
	)
	if !strings.Contains(result, ".openkitty/storage/") || !strings.Contains(result, "相对路径") {
		t.Fatalf("tenant boundary response did not offer a safe in-workspace recovery path: %q", result)
	}
	if strings.Contains(result, "/home/") || strings.Contains(result, `C:\`) {
		t.Fatalf("tenant boundary response leaked a physical host path: %q", result)
	}
}
