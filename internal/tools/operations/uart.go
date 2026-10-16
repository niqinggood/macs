package operations

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goburrowserial "github.com/goburrow/serial"
	"mac/internal/configvalue"
)

const (
	defaultUARTTimeoutMS     = 3000
	defaultUARTBaudRate      = 115200
	defaultUARTMaxReadBytes  = 4096
	defaultUARTMaxWriteBytes = 4096
	maxUARTTransferBytes     = 65536
)

const MaxUARTTransferBytes = maxUARTTransferBytes

// UARTToolConfig controls the generic UART transport tool. It intentionally
// contains no robot, PLC, or device-model semantics. Skills decide which bytes
// represent a model-specific operation.
type UARTToolConfig struct {
	Enabled       bool                        `json:"enabled" yaml:"enabled"`
	AllowWrite    bool                        `json:"allow_write" yaml:"allow_write"`
	TimeoutMS     int                         `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	MaxReadBytes  int                         `json:"max_read_bytes,omitempty" yaml:"max_read_bytes,omitempty"`
	MaxWriteBytes int                         `json:"max_write_bytes,omitempty" yaml:"max_write_bytes,omitempty"`
	Targets       map[string]UARTTargetConfig `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// UARTTargetConfig is one named serial endpoint. A named target keeps device
// paths out of model-visible tool results and gives writes a second permission
// gate. Direct ports remain available for trusted Skills and explicit calls.
type UARTTargetConfig struct {
	Port       string `json:"port" yaml:"port"`
	BaudRate   int    `json:"baud_rate,omitempty" yaml:"baud_rate,omitempty"`
	DataBits   int    `json:"data_bits,omitempty" yaml:"data_bits,omitempty"`
	Parity     string `json:"parity,omitempty" yaml:"parity,omitempty"`
	StopBits   int    `json:"stop_bits,omitempty" yaml:"stop_bits,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	AllowWrite bool   `json:"allow_write" yaml:"allow_write"`
}

func defaultUARTToolConfig() UARTToolConfig {
	return UARTToolConfig{
		Enabled:       false,
		AllowWrite:    false,
		TimeoutMS:     defaultUARTTimeoutMS,
		MaxReadBytes:  defaultUARTMaxReadBytes,
		MaxWriteBytes: defaultUARTMaxWriteBytes,
		Targets:       map[string]UARTTargetConfig{},
	}
}

func DefaultUARTToolConfig() UARTToolConfig { return defaultUARTToolConfig() }

func normalizeUARTToolConfig(config UARTToolConfig) (UARTToolConfig, error) {
	normalized := config
	if normalized.TimeoutMS == 0 {
		normalized.TimeoutMS = defaultUARTTimeoutMS
	}
	if normalized.TimeoutMS < 100 || normalized.TimeoutMS > 60000 {
		return UARTToolConfig{}, fmt.Errorf("timeout_ms must be between 100 and 60000")
	}
	if normalized.MaxReadBytes == 0 {
		normalized.MaxReadBytes = defaultUARTMaxReadBytes
	}
	if normalized.MaxReadBytes < 1 || normalized.MaxReadBytes > maxUARTTransferBytes {
		return UARTToolConfig{}, fmt.Errorf("max_read_bytes must be between 1 and %d", maxUARTTransferBytes)
	}
	if normalized.MaxWriteBytes == 0 {
		normalized.MaxWriteBytes = defaultUARTMaxWriteBytes
	}
	if normalized.MaxWriteBytes < 1 || normalized.MaxWriteBytes > maxUARTTransferBytes {
		return UARTToolConfig{}, fmt.Errorf("max_write_bytes must be between 1 and %d", maxUARTTransferBytes)
	}

	normalized.Targets = make(map[string]UARTTargetConfig, len(config.Targets))
	names := make([]string, 0, len(config.Targets))
	for rawName := range config.Targets {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return UARTToolConfig{}, fmt.Errorf("UART target name cannot be empty")
		}
		if name != rawName {
			return UARTToolConfig{}, fmt.Errorf("UART target name %q cannot have surrounding whitespace", rawName)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := config.Targets[name]
		if err := normalizeUARTTarget(&target, normalized.TimeoutMS); err != nil {
			return UARTToolConfig{}, fmt.Errorf("UART target %q: %w", name, err)
		}
		normalized.Targets[name] = target
	}
	return normalized, nil
}

func NormalizeUARTToolConfig(config UARTToolConfig) (UARTToolConfig, error) {
	return normalizeUARTToolConfig(config)
}

func normalizeUARTTarget(target *UARTTargetConfig, defaultTimeoutMS int) error {
	if target == nil {
		return fmt.Errorf("target is missing")
	}
	target.Port = strings.TrimSpace(target.Port)
	if target.Port == "" {
		return fmt.Errorf("port is empty")
	}
	if strings.ContainsAny(target.Port, "\r\n\x00") {
		return fmt.Errorf("port contains invalid control characters")
	}
	if target.BaudRate == 0 {
		target.BaudRate = defaultUARTBaudRate
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
		target.Parity = "N"
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
	if target.TimeoutMS == 0 {
		target.TimeoutMS = defaultTimeoutMS
	}
	if target.TimeoutMS < 100 || target.TimeoutMS > 60000 {
		return fmt.Errorf("timeout_ms must be between 100 and 60000")
	}
	return nil
}

type uartPort interface {
	io.Reader
	io.Writer
	io.Closer
}

type UARTPort = uartPort

type uartOpenFunc func(*goburrowserial.Config) (uartPort, error)

type UARTOpenFunc = uartOpenFunc

type UARTTool struct {
	config    UARTToolConfig
	configErr error
	openPort  uartOpenFunc
}

var uartTargetLocks sync.Map

func NewUARTTool(config UARTToolConfig) *UARTTool {
	normalized, err := normalizeUARTToolConfig(config)
	return &UARTTool{
		config:    normalized,
		configErr: err,
		openPort: func(serialConfig *goburrowserial.Config) (uartPort, error) {
			return goburrowserial.Open(serialConfig)
		},
	}
}

func (t *UARTTool) SetOpenPort(opener UARTOpenFunc) {
	if t != nil && opener != nil {
		t.openPort = opener
	}
}

func (t *UARTTool) Name() string { return "uart" }

func (t *UARTTool) Description() string {
	return "Perform generic bounded UART reads, writes, and request-response transactions. " +
		"This tool does not interpret device meaning such as move, stop, left, right, registers, or commands. " +
		"Use the exact target, payload, encoding, and response rules supplied by the active hardware Skill or user. " +
		"Writes require tools_config.uart.allow_write; configured targets also require their own allow_write gate."
}

func (t *UARTTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list_targets", "probe", "read", "write", "transact"},
				"description": "UART transport operation",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "Optional named target from tools_config.uart.targets. Use either target or port",
			},
			"port": map[string]interface{}{
				"type":        "string",
				"description": "Serial device path supplied by the active hardware Skill or user, such as /dev/ttyUSB0 or COM3",
			},
			"baud_rate": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     4000000,
				"description": "Direct-port baud rate; defaults to 115200",
			},
			"data_bits": map[string]interface{}{
				"type":    "integer",
				"minimum": 5,
				"maximum": 8,
			},
			"parity": map[string]interface{}{
				"type": "string",
				"enum": []string{"N", "E", "O"},
			},
			"stop_bits": map[string]interface{}{
				"type": "integer",
				"enum": []int{1, 2},
			},
			"timeout_ms": map[string]interface{}{
				"type":        "integer",
				"minimum":     100,
				"maximum":     60000,
				"description": "Optional timeout no greater than the configured target timeout",
			},
			"data": map[string]interface{}{
				"type":        "string",
				"description": "Payload for write or transact, encoded according to encoding",
			},
			"encoding": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"hex", "base64", "utf8"},
				"description": "Input payload encoding; defaults to hex",
			},
			"read_bytes": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxUARTTransferBytes,
				"description": "Maximum response bytes to read",
			},
			"read_exact": map[string]interface{}{
				"type":        "boolean",
				"description": "Wait for exactly read_bytes instead of returning after the first non-empty read",
			},
			"read_delay_ms": map[string]interface{}{
				"type":        "integer",
				"minimum":     0,
				"maximum":     60000,
				"description": "Delay between write and read for transact",
			},
			"response_encoding": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"hex", "base64", "utf8"},
				"description": "Response encoding; defaults to hex",
			},
		},
		"required": []string{"action"},
	}
}

func (t *UARTTool) IsReadOnly() bool      { return false }
func (t *UARTTool) IsNeverParallel() bool { return true }
func (t *UARTTool) MaxResultSize() int    { return 12000 }

func (t *UARTTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", fmt.Errorf("UART tool is unavailable")
	}
	if t.configErr != nil {
		return "", fmt.Errorf("UART configuration is invalid: %w", t.configErr)
	}
	if !t.config.Enabled {
		return "", fmt.Errorf("UART tool is disabled")
	}
	action := strings.ToLower(strings.TrimSpace(configvalue.String(params, "action", "")))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	if action == "list_targets" {
		return t.listTargets()
	}

	targetName, target, configuredTarget, err := t.resolveRequestTarget(params)
	if err != nil {
		return "", err
	}
	lockValue, _ := uartTargetLocks.LoadOrStore(uartTargetLockKey(target.Port), &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	switch action {
	case "probe":
		return t.probe(ctx, targetName, target, params)
	case "read":
		return t.read(ctx, targetName, target, params)
	case "write":
		if err := t.checkWriteAllowed(targetName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.write(ctx, targetName, target, params)
	case "transact":
		if err := t.checkWriteAllowed(targetName, target, configuredTarget); err != nil {
			return "", err
		}
		return t.transact(ctx, targetName, target, params)
	default:
		return "", fmt.Errorf("unsupported UART action %q", action)
	}
}

func (t *UARTTool) listTargets() (string, error) {
	names := make([]string, 0, len(t.config.Targets))
	for name := range t.config.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	targets := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		target := t.config.Targets[name]
		targets = append(targets, map[string]interface{}{
			"name":          name,
			"baud_rate":     target.BaudRate,
			"data_bits":     target.DataBits,
			"parity":        target.Parity,
			"stop_bits":     target.StopBits,
			"write_enabled": t.config.AllowWrite && target.AllowWrite,
		})
	}
	return marshalUARTResult(map[string]interface{}{
		"tool_enabled":  t.config.Enabled,
		"write_enabled": t.config.AllowWrite,
		"targets":       targets,
	})
}

func (t *UARTTool) resolveRequestTarget(params map[string]interface{}) (string, UARTTargetConfig, bool, error) {
	targetName := strings.TrimSpace(configvalue.String(params, "target", ""))
	port := strings.TrimSpace(configvalue.String(params, "port", ""))
	if targetName != "" && port != "" {
		return "", UARTTargetConfig{}, false, fmt.Errorf("use either target or port, not both")
	}
	if port == "" {
		if targetName == "" {
			return "", UARTTargetConfig{}, false, fmt.Errorf("configured target or port from the active hardware Skill is required")
		}
		name, target, err := t.resolveTarget(targetName)
		return name, target, true, err
	}

	target := UARTTargetConfig{
		Port:       port,
		Parity:     strings.TrimSpace(configvalue.String(params, "parity", "")),
		AllowWrite: true,
	}
	var err error
	if target.BaudRate, err = optionalUARTInteger(params, "baud_rate", 1, 4_000_000); err != nil {
		return "", UARTTargetConfig{}, false, err
	}
	if target.DataBits, err = optionalUARTInteger(params, "data_bits", 5, 8); err != nil {
		return "", UARTTargetConfig{}, false, err
	}
	if target.StopBits, err = optionalUARTInteger(params, "stop_bits", 1, 2); err != nil {
		return "", UARTTargetConfig{}, false, err
	}
	target.TimeoutMS = t.config.TimeoutMS
	if err := normalizeUARTTarget(&target, t.config.TimeoutMS); err != nil {
		return "", UARTTargetConfig{}, false, fmt.Errorf("direct UART port: %w", err)
	}
	return "direct", target, false, nil
}

func (t *UARTTool) resolveTarget(requested string) (string, UARTTargetConfig, error) {
	name := strings.TrimSpace(requested)
	target, ok := t.config.Targets[name]
	if !ok {
		for configuredName, configuredTarget := range t.config.Targets {
			if strings.EqualFold(configuredName, name) {
				name = configuredName
				target = configuredTarget
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", UARTTargetConfig{}, fmt.Errorf("UART target %q is not configured", requested)
	}
	return name, target, nil
}

func (t *UARTTool) checkWriteAllowed(targetName string, target UARTTargetConfig, configuredTarget bool) error {
	if !t.config.AllowWrite {
		return fmt.Errorf("UART writes are disabled; set tools_config.uart.allow_write=true")
	}
	if configuredTarget && !target.AllowWrite {
		return fmt.Errorf("UART target %q is read-only; set its allow_write=true", targetName)
	}
	return nil
}

func (t *UARTTool) probe(ctx context.Context, targetName string, target UARTTargetConfig, params map[string]interface{}) (string, error) {
	port, closePort, _, err := t.open(ctx, target, params)
	if err != nil {
		return "", fmt.Errorf("probe UART target %q: %w", targetName, err)
	}
	defer closePort()
	_ = port
	return marshalUARTResult(map[string]interface{}{
		"success": true,
		"action":  "probe",
		"target":  targetName,
		"open":    true,
	})
}

func (t *UARTTool) read(ctx context.Context, targetName string, target UARTTargetConfig, params map[string]interface{}) (string, error) {
	port, closePort, _, err := t.open(ctx, target, params)
	if err != nil {
		return "", fmt.Errorf("open UART target %q for read: %w", targetName, err)
	}
	defer closePort()
	data, err := t.readResponse(port, params)
	if err != nil {
		return "", fmt.Errorf("read UART target %q: %w", targetName, err)
	}
	return t.dataResult("read", targetName, 0, data, params)
}

func (t *UARTTool) write(ctx context.Context, targetName string, target UARTTargetConfig, params map[string]interface{}) (string, error) {
	payload, err := t.requestPayload(params)
	if err != nil {
		return "", err
	}
	port, closePort, _, err := t.open(ctx, target, params)
	if err != nil {
		return "", fmt.Errorf("open UART target %q for write: %w", targetName, err)
	}
	defer closePort()
	written, err := writeAllUART(port, payload)
	if err != nil {
		return "", fmt.Errorf("write UART target %q: %w", targetName, err)
	}
	return marshalUARTResult(map[string]interface{}{
		"success":       true,
		"action":        "write",
		"target":        targetName,
		"bytes_written": written,
	})
}

func (t *UARTTool) transact(ctx context.Context, targetName string, target UARTTargetConfig, params map[string]interface{}) (string, error) {
	payload, err := t.requestPayload(params)
	if err != nil {
		return "", err
	}
	port, closePort, _, err := t.open(ctx, target, params)
	if err != nil {
		return "", fmt.Errorf("open UART target %q for transact: %w", targetName, err)
	}
	defer closePort()
	written, err := writeAllUART(port, payload)
	if err != nil {
		return "", fmt.Errorf("write UART target %q: %w", targetName, err)
	}
	delayMS, err := optionalUARTInteger(params, "read_delay_ms", 0, 60000)
	if err != nil {
		return "", err
	}
	if delayMS > 0 {
		timer := time.NewTimer(time.Duration(delayMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	data, err := t.readResponse(port, params)
	if err != nil {
		return "", fmt.Errorf("read UART target %q after write: %w", targetName, err)
	}
	return t.dataResult("transact", targetName, written, data, params)
}

func (t *UARTTool) open(
	ctx context.Context,
	target UARTTargetConfig,
	params map[string]interface{},
) (uartPort, func(), time.Duration, error) {
	timeoutMS := target.TimeoutMS
	if raw, exists := params["timeout_ms"]; exists && raw != nil {
		requested, err := uartIntegerValue(raw, 100, target.TimeoutMS)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("timeout_ms %w", err)
		}
		timeoutMS = requested
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil, 0, context.DeadlineExceeded
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, 0, err
	}
	if t.openPort == nil {
		return nil, nil, 0, fmt.Errorf("UART port opener is unavailable")
	}
	port, err := t.openPort(&goburrowserial.Config{
		Address:  target.Port,
		BaudRate: target.BaudRate,
		DataBits: target.DataBits,
		StopBits: target.StopBits,
		Parity:   target.Parity,
		Timeout:  timeout,
	})
	if err != nil {
		return nil, nil, 0, err
	}
	return port, func() { _ = port.Close() }, timeout, nil
}

func (t *UARTTool) requestPayload(params map[string]interface{}) ([]byte, error) {
	raw, exists := params["data"]
	data, ok := raw.(string)
	if !exists || !ok || data == "" {
		return nil, fmt.Errorf("data is required for UART write or transact")
	}
	encoding := normalizeUARTEncoding(configvalue.String(params, "encoding", "hex"))
	payload, err := decodeUARTData(data, encoding)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("UART payload cannot be empty")
	}
	if len(payload) > t.config.MaxWriteBytes {
		return nil, fmt.Errorf("UART payload is %d bytes; configured maximum is %d", len(payload), t.config.MaxWriteBytes)
	}
	return payload, nil
}

func (t *UARTTool) readResponse(port uartPort, params map[string]interface{}) ([]byte, error) {
	readBytes := t.config.MaxReadBytes
	if raw, exists := params["read_bytes"]; exists && raw != nil {
		value, err := uartIntegerValue(raw, 1, t.config.MaxReadBytes)
		if err != nil {
			return nil, fmt.Errorf("read_bytes %w", err)
		}
		readBytes = value
	}
	buffer := make([]byte, readBytes)
	exact, _ := params["read_exact"].(bool)
	var (
		count int
		err   error
	)
	if exact {
		count, err = io.ReadFull(port, buffer)
	} else {
		count, err = port.Read(buffer)
	}
	if err != nil {
		if count == 0 {
			return nil, err
		}
		if exact {
			return nil, fmt.Errorf("received %d of %d requested bytes: %w", count, readBytes, err)
		}
	}
	return append([]byte(nil), buffer[:count]...), nil
}

func (t *UARTTool) dataResult(action, target string, written int, data []byte, params map[string]interface{}) (string, error) {
	encoding := normalizeUARTEncoding(configvalue.String(params, "response_encoding", "hex"))
	encoded, err := encodeUARTData(data, encoding)
	if err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"success":    true,
		"action":     action,
		"target":     target,
		"bytes_read": len(data),
		"encoding":   encoding,
		"data":       encoded,
	}
	if written > 0 {
		result["bytes_written"] = written
	}
	return marshalUARTResult(result)
}

func normalizeUARTEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "base64":
		return "base64"
	case "utf8", "utf-8", "text":
		return "utf8"
	default:
		return "hex"
	}
}

func decodeUARTData(data, encoding string) ([]byte, error) {
	switch encoding {
	case "hex":
		cleaned := strings.Map(func(char rune) rune {
			switch char {
			case ' ', '\t', '\r', '\n', ':', '-', ',':
				return -1
			default:
				return char
			}
		}, data)
		decoded, err := hex.DecodeString(cleaned)
		if err != nil {
			return nil, fmt.Errorf("data is not valid hex: %w", err)
		}
		return decoded, nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
		if err != nil {
			return nil, fmt.Errorf("data is not valid base64: %w", err)
		}
		return decoded, nil
	case "utf8":
		return []byte(data), nil
	default:
		return nil, fmt.Errorf("unsupported UART encoding %q", encoding)
	}
}

func encodeUARTData(data []byte, encoding string) (string, error) {
	switch encoding {
	case "hex":
		return strings.ToUpper(hex.EncodeToString(data)), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(data), nil
	case "utf8":
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported UART response encoding %q", encoding)
	}
}

func writeAllUART(writer io.Writer, payload []byte) (int, error) {
	total := 0
	for total < len(payload) {
		count, err := writer.Write(payload[total:])
		total += count
		if err != nil {
			return total, err
		}
		if count == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func optionalUARTInteger(params map[string]interface{}, key string, minimum, maximum int) (int, error) {
	value, exists := params[key]
	if !exists || value == nil {
		return 0, nil
	}
	parsed, err := uartIntegerValue(value, minimum, maximum)
	if err != nil {
		return 0, fmt.Errorf("%s %w", key, err)
	}
	return parsed, nil
}

func uartIntegerValue(value interface{}, minimum, maximum int) (int, error) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
		number = parsed
	default:
		return 0, fmt.Errorf("must be an integer")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return 0, fmt.Errorf("must be an integer")
	}
	if number < float64(minimum) || number > float64(maximum) {
		return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
	}
	return int(number), nil
}

func UARTIntegerValue(value interface{}, minimum, maximum int) (int, error) {
	return uartIntegerValue(value, minimum, maximum)
}

func uartTargetLockKey(port string) string {
	key := strings.TrimSpace(port)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func marshalUARTResult(result map[string]interface{}) (string, error) {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode UART result: %w", err)
	}
	return string(encoded), nil
}
