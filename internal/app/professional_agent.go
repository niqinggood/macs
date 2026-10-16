package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mac/internal/textutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	pipelinecore "mac/internal/pipeline"
	"mac/internal/security"
)

const professionalAgentProtocol = "openkitty.professional-agent/v1"

const (
	// A professional-agent process may return up to the manifest output limit,
	// but a failed response is copied into several durable observability
	// surfaces. Keep those copies useful and bounded independently of the
	// process transport cap.
	professionalAgentStepOutputMaxBytes = 32 * 1024
	professionalAgentToolResultMaxBytes = 6000
	professionalAgentErrorMaxBytes      = 1000
	professionalAgentProgressMaxBytes   = 1600
)

// professionalAgentReportedFailure distinguishes a valid protocol response
// whose business status is failed from transport, decoding, and validation
// errors. Pipeline policy must not turn this hard terminal state into a
// continue-on-error degradation or a successful fallback.
type professionalAgentReportedFailure struct {
	message string
}

func (e *professionalAgentReportedFailure) Error() string {
	if e == nil {
		return "professional agent reported failure"
	}
	return e.message
}

// ExecutionPrincipal is persisted with a pipeline execution. It records who
// owns the run, but it is not itself an authorization token.
type ExecutionPrincipal struct {
	Kind       string `json:"kind"`
	EmployeeID string `json:"employee_id,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`
}

type employeeExecutionGrant struct {
	Principal ExecutionPrincipal
	seal      *struct{}
}

type employeeExecutionGrantContextKey struct{}

var employeeExecutionGrantSeal = &struct{}{}

func withEmployeeExecutionGrant(ctx context.Context, employeeID, tenantID string) context.Context {
	employeeID = strings.TrimSpace(employeeID)
	if employeeID == "" {
		return ctx
	}
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "default"
	}
	grant := employeeExecutionGrant{
		Principal: ExecutionPrincipal{Kind: "employee", EmployeeID: employeeID, TenantID: tenantID},
		seal:      employeeExecutionGrantSeal,
	}
	return context.WithValue(ctx, employeeExecutionGrantContextKey{}, grant)
}

func employeeExecutionPrincipal(ctx context.Context) (ExecutionPrincipal, bool) {
	grant, ok := ctx.Value(employeeExecutionGrantContextKey{}).(employeeExecutionGrant)
	if !ok || grant.seal != employeeExecutionGrantSeal || grant.Principal.Kind != "employee" || strings.TrimSpace(grant.Principal.EmployeeID) == "" {
		return ExecutionPrincipal{}, false
	}
	return grant.Principal, true
}

// ProfessionalAgentStep invokes an isolated process from professional_agents.
// It deliberately is not a Tool and is therefore invisible to the main agent.
type ProfessionalAgentStep = pipelinecore.ProfessionalAgentStep

type ProfessionalAgentManifest struct {
	Protocol          string                             `json:"protocol"`
	ID                string                             `json:"id"`
	Version           string                             `json:"version,omitempty"`
	Command           string                             `json:"command"`
	Args              []string                           `json:"args,omitempty"`
	AllowedEmployees  []string                           `json:"allowed_employees"`
	Capabilities      []string                           `json:"capabilities"`
	Environment       []string                           `json:"environment,omitempty"`
	ModelCredentials  []ProfessionalAgentModelCredential `json:"model_credentials,omitempty"`
	MaxOutputBytes    int                                `json:"max_output_bytes,omitempty"`
	MaxTimeoutSeconds int                                `json:"max_timeout_seconds,omitempty"`
}

type ProfessionalAgentRequest struct {
	Protocol    string `json:"protocol"`
	RequestID   string `json:"request_id"`
	AgentID     string `json:"agent_id"`
	Capability  string `json:"capability"`
	EmployeeID  string `json:"employee_id"`
	TenantID    string `json:"tenant_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ExecutionID string `json:"execution_id"`
	StepIndex   int    `json:"step_index"`
	Input       string `json:"input"`
}

type ProfessionalAgentArtifact struct {
	Name      string `json:"name,omitempty"`
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type ProfessionalAgentResponse struct {
	Protocol  string                      `json:"protocol"`
	RequestID string                      `json:"request_id"`
	AgentID   string                      `json:"agent_id"`
	Status    string                      `json:"status"`
	Output    string                      `json:"output,omitempty"`
	Data      json.RawMessage             `json:"data,omitempty"`
	Warnings  []string                    `json:"warnings,omitempty"`
	Artifacts []ProfessionalAgentArtifact `json:"artifacts,omitempty"`
	Error     string                      `json:"error,omitempty"`
}

type professionalAgentInvokeFunc func(context.Context, ProfessionalAgentManifest, ProfessionalAgentRequest, string, string) (ProfessionalAgentResponse, error)

type ProfessionalAgentRegistry struct {
	root             string
	workspace        string
	logger           *log.Logger
	invoke           professionalAgentInvokeFunc
	credentialSource professionalAgentCredentialSource
}

var professionalAgentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var professionalAgentEnvPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

func NewProfessionalAgentRegistry(workspace string, logger *log.Logger) *ProfessionalAgentRegistry {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	root := strings.TrimSpace(os.Getenv("OPENKITTY_PROFESSIONAL_AGENTS_DIR"))
	if root == "" {
		root = filepath.Join(workspace, "professional_agents")
	}
	registry := &ProfessionalAgentRegistry{
		root:      filepath.Clean(root),
		workspace: filepath.Clean(workspace),
		logger:    logger,
	}
	registry.invoke = registry.invokeProcess
	return registry
}

func (r *ProfessionalAgentRegistry) Invoke(ctx context.Context, principal ExecutionPrincipal, request ProfessionalAgentRequest, timeout time.Duration) (ProfessionalAgentResponse, error) {
	if r == nil {
		return ProfessionalAgentResponse{}, errors.New("professional agent registry is not configured")
	}
	manifest, agentDir, err := r.loadManifest(request.AgentID)
	if err != nil {
		return ProfessionalAgentResponse{}, err
	}
	if principal.Kind != "employee" || principal.EmployeeID == "" || principal.EmployeeID != request.EmployeeID {
		return ProfessionalAgentResponse{}, errors.New("professional agent invocation requires a matching AI employee grant")
	}
	if !stringListContains(manifest.AllowedEmployees, principal.EmployeeID) {
		return ProfessionalAgentResponse{}, fmt.Errorf("AI employee %q is not authorized for professional agent %q", principal.EmployeeID, manifest.ID)
	}
	if !stringListContains(manifest.Capabilities, request.Capability) {
		return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q does not provide capability %q", manifest.ID, request.Capability)
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if manifest.MaxTimeoutSeconds > 0 {
		maximum := time.Duration(manifest.MaxTimeoutSeconds) * time.Second
		if timeout > maximum {
			timeout = maximum
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runDir, err := r.createRunDir(manifest.ID, request.ExecutionID, request.StepIndex)
	if err != nil {
		return ProfessionalAgentResponse{}, err
	}
	response, err := r.invoke(ctx, manifest, request, agentDir, runDir)
	if err != nil {
		return ProfessionalAgentResponse{}, err
	}
	if err := validateProfessionalAgentResponse(request, &response, runDir); err != nil {
		return ProfessionalAgentResponse{}, err
	}
	return response, nil
}

func (r *ProfessionalAgentRegistry) loadManifest(agentID string) (ProfessionalAgentManifest, string, error) {
	agentID = strings.TrimSpace(agentID)
	if !professionalAgentIDPattern.MatchString(agentID) {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("invalid professional agent id %q", agentID)
	}
	rootAbs, err := filepath.Abs(r.root)
	if err != nil {
		return ProfessionalAgentManifest{}, "", err
	}
	directoryName := agentID
	agentDir := filepath.Join(rootAbs, directoryName)
	if _, statErr := os.Stat(filepath.Join(agentDir, "manifest.json")); statErr != nil {
		// Directory names may use filesystem-friendly snake_case while the stable
		// protocol/employee ID remains kebab-case.
		directoryName = strings.ReplaceAll(agentID, "-", "_")
		agentDir = filepath.Join(rootAbs, directoryName)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(agentDir); resolveErr == nil {
		agentDir = resolved
	}
	if !pathWithin(rootAbs, agentDir) {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("professional agent directory escapes registry root: %s", agentID)
	}
	manifestPath := filepath.Join(agentDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("read professional agent manifest %q: %w", agentID, err)
	}
	var manifest ProfessionalAgentManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("decode professional agent manifest %q: %w", agentID, err)
	}
	if manifest.Protocol != professionalAgentProtocol {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("professional agent %q uses unsupported protocol %q", agentID, manifest.Protocol)
	}
	if manifest.ID != agentID {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("professional agent manifest id %q does not match directory %q", manifest.ID, agentID)
	}
	if strings.TrimSpace(manifest.Command) == "" {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("professional agent %q has no command", agentID)
	}
	if len(manifest.AllowedEmployees) == 0 || len(manifest.Capabilities) == 0 {
		return ProfessionalAgentManifest{}, "", fmt.Errorf("professional agent %q must declare allowed_employees and capabilities", agentID)
	}
	// Validate the manifest-owned declaration even before an operator binds a
	// model profile. A declaration grants no credential access by itself.
	if _, err := resolveProfessionalAgentModelCredentials(
		manifest.ID,
		manifest.ModelCredentials,
		ProfessionalAgentsConfig{},
		nil,
		nil,
	); err != nil {
		return ProfessionalAgentManifest{}, "", err
	}
	return manifest, agentDir, nil
}

func (r *ProfessionalAgentRegistry) createRunDir(agentID, executionID string, stepIndex int) (string, error) {
	executionID = sanitizeProfessionalAgentPathPart(executionID)
	if executionID == "" {
		executionID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	// Keep physical paths comfortably below legacy Windows MAX_PATH. The full
	// execution/request IDs remain in the protocol, DB, logs, and tool audit;
	// this shortened key is only a deterministic directory component.
	if len(executionID) > 24 {
		digest := sha256.Sum256([]byte(executionID))
		executionID = fmt.Sprintf("%.8s-%x", executionID, digest[:6])
	}
	base := filepath.Join(r.runtimeWorkspace(), ".openkitty", "professional-agents", agentID)
	runDir := filepath.Join(base, executionID, fmt.Sprintf("step-%d", stepIndex))
	if !pathWithin(base, runDir) {
		return "", errors.New("professional agent run directory escaped its root")
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", fmt.Errorf("create professional agent run directory: %w", err)
	}
	return runDir, nil
}

// runtimeWorkspace is deliberately independent from the professional-agent
// code registry. Deployments may keep reviewed agent code in a shared or
// read-only directory, while every request artifact must remain inside the
// active OpenKitty workspace so the normal confined download path can serve
// it. The fallback preserves compatibility for small unit-test registries
// constructed directly instead of through NewProfessionalAgentRegistry.
func (r *ProfessionalAgentRegistry) runtimeWorkspace() string {
	if r != nil && strings.TrimSpace(r.workspace) != "" {
		return filepath.Clean(r.workspace)
	}
	if r != nil && strings.TrimSpace(r.root) != "" {
		return filepath.Dir(filepath.Clean(r.root))
	}
	workspace, _ := os.Getwd()
	return filepath.Clean(workspace)
}

func sanitizeProfessionalAgentPathPart(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func (r *ProfessionalAgentRegistry) invokeProcess(ctx context.Context, manifest ProfessionalAgentManifest, request ProfessionalAgentRequest, agentDir, runDir string) (ProfessionalAgentResponse, error) {
	modelEnvironment, err := r.resolveModelCredentialEnvironment(manifest)
	if err != nil {
		return ProfessionalAgentResponse{}, err
	}
	command := strings.TrimSpace(manifest.Command)
	if strings.ContainsAny(command, `/\\`) && !filepath.IsAbs(command) {
		command = filepath.Join(agentDir, filepath.FromSlash(command))
		if !pathWithin(agentDir, command) {
			return ProfessionalAgentResponse{}, errors.New("professional agent command escapes its directory")
		}
	}
	args := make([]string, len(manifest.Args))
	for i, arg := range manifest.Args {
		args[i] = strings.NewReplacer("${agent_dir}", agentDir, "${run_dir}", runDir).Replace(arg)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return ProfessionalAgentResponse{}, err
	}
	payload = append(payload, '\n')

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = agentDir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = professionalAgentEnvironment(manifest.Environment, modelEnvironment, manifest.ID, runDir)
	maxOutput := manifest.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}
	if maxOutput > 8*1024*1024 {
		maxOutput = 8 * 1024 * 1024
	}
	stdout := &cappedAgentBuffer{max: maxOutput}
	stderr := &cappedAgentBuffer{max: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	started := time.Now()
	err = cmd.Run()
	processStatus := professionalAgentProcessStatus(err)
	responseStatus := "not_decoded"
	defer func() {
		if r.logger != nil {
			r.logger.Printf(
				"[ProfessionalAgent] id=%s request=%s process_status=%s response_status=%s duration=%s",
				professionalAgentLogValue(manifest.ID),
				professionalAgentLogValue(request.RequestID),
				processStatus,
				responseStatus,
				time.Since(started).Round(time.Millisecond),
			)
		}
	}()
	if ctx.Err() != nil {
		return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q timed out or was cancelled: %w", manifest.ID, ctx.Err())
	}
	if stdout.overflow {
		return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q exceeded max output bytes (%d)", manifest.ID, maxOutput)
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q exited with error: %w: %s", manifest.ID, err, truncate(message, 1000))
		}
		return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q exited with error: %w", manifest.ID, err)
	}

	var response ProfessionalAgentResponse
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := decoder.Decode(&response); err != nil {
		return ProfessionalAgentResponse{}, fmt.Errorf("decode professional agent %q response: %w", manifest.ID, err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		responseStatus = "invalid"
		return ProfessionalAgentResponse{}, fmt.Errorf("professional agent %q returned more than one response", manifest.ID)
	}
	responseStatus = professionalAgentResponseLogStatus(response.Status)
	return response, nil
}

func professionalAgentLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return '_'
		}
		return r
	}, value)
	return truncate(value, 128)
}

func professionalAgentResponseLogStatus(status string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(status)); normalized {
	case "completed", "degraded", "unavailable", "failed":
		return normalized
	case "":
		return "missing"
	default:
		return "invalid"
	}
}

func professionalAgentEnvironment(allowed []string, injected map[string]string, agentID, runDir string) []string {
	baseNames := []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE"}
	seen := map[string]bool{}
	names := append(baseNames, allowed...)
	sort.Strings(names)
	env := make([]string, 0, len(names)+2)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if seen[name] || !professionalAgentEnvPattern.MatchString(name) {
			continue
		}
		seen[name] = true
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	// Credential values are resolved from an explicitly selected model profile.
	// Parent-process values keep priority and are already copied above when the
	// manifest allowlists them. Sort injected names for deterministic tests and
	// logs without ever logging their values.
	injectedNames := make([]string, 0, len(injected))
	for name := range injected {
		injectedNames = append(injectedNames, name)
	}
	sort.Strings(injectedNames)
	for _, name := range injectedNames {
		if seen[name] {
			if _, present := os.LookupEnv(name); present {
				continue
			}
		} else if !professionalAgentEnvPattern.MatchString(name) {
			continue
		}
		value := injected[name]
		if value == "" {
			continue
		}
		env = append(env, name+"="+value)
		seen[name] = true
	}
	env = append(env,
		"OPENKITTY_AGENT_ID="+agentID,
		"OPENKITTY_AGENT_RUN_DIR="+runDir,
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
		"PYTHONUNBUFFERED=1",
	)
	return env
}

func validateProfessionalAgentResponse(request ProfessionalAgentRequest, response *ProfessionalAgentResponse, runDir string) error {
	if response.Protocol != professionalAgentProtocol || response.RequestID != request.RequestID || response.AgentID != request.AgentID {
		return errors.New("professional agent response correlation or protocol mismatch")
	}
	switch response.Status {
	case "completed", "degraded", "unavailable":
	case "failed":
		if strings.TrimSpace(response.Error) == "" {
			response.Error = "professional agent reported failure"
		}
		// A failed business response is still a valid, correlated protocol
		// response. Preserve it so the caller can persist its bounded output,
		// warnings, and structured diagnostics before returning a hard failure.
	default:
		return fmt.Errorf("professional agent returned invalid status %q", response.Status)
	}
	for i := range response.Artifacts {
		artifact := &response.Artifacts[i]
		rel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(artifact.Path)))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("professional agent returned invalid artifact path %q", artifact.Path)
		}
		absolute := filepath.Join(runDir, rel)
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return fmt.Errorf("professional agent artifact %q is not readable: %w", artifact.Path, err)
		}
		if !pathWithin(runDir, resolved) {
			return fmt.Errorf("professional agent artifact %q escapes run directory", artifact.Path)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("professional agent artifact %q is missing, empty, or not a regular file", artifact.Path)
		}
		artifact.Path = resolved
		artifact.Size = info.Size()
	}
	return nil
}

func boundProfessionalAgentText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	return textutil.TruncateUTF8Bytes(value, maxBytes)
}

func professionalAgentBackendResultsDetail(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	backendResults := envelope["backend_results"]
	if len(backendResults) == 0 || string(backendResults) == "null" {
		return ""
	}
	wrapped, err := json.Marshal(struct {
		BackendResults json.RawMessage `json:"backend_results"`
	}{BackendResults: backendResults})
	if err != nil {
		return ""
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, wrapped, "", "  ") == nil {
		return pretty.String()
	}
	return string(wrapped)
}

func professionalAgentFailureSummary(response ProfessionalAgentResponse) string {
	message := strings.TrimSpace(response.Error)
	if message == "" {
		message = "professional agent reported failure"
	}

	// Surface a small backend/status/warning index in tool errors and progress.
	// The complete bounded response remains in ToolCall.Result and Step.Output.
	var envelope struct {
		BackendResults []struct {
			Backend  string   `json:"backend"`
			Status   string   `json:"status"`
			Warnings []string `json:"warnings"`
		} `json:"backend_results"`
	}
	if len(response.Data) > 0 && json.Unmarshal(response.Data, &envelope) == nil {
		items := make([]string, 0, len(envelope.BackendResults))
		for _, backend := range envelope.BackendResults {
			name := strings.TrimSpace(backend.Backend)
			status := strings.TrimSpace(backend.Status)
			if name == "" && status == "" {
				continue
			}
			item := strings.Trim(strings.Join([]string{name, status}, "="), "=")
			if len(backend.Warnings) > 0 && strings.TrimSpace(backend.Warnings[0]) != "" {
				item += " (" + textutil.TruncateUTF8Bytes(strings.TrimSpace(backend.Warnings[0]), 240) + ")"
			}
			items = append(items, item)
			if len(items) == 5 {
				break
			}
		}
		if len(items) > 0 {
			message += "; backends: " + strings.Join(items, "; ")
		}
	}
	return boundProfessionalAgentText(message, professionalAgentErrorMaxBytes)
}

func (r *ProfessionalAgentRegistry) validatedPipelineArtifactRecords(agentID string, artifacts []ProfessionalAgentArtifact) ([]ToolCallArtifactRecord, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	agentRoot := filepath.Join(r.runtimeWorkspace(), ".openkitty", "professional-agents", agentID)
	agentRootAbs, err := filepath.Abs(agentRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve professional agent artifact root: %w", err)
	}
	records := make([]ToolCallArtifactRecord, 0, len(artifacts))
	for _, artifact := range artifacts {
		absolute := filepath.Clean(strings.TrimSpace(artifact.Path))
		if !filepath.IsAbs(absolute) || artifact.Size <= 0 || !pathWithin(agentRootAbs, absolute) {
			return nil, fmt.Errorf("validated professional agent artifact cannot be mapped into its workspace root")
		}
		relative, err := filepath.Rel(agentRootAbs, absolute)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("validated professional agent artifact escapes its workspace root")
		}
		records = append(records, ToolCallArtifactRecord{
			Path:      filepath.ToSlash(filepath.Join(".openkitty", "professional-agents", agentID, relative)),
			Size:      artifact.Size,
			Validated: true,
		})
	}
	return records, nil
}

func formatProfessionalAgentResponse(response ProfessionalAgentResponse) string {
	var parts []string
	if response.Status == "failed" {
		// Put the failure identity and backend index first so they remain visible
		// even when an agent also returns a very large diagnostic payload.
		parts = append(parts, fmt.Sprintf("专业 Agent 状态：%s", response.Status))
		parts = append(parts, "错误："+strings.TrimSpace(response.Error))
		if detail := professionalAgentBackendResultsDetail(response.Data); detail != "" {
			parts = append(parts, "后端失败明细：\n```json\n"+detail+"\n```")
		}
		if len(response.Warnings) > 0 {
			parts = append(parts, "警告：\n- "+strings.Join(response.Warnings, "\n- "))
		}
		if output := strings.TrimSpace(response.Output); output != "" {
			parts = append(parts, output)
		}
	} else {
		if output := strings.TrimSpace(response.Output); output != "" {
			parts = append(parts, output)
		}
		parts = append(parts, fmt.Sprintf("专业 Agent 状态：%s", response.Status))
		if len(response.Warnings) > 0 {
			parts = append(parts, "警告：\n- "+strings.Join(response.Warnings, "\n- "))
		}
	}
	if len(response.Artifacts) > 0 {
		lines := make([]string, 0, len(response.Artifacts))
		for _, artifact := range response.Artifacts {
			label := strings.TrimSpace(artifact.Name)
			if label == "" {
				label = filepath.Base(artifact.Path)
			}
			lines = append(lines, fmt.Sprintf("- %s: %s (%d bytes)", label, artifact.Path, artifact.Size))
		}
		parts = append(parts, "产物：\n"+strings.Join(lines, "\n"))
	}
	if len(response.Data) > 0 && string(response.Data) != "null" && response.Status == "failed" {
		var pretty bytes.Buffer
		if json.Indent(&pretty, response.Data, "", "  ") == nil {
			parts = append(parts, "结构化结果：\n```json\n"+pretty.String()+"\n```")
		}
	} else if summary := compactProfessionalAgentData(response.Data); summary != "" {
		parts = append(parts, "运行摘要：\n```json\n"+summary+"\n```")
	}
	marker := "\n\n" + professionalAgentStatusMarker + response.Status
	visibleBudget := professionalAgentStepOutputMaxBytes - len(marker)
	visible := boundProfessionalAgentText(strings.Join(parts, "\n\n"), visibleBudget)
	return visible + marker
}

func compactProfessionalAgentData(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	var source map[string]interface{}
	if json.Unmarshal(data, &source) != nil {
		return ""
	}
	summary := map[string]interface{}{}
	for _, key := range []string{"mode", "live_trading", "run_dir", "employee_profile"} {
		if value, ok := source[key]; ok {
			summary[key] = value
		}
	}
	if resolution, ok := source["security_resolution"].(map[string]interface{}); ok {
		compact := map[string]interface{}{}
		for _, key := range []string{"symbols", "requested", "effective_as_of"} {
			if value, exists := resolution[key]; exists {
				compact[key] = value
			}
		}
		if len(compact) > 0 {
			summary["security_resolution"] = compact
		}
	}
	if marketData, ok := source["market_data"].(map[string]interface{}); ok {
		compact := map[string]interface{}{}
		for _, key := range []string{"status", "provider", "frequency", "adjustment", "rows", "effective_as_of", "requested_as_of"} {
			if value, exists := marketData[key]; exists {
				compact[key] = value
			}
		}
		if len(compact) > 0 {
			summary["market_data"] = compact
		}
	}
	if rawResults, ok := source["backend_results"].([]interface{}); ok {
		results := make([]map[string]interface{}, 0, len(rawResults))
		for _, rawResult := range rawResults {
			result, ok := rawResult.(map[string]interface{})
			if !ok {
				continue
			}
			compact := map[string]interface{}{}
			for _, key := range []string{"backend", "status"} {
				if value, exists := result[key]; exists {
					compact[key] = value
				}
			}
			if resultData, ok := result["data"].(map[string]interface{}); ok {
				quality := map[string]interface{}{}
				for _, key := range []string{"quality_gate", "manager_quality_gate", "text_quality_gate", "sentiment", "as_of", "start_date", "end_date"} {
					if value, exists := resultData[key]; exists {
						quality[key] = value
					}
				}
				if len(quality) > 0 {
					compact["result"] = quality
				}
			}
			results = append(results, compact)
		}
		if len(results) > 0 {
			summary["backends"] = results
		}
	}
	if len(summary) == 0 {
		return ""
	}
	formatted, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ""
	}
	return boundProfessionalAgentText(string(formatted), 12*1024)
}

const professionalAgentStatusMarker = "OPENKITTY_PROFESSIONAL_AGENT_STATUS:"

func professionalAgentOutputStatus(output string) string {
	idx := strings.LastIndex(output, professionalAgentStatusMarker)
	if idx < 0 {
		return ""
	}
	status := strings.TrimSpace(output[idx+len(professionalAgentStatusMarker):])
	if newline := strings.IndexAny(status, "\r\n"); newline >= 0 {
		status = status[:newline]
	}
	return strings.ToLower(strings.TrimSpace(status))
}

func stripProfessionalAgentStatusMarker(output string) string {
	idx := strings.LastIndex(output, professionalAgentStatusMarker)
	if idx < 0 {
		return output
	}
	return strings.TrimSpace(output[:idx])
}

func (e *PipelineExecutor) executeProfessionalAgentStep(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIdx int, sessionID string) (string, error) {
	if e == nil || e.professionalAgents == nil || step == nil || step.ProfessionalAgent == nil {
		return "", errors.New("professional agent step is not configured")
	}
	principal, ok := employeeExecutionPrincipal(ctx)
	if !ok {
		return "", errors.New("professional agent steps may only be started by an explicitly addressed AI employee")
	}
	if exec == nil || exec.Principal.Kind != "employee" || exec.Principal.EmployeeID != principal.EmployeeID || exec.Principal.TenantID != principal.TenantID {
		return "", errors.New("professional agent execution principal mismatch")
	}
	invocation := step.ProfessionalAgent
	capability := strings.TrimSpace(invocation.Capability)
	if capability == "" {
		capability = "analyze"
	}
	attempt := 1
	if stepIdx >= 0 && stepIdx < len(exec.Steps) && exec.Steps[stepIdx].Revision > 0 {
		attempt = exec.Steps[stepIdx].Revision
	}
	requestID := fmt.Sprintf("%s:s%d:a%d", exec.ID, stepIdx, attempt)
	input := strings.TrimSpace(exec.Input)
	request := ProfessionalAgentRequest{
		Protocol:    professionalAgentProtocol,
		RequestID:   requestID,
		AgentID:     strings.TrimSpace(invocation.ID),
		Capability:  capability,
		EmployeeID:  principal.EmployeeID,
		TenantID:    principal.TenantID,
		SessionID:   sessionID,
		ExecutionID: exec.ID,
		StepIndex:   stepIdx,
		Input:       input,
	}
	timeout := 5 * time.Minute
	if step.TimeoutSeconds > 0 {
		timeout = time.Duration(step.TimeoutSeconds) * time.Second
	}
	started := time.Now()
	response, err := e.professionalAgents.Invoke(ctx, principal, request, timeout)
	record := ToolCallRecord{
		ToolName:   "professional_agent:" + request.AgentID + ":" + request.Capability,
		Params:     fmt.Sprintf(`{"request_id":%q,"employee_id":%q,"capability":%q}`, request.RequestID, request.EmployeeID, request.Capability),
		Status:     "success",
		DurationMs: time.Since(started).Milliseconds(),
		Timestamp:  started,
	}
	if err != nil {
		record.Error = boundProfessionalAgentText(err.Error(), professionalAgentErrorMaxBytes)
		record.Result = "professional agent invocation failed"
		exec.recordToolCall(stepIdx, record)
		return "", errors.New(record.Error)
	}
	output := formatProfessionalAgentResponse(response)
	boundedResult := boundProfessionalAgentText(stripProfessionalAgentStatusMarker(output), professionalAgentToolResultMaxBytes)
	record.Artifacts, err = e.professionalAgents.validatedPipelineArtifactRecords(response.AgentID, response.Artifacts)
	if err != nil {
		record.Error = boundProfessionalAgentText(err.Error(), professionalAgentErrorMaxBytes)
		record.Result = boundedResult
		exec.recordToolCall(stepIdx, record)
		return output, errors.New(record.Error)
	}
	record.Result = boundedResult
	if response.Status == "failed" {
		record.Status = "error"
		record.Error = professionalAgentFailureSummary(response)
		exec.recordToolCall(stepIdx, record)
		return output, &professionalAgentReportedFailure{message: record.Error}
	}
	exec.recordToolCall(stepIdx, record)
	return output, nil
}

func stringListContains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type cappedAgentBuffer struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedAgentBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := b.max - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return originalLen, nil
	}
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return originalLen, nil
}

func professionalAgentProcessStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

type ProfessionalAgentsConfig = security.ProfessionalAgentsConfig
type ProfessionalAgentModelCredential = security.ProfessionalAgentModelCredential
type ProfessionalAgentModelCredentialEnvironment = security.ProfessionalAgentModelCredentialEnvironment

// professionalAgentCredentialSource returns the current host-owned binding
// configuration. Gateway supplies a read-locked closure, so config reloads are
// visible without copying credentials into the registry or restarting it.
type professionalAgentCredentialSource func() (ProfessionalAgentsConfig, map[string]ModelConfig)

func (r *ProfessionalAgentRegistry) resolveModelCredentialEnvironment(manifest ProfessionalAgentManifest) (map[string]string, error) {
	if r == nil || len(manifest.ModelCredentials) == 0 || r.credentialSource == nil {
		return map[string]string{}, nil
	}
	config, models := r.credentialSource()
	return resolveProfessionalAgentModelCredentials(
		manifest.ID,
		manifest.ModelCredentials,
		config,
		models,
		os.LookupEnv,
	)
}

func resolveProfessionalAgentModelCredentials(
	agentID string,
	declarations []ProfessionalAgentModelCredential,
	config ProfessionalAgentsConfig,
	models map[string]ModelConfig,
	lookupParent func(string) (string, bool),
) (map[string]string, error) {
	modelViews := make(map[string]security.ProfessionalAgentModelConfig, len(models))
	for name, model := range models {
		modelViews[name] = security.ProfessionalAgentModelConfig{
			APIKey:    model.APIKey,
			BaseURL:   model.BaseURL,
			ModelName: model.ModelName,
		}
	}
	return security.ResolveProfessionalAgentModelCredentials(agentID, declarations, config, modelViews, lookupParent)
}
