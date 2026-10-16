// terminal_manager.go - 终端管理器（跨平台通用部分）

package processutil

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ShellResourceOwner is host-owned metadata used to isolate terminal sessions.
type ShellResourceOwner struct {
	TenantID  string
	ServiceID string
	Protected bool
}

type ShellOwnerResolver func(context.Context) (ShellResourceOwner, error)
type ShellAccessChecker func(context.Context, ShellResourceOwner) bool

// TerminalManager 全局终端管理器（单例）
type TerminalManager struct {
	sessions     map[string]*PersistentShell // taskID -> Shell会话
	owners       map[string]ShellResourceOwner
	mu           sync.RWMutex
	gitBashPath  string // 缓存 Git Bash 路径
	resolveOwner ShellOwnerResolver
	canAccess    ShellAccessChecker
}

// NewTerminalManager creates a terminal session manager. Host-specific
// ownership is injected so this package never depends on transport identity.
func NewTerminalManager(resolveOwner ShellOwnerResolver, canAccess ShellAccessChecker) *TerminalManager {
	manager := &TerminalManager{
		sessions:     make(map[string]*PersistentShell),
		owners:       make(map[string]ShellResourceOwner),
		gitBashPath:  FindGitBash(),
		resolveOwner: resolveOwner,
		canAccess:    canAccess,
	}
	go manager.cleanupExpiredSessions()
	if manager.gitBashPath != "" {
		log.Printf("[TerminalManager] Git Bash 可用: %s", manager.gitBashPath)
	} else {
		log.Printf("[TerminalManager] Git Bash 不可用，将使用系统命令模式")
	}
	return manager
}

// GetGitBashPath 获取 Git Bash 路径
func (m *TerminalManager) GetGitBashPath() string {
	return m.gitBashPath
}

// IsGitBashAvailable 检查 Git Bash 是否可用
func (m *TerminalManager) IsGitBashAvailable() bool {
	return m.gitBashPath != ""
}

// GetOrCreateTerminal 获取或创建终端会话
func (m *TerminalManager) GetOrCreateTerminal(taskID, workDir string) (*PersistentShell, error) {
	return m.GetOrCreateTerminalForContext(context.Background(), taskID, workDir)
}

// GetOrCreateTerminalForContext binds tenant-scoped sessions to the sealed
// execution identity. An existing task ID owned by another tenant/service is
// indistinguishable from a missing session.
func (m *TerminalManager) GetOrCreateTerminalForContext(ctx context.Context, taskID, workDir string) (*PersistentShell, error) {
	log.Printf("begin GetOrCreateTerminal taskid[%s], workDir[%s]", taskID, workDir)
	newOwner := ShellResourceOwner{}
	var err error
	if m.resolveOwner != nil {
		newOwner, err = m.resolveOwner(ctx)
	}
	if err != nil {
		return nil, err
	}

	// 先尝试读取
	m.mu.RLock()
	shell, exists := m.sessions[taskID]
	existingOwner := m.owners[taskID]
	m.mu.RUnlock()

	if exists && !m.resourceAccessible(ctx, existingOwner) {
		return nil, fmt.Errorf("terminal session %q not found", taskID)
	}
	if exists && shell.IsHealthy() {
		log.Printf("GetOrCreateTerminal: 复用已有会话 taskid[%s], type[%s]", taskID, shell.GetType())
		return shell, nil
	}

	// 创建新会话
	m.mu.Lock()

	// 双重检查
	if shell, exists = m.sessions[taskID]; exists {
		existingOwner = m.owners[taskID]
		if !m.resourceAccessible(ctx, existingOwner) {
			m.mu.Unlock()
			return nil, fmt.Errorf("terminal session %q not found", taskID)
		}
		if shell.IsHealthy() {
			log.Printf("GetOrCreateTerminal: 双重检查复用会话 taskid[%s]", taskID)
			m.mu.Unlock()
			return shell, nil
		}
	}

	// 删除旧的无效会话
	var oldShell *PersistentShell
	if s, exists := m.sessions[taskID]; exists {
		log.Printf("GetOrCreateTerminal: 清理无效旧会话 taskid[%s]", taskID)
		oldShell = s
		delete(m.sessions, taskID)
		delete(m.owners, taskID)
	}
	m.mu.Unlock()
	if oldShell != nil {
		oldShell.Close()
	}

	// 创建新会话
	log.Printf("GetOrCreateTerminal: 开始创建新会话 taskid[%s], workDir[%s]", taskID, workDir)
	newShell, err := newPersistentShell(taskID, workDir, m.gitBashPath)
	if err != nil {
		log.Printf("GetOrCreateTerminal: 创建失败 taskid[%s], error[%v]", taskID, err)
		return nil, fmt.Errorf("创建持久Shell失败: %w", err)
	}

	log.Printf("GetOrCreateTerminal: 创建成功 taskid[%s], shellType[%s]", taskID, newShell.GetType())
	m.mu.Lock()
	if m.owners == nil {
		m.owners = make(map[string]ShellResourceOwner)
	}
	m.sessions[taskID] = newShell
	m.owners[taskID] = newOwner
	m.mu.Unlock()
	return newShell, nil
}

// GetShell 获取指定任务的 Shell 会话
func (m *TerminalManager) GetShell(taskID string) *PersistentShell {
	m.mu.RLock()
	defer m.mu.RUnlock()

	shell, exists := m.sessions[taskID]
	if exists && shell.IsHealthy() {
		return shell
	}
	return nil
}

// RemoveShell 移除并关闭 Shell 会话
func (m *TerminalManager) RemoveShell(taskID string) error {
	m.mu.Lock()
	if shell, exists := m.sessions[taskID]; exists {
		delete(m.sessions, taskID)
		delete(m.owners, taskID)
		m.mu.Unlock()
		return shell.Close()
	}
	m.mu.Unlock()
	return nil
}

// cleanupExpiredSessions 清理过期的会话
func (m *TerminalManager) cleanupExpiredSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		var expired []*PersistentShell
		m.mu.Lock()
		for taskID, shell := range m.sessions {
			if time.Since(shell.LastActive()) > 30*time.Minute {
				log.Printf("[cleanupExpiredSessions] 清理过期会话 taskid[%s], type[%s]", taskID, shell.GetType())
				expired = append(expired, shell)
				delete(m.sessions, taskID)
				delete(m.owners, taskID)
			}
		}
		m.mu.Unlock()
		for _, shell := range expired {
			shell.Close()
		}
	}
}

// CloseAll 关闭所有会话（用于程序退出时）
func (m *TerminalManager) CloseAll() {
	m.mu.Lock()
	shells := make([]*PersistentShell, 0, len(m.sessions))
	for taskID, shell := range m.sessions {
		log.Printf("[CloseAll] 关闭会话 taskid[%s]", taskID)
		shells = append(shells, shell)
		delete(m.sessions, taskID)
		delete(m.owners, taskID)
	}
	m.mu.Unlock()
	for _, shell := range shells {
		shell.Close()
	}
}

// GetSessionCount 获取当前会话数量
func (m *TerminalManager) GetSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

type TerminalSessionInfo struct {
	TaskID     string    `json:"task_id"`
	Type       ShellType `json:"type"`
	WorkDir    string    `json:"work_dir"`
	LastActive time.Time `json:"last_active"`
	Healthy    bool      `json:"healthy"`
}

func (m *TerminalManager) ListSessions() []TerminalSessionInfo {
	return m.ListSessionsForContext(context.Background())
}

func (m *TerminalManager) ListSessionsForContext(ctx context.Context) []TerminalSessionInfo {
	m.mu.RLock()
	snapshot := make(map[string]*PersistentShell, len(m.sessions))
	for taskID, shell := range m.sessions {
		if !m.resourceAccessible(ctx, m.owners[taskID]) {
			continue
		}
		snapshot[taskID] = shell
	}
	m.mu.RUnlock()

	infos := make([]TerminalSessionInfo, 0, len(snapshot))
	for taskID, shell := range snapshot {
		infos = append(infos, shell.SessionInfo(taskID))
	}
	return infos
}

func (m *TerminalManager) resourceAccessible(ctx context.Context, owner ShellResourceOwner) bool {
	if m.canAccess == nil {
		return !owner.Protected
	}
	return m.canAccess(ctx, owner)
}
