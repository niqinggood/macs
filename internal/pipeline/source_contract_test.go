package pipeline

import (
	"strings"
	"testing"
)

const (
	testContextHeader = "## 同会话候选上下文（宿主已冻结）"
	testContextPath   = "OPENKITTY_AE_CONTEXT_PATH:"
)

func TestInputReferencesSourceDistinguishesSourcesFromTopics(t *testing.T) {
	for _, input := range []string{
		"chatroom 有一个关于星河缓存转型的帖子，提取出来改成公众号文章",
		"请基于 https://example.com/post/1 改写",
		`读取 E:\materials\draft.md 后整理`,
	} {
		if !InputReferencesSource(input, testContextHeader, testContextPath) {
			t.Fatalf("expected source reference: %q", input)
		}
	}
	for _, input := range []string{"原创一篇公众号文章", "讲讲如何写好文章", "整理一篇公众号文章"} {
		if InputReferencesSource(input, testContextHeader, testContextPath) {
			t.Fatalf("topic-only request became a source reference: %q", input)
		}
	}
}

func TestReferencedSourceKindPrefersDescriptionOverOutputPath(t *testing.T) {
	input := `chatroom 有一个关于百度掉队的帖子，请输出到 E:\out\.openkitty\storage\article.md`
	kind, locator := ReferencedSourceKind(input, testContextHeader, testContextPath)
	if kind != "described" || locator != "" {
		t.Fatalf("kind=%q locator=%q", kind, locator)
	}
	if description := ExtractChatroomSourceDescription(input); description != "chatroom 有一个关于百度掉队的帖子" {
		t.Fatalf("description=%q", description)
	}
}

func TestSourceBundleAndToolEvidenceHelpers(t *testing.T) {
	if !SourceBundleLooksMissing("[待补充来源]\n[TODO 正文]") {
		t.Fatal("placeholder source bundle was accepted")
	}
	if !SourceBundleHasLocator(`{"source_url":"https://example.com/a"}`) {
		t.Fatal("source locator was not detected")
	}
	if !ToolCallMatchesURL(`{"url":"https://example.com/a"}`, `{"ok":true}`, "https://example.com/a") {
		t.Fatal("matching URL call was rejected")
	}
	if !ToolCallMatchesLocalPath(`{"path":"E:\\docs\\source.md"}`, `E:\docs\source.md`, ".openkitty/storage/source.json") {
		t.Fatal("matching local path call was rejected")
	}
}

func TestMarkdownAndHTMLProvenanceValidation(t *testing.T) {
	executionMarker := "OPENKITTY_EXECUTION_ID: run-1"
	sourceMarker := "OPENKITTY_SOURCE_SHA256: " + strings.Repeat("b", 64)
	markdown := "<!-- " + executionMarker + " -->\n<!-- " + sourceMarker + " -->\n# Result"
	if err := ValidateMarkdownProvenance(markdown, executionMarker, sourceMarker); err != nil {
		t.Fatalf("valid Markdown provenance failed: %v", err)
	}
	if err := ValidateMarkdownProvenance(markdown+"\nVisible "+executionMarker, executionMarker, sourceMarker); err == nil {
		t.Fatal("visible Markdown provenance was accepted")
	}

	provenance := "<!-- " + executionMarker + " --><!-- " + sourceMarker + " -->"
	validHTML := provenance + `<section><p>正文</p><img src="https://example.com/a.png" alt="配图"></section>`
	if err := ValidateWechatHTMLDocument(validHTML, executionMarker, sourceMarker, func(string) bool { return false }); err != nil {
		t.Fatalf("valid HTML failed: %v", err)
	}
	unsafeHTML := provenance + `<section><a href="javascript:alert(1)">x</a></section>`
	if err := ValidateWechatHTMLDocument(unsafeHTML, executionMarker, sourceMarker, func(string) bool { return false }); err == nil {
		t.Fatal("unsafe HTML URL was accepted")
	}
}

func TestPublishablePlaceholderDetection(t *testing.T) {
	if !PublishableTextHasPlaceholders("正文 [待填写数据]") {
		t.Fatal("publishable placeholder was missed")
	}
	if PublishableTextHasPlaceholders("正文数据已经核验") {
		t.Fatal("clean publishable text was rejected")
	}
}

func TestValidatePresentationArtifactContent(t *testing.T) {
	contract := &PipelineArtifactContract{
		MinSlides: 2, MinVisibleChars: 20,
		RequiredText:           []string{"商业模式"},
		RequiredAnyText:        [][]string{{"融资", "funding"}},
		RequireCurrencyAmount:  true,
		RequirePercentage:      true,
		MaxPlaceholderMentions: 1,
	}
	valid := "商业模式与融资计划：计划融资人民币 1500 万元，研发投入占 55%。"
	if err := ValidatePresentationArtifactContent("deck.pptx", contract, 2, valid); err != nil {
		t.Fatalf("complete presentation content failed: %v", err)
	}
	if err := ValidatePresentationArtifactContent("deck.pptx", contract, 1, valid); err == nil || !strings.Contains(err.Error(), "slides") {
		t.Fatalf("slide minimum was not enforced: %v", err)
	}
	if err := ValidatePresentationArtifactContent("deck.pptx", contract, 2, valid+" 未知 unknown"); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder maximum was not enforced: %v", err)
	}
}
