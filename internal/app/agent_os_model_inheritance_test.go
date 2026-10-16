package app

import (
	"io"
	"log"
	"strings"
	"testing"
)

func agentOSInheritedModelGateway(t *testing.T, published bool) (*GatewayService, string) {
	t.Helper()
	config := &Config{
		Workspace: t.TempDir(), DefaultAgent: "Assistant", DefaultModel: "chosen",
		Agents: []AgentConfig{{Name: "Assistant", Type: AgentTypeAutoGenAssistant, SystemMessage: "Summarize the requested work."}},
		ModelConfig: map[string]ModelConfig{
			"chosen": {Type: "openai", ModelName: "chosen-model", BaseURL: "https://model.example.test/v1", APIKey: "fixture-chosen-credential"},
			"other":  {Type: "openai", ModelName: "other-model", BaseURL: "https://model.example.test/v1", APIKey: "fixture-other-credential"},
		},
	}
	roleID := "Assistant"
	if published {
		roleID = "work-assistant"
		config.ServiceRoles = []AgentServiceRoleConfig{{ID: roleID, AgentName: "Assistant", Enabled: true}}
	}
	logger := log.New(io.Discard, "", 0)
	factory := NewAgentFactory(config.ModelConfig, nil, config, logger)
	if _, err := factory.GetOrCreateAgent(config.Agents[0]); err != nil {
		t.Fatal(err)
	}
	engine := &MultiAgentEngine{config: config, agentFactory: factory, logger: logger, fileLogger: logger}
	return &GatewayService{config: config, engine: engine, logger: logger}, roleID
}

func TestAgentOSRoleModelInheritanceDiscoveryAndRecovery(t *testing.T) {
	for _, published := range []bool{true, false} {
		name := "compatibility role"
		if published {
			name = "published role"
		}
		t.Run(name, func(t *testing.T) {
			gateway, roleID := agentOSInheritedModelGateway(t, published)
			identity := ExecutionIdentity{TenantID: "default", ServiceID: "test-client", AllowedRoles: []string{roleID}}
			roles, err := gateway.discoverableAgentOSRoles(identity)
			if err != nil || len(roles) != 1 || roles[0]["role_id"] != roleID {
				t.Fatalf("inherited role was not discoverable: roles=%v err=%v", roles, err)
			}
			revision, payload, err := gateway.captureAgentOSRoleRevision(roleID, "Assistant")
			if err != nil {
				t.Fatal(err)
			}
			task := &GatewayTask{RoleRevision: revision, RoleSnapshot: payload}
			snapshot, err := decodeAgentOSRoleRevision(task)
			if err != nil || snapshot.Agent.Model != "chosen" || snapshot.Model.Alias != "chosen" {
				t.Fatalf("inheritance was not frozen in both snapshots: snapshot=%#v err=%v", snapshot, err)
			}
			if gateway.config.Agents[0].Model != "" {
				t.Fatal("capturing a role changed the user's inheritance setting")
			}
			if strings.Contains(payload, "fixture-chosen-credential") || strings.Contains(payload, "api_key") {
				t.Fatal("role snapshot included a model credential")
			}
			stableRevision, stablePayload, err := gateway.captureAgentOSRoleRevision(roleID, "Assistant")
			if err != nil || stableRevision != revision || stablePayload != payload {
				t.Fatalf("unchanged inherited role revision was unstable: %v", err)
			}

			// New tasks follow the next choice, but an accepted task must retain
			// its original model behavior while reading the live rotated key.
			gateway.config.DefaultModel = "other"
			nextRevision, _, err := gateway.captureAgentOSRoleRevision(roleID, "Assistant")
			if err != nil || nextRevision == revision {
				t.Fatalf("a new default did not change the published revision: %v", err)
			}
			gateway.engine.agentFactory.modelConfigs["chosen"].APIKey = "fixture-rotated-credential"
			resolvedName, agent, found, cleanup, err := gateway.newAgentFromRoleRevision(snapshot, t.TempDir())
			if cleanup != nil {
				defer cleanup()
			}
			if err != nil || !found || resolvedName != "Assistant" {
				t.Fatalf("accepted role could not be restored: found=%v name=%q err=%v", found, resolvedName, err)
			}
			restored, ok := agent.(*AutoGenAssistant)
			if !ok || restored.modelConfig.ModelName != "chosen-model" || restored.modelConfig.APIKey != "fixture-rotated-credential" {
				t.Fatal("restored role changed model or failed to read the live credential")
			}
		})
	}
}

func TestAgentOSRoleModelInheritanceHonorsOverride(t *testing.T) {
	gateway, roleID := agentOSInheritedModelGateway(t, true)
	gateway.config.Agents[0].Model = "other"
	// An independent Agent model remains usable even if the current composer
	// selection is invalid. This must not silently rebind the Agent.
	gateway.config.DefaultModel = "missing"
	revision, payload, err := gateway.captureAgentOSRoleRevision(roleID, "Assistant")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := decodeAgentOSRoleRevision(&GatewayTask{RoleRevision: revision, RoleSnapshot: payload})
	if err != nil || snapshot.Agent.Model != "other" || snapshot.Model.Alias != "other" {
		t.Fatalf("Agent override was not used: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestAgentOSRoleModelInheritanceRejectsUnavailableSelection(t *testing.T) {
	for _, published := range []bool{true, false} {
		for _, state := range []string{"missing", "unavailable"} {
			name := "compatibility/" + state
			if published {
				name = "published/" + state
			}
			t.Run(name, func(t *testing.T) {
				gateway, roleID := agentOSInheritedModelGateway(t, published)
				if state == "missing" {
					gateway.config.DefaultModel = "missing"
				} else {
					model := gateway.config.ModelConfig["chosen"]
					model.APIKey = ""
					gateway.config.ModelConfig["chosen"] = model
				}
				if _, _, err := gateway.captureAgentOSRoleRevision(roleID, "Assistant"); err == nil || !strings.Contains(err.Error(), "usable model configuration") {
					t.Fatalf("unavailable selection produced an execution contract: %v", err)
				}
				if _, err := gateway.discoverableAgentOSRoles(ExecutionIdentity{AllowedRoles: []string{roleID}}); err == nil {
					t.Fatal("discovery advertised an inherited unavailable model")
				}
			})
		}
	}
}
