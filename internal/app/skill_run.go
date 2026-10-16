package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io"
	"io/fs"
	approvalcore "mac/internal/approval"
	"mac/internal/eventbus"
	"mac/internal/fsutil"
	gatewayhttp "mac/internal/gateway/httpx"
	"mac/internal/globalstate"
	"mac/internal/projectctx"
	"mac/internal/routing"
	"mac/internal/security"
	"mac/internal/skillpath"
	skilldomain "mac/internal/skills"
	securitytools "mac/internal/tools/security"
	workspacetools "mac/internal/tools/workspace"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	skillRunPhaseResolving = "resolving"
	skillRunPhaseActivated = "activated"
	skillRunPhaseRunning   = "running"
	skillRunPhaseVerifying = "verifying"
	skillRunPhaseCompleted = "completed"
	skillRunPhaseFailed    = "failed"
	skillRunPhaseStopped   = "stopped"

	skillRunActivationCallID = "host_skill_run_activation_v1"
)

// SkillRunRequest binds one task to one concrete Skill. It is deliberately a
// run-scoped request rather than a session setting: ordinary chat and @mention
// Pipeline routing retain their existing behaviour after this task finishes.
type SkillRunRequest struct {
	SkillName       string                 `json:"skill_name"`
	Version         string                 `json:"version,omitempty"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
	ClientReference string                 `json:"client_reference,omitempty"`
}

// SkillRunSnapshot is the immutable execution identity of the selected Skill.
// ContentSHA256 covers the exact SKILL.md bytes and RuntimeDigest covers the
// complete installed bundle when it can be read. The full activated output is
// persisted separately so an in-flight run never changes workflow after an
// upgrade or process restart.
type SkillRunSnapshot struct {
	Name            string                `json:"name"`
	Version         string                `json:"version,omitempty"`
	Author          string                `json:"author,omitempty"`
	Description     string                `json:"description,omitempty"`
	Type            skilldomain.SkillType `json:"type"`
	Source          string                `json:"source,omitempty"`
	ContentSHA256   string                `json:"content_sha256"`
	RuntimeDigest   string                `json:"runtime_digest,omitempty"`
	RequiresRuntime bool                  `json:"requires_runtime,omitempty"`
	HasSupportFiles bool                  `json:"has_support_files,omitempty"`
}

type SkillRunVerification struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Checked string `json:"checked_at,omitempty"`
}

// SkillRunReceipt is safe to expose to clients. It proves which immutable
// Skill version ran and which completion evidence the host observed.
type SkillRunReceipt struct {
	RunID           string                 `json:"run_id"`
	TaskID          string                 `json:"task_id"`
	Phase           string                 `json:"phase"`
	Skill           SkillRunSnapshot       `json:"skill"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
	ActivationID    string                 `json:"activation_id,omitempty"`
	ActivationCount int                    `json:"activation_count"`
	Artifacts       []string               `json:"artifacts,omitempty"`
	Verification    []SkillRunVerification `json:"verification,omitempty"`
	StartedAt       string                 `json:"started_at,omitempty"`
	CompletedAt     string                 `json:"completed_at,omitempty"`
	DurationMs      int64                  `json:"duration_ms,omitempty"`
	ClientReference string                 `json:"client_reference,omitempty"`
}

// SkillRunRecord is intentionally separate from GatewayTaskRecord. Gateway is
// the scheduler; this table is the durable execution checkpoint and receipt
// for the exact Skill version bound to a task.
type SkillRunRecord struct {
	ID               uint       `gorm:"column:id;primarykey" json:"-"`
	RunID            string     `gorm:"column:run_id;uniqueIndex;size:96" json:"run_id"`
	TaskID           string     `gorm:"column:task_id;uniqueIndex;size:64" json:"task_id"`
	TenantID         string     `gorm:"column:tenant_id;index;size:64;default:default" json:"tenant_id"`
	SubjectID        string     `gorm:"column:subject_id;index;size:128" json:"subject_id,omitempty"`
	SkillName        string     `gorm:"column:skill_name;index;size:128" json:"skill_name"`
	SkillVersion     string     `gorm:"column:skill_version;index;size:80" json:"skill_version,omitempty"`
	ContentSHA256    string     `gorm:"column:content_sha256;index;size:64" json:"content_sha256"`
	RuntimeDigest    string     `gorm:"column:runtime_digest;size:64" json:"runtime_digest,omitempty"`
	SkillSnapshot    string     `gorm:"column:skill_snapshot;type:text" json:"-"`
	ParametersJSON   string     `gorm:"column:parameters_json;type:text" json:"-"`
	ActivatedOutput  string     `gorm:"column:activated_output;type:text" json:"-"`
	FrozenRuntimeDir string     `gorm:"column:frozen_runtime_dir;type:text" json:"-"`
	ActivationID     string     `gorm:"column:activation_id;size:128" json:"activation_id,omitempty"`
	ActivationCount  int        `gorm:"column:activation_count" json:"activation_count"`
	Phase            string     `gorm:"column:phase;index;size:24" json:"phase"`
	ArtifactsJSON    string     `gorm:"column:artifacts_json;type:text" json:"-"`
	VerificationJSON string     `gorm:"column:verification_json;type:text" json:"-"`
	ClientReference  string     `gorm:"column:client_reference;index;size:128" json:"client_reference,omitempty"`
	ErrorMsg         string     `gorm:"column:error_msg;type:text" json:"error,omitempty"`
	StartedAt        time.Time  `gorm:"column:started_at;index" json:"started_at"`
	CompletedAt      *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	CreatedAt        time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (SkillRunRecord) TableName() string { return "skill_run_records" }

type skillRunContextKey struct{}

type skillRunContext struct {
	Snapshot         SkillRunSnapshot
	Parameters       map[string]interface{}
	ActivatedOutput  string
	FrozenRuntimeDir string
}

type skillRunExecutionOverrideKey struct{}

type skillRunExecutionOverride struct {
	Name  string
	Skill *skilldomain.Skill
}

func withSkillRunExecutionOverride(ctx context.Context, name string, skill *skilldomain.Skill) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if skill == nil || strings.TrimSpace(name) == "" {
		return ctx
	}
	return context.WithValue(ctx, skillRunExecutionOverrideKey{}, &skillRunExecutionOverride{
		Name: strings.TrimSpace(name), Skill: skill,
	})
}

func skillRunExecutionOverrideFromContext(ctx context.Context, requested string) (*skilldomain.Skill, string, bool) {
	if ctx == nil {
		return nil, requested, false
	}
	override, _ := ctx.Value(skillRunExecutionOverrideKey{}).(*skillRunExecutionOverride)
	if override == nil || override.Skill == nil {
		return nil, requested, false
	}
	if !strings.EqualFold(strings.TrimSpace(requested), strings.TrimSpace(override.Name)) {
		return nil, requested, false
	}
	return override.Skill, override.Name, true
}

func resolveSkillRuntimeScope(ctx context.Context) (bool, string) {
	workspaceScoped := isRestrictedTenantExecution(ctx)
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	for _, sandbox := range sandboxPoliciesFromContext(ctx) {
		if sandbox == nil {
			continue
		}
		if sandbox.ConfigSnapshot().Mode != securitytools.SandboxNone {
			workspaceScoped = true
		}
		if workspace == "" {
			workspace = strings.TrimSpace(sandbox.Root())
		}
	}
	return workspaceScoped, workspace
}

func withSkillRunContext(ctx context.Context, run *skillRunContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if run == nil {
		return ctx
	}
	return context.WithValue(ctx, skillRunContextKey{}, run)
}

func skillRunContextFromContext(ctx context.Context) *skillRunContext {
	if ctx == nil {
		return nil
	}
	run, _ := ctx.Value(skillRunContextKey{}).(*skillRunContext)
	return run
}

func skillRunPrompt(ctx context.Context) string {
	run := skillRunContextFromContext(ctx)
	if run == nil || strings.TrimSpace(run.Snapshot.Name) == "" || strings.TrimSpace(run.ActivatedOutput) == "" {
		return ""
	}
	params, _ := json.Marshal(run.Parameters)
	return fmt.Sprintf(`## Host-bound Skill Run
The host has bound this entire task to Skill %q, version %q, content SHA-256 %s.
Activation already completed before the first model call. Do not call use_skill again and do not substitute, upgrade, or silently combine another primary Skill. The normal OpenKitty tools remain available; use them as needed and preserve ordinary fallbacks. Follow the frozen instructions below until their completion conditions are actually satisfied. User instructions remain authoritative when they explicitly conflict with a workflow default.
The frozen Skill is the authoritative domain and output contract for this run, not optional background material. Apply its MUST, MUST NOT, scope, evidence, safety, format, and length rules to the final answer. Before returning, silently compare the draft with those rules and rewrite any conflicting passage; do not describe this check to the user. When the Skill routes a relevant supporting reference, read that reference before answering instead of improvising missing domain knowledge.
Run parameters: %s

### Frozen Skill instructions
%s`, run.Snapshot.Name, run.Snapshot.Version, run.Snapshot.ContentSHA256, string(params), run.ActivatedOutput)
}

func normalizeSkillRunRequest(req *SkillRunRequest) (*SkillRunRequest, error) {
	if req == nil {
		return nil, nil
	}
	name := strings.TrimSpace(req.SkillName)
	if normalized, ok := skilldomain.NormalizeName(name); !ok {
		return nil, fmt.Errorf("valid skill_run.skill_name is required")
	} else {
		name = normalized
	}
	version := strings.TrimSpace(req.Version)
	if len(version) > 80 {
		return nil, fmt.Errorf("skill_run.version is too long")
	}
	if len(req.ClientReference) > 128 {
		return nil, fmt.Errorf("skill_run.client_reference is too long")
	}
	parameters := cloneSkillRunParameters(req.Parameters)
	if parameters == nil {
		parameters = map[string]interface{}{}
	}
	return &SkillRunRequest{
		SkillName: name, Version: version, Parameters: parameters,
		ClientReference: strings.TrimSpace(req.ClientReference),
	}, nil
}

func cloneSkillRunParameters(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var output map[string]interface{}
	if json.Unmarshal(data, &output) != nil {
		return nil
	}
	return output
}

func cloneSkillRunRequest(req *SkillRunRequest) *SkillRunRequest {
	if req == nil {
		return nil
	}
	copyReq := *req
	copyReq.Parameters = cloneSkillRunParameters(req.Parameters)
	return &copyReq
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func skillRunID(taskID string) string {
	return "skr_" + strings.TrimSpace(taskID)
}

func (g *GatewayService) resolveSkillRunLoader(agent Agent) *skilldomain.SkillsLoader {
	if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.skillsLoader != nil {
		return llmAgent.skillsLoader
	}
	for _, loader := range g.loadedSkillLoaders() {
		if loader != nil {
			return loader
		}
	}
	return nil
}

func skillRunSnapshotForSkill(loader *skilldomain.SkillsLoader, skill *skilldomain.Skill, resolvedName string) (SkillRunSnapshot, error) {
	if skill == nil {
		return SkillRunSnapshot{}, fmt.Errorf("skill is nil")
	}
	raw := skill.Raw
	if strings.TrimSpace(raw) == "" {
		raw = skill.Content
	}
	runtimeDigest := ""
	needsFrozenBundle := skill.RequiresRuntime
	if skill.Embedded && loader != nil && loader.BuiltinFS() != nil && strings.TrimSpace(skill.SourceDir) != "" {
		subtree, err := fs.Sub(loader.BuiltinFS(), filepath.ToSlash(skill.SourceDir))
		if err != nil {
			return SkillRunSnapshot{}, fmt.Errorf("open embedded skill bundle: %w", err)
		}
		digest, err := skilldomain.BundleDigest(subtree, ".")
		if err != nil {
			return SkillRunSnapshot{}, fmt.Errorf("digest embedded skill bundle: %w", err)
		}
		runtimeDigest = digest
		hasSupport, err := skillRunBundleHasSupportFiles(subtree, ".")
		if err != nil {
			return SkillRunSnapshot{}, fmt.Errorf("inspect embedded skill bundle: %w", err)
		}
		needsFrozenBundle = needsFrozenBundle || hasSupport
	} else if strings.TrimSpace(skill.SourceDir) != "" {
		sourceFS := os.DirFS(skill.SourceDir)
		digest, err := skilldomain.BundleDigest(sourceFS, ".")
		if err != nil {
			return SkillRunSnapshot{}, fmt.Errorf("digest skill bundle: %w", err)
		}
		runtimeDigest = digest
		hasSupport, err := skillRunBundleHasSupportFiles(sourceFS, ".")
		if err != nil {
			return SkillRunSnapshot{}, fmt.Errorf("inspect skill bundle: %w", err)
		}
		needsFrozenBundle = needsFrozenBundle || hasSupport
	} else if skill.RequiresRuntime {
		return SkillRunSnapshot{}, fmt.Errorf("skill %q requires runtime assets but has no source directory", resolvedName)
	}
	return SkillRunSnapshot{
		Name: resolvedName, Version: strings.TrimSpace(skill.Metadata.Version),
		Author: strings.TrimSpace(skill.Metadata.Author), Description: strings.TrimSpace(skill.Metadata.Description),
		Type: skilldomain.NormalizeSkillType(string(skill.Metadata.Type)), Source: strings.TrimSpace(skill.Source),
		ContentSHA256: sha256Text(raw), RuntimeDigest: runtimeDigest,
		// A reference, asset, or template is just as execution-significant as a
		// script. In a host-bound run the whole support bundle must therefore be
		// frozen even when ordinary use_skill can treat the Markdown as read-only.
		RequiresRuntime: skill.RequiresRuntime,
		HasSupportFiles: needsFrozenBundle && !skill.RequiresRuntime,
	}, nil
}

func skillRunBundleHasSupportFiles(bundle fs.FS, root string) (bool, error) {
	if bundle == nil {
		return false, nil
	}
	root = filepath.ToSlash(strings.TrimSpace(root))
	if root == "" {
		root = "."
	}
	found := false
	err := fs.WalkDir(bundle, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(path))
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.ToSlash(relative), "SKILL.md") {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	return found, err
}

func (g *GatewayService) loadSkillRunRecord(taskID string) (*SkillRunRecord, error) {
	if g == nil || g.db == nil {
		return nil, gorm.ErrRecordNotFound
	}
	var record SkillRunRecord
	err := g.db.Where("task_id = ?", taskID).First(&record).Error
	return &record, err
}

func decodeSkillRunContext(record *SkillRunRecord) (*skillRunContext, error) {
	if record == nil {
		return nil, fmt.Errorf("skill run record is nil")
	}
	var snapshot SkillRunSnapshot
	if err := json.Unmarshal([]byte(record.SkillSnapshot), &snapshot); err != nil {
		return nil, fmt.Errorf("decode skill snapshot: %w", err)
	}
	params := map[string]interface{}{}
	if strings.TrimSpace(record.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(record.ParametersJSON), &params); err != nil {
			return nil, fmt.Errorf("decode skill parameters: %w", err)
		}
	}
	return &skillRunContext{
		Snapshot: snapshot, Parameters: params, ActivatedOutput: record.ActivatedOutput,
		FrozenRuntimeDir: record.FrozenRuntimeDir,
	}, nil
}

func skillRunReceiptFromRecord(record *SkillRunRecord) SkillRunReceipt {
	if record == nil {
		return SkillRunReceipt{}
	}
	var snapshot SkillRunSnapshot
	_ = json.Unmarshal([]byte(record.SkillSnapshot), &snapshot)
	params := map[string]interface{}{}
	_ = json.Unmarshal([]byte(record.ParametersJSON), &params)
	artifacts := []string{}
	_ = json.Unmarshal([]byte(record.ArtifactsJSON), &artifacts)
	verification := []SkillRunVerification{}
	_ = json.Unmarshal([]byte(record.VerificationJSON), &verification)
	receipt := SkillRunReceipt{
		RunID: record.RunID, TaskID: record.TaskID, Phase: record.Phase, Skill: snapshot,
		Parameters: params, ActivationID: record.ActivationID, ActivationCount: record.ActivationCount,
		Artifacts: artifacts, Verification: verification, ClientReference: record.ClientReference,
	}
	if !record.StartedAt.IsZero() {
		receipt.StartedAt = record.StartedAt.Format(time.RFC3339Nano)
	}
	if record.CompletedAt != nil {
		receipt.CompletedAt = record.CompletedAt.Format(time.RFC3339Nano)
		receipt.DurationMs = record.CompletedAt.Sub(record.StartedAt).Milliseconds()
	}
	return receipt
}

var skillRunMarkdownArtifactLink = regexp.MustCompile(`\[[^\]\r\n]*\]\(\s*(?:<([^>\r\n]+)>|([^\s)\r\n]+))`)

func skillRunLinkedArtifactPaths(text string) []string {
	matches := skillRunMarkdownArtifactLink.FindAllStringSubmatch(text, -1)
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		candidate := ""
		if len(match) > 1 {
			candidate = strings.TrimSpace(match[1])
		}
		if candidate == "" && len(match) > 2 {
			candidate = strings.TrimSpace(match[2])
		}
		if candidate == "" || strings.HasPrefix(candidate, "#") || strings.HasPrefix(candidate, "//") {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.IsAbs() || parsed.Host != "" {
			continue
		}
		candidate = parsed.Path
		if decoded, decodeErr := url.PathUnescape(candidate); decodeErr == nil {
			candidate = decoded
		}
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			paths = append(paths, candidate)
		}
	}
	return paths
}

func skillRunArtifactPaths(result interface{}, workspace string) []string {
	paths := extractArtifactPaths(gatewayResultText(result))
	paths = append(paths, skillRunLinkedArtifactPaths(gatewayResultText(result))...)
	seen := make(map[string]bool)
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		absolute := candidate
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(workspace, filepath.FromSlash(candidate))
		}
		absolute, err := filepath.Abs(absolute)
		if err != nil || !projectctx.ContainsPath(workspace, absolute) {
			continue
		}
		relative, err := filepath.Rel(workspace, absolute)
		if err != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		if !seen[relative] {
			seen[relative] = true
			out = append(out, relative)
		}
	}
	sort.Strings(out)
	return out
}

func validateSkillRunParameters(skill *skilldomain.Skill, supplied map[string]interface{}) (map[string]interface{}, error) {
	parameters := cloneSkillRunParameters(supplied)
	if parameters == nil {
		parameters = map[string]interface{}{}
	}
	if skill == nil {
		return parameters, fmt.Errorf("skill is nil")
	}
	for _, definition := range skill.Metadata.Parameters {
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			continue
		}
		value, exists := parameters[name]
		if !exists || value == nil {
			if definition.Default != nil {
				parameters[name] = definition.Default
				continue
			}
			if definition.Required {
				return nil, fmt.Errorf("skill_run.parameters.%s is required", name)
			}
			continue
		}
		if !skillRunParameterTypeMatches(definition.Type, value) {
			return nil, fmt.Errorf("skill_run.parameters.%s must be %s", name, strings.TrimSpace(definition.Type))
		}
	}
	return parameters, nil
}

func skillRunParameterTypeMatches(declared string, value interface{}) bool {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case "", "any":
		return true
	case "string":
		_, ok := value.(string)
		return ok
	case "bool", "boolean":
		_, ok := value.(bool)
		return ok
	case "int", "integer":
		switch number := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return number == float64(int64(number))
		case float32:
			return number == float32(int64(number))
		default:
			return false
		}
	case "number", "float", "double":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	case "array", "list":
		switch value.(type) {
		case []interface{}, []string:
			return true
		default:
			return false
		}
	case "object", "map":
		_, ok := value.(map[string]interface{})
		return ok
	default:
		// Older Skills sometimes use application-specific type labels. Preserve
		// their historical permissive behaviour instead of reducing capability.
		return true
	}
}

func bindSkillRunContext(ctx context.Context, workspace string, runContext *skillRunContext) context.Context {
	ctx = withSkillRunContext(ctx, runContext)
	if runContext == nil {
		return ctx
	}
	return skilldomain.WithActivatedSkillRequirement(
		ctx,
		runContext.Snapshot.Name,
		workspace,
		runContext.ActivatedOutput,
	)
}

func skillRunRuntimeRoot(workspace, taskID string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", fmt.Errorf("skill run runtime requires a resolved workspace")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve skill run workspace: %w", err)
	}
	absolute = filepath.Clean(absolute)
	root := filepath.Join(absolute, ".openkitty", "skill-runs", skilldomain.SafeFilename(taskID))
	if !projectctx.ContainsPath(absolute, root) {
		return "", fmt.Errorf("skill run runtime escapes the task workspace")
	}
	return root, nil
}

func skillRunBundleDigestAt(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", fmt.Errorf("skill run runtime directory is empty")
	}
	return skilldomain.BundleDigest(os.DirFS(directory), ".")
}

func validateFrozenSkillRunRuntime(workspace, directory, expectedDigest string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("frozen skill runtime is missing")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if !projectctx.ContainsPath(workspace, absolute) {
		return fmt.Errorf("frozen skill runtime is outside the task workspace")
	}
	digest, err := skillRunBundleDigestAt(absolute)
	if err != nil {
		return err
	}
	if expectedDigest != "" && digest != expectedDigest {
		return fmt.Errorf("frozen skill runtime digest mismatch: expected %s, got %s", expectedDigest, digest)
	}
	return nil
}

func copySkillRunBundle(source, destination string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill runtime contains unsupported symbolic link: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if !projectctx.ContainsPath(destination, target) {
			return fmt.Errorf("skill runtime entry escapes frozen directory: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		return os.WriteFile(target, data, mode)
	})
}

func prepareFrozenSkillRunRuntime(
	ctx context.Context,
	task *GatewayTask,
	loader *skilldomain.SkillsLoader,
	skill *skilldomain.Skill,
	snapshot SkillRunSnapshot,
	existingDirectory string,
) (*skilldomain.Skill, string, error) {
	if skill == nil {
		return nil, "", fmt.Errorf("skill is nil")
	}
	if !snapshot.RequiresRuntime && !snapshot.HasSupportFiles {
		return skill, "", nil
	}
	workspace := strings.TrimSpace(task.ResolvedWorkspace)
	if workspace == "" && loader != nil {
		workspace = strings.TrimSpace(loader.Workspace())
	}
	root, err := skillRunRuntimeRoot(workspace, task.ID)
	if err != nil {
		return nil, "", err
	}
	if snapshot.RuntimeDigest == "" {
		return nil, "", fmt.Errorf("skill %q runtime digest is unavailable", snapshot.Name)
	}
	digestPrefix := snapshot.RuntimeDigest
	if len(digestPrefix) > 16 {
		digestPrefix = digestPrefix[:16]
	}
	destination := filepath.Join(root, "runtime-"+digestPrefix)
	if strings.TrimSpace(existingDirectory) != "" {
		destination = filepath.Clean(existingDirectory)
	}
	if !projectctx.ContainsPath(workspace, destination) {
		return nil, "", fmt.Errorf("frozen skill runtime is outside the task workspace")
	}
	if info, statErr := os.Stat(destination); statErr == nil && info.IsDir() {
		if err := validateFrozenSkillRunRuntime(workspace, destination, snapshot.RuntimeDigest); err != nil {
			return nil, "", err
		}
		frozen := *skill
		frozen.SourceDir = destination
		frozen.RuntimeDir = destination
		frozen.RequiresRuntime = false
		return &frozen, destination, nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, "", statErr
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, "", err
	}
	staging, err := os.MkdirTemp(root, ".runtime-staging-")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if skill.Embedded {
		if loader == nil || loader.BuiltinFS() == nil {
			return nil, "", fmt.Errorf("embedded filesystem unavailable for frozen Skill runtime")
		}
		if err := skilldomain.MaterializeFSDir(loader.BuiltinFS(), filepath.ToSlash(skill.SourceDir), staging); err != nil {
			return nil, "", err
		}
	} else {
		if err := copySkillRunBundle(skill.SourceDir, staging); err != nil {
			return nil, "", err
		}
	}
	if err := validateFrozenSkillRunRuntime(workspace, staging, snapshot.RuntimeDigest); err != nil {
		return nil, "", err
	}
	if err := os.Rename(staging, destination); err != nil {
		if validateErr := validateFrozenSkillRunRuntime(workspace, destination, snapshot.RuntimeDigest); validateErr != nil {
			return nil, "", fmt.Errorf("publish frozen Skill runtime: %w", err)
		}
	}
	frozen := *skill
	frozen.SourceDir = destination
	frozen.RuntimeDir = destination
	frozen.RequiresRuntime = false
	return &frozen, destination, nil
}

func loadFrozenSkillRunRuntime(loader *skilldomain.SkillsLoader, runContext *skillRunContext, workspace string) (*skilldomain.Skill, error) {
	if loader == nil || runContext == nil {
		return nil, fmt.Errorf("frozen skill runtime loader is unavailable")
	}
	directory := strings.TrimSpace(runContext.FrozenRuntimeDir)
	if err := validateFrozenSkillRunRuntime(workspace, directory, runContext.Snapshot.RuntimeDigest); err != nil {
		return nil, err
	}
	skillFile := filepath.Join(directory, "SKILL.md")
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		return nil, fmt.Errorf("read frozen SKILL.md: %w", err)
	}
	if digest := sha256Text(string(raw)); digest != runContext.Snapshot.ContentSHA256 {
		return nil, fmt.Errorf("frozen SKILL.md digest mismatch")
	}
	metadata := loader.ParseMetadata(raw, skillFile)
	metadata.Name = runContext.Snapshot.Name
	if skilldomain.NormalizeSkillType(string(metadata.Type)) != runContext.Snapshot.Type {
		return nil, fmt.Errorf("frozen Skill type does not match its execution snapshot")
	}
	content := loader.StripFrontmatter(string(raw))
	cleaned, ok := security.ScanSkillContent(globalstate.PromptInjectionDetector(), runContext.Snapshot.Name, content)
	if !ok {
		return nil, fmt.Errorf("frozen Skill instructions failed validation")
	}
	return &skilldomain.Skill{
		Metadata: metadata, Content: cleaned, Path: skillFile, Source: runContext.Snapshot.Source,
		SourceDir: directory, RuntimeDir: directory,
		Embedded: strings.EqualFold(strings.TrimSpace(runContext.Snapshot.Source), "builtin"),
		Raw:      string(raw), Code: cleaned,
	}, nil
}

func frozenSkillRunDisplayPath(workspace, absolute string) string {
	if relative, err := filepath.Rel(workspace, absolute); err == nil && relative != "." && !filepath.IsAbs(relative) &&
		relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "./" + filepath.ToSlash(relative)
	}
	return filepath.Clean(absolute)
}

func appendFrozenSkillRunRuntimeNotice(output, workspace, directory string) string {
	if strings.TrimSpace(directory) == "" {
		return strings.TrimSpace(output)
	}
	display := frozenSkillRunDisplayPath(workspace, directory)
	return strings.TrimSpace(output) + fmt.Sprintf(`

### Host-frozen Skill bundle
For this run, the authoritative immutable Skill bundle directory is %s. Use this path for every script, reference, asset, template, package file, or helper named by the Skill. It overrides any earlier generic runtime/source directory printed above.`, display)
}

func (g *GatewayService) beginSkillRun(ctx context.Context, task *GatewayTask, agent Agent) (context.Context, error) {
	if task == nil || task.Request == nil || task.Request.SkillRun == nil {
		return ctx, fmt.Errorf("skill_run request is required")
	}
	request, err := normalizeSkillRunRequest(task.Request.SkillRun)
	if err != nil {
		return ctx, err
	}
	task.Request.SkillRun = request
	var (
		record       *SkillRunRecord
		runContext   *skillRunContext
		resolved     *skilldomain.Skill
		resolvedName string
		loader       *skilldomain.SkillsLoader
	)
	if existing, loadErr := g.loadSkillRunRecord(task.ID); loadErr == nil {
		switch existing.Phase {
		case skillRunPhaseFailed:
			return ctx, fmt.Errorf("skill run previously failed: %s", existing.ErrorMsg)
		case skillRunPhaseStopped:
			return ctx, fmt.Errorf("skill run was stopped and cannot be resumed")
		case skillRunPhaseCompleted:
			return ctx, fmt.Errorf("skill run already completed and cannot be resumed")
		}
		decoded, decodeErr := decodeSkillRunContext(existing)
		if decodeErr != nil {
			return ctx, decodeErr
		}
		if request.Version != "" && request.Version != decoded.Snapshot.Version {
			return ctx, fmt.Errorf("skill %q version conflict: requested %q, frozen %q", decoded.Snapshot.Name, request.Version, decoded.Snapshot.Version)
		}
		if strings.TrimSpace(decoded.ActivatedOutput) != "" {
			if strings.TrimSpace(decoded.FrozenRuntimeDir) != "" {
				if runtimeErr := validateFrozenSkillRunRuntime(task.ResolvedWorkspace, decoded.FrozenRuntimeDir, decoded.Snapshot.RuntimeDigest); runtimeErr != nil {
					return ctx, fmt.Errorf("restore frozen skill runtime: %w", runtimeErr)
				}
			}
			if existing.Phase != skillRunPhaseCompleted {
				g.setSkillRunPhase(task.ID, skillRunPhaseRunning)
			}
			return bindSkillRunContext(ctx, task.ResolvedWorkspace, decoded), nil
		}
		record = existing
		runContext = decoded
	} else if !errors.Is(loadErr, gorm.ErrRecordNotFound) {
		return ctx, loadErr
	}

	llmAgent, ok := agent.(*LLMAgent)
	if !ok || llmAgent == nil {
		return ctx, fmt.Errorf("skill runs require an LLM agent with the normal tool runtime")
	}
	loader = llmAgent.skillsLoader
	if loader == nil {
		loader = g.resolveSkillRunLoader(agent)
	}
	if loader == nil {
		return ctx, fmt.Errorf("skills are disabled or no skill loader is available")
	}
	var currentSnapshot SkillRunSnapshot
	if runContext != nil && strings.TrimSpace(runContext.FrozenRuntimeDir) != "" {
		resolved, err = loadFrozenSkillRunRuntime(loader, runContext, task.ResolvedWorkspace)
		if err != nil {
			return ctx, fmt.Errorf("restore frozen skill runtime: %w", err)
		}
		resolvedName = runContext.Snapshot.Name
		currentSnapshot = runContext.Snapshot
	} else {
		_, _ = loader.ReloadUserSkills(nil)
		lookupName := request.SkillName
		if runContext != nil {
			lookupName = runContext.Snapshot.Name
		}
		resolved, resolvedName, ok = loader.ResolveSkill(lookupName)
		if !ok || resolved == nil {
			return ctx, fmt.Errorf("skill %q is not installed", lookupName)
		}
		currentSnapshot, err = skillRunSnapshotForSkill(loader, resolved, resolvedName)
		if err != nil {
			return ctx, err
		}
	}
	if runContext != nil {
		frozen := runContext.Snapshot
		if currentSnapshot.ContentSHA256 != frozen.ContentSHA256 || currentSnapshot.RuntimeDigest != frozen.RuntimeDigest {
			return ctx, fmt.Errorf("frozen skill %q changed before activation; install content SHA-256 %s to resume", frozen.Name, frozen.ContentSHA256)
		}
		currentSnapshot = frozen
	} else {
		validated, validationErr := validateSkillRunParameters(resolved, request.Parameters)
		if validationErr != nil {
			return ctx, validationErr
		}
		request.Parameters = validated
		task.Request.SkillRun = request
	}
	if request.Version != "" && request.Version != currentSnapshot.Version {
		return ctx, fmt.Errorf("skill %q version conflict: requested %q, installed %q", resolvedName, request.Version, currentSnapshot.Version)
	}
	if record == nil {
		snapshotJSON, _ := json.Marshal(currentSnapshot)
		paramsJSON, _ := json.Marshal(request.Parameters)
		now := time.Now()
		record = &SkillRunRecord{
			RunID: skillRunID(task.ID), TaskID: task.ID, TenantID: task.TenantID, SubjectID: task.SubjectID,
			SkillName: currentSnapshot.Name, SkillVersion: currentSnapshot.Version, ContentSHA256: currentSnapshot.ContentSHA256,
			RuntimeDigest: currentSnapshot.RuntimeDigest, SkillSnapshot: string(snapshotJSON), ParametersJSON: string(paramsJSON),
			ActivationID: skillRunActivationCallID, Phase: skillRunPhaseResolving,
			ClientReference: request.ClientReference,
			StartedAt:       now, CreatedAt: now, UpdatedAt: now,
		}
		if g != nil && g.db != nil {
			if err := g.db.Create(record).Error; err != nil {
				if existing, loadErr := g.loadSkillRunRecord(task.ID); loadErr == nil {
					restored, decodeErr := decodeSkillRunContext(existing)
					if decodeErr == nil && strings.TrimSpace(restored.ActivatedOutput) != "" {
						return bindSkillRunContext(ctx, task.ResolvedWorkspace, restored), nil
					}
				}
				return ctx, fmt.Errorf("persist skill run snapshot: %w", err)
			}
		}
		runContext = &skillRunContext{Snapshot: currentSnapshot, Parameters: request.Parameters}
	}
	if runContext == nil {
		runContext, err = decodeSkillRunContext(record)
		if err != nil {
			return ctx, err
		}
	}
	frozenSkill, frozenRuntimeDir, freezeErr := prepareFrozenSkillRunRuntime(
		ctx, task, loader, resolved, runContext.Snapshot, runContext.FrozenRuntimeDir,
	)
	if freezeErr != nil {
		g.updateSkillRunFailure(task.ID, freezeErr)
		return ctx, fmt.Errorf("freeze skill %q runtime: %w", runContext.Snapshot.Name, freezeErr)
	}
	runContext.FrozenRuntimeDir = frozenRuntimeDir
	if g != nil && g.db != nil && frozenRuntimeDir != "" {
		if err := g.db.Model(&SkillRunRecord{}).Where("task_id = ?", task.ID).Updates(map[string]interface{}{
			"frozen_runtime_dir": frozenRuntimeDir, "updated_at": time.Now(),
		}).Error; err != nil {
			return ctx, fmt.Errorf("persist frozen Skill runtime: %w", err)
		}
	}

	call := ToolCall{ID: skillRunActivationCallID, Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": runContext.Snapshot.Name, "parameters": runContext.Parameters,
	}}
	activationCtx := withSkillRunExecutionOverride(ctx, runContext.Snapshot.Name, frozenSkill)
	run := NewAgentToolExecutor(llmAgent).RunSingleTool(activationCtx, call, RunSingleToolOptions{})
	result, runErr := run.Result, run.Err
	if runErr != nil || !ToolResultExecutionSucceeded(result, runErr) {
		failure := runErr
		if failure == nil {
			failure = fmt.Errorf("%s", truncate(strings.TrimSpace(result.Content), 800))
		}
		if _, approvalRequired := approvalcore.AsApprovalRequiredError(failure); approvalRequired {
			return ctx, fmt.Errorf("activate skill %q: %w", runContext.Snapshot.Name, failure)
		}
		g.updateSkillRunFailure(task.ID, failure)
		return ctx, fmt.Errorf("activate skill %q: %w", runContext.Snapshot.Name, failure)
	}
	activatedOutput := appendFrozenSkillRunRuntimeNotice(
		result.Content, task.ResolvedWorkspace, frozenRuntimeDir,
	)
	if activatedOutput == "" {
		failure := fmt.Errorf("skill activation returned empty output")
		g.updateSkillRunFailure(task.ID, failure)
		return ctx, failure
	}
	if g != nil && g.db != nil {
		updates := map[string]interface{}{
			"activated_output": activatedOutput, "activation_count": 1,
			"phase": skillRunPhaseActivated, "updated_at": time.Now(),
		}
		if err := g.db.Model(&SkillRunRecord{}).Where("task_id = ?", task.ID).Updates(updates).Error; err != nil {
			return ctx, fmt.Errorf("persist skill activation: %w", err)
		}
	}
	g.setSkillRunPhase(task.ID, skillRunPhaseRunning)
	runContext.ActivatedOutput = activatedOutput
	return bindSkillRunContext(ctx, task.ResolvedWorkspace, runContext), nil
}

func setSkillRunPhaseWithDB(db *gorm.DB, taskID, phase string) error {
	if db == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	return db.Model(&SkillRunRecord{}).Where("task_id = ?", taskID).
		Updates(map[string]interface{}{"phase": phase, "updated_at": time.Now()}).Error
}

func (g *GatewayService) setSkillRunPhase(taskID, phase string) {
	if g == nil || g.db == nil {
		return
	}
	_ = setSkillRunPhaseWithDB(g.db, taskID, phase)
}

func updateSkillRunTerminalWithDB(db *gorm.DB, taskID, phase, message string, now time.Time) error {
	if db == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	return db.Model(&SkillRunRecord{}).Where("task_id = ?", taskID).Updates(map[string]interface{}{
		"phase": phase, "error_msg": message, "completed_at": now, "updated_at": now,
	}).Error
}

func updateBoundSkillRunTerminalWithDB(db *gorm.DB, task *GatewayTask, phase, message string, now time.Time) error {
	if task == nil || task.Request == nil || task.Request.SkillRun == nil {
		return nil
	}
	return updateSkillRunTerminalWithDB(db, task.ID, phase, message, now)
}

func (g *GatewayService) updateSkillRunFailure(taskID string, runErr error) {
	if g == nil || g.db == nil {
		return
	}
	now := time.Now()
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	_ = updateSkillRunTerminalWithDB(g.db, taskID, skillRunPhaseFailed, message, now)
}

func finishSkillRunWithDB(db *gorm.DB, task *GatewayTask, result interface{}, runErr error) error {
	if task == nil || task.Request == nil || task.Request.SkillRun == nil || db == nil {
		return nil
	}
	if runErr != nil {
		return updateSkillRunTerminalWithDB(
			db, task.ID, skillRunPhaseFailed, runErr.Error(), time.Now(),
		)
	}
	if err := setSkillRunPhaseWithDB(db, task.ID, skillRunPhaseVerifying); err != nil {
		return err
	}
	candidates := skillRunArtifactPaths(result, task.ResolvedWorkspace)
	artifacts := make([]string, 0, len(candidates))
	verification := []SkillRunVerification{{
		Name: "agent_completion", Status: "passed", Detail: "Agent completed under the frozen Skill contract",
		Checked: time.Now().Format(time.RFC3339Nano),
	}}
	for _, artifact := range candidates {
		absolute := filepath.Join(task.ResolvedWorkspace, filepath.FromSlash(artifact))
		status := "passed"
		detail := artifact
		if info, statErr := os.Stat(absolute); statErr != nil {
			status = "failed"
			detail = artifact + ": " + statErr.Error()
		} else {
			artifacts = append(artifacts, artifact)
			if info.IsDir() {
				detail += " (directory)"
			}
		}
		verification = append(verification, SkillRunVerification{
			Name: "artifact_exists", Status: status, Detail: detail,
			Checked: time.Now().Format(time.RFC3339Nano),
		})
	}
	artifactJSON, _ := json.Marshal(artifacts)
	verificationJSON, _ := json.Marshal(verification)
	now := time.Now()
	return db.Model(&SkillRunRecord{}).Where("task_id = ?", task.ID).Updates(map[string]interface{}{
		"phase": skillRunPhaseCompleted, "artifacts_json": string(artifactJSON),
		"verification_json": string(verificationJSON), "error_msg": "",
		"completed_at": now, "updated_at": now,
	}).Error
}

func (g *GatewayService) finishSkillRun(task *GatewayTask, result interface{}, runErr error) SkillRunReceipt {
	if task == nil || task.Request == nil || task.Request.SkillRun == nil || g == nil || g.db == nil {
		return SkillRunReceipt{}
	}
	_ = finishSkillRunWithDB(g.db, task, result, runErr)
	record, _ := g.loadSkillRunRecord(task.ID)
	return skillRunReceiptFromRecord(record)
}

func (g *GatewayService) skillRunReceiptForTask(taskID string) *SkillRunReceipt {
	record, err := g.loadSkillRunRecord(strings.TrimSpace(taskID))
	if err != nil || record == nil {
		return nil
	}
	receipt := skillRunReceiptFromRecord(record)
	return &receipt
}

// ── Skill List ──────────────────────────────────────────────────────────────────

type gatewaySkillInfo struct {
	Name             string                       `json:"name"`
	Category         string                       `json:"category"`
	Aliases          []string                     `json:"aliases,omitempty"`
	Description      string                       `json:"description"`
	DisplayName      string                       `json:"display_name,omitempty"`
	ShortDescription string                       `json:"short_description,omitempty"`
	DefaultPrompt    string                       `json:"default_prompt,omitempty"`
	ModTime          string                       `json:"mod_time"`
	Source           string                       `json:"source"`
	RequiresRuntime  bool                         `json:"requires_runtime"`
	Author           string                       `json:"author,omitempty"`
	Type             skilldomain.SkillType        `json:"type,omitempty"`
	Parameters       []skilldomain.SkillParameter `json:"parameters,omitempty"`
	Slug             string                       `json:"slug,omitempty"`
	Owner            string                       `json:"owner,omitempty"`
	Version          string                       `json:"version,omitempty"`
	URL              string                       `json:"url,omitempty"`
	InstalledAt      string                       `json:"installed_at,omitempty"`
	TextOnly         bool                         `json:"text_only,omitempty"`
}

type gatewaySkillReloadResult struct {
	Loaders      int      `json:"loaders"`
	BuiltinCount int      `json:"builtin_count"`
	UserCount    int      `json:"user_count"`
	Directories  []string `json:"directories"`
}

func (g *GatewayService) loadedSkillLoaders() []*skilldomain.SkillsLoader {
	if g == nil || g.engine == nil || g.engine.agentFactory == nil {
		return []*skilldomain.SkillsLoader{}
	}

	factory := g.engine.agentFactory
	seen := make(map[*skilldomain.SkillsLoader]bool)
	loaders := make([]*skilldomain.SkillsLoader, 0)
	add := func(loader *skilldomain.SkillsLoader) {
		if loader == nil || seen[loader] {
			return
		}
		seen[loader] = true
		loaders = append(loaders, loader)
	}

	factory.mu.RLock()
	add(factory.skillsLoader)
	factory.mu.RUnlock()
	for _, agent := range factory.GetAllAgents() {
		if llmAgent, ok := agent.(*LLMAgent); ok {
			add(llmAgent.skillsLoader)
		}
	}
	return loaders
}

// reloadUserSkillsTransactional prepares every agent loader first and commits
// only after all configured directories validate. This prevents one bad user
// skill from leaving different agents with different catalogs.
func (g *GatewayService) reloadUserSkillsTransactional() (gatewaySkillReloadResult, error) {
	loaders := g.loadedSkillLoaders()
	if len(loaders) == 0 {
		return gatewaySkillReloadResult{}, nil
	}
	prepared := make([]*skilldomain.PreparedSkillReload, 0, len(loaders))
	for _, loader := range loaders {
		directories := append(loader.UserSkillDirs(), g.writableSkillDir())
		candidate, err := loader.PrepareUserSkillReload(directories)
		if err != nil {
			return gatewaySkillReloadResult{}, err
		}
		prepared = append(prepared, candidate)
	}

	result := gatewaySkillReloadResult{Loaders: len(prepared)}
	dirSet := make(map[string]bool)
	for _, candidate := range prepared {
		committed := candidate.Commit()
		if committed.BuiltinCount > result.BuiltinCount {
			result.BuiltinCount = committed.BuiltinCount
		}
		result.UserCount += committed.UserCount
		for _, dir := range committed.Directories {
			key := strings.ToLower(filepath.Clean(dir))
			if !dirSet[key] {
				dirSet[key] = true
				result.Directories = append(result.Directories, dir)
			}
		}
	}
	sort.Strings(result.Directories)
	return result, nil
}

func (g *GatewayService) skillSearchDirs() []string {
	// The writable directory already resolves to workspace/skills (or the
	// executable-adjacent fallback). Reading an additional cwd-relative
	// "skills" directory leaks unrelated skills when the configured workspace
	// differs from the process working directory, including in isolated tests.
	dirs := []string{g.writableSkillDir()}
	if g != nil && g.config != nil {
		if workspace := strings.TrimSpace(g.config.Workspace); workspace != "" {
			dirs = append(dirs, filepath.Join(workspace, "skills"))
		}
		if globalDir := strings.TrimSpace(g.config.SkillsConfig.GlobalDir); globalDir != "" {
			dirs = append(dirs, globalDir)
		}
		for _, agent := range g.config.Agents {
			dirs = append(dirs, agent.ExternalSkills...)
		}
	}

	seen := make(map[string]bool)
	unique := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, clean)
	}
	return unique
}

func skillModTime(path string) string {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Format("01-02 15:04")
	}
	return ""
}

func (g *GatewayService) collectSkillInfo() []gatewaySkillInfo {
	byName := make(map[string]gatewaySkillInfo)
	for _, loader := range g.loadedSkillLoaders() {
		snapshots := loader.SnapshotSkills()
		conflicts := skilldomain.SkillMentionAliasConflicts(snapshots)
		for _, skill := range snapshots {
			aliases := make([]string, 0, len(skill.Aliases))
			for _, alias := range skill.Aliases {
				if len(conflicts[strings.ToLower(skilldomain.NormalizeMentionAlias(alias))]) == 0 {
					aliases = append(aliases, alias)
				}
			}
			modPath := ""
			if skill.SourceDir != "" {
				modPath = filepath.Join(skill.SourceDir, "SKILL.md")
			}
			info := gatewaySkillInfo{
				Name:             skill.Name,
				Category:         skill.Category,
				Aliases:          aliases,
				Description:      skill.Description,
				DisplayName:      skill.Interface.DisplayName,
				ShortDescription: skill.Interface.ShortDescription,
				DefaultPrompt:    skill.Interface.DefaultPrompt,
				ModTime:          skillModTime(modPath),
				Source:           skill.Source,
				RequiresRuntime:  skill.RequiresRuntime,
				Author:           skill.Author,
				Type:             skill.Type,
				Parameters:       append([]skilldomain.SkillParameter(nil), skill.Parameters...),
				Version:          skill.Version,
			}
			if skill.SourceDir != "" {
				if origin, ok := readRemoteSkillOrigin(skill.SourceDir); ok {
					info.Source = origin.Source
					info.Slug = origin.Slug
					info.Owner = origin.Owner
					info.Version = origin.Version
					info.URL = origin.URL
					info.TextOnly = origin.TextOnly
					if !origin.InstalledAt.IsZero() {
						info.InstalledAt = origin.InstalledAt.Format(time.RFC3339)
					}
				}
			}
			byName[skill.Name] = info
		}
	}

	metadataLoader := &skilldomain.SkillsLoader{}
	for _, dir := range g.skillSearchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if _, loaded := byName[name]; loaded {
				continue
			}
			skillFile := filepath.Join(dir, name, "SKILL.md")
			data, err := os.ReadFile(skillFile)
			if err != nil {
				continue
			}
			metadata := metadataLoader.ParseMetadata(data, skillFile)
			interfaceMetadata := skillpath.LoadInterfaceMetadataFromDisk(filepath.Join(dir, name))
			info := gatewaySkillInfo{
				Name:             name,
				Category:         skilldomain.SkillCatalogCategory(metadata, interfaceMetadata.DisplayName, nil, ""),
				Aliases:          append([]string(nil), metadata.Aliases...),
				Description:      metadata.Description,
				DisplayName:      interfaceMetadata.DisplayName,
				ShortDescription: interfaceMetadata.ShortDescription,
				DefaultPrompt:    interfaceMetadata.DefaultPrompt,
				ModTime:          skillModTime(skillFile),
				Source:           "user",
				Author:           metadata.Author,
				Type:             skilldomain.NormalizeSkillType(string(metadata.Type)),
				Parameters:       append([]skilldomain.SkillParameter(nil), metadata.Parameters...),
				Version:          metadata.Version,
			}
			if origin, ok := readRemoteSkillOrigin(filepath.Join(dir, name)); ok {
				info.Source = origin.Source
				info.Slug = origin.Slug
				info.Owner = origin.Owner
				info.Version = origin.Version
				info.URL = origin.URL
				info.TextOnly = origin.TextOnly
				if !origin.InstalledAt.IsZero() {
					info.InstalledAt = origin.InstalledAt.Format(time.RFC3339)
				}
			}
			byName[name] = info
		}
	}

	skills := make([]gatewaySkillInfo, 0, len(byName))
	for _, skill := range byName {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills
}

func (g *GatewayService) handleSkillList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	skills := g.collectSkillInfo()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"count":       len(skills),
		"skills":      skills,
		"sources":     g.remoteSkills().Sources(),
		"install_dir": g.writableSkillDir(),
		"policy": map[string]interface{}{
			"text_only":         true,
			"executes_scripts":  false,
			"downloads_bundles": false,
			"max_installed":     maxRemoteInstalledSkills,
		},
	})
}

// ── Skill Detail ───────────────────────────────────────────────────────────────

func writeSkillAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": message})
}

func (g *GatewayService) writableSkillDir() string {
	if g != nil {
		if directory := strings.TrimSpace(g.skillInstallDir); directory != "" {
			return filepath.Clean(directory)
		}
	}
	if g != nil && g.config != nil {
		if workspace := strings.TrimSpace(g.config.Workspace); workspace != "" {
			return filepath.Join(workspace, "skills")
		}
	}
	return skillpath.ExecutableSkillsDir()
}

func (g *GatewayService) writableSkillTarget(rawName string) (string, string, error) {
	name, ok := skilldomain.NormalizeName(rawName)
	if !ok {
		return "", "", fmt.Errorf("valid skill name required")
	}
	root, err := filepath.Abs(g.writableSkillDir())
	if err != nil {
		return "", "", fmt.Errorf("resolve skills directory: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", "", fmt.Errorf("resolve skill path: %w", err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("skill path escapes user skills directory")
	}
	return name, target, nil
}

func (g *GatewayService) loadedSkillSnapshot(name string) (skilldomain.SkillSnapshot, bool) {
	for _, loader := range g.loadedSkillLoaders() {
		for _, skill := range loader.SnapshotSkills() {
			if skill.Name == name {
				return skill, true
			}
		}
	}
	return skilldomain.SkillSnapshot{}, false
}

func (g *GatewayService) handleSkillDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name, ok := skilldomain.NormalizeName(r.URL.Query().Get("name"))
	if !ok {
		writeSkillAPIError(w, http.StatusBadRequest, "valid skill name required")
		return
	}
	if skill, ok := g.loadedSkillSnapshot(name); ok {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "name": name, "content": skill.Raw, "source": skill.Source,
			"display_name":      skill.Interface.DisplayName,
			"short_description": skill.Interface.ShortDescription,
			"default_prompt":    skill.Interface.DefaultPrompt,
		})
		return
	}
	for _, dir := range g.skillSearchDirs() {
		skillDir := filepath.Join(dir, name)
		data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err == nil {
			interfaceMetadata := skillpath.LoadInterfaceMetadataFromDisk(skillDir)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true, "name": name, "content": string(data), "source": "user",
				"display_name":      interfaceMetadata.DisplayName,
				"short_description": interfaceMetadata.ShortDescription,
				"default_prompt":    interfaceMetadata.DefaultPrompt,
			})
			return
		}
	}
	writeSkillAPIError(w, http.StatusNotFound, "skill not found")
}

// ── Skill Save ─────────────────────────────────────────────────────────────────

func (g *GatewayService) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := decoder.Decode(&req); err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name, dir, err := g.writableSkillTarget(req.Name)
	if err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeSkillAPIError(w, http.StatusBadRequest, "content required")
		return
	}
	validator := skilldomain.NewSkillsLoader(filepath.Dir(filepath.Dir(dir)), "", "")
	if _, _, err := validator.ValidateUserSkillDocument(name, filepath.Join(dir, "SKILL.md"), []byte(req.Content)); err != nil {
		writeSkillAPIError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	dirInfo, dirStatErr := os.Stat(dir)
	dirExisted := dirStatErr == nil && dirInfo.IsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "create skill directory: "+err.Error())
		return
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	mode := os.FileMode(0644)
	var oldContent []byte
	fileExisted := false
	if info, statErr := os.Stat(skillFile); statErr == nil {
		mode = info.Mode().Perm()
		oldContent, _ = os.ReadFile(skillFile)
		fileExisted = true
		workspacetools.PreEditBackup(skillFile, r.Context())
	}
	if err := fsutil.WriteAtomic(skillFile, []byte(req.Content), mode); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "save skill: "+err.Error())
		return
	}
	reloaded := 0
	if g.engine != nil && g.engine.agentFactory != nil {
		result, reloadErr := g.reloadUserSkillsTransactional()
		if reloadErr != nil {
			if fileExisted {
				_ = fsutil.WriteAtomic(skillFile, oldContent, mode)
			} else if dirExisted {
				_ = os.Remove(skillFile)
			} else {
				_ = os.RemoveAll(dir)
			}
			writeSkillAPIError(w, http.StatusUnprocessableEntity, "skill save rolled back because the complete catalog did not validate: "+reloadErr.Error())
			return
		}
		reloaded = result.Loaders
	}
	eventbus.PushSystemMessage(fmt.Sprintf("Skill %s saved and activated on %d loaders", name, reloaded), "success")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name, "reloaded": reloaded})
}

// ── Skill Delete ───────────────────────────────────────────────────────────────

func (g *GatewayService) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	name, target, err := g.writableSkillTarget(r.URL.Query().Get("name"))
	if err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		writeSkillAPIError(w, http.StatusNotFound, "skill not found")
		return
	}
	if err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "inspect skill: "+err.Error())
		return
	}
	if !info.IsDir() {
		writeSkillAPIError(w, http.StatusConflict, "skill path is not a directory")
		return
	}
	archiveRoot := filepath.Join(filepath.Dir(target), ".archived")
	if err := os.MkdirAll(archiveRoot, 0755); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "prepare recoverable delete: "+err.Error())
		return
	}
	archiveTarget := filepath.Join(archiveRoot, fmt.Sprintf(".%s.deleting-%d", name, time.Now().UnixNano()))
	if err := os.Rename(target, archiveTarget); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "stage skill delete: "+err.Error())
		return
	}
	reloaded := 0
	if g.engine != nil && g.engine.agentFactory != nil {
		result, reloadErr := g.reloadUserSkillsTransactional()
		if reloadErr != nil {
			if restoreErr := os.Rename(archiveTarget, target); restoreErr != nil {
				writeSkillAPIError(w, http.StatusInternalServerError, fmt.Sprintf("skill delete reload failed: %v; restore failed: %v", reloadErr, restoreErr))
				return
			}
			writeSkillAPIError(w, http.StatusUnprocessableEntity, "skill delete rolled back because the complete catalog did not validate: "+reloadErr.Error())
			return
		}
		reloaded = result.Loaders
	}
	if err := os.RemoveAll(archiveTarget); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "skill deactivated but archived files could not be removed: "+err.Error())
		return
	}
	_ = os.Remove(archiveRoot)
	eventbus.PushSystemMessage("Skill "+name+" deleted", "warn")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "deleted": name, "reloaded": reloaded})
}

// stripYAMLQuotes removes surrounding double/single quotes from a YAML string value.
func stripYAMLQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

const maxRemoteInstalledSkills = 32

var remoteSkillInstallMu sync.Mutex

func (g *GatewayService) remoteSkills() *skilldomain.RemoteSkillRegistry {
	if g != nil && g.remoteSkillRegistry != nil {
		return g.remoteSkillRegistry
	}
	return skilldomain.NewRemoteSkillRegistry()
}

func (g *GatewayService) handleRemoteSkillSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"sources": g.remoteSkills().Sources(),
		"policy": map[string]interface{}{
			"text_only":         true,
			"executes_scripts":  false,
			"downloads_bundles": false,
			"max_installed":     maxRemoteInstalledSkills,
		},
		"install_dir": g.writableSkillDir(),
	})
}

func (g *GatewayService) handleRemoteSkillSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	results, err := g.remoteSkills().Search(r.Context(), r.URL.Query().Get("source"), r.URL.Query().Get("q"))
	if err != nil {
		writeRemoteSkillAPIError(w, err)
		return
	}
	origins := loadRemoteSkillOrigins(g.writableSkillDir())
	for index := range results {
		results[index].Installed = remoteSkillOriginExists(
			origins,
			results[index].Source,
			results[index].Slug,
			results[index].Owner,
		)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "results": results})
}

func (g *GatewayService) handleRemoteSkillInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeSkillAPIError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
		writeSkillAPIError(w, http.StatusForbidden, "cross-origin skill installation is not allowed")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	var input skilldomain.RemoteSkillInstallInput
	if err := decoder.Decode(&input); err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, "invalid skill installation request")
		return
	}
	if err := ensureJSONBodyConsumed(decoder); err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, "invalid skill installation request")
		return
	}
	normalized, err := skilldomain.NormalizeRemoteSkillInstallInput(input)
	if err != nil {
		writeRemoteSkillAPIError(w, err)
		return
	}

	remoteSkillInstallMu.Lock()
	defer remoteSkillInstallMu.Unlock()

	root := g.writableSkillDir()
	origins := loadRemoteSkillOrigins(root)
	if remoteSkillOriginExists(origins, normalized.Source, normalized.Slug, normalized.Owner) {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: %s", skilldomain.ErrRemoteSkillAlreadyInstalled, normalized.Slug))
		return
	}
	if len(origins) >= maxRemoteInstalledSkills {
		writeRemoteSkillAPIError(w, fmt.Errorf(
			"%w: at most %d marketplace skills can be installed",
			skilldomain.ErrRemoteSkillInvalidInput,
			maxRemoteInstalledSkills,
		))
		return
	}

	document, err := g.remoteSkills().Fetch(r.Context(), normalized)
	if err != nil {
		writeRemoteSkillAPIError(w, err)
		return
	}
	origins = loadRemoteSkillOrigins(root)
	if remoteSkillOriginExists(origins, document.Origin.Source, document.Origin.Slug, document.Origin.Owner) {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: %s", skilldomain.ErrRemoteSkillAlreadyInstalled, normalized.Slug))
		return
	}

	name, targetDir, err := g.writableSkillTarget(document.Name)
	if err != nil {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: %v", skilldomain.ErrRemoteSkillInvalidInput, err))
		return
	}
	if _, loaded := g.loadedSkillSnapshot(name); loaded {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: local skill name %s", skilldomain.ErrRemoteSkillAlreadyInstalled, name))
		return
	}
	if _, err := os.Lstat(targetDir); err == nil {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: local skill name %s", skilldomain.ErrRemoteSkillAlreadyInstalled, name))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeSkillAPIError(w, http.StatusInternalServerError, "inspect skill target: "+err.Error())
		return
	}

	validator := skilldomain.NewSkillsLoader(filepath.Dir(root), "", "")
	if _, _, err := validator.ValidateUserSkillDocument(name, filepath.Join(targetDir, "SKILL.md"), []byte(document.Content)); err != nil {
		writeRemoteSkillAPIError(w, fmt.Errorf("%w: %v", skilldomain.ErrRemoteSkillInvalidInput, err))
		return
	}
	if err := persistRemoteSkill(root, targetDir, document); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeRemoteSkillAPIError(w, fmt.Errorf("%w: local skill name %s", skilldomain.ErrRemoteSkillAlreadyInstalled, name))
			return
		}
		writeSkillAPIError(w, http.StatusInternalServerError, "save installed skill: "+err.Error())
		return
	}

	reloaded := 0
	if g.engine != nil && g.engine.agentFactory != nil {
		result, reloadErr := g.reloadUserSkillsTransactional()
		if reloadErr != nil {
			_ = os.RemoveAll(targetDir)
			writeSkillAPIError(
				w,
				http.StatusUnprocessableEntity,
				"installed skill failed validation; installation rolled back: "+reloadErr.Error(),
			)
			return
		}
		reloaded = result.Loaders
	}
	if g.logger != nil {
		g.logger.Printf(
			"[Skill] Installed text-only marketplace skill %s from %s/%s into %s",
			name,
			document.Origin.Source,
			document.Origin.Slug,
			targetDir,
		)
	}
	eventbus.PushSystemMessage(fmt.Sprintf("Skill %s installed from %s", name, document.Origin.Source), "success")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"name":        name,
		"description": document.Description,
		"source":      document.Origin.Source,
		"slug":        document.Origin.Slug,
		"owner":       document.Origin.Owner,
		"version":     document.Origin.Version,
		"url":         document.Origin.URL,
		"path":        targetDir,
		"install_dir": root,
		"text_only":   true,
		"reloaded":    reloaded,
	})
}

func ensureJSONBodyConsumed(decoder *json.Decoder) error {
	var trailing interface{}
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func persistRemoteSkill(root, targetDir string, document skilldomain.RemoteSkillDocument) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absoluteTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("resolved skill path escaped the install directory")
	}
	if err := os.MkdirAll(absoluteRoot, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(absoluteRoot), ".openkitty-skill-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "SKILL.md"), []byte(document.Content), 0o644); err != nil {
		return err
	}
	originData, err := json.MarshalIndent(document.Origin, "", "  ")
	if err != nil {
		return err
	}
	originData = append(originData, '\n')
	if err := os.WriteFile(filepath.Join(staging, "origin.json"), originData, 0o644); err != nil {
		return err
	}
	if _, err := os.Lstat(absoluteTarget); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(staging, absoluteTarget)
}

func loadRemoteSkillOrigins(root string) []skilldomain.RemoteSkillOrigin {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	origins := make([]skilldomain.RemoteSkillOrigin, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if origin, ok := readRemoteSkillOrigin(filepath.Join(root, entry.Name())); ok {
			origins = append(origins, origin)
		}
	}
	return origins
}

func readRemoteSkillOrigin(skillDir string) (skilldomain.RemoteSkillOrigin, bool) {
	file, err := os.Open(filepath.Join(skillDir, "origin.json"))
	if err != nil {
		return skilldomain.RemoteSkillOrigin{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16<<10))
	if err != nil {
		return skilldomain.RemoteSkillOrigin{}, false
	}
	var origin skilldomain.RemoteSkillOrigin
	if json.Unmarshal(data, &origin) != nil {
		return skilldomain.RemoteSkillOrigin{}, false
	}
	origin.Source = skilldomain.NormalizeRemoteSkillSource(origin.Source)
	origin.Slug = strings.ToLower(strings.TrimSpace(origin.Slug))
	origin.Owner = strings.TrimPrefix(strings.TrimSpace(origin.Owner), "@")
	if !skilldomain.KnownRemoteSkillSource(origin.Source) || !skilldomain.ValidRemoteSkillSlug.MatchString(origin.Slug) {
		return skilldomain.RemoteSkillOrigin{}, false
	}
	if origin.Source == skilldomain.SourceClawHub && !skilldomain.ValidRemoteSkillOwner.MatchString(origin.Owner) {
		return skilldomain.RemoteSkillOrigin{}, false
	}
	return origin, true
}

func remoteSkillOriginExists(origins []skilldomain.RemoteSkillOrigin, source, slug, owner string) bool {
	source = skilldomain.NormalizeRemoteSkillSource(source)
	slug = strings.ToLower(strings.TrimSpace(slug))
	owner = strings.TrimPrefix(strings.TrimSpace(owner), "@")
	for _, installed := range origins {
		if installed.Source != source || installed.Slug != slug {
			continue
		}
		if source != skilldomain.SourceClawHub || strings.EqualFold(installed.Owner, owner) {
			return true
		}
	}
	return false
}

func writeRemoteSkillAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, skilldomain.ErrRemoteSkillInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, skilldomain.ErrRemoteSkillAlreadyInstalled):
		status = http.StatusConflict
	case errors.Is(err, skilldomain.ErrRemoteSkillUnsafe):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, skilldomain.ErrRemoteSkillUpstream):
		status = http.StatusBadGateway
	}
	writeSkillAPIError(w, status, err.Error())
}

// normalizeBuiltinSkillProbeCalls turns guessed filesystem probes for
// embedded skills into the supported use_skill handoff.
func (a *LLMAgent) normalizeBuiltinSkillProbeCalls(calls []ToolCall) ([]ToolCall, bool) {
	if a == nil || a.skillsLoader == nil || len(calls) == 0 {
		return calls, false
	}
	out := append([]ToolCall(nil), calls...)
	changed := false
	for i := range out {
		candidate := routing.BuiltinSkillProbeCandidate(out[i].Name, out[i].Arguments)
		if candidate == "" {
			continue
		}
		skill, resolvedName, ok := a.skillsLoader.ResolveSkill(candidate)
		if !ok || skill == nil || !strings.EqualFold(strings.TrimSpace(skill.Source), "builtin") {
			continue
		}
		out[i] = ToolCall{
			ID:   out[i].ID,
			Name: "use_skill",
			Arguments: map[string]interface{}{
				"skill_name": resolvedName,
			},
		}
		changed = true
	}
	return out, changed
}

func (g *GatewayService) skillMentionLoader(agentName string) *skilldomain.SkillsLoader {
	if g == nil {
		return nil
	}
	if g.engine != nil {
		var agent Agent
		var found bool
		if strings.TrimSpace(agentName) == "" {
			_, agent, found = g.engine.DefaultAgent()
		} else {
			_, agent, found = g.engine.FindAgent(strings.TrimSpace(agentName))
		}
		if found {
			if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.skillsLoader != nil {
				return llmAgent.skillsLoader
			}
		}
	}
	for _, loader := range g.loadedSkillLoaders() {
		if loader != nil {
			return loader
		}
	}
	return nil
}

func (g *GatewayService) resolveSkillMention(message, agentName string) (*SkillRunRequest, string, bool, error) {
	resolved, task, matched, err := skilldomain.ResolveExplicitMention(message, g.skillMentionLoader(agentName))
	if err != nil || !matched || resolved == nil {
		return nil, task, matched, err
	}
	return &SkillRunRequest{
		SkillName:       resolved.SkillName,
		Parameters:      map[string]interface{}{},
		ClientReference: resolved.ClientReference,
	}, task, true, nil
}

// bindSkillMentionRequest is shared by transports that construct GatewayTask
// directly instead of entering through /api/chat. Callers must run their
// existing Codex/AI-employee routing first so those public @names retain
// precedence.
func (g *GatewayService) bindSkillMentionRequest(req *MessageRequest) (bool, error) {
	if req == nil {
		return false, nil
	}
	return g.bindSkillMentionRequestFrom(req, req.Message)
}

func (g *GatewayService) bindSkillMentionRequestFrom(req *MessageRequest, visibleMessage string) (bool, error) {
	if req == nil || req.SkillRun != nil {
		return false, nil
	}
	run, task, matched, err := g.resolveSkillMention(visibleMessage, req.AgentName)
	if err != nil || !matched {
		return matched, err
	}
	run, err = normalizeSkillRunRequest(run)
	if err != nil {
		return false, err
	}
	req.SkillRun = run
	req.TaskType = "skill"
	req.Message = task
	return true, nil
}
