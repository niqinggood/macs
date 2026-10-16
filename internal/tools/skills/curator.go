package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
)

var skillMetricsMu sync.Mutex

// 借鉴hermes Curator: 技能自动评分、去重、裁剪

// SkillMetric 追踪单个技能的使用指标
type SkillMetric struct {
	Name        string    `json:"name"`
	LoadCount   int       `json:"load_count"`  // 被加载次数
	UseCount    int       `json:"use_count"`   // 实际被调用次数
	FailCount   int       `json:"fail_count"`  // 失败次数
	LastUsed    time.Time `json:"last_used"`   // 最后使用时间
	LastLoaded  time.Time `json:"last_loaded"` // 最后加载时间
	CreatedAt   time.Time `json:"created_at"`  // 创建时间
	Score       float64   `json:"score"`       // 综合评分(0-100)
	Description string    `json:"description"` // 快照描述
	Tags        []string  `json:"tags"`        // 快照标签
}

// SkillMetricsDB 技能指标数据库
type SkillMetricsDB struct {
	Skills  map[string]*SkillMetric `json:"skills"`
	Version int                     `json:"version"`
}

// CurateSkillsTool 技能策展工具 — 借鉴hermes Curator
type CurateSkillsTool struct {
	skillsDir   string
	metricsPath string
}

func NewCurateSkillsTool(skillsDir string) *CurateSkillsTool {
	return &CurateSkillsTool{
		skillsDir:   skillsDir,
		metricsPath: filepath.Join(skillsDir, ".skill_metrics.json"),
	}
}

// NewCurateSkillsToolWithMetricsPath supports hosts that keep the metrics
// database outside the skill directory.
func NewCurateSkillsToolWithMetricsPath(skillsDir, metricsPath string) *CurateSkillsTool {
	return &CurateSkillsTool{skillsDir: skillsDir, metricsPath: metricsPath}
}

func (t *CurateSkillsTool) Name() string       { return "curate_skills" }
func (t *CurateSkillsTool) MaxResultSize() int { return 8000 }

func (t *CurateSkillsTool) Description() string {
	return `Review, score, and prune the skill library. Returns a report with:
- Quality scores for each skill (0-100 based on usage, recency, completeness)
- Duplicate detection (skills with similar names or descriptions)
- Staleness warnings (skills unused for 30+ days)
- Improvement suggestions (skills with vague descriptions)
- Auto-archive option for stale skills (set action="auto_prune")

Use this periodically or when the skill library feels cluttered.`
}

func (t *CurateSkillsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "review (default): show report only. auto_prune: also archive skills with score < 20 and unused for 60+ days.",
				"enum":        []string{"review", "auto_prune"},
			},
		},
		"required": []string{},
	}
}

func (t *CurateSkillsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	skillMetricsMu.Lock()
	defer skillMetricsMu.Unlock()

	action, _ := params["action"].(string)
	if action == "" {
		action = "review"
	}

	metricsDB := t.loadMetrics()
	skills, err := t.scanSkills()
	if err != nil {
		return "", fmt.Errorf("scan skills: %w", err)
	}

	// Score each skill
	for _, skill := range skills {
		name := filepath.Base(skill)
		if _, exists := metricsDB.Skills[name]; !exists {
			metricsDB.Skills[name] = &SkillMetric{
				Name:      name,
				CreatedAt: time.Now(),
			}
		}
		m := metricsDB.Skills[name]
		t.scoreSkill(m)
	}

	// Find duplicates
	dupes := t.findDuplicates(skills)

	// Build report
	var report strings.Builder
	report.WriteString("## Skill Curator Report\n\n")

	// Summary
	total := len(skills)
	highQuality, lowQuality, stale := 0, 0, 0
	for _, m := range metricsDB.Skills {
		if m.Score >= 70 {
			highQuality++
		}
		if m.Score < 30 {
			lowQuality++
		}
		if time.Since(m.LastUsed) > 30*24*time.Hour && m.UseCount > 0 {
			stale++
		}
	}
	report.WriteString(fmt.Sprintf("**Total:** %d | ⭐ High(≥70): %d | ⚠️ Low(<30): %d | 🕐 Stale(>30d): %d\n\n",
		total, highQuality, lowQuality, stale))

	// Top skills
	report.WriteString("### Top Skills\n")
	sorted := t.sortedMetrics(metricsDB)
	for i, m := range sorted {
		if i >= 5 {
			break
		}
		report.WriteString(fmt.Sprintf("- ⭐ **%s** (%.0f) — used %dx, last: %s\n",
			m.Name, m.Score, m.UseCount, m.LastUsed.Format("01-02")))
	}

	// Low quality
	if lowQuality > 0 {
		report.WriteString("\n### ⚠️ Low Quality (score < 30)\n")
		for _, m := range sorted {
			if m.Score < 30 {
				reason := ""
				if m.UseCount == 0 {
					reason = "从未使用"
				} else if m.FailCount > m.UseCount/2 {
					reason = "高失败率"
				} else {
					reason = "长期未维护"
				}
				report.WriteString(fmt.Sprintf("- **%s** (%.0f) — %s | %s\n", m.Name, m.Score, reason, m.Description))
			}
		}
	}

	// Duplicates
	if len(dupes) > 0 {
		report.WriteString("\n### 🔄 Potential Duplicates\n")
		for _, d := range dupes {
			report.WriteString(fmt.Sprintf("- **%s** ↔ **%s** — 建议合并\n", d[0], d[1]))
		}
	}

	// Staleness
	if stale > 0 {
		report.WriteString("\n### 🕐 Stale (>30d unused)\n")
		for _, m := range sorted {
			if time.Since(m.LastUsed) > 30*24*time.Hour && m.UseCount > 0 {
				report.WriteString(fmt.Sprintf("- **%s** — 最后使用: %s\n",
					m.Name, m.LastUsed.Format("2006-01-02")))
			}
		}
	}

	// Action: auto prune
	if action == "auto_prune" {
		report.WriteString("\n### 🔧 Auto-Prune Results\n")
		pruned := 0
		now := time.Now()
		for _, m := range sorted {
			if EligibleForSkillAutoPrune(m, now) {
				skillPath := filepath.Join(t.skillsDir, m.Name)
				archivePath := filepath.Join(t.skillsDir, ".archived", m.Name)
				os.MkdirAll(filepath.Join(t.skillsDir, ".archived"), 0755)
				if err := os.Rename(skillPath, archivePath); err == nil {
					report.WriteString(fmt.Sprintf("- 已归档: **%s** → .archived/\n", m.Name))
					pruned++
				} else {
					report.WriteString(fmt.Sprintf("- 归档失败: **%s** (%v)\n", m.Name, err))
				}
			}
		}
		if pruned == 0 {
			report.WriteString("无符合自动裁剪条件的技能。\n")
		}
		report.WriteString(fmt.Sprintf("\n裁剪 %d 个技能。\n", pruned))
	}

	// Save metrics
	t.saveMetrics(metricsDB)

	log.Printf("[Curator] Report: %d skills, high=%d low=%d stale=%d dupes=%d",
		total, highQuality, lowQuality, stale, len(dupes))

	return report.String(), nil
}

func (t *CurateSkillsTool) loadMetrics() *SkillMetricsDB {
	db := &SkillMetricsDB{Skills: make(map[string]*SkillMetric), Version: 1}
	data, err := os.ReadFile(t.metricsPath)
	if err != nil {
		return db
	}
	json.Unmarshal(data, db)
	if db.Skills == nil {
		db.Skills = make(map[string]*SkillMetric)
	}
	return db
}

// LoadMetrics reads the current metrics database.
func (t *CurateSkillsTool) LoadMetrics() *SkillMetricsDB {
	return t.loadMetrics()
}

func (t *CurateSkillsTool) saveMetrics(db *SkillMetricsDB) {
	data, _ := json.MarshalIndent(db, "", "  ")
	if err := os.MkdirAll(filepath.Dir(t.metricsPath), 0755); err != nil {
		log.Printf("[Curator] create metrics directory: %v", err)
		return
	}
	if err := fsutil.WriteAtomic(t.metricsPath, data, 0644); err != nil {
		log.Printf("[Curator] save metrics: %v", err)
	}
}

// SaveMetrics persists the supplied metrics database.
func (t *CurateSkillsTool) SaveMetrics(db *SkillMetricsDB) {
	t.saveMetrics(db)
}

// eligibleForSkillAutoPrune deliberately requires evidence of a real skill
// execution. A zero LastUsed value used to look centuries old to time.Since,
// which allowed a newly discovered skill to be archived on its first review.
// LastLoaded is considered activity, but loading alone is never sufficient to
// make a skill destructive-auto-prune eligible.
func EligibleForSkillAutoPrune(m *SkillMetric, now time.Time) bool {
	if m == nil || m.Score >= 20 || m.UseCount <= 0 || m.CreatedAt.IsZero() || m.LastUsed.IsZero() {
		return false
	}
	if now.Before(m.CreatedAt) || now.Before(m.LastUsed) {
		return false
	}
	lastActivity := m.LastUsed
	if m.LastLoaded.After(lastActivity) {
		lastActivity = m.LastLoaded
	}
	const minimumAge = 60 * 24 * time.Hour
	return now.Sub(m.CreatedAt) >= minimumAge && now.Sub(lastActivity) >= minimumAge
}

func (t *CurateSkillsTool) scanSkills() ([]string, error) {
	entries, err := os.ReadDir(t.skillsDir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		skillFile := filepath.Join(t.skillsDir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs, nil
}

// scoreSkill 计算技能质量分(0-100): 使用频率40分 + 成功率30分 + 新鲜度20分 + 文档完整度10分
func (t *CurateSkillsTool) scoreSkill(m *SkillMetric) {
	score := 0.0

	// 使用频率 (40分): 每次使用 +5, 上限40
	score += minF(40, float64(m.UseCount)*5)

	// 成功率 (30分): fail/use ratio
	if m.UseCount > 0 {
		successRate := 1.0 - float64(m.FailCount)/float64(m.UseCount)
		score += successRate * 30
	} else if m.LoadCount > 0 {
		score += 15 // 被加载但未使用，给基础分
	}

	// 新鲜度 (20分): 最近7天满分，30天衰减到0
	if !m.LastUsed.IsZero() {
		daysSince := time.Since(m.LastUsed).Hours() / 24
		if daysSince < 7 {
			score += 20
		} else if daysSince < 30 {
			score += 20 * (1 - (daysSince-7)/23)
		}
	} else if !m.LastLoaded.IsZero() {
		score += 5 // 加载过但未使用
	}

	// 文档完整度 (10分): 有描述+5, 有标签+5
	if m.Description != "" && len(m.Description) > 20 {
		score += 5
	}
	if len(m.Tags) > 0 {
		score += 5
	}

	m.Score = score
}

// findDuplicates 检测相似技能名
func (t *CurateSkillsTool) findDuplicates(skills []string) [][]string {
	var dupes [][]string
	seen := make(map[string][]string)
	for _, name := range skills {
		normalized := strings.ToLower(strings.ReplaceAll(name, "-", ""))
		normalized = strings.ReplaceAll(normalized, "_", "")
		seen[normalized] = append(seen[normalized], name)
	}
	// 前缀匹配
	for i := 0; i < len(skills); i++ {
		for j := i + 1; j < len(skills); j++ {
			a, b := skills[i], skills[j]
			if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) ||
				strings.Contains(a, b) || strings.Contains(b, a) {
				dupes = append(dupes, []string{a, b})
			}
		}
	}
	return dupes
}

func (t *CurateSkillsTool) sortedMetrics(db *SkillMetricsDB) []*SkillMetric {
	var list []*SkillMetric
	for _, m := range db.Skills {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Score > list[j].Score })
	return list
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// RecordSkillUse 记录技能使用（由skills loader调用）
func RecordSkillUse(metricsPath, skillName string, success bool) {
	skillMetricsMu.Lock()
	defer skillMetricsMu.Unlock()

	tool := &CurateSkillsTool{metricsPath: metricsPath}
	db := tool.loadMetrics()
	if m, ok := db.Skills[skillName]; ok {
		m.UseCount++
		if !success {
			m.FailCount++
		}
		m.LastUsed = time.Now()
	} else {
		db.Skills[skillName] = &SkillMetric{
			Name:      skillName,
			UseCount:  1,
			LastUsed:  time.Now(),
			CreatedAt: time.Now(),
		}
		if !success {
			db.Skills[skillName].FailCount = 1
		}
	}
	tool.saveMetrics(db)
}

// RecordSkillLoad 记录技能加载
func RecordSkillLoad(metricsPath, skillName string) {
	skillMetricsMu.Lock()
	defer skillMetricsMu.Unlock()

	tool := &CurateSkillsTool{metricsPath: metricsPath}
	db := tool.loadMetrics()
	if m, ok := db.Skills[skillName]; ok {
		m.LoadCount++
		m.LastLoaded = time.Now()
	} else {
		db.Skills[skillName] = &SkillMetric{
			Name:       skillName,
			LoadCount:  1,
			LastLoaded: time.Now(),
			CreatedAt:  time.Now(),
		}
	}
	tool.saveMetrics(db)
}
