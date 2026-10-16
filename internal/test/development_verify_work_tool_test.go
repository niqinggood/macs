package app

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type verificationTestAgent struct {
	generate func(context.Context, string) (string, error)
}

func (a *verificationTestAgent) Name() string       { return "verification-test" }
func (a *verificationTestAgent) GetType() AgentType { return AgentTypeCustom }
func (a *verificationTestAgent) Generate(ctx context.Context, input string) (string, error) {
	return a.generate(ctx, input)
}

func newVerificationTestSubAgent(t *testing.T, create func(AgentConfig) Agent) *SubAgentTool {
	t.Helper()
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(config AgentConfig, _ string) (Agent, error) {
			return create(config), nil
		},
	}
	return &SubAgentTool{
		agentFactory: factory,
		fileLogger:   log.New(io.Discard, "", 0),
		activeAgents: make(map[string]*subAgentSession),
		definitions:  make(map[string]DiscoveredAgent),
	}
}

func TestVerifyWorkToolParameters(t *testing.T) {
	params := NewVerifyWorkTool(nil).Parameters()
	props := params["properties"].(map[string]interface{})
	for _, key := range []string{"claim", "acceptance_criteria", "evidence", "max_iterations"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing parameter %q", key)
		}
	}
}

func TestVerifyWorkToolRequiresSubAgentTool(t *testing.T) {
	_, err := NewVerifyWorkTool(nil).Execute(context.Background(), map[string]interface{}{
		"claim": "the implementation is complete",
	})
	if err == nil || !strings.Contains(err.Error(), "verification agent is not available") {
		t.Fatalf("expected unavailable verification agent error, got %v", err)
	}
}

func TestStringSliceParam(t *testing.T) {
	got := stringSliceParam([]interface{}{" web/style.css ", "", 42, "go test ./..."})
	if len(got) != 2 || got[0] != "web/style.css" || got[1] != "go test ./..." {
		t.Fatalf("unexpected string slice: %#v", got)
	}
}

func TestVerifyWorkDefaultsToTwoIterations(t *testing.T) {
	var got int
	sub := newVerificationTestSubAgent(t, func(config AgentConfig) Agent {
		got = config.MaxIterations
		return &verificationTestAgent{generate: func(context.Context, string) (string, error) {
			return "EVIDENCE:\n- go test ./... passed\nVERDICT: PASS", nil
		}}
	})
	result, err := NewVerifyWorkTool(sub).Execute(context.Background(), map[string]interface{}{
		"claim": "done",
	})
	if err != nil || !strings.Contains(result, "VERDICT: PASS") {
		t.Fatalf("unexpected result=%q err=%v", result, err)
	}
	if got != 2 {
		t.Fatalf("verification max iterations = %d, want 2", got)
	}
}

func TestVerifyWorkDowngradesPassWithoutEvidence(t *testing.T) {
	sub := newVerificationTestSubAgent(t, func(AgentConfig) Agent {
		return &verificationTestAgent{generate: func(context.Context, string) (string, error) {
			return "Everything looks good.\nVERDICT: PASS", nil
		}}
	})
	result, err := NewVerifyWorkTool(sub).Execute(context.Background(), map[string]interface{}{"claim": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "VERDICT: PARTIAL") || !strings.Contains(result, "requires an EVIDENCE section") {
		t.Fatalf("unsupported pass was not downgraded: %s", result)
	}
}

func TestVerifyWorkAddsPartialWhenVerdictMissing(t *testing.T) {
	sub := newVerificationTestSubAgent(t, func(AgentConfig) Agent {
		return &verificationTestAgent{generate: func(context.Context, string) (string, error) {
			return "EVIDENCE:\n- browser was unavailable", nil
		}}
	})
	result, err := NewVerifyWorkTool(sub).Execute(context.Background(), map[string]interface{}{"claim": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "VERDICT: PARTIAL") || !strings.Contains(result, "omitted the required verdict") {
		t.Fatalf("missing verdict was not normalized: %s", result)
	}
}

func TestVerifyWorkFailsClosedOnBrokenClaimedOfficePackage(t *testing.T) {
	workspace := t.TempDir()
	source := writeOPCTestPackage(t, minimalOPCTestParts("rId2"))
	target := filepath.Join(workspace, "deliverable.xlsx")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}

	called := false
	sub := newVerificationTestSubAgent(t, func(AgentConfig) Agent {
		return &verificationTestAgent{generate: func(context.Context, string) (string, error) {
			called = true
			return "EVIDENCE:\n- size is non-zero\nVERDICT: PASS", nil
		}}
	})
	sub.workspace = workspace
	result, err := NewVerifyWorkTool(sub).Execute(context.Background(), map[string]interface{}{
		"claim": "deliverable.xlsx is complete and valid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("deterministic broken-package evidence should fail before asking the verifier model")
	}
	if !strings.Contains(result, "VERDICT: FAIL") || !strings.Contains(result, "undeclared relationship") {
		t.Fatalf("broken Office package was not failed closed: %s", result)
	}
}

func TestVerifyWorkAllowsVerifierAfterClaimedOfficePackagePassesOPCCheck(t *testing.T) {
	workspace := t.TempDir()
	source := writeOPCTestPackage(t, minimalOPCTestParts("rId1"))
	target := filepath.Join(workspace, "deliverable.xlsx")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var prompt string
	sub := newVerificationTestSubAgent(t, func(AgentConfig) Agent {
		return &verificationTestAgent{generate: func(_ context.Context, input string) (string, error) {
			prompt = input
			return "EVIDENCE:\n- host OPC check passed\nVERDICT: PASS", nil
		}}
	})
	sub.workspace = workspace
	result, err := NewVerifyWorkTool(sub).Execute(context.Background(), map[string]interface{}{
		"claim": "deliverable.xlsx is complete and valid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "VERDICT: PASS") || !strings.Contains(prompt, "Host read-only Office package checks") {
		t.Fatalf("valid package check was not carried into verifier evidence: result=%q prompt=%q", result, prompt)
	}
}

func TestVerifyWorkFastTimeoutReturnsPartial(t *testing.T) {
	sub := newVerificationTestSubAgent(t, func(AgentConfig) Agent {
		return &verificationTestAgent{generate: func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
	})
	tool := NewVerifyWorkTool(sub)
	tool.timeout = 20 * time.Millisecond
	result, err := tool.Execute(context.Background(), map[string]interface{}{"claim": "done"})
	if err != nil {
		t.Fatalf("fast verification timeout should be advisory, got %v", err)
	}
	if !strings.Contains(result, "VERDICT: PARTIAL") {
		t.Fatalf("timeout result = %q", result)
	}
}
