package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	pipelinecore "mac/internal/pipeline"
	"mac/internal/toolcontract"

	"github.com/gomarkdown/markdown"
	mdhtml "github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	wechatArticleBundleMaxMarkdownBytes   = 4 << 20
	wechatArticleBundleMaxImageBytes      = 8 << 20
	wechatArticleBundleMaxTotalImageBytes = 24 << 20
)

var (
	wechatMarkdownWindowsAngleImagePattern = regexp.MustCompile(`(?i)(!\[[^\]\r\n]*\]\()\s*<([a-z]:[\\/][^>\r\n]+)>\s*(\))`)
	wechatMarkdownWindowsBareImagePattern  = regexp.MustCompile(`(?i)(!\[[^\]\r\n]*\]\()\s*([a-z]:[\\/][^)\s\r\n]+)\s*(\))`)
)

// WechatArticleBundleTool turns one model-authored Markdown body into the two
// deterministic text artifacts needed by a WeChat article delivery. HTML is
// rendered and styled by the host; the model never has to generate it.
type WechatArticleBundleTool struct {
	workspace string
}

type wechatArticleImageError struct {
	cause error
}

func (e *wechatArticleImageError) Error() string {
	if e == nil || e.cause == nil {
		return "WeChat article image is unavailable"
	}
	return e.cause.Error()
}

func (e *wechatArticleImageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *wechatArticleImageError) ToolRecoveryDirective() toolcontract.RecoveryDirective {
	return toolcontract.RecoveryDirective{
		Code:          "wechat_image_unavailable",
		SuggestedTool: "wechat_article_bundle",
		Instruction:   "Correct or remove the invalid local Markdown image reference, then retry the same bundle once. The failed call published no partial article files, so the same output paths remain safe to reuse.",
		MaxPrompts:    1,
	}
}

func wechatBundleImageError(err error) error {
	if err == nil {
		return nil
	}
	var existing *wechatArticleImageError
	if errors.As(err, &existing) {
		return err
	}
	return &wechatArticleImageError{cause: err}
}

func NewWechatArticleBundleTool(workspace string) *WechatArticleBundleTool {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return &WechatArticleBundleTool{workspace: workspace}
}

func (t *WechatArticleBundleTool) Name() string { return "wechat_article_bundle" }

func (t *WechatArticleBundleTool) Description() string {
	return "Create a complete illustrated WeChat article bundle in one call. Submit the Markdown body once plus exact relative .md/.html paths under .openkitty/storage/. " +
		"The host atomically writes Markdown, a real cover PNG by default, and safe self-contained mobile-first HTML. Local Markdown images inside the workspace are validated and embedded; HTTP(S) images are preserved. " +
		"The receipt reports actual image counts, and missing/invalid local images fail instead of disappearing silently. The host also adds invisible execution/source provenance. " +
		"Do not include OPENKITTY provenance comments in markdown; pass execution_id and optional source_sha256 separately. Existing files are never overwritten."
}

func (t *WechatArticleBundleTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"markdown": map[string]interface{}{
				"type":        "string",
				"description": "The complete publishable Markdown article body, without OPENKITTY provenance comments.",
			},
			"md_path": map[string]interface{}{
				"type":        "string",
				"description": "Exact relative .md output path under .openkitty/storage/.",
			},
			"html_path": map[string]interface{}{
				"type":        "string",
				"description": "Exact relative .html output path under .openkitty/storage/.",
			},
			"execution_id": map[string]interface{}{
				"type":        "string",
				"description": "Current execution identifier required for invisible provenance.",
			},
			"source_sha256": map[string]interface{}{
				"type":        "string",
				"description": "Optional 64-hex SHA-256 of the frozen source. Omit for an original topic.",
			},
			"auto_cover": map[string]interface{}{
				"type":        "boolean",
				"description": "Generate and insert a real local cover PNG (default true). Set false only when the submitted Markdown already contains the intended lead image.",
			},
		},
		"required": []string{"markdown", "md_path", "html_path", "execution_id"},
	}
}

func (t *WechatArticleBundleTool) MaxResultSize() int    { return 4 * 1024 }
func (t *WechatArticleBundleTool) IsReadOnly() bool      { return false }
func (t *WechatArticleBundleTool) IsNeverParallel() bool { return true }

func (t *WechatArticleBundleTool) ScopePath(args map[string]interface{}) string {
	raw, _ := args["md_path"].(string)
	if strings.TrimSpace(raw) == "" || filepath.IsAbs(raw) {
		return ""
	}
	return filepath.Join(t.workspace, filepath.Clean(raw))
}

type wechatArticleBundleReceipt struct {
	OK           bool   `json:"ok"`
	MDPath       string `json:"md_path"`
	HTMLPath     string `json:"html_path"`
	MDBytes      int    `json:"md_bytes"`
	HTMLBytes    int    `json:"html_bytes"`
	MDSHA256     string `json:"md_sha256"`
	HTMLSHA256   string `json:"html_sha256"`
	ExecutionID  string `json:"execution_id"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	HTMLRenderer string `json:"html_renderer"`
	CoverPath    string `json:"cover_path,omitempty"`
	ImageCount   int    `json:"image_count"`
	Embedded     int    `json:"embedded_image_count"`
	Remote       int    `json:"remote_image_count"`
}

// WechatArticleBundleReceipt is the structured output returned by the bundle tool.
type WechatArticleBundleReceipt = wechatArticleBundleReceipt

func (t *WechatArticleBundleTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", errors.New("wechat_article_bundle is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	body, _ := params["markdown"].(string)
	body = strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	if body == "" {
		return "", errors.New("markdown is required")
	}
	if len(body) > wechatArticleBundleMaxMarkdownBytes {
		return "", fmt.Errorf("markdown exceeds %d bytes", wechatArticleBundleMaxMarkdownBytes)
	}
	lowerBody := strings.ToLower(body)
	if strings.Contains(lowerBody, "openkitty_execution_id") || strings.Contains(lowerBody, "openkitty_source_sha256") {
		return "", errors.New("markdown must not contain OPENKITTY provenance; pass provenance parameters separately")
	}

	executionID, _ := params["execution_id"].(string)
	executionID, err := validateWechatBundleExecutionID(executionID)
	if err != nil {
		return "", err
	}
	sourceSHA, err := validateWechatBundleSourceSHA(params)
	if err != nil {
		return "", err
	}

	mdRaw, _ := params["md_path"].(string)
	htmlRaw, _ := params["html_path"].(string)
	mdPath, mdReceiptPath, err := t.resolveOutputPath(mdRaw, ".md")
	if err != nil {
		return "", fmt.Errorf("md_path: %w", err)
	}
	htmlPath, htmlReceiptPath, err := t.resolveOutputPath(htmlRaw, ".html")
	if err != nil {
		return "", fmt.Errorf("html_path: %w", err)
	}
	if strings.EqualFold(mdPath, htmlPath) {
		return "", errors.New("md_path and html_path must be different")
	}
	autoCover := true
	if configured, ok := params["auto_cover"].(bool); ok {
		autoCover = configured
	}

	var coverPath, coverReceiptPath string
	var coverBytes []byte
	renderBody := body
	inMemoryImages := make(map[string][]byte)
	if autoCover {
		coverID := sha256.Sum256([]byte(htmlReceiptPath + "\x00" + executionID))
		coverName := "wechat_cover_" + hex.EncodeToString(coverID[:8]) + ".png"
		coverPath = filepath.Join(filepath.Dir(htmlPath), coverName)
		coverReceiptPath = filepath.ToSlash(filepath.Join(filepath.Dir(htmlReceiptPath), coverName))
		if _, statErr := os.Lstat(coverPath); statErr == nil {
			return "", errors.New("cover path already exists; use fresh execution-scoped paths")
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect cover path: %w", statErr)
		}
		coverBytes, err = renderWechatArticleCoverPNG(wechatArticleTitle(body))
		if err != nil {
			return "", fmt.Errorf("render WeChat cover: %w", err)
		}
		relativeCover, relErr := filepath.Rel(filepath.Dir(mdPath), coverPath)
		if relErr != nil {
			return "", fmt.Errorf("resolve cover reference: %w", relErr)
		}
		renderBody = insertWechatArticleCoverMarkdown(body, filepath.ToSlash(relativeCover))
		inMemoryImages[normalizedWechatBundleFileKey(coverPath)] = coverBytes
	}

	provenance := "<!-- OPENKITTY_EXECUTION_ID: " + executionID + " -->\n"
	if sourceSHA != "" {
		provenance += "<!-- OPENKITTY_SOURCE_SHA256: " + sourceSHA + " -->\n"
	}
	mdBytes := []byte(provenance + "\n" + renderBody + "\n")
	htmlRenderBody, err := prepareWechatMarkdownAbsoluteImages(renderBody, filepath.Dir(mdPath), t.workspace, inMemoryImages)
	if err != nil {
		return "", err
	}
	htmlText, imageStats, err := renderWechatArticleBundleHTML(htmlRenderBody, executionID, sourceSHA, t.workspace, filepath.Dir(mdPath), inMemoryImages)
	if err != nil {
		return "", err
	}
	htmlBytes := []byte(htmlText)

	if err := ctx.Err(); err != nil {
		return "", err
	}
	files := []wechatBundleOutputFile{{path: mdPath, data: mdBytes}, {path: htmlPath, data: htmlBytes}}
	if autoCover {
		files = append(files, wechatBundleOutputFile{path: coverPath, data: coverBytes})
	}
	if err := commitWechatBundleFiles(files); err != nil {
		return "", fmt.Errorf("commit WeChat article bundle: %w", err)
	}

	finalMD, err := os.ReadFile(mdPath)
	if err != nil {
		return "", fmt.Errorf("verify Markdown artifact: %w", err)
	}
	finalHTML, err := os.ReadFile(htmlPath)
	if err != nil {
		return "", fmt.Errorf("verify HTML artifact: %w", err)
	}
	mdHash := sha256.Sum256(finalMD)
	htmlHash := sha256.Sum256(finalHTML)
	receipt := wechatArticleBundleReceipt{
		OK:           true,
		MDPath:       mdReceiptPath,
		HTMLPath:     htmlReceiptPath,
		MDBytes:      len(finalMD),
		HTMLBytes:    len(finalHTML),
		MDSHA256:     hex.EncodeToString(mdHash[:]),
		HTMLSHA256:   hex.EncodeToString(htmlHash[:]),
		ExecutionID:  executionID,
		SourceSHA256: sourceSHA,
		HTMLRenderer: "gomarkdown/inline-wechat-v2",
		CoverPath:    coverReceiptPath,
		ImageCount:   imageStats.Total,
		Embedded:     imageStats.Embedded,
		Remote:       imageStats.Remote,
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("encode WeChat article receipt: %w", err)
	}
	return string(encoded), nil
}

func validateWechatBundleExecutionID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", errors.New("execution_id is required")
	}
	if utf8.RuneCountInString(id) > 240 || id != sanitizeWechatExecutionID(id) || strings.Contains(id, "--") {
		return "", errors.New("execution_id contains unsafe characters")
	}
	for _, r := range id {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return "", errors.New("execution_id contains unsafe characters")
		}
	}
	return id, nil
}

func validateWechatBundleSourceSHA(params map[string]interface{}) (string, error) {
	raw, exists := params["source_sha256"]
	if !exists || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", errors.New("source_sha256 must be a string")
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || len(value) != sha256.Size*2 {
		return "", errors.New("source_sha256 must be exactly 64 hexadecimal characters")
	}
	return value, nil
}

func (t *WechatArticleBundleTool) resolveOutputPath(rawPath, wantedExt string) (string, string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", "", errors.New("path is required")
	}
	if strings.IndexByte(rawPath, 0) >= 0 || filepath.IsAbs(rawPath) {
		return "", "", errors.New("path must be relative to the workspace")
	}
	cleanRelative := filepath.Clean(rawPath)
	if cleanRelative == "." || !strings.EqualFold(filepath.Ext(cleanRelative), wantedExt) {
		return "", "", fmt.Errorf("path must end in %s", wantedExt)
	}

	workspace, err := filepath.Abs(t.workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace: %w", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace links: %w", err)
	}
	kittyRoot := filepath.Join(workspace, ".openkitty")
	if err := wechatBundleEnsureRealChild(workspace, canonicalWorkspace, kittyRoot, ".openkitty"); err != nil {
		return "", "", err
	}
	canonicalKitty, err := filepath.EvalSymlinks(kittyRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve .openkitty: %w", err)
	}
	storageRoot := filepath.Join(kittyRoot, "storage")
	if err := wechatBundleEnsureRealChild(kittyRoot, canonicalKitty, storageRoot, "storage"); err != nil {
		return "", "", err
	}
	canonicalStorage, err := filepath.EvalSymlinks(storageRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage: %w", err)
	}
	if !wechatBundlePathWithin(canonicalWorkspace, canonicalStorage) {
		return "", "", errors.New("workspace/.openkitty/storage resolves outside the workspace")
	}

	absolutePath, err := filepath.Abs(filepath.Join(workspace, cleanRelative))
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}
	if !wechatBundlePathWithin(storageRoot, absolutePath) {
		return "", "", errors.New("path must stay under workspace/.openkitty/storage")
	}
	canonicalParent, err := wechatBundleCreateSafeParent(storageRoot, canonicalStorage, filepath.Dir(absolutePath))
	if err != nil {
		return "", "", err
	}
	if !wechatBundlePathWithin(canonicalStorage, filepath.Join(canonicalParent, filepath.Base(absolutePath))) {
		return "", "", errors.New("path escapes storage through a symlink or junction")
	}
	if _, err := os.Lstat(absolutePath); err == nil {
		return "", "", errors.New("path already exists; use fresh execution-scoped paths")
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("inspect path: %w", err)
	}
	return absolutePath, filepath.ToSlash(cleanRelative), nil
}

func wechatBundleEnsureRealChild(parent, canonicalParent, child, label string) error {
	if info, err := os.Lstat(child); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must be a real directory, not a link", child)
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(child, 0o700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create %s directory: %w", label, err)
		}
	} else {
		return fmt.Errorf("inspect %s directory: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(child)
	if err != nil || !wechatBundlePathWithin(canonicalParent, resolved) {
		return fmt.Errorf("%s directory resolves outside %s", label, parent)
	}
	return nil
}

func wechatBundleCreateSafeParent(storageRoot, canonicalStorage, wantedParent string) (string, error) {
	relative, err := filepath.Rel(storageRoot, wantedParent)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("output directory is outside storage")
	}
	current := storageRoot
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if part == "" || part == "." || part == ".." {
				return "", errors.New("output directory has an invalid path component")
			}
			current = filepath.Join(current, part)
			if info, statErr := os.Lstat(current); statErr == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("output directory component %s is not a real directory", current)
				}
			} else if os.IsNotExist(statErr) {
				if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil && !os.IsExist(mkdirErr) {
					return "", fmt.Errorf("create output directory: %w", mkdirErr)
				}
			} else {
				return "", fmt.Errorf("inspect output directory: %w", statErr)
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !wechatBundlePathWithin(canonicalStorage, resolved) {
				return "", errors.New("output directory escapes storage through a symlink or junction")
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !wechatBundlePathWithin(canonicalStorage, resolved) {
		return "", errors.New("output directory escapes storage through a symlink or junction")
	}
	return resolved, nil
}

func wechatBundlePathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type wechatBundleStagedFile struct {
	tempPath  string
	finalPath string
}

type wechatBundleOutputFile struct {
	path string
	data []byte
}

func stageWechatBundleFile(finalPath string, data []byte) (*wechatBundleStagedFile, error) {
	file, err := os.CreateTemp(filepath.Dir(finalPath), ".wechat-bundle-*")
	if err != nil {
		return nil, fmt.Errorf("create staging file: %w", err)
	}
	tempPath := file.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return nil, fmt.Errorf("write staging file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return nil, fmt.Errorf("sync staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("close staging file: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("secure staging file: %w", err)
	}
	return &wechatBundleStagedFile{tempPath: tempPath, finalPath: finalPath}, nil
}

func commitWechatBundleFiles(outputs []wechatBundleOutputFile) error {
	if len(outputs) == 0 {
		return errors.New("no WeChat bundle files to commit")
	}
	staged := make([]*wechatBundleStagedFile, 0, len(outputs))
	for _, output := range outputs {
		stage, err := stageWechatBundleFile(output.path, output.data)
		if err != nil {
			for _, existing := range staged {
				_ = os.Remove(existing.tempPath)
			}
			return err
		}
		staged = append(staged, stage)
	}
	defer func() {
		for _, stage := range staged {
			_ = os.Remove(stage.tempPath)
		}
	}()

	// Hard links provide atomic no-replace publication on the same filesystem.
	// If any member fails, remove only links proven to point at our staging
	// files so the three-artifact bundle is never left half-published.
	published := make([]*wechatBundleStagedFile, 0, len(staged))
	for _, stage := range staged {
		if err := os.Link(stage.tempPath, stage.finalPath); err != nil {
			for i := len(published) - 1; i >= 0; i-- {
				rollbackWechatBundleLink(published[i])
			}
			return fmt.Errorf("publish %s without overwrite: %w", filepath.Base(stage.finalPath), err)
		}
		published = append(published, stage)
	}
	return nil
}

func rollbackWechatBundleLink(stage *wechatBundleStagedFile) {
	finalInfo, finalErr := os.Stat(stage.finalPath)
	tempInfo, tempErr := os.Stat(stage.tempPath)
	if finalErr == nil && tempErr == nil && os.SameFile(finalInfo, tempInfo) {
		_ = os.Remove(stage.finalPath)
	}
}

func wechatArticleTitle(body string) string {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return truncateWechatCoverText(strings.TrimSpace(strings.TrimPrefix(trimmed, "# ")), 72)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(strings.Trim(line, "#>*_`- "))
		if trimmed != "" {
			return truncateWechatCoverText(trimmed, 72)
		}
	}
	return "微信公众号文章"
}

func truncateWechatCoverText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return value
}

func insertWechatArticleCoverMarkdown(body, coverReference string) string {
	cover := "![文章封面](" + coverReference + ")"
	return cover + "\n\n" + strings.TrimSpace(body)
}

// gomarkdown deliberately treats a raw Windows drive path as a URI scheme and
// may emit plain text instead of an image node. Keep the authored Markdown
// unchanged, but use a safe turn-local alias while rendering HTML. The alias
// bytes are still loaded from, and boundary-checked against, the real workspace.
func prepareWechatMarkdownAbsoluteImages(body, markdownDir, workspace string, inMemoryImages map[string][]byte) (string, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve absolute-image workspace: %w", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(absWorkspace)
	if err != nil {
		return "", fmt.Errorf("resolve absolute-image workspace links: %w", err)
	}
	aliases := make(map[string]string)
	var firstErr error
	rewrite := func(pattern *regexp.Regexp, input string) string {
		return pattern.ReplaceAllStringFunc(input, func(full string) string {
			if firstErr != nil {
				return full
			}
			match := pattern.FindStringSubmatch(full)
			if len(match) != 4 {
				return full
			}
			rawPath := strings.TrimSpace(match[2])
			actualPath, resolveErr := resolveWechatBundleLocalImagePath(rawPath, markdownDir, absWorkspace, canonicalWorkspace)
			if resolveErr != nil {
				firstErr = wechatBundleImageError(resolveErr)
				return full
			}
			key := normalizedWechatBundleFileKey(actualPath)
			alias := aliases[key]
			if alias == "" {
				data, readErr := os.ReadFile(actualPath)
				if readErr != nil {
					firstErr = wechatBundleImageError(fmt.Errorf("read WeChat image %s: %w", rawPath, readErr))
					return full
				}
				mimeType, validateErr := validateWechatBundleRaster(data)
				if validateErr != nil {
					firstErr = wechatBundleImageError(fmt.Errorf("invalid WeChat image %s: %w", rawPath, validateErr))
					return full
				}
				extension := ".png"
				switch mimeType {
				case "image/jpeg":
					extension = ".jpg"
				case "image/gif":
					extension = ".gif"
				case "image/webp":
					extension = ".webp"
				}
				digest := sha256.Sum256([]byte(key))
				alias = filepath.ToSlash(filepath.Join(".openkitty-inline-images", "image_"+hex.EncodeToString(digest[:8])+extension))
				aliasPath := filepath.Join(markdownDir, filepath.FromSlash(alias))
				inMemoryImages[normalizedWechatBundleFileKey(aliasPath)] = data
				aliases[key] = alias
			}
			return match[1] + alias + match[3]
		})
	}
	renderBody := rewrite(wechatMarkdownWindowsAngleImagePattern, body)
	renderBody = rewrite(wechatMarkdownWindowsBareImagePattern, renderBody)
	if firstErr != nil {
		return "", firstErr
	}
	return renderBody, nil
}

func renderWechatArticleCoverPNG(title string) ([]byte, error) {
	const width, height = 1200, 630
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		ratio := float64(y) / float64(height-1)
		row := color.RGBA{
			R: uint8(13 + 9*ratio),
			G: uint8(28 + 18*ratio),
			B: uint8(52 + 26*ratio),
			A: 255,
		}
		imagedraw.Draw(canvas, image.Rect(0, y, width, y+1), &image.Uniform{C: row}, image.Point{}, imagedraw.Src)
	}
	// Editorial accents remain deliberately neutral so the cover works across
	// business, technical and general-interest articles.
	imagedraw.Draw(canvas, image.Rect(72, 72, 84, 558), &image.Uniform{C: color.RGBA{R: 7, G: 193, B: 96, A: 255}}, image.Point{}, imagedraw.Src)
	imagedraw.Draw(canvas, image.Rect(108, 82, 1128, 84), &image.Uniform{C: color.RGBA{R: 81, G: 112, B: 148, A: 180}}, image.Point{}, imagedraw.Src)
	imagedraw.Draw(canvas, image.Rect(108, 546, 1128, 548), &image.Uniform{C: color.RGBA{R: 81, G: 112, B: 148, A: 180}}, image.Point{}, imagedraw.Src)

	titleSize := 62.0
	if utf8.RuneCountInString(title) > 30 {
		titleSize = 52
	}
	titleFace, err := loadWechatCoverFont(titleSize, true)
	if err != nil {
		return nil, err
	}
	defer closeWechatFontFace(titleFace)
	metaFace, err := loadWechatCoverFont(23, false)
	if err != nil {
		return nil, err
	}
	defer closeWechatFontFace(metaFace)

	lines := wrapWechatCoverText(titleFace, title, 930, 3)
	lineHeight := int(titleSize * 1.35)
	top := 214 - (len(lines)-1)*lineHeight/2
	for index, line := range lines {
		drawWechatCoverText(canvas, titleFace, line, 126, top+index*lineHeight, color.RGBA{R: 247, G: 250, B: 252, A: 255})
	}
	drawWechatCoverText(canvas, metaFace, "OPENKITTY · WECHAT EDITORIAL", 126, 132, color.RGBA{R: 122, G: 217, B: 176, A: 255})
	drawWechatCoverText(canvas, metaFace, "清晰表达 · 移动端阅读 · 可追溯交付", 126, 508, color.RGBA{R: 177, G: 194, B: 214, A: 255})

	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// RenderWechatArticleCoverPNG renders the deterministic fallback cover image.
func RenderWechatArticleCoverPNG(title string) ([]byte, error) {
	return renderWechatArticleCoverPNG(title)
}

func loadWechatCoverFont(size float64, bold bool) (font.Face, error) {
	var candidates []string
	if runtime.GOOS == "windows" {
		windowsDir := strings.TrimSpace(os.Getenv("WINDIR"))
		if windowsDir == "" {
			windowsDir = `C:\Windows`
		}
		fontDir := filepath.Join(windowsDir, "Fonts")
		if bold {
			candidates = append(candidates, filepath.Join(fontDir, "Dengb.ttf"), filepath.Join(fontDir, "msyhbd.ttc"), filepath.Join(fontDir, "simhei.ttf"))
		} else {
			candidates = append(candidates, filepath.Join(fontDir, "Deng.ttf"), filepath.Join(fontDir, "msyh.ttc"), filepath.Join(fontDir, "simhei.ttf"))
		}
	} else {
		candidates = append(candidates,
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed, err := parseWechatOpenTypeFont(data)
		if err != nil {
			continue
		}
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 96, Hinting: font.HintingFull})
		if err == nil {
			return face, nil
		}
	}
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 96, Hinting: font.HintingFull})
}

func parseWechatOpenTypeFont(data []byte) (*opentype.Font, error) {
	if collection, err := opentype.ParseCollection(data); err == nil {
		if parsed, fontErr := collection.Font(0); fontErr == nil {
			return parsed, nil
		}
	}
	return opentype.Parse(data)
}

func closeWechatFontFace(face font.Face) {
	if closer, ok := face.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func wrapWechatCoverText(face font.Face, value string, maxWidth, maxLines int) []string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return []string{"微信公众号文章"}
	}
	var lines []string
	var current []rune
	for _, r := range runes {
		candidate := string(append(append([]rune(nil), current...), r))
		if len(current) > 0 && font.MeasureString(face, candidate).Ceil() > maxWidth {
			lines = append(lines, strings.TrimSpace(string(current)))
			current = []rune{r}
			if len(lines) == maxLines {
				break
			}
			continue
		}
		current = append(current, r)
	}
	if len(lines) < maxLines && len(current) > 0 {
		lines = append(lines, strings.TrimSpace(string(current)))
	}
	if len(lines) == maxLines {
		consumed := 0
		for _, line := range lines {
			consumed += utf8.RuneCountInString(line)
		}
		if consumed < len(runes) {
			last := []rune(lines[len(lines)-1])
			if len(last) > 1 {
				last = last[:len(last)-1]
			}
			lines[len(lines)-1] = string(last) + "…"
		}
	}
	return lines
}

func drawWechatCoverText(dst imagedraw.Image, face font.Face, value string, x, baseline int, ink color.Color) {
	drawer := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{C: ink},
		Face: face,
		Dot:  fixed.P(x, baseline),
	}
	drawer.DrawString(value)
}

func normalizedWechatBundleFileKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	return strings.ToLower(filepath.Clean(abs))
}

func embedWechatBundleLocalImages(node *nethtml.Node, workspace, markdownDir string, inMemoryImages map[string][]byte) (wechatBundleImageStats, error) {
	stats := wechatBundleImageStats{}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return stats, fmt.Errorf("resolve image workspace: %w", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(absWorkspace)
	if err != nil {
		return stats, fmt.Errorf("resolve image workspace links: %w", err)
	}
	totalBytes := 0
	var walk func(*nethtml.Node) error
	walk = func(current *nethtml.Node) error {
		if current.Type == nethtml.ElementNode && strings.EqualFold(current.Data, "img") {
			src := wechatBundleNodeAttr(current, "src")
			parsed, parseErr := url.Parse(strings.TrimSpace(src))
			if parseErr != nil {
				return fmt.Errorf("invalid WeChat image URL %q: %w", src, parseErr)
			}
			stats.Total++
			if strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https") {
				stats.Remote++
			} else {
				localPath, resolveErr := resolveWechatBundleLocalImagePath(src, markdownDir, absWorkspace, canonicalWorkspace)
				if resolveErr != nil {
					return wechatBundleImageError(resolveErr)
				}
				data := inMemoryImages[normalizedWechatBundleFileKey(localPath)]
				if len(data) == 0 {
					data, resolveErr = os.ReadFile(localPath)
					if resolveErr != nil {
						return wechatBundleImageError(fmt.Errorf("read WeChat image %s: %w", src, resolveErr))
					}
				}
				mimeType, validateErr := validateWechatBundleRaster(data)
				if validateErr != nil {
					return wechatBundleImageError(fmt.Errorf("invalid WeChat image %s: %w", src, validateErr))
				}
				totalBytes += len(data)
				if totalBytes > wechatArticleBundleMaxTotalImageBytes {
					return wechatBundleImageError(fmt.Errorf("embedded images exceed %d bytes", wechatArticleBundleMaxTotalImageBytes))
				}
				wechatBundleSetNodeAttr(current, "src", "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(data))
				stats.Embedded++
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(node); err != nil {
		return stats, err
	}
	return stats, nil
}

func resolveWechatBundleLocalImagePath(raw, markdownDir, absWorkspace, canonicalWorkspace string) (string, error) {
	raw = strings.TrimSpace(raw)
	var pathValue string
	if wechatBundleLooksWindowsAbsolutePath(raw) {
		pathValue = raw
	} else {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("local image %q must not contain a query or fragment", raw)
		}
		if parsed.Scheme != "" && !strings.EqualFold(parsed.Scheme, "file") {
			return "", fmt.Errorf("unsupported local image scheme %q", parsed.Scheme)
		}
		if strings.EqualFold(parsed.Scheme, "file") && parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			return "", fmt.Errorf("local image %q uses an unsupported file host", raw)
		}
		pathValue = parsed.Path
		if len(pathValue) >= 3 && pathValue[0] == '/' && wechatBundleASCIIAlpha(pathValue[1]) && pathValue[2] == ':' {
			pathValue = pathValue[1:]
		}
	}
	var err error
	pathValue, err = url.PathUnescape(pathValue)
	if err != nil {
		return "", fmt.Errorf("decode image path %q: %w", raw, err)
	}
	pathValue = filepath.FromSlash(pathValue)
	if pathValue == "" {
		return "", errors.New("local image path is empty")
	}
	candidate := pathValue
	if !filepath.IsAbs(candidate) {
		workspaceRelative := filepath.ToSlash(filepath.Clean(candidate))
		workspaceRelative = strings.TrimPrefix(workspaceRelative, "./")
		if strings.HasPrefix(strings.ToLower(workspaceRelative), ".openkitty/") {
			candidate = filepath.Join(absWorkspace, filepath.FromSlash(workspaceRelative))
		} else {
			candidate = filepath.Join(markdownDir, candidate)
		}
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || !wechatBundlePathWithin(absWorkspace, candidate) {
		return "", fmt.Errorf("local image %q is outside the workspace", raw)
	}
	if _, statErr := os.Stat(candidate); statErr == nil {
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr != nil || !wechatBundlePathWithin(canonicalWorkspace, resolved) {
			return "", fmt.Errorf("local image %q escapes the workspace through a link", raw)
		}
	}
	return candidate, nil
}

func wechatBundleLooksWindowsAbsolutePath(value string) bool {
	return len(value) >= 3 && wechatBundleASCIIAlpha(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func wechatBundleASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func validateWechatBundleRaster(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("image is empty")
	}
	if len(data) > wechatArticleBundleMaxImageBytes {
		return "", fmt.Errorf("image exceeds %d bytes", wechatArticleBundleMaxImageBytes)
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return "", fmt.Errorf("unsupported raster type %s", mimeType)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "", errors.New("image header cannot be decoded")
	}
	if config.Width > 20000 || config.Height > 20000 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return "", fmt.Errorf("image dimensions are too large: %dx%d", config.Width, config.Height)
	}
	return mimeType, nil
}

func wechatBundleNodeAttr(node *nethtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func wechatBundleSetNodeAttr(node *nethtml.Node, key, value string) {
	for index := range node.Attr {
		if strings.EqualFold(node.Attr[index].Key, key) {
			node.Attr[index].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, nethtml.Attribute{Key: key, Val: value})
}

type wechatBundleImageStats struct {
	Total    int
	Embedded int
	Remote   int
}

func renderWechatArticleBundleHTML(body, executionID, sourceSHA, workspace, markdownDir string, inMemoryImages map[string][]byte) (string, wechatBundleImageStats, error) {
	extensions := parser.CommonExtensions | parser.Tables | parser.FencedCode | parser.Strikethrough | parser.SpaceHeadings | parser.NoEmptyLineBeforeBlock
	doc := parser.NewWithExtensions(extensions).Parse([]byte(body))
	flags := mdhtml.CommonFlags | mdhtml.SkipHTML | mdhtml.Safelink | mdhtml.NofollowLinks | mdhtml.NoreferrerLinks | mdhtml.NoopenerLinks | mdhtml.HrefTargetBlank
	fragment := markdown.Render(doc, mdhtml.NewRenderer(mdhtml.RendererOptions{Flags: flags}))
	wrapper := "<section><article>" + string(fragment) + "</article></section>"
	parsed, err := nethtml.Parse(strings.NewReader(wrapper))
	if err != nil {
		return "", wechatBundleImageStats{}, fmt.Errorf("parse rendered WeChat HTML: %w", err)
	}
	section := findWechatBundleElement(parsed, "section")
	if section == nil {
		return "", wechatBundleImageStats{}, errors.New("rendered WeChat HTML has no section wrapper")
	}
	if !sanitizeWechatBundleNode(section) {
		return "", wechatBundleImageStats{}, errors.New("rendered WeChat HTML was rejected by the sanitizer")
	}
	imageStats, err := embedWechatBundleLocalImages(section, workspace, markdownDir, inMemoryImages)
	if err != nil {
		return "", wechatBundleImageStats{}, err
	}
	var rendered bytes.Buffer
	if err := nethtml.Render(&rendered, section); err != nil {
		return "", wechatBundleImageStats{}, fmt.Errorf("serialize rendered WeChat HTML: %w", err)
	}
	provenance := "<!-- OPENKITTY_EXECUTION_ID: " + executionID + " -->\n"
	if sourceSHA != "" {
		provenance += "<!-- OPENKITTY_SOURCE_SHA256: " + sourceSHA + " -->\n"
	}
	result := provenance + rendered.String() + "\n"
	if err := pipelinecore.ValidateWechatHTMLDocument(result, "OPENKITTY_EXECUTION_ID: "+executionID, optionalWechatSourceMarker(sourceSHA), safeWechatInlineRasterDataURL); err != nil {
		return "", wechatBundleImageStats{}, fmt.Errorf("validate generated WeChat HTML: %w", err)
	}
	return result, imageStats, nil
}

func optionalWechatSourceMarker(sourceSHA string) string {
	if sourceSHA == "" {
		return ""
	}
	return "OPENKITTY_SOURCE_SHA256: " + sourceSHA
}

func sanitizeWechatExecutionID(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "step"
	}
	return name
}

func findWechatBundleElement(node *nethtml.Node, tag string) *nethtml.Node {
	if node.Type == nethtml.ElementNode && strings.EqualFold(node.Data, tag) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findWechatBundleElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}

var wechatBundleAllowedTags = map[string]bool{
	"section": true, "article": true, "div": true, "p": true, "span": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true,
	"blockquote": true, "ul": true, "ol": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hr": true, "br": true, "img": true, "a": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "th": true, "td": true,
	"figure": true, "figcaption": true, "small": true, "sup": true, "sub": true, "code": true, "pre": true,
}

var wechatBundleDropTags = map[string]bool{
	"script": true, "style": true, "meta": true, "link": true, "base": true, "iframe": true,
	"object": true, "embed": true, "svg": true, "math": true, "form": true, "input": true,
	"button": true, "textarea": true, "video": true, "audio": true, "canvas": true,
}

var wechatBundleInlineStyles = map[string]string{
	"section":    "max-width:677px;margin:0 auto;padding:20px 16px;background:#ffffff;color:#2b2b2b;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;font-size:16px;line-height:1.85;word-break:break-word;box-sizing:border-box;",
	"article":    "width:100%;margin:0;padding:0;box-sizing:border-box;",
	"h1":         "margin:0 0 24px;color:#1f2329;font-size:26px;line-height:1.4;font-weight:700;text-align:left;",
	"h2":         "margin:32px 0 14px;padding-left:10px;border-left:4px solid #07c160;color:#1f2329;font-size:21px;line-height:1.5;font-weight:700;",
	"h3":         "margin:26px 0 12px;color:#1f2329;font-size:18px;line-height:1.55;font-weight:700;",
	"h4":         "margin:22px 0 10px;color:#1f2329;font-size:17px;line-height:1.55;font-weight:700;",
	"h5":         "margin:20px 0 8px;color:#1f2329;font-size:16px;line-height:1.55;font-weight:700;",
	"h6":         "margin:18px 0 8px;color:#57606a;font-size:15px;line-height:1.55;font-weight:700;",
	"p":          "margin:0 0 16px;color:#2b2b2b;font-size:16px;line-height:1.85;letter-spacing:0.02em;text-align:justify;",
	"blockquote": "margin:20px 0;padding:14px 16px;border-left:4px solid #07c160;background:#f6f8f7;color:#57606a;font-size:15px;line-height:1.75;",
	"ul":         "margin:12px 0 18px;padding-left:24px;color:#2b2b2b;font-size:16px;line-height:1.8;",
	"ol":         "margin:12px 0 18px;padding-left:24px;color:#2b2b2b;font-size:16px;line-height:1.8;",
	"li":         "margin:6px 0;padding-left:2px;line-height:1.8;",
	"a":          "color:#576b95;text-decoration:none;border-bottom:1px solid #d8deea;",
	"img":        "display:block;max-width:100%;height:auto;margin:20px auto;border:0;box-sizing:border-box;",
	"strong":     "color:#1f2329;font-weight:700;",
	"b":          "color:#1f2329;font-weight:700;",
	"em":         "font-style:italic;",
	"i":          "font-style:italic;",
	"s":          "text-decoration:line-through;",
	"hr":         "height:1px;margin:28px 0;border:0;background:#e5e6eb;",
	"table":      "width:100%;margin:18px 0;border-collapse:collapse;table-layout:fixed;font-size:14px;line-height:1.65;",
	"th":         "padding:9px 8px;border:1px solid #dfe3e8;background:#f6f8fa;color:#1f2329;font-weight:700;text-align:left;word-break:break-word;",
	"td":         "padding:9px 8px;border:1px solid #dfe3e8;color:#2b2b2b;text-align:left;word-break:break-word;",
	"pre":        "margin:18px 0;padding:14px 16px;overflow-x:auto;background:#f6f8fa;border-radius:6px;color:#24292f;font-size:13px;line-height:1.65;white-space:pre-wrap;word-break:break-all;",
	"code":       "padding:2px 5px;background:#f3f4f5;border-radius:3px;color:#d14;font-family:Consolas,monospace;font-size:0.9em;",
	"figure":     "margin:20px 0;padding:0;",
	"figcaption": "margin:8px 0 0;color:#8a919f;font-size:13px;line-height:1.6;text-align:center;",
	"small":      "color:#8a919f;font-size:13px;line-height:1.6;",
}

func sanitizeWechatBundleNode(node *nethtml.Node) bool {
	if node.Type == nethtml.CommentNode || node.Type == nethtml.DoctypeNode {
		return false
	}
	if node.Type == nethtml.ElementNode {
		tag := strings.ToLower(node.Data)
		if wechatBundleDropTags[tag] {
			return false
		}
		switch tag {
		case "del":
			tag = "s"
		case "tt":
			tag = "code"
		}
		if !wechatBundleAllowedTags[tag] {
			tag = "span"
		}
		node.Data = tag
		node.DataAtom = atom.Lookup([]byte(tag))
		if tag == "img" && !wechatBundleImageHasSafeSource(node) {
			return false
		}
		node.Attr = wechatBundleSafeAttrs(tag, node.Attr)
	}
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if !sanitizeWechatBundleNode(child) {
			node.RemoveChild(child)
		}
		child = next
	}
	return true
}

func wechatBundleSafeAttrs(tag string, input []nethtml.Attribute) []nethtml.Attribute {
	lookup := make(map[string]string, len(input))
	for _, attr := range input {
		lookup[strings.ToLower(attr.Key)] = attr.Val
	}
	attrs := make([]nethtml.Attribute, 0, 6)
	if style := wechatBundleInlineStyles[tag]; style != "" {
		attrs = append(attrs, nethtml.Attribute{Key: "style", Val: style})
	}
	switch tag {
	case "a":
		if href := strings.TrimSpace(lookup["href"]); href != "" && wechatBundleSafeURL(href) {
			attrs = append(attrs,
				nethtml.Attribute{Key: "href", Val: href},
				nethtml.Attribute{Key: "target", Val: "_blank"},
				nethtml.Attribute{Key: "rel", Val: "nofollow noreferrer noopener"},
			)
		}
		if title := strings.TrimSpace(lookup["title"]); title != "" {
			attrs = append(attrs, nethtml.Attribute{Key: "title", Val: title})
		}
	case "img":
		attrs = append(attrs, nethtml.Attribute{Key: "src", Val: strings.TrimSpace(lookup["src"])})
		if alt := strings.TrimSpace(lookup["alt"]); alt != "" {
			attrs = append(attrs, nethtml.Attribute{Key: "alt", Val: alt})
		}
		if title := strings.TrimSpace(lookup["title"]); title != "" {
			attrs = append(attrs, nethtml.Attribute{Key: "title", Val: title})
		}
	case "td", "th":
		for _, key := range []string{"colspan", "rowspan"} {
			if value := strings.TrimSpace(lookup[key]); value != "" {
				attrs = append(attrs, nethtml.Attribute{Key: key, Val: value})
			}
		}
	case "ol":
		if start := strings.TrimSpace(lookup["start"]); start != "" {
			attrs = append(attrs, nethtml.Attribute{Key: "start", Val: start})
		}
	case "li":
		if value := strings.TrimSpace(lookup["value"]); value != "" {
			attrs = append(attrs, nethtml.Attribute{Key: "value", Val: value})
		}
	}
	return attrs
}

func wechatBundleImageHasSafeSource(node *nethtml.Node) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, "src") {
			return strings.TrimSpace(attr.Val) != "" && wechatBundleSafeURL(attr.Val)
		}
	}
	return false
}

func wechatBundleSafeURL(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		return parsed.Host == ""
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

func safeWechatInlineRasterDataURL(raw string) bool {
	value := strings.TrimSpace(raw)
	if len(value) == 0 || len(value) > (wechatArticleBundleMaxImageBytes*4/3)+256 {
		return false
	}
	comma := strings.IndexByte(value, ',')
	if comma <= 5 {
		return false
	}
	header := strings.ToLower(value[:comma])
	if !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
		return false
	}
	declared := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	switch declared {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value[comma+1:])
	if err != nil {
		return false
	}
	detected, err := validateWechatBundleRaster(decoded)
	return err == nil && detected == declared
}

// SafeWechatInlineRasterDataURL reports whether a data URL contains an
// allowed, bounded raster image for inline WeChat HTML.
func SafeWechatInlineRasterDataURL(raw string) bool {
	return safeWechatInlineRasterDataURL(raw)
}
