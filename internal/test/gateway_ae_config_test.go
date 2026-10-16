package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLoadAEsFromDBMergesBuiltInDefaults(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	if err := db.Create(&AEConfigRecord{
		AeID:     "meeting-secretary",
		Name:     "\u4f1a\u8bae\u79d8\u4e66",
		AtName:   "\u4f1a\u8bae\u79d8\u4e66",
		Role:     "legacy",
		Pipeline: "legacy-secretary-pipeline",
	}).Error; err != nil {
		t.Fatalf("insert ae config: %v", err)
	}

	gw := &GatewayService{db: db}
	configs := gw.loadAEsFromDB("")
	if len(configs) != 1 {
		t.Fatalf("configs length = %d, want 1", len(configs))
	}
	cfg := configs[0]
	if cfg.Name != "\u79d8\u4e66" || cfg.AtName != "\u79d8\u4e66" {
		t.Fatalf("built-in secretary aliases not normalized: name=%q at=%q", cfg.Name, cfg.AtName)
	}
	if cfg.Direct == nil {
		t.Fatal("built-in direct config should be merged from defaults")
	}
	if cfg.SystemMessage == "" {
		t.Fatal("built-in system message should be merged from defaults")
	}
}

func TestSeedDefaultAEsBackfillsMissingBuiltIns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	if err := db.Create(&AEConfigRecord{
		AeID:   "meeting-secretary",
		Name:   "自定义秘书",
		AtName: "秘书",
		Role:   "custom role",
	}).Error; err != nil {
		t.Fatalf("insert ae config: %v", err)
	}
	if err := db.Create(&AEConfigRecord{
		AeID:           "fundraising-advisor",
		TenantID:       "default",
		Name:           "投融资顾问（焕智FA）",
		AtName:         "投融资顾问",
		Role:           "legacy fundraising role",
		SystemPrompt:   "legacy fundraising prompt",
		MentionAliases: `["焕智FA","投融资专家"]`,
	}).Error; err != nil {
		t.Fatalf("insert legacy fundraising config: %v", err)
	}

	gw := &GatewayService{db: db, logger: log.New(io.Discard, "", 0)}
	gw.seedDefaultAEs()

	var codex AEConfigRecord
	if err := db.Where("ae_id = ?", "codex-specialist").First(&codex).Error; err != nil {
		t.Fatalf("codex-specialist was not backfilled: %v", err)
	}
	var secretary AEConfigRecord
	if err := db.Where("ae_id = ?", "meeting-secretary").First(&secretary).Error; err != nil {
		t.Fatalf("meeting-secretary missing: %v", err)
	}
	if secretary.Name != "自定义秘书" || secretary.Role != "custom role" {
		t.Fatalf("existing employee was overwritten: name=%q role=%q", secretary.Name, secretary.Role)
	}
	var fundraising AEConfigRecord
	if err := db.Where("ae_id = ?", "fundraising-advisor").First(&fundraising).Error; err != nil {
		t.Fatalf("fundraising-advisor was not backfilled: %v", err)
	}
	if fundraising.Name != "融资专家" || fundraising.AtName != "融资专家" {
		t.Fatalf("fundraising identity was not migrated: name=%q at=%q", fundraising.Name, fundraising.AtName)
	}
	for _, alias := range []string{"投融资顾问", "融资专员", "FA顾问", "BP专家"} {
		if !strings.Contains(fundraising.MentionAliases, alias) {
			t.Fatalf("fundraising aliases missing %q: %s", alias, fundraising.MentionAliases)
		}
	}
	if !strings.Contains(fundraising.SystemPrompt, "本轮直接交付完整 v0") ||
		!strings.Contains(fundraising.SystemPrompt, "规划估算（非项目事实）") {
		t.Fatalf("fundraising policy was not migrated: %q", fundraising.SystemPrompt)
	}
	if !strings.Contains(fundraising.RoutingExamples, "A轮融资") ||
		!strings.Contains(fundraising.RoutingExamples, "一级市场项目") {
		t.Fatalf("fundraising routing examples were not persisted: %q", fundraising.RoutingExamples)
	}

	gw.aeManager = NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	gw.aeManager.Register(gw.loadAEsFromDB(""))
	cfg, task := gw.aeManager.MatchAtName("@codex 查看下 e盘下的ai-movie 项目")
	if cfg == nil || cfg.ID != "codex-specialist" {
		t.Fatalf("@codex matched %+v, want codex-specialist", cfg)
	}
	if !strings.Contains(task, "ai-movie") {
		t.Fatalf("task = %q, want user task preserved", task)
	}
	fa, task := gw.aeManager.MatchAtName("@投融资顾问 评估一下我们是否适合启动A轮融资")
	if fa == nil || fa.ID != "fundraising-advisor" || !strings.Contains(task, "A轮融资") {
		t.Fatalf("@投融资顾问 matched %#v with task %q", fa, task)
	}
	if examples := strings.Join(fa.GetRoutingExamples(), "\n"); !strings.Contains(examples, "TS") {
		t.Fatalf("loaded fundraising advisor lost routing examples: %q", examples)
	}
	if fa.Direct == nil || fa.Direct.ArtifactPolicy != aiEmployeeArtifactPolicyExplicitRequest {
		t.Fatalf("loaded fundraising advisor lost explicit artifact policy: %#v", fa.Direct)
	}
	if strings.Contains(fa.Direct.Prompt, "BP/TS审阅、尽调清单、投资备忘录或可下载交付物") {
		t.Fatalf("fundraising prompt still treats content nouns as file authorization: %q", fa.Direct.Prompt)
	}
}

func TestRetireLegacySkillOnlyDefaultAEsPreservesCustomizedEmployees(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}

	legacyRelationship := AEConfigRecord{
		AeID: "relationship-mentor", TenantID: "default", Name: "情感导师", AtName: "情感导师",
		Role: "legacy relationship role", Tier: "community", SystemPrompt: "legacy relationship prompt",
		MentionAliases: `["情感咨询"]`, RoutingExamples: `["帮我分析关系"]`,
	}
	legacyHardship := AEConfigRecord{
		AeID: "hardship-support", TenantID: "default", Name: "困难帮扶专员", AtName: "困难帮扶",
		Role: "legacy hardship role", Tier: "community", SystemPrompt: "legacy hardship prompt",
		MentionAliases: `["债务帮扶"]`, RoutingExamples: `["帮我梳理债务"]`,
	}
	customRelationship := legacyRelationship
	customRelationship.ID = 0
	customRelationship.TenantID = "custom-tenant"
	customRelationship.SystemPrompt = "user customized prompt"
	unrelated := AEConfigRecord{
		AeID: "custom-helper", TenantID: "default", Name: "自定义助手", AtName: "自定义助手",
		Role: "custom role",
	}
	if err := db.Create(&[]AEConfigRecord{
		legacyRelationship, legacyHardship, customRelationship, unrelated,
	}).Error; err != nil {
		t.Fatalf("insert employee fixtures: %v", err)
	}

	fingerprints := map[string]string{
		legacyRelationship.AeID: aeConfigRecordFingerprint(legacyRelationship),
		legacyHardship.AeID:     aeConfigRecordFingerprint(legacyHardship),
	}
	gw := &GatewayService{db: db, logger: log.New(io.Discard, "", 0)}
	gw.retireLegacySkillOnlyDefaultAEsWithFingerprints(fingerprints)

	for _, employee := range []AEConfigRecord{legacyRelationship, legacyHardship} {
		var count int64
		if err := db.Model(&AEConfigRecord{}).
			Where("ae_id = ? AND tenant_id = ?", employee.AeID, employee.TenantID).
			Count(&count).Error; err != nil {
			t.Fatalf("count retired %s: %v", employee.AeID, err)
		}
		if count != 0 {
			t.Fatalf("legacy product-owned employee %s was not retired", employee.AeID)
		}
	}

	for _, employee := range []AEConfigRecord{customRelationship, unrelated} {
		var count int64
		if err := db.Model(&AEConfigRecord{}).
			Where("ae_id = ? AND tenant_id = ?", employee.AeID, employee.TenantID).
			Count(&count).Error; err != nil {
			t.Fatalf("count preserved %s: %v", employee.AeID, err)
		}
		if count != 1 {
			t.Fatalf("custom employee %s/%s should be preserved, count=%d", employee.AeID, employee.TenantID, count)
		}
	}
}

func TestSeedDefaultAEsMigratesBuiltInFinancialEmployeeFacades(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	legacy := []AEConfigRecord{
		{AeID: "quant-trader", TenantID: "default", Name: "AI量化交易员", AtName: "量化交易员", Role: "复合量化角色", Pipeline: "legacy-composite", SystemPrompt: "legacy composite prompt", MentionAliases: `["量化Agent"]`},
		{AeID: "investment-research-manager", TenantID: "default", Name: "旧投研", AtName: "旧投研", Role: "legacy", Pipeline: "legacy-research", SystemPrompt: "legacy"},
		{AeID: "financial-intelligence-analyst", TenantID: "default", Name: "AI金融分析师", AtName: "金融分析师", Role: "legacy", Pipeline: "legacy-finance", SystemPrompt: "legacy", MentionAliases: `["金融分析师"]`},
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("insert legacy financial employees: %v", err)
	}

	gw := &GatewayService{db: db, logger: log.New(io.Discard, "", 0)}
	gw.seedDefaultAEs()

	want := map[string]struct {
		name     string
		atName   string
		pipeline string
	}{
		"quant-trader":                   {"AI交易员", "交易员", "ai-trader"},
		"investment-research-manager":    {"AI投研经理", "投研经理", "investment-research-manager"},
		"financial-intelligence-analyst": {"AI金信分析师", "金信分析师", "financial-intelligence-analyst"},
	}
	for id, expected := range want {
		var record AEConfigRecord
		if err := db.Where("ae_id = ?", id).First(&record).Error; err != nil {
			t.Fatalf("load migrated %s: %v", id, err)
		}
		if record.Name != expected.name || record.AtName != expected.atName || record.Pipeline != expected.pipeline {
			t.Fatalf("%s migration = name:%q at:%q pipeline:%q", id, record.Name, record.AtName, record.Pipeline)
		}
		if strings.Contains(record.Name, "金融分析师") || strings.Contains(record.AtName, "金融分析师") || strings.Contains(record.MentionAliases, "金融分析师") {
			t.Fatalf("%s retained forbidden 金融分析师 identity: %#v", id, record)
		}
		if !strings.Contains(record.SystemPrompt, "不得执行真实下单") {
			t.Fatalf("%s did not receive sealed no-live-order policy", id)
		}
		if !record.InternalOnly {
			t.Fatalf("%s should migrate to internal-only compatibility facade", id)
		}
	}

	configs := gw.loadAEsFromDB("default")
	mgr := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	mgr.Register(configs)
	if cfg, _ := mgr.MatchAtName("@量化交易员 分析新安股份"); cfg != nil {
		t.Fatalf("internal quant facade still exposes a user @ route: %#v", cfg)
	}
	if cfg, _ := mgr.MatchAtName("@金融分析师 分析新安股份"); cfg != nil {
		t.Fatalf("forbidden legacy 金融分析师 alias still routes: %#v", cfg)
	}
}

func TestLoadAEsFromDBBackfillsBuiltInPipelineWhenEmpty(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	if err := db.Create(&AEConfigRecord{
		AeID:   "lead-researcher",
		Name:   "\u6f5c\u5ba2\u7814\u7a76\u5458",
		AtName: "\u6f5c\u5ba2\u7814\u7a76",
		Role:   "legacy",
	}).Error; err != nil {
		t.Fatalf("insert ae config: %v", err)
	}

	gw := &GatewayService{db: db}
	configs := gw.loadAEsFromDB("")
	if len(configs) != 1 {
		t.Fatalf("configs length = %d, want 1", len(configs))
	}
	if configs[0].Pipeline != "lead-research" {
		t.Fatalf("pipeline = %q, want lead-research", configs[0].Pipeline)
	}
}

func TestLoadAEsFromDBSkipsInternalModelDefaultsRecord(t *testing.T) {
	setAIEmployeeDefaultModelBinding("")
	defer setAIEmployeeDefaultModelBinding("")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	if err := db.Create(&AEConfigRecord{
		AeID:         aiEmployeeDefaultModelBindingID,
		TenantID:     "default",
		Name:         "AI员工默认模型",
		AtName:       aiEmployeeDefaultModelBindingID,
		Role:         "internal",
		ModelBinding: `{"text-to-text":"deepseek-chat"}`,
	}).Error; err != nil {
		t.Fatalf("insert model defaults: %v", err)
	}

	gw := &GatewayService{db: db}
	configs := gw.loadAEsFromDB("")
	if len(configs) != 0 {
		t.Fatalf("configs length = %d, want internal defaults hidden", len(configs))
	}
	if got := getAIEmployeeDefaultModelBinding(); !strings.Contains(got, "deepseek-chat") {
		t.Fatalf("default model binding = %q, want deepseek-chat", got)
	}
}

func TestAIEmployeeDefaultModelBindingExplainsRouteSource(t *testing.T) {
	oldRegistry := globalProviderRegistry
	defer func() {
		globalProviderRegistry = oldRegistry
		setAIEmployeeDefaultModelBinding("")
	}()
	reg := &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"deepseek": {
				Name:    "deepseek",
				Label:   "DeepSeek",
				Type:    "deepseek",
				BaseURL: "https://api.deepseek.com/v1",
				APIKey:  "sk-test-deepseek-valid-key",
				Models:  []ProviderModel{{Name: "deepseek-chat", Capability: "text-to-text"}},
			},
		},
	}
	globalProviderRegistry = reg
	setAIEmployeeDefaultModelBinding(`{"text-to-text":"deepseek-chat"}`)

	info := reg.ExplainResolveForAE("investment-advisor", "text-to-text")
	if !info.Available || info.Model != "deepseek-chat" {
		t.Fatalf("unexpected route: %#v", info)
	}
	if info.Source != "ai_employee_default" {
		t.Fatalf("route source = %q, want ai_employee_default", info.Source)
	}
}

func TestSaveAIEmployeeDefaultModelBindingWritesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openkitty-config.yaml")
	cfg := &Config{TeamName: "test"}
	gw := &GatewayService{config: cfg, configpath: configPath}

	if err := gw.saveAIEmployeeDefaultModelBinding(`{"text-to-text":"minimax-m3","multi-modal":"minimax-m3"}`); err != nil {
		t.Fatalf("save model defaults: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "ai_employee_model") || !strings.Contains(string(data), "minimax-m3") {
		t.Fatalf("config should contain ai_employee_model defaults, got:\n%s", string(data))
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.AIEmployeeModel.Defaults["text-to-text"] != "minimax-m3" {
		t.Fatalf("text default = %q, want minimax-m3", loaded.AIEmployeeModel.Defaults["text-to-text"])
	}
}

func TestSaveModelProvidersToConfigWritesPlainProvider(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openkitty-config.yaml")
	cfg := &Config{TeamName: "test"}
	gw := &GatewayService{config: cfg, configpath: configPath}

	providers := []*ModelProvider{{
		Name:    "minimax",
		Label:   "MiniMax",
		Type:    "minimax",
		BaseURL: "https://api.minimaxi.com/v1",
		APIKey:  "sk-plain-test-key",
		Models:  []ProviderModel{{Name: "minimax-m3", Capability: "text-to-text", MaxTokens: 16384}},
	}}
	if err := gw.saveModelProvidersToConfig(providers); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.ModelProviders) != 1 {
		t.Fatalf("providers length = %d, want 1", len(loaded.ModelProviders))
	}
	if loaded.ModelProviders[0].APIKey != "sk-plain-test-key" {
		t.Fatalf("api key = %q, want plaintext key", loaded.ModelProviders[0].APIKey)
	}
	if loaded.ModelProviders[0].Models[0].MaxTokens != 16384 {
		t.Fatalf("max_tokens = %d, want 16384", loaded.ModelProviders[0].Models[0].MaxTokens)
	}
}

func TestModelProviderSaveEncryptsAPIKeyInDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&ModelProviderRecord{}); err != nil {
		t.Fatalf("migrate provider records: %v", err)
	}
	oldRegistry := globalProviderRegistry
	workspace := t.TempDir()
	reg := NewModelProviderRegistry(db, log.New(io.Discard, "", 0), workspace)
	defer func() { globalProviderRegistry = oldRegistry }()

	provider := &ModelProvider{
		Name:    "deepseek",
		Label:   "DeepSeek",
		Type:    "deepseek",
		BaseURL: "https://api.deepseek.com/v1",
		APIKey:  "sk-real-looking-test-key",
		Models:  []ProviderModel{{Name: "deepseek-chat", Capability: "text-to-text", MaxTokens: 8192}},
	}
	if err := reg.Save(provider); err != nil {
		t.Fatalf("save provider with api key: %v", err)
	}
	loaded, ok := reg.Get("deepseek")
	if !ok {
		t.Fatal("provider should be available after save")
	}
	if loaded.APIKey != "sk-real-looking-test-key" {
		t.Fatalf("runtime api key = %q, want saved key", loaded.APIKey)
	}
	var rec ModelProviderRecord
	if err := db.Where("name = ?", "deepseek").First(&rec).Error; err != nil {
		t.Fatalf("load provider record: %v", err)
	}
	if rec.APIKey == "sk-real-looking-test-key" || !strings.HasPrefix(rec.APIKey, providerSecretPrefix) {
		t.Fatalf("db api key was not encrypted: %q", rec.APIKey)
	}

	reloaded := NewModelProviderRegistry(db, log.New(io.Discard, "", 0), workspace)
	reloadedProvider, ok := reloaded.Get("deepseek")
	if !ok || reloadedProvider.APIKey != "sk-real-looking-test-key" {
		t.Fatalf("reloaded provider did not decrypt API key: %#v", reloadedProvider)
	}
}

func TestDefaultProviderRejectsUnavailableRemoteProvider(t *testing.T) {
	provider := &ModelProvider{
		Name:      "deepseek",
		Label:     "DeepSeek",
		Type:      "deepseek",
		BaseURL:   "https://api.deepseek.com/v1",
		IsDefault: true,
		Models:    []ProviderModel{{Name: "deepseek-chat", Capability: "text-to-text", MaxTokens: 8192}},
	}
	if err := validateDefaultProvider(provider); err == nil {
		t.Fatal("expected missing api key to reject default provider")
	}
}

func TestDefaultProviderAllowsLocalProviderWithoutAPIKey(t *testing.T) {
	provider := &ModelProvider{
		Name:      "ollama",
		Label:     "Ollama",
		Type:      "local",
		BaseURL:   "http://localhost:11434/v1",
		IsDefault: true,
		Models:    []ProviderModel{{Name: "qwen3", Capability: "text-to-text", MaxTokens: 8192}},
	}
	if err := validateDefaultProvider(provider); err != nil {
		t.Fatalf("local provider should be valid default without api key: %v", err)
	}
}

func TestSaveAEToDBNormalizesCapabilityModelBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AEConfigRecord{}); err != nil {
		t.Fatalf("migrate ae configs: %v", err)
	}
	gw := &GatewayService{db: db}
	cfg := &AIEmployeeConfig{
		ID:           "data-analyst",
		Name:         "数据分析师",
		AtName:       "数据分析",
		Role:         "legacy",
		ModelBinding: `{"text-to-image":"doubao-seedream-4-0","text-to-text":"minimax-m3"}`,
	}
	if err := gw.saveAEToDB(cfg); err != nil {
		t.Fatalf("save ae config: %v", err)
	}

	var rec AEConfigRecord
	if err := db.Where("ae_id = ?", "data-analyst").First(&rec).Error; err != nil {
		t.Fatalf("load ae config: %v", err)
	}
	if !strings.Contains(rec.ModelBinding, "minimax-m3") {
		t.Fatalf("model binding = %q, want text-to-text minimax-m3", rec.ModelBinding)
	}
	if !strings.Contains(rec.ModelBinding, "doubao-seedream-4-0") {
		t.Fatalf("model binding = %q, want text-to-image binding", rec.ModelBinding)
	}
}

func TestDecodeAIEmployeeConfigAcceptsObjectModelBinding(t *testing.T) {
	cfg, _, err := decodeAIEmployeeConfigPayload(bytes.NewBufferString(`{
		"id":"designer",
		"name":"设计师",
		"at_name":"设计",
		"role":"文生图",
		"model_binding":{
			"text-to-text":"minimax-m3",
			"text-to-image":"doubao-seedream-4-0"
		}
	}`))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !strings.Contains(cfg.ModelBinding, "text-to-image") || !strings.Contains(cfg.ModelBinding, "doubao-seedream-4-0") {
		t.Fatalf("unexpected model binding: %q", cfg.ModelBinding)
	}
}

func TestNormalizeAEModelBindingRejectsUnsupportedCapability(t *testing.T) {
	_, err := normalizeAEModelBinding(`{"random-capability":"minimax-m3"}`)
	if err == nil {
		t.Fatal("expected unsupported capability error")
	}
	if !strings.Contains(err.Error(), "unsupported model capability") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultDataAnalysisPipelineIsSchemaAware(t *testing.T) {
	gw := &GatewayService{}
	pipeline, ok := gw.defaultPipelineSet()["data-analysis-pipeline"]
	if !ok {
		t.Fatal("data-analysis-pipeline should be registered")
	}
	if len(pipeline.Steps) != 4 {
		t.Fatalf("data-analysis-pipeline steps = %d, want 4", len(pipeline.Steps))
	}
	combined := ""
	for _, step := range pipeline.Steps {
		combined += step.Prompt + "\n"
	}
	for _, want := range []string{
		"自适应分析",
		"通用表",
		"只有存在销售字段",
		"不得为不存在的字段编造指标",
		"json.load",
		"os.replace",
		"真实存在的图表文件",
		"完整数据分析 Markdown 报告",
		"data_analysis_report.md",
		"数据来源与分析口径",
		"产出文件清单",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("data-analysis prompt should contain %q", want)
		}
	}
	if strings.Contains(combined, "sales_analysis_report.md") {
		t.Fatal("data-analysis prompt should not force the generic pipeline into a sales report filename")
	}
}

func TestWechatArticlePipelineIncludesMaterialSOP(t *testing.T) {
	gw := &GatewayService{}
	pipeline, ok := gw.defaultPipelineSet()["wechat-article"]
	if !ok {
		t.Fatal("wechat-article should be registered")
	}
	if len(pipeline.Steps) != 2 {
		t.Fatalf("wechat-article steps = %d, want 2", len(pipeline.Steps))
	}
	if pipeline.Steps[len(pipeline.Steps)-1].RequireApproval {
		t.Fatal("wechat-article final self-check should not block on manual approval")
	}
	combined := ""
	for _, step := range pipeline.Steps {
		combined += step.Name + "\n" + step.Prompt + "\n"
	}
	for _, want := range []string{
		".openkitty/storage/wechat_article_${execution_id}.md",
		".openkitty/storage/wechat_article_${execution_id}.html",
		"40-120",
		"素材准备",
		"公众号交付物",
		"只调用一次 wechat_article_bundle",
		"不做普通网页搜索",
		"不再读取文件",
		"完整显示刚刚写入 markdown 的公众号正文",
		"\u5c01\u9762\u56fe\u63d0\u793a\u8bcd",
		"\u53d1\u5e03\u524d",
		"只冻结最终选中的 1 份",
		"1000-1500",
		"source_sha256",
		"可用图片清单",
		"不得用提示词、配图建议或素材库占位冒充图片",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("wechat-article prompt should contain %q", want)
		}
	}
	acquireTools := make(map[string]bool, len(pipeline.Steps[0].Tools))
	for _, name := range pipeline.Steps[0].Tools {
		acquireTools[name] = true
	}
	if !acquireTools["source_acquire"] {
		t.Fatalf("wechat-article material step should expose generic source_acquire: %v", pipeline.Steps[0].Tools)
	}
	for _, providerSpecific := range []string{"nova_chatroom_search", "Nova", "chatroom"} {
		if strings.Contains(combined, providerSpecific) {
			t.Fatalf("wechat-article business prompt must not contain provider/case-specific term %q", providerSpecific)
		}
	}
	if pipeline.Steps[0].SourceContract == nil || !pipeline.Steps[0].SourceContract.RequireDownstreamRead {
		t.Fatal("wechat-article source acquisition should enforce a frozen downstream-readable source")
	}
	deliveryTools := make(map[string]bool, len(pipeline.Steps[1].Tools))
	for _, name := range pipeline.Steps[1].Tools {
		deliveryTools[name] = true
	}
	if !deliveryTools["wechat_article_bundle"] || deliveryTools["write_file"] || deliveryTools["web_fetch"] || deliveryTools["read_file"] {
		t.Fatalf("wechat-article delivery should use only the deterministic bundle tool: %v", pipeline.Steps[1].Tools)
	}
	if pipeline.Steps[1].ArtifactContract == nil || len(pipeline.Steps[1].ArtifactContract.Paths) != 2 || pipeline.Steps[1].ArtifactContract.RequireRead {
		t.Fatal("wechat-article delivery should host-validate both current-run artifacts without a model re-read")
	}
	if pipeline.Steps[1].TimeoutSeconds <= 0 || pipeline.Steps[1].TimeoutSeconds > 90 {
		t.Fatalf("wechat-article delivery should fail over promptly, timeout=%d", pipeline.Steps[1].TimeoutSeconds)
	}

	var contentEditor *AIEmployeeConfig
	for _, cfg := range defaultAIEmployees() {
		if cfg.ID == "content-editor" {
			c := cfg
			contentEditor = &c
			break
		}
	}
	if contentEditor == nil {
		t.Fatal("content-editor should be registered")
	}
	foundAlias := false
	for _, alias := range contentEditor.MentionAliases {
		if alias == "\u516c\u4f17\u53f7\u52a9\u624b" {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Fatalf("content-editor aliases = %v, want 公众号助手", contentEditor.MentionAliases)
	}
}

func TestSyncBuiltinPipelinesRefreshesWechatArticle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&PipelineDBRecord{}, &PipelineTriggerDBRecord{}); err != nil {
		t.Fatalf("migrate pipelines: %v", err)
	}
	staleSteps, _ := json.Marshal([]PipelineStep{{
		Name:   "微信排版物料",
		Prompt: "固定保存到 .openkitty/storage/wechat_article.md 和 .openkitty/storage/wechat_article.html；如同名冲突可追加日期或时间。",
		Tools:  []string{"write_file"},
	}})
	if err := db.Create(&PipelineDBRecord{
		Name:        "wechat-article",
		DisplayName: "old",
		Description: "old",
		StepsJSON:   string(staleSteps),
	}).Error; err != nil {
		t.Fatalf("insert stale pipeline: %v", err)
	}

	gw := &GatewayService{
		db:     db,
		config: &Config{},
		logger: log.New(io.Discard, "", 0),
	}
	gw.loadPipelinesFromDB()

	pipeline, ok := gw.config.Pipeline.Pipelines["wechat-article"]
	if !ok {
		t.Fatal("wechat-article should be loaded")
	}
	combined := ""
	for _, step := range pipeline.Steps {
		combined += step.Name + "\n" + step.Prompt + "\n"
	}
	toolSet := make(map[string]bool)
	for _, step := range pipeline.Steps {
		for _, name := range step.Tools {
			toolSet[name] = true
		}
	}
	if !toolSet["source_acquire"] || !toolSet["wechat_article_bundle"] || !strings.Contains(combined, "wechat_article_${execution_id}.html") {
		t.Fatal("wechat-article builtin SOP was not refreshed from code")
	}
	if strings.Contains(combined, "固定保存到 .openkitty/storage/wechat_article.md 和 .openkitty/storage/wechat_article.html；如同名冲突可追加日期或时间。") {
		t.Fatal("stale wechat-article SOP should be overwritten")
	}
}

func TestDataAnalystWantsPresentationArtifact(t *testing.T) {
	tests := []struct {
		name string
		task string
		want bool
	}{
		{name: "ppt report", task: "针对 diamonds.csv 整理成一个ppt报告", want: true},
		{name: "slides", task: "请生成幻灯片汇报材料", want: true},
		{name: "powerpoint", task: "build a PowerPoint deck from the analysis", want: true},
		{name: "plain report", task: "整理成一个完整报告", want: false},
		{name: "negated ppt", task: "整理成报告，不需要ppt", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dataAnalystWantsPresentationArtifact(tt.task); got != tt.want {
				t.Fatalf("dataAnalystWantsPresentationArtifact(%q) = %v, want %v", tt.task, got, tt.want)
			}
		})
	}
}

func TestDataAnalystPresentationPipelineAddsPPTStep(t *testing.T) {
	gw := &GatewayService{}
	mgr := NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	pipeline, ok := mgr.dataAnalystPresentationPipeline("data-analysis-pipeline", "")
	if !ok {
		t.Fatal("data analyst presentation pipeline should resolve")
	}
	if pipeline.Name != dataAnalysisPresentationPipelineName {
		t.Fatalf("pipeline name = %q, want %q", pipeline.Name, dataAnalysisPresentationPipelineName)
	}
	if len(pipeline.Steps) != 5 {
		t.Fatalf("presentation pipeline steps = %d, want 5", len(pipeline.Steps))
	}
	last := pipeline.Steps[len(pipeline.Steps)-1]
	if last.Name != "PPT报告" {
		t.Fatalf("last step = %q, want PPT报告", last.Name)
	}
	for _, want := range []string{
		".pptx",
		"python-pptx",
		"不要停留在 Markdown",
		"PPTX 真实路径",
		"use_skill ppt-art-director",
		"base-ppt",
		"PptxGenJS",
		"complete its bundled upstream Strategist/Executor",
		"visual-QA",
		"Editable true/false",
		"Never invent facts",
	} {
		if !strings.Contains(last.Prompt, want) {
			t.Fatalf("PPT step prompt should contain %q", want)
		}
	}
	for _, tool := range []string{"persistent_shell", "edit_file", "list_skills", "use_skill", "convert", "vision"} {
		if !containsString(last.Tools, tool) {
			t.Fatalf("PPT step should allow %s", tool)
		}
	}
	if last.MaxIter != 80 {
		t.Fatalf("PPT step iteration budget = %d, want 80", last.MaxIter)
	}
	if containsString(last.Tools, "pptmaster_generate") || strings.Contains(last.Prompt, "pptmaster_generate") {
		t.Fatal("PPT step must not expose the removed Go PPT generator")
	}
}

func TestBuildAEWorkflowPlanMessageIncludesStepsAndDeliverable(t *testing.T) {
	msg := buildAEWorkflowPlanMessage("数据分析师", dataAnalysisPresentationPipelineName, []PipelineStep{
		{Name: "数据加载"},
		{Name: "探索分析"},
		{Name: "可视化"},
		{Name: "输出报告"},
		{Name: "PPT报告"},
	})
	for _, want := range []string{
		"已接收任务",
		"数据分析师",
		dataAnalysisPresentationPipelineName,
		"数据加载 → 探索分析 → 可视化 → 输出报告 → PPT报告",
		"Markdown 数据分析报告 + 图表 + PPTX",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("workflow plan should contain %q, got %q", want, msg)
		}
	}
}

func TestInvestmentWorkflowPlanIsUserFacingAndAdaptive(t *testing.T) {
	msg := buildAEWorkflowPlanMessage("投资顾问", "investment-advisory", []PipelineStep{
		{Name: "自主判断与证据采集", Tools: []string{"stock_facts", "web_search"}},
		{Name: "分类型研究与研判", NoTools: true},
		{Name: "结果交付", Tools: []string{"write_file"}},
	})
	for _, want := range []string{"投资顾问", "识别问题类型", "只核验本题必要", "直接回答", "明确要求保存"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("adaptive plan missing %q: %s", want, msg)
		}
	}
	for _, forbidden := range []string{"Pipeline：", "填充槽位", "权限边界", "stock_facts", "执行流程："} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("adaptive plan leaked implementation detail %q: %s", forbidden, msg)
		}
	}
}

func TestApplyBuiltInAEPolicyAppendsDataAnalystReportRule(t *testing.T) {
	cfg := &AIEmployeeConfig{
		ID:            "data-analyst",
		SystemMessage: "你是数据分析师。已有自定义要求。",
	}
	def := &AIEmployeeConfig{
		ID:            "data-analyst",
		SystemMessage: "默认规则：必须包含产出文件清单。",
	}
	applyBuiltInAEPolicy(cfg, def)
	if !strings.Contains(cfg.SystemMessage, "已有自定义要求") {
		t.Fatal("existing data analyst system message should be preserved")
	}
	if !strings.Contains(cfg.SystemMessage, "产出文件清单") {
		t.Fatal("built-in data analyst report rule should be appended")
	}
	if !strings.Contains(cfg.SystemMessage, "PPT") || !strings.Contains(cfg.SystemMessage, ".pptx") {
		t.Fatal("built-in data analyst presentation rule should be appended")
	}

	oldCfg := &AIEmployeeConfig{
		ID:            "data-analyst",
		SystemMessage: "已有完整报告规则，包含产出文件清单。",
	}
	applyBuiltInAEPolicy(oldCfg, def)
	if !strings.Contains(oldCfg.SystemMessage, "PPT") || !strings.Contains(oldCfg.SystemMessage, ".pptx") {
		t.Fatal("old data analyst policy with report inventory should still receive presentation rule")
	}
}
