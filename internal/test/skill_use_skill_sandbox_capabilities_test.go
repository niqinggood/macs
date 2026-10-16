package app

import (
	"context"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"
	"mac/internal/toolcontract"
)

func TestUseSkillSandboxCapabilitiesDependOnSelectedSkill(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	loader.RegisterSkill("local-memo", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "local-memo", Type: skilldomain.SkillTypeBuiltin},
		Content:  "Follow this local memo format.",
		Source:   "user",
	})
	loader.RegisterSkill("remote-api", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "remote-api", Type: skilldomain.SkillTypeHTTP},
		Source:   "user",
	})
	loader.RegisterSkill("local-script", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "local-script", Type: skilldomain.SkillTypePython},
		Source:   "user",
	})
	loader.RegisterSkill("runtime-doc", &skilldomain.Skill{
		Metadata:        skilldomain.SkillMetadata{Name: "runtime-doc", Type: skilldomain.SkillTypeBuiltin},
		Source:          "builtin",
		RequiresRuntime: true,
	})

	tool := NewUseSkillTool(loader, nil)
	if got := tool.SandboxCapabilitiesForCall(map[string]interface{}{"skill_name": "local-memo"}); got.Process || got.Network || got.SubAgent || len(got.NetworkTargets) != 0 {
		t.Fatalf("local instruction skill capabilities = %+v, want none", got)
	}
	if got := tool.SandboxCapabilitiesForCall(map[string]interface{}{"skill_name": "remote-api"}); !got.Network || got.Process {
		t.Fatalf("HTTP skill capabilities = %+v, want network only", got)
	}
	if got := tool.SandboxCapabilitiesForCall(map[string]interface{}{"skill_name": "local-script"}); !got.Process || got.Network {
		t.Fatalf("script skill capabilities = %+v, want process only", got)
	}
	if !tool.IsReadOnlyForCall(map[string]interface{}{"skill_name": "local-memo"}) {
		t.Fatal("local instruction skill should be read-only")
	}
	for _, name := range []string{"remote-api", "local-script", "runtime-doc", "missing"} {
		if tool.IsReadOnlyForCall(map[string]interface{}{"skill_name": name}) {
			t.Fatalf("skill %q was incorrectly classified read-only", name)
		}
	}
	registry := NewToolRegistry()
	registry.Register(tool)
	if got := toolcontract.ClassifyToolParallel("use_skill", map[string]interface{}{"skill_name": "local-memo"}, registry, nil); got != toolcontract.SafeParallel {
		t.Fatalf("local instruction parallel class=%v, want safe", got)
	}
	if got := toolcontract.ClassifyToolParallel("use_skill", map[string]interface{}{"skill_name": "runtime-doc"}, registry, nil); got < toolcontract.MustSerial {
		t.Fatalf("runtime-materializing parallel class=%v, want serial", got)
	}
}

func TestReadOnlySandboxBlocksRuntimeMaterializingSkill(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "Assistant", "")
	loader.RegisterSkill("runtime-doc", &skilldomain.Skill{
		Metadata:        skilldomain.SkillMetadata{Name: "runtime-doc", Type: skilldomain.SkillTypeBuiltin},
		Content:         "Use the runtime helper.",
		Source:          "builtin",
		RequiresRuntime: true,
	})
	registry := NewToolRegistry()
	registry.Register(NewUseSkillTool(loader, registry))
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(NewWorkspaceSandbox(
		workspace,
		SandboxConfig{Mode: SandboxReadOnly, NetworkPolicy: "deny", ShellPolicy: "deny"},
	))))

	result, err := registry.Execute(context.Background(), ToolCall{
		ID: "runtime", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "runtime-doc"},
	})
	if err != nil {
		t.Fatalf("sandbox denial should be returned as a tool result: %v", err)
	}
	if ToolResultExecutionSucceeded(result, err) {
		t.Fatalf("runtime materialization bypassed read-only sandbox: %+v", result)
	}
}

func TestStrictOfflineSandboxAllowsLocalInstructionSkillAndBlocksHTTPSkill(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "Assistant", "")
	loader.RegisterSkill("local-memo", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "local-memo", Type: skilldomain.SkillTypeBuiltin},
		Content:  "RISK-MEMO-V1 instructions",
		Source:   "user",
	})
	loader.RegisterSkill("remote-api", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "remote-api", Type: skilldomain.SkillTypeHTTP},
		Source:   "user",
	})

	registry := NewToolRegistry()
	registry.Register(NewUseSkillTool(loader, registry))
	registry.SetChain(NewMiddlewareChain(NewSandboxPolicyMiddleware(NewWorkspaceSandbox(
		workspace,
		SandboxConfig{Mode: SandboxStrict, NetworkPolicy: "deny", ShellPolicy: "allow"},
	))))

	local, err := registry.Execute(context.Background(), ToolCall{
		ID: "local", Name: "use_skill",
		Arguments: map[string]interface{}{"skill_name": "local-memo"},
	})
	if err != nil || !ToolResultExecutionSucceeded(local, err) || !strings.Contains(local.Content, "技能: local-memo") {
		t.Fatalf("local instruction skill was blocked: result=%+v err=%v", local, err)
	}

	remote, err := registry.Execute(context.Background(), ToolCall{
		ID: "remote", Name: "use_skill",
		Arguments: map[string]interface{}{"skill_name": "remote-api"},
	})
	if err != nil {
		t.Fatalf("sandbox denial should be returned as a tool result, got err=%v", err)
	}
	if ToolResultExecutionSucceeded(remote, err) || remote.ErrorCode != "sandbox_network_denied" {
		t.Fatalf("HTTP skill bypassed offline sandbox: result=%+v", remote)
	}
}
