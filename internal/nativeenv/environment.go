package nativeenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/fsutil"
	"mac/internal/processutil"
)

const (
	nativeEnvironmentSchemaVersion = 1
	DefaultCacheMaxMB              = int64(10 * 1024)
	DefaultCacheTTLHours           = 14 * 24
	DefaultPrepareTimeoutSeconds   = 15 * 60
	maxNativeDependencyFileBytes   = int64(64 << 20)
)

// NativeExecutionConfig controls the host-native, content-addressed
// environment cache. It is opt-in so existing installations keep their
// current command execution behavior until explicitly enabled.
type NativeExecutionConfig struct {
	Enabled               bool   `json:"enabled" yaml:"enabled"`
	CacheDir              string `json:"cache_dir,omitempty" yaml:"cache_dir,omitempty"`
	MaxCacheMB            int64  `json:"max_cache_mb,omitempty" yaml:"max_cache_mb,omitempty"`
	TTLHours              int    `json:"ttl_hours,omitempty" yaml:"ttl_hours,omitempty"`
	PrepareTimeoutSeconds int    `json:"prepare_timeout_seconds,omitempty" yaml:"prepare_timeout_seconds,omitempty"`
}

func NormalizeExecutionConfig(config NativeExecutionConfig) NativeExecutionConfig {
	if config.MaxCacheMB <= 0 {
		config.MaxCacheMB = DefaultCacheMaxMB
	}
	if config.TTLHours <= 0 {
		config.TTLHours = DefaultCacheTTLHours
	}
	if config.PrepareTimeoutSeconds <= 0 {
		config.PrepareTimeoutSeconds = DefaultPrepareTimeoutSeconds
	}
	return config
}

type NativeDependencyDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type NativeEnvironmentMetadata struct {
	SchemaVersion   int                      `json:"schema_version"`
	ID              string                   `json:"id"`
	Ecosystem       string                   `json:"ecosystem"`
	Scope           string                   `json:"scope"`
	Platform        string                   `json:"platform"`
	Architecture    string                   `json:"architecture"`
	RuntimePath     string                   `json:"runtime_path"`
	RuntimeVersion  string                   `json:"runtime_version"`
	Dependencies    []NativeDependencyDigest `json:"dependencies"`
	SetupSHA256     string                   `json:"setup_sha256"`
	PathEntries     []string                 `json:"path_entries,omitempty"`
	Environment     map[string]string        `json:"environment,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	LastUsedAt      time.Time                `json:"last_used_at"`
	SizeBytes       int64                    `json:"size_bytes"`
	ProjectIdentity string                   `json:"project_identity,omitempty"`
}

type NativeEnvironmentSpec struct {
	Ecosystem       string
	ProjectDir      string
	Runtime         string
	DependencyFiles []string
	SetupCommand    string
	Scope           string
	PathEntries     []string
	Environment     map[string]string
}

type resolvedNativeEnvironmentSpec struct {
	NativeEnvironmentSpec
	ID              string
	RuntimePath     string
	RuntimeVersion  string
	Dependencies    []NativeDependencyDigest
	SetupSHA256     string
	ProjectIdentity string
}

type NativeEnvironmentInspection struct {
	ID             string                   `json:"environment_id"`
	CacheHit       bool                     `json:"cache_hit"`
	CacheDir       string                   `json:"cache_dir"`
	EntryDir       string                   `json:"entry_dir"`
	Ecosystem      string                   `json:"ecosystem"`
	Scope          string                   `json:"scope"`
	RuntimePath    string                   `json:"runtime_path"`
	RuntimeVersion string                   `json:"runtime_version"`
	Dependencies   []NativeDependencyDigest `json:"dependencies"`
	SizeBytes      int64                    `json:"size_bytes,omitempty"`
	LastUsedAt     time.Time                `json:"last_used_at,omitempty"`
}

type NativePruneEntry struct {
	ID        string `json:"environment_id"`
	SizeBytes int64  `json:"size_bytes"`
	Reason    string `json:"reason"`
}

type NativePruneReport struct {
	DryRun      bool               `json:"dry_run"`
	BeforeBytes int64              `json:"before_bytes"`
	AfterBytes  int64              `json:"after_bytes"`
	Removed     []NativePruneEntry `json:"removed,omitempty"`
	SkippedBusy []string           `json:"skipped_busy,omitempty"`
}

type NativeEnvironmentManager struct {
	workspace string
	cacheDir  string
	config    NativeExecutionConfig
	now       func() time.Time

	keyLocks sync.Map
	activeMu sync.Mutex
	active   map[string]int
	pruneMu  sync.Mutex
}

func NewNativeEnvironmentManager(workspace string, config NativeExecutionConfig) *NativeEnvironmentManager {
	config = NormalizeExecutionConfig(config)
	absoluteWorkspace, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || strings.TrimSpace(absoluteWorkspace) == "" {
		absoluteWorkspace = filepath.Clean(workspace)
	}
	cacheDir := strings.TrimSpace(config.CacheDir)
	if cacheDir == "" {
		cacheDir = filepath.Join(openKittyHome(), "cache", "native-envs")
	} else if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(absoluteWorkspace, cacheDir)
	}
	if absolute, absErr := filepath.Abs(cacheDir); absErr == nil {
		cacheDir = absolute
	}
	return &NativeEnvironmentManager{
		workspace: absoluteWorkspace,
		cacheDir:  filepath.Clean(cacheDir),
		config:    config,
		now:       time.Now,
		active:    make(map[string]int),
	}
}

func (m *NativeEnvironmentManager) Enabled() bool {
	return m != nil && m.config.Enabled
}

func (m *NativeEnvironmentManager) CacheDir() string {
	if m == nil {
		return ""
	}
	return m.cacheDir
}

func (m *NativeEnvironmentManager) Inspect(ctx context.Context, spec NativeEnvironmentSpec) (NativeEnvironmentInspection, error) {
	resolved, err := m.resolveSpec(ctx, spec)
	if err != nil {
		return NativeEnvironmentInspection{}, err
	}
	inspection := NativeEnvironmentInspection{
		ID:             resolved.ID,
		CacheDir:       m.cacheDir,
		EntryDir:       m.entryDir(resolved.ID),
		Ecosystem:      resolved.Ecosystem,
		Scope:          resolved.Scope,
		RuntimePath:    resolved.RuntimePath,
		RuntimeVersion: resolved.RuntimeVersion,
		Dependencies:   append([]NativeDependencyDigest(nil), resolved.Dependencies...),
	}
	if metadata, loadErr := m.loadMetadata(resolved.ID); loadErr == nil {
		inspection.CacheHit = true
		inspection.SizeBytes = metadata.SizeBytes
		inspection.LastUsedAt = metadata.LastUsedAt
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return NativeEnvironmentInspection{}, loadErr
	}
	return inspection, nil
}

func (m *NativeEnvironmentManager) Prepare(ctx context.Context, spec NativeEnvironmentSpec) (NativeEnvironmentMetadata, bool, string, error) {
	if !m.Enabled() {
		return NativeEnvironmentMetadata{}, false, "", errors.New("native execution environments are disabled")
	}
	resolved, err := m.resolveSpec(ctx, spec)
	if err != nil {
		return NativeEnvironmentMetadata{}, false, "", err
	}

	lock := m.lockForKey(resolved.ID)
	lock.Lock()
	defer lock.Unlock()

	prepareCtx, cancel := context.WithTimeout(ctx, time.Duration(m.config.PrepareTimeoutSeconds)*time.Second)
	defer cancel()
	releaseProcessLock, err := m.acquireProcessLock(prepareCtx, resolved.ID)
	if err != nil {
		return NativeEnvironmentMetadata{}, false, "", err
	}
	defer releaseProcessLock()

	if metadata, loadErr := m.loadMetadata(resolved.ID); loadErr == nil {
		metadata.LastUsedAt = m.now().UTC()
		_ = m.writeMetadata(m.entryDir(resolved.ID), metadata)
		return metadata, true, "cache hit; setup command was not executed", nil
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return NativeEnvironmentMetadata{}, false, "", loadErr
	}

	stagingRoot := filepath.Join(m.cacheDir, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return NativeEnvironmentMetadata{}, false, "", fmt.Errorf("create native environment staging root: %w", err)
	}
	stageDir, err := os.MkdirTemp(stagingRoot, resolved.ID+"-")
	if err != nil {
		return NativeEnvironmentMetadata{}, false, "", fmt.Errorf("create native environment staging directory: %w", err)
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stageDir)
		}
	}()

	payloadDir := filepath.Join(stageDir, "payload")
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return NativeEnvironmentMetadata{}, false, "", fmt.Errorf("create native environment payload: %w", err)
	}
	expandedCommand := expandNativeSetupCommand(
		resolved.SetupCommand, resolved.RuntimePath, resolved.ProjectDir, payloadDir, m.cacheDir)
	cmd := processutil.ShellCommand(prepareCtx, expandedCommand)
	cmd.Dir = resolved.ProjectDir
	cmd.Env = mergeCommandEnvironment(os.Environ(), map[string]string{
		"OPENKITTY_NATIVE_ENV_DIR": payloadDir,
		"OPENKITTY_PROJECT_DIR":    resolved.ProjectDir,
	})
	output, runErr := cmd.CombinedOutput()
	safeOutput := compactNativeCommandOutput(string(output), 4000)
	if runErr != nil {
		if prepareCtx.Err() == context.DeadlineExceeded {
			return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf(
				"native environment setup timed out after %ds", m.config.PrepareTimeoutSeconds)
		}
		return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf("native environment setup failed: %w", runErr)
	}
	empty, err := directoryIsEmpty(payloadDir)
	if err != nil {
		return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf("inspect native environment payload: %w", err)
	}
	if empty {
		return NativeEnvironmentMetadata{}, false, safeOutput, errors.New(
			"setup command completed but produced no files under {env_dir}; refusing to cache an empty environment")
	}

	now := m.now().UTC()
	metadata := NativeEnvironmentMetadata{
		SchemaVersion:   nativeEnvironmentSchemaVersion,
		ID:              resolved.ID,
		Ecosystem:       resolved.Ecosystem,
		Scope:           resolved.Scope,
		Platform:        runtime.GOOS,
		Architecture:    runtime.GOARCH,
		RuntimePath:     resolved.RuntimePath,
		RuntimeVersion:  resolved.RuntimeVersion,
		Dependencies:    append([]NativeDependencyDigest(nil), resolved.Dependencies...),
		SetupSHA256:     resolved.SetupSHA256,
		PathEntries:     append([]string(nil), resolved.PathEntries...),
		Environment:     cloneStringMap(resolved.Environment),
		CreatedAt:       now,
		LastUsedAt:      now,
		ProjectIdentity: resolved.ProjectIdentity,
	}
	if err := m.writeMetadata(stageDir, metadata); err != nil {
		return NativeEnvironmentMetadata{}, false, safeOutput, err
	}
	metadata.SizeBytes, err = directorySize(stageDir)
	if err != nil {
		return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf("measure native environment: %w", err)
	}
	if err := m.writeMetadata(stageDir, metadata); err != nil {
		return NativeEnvironmentMetadata{}, false, safeOutput, err
	}

	entriesRoot := filepath.Join(m.cacheDir, "entries")
	if err := os.MkdirAll(entriesRoot, 0o755); err != nil {
		return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf("create native environment entry root: %w", err)
	}
	finalDir := m.entryDir(resolved.ID)
	if err := publishNativeEnvironmentDirectory(prepareCtx, stageDir, finalDir); err != nil {
		if existing, loadErr := m.loadMetadata(resolved.ID); loadErr == nil {
			return existing, true, "another process prepared the same environment first", nil
		}
		return NativeEnvironmentMetadata{}, false, safeOutput, fmt.Errorf("publish native environment: %w", err)
	}
	stageOwned = false

	_, _ = m.prune(false, resolved.ID)
	return metadata, false, safeOutput, nil
}

func publishNativeEnvironmentDirectory(ctx context.Context, source, target string) error {
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		if err := os.Rename(source, target); err == nil {
			return nil
		} else {
			lastErr = err
			if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) {
				return err
			}
		}
		delay := time.Duration(attempt+1) * 50 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("publish native environment while waiting for Windows file handles: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return lastErr
}

// Activate loads one completed environment and returns process environment
// overrides plus a release function. Callers must release after the command so
// in-process pruning cannot remove an environment that is being used.
func (m *NativeEnvironmentManager) Activate(id, projectDir string) (map[string]string, NativeEnvironmentMetadata, func(), error) {
	if !m.Enabled() {
		return nil, NativeEnvironmentMetadata{}, func() {}, errors.New("native execution environments are disabled")
	}
	if !validNativeEnvironmentID(id) {
		return nil, NativeEnvironmentMetadata{}, func() {}, fmt.Errorf("invalid native environment id %q", id)
	}
	metadata, err := m.loadMetadata(id)
	if err != nil {
		return nil, NativeEnvironmentMetadata{}, func() {}, err
	}
	projectDir, err = resolveNativeProjectDir(m.workspace, projectDir)
	if err != nil {
		return nil, NativeEnvironmentMetadata{}, func() {}, err
	}
	if metadata.Scope == "project" && !sameNativeProject(metadata.ProjectIdentity, projectDir) {
		return nil, NativeEnvironmentMetadata{}, func() {}, fmt.Errorf(
			"native environment %q is project-scoped and cannot be attached to %s", id, projectDir)
	}
	releaseLease, err := m.acquireUseLease(id)
	if err != nil {
		return nil, NativeEnvironmentMetadata{}, func() {}, err
	}

	m.activeMu.Lock()
	m.active[id]++
	m.activeMu.Unlock()
	released := false
	release := func() {
		m.activeMu.Lock()
		if !released {
			released = true
			if m.active[id] <= 1 {
				delete(m.active, id)
			} else {
				m.active[id]--
			}
		}
		m.activeMu.Unlock()
		releaseLease()
	}

	payloadDir := filepath.Join(m.entryDir(id), "payload")
	overrides := make(map[string]string, len(metadata.Environment)+3)
	var pathEntries []string
	for _, entry := range metadata.PathEntries {
		expanded := expandNativeActivationValue(entry, payloadDir, projectDir, m.cacheDir)
		if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(payloadDir, filepath.FromSlash(expanded))
		}
		pathEntries = append(pathEntries, filepath.Clean(expanded))
	}
	if len(pathEntries) > 0 {
		pathEntries = append(pathEntries, os.Getenv("PATH"))
		overrides["PATH"] = strings.Join(pathEntries, string(os.PathListSeparator))
	}
	for key, value := range metadata.Environment {
		overrides[key] = expandNativeActivationValue(value, payloadDir, projectDir, m.cacheDir)
	}
	if metadata.Ecosystem == "python" {
		if !nativeEnvironmentHasKey(overrides, "PYTHONPYCACHEPREFIX") {
			overrides["PYTHONPYCACHEPREFIX"] = filepath.Join(payloadDir, ".pycache")
		}
	}
	overrides["OPENKITTY_NATIVE_ENV_ID"] = id
	metadata.LastUsedAt = m.now().UTC()
	_ = m.writeMetadata(m.entryDir(id), metadata)
	return overrides, metadata, release, nil
}

func (m *NativeEnvironmentManager) List() ([]NativeEnvironmentMetadata, error) {
	if m == nil {
		return nil, errors.New("native environment manager is unavailable")
	}
	entriesRoot := filepath.Join(m.cacheDir, "entries")
	entries, err := os.ReadDir(entriesRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []NativeEnvironmentMetadata{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list native environments: %w", err)
	}
	var result []NativeEnvironmentMetadata
	for _, entry := range entries {
		if !entry.IsDir() || !validNativeEnvironmentID(entry.Name()) {
			continue
		}
		metadata, loadErr := m.loadMetadata(entry.Name())
		if loadErr != nil {
			continue
		}
		if measured, sizeErr := directorySize(m.entryDir(entry.Name())); sizeErr == nil {
			metadata.SizeBytes = measured
		}
		result = append(result, metadata)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastUsedAt.After(result[j].LastUsedAt)
	})
	return result, nil
}

func (m *NativeEnvironmentManager) Prune(dryRun bool) (NativePruneReport, error) {
	return m.prune(dryRun, "")
}

func (m *NativeEnvironmentManager) prune(dryRun bool, preserveID string) (NativePruneReport, error) {
	m.pruneMu.Lock()
	defer m.pruneMu.Unlock()

	entries, err := m.List()
	if err != nil {
		return NativePruneReport{}, err
	}
	report := NativePruneReport{DryRun: dryRun}
	for _, entry := range entries {
		report.BeforeBytes += entry.SizeBytes
	}
	report.AfterBytes = report.BeforeBytes
	now := m.now().UTC()
	ttl := time.Duration(m.config.TTLHours) * time.Hour
	maxBytes := m.config.MaxCacheMB * 1024 * 1024
	selected := make(map[string]bool)

	mark := func(entry NativeEnvironmentMetadata, reason string) {
		busy := m.isBusy(entry.ID)
		if entry.ID == preserveID || selected[entry.ID] || busy {
			if busy && !nativeStringSliceContains(report.SkippedBusy, entry.ID) {
				report.SkippedBusy = append(report.SkippedBusy, entry.ID)
			}
			return
		}
		selected[entry.ID] = true
		report.Removed = append(report.Removed, NativePruneEntry{
			ID:        entry.ID,
			SizeBytes: entry.SizeBytes,
			Reason:    reason,
		})
		report.AfterBytes -= entry.SizeBytes
	}

	for _, entry := range entries {
		if ttl > 0 && !entry.LastUsedAt.IsZero() && now.Sub(entry.LastUsedAt) > ttl {
			mark(entry, "ttl")
		}
	}
	oldestFirst := append([]NativeEnvironmentMetadata(nil), entries...)
	sort.Slice(oldestFirst, func(i, j int) bool {
		return oldestFirst[i].LastUsedAt.Before(oldestFirst[j].LastUsedAt)
	})
	for _, entry := range oldestFirst {
		if maxBytes <= 0 || report.AfterBytes <= maxBytes {
			break
		}
		mark(entry, "capacity")
	}
	sort.Strings(report.SkippedBusy)

	if !dryRun {
		for _, entry := range report.Removed {
			if err := m.removeEntry(entry.ID); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func (m *NativeEnvironmentManager) resolveSpec(ctx context.Context, spec NativeEnvironmentSpec) (resolvedNativeEnvironmentSpec, error) {
	if m == nil {
		return resolvedNativeEnvironmentSpec{}, errors.New("native environment manager is unavailable")
	}
	if !m.config.Enabled {
		return resolvedNativeEnvironmentSpec{}, errors.New("native execution environments are disabled")
	}
	ecosystem := strings.ToLower(strings.TrimSpace(spec.Ecosystem))
	if !validNativeEcosystem(ecosystem) {
		return resolvedNativeEnvironmentSpec{}, fmt.Errorf(
			"ecosystem must be one of python, node, go, rust, jvm, ruby, dotnet, or other")
	}
	projectDir, err := resolveNativeProjectDir(m.workspace, spec.ProjectDir)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, err
	}
	runtimePath, runtimeVersion, err := resolveNativeRuntime(ctx, ecosystem, spec.Runtime)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, err
	}
	dependencies, err := resolveNativeDependencies(projectDir, ecosystem, spec.DependencyFiles)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, err
	}
	if len(dependencies) == 0 {
		return resolvedNativeEnvironmentSpec{}, fmt.Errorf(
			"no dependency manifest was found for ecosystem %q; pass dependency_files explicitly", ecosystem)
	}
	setupCommand := strings.TrimSpace(spec.SetupCommand)
	if setupCommand == "" {
		return resolvedNativeEnvironmentSpec{}, errors.New(
			"setup_command is required so the cache key describes how the environment is built")
	}
	scope := strings.ToLower(strings.TrimSpace(spec.Scope))
	if scope == "" {
		scope = "shared"
	}
	if scope != "shared" && scope != "project" {
		return resolvedNativeEnvironmentSpec{}, errors.New("scope must be shared or project")
	}
	pathEntries, err := normalizeNativePathEntries(spec.PathEntries)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, err
	}
	environment, err := normalizeNativeEnvironmentMap(spec.Environment)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, err
	}
	setupHash := sha256.Sum256([]byte(setupCommand))
	setupDigest := hex.EncodeToString(setupHash[:])
	projectIdentity := ""
	if scope == "project" {
		projectIdentity = nativeProjectIdentity(projectDir)
	}
	fingerprint := struct {
		SchemaVersion   int                      `json:"schema_version"`
		Ecosystem       string                   `json:"ecosystem"`
		Scope           string                   `json:"scope"`
		Platform        string                   `json:"platform"`
		Architecture    string                   `json:"architecture"`
		RuntimePath     string                   `json:"runtime_path"`
		RuntimeVersion  string                   `json:"runtime_version"`
		Dependencies    []NativeDependencyDigest `json:"dependencies"`
		SetupSHA256     string                   `json:"setup_sha256"`
		PathEntries     []string                 `json:"path_entries,omitempty"`
		Environment     map[string]string        `json:"environment,omitempty"`
		ProjectIdentity string                   `json:"project_identity,omitempty"`
	}{
		SchemaVersion:   nativeEnvironmentSchemaVersion,
		Ecosystem:       ecosystem,
		Scope:           scope,
		Platform:        runtime.GOOS,
		Architecture:    runtime.GOARCH,
		RuntimePath:     canonicalNativePath(runtimePath),
		RuntimeVersion:  runtimeVersion,
		Dependencies:    dependencies,
		SetupSHA256:     setupDigest,
		PathEntries:     pathEntries,
		Environment:     environment,
		ProjectIdentity: projectIdentity,
	}
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return resolvedNativeEnvironmentSpec{}, fmt.Errorf("encode native environment fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	id := ecosystem + "-" + hex.EncodeToString(digest[:10])
	return resolvedNativeEnvironmentSpec{
		NativeEnvironmentSpec: NativeEnvironmentSpec{
			Ecosystem:       ecosystem,
			ProjectDir:      projectDir,
			Runtime:         spec.Runtime,
			DependencyFiles: append([]string(nil), spec.DependencyFiles...),
			SetupCommand:    setupCommand,
			Scope:           scope,
			PathEntries:     pathEntries,
			Environment:     environment,
		},
		ID:              id,
		RuntimePath:     runtimePath,
		RuntimeVersion:  runtimeVersion,
		Dependencies:    dependencies,
		SetupSHA256:     setupDigest,
		ProjectIdentity: projectIdentity,
	}, nil
}

func (m *NativeEnvironmentManager) entryDir(id string) string {
	return filepath.Join(m.cacheDir, "entries", id)
}

func (m *NativeEnvironmentManager) metadataPath(id string) string {
	return filepath.Join(m.entryDir(id), "environment.json")
}

func (m *NativeEnvironmentManager) loadMetadata(id string) (NativeEnvironmentMetadata, error) {
	if !validNativeEnvironmentID(id) {
		return NativeEnvironmentMetadata{}, fmt.Errorf("invalid native environment id %q", id)
	}
	data, err := os.ReadFile(m.metadataPath(id))
	if err != nil {
		return NativeEnvironmentMetadata{}, err
	}
	var metadata NativeEnvironmentMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return NativeEnvironmentMetadata{}, fmt.Errorf("decode native environment %q metadata: %w", id, err)
	}
	if metadata.SchemaVersion != nativeEnvironmentSchemaVersion || metadata.ID != id {
		return NativeEnvironmentMetadata{}, fmt.Errorf("native environment %q metadata is incompatible", id)
	}
	payloadInfo, err := os.Stat(filepath.Join(m.entryDir(id), "payload"))
	if err != nil {
		return NativeEnvironmentMetadata{}, err
	}
	if !payloadInfo.IsDir() {
		return NativeEnvironmentMetadata{}, fmt.Errorf("native environment %q payload is not a directory", id)
	}
	return metadata, nil
}

func (m *NativeEnvironmentManager) writeMetadata(entryDir string, metadata NativeEnvironmentMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode native environment metadata: %w", err)
	}
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return fmt.Errorf("create native environment entry: %w", err)
	}
	if err := fsutil.WriteAtomic(filepath.Join(entryDir, "environment.json"), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write native environment metadata: %w", err)
	}
	return nil
}

func openKittyHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openkitty"
	}
	return filepath.Join(home, ".openkitty")
}

func (m *NativeEnvironmentManager) lockForKey(id string) *sync.Mutex {
	value, _ := m.keyLocks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (m *NativeEnvironmentManager) acquireProcessLock(ctx context.Context, id string) (func(), error) {
	lockRoot := filepath.Join(m.cacheDir, ".locks")
	if err := os.MkdirAll(lockRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create native environment lock root: %w", err)
	}
	lockDir := filepath.Join(lockRoot, id+".lock")
	staleAfter := 2 * time.Duration(m.config.PrepareTimeoutSeconds) * time.Second
	if staleAfter < 10*time.Minute {
		staleAfter = 10 * time.Minute
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquire native environment lock: %w", err)
		}
		if info, statErr := os.Stat(lockDir); statErr == nil && m.now().Sub(info.ModTime()) > staleAfter {
			_ = os.Remove(lockDir)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for native environment %q lock: %w", id, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *NativeEnvironmentManager) isActive(id string) bool {
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	return m.active[id] > 0
}

func (m *NativeEnvironmentManager) isBusy(id string) bool {
	if m.isActive(id) {
		return true
	}
	return m.hasLiveUseLease(id)
}

func (m *NativeEnvironmentManager) acquireUseLease(id string) (func(), error) {
	if !validNativeEnvironmentID(id) {
		return nil, fmt.Errorf("invalid native environment id %q", id)
	}
	leaseDir := filepath.Join(m.cacheDir, ".leases", id)
	if err := os.MkdirAll(leaseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create native environment lease directory: %w", err)
	}
	lease, err := os.CreateTemp(leaseDir, fmt.Sprintf("%d-", os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("create native environment use lease: %w", err)
	}
	_, writeErr := fmt.Fprintf(lease, "pid=%d\nstarted_at=%s\n", os.Getpid(), m.now().UTC().Format(time.RFC3339Nano))
	closeErr := lease.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(lease.Name())
		if writeErr != nil {
			return nil, fmt.Errorf("write native environment use lease: %w", writeErr)
		}
		return nil, fmt.Errorf("close native environment use lease: %w", closeErr)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = os.Remove(lease.Name())
			_ = os.Remove(leaseDir)
		})
	}, nil
}

func (m *NativeEnvironmentManager) hasLiveUseLease(id string) bool {
	if !validNativeEnvironmentID(id) {
		return false
	}
	leaseDir := filepath.Join(m.cacheDir, ".leases", id)
	entries, err := os.ReadDir(leaseDir)
	if err != nil {
		return false
	}
	staleAfter := 7 * 24 * time.Hour
	live := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(leaseDir, entry.Name())
		if m.now().Sub(info.ModTime()) > staleAfter {
			_ = os.Remove(path)
			continue
		}
		live = true
	}
	if !live {
		_ = os.Remove(leaseDir)
	}
	return live
}

func (m *NativeEnvironmentManager) removeEntry(id string) error {
	if !validNativeEnvironmentID(id) {
		return fmt.Errorf("refuse to remove invalid native environment id %q", id)
	}
	entriesRoot, err := filepath.Abs(filepath.Join(m.cacheDir, "entries"))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(m.entryDir(id))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(entriesRoot, target)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("refuse to remove native environment outside cache entries: %s", target)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove native environment %q: %w", id, err)
	}
	_ = os.Remove(filepath.Join(m.cacheDir, ".leases", id))
	return nil
}

var nativeEnvironmentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*-[0-9a-f]{20}$`)

func validNativeEnvironmentID(id string) bool {
	return nativeEnvironmentIDPattern.MatchString(strings.TrimSpace(id))
}

func validNativeEcosystem(ecosystem string) bool {
	switch ecosystem {
	case "python", "node", "go", "rust", "jvm", "ruby", "dotnet", "other":
		return true
	default:
		return false
	}
}

func resolveNativeProjectDir(workspace, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = workspace
	}
	if !filepath.IsAbs(configured) {
		configured = filepath.Join(workspace, configured)
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve native project directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect native project directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("native project directory is not a directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func resolveNativeRuntime(ctx context.Context, ecosystem, configured string) (string, string, error) {
	candidates := []string{strings.TrimSpace(configured)}
	if candidates[0] == "" {
		switch ecosystem {
		case "python":
			candidates = []string{"python", "python3", "py"}
		case "node":
			candidates = []string{"node"}
		case "go":
			candidates = []string{"go"}
		case "rust":
			candidates = []string{"rustc"}
		case "jvm":
			candidates = []string{"java"}
		case "ruby":
			candidates = []string{"ruby"}
		case "dotnet":
			candidates = []string{"dotnet"}
		default:
			return "", "", errors.New("runtime is required for ecosystem other")
		}
	}
	var runtimePath string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			runtimePath = path
			break
		}
	}
	if runtimePath == "" {
		return "", "", fmt.Errorf("could not find runtime executable; tried %s", strings.Join(candidates, ", "))
	}
	if absolute, err := filepath.Abs(runtimePath); err == nil {
		runtimePath = absolute
	}
	versionArgs := []string{"--version"}
	switch ecosystem {
	case "go":
		versionArgs = []string{"version"}
	case "jvm":
		versionArgs = []string{"-version"}
	}
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	versionCmd := exec.CommandContext(versionCtx, runtimePath, versionArgs...)
	output, err := versionCmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("query runtime version from %s: %w", runtimePath, err)
	}
	version := strings.Join(strings.Fields(strings.TrimSpace(string(output))), " ")
	if version == "" {
		return "", "", fmt.Errorf("runtime %s returned an empty version", runtimePath)
	}
	return filepath.Clean(runtimePath), version, nil
}

var nativeManifestPatterns = map[string][]string{
	"python": {"requirements*.txt", "requirements/*.txt", "pyproject.toml", "uv.lock", "poetry.lock", "Pipfile", "Pipfile.lock", "setup.cfg", "setup.py"},
	"node":   {"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"},
	"go":     {"go.mod", "go.sum", "go.work", "go.work.sum"},
	"rust":   {"Cargo.toml", "Cargo.lock"},
	"jvm":    {"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.lockfile"},
	"ruby":   {"Gemfile", "Gemfile.lock", "*.gemspec"},
	"dotnet": {"global.json", "Directory.Packages.props", "packages.lock.json", "*.sln", "*.csproj", "*.fsproj"},
}

func resolveNativeDependencies(projectDir, ecosystem string, configured []string) ([]NativeDependencyDigest, error) {
	paths := append([]string(nil), configured...)
	if len(paths) == 0 {
		patterns := nativeManifestPatterns[ecosystem]
		for _, pattern := range patterns {
			matches, err := filepath.Glob(filepath.Join(projectDir, filepath.FromSlash(pattern)))
			if err != nil {
				return nil, fmt.Errorf("expand dependency manifest pattern %q: %w", pattern, err)
			}
			paths = append(paths, matches...)
		}
	}
	seen := make(map[string]bool)
	var dependencies []NativeDependencyDigest
	for _, rawPath := range paths {
		rawPath = strings.TrimSpace(rawPath)
		if rawPath == "" {
			continue
		}
		path := rawPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, filepath.FromSlash(path))
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency file %q: %w", rawPath, err)
		}
		relative, err := filepath.Rel(projectDir, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("dependency file must stay inside project directory: %s", rawPath)
		}
		resolvedProject, projectResolveErr := filepath.EvalSymlinks(projectDir)
		resolvedFile, fileResolveErr := filepath.EvalSymlinks(absolute)
		if projectResolveErr != nil {
			return nil, fmt.Errorf("resolve project directory links: %w", projectResolveErr)
		}
		if fileResolveErr != nil {
			return nil, fmt.Errorf("resolve dependency file links for %q: %w", rawPath, fileResolveErr)
		}
		resolvedRelative, relErr := filepath.Rel(resolvedProject, resolvedFile)
		if relErr != nil || resolvedRelative == ".." ||
			strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRelative) {
			return nil, fmt.Errorf("dependency file resolves outside project directory: %s", rawPath)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("inspect dependency file %q: %w", rawPath, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("dependency path is not a regular file: %s", rawPath)
		}
		relative = filepath.ToSlash(filepath.Clean(relative))
		if seen[relative] {
			continue
		}
		seen[relative] = true
		digest, err := hashNativeDependencyFile(absolute, info.Size())
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, NativeDependencyDigest{
			Path: relative, SHA256: digest, Bytes: info.Size(),
		})
	}
	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i].Path < dependencies[j].Path
	})
	return dependencies, nil
}

func hashNativeDependencyFile(path string, size int64) (string, error) {
	if size > maxNativeDependencyFileBytes {
		return "", fmt.Errorf("dependency file exceeds %d bytes: %s", maxNativeDependencyFileBytes, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open dependency file %q: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxNativeDependencyFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("hash dependency file %q: %w", path, err)
	}
	if written > maxNativeDependencyFileBytes {
		return "", fmt.Errorf("dependency file exceeds %d bytes: %s", maxNativeDependencyFileBytes, path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeNativePathEntries(entries []string) ([]string, error) {
	seen := make(map[string]bool)
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cleaned := filepath.Clean(filepath.FromSlash(entry))
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path_entries must be relative to {env_dir}: %s", entry)
		}
		cleaned = filepath.ToSlash(cleaned)
		if !seen[cleaned] {
			seen[cleaned] = true
			result = append(result, cleaned)
		}
	}
	return result, nil
}

func normalizeNativeEnvironmentMap(values map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	seen := make(map[string]bool, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return nil, fmt.Errorf("invalid environment variable name %q", key)
		}
		canonical := strings.ToUpper(key)
		if canonical == "PATH" {
			return nil, errors.New("use path_entries instead of setting PATH in environment")
		}
		if seen[canonical] {
			return nil, fmt.Errorf("duplicate environment variable name %q", key)
		}
		seen[canonical] = true
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("environment variable %q contains a NUL byte", key)
		}
		result[key] = value
	}
	return result, nil
}

func nativeEnvironmentHasKey(values map[string]string, target string) bool {
	for key := range values {
		if strings.EqualFold(key, target) {
			return true
		}
	}
	return false
}

func expandNativeSetupCommand(command, runtimePath, projectDir, payloadDir, cacheDir string) string {
	envPython := filepath.Join(payloadDir, "bin", "python")
	if runtime.GOOS == "windows" {
		envPython = filepath.Join(payloadDir, "Scripts", "python.exe")
	}
	runtimeInvocation := quoteNativeShellValue(runtimePath)
	envPythonInvocation := quoteNativeShellValue(envPython)
	if runtime.GOOS == "windows" {
		runtimeInvocation = "& " + runtimeInvocation
		envPythonInvocation = "& " + envPythonInvocation
	}
	replacements := map[string]string{
		"{env_dir}":     quoteNativeShellValue(payloadDir),
		"{project_dir}": quoteNativeShellValue(projectDir),
		"{runtime}":     runtimeInvocation,
		"{env_python}":  envPythonInvocation,
		"{cache_dir}":   quoteNativeShellValue(cacheDir),
	}
	for placeholder, value := range replacements {
		command = strings.ReplaceAll(command, placeholder, value)
	}
	return command
}

func quoteNativeShellValue(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func expandNativeActivationValue(value, payloadDir, projectDir, cacheDir string) string {
	replacements := map[string]string{
		"{env_dir}":     payloadDir,
		"{project_dir}": projectDir,
		"{cache_dir}":   cacheDir,
	}
	for placeholder, replacement := range replacements {
		value = strings.ReplaceAll(value, placeholder, replacement)
	}
	return value
}

func mergeCommandEnvironment(base []string, overrides map[string]string) []string {
	caseInsensitive := runtime.GOOS == "windows"
	keyFor := func(key string) string {
		if caseInsensitive {
			return strings.ToUpper(key)
		}
		return key
	}
	merged := make(map[string]string, len(base)+len(overrides))
	names := make(map[string]string, len(base)+len(overrides))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		canonical := keyFor(key)
		names[canonical] = key
		merged[canonical] = value
	}
	for key, value := range overrides {
		canonical := keyFor(key)
		names[canonical] = key
		merged[canonical] = value
	}
	keys := make([]string, 0, len(merged))
	for canonical := range merged {
		keys = append(keys, canonical)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, canonical := range keys {
		result = append(result, names[canonical]+"="+merged[canonical])
	}
	return result
}

func directoryIsEmpty(path string) (bool, error) {
	directory, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer directory.Close()
	_, err = directory.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func compactNativeCommandOutput(output string, limit int) string {
	output = strings.TrimSpace(output)
	if len(output) <= limit {
		return output
	}
	return output[:limit] + fmt.Sprintf("\n... (%d bytes omitted)", len(output)-limit)
}

func canonicalNativePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return filepath.ToSlash(path)
}

func nativeProjectIdentity(projectDir string) string {
	digest := sha256.Sum256([]byte(canonicalNativePath(projectDir)))
	return hex.EncodeToString(digest[:])
}

func sameNativeProject(identity, projectDir string) bool {
	return identity != "" && identity == nativeProjectIdentity(projectDir)
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func nativeStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func SpecFromParams(workspace string, params map[string]interface{}) (NativeEnvironmentSpec, error) {
	environment := make(map[string]string)
	if raw, ok := params["environment"].(map[string]interface{}); ok {
		for key, value := range raw {
			text, ok := value.(string)
			if !ok {
				return NativeEnvironmentSpec{}, fmt.Errorf("environment value for %q must be a string", key)
			}
			environment[key] = text
		}
	} else if raw, ok := params["environment"].(map[string]string); ok {
		environment = cloneStringMap(raw)
	} else if params["environment"] != nil {
		return NativeEnvironmentSpec{}, errors.New("environment must be an object of string values")
	}
	return NativeEnvironmentSpec{
		Ecosystem:       configvalue.String(params, "ecosystem", ""),
		ProjectDir:      configvalue.String(params, "dir", workspace),
		Runtime:         configvalue.String(params, "runtime", ""),
		DependencyFiles: stringSliceParameter(params["dependency_files"]),
		SetupCommand:    configvalue.String(params, "setup_command", ""),
		Scope:           configvalue.String(params, "scope", "shared"),
		PathEntries:     stringSliceParameter(params["path_entries"]),
		Environment:     environment,
	}, nil
}

func (m *NativeEnvironmentManager) Workspace() string {
	if m == nil {
		return ""
	}
	return m.workspace
}

func (m *NativeEnvironmentManager) EntryDir(id string) string {
	if m == nil {
		return ""
	}
	return m.entryDir(id)
}

func stringSliceParameter(value interface{}) []string {
	var source []string
	switch values := value.(type) {
	case []string:
		source = values
	case []interface{}:
		for _, item := range values {
			if text, ok := item.(string); ok {
				source = append(source, text)
			}
		}
	}
	result := make([]string, 0, len(source))
	for _, item := range source {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
