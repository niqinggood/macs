package app

import (
	"path/filepath"
	"strings"
	"testing"
)

var unscopedCollaborationToolNamesForTest = []string{
	"assemble_team",
	"sub_agent",
	"parallel_sub_agent",
	"swarm",
	"agent_status",
	"agent_stop",
	"verify_work",
}

func TestAgentOSTaskScopedAgentDoesNotRegainCollaborationTools(t *testing.T) {
	gateway := newAgentOSRoleRevisionGateway(t)
	gateway.config.ToolsConfig.Enabled = true
	gateway.config.Agents[0].EnabledTools = []string{"read_file"}
	gateway.config.Agents[0].DisallowedTools = nil

	revision, snapshot, err := gateway.captureAgentOSRoleRevision(
		"customer-research",
		"CustomerResearchAgent",
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvedWorkspace := t.TempDir()
	task := &GatewayTask{
		ID:           "task-personal-workspace",
		QueueName:    agentOSQueueName,
		RoleID:       "customer-research",
		RoleRevision: revision,
		RoleSnapshot: snapshot,
	}
	_, agent, found, cleanup, err := gateway.resolveChatAgentForGatewayTask(
		task,
		"CustomerResearchAgent",
		resolvedWorkspace,
	)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("captured AgentOS role was not resolved")
	}

	llmAgent, ok := agent.(*LLMAgent)
	if !ok {
		t.Fatalf("resolved agent type = %T, want *LLMAgent", agent)
	}
	if !llmAgent.taskScopedRuntime {
		t.Fatal("AgentOS per-task agent was not marked task-scoped")
	}
	wantWorkspace := normalizeProjectWorkspace(resolvedWorkspace)
	if llmAgent.workspace != wantWorkspace {
		t.Fatalf("agent workspace = %q, want %q", llmAgent.workspace, wantWorkspace)
	}
	if _, ok := llmAgent.toolRegistry.Get("read_file"); !ok {
		t.Fatal("role-authorized read_file tool was removed")
	}
	for _, name := range unscopedCollaborationToolNamesForTest {
		if _, ok := llmAgent.toolRegistry.Get(name); ok {
			t.Fatalf("task-scoped AgentOS role regained unscoped collaboration tool %q", name)
		}
	}
	if got := strings.Join(llmAgent.toolRegistry.ListNames(), ","); got != "read_file" {
		t.Fatalf("task-scoped role registry escaped its read_file-only contract: %s", got)
	}
}

func TestOrdinaryLocalAgentStillReceivesCollaborationRuntime(t *testing.T) {
	gateway := newAgentOSRoleRevisionGateway(t)
	gateway.config.Agents[0].EnabledTools = nil
	gateway.config.Agents[0].DisallowedTools = nil
	resolvedWorkspace := t.TempDir()

	_, agent, found, err := gateway.engine.NewAgentForTaskInWorkspace(
		"CustomerResearchAgent",
		resolvedWorkspace,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("ordinary configured agent was not resolved")
	}

	llmAgent, ok := agent.(*LLMAgent)
	if !ok {
		t.Fatalf("resolved agent type = %T, want *LLMAgent", agent)
	}
	if llmAgent.ptcServer != nil {
		defer llmAgent.ptcServer.Stop()
	}
	if llmAgent.taskScopedRuntime {
		t.Fatal("ordinary local agent was marked task-scoped")
	}
	if llmAgent.workspace != normalizeProjectWorkspace(resolvedWorkspace) {
		t.Fatalf("ordinary agent workspace = %q, want %q", llmAgent.workspace, normalizeProjectWorkspace(resolvedWorkspace))
	}
	for _, name := range unscopedCollaborationToolNamesForTest {
		if _, ok := llmAgent.toolRegistry.Get(name); !ok {
			t.Fatalf("ordinary local agent lost collaboration tool %q", name)
		}
	}
}

func TestAgentOSRoleRevisionIgnoresAbsoluteGlobalMemoryBaseDir(t *testing.T) {
	database := openAgentOSScenarioDB(t)
	previousDB := db
	db = database
	t.Cleanup(func() { db = previousDB })

	gateway := newAgentOSRoleRevisionGateway(t)
	globalMemoryDir := filepath.Join(t.TempDir(), "shared-memory")
	gateway.config.MemoryConfig.Enabled = true
	gateway.config.MemoryConfig.BaseDir = globalMemoryDir
	gateway.config.Runtime.RuntimeDir = "runtime-data"

	revision, snapshot, err := gateway.captureAgentOSRoleRevision(
		"customer-research",
		"CustomerResearchAgent",
	)
	if err != nil {
		t.Fatal(err)
	}

	personalWorkspace := t.TempDir()
	task := &GatewayTask{
		ID:           "task-personal-memory",
		QueueName:    agentOSQueueName,
		RoleID:       "customer-research",
		RoleRevision: revision,
		RoleSnapshot: snapshot,
	}
	_, agent, found, cleanup, err := gateway.resolveChatAgentForGatewayTask(
		task,
		"CustomerResearchAgent",
		personalWorkspace,
	)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("captured AgentOS role was not resolved")
	}

	llmAgent, ok := agent.(*LLMAgent)
	if !ok {
		t.Fatalf("resolved agent type = %T, want *LLMAgent", agent)
	}
	if llmAgent.memory == nil || llmAgent.sessionMemory == nil || llmAgent.scm == nil || llmAgent.fileHistory == nil {
		t.Fatalf(
			"expected file-backed state managers, memory=%v session=%v scm=%v history=%v",
			llmAgent.memory != nil,
			llmAgent.sessionMemory != nil,
			llmAgent.scm != nil,
			llmAgent.fileHistory != nil,
		)
	}
	if llmAgent.globalConfig == nil || llmAgent.globalConfig.MemoryConfig.BaseDir != "" {
		t.Fatalf("AgentOS runtime retained absolute memory base_dir: %#v", llmAgent.globalConfig)
	}
	if gateway.config.MemoryConfig.BaseDir != globalMemoryDir {
		t.Fatalf("host configuration was mutated: base_dir=%q want=%q", gateway.config.MemoryConfig.BaseDir, globalMemoryDir)
	}

	llmAgent.sessionMemory.SetSession("person-42/session-1")
	if err := llmAgent.saveColdMessages(
		"person-42-session-1-t1",
		[]map[string]interface{}{{"role": "user", "content": "private canary"}},
	); err != nil {
		t.Fatalf("initialize personal cold-message path: %v", err)
	}

	personalPaths := map[string]string{
		"memory base":          llmAgent.memory.baseDir,
		"memory file":          llmAgent.memory.memoryFile,
		"memory logs":          llmAgent.memory.logsDir,
		"cold messages":        llmAgent.coldMessagesPath,
		"session memory":       llmAgent.sessionMemory.MemoryPath(),
		"session context":      llmAgent.scm.StoreDir(),
		"file history backups": llmAgent.fileHistory.BackupDir(),
	}
	for label, path := range personalPaths {
		if !testPathWithin(path, personalWorkspace) {
			t.Errorf("%s path escaped personal workspace: %q", label, path)
		}
		if testPathWithin(path, globalMemoryDir) {
			t.Errorf("%s path still uses absolute global memory base: %q", label, path)
		}
	}
}

func testPathWithin(path, root string) bool {
	path = normalizeProjectWorkspace(path)
	root = normalizeProjectWorkspace(root)
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
