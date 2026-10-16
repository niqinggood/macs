package app

import (
	"io"
	"log"
	"strings"
	"testing"
)

func newTeamRuntimeTestEngine(t *testing.T) (*MultiAgentEngine, *Config, *log.Logger) {
	t.Helper()
	workspace := t.TempDir()
	cfg := &Config{Workspace: workspace}
	logger := log.New(io.Discard, "", 0)
	factory := &AgentFactory{
		workspace:    workspace,
		globalConfig: cfg,
		fileLogger:   logger,
		agents:       make(map[string]Agent),
		subAgents:    make(map[string]Agent),
	}
	return &MultiAgentEngine{agentFactory: factory, config: cfg, fileLogger: logger}, cfg, logger
}

func TestRegisterRuntimeToolsForAgentAddsTeamDependencies(t *testing.T) {
	engine, _, logger := newTeamRuntimeTestEngine(t)
	agent := &LLMAgent{toolRegistry: NewToolRegistry()}
	engine.registerRuntimeToolsForAgentWithWorkspace(agent, engine.agentFactory.workspace, logger)
	for _, name := range []string{"sub_agent", "parallel_sub_agent", "swarm", "agent_stop", "verify_work"} {
		if tool, ok := agent.toolRegistry.Get(name); !ok || tool == nil {
			t.Fatalf("runtime tool %q was not registered", name)
		}
	}
}

func TestAssembleTeamRepairsMissingRuntimeDependencies(t *testing.T) {
	engine, cfg, logger := newTeamRuntimeTestEngine(t)
	previous := globalEngine
	SetGlobalEngine(engine)
	t.Cleanup(func() { SetGlobalEngine(previous) })

	registry := NewToolRegistry()
	team := NewAssembleTeamTool(cfg.Workspace, logger, nil, registry, cfg)
	if _, ok := registry.Get("sub_agent"); ok {
		t.Fatal("test precondition failed: sub_agent already registered")
	}
	if err := team.ensureRuntimeTeamTools(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sub_agent", "parallel_sub_agent", "swarm", "agent_stop", "verify_work"} {
		if tool, ok := registry.Get(name); !ok || tool == nil {
			t.Fatalf("self-heal did not register %q", name)
		}
	}
}

func TestAssembleTeamReportsUnavailableRuntimeClearly(t *testing.T) {
	previous := globalEngine
	SetGlobalEngine(nil)
	t.Cleanup(func() { SetGlobalEngine(previous) })

	team := NewAssembleTeamTool(t.TempDir(), log.New(io.Discard, "", 0), nil, NewToolRegistry(), nil)
	if err := team.ensureRuntimeTeamTools(); err == nil {
		t.Fatal("missing multi-agent runtime should return a clear dependency error")
	}
}

func TestAssembleTeamRoleTaskKeepsResponseOnlyWorkInMemory(t *testing.T) {
	team := NewAssembleTeamTool(t.TempDir(), log.New(io.Discard, "", 0), nil, NewToolRegistry(), nil)
	prompt := team.buildRoleTask(TeamRole{Role: "研究员", SystemPrompt: "调查并总结事实"}, "研究 AI 大模型最新进展").String()
	for _, required := range []string{
		"将本角色的实质产出直接返回给团队编排器",
		"原始任务没有要求落盘时，不要自行发明文件名或目录",
		"不要因为任务包含“研究、分析、报告、撰稿”等词就创建中间文件或最终文件",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("role prompt is missing delivery boundary %q:\n%s", required, prompt)
		}
	}
}
