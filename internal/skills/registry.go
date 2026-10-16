package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	SourceTencent = "tencent"
	SourceClawHub = "clawhub"

	defaultTencentSkillAPI = "https://api.skillhub.cn"
	defaultClawHubSkillAPI = "https://clawhub.ai"

	maxRemoteSkillBytes   = 128 << 10
	maxSkillCatalogBytes  = 2 << 20
	maxRemoteSkillResults = 12
)

var (
	ErrRemoteSkillInvalidInput     = errors.New("invalid remote skill request")
	ErrRemoteSkillAlreadyInstalled = errors.New("remote skill is already installed")
	ErrRemoteSkillUnsafe           = errors.New("remote skill was flagged by its source")
	ErrRemoteSkillUpstream         = errors.New("remote skill source request failed")

	ValidRemoteSkillSlug    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	ValidRemoteSkillOwner   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,62}[A-Za-z0-9])?$`)
	validRemoteSkillVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}$`)
)

type RemoteSkillSourceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Homepage    string `json:"homepage"`
	Recommended bool   `json:"recommended"`
}

type RemoteSkillSearchResult struct {
	Source    string `json:"source"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	SummaryZH string `json:"summary_zh,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url"`
	Downloads int64  `json:"downloads,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
	Installed bool   `json:"installed"`
}

type RemoteSkillInstallInput struct {
	Source  string `json:"source"`
	Slug    string `json:"slug"`
	Owner   string `json:"owner,omitempty"`
	Version string `json:"version,omitempty"`
}

type RemoteSkillOrigin struct {
	Source      string    `json:"source"`
	Slug        string    `json:"slug"`
	Owner       string    `json:"owner,omitempty"`
	Version     string    `json:"version,omitempty"`
	URL         string    `json:"url,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	TextOnly    bool      `json:"text_only"`
}

type RemoteSkillDocument struct {
	Name        string
	Description string
	Content     string
	Origin      RemoteSkillOrigin
}

type RemoteSkillRegistryOptions struct {
	Client         *http.Client
	TencentAPIBase string
	ClawHubAPIBase string
}

// RemoteSkillRegistry discovers text-only skills from Tencent SkillHub and
// ClawHub. It never downloads packages, scripts, binaries, or dependencies.
type RemoteSkillRegistry struct {
	client     *http.Client
	tencentAPI string
	clawHubAPI string
}

func NewRemoteSkillRegistry(options ...RemoteSkillRegistryOptions) *RemoteSkillRegistry {
	option := RemoteSkillRegistryOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	if option.Client == nil {
		option.Client = secureRemoteSkillHTTPClient()
	}
	if strings.TrimSpace(option.TencentAPIBase) == "" {
		option.TencentAPIBase = defaultTencentSkillAPI
	}
	if strings.TrimSpace(option.ClawHubAPIBase) == "" {
		option.ClawHubAPIBase = defaultClawHubSkillAPI
	}
	return &RemoteSkillRegistry{
		client:     option.Client,
		tencentAPI: strings.TrimRight(option.TencentAPIBase, "/"),
		clawHubAPI: strings.TrimRight(option.ClawHubAPIBase, "/"),
	}
}

func (r *RemoteSkillRegistry) Sources() []RemoteSkillSourceInfo {
	return []RemoteSkillSourceInfo{
		{
			ID:          SourceTencent,
			Name:        "腾讯 SkillHub",
			Homepage:    "https://skillhub.cn",
			Recommended: true,
		},
		{
			ID:       SourceClawHub,
			Name:     "OpenClaw / ClawHub",
			Homepage: "https://clawhub.ai",
		},
	}
}

func (r *RemoteSkillRegistry) Search(ctx context.Context, source, query string) ([]RemoteSkillSearchResult, error) {
	source = NormalizeRemoteSkillSource(source)
	query = strings.TrimSpace(query)
	if !KnownRemoteSkillSource(source) || query == "" || len([]rune(query)) > 100 {
		return nil, fmt.Errorf("%w: choose a source and enter 1-100 search characters", ErrRemoteSkillInvalidInput)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if source == SourceTencent {
		return r.searchTencent(requestCtx, query)
	}
	return r.searchClawHub(requestCtx, query)
}

func (r *RemoteSkillRegistry) Fetch(ctx context.Context, input RemoteSkillInstallInput) (RemoteSkillDocument, error) {
	normalized, err := NormalizeRemoteSkillInstallInput(input)
	if err != nil {
		return RemoteSkillDocument{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if normalized.Source == SourceTencent {
		return r.fetchTencent(requestCtx, normalized)
	}
	return r.fetchClawHub(requestCtx, normalized)
}

func NormalizeRemoteSkillInstallInput(input RemoteSkillInstallInput) (RemoteSkillInstallInput, error) {
	input.Source = NormalizeRemoteSkillSource(input.Source)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Owner = strings.TrimPrefix(strings.TrimSpace(input.Owner), "@")
	input.Version = strings.TrimSpace(input.Version)
	if !KnownRemoteSkillSource(input.Source) || !ValidRemoteSkillSlug.MatchString(input.Slug) {
		return RemoteSkillInstallInput{}, fmt.Errorf("%w: source or slug is invalid", ErrRemoteSkillInvalidInput)
	}
	if input.Source == SourceClawHub && !ValidRemoteSkillOwner.MatchString(input.Owner) {
		return RemoteSkillInstallInput{}, fmt.Errorf("%w: ClawHub owner is required", ErrRemoteSkillInvalidInput)
	}
	if input.Version != "" && !validRemoteSkillVersion.MatchString(input.Version) {
		return RemoteSkillInstallInput{}, fmt.Errorf("%w: version is invalid", ErrRemoteSkillInvalidInput)
	}
	return input, nil
}

func NormalizeRemoteSkillSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return SourceTencent
	}
	if source == "openclaw" {
		return SourceClawHub
	}
	return source
}

func KnownRemoteSkillSource(source string) bool {
	return source == SourceTencent || source == SourceClawHub
}

func (r *RemoteSkillRegistry) searchTencent(ctx context.Context, query string) ([]RemoteSkillSearchResult, error) {
	values := url.Values{
		"page":     {"1"},
		"pageSize": {fmt.Sprintf("%d", maxRemoteSkillResults)},
		"keyword":  {query},
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Skills []struct {
				Name          string `json:"name"`
				Slug          string `json:"slug"`
				Description   string `json:"description"`
				DescriptionZH string `json:"description_zh"`
				Downloads     int64  `json:"downloads"`
				Version       string `json:"version"`
				Verified      bool   `json:"verified"`
				OwnerName     string `json:"ownerName"`
				Namespace     struct {
					Handle string `json:"handle"`
				} `json:"namespace"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := r.getJSON(ctx, r.tencentAPI+"/api/skills?"+values.Encode(), &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "catalog returned a non-zero status"
		}
		return nil, fmt.Errorf("%w: Tencent SkillHub: %s", ErrRemoteSkillUpstream, message)
	}
	results := make([]RemoteSkillSearchResult, 0, len(payload.Data.Skills))
	for _, item := range payload.Data.Skills {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		if !ValidRemoteSkillSlug.MatchString(slug) {
			continue
		}
		owner := strings.TrimSpace(item.Namespace.Handle)
		if owner == "" {
			owner = strings.TrimSpace(item.OwnerName)
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = slug
		}
		results = append(results, RemoteSkillSearchResult{
			Source:    SourceTencent,
			Slug:      slug,
			Name:      name,
			Summary:   strings.TrimSpace(item.Description),
			SummaryZH: strings.TrimSpace(item.DescriptionZH),
			Owner:     owner,
			Version:   strings.TrimSpace(item.Version),
			URL:       "https://skillhub.cn/skills/" + url.PathEscape(slug),
			Downloads: item.Downloads,
			Verified:  item.Verified,
		})
	}
	return results, nil
}

func (r *RemoteSkillRegistry) searchClawHub(ctx context.Context, query string) ([]RemoteSkillSearchResult, error) {
	values := url.Values{"q": {query}, "nonSuspiciousOnly": {"true"}}
	var payload struct {
		Results []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"displayName"`
			Summary     string `json:"summary"`
			Version     string `json:"version"`
			Downloads   int64  `json:"downloads"`
			OwnerHandle string `json:"ownerHandle"`
		} `json:"results"`
	}
	if err := r.getJSON(ctx, r.clawHubAPI+"/api/v1/search?"+values.Encode(), &payload); err != nil {
		return nil, err
	}
	results := make([]RemoteSkillSearchResult, 0, len(payload.Results))
	for _, item := range payload.Results {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		owner := strings.TrimSpace(item.OwnerHandle)
		if !ValidRemoteSkillSlug.MatchString(slug) || !ValidRemoteSkillOwner.MatchString(owner) {
			continue
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = slug
		}
		results = append(results, RemoteSkillSearchResult{
			Source:    SourceClawHub,
			Slug:      slug,
			Name:      name,
			Summary:   strings.TrimSpace(item.Summary),
			Owner:     owner,
			Version:   strings.TrimSpace(item.Version),
			URL:       "https://clawhub.ai/" + url.PathEscape(owner) + "/skills/" + url.PathEscape(slug),
			Downloads: item.Downloads,
		})
	}
	return results, nil
}

func (r *RemoteSkillRegistry) fetchTencent(ctx context.Context, input RemoteSkillInstallInput) (RemoteSkillDocument, error) {
	var detail struct {
		LatestVersion struct {
			Version string `json:"version"`
		} `json:"latestVersion"`
		Namespace struct {
			Handle string `json:"handle"`
		} `json:"namespace"`
		Skill struct {
			DisplayName string `json:"displayName"`
			Summary     string `json:"summary"`
			SummaryZH   string `json:"summary_zh"`
		} `json:"skill"`
		SecurityReports map[string]struct {
			Status     string `json:"status"`
			StatusText string `json:"statusText"`
		} `json:"securityReports"`
	}
	detailURL := r.tencentAPI + "/api/v1/skills/" + url.PathEscape(input.Slug)
	if err := r.getJSON(ctx, detailURL, &detail); err != nil {
		return RemoteSkillDocument{}, err
	}
	for scanner, report := range detail.SecurityReports {
		if unsafeRemoteSkillStatus(report.Status) || unsafeRemoteSkillStatus(report.StatusText) {
			return RemoteSkillDocument{}, fmt.Errorf(
				"%w: Tencent scanner %s reported %s",
				ErrRemoteSkillUnsafe,
				scanner,
				strings.TrimSpace(firstRemoteNonEmpty(report.StatusText, report.Status)),
			)
		}
	}
	latestVersion := strings.TrimSpace(detail.LatestVersion.Version)
	if input.Version != "" && latestVersion != "" && input.Version != latestVersion {
		return RemoteSkillDocument{}, fmt.Errorf(
			"%w: Tencent SkillHub version changed; search again before installing",
			ErrRemoteSkillInvalidInput,
		)
	}
	version := latestVersion
	if version == "" {
		version = input.Version
	}
	values := url.Values{"path": {"SKILL.md"}}
	if version != "" {
		values.Set("version", version)
	}
	raw, err := r.getSkillText(ctx, detailURL+"/file?"+values.Encode())
	if err != nil {
		return RemoteSkillDocument{}, err
	}
	description := firstRemoteNonEmpty(
		detail.Skill.SummaryZH,
		detail.Skill.Summary,
		detail.Skill.DisplayName,
		"从腾讯 SkillHub 导入的提示型 Skill。",
	)
	owner := strings.TrimSpace(detail.Namespace.Handle)
	name, content, normalizedDescription, err := prepareRemoteSkillDocument(raw, input.Slug, description, version, owner)
	if err != nil {
		return RemoteSkillDocument{}, err
	}
	return RemoteSkillDocument{
		Name:        name,
		Description: normalizedDescription,
		Content:     content,
		Origin: RemoteSkillOrigin{
			Source:      SourceTencent,
			Slug:        input.Slug,
			Owner:       owner,
			Version:     version,
			URL:         "https://skillhub.cn/skills/" + url.PathEscape(input.Slug),
			InstalledAt: time.Now().UTC(),
			TextOnly:    true,
		},
	}, nil
}

func (r *RemoteSkillRegistry) fetchClawHub(ctx context.Context, input RemoteSkillInstallInput) (RemoteSkillDocument, error) {
	values := url.Values{"ownerHandle": {input.Owner}}
	detailURL := r.clawHubAPI + "/api/v1/skills/" + url.PathEscape(input.Slug)
	var detail struct {
		LatestVersion struct {
			Version string `json:"version"`
		} `json:"latestVersion"`
		Owner struct {
			Handle string `json:"handle"`
		} `json:"owner"`
		Skill struct {
			DisplayName string `json:"displayName"`
			Summary     string `json:"summary"`
			Description string `json:"description"`
		} `json:"skill"`
	}
	if err := r.getJSON(ctx, detailURL+"?"+values.Encode(), &detail); err != nil {
		return RemoteSkillDocument{}, err
	}
	version := input.Version
	if version == "" {
		version = strings.TrimSpace(detail.LatestVersion.Version)
	}
	scanValues := url.Values{"ownerHandle": {input.Owner}}
	if version != "" {
		scanValues.Set("version", version)
	}
	var scan struct {
		Security struct {
			Status string `json:"status"`
		} `json:"security"`
	}
	if err := r.getJSON(ctx, detailURL+"/scan?"+scanValues.Encode(), &scan); err != nil {
		return RemoteSkillDocument{}, err
	}
	if unsafeRemoteSkillStatus(scan.Security.Status) {
		return RemoteSkillDocument{}, fmt.Errorf(
			"%w: ClawHub scan status is %s",
			ErrRemoteSkillUnsafe,
			strings.TrimSpace(scan.Security.Status),
		)
	}
	fileValues := url.Values{"path": {"SKILL.md"}, "ownerHandle": {input.Owner}}
	if version != "" {
		fileValues.Set("version", version)
	}
	raw, err := r.getSkillText(ctx, detailURL+"/file?"+fileValues.Encode())
	if err != nil {
		return RemoteSkillDocument{}, err
	}
	description := firstRemoteNonEmpty(
		detail.Skill.Summary,
		detail.Skill.Description,
		detail.Skill.DisplayName,
		"从 OpenClaw / ClawHub 导入的提示型 Skill。",
	)
	owner := strings.TrimSpace(detail.Owner.Handle)
	if owner == "" {
		owner = input.Owner
	}
	name, content, normalizedDescription, err := prepareRemoteSkillDocument(raw, input.Slug, description, version, owner)
	if err != nil {
		return RemoteSkillDocument{}, err
	}
	return RemoteSkillDocument{
		Name:        name,
		Description: normalizedDescription,
		Content:     content,
		Origin: RemoteSkillOrigin{
			Source:      SourceClawHub,
			Slug:        input.Slug,
			Owner:       owner,
			Version:     version,
			URL:         "https://clawhub.ai/" + url.PathEscape(owner) + "/skills/" + url.PathEscape(input.Slug),
			InstalledAt: time.Now().UTC(),
			TextOnly:    true,
		},
	}, nil
}

type safeRemoteSkillFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Type        string   `yaml:"type"`
	Version     string   `yaml:"version,omitempty"`
	Author      string   `yaml:"author,omitempty"`
	Aliases     []string `yaml:"aliases,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	ReadWhen    []string `yaml:"read_when,omitempty"`
}

// prepareRemoteSkillDocument deliberately rewrites frontmatter. Remote
// executable types, entrypoints, dependency installers, and always-on flags
// are discarded so a marketplace import remains prompt-only.
func prepareRemoteSkillDocument(raw, fallbackName, fallbackDescription, fallbackVersion, fallbackAuthor string) (string, string, string, error) {
	normalized := strings.TrimPrefix(strings.ReplaceAll(raw, "\r\n", "\n"), "\uFEFF")
	if !utf8.ValidString(normalized) || strings.IndexByte(normalized, 0) >= 0 {
		return "", "", "", fmt.Errorf("%w: SKILL.md must be UTF-8 text", ErrRemoteSkillInvalidInput)
	}
	if len(normalized) > maxRemoteSkillBytes {
		return "", "", "", fmt.Errorf("%w: SKILL.md exceeds %d bytes", ErrRemoteSkillInvalidInput, maxRemoteSkillBytes)
	}

	var (
		metadata safeRemoteSkillFrontmatter
		body     string
	)
	frontmatter, splitBody, hasFrontmatter := splitSkillFrontmatter(normalized)
	if hasFrontmatter {
		if err := yaml.Unmarshal([]byte(frontmatter), &metadata); err != nil {
			return "", "", "", fmt.Errorf("%w: invalid SKILL.md frontmatter: %v", ErrRemoteSkillInvalidInput, err)
		}
		body = splitBody
	} else {
		body = normalized
		if !strings.HasPrefix(strings.TrimSpace(body), "#") {
			return "", "", "", fmt.Errorf(
				"%w: legacy SKILL.md without frontmatter must begin with a Markdown heading",
				ErrRemoteSkillInvalidInput,
			)
		}
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", "", fmt.Errorf("%w: SKILL.md has no instruction body", ErrRemoteSkillInvalidInput)
	}

	name := strings.ToLower(strings.TrimSpace(metadata.Name))
	if !ValidRemoteSkillSlug.MatchString(name) {
		name = strings.ToLower(strings.TrimSpace(fallbackName))
	}
	if !ValidRemoteSkillSlug.MatchString(name) {
		return "", "", "", fmt.Errorf("%w: SKILL.md name is invalid", ErrRemoteSkillInvalidInput)
	}
	description := strings.TrimSpace(metadata.Description)
	if description == "" {
		description = strings.TrimSpace(fallbackDescription)
	}
	if description == "" || len([]rune(description)) > 2000 {
		return "", "", "", fmt.Errorf("%w: SKILL.md description is missing or too long", ErrRemoteSkillInvalidInput)
	}
	version := strings.TrimSpace(metadata.Version)
	if version == "" {
		version = strings.TrimSpace(fallbackVersion)
	}
	if version != "" && !validRemoteSkillVersion.MatchString(version) {
		version = ""
	}
	author := strings.TrimSpace(metadata.Author)
	if author == "" {
		author = strings.TrimSpace(fallbackAuthor)
	}
	if len([]rune(author)) > 256 {
		author = ""
	}

	safeMetadata := safeRemoteSkillFrontmatter{
		Name:        name,
		Description: description,
		Type:        "builtin",
		Version:     version,
		Author:      author,
		Aliases:     sanitizeRemoteMetadataStrings(metadata.Aliases),
		Tags:        sanitizeRemoteMetadataStrings(metadata.Tags),
		ReadWhen:    sanitizeRemoteMetadataStrings(metadata.ReadWhen),
	}
	encodedMetadata, err := yaml.Marshal(safeMetadata)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: normalize SKILL.md metadata: %v", ErrRemoteSkillInvalidInput, err)
	}
	content := "---\n" + string(encodedMetadata) + "---\n\n" + body + "\n"
	if len(content) > maxRemoteSkillBytes {
		return "", "", "", fmt.Errorf("%w: normalized SKILL.md exceeds %d bytes", ErrRemoteSkillInvalidInput, maxRemoteSkillBytes)
	}
	return name, content, description, nil
}

func sanitizeRemoteMetadataStrings(values []string) []string {
	const (
		maxValues = 32
		maxRunes  = 256
	)
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > maxRunes || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == maxValues {
			break
		}
	}
	sort.Strings(result)
	return result
}

func (r *RemoteSkillRegistry) getJSON(ctx context.Context, rawURL string, target interface{}) error {
	data, err := r.get(ctx, rawURL, maxSkillCatalogBytes, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: decode catalog response: %v", ErrRemoteSkillUpstream, err)
	}
	return nil
}

func (r *RemoteSkillRegistry) getSkillText(ctx context.Context, rawURL string) (string, error) {
	data, err := r.get(ctx, rawURL, maxRemoteSkillBytes, "text/markdown, text/plain;q=0.9")
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", fmt.Errorf("%w: SKILL.md must be UTF-8 text", ErrRemoteSkillInvalidInput)
	}
	return string(data), nil
}

func (r *RemoteSkillRegistry) get(ctx context.Context, rawURL string, limit int64, accept string) ([]byte, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("%w: HTTP client is unavailable", ErrRemoteSkillUpstream)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build source request: %v", ErrRemoteSkillUpstream, err)
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "OpenKitty/1.0 text-skill-importer")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRemoteSkillUpstream, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		messageText := strings.Join(strings.Fields(string(message)), " ")
		if messageText == "" {
			messageText = response.Status
		}
		return nil, fmt.Errorf("%w: %s", ErrRemoteSkillUpstream, messageText)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("%w: source response exceeds %d bytes", ErrRemoteSkillUpstream, limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read source response: %v", ErrRemoteSkillUpstream, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: source response exceeds %d bytes", ErrRemoteSkillUpstream, limit)
	}
	return data, nil
}

func unsafeRemoteSkillStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "benign" || status == "clean" || status == "safe" || strings.Contains(status, "无风险") {
		return false
	}
	for _, marker := range []string{
		"suspicious", "malicious", "blocked", "danger", "unsafe", "virus",
		"风险", "恶意", "危险", "木马",
	} {
		if strings.Contains(status, marker) {
			return true
		}
	}
	return false
}

func firstRemoteNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func secureRemoteSkillHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			host := strings.ToLower(request.URL.Hostname())
			allowedHost := host == "api.skillhub.cn" ||
				host == "api.skillhub.tencent.com" ||
				host == "clawhub.ai" ||
				strings.HasSuffix(host, ".myqcloud.com") ||
				strings.HasSuffix(host, ".tencentcos.cn")
			if request.URL.Scheme != "https" || !allowedHost {
				return fmt.Errorf("skill source redirected to an unapproved host %q", host)
			}
			return nil
		},
	}
}
