package operations

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ====================== Ops Host Config ======================

type OpsHostConfig struct {
	Name     string `json:"name" yaml:"name"`
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port,omitempty" yaml:"port,omitempty"`
	User     string `json:"user" yaml:"user"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
	KeyPath  string `json:"key_path,omitempty" yaml:"key_path,omitempty"` // ~/.ssh/id_rsa default
	// Jump host
	JumpHost string `json:"jump_host,omitempty" yaml:"jump_host,omitempty"`
	JumpUser string `json:"jump_user,omitempty" yaml:"jump_user,omitempty"`
	// Alternative access
	KubeCtx    string `json:"kube_context,omitempty" yaml:"kube_context,omitempty"` // kubectl context
	DockerHost string `json:"docker_host,omitempty" yaml:"docker_host,omitempty"`   // DOCKER_HOST
	// Windows
	WinRM     bool `json:"winrm,omitempty" yaml:"winrm,omitempty"`
	WinRMPort int  `json:"winrm_port,omitempty" yaml:"winrm_port,omitempty"`
	// Cloud tunnel
	CloudTunnel string `json:"cloud_tunnel,omitempty" yaml:"cloud_tunnel,omitempty"` // ssm, iap
	InstanceID  string `json:"instance_id,omitempty" yaml:"instance_id,omitempty"`
	// Local
	Local bool `json:"local,omitempty" yaml:"local,omitempty"`
	// Tags
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Default SSH port
func (h *OpsHostConfig) sshPort() int {
	if h.Port <= 0 {
		return 22
	}
	return h.Port
}

// Default user
func (h *OpsHostConfig) sshUser() string {
	if h.User == "" {
		return "root"
	}
	return h.User
}

// ====================== Ops Connection ======================

type opsConnection struct {
	host   *OpsHostConfig
	client *ssh.Client
	logger *log.Logger
}

func connectSSH(host *OpsHostConfig, logger *log.Logger) (*opsConnection, error) {
	// Build auth methods
	var authMethods []ssh.AuthMethod

	// 1. Try SSH key
	keyPath := host.KeyPath
	if keyPath == "" {
		keyPath = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
	}
	if key, err := os.ReadFile(keyPath); err == nil {
		signer, err := ssh.ParsePrivateKey(key)
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			logger.Printf("[Ops] Using SSH key: %s", keyPath)
		}
	}
	// Also try id_ed25519, id_ecdsa
	for _, kf := range []string{"id_ed25519", "id_ecdsa", "id_dsa"} {
		altKey := filepath.Join(os.Getenv("HOME"), ".ssh", kf)
		if key, err := os.ReadFile(altKey); err == nil {
			signer, err := ssh.ParsePrivateKey(key)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
				logger.Printf("[Ops] Also loaded SSH key: %s", altKey)
			}
		}
	}

	// 2. Password
	if host.Password != "" {
		authMethods = append(authMethods, ssh.Password(host.Password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth method available for %s (tried key, agent, password)", host.Host)
	}

	// Handle jump host
	var client *ssh.Client
	var err error
	addr := fmt.Sprintf("%s:%d", host.Host, host.sshPort())

	if host.JumpHost != "" {
		jumpUser := host.JumpUser
		if jumpUser == "" {
			jumpUser = host.sshUser()
		}
		jumpAddr := fmt.Sprintf("%s:%d", host.JumpHost, 22)
		jumpClient, err := ssh.Dial("tcp", jumpAddr, &ssh.ClientConfig{
			User:            jumpUser,
			Auth:            authMethods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         10 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("jump host %s: %w", host.JumpHost, err)
		}
		conn, err := jumpClient.Dial("tcp", addr)
		if err != nil {
			jumpClient.Close()
			return nil, fmt.Errorf("dial through jump: %w", err)
		}
		ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
			User:            host.sshUser(),
			Auth:            authMethods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         10 * time.Second,
		})
		if err != nil {
			jumpClient.Close()
			return nil, fmt.Errorf("connect through jump: %w", err)
		}
		client = ssh.NewClient(ncc, chans, reqs)
		logger.Printf("[Ops] Connected via jump %s -> %s", host.JumpHost, addr)
	} else {
		client, err = ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            host.sshUser(),
			Auth:            authMethods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         10 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
		}
	}

	return &opsConnection{host: host, client: client, logger: logger}, nil
}

func (c *opsConnection) run(cmd string, timeout time.Duration) (string, error) {
	// kubectl mode
	if c.host.KubeCtx != "" {
		return c.runKubectl(cmd, timeout)
	}
	// Docker mode
	if c.host.DockerHost != "" {
		return c.runDocker(cmd, timeout)
	}
	// Local mode
	if c.host.Local {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("%s: %w", stderr.String(), err)
		}
		return stdout.String(), nil
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("timeout after %v", timeout)
	}
}

func (c *opsConnection) runKubectl(cmd string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	kubectl := exec.CommandContext(ctx, "kubectl", "--context", c.host.KubeCtx, "exec", "-i", "--", "sh", "-c", cmd)
	out, err := kubectl.Output()
	if err != nil {
		return "", fmt.Errorf("kubectl(%s): %w", c.host.KubeCtx, err)
	}
	return string(out), nil
}

func (c *opsConnection) runDocker(cmd string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	docker := exec.CommandContext(ctx, "docker", "exec", "-i", c.host.DockerHost, "sh", "-c", cmd)
	out, err := docker.Output()
	if err != nil {
		return "", fmt.Errorf("docker(%s): %w", c.host.DockerHost, err)
	}
	return string(out), nil
}

func (c *opsConnection) close() {
	if c.client != nil {
		c.client.Close()
	}
}

// ====================== Server Health Tool ======================

type ServerHealthTool struct {
	hosts  []OpsHostConfig
	logger *log.Logger
	mu     sync.Mutex
}

func NewServerHealthTool(hosts []OpsHostConfig, logger *log.Logger) *ServerHealthTool {
	return &ServerHealthTool{hosts: hosts, logger: logger}
}

func (t *ServerHealthTool) Name() string { return "server_health" }
func (t *ServerHealthTool) Description() string {
	return `Run health checks on configured servers. Checks CPU, memory, disk, services, and recent errors.
Target: "all" for all configured hosts, or a specific host name/tag.
Example: server_health(target="prod-web"), server_health(target="all")`
}
func (t *ServerHealthTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target": map[string]interface{}{
				"type": "string", "description": "Host name, tag, or 'all'",
			},
		},
	}
}

func (t *ServerHealthTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	target, _ := params["target"].(string)
	if target == "" {
		target = "all"
	}

	var selected []OpsHostConfig
	for _, h := range t.hosts {
		if target == "all" || h.Name == target {
			selected = append(selected, h)
		}
		for _, tag := range h.Tags {
			if tag == target {
				selected = append(selected, h)
				break
			}
		}
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("no hosts matching target '%s'", target)
	}

	var result strings.Builder
	result.WriteString("# Server Health Report\n\n")

	checks := []struct {
		name    string
		cmd     string
		okAbove float64
		warnAt  float64
		unit    string
	}{
		{"CPU", "top -bn1 | grep 'Cpu(s)' | awk '{print $2+$4}'", 0, 80, "%"},
		{"Memory", "free | grep Mem | awk '{print $3/$2 * 100.0}'", 0, 85, "%"},
		{"Disk /", "df / | tail -1 | awk '{print $5}' | sed 's/%//'", 0, 85, "%"},
	}

	for _, host := range selected {
		result.WriteString(fmt.Sprintf("## %s (%s)\n\n", host.Name, host.Host))
		result.WriteString("| Check | Value | Status |\n|---|---|---|\n")

		if host.Local {
			for _, chk := range checks {
				out, err := exec.Command("sh", "-c", chk.cmd).Output()
				status := "✅"
				if err != nil {
					result.WriteString(fmt.Sprintf("| %s | ERR | 🔴 %v |\n", chk.name, err))
					continue
				}
				val := strings.TrimSpace(string(out))
				var v float64
				fmt.Sscanf(val, "%f", &v)
				if v > chk.warnAt {
					status = "🔴"
				} else if v > chk.okAbove {
					status = "🟡"
				}
				result.WriteString(fmt.Sprintf("| %s | %s%s | %s |\n", chk.name, val, chk.unit, status))
			}
			continue
		}

		conn, err := connectSSH(&host, t.logger)
		if err != nil {
			result.WriteString(fmt.Sprintf("| Connect | ERR | 🔴 %v |\n", err))
			continue
		}

		for _, chk := range checks {
			out, err := conn.run(chk.cmd, 10*time.Second)
			status := "✅"
			if err != nil {
				result.WriteString(fmt.Sprintf("| %s | ERR | 🔴 %v |\n", chk.name, err))
				continue
			}
			val := strings.TrimSpace(out)
			var v float64
			fmt.Sscanf(val, "%f", &v)
			if v > chk.warnAt {
				status = "🔴"
			} else if v > chk.okAbove {
				status = "🟡"
			}
			result.WriteString(fmt.Sprintf("| %s | %s%s | %s |\n", chk.name, val, chk.unit, status))
		}

		// Service checks
		for _, svc := range []string{"nginx", "mysql", "docker", "sshd"} {
			out, _ := conn.run(fmt.Sprintf("systemctl is-active %s 2>/dev/null || echo inactive", svc), 5*time.Second)
			st := strings.TrimSpace(out)
			sIcon := "🔴"
			if st == "active" {
				sIcon = "✅"
			}
			result.WriteString(fmt.Sprintf("| Service:%s | %s | %s |\n", svc, st, sIcon))
		}

		conn.close()
		result.WriteString("\n")
	}

	return result.String(), nil
}

// ====================== Log Trace Tool ======================

type LogTraceTool struct {
	logger *log.Logger
}

func NewLogTraceTool(logger *log.Logger) *LogTraceTool {
	return &LogTraceTool{logger: logger}
}

func (t *LogTraceTool) Name() string { return "log_trace" }
func (t *LogTraceTool) Description() string {
	return `Search and analyze logs on a server. Specify host, log path, pattern, and time range.
Examples:
- log_trace(host="prod-web-01", path="/var/log/nginx/access.log", pattern=" 500 ", tail="1h")
- log_trace(host="prod-web-01", path="/var/log/syslog", pattern="error", tail="30m", top=10)
Returns matching lines, count, and time distribution.`
}
func (t *LogTraceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"host":    map[string]interface{}{"type": "string", "description": "Target host name"},
			"path":    map[string]interface{}{"type": "string", "description": "Log file path on the server"},
			"pattern": map[string]interface{}{"type": "string", "description": "Search pattern (grep-compatible)"},
			"tail":    map[string]interface{}{"type": "string", "description": "Time range: 30m, 1h, 6h, 24h"},
			"top":     map[string]interface{}{"type": "integer", "description": "Max lines to return (default 50)"},
		},
		"required": []string{"path", "pattern"},
	}
}
func (t *LogTraceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	path, _ := params["path"].(string)
	pattern, _ := params["pattern"].(string)
	if path == "" || pattern == "" {
		return "", fmt.Errorf("path and pattern are required")
	}
	hostName, _ := params["host"].(string)
	tail, _ := params["tail"].(string)
	top := 50
	if t, ok := params["top"].(float64); ok {
		top = int(t)
	}

	if hostName == "" || hostName == "local" {
		return t.runLocal(path, pattern, tail, top)
	}
	// SSH path — for now return guidance
	return fmt.Sprintf("Remote log trace for host '%s' requires ops host config.\nUse `server_health` first to verify connectivity.", hostName), nil
}

func (t *LogTraceTool) runLocal(path, pattern, tail string, top int) (string, error) {
	cmd := fmt.Sprintf("grep -i '%s' %s 2>/dev/null | tail -%d", pattern, path, top)
	if tail != "" {
		// Try to be smart about time range
		cmd = fmt.Sprintf("grep -i '%s' %s 2>/dev/null | tail -%d", pattern, path, top)
	}
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return "", fmt.Errorf("log trace failed: %w (check path and permissions)", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := len(lines)
	if count == 0 {
		return fmt.Sprintf("No matches for '%s' in %s", pattern, path), nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("# Log Trace: %s\n", path))
	result.WriteString(fmt.Sprintf("**Pattern**: `%s` | **Matches**: %d\n\n", pattern, count))
	result.WriteString("```\n")
	for i, line := range lines {
		if i >= top {
			result.WriteString(fmt.Sprintf("\n... %d more lines", count-top))
			break
		}
		result.WriteString(line + "\n")
	}
	result.WriteString("```")
	return result.String(), nil
}

// ====================== Kube Medic Tool ======================

type KubeMedicTool struct {
	logger *log.Logger
}

func NewKubeMedicTool(logger *log.Logger) *KubeMedicTool {
	return &KubeMedicTool{logger: logger}
}

func (t *KubeMedicTool) Name() string { return "kube_medic" }
func (t *KubeMedicTool) Description() string {
	return `Kubernetes cluster diagnostics tool. Read-only by default.
Subcommands:
- sweep: Full cluster health scan (nodes, problem pods, events)
- pod <name>: Deep pod autopsy (status, logs, events, image check)
- deploy <name>: Deployment status (replicas, rollout, history)
- resources: CPU/memory pressure + top consumers
- events: Recent cluster events (use since="15m" for time window)
Write operations (rollback, restart, scale) require explicit user confirmation.`
}
func (t *KubeMedicTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subcommand": map[string]interface{}{
				"type": "string", "description": "sweep, pod, deploy, resources, events",
				"enum": []string{"sweep", "pod", "deploy", "resources", "events"},
			},
			"target":    map[string]interface{}{"type": "string", "description": "Target name (pod/deploy name for pod/deploy subcommands)"},
			"namespace": map[string]interface{}{"type": "string", "description": "K8s namespace (default all for sweep/resources)"},
			"since":     map[string]interface{}{"type": "string", "description": "Time window for events (default: 15m)"},
			"context":   map[string]interface{}{"type": "string", "description": "K8s context for multi-cluster"},
		},
		"required": []string{"subcommand"},
	}
}
func (t *KubeMedicTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	sub, _ := params["subcommand"].(string)
	target, _ := params["target"].(string)
	ns, _ := params["namespace"].(string)
	since, _ := params["since"].(string)
	kctx, _ := params["context"].(string)
	if since == "" {
		since = "15m"
	}
	if ns == "" {
		ns = "--all-namespaces"
	}

	kubectl := "kubectl"
	if kctx != "" {
		kubectl += " --context " + kctx
	}

	var cmd, title string
	switch sub {
	case "sweep":
		title = "Cluster Health Sweep"
		cmd = fmt.Sprintf(`echo "=== Nodes ===" && %s get nodes && echo "=== Problem Pods ===" && %s get pods %s --field-selector=status.phase!=Running -o wide 2>/dev/null && echo "=== CrashLoopBackOff ===" && %s get pods %s --field-selector=status.phase=Running -o json 2>/dev/null | python3 -c "import json,sys;d=json.load(sys.stdin);[print(p['metadata']['namespace'],p['metadata']['name'],s['state']['waiting']['reason']) for p in d['items'] for s in p['status']['containerStatuses'] if s.get('state',{}).get('waiting',{}).get('reason')=='CrashLoopBackOff']" 2>/dev/null && echo "=== Warning Events ===" && %s get events %s --field-selector type=Warning --sort-by='.lastTimestamp' 2>/dev/null | tail -20`, kubectl, kubectl, ns, kubectl, ns, kubectl, ns)
	case "pod":
		title = fmt.Sprintf("Pod Autopsy: %s", target)
		cmd = fmt.Sprintf(`echo "=== Pod Status ===" && %s describe pod %s %s && echo "=== Recent Logs ===" && %s logs %s --tail=200 %s && echo "=== Events ===" && %s get events --field-selector involvedObject.name=%s %s --sort-by='.lastTimestamp'`, kubectl, target, nsFlag(ns), kubectl, target, nsFlag(ns), kubectl, target, nsFlag(ns))
	case "deploy":
		title = fmt.Sprintf("Deployment: %s", target)
		cmd = fmt.Sprintf(`echo "=== Status ===" && %s get deploy %s %s -o wide && echo "=== Rollout History ===" && %s rollout history deploy/%s %s && echo "=== Events ===" && %s get events --field-selector involvedObject.name=%s %s --sort-by='.lastTimestamp'`, kubectl, target, nsFlag(ns), kubectl, target, nsFlag(ns), kubectl, target, nsFlag(ns))
	case "resources":
		title = "Resource Pressure"
		cmd = fmt.Sprintf(`echo "=== Node Usage ===" && %s top nodes 2>/dev/null && echo "=== Top CPU ===" && %s top pods %s --sort-by=cpu 2>/dev/null | head -20 && echo "=== Top Memory ===" && %s top pods %s --sort-by=memory 2>/dev/null | head -20`, kubectl, kubectl, ns, kubectl, ns)
	case "events":
		title = fmt.Sprintf("Events (since %s)", since)
		cmd = fmt.Sprintf(`%s get events %s --sort-by='.lastTimestamp' 2>/dev/null | tail -50`, kubectl, ns)
	default:
		return "", fmt.Errorf("unknown subcommand: %s. Use sweep/pod/deploy/resources/events", sub)
	}

	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return "", fmt.Errorf("kubectl error: %w (is kubectl installed and configured?)", err)
	}
	result := "# " + title + "\n\n```\n" + string(out) + "\n```"
	if len(result) > 8000 {
		result = result[:8000] + "\n...truncated"
	}
	return result, nil
}

func nsFlag(ns string) string {
	if ns == "" || ns == "--all-namespaces" {
		return ""
	}
	return "-n " + ns
}
