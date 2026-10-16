package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"mac/internal/fsutil"
)

const (
	novaChatroomDefaultBaseURL = "https://www.novaexel.com/nova/chatroom"
	novaChatroomMaxBodyBytes   = 8 << 20
)

// NovaChatroomSearchTool performs an authenticated-free, GET-only search of
// Nova Chatroom and freezes the selected full post detail into an auditable
// source bundle. It never invokes a shell or trusts process stdout.
type NovaChatroomSearchTool struct {
	workspace        string
	apiBaseURL       string
	canonicalBaseURL string
	httpClient       *http.Client
	requestTimeout   time.Duration
	totalTimeout     time.Duration
	maxConcurrency   int
	pageSize         int
	now              func() time.Time
}

// NewNovaChatroomSearchTool creates the production Nova source-acquisition
// tool. The only remote operation it can issue is HTTP GET against the fixed
// public Nova Chatroom API base URL.
func NewNovaChatroomSearchTool(workspace string) *NovaChatroomSearchTool {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 4 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	transport.MaxConnsPerHost = 6
	transport.MaxIdleConnsPerHost = 6

	return &NovaChatroomSearchTool{
		workspace:        workspace,
		apiBaseURL:       novaChatroomDefaultBaseURL,
		canonicalBaseURL: novaChatroomDefaultBaseURL,
		httpClient: &http.Client{
			Transport: transport,
			// Do not follow redirects to an untrusted host. The production API
			// endpoints are canonical HTTPS URLs and do not need redirects.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		requestTimeout: 5 * time.Second,
		totalTimeout:   20 * time.Second,
		maxConcurrency: 6,
		pageSize:       50,
		now:            time.Now,
	}
}

func (t *NovaChatroomSearchTool) Name() string { return "nova_chatroom_search" }

func (t *NovaChatroomSearchTool) Description() string {
	return "Search Nova Chatroom semantically using its public read-only API, fetch selected posts in full, and atomically freeze an auditable source.json. " +
		"Use this for user-referenced Nova/chatroom posts instead of web search or shell scripts. Remote requests are GET-only."
}

func (t *NovaChatroomSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Two to five distinctive topic terms that identify the referenced post; do not include article-writing instructions.",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     10,
				"description": "Maximum number of matching full posts to freeze. Default: 1 for a single referenced source.",
			},
			"max_pages": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     10,
				"description": "Maximum pages to scan per topic. Default: 5.",
			},
			"output_path": map[string]interface{}{
				"type":        "string",
				"description": "Execution-scoped source.json path under .openkitty/storage/ (for example .openkitty/storage/.system/pipeline_sources/EXECUTION_ID/source.json).",
			},
		},
		"required": []string{"query", "output_path"},
	}
}

// IsReadOnly declares that the external provider operation is read-only. The
// local source.json write is an explicit, execution-scoped audit artifact.
func (t *NovaChatroomSearchTool) IsReadOnly() bool { return true }

// IsNeverParallel avoids two model-issued calls racing to the same audit path.
// The tool still performs its independent provider reads concurrently inside a
// single call, with a hard concurrency bound.
func (t *NovaChatroomSearchTool) IsNeverParallel() bool { return true }

func (t *NovaChatroomSearchTool) MaxResultSize() int { return 16 * 1024 }

func (t *NovaChatroomSearchTool) ScopePath(args map[string]interface{}) string {
	path, _ := args["output_path"].(string)
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(t.workspace, filepath.Clean(path))
}

type novaChatroomSource struct {
	PostID        int64  `json:"post_id"`
	TopicID       int64  `json:"topic_id,omitempty"`
	Author        string `json:"author,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	SourceURL     string `json:"source_url"`
	ContentType   string `json:"content_type"`
	ContentSHA256 string `json:"content_sha256"`
	Content       string `json:"content"`
}

type novaChatroomSourceBundle struct {
	Status     string               `json:"status"`
	Source     string               `json:"source"`
	Query      string               `json:"query"`
	FetchedAt  string               `json:"fetched_at"`
	MatchCount int                  `json:"match_count"`
	Matches    []novaChatroomSource `json:"matches"`
}

type NovaChatroomSourceBundle = novaChatroomSourceBundle

type novaChatroomSelectedReceipt struct {
	PostID        int64  `json:"post_id"`
	TopicID       int64  `json:"topic_id,omitempty"`
	Author        string `json:"author,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	SourceURL     string `json:"source_url"`
	ContentType   string `json:"content_type"`
	ContentSHA256 string `json:"content_sha256"`
}

type novaChatroomSearchReceipt struct {
	OK                    bool                        `json:"ok"`
	Source                string                      `json:"source"`
	Query                 string                      `json:"query"`
	MatchCount            int                         `json:"match_count"`
	Selected              novaChatroomSelectedReceipt `json:"selected"`
	SelectedPostID        int64                       `json:"selected_post_id"`
	SelectedSourceURL     string                      `json:"selected_source_url"`
	SelectedContentSHA256 string                      `json:"selected_content_sha256"`
	OutputPath            string                      `json:"output_path"`
	WholeFileSHA256       string                      `json:"whole_file_sha256"`
	FileSHA256            string                      `json:"file_sha256"`
	FileBytes             int                         `json:"file_bytes"`
	FetchedAt             string                      `json:"fetched_at"`
}

type NovaChatroomSearchReceipt = novaChatroomSearchReceipt

type novaChatroomTopic struct {
	ID          int64
	PostCount   int64
	Title       string
	Description string
	Category    string
}

type NovaChatroomTopic = novaChatroomTopic

type novaChatroomPost struct {
	ID          int64
	TopicID     int64
	Author      string
	CreatedAt   string
	ContentType string
	Content     string
	searchScore int
}

type NovaChatroomPost = novaChatroomPost

func (t *NovaChatroomSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil {
		return "", errors.New("nova_chatroom_search is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is required")
	}
	if utf8.RuneCountInString(query) > 240 {
		return "", errors.New("query is too long; use 2-5 distinctive topic terms")
	}
	if normalizeNovaChatroomSearchText(query) == "" {
		return "", errors.New("query has no searchable topic terms")
	}
	maxResults, err := novaChatroomIntParam(params, "max_results", 1, 1, 10)
	if err != nil {
		return "", err
	}
	maxPages, err := novaChatroomIntParam(params, "max_pages", 5, 1, 10)
	if err != nil {
		return "", err
	}
	outputPath, _ := params["output_path"].(string)
	absoluteOutputPath, err := t.resolveOutputPath(outputPath)
	if err != nil {
		return "", err
	}

	totalTimeout := t.totalTimeout
	if totalTimeout <= 0 {
		totalTimeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	posts, err := t.search(runCtx, query, maxResults, maxPages)
	if err != nil {
		return "", err
	}
	if len(posts) == 0 {
		return "", fmt.Errorf("no Nova chatroom posts matched query %q", query)
	}

	fetchedAt := t.nowUTC().Format(time.RFC3339Nano)
	sources := make([]novaChatroomSource, 0, len(posts))
	canonicalBase := strings.TrimRight(t.canonicalBaseURL, "/")
	if canonicalBase == "" {
		canonicalBase = strings.TrimRight(t.apiBaseURL, "/")
	}
	for _, post := range posts {
		contentHash := sha256.Sum256([]byte(post.Content))
		contentType := strings.TrimSpace(post.ContentType)
		if contentType == "" {
			contentType = "markdown"
		}
		sources = append(sources, novaChatroomSource{
			PostID:        post.ID,
			TopicID:       post.TopicID,
			Author:        post.Author,
			CreatedAt:     post.CreatedAt,
			SourceURL:     canonicalBase + "/posts/" + strconv.FormatInt(post.ID, 10),
			ContentType:   contentType,
			ContentSHA256: hex.EncodeToString(contentHash[:]),
			Content:       post.Content,
		})
	}
	bundle := novaChatroomSourceBundle{
		Status:     "ready",
		Source:     "nova_chatroom",
		Query:      query,
		FetchedAt:  fetchedAt,
		MatchCount: len(sources),
		Matches:    sources,
	}
	fileBytes, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Nova source bundle: %w", err)
	}
	fileBytes = append(fileBytes, '\n')
	if err := fsutil.WriteAtomic(absoluteOutputPath, fileBytes, 0o600); err != nil {
		return "", fmt.Errorf("atomically freeze Nova source: %w", err)
	}

	// Hash the final bytes from disk, not a pre-write logical representation.
	finalBytes, err := os.ReadFile(absoluteOutputPath)
	if err != nil {
		return "", fmt.Errorf("verify frozen Nova source: %w", err)
	}
	fileHash := sha256.Sum256(finalBytes)
	fileHashHex := hex.EncodeToString(fileHash[:])
	selected := sources[0]
	receipt := novaChatroomSearchReceipt{
		OK:         true,
		Source:     "nova_chatroom",
		Query:      query,
		MatchCount: len(sources),
		Selected: novaChatroomSelectedReceipt{
			PostID:        selected.PostID,
			TopicID:       selected.TopicID,
			Author:        selected.Author,
			CreatedAt:     selected.CreatedAt,
			SourceURL:     selected.SourceURL,
			ContentType:   selected.ContentType,
			ContentSHA256: selected.ContentSHA256,
		},
		SelectedPostID:        selected.PostID,
		SelectedSourceURL:     selected.SourceURL,
		SelectedContentSHA256: selected.ContentSHA256,
		OutputPath:            filepath.Clean(absoluteOutputPath),
		WholeFileSHA256:       fileHashHex,
		FileSHA256:            fileHashHex,
		FileBytes:             len(finalBytes),
		FetchedAt:             fetchedAt,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("encode Nova source receipt: %w", err)
	}
	return string(receiptJSON), nil
}

func (t *NovaChatroomSearchTool) nowUTC() time.Time {
	if t.now != nil {
		return t.now().UTC()
	}
	return time.Now().UTC()
}

func (t *NovaChatroomSearchTool) SetFeedOptions(pageSize int, requestTimeout time.Duration) {
	if pageSize > 0 {
		t.pageSize = pageSize
	}
	if requestTimeout > 0 {
		t.requestTimeout = requestTimeout
	}
}

func (t *NovaChatroomSearchTool) ConfigureEndpoint(apiBaseURL, canonicalBaseURL string, client *http.Client, requestTimeout, totalTimeout time.Duration) {
	if t == nil {
		return
	}
	if value := strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"); value != "" {
		t.apiBaseURL = value
	}
	if value := strings.TrimRight(strings.TrimSpace(canonicalBaseURL), "/"); value != "" {
		t.canonicalBaseURL = value
	}
	if client != nil {
		t.httpClient = client
	}
	if requestTimeout > 0 {
		t.requestTimeout = requestTimeout
	}
	if totalTimeout > 0 {
		t.totalTimeout = totalTimeout
	}
}

func (t *NovaChatroomSearchTool) SetCanonicalBaseURL(value string) {
	if t != nil {
		t.canonicalBaseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	}
}

func (t *NovaChatroomSearchTool) CanonicalBaseURL() string {
	if t == nil {
		return ""
	}
	if value := strings.TrimRight(t.canonicalBaseURL, "/"); value != "" {
		return value
	}
	return strings.TrimRight(t.apiBaseURL, "/")
}

func (t *NovaChatroomSearchTool) FetchTopics(ctx context.Context) ([]NovaChatroomTopic, error) {
	return t.fetchTopics(ctx)
}

func (t *NovaChatroomSearchTool) FetchTopicPageBatch(ctx context.Context, topics []NovaChatroomTopic, page int) ([]NovaChatroomPost, []error) {
	return t.fetchTopicPageBatch(ctx, topics, page)
}

// FetchPost reuses the bounded, read-only detail client and verifies the
// response belongs to the requested post. The caller cannot supply a URL.
func (t *NovaChatroomSearchTool) FetchPost(ctx context.Context, id int64) (NovaChatroomPost, error) {
	if t == nil || id <= 0 {
		return NovaChatroomPost{}, errors.New("invalid community post")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	posts, errs := t.fetchPostDetailBatch(ctx, []novaChatroomPost{{ID: id}})
	if len(errs) > 0 {
		return NovaChatroomPost{}, errors.Join(errs...)
	}
	if len(posts) != 1 {
		return NovaChatroomPost{}, errors.New("community post is unavailable")
	}
	return posts[0], nil
}

func novaChatroomIntParam(params map[string]interface{}, name string, defaultValue, minValue, maxValue int) (int, error) {
	raw, exists := params[name]
	if !exists || raw == nil {
		return defaultValue, nil
	}
	var value int64
	switch typed := raw.(type) {
	case int:
		value = int64(typed)
	case int64:
		value = typed
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		value = int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		value = parsed
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if value < int64(minValue) || value > int64(maxValue) {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minValue, maxValue)
	}
	return int(value), nil
}

func (t *NovaChatroomSearchTool) resolveOutputPath(rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", errors.New("output_path is required")
	}
	if strings.IndexByte(rawPath, 0) >= 0 {
		return "", errors.New("output_path contains an invalid NUL byte")
	}
	if !strings.EqualFold(filepath.Base(filepath.Clean(rawPath)), "source.json") {
		return "", errors.New("output_path must name an execution-scoped source.json")
	}
	workspace, err := filepath.Abs(t.workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace links: %w", err)
	}
	kittyRoot := filepath.Join(workspace, ".openkitty")
	if err := novaChatroomEnsureDirectChildDir(workspace, canonicalWorkspace, kittyRoot, ".openkitty"); err != nil {
		return "", err
	}
	canonicalKittyRoot, err := filepath.EvalSymlinks(kittyRoot)
	if err != nil || !novaChatroomPathWithin(canonicalWorkspace, canonicalKittyRoot) {
		return "", errors.New("workspace/.openkitty resolves outside the workspace")
	}
	storageRoot := filepath.Join(workspace, ".openkitty", "storage")
	if err := novaChatroomEnsureDirectChildDir(kittyRoot, canonicalKittyRoot, storageRoot, "storage"); err != nil {
		return "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(storageRoot)
	if err != nil {
		return "", fmt.Errorf("resolve storage root: %w", err)
	}
	if !novaChatroomPathWithin(canonicalWorkspace, canonicalRoot) {
		return "", errors.New("workspace/.openkitty/storage resolves outside the workspace")
	}
	var outputPath string
	if filepath.IsAbs(rawPath) {
		outputPath = filepath.Clean(rawPath)
	} else {
		outputPath = filepath.Join(workspace, filepath.Clean(rawPath))
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve output_path: %w", err)
	}
	if !novaChatroomPathWithin(storageRoot, outputPath) {
		return "", errors.New("output_path must stay under workspace/.openkitty/storage")
	}
	canonicalParent, err := novaChatroomCreateSafeOutputParent(storageRoot, canonicalRoot, filepath.Dir(outputPath))
	if err != nil {
		return "", err
	}
	if !novaChatroomPathWithin(canonicalRoot, filepath.Join(canonicalParent, filepath.Base(outputPath))) {
		return "", errors.New("output_path escapes storage through a symlink or junction")
	}
	if info, err := os.Lstat(outputPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("output_path must not be a symlink")
		}
		// A current pipeline execution must own a fresh path. Refusing to
		// overwrite also prevents stale source substitution during retries.
		return "", errors.New("output_path already exists; use a fresh execution-scoped path")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect output_path: %w", err)
	}
	return outputPath, nil
}

func novaChatroomEnsureDirectChildDir(parent, canonicalParent, child, label string) error {
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
	if err != nil {
		return fmt.Errorf("resolve %s directory: %w", label, err)
	}
	if !novaChatroomPathWithin(canonicalParent, resolved) {
		return fmt.Errorf("%s directory resolves outside %s", label, parent)
	}
	return nil
}

func novaChatroomCreateSafeOutputParent(storageRoot, canonicalRoot, wantedParent string) (string, error) {
	relative, err := filepath.Rel(storageRoot, wantedParent)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("source output directory is outside storage")
	}
	current := storageRoot
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if part == "" || part == "." || part == ".." {
				return "", errors.New("source output directory has an invalid path component")
			}
			current = filepath.Join(current, part)
			if info, statErr := os.Lstat(current); statErr == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("source output directory component %s is not a real directory", current)
				}
			} else if os.IsNotExist(statErr) {
				if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil && !os.IsExist(mkdirErr) {
					return "", fmt.Errorf("create source output directory: %w", mkdirErr)
				}
			} else {
				return "", fmt.Errorf("inspect source output directory: %w", statErr)
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !novaChatroomPathWithin(canonicalRoot, resolved) {
				return "", errors.New("source output directory escapes storage through a symlink or junction")
			}
		}
	}
	canonicalParent, err := filepath.EvalSymlinks(current)
	if err != nil || !novaChatroomPathWithin(canonicalRoot, canonicalParent) {
		return "", errors.New("source output directory escapes storage through a symlink or junction")
	}
	return canonicalParent, nil
}

func novaChatroomPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (t *NovaChatroomSearchTool) search(ctx context.Context, query string, maxResults, maxPages int) ([]novaChatroomPost, error) {
	topics, err := t.fetchTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch Nova topics: %w", err)
	}
	if len(topics) == 0 {
		return nil, errors.New("Nova chatroom returned no searchable topics")
	}

	candidates := make(map[int64]novaChatroomPost)
	pagesWithoutNewMatches := 0
	var pageErrors []error
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("Nova chatroom search deadline: %w", err)
		}
		activeTopics := make([]novaChatroomTopic, 0, len(topics))
		for _, topic := range topics {
			if topic.PostCount > 0 && int64((page-1)*t.effectivePageSize()) >= topic.PostCount {
				continue
			}
			activeTopics = append(activeTopics, topic)
		}
		if len(activeTopics) == 0 {
			break
		}

		pagePosts, errs := t.fetchTopicPageBatch(ctx, activeTopics, page)
		pageErrors = append(pageErrors, errs...)
		before := len(candidates)
		for _, post := range pagePosts {
			score := scoreNovaChatroomPost(post, query)
			if score <= 0 {
				continue
			}
			post.searchScore = score
			if previous, exists := candidates[post.ID]; !exists || novaChatroomPostLess(previous, post) {
				candidates[post.ID] = post
			}
		}
		if len(candidates) == before {
			pagesWithoutNewMatches++
		} else {
			pagesWithoutNewMatches = 0
		}
		strongMatch := false
		for _, candidate := range candidates {
			if candidate.searchScore >= 200 {
				strongMatch = true
				break
			}
		}
		if strongMatch || len(candidates) >= maxResults || (len(candidates) > 0 && pagesWithoutNewMatches >= 1) {
			break
		}
	}

	if len(candidates) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("Nova chatroom search deadline: %w", err)
		}
		if len(pageErrors) > 0 {
			return nil, fmt.Errorf("Nova topic pages were unavailable: %w", errors.Join(pageErrors...))
		}
		return nil, fmt.Errorf("no Nova chatroom posts matched query %q", query)
	}

	ranked := make([]novaChatroomPost, 0, len(candidates))
	for _, post := range candidates {
		ranked = append(ranked, post)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return novaChatroomPostLess(ranked[i], ranked[j])
	})
	if len(ranked) > maxResults {
		ranked = ranked[:maxResults]
	}

	// Search-list records may be truncated previews. Only full detail records
	// can enter the frozen source bundle.
	detailed, detailErrors := t.fetchPostDetailBatch(ctx, ranked)
	if len(detailed) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("Nova post-detail deadline: %w", err)
		}
		return nil, fmt.Errorf("matched Nova posts had no usable full detail: %w", errors.Join(detailErrors...))
	}
	return detailed, nil
}

func (t *NovaChatroomSearchTool) effectivePageSize() int {
	if t.pageSize < 1 {
		return 50
	}
	if t.pageSize > 50 {
		return 50
	}
	return t.pageSize
}

func (t *NovaChatroomSearchTool) effectiveConcurrency() int {
	if t.maxConcurrency < 1 {
		return 1
	}
	if t.maxConcurrency > 12 {
		return 12
	}
	return t.maxConcurrency
}

func (t *NovaChatroomSearchTool) fetchTopics(ctx context.Context) ([]novaChatroomTopic, error) {
	data, err := t.getJSON(ctx, "/topics", nil)
	if err != nil {
		return nil, err
	}
	items, err := novaChatroomEnvelopeArray(data, "topics")
	if err != nil {
		return nil, err
	}
	topics := make([]novaChatroomTopic, 0, len(items))
	for _, item := range items {
		var raw struct {
			ID          json.RawMessage `json:"id"`
			PostCount   json.RawMessage `json:"postCount"`
			PostCount2  json.RawMessage `json:"post_count"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Category    string          `json:"category"`
			IsActive    *bool           `json:"isActive"`
		}
		if json.Unmarshal(item, &raw) != nil || (raw.IsActive != nil && !*raw.IsActive) {
			continue
		}
		id, ok := novaChatroomPositiveInt(raw.ID)
		if !ok {
			continue
		}
		count, _ := novaChatroomNonNegativeInt(raw.PostCount)
		if count == 0 {
			count, _ = novaChatroomNonNegativeInt(raw.PostCount2)
		}
		topics = append(topics, novaChatroomTopic{
			ID:          id,
			PostCount:   count,
			Title:       strings.TrimSpace(raw.Title),
			Description: strings.TrimSpace(raw.Description),
			Category:    strings.TrimSpace(raw.Category),
		})
	}
	return topics, nil
}

type novaChatroomPageResult struct {
	posts []novaChatroomPost
	err   error
}

func (t *NovaChatroomSearchTool) fetchTopicPageBatch(ctx context.Context, topics []novaChatroomTopic, page int) ([]novaChatroomPost, []error) {
	results := make([]novaChatroomPageResult, len(topics))
	sem := make(chan struct{}, t.effectiveConcurrency())
	var wg sync.WaitGroup
	for i, topic := range topics {
		i, topic := i, topic
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			values := url.Values{
				"page":  {strconv.Itoa(page)},
				"limit": {strconv.Itoa(t.effectivePageSize())},
			}
			path := "/topics/" + strconv.FormatInt(topic.ID, 10) + "/posts"
			data, err := t.getJSON(ctx, path, values)
			if err != nil {
				results[i].err = fmt.Errorf("topic %d page %d: %w", topic.ID, page, err)
				return
			}
			items, err := novaChatroomEnvelopeArray(data, "posts")
			if err != nil {
				results[i].err = fmt.Errorf("topic %d page %d: %w", topic.ID, page, err)
				return
			}
			for _, item := range items {
				post, ok := decodeNovaChatroomPost(item)
				if !ok {
					continue
				}
				if post.TopicID == 0 {
					post.TopicID = topic.ID
				}
				results[i].posts = append(results[i].posts, post)
			}
		}()
	}
	wg.Wait()
	var posts []novaChatroomPost
	var errs []error
	for _, result := range results {
		posts = append(posts, result.posts...)
		if result.err != nil {
			errs = append(errs, result.err)
		}
	}
	return posts, errs
}

func (t *NovaChatroomSearchTool) fetchPostDetailBatch(ctx context.Context, ranked []novaChatroomPost) ([]novaChatroomPost, []error) {
	results := make([]novaChatroomPageResult, len(ranked))
	sem := make(chan struct{}, t.effectiveConcurrency())
	var wg sync.WaitGroup
	for i, candidate := range ranked {
		i, candidate := i, candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			data, err := t.getJSON(ctx, "/posts/"+strconv.FormatInt(candidate.ID, 10), nil)
			if err != nil {
				results[i].err = fmt.Errorf("post %d detail: %w", candidate.ID, err)
				return
			}
			item, err := novaChatroomEnvelopeObject(data, "post")
			if err != nil {
				results[i].err = fmt.Errorf("post %d detail: %w", candidate.ID, err)
				return
			}
			detail, ok := decodeNovaChatroomPost(item)
			if !ok || detail.ID != candidate.ID || strings.TrimSpace(detail.Content) == "" {
				results[i].err = fmt.Errorf("post %d detail did not contain the selected full post", candidate.ID)
				return
			}
			if detail.TopicID == 0 {
				detail.TopicID = candidate.TopicID
			}
			detail.searchScore = candidate.searchScore
			results[i].posts = []novaChatroomPost{detail}
		}()
	}
	wg.Wait()
	var posts []novaChatroomPost
	var errs []error
	for _, result := range results {
		posts = append(posts, result.posts...)
		if result.err != nil {
			errs = append(errs, result.err)
		}
	}
	return posts, errs
}

func (t *NovaChatroomSearchTool) getJSON(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestTimeout := t.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 5 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	base := strings.TrimRight(t.apiBaseURL, "/")
	if base == "" {
		return nil, errors.New("Nova API base URL is empty")
	}
	endpoint := base + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "OpenKittyNovaChatroom/3.0 (host-tool; read-only)")
	client := t.httpClient
	if client == nil {
		client = NewNovaChatroomSearchTool(t.workspace).httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, novaChatroomMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > novaChatroomMaxBodyBytes {
		return nil, errors.New("Nova response exceeded 8 MiB limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Nova API returned HTTP %d", resp.StatusCode)
	}
	if !json.Valid(body) {
		return nil, errors.New("Nova API returned invalid JSON")
	}
	return body, nil
}

func novaChatroomEnvelopeArray(data []byte, field string) ([]json.RawMessage, error) {
	var direct []json.RawMessage
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &direct); err != nil {
			return nil, err
		}
		return direct, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	raw := envelope[field]
	if len(raw) == 0 {
		return nil, fmt.Errorf("Nova response has no %s array", field)
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return nil, fmt.Errorf("decode Nova %s array: %w", field, err)
	}
	return direct, nil
}

func novaChatroomEnvelopeObject(data []byte, field string) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if raw := envelope[field]; len(raw) > 0 && string(raw) != "null" {
		return raw, nil
	}
	// Some Nova deployments return a post object directly.
	if _, ok := envelope["id"]; ok {
		return json.RawMessage(data), nil
	}
	return nil, fmt.Errorf("Nova response has no %s object", field)
}

func decodeNovaChatroomPost(item json.RawMessage) (novaChatroomPost, bool) {
	var raw struct {
		ID           json.RawMessage `json:"id"`
		TopicID      json.RawMessage `json:"topicId"`
		TopicID2     json.RawMessage `json:"topic_id"`
		Topic        json.RawMessage `json:"topic"`
		Author       json.RawMessage `json:"author"`
		RobotID      string          `json:"robotId"`
		CreatedAt    string          `json:"createdAt"`
		CreatedAt2   string          `json:"created_at"`
		ContentType  string          `json:"contentType"`
		ContentType2 string          `json:"content_type"`
		Content      string          `json:"content"`
	}
	if json.Unmarshal(item, &raw) != nil {
		return novaChatroomPost{}, false
	}
	id, ok := novaChatroomPositiveInt(raw.ID)
	if !ok {
		return novaChatroomPost{}, false
	}
	topicID, _ := novaChatroomPositiveInt(raw.TopicID)
	if topicID == 0 {
		topicID, _ = novaChatroomPositiveInt(raw.TopicID2)
	}
	if topicID == 0 && len(raw.Topic) > 0 && string(raw.Topic) != "null" {
		var topic struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(raw.Topic, &topic) == nil {
			topicID, _ = novaChatroomPositiveInt(topic.ID)
		}
	}
	author := novaChatroomString(raw.Author)
	if author == "" {
		author = strings.TrimSpace(raw.RobotID)
	}
	createdAt := strings.TrimSpace(raw.CreatedAt)
	if createdAt == "" {
		createdAt = strings.TrimSpace(raw.CreatedAt2)
	}
	contentType := strings.TrimSpace(raw.ContentType)
	if contentType == "" {
		contentType = strings.TrimSpace(raw.ContentType2)
	}
	return novaChatroomPost{
		ID:          id,
		TopicID:     topicID,
		Author:      author,
		CreatedAt:   createdAt,
		ContentType: contentType,
		Content:     raw.Content,
	}, true
}

func novaChatroomPositiveInt(raw json.RawMessage) (int64, bool) {
	value, ok := novaChatroomNonNegativeInt(raw)
	return value, ok && value > 0
}

func novaChatroomNonNegativeInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var number json.Number
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return 0, false
		}
		number = json.Number(strings.TrimSpace(text))
	} else {
		number = json.Number(strings.TrimSpace(string(raw)))
	}
	value, err := number.Int64()
	return value, err == nil && value >= 0
}

func novaChatroomString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var object map[string]interface{}
	if json.Unmarshal(raw, &object) == nil {
		for _, key := range []string{"name", "robotId", "username", "id"} {
			if value := strings.TrimSpace(fmt.Sprint(object[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

var novaChatroomQueryStopPhrases = []string{
	"chatroom", "nova", "硅基聊天室", "聊天室", "微信公众号", "公众号",
	"有一个", "有一篇", "有篇", "里面", "其中", "关于", "帖子", "原帖", "文章",
	"我希望你", "希望你", "请你", "麻烦你", "帮我", "给我", "我想让你",
	"提取出来", "提取出", "提取", "整理成", "整理", "改写成", "改写", "重写",
	"内容", "然后", "一个", "我去", "吸粉", "发布", "用于",
}

func stripNovaChatroomQueryInstructions(value string) string {
	value = strings.ToLower(value)
	for _, phrase := range novaChatroomQueryStopPhrases {
		value = strings.ReplaceAll(value, phrase, " ")
	}
	return value
}

func normalizeNovaChatroomSearchText(value string) string {
	value = stripNovaChatroomQueryInstructions(value)
	var builder strings.Builder
	for _, r := range value {
		if r == '的' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

func novaChatroomQueryTerms(query string) []string {
	cleaned := stripNovaChatroomQueryInstructions(query)
	fields := strings.FieldsFunc(cleaned, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '+')
	})
	seen := make(map[string]bool)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		term := normalizeNovaChatroomSearchText(field)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func scoreNovaChatroomPost(post novaChatroomPost, query string) int {
	content := strings.TrimSpace(post.Content)
	if content == "" {
		return 0
	}
	title := content
	if newline := strings.IndexByte(title, '\n'); newline >= 0 {
		title = title[:newline]
	}
	normalizedQuery := normalizeNovaChatroomSearchText(query)
	normalizedTitle := normalizeNovaChatroomSearchText(title)
	normalizedContent := normalizeNovaChatroomSearchText(content)
	if normalizedQuery == "" || normalizedContent == "" {
		return 0
	}
	score := 0
	phraseMatch := false
	if strings.Contains(normalizedTitle, normalizedQuery) {
		score += 300
		phraseMatch = true
	} else if strings.Contains(normalizedContent, normalizedQuery) {
		score += 200
		phraseMatch = true
	}
	terms := novaChatroomQueryTerms(query)
	matchedContent := 0
	matchedTitle := 0
	distinctiveTitleMatch := false
	for _, term := range terms {
		if strings.Contains(normalizedContent, term) {
			matchedContent++
		}
		if strings.Contains(normalizedTitle, term) {
			matchedTitle++
			if novaChatroomDistinctiveTerm(term) {
				distinctiveTitleMatch = true
				score += 220
			}
		}
	}
	allTermsMatch := len(terms) > 0 && matchedContent == len(terms)
	if len(terms) > 0 && matchedTitle == len(terms) {
		score += 180 + len(terms)*10
	} else if allTermsMatch {
		score += 100 + len(terms)*10
	}
	// A partial token overlap is too noisy to select somebody else's source.
	if !phraseMatch && !allTermsMatch && !distinctiveTitleMatch {
		return 0
	}
	return score
}

func novaChatroomDistinctiveTerm(term string) bool {
	runeCount := utf8.RuneCountInString(term)
	hasHan := false
	for _, r := range term {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			break
		}
	}
	if hasHan {
		return runeCount >= 4
	}
	return runeCount >= 8
}

// novaChatroomPostLess reports whether a should rank before b.
func novaChatroomPostLess(a, b novaChatroomPost) bool {
	if a.searchScore != b.searchScore {
		return a.searchScore > b.searchScore
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	return a.ID > b.ID
}
