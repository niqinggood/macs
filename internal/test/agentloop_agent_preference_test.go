package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type preferenceTestAgent struct {
	name string
}

func (a *preferenceTestAgent) Name() string { return a.name }

func (a *preferenceTestAgent) Generate(context.Context, string) (string, error) {
	return "ok", nil
}

func (a *preferenceTestAgent) GetType() AgentType { return AgentTypeCustom }

func TestDefaultAgentPrefersAssistantOverConfigOrder(t *testing.T) {
	researcher := &preferenceTestAgent{name: "Researcher"}
	assistant := &preferenceTestAgent{name: "Assistant"}
	engine := &MultiAgentEngine{
		config: &Config{Agents: []AgentConfig{
			{Name: "Researcher", Model: "research_model"},
			{Name: "Assistant", Model: "assist_model"},
		}},
		agentFactory: &AgentFactory{agents: map[string]Agent{
			"Researcher": researcher,
			"Assistant":  assistant,
		}},
	}

	name, agent, ok := engine.DefaultAgent()
	if !ok || name != "Assistant" || agent != assistant {
		t.Fatalf("default agent = (%q, %T, %v), want Assistant", name, agent, ok)
	}
}

func TestExplicitDefaultAgentOverridesAssistantFallback(t *testing.T) {
	researcher := &preferenceTestAgent{name: "Researcher"}
	assistant := &preferenceTestAgent{name: "Assistant"}
	engine := &MultiAgentEngine{
		config: &Config{
			DefaultAgent: "Researcher",
			Agents: []AgentConfig{
				{Name: "Assistant", Model: "assist_model"},
				{Name: "Researcher", Model: "research_model"},
			},
		},
		agentFactory: &AgentFactory{agents: map[string]Agent{
			"Researcher": researcher,
			"Assistant":  assistant,
		}},
	}

	name, agent, ok := engine.DefaultAgent()
	if !ok || name != "Researcher" || agent != researcher {
		t.Fatalf("default agent = (%q, %T, %v), want explicit Researcher", name, agent, ok)
	}
}

func TestAgentNamesPutDefaultAssistantFirst(t *testing.T) {
	engine := &MultiAgentEngine{
		config: &Config{
			DefaultAgent: "Assistant",
			Agents: []AgentConfig{
				{Name: "Researcher"},
				{Name: "Developer"},
				{Name: "Assistant"},
			},
		},
		agentFactory: &AgentFactory{agents: map[string]Agent{
			"Researcher": &preferenceTestAgent{name: "Researcher"},
			"Developer":  &preferenceTestAgent{name: "Developer"},
			"Assistant":  &preferenceTestAgent{name: "Assistant"},
		}},
	}

	got := engine.GetAgentNames()
	want := []string{"Assistant", "Developer", "Researcher"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent order = %#v, want %#v", got, want)
	}
}

func TestAgentFactoryInheritsDefaultAssistantModel(t *testing.T) {
	config := &Config{
		Workspace:    t.TempDir(),
		DefaultAgent: "Assistant",
		Agents: []AgentConfig{
			{Name: "Researcher", Model: "research_model"},
			{Name: "Assistant", Model: "assist_model"},
		},
	}
	factory := NewAgentFactory(
		map[string]ModelConfig{
			"research_model": {Type: "ollama", ModelName: "research-model", BaseURL: "http://127.0.0.1:11434/v1"},
			"assist_model":   {Type: "ollama", ModelName: "assistant-model", BaseURL: "http://127.0.0.1:11434/v1"},
		},
		nil,
		config,
		log.New(io.Discard, "", 0),
	)

	if factory.preferredModel != "assist_model" {
		t.Fatalf("preferred sub-agent model = %q, want assist_model", factory.preferredModel)
	}
	resolved := factory.resolveSubAgentModelConfig("")
	if resolved == nil || resolved.ModelName != "assistant-model" {
		t.Fatalf("resolved sub-agent model = %#v, want Assistant's bound model", resolved)
	}
}

func TestListAgentsAPIReturnsAssistantFirst(t *testing.T) {
	engine := &MultiAgentEngine{
		config: &Config{
			DefaultAgent: "Assistant",
			Agents: []AgentConfig{
				{Name: "Researcher"},
				{Name: "Assistant"},
			},
		},
		agentFactory: &AgentFactory{agents: map[string]Agent{
			"Researcher": &preferenceTestAgent{name: "Researcher"},
			"Assistant":  &preferenceTestAgent{name: "Assistant"},
		}},
	}
	gateway := &GatewayService{engine: engine}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)

	gateway.handleListAgents(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response struct {
		DefaultAgent string `json:"default_agent"`
		Agents       []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode /api/agents response: %v", err)
	}
	if response.DefaultAgent != "Assistant" {
		t.Fatalf("default_agent = %q, want Assistant", response.DefaultAgent)
	}
	if len(response.Agents) != 2 || response.Agents[0].Name != "Assistant" {
		t.Fatalf("Agent API order = %#v, want Assistant first", response.Agents)
	}
}

func TestDefaultAgentSettingsMergeTriggersEngineReload(t *testing.T) {
	config := &Config{DefaultAgent: "Researcher"}
	if err := mergeFullConfig(config, map[string]interface{}{"default_agent": " Assistant "}); err != nil {
		t.Fatalf("merge default_agent: %v", err)
	}
	if config.DefaultAgent != "Assistant" {
		t.Fatalf("merged default_agent = %q, want trimmed Assistant", config.DefaultAgent)
	}
	oldConfig := &Config{DefaultAgent: "Researcher"}
	if !(&GatewayService{}).needUpdateEngine(oldConfig, config) {
		t.Fatal("Gateway hot reload must rebuild the engine when default_agent changes")
	}
	if !(&ClawBot{}).needUpdateEngine(oldConfig, config) {
		t.Fatal("CLI hot reload must rebuild the engine when default_agent changes")
	}
}
