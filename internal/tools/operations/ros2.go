package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
)

const (
	defaultROS2TimeoutMS      = 10000
	defaultROS2MaxOutputBytes = 65536
	maxROS2OutputBytes        = 1048576
)

const MaxROS2OutputBytes = maxROS2OutputBytes

var (
	ros2ResourceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_~/]+$`)
	ros2TypeNamePattern     = regexp.MustCompile(`^[A-Za-z0-9_/]+$`)
)

// ROS2ToolConfig controls the generic ROS 2 CLI transport tool. The tool does
// not assign device meaning to topics, services, actions, or payloads; hardware
// Skills supply those model-specific details.
type ROS2ToolConfig struct {
	Enabled          bool     `json:"enabled" yaml:"enabled"`
	AllowPublish     bool     `json:"allow_publish" yaml:"allow_publish"`
	AllowServiceCall bool     `json:"allow_service_call" yaml:"allow_service_call"`
	AllowAction      bool     `json:"allow_action" yaml:"allow_action"`
	TimeoutMS        int      `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	MaxOutputBytes   int      `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	Command          string   `json:"command,omitempty" yaml:"command,omitempty"`
	DomainID         int      `json:"domain_id,omitempty" yaml:"domain_id,omitempty"`
	TopicAllowlist   []string `json:"topic_allowlist,omitempty" yaml:"topic_allowlist,omitempty"`
	ServiceAllowlist []string `json:"service_allowlist,omitempty" yaml:"service_allowlist,omitempty"`
	ActionAllowlist  []string `json:"action_allowlist,omitempty" yaml:"action_allowlist,omitempty"`
}

func defaultROS2ToolConfig() ROS2ToolConfig {
	return ROS2ToolConfig{
		Enabled:        false,
		TimeoutMS:      defaultROS2TimeoutMS,
		MaxOutputBytes: defaultROS2MaxOutputBytes,
		Command:        "ros2",
	}
}

func DefaultROS2ToolConfig() ROS2ToolConfig { return defaultROS2ToolConfig() }

func normalizeROS2ToolConfig(config ROS2ToolConfig) (ROS2ToolConfig, error) {
	normalized := config
	if normalized.TimeoutMS == 0 {
		normalized.TimeoutMS = defaultROS2TimeoutMS
	}
	if normalized.TimeoutMS < 100 || normalized.TimeoutMS > 300000 {
		return ROS2ToolConfig{}, fmt.Errorf("timeout_ms must be between 100 and 300000")
	}
	if normalized.MaxOutputBytes == 0 {
		normalized.MaxOutputBytes = defaultROS2MaxOutputBytes
	}
	if normalized.MaxOutputBytes < 1024 || normalized.MaxOutputBytes > maxROS2OutputBytes {
		return ROS2ToolConfig{}, fmt.Errorf("max_output_bytes must be between 1024 and %d", maxROS2OutputBytes)
	}
	normalized.Command = strings.TrimSpace(normalized.Command)
	if normalized.Command == "" {
		normalized.Command = "ros2"
	}
	if strings.ContainsAny(normalized.Command, "\r\n\x00") {
		return ROS2ToolConfig{}, fmt.Errorf("command contains invalid control characters")
	}
	if normalized.DomainID < 0 || normalized.DomainID > 232 {
		return ROS2ToolConfig{}, fmt.Errorf("domain_id must be between 0 and 232")
	}
	normalized.TopicAllowlist = normalizeROS2Allowlist(normalized.TopicAllowlist)
	normalized.ServiceAllowlist = normalizeROS2Allowlist(normalized.ServiceAllowlist)
	normalized.ActionAllowlist = normalizeROS2Allowlist(normalized.ActionAllowlist)
	return normalized, nil
}

func NormalizeROS2ToolConfig(config ROS2ToolConfig) (ROS2ToolConfig, error) {
	return normalizeROS2ToolConfig(config)
}

func normalizeROS2Allowlist(values []string) []string {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

type ros2RunFunc func(context.Context, string, []string, []string, int) (string, bool, error)

type ROS2RunFunc = ros2RunFunc

type ROS2Tool struct {
	config    ROS2ToolConfig
	configErr error
	run       ros2RunFunc
}

func NewROS2Tool(config ROS2ToolConfig) *ROS2Tool {
	normalized, err := normalizeROS2ToolConfig(config)
	return &ROS2Tool{
		config:    normalized,
		configErr: err,
		run:       runROS2Command,
	}
}

func (t *ROS2Tool) SetRunner(runner ROS2RunFunc) {
	if t != nil && runner != nil {
		t.run = runner
	}
}

func (t *ROS2Tool) Name() string { return "ros2" }

func (t *ROS2Tool) Description() string {
	return "Inspect and invoke ROS 2 graph resources through a bounded ros2 CLI adapter. " +
		"This tool does not decide what a topic, service, action, or payload means for a device. " +
		"Use only names, message types, and payloads supplied by the active hardware Skill or user. " +
		"Publishing, service calls, and action goals have separate tools_config.ros2 permission gates."
}

func (t *ROS2Tool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"status",
					"list_nodes",
					"list_topics",
					"list_services",
					"list_actions",
					"topic_info",
					"topic_echo",
					"topic_publish",
					"service_type",
					"service_call",
					"action_info",
					"action_send_goal",
				},
				"description": "ROS 2 graph or invocation operation",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Topic, service, or action name supplied by the active hardware Skill or user",
			},
			"message_type": map[string]interface{}{
				"type":        "string",
				"description": "ROS interface type, for example pkg/msg/Type, pkg/srv/Type, or pkg/action/Type",
			},
			"payload": map[string]interface{}{
				"description": "Message, service request, or action goal as an object or a ROS-compatible YAML/JSON string",
			},
			"verbose": map[string]interface{}{
				"type":        "boolean",
				"description": "Request verbose topic information",
			},
			"feedback": map[string]interface{}{
				"type":        "boolean",
				"description": "Print action feedback while waiting for the goal result",
			},
			"timeout_ms": map[string]interface{}{
				"type":        "integer",
				"minimum":     100,
				"maximum":     300000,
				"description": "Optional timeout no greater than tools_config.ros2.timeout_ms",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ROS2Tool) IsReadOnly() bool      { return false }
func (t *ROS2Tool) IsNeverParallel() bool { return true }
func (t *ROS2Tool) MaxResultSize() int    { return 20000 }

func (t *ROS2Tool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", fmt.Errorf("ROS2 tool is unavailable")
	}
	if t.configErr != nil {
		return "", fmt.Errorf("ROS2 configuration is invalid: %w", t.configErr)
	}
	if !t.config.Enabled {
		return "", fmt.Errorf("ROS2 tool is disabled")
	}
	action := strings.ToLower(strings.TrimSpace(configvalue.String(params, "action", "")))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	args, resourceName, err := t.buildArgs(action, params)
	if err != nil {
		return "", err
	}
	timeout, err := t.requestTimeout(ctx, params)
	if err != nil {
		return "", err
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if t.run == nil {
		return "", fmt.Errorf("ROS2 command runner is unavailable")
	}
	output, truncated, err := t.run(
		runContext,
		t.config.Command,
		args,
		ros2CommandEnvironment(t.config.DomainID),
		t.config.MaxOutputBytes,
	)
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return "", fmt.Errorf("ROS2 %s failed: %w; output: %s", action, err, strings.TrimSpace(output))
		}
		return "", fmt.Errorf("ROS2 %s failed: %w", action, err)
	}
	result := map[string]interface{}{
		"success":   true,
		"action":    action,
		"domain_id": t.config.DomainID,
		"output":    strings.TrimSpace(output),
	}
	if resourceName != "" {
		result["name"] = resourceName
	}
	if truncated {
		result["output_truncated"] = true
	}
	return marshalROS2Result(result)
}

func (t *ROS2Tool) buildArgs(action string, params map[string]interface{}) ([]string, string, error) {
	switch action {
	case "status", "list_nodes":
		return []string{"node", "list"}, "", nil
	case "list_topics":
		return []string{"topic", "list", "--show-types"}, "", nil
	case "list_services":
		return []string{"service", "list", "--show-types"}, "", nil
	case "list_actions":
		return []string{"action", "list", "--show-types"}, "", nil
	}

	name, err := ros2ResourceName(configvalue.String(params, "name", ""))
	if err != nil {
		return nil, "", err
	}
	switch action {
	case "topic_info":
		if err := t.requireAllowedROS2Name("topic", name, t.config.TopicAllowlist); err != nil {
			return nil, "", err
		}
		args := []string{"topic", "info", name}
		if verbose, _ := params["verbose"].(bool); verbose {
			args = append(args, "--verbose")
		}
		return args, name, nil
	case "topic_echo":
		if err := t.requireAllowedROS2Name("topic", name, t.config.TopicAllowlist); err != nil {
			return nil, "", err
		}
		args := []string{"topic", "echo", "--once", name}
		if rawType := strings.TrimSpace(configvalue.String(params, "message_type", "")); rawType != "" {
			messageType, err := ros2MessageType(rawType)
			if err != nil {
				return nil, "", err
			}
			args = append(args, messageType)
		}
		return args, name, nil
	case "topic_publish":
		if !t.config.AllowPublish {
			return nil, "", fmt.Errorf("ROS2 topic publishing is disabled; set tools_config.ros2.allow_publish=true")
		}
		if err := t.requireAllowedROS2Name("topic", name, t.config.TopicAllowlist); err != nil {
			return nil, "", err
		}
		messageType, err := ros2MessageType(configvalue.String(params, "message_type", ""))
		if err != nil {
			return nil, "", err
		}
		payload, err := ros2Payload(params["payload"])
		if err != nil {
			return nil, "", err
		}
		return []string{"topic", "pub", "--once", name, messageType, payload}, name, nil
	case "service_type":
		if err := t.requireAllowedROS2Name("service", name, t.config.ServiceAllowlist); err != nil {
			return nil, "", err
		}
		return []string{"service", "type", name}, name, nil
	case "service_call":
		if !t.config.AllowServiceCall {
			return nil, "", fmt.Errorf("ROS2 service calls are disabled; set tools_config.ros2.allow_service_call=true")
		}
		if err := t.requireAllowedROS2Name("service", name, t.config.ServiceAllowlist); err != nil {
			return nil, "", err
		}
		messageType, err := ros2MessageType(configvalue.String(params, "message_type", ""))
		if err != nil {
			return nil, "", err
		}
		payload, err := ros2Payload(params["payload"])
		if err != nil {
			return nil, "", err
		}
		return []string{"service", "call", name, messageType, payload}, name, nil
	case "action_info":
		if err := t.requireAllowedROS2Name("action", name, t.config.ActionAllowlist); err != nil {
			return nil, "", err
		}
		return []string{"action", "info", name}, name, nil
	case "action_send_goal":
		if !t.config.AllowAction {
			return nil, "", fmt.Errorf("ROS2 action goals are disabled; set tools_config.ros2.allow_action=true")
		}
		if err := t.requireAllowedROS2Name("action", name, t.config.ActionAllowlist); err != nil {
			return nil, "", err
		}
		messageType, err := ros2MessageType(configvalue.String(params, "message_type", ""))
		if err != nil {
			return nil, "", err
		}
		payload, err := ros2Payload(params["payload"])
		if err != nil {
			return nil, "", err
		}
		args := []string{"action", "send_goal", name, messageType, payload}
		if feedback, _ := params["feedback"].(bool); feedback {
			args = append(args, "--feedback")
		}
		return args, name, nil
	default:
		return nil, "", fmt.Errorf("unsupported ROS2 action %q", action)
	}
}

func (t *ROS2Tool) requireAllowedROS2Name(kind, name string, allowlist []string) error {
	if ros2NameAllowed(name, allowlist) {
		return nil
	}
	return fmt.Errorf("ROS2 %s %q is not allowed by tools_config.ros2.%s_allowlist", kind, name, kind)
}

func ros2NameAllowed(name string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, allowed := range allowlist {
		if allowed == "*" || allowed == name {
			return true
		}
		if strings.HasSuffix(allowed, "/**") {
			prefix := strings.TrimSuffix(allowed, "**")
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func ros2ResourceName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("name is required")
	}
	if len(value) > 256 || !ros2ResourceNamePattern.MatchString(value) || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("invalid ROS2 resource name %q", value)
	}
	return value, nil
}

func ros2MessageType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("message_type is required")
	}
	if len(value) > 256 || !ros2TypeNamePattern.MatchString(value) || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("invalid ROS2 message_type %q", value)
	}
	return value, nil
}

func ros2Payload(value interface{}) (string, error) {
	if value == nil {
		return "{}", nil
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return "{}", nil
		}
		if len(text) > 65536 {
			return "", fmt.Errorf("ROS2 payload exceeds 65536 characters")
		}
		return text, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode ROS2 payload: %w", err)
	}
	if len(encoded) > 65536 {
		return "", fmt.Errorf("ROS2 payload exceeds 65536 bytes")
	}
	return string(encoded), nil
}

func (t *ROS2Tool) requestTimeout(ctx context.Context, params map[string]interface{}) (time.Duration, error) {
	timeoutMS := t.config.TimeoutMS
	if raw, exists := params["timeout_ms"]; exists && raw != nil {
		requested, err := uartIntegerValue(raw, 100, t.config.TimeoutMS)
		if err != nil {
			return 0, fmt.Errorf("timeout_ms %w", err)
		}
		timeoutMS = requested
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
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

func ros2CommandEnvironment(domainID int) []string {
	key := "ROS_DOMAIN_ID="
	value := key + strconv.Itoa(domainID)
	environment := os.Environ()
	for index, entry := range environment {
		if strings.HasPrefix(entry, key) {
			environment[index] = value
			return environment
		}
	}
	return append(environment, value)
}

type cappedROS2Buffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *cappedROS2Buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(data)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(data) > remaining {
			b.data = append(b.data, data[:remaining]...)
		} else {
			b.data = append(b.data, data...)
		}
	}
	if originalLength > remaining {
		b.truncated = true
	}
	return originalLength, nil
}

func (b *cappedROS2Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

func runROS2Command(
	ctx context.Context,
	command string,
	args []string,
	environment []string,
	maxOutputBytes int,
) (string, bool, error) {
	process := exec.CommandContext(ctx, command, args...)
	process.Env = environment
	output := &cappedROS2Buffer{limit: maxOutputBytes}
	process.Stdout = output
	process.Stderr = output
	err := process.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return output.String(), output.truncated, err
}

func marshalROS2Result(result map[string]interface{}) (string, error) {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode ROS2 result: %w", err)
	}
	return string(encoded), nil
}
