package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const wechatFallbackBriefFixture = `# 素材简报

## 4. 素材明确陈述（事实陈述，可直接复述）
- 截至2026年7月15日收盘，新安股份股价为 **11.67元**。
- 一季度营业收入同比增长 **12.69%**，归母净利润同比增长 **190.61%**。
- MA20为14.53元，60日区间低点为11.11元。

## 5. 素材观点（条件式判断）
- 当前属于偏弱观察区，反转确认前优先关注下行风险。
- 盈利弹性正在释放，但持续性仍需经营现金流验证。

## 6. 事实与观点边界（须在正文保留）
- 数据可以作为事实呈现，偏弱观察属于条件式观点。
- 不支持目标价、仓位或确定性收益预测。

## 7. 版权风险
- 发布前复核财报、公告和行情来源，不大段复制原文。

## 8. 待核验关键断言（≤2 组）
1. 财务指标的单位和同比口径。
2. 半年度业绩预增公告正文。

## 9. 标题角度（3-5 个）
1. **新安股份净利润大增，股价为何仍在偏弱区？**
2. **盈利改善之后，还要看三道压力位**
3. **不急着谈反转：看懂当前三组矛盾信号**

## 10. 摘要方向（≤120 字）
盈利改善尚未转化为趋势反转，文章梳理关键数据、位置、催化和判断失效条件。`

func TestBuildWechatArticleFallbackMarkdownProducesPublishableContent(t *testing.T) {
	article := buildWechatArticleFallbackMarkdown(wechatFallbackBriefFixture)
	for _, want := range []string{"# 新安股份净利润大增", "## 先说结论", "11.67元", "190.61%", "## 写在最后", "### 备选标题", "### 封面图提示词"} {
		if !strings.Contains(article, want) {
			t.Fatalf("fallback article missing %q:\n%s", want, article)
		}
	}
	for _, forbidden := range []string{"SOURCE_STATUS", "SOURCE_FILE", "PIPELINE_STOP", "素材简报"} {
		if strings.Contains(article, forbidden) {
			t.Fatalf("internal brief marker leaked into article %q:\n%s", forbidden, article)
		}
	}
	if utf8.RuneCountInString(article) < 800 {
		t.Fatalf("fallback article too short: %d chars", utf8.RuneCountInString(article))
	}
}

func TestHostFallbackWechatArticleDeliveryCreatesValidatedBundle(t *testing.T) {
	workspace := t.TempDir()
	storageDir := filepath.Join(workspace, ".openkitty", "storage")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatalf("create storage: %v", err)
	}
	sourceData := []byte(wechatFallbackBriefFixture)
	sourcePath := filepath.Join(storageDir, "wechat_source_host_fallback.md")
	if err := os.WriteFile(sourcePath, sourceData, 0o644); err != nil {
		t.Fatalf("write frozen source: %v", err)
	}
	sourceHash := sha256.Sum256(sourceData)
	sourceSHA := hex.EncodeToString(sourceHash[:])
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}}
	execution := &PipelineExecution{
		ID: "pipe_wechat-article_host_fallback", PipelineName: "wechat-article", Status: "running", CreatedAt: time.Now(),
		Steps: []StepResult{
			{Name: aiEmployeeIntakeStepName, Status: "completed"},
			{Name: "素材准备", Status: "completed", Output: wechatFallbackBriefFixture, SourceSnapshot: &PipelineSourceSnapshot{
				Path: ".openkitty/storage/wechat_source_host_fallback.md", SHA256: sourceSHA, Bytes: len(sourceData),
			}},
			{Name: "公众号交付物", Status: "running"},
		},
	}
	step := &PipelineStep{Name: "公众号交付物", ArtifactContract: &PipelineArtifactContract{
		Paths:    []string{".openkitty/storage/wechat_article_${execution_id}.md", ".openkitty/storage/wechat_article_${execution_id}.html"},
		MinBytes: 500, TextProvenance: true, RejectPlaceholders: true,
	}}
	output, ready, err := executor.hostFallbackWechatArticleDelivery(context.Background(), execution, step, 2)
	if err != nil || !ready {
		t.Fatalf("host fallback ready=%v err=%v", ready, err)
	}
	for _, ext := range []string{".md", ".html"} {
		path := filepath.Join(workspace, ".openkitty", "storage", "wechat_article_pipe_wechat-article_host_fallback"+ext)
		info, statErr := os.Stat(path)
		if statErr != nil || info.Size() < 500 {
			t.Fatalf("fallback artifact %s missing/too small: info=%v err=%v", path, info, statErr)
		}
	}
	if _, err := executor.validatePipelineStepContracts(execution, step, 2, "根据上面内容整理公众号文案", output); err != nil {
		t.Fatalf("host fallback bundle failed pipeline contract: %v", err)
	}
	if len(execution.Steps[2].ToolCalls) != 1 || execution.Steps[2].ToolCalls[0].ToolName != "wechat_article_bundle" {
		t.Fatalf("host fallback tool receipt missing: %#v", execution.Steps[2].ToolCalls)
	}
	adopted, ready, err := executor.hostFallbackWechatArticleDelivery(context.Background(), execution, step, 2)
	if err != nil || !ready || !strings.Contains(adopted, "## 交付文件") || !strings.Contains(adopted, "## 先说结论") {
		t.Fatalf("existing valid bundle was not adopted as visible delivery: ready=%v err=%v output=%s", ready, err, adopted)
	}
	if !strings.Contains(adopted, "封面 PNG：.openkitty/storage/wechat_cover_") {
		t.Fatalf("adopted delivery lost the generated cover path: %s", adopted)
	}
	if len(execution.Steps[2].ToolCalls) != 1 {
		t.Fatalf("adopting an existing bundle must not repeat the write: %#v", execution.Steps[2].ToolCalls)
	}
}
