package tenant

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mac/internal/security"
)

// ====================== Tenant Config ======================

type TenantConfig struct {
	ID    string `json:"id" yaml:"id"`
	Name  string `json:"name" yaml:"name"`
	Token string `json:"token" yaml:"token"`
}

// ====================== Tenant Manager ======================

type TenantManager struct {
	tenants   map[string]*TenantConfig // token → config
	rootToken string
	baseDir   string // tenants/
	enabled   bool
	logger    *log.Logger
	mu        sync.RWMutex
}

func NewTenantManager(baseDir string, logger *log.Logger) *TenantManager {
	os.MkdirAll(baseDir, 0755)
	return &TenantManager{
		tenants: make(map[string]*TenantConfig),
		baseDir: baseDir,
		logger:  logger,
	}
}

// Init loads tenants from config. If no tenants exist, migrates current config to tenants/default.
func (m *TenantManager) Init(configs []TenantConfig, rootToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(configs) == 0 {
		// Migrate: move current config to tenants/default/
		defaultDir := filepath.Join(m.baseDir, "default")
		os.MkdirAll(defaultDir, 0755)
		os.MkdirAll(filepath.Join(defaultDir, ".openkitty", "storage"), 0755)

		// Copy config if not already there
		defaultConfig := filepath.Join(defaultDir, "config.yaml")
		if _, err := os.Stat(defaultConfig); os.IsNotExist(err) {
			if data, err := os.ReadFile("config.yaml"); err == nil {
				os.WriteFile(defaultConfig, data, 0644)
				m.logger.Printf("Migrated config → %s", defaultConfig)
			}
		}

		// Copy existing db if any
		for _, dbFile := range []string{"data.db", "openkitty.db"} {
			if _, err := os.Stat(dbFile); err == nil {
				dst := filepath.Join(defaultDir, dbFile)
				if _, err := os.Stat(dst); os.IsNotExist(err) {
					data, _ := os.ReadFile(dbFile)
					os.WriteFile(dst, data, 0644)
				}
			}
		}

		defaultToken := security.GenerateToken(48)
		m.tenants[defaultToken] = &TenantConfig{
			ID:    "default",
			Name:  "默认租户",
			Token: defaultToken,
		}
		m.rootToken = rootToken
		m.enabled = true
		if m.logger != nil {
			m.logger.Printf("Created default tenant (token generated and redacted)")
		}
		return nil
	}

	m.rootToken = rootToken
	for _, cfg := range configs {
		if cfg.ID == "" {
			continue
		}
		if !ValidID(cfg.ID) {
			return fmt.Errorf("invalid tenant id %q", cfg.ID)
		}
		if cfg.Token == "" {
			cfg.Token = security.GenerateToken(48)
		}
		m.tenants[cfg.Token] = &TenantConfig{
			ID:    cfg.ID,
			Name:  cfg.Name,
			Token: cfg.Token,
		}

		// Ensure tenant directory exists
		tenantDir := m.TenantDir(cfg.ID)
		os.MkdirAll(tenantDir, 0755)
		os.MkdirAll(filepath.Join(tenantDir, ".openkitty", "storage"), 0755)

		// Copy default config if not exists
		confPath := filepath.Join(tenantDir, "config.yaml")
		if _, err := os.Stat(confPath); os.IsNotExist(err) {
			if data, err := os.ReadFile("config.yaml"); err == nil {
				os.WriteFile(confPath, data, 0644)
			}
		}

		if m.logger != nil {
			m.logger.Printf("Tenant: %s (%s)", cfg.Name, cfg.ID)
		}
	}

	m.enabled = true
	return nil
}

func ValidID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len(value) > 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func (m *TenantManager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Resolve returns the tenant for a request, or nil if root/disabled.
func (m *TenantManager) Resolve(r *http.Request) *TenantConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.enabled {
		return nil // no tenant routing
	}

	// Root bypass
	token := r.Header.Get("Authorization")
	token = strings.TrimPrefix(token, "Bearer ")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == m.rootToken && m.rootToken != "" {
		return nil // root can access everything
	}

	// Local bypass
	if security.IsLocalAddress(clientIP(r)) {
		return nil // local machine access
	}

	// Lookup tenant by token
	if tenant, ok := m.tenants[token]; ok {
		return tenant
	}
	return nil
}

// TenantDir returns the directory for a tenant.
func (m *TenantManager) TenantDir(tenantID string) string {
	return filepath.Join(m.baseDir, tenantID)
}

// GetByToken returns tenant config by token.
func (m *TenantManager) GetByToken(token string) *TenantConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tenants[token]
}

// ListTenants returns all registered tenants.
func (m *TenantManager) ListTenants() []*TenantConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*TenantConfig, 0, len(m.tenants))
	for _, t := range m.tenants {
		out = append(out, &TenantConfig{ID: t.ID, Name: t.Name, Token: t.Token})
	}
	return out
}

// SandboxPath ensures a file path is within the tenant's directory.
func (m *TenantManager) SandboxPath(tenantID, requestedPath string) (string, error) {
	if tenantID == "" {
		return requestedPath, nil // root, no sandbox
	}
	tenantDir := m.TenantDir(tenantID)

	// Resolve relative paths within tenant dir
	var resolved string
	if filepath.IsAbs(requestedPath) {
		// Absolute paths must be under tenant dir
		resolved = requestedPath
	} else {
		resolved = filepath.Join(tenantDir, requestedPath)
	}

	// Clean and verify
	resolved = filepath.Clean(resolved)
	if !strings.HasPrefix(resolved, filepath.Clean(tenantDir)+string(os.PathSeparator)) &&
		resolved != filepath.Clean(tenantDir) {
		return "", fmt.Errorf("access denied: path outside tenant directory")
	}

	// Ensure parent directory exists
	os.MkdirAll(filepath.Dir(resolved), 0755)
	return resolved, nil
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// ====================== Tenant CLI Sandbox ======================

// WrapCLI wraps a shell command for tenant sandboxing.
// Tenant commands are prefixed with cd <tenant_dir> && to confine execution.
func (m *TenantManager) WrapCLI(tenantID, cmd string) string {
	if tenantID == "" {
		return cmd // root, no sandbox
	}
	dir := m.TenantDir(tenantID)
	// Confine to tenant directory, block destructive system commands
	blocked := []string{"rm -rf /", "mkfs.", "dd if=", "> /dev/", "fdisk", "shutdown", "reboot", "chown", "chmod 777 /"}
	for _, b := range blocked {
		if strings.Contains(strings.ToLower(cmd), strings.ToLower(b)) {
			return fmt.Sprintf("echo 'BLOCKED: destructive command not allowed in tenant sandbox'")
		}
	}
	return fmt.Sprintf("cd %s && %s", dir, cmd)
}

// CanAccessConfig returns whether a tenant can modify a config path.
func (m *TenantManager) CanAccessConfig(tenantID, configPath string) bool {
	if tenantID == "" {
		return true // root
	}
	// Tenant can only access their own config
	expected := filepath.Join(m.TenantDir(tenantID), "config.yaml")
	return configPath == expected || strings.HasPrefix(configPath, m.TenantDir(tenantID))
}

// ====================== Tenant-Aware DB Path ======================

// TenantDB returns the database path for a tenant.
func (m *TenantManager) TenantDB(tenantID string) string {
	if tenantID == "" {
		return "data.db"
	}
	return filepath.Join(m.TenantDir(tenantID), "data.db")
}

// TenantStorage returns the storage path for a tenant.
func (m *TenantManager) TenantStorage(tenantID string) string {
	if tenantID == "" {
		return "storage"
	}
	return filepath.Join(m.TenantDir(tenantID), "storage")
}
