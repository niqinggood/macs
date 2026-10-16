package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestProfessionalEmployeeRejectsNonMentionDispatchPaths(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	gateway := &GatewayService{logger: logger}
	gateway.pipelineExec = NewPipelineExecutor(nil, gateway, logger)
	gateway.pipelineExec.RegisterDynamic("mention-only-professional", Pipeline{
		Name: "mention-only-professional",
		Steps: []PipelineStep{{
			Name: "isolated",
			ProfessionalAgent: &ProfessionalAgentStep{
				ID:         "quant-trader",
				Capability: "trading",
			},
		}},
	})
	mgr := NewAIEmployeeManager(gateway, logger)
	gateway.aeManager = mgr
	mgr.Register([]AIEmployeeConfig{{
		ID:       "quant-trader",
		Name:     "AI交易员",
		AtName:   "交易员",
		Role:     "paper trading research",
		Pipeline: "mention-only-professional",
	}})

	if _, err := mgr.Dispatch(context.Background(), "quant-trader", "分析新安股份", "webhook-session"); err == nil || !strings.Contains(err.Error(), "explicit @交易员") {
		t.Fatalf("ordinary Dispatch must be rejected for professional employee, got %v", err)
	}
	if _, err := mgr.DispatchFromChannel(context.Background(), "quant-trader", "分析新安股份", "cron-session", "test", "recipient", "user"); err == nil {
		t.Fatal("ordinary channel dispatch must not bypass explicit-mention policy")
	}

	// Avoid invoking a real child process: a valid explicitly mentioned task is
	// accepted by the policy and reaches the normal busy-queue path.
	mgr.mu.Lock()
	mgr.busy["quant-trader"] = true
	mgr.mu.Unlock()
	record, err := mgr.DispatchExplicitMention(context.Background(), "quant-trader", "分析新安股份", "mention-session")
	if err != nil {
		t.Fatalf("explicit mention dispatch should pass activation policy: %v", err)
	}
	if record == nil || record.Status != "queued" {
		t.Fatalf("explicit mention dispatch = %#v, want queued record", record)
	}
}
