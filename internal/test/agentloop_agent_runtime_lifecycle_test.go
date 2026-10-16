package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

type lifecycleProbeAgent struct {
	closeCount int
}

func (a *lifecycleProbeAgent) Name() string { return "lifecycle-probe" }

func (a *lifecycleProbeAgent) Generate(context.Context, string) (string, error) {
	return "", nil
}

func (a *lifecycleProbeAgent) GetType() AgentType { return AgentTypeCustom }

func (a *lifecycleProbeAgent) Close() { a.closeCount++ }

func runLifecycleCallsConcurrently(count int, call func()) {
	var wait sync.WaitGroup
	wait.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wait.Done()
			call()
		}()
	}
	wait.Wait()
}

func TestProactiveEngineStopIsIdempotentAndInterruptsInitialDelay(t *testing.T) {
	agent := &LLMAgent{name: "proactive-lifecycle-test"}
	engine := NewProactiveEngine(agent, ProactiveConfig{
		Enabled:       true,
		CheckInterval: time.Hour,
	})
	if engine == nil {
		t.Fatal("NewProactiveEngine returned nil")
	}

	startedAt := time.Now()
	runLifecycleCallsConcurrently(8, engine.Start)
	runLifecycleCallsConcurrently(8, engine.Stop)
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("Stop waited for the 30-second initial delay: %v", elapsed)
	}

	select {
	case <-engine.done:
	default:
		t.Fatal("proactive worker still holds the agent after Stop returned")
	}
}

func TestLLMAgentCloseStopsOwnedBackgroundServices(t *testing.T) {
	registry := NewToolRegistry()
	ptc := NewPTCServer(registry)
	if err := ptc.Start(); err != nil {
		t.Fatalf("start PTC server: %v", err)
	}

	agent := &LLMAgent{name: "agent-lifecycle-test", ptcServer: ptc}
	proactive := NewProactiveEngine(agent, ProactiveConfig{
		Enabled:       true,
		CheckInterval: time.Hour,
	})
	agent.proactiveEngine = proactive
	proactive.Start()

	runLifecycleCallsConcurrently(8, agent.Close)

	select {
	case <-proactive.done:
	default:
		t.Fatal("LLMAgent.Close did not stop its proactive worker")
	}
	ptc.mu.Lock()
	ptcRunning := ptc.running
	ptc.mu.Unlock()
	if ptcRunning {
		t.Fatal("LLMAgent.Close did not stop its PTC server")
	}
}

func TestNewAgentRuntimeCleanupIsIdempotent(t *testing.T) {
	agent := &lifecycleProbeAgent{}
	cleanup := newAgentRuntimeCleanup(agent)
	runLifecycleCallsConcurrently(8, cleanup)
	if agent.closeCount != 1 {
		t.Fatalf("agent close count = %d, want 1", agent.closeCount)
	}
}
