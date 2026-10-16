package operations

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goburrowmodbus "github.com/goburrow/modbus"
	"mac/internal/configvalue"
)

// ModbusToolConfig controls the PLC Modbus tool. The tool is deliberately
// disabled by default. PLC endpoints may be supplied conversationally on each
// tool call; Targets remains available for backwards-compatible named targets.
type ModbusToolConfig struct {
	Enabled    bool                          `json:"enabled" yaml:"enabled"`
	AllowWrite bool                          `json:"allow_write" yaml:"allow_write"`
	TimeoutMS  int                           `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Targets    map[string]ModbusTargetConfig `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// ModbusTargetConfig describes one explicitly allowed PLC endpoint. Transport
// defaults to tcp. For rtu/ascii, Address is the serial device name (for
// example COM3 or /dev/ttyUSB0) and omitted serial settings use 19200/8/E/1.
type ModbusTargetConfig struct {
	Transport string `json:"transport,omitempty" yaml:"transport,omitempty"`
	Address   string `json:"address" yaml:"address"`
	UnitID    int    `json:"unit_id,omitempty" yaml:"unit_id,omitempty"`

	BaudRate int    `json:"baud_rate,omitempty" yaml:"baud_rate,omitempty"`
	DataBits int    `json:"data_bits,omitempty" yaml:"data_bits,omitempty"`
	Parity   string `json:"parity,omitempty" yaml:"parity,omitempty"`
	StopBits int    `json:"stop_bits,omitempty" yaml:"stop_bits,omitempty"`

	AllowWrite bool `json:"allow_write" yaml:"allow_write"`
}

type ModbusTool struct {
	config ModbusToolConfig
}

var modbusTargetLocks sync.Map

func NewModbusTool(config ModbusToolConfig) *ModbusTool {
	copyConfig := config
	copyConfig.Targets = make(map[string]ModbusTargetConfig, len(config.Targets))
	for name, target := range config.Targets {
		copyConfig.Targets[name] = target
	}
	return &ModbusTool{config: copyConfig}
}

func (t *ModbusTool) Name() string { return "plc_modbus" }

func (t *ModbusTool) Description() string {
	return "Read from and write to PLCs over Modbus TCP, RTU, or ASCII using github.com/goburrow/modbus. " +
		"Supports coils, discrete inputs, holding registers, and input registers. " +
		"Addresses are zero-based Modbus protocol offsets, not 00001/40001 display references. " +
		"For a conversational connection, pass the endpoint stated by the user in the current or an earlier message of the same conversation (for example 192.168.1.20:502), and never invent or scan for one. " +
		"When the user explicitly commands a write and the endpoint is already present in the same conversation, execute it without asking them to repeat or reconfirm the endpoint. " +
		"A configured target name is optional for backwards compatibility. Writes require the global Modbus write permission; configured targets also require their target write permission."
}

func (t *ModbusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"list_targets",
					"read_coils",
					"read_discrete_inputs",
					"read_holding_registers",
					"read_input_registers",
					"write_single_coil",
					"write_single_register",
					"write_multiple_coils",
					"write_multiple_registers",
				},
				"description": "Modbus operation to execute",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "Optional named PLC target from tools_config.modbus.targets. Use either target or endpoint, not both",
			},
			"endpoint": map[string]interface{}{
				"type":        "string",
				"description": "PLC connection endpoint taken verbatim from the current or an earlier user message in the same conversation: TCP host or host:port (port defaults to 502), or serial device such as COM3 for RTU/ASCII",
			},
			"transport": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"tcp", "rtu", "ascii"},
				"description": "Connection transport for endpoint; defaults to tcp",
			},
			"unit_id": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     247,
				"description": "Modbus unit/slave ID; defaults to 1",
			},
			"baud_rate": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     4000000,
				"description": "RTU/ASCII baud rate; defaults to 19200",
			},
			"data_bits": map[string]interface{}{
				"type":        "integer",
				"minimum":     5,
				"maximum":     8,
				"description": "RTU/ASCII data bits; defaults to 8",
			},
			"parity": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"N", "E", "O"},
				"description": "RTU/ASCII parity; defaults to E",
			},
			"stop_bits": map[string]interface{}{
				"type":        "integer",
				"enum":        []int{1, 2},
				"description": "RTU/ASCII stop bits; defaults to 1",
			},
			"address": map[string]interface{}{
				"type":        "integer",
				"minimum":     0,
				"maximum":     65535,
				"description": "Zero-based Modbus protocol address",
			},
			"quantity": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"description": "Number of coils or registers to read",
			},
			"value": map[string]interface{}{
				"description": "Boolean/0/1 for a single coil, or integer 0..65535 for a single register",
			},
			"values": map[string]interface{}{
				"type":        "array",
				"description": "Boolean array for multiple coils, or integer array for multiple registers",
				"items":       map[string]interface{}{},
			},
		},
		"required": []string{"action"},
	}
}

// The same tool can perform writes, so it must never be classified as
// concurrency-safe. Target locks also serialize calls from different agents.
func (t *ModbusTool) IsReadOnly() bool      { return false }
func (t *ModbusTool) IsNeverParallel() bool { return true }
func (t *ModbusTool) MaxResultSize() int    { return 12000 }

func (t *ModbusTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(configvalue.String(params, "action", "")))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	if action == "list_targets" {
		return t.listTargets()
	}

	resolvedName, target, configuredTarget, err := t.resolveRequestTarget(params)
	if err != nil {
		return "", err
	}

	lockValue, _ := modbusTargetLocks.LoadOrStore(modbusTargetLockKey(target), &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	switch action {
	case "read_coils":
		return t.readBits(ctx, resolvedName, target, params, 0x01, 2000)
	case "read_discrete_inputs":
		return t.readBits(ctx, resolvedName, target, params, 0x02, 2000)
	case "read_holding_registers":
		return t.readRegisters(ctx, resolvedName, target, params, 0x03)
	case "read_input_registers":
		return t.readRegisters(ctx, resolvedName, target, params, 0x04)
	case "write_single_coil":
		if err := t.checkWriteAllowed(resolvedName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.writeSingleCoil(ctx, resolvedName, target, params)
	case "write_single_register":
		if err := t.checkWriteAllowed(resolvedName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.writeSingleRegister(ctx, resolvedName, target, params)
	case "write_multiple_coils":
		if err := t.checkWriteAllowed(resolvedName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.writeMultipleCoils(ctx, resolvedName, target, params)
	case "write_multiple_registers":
		if err := t.checkWriteAllowed(resolvedName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.writeMultipleRegisters(ctx, resolvedName, target, params)
	default:
		return "", fmt.Errorf("unsupported Modbus action %q", action)
	}
}

func (t *ModbusTool) resolveRequestTarget(params map[string]interface{}) (string, ModbusTargetConfig, bool, error) {
	targetName := strings.TrimSpace(configvalue.String(params, "target", ""))
	endpoint := strings.TrimSpace(configvalue.String(params, "endpoint", ""))
	if targetName != "" && endpoint != "" {
		return "", ModbusTargetConfig{}, false, fmt.Errorf("use either target or endpoint, not both")
	}
	if endpoint == "" {
		if targetName == "" {
			return "", ModbusTargetConfig{}, false, fmt.Errorf("endpoint (from the conversation) or configured target is required")
		}
		name, target, err := t.resolveTarget(targetName)
		return name, target, true, err
	}

	target := ModbusTargetConfig{
		Transport:  strings.TrimSpace(configvalue.String(params, "transport", "")),
		Address:    endpoint,
		Parity:     strings.TrimSpace(configvalue.String(params, "parity", "")),
		AllowWrite: true,
	}
	var err error
	if target.UnitID, err = optionalModbusInteger(params, "unit_id", 1, 247); err != nil {
		return "", ModbusTargetConfig{}, false, err
	}
	if target.BaudRate, err = optionalModbusInteger(params, "baud_rate", 1, 4_000_000); err != nil {
		return "", ModbusTargetConfig{}, false, err
	}
	if target.DataBits, err = optionalModbusInteger(params, "data_bits", 5, 8); err != nil {
		return "", ModbusTargetConfig{}, false, err
	}
	if target.StopBits, err = optionalModbusInteger(params, "stop_bits", 1, 2); err != nil {
		return "", ModbusTargetConfig{}, false, err
	}
	if err := normalizeModbusTarget(&target); err != nil {
		return "", ModbusTargetConfig{}, false, fmt.Errorf("conversational Modbus endpoint: %w", err)
	}
	return target.Address, target, false, nil
}

func (t *ModbusTool) listTargets() (string, error) {
	names := make([]string, 0, len(t.config.Targets))
	for name := range t.config.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	targets := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		target := t.config.Targets[name]
		unitID := target.UnitID
		if unitID == 0 {
			unitID = 1
		}
		transport := strings.ToLower(strings.TrimSpace(target.Transport))
		if transport == "" {
			transport = "tcp"
		}
		targets = append(targets, map[string]interface{}{
			"name":          name,
			"transport":     transport,
			"unit_id":       unitID,
			"write_enabled": t.config.AllowWrite && target.AllowWrite,
		})
	}
	return marshalModbusResult(map[string]interface{}{
		"tool_enabled":  t.config.Enabled,
		"write_enabled": t.config.AllowWrite,
		"targets":       targets,
	})
}

func (t *ModbusTool) resolveTarget(requested string) (string, ModbusTargetConfig, error) {
	name := requested
	target, ok := t.config.Targets[name]
	if !ok {
		for configuredName, configuredTarget := range t.config.Targets {
			if strings.EqualFold(strings.TrimSpace(configuredName), requested) {
				name = configuredName
				target = configuredTarget
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", ModbusTargetConfig{}, fmt.Errorf("Modbus target %q is not configured", requested)
	}

	if err := normalizeModbusTarget(&target); err != nil {
		return "", ModbusTargetConfig{}, fmt.Errorf("Modbus target %q: %w", name, err)
	}
	return name, target, nil
}

func (t *ModbusTool) ResolveTarget(requested string) (string, ModbusTargetConfig, error) {
	return t.resolveTarget(requested)
}

func (t *ModbusTool) ListTargets() (string, error) {
	return t.listTargets()
}

func normalizeModbusTarget(target *ModbusTargetConfig) error {
	transport, err := normalizeModbusTransport(target.Transport)
	if err != nil {
		return err
	}
	target.Transport = transport
	if transport == "tcp" {
		address, addressErr := normalizeModbusAddress(target.Address)
		if addressErr != nil {
			return addressErr
		}
		target.Address = address
	} else if err := normalizeModbusSerialTarget(target); err != nil {
		return err
	}
	if target.UnitID == 0 {
		target.UnitID = 1
	}
	if target.UnitID < 1 || target.UnitID > 247 {
		return fmt.Errorf("invalid unit_id %d (expected 1..247)", target.UnitID)
	}
	return nil
}

func normalizeModbusTransport(value string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "", "tcp", "modbus_tcp":
		return "tcp", nil
	case "rtu", "modbus_rtu":
		return "rtu", nil
	case "ascii", "modbus_ascii":
		return "ascii", nil
	default:
		return "", fmt.Errorf("transport %q is unsupported (expected tcp, rtu, or ascii)", value)
	}
}

func normalizeModbusSerialTarget(target *ModbusTargetConfig) error {
	if target == nil {
		return fmt.Errorf("serial target is missing")
	}
	target.Address = strings.TrimSpace(target.Address)
	if target.Address == "" {
		return fmt.Errorf("serial address is empty")
	}
	if target.BaudRate == 0 {
		target.BaudRate = 19200
	}
	if target.BaudRate < 1 || target.BaudRate > 4_000_000 {
		return fmt.Errorf("baud_rate %d is invalid", target.BaudRate)
	}
	if target.DataBits == 0 {
		target.DataBits = 8
	}
	if target.DataBits < 5 || target.DataBits > 8 {
		return fmt.Errorf("data_bits %d is invalid (expected 5..8)", target.DataBits)
	}
	target.Parity = strings.ToUpper(strings.TrimSpace(target.Parity))
	if target.Parity == "" {
		target.Parity = "E"
	}
	if target.Parity != "N" && target.Parity != "E" && target.Parity != "O" {
		return fmt.Errorf("parity %q is invalid (expected N, E, or O)", target.Parity)
	}
	if target.StopBits == 0 {
		target.StopBits = 1
	}
	if target.StopBits != 1 && target.StopBits != 2 {
		return fmt.Errorf("stop_bits %d is invalid (expected 1 or 2)", target.StopBits)
	}
	return nil
}

func normalizeModbusAddress(value string) (string, error) {
	address := strings.TrimSpace(value)
	if address == "" {
		return "", fmt.Errorf("address is empty")
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return "", fmt.Errorf("address must contain a host and port")
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", fmt.Errorf("invalid TCP port %q", port)
		}
		return address, nil
	}

	trimmedIP := strings.Trim(address, "[]")
	if ip := net.ParseIP(trimmedIP); ip != nil {
		return net.JoinHostPort(ip.String(), "502"), nil
	}
	if !strings.Contains(address, ":") {
		return net.JoinHostPort(address, "502"), nil
	}
	return "", fmt.Errorf("address %q must use host:port (default Modbus TCP port is 502)", address)
}

func (t *ModbusTool) checkWriteAllowed(targetName string, target ModbusTargetConfig, configuredTarget bool) error {
	if !t.config.AllowWrite {
		return fmt.Errorf("Modbus writes are disabled; set tools_config.modbus.allow_write=true to permit machine control")
	}
	if configuredTarget && !target.AllowWrite {
		return fmt.Errorf("Modbus target %q is read-only; set its allow_write=true to permit machine control", targetName)
	}
	return nil
}

func (t *ModbusTool) readBits(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}, function byte, maxQuantity int) (string, error) {
	address, quantity, err := modbusAddressAndQuantity(params, maxQuantity)
	if err != nil {
		return "", err
	}
	response, err := t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		switch function {
		case goburrowmodbus.FuncCodeReadCoils:
			return client.ReadCoils(address, uint16(quantity))
		case goburrowmodbus.FuncCodeReadDiscreteInputs:
			return client.ReadDiscreteInputs(address, uint16(quantity))
		default:
			return nil, fmt.Errorf("unsupported bit-read function 0x%02X", function)
		}
	})
	if err != nil {
		return "", fmt.Errorf("%s on target %q failed: %w", modbusFunctionName(function), targetName, err)
	}
	wantBytes := (quantity + 7) / 8
	if len(response) != wantBytes {
		return "", fmt.Errorf("%s on target %q returned %d data bytes, want %d", modbusFunctionName(function), targetName, len(response), wantBytes)
	}
	values := make([]bool, quantity)
	for i := 0; i < quantity; i++ {
		values[i] = response[i/8]&(1<<uint(i%8)) != 0
	}
	return marshalModbusResult(map[string]interface{}{
		"success":  true,
		"action":   modbusFunctionName(function),
		"target":   targetName,
		"unit_id":  target.UnitID,
		"address":  address,
		"quantity": quantity,
		"values":   values,
	})
}

func (t *ModbusTool) readRegisters(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}, function byte) (string, error) {
	address, quantity, err := modbusAddressAndQuantity(params, 125)
	if err != nil {
		return "", err
	}
	response, err := t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		switch function {
		case goburrowmodbus.FuncCodeReadHoldingRegisters:
			return client.ReadHoldingRegisters(address, uint16(quantity))
		case goburrowmodbus.FuncCodeReadInputRegisters:
			return client.ReadInputRegisters(address, uint16(quantity))
		default:
			return nil, fmt.Errorf("unsupported register-read function 0x%02X", function)
		}
	})
	if err != nil {
		return "", fmt.Errorf("%s on target %q failed: %w", modbusFunctionName(function), targetName, err)
	}
	if len(response) != quantity*2 {
		return "", fmt.Errorf("%s on target %q returned %d data bytes, want %d", modbusFunctionName(function), targetName, len(response), quantity*2)
	}
	values := make([]uint16, quantity)
	for i := range values {
		values[i] = binary.BigEndian.Uint16(response[i*2 : 2+i*2])
	}
	return marshalModbusResult(map[string]interface{}{
		"success":  true,
		"action":   modbusFunctionName(function),
		"target":   targetName,
		"unit_id":  target.UnitID,
		"address":  address,
		"quantity": quantity,
		"values":   values,
	})
}

func (t *ModbusTool) writeSingleCoil(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}) (string, error) {
	address, err := modbusAddress(params)
	if err != nil {
		return "", err
	}
	value, err := modbusBoolValue(params["value"])
	if err != nil {
		return "", fmt.Errorf("value: %w", err)
	}
	encoded := uint16(0)
	if value {
		encoded = 0xFF00
	}
	_, err = t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		return client.WriteSingleCoil(address, encoded)
	})
	if err != nil {
		return "", fmt.Errorf("write_single_coil on target %q failed: %w", targetName, err)
	}
	return marshalModbusWriteResult("write_single_coil", targetName, target.UnitID, address, []interface{}{value})
}

func (t *ModbusTool) writeSingleRegister(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}) (string, error) {
	address, err := modbusAddress(params)
	if err != nil {
		return "", err
	}
	value, err := modbusIntegerValue(params["value"], 0, 65535)
	if err != nil {
		return "", fmt.Errorf("value: %w", err)
	}
	_, err = t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		return client.WriteSingleRegister(address, uint16(value))
	})
	if err != nil {
		return "", fmt.Errorf("write_single_register on target %q failed: %w", targetName, err)
	}
	return marshalModbusWriteResult("write_single_register", targetName, target.UnitID, address, []interface{}{value})
}

func (t *ModbusTool) writeMultipleCoils(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}) (string, error) {
	address, err := modbusAddress(params)
	if err != nil {
		return "", err
	}
	values, err := modbusBoolValues(params["values"])
	if err != nil {
		return "", fmt.Errorf("values: %w", err)
	}
	if len(values) < 1 || len(values) > 1968 {
		return "", fmt.Errorf("values must contain 1..1968 coils")
	}
	if err := validateModbusRange(address, len(values)); err != nil {
		return "", err
	}
	packed := make([]byte, (len(values)+7)/8)
	resultValues := make([]interface{}, len(values))
	for i, value := range values {
		resultValues[i] = value
		if value {
			packed[i/8] |= 1 << uint(i%8)
		}
	}
	_, err = t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		return client.WriteMultipleCoils(address, uint16(len(values)), packed)
	})
	if err != nil {
		return "", fmt.Errorf("write_multiple_coils on target %q failed: %w", targetName, err)
	}
	return marshalModbusWriteResult("write_multiple_coils", targetName, target.UnitID, address, resultValues)
}

func (t *ModbusTool) writeMultipleRegisters(ctx context.Context, targetName string, target ModbusTargetConfig, params map[string]interface{}) (string, error) {
	address, err := modbusAddress(params)
	if err != nil {
		return "", err
	}
	values, err := modbusRegisterValues(params["values"])
	if err != nil {
		return "", fmt.Errorf("values: %w", err)
	}
	if len(values) < 1 || len(values) > 123 {
		return "", fmt.Errorf("values must contain 1..123 registers")
	}
	if err := validateModbusRange(address, len(values)); err != nil {
		return "", err
	}
	encoded := make([]byte, len(values)*2)
	resultValues := make([]interface{}, len(values))
	for i, value := range values {
		binary.BigEndian.PutUint16(encoded[i*2:2+i*2], value)
		resultValues[i] = int(value)
	}
	_, err = t.executeModbusRequest(ctx, target, func(client goburrowmodbus.Client) ([]byte, error) {
		return client.WriteMultipleRegisters(address, uint16(len(values)), encoded)
	})
	if err != nil {
		return "", fmt.Errorf("write_multiple_registers on target %q failed: %w", targetName, err)
	}
	return marshalModbusWriteResult("write_multiple_registers", targetName, target.UnitID, address, resultValues)
}

func (t *ModbusTool) executeModbusRequest(
	ctx context.Context,
	target ModbusTargetConfig,
	request func(goburrowmodbus.Client) ([]byte, error),
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, closeClient, err := t.openModbusClient(ctx, target)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return request(client)
}

func (t *ModbusTool) openModbusClient(
	ctx context.Context,
	target ModbusTargetConfig,
) (goburrowmodbus.Client, func() error, error) {
	timeout, err := t.modbusRequestTimeout(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch target.Transport {
	case "tcp":
		handler := goburrowmodbus.NewTCPClientHandler(target.Address)
		handler.Timeout = timeout
		handler.SlaveId = byte(target.UnitID)
		if err := handler.Connect(); err != nil {
			return nil, nil, fmt.Errorf("connect Modbus TCP target: %w", err)
		}
		return goburrowmodbus.NewClient(handler), handler.Close, nil
	case "rtu":
		handler := goburrowmodbus.NewRTUClientHandler(target.Address)
		configureModbusSerialHandler(
			&handler.BaudRate,
			&handler.DataBits,
			&handler.Parity,
			&handler.StopBits,
			target,
		)
		handler.Timeout = timeout
		handler.SlaveId = byte(target.UnitID)
		if err := handler.Connect(); err != nil {
			return nil, nil, fmt.Errorf("connect Modbus RTU target: %w", err)
		}
		return goburrowmodbus.NewClient(handler), handler.Close, nil
	case "ascii":
		handler := goburrowmodbus.NewASCIIClientHandler(target.Address)
		configureModbusSerialHandler(
			&handler.BaudRate,
			&handler.DataBits,
			&handler.Parity,
			&handler.StopBits,
			target,
		)
		handler.Timeout = timeout
		handler.SlaveId = byte(target.UnitID)
		if err := handler.Connect(); err != nil {
			return nil, nil, fmt.Errorf("connect Modbus ASCII target: %w", err)
		}
		return goburrowmodbus.NewClient(handler), handler.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported normalized Modbus transport %q", target.Transport)
	}
}

func configureModbusSerialHandler(
	baudRate, dataBits *int,
	parity *string,
	stopBits *int,
	target ModbusTargetConfig,
) {
	*baudRate = target.BaudRate
	*dataBits = target.DataBits
	*parity = target.Parity
	*stopBits = target.StopBits
}

func (t *ModbusTool) modbusRequestTimeout(ctx context.Context) (time.Duration, error) {
	timeout := time.Duration(t.config.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	return timeout, nil
}

func modbusAddress(params map[string]interface{}) (uint16, error) {
	value, err := modbusRequiredInteger(params, "address")
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 65535 {
		return 0, fmt.Errorf("address must be between 0 and 65535")
	}
	return uint16(value), nil
}

func modbusAddressAndQuantity(params map[string]interface{}, maxQuantity int) (uint16, int, error) {
	address, err := modbusAddress(params)
	if err != nil {
		return 0, 0, err
	}
	quantity, err := modbusRequiredInteger(params, "quantity")
	if err != nil {
		return 0, 0, err
	}
	if quantity < 1 || quantity > maxQuantity {
		return 0, 0, fmt.Errorf("quantity must be between 1 and %d", maxQuantity)
	}
	if err := validateModbusRange(address, quantity); err != nil {
		return 0, 0, err
	}
	return address, quantity, nil
}

func validateModbusRange(address uint16, quantity int) error {
	if quantity < 1 || int(address)+quantity-1 > 65535 {
		return fmt.Errorf("address range exceeds Modbus address 65535")
	}
	return nil
}

func modbusRequiredInteger(params map[string]interface{}, key string) (int, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	parsed, err := modbusIntegerValue(value, math.MinInt32, math.MaxInt32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func modbusIntegerValue(value interface{}, minValue, maxValue int) (int, error) {
	var parsed int64
	switch typed := value.(type) {
	case int:
		parsed = int64(typed)
	case int8:
		parsed = int64(typed)
	case int16:
		parsed = int64(typed)
	case int32:
		parsed = int64(typed)
	case int64:
		parsed = typed
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return 0, fmt.Errorf("integer is too large")
		}
		parsed = int64(typed)
	case uint16:
		parsed = int64(typed)
	case uint32:
		parsed = int64(typed)
	case uint64:
		if typed > math.MaxInt64 {
			return 0, fmt.Errorf("integer is too large")
		}
		parsed = int64(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, fmt.Errorf("must be an integer")
		}
		parsed = int64(typed)
	case float32:
		asFloat := float64(typed)
		if math.IsNaN(asFloat) || math.IsInf(asFloat, 0) || math.Trunc(asFloat) != asFloat {
			return 0, fmt.Errorf("must be an integer")
		}
		parsed = int64(asFloat)
	case json.Number:
		var err error
		parsed, err = typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
	case string:
		var err error
		parsed, err = strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
	default:
		return 0, fmt.Errorf("must be an integer")
	}
	if parsed < int64(minValue) || parsed > int64(maxValue) {
		return 0, fmt.Errorf("must be between %d and %d", minValue, maxValue)
	}
	return int(parsed), nil
}

func ModbusIntegerValue(value interface{}, minValue, maxValue int) (int, error) {
	return modbusIntegerValue(value, minValue, maxValue)
}

func modbusBoolValue(value interface{}) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "on", "1":
			return true, nil
		case "false", "off", "0":
			return false, nil
		}
	case int:
		if typed == 0 || typed == 1 {
			return typed == 1, nil
		}
	case float64:
		if typed == 0 || typed == 1 {
			return typed == 1, nil
		}
	}
	return false, fmt.Errorf("must be boolean or 0/1")
}

func modbusBoolValues(value interface{}) ([]bool, error) {
	switch typed := value.(type) {
	case []bool:
		return append([]bool(nil), typed...), nil
	case []interface{}:
		values := make([]bool, len(typed))
		for i, item := range typed {
			parsed, err := modbusBoolValue(item)
			if err != nil {
				return nil, fmt.Errorf("item %d %w", i, err)
			}
			values[i] = parsed
		}
		return values, nil
	default:
		return nil, fmt.Errorf("must be an array of booleans or 0/1 values")
	}
}

func modbusRegisterValues(value interface{}) ([]uint16, error) {
	switch typed := value.(type) {
	case []uint16:
		return append([]uint16(nil), typed...), nil
	case []int:
		values := make([]uint16, len(typed))
		for i, item := range typed {
			if item < 0 || item > 65535 {
				return nil, fmt.Errorf("item %d must be between 0 and 65535", i)
			}
			values[i] = uint16(item)
		}
		return values, nil
	case []interface{}:
		values := make([]uint16, len(typed))
		for i, item := range typed {
			parsed, err := modbusIntegerValue(item, 0, 65535)
			if err != nil {
				return nil, fmt.Errorf("item %d %w", i, err)
			}
			values[i] = uint16(parsed)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("must be an array of integers")
	}
}

func marshalModbusWriteResult(action, target string, unitID int, address uint16, values []interface{}) (string, error) {
	return marshalModbusResult(map[string]interface{}{
		"success":  true,
		"action":   action,
		"target":   target,
		"unit_id":  unitID,
		"address":  address,
		"quantity": len(values),
		"values":   values,
	})
}

func marshalModbusResult(result map[string]interface{}) (string, error) {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Modbus result: %w", err)
	}
	return string(encoded), nil
}

func equalModbusBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func modbusFunctionName(function byte) string {
	switch function {
	case 0x01:
		return "read_coils"
	case 0x02:
		return "read_discrete_inputs"
	case 0x03:
		return "read_holding_registers"
	case 0x04:
		return "read_input_registers"
	case 0x05:
		return "write_single_coil"
	case 0x06:
		return "write_single_register"
	case 0x0F:
		return "write_multiple_coils"
	case 0x10:
		return "write_multiple_registers"
	default:
		return fmt.Sprintf("function_0x%02X", function)
	}
}

func modbusTargetLockKey(target ModbusTargetConfig) string {
	return target.Transport + "/" + strings.ToLower(target.Address) + "/" + strconv.Itoa(target.UnitID)
}

func optionalModbusInteger(params map[string]interface{}, key string, minValue, maxValue int) (int, error) {
	value, exists := params[key]
	if !exists || value == nil {
		return 0, nil
	}
	parsed, err := modbusIntegerValue(value, minValue, maxValue)
	if err != nil {
		return 0, fmt.Errorf("%s %w", key, err)
	}
	return parsed, nil
}
