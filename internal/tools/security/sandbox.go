package security

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SandboxMode controls execution isolation level.
type SandboxMode string

const (
	SandboxNone     SandboxMode = "none"
	SandboxReadOnly SandboxMode = "readonly"
	SandboxStrict   SandboxMode = "strict"
)

// SandboxConfig defines sandbox restrictions.
type SandboxConfig struct {
	Mode           SandboxMode `json:"mode" yaml:"mode"`
	AllowedPaths   []string    `json:"allowed_paths" yaml:"allowed_paths"`
	DeniedPaths    []string    `json:"denied_paths" yaml:"denied_paths"`
	AllowedHosts   []string    `json:"allowed_hosts" yaml:"allowed_hosts"`
	DeniedHosts    []string    `json:"denied_hosts" yaml:"denied_hosts"`
	ShellPolicy    string      `json:"shell_policy,omitempty" yaml:"shell_policy,omitempty"`
	NetworkPolicy  string      `json:"network_policy,omitempty" yaml:"network_policy,omitempty"`
	SubAgentPolicy string      `json:"sub_agent_policy,omitempty" yaml:"sub_agent_policy,omitempty"`
	MaxFileSize    int64       `json:"max_file_size" yaml:"max_file_size"`
}

// DefaultSandboxConfig leaves local execution unrestricted. Hosts that need
// workspace or read-only isolation must opt in with an explicit sandbox mode.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Mode:         SandboxNone,
		AllowedPaths: []string{"."},
		DeniedPaths: []string{
			".git/config", ".env", ".env.local", ".env.production", ".env.development", ".env.test",
			"*.key", "*.pem", "*.p12", "*.pfx", "credentials.*", "*credentials*", "*secret*", "*token*",
			"chatroom_key.txt", "id_rsa", "id_ed25519",
		},
		DeniedHosts:    []string{"169.254.169.254", "metadata.google.internal"},
		ShellPolicy:    "allow",
		NetworkPolicy:  "allow",
		SubAgentPolicy: "allow",
		MaxFileSize:    100 * 1024 * 1024,
	}
}

type sandboxViolation struct {
	Time    time.Time
	Type    string
	Detail  string
	Blocked bool
}

// Sandbox wraps tool execution with filesystem and network restrictions.
type Sandbox struct {
	mu         sync.RWMutex
	root       string
	config     SandboxConfig
	violations []sandboxViolation
	blocked    int
}

func NewSandbox(config SandboxConfig) *Sandbox {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	return NewWorkspaceSandbox(root, config)
}

func NewWorkspaceSandbox(root string, config SandboxConfig) *Sandbox {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	config = normalizeSandboxConfig(config)
	return &Sandbox{root: filepath.Clean(root), config: config}
}

func normalizeSandboxConfig(config SandboxConfig) SandboxConfig {
	defaults := DefaultSandboxConfig()
	if config.Mode == "" {
		config.Mode = defaults.Mode
	}
	if len(config.AllowedPaths) == 0 {
		config.AllowedPaths = append([]string(nil), defaults.AllowedPaths...)
	}
	if config.DeniedPaths == nil {
		config.DeniedPaths = append([]string(nil), defaults.DeniedPaths...)
	}
	if config.DeniedHosts == nil {
		config.DeniedHosts = append([]string(nil), defaults.DeniedHosts...)
	}
	if strings.TrimSpace(config.ShellPolicy) == "" {
		config.ShellPolicy = defaults.ShellPolicy
	}
	if strings.TrimSpace(config.NetworkPolicy) == "" {
		config.NetworkPolicy = defaults.NetworkPolicy
	}
	if strings.TrimSpace(config.SubAgentPolicy) == "" {
		config.SubAgentPolicy = defaults.SubAgentPolicy
	}
	if config.MaxFileSize <= 0 {
		config.MaxFileSize = defaults.MaxFileSize
	}
	return config
}

func validateSandboxConfig(label string, config *SandboxConfig) error {
	if config == nil {
		return nil
	}
	switch config.Mode {
	case "", SandboxNone, SandboxReadOnly, SandboxStrict:
	default:
		return fmt.Errorf("%s.mode %q is invalid: use none, readonly, or strict", label, config.Mode)
	}
	for field, value := range map[string]string{
		"shell_policy": config.ShellPolicy, "network_policy": config.NetworkPolicy,
		"sub_agent_policy": config.SubAgentPolicy,
	} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && value != "allow" && value != "deny" {
			return fmt.Errorf("%s.%s %q is invalid: use allow or deny", label, field, value)
		}
	}
	if config.MaxFileSize < 0 {
		return fmt.Errorf("%s.max_file_size cannot be negative", label)
	}
	for _, entry := range append(append([]string(nil), config.AllowedPaths...), config.DeniedPaths...) {
		if strings.TrimSpace(entry) == "" {
			return fmt.Errorf("%s contains an empty path rule", label)
		}
	}
	for _, entry := range append(append([]string(nil), config.AllowedHosts...), config.DeniedHosts...) {
		if strings.TrimSpace(entry) == "" {
			return fmt.Errorf("%s contains an empty host rule", label)
		}
	}
	return nil
}

func mergeSandboxConfig(base SandboxConfig, override *SandboxConfig) SandboxConfig {
	base = normalizeSandboxConfig(base)
	if override == nil {
		return base
	}
	merged := base
	if override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.AllowedPaths != nil {
		merged.AllowedPaths = append([]string(nil), override.AllowedPaths...)
	}
	if override.DeniedPaths != nil {
		merged.DeniedPaths = append([]string(nil), override.DeniedPaths...)
	}
	if override.AllowedHosts != nil {
		merged.AllowedHosts = append([]string(nil), override.AllowedHosts...)
	}
	if override.DeniedHosts != nil {
		merged.DeniedHosts = append([]string(nil), override.DeniedHosts...)
	}
	if strings.TrimSpace(override.ShellPolicy) != "" {
		merged.ShellPolicy = override.ShellPolicy
	}
	if strings.TrimSpace(override.NetworkPolicy) != "" {
		merged.NetworkPolicy = override.NetworkPolicy
	}
	if strings.TrimSpace(override.SubAgentPolicy) != "" {
		merged.SubAgentPolicy = override.SubAgentPolicy
	}
	if override.MaxFileSize > 0 {
		merged.MaxFileSize = override.MaxFileSize
	}
	return normalizeSandboxConfig(merged)
}

func (s *Sandbox) ConfigSnapshot() SandboxConfig {
	if s == nil {
		return DefaultSandboxConfig()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	config := s.config
	config.AllowedPaths = append([]string(nil), config.AllowedPaths...)
	config.DeniedPaths = append([]string(nil), config.DeniedPaths...)
	config.AllowedHosts = append([]string(nil), config.AllowedHosts...)
	config.DeniedHosts = append([]string(nil), config.DeniedHosts...)
	return config
}

func (s *Sandbox) Root() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.root
}

// ValidatePath checks if a file path is allowed under sandbox rules.
func (s *Sandbox) ValidatePath(requestedPath string) (bool, string) {
	if s == nil {
		return false, "sandbox is unavailable"
	}
	s.mu.RLock()
	cfg := s.config
	root := s.root
	s.mu.RUnlock()

	if cfg.Mode == SandboxNone {
		return true, ""
	}
	candidate, err := resolveSandboxPath(root, requestedPath)
	if err != nil {
		s.addViolation("path", requestedPath, true)
		return false, err.Error()
	}
	relative, relErr := filepath.Rel(root, candidate)
	if relErr != nil {
		relative = candidate
	}
	relativeSlash := filepath.ToSlash(filepath.Clean(relative))
	for _, denied := range cfg.DeniedPaths {
		if sandboxPathPatternMatch(denied, relativeSlash) {
			s.addViolation("path", requestedPath, true)
			return false, "path denied: " + denied
		}
	}

	if len(cfg.AllowedPaths) > 0 {
		for _, allowed := range cfg.AllowedPaths {
			allowedRoot, err := resolveSandboxPath(root, allowed)
			if err == nil && pathWithinRoot(allowedRoot, candidate) {
				return true, ""
			}
		}
		s.addViolation("path", requestedPath, true)
		return false, "outside allowed paths"
	}
	if pathWithinRoot(root, candidate) {
		return true, ""
	}
	s.addViolation("path", requestedPath, true)
	return false, "outside workspace"
}

// ValidateNetwork checks if a host is allowed.
func (s *Sandbox) ValidateNetwork(rawHost string) (bool, string) {
	if s == nil {
		return false, "sandbox is unavailable"
	}
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if cfg.Mode == SandboxNone {
		return true, ""
	}
	host := normalizeSandboxHost(rawHost)
	if host == "" {
		s.addViolation("network", rawHost, true)
		return false, "invalid host"
	}

	for _, denied := range cfg.DeniedHosts {
		if sandboxHostMatch(host, denied) {
			s.addViolation("network", host, true)
			return false, "host denied"
		}
	}

	if len(cfg.AllowedHosts) > 0 {
		for _, allowed := range cfg.AllowedHosts {
			if sandboxHostMatch(host, allowed) {
				return true, ""
			}
		}
		s.addViolation("network", host, true)
		return false, "host not in allow list"
	}
	if isPrivateSandboxHost(host) {
		s.addViolation("network", host, true)
		return false, "private, loopback, or link-local host denied"
	}
	return true, ""
}

func resolveSandboxPath(root, requestedPath string) (string, error) {
	requestedPath = strings.TrimSpace(strings.Trim(requestedPath, `"'`))
	if requestedPath == "" {
		return "", fmt.Errorf("empty path")
	}
	candidate := requestedPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	absolute = filepath.Clean(absolute)

	// Resolve symlinks for the longest existing ancestor so a write to a new
	// file cannot escape through a symlinked parent directory.
	existing := absolute
	var suffix []string
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	if resolved, evalErr := filepath.EvalSymlinks(existing); evalErr == nil {
		absolute = resolved
		for _, part := range suffix {
			absolute = filepath.Join(absolute, part)
		}
	}
	return filepath.Clean(absolute), nil
}

func pathWithinRoot(root, candidate string) bool {
	rootAbs, rootErr := filepath.Abs(root)
	candidateAbs, candidateErr := filepath.Abs(candidate)
	if rootErr != nil || candidateErr != nil {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(candidateAbs))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func sandboxPathPatternMatch(patternValue, relativePath string) bool {
	patternValue = strings.ToLower(strings.TrimSpace(filepath.ToSlash(patternValue)))
	relativePath = strings.ToLower(strings.TrimPrefix(filepath.ToSlash(relativePath), "./"))
	if patternValue == "" {
		return false
	}
	if !strings.Contains(patternValue, "/") {
		for _, segment := range strings.Split(relativePath, "/") {
			if matched, _ := path.Match(patternValue, segment); matched {
				return true
			}
		}
		return false
	}
	if matched, _ := path.Match(patternValue, relativePath); matched {
		return true
	}
	return strings.HasSuffix(relativePath, "/"+strings.TrimPrefix(patternValue, "/"))
}

func normalizeSandboxHost(rawHost string) string {
	rawHost = strings.TrimSpace(rawHost)
	if parsed, err := url.Parse(rawHost); err == nil && parsed.Hostname() != "" {
		rawHost = parsed.Hostname()
	}
	if host, _, err := net.SplitHostPort(rawHost); err == nil {
		rawHost = host
	}
	return strings.TrimSuffix(strings.ToLower(strings.Trim(rawHost, "[] \t\r\n.")), ".")
}

func sandboxHostMatch(host, patternValue string) bool {
	host = normalizeSandboxHost(host)
	patternValue = normalizeSandboxHost(patternValue)
	patternValue = strings.TrimPrefix(patternValue, "*.")
	if host == "" || patternValue == "" {
		return false
	}
	return host == patternValue || strings.HasSuffix(host, "."+patternValue)
}

func isPrivateSandboxHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func (s *Sandbox) addViolation(vType, detail string, blocked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violations = append(s.violations, sandboxViolation{Time: time.Now(), Type: vType, Detail: detail, Blocked: blocked})
	if blocked {
		s.blocked++
	}
	if len(s.violations) > 200 {
		s.violations = s.violations[len(s.violations)-200:]
	}
}

// AddViolation records a host policy decision without exposing the sandbox's
// mutable internal slices to the root middleware layer.
func (s *Sandbox) AddViolation(vType, detail string, blocked bool) {
	s.addViolation(vType, detail, blocked)
}

// SetMode safely updates the sandbox mode.
func (s *Sandbox) SetMode(mode SandboxMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Mode = mode
}

// AddAllowedPath safely adds a path to the allow list.
func (s *Sandbox) AddAllowedPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AllowedPaths = append(s.config.AllowedPaths, path)
}

// AddAllowedHost safely adds a host to the allow list.
func (s *Sandbox) AddAllowedHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AllowedHosts = append(s.config.AllowedHosts, host)
}

// AddDeniedPath safely adds a path to the deny list.
func (s *Sandbox) AddDeniedPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.DeniedPaths = append(s.config.DeniedPaths, path)
}

// AddDeniedHost safely adds a host to the deny list.
func (s *Sandbox) AddDeniedHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.DeniedHosts = append(s.config.DeniedHosts, host)
}

// GetViolations safely returns recent violations.
func (s *Sandbox) GetViolations(limit int) []sandboxViolation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.violations) {
		limit = len(s.violations)
	}
	r := make([]sandboxViolation, limit)
	copy(r, s.violations[len(s.violations)-limit:])
	return r
}

// SandboxTool provides LLM access to sandbox configuration.
type SandboxTool struct {
	sandbox *Sandbox
}

func NewSandboxTool(s *Sandbox) *SandboxTool { return &SandboxTool{sandbox: s} }

func (t *SandboxTool) Name() string { return "sandbox" }

func (t *SandboxTool) Description() string {
	return "Inspect the host-owned execution sandbox and recent violations. Sandbox mutation actions are control-plane only."
}

func (t *SandboxTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":       map[string]interface{}{"type": "string", "enum": []string{"status", "mode", "allow", "deny", "violations"}},
			"sandbox_mode": map[string]interface{}{"type": "string", "enum": []string{"none", "readonly", "strict"}},
			"target_type":  map[string]interface{}{"type": "string", "enum": []string{"path", "host"}},
			"target":       map[string]interface{}{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *SandboxTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := getString(params, "action", "status")

	switch action {
	case "status":
		s := t.sandbox
		s.mu.RLock()
		defer s.mu.RUnlock()
		return fmt.Sprintf("## Sandbox\n\n**Mode:** %s | **Blocked:** %d\n\n**Allowed paths:** %v\n**Denied:** %v\n**Allowed hosts:** %v\n**Max file:** %d MB",
			s.config.Mode, s.blocked, s.config.AllowedPaths, s.config.DeniedPaths, s.config.AllowedHosts, s.config.MaxFileSize/(1024*1024)), nil

	case "mode":
		mode := getString(params, "sandbox_mode", "")
		switch mode {
		case "none":
			t.sandbox.SetMode(SandboxNone)
			return "🔓 Sandbox disabled.", nil
		case "readonly":
			t.sandbox.SetMode(SandboxReadOnly)
			return "🔒 Read-only mode.", nil
		case "strict":
			t.sandbox.SetMode(SandboxStrict)
			return "🔒 Strict mode.", nil
		}
		return "Error: use none/readonly/strict", nil

	case "allow":
		target := getString(params, "target", "")
		if target == "" {
			return "Error: 'target' required.", nil
		}
		if getString(params, "target_type", "") == "host" {
			t.sandbox.AddAllowedHost(target)
		} else {
			t.sandbox.AddAllowedPath(target)
		}
		return fmt.Sprintf("✅ Allowed: %s", target), nil

	case "deny":
		target := getString(params, "target", "")
		if target == "" {
			return "Error: 'target' required.", nil
		}
		if getString(params, "target_type", "") == "host" {
			t.sandbox.AddDeniedHost(target)
		} else {
			t.sandbox.AddDeniedPath(target)
		}
		return fmt.Sprintf("🚫 Denied: %s", target), nil

	case "violations":
		violations := t.sandbox.GetViolations(20)
		if len(violations) == 0 {
			return "✅ No violations.", nil
		}
		var sb strings.Builder
		sb.WriteString("## Violations\n\n")
		for _, v := range violations {
			icon := "🚫"
			if !v.Blocked {
				icon = "⚠️"
			}
			sb.WriteString(fmt.Sprintf("%s [%s] %s — %s\n", icon, v.Time.Format("15:04"), v.Type, truncate(v.Detail, 80)))
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// Exported boundary helpers retain the root package's configuration and path
// validation seams while the implementation lives in the security tool set.
func ValidateSandboxConfig(label string, config *SandboxConfig) error {
	return validateSandboxConfig(label, config)
}

func MergeSandboxConfig(base SandboxConfig, override *SandboxConfig) SandboxConfig {
	return mergeSandboxConfig(base, override)
}

func PathWithinRoot(root, candidate string) bool {
	return pathWithinRoot(root, candidate)
}

func getString(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func truncate(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	return value[:maximum] + "..."
}
