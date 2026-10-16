package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func enabledROS2TestConfig() ROS2ToolConfig {
	return ROS2ToolConfig{
		Enabled:          true,
		AllowPublish:     true,
		AllowServiceCall: true,
		AllowAction:      true,
		TimeoutMS:        5000,
		MaxOutputBytes:   8192,
		Command:          "ros2-test",
		DomainID:         17,
		TopicAllowlist:   []string{"/device/**"},
		ServiceAllowlist: []string{"/device/set_mode"},
		ActionAllowlist:  []string{"/device/run"},
	}
}

func TestROS2ToolBuildsSkillSuppliedInvocation(t *testing.T) {
	if !CoreTools["ros2"] {
		t.Fatal("ros2 must be exposed when its config gate registers it")
	}
	tool := NewROS2Tool(enabledROS2TestConfig())
	var (
		gotCommand string
		gotArgs    []string
		gotEnv     []string
		gotLimit   int
	)
	tool.SetRunner(func(_ context.Context, command string, args, environment []string, limit int) (string, bool, error) {
		gotCommand = command
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), environment...)
		gotLimit = limit
		return "publisher: beginning loop", false, nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":       "topic_publish",
		"name":         "/device/command",
		"message_type": "example_interfaces/msg/String",
		"payload":      map[string]interface{}{"data": "opaque-model-command"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wantArgs := []string{
		"topic", "pub", "--once", "/device/command",
		"example_interfaces/msg/String", `{"data":"opaque-model-command"}`,
	}
	if gotCommand != "ros2-test" || !reflect.DeepEqual(gotArgs, wantArgs) || gotLimit != 8192 {
		t.Fatalf("unexpected ROS2 invocation: command=%q args=%q limit=%d", gotCommand, gotArgs, gotLimit)
	}
	foundDomain := false
	for _, entry := range gotEnv {
		if entry == "ROS_DOMAIN_ID=17" {
			foundDomain = true
			break
		}
	}
	if !foundDomain {
		t.Fatalf("ROS_DOMAIN_ID missing from environment: %v", gotEnv)
	}
	if !strings.Contains(result, `"success": true`) || !strings.Contains(result, `"name": "/device/command"`) {
		t.Fatalf("unexpected ROS2 result: %s", result)
	}

	schema, _ := json.Marshal(tool.Parameters())
	for _, forbidden := range []string{"robot_move", "turn_left", "turn_right", "register_address", "shell_command"} {
		if strings.Contains(strings.ToLower(string(schema)), forbidden) {
			t.Fatalf("ROS2 schema contains device semantics %q: %s", forbidden, schema)
		}
	}
}

func TestROS2ToolEnforcesWriteGatesAndAllowlists(t *testing.T) {
	config := enabledROS2TestConfig()
	config.AllowPublish = false
	tool := NewROS2Tool(config)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":       "topic_publish",
		"name":         "/device/command",
		"message_type": "example_interfaces/msg/String",
		"payload":      map[string]interface{}{"data": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "allow_publish=true") {
		t.Fatalf("publish gate error = %v", err)
	}

	config.AllowPublish = true
	tool = NewROS2Tool(config)
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action":       "topic_publish",
		"name":         "/other/command",
		"message_type": "example_interfaces/msg/String",
		"payload":      map[string]interface{}{"data": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "topic_allowlist") {
		t.Fatalf("topic allowlist error = %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action":       "service_call",
		"name":         "/device/not_allowed",
		"message_type": "example_interfaces/srv/SetBool",
		"payload":      map[string]interface{}{"data": true},
	})
	if err == nil || !strings.Contains(err.Error(), "service_allowlist") {
		t.Fatalf("service allowlist error = %v", err)
	}
}

func TestROS2ToolGateHotReloadsAgentRegistry(t *testing.T) {
	registry := NewToolRegistry()
	agent := &LLMAgent{name: "Assistant", toolRegistry: registry}
	engine := &MultiAgentEngine{agentFactory: &AgentFactory{
		agents: map[string]Agent{"Assistant": agent},
	}}
	config := &Config{Agents: []AgentConfig{{Name: "Assistant"}}}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.ROS2 = enabledROS2TestConfig()

	engine.syncROS2ToolGate(config)
	if _, exists := registry.Get("ros2"); !exists {
		t.Fatal("ros2 was not registered after enabling it")
	}

	changed := *config
	changed.ToolsConfig = config.ToolsConfig
	changed.ToolsConfig.ROS2 = config.ToolsConfig.ROS2
	changed.ToolsConfig.ROS2.DomainID = 18
	if !ros2ToolGateChanged(config, &changed) {
		t.Fatal("ROS2 runtime config change was not detected")
	}
	if !(&GatewayService{}).needUpdateEngine(config, &changed) {
		t.Fatal("gateway reload path did not detect ROS2 config change")
	}
	if !(&ClawBot{}).needUpdateEngine(config, &changed) {
		t.Fatal("process config watcher did not detect ROS2 config change")
	}

	config.ToolsConfig.ROS2.Enabled = false
	engine.syncROS2ToolGate(config)
	if _, exists := registry.Get("ros2"); exists {
		t.Fatal("ros2 remained registered after disabling it")
	}
}

func TestMergeToolsConfigParsesROS2Permissions(t *testing.T) {
	config := &Config{}
	config.ToolsConfig.Enabled = true
	mergeToolsConfig(config, map[string]interface{}{
		"tools_config": map[string]interface{}{
			"ros2": map[string]interface{}{
				"enabled":            true,
				"allow_publish":      true,
				"allow_service_call": true,
				"allow_action":       true,
				"timeout_ms":         float64(45000),
				"max_output_bytes":   float64(32768),
				"command":            "/opt/ros/bin/ros2",
				"domain_id":          float64(21),
				"topic_allowlist":    []interface{}{"/device/**"},
				"service_allowlist":  []interface{}{"/device/set_mode"},
				"action_allowlist":   []interface{}{"/device/run"},
			},
		},
	})
	ros2 := config.ToolsConfig.ROS2
	if !ros2.Enabled || !ros2.AllowPublish || !ros2.AllowServiceCall || !ros2.AllowAction ||
		ros2.TimeoutMS != 45000 || ros2.MaxOutputBytes != 32768 ||
		ros2.Command != "/opt/ros/bin/ros2" || ros2.DomainID != 21 {
		t.Fatalf("unexpected ROS2 config: %+v", ros2)
	}
	if !reflect.DeepEqual(ros2.TopicAllowlist, []string{"/device/**"}) ||
		!reflect.DeepEqual(ros2.ServiceAllowlist, []string{"/device/set_mode"}) ||
		!reflect.DeepEqual(ros2.ActionAllowlist, []string{"/device/run"}) {
		t.Fatalf("unexpected ROS2 allowlists: %+v", ros2)
	}
	if _, err := normalizeROS2ToolConfig(ros2); err != nil {
		t.Fatalf("merged ROS2 config is invalid: %v", err)
	}
}

func TestTemplateROS2ConfigIsDisabledByDefault(t *testing.T) {
	content, err := os.ReadFile("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	want := "ros2:\n        enabled: false\n        allow_publish: false\n        allow_service_call: false\n        allow_action: false"
	if !strings.Contains(normalized, want) {
		t.Fatal("template ROS2 tool must default to disabled with all write gates closed")
	}
	loaded, err := LoadConfig("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	ros2 := loaded.ToolsConfig.ROS2
	if ros2.Enabled || ros2.AllowPublish || ros2.AllowServiceCall || ros2.AllowAction {
		t.Fatalf("template ROS2 config is unsafe: %+v", ros2)
	}
}
