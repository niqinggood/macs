package pipeline

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
)

// PipelineSourceContract turns source acquisition from a prompt convention
// into an executable boundary.
type PipelineSourceContract struct {
	Required              bool   `json:"required,omitempty" yaml:"required,omitempty"`
	WhenReferenced        bool   `json:"when_referenced,omitempty" yaml:"when_referenced,omitempty"`
	FrozenPath            string `json:"frozen_path,omitempty" yaml:"frozen_path,omitempty"`
	MinBytes              int    `json:"min_bytes,omitempty" yaml:"min_bytes,omitempty"`
	RequireDownstreamRead bool   `json:"require_downstream_read,omitempty" yaml:"require_downstream_read,omitempty"`
}

// PipelineArtifactContract prevents a later step from accepting a stale file
// left by a previous run.
type PipelineArtifactContract struct {
	Paths                  []string   `json:"paths,omitempty" yaml:"paths,omitempty"`
	MinBytes               int        `json:"min_bytes,omitempty" yaml:"min_bytes,omitempty"`
	RequireRead            bool       `json:"require_read,omitempty" yaml:"require_read,omitempty"`
	TextProvenance         bool       `json:"text_provenance,omitempty" yaml:"text_provenance,omitempty"`
	RejectPlaceholders     bool       `json:"reject_placeholders,omitempty" yaml:"reject_placeholders,omitempty"`
	VerifyOnly             bool       `json:"verify_only,omitempty" yaml:"verify_only,omitempty"`
	MinSlides              int        `json:"min_slides,omitempty" yaml:"min_slides,omitempty"`
	MinVisibleChars        int        `json:"min_visible_chars,omitempty" yaml:"min_visible_chars,omitempty"`
	RequiredText           []string   `json:"required_text,omitempty" yaml:"required_text,omitempty"`
	RequiredAnyText        [][]string `json:"required_any_text,omitempty" yaml:"required_any_text,omitempty"`
	RequireCurrencyAmount  bool       `json:"require_currency_amount,omitempty" yaml:"require_currency_amount,omitempty"`
	RequirePercentage      bool       `json:"require_percentage,omitempty" yaml:"require_percentage,omitempty"`
	MaxPlaceholderMentions int        `json:"max_placeholder_mentions,omitempty" yaml:"max_placeholder_mentions,omitempty"`
	MaterializeOutput      bool       `json:"materialize_output,omitempty" yaml:"materialize_output,omitempty"`
}

type PipelineSourceSnapshot struct {
	Path                  string    `json:"path"`
	SHA256                string    `json:"sha256"`
	Bytes                 int       `json:"bytes"`
	CapturedAt            time.Time `json:"captured_at"`
	RequireDownstreamRead bool      `json:"require_downstream_read,omitempty"`
}

var (
	urlSourcePattern    = regexp.MustCompile(`(?i)https?://[^\s<>'"]+`)
	pathSourcePattern   = regexp.MustCompile(`(?i)(?:\b[A-Z]:[\\/][^\s]+|(?:^|\s)(?:\.?\.?[\\/])[^\s]+|[^\s]+\.(?:md|txt|html?|pdf|docx?|xlsx?|csv|json|png|jpe?g|webp)\b)`)
	placeholderPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\[(?:\s*)(?:待补充|待填写|待核验|此处(?:粘贴|填写|补充)|TODO|TBD|PLACEHOLDER)[^\]]*\]`),
		regexp.MustCompile(`(?i)【(?:\s*)(?:待补充|待填写|待核验|TODO|TBD|PLACEHOLDER)[^】]*】`),
		regexp.MustCompile(`(?i)\{\{[^{}]*(?:TODO|TBD|PLACEHOLDER|待补充|待填写)[^{}]*\}\}`),
		regexp.MustCompile(`(?i)<[^>]*placeholder[^>]*>`),
	}
	chatroomSourceDescriptionPattern = regexp.MustCompile(`(?i)(?:chatroom|nova|硅基聊天室|聊天室)[^。；;\r\n]{0,100}?(?:帖子|原帖|文章|内容)`)
	presentationCurrencyPattern      = regexp.MustCompile(`(?i)(?:[¥￥$€£]\s*\d[\d,.]*(?:\s*(?:万|亿|千|k|m|mn|million|billion))?(?:\s*(?:元|美元|人民币|rmb|cny|usd|eur|gbp))?|\d[\d,.]*\s*(?:万|亿)\s*(?:元|美元|人民币)?|\d[\d,.]*\s*(?:rmb|cny|usd|eur|gbp))`)
	presentationPercentPattern       = regexp.MustCompile(`\d+(?:\.\d+)?\s*%`)
	presentationPlaceholderPattern   = regexp.MustCompile(`(?i)(?:\[\s*(?:待补|待填写|待核验|tbd|todo|placeholder)[^\]]*\]|待补充可核验信息|来源等级|\bunknown\b|未知)`)
)

// ContainsSourceLocator reports whether text contains a URL or a supported
// local/document path.
func ContainsSourceLocator(text string) bool {
	return urlSourcePattern.MatchString(text) || pathSourcePattern.MatchString(text)
}

// InputReferencesSource distinguishes an existing source from a topic-only
// creation request. Host-only context blocks are excluded first.
func InputReferencesSource(input, contextHeader, contextPathMarker string) bool {
	text := strings.TrimSpace(StripContextBlock(input, contextHeader, contextPathMarker))
	if text == "" {
		return false
	}
	if ContainsSourceLocator(text) {
		return true
	}
	lower := strings.ToLower(text)
	sourceNouns := `(?:chatroom|聊天室|群聊|原帖|帖子|原文|文章|内容|材料|素材|附件|链接|网页|报告|文档|文件|草稿|截图|录音|视频)`
	referenceSignals := []string{
		`(?i)(?:基于|根据|按照|使用|读取|打开|提取|改写|重写|总结|转成|来自|从|这份|这篇|这个|那个|上面|里面).{0,24}` + sourceNouns,
		`(?i)` + sourceNouns + `.{0,16}(?:里|中|上|内).{0,20}(?:有|的|那|这)`,
		`(?i)(?:有|找到|提到).{0,24}` + sourceNouns,
		`(?i)(?:based\s+on|from|read|extract|rewrite|summari[sz]e|use).{0,40}` + sourceNouns,
	}
	for _, pattern := range referenceSignals {
		if regexp.MustCompile(pattern).MatchString(lower) {
			return true
		}
	}
	return false
}

func SourceBundleLooksMissing(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if containsAny(lower, []string{
		"source_status: missing", "source_status=missing", "原帖未获取", "未找到原帖",
		"等待用户提供原文", "请用户粘贴原文", "placeholder source", "skeleton source",
	}) {
		return true
	}
	placeholderCount := 0
	for _, pattern := range placeholderPatterns {
		placeholderCount += len(pattern.FindAllStringIndex(text, -1))
	}
	return placeholderCount >= 2
}

func SourceBundleHasLocator(text string) bool {
	if ContainsSourceLocator(text) {
		return true
	}
	return containsAny(strings.ToLower(text), []string{"source_url", "source locator", "source_locator", "来源:", "来源：", `"source"`})
}

func ReferencedSourceKind(input, contextHeader, contextPathMarker string) (kind, locator string) {
	input = StripContextBlock(input, contextHeader, contextPathMarker)
	if match := urlSourcePattern.FindString(input); match != "" {
		return "url", strings.TrimSpace(match)
	}
	if ExtractChatroomSourceDescription(input) != "" {
		return "described", ""
	}
	if match := pathSourcePattern.FindString(input); match != "" {
		return "local", strings.TrimSpace(match)
	}
	if InputReferencesSource(input, contextHeader, contextPathMarker) {
		return "described", ""
	}
	return "referenced", ""
}

func ExtractContextPath(input, contextPathMarker string) string {
	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, contextPathMarker) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, contextPathMarker))
		}
	}
	return ""
}

func StripContextBlock(input, contextHeader, contextPathMarker string) string {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	if index := strings.Index(normalized, "\n"+contextHeader); index >= 0 {
		return strings.TrimSpace(normalized[:index])
	}
	if strings.HasPrefix(strings.TrimSpace(normalized), contextHeader) {
		return ""
	}
	kept := make([]string, 0, strings.Count(normalized, "\n")+1)
	for _, line := range strings.Split(normalized, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), contextPathMarker) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func ExtractChatroomSourceDescription(input string) string {
	match := strings.TrimSpace(chatroomSourceDescriptionPattern.FindString(input))
	return strings.Trim(match, " ，,：:")
}

func ToolCallMatchesURL(params, result, sourceURL string) bool {
	if strings.Contains(strings.ToLower(params), strings.ToLower(sourceURL)) {
		return true
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return strings.Contains(strings.ToLower(params), host) && strings.Contains(strings.ToLower(result), host)
}

func ToolCallMatchesLocalPath(params, sourcePath, frozenPath string) bool {
	var values map[string]interface{}
	if json.Unmarshal([]byte(params), &values) != nil {
		return false
	}
	path, _ := values["path"].(string)
	if path == "" {
		path, _ = values["file"].(string)
	}
	return PathsEquivalent(path, sourcePath) && !PathsEquivalent(path, frozenPath)
}

func PathsEquivalent(first, second string) bool {
	first = NormalizePath(first)
	second = NormalizePath(second)
	return first == second || strings.HasSuffix(first, "/"+second) || strings.HasSuffix(second, "/"+first)
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	for strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	path = strings.TrimLeft(path, "/")
	return strings.ToLower(path)
}

func WechatRenderableImageCount(text string, safeInlineRasterURL func(string) bool) int {
	document, err := nethtml.Parse(strings.NewReader(text))
	if err != nil {
		return 0
	}
	count := 0
	var walk func(*nethtml.Node)
	walk = func(node *nethtml.Node) {
		if node.Type == nethtml.ElementNode && strings.EqualFold(node.Data, "img") {
			source := ""
			for _, attribute := range node.Attr {
				if strings.EqualFold(attribute.Key, "src") {
					source = strings.TrimSpace(attribute.Val)
					break
				}
			}
			inlineSafe := safeInlineRasterURL != nil && safeInlineRasterURL(source)
			parsed, parseErr := url.Parse(source)
			if inlineSafe || (parseErr == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))) {
				count++
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return count
}

var wechatHTMLAllowedTags = map[string]bool{
	"html": true, "head": true, "body": true,
	"section": true, "article": true, "div": true, "p": true, "span": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true,
	"blockquote": true, "ul": true, "ol": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hr": true, "br": true, "img": true, "a": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "th": true, "td": true,
	"figure": true, "figcaption": true, "small": true, "sup": true, "sub": true,
	"code": true, "pre": true,
}

var wechatHTMLAllowedAttrs = map[string]bool{
	"style": true, "class": true, "id": true, "title": true, "lang": true, "dir": true, "role": true,
	"href": true, "src": true, "alt": true, "width": true, "height": true, "target": true, "rel": true, "name": true,
	"colspan": true, "rowspan": true, "align": true, "valign": true, "cellpadding": true, "cellspacing": true, "border": true,
	"start": true, "type": true, "value": true, "scope": true, "cite": true,
}

func ValidateWechatHTMLDocument(text, executionMarker, sourceMarker string, safeInlineRasterURL func(string) bool) error {
	document, err := nethtml.Parse(strings.NewReader(text))
	if err != nil {
		return fmt.Errorf("HTML parse error: %w", err)
	}
	executionComments := 0
	sourceComments := 0
	contentElements := 0
	var walk func(*nethtml.Node, bool) error
	walk = func(node *nethtml.Node, rawContext bool) error {
		if node.Type == nethtml.ElementNode {
			tag := strings.ToLower(node.Data)
			if !wechatHTMLAllowedTags[tag] {
				return fmt.Errorf("tag <%s> is not allowed", tag)
			}
			if tag != "html" && tag != "head" && tag != "body" {
				contentElements++
			}
			rawContext = rawContext || tag == "textarea" || tag == "title" || tag == "code" || tag == "pre"
			for _, attribute := range node.Attr {
				name := strings.ToLower(attribute.Key)
				if attribute.Namespace != "" || strings.HasPrefix(name, "on") || name == "srcdoc" {
					return fmt.Errorf("attribute %s is not allowed on <%s>", name, tag)
				}
				if !wechatHTMLAllowedAttrs[name] && !strings.HasPrefix(name, "aria-") && !strings.HasPrefix(name, "data-") {
					return fmt.Errorf("attribute %s is not in the allowlist", name)
				}
				if (executionMarker != "" && strings.Contains(attribute.Val, strings.TrimPrefix(executionMarker, "OPENKITTY_EXECUTION_ID: "))) ||
					(sourceMarker != "" && strings.Contains(strings.ToLower(attribute.Val), strings.ToLower(strings.TrimPrefix(sourceMarker, "OPENKITTY_SOURCE_SHA256: ")))) {
					return fmt.Errorf("provenance must not appear in attributes")
				}
				if isHTMLURLAttribute(name) && unsafeHTMLURL(attribute.Val, safeInlineRasterURL) {
					return fmt.Errorf("unsafe URL protocol in %s", name)
				}
				if name == "style" && unsafeInlineCSS(attribute.Val, safeInlineRasterURL) {
					return fmt.Errorf("unsafe inline CSS")
				}
			}
		}
		if node.Type == nethtml.CommentNode {
			comment := strings.TrimSpace(node.Data)
			switch comment {
			case executionMarker:
				if executionMarker != "" {
					if rawContext {
						return fmt.Errorf("execution provenance is inside a raw/visible context")
					}
					executionComments++
				}
			case sourceMarker:
				if sourceMarker != "" {
					if rawContext {
						return fmt.Errorf("source provenance is inside a raw/visible context")
					}
					sourceComments++
				}
			default:
				if provenanceValueAppears(comment, executionMarker, sourceMarker) {
					return fmt.Errorf("malformed provenance comment")
				}
			}
		}
		if node.Type == nethtml.TextNode && provenanceValueAppears(node.Data, executionMarker, sourceMarker) {
			return fmt.Errorf("provenance appears as visible/raw text")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child, rawContext); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document, false); err != nil {
		return err
	}
	if contentElements == 0 {
		return fmt.Errorf("document has no allowed content elements")
	}
	if executionMarker != "" && executionComments != 1 {
		return fmt.Errorf("execution provenance must be exactly one real comment node")
	}
	if sourceMarker != "" && sourceComments != 1 {
		return fmt.Errorf("source provenance must be exactly one real comment node")
	}
	return nil
}

func ValidateMarkdownProvenance(text, executionMarker, sourceMarker string) error {
	wanted := make(map[string]*int)
	executionCount := 0
	sourceCount := 0
	if executionMarker != "" {
		wanted["<!-- "+executionMarker+" -->"] = &executionCount
	}
	if sourceMarker != "" {
		wanted["<!-- "+sourceMarker+" -->"] = &sourceCount
	}
	inFence := false
	var remaining []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if counter, ok := wanted[trimmed]; ok {
			if inFence {
				return fmt.Errorf("provenance comment is inside a code fence")
			}
			(*counter)++
			continue
		}
		remaining = append(remaining, line)
	}
	if executionMarker != "" && executionCount != 1 {
		return fmt.Errorf("execution provenance must be exactly one standalone comment")
	}
	if sourceMarker != "" && sourceCount != 1 {
		return fmt.Errorf("source provenance must be exactly one standalone comment")
	}
	if provenanceValueAppears(strings.Join(remaining, "\n"), executionMarker, sourceMarker) {
		return fmt.Errorf("provenance appears outside its invisible comments")
	}
	return nil
}

func PublishableTextHasPlaceholders(text string) bool {
	for _, pattern := range placeholderPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// ValidatePresentationArtifactContent applies the deterministic, extracted
// content portion of a PPTX artifact contract. File parsing stays with the
// host's content tool boundary.
func ValidatePresentationArtifactContent(displayPath string, contract *PipelineArtifactContract, slideCount int, visible string) error {
	if contract == nil {
		return nil
	}
	if contract.MinSlides > 0 && slideCount < contract.MinSlides {
		return fmt.Errorf("current-run presentation %s has %d slides, requires at least %d", displayPath, slideCount, contract.MinSlides)
	}
	visibleCharacters := utf8.RuneCountInString(visible)
	if contract.MinVisibleChars > 0 && visibleCharacters < contract.MinVisibleChars {
		return fmt.Errorf("current-run presentation %s has %d visible characters, requires at least %d", displayPath, visibleCharacters, contract.MinVisibleChars)
	}
	lowerVisible := strings.ToLower(visible)
	for _, required := range contract.RequiredText {
		required = strings.TrimSpace(required)
		if required != "" && !strings.Contains(lowerVisible, strings.ToLower(required)) {
			return fmt.Errorf("current-run presentation %s is missing required audience-facing content: %s", displayPath, required)
		}
	}
	for _, alternatives := range contract.RequiredAnyText {
		matched := false
		labels := make([]string, 0, len(alternatives))
		for _, alternative := range alternatives {
			alternative = strings.TrimSpace(alternative)
			if alternative == "" {
				continue
			}
			labels = append(labels, alternative)
			if strings.Contains(lowerVisible, strings.ToLower(alternative)) {
				matched = true
			}
		}
		if len(labels) > 0 && !matched {
			return fmt.Errorf("current-run presentation %s is missing required audience-facing concept (accepted expressions: %s)", displayPath, strings.Join(labels, " | "))
		}
	}
	if contract.RequireCurrencyAmount && !presentationCurrencyPattern.MatchString(visible) {
		return fmt.Errorf("current-run presentation %s is missing a concrete currency amount or range", displayPath)
	}
	if contract.RequirePercentage && !presentationPercentPattern.MatchString(visible) {
		return fmt.Errorf("current-run presentation %s is missing a concrete percentage", displayPath)
	}
	if contract.MaxPlaceholderMentions > 0 {
		count := len(presentationPlaceholderPattern.FindAllString(visible, -1))
		if count > contract.MaxPlaceholderMentions {
			return fmt.Errorf("current-run presentation %s contains %d placeholder/unknown mentions, maximum is %d", displayPath, count, contract.MaxPlaceholderMentions)
		}
	}
	return nil
}

func provenanceValueAppears(value, executionMarker, sourceMarker string) bool {
	if executionMarker != "" && (strings.Contains(value, executionMarker) || provenanceRawValueAppearsStandalone(value, strings.TrimPrefix(executionMarker, "OPENKITTY_EXECUTION_ID: "))) {
		return true
	}
	return sourceMarker != "" && (strings.Contains(strings.ToLower(value), strings.ToLower(sourceMarker)) || provenanceRawValueAppearsStandalone(value, strings.TrimPrefix(sourceMarker, "OPENKITTY_SOURCE_SHA256: ")))
}

func provenanceRawValueAppearsStandalone(value, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	for _, field := range strings.Fields(value) {
		field = strings.Trim(field, " \t\r\n`'\"[](){}<>.,;:!?=，。；：！？（）【】《》")
		if strings.EqualFold(field, raw) {
			return true
		}
	}
	return false
}

func isHTMLURLAttribute(name string) bool {
	return name == "href" || name == "src" || name == "cite"
}

func unsafeHTMLURL(value string, safeInlineRasterURL func(string) bool) bool {
	if safeInlineRasterURL != nil && safeInlineRasterURL(value) {
		return false
	}
	normalized := strings.Map(func(current rune) rune {
		if current <= ' ' || current == 0x7f {
			return -1
		}
		return current
	}, strings.ToLower(strings.TrimSpace(value)))
	parsed, err := url.Parse(normalized)
	if err != nil {
		return true
	}
	return parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https"
}

func unsafeInlineCSS(value string, safeInlineRasterURL func(string) bool) bool {
	lower := strings.ToLower(value)
	lower = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(lower, "")
	compact := strings.Map(func(current rune) rune {
		if current <= ' ' || current == '\'' || current == '"' {
			return -1
		}
		return current
	}, lower)
	if strings.Contains(compact, "@import") || strings.Contains(compact, "expression(") || strings.Contains(lower, `\`) {
		return true
	}
	for _, match := range regexp.MustCompile(`(?i)url\(([^)]*)\)`).FindAllStringSubmatch(lower, -1) {
		if len(match) == 2 && unsafeHTMLURL(strings.Trim(match[1], " \t\r\n\"'"), safeInlineRasterURL) {
			return true
		}
	}
	return false
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
