package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const artifactFormatPreferencesConstraintKey = "artifact_format_preferences"

// ArtifactFormatPreference is a durable default for generated or edited
// artifacts. It lives inside UserProfile.Constraints so the feature remains
// compatible with existing databases while still giving artifact tools a
// predictable, typed contract.
type ArtifactFormatPreference struct {
	Scope                 string   `json:"scope"`
	OutputFormats         []string `json:"output_formats,omitempty"`
	FontFamily            string   `json:"font_family,omitempty"`
	FontSize              string   `json:"font_size,omitempty"`
	PageSize              string   `json:"page_size,omitempty"`
	Orientation           string   `json:"orientation,omitempty"`
	VisualStyle           string   `json:"visual_style,omitempty"`
	TemplateRule          string   `json:"template_rule,omitempty"`
	TableLayoutRule       string   `json:"table_layout_rule,omitempty"`
	PlaceholderRule       string   `json:"placeholder_rule,omitempty"`
	FileNamePattern       string   `json:"file_name_pattern,omitempty"`
	DateFormat            string   `json:"date_format,omitempty"`
	NumberFormat          string   `json:"number_format,omitempty"`
	AdditionalInstruction string   `json:"additional_instruction,omitempty"`
}

var (
	artifactFontSizePattern = regexp.MustCompile(`(?i)(小初|初号|小一|一号|小二|二号|小三|三号|小四|四号|小五|五号|小六|六号|小七|七号|小八|八号|[0-9]{1,2}(?:\.[0-9]+)?\s*(?:pt|磅|号))`)
	artifactPageSizePattern = regexp.MustCompile(`(?i)\b(A[3-6]|letter)\b`)
	artifactFileNamePattern = regexp.MustCompile(`(?i)(?:文件名|命名)(?:格式|规则)?(?:为|用|采用|[:：])\s*([^,，。;；\n]{1,80})`)
	artifactDatePattern     = regexp.MustCompile(`(?i)(?:日期格式|date format)(?:为|用|采用|[:：])?\s*([ymd年月日./_-]{3,24})`)
	artifactNumberPattern   = regexp.MustCompile(`(?i)(?:数字格式|数值格式|number format)(?:为|用|采用|[:：])?\s*([^,，。;；\n]{1,40})`)
)

// DetectArtifactFormatPreferences extracts only explicit long-lived defaults.
// One-off formatting instructions are intentionally left to the current turn
// and are not written into the user profile.
func DetectArtifactFormatPreferences(input string) []ArtifactFormatPreference {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	lower := strings.ToLower(input)
	if !containsAny(lower, []string{
		"以后", "今后", "往后", "每次", "始终", "一律", "统一", "默认", "长期", "记住", "我喜欢", "我偏好", "我习惯",
		"from now on", "always", "by default", "remember", "every time",
	}) {
		return nil
	}
	if !containsAny(lower, []string{
		"word", "docx", "文档", "报告", "公文", "excel", "xlsx", "电子表格", "工作簿",
		"powerpoint", "ppt", "pptx", "幻灯片", "演示文稿", "pdf", "产物", "文件", "表格", "表单",
		"格式", "排版", "字体", "字号", "模板", "模版", "下划线", "输出", "导出", "交付",
	}) {
		return nil
	}

	scopes := detectArtifactPreferenceScopes(lower)
	preference := extractArtifactFormatPreference(input, lower)
	updates := make([]ArtifactFormatPreference, 0, len(scopes))
	for _, scope := range scopes {
		item := preference
		item.Scope = scope
		updates = append(updates, item)
	}
	return updates
}

func detectArtifactPreferenceScopes(lower string) []string {
	var scopes []string
	if containsAny(lower, []string{"word", "docx", "文档", "报告", "公文"}) {
		scopes = append(scopes, "document")
	}
	if containsAny(lower, []string{"excel", "xlsx", "spreadsheet", "workbook", "电子表格", "工作簿"}) {
		scopes = append(scopes, "spreadsheet")
	}
	if containsAny(lower, []string{"powerpoint", "pptx", "ppt", "presentation", "slides", "幻灯片", "演示文稿"}) {
		scopes = append(scopes, "presentation")
	}
	if len(scopes) == 0 && strings.Contains(lower, "pdf") {
		scopes = append(scopes, "pdf")
	}
	if len(scopes) == 0 {
		scopes = append(scopes, "general")
	}
	return scopes
}

func extractArtifactFormatPreference(input, lower string) ArtifactFormatPreference {
	preference := ArtifactFormatPreference{}

	if containsAny(lower, []string{"输出", "导出", "交付", "保存为", "格式", "同时给", "同时提供", "deliver", "export", "output"}) {
		preference.OutputFormats = detectArtifactOutputFormats(lower)
	}
	for _, font := range []string{"Times New Roman", "Microsoft YaHei", "微软雅黑", "思源宋体", "思源黑体", "仿宋", "楷体", "黑体", "宋体", "Aptos", "Calibri", "Arial"} {
		if strings.Contains(lower, strings.ToLower(font)) {
			preference.FontFamily = font
			break
		}
	}
	if match := artifactFontSizePattern.FindString(input); match != "" {
		preference.FontSize = strings.TrimSpace(match)
	}
	if match := artifactPageSizePattern.FindString(input); match != "" {
		preference.PageSize = strings.ToUpper(match)
		if strings.EqualFold(match, "letter") {
			preference.PageSize = "Letter"
		}
	}
	switch {
	case strings.Contains(lower, "横向") || strings.Contains(lower, "landscape"):
		preference.Orientation = "landscape"
	case strings.Contains(lower, "纵向") || strings.Contains(lower, "portrait"):
		preference.Orientation = "portrait"
	}
	for _, style := range []string{"简洁", "正式", "商务", "科研", "学术", "极简", "专业", "minimal", "formal", "academic"} {
		if strings.Contains(lower, style) {
			preference.VisualStyle = style
			break
		}
	}

	switch {
	case containsAny(lower, []string{"沿用原模板", "使用原模板", "采用原模板", "基于原模板", "按原模板", "按照原模板"}):
		preference.TemplateRule = "沿用原模板"
	case containsAny(lower, []string{"按模板", "按照模板", "基于模板", "使用模板", "采用模板", "沿用模板"}):
		preference.TemplateRule = "按提供的模板"
	}

	var tableRules []string
	for _, candidate := range []struct {
		keywords []string
		value    string
	}{
		{[]string{"三线表"}, "使用三线表"},
		{[]string{"无边框", "无框线"}, "表格无边框"},
		{[]string{"保留原表格", "保持原表格", "不改表格布局", "不改变表格布局"}, "保留原表格布局"},
		{[]string{"不改行高", "不改变行高", "固定行高", "行高列宽"}, "保持行高"},
		{[]string{"不改列宽", "不改变列宽", "固定列宽", "行高列宽"}, "保持列宽"},
		{[]string{"自适应列宽", "自动调整列宽"}, "自适应列宽"},
		{[]string{"保留合并单元格", "不拆分合并单元格"}, "保留合并单元格"},
	} {
		if containsAny(lower, candidate.keywords) {
			tableRules = append(tableRules, candidate.value)
		}
	}
	preference.TableLayoutRule = strings.Join(uniqueStrings(tableRules), "；")

	if strings.Contains(lower, "下划线") {
		switch {
		case containsAny(lower, []string{"保留下划线", "不删除下划线", "不破坏下划线"}):
			preference.PlaceholderRule = "在下划线位置填写并保留下划线与原布局"
		case containsAny(lower, []string{"填充", "填写", "替换"}):
			preference.PlaceholderRule = "填写下划线占位内容"
		default:
			preference.PlaceholderRule = "保留下划线占位样式"
		}
	} else if containsAny(lower, []string{"保留占位符样式", "不改变占位符样式"}) {
		preference.PlaceholderRule = "填写时保留占位符样式"
	}

	if match := artifactFileNamePattern.FindStringSubmatch(input); len(match) > 1 {
		preference.FileNamePattern = strings.TrimSpace(match[1])
	}
	if match := artifactDatePattern.FindStringSubmatch(input); len(match) > 1 {
		preference.DateFormat = strings.TrimSpace(match[1])
	}
	if match := artifactNumberPattern.FindStringSubmatch(input); len(match) > 1 {
		preference.NumberFormat = strings.TrimSpace(match[1])
	}

	if artifactPreferenceIsEmpty(preference) {
		preference.AdditionalInstruction = truncatePreferenceInstruction(input, 300)
	}
	return preference
}

func detectArtifactOutputFormats(lower string) []string {
	formats := make([]string, 0, 6)
	for _, candidate := range []struct {
		keywords []string
		format   string
	}{
		{[]string{"word", "docx"}, "docx"},
		{[]string{"pdf"}, "pdf"},
		{[]string{"excel", "xlsx"}, "xlsx"},
		{[]string{"powerpoint", "pptx", "ppt"}, "pptx"},
		{[]string{"markdown", ".md"}, "markdown"},
		{[]string{"html", "网页"}, "html"},
	} {
		if containsAny(lower, candidate.keywords) {
			formats = append(formats, candidate.format)
		}
	}
	return formats
}

func artifactPreferenceIsEmpty(preference ArtifactFormatPreference) bool {
	return len(preference.OutputFormats) == 0 &&
		preference.FontFamily == "" && preference.FontSize == "" &&
		preference.PageSize == "" && preference.Orientation == "" &&
		preference.VisualStyle == "" && preference.TemplateRule == "" &&
		preference.TableLayoutRule == "" && preference.PlaceholderRule == "" &&
		preference.FileNamePattern == "" && preference.DateFormat == "" &&
		preference.NumberFormat == ""
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func truncatePreferenceInstruction(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// ApplyArtifactFormatPreferencesForTenant merges non-empty fields into the
// durable profile. Repeating a preference therefore overrides the old value
// for that field without erasing unrelated defaults.
func (m *MemoryStore) ApplyArtifactFormatPreferencesForTenant(tenantID, userName string, updates []ArtifactFormatPreference) error {
	if len(updates) == 0 {
		return nil
	}
	profile, err := m.getOrCreateUserProfileForTenant(tenantID, userName)
	if err != nil {
		return err
	}
	constraints, err := decodeConstraintJSON(profile.Constraints)
	if err != nil {
		return err
	}
	preferences, err := decodeArtifactPreferences(constraints[artifactFormatPreferencesConstraintKey])
	if err != nil {
		return err
	}
	for _, update := range updates {
		scope := normalizeArtifactPreferenceScope(update.Scope)
		if scope == "" {
			continue
		}
		update.Scope = scope
		preferences[scope] = mergeArtifactFormatPreference(preferences[scope], update)
	}
	encodedPreferences, err := json.Marshal(preferences)
	if err != nil {
		return fmt.Errorf("encode artifact format preferences: %w", err)
	}
	constraints[artifactFormatPreferencesConstraintKey] = encodedPreferences
	encodedConstraints, err := json.Marshal(constraints)
	if err != nil {
		return fmt.Errorf("encode user constraints: %w", err)
	}
	profile.Constraints = datatypes.JSON(encodedConstraints)
	profile.LastSeen = time.Now()
	return m.db.Save(profile).Error
}

func (m *MemoryStore) ApplyArtifactFormatPreferences(userName string, updates []ArtifactFormatPreference) error {
	return m.ApplyArtifactFormatPreferencesForTenant(defaultExecutionTenantID, userName, updates)
}

func (m *MemoryStore) GetArtifactFormatPreferencesForTenant(tenantID, userName string) (map[string]ArtifactFormatPreference, error) {
	profile, err := m.GetUserProfileForTenant(tenantID, userName)
	if err != nil {
		return nil, err
	}
	constraints, err := decodeConstraintJSON(profile.Constraints)
	if err != nil {
		return nil, err
	}
	return decodeArtifactPreferences(constraints[artifactFormatPreferencesConstraintKey])
}

func (m *MemoryStore) GetArtifactFormatPreferences(userName string) (map[string]ArtifactFormatPreference, error) {
	return m.GetArtifactFormatPreferencesForTenant(defaultExecutionTenantID, userName)
}

func (m *MemoryStore) getOrCreateUserProfileForTenant(tenantID, userName string) (*UserProfile, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	userName = strings.TrimSpace(userName)
	if userName == "" {
		return nil, fmt.Errorf("user name is required")
	}
	tenantID = memoryTenantID(tenantID)
	var profile UserProfile
	result := m.db.Where("tenant_id = ? AND user_name = ?", tenantID, userName).First(&profile)
	if result.Error == nil {
		return &profile, nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, result.Error
	}
	now := time.Now()
	profile = UserProfile{
		TenantID:           tenantID,
		UserName:           userName,
		FirstSeen:          now,
		LastSeen:           now,
		Language:           "zh",
		ResponseStyle:      "balanced",
		ToolStats:          datatypes.JSON(`{}`),
		SkillStats:         datatypes.JSON(`{}`),
		TopicFrequency:     datatypes.JSON(`{}`),
		PersonalityTraits:  datatypes.JSON(`{}`),
		CareerAffinity:     datatypes.JSON(`{}`),
		InterestAreas:      datatypes.JSON(`{}`),
		CommunicationStyle: datatypes.JSON(`{}`),
		Constraints:        datatypes.JSON(`{}`),
		PositivePatterns:   datatypes.JSON(`[]`),
		NegativePatterns:   datatypes.JSON(`[]`),
		RecentQuestions:    datatypes.JSON(`[]`),
	}
	if err := m.db.Create(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func decodeConstraintJSON(value datatypes.JSON) (map[string]json.RawMessage, error) {
	constraints := make(map[string]json.RawMessage)
	if len(value) == 0 || string(value) == "null" {
		return constraints, nil
	}
	if err := json.Unmarshal(value, &constraints); err != nil {
		return nil, fmt.Errorf("decode user constraints: %w", err)
	}
	return constraints, nil
}

func decodeArtifactPreferences(value json.RawMessage) (map[string]ArtifactFormatPreference, error) {
	preferences := make(map[string]ArtifactFormatPreference)
	if len(value) == 0 || string(value) == "null" {
		return preferences, nil
	}
	if err := json.Unmarshal(value, &preferences); err != nil {
		return nil, fmt.Errorf("decode artifact format preferences: %w", err)
	}
	return preferences, nil
}

func normalizeArtifactPreferenceScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "document", "word", "docx":
		return "document"
	case "spreadsheet", "excel", "xlsx":
		return "spreadsheet"
	case "presentation", "powerpoint", "ppt", "pptx":
		return "presentation"
	case "pdf":
		return "pdf"
	case "general", "artifact", "all":
		return "general"
	default:
		return ""
	}
}

func mergeArtifactFormatPreference(current, update ArtifactFormatPreference) ArtifactFormatPreference {
	current.Scope = update.Scope
	if len(update.OutputFormats) > 0 {
		current.OutputFormats = uniqueStrings(update.OutputFormats)
	}
	if update.FontFamily != "" {
		current.FontFamily = update.FontFamily
	}
	if update.FontSize != "" {
		current.FontSize = update.FontSize
	}
	if update.PageSize != "" {
		current.PageSize = update.PageSize
	}
	if update.Orientation != "" {
		current.Orientation = update.Orientation
	}
	if update.VisualStyle != "" {
		current.VisualStyle = update.VisualStyle
	}
	if update.TemplateRule != "" {
		current.TemplateRule = update.TemplateRule
	}
	if update.TableLayoutRule != "" {
		current.TableLayoutRule = update.TableLayoutRule
	}
	if update.PlaceholderRule != "" {
		current.PlaceholderRule = update.PlaceholderRule
	}
	if update.FileNamePattern != "" {
		current.FileNamePattern = update.FileNamePattern
	}
	if update.DateFormat != "" {
		current.DateFormat = update.DateFormat
	}
	if update.NumberFormat != "" {
		current.NumberFormat = update.NumberFormat
	}
	if update.AdditionalInstruction != "" {
		current.AdditionalInstruction = update.AdditionalInstruction
	}
	return current
}

// FormatArtifactFormatPreferences renders a compact prompt fragment. The
// current request is explicitly authoritative, preventing a remembered default
// from overriding a one-off instruction.
func FormatArtifactFormatPreferences(preferences map[string]ArtifactFormatPreference) string {
	if len(preferences) == 0 {
		return ""
	}
	order := map[string]int{"document": 0, "spreadsheet": 1, "presentation": 2, "pdf": 3, "general": 4}
	keys := make([]string, 0, len(preferences))
	for key := range preferences {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, leftOK := order[keys[i]]
		right, rightOK := order[keys[j]]
		if leftOK && rightOK && left != right {
			return left < right
		}
		if leftOK != rightOK {
			return leftOK
		}
		return keys[i] < keys[j]
	})

	var lines []string
	for _, key := range keys {
		preference := preferences[key]
		var details []string
		if len(preference.OutputFormats) > 0 {
			details = append(details, "输出="+strings.Join(preference.OutputFormats, "+"))
		}
		for _, field := range []struct{ label, value string }{
			{"字体", preference.FontFamily}, {"字号", preference.FontSize}, {"纸张", preference.PageSize},
			{"方向", preference.Orientation}, {"风格", preference.VisualStyle}, {"模板", preference.TemplateRule},
			{"表格", preference.TableLayoutRule}, {"占位填写", preference.PlaceholderRule},
			{"文件名", preference.FileNamePattern}, {"日期", preference.DateFormat}, {"数字", preference.NumberFormat},
			{"补充", preference.AdditionalInstruction},
		} {
			if field.value != "" {
				details = append(details, field.label+"="+field.value)
			}
		}
		if len(details) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s：%s", artifactPreferenceScopeLabel(key), strings.Join(details, "；")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "- 复杂产物格式偏好（长期默认；当前请求的明确格式要求优先）：\n" + strings.Join(lines, "\n")
}

func artifactPreferenceScopeLabel(scope string) string {
	switch scope {
	case "document":
		return "Word/文档"
	case "spreadsheet":
		return "Excel/电子表格"
	case "presentation":
		return "PPT/演示文稿"
	case "pdf":
		return "PDF"
	default:
		return "通用产物"
	}
}
