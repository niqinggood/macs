package app

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
)

type scopedCollaborationTestAgent struct {
	name   string
	output string
	mu     sync.Mutex
	inputs []string
}

func (agent *scopedCollaborationTestAgent) Name() string { return agent.name }
func (agent *scopedCollaborationTestAgent) GetType() AgentType {
	return AgentTypeCustom
}
func (agent *scopedCollaborationTestAgent) Generate(_ context.Context, input string) (string, error) {
	agent.mu.Lock()
	agent.inputs = append(agent.inputs, input)
	agent.mu.Unlock()
	return agent.output, nil
}

func newScopedCollaborationValidationGateway(agents ...Agent) *GatewayService {
	agentMap := make(map[string]Agent, len(agents))
	for _, agent := range agents {
		agentMap[agent.Name()] = agent
	}
	logger := log.New(io.Discard, "", 0)
	config := &Config{}
	return &GatewayService{
		config: config,
		engine: &MultiAgentEngine{
			config: config,
			agentFactory: &AgentFactory{
				agents:       agentMap,
				subAgents:    make(map[string]Agent),
				globalConfig: config,
				fileLogger:   logger,
			},
			logger: logger, fileLogger: logger,
		},
		logger: logger,
		tasks:  make(map[string]*GatewayTask),
	}
}

func TestNormalizeAgentOSCollaborationAuthorizesAndNormalizesMembers(t *testing.T) {
	gateway := newScopedCollaborationValidationGateway(
		&scopedCollaborationTestAgent{name: "Researcher"},
		&scopedCollaborationTestAgent{name: "Reviewer"},
		&scopedCollaborationTestAgent{name: "Host"},
	)
	config, err := gateway.normalizeAgentOSCollaboration(
		CollaborationConfig{
			Type: GroupChatWithHost,
			Tasks: []TaskConfig{
				{TaskID: "research", Agent: "Researcher", Alias: "  市场研究  "},
				{TaskID: "review", Agent: "Reviewer"},
				{TaskID: "host", Agent: "Host"},
			},
			MaxRound:        2,
			FailureStrategy: Continue,
			HostAgent:       "Host",
		},
		"nova",
		ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("normalize collaboration: %v", err)
	}
	if config.Type != GroupChatWithHost || config.HostAgent != "Host" ||
		len(config.Tasks) != 3 {
		t.Fatalf("unexpected normalized config: %#v", config)
	}
	if config.Tasks[0].Agent != "Researcher" || config.Tasks[0].Alias != "市场研究" {
		t.Fatalf("alias must be trimmed without changing the routed role: %#v", config.Tasks[0])
	}
}

func TestNormalizeAgentOSCollaborationRejectsUnsafeOrUnavailableMembers(t *testing.T) {
	gateway := newScopedCollaborationValidationGateway(
		&scopedCollaborationTestAgent{name: "Researcher"},
		&scopedCollaborationTestAgent{name: "Reviewer"},
	)
	for name, config := range map[string]CollaborationConfig{
		"unsupported mode": {
			Type: "invented",
			Tasks: []TaskConfig{
				{TaskID: "one", Agent: "Researcher"},
				{TaskID: "two", Agent: "Reviewer"},
			},
		},
		"too few members": {
			Type:  Parallel,
			Tasks: []TaskConfig{{TaskID: "one", Agent: "Researcher"}},
		},
		"unavailable member": {
			Type: Parallel,
			Tasks: []TaskConfig{
				{TaskID: "one", Agent: "Researcher"},
				{TaskID: "two", Agent: "Missing"},
			},
		},
		"host outside team": {
			Type: GroupChatWithHost,
			Tasks: []TaskConfig{
				{TaskID: "one", Agent: "Researcher"},
				{TaskID: "two", Agent: "Reviewer"},
			},
			HostAgent: "Missing",
		},
		"invalid alias": {
			Type: Parallel,
			Tasks: []TaskConfig{
				{TaskID: "one", Agent: "Researcher", Alias: "市场\n研究"},
				{TaskID: "two", Agent: "Reviewer"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := gateway.normalizeAgentOSCollaboration(
				config,
				"nova",
				ExecutionIdentity{},
			); err == nil {
				t.Fatal("invalid collaboration was accepted")
			}
		})
	}
}

func TestScopedParallelCollaborationProducesUserFacingSynthesis(t *testing.T) {
	researcher := &scopedCollaborationTestAgent{
		name: "Researcher", output: "研究结论：需求真实，但证据仍需核验。",
	}
	reviewer := &scopedCollaborationTestAgent{
		name: "Reviewer", output: "风险结论：实施成本和数据质量是主要风险。",
	}
	gateway := newScopedCollaborationValidationGateway(researcher, reviewer)
	gateway.initSSE()
	request := &MessageRequest{
		TaskID:  "task_multi_agent",
		Message: "评审这个方案",
		Collaboration: &CollaborationConfig{
			Type: Parallel,
			Tasks: []TaskConfig{
				{TaskID: "research", Agent: "Researcher", Alias: "市场研究"},
				{TaskID: "review", Agent: "Reviewer", Alias: "风险审阅"},
			},
			FailureStrategy: Continue,
		},
	}
	participants := []scopedCollaborationParticipant{
		{task: request.Collaboration.Tasks[0], agent: researcher},
		{task: request.Collaboration.Tasks[1], agent: reviewer},
	}
	result, err := gateway.runScopedCollaboration(
		context.Background(),
		request,
		participants,
	)
	if err != nil {
		t.Fatalf("run collaboration: %v", err)
	}
	content := strings.TrimSpace(result["content"].(string))
	if content == "" {
		t.Fatal("collaboration did not produce a user-facing answer")
	}
	if !strings.Contains(content, "协作分析明细") ||
		!strings.Contains(content, "市场研究") ||
		!strings.Contains(content, "风险审阅") ||
		!strings.Contains(content, reviewer.output) {
		t.Fatalf("collaboration delivery dropped member contributions: %q", content)
	}
	if strings.Contains(content, "### Researcher") || strings.Contains(content, "### Reviewer") {
		t.Fatalf("user-facing headings should prefer member aliases: %q", content)
	}
	if len(researcher.inputs) == 0 ||
		!strings.Contains(researcher.inputs[0], "市场研究（执行角色：Researcher）") {
		t.Fatalf("alias must preserve the underlying role in the member prompt: %#v", researcher.inputs)
	}
	if result["status"] != "success" {
		t.Fatalf("unexpected collaboration status: %#v", result)
	}
}

func TestComposeScopedCollaborationDeliveryKeepsRoundsWithinMessageLimit(t *testing.T) {
	longOutput := strings.Repeat("成员证据与分析。", 1200)
	results := []TaskResult{
		{AgentName: "Researcher", Status: Completed, Output: longOutput},
		{AgentName: "Developer", Status: Completed, Output: longOutput},
		{AgentName: "Researcher", Status: Completed, Output: longOutput},
		{AgentName: "Developer", Status: Completed, Output: longOutput},
	}
	content := composeScopedCollaborationDelivery(
		GroupChatWithHost,
		"主持人的综合结论。",
		results,
		2,
	)
	for _, expected := range []string{
		"综合结论",
		"协作分析明细",
		"### 第 1 轮",
		"#### Researcher",
		"#### Developer",
		"### 第 2 轮",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("delivery missing %q", expected)
		}
	}
	if strings.Count(content, "### 第 1 轮") != 1 ||
		strings.Count(content, "### 第 2 轮") != 1 {
		t.Fatalf("delivery repeated a round heading:\n%s", content)
	}
	if got := len([]rune(content)); got > 19000 {
		t.Fatalf("delivery runes = %d, want <= 19000", got)
	}
}
