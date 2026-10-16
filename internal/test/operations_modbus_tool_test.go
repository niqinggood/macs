package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestModbusToolReadsHoldingRegistersFromConversationalEndpoint(t *testing.T) {
	address, done := startModbusTestServer(t, func(unitID byte, pdu []byte) ([]byte, error) {
		if unitID != 7 {
			return nil, fmt.Errorf("unit ID = %d, want 7", unitID)
		}
		if len(pdu) != 5 || pdu[0] != 0x03 {
			return nil, fmt.Errorf("unexpected read PDU: %v", pdu)
		}
		if got := binary.BigEndian.Uint16(pdu[1:3]); got != 10 {
			return nil, fmt.Errorf("address = %d, want 10", got)
		}
		if got := binary.BigEndian.Uint16(pdu[3:5]); got != 2 {
			return nil, fmt.Errorf("quantity = %d, want 2", got)
		}
		return []byte{0x03, 0x04, 0x00, 0x2A, 0x00, 0x63}, nil
	})

	tool := NewModbusTool(ModbusToolConfig{
		Enabled:   true,
		TimeoutMS: 1000,
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":   "read_holding_registers",
		"endpoint": address,
		"unit_id":  7,
		"address":  10,
		"quantity": 2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	waitModbusServer(t, done)

	var decoded struct {
		Success bool     `json:"success"`
		Values  []uint16 `json:"values"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	if !decoded.Success || len(decoded.Values) != 2 || decoded.Values[0] != 42 || decoded.Values[1] != 99 {
		t.Fatalf("unexpected read result: %+v", decoded)
	}
}

func TestModbusToolWritesMultipleRegistersWhenBothWriteGatesAreEnabled(t *testing.T) {
	address, done := startModbusTestServer(t, func(unitID byte, pdu []byte) ([]byte, error) {
		if unitID != 1 || len(pdu) != 12 || pdu[0] != 0x10 {
			return nil, fmt.Errorf("unexpected write PDU: unit=%d pdu=%v", unitID, pdu)
		}
		if got := binary.BigEndian.Uint16(pdu[1:3]); got != 20 {
			return nil, fmt.Errorf("address = %d, want 20", got)
		}
		if got := binary.BigEndian.Uint16(pdu[3:5]); got != 3 || pdu[5] != 6 {
			return nil, fmt.Errorf("invalid quantity/byte count: %v", pdu[3:6])
		}
		want := []uint16{11, 22, 33}
		for i, value := range want {
			if got := binary.BigEndian.Uint16(pdu[6+i*2 : 8+i*2]); got != value {
				return nil, fmt.Errorf("value[%d] = %d, want %d", i, got, value)
			}
		}
		return []byte{0x10, 0x00, 0x14, 0x00, 0x03}, nil
	})

	tool := NewModbusTool(ModbusToolConfig{
		Enabled:    true,
		AllowWrite: true,
		TimeoutMS:  1000,
		Targets: map[string]ModbusTargetConfig{
			"machine_1": {Address: address, UnitID: 1, AllowWrite: true},
		},
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "write_multiple_registers",
		"target":  "machine_1",
		"address": 20,
		"values":  []interface{}{11.0, 22.0, 33.0},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	waitModbusServer(t, done)
	if !strings.Contains(result, `"success": true`) || !strings.Contains(result, `"quantity": 3`) {
		t.Fatalf("unexpected write result: %s", result)
	}
}

func TestModbusToolBlocksWritesUnlessBothWriteGatesAreEnabled(t *testing.T) {
	config := ModbusToolConfig{
		Enabled: true,
		Targets: map[string]ModbusTargetConfig{
			"machine_1": {Address: "127.0.0.1:502", UnitID: 1, AllowWrite: true},
		},
	}
	_, err := NewModbusTool(config).Execute(context.Background(), map[string]interface{}{
		"action": "write_single_coil", "target": "machine_1", "address": 0, "value": true,
	})
	if err == nil || !strings.Contains(err.Error(), "tools_config.modbus.allow_write=true") {
		t.Fatalf("global write gate error = %v", err)
	}

	config.AllowWrite = true
	target := config.Targets["machine_1"]
	target.AllowWrite = false
	config.Targets["machine_1"] = target
	_, err = NewModbusTool(config).Execute(context.Background(), map[string]interface{}{
		"action": "write_single_register", "target": "machine_1", "address": 0, "value": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "target \"machine_1\" is read-only") {
		t.Fatalf("target write gate error = %v", err)
	}
}

func TestModbusToolOnlyUsesConfiguredTargets(t *testing.T) {
	if !CoreTools["plc_modbus"] {
		t.Fatal("plc_modbus must expose its schema whenever the config gate registers it")
	}
	tool := NewModbusTool(ModbusToolConfig{
		Enabled: true,
		Targets: map[string]ModbusTargetConfig{
			"machine_1": {Address: "10.20.30.40:502", UnitID: 1},
		},
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "list_targets"})
	if err != nil {
		t.Fatalf("list_targets: %v", err)
	}
	if !strings.Contains(result, "machine_1") {
		t.Fatalf("configured target missing from result: %s", result)
	}
	if strings.Contains(result, "10.20.30.40") {
		t.Fatalf("list_targets should not expose PLC network addresses: %s", result)
	}
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "read_coils", "target": "10.20.30.40:502", "address": 0, "quantity": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "is not configured") {
		t.Fatalf("direct unconfigured address error = %v", err)
	}
}

func TestModbusToolNormalizesRTUTarget(t *testing.T) {
	tool := NewModbusTool(ModbusToolConfig{
		Enabled: true,
		Targets: map[string]ModbusTargetConfig{
			"serial_plc": {Transport: "rtu", Address: " COM7 ", UnitID: 2},
		},
	})
	name, target, err := tool.ResolveTarget("SERIAL_PLC")
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if name != "serial_plc" || target.Transport != "rtu" || target.Address != "COM7" {
		t.Fatalf("unexpected RTU target identity: name=%q target=%+v", name, target)
	}
	if target.BaudRate != 19200 || target.DataBits != 8 || target.Parity != "E" || target.StopBits != 1 {
		t.Fatalf("unexpected RTU defaults: %+v", target)
	}
	result, err := tool.ListTargets()
	if err != nil {
		t.Fatalf("listTargets: %v", err)
	}
	if !strings.Contains(result, `"transport": "rtu"`) || strings.Contains(result, "COM7") {
		t.Fatalf("RTU target listing should expose transport but not serial address: %s", result)
	}
}

func TestModbusToolRejectsUnknownTransportBeforeConnecting(t *testing.T) {
	tool := NewModbusTool(ModbusToolConfig{
		Enabled: true,
		Targets: map[string]ModbusTargetConfig{
			"machine_1": {Transport: "udp", Address: "127.0.0.1:502", UnitID: 1},
		},
	})
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "read_coils", "target": "machine_1", "address": 0, "quantity": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "transport") || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown transport error = %v", err)
	}
}

func TestModbusToolAgainstExternalSimulator(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("OPENKITTY_MODBUS_TEST_ADDRESS"))
	if address == "" {
		t.Skip("set OPENKITTY_MODBUS_TEST_ADDRESS to run against a Modbus TCP simulator")
	}
	tool := NewModbusTool(ModbusToolConfig{
		Enabled:    true,
		AllowWrite: true,
		TimeoutMS:  3000,
	})

	execute := func(params map[string]interface{}) string {
		t.Helper()
		params["endpoint"] = address
		params["unit_id"] = 1
		result, err := tool.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("%s: %v", params["action"], err)
		}
		return result
	}

	execute(map[string]interface{}{
		"action": "write_single_register", "address": 100, "value": 350,
	})
	execute(map[string]interface{}{
		"action": "write_single_register", "address": 101, "value": 1234,
	})
	execute(map[string]interface{}{
		"action": "write_multiple_registers", "address": 102, "values": []interface{}{11, 22, 33},
	})
	registerResult := execute(map[string]interface{}{
		"action": "read_holding_registers", "address": 100, "quantity": 5,
	})
	assertModbusIntegerValues(t, registerResult, []uint16{350, 1234, 11, 22, 33})
	inputResult := execute(map[string]interface{}{
		"action": "read_input_registers", "address": 100, "quantity": 5,
	})
	assertModbusIntegerValues(t, inputResult, []uint16{350, 1234, 11, 22, 33})

	execute(map[string]interface{}{
		"action": "write_single_coil", "address": 0, "value": true,
	})
	execute(map[string]interface{}{
		"action": "write_multiple_coils", "address": 1, "values": []interface{}{true, false, true},
	})
	coilResult := execute(map[string]interface{}{
		"action": "read_coils", "address": 0, "quantity": 4,
	})
	assertModbusBoolValues(t, coilResult, []bool{true, true, false, true})
	discreteResult := execute(map[string]interface{}{
		"action": "read_discrete_inputs", "address": 0, "quantity": 4,
	})
	assertModbusBoolValues(t, discreteResult, []bool{true, true, false, true})
}

func TestModbusToolThroughNaturalLanguageAgent(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_MODBUS_NL_TEST")) != "1" {
		t.Skip("set OPENKITTY_MODBUS_NL_TEST=1 to run the external LLM/tool integration test")
	}
	address := strings.TrimSpace(os.Getenv("OPENKITTY_MODBUS_TEST_ADDRESS"))
	if address == "" {
		t.Fatal("OPENKITTY_MODBUS_TEST_ADDRESS is required")
	}

	config, err := LoadConfig("openkitty-config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	config.MemoryConfig.Enabled = false
	config.SkillsConfig.DisEnabled = true
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.DisabledTools = nil
	config.ToolsConfig.Modbus = ModbusToolConfig{
		Enabled:    true,
		AllowWrite: true,
		TimeoutMS:  3000,
	}

	var assistantConfig *AgentConfig
	for i := range config.Agents {
		if strings.EqualFold(config.Agents[i].Name, "Assistant") {
			assistantConfig = &config.Agents[i]
			break
		}
	}
	if assistantConfig == nil {
		t.Fatal("Assistant agent is not configured")
	}
	assistantConfig.EnabledTools = []string{"plc_modbus"}
	assistantConfig.DisallowedTools = nil
	memoryDisabled := false
	assistantConfig.HasMemory = &memoryDisabled
	if requestedModel := strings.TrimSpace(os.Getenv("OPENKITTY_MODBUS_NL_MODEL")); requestedModel != "" {
		assistantConfig.Model = requestedModel
	}
	assistantConfig.MaxIterations = 12
	assistantConfig.MaxTokens = 2048
	modelConfig, ok := config.ModelConfig[assistantConfig.Model]
	if !ok {
		t.Fatalf("model %q is not configured", assistantConfig.Model)
	}

	tool := NewModbusTool(config.ToolsConfig.Modbus)
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "write_single_register", "endpoint": address, "unit_id": 1, "address": 101, "value": 1234,
	}); err != nil {
		t.Fatalf("reset simulator register: %v", err)
	}

	agent := NewLLMAgent(*assistantConfig, &modelConfig, nil, config, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	readPrompt := fmt.Sprintf("请通过 Modbus TCP 连接 %s，站号 1，读取零基地址 100 开始的 5 个保持寄存器，并告诉我实际读到的数值。设备连接信息只在本次对话提供，不要让我修改配置文件。请实际操作，不要猜测。", address)
	readAnswer, err := agent.Generate(ctx, readPrompt)
	if err != nil {
		t.Fatalf("natural-language read: %v", err)
	}
	t.Logf("USER READ: %s", readPrompt)
	t.Logf("ASSISTANT READ: %s", readAnswer)
	for _, value := range []string{"350", "1234", "11", "22", "33"} {
		if !strings.Contains(readAnswer, value) {
			t.Fatalf("read answer does not contain %s: %s", value, readAnswer)
		}
	}

	writePrompt := "继续操作刚才对话中的那个 PLC：把零基保持寄存器地址 101 写成 2468，然后重新读取地址 100 开始的 5 个保持寄存器确认。请实际操作设备。"
	writeCtx := context.WithValue(ctx, "history_messages", []map[string]interface{}{
		{"role": "user", "content": readPrompt},
		{"role": "assistant", "content": readAnswer},
	})
	writeAnswer, err := agent.Generate(writeCtx, writePrompt)
	if err != nil {
		t.Fatalf("natural-language write: %v", err)
	}
	t.Logf("USER WRITE: %s", writePrompt)
	t.Logf("ASSISTANT WRITE: %s", writeAnswer)

	verifyResult, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "read_holding_registers", "endpoint": address, "unit_id": 1, "address": 100, "quantity": 5,
	})
	if err != nil {
		t.Fatalf("verify natural-language write: %v", err)
	}
	assertModbusIntegerValues(t, verifyResult, []uint16{350, 2468, 11, 22, 33})
	if !strings.Contains(writeAnswer, "2468") {
		t.Fatalf("write answer does not contain confirmed value: %s", writeAnswer)
	}
}

func assertModbusIntegerValues(t *testing.T, result string, want []uint16) {
	t.Helper()
	var decoded struct {
		Values []uint16 `json:"values"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode Modbus integer result: %v\n%s", err, result)
	}
	if len(decoded.Values) != len(want) {
		t.Fatalf("integer values = %v, want %v", decoded.Values, want)
	}
	for i := range want {
		if decoded.Values[i] != want[i] {
			t.Fatalf("integer values = %v, want %v", decoded.Values, want)
		}
	}
}

func assertModbusBoolValues(t *testing.T, result string, want []bool) {
	t.Helper()
	var decoded struct {
		Values []bool `json:"values"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode Modbus bit result: %v\n%s", err, result)
	}
	if len(decoded.Values) != len(want) {
		t.Fatalf("bit values = %v, want %v", decoded.Values, want)
	}
	for i := range want {
		if decoded.Values[i] != want[i] {
			t.Fatalf("bit values = %v, want %v", decoded.Values, want)
		}
	}
}

func TestSyncModbusToolGateUpdatesExistingAgentRegistry(t *testing.T) {
	registry := NewToolRegistry()
	agent := &LLMAgent{name: "Assistant", toolRegistry: registry}
	engine := &MultiAgentEngine{agentFactory: &AgentFactory{
		agents: map[string]Agent{"Assistant": agent},
	}}
	config := &Config{Agents: []AgentConfig{{Name: "Assistant"}}}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.Modbus = ModbusToolConfig{
		Enabled: true,
		Targets: map[string]ModbusTargetConfig{"machine_1": {Address: "127.0.0.1:502", UnitID: 1}},
	}

	engine.syncModbusToolGate(config)
	if _, exists := registry.Get("plc_modbus"); !exists {
		t.Fatal("plc_modbus was not registered after enabling it")
	}

	changedConfig := *config
	changedConfig.ToolsConfig = config.ToolsConfig
	changedConfig.ToolsConfig.Modbus = config.ToolsConfig.Modbus
	changedConfig.ToolsConfig.Modbus.TimeoutMS = 5000
	if !modbusToolGateChanged(config, &changedConfig) {
		t.Fatal("target/runtime Modbus config change was not detected")
	}
	if !(&GatewayService{}).needUpdateEngine(config, &changedConfig) {
		t.Fatal("gateway reload path did not detect the Modbus config change")
	}
	if !(&ClawBot{}).needUpdateEngine(config, &changedConfig) {
		t.Fatal("process config watcher did not detect the Modbus config change")
	}

	config.ToolsConfig.Modbus.Enabled = false
	engine.syncModbusToolGate(config)
	if _, exists := registry.Get("plc_modbus"); exists {
		t.Fatal("plc_modbus remained registered after disabling it")
	}
}

func TestMergeToolsConfigParsesModbusTargets(t *testing.T) {
	config := &Config{}
	config.ToolsConfig.Enabled = true
	mergeToolsConfig(config, map[string]interface{}{
		"tools_config": map[string]interface{}{
			"modbus": map[string]interface{}{
				"enabled":     true,
				"allow_write": true,
				"timeout_ms":  float64(2500),
				"targets": map[string]interface{}{
					"machine_1": map[string]interface{}{
						"transport":   "rtu",
						"address":     "COM9",
						"unit_id":     float64(9),
						"baud_rate":   float64(115200),
						"data_bits":   float64(8),
						"parity":      "N",
						"stop_bits":   float64(2),
						"allow_write": true,
					},
				},
			},
		},
	})
	if !config.ToolsConfig.Modbus.Enabled || !config.ToolsConfig.Modbus.AllowWrite || config.ToolsConfig.Modbus.TimeoutMS != 2500 {
		t.Fatalf("unexpected merged Modbus gate: %+v", config.ToolsConfig.Modbus)
	}
	target, ok := config.ToolsConfig.Modbus.Targets["machine_1"]
	if !ok || target.Transport != "rtu" || target.Address != "COM9" || target.UnitID != 9 ||
		target.BaudRate != 115200 || target.DataBits != 8 || target.Parity != "N" || target.StopBits != 2 || !target.AllowWrite {
		t.Fatalf("unexpected merged Modbus target: %+v", target)
	}
}

func TestTemplateModbusConfigIsDisabledByDefault(t *testing.T) {
	content, err := os.ReadFile("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	want := "modbus:\n        enabled: false\n        allow_write: false\n        timeout_ms: 3000"
	if !strings.Contains(strings.ReplaceAll(string(content), "\r\n", "\n"), want) {
		t.Fatal("template Modbus machine control must default to disabled with a 3000ms timeout")
	}
}

func TestWebModbusControlsLiveInToolsSettings(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	appJS := mustReadWebTestFile(t, "web/app.js")
	for _, marker := range []string{
		`data-tool-name="plc_modbus"`,
		`id="modbusToolEnabled"`,
		`id="modbusWriteEnabled"`,
		`id="modbusTargetCount"`,
		`tools_config.modbus.targets`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("Modbus settings UI missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function collectModbusConfigFromSettings()",
		"modbus: collectModbusConfigFromSettings()",
		"syncModbusSettingsState(false)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("Modbus settings behavior missing %q", marker)
		}
	}
}

type modbusTestHandler func(unitID byte, pdu []byte) ([]byte, error)

func startModbusTestServer(t *testing.T, handler modbusTestHandler) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		header := make([]byte, 7)
		if _, err := io.ReadFull(conn, header); err != nil {
			done <- err
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if length < 2 {
			done <- fmt.Errorf("invalid request length %d", length)
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			done <- err
			return
		}
		responsePDU, err := handler(header[6], pdu)
		if err != nil {
			done <- err
			return
		}
		responseHeader := append([]byte(nil), header...)
		binary.BigEndian.PutUint16(responseHeader[4:6], uint16(len(responsePDU)+1))
		if err := writeAllModbusTestResponse(conn, append(responseHeader, responsePDU...)); err != nil {
			done <- err
			return
		}
		done <- nil
	}()
	return listener.Addr().String(), done
}

func writeAllModbusTestResponse(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func waitModbusServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Modbus test server: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Modbus test server")
	}
}
