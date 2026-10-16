package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPPTPolishFallbackMarkdownUsesOnlyAudiencePages(t *testing.T) {
	brief := `# PPT 制作简报 — 2026-07-28 A 股盘后行情复盘

- **SLOT_ID**: step_02
- **PPT_INPUT_MODE**: source_material
- **真实保存路径**: .openkitty/storage/internal.md

## 1. 输入模式

source_material：内部制作说明。

## 5. 逐页大纲

### Page 1 — 封面
- **唯一核心观点**: 这是 A 股盘后行情复盘。
- **支持内容**: 标题与副标题。
- **图表/配图**: 不放图。
- **演讲备注**: 这是内部提示。
- **来源**: session://messages/1

### Page 2 — 核心摘要
- **唯一核心观点**: 创业板领跌，权重相对抗跌。
- **支持内容**: 上证 -1.16%；沪深 300 -2.83%；创业板 -7.35%。
- **图表/配图**: 指标卡。
- **演讲备注**: 先讲数据。

### Page 3 — 风格分化
- **唯一核心观点**: 成长和权重出现明显分化。
- **支持内容**: 创业板与沪深 300 跌幅差 4.52 个百分点。

## 9. 视觉规范（落到 Step 3）

- 深蓝标题。

## 10. 放宽路径（结果不足时按此顺序逐项放宽）

1. 页数区间。

## 11. 交付物预告

- **下游不提前执行**: Step 3。
`
	got, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatalf("build fallback audience markdown: %v", err)
	}
	for _, want := range []string{
		"# 2026-07-28 A 股盘后行情复盘",
		"## 核心摘要",
		"创业板领跌，权重相对抗跌。",
		"上证 -1.16%",
		"## 风格分化",
		"4.52 个百分点",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("audience markdown missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"SLOT_ID",
		"PPT_INPUT_MODE",
		".openkitty/storage",
		"输入模式",
		"图表/配图",
		"演讲备注",
		"视觉规范",
		"放宽路径",
		"交付物预告",
		"下游不提前执行",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("audience markdown leaked %q:\n%s", unwanted, got)
		}
	}
}

func TestBuildPPTPolishFallbackMarkdownRejectsMissingAudiencePlan(t *testing.T) {
	brief := "# PPT 制作简报\n\n## 视觉规范\n\n- 仅有内部制作说明。\n"
	if _, err := buildPPTPolishFallbackMarkdown(brief); err == nil {
		t.Fatal("production brief without Page sections must not be rendered directly")
	}
}

func TestBuildPPTPolishFallbackMarkdownAcceptsCompactPageHeadingsAndNestedSupport(t *testing.T) {
	brief := `# FDE 普通分享 PPT · 制作简报

### P1. 封面 — FDE 是什么

- **唯一核心观点**：FDE 负责把企业软件落到真实业务流程。
- **支持内容**
  - Forward Deployed Engineer
  - 前线部署工程师

### P2. 为什么需要 FDE

- **唯一核心观点**：企业软件交付的最后一公里通常发生在客户现场。
- **支持内容**
  - 理解业务问题
  - 打通系统与数据

### P3. 典型工作

- **唯一核心观点**：FDE 同时连接客户、产品与工程团队。
- **支持内容**
  - 需求澄清
  - 方案落地

### P4. 小结

- **唯一核心观点**：FDE 让软件真正被用起来。
- **支持内容**
  - 技术
  - 业务
  - 沟通
`
	got, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatalf("build compact-heading fallback: %v", err)
	}
	for _, want := range []string{
		"# FDE 普通分享",
		"## 为什么需要 FDE",
		"企业软件交付的最后一公里",
		"- 理解业务问题",
		"## 典型工作",
		"## 小结",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact-heading fallback missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## 封面") {
		t.Fatalf("cover page must not be duplicated in fallback body:\n%s", got)
	}
}

func TestBuildPPTPolishFallbackMarkdownRemovesPresentationDirectionsFromSupport(t *testing.T) {
	brief := `# FDE 分享 · 制作简报

### P1 · 封面
- **唯一核心观点**：认识 FDE。
- **支持内容**：标题与副标题

### P2 · 为什么出现
- **唯一核心观点**：FDE 填补技术能力与客户采纳之间的鸿沟。
- **支持内容**：3 行短句：模型能力 ↑↑、采纳速度 ↓、FDE = 桥梁；右下小字注"Bob McGrew 观点"

### P3 · 工作方式
- **唯一核心观点**：现场工作形成反馈闭环。
- **支持内容**：四象限或四步时间轴，每步 1 行说明
  - 嵌入式：与客户业务团队一起工作
  - 共建：把需求变成可运行用例
  - 三个关键词 chip：嵌入式交付 · 业务成果 · 现场反馈
`
	markdown, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatalf("build direction-clean fallback: %v", err)
	}
	for _, expected := range []string{
		"- 模型能力 ↑↑、采纳速度 ↓、FDE = 桥梁",
		"- Bob McGrew 观点",
		"- 嵌入式：与客户业务团队一起工作",
		"- 嵌入式交付 · 业务成果 · 现场反馈",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("audience content missing after direction cleanup %q:\n%s", expected, markdown)
		}
	}
	for _, forbidden := range []string{"3 行短句", "右下小字", "四象限", "每步 1 行", "三个关键词 chip"} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf("presentation direction leaked into fallback %q:\n%s", forbidden, markdown)
		}
	}
}

func TestPPTPolishAudienceContentViolations(t *testing.T) {
	safe := "# 市场复盘\n\n## 核心摘要\n\n- 指数出现分化。"
	if violations := pptPolishAudienceContentViolations(safe); len(violations) != 0 {
		t.Fatalf("safe audience content was rejected: %v", violations)
	}
	technical := "产物可以保存在 .openkitty/storage 下。"
	if violations := pptPolishAudienceContentViolations(technical); len(violations) != 0 {
		t.Fatalf("one contextual technical phrase must not be rejected: %v", violations)
	}
	unsafe := "SLOT_ID: step_02\nPPT_INPUT_MODE: source_material\n真实保存路径：.openkitty/storage/a.pptx"
	violations := pptPolishAudienceContentViolations(unsafe)
	if len(violations) < 3 {
		t.Fatalf("production metadata was not fully detected: %v", violations)
	}
	directionLeak := `3 行短句：模型能力 ↑↑
右下小字注"Bob McGrew 观点"`
	if violations := pptPolishAudienceContentViolations(directionLeak); len(violations) == 0 {
		t.Fatal("presentation direction labels were not detected")
	}
	for _, serialized := range []string{
		`{'name': 'AgentOS', 'section': '90 DAY PLAN', 'tagline': 'generic'}`,
		`{owner: "平台组", action: "完成逐页验收"}`,
	} {
		violations := pptPolishAudienceContentViolations(serialized)
		if len(violations) == 0 || !strings.Contains(strings.Join(violations, " | "), "serialized configuration/action payload") {
			t.Fatalf("serialized production dictionary was not detected: %q => %v", serialized, violations)
		}
	}
	for _, garbled := range []string{
		"鍒嗘瀽缁撴灉锛氫换鍔″凡瀹屾垚",
		"Capability â€” delivery â€™ status",
		"标题包含�替换字符",
	} {
		violations := pptPolishAudienceContentViolations(garbled)
		if len(violations) == 0 || !strings.Contains(strings.Join(violations, " | "), "likely mojibake/garbled text") {
			t.Fatalf("garbled audience text was not detected: %q => %v", garbled, violations)
		}
	}
}

func TestPPTPolishUserVisiblePath(t *testing.T) {
	workspace := filepath.Join("srv", "tenant", "user")
	target := filepath.Join(workspace, ".openkitty", "storage", "deck.pptx")
	if got := pptPolishUserVisiblePath(target, workspace); got != ".openkitty/storage/deck.pptx" {
		t.Fatalf("unexpected visible path: %q", got)
	}
}

func TestSelectPPTPolishFallbackThemeHonorsExplicitGoogleStyle(t *testing.T) {
	for _, input := range []string{
		"请做成谷歌风格的清爽白底 PPT",
		"Use a Google-style product presentation.",
		"主题使用 google_style",
	} {
		theme := selectPPTPolishFallbackTheme(input, "")
		if theme != "google_style" {
			t.Fatalf("explicit Google style %q selected %q", input, theme)
		}
		markdown := applyPPTPolishFallbackTheme("<!-- OPENKITTY_THEME:mckinsey -->\n# Demo\n", theme)
		if !strings.Contains(markdown, "OPENKITTY_THEME:google_style") {
			t.Fatalf("theme marker was not applied: %s", markdown)
		}
	}
}

func TestBuildPPTPolishFallbackMarkdownAcceptsAudiencePlanTable(t *testing.T) {
	brief := `# FDE 科普 PPT 制作简报

**PPT_INPUT_MODE**: concept_only

## 逐页标题与唯一核心观点

| # | 页面 | 唯一核心观点 | 支持内容要点 |
|---|---|---|---|
| 1 | 封面：认识 FDE | 把通用软件落到业务现场 | 标题；副标题 |
| 2 | FDE 是什么 | 驻场、动手、闭环 | 与客户共建；反馈回产品 |
| 3 | 适用场景 | 复杂产品和复杂客户才需要 | 产品已成型；客户愿意共建 |

## 演讲备注

- 不应出现在成品中
`
	markdown, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatalf("table-form brief was rejected: %v", err)
	}
	for _, expected := range []string{
		"# FDE 科普",
		"## FDE 是什么",
		"> 驻场、动手、闭环",
		"- 与客户共建",
		"## 适用场景",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("table-form audience content missing %q:\n%s", expected, markdown)
		}
	}
	for _, forbidden := range []string{"PPT_INPUT_MODE", "演讲备注", "不应出现在成品中"} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf("production metadata leaked from table-form brief: %q\n%s", forbidden, markdown)
		}
	}
}

func TestBuildPPTPolishFallbackMarkdownAcceptsSlideHeadingsWithPerPageTables(t *testing.T) {
	brief := `# AgentOS 90 天能力增强计划 · 制作简报

## 逐页规划

### Slide 01 — 封面

| 项目 | 内容 |
| --- | --- |
| 唯一核心观点 | 这是一份 90 天执行计划 |
| 支持内容 | 从可用走向可信交付 |
| 图表 / 配图 | 极简时间轴 |

### Slide 02 — 现状与目标

| 项目 | 内容 |
| --- | --- |
| 唯一核心观点 | 四项硬指标均有明确目标 |
| 支持内容（基线 / 目标） | 任务成功率 72% → 90%；一次验收率 58% → 85% |
| 演讲备注 | 先讲差距 |

### Slide 03 — 核心诊断

| 项目 | 内容 |
| --- | --- |
| 唯一核心观点 | 根因在闭环质量而非工具数量 |
| 支持内容（四个根因） | 任务边界不清；视觉质检不完整 |
| 结论金句（右侧色块） | 能力提升的关键是闭环质量 |
`

	markdown, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatalf("slide-heading table brief was rejected: %v", err)
	}
	for _, want := range []string{
		"# AgentOS 90 天能力增强计划",
		"## 现状与目标",
		"> 四项硬指标均有明确目标",
		"- 任务成功率 72% → 90%",
		"## 核心诊断",
		"- 能力提升的关键是闭环质量",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("slide-heading table audience content missing %q:\n%s", want, markdown)
		}
	}
	for _, forbidden := range []string{"图表 / 配图", "演讲备注", "先讲差距", "极简时间轴"} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf("production direction leaked from slide-heading table (%q):\n%s", forbidden, markdown)
		}
	}
}

func TestSelectPPTPolishFallbackThemeKeepsConsultingDefault(t *testing.T) {
	if theme := selectPPTPolishFallbackTheme("整理一份普通商务分享", "技术产品介绍"); theme != "mckinsey" {
		t.Fatalf("broad business/technology wording must not infer a branded style: %q", theme)
	}
}

func TestBuildPPTPolishFallbackMarkdownMergesSeparatePerPageSupport(t *testing.T) {
	brief := `# FDE 分享制作简报

## 逐页标题与核心观点

| # | 标题 | 唯一核心观点 |
|---|---|---|
| 1 | 封面 | FDE 连接技术与业务 |
| 2 | 为什么需要 | 企业需要把通用能力放进真实流程 |
| 3 | 工作方式 | FDE 用工程闭环推动落地 |
| 4 | 适用场景 | 复杂业务更需要贴近现场 |

## 单页支持内容

**P1 封面**：普通分享，不堆术语。
**P2 鸿沟**：模型迭代快；企业流程仍有落地鸿沟。
**P3 方法**：理解问题；验证方案；推动上线；复盘沉淀。
**P4 场景**：复杂流程；跨部门协作；深度系统集成。

## 图表 / 配图建议

- P2 概念图：能力与应用之间的鸿沟。
`
	markdown, err := buildPPTPolishFallbackMarkdown(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"企业流程仍有落地鸿沟", "理解问题", "复盘沉淀", "深度系统集成"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("separate per-page support was not preserved (%q):\n%s", want, markdown)
		}
	}
	for _, forbidden := range []string{"图表 / 配图建议", "能力与应用之间的鸿沟"} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf("production direction leaked into audience markdown (%q):\n%s", forbidden, markdown)
		}
	}
}
