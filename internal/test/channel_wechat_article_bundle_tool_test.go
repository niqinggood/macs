package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWechatArticleBundleCreatesDeterministicSafeArtifacts(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWechatArticleBundleTool(workspace)
	executionID := "pipe_wechat_20260711_010203_123456789_abcdef"
	sourceSHA := strings.Repeat("a", 64)
	body := `# 一个真实标题

这是面向手机读者的完整正文，包含**重点**、[可信来源](https://example.com/report?q=1)和行动建议。

## 核心判断

> 判断必须来自已有材料，也要区分事实与观点。

- 第一步：核验关键事实
- 第二步：给读者可执行建议

| 维度 | 建议 |
| --- | --- |
| 阅读 | 短段落 |
| 发布 | 先校对 |

![安全配图](https://example.com/cover.png)
![不安全配图](data:image/svg+xml,%3Csvg%3E)
[不安全链接](javascript:alert(1))

<script onclick="alert(1)">不得进入 HTML</script>
<style>body{display:none}</style>

最后自然提醒读者关注后续更新。`

	params := map[string]interface{}{
		"markdown":      body,
		"md_path":       ".openkitty/storage/wechat/article_" + executionID + ".md",
		"html_path":     ".openkitty/storage/wechat/article_" + executionID + ".html",
		"execution_id":  executionID,
		"source_sha256": sourceSHA,
	}
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !json.Valid([]byte(result)) || strings.TrimSpace(result) != result {
		t.Fatalf("receipt must be plain compact JSON, got %q", result)
	}
	var receipt wechatArticleBundleReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if !receipt.OK || receipt.MDPath != filepath.ToSlash(params["md_path"].(string)) || receipt.HTMLPath != filepath.ToSlash(params["html_path"].(string)) {
		t.Fatalf("unexpected receipt paths: %+v", receipt)
	}

	mdBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(receipt.MDPath)))
	if err != nil {
		t.Fatalf("read Markdown: %v", err)
	}
	htmlBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(receipt.HTMLPath)))
	if err != nil {
		t.Fatalf("read HTML: %v", err)
	}
	assertWechatBundleReceiptFile(t, "Markdown", mdBytes, receipt.MDBytes, receipt.MDSHA256)
	assertWechatBundleReceiptFile(t, "HTML", htmlBytes, receipt.HTMLBytes, receipt.HTMLSHA256)

	mdText := string(mdBytes)
	htmlText := string(htmlBytes)
	for _, marker := range []string{
		"<!-- OPENKITTY_EXECUTION_ID: " + executionID + " -->",
		"<!-- OPENKITTY_SOURCE_SHA256: " + sourceSHA + " -->",
	} {
		if strings.Count(mdText, marker) != 1 || strings.Count(htmlText, marker) != 1 {
			t.Fatalf("marker %q must occur once in both artifacts", marker)
		}
	}
	if !strings.Contains(mdText, body) {
		t.Fatal("Markdown artifact lost the submitted body")
	}
	for _, forbidden := range []string{"<script", "<style", "<meta", "<iframe", "javascript:", "data:image/svg"} {
		if strings.Contains(strings.ToLower(htmlText), forbidden) {
			t.Fatalf("HTML contains forbidden content %q: %s", forbidden, htmlText)
		}
	}
	if !strings.Contains(htmlText, "data:image/png;base64,") {
		t.Fatal("generated cover was not embedded into the self-contained HTML")
	}
	if receipt.ImageCount != 2 || receipt.Embedded != 1 || receipt.Remote != 1 || receipt.CoverPath == "" {
		t.Fatalf("unexpected image receipt: %+v", receipt)
	}
	coverBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(receipt.CoverPath)))
	if err != nil || len(coverBytes) == 0 {
		t.Fatalf("generated cover artifact is missing: bytes=%d err=%v", len(coverBytes), err)
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(htmlText) {
		t.Fatalf("HTML contains an event attribute: %s", htmlText)
	}
	for _, want := range []string{"<section style=", "<article style=", "<h1 style=", "<p style=", "max-width:100%"} {
		if !strings.Contains(htmlText, want) {
			t.Fatalf("HTML missing deterministic inline styling %q: %s", want, htmlText)
		}
	}
	if err := validateWechatHTMLDocument(htmlText, "OPENKITTY_EXECUTION_ID: "+executionID, "OPENKITTY_SOURCE_SHA256: "+sourceSHA); err != nil {
		t.Fatalf("generated HTML failed the independent artifact validator: %v", err)
	}
	if err := validateMarkdownProvenance(mdText, "OPENKITTY_EXECUTION_ID: "+executionID, "OPENKITTY_SOURCE_SHA256: "+sourceSHA); err != nil {
		t.Fatalf("generated Markdown failed provenance validation: %v", err)
	}

	secondParams := map[string]interface{}{
		"markdown":      body,
		"md_path":       ".openkitty/storage/wechat/deterministic.md",
		"html_path":     ".openkitty/storage/wechat/deterministic.html",
		"execution_id":  executionID,
		"source_sha256": sourceSHA,
	}
	if _, err := tool.Execute(context.Background(), secondParams); err != nil {
		t.Fatalf("second deterministic render: %v", err)
	}
	secondHTML, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(secondParams["html_path"].(string))))
	if err != nil {
		t.Fatalf("read second HTML: %v", err)
	}
	if string(secondHTML) != htmlText {
		t.Fatal("same Markdown and provenance did not render byte-for-byte deterministically")
	}
}

func assertWechatBundleReceiptFile(t *testing.T, label string, data []byte, wantBytes int, wantSHA string) {
	t.Helper()
	if len(data) != wantBytes {
		t.Fatalf("%s bytes = %d, receipt = %d", label, len(data), wantBytes)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != wantSHA {
		t.Fatalf("%s hash does not match receipt", label)
	}
}

func TestWechatArticleBundleNeverOverwritesEitherArtifact(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWechatArticleBundleTool(workspace)
	htmlPath := filepath.Join(workspace, ".openkitty", "storage", "existing.html")
	if err := os.MkdirAll(filepath.Dir(htmlPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(htmlPath, []byte("old execution"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 正文\n\n这是足够清晰的内容。",
		"md_path":      ".openkitty/storage/fresh.md",
		"html_path":    ".openkitty/storage/existing.html",
		"execution_id": "exec-fresh-1",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Execute() error = %v, want no-overwrite failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, ".openkitty", "storage", "fresh.md")); !os.IsNotExist(statErr) {
		t.Fatalf("Markdown should not be created when HTML preflight fails: %v", statErr)
	}
	old, readErr := os.ReadFile(htmlPath)
	if readErr != nil || string(old) != "old execution" {
		t.Fatalf("existing artifact was changed: data=%q err=%v", old, readErr)
	}
}

func TestWechatArticleBundleRejectsUnsafeInputsAndPaths(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "outside.md")
	cases := []struct {
		name   string
		change func(map[string]interface{})
		want   string
	}{
		{name: "absolute", change: func(p map[string]interface{}) { p["md_path"] = abs }, want: "relative"},
		{name: "traversal", change: func(p map[string]interface{}) { p["md_path"] = "../outside.md" }, want: "under workspace/.openkitty/storage"},
		{name: "outside storage", change: func(p map[string]interface{}) { p["md_path"] = "report.md" }, want: "under workspace/.openkitty/storage"},
		{name: "wrong md extension", change: func(p map[string]interface{}) { p["md_path"] = ".openkitty/storage/article.txt" }, want: ".md"},
		{name: "wrong html extension", change: func(p map[string]interface{}) { p["html_path"] = ".openkitty/storage/article.xml" }, want: ".html"},
		{name: "missing execution", change: func(p map[string]interface{}) { p["execution_id"] = "" }, want: "execution_id is required"},
		{name: "comment injection", change: func(p map[string]interface{}) { p["execution_id"] = "exec-->bad" }, want: "unsafe"},
		{name: "bad source hash", change: func(p map[string]interface{}) { p["source_sha256"] = "abc" }, want: "64 hexadecimal"},
		{name: "provenance in body", change: func(p map[string]interface{}) { p["markdown"] = "<!-- OPENKITTY_EXECUTION_ID: fake -->\n正文" }, want: "must not contain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewWechatArticleBundleTool(t.TempDir())
			params := map[string]interface{}{
				"markdown":     "# 正文\n\n这是一段可发布内容。",
				"md_path":      ".openkitty/storage/article.md",
				"html_path":    ".openkitty/storage/article.html",
				"execution_id": "exec-safe-1",
			}
			tc.change(params)
			_, err := tool.Execute(context.Background(), params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Execute() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestWechatArticleBundleOriginalTopicAndTraits(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWechatArticleBundleTool(workspace)
	if tool.Name() != "wechat_article_bundle" || tool.IsReadOnly() || !tool.IsNeverParallel() || tool.MaxResultSize() != 4*1024 {
		t.Fatalf("unexpected traits: name=%q readonly=%v neverParallel=%v max=%d", tool.Name(), tool.IsReadOnly(), tool.IsNeverParallel(), tool.MaxResultSize())
	}
	if !CoreTools[tool.Name()] {
		t.Fatal("wechat_article_bundle must be a core pipeline tool")
	}
	scope := tool.ScopePath(map[string]interface{}{"md_path": ".openkitty/storage/article.md"})
	if filepath.Clean(scope) != filepath.Join(workspace, ".openkitty", "storage", "article.md") {
		t.Fatalf("ScopePath() = %q", scope)
	}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 原创文章\n\n这是一篇不依赖冻结来源的原创主题文章。",
		"md_path":      ".openkitty/storage/original.md",
		"html_path":    ".openkitty/storage/original.html",
		"execution_id": "exec-original-1",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, path := range []string{"original.md", "original.html"} {
		data, readErr := os.ReadFile(filepath.Join(workspace, ".openkitty", "storage", path))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), "OPENKITTY_SOURCE_SHA256") {
			t.Fatalf("original topic invented source provenance in %s", path)
		}
	}
}

func TestWechatArticleBundleEmbedsValidatedLocalImages(t *testing.T) {
	workspace := t.TempDir()
	assetDir := filepath.Join(workspace, ".openkitty", "storage", "assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	asset, err := renderWechatArticleCoverPNG("本地数据图")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "chart.png"), asset, 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewWechatArticleBundleTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 图文测试\n\n![真实图表](.openkitty/storage/assets/chart.png)\n\n正文。",
		"md_path":      ".openkitty/storage/article/local.md",
		"html_path":    ".openkitty/storage/article/local.html",
		"execution_id": "exec-local-image-1",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var receipt wechatArticleBundleReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ImageCount != 2 || receipt.Embedded != 2 || receipt.Remote != 0 {
		t.Fatalf("local image receipt = %+v", receipt)
	}
	htmlBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(receipt.HTMLPath)))
	if err != nil {
		t.Fatal(err)
	}
	htmlText := string(htmlBytes)
	if strings.Contains(htmlText, ".openkitty/storage/assets/chart.png") || strings.Count(htmlText, "data:image/png;base64,") != 2 {
		t.Fatalf("local images were not embedded exactly once each")
	}
}

func TestWechatArticleBundleEmbedsAbsoluteImagePathInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	assetDir := filepath.Join(workspace, "assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(assetDir, "absolute-chart.png")
	pngBytes, err := renderWechatArticleCoverPNG("Absolute image")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := NewWechatArticleBundleTool(workspace).Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 绝对路径图文\n\n![图表](<" + filepath.ToSlash(assetPath) + ">)",
		"md_path":      ".openkitty/storage/absolute.md",
		"html_path":    ".openkitty/storage/absolute.html",
		"execution_id": "exec-absolute-image-1",
		"auto_cover":   false,
	})
	if err != nil {
		t.Fatalf("workspace-contained absolute image should be supported: %v", err)
	}
	var receipt wechatArticleBundleReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ImageCount != 1 || receipt.Embedded != 1 {
		t.Fatalf("absolute local image receipt = %+v", receipt)
	}
}

func TestWechatArticleBundleRejectsMissingLocalImageWithoutPublishing(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWechatArticleBundleTool(workspace)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 图文测试\n\n![缺失图片](missing.png)",
		"md_path":      ".openkitty/storage/missing.md",
		"html_path":    ".openkitty/storage/missing.html",
		"execution_id": "exec-missing-image-1",
		"auto_cover":   false,
	})
	if err == nil || !strings.Contains(err.Error(), "read WeChat image") {
		t.Fatalf("missing image error = %v", err)
	}
	for _, name := range []string{"missing.md", "missing.html"} {
		if _, statErr := os.Stat(filepath.Join(workspace, ".openkitty", "storage", name)); !os.IsNotExist(statErr) {
			t.Fatalf("failed bundle must not publish %s: %v", name, statErr)
		}
	}
}

func TestWechatArticleBundleCanDisableAutoCoverWhenRealRemoteLeadExists(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWechatArticleBundleTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown":     "# 远程封面\n\n![封面](https://example.com/lead.png)",
		"md_path":      ".openkitty/storage/remote.md",
		"html_path":    ".openkitty/storage/remote.html",
		"execution_id": "exec-remote-image-1",
		"auto_cover":   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	var receipt wechatArticleBundleReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.CoverPath != "" || receipt.ImageCount != 1 || receipt.Embedded != 0 || receipt.Remote != 1 {
		t.Fatalf("unexpected no-cover receipt: %+v", receipt)
	}
}
