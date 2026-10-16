package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"mac/internal/projectctx"
	"mac/internal/routing"
	"mac/internal/security"
	"mac/internal/skillpath"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
	skilltools "mac/internal/tools/skills"

	"gopkg.in/yaml.v3"
)

type SkillMetadata struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Version     string                 `json:"version"`
	Author      string                 `json:"author"`
	Type        SkillType              `json:"type"`       // python, shell, http, builtin
	Entrypoint  string                 `json:"entrypoint"` // 入口函数/文件
	Requires    map[string]interface{} `json:"requires"`   // 依赖
	Always      bool                   `json:"always"`     // 是否总是加载
	Aliases     []string               `json:"aliases" yaml:"aliases"`
	Tags        []string               `json:"tags"`
	ReadWhen    []string               `json:"read_when" yaml:"read_when"`
	Parameters  []SkillParameter       `json:"parameters"` // 参数定义
}

type SkillParameter struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"` // string, int, bool, array
	Description string      `json:"description"`
	Required    bool        `json:"required"`
	Default     interface{} `json:"default,omitempty"`
}

type Skill struct {
	Metadata        SkillMetadata
	Interface       skillpath.SkillInterfaceMetadata
	Content         string
	Path            string
	Source          string
	SourceDir       string
	RuntimeDir      string
	Embedded        bool
	RequiresRuntime bool
	Raw             string
	Code            string // 提取的代码部分
}

type InvokedSkillInfo struct {
	Name      string
	Path      string
	Content   string
	InvokedAt time.Time
}

const legacyInvokedSkillScope = "legacy"

var skillContentDetector = security.NewPromptInjectionDetector()

func invokedSkillScopeFromContext(ctx context.Context) string {
	taskID := strings.TrimSpace(taskIDFromContext(ctx))
	if taskID == "" {
		return legacyInvokedSkillScope
	}
	return "task:" + taskID
}

func taskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	taskID, _ := ctx.Value("task_id").(string)
	return taskID
}

func openKittyHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openkitty"
	}
	return filepath.Join(home, ".openkitty")
}

type SkillsLoader struct {
	workspace         string
	agentName         string
	builtinSkills     string
	skills            map[string]*Skill
	alwaysSkills      []string
	executors         map[SkillType]SkillExecutor
	skillIndex        *SkillIndex
	builtinDocs       map[string]string
	builtinIndex      []byte
	builtinFS         fs.FS
	builtinRoot       string
	userSkillDirs     []string
	runtimeDigests    map[string]string
	indexPrompt       string
	summaryCache      string
	invokedSkills     map[string]map[string]InvokedSkillInfo
	llmCaller         SkillLLMCaller
	skillCache        *SkillSearchCache
	executionOverride func(context.Context, string) (*Skill, string, bool)
	runtimeScope      func(context.Context) (workspaceScoped bool, workspace string)
	mu                sync.RWMutex
}

type LoaderOption func(*SkillsLoader)

func WithExecutionOverride(resolver func(context.Context, string) (*Skill, string, bool)) LoaderOption {
	return func(loader *SkillsLoader) { loader.executionOverride = resolver }
}

func WithRuntimeScope(resolver func(context.Context) (bool, string)) LoaderOption {
	return func(loader *SkillsLoader) { loader.runtimeScope = resolver }
}

// SkillSnapshot is an immutable view of a loaded skill for status and API
// surfaces. Keeping callers away from the loader's internal pointers prevents
// concurrent reloads from racing with capability discovery.
type SkillSnapshot struct {
	Name            string
	Category        string
	Aliases         []string
	Description     string
	Interface       skillpath.SkillInterfaceMetadata
	Version         string
	Author          string
	Type            SkillType
	Parameters      []SkillParameter
	Source          string
	Path            string
	SourceDir       string
	Raw             string
	RequiresRuntime bool
}

// SkillPromptMatch is the minimal immutable view needed for automatic prompt
// routing. Keeping it small avoids copying full Skill metadata on every turn.
type SkillPromptMatch struct {
	Name        string
	Description string
}

func skillSnapshotFromSkill(name string, skill *Skill) SkillSnapshot {
	if skill == nil {
		return SkillSnapshot{}
	}
	return SkillSnapshot{
		Name:            name,
		Aliases:         append([]string(nil), skill.Metadata.Aliases...),
		Description:     skill.Metadata.Description,
		Interface:       skill.Interface,
		Version:         skill.Metadata.Version,
		Author:          skill.Metadata.Author,
		Type:            NormalizeSkillType(string(skill.Metadata.Type)),
		Parameters:      append([]SkillParameter(nil), skill.Metadata.Parameters...),
		Source:          skill.Source,
		Path:            skill.Path,
		SourceDir:       skill.SourceDir,
		Raw:             skill.Raw,
		RequiresRuntime: skill.RequiresRuntime,
	}
}

func (l *SkillsLoader) SnapshotSkills() []SkillSnapshot {
	if l == nil {
		return []SkillSnapshot{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	snapshots := make([]SkillSnapshot, 0, len(l.skills))
	indexed := make(map[string][]string)
	if l.skillIndex != nil {
		for category, entry := range l.skillIndex.Categories {
			for _, name := range entry.Skills {
				indexed[name] = append(indexed[name], category)
			}
		}
	}
	for name, skill := range l.skills {
		if skill == nil {
			continue
		}
		snapshot := skillSnapshotFromSkill(name, skill)
		builtinDir := ""
		if skill.Source == "builtin" {
			builtinDir = skill.SourceDir
		}
		snapshot.Category = SkillCatalogCategory(skill.Metadata, skill.Interface.DisplayName, indexed[name], builtinDir)
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name < snapshots[j].Name
	})
	return snapshots
}

func (l *SkillsLoader) SkillCount() int {
	if l == nil {
		return 0
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.skills)
}

func NewSkillsLoader(workspace, agentName, builtinSkills string, options ...LoaderOption) *SkillsLoader {

	loader := &SkillsLoader{
		workspace:      workspace,
		agentName:      agentName,
		builtinSkills:  builtinSkills,
		skills:         make(map[string]*Skill),
		alwaysSkills:   make([]string, 0),
		executors:      make(map[SkillType]SkillExecutor),
		builtinDocs:    make(map[string]string),
		runtimeDigests: make(map[string]string),
		invokedSkills:  make(map[string]map[string]InvokedSkillInfo),
	}
	for _, option := range options {
		if option != nil {
			option(loader)
		}
	}

	// 注册默认执行器
	loader.RegisterExecutor(SkillTypePython, &PythonSkillExecutor{})
	loader.RegisterExecutor(SkillTypeNode, &NodeSkillExecutor{})
	loader.RegisterExecutor(SkillTypeJS, &NodeSkillExecutor{})
	loader.RegisterExecutor(SkillTypeShell, &ShellSkillExecutor{})
	loader.RegisterExecutor(SkillTypeHTTP, &HTTPSkillExecutor{})
	loader.RegisterExecutor(SkillTypeBuiltin, &BuiltinSkillExecutor{runtimeScope: loader.runtimeScope})

	return loader
}

func (l *SkillsLoader) RegisterExecutor(skillType SkillType, executor SkillExecutor) {
	l.executors[skillType] = executor
}

func (l *SkillsLoader) RegisterSkill(name string, skill *Skill) {
	if l == nil || skill == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cacheSkillLocked(name, skill)
}

func isExecutableSkillType(t SkillType) bool {
	switch t {
	case SkillTypePython, SkillTypeNode, SkillTypeJS, SkillTypeShell:
		return true
	default:
		return false
	}
}

func isEntrypointPath(entry string) bool {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if entry == "" {
		return false
	}
	if strings.Contains(entry, "/") || strings.Contains(entry, "\\") {
		return true
	}
	for _, ext := range []string{".py", ".js", ".mjs", ".cjs", ".sh", ".bash", ".ps1", ".bat", ".cmd"} {
		if strings.HasSuffix(entry, ext) {
			return true
		}
	}
	return false
}

func (l *SkillsLoader) invalidatePromptCachesLocked() {
	l.indexPrompt = ""
	l.summaryCache = ""
}

func (l *SkillsLoader) shouldCacheBuiltin(source string) bool {
	return source == "builtin"
}

func (l *SkillsLoader) normalizeSkillPath(source, name, skillFile string) string {
	if l.shouldCacheBuiltin(source) {
		return fmt.Sprintf("builtin://%s", name)
	}
	return skillFile
}

func (l *SkillsLoader) cacheBuiltinDocLocked(name string, raw string) {
	l.builtinDocs[name] = raw
	l.invalidatePromptCachesLocked()
}

func (l *SkillsLoader) cacheSkillLocked(name string, skill *Skill) {
	l.skills[name] = skill
	l.invalidatePromptCachesLocked()
}

func (l *SkillsLoader) cacheBuiltinIndexLocked(data []byte) {
	l.builtinIndex = append([]byte(nil), data...)
	l.indexPrompt = ""
}

func (l *SkillsLoader) ExecuteSkill(ctx context.Context, skillName string, params map[string]interface{}) (string, error) {
	var skill *Skill
	var resolvedName string
	var ok bool
	if l.executionOverride != nil {
		skill, resolvedName, ok = l.executionOverride(ctx, skillName)
	}
	if !ok {
		skill, resolvedName, ok = l.ResolveSkill(skillName)
	}
	if !ok {
		return "", fmt.Errorf("技能 %s 不存在", skillName)
	}

	executionSkill := skill
	if skill.RequiresRuntime {
		runtimeDir, err := l.ensureSkillRuntime(ctx, skill)
		if err != nil {
			return "", err
		}
		// RuntimeDir depends on the authenticated execution workspace. Use a
		// per-call copy so concurrent invocations cannot observe another
		// tenant's runtime directory through the shared Skill pointer.
		skillCopy := *skill
		skillCopy.RuntimeDir = runtimeDir
		executionSkill = &skillCopy
	}

	executor, ok := l.executors[executionSkill.Metadata.Type]
	if !ok {
		return "", fmt.Errorf("不支持的技能类型: %s", executionSkill.Metadata.Type)
	}
	log.Printf("执行技能 %s, 参数: %v", resolvedName, params)
	// 验证参数
	for _, param := range executionSkill.Metadata.Parameters {
		if param.Required {
			if _, ok := params[param.Name]; !ok {
				return "", fmt.Errorf("缺少必需参数: %s", param.Name)
			}
		}
	}

	result, err := executor.Execute(ctx, executionSkill, params)
	if !strings.EqualFold(strings.TrimSpace(executionSkill.Source), "builtin") && strings.TrimSpace(executionSkill.SourceDir) != "" {
		metricsPath := filepath.Join(filepath.Dir(executionSkill.SourceDir), ".skill_metrics.json")
		skilltools.RecordSkillUse(metricsPath, resolvedName, err == nil)
	}

	// 记录结果
	if err != nil {
		log.Printf("技能 %s 执行失败: %v", resolvedName, err)
	} else {
		log.Printf("技能 %s 执行成功, 结果长度: %d", resolvedName, len(result))
	}

	return result, err
}

func (l *SkillsLoader) ResolveSkill(name string) (*Skill, string, bool) {
	name = strings.TrimSpace(name)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.findSkillLocked(name)
}

// SkillExecutionPolicy exposes only the execution boundary needed by the
// generic use_skill tool, without leaking Skill internals into the tool layer.
func (l *SkillsLoader) SkillExecutionPolicy(name string) (bool, toolcontract.SandboxCapabilities, bool) {
	skill, _, ok := l.ResolveSkill(strings.TrimSpace(name))
	if !ok || skill == nil {
		return false, toolcontract.SandboxCapabilities{}, false
	}
	skillType := NormalizeSkillType(string(skill.Metadata.Type))
	readOnly := !skill.RequiresRuntime && skillType == SkillTypeBuiltin
	switch skillType {
	case SkillTypeHTTP:
		return readOnly, toolcontract.SandboxCapabilities{Network: true}, true
	case SkillTypePython, SkillTypeNode, SkillTypeJS, SkillTypeShell:
		return readOnly, toolcontract.SandboxCapabilities{Process: true}, true
	default:
		return readOnly, toolcontract.SandboxCapabilities{}, true
	}
}

func (l *SkillsLoader) findSkillLocked(name string) (*Skill, string, bool) {
	name = strings.TrimSpace(name)
	if skill, ok := l.skills[name]; ok {
		return skill, name, true
	}
	nameLower := strings.ToLower(name)
	for key, skill := range l.skills {
		if strings.EqualFold(skill.Metadata.Name, name) {
			return skill, key, true
		}
		for _, alias := range skill.Metadata.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == nameLower {
				return skill, key, true
			}
		}
	}
	return nil, name, false
}

type PreparedSkillReload struct {
	loader       *SkillsLoader
	skills       map[string]*Skill
	userDirs     []string
	builtinCount int
	userCount    int
}

type SkillReloadResult struct {
	BuiltinCount int      `json:"builtin_count"`
	UserCount    int      `json:"user_count"`
	Directories  []string `json:"directories"`
}

func (l *SkillsLoader) rememberUserSkillDir(skillsDir, source string) {
	if l == nil || strings.EqualFold(strings.TrimSpace(source), "builtin") || strings.TrimSpace(skillsDir) == "" {
		return
	}
	root := filepath.Clean(skillsDir)
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		root = filepath.Dir(root)
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(absolute)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, existing := range l.userSkillDirs {
		if strings.EqualFold(filepath.Clean(existing), root) {
			return
		}
	}
	l.userSkillDirs = append(l.userSkillDirs, root)
}

func (l *SkillsLoader) UserSkillDirs() []string {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.userSkillDirs...)
}

func (l *SkillsLoader) Workspace() string {
	if l == nil {
		return ""
	}
	return l.workspace
}

func (l *SkillsLoader) BuiltinFS() fs.FS {
	if l == nil {
		return nil
	}
	return l.builtinFS
}

func NormalizeReloadDirs(dirs []string, fallback string) []string {
	if len(dirs) == 0 && strings.TrimSpace(fallback) != "" {
		dirs = []string{fallback}
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if absolute, err := filepath.Abs(clean); err == nil {
			clean = filepath.Clean(absolute)
		}
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, clean)
	}
	return out
}

func cloneSkillForReload(skill *Skill) *Skill {
	if skill == nil {
		return nil
	}
	clone := *skill
	clone.Metadata.Aliases = append([]string(nil), skill.Metadata.Aliases...)
	clone.Metadata.Tags = append([]string(nil), skill.Metadata.Tags...)
	clone.Metadata.ReadWhen = append([]string(nil), skill.Metadata.ReadWhen...)
	clone.Metadata.Parameters = append([]SkillParameter(nil), skill.Metadata.Parameters...)
	if skill.Metadata.Requires != nil {
		clone.Metadata.Requires = make(map[string]interface{}, len(skill.Metadata.Requires))
		for key, value := range skill.Metadata.Requires {
			clone.Metadata.Requires[key] = value
		}
	}
	return &clone
}

func (l *SkillsLoader) PrepareUserSkillReload(dirs []string) (*PreparedSkillReload, error) {
	if l == nil {
		return nil, fmt.Errorf("skills loader is nil")
	}
	if len(dirs) == 0 {
		dirs = l.UserSkillDirs()
	}
	dirs = NormalizeReloadDirs(dirs, filepath.Join(l.workspace, "skills"))
	staged := make(map[string]*Skill)
	l.mu.RLock()
	for name, skill := range l.skills {
		if skill != nil && strings.EqualFold(skill.Source, "builtin") {
			staged[name] = cloneSkillForReload(skill)
		}
	}
	l.mu.RUnlock()
	builtinCount := len(staged)

	for _, dir := range dirs {
		if err := l.stageUserSkillsFromDir(staged, dir, "user"); err != nil {
			return nil, fmt.Errorf("reload user skills from %q: %w", dir, err)
		}
		if l.agentName != "" {
			agentDir := filepath.Join(dir, l.agentName, "skills")
			if err := l.stageUserSkillsFromDir(staged, agentDir, "user"); err != nil {
				return nil, fmt.Errorf("reload agent skills from %q: %w", agentDir, err)
			}
		}
	}

	return &PreparedSkillReload{
		loader:       l,
		skills:       staged,
		userDirs:     append([]string(nil), dirs...),
		builtinCount: builtinCount,
		userCount:    len(staged) - builtinCount,
	}, nil
}

func (p *PreparedSkillReload) Commit() SkillReloadResult {
	if p == nil || p.loader == nil {
		return SkillReloadResult{}
	}
	l := p.loader
	l.mu.Lock()
	oldUserNames := make(map[string]bool)
	for name, skill := range l.skills {
		if skill != nil && !strings.EqualFold(skill.Source, "builtin") {
			oldUserNames[name] = true
		}
	}
	l.skills = p.skills
	l.userSkillDirs = append([]string(nil), p.userDirs...)
	for scope, invoked := range l.invokedSkills {
		// Task-scoped entries are immutable copies already observed by a running
		// turn. Reload controls future activations; it must not rewrite or remove
		// an in-flight task's instructions.
		if scope != legacyInvokedSkillScope {
			continue
		}
		for name := range oldUserNames {
			delete(invoked, name)
		}
		if len(invoked) == 0 {
			delete(l.invokedSkills, scope)
		}
	}
	l.invalidatePromptCachesLocked()
	l.mu.Unlock()
	return SkillReloadResult{
		BuiltinCount: p.builtinCount,
		UserCount:    p.userCount,
		Directories:  append([]string(nil), p.userDirs...),
	}
}

// ReloadUserSkills builds a complete immutable snapshot from the embedded
// builtin baseline plus every configured user directory. The live map is
// swapped only after all files parse and pass the security scan, so a broken
// edit cannot partially erase or corrupt the running skill catalog.
func (l *SkillsLoader) ReloadUserSkills(dirs []string) (SkillReloadResult, error) {
	prepared, err := l.PrepareUserSkillReload(dirs)
	if err != nil {
		return SkillReloadResult{}, err
	}
	return prepared.Commit(), nil
}

func (l *SkillsLoader) stageUserSkillsFromDir(staged map[string]*Skill, dir, source string) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill root is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".archived" {
			continue
		}
		entryDir := filepath.Join(dir, entry.Name())
		if err := l.stageUserSkill(staged, entryDir, entry.Name(), source); err != nil {
			return err
		}
		subEntries, err := os.ReadDir(entryDir)
		if err != nil {
			return fmt.Errorf("scan skill directory %q: %w", entryDir, err)
		}
		sort.Slice(subEntries, func(i, j int) bool { return subEntries[i].Name() < subEntries[j].Name() })
		for _, sub := range subEntries {
			if !sub.IsDir() || sub.Name() == ".archived" {
				continue
			}
			if err := l.stageUserSkill(staged, filepath.Join(entryDir, sub.Name()), sub.Name(), source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *SkillsLoader) stageUserSkill(staged map[string]*Skill, skillDir, name, source string) error {
	if _, exists := staged[name]; exists {
		return nil // embedded builtin or an earlier configured directory wins
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %q: %w", skillFile, err)
	}
	metadata, skillContent, err := l.ValidateUserSkillDocument(name, skillFile, content)
	if err != nil {
		return err
	}
	staged[name] = &Skill{
		Metadata:        metadata,
		Interface:       skillpath.LoadInterfaceMetadataFromDisk(skillDir),
		Content:         skillContent,
		Path:            skillFile,
		Source:          source,
		SourceDir:       skillDir,
		RuntimeDir:      skillDir,
		RequiresRuntime: skillDirHasRuntimeAssetsOnDisk(skillDir, metadata),
		Raw:             string(content),
		Code:            skillContent,
	}
	return nil
}

func (l *SkillsLoader) ValidateUserSkillDocument(name, skillFile string, content []byte) (SkillMetadata, string, error) {
	frontmatter, _, hasFrontmatter := splitSkillFrontmatter(string(content))
	if hasFrontmatter && strings.TrimSpace(frontmatter) != "" {
		var raw map[string]interface{}
		if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
			return SkillMetadata{}, "", fmt.Errorf("invalid YAML frontmatter in %q: %w", skillFile, err)
		}
		if declaredName, exists := raw["name"]; exists {
			value, isString := declaredName.(string)
			if !isString || value != name {
				return SkillMetadata{}, "", fmt.Errorf("skill frontmatter name must match its directory name")
			}
		}
	}
	metadata := l.ParseMetadata(content, skillFile)
	metadata.Name = name
	skillContent := l.StripFrontmatter(string(content))
	if strings.TrimSpace(skillContent) == "" {
		return SkillMetadata{}, "", fmt.Errorf("skill %q has no instruction body", skillFile)
	}
	cleaned, ok := security.ScanSkillContent(skillContentDetector, name, skillContent)
	if !ok {
		return SkillMetadata{}, "", fmt.Errorf("skill %q failed the prompt-injection security scan", skillFile)
	}
	return metadata, cleaned, nil
}

// LoadAllSkills loads skills from a single filesystem directory.
// Skills already registered (e.g. by builtin) are NOT overridden — first loader wins.
func (l *SkillsLoader) LoadAllSkills(skillsDir, source string) error {
	if skillsDir == "" {
		return nil
	}
	l.rememberUserSkillDir(skillsDir, source)
	log.Printf("begin load skills from %s (source=%s)", skillsDir, source)
	if info, err := os.Stat(filepath.Join(skillsDir, "SKILL.md")); err == nil && !info.IsDir() {
		if err := l.loadSkillDir(skillsDir, filepath.Base(filepath.Clean(skillsDir)), source); err != nil {
			return err
		}
	}
	if err := l.loadSkillsFromDir(skillsDir, source); err != nil {
		return err
	}
	// Agent-specific sub-directory
	if l.agentName != "" {
		agentSkillDir := filepath.Join(skillsDir, l.agentName, "skills")
		if err := l.loadSkillsFromDir(agentSkillDir, source); err != nil {
			return err
		}
	}
	// Refresh builtin skill index into memory
	if source == "builtin" {
		if _, err := l.LoadSkillIndex(); err != nil {
			log.Printf("[WARN] 加载 skill 索引失败: %v", err)
		}
	}
	return nil
}

// LoadBuiltinSkillsFromFS loads built-in skills directly from an embedded fs.FS.
// It keeps SKILL.md content and the index in memory and avoids materializing
// bundled skills into the user's skills directory.
func (l *SkillsLoader) LoadBuiltinSkillsFromFS(fsys fs.FS, root string) error {
	if fsys == nil || root == "" {
		return nil
	}
	l.mu.Lock()
	l.builtinFS = fsys
	l.builtinRoot = filepath.ToSlash(root)
	l.mu.Unlock()
	log.Printf("begin load builtin skills from embedded fs: %s", root)
	if err := l.loadSkillsFromFS(fsys, root, "builtin"); err != nil {
		return err
	}
	if _, err := l.LoadSkillIndexFromFS(fsys, filepath.Join(root, "OPENKITTY_INDEX.json")); err != nil {
		log.Printf("[WARN] 加载 embedded skill 索引失败: %v", err)
	}
	return nil
}

// loadSkillDir loads a single skill from a directory if SKILL.md exists.
// Skips if already loaded (builtin has highest priority, never overridden).
func (l *SkillsLoader) loadSkillDir(skillDir, name, source string) error {
	l.mu.RLock()
	_, exists := l.skills[name]
	l.mu.RUnlock()
	if exists {
		return nil // builtin already won
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		return nil
	}
	content, err := os.ReadFile(skillFile)
	if err != nil {
		return nil
	}
	var metadata SkillMetadata
	var skillContent string
	if strings.EqualFold(strings.TrimSpace(source), "builtin") {
		metadata = l.ParseMetadata(content, skillFile)
		metadata.Name = name
		skillContent = l.StripFrontmatter(string(content))
		if cleaned, ok := security.ScanSkillContent(skillContentDetector, name, skillContent); !ok {
			log.Printf("[SECURITY] 技能 %s 内容检测到注入攻击，已拒绝加载", name)
			return nil
		} else {
			skillContent = cleaned
		}
	} else {
		metadata, skillContent, err = l.ValidateUserSkillDocument(name, skillFile, content)
		if err != nil {
			return err
		}
	}
	requiresRuntime := skillDirHasRuntimeAssetsOnDisk(skillDir, metadata)
	interfaceMetadata := skillpath.LoadInterfaceMetadataFromDisk(skillDir)
	l.mu.Lock()
	l.cacheSkillLocked(name, &Skill{
		Metadata:        metadata,
		Interface:       interfaceMetadata,
		Content:         skillContent,
		Path:            l.normalizeSkillPath(source, name, skillFile),
		Source:          source,
		SourceDir:       skillDir,
		RuntimeDir:      skillDir,
		RequiresRuntime: requiresRuntime,
		Raw:             string(content),
		Code:            skillContent,
	})
	if l.shouldCacheBuiltin(source) {
		l.cacheBuiltinDocLocked(name, string(content))
	}
	l.mu.Unlock()
	metricsPath := filepath.Join(filepath.Dir(skillDir), ".skill_metrics.json")
	skilltools.RecordSkillLoad(metricsPath, name)
	return nil
}

func (l *SkillsLoader) loadSkillDirFromFS(fsys fs.FS, skillDir, name, source string) {
	l.mu.RLock()
	_, exists := l.skills[name]
	l.mu.RUnlock()
	if exists {
		return
	}

	skillFile := filepath.ToSlash(filepath.Join(skillDir, "SKILL.md"))
	content, err := fs.ReadFile(fsys, skillFile)
	if err != nil {
		return
	}

	metadata := l.ParseMetadata(content, skillFile)
	metadata.Name = name
	skillContent := l.StripFrontmatter(string(content))
	if cleaned, ok := security.ScanSkillContent(skillContentDetector, name, skillContent); !ok {
		log.Printf("[SECURITY] 技能 %s 内容检测到注入攻击，已拒绝加载", name)
		return
	} else {
		skillContent = cleaned
	}

	requiresRuntime := l.shouldCacheBuiltin(source) && EmbeddedSkillHasRuntimeAssets(fsys, skillDir, metadata)
	interfaceMetadata := skillpath.LoadInterfaceMetadataFromFS(fsys, skillDir)
	l.mu.Lock()
	l.cacheSkillLocked(name, &Skill{
		Metadata:        metadata,
		Interface:       interfaceMetadata,
		Content:         skillContent,
		Path:            l.normalizeSkillPath(source, name, skillFile),
		Source:          source,
		SourceDir:       filepath.ToSlash(skillDir),
		Embedded:        l.shouldCacheBuiltin(source),
		RequiresRuntime: requiresRuntime,
		Raw:             string(content),
		Code:            skillContent,
	})
	if l.shouldCacheBuiltin(source) {
		l.cacheBuiltinDocLocked(name, string(content))
	}
	l.mu.Unlock()
}

func (l *SkillsLoader) loadSkillsFromFS(fsys fs.FS, dir, source string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".archived" {
			continue
		}

		entryDir := filepath.ToSlash(filepath.Join(dir, entry.Name()))
		l.loadSkillDirFromFS(fsys, entryDir, entry.Name(), source)

		subEntries, err := fs.ReadDir(fsys, entryDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			l.loadSkillDirFromFS(fsys, filepath.ToSlash(filepath.Join(entryDir, sub.Name())), sub.Name(), source)
		}
	}

	return nil
}

func EmbeddedSkillHasRuntimeAssets(fsys fs.FS, skillDir string, metadata SkillMetadata) bool {
	if isEntrypointPath(metadata.Entrypoint) {
		return true
	}
	scriptExt := map[string]bool{
		".py": true, ".js": true, ".mjs": true, ".cjs": true,
		".sh": true, ".bash": true, ".ps1": true, ".bat": true, ".cmd": true,
	}
	skillDir = filepath.ToSlash(skillDir)
	found := false
	_ = fs.WalkDir(fsys, skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if path == skillDir {
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			switch name {
			case "scripts", "bin":
				found = true
				return fs.SkipDir
			case "node_modules", ".venv", "venv", "__pycache__":
				return fs.SkipDir
			}
			return nil
		}
		switch name {
		case "package.json", "package-lock.json", "requirements.txt", "pyproject.toml":
			found = true
			return nil
		}
		if scriptExt[strings.ToLower(filepath.Ext(name))] {
			found = true
		}
		return nil
	})
	return found
}

func skillDirHasRuntimeAssetsOnDisk(skillDir string, metadata SkillMetadata) bool {
	if isEntrypointPath(metadata.Entrypoint) {
		return true
	}
	scriptExt := map[string]bool{
		".py": true, ".js": true, ".mjs": true, ".cjs": true,
		".sh": true, ".bash": true, ".ps1": true, ".bat": true, ".cmd": true,
	}
	found := false
	_ = filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if path == skillDir {
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			switch name {
			case "scripts", "bin":
				found = true
				return filepath.SkipDir
			case "node_modules", ".venv", "venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		switch name {
		case "package.json", "package-lock.json", "requirements.txt", "pyproject.toml":
			found = true
			return nil
		}
		if scriptExt[strings.ToLower(filepath.Ext(name))] {
			found = true
		}
		return nil
	})
	return found
}

func (l *SkillsLoader) EnsureSkillRuntime(skill *Skill) (string, error) {
	return l.ensureSkillRuntime(context.Background(), skill)
}

func (l *SkillsLoader) ensureSkillRuntime(ctx context.Context, skill *Skill) (string, error) {
	if skill == nil {
		return "", fmt.Errorf("skill is nil")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !skill.Embedded {
		if skill.RuntimeDir == "" {
			skill.RuntimeDir = skill.SourceDir
		}
		return skill.RuntimeDir, nil
	}
	fsys := l.builtinFS
	root := l.builtinRoot
	if fsys == nil {
		return "", fmt.Errorf("embedded filesystem unavailable for builtin skill %s", skill.Metadata.Name)
	}

	sourceDir := filepath.ToSlash(skill.SourceDir)
	rel := strings.TrimPrefix(sourceDir, filepath.ToSlash(root))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = SafeFilename(skill.Metadata.Name)
	}

	baseDir, err := l.embeddedRuntimeBaseDir(ctx)
	if err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(baseDir, filepath.FromSlash(rel))
	if err := EnsurePathWithin(baseDir, runtimeDir); err != nil {
		return "", err
	}

	digest := l.runtimeDigests[sourceDir]
	if digest == "" {
		var err error
		digest, err = EmbeddedFSDirDigest(fsys, sourceDir)
		if err != nil {
			return "", err
		}
		l.runtimeDigests[sourceDir] = digest
	}
	markerPath := filepath.Join(runtimeDir, ".openkitty-embedded.sha256")
	if marker, err := os.ReadFile(markerPath); err == nil && strings.TrimSpace(string(marker)) == digest {
		current, checkErr := embeddedRuntimeCriticalFilesCurrent(fsys, sourceDir, runtimeDir)
		if checkErr != nil {
			return "", checkErr
		}
		if current {
			skill.RuntimeDir = runtimeDir
			return runtimeDir, nil
		}
	}

	// Materialize only when the embedded version changed, the runtime is
	// incomplete, or a critical instruction/script was modified. This avoids
	// rewriting hundreds of template/icon files on every use_skill call while
	// still repairing stale executable runtime content.
	if err := MaterializeFSDir(fsys, sourceDir, runtimeDir); err != nil {
		return "", err
	}
	if err := os.WriteFile(markerPath, []byte(digest+"\n"), 0644); err != nil {
		return "", err
	}
	skill.RuntimeDir = runtimeDir
	return runtimeDir, nil
}

// embeddedRuntimeBaseDir keeps executable builtin assets inside the
// authenticated user's immutable workspace boundary. Local and control-plane
// executions retain the historical process-user cache under OpenKittyHome.
func (l *SkillsLoader) embeddedRuntimeBaseDir(ctx context.Context) (string, error) {
	return EmbeddedRuntimeBaseDirForContext(ctx, l.workspace, l.runtimeScope)
}

func EmbeddedRuntimeBaseDirForContext(
	ctx context.Context,
	loaderWorkspace string,
	resolveScope func(context.Context) (bool, string),
) (string, error) {
	workspaceScoped, workspace := false, ""
	if resolveScope != nil {
		workspaceScoped, workspace = resolveScope(ctx)
	}
	if !workspaceScoped {
		return filepath.Join(openKittyHome(), "runtime", "builtin"), nil
	}

	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	}
	if workspace == "" {
		workspace = strings.TrimSpace(loaderWorkspace)
	}
	if workspace == "" {
		return "", fmt.Errorf("workspace-scoped skill runtime requires a resolved workspace")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve tenant skill runtime workspace: %w", err)
	}
	workspace = filepath.Clean(workspace)

	if loaderWorkspace = strings.TrimSpace(loaderWorkspace); loaderWorkspace != "" &&
		!projectctx.SameWorkspace(loaderWorkspace, workspace) {
		return "", fmt.Errorf("workspace-scoped skill runtime does not match the skill loader workspace")
	}

	baseDir := filepath.Join(workspace, ".openkitty", "runtime", "builtin")
	if !projectctx.ContainsPath(workspace, baseDir) {
		return "", fmt.Errorf("tenant skill runtime escapes the user's workspace")
	}
	if err := projectctx.EnsureResolvedPathInside(workspace, baseDir); err != nil {
		return "", fmt.Errorf("resolve tenant skill runtime boundary: %w", err)
	}
	return baseDir, nil
}

func EmbeddedFSDirDigest(fsys fs.FS, sourceDir string) (string, error) {
	hasher := sha256.New()
	sourceDir = filepath.ToSlash(sourceDir)
	err := fs.WalkDir(fsys, sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, filepath.ToSlash(path))
		if err != nil {
			return err
		}
		_, _ = hasher.Write([]byte(filepath.ToSlash(path)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func embeddedRuntimeCriticalFilesCurrent(fsys fs.FS, sourceDir, runtimeDir string) (bool, error) {
	sourceDir = filepath.ToSlash(sourceDir)
	current := true
	err := fs.WalkDir(fsys, sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !current {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.FromSlash(sourceDir), filepath.FromSlash(path))
		if err != nil {
			return err
		}
		if !embeddedRuntimeCriticalFile(rel) {
			return nil
		}
		embedded, err := fs.ReadFile(fsys, filepath.ToSlash(path))
		if err != nil {
			return err
		}
		runtimeData, err := os.ReadFile(filepath.Join(runtimeDir, rel))
		if err != nil || !bytes.Equal(embedded, runtimeData) {
			current = false
		}
		return nil
	})
	return current, err
}

func embeddedRuntimeCriticalFile(rel string) bool {
	normalized := strings.ToLower(filepath.ToSlash(rel))
	base := filepath.Base(normalized)
	if base == "skill.md" || base == "requirements.txt" || base == "pyproject.toml" || base == "package.json" || base == "package-lock.json" {
		return true
	}
	// Runtime code can live under assets/ as well as scripts/. For example,
	// presentation scaffolds copy assets/task-pptx/pptx_design.py into each
	// task. Treat executable source as critical so a matching/stale marker can
	// never preserve an old helper implementation.
	switch filepath.Ext(base) {
	case ".py", ".pyi", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".ps1", ".sh", ".bat", ".cmd":
		return true
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "scripts" || part == "bin" {
			return true
		}
	}
	return false
}

func EnsurePathWithin(baseDir, target string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("runtime path escapes base dir: %s", target)
	}
	return nil
}

func MaterializeFSDir(fsys fs.FS, sourceDir, runtimeDir string) error {
	sourceDir = filepath.ToSlash(sourceDir)
	return fs.WalkDir(fsys, sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.FromSlash(sourceDir), filepath.FromSlash(path))
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(runtimeDir, 0755)
		}
		target := filepath.Join(runtimeDir, rel)
		if err := EnsurePathWithin(runtimeDir, target); err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := fs.ReadFile(fsys, filepath.ToSlash(path))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func SafeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "skill"
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "skill"
	}
	return name
}

// loadSkillsFromDir loads skills from a filesystem directory (user skills, on disk).
func (l *SkillsLoader) loadSkillsFromDir(dir, source string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	log.Printf("begin load skills in %s dir", dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".archived" {
			continue
		}

		entryDir := filepath.Join(dir, entry.Name())

		// Load top-level skill SKILL.md
		if err := l.loadSkillDir(entryDir, entry.Name(), source); err != nil {
			return err
		}

		// Also scan one level deeper for nested sub-skills
		subEntries, err := os.ReadDir(entryDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			if err := l.loadSkillDir(filepath.Join(entryDir, sub.Name()), sub.Name(), source); err != nil {
				return err
			}
		}
	}

	return nil
}

func splitSkillFrontmatter(content string) (frontmatter, body string, ok bool) {
	normalized := strings.TrimPrefix(content, "\ufeff")
	firstEnd := strings.IndexByte(normalized, '\n')
	if firstEnd < 0 {
		return "", content, false
	}
	firstLine := strings.TrimSuffix(normalized[:firstEnd], "\r")
	if strings.TrimSpace(firstLine) != "---" {
		return "", content, false
	}

	frontStart := firstEnd + 1
	lineStart := frontStart
	for lineStart <= len(normalized) {
		relativeEnd := strings.IndexByte(normalized[lineStart:], '\n')
		lineEnd := len(normalized)
		nextLine := len(normalized)
		if relativeEnd >= 0 {
			lineEnd = lineStart + relativeEnd
			nextLine = lineEnd + 1
		}
		line := strings.TrimSuffix(normalized[lineStart:lineEnd], "\r")
		if strings.TrimSpace(line) == "---" {
			return normalized[frontStart:lineStart], normalized[nextLine:], true
		}
		if relativeEnd < 0 {
			break
		}
		lineStart = nextLine
	}
	return "", content, false
}

func (l *SkillsLoader) ParseMetadata(content []byte, sourcePath ...string) SkillMetadata {
	metadata := SkillMetadata{
		Requires:   make(map[string]interface{}),
		Tags:       []string{},
		Parameters: []SkillParameter{},
		Type:       SkillTypeBuiltin, // 默认类型
	}

	// 将 []byte 转为字符串
	yamlContent, _, ok := splitSkillFrontmatter(string(content))
	if !ok {
		return metadata
	}
	yamlContent = strings.TrimSpace(yamlContent)

	if yamlContent == "" {
		return metadata
	}

	// 解析 YAML
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &raw); err != nil {
		source := "skill content"
		if len(sourcePath) > 0 && strings.TrimSpace(sourcePath[0]) != "" {
			source = sourcePath[0]
		}
		log.Printf("YAML parse error in %s: %v", source, err)
		return metadata
	}

	// 提取 name 字段
	if name, ok := raw["name"].(string); ok {
		metadata.Name = name
	}

	// 关键：处理 description 可能的各种类型
	switch desc := raw["description"].(type) {
	case string:
		metadata.Description = desc
	case []interface{}: // 可能被解析为数组
		parts := make([]string, len(desc))
		for i, part := range desc {
			if str, ok := part.(string); ok {
				parts[i] = str
			}
		}
		metadata.Description = strings.Join(parts, "\n")
	default:
		if desc != nil {
			metadata.Description = fmt.Sprintf("%v", desc)
		}
	}

	// 提取 version 字段
	if version, ok := raw["version"].(string); ok {
		metadata.Version = version
	}
	// metadata.version is the standard form and takes precedence over the
	// legacy top-level version field when both are present.
	nestedMetadata, hasNestedMetadata := raw["metadata"].(map[string]interface{})
	if hasNestedMetadata {
		if version, ok := nestedMetadata["version"].(string); ok {
			metadata.Version = version
		}
	}

	// 提取 author 字段
	if author, ok := raw["author"].(string); ok {
		metadata.Author = author
	}
	if hasNestedMetadata {
		if author, ok := nestedMetadata["author"].(string); ok {
			metadata.Author = author
		}
	}

	// 提取 type 字段
	if skillType, ok := raw["type"].(string); ok {
		metadata.Type = NormalizeSkillType(skillType)
	}

	// 提取 entrypoint 字段
	if entrypoint, ok := raw["entrypoint"].(string); ok {
		metadata.Entrypoint = entrypoint
	}

	// 提取 always 字段
	if always, ok := raw["always"].(bool); ok {
		metadata.Always = always
	}

	// 解析参数
	if params, ok := raw["parameters"].([]interface{}); ok {
		for _, p := range params {
			if paramMap, ok := p.(map[string]interface{}); ok {
				param := SkillParameter{
					Required: true,
				}
				if name, ok := paramMap["name"].(string); ok {
					param.Name = name
				}
				if pType, ok := paramMap["type"].(string); ok {
					param.Type = pType
				}
				if desc, ok := paramMap["description"].(string); ok {
					param.Description = desc
				}
				if required, ok := paramMap["required"].(bool); ok {
					param.Required = required
				}
				if def, ok := paramMap["default"]; ok {
					param.Default = def
				}
				metadata.Parameters = append(metadata.Parameters, param)
			}
		}
	}

	// 解析标签
	metadata.Aliases = append(metadata.Aliases, metadataStringList(raw["aliases"])...)
	metadata.Tags = append(metadata.Tags, metadataStringList(raw["tags"])...)
	metadata.ReadWhen = append(metadata.ReadWhen, metadataStringList(raw["read_when"])...)
	if hasNestedMetadata {
		metadata.Aliases = append(metadata.Aliases, metadataStringList(nestedMetadata["aliases"])...)
		metadata.Tags = append(metadata.Tags, metadataStringList(nestedMetadata["tags"])...)
		metadata.ReadWhen = append(metadata.ReadWhen, metadataStringList(nestedMetadata["read_when"])...)
	}

	// 解析 requires
	if requires, ok := raw["requires"].(map[string]interface{}); ok {
		metadata.Requires = requires
	}

	return metadata
}

func metadataStringList(value interface{}) []string {
	switch v := value.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{strings.TrimSpace(v)}
	default:
		return nil
	}
}

func (l *SkillsLoader) extractCode(content string) string {
	if _, body, ok := splitSkillFrontmatter(content); ok {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(content)
}

// stripFrontmatter 移除frontmatter
func (l *SkillsLoader) StripFrontmatter(content string) string {
	if _, body, ok := splitSkillFrontmatter(content); ok {
		return body
	}
	return content
}

func extractIntentKeywords(question string) []string {
	keywords := make([]string, 0)
	seen := make(map[string]bool)
	questionLower := strings.ToLower(question)
	if strings.Contains(questionLower, "weixin.qq.com/sph/") {
		for _, kw := range []string{"wx-video-account-notes", "视频号", "video-note", "asr", "ocr", "download"} {
			if !seen[kw] {
				keywords = append(keywords, kw)
				seen[kw] = true
			}
		}
	}
	if strings.Contains(questionLower, "generator: pptmaster") ||
		strings.Contains(questionLower, "generator=pptmaster") ||
		strings.Contains(questionLower, "pptmaster-main") ||
		strings.Contains(questionLower, "ppt-master") ||
		strings.Contains(questionLower, "pptmaster") {
		for _, kw := range []string{"pptmaster", "ppt-master", "generator:pptmaster", "generator=pptmaster"} {
			if !seen[kw] {
				keywords = append(keywords, kw)
				seen[kw] = true
			}
		}
	}

	// 常见任务意图模式
	patterns := map[string][]string{
		// 编程开发
		"code":     {"写代码", "编写", "实现", "code", "write", "implement", "function", "class", "开发", "生成代码", "脚本", "编程", "函数"},
		"analysis": {"分析", "分析文件", "analyze", "统计", "计算", "count", "calculate", "汇总", "对比"},
		"file_ops": {"读取", "读文件", "read", "写入", "保存", "write", "save", "目录", "directory", "删除", "重命名", "移动", "创建文件"},
		"data":     {"数据", "csv", "json", "数据提取", "data", "extract", "parse", "清洗", "转换", "格式化", "导入", "导出"},
		"test":     {"测试", "test", "验证", "verify", "check", "单元测试", "用例", "自测"},
		"debug":    {"调试", "debug", "错误", "error", "fix", "修复", "排查", "异常", "崩溃", "报错"},
		"api":      {"接口", "api", "请求", "调用", "post", "get", "http", "rpc", "接口文档", "传参"},
		"db":       {"数据库", "sql", "mysql", "查询", "增删改查", "insert", "update", "delete", "表", "字段"},
		"git":      {"git", "提交", "推送", "拉取", "分支", "合并", "commit", "push", "pull"},

		// AI & 平台
		"xialiaoai": {"虾聊", "xialiao", "Agent社区", "智能体", "AI社区"},
		"search":    {"搜索", "查询", "查找", "查一下", "搜一下", "search", "find", "lookup", "检索"},

		// 办公 & 报表
		"report": {"报表", "日报", "周报", "月报", "统计报表", "图表", "可视化", "excel", "表格", "ppt", "pptx", "幻灯片", "演示文稿", "演示", "presentation"},
		"chart":  {"图表", "折线图", "柱状图", "饼图", "绘图", "画图"},
		"notify": {"通知", "提醒", "告警", "alert", "消息", "推送", "钉钉", "企业微信"},

		// 运维 & 部署
		"deploy":  {"部署", "发布", "上线", "deploy", "publish"},
		"monitor": {"监控", "状态", "健康检查", "指标", "性能"},
		"log":     {"日志", "查看日志", "log", "日志分析", "排查日志"},

		// 工具 & 其他
		"config":    {"配置", "设置", "参数", "修改配置", "环境变量"},
		"schedule":  {"定时", "计划任务", "调度", "定时任务"},
		"help":      {"帮助", "怎么用", "使用方法", "教程", "说明"},
		"image":     {"图片", "截图", "画图", "生成图片", "image", "img", "图表", "海报"},
		"translate": {"翻译", "translate", "中英文", "转英文", "转中文"},
		"summarize": {"总结", "摘要", "概括", "提炼", "总结内容"},
		"optimize":  {"优化", "提速", "改进", "重构", "优化代码", "提升性能"},
		"document":  {"文档", "说明文档", "写文档", "注释", "markdown", "接口文档"},
		"security":  {"安全", "权限", "加密", "认证", "登录", "token", "漏洞"},
		"backup":    {"备份", "恢复", "backup", "还原", "数据备份"},
		"clean":     {"清理", "删除", "清空", "清除", "磁盘清理"},
		"download":  {"下载", "获取", "download", "导出文件"},
		"upload":    {"上传", "提交文件", "upload", "导入文件"},
		"run":       {"运行", "执行", "启动", "开始", "run", "execute", "启动服务"},
		"stop":      {"停止", "关闭", "暂停", "终止", "stop", "kill"},
		"restart":   {"重启", "重新启动", "reboot", "restart"},
		"order":     {"排序", "排序方式", "order", "sort", "升序", "降序"},
		"filter":    {"过滤", "筛选", "条件", "filter", "where"},
		"approval":  {"审批", "审核", "流程", "approve", "工单"},
		"sendemail": {"发邮件", "发邮箱", "163"},
	}

	for intent, patternKeywords := range patterns {
		for _, kw := range patternKeywords {
			if strings.Contains(questionLower, kw) {
				if !seen[intent] {
					keywords = append(keywords, intent)
					seen[intent] = true
				}
				if !seen[kw] {
					keywords = append(keywords, kw)
					seen[kw] = true
				}
				break
			}
		}
	}

	// 始终进行分词，补充 tokenize 产生的关键词
	tokens := tokenizeKeywords(question)
	for _, t := range tokens {
		if !seen[t] {
			keywords = append(keywords, t)
			seen[t] = true
		}
	}

	return keywords
}

func hasPPTMasterIntent(keywords []string) bool {
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		switch kw {
		case "pptmaster", "ppt-master", "pptmaster-main", "generator:pptmaster", "generator=pptmaster":
			return true
		}
	}
	return false
}

func hasPPTIntent(keywords []string) bool {
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		switch kw {
		case "ppt", "pptx", "presentation", "report", "chart", "slides", "幻灯片", "演示", "演示文稿", "报告":
			return true
		}
		if strings.Contains(kw, "ppt") || strings.Contains(kw, "presentation") {
			return true
		}
	}
	return false
}

func (l *SkillsLoader) BuildSkillsSummaryWithIntent(user_input string) string {
	intentKeywords := extractIntentKeywords(user_input)
	searchResults := l.SearchSkills(intentKeywords, 2, true)
	// 检查是否找到技能
	if strings.Contains(searchResults, "未找到匹配关键词") {
		return "🔍 未找到与需求匹配的技能，以下是可用技能示例：\n\n" + l.getTopNSkills(3)
	}

	// 统计找到的技能数量
	skillCount := l.countSkillsInSearchResult(searchResults)
	if skillCount < 50 {
		// 补充到3个
		return searchResults + "\n\n" + l.getComplementarySkills(searchResults, 50-skillCount)
	}
	return searchResults
}

func (l *SkillsLoader) countSkillsInSearchResult(result string) int {
	count := 0
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		// 匹配 "### 数字. 技能名" 的格式
		if matched, _ := regexp.MatchString(`^### \d+\.`, line); matched {
			count++
		}
	}
	return count
}

func (l *SkillsLoader) getTopNSkills(n int) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.skills) == 0 {
		return "暂无可用技能。"
	}

	// 收集所有技能名称并排序
	skillNames := make([]string, 0, len(l.skills))
	for name := range l.skills {
		skillNames = append(skillNames, name)
	}
	sort.Strings(skillNames)

	// 只取前N个
	if len(skillNames) > n {
		skillNames = skillNames[:n]
	}

	var builder strings.Builder
	for i, name := range skillNames {
		skill := l.skills[name]
		builder.WriteString(fmt.Sprintf("%d. **%s**", i+1, name))
		if skill.Metadata.Description != "" {
			builder.WriteString(fmt.Sprintf(": %s", truncate(PromptSafeDescription(skill.Metadata.Description), 130)))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

// 获取补充技能（排除已显示的，只取需要的数量）
func (l *SkillsLoader) getComplementarySkills(searchResults string, needCount int) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	// 提取搜索结果中已有的技能名称
	existingSkills := make(map[string]bool)
	re := regexp.MustCompile(`### \d+\. (\S+)`)
	matches := re.FindAllStringSubmatch(searchResults, -1)
	for _, match := range matches {
		if len(match) > 1 {
			existingSkills[match[1]] = true
		}
	}

	// 收集未显示的其他技能，按名称排序
	otherSkills := make([]string, 0)
	for name := range l.skills {
		if !existingSkills[name] {
			otherSkills = append(otherSkills, name)
		}
	}
	sort.Strings(otherSkills)

	// 只取需要的数量
	if len(otherSkills) > needCount {
		otherSkills = otherSkills[:needCount]
	}

	if len(otherSkills) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("## 📌 其他推荐技能\n\n")
	for i, name := range otherSkills {
		skill := l.skills[name]
		builder.WriteString(fmt.Sprintf("%d. **%s**", i+1, name))
		if skill.Metadata.Version != "" {
			builder.WriteString(fmt.Sprintf(" v%s", skill.Metadata.Version))
		}
		if skill.Metadata.Description != "" {
			builder.WriteString(fmt.Sprintf(": %s", truncate(PromptSafeDescription(skill.Metadata.Description), 30)))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

func (l *SkillsLoader) BuildSkillsSummary() string {
	l.mu.RLock()
	if l.summaryCache != "" {
		defer l.mu.RUnlock()
		return l.summaryCache
	}

	if len(l.skills) == 0 {
		l.mu.RUnlock()
		return ""
	}

	var summary strings.Builder
	summary.WriteString("## Available Skills\n\n")

	// Group by always/normal
	alwaysSkills := make([]string, 0)
	normalSkills := make([]string, 0)
	for name, skill := range l.skills {
		if skill.Metadata.Always {
			alwaysSkills = append(alwaysSkills, name)
		} else {
			normalSkills = append(normalSkills, name)
		}
	}
	if len(alwaysSkills) > 0 {
		summary.WriteString("### Always Available\n\n")
		for _, name := range alwaysSkills {
			skill := l.skills[name]

			summary.WriteString(fmt.Sprintf("**%s**", name))

			summary.WriteString("\n")
			if skill.Metadata.Description != "" {
				summary.WriteString(truncate(fmt.Sprintf("> %s\n", PromptSafeDescription(skill.Metadata.Description)), 30))
			}
			if len(skill.Metadata.Tags) > 0 {
				summary.WriteString(fmt.Sprintf("Tags: `%s`\n", strings.Join(skill.Metadata.Tags, "`, `")))
			}
			summary.WriteString("\n")
		}
		summary.WriteString("\n")
	}

	// Normal skills - full details (limited to 2 examples)
	if len(normalSkills) > 0 {
		summary.WriteString("### Other Skills\n\n")
		for _, name := range normalSkills {
			skill := l.skills[name]

			summary.WriteString(fmt.Sprintf("**%s**", name))
			if skill.Metadata.Version != "" {
				summary.WriteString(fmt.Sprintf(" v%s", skill.Metadata.Version))
			}
			if skill.Metadata.Description != "" {
				summary.WriteString(fmt.Sprintf(": %s", PromptSafeDescription(skill.Metadata.Description)))
			}
			summary.WriteString("\n")

		}
		summary.WriteString("\n")
	}

	// Usage hint
	summary.WriteString("### Usage\n\n")
	summary.WriteString("1 Use tool `list_skills` to search skills by name, tag or description.\n")
	summary.WriteString("2 Built-in skills are already in memory; prefer `use_skill` or the routed tool directly.\n")
	summary.WriteString("3 Read external skill files only when a custom user skill needs inspection.\n")
	summary.WriteString("4. **Combine**: Chain multiple skills to complete complex tasks.\n")
	summary.WriteString("5. **Dependencies**: Install required tools first if needed.\n\n")
	result := summary.String()
	l.mu.RUnlock()
	l.mu.Lock()
	l.summaryCache = result
	l.mu.Unlock()
	return result
}

// PromptSafeDescription removes instruction-like text from skill descriptions before they enter model prompts.
func PromptSafeDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	stripPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\s*read\s+the\s+full\s+skill\.md\s+before\s+acting\.?`),
		regexp.MustCompile(`(?i)\s*read\s+the\s+skill\.md\s+before\s+acting\.?`),
		regexp.MustCompile(`(?i)\s*read\s+skill\.md\s+first\.?`),
		regexp.MustCompile(`(?i)\s*read\s+the\s+full\s+skill\.md\.?`),
	}
	for _, re := range stripPatterns {
		desc = re.ReplaceAllString(desc, "")
	}
	desc = strings.Join(strings.Fields(desc), " ")
	return strings.Trim(desc, " ;,")
}

func DescriptionPromptSummary(desc string, maxRunes int) string {
	desc = PromptSafeDescription(desc)
	if maxRunes <= 0 || desc == "" {
		return ""
	}
	runes := []rune(desc)
	if len(runes) <= maxRunes {
		return desc
	}
	if maxRunes < 120 {
		return truncate(desc, maxRunes)
	}
	marker := " ... "
	markerLen := len([]rune(marker))
	headLen := maxRunes * 2 / 3
	tailLen := maxRunes - headLen - markerLen
	if tailLen < maxRunes/4 {
		tailLen = maxRunes / 4
		headLen = maxRunes - tailLen - markerLen
	}
	if headLen <= 0 || tailLen <= 0 {
		return truncate(desc, maxRunes)
	}
	head := strings.TrimSpace(string(runes[:headLen]))
	tail := strings.TrimSpace(string(runes[len(runes)-tailLen:]))
	return head + marker + tail
}

// keywordTranslations maps Chinese-English search terms for skill matching.
var keywordTranslations = map[string][]string{
	"截图":         {"screenshot", "screen capture", "截屏"},
	"截屏":         {"screenshot", "screen capture", "截图"},
	"录音":         {"audio", "record", "microphone", "录歌", "麦克风"},
	"录歌":         {"audio", "record", "录音"},
	"麦克风":        {"audio", "microphone", "record"},
	"图片处理":       {"image_process", "process image", "resize", "crop", "图片裁剪", "图片缩放"},
	"提取":         {"extract", "parse", "ocr"},
	"PDF":        {"extract", "pdf"},
	"磁盘":         {"sys", "disk", "system", "空间"},
	"空间":         {"sys", "disk", "storage"},
	"进程":         {"sys", "process", "系统"},
	"系统":         {"sys", "system", "admin"},
	"远程":         {"remote", "ssh", "deploy", "部署"},
	"部署":         {"remote", "deploy", "ssh"},
	"SSH":        {"remote", "deploy"},
	"screenshot": {"截图", "截屏", "screen"},
	"audio":      {"录音", "音频", "声音"},
	"extract":    {"提取", "解析", "ocr"},
	"remote":     {"远程", "ssh", "部署"},
	"sys":        {"系统", "磁盘", "进程", "管理"},
}

// expandKeywords adds Chinese↔English translations to search keywords.
func expandKeywords(keywords []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" || seen[kw] {
			continue
		}
		seen[kw] = true
		result = append(result, kw)
		// Add translations
		if trans, ok := keywordTranslations[kw]; ok {
			for _, t := range trans {
				t = strings.ToLower(t)
				if !seen[t] {
					seen[t] = true
					result = append(result, t)
				}
			}
		}
	}
	return result
}

func (l *SkillsLoader) SearchSkills(keywords []string, limit int, includeContent bool) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	keywords = expandKeywords(keywords)
	if len(l.skills) == 0 {
		return "暂无可用技能。"
	}

	// 收集匹配结果
	type match struct {
		name     string
		skill    *Skill
		score    int
		snippets []string
	}
	matches := make([]match, 0)

	for name, skill := range l.skills {
		score, snippets := l.CalculateSkillMatchScore(skill, keywords)
		if score > 0 {
			matches = append(matches, match{
				name:     name, // 这里用 map 的 key 作为名称
				skill:    skill,
				score:    score,
				snippets: snippets,
			})
		}
	}

	// 按分数排序
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	// 限制数量
	if len(matches) > limit {
		matches = matches[:limit]
	}

	if len(matches) == 0 {
		return fmt.Sprintf("未找到匹配关键词 [%s] 的技能。\n\n建议：\n- 尝试使用更通用的关键词\n- 或不传 keywords 参数查看所有技能列表", strings.Join(keywords, ", "))
	}

	// 构建输出
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("🔍 找到 **%d** 个匹配关键词 [%s] 的技能：\n\n", len(matches), strings.Join(keywords, ", ")))

	for i, m := range matches {
		builder.WriteString(fmt.Sprintf("### %d. %s\n", i+1, m.name))

		if m.skill.Metadata.Description != "" {
			builder.WriteString(fmt.Sprintf("  描述: %s\n", DescriptionPromptSummary(m.skill.Metadata.Description, 600)))
		}
		if len(m.skill.Metadata.Tags) > 0 {
			builder.WriteString(fmt.Sprintf("  标签: `%s`\n", strings.Join(m.skill.Metadata.Tags, "`, `")))
		}
		if strings.EqualFold(strings.TrimSpace(m.skill.Source), "builtin") || m.skill.Embedded {
			builder.WriteString("  来源: `builtin`（内存）\n")
		} else {
			source := strings.TrimSpace(m.skill.Source)
			if source == "" {
				source = "user"
			}
			builder.WriteString(fmt.Sprintf("  来源: `%s`（用户目录/磁盘）\n", source))
		}

		// 匹配度提示
		builder.WriteString(fmt.Sprintf("  📊 匹配度: %d\n", m.score))

		// 内容片段
		if includeContent && len(m.snippets) > 0 {
			builder.WriteString("  📄 相关内容:\n")
			for _, snippet := range m.snippets {
				builder.WriteString(fmt.Sprintf("    > ...%s...\n", snippet))
			}
		}

		builder.WriteString(fmt.Sprintf("  📁 位置: `%s`\n", m.skill.Path))
		builder.WriteString("\n")
	}

	builder.WriteString("---\n")
	builder.WriteString(fmt.Sprintf("💡 共 %d 个技能匹配，内置技能已在内存中；优先直接调用或使用 `use_skill`。\n", len(matches)))

	return builder.String()
}

// GetTopSkills returns the top N matching skill names for given keywords.
// Used for progressive loading in the system prompt.
func (l *SkillsLoader) GetTopSkills(keywords []string, limit int) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	keywords = expandKeywords(keywords)

	type match struct {
		name  string
		score int
	}
	var matches []match

	for name, skill := range l.skills {
		score, _ := l.CalculateSkillMatchScore(skill, keywords)
		if score > 0 {
			matches = append(matches, match{name: name, score: score})
		}
	}

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].name < matches[j].name
		}
		return matches[i].score > matches[j].score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.name
	}
	return result
}

var skillRoutingControlLabelPattern = regexp.MustCompile(`(?im)(?:^|\n)\s*[\[【]\s*(?:用户补充|系统补充|上下文补充|user\s+supplement|additional\s+context)\s*[\]】]\s*[:：]\s*`)

const (
	maxSkillRoutingQueryRunes = 2400
	maxSkillRoutingTerms      = 384
)

// SanitizeRoutingQuery removes host-added control labels while retaining
// their user-supplied value. Control syntax must never become a search term.
func SanitizeRoutingQuery(query string) string {
	return skillRoutingControlLabelPattern.ReplaceAllString(query, "\n")
}

func boundSkillRoutingQuery(query string) string {
	runes := []rune(query)
	if len(runes) <= maxSkillRoutingQueryRunes {
		return query
	}
	const tailRunes = 800
	headRunes := maxSkillRoutingQueryRunes - tailRunes
	return string(runes[:headRunes]) + "\n" + string(runes[len(runes)-tailRunes:])
}

func IsRoutingDelimiter(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func tokenizeKeywords(question string) []string {
	stopWords := map[string]bool{
		"的": true, "了": true, "是": true, "在": true, "我": true,
		"有": true, "和": true, "就": true, "不": true, "人": true,
		"都": true, "一": true, "一个": true, "上": true, "也": true,
		"很": true, "到": true, "说": true, "要": true, "去": true,
		"你": true, "会": true, "着": true, "没有": true, "看": true,
		"好": true, "自己": true, "这": true, "他": true, "她": true,
		"它": true, "们": true, "那": true, "什么": true, "怎么": true,
		"如何": true, "为什么": true, "可以": true, "能": true, "需要": true,
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "can": true, "shall": true, "to": true,
		"of": true, "in": true, "for": true, "on": true, "with": true,
		"at": true, "by": true, "from": true, "as": true, "into": true,
		"through": true, "during": true, "before": true, "after": true,
		"above": true, "below": true, "between": true, "and": true,
		"but": true, "or": true, "nor": true, "not": true, "so": true,
		"yet": true, "both": true, "either": true, "neither": true,
		"each": true, "every": true, "all": true, "any": true, "few": true,
		"more": true, "most": true, "other": true, "some": true, "such": true,
		"only": true, "own": true, "same": true, "than": true, "too": true,
		"very": true, "just": true, "about": true, "also": true, "now": true,
		"then": true, "here": true, "there": true, "when": true, "where": true,
		"which": true, "who": true, "whom": true, "this": true, "that": true,
		"these": true, "those": true, "请": true, "帮助": true, "帮": true,
	}

	question = strings.ToLower(SanitizeRoutingQuery(question))
	words := strings.FieldsFunc(question, IsRoutingDelimiter)
	var splitWords []string
	for _, word := range words {
		splitWords = append(splitWords, splitCJKASCII(word)...)
	}

	seen := make(map[string]bool)
	var keywords []string
	for _, word := range splitWords {
		word = strings.TrimSpace(word)
		if len([]rune(word)) < 2 || stopWords[word] || seen[word] {
			continue
		}
		seen[word] = true
		keywords = append(keywords, word)
	}
	if len(keywords) == 0 && len(question) > 0 {
		keywords = append(keywords, question)
	}
	return keywords
}

func splitCJKASCII(value string) []string {
	var result []string
	var current []rune
	lastType := 0
	for _, character := range value {
		characterType := 2
		if character >= 0x4E00 && character <= 0x9FFF ||
			character >= 0x3400 && character <= 0x4DBF ||
			character >= 0xF900 && character <= 0xFAFF {
			characterType = 1
		}
		if characterType != lastType && len(current) > 0 {
			result = append(result, string(current))
			current = nil
		}
		current = append(current, character)
		lastType = characterType
	}
	if len(current) > 0 {
		result = append(result, string(current))
	}
	return result
}

func truncate(value string, maximum int) string {
	return textutil.Truncate(value, maximum)
}

func normalizeSkillRoutingText(text string) string {
	text = strings.ToLower(SanitizeRoutingQuery(text))
	parts := strings.FieldsFunc(text, IsRoutingDelimiter)
	return strings.Join(parts, " ")
}

func isHanRoutingTerm(term string) bool {
	runes := []rune(term)
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

func skillRoutingTermWeight(term string) int {
	length := len([]rune(term))
	if isHanRoutingTerm(term) {
		switch {
		case length >= 6:
			return 18
		case length == 5:
			return 14
		case length == 4:
			return 10
		case length == 3:
			return 6
		case length == 2:
			return 3
		default:
			return 0
		}
	}
	switch {
	case length >= 8:
		return 12
	case length >= 5:
		return 9
	case length >= 3:
		return 6
	case length == 2:
		return 3
	default:
		return 0
	}
}

func appendSkillRoutingTerms(terms *[]string, seen map[string]bool, text string) {
	if len(*terms) >= maxSkillRoutingTerms {
		return
	}
	normalized := normalizeSkillRoutingText(text)
	for _, field := range strings.Fields(normalized) {
		for _, segment := range splitCJKASCII(field) {
			if len(*terms) >= maxSkillRoutingTerms {
				return
			}
			segment = strings.TrimSpace(segment)
			if segment == "" {
				continue
			}
			if !isHanRoutingTerm(segment) {
				if skillRoutingTermWeight(segment) > 0 && !seen[segment] {
					seen[segment] = true
					*terms = append(*terms, segment)
				}
				continue
			}

			runes := []rune(segment)
			maxN := min(6, len(runes))
			for n := maxN; n >= 2; n-- {
				for start := 0; start+n <= len(runes); start++ {
					if len(*terms) >= maxSkillRoutingTerms {
						return
					}
					term := string(runes[start : start+n])
					if seen[term] {
						continue
					}
					seen[term] = true
					*terms = append(*terms, term)
				}
			}
		}
	}
}

func skillRoutingTerms(query string) []string {
	query = boundSkillRoutingQuery(SanitizeRoutingQuery(query))
	seen := make(map[string]bool)
	terms := make([]string, 0, 48)
	appendSkillRoutingTerms(&terms, seen, query)
	for _, keyword := range expandKeywords(extractIntentKeywords(query)) {
		appendSkillRoutingTerms(&terms, seen, keyword)
	}
	return terms
}

func skillAutoRoutingScore(key string, skill *Skill, terms []string) int {
	if skill == nil || len(terms) == 0 {
		return 0
	}

	nameParts := []string{key, skill.Metadata.Name}
	nameParts = append(nameParts, skill.Metadata.Aliases...)
	nameText := normalizeSkillRoutingText(strings.Join(nameParts, " "))
	tagText := normalizeSkillRoutingText(strings.Join(skill.Metadata.Tags, " "))
	readWhenText := normalizeSkillRoutingText(strings.Join(skill.Metadata.ReadWhen, " "))
	descriptionText := normalizeSkillRoutingText(skill.Metadata.Description)

	score := 0
	for _, term := range terms {
		weight := skillRoutingTermWeight(term)
		if weight == 0 {
			continue
		}
		if strings.Contains(nameText, term) {
			score += weight * 5
		}
		if strings.Contains(tagText, term) {
			score += weight * 4
		}
		if strings.Contains(readWhenText, term) {
			score += weight * 3
		}
		if strings.Contains(descriptionText, term) {
			score += weight * 2
		}
	}
	return score
}

// GetTopSkillPromptMatchesForQuery ranks automatic prompt injections and
// returns the matching prompt fields from the same locked catalog generation.
func (l *SkillsLoader) GetTopSkillPromptMatchesForQuery(query string, limit int) []SkillPromptMatch {
	if l == nil || limit <= 0 {
		return nil
	}
	terms := skillRoutingTerms(query)
	if len(terms) == 0 {
		return nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	type match struct {
		name  string
		score int
	}
	matches := make([]match, 0)
	for name, skill := range l.skills {
		if strings.EqualFold(strings.TrimSpace(name), "agent-reach") && !routing.AgentReachAutomaticIntent(query) {
			continue
		}
		if score := skillAutoRoutingScore(name, skill, terms); score > 0 {
			matches = append(matches, match{name: name, score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].name < matches[j].name
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	result := make([]SkillPromptMatch, 0, len(matches))
	for _, item := range matches {
		if skill := l.skills[item.name]; skill != nil {
			result = append(result, SkillPromptMatch{Name: item.name, Description: skill.Metadata.Description})
		}
	}
	return result
}

// GetTopSkillsForQuery keeps the name-only API for existing callers. Skill
// bodies are deliberately excluded from routing: metadata is sufficient.
func (l *SkillsLoader) GetTopSkillsForQuery(query string, limit int) []string {
	matches := l.GetTopSkillPromptMatchesForQuery(query, limit)
	result := make([]string, len(matches))
	for i, match := range matches {
		result[i] = match.Name
	}
	return result
}

// CalculateSkillMatchScore 计算技能匹配分数并返回内容片段
func (l *SkillsLoader) CalculateSkillMatchScore(skill *Skill, keywords []string) (int, []string) {
	score := 0
	snippets := make([]string, 0)

	nameLower := strings.ToLower(skill.Metadata.Name)
	descLower := strings.ToLower(skill.Metadata.Description)
	contentLower := strings.ToLower(skill.Content)
	pptMasterIntent := hasPPTMasterIntent(keywords)
	pptIntent := hasPPTIntent(keywords)
	wechatVideoAccountIntent := false
	for _, keyword := range keywords {
		if keyword == "wx-video-account-notes" {
			wechatVideoAccountIntent = true
			break
		}
	}

	if wechatVideoAccountIntent {
		switch nameLower {
		case "wx-video-account-notes":
			score += 1000
			snippets = append(snippets, "精确命中微信视频号分享链接，强制专用媒体提取流程优先")
		case "video-note":
			score += 40
			snippets = append(snippets, "保留通用视频笔记作为专用流程回退")
		}
	}

	if pptMasterIntent {
		switch nameLower {
		case "ppt-master":
			score += 1000
			snippets = append(snippets, "显式命中 generator: pptmaster，强制优先")
		case "base-ppt":
			score += 120
			snippets = append(snippets, "显式命中 generator: pptmaster，保留 base-ppt 作为回退")
		case "better-ppt", "better-ppt-generator":
			score -= 200
			snippets = append(snippets, "显式命中 generator: pptmaster，降低 better-ppt 优先级")
		}
	} else if pptIntent {
		switch nameLower {
		case "ppt-art-director":
			score += 240
			snippets = append(snippets, "普通 PPT 任务先由 ppt-art-director 自主选择生产路线")
		case "ppt-master":
			score -= 80
			snippets = append(snippets, "未明确要求高端商业或 PPTmaster，不自动强制完整上游流程")
		case "base-ppt":
			snippets = append(snippets, "base-ppt 仅作为快速中立、固定结构或失败降级路线")
		case "better-ppt", "better-ppt-generator":
			score -= 120
			snippets = append(snippets, "better-ppt 作为兼容别名保留")
		}
	}

	// read_when match (highest weight: 12)
	for _, rw := range skill.Metadata.ReadWhen {
		rwLower := strings.ToLower(rw)
		for _, kw := range keywords {
			if strings.Contains(rwLower, strings.ToLower(kw)) {
				score += 12
				snippets = append(snippets, fmt.Sprintf("read_when匹配「%s」", kw))
				break
			}
		}
	}

	// Tag match (weight: 10)
	for _, tag := range skill.Metadata.Tags {
		tagLower := strings.ToLower(tag)
		for _, kw := range keywords {
			if strings.Contains(tagLower, strings.ToLower(kw)) {
				score += 10
				snippets = append(snippets, fmt.Sprintf("tag匹配「%s」", kw))
				break
			}
		}
	}

	for _, kw := range keywords {
		kwLower := strings.ToLower(kw)

		// 名称匹配（最高权重）
		if strings.Contains(nameLower, kwLower) {
			score += 10
			if nameLower == kwLower {
				score += 5 // 完全匹配额外加分
			}
			snippets = append(snippets, fmt.Sprintf("名称包含「%s」", kw))
		}

		// 描述匹配
		if strings.Contains(descLower, kwLower) {
			score += 5
			// 提取描述片段
			if idx := strings.Index(descLower, kwLower); idx >= 0 {
				start := max(0, idx-30)
				end := min(len(skill.Metadata.Description), idx+len(kw)+30)
				snippet := skill.Metadata.Description[start:end]
				snippets = append(snippets, truncate(snippet, 80))
			}
		}

		// 内容匹配
		if strings.Contains(contentLower, kwLower) {
			count := strings.Count(contentLower, kwLower)
			score += min(count, 10) // 最多加10分

			// 提取第一个匹配位置的内容片段
			if idx := strings.Index(contentLower, kwLower); idx >= 0 && len(snippets) < 3 {
				start := max(0, idx-40)
				end := min(len(skill.Content), idx+len(kw)+40)
				snippet := skill.Content[start:end]
				// 清理换行
				snippet = strings.ReplaceAll(snippet, "\n", " ")
				snippets = append(snippets, truncate(snippet, 100))
			}
		}
	}

	// 去重片段
	seen := make(map[string]bool)
	uniqueSnippets := make([]string, 0)
	for _, s := range snippets {
		if !seen[s] {
			seen[s] = true
			uniqueSnippets = append(uniqueSnippets, s)
		}
	}

	if score < 0 {
		score = 0
	}

	return score, uniqueSnippets
}

// GetSkill 获取技能内容
func (l *SkillsLoader) GetSkill(name string) (*Skill, error) {
	skill, _, ok := l.ResolveSkill(name)
	if !ok {
		return nil, fmt.Errorf("技能 %s 不存在", name)
	}
	return skill, nil
}

// RecordInvokedSkill keeps the pre-context API on an explicit legacy scope.
// Runtime tool calls should use RecordInvokedSkillForContext so one task can
// never restore another task's skill instructions or workspace runtime path.
func (l *SkillsLoader) RecordInvokedSkill(name, content string) {
	l.RecordInvokedSkillForContext(context.Background(), name, content)
}

func (l *SkillsLoader) RecordInvokedSkillForContext(ctx context.Context, name, content string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	skill, resolvedName, ok := l.findSkillLocked(name)
	if !ok {
		return
	}
	if strings.TrimSpace(content) == "" {
		content = skill.Code
	}
	if len(content) > 12000 {
		content = content[:12000] + "\n[Skill content truncated]"
	}
	if l.invokedSkills == nil {
		l.invokedSkills = make(map[string]map[string]InvokedSkillInfo)
	}
	scope := invokedSkillScopeFromContext(ctx)
	if l.invokedSkills[scope] == nil {
		l.invokedSkills[scope] = make(map[string]InvokedSkillInfo)
	}
	l.invokedSkills[scope][resolvedName] = InvokedSkillInfo{
		Name:      resolvedName,
		Path:      skill.Path,
		Content:   content,
		InvokedAt: time.Now(),
	}
}

// BuildInvokedSkillsPrompt keeps the pre-context API on the legacy scope.
func (l *SkillsLoader) BuildInvokedSkillsPrompt(limit, maxCharsPerSkill int) string {
	return l.BuildInvokedSkillsPromptForContext(context.Background(), limit, maxCharsPerSkill)
}

func (l *SkillsLoader) BuildInvokedSkillsPromptForContext(ctx context.Context, limit, maxCharsPerSkill int) string {
	if limit <= 0 {
		limit = 5
	}
	if maxCharsPerSkill <= 0 {
		maxCharsPerSkill = 4000
	}
	l.mu.RLock()
	scoped := l.invokedSkills[invokedSkillScopeFromContext(ctx)]
	items := make([]InvokedSkillInfo, 0, len(scoped))
	for _, item := range scoped {
		items = append(items, item)
	}
	l.mu.RUnlock()
	if len(items) == 0 {
		return ""
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].InvokedAt.After(items[j].InvokedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	var b strings.Builder
	b.WriteString("[INVOKED SKILLS]\nThese skills were already loaded through use_skill. Continue following their instructions after compaction; do not call use_skill again only to reload them.\n")
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if len(content) > maxCharsPerSkill {
			content = content[:maxCharsPerSkill] + "\n[truncated]"
		}
		b.WriteString(fmt.Sprintf("\n## %s\nSource: %s\n%s\n", item.Name, item.Path, content))
	}
	return b.String()
}

// GetAlwaysSkills 获取始终加载的技能
func (l *SkillsLoader) GetAlwaysSkills() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	always := make([]string, 0)
	for name, skill := range l.skills {
		if skill.Metadata.Always {
			always = append(always, name)
		}
	}
	return always
}

type SkillExecutor interface {
	Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error)
}

type SkillType string

const (
	SkillTypePython  SkillType = "python"
	SkillTypeNode    SkillType = "node"
	SkillTypeJS      SkillType = "js"
	SkillTypeShell   SkillType = "shell"
	SkillTypeHTTP    SkillType = "http"
	SkillTypeBuiltin SkillType = "builtin"
)

func NormalizeSkillType(raw string) SkillType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "builtin", "skill", "markdown", "md", "doc", "docs":
		return SkillTypeBuiltin
	case "python", "py":
		return SkillTypePython
	case "node", "nodejs":
		return SkillTypeNode
	case "js", "javascript":
		return SkillTypeJS
	case "shell", "bash", "sh":
		return SkillTypeShell
	case "http", "api":
		return SkillTypeHTTP
	default:
		return SkillType(raw)
	}
}

type PythonSkillExecutor struct{}

func (e *PythonSkillExecutor) Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error) {
	pythonBin := findFirstExecutable("python", "python3")
	if pythonBin == "" {
		return "", fmt.Errorf("python executable not found")
	}
	if entry := resolveRuntimeEntrypoint(skill, ".py"); entry != "" {
		if err := ensurePythonDeps(ctx, pythonBin, skill.RuntimeDir); err != nil {
			return "", err
		}
		cmd := exec.CommandContext(ctx, pythonBin, entry)
		cmd.Dir = skill.RuntimeDir
		cmd.Env = buildSkillEnv(params, skill.RuntimeDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), fmt.Errorf("执行Python脚本失败: %v", err)
		}
		return string(output), nil
	}

	// 创建临时Python文件
	tempDir := filepath.Join(os.TempDir(), "agent_skills")
	os.MkdirAll(tempDir, 0755)

	scriptPath := filepath.Join(tempDir, fmt.Sprintf("%s_%d.py",
		skill.Metadata.Name, time.Now().UnixNano()))

	// 生成Python脚本
	scriptContent := e.generateScript(skill, params)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		return "", fmt.Errorf("写入临时脚本失败: %v", err)
	}
	defer os.Remove(scriptPath)

	// 执行Python脚本
	cmd := exec.CommandContext(ctx, pythonBin, scriptPath)

	// 设置参数为环境变量
	cmd.Env = buildSkillEnv(params, skill.RuntimeDir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("执行Python脚本失败: %v", err)
	}

	return string(output), nil
}

func (e *PythonSkillExecutor) generateScript(skill *Skill, params map[string]interface{}) string {
	var script strings.Builder

	script.WriteString("import sys\n")
	script.WriteString("import json\n")
	script.WriteString("import os\n\n")

	// 添加参数读取代码
	script.WriteString("# 读取参数\n")
	for _, param := range skill.Metadata.Parameters {
		script.WriteString(fmt.Sprintf("%s = os.environ.get('PARAM_%s', %v)\n",
			param.Name,
			strings.ToUpper(param.Name),
			jsonEncode(param.Default)))
	}
	script.WriteString("\n")

	// 添加技能代码
	script.WriteString("# 技能代码\n")
	script.WriteString(skill.Code)
	script.WriteString("\n\n")

	// 添加执行入口
	if skill.Metadata.Entrypoint != "" {
		script.WriteString(fmt.Sprintf("\n# 执行入口\n"))
		script.WriteString(fmt.Sprintf("if __name__ == '__main__':\n"))
		script.WriteString(fmt.Sprintf("    result = %s()\n", skill.Metadata.Entrypoint))
		script.WriteString(fmt.Sprintf("    print(result)\n"))
	}

	return script.String()
}

type NodeSkillExecutor struct{}

func (e *NodeSkillExecutor) Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error) {
	nodeBin := findFirstExecutable("node")
	if nodeBin == "" {
		return "", fmt.Errorf("node executable not found")
	}
	if err := ensureNodeDeps(ctx, skill.RuntimeDir); err != nil {
		return "", err
	}

	entry := resolveRuntimeEntrypoint(skill, ".js")
	tempFile := ""
	if entry == "" {
		tempDir := filepath.Join(os.TempDir(), "agent_skills")
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			return "", err
		}
		tempFile = filepath.Join(tempDir, fmt.Sprintf("%s_%d.js", SafeFilename(skill.Metadata.Name), time.Now().UnixNano()))
		if err := os.WriteFile(tempFile, []byte(skill.Code), 0644); err != nil {
			return "", fmt.Errorf("写入临时Node脚本失败: %v", err)
		}
		defer os.Remove(tempFile)
		entry = tempFile
	}

	cmd := exec.CommandContext(ctx, nodeBin, entry)
	cmd.Dir = skill.RuntimeDir
	cmd.Env = buildSkillEnv(params, skill.RuntimeDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("执行Node脚本失败: %v", err)
	}
	return string(output), nil
}

func resolveRuntimeEntrypoint(skill *Skill, defaultExt string) string {
	if skill == nil || skill.RuntimeDir == "" {
		return ""
	}
	entry := strings.TrimSpace(skill.Metadata.Entrypoint)
	if entry == "" {
		return ""
	}
	isPathLike := strings.Contains(entry, "/") || strings.Contains(entry, "\\") || strings.HasSuffix(strings.ToLower(entry), defaultExt)
	if !isPathLike {
		return ""
	}
	entry = filepath.Clean(filepath.FromSlash(entry))
	if filepath.IsAbs(entry) {
		if _, err := os.Stat(entry); err == nil {
			return entry
		}
		return ""
	}
	candidate := filepath.Join(skill.RuntimeDir, entry)
	if err := EnsurePathWithin(skill.RuntimeDir, candidate); err != nil {
		return ""
	}
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func buildSkillEnv(params map[string]interface{}, runtimeDir string) []string {
	env := os.Environ()
	payload, _ := json.Marshal(params)
	env = append(env, "SKILL_PARAMS_JSON="+string(payload))
	if runtimeDir != "" {
		env = append(env, "SKILL_RUNTIME_DIR="+runtimeDir)
		pythonPath := runtimeDir + string(os.PathListSeparator) + filepath.Join(runtimeDir, "scripts")
		if existing := os.Getenv("PYTHONPATH"); existing != "" {
			pythonPath += string(os.PathListSeparator) + existing
		}
		env = append(env, "PYTHONPATH="+pythonPath)
	}
	for k, v := range params {
		env = append(env, fmt.Sprintf("PARAM_%s=%v", strings.ToUpper(k), v))
	}
	return env
}

func findFirstExecutable(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func ensurePythonDeps(ctx context.Context, pythonBin, runtimeDir string) error {
	if runtimeDir == "" {
		return nil
	}
	req := filepath.Join(runtimeDir, "requirements.txt")
	reqInfo, err := os.Stat(req)
	if err != nil {
		return nil
	}
	marker := filepath.Join(runtimeDir, ".openkitty-pip-installed")
	if markerInfo, err := os.Stat(marker); err == nil && markerInfo.ModTime().After(reqInfo.ModTime()) {
		return nil
	}
	cmd := exec.CommandContext(ctx, pythonBin, "-m", "pip", "install", "-r", req)
	cmd.Dir = runtimeDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装Python依赖失败: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0644)
	return nil
}

func ensureNodeDeps(ctx context.Context, runtimeDir string) error {
	if runtimeDir == "" {
		return nil
	}
	pkg := filepath.Join(runtimeDir, "package.json")
	if _, err := os.Stat(pkg); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "node_modules")); err == nil {
		return nil
	}
	npmBin := findFirstExecutable("npm", "npm.cmd")
	if npmBin == "" {
		return fmt.Errorf("package.json exists but npm executable not found")
	}
	cmd := exec.CommandContext(ctx, npmBin, "install", "--omit=dev")
	cmd.Dir = runtimeDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.CommandContext(ctx, npmBin, "install")
		cmd.Dir = runtimeDir
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("安装Node依赖失败: %v\n%s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

type ShellSkillExecutor struct{}

func (e *ShellSkillExecutor) Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error) {
	// 创建临时目录
	tempDir := filepath.Join(os.TempDir(), "agent_skills", skill.Metadata.Name)
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	// 生成脚本文件
	scriptPath := filepath.Join(tempDir, "script.sh")

	// 将参数注入为环境变量
	env := os.Environ()
	for k, v := range params {
		env = append(env, fmt.Sprintf("%s=%v", strings.ToUpper(k), v))
	}

	// 写入脚本
	if err := os.WriteFile(scriptPath, []byte(skill.Code), 0755); err != nil {
		return "", fmt.Errorf("写入脚本失败: %v", err)
	}

	// 执行脚本
	cmd := exec.CommandContext(ctx, "/bin/bash", scriptPath)
	cmd.Env = env
	cmd.Dir = tempDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("执行脚本失败: %v", err)
	}

	return string(output), nil
}

// HTTPSkillExecutor HTTP API调用执行器
type HTTPSkillExecutor struct{}

func (e *HTTPSkillExecutor) Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error) {
	// 从技能代码中解析HTTP配置
	var config struct {
		Method  string                 `json:"method"`
		URL     string                 `json:"url"`
		Headers map[string]string      `json:"headers"`
		Body    map[string]interface{} `json:"body"`
	}

	if err := json.Unmarshal([]byte(skill.Code), &config); err != nil {
		return "", fmt.Errorf("解析HTTP配置失败: %v", err)
	}

	// 替换URL中的参数
	url := config.URL
	for k, v := range params {
		url = strings.ReplaceAll(url, "{"+k+"}", fmt.Sprintf("%v", v))
	}

	// 构建请求体
	var bodyBytes []byte
	if config.Body != nil {
		// 合并参数到body
		mergedBody := make(map[string]interface{})
		for k, v := range config.Body {
			mergedBody[k] = v
		}
		for k, v := range params {
			mergedBody[k] = v
		}

		bodyBytes, _ = json.Marshal(mergedBody)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, config.Method, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置headers
	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	// 执行请求
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	// 构建结果
	result := fmt.Sprintf("状态码: %d\n\n", resp.StatusCode)
	result += string(body)

	return result, nil
}

func jsonEncode(v interface{}) string {
	if v == nil {
		return "None"
	}
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("'%s'", val)
	case int, int64, float64:
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("'%v'", val)
	}
}

type BuiltinSkillExecutor struct {
	runtimeScope func(context.Context) (workspaceScoped bool, workspace string)
}

func (e *BuiltinSkillExecutor) Execute(ctx context.Context, skill *Skill, params map[string]interface{}) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("技能: %s\n", skill.Metadata.Name))
	result.WriteString(fmt.Sprintf("描述: %s\n\n", PromptSafeDescription(skill.Metadata.Description)))
	isBuiltin := skill.Embedded || strings.EqualFold(strings.TrimSpace(skill.Source), "builtin")
	if isBuiltin {
		result.WriteString("来源: builtin（内存）\n")
		if skill.RuntimeDir != "" {
			result.WriteString("本次 use_skill 已返回主指令；主指令点名的 references、scripts 或 assets 仍须按本轮任务按需读取或执行。\n")
		} else {
			result.WriteString("本次 use_skill 已返回完整的内存指令。\n")
		}
		result.WriteString("不要再读取运行时目录中的 SKILL.md，也不要用 cli/ptc_execute 重复提取它。\n")
	} else {
		source := strings.TrimSpace(skill.Source)
		if source == "" {
			source = "user"
		}
		result.WriteString(fmt.Sprintf("来源: %s（用户目录/磁盘）\n", source))
		if strings.TrimSpace(skill.Path) != "" {
			result.WriteString(fmt.Sprintf("Skill 文件: %s\n", skill.Path))
		}
	}
	if isBuiltin && skill.RuntimeDir != "" {
		runtimeDir := skill.RuntimeDir
		workspaceRuntime, workspace := false, ""
		if e.runtimeScope != nil {
			workspaceRuntime, workspace = e.runtimeScope(ctx)
		}
		if workspaceRuntime {
			workspace = strings.TrimSpace(workspace)
			if workspace == "" {
				workspace = projectctx.ResolvedWorkspace(ctx)
			}
			if workspace != "" {
				if rel, err := filepath.Rel(workspace, runtimeDir); err == nil &&
					rel != "." && !filepath.IsAbs(rel) &&
					rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					runtimeDir = "." + string(filepath.Separator) + rel
				}
			}
		}
		result.WriteString(fmt.Sprintf("运行时目录: %s\n", runtimeDir))
		result.WriteString("脚本、引用资料和 package/requirements 文件已按需释放到运行时目录；执行命令时优先使用该目录下的相对路径。\n")
		if workspaceRuntime {
			result.WriteString("路径规则: 该运行时目录属于当前用户工作区，可直接把返回的相对路径用于 cli 命令；不要探测、展开或披露宿主机绝对路径，也不要改写其中的官方 scripts。正文中的 legacy `skills/...` 仅表示该运行时目录内的相对位置。\n")
		} else {
			result.WriteString("路径规则: 本内置技能正文中的 legacy `skills/...` 路径只表示运行时目录内的相对位置；不要探测 workspace `skills/` 或 `.openkitty/skills/`。使用 ptc_execute 时，其 cwd 默认是 workspace，需用运行时目录的绝对路径或在 subprocess.run 中设置 cwd=运行时目录。\n")
		}
		result.WriteString("这是生成的运行时支持副本，不是 builtin 指令源；只按需读取 references、scripts、assets 等被正文点名的支持文件。该副本可安全清理，后续调用会按需重新生成。\n")
	} else if !isBuiltin && skill.RuntimeDir != "" {
		result.WriteString(fmt.Sprintf("用户 Skill 目录: %s\n", skill.RuntimeDir))
	}
	result.WriteString("\n")
	result.WriteString("使用说明:\n")
	result.WriteString(skill.Code)

	return result.String(), nil
}

// SkillIndex — built-in skill index loaded from skills/OPENKITTY_INDEX.json
type SkillIndex struct {
	Categories map[string]SkillCategory `json:"categories"`
}
type SkillCategory struct {
	Desc   string   `json:"desc"`
	Skills []string `json:"skills"`
}

func (l *SkillsLoader) LoadSkillIndex() (*SkillIndex, error) {
	l.mu.RLock()
	if l.skillIndex != nil {
		defer l.mu.RUnlock()
		return l.skillIndex, nil
	}
	l.mu.RUnlock()

	indexCandidates := []string{
		filepath.Join(l.workspace, "builtin", "OPENKITTY_INDEX.json"),
		filepath.Join(l.workspace, "skills", "OPENKITTY_INDEX.json"),
	}
	var (
		data []byte
		err  error
	)
	for _, indexPath := range indexCandidates {
		data, err = os.ReadFile(indexPath)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("读取 OPENKITTY_INDEX.json 失败: %w", err)
	}
	idx, err := parseSkillIndex(data)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.skillIndex = idx
	l.cacheBuiltinIndexLocked(data)
	l.mu.Unlock()
	return idx, nil
}

func (l *SkillsLoader) LoadSkillIndexFromFS(fsys fs.FS, indexPath string) (*SkillIndex, error) {
	if fsys == nil || indexPath == "" {
		return nil, fmt.Errorf("embedded skill index path is empty")
	}

	l.mu.RLock()
	if l.skillIndex != nil {
		defer l.mu.RUnlock()
		return l.skillIndex, nil
	}
	l.mu.RUnlock()

	data, err := fs.ReadFile(fsys, filepath.ToSlash(indexPath))
	if err != nil {
		return nil, fmt.Errorf("读取 embedded OPENKITTY_INDEX.json 失败: %w", err)
	}
	idx, err := parseSkillIndex(data)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.skillIndex = idx
	l.cacheBuiltinIndexLocked(data)
	l.mu.Unlock()
	return idx, nil
}

func parseSkillIndex(data []byte) (*SkillIndex, error) {
	var wrapper struct {
		Categories map[string]SkillCategory `json:"categories"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("解析 OPENKITTY_INDEX.json 失败: %w", err)
	}
	if len(wrapper.Categories) > 0 {
		return &SkillIndex{Categories: wrapper.Categories}, nil
	}

	var flat map[string]SkillCategory
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, fmt.Errorf("解析 OPENKITTY_INDEX.json 失败: %w", err)
	}
	return &SkillIndex{Categories: flat}, nil
}

func (l *SkillsLoader) IsInIndex(name string) bool {
	if l.skillIndex == nil {
		return false
	}
	for _, cat := range l.skillIndex.Categories {
		for _, s := range cat.Skills {
			if s == name {
				return true
			}
		}
	}
	return false
}

func (l *SkillsLoader) BuildIndexPrompt() string {
	l.mu.RLock()
	if l.indexPrompt != "" {
		defer l.mu.RUnlock()
		return l.indexPrompt
	}
	if l.skillIndex == nil {
		l.mu.RUnlock()
		return ""
	}
	var b strings.Builder
	b.WriteString("## Built-in Skills\n")
	b.WriteString("Built-in skills are preloaded into runtime context. Prefer using them directly. If detailed instructions are needed, call use_skill; do not read or probe local SKILL.md paths.\n")
	// Deterministic order
	keys := make([]string, 0, len(l.skillIndex.Categories))
	for k := range l.skillIndex.Categories {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, catName := range keys {
		cat := l.skillIndex.Categories[catName]
		b.WriteString(fmt.Sprintf("  %s — %s\n    %s\n", catName, cat.Desc, strings.Join(cat.Skills, ", ")))
	}
	b.WriteString("\n## User Skills\n  Created skills are discoverable via list_skills.\n")
	b.WriteString("\nOnly read external skill files when a custom user skill is missing from context or must be inspected in detail. Never inspect `.openkitty/skills` for built-in skills.\n")
	result := b.String()
	l.mu.RUnlock()
	l.mu.Lock()
	l.indexPrompt = result
	l.mu.Unlock()
	return result
}
