package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type teamTemplateDTO struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Mode            string     `json:"mode"`
	Builtin         bool       `json:"builtin"`
	Roles           []TeamRole `json:"roles"`
	RoleCountLocked bool       `json:"role_count_locked"`
}

var teamTemplatePresentation = map[string][2]string{
	"research":   {"调研分析团队", "搜集资料、分析事实并整理报告"},
	"coding":     {"代码开发团队", "设计方案、编写代码并审核质量"},
	"fullstack":  {"全栈开发团队", "产品规划后并行开发，再进行测试"},
	"content":    {"内容创作团队", "策划内容、撰写文案并审核表达"},
	"ppt":        {"PPT 演示团队", "艺术指导、内容策划、设计排版和视觉审核"},
	"codebase":   {"代码审计团队", "扫描项目、分析文件并审核架构"},
	"contract":   {"合同审查团队", "梳理结构、分析条款并核对合规风险"},
	"competitor": {"竞对分析团队", "扫描市场格局、并行分析竞对并提出策略"},
}

func teamTemplateForConfig(config *Config, id string) teamTemplateDTO {
	roles, builtin := defaultTeamTemplates[id]
	if config != nil && len(config.TeamTemplates[id]) > 0 {
		roles = config.TeamTemplates[id]
	}
	name, description := id, "自定义项目专家团队"
	if label, ok := teamTemplatePresentation[id]; ok {
		name, description = label[0], label[1]
	}
	_, locked := hybridTemplateStages[id]
	return teamTemplateDTO{ID: id, Name: name, Description: description, Mode: teamTemplateMode(id),
		Builtin: builtin, Roles: append([]TeamRole(nil), roles...), RoleCountLocked: locked}
}

func teamTemplatesForConfig(config *Config) []teamTemplateDTO {
	ids := []string{"research", "coding", "fullstack", "content", "ppt", "codebase", "contract", "competitor"}
	var custom []string
	if config != nil {
		for id, roles := range config.TeamTemplates {
			if _, builtin := defaultTeamTemplates[id]; !builtin && len(roles) > 0 && id != "auto" {
				custom = append(custom, id)
			}
		}
	}
	sort.Strings(custom)
	ids = append(ids, custom...)
	teams := make([]teamTemplateDTO, 0, len(ids))
	for _, id := range ids {
		teams = append(teams, teamTemplateForConfig(config, id))
	}
	return teams
}

type teamTemplateRoleInput struct {
	Role         string  `json:"role"`
	SystemPrompt string  `json:"system_prompt"`
	Skill        string  `json:"skill,omitempty"`
	Model        *string `json:"model,omitempty"`
}

type teamTemplateUpdate struct {
	ID       string                  `json:"id"`
	Roles    []teamTemplateRoleInput `json:"roles"`
	Revision string                  `json:"config_revision"`
}

func safeTeamName(value string, max int) bool {
	if value == "" || utf8.RuneCountInString(value) > max || value == "." || value == ".." ||
		strings.ContainsAny(value, `/\<>:"|?*`) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func safeTeamSkill(value string) bool {
	if value == "" {
		return true
	}
	if !safeTeamName(value, 128) || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, char := range value {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func validateTeamTemplateUpdate(config *Config, input teamTemplateUpdate) (string, []TeamRole, error) {
	id := strings.TrimSpace(input.ID)
	if !safeTeamName(id, 80) || strings.EqualFold(id, "auto") {
		return "", nil, errors.New("团队名称需为 1–80 个字符，不能包含路径字符或使用 auto")
	}
	if len(input.Roles) == 0 || len(input.Roles) > 16 {
		return "", nil, errors.New("团队需包含 1–16 个岗位")
	}
	if stages, locked := hybridTemplateStages[id]; locked {
		if len(input.Roles) != len(defaultTeamTemplates[id]) {
			return "", nil, fmt.Errorf("该团队的固定流程需保留 %d 个岗位及其顺序", len(defaultTeamTemplates[id]))
		}
		if err := validateTeamStages(make([]TeamRole, len(input.Roles)), stages); err != nil {
			return "", nil, errors.New("团队岗位与固定流程不匹配")
		}
	}
	previous := teamTemplateForConfig(config, id).Roles
	roles := make([]TeamRole, 0, len(input.Roles))
	seen := make(map[string]bool)
	total := 0
	for _, item := range input.Roles {
		role, prompt, skill := strings.TrimSpace(item.Role), strings.TrimSpace(item.SystemPrompt), strings.TrimSpace(item.Skill)
		if !safeTeamName(role, 80) || seen[role] {
			return "", nil, errors.New("岗位名称需为不重复的 1–80 个字符，不能包含路径字符")
		}
		seen[role] = true
		count := utf8.RuneCountInString(prompt)
		if count == 0 || count > 8000 || strings.ContainsRune(prompt, '\x00') {
			return "", nil, errors.New("每个岗位的职责需为 1–8000 个字符")
		}
		total += count
		if total > 64000 || !safeTeamSkill(skill) {
			return "", nil, errors.New("职责总长不能超过 64000 个字符；Skill 需为有效名称而非路径")
		}
		model := ""
		if item.Model != nil {
			model = strings.TrimSpace(*item.Model)
		} else {
			for _, old := range previous {
				if old.Role == role {
					model = old.Model
					break
				}
			}
		}
		if utf8.RuneCountInString(model) > 200 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
			return "", nil, errors.New("模型配置名称无效")
		}
		roles = append(roles, TeamRole{Role: role, SystemPrompt: prompt, Skill: skill, Model: model})
	}
	return id, roles, nil
}

// This is a local settings surface, not a tenant API or a model-call endpoint.
func (g *GatewayService) handleTeamTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !personalMemoryLocalRequest(r) {
		sendJSONError(w, "团队配置仅允许本机同源访问", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/api/team-templates" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		sendJSONError(w, "不支持该请求方式", http.StatusMethodNotAllowed)
		return
	}
	if len(r.URL.Query()) > 0 {
		sendJSONError(w, "团队配置不接收 scope 或其他查询参数", http.StatusBadRequest)
		return
	}
	var input teamTemplateUpdate
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(interface{})) != io.EOF {
			sendJSONError(w, "团队配置格式无效或内容过大", http.StatusBadRequest)
			return
		}
		if len(input.Revision) != 64 {
			sendJSONError(w, "请先读取团队配置，再保存当前版本", http.StatusBadRequest)
			return
		}
	}
	g.configWriteMu.Lock()
	defer g.configWriteMu.Unlock()
	revision, err := configFileRevision(g.configpath)
	if err != nil {
		sendJSONError(w, "无法读取团队配置", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut && input.Revision != revision {
		teamTemplateConflict(w)
		return
	}
	config, err := LoadConfig(g.configpath)
	if err != nil {
		sendJSONError(w, "无法读取团队配置", http.StatusInternalServerError)
		return
	}
	current, err := configFileRevision(g.configpath)
	if err != nil || current != revision {
		teamTemplateConflict(w)
		return
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"teams": teamTemplatesForConfig(config), "config_revision": revision})
		return
	}
	id, roles, err := validateTeamTemplateUpdate(config, input)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if config.TeamTemplates == nil {
		config.TeamTemplates = make(map[string][]TeamRole)
	}
	config.TeamTemplates[id] = roles
	if validateSettingsConfigForSave(config) != nil {
		sendJSONError(w, "当前模型或助手配置无效，请先在系统设置中完成配置", http.StatusBadRequest)
		return
	}
	current, err = configFileRevision(g.configpath)
	if err != nil || current != revision {
		teamTemplateConflict(w)
		return
	}
	if SaveConfig(g.configpath, config) != nil {
		sendJSONError(w, "无法保存团队配置，原配置仍保留", http.StatusInternalServerError)
		return
	}
	savedRevision, err := configFileRevision(g.configpath)
	if err != nil || g.ReloadConfig() != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "saved_reload_failed",
			"message": "团队已写入配置文件，但运行时更新失败，请检查设置后重新加载", "config_revision": savedRevision})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "team": teamTemplateForConfig(config, id), "config_revision": savedRevision})
}

func teamTemplateConflict(w http.ResponseWriter) {
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "conflict", "message": "配置已发生变化，请重新读取并核对后保存"})
}
