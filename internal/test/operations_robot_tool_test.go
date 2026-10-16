package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enabledRobotTestConfig() RobotToolConfig {
	return RobotToolConfig{
		Enabled:       true,
		AllowMotion:   true,
		DefaultDevice: "sim_rover",
		Devices: map[string]RobotDeviceConfig{
			"sim_rover": {
				Enabled:        true,
				Driver:         "sim",
				AllowMotion:    true,
				WatchdogMS:     1000,
				MaxCommandMS:   1000,
				MaxLinearMPS:   1,
				MaxAngularRPS:  2,
				MaxDistanceM:   1,
				MaxTurnDegrees: 180,
			},
		},
	}
}

func TestRobotToolsExposeConstrainedSchemas(t *testing.T) {
	hub := newRobotRuntimeHub(t.TempDir(), enabledRobotTestConfig())
	tools := newRobotToolsForHub(hub)
	if len(tools) != 4 {
		t.Fatalf("robot tools = %d, want 4", len(tools))
	}
	for _, tool := range tools {
		if !CoreTools[tool.Name()] {
			t.Fatalf("%s must expose its schema whenever the robot config gate registers it", tool.Name())
		}
		if strings.Contains(strings.ToLower(tool.Description()), "shell command") {
			t.Fatalf("%s description must not offer arbitrary shell execution", tool.Name())
		}
	}
	statusTool, ok := tools[0].(*RobotStatusTool)
	if !ok || !statusTool.IsReadOnly() {
		t.Fatal("robot_status must be the read-only robot tool")
	}
	stopTool, ok := tools[1].(*RobotStopTool)
	if !ok || !stopTool.IsNeverParallel() {
		t.Fatal("robot_stop must never be scheduled in a parallel tool batch")
	}
	moveTool, ok := tools[2].(*RobotMoveTool)
	if !ok || !moveTool.IsNeverParallel() {
		t.Fatal("robot_move must never be scheduled in a parallel tool batch")
	}
	commandTool, ok := tools[3].(*RobotQuickCommandTool)
	if !ok || !commandTool.IsNeverParallel() {
		t.Fatal("robot_command must never be scheduled in a parallel tool batch")
	}

	schema := moveTool.Parameters()
	properties, _ := schema["properties"].(map[string]interface{})
	for _, field := range []string{"motion", "command_id", "distance_m", "linear_mps", "angle_deg", "angular_rps", "timeout_ms"} {
		if _, exists := properties[field]; !exists {
			t.Fatalf("robot_move schema is missing %s", field)
		}
	}
	encoded, _ := json.Marshal(schema)
	for _, forbidden := range []string{"shell", "register", "topic", "serial_bytes"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("robot_move schema exposes forbidden low-level field %q: %s", forbidden, encoded)
		}
	}
}

func TestRobotMotionRequiresBothPermissionGates(t *testing.T) {
	config := enabledRobotTestConfig()
	config.AllowMotion = false
	hub := newRobotRuntimeHub(t.TempDir(), config)

	status, err := hub.Status(context.Background(), "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status["motion_enabled"] != false {
		t.Fatalf("motion_enabled = %#v, want false", status["motion_enabled"])
	}
	_, err = hub.Move(context.Background(), map[string]interface{}{
		"motion": "translate", "distance_m": 0.1, "command_id": "gate-global",
	})
	if err == nil || !strings.Contains(err.Error(), "both tools_config.robot.allow_motion") {
		t.Fatalf("global motion gate error = %v", err)
	}

	config.AllowMotion = true
	device := config.Devices["sim_rover"]
	device.AllowMotion = false
	config.Devices["sim_rover"] = device
	hub.Configure(config)
	_, err = hub.Move(context.Background(), map[string]interface{}{
		"motion": "translate", "distance_m": 0.1, "command_id": "gate-device",
	})
	if err == nil || !strings.Contains(err.Error(), "device allow_motion") {
		t.Fatalf("device motion gate error = %v", err)
	}
}

func TestRobotSimMoveIsIdempotentAndStopPreempts(t *testing.T) {
	workspace := t.TempDir()
	hub := newRobotRuntimeHub(workspace, enabledRobotTestConfig())
	params := map[string]interface{}{
		"motion":     "translate",
		"distance_m": 0.20,
		"linear_mps": 0.40,
		"command_id": "move-forward-001",
		"timeout_ms": 800,
	}

	receipt, err := hub.Move(context.Background(), params)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if !receipt.OK || receipt.Status != "accepted" || receipt.CommandID != "move-forward-001" || receipt.DurationMS != 500 {
		t.Fatalf("unexpected move receipt: %+v", receipt)
	}

	replay, err := hub.Move(context.Background(), params)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.CommandID != receipt.CommandID {
		t.Fatalf("unexpected idempotent replay: %+v", replay)
	}

	changed := map[string]interface{}{
		"motion": "translate", "distance_m": 0.10, "linear_mps": 0.40, "command_id": "move-forward-001",
	}
	if _, err := hub.Move(context.Background(), changed); err == nil || !strings.Contains(err.Error(), "different robot motion parameters") {
		t.Fatalf("changed idempotent command error = %v", err)
	}

	time.Sleep(25 * time.Millisecond)
	stopReceipt, err := hub.Stop(context.Background(), map[string]interface{}{
		"command_id": "stop-001",
		"reason":     "test preemption",
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !stopReceipt.WasMoving || stopReceipt.StoppedCommandID != "move-forward-001" {
		t.Fatalf("unexpected stop receipt: %+v", stopReceipt)
	}

	status, err := hub.Status(context.Background(), "")
	if err != nil {
		t.Fatalf("status after stop: %v", err)
	}
	if status["mode"] != "stopped" || status["active_command_id"] != "" || status["software_stopped"] != true {
		t.Fatalf("unexpected stopped status: %#v", status)
	}
	velocity := status["velocity"].(map[string]interface{})
	if velocity["linear_mps"] != float64(0) || velocity["angular_rps"] != float64(0) {
		t.Fatalf("velocity was not zeroed: %#v", velocity)
	}
	pose := status["pose_estimate"].(map[string]interface{})
	x := pose["x_m"].(float64)
	if x < 0 || x >= 0.20 {
		t.Fatalf("preempted pose x=%f, want partial range [0, 0.20)", x)
	}

	audit, err := os.ReadFile(filepath.Join(workspace, ".openkitty", "robot", "audit.jsonl"))
	if err != nil {
		t.Fatalf("read robot audit: %v", err)
	}
	for _, marker := range []string{`"command_id":"move-forward-001"`, `"outcome":"authorized"`, `"outcome":"started"`, `"command_id":"stop-001"`, `"stopped_command_id":"move-forward-001"`} {
		if !strings.Contains(string(audit), marker) {
			t.Fatalf("robot audit is missing %s:\n%s", marker, audit)
		}
	}
}

func TestRobotSimWatchdogCompletesBoundedMotion(t *testing.T) {
	hub := newRobotRuntimeHub(t.TempDir(), enabledRobotTestConfig())
	receipt, err := hub.Move(context.Background(), map[string]interface{}{
		"motion": "rotate", "angle_deg": 5.0, "angular_rps": 2.0, "command_id": "turn-left-001",
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if receipt.DurationMS < 40 || receipt.DurationMS > 50 {
		t.Fatalf("rotate duration = %dms, want about 44ms", receipt.DurationMS)
	}

	deadline := time.Now().Add(time.Second)
	var status map[string]interface{}
	for time.Now().Before(deadline) {
		status, err = hub.Status(context.Background(), "")
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if status["mode"] == "idle" && status["active_command_id"] == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status["mode"] != "idle" || status["last_outcome"] != "completed" {
		t.Fatalf("bounded motion did not complete: %#v", status)
	}
	pose := status["pose_estimate"].(map[string]interface{})
	if math.Abs(pose["heading_deg"].(float64)-5) > 0.001 {
		t.Fatalf("heading = %v, want 5 degrees", pose["heading_deg"])
	}
}

func TestRobotQuickCommandParsesStrictChineseAndEnglish(t *testing.T) {
	cases := []struct {
		text      string
		action    string
		motion    string
		distanceM float64
		angleDeg  float64
	}{
		{text: "前进二十厘米", action: "move", motion: "translate", distanceM: 0.2},
		{text: "请小车后退0.5米。", action: "move", motion: "translate", distanceM: -0.5},
		{text: "向左转九十度", action: "move", motion: "rotate", angleDeg: 90},
		{text: "turn right 45 degrees", action: "move", motion: "rotate", angleDeg: -45},
		{text: "move forward 25 cm", action: "move", motion: "translate", distanceM: 0.25},
		{text: "机器人立即停下！", action: "stop"},
		{text: "不要再前进了，马上停", action: "stop"},
		{text: "emergency stop now", action: "stop"},
	}
	for _, test := range cases {
		t.Run(test.text, func(t *testing.T) {
			intent, err := parseRobotQuickCommand(test.text)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if intent.Action != test.action || intent.Motion != test.motion ||
				math.Abs(intent.DistanceM-test.distanceM) > 0.000001 ||
				math.Abs(intent.AngleDeg-test.angleDeg) > 0.000001 {
				t.Fatalf("intent = %+v", intent)
			}
		})
	}

	for _, ambiguous := range []string{
		"前进一点",
		"前进20厘米然后右转90度",
		"去仓库",
		"抓起箱子",
		"move forward",
		"左转一些",
	} {
		if intent, err := parseRobotQuickCommand(ambiguous); err == nil {
			t.Fatalf("ambiguous command %q unexpectedly parsed as %+v", ambiguous, intent)
		}
	}
}

func TestRobotQuickCommandExecutesThroughSafetyRuntime(t *testing.T) {
	hub := newRobotRuntimeHub(t.TempDir(), enabledRobotTestConfig())
	tool := NewRobotQuickCommandTool(hub)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"text": "前进10厘米", "command_id": "nlp-forward-001",
	})
	if err != nil {
		t.Fatalf("quick command: %v", err)
	}
	var decoded struct {
		OK           bool             `json:"ok"`
		ParsedIntent robotQuickIntent `json:"parsed_intent"`
		Result       robotMoveReceipt `json:"result"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode quick command result: %v\n%s", err, result)
	}
	if !decoded.OK || decoded.ParsedIntent.DistanceM != 0.1 || decoded.Result.CommandID != "nlp-forward-001" {
		t.Fatalf("unexpected quick command result: %+v", decoded)
	}

	stopResult, err := tool.Execute(context.Background(), map[string]interface{}{
		"text": "停", "command_id": "nlp-stop-001",
	})
	if err != nil {
		t.Fatalf("quick stop: %v", err)
	}
	var stopped struct {
		Result robotStopReceipt `json:"result"`
	}
	if err := json.Unmarshal([]byte(stopResult), &stopped); err != nil {
		t.Fatalf("decode quick stop result: %v\n%s", err, stopResult)
	}
	if !stopped.Result.OK || !stopped.Result.WasMoving || stopped.Result.StoppedCommandID != "nlp-forward-001" {
		t.Fatalf("quick stop did not preempt motion: %+v", stopped.Result)
	}
}

func TestRobotRuntimeRejectsLimitAndAmbiguousMotion(t *testing.T) {
	hub := newRobotRuntimeHub(t.TempDir(), enabledRobotTestConfig())
	cases := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{
			name: "distance limit",
			params: map[string]interface{}{
				"motion": "translate", "distance_m": 1.1, "command_id": "too-far",
			},
			want: "exceeds the configured limit",
		},
		{
			name: "speed limit",
			params: map[string]interface{}{
				"motion": "translate", "distance_m": 0.1, "linear_mps": 1.1, "command_id": "too-fast",
			},
			want: "linear_mps",
		},
		{
			name: "ambiguous fields",
			params: map[string]interface{}{
				"motion": "translate", "distance_m": 0.1, "angle_deg": 10, "command_id": "ambiguous",
			},
			want: "angle_deg is not allowed",
		},
		{
			name: "timeout",
			params: map[string]interface{}{
				"motion": "translate", "distance_m": 0.2, "linear_mps": 0.2, "timeout_ms": 100, "command_id": "too-slow",
			},
			want: "watchdog limit",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := hub.Move(context.Background(), test.params); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestRobotMotionFailsClosedWhenAuditCannotPersist(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".openkitty"), []byte("not a directory"), 0600); err != nil {
		t.Fatalf("create audit blocker: %v", err)
	}
	hub := newRobotRuntimeHub(workspace, enabledRobotTestConfig())
	_, err := hub.Move(context.Background(), map[string]interface{}{
		"motion": "translate", "distance_m": 0.1, "command_id": "audit-fail",
	})
	if err == nil || !strings.Contains(err.Error(), "audit record could not be persisted") {
		t.Fatalf("audit failure error = %v", err)
	}
	status, statusErr := hub.Status(context.Background(), "")
	if statusErr != nil {
		t.Fatalf("status: %v", statusErr)
	}
	if status["active_command_id"] != "" || status["mode"] != "idle" {
		t.Fatalf("motion remained active after audit failure: %#v", status)
	}

	// Stop is safety-critical and remains successful even when its audit record
	// cannot be persisted.
	stop, stopErr := hub.Stop(context.Background(), map[string]interface{}{"command_id": "stop-despite-audit"})
	if stopErr != nil || !stop.OK || stop.Status != "stopped" {
		t.Fatalf("stop should bypass audit failure: receipt=%+v err=%v", stop, stopErr)
	}
}

func TestRobotToolGateHotReloadsAgentRegistry(t *testing.T) {
	registry := NewToolRegistry()
	agent := &LLMAgent{name: "Assistant", toolRegistry: registry}
	engine := &MultiAgentEngine{agentFactory: &AgentFactory{
		agents: map[string]Agent{"Assistant": agent},
	}}
	config := &Config{
		Workspace: t.TempDir(),
		Agents:    []AgentConfig{{Name: "Assistant"}},
	}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.Robot = enabledRobotTestConfig()

	engine.syncRobotToolGate(config)
	for _, name := range []string{"robot_status", "robot_stop", "robot_move", "robot_command"} {
		if _, exists := registry.Get(name); !exists {
			t.Fatalf("%s was not registered after enabling robot runtime", name)
		}
	}

	changed := *config
	changed.ToolsConfig = config.ToolsConfig
	changed.ToolsConfig.Robot = config.ToolsConfig.Robot
	changed.ToolsConfig.Robot.AllowMotion = false
	if !robotToolGateChanged(config, &changed) {
		t.Fatal("robot safety config change was not detected")
	}
	if !(&GatewayService{}).needUpdateEngine(config, &changed) {
		t.Fatal("gateway reload path did not detect robot config change")
	}
	if !(&ClawBot{}).needUpdateEngine(config, &changed) {
		t.Fatal("process config watcher did not detect robot config change")
	}

	config.ToolsConfig.Robot.Enabled = false
	engine.syncRobotToolGate(config)
	for _, name := range []string{"robot_status", "robot_stop", "robot_move", "robot_command"} {
		if _, exists := registry.Get(name); exists {
			t.Fatalf("%s remained registered after disabling robot runtime", name)
		}
	}
}

func TestMergeToolsConfigParsesRobotDevices(t *testing.T) {
	config := &Config{}
	config.ToolsConfig.Enabled = true
	mergeToolsConfig(config, map[string]interface{}{
		"tools_config": map[string]interface{}{
			"robot": map[string]interface{}{
				"enabled":        true,
				"allow_motion":   true,
				"default_device": "rover_1",
				"devices": map[string]interface{}{
					"rover_1": map[string]interface{}{
						"enabled":          true,
						"driver":           "sim",
						"allow_motion":     true,
						"watchdog_ms":      float64(2200),
						"max_command_ms":   float64(2000),
						"max_linear_mps":   0.35,
						"max_angular_rps":  0.75,
						"max_distance_m":   0.8,
						"max_turn_degrees": float64(120),
					},
				},
			},
		},
	})
	robot := config.ToolsConfig.Robot
	if !robot.Enabled || !robot.AllowMotion || robot.DefaultDevice != "rover_1" {
		t.Fatalf("unexpected robot config: %+v", robot)
	}
	device := robot.Devices["rover_1"]
	if !device.Enabled || device.Driver != "sim" || !device.AllowMotion ||
		device.WatchdogMS != 2200 || device.MaxCommandMS != 2000 ||
		device.MaxLinearMPS != 0.35 || device.MaxAngularRPS != 0.75 ||
		device.MaxDistanceM != 0.8 || device.MaxTurnDegrees != 120 {
		t.Fatalf("unexpected robot device: %+v", device)
	}
	if _, err := normalizeRobotToolConfig(robot); err != nil {
		t.Fatalf("merged robot config is invalid: %v", err)
	}
}

func TestRobotTemplateAndBuiltinSkillAreSafeByDefault(t *testing.T) {
	template, err := os.ReadFile("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("read config template: %v", err)
	}
	normalized := strings.ReplaceAll(string(template), "\r\n", "\n")
	for _, marker := range []string{
		"robot:\n        enabled: false\n        allow_motion: false",
		"driver: sim",
		"allow_motion: false",
		"watchdog_ms: 5000",
		"max_distance_m: 1.0",
	} {
		if !strings.Contains(normalized, marker) {
			t.Fatalf("safe robot template is missing %q", marker)
		}
	}
	loaded, err := LoadConfig("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("load config template: %v", err)
	}
	if loaded.ToolsConfig.Robot.Enabled || loaded.ToolsConfig.Robot.AllowMotion ||
		loaded.ToolsConfig.Robot.DefaultDevice != "sim_rover" ||
		loaded.ToolsConfig.Robot.Devices["sim_rover"].Driver != "sim" {
		t.Fatalf("decoded robot template is not safely disabled: %+v", loaded.ToolsConfig.Robot)
	}

	skillPath := "builtin/robotics/robot-control/SKILL.md"
	skill, err := fs.ReadFile(builtinSkillsFS, skillPath)
	if err != nil {
		t.Fatalf("embedded robot skill %s: %v", skillPath, err)
	}
	for _, marker := range []string{
		"name: robot-control",
		"`robot_status` before motion",
		"`robot_command`",
		"Call `robot_stop` immediately",
		"Never bypass it with shell",
		"Do not claim physical movement while the driver is `sim`",
	} {
		if !strings.Contains(string(skill), marker) {
			t.Fatalf("robot skill is missing %q", marker)
		}
	}
}
