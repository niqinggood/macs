package app

import (
	"context"
	"fmt"
	"testing"
)

type isolatedSubAgentStub struct{ id int }

func (a *isolatedSubAgentStub) Name() string       { return fmt.Sprintf("sub-%d", a.id) }
func (a *isolatedSubAgentStub) GetType() AgentType { return AgentTypeCustom }
func (a *isolatedSubAgentStub) Generate(context.Context, string) (string, error) {
	return "ok", nil
}

func TestSubAgentFactoryCreatesFreshRunInstances(t *testing.T) {
	created := 0
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(AgentConfig, string) (Agent, error) {
			created++
			return &isolatedSubAgentStub{id: created}, nil
		},
	}
	config := AgentConfig{Name: "same-step"}
	first, err := factory.GetOrCreateSubAgent(config, "first run")
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory.GetOrCreateSubAgent(config, "second run")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || created != 2 {
		t.Fatalf("mutable sub-agent was reused: first=%p second=%p created=%d", first, second, created)
	}
}
