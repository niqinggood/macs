package operations

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RemoteTool provides safe remote server operations: backup, upload, restart, exec.
// Safety rules: always backup before overwrite, reject destructive commands, support dry-run.
type RemoteTool struct {
	workspace        string
	dangerousCommand func(string) bool
}

func NewRemoteTool(workspace string, dangerousCommand func(string) bool) *RemoteTool {
	return &RemoteTool{workspace: workspace, dangerousCommand: dangerousCommand}
}

func (t *RemoteTool) Name() string { return "remote" }
func (t *RemoteTool) Description() string {
	return "Safely manage remote servers: backup directories, upload files, restart services, run commands. " +
		"SAFETY RULES: (1) Always backup before overwriting files. " +
		"(2) System-destructive commands (rm -rf /, disk formatting/writes, shutdown/reboot) are BLOCKED. " +
		"(3) Use dry_run=true to preview without executing. " +
		"Auth: SSH key (default) or password (passed per-call, never stored)."
}

func (t *RemoteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"deploy", "backup", "upload", "restart", "exec",
					"status", "check_connection",
				},
				"description": "Action: deploy(full deploy: backup→upload→restart), backup(tar dirs), upload(scp files), restart(systemctl/docker), exec(run command), status(service health), check_connection(test SSH)",
			},
			"host": map[string]interface{}{
				"type":        "string",
				"description": "Remote server hostname or IP",
			},
			"port": map[string]interface{}{
				"type":        "integer",
				"description": "SSH port (default: 22)",
			},
			"username": map[string]interface{}{
				"type":        "string",
				"description": "SSH username",
			},
			"password": map[string]interface{}{
				"type":        "string",
				"description": "SSH password (optional, uses key auth if empty). Passed per-call, never stored in logs.",
			},
			"local_path": map[string]interface{}{
				"type":        "string",
				"description": "Local file/directory path for upload",
			},
			"remote_path": map[string]interface{}{
				"type":        "string",
				"description": "Remote path for upload or backup target",
			},
			"backup_dirs": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated directories to backup before deploy (e.g. '/opt/app,/etc/nginx')",
			},
			"service": map[string]interface{}{
				"type":        "string",
				"description": "Service name for restart (systemctl or docker container)",
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Command to run on remote server (for exec action)",
			},
			"dry_run": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, show what would be done without actually doing it. Default: false.",
			},
		},
		"required": []string{"action", "host"},
	}
}

func (t *RemoteTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	host, _ := params["host"].(string)
	username, _ := params["username"].(string)
	password, _ := params["password"].(string)
	localPath, _ := params["local_path"].(string)
	remotePath, _ := params["remote_path"].(string)
	backupDirs, _ := params["backup_dirs"].(string)
	service, _ := params["service"].(string)
	command, _ := params["command"].(string)

	port := 22
	if p, ok := params["port"].(float64); ok && p > 0 {
		port = int(p)
	}

	dryRun := false
	if d, ok := params["dry_run"].(bool); ok {
		dryRun = d
	}

	if host == "" {
		return "", fmt.Errorf("host is required")
	}

	sshConn := buildSSHConn(host, port, username)

	switch action {
	case "check_connection":
		return t.checkConnection(ctx, sshConn, password, dryRun)
	case "backup":
		if backupDirs == "" {
			return "", fmt.Errorf("backup_dirs required (comma-separated paths)")
		}
		return t.backupDirs(ctx, sshConn, password, backupDirs, remotePath, dryRun)
	case "upload":
		if localPath == "" || remotePath == "" {
			return "", fmt.Errorf("local_path and remote_path required for upload")
		}
		return t.uploadFiles(ctx, sshConn, password, localPath, remotePath, dryRun)
	case "restart":
		if service == "" {
			return "", fmt.Errorf("service name required for restart")
		}
		return t.restartService(ctx, sshConn, password, service, dryRun)
	case "exec":
		if command == "" {
			return "", fmt.Errorf("command required for exec")
		}
		return t.execRemote(ctx, sshConn, password, command, dryRun)
	case "status":
		if service == "" {
			return "", fmt.Errorf("service name required for status check")
		}
		return t.checkStatus(ctx, sshConn, password, service, dryRun)
	case "deploy":
		if localPath == "" || remotePath == "" {
			return "", fmt.Errorf("local_path and remote_path required for deploy")
		}
		return t.fullDeploy(ctx, sshConn, password, localPath, remotePath, backupDirs, service, dryRun)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func buildSSHConn(host string, port int, username string) string {
	if username != "" {
		return fmt.Sprintf("%s@%s", username, host)
	}
	return host
}

func (t *RemoteTool) sshCmd(ctx context.Context, conn, password, command string) *exec.Cmd {
	if password != "" {
		// Use sshpass for password auth
		return exec.CommandContext(ctx, "sshpass", "-p", password, "ssh",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			conn, command)
	}
	return exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		conn, command)
}

func (t *RemoteTool) scpCmd(ctx context.Context, conn, password, src, dst string) *exec.Cmd {
	if password != "" {
		return exec.CommandContext(ctx, "sshpass", "-p", password, "scp",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-r", src, conn+":"+dst)
	}
	return exec.CommandContext(ctx, "scp",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-r", src, conn+":"+dst)
}

func (t *RemoteTool) checkConnection(ctx context.Context, conn, password string, dryRun bool) (string, error) {
	cmdStr := fmt.Sprintf("ssh %s 'echo OK && uname -a && uptime'", conn)
	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would run: %s", cmdStr), nil
	}
	cmd := t.sshCmd(ctx, conn, password, "echo OK && uname -a && uptime")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("SSH connection failed: %s\n%s", err, string(out))
	}
	return fmt.Sprintf("SSH connection OK:\n%s", strings.TrimSpace(string(out))), nil
}

func (t *RemoteTool) backupDirs(ctx context.Context, conn, password, dirs, remoteBase string, dryRun bool) (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	dirList := strings.Split(dirs, ",")

	var backupFiles []string
	for _, d := range dirList {
		d = strings.TrimSpace(d)
		if d == "" || d == "/" || d == "/root" || d == "~" {
			return "", fmt.Errorf("backup of '%s' is not allowed (too broad, risk of system dirs)", d)
		}
		dirname := strings.ReplaceAll(strings.TrimPrefix(d, "/"), "/", "_")
		if remoteBase == "" {
			remoteBase = "/tmp"
		}
		backupName := fmt.Sprintf("%s/backup_%s_%s.tar.gz", remoteBase, dirname, timestamp)
		backupFiles = append(backupFiles, backupName)
	}

	commands := []string{
		"echo '=== Starting backup ==='",
	}
	for i, d := range dirList {
		d = strings.TrimSpace(d)
		commands = append(commands, fmt.Sprintf(
			"tar czf %s %s 2>&1 && echo 'Backup OK: %s -> %s' || echo 'Backup FAILED: %s'",
			backupFiles[i], d, d, backupFiles[i], d,
		))
	}
	commands = append(commands, fmt.Sprintf("ls -lh %s", strings.Join(backupFiles, " ")))
	script := strings.Join(commands, " && ")

	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would backup:\n  Dirs: %s\n  To: %s\n  Script: %s", dirs, strings.Join(backupFiles, ", "), script), nil
	}

	cmd := t.sshCmd(ctx, conn, password, script)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Sprintf("Backup completed with warnings:\n%s", result), nil
	}
	return fmt.Sprintf("Backup OK:\n%s", result), nil
}

func (t *RemoteTool) uploadFiles(ctx context.Context, conn, password, localPath, remotePath string, dryRun bool) (string, error) {
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(t.workspace, localPath)
	}

	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would upload:\n  From: %s\n  To: %s:%s", localPath, conn, remotePath), nil
	}

	cmd := t.scpCmd(ctx, conn, password, localPath, remotePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("upload failed: %s\n%s", err, string(out))
	}
	return fmt.Sprintf("Upload OK: %s -> %s:%s\n%s", localPath, conn, remotePath, strings.TrimSpace(string(out))), nil
}

func (t *RemoteTool) restartService(ctx context.Context, conn, password, service string, dryRun bool) (string, error) {
	// Try systemctl first, then docker, then supervisor
	script := fmt.Sprintf(
		`if systemctl is-active %s >/dev/null 2>&1; then
  systemctl restart %s && echo "Restarted via systemctl: %s"
elif docker ps --filter name=%s --format '{{.Names}}' | grep -q . 2>/dev/null; then
  docker restart %s && echo "Restarted via docker: %s"
elif supervisorctl status %s >/dev/null 2>&1; then
  supervisorctl restart %s && echo "Restarted via supervisor: %s"
else
  echo "ERROR: Cannot find service '%s' (tried systemctl, docker, supervisor)"
  exit 1
fi`,
		service, service, service,
		service,
		service, service,
		service,
		service, service,
		service,
	)

	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would restart service: %s", service), nil
	}

	cmd := t.sshCmd(ctx, conn, password, script)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("restart failed: %s", result)
	}
	return result, nil
}

func (t *RemoteTool) execRemote(ctx context.Context, conn, password, command string, dryRun bool) (string, error) {
	if t.dangerousCommand == nil {
		return "", fmt.Errorf("remote command safety policy is unavailable")
	}
	if t.dangerousCommand(command) {
		return "", fmt.Errorf("BLOCKED: command '%s' matches dangerous pattern. Operation denied.", command)
	}

	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would execute on %s: %s", conn, command), nil
	}

	cmd := t.sshCmd(ctx, conn, password, command)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Sprintf("Command completed with error:\n%s", result), nil
	}
	if result == "" {
		return "Command completed (no output)", nil
	}
	return result, nil
}

func (t *RemoteTool) checkStatus(ctx context.Context, conn, password, service string, dryRun bool) (string, error) {
	script := fmt.Sprintf(
		`(systemctl status %s 2>/dev/null || docker ps --filter name=%s || echo "Service '%s' not found") | head -20`,
		service, service, service,
	)

	if dryRun {
		return fmt.Sprintf("[DRY RUN] Would check status of: %s", service), nil
	}

	cmd := t.sshCmd(ctx, conn, password, script)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), nil
}

func (t *RemoteTool) fullDeploy(ctx context.Context, conn, password, localPath, remotePath, backupDirs, service string, dryRun bool) (string, error) {
	var report strings.Builder

	if dryRun {
		report.WriteString(fmt.Sprintf("[DRY RUN] Full deploy to %s:\n", conn))
	} else {
		report.WriteString(fmt.Sprintf("Full deploy to %s:\n", conn))
	}

	// Step 1: Check connection
	report.WriteString("1. Checking connection...\n")
	result, err := t.checkConnection(ctx, conn, password, dryRun)
	if err != nil {
		return "", fmt.Errorf("connection check failed: %w", err)
	}
	report.WriteString(fmt.Sprintf("   %s\n", result))

	// Step 2: Backup if requested
	if backupDirs != "" {
		report.WriteString("2. Backing up...\n")
		result, err := t.backupDirs(ctx, conn, password, backupDirs, "", dryRun)
		if err != nil {
			return "", fmt.Errorf("backup failed (aborting deploy): %w", err)
		}
		report.WriteString(fmt.Sprintf("   %s\n", result))
	} else {
		report.WriteString("2. No backup_dirs specified, skipping backup.\n")
	}

	// Step 3: Upload
	report.WriteString("3. Uploading files...\n")
	result, err = t.uploadFiles(ctx, conn, password, localPath, remotePath, dryRun)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}
	report.WriteString(fmt.Sprintf("   %s\n", result))

	// Step 4: Restart if service specified
	if service != "" {
		report.WriteString("4. Restarting service...\n")
		result, err = t.restartService(ctx, conn, password, service, dryRun)
		if err != nil {
			return "", fmt.Errorf("restart failed: %w", err)
		}
		report.WriteString(fmt.Sprintf("   %s\n", result))

		// Verify service is running
		report.WriteString("5. Verifying service...\n")
		status, _ := t.checkStatus(ctx, conn, password, service, dryRun)
		report.WriteString(fmt.Sprintf("   %s\n", status))
	}

	if dryRun {
		report.WriteString("\n=== DRY RUN COMPLETE (no changes made) ===")
	} else {
		report.WriteString("\n=== DEPLOY COMPLETE ===")
	}

	return report.String(), nil
}
