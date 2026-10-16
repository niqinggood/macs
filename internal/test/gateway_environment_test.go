package app

import "testing"

func TestApplyGatewayEnvironmentSupportsKubernetesRoleAndCapacity(t *testing.T) {
	t.Setenv("OPENKITTY_LISTEN_ADDR", "0.0.0.0:8080")
	t.Setenv("OPENKITTY_AGENTOS_LISTEN_ADDR", "0.0.0.0:8081")
	t.Setenv("OPENKITTY_WORKSPACE", "/workspace")
	t.Setenv("OPENKITTY_HEADLESS", "true")
	t.Setenv("OPENKITTY_AGENT_OS_MODE", "worker")
	t.Setenv("OPENKITTY_MAX_TASKS", "6")
	t.Setenv("OPENKITTY_MAX_QUEUED_TASKS", "250")
	t.Setenv("OPENKITTY_TASK_LEASE_SECONDS", "90")
	t.Setenv("OPENKITTY_TASK_POLL_INTERVAL_MS", "750")
	config := &Config{}
	if err := applyGatewayEnvironment(config); err != nil {
		t.Fatal(err)
	}
	if config.Workspace != "/workspace" || !config.Headless ||
		config.Gateway.ListenAddr != "0.0.0.0:8080" ||
		config.Gateway.AgentOSListenAddr != "0.0.0.0:8081" ||
		config.Gateway.AgentOSMode != "worker" ||
		config.Gateway.MaxTasks != 6 || config.Gateway.MaxQueuedTasks != 250 ||
		config.Gateway.TaskLeaseSeconds != 90 || config.Gateway.TaskPollIntervalMS != 750 {
		t.Fatalf("gateway config = %+v", config.Gateway)
	}
}

func TestApplyGatewayEnvironmentRejectsInvalidMode(t *testing.T) {
	t.Setenv("OPENKITTY_AGENT_OS_MODE", "fallback")
	if err := applyGatewayEnvironment(&Config{}); err == nil {
		t.Fatal("invalid AgentOS mode was accepted")
	}
}

func TestFixedGatewayPortForContainerAndKubernetes(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("OPENKITTY_FIXED_PORT", "true")
	if !fixedGatewayPort() {
		t.Fatal("container fixed-port setting was ignored")
	}
	t.Setenv("OPENKITTY_FIXED_PORT", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if !fixedGatewayPort() {
		t.Fatal("Kubernetes environment did not force a fixed port")
	}
}
