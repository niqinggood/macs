package configvalue

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// GatewayEnvironment is the environment-overridable subset of application
// configuration used when the gateway starts.
type GatewayEnvironment struct {
	Workspace          string
	Headless           bool
	ListenAddr         string
	AgentOSListenAddr  string
	AgentOSMode        string
	MaxTasks           int
	MaxQueuedTasks     int
	TaskLeaseSeconds   int
	TaskPollIntervalMS int
}

func ApplyGatewayEnvironment(config *GatewayEnvironment, normalizeMode func(string) string) error {
	if config == nil {
		return nil
	}
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_WORKSPACE")); value != "" {
		config.Workspace = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_HEADLESS")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid OPENKITTY_HEADLESS %q", value)
		}
		config.Headless = parsed
	}
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_LISTEN_ADDR")); value != "" {
		config.ListenAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_AGENTOS_LISTEN_ADDR")); value != "" {
		config.AgentOSListenAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_AGENT_OS_MODE")); value != "" {
		mode := strings.ToLower(value)
		if normalizeMode != nil {
			mode = normalizeMode(value)
		}
		if mode != strings.ToLower(value) {
			return fmt.Errorf("invalid OPENKITTY_AGENT_OS_MODE %q", value)
		}
		config.AgentOSMode = mode
	}
	for _, item := range []struct {
		name   string
		target *int
		min    int
	}{
		{name: "OPENKITTY_MAX_TASKS", target: &config.MaxTasks, min: 1},
		{name: "OPENKITTY_MAX_QUEUED_TASKS", target: &config.MaxQueuedTasks, min: 1},
		{name: "OPENKITTY_TASK_LEASE_SECONDS", target: &config.TaskLeaseSeconds, min: 10},
		{name: "OPENKITTY_TASK_POLL_INTERVAL_MS", target: &config.TaskPollIntervalMS, min: 25},
	} {
		value := strings.TrimSpace(os.Getenv(item.name))
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < item.min {
			return fmt.Errorf("invalid %s %q: expected integer >= %d", item.name, value, item.min)
		}
		*item.target = parsed
	}
	return nil
}
