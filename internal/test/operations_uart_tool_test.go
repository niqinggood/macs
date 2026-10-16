package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	goburrowserial "github.com/goburrow/serial"
)

type fakeUARTPort struct {
	reader *bytes.Reader
	writes bytes.Buffer
	closed bool
}

func newFakeUARTPort(response []byte) *fakeUARTPort {
	return &fakeUARTPort{reader: bytes.NewReader(response)}
}

func (p *fakeUARTPort) Read(buffer []byte) (int, error) {
	if p.reader == nil {
		return 0, io.EOF
	}
	return p.reader.Read(buffer)
}

func (p *fakeUARTPort) Write(data []byte) (int, error) {
	return p.writes.Write(data)
}

func (p *fakeUARTPort) Close() error {
	p.closed = true
	return nil
}

func enabledUARTTestConfig() UARTToolConfig {
	return UARTToolConfig{
		Enabled:       true,
		AllowWrite:    true,
		TimeoutMS:     1500,
		MaxReadBytes:  32,
		MaxWriteBytes: 32,
		Targets: map[string]UARTTargetConfig{
			"device_uart": {
				Port:       "COM_TEST",
				BaudRate:   57600,
				DataBits:   8,
				Parity:     "N",
				StopBits:   1,
				TimeoutMS:  1200,
				AllowWrite: true,
			},
		},
	}
}

func TestUARTToolTransactsWithoutDeviceSemantics(t *testing.T) {
	if !CoreTools["uart"] {
		t.Fatal("uart must be exposed when its config gate registers it")
	}
	tool := NewUARTTool(enabledUARTTestConfig())
	port := newFakeUARTPort([]byte{0x10, 0x20, 0x30})
	var opened goburrowserial.Config
	tool.SetOpenPort(func(config *goburrowserial.Config) (UARTPort, error) {
		opened = *config
		return port, nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":            "transact",
		"target":            "device_uart",
		"encoding":          "hex",
		"data":              "AA 55 01",
		"read_bytes":        3,
		"read_exact":        true,
		"response_encoding": "hex",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if opened.Address != "COM_TEST" || opened.BaudRate != 57600 || opened.Timeout.String() != "1.2s" {
		t.Fatalf("unexpected serial configuration: %+v", opened)
	}
	if got := port.writes.Bytes(); !bytes.Equal(got, []byte{0xAA, 0x55, 0x01}) {
		t.Fatalf("UART bytes written = % X", got)
	}
	if !port.closed {
		t.Fatal("UART port was not closed after transaction")
	}

	var decoded struct {
		Success      bool   `json:"success"`
		Action       string `json:"action"`
		Target       string `json:"target"`
		BytesWritten int    `json:"bytes_written"`
		BytesRead    int    `json:"bytes_read"`
		Encoding     string `json:"encoding"`
		Data         string `json:"data"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	if !decoded.Success || decoded.Action != "transact" || decoded.Target != "device_uart" ||
		decoded.BytesWritten != 3 || decoded.BytesRead != 3 ||
		decoded.Encoding != "hex" || decoded.Data != "102030" {
		t.Fatalf("unexpected UART result: %+v", decoded)
	}

	schema, _ := json.Marshal(tool.Parameters())
	for _, forbidden := range []string{"robot_move", "turn_left", "turn_right", "register_address"} {
		if strings.Contains(strings.ToLower(string(schema)), forbidden) {
			t.Fatalf("UART schema contains device semantics %q: %s", forbidden, schema)
		}
	}
}

func TestUARTToolRequiresGlobalAndTargetWriteGates(t *testing.T) {
	config := enabledUARTTestConfig()
	config.AllowWrite = false
	tool := NewUARTTool(config)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "write", "target": "device_uart", "data": "AA",
	})
	if err == nil || !strings.Contains(err.Error(), "tools_config.uart.allow_write=true") {
		t.Fatalf("global UART write gate error = %v", err)
	}

	config.AllowWrite = true
	target := config.Targets["device_uart"]
	target.AllowWrite = false
	config.Targets["device_uart"] = target
	tool = NewUARTTool(config)
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "write", "target": "device_uart", "data": "AA",
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("target UART write gate error = %v", err)
	}
}

func TestUARTToolGateHotReloadsAgentRegistry(t *testing.T) {
	registry := NewToolRegistry()
	agent := &LLMAgent{name: "Assistant", toolRegistry: registry}
	engine := &MultiAgentEngine{agentFactory: &AgentFactory{
		agents: map[string]Agent{"Assistant": agent},
	}}
	config := &Config{Agents: []AgentConfig{{Name: "Assistant"}}}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.UART = enabledUARTTestConfig()

	engine.syncUARTToolGate(config)
	if _, exists := registry.Get("uart"); !exists {
		t.Fatal("uart was not registered after enabling it")
	}

	changed := *config
	changed.ToolsConfig = config.ToolsConfig
	changed.ToolsConfig.UART = config.ToolsConfig.UART
	changed.ToolsConfig.UART.TimeoutMS = 2000
	if !uartToolGateChanged(config, &changed) {
		t.Fatal("UART runtime config change was not detected")
	}
	if !(&GatewayService{}).needUpdateEngine(config, &changed) {
		t.Fatal("gateway reload path did not detect UART config change")
	}
	if !(&ClawBot{}).needUpdateEngine(config, &changed) {
		t.Fatal("process config watcher did not detect UART config change")
	}

	config.ToolsConfig.UART.Enabled = false
	engine.syncUARTToolGate(config)
	if _, exists := registry.Get("uart"); exists {
		t.Fatal("uart remained registered after disabling it")
	}
}

func TestMergeToolsConfigParsesUARTTargets(t *testing.T) {
	config := &Config{}
	config.ToolsConfig.Enabled = true
	mergeToolsConfig(config, map[string]interface{}{
		"tools_config": map[string]interface{}{
			"uart": map[string]interface{}{
				"enabled":         true,
				"allow_write":     true,
				"timeout_ms":      float64(2500),
				"max_read_bytes":  float64(2048),
				"max_write_bytes": float64(1024),
				"targets": map[string]interface{}{
					"device_uart": map[string]interface{}{
						"port":        "/dev/ttyUSB9",
						"baud_rate":   float64(230400),
						"data_bits":   float64(8),
						"parity":      "N",
						"stop_bits":   float64(1),
						"timeout_ms":  float64(1800),
						"allow_write": true,
					},
				},
			},
		},
	})
	uart := config.ToolsConfig.UART
	if !uart.Enabled || !uart.AllowWrite || uart.TimeoutMS != 2500 ||
		uart.MaxReadBytes != 2048 || uart.MaxWriteBytes != 1024 {
		t.Fatalf("unexpected UART config: %+v", uart)
	}
	target := uart.Targets["device_uart"]
	if target.Port != "/dev/ttyUSB9" || target.BaudRate != 230400 ||
		target.DataBits != 8 || target.Parity != "N" || target.StopBits != 1 ||
		target.TimeoutMS != 1800 || !target.AllowWrite {
		t.Fatalf("unexpected UART target: %+v", target)
	}
	if _, err := normalizeUARTToolConfig(uart); err != nil {
		t.Fatalf("merged UART config is invalid: %v", err)
	}
}

func TestTemplateUARTConfigIsDisabledByDefault(t *testing.T) {
	content, err := os.ReadFile("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	want := "uart:\n        enabled: false\n        allow_write: false\n        timeout_ms: 3000"
	if !strings.Contains(normalized, want) {
		t.Fatal("template UART tool must default to disabled and read-only")
	}
	loaded, err := LoadConfig("openkitty-config.template.yaml")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	if loaded.ToolsConfig.UART.Enabled || loaded.ToolsConfig.UART.AllowWrite {
		t.Fatalf("template UART config is unsafe: %+v", loaded.ToolsConfig.UART)
	}
}
