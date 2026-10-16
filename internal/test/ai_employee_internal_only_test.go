package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func newInternalOnlyTestManager() *AIEmployeeManager {
	manager := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	manager.Register([]AIEmployeeConfig{
		{
			ID:              "public-worker",
			Name:            "Public Worker",
			AtName:          "public",
			Role:            "public customer support",
			Pipeline:        "public-pipeline",
			RoutingExamples: []string{"help a public customer"},
		},
		{
			ID:              "internal-worker",
			Name:            "Internal Worker Secret Name",
			AtName:          "internal-secret",
			Role:            "internal secret capability",
			Pipeline:        "internal-pipeline",
			MentionAliases:  []string{"secret-alias"},
			RoutingExamples: []string{"run the secret internal workflow"},
			InternalOnly:    true,
		},
	})
	return manager
}

func TestAIEmployeeConfigInternalOnlyJSONAndYAML(t *testing.T) {
	var fromJSON AIEmployeeConfig
	if err := json.Unmarshal([]byte(`{"id":"internal","internal_only":true}`), &fromJSON); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if !fromJSON.InternalOnly {
		t.Fatal("json internal_only was not decoded")
	}

	var fromYAML AIEmployeeConfig
	if err := yaml.Unmarshal([]byte("id: internal\ninternal_only: true\n"), &fromYAML); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if !fromYAML.InternalOnly {
		t.Fatal("yaml internal_only was not decoded")
	}
}

func TestInternalOnlyEmployeeRemainsRegisteredWithoutMentionAliases(t *testing.T) {
	manager := newInternalOnlyTestManager()

	cfg, ok := manager.GetEmployee("internal-worker")
	if !ok || cfg == nil || !cfg.InternalOnly {
		t.Fatalf("GetEmployee(internal-worker) = %#v, %v", cfg, ok)
	}
	if len(manager.ListEmployees()) != 2 {
		t.Fatalf("registered employees = %d, want internal employee retained", len(manager.ListEmployees()))
	}
	if examples := cfg.GetRoutingExamples(); len(examples) != 0 {
		t.Fatalf("internal routing examples leaked: %#v", examples)
	}
	if aliases := employeeMentionAliases(cfg); len(aliases) != 0 {
		t.Fatalf("internal mention aliases registered: %#v", aliases)
	}

	for _, message := range []string{
		"@internal-secret do work",
		"@Internal Worker Secret Name do work",
		"@internal-worker do work",
		"@secret-alias do work",
	} {
		if matched, _ := manager.MatchAtName(message); matched != nil {
			t.Fatalf("MatchAtName(%q) exposed internal employee: %#v", message, matched)
		}
	}
}

func TestReRegisteringEmployeeAsInternalOnlyRemovesStaleAliases(t *testing.T) {
	manager := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	manager.Register([]AIEmployeeConfig{{
		ID: "worker", Name: "Worker", AtName: "worker", MentionAliases: []string{"old-worker"},
	}})
	if matched, _ := manager.MatchAtName("@old-worker task"); matched == nil {
		t.Fatal("public employee alias did not register")
	}

	manager.Register([]AIEmployeeConfig{{
		ID: "worker", Name: "Worker", AtName: "worker", MentionAliases: []string{"old-worker"}, InternalOnly: true,
	}})
	for _, message := range []string{"@worker task", "@old-worker task"} {
		if matched, _ := manager.MatchAtName(message); matched != nil {
			t.Fatalf("stale alias still matched after internal_only transition: %#v", matched)
		}
	}
}

func TestInternalOnlyEmployeeRejectsOrdinaryDispatchButRetainsSealedDiagnosticDispatch(t *testing.T) {
	manager := newInternalOnlyTestManager()

	if _, err := manager.Dispatch(context.Background(), "internal-worker", "task", "session"); err == nil || !strings.Contains(err.Error(), "internal-only") {
		t.Fatalf("Dispatch error = %v, want internal-only denial", err)
	}
	if _, err := manager.DispatchExplicitMention(context.Background(), "internal-worker", "task", "session"); err == nil || strings.Contains(err.Error(), "internal-only") {
		t.Fatalf("DispatchExplicitMention error = %v, want sealed diagnostic dispatch to pass the internal-only gate", err)
	}
}

func TestSupervisorExcludesInternalOnlyEmployeesFromEveryRoutingSurface(t *testing.T) {
	manager := newInternalOnlyTestManager()
	router := NewSupervisorRouter(SupervisorConfig{
		Identity:             "assistant",
		AutoRouteAIEmployees: true,
	}, manager, nil, log.New(io.Discard, "", 0))

	if result := router.keywordMatch("Internal Worker Secret Name internal secret capability"); result != nil {
		t.Fatalf("keywordMatch routed to internal employee: %#v", result)
	}
	if result := router.Route(context.Background(), "@internal-secret do work"); result.TargetAE != "" || result.Mode == ModeAE {
		t.Fatalf("explicit internal mention routed: %#v", result)
	}

	classificationPrompt := router.buildClassificationPrompt("route this")
	for _, secret := range []string{"internal-worker", "Internal Worker Secret Name", "internal secret capability"} {
		if strings.Contains(classificationPrompt, secret) {
			t.Fatalf("classification prompt disclosed %q: %s", secret, classificationPrompt)
		}
	}
	if !strings.Contains(classificationPrompt, "public-worker") {
		t.Fatalf("classification prompt omitted public employee: %s", classificationPrompt)
	}

	fewShots := router.generateFewShots()
	if strings.Contains(fewShots, "secret internal workflow") || strings.Contains(fewShots, "internal-worker") {
		t.Fatalf("few-shot prompt disclosed internal employee: %s", fewShots)
	}
	if !strings.Contains(fewShots, "public-worker") {
		t.Fatalf("few-shot prompt omitted public employee: %s", fewShots)
	}

	clarification := router.buildClarification("unclear")
	if strings.Contains(clarification, "Internal Worker Secret Name") || strings.Contains(clarification, "internal secret capability") {
		t.Fatalf("clarification disclosed internal employee: %s", clarification)
	}
	if !strings.Contains(clarification, "Public Worker") {
		t.Fatalf("clarification omitted public employee: %s", clarification)
	}

	if result := router.parseLLMResult("task", `{"target_de":"internal-worker","confidence":0.99}`); result != nil {
		t.Fatalf("LLM result selected internal employee: %#v", result)
	}
}

func TestEmployeeListingToolsNeverDiscloseInternalOnlyEmployees(t *testing.T) {
	manager := newInternalOnlyTestManager()
	previous := globalAEManager
	globalAEManager = manager
	t.Cleanup(func() { globalAEManager = previous })

	listOutput, err := NewListEmployeesTool().Execute(context.Background(), map[string]interface{}{"include_hidden": true})
	if err != nil {
		t.Fatalf("list_employees: %v", err)
	}
	if strings.Contains(listOutput, "Internal Worker Secret Name") || strings.Contains(listOutput, "internal secret capability") {
		t.Fatalf("list_employees disclosed internal employee: %s", listOutput)
	}
	if !strings.Contains(listOutput, "Public Worker") {
		t.Fatalf("list_employees omitted public employee: %s", listOutput)
	}

	colleagueOutput, err := NewDEInfoTool(manager).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("list_colleagues: %v", err)
	}
	if strings.Contains(colleagueOutput, "internal-worker") || strings.Contains(colleagueOutput, "Internal Worker Secret Name") {
		t.Fatalf("list_colleagues disclosed internal employee: %s", colleagueOutput)
	}
	if !strings.Contains(colleagueOutput, "public-worker") {
		t.Fatalf("list_colleagues omitted public employee: %s", colleagueOutput)
	}
}

func TestDelegateToRejectsInternalOnlyTarget(t *testing.T) {
	manager := newInternalOnlyTestManager()
	tool := NewAECollaborationTool(manager, log.New(io.Discard, "", 0))
	ctx := withEmployeeExecutionGrant(context.Background(), "public-worker", "default")

	output, err := tool.Execute(ctx, map[string]interface{}{
		"ae_id": "internal-worker",
		"task":  "run internal work",
	})
	if err != nil {
		t.Fatalf("delegate_to returned protocol error: %v", err)
	}
	if strings.Contains(output, "Internal Worker Secret Name") || strings.Contains(output, "internal secret capability") {
		t.Fatalf("delegate_to disclosed or dispatched internal employee: %s", output)
	}
	if !strings.Contains(output, "public-worker") {
		t.Fatalf("delegate_to did not return safe public alternatives: %s", output)
	}
}
