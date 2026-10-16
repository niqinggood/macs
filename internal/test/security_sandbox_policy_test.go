package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	securitytools "mac/internal/tools/security"
)

type SandboxMode = securitytools.SandboxMode
type SandboxConfig = securitytools.SandboxConfig
type Sandbox = securitytools.Sandbox
type SandboxTool = securitytools.SandboxTool

const (
	SandboxNone     = securitytools.SandboxNone
	SandboxReadOnly = securitytools.SandboxReadOnly
	SandboxStrict   = securitytools.SandboxStrict
)

var (
	DefaultSandboxConfig = securitytools.DefaultSandboxConfig
	NewSandbox           = securitytools.NewSandbox
	NewWorkspaceSandbox  = securitytools.NewWorkspaceSandbox
	NewSandboxTool       = securitytools.NewSandboxTool
)

func validateSandboxConfig(label string, config *SandboxConfig) error {
	return securitytools.ValidateSandboxConfig(label, config)
}

func mergeSandboxConfig(base SandboxConfig, override *SandboxConfig) SandboxConfig {
	return securitytools.MergeSandboxConfig(base, override)
}

func pathWithinRoot(root, candidate string) bool {
	return securitytools.PathWithinRoot(root, candidate)
}

type sandboxPolicyTestTool struct {
	name     string
	readOnly bool
	caps     ToolSandboxCapabilities
	calls    atomic.Int32
}

func (t *sandboxPolicyTestTool) Name() string        { return t.name }
func (t *sandboxPolicyTestTool) Description() string { return "sandbox policy test tool" }
func (t *sandboxPolicyTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": true}
}
func (t *sandboxPolicyTestTool) IsReadOnly() bool { return t.readOnly }
func (t *sandboxPolicyTestTool) SandboxCapabilities() ToolSandboxCapabilities {
	return t.caps
}
func (t *sandboxPolicyTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	t.calls.Add(1)
	return "executed", nil
}

func TestSandboxDefaultsToNoneAndStrictRemainsOptIn(t *testing.T) {
	if got := DefaultSandboxConfig().Mode; got != SandboxNone {
		t.Fatalf("default sandbox mode = %q, want %q", got, SandboxNone)
	}

	root := t.TempDir()
	externalPath := filepath.Join(filepath.Dir(root), "external-materials", "requirements.docx")

	unrestrictedTool := &sandboxPolicyTestTool{name: "read_file", readOnly: true}
	unrestrictedRegistry := NewToolRegistry()
	unrestrictedRegistry.Register(unrestrictedTool)
	unrestrictedRegistry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(
		NewWorkspaceSandbox(root, SandboxConfig{}),
	)))
	unrestrictedResult, err := unrestrictedRegistry.Execute(context.Background(), ToolCall{
		ID: "default-external-read", Name: "read_file",
		Arguments: map[string]interface{}{"path": externalPath},
	})
	if err != nil || !ToolResultExecutionSucceeded(unrestrictedResult, err) || unrestrictedTool.calls.Load() != 1 {
		t.Fatalf("default mode blocked external path: result=%+v err=%v calls=%d", unrestrictedResult, err, unrestrictedTool.calls.Load())
	}

	strictTool := &sandboxPolicyTestTool{name: "read_file", readOnly: true}
	strictRegistry := NewToolRegistry()
	strictRegistry.Register(strictTool)
	strictRegistry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(
		NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxStrict}),
	)))
	strictResult, err := strictRegistry.Execute(context.Background(), ToolCall{
		ID: "strict-external-read", Name: "read_file",
		Arguments: map[string]interface{}{"path": externalPath},
	})
	if err != nil || strictResult.ErrorCode != "sandbox_path_denied" || strictTool.calls.Load() != 0 {
		t.Fatalf("strict mode did not block external path: result=%+v err=%v calls=%d", strictResult, err, strictTool.calls.Load())
	}
}

func TestDefaultSandboxAllowsRealListDirOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	externalDir := t.TempDir()
	const canary = "proposal-material.docx"
	if err := os.WriteFile(filepath.Join(externalDir, canary), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := NewToolRegistry()
	registry.Register(NewListDirTool(workspace))
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(
		NewWorkspaceSandbox(workspace, SandboxConfig{}),
	)))
	result, err := registry.Execute(context.Background(), ToolCall{
		ID: "default-real-external-list", Name: "list_dir",
		Arguments: map[string]interface{}{"path": externalDir, "depth": float64(1)},
	})
	if err != nil || !ToolResultExecutionSucceeded(result, err) {
		t.Fatalf("default mode blocked real external list_dir: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Content, canary) {
		t.Fatalf("external list_dir did not return canary %q: %s", canary, result.Content)
	}
}

func TestSandboxCommandPathsPreservesQuotedAbsolutePathWithSpaces(t *testing.T) {
	want := `E:\产品资料\OPC demans`
	paths := sandboxCommandPaths(`Get-ChildItem -LiteralPath "` + want + `"`)
	for _, got := range paths {
		if got == want {
			return
		}
	}
	t.Fatalf("quoted absolute path %q was not preserved: %#v", want, paths)
}

func TestSandboxPathContainmentUsesRealBoundaries(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	sibling := filepath.Join(parent, "workspace-escape")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	sandbox := NewWorkspaceSandbox(root, SandboxConfig{
		Mode: SandboxStrict, AllowedPaths: []string{"src"},
		DeniedPaths: []string{".env", ".git/config"},
	})

	if allowed, reason := sandbox.ValidatePath(filepath.Join(root, "src", "main.go")); !allowed {
		t.Fatalf("workspace path denied: %s", reason)
	}
	for _, denied := range []string{
		filepath.Join(root, "src-other", "main.go"),
		filepath.Join(sibling, "main.go"),
		filepath.Join(root, "src", ".env"),
		filepath.Join(root, "src", ".git", "config"),
	} {
		if allowed, _ := sandbox.ValidatePath(denied); allowed {
			t.Fatalf("path escaped sandbox: %s", denied)
		}
	}

	if runtime.GOOS != "windows" {
		outside := filepath.Join(parent, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "src", "linked")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if allowed, _ := sandbox.ValidatePath(filepath.Join(link, "new.txt")); allowed {
			t.Fatal("symlinked parent escaped sandbox")
		}
	}
}

func TestSandboxNetworkBoundaryAndPrivateAddressProtection(t *testing.T) {
	root := t.TempDir()
	allowlisted := NewWorkspaceSandbox(root, SandboxConfig{
		Mode: SandboxStrict, AllowedHosts: []string{"api.example.com"},
	})
	for _, target := range []string{"api.example.com", "https://api.example.com/v1", "child.api.example.com"} {
		if allowed, reason := allowlisted.ValidateNetwork(target); !allowed {
			t.Fatalf("allowlisted target %q denied: %s", target, reason)
		}
	}
	for _, target := range []string{"evilapi.example.com", "example.com", "169.254.169.254"} {
		if allowed, _ := allowlisted.ValidateNetwork(target); allowed {
			t.Fatalf("network target escaped allowlist: %s", target)
		}
	}

	publicOnly := NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxStrict})
	for _, target := range []string{"127.0.0.1", "10.0.0.8", "http://localhost:8080", "0.0.0.0"} {
		if allowed, _ := publicOnly.ValidateNetwork(target); allowed {
			t.Fatalf("private target allowed: %s", target)
		}
	}
	if allowed, reason := publicOnly.ValidateNetwork("https://openai.com"); !allowed {
		t.Fatalf("public target denied: %s", reason)
	}
}

func TestSandboxMiddlewareCoversFileShellNetworkAndReadonly(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	strict := NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxStrict})

	writeTool := &sandboxPolicyTestTool{name: "write_file"}
	registry := NewToolRegistry()
	registry.Register(writeTool)
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(strict)))

	blocked, err := registry.Execute(context.Background(), ToolCall{
		ID: "outside-write", Name: "write_file",
		Arguments: map[string]interface{}{"path": outside, "content": "no"},
	})
	if err != nil || blocked.ErrorCode != "sandbox_path_denied" || writeTool.calls.Load() != 0 {
		t.Fatalf("outside write result=%+v err=%v calls=%d", blocked, err, writeTool.calls.Load())
	}
	inside, err := registry.Execute(context.Background(), ToolCall{
		ID: "inside-write", Name: "write_file",
		Arguments: map[string]interface{}{"path": filepath.Join(root, "ok.txt"), "content": "ok"},
	})
	if err != nil || !ToolResultExecutionSucceeded(inside, err) || writeTool.calls.Load() != 1 {
		t.Fatalf("inside write result=%+v err=%v calls=%d", inside, err, writeTool.calls.Load())
	}

	cliTool := &sandboxPolicyTestTool{name: "cli"}
	registry.Register(cliTool)
	command := "type " + outside
	if runtime.GOOS != "windows" {
		command = "cat " + outside
	}
	shellResult, err := registry.Execute(context.Background(), ToolCall{
		ID: "shell-escape", Name: "cli",
		Arguments: map[string]interface{}{"command": command},
	})
	if err != nil || shellResult.ErrorCode != "sandbox_path_denied" || cliTool.calls.Load() != 0 {
		t.Fatalf("shell escape result=%+v err=%v calls=%d", shellResult, err, cliTool.calls.Load())
	}

	httpTool := &sandboxPolicyTestTool{name: "http", readOnly: true}
	registry.Register(httpTool)
	networkResult, err := registry.Execute(context.Background(), ToolCall{
		ID: "private-http", Name: "http",
		Arguments: map[string]interface{}{"url": "http://127.0.0.1/admin"},
	})
	if err != nil || networkResult.ErrorCode != "sandbox_network_denied" || httpTool.calls.Load() != 0 {
		t.Fatalf("private network result=%+v err=%v calls=%d", networkResult, err, httpTool.calls.Load())
	}

	readonly := NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxReadOnly})
	readRegistry := NewToolRegistry()
	readTool := &sandboxPolicyTestTool{name: "read_file", readOnly: true}
	readRegistry.Register(readTool)
	readRegistry.Register(writeTool)
	readRegistry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(readonly)))
	readResult, err := readRegistry.Execute(context.Background(), ToolCall{
		ID: "read", Name: "read_file",
		Arguments: map[string]interface{}{"path": filepath.Join(root, "ok.txt")},
	})
	if err != nil || !ToolResultExecutionSucceeded(readResult, err) || readTool.calls.Load() != 1 {
		t.Fatalf("readonly read result=%+v err=%v", readResult, err)
	}
	writeResult, err := readRegistry.Execute(context.Background(), ToolCall{
		ID: "readonly-write", Name: "write_file",
		Arguments: map[string]interface{}{"path": filepath.Join(root, "blocked.txt"), "content": "no"},
	})
	if err != nil || !strings.Contains(writeResult.ErrorCode, "sandbox_readonly") {
		t.Fatalf("readonly write result=%+v err=%v", writeResult, err)
	}
}

func TestChildSandboxCannotWeakenInheritedPolicy(t *testing.T) {
	root := t.TempDir()
	parent := NewWorkspaceSandbox(root, SandboxConfig{
		Mode: SandboxStrict, NetworkPolicy: "deny",
	})
	child := NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxNone})
	tool := &sandboxPolicyTestTool{name: "web_fetch", readOnly: true}
	registry := NewToolRegistry()
	registry.Register(tool)
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(child)))
	ctx := withSandboxPolicy(context.Background(), parent)

	result, err := registry.Execute(ctx, ToolCall{
		ID: "child-network", Name: "web_fetch",
		Arguments: map[string]interface{}{"url": "https://example.com"},
	})
	if err != nil || result.ErrorCode != "sandbox_network_denied" || tool.calls.Load() != 0 {
		t.Fatalf("inherited policy result=%+v err=%v calls=%d", result, err, tool.calls.Load())
	}
}

func TestSandboxUsesDeclaredCapabilitiesForDynamicTools(t *testing.T) {
	root := t.TempDir()
	testCases := []struct {
		name      string
		config    SandboxConfig
		caps      ToolSandboxCapabilities
		errorCode string
	}{
		{
			name:      "opaque_process_connector",
			config:    SandboxConfig{Mode: SandboxStrict, ShellPolicy: "deny"},
			caps:      ToolSandboxCapabilities{Process: true},
			errorCode: "sandbox_shell_denied",
		},
		{
			name:      "opaque_network_connector",
			config:    SandboxConfig{Mode: SandboxStrict, NetworkPolicy: "deny"},
			caps:      ToolSandboxCapabilities{Network: true},
			errorCode: "sandbox_network_denied",
		},
		{
			name:      "opaque_agent_connector",
			config:    SandboxConfig{Mode: SandboxStrict, SubAgentPolicy: "deny"},
			caps:      ToolSandboxCapabilities{SubAgent: true},
			errorCode: "sandbox_sub_agent_denied",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := &sandboxPolicyTestTool{
				name: testCase.name, readOnly: true, caps: testCase.caps,
			}
			registry := NewToolRegistry()
			registry.Register(tool)
			registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(
				NewWorkspaceSandbox(root, testCase.config),
			)))

			result, err := registry.Execute(context.Background(), ToolCall{
				ID: testCase.name, Name: testCase.name,
				Arguments: map[string]interface{}{},
			})
			if err != nil || result.ErrorCode != testCase.errorCode || tool.calls.Load() != 0 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, tool.calls.Load())
			}
		})
	}
}

func TestSandboxNetworkDenyKeepsLocalCLIUsable(t *testing.T) {
	root := t.TempDir()
	sandbox := NewWorkspaceSandbox(root, SandboxConfig{
		Mode: SandboxStrict, ShellPolicy: "allow", NetworkPolicy: "deny",
	})
	tool := &sandboxPolicyTestTool{
		name: "cli", readOnly: true, caps: ToolSandboxCapabilities{Process: true},
	}
	registry := NewToolRegistry()
	registry.Register(tool)
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(sandbox)))

	localResult, err := registry.Execute(context.Background(), ToolCall{
		ID: "local-command", Name: "cli",
		Arguments: map[string]interface{}{"command": "echo local-only"},
	})
	if err != nil || !ToolResultExecutionSucceeded(localResult, err) || tool.calls.Load() != 1 {
		t.Fatalf("local CLI result=%+v err=%v calls=%d", localResult, err, tool.calls.Load())
	}

	networkResult, err := registry.Execute(context.Background(), ToolCall{
		ID: "network-command", Name: "cli",
		Arguments: map[string]interface{}{"command": "curl https://example.com"},
	})
	if err != nil || networkResult.ErrorCode != "sandbox_network_denied" || tool.calls.Load() != 1 {
		t.Fatalf("network CLI result=%+v err=%v calls=%d", networkResult, err, tool.calls.Load())
	}
}

func TestSandboxExecutionPathsAllowTrustedInterpreterButKeepDataBounded(t *testing.T) {
	allowed := sandboxExecutionCommandPaths(`/usr/bin/python3 ./scripts/build.py --output ./.openkitty/storage/result.json`)
	if strings.Contains(strings.Join(allowed, "|"), "/usr/bin/python3") {
		t.Fatalf("trusted interpreter remained a workspace data path: %v", allowed)
	}
	for _, expected := range []string{"./scripts/build.py", "./.openkitty/storage/result.json"} {
		if !containsString(allowed, expected) {
			t.Fatalf("workspace data path %q missing from %v", expected, allowed)
		}
	}

	outside := sandboxExecutionCommandPaths(`/usr/bin/cat /etc/passwd`)
	if containsString(outside, "/usr/bin/cat") || !containsString(outside, "/etc/passwd") {
		t.Fatalf("executable/data path separation failed: %v", outside)
	}
	if paths := sandboxExecutionCommandPaths(`/tmp/untrusted-tool ./input.txt`); !containsString(paths, "/tmp/untrusted-tool") {
		t.Fatalf("untrusted executable path was relaxed: %v", paths)
	}
}

func TestTenantSandboxAllowsSystemExecutableWithWorkspaceData(t *testing.T) {
	workspace := t.TempDir()
	ctx := restrictedTenantTestContext(workspace)
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxNone})

	command := `/usr/bin/python3 ./scripts/build.py`
	if runtime.GOOS == "windows" {
		command = `"C:\Windows\System32\cmd.exe" /c type .\scripts\build.py`
	}
	if err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: ctx,
		ToolCall: ToolCall{Name: "cli", Arguments: map[string]interface{}{
			"command": command,
		}},
		Metadata: map[string]interface{}{},
	}); err != nil {
		t.Fatalf("trusted executable with workspace data was blocked: %v", err)
	}
}

func TestTenantSandboxChecksCodeCheckArgumentFiles(t *testing.T) {
	workspace := t.TempDir()
	ctx := restrictedTenantTestContext(workspace)
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxNone})
	baseCall := ToolCall{Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `.openkitty/runtime/builtin/nova-community/scripts/chatroom_agent.py`,
		"args":   `publish @".openkitty/storage/.system/nova-community/prepared.json" --intent-id task-1`,
	}}
	if err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: ctx, ToolCall: baseCall, Metadata: map[string]interface{}{},
	}); err != nil {
		t.Fatalf("workspace-local bundled client call was blocked: %v", err)
	}

	escape := baseCall
	escape.Arguments = map[string]interface{}{
		"action": "run",
		"file":   `.openkitty/runtime/builtin/nova-community/scripts/chatroom_agent.py`,
		"args":   `publish @"../other-user/prepared.json" --intent-id task-1`,
	}
	err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: ctx, ToolCall: escape, Metadata: map[string]interface{}{},
	})
	var policyErr *sandboxPolicyError
	if err == nil || !errors.As(err, &policyErr) || policyErr.MiddlewareErrorCode() != "sandbox_tenant_path_denied" {
		t.Fatalf("argument path escape error=%v", err)
	}
}

func TestTenantSandboxChecksAllFileToolPathsWhenModeIsNone(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(workspace), "other-user", "secret.pdf")
	ctx := restrictedTenantTestContext(workspace)
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxNone})

	blockedCalls := []ToolCall{
		{Name: "list_dir", Arguments: map[string]interface{}{"path": filepath.Dir(outside)}},
		{Name: "extract", Arguments: map[string]interface{}{"file": outside}},
		{Name: "extract", Arguments: map[string]interface{}{
			"file":   filepath.Join(workspace, "input.pdf"),
			"output": filepath.Join(filepath.Dir(outside), "stolen.md"),
		}},
	}
	for _, call := range blockedCalls {
		err := validateSandboxToolCall(sandbox, &AgentContext{
			Context: ctx, ToolCall: call, Metadata: map[string]interface{}{},
		})
		var policyErr *sandboxPolicyError
		if !errors.As(err, &policyErr) || policyErr.MiddlewareErrorCode() != "sandbox_tenant_path_denied" {
			t.Errorf("call %#v error=%v, want sandbox_tenant_path_denied", call, err)
		}
	}
}

func TestStrictSandboxChecksExtractOutputPath(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(workspace), "outside.md")
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxStrict})
	err := validateSandboxToolCall(sandbox, &AgentContext{
		Context: context.Background(),
		ToolCall: ToolCall{Name: "extract", Arguments: map[string]interface{}{
			"file":   filepath.Join(workspace, "input.pdf"),
			"output": outside,
		}},
		Metadata: map[string]interface{}{},
	})
	var policyErr *sandboxPolicyError
	if !errors.As(err, &policyErr) || policyErr.MiddlewareErrorCode() != "sandbox_path_denied" {
		t.Fatalf("extract output error=%v, want sandbox_path_denied", err)
	}
}

func TestSandboxDeniedCallDoesNotRequestApproval(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	sandbox := NewWorkspaceSandbox(root, SandboxConfig{Mode: SandboxStrict})
	tool := &sandboxPolicyTestTool{name: "write_file"}
	registry := NewToolRegistry()
	registry.Register(tool)

	permissions := NewPermissionEngine(PermissionDefault)
	permissions.AddRule(PermissionRule{
		Source: SourcePolicy, Behavior: BehaviorAsk, ToolName: "write_file",
	})
	call := ToolCall{
		ID: "outside-ask", Name: "write_file",
		Arguments: map[string]interface{}{"path": outside, "content": "blocked"},
	}
	if err := preflightToolApprovals(
		context.Background(), permissions, registry, sandbox, []ToolCall{call},
	); err != nil {
		t.Fatalf("sandbox-denied action should not open an approval: %v", err)
	}

	registry.SetChain(NewMiddlewareChain(
		NewSandboxPolicyMiddleware(sandbox),
		NewExecutionPolicyMiddleware(permissions, nil, nil),
	))
	result, err := registry.Execute(context.Background(), call)
	if err != nil || result.ErrorCode != "sandbox_path_denied" || tool.calls.Load() != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, tool.calls.Load())
	}
}

func TestSandboxHostAllowlistCoversDeclaredConnectorTargets(t *testing.T) {
	root := t.TempDir()
	sandbox := NewWorkspaceSandbox(root, SandboxConfig{
		Mode: SandboxStrict, AllowedHosts: []string{"api.example.com"},
	})
	testCases := []struct {
		name      string
		targets   []string
		wantError string
	}{
		{
			name:    "allowlisted_connector",
			targets: []string{"https://api.example.com/mcp"},
		},
		{
			name:      "off_list_connector",
			targets:   []string{"https://evil.example/mcp"},
			wantError: "sandbox_network_denied",
		},
		{
			name:      "opaque_connector",
			wantError: "sandbox_network_denied",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := &sandboxPolicyTestTool{
				name: testCase.name, readOnly: true,
				caps: ToolSandboxCapabilities{
					Network: true, NetworkTargets: testCase.targets,
				},
			}
			registry := NewToolRegistry()
			registry.Register(tool)
			registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(sandbox)))
			result, err := registry.Execute(context.Background(), ToolCall{
				ID: testCase.name, Name: testCase.name,
				Arguments: map[string]interface{}{},
			})
			if err != nil || result.ErrorCode != testCase.wantError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			wantCalls := int32(1)
			if testCase.wantError != "" {
				wantCalls = 0
			}
			if tool.calls.Load() != wantCalls {
				t.Fatalf("calls=%d want=%d", tool.calls.Load(), wantCalls)
			}
		})
	}
}
